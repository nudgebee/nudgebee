package core

import (
	"fmt"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// EventDetailsRetrievalTitle is excluded from productivity aggregates because
// it represents an internal event lookup, not a user-facing investigation.
// Mirrors the constant used by the frontend troubleshoot summary widget.
const EventDetailsRetrievalTitle = "Event details retrieval by ID"

type ConversationTimeAggregatesRequest struct {
	// AccountId optionally narrows the rollup to one account. When empty the
	// handler falls back to every account the caller's session is permitted
	// to read, matching the multi-account behaviour the dashboard widget had
	// when it queried Hasura under role-based row filters.
	AccountId string `json:"account_id"`
	UserId    string `json:"user_id"`

	// StartDate / EndDate are inclusive bounds on llm_conversations.updated_at
	// (RFC3339). The frontend supplies UTC ISO timestamps from the same
	// rolling 24h window the legacy widget used.
	StartDate string `json:"start_date" validate:"required"`
	EndDate   string `json:"end_date" validate:"required"`

	// Sources scopes to specific ConversationSource values (e.g.
	// ["Investigation"] for the auto tab, ["UserInvestigation"] for manual).
	// Empty includes all sources.
	Sources []string `json:"sources,omitempty"`

	// EventScoped narrows to conversations whose title contains a UUID,
	// matching the auto-investigation tab semantics in groupings_v2.
	EventScoped bool `json:"event_scoped,omitempty"`
}

// ConversationTimeAggregatesResponse exposes both the rolled-up time numbers
// and the productivity tunables so the frontend renders consistent values
// without owning either source of truth.
type ConversationTimeAggregatesResponse struct {
	CompletedCount              int     `json:"completed_count"`
	TotalCount                  int     `json:"total_count"`
	TotalWallTimeSeconds        float64 `json:"total_wall_time_seconds"`
	TotalAgentActiveTimeSeconds float64 `json:"total_agent_active_time_seconds"`
	TotalToolTimeSeconds        float64 `json:"total_tool_time_seconds"`

	// ManualBaselineMinutes is the assumed time a human investigator would
	// spend per task; productivity = (baseline - avg AI time) / baseline.
	ManualBaselineMinutes int `json:"manual_baseline_minutes"`

	// EngineerHourlyRateUsd converts saved hours into the cost-saved widget.
	EngineerHourlyRateUsd float64 `json:"engineer_hourly_rate_usd"`
}

// HandleConversationTimeAggregatesApi rolls up wall / agent-active / tool
// time across many conversations for the troubleshoot dashboard. It
// intentionally returns raw seconds plus the productivity tunables — the
// frontend still renders averages and percentages, but it no longer owns the
// arithmetic on raw conversation timestamps.
func HandleConversationTimeAggregatesApi(ctx *security.RequestContext, request ConversationTimeAggregatesRequest) (ConversationTimeAggregatesResponse, error) {
	startDate, err := time.Parse(time.RFC3339, request.StartDate)
	if err != nil {
		return ConversationTimeAggregatesResponse{}, fmt.Errorf("HandleConversationTimeAggregatesApi: invalid start_date: %w", err)
	}
	endDate, err := time.Parse(time.RFC3339, request.EndDate)
	if err != nil {
		return ConversationTimeAggregatesResponse{}, fmt.Errorf("HandleConversationTimeAggregatesApi: invalid end_date: %w", err)
	}

	// Resolve scoping: explicit account_id takes precedence after authz, else
	// fall back to every account the caller can read. This mirrors the legacy
	// widget which leaned on Hasura's role-based row filters to pick the same
	// set automatically.
	var accountIDs []string
	if request.AccountId != "" {
		if !ctx.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			return ConversationTimeAggregatesResponse{}, fmt.Errorf("HandleConversationTimeAggregatesApi: forbidden account_id")
		}
		accountIDs = []string{request.AccountId}
	} else {
		accountIDs = ctx.GetSecurityContext().ListAccountIds()
	}

	filter := ConversationTimeAggregatesFilter{
		AccountIDs:     accountIDs,
		StartDate:      startDate,
		EndDate:        endDate,
		Sources:        request.Sources,
		ExcludedTitles: []string{EventDetailsRetrievalTitle},
		EventScoped:    request.EventScoped,
	}

	aggregates, err := GetConversationDao().GetConversationTimeAggregates(filter)
	if err != nil {
		return ConversationTimeAggregatesResponse{}, err
	}

	return ConversationTimeAggregatesResponse{
		CompletedCount:              aggregates.CompletedCount,
		TotalCount:                  aggregates.TotalCount,
		TotalWallTimeSeconds:        aggregates.TotalWallTimeSeconds,
		TotalAgentActiveTimeSeconds: aggregates.TotalAgentActiveTimeSeconds,
		TotalToolTimeSeconds:        aggregates.TotalToolTimeSeconds,
		ManualBaselineMinutes:       config.Config.ProductivityManualBaselineMinutes,
		EngineerHourlyRateUsd:       config.Config.ProductivityEngineerHourlyRateUsd,
	}, nil
}
