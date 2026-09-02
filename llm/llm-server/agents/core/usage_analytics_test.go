package core

import (
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUsageMetricsFilter_BuildWhere_BaseOnly verifies the always-present
// account/date clauses and that no optional args leak in when filters are empty.
func TestUsageMetricsFilter_BuildWhere_BaseOnly(t *testing.T) {
	f := UsageMetricsFilter{
		AccountIDs: []string{"acc-1"},
		StartDate:  time.Now(),
		EndDate:    time.Now(),
	}
	where, args := f.buildWhere()

	// With no explicit source filter, the optimizer's own runs are excluded by default.
	assert.Equal(t, "c.account_id = ANY($1::uuid[]) AND c.created_at >= $2 AND c.created_at <= $3 AND c.source IS DISTINCT FROM 'Optimize'", where)
	assert.Len(t, args, 3) // the exclusion is a literal — no extra bound arg
}

// TestUsageMetricsFilter_BuildWhere_OptionalFilters checks that each optional
// dimension appends one ANDed clause with the next sequential placeholder.
func TestUsageMetricsFilter_BuildWhere_OptionalFilters(t *testing.T) {
	f := UsageMetricsFilter{
		AccountIDs: []string{"acc-1"},
		StartDate:  time.Now(),
		EndDate:    time.Now(),
		Sources:    []string{"Investigation"},
		Models:     []string{"claude-opus"},
		UserID:     "user-9",
	}
	where, args := f.buildWhere()

	assert.Contains(t, where, "c.source = ANY($4)")
	assert.Contains(t, where, "t.llm_model = ANY($5)")
	assert.Contains(t, where, "t.user_id = $6")
	// base(3) + sources + models + user
	assert.Len(t, args, 6)
}

// TestCacheHitPct covers the zero-denominator guard and a normal ratio.
func TestCacheHitPct(t *testing.T) {
	assert.Equal(t, 0.0, cacheHitPct(0, 0))
	assert.Equal(t, 0.0, cacheHitPct(100, 0))
	assert.InDelta(t, 25.0, cacheHitPct(250, 1000), 1e-9)
}

// TestHandleUsageMetricsApi_InvalidStartDate rejects a malformed start_date
// before any DB access.
func TestHandleUsageMetricsApi_InvalidStartDate(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	_, err := HandleUsageMetricsApi(ctx, UsageMetricsRequest{
		StartDate: "nope",
		EndDate:   "2026-05-01T00:00:00Z",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "start_date")
}

// TestHandleUsageMetricsApi_InvalidGroupBy rejects an unknown dimension so an
// untrusted value never reaches the SQL dispatch.
func TestHandleUsageMetricsApi_InvalidGroupBy(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	_, err := HandleUsageMetricsApi(ctx, UsageMetricsRequest{
		StartDate: "2026-05-01T00:00:00Z",
		EndDate:   "2026-06-01T00:00:00Z",
		GroupBy:   []string{"; DROP TABLE"},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "group_by")
}

// TestHandleUsageMetricsApi_InvalidEndDate rejects a malformed end_date before
// any DB access.
func TestHandleUsageMetricsApi_InvalidEndDate(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	_, err := HandleUsageMetricsApi(ctx, UsageMetricsRequest{
		StartDate: "2026-05-01T00:00:00Z",
		EndDate:   "not-a-date",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "end_date")
}

// TestUsageMetricsFilter_BuildScopeWhere verifies the coarse account+date scope
// used by the filters endpoint carries exactly three args and no dimension leaks.
func TestUsageMetricsFilter_BuildScopeWhere(t *testing.T) {
	f := UsageMetricsFilter{
		AccountIDs: []string{"acc-1"},
		StartDate:  time.Now(),
		EndDate:    time.Now(),
		Sources:    []string{"Investigation"}, // must NOT appear in scope
		Models:     []string{"claude-opus"},
	}
	where, args := f.buildScopeWhere()

	assert.Equal(t, "c.account_id = ANY($1::uuid[]) AND c.created_at >= $2 AND c.created_at <= $3", where)
	assert.Len(t, args, 3)
	assert.NotContains(t, where, "source")
	assert.NotContains(t, where, "llm_model")
}

// TestHandleUsageFiltersApi_InvalidStartDate rejects a bad start_date up front.
func TestHandleUsageFiltersApi_InvalidStartDate(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	_, err := HandleUsageFiltersApi(ctx, UsageFiltersRequest{
		StartDate: "bad",
		EndDate:   "2026-06-01T00:00:00Z",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "start_date")
}

// TestConversationSortColumns_Whitelist documents the allowed ORDER BY targets
// and asserts they are single trusted tokens (no injectable expressions).
func TestConversationSortColumns_Whitelist(t *testing.T) {
	for _, k := range []string{"cost", "start_time", "duration", "llm_calls", "tokens", "latency"} {
		col, ok := conversationSortColumns[k]
		assert.True(t, ok, "expected %s sortable", k)
		assert.NotEmpty(t, col)
		assert.False(t, strings.ContainsAny(col, "; "))
	}
	_, ok := conversationSortColumns["1=1"]
	assert.False(t, ok)
}

// TestHandleListConversationCostsApi_InvalidStartDate rejects a bad start_date.
func TestHandleListConversationCostsApi_InvalidStartDate(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	_, err := HandleListConversationCostsApi(ctx, ListConversationCostsRequest{
		StartDate: "bad",
		EndDate:   "2026-06-01T00:00:00Z",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "start_date")
}

// TestHandleConversationTreeApi_MissingFields rejects calls without the
// required conversation_id / account_id before any DB access.
func TestHandleConversationTreeApi_MissingFields(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	_, err := HandleConversationTreeApi(ctx, ConversationTreeRequest{AccountId: "acc-1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "conversation_id")
}

// TestListConversationCosts_ModelBreakdown verifies that each row is populated
// with a per-model breakdown via the single extra query, and that the breakdown's
// per-model costs sum (within float tolerance) to the row's cost_usd.
func TestListConversationCosts_ModelBreakdown(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// runUsageTotals now runs concurrently with the page query (see
	// ListConversationCosts), so the two arrive in non-deterministic order —
	// match expectations by content, not sequence.
	mock.MatchExpectationsInOrder(false)

	dao := &ConversationDao{
		dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")},
	}

	convID := "11111111-1111-1111-1111-111111111111"
	now := time.Now()

	// 1) filter-wide KPI totals (runUsageTotals).
	mock.ExpectQuery("total_tasks").
		WillReturnRows(sqlmock.NewRows([]string{
			"cost_usd", "cache_savings_usd", "total_tasks",
			"input_tokens", "output_tokens", "cached_input_tokens", "requests",
		}).AddRow(0.30, 0.0, 1, 1500, 600, 0, 3))

	// 2) the page of conversation rows. Row cost_usd = 0.30, llm_call_count = 3.
	mock.ExpectQuery("models_used").
		WillReturnRows(sqlmock.NewRows([]string{
			"conversation_id", "session_id", "source", "status", "title",
			"user_id", "account_id", "created_at", "updated_at",
			"wall_clock_seconds", "total_model_time_seconds", "cost_usd",
			"input_tokens", "output_tokens", "cached_input_tokens",
			"message_count", "agent_count", "llm_call_count", "models_used",
		}).AddRow(
			convID, "sess-1", "Investigation", "completed", "A task",
			"user-1", "acc-1", now, now,
			12.0, 4.5, 0.30,
			1500, 600, 0,
			2, 1, 3, "{claude-opus,claude-haiku}",
		))

	// 3) the per-model breakdown query for this page's conversation ids.
	// Two models whose calls (2+1=3) and cost (0.20+0.10=0.30) reconcile.
	mock.ExpectQuery("GROUP BY c.id, t.llm_model").
		WillReturnRows(sqlmock.NewRows([]string{
			"conversation_id", "model", "provider", "calls",
			"cost_usd", "input_tokens", "output_tokens",
		}).
			AddRow(convID, "claude-opus", "bedrock", 2, 0.20, 1000, 400).
			AddRow(convID, "claude-haiku", "bedrock", 1, 0.10, 500, 200))

	filter := UsageMetricsFilter{
		AccountIDs: []string{"acc-1"},
		StartDate:  now.Add(-24 * time.Hour),
		EndDate:    now,
	}

	out, err := dao.ListConversationCosts(filter, "cost", "desc", 50, 0)
	require.NoError(t, err)
	require.Len(t, out.Rows, 1)

	row := out.Rows[0]
	require.NotEmpty(t, row.ModelBreakdown, "model_breakdown must be populated")
	assert.Len(t, row.ModelBreakdown, 2)

	var sumCost float64
	var sumCalls int64
	for _, m := range row.ModelBreakdown {
		sumCost += m.CostUsd
		sumCalls += m.Calls
	}
	assert.InDelta(t, row.CostUsd, sumCost, 1e-9, "breakdown cost must reconcile with row cost_usd")
	assert.Equal(t, int64(row.LLMCallCount), sumCalls, "breakdown calls must sum to llm_call_count")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestUserBreakdown_IncludesSystemUser is a regression test for #35804: the
// per-user breakdown used to drop every row for the synthetic system user via
// "AND t.user_id <> systemUserID", making Users-tab totals fall short of
// Overview's by however much automation usage the tenant had. It must now
// surface that usage, labeled the same way the audit log already labels the
// same sentinel (app/src/components/audits/index.jsx), instead of excluding it.
func TestUserBreakdown_IncludesSystemUser(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	now := time.Now()

	// Matching this regex is only possible against the new CASE-mapped query —
	// the old "AND t.user_id <> '...'" exclusion produces different SQL, so this
	// also guards against the exclusion silently coming back.
	mock.ExpectQuery(`CASE WHEN t\.user_id = '00000000-0000-0000-0000-000000000000' THEN 'SYSTEM'`).
		WillReturnRows(sqlmock.NewRows([]string{
			"group_key", "cost_usd", "input_tokens", "output_tokens", "cached_input_tokens",
			"requests", "conversations", "avg_latency_seconds",
		}).
			AddRow("SYSTEM", 1.50, 5_590_000, 162_800, 0, 40, 348, 3.2).
			AddRow("alice", 0.71, 2_610_000, 76_000, 0, 30, 30, 1.1))

	filter := UsageMetricsFilter{AccountIDs: []string{"acc-1"}, StartDate: now.Add(-24 * time.Hour), EndDate: now}
	where, args := filter.buildWhere()

	rows, err := dao.breakdownForDimension("user", where, args, 100)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	require.Len(t, rows, 2)
	assert.Equal(t, "SYSTEM", rows[0].Key)
	assert.InDelta(t, 1.50, rows[0].CostUsd, 0.001)
}

// TestUserBreakdown_NullUserIDFallsBackToUnknown is a regression test for the
// gap the #35804 fix (above) opened: dropping "AND t.user_id <> systemUserID"
// means rows with a NULL t.user_id (background code-analysis usage inserted
// via buildCodeAnalysisUsageRecord, which has no systemUserID fallback the
// way GenerateAndTrackLLMContent does) are no longer excluded upstream. The
// bare CASE would then emit a NULL group_key and fail the scan into
// GroupKey's non-nullable string. The outer COALESCE(..., 'unknown') must
// guard it, matching how every other dimension in this file already handles
// a nullable group key.
func TestUserBreakdown_NullUserIDFallsBackToUnknown(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	now := time.Now()

	// Asserts the outer COALESCE guard is present in the generated SQL, then
	// simulates what Postgres would return for a NULL-user_id row once that
	// guard is in place: "unknown", not NULL — so the scan into GroupKey
	// (non-nullable string) succeeds instead of erroring.
	mock.ExpectQuery(`COALESCE\(CASE WHEN t\.user_id = '00000000-0000-0000-0000-000000000000' THEN 'SYSTEM'.*END, 'unknown'\)`).
		WillReturnRows(sqlmock.NewRows([]string{
			"group_key", "cost_usd", "input_tokens", "output_tokens", "cached_input_tokens",
			"requests", "conversations", "avg_latency_seconds",
		}).
			AddRow("unknown", 0.05, 12_000, 400, 0, 2, 2, 0.4))

	filter := UsageMetricsFilter{AccountIDs: []string{"acc-1"}, StartDate: now.Add(-24 * time.Hour), EndDate: now}
	where, args := filter.buildWhere()

	rows, err := dao.breakdownForDimension("user", where, args, 100)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	require.Len(t, rows, 1)
	assert.Equal(t, "unknown", rows[0].Key)
}

// TestUsageDimensions_Whitelist documents the exact set of groupable
// dimensions; new ones must be added deliberately, never derived from input.
func TestUsageDimensions_Whitelist(t *testing.T) {
	for _, dim := range []string{"model", "provider", "source", "agent", "status", "user", "account"} {
		assert.True(t, usageDimensions[dim], "expected %s to be groupable", dim)
	}
	assert.False(t, usageDimensions["password"])
	assert.False(t, usageDimensions["; DROP TABLE"])
}

// argContains matches a bound pq array arg by substring — enough to tell the
// readable-account list apart from the selected one without re-encoding pq's
// array literal syntax in the test.
type argContains struct{ want string }

func (a argContains) Match(v driver.Value) bool {
	s, ok := v.(string)
	return ok && strings.Contains(s, a.want)
}

// TestGetUsageFilters_AccountsScopedToWindowNotSelection pins the two rules the
// account dropdown depends on: it is offered from every READABLE account (not
// just the selected one, which would collapse it to a single option), narrowed
// to accounts with usage in the window, and it carries cloud_provider so the UI
// can group by provider and show its logo.
func TestGetUsageFilters_AccountsScopedToWindowNotSelection(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	dao := &ConversationDao{
		dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")},
	}

	mock.ExpectQuery("array_agg").
		WillReturnRows(sqlmock.NewRows([]string{"sources", "models", "providers", "agents", "statuses"}).
			AddRow("{Investigation}", "{claude-opus}", "{bedrock}", "{aws}", "{success}"))
	mock.ExpectQuery("LEFT JOIN users").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))
	// $1 = readable accounts (must include acc-2, which is NOT the selection),
	// $4 = the selection kept visible even without data in the window.
	mock.ExpectQuery("cloud_provider").
		WithArgs(argContains{"acc-2"}, sqlmock.AnyArg(), sqlmock.AnyArg(), argContains{"acc-1"}).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "cloud_provider"}).
			AddRow("acc-1", "prod-aws", "AWS").
			AddRow("acc-2", "prod-gcp", "GCP"))

	now := time.Now()
	out, err := dao.GetUsageFilters(UsageMetricsFilter{
		AccountIDs: []string{"acc-1"}, // selected scope — dimensions/users use this
		StartDate:  now.Add(-24 * time.Hour),
		EndDate:    now,
	}, []string{"acc-1", "acc-2"}, []string{"acc-1"})
	require.NoError(t, err)

	require.Len(t, out.Accounts, 2, "dropdown must offer every readable account, not just the selected one")
	assert.Equal(t, "AWS", out.Accounts[0].CloudProvider)
	assert.Equal(t, "GCP", out.Accounts[1].CloudProvider)
	assert.NoError(t, mock.ExpectationsWereMet())
}
