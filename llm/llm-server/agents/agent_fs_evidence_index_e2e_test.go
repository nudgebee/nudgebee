//go:build e2e

package agents

import (
	"os"
	"testing"

	"nudgebee/llm/agents/core"

	"github.com/stretchr/testify/assert"
)

// TestEvidenceIndex_LocalDB validates use case #2 (cross-message evidence
// recall) end-to-end against the real DB: it pulls the workspace files a prior
// message saved for a conversation via the same DAO query the prompt injection
// uses. Point it at a conversation that has saved files:
//
//	TEST_ACCOUNT=<acct> FSEV_TEST_CONV=<conversation_uuid> \
//	  go test -tags e2e ./agents/ -run TestEvidenceIndex_LocalDB -v
//
// Skips when the env is not set.
func TestEvidenceIndex_LocalDB(t *testing.T) {
	accountId := os.Getenv("TEST_ACCOUNT")
	conv := os.Getenv("FSEV_TEST_CONV")
	if accountId == "" || conv == "" {
		t.Skip("set TEST_ACCOUNT and FSEV_TEST_CONV (a conversation UUID with saved files) to run")
	}

	refs, err := core.GetConversationDao().ListConversationFileRefs(accountId, conv, 20)
	assert.NoError(t, err)

	t.Logf("conversation %s has %d workspace file(s):", conv, len(refs))
	for _, r := range refs {
		t.Logf("  - %s (%s): %s", r.File, r.ToolName, r.Description)
	}
	assert.NotEmpty(t, refs, "expected at least one saved file — this is the evidence a new message would recall")
}
