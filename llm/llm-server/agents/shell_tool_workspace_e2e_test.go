//go:build e2e

package agents

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the runtime claims that PR #32007 added to the prompt
// layer (consolidated into ShellTool.Description() — see
// tools/tool_shell.go + tools/tool_shell_test.go):
//
//   - grep/find/jq exit 1 is reported as success-with-no-matches, not
//     as a tool failure (Change 1).
//   - Files at relative paths persist across turns within the same
//     conversation (the cwd = per-conversation directory claim).
//   - Files at absolute /tmp/... paths leak across conversations on the
//     same account (the cross-conversation warning).
//   - shell_execute wraps a small set of well-known stderr patterns
//     with an actionable error_hint (Change 6).
//
// All five tests drive ShellTool.Call directly rather than going through
// the full LLM-driven agent flow, so they are deterministic and check
// only the behavior under test. They require a live workspace pod for
// the configured TEST_ACCOUNT; without it they skip.
//
// Run locally:
//
//	TEST_TENANT=... TEST_ACCOUNT=... TEST_USER=... \
//	go test -tags=e2e -run 'TestShellExecute_' ./agents/
func newShellTestEnv(t *testing.T) (toolcore.NBTool, func(conversationId, query string) (toolcore.NBToolResponse, error)) {
	t.Helper()
	tenant := os.Getenv("TEST_TENANT")
	account := os.Getenv("TEST_ACCOUNT")
	user := os.Getenv("TEST_USER")
	if tenant == "" || account == "" || user == "" {
		t.Skip("TEST_TENANT / TEST_ACCOUNT / TEST_USER not set; shell workspace e2e tests skipped")
	}

	originalShellEnabled := config.Config.LlmServerShellToolEnabled
	originalWorkspaceEnabled := config.Config.LlmServerWorkspaceEnabled
	config.Config.LlmServerShellToolEnabled = true
	config.Config.LlmServerWorkspaceEnabled = true
	t.Cleanup(func() {
		config.Config.LlmServerShellToolEnabled = originalShellEnabled
		config.Config.LlmServerWorkspaceEnabled = originalWorkspaceEnabled
	})

	shell := tools.ShellTool{AccountId: account}
	runner := func(conversationId, command string) (toolcore.NBToolResponse, error) {
		sc := security.NewRequestContextForTenantAccountAdmin(tenant, user, []string{account})
		nctx := toolcore.NbToolContext{
			Ctx:            sc,
			AccountId:      account,
			UserId:         user,
			ConversationId: conversationId,
		}
		return shell.Call(nctx, toolcore.NBToolCallRequest{Command: command})
	}
	return shell, runner
}

// uniqueSuffix yields a per-test random tag so concurrent test runs and
// previous-run leftovers can't collide on /tmp/leak-marker filenames or
// relative scratch filenames.
func uniqueSuffix() string {
	return fmt.Sprintf("%d_%d", time.Now().UnixNano(), rand.Int63())
}

// ============================================================================
// Test 1 — grep/find/jq exit 1 is success-with-no-matches, not a tool failure
// ============================================================================

func TestShellExecute_GrepNoMatchesIsSuccess_E2E(t *testing.T) {
	_, run := newShellTestEnv(t)

	convId := "e2e-grep-nomatch-" + uniqueSuffix()
	scratch := "scratch_" + uniqueSuffix() + ".txt"

	// Seed a file we can grep against — keeps the test self-contained and
	// proves the seed write succeeded before the grep runs.
	seedResp, err := run(convId, fmt.Sprintf("printf 'apple\\nbanana\\ncherry\\n' > %s && wc -l %s", scratch, scratch))
	require.NoError(t, err, "seed write must succeed")
	require.Equal(t, toolcore.NBToolResponseStatusSuccess, seedResp.Status, "seed write must report Success; got %q with body %q", seedResp.Status, seedResp.Data)
	t.Cleanup(func() { _, _ = run(convId, "rm -f "+scratch) })

	// Grep for a pattern that is definitely not present. Pre-fix, the
	// workspace's "exit status 1" came back as Status=Error with the
	// LLM-confusing "workspace command failed: ..." body. Post-fix, it
	// must come back as Success carrying {"no_matches": true}.
	resp, err := run(convId, fmt.Sprintf("grep zzznosuchpattern %s", scratch))
	require.NoError(t, err, "shell tool itself must not error — exit 1 from grep is a success-with-no-matches now")
	assert.Equal(t, toolcore.NBToolResponseStatusSuccess, resp.Status,
		"grep exit 1 on a no-match query must report Success (was Error pre-PR); body=%q", resp.Data)
	assert.Contains(t, resp.Data, `"no_matches":true`,
		"the response body must carry no_matches:true so the LLM (and the prompt's matching guidance) sees an unambiguous signal; body=%q", resp.Data)
}

// ============================================================================
// Test 2 — relative-path files persist across turns within the same conversation
// ============================================================================
// Proves the cwd=per-conversation claim that ShellTool.Description() teaches.

func TestShellExecute_RelativePathPersistsAcrossTurns_E2E(t *testing.T) {
	_, run := newShellTestEnv(t)

	convId := "e2e-relpath-persist-" + uniqueSuffix()
	file := "persist_" + uniqueSuffix() + ".txt"
	marker := "marker_" + uniqueSuffix()

	writeResp, err := run(convId, fmt.Sprintf("echo %q > %s", marker, file))
	require.NoError(t, err)
	require.Equal(t, toolcore.NBToolResponseStatusSuccess, writeResp.Status, "write must succeed; body=%q", writeResp.Data)
	t.Cleanup(func() { _, _ = run(convId, "rm -f "+file) })

	readResp, err := run(convId, "cat "+file)
	require.NoError(t, err)
	assert.Equal(t, toolcore.NBToolResponseStatusSuccess, readResp.Status, "read must succeed; body=%q", readResp.Data)
	assert.Contains(t, readResp.Data, marker,
		"relative-path file written in one shell_execute call must be readable from the next call in the same conversation; body=%q", readResp.Data)
}

// ============================================================================
// Test 3 — absolute /tmp/... leaks across conversations on the same account
// ============================================================================
// Pins the cross-conversation warning in ShellTool.Description() to
// concrete behavior: if this test ever STARTS failing because /tmp/ no
// longer leaks (e.g. someone adds bwrap isolation), we know to rewrite
// the prompt rather than silently mislead the LLM.

func TestShellExecute_AbsoluteTmpLeaksAcrossConversations_E2E(t *testing.T) {
	_, run := newShellTestEnv(t)

	convA := "e2e-leak-a-" + uniqueSuffix()
	convB := "e2e-leak-b-" + uniqueSuffix()
	leakPath := "/tmp/conv_leak_" + uniqueSuffix() + ".txt"
	marker := "leak_marker_" + uniqueSuffix()

	writeResp, err := run(convA, fmt.Sprintf("echo %q > %s", marker, leakPath))
	require.NoError(t, err)
	require.Equal(t, toolcore.NBToolResponseStatusSuccess, writeResp.Status, "write to /tmp/ in conv A must succeed; body=%q", writeResp.Data)
	// Cleanup from BOTH conversations to be safe — whichever one runs second
	// is a no-op.
	t.Cleanup(func() {
		_, _ = run(convA, "rm -f "+leakPath)
		_, _ = run(convB, "rm -f "+leakPath)
	})

	readResp, err := run(convB, "cat "+leakPath)
	require.NoError(t, err, "cat of a /tmp/ file written by another conversation must not surface as a tool error")
	assert.Equal(t, toolcore.NBToolResponseStatusSuccess, readResp.Status,
		"conv B must be able to read /tmp/ files written by conv A on the same account; body=%q", readResp.Data)
	assert.Contains(t, readResp.Data, marker,
		"the marker written by conv A must be visible to conv B — this is exactly the leak the prompt warns the LLM about; body=%q", readResp.Data)
}

// ============================================================================
// Test 4 — relative-path files are scoped to their conversation
// ============================================================================
// Companion to Test 3: proves the OTHER half of the persistence claim.
// Relative paths land in the per-conversation cwd, so a sibling
// conversation must not see them.

func TestShellExecute_RelativePathScopedToConversation_E2E(t *testing.T) {
	_, run := newShellTestEnv(t)

	convA := "e2e-scope-a-" + uniqueSuffix()
	convB := "e2e-scope-b-" + uniqueSuffix()
	file := "scoped_" + uniqueSuffix() + ".txt"

	writeResp, err := run(convA, fmt.Sprintf("echo hello > %s", file))
	require.NoError(t, err)
	require.Equal(t, toolcore.NBToolResponseStatusSuccess, writeResp.Status, "write to relative path in conv A must succeed; body=%q", writeResp.Data)
	t.Cleanup(func() { _, _ = run(convA, "rm -f "+file) })

	readResp, err := run(convB, "cat "+file)
	// Either an error response is fine — the point is that conv B does NOT
	// see the file from conv A. Distinguishing "Error with no-such-file"
	// from "Success with empty stdout" guards against a regression where
	// the workspace silently falls through to a shared directory.
	assert.NotEqual(t, toolcore.NBToolResponseStatusSuccess, readResp.Status,
		"conv B must NOT be able to read conv A's relative-path file; got Success with body=%q (err=%v)", readResp.Data, err)
}

// ============================================================================
// Test 5 — unbalanced-quote stderr is wrapped with an actionable error_hint
// ============================================================================
// Pins Change 6: the shellErrorHint wrapping for the three known
// failure shapes. We use unbalanced quotes here because it's the only
// shape the LLM can't avoid by reading the prompt — quote-escaping bugs
// are emergent.

func TestShellExecute_ErrorHintOnUnbalancedQuotes_E2E(t *testing.T) {
	_, run := newShellTestEnv(t)

	convId := "e2e-quote-hint-" + uniqueSuffix()

	// `printf "unterminated` — opens a double quote and never closes it.
	// sh reports a syntax error verbatim; pre-PR that came back as raw
	// stderr; post-PR it's wrapped with an error_hint pointing at the
	// nested-escaping pattern.
	resp, err := run(convId, `printf "unterminated`)
	require.Error(t, err, "unbalanced quote must surface as an error from the workspace; got Success with body=%q", resp.Data)
	assert.Equal(t, toolcore.NBToolResponseStatusError, resp.Status, "status must remain Error — Change 6 only changes the body shape; body=%q", resp.Data)

	lowered := strings.ToLower(resp.Data)
	hasHint := strings.Contains(resp.Data, `"error_hint"`) &&
		strings.Contains(resp.Data, `"original_error"`) &&
		strings.Contains(lowered, "unbalanced quotes")
	assert.True(t, hasHint,
		"unbalanced-quote error must be wrapped with the structured error_hint + original_error envelope so the LLM can learn from it; body=%q", resp.Data)
}
