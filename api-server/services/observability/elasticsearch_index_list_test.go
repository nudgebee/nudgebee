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

// TestCollapseConcreteIndexTargets_LogsPicker reproduces the reported bug input:
// six rolled-over backing-index generations of one logical log stream, a metrics
// backing index, and system streams. The logs picker must show exactly one stable
// stream name and never the metrics stream or ".ds-*" rows.
func TestCollapseConcreteIndexTargets_LogsPicker(t *testing.T) {
	indices := catIndices(
		".ds-logs-generic.otel-default-2026.06.18-000004",
		".ds-logs-generic.otel-default-2026.06.19-000006",
		".ds-logs-generic.otel-default-2026.06.18-000005",
		".ds-logs-generic.otel-default-2026.06.17-000003",
		".ds-logs-generic.otel-default-2026.06.15-000001",
		".ds-logs-generic.otel-default-2026.06.15-000002",
		".ds-metrics-kubeletstatsreceiver.otel-default-2026.06.17-000001", // other type -> excluded
		".ds-ilm-history-7-2026.06.10-000001",                             // system stream -> excluded (not logs-)
		".geoip_databases",                                                // system index -> excluded
	)
	got := collapseConcreteIndexTargets("logs", indices)
	assert.Equal(t, []string{"logs-generic.otel-default"}, got)
}

func TestCollapseConcreteIndexTargets_MetricsPicker(t *testing.T) {
	indices := catIndices(
		".ds-metrics-kubeletstatsreceiver.otel-default-2026.06.17-000001",
		".ds-logs-generic.otel-default-2026.06.18-000004", // other type -> excluded
	)
	got := collapseConcreteIndexTargets("metrics", indices)
	assert.Equal(t, []string{"metrics-kubeletstatsreceiver.otel-default"}, got)
}

// TestCollapseConcreteIndexTargets_LegacyPassthrough guards that pre-data-stream
// (e.g. Fluent-Bit) deployments, whose data lives in plain non-".ds" indices,
// still get their indices listed and system indices are still excluded.
func TestCollapseConcreteIndexTargets_LegacyPassthrough(t *testing.T) {
	indices := catIndices(
		"nudgebee-agent",
		"iteration-prod",
		"nudgebee-agent",       // duplicate -> deduped
		".kibana_task_manager", // system -> excluded
	)
	got := collapseConcreteIndexTargets("logs", indices)
	assert.Equal(t, []string{"nudgebee-agent", "iteration-prod"}, got)
}

func TestCollapseConcreteIndexTargets_Empty(t *testing.T) {
	assert.Empty(t, collapseConcreteIndexTargets("logs", nil))
}
