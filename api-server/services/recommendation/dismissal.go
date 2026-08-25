package recommendation

import (
	"fmt"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/recommendation/coordinator"
	"nudgebee/services/security"
)

type RecommendationDismissalRequest struct {
	AccountId        string `json:"account_id" mapstructure:"account_id" validate:"required"`
	RecommendationId string `json:"recommendation_id" mapstructure:"recommendation_id" validate:"required"`
	Dismissed        bool   `json:"dismissed" mapstructure:"dismissed"`
	Reason           string `json:"reason" mapstructure:"reason"`
	// SnoozedUntil is RFC3339; set together with Dismissed=true it snoozes
	// instead of permanently dismissing. Carried as a string because the RPC
	// payload arrives as loose JSON.
	SnoozedUntil string `json:"snoozed_until" mapstructure:"snoozed_until"`
}

type RecommendationDismissalResponse struct {
	Status  models.RecommendationStatus `json:"status"`
	Applied bool                        `json:"applied"`
	Message string                      `json:"message,omitempty"`
}

// UpdateRecommendationDismissal dismisses, snoozes, or reactivates a
// recommendation via the coordinator, its only writer.
func UpdateRecommendationDismissal(ctx *security.RequestContext, request RecommendationDismissalRequest) (RecommendationDismissalResponse, error) {
	if err := common.ValidateStruct(request); err != nil {
		return RecommendationDismissalResponse{}, fmt.Errorf("invalid dismissal request: %w", err)
	}
	if !ctx.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeCreate) {
		return RecommendationDismissalResponse{}, common.ErrorUnauthorized("error: account access not found")
	}

	var snoozedUntil *time.Time
	if request.Dismissed && request.SnoozedUntil != "" {
		parsed, err := time.Parse(time.RFC3339, request.SnoozedUntil)
		if err != nil {
			return RecommendationDismissalResponse{}, fmt.Errorf("invalid snoozed_until (want RFC3339): %w", err)
		}
		if !parsed.After(time.Now().UTC()) {
			return RecommendationDismissalResponse{}, fmt.Errorf("snoozed_until must be in the future")
		}
		snoozedUntil = &parsed
	}

	result, err := coordinator.SetDismissal(ctx, request.RecommendationId, request.AccountId, request.Dismissed, request.Reason, snoozedUntil, ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return RecommendationDismissalResponse{}, err
	}
	return RecommendationDismissalResponse{Status: result.RecommendationStatus, Applied: result.Applied, Message: result.Reason}, nil
}
