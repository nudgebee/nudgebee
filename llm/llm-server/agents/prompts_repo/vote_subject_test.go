package prompts_repo

import (
	"strings"
	"testing"
)

// Prompts are selected by a switch on their constant, so an unwired constant
// resolves to the empty string rather than failing. For this one that would
// mean asking the model to reduce an RCA to a line with no instructions at
// all — the caller would then store whatever came back as a decision subject.
func TestVoteSubjectPromptResolves(t *testing.T) {
	p := GetPrompt(PromptVoteSubject)
	if strings.TrimSpace(p) == "" {
		t.Fatal("PromptVoteSubject resolved to empty — is the switch case wired up?")
	}
	if !strings.Contains(p, "NONE") {
		t.Error("the NONE escape hatch must survive edits — the caller reads it as 'record nothing'")
	}
}
