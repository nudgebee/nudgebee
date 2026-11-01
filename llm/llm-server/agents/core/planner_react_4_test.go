package core

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/llms/googleai"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestReAct4_HumanText_TodayIncludesTimeOfDay guards against a real bug found
// via benchmark run dd7bc6b21b45 (test 43_current_datetime_from_prompt): the
// only grounded "current time" fact reaching the model for a no-tool-call
// turn was date-only ("January 02, 2006"), so a direct "what time is it"
// question had no time-of-day data to draw on and the model fabricated
// 00:00:00. humanText's injected "today" value must carry both an actual
// UTC date AND a time-of-day component the model can read directly.
func TestReAct4_HumanText_TodayIncludesTimeOfDay(t *testing.T) {
	planner := &NBReActPlanner4{}
	human := planner.humanText("what time is it right now?")

	assert.Regexp(t, regexp.MustCompile(`\d{2}:\d{2}:\d{2} UTC`), human,
		"today must carry a real time-of-day component, not just a date")

	wantDate := time.Now().UTC().Format("January 2, 2006")
	assert.Contains(t, human, wantDate,
		"today's date component must be rendered in UTC, matching time.Now().UTC()")
}

func TestReAct4_AccountContextIsSystemOnlyAndRequestPromptIsHumanOnly(t *testing.T) {
	request := NBAgentRequest{
		AccountId:      "account-1",
		AccountContext: "stable deployment fact",
		AccountPrompt:  "event-only instruction",
	}
	ctx := security.NewRequestContextForSuperAdmin()
	system := composeReact4SystemMessage(ctx, request, notebookOptOutAgent{}, "agent prompt", "", nil)
	planner := &NBReActPlanner4{request: request}
	human := planner.humanText("what happened?")

	assert.Contains(t, system, "stable deployment fact")
	assert.NotContains(t, system, "event-only instruction")
	assert.Contains(t, human, "event-only instruction")
	assert.NotContains(t, human, "stable deployment fact")
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

func TestReAct4_ParseCompletion_UsesPerCallThoughtAndStripsItFromExecutionInput(t *testing.T) {
	o := &NBReActPlanner4{}
	nativeInput := `{"command":"get pods","_thought":"Checking whether the workload is healthy."}`
	actions, finish, err := o.parseCompletion(&llms.ContentChoice{
		Content:   "shared fallback intent",
		ToolCalls: []llms.ToolCall{toolCall("call_1", "kubectl", nativeInput)},
	})

	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.Len(t, actions, 1)
	assert.JSONEq(t, `{"command":"get pods"}`, actions[0].ToolInput)
	assert.Equal(t, nativeInput, actions[0].NativeToolInput)
	assert.Equal(t, "Checking whether the workload is healthy.", actions[0].Log)
}

func TestReAct4_ParseCompletion_KeepsSiblingThoughtsIndependent(t *testing.T) {
	o := &NBReActPlanner4{}
	actions, _, err := o.parseCompletion(&llms.ContentChoice{
		Content: "shared fallback must not replace per-call intent",
		ToolCalls: []llms.ToolCall{
			toolCall("call_1", "kubectl", `{"command":"get pods","_thought":"Checking pod health."}`),
			toolCall("call_2", "kubectl", `{"command":"get events","_thought":"Checking recent failure events."}`),
		},
	})

	assert.NoError(t, err)
	assert.Len(t, actions, 2)
	assert.Equal(t, "Checking pod health.", actions[0].Log)
	assert.Equal(t, "Checking recent failure events.", actions[1].Log)
	assert.JSONEq(t, `{"command":"get pods"}`, actions[0].ToolInput)
	assert.JSONEq(t, `{"command":"get events"}`, actions[1].ToolInput)
}

func TestReAct4_ParseCompletion_StripsInvalidThoughtMetadataBeforeExecution(t *testing.T) {
	o := &NBReActPlanner4{}
	actions, _, err := o.parseCompletion(&llms.ContentChoice{
		Content:   "safe fallback intent",
		ToolCalls: []llms.ToolCall{toolCall("call_1", "kubectl", `{"command":"get pods","_thought":{"unexpected":true}}`)},
	})

	assert.NoError(t, err)
	assert.JSONEq(t, `{"command":"get pods"}`, actions[0].ToolInput,
		"planner-only metadata must never reach validation or execution, even when malformed")
	assert.Equal(t, "safe fallback intent", actions[0].Log)
}

func TestReAct4_ParseCompletion_SanitizesXMLInsideThoughtArgument(t *testing.T) {
	o := &NBReActPlanner4{}
	actions, _, err := o.parseCompletion(&llms.ContentChoice{
		ToolCalls: []llms.ToolCall{toolCall("call_1", "kubectl",
			`{"command":"get pods","_thought":"Checking pods. <action><tool_name>kubectl</tool_name><tool_input>{}</tool_input></action>"}`)},
	})

	assert.NoError(t, err)
	assert.Equal(t, "Checking pods.", actions[0].Log)
	assert.NotContains(t, actions[0].Log, "<action>")
}

func TestReAct4_ParseCompletion_FallsBackToAssistantTextWhenThoughtArgumentMissing(t *testing.T) {
	o := &NBReActPlanner4{}
	actions, _, err := o.parseCompletion(&llms.ContentChoice{
		Content:   "checking workload health",
		ToolCalls: []llms.ToolCall{toolCall("call_1", "kubectl", `{"command":"get pods"}`)},
	})

	assert.NoError(t, err)
	assert.Equal(t, "checking workload health", actions[0].Log)
}

func TestReAct4_ThoughtSchemaIsRequiredWithoutMutatingOriginal(t *testing.T) {
	original := []llms.Tool{{
		Type: "function",
		Function: &llms.FunctionDefinition{
			Name: "kubectl",
			Parameters: map[string]any{
				"type":       "object",
				"required":   []string{"command"},
				"properties": map[string]any{"command": map[string]any{"type": "string"}},
			},
		},
	}}

	augmented := withReact4ThoughtSchemas(original)
	originalParameters := original[0].Function.Parameters.(map[string]any)
	augmentedParameters := augmented[0].Function.Parameters.(map[string]any)

	assert.NotContains(t, originalParameters["properties"].(map[string]any), react4ThoughtArgument)
	assert.Equal(t, []string{"command"}, originalParameters["required"])
	assert.Contains(t, augmentedParameters["properties"].(map[string]any), react4ThoughtArgument)
	assert.Equal(t, []string{"command", react4ThoughtArgument}, augmentedParameters["required"])
	assert.NoError(t, googleai.ValidateTools(augmented))
}

func TestReAct4_MetadataSchemasPreserveJSONDecodedRequiredFields(t *testing.T) {
	original := []llms.Tool{{
		Type: "function",
		Function: &llms.FunctionDefinition{
			Name: "kubectl",
			Parameters: map[string]any{
				"type":       "object",
				"required":   []any{"command", "namespace"},
				"properties": map[string]any{},
			},
		},
	}}

	augmented := withReact4MemoryAttributionSchemas(withReact4ThoughtSchemas(original))
	parameters := augmented[0].Function.Parameters.(map[string]any)

	assert.Equal(t, []any{"command", "namespace"}, original[0].Function.Parameters.(map[string]any)["required"])
	assert.Equal(t, []string{"command", "namespace", react4ThoughtArgument, react4MemoryRefsArgument}, parameters["required"])
}

func TestReAct4_ParseCompletion_ExtractsMemoryRefsPerNativeCall(t *testing.T) {
	o := &NBReActPlanner4{}
	nativeInput := `{"command":"get pods -n payments","_thought":"Checking pods.","_memory_refs":[{"position":2,"note":" default namespace "},{"position":2,"note":"duplicate"},{"position":0}]}`
	actions, _, err := o.parseCompletion(&llms.ContentChoice{
		ToolCalls: []llms.ToolCall{toolCall("call_1", "kubectl", nativeInput)},
	})

	assert.NoError(t, err)
	assert.Len(t, actions, 1)
	assert.JSONEq(t, `{"command":"get pods -n payments"}`, actions[0].ToolInput)
	assert.Equal(t, nativeInput, actions[0].NativeToolInput)
	assert.Equal(t, []NBAgentPlannerToolActionMemoryRef{{Position: 2, Note: "default namespace"}}, actions[0].MemoryRefs)
}

func TestReAct4_MemoryContextRemovesLegacyActionGrammar(t *testing.T) {
	legacy := `<user_memory><preferences>default_namespace: payments</preferences></user_memory>
<memory_index>
  [m1] preferences: default_namespace
</memory_index>
For EVERY <action> include <memory_used><ref n="1"/></memory_used></action>`

	got := react4MemoryContext(legacy)
	assert.Contains(t, got, "<user_memory>")
	assert.Contains(t, got, "<memory_index>")
	assert.Contains(t, got, react4MemoryRefsArgument)
	assert.NotContains(t, got, "<action>")
	assert.NotContains(t, got, "<memory_used>")
}

func TestReAct4_MemorySchemaRequiresExplicitPerCallDeclaration(t *testing.T) {
	base := []llms.Tool{{Type: "function", Function: &llms.FunctionDefinition{
		Name: "kubectl",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
			"required":   []string{},
		},
	}}}
	augmented := withReact4MemoryAttributionSchemas(base)
	parameters := augmented[0].Function.Parameters.(map[string]any)
	assert.Contains(t, parameters["properties"].(map[string]any), react4MemoryRefsArgument)
	assert.Contains(t, parameters["required"].([]string), react4MemoryRefsArgument)
	assert.NoError(t, googleai.ValidateTools(augmented))
}

func TestReAct4_ParseCompletion_StripsLegacyActionXMLFromNativeToolThought(t *testing.T) {
	o := &NBReActPlanner4{}
	choice := &llms.ContentChoice{
		Content: `<action><tool_name>shell_execute</tool_name><tool_input>{"command":"kubectl get pods"}</tool_input></action>`,
		ToolCalls: []llms.ToolCall{
			toolCall("call_1", "shell_execute", `{"command":"kubectl get pods"}`),
		},
	}

	actions, finish, err := o.parseCompletion(choice)
	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.Len(t, actions, 1)
	assert.Equal(t, "shell_execute", actions[0].Tool)
	assert.Empty(t, actions[0].Log, "legacy action XML must not be persisted as the UI thought")
}

func TestReAct4_ParseCompletion_ExtractsThoughtFromLegacyXMLAlongsideNativeToolCall(t *testing.T) {
	o := &NBReActPlanner4{}
	choice := &llms.ContentChoice{
		Content: `<thought_action><thought>Check whether the pods are healthy.</thought><action>` +
			`<tool_name>kubectl</tool_name><tool_input>{"command":"get pods"}</tool_input>` +
			`</action></thought_action>`,
		ToolCalls: []llms.ToolCall{toolCall("call_1", "kubectl", `{"command":"get pods"}`)},
	}

	actions, finish, err := o.parseCompletion(choice)
	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.Len(t, actions, 1)
	assert.Equal(t, "Check whether the pods are healthy.", actions[0].Log)
	assert.NotContains(t, actions[0].Log, "<action>")
}

func TestReAct4_ParseCompletion_PreservesProseAroundLegacyActionXML(t *testing.T) {
	o := &NBReActPlanner4{}
	choice := &llms.ContentChoice{
		Content: `Checking pod health now. <action><tool_name>shell_execute</tool_name>` +
			`<tool_input>{"command":"kubectl get pods"}</tool_input></action>`,
		ToolCalls: []llms.ToolCall{toolCall("call_1", "shell_execute", `{"command":"kubectl get pods"}`)},
	}

	actions, finish, err := o.parseCompletion(choice)
	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.Len(t, actions, 1)
	assert.Equal(t, "Checking pod health now.", actions[0].Log)
}

func TestReAct4_ParseCompletion_StripsEncodedActionXMLInsideThought(t *testing.T) {
	o := &NBReActPlanner4{}
	choice := &llms.ContentChoice{
		Content: `<thought_action><thought>Checking pods. &lt;action&gt;&lt;tool_name&gt;shell_execute&lt;/tool_name&gt;` +
			`&lt;tool_input&gt;{}&lt;/tool_input&gt;&lt;/action&gt;</thought></thought_action>`,
		ToolCalls: []llms.ToolCall{toolCall("call_1", "shell_execute", `{}`)},
	}

	actions, finish, err := o.parseCompletion(choice)
	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.Len(t, actions, 1)
	assert.Equal(t, "Checking pods.", actions[0].Log)
	assert.NotContains(t, actions[0].Log, "<action>")
}

func TestReAct4_ParseCompletion_StripsOrphanedLegacyClosingTag(t *testing.T) {
	o := &NBReActPlanner4{}
	choice := &llms.ContentChoice{
		Content:   `Checking pods now.</action>`,
		ToolCalls: []llms.ToolCall{toolCall("call_1", "shell_execute", `{}`)},
	}

	actions, finish, err := o.parseCompletion(choice)
	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.Len(t, actions, 1)
	assert.Equal(t, "Checking pods now.", actions[0].Log)
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

func TestReAct4_ExtractNotebookUpdate(t *testing.T) {
	content, appendEntry := extractNotebookUpdate(`{"content":"new evidence","append":true}`)
	assert.Equal(t, "new evidence", content)
	assert.True(t, appendEntry)

	content, appendEntry = extractNotebookUpdate(`{"content":"full state"}`)
	assert.Equal(t, "full state", content)
	assert.False(t, appendEntry, "replacement must remain the backward-compatible default")
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

func TestReAct4_RefreshNotebookFromSteps_AppendsTimestampedIdempotentJournalEntry(t *testing.T) {
	o := &NBReActPlanner4{Notebook: "## Initial state\n- H1 [OPEN]"}
	step := NBAgentPlannerToolActionStep{
		Action: NBAgentPlannerToolAction{
			Tool:             toolcore.NotebookToolName,
			ToolInput:        `{"content":"H1 is supported by error traces","append":true}`,
			ToolID:           "call-journal-1",
			PlannerIteration: 4,
		},
		Status: ToolStatusSuccess,
	}

	o.refreshNotebookFromSteps([]NBAgentPlannerToolActionStep{step})
	first := o.Notebook
	assert.Contains(t, first, "## Initial state\n- H1 [OPEN]")
	assert.Contains(t, first, "## Journal entry — ")
	assert.Contains(t, first, " · Iteration 4")
	assert.Contains(t, first, "<!-- notebook-entry:call-journal-1 -->")
	assert.Contains(t, first, "H1 is supported by error traces")

	o.refreshNotebookFromSteps([]NBAgentPlannerToolActionStep{step})
	assert.Equal(t, first, o.Notebook, "replaying the same successful tool step must not append twice")
	assert.Equal(t, 1, strings.Count(o.Notebook, "notebook-entry:call-journal-1"))
}

func TestReAct4_RefreshNotebookFromSteps_ReplacementAfterAppendStillReplacesWholeDocument(t *testing.T) {
	o := &NBReActPlanner4{Notebook: "older journal"}
	o.refreshNotebookFromSteps([]NBAgentPlannerToolActionStep{{
		Action: NBAgentPlannerToolAction{
			Tool:      toolcore.NotebookToolName,
			ToolInput: `{"content":"consolidated state","append":false}`,
		},
		Status: ToolStatusSuccess,
	}})
	assert.Equal(t, "consolidated state", o.Notebook)
}

func notebookUpdateStep(id, input string) NBAgentPlannerToolActionStep {
	return NBAgentPlannerToolActionStep{
		Action: NBAgentPlannerToolAction{Tool: toolcore.NotebookToolName, ToolInput: input, ToolID: id},
		Status: ToolStatusSuccess,
	}
}

// Several update_notebook calls in one batch must all land, not only the last.
func TestReAct4_RefreshNotebookFromSteps_AppliesEveryUpdateInABatch(t *testing.T) {
	o := &NBReActPlanner4{Notebook: "base"}
	o.refreshNotebookFromSteps([]NBAgentPlannerToolActionStep{
		notebookUpdateStep("a1", `{"content":"first finding","append":true}`),
		notebookUpdateStep("a2", `{"content":"second finding","append":true}`),
	})
	assert.Contains(t, o.Notebook, "base")
	assert.Contains(t, o.Notebook, "first finding")
	assert.Contains(t, o.Notebook, "second finding")
	assert.Less(t, strings.Index(o.Notebook, "first finding"), strings.Index(o.Notebook, "second finding"))
	assert.Equal(t, 1, o.notebookUpdateCount)
}

// A replacement followed by an append in the same batch keeps both, and later
// Plan calls over the same steps (more steps appended) change nothing.
func TestReAct4_RefreshNotebookFromSteps_ReplaceThenAppendIsStable(t *testing.T) {
	o := &NBReActPlanner4{Notebook: "old"}
	steps := []NBAgentPlannerToolActionStep{
		notebookUpdateStep("r1", `{"content":"consolidated"}`),
		notebookUpdateStep("a1", `{"content":"new finding","append":true}`),
	}
	o.refreshNotebookFromSteps(steps)
	after := o.Notebook
	assert.True(t, strings.HasPrefix(after, "consolidated"))
	assert.Contains(t, after, "new finding")
	assert.NotContains(t, after, "old")

	steps = append(steps, NBAgentPlannerToolActionStep{Action: NBAgentPlannerToolAction{Tool: "kubectl"}, Status: ToolStatusSuccess})
	o.refreshNotebookFromSteps(steps)
	o.refreshNotebookFromSteps(steps)
	assert.Equal(t, after, o.Notebook)
	assert.Equal(t, 1, o.notebookUpdateCount)

	state, err := o.Marshal()
	require.NoError(t, err)
	resumed := &NBReActPlanner4{}
	require.NoError(t, resumed.Unmarshal(state))
	resumed.refreshNotebookFromSteps(steps)
	assert.Equal(t, after, resumed.Notebook, "a resumed planner builds on the applied replacement")
}

func TestReAct4_RenderStepsToMessages_PairsAndReplaysNotebook(t *testing.T) {
	o := &NBReActPlanner4{}
	steps := []NBAgentPlannerToolActionStep{
		{
			Action: NBAgentPlannerToolAction{
				Tool:            "kubectl",
				ToolInput:       `{"command":"get pods"}`,
				NativeToolInput: `{"command":"get pods","_thought":"check pods"}`,
				ToolID:          "call_1",
				Log:             "check pods",
			},
			Observation: "pod running",
		},
		{
			Action:      NBAgentPlannerToolAction{Tool: toolcore.NotebookToolName, ToolInput: `{"content":"nb"}`, ToolID: "call_nb"},
			Observation: "nb",
			Status:      ToolStatusSuccess,
		},
	}
	msgs := o.renderStepsToMessages(steps)
	// Each step -> assistant(tool_use) + tool(tool_result); the notebook step is
	// replayed too, with a short acknowledgement as its result.
	assert.Len(t, msgs, 4)
	assert.Equal(t, "call_nb", msgs[2].Parts[0].(llms.ToolCall).ID)
	assert.Equal(t, notebookReplacedReplay, msgs[3].Parts[0].(llms.ToolCallResponse).Content)

	assert.Equal(t, llms.ChatMessageTypeAI, msgs[0].Role)
	// assistant parts: thought text + tool call
	assert.Len(t, msgs[0].Parts, 2)
	_, isText := msgs[0].Parts[0].(llms.TextContent)
	assert.True(t, isText)
	tc, isToolCall := msgs[0].Parts[1].(llms.ToolCall)
	assert.True(t, isToolCall)
	assert.Equal(t, "call_1", tc.ID)
	assert.Equal(t, "kubectl", tc.FunctionCall.Name)
	assert.Equal(t, `{"command":"get pods","_thought":"check pods"}`, tc.FunctionCall.Arguments,
		"history replay must use the byte-for-byte provider arguments, not executable ToolInput")

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

func TestReAct4_RenderStepsToMessages_DoesNotReplayPersistedActionXML(t *testing.T) {
	o := &NBReActPlanner4{}
	steps := []NBAgentPlannerToolActionStep{{
		Action: NBAgentPlannerToolAction{
			Tool:      "shell_execute",
			ToolInput: `{"command":"kubectl get pods"}`,
			ToolID:    "c1",
			Log:       `<action><tool_name>shell_execute</tool_name><tool_input>{"command":"kubectl get pods"}</tool_input></action>`,
		},
		Observation: "ok",
	}}

	msgs := o.renderStepsToMessages(steps)
	assert.Len(t, msgs, 2)
	assert.Len(t, msgs[0].Parts, 1, "contaminated text must be omitted while the native call is replayed")
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

// A model that wraps a call in its OWN TOOL NAME as an ad-hoc XML tag (observed:
// qwen emitting "<update_notebook>...</update_notebook>") must be treated the same
// as react_3's generic action grammar — a retryable parse failure, not a terminal
// final answer. containsActionGrammar alone misses this shape because it only
// checks a fixed react_3 tag set that doesn't include real tool names.
func TestReAct4_ParseCompletion_ToolNameGrammarWithoutToolCallIsParseFailure(t *testing.T) {
	o := &NBReActPlanner4{tools: []toolcore.NBTool{&stubTool{name: "update_notebook"}}}
	raw := "<update_notebook>Confirmed the root cause is a Redis timeout.</update_notebook>"

	actions, finish, err := o.parseCompletion(&llms.ContentChoice{Content: raw})
	assert.Error(t, err)
	assert.Nil(t, actions)
	assert.Nil(t, finish, "must NOT be treated as a final answer")
	assert.True(t, errors.Is(err, ErrParseFailure), "executor must see a retryable parse failure")
}

// Prose that merely mentions a tool's name in passing (no wrapping tag) must
// still answer normally — only the XML-tag shape is a parse failure.
func TestReAct4_ParseCompletion_ProseMentioningToolNameStillAnswers(t *testing.T) {
	o := &NBReActPlanner4{tools: []toolcore.NBTool{&stubTool{name: "update_notebook"}}}
	_, finish, err := o.parseCompletion(&llms.ContentChoice{
		Content: "I used update_notebook to record the finding, then confirmed it via logs.",
	})
	assert.NoError(t, err)
	assert.NotNil(t, finish)
}

// Every observed "tool call written as text" shape — react_3 XML, a tool name
// as a tag, Gemini's call:default_api: serialization (#39637), and a bare
// name{...} / name(...) call — must be a retryable text-tool-call failure,
// never a terminal final answer.
func TestReAct4_ParseCompletion_TextToolCallShapesAreParseFailures(t *testing.T) {
	o := &NBReActPlanner4{tools: []toolcore.NBTool{
		&stubTool{name: "qa_browser_click"},
		&stubTool{name: "update_notebook"},
	}}
	for name, raw := range map[string]string{
		"react3 xml":  "<action><tool_name>kubectl_execute</tool_name><tool_input>get pods</tool_input></action>",
		"tool as tag": "<update_notebook>Confirmed the root cause.</update_notebook>",
		"gemini default_api": "Click on Troubleshoot in the sidebarcall:default_api:qa_browser_click" +
			`{_thought:Click on Troubleshoot in the sidebar,element:link Troubleshoot,target:link "Troubleshoot" [ref=f5e71]}`,
		"gemini unregistered tool": "Checking the pod.call:default_api:shell_execute{command:kubectl get pods}",
		"brace call":               `Next step: qa_browser_click{"element":"link Troubleshoot"}`,
		"paren call":               `qa_browser_click(element="link Troubleshoot")`,
	} {
		t.Run(name, func(t *testing.T) {
			actions, finish, err := o.parseCompletion(&llms.ContentChoice{Content: raw})
			assert.ErrorIs(t, err, ErrParseFailure, "executor contract: still a parse failure")
			assert.ErrorIs(t, err, errReact4TextToolCall, "Plan keys its one re-prompt on this")
			assert.Nil(t, actions)
			assert.Nil(t, finish, "must NOT be treated as a final answer")
		})
	}
}

// Prose that names a tool without call syntax, or contains the name only as
// part of a longer identifier, is still a final answer.
func TestReAct4_ParseCompletion_ToolNameWithoutCallSyntaxStillAnswers(t *testing.T) {
	o := &NBReActPlanner4{tools: []toolcore.NBTool{&stubTool{name: "qa_browser_click"}}}
	for _, raw := range []string{
		"I used qa_browser_click to open the Troubleshoot page and it loaded.",
		"The qa_browser_click (second call) succeeded.",
		"my_qa_browser_click(x) is unrelated.",
		"Clicking failed: qa_browser_click_v2{} is not a tool here.",
	} {
		t.Run(raw, func(t *testing.T) {
			_, finish, err := o.parseCompletion(&llms.ContentChoice{Content: raw})
			assert.NoError(t, err)
			assert.NotNil(t, finish)
		})
	}
}

// The re-prompt replays the leaked text as the assistant turn and follows it
// with the correction, so the model sees exactly what it did wrong.
func TestReAct4_TextToolCallRetryMessages(t *testing.T) {
	leaked := "call:default_api:qa_browser_snapshot{}"
	msgs := textToolCallRetryMessages(leaked)
	assert.Len(t, msgs, 2)
	assert.Equal(t, llms.ChatMessageTypeAI, msgs[0].Role)
	assert.Equal(t, leaked, msgs[0].Parts[0].(llms.TextContent).Text)
	assert.Equal(t, llms.ChatMessageTypeHuman, msgs[1].Role)
	assert.Contains(t, msgs[1].Parts[0].(llms.TextContent).Text, "native function calling")
}

// A final-answer wrapper must not hide an attempted text-protocol tool call.
func TestReAct4_ParseCompletion_FinalAnswerCannotHideActionGrammar(t *testing.T) {
	o := &NBReActPlanner4{}
	action := `<action><tool_name>list_events</tool_name><tool_input>{}</tool_input></action>`
	for _, raw := range []string{
		action + `<final_answer><content>Done</content></final_answer>`,
		`<final_answer><content>` + action + `</content></final_answer>`,
		`<final_answer><content>Done</content></final_answer>` + action,
	} {
		t.Run(raw, func(t *testing.T) {
			actions, finish, err := o.parseCompletion(&llms.ContentChoice{Content: raw})
			assert.ErrorIs(t, err, ErrParseFailure)
			assert.Nil(t, actions)
			assert.Nil(t, finish)
		})
	}
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

type mockScopedAgent struct {
	notebookOptOutAgent
	scope CacheScope
}

func (m mockScopedAgent) GetCacheScope() CacheScope {
	return m.scope
}

func TestReAct4_ResolveAgentContext_HonorsDeclaredScope(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	caps := toolcore.AgentCapabilities{AllowedTools: []string{"kubectl_execute"}}
	planner := &NBReActPlanner4{
		ctx:     ctx,
		nbAgent: mockScopedAgent{scope: CacheScopeAccount},
		request: NBAgentRequest{Capabilities: caps},
	}
	agentCtx := planner.resolveAgentContext()
	assert.NotNil(t, agentCtx)
	assert.Equal(t, CacheScopeAccount, agentCtx.GetContext().Value(ContextKeyCacheScope))
	assert.Equal(t, caps, agentCtx.GetContext().Value(ContextKeyCapabilities))
}

func TestReAct4_ResolveAgentContext_DowngradesOnClientTools(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	clientTool := toolcore.NBToolCommand{Name: "custom_client_tool"}
	planner := &NBReActPlanner4{
		ctx:     ctx,
		nbAgent: mockScopedAgent{scope: CacheScopeAccount},
		request: NBAgentRequest{ClientTools: []toolcore.NBToolCommand{clientTool}},
	}
	agentCtx := planner.resolveAgentContext()
	assert.NotNil(t, agentCtx)
	assert.Equal(t, CacheScopeConversation, agentCtx.GetContext().Value(ContextKeyCacheScope))
}

func TestReAct4_ResolveAgentContext_DefaultsWithoutCacheProvider(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	planner := &NBReActPlanner4{
		ctx:     ctx,
		nbAgent: notebookOptOutAgent{},
		request: NBAgentRequest{},
	}
	agentCtx := planner.resolveAgentContext()
	assert.NotNil(t, agentCtx)
	assert.Equal(t, CacheScopeConversation, agentCtx.GetContext().Value(ContextKeyCacheScope))
}
