package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

// Real-world notebook-only outputs observed in the 49-call churn (conversation
// a6bae7da): the model recorded its finished answer INTO the notebook via an
// update_notebook tool-call instead of emitting <final_answer>. Both a complete
// CDATA variant and a truncated one occurred, back-to-back, and each was
// mislabeled ErrParseFailure — burning the reformat-retry loop.
const notebookOnlyCDATA = `<thought_action>
<thought>I have confirmed there are no crash-looping pods. I have sufficient evidence to provide the final answer.</thought>
<action>
    <tool_name>update_notebook</tool_name>
    <tool_input><![CDATA[## Answer Contract
- Sub-question 1: crash-looping pods in nudgebee-oss? -> None found [E1, E2]
## Plan
1. [DONE] Check nudgebee-oss pods [E1]
]]></tool_input>
</action>
</thought_action>`

const notebookOnlyTruncated = `<thought_action>
<thought>The previous final answer was truncated, so I need to provide the complete answer now.</thought>
<action>
    <tool_name>update_notebook</tool_name>
    <tool_input>{"notebook": "## Answer Contract\n- No crash-looping pods in nudgebee-oss`

const notebookInlineTag = `<thought>Recording progress.</thought><update_notebook>## Findings
- all pods healthy</update_notebook>`

// newNotebookTestPlanner builds a planner backed by notebookOptOutAgent, whose
// notebookSectionEnabled()==false makes processNotebookUpdate early-return (no
// ctx/DB needed). This is the LEAN-equivalent path — exactly where the churn was
// worst (lean discards the content AND mislabeled the turn as a parse failure).
func newNotebookTestPlanner() *NBReActPlanner3 {
	return &NBReActPlanner3{nbAgent: notebookOptOutAgent{}}
}

// TestIsNotebookOnlyOutput covers the detector across the observed variants and
// the negatives it must not trip on.
func TestIsNotebookOnlyOutput(t *testing.T) {
	p := &NBReActPlanner3{}
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"cdata update_notebook tool-call", notebookOnlyCDATA, true},
		{"truncated update_notebook tool-call", notebookOnlyTruncated, true},
		{"inline update_notebook tag", notebookInlineTag, true},
		{"normal kubectl action", `<thought_action><thought>x</thought><action><tool_name>kubectl_execute</tool_name><tool_input>kubectl get pods</tool_input></action></thought_action>`, false},
		{"final answer", `<final_answer><content>All healthy.</content></final_answer>`, false},
		{"malformed prose", `sorry, I cannot help with that`, false},
		{"empty", ``, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, p.isNotebookOnlyOutput(c.in))
		})
	}
}

// TestParseOutput_NotebookOnlyIsDistinctSentinel is the core of the fix: a
// notebook-only turn is classified as ErrNotebookOnlyTurn, NOT ErrParseFailure,
// so the caller nudges the model to continue instead of running the reformat
// loop that burns MaxRetries.
func TestParseOutput_NotebookOnlyIsDistinctSentinel(t *testing.T) {
	for _, out := range []string{notebookOnlyCDATA, notebookOnlyTruncated} {
		resp := &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: out}}}
		actions, finish, err := newNotebookTestPlanner().parseOutput(resp, nil)
		assert.Nil(t, actions)
		assert.Nil(t, finish)
		assert.Truef(t, errors.Is(err, ErrNotebookOnlyTurn), "want ErrNotebookOnlyTurn, got %v", err)
		assert.Falsef(t, errors.Is(err, ErrParseFailure), "notebook-only must NOT be classified as a parse failure")
	}
}

// TestParseOutput_MalformedStillParseFailure guards the negative: genuinely
// malformed (non-notebook) output stays ErrParseFailure so it still gets the
// reformat retry.
func TestParseOutput_MalformedStillParseFailure(t *testing.T) {
	resp := &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: `<thought_action><thought>oops, no action here</thought>`}}}
	_, _, err := newNotebookTestPlanner().parseOutput(resp, nil)
	assert.True(t, errors.Is(err, ErrParseFailure))
	assert.False(t, errors.Is(err, ErrNotebookOnlyTurn))
}

// TestParseOutput_NormalActionUnaffected guards the happy path: a real tool
// action parses normally and is never treated as notebook-only.
func TestParseOutput_NormalActionUnaffected(t *testing.T) {
	resp := &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: `<thought_action><thought>check pods</thought><action><tool_name>kubectl_execute</tool_name><tool_input>kubectl get pods</tool_input></action></thought_action>`}}}
	actions, finish, err := newNotebookTestPlanner().parseOutput(resp, nil)
	assert.NoError(t, err)
	assert.Nil(t, finish)
	assert.Len(t, actions, 1)
	assert.Equal(t, "kubectl_execute", actions[0].Tool)
}

// TestNotebookOnlyNudge checks the context-aware corrective: refine and the
// consecutive-cap both hard-demand a <final_answer>; the default allows either a
// next action or a final answer; all three tell the model not to re-record.
func TestNotebookOnlyNudge(t *testing.T) {
	forceFinal := notebookOnlyNudge(false, true)
	assert.Contains(t, forceFinal, "<final_answer>")
	assert.Contains(t, forceFinal, "MUST")

	refining := notebookOnlyNudge(true, false)
	assert.Contains(t, refining, "<final_answer>")

	def := notebookOnlyNudge(false, false)
	assert.Contains(t, def, "<thought_action>")
	assert.Contains(t, def, "<final_answer>")

	for _, s := range []string{forceFinal, refining, def} {
		assert.Contains(t, strings.ToLower(s), "notebook")
	}
}
