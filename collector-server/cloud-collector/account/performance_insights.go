package account

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	awsProvider "nudgebee/collector/cloud/providers/aws"
	"nudgebee/collector/cloud/security"
	"strings"
	"time"
)

// QueryPerformanceInsights fetches Performance Insights metrics for an RDS instance
// DEPRECATED: Use QueryDatabasePerformance for multi-cloud support
func QueryPerformanceInsights(ctx *security.RequestContext, accountId string, dbInstanceIdentifier string, region string, startTimeStr string, endTimeStr string) (awsProvider.PerformanceInsightsResponse, error) {
	account, provider, err := getAccount(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch account", "error", err, "accountId", accountId)
		return awsProvider.PerformanceInsightsResponse{}, err
	}

	// Only AWS RDS supports Performance Insights
	if strings.ToLower(provider) != "aws" {
		return awsProvider.PerformanceInsightsResponse{}, fmt.Errorf("performance insights is only supported for AWS RDS")
	}

	// Parse time strings if provided
	var startTime, endTime *time.Time
	if startTimeStr != "" {
		parsed, err := time.Parse(time.RFC3339, startTimeStr)
		if err != nil {
			return awsProvider.PerformanceInsightsResponse{}, fmt.Errorf("invalid start_time format: %w", err)
		}
		startTime = &parsed
	}

	if endTimeStr != "" {
		parsed, err := time.Parse(time.RFC3339, endTimeStr)
		if err != nil {
			return awsProvider.PerformanceInsightsResponse{}, fmt.Errorf("invalid end_time format: %w", err)
		}
		endTime = &parsed
	}

	// Build the request
	request := awsProvider.PerformanceInsightsRequest{
		DBInstanceIdentifier: dbInstanceIdentifier,
		Region:               region,
		StartTime:            startTime,
		EndTime:              endTime,
	}

	// Create cloud provider context with proper context propagation for timeouts and tracing
	cloudCtx := providers.NewCloudProviderContextWithLogger(ctx.GetContext(), ctx.GetLogger())

	// Fetch Performance Insights metrics
	response, err := awsProvider.GetPerformanceInsightsMetrics(cloudCtx, account, request)
	if err != nil {
		ctx.GetLogger().Error("failed to fetch performance insights metrics", "error", err, "accountId", accountId, "dbInstance", dbInstanceIdentifier)
		return awsProvider.PerformanceInsightsResponse{}, err
	}

	return response, nil
}

// QueryDatabasePerformance fetches database performance insights for AWS RDS, GCP Cloud SQL, or Azure SQL Database
// This is the new generic interface that supports multi-cloud providers
func QueryDatabasePerformance(ctx *security.RequestContext, accountId string, request providers.DatabasePerformanceRequest) (providers.DatabasePerformanceResponse, error) {
	account, providerName, err := getAccount(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch account", "error", err, "accountId", accountId)
		return providers.DatabasePerformanceResponse{}, err
	}

	// Create cloud provider context with proper context propagation for timeouts and tracing
	cloudCtx := providers.NewCloudProviderContextWithLogger(ctx.GetContext(), ctx.GetLogger())

	// Get the appropriate provider instance
	provider, ok := providers.GetProvider(providerName)
	if !ok {
		return providers.DatabasePerformanceResponse{}, fmt.Errorf("unsupported cloud provider: %s", providerName)
	}

	// Call the provider's QueryDatabasePerformance method
	response, err := provider.QueryDatabasePerformance(cloudCtx, account, request)
	if err != nil {
		ctx.GetLogger().Error("failed to fetch database performance insights",
			"error", err,
			"accountId", accountId,
			"provider", providerName,
			"database", request.DatabaseIdentifier)
		return providers.DatabasePerformanceResponse{}, err
	}

	ctx.GetLogger().Info("successfully fetched database performance insights",
		"accountId", accountId,
		"provider", response.Provider,
		"database", response.DatabaseIdentifier,
		"performance_enabled", response.PerformanceEnabled,
		"load_metrics_count", len(response.LoadMetrics),
		"resource_metrics_count", len(response.ResourceMetrics),
		"top_queries_count", len(response.TopQueries),
		"wait_events_count", len(response.WaitEvents))

	return response, nil
}
