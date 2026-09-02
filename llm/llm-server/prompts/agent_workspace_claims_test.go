package prompts

import (
	"strings"
	"testing"
)

// TestAgentPrompts_NoLegacyWorkspacePersistenceClaim guards every agent prompt
// against the pre-#32007 phrasing that promised files would persist across
// `shell_execute` steps "for the same account". The shell actually runs in a
// per-conversation working directory; only files under absolute /tmp/... are
// shared at the account level.
//
// Moved here from prompts_repo with the prompts themselves; it now walks the
// YAML tree instead of a //go:embed glob over agent_*.txt.
func TestAgentPrompts_NoLegacyWorkspacePersistenceClaim(t *testing.T) {
	legacyClaims := []string{
		"files persist across `shell_execute` steps for the same account",
		"files persist across shell_execute steps for the same account",
		"available in subsequent steps for the same account",
	}

	checked := 0
	walkPromptFiles(t, func(promptPath string, promptFile *PromptFile) {
		if promptFile.Category != CategoryAgents {
			return
		}
		checked++
		for _, claim := range legacyClaims {
			if strings.Contains(promptFile.Body, claim) {
				t.Errorf("%s carries the legacy workspace-persistence claim %q — the shell runs "+
					"in a per-conversation directory; only absolute /tmp/... paths are account-shared.",
					promptPath, claim)
			}
		}
	})

	if checked == 0 {
		t.Fatal("no agent prompts were checked — the walk matched nothing")
	}
}
