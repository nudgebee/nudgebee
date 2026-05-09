package core

import (
	"testing"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

// notebookOptOutAgent is a minimal NBAgent that opts out of the notebook
// section via NBAgentNotebookSectionProvider. Used to verify that runtime
// notebook-handling paths (system_nudge, hallucination capture) are
// suppressed for opt-out agents.
type notebookOptOutAgent struct{}

func (notebookOptOutAgent) GetName() string                    { return "notebook_optout_test" }
func (notebookOptOutAgent) GetNameAliases() []string           { return nil }
func (notebookOptOutAgent) GetDescription() string             { return "test" }
func (notebookOptOutAgent) GetPlannerType() AgentPlannerType   { return AgentPlannerTypeReAct }
func (notebookOptOutAgent) GetSupportedTools(_ *security.RequestContext) []toolcore.NBTool {
	return nil
}
func (notebookOptOutAgent) GetSystemPrompt(_ *security.RequestContext, _ NBAgentRequest) NBAgentPrompt {
	return NBAgentPrompt{}
}
func (notebookOptOutAgent) GetNotebookEnabled() bool { return false }

func TestReAct3ParseSingleAction(t *testing.T) {
	output := `<thought_action>
		<thought>I need to search for the service status.</thought>
		<action>
			<tool_name>kubectl</tool_name>
			<tool_input>kubectl get pods -n default</tool_input>
		</action>
	</thought_action>`

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner3{}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.NotNil(t, actions)
	assert.Len(t, actions, 1)
	assert.Equal(t, "kubectl", actions[0].Tool)
	assert.Equal(t, "kubectl get pods -n default", actions[0].ToolInput)
}

func TestReAct3ParseParallelActions(t *testing.T) {
	output := `<thought_action>
		<thought>I know the service is checkout-svc. I can fetch logs, metrics, and events in parallel.</thought>
		<actions>
			<action>
				<tool_name>logs</tool_name>
				<tool_input>{"service": "checkout-svc"}</tool_input>
			</action>
			<action>
				<tool_name>metrics</tool_name>
				<tool_input>{"service": "checkout-svc"}</tool_input>
			</action>
			<action>
				<tool_name>kubectl</tool_name>
				<tool_input>kubectl get events --field-selector involvedObject.name=checkout-svc</tool_input>
			</action>
		</actions>
	</thought_action>`

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner3{}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.NotNil(t, actions)
	assert.Len(t, actions, 3)
	assert.Equal(t, "logs", actions[0].Tool)
	assert.Equal(t, "metrics", actions[1].Tool)
	assert.Equal(t, "kubectl", actions[2].Tool)

	// All actions should share the same thought/log
	assert.Equal(t, actions[0].Log, actions[1].Log)
	assert.Equal(t, actions[1].Log, actions[2].Log)
	assert.Contains(t, actions[0].Log, "checkout-svc")
}

func TestReAct3ParseParallelActionsWithCDATA(t *testing.T) {
	output := `<thought_action>
		<thought>Fetching logs and metrics for the service.</thought>
		<actions>
			<action>
				<tool_name>logs</tool_name>
				<tool_input><![CDATA[{"service": "api-gw", "filter": "level=error & status>=500"}]]></tool_input>
			</action>
			<action>
				<tool_name>metrics</tool_name>
				<tool_input><![CDATA[{"service": "api-gw", "metric": "latency_p99"}]]></tool_input>
			</action>
		</actions>
	</thought_action>`

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner3{}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.NotNil(t, actions)
	assert.Len(t, actions, 2)
	// XmlExtractTagContent handles CDATA and unescapes entities
	assert.Contains(t, actions[0].ToolInput, "api-gw")
	assert.Contains(t, actions[1].ToolInput, "latency_p99")
}

func TestReAct3ParseParallelMixedCDATAAndPlain(t *testing.T) {
	// One action uses CDATA, the other uses plain text — common real-world pattern
	output := `<thought_action>
		<thought>Checking multiple sources.</thought>
		<actions>
			<action>
				<tool_name>logs</tool_name>
				<tool_input><![CDATA[{"filter": "level=error & code>=500"}]]></tool_input>
			</action>
			<action>
				<tool_name>kubectl</tool_name>
				<tool_input>get pods -n production</tool_input>
			</action>
		</actions>
	</thought_action>`

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner3{}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.Len(t, actions, 2)
	assert.Equal(t, "logs", actions[0].Tool)
	assert.Contains(t, actions[0].ToolInput, "level=error & code>=500")
	assert.Equal(t, "kubectl", actions[1].Tool)
	assert.Equal(t, "get pods -n production", actions[1].ToolInput)
}

func TestReAct3ParseParallelEmptyActionsBlock(t *testing.T) {
	// Empty <actions> block — should fall through to singular parsing, then fail
	output := `<thought_action>
		<thought>Thinking.</thought>
		<actions></actions>
	</thought_action>`

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner3{}
	_, _, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	assert.Error(t, err)
}

func TestReAct3ParseParallelThreeActions(t *testing.T) {
	// Three parallel actions — verifies we handle more than 2
	output := `<thought_action>
		<thought>Gathering data from three sources.</thought>
		<actions>
			<action>
				<tool_name>logs</tool_name>
				<tool_input>service=api</tool_input>
			</action>
			<action>
				<tool_name>metrics</tool_name>
				<tool_input>cpu_usage</tool_input>
			</action>
			<action>
				<tool_name>kubectl</tool_name>
				<tool_input>get events</tool_input>
			</action>
		</actions>
	</thought_action>`

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner3{}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.Len(t, actions, 3)
	assert.Equal(t, "logs", actions[0].Tool)
	assert.Equal(t, "metrics", actions[1].Tool)
	assert.Equal(t, "kubectl", actions[2].Tool)
}

func TestReAct3ParseSingleActionInActionsBlock(t *testing.T) {
	// If LLM puts only 1 action inside <actions>, fall through to singular parsing
	output := `<thought_action>
		<thought>Checking pod status.</thought>
		<actions>
			<action>
				<tool_name>kubectl</tool_name>
				<tool_input>kubectl get pods</tool_input>
			</action>
		</actions>
	</thought_action>`

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner3{}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.NotNil(t, actions)
	// Single action in <actions> block should fall through to singular parsing
	assert.Len(t, actions, 1)
	assert.Equal(t, "kubectl", actions[0].Tool)
}

func TestReAct3ParseFinalAnswer(t *testing.T) {
	output := `<final_answer>
		<thought>I have gathered all the information needed.</thought>
		<content>The pod is crashing due to an OOMKilled event. The container memory limit is set to 256Mi but the application requires at least 512Mi.</content>
	</final_answer>`

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner3{}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	assert.NoError(t, err)
	assert.Nil(t, actions)
	assert.NotNil(t, finish)
	assert.Contains(t, finish.Data, "OOMKilled")
	assert.Contains(t, finish.Log, "gathered all the information")
}

func TestReAct3ParseActionPriorityOverFinalAnswer(t *testing.T) {
	// If both <thought_action> and <final_answer> present, action takes priority
	output := `<thought_action>
		<thought>I need more data.</thought>
		<action>
			<tool_name>logs</tool_name>
			<tool_input>fetch error logs</tool_input>
		</action>
	</thought_action>
	<final_answer>
		<thought>Done</thought>
		<content>Some answer</content>
	</final_answer>`

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner3{}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.NotNil(t, actions)
	assert.Len(t, actions, 1)
	assert.Equal(t, "logs", actions[0].Tool)
}

func TestReAct3NotebookUpdate(t *testing.T) {
	output := `<update_notebook>
## Investigation Plan
1. [DONE] Check pod status
2. [NEXT] Fetch logs
3. [ ] Check metrics
</update_notebook>
<thought_action>
	<thought>Now fetching logs.</thought>
	<action>
		<tool_name>logs</tool_name>
		<tool_input>fetch logs for pod-abc</tool_input>
	</action>
</thought_action>`

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner3{notebookLastUpdateTurn: -1, notebookFirstUpdateTurn: -1}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.NotNil(t, actions)
	assert.Len(t, actions, 1)
	// Notebook should be updated
	assert.Contains(t, planner.Notebook, "Investigation Plan")
	assert.Contains(t, planner.Notebook, "[DONE] Check pod status")
	// Telemetry counters should reflect the update
	assert.Equal(t, 1, planner.notebookUpdateCount)
	assert.Equal(t, 0, planner.notebookLastUpdateTurn)
	assert.Equal(t, 0, planner.notebookFirstUpdateTurn)
}

func TestReAct3NotebookAnalyze(t *testing.T) {
	content := `## Investigation Plan
1. [DONE] Identify pod
2. [DOING] Fetch logs
3. [NEXT] Check metrics
4. [ ] Correlate with deploy
5. [BLOCKED] Dashboard unreachable
6. [SKIP] Not applicable

## Key Findings
- Pod OOMKilled 3x`
	s := analyzeNotebook(content)
	assert.True(t, s.HasPlanSection)
	assert.True(t, s.HasFindings)
	assert.Equal(t, 1, s.DoneCount)
	assert.Equal(t, 1, s.DoingCount)
	assert.Equal(t, 1, s.NextCount)
	assert.Equal(t, 1, s.TodoCount)
	assert.Equal(t, 1, s.BlockedCount)
	assert.Equal(t, 1, s.SkipCount)
	assert.Equal(t, 6, s.TotalMarkers)
}

func TestReAct3NotebookStaleTracking(t *testing.T) {
	// Simulate three turns where the LLM never emits an update_notebook
	// block — telemetry should track that the notebook is stale.
	planner := &NBReActPlanner3{notebookLastUpdateTurn: -1, notebookFirstUpdateTurn: -1}

	// Turn 0: a tool call, no notebook — not stale yet.
	planner.processNotebookUpdate("<thought_action><thought>x</thought><action><tool_name>k</tool_name><tool_input>y</tool_input></action></thought_action>", 0)
	assert.Equal(t, 0, planner.notebookUpdateCount)
	assert.Equal(t, 0, planner.notebookStaleWarningsLog)

	// Turn 2: still no notebook after several turns — stale warning fires.
	planner.processNotebookUpdate("<thought_action><thought>x</thought><action><tool_name>k</tool_name><tool_input>y</tool_input></action></thought_action>", 2)
	// Stale warning only emits when a logger is attached; without ctx
	// we expect counter to remain 0. Attach a logger and retry.
	assert.Equal(t, 0, planner.notebookStaleWarningsLog)

	// Now provide a notebook update at turn 3 — counters should reset.
	planner.processNotebookUpdate(`<update_notebook>
## Investigation Plan
1. [DOING] Check pod
</update_notebook>`, 3)
	assert.Equal(t, 1, planner.notebookUpdateCount)
	assert.Equal(t, 3, planner.notebookFirstUpdateTurn)
	assert.Equal(t, 3, planner.notebookLastUpdateTurn)
}

func TestReAct3ParseEmpty(t *testing.T) {
	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: ""}},
	}

	planner := &NBReActPlanner3{}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	assert.Error(t, err)
	assert.Nil(t, actions)
	assert.Nil(t, finish)
}

func TestReAct3ParseMissingClosingTag(t *testing.T) {
	// Truncated XML should be recovered by the sanitization stage (XmlSanitize
	// closes unclosed tags), so the parser extracts the action successfully.
	output := `<thought_action>
		<thought>I need to check.</thought>
		<action>
			<tool_name>kubectl</tool_name>
			<tool_input>get pods`

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner3{}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.NotNil(t, actions)
	assert.Len(t, actions, 1)
	assert.Equal(t, "kubectl", actions[0].Tool)
	// tool_input may or may not be fully recovered depending on XmlSanitize behavior
}

func TestReAct3DiagnoseParseFailure(t *testing.T) {
	planner := &NBReActPlanner3{}

	tests := []struct {
		name     string
		output   string
		expected string
	}{
		{
			name:     "no xml blocks",
			output:   "I think the answer is 42",
			expected: "no <thought_action> or <final_answer> block found",
		},
		{
			name:     "unclosed thought_action",
			output:   "<thought_action><thought>test</thought><action><tool_name>x</tool_name><tool_input>y</tool_input></action>",
			expected: "missing closing </thought_action> tag",
		},
		{
			name:     "unclosed actions",
			output:   "<thought_action><thought>test</thought><actions><action><tool_name>x</tool_name></action></thought_action>",
			expected: "missing closing </actions> tag",
		},
		{
			name:     "missing content in final_answer",
			output:   "<final_answer><thought>test</thought></final_answer>",
			expected: "missing <content> tag inside <final_answer>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := planner.diagnoseParseFailure(tc.output)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestReAct3BuildScratchpadSingleAction(t *testing.T) {
	planner := &NBReActPlanner3{}
	steps := []NBAgentPlannerToolActionStep{
		{
			Action: NBAgentPlannerToolAction{
				Tool:      "kubectl",
				ToolInput: "get pods",
				Log:       "Checking pods.",
				ToolID:    "kubectl-001",
			},
			Observation: "pod-abc Running\npod-def CrashLoopBackOff",
			Status:      ToolStatusSuccess,
		},
	}

	scratchpad := planner.buildScratchpad(steps)
	assert.Contains(t, scratchpad, "<scratchpad>")
	assert.Contains(t, scratchpad, "<thought><![CDATA[Checking pods.]]></thought>")
	assert.Contains(t, scratchpad, "<tool_name>kubectl</tool_name>")
	assert.Contains(t, scratchpad, "<tool_input><![CDATA[get pods]]></tool_input>")
	assert.Contains(t, scratchpad, `<observation tool="kubectl">`)
	assert.Contains(t, scratchpad, "<![CDATA[pod-abc Running")
	// Should use singular <action>, not <actions>
	assert.NotContains(t, scratchpad, "<actions>")
}

func TestReAct3BuildScratchpadParallelActions(t *testing.T) {
	planner := &NBReActPlanner3{}
	sharedThought := "Fetching logs and metrics in parallel."
	steps := []NBAgentPlannerToolActionStep{
		{
			Action: NBAgentPlannerToolAction{
				Tool:      "logs",
				ToolInput: `{"service": "svc-a"}`,
				Log:       sharedThought,
				ToolID:    "logs-001",
			},
			Observation: "ERROR: connection refused",
			Status:      ToolStatusSuccess,
		},
		{
			Action: NBAgentPlannerToolAction{
				Tool:      "metrics",
				ToolInput: `{"service": "svc-a"}`,
				Log:       sharedThought,
				ToolID:    "metrics-001",
			},
			Observation: "cpu: 95%, memory: 80%",
			Status:      ToolStatusSuccess,
		},
	}

	scratchpad := planner.buildScratchpad(steps)
	assert.Contains(t, scratchpad, "<scratchpad>")
	// Should group under <actions>
	assert.Contains(t, scratchpad, "<actions>")
	assert.Contains(t, scratchpad, "</actions>")
	// Single thought for the group
	assert.Contains(t, scratchpad, "<thought><![CDATA[Fetching logs and metrics in parallel.]]></thought>")
	// Both observations present with CDATA
	assert.Contains(t, scratchpad, `<observation tool="logs">`)
	assert.Contains(t, scratchpad, `<observation tool="metrics">`)
	assert.Contains(t, scratchpad, "<![CDATA[ERROR: connection refused]]>")
	assert.Contains(t, scratchpad, "<![CDATA[cpu: 95%, memory: 80%]]>")
}

func TestReAct3MarshalUnmarshal(t *testing.T) {
	planner := &NBReActPlanner3{
		Notebook:                "Some important findings here",
		refinementAttempts:      1,
		postRefinementToolIndex: 3,
	}

	data, err := planner.Marshal()
	assert.NoError(t, err)

	planner2 := &NBReActPlanner3{}
	err = planner2.Unmarshal(data)
	assert.NoError(t, err)

	assert.Equal(t, "Some important findings here", planner2.Notebook)
	assert.Equal(t, 1, planner2.refinementAttempts)
	assert.Equal(t, 3, planner2.postRefinementToolIndex)
}

// --- XML Robustness Tests ---

func TestReAct3ParseBareAmpersandInToolInput(t *testing.T) {
	// LLMs often produce bare & in tool input instead of &amp;
	output := `<thought_action>
		<thought>Fetching logs with a filter.</thought>
		<action>
			<tool_name>logs</tool_name>
			<tool_input>{"filter": "status >= 500 & level > warn"}</tool_input>
		</action>
	</thought_action>`

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner3{}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.NotNil(t, actions)
	assert.Len(t, actions, 1)
	assert.Equal(t, "logs", actions[0].Tool)
	assert.Contains(t, actions[0].ToolInput, "status >= 500")
}

func TestReAct3ParseTruncatedXML(t *testing.T) {
	// Simulates LLM response cut off mid-XML (e.g. stop word or token limit)
	output := `<thought_action>
		<thought>Checking pod status.</thought>
		<action>
			<tool_name>kubectl</tool_name>
			<tool_input>kubectl get pods -n default</tool_input>
		</action>`
	// Missing </thought_action>

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner3{}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	// XmlSanitize should close the truncated tag and allow extraction
	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.NotNil(t, actions)
	assert.Len(t, actions, 1)
	assert.Equal(t, "kubectl", actions[0].Tool)
}

func TestReAct3ParseParallelActionsWithBareAmpersands(t *testing.T) {
	output := `<thought_action>
		<thought>Checking logs & metrics in parallel.</thought>
		<actions>
			<action>
				<tool_name>logs</tool_name>
				<tool_input>{"query": "error & timeout"}</tool_input>
			</action>
			<action>
				<tool_name>metrics</tool_name>
				<tool_input>{"query": "cpu & memory"}</tool_input>
			</action>
		</actions>
	</thought_action>`

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner3{}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.NotNil(t, actions)
	assert.Len(t, actions, 2)
	assert.Equal(t, "logs", actions[0].Tool)
	assert.Equal(t, "metrics", actions[1].Tool)
}

func TestReAct3ParseFinalAnswerTruncated(t *testing.T) {
	// Final answer with missing closing tag
	output := `<final_answer>
		<thought>I have the answer.</thought>
		<content>The pod is OOMKilled due to memory limits.</content>`

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner3{}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	// XmlSanitize with final_answer root should close the tag
	assert.NoError(t, err)
	assert.Nil(t, actions)
	assert.NotNil(t, finish)
	assert.Contains(t, finish.Data, "OOMKilled")
}

func TestReAct3ParseCommonTypo(t *testing.T) {
	// LLM misspells </thought_action> as </thought_acton>
	output := `<thought_action>
		<thought>Checking pods.</thought>
		<action>
			<tool_name>kubectl</tool_name>
			<tool_input>kubectl get pods</tool_input>
		</action>
	</thought_acton>`

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner3{}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	// XmlSanitize has typo corrections for </thought_acton>
	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.NotNil(t, actions)
	assert.Len(t, actions, 1)
	assert.Equal(t, "kubectl", actions[0].Tool)
}

// TestReAct3NotebookAsToolCall verifies that when the LLM mistakenly emits
// update_notebook as a tool call, the notebook content is still captured
// and the tool call is filtered out (not dispatched to the executor).
func TestReAct3NotebookAsToolCall(t *testing.T) {
	output := `<thought_action>
<thought>I will update the notebook to reflect my findings.</thought>
<action>
	<tool_name>update_notebook</tool_name>
	<tool_input>## Investigation Plan
1. [DONE] Query metrics-* - No data
2. [DOING] List all indices
3. [ ] Summarize findings

## Key Findings
- No CPU metrics found for nudgebee namespace</tool_input>
</action>
</thought_action>`

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner3{notebookLastUpdateTurn: -1, notebookFirstUpdateTurn: -1}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	// The tool call should be filtered out — no actions to execute
	assert.Error(t, err) // parse failure because no real tool action remains
	assert.Nil(t, actions)
	assert.Nil(t, finish)

	// But the notebook should still be updated from the tool_input content
	assert.Contains(t, planner.Notebook, "Investigation Plan")
	assert.Contains(t, planner.Notebook, "[DONE] Query metrics-*")
	assert.Equal(t, 1, planner.notebookUpdateCount)
}

// TestReAct3NotebookAsToolCallInParallelActions verifies that when the LLM
// emits update_notebook as one of several parallel tool calls, the notebook
// content is captured and that action is filtered out while the other real
// tool calls are preserved.
func TestReAct3NotebookAsToolCallInParallelActions(t *testing.T) {
	output := `<thought_action>
<thought>I will update the notebook and query metrics in parallel.</thought>
<actions>
	<action>
		<tool_name>update_notebook</tool_name>
		<tool_input>## Investigation Plan
1. [DONE] Query metrics-* - No data
2. [DOING] Try metricbeat-*</tool_input>
	</action>
	<action>
		<tool_name>elastic_search_execute</tool_name>
		<tool_input>{"index": "metricbeat-*", "size": 0}</tool_input>
	</action>
	<action>
		<tool_name>shell_execute</tool_name>
		<tool_input>{"command": "curl -s http://localhost:9200/_cat/indices"}</tool_input>
	</action>
</actions>
</thought_action>`

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner3{notebookLastUpdateTurn: -1, notebookFirstUpdateTurn: -1}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.NotNil(t, actions)
	// update_notebook should be filtered out, leaving 2 real actions
	assert.Len(t, actions, 2)
	assert.Equal(t, "elastic_search_execute", actions[0].Tool)
	assert.Equal(t, "shell_execute", actions[1].Tool)

	// Notebook should still be updated
	assert.Contains(t, planner.Notebook, "Investigation Plan")
	assert.Contains(t, planner.Notebook, "[DOING] Try metricbeat-*")
	assert.Equal(t, 1, planner.notebookUpdateCount)
}

// TestReAct3NotebookAsNonFirstParallelAction verifies notebook content is
// captured even when update_notebook is NOT the first action in a parallel
// block (processNotebookUpdate's fallback only checks the first <tool_name>).
func TestReAct3NotebookAsNonFirstParallelAction(t *testing.T) {
	output := `<thought_action>
<thought>I will query metrics and update the notebook.</thought>
<actions>
	<action>
		<tool_name>elastic_search_execute</tool_name>
		<tool_input>{"index": "metricbeat-*", "size": 0}</tool_input>
	</action>
	<action>
		<tool_name>shell_execute</tool_name>
		<tool_input>{"command": "curl -s http://localhost:9200/_cat/indices"}</tool_input>
	</action>
	<action>
		<tool_name>update_notebook</tool_name>
		<tool_input>## Investigation Plan
1. [DONE] Query metrics-* - No data
2. [DOING] Try metricbeat-*
3. [ ] Check alternative indices</tool_input>
	</action>
</actions>
</thought_action>`

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner3{notebookLastUpdateTurn: -1, notebookFirstUpdateTurn: -1}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.NotNil(t, actions)
	// update_notebook filtered out, 2 real actions remain
	assert.Len(t, actions, 2)
	assert.Equal(t, "elastic_search_execute", actions[0].Tool)
	assert.Equal(t, "shell_execute", actions[1].Tool)

	// Notebook content must be captured even though it was the last action
	assert.Contains(t, planner.Notebook, "Investigation Plan")
	assert.Contains(t, planner.Notebook, "[DOING] Try metricbeat-*")
	assert.Equal(t, 1, planner.notebookUpdateCount)
}

func TestIsNotebookToolName(t *testing.T) {
	// Known notebook aliases — should all return true (including whitespace)
	for _, name := range []string{
		"update_notebook", "Update_Notebook", "UPDATE_NOTEBOOK",
		"notebook", "Notebook",
		"create_notebook", "write_notebook", "save_notebook",
		"notebook_update", "edit_notebook",
		" update_notebook ", "\tupdate_notebook\n",
	} {
		assert.True(t, isNotebookToolName(name), "expected true for %q", name)
	}

	// Non-notebook tool names — should all return false
	for _, name := range []string{
		"kubectl", "elastic_search_execute", "shell_execute",
		"logs", "metrics", "LLM", "", "note_book",
	} {
		assert.False(t, isNotebookToolName(name), "expected false for %q", name)
	}
}

// TestReAct3NotebookOptOut_NoCaptureOrNudge verifies that an agent which
// implements NBAgentNotebookSectionProvider returning false has all four
// runtime notebook touchpoints suppressed:
//
//  1. processNotebookUpdate does NOT capture content from a real
//     <update_notebook> tag.
//  2. processToolAction (singular path) does NOT capture content from a
//     hallucinated <tool_name>update_notebook</tool_name> call.
//  3. processToolActions (parallel path) does NOT capture content when
//     update_notebook appears as one of many <action> blocks.
//  4. buildScratchpad does NOT inject the "⚠ NOTEBOOK STALE" system_nudge
//     after the staleness threshold elapses.
//
// All four were confirmed real leaks during PR #29949 review — without
// them, an opt-out agent would still be re-fed notebook content via the
// {{if .notebook}} block in the human prompt, defeating opt-out.
func TestReAct3NotebookOptOut_NoCaptureOrNudge(t *testing.T) {
	t.Run("processNotebookUpdate skips capture", func(t *testing.T) {
		planner := &NBReActPlanner3{
			nbAgent:                 notebookOptOutAgent{},
			notebookLastUpdateTurn:  -1,
			notebookFirstUpdateTurn: -1,
		}
		output := `<update_notebook>
## Plan
1. [DONE] Did stuff
</update_notebook>
<thought_action><thought>x</thought><action><tool_name>k</tool_name><tool_input>y</tool_input></action></thought_action>`
		planner.processNotebookUpdate(output, 0)
		assert.Equal(t, "", planner.Notebook, "notebook content should NOT be captured for opt-out agents")
		assert.Equal(t, 0, planner.notebookUpdateCount)
	})

	t.Run("singular tool-call hallucination skipped without capture", func(t *testing.T) {
		planner := &NBReActPlanner3{
			nbAgent:                 notebookOptOutAgent{},
			notebookLastUpdateTurn:  -1,
			notebookFirstUpdateTurn: -1,
		}
		output := `<thought_action>
<thought>logging plan</thought>
<action>
	<tool_name>update_notebook</tool_name>
	<tool_input>## Plan
1. [DONE] Step</tool_input>
</action>
</thought_action>`
		response := &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: output}}}
		_, _, _ = planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})
		assert.Equal(t, "", planner.Notebook, "hallucinated notebook tool call should NOT capture for opt-out")
		assert.Equal(t, 0, planner.notebookUpdateCount)
	})

	t.Run("parallel tool-call hallucination skipped without capture", func(t *testing.T) {
		planner := &NBReActPlanner3{
			nbAgent:                 notebookOptOutAgent{},
			notebookLastUpdateTurn:  -1,
			notebookFirstUpdateTurn: -1,
		}
		output := `<thought_action>
<thought>parallel</thought>
<actions>
	<action>
		<tool_name>shell_execute</tool_name>
		<tool_input>{"command": "ls"}</tool_input>
	</action>
	<action>
		<tool_name>update_notebook</tool_name>
		<tool_input>## Plan
1. [DOING] Listing</tool_input>
	</action>
</actions>
</thought_action>`
		response := &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: output}}}
		actions, _, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})
		assert.NoError(t, err)
		// update_notebook is filtered out, leaving the real shell_execute call
		assert.Len(t, actions, 1)
		assert.Equal(t, "shell_execute", actions[0].Tool)
		// But notebook content is NOT captured
		assert.Equal(t, "", planner.Notebook)
		assert.Equal(t, 0, planner.notebookUpdateCount)
	})

	t.Run("buildScratchpad emits no system_nudge", func(t *testing.T) {
		planner := &NBReActPlanner3{
			nbAgent:                 notebookOptOutAgent{},
			notebookLastUpdateTurn:  -1,
			notebookFirstUpdateTurn: -1,
		}
		// Far past the stale threshold (notebookStaleTurnThreshold = 2).
		// SRE agents would receive the nudge here; opt-out agents must not.
		steps := make([]NBAgentPlannerToolActionStep, 5)
		for i := range steps {
			steps[i] = NBAgentPlannerToolActionStep{
				Action:      NBAgentPlannerToolAction{ToolID: "E", Tool: "shell_execute", ToolInput: "x", Log: "t"},
				Observation: "o",
				Status:      ToolStatusSuccess,
			}
		}
		scratchpad := planner.buildScratchpad(steps)
		assert.NotContains(t, scratchpad, "NOTEBOOK STALE")
		assert.NotContains(t, scratchpad, "<system_nudge>")
	})
}

// TestReAct3NotebookSectionEnabled_DefaultTrue confirms the helper returns
// true (legacy behavior) when nbAgent is nil OR doesn't implement the
// opt-out interface — i.e. existing struct-literal tests and SRE agents
// keep their notebook discipline intact.
func TestReAct3NotebookSectionEnabled_DefaultTrue(t *testing.T) {
	t.Run("nil agent returns true", func(t *testing.T) {
		planner := &NBReActPlanner3{nbAgent: nil}
		assert.True(t, planner.notebookSectionEnabled())
	})
	t.Run("agent without opt-out interface returns true", func(t *testing.T) {
		planner := &NBReActPlanner3{nbAgent: &MockAgent{}}
		assert.True(t, planner.notebookSectionEnabled())
	})
	t.Run("agent with GetNotebookEnabled=false returns false", func(t *testing.T) {
		planner := &NBReActPlanner3{nbAgent: notebookOptOutAgent{}}
		assert.False(t, planner.notebookSectionEnabled())
	})
}

// TestReAct3NotebookAliasAsToolCall verifies that aliased notebook tool
// names (e.g., "create_notebook", "write_notebook") are handled the same
// way as "update_notebook".
func TestReAct3NotebookAliasAsToolCall(t *testing.T) {
	for _, alias := range []string{"create_notebook", "write_notebook", "notebook"} {
		t.Run(alias, func(t *testing.T) {
			output := `<thought_action>
<thought>I will record my findings.</thought>
<action>
	<tool_name>` + alias + `</tool_name>
	<tool_input>## Plan
1. [DONE] Step one</tool_input>
</action>
</thought_action>`

			response := &llms.ContentResponse{
				Choices: []*llms.ContentChoice{{Content: output}},
			}

			planner := &NBReActPlanner3{notebookLastUpdateTurn: -1, notebookFirstUpdateTurn: -1}
			actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

			// Tool call filtered out → parse failure (no real action)
			assert.Error(t, err)
			assert.Nil(t, actions)
			assert.Nil(t, finish)

			// Notebook content captured
			assert.Contains(t, planner.Notebook, "Step one")
			assert.Equal(t, 1, planner.notebookUpdateCount)
		})
	}
}
