package ai

import (
	"encoding/base64"
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/tasks/types"

	"go.temporal.io/sdk/activity"
)

// LLMEventInvestigateTask defines a task that interacts with an LLM for event investigation.
type LLMEventInvestigateTask struct{}

// GetName returns the unique name of the task.
func (t *LLMEventInvestigateTask) GetName() string {
	return "llm.event_investigate"
}

// GetDescription returns a brief description of the task.
func (t *LLMEventInvestigateTask) GetDescription() string {
	return "Ask AI to investigate a specific event or alert. Designed for event-triggered workflows — automatically analyzes the event context, checks related resources, and provides root cause analysis."
}

// GetDisplayName returns a human-readable name for the task.
func (t *LLMEventInvestigateTask) GetDisplayName() string {
	return "AI Event Investigation"
}

// troubleshootRequest is the envelope llm-server's
// processTroubleshootingEventFromMq consumes. The shape MUST match
// llm-server/api/event_analyzer.go EventAnalysisRequest fields used by the
// MQ path: event_id, account_id, task_token. Other EventAnalysisRequest
// fields default to zero values which the consumer treats as no-op.
type troubleshootRequest struct {
	EventId   string `json:"event_id"`
	AccountId string `json:"account_id"`
	TaskToken string `json:"task_token"`
}

// Execute kicks off (or re-uses) an investigation in llm-server and
// suspends the activity. llm-server publishes a completion envelope to
// the configured completion exchange when the analysis pipeline reaches
// a terminal state; the corresponding consumer in runbook-server
// (internal/events/investigation_completion.go) calls
// temporalClient.CompleteActivity with this activity's task_token,
// resuming the workflow with the investigation result.
//
// Activity StartToCloseTimeout governs how long this can wait — the
// default is 10m (set by createActivityOptionsForTask), overridable per
// task via task.timeout in the workflow definition.
func (t *LLMEventInvestigateTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing LLMEventInvestigateTask", "params", params)

	if !activity.IsActivity(taskCtx.GetContext()) {
		return nil, fmt.Errorf("llm.event_investigate task is not allowed to execute directly. It must be part of a workflow")
	}

	eventID, _ := params["event_id"].(string)
	accountID, _ := params["account_id"].(string)
	if eventID == "" {
		return nil, fmt.Errorf("llm.event_investigate: event_id is required (use {{ event.id }})")
	}
	if accountID == "" {
		return nil, fmt.Errorf("llm.event_investigate: account_id is required (use {{ event.cloud_account_id }})")
	}

	// Capture the Temporal activity token. llm-server treats this as an
	// opaque correlation handle and echoes it back in the completion
	// envelope; only runbook-server's CompleteActivity caller needs to
	// decode it. Base64-encode to keep the JSON envelope ASCII-safe.
	taskToken := activity.GetInfo(taskCtx.GetContext()).TaskToken
	encodedToken := base64.StdEncoding.EncodeToString(taskToken)

	exch := config.Config.RabbitMqTroubleshootExchange
	rk := config.Config.RabbitMqTroubleshootRoutingKey
	if exch == "" || rk == "" {
		return nil, fmt.Errorf("llm.event_investigate: troubleshoot exchange/routing-key not configured")
	}

	req := troubleshootRequest{
		EventId:   eventID,
		AccountId: accountID,
		TaskToken: encodedToken,
	}
	if err := common.MqPublish(exch, rk, req); err != nil {
		taskCtx.GetLogger().Error("llm.event_investigate: failed to publish troubleshoot request", "error", err, "event_id", eventID)
		return nil, fmt.Errorf("failed to enqueue investigation: %w", err)
	}

	taskCtx.GetLogger().Info("llm.event_investigate: investigation requested, awaiting completion", "event_id", eventID, "account_id", accountID)
	return nil, activity.ErrResultPending
}

// InputSchema returns the schema for the task's expected parameters.
func (t *LLMEventInvestigateTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"account_id": {
				Type:        types.PropertyTypeAccount,
				Description: "Cloud account ID owning the event. Typically {{ event.cloud_account_id }}.",
				Required:    true,
				Order:       1,
			},
			"event_id": {
				Type:        types.PropertyTypeString,
				Description: "Event ID to investigate. Typically {{ event.id }} when triggered from an event-bound workflow.",
				Required:    true,
				Order:       2,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *LLMEventInvestigateTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        "string",
				Description: "Synthesized investigation summary (markdown). Falls back to the initial event summary when the deeper analysis pipeline was disabled or partial.",
				Required:    true,
			},
			"log_summary": {
				Type:        "string",
				Description: "Short structured event summary (Step 1 of the analysis pipeline).",
				Required:    false,
			},
			"log_analysis": {
				Type:        "string",
				Description: "Structured log evidence dig (Step 3 of the analysis pipeline). Empty when no logs were available.",
				Required:    false,
			},
			"investigation": {
				Type:        "string",
				Description: "Conversational root-cause analysis (Step 2 of the analysis pipeline).",
				Required:    false,
			},
			"status": {
				Type:        "string",
				Description: "Terminal status of the analysis (COMPLETED / FAILED).",
				Required:    false,
			},
			"status_reason": {
				Type:        "string",
				Description: "Human-readable reason when status is FAILED.",
				Required:    false,
			},
		},
	}
}
