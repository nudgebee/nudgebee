package adapter

import (
	"context"
	"log/slog"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"
)

type ApplyRecommendationRequest struct {
	Data              map[string]any
	Recommendation    models.Recommendation
	Resource          models.Resource
	ProviderConfig    map[string]any
	ResolverType      string  `default:"recommendation"`
	ReferenceLink     *string `default:"nil"` // This is the link to the resolver for PR
	IsEventResolution bool    `default:"false"`
}

type RecommendationResolutionStatus string

const (
	RecommendationResolutionStatusInProgress RecommendationResolutionStatus = "InProgress"
	RecommendationResolutionStatusFailed     RecommendationResolutionStatus = "Failed"
	RecommendationResolutionStatusSuccess    RecommendationResolutionStatus = "Success"
)

type RecommendationResolutionType string

const (
	RecommendationResolutionTypePullRequest      RecommendationResolutionType = "PullRequest"
	RecommendationResolutionTypeTicket           RecommendationResolutionType = "Ticket"
	RecommendationResolutionTypeDeploymentChange RecommendationResolutionType = "DeploymentChange"
	RecommendationEventResolutionType            RecommendationResolutionType = "EventResolution"
	RecommendationResolutionTypeCloudResource    RecommendationResolutionType = "CloudResource"
)

type ApplyRecommendationResponse struct {
	Data                     map[string]interface{}
	Status                   RecommendationResolutionStatus
	ResolutionType           RecommendationResolutionType
	ResolutionTypeRefrenceId string
	StatusMessage            string
}

type GetRecommendationResolutionStatusResponse struct {
	Status        RecommendationResolutionStatus
	StatusMessage string
}

type AccountAdapterContext interface {
	GetContext() context.Context
	GetLogger() *slog.Logger
	GetSecurityContext() *security.SecurityContext
}

type AccountAdapter interface {
	ApplyRecommendation(ctx AccountAdapterContext, recommendation ApplyRecommendationRequest, existingRecommendations []models.RecommendationResolution, recommendResolutionId string) (ApplyRecommendationResponse, error)
	GetRecommendationResolutionStatus(ctx AccountAdapterContext, Recommendation models.Recommendation, resolutionReferenceId string, applyRequestPayload models.Json, resolutionStatusMessage string) (GetRecommendationResolutionStatusResponse, error)
}
