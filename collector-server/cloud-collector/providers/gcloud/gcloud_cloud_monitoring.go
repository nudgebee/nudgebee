package gcloud

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
)

const (
	ServiceNameCloudMonitoring = "Cloud Monitoring"
)

type cloudMonitoringService struct{}

func (s *cloudMonitoringService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	// Cloud Monitoring is a global service - resources are not region-specific
	// The service itself doesn't have traditional "resources" like VMs or buckets
	// Monitoring data is accessed via metrics, alerts, and dashboards
	// These are tracked through billing but not as listable infrastructure resources
	ctx.GetLogger().Debug("cloud monitoring has no listable resources - billing only", "region", region)
	return nil, errors.ErrUnsupported
}

func (s *cloudMonitoringService) GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Cloud Monitoring is the metrics service itself
	// It provides metrics for other GCP services via getGcloudMonitoringMetrics
	// Querying "metrics about metrics" is not typically useful
	// Usage is tracked through billing (ingestion volume, API calls)
	return getGcloudMonitoringMetrics(ctx, account, filter)
}

func (s *cloudMonitoringService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	// Cloud Monitoring doesn't have infrastructure resources to optimize
	// Cost optimization is done by reducing metric ingestion or sampling rates
	// This is configured at the application/service level, not via recommendations
	return []providers.Recommendation{}, nil
}

func (s *cloudMonitoringService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Cloud Monitoring doesn't support infrastructure-level recommendations
	return fmt.Errorf("cloud monitoring service does not support automatic recommendation application")
}

func (s *cloudMonitoringService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// Cloud Monitoring configuration (alerts, dashboards, uptime checks) is managed
	// via the Cloud Monitoring API or GCP Console, not via this interface
	return providers.ApplyCommandResponse{
		Success: false,
		Message: "Cloud Monitoring configuration must be managed via GCP Console or Cloud Monitoring API",
	}, fmt.Errorf("cloud monitoring service does not support commands via this interface")
}

func (s *cloudMonitoringService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, _ string) string {
	return ""
}
