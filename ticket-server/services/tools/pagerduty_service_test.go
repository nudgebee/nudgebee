package tools

import "testing"

func TestNormalizePagerDutyUrgency(t *testing.T) {
	cases := map[string]string{
		"high":   "high",
		"High":   "high",
		"low":    "low",
		"Low":    "low",
		"0":      "low",
		"medium": "high", // PagerDuty has no medium — treated as high
		"":       "",     // empty defers to the service default
	}
	for in, want := range cases {
		if got := normalizePagerDutyUrgency(in); got != want {
			t.Errorf("normalizePagerDutyUrgency(%q) = %q, want %q", in, got, want)
		}
	}
}
