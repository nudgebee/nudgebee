package admin

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm-gateway/common"
	"nudgebee/llm-gateway/ratelimit"
)

func TestFillUsage_SetsWindowedRowsOnly(t *testing.T) {
	now := time.Date(2026, 10, 16, 18, 30, 0, 0, time.UTC)
	rows := []rateLimitRow{
		{ID: "cost", Metric: ratelimit.MetricCost, Period: ratelimit.PeriodDay},
		{ID: "conc", Metric: ratelimit.MetricConcurrent},
		{ID: "unread", Metric: ratelimit.MetricTokens, Period: ratelimit.PeriodHour},
	}
	fillUsage(rows, map[string]float64{"cost": 32.5, "conc": 3}, now)
	require.NotNil(t, rows[0].Used)
	assert.Equal(t, 32.5, *rows[0].Used)
	assert.Equal(t, time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC), *rows[0].ResetsAt)
	assert.Nil(t, rows[1].Used, "concurrent has no window")
	assert.Nil(t, rows[2].Used, "a row the usage read missed stays unset")
}

// quotaUsageTestDB is a throwaway schema with just the columns the usage query reads.
func quotaUsageTestDB(t *testing.T) *common.DatabaseManager {
	t.Helper()
	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("TEST_POSTGRES_DSN")
	}
	if dsn == "" {
		t.Skip("no database configured; export TEST_POSTGRES_DSN to run this test")
	}
	db, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	schema := fmt.Sprintf("gateway_quota_usage_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	_, err = db.Exec(fmt.Sprintf(`CREATE SCHEMA %s; SET search_path TO %s;
		CREATE TABLE llm_gateway_rate_limits (id text PRIMARY KEY, tenant_id text, user_id text DEFAULT '',
			api_key_id text DEFAULT '', model text DEFAULT '', metric text, period text, limit_value numeric,
			enabled bool DEFAULT true, soft_pct smallint, alerted_window timestamptz, alerted_pct smallint);
		CREATE TABLE llm_gateway_usage (created_at timestamptz DEFAULT now(), tenant_id text, user_id text DEFAULT '',
			api_key_id text DEFAULT '', provider text DEFAULT '', model text DEFAULT '', requested_provider text DEFAULT '',
			requested_model text DEFAULT '', total_tokens int DEFAULT 0, cost_usd double precision DEFAULT 0,
			attributes text NOT NULL DEFAULT '{}')`, schema, schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
		_ = db.Close()
	})
	return &common.DatabaseManager{Db: db}
}

func TestUsageByLimit_SumsTheWindowPerScope_DB(t *testing.T) {
	m := quotaUsageTestDB(t)
	exec := func(q string, args ...any) {
		_, err := m.Db.Exec(q, args...)
		require.NoError(t, err)
	}
	exec(`INSERT INTO llm_gateway_rate_limits (id, tenant_id, user_id, api_key_id, metric, period, limit_value) VALUES
		('tenant-cost', 't1', '', '', 'cost', 'month', 50), ('user-req', 't1', 'u1', '', 'requests', 'day', 10),
		('key-tok', 't1', 'u1', 'k1', 'tokens', 'hour', 1000), ('conc', 't1', '', '', 'concurrent', '', 5),
		('other', 't2', '', '', 'cost', 'month', 5)`)
	exec(`INSERT INTO llm_gateway_usage (tenant_id, user_id, api_key_id, total_tokens, cost_usd) VALUES
		('t1', 'u1', 'k1', 100, 1.5), ('t1', 'u1', 'k2', 200, 2.0), ('t1', 'u2', 'k3', 300, 0.5), ('t2', 'x', 'y', 1, 9)`)
	// Refused calls (the gateway writes a row for each) never reached the limiter.
	exec(`INSERT INTO llm_gateway_usage (tenant_id, user_id, api_key_id, attributes) VALUES
		('t1', 'u1', 'k1', '{"derived":{"reject_reason":"model_not_allowed"}}'),
		('t1', 'u1', 'k1', '{"derived":{"reject_reason":"rate_limited"}}')`)
	// A label named like the field is not a refusal.
	exec(`INSERT INTO llm_gateway_usage (tenant_id, user_id, api_key_id, total_tokens, cost_usd, attributes) VALUES
		('t1', 'u1', 'k1', 0, 0, '{"derived":{"labels":{"reject_reason":"x"}}}')`)
	// Before every window: last month, so it counts toward none of them.
	exec(`INSERT INTO llm_gateway_usage (created_at, tenant_id, user_id, api_key_id, total_tokens, cost_usd)
		VALUES (now() - interval '40 days', 't1', 'u1', 'k1', 5000, 100)`)

	used, err := usageByLimit(m, "t1")
	require.NoError(t, err)
	assert.InDelta(t, 4.0, used["tenant-cost"], 1e-9)
	assert.Equal(t, 3.0, used["user-req"], "two served calls and the labelled one; refusals don't count")
	assert.Equal(t, 100.0, used["key-tok"])
	assert.NotContains(t, used, "conc")
	assert.NotContains(t, used, "other")
}

// The SQL model test must agree with ratelimit.ModelMatches, which enforcement uses.
func TestUsageByLimit_ModelMatchMirrorsEnforcement_DB(t *testing.T) {
	m := quotaUsageTestDB(t)
	limitModels := []string{"claude-haiku-4-5", "anthropic/claude-haiku-4-5", "openai/claude-haiku-4-5", "Qwen/Qwen3-8B", "GPT-4o"}
	calls := []struct{ provider, model, reqProvider, reqModel string }{
		{"anthropic", "claude-haiku-4-5", "", "nb-fast"},
		{"anthropic", "claude-sonnet-4-6", "anthropic", "claude-haiku-4-5"},
		{"vllm", "Qwen/Qwen3-8B", "", ""},
		{"openai", "gpt-4o", "", ""},
		{"bedrock", "other", "", ""},
	}
	for i, lm := range limitModels {
		_, err := m.Db.Exec(`INSERT INTO llm_gateway_rate_limits (id, tenant_id, model, metric, period, limit_value)
			VALUES ($1, 't1', $2, 'requests', 'day', 10)`, fmt.Sprint(i), lm)
		require.NoError(t, err)
	}
	for _, c := range calls {
		_, err := m.Db.Exec(`INSERT INTO llm_gateway_usage (tenant_id, provider, model, requested_provider, requested_model)
			VALUES ('t1', $1, $2, $3, $4)`, c.provider, c.model, c.reqProvider, c.reqModel)
		require.NoError(t, err)
	}
	used, err := usageByLimit(m, "t1")
	require.NoError(t, err)
	for i, lm := range limitModels {
		want := 0
		for _, c := range calls {
			if ratelimit.ModelMatches(lm, c.provider, c.model) || ratelimit.ModelMatches(lm, c.reqProvider, c.reqModel) {
				want++
			}
		}
		assert.Equal(t, float64(want), used[fmt.Sprint(i)], "limit model %q", lm)
	}
}

// Editing a row's limit or warn level clears its alert state; editing anything else keeps it.
func TestUpdateRateLimit_ResetsAlertStateOnLimitChange_DB(t *testing.T) {
	m := quotaUsageTestDB(t)
	pct := func(v int) *int { return &v }
	_, err := m.Db.Exec(`INSERT INTO llm_gateway_rate_limits (id, tenant_id, metric, period, limit_value, soft_pct, alerted_window, alerted_pct)
		VALUES ('q1', 't1', 'cost', 'day', 50, 80, now(), 80)`)
	require.NoError(t, err)
	alerted := func() (pct *int) {
		require.NoError(t, m.Db.Get(&pct, `SELECT alerted_pct FROM llm_gateway_rate_limits WHERE id = 'q1'`))
		return pct
	}
	row := rateLimitRow{ID: "q1", Metric: ratelimit.MetricCost, Period: ratelimit.PeriodDay, Value: 50, SoftPct: pct(80), Enabled: false}
	_, err = updateRateLimit(m, "t1", row)
	require.NoError(t, err)
	assert.Equal(t, pct(80), alerted(), "toggling enabled keeps the alert state")

	row.Value = 100
	_, err = updateRateLimit(m, "t1", row)
	require.NoError(t, err)
	assert.Nil(t, alerted(), "a new limit may alert again")
}
