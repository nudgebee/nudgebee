package integrations

import (
	"testing"

	"nudgebee/services/event"
)

func TestMapGCPSeverityToPriority(t *testing.T) {
	cases := []struct {
		in   string
		want event.EventPriority
	}{
		// GCP native policy severities
		{"CRITICAL", event.EventPriorityHigh},
		{"ERROR", event.EventPriorityHigh},
		{"WARNING", event.EventPriorityMedium},
		// severity user-label style values
		{"high", event.EventPriorityHigh},
		{"medium", event.EventPriorityMedium},
		{"low", event.EventPriorityLow},
		{"info", event.EventPriorityInfo},
		{"informational", event.EventPriorityInfo},
		// case/whitespace insensitivity
		{"  Critical ", event.EventPriorityHigh},
		// unset / unknown must default to MEDIUM, not HIGH — an alert with no
		// configured severity is still a real alert but should not flood HIGH.
		{"", event.EventPriorityMedium},
		{"SEVERITY_UNSPECIFIED", event.EventPriorityMedium},
		{"garbage", event.EventPriorityMedium},
	}
	for _, c := range cases {
		if got := mapGCPSeverityToPriority(c.in); got != c.want {
			t.Errorf("mapGCPSeverityToPriority(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
