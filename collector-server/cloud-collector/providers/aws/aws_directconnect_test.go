package aws

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/directconnect/types"
	"github.com/stretchr/testify/assert"
)

func TestDirectConnectConnectionStateToNbStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   types.ConnectionState
		expected providers.ResourceStatus
	}{
		{"available", types.ConnectionStateAvailable, providers.ResourceStatusActive},
		{"ordering", types.ConnectionStateOrdering, providers.ResourceStatusActive},
		{"pending", types.ConnectionStatePending, providers.ResourceStatusActive},
		{"requested", types.ConnectionStateRequested, providers.ResourceStatusActive},
		{"deleted", types.ConnectionStateDeleted, providers.ResourceStatusDeleted},
		{"deleting", types.ConnectionStateDeleting, providers.ResourceStatusDeleted},
		{"down", types.ConnectionStateDown, providers.ResourceStatusInactive},
		{"rejected", types.ConnectionStateRejected, providers.ResourceStatusInactive},
		{"unknown", types.ConnectionStateUnknown, providers.ResourceStatusInactive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := directConnectConnectionStateToNbStatus(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDirectConnectVirtualInterfaceStateToNbStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   types.VirtualInterfaceState
		expected providers.ResourceStatus
	}{
		{"available", types.VirtualInterfaceStateAvailable, providers.ResourceStatusActive},
		{"confirming", types.VirtualInterfaceStateConfirming, providers.ResourceStatusActive},
		{"pending", types.VirtualInterfaceStatePending, providers.ResourceStatusActive},
		{"verifying", types.VirtualInterfaceStateVerifying, providers.ResourceStatusActive},
		{"deleted", types.VirtualInterfaceStateDeleted, providers.ResourceStatusDeleted},
		{"deleting", types.VirtualInterfaceStateDeleting, providers.ResourceStatusDeleted},
		{"down", types.VirtualInterfaceStateDown, providers.ResourceStatusInactive},
		{"rejected", types.VirtualInterfaceStateRejected, providers.ResourceStatusInactive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := directConnectVirtualInterfaceStateToNbStatus(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDirectConnectGetResources(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")

	dx := &awsDirectConnect{}
	account := providers.Account{
		AccountNumber: "123456789012",
		Region:        stringPtr("us-east-1"),
	}

	ctx := providers.NewCloudProviderContext(context.Background())

	resources, err := dx.GetResources(ctx, account, "us-east-1")
	assert.NoError(t, err)
	assert.NotNil(t, resources)
}

func TestDirectConnectGetRecommendations(t *testing.T) {
	dx := &awsDirectConnect{}

	tests := []struct {
		name          string
		resources     []providers.Resource
		expectedCount int
	}{
		{
			name: "connection down",
			resources: []providers.Resource{
				{
					Id:          "dxcon-1",
					ServiceName: ServiceNameDirectConnect,
					Meta: map[string]any{
						"ConnectionState": "DOWN",
					},
					Type: getAwsServiceResourceType(ServiceNameDirectConnect, "connection"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "no redundancy",
			resources: []providers.Resource{
				{
					Id:          "dxcon-2",
					ServiceName: ServiceNameDirectConnect,
					Meta: map[string]any{
						"VirtualInterfaceCount": 2,
					},
					Type: getAwsServiceResourceType(ServiceNameDirectConnect, "connection"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "no virtual interfaces",
			resources: []providers.Resource{
				{
					Id:          "dxcon-3",
					ServiceName: ServiceNameDirectConnect,
					Meta: map[string]any{
						"VirtualInterfaceCount": 0,
					},
					Type: getAwsServiceResourceType(ServiceNameDirectConnect, "connection"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "lag below minimum",
			resources: []providers.Resource{
				{
					Id:          "dxlag-1",
					ServiceName: ServiceNameDirectConnect,
					Meta: map[string]any{
						"NumberOfConnections": int32(1),
						"MinimumLinks":        int32(2),
					},
					Type: getAwsServiceResourceType(ServiceNameDirectConnect, "lag"),
				},
			},
			expectedCount: 1,
		},
		{
			name: "vif down",
			resources: []providers.Resource{
				{
					Id:          "dxvif-1",
					ServiceName: ServiceNameDirectConnect,
					Meta: map[string]any{
						"VirtualInterfaceState": "DOWN",
					},
					Type: getAwsServiceResourceType(ServiceNameDirectConnect, "virtualinterface"),
				},
			},
			expectedCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := providers.NewCloudProviderContext(context.Background())

			recommendations, err := dx.GetRecommendations(ctx, providers.Account{}, providers.ListRecommendationsRequest{}, tt.resources)
			assert.NoError(t, err)
			assert.GreaterOrEqual(t, len(recommendations), tt.expectedCount)
		})
	}
}

func TestDirectConnectApplyRecommendation(t *testing.T) {
	dx := &awsDirectConnect{}
	ctx := providers.NewCloudProviderContext(context.Background())

	err := dx.ApplyRecommendation(ctx, providers.Account{}, providers.Recommendation{})
	assert.Error(t, err)
}

func TestDirectConnectQueryMetrics(t *testing.T) {
	t.Skip("Skipping integration test that requires AWS credentials")

	dx := &awsDirectConnect{}
	ctx := providers.NewCloudProviderContext(context.Background())

	response, err := dx.QueryMetrices(ctx, providers.Account{}, providers.QueryMetricsRequest{
		ServiceName: ServiceNameDirectConnect,
	})
	assert.NoError(t, err)
	assert.NotNil(t, response)
}
