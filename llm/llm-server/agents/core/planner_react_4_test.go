package core

import (
	"errors"
	"testing"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

type react4NotebookDAO struct {
	IConversationDao
	createdID       uuid.UUID
	createCalls     int
	updateCalls     int
	createdAgent    string
	createdResponse string
	updatedResponse string
	createErr       error
}

func (d *react4NotebookDAO) SaveCompletedConversationAgentCall(_ uuid.UUID, _, _, _, _, agentName, _, _, _, response, _ string, _ toolcore.NBQueryConfig, _ AgentExecutionStatus, _ string) (uuid.UUID, error) {
	d.createCalls++
	d.createdAgent = agentName
	d.createdResponse = response
	if d.createErr != nil {
		return uuid.Nil, d.createErr
	}
	return d.createdID, nil
}

func (d *react4NotebookDAO) UpdateConversationNotebook(_ string, response, _ string) error {
	d.updateCalls++
	d.updatedResponse = response
	return nil
}

func toolCall(id, name, args string) llms.ToolCall {
	return llms.ToolCall{
		ID:           id,
		Type:         "function",
		FunctionCall: &llms.FunctionCall{Name: name, Arguments: args},
	}
}

func TestReAct4_ParseCompletion_FinalAnswer(t *testing.T) {
	o := &NBReActPlanner4{}
	actions, finish, err := o.parseCompletion(&llms.ContentChoice{Content: "root cause is OOMKill"})
	assert.NoError(t, err)
	assert.Nil(t, actions)
	assert.NotNil(t, finish)
	assert.Equal(t, "root cause is OOMKill", finish.Data)
	assert.True(t, finish.IsTerminal)
}

func TestReAct4_ParseCompletion_EmptyTurnIsParseFailure(t *testing.T) {
	o := &NBReActPlanner4{}
	actions, finish, err := o.parseCompletion(&llms.ContentChoice{Content: "  ", StopReason: "max_tokens"})
	assert.Error(t, err)
	assert.Nil(t, actions)
	assert.Nil(t, finish)
	// Must be the ErrParseFailure sentinel so the executor treats it as a
	// retryable failed step rather than a hard abort (react_3 parity).
	assert.True(t, errors.Is(err, ErrParseFailure))
}

func TestReAct4_ParseCompletion_ToolCallsToActions(t *testing.T) {
	o := &NBReActPlanner4{}
	choice := &llms.ContentChoice{
		Content: "checking pods and services",
		ToolCalls: []llms.ToolCall{
			toolCall("call_1", "kubectl", `{"command":"get pods"}`),
			toolCall("call_2", "kubectl", `{"command":"get svc"}`),
		},
	}
	actions, finish, err := o.parseCompletion(choice)
	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.Len(t, actions, 2)

	assert.Equal(t, "kubectl", actions[0].Tool)
	assert.Equal(t, `{"command":"get pods"}`, actions[0].ToolInput)
	assert.Equal(t, "call_1", actions[0].ToolID)
	assert.Equal(t, "checking pods and services", actions[0].Log, "parallel siblings share the thought")
	assert.Equal(t, "E1", actions[0].DisplayID)
	assert.Equal(t, "E2", actions[1].DisplayID)
	assert.Equal(t, "call_2", actions[1].ToolID)
}

// The model still emits react_3's XML answer shape even though react_4's prompt
// has no XML grammar. Taking it raw leaked the tags and the internal <thought>
// into the user-visible answer ("The user is asking for a definition of PDB…").
func TestReAct4_ParseCompletion_UnwrapsXMLFinalAnswer(t *testing.T) {
	o := &NBReActPlanner4{}
	raw := "<final_answer>\n<thought>The user is asking for a definition of PDB.</thought>\n" +
		"<content>**PDB** stands for **PodDisruptionBudget**.</content>\n</final_answer>"

	actions, finish, err := o.parseCompletion(&llms.ContentChoice{Content: raw})
	assert.NoError(t, err)
	assert.Nil(t, actions)
	assert.NotNil(t, finish)
	assert.Equal(t, "**PDB** stands for **PodDisruptionBudget**.", finish.Data,
		"answer must be the <content> body, with no XML wrapper and no monologue")
	assert.NotContains(t, finish.Data, "<final_answer>")
	assert.NotContains(t, finish.Data, "The user is asking")
	assert.Equal(t, "The user is asking for a definition of PDB.", finish.Log,
		"the thought belongs in Log, like react_3")
	assert.True(t, finish.IsTerminal)
}

func TestReAct4_ParseCompletion_PlainTextAnswerUnchanged(t *testing.T) {
	o := &NBReActPlanner4{}
	_, finish, err := o.parseCompletion(&llms.ContentChoice{Content: "Hello. How can I help you today?"})
	assert.NoError(t, err)
	assert.NotNil(t, finish)
	assert.Equal(t, "Hello. How can I help you today?", finish.Data,
		"a normal react_4 plain-text answer must pass through untouched")
}

func TestReAct4_ParseCompletion_ContinuesDisplayIDFromStepCount(t *testing.T) {
	// On resume Plan re-seeds stepCount to len(intermediateSteps); parseCompletion
	// must continue numbering from there so DisplayIDs / synthesized ids don't
	// collide with pre-suspension steps.
	o := &NBReActPlanner4{stepCount: 5}
	choice := &llms.ContentChoice{
		ToolCalls: []llms.ToolCall{
			toolCall("call_a", "kubectl", `{"command":"get pods"}`),
			toolCall("", "kubectl", `{"command":"get svc"}`),
		},
	}
	actions, _, err := o.parseCompletion(choice)
	assert.NoError(t, err)
	assert.Len(t, actions, 2)
	assert.Equal(t, "E6", actions[0].DisplayID)
	assert.Equal(t, "E7", actions[1].DisplayID)
	// DisplayID continues from the prior step count, but the synthesized ToolID is
	// keyed on the call's index WITHIN THE TURN so a repeat across turns still
	// collides with its earlier self and gets deduped by accumulateSteps.
	// A parallel batch keeps a turn-unique suffix (it must not be deduped — see
	// the batch note in parseCompletion), so the id carries both the step and the
	// sibling index.
	assert.Equal(t, generateToolId("kubectl", `{"command":"get svc"}`)+"-E7-1", actions[1].ToolID)
}

func TestReAct4_ParseCompletion_SynthesizesMissingID(t *testing.T) {
	o := &NBReActPlanner4{}
	choice := &llms.ContentChoice{
		ToolCalls: []llms.ToolCall{toolCall("", "kubectl", `{"command":"get pods"}`)},
	}
	actions, _, err := o.parseCompletion(choice)
	assert.NoError(t, err)
	assert.Len(t, actions, 1)
	assert.NotEmpty(t, actions[0].ToolID, "empty provider id must be replaced with a synthesized one")
	// A single-call turn gets the bare deterministic id, so a repeat collides with
	// its earlier self and accumulateSteps can dedup it.
	assert.Equal(t, generateToolId("kubectl", `{"command":"get pods"}`), actions[0].ToolID)
}

func TestReAct4_ParseCompletion_SynthesizedIDsAreUniqueForIdenticalCalls(t *testing.T) {
	o := &NBReActPlanner4{}
	choice := &llms.ContentChoice{
		ToolCalls: []llms.ToolCall{
			toolCall("", "kubectl", `{"command":"get pods"}`),
			toolCall("", "kubectl", `{"command":"get pods"}`),
		},
	}
	actions, _, err := o.parseCompletion(choice)
	assert.NoError(t, err)
	assert.Len(t, actions, 2)
	assert.NotEqual(t, actions[0].ToolID, actions[1].ToolID,
		"identical parallel calls with omitted ids must still get distinct synthesized ids")
}

func TestReAct4_ExtractNotebookContent(t *testing.T) {
	assert.Equal(t, "hi", extractNotebookContent(`{"content":"hi"}`))
	assert.Equal(t, "raw text", extractNotebookContent("raw text"))
	assert.Equal(t, "", extractNotebookContent("   "))
	// JSON without a content field falls back to the raw string.
	assert.Equal(t, `{"other":"x"}`, extractNotebookContent(`{"other":"x"}`))
}

func TestReAct4_RefreshNotebookFromSteps_PicksLatest(t *testing.T) {
	o := &NBReActPlanner4{}
	steps := []NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{Tool: toolcore.NotebookToolName, ToolInput: `{"content":"first"}`}, Status: ToolStatusSuccess},
		{Action: NBAgentPlannerToolAction{Tool: "kubectl", ToolInput: `{"command":"get pods"}`}, Observation: "pods"},
		{Action: NBAgentPlannerToolAction{Tool: toolcore.NotebookToolName, ToolInput: `{"content":"second"}`}, Status: ToolStatusSuccess},
	}
	o.refreshNotebookFromSteps(steps)
	assert.Equal(t, "second", o.Notebook)
}

func TestReAct4_RenderStepsToMessages_PairsAndSkipsNotebook(t *testing.T) {
	o := &NBReActPlanner4{}
	steps := []NBAgentPlannerToolActionStep{
		{
			Action:      NBAgentPlannerToolAction{Tool: "kubectl", ToolInput: `{"command":"get pods"}`, ToolID: "call_1", Log: "check pods"},
			Observation: "pod running",
		},
		{
			Action:      NBAgentPlannerToolAction{Tool: toolcore.NotebookToolName, ToolInput: `{"content":"nb"}`, ToolID: "call_nb"},
			Observation: "nb",
		},
	}
	msgs := o.renderStepsToMessages(steps)
	// One non-notebook step -> assistant(tool_use) + tool(tool_result). Notebook step skipped.
	assert.Len(t, msgs, 2)

	assert.Equal(t, llms.ChatMessageTypeAI, msgs[0].Role)
	// assistant parts: thought text + tool call
	assert.Len(t, msgs[0].Parts, 2)
	_, isText := msgs[0].Parts[0].(llms.TextContent)
	assert.True(t, isText)
	tc, isToolCall := msgs[0].Parts[1].(llms.ToolCall)
	assert.True(t, isToolCall)
	assert.Equal(t, "call_1", tc.ID)
	assert.Equal(t, "kubectl", tc.FunctionCall.Name)

	assert.Equal(t, llms.ChatMessageTypeTool, msgs[1].Role)
	resp, isResp := msgs[1].Parts[0].(llms.ToolCallResponse)
	assert.True(t, isResp)
	assert.Equal(t, "call_1", resp.ToolCallID, "tool_result id must match the tool_use id")
	assert.Equal(t, "pod running", resp.Content)
}

func TestReAct4_RenderStepsToMessages_OmitsEmptyThought(t *testing.T) {
	o := &NBReActPlanner4{}
	steps := []NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{Tool: "kubectl", ToolInput: `{"command":"get pods"}`, ToolID: "c1", Log: ""}, Observation: "ok"},
	}
	msgs := o.renderStepsToMessages(steps)
	assert.Len(t, msgs, 2)
	// No thought -> assistant message carries only the tool call.
	assert.Len(t, msgs[0].Parts, 1)
	_, isToolCall := msgs[0].Parts[0].(llms.ToolCall)
	assert.True(t, isToolCall)
}

func TestReAct4_MarshalUnmarshalRoundtrip(t *testing.T) {
	// Only the notebook is conversation state; the refinement budget is per-turn
	// and deliberately not persisted.
	o := &NBReActPlanner4{Notebook: "## Hypothesis Tree\n- OOM [SUPPORTED]"}
	data, err := o.Marshal()
	assert.NoError(t, err)

	restored := &NBReActPlanner4{}
	assert.NoError(t, restored.Unmarshal(data))
	assert.Equal(t, o.Notebook, restored.Notebook)
}

func TestReAct4_Unmarshal_EmptyIsNoop(t *testing.T) {
	o := &NBReActPlanner4{Notebook: "keep"}
	assert.NoError(t, o.Unmarshal(nil))
	assert.Equal(t, "keep", o.Notebook)
}

// A turn carrying react_3's XML tool protocol but NO native tool call means
// nothing was dispatched. Returning it as a final answer would ship raw XML to
// the user and silently skip the tool, so it must surface as a parse failure.
func TestReAct4_ParseCompletion_ActionGrammarWithoutToolCallIsParseFailure(t *testing.T) {
	o := &NBReActPlanner4{}
	raw := "<thought_action><thought>check pods</thought><action>" +
		"<tool_name>kubectl_execute</tool_name><tool_input>get pods</tool_input>" +
		"</action></thought_action>"

	actions, finish, err := o.parseCompletion(&llms.ContentChoice{Content: raw})
	assert.Error(t, err)
	assert.Nil(t, actions)
	assert.Nil(t, finish, "must NOT be treated as a final answer")
	assert.True(t, errors.Is(err, ErrParseFailure), "executor must see a retryable parse failure")
}

// Prose that merely mentions the tags must still answer normally.
func TestReAct4_ParseCompletion_ProseMentioningTagsStillAnswers(t *testing.T) {
	o := &NBReActPlanner4{}
	_, finish, err := o.parseCompletion(&llms.ContentChoice{
		Content: "ReAct3 used a thought_action protocol; ReAct4 uses native tool calls.",
	})
	assert.NoError(t, err)
	assert.NotNil(t, finish)
}

// The executor dedups accumulated steps by Action.ToolID, and its duplicate-action
// brake only fires on an iteration that adds NO new steps. A synthesized id that
// embeds a running counter makes every repeat unique, silently disabling both:
// react_4 showed 11-19% duplicate tool calls against react_3's ~0%, and the brake
// never engaged across an entire A/B run. The same call in a later turn must
// therefore produce the SAME id.
func TestReAct4_ParseCompletion_RepeatedCallAcrossTurnsKeepsSameID(t *testing.T) {
	o := &NBReActPlanner4{}
	call := func() []llms.ToolCall {
		return []llms.ToolCall{toolCall("", "kubectl", `{"command":"get pods"}`)}
	}

	first, _, err := o.parseCompletion(&llms.ContentChoice{ToolCalls: call()})
	assert.NoError(t, err)
	// A later turn — stepCount has advanced — repeating the identical call.
	o.stepCount = 17
	second, _, err := o.parseCompletion(&llms.ContentChoice{ToolCalls: call()})
	assert.NoError(t, err)

	assert.Equal(t, first[0].ToolID, second[0].ToolID,
		"an identical single call in a later turn must reuse the id so accumulateSteps can dedup it")
	// DisplayID still advances — it is the human-facing citation label, not a key.
	assert.NotEqual(t, first[0].DisplayID, second[0].DisplayID)
}

// A parallel batch must stay OUT of the dedup path: Gemini signs only the first
// functionCall part of a turn, so dropping that sibling as a "duplicate" leaves
// the survivors replaying as an unsigned group and the request is rejected
// ("...missing a thought_signature..., position 2" — observed on the one case in
// a comparison run that used a batch). Batch ids therefore stay turn-unique.
func TestReAct4_ParseCompletion_ParallelBatchIDsStayTurnUnique(t *testing.T) {
	o := &NBReActPlanner4{}
	batch := func() []llms.ToolCall {
		return []llms.ToolCall{
			toolCall("", "events", `{"q":"a"}`),
			toolCall("", "events", `{"q":"b"}`),
		}
	}
	first, _, err := o.parseCompletion(&llms.ContentChoice{ToolCalls: batch()})
	assert.NoError(t, err)
	second, _, err := o.parseCompletion(&llms.ContentChoice{ToolCalls: batch()})
	assert.NoError(t, err)

	// Siblings differ within a turn...
	assert.NotEqual(t, first[0].ToolID, first[1].ToolID)
	// ...and the same batch in a later turn does NOT collide, so no member can be
	// deduped away from under its signature.
	assert.NotEqual(t, first[0].ToolID, second[0].ToolID)
	assert.NotEqual(t, first[1].ToolID, second[1].ToolID)
}

// The logging added for the critique gate and the empty-intent warning called
// o.nbAgent.GetName() directly, which panics on a bare planner — the same shape
// as the o.ctx deref that already broke the unit tests once. Two call sites got
// hand-rolled nil guards and two did not; agentName() removes the inconsistency,
// and this pins it so a future call site cannot reintroduce the raw access.
func TestReAct4_AgentNameIsNilSafe(t *testing.T) {
	o := &NBReActPlanner4{}
	assert.NotPanics(t, func() { _ = o.agentName() })
	assert.Equal(t, "", o.agentName())
}

func TestReAct4_RefreshNotebookPersistsSuccessfulReplacement(t *testing.T) {
	originalDAO := GetConversationDao()
	dao := &react4NotebookDAO{createdID: uuid.MustParse("11111111-1111-1111-1111-111111111111")}
	SetConversationDao(dao)
	t.Cleanup(func() { SetConversationDao(originalDAO) })

	planner := &NBReActPlanner4{
		ctx: security.NewRequestContextForTenantAccountAdmin(
			"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
			[]string{"cccccccc-cccc-cccc-cccc-cccccccccccc"},
		),
		request: NBAgentRequest{
			ConversationId: "conversation-id",
			MessageId:      "message-id",
			AccountId:      "account-id",
			UserId:         "user-id",
		},
	}
	first := "[DOING] inspect pods"
	planner.refreshNotebookFromSteps([]NBAgentPlannerToolActionStep{{
		Action: NBAgentPlannerToolAction{Tool: "update_notebook", ToolInput: `{"content":"` + first + `"}`},
		Status: ToolStatusSuccess,
	}})

	assert.Equal(t, first, planner.Notebook)
	assert.Equal(t, 1, dao.createCalls)
	assert.Equal(t, notebookDummyAgent, dao.createdAgent)
	assert.Equal(t, first, dao.createdResponse)
	assert.Equal(t, dao.createdID.String(), planner.notebookAgentID)

	second := "[DONE] inspect pods"
	planner.refreshNotebookFromSteps([]NBAgentPlannerToolActionStep{{
		Action: NBAgentPlannerToolAction{Tool: "update_notebook", ToolInput: `{"content":"` + second + `"}`},
		Status: ToolStatusSuccess,
	}})
	assert.Equal(t, second, planner.Notebook)
	assert.Equal(t, 1, dao.createCalls, "later replacements must update the same synthetic row")
	assert.Equal(t, 1, dao.updateCalls)
	assert.Equal(t, second, dao.updatedResponse)
}

func TestReAct4_RefreshNotebookIgnoresFailedAndDuplicateSteps(t *testing.T) {
	originalDAO := GetConversationDao()
	dao := &react4NotebookDAO{createdID: uuid.MustParse("22222222-2222-2222-2222-222222222222")}
	SetConversationDao(dao)
	t.Cleanup(func() { SetConversationDao(originalDAO) })

	planner := &NBReActPlanner4{Notebook: "existing"}
	planner.refreshNotebookFromSteps([]NBAgentPlannerToolActionStep{{
		Action: NBAgentPlannerToolAction{Tool: "update_notebook", ToolInput: `{"content":"failed replacement"}`},
		Status: ToolStatusFailure,
	}})
	planner.refreshNotebookFromSteps([]NBAgentPlannerToolActionStep{{
		Action: NBAgentPlannerToolAction{Tool: "update_notebook", ToolInput: `{"content":"existing"}`},
		Status: ToolStatusSuccess,
	}})

	assert.Equal(t, "existing", planner.Notebook)
	assert.Zero(t, dao.createCalls)
	assert.Zero(t, dao.updateCalls)
}

func TestReAct4_NotebookPersistenceStateSurvivesResume(t *testing.T) {
	original := &NBReActPlanner4{
		Notebook:            "current notebook",
		notebookAgentID:     "notebook-agent-id",
		notebookUpdateCount: 3,
		persistedNotebook:   "current notebook",
	}
	state, err := original.Marshal()
	assert.NoError(t, err)

	restored := &NBReActPlanner4{}
	assert.NoError(t, restored.Unmarshal(state))
	assert.Equal(t, original.Notebook, restored.Notebook)
	assert.Equal(t, original.notebookAgentID, restored.notebookAgentID)
	assert.Equal(t, original.notebookUpdateCount, restored.notebookUpdateCount)
	assert.Equal(t, original.persistedNotebook, restored.persistedNotebook)
}

func TestReAct4_NotebookPersistenceRetriesAfterTransientFailure(t *testing.T) {
	originalDAO := GetConversationDao()
	dao := &react4NotebookDAO{
		createdID: uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		createErr: errors.New("temporary database failure"),
	}
	SetConversationDao(dao)
	t.Cleanup(func() { SetConversationDao(originalDAO) })

	planner := &NBReActPlanner4{
		ctx: security.NewRequestContextForTenantAccountAdmin(
			"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
			[]string{"cccccccc-cccc-cccc-cccc-cccccccccccc"},
		),
		request: NBAgentRequest{ConversationId: "conversation-id", MessageId: "message-id"},
	}
	steps := []NBAgentPlannerToolActionStep{{
		Action: NBAgentPlannerToolAction{Tool: "update_notebook", ToolInput: `{"content":"retry me"}`},
		Status: ToolStatusSuccess,
	}}
	planner.refreshNotebookFromSteps(steps)
	assert.Equal(t, "retry me", planner.Notebook)
	assert.Empty(t, planner.persistedNotebook)

	dao.createErr = nil
	planner.refreshNotebookFromSteps(steps)
	assert.Equal(t, 2, dao.createCalls)
	assert.Equal(t, "retry me", planner.persistedNotebook)
}

func TestReAct4_NeedsClarificationContinuation(t *testing.T) {
	clarification := NBAgentPlannerToolActionStep{
		Action: NBAgentPlannerToolAction{Tool: "ask_clarification"},
		Status: ToolStatusSuccess,
	}
	assert.True(t, needsClarificationContinuation([]NBAgentPlannerToolActionStep{clarification}))
	assert.True(t, needsClarificationContinuation([]NBAgentPlannerToolActionStep{
		clarification,
		{Action: NBAgentPlannerToolAction{Tool: "update_notebook"}, Status: ToolStatusSuccess},
	}), "notebook updates are control state, not investigation evidence")
	assert.False(t, needsClarificationContinuation([]NBAgentPlannerToolActionStep{
		clarification,
		{Action: NBAgentPlannerToolAction{Tool: "kubectl_execute"}, Status: ToolStatusSuccess},
	}), "a post-clarification tool result satisfies the continuation invariant")
	assert.False(t, needsClarificationContinuation([]NBAgentPlannerToolActionStep{{
		Action: NBAgentPlannerToolAction{Tool: "ask_clarification"},
		Status: ToolStatusFailure,
	}}))
}

func TestReAct4_ClarificationContinuationMessageRejectsUnsupportedInspection(t *testing.T) {
	messages := clarificationContinuationMessages("I inspected the deployment.")
	assert.Len(t, messages, 2)
	assert.Equal(t, llms.ChatMessageTypeAI, messages[0].Role)
	assert.Equal(t, llms.ChatMessageTypeHuman, messages[1].Role)
	text := messages[1].Parts[0].(llms.TextContent).Text
	assert.Contains(t, text, "no investigation tool has run")
	assert.Contains(t, text, "Do not claim")
	assert.Contains(t, text, "call the appropriate native tool")
}

func TestReAct4_ClarificationContinuationInputRestoresOriginalTask(t *testing.T) {
	got := clarificationContinuationInput(
		"Check why my pod is unhealthy.",
		"Check the llm-server deployment in nudgebee.",
	)
	assert.Contains(t, got, "Original task:\nCheck why my pod is unhealthy.")
	assert.Contains(t, got, "User's clarification response:\nCheck the llm-server deployment in nudgebee.")
	assert.Equal(t, "selected-pod", clarificationContinuationInput("", "selected-pod"))
	assert.Equal(t, "same", clarificationContinuationInput("same", "same"))
}
