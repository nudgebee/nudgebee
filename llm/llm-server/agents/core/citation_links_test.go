package core

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"

	"nudgebee/llm/security"
)

type fakeRowFinder struct {
	rows  []ToolCallRowRef
	err   error
	asked []string
}

func (f *fakeRowFinder) FindToolCallRows(_ string, toolIDs []string) ([]ToolCallRowRef, error) {
	f.asked = append(f.asked, toolIDs...)
	return f.rows, f.err
}

func citationTestCtx() *security.RequestContext {
	return security.NewRequestContext(context.Background(), &security.SecurityContext{}, slog.Default(), nil, nil)
}

func citedStep(displayID, toolID string) ToolInvocation {
	return ToolInvocation{DisplayID: displayID, Call: llms.ToolCall{ID: toolID}}
}

func TestLinkStepCitations(t *testing.T) {
	const agent, message = "agent-1", "msg-1"
	answer := "Pod restarted [Kubectl - E2](#task-E2) after OOM [Metrics - E3](#task-E3)."

	t.Run("each cited step links to the row it wrote", func(t *testing.T) {
		finder := &fakeRowFinder{rows: []ToolCallRowRef{
			{ID: "00000000-0000-4000-8000-000000000002", ToolID: "tool-b", AgentID: agent, MessageID: message},
			{ID: "00000000-0000-4000-8000-000000000003", ToolID: "tool-c", AgentID: agent, MessageID: message},
		}}
		got := linkStepCitations(citationTestCtx(), finder, "conv", agent, message, "k8s", "acct", answer,
			[]ToolInvocation{citedStep("E1", "tool-a"), citedStep("E2", "tool-b"), citedStep("E3", "tool-c")})
		assert.Equal(t, "Pod restarted [Kubectl - E2](#task-00000000-0000-4000-8000-000000000002) after OOM [Metrics - E3](#task-00000000-0000-4000-8000-000000000003).", got)
		assert.ElementsMatch(t, []string{"tool-b", "tool-c"}, finder.asked, "only cited steps are looked up")
	})

	t.Run("a cached step resolves to the row of the step that ran", func(t *testing.T) {
		cached := citedStep("E3", "tool-c-again")
		cached.SourceToolID = "tool-c"
		finder := &fakeRowFinder{rows: []ToolCallRowRef{
			{ID: "00000000-0000-4000-8000-000000000002", ToolID: "tool-b", AgentID: agent},
			{ID: "00000000-0000-4000-8000-000000000003", ToolID: "tool-c", AgentID: agent},
		}}
		got := linkStepCitations(citationTestCtx(), finder, "conv", agent, message, "k8s", "acct", answer,
			[]ToolInvocation{citedStep("E2", "tool-b"), cached})
		assert.Contains(t, got, "[Metrics - E3](#task-00000000-0000-4000-8000-000000000003)")
	})

	t.Run("citations that match no single row become plain text", func(t *testing.T) {
		finder := &fakeRowFinder{rows: []ToolCallRowRef{
			{ID: "row-x", ToolID: "tool-x", AgentID: "other-agent"},
			{ID: "row-y", ToolID: "tool-x", AgentID: "another-agent"},
		}}
		text := "[A - E1](#task-E1) [B - E2](#task-E2) [C - E9](#task-E9) [D - E4](#task-E4)"
		got := linkStepCitations(citationTestCtx(), finder, "conv", agent, message, "k8s", "acct", text,
			[]ToolInvocation{
				citedStep("E1", "tool-missing"),                      // no row
				citedStep("E2", "tool-p"), citedStep("E2", "tool-q"), // one number, two calls
				citedStep("E4", "tool-x"), // two rows, neither this agent's
			})
		assert.Equal(t, "A - E1 B - E2 C - E9 D - E4", got, "the label stays, only the link goes")
	})

	t.Run("lowercase ids resolve, malformed step links become plain text", func(t *testing.T) {
		finder := &fakeRowFinder{rows: []ToolCallRowRef{{ID: "00000000-0000-4000-8000-000000000002", ToolID: "tool-b", AgentID: agent}}}
		got := linkStepCitations(citationTestCtx(), finder, "conv", agent, message, "k8s", "acct",
			"[kubectl - e2](#task-e2) and [both](#task-E2,E3)", []ToolInvocation{citedStep("E2", "tool-b")})
		assert.Equal(t, "[kubectl - e2](#task-00000000-0000-4000-8000-000000000002) and both", got)
	})

	t.Run("links already pointing at rows and other links are left alone", func(t *testing.T) {
		text := "[Logs - E3](#task-0b6f2a1c-1111-4222-8333-944445555666) and [PR](https://example.com/pr/1)"
		finder := &fakeRowFinder{}
		got := linkStepCitations(citationTestCtx(), finder, "conv", agent, message, "k8s", "acct", text, nil)
		assert.Equal(t, text, got)
		assert.Empty(t, finder.asked)
	})

	t.Run("a lookup failure keeps the answer unchanged", func(t *testing.T) {
		finder := &fakeRowFinder{err: errors.New("db down")}
		got := linkStepCitations(citationTestCtx(), finder, "conv", agent, message, "k8s", "acct", answer,
			[]ToolInvocation{citedStep("E2", "tool-b"), citedStep("E3", "tool-c")})
		assert.Equal(t, answer, got)
	})

	t.Run("no row lookup without a finder or an agent id", func(t *testing.T) {
		invs := []ToolInvocation{citedStep("E2", "tool-b")}
		assert.Equal(t, answer, linkStepCitations(citationTestCtx(), nil, "conv", agent, message, "k8s", "acct", answer, invs))
		assert.Equal(t, answer, linkStepCitations(citationTestCtx(), &fakeRowFinder{}, "conv", "", message, "k8s", "acct", answer, invs))
	})
}

func TestPickToolCallRows(t *testing.T) {
	now := time.Now()
	rows := []ToolCallRowRef{
		// Same call in an earlier turn of this agent, and in the current one.
		{ID: "old-turn", ToolID: "t1", AgentID: "a", MessageID: "m0", CreatedAt: now.Add(-time.Hour)},
		{ID: "this-turn", ToolID: "t1", AgentID: "a", MessageID: "m1", CreatedAt: now.Add(-2 * time.Hour)},
		// Resumed turn: the step was written under the suspended message.
		{ID: "older", ToolID: "t2", AgentID: "a", MessageID: "m-a", CreatedAt: now.Add(-time.Hour)},
		{ID: "newer", ToolID: "t2", AgentID: "a", MessageID: "m-b", CreatedAt: now},
		// Only another agent stored this call (reused through the cache).
		{ID: "foreign", ToolID: "t3", AgentID: "b", MessageID: "m1"},
	}
	picked, problem := pickToolCallRows(rows, []string{"t1", "t2", "t3", "t4"}, "a", "m1")
	assert.Equal(t, "this-turn", picked["t1"], "the current message wins over a newer row elsewhere")
	assert.Equal(t, "newer", picked["t2"], "otherwise the latest row of this agent")
	assert.Equal(t, "foreign", picked["t3"], "another agent's row only when it is the only one")
	assert.Equal(t, citationNoRow, problem["t4"])
}

func TestStripUnresolvedStepLinks(t *testing.T) {
	row := "[Kubectl - E1](#task-91797f8b-71d7-4b64-9105-1de67bdee886)"
	in := "Found it [Logs - E3](#task-E3), see " + row + ", [x](#task-E1,E2) and [docs](https://example.com)."
	assert.Equal(t, "Found it Logs - E3, see "+row+", x and [docs](https://example.com).", StripUnresolvedStepLinks(in),
		"links to rows stay valid evidence for the parent; E-numbers and malformed ones go")
}

func TestNextStepCountSeed(t *testing.T) {
	withIDs := func(ids ...string) []NBAgentPlannerToolActionStep {
		var steps []NBAgentPlannerToolActionStep
		for _, id := range ids {
			steps = append(steps, NBAgentPlannerToolActionStep{Action: NBAgentPlannerToolAction{DisplayID: id}})
		}
		return steps
	}
	assert.Equal(t, 0, nextStepCountSeed(nil))
	assert.Equal(t, 3, nextStepCountSeed(withIDs("E1", "E2", "E3")))
	// E2 failed and was replaced in place by its retry, numbered E3: the list
	// has two steps but E3 is taken.
	assert.Equal(t, 3, nextStepCountSeed(withIDs("E1", "E3")))
	assert.Equal(t, 2, nextStepCountSeed(withIDs("", "")), "steps without ids fall back to the count")
}

func TestReuseCachedStep(t *testing.T) {
	cached := NBAgentPlannerToolActionStep{Action: NBAgentPlannerToolAction{ToolID: "ran-1", DisplayID: "E2"}}
	reuseCachedStep(&cached, NBAgentPlannerToolAction{ToolID: "call-9", DisplayID: "E9"})
	assert.Equal(t, "call-9", cached.Action.ToolID)
	assert.Equal(t, "E9", cached.Action.DisplayID, "the model sees the result under the new number")
	assert.Equal(t, "ran-1", cached.Action.SourceToolID)

	reuseCachedStep(&cached, NBAgentPlannerToolAction{ToolID: "call-12", DisplayID: "E12"})
	assert.Equal(t, "ran-1", cached.Action.SourceToolID, "a second reuse still points at the step that ran")
}

func TestReAct4_ToolResultsCarryTheirStepID(t *testing.T) {
	o := &NBReActPlanner4{}
	msgs := o.renderStepsToMessages([]NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{Tool: "kubectl", ToolInput: `{}`, ToolID: "c1", DisplayID: "E4"}, Observation: "pod running"},
		{Action: NBAgentPlannerToolAction{Tool: "kubectl", ToolInput: `{}`, ToolID: "c2"}, Observation: "no id"},
	})
	assert.Equal(t, "[E4] pod running", msgs[1].Parts[0].(llms.ToolCallResponse).Content)
	assert.Equal(t, "no id", msgs[3].Parts[0].(llms.ToolCallResponse).Content)
}

func TestBuildStepReferenceParts_UsesPlannerStepIDs(t *testing.T) {
	first, second := toolStep("aws", "region us-east-1"), toolStep("cloudwatch", "5xx spike")
	first.DisplayID, second.DisplayID = "E2", "E5" // E1, E3, E4 were dropped or waiting
	guide, supporting := buildStepReferenceParts(NBAgentResponse{AgentStepResponse: []ToolInvocation{first, second}}, AgentPlannerTypeReAct3)
	assert.Contains(t, guide, "E2 = aws")
	assert.Contains(t, guide, "E5 = cloudwatch")
	assert.NotContains(t, guide, "E1 =")
	assert.Contains(t, supporting, "[E5 (cloudwatch)]")
}

func TestExpandBareCitations(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"single", "uptime 47h [Kubectl - E4], cpu", "uptime 47h [Kubectl - E4](#task-E4), cpu"},
		{"numbers share the label before them", "[Shell Execute - E1, E9, E10].",
			"[Shell Execute - E1](#task-E1), [Shell Execute - E9](#task-E9), [Shell Execute - E10](#task-E10)."},
		{"each part keeps its own label", "[CLI Logs/Metrics - E3, Nudgebee - E4]",
			"[CLI Logs/Metrics - E3](#task-E3), [Nudgebee - E4](#task-E4)"},
		{"lowercase number", "[logs - e2]", "[logs - E2](#task-E2)"},
		{"a markdown link is kept", "[Logs - E3](#task-E3)", "[Logs - E3](#task-E3)"},
		{"an image is kept", "![chart - E3]", "![chart - E3]"},
		{"other brackets are kept", "level [INFO] and arr[0] and [see docs]", "level [INFO] and arr[0] and [see docs]"},
		{"double bracket from old answers", "[[events - E3](#task-E3)", "[[events - E3](#task-E3)"},
		{"code is kept", "run `x[E3]` then\n```\nrow[E4]\n```\nsee [Logs - E5]", "run `x[E3]` then\n```\nrow[E4]\n```\nsee [Logs - E5](#task-E5)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := expandBareCitations(c.in, func(string) bool { return true })
			assert.Equal(t, c.want, got)
		})
	}
}

func TestLinkStepCitations_BareCitations(t *testing.T) {
	const agent = "agent-1"
	row2, row9 := "00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000009"
	finder := &fakeRowFinder{rows: []ToolCallRowRef{
		{ID: row2, ToolID: "tool-b", AgentID: agent},
		{ID: row9, ToolID: "tool-i", AgentID: agent},
	}}
	got := linkStepCitations(citationTestCtx(), finder, "conv", agent, "msg", "k8s", "acct",
		"Restarted twice [Kubectl - E2, E9]; maintenance window [Site24x7 Alert - E0].",
		[]ToolInvocation{citedStep("E2", "tool-b"), citedStep("E9", "tool-i")})
	assert.Equal(t, "Restarted twice [Kubectl - E2](#task-"+row2+"), [Kubectl - E9](#task-"+row9+"); maintenance window [Site24x7 Alert - E0].", got,
		"a bare citation links like any other; a bracket naming no step of this run is left as written")
}

func TestExpandBareCitations_OnlyStepsOfThisRun(t *testing.T) {
	isStep := func(id string) bool { return id == "E2" }
	got, n := expandBareCitations("[Kubectl - E2] and [Error - E404] and [Logs - E2, E7]", isStep)
	assert.Equal(t, "[Kubectl - E2](#task-E2) and [Error - E404] and [Logs - E2, E7]", got)
	assert.Equal(t, 1, n)
}

func TestLinkStepCitations_LookupErrorKeepsAnswer(t *testing.T) {
	in := "Restarted [Kubectl - E2]."
	got := linkStepCitations(citationTestCtx(), &fakeRowFinder{err: errors.New("db down")}, "conv", "agent-1", "msg", "k8s", "acct", in,
		[]ToolInvocation{citedStep("E2", "tool-b")})
	assert.Equal(t, in, got)
}
