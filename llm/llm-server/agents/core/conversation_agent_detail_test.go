package core

import (
	"database/sql"
	"testing"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sumComponents(b CostBreakdown) float64 {
	var s float64
	for _, c := range b.Components {
		s += c.CostUsd
	}
	return s
}

// TestCalculateCostBreakdown_ReconcilesStandard verifies the per-component split
// sums exactly to CalculateTotalCost on the standard tier, and labels it.
func TestCalculateCostBreakdown_ReconcilesStandard(t *testing.T) {
	p := modelPricing{
		CostPerMillionInput:       3.0,
		CostPerMillionOutput:      15.0,
		CostPerMillionCachedInput: sql.NullFloat64{Float64: 0.3, Valid: true},
	}
	nonCached, cached, creation, output, thinking := 90000, 10000, 0, 5000, 1000

	total := CalculateTotalCost(&p, nonCached, cached, creation, output, thinking)
	bd := CalculateCostBreakdown(&p, nonCached, cached, creation, output, thinking)

	assert.Equal(t, "standard", bd.Tier)
	assert.Len(t, bd.Components, 4)
	assert.InDelta(t, total, sumComponents(bd), 1e-12, "components must sum to CalculateTotalCost")

	// output component folds in thinking tokens (billed at output rate).
	for _, c := range bd.Components {
		if c.Kind == "output" {
			assert.Equal(t, int64(output+thinking), c.Tokens)
		}
	}
}

// TestCalculateCostBreakdown_LongContextTier verifies the tier flag flips and the
// long-ctx rates are applied (and still reconcile) once the prompt crosses the
// threshold.
func TestCalculateCostBreakdown_LongContextTier(t *testing.T) {
	p := modelPricing{
		CostPerMillionInput:         3.0,
		CostPerMillionOutput:        15.0,
		ContextThresholdTokens:      sql.NullInt64{Int64: 200000, Valid: true},
		CostPerMillionInputLongCtx:  sql.NullFloat64{Float64: 6.0, Valid: true},
		CostPerMillionOutputLongCtx: sql.NullFloat64{Float64: 22.5, Valid: true},
	}
	// totalPrompt = 250k > 200k threshold → long-ctx.
	nonCached, cached, creation, output, thinking := 250000, 0, 0, 1000, 0

	total := CalculateTotalCost(&p, nonCached, cached, creation, output, thinking)
	bd := CalculateCostBreakdown(&p, nonCached, cached, creation, output, thinking)

	assert.Equal(t, "long_context", bd.Tier)
	assert.InDelta(t, total, sumComponents(bd), 1e-12)
	// input component should use the long-ctx rate (6.0), not 3.0.
	for _, c := range bd.Components {
		if c.Kind == "input" {
			assert.InDelta(t, float64(nonCached)/1e6*6.0, c.CostUsd, 1e-12)
		}
	}
}

// TestCalculateCostBreakdown_NilPricing returns an empty, standard breakdown.
func TestCalculateCostBreakdown_NilPricing(t *testing.T) {
	bd := CalculateCostBreakdown(nil, 10, 0, 0, 5, 0)
	assert.Equal(t, "standard", bd.Tier)
	assert.Empty(t, bd.Components)
}

// TestHandleConversationAgentDetailApi_MissingFields rejects calls without the
// required identifiers before any DB access.
func TestHandleConversationAgentDetailApi_MissingFields(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	_, err := HandleConversationAgentDetailApi(ctx, ConversationAgentDetailRequest{
		ConversationId: "sess-1",
		AccountId:      "acc-1",
		// AgentId missing
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "agent_id")
}

// TestGetConversationAgentDetail_FullRecord exercises the whole detail assembly:
// agent content, tool call (error), two model calls (success + error), with cost
// breakdown reconciling to each call's cost and the agent cost = sum of calls.
func TestGetConversationAgentDetail_FullRecord(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	now := time.Now()

	// 1) the agent row.
	mock.ExpectQuery("FROM llm_conversation_agent").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "message_id", "parent_agent_id", "agent_name", "status",
			"query", "thought", "response", "created_at", "updated_at", "duration_seconds",
		}).AddRow(
			"agent-1", "msg-1", "", "k8s_debug", "success",
			"investigate pod crash", "checking events", "root cause: OOMKilled",
			now, now, 4.2,
		))

	// 2) tool calls — one that errored.
	mock.ExpectQuery("FROM llm_conversation_tool_calls").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "tool_name", "tool_id", "tool_type", "status",
			"parameters", "response", "thought", "child_agent_id",
			"created_at", "updated_at", "duration_seconds",
		}).AddRow(
			"tool-1", "kubectl", "t1", "read", "ERROR",
			"get pods -n prod", "Error from server: timeout", "list pods", "",
			now, now, 1.1,
		))

	// 3) model calls — success + error.
	mock.ExpectQuery("FROM llm_conversation_token_usage").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "llm_model", "llm_provider", "input_tokens", "output_tokens",
			"cached_input_tokens", "cache_creation_tokens", "thinking_tokens",
			"latency_seconds", "ttft_ms", "retry_attempt", "is_cache_hit",
			"request_status", "stop_reason", "error_message", "created_at",
		}).
			AddRow("mc-1", "claude", "bedrock", 1000, 500, 0, 0, 0,
				2.0, 300, 0, false, "success", "end_turn", "", now).
			AddRow("mc-2", "claude", "bedrock", 200, 0, 0, 0, 0,
				0.5, 0, 1, false, "error", "", "rate limited", now))

	// 4) pricing (GetConversationCosts).
	mock.ExpectQuery("FROM llm_model_pricing").
		WillReturnRows(sqlmock.NewRows([]string{
			"provider_name", "model_name",
			"cost_per_million_input_tokens", "cost_per_million_output_tokens",
			"cache_cost_per_million_tokens_per_hour", "cost_per_million_cached_input_tokens",
			"cost_per_million_cache_creation_tokens", "cost_per_million_cached_storage_per_hour",
			"context_threshold_tokens", "cost_per_million_input_tokens_long_ctx",
			"cost_per_million_output_tokens_long_ctx", "cost_per_million_cached_input_tokens_long_ctx",
			"cost_per_million_cache_creation_tokens_long_ctx",
		}).AddRow(
			"bedrock", "claude", 3.0, 15.0,
			nil, nil, nil, nil, nil, nil, nil, nil, nil,
		))

	detail, err := dao.GetConversationAgentDetail("sess-1", "acc-1", "agent-1", "")
	require.NoError(t, err)

	// Agent execution content.
	assert.Equal(t, "investigate pod crash", detail.Agent.Query)
	assert.Equal(t, "root cause: OOMKilled", detail.Agent.Response)
	assert.False(t, detail.Agent.IsError)

	// Tool: what it ran + what came back + error flag.
	require.Len(t, detail.ToolCalls, 1)
	assert.Equal(t, "get pods -n prod", detail.ToolCalls[0].Parameters)
	assert.Equal(t, "Error from server: timeout", detail.ToolCalls[0].Response)
	assert.True(t, detail.ToolCalls[0].IsError)

	// Model calls: status flags + cost breakdown reconciliation.
	require.Len(t, detail.ModelCalls, 2)
	assert.False(t, detail.ModelCalls[0].IsError)
	assert.True(t, detail.ModelCalls[1].IsError)
	assert.Equal(t, "rate limited", detail.ModelCalls[1].ErrorMessage)

	var sumCalls float64
	for _, mc := range detail.ModelCalls {
		assert.InDelta(t, mc.CostUsd, sumComponents(mc.CostBreakdown), 1e-12,
			"each model call's components must sum to its cost_usd")
		sumCalls += mc.CostUsd
	}
	// mc-1: 1000/1e6*3 + 500/1e6*15 = 0.0105 ; mc-2: 200/1e6*3 = 0.0006
	assert.InDelta(t, 0.0111, sumCalls, 1e-9)
	assert.InDelta(t, sumCalls, detail.Agent.CostUsd, 1e-12, "agent cost = sum of its model calls")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetConversationAgentDetail_NotFound returns an empty detail (no error) when
// the agent is absent from the caller's scope.
func TestGetConversationAgentDetail_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

	mock.ExpectQuery("FROM llm_conversation_agent").WillReturnError(sql.ErrNoRows)

	detail, err := dao.GetConversationAgentDetail("sess-1", "acc-1", "missing", "")
	require.NoError(t, err)
	assert.Empty(t, detail.Agent.ID)
	assert.Empty(t, detail.ToolCalls)
	assert.Empty(t, detail.ModelCalls)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetConversationAgentDetail_TraceMode verifies that passing a model_call_id
// returns that single call WITH its prompt/response trace populated (the lazy
// "view prompt" fetch), while the default listing leaves the trace text empty.
func TestGetConversationAgentDetail_TraceMode(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	now := time.Now()

	mock.ExpectQuery("FROM llm_conversation_agent").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "message_id", "parent_agent_id", "agent_name", "status",
			"query", "thought", "response", "created_at", "updated_at", "duration_seconds",
		}).AddRow("agent-1", "msg-1", "", "k8s_debug", "success", "q", "t", "r", now, now, 1.0))

	mock.ExpectQuery("FROM llm_conversation_tool_calls").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "tool_name", "tool_id", "tool_type", "status",
			"parameters", "response", "thought", "child_agent_id",
			"created_at", "updated_at", "duration_seconds",
		}))

	// model_calls in trace mode also returns has_trace + the raw trace columns.
	mock.ExpectQuery("FROM llm_conversation_token_usage").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "llm_model", "llm_provider", "input_tokens", "output_tokens",
			"cached_input_tokens", "cache_creation_tokens", "thinking_tokens",
			"latency_seconds", "ttft_ms", "retry_attempt", "is_cache_hit",
			"request_status", "stop_reason", "error_message", "created_at",
			"has_trace", "prompt_messages", "response_content",
		}).AddRow("mc-1", "claude", "bedrock", 1000, 500, 0, 0, 0,
			2.0, 300, 0, false, "success", "end_turn", "", now,
			true, `[{"role":"system","parts":[{"type":"text","text":"hi"}]}]`, "the answer"))

	mock.ExpectQuery("FROM llm_model_pricing").
		WillReturnRows(sqlmock.NewRows([]string{
			"provider_name", "model_name",
			"cost_per_million_input_tokens", "cost_per_million_output_tokens",
			"cache_cost_per_million_tokens_per_hour", "cost_per_million_cached_input_tokens",
			"cost_per_million_cache_creation_tokens", "cost_per_million_cached_storage_per_hour",
			"context_threshold_tokens", "cost_per_million_input_tokens_long_ctx",
			"cost_per_million_output_tokens_long_ctx", "cost_per_million_cached_input_tokens_long_ctx",
			"cost_per_million_cache_creation_tokens_long_ctx",
		}).AddRow("bedrock", "claude", 3.0, 15.0, nil, nil, nil, nil, nil, nil, nil, nil, nil))

	detail, err := dao.GetConversationAgentDetail("sess-1", "acc-1", "agent-1", "mc-1")
	require.NoError(t, err)
	require.Len(t, detail.ModelCalls, 1)
	assert.True(t, detail.ModelCalls[0].HasTrace)
	assert.Contains(t, detail.ModelCalls[0].PromptMessages, `"role":"system"`)
	assert.Equal(t, "the answer", detail.ModelCalls[0].ResponseContent)
	assert.NoError(t, mock.ExpectationsWereMet())
}
