package reports

import (
	"strings"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/query"
)

// The report reads its rules from one list. Both consumers must use it, and
// every entry must be spelled the way producers write rule_name — a hyphenated
// legacy spelling matches no row and silently empties that rule from the
// report.
func TestOptimizationRuleNames(t *testing.T) {
	if len(optimizationRuleNames) == 0 {
		t.Fatal("the report would cover no rules at all")
	}

	seen := map[string]bool{}
	for _, name := range optimizationRuleNames {
		if strings.Contains(name, "-") {
			t.Errorf("%q is a legacy hyphenated spelling and matches no row; producers write snake_case", name)
		}
		if seen[name] {
			t.Errorf("%q is listed twice", name)
		}
		seen[name] = true
	}
}

// Spelled out so that renaming a rule is a deliberate, reviewed edit rather
// than a one-character slip that silently empties the report. Each name is the
// rule_name its producer writes.
func TestOptimizationRuleNamesAreTheOnesProducersWrite(t *testing.T) {
	want := []string{
		"pod_right_sizing",
		"unused_pvc",
		"pv_rightsize",
		"replica_right_sizing",
		"abandoned_resource",
		"eks_cluster_upgrade",
	}

	if len(optimizationRuleNames) != len(want) {
		t.Fatalf("report covers %d rules, expected %d: %q", len(optimizationRuleNames), len(want), optimizationRuleNames)
	}
	for i, name := range want {
		if optimizationRuleNames[i] != name {
			t.Errorf("rule %d is %q, expected %q", i, optimizationRuleNames[i], name)
		}
	}
}

func TestPrepareRecommendationQueryFiltersOnTheSharedRuleList(t *testing.T) {
	q := prepareRecommendationQuery("tenant", nil)

	var filtered []string
	for _, clause := range q.Where.And {
		if in, ok := clause.Binary["rule_name"][query.In]; ok {
			names, ok := in.([]string)
			if !ok {
				t.Fatalf("rule_name filter is %T, not a string slice", in)
			}
			filtered = names
		}
	}

	if filtered == nil {
		t.Fatal("the query does not filter on rule_name at all")
	}
	if len(filtered) != len(optimizationRuleNames) {
		t.Fatalf("query filters on %d rules, the shared list has %d", len(filtered), len(optimizationRuleNames))
	}
	for i, name := range optimizationRuleNames {
		if filtered[i] != name {
			t.Errorf("rule %d: query filters on %q, the shared list says %q", i, filtered[i], name)
		}
	}
}

func TestDailySummarySubjectClause(t *testing.T) {
	tests := []struct {
		name     string
		clusters int
		savings  float64
		want     string
	}{
		{"nothing needs attention", 0, 100, ""},
		{"one cluster no savings", 1, 0, " · 1 cluster needs attention"},
		{"two clusters with savings", 2, 535.79, " · 2 clusters need attention, $535.79/mo savings"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := dailySummarySubjectClause(tc.clusters, tc.savings); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSubjectClausePart(t *testing.T) {
	if got := subjectClausePart(" · 1 cluster needs attention", " Daily Insights"); got != " · 1 cluster needs attention" {
		t.Fatalf("dynamic clause: got %q", got)
	}
	// Callers pass fallbackLabel with a leading space (" Daily Insights"); subjectClausePart
	// prepends one more so the brand name is separated from the report label.
	if got := subjectClausePart("", " Daily Insights"); got != "  Daily Insights" {
		t.Fatalf("fallback: got %q", got)
	}
}

func TestAgentStatusSubjectClause(t *testing.T) {
	if got := agentStatusSubjectClause(0, 0); got != "" {
		t.Fatalf("healthy: got %q", got)
	}
	if got := agentStatusSubjectClause(1, 0); got != " · 1 agent disconnected" {
		t.Fatalf("one disconnected: got %q", got)
	}
	if got := agentStatusSubjectClause(2, 1); got != " · 2 agents disconnected, 1 agent partial connectivity" {
		t.Fatalf("mixed: got %q", got)
	}
}

func TestEventsSummarySubjectClause(t *testing.T) {
	if got := eventsSummarySubjectClause(0); got != "" {
		t.Fatalf("none: got %q", got)
	}
	if got := eventsSummarySubjectClause(1); got != " · 1 high-priority event" {
		t.Fatalf("singular: got %q", got)
	}
	if got := eventsSummarySubjectClause(12); got != " · 12 high-priority events" {
		t.Fatalf("plural: got %q", got)
	}
}

func TestComputeDailySummaryStats(t *testing.T) {
	accA, accB := "acc-a", "acc-b"
	insights := common.GqlResponse{
		Data: map[string]any{
			"cloud_accounts": []interface{}{
				map[string]interface{}{"id": accA},
				map[string]interface{}{"id": accB},
			},
			"insight": []interface{}{
				map[string]interface{}{"account_id": accA},
			},
			"recommendation_security_groupings_v2": map[string]interface{}{
				"rows": []interface{}{
					map[string]interface{}{"account_id": accB},
				},
			},
		},
	}
	highlight1 := map[string]interface{}{
		"data": map[string]interface{}{
			"recommendation_groupings": map[string]interface{}{
				"rows": []interface{}{
					map[string]interface{}{"account_id": accA},
				},
			},
			"app_events": map[string]interface{}{
				"rows": []interface{}{
					map[string]interface{}{"account_id": accB, "event_count": int64(3)},
				},
			},
			"node_events": map[string]interface{}{"rows": []interface{}{}},
			"pod_events":  map[string]interface{}{"rows": []interface{}{}},
		},
	}

	clusterCount, attentionCount := computeDailySummaryStats(insights, highlight1)
	if clusterCount != 2 {
		t.Fatalf("clusterCount = %d, want 2", clusterCount)
	}
	if attentionCount != 2 {
		t.Fatalf("attentionCount = %d, want 2 (one account per signal type)", attentionCount)
	}
}

func TestComputeDailySummaryStatsNoDoubleCount(t *testing.T) {
	accA := "acc-a"
	insights := common.GqlResponse{
		Data: map[string]any{
			"cloud_accounts": []interface{}{
				map[string]interface{}{"id": accA},
			},
			"insight": []interface{}{
				map[string]interface{}{"account_id": accA},
				map[string]interface{}{"account_id": accA},
			},
		},
	}
	highlight1 := map[string]interface{}{
		"data": map[string]interface{}{
			"recommendation_groupings": map[string]interface{}{
				"rows": []interface{}{
					map[string]interface{}{"account_id": accA},
					map[string]interface{}{"account_id": accA},
				},
			},
			"app_events":  map[string]interface{}{"rows": []interface{}{}},
			"node_events": map[string]interface{}{"rows": []interface{}{}},
			"pod_events":  map[string]interface{}{"rows": []interface{}{}},
		},
	}
	_, attentionCount := computeDailySummaryStats(insights, highlight1)
	if attentionCount != 1 {
		t.Fatalf("attentionCount = %d, want 1 (count account once)", attentionCount)
	}
}

func TestComputeAgentStatusStats(t *testing.T) {
	accounts := []models.Account{
		{Id: "missing-agent"},
		{Id: "disconnected"},
		{Id: "partial"},
		{Id: "healthy"},
	}
	agentsByAccount := map[string][]interface{}{
		"disconnected": {
			map[string]interface{}{"status": "stopped", "connection_status": map[string]interface{}{}},
		},
		"partial": {
			map[string]interface{}{
				"status": "running",
				"connection_status": map[string]interface{}{
					"alertManagerConnection": true,
					"grafanaEnabled":         false,
					"karpenterEnabled":       false,
					"logsConnection":         false,
					"opencostConnection":     false,
				},
			},
		},
		"healthy": {
			map[string]interface{}{
				"status": "connected",
				"connection_status": map[string]interface{}{
					"alertManagerConnection": true,
					"grafanaEnabled":         true,
					"karpenterEnabled":       true,
					"logsConnection":         true,
					"opencostConnection":     true,
				},
			},
		},
	}

	total, disconnected, partial := computeAgentStatusStats(accounts, agentsByAccount)
	if total != 4 || disconnected != 2 || partial != 1 {
		t.Fatalf("got total=%d disconnected=%d partial=%d, want 4/2/1", total, disconnected, partial)
	}
}

func TestComputeEventsSummaryStats(t *testing.T) {
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
				"rows": []interface{}{
					map[string]interface{}{"account_id": "acc-1", "event_count": int64(5), "count_priority_high": int64(2)},
					map[string]interface{}{"account_id": "acc-1", "event_count": int64(3), "count_priority_high": int64(0)},
				},
			},
		},
	}

	totalEvents, totalHigh, accountCount := computeEventsSummaryStats(accountList, dailyEventCounts)
	if totalEvents != 8 || totalHigh != 2 || accountCount != 1 {
		t.Fatalf("got events=%d high=%d accounts=%d, want 8/2/1", totalEvents, totalHigh, accountCount)
	}
}

func TestCalculateTotalPotentialSavings(t *testing.T) {
	t.Run("missing rows", func(t *testing.T) {
		if got := calculateTotalPotentialSavings(map[string]interface{}{}); got != 0 {
			t.Fatalf("got %v, want 0", got)
		}
	})
	t.Run("query row shape", func(t *testing.T) {
		data := map[string]interface{}{
			"rows": []query.QueryRow{
				{"sum_estimated_savings": 100.25},
				{"sum_estimated_savings": 435.54},
				{"sum_estimated_savings": "not-a-number"},
			},
		}
		if got := calculateTotalPotentialSavings(data); got != 535.79 {
			t.Fatalf("got %v, want 535.79", got)
		}
	})
	t.Run("json interface shape", func(t *testing.T) {
		data := map[string]interface{}{
			"rows": []interface{}{
				map[string]interface{}{"sum_estimated_savings": 10.0},
				map[string]interface{}{"sum_estimated_savings": 5.5},
			},
		}
		if got := calculateTotalPotentialSavings(data); got != 15.5 {
			t.Fatalf("got %v, want 15.5", got)
		}
	})
}
