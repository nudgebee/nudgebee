package core

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
)

func TestConstructScratchPad_PlanSummaryObservationTruncation(t *testing.T) {
	// Observation must exceed maxObservationChars (65536) to trigger truncation
	longObservation := strings.Repeat("x", getMaxObservationChars()+1000)

	steps := []NBAgentPlannerToolActionStep{
		{
			Action: NBAgentPlannerToolAction{
				Tool: plannerDummyTool,
				Log:  "Plan step 1",
			},
		},
		{
			Action: NBAgentPlannerToolAction{
				Tool:      ToolLlm,
				ToolID:    "llm_1",
				ToolInput: "summarize",
			},
			Observation: longObservation,
			Status:      ToolStatusSuccess,
		},
		{
			Action: NBAgentPlannerToolAction{
				Tool: plannerDummyTool,
				Log:  "Plan step 2",
			},
		},
		{
			Action: NBAgentPlannerToolAction{
				Tool:      "kubectl_execute",
				ToolID:    "step_1",
				ToolInput: "get pods",
			},
			Observation: "pod1 Running",
			Status:      ToolStatusSuccess,
		},
	}

	result := ConstructScratchPad(steps)

	// The plan_summary observation should be truncated using TruncateMiddle
	assert.Contains(t, result, "output truncated")
	assert.Contains(t, result, "chars removed")
	// The full observation should NOT appear
	assert.NotContains(t, result, longObservation)
}

func TestConstructScratchPad_AggregateBudget(t *testing.T) {
	originalMax := config.Config.LlmServerAgentMaxScratchpadChars
	defer func() { config.Config.LlmServerAgentMaxScratchpadChars = originalMax }()

	const budget = 1000
	config.Config.LlmServerAgentMaxScratchpadChars = budget

	steps := make([]NBAgentPlannerToolActionStep, 10)
	for i := range steps {
		steps[i] = NBAgentPlannerToolActionStep{
			Action: NBAgentPlannerToolAction{
				Tool:      "kubectl_execute",
				ToolID:    fmt.Sprintf("step_%d", i),
				ToolInput: "get pods",
				Log:       "Checking pods",
			},
			Observation: fmt.Sprintf("data_from_step_%d_%s", i, strings.Repeat("x", 200)),
			Status:      ToolStatusSuccess,
		}
	}

	result := ConstructScratchPad(steps)

	// Result should strictly honor the aggregate budget plus minimal overhead for the truncation note.
	// We allow a small buffer for the note itself, but it must be deterministic.
	assert.LessOrEqual(t, len(result), budget+100, "Scratchpad should strictly honor the configured budget")
	assert.Contains(t, result, "Budget exceeded. Earliest steps truncated.")
	// Should contain the LATEST step data because it uses truncateTail
	assert.Contains(t, result, "step_9")
}

func TestConstructScratchPad_NoBudgetWhenUnderLimit(t *testing.T) {
	originalMax := config.Config.LlmServerAgentMaxScratchpadChars
	defer func() { config.Config.LlmServerAgentMaxScratchpadChars = originalMax }()

	config.Config.LlmServerAgentMaxScratchpadChars = 100000

	steps := []NBAgentPlannerToolActionStep{
		{
			Action: NBAgentPlannerToolAction{
				Tool:      "kubectl_execute",
				ToolID:    "step_1",
				ToolInput: "get pods",
				Log:       "Checking pods",
			},
			Observation: "pod1 Running",
			Status:      ToolStatusSuccess,
		},
	}

	result := ConstructScratchPad(steps)

	assert.NotContains(t, result, "Budget exceeded. Earliest steps truncated.")
	assert.Contains(t, result, "pod1 Running")
}

func TestConstructScratchPad_PerObservationTruncation(t *testing.T) {
	// Observation must exceed maxObservationChars (65536) to trigger truncation
	headMarker := "START_OF_LOG"
	middleMarker := "MIDDLE_OF_LOG_THAT_SHOULD_BE_REMOVED"
	tailMarker := "END_OF_LOG"

	// Create a string where:
	// - headMarker is at index 0 (will be in first 2048)
	// - middleMarker is at index 5000 (will be cut, as it's > 2048 and before tail start)
	// - tailMarker is at the very end
	// Total length must exceed 65536
	longObs := headMarker + strings.Repeat("y", 5000) + middleMarker + strings.Repeat("z", getMaxObservationChars()+4000) + tailMarker

	steps := []NBAgentPlannerToolActionStep{
		{
			Action: NBAgentPlannerToolAction{
				Tool:      "kubectl_execute",
				ToolID:    "step_1",
				ToolInput: "get pods -o json",
				Log:       "Getting detailed pod info",
			},
			Observation: longObs,
			Status:      ToolStatusSuccess,
		},
	}

	result := ConstructScratchPad(steps)

	// Per-observation truncation using TruncateMiddle
	assert.Contains(t, result, "output truncated", "Should contain truncation marker")
	assert.Contains(t, result, headMarker, "Should contain head marker")
	assert.Contains(t, result, tailMarker, "Should contain tail marker")
	assert.NotContains(t, result, middleMarker, "Should NOT contain the middle marker as it should have been truncated")
}

func TestConstructScratchPad_DisabledBudget(t *testing.T) {
	originalMax := config.Config.LlmServerAgentMaxScratchpadChars
	defer func() { config.Config.LlmServerAgentMaxScratchpadChars = originalMax }()

	config.Config.LlmServerAgentMaxScratchpadChars = 0

	steps := make([]NBAgentPlannerToolActionStep, 5)
	for i := range steps {
		steps[i] = NBAgentPlannerToolActionStep{
			Action: NBAgentPlannerToolAction{
				Tool:      "kubectl_execute",
				ToolID:    "step_1",
				ToolInput: "get pods",
				Log:       "Checking pods",
			},
			Observation: strings.Repeat("data", 500),
			Status:      ToolStatusSuccess,
		}
	}

	result := ConstructScratchPad(steps)

	// When budget is 0 (disabled), no compression should happen
	assert.NotContains(t, result, "Budget exceeded. Earliest steps truncated.")
}

func TestCompressObservation(t *testing.T) {
	// Short observation — returned as-is
	short := "pod1 Running"
	assert.Equal(t, short, compressObservation(short))

	// Exactly at limit — returned as-is
	exact := strings.Repeat("x", compressedObservationPreview)
	assert.Equal(t, exact, compressObservation(exact))

	// Long observation — compressed to preview
	long := strings.Repeat("y", 5000)
	compressed := compressObservation(long)
	assert.Contains(t, compressed, "[output truncated — 5000 chars]")
	assert.Less(t, len(compressed), compressedObservationPreview+50) // preview + metadata prefix
}

func TestConstructScratchPad_SemanticCompression(t *testing.T) {
	// Budget-based compression: set a small budget so older steps get compressed
	// while recent steps keep full observations.
	originalMax := config.Config.LlmServerAgentMaxScratchpadChars
	defer func() { config.Config.LlmServerAgentMaxScratchpadChars = originalMax }()

	config.Config.LlmServerAgentMaxScratchpadChars = 15000

	totalSteps := 14
	steps := make([]NBAgentPlannerToolActionStep, totalSteps)
	for i := range steps {
		obs := strings.Repeat(fmt.Sprintf("data_%d_", i), 300) // ~2100 chars each
		steps[i] = NBAgentPlannerToolActionStep{
			Action: NBAgentPlannerToolAction{
				Tool:      "kubectl_execute",
				ToolID:    fmt.Sprintf("step_%d", i),
				ToolInput: fmt.Sprintf("get pods %d", i),
				Log:       fmt.Sprintf("Checking step %d", i),
			},
			Observation: obs,
			Status:      ToolStatusSuccess,
		}
	}

	result := ConstructScratchPad(steps)

	// Oldest steps should have compressed observations
	assert.Contains(t, result, "[output truncated")

	// Recent steps should have full observations
	assert.Contains(t, result, steps[totalSteps-1].Observation)
	assert.Contains(t, result, steps[totalSteps-2].Observation)

	// Oldest steps should NOT have full observations
	assert.NotContains(t, result, steps[0].Observation)
	assert.NotContains(t, result, steps[1].Observation)

	// But oldest steps should still have their thoughts and tool info
	assert.Contains(t, result, "Checking step 0")
	assert.Contains(t, result, "Checking step 1")
}

func TestConstructScratchPad_FewStepsNoCompression(t *testing.T) {
	// With only 2 steps, both should be "recent" — no compression
	steps := []NBAgentPlannerToolActionStep{
		{
			Action: NBAgentPlannerToolAction{
				Tool:      "kubectl_execute",
				ToolID:    "step_0",
				ToolInput: "get pods",
				Log:       "First step",
			},
			Observation: strings.Repeat("a", 500),
			Status:      ToolStatusSuccess,
		},
		{
			Action: NBAgentPlannerToolAction{
				Tool:      "kubectl_execute",
				ToolID:    "step_1",
				ToolInput: "get services",
				Log:       "Second step",
			},
			Observation: strings.Repeat("b", 500),
			Status:      ToolStatusSuccess,
		},
	}

	result := ConstructScratchPad(steps)

	// Both observations should be present in full
	assert.Contains(t, result, steps[0].Observation)
	assert.Contains(t, result, steps[1].Observation)
	// No compression markers
	assert.NotContains(t, result, "(output truncated")
}

func TestTruncateHead_UTF8Safety(t *testing.T) {
	// "Hello, 世界!" — 'H'...' '= 7 bytes, '世'= 3 bytes (7-9), '界'= 3 bytes (10-12), '!'= 1 byte (13)
	// Total: 14 bytes
	s := "Hello, 世界!"

	// Cut at 10 bytes — exactly fits '世' (bytes 7,8,9)
	result := TruncateHead(s, 10)
	assert.True(t, utf8.ValidString(result), "TruncateHead should produce valid UTF-8")
	assert.Equal(t, "Hello, 世", result)

	// Cut at 9 bytes — falls in the middle of '世', should walk back to 7
	result = TruncateHead(s, 9)
	assert.True(t, utf8.ValidString(result), "TruncateHead should produce valid UTF-8")
	assert.Equal(t, "Hello, ", result)

	// Cut at 8 bytes — also falls in the middle of '世'
	result = TruncateHead(s, 8)
	assert.True(t, utf8.ValidString(result), "TruncateHead should produce valid UTF-8")
	assert.Equal(t, "Hello, ", result)

	// No truncation needed
	result = TruncateHead(s, 100)
	assert.Equal(t, s, result)
}

func TestTruncateMiddle_UTF8Safety(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		headBytes int
		tailBytes int
		contains  []string
	}{
		{
			name:      "Standard truncation",
			input:     "Hello, 世界!",
			headBytes: 7,
			tailBytes: 4,
			contains:  []string{"Hello, ", "界!", "output truncated"},
		},
		{
			name:      "Head boundary adjustment (walk back)",
			input:     "Hello, 世界!",
			headBytes: 9,
			tailBytes: 4,
			contains:  []string{"Hello, \n\n[... output truncated"},
		},
		{
			name:      "Tail boundary adjustment (walk forward)",
			input:     "Hello, 世界!",
			headBytes: 7,
			tailBytes: 5,
			contains:  []string{"removed ...]\n\n界!"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TruncateMiddle(tt.input, tt.headBytes, tt.tailBytes)
			assert.True(t, utf8.ValidString(result), "Result should be valid UTF-8")
			for _, c := range tt.contains {
				assert.Contains(t, result, c)
			}
		})
	}
}

func TestTruncateTail_UTF8Safety(t *testing.T) {
	// "Hello, 世界!" — 14 bytes total
	s := "Hello, 世界!"

	// Keep last 7 bytes — start=7, which is the start of '世' (rune boundary)
	result := truncateTail(s, 7)
	assert.True(t, utf8.ValidString(result), "truncateTail should produce valid UTF-8")
	assert.Equal(t, "世界!", result)

	// Keep last 5 bytes — start=9, falls in the middle of '世', should advance to 10 ('界')
	result = truncateTail(s, 5)
	assert.True(t, utf8.ValidString(result), "truncateTail should produce valid UTF-8")
	assert.Equal(t, "界!", result)

	// Keep last 4 bytes — start=10, exactly the start of '界' (rune boundary)
	result = truncateTail(s, 4)
	assert.True(t, utf8.ValidString(result), "truncateTail should produce valid UTF-8")
	assert.Equal(t, "界!", result)

	// No truncation needed
	result = truncateTail(s, 100)
	assert.Equal(t, s, result)
}

// TestConstructScratchPad_BudgetCompressionSetsCompressedObservation verifies that
// when ConstructScratchPad triggers budget compression, it sets CompressedObservation
// on the original intermediateSteps so that SaveCompressionVisibility can detect them.
func TestConstructScratchPad_BudgetCompressionSetsCompressedObservation(t *testing.T) {
	originalMax := config.Config.LlmServerAgentMaxScratchpadChars
	defer func() { config.Config.LlmServerAgentMaxScratchpadChars = originalMax }()

	// Set a very low budget so compression is forced.
	config.Config.LlmServerAgentMaxScratchpadChars = 2000

	steps := make([]NBAgentPlannerToolActionStep, 5)
	for i := range steps {
		steps[i] = NBAgentPlannerToolActionStep{
			Action: NBAgentPlannerToolAction{
				Tool:      "kubectl_execute",
				ToolID:    fmt.Sprintf("step_%d", i),
				ToolInput: "get pods",
				Log:       "Checking pods",
			},
			Observation: fmt.Sprintf("data_from_step_%d_%s", i, strings.Repeat("x", 1000)),
			Status:      ToolStatusSuccess,
		}
	}

	// Verify no CompressedObservation before calling ConstructScratchPad.
	for _, s := range steps {
		assert.Empty(t, s.CompressedObservation, "CompressedObservation should be empty before scratchpad build")
	}

	_ = ConstructScratchPad(steps)

	// At least some steps should now have CompressedObservation set by budget compression.
	compressedCount := 0
	for _, s := range steps {
		if s.CompressedObservation != "" {
			compressedCount++
			assert.Contains(t, s.CompressedObservation, "[output truncated",
				"CompressedObservation should contain truncation marker")
		}
	}
	assert.Greater(t, compressedCount, 0, "Budget compression should have set CompressedObservation on at least one step")

	// Verify collectCompressionEvents can now see them (this is what SaveCompressionVisibility uses).
	events := collectCompressionEvents(steps)
	assert.Equal(t, compressedCount, len(events), "collectCompressionEvents should find all compressed steps")
	for _, e := range events {
		assert.Equal(t, "truncated", e.method)
		assert.Greater(t, e.originalLen, e.compressedLen, "Original should be larger than compressed")
	}
}
