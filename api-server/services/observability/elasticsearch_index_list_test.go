package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBackingIndexStream(t *testing.T) {
	cases := map[string]string{
		".ds-logs-generic.otel-default-2026.06.18-000004":                 "logs-generic.otel-default",
		".ds-metrics-kubeletstatsreceiver.otel-default-2026.06.17-000001": "metrics-kubeletstatsreceiver.otel-default",
		".ds-logs-generic.otel-default-2026.06.15-000002":                 "logs-generic.otel-default",
		"logs-generic.otel-default":                                       "", // already a data-stream name, not a backing index
		"nudgebee-agent":                                                  "", // legacy concrete index
		".kibana_1":                                                       "", // system index, not ".ds-"
	}
	for in, want := range cases {
		assert.Equal(t, want, backingIndexStream(in), "input %q", in)
	}
}

func TestFilterDataStreamNames(t *testing.T) {
	parsed := dataStreamsResponse{}
	parsed.DataStreams = []struct {
		Name string `json:"name"`
	}{
		{Name: "logs-generic.otel-default"},
		{Name: ".logs-deprecation.elasticsearch-default"}, // system stream -> dropped
		{Name: "logs-generic.otel-default"},               // duplicate -> deduped
		{Name: ""},                                        // empty -> dropped
	}
	got := filterDataStreamNames(parsed)
	assert.Equal(t, []string{"logs-generic.otel-default"}, got)
}

// catIndices is the _cat/indices?format=json shape this product sees in a
// data-stream (OTel) deployment: every concrete index is a hidden ".ds-*"
// backing index, plus system data streams.
func catIndices(names ...string) []map[string]any {
	out := make([]map[string]any, 0, len(names))
	for _, n := range names {
		out = append(out, map[string]any{"index": n})
	}
	return out
}

// TestCollapseAllConcreteIndexTargets checks the unfiltered variant backing the
// per-account index picker: every ".ds-*" backing index collapses to its parent
// stream regardless of signal type, legacy indices pass through, dot-prefixed
// system indices drop, and duplicates dedupe.
func TestCollapseAllConcreteIndexTargets(t *testing.T) {
	indices := catIndices(
		".ds-logs-generic.otel-default-2026.06.18-000004",
		".ds-logs-generic.otel-default-2026.06.19-000006",                 // same stream -> deduped
		".ds-metrics-kubeletstatsreceiver.otel-default-2026.06.17-000001", // kept (no type filter)
		".ds-traces-generic.otel-default-2026.06.17-000001",               // kept
		"nudgebee-agent",   // legacy passthrough
		"nudgebee-agent",   // duplicate -> deduped
		".geoip_databases", // system concrete -> dropped
	)
	got := collapseAllConcreteIndexTargets(indices)
	assert.Equal(t, []string{
		"logs-generic.otel-default",
		"metrics-kubeletstatsreceiver.otel-default",
		"traces-generic.otel-default",
		"nudgebee-agent",
	}, got)
}
