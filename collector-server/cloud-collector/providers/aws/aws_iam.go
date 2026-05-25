package aws

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// IAMRecommendationsProvider generates IAM-related security recommendations
type IAMRecommendationsProvider struct {
	DefaultAwsServiceImpl
	IAMClient IAMClientAPI
}

// IAMClientAPI defines methods we use from IAM client (mockable in tests)
type IAMClientAPI interface {
	ListUsers(ctx context.Context, input *iam.ListUsersInput, optFns ...func(*iam.Options)) (*iam.ListUsersOutput, error)
	ListUserTags(ctx context.Context, input *iam.ListUserTagsInput, optFns ...func(*iam.Options)) (*iam.ListUserTagsOutput, error)
	ListRoles(ctx context.Context, input *iam.ListRolesInput, optFns ...func(*iam.Options)) (*iam.ListRolesOutput, error)
	ListRoleTags(ctx context.Context, input *iam.ListRoleTagsInput, optFns ...func(*iam.Options)) (*iam.ListRoleTagsOutput, error)
	ListGroups(ctx context.Context, input *iam.ListGroupsInput, optFns ...func(*iam.Options)) (*iam.ListGroupsOutput, error)
	ListMFADevices(ctx context.Context, input *iam.ListMFADevicesInput, optFns ...func(*iam.Options)) (*iam.ListMFADevicesOutput, error)
	GetAccountSummary(ctx context.Context, input *iam.GetAccountSummaryInput, optFns ...func(*iam.Options)) (*iam.GetAccountSummaryOutput, error)
	GetAccountPasswordPolicy(ctx context.Context, input *iam.GetAccountPasswordPolicyInput, optFns ...func(*iam.Options)) (*iam.GetAccountPasswordPolicyOutput, error)
	ListAccessKeys(ctx context.Context, input *iam.ListAccessKeysInput, optFns ...func(*iam.Options)) (*iam.ListAccessKeysOutput, error)
	ListAttachedUserPolicies(ctx context.Context, input *iam.ListAttachedUserPoliciesInput, optFns ...func(*iam.Options)) (*iam.ListAttachedUserPoliciesOutput, error)
	ListUserPolicies(ctx context.Context, input *iam.ListUserPoliciesInput, optFns ...func(*iam.Options)) (*iam.ListUserPoliciesOutput, error)
	GetAccessKeyLastUsed(ctx context.Context, input *iam.GetAccessKeyLastUsedInput, optFns ...func(*iam.Options)) (*iam.GetAccessKeyLastUsedOutput, error)
}

func (p *IAMRecommendationsProvider) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (p *IAMRecommendationsProvider) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (p *IAMRecommendationsProvider) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

// IsGlobal reports IAM as a global service. ListUsers/ListRoles/ListGroups
// return account-wide results regardless of region; the central ListResources
// short-circuit calls GetResources exactly once.
func (p *IAMRecommendationsProvider) IsGlobal() bool {
	return true
}

func (p *IAMRecommendationsProvider) GetResources(ctx providers.CloudProviderContext, account providers.Account, _ string) ([]providers.Resource, error) {
	client, err := getIAMClient(ctx, account)
	if err != nil {
		return nil, err
	}

	var resources []providers.Resource

	// --- Get IAM Users ---
	userPaginator := iam.NewListUsersPaginator(client, &iam.ListUsersInput{})
	for userPaginator.HasMorePages() {
		usersOutput, err := userPaginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to list iam users", "error", err, "accountNumber", account.AccountNumber)
			return resources, err
		}

		for _, user := range usersOutput.Users {
			if user.UserName == nil || user.UserId == nil || user.Arn == nil || user.CreateDate == nil {
				continue
			}
			tagsOutput, err := client.ListUserTags(ctx.GetContext(), &iam.ListUserTagsInput{UserName: user.UserName})
			tags := make(map[string][]string)
			if err != nil {
				ctx.GetLogger().Warn("failed to list tags for iam user", "user", *user.UserName, "error", err)
			} else {
				for _, tag := range tagsOutput.Tags {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}

			resources = append(resources, providers.Resource{
				Id:          *user.UserName,
				Name:        *user.UserName,
				Arn:         *user.Arn,
				Type:        "User",
				ServiceName: ServiceNameIAM,
				Status:      providers.ResourceStatusActive,
				Region:      "global",
				Tags:        tags,
				Meta:        structToMap(user),
				CreatedAt:   *user.CreateDate,
			})
		}
	}

	// --- Get IAM Roles ---
	rolePaginator := iam.NewListRolesPaginator(client, &iam.ListRolesInput{})
	for rolePaginator.HasMorePages() {
		rolesOutput, err := rolePaginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to list iam roles", "error", err, "accountNumber", account.AccountNumber)
			return resources, err
		}

		for _, role := range rolesOutput.Roles {
			if role.RoleName == nil || role.RoleId == nil || role.Arn == nil || role.CreateDate == nil {
				continue
			}
			tagsOutput, err := client.ListRoleTags(ctx.GetContext(), &iam.ListRoleTagsInput{RoleName: role.RoleName})
			tags := make(map[string][]string)
			if err != nil {
				ctx.GetLogger().Warn("failed to list tags for iam role", "role", *role.RoleName, "error", err)
			} else {
				for _, tag := range tagsOutput.Tags {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}

			resources = append(resources, providers.Resource{
				Id:          *role.RoleName,
				Name:        *role.RoleName,
				Arn:         *role.Arn,
				Type:        "Role",
				ServiceName: ServiceNameIAM,
				Status:      providers.ResourceStatusActive,
				Region:      "global",
				Tags:        tags,
				Meta:        structToMap(role),
				CreatedAt:   *role.CreateDate,
			})
		}
	}

	// --- Get IAM Groups ---
	groupPaginator := iam.NewListGroupsPaginator(client, &iam.ListGroupsInput{})
	for groupPaginator.HasMorePages() {
		groupsOutput, err := groupPaginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to list iam groups", "error", err, "accountNumber", account.AccountNumber)
			return resources, err
		}

		for _, group := range groupsOutput.Groups {
			if group.GroupName == nil || group.GroupId == nil || group.Arn == nil || group.CreateDate == nil {
				continue
			}
			// IAM Groups do not support tags directly.
			resources = append(resources, providers.Resource{
				Id:          *group.GroupName,
				Name:        *group.GroupName,
				Arn:         *group.Arn,
				Type:        "Group",
				ServiceName: ServiceNameIAM,
				Status:      providers.ResourceStatusActive,
				Region:      "global",
				Tags:        make(map[string][]string), // No tags for groups
				Meta:        structToMap(group),
				CreatedAt:   *group.CreateDate,
			})
		}
	}

	return resources, nil
}

func (p *IAMRecommendationsProvider) GetRecommendations(
	ctx providers.CloudProviderContext,
	account providers.Account,
	filter providers.ListRecommendationsRequest,
	existingResources []providers.Resource,
) ([]providers.Recommendation, error) {

	var client IAMClientAPI
	if p.IAMClient != nil {
		client = p.IAMClient // Use mock for testing
	} else {
		realClient, err := getIAMClient(ctx, account)
		if err != nil {
			return nil, err
		}
		client = realClient
	}

	var recs []providers.Recommendation

	checks := []func(IAMClientAPI, providers.CloudProviderContext, providers.Account) ([]providers.Recommendation, error){
		checkUserMFA, // This function now needs context
		checkAccessKeyRotation,
		checkPasswordPolicy,
		checkRootAccountSecurity,
		checkInactiveUsers,
		checkAdminPolicyUsage,
		checkInlinePolicies,
		checkExcessiveAccessKeys,
		checkUnusedAccessKeys,
	}

	for _, check := range checks {
		results, err := check(client, ctx, account) // Pass context down
		if err != nil {
			ctx.GetLogger().Warn("IAM security check failed", "error", err)
			continue
		}
		recs = append(recs, results...)
	}

	return recs, nil
}

type iamClientWrapper struct {
	*iam.Client
}

func (w *iamClientWrapper) ListUsers(ctx context.Context, input *iam.ListUsersInput, optFns ...func(*iam.Options)) (*iam.ListUsersOutput, error) {
	return w.Client.ListUsers(ctx, input, optFns...)
}
func (w *iamClientWrapper) ListUserTags(ctx context.Context, input *iam.ListUserTagsInput, optFns ...func(*iam.Options)) (*iam.ListUserTagsOutput, error) {
	return w.Client.ListUserTags(ctx, input, optFns...)
}
func (w *iamClientWrapper) ListRoles(ctx context.Context, input *iam.ListRolesInput, optFns ...func(*iam.Options)) (*iam.ListRolesOutput, error) {
	return w.Client.ListRoles(ctx, input, optFns...)
}
func (w *iamClientWrapper) ListRoleTags(ctx context.Context, input *iam.ListRoleTagsInput, optFns ...func(*iam.Options)) (*iam.ListRoleTagsOutput, error) {
	return w.Client.ListRoleTags(ctx, input, optFns...)
}
func (w *iamClientWrapper) ListGroups(ctx context.Context, input *iam.ListGroupsInput, optFns ...func(*iam.Options)) (*iam.ListGroupsOutput, error) {
	return w.Client.ListGroups(ctx, input, optFns...)
}
func (w *iamClientWrapper) ListMFADevices(ctx context.Context, input *iam.ListMFADevicesInput, optFns ...func(*iam.Options)) (*iam.ListMFADevicesOutput, error) {
	return w.Client.ListMFADevices(ctx, input, optFns...)
}
func (w *iamClientWrapper) GetAccountSummary(ctx context.Context, input *iam.GetAccountSummaryInput, optFns ...func(*iam.Options)) (*iam.GetAccountSummaryOutput, error) {
	return w.Client.GetAccountSummary(ctx, input, optFns...)
}
func (w *iamClientWrapper) GetAccountPasswordPolicy(ctx context.Context, input *iam.GetAccountPasswordPolicyInput, optFns ...func(*iam.Options)) (*iam.GetAccountPasswordPolicyOutput, error) {
	return w.Client.GetAccountPasswordPolicy(ctx, input, optFns...)
}
func (w *iamClientWrapper) ListAccessKeys(ctx context.Context, input *iam.ListAccessKeysInput, optFns ...func(*iam.Options)) (*iam.ListAccessKeysOutput, error) {
	return w.Client.ListAccessKeys(ctx, input, optFns...)
}
func (w *iamClientWrapper) ListAttachedUserPolicies(ctx context.Context, input *iam.ListAttachedUserPoliciesInput, optFns ...func(*iam.Options)) (*iam.ListAttachedUserPoliciesOutput, error) {
	return w.Client.ListAttachedUserPolicies(ctx, input, optFns...)
}
func (w *iamClientWrapper) ListUserPolicies(ctx context.Context, input *iam.ListUserPoliciesInput, optFns ...func(*iam.Options)) (*iam.ListUserPoliciesOutput, error) {
	return w.Client.ListUserPolicies(ctx, input, optFns...)
}
func (w *iamClientWrapper) GetAccessKeyLastUsed(ctx context.Context, input *iam.GetAccessKeyLastUsedInput, optFns ...func(*iam.Options)) (*iam.GetAccessKeyLastUsedOutput, error) {
	return w.Client.GetAccessKeyLastUsed(ctx, input, optFns...)
}

func getIAMClient(ctx providers.CloudProviderContext, account providers.Account) (IAMClientAPI, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Warn("failed to create AWS session", "error", err)
		return nil, err
	}
	return &iamClientWrapper{iam.NewFromConfig(cfg)}, nil
}

// 1. MFA enabled for users
func checkUserMFA(client IAMClientAPI, ctx providers.CloudProviderContext, account providers.Account) ([]providers.Recommendation, error) {
	var recs []providers.Recommendation

	users, err := client.ListUsers(ctx.GetContext(), &iam.ListUsersInput{})
	if err != nil {
		return nil, err
	}

	for _, user := range users.Users {
		mfa, err := client.ListMFADevices(ctx.GetContext(), &iam.ListMFADevicesInput{
			UserName: user.UserName,
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to list MFA devices", "user", *user.UserName, "error", err)
			continue
		}
		if len(mfa.MFADevices) == 0 {
			recs = append(recs, buildRec("iam_mfa_not_enabled", "User", *user.UserName,
				"MFA not enabled for IAM user: "+*user.UserName,
				providers.RecommendationSeverityHigh))
		}
	}
	return recs, nil
}

// 2. Access key rotation (older than 90 days)
func checkAccessKeyRotation(client IAMClientAPI, ctx providers.CloudProviderContext, account providers.Account) ([]providers.Recommendation, error) {
	var recs []providers.Recommendation

	users, err := client.ListUsers(ctx.GetContext(), &iam.ListUsersInput{})
	if err != nil {
		return nil, err
	}

	for _, user := range users.Users {
		keys, err := client.ListAccessKeys(ctx.GetContext(), &iam.ListAccessKeysInput{UserName: user.UserName})
		if err != nil {
			ctx.GetLogger().Warn("failed to list access keys", "user", *user.UserName, "error", err)
			continue
		}

		for _, key := range keys.AccessKeyMetadata {
			if key.CreateDate != nil {
				age := time.Since(*key.CreateDate)
				if age.Hours() > 24*90 {
					recs = append(recs, buildRec("iam_access_key_rotation", "User", *user.UserName,
						"Access key "+*key.AccessKeyId+" for user "+*user.UserName+" is older than 90 days",
						providers.RecommendationSeverityMedium))
				}
			}
		}
	}
	return recs, nil
}

// 3. Password policy
func checkPasswordPolicy(client IAMClientAPI, ctx providers.CloudProviderContext, account providers.Account) ([]providers.Recommendation, error) {
	var recs []providers.Recommendation

	output, err := client.GetAccountPasswordPolicy(ctx.GetContext(), &iam.GetAccountPasswordPolicyInput{})
	if err != nil {
		ctx.GetLogger().Warn("no password policy set", "error", err)
		recs = append(recs, buildRec("iam_password_policy_missing", "AccountPasswordPolicy", account.AccountNumber,
			"No account password policy is set", providers.RecommendationSeverityHigh))
		return recs, nil
	}

	p := output.PasswordPolicy
	if p.MinimumPasswordLength == nil || *p.MinimumPasswordLength < 12 {
		recs = append(recs, buildRec("iam_password_policy_length", "AccountPasswordPolicy", account.AccountNumber,
			"Password policy minimum length is less than 12", providers.RecommendationSeverityMedium))
	}
	if !p.RequireUppercaseCharacters {
		recs = append(recs, buildRec("iam_password_policy_uppercase", "AccountPasswordPolicy", account.AccountNumber,
			"Password policy does not enforce uppercase characters", providers.RecommendationSeverityLow))
	}
	if !p.RequireLowercaseCharacters {
		recs = append(recs, buildRec("iam_password_policy_lowercase", "AccountPasswordPolicy", account.AccountNumber,
			"Password policy does not enforce lowercase characters", providers.RecommendationSeverityLow))
	}
	if !p.RequireSymbols {
		recs = append(recs, buildRec("iam_password_policy_symbols", "AccountPasswordPolicy", account.AccountNumber,
			"Password policy does not enforce special characters", providers.RecommendationSeverityLow))
	}
	if !p.RequireNumbers {
		recs = append(recs, buildRec("iam_password_policy_numbers", "AccountPasswordPolicy", account.AccountNumber,
			"Password policy does not enforce numeric characters", providers.RecommendationSeverityLow))
	}
	if p.PasswordReusePrevention != nil && *p.PasswordReusePrevention < 5 {
		recs = append(recs, buildRec("iam_password_policy_reuse", "AccountPasswordPolicy", account.AccountNumber,
			"Password reuse prevention is less than 5", providers.RecommendationSeverityLow))
	}
	if p.MaxPasswordAge != nil && *p.MaxPasswordAge > 90 {
		recs = append(recs, buildRec("iam_password_policy_max_age", "AccountPasswordPolicy", account.AccountNumber,
			"Password expiration age is greater than 90 days", providers.RecommendationSeverityLow))
	}

	return recs, nil
}

// 4. Root account checks
func checkRootAccountSecurity(client IAMClientAPI, ctx providers.CloudProviderContext, account providers.Account) ([]providers.Recommendation, error) {
	var recs []providers.Recommendation

	summary, err := client.GetAccountSummary(ctx.GetContext(), &iam.GetAccountSummaryInput{})
	if err != nil {
		return nil, err
	}

	if v, ok := summary.SummaryMap["AccountMFAEnabled"]; ok && v == 0 {
		recs = append(recs, buildRec("iam_root_mfa_not_enabled", "Root", account.AccountNumber,
			"Root account does not have MFA enabled", providers.RecommendationSeverityHigh))
	}
	if v, ok := summary.SummaryMap["AccountAccessKeysPresent"]; ok && v > 0 {
		recs = append(recs, buildRec("iam_root_access_keys_present", "Root", account.AccountNumber,
			"Root account has active access keys", providers.RecommendationSeverityHigh))
	}

	return recs, nil
}

// 5. Inactive users (90+ days without login)
func checkInactiveUsers(client IAMClientAPI, ctx providers.CloudProviderContext, account providers.Account) ([]providers.Recommendation, error) {
	var recs []providers.Recommendation

	users, err := client.ListUsers(ctx.GetContext(), &iam.ListUsersInput{})
	if err != nil {
		return nil, err
	}

	for _, user := range users.Users {
		if user.PasswordLastUsed != nil {
			age := time.Since(*user.PasswordLastUsed)
			if age.Hours() > 24*90 {
				recs = append(recs, buildRec("iam_inactive_user", "User", *user.UserName,
					"IAM user "+*user.UserName+" has not logged in for "+strconv.Itoa(int(age.Hours()/24))+" days",
					providers.RecommendationSeverityMedium))
			}
		}
	}
	return recs, nil
}

// 6. Users with admin policies
func checkAdminPolicyUsage(client IAMClientAPI, ctx providers.CloudProviderContext, account providers.Account) ([]providers.Recommendation, error) {
	var recs []providers.Recommendation

	users, err := client.ListUsers(ctx.GetContext(), &iam.ListUsersInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}

	for _, user := range users.Users {
		policies, err := client.ListAttachedUserPolicies(ctx.GetContext(), &iam.ListAttachedUserPoliciesInput{UserName: user.UserName})
		if err != nil {
			ctx.GetLogger().Warn("failed to list attached user policies", "user", *user.UserName, "error", err)
			continue
		}
		for _, pol := range policies.AttachedPolicies {
			if pol.PolicyName != nil && *pol.PolicyName == "AdministratorAccess" {
				recs = append(recs, buildRec("iam_user_admin_policy", "User", *user.UserName,
					"User "+*user.UserName+" has AdministratorAccess policy",
					providers.RecommendationSeverityHigh))
			}
		}
	}
	return recs, nil
}

// 7. Inline policies
func checkInlinePolicies(client IAMClientAPI, ctx providers.CloudProviderContext, account providers.Account) ([]providers.Recommendation, error) {
	var recs []providers.Recommendation

	users, err := client.ListUsers(ctx.GetContext(), &iam.ListUsersInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}

	for _, user := range users.Users {
		inline, err := client.ListUserPolicies(ctx.GetContext(), &iam.ListUserPoliciesInput{UserName: user.UserName})
		if err != nil {
			ctx.GetLogger().Warn("failed to list inline policies", "user", *user.UserName, "error", err)
			continue
		}
		if len(inline.PolicyNames) > 0 {
			recs = append(recs, buildRec("iam_inline_policies", "User", *user.UserName,
				fmt.Sprintf("User %s has inline policies: %v", *user.UserName, inline.PolicyNames),
				providers.RecommendationSeverityMedium))
		}
	}
	return recs, nil
}

// 8. Excessive access keys
func checkExcessiveAccessKeys(client IAMClientAPI, ctx providers.CloudProviderContext, account providers.Account) ([]providers.Recommendation, error) {
	var recs []providers.Recommendation

	users, err := client.ListUsers(ctx.GetContext(), &iam.ListUsersInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to list users for excessive key check: %w", err)
	}
	for _, user := range users.Users {
		keys, err := client.ListAccessKeys(ctx.GetContext(), &iam.ListAccessKeysInput{UserName: user.UserName})
		if err != nil {
			ctx.GetLogger().Warn("failed to list access keys for excessive key check", "user", *user.UserName, "error", err)
			continue
		}
		if len(keys.AccessKeyMetadata) > 2 {
			recs = append(recs, buildRec("iam_excessive_keys", "User", *user.UserName,
				"User "+*user.UserName+" has more than 2 access keys",
				providers.RecommendationSeverityLow))
		}
	}
	return recs, nil
}

// 9. Unused access keys
func checkUnusedAccessKeys(client IAMClientAPI, ctx providers.CloudProviderContext, account providers.Account) ([]providers.Recommendation, error) {
	var recs []providers.Recommendation

	users, err := client.ListUsers(ctx.GetContext(), &iam.ListUsersInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to list users for unused key check: %w", err)
	}
	for _, user := range users.Users {
		keys, err := client.ListAccessKeys(ctx.GetContext(), &iam.ListAccessKeysInput{UserName: user.UserName})
		if err != nil {
			ctx.GetLogger().Warn("failed to list access keys for unused key check", "user", *user.UserName, "error", err)
			continue
		}
		for _, key := range keys.AccessKeyMetadata {
			lastUsedOutput, err := client.GetAccessKeyLastUsed(ctx.GetContext(), &iam.GetAccessKeyLastUsedInput{AccessKeyId: key.AccessKeyId})
			if err != nil {
				ctx.GetLogger().Warn("failed to get access key last used", "keyId", *key.AccessKeyId, "error", err)
				continue
			}
			if lastUsedOutput.AccessKeyLastUsed != nil && lastUsedOutput.AccessKeyLastUsed.LastUsedDate != nil {
				if time.Since(*lastUsedOutput.AccessKeyLastUsed.LastUsedDate).Hours() > 24*90 {
					recs = append(recs, buildRec("iam_unused_access_key", "User", *user.UserName,
						"Access key "+*key.AccessKeyId+" for user "+*user.UserName+" has not been used in over 90 days",
						providers.RecommendationSeverityMedium))
				}
			} else { // Never used
				if time.Since(*key.CreateDate).Hours() > 24*90 {
					recs = append(recs, buildRec("iam_unused_access_key", "User", *user.UserName,
						"Access key "+*key.AccessKeyId+" for user "+*user.UserName+" has never been used and is over 90 days old",
						providers.RecommendationSeverityMedium))
				}
			}
		}
	}
	return recs, nil
}

// Helpers

func buildRec(rule, rType, rID, msg string, severity providers.RecommendationSeverity) providers.Recommendation {
	return providers.Recommendation{
		RuleName:            rule,
		CategoryName:        providers.RecommendationCategorySecurity,
		Severity:            severity,
		Action:              providers.RecommendationActionModify,
		ResourceServiceName: "AWSIAM",
		ResourceType:        rType,
		ResourceId:          rID,
		ResourceRegion:      "global",
		Data:                map[string]any{"reason": msg},
	}
}

func (p *IAMRecommendationsProvider) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return "", err
	}
	regionalCfg := cfg.Copy()
	regionalCfg.Region = region
	logsSvc := cloudwatchlogs.NewFromConfig(regionalCfg)

	var foundLogGroup string
	paginator := cloudwatchlogs.NewDescribeLogGroupsPaginator(logsSvc, &cloudwatchlogs.DescribeLogGroupsInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.TODO())
		if err != nil {
			return "", err
		}
		for _, lg := range page.LogGroups {
			logGroupName := *lg.LogGroupName
			describeLogStreamsOutput, err := logsSvc.DescribeLogStreams(context.TODO(), &cloudwatchlogs.DescribeLogStreamsInput{
				LogGroupName:        &logGroupName,
				LogStreamNamePrefix: &resourceId,
				Limit:               aws.Int32(1),
			})
			if err == nil && len(describeLogStreamsOutput.LogStreams) > 0 {
				foundLogGroup = logGroupName
				return foundLogGroup, nil
			}
		}
	}
	return foundLogGroup, nil
}

func (p *IAMRecommendationsProvider) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "AWSIAM",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	return app, nil
}
