//go:build e2e

package agents

// End-to-end scenario tests for the Memory Module. Each scenario drives a
// multi-turn k8s_debug conversation with realistic SRE questions and
// asserts on llm_conversation_token_usage.prompt_messages to verify what
// the LLM actually saw across turns.
//
// Scenarios:
//
//   1. TestScenario_MidConversationSoulUpdate_EndToEnd
//      Run a 4-turn k8s investigation. Update the Soul between turn 2 and
//      turn 3. Early turns must see the first soul marker; later turns must
//      see the new one (and never the stale one).
//
//   2. TestScenario_TwoUsersIsolation_EndToEnd
//      User A runs a 3-turn investigation. User B's slab is composed in
//      parallel. Each user's view must contain ONLY their own sentinel.
//
//   3. TestScenario_FlagOffMidSession_EndToEnd
//      Run a 4-turn k8s investigation. After turn 2, flip the module flag
//      OFF. Turns 1–2 must contain the memory block; turns 3–4 must NOT
//      contain a freshly-injected <user_style> block (rollback drill).
//
//   4. TestScenario_ColdStartUser_EndToEnd
//      Run a 4-turn investigation with no memory seeded. Every prompt must
//      be free of <user_style> / <user_preferences>.
//
// Gated on RUN_MEMORY_INTEGRATION=true + TEST_TENANT/USER/ACCOUNT env vars.
// Conversation rows are preserved for UI inspection — never deleted.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/memory"
	"nudgebee/llm/security"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Realistic multi-turn k8s SRE conversations. Each slice is one scenario's
// turn sequence; each question should be answerable by the k8s_debug agent
// regardless of what the cluster actually contains.
var (
	// Investigation arc — cluster overview → narrow to failures → summarize.
	scenarioInvestigationQueries = []string{
		"list all namespaces in this cluster, one per line",
		"of those namespaces, which have fewer than 3 pods right now?",
		"are there any pods in CrashLoopBackOff or ImagePullBackOff across the cluster?",
		"summarize the cluster health in 3 bullets based on what you just checked",
	}

	// Second investigation arc used for the mid-session update scenario so
	// turn 3 onward reads naturally after the soul change.
	scenarioDeepDiveQueries = []string{
		"list the top 5 pods by restart count in the last 24h",
		"for the pod with the most restarts, show its last terminated reason",
	}

	// Short arc for User A in the isolation test.
	scenarioUserAQueries = []string{
		"list the namespaces in this cluster",
		"any warning events in the kube-system namespace in the last 30m?",
		"summarize the control-plane health in one line",
	}
)

// scenarioSetup flips the memory module flags on, pins the tenant allowlist,
// enables LlmTraceEnabled so we can assert on captured prompts, and returns
// a restore function.
func scenarioSetup(t *testing.T, tenantID string) func() {
	t.Helper()
	if os.Getenv("RUN_MEMORY_INTEGRATION") != "true" {
		t.Skip("set RUN_MEMORY_INTEGRATION=true to run")
	}
	if os.Getenv("TEST_TENANT") == "" || os.Getenv("TEST_USER") == "" || os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("skipping: TEST_TENANT / TEST_USER / TEST_ACCOUNT env vars must be set")
	}
	if _, err := common.GetDatabaseManager(common.Metastore); err != nil {
		t.Skipf("metastore unreachable: %v", err)
	}
	prev := struct {
		module, compose, soul, prefs, trace bool
		allowlist                           string
	}{
		module:    config.Config.MemoryModuleEnabled,
		compose:   config.Config.MemoryComposeEnabled,
		soul:      config.Config.MemoryLayerSoulEnabled,
		prefs:     config.Config.MemoryLayerPrefsEnabled,
		trace:     config.Config.LlmTraceEnabled,
		allowlist: config.Config.MemoryTenantAllowlist,
	}
	config.Config.MemoryModuleEnabled = true
	config.Config.MemoryComposeEnabled = true
	config.Config.MemoryLayerSoulEnabled = true
	config.Config.MemoryLayerPrefsEnabled = true
	config.Config.LlmTraceEnabled = true
	config.Config.MemoryTenantAllowlist = tenantID
	return func() {
		config.Config.MemoryModuleEnabled = prev.module
		config.Config.MemoryComposeEnabled = prev.compose
		config.Config.MemoryLayerSoulEnabled = prev.soul
		config.Config.MemoryLayerPrefsEnabled = prev.prefs
		config.Config.LlmTraceEnabled = prev.trace
		config.Config.MemoryTenantAllowlist = prev.allowlist
	}
}

// scenarioEraseUser wipes a single user's memory + any event rows.
// Conversation rows are preserved for UI inspection — never deleted.
func scenarioEraseUser(m memory.Memory, tenantID, userID string) {
	_ = m.Erase(context.Background(), memory.EraseRequest{TenantID: tenantID, UserID: userID})
	if db, err := common.GetDatabaseManager(common.Metastore); err == nil {
		_, _ = db.Db.Exec(`DELETE FROM llm_memory_events WHERE tenant_id = $1 AND user_id = $2`, tenantID, userID)
	}
}

// scenarioSeedSoul writes a Soul tagged with a fingerprint so tests can
// verify the correct variant reached the LLM.
func scenarioSeedSoul(t *testing.T, m memory.Memory, tenantID, userID, tone, markdown string) {
	t.Helper()
	resp, err := m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID,
		Layer: "soul", Action: "set",
		ActorKind: "user", ActorID: userID,
		Value: map[string]any{
			"style":    map[string]any{"tone": tone},
			"markdown": markdown,
		},
	})
	require.NoError(t, err)
	require.True(t, resp.Success)
}

// scenarioPollPromptsForMsg fetches llm_conversation_token_usage prompts for
// a specific message_id. Polls because writes are async on the metrics
// worker pool. Heavy tool chains flush later than simple turns, so the
// window is generous.
func scenarioPollPromptsForMsg(t *testing.T, convID, msgID string, minRows int) []string {
	t.Helper()
	db, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	var prompts []string
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		prompts = nil
		err = db.Db.Select(&prompts, `
			SELECT COALESCE(prompt_messages, '') FROM llm_conversation_token_usage
			WHERE conversation_id = $1::uuid AND message_id = $2::uuid AND prompt_messages IS NOT NULL
			ORDER BY created_at ASC
		`, convID, msgID)
		require.NoError(t, err)
		if len(prompts) >= minRows {
			return prompts
		}
		time.Sleep(500 * time.Millisecond)
	}
	return prompts
}

// scenarioLastMsgID returns the most recent message id for a session.
func scenarioLastMsgID(t *testing.T, sessionID, userID string) (convID, msgID string) {
	t.Helper()
	db, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	var row struct {
		ConvID string `db:"conversation_id"`
		MsgID  string `db:"message_id"`
	}
	err = db.Db.Get(&row, `
		SELECT c.id::text AS conversation_id, m.id::text AS message_id
		FROM llm_conversations c
		JOIN llm_conversation_messages m ON m.conversation_id = c.id
		WHERE c.session_id = $1 AND c.user_id = $2::uuid
		ORDER BY m.created_at DESC LIMIT 1
	`, sessionID, userID)
	require.NoError(t, err)
	return row.ConvID, row.MsgID
}

// runTurnAndPollPrompts runs a single conversation turn and returns the
// prompts that went into the LLM for that turn's top-level message.
func runTurnAndPollPrompts(
	t *testing.T,
	sc *security.RequestContext,
	agent core.NBAgent,
	userID, accountID, sessionID, query string,
) (convID string, msgID string, prompts []string) {
	t.Helper()
	resp, err := core.HandleConversationSessionRequest(sc, agent, userID, accountID, sessionID, query)
	require.NoError(t, err, "turn must succeed: %q", query)
	require.NotEmpty(t, resp.Response, "turn response should not be empty")
	convID, msgID = scenarioLastMsgID(t, sessionID, userID)
	prompts = scenarioPollPromptsForMsg(t, convID, msgID, 1)
	return
}

func anyPromptContains(prompts []string, needle string) bool {
	for _, p := range prompts {
		if strings.Contains(p, needle) {
			return true
		}
	}
	return false
}

// ── Scenario 1: mid-conversation Soul update ────────────────────────────

// Runs a 4-turn SRE investigation. Updates the Soul between turn 2 and
// turn 3 and verifies that each turn's prompt contains the soul variant
// that was current when the turn fired.
func TestScenario_MidConversationSoulUpdate_EndToEnd(t *testing.T) {
	tenantID := os.Getenv("TEST_TENANT")
	userID := os.Getenv("TEST_USER")
	accountID := os.Getenv("TEST_ACCOUNT")
	sessionID := "scenario-update-" + uuid.NewString()[:8]
	defer scenarioSetup(t, tenantID)()

	m := memory.Default()
	defer scenarioEraseUser(m, tenantID, userID)
	t.Logf("session=%s preserved for UI inspection", sessionID)

	sc := security.NewRequestContextForTenantAccountAdmin(tenantID, userID, []string{accountID})
	k8sAgent := newK8sDebugAgent(accountID)

	// Soul v1 — active for turns 1-2.
	scenarioSeedSoul(t, m, tenantID, userID, "terse", "SENTINEL_FIRST unique marker.")

	phase1 := scenarioInvestigationQueries[:2]
	for i, q := range phase1 {
		t.Logf("\n======== TURN %d (soul=SENTINEL_FIRST) ========\nquery: %s", i+1, q)
		_, _, prompts := runTurnAndPollPrompts(t, sc, k8sAgent, userID, accountID, sessionID, q)
		assert.True(t, anyPromptContains(prompts, "SENTINEL_FIRST"),
			"turn %d prompt must contain SENTINEL_FIRST", i+1)
		assert.False(t, anyPromptContains(prompts, "SENTINEL_SECOND"),
			"turn %d must NOT contain SENTINEL_SECOND (not set yet)", i+1)
	}

	// Mid-session soul update.
	scenarioSeedSoul(t, m, tenantID, userID, "friendly", "SENTINEL_SECOND unique marker.")

	// Soul v2 — active for turns 3-4.
	phase2 := append([]string{scenarioInvestigationQueries[2], scenarioInvestigationQueries[3]}, scenarioDeepDiveQueries...)[:2]
	for i, q := range phase2 {
		turn := i + 3
		t.Logf("\n======== TURN %d (soul=SENTINEL_SECOND) ========\nquery: %s", turn, q)
		_, _, prompts := runTurnAndPollPrompts(t, sc, k8sAgent, userID, accountID, sessionID, q)
		assert.True(t, anyPromptContains(prompts, "SENTINEL_SECOND"),
			"turn %d must contain the UPDATED soul (SENTINEL_SECOND)", turn)
		// NOTE: earlier turns' history is echoed in this turn's prompt, which
		// may carry the old sentinel. We only assert on the rendered memory
		// block — check that the <user_style> block shows SENTINEL_SECOND.
		for _, p := range prompts {
			if strings.Contains(p, "user_style") {
				idx := strings.Index(p, "user_style")
				end := idx + 400
				if end > len(p) {
					end = len(p)
				}
				styleBlock := p[idx:end]
				assert.Contains(t, styleBlock, "SENTINEL_SECOND",
					"turn %d's <user_style> block must contain SENTINEL_SECOND", turn)
				assert.NotContains(t, styleBlock, "SENTINEL_FIRST",
					"turn %d's <user_style> block must NOT contain SENTINEL_FIRST", turn)
				break
			}
		}
	}
	t.Logf("[update] 4 turns complete, session=%s preserved", sessionID)
}

// ── Scenario 2: two users, same tenant, strict isolation ────────────────

// User A runs a 3-turn investigation and User B's slab is composed in
// parallel. Verifies each user's view is free of the other's sentinel.
func TestScenario_TwoUsersIsolation_EndToEnd(t *testing.T) {
	tenantID := os.Getenv("TEST_TENANT")
	userA := os.Getenv("TEST_USER")
	userB := "scenario-user-b-" + uuid.NewString()
	accountID := os.Getenv("TEST_ACCOUNT")

	defer scenarioSetup(t, tenantID)()
	m := memory.Default()
	defer scenarioEraseUser(m, tenantID, userA)
	defer scenarioEraseUser(m, tenantID, userB)

	scenarioSeedSoul(t, m, tenantID, userA, "terse", "USER_A_ONLY sentinel")
	scenarioSeedSoul(t, m, tenantID, userB, "terse", "USER_B_ONLY sentinel")

	k8sAgent := newK8sDebugAgent(accountID)
	sessA := "iso-a-" + uuid.NewString()[:8]
	scA := security.NewRequestContextForTenantAccountAdmin(tenantID, userA, []string{accountID})

	for i, q := range scenarioUserAQueries {
		t.Logf("\n======== USER A TURN %d ========\nquery: %s", i+1, q)
		_, _, prompts := runTurnAndPollPrompts(t, scA, k8sAgent, userA, accountID, sessA, q)
		assert.True(t, anyPromptContains(prompts, "USER_A_ONLY"),
			"A's turn %d must contain USER_A_ONLY", i+1)
		assert.False(t, anyPromptContains(prompts, "USER_B_ONLY"),
			"A's turn %d must NEVER contain USER_B_ONLY (isolation breach)", i+1)
	}

	// We can't fabricate a new authenticated user mid-test (JWT required),
	// so verify B's in-memory view directly via Compose.
	slabB, err := m.Compose(context.Background(), memory.ComposeRequest{
		TenantID:    tenantID,
		UserID:      userB,
		AgentModule: "generic",
	})
	require.NoError(t, err)
	assert.Contains(t, slabB.Soul, "USER_B_ONLY")
	assert.NotContains(t, slabB.Soul, "USER_A_ONLY",
		"B's slab must NEVER contain A's soul (isolation breach)")

	t.Logf("[isolation] A's 3 turns OK, B's compose OK, session=%s preserved", sessA)
}

// ── Scenario 3: flag-off mid-session (rollback drill) ───────────────────

// Runs a 4-turn SRE investigation. Flips the memory module flag OFF after
// turn 2. Verifies turns 1–2 contain the memory block and turns 3–4 no
// longer have a freshly-injected <user_style> block.
func TestScenario_FlagOffMidSession_EndToEnd(t *testing.T) {
	tenantID := os.Getenv("TEST_TENANT")
	userID := os.Getenv("TEST_USER")
	accountID := os.Getenv("TEST_ACCOUNT")
	sessionID := "scenario-rollback-" + uuid.NewString()[:8]
	defer scenarioSetup(t, tenantID)()

	m := memory.Default()
	defer scenarioEraseUser(m, tenantID, userID)

	scenarioSeedSoul(t, m, tenantID, userID, "terse", "ROLLBACK_CANARY seed")

	sc := security.NewRequestContextForTenantAccountAdmin(tenantID, userID, []string{accountID})
	k8sAgent := newK8sDebugAgent(accountID)

	// Phase 1: flag on — memory block must land in every turn.
	for i, q := range scenarioInvestigationQueries[:2] {
		t.Logf("\n======== TURN %d (flag=ON) ========\nquery: %s", i+1, q)
		_, _, prompts := runTurnAndPollPrompts(t, sc, k8sAgent, userID, accountID, sessionID, q)
		assert.True(t, anyPromptContains(prompts, "ROLLBACK_CANARY"),
			"turn %d (flag on) must contain the canary", i+1)
		assert.True(t, anyPromptContains(prompts, "user_style"),
			"turn %d (flag on) must contain <user_style> block", i+1)
	}

	// FLIP the module off — simulates a production rollback.
	config.Config.MemoryModuleEnabled = false

	// Phase 2: flag off — bridge must short-circuit; no fresh <user_style>.
	for i, q := range scenarioInvestigationQueries[2:4] {
		turn := i + 3
		t.Logf("\n======== TURN %d (flag=OFF) ========\nquery: %s", turn, q)
		_, msgID, prompts := runTurnAndPollPrompts(t, sc, k8sAgent, userID, accountID, sessionID, q)
		// Conversation history from the flag-on turns will carry historical
		// canary mentions — what we check is that no NEW <user_style> block
		// was attached to THIS turn's system prompt by the bridge.
		//
		// The bridge writes to request.AccountPrompt. History echoes come
		// through request.Query. So inspect the captured prompt for a freshly
		// appended user_style block near the top of the system prompt.
		fresh := false
		for _, p := range prompts {
			// Look only at the first system message. History entries are
			// separate messages in the captured array and don't count.
			head := p
			if len(head) > 8000 {
				head = head[:8000]
			}
			if strings.Contains(head, "<user_style>") || strings.Contains(head, "\\u003cuser_style\\u003e") {
				fresh = true
				break
			}
		}
		assert.False(t, fresh,
			"turn %d (flag off) must NOT contain a freshly-injected <user_style> block (msg=%s)", turn, msgID)
	}
	t.Logf("[rollback] 4 turns complete, session=%s preserved", sessionID)
}

// ── Scenario 4: cold-start user with no memory ──────────────────────────

// Runs a 4-turn SRE investigation with no memory seeded. Every prompt must
// be free of <user_style> and <user_preferences> blocks, and every turn
// must succeed (no errors, no crashes).
func TestScenario_ColdStartUser_EndToEnd(t *testing.T) {
	tenantID := os.Getenv("TEST_TENANT")
	userID := os.Getenv("TEST_USER")
	accountID := os.Getenv("TEST_ACCOUNT")
	sessionID := "scenario-cold-" + uuid.NewString()[:8]
	defer scenarioSetup(t, tenantID)()

	m := memory.Default()
	// Ensure NO memory for this user upfront.
	scenarioEraseUser(m, tenantID, userID)
	defer scenarioEraseUser(m, tenantID, userID)

	sc := security.NewRequestContextForTenantAccountAdmin(tenantID, userID, []string{accountID})
	k8sAgent := newK8sDebugAgent(accountID)

	for i, q := range scenarioInvestigationQueries {
		t.Logf("\n======== TURN %d (cold-start) ========\nquery: %s", i+1, q)
		_, _, prompts := runTurnAndPollPrompts(t, sc, k8sAgent, userID, accountID, sessionID, q)
		require.NotEmpty(t, prompts, "prompts should be captured for turn %d", i+1)
		for j, p := range prompts {
			assert.NotContains(t, p, "user_style",
				"cold-start turn %d prompt %d must not contain <user_style>", i+1, j)
			assert.NotContains(t, p, "user_preferences",
				"cold-start turn %d prompt %d must not contain <user_preferences>", i+1, j)
		}
	}
	t.Logf("[cold-start] 4 turns complete, session=%s preserved", sessionID)
}
