package observability

import (
	"log/slog"
	"nudgebee/services/eventrule/playbooks"
	"testing"

	"github.com/stretchr/testify/assert"
)

// These cases moved here with the action itself: it now needs the metrics provider
// lookup, which lives in this package, and playbooks cannot import it.
func TestPodNodeMetricsCanAutoExecute(t *testing.T) {
	ctxFor := func(aggKey, subjectType, name, namespace string) playbooks.PlaybookActionContext {
		return playbooks.NewPlaybookActionContext("t", "a", slog.Default(), playbooks.PlaybookEvent{
			AggregationKey:   aggKey,
			SubjectType:      subjectType,
			SubjectName:      name,
			SubjectNamespace: namespace,
		})
	}

	memory := &podNodeMetricsAction{resourceType: "memory"}
	assert.True(t, memory.CanAutoExecute(ctxFor("pod_oom_killer_enricher", "pod", "p1", "ns")))
	assert.True(t, memory.CanAutoExecute(ctxFor("report_crash_loop", "pod", "p1", "ns")))
	// Only the OOM/crash-loop aggregation keys draw this card.
	assert.False(t, memory.CanAutoExecute(ctxFor("job_failure", "job", "j1", "ns")))
	// Without a pod there is nothing to chart.
	assert.False(t, memory.CanAutoExecute(ctxFor("pod_oom_killer_enricher", "pod", "", "ns")))
	// An action with no resource type configured must never fire.
	assert.False(t, (&podNodeMetricsAction{}).CanAutoExecute(ctxFor("pod_oom_killer_enricher", "pod", "p1", "ns")))
}

// The UI reads this payload verbatim, so the wire shape is a contract: the relay's
// /prometheus facade returned {"<metric>": {"series_list_result": [...]}} with string
// values, and the provider-agnostic path has to produce the same.
func TestSeriesListByQueryKeyMatchesTheRelayWireShape(t *testing.T) {
	out := OutputMetricQuery{Results: []QueryResult{
		{QueryKey: "memory_usage", Payload: []Result{{
			Metric:     map[string]string{"pod": "p1", "namespace": "ns"},
			Timestamps: []int64{1787803200, 1787803500},
			Values:     []float64{1024, 2048},
		}}},
		// A metric the backend could not answer must be absent, not present-and-empty.
		{QueryKey: "memory_limit", Payload: []Result{}},
	}}

	got := seriesListByQueryKey(out)
	assert.NotContains(t, got, "memory_limit")

	usage, ok := got["memory_usage"].(map[string]any)
	assert.True(t, ok)
	series, ok := usage["series_list_result"].([]any)
	assert.True(t, ok)
	assert.Len(t, series, 1)

	first := series[0].(map[string]any)
	assert.Equal(t, map[string]any{"pod": "p1", "namespace": "ns"}, first["metric"])
	assert.Equal(t, []any{"1787803200", "1787803500"}, first["timestamps"])
	// Values are strings, matching what transformToPrometheusValues consumes.
	assert.Equal(t, []any{"1024", "2048"}, first["values"])
}

func TestSeriesListByQueryKeyDropsValuelessSeries(t *testing.T) {
	out := OutputMetricQuery{Results: []QueryResult{
		{QueryKey: "memory_usage", Payload: []Result{{Metric: map[string]string{"pod": "p1"}}}},
	}}
	assert.Empty(t, seriesListByQueryKey(out))
}
