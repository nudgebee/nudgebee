package recommendation

import "nudgebee/services/internal/database/models"

type TroubleshootingRecommendationApplyRequest struct {
	AccountId      string         `json:"account_id" mapstructure:"account_id" validate:"required"`
	EventId        string         `json:"event_id" mapstructure:"event_id" validate:"required"`
	Data           any            `json:"data" mapstructure:"data"`
	Provider       string         `json:"provider" mapstructure:"provider"`
	ProviderConfig map[string]any `json:"provider_config" mapstructure:"provider_config"`
}
type TroubleshootingRecommendationApplyResponse struct {
	Data       []any                           `json:"data" mapstructure:"data"`
	Resolution models.RecommendationResolution `json:"resolution" mapstructure:"resolution"`
	Status     models.RecommendationStatus     `json:"status" mapstructure:"status"`
}

type RecommendationApplyRequest struct {
	AccountId        string                                      `json:"account_id" mapstructure:"account_id" validate:"required"`
	RecommendationId string                                      `json:"recommendation_id" mapstructure:"recommendation_id" validate:"required"`
	Data             any                                         `json:"data" mapstructure:"data"`
	Provider         string                                      `json:"provider" mapstructure:"provider"`
	ProviderConfig   map[string]any                              `json:"provider_config" mapstructure:"provider_config"`
	ResolverType     models.RecommendationResolutionResolverType `json:"resolver_type" mapstructure:"resolver_type"`
	ResolverId       string                                      `json:"resolver_id" mapstructure:"resolver_id"`
}

type RecommendationApplyResponse struct {
	Data       []any                           `json:"data" mapstructure:"data"`
	Resolution models.RecommendationResolution `json:"resolution" mapstructure:"resolution"`
	Status     models.RecommendationStatus     `json:"status" mapstructure:"status"`
}

type GenerateRecommendationRequest struct {
	AccountId []string `json:"account_id" mapstructure:"account_id" validate:"required"`
}

type GenerateRecommendationResponse struct {
}

type RecommendationScanImageRequest struct {
	AccountId string `json:"account_id" mapstructure:"account_id" validate:"required"`
	Namespace string `json:"namespace" mapstructure:"namespace" validate:"required"`
	Workload  string `json:"workload" mapstructure:"workload" validate:"required"`
}

type RecommendationJobCreateRequest struct {
	AccountId string `json:"account_id" mapstructure:"account_id" validate:"required"`
	JobName   string `json:"job_name" mapstructure:"job_name" validate:"required"`
}

type RecommendationJobCreateResponse struct {
	Data []any `json:"data" mapstructure:"data"`
}

type RetryRecommendationResolutionRequest struct {
	ResolutionId string `json:"resolution_id" mapstructure:"resolution_id" validate:"required"`
	AccountId    string `json:"account_id" mapstructure:"account_id" validate:"required"`
}

type RetryRecommendationResolutionResponse struct {
	Resolution models.RecommendationResolution `json:"resolution" mapstructure:"resolution"`
	Status     string                          `json:"status" mapstructure:"status"`
	Message    string                          `json:"message" mapstructure:"message"`
}
