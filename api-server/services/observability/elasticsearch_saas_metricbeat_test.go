package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A Metricbeat 8.x kubernetes/pod document, trimmed to the fields that matter.
// Values are nested under kubernetes.* and there is no name/value pair — the
// shape that used to parse as a single all-zero series.
const metricbeatPodResponse = `{
  "hits": {"total": {"value": 2}, "hits": [
    {"_source": {
      "@timestamp": "2026-08-05T09:20:34.224Z",
      "metricset": {"name": "pod", "period": 30000},
      "event": {"module": "kubernetes", "dataset": "kubernetes.pod"},
      "agent": {"type": "metricbeat", "version": "8.19.11"},
      "kubernetes": {
        "namespace": "nudgebee",
        "node": {"name": "gke-node-1", "labels": {"noisy": "dropped"}},
        "labels": {"app_kubernetes_io/name": "dropped-too"},
        "pod": {
          "name": "services-server-f684f975d-nktkz",
          "cpu": {"usage": {"nanocores": 2156882, "node": {"pct": 0.000216}}},
          "memory": {"working_set": {"bytes": 179466240}}
        }
      }
    }},
    {"_source": {
      "@timestamp": "2026-08-05T09:21:04.224Z",
      "metricset": {"name": "pod"},
      "agent": {"type": "metricbeat"},
      "kubernetes": {
        "namespace": "nudgebee",
        "node": {"name": "gke-node-1"},
        "pod": {
          "name": "services-server-f684f975d-nktkz",
          "cpu": {"usage": {"nanocores": 3100000, "node": {"pct": 0.00031}}},
          "memory": {"working_set": {"bytes": 180000000}}
        }
      }
    }}
  ]}
}`

func seriesByName(t *testing.T, results []Result, name string) Result {
	t.Helper()
	for _, r := range results {
		if r.Metric["__name__"] == name {
			return r
		}
	}
	t.Fatalf("no series named %q (have %d series)", name, len(results))
	return Result{}
}

func TestParseESMetricsHits_MetricbeatEmitsSeriesPerNumericLeaf(t *testing.T) {
	results, err := parseESMetricsHits([]byte(metricbeatPodResponse))
	require.NoError(t, err)

	// One series per numeric leaf: cpu.usage.nanocores, cpu.usage.node.pct,
	// memory.working_set.bytes.
	assert.Len(t, results, 3)

	cpu := seriesByName(t, results, "kubernetes.pod.cpu.usage.nanocores")
	assert.Equal(t, []float64{2156882, 3100000}, cpu.Values,
		"values come from the nested leaf, not a top-level `value` key")
	assert.Equal(t, []int64{1785921634, 1785921664}, cpu.Timestamps)

	mem := seriesByName(t, results, "kubernetes.pod.memory.working_set.bytes")
	assert.Equal(t, []float64{179466240, 180000000}, mem.Values)

	pct := seriesByName(t, results, "kubernetes.pod.cpu.usage.node.pct")
	assert.Equal(t, []float64{0.000216, 0.00031}, pct.Values)
}

func TestParseESMetricsHits_MetricbeatLabelsFromNestedIdentityFields(t *testing.T) {
	results, err := parseESMetricsHits([]byte(metricbeatPodResponse))
	require.NoError(t, err)

	cpu := seriesByName(t, results, "kubernetes.pod.cpu.usage.nanocores")
	assert.Equal(t, "nudgebee", cpu.Metric["kubernetes.namespace"])
	assert.Equal(t, "services-server-f684f975d-nktkz", cpu.Metric["kubernetes.pod.name"])
	assert.Equal(t, "gke-node-1", cpu.Metric["kubernetes.node.name"])

	// Per-pod/node/namespace constants repeated on every document are dropped.
	assert.NotContains(t, cpu.Metric, "kubernetes.labels.app_kubernetes_io/name")
	assert.NotContains(t, cpu.Metric, "kubernetes.node.labels.noisy")
}

// The regression this whole change exists for: a document carrying no
// recognisable value used to be charted as a real reading of 0.
func TestParseESMetricsHits_DropsHitsWithNoValueInsteadOfChartingZero(t *testing.T) {
	const noValue = `{"hits": {"hits": [
      {"_source": {"@timestamp": "2026-08-05T09:20:34.224Z", "name": "some.metric",
                   "attributes": {"pod": "p1"}}}
    ]}}`

	results, err := parseESMetricsHits([]byte(noValue))
	require.NoError(t, err)
	assert.Empty(t, results, "no value key present => no series, not a zero-valued one")
}

// A value key that is genuinely 0 is a real reading and must survive.
func TestParseESMetricsHits_KeepsExplicitZeroValue(t *testing.T) {
	const explicitZero = `{"hits": {"hits": [
      {"_source": {"@timestamp": "2026-08-05T09:20:34.224Z", "name": "some.metric",
                   "value": 0, "attributes": {"pod": "p1"}}}
    ]}}`

	results, err := parseESMetricsHits([]byte(explicitZero))
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, []float64{0}, results[0].Values)
}

// OTel-native documents must keep their existing branch — isBeatsDoc has to not
// steal them, even though they also carry k8s dimensions.
func TestParseESMetricsHits_OTelNativeUnaffected(t *testing.T) {
	const otelDoc = `{"hits": {"hits": [
      {"_source": {"@timestamp": "2026-08-05T09:20:34.224Z",
                   "metrics": {"container.cpu.usage": 0.0066},
                   "resource": {"attributes": {"k8s.pod.name": "p1"}}}}
    ]}}`

	results, err := parseESMetricsHits([]byte(otelDoc))
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "container.cpu.usage", results[0].Metric["__name__"])
	assert.Equal(t, "p1", results[0].Metric["k8s.pod.name"])
	assert.Equal(t, []float64{0.0066}, results[0].Values)
}

// `_source` filtering is the documented way to narrow a Metricbeat query to one
// metric, and it strips `metricset`/`agent`. Dispatch must therefore key off the
// document's shape, not off shipper-identifying fields — detecting by shipper
// made every _source-filtered query return nothing.
func TestParseESMetricsHits_MetricbeatSurvivesSourceFiltering(t *testing.T) {
	const sourceFiltered = `{"hits": {"hits": [
      {"_source": {
        "@timestamp": "2026-08-05T09:20:34.224Z",
        "kubernetes": {"namespace": "nudgebee", "pod": {
          "name": "services-server-f684f975d-nktkz",
          "cpu": {"usage": {"nanocores": 2156882}}}}
      }}
    ]}}`

	results, err := parseESMetricsHits([]byte(sourceFiltered))
	require.NoError(t, err)
	require.Len(t, results, 1, "_source filtering yields exactly the one requested metric")
	assert.Equal(t, "kubernetes.pod.cpu.usage.nanocores", results[0].Metric["__name__"])
	assert.Equal(t, []float64{2156882}, results[0].Values)
}

// A Beat can also be configured to emit the flat OTLP shape (name/value). Those
// documents must keep using the flat branch — dispatching on "was written by a
// Beat" stole them and dropped every one.
func TestParseESMetricsHits_BeatsWrittenFlatDocUsesFlatBranch(t *testing.T) {
	const reshaped = `{"hits": {"hits": [
      {"_source": {
        "@timestamp": "2026-08-05T09:20:34.224Z",
        "agent": {"type": "metricbeat", "version": "8.19.11"},
        "name": "container.cpu.usage",
        "value": 0.0066,
        "attributes": {"pod": "services-server-f684f975d-nktkz"}
      }}
    ]}}`

	results, err := parseESMetricsHits([]byte(reshaped))
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "container.cpu.usage", results[0].Metric["__name__"])
	assert.Equal(t, []float64{0.0066}, results[0].Values)
}

// Precedence: an explicit name/value pair wins over nested kubernetes leaves,
// so a document carrying both is read once, by the flat branch.
func TestParseESMetricsHits_FlatShapeWinsOverNestedLeaves(t *testing.T) {
	const both = `{"hits": {"hits": [
      {"_source": {
        "@timestamp": "2026-08-05T09:20:34.224Z",
        "name": "container.cpu.usage", "value": 0.0066,
        "kubernetes": {"pod": {"name": "p1", "cpu": {"usage": {"nanocores": 2156882}}}}
      }}
    ]}}`

	results, err := parseESMetricsHits([]byte(both))
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "container.cpu.usage", results[0].Metric["__name__"])
	assert.Equal(t, []float64{0.0066}, results[0].Values)
}
