package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/llm/events"

	"github.com/stretchr/testify/assert"
)

// buildEvidencesRow marshals evidence blocks into the row shape
// processRowWithMessages consumes (the `evidences` column as a JSON string).
func buildEvidencesRow(t *testing.T, blocks []map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(blocks)
	if err != nil {
		t.Fatalf("marshal evidences: %v", err)
	}
	return map[string]any{"id": "test-event", "evidences": string(raw)}
}

func processedEvidence(t *testing.T, row map[string]any) events.InvestigateData {
	t.Helper()
	ev, ok := row["evidences"].(events.InvestigateData)
	if !ok {
		t.Fatalf("expected processed evidences to be InvestigateData, got %T", row["evidences"])
	}
	return ev
}

// logsEvidenceBlock mirrors the logs enricher's block shape: type "json",
// additional_info.action_name "logs", data = stringified {"data":[entries]}.
func logsEvidenceBlock(t *testing.T, entries []map[string]any, additionalInfo map[string]any) map[string]any {
	t.Helper()
	inner, err := json.Marshal(map[string]any{"data": entries})
	if err != nil {
		t.Fatalf("marshal log entries: %v", err)
	}
	if additionalInfo == nil {
		additionalInfo = map[string]any{}
	}
	additionalInfo["action_name"] = "logs"
	return map[string]any{
		"type":            "json",
		"data":            string(inner),
		"additional_info": additionalInfo,
	}
}

// Regression for event 15d3e867: the traces evidence block is emitted as
// type "json" with additional_info.action_name "traces", which previously
// matched no branch and fell into Others — the trace data showing the actual
// 504s was structurally invisible to every consumer of the Traces slot.
func TestProcessRowWithMessages_MapsTracesActionName(t *testing.T) {
	tracesData, _ := json.Marshal(map[string]any{"spans": []any{map[string]any{"status": "504"}}})
	row := buildEvidencesRow(t, []map[string]any{
		{
			"type":            "json",
			"data":            string(tracesData),
			"additional_info": map[string]any{"action_name": "traces"},
			"insight":         []map[string]any{{"message": "2× traefik → app-dev status=504", "severity": "Critical"}},
		},
	})
	ev := processedEvidence(t, EventsExecuteTool{}.processRowWithMessages(row, 0, 1, nil))
	if ev.Traces.Data == nil {
		t.Fatalf("expected traces block to map into Traces slot, got Others=%v", ev.Others)
	}
	tracesMap, ok := ev.Traces.Data.(map[string]any)
	if !ok || tracesMap["spans"] == nil {
		t.Errorf("expected parsed traces map with spans, got %#v", ev.Traces.Data)
	}
	if len(ev.Traces.Insight) == 0 {
		t.Errorf("expected traces insights carried")
	}
}

func TestProcessRowWithMessages_MapsPodEnricherActionName(t *testing.T) {
	podData, _ := json.Marshal([]any{map[string]any{"kind": "Pod", "metadata": map[string]any{"name": "checkout-6c54595687-cjp9x"}}})
	row := buildEvidencesRow(t, []map[string]any{
		{
			"type":            "json",
			"data":            string(podData),
			"additional_info": map[string]any{"action_name": "pod_enricher", "title": "Pod details", "pod_name": "checkout-6c54595687-cjp9x"},
			"insight":         []any{},
		},
	})
	ev := processedEvidence(t, EventsExecuteTool{}.processRowWithMessages(row, 0, 1, nil))
	if ev.PodData.Data == nil {
		t.Fatalf("expected pod_enricher block to map into PodData slot, got Others=%v", ev.Others)
	}
	pods, ok := ev.PodData.Data.([]any)
	if !ok || len(pods) != 1 {
		t.Errorf("expected parsed pod list with 1 entry, got %#v", ev.PodData.Data)
	}
	if len(ev.Others) != 0 {
		t.Errorf("expected pod_enricher block not to fall into Others, got %v", ev.Others)
	}
}

// A non-string action_name (malformed/unexpected upstream JSON) must not
// panic the type assertion that gates the pod_enricher branch.
func TestProcessRowWithMessages_PodEnricherNonStringActionName(t *testing.T) {
	row := buildEvidencesRow(t, []map[string]any{
		{
			"type":            "json",
			"data":            "{}",
			"additional_info": map[string]any{"action_name": []any{"pod_enricher"}},
		},
	})
	assert.NotPanics(t, func() {
		EventsExecuteTool{}.processRowWithMessages(row, 0, 1, nil)
	})
}

func TestBuildEvidenceManifest_MapsPodEnricherToPodData(t *testing.T) {
	data := map[string]any{"id": "test-event"}
	evidences := []events.Evidence{
		{Type: "json", AdditionalInfo: map[string]any{"action_name": "pod_enricher"}},
	}
	EventsExecuteTool{}.buildEvidenceManifest(data, evidences)
	manifest, ok := data["evidences"].(map[string]any)
	if !ok {
		t.Fatalf("expected manifest map, got %T", data["evidences"])
	}
	types, _ := manifest["available_evidence_types"].([]string)
	assert.Contains(t, types, "pod_data")
	assert.NotContains(t, types, "json_other")
}

func TestProcessRowWithMessages_LogsEvidence(t *testing.T) {
	entries := []map[string]any{
		{"timestamp": "2026-07-27T09:16:48Z", "message": "upstream timed out (110)", "severity": "ERROR"},
		{"timestamp": "2026-07-27T09:17:01Z", "message": "GET /health 200", "severity": "INFO"},
		{"timestamp": "", "message": "upstream_unreachable services-server", "severity": "error"},
	}
	logSummary := map[string]any{
		"total_lines":  float64(3),
		"log_patterns": []any{map[string]any{"count": float64(2), "template": "upstream *"}},
	}

	t.Run("full evidence keeps body, extracts error lines, carries digest", func(t *testing.T) {
		row := buildEvidencesRow(t, []map[string]any{logsEvidenceBlock(t, entries, map[string]any{"log_summary": logSummary})})
		ev := processedEvidence(t, EventsExecuteTool{FullEvidence: true}.processRowWithMessages(row, 0, 1, nil))

		if !strings.Contains(ev.LogData, "GET /health 200") || !strings.Contains(ev.LogData, "upstream timed out (110)") {
			t.Errorf("expected full log body preserved, got %q", ev.LogData)
		}
		if len(ev.ErrorLogData) != 2 {
			t.Fatalf("expected 2 severity-extracted error lines, got %v", ev.ErrorLogData)
		}
		if ev.ErrorLogData[0] != "2026-07-27T09:16:48Z\tERROR\tupstream timed out (110)" {
			t.Errorf("expected ts+severity formatted error line, got %q", ev.ErrorLogData[0])
		}
		summary, ok := ev.LogSummary.(map[string]any)
		if !ok || summary["total_lines"] == nil {
			t.Errorf("expected log_summary carried, got %#v", ev.LogSummary)
		}
	})

	t.Run("default (lean) mode still clears the body when error lines exist", func(t *testing.T) {
		row := buildEvidencesRow(t, []map[string]any{logsEvidenceBlock(t, entries, nil)})
		ev := processedEvidence(t, EventsExecuteTool{}.processRowWithMessages(row, 0, 1, nil))
		if ev.LogData != "" {
			t.Errorf("expected lean mode to clear LogData when error lines exist, got %q", ev.LogData)
		}
		if len(ev.ErrorLogData) == 0 {
			t.Errorf("expected error lines in lean mode")
		}
	})
}

func TestFormatErrorLogLine(t *testing.T) {
	if _, isErr := formatErrorLogLine(map[string]any{"severity": "info"}, "msg"); isErr {
		t.Errorf("info severity must not be an error line")
	}
	if _, isErr := formatErrorLogLine(map[string]any{}, "msg"); isErr {
		t.Errorf("missing severity must not be an error line")
	}
	line, isErr := formatErrorLogLine(map[string]any{"severity": " warning ", "timestamp": "t1"}, "disk pressure")
	if !isErr || line != "t1\tWARNING\tdisk pressure" {
		t.Errorf("expected formatted warning line, got %q (isErr=%v)", line, isErr)
	}
}
