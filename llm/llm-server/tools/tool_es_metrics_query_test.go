package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/tools/core"

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
