package aws

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/stretchr/testify/assert"
)

func TestSSMInstanceStatusToNbStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   types.PingStatus
		expected providers.ResourceStatus
	}{
		{"online", types.PingStatusOnline, providers.ResourceStatusActive},
		{"connection_lost", types.PingStatusConnectionLost, providers.ResourceStatusActive},
		{"inactive", types.PingStatusInactive, providers.ResourceStatusInactive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ssmInstanceStatusToNbStatus(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSSMComplianceStatusToNbStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   types.ComplianceStatus
		expected providers.ResourceStatus
	}{
		{"compliant", types.ComplianceStatusCompliant, providers.ResourceStatusActive},
		{"non_compliant", types.ComplianceStatusNonCompliant, providers.ResourceStatusInactive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ssmComplianceStatusToNbStatus(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSSMGetResources(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")

	ssm := &awsSystemsManager{}
	account := providers.Account{
		AccountNumber: "123456789012",
		Region:        stringPtr("us-east-1"),
	}

	ctx := providers.NewCloudProviderContext(context.Background())

	resources, err := ssm.GetResources(ctx, account, "us-east-1")
	assert.NoError(t, err)
	assert.NotNil(t, resources)
}

func TestSSMGetRecommendations(t *testing.T) {
	ssm := &awsSystemsManager{}

	tests := []struct {
		name          string
		resources     []providers.Resource
		expectedCount int
	}{
		{
			name: "instance offline",
			resources: []providers.Resource{
				{
					Id:          "i-123",
					ServiceName: ServiceNameSystemsManager,
					Meta: map[string]any{
						"PingStatus": "CONNECTIONLOST",
					},
					Type: getAwsServiceResourceType(ServiceNameSystemsManager, "managedinstance"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "missing patches",
			resources: []providers.Resource{
				{
					Id:          "i-456",
					ServiceName: ServiceNameSystemsManager,
					Meta: map[string]any{
						"PatchCompliance": map[string]int{
							"Installed": 10,
							"Missing":   5,
							"Failed":    0,
						},
					},
					Type: getAwsServiceResourceType(ServiceNameSystemsManager, "managedinstance"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "no associations",
			resources: []providers.Resource{
				{
					Id:          "i-789",
					ServiceName: ServiceNameSystemsManager,
					Meta: map[string]any{
						"AssociationCount": 0,
					},
					Type: getAwsServiceResourceType(ServiceNameSystemsManager, "managedinstance"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "outdated agent",
			resources: []providers.Resource{
				{
					Id:          "i-101",
					ServiceName: ServiceNameSystemsManager,
					Meta: map[string]any{
						"AgentVersion": "2.3.123",
					},
					Type: getAwsServiceResourceType(ServiceNameSystemsManager, "managedinstance"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "parameter not encrypted",
			resources: []providers.Resource{
				{
					Id:          "param-1",
					ServiceName: ServiceNameSystemsManager,
					Meta: map[string]any{
						"Type": "String",
					},
					Type: getAwsServiceResourceType(ServiceNameSystemsManager, "parameter"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "maintenance window disabled",
			resources: []providers.Resource{
				{
					Id:          "mw-1",
					ServiceName: ServiceNameSystemsManager,
					Meta: map[string]any{
						"Enabled": false,
					},
					Type: getAwsServiceResourceType(ServiceNameSystemsManager, "maintenancewindow"),
				},
			},
			expectedCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := providers.NewCloudProviderContext(context.Background())

			recommendations, err := ssm.GetRecommendations(ctx, providers.Account{}, providers.ListRecommendationsRequest{}, tt.resources)
			assert.NoError(t, err)
			assert.GreaterOrEqual(t, len(recommendations), tt.expectedCount)
		})
	}
}

func TestSSMApplyRecommendation(t *testing.T) {
	ssm := &awsSystemsManager{}
	ctx := providers.NewCloudProviderContext(context.Background())

	err := ssm.ApplyRecommendation(ctx, providers.Account{}, providers.Recommendation{})
	assert.Error(t, err)
}

func TestSSMQueryMetrics(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")

	ssm := &awsSystemsManager{}
	ctx := providers.NewCloudProviderContext(context.Background())

	response, err := ssm.QueryMetrices(ctx, providers.Account{}, providers.QueryMetricsRequest{
		ServiceName: ServiceNameSystemsManager,
	})
	assert.NoError(t, err)
	assert.NotNil(t, response)
}
