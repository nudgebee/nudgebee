package agents

import (
	"context"
	"strings"
	"testing"
)

const prSkillMarker = "NB-TEST-PR-SKILL: PRs must target the develop branch and include a Rollback section"

func skillsXMLBlock() string {
	return "<skills><skill name=\"create-pr\">" + prSkillMarker + "</skill></skills>"
}

func TestOperatorSkillsPromptBlock(t *testing.T) {
	// Empty / whitespace skills produce no block so prompts stay unchanged.
	for _, empty := range []string{"", "   ", "\n\t"} {
		if got := operatorSkillsPromptBlock(empty); got != "" {
			t.Errorf("operatorSkillsPromptBlock(%q) = %q, want empty", empty, got)
		}
	}

	block := operatorSkillsPromptBlock(skillsXMLBlock())
	if !strings.Contains(block, "<operator-skills>") {
		t.Errorf("expected wrapper tag, got: %s", block)
	}
	if !strings.Contains(block, prSkillMarker) {
		t.Errorf("expected skill body inlined, got: %s", block)
	}
}

func TestSanitizeBranchRef(t *testing.T) {
	cases := map[string]string{
		"feat/Add Cool Thing!":  "feat/add-cool-thing",
		"   Fix: NPE in foo   ": "fix-npe-in-foo",
		"!!!":                   "",
		"already-valid/slug":    "already-valid/slug",
	}
	for in, want := range cases {
		if got := sanitizeBranchRef(in); got != want {
			t.Errorf("sanitizeBranchRef(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGenerateBranchNameDeterministicWithoutSkills(t *testing.T) {
	// With no skills the LLM path is skipped entirely, so a nil llmClient is fine.
	a := &OrchestratorAgent{}
	name := a.generateBranchName(context.Background(), "Fix the broken thing", "")
	if !strings.HasPrefix(name, "fix/") {
		t.Errorf("expected deterministic 'fix/' prefix, got %q", name)
	}
	if !strings.Contains(name, "fix-the-broken-thing") {
		t.Errorf("expected slug from title, got %q", name)
	}
}

func TestBuildCommitHeredocAvoidsDelimiterCollision(t *testing.T) {
	// Normal message uses the default delimiter.
	normal := buildCommitHeredoc("fix: a thing\n\nbody line")
	if !strings.Contains(normal, "<<'EOF_COMMIT_MSG'\n") || !strings.HasSuffix(normal, "\nEOF_COMMIT_MSG") {
		t.Errorf("expected default delimiter for normal message, got:\n%s", normal)
	}

	// A message containing the delimiter as its own line must force a different
	// delimiter so the heredoc is not terminated early.
	colliding := "fix: thing\nEOF_COMMIT_MSG\nmore body"
	out := buildCommitHeredoc(colliding)
	if strings.Contains(out, "<<'EOF_COMMIT_MSG'\n") {
		t.Errorf("delimiter collided with message body; heredoc would truncate:\n%s", out)
	}
	if !strings.Contains(out, colliding) {
		t.Errorf("commit message body must be preserved verbatim, got:\n%s", out)
	}
}

func TestPatternDetectionPromptIncludesSkills(t *testing.T) {
	a := &OrchestratorAgent{}

	withSkills := a.buildPatternDetectionPrompt(nil, nil, "fix thing", "desc", skillsXMLBlock())
	if !strings.Contains(withSkills, "<operator-skills>") || !strings.Contains(withSkills, prSkillMarker) {
		t.Errorf("pattern-detection prompt should embed operator skills when present")
	}

	without := a.buildPatternDetectionPrompt(nil, nil, "fix thing", "desc", "")
	if strings.Contains(without, "<operator-skills>") {
		t.Errorf("pattern-detection prompt must not emit an empty skills block")
	}
}
