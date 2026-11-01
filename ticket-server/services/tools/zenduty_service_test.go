package tools

import (
	"errors"
	"net/http"
	"testing"

	"nudgebee/tickets-server/clients"
)

// A deleted ZenDuty incident (404) must be recognized as not-found so Get()
// can wrap it in ticket.ErrNotFound — the ticket-status sync job relies on
// that to skip it without counting toward its consecutive-failure abort
// guard.
func TestZenDutyIsNotFound(t *testing.T) {
	if !zenDutyIsNotFound(clients.ZenDutyStatusError{StatusCode: http.StatusNotFound}) {
		t.Error("zenDutyIsNotFound(404) = false, want true")
	}
	if zenDutyIsNotFound(clients.ZenDutyStatusError{StatusCode: http.StatusForbidden}) {
		t.Error("zenDutyIsNotFound(403) = true, want false")
	}
	if zenDutyIsNotFound(errors.New("boom")) {
		t.Error("zenDutyIsNotFound(generic error) = true, want false")
	}
}

// ZenDuty urgency is binary (0 = Low, 1 = High), so the create-meta severity
// options ("low"/"medium"/"high") collapse: "low" maps to Low, everything else
// maps to High. Guards against reintroducing the invented 3-level scale that
// sent ZenDuty an invalid urgency=2 for high-severity tickets.
func TestZenDutyUrgencyValuesRoundTrip(t *testing.T) {
	cases := map[string]int{
		"low":     clients.ZenDutyUrgencyLow,
		"medium":  clients.ZenDutyUrgencyHigh,
		"high":    clients.ZenDutyUrgencyHigh,
		"unknown": clients.ZenDutyUrgencyHigh,
		"":        clients.ZenDutyUrgencyHigh,
	}
	for value, want := range cases {
		if got := clients.MapUrgencyFromString(value); got != want {
			t.Errorf("MapUrgencyFromString(%q) = %d, want %d", value, got, want)
		}
	}
}
