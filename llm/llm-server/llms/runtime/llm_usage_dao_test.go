package runtime

import (
	"nudgebee/llm/common"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetConversationCost_KnownModel checks the single-call cost helper used
// at insert time (trackTokenUsage, agent_code2.go) against a priced model.
func TestGetConversationCost_KnownModel(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	dao := &usageDAO{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

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

	cost, err := dao.GetConversationCost("googleai", "gemini-2.5-flash", 1000, 0, 0, 200, 0, "")
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

	dao := &usageDAO{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

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

	cost, err := dao.GetConversationCost("bedrock", "unknown-model", 100, 0, 0, 50, 0, "")
	require.Error(t, err)
	assert.Equal(t, -1.0, cost)
}

// pricingRows is the column set GetConversationCosts scans, so a test only has
// to state the values it cares about.
func pricingRows(provider, model string, in, out float64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"provider_name", "model_name",
		"cost_per_million_input_tokens", "cost_per_million_output_tokens",
		"cache_cost_per_million_tokens_per_hour", "cost_per_million_cached_input_tokens",
		"cost_per_million_cache_creation_tokens", "cost_per_million_cached_storage_per_hour",
		"context_threshold_tokens", "cost_per_million_input_tokens_long_ctx",
		"cost_per_million_output_tokens_long_ctx", "cost_per_million_cached_input_tokens_long_ctx",
		"cost_per_million_cache_creation_tokens_long_ctx",
	}).AddRow(provider, model, in, out, nil, nil, nil, nil, nil, nil, nil, nil, nil)
}

// A tenant's own price must win over the built-in one. The preference is
// expressed in SQL (DISTINCT ON + tenant_id NULLS LAST) rather than by merging
// two result sets, because an OR-join would return both rows and double-count.
func TestGetConversationCosts_PrefersTenantRow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &usageDAO{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

	// The tenant's rate is what the DB returns once DISTINCT ON has picked.
	mock.ExpectQuery("DISTINCT ON").WillReturnRows(pricingRows("openai", "gpt-4o", 1.11, 2.22))

	got, err := dao.GetConversationCosts(nil, "tenant-a")
	require.NoError(t, err)
	require.Contains(t, got, "openai:gpt-4o")
	assert.InDelta(t, 1.11, got["openai:gpt-4o"].CostPerMillionInput, 1e-9)
	require.NoError(t, mock.ExpectationsWereMet())
}

// An empty tenant means built-in only — the query must not offer to match a
// tenant row at all, so no caller without tenant context can pick one up.
func TestGetConversationCosts_NoTenantReadsBuiltInOnly(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &usageDAO{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

	// sqlmock's default matcher is a regexp over the SQL, so this asserts the
	// built-in-only predicate is present rather than the tenant one.
	mock.ExpectQuery("tenant_id IS NULL").WillReturnRows(pricingRows("openai", "gpt-4o", 2.50, 10.00))

	got, err := dao.GetConversationCosts(nil, "")
	require.NoError(t, err)
	assert.InDelta(t, 2.50, got["openai:gpt-4o"].CostPerMillionInput, 1e-9)
	require.NoError(t, mock.ExpectationsWereMet())
}

// The insert-time helper is the one that matters most: aggregates prefer the
// cost_usd it stores, so a tenant's rate has to reach it.
func TestGetConversationCost_UsesTenantPricing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &usageDAO{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

	mock.ExpectQuery("FROM llm_model_pricing").WillReturnRows(pricingRows("custom", "llama-3-70b", 0.40, 0.40))

	cost, err := dao.GetConversationCost("custom", "llama-3-70b", 1_000_000, 0, 0, 1_000_000, 0, "tenant-a")
	require.NoError(t, err)
	assert.InDelta(t, 0.80, cost, 1e-9)
}
