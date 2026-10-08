package agents

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// The builder's loop takes one step per model reply. Now and then the model writes out a whole
// run in a single reply instead: a tool call, the result it imagines, the next call, and so on,
// each in a new <thought_action> it never closes. The loop used to run the first call, drop the
// rest without a word, and — when the reply trailed off in an empty <final_answer> — end the
// build with the answer "</thought_action>", saving nothing.

const runAheadInit = `<thought_action>
<thought>Let's initialize the workflow.</thought>
<action>
    <tool_name>init_workflow</tool_name>
    <tool_input>{"name":"nightly-report","triggers":[{"type":"manual"}]}</tool_input>
</action>
<thought_action>
<thought>Now add the tasks.</thought>
<action>
    <tool_name>add_tasks</tool_name>
    <tool_input>{"tasks":[{"id":"imagined","type":"core.print","params":{"message":"never ran"}}]}</tool_input>
</action>
<thought_action>
<thought>finalize returned the workflow JSON. Now provide the final response.</thought>
`

// The second reply of the same run: it starts with a real call and trails off in a final answer
// that has nothing in it.
const runAheadThenEmptyAnswer = `<thought_action>
<thought>Let's list tasks to check if they exist.</thought>
<action>
    <tool_name>list_tasks</tool_name>
    <tool_input>{}</tool_input>
</action>
<thought_action>
<thought>Tasks are empty. Now add all tasks.</thought>
<action>
    <tool_name>add_tasks</tool_name>
    <tool_input>{"tasks":[{"id":"imagined","type":"core.print","params":{"message":"never ran"}}]}</tool_input>
</action>
<thought_action>
<thought>I can now present the final answer to the user.</thought>
<final_answer>
`

func TestFirstStep(t *testing.T) {
	oneCall := "<thought_action><thought>next</thought><action><tool_name>validate</tool_name><tool_input>{}</tool_input>"

	cases := []struct {
		name  string
		reply string
		want  string
	}{
		{
			name:  "one call, closing tags consumed by the stop word",
			reply: oneCall,
			want:  oneCall + "</action></thought_action>",
		},
		{
			name:  "one call, already closed",
			reply: oneCall + "</action></thought_action>",
			want:  oneCall + "</action></thought_action>",
		},
		{
			name:  "a run written out in one reply keeps its first call",
			reply: runAheadInit,
			want:  "<thought_action>\n<thought>Let's initialize the workflow.</thought>\n<action>\n    <tool_name>init_workflow</tool_name>\n    <tool_input>{\"name\":\"nightly-report\",\"triggers\":[{\"type\":\"manual\"}]}</tool_input></action></thought_action>",
		},
		{
			name:  "a call left open when the next step begins",
			reply: "<thought_action>\n<thought>Start it.</thought>\n<action>\n<tool_name>init_workflow</tool_name>\n<tool_input>{\"name\":\"nightly-report\"}</tool_input>\n<thought_action>\n<thought>Now check the task",
			want:  "<thought_action>\n<thought>Start it.</thought>\n<action>\n<tool_name>init_workflow</tool_name>\n<tool_input>{\"name\":\"nightly-report\"}</tool_input></action></thought_action>",
		},
		{
			name:  "a call followed by an empty final answer is a call",
			reply: runAheadThenEmptyAnswer,
			want:  "<thought_action>\n<thought>Let's list tasks to check if they exist.</thought>\n<action>\n    <tool_name>list_tasks</tool_name>\n    <tool_input>{}</tool_input></action></thought_action>",
		},
		{
			name:  "a call with no input, left open when the next step begins",
			reply: "<thought_action><action><tool_name>validate</tool_name>\n<thought_action><thought>next</thought><action><tool_name>finalize</tool_name><tool_input>{\"change_summary\":\"x\"}</tool_input>",
			want:  "<thought_action><action><tool_name>validate</tool_name></action></thought_action>",
		},
		{
			name:  "the loop's own tags inside a call's input are part of the input",
			reply: "<thought_action><action><tool_name>add_task</tool_name><tool_input>{\"message\":\"close </action>, then <final_answer> or <thought_action>\"}</tool_input>",
			want:  "<thought_action><action><tool_name>add_task</tool_name><tool_input>{\"message\":\"close </action>, then <final_answer> or <thought_action>\"}</tool_input></action></thought_action>",
		},
		{
			name:  "an <action> mentioned in text before the call",
			reply: "Please take <action> to fix this. <action><tool_name>init_workflow</tool_name><tool_input>{}</tool_input>",
			want:  "Please take <action> to fix this. <action><tool_name>init_workflow</tool_name><tool_input>{}</tool_input></action>",
		},
		{
			name:  "tags mentioned in a thought before a call that has no input",
			reply: "<thought_action><thought>wrap it in <action> and </action></thought><action><tool_name>validate</tool_name>",
			want:  "<thought_action><thought>wrap it in <action> and </action></thought><action><tool_name>validate</tool_name></action></thought_action>",
		},
		{
			name:  "a final answer, closing tag consumed by the stop word",
			reply: "<final_answer><content>Changed the schedule to hourly.</content>",
			want:  "<final_answer><content>Changed the schedule to hourly.</content></final_answer>",
		},
		{
			name:  "a thought and then a final answer",
			reply: "<thought_action><thought>done</thought>\n<final_answer><content>Done.</content>",
			want:  "<thought_action><thought>done</thought>\n<final_answer><content>Done.</content></final_answer></thought_action>",
		},
		{
			name:  "a final answer with nothing in it is not an answer",
			reply: "<thought_action><thought>done</thought>\n<final_answer>\n",
			want:  "<thought_action><thought>done</thought>\n</thought_action>",
		},
		{
			name:  "a final answer with nothing in it and nothing before it leaves nothing",
			reply: "<final_answer>\n",
			want:  "",
		},
		{
			name:  "neither a call nor an answer",
			reply: "<thought_action><thought>let me think",
			want:  "<thought_action><thought>let me think</thought_action>",
		},
		{
			name:  "plain text is left alone",
			reply: `{"name":"x","definition":{}}`,
			want:  `{"name":"x","definition":{}}`,
		},
		{
			name:  "an automation that mentions <action> in its own text is left alone",
			reply: `{"name":"x","definition":{"tasks":[{"id":"t","params":{"message":"take <action> now"}}]}}`,
			want:  `{"name":"x","definition":{"tasks":[{"id":"t","params":{"message":"take <action> now"}}]}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, firstStep(tc.reply))
		})
	}
}

// A reply has to end at its first call. Without </action> among the stop words nothing stops
// the model from writing a whole run: it leaves <thought_action> open, so that never matches.
func TestToolLoopStopWords_EndAReplyAtItsFirstCall(t *testing.T) {
	assert.Contains(t, toolLoopStopWords, "</action>")
}

// sentToModel is the text of every message of one model call, in order.
func sentToModel(messages []llms.MessageContent) []string {
	var texts []string
	for _, message := range messages {
		for _, part := range message.Parts {
			if text, ok := part.(llms.TextContent); ok {
				texts = append(texts, text.Text)
			}
		}
	}
	return texts
}

// recordingBuilder is scriptedBuilder that also keeps what the model was sent on each step.
func recordingBuilder(turn func(step int) string) (*WorkflowBuilderAgent, *[][]string) {
	agent, _ := scriptedBuilder(turn)
	scripted := agent.toolLoopModel
	var sent [][]string
	agent.toolLoopModel = func(messages []llms.MessageContent) (*llms.ContentResponse, error) {
		sent = append(sent, sentToModel(messages))
		return scripted(messages)
	}
	return agent, &sent
}

// Only the first call of a written-out run is run. The calls the model imagined after it never
// happened: the automation holds what the loop's own steps put there, and the model is not sent
// its imagined steps back as if they had.
func TestRunToolLoop_RunsOnlyTheFirstCallOfAWrittenOutRun(t *testing.T) {
	agent, sent := recordingBuilder(func(step int) string {
		switch step {
		case 0:
			return runAheadInit
		case 1:
			return scriptedToolCall("add_tasks", batchOf("fetch", "format"))
		default:
			return scriptedToolCall("finalize", `{"change_summary":"built it"}`)
		}
	})
	validatedBefore(agent, 2)
	agent.state.WorkingWorkflow = map[string]interface{}{}

	answer, err := agent.runToolLoop(stepBudgetTestContext(), core.NBAgentRequest{}, "system", "build it",
		toolLoopOptions{stepBudget: agent.sizedStepBudget, commitOnFinalize: true})

	require.NoError(t, err)
	require.Len(t, *sent, 3)
	assert.Equal(t, []string{"fetch", "format"}, taskIDs(t, answer), "the imagined task was never added")
	for _, text := range (*sent)[1] {
		assert.NotContains(t, text, "imagined", "the model's next step is not told about a call that never ran")
		assert.NotContains(t, text, "finalize returned")
	}
}

// A reply that runs a call and then trails off in an empty final answer does not end the loop,
// and a closing tag is never returned as the answer.
func TestRunToolLoop_AnEmptyFinalAnswerAfterACallDoesNotEndTheBuild(t *testing.T) {
	agent, steps := scriptedBuilder(func(step int) string {
		switch step {
		case 0:
			return scriptedToolCall("init_workflow", scriptedInit)
		case 1:
			return runAheadThenEmptyAnswer
		case 2:
			return scriptedToolCall("add_tasks", batchOf("fetch"))
		default:
			return "<final_answer><content>Built it.</content></final_answer>"
		}
	})
	agent.state.WorkingWorkflow = map[string]interface{}{}

	answer, err := agent.runToolLoop(stepBudgetTestContext(), core.NBAgentRequest{}, "system", "build it",
		toolLoopOptions{stepBudget: agent.sizedStepBudget})

	require.NoError(t, err)
	assert.Equal(t, 4, *steps, "the loop went on after the reply that trailed off")
	assert.Equal(t, "Built it.", answer)
	assert.False(t, strings.Contains(answer, "thought_action"))
	assert.Equal(t, []string{"fetch"}, taskIDs(t, agent.toolFinalize()))
}

// A reply that is an empty final answer and nothing else is asked again, and the model is never
// sent an empty message of its own: some providers refuse a conversation that holds one.
func TestRunToolLoop_AnEmptyFinalAnswerAloneIsAskedAgain(t *testing.T) {
	agent, sent := recordingBuilder(func(step int) string {
		if step == 0 {
			return "<final_answer>\n"
		}
		return "<final_answer><content>Built it.</content></final_answer>"
	})
	agent.state.WorkingWorkflow = map[string]interface{}{}

	answer, err := agent.runToolLoop(stepBudgetTestContext(), core.NBAgentRequest{}, "system", "build it",
		toolLoopOptions{stepBudget: agent.sizedStepBudget})

	require.NoError(t, err)
	assert.Equal(t, "Built it.", answer)
	require.Len(t, *sent, 2)
	for _, text := range (*sent)[1] {
		assert.NotEmpty(t, strings.TrimSpace(text))
	}
}
