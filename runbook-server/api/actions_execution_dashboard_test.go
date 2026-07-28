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

// The dashboard is tenant-level now (#35113): no account means "every account
// the caller can read", which ResolveReadableAccounts fills in downstream. The
// parser must therefore accept an absent account rather than reject it -- but
// it must not invent one either, or the scope check would be bypassed.
func TestParseExecutionDashboardFilterAllowsNoAccount(t *testing.T) {
	for name, args := range map[string]map[string]any{
		"missing": {},
		"empty":   {"account_id": ""},
	} {
		filter, err := parseExecutionDashboardFilter(args)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if len(filter.AccountIDs) != 0 {
			t.Fatalf("%s: expected no accounts, got %v", name, filter.AccountIDs)
		}
	}
}

func TestParseExecutionDashboardFilterReadsAccountFilter(t *testing.T) {
	// The multi-select filter sends account_ids...
	filter, err := parseExecutionDashboardFilter(map[string]any{"account_ids": []any{"acct-1", "acct-2"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(filter.AccountIDs) != 2 || filter.AccountIDs[0] != "acct-1" || filter.AccountIDs[1] != "acct-2" {
		t.Fatalf("unexpected accounts: %v", filter.AccountIDs)
	}

	// ...while pre-#35113 deep links still send a single account_id.
	filter, err = parseExecutionDashboardFilter(map[string]any{"account_id": "acct-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(filter.AccountIDs) != 1 || filter.AccountIDs[0] != "acct-1" {
		t.Fatalf("unexpected accounts: %v", filter.AccountIDs)
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
