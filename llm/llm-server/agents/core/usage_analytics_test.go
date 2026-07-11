package core

import (
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
	for _, k := range []string{"cost", "start_time", "duration", "llm_calls", "tokens"} {
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
			"wall_clock_seconds", "model_latency_seconds", "cost_usd",
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

// TestUsageDimensions_Whitelist documents the exact set of groupable
// dimensions; new ones must be added deliberately, never derived from input.
func TestUsageDimensions_Whitelist(t *testing.T) {
	for _, dim := range []string{"model", "provider", "source", "agent", "status", "user", "account"} {
		assert.True(t, usageDimensions[dim], "expected %s to be groupable", dim)
	}
	assert.False(t, usageDimensions["password"])
	assert.False(t, usageDimensions["; DROP TABLE"])
}
