package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPromQLUnit(t *testing.T) {
	cases := []struct {
		name, query, want string
	}{
		{"cpu seconds under rate are cores", `sum by (pod) (rate(container_cpu_usage_seconds_total{namespace="shop", container!=""}[5m]))`, "cores"},
		{"cluster placeholder and trailing comma as stored", `sum(rate(container_cpu_usage_seconds_total{__CLUSTER__  namespace="shop", pod=~"web-.*", container!="",}[5m]))`, "cores"},
		{"bytes gauge", `sum by (pod, namespace) (container_memory_working_set_bytes{__CLUSTER__  namespace="shop", pod=~"web-.*", container!="",})`, "bytes"},
		{"bytes gauge over time", `max_over_time(container_memory_working_set_bytes{pod="web-1"}[10m])`, "bytes"},
		{"bytes counter under rate", `sum(rate(container_network_receive_bytes_total{pod="web-1"}[5m]))`, "bytes/s"},
		{"count counter under rate filtered by a constant", `sum by (code) (rate(http_requests_total{job="web"}[5m])) > 10`, "per second"},
		{"seconds counter under rate is not a count per second", `rate(node_disk_io_time_weighted_seconds_total{job="node-exporter"}[5m]) > 10`, ""},
		{"other unit-suffixed counter under rate", `rate(node_energy_joules_total[5m])`, ""},
		{"ratio has no unit", `sum(rate(http_requests_total{status=~"5.."}[5m])) / sum(rate(http_requests_total[5m])) > 0.05`, ""},
		{"bytes over bytes is a ratio", `container_memory_working_set_bytes / container_spec_memory_limit_bytes`, ""},
		{"gauge without a unit suffix", `sum(queue_messages_ready{namespace="mq"}) BY (queue, pod) > 100`, ""},
		{"count changes the unit", `count(container_memory_working_set_bytes)`, ""},
		{"increase is not per second", `increase(errors_total[5m]) > 0`, ""},
		{"bool comparison yields 0/1", `rate(container_cpu_usage_seconds_total[5m]) > bool 0.5`, ""},
		{"rate of a gauge", `rate(container_memory_working_set_bytes[5m])`, ""},
		{"not PromQL", `avg:kubernetes.cpu.usage.total{kube_namespace:shop}`, ""},
		{"provider-neutral key", `cpu_usage_by_pod`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, promQLUnit(c.query))
		})
	}
}

func TestMetricCardQuery(t *testing.T) {
	t.Run("PromQL is put on one line with comment lines dropped", func(t *testing.T) {
		query, unit := metricCardQuery(map[string]any{"query": "# per pod\nsum by (pod) (\n  rate(container_cpu_usage_seconds_total{namespace=\"shop\"}[5m])\n)\n"})
		assert.Equal(t, `sum by (pod) ( rate(container_cpu_usage_seconds_total{namespace="shop"}[5m]) )`, query)
		assert.Equal(t, "cores", unit)
	})

	t.Run("long PromQL is bounded", func(t *testing.T) {
		long := `sum by (namespace, pod) (container_memory_working_set_bytes{namespace=~"` + strings.Repeat("team-a|", 80) + `team-b"})`
		query, unit := metricCardQuery(map[string]any{"query": long})
		assert.Equal(t, maxMetricQueryBytes+len("…"), len(query))
		assert.True(t, strings.HasSuffix(query, "…"))
		assert.Equal(t, "bytes", unit, "the unit comes from the full query, not the truncated text")
	})

	t.Run("provider-neutral request is described; its unit comes from utilisationUnit", func(t *testing.T) {
		query, unit := metricCardQuery(map[string]any{"query": map[string]any{
			"kind": "deployment", "metrics": []any{"cpu_usage"}, "workload_name": "web", "workload_namespace": "shop",
		}})
		assert.Equal(t, "metrics=cpu_usage", query)
		assert.Empty(t, unit)

		query, unit = metricCardQuery(map[string]any{"query": map[string]any{"pod_name": "web-1", "namespace": "shop", "resource_type": "memory"}})
		assert.Equal(t, "resource_type=memory", query)
		assert.Empty(t, unit)
	})

	t.Run("nothing to say", func(t *testing.T) {
		for _, md := range []map[string]any{
			nil,
			{"query-result-version": "1.0"},
			{"query": "  \n "},
			{"query": map[string]any{"service_name": "AmazonEC2", "region": "us-east-1"}},
		} {
			query, unit := metricCardQuery(md)
			assert.Empty(t, query, "%v", md)
			assert.Empty(t, unit, "%v", md)
		}
	})
}

func TestUtilisationUnit(t *testing.T) {
	pod := func(rt string) map[string]any {
		return map[string]any{"query": map[string]any{"pod_name": "web-1", "namespace": "shop", "resource_type": rt}}
	}
	workload := func(keys ...any) map[string]any {
		return map[string]any{"query": map[string]any{"kind": "deployment", "metrics": keys, "workload_name": "web", "workload_namespace": "shop"}}
	}
	cases := []struct {
		name     string
		metadata map[string]any
		want     string
	}{
		{"pod cpu", pod("cpu"), "cores"},
		{"pod CPU", pod("CPU"), "cores"},
		{"pod memory", pod("memory"), "bytes"},
		{"pod Memory", pod("Memory"), "bytes"},
		{"pod other resource", pod("disk"), ""},
		{"pod no resource", pod(""), ""},
		{"workload cpu_usage", workload("cpu_usage"), "cores"},
		{"workload memory_usage", workload("memory_usage"), "bytes"},
		{"workload mixed units", workload("cpu_usage", "memory_usage"), ""},
		{"workload unverified key", workload("cpu_usage_by_pod"), ""},
		{"workload verified plus unverified key", workload("cpu_usage", "cpu_throttling"), ""},
		{"workload no keys", workload(), ""},
		{"no metadata", nil, ""},
		{"PromQL string query", map[string]any{"query": "sum(container_memory_working_set_bytes)"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, utilisationUnit(c.metadata))
		})
	}
}

// Realistic cards as the enrichers persist them: the query lives in metadata,
// which the Evidence struct used to drop at unmarshal.
func TestProcessRowWithMessages_MetricCardsCarryQuery(t *testing.T) {
	promQL := "sum by (pod) (\n  rate(container_cpu_usage_seconds_total{namespace=\"shop\", container!=\"\"}[5m])\n)"
	metricsData, _ := json.Marshal(map[string]any{"data": map[string]any{"results": []any{map[string]any{
		"query_key": "cpu_usage",
		"payload":   []any{map[string]any{"metric": map[string]any{}, "timestamps": []any{1790473680, 1790473740}, "values": []any{0.21, 0.35}}},
		"query":     `sum(rate(container_cpu_usage_seconds_total{namespace="shop", pod=~"web-.*"}[5m]))`,
	}}}})
	podData, _ := json.Marshal(map[string]any{"name": "pod_metric", "resource_type": "memory", "data": []any{map[string]any{
		"metric":     map[string]any{"container": "app", "pod": "web-1", "namespace": "shop"},
		"timestamps": []any{1790473680, 1790473740}, "values": []any{"30973952", "210489344"},
	}}})
	kubectlData, _ := json.Marshal(map[string]any{"items": []any{}})
	row := buildEvidencesRow(t, []map[string]any{
		{
			"type": "prometheus",
			"data": map[string]any{"result_type": "matrix", "series_list_result": []any{map[string]any{
				"metric": map[string]any{"pod": "web-1"}, "values": []any{"0.2", "0.9"}, "timestamps": []any{1790473680, 1790473740},
			}}},
			"metadata":        map[string]any{"query": promQL, "query-result-version": "1.0"},
			"additional_info": map[string]any{"action_name": "prometheus_enricher"},
		},
		{
			"type":            "json",
			"data":            string(metricsData),
			"metadata":        map[string]any{"query": map[string]any{"kind": "deployment", "metrics": []any{"cpu_usage"}, "workload_name": "web", "workload_namespace": "shop"}},
			"additional_info": map[string]any{"action_name": "metrics"},
		},
		{
			"type":            "json",
			"data":            string(podData),
			"metadata":        map[string]any{"query": map[string]any{"pod_name": "web-1", "namespace": "shop", "resource_type": "memory"}},
			"additional_info": map[string]any{"action_name": "pod_metric_enricher"},
		},
		{
			// Not a metric card: its request also has a resource_type.
			"type":            "json",
			"data":            string(kubectlData),
			"metadata":        map[string]any{"query": map[string]any{"resource_type": "pods", "namespace": "shop"}},
			"additional_info": map[string]any{"action_name": "k8s_resource"},
		},
	})
	ev := processedEvidence(t, EventsExecuteTool{}.processRowWithMessages(row, 0, 1, nil))

	if assert.Len(t, ev.MetricsData, 1) {
		assert.Equal(t, `sum by (pod) ( rate(container_cpu_usage_seconds_total{namespace="shop", container!=""}[5m]) )`, ev.MetricsData[0].Query)
		assert.Equal(t, "cores", ev.MetricsData[0].Unit)
	}
	if assert.Len(t, ev.PodMetrics, 1) {
		assert.Equal(t, "resource_type=memory", ev.PodMetrics[0].Query)
		assert.Equal(t, "bytes", ev.PodMetrics[0].Unit)
	}
	if assert.Len(t, ev.Others, 2) {
		assert.Equal(t, "metrics=cpu_usage", ev.Others[0].Query)
		assert.Equal(t, "cores", ev.Others[0].Unit)
		assert.Empty(t, ev.Others[1].Query, "non-metric cards get no query")
		assert.Empty(t, ev.Others[1].Unit, "non-metric cards get no unit")
	}

	// get_event_by_id caps each card's data; the query sits beside it, so it survives.
	capInvestigateDataEvidence(&ev)
	out, err := json.Marshal(ev)
	if assert.NoError(t, err) {
		assert.Equal(t, 2, strings.Count(string(out), `"unit":"cores"`))
		assert.Equal(t, 1, strings.Count(string(out), `"query":"resource_type=memory"`))
	}
}
