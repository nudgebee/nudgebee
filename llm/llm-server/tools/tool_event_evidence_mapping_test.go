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

// The error-log-spike card must land in its own slot. Left to the generic
// json branch it fell into Others, where only its insight line reached the
// investigation and the whole slot can be dropped when evidence is over budget.
func TestProcessRowWithMessages_MapsLogPatternBreakdown(t *testing.T) {
	card := `{"enriched_labels":{"container_id":"/k8s/shop/api-1/api"},"pattern_breakdown":{"patterns_seen":1,"total":{"peak":705,"normal":9},"patterns":[{"pattern_hash":"aaa","level":"error","peak":700,"normal":2.8}]}}`
	row := buildEvidencesRow(t, []map[string]any{
		{
			"type":            "json",
			"data":            card,
			"additional_info": map[string]any{"action_name": "aggregated_alert_label_enricher"},
			"insight":         []map[string]any{{"message": "Error and critical log lines peaked at 705 per 5 min against 9 normal.", "severity": "Info"}},
		},
	})
	ev := processedEvidence(t, EventsExecuteTool{}.processRowWithMessages(row, 0, 1, nil))

	breakdown, ok := events.DecodeLogPatternBreakdown(ev.LogPatternBreakdown.Data)
	if !ok {
		t.Fatalf("expected the card in the LogPatternBreakdown slot, got %#v (Others=%v)", ev.LogPatternBreakdown.Data, ev.Others)
	}
	assert.Equal(t, 700.0, breakdown.Patterns[0].Peak)
	assert.Equal(t, 9.0, breakdown.Total.Normal)
	assert.Len(t, ev.LogPatternBreakdown.Insight, 1)
	assert.Empty(t, ev.Others)
	assert.Equal(t, ev.LogPatternBreakdown, extractEvidenceByType(ev, "log_pattern_breakdown"))
}

// The same action enriches labels for other alerts with no breakdown. That
// card stays with the generic json evidence, as before.
func TestProcessRowWithMessages_LabelOnlyEnricherCardStaysInOthers(t *testing.T) {
	block := map[string]any{
		"type":            "json",
		"data":            `{"enriched_labels":{"path":"/checkout","method":"GET","status":"503"}}`,
		"additional_info": map[string]any{"action_name": "aggregated_alert_label_enricher"},
	}
	ev := processedEvidence(t, EventsExecuteTool{}.processRowWithMessages(buildEvidencesRow(t, []map[string]any{block}), 0, 1, nil))
	assert.Nil(t, ev.LogPatternBreakdown.Data)
	assert.Len(t, ev.Others, 1)
	assert.Nil(t, extractEvidenceByType(ev, "log_pattern_breakdown"))

	data := map[string]any{"id": "test-event"}
	EventsExecuteTool{}.buildEvidenceManifest(data, []events.Evidence{{Type: "json", Data: block["data"], AdditionalInfo: map[string]any{"action_name": "aggregated_alert_label_enricher"}}})
	types, _ := data["evidences"].(map[string]any)["available_evidence_types"].([]string)
	assert.NotContains(t, types, "log_pattern_breakdown")
}

func TestBuildEvidenceManifest_ListsLogPatternBreakdown(t *testing.T) {
	data := map[string]any{"id": "test-event"}
	evidences := []events.Evidence{
		{
			Type:           "json",
			Data:           `{"pattern_breakdown":{"patterns":[{"pattern_hash":"aaa","level":"error","peak":700}]}}`,
			AdditionalInfo: map[string]any{"action_name": "aggregated_alert_label_enricher"},
		},
	}
	EventsExecuteTool{}.buildEvidenceManifest(data, evidences)
	manifest, ok := data["evidences"].(map[string]any)
	if !ok {
		t.Fatalf("expected manifest map, got %T", data["evidences"])
	}
	types, _ := manifest["available_evidence_types"].([]string)
	assert.Contains(t, types, "log_pattern_breakdown")
}

// A log card that could not hold its whole window says so in additional_info.
// That has to reach the evidence, or a full card reads as the whole window.
func TestProcessRowWithMessages_LogCoverageNote(t *testing.T) {
	entries := []map[string]any{{"timestamp": "2026-01-15T10:19:58Z", "message": "discovery failed", "severity": "ERROR"}}

	t.Run("a cut card states its span and what it kept", func(t *testing.T) {
		row := buildEvidencesRow(t, []map[string]any{logsEvidenceBlock(t, entries, map[string]any{
			"log_window_start":    "2026-01-15T10:14:02Z",
			"log_window_end":      "2026-01-15T10:20:00Z",
			"log_truncated":       true,
			"log_truncation_note": "The queried window has more lines than the 1000 a log card holds.",
		})})
		ev := processedEvidence(t, EventsExecuteTool{FullEvidence: true}.processRowWithMessages(row, 0, 1, nil))
		assert.Equal(t,
			"The collected log lines span 2026-01-15T10:14:02Z to 2026-01-15T10:20:00Z. The queried window has more lines than the 1000 a log card holds.",
			ev.LogCoverageNote)
	})

	t.Run("a whole card states its span only", func(t *testing.T) {
		row := buildEvidencesRow(t, []map[string]any{logsEvidenceBlock(t, entries, map[string]any{
			"log_window_start":    "2026-01-15T10:14:02Z",
			"log_window_end":      "2026-01-15T10:20:00Z",
			"log_truncation_note": "stale note without the flag",
		})})
		ev := processedEvidence(t, EventsExecuteTool{FullEvidence: true}.processRowWithMessages(row, 0, 1, nil))
		assert.Equal(t, "The collected log lines span 2026-01-15T10:14:02Z to 2026-01-15T10:20:00Z.", ev.LogCoverageNote)
	})

	t.Run("a card that says nothing leaves no note", func(t *testing.T) {
		row := buildEvidencesRow(t, []map[string]any{logsEvidenceBlock(t, entries, nil)})
		ev := processedEvidence(t, EventsExecuteTool{FullEvidence: true}.processRowWithMessages(row, 0, 1, nil))
		assert.Empty(t, ev.LogCoverageNote)
	})
}
