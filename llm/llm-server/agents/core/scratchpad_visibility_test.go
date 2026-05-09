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
	events := collectCompressionEvents(steps)
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

	events := collectCompressionEvents(steps)
	assert.Len(t, events, 2)

	assert.Equal(t, "E1", events[0].toolID)
	assert.Equal(t, "llm_summary", events[0].method)
	assert.Equal(t, 5000, events[0].originalLen)

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
	events := collectCompressionEvents(steps)
	assert.Len(t, events, 1)
	assert.Equal(t, "", events[0].toolID)
	assert.Equal(t, "kubectl_execute", events[0].toolName)
}

func TestFormatCompressionSummary(t *testing.T) {
	events := []compressionEvent{
		{toolID: "E1", toolName: "kubectl_execute", originalLen: 10000, compressedLen: 200, method: "llm_summary"},
		{toolID: "E2", toolName: "kubectl_execute", originalLen: 500, compressedLen: 120, method: "truncated"},
	}

	summary := formatCompressionSummary(events)
	assert.Contains(t, summary, "Context Compression Summary:")
	assert.Contains(t, summary, "E1 (kubectl_execute): 10000")
	assert.Contains(t, summary, "E2 (kubectl_execute): 500")
	assert.Contains(t, summary, "1 LLM summaries")
	assert.Contains(t, summary, "1 byte truncations")
	assert.Contains(t, summary, "Total: 10500")
	assert.Contains(t, summary, "reduction")
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

	events := collectCompressionEvents(steps)
	assert.Len(t, events, 2)

	// Simulate what SaveCompressionVisibility does: check tracker, update count.
	assert.NotEqual(t, len(events), tracker.lastReportedCount) // should differ
	tracker.lastReportedCount = len(events)

	// Second call with same steps: should be deduped.
	events2 := collectCompressionEvents(steps)
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

	events := collectCompressionEvents(steps)
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
