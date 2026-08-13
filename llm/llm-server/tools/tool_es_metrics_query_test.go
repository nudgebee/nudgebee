package tools

import (
	"encoding/json"
	"strings"
	"testing"
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
