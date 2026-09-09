package core

import (
	"fmt"
	"strings"
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func TestReAct4_IsStepRecent(t *testing.T) {
	// Compression inactive: every step is recent regardless of position.
	assert.True(t, isStepRecent(0, 100, false))
	assert.True(t, isStepRecent(50, 100, false))

	// Active: only the last recentStepsFullContext steps are recent (strict <,
	// mirroring react_3's planner_react_3.go gate).
	assert.True(t, isStepRecent(91, 100, true))  // 100-91 = 9 < 10
	assert.False(t, isStepRecent(90, 100, true)) // 100-90 = 10, not < 10
	assert.False(t, isStepRecent(0, 100, true))
}

func TestReAct4_Compression_OldStepsCompressedRecentKeptFull(t *testing.T) {
	// Force a tiny activation budget so compression activates. With a nil ctx the
	// model window is unresolved, so scratchpadBudget falls back to this legacy
	// char budget.
	orig := config.Config.LlmServerAgentMaxScratchpadChars
	t.Cleanup(func() { config.Config.LlmServerAgentMaxScratchpadChars = orig })
	config.Config.LlmServerAgentMaxScratchpadChars = 100

	o := &NBReActPlanner4{} // nil ctx -> window 0 -> legacy budget

	bigObs := strings.Repeat("x", 2000)
	const n = 15 // > recentStepsFullContext, so early steps are "old"
	steps := make([]NBAgentPlannerToolActionStep, n)
	for i := range steps {
		steps[i] = NBAgentPlannerToolActionStep{
			Action:      NBAgentPlannerToolAction{Tool: "kubectl", ToolInput: "{}", ToolID: fmt.Sprintf("c%d", i)},
			Observation: bigObs,
		}
	}

	// Total obs bytes (30000) >> activation budget (100) -> compression active.
	assert.True(t, o.compressionActive(steps))

	msgs := o.renderStepsToMessages(steps)
	assert.Len(t, msgs, n*2) // assistant + tool per step

	// Step 0 is old -> compressed; step 14 is recent -> full.
	oldContent := msgs[1].Parts[0].(llms.ToolCallResponse).Content
	recentContent := msgs[(n-1)*2+1].Parts[0].(llms.ToolCallResponse).Content

	assert.Less(t, len(oldContent), len(bigObs), "old step observation should be compressed")
	assert.Equal(t, bigObs, recentContent, "recent step keeps its full observation")
}

func TestReAct4_Compression_InactiveKeepsAllFull(t *testing.T) {
	o := &NBReActPlanner4{} // legacy budget default (200000) -> not active for small obs

	obs := strings.Repeat("y", 1000)
	steps := make([]NBAgentPlannerToolActionStep, 15)
	for i := range steps {
		steps[i] = NBAgentPlannerToolActionStep{
			Action:      NBAgentPlannerToolAction{Tool: "kubectl", ToolInput: "{}", ToolID: fmt.Sprintf("c%d", i)},
			Observation: obs,
		}
	}

	assert.False(t, o.compressionActive(steps))
	msgs := o.renderStepsToMessages(steps)
	for i := 1; i < len(msgs); i += 2 {
		assert.Equal(t, obs, msgs[i].Parts[0].(llms.ToolCallResponse).Content)
	}
}
