package reports

import (
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fixture helpers

// makeAccounts returns a slice of Account stubs. All tests share this helper
// so the fixture shape stays in one place.
func makeAccounts(ids ...string) []models.Account {
	out := make([]models.Account, len(ids))
	for i, id := range ids {
		out[i] = models.Account{Id: id}
	}
	return out
}

// fullyConnectedStatus returns a connection_status map where all five keys
// reported by computeAgentStatusStats are true. Tests that want to assert
// "not partial" should use this; tests asserting partial-connectivity should
// set only a subset.
func fullyConnectedStatus() map[string]interface{} {
	return map[string]interface{}{
		"alertManagerConnection": true,
		"grafanaEnabled":         true,
		"karpenterEnabled":       true,
		"logsConnection":         true,
		"opencostConnection":     true,
	}
}

// agentRow builds a minimal agent map matching the shape produced by
// fetchBatchedAgentStatus → toAnySlice.
func agentRow(status string, connStatus map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"status":            status,
		"connection_status": connStatus,
	}
}

// queryRows is a convenience to build []query.QueryRow inline.
func queryRows(rows ...query.QueryRow) []query.QueryRow {
	return rows
}

// dailySummarySubjectClause

// The subject clause for the daily highlight email must be empty whenever
// there are no clusters needing attention — regardless of whether potential
// savings exist — so the fallback label keeps the subject readable.
func TestDailySummarySubjectClause_NoAttention(t *testing.T) {
	cases := []struct {
		name      string
		attention int
		savings   float64
	}{
		{"zero attention, zero savings", 0, 0},
		{"zero attention, has savings", 0, 1000},
		{"negative attention", -1, 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dailySummarySubjectClause(tc.attention, tc.savings)
			assert.Equal(t, "", got,
				"attention=%d savings=%.2f must produce empty clause", tc.attention, tc.savings)
		})
	}
}

// A single cluster uses the singular noun form "cluster needs" to avoid
// producing "1 clusters need attention" in the customer's inbox.
func TestDailySummarySubjectClause_SingularCluster(t *testing.T) {
	got := dailySummarySubjectClause(1, 0)
	assert.Equal(t, " · 1 cluster needs attention", got)
}

// Two or more clusters use the plural form "clusters need".
func TestDailySummarySubjectClause_PluralClusters(t *testing.T) {
	got := dailySummarySubjectClause(3, 0)
	assert.Equal(t, " · 3 clusters need attention", got)
}

// Savings are appended when positive, giving customers a dollar-denominated
// hook that drives open rates.
func TestDailySummarySubjectClause_WithSavings(t *testing.T) {
	got := dailySummarySubjectClause(2, 535.79)
	assert.Equal(t, " · 2 clusters need attention, $535.79/mo savings", got)
}

// Zero or negative savings must not append a "$0.00/mo savings" tail; the
// savings clause only appears when there is actual money on the table.
func TestDailySummarySubjectClause_ZeroSavingsOmitted(t *testing.T) {
	got := dailySummarySubjectClause(2, 0)
	assert.Equal(t, " · 2 clusters need attention", got)
	assert.NotContains(t, got, "savings",
		"zero savings must not produce a savings suffix")
}

// agentStatusSubjectClause

// When all agents are healthy the clause must be empty so the subject line
// collapses to the static "{Brand} Agent Status - {date}" form. A non-empty
// clause for a healthy fleet would alarm customers unnecessarily.
func TestAgentStatusSubjectClause_AllHealthy(t *testing.T) {
	assert.Equal(t, "", agentStatusSubjectClause(0, 0))
}

// One disconnected agent triggers the singular "agent" form.
func TestAgentStatusSubjectClause_OneDisconnected(t *testing.T) {
	assert.Equal(t, " · 1 agent disconnected", agentStatusSubjectClause(1, 0))
}

// Multiple disconnected agents use the plural "agents".
func TestAgentStatusSubjectClause_ManyDisconnected(t *testing.T) {
	assert.Equal(t, " · 3 agents disconnected", agentStatusSubjectClause(3, 0))
}

// One agent with partial connectivity uses the singular "agent" form.
func TestAgentStatusSubjectClause_OnePartial(t *testing.T) {
	assert.Equal(t, " · 1 agent partial connectivity", agentStatusSubjectClause(0, 1))
}

// Multiple partially-connected agents use the plural "agents".
func TestAgentStatusSubjectClause_ManyPartial(t *testing.T) {
	assert.Equal(t, " · 2 agents partial connectivity", agentStatusSubjectClause(0, 2))
}

// Both disconnected and partial agents are listed in the same clause,
// comma-separated, in that order (disconnected first, then partial).
func TestAgentStatusSubjectClause_BothDisconnectedAndPartial(t *testing.T) {
	got := agentStatusSubjectClause(2, 3)
	assert.Equal(t, " · 2 agents disconnected, 3 agents partial connectivity", got)
}

// subjectClausePart

// When the dynamic clause is non-empty it is returned unchanged, giving the
// email subject its alert-specific context.
func TestSubjectClausePart_NonEmptyPassesThrough(t *testing.T) {
	clause := " · 2 clusters need attention"
	assert.Equal(t, clause, subjectClausePart(clause, "Daily Insights"))
}

// An empty clause (all-healthy state) falls back to the static label so the
// subject remains identifiable as a daily report rather than just
// "{Brand} - {date}". The leading space is intentional: the assembled subject
// is "{Brand}" + subjectClausePart(...) + " - {date}", so the space
// separates the brand name from the label.
func TestSubjectClausePart_EmptyClauseFallsBackToLabel(t *testing.T) {
	assert.Equal(t, " Daily Insights", subjectClausePart("", "Daily Insights"))
}

// rowsOf

// The canonical payload shape is {"rows": [...]}. rowsOf must extract the
// slice so callers can pass either a raw GqlResponse section or a
// getTroubleshootHighlights bucket without branching.
func TestRowsOf_ExtractsRowsKey(t *testing.T) {
	rows := []interface{}{"event-a", "event-b"}
	got := rowsOf(map[string]interface{}{"rows": rows})
	// Must return the exact same slice — not just a non-nil value.
	require.Equal(t, rows, got)
}

// A nil input is safely ignored — this avoids a panic when a highlight section
// is absent from the response (e.g. no recommendations today).
func TestRowsOf_NilInputReturnsNil(t *testing.T) {
	assert.Nil(t, rowsOf(nil))
}

// A non-map value (e.g. stray string from a misconfigured upstream) returns nil
// rather than panicking.
func TestRowsOf_NonMapInputReturnsNil(t *testing.T) {
	assert.Nil(t, rowsOf("not a map"))
}

// toStringMapSlice

// JSON-round-tripped GqlResponse data arrives as []interface{} of
// map[string]interface{}. toStringMapSlice must normalise this into the typed
// slice that countForAccount and sumEventCountForAccount expect.
func TestToStringMapSlice_InterfaceSliceOfMaps(t *testing.T) {
	input := []interface{}{
		map[string]interface{}{"id": "acc-1"},
		map[string]interface{}{"id": "acc-2"},
	}
	got := toStringMapSlice(input)
	require.Len(t, got, 2)
	assert.Equal(t, "acc-1", got[0]["id"])
}

// Non-map elements inside a []interface{} (corrupt payloads) must be silently
// dropped rather than panicking or producing a nil entry in the output slice.
func TestToStringMapSlice_DropsBadElementsInsideSlice(t *testing.T) {
	input := []interface{}{
		map[string]interface{}{"id": "acc-1"},
		"not-a-map",
		42,
	}
	got := toStringMapSlice(input)
	require.Len(t, got, 1, "non-map elements must be silently dropped")
	assert.Equal(t, "acc-1", got[0]["id"])
}

// Raw query results (query.ExecuteQuery output) arrive as []query.QueryRow,
// a distinct Go type. toStringMapSlice must convert these to the uniform
// map slice so callers don't have to distinguish the two sources.
func TestToStringMapSlice_QueryRowSlice(t *testing.T) {
	input := queryRows(
		query.QueryRow{"account_id": "acc-1", "event_count": int64(5)},
	)
	got := toStringMapSlice(input)
	require.Len(t, got, 1)
	assert.Equal(t, "acc-1", got[0]["account_id"])
}

// A nil value is common when a GqlResponse key is absent. Must return nil,
// not an empty slice, so callers can distinguish "key missing" from
// "key present but empty".
func TestToStringMapSlice_NilInputReturnsNil(t *testing.T) {
	assert.Nil(t, toStringMapSlice(nil))
}

// Any unknown type (e.g. a string or int) must produce nil. The function is
// a type dispatcher, not a panic surface.
func TestToStringMapSlice_UnknownTypeReturnsNil(t *testing.T) {
	assert.Nil(t, toStringMapSlice("unexpected"))
	assert.Nil(t, toStringMapSlice(42))
}

// toInt64

// toInt64 is the numeric coercion used when summing event counts. JSON
// decoding produces float64; sqlx scans may produce int64 or []uint8; manual
// test fixtures use int. All must coerce correctly.
func TestToInt64(t *testing.T) {
	cases := []struct {
		name  string
		input interface{}
		want  int64
	}{
		{"int64 passthrough", int64(42), 42},
		{"int promotes to int64", int(10), 10},
		{"int32 promotes to int64", int32(7), 7},
		{"float64 truncates (JSON default)", float64(3.9), 3},
		{"float32 truncates", float32(1.1), 1},
		{"numeric string parses", "99", 99},
		{"[]uint8 (sqlx TEXT scan) parses", []uint8("55"), 55},
		{"nil returns zero — no panic", nil, 0},
		{"non-numeric string returns zero", "abc", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, toInt64(tc.input))
		})
	}
}

func TestToFloat64(t *testing.T) {
	cases := []struct {
		name  string
		input interface{}
		want  float64
	}{
		{"float64 passthrough", float64(100.5), 100.5},
		{"float32 promotes", float32(1.5), 1.5},
		{"int64 promotes", int64(7), 7},
		{"int promotes", int(3), 3},
		{"numeric string (lib/pq numeric)", "50.5", 50.5},
		{"[]uint8 (sqlx MapScan numeric)", []uint8("25.25"), 25.25},
		{"nil returns zero — no panic", nil, 0},
		{"non-numeric string returns zero", "not-a-float", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.InDelta(t, tc.want, toFloat64(tc.input), 0.001)
		})
	}
}

// countForAccount

// countForAccount tallies how many rows in a grouping response belong to a
// given account_id. It drives the "needs attention" check in
// computeDailySummaryStats and must correctly handle multiple rows per account.
func TestCountForAccount_CountsMatchingRows(t *testing.T) {
	rows := []map[string]interface{}{
		{"account_id": "acc-1"},
		{"account_id": "acc-1"},
		{"account_id": "acc-2"},
	}
	assert.Equal(t, 2, countForAccount(rows, "acc-1"))
	assert.Equal(t, 1, countForAccount(rows, "acc-2"))
	assert.Equal(t, 0, countForAccount(rows, "acc-99"),
		"a missing account must return 0, not panic")
}

// A nil row slice (no data from the DB or GqlResponse) must return 0 without
// panicking — this is the common case when there are no open recommendations.
func TestCountForAccount_NilRowsReturnsZero(t *testing.T) {
	assert.Equal(t, 0, countForAccount(nil, "acc-1"))
}

// sumEventCountForAccount

// sumEventCountForAccount aggregates the event_count field across all rows
// for a given account. Multiple rows per account are summed, not just the first.
func TestSumEventCountForAccount_SumsAllMatchingRows(t *testing.T) {
	rows := []map[string]interface{}{
		{"account_id": "acc-1", "event_count": int64(10)},
		{"account_id": "acc-1", "event_count": int64(5)},
		{"account_id": "acc-2", "event_count": int64(3)},
	}
	assert.Equal(t, int64(15), sumEventCountForAccount(rows, "acc-1"))
	assert.Equal(t, int64(3), sumEventCountForAccount(rows, "acc-2"))
	assert.Equal(t, int64(0), sumEventCountForAccount(rows, "acc-99"))
}

// JSON-decoded payloads arrive with float64 event_count values (the default
// numeric type for json.Unmarshal). toInt64 must handle this so counts are
// not silently dropped when the payload was round-tripped through JSON.
func TestSumEventCountForAccount_Float64EventCount(t *testing.T) {
	rows := []map[string]interface{}{
		{"account_id": "acc-1", "event_count": float64(7)},
	}
	assert.Equal(t, int64(7), sumEventCountForAccount(rows, "acc-1"))
}

// isPayloadEmpty

// isPayloadEmpty gates whether a daily report is published. A nil Data map
// must be treated as empty to prevent a nil-deref downstream.
func TestIsPayloadEmpty_NilDataIsEmpty(t *testing.T) {
	assert.True(t, isPayloadEmpty(common.GqlResponse{Data: nil}))
}

// When the insight key is entirely absent (no open insights for this tenant)
// the payload is empty — no report should be sent for a fully-quiet day.
func TestIsPayloadEmpty_MissingInsightKeyIsEmpty(t *testing.T) {
	resp := common.GqlResponse{Data: map[string]any{"cloud_accounts": []interface{}{}}}
	assert.True(t, isPayloadEmpty(resp))
}

// An empty insight slice (all insights closed) must be treated as empty.
// This prevents a report being sent with a zero-row table.
func TestIsPayloadEmpty_EmptySliceIsEmpty(t *testing.T) {
	resp := common.GqlResponse{Data: map[string]any{"insight": []interface{}{}}}
	assert.True(t, isPayloadEmpty(resp))
}

// At least one open insight makes the payload non-empty — the report is
// worth sending.
func TestIsPayloadEmpty_NonEmptySliceIsNotEmpty(t *testing.T) {
	resp := common.GqlResponse{Data: map[string]any{
		"insight": []interface{}{
			map[string]interface{}{"title": "CPU throttling on cart-service"},
		},
	}}
	assert.False(t, isPayloadEmpty(resp))
}

// calculateTotalPotentialSavings

// The savings figure in the email subject and body is the sum of
// sum_estimated_savings across all recommendation rows. This verifies that
// big.Float is summed correctly across multiple rows and converted back to
// float64.
func TestCalculateTotalPotentialSavings_SumsAllRows(t *testing.T) {
	data := map[string]interface{}{
		"rows": queryRows(
			query.QueryRow{"sum_estimated_savings": float64(100.50)},
			query.QueryRow{"sum_estimated_savings": float64(200.25)},
			query.QueryRow{"sum_estimated_savings": float64(50.00)},
		),
	}
	assert.InDelta(t, 350.75, calculateTotalPotentialSavings(data), 0.001)
}

// Zero rows must produce zero savings — the email subject must not claim
// savings when the tenant has no open recommendations.
func TestCalculateTotalPotentialSavings_EmptyRowsIsZero(t *testing.T) {
	data := map[string]interface{}{
		"rows": queryRows(),
	}
	assert.Equal(t, float64(0), calculateTotalPotentialSavings(data))
}

// Garbage savings values must be skipped; numeric strings and []uint8 (the
// shapes sqlx.MapScan / lib/pq yield for numeric columns) must still be summed.
func TestCalculateTotalPotentialSavings_CoercesDriverNumericTypes(t *testing.T) {
	data := map[string]interface{}{
		"rows": queryRows(
			query.QueryRow{"sum_estimated_savings": "not-a-float"},
			query.QueryRow{"sum_estimated_savings": float64(100)},
			query.QueryRow{"sum_estimated_savings": "50.5"},
			query.QueryRow{"sum_estimated_savings": []uint8("25.25")},
		),
	}
	assert.InDelta(t, 175.75, calculateTotalPotentialSavings(data), 0.001)
}

// A nil value for data["rows"] (missing key in the map) must return 0 instead
// of panicking. Live recommendation_groupings payloads can omit the key.
func TestCalculateTotalPotentialSavings_NilRowsIsZero(t *testing.T) {
	data := map[string]interface{}{} // "rows" key absent
	assert.Equal(t, float64(0), calculateTotalPotentialSavings(data))
}

// JSON-decoded recommendation payloads arrive as []interface{} of maps, not
// []query.QueryRow. Savings on that shape must still be summed.
func TestCalculateTotalPotentialSavings_InterfaceSliceIsSummed(t *testing.T) {
	data := map[string]interface{}{
		"rows": []interface{}{
			map[string]interface{}{"sum_estimated_savings": float64(99)},
		},
	}
	assert.InDelta(t, 99.0, calculateTotalPotentialSavings(data), 0.001)
}

// computeDailySummaryStats

// computeDailySummaryStats mirrors the JavaScript counting logic in
// daily_highlight_report.html so the email subject and the body template always
// report the same cluster and attention counts. When a tenant has one cluster
// with no open signals the cluster count must be 1 and attention must be 0.
func TestComputeDailySummaryStats_NoOpenSignals(t *testing.T) {
	insights := common.GqlResponse{
		Data: map[string]any{
			"cloud_accounts":                       []interface{}{map[string]interface{}{"id": "acc-1"}},
			"insight":                              []interface{}{},
			"recommendation_security_groupings_v2": map[string]interface{}{"rows": []interface{}{}},
		},
	}
	highlight1 := map[string]interface{}{
		"data": map[string]interface{}{
			"recommendation_groupings": map[string]interface{}{"rows": queryRows()},
			"app_events":               map[string]interface{}{"rows": queryRows()},
			"node_events":              map[string]interface{}{"rows": queryRows()},
			"pod_events":               map[string]interface{}{"rows": queryRows()},
		},
	}

	clusters, attention := computeDailySummaryStats(insights, highlight1)

	assert.Equal(t, 1, clusters, "one cloud account → one cluster")
	assert.Equal(t, 0, attention, "no open signals → no attention")
}

// A single open insight on one of two clusters must mark only that cluster as
// needing attention, leaving the other (quiet) cluster uncounted.
func TestComputeDailySummaryStats_InsightOnOneClusterRaisesAttention(t *testing.T) {
	insights := common.GqlResponse{
		Data: map[string]any{
			"cloud_accounts": []interface{}{
				map[string]interface{}{"id": "acc-1"},
				map[string]interface{}{"id": "acc-2"},
			},
			"insight": []interface{}{
				map[string]interface{}{"account_id": "acc-1"},
			},
			"recommendation_security_groupings_v2": map[string]interface{}{"rows": []interface{}{}},
		},
	}
	highlight1 := map[string]interface{}{
		"data": map[string]interface{}{
			"recommendation_groupings": map[string]interface{}{"rows": queryRows()},
			"app_events":               map[string]interface{}{"rows": queryRows()},
			"node_events":              map[string]interface{}{"rows": queryRows()},
			"pod_events":               map[string]interface{}{"rows": queryRows()},
		},
	}

	clusters, attention := computeDailySummaryStats(insights, highlight1)

	assert.Equal(t, 2, clusters)
	assert.Equal(t, 1, attention, "insight on acc-1 must mark only acc-1 as needing attention")
}

// A recommendation row alone (no open insights) must be sufficient to mark a
// cluster as needing attention. This guards against a regression where only
// the insight signal is checked.
func TestComputeDailySummaryStats_RecommendationOnlyRaisesAttention(t *testing.T) {
	insights := common.GqlResponse{
		Data: map[string]any{
			"cloud_accounts": []interface{}{
				map[string]interface{}{"id": "acc-1"},
				map[string]interface{}{"id": "acc-2"},
			},
			"insight":                              []interface{}{}, // no open insights
			"recommendation_security_groupings_v2": map[string]interface{}{"rows": []interface{}{}},
		},
	}
	highlight1 := map[string]interface{}{
		"data": map[string]interface{}{
			// acc-1 has an open recommendation; acc-2 does not.
			"recommendation_groupings": map[string]interface{}{
				"rows": queryRows(
					query.QueryRow{"account_id": "acc-1"},
				),
			},
			"app_events":  map[string]interface{}{"rows": queryRows()},
			"node_events": map[string]interface{}{"rows": queryRows()},
			"pod_events":  map[string]interface{}{"rows": queryRows()},
		},
	}

	clusters, attention := computeDailySummaryStats(insights, highlight1)

	assert.Equal(t, 2, clusters)
	assert.Equal(t, 1, attention, "open recommendation on acc-1 must mark acc-1 as needing attention")
}

// A security-grouping row alone must mark a cluster as needing attention.
func TestComputeDailySummaryStats_SecurityOnlyRaisesAttention(t *testing.T) {
	insights := common.GqlResponse{
		Data: map[string]any{
			"cloud_accounts": []interface{}{
				map[string]interface{}{"id": "acc-1"},
			},
			"insight": []interface{}{},
			"recommendation_security_groupings_v2": map[string]interface{}{
				"rows": []interface{}{
					map[string]interface{}{"account_id": "acc-1"},
				},
			},
		},
	}
	highlight1 := map[string]interface{}{
		"data": map[string]interface{}{
			"recommendation_groupings": map[string]interface{}{"rows": queryRows()},
			"app_events":               map[string]interface{}{"rows": queryRows()},
			"node_events":              map[string]interface{}{"rows": queryRows()},
			"pod_events":               map[string]interface{}{"rows": queryRows()},
		},
	}

	_, attention := computeDailySummaryStats(insights, highlight1)

	assert.Equal(t, 1, attention, "security grouping on acc-1 must mark acc-1 as needing attention")
}

// Event counts on app/node/pod buckets must raise attention even with no
// insights or recommendations.
func TestComputeDailySummaryStats_EventsOnlyRaiseAttention(t *testing.T) {
	insights := common.GqlResponse{
		Data: map[string]any{
			"cloud_accounts": []interface{}{
				map[string]interface{}{"id": "acc-1"},
			},
			"insight":                              []interface{}{},
			"recommendation_security_groupings_v2": map[string]interface{}{"rows": []interface{}{}},
		},
	}
	highlight1 := map[string]interface{}{
		"data": map[string]interface{}{
			"recommendation_groupings": map[string]interface{}{"rows": queryRows()},
			"app_events": map[string]interface{}{
				"rows": queryRows(
					query.QueryRow{"account_id": "acc-1", "event_count": int64(4)},
				),
			},
			"node_events": map[string]interface{}{"rows": queryRows()},
			"pod_events":  map[string]interface{}{"rows": queryRows()},
		},
	}

	_, attention := computeDailySummaryStats(insights, highlight1)

	assert.Equal(t, 1, attention, "app events on acc-1 must mark acc-1 as needing attention")
}

// When a cluster has both an open insight and an open recommendation it must
// still count as only 1 cluster needing attention, not 2. The per-account
// gate is a logical OR across all signal sources, not a sum.
func TestComputeDailySummaryStats_InsightAndRecommendationNoDoubleCounting(t *testing.T) {
	insights := common.GqlResponse{
		Data: map[string]any{
			"cloud_accounts": []interface{}{
				map[string]interface{}{"id": "acc-1"},
			},
			"insight": []interface{}{
				map[string]interface{}{"account_id": "acc-1"},
			},
			"recommendation_security_groupings_v2": map[string]interface{}{"rows": []interface{}{}},
		},
	}
	highlight1 := map[string]interface{}{
		"data": map[string]interface{}{
			"recommendation_groupings": map[string]interface{}{
				"rows": queryRows(
					query.QueryRow{"account_id": "acc-1"}, // same account as the insight above
				),
			},
			"app_events":  map[string]interface{}{"rows": queryRows()},
			"node_events": map[string]interface{}{"rows": queryRows()},
			"pod_events":  map[string]interface{}{"rows": queryRows()},
		},
	}

	_, attention := computeDailySummaryStats(insights, highlight1)

	assert.Equal(t, 1, attention, "insight + recommendation on the same cluster must count as 1, not 2")
}

// A nil or missing highlight1["data"] map must not panic. This guards the
// type assertion h1Data, _ := highlight1["data"].(map[string]interface{}) on
// the nil path.
func TestComputeDailySummaryStats_NilHighlight1DataIsZeroAttention(t *testing.T) {
	insights := common.GqlResponse{
		Data: map[string]any{
			"cloud_accounts": []interface{}{
				map[string]interface{}{"id": "acc-1"},
			},
			"insight":                              []interface{}{},
			"recommendation_security_groupings_v2": map[string]interface{}{"rows": []interface{}{}},
		},
	}
	highlight1 := map[string]interface{}{} // "data" key absent

	clusters, attention := computeDailySummaryStats(insights, highlight1)

	assert.Equal(t, 1, clusters)
	assert.Equal(t, 0, attention, "nil highlight data must not panic and must produce zero attention")
}

// computeAgentStatusStats

// All agents fully running with all five connection keys true must produce
// total=2, disconnected=0, partial=0. This is the happy path: no report noise.
func TestComputeAgentStatusStats_AllFullyConnected(t *testing.T) {
	accounts := makeAccounts("acc-1", "acc-2")
	agentsByAccount := map[string][]interface{}{
		"acc-1": {agentRow("running", fullyConnectedStatus())},
		"acc-2": {agentRow("connected", fullyConnectedStatus())},
	}

	total, disconnected, partial := computeAgentStatusStats(accounts, agentsByAccount)

	assert.Equal(t, 2, total)
	assert.Equal(t, 0, disconnected)
	assert.Equal(t, 0, partial)
}

// An account with no agent row in agentsByAccount is treated as disconnected.
// This covers the case where the agent crashed and never sent a heartbeat.
func TestComputeAgentStatusStats_MissingAgentCountsAsDisconnected(t *testing.T) {
	accounts := makeAccounts("acc-1")
	agentsByAccount := map[string][]interface{}{} // no entry for acc-1

	total, disconnected, _ := computeAgentStatusStats(accounts, agentsByAccount)

	assert.Equal(t, 1, total)
	assert.Equal(t, 1, disconnected, "absent agent row must count as disconnected")
}

// An agent whose status field is not one of the running/connected/active set
// is counted as disconnected. This guards against drift in agent status strings.
func TestComputeAgentStatusStats_BadStatusCountsAsDisconnected(t *testing.T) {
	cases := []string{"error", "failed", "unknown", ""}
	for _, status := range cases {
		t.Run("status="+status, func(t *testing.T) {
			accounts := makeAccounts("acc-1")
			agentsByAccount := map[string][]interface{}{
				"acc-1": {agentRow(status, fullyConnectedStatus())},
			}
			_, disconnected, _ := computeAgentStatusStats(accounts, agentsByAccount)
			assert.Equal(t, 1, disconnected)
		})
	}
}

// An agent that is running but has fewer than all five connection keys true is
// counted as partial. An empty connection_status map means 0/5 → partial.
func TestComputeAgentStatusStats_EmptyConnectionStatusIsPartial(t *testing.T) {
	accounts := makeAccounts("acc-1")
	agentsByAccount := map[string][]interface{}{
		"acc-1": {agentRow("running", map[string]interface{}{})},
	}

	_, disconnected, partial := computeAgentStatusStats(accounts, agentsByAccount)

	assert.Equal(t, 0, disconnected, "running agent must not be disconnected")
	assert.Equal(t, 1, partial, "0/5 connection keys → partial")
}

// Only one of the five connection integrations enabled (e.g. only
// alertManager, nothing else) must still be counted as partial.
func TestComputeAgentStatusStats_PartialConnectivity(t *testing.T) {
	accounts := makeAccounts("acc-1")
	connStatus := map[string]interface{}{
		"alertManagerConnection": true,
		// grafanaEnabled, karpenterEnabled, logsConnection, opencostConnection absent
	}
	agentsByAccount := map[string][]interface{}{
		"acc-1": {agentRow("running", connStatus)},
	}

	_, disconnected, partial := computeAgentStatusStats(accounts, agentsByAccount)

	assert.Equal(t, 0, disconnected)
	assert.Equal(t, 1, partial, "1/5 connection keys → partial")
}

// Mixed fleet: one fully-connected agent, one disconnected, one partial.
// All three counters must be independent.
func TestComputeAgentStatusStats_MixedFleet(t *testing.T) {
	accounts := makeAccounts("full", "disc", "part")
	agentsByAccount := map[string][]interface{}{
		"full": {agentRow("running", fullyConnectedStatus())},
		"disc": {agentRow("error", fullyConnectedStatus())},
		"part": {agentRow("active", map[string]interface{}{"alertManagerConnection": true})},
	}

	total, disconnected, partial := computeAgentStatusStats(accounts, agentsByAccount)

	assert.Equal(t, 3, total)
	assert.Equal(t, 1, disconnected)
	assert.Equal(t, 1, partial)
}

// An account that has multiple agent rows must be classified by the first
// agent in the slice (the implementation uses agents[0]). This test documents
// the current semantics so any future change to multi-agent handling is a
// deliberate decision rather than an accidental regression.
func TestComputeAgentStatusStats_MultipleAgentsUsesFirst(t *testing.T) {
	accounts := makeAccounts("acc-1")
	// First agent: running + fully connected → should be healthy (not partial, not disconnected).
	// Second agent: error → would make it disconnected if the impl iterated all agents.
	agentsByAccount := map[string][]interface{}{
		"acc-1": {
			agentRow("running", fullyConnectedStatus()),
			agentRow("error", fullyConnectedStatus()),
		},
	}

	total, disconnected, partial := computeAgentStatusStats(accounts, agentsByAccount)

	assert.Equal(t, 1, total)
	// Current semantics: first agent wins. The account is healthy because
	// agents[0] is running and fully connected.
	assert.Equal(t, 0, disconnected, "first agent is running — account must not be disconnected")
	assert.Equal(t, 0, partial, "first agent has all connection keys — account must not be partial")
}

// toAnySlice

// toAnySlice round-trips a typed struct slice through JSON so the result
// matches the []interface{} of map[string]interface{} shape that downstream
// GqlResponse handling and payload publishing expect. This avoids a second
// full JSON marshal during notify dispatch.
func TestToAnySlice_StructSliceRoundTrips(t *testing.T) {
	input := []k8sAccountRow{
		{Id: "acc-1", AccountName: "cluster-a"},
		{Id: "acc-2", AccountName: "cluster-b"},
	}

	got, err := toAnySlice(input)

	require.NoError(t, err)
	require.Len(t, got, 2)
	first, ok := got[0].(map[string]interface{})
	require.True(t, ok, "each element must be map[string]interface{}")
	assert.Equal(t, "acc-1", first["id"])
	assert.Equal(t, "cluster-a", first["account_name"])
	second, ok := got[1].(map[string]interface{})
	require.True(t, ok, "second element must be map[string]interface{}")
	assert.Equal(t, "acc-2", second["id"])
	assert.Equal(t, "cluster-b", second["account_name"])
}

// An empty struct slice must produce an empty (non-nil) []interface{} rather
// than nil, so callers can safely iterate without a nil check.
func TestToAnySlice_EmptySliceProducesEmptySlice(t *testing.T) {
	got, err := toAnySlice([]k8sAccountRow{})
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

// eventsSummarySubjectClause

// When there are no high-priority events the clause must be empty so the
// subject falls back to the static "{Brand} Events Summary - {date}" form.
func TestEventsSummarySubjectClause_NoHighPriorityEvents(t *testing.T) {
	cases := []struct {
		name      string
		totalHigh int64
	}{
		{"zero high events", 0},
		{"negative high events", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := eventsSummarySubjectClause(tc.totalHigh)
			assert.Equal(t, "", got,
				"totalHigh=%d must produce empty clause", tc.totalHigh)
		})
	}
}

// A single high-priority event uses the singular noun form "event".
func TestEventsSummarySubjectClause_SingleEvent(t *testing.T) {
	got := eventsSummarySubjectClause(1)
	assert.Equal(t, " · 1 high-priority event", got)
}

// Two or more high-priority events use the plural noun form "events".
func TestEventsSummarySubjectClause_MultipleEvents(t *testing.T) {
	got := eventsSummarySubjectClause(5)
	assert.Equal(t, " · 5 high-priority events", got)
}

// computeEventsSummaryStats

// When there are no event rows for any account all three return values are
// zero. This is the baseline "quiet day" path.
func TestComputeEventsSummaryStats_NoEvents(t *testing.T) {
	accountList := common.GqlResponse{
		Data: map[string]any{
			"accounts": []interface{}{
				map[string]interface{}{"id": "acc-1"},
			},
		},
	}
	dailyEventCounts := map[string]interface{}{
		"data": map[string]interface{}{
			"event_groupings": map[string]interface{}{
				"rows": queryRows(),
			},
		},
	}

	totalEvents, totalHigh, accountCount := computeEventsSummaryStats(accountList, dailyEventCounts)

	assert.Equal(t, int64(0), totalEvents)
	assert.Equal(t, int64(0), totalHigh)
	assert.Equal(t, 0, accountCount)
}

// Multiple event rows for the same account must be summed, and the account
// must be counted once (not once per row).
func TestComputeEventsSummaryStats_SumsRowsAndCountsAccountOnce(t *testing.T) {
	accountList := common.GqlResponse{
		Data: map[string]any{
			"accounts": []interface{}{
				map[string]interface{}{"id": "acc-1"},
				map[string]interface{}{"id": "acc-2"},
			},
		},
	}
	dailyEventCounts := map[string]interface{}{
		"data": map[string]interface{}{
			"event_groupings": map[string]interface{}{
				"rows": queryRows(
					query.QueryRow{"account_id": "acc-1", "event_count": int64(10), "count_priority_high": int64(3)},
					query.QueryRow{"account_id": "acc-1", "event_count": int64(5), "count_priority_high": int64(2)},
					query.QueryRow{"account_id": "acc-2", "event_count": int64(7), "count_priority_high": int64(0)},
				),
			},
		},
	}

	totalEvents, totalHigh, accountCount := computeEventsSummaryStats(accountList, dailyEventCounts)

	assert.Equal(t, int64(22), totalEvents, "10+5+7=22 events across both accounts")
	assert.Equal(t, int64(5), totalHigh, "3+2=5 high-priority events on acc-1")
	assert.Equal(t, 2, accountCount, "both accounts have rows — both must be counted")
}

// An account with no rows in the event_groupings response must not be included
// in accountCount, even though it appears in the accounts list.
func TestComputeEventsSummaryStats_AccountWithNoRowsNotCounted(t *testing.T) {
	accountList := common.GqlResponse{
		Data: map[string]any{
			"accounts": []interface{}{
				map[string]interface{}{"id": "acc-1"},
				map[string]interface{}{"id": "acc-2"}, // no rows below
			},
		},
	}
	dailyEventCounts := map[string]interface{}{
		"data": map[string]interface{}{
			"event_groupings": map[string]interface{}{
				"rows": queryRows(
					query.QueryRow{"account_id": "acc-1", "event_count": int64(4), "count_priority_high": int64(1)},
				),
			},
		},
	}

	_, _, accountCount := computeEventsSummaryStats(accountList, dailyEventCounts)

	assert.Equal(t, 1, accountCount, "acc-2 has no rows and must not be counted")
}

// float64 event_count and count_priority_high values (the shape produced by
// json.Unmarshal) must be coerced correctly via toInt64.
func TestComputeEventsSummaryStats_Float64FieldsCoerced(t *testing.T) {
	accountList := common.GqlResponse{
		Data: map[string]any{
			"accounts": []interface{}{
				map[string]interface{}{"id": "acc-1"},
			},
		},
	}
	dailyEventCounts := map[string]interface{}{
		"data": map[string]interface{}{
			"event_groupings": map[string]interface{}{
				"rows": queryRows(
					query.QueryRow{"account_id": "acc-1", "event_count": float64(8), "count_priority_high": float64(2)},
				),
			},
		},
	}

	totalEvents, totalHigh, _ := computeEventsSummaryStats(accountList, dailyEventCounts)

	assert.Equal(t, int64(8), totalEvents)
	assert.Equal(t, int64(2), totalHigh)
}
