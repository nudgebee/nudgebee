package core

import (
	"testing"
	"time"

	"nudgebee/llm/common"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetConversationTree_PrefersStoredCostUsd mirrors
// TestGetConversationTokenUsage_PrefersStoredCostUsd: a model-call leaf with a
// persisted cost_usd must use that value as-is, not recompute it from the live
// pricing table — the two cost surfaces must reconcile (see conversation_tree.go
// package doc). The pricing table intentionally has no entry for the row's
// model, so a fallback-to-live-computation bug would price it at $0.
func TestGetConversationTree_PrefersStoredCostUsd(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

	now := time.Now()

	mock.ExpectQuery("FROM llm_conversations").
		WithArgs("sess-1", "acc-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"conversation_id", "session_id", "source", "status", "title", "started_at", "ended_at",
		}).AddRow("conv-1", "sess-1", "web", "COMPLETED", "title", now, now))

	mock.ExpectQuery("FROM llm_conversation_token_usage").
		WithArgs("sess-1", "acc-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "agent_id", "message_id", "llm_model", "llm_provider",
			"input_tokens", "output_tokens", "cached_input_tokens", "cache_creation_tokens",
			"thinking_tokens", "latency_seconds", "ttft_ms", "retry_attempt", "is_cache_hit",
			"request_status", "stop_reason", "error_message", "created_at", "cost_usd",
		}).AddRow(
			"mc-1", "agent-1", "msg-1", "some-unpriced-model", "bedrock",
			1000, 200, 0, 0,
			0, 1.5, 0, 0, false,
			"success", "", "", now, 0.0456,
		))

	mock.ExpectQuery("FROM llm_model_pricing").
		WillReturnRows(sqlmock.NewRows([]string{
			"provider_name", "model_name",
			"cost_per_million_input_tokens", "cost_per_million_output_tokens",
			"cache_cost_per_million_tokens_per_hour", "cost_per_million_cached_input_tokens",
			"cost_per_million_cache_creation_tokens", "cost_per_million_cached_storage_per_hour",
			"context_threshold_tokens", "cost_per_million_input_tokens_long_ctx",
			"cost_per_million_output_tokens_long_ctx", "cost_per_million_cached_input_tokens_long_ctx",
			"cost_per_million_cache_creation_tokens_long_ctx",
		}))

	mock.ExpectQuery("FROM llm_conversation_messages").
		WithArgs("sess-1", "acc-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "parent_agent_id", "role", "message_type", "agent_name", "status", "created_at", "updated_at",
		}))

	mock.ExpectQuery("FROM llm_conversation_agent").
		WithArgs("sess-1", "acc-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "message_id", "parent_agent_id", "agent_name", "status", "created_at", "updated_at", "duration_seconds",
		}))

	mock.ExpectQuery("FROM llm_conversation_tool_calls").
		WithArgs("sess-1", "acc-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "agent_id", "message_id", "tool_name", "tool_id", "tool_type", "child_agent_id",
			"status", "created_at", "updated_at", "duration_seconds",
		}))

	tree, err := dao.GetConversationTree("sess-1", "acc-1")
	require.NoError(t, err)
	assert.InDelta(t, 0.0456, tree.Conversation.TotalCostUsd, 1e-9)
}
