package tools

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/events"
	"nudgebee/llm/tools/core"
)

func TestCapInsight_TruncatesOversizedData(t *testing.T) {
	insight := events.InvestigateDataInsight{Data: strings.Repeat("x", maxEvidenceInsightDataChars+500)}

	capped := capInsight(insight)

	dataStr, ok := capped.Data.(string)
	assert.True(t, ok)
	assert.LessOrEqual(t, len(dataStr), maxEvidenceInsightDataChars+len("\n... (truncated)"))
	assert.Contains(t, dataStr, "... (truncated)")
}

// Regression: capInsight used to fmt.Sprintf("%v", ...) a non-string Data
// value before truncating, producing Go's debug syntax (map[k:v ...],
// scientific-notation floats) instead of valid JSON — more verbose than the
// equivalent JSON for the same data, and not parseable by anything expecting
// JSON (including a model asked to reproduce it). It must JSON-marshal
// instead.
func TestCapInsight_JSONMarshalsNonStringDataInsteadOfGoSyntaxDump(t *testing.T) {
	// Large enough (> maxEvidenceInsightDataChars once marshaled) that
	// capInsight actually stringifies it, the same way a real Prometheus
	// matrix result crosses the cap.
	timestamps := make([]float64, 500)
	for i := range timestamps {
		timestamps[i] = 1789018406 + float64(i)
	}
	insight := events.InvestigateDataInsight{
		Data: map[string]any{
			"result_type": "matrix",
			"timestamps":  timestamps,
		},
	}

	capped := capInsight(insight)

	dataStr, ok := capped.Data.(string)
	require.True(t, ok)
	assert.NotContains(t, dataStr, "map[", "must not fall back to Go's %v debug syntax")
	assert.Contains(t, dataStr, `"result_type":"matrix"`)
	assert.Contains(t, dataStr, "1789018406", "must not switch to scientific notation the way %v does")
}

func TestCapInsight_LeavesSmallDataUntouched(t *testing.T) {
	insight := events.InvestigateDataInsight{Data: "small"}

	capped := capInsight(insight)

	assert.Equal(t, "small", capped.Data)
}

func TestTruncateAtRuneBoundary_NonPositiveMaxBytesReturnsEmpty(t *testing.T) {
	assert.Equal(t, "", truncateAtRuneBoundary("hello", 0))
	assert.Equal(t, "", truncateAtRuneBoundary("hello", -1))
}

func TestCapInsight_DoesNotSplitMultiByteRune(t *testing.T) {
	// "日" is 3 bytes; place one right at the maxEvidenceInsightDataChars boundary
	// so a naive byte-index slice would cut it in half.
	data := strings.Repeat("x", maxEvidenceInsightDataChars-1) + "日本語"
	insight := events.InvestigateDataInsight{Data: data}

	capped := capInsight(insight)

	dataStr, ok := capped.Data.(string)
	assert.True(t, ok)
	truncated := strings.TrimSuffix(dataStr, "\n... (truncated)")
	assert.True(t, utf8.ValidString(truncated), "truncated string must not split a UTF-8 rune")
}

func TestCapInsightSlice_CapsEntryCountAndSize(t *testing.T) {
	insights := make([]events.InvestigateDataInsight, maxEvidenceInsightEntries+10)
	for i := range insights {
		insights[i] = events.InvestigateDataInsight{Data: strings.Repeat("y", maxEvidenceInsightDataChars+100)}
	}

	capped := capInsightSlice(insights)

	assert.Len(t, capped, maxEvidenceInsightEntries)
	for _, insight := range capped {
		dataStr, ok := insight.Data.(string)
		assert.True(t, ok)
		assert.LessOrEqual(t, len(dataStr), maxEvidenceInsightDataChars+len("\n... (truncated)"))
	}
}

func TestCapInvestigateDataEvidence_BoundsHeavyEvent(t *testing.T) {
	heavy := events.InvestigateData{
		PodEvents: make([]events.InvestigateDataInsight, 200),
	}
	for i := range heavy.PodEvents {
		heavy.PodEvents[i] = events.InvestigateDataInsight{Data: strings.Repeat("crash-loop pod event\n", 500)}
	}
	heavy.AlertLabels = events.InvestigateDataInsight{Data: strings.Repeat("label=value;", 100)}

	capInvestigateDataEvidence(&heavy)

	assert.Len(t, heavy.PodEvents, maxEvidenceInsightEntries)
	for _, insight := range heavy.PodEvents {
		dataStr, ok := insight.Data.(string)
		assert.True(t, ok)
		assert.LessOrEqual(t, len(dataStr), maxEvidenceInsightDataChars+len("\n... (truncated)"))
	}
	labelStr, ok := heavy.AlertLabels.Data.(string)
	assert.True(t, ok)
	assert.Equal(t, strings.Repeat("label=value;", 100), labelStr, "well under the per-field cap, so untouched")
}

// Regression: per-field capping (capInsight/capInsightSlice) only bounds each
// field individually — an event with several moderately-sized fields can
// still sum to hundreds of KB even with every per-field cap applied (see
// maxTotalEvidenceChars's comment for the worst-case math). This event's
// three slice fields are deliberately sized so only PodEvents (the largest
// after per-field capping) needs to be dropped to get back under budget;
// NodeEvents and Others survive with their real, per-field-capped data.
func TestCapInvestigateDataEvidence_AggregateBudgetDropsLargestFieldOnly(t *testing.T) {
	heavy := events.InvestigateData{
		// 200 oversized items -> capped to maxEvidenceInsightEntries (15) x
		// (maxEvidenceInsightDataChars + truncation marker) = the largest
		// single field once every per-field cap is applied.
		PodEvents: make([]events.InvestigateDataInsight, 200),
		// Only 10 oversized items (<= maxEvidenceInsightEntries), so the
		// entry count survives uncapped — smaller total than PodEvents.
		NodeEvents: make([]events.InvestigateDataInsight, 10),
		// Only 5 oversized items — smaller still.
		Others: make([]events.InvestigateDataInsight, 5),
	}
	for i := range heavy.PodEvents {
		heavy.PodEvents[i] = events.InvestigateDataInsight{Data: strings.Repeat("crash-loop pod event\n", 500)}
	}
	for i := range heavy.NodeEvents {
		heavy.NodeEvents[i] = events.InvestigateDataInsight{Data: strings.Repeat("node pressure event\n", 500)}
	}
	for i := range heavy.Others {
		heavy.Others[i] = events.InvestigateDataInsight{Data: strings.Repeat("other evidence\n", 500)}
	}
	heavy.AlertLabels = events.InvestigateDataInsight{Data: strings.Repeat("label=value;", 100)}

	capInvestigateDataEvidence(&heavy)

	total := insightSliceSize(heavy.PodEvents) + insightSliceSize(heavy.NodeEvents) + insightSliceSize(heavy.Others) +
		insightSize(heavy.AlertLabels)
	assert.LessOrEqual(t, total, maxTotalEvidenceChars, "combined size of every field must stay within the aggregate budget")

	// PodEvents (the largest field) was dropped: collapsed to a single
	// pointer entry rather than its real, per-field-capped data.
	require.Len(t, heavy.PodEvents, 1)
	droppedStr, ok := heavy.PodEvents[0].Data.(string)
	require.True(t, ok)
	assert.Contains(t, droppedStr, "omitted")
	assert.Contains(t, droppedStr, "get_event_evidence")
	assert.Contains(t, droppedStr, "pod_events")

	// Smaller fields survive with their real data, untouched by the
	// aggregate pass.
	assert.Len(t, heavy.NodeEvents, 10)
	assert.Len(t, heavy.Others, 5)
	for _, insight := range heavy.NodeEvents {
		dataStr, ok := insight.Data.(string)
		require.True(t, ok)
		assert.NotContains(t, dataStr, "omitted")
	}
}

// Regression: capTracesInsight/capAlertLabelsInsight's Data must stay a
// map/[]any for downstream consumers (see their doc comments) — the
// aggregate pass must count them toward the total without ever replacing
// their Data with the plain-string "omitted" marker, even when they're the
// largest fields present.
func TestCapInvestigateDataEvidence_AggregateBudgetNeverDropsTracesOrAlertLabels(t *testing.T) {
	heavy := events.InvestigateData{
		Traces: events.InvestigateDataInsight{
			Data: map[string]any{"data": make([]any, 5)},
		},
		AlertLabels: events.InvestigateDataInsight{
			Data: make([]any, 5),
		},
		Others: make([]events.InvestigateDataInsight, 200),
	}
	for i := range heavy.Others {
		heavy.Others[i] = events.InvestigateDataInsight{Data: strings.Repeat("other evidence\n", 500)}
	}

	capInvestigateDataEvidence(&heavy)

	_, tracesStillMap := heavy.Traces.Data.(map[string]any)
	assert.True(t, tracesStillMap, "Traces.Data must never become a plain string")
	_, alertLabelsStillSlice := heavy.AlertLabels.Data.([]any)
	assert.True(t, alertLabelsStillSlice, "AlertLabels.Data must never become a plain string")
}

// Regression: capInsight's generic byte-truncation replaces a map/slice Data
// with a plain string, which crashes agent_events.go's reduceEventData (it
// expects Traces.Data to stay map[string]any and AlertLabels.Data to stay
// []any). capTracesInsight/capAlertLabelsInsight must cap by shrinking the
// inner array instead, preserving the shape reduceEventData relies on.
func TestCapTracesInsight_CapsSpanCountWithoutChangingShape(t *testing.T) {
	spans := make([]any, maxEvidenceInsightEntries+50)
	for i := range spans {
		spans[i] = map[string]any{"span_id": i}
	}
	insight := events.InvestigateDataInsight{Data: map[string]any{"data": spans}}

	capped := capTracesInsight(insight)

	tracesMap, ok := capped.Data.(map[string]any)
	assert.True(t, ok, "Traces.Data must stay a map, not collapse to a string")
	cappedSpans, ok := tracesMap["data"].([]any)
	assert.True(t, ok)
	assert.Len(t, cappedSpans, maxEvidenceInsightEntries)
}

func TestCapTracesInsight_FallsBackToCapInsightForNonMapData(t *testing.T) {
	insight := events.InvestigateDataInsight{Data: strings.Repeat("x", maxEvidenceInsightDataChars+500)}

	capped := capTracesInsight(insight)

	dataStr, ok := capped.Data.(string)
	assert.True(t, ok)
	assert.LessOrEqual(t, len(dataStr), maxEvidenceInsightDataChars+len("\n... (truncated)"))
}

func TestCapAlertLabelsInsight_CapsLabelCountWithoutChangingShape(t *testing.T) {
	labels := make([]any, maxEvidenceInsightEntries+50)
	for i := range labels {
		labels[i] = map[string]any{"label": "l", "value": i}
	}
	insight := events.InvestigateDataInsight{Data: labels}

	capped := capAlertLabelsInsight(insight)

	cappedLabels, ok := capped.Data.([]any)
	assert.True(t, ok, "AlertLabels.Data must stay a []any, not collapse to a string")
	assert.Len(t, cappedLabels, maxEvidenceInsightEntries)
}

func TestCapAlertLabelsInsight_FallsBackToCapInsightForNonSliceData(t *testing.T) {
	insight := events.InvestigateDataInsight{Data: strings.Repeat("label=value;", 10000)}

	capped := capAlertLabelsInsight(insight)

	dataStr, ok := capped.Data.(string)
	assert.True(t, ok)
	assert.LessOrEqual(t, len(dataStr), maxEvidenceInsightDataChars+len("\n... (truncated)"))
}

func TestAggregateEventsToolSchemaIncludesPriorityFilter(t *testing.T) {
	schema := (AggregateEventsTool{}).InputSchema()
	priority, ok := schema.Properties["priority"]
	assert.True(t, ok)
	assert.Equal(t, core.ToolSchemaTypeArray, priority.Type)
}

func TestBuildListEventsMetadataViewFiltersBeforeDedupWithoutEvidence(t *testing.T) {
	view := buildListEventsMetadataView("a2a30b02-0f67-42e5-a2ab-c658230fd798", []string{
		"priority IN ('HIGH')",
		"starts_at >= '2026-07-01T00:00:00Z'",
	})

	assert.Contains(t, view, "cloud_account_id = 'a2a30b02-0f67-42e5-a2ab-c658230fd798'::uuid")
	assert.Contains(t, view, "priority IN ('HIGH')")
	assert.Contains(t, view, "starts_at >= '2026-07-01T00:00:00Z'")
	assert.Contains(t, view, "COUNT(*) OVER")
	assert.Contains(t, view, "ROW_NUMBER() OVER")
	assert.NotContains(t, view, "evidences")
	assert.Less(t, strings.Index(view, "priority IN ('HIGH')"), strings.Index(view, ") ranked_events"))
}

func TestAppendDefaultEventStartOnlyWhenNoExplicitStart(t *testing.T) {
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)

	withDefault := appendDefaultEventStart([]string{"priority = 'HIGH'"}, "", now)
	assert.Equal(t, []string{"priority = 'HIGH'", "starts_at >= '2026-08-02T12:00:00Z'"}, withDefault)

	explicit := []string{"starts_at >= '2026-01-01T00:00:00Z'"}
	assert.Equal(t, explicit, appendDefaultEventStart(explicit, "2026-01-01T00:00:00Z", now))
}

func TestBuildListEventsEvidenceViewUsesOnlySelectedUUIDs(t *testing.T) {
	view := buildListEventsEvidenceView("550e8400-e29b-41d4-a716-446655440000", []map[string]any{
		{"id": "fca7480a-496b-4654-8453-63058aa6c21e"},
		{"id": "99dffbb0-0a79-4427-ac74-e4e919adbbf7"},
		{"id": "not-a-uuid"},
	})

	assert.Equal(t, "SELECT id::text, evidences::text FROM events WHERE cloud_account_id = "+
		"'550e8400-e29b-41d4-a716-446655440000'::uuid AND id IN ("+
		"'fca7480a-496b-4654-8453-63058aa6c21e'::uuid, '99dffbb0-0a79-4427-ac74-e4e919adbbf7'::uuid)", view)
	assert.NotContains(t, view, "not-a-uuid")
	assert.Empty(t, buildListEventsEvidenceView("550e8400-e29b-41d4-a716-446655440000", []map[string]any{{"title": "missing id"}}))
}

func TestMergeEvidenceManifestsPreservesMetadataOrderAndMissingEvidence(t *testing.T) {
	firstManifest := map[string]any{"has_logs": true}
	secondManifest := map[string]any{"has_metrics": true}
	data := []map[string]any{
		{"id": "event-2", "title": "second"},
		{"id": "event-1", "title": "first"},
		{"id": "event-missing", "title": "missing"},
	}
	evidenceData := []map[string]any{
		{"id": "event-1", "evidences": firstManifest},
		{"id": "event-2", "evidences": secondManifest},
	}

	mergeEvidenceManifests(data, evidenceData)

	assert.Equal(t, "event-2", data[0]["id"])
	assert.Equal(t, secondManifest, data[0]["evidences"])
	assert.Equal(t, "event-1", data[1]["id"])
	assert.Equal(t, firstManifest, data[1]["evidences"])
	assert.NotContains(t, data[2], "evidences")
}
