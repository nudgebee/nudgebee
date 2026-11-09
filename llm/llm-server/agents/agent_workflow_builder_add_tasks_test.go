package agents

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// A build used to take one model call per task, plus a closing answer in which the model typed
// the whole automation out again. On a sixteen-task automation that was 135 s of add_task calls
// and 53 s of re-typing. These tests pin the two things that replaced them: add_tasks, which
// adds a batch in one call, and a build loop that ends at finalize.

func batchOf(ids ...string) string {
	tasks := make([]string, len(ids))
	for i, id := range ids {
		tasks[i] = fmt.Sprintf(`{"id":%q,"type":"core.print","params":{"message":"hello"}}`, id)
	}
	return `{"tasks":[` + strings.Join(tasks, ",") + `]}`
}

func initializedBuilder(t *testing.T) *WorkflowBuilderAgent {
	t.Helper()
	agent := newWorkflowBuilderAgent("acct-1")
	agent.cachedTaskTypes = "[]"
	require.Contains(t, agent.executeWorkflowTool(nil, "init_workflow", scriptedInit, ""), "initialized")
	return agent
}

func TestAddTasks_AddsEveryTaskInOrder(t *testing.T) {
	agent := initializedBuilder(t)

	observation := agent.executeWorkflowTool(nil, "add_tasks", batchOf("fetch", "format", "choose"), "")

	assert.Equal(t, "Added 3 of 3 task(s): fetch, format, choose. Automation now has 3 task(s).", observation)
	assert.Equal(t, []string{"fetch", "format", "choose"}, taskIDs(t, agent.toolFinalize()))
}

// The history keeps one row per task, as it did with add_task: the progress counter and anyone
// reading a run afterwards both work per task.
func TestAddTasks_KeepsAResultPerTask(t *testing.T) {
	agent := initializedBuilder(t)
	agent.executeWorkflowTool(nil, "add_tasks", batchOf("fetch", "format"), "")

	require.Len(t, agent.batchResults, 2)
	assert.Contains(t, agent.batchResults[0].input, `"id":"fetch"`)
	assert.Contains(t, agent.batchResults[0].observation, "Task 'fetch' (type: core.print) added successfully.")
	assert.Contains(t, agent.batchResults[1].observation, "Task 'format' (type: core.print) added successfully.")

	// A later call starts clean, so its rows are never mixed with an earlier batch.
	agent.executeWorkflowTool(nil, "add_tasks", batchOf("choose"), "")
	require.Len(t, agent.batchResults, 1)
	assert.Contains(t, agent.batchResults[0].input, `"id":"choose"`)
}

func TestAddTasks_SaysWhichTasksWereNotAdded(t *testing.T) {
	agent := initializedBuilder(t)

	input := `{"tasks":[
		{"id":"fetch","type":"core.print"},
		{"id":"fetch","type":"core.print"},
		{"id":"no-type"},
		"not an object",
		{"id":"choose","type":"core.print"}
	]}`
	observation := agent.executeWorkflowTool(nil, "add_tasks", input, "")

	assert.True(t, strings.HasPrefix(observation, "NOT ADDED (3)"), observation)
	assert.Contains(t, observation, "- fetch: Error: task 'fetch' already exists.")
	assert.Contains(t, observation, "- no-type: Error: task 'type' is required")
	assert.Contains(t, observation, "- tasks[3]: not a task object")
	assert.Contains(t, observation, "Added 2 of 5 task(s): fetch, choose. Automation now has 2 task(s).")
	assert.Equal(t, []string{"fetch", "choose"}, taskIDs(t, agent.toolFinalize()))

	// The rejected tasks are recorded as failures, the way a failed add_task was.
	var failed int
	for _, result := range agent.batchResults {
		if strings.HasPrefix(result.observation, "Error") {
			failed++
		}
	}
	assert.Equal(t, 2, failed)
}

// Warnings about a task come before the list of what was added, so they are read first.
func TestAddTasks_PutsWarningsAheadOfTheSummary(t *testing.T) {
	agent := initializedBuilder(t)

	input := `{"tasks":[
		{"id":"fetch","type":"core.print","params":{"message":"hello"}},
		{"id":"format","type":"scripting.run_script","params":{"script":"import json\nprint(json.dumps({}))"}}
	]}`
	observation := agent.executeWorkflowTool(nil, "add_tasks", input, "")

	warnings := strings.Index(observation, "WARNINGS")
	summary := strings.Index(observation, "Added 2 of 2 task(s)")
	require.GreaterOrEqual(t, warnings, 0, observation)
	require.Greater(t, summary, warnings, observation)
	assert.Contains(t, observation, "- format: ")
	assert.Contains(t, observation, "parser_type is not 'json'")
	assert.NotContains(t, observation, "- fetch: ", "a task without warnings is not listed under them")
}

func TestAddTasks_NeedsATaskList(t *testing.T) {
	agent := initializedBuilder(t)

	for _, input := range []string{`{}`, `{"tasks":[]}`, `{"tasks":"fetch"}`} {
		observation := agent.executeWorkflowTool(nil, "add_tasks", input, "")
		assert.True(t, strings.HasPrefix(observation, "Error: add_tasks needs 'tasks'"), "%s -> %s", input, observation)
		assert.Empty(t, agent.batchResults)
	}
}

// A batch that is not valid JSON says so: one bad escape in a long script is the likely cause,
// and "needs 'tasks'" would send the model looking for the wrong fault.
func TestAddTasks_SaysWhenTheInputWasNotJSON(t *testing.T) {
	agent := initializedBuilder(t)

	observation := agent.executeWorkflowTool(nil, "add_tasks", `{"tasks":[{"id":"fetch","type":"core.print","params":{"message":"a "quoted" word"}}]}`, "")

	assert.True(t, strings.HasPrefix(observation, "Error: the add_tasks input was not valid JSON"), observation)
	assert.Empty(t, taskIDs(t, agent.toolFinalize()))
}

// init_workflow carries the settings of the automation itself. No other tool sets them, and a
// build ends at finalize, so anything left out here would never reach the saved automation.
func TestInitWorkflow_CarriesAutomationSettings(t *testing.T) {
	agent := newWorkflowBuilderAgent("acct-1")

	observation := agent.executeWorkflowTool(nil, "init_workflow", `{
		"name":"nightly-report",
		"triggers":[{"type":"manual"}],
		"timeout":"10m",
		"retry_policy":{"maximum_attempts":2},
		"output":{"report":"{{ tasks.fetch.output }}"},
		"tags":{"team":"platform"},
		"not_a_setting":"ignored"
	}`, "")
	require.Contains(t, observation, "initialized")

	var built struct {
		Tags       map[string]any `json:"tags"`
		Definition map[string]any `json:"definition"`
	}
	finalized := agent.toolFinalize()
	require.NoError(t, json.Unmarshal([]byte(finalized), &built))
	assert.Equal(t, "10m", built.Definition["timeout"])
	assert.Equal(t, map[string]any{"maximum_attempts": float64(2)}, built.Definition["retry_policy"])
	assert.Equal(t, map[string]any{"report": "{{ tasks.fetch.output }}"}, built.Definition["output"])
	assert.Equal(t, map[string]any{"team": "platform"}, built.Tags)
	assert.NotContains(t, finalized, "not_a_setting")

	// Without settings the automation is exactly what it was before they could be passed.
	plain := newWorkflowBuilderAgent("acct-1")
	plain.executeWorkflowTool(nil, "init_workflow", scriptedInit, "")
	assert.JSONEq(t, `{"name":"nightly-report","definition":{"version":"v1","triggers":[{"type":"manual"}],"tasks":[]}}`, plain.toolFinalize())
}

// Once tasks exist a second init_workflow call changes settings and nothing else, so a setting
// that validate rejects can be corrected without discarding the tasks. Without settings it is
// still refused.
func TestInitWorkflow_ASecondCallChangesOnlySettings(t *testing.T) {
	agent := initializedBuilder(t)
	agent.executeWorkflowTool(nil, "add_tasks", batchOf("fetch", "format"), "")

	observation := agent.executeWorkflowTool(nil, "init_workflow", `{"name":"another-name","triggers":[{"type":"schedule","params":{"cron":"0 9 * * *"}}],"timeout":"15m"}`, "")

	assert.Equal(t, "Automation settings updated: timeout. The name, triggers, inputs and 2 task(s) were left as they are.", observation)
	var built struct {
		Name       string `json:"name"`
		Definition struct {
			Timeout  string           `json:"timeout"`
			Triggers []map[string]any `json:"triggers"`
		} `json:"definition"`
	}
	finalized := agent.toolFinalize()
	require.NoError(t, json.Unmarshal([]byte(finalized), &built))
	assert.Equal(t, "nightly-report", built.Name)
	assert.Equal(t, "15m", built.Definition.Timeout)
	assert.Equal(t, []map[string]any{{"type": "manual"}}, built.Definition.Triggers)
	assert.Equal(t, []string{"fetch", "format"}, taskIDs(t, finalized))

	refused := agent.executeWorkflowTool(nil, "init_workflow", `{"name":"another-name"}`, "")
	assert.True(t, strings.HasPrefix(refused, "Warning: automation already has 2 task(s)."), refused)
	assert.Equal(t, finalized, agent.toolFinalize())
}

func TestAddTasks_FailsWhenNothingWasAdded(t *testing.T) {
	agent := initializedBuilder(t)
	agent.executeWorkflowTool(nil, "add_tasks", batchOf("fetch"), "")

	observation := agent.executeWorkflowTool(nil, "add_tasks", batchOf("fetch"), "")

	assert.True(t, strings.HasPrefix(observation, "Error: no task was added."), observation)
	assert.Contains(t, observation, "- fetch: Error: task 'fetch' already exists.")
}

// scriptedBuild builds a sixteen-task automation in four steps: init, two batches, finalize.
// Any further step is the closing answer.
func scriptedBuild(closingAnswer string) func(step int) string {
	return func(step int) string {
		switch step {
		case 0:
			return scriptedToolCall("init_workflow", scriptedInit)
		case 1:
			return scriptedToolCall("add_tasks", batchOf("t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8"))
		case 2:
			return scriptedToolCall("add_tasks", batchOf("t9", "t10", "t11", "t12", "t13", "t14", "t15", "t16"))
		case 3:
			return scriptedToolCall("finalize", `{"change_summary":"built the report"}`)
		default:
			return closingAnswer
		}
	}
}

// validatedBefore marks the automation as having passed validate just before the given step.
// It stands in for a validate call, which needs the workflow server.
func validatedBefore(agent *WorkflowBuilderAgent, step int) {
	model := agent.toolLoopModel
	calls := 0
	agent.toolLoopModel = func(messages []llms.MessageContent) (*llms.ContentResponse, error) {
		if calls == step {
			agent.validatedSnapshot = agent.workflowSnapshot()
		}
		calls++
		return model(messages)
	}
}

// A build ends when finalize succeeds on a validated automation: the model is not asked to type
// the automation out again.
func TestRunToolLoop_BuildEndsAtFinalize(t *testing.T) {
	agent, steps := scriptedBuilder(scriptedBuild("<final_answer><content>this turn must never be asked for</content></final_answer>"))
	validatedBefore(agent, 3)
	agent.state.WorkingWorkflow = map[string]interface{}{}
	before := agent.workflowSnapshot()

	answer, err := agent.runToolLoop(stepBudgetTestContext(), core.NBAgentRequest{}, "system", "build it",
		toolLoopOptions{stepBudget: agent.sizedStepBudget, commitOnFinalize: true})

	require.NoError(t, err)
	assert.Equal(t, 4, *steps, "init, two batches and finalize — no closing answer")
	workflowJSON, prose := agent.resolveToolLoopOutcome(answer, before)
	assert.Empty(t, prose)
	assert.Len(t, taskIDs(t, workflowJSON), 16)
	assert.Equal(t, "built the report", agent.changeSummary)
}

// An edit still gives its closing answer: it is the only way an edit can change the triggers,
// inputs or name of an existing automation.
func TestRunToolLoop_EditStillGivesItsClosingAnswer(t *testing.T) {
	agent, steps := scriptedBuilder(scriptedBuild("<final_answer><content>Changed the schedule to hourly.</content></final_answer>"))
	agent.state.WorkingWorkflow = map[string]interface{}{}

	answer, err := agent.runToolLoop(stepBudgetTestContext(), core.NBAgentRequest{}, "system", "edit it",
		toolLoopOptions{stepBudget: agent.sizedStepBudget})

	require.NoError(t, err)
	assert.Equal(t, 5, *steps)
	assert.Equal(t, "Changed the schedule to hourly.", answer)
}

// A build that finalizes an automation which has not passed validate as it stands carries on as
// it always did, to a closing answer: never validated, or changed since it was.
func TestRunToolLoop_BuildDoesNotEndOnAnUnvalidatedFinalize(t *testing.T) {
	for name, validatedAt := range map[string]int{"never validated": -1, "changed after validate": 2} {
		t.Run(name, func(t *testing.T) {
			agent, steps := scriptedBuilder(scriptedBuild("<final_answer><content>Built it.</content></final_answer>"))
			validatedBefore(agent, validatedAt)
			agent.state.WorkingWorkflow = map[string]interface{}{}

			answer, err := agent.runToolLoop(stepBudgetTestContext(), core.NBAgentRequest{}, "system", "build it",
				toolLoopOptions{stepBudget: agent.sizedStepBudget, commitOnFinalize: true})

			require.NoError(t, err)
			assert.Equal(t, 5, *steps)
			assert.Equal(t, "Built it.", answer)
		})
	}
}

// A finalize that came before validate does not cost the early finish: once validate has passed,
// the next finalize ends the build, even though finalize itself answers that nothing changed.
func TestRunToolLoop_BuildEndsAtARepeatedFinalizeOnceValidated(t *testing.T) {
	agent, steps := scriptedBuilder(func(step int) string {
		switch step {
		case 0:
			return scriptedToolCall("init_workflow", scriptedInit)
		case 1:
			return scriptedToolCall("add_tasks", batchOf("fetch", "format"))
		default:
			return scriptedToolCall("finalize", `{"change_summary":"built it"}`)
		}
	})
	validatedBefore(agent, 3)
	agent.state.WorkingWorkflow = map[string]interface{}{}

	answer, err := agent.runToolLoop(stepBudgetTestContext(), core.NBAgentRequest{}, "system", "build it",
		toolLoopOptions{stepBudget: agent.sizedStepBudget, commitOnFinalize: true})

	require.NoError(t, err)
	assert.Equal(t, 4, *steps, "the first finalize was not validated; the second was")
	assert.Equal(t, []string{"fetch", "format"}, taskIDs(t, answer))
}

// The loop reads a tool's name the way the dispatcher does, so a build still ends at finalize and
// a batch still gets a history row per task when the model varies the case or adds a space.
func TestRunToolLoop_ReadsToolNamesAsTheDispatcherDoes(t *testing.T) {
	agent, steps := scriptedBuilder(func(step int) string {
		switch step {
		case 0:
			return scriptedToolCall("init_workflow", scriptedInit)
		case 1:
			return scriptedToolCall(" Add_Tasks ", batchOf("fetch", "format"))
		default:
			return scriptedToolCall("Finalize", `{}`)
		}
	})
	validatedBefore(agent, 2)
	agent.state.WorkingWorkflow = map[string]interface{}{}

	answer, err := agent.runToolLoop(stepBudgetTestContext(), core.NBAgentRequest{}, "system", "build it",
		toolLoopOptions{stepBudget: agent.sizedStepBudget, commitOnFinalize: true})

	require.NoError(t, err)
	assert.Equal(t, 3, *steps)
	assert.Equal(t, []string{"fetch", "format"}, taskIDs(t, answer))
}

// A finalize that fails, here because nothing was initialised, does not end the build.
func TestRunToolLoop_BuildDoesNotEndOnAFailedFinalize(t *testing.T) {
	agent, steps := scriptedBuilder(func(step int) string {
		if step == 0 {
			return scriptedToolCall("finalize", `{}`)
		}
		return "<final_answer><content>Nothing to build.</content></final_answer>"
	})

	answer, err := agent.runToolLoop(stepBudgetTestContext(), core.NBAgentRequest{}, "system", "build it",
		toolLoopOptions{stepBudget: agent.sizedStepBudget, commitOnFinalize: true})

	require.NoError(t, err)
	assert.Equal(t, 2, *steps)
	assert.Equal(t, "Nothing to build.", answer)
}

// A batch result stays whole in the loop's history. Other results are cut to a short head once
// three newer ones exist, which would drop the warnings for the later tasks of a batch.
func TestRunToolLoop_BatchResultIsNotShortened(t *testing.T) {
	ids := make([]string, 60)
	for i := range ids {
		ids[i] = fmt.Sprintf("a-rather-long-task-identifier-%02d", i)
	}
	var history []llms.MessageContent
	agent := newWorkflowBuilderAgent("acct-1")
	agent.cachedTaskTypes = "[]"
	step := 0
	agent.toolLoopModel = func(messages []llms.MessageContent) (*llms.ContentResponse, error) {
		history = messages
		var content string
		switch {
		case step == 0:
			content = scriptedToolCall("init_workflow", scriptedInit)
		case step == 1:
			content = scriptedToolCall("add_tasks", batchOf(ids...))
		case step <= 6:
			content = scriptedToolCall("get_task", fmt.Sprintf(`{"task_id":%q}`, ids[step]))
		default:
			content = "<final_answer><content>done</content></final_answer>"
		}
		step++
		return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: content}}}, nil
	}
	agent.state.WorkingWorkflow = map[string]interface{}{}

	_, err := agent.runToolLoop(stepBudgetTestContext(), core.NBAgentRequest{}, "system", "build it",
		toolLoopOptions{stepBudget: agent.sizedStepBudget})
	require.NoError(t, err)

	var batchResult string
	for _, message := range history {
		for _, part := range message.Parts {
			if text, ok := part.(llms.TextContent); ok && strings.Contains(text.Text, "Added 60 of 60 task(s)") {
				batchResult = text.Text
			}
		}
	}
	require.NotEmpty(t, batchResult, "the batch result is still in the history")
	assert.Greater(t, len(batchResult), trimmedObservationHeadBytes)
	assert.Contains(t, batchResult, ids[59])
	assert.NotContains(t, batchResult, "truncated to save context")
}

// The step budget became an option when the loop's settings were gathered into one struct, so a
// caller can now leave it out. It then gets the fixed budget.
func TestRunToolLoop_UnsetStepBudgetIsTheFixedOne(t *testing.T) {
	agent, steps := scriptedBuilder(func(step int) string {
		return scriptedToolCall("get_task", fmt.Sprintf(`{"task_id":"step-%d"}`, step))
	})
	agent.state.WorkingWorkflow = map[string]interface{}{}

	_, err := agent.runToolLoop(stepBudgetTestContext(), core.NBAgentRequest{}, "system", "what does it do?", toolLoopOptions{})

	require.Error(t, err)
	assert.Equal(t, toolLoopBaseSteps, *steps)
}

// The builder never reads composed memory, so it does not ask for it.
func TestWorkflowBuilder_OptsOutOfMemoryComposition(t *testing.T) {
	assert.False(t, core.ResolveAgentMemoryEnabled(newWorkflowBuilderAgent("acct-1")))
}

// One call returns the schema of every task type in the plan; asking one type at a time cost a
// model round trip per type.
func TestGetTaskSchema_ReturnsSeveralTypesInOneCall(t *testing.T) {
	registry := `{"tasks":[
		{"type":"core.print","params":{"message":"string"}},
		{"type":"notifications.im","params":{"channel":"string"}}
	]}`
	agent := newWorkflowBuilderAgent("acct-1")

	observation := agent.toolGetTaskSchema(map[string]interface{}{
		"task_types": []interface{}{"core.print", "notifications.im", "no.such_type"},
	}, registry)

	print := strings.Index(observation, "## core.print")
	im := strings.Index(observation, "## notifications.im")
	missing := strings.Index(observation, "## no.such_type")
	require.GreaterOrEqual(t, print, 0, observation)
	require.Greater(t, im, print, observation)
	require.Greater(t, missing, im, observation)
	assert.Contains(t, observation[print:im], `"message"`)
	assert.Contains(t, observation[im:missing], `"channel"`)
	assert.Equal(t, "## no.such_type\n"+agent.toolGetTaskSchema(map[string]interface{}{"task_type": "no.such_type"}, registry), observation[missing:])
}

// A single type is answered exactly as before.
func TestGetTaskSchema_SingleTypeIsUnchanged(t *testing.T) {
	registry := `[{"type":"core.print","params":{"message":"string"}}]`
	agent := newWorkflowBuilderAgent("acct-1")

	single := agent.toolGetTaskSchema(map[string]interface{}{"task_type": "core.print"}, registry)

	assert.Contains(t, single, `"message"`)
	assert.NotContains(t, single, "## core.print")
	assert.Equal(t, "Error: 'task_type' (or 'task_types') is required", agent.toolGetTaskSchema(map[string]interface{}{}, registry))
	assert.Equal(t, "Error: 'task_type' (or 'task_types') is required", agent.toolGetTaskSchema(map[string]interface{}{"task_types": []interface{}{}}, registry))
}

// A build asks for less reasoning only from a model whose default is higher. Asking a model that
// does not reason by default for "low" would switch reasoning on.
func TestLowReasoningLevel_OnlyLowersADefault(t *testing.T) {
	assert.Equal(t, core.ThinkingLevelLow, lowReasoningLevel("gemini-3-flash-preview"))
	// Already at low by default, or not reasoning by default: nothing is asked for.
	for _, model := range []string{"gemini-3-pro-preview", "gemini-2.5-flash", "claude-sonnet-4-6", "us.anthropic.claude-sonnet-4-5-20250929-v1:0", "gpt-5", ""} {
		assert.Empty(t, lowReasoningLevel(model), model)
	}

	// An operator's override stands in for the model's default.
	defer func(level string) { config.Config.LlmProviderThinkingLevel = level }(config.Config.LlmProviderThinkingLevel)
	config.Config.LlmProviderThinkingLevel = "minimal"
	assert.Empty(t, lowReasoningLevel("gemini-3-flash-preview"))
	config.Config.LlmProviderThinkingLevel = "HIGH"
	assert.Equal(t, core.ThinkingLevelLow, lowReasoningLevel("gemini-3-flash-preview"))
	assert.Empty(t, lowReasoningLevel("claude-sonnet-4-6"))
}
