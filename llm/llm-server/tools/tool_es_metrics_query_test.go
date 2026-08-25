package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/llm/tools/core"
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
