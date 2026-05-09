package gcloud

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
)

const (
	ServiceNameVMManager = "VM Manager"
)

type vmManagerService struct{}

func (s *vmManagerService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	// VM Manager (OS Config) is a service that manages OS patches and configurations
	// It doesn't have traditional "resources" like VMs or buckets
	// The service is tracked through billing, but resources are managed through policies
	ctx.GetLogger().Debug("vm manager has no listable resources - billing only", "region", region)
	return nil, errors.ErrUnsupported
}

func (s *vmManagerService) GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// VM Manager metrics come from Cloud Monitoring
	// Common metrics: agent/uptime, patch_job/completed_count, patch_job/duration
	return getGcloudMonitoringMetrics(ctx, account, filter)
}

func (s *vmManagerService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	// VM Manager doesn't have traditional resources, so no recommendations
	return []providers.Recommendation{}, nil
}

func (s *vmManagerService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// VM Manager doesn't support automatic recommendation application
	return fmt.Errorf("vm manager service does not support automatic recommendation application")
}

func (s *vmManagerService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// VM Manager doesn't support commands via this interface
	// Patch deployments and OS policies are managed via GCP Console or gcloud CLI
	return providers.ApplyCommandResponse{
		Success: false,
		Message: "VM Manager commands must be executed via GCP Console or gcloud CLI",
	}, fmt.Errorf("vm manager service does not support commands via this interface")
}

func (s *vmManagerService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, _ string) string {
	return ""
}
