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

// TestAgentSortColumns_Whitelist documents the allowed sort targets.
func TestAgentSortColumns_Whitelist(t *testing.T) {
	for _, k := range []string{"cost", "latency", "errors"} {
		col, ok := agentSortColumns[k]
		assert.True(t, ok, "expected %s sortable", k)
		assert.NotEmpty(t, col)
	}
	_, ok := agentSortColumns["; DROP TABLE"]
	assert.False(t, ok)
}

// TestListAgentCalls_PerInvocationGrain verifies the leaderboard returns one row
// per agent INVOCATION (not grouped by name): the same agent name recurs, each row
// linked to its own conversation. Also checks the limit arg and unknown sort → cost.
func TestListAgentCalls_PerInvocationGrain(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	now := time.Now()

	cols := []string{
		"agent_id", "agent_name", "conversation_id", "conversation_title", "account_id",
		"status", "started_at", "cost_usd", "latency_sum_seconds", "latency_max_seconds",
		"latency_median_seconds", "llm_call_count", "error_count", "input_tokens", "output_tokens", "models_used",
	}
	// 1) agent-wise latency profile (graph) query runs first.
	mock.ExpectQuery("GROUP BY agent_name").WillReturnRows(
		sqlmock.NewRows([]string{"agent_name", "p50_seconds", "p90_seconds", "p99_seconds", "invocations"}).
			AddRow("k8s_debug", 4.0, 12.0, 28.0, 2))

	// 2) Two invocations of the SAME agent name "k8s_debug", in two different conversations.
	mock.ExpectQuery("GROUP BY a.id, c.id").
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow("ag-1", "k8s_debug", "sess-A", "Investigate pods", "acc-1", "success",
				now, 2.50, 30.0, 12.0, 4.0, 40, 0, 500000, 1200, "{gemini-3.1-pro-preview}").
			AddRow("ag-2", "k8s_debug", "sess-B", "Investigate nodes", "acc-1", "fail",
				now, 1.10, 18.0, 9.0, 3.0, 22, 3, 200000, 600, "{gemini-3.1-pro-preview,gemini-2.5-flash}"))

	filter := UsageMetricsFilter{
		AccountIDs: []string{"acc-1"},
		StartDate:  now.Add(-24 * time.Hour),
		EndDate:    now,
	}

	out, err := dao.ListAgentCalls(filter, "bogus-sort", 100, 0) // unknown sort → cost; no latency filter
	require.NoError(t, err)
	assert.Equal(t, "cost", out.SortBy)
	require.Len(t, out.Rows, 2)

	// same name, distinct invocations, distinct conversations — the whole point
	assert.Equal(t, out.Rows[0].AgentName, out.Rows[1].AgentName)
	assert.NotEqual(t, out.Rows[0].AgentID, out.Rows[1].AgentID)
	assert.NotEqual(t, out.Rows[0].ConversationID, out.Rows[1].ConversationID)

	// latency baseline trio is carried through
	assert.Equal(t, 30.0, out.Rows[0].LatencySumSeconds)
	assert.Equal(t, 12.0, out.Rows[0].LatencyMaxSeconds)
	assert.Equal(t, 4.0, out.Rows[0].LatencyMedianSeconds)
	// orchestration failure surfaced via status even where error_count could be 0
	assert.Equal(t, "fail", out.Rows[1].Status)
	assert.Equal(t, 3, out.Rows[1].ErrorCount)
	assert.Equal(t, []string{"gemini-3.1-pro-preview", "gemini-2.5-flash"}, out.Rows[1].ModelsUsed)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestListAgentCalls_LatencyPercentile runs the two-query path: a pXX threshold
// over the 24h baseline, then the report-range leaderboard filtered by HAVING.
// Verifies the resolved percentile + threshold are echoed and the HAVING clause
// is emitted.
func TestListAgentCalls_LatencyPercentile(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	now := time.Now()

	// 1) agent-wise latency profile (graph) query.
	mock.ExpectQuery("GROUP BY agent_name").WillReturnRows(
		sqlmock.NewRows([]string{"agent_name", "p50_seconds", "p90_seconds", "p99_seconds", "invocations"}).
			AddRow("k8s_debug", 4.0, 12.0, 28.0, 5))

	// 2) threshold query (pXX of per-invocation latency over the 24h baseline).
	mock.ExpectQuery("ORDER BY inv_latency").WillReturnRows(sqlmock.NewRows([]string{"percentile_cont"}).AddRow(3.2))

	// 3) leaderboard query — must carry the HAVING latency floor.
	cols := []string{
		"agent_id", "agent_name", "conversation_id", "conversation_title", "account_id",
		"status", "started_at", "cost_usd", "latency_sum_seconds", "latency_max_seconds",
		"latency_median_seconds", "llm_call_count", "error_count", "input_tokens", "output_tokens", "models_used",
	}
	mock.ExpectQuery("HAVING COALESCE").WillReturnRows(sqlmock.NewRows(cols).
		AddRow("ag-1", "k8s_debug", "sess-A", "Slow one", "acc-1", "success",
			now, 2.50, 30.0, 12.0, 4.0, 40, 0, 500000, 1200, "{gemini-3.1-pro-preview}"))

	filter := UsageMetricsFilter{
		AccountIDs: []string{"acc-1"},
		StartDate:  now.Add(-7 * 24 * time.Hour),
		EndDate:    now,
	}

	out, err := dao.ListAgentCalls(filter, "latency", 100, 90)
	require.NoError(t, err)
	assert.Equal(t, 90, out.LatencyPercentile)
	assert.Equal(t, 3.2, out.LatencyThresholdSeconds)
	require.Len(t, out.Rows, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestListAgentCalls_NoAccounts short-circuits to an empty list (no query).
func TestListAgentCalls_NoAccounts(t *testing.T) {
	dao := &ConversationDao{}
	out, err := dao.ListAgentCalls(UsageMetricsFilter{}, "cost", 100, 0)
	require.NoError(t, err)
	assert.Empty(t, out.Rows)
}
