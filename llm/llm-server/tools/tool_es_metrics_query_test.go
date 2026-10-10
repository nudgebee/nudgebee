package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestESMetricsQueryTool_InputWrapping(t *testing.T) {
	// Verify that input without top-level "query" key is wrapped into {"query": ...}
	inputJSON := `{"index":"metricbeat-*","query":{"bool":{"filter":[{"term":{"kubernetes.namespace":"nudgebee"}}]}}}`
	var inputObj map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &inputObj); err != nil {
		t.Fatalf("failed to unmarshal test input: %v", err)
	}

	queryObj := inputObj["query"]
	if qMap, isMap := queryObj.(map[string]any); isMap && qMap != nil {
		if _, hasQuery := qMap["query"]; !hasQuery {
			queryObj = map[string]any{
				"query": qMap,
			}
		}
	}

	queryBytes, err := json.Marshal(queryObj)
	if err != nil {
		t.Fatalf("failed to marshal query: %v", err)
	}

	got := string(queryBytes)
	if !strings.HasPrefix(got, `{"query":`) {
		t.Fatalf("expected query to start with {\"query\":, got: %s", got)
	}
}

// A `[[Time:...]]` macro must be resolved to a real timestamp before the DSL is
// parsed. Left literal, it reaches the ES range filter verbatim, matches nothing
// and still returns HTTP 200 — an empty result the caller reads as "no data in
// this environment". Reproduced against a live index that returned 10,000
// documents for the same query without the macro and 0 with it.
func TestESMetricsQueryTool_SubstitutesTimeMacros(t *testing.T) {
	input := `{"index":"metrics-*","query":{"bool":{"filter":[` +
		`{"range":{"@timestamp":{"gte":"[[Time:-24h]]","lte":"[[Time:Now]]"}}}]}}}`

	// Through the real parse path the tool uses, so deleting the substitution call
	// fails this test rather than leaving it green against the helper.
	_, queryObj, userMsg, err := parseESMetricsQueryInput(input)
	if err != nil {
		t.Fatalf("parse failed: %v (%s)", err, userMsg)
	}

	marshalled, err := json.Marshal(queryObj)
	if err != nil {
		t.Fatalf("failed to marshal parsed query: %v", err)
	}
	if strings.Contains(string(marshalled), "[[Time:") {
		t.Fatalf("macro survived parsing and would reach Elasticsearch verbatim: %s", marshalled)
	}

	// The bound must also be readable as a real instant — a macro replaced by
	// anything ES cannot parse as a date is the same silent-zero in a new costume.
	// Decoded into a typed struct rather than chained map[string]any assertions so
	// a change to the wrapping shape fails by name here instead of panicking.
	var decoded struct {
		Query struct {
			Bool struct {
				Filter []struct {
					Range map[string]struct {
						Gte string `json:"gte"`
						Lte string `json:"lte"`
					} `json:"range"`
				} `json:"filter"`
			} `json:"bool"`
		} `json:"query"`
	}
	if err := json.Unmarshal(marshalled, &decoded); err != nil {
		t.Fatalf("parsed query is no longer valid JSON: %v\n%s", err, marshalled)
	}

	filters := decoded.Query.Bool.Filter
	if len(filters) == 0 {
		t.Fatalf("parsed query has no bool filter clause: %s", marshalled)
	}
	rng, ok := filters[0].Range["@timestamp"]
	if !ok {
		t.Fatalf("parsed query has no @timestamp range filter: %s", marshalled)
	}

	gte := rng.Gte
	parsed, err := time.Parse(time.RFC3339, gte)
	require.NoError(t, err, "gte %q is not an RFC3339 instant Elasticsearch can range on", gte)
	assert.WithinDuration(t, time.Now().UTC().Add(-24*time.Hour), parsed, time.Minute)
}

func TestValidateESMetricsQueryShape_RejectsObservedMalformedQueries(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"placeholder and bare filter", `{"query":{"bool_query_placeholder":true,"filter":[0,1,2]}}`, "bool_query_placeholder"},
		{"fabricated bool_filter", `{"query":{"bool_filter":[null,null,null]}}`, "bool_filter"},
		{"fabricated encoded bool path", `{"query":{"bool_1_0_0_1":true}}`, "bool_1_0_0_1"},
		{"fabricated encoded query path", `{"query":{"query_bool_filter_0_term_kubernetes_namespace":"nudgebee"}}`, "query_bool_filter_0"},
		{"bool scalar from live E2E", `{"query":{"bool":0}}`, "bool query must be a JSON object"},
		{"bool filter scalar", `{"query":{"bool":{"filter":[0,1,2]}}}`, "bool.filter must contain query objects"},
		{"filter without bool", `{"query":{"filter":[{"match_all":{}}]}}`, "filter must be inside a bool"},
		{"nonobject query", `{"query":"match_all"}`, "query clause must be a JSON object"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var input any
			if err := json.Unmarshal([]byte(tc.input), &input); err != nil {
				t.Fatal(err)
			}
			if err := validateESMetricsQueryShape(input); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestValidateESMetricsQueryShape_AcceptsValidForms(t *testing.T) {
	for _, raw := range []string{
		`{"query":{"bool":{"filter":[{"exists":{"field":"kubernetes.pod.cpu.usage.nanocores"}},{"range":{"@timestamp":{"gte":"now-7d"}}}]}}}`,
		`{"bool":{"filter":[{"term":{"kubernetes.namespace":"nudgebee"}}]}}`,
		`{"query":{"match_all":{}},"size":1}`,
	} {
		var input any
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			t.Fatal(err)
		}
		if err := validateESMetricsQueryShape(input); err != nil {
			t.Errorf("valid DSL rejected: %v", err)
		}
	}
}

func TestESMetricsQueryTool_RejectsObservedPlaceholderBeforeAPI(t *testing.T) {
	input := core.NBToolCallRequest{Command: `{"index":"metrics-kubernetes.*","query":{"query":{"bool_query_placeholder":true,"filter":[0,1,2]}}}`}
	response, err := (ESMetricsQueryTool{}).Call(core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin()}, input)
	if err == nil || response.Status != core.NBToolResponseStatusError {
		t.Fatalf("expected tool error, got response=%+v err=%v", response, err)
	}
	if !strings.Contains(response.Data, "bool_query_placeholder") || !strings.Contains(response.Data, "Build a new query") {
		t.Fatalf("expected actionable validation error, got %q", response.Data)
	}
}

// Regression tests for #36236: per-query errors carried in results[].Error
// must be surfaced as tool-level failures, not swallowed into a shaped-empty
// success payload. See collectESMetricsErrors in tool_es_metrics_query.go.

func TestCollectESMetricsErrors_AllSuccess(t *testing.T) {
	resp := core.ObservabilityMetricsQueryResponse{
		Results: []core.ObservabilityMetricsQueryResult{
			{QueryKey: "q1", Payload: []core.ObservabilityMetricsQuerySeries{{}}},
		},
	}
	if got := collectESMetricsErrors(resp); got != "" {
		t.Fatalf("expected empty error for all-success batch, got %q", got)
	}
}

func TestCollectESMetricsErrors_SingleFailure(t *testing.T) {
	errText := "metric query failed with status 400: index_closed_exception"
	resp := core.ObservabilityMetricsQueryResponse{
		Results: []core.ObservabilityMetricsQueryResult{
			{QueryKey: "q1", Error: &errText},
		},
	}
	got := collectESMetricsErrors(resp)
	if !strings.Contains(got, errText) {
		t.Fatalf("expected joined error to contain %q, got %q", errText, got)
	}
	if !strings.Contains(got, "q1") {
		t.Fatalf("expected joined error to name the query key q1, got %q", got)
	}
}

func TestCollectESMetricsErrors_EmptyStringNotAnError(t *testing.T) {
	empty := ""
	resp := core.ObservabilityMetricsQueryResponse{
		Results: []core.ObservabilityMetricsQueryResult{
			{QueryKey: "q1", Error: &empty},
		},
	}
	if got := collectESMetricsErrors(resp); got != "" {
		t.Fatalf("expected empty-string Error to be treated as no failure, got %q", got)
	}
}

func TestCollectESMetricsErrors_MixedBatch(t *testing.T) {
	errText := "shard failure"
	resp := core.ObservabilityMetricsQueryResponse{
		Results: []core.ObservabilityMetricsQueryResult{
			{QueryKey: "ok", Payload: []core.ObservabilityMetricsQuerySeries{{}}},
			{QueryKey: "bad", Error: &errText},
		},
	}
	got := collectESMetricsErrors(resp)
	if got == "" {
		t.Fatal("expected non-empty error for mixed batch")
	}
	if !strings.Contains(got, "bad") || !strings.Contains(got, errText) {
		t.Fatalf("expected joined error to name the failing key and text, got %q", got)
	}
}

func TestSummarizeESMetricsResponse_PassesSampleThrough(t *testing.T) {
	zero := int64(0)
	out := summarizeESMetricsResponse(core.ObservabilityMetricsQueryResponse{Results: []core.ObservabilityMetricsQueryResult{{
		QueryKey: "q", DocsMatched: &zero, Note: "matched nothing", Sample: "metricset.metric_name: CPUUtilization",
	}}}, false)
	b, err := json.Marshal(out)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"sample":"metricset.metric_name: CPUUtilization"`)
}

// The api-server reads documents on a miss only when asked; this tool is the one caller
// that asks. Dropping the flag would silently turn the sample off for the agent.
func TestBuildESMetricsQueryRequest_AsksForZeroMatchSample(t *testing.T) {
	req := buildESMetricsQueryRequest("acc", "metrics-*", "k", `{"query":{"match_all":{}}}`, time.Unix(0, 0), time.Unix(60, 0))
	assert.Equal(t, true, req.Request["zero_match_sample"])
	assert.Equal(t, "dsl", req.Request["query_type"])
	assert.Equal(t, "metrics-*", req.Request["metric_name"])
}

func TestParseESDiscoverInput(t *testing.T) {
	d, ok, _, err := parseESDiscoverInput(`{"index":"metrics-*","discover":"datasets"}`)
	require.True(t, ok)
	require.NoError(t, err)
	assert.Equal(t, "datasets", d.mode)

	d, ok, _, err = parseESDiscoverInput(`{"index":"metrics-*","discover":"sample","dataset":"aws.cloudwatch_metrics"}`)
	require.True(t, ok)
	require.NoError(t, err)
	assert.Equal(t, "aws.cloudwatch_metrics", d.dataset)

	_, ok, msg, err := parseESDiscoverInput(`{"index":"metrics-*","discover":"sample"}`)
	assert.True(t, ok)
	assert.Error(t, err)
	assert.Contains(t, msg, "Run discover \"datasets\" first")

	_, ok, _, err = parseESDiscoverInput(`{"index":"metrics-*","discover":"everything"}`)
	assert.True(t, ok)
	assert.Error(t, err)

	_, ok, _, _ = parseESDiscoverInput(`{"index":"metrics-*","query":{"query":{"match_all":{}}}}`)
	assert.False(t, ok, "a normal query is not discovery")
}

func esTestResponse(n, points int) core.ObservabilityMetricsQueryResponse {
	var p []core.ObservabilityMetricsQuerySeries
	for i := 0; i < n; i++ {
		ts := make([]int64, points)
		vs := make([]float64, points)
		for j := range ts {
			ts[j] = int64(points - j) // newest first, to check "last" uses timestamps
			vs[j] = float64(i*10 + j)
		}
		p = append(p, core.ObservabilityMetricsQuerySeries{Metric: map[string]string{"pod": fmt.Sprint(i)}, Timestamps: ts, Values: vs})
	}
	return core.ObservabilityMetricsQueryResponse{Results: []core.ObservabilityMetricsQueryResult{{QueryKey: "q", Query: "{}", Payload: p}}}
}

type esSummary struct {
	Results []struct {
		CommonLabels map[string]string `json:"common_labels"`
		Payload      []struct {
			Metric map[string]string  `json:"metric"`
			Values []float64          `json:"values"`
			Stats  map[string]float64 `json:"stats"`
		} `json:"payload"`
		CappedNotice string `json:"capped_notice"`
	} `json:"results"`
}

func summarizeForTest(t *testing.T, resp core.ObservabilityMetricsQueryResponse) esSummary {
	t.Helper()
	b, err := json.Marshal(summarizeESMetricsResponse(resp, esOversized(resp)))
	require.NoError(t, err)
	var out esSummary
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

// Same limits as prometheus_execute: within them every series and its values stay
// inline; past them, stats only for the most significant series.
func TestSummarizeESMetricsResponse_SizeLimitsMatchPrometheus(t *testing.T) {
	small := summarizeForTest(t, esTestResponse(43, 3)) // 43 series, 129 points: inline
	require.Len(t, small.Results[0].Payload, 43)
	assert.Len(t, small.Results[0].Payload[0].Values, 3, "within the limits, values stay inline")
	assert.Equal(t, 0.0, small.Results[0].Payload[0].Stats["last"], "last = value at the newest timestamp")

	big := summarizeForTest(t, esTestResponse(43, 60)) // 2,580 points: past maxInlinePoints
	require.Len(t, big.Results[0].Payload, 43, "every series still has a stats line")
	assert.Nil(t, big.Results[0].Payload[0].Values, "past the limits: stats only")
	assert.Equal(t, "42", big.Results[0].Payload[0].Metric["pod"], "largest max first")
	assert.Empty(t, big.Results[0].CappedNotice)

	huge := summarizeForTest(t, esTestResponse(250, 2))
	require.Len(t, huge.Results[0].Payload, maxInlineSeries)
	assert.Contains(t, huge.Results[0].CappedNotice, "of 250 series")
}

type esSaveCapture struct {
	workspace.WorkspaceManager
	path, body string
}

func (c *esSaveCapture) SaveFile(_ *security.RequestContext, _, _, path, content string) error {
	c.path, c.body = path, content
	return nil
}

func TestSaveESSeriesToWorkspace_KeepsEverySeriesAndPoint(t *testing.T) {
	orig := wm
	capture := &esSaveCapture{}
	wm = capture
	defer func() { wm = orig }()

	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: "a", ConversationId: "c"}
	ref, refs, err := saveESSeriesToWorkspace(ctx, esTestResponse(43, 60), "Elasticsearch")
	require.NoError(t, err)
	assert.Equal(t, capture.path, ref)
	assert.True(t, strings.HasPrefix(ref, "metrics_es_"))
	require.Len(t, refs, 1)
	var saved map[string][]core.ObservabilityMetricsQuerySeries
	require.NoError(t, json.Unmarshal([]byte(capture.body), &saved))
	require.Len(t, saved["{}"], 43)
	assert.Len(t, saved["{}"][0].Values, 60, "the file keeps every point")

	note := esSeriesFileNote(ref, false, 43, 2580)
	assert.Contains(t, note, ref)
	assert.Contains(t, note, "43 series")
	assert.Contains(t, esSeriesFileNote("", true, 43, 2580), "could not be saved")
}

// splunk_metrics_query shares inlineESResponse, so it shares this step too: a
// stats-only result must always say where the full data went.
func TestInlineESResponse_NotesEveryResultWithSeries(t *testing.T) {
	orig := wm
	wm = &esSaveCapture{}
	defer func() { wm = orig }()
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: "a", ConversationId: "c"}

	out, refs := inlineESResponse(ctx, esTestResponse(5, 3), "splunk_metrics_query", "Splunk")
	assert.Nil(t, refs, "within the limits nothing is saved")
	b, _ := json.Marshal(out)
	assert.NotContains(t, string(b), "file_ref")

	big := esTestResponse(43, 60)
	big.Results = append(big.Results, core.ObservabilityMetricsQueryResult{QueryKey: "empty"})
	out, refs = inlineESResponse(ctx, big, "splunk_metrics_query", "Splunk")
	require.Len(t, refs, 1)
	assert.Equal(t, "Full Splunk metrics query results", refs[0].Description)
	b, _ = json.Marshal(out)
	var s struct {
		Results []struct {
			Note string `json:"note"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(b, &s))
	assert.Contains(t, s.Results[0].Note, refs[0].Url)
	assert.Empty(t, s.Results[1].Note, "a result without series gets no note")
}

// Series the way Elasticsearch builds them: a dozen labels per series, most shared.
func esWideLabelResponse(n, points int) core.ObservabilityMetricsQueryResponse {
	resp := esTestResponse(n, points)
	for i := range resp.Results[0].Payload {
		m := resp.Results[0].Payload[i].Metric
		m["__name__"] = "kubernetes.pod.cpu.usage.nanocores"
		m["kubernetes.namespace"] = "demo"
		m["kubernetes.namespace_uid"] = "00000000-0000-0000-0000-000000000000"
		m["kubernetes.node.hostname"] = fmt.Sprintf("node-%d.example.com", i%3)
		m["kubernetes.pod.uid"] = fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
		m["kubernetes.pod.ip"] = fmt.Sprintf("10.0.0.%d", i%250)
		m["kubernetes.replicaset.name"] = fmt.Sprintf("app-%d-5d8d6bb8f6", i)
		m["kubernetes.pod.start_time"] = "2026-10-09T01:35:13Z"
	}
	return resp
}

func TestSummarizeESMetricsResponse_SharedLabelsListedOnce(t *testing.T) {
	out := summarizeForTest(t, esWideLabelResponse(5, 3))
	r := out.Results[0]
	assert.Equal(t, "demo", r.CommonLabels["kubernetes.namespace"])
	assert.Equal(t, "kubernetes.pod.cpu.usage.nanocores", r.CommonLabels["__name__"])
	require.Len(t, r.Payload, 5)
	assert.NotContains(t, r.Payload[0].Metric, "kubernetes.namespace", "a shared label is not repeated per row")
	assert.Contains(t, r.Payload[0].Metric, "kubernetes.pod.uid", "a label that differs stays on the row")
}

// The bound that matters is bytes: 150 stats rows with Elasticsearch-sized labels were
// ~90 KB and got cut in the middle at the 64 KB observation limit.
func TestInlineESResponse_StaysWithinByteBudget(t *testing.T) {
	orig := wm
	capture := &esSaveCapture{}
	wm = capture
	defer func() { wm = orig }()
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: "a", ConversationId: "c"}

	t.Run("many series", func(t *testing.T) {
		out, refs := inlineESResponse(ctx, esWideLabelResponse(400, 2), "es_metrics_query", "Elasticsearch")
		b, err := json.Marshal(out)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(b), maxESInlineBytes+1024, "inline result stays near the budget (notes and query text ride on top)")
		require.Len(t, refs, 1, "what does not fit is saved")
		assert.NotContains(t, string(b), `"values":null`)
		var s esSummary
		require.NoError(t, json.Unmarshal(b, &s))
		assert.Contains(t, s.Results[0].CappedNotice, "of 400 series")
		assert.Equal(t, "399", s.Results[0].Payload[0].Metric["pod"], "largest max first")
	})
	t.Run("few points, heavy labels", func(t *testing.T) {
		resp := esWideLabelResponse(140, 1) // under the series and point limits, over the byte budget
		require.False(t, esOversized(resp))
		out, refs := inlineESResponse(ctx, resp, "splunk_metrics_query", "Splunk")
		require.Len(t, refs, 1, "too large to inline is saved even under the count limits")
		b, _ := json.Marshal(out)
		assert.LessOrEqual(t, len(b), maxESInlineBytes+1024)
	})
	t.Run("small result unchanged", func(t *testing.T) {
		out, refs := inlineESResponse(ctx, esTestResponse(3, 5), "es_metrics_query", "Elasticsearch")
		assert.Nil(t, refs)
		var s esSummary
		b, _ := json.Marshal(out)
		require.NoError(t, json.Unmarshal(b, &s))
		assert.Len(t, s.Results[0].Payload[0].Values, 5, "values stay inline")
	})
}
