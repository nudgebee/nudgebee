package core

import (
	"strings"
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
)

func TestCollectCompressionEvents_NoCompressed(t *testing.T) {
	steps := []NBAgentPlannerToolActionStep{
		{
			Action:      NBAgentPlannerToolAction{Tool: "kubectl_execute", ToolID: "E1"},
			Observation: "some output",
		},
	}
	events := collectCompressionEvents(steps, false, 0)
	assert.Empty(t, events)
}

func TestCollectCompressionEvents_MixedMethods(t *testing.T) {
	steps := []NBAgentPlannerToolActionStep{
		{
			Action:                NBAgentPlannerToolAction{Tool: "kubectl_execute", ToolID: "E1"},
			Observation:           strings.Repeat("x", 5000),
			CompressedObservation: "[summarized from 5000 chars] Pod logs showed errors.",
		},
		{
			Action:                NBAgentPlannerToolAction{Tool: "kubectl_execute", ToolID: "E2"},
			Observation:           strings.Repeat("y", 600),
			CompressedObservation: "[output truncated — 600 chars] yyyyyy",
		},
		{
			Action:      NBAgentPlannerToolAction{Tool: "kubectl_execute", ToolID: "E3"},
			Observation: "short",
		},
	}

	events := collectCompressionEvents(steps, true /*windowPressureActive*/, 0)
	assert.Len(t, events, 2)

	assert.Equal(t, "E1", events[0].toolID)
	assert.Equal(t, "llm_summary", events[0].method)
	assert.Equal(t, 5000, events[0].originalLen)
	assert.Equal(t, compressionCauseWindowPressure, events[0].cause)
	assert.NotEmpty(t, events[0].originalPreview, "original preview must be populated for review")
	assert.LessOrEqual(t, len(events[0].originalPreview), compressionEventOriginalPreviewBytes)
	assert.Contains(t, events[0].compressedText, "summarized from 5000")

	assert.Equal(t, "E2", events[1].toolID)
	assert.Equal(t, "truncated", events[1].method)
	assert.Equal(t, 600, events[1].originalLen)
}

func TestCollectCompressionEvents_EmptyToolID(t *testing.T) {
	steps := []NBAgentPlannerToolActionStep{
		{
			Action:                NBAgentPlannerToolAction{Tool: "kubectl_execute"},
			Observation:           strings.Repeat("x", 3000),
			CompressedObservation: "[summarized from 3000 chars] Summary.",
		},
	}
	events := collectCompressionEvents(steps, true, 0)
	assert.Len(t, events, 1)
	assert.Equal(t, "", events[0].toolID)
	assert.Equal(t, "kubectl_execute", events[0].toolName)
}

func TestFormatCompressionSummary(t *testing.T) {
	events := []compressionEvent{
		{
			toolID: "E1", toolName: "kubectl_execute", originalLen: 10000, compressedLen: 200,
			method: "llm_summary", cause: compressionCauseWindowPressure,
			originalPreview: "raw kubectl output start ...",
			compressedText:  "[summarized from 10000 chars] pod logs showed ...",
		},
		{
			toolID: "E2", toolName: "kubectl_execute", originalLen: 500, compressedLen: 120,
			method: "truncated", cause: compressionCauseRefinementFocus,
			originalPreview: "yyyyy",
			compressedText:  "[output truncated — 500 chars] yyy",
		},
	}

	summary := formatCompressionSummary(events)
	assert.Contains(t, summary, "Context Compression Summary:")
	assert.Contains(t, summary, "E1 (kubectl_execute): 10000")
	assert.Contains(t, summary, "E2 (kubectl_execute): 500")
	assert.Contains(t, summary, "1 LLM summaries")
	assert.Contains(t, summary, "1 byte truncations")
	assert.Contains(t, summary, "Total: 10500")
	assert.Contains(t, summary, "reduction")
	// Cause classification + previews must surface so reviewers can judge what was dropped.
	assert.Contains(t, summary, compressionCauseWindowPressure)
	assert.Contains(t, summary, compressionCauseRefinementFocus)
	assert.Contains(t, summary, "before: raw kubectl output start")
	assert.Contains(t, summary, "after:  [summarized from 10000 chars]")
	assert.Contains(t, summary, "Causes: 1 window-pressure, 1 refinement-focus")
}

func TestFormatCompressionSummary_FallsBackToToolName(t *testing.T) {
	events := []compressionEvent{
		{toolID: "", toolName: "kubectl_execute", originalLen: 1000, compressedLen: 100, method: "truncated"},
	}
	summary := formatCompressionSummary(events)
	assert.Contains(t, summary, "kubectl_execute (kubectl_execute)")
}

func TestCompressionTracker_DeduplicatesSaves(t *testing.T) {
	tracker := NewCompressionTracker()

	// First call: 2 events → should save (count changes from 0 to 2).
	steps := []NBAgentPlannerToolActionStep{
		{
			Action:                NBAgentPlannerToolAction{Tool: "t1", ToolID: "E1"},
			Observation:           strings.Repeat("x", 3000),
			CompressedObservation: "[summarized from 3000 chars] summary",
		},
		{
			Action:                NBAgentPlannerToolAction{Tool: "t2", ToolID: "E2"},
			Observation:           strings.Repeat("y", 500),
			CompressedObservation: "[output truncated — 500 chars] yyy",
		},
	}

	events := collectCompressionEvents(steps, true, 0)
	assert.Len(t, events, 2)

	// Simulate what SaveCompressionVisibility does: check tracker, update count.
	assert.NotEqual(t, len(events), tracker.lastReportedCount) // should differ
	tracker.lastReportedCount = len(events)

	// Second call with same steps: should be deduped.
	events2 := collectCompressionEvents(steps, true, 0)
	assert.Equal(t, len(events2), tracker.lastReportedCount) // same count → skip
}

func TestCompressionTracker_DetectsNewCompression(t *testing.T) {
	tracker := NewCompressionTracker()
	tracker.lastReportedCount = 1

	// Add a second compressed step.
	steps := []NBAgentPlannerToolActionStep{
		{
			Action:                NBAgentPlannerToolAction{Tool: "t1", ToolID: "E1"},
			Observation:           strings.Repeat("x", 3000),
			CompressedObservation: "[summarized from 3000 chars] summary",
		},
		{
			Action:                NBAgentPlannerToolAction{Tool: "t2", ToolID: "E2"},
			Observation:           strings.Repeat("y", 2000),
			CompressedObservation: "[summarized from 2000 chars] another summary",
		},
	}

	events := collectCompressionEvents(steps, true, 0)
	assert.Len(t, events, 2)
	assert.NotEqual(t, len(events), tracker.lastReportedCount) // 2 != 1 → should save
}

func TestSaveCompressionVisibility_SkipsWhenFlagDisabled(t *testing.T) {
	original := config.Config.LlmServerScratchpadSummarizationEnabled
	defer func() { config.Config.LlmServerScratchpadSummarizationEnabled = original }()
	config.Config.LlmServerScratchpadSummarizationEnabled = false

	steps := []NBAgentPlannerToolActionStep{
		{
			Action:                NBAgentPlannerToolAction{Tool: "t1", ToolID: "E1"},
			Observation:           strings.Repeat("x", 3000),
			CompressedObservation: "[summarized from 3000 chars] summary",
		},
	}

	// Should not panic or error — just return early.
	SaveCompressionVisibility(nil, NBAgentRequest{}, steps, nil)
}

func TestSaveCompressionVisibility_SkipsWhenNilCtx(t *testing.T) {
	original := config.Config.LlmServerScratchpadSummarizationEnabled
	defer func() { config.Config.LlmServerScratchpadSummarizationEnabled = original }()
	config.Config.LlmServerScratchpadSummarizationEnabled = true

	steps := []NBAgentPlannerToolActionStep{
		{
			Action:                NBAgentPlannerToolAction{Tool: "t1", ToolID: "E1"},
			Observation:           strings.Repeat("x", 3000),
			CompressedObservation: "[summarized from 3000 chars] summary",
		},
	}

	// nil ctx → early return, no panic.
	SaveCompressionVisibility(nil, NBAgentRequest{}, steps, nil)
}

func TestSaveCompressionVisibility_SkipsWhenNoEvents(t *testing.T) {
	original := config.Config.LlmServerScratchpadSummarizationEnabled
	defer func() { config.Config.LlmServerScratchpadSummarizationEnabled = original }()
	config.Config.LlmServerScratchpadSummarizationEnabled = true

	// No compressed steps.
	steps := []NBAgentPlannerToolActionStep{
		{
			Action:      NBAgentPlannerToolAction{Tool: "t1", ToolID: "E1"},
			Observation: "short output",
		},
	}

	// Should return early without trying to save.
	SaveCompressionVisibility(nil, NBAgentRequest{}, steps, NewCompressionTracker())
}

// TestCollectCompressionEvents_ClassifiesCausesByIndex pins the cause
// classifier — the entire reason the (windowPressureActive,
// postRefinementToolIndex) plumbing exists. A 4-step run where the first 2
// steps are pre-refinement and the last 2 are post-refinement + under window
// pressure must produce: 2 refinement-focus events + 2 window-pressure events.
// Pre-this-change, every event was implicitly labelled "window pressure" by
// the card title regardless of why it actually fired (production convo
// 23a9d9fc-fa49-48af-abcf-9a19d7229088 was the smoking gun: 4 steps / 7 KB
// triggered a "stay within context window" card on a 1M-token model).
func TestCollectCompressionEvents_ClassifiesCausesByIndex(t *testing.T) {
	steps := []NBAgentPlannerToolActionStep{
		{ // index 0 — pre-refinement
			Action:                NBAgentPlannerToolAction{Tool: "shell_execute", ToolID: "E1"},
			Observation:           strings.Repeat("a", 5000),
			CompressedObservation: "[summarized from 5000 chars] pre-refinement A",
		},
		{ // index 1 — pre-refinement
			Action:                NBAgentPlannerToolAction{Tool: "shell_execute", ToolID: "E2"},
			Observation:           strings.Repeat("b", 5000),
			CompressedObservation: "[summarized from 5000 chars] pre-refinement B",
		},
		{ // index 2 — post-refinement, under window pressure
			Action:                NBAgentPlannerToolAction{Tool: "shell_execute", ToolID: "E3"},
			Observation:           strings.Repeat("c", 5000),
			CompressedObservation: "[summarized from 5000 chars] post-refinement C",
		},
		{ // index 3 — post-refinement, under window pressure
			Action:                NBAgentPlannerToolAction{Tool: "shell_execute", ToolID: "E4"},
			Observation:           strings.Repeat("d", 5000),
			CompressedObservation: "[summarized from 5000 chars] post-refinement D",
		},
	}
	events := collectCompressionEvents(steps, true /*postRefinementToolIndex*/, 2)
	assert.Len(t, events, 4)
	assert.Equal(t, compressionCauseRefinementFocus, events[0].cause)
	assert.Equal(t, compressionCauseRefinementFocus, events[1].cause)
	assert.Equal(t, compressionCauseWindowPressure, events[2].cause)
	assert.Equal(t, compressionCauseWindowPressure, events[3].cause)
}

// TestCollectCompressionEvents_UnknownWhenNoSignal locks the defensive default:
// if a new compression path appears that doesn't set the tracker context,
// classify as "unknown" rather than silently claiming "window pressure" (the
// pre-#33083 bug). The card description then nudges the next contributor to
// thread the signal through.
func TestCollectCompressionEvents_UnknownWhenNoSignal(t *testing.T) {
	steps := []NBAgentPlannerToolActionStep{
		{
			Action:                NBAgentPlannerToolAction{Tool: "kubectl_execute", ToolID: "E1"},
			Observation:           strings.Repeat("x", 1000),
			CompressedObservation: "[summarized from 1000 chars] s",
		},
	}
	events := collectCompressionEvents(steps, false /*no window pressure*/, 0 /*no refinement*/)
	assert.Len(t, events, 1)
	assert.Equal(t, compressionCauseUnknown, events[0].cause)
}

// TestCompressionCardCopy_TitlesByCause pins the title/description picker —
// the user-facing fix. Each cause flavor gets a distinct title so a reviewer
// can immediately tell why a card fired without reading the per-event summary.
func TestCompressionCardCopy_TitlesByCause(t *testing.T) {
	cases := []struct {
		name          string
		counts        causeCounts
		total         int
		titleContains string
		descrContains string
	}{
		{
			name:          "all window pressure",
			counts:        causeCounts{window: 3},
			total:         3,
			titleContains: "scratchpad near model context window",
			descrContains: "approached the model's context window",
		},
		{
			name:          "all refinement",
			counts:        causeCounts{refinement: 2},
			total:         2,
			titleContains: "kept refined investigation focused",
			descrContains: "regardless of context-window usage",
		},
		{
			name:          "mixed window + refinement leads with both counts",
			counts:        causeCounts{window: 1, refinement: 4},
			total:         5,
			titleContains: "1 window-pressure, 4 refinement-focus",
			descrContains: "Some observations",
		},
		{
			name:          "unknown cause flags itself",
			counts:        causeCounts{unknown: 1},
			total:         1,
			titleContains: "cause not classified",
			descrContains: "may have been added without updating",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			title, descr := compressionCardCopy(c.total, c.counts)
			assert.Contains(t, title, c.titleContains)
			assert.Contains(t, descr, c.descrContains)
		})
	}
}

// TestCompressionTracker_SetCompressionContext is a tiny but load-bearing
// test: the tracker is the seam through which buildScratchpad hands its
// gating decisions to SaveCompressionVisibility. A regression here re-introduces
// the "always window pressure" mislabel bug.
func TestCompressionTracker_SetCompressionContext(t *testing.T) {
	tracker := NewCompressionTracker()
	tracker.SetCompressionContext(true, 7)
	assert.True(t, tracker.windowPressureActive)
	assert.Equal(t, 7, tracker.postRefinementToolIndex)

	// Subsequent call overrides — this is intentional, the tracker reflects
	// the MOST RECENT buildScratchpad pass, not a history of passes.
	tracker.SetCompressionContext(false, 0)
	assert.False(t, tracker.windowPressureActive)
	assert.Equal(t, 0, tracker.postRefinementToolIndex)

	// nil receiver is a no-op — used by tests / ReWOO callers that pass nil.
	var nilTracker *CompressionTracker
	assert.NotPanics(t, func() { nilTracker.SetCompressionContext(true, 5) })
}
