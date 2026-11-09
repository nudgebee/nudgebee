package aws

import (
	"context"
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/smithy-go/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsAWSUniqueIDTagKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want bool
	}{
		// The AWS console tags an IAM user with its own access key IDs.
		{name: "access key id", key: "AKIAIOSFODNN7EXAMPLE", want: true},
		{name: "temporary access key id", key: "ASIAIOSFODNN7EXAMPLE", want: true},
		{name: "role id", key: "AROAEXAMPLEEXAMPLE12", want: true},
		// Tag names an engineer would actually set.
		{name: "ordinary name tag", key: "Name", want: false},
		{name: "ordinary environment tag", key: "environment", want: false},
		{name: "unknown prefix of the right shape", key: "BKIAIOSFODNN7EXAMPLE", want: false},
		{name: "too short", key: "AKIAIOSFODNN7EXAMPL", want: false},
		{name: "too long", key: "AKIAIOSFODNN7EXAMPLE1", want: false},
		{name: "lowercase is not an identifier", key: "akiaiosfodnn7example", want: false},
		{name: "right length but punctuated", key: "AKIA-EXAMPLE-TAG-KEY", want: false},
		{name: "empty", key: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isAWSUniqueIDTagKey(tt.key))
		})
	}
}

// TestGetResourcesDropsAccessKeyIDTags proves the access key ID never reaches
// cloud_resourses.tags, so it cannot be published as a label name downstream,
// while ordinary tags on the same user and role survive.
func TestGetResourcesDropsAccessKeyIDTags(t *testing.T) {
	now := time.Now()
	provider := &IAMRecommendationsProvider{
		IAMClient: &MockIAMClient{
			ListUsersOutput: &iam.ListUsersOutput{Users: []types.User{{
				UserName:   ptr.String("deploy-bot"),
				UserId:     ptr.String("AIDAEXAMPLEEXAMPLE12"),
				Arn:        ptr.String("arn:aws:iam::123456789012:user/deploy-bot"),
				CreateDate: &now,
			}}},
			ListUserTagsOutput: &iam.ListUserTagsOutput{Tags: []types.Tag{
				{Key: ptr.String("AKIAIOSFODNN7EXAMPLE"), Value: ptr.String("[ci key]")},
				{Key: ptr.String("Name"), Value: ptr.String("deploy-bot")},
			}},
			ListRolesOutput: &iam.ListRolesOutput{Roles: []types.Role{{
				RoleName:   ptr.String("deploy-role"),
				RoleId:     ptr.String("AROAEXAMPLEEXAMPLE12"),
				Arn:        ptr.String("arn:aws:iam::123456789012:role/deploy-role"),
				CreateDate: &now,
			}}},
			ListRoleTagsOutput: &iam.ListRoleTagsOutput{Tags: []types.Tag{
				{Key: ptr.String("ASIAIOSFODNN7EXAMPLE"), Value: ptr.String("[temp key]")},
				{Key: ptr.String("team"), Value: ptr.String("platform")},
			}},
			ListGroupsOutput: &iam.ListGroupsOutput{Groups: []types.Group{}},
		},
	}

	resources, err := provider.GetResources(
		providers.NewCloudProviderContext(context.Background()),
		providers.Account{AccountNumber: "123456789012"},
		"",
	)
	require.NoError(t, err)

	byType := make(map[string]providers.Resource, len(resources))
	for _, r := range resources {
		byType[r.Type] = r
	}

	user, ok := byType["User"]
	require.True(t, ok, "expected a User resource")
	assert.Equal(t, map[string][]string{"Name": {"deploy-bot"}}, user.Tags)

	role, ok := byType["Role"]
	require.True(t, ok, "expected a Role resource")
	assert.Equal(t, map[string][]string{"team": {"platform"}}, role.Tags)
}
