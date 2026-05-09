package core

import (
	"strings"
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
)

func TestSummarizeObservation_ShortObservation(t *testing.T) {
	// Observations shorter than the preview threshold are returned verbatim.
	step := &NBAgentPlannerToolActionStep{
		Action: NBAgentPlannerToolAction{
			Tool:      "kubectl_execute",
			ToolInput: "get pods",
		},
		Observation: "pod1 Running",
		Status:      ToolStatusSuccess,
	}

	result := SummarizeObservation(nil, step, NBAgentRequest{}, step.Observation)
	assert.Equal(t, "pod1 Running", result)
}

func TestSummarizeObservation_FeatureFlagDisabled(t *testing.T) {
	// When the feature flag is off, falls back to compressObservation.
	original := config.Config.LlmServerScratchpadSummarizationEnabled
	defer func() { config.Config.LlmServerScratchpadSummarizationEnabled = original }()
	config.Config.LlmServerScratchpadSummarizationEnabled = false

	longObs := strings.Repeat("x", 5000)
	step := &NBAgentPlannerToolActionStep{
		Action: NBAgentPlannerToolAction{
			Tool:      "kubectl_execute",
			ToolInput: "get pods",
		},
		Observation: longObs,
		Status:      ToolStatusSuccess,
	}

	result := SummarizeObservation(nil, step, NBAgentRequest{}, longObs)
	assert.Contains(t, result, "[output truncated — 5000 chars]")
	assert.Less(t, len(result), compressedObservationPreview+50)
}

func TestSummarizeObservation_CachedResult(t *testing.T) {
	// When CompressedObservation is already set, it is returned directly.
	original := config.Config.LlmServerScratchpadSummarizationEnabled
	defer func() { config.Config.LlmServerScratchpadSummarizationEnabled = original }()
	config.Config.LlmServerScratchpadSummarizationEnabled = true

	cachedSummary := "[summarized from 40000 chars] Pod logs showed 47 connection refused errors."
	longObs := strings.Repeat("x", 5000)
	step := &NBAgentPlannerToolActionStep{
		Action: NBAgentPlannerToolAction{
			Tool:      "kubectl_execute",
			ToolInput: "get pods",
		},
		Observation:           longObs,
		Status:                ToolStatusSuccess,
		CompressedObservation: cachedSummary,
	}

	result := SummarizeObservation(nil, step, NBAgentRequest{}, longObs)
	assert.Equal(t, cachedSummary, result)
}

func TestSummarizeObservation_NilContextFallback(t *testing.T) {
	// When feature flag is on but ctx is nil, SummarizeObservation should
	// still fall back to compressObservation (no panic).
	originalFlag := config.Config.LlmServerScratchpadSummarizationEnabled
	originalMin := config.Config.LlmServerScratchpadSummaryMinBytes
	defer func() {
		config.Config.LlmServerScratchpadSummarizationEnabled = originalFlag
		config.Config.LlmServerScratchpadSummaryMinBytes = originalMin
	}()
	config.Config.LlmServerScratchpadSummarizationEnabled = true
	config.Config.LlmServerScratchpadSummaryMinBytes = 500

	longObs := strings.Repeat("x", 5000)
	step := &NBAgentPlannerToolActionStep{
		Action: NBAgentPlannerToolAction{
			Tool:      "kubectl_execute",
			ToolInput: "get pods",
		},
		Observation: longObs,
		Status:      ToolStatusSuccess,
	}

	result := SummarizeObservation(nil, step, NBAgentRequest{}, longObs)
	// Without ctx, the function can't call the LLM, so it falls back to compressObservation
	assert.Contains(t, result, "[output truncated — 5000 chars]")
}

func TestSummarizeObservation_TieredThreshold_BelowMinUsesByteTruncation(t *testing.T) {
	// Observations between preview (100) and min bytes (1024) should use byte
	// truncation, not LLM summarization. This preserves the old behavior for
	// mid-size observations and reserves LLM calls for genuinely large ones.
	originalFlag := config.Config.LlmServerScratchpadSummarizationEnabled
	originalMin := config.Config.LlmServerScratchpadSummaryMinBytes
	defer func() {
		config.Config.LlmServerScratchpadSummarizationEnabled = originalFlag
		config.Config.LlmServerScratchpadSummaryMinBytes = originalMin
	}()
	config.Config.LlmServerScratchpadSummarizationEnabled = true
	config.Config.LlmServerScratchpadSummaryMinBytes = 1024

	// 800-byte observation: above preview (500) but below min (1024)
	midObs := strings.Repeat("x", 800)
	step := &NBAgentPlannerToolActionStep{
		Action: NBAgentPlannerToolAction{
			Tool:      "kubectl_execute",
			ToolInput: "get pods",
		},
		Observation: midObs,
		Status:      ToolStatusSuccess,
	}

	// Even with ctx=nil, the path goes through compressObservation (no LLM call needed)
	result := SummarizeObservation(nil, step, NBAgentRequest{}, midObs)
	assert.Contains(t, result, "[output truncated — 800 chars]")
	// CompressedObservation is cached so compression visibility can detect all fallback paths.
	assert.Equal(t, result, step.CompressedObservation)
}

func TestSummarizeObservation_TieredThreshold_TinyReturnedVerbatim(t *testing.T) {
	// Observations at or below compressedObservationPreview are returned verbatim.
	original := config.Config.LlmServerScratchpadSummarizationEnabled
	defer func() { config.Config.LlmServerScratchpadSummarizationEnabled = original }()
	config.Config.LlmServerScratchpadSummarizationEnabled = true

	tiny := strings.Repeat("x", compressedObservationPreview) // exactly at the limit
	step := &NBAgentPlannerToolActionStep{
		Action: NBAgentPlannerToolAction{
			Tool:      "kubectl_execute",
			ToolInput: "get pods",
		},
		Observation: tiny,
		Status:      ToolStatusSuccess,
	}

	result := SummarizeObservation(nil, step, NBAgentRequest{}, tiny)
	assert.Equal(t, tiny, result)
}

func TestGetScratchpadSummaryMaxLen(t *testing.T) {
	original := config.Config.LlmServerScratchpadSummaryMaxLen
	defer func() { config.Config.LlmServerScratchpadSummaryMaxLen = original }()

	// Default
	config.Config.LlmServerScratchpadSummaryMaxLen = 0
	assert.Equal(t, 500, getScratchpadSummaryMaxLen())

	// Custom
	config.Config.LlmServerScratchpadSummaryMaxLen = 1000
	assert.Equal(t, 1000, getScratchpadSummaryMaxLen())
}

func TestGetScratchpadSummaryMinBytes(t *testing.T) {
	original := config.Config.LlmServerScratchpadSummaryMinBytes
	defer func() { config.Config.LlmServerScratchpadSummaryMinBytes = original }()

	// Default
	config.Config.LlmServerScratchpadSummaryMinBytes = 0
	assert.Equal(t, 1024, getScratchpadSummaryMinBytes())

	// Custom
	config.Config.LlmServerScratchpadSummaryMinBytes = 2048
	assert.Equal(t, 2048, getScratchpadSummaryMinBytes())
}

func TestGetScratchpadSummaryTimeout(t *testing.T) {
	original := config.Config.LlmServerScratchpadSummaryTimeoutMs
	defer func() { config.Config.LlmServerScratchpadSummaryTimeoutMs = original }()

	// Default
	config.Config.LlmServerScratchpadSummaryTimeoutMs = 0
	assert.Equal(t, 10*1000*1000*1000, int(getScratchpadSummaryTimeout().Nanoseconds())) // 10s

	// Custom
	config.Config.LlmServerScratchpadSummaryTimeoutMs = 5000
	assert.Equal(t, 5*1000*1000*1000, int(getScratchpadSummaryTimeout().Nanoseconds())) // 5s
}

func TestConstructScratchPad_WithSummarizationDisabled(t *testing.T) {
	// ConstructScratchPad with ScratchpadContext but feature flag off
	// should still use byte truncation.
	originalMax := config.Config.LlmServerAgentMaxScratchpadChars
	originalFlag := config.Config.LlmServerScratchpadSummarizationEnabled
	defer func() {
		config.Config.LlmServerAgentMaxScratchpadChars = originalMax
		config.Config.LlmServerScratchpadSummarizationEnabled = originalFlag
	}()

	config.Config.LlmServerAgentMaxScratchpadChars = 3000
	config.Config.LlmServerScratchpadSummarizationEnabled = false

	steps := make([]NBAgentPlannerToolActionStep, 10)
	for i := range steps {
		steps[i] = NBAgentPlannerToolActionStep{
			Action: NBAgentPlannerToolAction{
				Tool:      "kubectl_execute",
				ToolID:    "step_" + strings.Repeat("0", 1) + string(rune('0'+i)),
				ToolInput: "get pods",
				Log:       "Checking",
			},
			Observation: strings.Repeat("x", 600),
			Status:      ToolStatusSuccess,
		}
	}

	// Pass ScratchpadContext with nil Ctx — should work fine, fall back to truncation
	result := ConstructScratchPad(steps, ScratchpadContext{})
	assert.Contains(t, result, "[output truncated")
}
