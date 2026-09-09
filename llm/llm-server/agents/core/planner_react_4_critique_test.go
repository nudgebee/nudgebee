package core

import (
	"slices"
	"testing"

	"nudgebee/llm/config"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func TestReAct4_CritiquerInputsIncludePremiseVerification(t *testing.T) {
	assert.True(t, slices.Contains(reactCritiquerInputVariables, "premise_verification_enabled"),
		"react_4 must pass the shared premise-verification gate into react_critiquer")
}

func TestReAct4_IsTopLevel(t *testing.T) {
	// No parent -> top level.
	o := &NBReActPlanner4{request: NBAgentRequest{AgentId: "a1"}}
	assert.True(t, o.isTopLevel())

	// Parent == self -> top level.
	o = &NBReActPlanner4{request: NBAgentRequest{AgentId: "a1", ParentAgentId: "a1"}}
	assert.True(t, o.isTopLevel())

	// Distinct parent -> sub-agent.
	o = &NBReActPlanner4{request: NBAgentRequest{AgentId: "child", ParentAgentId: "parent"}}
	assert.False(t, o.isTopLevel())
}

func TestReAct4_ShouldCritique(t *testing.T) {
	// Each case mutates the global critique flag, so isolate them in subtests with
	// per-case t.Cleanup restore rather than a single shared defer.
	setCritiqueFlag := func(t *testing.T, v bool) {
		orig := config.Config.LlmServerReActCritiqueEnabled
		t.Cleanup(func() { config.Config.LlmServerReActCritiqueEnabled = orig })
		config.Config.LlmServerReActCritiqueEnabled = v
	}

	t.Run("explicit per-request enable wins regardless of config/topology", func(t *testing.T) {
		setCritiqueFlag(t, false)
		o := &NBReActPlanner4{enableCritique: true, request: NBAgentRequest{AgentId: "child", ParentAgentId: "parent"}}
		assert.True(t, o.shouldCritique())
	})

	t.Run("config on + sub-agent -> not allowed (sub-agents are never critiqued)", func(t *testing.T) {
		setCritiqueFlag(t, true)
		o := &NBReActPlanner4{enableCritique: false, request: NBAgentRequest{AgentId: "child", ParentAgentId: "parent", Query: "why is the pod crashing"}}
		assert.False(t, o.shouldCritique())
	})

	t.Run("config off + no explicit enable -> not allowed even at top level", func(t *testing.T) {
		setCritiqueFlag(t, false)
		o := &NBReActPlanner4{enableCritique: false, request: NBAgentRequest{AgentId: "a1", Query: "why is the pod crashing"}}
		assert.False(t, o.shouldCritique())
	})
}

func TestReAct4_FlattenTranscript_SkipsNotebookAndFormats(t *testing.T) {
	o := &NBReActPlanner4{}
	steps := []NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{Tool: "kubectl", ToolInput: `{"command":"get pods"}`, Log: "check pods"}, Observation: "pod running"},
		{Action: NBAgentPlannerToolAction{Tool: toolcore.NotebookToolName, ToolInput: `{"content":"nb"}`}, Observation: "nb"},
		{Action: NBAgentPlannerToolAction{Tool: "logs", ToolInput: `{"svc":"api"}`}, Observation: "no errors"},
	}
	out := o.flattenTranscript(steps)

	assert.Contains(t, out, "check pods")
	assert.Contains(t, out, "kubectl")
	assert.Contains(t, out, "pod running")
	assert.Contains(t, out, "logs")
	assert.Contains(t, out, "no errors")
	// Notebook step is excluded from the transcript.
	assert.NotContains(t, out, "update_notebook")
	// Non-notebook steps are renumbered 1..N (notebook skipped, not counted).
	assert.Contains(t, out, "Step 1 — kubectl")
	assert.Contains(t, out, "Step 2 — logs")
}

// A refinement that produced a tool call returns to the executor, which rebuilds
// the history from intermediateSteps (no rejected answer / feedback). buildMessages
// must replay refinementData so the critique redirect survives the round-trip.
func TestReAct4_BuildMessages_ReplaysRefinementRedirect(t *testing.T) {
	o := &NBReActPlanner4{
		refinementData: []refinementRecord{
			{Answer: "shallow answer 1", Feedback: "cite the OOM evidence", StepIndex: 0},
			{Answer: "shallow answer 2", Feedback: "add the 5-whys chain", StepIndex: 0},
		},
	}

	msgs := o.buildMessages("why did it crash", nil)

	// Tail is the two redirects in order: AI(answer) then Human(feedback) each.
	assert.Len(t, msgs, 5) // human(question) + 2 pairs
	assert.Equal(t, llms.ChatMessageTypeAI, msgs[1].Role)
	assert.Contains(t, textOf(t, msgs[1]), "shallow answer 1")
	assert.Equal(t, llms.ChatMessageTypeHuman, msgs[2].Role)
	assert.Contains(t, textOf(t, msgs[2]), "cite the OOM evidence")
	assert.Equal(t, llms.ChatMessageTypeAI, msgs[3].Role)
	assert.Contains(t, textOf(t, msgs[3]), "shallow answer 2")
	assert.Equal(t, llms.ChatMessageTypeHuman, msgs[4].Role)
	assert.Contains(t, textOf(t, msgs[4]), "add the 5-whys chain")
}

// When a refinement triggered a tool call, that tool step lands in
// intermediateSteps at the refinement's stepIndex. buildMessages must splice the
// redirect in BEFORE that step so cause (feedback) precedes effect (the tool
// call it prompted), not after it.
func TestReAct4_BuildMessages_InterleavesRedirectBeforeTriggeredStep(t *testing.T) {
	o := &NBReActPlanner4{
		// Refinement was recorded after step 0 (stepIndex 1) and then triggered
		// the tool call now sitting at index 1.
		refinementData: []refinementRecord{
			{Answer: "shallow answer", Feedback: "check the logs", StepIndex: 1},
		},
	}
	steps := []NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{Tool: "kubectl", ToolInput: `{"command":"get pods"}`, ToolID: "c0", Log: "list pods"}, Observation: "pod running"},
		{Action: NBAgentPlannerToolAction{Tool: "logs", ToolInput: `{"svc":"api"}`, ToolID: "c1", Log: "fetch logs"}, Observation: "OOM killed"},
	}

	msgs := o.buildMessages("why did it crash", steps)

	// Expected order: human(Q), step0(AI,tool), redirect(AI,human), step1(AI,tool).
	assert.Len(t, msgs, 7)
	assert.Equal(t, llms.ChatMessageTypeHuman, msgs[0].Role)
	// step 0
	assert.Equal(t, llms.ChatMessageTypeAI, msgs[1].Role)
	assert.Equal(t, llms.ChatMessageTypeTool, msgs[2].Role)
	// redirect spliced BEFORE the triggered step
	assert.Equal(t, llms.ChatMessageTypeAI, msgs[3].Role)
	assert.Contains(t, textOf(t, msgs[3]), "shallow answer")
	assert.Equal(t, llms.ChatMessageTypeHuman, msgs[4].Role)
	assert.Contains(t, textOf(t, msgs[4]), "check the logs")
	// triggered step 1 comes AFTER the feedback
	assert.Equal(t, llms.ChatMessageTypeAI, msgs[5].Role)
	tc, ok := msgs[5].Parts[len(msgs[5].Parts)-1].(llms.ToolCall)
	assert.True(t, ok)
	assert.Equal(t, "logs", tc.FunctionCall.Name)
	assert.Equal(t, llms.ChatMessageTypeTool, msgs[6].Role)
}

func textOf(t *testing.T, m llms.MessageContent) string {
	t.Helper()
	tc, ok := m.Parts[0].(llms.TextContent)
	assert.True(t, ok, "expected a text part")
	return tc.Text
}
