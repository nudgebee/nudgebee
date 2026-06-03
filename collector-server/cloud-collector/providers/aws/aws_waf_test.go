package aws

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWAFGetResources(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")

	waf := &awsWAF{}
	account := providers.Account{
		AccountNumber: "123456789012",
		Region:        stringPtr("us-east-1"),
	}

	ctx := providers.NewCloudProviderContext(context.Background())

	resources, err := waf.GetResources(ctx, account, "us-east-1")
	assert.NoError(t, err)
	assert.NotNil(t, resources)
}

func TestWAFGetRecommendations(t *testing.T) {
	waf := &awsWAF{}

	tests := []struct {
		name          string
		resources     []providers.Resource
		expectedCount int
	}{
		{
			name: "logging disabled",
			resources: []providers.Resource{
				{
					Id:          "webacl-1",
					ServiceName: ServiceNameWAF,
					Meta:        map[string]any{},
					Type:        getAwsServiceResourceType(ServiceNameWAF, "webacl"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "no associated resources",
			resources: []providers.Resource{
				{
					Id:          "webacl-2",
					ServiceName: ServiceNameWAF,
					Meta: map[string]any{
						"AssociatedResourceCount": 0,
					},
					Type: getAwsServiceResourceType(ServiceNameWAF, "webacl"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "no rules",
			resources: []providers.Resource{
				{
					Id:          "webacl-3",
					ServiceName: ServiceNameWAF,
					Meta: map[string]any{
						"Rules": []any{},
					},
					Type: getAwsServiceResourceType(ServiceNameWAF, "webacl"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "no rate limiting",
			resources: []providers.Resource{
				{
					Id:          "webacl-4",
					ServiceName: ServiceNameWAF,
					Meta: map[string]any{
						"Rules": []any{
							map[string]any{
								"Statement": map[string]any{},
							},
						},
					},
					Type: getAwsServiceResourceType(ServiceNameWAF, "webacl"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "no managed rules",
			resources: []providers.Resource{
				{
					Id:          "webacl-5",
					ServiceName: ServiceNameWAF,
					Meta: map[string]any{
						"ManagedRuleCount": 0,
						"CustomRuleCount":  3,
					},
					Type: getAwsServiceResourceType(ServiceNameWAF, "webacl"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "empty ip set",
			resources: []providers.Resource{
				{
					Id:          "ipset-1",
					ServiceName: ServiceNameWAF,
					Meta: map[string]any{
						"Addresses": []any{},
					},
					Type: getAwsServiceResourceType(ServiceNameWAF, "ipset"),
				},
			},
			expectedCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := providers.NewCloudProviderContext(context.Background())

			recommendations, err := waf.GetRecommendations(ctx, providers.Account{}, providers.ListRecommendationsRequest{}, tt.resources)
			assert.NoError(t, err)
			assert.GreaterOrEqual(t, len(recommendations), tt.expectedCount)
		})
	}
}

func TestWAFApplyRecommendation(t *testing.T) {
	waf := &awsWAF{}
	ctx := providers.NewCloudProviderContext(context.Background())

	err := waf.ApplyRecommendation(ctx, providers.Account{}, providers.Recommendation{})
	assert.Error(t, err)
}

func TestWAFQueryMetrics(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")

	waf := &awsWAF{}
	ctx := providers.NewCloudProviderContext(context.Background())

	response, err := waf.QueryMetrices(ctx, providers.Account{}, providers.QueryMetricsRequest{
		ServiceName: ServiceNameWAF,
	})
	assert.NoError(t, err)
	assert.NotNil(t, response)
}
