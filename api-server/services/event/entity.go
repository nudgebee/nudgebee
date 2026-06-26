package event

import (
	eventtypes "nudgebee/services/event/types"
	"nudgebee/services/internal/database/models"
)

type EventRecommendationApplyRequest struct {
	AccountId      string         `json:"account_id" mapstructure:"account_id" validate:"required"`
	EventId        string         `json:"event_id" mapstructure:"event_id" validate:"required"`
	Data           any            `json:"data" mapstructure:"data"`
	Provider       string         `json:"provider" mapstructure:"provider"`
	ProviderConfig map[string]any `json:"provider_config" mapstructure:"provider_config"`
}

type EventRecommendationApplyResponse struct {
	Data       []any                       `json:"data" mapstructure:"data"`
	Resolution models.EventResolution      `json:"resolution" mapstructure:"resolution"`
	Status     models.RecommendationStatus `json:"status" mapstructure:"status"`
}

// Type aliases from event/types for backward compatibility
type EventPriority = eventtypes.EventPriority

const (
	EventPriorityDebug  = eventtypes.EventPriorityDebug
	EventPriorityInfo   = eventtypes.EventPriorityInfo
	EventPriorityLow    = eventtypes.EventPriorityLow
	EventPriorityMedium = eventtypes.EventPriorityMedium
	EventPriorityHigh   = eventtypes.EventPriorityHigh
)

type EventStatus = eventtypes.EventStatus

const (
	EventStatusFiring   = eventtypes.EventStatusFiring
	EventStatusResolved = eventtypes.EventStatusResolved
	EventStatusClosed   = eventtypes.EventStatusClosed
)

type Event = eventtypes.Event

type EventEvidenceInsight = eventtypes.EventEvidenceInsight
type EventEvidence = eventtypes.EventEvidence
