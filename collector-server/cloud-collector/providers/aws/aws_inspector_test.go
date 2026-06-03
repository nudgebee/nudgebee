package aws

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/inspector2/types"
	"github.com/stretchr/testify/assert"
)

func TestInspectorFindingStatusToNbStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   types.FindingStatus
		expected providers.ResourceStatus
	}{
		{"active", types.FindingStatusActive, providers.ResourceStatusActive},
		{"closed", types.FindingStatusClosed, providers.ResourceStatusInactive},
		{"suppressed", types.FindingStatusSuppressed, providers.ResourceStatusInactive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := inspectorFindingStatusToNbStatus(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestInspectorGetResources(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")

	inspector := &awsInspector{}
	account := providers.Account{
		AccountNumber: "123456789012",
		Region:        stringPtr("us-east-1"),
	}

	ctx := providers.NewCloudProviderContext(context.Background())

	resources, err := inspector.GetResources(ctx, account, "us-east-1")
	assert.NoError(t, err)
	assert.NotNil(t, resources)
}

func TestInspectorGetRecommendations(t *testing.T) {
	inspector := &awsInspector{}

	tests := []struct {
		name          string
		resources     []providers.Resource
		expectedCount int
	}{
		{
			name: "inspector not enabled",
			resources: []providers.Resource{
				{
					Id:          "inspector-account-123",
					ServiceName: ServiceNameInspector,
					Meta: map[string]any{
						"InspectorEnabled": false,
					},
					Type: getAwsServiceResourceType(ServiceNameInspector, "account"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "ec2 scanning disabled",
			resources: []providers.Resource{
				{
					Id:          "inspector-account-123",
					ServiceName: ServiceNameInspector,
					Meta: map[string]any{
						"InspectorEnabled": true,
						"Ec2Status":        "DISABLED",
					},
					Type: getAwsServiceResourceType(ServiceNameInspector, "account"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "critical finding",
			resources: []providers.Resource{
				{
					Id:          "finding-1",
					ServiceName: ServiceNameInspector,
					Meta: map[string]any{
						"Severity":    "CRITICAL",
						"Description": "Critical vulnerability found",
					},
					Type:      getAwsServiceResourceType(ServiceNameInspector, "finding"),
					CreatedAt: time.Now(),
				},
			},
			expectedCount: 1,
		},
		{
			name: "old finding",
			resources: []providers.Resource{
				{
					Id:          "finding-2",
					ServiceName: ServiceNameInspector,
					Meta: map[string]any{
						"Severity": "MEDIUM",
					},
					Type:      getAwsServiceResourceType(ServiceNameInspector, "finding"),
					CreatedAt: time.Now().AddDate(0, 0, -35),
				},
			},
			expectedCount: 1,
		},
		{
			name: "no coverage",
			resources: []providers.Resource{
				{
					Id:          "inspector-coverage-123",
					ServiceName: ServiceNameInspector,
					Meta: map[string]any{
						"TotalResourcesCovered": 0,
					},
					Type: getAwsServiceResourceType(ServiceNameInspector, "coverage"),
				},
			},
			expectedCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := providers.NewCloudProviderContext(context.Background())

			recommendations, err := inspector.GetRecommendations(ctx, providers.Account{}, providers.ListRecommendationsRequest{}, tt.resources)
			assert.NoError(t, err)
			assert.GreaterOrEqual(t, len(recommendations), tt.expectedCount)
		})
	}
}

func TestInspectorApplyRecommendation(t *testing.T) {
	inspector := &awsInspector{}
	ctx := providers.NewCloudProviderContext(context.Background())

	err := inspector.ApplyRecommendation(ctx, providers.Account{}, providers.Recommendation{})
	assert.Error(t, err)
}

func TestInspectorQueryMetrics(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")

	inspector := &awsInspector{}
	ctx := providers.NewCloudProviderContext(context.Background())

	response, err := inspector.QueryMetrices(ctx, providers.Account{}, providers.QueryMetricsRequest{
		ServiceName: ServiceNameInspector,
	})
	assert.NoError(t, err)
	assert.NotNil(t, response)
}
