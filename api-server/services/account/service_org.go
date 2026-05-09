package account

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	tenantpkg "nudgebee/services/tenant"
)

// AwsOrgOnboard initiates AWS Organization onboarding for a tenant.
// It stores org metadata in tenant_attrs, generates a verification token, and
// returns the StackSet template URL with a quick-create launch URL.
func AwsOrgOnboard(ctx *security.RequestContext, req AwsOrgOnboardRequest) (AwsOrgOnboardResponse, error) {
	err := common.ValidateStruct(req)
	if err != nil {
		return AwsOrgOnboardResponse{}, err
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()

	// Generate 64-char random hex verification token (32 bytes = 64 hex chars)
	verificationToken, err := common.GenerateRandomHexString(32)
	if err != nil {
		return AwsOrgOnboardResponse{}, fmt.Errorf("account: failed to generate verification token: %w", err)
	}

	// SHA-256 hash for secure storage (raw token is returned once, never stored)
	tokenHash := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(verificationToken)))
	tokenPrefix := verificationToken[:8]

	// Upsert tenant_attrs for org metadata
	_, err = tenantpkg.UpsertTenantAttributes(ctx, tenantpkg.TenantAttributeUpsertRequest{
		Object: []tenantpkg.AttributeObject{
			{Name: "aws_org_name", Value: req.AccountName},
			{Name: "aws_org_verification_token_hash", Value: tokenHash},
			{Name: "aws_org_verification_token_prefix", Value: tokenPrefix},
			{Name: "aws_org_status", Value: "pending"},
		},
	})
	if err != nil {
		return AwsOrgOnboardResponse{}, fmt.Errorf("account: failed to store org metadata: %w", err)
	}

	ctx.GetLogger().Info("account: aws org onboarding initiated",
		"tenantId", tenantId,
		"orgName", req.AccountName,
		"tokenPrefix", tokenPrefix,
	)

	// Build URLs
	templateUrl := config.Config.AwsOrgTemplateUrl
	snsTopicArn := config.Config.AwsOrgSnsTopicArn

	// Extract Nudgebee AWS account ID from instance role ARN
	nudgebeeAwsAccountId, err := extractAwsAccountIdFromRoleArn(config.Config.NUDGEBEE_INSTANCE_ROLE)
	if err != nil {
		ctx.GetLogger().Error("account: failed to extract AWS account ID from instance role ARN", "error", err)
		return AwsOrgOnboardResponse{}, fmt.Errorf("account: failed to extract AWS account ID: %w", err)
	}

	// Extract SQS queue name for EventBridge rules
	sqsQueueName := extractQueueNameFromSqsIdentifier(config.Config.CloudCollectorAwsEventbridgeSqs)

	// Build CloudFormation StackSet creation URL
	// StackSets don't support pre-filled parameters via URL like Stacks do,
	// so we pass the template URL and let the user fill parameters from the UI.
	stackSetLaunchUrl := fmt.Sprintf(
		"https://us-east-1.console.aws.amazon.com/cloudformation/home?region=us-east-1#/stacksets/create?templateURL=%s",
		url.QueryEscape(templateUrl),
	)

	return AwsOrgOnboardResponse{
		VerificationToken:   verificationToken,
		StackSetTemplateUrl: templateUrl,
		StackSetLaunchUrl:   stackSetLaunchUrl,
		SnsTopicArn:         snsTopicArn,
		StackSetParameters: map[string]string{
			"NudgebeePingbackArn":       snsTopicArn,
			"NudgebeeVerificationToken": verificationToken,
			"NudgebeeTenantId":          tenantId,
			"NudgebeeAwsAccountId":      nudgebeeAwsAccountId,
			"NudgebeeIamRole":           config.Config.NUDGEBEE_INSTANCE_ROLE,
			"NudgebeeSqsQueueName":      sqsQueueName,
		},
	}, nil
}

// AwsOrgStatus returns the current org onboarding status and all member accounts for a tenant.
func AwsOrgStatus(ctx *security.RequestContext) (AwsOrgStatusResponse, error) {
	tenantId := ctx.GetSecurityContext().GetTenantId()

	// Fetch org metadata from tenant_attrs
	orgAttrs, err := tenantpkg.GetTenantAttributesByName(ctx, "aws_org_name")
	if err != nil || len(orgAttrs) == 0 {
		return AwsOrgStatusResponse{}, fmt.Errorf("account: no org onboarding found for tenant %s", tenantId)
	}
	orgName := orgAttrs[0].Value

	statusAttrs, err := tenantpkg.GetTenantAttributesByName(ctx, "aws_org_status")
	if err != nil || len(statusAttrs) == 0 {
		return AwsOrgStatusResponse{}, fmt.Errorf("account: org status not found for tenant %s", tenantId)
	}
	orgStatus := statusAttrs[0].Value

	// Fetch member accounts (cloud_accounts with aws_org_type=member attr)
	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return AwsOrgStatusResponse{}, fmt.Errorf("account: failed to get database manager: %w", err)
	}

	var members []OrgMemberStatus
	err = manager.Db.Select(&members,
		`SELECT ca.id, ca.account_number, ca.account_name, ca.status, ca.created_at
		FROM cloud_accounts ca
		JOIN cloud_account_attrs caa ON ca.id = caa.cloud_account_id
		WHERE ca.tenant = $1 AND caa.name = 'aws_org_type' AND caa.value = 'member'
		ORDER BY ca.created_at DESC`,
		tenantId,
	)
	if err != nil {
		return AwsOrgStatusResponse{}, fmt.Errorf("account: failed to query org member accounts: %w", err)
	}

	// If we have member accounts and status is still pending, update to active
	if len(members) > 0 && orgStatus == "pending" {
		orgStatus = "active"
		_, _ = tenantpkg.UpsertTenantAttributes(ctx, tenantpkg.TenantAttributeUpsertRequest{
			Object: []tenantpkg.AttributeObject{
				{Name: "aws_org_status", Value: "active"},
			},
		})
	}

	return AwsOrgStatusResponse{
		OrgName:        orgName,
		OrgStatus:      orgStatus,
		MemberAccounts: members,
	}, nil
}

// AwsOrgRefreshToken regenerates the verification token for an existing org onboarding.
func AwsOrgRefreshToken(ctx *security.RequestContext) (AwsOrgRefreshTokenResponse, error) {
	tenantId := ctx.GetSecurityContext().GetTenantId()

	// Verify org exists
	orgAttrs, err := tenantpkg.GetTenantAttributesByName(ctx, "aws_org_name")
	if err != nil || len(orgAttrs) == 0 {
		return AwsOrgRefreshTokenResponse{}, fmt.Errorf("account: no org onboarding found for tenant %s", tenantId)
	}

	// Generate new verification token
	verificationToken, err := common.GenerateRandomHexString(32)
	if err != nil {
		return AwsOrgRefreshTokenResponse{}, fmt.Errorf("account: failed to generate verification token: %w", err)
	}

	tokenHash := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(verificationToken)))
	tokenPrefix := verificationToken[:8]

	// Update token in tenant_attrs
	_, err = tenantpkg.UpsertTenantAttributes(ctx, tenantpkg.TenantAttributeUpsertRequest{
		Object: []tenantpkg.AttributeObject{
			{Name: "aws_org_verification_token_hash", Value: tokenHash},
			{Name: "aws_org_verification_token_prefix", Value: tokenPrefix},
		},
	})
	if err != nil {
		return AwsOrgRefreshTokenResponse{}, fmt.Errorf("account: failed to update verification token: %w", err)
	}

	ctx.GetLogger().Info("account: aws org verification token refreshed",
		"tenantId", tenantId,
		"tokenPrefix", tokenPrefix,
	)

	return AwsOrgRefreshTokenResponse{
		VerificationToken: verificationToken,
	}, nil
}
