//go:build e2e

package agents

import (
	"context"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/watch"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// e2e for the watch feature: drive a k8s_debug query that warrants a
// background watch ("scale + notify when ready"), assert the planner
// actually registers a watch row via watch_resource, and then poll the
// watch row until it terminates. Follows the same shape as
// agent_github_e2e_test.go (live LLM + DB, gated by build tag e2e and
// TEST_ACCOUNT/TEST_USER envs).
func TestK8sDebugAgent_WatchResource_RegistersAndTerminates(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" {
		t.Skip("watch e2e requires TEST_ACCOUNT and TEST_USER")
	}

	sc := security.NewRequestContextForSuperAdmin()

	tc := struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		SessionId: "ut-watch-k8s-1",
		AccountId: os.Getenv("TEST_ACCOUNT"),
		UserId:    os.Getenv("TEST_USER"),
		// A write that takes seconds to reconcile — the agent should
		// pair the scale with a watch_resource registration in the same
		// turn (per shared_async_completion_rules + k8s_debug react
		// prompt's Write + Watch Pattern).
		Query: "scale deployment slow-rollout-app in namespace otel-demo to 3 replicas and let me know when it's ready",
	}

	k8sAgent := newK8sOrchestratorAgent(tc.AccountId)
	require.NoError(t, core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId))

	resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, resp.AgentName, k8sAgent.GetName())
	assert.Greater(t, len(resp.Response), 0)

	// The agent should have produced a watch row keyed on this conversation.
	convUUID, err := uuid.Parse(resp.ConversationId)
	require.NoError(t, err, "agent response must carry a real conversation uuid")
	tenantUUID, err := uuid.Parse(sc.GetSecurityContext().GetTenantId())
	require.NoError(t, err)

	mgr := watch.NewManager()
	watches, err := mgr.ListByConversation(context.Background(), tenantUUID, convUUID)
	require.NoError(t, err)
	require.NotEmpty(t, watches,
		"agent must register at least one watch via watch_resource on a write+watch turn — see shared_async_completion_rules.txt")
	w := watches[0]
	assert.Equal(t, "tool", string(w.SourceKind))
	assert.NotEmpty(t, w.PredicateExpr)

	// Wait for the dispatcher to drive the watch to a terminal status.
	// max_duration_sec caps the loop on the watch side; we cap the test
	// at 5 minutes so a stuck watch fails fast instead of hanging CI.
	awaitTerminal(t, mgr, tenantUUID, w.ID, 5*time.Minute)

	// A terminal status is NOT delivery, and stopping the assertion there is
	// what let a real bug through: terminate() commits the status first, then
	// spends up to WatchSummarizerTimeoutSec on the summarizer, and only then
	// does the responder append the block to the parent message. Assert the
	// block actually arrives, and log how long after the status flip it did —
	// that gap is what the chat UI has to poll across
	// (app/src/components/llm/utils/watchFollowup.js), so the number here is
	// the empirical basis for that client-side budget.
	delay := awaitWatchFollowupBlock(t, convUUID, w.ID, 2*time.Minute)
	t.Logf("watch %s: follow-up block landed %s after the terminal status was first observed", w.ID, delay.Round(time.Millisecond))
}

// awaitTerminal blocks until the watch row reaches a terminal status, failing
// the test if it does not within timeout.
func awaitTerminal(t *testing.T, mgr *watch.Manager, tenantID, watchID uuid.UUID, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		got, err := mgr.Get(context.Background(), tenantID, watchID)
		require.NoError(t, err)
		if got.Status.IsTerminal() {
			t.Logf("watch %s reached %s after %d poll(s)", got.ID, got.Status, got.PollCount)
			return
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("watch %s did not reach a terminal status within %s", watchID, timeout)
}

// awaitWatchFollowupBlock polls the conversation's messages until the
// responder's watch-update block for watchID appears in one of them, returning
// how long that took. Keyed on watch.UpdateMarker — the same token the
// responder writes for idempotency and the chat UI polls for.
func awaitWatchFollowupBlock(t *testing.T, conversationID, watchID uuid.UUID, timeout time.Duration) time.Duration {
	t.Helper()
	dbms, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err, "metastore must be reachable to verify watch follow-up delivery")

	marker := watch.UpdateMarker(watchID)
	start := time.Now()
	deadline := start.Add(timeout)
	for time.Now().Before(deadline) {
		var found int
		require.NoError(t, dbms.Db.QueryRow(`
			SELECT count(*) FROM llm_conversation_messages
			WHERE conversation_id = $1 AND position($2 IN COALESCE(response, '')) > 0`,
			conversationID, marker).Scan(&found))
		if found > 0 {
			return time.Since(start)
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("watch %s went terminal but its follow-up block never reached conversation %s within %s — "+
		"the user would see a completed watch with no answer in the thread", watchID, conversationID, timeout)
	return 0
}

// TestK8sDebugAgent_NoWatch_OnInvestigationOnly is the negative companion:
// pure-read queries must NOT register a watch (see shared_async_completion_rules
// "Skip ONLY for pure read / investigation"). Without this, planner drift
// toward over-registering watches goes uncaught until users complain.
func TestK8sDebugAgent_NoWatch_OnInvestigationOnly(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" {
		t.Skip("watch e2e requires TEST_ACCOUNT and TEST_USER")
	}

	sc := security.NewRequestContextForSuperAdmin()

	tc := struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		SessionId: "ut-watch-k8s-noop-1",
		AccountId: os.Getenv("TEST_ACCOUNT"),
		UserId:    os.Getenv("TEST_USER"),
		Query:     "list the deployments in namespace otel-demo",
	}

	k8sAgent := newK8sOrchestratorAgent(tc.AccountId)
	require.NoError(t, core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId))

	resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
	require.NoError(t, err)
	require.NotNil(t, resp)

	convUUID, err := uuid.Parse(resp.ConversationId)
	require.NoError(t, err)
	tenantUUID, err := uuid.Parse(sc.GetSecurityContext().GetTenantId())
	require.NoError(t, err)

	watches, err := watch.NewManager().ListByConversation(context.Background(), tenantUUID, convUUID)
	require.NoError(t, err)
	assert.Empty(t, watches,
		"pure investigation queries must not register watches — guard against planner over-registration")
}

// TestGithubAgent_WatchWorkflowRun_DeliversFollowup is the journey users
// actually describe: "re-run this workflow run and tell me when it's done".
// It exercises the same write+watch contract as the k8s case but through the
// github agent, over a resource whose completion takes minutes — the shape
// most likely to expose a delivery gap, because the summarize-then-append tail
// runs long after the user's turn returned.
//
// Env-gated on a specific run id per agents/TESTING.md: the run has to be
// re-runnable by the account's GitHub integration, so it cannot be discovered
// generically the way FetchRecentEventID does.
//
//	TEST_GITHUB_REPO=owner/name TEST_GITHUB_WORKFLOW_RUN_ID=32212744327 \
//	  make test-e2e-one TEST=TestGithubAgent_WatchWorkflowRun_DeliversFollowup PKG=./agents/
func TestGithubAgent_WatchWorkflowRun_DeliversFollowup(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" {
		t.Skip("watch e2e requires TEST_ACCOUNT and TEST_USER")
	}
	repo := os.Getenv("TEST_GITHUB_REPO")
	runID := os.Getenv("TEST_GITHUB_WORKFLOW_RUN_ID")
	if repo == "" || runID == "" {
		t.Skip("requires TEST_GITHUB_REPO and TEST_GITHUB_WORKFLOW_RUN_ID (a re-runnable workflow run)")
	}

	sc := security.NewRequestContextForSuperAdmin()
	accountID := os.Getenv("TEST_ACCOUNT")
	userID := os.Getenv("TEST_USER")
	sessionID := "ut-watch-github-rerun-1"
	query := "re-run github actions workflow run " + runID + " in " + repo + " and tell me when it has completed"

	githubChain := newGithubAgent(accountID)
	require.NoError(t, core.DeleteConversationBySession(sessionID, accountID, userID))

	resp, err := core.HandleConversationSessionRequest(sc, githubChain, userID, accountID, sessionID, query)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Greater(t, len(resp.Response), 0)

	convUUID, err := uuid.Parse(resp.ConversationId)
	require.NoError(t, err, "agent response must carry a real conversation uuid")
	tenantUUID, err := uuid.Parse(sc.GetSecurityContext().GetTenantId())
	require.NoError(t, err)

	mgr := watch.NewManager()
	watches, err := mgr.ListByConversation(context.Background(), tenantUUID, convUUID)
	require.NoError(t, err)
	require.NotEmpty(t, watches,
		"re-running a workflow is a write whose result arrives later — the agent must pair it with watch_resource")
	w := watches[0]
	assert.Equal(t, "tool", string(w.SourceKind))

	// A workflow run can take a while; the watch's own max_duration_sec is the
	// real ceiling, this is just a fail-fast bound for CI.
	awaitTerminal(t, mgr, tenantUUID, w.ID, 20*time.Minute)

	delay := awaitWatchFollowupBlock(t, convUUID, w.ID, 2*time.Minute)
	t.Logf("watch %s: workflow-run follow-up landed %s after the terminal status was first observed", w.ID, delay.Round(time.Millisecond))
}
