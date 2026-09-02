package tools

import (
	"encoding/json"
	"fmt"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/watch"
	"strings"

	"github.com/google/uuid"
)

const ToolWatchResource = "watch_resource"

func init() {
	core.RegisterNBToolFactory(ToolWatchResource, func(accountId string) (core.NBTool, error) {
		return WatchResourceTool{}, nil
	})
}

// WatchResourceTool is the agent-facing entry point for the watch
// feature. The agent calls this tool with a description of WHAT to poll
// (a tool + arguments, or a SQL query) and HOW to decide we're done (a
// predicate). The tool persists the watch and returns a watch_id so the
// agent can reply to the user immediately and let the dispatcher take
// over.
//
// The tool's input schema is intentionally narrow and structured —
// historically we would have stored a free-form natural-language
// "termination_condition" and re-invoked the agent each poll, but that
// gave us non-deterministic observations and a polluted conversation
// history. v1 forces the caller to commit to a single observation
// primitive plus a predicate.
type WatchResourceTool struct{}

func (WatchResourceTool) Name() string             { return ToolWatchResource }
func (WatchResourceTool) GetType() core.NBToolType { return core.NBToolTypeTool }

// InferToolRequestType classifies watch_resource as a write (Create). A
// registered watch repeatedly drives side-effecting *_execute tools under a
// static admin context, so it MUST go through the per-account create gate in
// auth_agent.go — without this the tool defaults to Read and a read-only
// user's agent could register watches.
func (WatchResourceTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeCreate, nil
}

func (WatchResourceTool) Description() string {
	return strings.TrimSpace(`
Register a background watch that polls a read-only source on a fixed interval and notifies the user when a predicate matches or the watch expires. Use when the user wants async follow-up ("ping me when X is done", "check back in N minutes") or when an action you took has an outcome only verifiable later.

CRITICAL:
  - source_config.tool_name MUST be a primitive *_execute tool (shell_execute, kubectl_execute, github_execute, gitlab_execute, aws_execute, gcloud_execute, postgres_query_execute, events_execute). NEVER a sub-agent (kubectl, github, gitlab, postgres, events) — those load conversation history and fail in background polls.
  - source_config.tool_input MUST be a JSON object, not a bare string. Wrong: "tool_input": "gh run view 12345". Right: "tool_input": { "command": "gh run view 12345" }.
  - If the command uses an integration (gh, glab, aws, gcloud, az, psql, ...), use the MATCHING *_execute tool (github_execute for gh, gitlab_execute for glab, aws_execute for aws, ...) AND carry the same tool_config_name the action used — a bare shell_execute can't resolve which integration's credentials to inject when the tenant has more than one, so the poll fails auth.
  - The polled command must be idempotent and read-only.

Required fields: source_kind ("tool" | "sql"), source_config, predicate_kind ("regex" | "substring" | "llm_judge"), predicate_expr, poll_interval_sec, max_duration_sec. Optional: predicate_negate (true for "wait until X disappears").

source_config shapes:
  tool: {"tool_name": "<*_execute>", "tool_input": {<args>}, "tool_config_name": "<integration; required when the command uses one>"}
  sql:  {"datasource": "metastore", "query": "SELECT ...", "params": [...]}

Worked example (watch a GitHub workflow run until completion):
{"source_kind":"tool","source_config":{"tool_name":"github_execute","tool_input":{"command":"gh run view 12345 --repo owner/repo --json status --jq .status"},"tool_config_name":"<integration>"},"predicate_kind":"regex","predicate_expr":"completed","poll_interval_sec":60,"max_duration_sec":1800}

Returns: {"watch_id":"...","status":"PENDING","poll_interval_sec":N,"max_duration_sec":N,"next_poll_at":"...","message":"..."}
`)
}

func (WatchResourceTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"source_kind": {
				Type:        core.ToolSchemaTypeString,
				Description: "Which Source observes the resource each poll. Must be 'tool' or 'sql'.",
				Enum:        []any{string(watch.SourceKindTool), string(watch.SourceKindSQL)},
			},
			"source_config": {
				Type:        core.ToolSchemaTypeObject,
				Description: "Configuration for the chosen source kind. For 'tool': {tool_name, tool_input, command?}. For 'sql': {datasource, query, params?, max_rows?}.",
			},
			"predicate_kind": {
				Type:        core.ToolSchemaTypeString,
				Description: "How the observation is evaluated for termination. 'regex' / 'substring' check the textual observation; 'llm_judge' asks the model to decide.",
				Enum: []any{
					string(watch.PredicateKindRegex),
					string(watch.PredicateKindSubstring),
					string(watch.PredicateKindLLMJudge),
				},
			},
			"predicate_expr": {
				Type:        core.ToolSchemaTypeString,
				Description: "Predicate body: a regex pattern, a substring, or a yes/no question for the LLM judge.",
			},
			"predicate_negate": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "When true the predicate's met value is inverted. Useful for 'wait until X disappears' semantics with substring/regex.",
				Default:     false,
			},
			"poll_interval_sec": {
				Type:        core.ToolSchemaTypeInteger,
				Description: "Seconds between polls. Will be clamped up to the configured minimum.",
			},
			"max_duration_sec": {
				Type:        core.ToolSchemaTypeInteger,
				Description: "Hard ceiling on watch lifetime; the watch terminates as EXPIRED at this point. Will be clamped down to the configured maximum.",
			},
			"notify_template": {
				Type:        core.ToolSchemaTypeString,
				Description: "Optional template for the terminal notification. {summary} is substituted with the predicate summary.",
			},
		},
		Required: []string{"source_kind", "source_config", "predicate_kind", "predicate_expr", "poll_interval_sec", "max_duration_sec"},
	}
}

// watchResourceInput is the parsed and validated form of the agent's
// arguments. We intentionally re-decode rather than trusting Arguments'
// Go types because most callers send everything as JSON values.
type watchResourceInput struct {
	SourceKind      watch.SourceKind    `json:"source_kind"`
	SourceConfig    json.RawMessage     `json:"source_config"`
	PredicateKind   watch.PredicateKind `json:"predicate_kind"`
	PredicateExpr   string              `json:"predicate_expr"`
	PredicateNegate bool                `json:"predicate_negate"`
	PollIntervalSec int                 `json:"poll_interval_sec"`
	MaxDurationSec  int                 `json:"max_duration_sec"`
	NotifyTemplate  string              `json:"notify_template,omitempty"`
}

func (WatchResourceTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	if !config.Config.WatchEnabled {
		return errorResponse("watch feature is disabled (LLM_SERVER_WATCH_ENABLED=false)"), nil
	}

	parsed, err := parseWatchInput(input.Arguments)
	if err != nil {
		return errorResponse(err.Error()), nil
	}

	conversationID, err := uuid.Parse(nbCtx.ConversationId)
	if err != nil || conversationID == uuid.Nil {
		return errorResponse("watch_resource requires a valid conversation; none was provided"), nil
	}
	accountID, err := uuid.Parse(nbCtx.AccountId)
	if err != nil || accountID == uuid.Nil {
		return errorResponse("watch_resource requires a valid account_id"), nil
	}
	tenantID := uuid.Nil
	if sec := nbCtx.Ctx.GetSecurityContext(); sec != nil {
		if t, terr := uuid.Parse(sec.GetTenantId()); terr == nil {
			tenantID = t
		}
	}
	if tenantID == uuid.Nil {
		return errorResponse("watch_resource requires a valid tenant context"), nil
	}
	var userIDPtr *uuid.UUID
	if nbCtx.UserId != "" {
		if u, uerr := uuid.Parse(nbCtx.UserId); uerr == nil {
			userIDPtr = &u
		}
	}
	// Capture the parent message_id so the responder can land the terminal
	// follow-up on the exact agent reply that registered the watch, not
	// "the latest generation message" (which is the wrong target the
	// moment the user types a follow-up question while the watch runs).
	// Best-effort: malformed/empty message_id is acceptable — the
	// responder gracefully falls back to the latest-generation heuristic
	// when ParentMessageID is nil.
	var parentMessageIDPtr *uuid.UUID
	if nbCtx.MessageId != "" {
		if mid, merr := uuid.Parse(nbCtx.MessageId); merr == nil {
			parentMessageIDPtr = &mid
		}
	}

	manager := watch.NewManager()
	created, err := manager.Create(nbCtx.Ctx.GetContext(), watch.CreateInput{
		ConversationID:  conversationID,
		AccountID:       accountID,
		TenantID:        tenantID,
		UserID:          userIDPtr,
		ParentMessageID: parentMessageIDPtr,
		SourceKind:      parsed.SourceKind,
		SourceConfig:    parsed.SourceConfig,
		PredicateKind:   parsed.PredicateKind,
		PredicateExpr:   parsed.PredicateExpr,
		PredicateNegate: parsed.PredicateNegate,
		NotifyTemplate:  parsed.NotifyTemplate,
		PollIntervalSec: parsed.PollIntervalSec,
		MaxDurationSec:  parsed.MaxDurationSec,
	})
	if err != nil {
		return errorResponse(fmt.Sprintf("failed to register watch: %v", err)), nil
	}

	respBody := map[string]any{
		"watch_id":          created.ID.String(),
		"status":            string(created.Status),
		"poll_interval_sec": created.PollIntervalSec,
		"max_duration_sec":  created.MaxDurationSec,
		"next_poll_at":      created.NextPollAt,
		"message":           fmt.Sprintf("Watch %s registered. The dispatcher will poll every %ds and notify the conversation when the predicate is met or after %ds.", created.ID.String(), created.PollIntervalSec, created.MaxDurationSec),
	}
	encoded, _ := json.Marshal(respBody)
	return core.NBToolResponse{
		Data:   string(encoded),
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

// parseWatchInput accepts the loose map[string]any that the planner
// hands us and produces a typed, validated struct. We re-marshal to JSON
// so json.RawMessage fields (source_config) round-trip correctly.
func parseWatchInput(args map[string]any) (*watchResourceInput, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("watch_resource: empty arguments")
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("watch_resource: failed to re-encode args: %w", err)
	}
	out := &watchResourceInput{}
	if err := json.Unmarshal(encoded, out); err != nil {
		return nil, fmt.Errorf("watch_resource: invalid arguments: %w", err)
	}
	return out, nil
}

func errorResponse(msg string) core.NBToolResponse {
	return core.NBToolResponse{
		Data:   msg,
		Type:   core.NBToolResponseTypeText,
		Status: core.NBToolResponseStatusError,
	}
}
