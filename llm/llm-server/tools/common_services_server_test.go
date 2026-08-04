package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestClampLogLimitForProvider pins the per-provider limit cap added 2026-06-22
// after a 30-day production sample showed 12 fetch_logs failures matching
// `loki: limit exceeds maximum of 5000`. The clamp lives in the tool layer so
// any caller (current agent prompt, a future integration, a raw API hit) gets
// a capped fetch instead of an opaque backend error.
func TestClampLogLimitForProvider(t *testing.T) {
	cases := []struct {
		name        string
		provider    string
		requested   int
		wantLimit   int
		wantClamped bool
	}{
		{
			name:        "loki at cap — no clamp, returns same",
			provider:    "loki",
			requested:   lokiMaxLogLimit,
			wantLimit:   lokiMaxLogLimit,
			wantClamped: false,
		},
		{
			name:        "loki above cap — clamps to cap",
			provider:    "loki",
			requested:   10000,
			wantLimit:   lokiMaxLogLimit,
			wantClamped: true,
		},
		{
			name:        "loki at 5001 — clamps to cap",
			provider:    "loki",
			requested:   lokiMaxLogLimit + 1,
			wantLimit:   lokiMaxLogLimit,
			wantClamped: true,
		},
		{
			name:        "loki well below cap — no clamp",
			provider:    "loki",
			requested:   1000,
			wantLimit:   1000,
			wantClamped: false,
		},
		{
			name:        "case-insensitive provider match",
			provider:    "Loki",
			requested:   9999,
			wantLimit:   lokiMaxLogLimit,
			wantClamped: true,
		},
		{
			name:        "whitespace-tolerant provider match (Gemini #32847 review)",
			provider:    "  loki  ",
			requested:   9999,
			wantLimit:   lokiMaxLogLimit,
			wantClamped: true,
		},
		{
			name:        "unknown provider — passes through unchanged, no warning fires",
			provider:    "elasticsearch",
			requested:   10000,
			wantLimit:   10000,
			wantClamped: false,
		},
		{
			name:        "empty provider — passes through unchanged",
			provider:    "",
			requested:   10000,
			wantLimit:   10000,
			wantClamped: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, clamped := clampLogLimitForProvider(c.provider, c.requested)
			assert.Equal(t, c.wantLimit, got)
			assert.Equal(t, c.wantClamped, clamped)
		})
	}
}

// TestCloudProviderToObservabilityFallback pins the pure mapping used by the
// Get{Metrics,Log,Trace}Provider CLI fallback: only GCP/Azure cloud accounts
// route to a cloud-CLI observability backend; AWS and unknowns keep the
// existing prometheus/k8s/clickhouse defaults. Case and surrounding whitespace
// in cloud_accounts.cloud_provider must not defeat the match.
func TestCloudProviderToObservabilityFallback(t *testing.T) {
	cases := []struct {
		name          string
		cloudProvider string
		want          string
	}{
		{"gcp lowercase", "gcp", "gcp"},
		{"gcp uppercase", "GCP", "gcp"},
		{"gcp padded", "  gcp ", "gcp"},
		{"azure lowercase", "azure", "azure"},
		{"azure mixed case", "Azure", "azure"},
		{"aws excluded", "aws", ""},
		{"empty", "", ""},
		{"unknown", "digitalocean", ""},
		{"gke is not a cloud_provider value", "gke", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, cloudProviderToObservabilityFallback(c.cloudProvider))
		})
	}
}
