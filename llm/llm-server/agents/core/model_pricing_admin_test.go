package core

import (
	"testing"

	"nudgebee/llm/common"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func f(v float64) *float64 { return &v }
func i64(v int64) *int64   { return &v }

// The long-context tier only fires when the threshold AND the long input rate
// are both set (modelPricing.useLongCtx). A half-configured tier is therefore
// worse than none: it reads as tiered in the UI but bills every long prompt at
// the short rate, under-reporting spend on exactly the calls that cost most.
func TestModelPriceInput_LongContextTierIsAllOrNothing(t *testing.T) {
	base := ModelPriceInput{ModelName: "gemini-2.5-pro", ProviderName: "googleai", InputPerM: 1.25, OutputPerM: 10}

	t.Run("flat pricing is valid", func(t *testing.T) {
		require.NoError(t, base.Validate())
	})

	t.Run("complete tier is valid", func(t *testing.T) {
		in := base
		in.ContextThresholdTokens = i64(200000)
		in.InputPerMLongCtx = f(2.5)
		in.OutputPerMLongCtx = f(15)
		require.NoError(t, in.Validate())
	})

	t.Run("threshold without rates is rejected", func(t *testing.T) {
		in := base
		in.ContextThresholdTokens = i64(200000)
		err := in.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "long-context")
	})

	t.Run("rates without a threshold are rejected", func(t *testing.T) {
		in := base
		in.InputPerMLongCtx = f(2.5)
		in.OutputPerMLongCtx = f(15)
		require.Error(t, in.Validate())
	})

	t.Run("only one long rate is rejected", func(t *testing.T) {
		in := base
		in.ContextThresholdTokens = i64(200000)
		in.InputPerMLongCtx = f(2.5)
		err := in.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "input and an output")
	})

	t.Run("non-positive threshold is rejected", func(t *testing.T) {
		in := base
		in.ContextThresholdTokens = i64(0)
		in.InputPerMLongCtx = f(2.5)
		in.OutputPerMLongCtx = f(15)
		require.Error(t, in.Validate())
	})
}

// Zero is a legitimate rate — a self-hosted model can genuinely be free — but a
// negative one would subtract from the tenant's spend.
func TestModelPriceInput_RejectsNegativeButAllowsZero(t *testing.T) {
	base := ModelPriceInput{ModelName: "llama-3-70b", ProviderName: ProviderCustom}

	require.NoError(t, base.Validate(), "a free self-hosted model is valid")

	neg := base
	neg.InputPerM = -0.01
	require.Error(t, neg.Validate())

	negLong := base
	negLong.ContextThresholdTokens = i64(100000)
	negLong.InputPerMLongCtx = f(-1)
	negLong.OutputPerMLongCtx = f(1)
	require.Error(t, negLong.Validate())
}

func TestModelPriceInput_RequiresModelAndProvider(t *testing.T) {
	require.Error(t, ModelPriceInput{ProviderName: "openai"}.Validate())
	require.Error(t, ModelPriceInput{ModelName: "gpt-4o"}.Validate())
	require.Error(t, ModelPriceInput{ModelName: "  ", ProviderName: "openai"}.Validate())
}

// A tenant is mandatory: writing with an empty one would create a built-in row
// visible to every tenant, which is the collision V843 exists to prevent.
func TestUpsertModelPricing_RefusesEmptyTenant(t *testing.T) {
	err := UpsertModelPricing(nil, "", "user-1", []ModelPriceInput{{ModelName: "m", ProviderName: "p"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database manager unavailable")
}

// A tenant override REPLACES the built-in rate — it does not sit beside it.
// Without the DISTINCT ON, `(tenant_id IS NULL OR tenant_id = $1)` returns both
// rows for an overridden model, so the tab lists the model twice: once at the
// rate the tenant is billed and once at the rate it superseded.
func TestListModelPricing_CollapsesOverrideOntoBuiltIn(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	cols := []string{
		"model_name", "provider_name",
		"cost_per_million_input_tokens", "cost_per_million_output_tokens",
		"cost_per_million_cached_input_tokens", "cost_per_million_cache_creation_tokens",
		"context_threshold_tokens",
		"cost_per_million_input_tokens_long_ctx", "cost_per_million_output_tokens_long_ctx",
		"is_built_in", "pricing_updated_at", "has_built_in",
	}
	// The dedup happens in SQL, so pin the two clauses that produce it: without
	// either one Postgres hands back both rows.
	mock.ExpectQuery(`DISTINCT ON \(provider_name, model_name\)`).
		WithArgs("tenant-1").
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow("gpt-4o", "openai", 1.8, 7.2, nil, nil, nil, nil, nil, false, nil, true))

	got, err := ListModelPricing(&common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, "tenant-1")
	require.NoError(t, err)
	require.Len(t, got, 1, "an overridden model must appear once")
	assert.Equal(t, 1.8, got[0].InputPerM, "at the tenant rate, not the built-in")
	assert.False(t, got[0].IsBuiltIn)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Tenant preference must be expressed as NULLS LAST: with NULLS FIRST the query
// still returns one row per model, but it would be the built-in — silently
// showing rates the tenant is not billed at.
func TestListModelPricing_PrefersTenantRowOverBuiltIn(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`ORDER BY provider_name, model_name, tenant_id NULLS LAST`).
		WithArgs("tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"model_name", "provider_name",
			"cost_per_million_input_tokens", "cost_per_million_output_tokens",
			"cost_per_million_cached_input_tokens", "cost_per_million_cache_creation_tokens",
			"context_threshold_tokens",
			"cost_per_million_input_tokens_long_ctx", "cost_per_million_output_tokens_long_ctx",
			"is_built_in", "pricing_updated_at", "has_built_in",
		}))

	_, err = ListModelPricing(&common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, "tenant-1")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// With no tenant resolved the caller must see built-ins only — never another
// tenant's negotiated rates.
func TestListModelPricing_NoTenantSeesBuiltInsOnly(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`WHERE tenant_id IS NULL`).
		WithArgs(). // no tenant arg is bound
		WillReturnRows(sqlmock.NewRows([]string{
			"model_name", "provider_name",
			"cost_per_million_input_tokens", "cost_per_million_output_tokens",
			"cost_per_million_cached_input_tokens", "cost_per_million_cache_creation_tokens",
			"context_threshold_tokens",
			"cost_per_million_input_tokens_long_ctx", "cost_per_million_output_tokens_long_ctx",
			"is_built_in", "pricing_updated_at", "has_built_in",
		}))

	_, err = ListModelPricing(&common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, "")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Upsert stores trimmed values, so Delete has to trim too. Without it a caller
// passing " openai " clears validation and then matches nothing, and the only
// signal is a removed count of 0 that reads like "already gone".
func TestDeleteModelPricing_TrimsToMatchWhatUpsertStored(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec("DELETE FROM llm_model_pricing").
		WithArgs("tenant-1", "openai", "gpt-4o").
		WillReturnResult(sqlmock.NewResult(0, 1))

	removed, err := DeleteModelPricing(&common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, "tenant-1", "  openai ", " gpt-4o  ")
	require.NoError(t, err)
	assert.Equal(t, int64(1), removed)
	require.NoError(t, mock.ExpectationsWereMet())
}
