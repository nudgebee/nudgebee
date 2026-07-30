package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/llm/events"
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
