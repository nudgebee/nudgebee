package api

import (
	"testing"
	"time"

	"nudgebee/runbook/internal/model"
)

func TestParseStringSlice(t *testing.T) {
	// The gateway sends JSON arrays, which land as []any.
	if got := parseStringSlice([]any{"a", "b"}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("expected [a b], got %v", got)
	}
	// A bare string is tolerated so single-value callers don't have to wrap.
	if got := parseStringSlice("a"); len(got) != 1 || got[0] != "a" {
		t.Fatalf("expected [a], got %v", got)
	}
	// Non-strings and empties are dropped rather than failing the request.
	if got := parseStringSlice([]any{"a", 1, "", nil}); len(got) != 1 || got[0] != "a" {
		t.Fatalf("expected [a], got %v", got)
	}
	if got := parseStringSlice(""); got != nil {
		t.Fatalf("expected nil for an empty string, got %v", got)
	}
	if got := parseStringSlice(nil); got != nil {
		t.Fatalf("expected nil for a missing arg, got %v", got)
	}
}

func TestParseExecutionDashboardFilterRequiresAccountID(t *testing.T) {
	if _, err := parseExecutionDashboardFilter(map[string]any{}); err == nil {
		t.Fatal("expected an error when account_id is missing")
	}
	if _, err := parseExecutionDashboardFilter(map[string]any{"account_id": ""}); err == nil {
		t.Fatal("expected an error when account_id is empty")
	}
}

func TestParseExecutionDashboardFilterReadsEveryFilter(t *testing.T) {
	filter, err := parseExecutionDashboardFilter(map[string]any{
		"account_id":    "acct-1",
		"workflow_ids":  []any{"wf-1", "wf-2"},
		"triggered_by":  []any{"user-1"},
		"statuses":      []any{"FAILED", "TIMED_OUT"},
		"trigger_types": []any{"schedule"},
		"start_date":    "2026-07-20T00:00:00Z",
		"end_date":      "2026-07-27T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(filter.WorkflowIDs) != 2 || len(filter.TriggeredBy) != 1 || len(filter.TriggerTypes) != 1 {
		t.Fatalf("unexpected filter values: %+v", filter)
	}
	if len(filter.Statuses) != 2 || filter.Statuses[0] != model.WorkflowExecutionStatusFailed {
		t.Fatalf("unexpected statuses: %v", filter.Statuses)
	}
	if filter.StartDate == nil || !filter.StartDate.Equal(time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected start date: %v", filter.StartDate)
	}
	if filter.EndDate == nil || !filter.EndDate.Equal(time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected end date: %v", filter.EndDate)
	}
}

func TestParseExecutionDashboardFilterRejectsBadTimestamps(t *testing.T) {
	if _, err := parseExecutionDashboardFilter(map[string]any{"account_id": "acct-1", "start_date": "yesterday"}); err == nil {
		t.Fatal("expected an error for an unparseable start_date")
	}
	if _, err := parseExecutionDashboardFilter(map[string]any{"account_id": "acct-1", "end_date": "yesterday"}); err == nil {
		t.Fatal("expected an error for an unparseable end_date")
	}
}

func TestParseExecutionDashboardFilterOmitsAbsentDates(t *testing.T) {
	filter, err := parseExecutionDashboardFilter(map[string]any{"account_id": "acct-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filter.StartDate != nil || filter.EndDate != nil {
		t.Fatalf("expected nil date bounds, got %v / %v", filter.StartDate, filter.EndDate)
	}
}
