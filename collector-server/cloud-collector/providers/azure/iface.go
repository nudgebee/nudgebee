package azure

import (
	"nudgebee/collector/cloud/providers"
)

// ServiceScope defines the scope at which an Azure service operates
type ServiceScope string

const (
	// ServiceScopeGlobal indicates the service operates at global scope (e.g., Front Door, DNS, CDN)
	ServiceScopeGlobal ServiceScope = "global"
	// ServiceScopeRegional indicates the service operates at regional scope (e.g., VMs, Storage)
	ServiceScopeRegional ServiceScope = "regional"
	// ServiceScopeSubscription indicates the service operates at subscription scope
	ServiceScopeSubscription ServiceScope = "subscription"
)

type azureService interface {
	Name() string
	Scope() ServiceScope
	QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error)
	GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error)
	GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error)
	ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error
	ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error)
	GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error)
	GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error)
}
