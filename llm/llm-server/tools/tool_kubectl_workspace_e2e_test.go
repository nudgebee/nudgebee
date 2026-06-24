//go:build e2e

package tools

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// uniqueSuffix yields a per-test random tag so concurrent test runs and
// previous-run leftovers can't collide on conversation IDs.
func uniqueSuffix() string {
	return fmt.Sprintf("%d_%d", time.Now().UnixNano(), rand.Int63())
}

// These tests drive KubectlExecuteTool.Call directly against the live
// workspace pod for TEST_ACCOUNT, to root-cause why production
// kubectl_execute errors surface as the opaque
// `workspace command failed: status="failed" error="exit status 1"`
// envelope instead of carrying the actual kubectl stderr.
//
// Run locally:
//
//	TEST_TENANT=... TEST_ACCOUNT=... TEST_USER=... \
//	go test -tags=e2e -v -run 'TestKubectlExecute_' ./tools/

func newKubectlTestEnv(t *testing.T) func(conversationId, command string) (toolcore.NBToolResponse, error) {
	t.Helper()
	tenant := os.Getenv("TEST_TENANT")
	account := os.Getenv("TEST_ACCOUNT")
	user := os.Getenv("TEST_USER")
	if tenant == "" || account == "" || user == "" {
		t.Skip("TEST_TENANT / TEST_ACCOUNT / TEST_USER not set; kubectl workspace e2e tests skipped")
	}

	originalWorkspaceEnabled := config.Config.LlmServerWorkspaceEnabled
	config.Config.LlmServerWorkspaceEnabled = true
	t.Cleanup(func() {
		config.Config.LlmServerWorkspaceEnabled = originalWorkspaceEnabled
	})

	tool := KubectlExecuteTool{}
	return func(conversationId, command string) (toolcore.NBToolResponse, error) {
		sc := security.NewRequestContextForTenantAccountAdmin(tenant, user, []string{account})
		nctx := toolcore.NbToolContext{
			Ctx:            sc,
			AccountId:      account,
			UserId:         user,
			ConversationId: conversationId,
			// Non-empty Name bypasses the "no tool configs found"
			// guard at tool_kubectl.go:440 — we want to exercise the
			// workspace-mode code path that follows, even though the
			// docker-desktop workspace pod has no actual cluster config.
			ToolConfig: toolcore.ToolConfig{
				Name:   "e2e-test-config",
				Values: []toolcore.ToolConfigValue{},
			},
		}
		return tool.Call(nctx, toolcore.NBToolCallRequest{Command: command})
	}
}

// dumpResponse prints the response shape in a way that's easy to scan
// when running with -v — exposes both the Data and any Metadata so we
// can see which fields the LLM actually receives.
func dumpResponse(t *testing.T, label string, resp toolcore.NBToolResponse, err error) {
	t.Helper()
	t.Logf("─── %s ─────────────────────────────────────────────────", label)
	t.Logf("  Status:           %s", resp.Status)
	t.Logf("  Data (first 500): %.500s", resp.Data)
	if resp.Metadata != nil {
		t.Logf("  Metadata.Stderr:  %q", resp.Metadata.Stderr)
		t.Logf("  Metadata.ExitStatus: %d", resp.Metadata.ExitStatus)
	} else {
		t.Logf("  Metadata:         (nil)")
	}
	if err != nil {
		t.Logf("  Go error:         %v", err)
	}
}

// ──────────────────────────────────────────────────────────────────────
// Scenario 1 — kubectl with non-zero exit + real stderr message
// (the "should-have-actionable-message" case)
// ──────────────────────────────────────────────────────────────────────
//
// Production sample: bare `workspace command failed: status="failed"
// error="exit status 1"` with NO actual kubectl stderr surfaced.
//
// Expected: the LLM should see kubectl's actual stderr ("error: the
// server doesn't have a resource type 'nonexistentresource'") so it can
// fix the command.
//
// What the test reveals: whether the workspace pod's execution_handler
// preserved the real stderr in result.Response, and whether the
// kubectl tool surfaced it to NBToolResponse.Data.
func TestKubectlExecute_NonZeroExitPreservesStderr_E2E(t *testing.T) {
	run := newKubectlTestEnv(t)
	resp, err := run("e2e-kubectl-bad-resource-"+uniqueSuffix(),
		"kubectl get nonexistentresource -n kube-system")

	dumpResponse(t, "kubectl get nonexistentresource", resp, err)

	// We expect failure — but the error should carry kubectl's actual
	// message, NOT the opaque workspace-exit-status envelope.
	assert.Equal(t, toolcore.NBToolResponseStatusError, resp.Status,
		"this kubectl command must fail; resp=%+v", resp)
	assert.NotContains(t, resp.Data,
		`workspace command failed: status="failed" error="exit status 1"`,
		"Data must not be the opaque envelope; the actual kubectl stderr should be surfaced. Got: %s", resp.Data)
	// Real kubectl stderr for this command typically contains one of these.
	hasRealStderr := strings.Contains(resp.Data, "the server doesn't have a resource type") ||
		strings.Contains(resp.Data, "doesn't have a resource") ||
		strings.Contains(resp.Data, "no matches for kind") ||
		strings.Contains(resp.Data, "error: ")
	assert.True(t, hasRealStderr,
		"Data must carry kubectl's actual stderr (server-side error message) so the LLM can fix the command. Got: %s", resp.Data)
}

// ──────────────────────────────────────────────────────────────────────
// Scenario 2 — kubectl SUCCESS with informational stderr noise
// ("Defaulted container" warning)
// ──────────────────────────────────────────────────────────────────────
//
// Production sample: `Logs for pod/X:\nDefaulted container "Y" out of:
// ...` showing up under Status=Error.
//
// Expected: kubectl logs against a multi-container pod succeeds.
// The "Defaulted container" warning is informational stderr — should
// land in Metadata.Stderr (PR #32006), with Status=Success and the
// actual log lines in Data.
//
// What the test reveals: whether the splitter is actually catching
// this pattern AND whether the response is correctly classified as
// Success.
func TestKubectlExecute_DefaultedContainerWarningSurfacesAsSuccess_E2E(t *testing.T) {
	run := newKubectlTestEnv(t)
	// kubectl logs against the workspace pod itself — it has multiple
	// containers (the shim sidecar pattern); informational stderr is
	// guaranteed.
	account := os.Getenv("TEST_ACCOUNT")
	podName := fmt.Sprintf("workspace-%s", strings.ToLower(account))
	resp, err := run("e2e-kubectl-defaulted-container-"+uniqueSuffix(),
		fmt.Sprintf("kubectl logs %s -n nudgebee --tail=5", podName))

	dumpResponse(t, "kubectl logs (multi-container pod)", resp, err)

	if resp.Status == toolcore.NBToolResponseStatusError {
		t.Fatalf("kubectl logs of a real multi-container pod must succeed; got Error with Data=%s", resp.Data)
	}
	// The "Defaulted container" warning belongs in Metadata.Stderr,
	// not in Data.
	if strings.Contains(resp.Data, "Defaulted container") {
		t.Errorf("'Defaulted container' warning leaked into Data — splitKubectlStderrNoise didn't catch it. Data: %s", resp.Data)
	}
}

// ──────────────────────────────────────────────────────────────────────
// Scenario 3 — kubectl with empty result ("No resources found")
// ──────────────────────────────────────────────────────────────────────
//
// Production sample: `No resources found in app-108 namespace.\n` as
// Status=Error.
//
// Expected: this is kubectl's normal "ran successfully, nothing
// matched" output (exit 0). Should be Status=Success. If it's coming
// back as Error today, that's the same class of misclassification as
// my grep-no-match work, and worth a no_matches-style envelope.
//
// What the test reveals: whether kubectl's empty-result is currently
// being misclassified as failure.
func TestKubectlExecute_EmptyResultIsSuccess_E2E(t *testing.T) {
	run := newKubectlTestEnv(t)
	// A namespace that almost certainly exists but has no Jobs.
	resp, err := run("e2e-kubectl-empty-result-"+uniqueSuffix(),
		"kubectl get jobs -n kube-system")

	dumpResponse(t, "kubectl get jobs -n kube-system (likely empty)", resp, err)

	// Empty result must be Status=Success — exit 0 from kubectl.
	if resp.Status == toolcore.NBToolResponseStatusError {
		t.Errorf("kubectl with no matching resources must NOT be Status=Error. Got Data=%s, err=%v",
			resp.Data, err)
	}
}

// ──────────────────────────────────────────────────────────────────────
// Scenario 4 — diagnostic harness: what does Data look like on a
// confirmed-failure command? (the explicit "what does the LLM see"
// observation)
// ──────────────────────────────────────────────────────────────────────
func TestKubectlExecute_DiagnosticDumpOnObviousFailure_E2E(t *testing.T) {
	run := newKubectlTestEnv(t)
	// Command shape that should fail server-side with a clear stderr.
	resp, err := run("e2e-kubectl-bad-flag-"+uniqueSuffix(),
		"kubectl get pods --bogus-flag")

	dumpResponse(t, "kubectl get pods --bogus-flag", resp, err)

	// We don't assert behavior here — this is purely diagnostic so the
	// log output shows what the LLM actually sees. The other tests
	// assert; this one informs.
	t.Logf("(diagnostic only — no assertions)")
}

// ──────────────────────────────────────────────────────────────────────
// Scenario 5 — pipeline-grep no-match wiring (the actual production
// case + the fix in this PR)
// ──────────────────────────────────────────────────────────────────────
//
// The docker-desktop workspace pod has no real kubeconfig, so the
// `kubectl` binary will exit non-zero. But the LOCAL shell pipeline
// semantics ARE the same as production: anything piped through `grep`
// at the tail with no match exits 1. We use `echo` (always available)
// piped to `grep` to deterministically reproduce the pipeline-tail
// no-match exit, bypassing the cluster connectivity dependency.
//
// This is the EXACT response shape the LLM sees post-fix:
//
//	pre-fix:  Status=Error, Data='workspace command failed: status="failed" error="exit status 1"'
//	post-fix: Status=Success, Data='{"stdout":"<actual output>","no_matches":true}'
//
// Asserts the new wiring fires + dumps the full response shape so we
// can see exactly what changed.
func TestKubectlExecute_PipelineGrepNoMatchIsReclassifiedAsSuccess_E2E(t *testing.T) {
	run := newKubectlTestEnv(t)

	// A pipeline that:
	//   1. Produces some output (echo "alpha\nbeta\ngamma")
	//   2. Pipes through grep for a pattern that does NOT match
	// In a real shell, this exits 1 (grep's exit code).
	// Pre-fix: surfaces as Error.
	// Post-fix: surfaces as Success + {"no_matches":true}.
	// Real kubectl command that the LLM might generate. kubectl runs
	// against the cluster the workspace pod is configured for (the
	// pod's shim posts back to llm-server which dispatches the actual
	// kubectl). kubectl's real stdout (pod list) is piped to grep.
	// grep finds no match → exits 1 → pipeline exits 1.
	cmd := `kubectl get pods --all-namespaces | grep zzzzz_does_not_match`
	resp, err := run("e2e-kubectl-pipegrep-nomatch-"+uniqueSuffix(), cmd)

	dumpResponse(t, "echo ... | grep no-match (proxy for kubectl ... | grep no-match)", resp, err)

	// Hard assertion: this MUST be Status=Success, not Error.
	if resp.Status != toolcore.NBToolResponseStatusSuccess {
		t.Fatalf("expected pipeline-grep no-match to be Success (the fix in this PR), got Status=%q with Data=%q and err=%v",
			resp.Status, resp.Data, err)
	}
	// Hard assertion: Data must carry the no_matches semantic so the
	// LLM doesn't retry.
	if !strings.Contains(resp.Data, `"no_matches":true`) {
		t.Fatalf("expected Data to carry no_matches:true envelope, got %q", resp.Data)
	}
	t.Logf("✓ Post-fix shape confirmed: Status=Success, Data contains no_matches:true")
}

// ──────────────────────────────────────────────────────────────────────
// Scenario 6 — pipeline tail that is NOT a no-match command should
// still surface as Error (regression guard)
// ──────────────────────────────────────────────────────────────────────
func TestKubectlExecute_PipelineNonNoMatchStillError_E2E(t *testing.T) {
	run := newKubectlTestEnv(t)

	// false at the tail exits 1, but `false` is not a no-match command,
	// so this should still surface as Error (not reclassified).
	cmd := `kubectl version --client | false`
	resp, err := run("e2e-kubectl-pipe-false-"+uniqueSuffix(), cmd)

	dumpResponse(t, "echo hello | false (non-no-match exit 1)", resp, err)

	// Must NOT be reclassified — `false` isn't in noMatchExitCommands.
	if resp.Status == toolcore.NBToolResponseStatusSuccess && strings.Contains(resp.Data, `"no_matches":true`) {
		t.Fatalf("expected pipeline ending in `false` (not a no-match command) to remain Error, got Success+no_matches: %q", resp.Data)
	}
	t.Logf("✓ Regression guard: non-grep-family pipeline tail stays Error")
}
