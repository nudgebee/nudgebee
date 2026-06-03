package aws

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRoute53HealthCheckStatusToNbStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		expected providers.ResourceStatus
	}{
		{"healthy", "healthy", providers.ResourceStatusActive},
		{"unhealthy", "unhealthy", providers.ResourceStatusInactive},
		{"unknown", "unknown", providers.ResourceStatusUnknown},
		{"uppercase_healthy", "HEALTHY", providers.ResourceStatusActive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := route53HealthCheckStatusToNbStatus(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRoute53GetResources(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")

	r53 := &awsRoute53{}
	account := providers.Account{
		AccountNumber: "123456789012",
		Region:        stringPtr("us-east-1"),
	}

	ctx := providers.NewCloudProviderContext(context.Background())

	resources, err := r53.GetResources(ctx, account, "us-east-1")
	assert.NoError(t, err)
	assert.NotNil(t, resources)
}

func TestRoute53GetRecommendations(t *testing.T) {
	r53 := &awsRoute53{}

	tests := []struct {
		name          string
		resources     []providers.Resource
		expectedCount int
	}{
		{
			name: "query logging disabled",
			resources: []providers.Resource{
				{
					Id:          "zone-1",
					ServiceName: ServiceNameRoute53,
					Meta:        map[string]any{},
					Type:        getAwsServiceResourceType(ServiceNameRoute53, "hostedzone"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "dnssec disabled",
			resources: []providers.Resource{
				{
					Id:          "zone-2",
					ServiceName: ServiceNameRoute53,
					Meta: map[string]any{
						"DNSSEC": map[string]any{
							"Status": map[string]any{
								"ServeSignature": "NOT_SIGNING",
							},
						},
					},
					Type: getAwsServiceResourceType(ServiceNameRoute53, "hostedzone"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "empty hosted zone",
			resources: []providers.Resource{
				{
					Id:          "zone-3",
					ServiceName: ServiceNameRoute53,
					Meta: map[string]any{
						"RecordSetCount": 2,
					},
					Type: getAwsServiceResourceType(ServiceNameRoute53, "hostedzone"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "unhealthy health check",
			resources: []providers.Resource{
				{
					Id:          "hc-1",
					ServiceName: ServiceNameRoute53,
					Meta: map[string]any{
						"HealthyCheckers": 1,
						"TotalCheckers":   4,
					},
					Type: getAwsServiceResourceType(ServiceNameRoute53, "healthcheck"),
				},
			},
			expectedCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := providers.NewCloudProviderContext(context.Background())

			recommendations, err := r53.GetRecommendations(ctx, providers.Account{}, providers.ListRecommendationsRequest{}, tt.resources)
			assert.NoError(t, err)
			assert.GreaterOrEqual(t, len(recommendations), tt.expectedCount)
		})
	}
}

func TestRoute53ApplyRecommendation(t *testing.T) {
	r53 := &awsRoute53{}
	ctx := providers.NewCloudProviderContext(context.Background())

	err := r53.ApplyRecommendation(ctx, providers.Account{}, providers.Recommendation{})
	assert.Error(t, err)
}

func TestRoute53QueryMetrics(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")

	r53 := &awsRoute53{}
	ctx := providers.NewCloudProviderContext(context.Background())

	response, err := r53.QueryMetrices(ctx, providers.Account{}, providers.QueryMetricsRequest{
		ServiceName: ServiceNameRoute53,
	})
	assert.NoError(t, err)
	assert.NotNil(t, response)
}
