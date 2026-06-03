package tools

import (
	"testing"

	"nudgebee/tickets-server/clients"
)

// The ZenDuty create-meta urgency field emits option values "low"/"medium"/"high"
// (the severity source). CreateZenDutyIncident maps ticket.Severity via
// MapUrgencyFromString, so the option value must round-trip to a distinct urgency.
// This guards the Pillar-B contract that "loads" also "applies" for ZenDuty.
func TestZenDutyUrgencyValuesRoundTrip(t *testing.T) {
	cases := map[string]int{
		"low":    clients.ZenDutyUrgencyLow,
		"medium": clients.ZenDutyUrgencyMedium,
		"high":   clients.ZenDutyUrgencyHigh,
	}
	for value, want := range cases {
		if got := clients.MapUrgencyFromString(value); got != want {
			t.Errorf("MapUrgencyFromString(%q) = %d, want %d", value, got, want)
		}
	}
}
