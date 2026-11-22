package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

func init() {
	core.RegisterNBToolFactory(ToolRecommendationSetDismissal, func(accountId string) (core.NBTool, error) {
		return RecommendationDismissalTool{}, nil
	})
}

const ToolRecommendationSetDismissal = "recommendation_set_dismissal"

const (
	dismissalActionDismiss    = "dismiss"
	dismissalActionSnooze     = "snooze"
	dismissalActionReactivate = "reactivate"

	defaultSnoozeDays = 30
	maxSnoozeDays     = 365
)

var dismissalPastTense = map[string]string{
	dismissalActionDismiss:    "dismissed",
	dismissalActionSnooze:     "snoozed",
	dismissalActionReactivate: "reactivated",
}

// RecommendationDismissalTool parks or restores a recommendation through
// api-server's recommendations_update_dismissal RPC under the requesting
// user's role — the same endpoint the Optimise tabs use. Dismiss and snooze
// take an Open row out of the listings and the savings headline; snooze
// brings it back on a date, dismiss keeps it out until someone reactivates
// it. The backend accepts dismiss/snooze from Open and reactivate from
// Dismissed only, and answers applied=false with a reason otherwise.
type RecommendationDismissalTool struct{}

func (m RecommendationDismissalTool) Name() string { return ToolRecommendationSetDismissal }
func (m RecommendationDismissalTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m RecommendationDismissalTool) Description() string {
	return "Dismisses, snoozes or reactivates a recommendation after the user confirms — the same action as Dismiss / snooze on Optimise. Use when the user decides a finding is accepted risk (dismiss), should wait (snooze for N days, default 30), or should be back on the list (reactivate). Only an Open recommendation can be dismissed or snoozed and only a Dismissed one reactivated; the tool reports when the row's status does not allow the change. Inputs: recommendation_id (required), action (required: dismiss | snooze | reactivate), reason (optional, shown on the row), snooze_days (snooze only, 1–365, default 30)."
}

func (m RecommendationDismissalTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"recommendation_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "UUID of the recommendation (recommendation.id).",
			},
			"action": {
				Type:        core.ToolSchemaTypeString,
				Description: "dismiss (park it until someone reactivates it), snooze (park it until a date), or reactivate (put a dismissed or snoozed row back on the list).",
				Enum:        []any{dismissalActionDismiss, dismissalActionSnooze, dismissalActionReactivate},
			},
			"reason": {
				Type:        core.ToolSchemaTypeString,
				Description: "Why, in the user's words; recorded on the row and shown on Optimise.",
			},
			"snooze_days": {
				Type:        core.ToolSchemaTypeInteger,
				Description: "For snooze: how many days from now the recommendation returns to Open (1–365, default 30).",
			},
		},
		Required: []string{"recommendation_id", "action"},
	}
}

// InferToolRequestType is static on purpose (see RecommendationApplyTool).
func (m RecommendationDismissalTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeUpdate, nil
}

// ConfirmationKey makes the write confirmation per-action.
func (m RecommendationDismissalTool) ConfirmationKey(toolInput string) string {
	return perActionConfirmationKey(ToolRecommendationSetDismissal, toolInput)
}

// snoozeDays reads snooze_days as a whole number of days, defaulting when
// absent; the RPC payload may carry it as a float.
func snoozeDays(args map[string]any) (int, error) {
	raw, ok := args["snooze_days"]
	if !ok || raw == nil {
		return defaultSnoozeDays, nil
	}
	var days int
	switch v := raw.(type) {
	case float64:
		days = int(v)
	case int:
		days = v
	case int64:
		days = int(v)
	default:
		return 0, fmt.Errorf("snooze_days must be a number of days, got %T", raw)
	}
	if days < 1 || days > maxSnoozeDays {
		return 0, fmt.Errorf("snooze_days must be between 1 and %d, got %d", maxSnoozeDays, days)
	}
	return days, nil
}

func snoozeUntil(days int, now time.Time) time.Time {
	return now.UTC().Add(time.Duration(days) * 24 * time.Hour).Truncate(time.Minute)
}

// ConfirmationQuestion states the effect on the row in operator terms.
func (m RecommendationDismissalTool) ConfirmationQuestion(toolInput string) string {
	args := confirmationArgs(toolInput)
	recommendationId, _ := args["recommendation_id"].(string)
	action, _ := args["action"].(string)
	if recommendationId == "" || action == "" {
		return ""
	}
	reason, _ := args["reason"].(string)
	because := ""
	if reason != "" {
		because = fmt.Sprintf(" (reason: %s)", reason)
	}
	switch action {
	case dismissalActionDismiss:
		return fmt.Sprintf("Dismiss recommendation %s%s?\nIt leaves the Open list and stops counting toward savings until someone reactivates it. Do you want to continue?", recommendationId, because)
	case dismissalActionSnooze:
		days, err := snoozeDays(args)
		if err != nil {
			return ""
		}
		return fmt.Sprintf("Snooze recommendation %s for %d days, until %s%s?\nIt leaves the Open list and comes back on that date. Do you want to continue?",
			recommendationId, days, snoozeUntil(days, time.Now()).Format("2 Jan 2006"), because)
	case dismissalActionReactivate:
		return fmt.Sprintf("Reactivate recommendation %s?\nIt returns to the Open list and counts toward savings again. Do you want to continue?", recommendationId)
	}
	return ""
}

func (m RecommendationDismissalTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	recommendationId := stringArg(input, "recommendation_id")
	if recommendationId == "" {
		return errNBLLMToolResponse(errRecommendationIdRequired), nil
	}
	action := stringArg(input, "action")

	object := map[string]any{
		"account_id":        nbCtx.AccountId,
		"recommendation_id": recommendationId,
		"reason":            stringArg(input, "reason"),
	}
	switch action {
	case dismissalActionDismiss:
		object["dismissed"] = true
	case dismissalActionSnooze:
		days, err := snoozeDays(input.Arguments)
		if err != nil {
			return errNBLLMToolResponse(err), nil
		}
		object["dismissed"] = true
		object["snoozed_until"] = snoozeUntil(days, time.Now()).Format(time.RFC3339)
	case dismissalActionReactivate:
		object["dismissed"] = false
	default:
		return errNBLLMToolResponse(errors.New("action must be one of dismiss, snooze, reactivate")), nil
	}

	resp, err := doApiServerActionRequest(nbCtx, "/rpc/recommendation", "recommendations_update_dismissal", recommendationsExecuteKey, map[string]any{"object": object}, ToolRecommendationSetDismissal)
	if err != nil {
		nbCtx.Ctx.GetLogger().Error("recommendation_set_dismissal: failed", "error", err, "recommendation_id", recommendationId, "action", action)
		return errNBLLMToolResponse(err), nil
	}

	// The backend refuses a change the row's status does not allow (e.g. a
	// row already InProgress) with applied=false and a reason; surface that
	// as a failure so the agent reports it instead of claiming success.
	var result struct {
		Applied *bool  `json:"applied"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(resp), &result); err == nil && result.Applied != nil && !*result.Applied {
		reason := result.Message
		if reason == "" {
			reason = "the recommendation's current status does not allow this change"
		}
		return errNBLLMToolResponse(fmt.Errorf("recommendation %s was not %s: %s", recommendationId, dismissalPastTense[action], reason)), nil
	}
	return core.NBToolResponse{
		Data:   resp,
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}
