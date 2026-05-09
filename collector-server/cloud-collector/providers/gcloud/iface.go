package gcloud

import (
	"nudgebee/collector/cloud/providers"
)

type gcloudService interface {
	GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error)
	GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error)
	GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error)
	ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error
	ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error)
	// GetLogFilter returns a Cloud Logging filter string to scope logs for a specific resource.
	// Returns empty string if the service does not support log querying.
	GetLogFilter(ctx providers.CloudProviderContext, account providers.Account, resourceId string) string
}
