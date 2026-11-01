package agents

import (
	"strings"
	"testing"
)

// An instruction is the work itself. Without this, a run carrying one on a PR
// with no comments and no CI failures finished as "nothing to do" before the
// planner ever saw it.
func TestNothingForPlannerInstructionIsWork(t *testing.T) {
	cases := []struct {
		name        string
		instruction string
		pending     int
		ciLogs      string
		sync        baseSyncResult
		want        bool
	}{
		{name: "quiet PR, no instruction", want: true},
		{name: "quiet PR, clean base sync only", sync: baseSyncResult{preMergeHead: "abc123"}, want: true},
		{name: "instruction on a quiet PR", instruction: "set cpu to 200m", want: false},
		{name: "instruction after a clean base sync", instruction: "set cpu to 200m", sync: baseSyncResult{preMergeHead: "abc123"}, want: false},
		{name: "pending comment", pending: 1, want: false},
		{name: "CI failure", ciLogs: "lint failed", want: false},
		{name: "conflicted base merge", sync: baseSyncResult{conflicted: true}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nothingForPlanner(tc.instruction, tc.pending, tc.ciLogs, tc.sync); got != tc.want {
				t.Errorf("nothingForPlanner = %v, want %v", got, tc.want)
			}
		})
	}
}

// The instruction has to reach the model, and the prompt must scope the run to
// it — otherwise the planner goes looking for comments and CI failures and a run
// that answered an old comment reads as having made the change.
func TestBuildSystemPromptInstructionScopesTheRun(t *testing.T) {
	a := newBaseSyncAgent(t.TempDir())
	instruction := "Update the existing branch so it applies these values instead: cpu request 200m"

	prompt := a.buildSystemPrompt("o/r", "12", "details", "", "", "", nil, "main", baseSyncResult{}, instruction)

	for _, want := range []string{
		"## Requested change",
		instruction,
		"It is the whole task for this run",
		"Do not post comments",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("instruction prompt missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"The CI failures and review comments above are everything you need to address",
		"2. Address review comments from code reviewers",
	} {
		if strings.Contains(prompt, unwanted) {
			t.Errorf("instruction prompt should not contain %q", unwanted)
		}
	}

	// The regular followup prompt is unchanged.
	regular := a.buildSystemPrompt("o/r", "12", "details", "", "", "", nil, "main", baseSyncResult{}, "")
	if strings.Contains(regular, "## Requested change") {
		t.Error("regular followup prompt must not carry a requested-change section")
	}
	if !strings.Contains(regular, "The CI failures and review comments above are everything you need to address") {
		t.Error("regular followup prompt lost its job statement")
	}
	if strings.Contains(regular, "tool call.\n\n\nYou own") {
		t.Error("regular followup prompt gained an extra blank line")
	}
}
