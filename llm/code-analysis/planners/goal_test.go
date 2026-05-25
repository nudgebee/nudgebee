package planners

import (
	"strings"
	"testing"
)

func TestBuildGoal_ExploreMode(t *testing.T) {
	g := BuildGoal("how does auth work?", "explore")
	if g == nil {
		t.Fatal("BuildGoal returned nil")
	}
	if g.Mode != "explore" {
		t.Errorf("Mode = %q, want explore", g.Mode)
	}
	if g.Query != "how does auth work?" {
		t.Errorf("Query not preserved: %q", g.Query)
	}
	if !strings.Contains(g.Contract, "answer") || !strings.Contains(g.Contract, "citations") {
		t.Errorf("explore contract missing answer/citations: %q", g.Contract)
	}
	if !strings.Contains(g.Contract, "MUST NOT include implementation_instructions") {
		t.Error("explore contract should explicitly forbid fix-shape fields")
	}
	if g.TerminationCriterion == "" {
		t.Error("TerminationCriterion empty for explore mode")
	}
}

func TestBuildGoal_FixMode(t *testing.T) {
	g := BuildGoal("fix the null pointer in handler.go", "fix")
	if g.Mode != "fix" {
		t.Errorf("Mode = %q, want fix", g.Mode)
	}
	if !strings.Contains(g.Contract, "implementation_instructions") {
		t.Errorf("fix contract missing implementation_instructions: %q", g.Contract)
	}
	if !strings.Contains(g.Contract, "requires_fix") {
		t.Error("fix contract should mention requires_fix")
	}
}

func TestBuildGoal_UnknownMode_FallsBackToSpecialist(t *testing.T) {
	g := BuildGoal("q", "")
	if g.Mode != "" {
		t.Errorf("Mode = %q, want empty", g.Mode)
	}
	if !strings.Contains(g.Contract, "specialist") {
		t.Errorf("fallback contract should mention specialist context: %q", g.Contract)
	}
}

func TestGoal_ToPromptBlock_IncludesAllSections(t *testing.T) {
	g := BuildGoal("explain X", "explore")
	block := g.ToPromptBlock()
	for _, want := range []string{
		"## TASK GOAL",
		"Original query: explain X",
		"Termination criterion:",
		"EXPLORE MODE",
		"Plan your investigation around this goal",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("ToPromptBlock missing %q\nfull output:\n%s", want, block)
		}
	}
}

func TestGoal_ToPromptBlock_NilSafe(t *testing.T) {
	var g *Goal
	if got := g.ToPromptBlock(); got != "" {
		t.Errorf("nil Goal should render empty, got %q", got)
	}
}
