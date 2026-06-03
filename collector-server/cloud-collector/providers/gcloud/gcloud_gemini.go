package gcloud

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
)

const (
	ServiceNameGemini = "Gemini API"
)

type geminiService struct{}

func (s *geminiService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	// Gemini API is a fully managed API service without traditional cloud resources
	// It operates through API calls and doesn't have resources like VMs, buckets, etc.
	// Usage is tracked through billing only
	ctx.GetLogger().Debug("gemini api has no listable resources - billing only", "region", region)
	return nil, errors.ErrUnsupported
}

func (s *geminiService) GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Gemini API metrics come from Cloud Monitoring
	// Common metrics: aiplatform.googleapis.com/prediction/online/prediction_count,
	// aiplatform.googleapis.com/prediction/online/response_count,
	// aiplatform.googleapis.com/prediction/online/error_count
	return getGcloudMonitoringMetrics(ctx, account, filter)
}

func (s *geminiService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	// Gemini API is a consumption-based service without persistent resources
	// Cost optimization recommendations would be usage-based (e.g., model selection, prompt efficiency)
	// These are typically handled at the application level, not infrastructure level
	return []providers.Recommendation{}, nil
}

func (s *geminiService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Gemini API doesn't have infrastructure-level recommendations to apply
	// Optimization is done through API usage patterns, not resource configuration
	return fmt.Errorf("gemini api service does not support automatic recommendation application - optimize via API usage patterns")
}

func (s *geminiService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// Gemini API is accessed programmatically via API calls
	// There are no administrative commands to execute via this interface
	return providers.ApplyCommandResponse{
		Success: false,
		Message: "Gemini API is a fully managed service - use Vertex AI SDK or REST API for interactions",
	}, fmt.Errorf("gemini api service does not support administrative commands via this interface")
}

func (s *geminiService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, _ string) string {
	return ""
}
