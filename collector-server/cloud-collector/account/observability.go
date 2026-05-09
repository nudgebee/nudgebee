package account

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
)

func QueryServiceMap(ctx *security.RequestContext, accountId string, query providers.QueryServiceMapRequest) (providers.QueryServiceMapResponse, error) {
	account, provider, err := getAccount(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch account", "error", err)
		return providers.QueryServiceMapResponse{}, err
	}
	cloudProvider, ok := providers.GetProvider(provider)
	if !ok {
		return providers.QueryServiceMapResponse{}, fmt.Errorf("provider not found")
	}
	return cloudProvider.QueryServiceMap(ctx, account, query)
}

func QueryMetrics(ctx *security.RequestContext, accountId string, query providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	account, provider, err := getAccount(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch account", "error", err)
		return providers.QueryMetricsResponse{}, err
	}
	cloudProvider, ok := providers.GetProvider(provider)
	if !ok {
		return providers.QueryMetricsResponse{}, fmt.Errorf("provider not found")
	}
	return cloudProvider.QueryMetrices(ctx, account, query)
}

func ListMetrics(ctx *security.RequestContext, accountId string, request providers.ListMetricsRequest) (providers.ListMetricsResponse, error) {
	account, provider, err := getAccount(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch account", "error", err)
		return providers.ListMetricsResponse{}, err
	}
	cloudProvider, ok := providers.GetProvider(provider)
	if !ok {
		return providers.ListMetricsResponse{}, fmt.Errorf("provider not found")
	}
	return cloudProvider.ListMetrics(ctx, account, request)
}

func QueryLogs(ctx *security.RequestContext, accountId string, query providers.QueryLogsRequest) (providers.QueryLogsResponse, error) {
	account, provider, err := getAccount(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch account", "error", err)
		return providers.QueryLogsResponse{}, err
	}
	cloudProvider, ok := providers.GetProvider(provider)
	if !ok {
		return providers.QueryLogsResponse{}, fmt.Errorf("provider not found")
	}
	return cloudProvider.QueryLogs(ctx, account, query)
}

func ListResources(ctx *security.RequestContext, accountId string, request providers.ListResourceRequest) (providers.ListResourcesResponse, error) {
	resources, _, err := getResourcesInternal(ctx, accountId, request)
	return resources, err
}

func ListRecommendations(ctx *security.RequestContext, accountId string, filter providers.ListRecommendationsRequest) (providers.ListRecommendationsResponse, error) {
	recommendations, _, err := getRecommendationsInternal(ctx, accountId, filter)
	return recommendations, err
}

func ListEvents(ctx *security.RequestContext, accountId string, filter providers.ListEventRequest) (providers.ListEventResponse, error) {
	events, _, err := getEventsInternal(ctx, accountId, filter)
	return events, err
}

func ListEventRules(ctx *security.RequestContext, accountId string) (providers.ListEventRules, error) {
	account, provider, err := getAccount(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch account", "error", err)
		return providers.ListEventRules{}, err
	}
	cloudProvider, ok := providers.GetProvider(provider)
	if !ok {
		return providers.ListEventRules{}, fmt.Errorf("provider not found")
	}

	return cloudProvider.ListEventRules(ctx, account)
}
