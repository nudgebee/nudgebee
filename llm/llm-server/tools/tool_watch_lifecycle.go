package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/watch"

	"github.com/google/uuid"
)

// Lifecycle tools for the watch feature: status, cancel, list. They mirror
// the subscribe/inspect/unsubscribe surface that Claude Code exposes for
// PR-activity subscriptions, so the user can ask "what's my watch doing?"
// or "stop watching X" and the agent has a real answer instead of guessing
// from chat history.
//
// All three share the same enable-gate, account/tenant resolution, and
// error-response shape as watch_resource. They only differ in the SQL
// they ultimately drive.

const (
	ToolWatchStatus = "watch_status"
	ToolWatchCancel = "watch_cancel"
	ToolWatchList   = "watch_list"
)

func init() {
	core.RegisterNBToolFactory(ToolWatchStatus, func(accountId string) (core.NBTool, error) {
		return WatchStatusTool{}, nil
	})
	core.RegisterNBToolFactory(ToolWatchCancel, func(accountId string) (core.NBTool, error) {
		return WatchCancelTool{}, nil
	})
	core.RegisterNBToolFactory(ToolWatchList, func(accountId string) (core.NBTool, error) {
		return WatchListTool{}, nil
	})
}

// ---------------------------------------------------------------------------
// watch_status
// ---------------------------------------------------------------------------

type WatchStatusTool struct{}

func (WatchStatusTool) Name() string             { return ToolWatchStatus }
func (WatchStatusTool) GetType() core.NBToolType { return core.NBToolTypeTool }

// InferToolRequestType — watch_status is read-only (inspects a watch row).
func (WatchStatusTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func (WatchStatusTool) Description() string {
	return `Look up the current state of a previously-registered watch by ID.
Returns status (PENDING|ACTIVE|COMPLETED|EXPIRED|FAILED|CANCELLED), poll
counts, the most recent observation, and termination details if the
watch has finished. Use when the user asks "where is my watch?" or
"is it still running?".`
}

func (WatchStatusTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"watch_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "UUID of the watch to inspect (returned by watch_resource).",
			},
		},
		Required: []string{"watch_id"},
	}
}

func (WatchStatusTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	if !config.Config.WatchEnabled {
		return errorResponse("watch feature is disabled (LLM_SERVER_WATCH_ENABLED=false)"), nil
	}
	id, err := requireWatchID(input.Arguments)
	if err != nil {
		return errorResponse(err.Error()), nil
	}
	tenantID, terr := requireTenantID(nbCtx)
	if terr != nil {
		return errorResponse(terr.Error()), nil
	}
	w, err := watch.NewManager().Get(nbCtx.Ctx.GetContext(), tenantID, id)
	if err != nil {
		if errors.Is(err, watch.ErrWatchNotFound) {
			return errorResponse(fmt.Sprintf("watch %s not found", id)), nil
		}
		return errorResponse(fmt.Sprintf("failed to look up watch: %v", err)), nil
	}
	return jsonResponse(watchSummary(w))
}

// ---------------------------------------------------------------------------
// watch_cancel
// ---------------------------------------------------------------------------

type WatchCancelTool struct{}

func (WatchCancelTool) Name() string             { return ToolWatchCancel }
func (WatchCancelTool) GetType() core.NBToolType { return core.NBToolTypeTool }

// InferToolRequestType — watch_cancel mutates a watch (terminates it), so it
// must pass the per-account write gate in auth_agent.go rather than defaulting
// to Read (which would let a read-only user cancel watches).
func (WatchCancelTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeUpdate, nil
}

func (WatchCancelTool) Description() string {
	return `Cancel a watch by ID. Idempotent — cancelling an already-terminal
watch is a no-op success. Use when the user says "stop watching X" or
"never mind, you can stop checking". A cancelled watch does NOT send a
notification (the user already knows).`
}

func (WatchCancelTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"watch_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "UUID of the watch to cancel.",
			},
		},
		Required: []string{"watch_id"},
	}
}

func (WatchCancelTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	if !config.Config.WatchEnabled {
		return errorResponse("watch feature is disabled (LLM_SERVER_WATCH_ENABLED=false)"), nil
	}
	id, err := requireWatchID(input.Arguments)
	if err != nil {
		return errorResponse(err.Error()), nil
	}
	tenantID, terr := requireTenantID(nbCtx)
	if terr != nil {
		return errorResponse(terr.Error()), nil
	}
	if err := watch.NewManager().Cancel(nbCtx.Ctx.GetContext(), tenantID, id); err != nil {
		if errors.Is(err, watch.ErrWatchNotFound) {
			return errorResponse(fmt.Sprintf("watch %s not found", id)), nil
		}
		return errorResponse(fmt.Sprintf("failed to cancel watch: %v", err)), nil
	}
	return jsonResponse(map[string]any{
		"watch_id":  id.String(),
		"cancelled": true,
		"message":   fmt.Sprintf("Watch %s cancelled.", id.String()),
	})
}

// ---------------------------------------------------------------------------
// watch_list
// ---------------------------------------------------------------------------

type WatchListTool struct{}

func (WatchListTool) Name() string             { return ToolWatchList }
func (WatchListTool) GetType() core.NBToolType { return core.NBToolTypeTool }

// InferToolRequestType — watch_list is read-only (lists watch rows).
func (WatchListTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func (WatchListTool) Description() string {
	return `List all watches registered against the current conversation,
oldest first. Each entry is a summary suitable for showing the user
("you have 3 active watches…"). Includes terminal watches so the user
can see what already finished. Takes no input.`
}

func (WatchListTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type:       core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{},
	}
}

func (WatchListTool) Call(nbCtx core.NbToolContext, _ core.NBToolCallRequest) (core.NBToolResponse, error) {
	if !config.Config.WatchEnabled {
		return errorResponse("watch feature is disabled (LLM_SERVER_WATCH_ENABLED=false)"), nil
	}
	conversationID, err := uuid.Parse(nbCtx.ConversationId)
	if err != nil || conversationID == uuid.Nil {
		return errorResponse("watch_list requires a valid conversation; none was provided"), nil
	}
	tenantID, terr := requireTenantID(nbCtx)
	if terr != nil {
		return errorResponse(terr.Error()), nil
	}
	watches, err := watch.NewManager().ListByConversation(nbCtx.Ctx.GetContext(), tenantID, conversationID)
	if err != nil {
		return errorResponse(fmt.Sprintf("failed to list watches: %v", err)), nil
	}
	summaries := make([]map[string]any, 0, len(watches))
	for i := range watches {
		summaries = append(summaries, watchSummary(&watches[i]))
	}
	return jsonResponse(map[string]any{
		"conversation_id": conversationID.String(),
		"count":           len(summaries),
		"watches":         summaries,
	})
}

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

// watchSummary projects a Watch into the shape the agent shows to the
// user. We deliberately omit source_config / predicate_expr — they're
// authored by the agent and rarely useful to surface verbatim.
func watchSummary(w *watch.Watch) map[string]any {
	out := map[string]any{
		"watch_id":          w.ID.String(),
		"status":            string(w.Status),
		"source_kind":       string(w.SourceKind),
		"predicate_kind":    string(w.PredicateKind),
		"poll_count":        w.PollCount,
		"failure_count":     w.FailureCount,
		"poll_interval_sec": w.PollIntervalSec,
		"max_duration_sec":  w.MaxDurationSec,
		"next_poll_at":      w.NextPollAt,
		"expires_at":        w.ExpiresAt,
		"created_at":        w.CreatedAt,
	}
	if w.LastPollAt != nil {
		out["last_poll_at"] = *w.LastPollAt
	}
	if w.LastPollResult != nil {
		out["last_poll_result"] = *w.LastPollResult
	}
	if w.FinalResult != nil {
		out["final_result"] = *w.FinalResult
	}
	if w.Error != nil {
		out["error"] = *w.Error
	}
	return out
}

// requireTenantID extracts the caller's tenant from the security context.
// All lifecycle tools must scope their DAO calls by tenant — otherwise a
// user can read or cancel another tenant's watch by replaying its UUID.
func requireTenantID(nbCtx core.NbToolContext) (uuid.UUID, error) {
	sec := nbCtx.Ctx.GetSecurityContext()
	if sec == nil {
		return uuid.Nil, fmt.Errorf("no tenant context available; cannot scope watch operation safely")
	}
	tenantID, err := uuid.Parse(sec.GetTenantId())
	if err != nil || tenantID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("invalid tenant context; cannot scope watch operation safely")
	}
	return tenantID, nil
}

// requireWatchID extracts and parses the watch_id arg. Centralized so
// status / cancel agree on validation.
func requireWatchID(args map[string]any) (uuid.UUID, error) {
	raw, ok := args["watch_id"]
	if !ok {
		return uuid.Nil, fmt.Errorf("watch_id is required")
	}
	s, ok := raw.(string)
	if !ok || s == "" {
		return uuid.Nil, fmt.Errorf("watch_id must be a non-empty string")
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("watch_id %q is not a valid UUID: %w", s, err)
	}
	return id, nil
}

func jsonResponse(body map[string]any) (core.NBToolResponse, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return errorResponse(fmt.Sprintf("internal: failed to encode response: %v", err)), nil
	}
	return core.NBToolResponse{
		Data:   string(encoded),
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}
