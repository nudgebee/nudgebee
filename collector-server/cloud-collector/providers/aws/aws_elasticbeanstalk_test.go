package aws

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestElasticBeanstalkStatusToNbStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		expected providers.ResourceStatus
	}{
		{"launching", "launching", providers.ResourceStatusActive},
		{"updating", "updating", providers.ResourceStatusActive},
		{"ready", "ready", providers.ResourceStatusActive},
		{"terminating", "terminating", providers.ResourceStatusDeleted},
		{"terminated", "terminated", providers.ResourceStatusDeleted},
		{"terminationfailed", "terminationfailed", providers.ResourceStatusInactive},
		{"invalidstate", "invalidstate", providers.ResourceStatusInactive},
		{"unknown", "unknown", providers.ResourceStatusUnknown},
		{"uppercase_ready", "READY", providers.ResourceStatusActive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := elasticbeanstalkStatusToNbStatus(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestElasticBeanstalkGetResources(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")

	eb := &awsElasticBeanstalk{}
	account := providers.Account{
		AccountNumber: "123456789012",
		Region:        stringPtr("us-east-1"),
	}

	ctx := providers.NewCloudProviderContext(context.Background())

	resources, err := eb.GetResources(ctx, account, "us-east-1")
	assert.NoError(t, err)
	assert.NotNil(t, resources)
}

func TestElasticBeanstalkGetRecommendations(t *testing.T) {
	eb := &awsElasticBeanstalk{}

	tests := []struct {
		name          string
		resources     []providers.Resource
		expectedCount int
	}{
		{
			name: "outdated platform",
			resources: []providers.Resource{
				{
					Id:          "env-1",
					ServiceName: ServiceNameElasticBeanstalk,
					Meta: map[string]any{
						"PlatformArn": "arn:aws:elasticbeanstalk:us-east-1::platform/deprecated-platform",
					},
					Type: getAwsServiceResourceType(ServiceNameElasticBeanstalk, "environment"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "single instance environment",
			resources: []providers.Resource{
				{
					Id:          "env-2",
					ServiceName: ServiceNameElasticBeanstalk,
					Meta: map[string]any{
						"Health": map[string]any{
							"InstancesHealth": map[string]any{
								"Ok": int32(1),
							},
						},
					},
					Type: getAwsServiceResourceType(ServiceNameElasticBeanstalk, "environment"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "unhealthy environment",
			resources: []providers.Resource{
				{
					Id:          "env-3",
					ServiceName: ServiceNameElasticBeanstalk,
					Meta: map[string]any{
						"HealthStatus": "severe",
					},
					Type: getAwsServiceResourceType(ServiceNameElasticBeanstalk, "environment"),
				},
			},
			expectedCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := providers.NewCloudProviderContext(context.Background())

			recommendations, err := eb.GetRecommendations(ctx, providers.Account{}, providers.ListRecommendationsRequest{}, tt.resources)
			assert.NoError(t, err)
			assert.GreaterOrEqual(t, len(recommendations), tt.expectedCount)
		})
	}
}

func TestElasticBeanstalkApplyRecommendation(t *testing.T) {
	eb := &awsElasticBeanstalk{}
	ctx := providers.NewCloudProviderContext(context.Background())

	err := eb.ApplyRecommendation(ctx, providers.Account{}, providers.Recommendation{})
	assert.Error(t, err)
}

func TestElasticBeanstalkQueryMetrics(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")

	eb := &awsElasticBeanstalk{}
	ctx := providers.NewCloudProviderContext(context.Background())

	response, err := eb.QueryMetrices(ctx, providers.Account{}, providers.QueryMetricsRequest{
		ServiceName: ServiceNameElasticBeanstalk,
	})
	assert.NoError(t, err)
	assert.NotNil(t, response)
}
