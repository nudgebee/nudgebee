package observability

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCubeAPMMetricRangeParams(t *testing.T) {
	t.Run("honours an explicit step", func(t *testing.T) {
		start, end, step := cubeAPMMetricRangeParams(FetchMetricsRequest{
			StartTime: 1_700_000_000_000, EndTime: 1_700_003_600_000, StepInterval: 30,
		})
		if start != "1700000000" || end != "1700003600" {
			t.Errorf("window = (%s, %s), want epoch seconds", start, end)
		}
		if step != 30 {
			t.Errorf("step = %d, want 30", step)
		}
	})

	// An unset step is chosen so the window yields roughly 100 points, which is
	// what the charts render.
	t.Run("derives a step of about 100 points", func(t *testing.T) {
		_, _, step := cubeAPMMetricRangeParams(FetchMetricsRequest{
			StartTime: 1_700_000_000_000, EndTime: 1_700_003_600_000,
		})
		if step != 36 {
			t.Errorf("step = %d, want 36 (3600s / 100)", step)
		}
	})

	t.Run("never returns a zero step", func(t *testing.T) {
		_, _, step := cubeAPMMetricRangeParams(FetchMetricsRequest{StartTime: 1000, EndTime: 2000})
		if step < 1 {
			t.Errorf("step = %d; a zero step makes query_range reject the request", step)
		}
	})
}

func TestCubeAPMSample(t *testing.T) {
	tests := []struct {
		name    string
		sample  []any
		wantTs  int64
		wantVal float64
		wantOK  bool
	}{
		{"normal", []any{float64(1700000000), "1.5"}, 1700000000000, 1.5, true},
		{"integer value", []any{float64(1700000000), "42"}, 1700000000000, 42, true},
		{"negative", []any{float64(1700000000), "-3"}, 1700000000000, -3, true},
		{"wrong arity", []any{float64(1)}, 0, 0, false},
		{"timestamp not a number", []any{"1700000000", "1"}, 0, 0, false},
		// Prometheus encodes the value as a STRING so NaN/Inf survive the wire.
		{"value not a string", []any{float64(1), 1.5}, 0, 0, false},
		{"unparseable value", []any{float64(1), "abc"}, 0, 0, false},
		// Non-finite samples would chart as spikes; they are dropped instead.
		{"NaN", []any{float64(1), "NaN"}, 0, 0, false},
		{"Inf", []any{float64(1), "+Inf"}, 0, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, val, ok := cubeAPMSample(tt.sample)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if ts != tt.wantTs || val != tt.wantVal {
				t.Errorf("got (%d, %v), want (%d, %v)", ts, val, tt.wantTs, tt.wantVal)
			}
		})
	}
}

func TestCubeAPMPromResults(t *testing.T) {
	decode := func(t *testing.T, body string) cubeAPMPromResponse {
		t.Helper()
		var out cubeAPMPromResponse
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("failed to decode: %v", err)
		}
		return out
	}

	t.Run("range query", func(t *testing.T) {
		resp := decode(t, `{"status":"success","isPartial":false,"data":{"resultType":"matrix","result":[
			{"metric":{"__name__":"up","job":"api"},"values":[[1700000000,"1"],[1700000060,"0"]]}]}}`)

		results := cubeAPMPromResults(resp)
		if len(results) != 1 {
			t.Fatalf("got %d series, want 1", len(results))
		}
		if len(results[0].Values) != 2 || results[0].Values[1] != 0 {
			t.Errorf("values = %v", results[0].Values)
		}
		if results[0].Timestamps[0] != 1700000000000 {
			t.Errorf("timestamps = %v, want epoch milliseconds", results[0].Timestamps)
		}
		if results[0].Metric["job"] != "api" {
			t.Errorf("metric labels = %v", results[0].Metric)
		}
	})

	// An instant query carries a single `value` pair per series rather than a
	// `values` array; both are normalized so charts need not care which ran.
	t.Run("instant query normalizes value to values", func(t *testing.T) {
		resp := decode(t, `{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{"job":"api"},"value":[1700000000,"7"]}]}}`)

		results := cubeAPMPromResults(resp)
		if len(results) != 1 || len(results[0].Values) != 1 || results[0].Values[0] != 7 {
			t.Fatalf("got %+v", results)
		}
	})

	t.Run("empty result", func(t *testing.T) {
		resp := decode(t, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
		if len(cubeAPMPromResults(resp)) != 0 {
			t.Error("expected no series")
		}
	})

	t.Run("drops unusable samples but keeps the series", func(t *testing.T) {
		resp := decode(t, `{"status":"success","data":{"result":[
			{"metric":{},"values":[[1700000000,"1"],[1700000060,"NaN"],[1700000120,"3"]]}]}}`)

		results := cubeAPMPromResults(resp)
		if len(results) != 1 {
			t.Fatalf("got %d series, want 1", len(results))
		}
		if len(results[0].Values) != 2 {
			t.Errorf("values = %v, want the NaN dropped", results[0].Values)
		}
		if len(results[0].Timestamps) != len(results[0].Values) {
			t.Errorf("timestamps (%d) and values (%d) must stay aligned",
				len(results[0].Timestamps), len(results[0].Values))
		}
	})
}

func TestCubeAPMMetadataQuery(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 0, 0, 0, time.UTC)

	t.Run("carries the window and the limit", func(t *testing.T) {
		got := cubeAPMMetadataQuery(1_700_000_000_000, 1_700_003_600_000, nil, now)
		params, err := url.ParseQuery(strings.TrimPrefix(got, "?"))
		if err != nil {
			t.Fatalf("not a valid query string: %v", err)
		}
		if params.Get("start") != "1700000000" || params.Get("end") != "1700003600" {
			t.Errorf("window = %v, want epoch seconds", params)
		}
		if params.Get("limit") == "" {
			t.Error("limit must be sent; without it the engine scans the whole index")
		}
	})

	// Omitting the window lets the server pick its own default range, which is how
	// a match[]-filtered lookup ends up returning nothing.
	t.Run("defaults the window to the last hour", func(t *testing.T) {
		got := cubeAPMMetadataQuery(0, 0, nil, now)
		params, _ := url.ParseQuery(strings.TrimPrefix(got, "?"))
		if params.Get("end") != "1788483600" {
			t.Errorf("end = %s, want now in epoch seconds", params.Get("end"))
		}
		if params.Get("start") == "" || params.Get("start") == "0" {
			t.Errorf("start = %s, want an hour before now", params.Get("start"))
		}
	})

	t.Run("passes matchers through as match[]", func(t *testing.T) {
		got := cubeAPMMetadataQuery(1000, 2000, []string{`{__name__="up"}`, ""}, now)
		params, _ := url.ParseQuery(strings.TrimPrefix(got, "?"))
		matchers := params["match[]"]
		if len(matchers) != 1 || matchers[0] != `{__name__="up"}` {
			t.Errorf("match[] = %v, want the one non-empty matcher", matchers)
		}
	})
}

func TestCubeAPMStatusError(t *testing.T) {
	t.Run("401 names the field to fix", func(t *testing.T) {
		err := cubeAPMStatusError(401, []byte("unauthorized"))
		if !strings.Contains(err.Error(), "token") {
			t.Errorf("error should point at the token: %v", err)
		}
	})

	// CubeAPM echoes the offending query back on a parse error, and an unbounded
	// query string in a UI toast is unreadable.
	t.Run("truncates a long body", func(t *testing.T) {
		err := cubeAPMStatusError(400, []byte(strings.Repeat("x", 5000)))
		if len(err.Error()) > 700 {
			t.Errorf("error is %d chars; the body should be truncated", len(err.Error()))
		}
		if !strings.Contains(err.Error(), "…") {
			t.Error("truncation should be marked with an ellipsis")
		}
	})
}

func TestBuildCubeAPMWorkloadCPUQuery(t *testing.T) {
	t.Run("counter family is rated", func(t *testing.T) {
		got := buildCubeAPMWorkloadCPUQuery(cubeAPMWorkloadMetricCandidate{
			Family: "container_cpu_usage_seconds_total", NamespaceLabel: "namespace",
			WorkloadLabel: "pod", Counter: true,
		}, "checkout-api", "payments")

		want := `sum(rate(container_cpu_usage_seconds_total{namespace="payments",pod=~"checkout-api-.*"}[5m])) by (pod)`
		if got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	})

	// The OTel "usage" gauges are already expressed in cores; rate()ing them
	// would report a rate of a rate.
	t.Run("gauge family is summed", func(t *testing.T) {
		got := buildCubeAPMWorkloadCPUQuery(cubeAPMWorkloadMetricCandidate{
			Family: "k8s_pod_cpu_usage", NamespaceLabel: "k8s_namespace_name", WorkloadLabel: "k8s_pod_name",
		}, "checkout-api", "payments")

		if strings.Contains(got, "rate(") {
			t.Errorf("a gauge must not be rated: %s", got)
		}
		want := `sum(k8s_pod_cpu_usage{k8s_namespace_name="payments",k8s_pod_name=~"checkout-api-.*"}) by (k8s_pod_name)`
		if got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	})

	// Pod names carry a replica-hash suffix, so a prefix match is the only form
	// that works for every workload kind.
	t.Run("workload is matched as a pod prefix", func(t *testing.T) {
		got := buildCubeAPMWorkloadCPUQuery(CubeAPMWorkloadCPUCandidates[0], "ledger", "payments")
		if !strings.Contains(got, `=~"ledger-.*"`) {
			t.Errorf("expected a prefix regex, got %s", got)
		}
	})

	t.Run("values are escaped", func(t *testing.T) {
		got := buildCubeAPMWorkloadCPUQuery(CubeAPMWorkloadCPUCandidates[0], `a"b`, `c"d`)
		if strings.Contains(got, `"c"d"`) {
			t.Errorf("unescaped quote broke out of the matcher: %s", got)
		}
		if !strings.Contains(got, `c\"d`) {
			t.Errorf("namespace not escaped: %s", got)
		}
	})
}

func TestCubeAPMWorkloadCPUCandidatesAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range CubeAPMWorkloadCPUCandidates {
		key := c.Family + "|" + c.NamespaceLabel + "|" + c.WorkloadLabel
		if seen[key] {
			t.Errorf("duplicate candidate %s — every entry costs one probe query", key)
		}
		seen[key] = true

		if c.Family == "" || c.NamespaceLabel == "" || c.WorkloadLabel == "" {
			t.Errorf("candidate %+v has an empty field", c)
		}
	}
	if len(CubeAPMWorkloadCPUCandidates) == 0 {
		t.Error("no CPU candidates configured; workload metric discovery would always fail")
	}
}

func TestCubeAPMQueryHasSeries(t *testing.T) {
	errMsg := "boom"

	tests := []struct {
		name   string
		result OutputMetricQuery
		want   bool
	}{
		{"empty", OutputMetricQuery{}, false},
		{
			// A query for an absent family answers success-with-zero-series, so an
			// empty payload is the signal that this candidate is the wrong convention.
			name:   "success with no series",
			result: OutputMetricQuery{Results: []QueryResult{{QueryKey: "cpu"}}},
			want:   false,
		},
		{
			name:   "error result does not count as series",
			result: OutputMetricQuery{Results: []QueryResult{{QueryKey: "cpu", Error: &errMsg, Payload: []Result{{}}}}},
			want:   false,
		},
		{
			name:   "has series",
			result: OutputMetricQuery{Results: []QueryResult{{QueryKey: "cpu", Payload: []Result{{Values: []float64{1}}}}}},
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cubeAPMQueryHasSeries(tt.result); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCubeAPMMetricSourceContract(t *testing.T) {
	var s any = &CubeAPMMetricSource{}

	if _, ok := s.(MetricSource); !ok {
		t.Error("CubeAPMMetricSource must implement MetricSource")
	}
	// CubeAPM's engine answers /label/__name__/values with a match[] selector, so
	// series discovery is genuinely available rather than stubbed.
	if _, ok := s.(MetricSeriesSource); !ok {
		t.Error("CubeAPMMetricSource must implement MetricSeriesSource")
	}
	// observabilityMetricsAction type-asserts to this to decide whether it can
	// auto-execute a workload metric query; without it CubeAPM accounts silently
	// fall back to the generic query set.
	if _, ok := s.(PlaybookQueryGenerator); !ok {
		t.Error("CubeAPMMetricSource must implement PlaybookQueryGenerator")
	}
}

func TestCubeAPMMetricSourceRoutedFromDispatcher(t *testing.T) {
	src, err := getMetricsSource("cubeapm", "user")
	if err != nil {
		t.Fatalf("getMetricsSource(cubeapm, user) failed: %v", err)
	}
	if _, ok := src.(*CubeAPMMetricSource); !ok {
		t.Errorf("got %T, want *CubeAPMMetricSource", src)
	}
}

// Every operator advertised for metrics must be one injectPromQLMatchers can
// render, or the builder offers a filter that fails at query time.
func TestCubeAPMMetricSupportedOperatorsAllRender(t *testing.T) {
	s := &CubeAPMMetricSource{}
	for _, op := range s.GetSupportedOperators() {
		t.Run(op, func(t *testing.T) {
			_, err := injectPromQLMatchers("up", []LabelMatcher{{Label: "job", Operator: op, Value: "api"}}, nil)
			if err != nil {
				t.Errorf("advertised operator %q does not render: %v", op, err)
			}
		})
	}
}

func TestCubeAPMProviderCapabilitiesRegistered(t *testing.T) {
	caps, ok := allProviderCaps["cubeapm"]
	if !ok {
		t.Fatal("cubeapm is missing from allProviderCaps; the Traces tab reads its " +
			"grouping and heatmap support from there")
	}
	if !caps.SupportsHeatmap {
		t.Error("CubeAPM implements QueryTracesHeatmap via the by-id trace fetch")
	}
	if !caps.SupportsTraceGrouping {
		t.Error("CubeAPM implements QueryGroupedTraces")
	}
	if !caps.SupportsRawQuery {
		t.Error("CubeAPM accepts raw LogsQL and PromQL in Code mode")
	}
}
