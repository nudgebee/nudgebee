package aws

import (
	"context"
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/smithy-go/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockIAMClient implements the parts of the IAMClientAPI interface needed for tests.
type MockIAMClient struct {
	ListUsersOutput                *iam.ListUsersOutput
	ListMFADevicesOutput           *iam.ListMFADevicesOutput
	ListAccessKeysOutput           *iam.ListAccessKeysOutput
	GetAccountSummaryOutput        *iam.GetAccountSummaryOutput
	GetAccountPasswordPolicyOutput *iam.GetAccountPasswordPolicyOutput
	ListUserPoliciesOutput         *iam.ListUserPoliciesOutput
	ListAttachedUserPoliciesOutput *iam.ListAttachedUserPoliciesOutput
	GetAccessKeyLastUsedOutput     *iam.GetAccessKeyLastUsedOutput
	ListUserTagsOutput             *iam.ListUserTagsOutput
	ListRolesOutput                *iam.ListRolesOutput
	ListRoleTagsOutput             *iam.ListRoleTagsOutput
	ListGroupsOutput               *iam.ListGroupsOutput
}

func (m *MockIAMClient) ListUsers(ctx context.Context, input *iam.ListUsersInput, optFns ...func(*iam.Options)) (*iam.ListUsersOutput, error) {
	return m.ListUsersOutput, nil
}

func (m *MockIAMClient) ListUserTags(ctx context.Context, input *iam.ListUserTagsInput, optFns ...func(*iam.Options)) (*iam.ListUserTagsOutput, error) {
	return m.ListUserTagsOutput, nil
}

func (m *MockIAMClient) ListRoles(ctx context.Context, input *iam.ListRolesInput, optFns ...func(*iam.Options)) (*iam.ListRolesOutput, error) {
	return m.ListRolesOutput, nil
}

func (m *MockIAMClient) ListRoleTags(ctx context.Context, input *iam.ListRoleTagsInput, optFns ...func(*iam.Options)) (*iam.ListRoleTagsOutput, error) {
	return m.ListRoleTagsOutput, nil
}

func (m *MockIAMClient) ListGroups(ctx context.Context, input *iam.ListGroupsInput, optFns ...func(*iam.Options)) (*iam.ListGroupsOutput, error) {
	return m.ListGroupsOutput, nil
}

func (m *MockIAMClient) ListMFADevices(ctx context.Context, input *iam.ListMFADevicesInput, optFns ...func(*iam.Options)) (*iam.ListMFADevicesOutput, error) {
	return m.ListMFADevicesOutput, nil
}

func (m *MockIAMClient) GetAccountSummary(ctx context.Context, input *iam.GetAccountSummaryInput, optFns ...func(*iam.Options)) (*iam.GetAccountSummaryOutput, error) {
	if m.GetAccountSummaryOutput == nil {
		// Return a default empty summary to avoid nil pointer panics in tests that don't set this.
		return &iam.GetAccountSummaryOutput{
			SummaryMap: make(map[string]int32),
		}, nil
	}
	return m.GetAccountSummaryOutput, nil
}

func (m *MockIAMClient) GetAccountPasswordPolicy(ctx context.Context, input *iam.GetAccountPasswordPolicyInput, optFns ...func(*iam.Options)) (*iam.GetAccountPasswordPolicyOutput, error) {
	if m.GetAccountPasswordPolicyOutput == nil {
		// Simulate "policy not found" error
		return nil, &types.NoSuchEntityException{}
	}
	return m.GetAccountPasswordPolicyOutput, nil
}

func (m *MockIAMClient) ListAccessKeys(ctx context.Context, input *iam.ListAccessKeysInput, optFns ...func(*iam.Options)) (*iam.ListAccessKeysOutput, error) {
	if m.ListAccessKeysOutput == nil {
		return &iam.ListAccessKeysOutput{AccessKeyMetadata: []types.AccessKeyMetadata{}}, nil
	}
	return m.ListAccessKeysOutput, nil
}

func (m *MockIAMClient) ListAttachedUserPolicies(ctx context.Context, input *iam.ListAttachedUserPoliciesInput, optFns ...func(*iam.Options)) (*iam.ListAttachedUserPoliciesOutput, error) {
	if m.ListAttachedUserPoliciesOutput == nil {
		return &iam.ListAttachedUserPoliciesOutput{AttachedPolicies: []types.AttachedPolicy{}}, nil
	}
	return m.ListAttachedUserPoliciesOutput, nil
}

func (m *MockIAMClient) ListUserPolicies(ctx context.Context, input *iam.ListUserPoliciesInput, optFns ...func(*iam.Options)) (*iam.ListUserPoliciesOutput, error) {
	if m.ListUserPoliciesOutput == nil {
		return &iam.ListUserPoliciesOutput{PolicyNames: []string{}}, nil
	}
	return m.ListUserPoliciesOutput, nil
}

func (m *MockIAMClient) GetAccessKeyLastUsed(ctx context.Context, input *iam.GetAccessKeyLastUsedInput, optFns ...func(*iam.Options)) (*iam.GetAccessKeyLastUsedOutput, error) {
	if m.GetAccessKeyLastUsedOutput == nil {
		return &iam.GetAccessKeyLastUsedOutput{AccessKeyLastUsed: &types.AccessKeyLastUsed{}}, nil
	}
	return m.GetAccessKeyLastUsedOutput, nil
}

func TestRecommendMFA(t *testing.T) {
	pCtx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{AccountNumber: "123456789012"}

	mockIAM := &MockIAMClient{
		ListUsersOutput: &iam.ListUsersOutput{
			Users: []types.User{
				{
					UserName: ptr.String("test-user"),
					UserId:   ptr.String("test-user-id"),
				},
			},
		},
		ListMFADevicesOutput: &iam.ListMFADevicesOutput{
			MFADevices: []types.MFADevice{},
		},
	}
	provider := IAMRecommendationsProvider{IAMClient: mockIAM}

	recs, err := provider.GetRecommendations(pCtx, account, providers.ListRecommendationsRequest{}, nil)

	assert.NoError(t, err)
	// Filter for the specific recommendation we're testing
	mfaRecs := []providers.Recommendation{}
	for _, r := range recs {
		if r.RuleName == "iam_mfa_not_enabled" {
			mfaRecs = append(mfaRecs, r)
		}
	}

	assert.Len(t, mfaRecs, 1, "Expected exactly one MFA recommendation")
	assert.Equal(t, "test-user", mfaRecs[0].ResourceId)
	assert.Equal(t, providers.RecommendationSeverityHigh, mfaRecs[0].Severity)
	assert.Contains(t, mfaRecs[0].Data["reason"], "MFA not enabled for IAM user")
}

func TestRecommendNoMFA(t *testing.T) {
	pCtx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{AccountNumber: "123456789012"}

	mockIAM := &MockIAMClient{
		ListUsersOutput: &iam.ListUsersOutput{
			Users: []types.User{
				{
					UserName: ptr.String("test-user"),
					UserId:   ptr.String("test-user-id"),
				},
			},
		},
		ListMFADevicesOutput: &iam.ListMFADevicesOutput{
			MFADevices: []types.MFADevice{
				{
					UserName:     ptr.String("test-user"),
					SerialNumber: ptr.String("arn:aws:iam::864186153326:mfa/test-user"),
				},
			},
		},
	}
	provider := IAMRecommendationsProvider{IAMClient: mockIAM}
	recs, err := provider.GetRecommendations(pCtx, account, providers.ListRecommendationsRequest{}, nil)

	assert.NoError(t, err)
	mfaRecs := []providers.Recommendation{}
	for _, r := range recs {
		if r.RuleName == "iam_mfa_not_enabled" {
			mfaRecs = append(mfaRecs, r)
		}
	}
	assert.Len(t, mfaRecs, 0, "Expected no MFA recommendations since it is enabled")
}

func TestGetIAMRecommendations(t *testing.T) {
	region := "us-east-1"
	account := providers.Account{Region: &region,
		AccountNumber: os.Getenv("TEST_ACCOUNT"),
		AccessKey:     ptr.String(os.Getenv("TEST_ACCESS_KEY")),
		AccessSecret:  ptr.String(os.Getenv("TEST_SECRET_KEY")),
	}
	pCtx := providers.NewCloudProviderContext(context.Background())

	provider := IAMRecommendationsProvider{}

	recs, err := provider.GetRecommendations(pCtx, account, providers.ListRecommendationsRequest{}, nil)

	require.NoError(t, err, "GetRecommendations should not fail")
	for _, rec := range recs {
		data, _ := common.MarshalJson(rec)
		fmt.Println(string(data))
	}
}
