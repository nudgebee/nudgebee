package tools

import (
	"testing"

	"nudgebee/tickets-server/clients"
)

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
