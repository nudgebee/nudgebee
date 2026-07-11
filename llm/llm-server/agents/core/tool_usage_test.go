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

// TestToolSortKeys_Whitelist documents the allowed rank metrics; anything else
// (including injection attempts) must fall through to "calls".
func TestToolSortKeys_Whitelist(t *testing.T) {
	for _, k := range []string{"calls", "errors", "duration", "cost"} {
		assert.True(t, toolSortKeys[k], "expected %s rankable", k)
	}
	assert.False(t, toolSortKeys["; DROP TABLE"])
}

var toolOpCols = []string{
	"tool_name", "tool_type", "calls", "success_count", "error_count", "in_progress_count",
	"avg_duration_seconds", "p90_duration_seconds", "max_duration_seconds",
	"distinct_agents", "distinct_conversations",
}

// TestListToolUsage_MergeAndCostAttribution verifies the two-query merge: the
// operational scan provides per-tool usage/reliability/latency, the downstream-cost
// scan attaches LLM cost ONLY to the sub-agent-spawn tool, and error rate is derived.
func TestListToolUsage_MergeAndCostAttribution(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	now := time.Now()

	// 1) Operational aggregates (one row per tool).
	mock.ExpectQuery("GROUP BY tc.tool_name").WillReturnRows(
		sqlmock.NewRows(toolOpCols).
			AddRow("kubectl_get", "tool", 100, 95, 5, 0, 0.4, 1.1, 2.0, 8, 60).
			AddRow("k8s_debug", "agent", 20, 18, 2, 0, 8.2, 22.0, 30.0, 1, 15))

	// 2) Downstream LLM cost — only the agent-spawn tool appears.
	mock.ExpectQuery("INNER JOIN llm_conversation_token_usage").WillReturnRows(
		sqlmock.NewRows([]string{"tool_name", "downstream_cost_usd", "downstream_llm_calls"}).
			AddRow("k8s_debug", 4.10, 40))

	filter := UsageMetricsFilter{AccountIDs: []string{"acc-1"}, StartDate: now.Add(-24 * time.Hour), EndDate: now}

	out, err := dao.ListToolUsage(filter, "calls", 100)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	assert.Equal(t, "calls", out.SortBy)
	require.Len(t, out.Rows, 2)

	// Ranked by calls desc → kubectl_get first.
	top := out.Rows[0]
	assert.Equal(t, "kubectl_get", top.ToolName)
	assert.Equal(t, 100, top.Calls)
	assert.InDelta(t, 5.0, top.ErrorRatePct, 0.001) // 5 / 100
	assert.Zero(t, top.DownstreamCostUsd)           // plain tool → no LLM cost

	spawn := out.Rows[1]
	assert.Equal(t, "k8s_debug", spawn.ToolName)
	assert.InDelta(t, 4.10, spawn.DownstreamCostUsd, 0.001)
	assert.Equal(t, 40, spawn.DownstreamLLMCalls)
	assert.InDelta(t, 10.0, spawn.ErrorRatePct, 0.001) // 2 / 20
}

// TestListToolUsage_SortByCost ranks by downstream cost regardless of call volume.
func TestListToolUsage_SortByCost(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	now := time.Now()

	mock.ExpectQuery("GROUP BY tc.tool_name").WillReturnRows(
		sqlmock.NewRows(toolOpCols).
			AddRow("kubectl_get", "tool", 100, 100, 0, 0, 0.4, 1.1, 2.0, 8, 60).
			AddRow("k8s_debug", "agent", 20, 20, 0, 0, 8.2, 22.0, 30.0, 1, 15))
	mock.ExpectQuery("INNER JOIN llm_conversation_token_usage").WillReturnRows(
		sqlmock.NewRows([]string{"tool_name", "downstream_cost_usd", "downstream_llm_calls"}).
			AddRow("k8s_debug", 4.10, 40))

	filter := UsageMetricsFilter{AccountIDs: []string{"acc-1"}, StartDate: now.Add(-24 * time.Hour), EndDate: now}

	out, err := dao.ListToolUsage(filter, "cost", 100)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	require.Len(t, out.Rows, 2)
	assert.Equal(t, "k8s_debug", out.Rows[0].ToolName) // highest downstream cost first
	assert.Equal(t, "kubectl_get", out.Rows[1].ToolName)
}

// TestListToolUsage_NoAccounts short-circuits to an empty list (no query issued).
func TestListToolUsage_NoAccounts(t *testing.T) {
	dao := &ConversationDao{}
	out, err := dao.ListToolUsage(UsageMetricsFilter{}, "calls", 100)
	require.NoError(t, err)
	assert.Empty(t, out.Rows)
}

var toolCallCols = []string{
	"id", "tool_name", "tool_type", "status", "agent_id", "agent_name",
	"conversation_id", "conversation_title", "account_id", "duration_seconds",
	"created_at", "parameters", "response", "stderr",
}

// TestListToolCalls_FailuresFilter returns one tool's recent invocations restricted
// to the failure statuses, each carrying its conversation cross-link + error snippet.
func TestListToolCalls_FailuresFilter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	now := time.Now()

	mock.ExpectQuery("FROM llm_conversation_tool_calls tc").WillReturnRows(
		sqlmock.NewRows(toolCallCols).
			AddRow("tcl-1", "shell", "tool", "fail", "ag-1", "Kubectl Agent",
				"sess-A", "OOM killed pod", "acc-1", 2.8, now, "kubectl get pods", "", "exit 1: OOMKilled").
			AddRow("tcl-2", "shell", "tool", "error", "ag-2", "Kubectl Agent",
				"sess-B", "Disk pressure", "acc-1", 3.1, now, "df -h", "timeout after 30s", ""))

	filter := UsageMetricsFilter{AccountIDs: []string{"acc-1"}, StartDate: now.Add(-24 * time.Hour), EndDate: now}

	out, err := dao.ListToolCalls(filter, "shell", []string{"fail", "error", "terminated"}, 100)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	assert.Equal(t, "shell", out.ToolName)
	require.Len(t, out.Rows, 2)
	assert.Equal(t, "sess-A", out.Rows[0].ConversationID) // session_id cross-link
	assert.Equal(t, "exit 1: OOMKilled", out.Rows[0].Stderr)
	assert.Equal(t, "timeout after 30s", out.Rows[1].Response)
}

// TestListToolCalls_NoToolName short-circuits without a query.
func TestListToolCalls_NoToolName(t *testing.T) {
	dao := &ConversationDao{}
	out, err := dao.ListToolCalls(UsageMetricsFilter{AccountIDs: []string{"acc-1"}}, "", nil, 100)
	require.NoError(t, err)
	assert.Empty(t, out.Rows)
}
