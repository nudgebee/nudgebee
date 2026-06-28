package core

import (
	"testing"
)

func TestRewooSolverMetricLikeRegex(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "fabricated latency and throughput values match",
			content: "Error Rate 0% · P95 245ms · P99 412ms · Throughput 12.4 req/s",
			want:    true,
		},
		{
			name:    "plain percentage matches",
			content: "The error rate was 0%.",
			want:    true,
		},
		{
			name:    "byte size matches",
			content: "Response payload was 4.2 MB.",
			want:    true,
		},
		{
			name:    "no numeric metric values present",
			content: "P95 and P99 latency are unavailable because the duration-bucket metric returned no data.",
			want:    false,
		},
		{
			name:    "plain number without unit does not match",
			content: "We tried 10 queries and all failed.",
			want:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rewooSolverMetricLikeRegex.MatchString(tc.content)
			if got != tc.want {
				t.Errorf("rewooSolverMetricLikeRegex.MatchString(%q) = %v, want %v", tc.content, got, tc.want)
			}
		})
	}
}

func TestTruncateForLog(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"shorter than limit returned as-is", "hello", 10, "hello"},
		{"exactly at limit returned as-is", "hello", 5, "hello"},
		{"longer than limit truncated with ellipsis", "hello world", 5, "hello..."},
		{"empty string", "", 5, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateForLog(tc.in, tc.n)
			if got != tc.want {
				t.Errorf("truncateForLog(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}
