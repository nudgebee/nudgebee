package aws

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sfn/types"
	"github.com/stretchr/testify/assert"
)

func TestStepFunctionsStatusToNbStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   types.StateMachineStatus
		expected providers.ResourceStatus
	}{
		{"active", types.StateMachineStatusActive, providers.ResourceStatusActive},
		{"deleting", types.StateMachineStatusDeleting, providers.ResourceStatusDeleted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stepFunctionsStatusToNbStatus(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExecutionStatusToNbStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   types.ExecutionStatus
		expected providers.ResourceStatus
	}{
		{"running", types.ExecutionStatusRunning, providers.ResourceStatusActive},
		{"succeeded", types.ExecutionStatusSucceeded, providers.ResourceStatusActive},
		{"failed", types.ExecutionStatusFailed, providers.ResourceStatusInactive},
		{"timed_out", types.ExecutionStatusTimedOut, providers.ResourceStatusInactive},
		{"aborted", types.ExecutionStatusAborted, providers.ResourceStatusInactive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := executionStatusToNbStatus(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestStepFunctionsGetResources(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")

	sfn := &awsStepFunctions{}
	account := providers.Account{
		AccountNumber: "123456789012",
		Region:        stringPtr("us-east-1"),
	}

	ctx := providers.NewCloudProviderContext(context.Background())

	resources, err := sfn.GetResources(ctx, account, "us-east-1")
	assert.NoError(t, err)
	assert.NotNil(t, resources)
}

func TestStepFunctionsGetRecommendations(t *testing.T) {
	sfn := &awsStepFunctions{}

	tests := []struct {
		name          string
		resources     []providers.Resource
		expectedCount int
	}{
		{
			name: "high failure rate",
			resources: []providers.Resource{
				{
					Id:          "sm-1",
					ServiceName: ServiceNameStepFunctions,
					Meta: map[string]any{
						"ExecutionStats": map[string]int{
							"Failed":    6,
							"Succeeded": 2,
							"Running":   0,
							"Total":     8,
						},
					},
					Type: getAwsServiceResourceType(ServiceNameStepFunctions, "statemachine"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "logging disabled",
			resources: []providers.Resource{
				{
					Id:          "sm-2",
					ServiceName: ServiceNameStepFunctions,
					Meta: map[string]any{
						"LoggingConfiguration": map[string]any{
							"Level": "OFF",
						},
					},
					Type: getAwsServiceResourceType(ServiceNameStepFunctions, "statemachine"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "xray tracing disabled",
			resources: []providers.Resource{
				{
					Id:          "sm-3",
					ServiceName: ServiceNameStepFunctions,
					Meta: map[string]any{
						"TracingConfiguration": map[string]any{
							"Enabled": false,
						},
					},
					Type: getAwsServiceResourceType(ServiceNameStepFunctions, "statemachine"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "large definition",
			resources: []providers.Resource{
				{
					Id:          "sm-4",
					ServiceName: ServiceNameStepFunctions,
					Meta: map[string]any{
						"Definition": string(make([]byte, 150000)),
					},
					Type: getAwsServiceResourceType(ServiceNameStepFunctions, "statemachine"),
				},
			},
			expectedCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := providers.NewCloudProviderContext(context.Background())

			recommendations, err := sfn.GetRecommendations(ctx, providers.Account{}, providers.ListRecommendationsRequest{}, tt.resources)
			assert.NoError(t, err)
			assert.GreaterOrEqual(t, len(recommendations), tt.expectedCount)
		})
	}
}

func TestStepFunctionsApplyRecommendation(t *testing.T) {
	sfn := &awsStepFunctions{}
	ctx := providers.NewCloudProviderContext(context.Background())

	err := sfn.ApplyRecommendation(ctx, providers.Account{}, providers.Recommendation{})
	assert.Error(t, err)
}

func TestStepFunctionsQueryMetrics(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")

	sfn := &awsStepFunctions{}
	ctx := providers.NewCloudProviderContext(context.Background())

	response, err := sfn.QueryMetrices(ctx, providers.Account{}, providers.QueryMetricsRequest{
		ServiceName: ServiceNameStepFunctions,
	})
	assert.NoError(t, err)
	assert.NotNil(t, response)
}
