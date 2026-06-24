package prompts_repo

import (
	"embed"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

//go:embed agent_*.txt
var agentPromptFiles embed.FS

// TestAgentPrompts_NoLegacyWorkspacePersistenceClaim guards every
// agents/prompts_repo/agent_*.txt against the pre-#32007 phrasing that
// promised files would persist across `shell_execute` steps "for the
// same account". The shell actually runs in a per-conversation working
// directory; only files under absolute /tmp/... are shared at the
// account level. Two prompt files (agent_k8s_debug.txt for the legacy
// ReWOO path, agent_k8s_debug_react.txt for the active ReAct3 path)
// originally carried the wrong claim and were both corrected.
//
// This test ensures a future agent prompt cannot reintroduce the same
// misleading framing by copy-paste.
func TestAgentPrompts_NoLegacyWorkspacePersistenceClaim(t *testing.T) {
	// Phrases that imply account-level persistence of relative-path
	// files — that contract is wrong. The corrected wording uses
	// "within this conversation" or "per-conversation working
	// directory" and explicitly carves out /tmp/ as the account-shared
	// path.
	forbidden := []string{
		"files persist across `shell_execute` steps for the same account",
		"files persist across shell_execute steps for the same account",
		"available in subsequent steps for the same account",
	}

	entries, err := agentPromptFiles.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded agent_*.txt: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no agent_*.txt files were embedded — the //go:embed glob did not match anything")
	}

	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "agent_") || !strings.HasSuffix(name, ".txt") {
			continue
		}
		body, err := agentPromptFiles.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(body)
		for _, phrase := range forbidden {
			assert.NotContains(t, text, phrase,
				"%s carries the legacy workspace-persistence claim %q — the shell runs in a per-conversation directory; only absolute /tmp/... paths are account-shared. Use the corrected wording from agent_k8s_debug.txt (relative paths persist within this conversation; /tmp/ is shared across conversations).",
				name, phrase)
		}
	}
}
