//go:build e2e

package agents

import (
	"context"
	"nudgebee/llm/agents/core"
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
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		got, err := mgr.Get(context.Background(), tenantUUID, w.ID)
		require.NoError(t, err)
		if got.Status.IsTerminal() {
			t.Logf("watch %s reached %s after %d poll(s)", got.ID, got.Status, got.PollCount)
			return
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("watch %s did not reach a terminal status within 5 minutes", w.ID)
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
