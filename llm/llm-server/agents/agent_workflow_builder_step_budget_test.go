package agents

import (
	"encoding/json"
	"fmt"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// The tool loop makes one tool call per step. These tests pin its step budget and what happens
// when the budget runs out.
//
// The loop used to allow a flat 20 steps, which no build past about thirteen tasks could fit in,
// and on running out it returned the working automation as if it had finished — so the callers
// saved unvalidated builds and half-applied edits and reported them as done.

func TestToolLoopStepBudget(t *testing.T) {
	flat := func(n int) []interface{} {
		tasks := make([]interface{}, n)
		for i := range tasks {
			tasks[i] = map[string]interface{}{"id": fmt.Sprintf("step-%d", i), "type": "core.print"}
		}
		return tasks
	}
	withTasks := func(tasks []interface{}) map[string]interface{} {
		return map[string]interface{}{"name": "wf", "definition": map[string]interface{}{"tasks": tasks}}
	}
	nested := []interface{}{
		map[string]interface{}{"id": "group", "type": "core.group", "tasks": flat(2)},
		map[string]interface{}{"id": "each", "type": "core.foreach", "params": map[string]interface{}{"tasks": flat(3)}},
	}

	cases := []struct {
		name     string
		workflow map[string]interface{}
		want     int
	}{
		{"no automation yet", nil, toolLoopBaseSteps},
		{"initialized, no tasks", withTasks(nil), toolLoopBaseSteps},
		{"a small automation gets a little more than the old flat limit", withTasks(flat(5)), 30},
		{"fourteen tasks — the size that could not be built", withTasks(flat(14)), 48},
		{"tasks inside a group and a foreach body count", withTasks(nested), toolLoopBaseSteps + toolLoopStepsPerTask*7},
		{"the ceiling holds however large the automation", withTasks(flat(200)), toolLoopMaxSteps},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, toolLoopStepBudget(tc.workflow))
		})
	}
}

func stepBudgetTestContext() *security.RequestContext {
	return security.NewRequestContextForTenantAccountAdmin("tenant-1", "user-1", []string{"acct-1"})
}

func scriptedToolCall(tool, input string) string {
	return fmt.Sprintf("<thought_action><thought>next step</thought><action><tool_name>%s</tool_name><tool_input>%s</tool_input></action></thought_action>", tool, input)
}

func scriptedAddTask(i int) string {
	return scriptedToolCall("add_task", fmt.Sprintf(`{"id":"step-%d","type":"core.print","params":{"message":"hello"}}`, i))
}

const scriptedInit = `{"name":"nightly-report","triggers":[{"type":"manual"}]}`

// scriptedBuilder returns an agent whose tool loop is driven by turn(step) instead of an LLM,
// and a counter of the steps the loop took.
func scriptedBuilder(turn func(step int) string) (*WorkflowBuilderAgent, *int) {
	agent := newWorkflowBuilderAgent("acct-1")
	// Non-empty so the loop does not fetch the task registry over the network.
	agent.cachedTaskTypes = "[]"
	steps := 0
	agent.toolLoopModel = func([]llms.MessageContent) (*llms.ContentResponse, error) {
		content := turn(steps)
		steps++
		return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: content}}}, nil
	}
	return agent, &steps
}

func taskIDs(t *testing.T, workflowJSON string) []string {
	t.Helper()
	var wf struct {
		Definition struct {
			Tasks []struct {
				ID string `json:"id"`
			} `json:"tasks"`
		} `json:"definition"`
	}
	require.NoError(t, json.Unmarshal([]byte(workflowJSON), &wf))
	ids := make([]string, len(wf.Definition.Tasks))
	for i, task := range wf.Definition.Tasks {
		ids[i] = task.ID
	}
	return ids
}

// Thirty tasks need 33 steps (init, 30 adds, finalize, final answer) — past the old flat 20.
func TestRunToolLoop_BuildLargerThanOldLimitFinishes(t *testing.T) {
	const tasks = 30
	agent, steps := scriptedBuilder(func(step int) string {
		switch {
		case step == 0:
			return scriptedToolCall("init_workflow", scriptedInit)
		case step <= tasks:
			return scriptedAddTask(step)
		case step == tasks+1:
			return scriptedToolCall("finalize", `{"change_summary":"built the report"}`)
		default:
			return "<final_answer><content>Built the automation.</content></final_answer>"
		}
	})
	agent.state.WorkingWorkflow = map[string]interface{}{}
	before := agent.workflowSnapshot()

	answer, err := agent.runToolLoop(stepBudgetTestContext(), core.NBAgentRequest{}, "system", "build it", toolLoopOptions{stepBudget: agent.sizedStepBudget})

	require.NoError(t, err)
	assert.Equal(t, tasks+3, *steps)
	workflowJSON, prose := agent.resolveToolLoopOutcome(answer, before)
	assert.Empty(t, prose)
	assert.Len(t, taskIDs(t, workflowJSON), tasks)
}

// A loop that keeps adding tasks and never finalizes stops at the ceiling and hands back no
// definition, so the caller has nothing to save.
func TestRunToolLoop_OutOfStepsReturnsNoDefinition(t *testing.T) {
	agent, steps := scriptedBuilder(func(step int) string {
		if step == 0 {
			return scriptedToolCall("init_workflow", scriptedInit)
		}
		return scriptedAddTask(step)
	})
	agent.state.WorkingWorkflow = map[string]interface{}{}
	before := agent.workflowSnapshot()

	answer, err := agent.runToolLoop(stepBudgetTestContext(), core.NBAgentRequest{}, "system", "build it", toolLoopOptions{stepBudget: agent.sizedStepBudget})

	require.Error(t, err)
	assert.Empty(t, answer)
	assert.Equal(t, toolLoopMaxSteps, *steps)
	assert.Contains(t, err.Error(), fmt.Sprintf("used all %d of its steps", toolLoopMaxSteps))
	_, kept := agent.finalizedAfterFailure(before)
	assert.False(t, kept, "nothing was finalized, so nothing may be saved")
}

// What the loop finalized is kept when it later runs out of steps; what it changed after that
// finalize is not.
func TestRunToolLoop_OutOfStepsKeepsOnlyWhatWasFinalized(t *testing.T) {
	agent, _ := scriptedBuilder(func(step int) string {
		switch step {
		case 0:
			return scriptedToolCall("init_workflow", scriptedInit)
		case 1:
			return scriptedAddTask(1)
		case 2:
			return scriptedToolCall("finalize", `{"change_summary":"first cut"}`)
		default:
			return scriptedAddTask(step)
		}
	})
	agent.state.WorkingWorkflow = map[string]interface{}{}
	before := agent.workflowSnapshot()

	_, err := agent.runToolLoop(stepBudgetTestContext(), core.NBAgentRequest{}, "system", "build it", toolLoopOptions{stepBudget: func() int { return 6 }})

	require.Error(t, err)
	finalized, kept := agent.finalizedAfterFailure(before)
	require.True(t, kept)
	assert.Equal(t, []string{"step-1"}, taskIDs(t, finalized))
}

// A turn that only answers a question keeps the fixed budget even on a large automation: the
// definition is already in its prompt.
func TestRunToolLoop_QuestionTurnKeepsFixedBudget(t *testing.T) {
	const tasks = 30
	existing := make([]interface{}, tasks)
	for i := range existing {
		existing[i] = map[string]interface{}{"id": fmt.Sprintf("step-%d", i), "type": "core.print"}
	}
	agent, steps := scriptedBuilder(func(step int) string {
		return scriptedToolCall("get_task", fmt.Sprintf(`{"task_id":"step-%d"}`, step))
	})
	agent.state.WorkingWorkflow = map[string]interface{}{"name": "big", "definition": map[string]interface{}{"tasks": existing}}

	answer, err := agent.runToolLoop(stepBudgetTestContext(), core.NBAgentRequest{}, "system", "what does it do?", toolLoopOptions{stepBudget: fixedStepBudget})

	require.Error(t, err)
	assert.Empty(t, answer, "the automation's JSON is not an answer to a question")
	assert.Equal(t, toolLoopBaseSteps, *steps)
}
