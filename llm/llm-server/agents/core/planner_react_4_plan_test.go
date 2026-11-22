package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

func planStep(input string, status ToolStatus) NBAgentPlannerToolActionStep {
	return NBAgentPlannerToolActionStep{
		Action: NBAgentPlannerToolAction{Tool: toolcore.NotebookV2ToolName, ToolInput: input, TurnID: "t-" + input},
		Status: status,
	}
}

func TestPlanFromSteps_AppliesSuccessfulCallsInOrder(t *testing.T) {
	steps := []NBAgentPlannerToolActionStep{
		planStep(`{"steps":[{"id":"s1","phase":"Scope","title":"Find pod","status":"in_progress"},{"id":"h1","phase":"Verify","title":"OOMKill","kind":"hypothesis"}]}`, ToolStatusSuccess),
		{Action: NBAgentPlannerToolAction{Tool: "kubectl_execute", ToolInput: `{"command":"get pods"}`}, Status: ToolStatusSuccess},
		planStep(`{"steps":[{"id":"s1","status":"done","finding":"api-7f, 03:41-03:43"}]}`, ToolStatusSuccess),
		planStep(`{"steps":[{"id":"h1","status":"refuted","finding":"x"}]}`, ToolStatusFailure), // failed call ignored
	}
	plan := planFromSteps(steps)
	require.Len(t, plan, 2)
	assert.Equal(t, toolcore.PlanStatusDone, plan[0].Status)
	assert.Equal(t, toolcore.PlanStatusOpen, plan[1].Status)
	assert.Equal(t, 4, plannerIterations(steps))
}

func TestLastPlanUpdateIgnored_ReportsAStatusOfTheOtherKind(t *testing.T) {
	steps := []NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{Tool: toolcore.NotebookV2ToolName, TurnID: "t1",
			ToolInput: `{"steps":[{"id":"h1","title":"OOMKill","kind":"hypothesis"}]}`}, Status: ToolStatusSuccess},
		{Action: NBAgentPlannerToolAction{Tool: toolcore.NotebookV2ToolName, TurnID: "t2",
			ToolInput: `{"steps":[{"id":"h1","status":"done","finding":"exit 137"}]}`}, Status: ToolStatusSuccess},
	}
	ignored := lastPlanUpdateIgnored(steps)
	require.Len(t, ignored, 1)
	assert.Contains(t, ignored[0], `h1 is a hypothesis, so status "done" was not applied; close it as supported, refuted or inconclusive`)

	note := planNote(planFromSteps(steps), steps, planSignals{})
	assert.Contains(t, note, "Your last plan update was not fully applied", "the model hears that its close did not land")

	assert.Empty(t, lastPlanUpdateIgnored(steps[:1]), "nothing ignored when the update fits")
}

func TestPlanGateFeedback(t *testing.T) {
	open := []toolcore.PlanStep{{ID: "h1", Title: "OOMKill", Kind: toolcore.PlanStepKindHypothesis, Status: toolcore.PlanStatusOpen}}
	closed := []toolcore.PlanStep{{ID: "h1", Title: "OOMKill", Status: toolcore.PlanStatusRefuted, Finding: "exit 130"}}

	assert.Contains(t, planGateFeedback(open), `h1 "OOMKill" (hypothesis, open; close as supported, refuted or inconclusive)`,
		"a hypothesis is pointed at hypothesis statuses, not task ones")
	assert.Empty(t, planGateFeedback(closed))
	assert.Empty(t, planGateFeedback(nil), "no plan means nothing to close: a run that never planned answers freely")
	assert.Contains(t, unresolvedPlanLine(open), "Not verified: OOMKill")
	assert.Empty(t, unresolvedPlanLine(closed))
}

func roundsOf(tools ...string) []NBAgentPlannerToolActionStep {
	var steps []NBAgentPlannerToolActionStep
	for i, tool := range tools {
		steps = append(steps, NBAgentPlannerToolActionStep{Action: NBAgentPlannerToolAction{Tool: tool, TurnID: fmt.Sprintf("t%d", i)}})
	}
	return steps
}

func testSignals(investigation bool) planSignals {
	return planSignals{investigation: investigation, roundsLeft: -1, redirectStep: -1,
		isSubAgent: func(tool string) bool { return tool == "logs" || tool == "events" }}
}

func TestPlanRequirement_CheckpointOnlyOnceDiscoveryBranchesOut(t *testing.T) {
	sig := testSignals(true)
	// A straight line of one tool never requires a plan (temporal-history run).
	_, req := planRequirement(nil, roundsOf("shell_execute", "shell_execute", "shell_execute", "shell_execute"), sig)
	assert.Empty(t, req)
	// Too early: discovery still running.
	_, req = planRequirement(nil, roundsOf("shell_execute", "logs"), sig)
	assert.Empty(t, req)
	// Round 3 after a sub-agent run: required.
	key, req := planRequirement(nil, roundsOf("shell_execute", "logs", "shell_execute"), sig)
	assert.Equal(t, "checkpoint", key)
	assert.Contains(t, req, "Discovery is done")
	// Plain requests never.
	_, req = planRequirement(nil, roundsOf("shell_execute", "logs", "shell_execute"), testSignals(false))
	assert.Empty(t, req)
	// Once a plan exists, satisfied.
	_, req = planRequirement([]toolcore.PlanStep{{ID: "s1", Title: "x", Kind: "task", Status: "done"}}, roundsOf("shell_execute", "logs", "shell_execute"), sig)
	assert.Empty(t, req)
}

func TestPlanRequirement_AfterReviewRedirect(t *testing.T) {
	plan := []toolcore.PlanStep{{ID: "h1", Title: "Probe starvation", Kind: "hypothesis", Status: "supported", Finding: "x"}}
	sig := testSignals(true)
	sig.redirectStep = 2
	steps := roundsOf("shell_execute", "logs")
	key, req := planRequirement(plan, steps, sig)
	assert.Equal(t, "redirect-2", key)
	assert.Contains(t, req, "review challenged")
	// A plan call after the redirect satisfies it.
	steps = append(steps, planStep(`{"steps":[{"id":"h1","status":"testing"}]}`, ToolStatusSuccess))
	_, req = planRequirement(plan, steps, sig)
	assert.Empty(t, req)
}

func TestPlanNote_FollowUps(t *testing.T) {
	active := []toolcore.PlanStep{
		{ID: "s2", Title: "Inspect pod", Kind: "task", Status: toolcore.PlanStatusInProgress},
		{ID: "h1", Title: "Crash", Kind: "hypothesis", Status: toolcore.PlanStatusOpen, Prior: "high"},
	}
	sig := testSignals(true)

	created := append([]NBAgentPlannerToolActionStep{planStep(`{"steps":[{"id":"s2","title":"Inspect pod","status":"in_progress"}]}`, ToolStatusSuccess)},
		roundsOf("shell_execute", "shell_execute", "shell_execute")...)
	assert.Contains(t, planNote(active, created, sig), "Step s2 has been in progress for 3 rounds")

	// Sub-agent cap beats staleness.
	fanned := append([]NBAgentPlannerToolActionStep{}, created...)
	for i := 0; i < 2; i++ {
		fanned = append(fanned, NBAgentPlannerToolActionStep{Action: NBAgentPlannerToolAction{Tool: "logs", TurnID: fmt.Sprintf("l%d", i), PlanStep: "s2"}})
	}
	assert.Contains(t, planNote(active, fanned, sig), "Step s2 has already started 2 sub-agent runs")

	// Budget: 70% of rounds used.
	sig.roundsLeft = 1
	assert.Contains(t, planNote(active, created, sig), "Rounds left: 1")
	sig.roundsLeft = -1

	// Resolved plan: answer now; still digging: add a step.
	closed := []toolcore.PlanStep{{ID: "h1", Title: "Crash", Kind: "hypothesis", Status: "refuted", Finding: "exit 0"}}
	justClosed := append(roundsOf("shell_execute"), planStep(`{"steps":[{"id":"h1","status":"refuted","finding":"exit 0"}]}`, ToolStatusSuccess))
	assert.Contains(t, planNote(closed, justClosed, sig), "Your plan is resolved: write the answer now.")
	assert.Contains(t, planNote(closed, append(justClosed, roundsOf("events")...), sig), "Every step is closed, but you are still investigating")

	// Last review spent: one more round, no sub-agents.
	sig.finalReviewDone = true
	assert.Contains(t, planNote(closed, justClosed, sig), "That was the last review")
}

func TestPlanNote_CheckpointAndNoPlan(t *testing.T) {
	sig := testSignals(true)
	assert.Empty(t, planNote(nil, roundsOf("shell_execute"), sig), "discovery runs unplanned")
	assert.Contains(t, planNote(nil, roundsOf("shell_execute", "logs", "shell_execute"), sig), "Required now: Discovery is done")
}

func TestRenderPlanMarkdown_CritiqueReadableHypotheses(t *testing.T) {
	plan := []toolcore.PlanStep{
		{ID: "s1", Phase: "Scope", Title: "Find pod", Kind: "task", Status: "done", Finding: "api-7f"},
		{ID: "h1", Phase: "Verify", Title: "Spot preemption", Kind: "hypothesis", Status: "supported", Prior: "high", Finding: "node is spot"},
		{ID: "h1a", Phase: "Verify", Title: "check node events", Kind: "task", Status: "done", Finding: "shutdown 03:43", Parent: "h1"},
	}
	assert.Equal(t, "## Plan\n### Scope\n- [DONE] s1: Find pod — api-7f\n### Verify\n"+
		"- H1 [High][SUPPORTED] Spot preemption — node is spot\n"+
		"  - [DONE] h1a: check node events — shutdown 03:43", renderPlanMarkdown(plan))

	var state struct {
		Version int                 `json:"version"`
		Steps   []toolcore.PlanStep `json:"steps"`
	}
	require.NoError(t, json.Unmarshal([]byte(planStateJSON(plan)), &state))
	assert.Equal(t, 1, state.Version)
	assert.Len(t, state.Steps, 3)
	assert.Equal(t, 3, planStats(plan).DoneCount)
}

func TestExtractReact4PlanStep(t *testing.T) {
	clean, step := extractReact4PlanStep(`{"command":"get pods","_plan_step":"h2"}`)
	assert.JSONEq(t, `{"command":"get pods"}`, clean)
	assert.Equal(t, "h2", step)

	clean, step = extractReact4PlanStep(`{"command":"get pods"}`)
	assert.Equal(t, `{"command":"get pods"}`, clean)
	assert.Empty(t, step)
}

func TestWithReact4PlanStepSchemas_OptionalAndSkipsNotebook(t *testing.T) {
	params := func() map[string]any {
		return map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}, "required": []string{"command"}}
	}
	tools := withReact4PlanStepSchemas([]llms.Tool{
		{Type: "function", Function: &llms.FunctionDefinition{Name: "kubectl_execute", Parameters: params()}},
		{Type: "function", Function: &llms.FunctionDefinition{Name: toolcore.NotebookV2ToolName, Parameters: params()}},
	})
	kubectl := tools[0].Function.Parameters.(map[string]any)
	assert.Contains(t, kubectl["properties"], react4PlanStepArgument)
	assert.Equal(t, []string{"command"}, kubectl["required"], "_plan_step is optional")
	notebook := tools[1].Function.Parameters.(map[string]any)
	assert.NotContains(t, notebook["properties"], react4PlanStepArgument)
}

func TestAttributePlanSteps(t *testing.T) {
	plan := []toolcore.PlanStep{
		{ID: "s1", Title: "Scope", Kind: "task", Status: toolcore.PlanStatusDone, Finding: "x"},
		{ID: "h1", Title: "OOMKill", Kind: "hypothesis", Status: toolcore.PlanStatusTesting},
		{ID: "h2", Title: "Spot", Kind: "hypothesis", Status: toolcore.PlanStatusOpen},
	}
	actions := []NBAgentPlannerToolAction{
		{Tool: "kubectl_execute", PlanStep: "h2"},   // explicit, known
		{Tool: "kubectl_execute", PlanStep: "nope"}, // unknown -> active step
		{Tool: "kubectl_execute"},                   // none -> active step
		{Tool: toolcore.NotebookV2ToolName, ToolInput: `{"steps":[{"id":"h3","title":"DNS","status":"testing"}]}`},
	}
	attributePlanSteps(plan, actions)
	assert.Equal(t, "h2", actions[0].PlanStep)
	assert.False(t, actions[0].PlanStepInferred, "named by the model")
	assert.Equal(t, "h3", actions[1].PlanStep, "a step started in the same response is the active one")
	assert.True(t, actions[1].PlanStepInferred)
	assert.Equal(t, "h3", actions[2].PlanStep)
	assert.Empty(t, actions[3].PlanStep, "the plan call itself is not attributed")
}

func TestRefinementCounters_SeparateBudgets(t *testing.T) {
	o := &NBReActPlanner4{refinementData: []refinementRecord{
		{Answer: "a", Feedback: "plan open", PlanGate: true},
		{Answer: "b", Feedback: "cite evidence"},
	}}
	assert.Equal(t, 1, o.critiqueAttempts())
	assert.Equal(t, 1, o.planGateRejections())
	assert.Equal(t, "plan open", messageText(o.redirectMessages(o.refinementData[0])[1]), "gate feedback is verbatim")
	assert.NotEqual(t, "cite evidence", messageText(o.redirectMessages(o.refinementData[1])[1]), "critique feedback keeps its wrapper")
}

func TestActionExecutionMetadata_CarriesPlanStep(t *testing.T) {
	assert.Equal(t, map[string]any{"plan_step": "h1"}, actionExecutionMetadata(NBAgentPlannerToolAction{PlanStep: "h1"}))
	assert.Equal(t, map[string]any{"plan_step": "h1", "plan_step_inferred": true},
		actionExecutionMetadata(NBAgentPlannerToolAction{PlanStep: "h1", PlanStepInferred: true}))
	assert.Nil(t, actionExecutionMetadata(NBAgentPlannerToolAction{}))
}

// End to end through Plan: an answer with an open hypothesis is sent back, the
// second attempt too, and the third is accepted with the open step named.
func TestReAct4_Plan_PlanGateSendsAnswerBackThenAccepts(t *testing.T) {
	model := &critiqueRecordingModel{replies: []recordedReply{
		{content: "It was an OOM."}, {content: "It was an OOM, really."}, {content: "Final: OOM."},
	}}
	withFakeLLMModel(t, model)
	ctx := llmOverrideContext()
	p := &NBReActPlanner4{ctx: ctx, request: NBAgentRequest{Query: "investigate oom on llm-server"}, nbAgent: notebookOptOutAgent{}, notebookV2: true,
		tools: []toolcore.NBTool{&stubTool{name: "kubectl_execute"}}}
	steps := []NBAgentPlannerToolActionStep{
		planStep(`{"steps":[{"id":"h1","phase":"Verify","title":"OOMKill","kind":"hypothesis","status":"testing"}]}`, ToolStatusSuccess),
	}

	actions, finish, err := p.Plan(ctx.GetContext(), steps, "investigate oom on llm-server")

	require.NoError(t, err)
	require.Empty(t, actions)
	require.NotNil(t, finish)
	assert.Contains(t, finish.Data, "Final: OOM.")
	assert.Contains(t, finish.Data, "Not verified: OOMKill")
	requests := model.requests()
	require.Len(t, requests, 3, "two rejections, then accepted")
	assert.Contains(t, allPartsText(requests[0][len(requests[0])-1]), "Current plan", "the plan rides as a tail note on the last message")
	assert.Contains(t, messageText(requests[1][len(requests[1])-1]), `h1 "OOMKill" (hypothesis, testing;`)
	assert.Equal(t, "It was an OOM.", messageText(requests[1][len(requests[1])-2]), "the rejected answer is replayed")
}

// allPartsText also reads tool results, where a tail note joins the last one.
func allPartsText(m llms.MessageContent) string {
	var b strings.Builder
	for _, part := range m.Parts {
		switch p := part.(type) {
		case llms.TextContent:
			b.WriteString(p.Text)
		case llms.ToolCallResponse:
			b.WriteString(p.Content)
		}
	}
	return b.String()
}

func TestReAct4_Plan_PlanGatePassesWhenPlanClosed(t *testing.T) {
	model := &critiqueRecordingModel{replies: []recordedReply{{content: "Spot preemption."}}}
	withFakeLLMModel(t, model)
	ctx := llmOverrideContext()
	p := &NBReActPlanner4{ctx: ctx, request: NBAgentRequest{Query: "investigate oom on llm-server"}, nbAgent: notebookOptOutAgent{}, notebookV2: true}
	steps := []NBAgentPlannerToolActionStep{
		planStep(`{"steps":[{"id":"h1","title":"Spot","kind":"hypothesis","status":"supported","finding":"node is spot"}]}`, ToolStatusSuccess),
	}

	_, finish, err := p.Plan(ctx.GetContext(), steps, "investigate oom on llm-server")

	require.NoError(t, err)
	require.NotNil(t, finish)
	assert.Equal(t, "Spot preemption.", finish.Data)
	assert.Len(t, model.requests(), 1)
	assert.Contains(t, p.Notebook, "H1 [Med][SUPPORTED] Spot", "the plan is rendered into the notebook string")
}

// A response that ignores the after-discovery requirement is sent back once;
// the second response is dispatched either way.
func TestReAct4_Plan_ReasksOnceWhenPlanRequirementIgnored(t *testing.T) {
	model := &critiqueRecordingModel{replies: []recordedReply{{toolCall: true}, {toolCall: true}}}
	withFakeLLMModel(t, model)
	ctx := llmOverrideContext()
	p := &NBReActPlanner4{ctx: ctx, request: NBAgentRequest{Query: "investigate why pod x restarted"}, nbAgent: notebookOptOutAgent{}, notebookV2: true,
		tools: []toolcore.NBTool{&stubTool{name: "metrics"}}}
	steps := roundsOf("shell_execute", "kubectl_execute", "shell_execute")
	for i := range steps {
		steps[i].Status = ToolStatusSuccess
	}

	actions, finish, err := p.Plan(ctx.GetContext(), steps, "investigate why pod x restarted")

	require.NoError(t, err)
	require.Nil(t, finish)
	require.Len(t, actions, 1, "the second response is dispatched")
	requests := model.requests()
	require.Len(t, requests, 2, "exactly one re-ask")
	assert.Contains(t, allPartsText(requests[0][len(requests[0])-1]), "Required now: Discovery is done")
	assert.Contains(t, allPartsText(requests[1][len(requests[1])-1]), "Required before these calls: Discovery is done")
	assert.Equal(t, len(steps)+1, p.stepCount, "the dropped response does not advance step ids")
}
