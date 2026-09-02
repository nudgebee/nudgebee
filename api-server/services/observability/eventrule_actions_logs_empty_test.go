package observability

import (
	"testing"
)

// TestBuildLogsActionResponseSkipsEmpty is the regression for an event carrying a
// log card whose entire body was {"data":[]}. An empty log viewer reads as "we
// looked and the service was quiet", when in practice it means nothing was
// collected — a resource shipping no logs, a scope that matched nothing, or
// missing permissions. The cloud_logs action already returned nil in this case,
// so the same query produced a card or no card depending on which provider
// answered.
func TestBuildLogsActionResponseSkipsEmpty(t *testing.T) {
	if got := buildLogsActionResponse(nil, "Logs", nil, 2, nil, nil, nil); got != nil {
		t.Errorf("nil logs produced a response: %+v", got)
	}
	if got := buildLogsActionResponse([]OutputLog{}, "Logs", nil, 2, nil, nil, nil); got != nil {
		t.Errorf("empty logs produced a response: %+v", got)
	}
}

// TestLogsResponseOrNilAvoidsTypedNilInterface pins the trap that makes the skip
// safe. Every caller returns into a playbooks.PlaybookActionResponse interface;
// handing it a nil *LogsActionResponse directly yields a NON-nil interface
// wrapping a nil pointer, so downstream `!= nil` checks pass and the first field
// access panics.
func TestLogsResponseOrNilAvoidsTypedNilInterface(t *testing.T) {
	resp, err := logsResponseOrNil(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != nil {
		t.Errorf("nil response is not a true nil interface: %#v", resp)
	}
	// The naive alternative — `return buildLogsActionResponse(...), nil` — cannot
	// be asserted here: staticcheck rejects the comparison as never true (SA4023),
	// which is itself the proof that a typed nil pointer in an interface is not
	// nil. That is precisely why the conversion exists.
}

// TestLogsResponseOrNilPassesThrough confirms a real response is unaffected.
func TestLogsResponseOrNilPassesThrough(t *testing.T) {
	built := buildLogsActionResponse([]OutputLog{{Message: "boom"}}, "Logs", nil, 2, nil, nil, nil)
	if built == nil {
		t.Fatal("non-empty logs produced no response")
	}
	resp, err := logsResponseOrNil(built)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Error("a real response was dropped")
	}
}
