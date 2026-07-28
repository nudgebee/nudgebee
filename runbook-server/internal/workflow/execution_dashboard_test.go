package workflow

import (
	"strings"
	"testing"
	"time"

	"nudgebee/runbook/internal/model"
)

func TestBuildExecutionQueryAlwaysScopesTenantAndAccount(t *testing.T) {
	query, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{AccountID: "acct-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "nb_tenant_id='tenant-1' AND nb_account_id='acct-1'"
	if query != want {
		t.Fatalf("got %q, want %q", query, want)
	}
}

// The SQL visibility store rejects ORDER BY. If anyone adds sorting, this test
// is the tripwire — see the disabled ORDER BY cases in the integration suite.
func TestBuildExecutionQueryNeverEmitsOrderBy(t *testing.T) {
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	query, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{
		AccountID:    "acct-1",
		StartDate:    &start,
		EndDate:      &end,
		WorkflowIDs:  []string{"wf-1", "wf-2"},
		TriggeredBy:  []string{"user-1"},
		Statuses:     []model.WorkflowExecutionStatus{model.WorkflowExecutionStatusFailed},
		TriggerTypes: []string{"schedule"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(strings.ToUpper(query), "ORDER BY") {
		t.Fatalf("query must not contain ORDER BY, got %q", query)
	}
}

func TestBuildExecutionQueryMultiValueFiltersUseOrGroups(t *testing.T) {
	query, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{
		AccountID:   "acct-1",
		WorkflowIDs: []string{"wf-1", "wf-2"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(query, "(nb_workflow_id='wf-1' OR nb_workflow_id='wf-2')") {
		t.Fatalf("expected OR group, got %q", query)
	}
}

func TestBuildExecutionQuerySingleValueFilterSkipsParentheses(t *testing.T) {
	query, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{
		AccountID:   "acct-1",
		WorkflowIDs: []string{"wf-1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(query, "AND nb_workflow_id='wf-1'") || strings.Contains(query, "(nb_workflow_id") {
		t.Fatalf("expected bare equality clause, got %q", query)
	}
}

func TestBuildExecutionQueryEscapesQuotes(t *testing.T) {
	query, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{
		AccountID:    "acct-1",
		TriggerTypes: []string{"o'brien"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(query, "nb_workflow_trigger='o''brien'") {
		t.Fatalf("expected doubled quote, got %q", query)
	}
}

func TestBuildExecutionQueryFormatsDatesAsUTCRFC3339(t *testing.T) {
	// Deliberately non-UTC: the query must still render in UTC so a browser in
	// another zone can't shift the window.
	zone := time.FixedZone("IST", 5*3600+1800)
	start := time.Date(2026, 7, 27, 5, 30, 0, 0, zone)
	query, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{
		AccountID: "acct-1",
		StartDate: &start,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(query, "StartTime >= '2026-07-27T00:00:00Z'") {
		t.Fatalf("expected UTC RFC3339 start bound, got %q", query)
	}
}

func TestBuildExecutionQueryDropsUnmappedStatuses(t *testing.T) {
	query, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{
		AccountID: "acct-1",
		Statuses:  []model.WorkflowExecutionStatus{model.WorkflowExecutionStatusUnspecified},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(query, "ExecutionStatus") {
		t.Fatalf("unspecified status must not produce a clause, got %q", query)
	}
}

func TestBuildExecutionQueryRejectsOversizedFilters(t *testing.T) {
	ids := make([]string, model.MaxExecutionFilterValues+1)
	for i := range ids {
		ids[i] = "wf"
	}
	if _, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{AccountID: "acct-1", WorkflowIDs: ids}); err == nil {
		t.Fatal("expected an error for an oversized filter")
	}
}

func TestBuildExecutionQueryRequiresTenantAndAccount(t *testing.T) {
	if _, err := buildExecutionQuery("", model.ExecutionDashboardFilter{AccountID: "acct-1"}); err == nil {
		t.Fatal("expected an error when tenantID is empty")
	}
	if _, err := buildExecutionQuery("tenant-1", model.ExecutionDashboardFilter{}); err == nil {
		t.Fatal("expected an error when accountID is empty")
	}
}

func TestIntersectStatuses(t *testing.T) {
	failed := model.FailedExecutionStatuses

	// No user filter → the bucket passes through untouched.
	if got := intersectStatuses(nil, failed); len(got) != len(failed) {
		t.Fatalf("expected pass-through, got %v", got)
	}

	// User filtered to COMPLETED → the failed bucket is empty, which is the
	// honest answer (the table shows no failures either).
	if got := intersectStatuses([]model.WorkflowExecutionStatus{model.WorkflowExecutionStatusCompleted}, failed); len(got) != 0 {
		t.Fatalf("expected empty intersection, got %v", got)
	}

	// Partial overlap keeps only the shared statuses.
	got := intersectStatuses([]model.WorkflowExecutionStatus{
		model.WorkflowExecutionStatusFailed,
		model.WorkflowExecutionStatusRunning,
	}, failed)
	if len(got) != 1 || got[0] != model.WorkflowExecutionStatusFailed {
		t.Fatalf("expected [FAILED], got %v", got)
	}
}

func TestTemporalOrGroupIgnoresEmptyValues(t *testing.T) {
	if got := temporalOrGroup("attr", []string{"", ""}); got != "" {
		t.Fatalf("expected no clause, got %q", got)
	}
	if got := temporalOrGroup("attr", []string{"", "a"}); got != "attr='a'" {
		t.Fatalf("expected single clause, got %q", got)
	}
}

func TestDistinctNonEmpty(t *testing.T) {
	values := []string{"a", "", "b", "a", ""}
	got := distinctNonEmpty(len(values), func(i int) string { return values[i] })
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("expected [a b] in first-seen order, got %v", got)
	}
}
