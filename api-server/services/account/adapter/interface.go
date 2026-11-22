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
	// Unverified means the provider could not be asked, or did not answer, so
	// Status is not an observation — the pull request is kept as it was (see
	// unverifiedPRStatus). Pollers stamp last_pr_check_at only on a verified
	// answer, which is what bounds how long a pull request may stay unverified.
	Unverified bool
}

type AccountAdapterContext interface {
	GetContext() context.Context
	GetLogger() *slog.Logger
	GetSecurityContext() *security.SecurityContext
}

// ApplyPrechecker is an adapter that can tell, before anything is recorded or
// queued, that an apply is certain to be refused.
type ApplyPrechecker interface {
	PrecheckApply(ctx AccountAdapterContext, request ApplyRecommendationRequest) error
}

// PrecheckApply returns the refusal adptr can see coming for request, if it
// looks for any. Callers run it before they record an attempt: a refusal stored
// as a failed attempt is offered for a retry that can never succeed, and counts
// as a failed apply. The adapter still makes the same checks when it applies, so
// a caller that skips this is refused all the same.
func PrecheckApply(ctx AccountAdapterContext, adptr AccountAdapter, request ApplyRecommendationRequest) error {
	if p, ok := adptr.(ApplyPrechecker); ok {
		return p.PrecheckApply(ctx, request)
	}
	return nil
}

type AccountAdapter interface {
	ApplyRecommendation(ctx AccountAdapterContext, recommendation ApplyRecommendationRequest, existingRecommendations []models.RecommendationResolution, recommendResolutionId string) (ApplyRecommendationResponse, error)
	GetRecommendationResolutionStatus(ctx AccountAdapterContext, Recommendation models.Recommendation, resolutionReferenceId string, applyRequestPayload models.Json, resolutionStatusMessage string) (GetRecommendationResolutionStatusResponse, error)
}
