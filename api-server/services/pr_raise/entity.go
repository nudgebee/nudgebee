package pr_raise

import "nudgebee/services/internal/database/models"

type PRraiseRequest struct {
	SourceID       string         `json:"source_id" mapstructure:"source_id" validate:"required"`
	AccountId      string         `json:"account_id" mapstructure:"account_id" validate:"required"`
	TenantId       string         `json:"tenant_id" mapstructure:"tenant_id" validate:"required"`
	EventId        *string        `json:"event_id,omitempty" mapstructure:"event_id,omitempty"`
	ChangeType     string         `json:"change_type" mapstructure:"change_type" validate:"required"`
	Data           any            `json:"data" mapstructure:"data"`
	Provider       string         `json:"provider" mapstructure:"provider"`
	ResolverType   string         `json:"resolver_type" mapstructure:"resolver_type" validate:"required"`
	ResolverID     string         `json:"resolver_id" mapstructure:"resolver_id" validate:"required"`
	ProviderConfig map[string]any `json:"provider_config" mapstructure:"provider_config" validate:"required"`
	ResourceId     *string        `json:"resource_id" mapstructure:"resource_id" validate:"required"`
	ReferenceLink  *string        `json:"reference_link,omitempty" mapstructure:"reference_link,omitempty"`
}

type EventRecommendationApplyResponse struct {
	Data       []any                       `json:"data" mapstructure:"data"`
	Resolution models.EventResolution      `json:"resolution" mapstructure:"resolution"`
	Status     models.RecommendationStatus `json:"status" mapstructure:"status"`
}
