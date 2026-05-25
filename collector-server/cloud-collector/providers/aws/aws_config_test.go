package aws

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/configservice/types"
	"github.com/stretchr/testify/assert"
)

func TestConfigRecorderStatusToNbStatus(t *testing.T) {
	tests := []struct {
		name      string
		recording bool
		expected  providers.ResourceStatus
	}{
		{"recording", true, providers.ResourceStatusActive},
		{"not_recording", false, providers.ResourceStatusInactive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := configRecorderStatusToNbStatus(tt.recording)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfigRuleComplianceToNbStatus(t *testing.T) {
	tests := []struct {
		name       string
		compliance types.ComplianceType
		expected   providers.ResourceStatus
	}{
		{"compliant", types.ComplianceTypeCompliant, providers.ResourceStatusActive},
		{"non_compliant", types.ComplianceTypeNonCompliant, providers.ResourceStatusInactive},
		{"not_applicable", types.ComplianceTypeNotApplicable, providers.ResourceStatusUnknown},
		{"insufficient_data", types.ComplianceTypeInsufficientData, providers.ResourceStatusUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := configRuleComplianceToNbStatus(tt.compliance)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func stringPtr(s string) *string {
	return &s
}

func TestConfigGetResources(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")

	config := &awsConfig{}
	account := providers.Account{
		AccountNumber: "123456789012",
		Region:        stringPtr("us-east-1"),
	}

	ctx := providers.NewCloudProviderContext(context.Background())

	resources, err := config.GetResources(ctx, account, "us-east-1")
	assert.NoError(t, err)
	assert.NotNil(t, resources)
}

func TestConfigGetRecommendations(t *testing.T) {
	config := &awsConfig{}

	tests := []struct {
		name          string
		resources     []providers.Resource
		expectedCount int
	}{
		{
			name: "recorder not recording",
			resources: []providers.Resource{
				{
					Id:          "recorder-1",
					ServiceName: ServiceNameConfig,
					Meta: map[string]any{
						"RecorderStatus": map[string]any{
							"Recording": false,
						},
					},
					Type: getAwsServiceResourceType(ServiceNameConfig, "recorder"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "no delivery channel",
			resources: []providers.Resource{
				{
					Id:          "recorder-2",
					ServiceName: ServiceNameConfig,
					Meta: map[string]any{
						"RecorderStatus": map[string]any{
							"Recording": true,
						},
					},
					Type: getAwsServiceResourceType(ServiceNameConfig, "recorder"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "not recording all resources",
			resources: []providers.Resource{
				{
					Id:          "recorder-3",
					ServiceName: ServiceNameConfig,
					Meta: map[string]any{
						"RecorderStatus": map[string]any{
							"Recording": true,
						},
						"RecordingGroup": map[string]any{
							"AllSupported": false,
						},
					},
					Type: getAwsServiceResourceType(ServiceNameConfig, "recorder"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "non compliant rule",
			resources: []providers.Resource{
				{
					Id:          "rule-1",
					ServiceName: ServiceNameConfig,
					Name:        "test-rule",
					Meta: map[string]any{
						"Compliance": map[string]any{
							"ComplianceType": "NON_COMPLIANT",
						},
					},
					Type: getAwsServiceResourceType(ServiceNameConfig, "rule"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "rule evaluation error",
			resources: []providers.Resource{
				{
					Id:          "rule-2",
					ServiceName: ServiceNameConfig,
					Name:        "test-rule-2",
					Meta: map[string]any{
						"EvaluationStatus": map[string]any{
							"LastErrorCode":    "INSUFFICIENT_PERMISSIONS",
							"LastErrorMessage": "Insufficient permissions",
						},
					},
					Type: getAwsServiceResourceType(ServiceNameConfig, "rule"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "conformance pack non compliant",
			resources: []providers.Resource{
				{
					Id:          "pack-1",
					ServiceName: ServiceNameConfig,
					Name:        "test-pack",
					Meta: map[string]any{
						"NonCompliantRulesCount": 5,
					},
					Type: getAwsServiceResourceType(ServiceNameConfig, "conformancepack"),
				},
			},
			expectedCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := providers.NewCloudProviderContext(context.Background())

			recommendations, err := config.GetRecommendations(ctx, providers.Account{}, providers.ListRecommendationsRequest{}, tt.resources)
			assert.NoError(t, err)
			assert.GreaterOrEqual(t, len(recommendations), tt.expectedCount)
		})
	}
}

func TestConfigApplyRecommendation(t *testing.T) {
	config := &awsConfig{}
	ctx := providers.NewCloudProviderContext(context.Background())

	err := config.ApplyRecommendation(ctx, providers.Account{}, providers.Recommendation{})
	assert.Error(t, err)
}

func TestConfigQueryMetrics(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")

	config := &awsConfig{}
	ctx := providers.NewCloudProviderContext(context.Background())

	response, err := config.QueryMetrices(ctx, providers.Account{}, providers.QueryMetricsRequest{
		ServiceName: ServiceNameConfig,
	})
	assert.NoError(t, err)
	assert.NotNil(t, response)
}
