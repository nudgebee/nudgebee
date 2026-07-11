package core

import (
	"testing"

	"nudgebee/llm/common"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetConversationTokenUsage_PerCallTiering locks in the fix for a cost bug
// where GetConversationTokenUsage SUMmed tokens for every call sharing an
// (agent_name, model, message_id) group in SQL and priced the aggregate once.
// CalculateTotalCost tiers pricing on a single call's own prompt size, so two
// calls that are each individually under a model's long-ctx threshold must
// each be billed at the standard rate — even though their summed tokens
// exceed the threshold. Pricing the aggregate instead (the bug) roughly
// doubled real conversations' reported cost (observed $16 vs $30).
func TestGetConversationTokenUsage_PerCallTiering(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

	// 1) pricing (fetchModelPricing → GetConversationCosts) runs first.
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
			"googleai", "gemini-3.1-pro-preview", 2.0, 12.0,
			nil, nil, nil, nil, 200000, 4.0, 18.0, nil, nil,
		))

	// 2) two calls in the SAME (agent_name, model, message_id) group, each
	// with a 150K-token prompt (under the 200K threshold individually) but
	// summing to 300K within the group.
	mock.ExpectQuery("FROM llm_conversation_token_usage").
		WithArgs("sess-1", "acc-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"agentname", "inputtokens", "outputtokens", "cachedinputtokens",
			"cachecreationtokens", "thinkingtokens", "modelname", "modelprovidername",
			"messageid", "conversationid",
		}).
			AddRow("k8s_debug", 150000, 0, 0, 0, 0, "gemini-3.1-pro-preview", "googleai", "msg-1", "conv-1").
			AddRow("k8s_debug", 150000, 0, 0, 0, 0, "gemini-3.1-pro-preview", "googleai", "msg-1", "conv-1"))

	agents, err := dao.GetConversationTokenUsage("sess-1", "acc-1")
	require.NoError(t, err)
	require.Len(t, agents, 1)

	// Correct (per-call): both calls priced at the standard $2/M rate.
	wantCost := (150000.0 / 1_000_000.0 * 2.0) * 2
	assert.InDelta(t, wantCost, agents[0].Cost, 1e-9)

	// The bug's signature: pricing the 300K group aggregate at the long-ctx
	// $4/M rate would double this. Guard against regressing back to that.
	buggyCost := (300000.0 / 1_000_000.0) * 4.0
	assert.NotEqual(t, buggyCost, agents[0].Cost)

	assert.Equal(t, 300000, agents[0].InputTokens)
}

// TestGetConversationTokenUsage_PrefersStoredCostUsd verifies a persisted
// cost_usd on the row is used as-is instead of being recomputed from the live
// pricing table. The pricing table intentionally has no entry for the row's
// model — a fallback-to-live-computation bug would price this row at $0.
func TestGetConversationTokenUsage_PrefersStoredCostUsd(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

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

	mock.ExpectQuery("FROM llm_conversation_token_usage").
		WithArgs("sess-2", "acc-2").
		WillReturnRows(sqlmock.NewRows([]string{
			"agentname", "inputtokens", "outputtokens", "cachedinputtokens",
			"cachecreationtokens", "thinkingtokens", "modelname", "modelprovidername",
			"messageid", "conversationid", "costusd",
		}).
			AddRow("k8s_debug", 1000, 200, 0, 0, 0, "some-unpriced-model", "bedrock", "msg-1", "conv-1", 0.0456))

	agents, err := dao.GetConversationTokenUsage("sess-2", "acc-2")
	require.NoError(t, err)
	require.Len(t, agents, 1)
	assert.InDelta(t, 0.0456, agents[0].Cost, 1e-9)
}

// TestGetConversationCost_KnownModel checks the single-call cost helper used
// at insert time (trackTokenUsage, agent_code2.go) against a priced model.
func TestGetConversationCost_KnownModel(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

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
			"googleai", "gemini-2.5-flash", 0.30, 2.50,
			nil, nil, nil, nil, nil, nil, nil, nil, nil,
		))

	cost, err := dao.GetConversationCost("googleai", "gemini-2.5-flash", 1000, 0, 0, 200, 0)
	require.NoError(t, err)
	want := (1000.0/1_000_000.0)*0.30 + (200.0/1_000_000.0)*2.50
	assert.InDelta(t, want, cost, 1e-9)
}

// TestGetConversationCost_UnknownModelReturnsError checks that a model with
// no llm_model_pricing entry errors rather than silently returning $0 — this
// is what lets insert-time callers distinguish "unpriced" and leave cost_usd
// NULL instead of persisting an incorrect $0.
func TestGetConversationCost_UnknownModelReturnsError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

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

	cost, err := dao.GetConversationCost("bedrock", "unknown-model", 100, 0, 0, 50, 0)
	require.Error(t, err)
	assert.Equal(t, -1.0, cost)
}
