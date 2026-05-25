package planners

import (
	"strings"
	"testing"
)

func TestBuildReflectionPrompt_IncludesGoalAndContract(t *testing.T) {
	g := BuildGoal("why does X fail?", "explore")
	out := buildReflectionPrompt(g, NewLedger(nil), nil)

	for _, want := range []string{
		"# Goal recap",
		"Original query: why does X fail?",
		"Mode: explore",
		"Termination criterion:",
		"# Contract for ready submission",
		"answer",
		"citations",
		"# Prior ledger",
		"empty",
		"# Recent investigation steps",
		"no new steps",
		"# Your task",
		"ready_to_submit",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("reflection prompt missing %q\nfull:\n%s", want, out)
		}
	}
}

func TestBuildReflectionPrompt_RendersRecentSteps(t *testing.T) {
	g := BuildGoal("q", "explore")
	steps := []Step{
		{Number: 3, Action: "file_view", Thought: "checking config", Observation: "1: image: foo\n2: version: bar"},
		{Number: 4, Action: "rg", Status: "failed", Error: "exit 1"},
	}
	out := buildReflectionPrompt(g, nil, steps)
	for _, want := range []string{
		"Step 3",
		"action=file_view",
		"image: foo",
		"Step 4",
		"action=rg",
		"ERROR: exit 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt missing %q\nfull:\n%s", want, out)
		}
	}
}

func TestBuildReflectionPrompt_ExposesPriorLedger(t *testing.T) {
	g := BuildGoal("q", "explore")
	prior := &Ledger{
		Citations: []LedgerCitation{{FilePath: "a.go", LineStart: 1, Snippet: "x"}},
		Answer:    "draft",
	}
	out := buildReflectionPrompt(g, prior, nil)
	if !strings.Contains(out, "a.go:1") {
		t.Errorf("prior ledger not surfaced in prompt:\n%s", out)
	}
	if strings.Contains(out, "empty — this is the first reflection") {
		t.Error("non-empty prior ledger marked as empty")
	}
}

func TestTruncateLine_CollapsesNewlinesAndClips(t *testing.T) {
	got := truncateLine("foo\nbar\nbaz", 100)
	if got != "foo bar baz" {
		t.Errorf("expected newline collapse, got %q", got)
	}

	got = truncateLine(strings.Repeat("x", 200), 20)
	if len(got) != 20 || !strings.HasSuffix(got, "...") {
		t.Errorf("expected 20-char truncation with ellipsis, got %q (len=%d)", got, len(got))
	}
}
