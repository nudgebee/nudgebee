package api

import (
	"fmt"
	"strings"
	"testing"

	"nudgebee/llm/events"
)

func TestRenderMetricDigest(t *testing.T) {
	t.Run("cloudwatch items shape", func(t *testing.T) {
		ev := events.InvestigateData{Others: []events.InvestigateDataInsight{{
			Data: `{"items":[{"name":"CPUUtilization","statistics":"Average","resource_id":"janus",` +
				`"values":[81.67,98.63,92.64],"timestamps":["2026-09-14T09:34:00Z","2026-09-14T10:09:00Z","2026-09-14T10:29:00Z"]}]}`,
		}}}
		got := renderMetricDigest(ev)
		for _, want := range []string{"CPUUtilization Average janus", "3 points 09-14 09:34→09-14 10:29", "first 81.670", "max 98.630 @09-14 10:09", "last 92.640"} {
			if !strings.Contains(got, want) {
				t.Errorf("digest missing %q:\n%s", want, got)
			}
		}
	})

	t.Run("metric_groups shape", func(t *testing.T) {
		ev := events.InvestigateData{MetricsData: []events.InvestigateDataInsight{{
			Data: `{"result_type":"matrix","metric_groups":{"CPUUtilization (Maximum)":{"series_list_result":[` +
				`{"metric":{"name":"CPUUtilization","statistics":"Maximum","resource_id":"billing-server"},` +
				`"values":[98.7,100.1],"timestamps":[1789382040,1789382100]}]}}}`,
		}}}
		got := renderMetricDigest(ev)
		if !strings.Contains(got, "CPUUtilization Maximum billing-server") || !strings.Contains(got, "2 points") {
			t.Errorf("unexpected digest:\n%s", got)
		}
	})

	t.Run("series repeated at top level (aws evidence) are rendered once, oldest first", func(t *testing.T) {
		series := `{"metric":{"name":"CPUUtilization","statistics":"Maximum","resource_id":"billing-server"},"values":[99.8,97.1,98.7],"timestamps":[1789381980,1789381920,1789377840]}`
		ev := events.InvestigateData{MetricsData: []events.InvestigateDataInsight{{
			Data: `{"metric_groups":{"CPUUtilization (Maximum)":{"series_list_result":[` + series + `]}},"series_list_result":[` + series + `]}`,
		}}}
		got := renderMetricDigest(ev)
		if strings.Count(got, "CPUUtilization") != 1 {
			t.Errorf("expected the repeated series once, got:\n%s", got)
		}
		if !strings.Contains(got, "first 98.700") || !strings.Contains(got, "last 99.800") {
			t.Errorf("expected points sorted by time (first 98.7 at the earliest timestamp), got:\n%s", got)
		}
	})

	t.Run("kubernetes pod metrics with string values and unix seconds", func(t *testing.T) {
		ev := events.InvestigateData{PodMetrics: []events.InvestigateDataInsight{{
			Data: `{"data":{"memory_usage":{"series_list_result":[{"metric":{"__name__":"container_memory_working_set_bytes","pod":"services-server-6pl88"},` +
				`"values":["91922432","355463168","57880576"],"timestamps":[1787408040,1787410470,1787411640]}]}}}`,
		}}}
		got := renderMetricDigest(ev)
		if !strings.Contains(got, "memory_usage container_memory_working_set_bytes services-server-6pl88") || !strings.Contains(got, "max 355.46e6") {
			t.Errorf("unexpected digest:\n%s", got)
		}
	})

	t.Run("non-metric evidence and mismatched arrays are ignored", func(t *testing.T) {
		ev := events.InvestigateData{
			Others: []events.InvestigateDataInsight{
				{Data: `{"data":[{"labels":{"@ptr":"x"},"message":"GET /health 499"}]}`},
				{Data: `{"values":[1,2],"timestamps":["2026-09-14T09:34:00Z"]}`},
				{Data: "plain text"},
			},
		}
		if got := renderMetricDigest(ev); got != "" {
			t.Errorf("expected no digest, got:\n%s", got)
		}
	})

	t.Run("series count is bounded", func(t *testing.T) {
		var list []events.InvestigateDataInsight
		for i := 0; i < maxMetricDigestSeries+5; i++ {
			list = append(list, events.InvestigateDataInsight{Data: map[string]any{
				"name": "m" + strings.Repeat("x", i), "values": []any{float64(i)}, "timestamps": []any{"2026-09-14T09:34:00Z"},
			}})
		}
		got := renderMetricDigest(events.InvestigateData{MetricsData: list})
		if lines := strings.Count(got, "\n- ") + 1; lines > maxMetricDigestSeries+1 {
			t.Errorf("expected at most %d lines, got %d:\n%s", maxMetricDigestSeries+1, lines, got)
		}
		if !strings.Contains(got, "- … 5 more series in the structured evidence file") {
			t.Errorf("expected overflow note, got:\n%s", got)
		}
	})

	t.Run("digest appears in the investigation evidence context", func(t *testing.T) {
		ev := events.InvestigateData{Others: []events.InvestigateDataInsight{{
			Insight: []events.Insight{{Message: "Fetch metric data on alarm"}},
			Data:    `{"items":[{"name":"WriteIOPS","values":[7047.9],"timestamps":["2026-09-13T01:10:00Z"]}]}`,
		}}}
		got := buildInvestigationEvidenceContext(ev)
		if !strings.Contains(got, "### Metric Values") || !strings.Contains(got, "WriteIOPS") {
			t.Errorf("expected metric values section, got:\n%s", got)
		}
	})
}

// Cards as the events tool maps them: series from rate()/sum() carry no __name__,
// so the card's query is the only thing saying what they measure.
func TestRenderMetricDigest_StatesEachCardsQueryOnce(t *testing.T) {
	rateQuery := `sum by (pod) ( rate(container_cpu_usage_seconds_total{namespace="shop", container!=""}[5m]) )`
	ev := events.InvestigateData{
		MetricsData: []events.InvestigateDataInsight{{
			// prometheus_enricher: two pods, no __name__.
			Data: `{"result_type":"matrix","series_list_result":[` +
				`{"metric":{"namespace":"shop","pod":"web-1"},"values":["0.21","0.93"],"timestamps":[1790473680,1790473740]},` +
				`{"metric":{"namespace":"shop","pod":"web-2"},"values":["0.18","0.25"],"timestamps":[1790473680,1790473740]}]}`,
			Query: rateQuery,
			Unit:  "cores",
		}},
		PodMetrics: []events.InvestigateDataInsight{{
			// pod_metric_enricher: memory, working-set bytes.
			Data: map[string]any{"name": "pod_metric", "resource_type": "memory", "data": []any{map[string]any{
				"metric":     map[string]any{"container": "app", "pod": "web-1", "namespace": "shop"},
				"timestamps": []any{1790473680.0, 1790473740.0}, "values": []any{"30973952", "210489344"},
			}}},
			Query: "resource_type=memory",
			Unit:  "bytes",
		}},
		Others: []events.InvestigateDataInsight{{
			// The workload `metrics` action: one unlabelled series under results[].payload.
			Data: map[string]any{"data": map[string]any{"results": []any{map[string]any{
				"query_key": "cpu_usage",
				"payload":   []any{map[string]any{"metric": map[string]any{}, "timestamps": []any{1790473680.0, 1790473740.0}, "values": []any{0.39, 1.18}}},
				"query":     `sum(rate(container_cpu_usage_seconds_total{namespace="shop", pod=~"web-.*"}[5m]))`,
			}}}},
			Query: "metrics=cpu_usage",
			Unit:  "cores",
		}},
	}
	got := renderMetricDigest(ev)

	for _, want := range []string{
		"- query `" + rateQuery + "` · unit: cores\n  - shop web-1: 2 points",
		"  - shop web-2: 2 points",
		"- query `resource_type=memory` · unit: bytes\n  - shop web-1 app: 2 points",
		"- query `metrics=cpu_usage` · unit: cores\n  - series: 2 points",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("digest missing %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "container_cpu_usage_seconds_total"); n != 1 {
		t.Errorf("expected the rate() query stated once for its two series, got %d:\n%s", n, got)
	}
	if n := strings.Count(got, "unit:"); n != 3 {
		t.Errorf("expected one unit per card, stated once, got %d:\n%s", n, got)
	}
	if strings.Contains(got, "payload") {
		t.Errorf("container key named a series:\n%s", got)
	}
}

func TestRenderMetricDigest_QueryHeadersStayInsideTheBudget(t *testing.T) {
	var cards []events.InvestigateDataInsight
	for i := 0; i < 10; i++ {
		cards = append(cards, events.InvestigateDataInsight{
			Data: map[string]any{"series_list_result": []any{
				map[string]any{"metric": map[string]any{"pod": fmt.Sprintf("web-%d", i)}, "values": []any{"1", "2"}, "timestamps": []any{1790473680.0, 1790473740.0}},
				map[string]any{"metric": map[string]any{"pod": fmt.Sprintf("api-%d", i)}, "values": []any{"1", "2"}, "timestamps": []any{1790473680.0, 1790473740.0}},
			}},
			Query: strings.Repeat("q", 300),
		})
	}
	got := renderMetricDigest(events.InvestigateData{MetricsData: cards})
	overflow := strings.LastIndex(got, "- … ")
	if overflow < 0 || overflow > maxMetricDigestBytes {
		t.Errorf("expected the overflow note after at most %d bytes, got it at %d:\n%s", maxMetricDigestBytes, overflow, got)
	}
	if !strings.HasSuffix(got, "more series in the structured evidence file\n") {
		t.Errorf("expected an overflow note, got:\n%s", got)
	}
}

// A per-object rule returns every breaching object; the one the alert fired for
// must be told apart from the rest and lead the digest (#39669).
func TestRenderMetricDigest_AlertingSeriesLeadsAndSeriesAreNamedApart(t *testing.T) {
	ev := events.InvestigateData{
		PodMetrics: []events.InvestigateDataInsight{{
			Data: `{"series_list_result":[{"metric":{"__name__":"container_memory_working_set_bytes","pod":"api-1"},"values":["1"],"timestamps":[1789382040]}]}`,
		}},
		MetricsData: []events.InvestigateDataInsight{{
			Query: "kubelet_volume_stats_available_bytes / kubelet_volume_stats_capacity_bytes * 100 < 20",
			Data: `{"result_type":"matrix","series_list_result":[` +
				`{"alerting":true,"metric":{"namespace":"nudgebee","persistentvolumeclaim":"data-postgresql-0"},"values":["18.92"],"timestamps":[1789382040]},` +
				`{"metric":{"namespace":"nudgebee","persistentvolumeclaim":"data-elasticsearch-0"},"values":["5.79"],"timestamps":[1789382040]}]}`,
		}},
	}
	got := renderMetricDigest(ev)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected a query header, the alerting series, the other-objects line and the pod series, got:\n%s", got)
	}
	if !strings.HasPrefix(lines[0], "- query `kubelet_volume_stats") {
		t.Errorf("expected the card holding the alerting series first, got:\n%s", got)
	}
	if !strings.Contains(lines[1], "persistentvolumeclaim=data-postgresql-0 [the object this alert fired for]") || !strings.Contains(lines[1], "last 18.920") {
		t.Errorf("expected the postgres volume first and tagged, got:\n%s", got)
	}
	// The other volume is named, but its number is what the model quoted as the
	// postgres volume's: it is not in the digest.
	if !strings.Contains(lines[2], "1 other object also matched this alert's query") || !strings.Contains(lines[2], "persistentvolumeclaim=data-elasticsearch-0") {
		t.Errorf("expected the elasticsearch volume named on the other-objects line, got:\n%s", got)
	}
	if strings.Contains(got, "5.79") {
		t.Errorf("expected the other volume's values left out, got:\n%s", got)
	}
}

// On a rule tracking many objects the others used to fill the whole budget, cutting
// the alerting workload's own metrics. They take one line now, whatever their number.
func TestRenderMetricDigest_OtherObjectsTakeOneLine(t *testing.T) {
	series := `{"alerting":true,"metric":{"namespace":"shop","deployment":"orders"},"values":["1"],"timestamps":[1789382040]}`
	for i := 0; i < 14; i++ {
		series += fmt.Sprintf(`,{"metric":{"namespace":"shop","deployment":"other-%02d"},"values":["1"],"timestamps":[%d]}`, i, 1789382040+60*(i%3))
	}
	ev := events.InvestigateData{
		PodMetrics: []events.InvestigateDataInsight{{
			Data: `{"series_list_result":[{"metric":{"__name__":"container_memory_working_set_bytes","pod":"orders-1"},"values":["1"],"timestamps":[1789382040]}]}`,
		}},
		MetricsData: []events.InvestigateDataInsight{{
			Query: "kube_deployment_spec_replicas > kube_deployment_status_replicas_available",
			Data:  `{"result_type":"matrix","series_list_result":[` + series + `]}`,
		}},
	}
	got := renderMetricDigest(ev)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected header, alerting series, one other-objects line and the pod series, got:\n%s", got)
	}
	if !strings.Contains(lines[1], "deployment=orders [the object this alert fired for]") {
		t.Errorf("expected the alerting deployment first, got:\n%s", got)
	}
	note := lines[2]
	if !strings.Contains(note, "14 other objects also matched this alert's query") || !strings.Contains(note, ": deployment=other-00, deployment=other-01,") ||
		!strings.Contains(note, "deployment=other-04, +9 more") || strings.Contains(note, "deployment=other-05") {
		t.Errorf("expected a count, the first five names and the rest counted, got:\n%s", note)
	}
	if !strings.Contains(note, "first seen 09-14 10:34→09-14 10:36 UTC") {
		t.Errorf("expected when the other objects were first seen, got:\n%s", note)
	}
	if !strings.Contains(lines[3], "container_memory_working_set_bytes") || strings.Contains(got, "more series") {
		t.Errorf("expected the workload's own metric still rendered, with nothing cut, got:\n%s", got)
	}
}

// The same series stored in two cards with different siblings is rendered once,
// even though the labels that tell series apart differ per card.
func TestRenderMetricDigest_SeriesInTwoCardsRenderedOnce(t *testing.T) {
	a := `{"metric":{"namespace":"n","persistentvolumeclaim":"a"},"values":["1"],"timestamps":[1789382040]}`
	b := `{"metric":{"namespace":"n","persistentvolumeclaim":"b"},"values":["2"],"timestamps":[1789382040]}`
	ev := events.InvestigateData{
		PodMetrics:  []events.InvestigateDataInsight{{Data: `{"series_list_result":[` + a + `]}`}},
		MetricsData: []events.InvestigateDataInsight{{Data: `{"series_list_result":[` + a + `,` + b + `]}`}},
	}
	got := renderMetricDigest(ev)
	if n := strings.Count(got, "\n"); n != 2 || !strings.Contains(got, "persistentvolumeclaim=b") {
		t.Errorf("expected series a once and b once, got:\n%s", got)
	}
}

// One of the rule's other objects is counted once and never listed, wherever a copy
// of its series sits: a card that holds it alone must not print its numbers.
func TestRenderMetricDigest_OtherObjectIsNotListedByAnotherCard(t *testing.T) {
	a := `{"alerting":true,"metric":{"namespace":"n","persistentvolumeclaim":"a"},"values":["1"],"timestamps":[1789382040]}`
	b := `{"metric":{"namespace":"n","persistentvolumeclaim":"b"},"values":["2"],"timestamps":[1789382040]}`
	for name, ev := range map[string]events.InvestigateData{
		"lone copy first": {
			PodMetrics:  []events.InvestigateDataInsight{{Data: `{"series_list_result":[` + b + `]}`}},
			MetricsData: []events.InvestigateDataInsight{{Data: `{"series_list_result":[` + a + `,` + b + `]}`}},
		},
		"lone copy last": {
			PodMetrics:  []events.InvestigateDataInsight{{Data: `{"series_list_result":[` + a + `,` + b + `]}`}},
			MetricsData: []events.InvestigateDataInsight{{Data: `{"series_list_result":[` + b + `]}`}},
		},
	} {
		got := renderMetricDigest(ev)
		if strings.Count(got, "\n") != 2 || strings.Count(got, "persistentvolumeclaim=b") != 1 || !strings.Contains(got, "1 other object") || strings.Contains(got, "last 2.000") {
			t.Errorf("%s: expected volume a listed and volume b counted once, got:\n%s", name, got)
		}
	}
}

// When the series budget cuts the digest, the count of other objects still gets
// through: without it the prompt would not say that anything else matched.
func TestRenderMetricDigest_OtherObjectsCountSurvivesTheCut(t *testing.T) {
	series := `{"metric":{"namespace":"n","container":"other"},"values":["9"],"timestamps":[1789382040]}`
	for i := 0; i < maxMetricDigestSeries+1; i++ {
		series += fmt.Sprintf(`,{"alerting":true,"metric":{"namespace":"n","container":"c-%02d"},"values":["1"],"timestamps":[1789382040]}`, i)
	}
	got := renderMetricDigest(events.InvestigateData{MetricsData: []events.InvestigateDataInsight{{Data: `{"series_list_result":[` + series + `]}`}}})
	if !strings.Contains(got, "… 1 more series in the structured evidence file") || !strings.Contains(got, "1 other object also matched this alert's query") {
		t.Errorf("expected the cut note and the other-objects count, got:\n%s", got)
	}
}

// Two objects whose values match are still two objects: only the alerting one is
// tagged and rendered, and the other is counted, not merged into it.
func TestRenderMetricDigest_SeriesWithEqualValuesStayApart(t *testing.T) {
	ev := events.InvestigateData{MetricsData: []events.InvestigateDataInsight{{
		Data: `{"series_list_result":[` +
			`{"alerting":true,"metric":{"namespace":"n","persistentvolumeclaim":"a"},"values":["0"],"timestamps":[1789382040]},` +
			`{"metric":{"namespace":"n","persistentvolumeclaim":"b"},"values":["0"],"timestamps":[1789382040]}]}`,
	}}}
	got := renderMetricDigest(ev)
	if strings.Count(got, "\n") != 2 || strings.Count(got, "fired for") != 1 || !strings.Contains(got, "1 other object") || !strings.Contains(got, "persistentvolumeclaim=b") {
		t.Errorf("expected volume a tagged and volume b counted as the other object, got:\n%s", got)
	}
}
