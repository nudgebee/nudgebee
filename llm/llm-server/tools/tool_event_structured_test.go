package tools

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/events"
	"nudgebee/llm/security"
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

func TestEventToolsShareTheFilterSchema(t *testing.T) {
	list := (ListEventsTool{}).InputSchema().Properties
	agg := (AggregateEventsTool{}).InputSchema().Properties
	for name := range eventFilterSchema() {
		assert.Contains(t, list, name)
		assert.Contains(t, agg, name)
	}
	for _, name := range []string{"source", "status", "nb_status", "text", "subject_owner", "subject_node", "cluster", "fingerprint"} {
		assert.Contains(t, list, name)
	}
}

func TestEventFilterClauses(t *testing.T) {
	now := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
	where, err := eventFilterClauses(core.NBToolCallRequest{Arguments: map[string]any{
		"subject_owner": "checkout",
		"subject_node":  "ip-10-0-1-2",
		"cluster":       "prod",
		"source":        []any{"datadog", "PagerDuty"},
		"status":        []any{"firing"},
		"nb_status":     "open", // a bare string, not a list
		"fingerprint":   "abc",
		"text":          "it's a timeout",
		"priority":      []any{"HIGH"},
	}}, now)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"subject_owner ILIKE '%checkout%'",
		"subject_node ILIKE '%ip-10-0-1-2%'",
		"cluster ILIKE '%prod%'",
		"priority IN ('HIGH')",
		"status IN ('FIRING')",
		"nb_status IN ('OPEN')",
		"(source ILIKE '%datadog%' OR source ILIKE '%PagerDuty%')",
		"fingerprint = 'abc'",
		"(title ILIKE '%it''s a timeout%' OR description ILIKE '%it''s a timeout%')",
		"starts_at >= '2026-09-08T12:00:00Z'",
	}, where)
}

func TestEventFilterClausesRejectsLongText(t *testing.T) {
	_, err := eventFilterClauses(core.NBToolCallRequest{Arguments: map[string]any{
		"text": strings.Repeat("a", maxEventTextFilterChars+1),
	}}, time.Now())
	assert.Error(t, err)
}

func TestBuildAggregateEventsQuery(t *testing.T) {
	where := []string{"status IN ('FIRING')", "(title ILIKE '%oom%' OR description ILIKE '%oom%')"}
	q, err := buildAggregateEventsQuery([]string{"source", "subject_namespace"}, true, where, 50)
	require.NoError(t, err)
	assert.Equal(t, "SELECT source, subject_namespace, count(DISTINCT fingerprint) AS count, min(starts_at) AS first_seen, max(starts_at) AS last_seen, count(*) OVER () AS total_event_groups "+
		"FROM events WHERE status IN ('FIRING') AND (title ILIKE '%oom%' OR description ILIKE '%oom%') "+
		"GROUP BY source, subject_namespace ORDER BY count DESC LIMIT 50", q)
	// The query still goes through the metastore SQL check; it must pass it.
	assert.NoError(t, sqlValidateReadOnly(q, "events"))

	for _, bad := range [][]string{nil, {"id"}, {"title", "title"}, {"source", "status", "title", "cluster"}, {"source; drop table x"}} {
		_, err := buildAggregateEventsQuery(bad, false, nil, 50)
		assert.Error(t, err, "%v", bad)
	}
}

// The cap holds even when a filter value contains the word "limit", which
// makes buildLimitedQuery skip its own LIMIT.
func TestBuildAggregateEventsQueryKeepsItsLimit(t *testing.T) {
	q, err := buildAggregateEventsQuery([]string{"title"}, false, []string{"(title ILIKE '%rate limit%' OR description ILIKE '%rate limit%')"}, 200)
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(q, " LIMIT 200"))
	assert.Equal(t, q+";", buildLimitedQuery(q, 200), "no second wrapper is needed")
}

func TestBuildManifestOnlyEvidenceDropsEmptyEvidence(t *testing.T) {
	data := []map[string]any{{"id": "a", "evidences": "[]"}}
	buildManifestOnlyEvidence(data)
	assert.NotContains(t, data[0], "evidences")
}

func TestBuildListEventsView(t *testing.T) {
	view := buildListEventsView("a2a30b02-0f67-42e5-a2ab-c658230fd798", []string{
		"priority IN ('HIGH')",
		"starts_at >= '2026-07-01T00:00:00Z'",
	}, 10)

	assert.Contains(t, view, "cloud_account_id = 'a2a30b02-0f67-42e5-a2ab-c658230fd798'::uuid AND priority IN ('HIGH') AND starts_at >= '2026-07-01T00:00:00Z'")
	// Filters apply to the groups and again to the row picked from each.
	assert.Equal(t, 2, strings.Count(view, "priority IN ('HIGH')"))
	assert.Contains(t, view, "GROUP BY aggregation_key, subject_name, subject_type, subject_namespace, fingerprint ORDER BY last_starts_at DESC LIMIT 10")
	assert.Contains(t, view, "e.fingerprint IS NOT DISTINCT FROM g.fingerprint")
	// Never the whole evidence blob.
	assert.NotContains(t, view, "e.evidences::text")
	assert.NotContains(t, view, "r.evidences::text")
}

func TestAppendDefaultEventStartOnlyWhenNoExplicitStart(t *testing.T) {
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)

	withDefault := appendDefaultEventStart([]string{"priority = 'HIGH'"}, "", now)
	assert.Equal(t, []string{"priority = 'HIGH'", "starts_at >= '2026-08-02T12:00:00Z'"}, withDefault)

	explicit := []string{"starts_at >= '2026-01-01T00:00:00Z'"}
	assert.Equal(t, explicit, appendDefaultEventStart(explicit, "2026-01-01T00:00:00Z", now))
}

// metricSeriesJSON builds a valid JSON metric document, larger than
// maxEvidenceInsightDataChars, shaped like the pod_metrics evidence the
// collector stores as a string.
func metricSeriesJSON(t *testing.T, points int) string {
	t.Helper()
	values := make([]string, points)
	timestamps := make([]int64, points)
	for i := range values {
		values[i] = "2097152000"
		timestamps[i] = 1787408040 + int64(i*30)
	}
	doc, err := json.Marshal(map[string]any{
		"data": map[string]any{
			"memory_usage": map[string]any{
				"series_list_result": []any{map[string]any{"timestamps": timestamps, "values": values}},
			},
		},
	})
	require.NoError(t, err)
	require.Greater(t, len(doc), maxEvidenceInsightDataChars)
	return string(doc)
}

// Regression (dev events 2271fe6b, f1580e90, f6d8fa9e, 509532e3): the workspace
// file get_event_by_id points agents to as "full evidence" was written AFTER the
// per-field 3000-char cap, so every JSON-string field over the cap (pod_metrics,
// pod_data, others) was invalid JSON and json.load failed at char ~3000.
func TestOffloadEventEvidence_WorkspaceFileHoldsUncappedValidJSON(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	metrics := metricSeriesJSON(t, 400)
	podData := metricSeriesJSON(t, 300)
	evidence := events.InvestigateData{
		PodMetrics: []events.InvestigateDataInsight{{Data: metrics, Title: "json"}},
		PodData:    events.InvestigateDataInsight{Data: podData},
	}

	var savedPath, savedContent string
	wm := &mockWorkspaceManager{
		saveFileFunc: func(ctx *security.RequestContext, accountId, conversationId, path, content string) error {
			savedPath, savedContent = path, content
			return nil
		},
	}

	inline, refs := offloadEventEvidence(sc, wm, "acct-1", "conv-1", "evt-1", evidence)

	assert.Equal(t, "event_evt-1_evidence.json", savedPath)
	var saved events.InvestigateData
	require.NoError(t, json.Unmarshal([]byte(savedContent), &saved))
	require.Len(t, saved.PodMetrics, 1)
	assert.Equal(t, metrics, saved.PodMetrics[0].Data, "file must carry the uncapped metric document")
	assert.True(t, json.Valid([]byte(saved.PodMetrics[0].Data.(string))))
	assert.Equal(t, podData, saved.PodData.Data)

	inlineStr, ok := inline.(string)
	require.True(t, ok)
	assert.Contains(t, inlineStr, "Evidence saved to event_evt-1_evidence.json with every section complete")
	assert.Contains(t, inlineStr, "evidence_type=logs", "log_data is reduced by enrichData, so the note must not claim the logs are complete")
	assert.Contains(t, inlineStr, "pod_metrics=", "preview lists the sections available in the file")
	assert.NotContains(t, inlineStr, `"timestamps"`, "raw series stay in the file, not inline")
	require.Len(t, refs, 1)
	assert.Equal(t, "event_evt-1_evidence.json", refs[0].Url)
}

func TestOffloadEventEvidence_SaveFailureFallsBackToCappedInline(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	evidence := events.InvestigateData{
		PodMetrics: []events.InvestigateDataInsight{{Data: metricSeriesJSON(t, 400)}},
	}
	wm := &mockWorkspaceManager{
		saveFileFunc: func(ctx *security.RequestContext, accountId, conversationId, path, content string) error {
			return errors.New("workspace down")
		},
	}

	inline, refs := offloadEventEvidence(sc, wm, "acct-1", "conv-1", "evt-1", evidence)

	assert.Nil(t, refs)
	capped, ok := inline.(events.InvestigateData)
	require.True(t, ok)
	assert.LessOrEqual(t, len(capped.PodMetrics[0].Data.(string)), maxEvidenceInsightDataChars+len("\n... (truncated)"))
}

func TestOffloadEventEvidence_SmallEvidenceIsNotCapped(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	many := make([]events.InvestigateDataInsight, maxEvidenceInsightEntries+5)
	for i := range many {
		many[i] = events.InvestigateDataInsight{Data: "e"}
	}
	evidence := events.InvestigateData{PodEvents: many}
	wm := &mockWorkspaceManager{
		saveFileFunc: func(ctx *security.RequestContext, accountId, conversationId, path, content string) error {
			t.Fatal("SaveFile must not be called for evidence under the overflow threshold")
			return nil
		},
	}

	inline, refs := offloadEventEvidence(sc, wm, "acct-1", "conv-1", "evt-1", evidence)

	assert.Nil(t, refs)
	got, ok := inline.(events.InvestigateData)
	require.True(t, ok)
	assert.Len(t, got.PodEvents, maxEvidenceInsightEntries+5, "evidence under the threshold is returned whole, without the entry cap")
}

func TestOffloadEventEvidence_SmallEvidenceStaysInlineWithoutSave(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	evidence := events.InvestigateData{Markdowns: []events.InvestigateDataInsight{{Data: "restart count: 2"}}}
	wm := &mockWorkspaceManager{
		saveFileFunc: func(ctx *security.RequestContext, accountId, conversationId, path, content string) error {
			t.Fatal("SaveFile must not be called for evidence under the overflow threshold")
			return nil
		},
	}

	inline, refs := offloadEventEvidence(sc, wm, "acct-1", "conv-1", "evt-1", evidence)

	assert.Nil(t, refs)
	assert.Equal(t, evidence, inline)
}

// The inline result must stay small however heavy the evidence is: the file
// holds the data, the preview only orients the model.
func TestOffloadEventEvidence_InlinePreviewIsCompact(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	errLines := make([]string, 500)
	for i := range errLines {
		errLines[i] = strings.Repeat("warning: previous definition ", 40)
	}
	evidence := events.InvestigateData{
		LogData:      strings.Repeat("log line\n", 20000),
		ErrorLogData: errLines,
		PodMetrics:   []events.InvestigateDataInsight{{Data: metricSeriesJSON(t, 400)}},
		PodEvents:    []events.InvestigateDataInsight{{Data: map[string]any{"reason": "BackOff", "message": strings.Repeat("Back-off restarting failed container ", 200)}}},
		PodData:      events.InvestigateDataInsight{Data: map[string]any{"lastState": map[string]any{"terminated": map[string]any{"reason": "OOMKilled", "exitCode": 137}}}},
	}
	wm := &mockWorkspaceManager{
		saveFileFunc: func(ctx *security.RequestContext, accountId, conversationId, path, content string) error { return nil },
	}

	inline, refs := offloadEventEvidence(sc, wm, "acct-1", "conv-1", "evt-1", evidence)

	inlineStr, ok := inline.(string)
	require.True(t, ok)
	assert.Less(t, len(inlineStr), eventEvidencePreviewBytes+1500, "instruction text plus a preview bounded by eventEvidencePreviewBytes")
	assert.Contains(t, inlineStr, "error_log_data (500 lines, 1 distinct patterns, first 1 of them)")
	assert.Contains(t, inlineStr, "log_data[evidence_type=logs]=180000", "sizes come from the uncapped evidence")
	assert.Contains(t, inlineStr, "evt-1_evidence.json")
	assert.Contains(t, inlineStr, "pod_events: ", "root-cause slices are inlined so crash loops need no second call")
	assert.Contains(t, inlineStr, "OOMKilled")
	assert.Contains(t, inlineStr, "Sections in the file (bytes):")
	assert.Contains(t, inlineStr, "log_data[evidence_type=logs]=180000", "section list is not cut off by the facts above it")
	assert.Contains(t, inlineStr, "get_event_evidence")
	assert.NotContains(t, inlineStr, "shell_execute", "the events agent has no shell_execute")
	require.Len(t, refs, 1)
}

// get_event_evidence rejects names it does not know, and the model reads
// "No evidence found" as "the data does not exist". Every section in the
// preview must therefore carry a name the tool accepts, or say it is file-only.
func TestEventEvidencePreview_SectionsNameValidEvidenceTypes(t *testing.T) {
	big := events.InvestigateDataInsight{Data: strings.Repeat("x", 50)}
	id := events.InvestigateData{
		LogData:        "line",
		MetricsData:    []events.InvestigateDataInsight{big},
		RDBMSQueryData: []events.InvestigateDataInsight{big},
		Markdowns:      []events.InvestigateDataInsight{big},
		Others:         []events.InvestigateDataInsight{big},
		NodeData:       big,
		JobPodEvents:   big,
		ServiceMap:     big,
		PodMetrics:     []events.InvestigateDataInsight{big},
	}
	out := buildEventEvidencePreview(id)

	for _, want := range []string{
		"log_data[evidence_type=logs]=",
		"metrics_queries_data[evidence_type=metrics_data]=",
		"rdbms_query_data[evidence_type=rdbms_query_response]=",
		"markdown_data[evidence_type=markdowns]=",
		"others[file only]=",
		"node_data[file only]=",
		"job_pod_events[file only]=",
		"service_map_data[file only]=",
		" pod_metrics=",
	} {
		assert.Contains(t, out, want)
	}
	// Every named evidence_type must be one extractEvidenceByType handles.
	for _, m := range regexp.MustCompile(`evidence_type=(\w+)\]`).FindAllStringSubmatch(out, -1) {
		assert.NotNil(t, extractEvidenceByType(events.InvestigateData{
			LogData: "x", MetricsData: id.MetricsData, RDBMSQueryData: id.RDBMSQueryData, Markdowns: id.Markdowns,
		}, m[1]), "evidence_type %q is not accepted by get_event_evidence", m[1])
	}
}

// The OOMKilled block is appended last and each block marshals its raw rows
// before its insight, so a plain cut from the front loses the cause. The slice
// must lead with the OOMKilled block and with insights, not rows.
func TestRootCauseSlice_KeepsOOMKilledInsightFirst(t *testing.T) {
	rows := strings.Repeat("row ", 500)
	blocks := []events.InvestigateDataInsight{
		{Title: "Pod events", Data: rows, Insight: []events.Insight{{Message: "pod restarted 5 times"}}},
		{Title: oomKilledTitle, Data: rows, Insight: []events.Insight{{Message: "container killed: OOMKilled exit 137"}}},
	}
	out := rootCauseSlice(blocks, previewRootCauseChars)

	assert.LessOrEqual(t, len(out), previewRootCauseChars)
	assert.Contains(t, out, "OOMKilled exit 137")
	assert.Contains(t, out, "pod restarted 5 times")
	assert.Less(t, strings.Index(out, oomKilledTitle), strings.Index(out, "Pod events"), "OOMKilled block comes first")
	assert.Contains(t, out, `data: "row`, "leftover budget carries raw rows")
}

// Sections whose raw data stays in the file (others, markdown_data, traces)
// carry their lead in one-line insights, e.g. "Deployment occurred 48 minutes
// before the event". Those must reach the model, worst first, without
// duplicates, and without repeating what the pod slices already carry.
func TestEventEvidencePreview_InlinesInsightsWorstFirst(t *testing.T) {
	big := strings.Repeat("x", 5000)
	id := events.InvestigateData{
		Others: []events.InvestigateDataInsight{
			{Data: big, Insight: []events.Insight{
				{Message: "Found 4 deployment(s) in the 7 days before the event", Severity: "info"},
				{Message: "Deployment occurred 48 minutes before the event - potentially related", Severity: "warning"},
			}},
			{Data: big, Insight: []events.Insight{{Message: "Found 4 deployment(s) in the 7 days before the event", Severity: "info"}}},
		},
		Traces:    events.InvestigateDataInsight{Data: big, Insight: []events.Insight{{Message: "1x [http] rag-server POST status=400", Severity: "Critical"}}},
		PodEvents: []events.InvestigateDataInsight{{Data: big, Insight: []events.Insight{{Message: "pod restarted 5 times", Severity: "warning"}}}},
	}
	out := buildEventEvidencePreview(id)

	assert.Contains(t, out, "[others] Deployment occurred 48 minutes before the event")
	assert.Contains(t, out, "[traces] 1x [http] rag-server POST status=400")
	assert.Equal(t, 1, strings.Count(out, "Found 4 deployment(s)"), "duplicate messages are dropped")
	assert.Less(t, strings.Index(out, "[traces]"), strings.Index(out, "[others] Deployment occurred"), "critical before warning")
	assert.Less(t, strings.Index(out, "[others] Deployment occurred"), strings.Index(out, "Found 4 deployment(s)"), "warning before info")
	assert.NotContains(t, out, "[pod_events]", "pod_events insights are carried by rootCauseSlice")
}

func TestInsightHighlights_StaysWithinBudget(t *testing.T) {
	var items []events.InvestigateDataInsight
	for i := 0; i < 50; i++ {
		items = append(items, events.InvestigateDataInsight{Insight: []events.Insight{{Message: strings.Repeat("m", 200) + string(rune('a'+i%26)) + strings.Repeat("n", i), Severity: "info"}}})
	}
	out := insightHighlights([]previewSection{{name: "others", items: items}}, previewInsightBytes)

	assert.LessOrEqual(t, len(out), previewInsightBytes)
	assert.NotEmpty(t, out)
}

// The first N raw error lines are usually one message repeated; the lines that
// show other collections failing too come later. One line per distinct
// pattern keeps them in view.
func TestDistinctErrorLines_OneLinePerPattern(t *testing.T) {
	mk := func(ts, trace, coll string) string {
		return `2026-10-06T12:11:` + ts + `Z ERROR {"trace_id": "` + trace + `", "message": "Error searching collection ` + coll + `: 400"}`
	}
	var lines []string
	for i := 0; i < 8; i++ {
		lines = append(lines, mk("4"+string(rune('0'+i)), "ff77ce25e6f51ee9297027c0f963c7d5", "nudgebee_docs"))
	}
	lines = append(lines,
		mk("50", "213ba7fe3ba205afd00aa4092b0a7f80", "6bb67903-bf24-4f18-ad55-4e874d9303d7_memory_patterns"),
		mk("51", "2b3adbbee9acca99679a6e3fea8a4ef1", "6bb67903-bf24-4f18-ad55-4e874d9303d7_memory_decisions"),
		mk("52", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "6bb67903-bf24-4f18-ad55-4e874d9303d7_memory_patterns"),
	)

	out, patterns := distinctErrorLines(lines, 10)

	assert.Equal(t, 3, patterns)
	require.Len(t, out, 3)
	assert.Contains(t, out[0], "nudgebee_docs")
	assert.Contains(t, out[1], "memory_patterns")
	assert.Contains(t, out[2], "memory_decisions")

	capped, total := distinctErrorLines(lines, 2)
	assert.Len(t, capped, 2)
	assert.Equal(t, 3, total, "the pattern count is not limited by the line limit")
}

// get_event_by_id must filter on the uuid column inside the view so the
// lookup uses the primary key, not a scan of the account's events.
func TestByIDViewFiltersOnTheUUIDColumn(t *testing.T) {
	query := "SELECT id, title FROM events WHERE id = 'c0f89551-c23d-49ea-bd8d-d0291170e66b'"
	view := byIDView("a2a30b02-0f67-42e5-a2ab-c658230fd798", query)
	assert.Contains(t, view, "FROM events WHERE id = 'c0f89551-c23d-49ea-bd8d-d0291170e66b'::uuid AND cloud_account_id = 'a2a30b02-0f67-42e5-a2ab-c658230fd798'::uuid")
}
