package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"nudgebee/llm/events"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// logPatternBreakdownCardJSON is the card the api-server writes. The same
// literal is in
// api-server/services/observability/eventrule_action_log_pattern_breakdown_test.go;
// keep the two identical so a change to the shape fails on both sides.
const logPatternBreakdownCardJSON = `{
  "window_start": "2026-01-15T10:00:00Z",
  "window_end": "2026-01-15T10:20:00Z",
  "bucket_minutes": 5,
  "baseline_hours": 6,
  "baseline_evaluated_at": "2026-01-15T09:59:00Z",
  "baseline_known": true,
  "counted_levels": ["error", "critical"],
  "counting_note": "Counts only log lines the log agent tagged error or critical. The tag comes from the line's own level field, or from a level word at the start of the line. A warning, or a line that only mentions \"error\" further in, is not counted.",
  "total": {"peak": 705, "peak_at": "2026-01-15T10:10:00Z", "normal": 9},
  "patterns_seen": 3,
  "patterns": [
    {"pattern_hash": "aaa", "level": "error", "peak": 700, "peak_at": "2026-01-15T10:10:00Z", "normal": 2.8, "example": "ERROR relay query failed", "container_id": "/k8s/shop/api-1/api"},
    {"pattern_hash": "bbb", "level": "error", "peak": 264, "peak_at": "2026-01-15T10:15:00Z", "normal": 0, "example": "ERROR discovery failed", "container_id": "/k8s/shop/api-1/api"},
    {"pattern_hash": "ccc", "level": "critical", "peak": 5, "peak_at": "2026-01-15T10:00:00Z", "normal": 5, "example": "CRITICAL heartbeat late", "container_id": "/k8s/shop/api-2/api"}
  ]
}`

// breakdownEvidence wraps a breakdown the way the events tool hands it over:
// the card's data decoded to an object, under "pattern_breakdown".
func breakdownEvidence(t *testing.T, breakdownJSON string) events.InvestigateData {
	t.Helper()
	var breakdown map[string]any
	require.NoError(t, json.Unmarshal([]byte(breakdownJSON), &breakdown))
	return events.InvestigateData{LogPatternBreakdown: events.InvestigateDataInsight{
		Data: map[string]any{"enriched_labels": map[string]any{}, "pattern_breakdown": breakdown},
	}}
}

func TestRenderLogPatternBreakdown(t *testing.T) {
	got := renderLogPatternBreakdown(breakdownEvidence(t, logPatternBreakdownCardJSON))
	want := `Counts only log lines the log agent tagged error or critical. The tag comes from the line's own level field, or from a level word at the start of the line. A warning, or a line that only mentions "error" further in, is not counted.
Window 2026-01-15 10:00 → 10:20 UTC. Counts are per 5 minutes; a time below is the end of those 5 minutes.
Normal is the average per 5 min over the 6 h before 09:59. It was measured before the alert fired, so a ratio here can be higher than the one in the alert's description, whose 6 h already held the start of the spike.
Total: peak 705 per 5 min at 10:10 vs 9 normal (78×).
Patterns that rose most (2 shown, 3 seen in the window):
- error: peak 700 at 10:10 vs 2.8 normal (250×) — e.g. ` + "`ERROR relay query failed`" + `
- error: peak 264 at 10:15, not seen in the 6 h before — e.g. ` + "`ERROR discovery failed`" + `
An example line shows what a pattern looks like; a time inside it is when the pattern was first seen, not the spike.
`
	assert.Equal(t, want, got)
}

func TestRenderLogPatternBreakdown_NoErrorsBeforeTheWindow(t *testing.T) {
	got := renderLogPatternBreakdown(breakdownEvidence(t, `{
  "window_start": "2026-01-15T23:50:00Z", "window_end": "2026-01-16T00:20:00Z",
  "bucket_minutes": 5, "baseline_hours": 6, "baseline_evaluated_at": "2026-01-15T23:50:00Z", "baseline_known": true,
  "total": {"peak": 40.5, "peak_at": "2026-01-16T00:05:00Z", "normal": 0},
  "patterns_seen": 1,
  "patterns": [{"pattern_hash": "new", "level": "critical", "peak": 40.5, "peak_at": "2026-01-16T00:05:00Z", "normal": 0, "example": "FATAL out of\nmemory `+"`x`"+`"}]
}`))
	assert.Contains(t, got, "Window 2026-01-15 23:50 → 2026-01-16 00:20 UTC")
	assert.Contains(t, got, "Total: peak 40.5 per 5 min at 00:05; no error or critical line in the 6 h before.")
	assert.Contains(t, got, "- critical: peak 40.5 at 00:05, not seen in the 6 h before — e.g. `FATAL out of memory 'x'`")
}

func TestRenderLogPatternBreakdown_NothingRose(t *testing.T) {
	got := renderLogPatternBreakdown(breakdownEvidence(t, `{
  "window_start": "2026-01-15T10:00:00Z", "window_end": "2026-01-15T10:20:00Z",
  "bucket_minutes": 5, "baseline_hours": 6, "baseline_evaluated_at": "2026-01-15T10:00:00Z", "baseline_known": true,
  "total": {"peak": 5, "peak_at": "2026-01-15T10:00:00Z", "normal": 5},
  "patterns_seen": 1,
  "patterns": [{"pattern_hash": "ccc", "level": "critical", "peak": 5, "peak_at": "2026-01-15T10:00:00Z", "normal": 5, "example": "CRITICAL heartbeat late"}]
}`))
	assert.Contains(t, got, "No single pattern was above its own normal; 1 seen in the window.")
	assert.NotContains(t, got, "heartbeat late")
}

// The section has its own budget and is never cut mid-line: it lists whole
// patterns and says how many of those seen it shows.
func TestRenderLogPatternBreakdown_StaysInBudgetWithWholeLines(t *testing.T) {
	patterns := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		patterns = append(patterns, fmt.Sprintf(
			`{"pattern_hash": "p%d", "level": "error", "peak": %d, "peak_at": "2026-01-15T10:10:00Z", "normal": 1, "example": %q}`,
			i, 1000-i, strings.Repeat("é", 300)))
	}
	got := renderLogPatternBreakdown(breakdownEvidence(t, `{
  "window_start": "2026-01-15T10:00:00Z", "window_end": "2026-01-15T10:20:00Z",
  "bucket_minutes": 5, "baseline_hours": 6, "baseline_evaluated_at": "2026-01-15T10:00:00Z", "baseline_known": true,
  "counting_note": "`+strings.Repeat("what is counted ", 100)+`",
  "total": {"peak": 9000, "peak_at": "2026-01-15T10:10:00Z", "normal": 10},
  "patterns_seen": 37,
  "patterns": [`+strings.Join(patterns, ",")+`]
}`))

	assert.LessOrEqual(t, len(got), maxLogPatternBreakdownBytes)
	shown := strings.Count(got, "\n- error: peak ")
	assert.Positive(t, shown)
	assert.LessOrEqual(t, shown, maxLogPatternBreakdownPatterns)
	assert.Contains(t, got, fmt.Sprintf("Patterns that rose most (%d shown, 37 seen in the window):", shown))
	assert.True(t, strings.HasSuffix(got, "not the spike.\n"), "the closing caveat survives")
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "- error") {
			assert.True(t, strings.HasSuffix(line, "`"), "pattern line cut mid-example: %q", line)
		}
	}
}

func TestRenderLogPatternBreakdown_NoBreakdown(t *testing.T) {
	assert.Empty(t, renderLogPatternBreakdown(events.InvestigateData{}))
	// The action also runs for alerts it only enriches labels for.
	labelsOnly := events.InvestigateData{LogPatternBreakdown: events.InvestigateDataInsight{
		Data: map[string]any{"enriched_labels": map[string]any{"path": "/checkout"}},
	}}
	assert.Empty(t, renderLogPatternBreakdown(labelsOnly))
	// Data that is not readable as a card renders nothing rather than a broken section.
	assert.Empty(t, renderLogPatternBreakdown(events.InvestigateData{LogPatternBreakdown: events.InvestigateDataInsight{Data: `{"pattern_breakdown": {"patt`}}))
}

func TestBuildInvestigationEvidenceContext_LogSpike(t *testing.T) {
	ev := breakdownEvidence(t, logPatternBreakdownCardJSON)
	ev.LogPatternBreakdown.Insight = []events.Insight{{Message: "Error and critical log lines peaked at 705 per 5 min against 9 normal."}}
	ev.MetricsData = []events.InvestigateDataInsight{{
		Query: "error_rate",
		Data:  map[string]any{"values": []any{"1", "2"}, "timestamps": []any{float64(1768471200), float64(1768471260)}},
	}}
	ev.LogCoverageNote = "The collected log lines span 2026-01-15T10:14:02Z to 2026-01-15T10:20:00Z. The queried window has more lines than the 1000 a log card holds."
	ev.ErrorLogData = []string{"2026-01-15T10:19:58Z\tERROR\tdiscovery failed"}

	got := buildInvestigationEvidenceContext(ev)

	insights := strings.Index(got, "### Collected Insights")
	breakdown := strings.Index(got, "### Error Patterns Against Normal")
	coverage := strings.Index(got, "### Log Coverage")
	errorLines := strings.Index(got, "### Error Log Lines")
	require.True(t, insights >= 0 && breakdown > insights, "breakdown follows the insights: %q", got)
	require.True(t, coverage > breakdown && errorLines > coverage, "coverage is stated before the log lines it describes: %q", got)
	assert.Contains(t, got, "- error: peak 700 at 10:10 vs 2.8 normal (250×)")
	assert.Contains(t, got, "The collected log lines span 2026-01-15T10:14:02Z to 2026-01-15T10:20:00Z.")
	assert.Contains(t, got, "peaked at 705 per 5 min", "the card's insight is collected like any other")
}

func TestBuildInvestigationEvidenceContext_CoverageNoteNeedsLogs(t *testing.T) {
	got := buildInvestigationEvidenceContext(events.InvestigateData{LogCoverageNote: "The collected log lines span a to b."})
	assert.NotContains(t, got, "Log Coverage")
}

// With every section above it full, the error lines shrink to what is left
// rather than losing their end to the block's final cap.
func TestBuildInvestigationEvidenceContext_LastErrorsSurviveFullSections(t *testing.T) {
	patterns := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		patterns = append(patterns, fmt.Sprintf(
			`{"pattern_hash": "p%d", "level": "error", "peak": %d, "peak_at": "2026-01-15T10:10:00Z", "normal": 1, "example": %q}`,
			i, 1000-i, strings.Repeat("x", 300)))
	}
	ev := breakdownEvidence(t, `{
  "window_start": "2026-01-15T10:00:00Z", "window_end": "2026-01-15T10:20:00Z",
  "bucket_minutes": 5, "baseline_hours": 6, "baseline_evaluated_at": "2026-01-15T10:00:00Z", "baseline_known": true,
  "counting_note": "`+strings.Repeat("what is counted ", 100)+`",
  "total": {"peak": 9000, "peak_at": "2026-01-15T10:10:00Z", "normal": 10},
  "patterns_seen": 10,
  "patterns": [`+strings.Join(patterns, ",")+`]
}`)
	ev.Deployment = events.InvestigateDataInsight{Insight: []events.Insight{{Message: strings.Repeat("suspected deployment cause ", 2000)}}}
	series := make([]any, 0, 40)
	for i := 0; i < 40; i++ {
		series = append(series, map[string]any{
			"metric":     map[string]any{"pod": fmt.Sprintf("api-%02d-%s", i, strings.Repeat("y", 60))},
			"values":     []any{"1", "2"},
			"timestamps": []any{float64(1768471200), float64(1768471260)},
		})
	}
	ev.MetricsData = []events.InvestigateDataInsight{{Query: "error_rate", Data: map[string]any{"series": series}}}
	ev.LogSummary = map[string]any{"log_patterns": []any{map[string]any{"count": 1, "template": strings.Repeat("pattern ", 2000)}}}
	ev.LogCoverageNote = strings.Repeat("coverage ", 200)
	ev.ErrorLogData = []string{"FIRST-ERROR", strings.Repeat("error ", 2000), "LAST-ERROR"}

	got := buildInvestigationEvidenceContext(ev)

	assert.LessOrEqual(t, len(got), maxInvestigationEvidenceBytes)
	assert.Contains(t, got, "FIRST-ERROR")
	assert.Contains(t, got, "LAST-ERROR")
}

// The workload had no log counts at all before the window, so there is no
// normal to compare with. The section says so and lists the busiest patterns
// rather than claiming they are new.
func TestRenderLogPatternBreakdown_BaselineUnknown(t *testing.T) {
	got := renderLogPatternBreakdown(breakdownEvidence(t, `{
  "window_start": "2026-01-15T10:00:00Z", "window_end": "2026-01-15T10:20:00Z",
  "bucket_minutes": 5, "baseline_hours": 6, "baseline_evaluated_at": "2026-01-15T10:00:00Z", "baseline_known": false,
  "total": {"peak": 726, "peak_at": "2026-01-15T10:10:00Z", "normal": 0},
  "patterns_seen": 2,
  "patterns": [
    {"pattern_hash": "aaa", "level": "error", "peak": 678, "peak_at": "2026-01-15T10:10:00Z", "normal": 0, "example": "ERROR relay query failed"},
    {"pattern_hash": "bbb", "level": "error", "peak": 264, "peak_at": "2026-01-15T10:15:00Z", "normal": 0, "example": "ERROR discovery failed"}
  ]
}`))
	assert.Contains(t, got, "The workload has no log counts of any level in the 6 h before 10:00 (it may be new), so what is normal for it is unknown.")
	assert.Contains(t, got, "Total: peak 726 per 5 min at 10:10.\n")
	assert.Contains(t, got, "Busiest patterns (2 shown, 2 seen in the window):")
	assert.Contains(t, got, "- error: peak 678 at 10:10 — e.g. `ERROR relay query failed`")
	assert.NotContains(t, got, "not seen in the")
	assert.NotContains(t, got, "no error or critical line")
}

// A pattern seen once in the baseline hours has a normal of about 0.014 per 5
// minutes. It must not print as 0 or read as "not seen".
func TestRenderLogPatternBreakdown_SmallNormal(t *testing.T) {
	got := renderLogPatternBreakdown(breakdownEvidence(t, `{
  "window_start": "2026-01-15T10:00:00Z", "window_end": "2026-01-15T10:20:00Z",
  "bucket_minutes": 5, "baseline_hours": 6, "baseline_evaluated_at": "2026-01-15T10:00:00Z", "baseline_known": true,
  "total": {"peak": 40, "peak_at": "2026-01-15T10:10:00Z", "normal": 0.014},
  "patterns_seen": 1,
  "patterns": [{"pattern_hash": "aaa", "level": "error", "peak": 40, "peak_at": "2026-01-15T10:10:00Z", "normal": 0.014, "example": "ERROR relay query failed"}]
}`))
	assert.Contains(t, got, "Total: peak 40 per 5 min at 10:10 vs 0.014 normal (2857×).")
	assert.Contains(t, got, "- error: peak 40 at 10:10 vs 0.014 normal (2857×)")
}

// The card is read back from the database. Fields that are not what they
// should be must not push the pattern list out, which read as "no pattern rose".
func TestRenderLogPatternBreakdown_MalformedFieldsStayBounded(t *testing.T) {
	long := strings.Repeat("x", 5000)
	got := renderLogPatternBreakdown(breakdownEvidence(t, `{
  "window_start": "`+long+`", "window_end": "`+long+`",
  "bucket_minutes": 5, "baseline_hours": 6, "baseline_evaluated_at": "`+long+`", "baseline_known": true,
  "counting_note": "`+long+`",
  "total": {"peak": 1e300, "peak_at": "`+long+`", "normal": 2.8},
  "patterns_seen": 1,
  "patterns": [{"pattern_hash": "aaa", "level": "`+long+`", "peak": 700, "peak_at": "`+long+`", "normal": 2.8, "example": "`+long+`"}]
}`))
	assert.LessOrEqual(t, len(got), maxLogPatternBreakdownBytes)
	assert.Contains(t, got, "Patterns that rose most (1 shown, 1 seen in the window):")
	assert.Contains(t, got, "peak 700 at ")
	assert.NotContains(t, got, "No single pattern was above its own normal")
}
