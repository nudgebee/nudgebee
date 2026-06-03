package aws

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/google/uuid"

	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/providers"
)

// CfnCustomResourceEvent represents a CloudFormation Custom Resource event
// received via SNS → SQS when a StackSet deploys to an org member account.
type CfnCustomResourceEvent struct {
	RequestType        string                    `json:"RequestType"` // Create, Update, Delete
	ResponseURL        string                    `json:"ResponseURL"`
	StackId            string                    `json:"StackId"`
	RequestId          string                    `json:"RequestId"`
	ResourceType       string                    `json:"ResourceType"`
	LogicalResourceId  string                    `json:"LogicalResourceId"`
	PhysicalResourceId string                    `json:"PhysicalResourceId,omitempty"`
	ResourceProperties CfnRegistrationProperties `json:"ResourceProperties"`
}

// CfnRegistrationProperties contains the properties passed from the CF template's
// RegistrationResource Custom Resource.
type CfnRegistrationProperties struct {
	ServiceToken        string `json:"ServiceToken"`
	RoleArn             string `json:"RoleArn"`
	AccountNumber       string `json:"AccountNumber"`
	TenantId            string `json:"TenantId"`
	VerificationToken   string `json:"VerificationToken"`
	ExternalId          string `json:"ExternalId"`  // non-empty = single-account flow
	AccountName         string `json:"AccountName"` // user-provided display name (single-account)
	UserId              string `json:"UserId"`      // user who initiated onboarding (single-account)
	CurBucketName       string `json:"CurBucketName"`
	CurBucketArn        string `json:"CurBucketArn"`
	CurReportName       string `json:"CurReportName"`
	CurS3Prefix         string `json:"CurS3Prefix"`
	CurS3Region         string `json:"CurS3Region"`
	CurCompression      string `json:"CurCompression"`
	CurTimeUnit         string `json:"CurTimeUnit"`
	CurReportVersioning string `json:"CurReportVersioning"`
	CurFormat           string `json:"CurFormat"`
	TemplateVersion     string `json:"TemplateVersion"`
}

// CfnResponse is the response sent back to CloudFormation via the ResponseURL.
type CfnResponse struct {
	Status             string            `json:"Status"`
	Reason             string            `json:"Reason"`
	PhysicalResourceId string            `json:"PhysicalResourceId"`
	StackId            string            `json:"StackId"`
	RequestId          string            `json:"RequestId"`
	LogicalResourceId  string            `json:"LogicalResourceId"`
	Data               map[string]string `json:"Data"`
}

// tenantInfo holds the tenant data needed for org registration verification.
type tenantInfo struct {
	TenantID  string
	CreatedBy string
}

// StartOrgRegistrationSQSConsumer continuously polls an SQS queue for
// CloudFormation Custom Resource events (org member account registration)
// and processes them. Designed to run as a long-running goroutine.
func StartOrgRegistrationSQSConsumer(pCtx providers.CloudProviderContext) {
	defer func() {
		if r := recover(); r != nil {
			pCtx.GetLogger().Error("org registration SQS consumer panicked", "error", r)
		}
	}()

	queueIdentifier := config.Config.CloudCollectorOrgRegistrationSqs
	logger := pCtx.GetLogger()

	if queueIdentifier == "" {
		logger.Warn("aws: org registration SQS queue URL is not configured (CloudCollectorOrgRegistrationSqs). Consumer will not start.")
		return
	}

	logger.Info("aws: starting org registration SQS consumer", "queueIdentifier", queueIdentifier, "component", "OrgRegistrationSQSConsumer")

	awsCfg, err := awsconfig.LoadDefaultConfig(pCtx.GetContext())
	if err != nil {
		logger.Error("aws: failed to load AWS config for org registration SQS consumer", "error", err)
		return
	}

	// Resolve queue URL (supports both ARN and URL formats)
	var sqsRegion string
	var finalQueueURL string

	if strings.HasPrefix(queueIdentifier, "arn:aws:sqs:") {
		_, parsedRegion, parsedAccount, _, parsedQueueName := parseARN(queueIdentifier)
		if parsedRegion == "" || parsedQueueName == "" {
			logger.Error("aws: invalid SQS ARN format for org registration", "arn", queueIdentifier)
			return
		}
		sqsRegion = parsedRegion

		sqsCfg := awsCfg.Copy()
		sqsCfg.Region = sqsRegion
		sqsSvc := sqs.NewFromConfig(sqsCfg)

		getQueueUrlInput := &sqs.GetQueueUrlInput{QueueName: aws.String(parsedQueueName)}
		if parsedAccount != "" {
			getQueueUrlInput.QueueOwnerAWSAccountId = aws.String(parsedAccount)
		}
		queueUrlOutput, err := sqsSvc.GetQueueUrl(pCtx.GetContext(), getQueueUrlInput)
		if err != nil {
			logger.Error("aws: failed to get org registration SQS queue URL from ARN", "arn", queueIdentifier, "error", err)
			return
		}
		finalQueueURL = *queueUrlOutput.QueueUrl
	} else {
		finalQueueURL = queueIdentifier
		urlParts := strings.Split(finalQueueURL, ".")
		if len(urlParts) > 2 && strings.HasPrefix(urlParts[0], "https://sqs") {
			sqsRegion = urlParts[1]
		}
	}

	if sqsRegion == "" {
		if awsCfg.Region != "" {
			sqsRegion = awsCfg.Region
		} else {
			logger.Error("aws: SQS region cannot be determined for org registration consumer")
			return
		}
	}

	// Set region on awsCfg so downstream handlers (validateAssumeRole, etc.) inherit it
	awsCfg.Region = sqsRegion

	sqsCfg := awsCfg.Copy()
	sqsCfg.Region = sqsRegion
	sqsSvc := sqs.NewFromConfig(sqsCfg)

	logger.Info("aws: org registration SQS consumer started", "queueURL", finalQueueURL, "region", sqsRegion)

	for {
		select {
		case <-pCtx.GetContext().Done():
			logger.Info("aws: org registration SQS consumer shutting down", "queueURL", finalQueueURL)
			return
		default:
			resp, err := sqsSvc.ReceiveMessage(pCtx.GetContext(), &sqs.ReceiveMessageInput{
				QueueUrl:            aws.String(finalQueueURL),
				MaxNumberOfMessages: int32(10),
				WaitTimeSeconds:     int32(20),
			})
			if err != nil {
				if pCtx.GetContext().Err() == context.Canceled || pCtx.GetContext().Err() == context.DeadlineExceeded {
					logger.Info("aws: org registration SQS consumer context done during ReceiveMessage")
					return
				}
				logger.Error("aws: error receiving org registration messages from SQS", "error", err)
				time.Sleep(5 * time.Second)
				continue
			}

			if len(resp.Messages) == 0 {
				continue
			}

			logger.Info("aws: received org registration messages from SQS", "count", len(resp.Messages))

			for _, msg := range resp.Messages {
				if msg.Body == nil {
					deleteOrgSQSMessage(pCtx, sqsSvc, finalQueueURL, msg.ReceiptHandle, msg.MessageId)
					continue
				}

				processOrgRegistrationMessage(pCtx, *msg.Body, awsCfg)

				deleteOrgSQSMessage(pCtx, sqsSvc, finalQueueURL, msg.ReceiptHandle, msg.MessageId)
			}
		}
	}
}

// deleteOrgSQSMessage deletes a message from the org registration SQS queue.
func deleteOrgSQSMessage(pCtx providers.CloudProviderContext, sqsSvc *sqs.Client, queueURL string, receiptHandle *string, messageId *string) {
	if _, delErr := sqsSvc.DeleteMessage(pCtx.GetContext(), &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(queueURL),
		ReceiptHandle: receiptHandle,
	}); delErr != nil {
		pCtx.GetLogger().Error("aws: failed to delete org registration SQS message", "messageId", aws.ToString(messageId), "error", delErr)
	}
}

// processOrgRegistrationMessage handles a single SNS-wrapped CF Custom Resource event.
func processOrgRegistrationMessage(pCtx providers.CloudProviderContext, messageBody string, awsCfg aws.Config) {
	logger := pCtx.GetLogger()

	// Step 1: Parse SNS wrapper
	var snsPayload SNSNotificationPayload
	if err := json.Unmarshal([]byte(messageBody), &snsPayload); err != nil {
		logger.Error("aws: failed to parse org registration SNS payload", "error", err)
		return
	}

	// The actual CF Custom Resource event is in the SNS Message field
	cfEventBody := snsPayload.Message
	if cfEventBody == "" {
		// If not an SNS wrapper, try parsing directly as CF event
		cfEventBody = messageBody
	}

	// Step 2: Parse CF Custom Resource event
	var cfEvent CfnCustomResourceEvent
	if err := json.Unmarshal([]byte(cfEventBody), &cfEvent); err != nil {
		logger.Error("aws: failed to parse CF Custom Resource event", "error", err)
		return
	}

	props := cfEvent.ResourceProperties
	logger.Info("aws: processing org registration CF event",
		"requestType", cfEvent.RequestType,
		"accountNumber", props.AccountNumber,
		"tenantId", props.TenantId,
	)

	// Step 3: Look up tenant and verify token
	tenant, err := lookupTenant(props.TenantId)
	if err != nil {
		logger.Error("aws: tenant lookup failed", "error", err, "tenantId", props.TenantId)
		sendCfnResponse(cfEvent, "FAILED", "Tenant not found", props.AccountNumber)
		return
	}

	// Route between single-account and org flows based on OnboardRequestId
	isSingleAccount := props.ExternalId != ""

	switch cfEvent.RequestType {
	case "Create":
		if isSingleAccount {
			if err := verifySingleAccountToken(props.VerificationToken, tenant.TenantID, props.ExternalId); err != nil {
				logger.Error("aws: single-account verification token mismatch", "error", err, "tenantId", props.TenantId, "requestId", props.ExternalId)
				sendCfnResponse(cfEvent, "FAILED", "Verification token invalid", props.AccountNumber)
				return
			}
			err = handleSingleAccountCreate(pCtx, cfEvent, props, tenant, awsCfg)
		} else {
			if err := verifyOrgToken(props.VerificationToken, tenant.TenantID); err != nil {
				logger.Error("aws: org verification token mismatch", "error", err, "tenantId", props.TenantId)
				sendCfnResponse(cfEvent, "FAILED", "Verification token invalid", props.AccountNumber)
				return
			}
			err = handleOrgAccountCreate(pCtx, cfEvent, props, tenant, awsCfg)
		}
	case "Update":
		if isSingleAccount {
			// Single-account tokens are one-time-use (deleted after successful Create).
			// On Update, verify ownership via the (account_number, tenant, external_id) triplet
			// which uniquely identifies the account created by this CF stack.
			if err := verifySingleAccountOwnership(props.AccountNumber, tenant.TenantID, props.ExternalId); err != nil {
				logger.Error("aws: single-account ownership verification failed on Update", "error", err, "tenantId", props.TenantId, "accountNumber", props.AccountNumber)
				sendCfnResponse(cfEvent, "FAILED", "Account ownership verification failed", props.AccountNumber)
				return
			}
			err = handleSingleAccountUpdate(pCtx, cfEvent, props, tenant, awsCfg)
		} else {
			if err := verifyOrgToken(props.VerificationToken, tenant.TenantID); err != nil {
				logger.Error("aws: org verification token mismatch", "error", err, "tenantId", props.TenantId)
				sendCfnResponse(cfEvent, "FAILED", "Verification token invalid", props.AccountNumber)
				return
			}
			err = handleOrgAccountCreate(pCtx, cfEvent, props, tenant, awsCfg)
		}
	case "Delete":
		// No token verification for Delete — the single-account token is consumed after Create,
		// and the event arrives through our trusted SNS→SQS pipeline. Account lookup by
		// account_number + tenant is sufficient authentication.
		err = handleOrgAccountDelete(pCtx, props, tenant)
	default:
		logger.Warn("aws: unknown CF RequestType for registration", "requestType", cfEvent.RequestType)
		sendCfnResponse(cfEvent, "SUCCESS", "Unknown request type, ignoring", props.AccountNumber)
		return
	}

	if err != nil {
		logger.Error("aws: registration processing failed", "error", err, "accountNumber", props.AccountNumber, "singleAccount", isSingleAccount)
		sendCfnResponse(cfEvent, "FAILED", fmt.Sprintf("Failed to process: %v", err), props.AccountNumber)
		return
	}

	// Best-effort: update agent status with CF stack info for Create/Update
	if cfEvent.RequestType == "Create" || cfEvent.RequestType == "Update" {
		if accountId, lookupErr := lookupAccountId(props.AccountNumber, tenant.TenantID); lookupErr == nil {
			updateAgentStatusFromCfEvent(cfEvent, accountId, tenant.TenantID, logger)
		} else {
			logger.Warn("aws: could not look up account for agent status update", "error", lookupErr)
		}
	}

	sendCfnResponse(cfEvent, "SUCCESS", "Account registered successfully", props.AccountNumber)
}

// handleOrgAccountCreate processes a Create/Update request from CF Custom Resource.
func handleOrgAccountCreate(
	pCtx providers.CloudProviderContext,
	cfEvent CfnCustomResourceEvent,
	props CfnRegistrationProperties,
	tenant tenantInfo,
	awsCfg aws.Config,
) error {
	logger := pCtx.GetLogger()

	// Step 1: Validate role via STS AssumeRole
	accountNumber, err := validateAssumeRole(pCtx.GetContext(), awsCfg, props.RoleArn)
	if err != nil {
		return fmt.Errorf("failed to validate assume role: %w", err)
	}

	// Use the account number from STS if available, otherwise from props
	if accountNumber == "" {
		accountNumber = props.AccountNumber
	}

	logger.Info("aws: validated org member account", "accountNumber", accountNumber, "roleArn", props.RoleArn)

	// Step 2: Check if account already exists (idempotency)
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database manager: %w", err)
	}

	var existingAccountId string
	row, err := dbms.QueryRow(
		"SELECT id FROM cloud_accounts WHERE account_number = $1 AND tenant = $2 ORDER BY created_at DESC LIMIT 1",
		accountNumber, tenant.TenantID,
	)
	if err == nil {
		if scanErr := row.Scan(&existingAccountId); scanErr != nil && scanErr.Error() != "sql: no rows in result set" {
			return fmt.Errorf("failed to scan existing account: %w", scanErr)
		}
	}

	// Build CUR data JSON for account.data
	curData := map[string]interface{}{
		"cost_report_s3_bucket":   props.CurBucketName,
		"cost_report_name":        props.CurReportName,
		"cost_report_s3_prefix":   props.CurS3Prefix + "/" + props.CurReportName,
		"cost_report_s3_region":   props.CurS3Region,
		"cost_report_compression": props.CurCompression,
		"cost_report_time_unit":   props.CurTimeUnit,
		"cost_report_versioning":  props.CurReportVersioning,
		"cost_report_format":      props.CurFormat,
		"cur_source":              "org_callback",
	}
	curDataJSON, err := json.Marshal(curData)
	if err != nil {
		return fmt.Errorf("failed to marshal CUR data: %w", err)
	}

	externalId := uuid.New().String()

	if existingAccountId != "" {
		// Update existing account with CUR data and org role
		logger.Info("aws: updating existing account with org membership", "accountNumber", accountNumber, "existingId", existingAccountId)
		_, err = dbms.Exec(
			`UPDATE cloud_accounts SET data = $1, assume_role = $2, updated_at = NOW() WHERE id = $3`,
			string(curDataJSON), props.RoleArn, existingAccountId,
		)
		if err != nil {
			return fmt.Errorf("failed to update existing account: %w", err)
		}

		// Upsert cloud_account_attrs for aws_org_type = member
		upsertOrgTypeAttr(dbms, existingAccountId, "member", logger)

		// Trigger initial data load
		triggerStoreUsageReport(existingAccountId, tenant.TenantID, false, logger)

		return nil
	}

	// Step 3: Create new cloud_accounts entry
	newAccountId := uuid.New().String()
	accountName := fmt.Sprintf("AWS-%s", accountNumber)

	// Use the actual onboarding user if available, fall back to tenant creator
	createdBy := tenant.CreatedBy
	if props.UserId != "" {
		createdBy = props.UserId
	}

	_, err = dbms.Exec(
		`INSERT INTO cloud_accounts (
			id, cloud_provider, account_number, account_name, account_type,
			assume_role, external_id, tenant,
			created_by, updated_by, status, data, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW(), NOW())`,
		newAccountId,        // $1 id
		"AWS",               // $2 cloud_provider
		accountNumber,       // $3 account_number
		accountName,         // $4 account_name
		"cloud",             // $5 account_type
		props.RoleArn,       // $6 assume_role
		externalId,          // $7 external_id
		tenant.TenantID,     // $8 tenant
		createdBy,           // $9 created_by
		createdBy,           // $10 updated_by
		"active",            // $11 status
		string(curDataJSON), // $12 data
	)
	if err != nil {
		return fmt.Errorf("failed to insert cloud account: %w", err)
	}

	logger.Info("aws: created org member account", "accountId", newAccountId, "accountNumber", accountNumber)

	// Step 4: Upsert cloud_account_attrs for aws_org_type = member
	upsertOrgTypeAttr(dbms, newAccountId, "member", logger)

	// Step 5: Trigger initial data load (async, same as CreateAccount flow)
	triggerStoreUsageReport(newAccountId, tenant.TenantID, true, logger)

	return nil
}

// handleOrgAccountDelete processes a Delete request from CF Custom Resource.
func handleOrgAccountDelete(
	pCtx providers.CloudProviderContext,
	props CfnRegistrationProperties,
	tenant tenantInfo,
) error {
	logger := pCtx.GetLogger()

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database manager: %w", err)
	}

	_, err = dbms.Exec(
		`UPDATE cloud_accounts SET status = 'disabled', updated_at = NOW()
		WHERE account_number = $1 AND tenant = $2
		AND EXISTS (
			SELECT 1 FROM cloud_account_attrs
			WHERE cloud_account_id = cloud_accounts.id
			AND name = 'aws_org_type' AND value = 'member'
		)`,
		props.AccountNumber, tenant.TenantID,
	)
	if err != nil {
		return fmt.Errorf("failed to disable account: %w", err)
	}

	logger.Info("aws: disabled org member account", "accountNumber", props.AccountNumber)
	return nil
}

// lookupTenant fetches tenant data needed for org registration verification.
func lookupTenant(tenantId string) (tenantInfo, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return tenantInfo{}, fmt.Errorf("failed to get database manager: %w", err)
	}

	row, err := dbms.QueryRow(
		"SELECT id, created_by FROM tenant WHERE id = $1",
		tenantId,
	)
	if err != nil {
		return tenantInfo{}, fmt.Errorf("tenant not found: %w", err)
	}

	var info tenantInfo
	if err := row.Scan(&info.TenantID, &info.CreatedBy); err != nil {
		return tenantInfo{}, fmt.Errorf("failed to scan tenant: %w", err)
	}

	return info, nil
}

// verifyOrgToken verifies the verification token against the stored hash in tenant_attrs.
func verifyOrgToken(providedToken string, tenantId string) error {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database manager: %w", err)
	}

	var storedHash string
	row, err := dbms.QueryRow(
		"SELECT value FROM tenant_attrs WHERE tenant_id = $1 AND name = 'aws_org_verification_token_hash'",
		tenantId,
	)
	if err != nil {
		return fmt.Errorf("verification token hash not found: %w", err)
	}
	if err := row.Scan(&storedHash); err != nil {
		return fmt.Errorf("failed to scan verification token hash: %w", err)
	}

	// Compute SHA-256 hash of provided token and compare
	computedHash := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(providedToken)))
	if computedHash != storedHash {
		return fmt.Errorf("verification token does not match")
	}

	return nil
}

// validateAssumeRole validates an IAM role via STS AssumeRole and returns the AWS account number.
func validateAssumeRole(ctx context.Context, awsCfg aws.Config, roleArn string) (string, error) {
	stsSvc := sts.NewFromConfig(awsCfg)

	// Assume the role
	assumeRoleProvider := stscreds.NewAssumeRoleProvider(stsSvc, roleArn)
	assumedCfg := awsCfg.Copy()
	assumedCfg.Credentials = aws.NewCredentialsCache(assumeRoleProvider)

	// Validate by calling GetCallerIdentity with the assumed role
	assumedStsSvc := sts.NewFromConfig(assumedCfg)
	identity, err := assumedStsSvc.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", fmt.Errorf("STS AssumeRole failed for %s: %w", roleArn, err)
	}

	return aws.ToString(identity.Account), nil
}

// upsertOrgTypeAttr upserts the aws_org_type cloud_account_attr for an account.
func upsertOrgTypeAttr(dbms *common.DatabaseManager, accountId string, orgType string, logger *slog.Logger) {
	attrId := uuid.New().String()
	_, err := dbms.Exec(
		`INSERT INTO cloud_account_attrs (id, cloud_account_id, name, value, created_at, updated_at)
		VALUES ($1, $2, 'aws_org_type', $3, NOW(), NOW())
		ON CONFLICT (cloud_account_id, name) DO UPDATE SET value = $3, updated_at = NOW()`,
		attrId, accountId, orgType,
	)
	if err != nil {
		logger.Error("aws: failed to upsert aws_org_type attr", "error", err, "accountId", accountId)
	}
}

// verifySingleAccountToken verifies a per-request onboarding token stored in tenant_attrs.
// The token is keyed by external_id (the unique identifier for the onboarding request).
func verifySingleAccountToken(providedToken string, tenantId string, externalId string) error {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database manager: %w", err)
	}

	attrName := fmt.Sprintf("aws_onboard_token_%s", externalId)

	var storedHash string
	row, err := dbms.QueryRow(
		"SELECT value FROM tenant_attrs WHERE tenant_id = $1 AND name = $2",
		tenantId, attrName,
	)
	if err != nil {
		return fmt.Errorf("onboard token not found for external_id %s: %w", externalId, err)
	}
	if err := row.Scan(&storedHash); err != nil {
		return fmt.Errorf("failed to scan onboard token hash: %w", err)
	}

	computedHash := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(providedToken)))
	if computedHash != storedHash {
		return fmt.Errorf("onboard verification token does not match for external_id %s", externalId)
	}

	return nil
}

// verifySingleAccountOwnership checks that an account exists with matching
// (account_number, tenant, external_id). Used for CF Update events where the
// one-time token has already been consumed during the original Create.
func verifySingleAccountOwnership(accountNumber string, tenantId string, externalId string) error {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database manager: %w", err)
	}

	var accountId string
	row, err := dbms.QueryRow(
		"SELECT id FROM cloud_accounts WHERE account_number = $1 AND tenant = $2 AND external_id = $3",
		accountNumber, tenantId, externalId,
	)
	if err != nil {
		return fmt.Errorf("account not found for account_number %s and external_id %s: %w", accountNumber, externalId, err)
	}
	if err := row.Scan(&accountId); err != nil {
		return fmt.Errorf("account not found for account_number %s and external_id %s: %w", accountNumber, externalId, err)
	}

	return nil
}

// lookupAccountId finds the most recently created active account for a given account_number + tenant.
func lookupAccountId(accountNumber string, tenantId string) (string, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", fmt.Errorf("failed to get database manager: %w", err)
	}

	var accountId string
	row, err := dbms.QueryRow(
		"SELECT id FROM cloud_accounts WHERE account_number = $1 AND tenant = $2 AND status = 'active' ORDER BY created_at DESC LIMIT 1",
		accountNumber, tenantId,
	)
	if err != nil {
		return "", fmt.Errorf("account not found: %w", err)
	}
	if err := row.Scan(&accountId); err != nil {
		return "", fmt.Errorf("account not found: %w", err)
	}
	return accountId, nil
}

// handleSingleAccountCreate processes a Create/Update for a single-account auto-registration.
// Similar to handleOrgAccountCreate but:
// - Uses user-provided AccountName instead of AWS-{number}
// - Does NOT add aws_org_type=member attribute
// - Stores aws_onboard_request_id in cloud_account_attrs for polling
// - Uses cur_source: "auto_callback"
// - Cleans up the onboard token from tenant_attrs after success
func handleSingleAccountCreate(
	pCtx providers.CloudProviderContext,
	cfEvent CfnCustomResourceEvent,
	props CfnRegistrationProperties,
	tenant tenantInfo,
	awsCfg aws.Config,
) error {
	logger := pCtx.GetLogger()

	// Step 1: Validate role via STS AssumeRole
	accountNumber, err := validateAssumeRole(pCtx.GetContext(), awsCfg, props.RoleArn)
	if err != nil {
		return fmt.Errorf("failed to validate assume role: %w", err)
	}

	if accountNumber == "" {
		accountNumber = props.AccountNumber
	}

	logger.Info("aws: validated single-account registration", "accountNumber", accountNumber, "roleArn", props.RoleArn, "requestId", props.ExternalId)

	// Step 2: Check if account already exists (idempotency)
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database manager: %w", err)
	}

	var existingAccountId string
	row, err := dbms.QueryRow(
		"SELECT id FROM cloud_accounts WHERE account_number = $1 AND tenant = $2 ORDER BY created_at DESC LIMIT 1",
		accountNumber, tenant.TenantID,
	)
	if err == nil {
		if scanErr := row.Scan(&existingAccountId); scanErr != nil && scanErr.Error() != "sql: no rows in result set" {
			return fmt.Errorf("failed to scan existing account: %w", scanErr)
		}
	}

	// Build CUR data JSON — same as org but with cur_source: "auto_callback"
	curData := map[string]interface{}{
		"cost_report_s3_bucket":   props.CurBucketName,
		"cost_report_name":        props.CurReportName,
		"cost_report_s3_prefix":   props.CurS3Prefix + "/" + props.CurReportName,
		"cost_report_s3_region":   props.CurS3Region,
		"cost_report_compression": props.CurCompression,
		"cost_report_time_unit":   props.CurTimeUnit,
		"cost_report_versioning":  props.CurReportVersioning,
		"cost_report_format":      props.CurFormat,
		"cur_source":              "auto_callback",
	}
	curDataJSON, err := json.Marshal(curData)
	if err != nil {
		return fmt.Errorf("failed to marshal CUR data: %w", err)
	}

	externalId := props.ExternalId
	accountName := props.AccountName
	if accountName == "" {
		accountName = fmt.Sprintf("AWS-%s", accountNumber)
	}

	// Look up access mode from tenant_attrs (stored during onboarding URL generation)
	accountAccess := "readwrite"
	accessModeAttr := fmt.Sprintf("aws_onboard_access_mode_%s", externalId)
	accessRow, accessErr := dbms.QueryRow(
		"SELECT value FROM tenant_attrs WHERE tenant_id = $1 AND name = $2",
		tenant.TenantID, accessModeAttr,
	)
	if accessErr == nil {
		var accessVal string
		if scanErr := accessRow.Scan(&accessVal); scanErr == nil && accessVal != "" {
			accountAccess = accessVal
		}
	}
	logger.Info("aws: resolved account access mode", "accessMode", accountAccess, "externalId", externalId)

	if existingAccountId != "" {
		logger.Info("aws: updating existing account via single-account auto-registration", "accountNumber", accountNumber, "existingId", existingAccountId)
		_, err = dbms.Exec(
			`UPDATE cloud_accounts SET
				data = $1, assume_role = $2, status = 'active',
				external_id = $4,
				account_name = CASE
					WHEN NOT EXISTS (
						SELECT 1 FROM cloud_accounts
						WHERE tenant = $7
						AND account_name = $5 AND id != $3
					) THEN $5
					ELSE account_name
				END,
				account_access = $6, updated_at = NOW()
			WHERE id = $3`,
			string(curDataJSON), props.RoleArn, existingAccountId, externalId, accountName, accountAccess, tenant.TenantID,
		)
		if err != nil {
			return fmt.Errorf("failed to update existing account: %w", err)
		}

		triggerStoreUsageReport(existingAccountId, tenant.TenantID, false, logger)
		cleanupOnboardToken(dbms, tenant.TenantID, props.ExternalId, logger)
		return nil
	}

	// Step 3: Create new cloud_accounts entry
	newAccountId := uuid.New().String()

	// Use the actual onboarding user if available, fall back to tenant creator
	createdBy := tenant.CreatedBy
	if props.UserId != "" {
		createdBy = props.UserId
	}

	_, err = dbms.Exec(
		`INSERT INTO cloud_accounts (
			id, cloud_provider, account_number, account_name, account_type,
			assume_role, external_id, tenant,
			created_by, updated_by, status, data, account_access, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NOW(), NOW())`,
		newAccountId,        // $1 id
		"AWS",               // $2 cloud_provider
		accountNumber,       // $3 account_number
		accountName,         // $4 account_name
		"cloud",             // $5 account_type
		props.RoleArn,       // $6 assume_role
		externalId,          // $7 external_id
		tenant.TenantID,     // $8 tenant
		createdBy,           // $9 created_by
		createdBy,           // $10 updated_by
		"active",            // $11 status
		string(curDataJSON), // $12 data
		accountAccess,       // $13 account_access
	)
	if err != nil {
		return fmt.Errorf("failed to insert cloud account: %w", err)
	}

	logger.Info("aws: created single-account via auto-registration", "accountId", newAccountId, "accountNumber", accountNumber, "externalId", props.ExternalId)

	// Trigger initial data load
	triggerStoreUsageReport(newAccountId, tenant.TenantID, true, logger)

	// Clean up the one-time token
	cleanupOnboardToken(dbms, tenant.TenantID, props.ExternalId, logger)

	return nil
}

// cleanupOnboardToken deletes the one-time onboard token and access mode attr from tenant_attrs after successful registration.
func cleanupOnboardToken(dbms *common.DatabaseManager, tenantId string, externalId string, logger *slog.Logger) {
	tokenAttr := fmt.Sprintf("aws_onboard_token_%s", externalId)
	accessModeAttr := fmt.Sprintf("aws_onboard_access_mode_%s", externalId)
	_, err := dbms.Exec(
		"DELETE FROM tenant_attrs WHERE tenant_id = $1 AND name IN ($2, $3)",
		tenantId, tokenAttr, accessModeAttr,
	)
	if err != nil {
		logger.Error("aws: failed to cleanup onboard attrs", "error", err, "tenantId", tenantId, "externalId", externalId)
	} else {
		logger.Info("aws: cleaned up onboard attrs", "tenantId", tenantId, "externalId", externalId)
	}
}

// handleSingleAccountUpdate processes a CF Update for an existing single-account registration.
// Unlike handleSingleAccountCreate, this only updates mutable fields (role ARN, CUR data)
// and preserves existing account_access to avoid silently upgrading readonly onboardings.
func handleSingleAccountUpdate(
	pCtx providers.CloudProviderContext,
	cfEvent CfnCustomResourceEvent,
	props CfnRegistrationProperties,
	tenant tenantInfo,
	awsCfg aws.Config,
) error {
	logger := pCtx.GetLogger()

	accountNumber, err := validateAssumeRole(pCtx.GetContext(), awsCfg, props.RoleArn)
	if err != nil {
		return fmt.Errorf("failed to validate assume role: %w", err)
	}
	if accountNumber == "" {
		accountNumber = props.AccountNumber
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database manager: %w", err)
	}

	// Find the existing account by external_id (already verified by verifySingleAccountOwnership)
	var existingAccountId string
	row, err := dbms.QueryRow(
		"SELECT id FROM cloud_accounts WHERE account_number = $1 AND tenant = $2 AND external_id = $3",
		accountNumber, tenant.TenantID, props.ExternalId,
	)
	if err != nil {
		return fmt.Errorf("failed to query existing account: %w", err)
	}
	if err := row.Scan(&existingAccountId); err != nil {
		return fmt.Errorf("account not found for update: %w", err)
	}

	curData := map[string]interface{}{
		"cost_report_s3_bucket":   props.CurBucketName,
		"cost_report_name":        props.CurReportName,
		"cost_report_s3_prefix":   props.CurS3Prefix + "/" + props.CurReportName,
		"cost_report_s3_region":   props.CurS3Region,
		"cost_report_compression": props.CurCompression,
		"cost_report_time_unit":   props.CurTimeUnit,
		"cost_report_versioning":  props.CurReportVersioning,
		"cost_report_format":      props.CurFormat,
		"cur_source":              "auto_callback",
	}
	curDataJSON, err := json.Marshal(curData)
	if err != nil {
		return fmt.Errorf("failed to marshal CUR data: %w", err)
	}

	// Update only role ARN, CUR data, and status. Preserve existing account_access.
	_, err = dbms.Exec(
		`UPDATE cloud_accounts SET
			data = $1, assume_role = $2, status = 'active', updated_at = NOW()
		WHERE id = $3`,
		string(curDataJSON), props.RoleArn, existingAccountId,
	)
	if err != nil {
		return fmt.Errorf("failed to update account: %w", err)
	}

	logger.Info("aws: updated single-account via CF Update", "accountId", existingAccountId, "accountNumber", accountNumber)

	triggerStoreUsageReport(existingAccountId, tenant.TenantID, false, logger)

	return nil
}

// updateAgentStatusFromCfEvent updates the agent table with CF stack info
// when a Create/Update event is processed. This provides immediate visibility
// in the UI without waiting for the daily sync.
func updateAgentStatusFromCfEvent(cfEvent CfnCustomResourceEvent, accountId string, tenantId string, logger *slog.Logger) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		logger.Error("aws: failed to get dbms for agent status update", "error", err)
		return
	}

	// Parse stack name and region from StackId ARN
	// Format: arn:aws:cloudformation:us-east-1:123456789012:stack/stackName/guid
	_, stackRegion, _, _, stackName := parseARN(cfEvent.StackId)

	props := cfEvent.ResourceProperties
	connectionStatus := map[string]interface{}{
		"account_number": props.AccountNumber,
		"cf_stack": map[string]interface{}{
			"template_version": props.TemplateVersion,
			"stack_name":       stackName,
			"stack_region":     stackRegion,
			"stack_status":     cfEvent.RequestType + "_IN_PROGRESS",
			"updated_at":       time.Now().UTC().Format(time.RFC3339),
		},
	}

	connectionStatusJSON, err := json.Marshal(connectionStatus)
	if err != nil {
		logger.Error("aws: failed to marshal agent connection status", "error", err)
		return
	}

	timestamp := time.Now().Format(time.RFC3339)
	var tenantIdParam interface{}
	if tenantId == "" {
		tenantIdParam = nil
	} else {
		tenantIdParam = tenantId
	}

	_, err = dbms.Exec(`
		INSERT INTO agent (cloud_account_id, tenant, updated_at, type, status, last_connected_at, last_synced_at, access_secret, connection_status, status_message)
		VALUES ($1, $2, $3, 'AWS', 'CONNECTED', $3, $3, 'dummy', $4, '')
		ON CONFLICT (tenant, cloud_account_id, type) WHERE type != 'proxy'
			DO UPDATE SET updated_at = excluded.updated_at,
				last_connected_at = excluded.last_connected_at,
				connection_status = COALESCE(agent.connection_status, '{}'::jsonb) || excluded.connection_status`,
		accountId, tenantIdParam, timestamp, string(connectionStatusJSON),
	)
	if err != nil {
		logger.Error("aws: failed to update agent status from CF event", "error", err, "accountId", accountId)
	} else {
		logger.Info("aws: updated agent status from CF event", "accountId", accountId, "requestType", cfEvent.RequestType, "templateVersion", props.TemplateVersion)
	}
}

// triggerStoreUsageReport publishes a cost report job to RabbitMQ
// to trigger initial data load for a newly created org account.
// This uses the same queue as the daily cron (StoreDailyUsageReportForAllAccounts),
// ensuring the job is processed by ConsumeCloudAccountCostReportJobs.
// When backfill is true (brand-new account onboarding), the consumer additionally
// discovers and processes the available historical billing periods in-process.
func triggerStoreUsageReport(accountId string, tenantId string, backfill bool, logger *slog.Logger) {
	now := time.Now().UTC()
	job := map[string]interface{}{
		"job_id":     uuid.New().String(),
		"account_id": accountId,
		"tenant_id":  tenantId,
		"month":      int(now.Month()),
		"year":       now.Year(),
		"backfill":   backfill,
	}

	err := common.MqPublish(
		config.Config.RabbitMqCloudAccountCostReportExchange,
		config.Config.RabbitMqCloudAccountCostReportQueue,
		job,
	)
	if err != nil {
		logger.Error("aws: failed to publish cost report job for org account", "error", err, "accountId", accountId)
		return
	}
	logger.Info("aws: published initial cost report job for org account", "accountId", accountId, "jobId", job["job_id"])
}

// sendCfnResponse sends a response to the CloudFormation ResponseURL.
// This is called to signal SUCCESS or FAILED to CloudFormation so the
// stack creation/deletion can complete.
func sendCfnResponse(event CfnCustomResourceEvent, status string, reason string, accountNumber string) {
	if event.ResponseURL == "" {
		slog.Warn("aws: no ResponseURL in CF event, skipping callback", "requestId", event.RequestId)
		return
	}

	responseBody := CfnResponse{
		Status:             status,
		Reason:             reason,
		PhysicalResourceId: fmt.Sprintf("nudgebee-%s", accountNumber),
		StackId:            event.StackId,
		RequestId:          event.RequestId,
		LogicalResourceId:  event.LogicalResourceId,
		Data:               map[string]string{"Message": reason},
	}

	jsonResponse, err := json.Marshal(responseBody)
	if err != nil {
		slog.Error("aws: failed to marshal CFN response", "error", err)
		return
	}

	req, err := http.NewRequest("PUT", event.ResponseURL, bytes.NewReader(jsonResponse))
	if err != nil {
		slog.Error("aws: failed to create CFN response request", "error", err)
		return
	}
	// Empty content-type as per CloudFormation requirements (matches old TS code)
	req.Header.Set("Content-Type", "")
	req.ContentLength = int64(len(jsonResponse))

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("aws: failed to send CFN response", "error", err, "responseURL", event.ResponseURL)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	// Read and discard body to allow connection reuse
	_, _ = io.Copy(io.Discard, resp.Body)

	slog.Info("aws: sent CFN response", "status", status, "cfnStatusCode", resp.StatusCode, "accountNumber", accountNumber)
}
