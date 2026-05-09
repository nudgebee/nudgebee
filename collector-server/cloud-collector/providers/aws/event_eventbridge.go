package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/providers"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// EventBridgeEvent represents the structure of an AWS EventBridge event.
// For more details on the EventBridge event structure, see:
// https://docs.aws.amazon.com/eventbridge/latest/userguide/eb-events.html
type EventBridgeEvent struct {
	Version              string          `json:"version"`
	ID                   string          `json:"id"`
	DetailType           string          `json:"detail-type"`
	Source               string          `json:"source"`
	Account              string          `json:"account"`
	Time                 time.Time       `json:"time"`
	Region               string          `json:"region"`
	Resources            []string        `json:"resources"`
	Detail               json.RawMessage `json:"detail"`
	NudgebeeAccountToken string          `json:"nudgebeeAccountToken,omitempty"`
}

// SNSMessageAttribute represents an SNS message attribute.
type SNSMessageAttribute struct {
	Type  string `json:"Type"`
	Value string `json:"Value"`
}

// SNSNotificationPayload represents the structure of an SNS notification,
// which might be the content of an SQS message if EventBridge publishes to SNS, then SNS to SQS.
type SNSNotificationPayload struct {
	Type              string                         `json:"Type"`
	MessageId         string                         `json:"MessageId"`
	TopicArn          string                         `json:"TopicArn,omitempty"`
	Message           string                         `json:"Message"` // This field will contain the JSON string of the actual event (e.g., EventBridgeEvent).
	Timestamp         time.Time                      `json:"Timestamp"`
	SignatureVersion  string                         `json:"SignatureVersion"`
	Signature         string                         `json:"Signature"`
	SigningCertURL    string                         `json:"SigningCertURL"`
	UnsubscribeURL    string                         `json:"UnsubscribeURL,omitempty"`
	Subject           string                         `json:"Subject,omitempty"`
	MessageAttributes map[string]SNSMessageAttribute `json:"MessageAttributes,omitempty"`
}

// AccountMetadata stores additional metadata about an account that's not in providers.Account
type AccountMetadata struct {
	ID       string // UUID of the account in cloud_accounts table
	TenantID string // UUID of the tenant
}

// Global map to store account metadata by account number (temporary solution)
// Key: account_number, Value: AccountMetadata
var (
	accountMetadataCache      = make(map[string]AccountMetadata)
	accountMetadataCacheMutex sync.RWMutex
)

// accountLookupCache caches the result of getAccountByExternalId so the SQS
// receive loop avoids a per-message PG SELECT. Key is (external_id, account_number)
// to keep the lookup tenant-safe (the same AWS account number can belong to
// multiple Nudgebee tenants — only the external_id disambiguates).
const accountLookupTTL = 5 * time.Minute

type accountLookupKey struct {
	externalId    string
	accountNumber string
}

type accountLookupEntry struct {
	account  providers.Account
	tenantId string
	expiry   time.Time
}

var (
	accountLookupCache   = make(map[accountLookupKey]accountLookupEntry)
	accountLookupCacheMu sync.RWMutex
)

// EventBridge agent status throttle cache.
// Limits DB writes to at most once per hour per cloud account.
const eventBridgeAgentStatusTTL = time.Hour

var (
	ebAgentStatusCache   = make(map[string]time.Time)
	ebAgentStatusCacheMu sync.Mutex
)

// updateEventBridgeAgentStatus merges EventBridge connectivity data into the
// main AWS agent row's connection_status JSONB. Regions accumulate over time
// via jsonb deep merge.
func updateEventBridgeAgentStatus(accountId, tenantId, region string) error {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("eventbridge agent status: failed to get dbms: %w", err)
	}

	now := time.Now()
	timestamp := now.Format(time.RFC3339)

	var tenantIdParam interface{}
	if tenantId == "" {
		tenantIdParam = nil
	} else {
		tenantIdParam = tenantId
	}

	// Update the main AWS agent row's connection_status to include eventbridge data.
	//
	// Uses the jsonb `||` deep-merge operator instead of `jsonb_set` because
	// jsonb_set does not create intermediate objects: if connection_status has
	// no `eventbridge` key yet (the case for any agent row created before
	// EventBridge was wired up to the main AWS agent), jsonb_set with path
	// '{eventbridge,regions}' returns the document unchanged and the eventbridge
	// data never lands. The merge approach builds the eventbridge sub-document
	// and shallowly merges it onto the existing one, so sibling keys (regions
	// accumulating across calls, connected_at stamped at onboarding) are
	// preserved while last_event_at and the per-region timestamp are upserted.
	query := `UPDATE agent
		SET updated_at = $1,
			connection_status = COALESCE(connection_status, '{}'::jsonb) ||
				jsonb_build_object(
					'eventbridge',
					COALESCE(connection_status->'eventbridge', '{}'::jsonb) ||
						jsonb_build_object(
							'last_event_at', to_jsonb($1::text),
							'regions',
								COALESCE(connection_status #> '{eventbridge,regions}', '{}'::jsonb) ||
									jsonb_build_object($4::text, to_jsonb($1::text))
						)
				)
		WHERE cloud_account_id = $2 AND tenant IS NOT DISTINCT FROM $3 AND type = 'AWS'`

	result, err := dbms.Exec(query, timestamp, accountId, tenantIdParam, region)
	if err != nil {
		return fmt.Errorf("eventbridge agent status: failed to update main AWS agent: %w", err)
	}

	if rowsAffected, _ := result.RowsAffected(); rowsAffected == 0 {
		slog.Warn("eventbridge: no main AWS agent row found to update",
			"accountId", accountId, "tenantId", tenantId)
	}
	return nil
}

// updateEventBridgeAgentStatusThrottled wraps updateEventBridgeAgentStatus with
// a per-account 1-hour throttle to avoid excessive DB writes.
func updateEventBridgeAgentStatusThrottled(accountId, tenantId, region string) {
	now := time.Now()

	ebAgentStatusCacheMu.Lock()
	// Evict expired entries to prevent unbounded growth
	for k, ts := range ebAgentStatusCache {
		if now.Sub(ts) >= eventBridgeAgentStatusTTL {
			delete(ebAgentStatusCache, k)
		}
	}
	if lastUpdate, ok := ebAgentStatusCache[accountId]; ok && now.Sub(lastUpdate) < eventBridgeAgentStatusTTL {
		ebAgentStatusCacheMu.Unlock()
		return
	}
	ebAgentStatusCache[accountId] = now
	ebAgentStatusCacheMu.Unlock()

	if err := updateEventBridgeAgentStatus(accountId, tenantId, region); err != nil {
		// Do not evict from cache on failure to prevent DB storms during persistent errors.
		// The throttle will prevent retries for 1 hour.
		slog.Error("eventbridge: failed to update agent status", "error", err, "accountId", accountId)
	} else {
		slog.Info("eventbridge: updated agent status", "accountId", accountId, "region", region)
	}
}

// eventBridgeSourceToServiceName maps common AWS EventBridge source prefixes to standardized service names.
var eventBridgeSourceToServiceNameMap = map[string]string{
	"aws.ec2":                  ServiceNameEc2,
	"aws.s3":                   ServiceNameS3,
	"aws.rds":                  ServiceNameRDS,
	"aws.lambda":               ServiceNameLambda,
	"aws.sns":                  ServiceNameSNS,
	"aws.sqs":                  ServiceNameSQS, // Corresponds to AWSQueueService in GetAwsService
	"aws.events":               ServiceNameEventBridge,
	"aws.guardduty":            ServiceNameGuardDuty,
	"aws.securityhub":          ServiceNameSecurityHub,
	"aws.health":               "AWSHealth", // AWS Health events often use this source
	"aws.cloudtrail":           ServiceNameCloudTrail,
	"aws.kms":                  ServiceNameKMS,
	"aws.ecr":                  ServiceNameECR,
	"aws.ecs":                  ServiceNameECS,
	"aws.eks":                  ServiceNameEKS,
	"aws.elasticloadbalancing": ServiceNameELB, // ELB events often use this source
	"aws.autoscaling":          ServiceNameAutoScaling,
	"aws.cloudformation":       ServiceNameCloudFormation,
	"aws.secretsmanager":       ServiceNameSecretsManager,
	"aws.config":               "AWSConfig", // AWS Config events
	"aws.iam":                  ServiceNameIAM,
	"aws.route53":              ServiceNameRoute53,
	"aws.vpc":                  ServiceNameVPC,        // For VPC Flow Logs, etc.
	"aws.cloudwatch":           ServiceNameCloudWatch, // For CloudWatch Alarms, etc.
	"aws.codeartifact":         ServiceNameCodeArtifact,
	"aws.elasticache":          ServiceNameElastiCache,
	"aws.msk":                  ServiceNameMSK,
	"aws.sagemaker":            ServiceNameSageMaker,
	"aws.redshift":             ServiceNameRedshift,
	"aws.es":                   ServiceNameES, // OpenSearch Service
	"aws.efs":                  ServiceNameEFS,
	"aws.bedrock":              ServiceNameBedrock,
	"aws.xray":                 ServiceNameXray,
	"aws.backup":               ServiceNameBackup,
	"aws.cloudfront":           ServiceNameCloudFront,
	"aws.dynamodb":             ServiceNameDynamoDB,
	// Add more mappings as required based on the sources you expect
}

// getServiceNameFromEventBridgeSource attempts to map an EventBridge source to a canonical service name.
func getServiceNameFromEventBridgeSource(source string) string {
	source2 := source
	if !strings.HasPrefix(source, "aws.") {
		source2 = "aws." + source
	}
	if serviceName, ok := eventBridgeSourceToServiceNameMap[source2]; ok {
		return serviceName
	}

	// For unmapped AWS services (e.g., aws.someotherservice)
	if strings.HasPrefix(source, "aws.") {
		parts := strings.SplitN(source, ".", 2)
		if len(parts) == 2 && parts[1] != "" {
			// Capitalize first letter of service part, e.g., "ec2" -> "Ec2"
			servicePart := parts[1]
			return "Amazon" + strings.ToUpper(servicePart[:1]) + servicePart[1:]
		}
	}
	// For custom sources or unhandled cases, return the source as is.
	return source
}

// parseARN extracts service, region, account, resource type, and resource ID from an ARN.
// This is a simplified parser. For a more robust solution, consider a dedicated ARN parsing library.
func parseARN(arnString string) (service, region, account, resourceType, resourceID string) {
	parts := strings.Split(arnString, ":")
	if len(parts) < 6 {
		return // Not a valid ARN or not enough parts
	}

	service = parts[2]
	region = parts[3]
	account = parts[4]
	resourcePart := parts[5]

	// Common patterns for resource part: type/id or type:id or just id
	if idx := strings.IndexAny(resourcePart, "/:"); idx != -1 {
		resourceType = resourcePart[:idx]
		resourceID = resourcePart[idx+1:]
	} else {
		resourceID = resourcePart // Assume the whole part is the ID if no separator
		// resourceType might need to be inferred from the service (parts[2]) or DetailType
	}
	return
}

// EventBridgeEventProcessor defines an interface for processing parsed EventBridge events.
// You would implement this interface to handle the specific logic for different event types.
type EventBridgeEventProcessor interface {
	Process(ctx providers.CloudProviderContext, event EventBridgeEvent, account providers.Account) (providers.Event, error)
}

// processSQSMessageBodyForEventBridgeEvent attempts to parse an EventBridgeEvent
// from a raw SQS message body string.
// The SQS message body might directly contain the EventBridge event JSON,
// or it might contain an SNS notification JSON which in turn wraps the EventBridge event JSON.
//
// Parameters:
//
//	ctx: CloudProviderContext for logging and other context.
//	sqsMessageBody: The raw string content of the SQS message's body.
//	processor: An implementation of EventProcessor that will handle the successfully parsed EventBridgeEvent.
//
// Returns:
//
//	A providers.Event and an error if parsing fails or if the processor returns an error.
func processSQSMessageBodyForEventBridgeEvent(ctx providers.CloudProviderContext, sqsMessageBody string, processor EventBridgeEventProcessor, eventHandler providers.ProcessedEventHandler) (providers.Event, providers.Account, error) {
	// Attempt to unmarshal as an SNS notification first.
	// This is common if EventBridge -> SNS -> SQS.
	var snsPayload SNSNotificationPayload
	if err := common.UnmarshalJson([]byte(sqsMessageBody), &snsPayload); err == nil && snsPayload.Type == "Notification" && snsPayload.Message != "" {
		// If it's an SNS notification, the actual EventBridge event is in snsPayload.Message (as a JSON string).
		event, err := parseEventBridgeEvent([]byte(snsPayload.Message))
		if err != nil {
			ctx.GetLogger().Error("Error parsing EventBridge event from SNS message", "error", err)
			return providers.Event{}, providers.Account{}, err
		}

		// Use token-based account lookup (falls back to legacy if no token)
		account, err := getAccountFromEventBridgeEvent(ctx, event, eventHandler)
		if err != nil {
			ctx.GetLogger().Error("Error getting account from EventBridge event", "error", err)
			return providers.Event{}, providers.Account{}, err
		}

		if account.Region == nil && event.Region != "" {
			account.Region = &event.Region
		}
		ctx.GetLogger().Debug("Processing EventBridge message from SNS", "source", event.Source, "detailType", event.DetailType, "eventId", event.ID)
		providerEvent, err := processor.Process(ctx, event, account)
		return providerEvent, account, err
	}

	// If not an SNS notification (or unmarshalling as SNS failed),
	// try to unmarshal the SQS message body directly as an EventBridge event.
	// This is common if EventBridge -> SQS directly.
	event, err := parseEventBridgeEvent([]byte(sqsMessageBody))
	if err != nil {
		ctx.GetLogger().Error("Error parsing EventBridge event directly from SQS message body", "error", err)
		return providers.Event{}, providers.Account{}, err
	}

	// Use token-based account lookup (falls back to legacy if no token)
	account, err := getAccountFromEventBridgeEvent(ctx, event, eventHandler)
	if err != nil {
		ctx.GetLogger().Error("Error getting account from EventBridge event", "error", err)
		return providers.Event{}, providers.Account{}, err
	}

	if account.Region == nil && event.Region != "" {
		account.Region = &event.Region
	}

	ctx.GetLogger().Debug("Processing EventBridge message (direct)", "source", event.Source, "detailType", event.DetailType, "eventId", event.ID)
	providerEvent, err := processor.Process(ctx, event, account)
	return providerEvent, account, err
}

// parseEventBridgeEvent is a helper function to unmarshal a byte slice into an EventBridgeEvent struct.
func parseEventBridgeEvent(data []byte) (EventBridgeEvent, error) {
	var event EventBridgeEvent
	if err := common.UnmarshalJson(data, &event); err != nil {
		return EventBridgeEvent{}, err
	}
	return event, nil
}

// getAccountFromEventBridgeEvent extracts account info from EventBridge event using token-based lookup.
// It attempts to extract the nudgebeeAccountToken from event.Detail and performs a secure lookup.
// Falls back to the legacy account number lookup if no token is found (for backward compatibility).
func getAccountFromEventBridgeEvent(ctx providers.CloudProviderContext, event EventBridgeEvent, eventHandler providers.ProcessedEventHandler) (providers.Account, error) {
	var externalId string

	// First check if token is at the top level (e.g. CloudWatch alarm events where
	// InputTransformer passes raw detail and places token at root)
	if event.NudgebeeAccountToken != "" {
		externalId = event.NudgebeeAccountToken
	}

	// If not at top level, check inside detail (e.g. EC2 events where InputTransformer
	// reconstructs detail and injects token inside)
	if externalId == "" {
		var detail map[string]interface{}
		if err := common.UnmarshalJson(event.Detail, &detail); err != nil {
			ctx.GetLogger().Error("Failed to parse event detail", "error", err)
			return providers.Account{}, err
		}
		if token, ok := detail["nudgebeeAccountToken"].(string); ok {
			externalId = token
		}
	}

	if externalId == "" {
		// Fallback: Try old method (will fail for multi-tenant with same account_number)
		ctx.GetLogger().Warn("No nudgebeeAccountToken in event, using legacy account lookup",
			"awsAccountNumber", event.Account,
			"warning", "This may return wrong account in multi-tenant scenarios!")
		return eventHandler.GetAccountFromCloudProviderAccountId(ctx, event.Account)
	}

	// Lookup by external_id (secure, tenant-aware)
	return getAccountByExternalId(ctx, externalId, event.Account)
}

// GetAccountMetadata retrieves the account ID and tenant ID for a given account number.
// This is used after account lookup to get metadata needed for resource updates.
func GetAccountMetadata(accountNumber string) (accountId string, tenantId string, found bool) {
	accountMetadataCacheMutex.RLock()
	defer accountMetadataCacheMutex.RUnlock()

	metadata, ok := accountMetadataCache[accountNumber]
	if !ok {
		return "", "", false
	}
	return metadata.ID, metadata.TenantID, true
}

// getAccountByExternalId looks up cloud_account by external_id (token) and AWS account number.
// This provides tenant-safe account resolution even when multiple tenants use the same AWS account.
// Successful lookups are cached for accountLookupTTL; negatives are NOT cached so that
// orphan tokens (rule still firing in customer account, cloud_accounts row deleted) self-resolve
// the moment the row is restored.
func getAccountByExternalId(ctx providers.CloudProviderContext, externalId string, expectedAwsAccountNumber string) (providers.Account, error) {
	cacheKey := accountLookupKey{externalId: externalId, accountNumber: expectedAwsAccountNumber}
	accountLookupCacheMu.RLock()
	if v, ok := accountLookupCache[cacheKey]; ok && time.Now().Before(v.expiry) {
		acct := v.account
		tenantId := v.tenantId
		accountLookupCacheMu.RUnlock()
		// Refresh derived metadata cache so downstream throttled status updates work.
		accountMetadataCacheMutex.Lock()
		accountMetadataCache[acct.AccountNumber] = AccountMetadata{ID: acct.ID, TenantID: tenantId}
		accountMetadataCacheMutex.Unlock()
		return acct, nil
	}
	accountLookupCacheMu.RUnlock()

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return providers.Account{}, fmt.Errorf("failed to get database manager: %w", err)
	}

	query := `
		SELECT
			id,
			account_number,
			account_name,
			cloud_provider,
			tenant,
			region,
			assume_role,
			access_key,
			access_secret,
			status
		FROM cloud_accounts
		WHERE external_id = $1
		  AND status = 'active'
		  AND account_number = $2
	`

	var accountRow struct {
		Id            string  `db:"id"`
		AccountNumber string  `db:"account_number"`
		AccountName   string  `db:"account_name"`
		CloudProvider string  `db:"cloud_provider"`
		Tenant        string  `db:"tenant"`
		Region        *string `db:"region"`
		AssumeRole    *string `db:"assume_role"`
		AccessKey     *string `db:"access_key"`
		AccessSecret  *string `db:"access_secret"`
		Status        string  `db:"status"`
	}

	err = dbms.QueryRowAndScan(&accountRow, query, externalId, expectedAwsAccountNumber)
	if err != nil {
		return providers.Account{}, fmt.Errorf("account not found for external_id '%s' and account_number '%s': %w",
			externalId, expectedAwsAccountNumber, err)
	}

	// Store account metadata (ID and TenantID) in cache for later use
	accountMetadataCacheMutex.Lock()
	accountMetadataCache[accountRow.AccountNumber] = AccountMetadata{
		ID:       accountRow.Id,
		TenantID: accountRow.Tenant,
	}
	accountMetadataCacheMutex.Unlock()

	// Build providers.Account with the fields it actually has
	account := providers.Account{
		ID:            accountRow.Id,
		AccountNumber: accountRow.AccountNumber,
		AccountName:   accountRow.AccountName,
		CloudProvider: accountRow.CloudProvider,
		AssumeRole:    accountRow.AssumeRole,
		AccessKey:     accountRow.AccessKey,
		AccessSecret:  accountRow.AccessSecret,
		Region:        accountRow.Region,
	}

	// Cache the resolved account+tenant for fast subsequent lookups.
	accountLookupCacheMu.Lock()
	accountLookupCache[cacheKey] = accountLookupEntry{
		account:  account,
		tenantId: accountRow.Tenant,
		expiry:   time.Now().Add(accountLookupTTL),
	}
	accountLookupCacheMu.Unlock()

	return account, nil
}

// StartEventBridgeSQSConsumer continuously polls an SQS queue (specified in config)
// for EventBridge events and processes them.
// This function is designed to run as a long-running goroutine.
func StartEventBridgeSQSConsumer(pCtx providers.CloudProviderContext, eventHandler providers.ProcessedEventHandler) {
	defer func() {
		if r := recover(); r != nil {
			pCtx.GetLogger().Error("SQS consumer panicked", "error", r)
		}
	}()
	queueIdentifier := config.Config.CloudCollectorAwsEventbridgeSqs
	logger := pCtx.GetLogger()

	if queueIdentifier == "" {
		logger.Warn("aws: sqs queue URL for EventBridge events is not configured (CloudCollectorAwsEventbridgeSqs). Consumer will not start.")
		return
	}

	if eventHandler == nil {
		logger.Error("aws: ProcessedEventHandler is nil. SQS consumer cannot start as it needs a way to handle processed events.")
		return
	}

	logger.Info("aws: starting EventBridge SQS consumer", "queueIdentifier", queueIdentifier, "component", "SQSConsumer")

	awsConfig, err := awsconfig.LoadDefaultConfig(pCtx.GetContext())
	if err != nil {
		logger.Error("aws: failed to load AWS config for SQS consumer", "error", err)
		return
	}

	var sqsRegion string
	var queueName string
	var queueOwnerAWSAccountId string
	var finalQueueURL string
	isArn := false

	if strings.HasPrefix(queueIdentifier, "arn:aws:sqs:") {
		isArn = true
		_, parsedRegion, parsedAccount, _, parsedQueueName := parseARN(queueIdentifier)

		if parsedRegion == "" || parsedQueueName == "" {
			logger.Error("aws: invalid SQS ARN format or failed to parse region/queue name", "arn", queueIdentifier)
			return
		}
		sqsRegion = parsedRegion
		queueName = parsedQueueName
		queueOwnerAWSAccountId = parsedAccount
		logger.Info("aws: identified SQS queue by ARN", "arn", queueIdentifier, "parsedRegion", sqsRegion, "parsedQueueName", queueName, "component", "SQSConsumer")
	} else {
		// Assume it's a URL
		finalQueueURL = queueIdentifier
		// Attempt to parse region from SQS URL
		// Example: https://sqs.us-east-1.amazonaws.com/123456789012/MyQueue
		urlParts := strings.Split(finalQueueURL, ".")
		if len(urlParts) > 2 && strings.HasPrefix(urlParts[0], "https://sqs") && (strings.Contains(urlParts[1], "-") || urlParts[1] == "sqs") { // Basic check for region pattern or global endpoint
			sqsRegion = urlParts[1]
			logger.Info("aws: parsed SQS region from queue URL", "region", sqsRegion)
		} else {
			logger.Info("aws: could not parse region from SQS URL, will rely on session region or fail.", "queueURL", finalQueueURL)
		}
	}

	if sqsRegion == "" {
		if awsConfig.Region != "" {
			sqsRegion = awsConfig.Region
			logger.Info("aws: using AWS config region for SQS consumer", "region", sqsRegion, "component", "SQSConsumer")
		} else {
			logger.Error("aws: SQS region is not specified and cannot be inferred. SQS consumer cannot start.")
			return
		}
	}

	// Create SQS client with the specified region
	sqsConfig := awsConfig.Copy()
	sqsConfig.Region = sqsRegion
	sqsSvc := sqs.NewFromConfig(sqsConfig)

	if isArn {
		getQueueUrlInput := &sqs.GetQueueUrlInput{QueueName: aws.String(queueName)}
		// It's good practice to provide QueueOwnerAWSAccountId if the queue might be in a different account
		// than the one the credentials resolve to, though for same-account GetQueueUrl it's often optional.
		if queueOwnerAWSAccountId != "" {
			getQueueUrlInput.QueueOwnerAWSAccountId = aws.String(queueOwnerAWSAccountId)
		}
		queueUrlOutput, err := sqsSvc.GetQueueUrl(pCtx.GetContext(), getQueueUrlInput)
		if err != nil {
			logger.Error("aws: failed to get SQS queue URL from ARN", "arn", queueIdentifier, "error", err, "component", "SQSConsumer")
			return
		}
		finalQueueURL = *queueUrlOutput.QueueUrl
		logger.Info("aws: resolved SQS ARN to URL", "arn", queueIdentifier, "url", finalQueueURL, "component", "SQSConsumer")
	}

	// Instantiate the awsProvider to pass to the TemplatedEventBridgeProcessor
	awsProviderInstance := &awsProvider{} // Assuming awsProvider is the concrete type implementing the awsProviderAPI methods.
	rules, err := GetEventRules("")
	if err != nil {
		logger.Error("aws: failed to get event rules", "error", err, "component", "SQSConsumer")
		return
	}

	processor := NewTemplatedEventBridgeProcessor(rules, awsProviderInstance)

	for {
		select {
		case <-pCtx.GetContext().Done():
			logger.Info("aws: SQS consumer shutting down.", "queueURL", finalQueueURL, "component", "SQSConsumer")
			return
		default:
			receiveParams := &sqs.ReceiveMessageInput{
				QueueUrl:            aws.String(finalQueueURL),
				MaxNumberOfMessages: int32(10), // Process up to 10 messages at a time
				WaitTimeSeconds:     int32(20), // Enable long polling
				// Request SentTimestamp so the staleness guard in
				// processBatchConcurrent can drop hour-old messages.
				MessageSystemAttributeNames: []sqstypes.MessageSystemAttributeName{
					sqstypes.MessageSystemAttributeNameSentTimestamp,
				},
			}

			resp, err := sqsSvc.ReceiveMessage(pCtx.GetContext(), receiveParams)
			if err != nil {
				// Check if the error is due to context cancellation
				if pCtx.GetContext().Err() == context.Canceled || pCtx.GetContext().Err() == context.DeadlineExceeded {
					logger.Info("aws: SQS consumer context cancelled or deadline exceeded during ReceiveMessage.", "queueURL", finalQueueURL, "error", err, "component", "SQSConsumer")
					return // Exit if context is done
				}
				logger.Error("aws: error receiving messages from SQS", "queueURL", finalQueueURL, "error", err, "component", "SQSConsumer")
				time.Sleep(5 * time.Second) // Wait before retrying
				continue
			}

			if len(resp.Messages) == 0 {
				logger.Debug("aws: no messages received from SQS, continuing to poll.", "queueURL", finalQueueURL, "component", "SQSConsumer")
				continue
			}

			logger.Info("aws: received messages from SQS", "count", len(resp.Messages), "queueURL", finalQueueURL, "component", "SQSConsumer")
			processBatchConcurrent(pCtx, sqsSvc, finalQueueURL, processor, eventHandler, resp.Messages)
		}
	}
}

// sqsWorkerConcurrency caps the number of in-flight goroutines per receive
// batch. SQS receive returns at most 10 messages, so concurrency=10 lets every
// message in a batch run in parallel while bounding fan-out.
const sqsWorkerConcurrency = 10

// maxMessageAge is the cutoff beyond which incoming messages are dropped
// without processing. Stale state-change events (e.g., an EC2 termination
// event delivered 4 days late after the queue backlogs) would otherwise
// re-emit historical alarms / runbook firings on already-resolved state and
// pollute the events table even though `cloud_resourses` itself is guarded
// by last_state_change. 1 hour is a generous bound — under normal operation
// messages process within seconds; if SQS is delivering hour-old events,
// the upstream snapshot has already drifted past the point where replaying
// is useful.
const maxMessageAge = 1 * time.Hour

// messageAge returns the wall-clock age of an SQS message based on the
// SentTimestamp system attribute (milliseconds since epoch). Returns
// ok=false if the attribute is missing or unparseable — caller should
// process the message normally in that case rather than guess.
func messageAge(msg sqstypes.Message) (time.Duration, bool) {
	v, ok := msg.Attributes[string(sqstypes.MessageSystemAttributeNameSentTimestamp)]
	if !ok {
		return 0, false
	}
	ts, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return time.Since(time.UnixMilli(ts)), true
}

// batchProcessResult records the outcome for one message so we can issue a
// single DeleteMessageBatch at the end of the batch.
type batchProcessResult struct {
	receiptHandle string
	messageId     string
	ackDelete     bool
}

// processBatchConcurrent runs the per-message pipeline in parallel and then
// issues a single DeleteMessageBatch for messages that are safe to ack.
// Messages whose processing failed are NOT deleted — SQS visibility timeout
// will redeliver them, and the queue's RedrivePolicy moves them to DLQ after
// maxReceiveCount attempts (same behavior as before).
func processBatchConcurrent(
	pCtx providers.CloudProviderContext,
	sqsSvc *sqs.Client,
	queueURL string,
	processor *TemplatedEventBridgeProcessor,
	eventHandler providers.ProcessedEventHandler,
	messages []sqstypes.Message,
) {
	logger := pCtx.GetLogger()
	results := make(chan batchProcessResult, len(messages))
	sem := make(chan struct{}, sqsWorkerConcurrency)
	var wg sync.WaitGroup

	for _, msg := range messages {
		msg := msg
		mid := aws.ToString(msg.MessageId)
		rh := aws.ToString(msg.ReceiptHandle)

		if msg.Body == nil {
			logger.Warn("aws: received SQS message with nil body", "messageId", mid, "queueURL", queueURL, "component", "SQSConsumer")
			results <- batchProcessResult{receiptHandle: rh, messageId: mid, ackDelete: true}
			continue
		}

		// Drop messages older than maxMessageAge. Re-emitting a stale
		// state-change event would create wrong-time alarms and trigger
		// runbooks on already-resolved state. We still ack-delete so the
		// queue drains; we just don't process.
		if age, ok := messageAge(msg); ok && age > maxMessageAge {
			logger.Info("aws: dropping stale SQS message",
				"messageId", mid, "ageSeconds", int(age.Seconds()),
				"maxAgeSeconds", int(maxMessageAge.Seconds()),
				"queueURL", queueURL, "component", "SQSConsumer")
			results <- batchProcessResult{receiptHandle: rh, messageId: mid, ackDelete: true}
			continue
		}

		// Fast-skip: if Eligible() rejects the event, no rule could match — ack
		// without invoking DB or AWS APIs. Source==""means we couldn't recognize
		// the body shape (e.g., SNS-wrapped); fall through to full processing
		// in that case to preserve correctness.
		var preview EventBridgeEvent
		if err := common.UnmarshalJson([]byte(*msg.Body), &preview); err == nil && preview.Source != "" && !processor.Eligible(preview) {
			logger.Debug("aws: skipping ineligible event",
				"messageId", mid, "source", preview.Source, "detailType", preview.DetailType, "component", "SQSConsumer")
			results <- batchProcessResult{receiptHandle: rh, messageId: mid, ackDelete: true}
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					logger.Error("aws: panic in SQS worker", "messageId", mid, "panic", r, "component", "SQSConsumer")
					// Don't ack — let visibility timeout redeliver. If panic is
					// deterministic, RedrivePolicy will eventually land it in DLQ.
					results <- batchProcessResult{receiptHandle: rh, messageId: mid, ackDelete: false}
				}
			}()

			processedEvent, originatingAccount, err := processSQSMessageBodyForEventBridgeEvent(pCtx, *msg.Body, processor, eventHandler)
			if err != nil {
				logger.Error("aws: failed to process SQS message for EventBridge event", "messageId", mid, "error", err, "queueURL", queueURL, "component", "SQSConsumer")
				results <- batchProcessResult{receiptHandle: rh, messageId: mid, ackDelete: false}
				return
			}

			if processedEvent.EventId == "" {
				// No rule matched — preserve previous "ack and continue" behavior.
				logger.Info("aws: EventBridge event skipped by processor", "sqsMessageId", mid, "component", "SQSConsumer")
				results <- batchProcessResult{receiptHandle: rh, messageId: mid, ackDelete: true}
				return
			}

			logger.Info("aws: successfully processed EventBridge event from SQS message",
				"processedEventId", processedEvent.EventId, "sqsMessageId", mid, "eventName", processedEvent.EventName, "component", "SQSConsumer")

			if hErr := eventHandler.ProcessEvent(pCtx, processedEvent, originatingAccount); hErr != nil {
				logger.Error("aws: failed to handle processed event", "error", hErr, "processedEventId", processedEvent.EventId, "sqsMessageId", mid, "component", "SQSConsumer")
				results <- batchProcessResult{receiptHandle: rh, messageId: mid, ackDelete: false}
				return
			}

			// Update EventBridge agent status (throttled, best-effort).
			go func(acc providers.Account, region string) {
				accId, tenantId, found := GetAccountMetadata(acc.AccountNumber)
				if !found {
					return
				}
				updateEventBridgeAgentStatusThrottled(accId, tenantId, region)
			}(originatingAccount, processedEvent.ResourceRegion)

			results <- batchProcessResult{receiptHandle: rh, messageId: mid, ackDelete: true}
		}()
	}

	wg.Wait()
	close(results)

	// Issue a single DeleteMessageBatch for all successfully-processed messages.
	// SQS caps DeleteMessageBatch at 10 entries — equal to MaxNumberOfMessages,
	// so a single call always covers a batch.
	var entries []sqstypes.DeleteMessageBatchRequestEntry
	idx := 0
	for r := range results {
		if !r.ackDelete {
			continue
		}
		entries = append(entries, sqstypes.DeleteMessageBatchRequestEntry{
			Id:            aws.String(strconv.Itoa(idx)),
			ReceiptHandle: aws.String(r.receiptHandle),
		})
		idx++
	}
	if len(entries) == 0 {
		return
	}
	out, err := sqsSvc.DeleteMessageBatch(pCtx.GetContext(), &sqs.DeleteMessageBatchInput{
		QueueUrl: aws.String(queueURL),
		Entries:  entries,
	})
	if err != nil {
		logger.Error("aws: DeleteMessageBatch failed; messages will be retried via visibility timeout", "error", err, "count", len(entries), "queueURL", queueURL, "component", "SQSConsumer")
		return
	}
	for _, f := range out.Failed {
		logger.Error("aws: DeleteMessageBatch entry failed", "id", aws.ToString(f.Id), "code", aws.ToString(f.Code), "message", aws.ToString(f.Message), "queueURL", queueURL, "component", "SQSConsumer")
	}
}
