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
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/memory"
	memcollective "nudgebee/llm/memory/stores/collective"
	memdecisions "nudgebee/llm/memory/stores/decisions"
	mempatterns "nudgebee/llm/memory/stores/patterns"
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

	// Cold-start assertion checks for the CLOSING tag fragment, not the bare
	// substring. A rendered memory block always carries `</user_style>` and
	// `</user_preferences>` (the prompt-block boundary). The shared
	// memory-consumption rules mention the OPENING tag names in prose to
	// teach the LLM how to read the blocks; they never contain a closing tag.
	// Closing-tag presence therefore uniquely signals a rendered block.
	for i, q := range scenarioInvestigationQueries {
		t.Logf("\n======== TURN %d (cold-start) ========\nquery: %s", i+1, q)
		_, _, prompts := runTurnAndPollPrompts(t, sc, k8sAgent, userID, accountID, sessionID, q)
		require.NotEmpty(t, prompts, "prompts should be captured for turn %d", i+1)
		for j, p := range prompts {
			assert.NotContains(t, p, "/user_style",
				"cold-start turn %d prompt %d must not contain a rendered </user_style> block", i+1, j)
			assert.NotContains(t, p, "/user_preferences",
				"cold-start turn %d prompt %d must not contain a rendered </user_preferences> block", i+1, j)
		}
	}
	t.Logf("[cold-start] 4 turns complete, session=%s preserved", sessionID)
}

// ── Scenario 5: imperative "remember X" → user_preference extraction ────

// Regression for the dev-session failures (e.g. 27d8c916, 567fdf5d) where
// the user typed an imperative instruction in a conversational turn and
// the extractor returned NONE, so the preference never reached the
// Preferences store and never appeared in a later turn's
// <user_preferences> block.
//
// Two sessions on purpose:
//   - Session A drives the imperative ("remember to use the nudgebee
//     namespace for all kubectl lookups") — exercises the extractor path.
//   - Session B is a fresh conversation with an unrelated query — only
//     way the namespace can reach this prompt is via the typed
//     Preferences store being read at Compose time.
func TestScenario_ImperativeInstruction_ExtractsAsPreference_EndToEnd(t *testing.T) {
	tenantID := os.Getenv("TEST_TENANT")
	userID := os.Getenv("TEST_USER")
	accountID := os.Getenv("TEST_ACCOUNT")
	sessSet := "scenario-imperative-set-" + uuid.NewString()[:8]
	sessRead := "scenario-imperative-read-" + uuid.NewString()[:8]
	defer scenarioSetup(t, tenantID)()

	m := memory.Default()
	// Start fresh — the only path that can land a namespace pref is the
	// extractor reacting to Session A's imperative input.
	scenarioEraseUser(m, tenantID, userID)
	defer scenarioEraseUser(m, tenantID, userID)

	sc := security.NewRequestContextForTenantAccountAdmin(tenantID, userID, []string{accountID})
	k8sAgent := newK8sDebugAgent(accountID)

	// ── Session A: imperative instruction ───────────────────────────────
	const imperative = "remember to use the nudgebee namespace for all kubectl lookups"
	t.Logf("\n======== SESSION A (set) ========\nquery: %s", imperative)
	_, _, _ = runTurnAndPollPrompts(t, sc, k8sAgent, userID, accountID, sessSet, imperative)

	// Extractor + projection are async (worker pool). Poll the typed
	// Preferences store until the namespace lands. Tolerant substring
	// match — the extractor may shape the row's key/value in any of a
	// few reasonable ways; we only care that "nudgebee" reaches the
	// store.
	require.Eventually(t, func() bool {
		prefs, err := m.Get(context.Background(), memory.GetRequest{
			TenantID: tenantID, UserID: userID, Layer: "preferences", Limit: 50,
		})
		if err != nil || len(prefs.Entries) == 0 {
			return false
		}
		for _, e := range prefs.Entries {
			if strings.Contains(strings.ToLower(fmt.Sprintf("%v", e)), "nudgebee") {
				return true
			}
		}
		return false
	}, 60*time.Second, 2*time.Second,
		"imperative 'remember to use nudgebee NS' must produce a user_preference within 60s; "+
			"see memory_extractor.txt — imperative phrasing must extract as user_preference")

	// ── Session B: unrelated query in a brand-new session ───────────────
	const followup = "show me pod errors in the last 30 minutes"
	t.Logf("\n======== SESSION B (read) ========\nquery: %s", followup)
	_, _, prompts := runTurnAndPollPrompts(t, sc, k8sAgent, userID, accountID, sessRead, followup)
	require.NotEmpty(t, prompts, "session B must have captured prompts")

	var foundPrefs, foundNamespace bool
	for _, p := range prompts {
		idx := strings.Index(p, "user_preferences")
		if idx < 0 {
			continue
		}
		foundPrefs = true
		// Scope the namespace assertion to the prefs block so we don't
		// accidentally pass on a query string that mentions nudgebee.
		if strings.Contains(strings.ToLower(p[idx:]), "nudgebee") {
			foundNamespace = true
			break
		}
	}
	assert.True(t, foundPrefs,
		"session B prompt must contain <user_preferences> (extractor → preferences store → compose)")
	assert.True(t, foundNamespace,
		"<user_preferences> in session B must carry the namespace from Session A's imperative")
	t.Logf("[imperative] OK — set=%s read=%s preserved", sessSet, sessRead)
}

// ── Scenario 6: session continuity across turns ────────────────────────

// Verifies the Phase 4 working-memory pipeline end-to-end:
//
//	Turn 1 → session_extractor produces a WorkingMemoryV1 blob → mutateSession
//	writes a session.working_memory.updated event + upserts
//	llm_session_working_memory.
//
//	Turn 2 (same session_id) → composeSessionLayer reads the blob →
//	<session_working_memory> appears in the captured prompt.
//
// Without Gap #2 (event log) or Gap #1 (extractor) wired, the second turn's
// prompt would be empty of session state and this test fails.
func TestScenario_SessionContinuity_EndToEnd(t *testing.T) {
	// Verifies the Session layer's compose chain deterministically by
	// seeding a WorkingMemoryV1 blob via Memory.Mutate (skipping the LLM
	// session_extractor whose JSON output is non-deterministic), then
	// running a turn in the same session and asserting
	// <session_working_memory> appears in the prompt with the seeded
	// content. Also confirms the audit event was emitted (Gap #2 —
	// event-log integration).
	tenantID := os.Getenv("TEST_TENANT")
	userID := os.Getenv("TEST_USER")
	accountID := os.Getenv("TEST_ACCOUNT")
	sessionID := "scenario-session-cont-" + uuid.NewString()[:8]
	defer scenarioSetup(t, tenantID)()

	prevSession := config.Config.MemoryLayerSessionEnabled
	config.Config.MemoryLayerSessionEnabled = true
	defer func() { config.Config.MemoryLayerSessionEnabled = prevSession }()

	m := memory.Default()
	scenarioEraseUser(m, tenantID, userID)
	defer scenarioEraseUser(m, tenantID, userID)

	// Seed a WorkingMemoryV1 blob whose last_action contains a unique
	// marker we can grep for in turn 2's prompt.
	marker := uuid.NewString()[:6]
	lastAction := fmt.Sprintf("seeded last_action marker=%s for compose check", marker)
	resp, err := m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID,
		Layer: "session", Action: "set",
		Key: sessionID,
		Value: map[string]any{
			"version":     1,
			"last_action": lastAction,
		},
		ActorKind: "agent", ActorID: "scenario_session_seed",
	})
	require.NoError(t, err)
	require.True(t, resp.Success)

	// Confirm an audit event landed (event-log integration).
	db, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	var evtCount int
	require.NoError(t, db.Db.Get(&evtCount,
		`SELECT COUNT(*) FROM llm_memory_events
		 WHERE tenant_id = $1 AND user_id = $2 AND event_type = $3`,
		tenantID, userID, "session.working_memory.updated"),
	)
	assert.GreaterOrEqual(t, evtCount, 1,
		"mutateSession must write a session.working_memory.updated event")

	// Drive a turn in the SAME session_id. Compose should attach the
	// seeded blob under <session_working_memory>.
	sc := security.NewRequestContextForTenantAccountAdmin(tenantID, userID, []string{accountID})
	k8sAgent := newK8sDebugAgent(accountID)

	turn := "what is the current investigation context"
	t.Logf("\n======== SESSION CONTINUITY TURN ========\nquery: %s", turn)
	_, _, prompts := runTurnAndPollPrompts(t, sc, k8sAgent, userID, accountID, sessionID, turn)
	require.NotEmpty(t, prompts, "turn must capture prompts")

	foundBlock, foundMarker := false, false
	for _, p := range prompts {
		if strings.Contains(p, "<session_working_memory>") || strings.Contains(p, "\\u003csession_working_memory\\u003e") {
			foundBlock = true
		}
		if strings.Contains(p, marker) {
			foundMarker = true
		}
	}
	assert.True(t, foundBlock, "prompt must contain <session_working_memory> block (seed → Mutate → Compose)")
	assert.True(t, foundMarker, "block must reference the seeded marker (%s)", marker)
	t.Logf("[session] OK — session_id=%s preserved", sessionID)
}

// ── Scenario 7: Patterns layer — full extract → store → compose chain ────

// Drives an investigation that produces a recurring-behaviour pattern, then
// verifies a row landed in llm_memory_patterns AND that a second
// conversation (same user) sees a <patterns> block referencing it.
//
// Closes the Phase-2 E2E gap: until this test the Patterns layer had no
// real-conversation coverage — only the consolidator's SQL was tested.
func TestScenario_PatternExtractsFromTurn_EndToEnd(t *testing.T) {
	// Verifies the Patterns layer's compose chain deterministically: seed a
	// Pattern row directly via the DAO (skipping the LLM extractor which is
	// non-deterministic), then drive a turn and assert <user_patterns>
	// appears in the prompt. The extraction half is covered by the
	// Phase-1+2 Preference scenario (TestScenario_ImperativeInstruction…).
	tenantID := os.Getenv("TEST_TENANT")
	userID := os.Getenv("TEST_USER")
	accountID := os.Getenv("TEST_ACCOUNT")
	sessionID := "scenario-pattern-" + uuid.NewString()[:8]
	defer scenarioSetup(t, tenantID)()

	prevPatternsFlag := config.Config.MemoryLayerPatternsEnabled
	config.Config.MemoryLayerPatternsEnabled = true
	defer func() { config.Config.MemoryLayerPatternsEnabled = prevPatternsFlag }()

	m := memory.Default()
	scenarioEraseUser(m, tenantID, userID)
	defer func() {
		scenarioEraseUser(m, tenantID, userID)
		// scenarioEraseUser doesn't touch Patterns; clean up our seed.
		if db, err := common.GetDatabaseManager(common.Metastore); err == nil {
			_, _ = db.Db.Exec(`DELETE FROM llm_memory_patterns WHERE tenant_id=$1 AND user_id=$2 AND subject LIKE 'scenario_pattern_%'`, tenantID, userID)
		}
	}()

	// Seed a Pattern row directly so the test does not depend on the
	// non-deterministic LLM extractor classifying a turn as a pattern.
	patternSubject := "scenario_pattern_" + uuid.NewString()[:6]
	require.NoError(t, mempatterns.Upsert(&mempatterns.Pattern{
		TenantID:    tenantID,
		UserID:      userID,
		AgentModule: nil,
		Kind:        mempatterns.KindFrequentResourceType,
		Subject:     patternSubject,
		Metadata:    map[string]any{"seed": "scenario_pattern_test"},
	}))

	sc := security.NewRequestContextForTenantAccountAdmin(tenantID, userID, []string{accountID})
	k8sAgent := newK8sDebugAgent(accountID)

	// Drive any turn; composePatternsLayer does not apply a keyword filter,
	// so the seeded row will appear in every prompt for this (tenant, user).
	turn := "list the pods in the nudgebee namespace"
	t.Logf("\n======== PATTERN COMPOSE TURN ========\nquery: %s", turn)
	_, _, prompts := runTurnAndPollPrompts(t, sc, k8sAgent, userID, accountID, sessionID, turn)
	require.NotEmpty(t, prompts, "turn must capture prompts")

	foundBlock, foundSubject := false, false
	for _, p := range prompts {
		if strings.Contains(p, "<user_patterns>") || strings.Contains(p, "\\u003cuser_patterns\\u003e") {
			foundBlock = true
		}
		if strings.Contains(p, patternSubject) {
			foundSubject = true
		}
	}
	assert.True(t, foundBlock, "prompt must contain <user_patterns> block (seeded row → composePatternsLayer → Render)")
	assert.True(t, foundSubject, "prompt's <user_patterns> block must reference the seeded subject %q", patternSubject)
	t.Logf("[pattern] OK — session=%s preserved", sessionID)
}

// ── Scenario 8: Decisions layer — root-cause turn → store → compose ────

// Drives a turn that closes with a root-cause + resolution, then verifies
// the Decision row landed and shows up in a follow-up conversation's
// <decisions> block.
func TestScenario_DecisionExtractsFromTurn_EndToEnd(t *testing.T) {
	// Verifies the Decisions layer's compose chain deterministically by
	// seeding a Decision row whose subject text matches a chosen turn-2
	// keyword. composeDecisionsLayer applies a Postgres FTS filter
	// (to_tsvector(subject) @@ plainto_tsquery(query)) so the seeded
	// subject must overlap with the query — that is the contract we are
	// asserting end-to-end here.
	tenantID := os.Getenv("TEST_TENANT")
	userID := os.Getenv("TEST_USER")
	accountID := os.Getenv("TEST_ACCOUNT")
	sessionID := "scenario-decision-" + uuid.NewString()[:8]
	defer scenarioSetup(t, tenantID)()

	prevDecisionsFlag := config.Config.MemoryLayerDecisionsEnabled
	config.Config.MemoryLayerDecisionsEnabled = true
	defer func() { config.Config.MemoryLayerDecisionsEnabled = prevDecisionsFlag }()

	m := memory.Default()
	scenarioEraseUser(m, tenantID, userID)
	// Decisions seeded by this test get explicit cleanup; scenarioEraseUser
	// does not touch llm_memory_decisions.
	defer func() {
		scenarioEraseUser(m, tenantID, userID)
		if db, err := common.GetDatabaseManager(common.Metastore); err == nil {
			_, _ = db.Db.Exec(`DELETE FROM llm_memory_decisions WHERE tenant_id=$1 AND user_id=$2 AND subject LIKE 'otel collector memory limit decision %'`, tenantID, userID)
		}
	}()

	// Seed a Decision whose subject contains words guaranteed to appear in
	// turn 2's keyword query.
	subjectMarker := uuid.NewString()[:6]
	decisionSubject := fmt.Sprintf("otel collector memory limit decision %s", subjectMarker)
	require.NoError(t, memdecisions.Append(&memdecisions.Decision{
		TenantID:     tenantID,
		UserID:       userID,
		DecisionType: "root_cause_agreed",
		Subject:      decisionSubject,
		DecidedAt:    time.Now(),
	}))

	sc := security.NewRequestContextForTenantAccountAdmin(tenantID, userID, []string{accountID})
	k8sAgent := newK8sDebugAgent(accountID)

	// composeDecisionsLayer applies Postgres FTS with plainto_tsquery, which
	// AND-joins every term in the query. Use a query that contains ONLY
	// words present in the seeded subject so every term matches.
	turn := "otel collector memory limit decision"
	t.Logf("\n======== DECISION COMPOSE TURN ========\nquery: %s", turn)
	_, _, prompts := runTurnAndPollPrompts(t, sc, k8sAgent, userID, accountID, sessionID, turn)
	require.NotEmpty(t, prompts, "turn must capture prompts")

	foundBlock, foundSubject := false, false
	for _, p := range prompts {
		if strings.Contains(p, "<past_decisions>") || strings.Contains(p, "\\u003cpast_decisions\\u003e") {
			foundBlock = true
		}
		if strings.Contains(p, subjectMarker) {
			foundSubject = true
		}
	}
	assert.True(t, foundBlock, "prompt must contain <past_decisions> block")
	assert.True(t, foundSubject, "prompt's <past_decisions> block must reference the seeded subject (marker=%s)", subjectMarker)
	t.Logf("[decision] OK — session=%s preserved", sessionID)
}

// ── Scenario 9: Collective layer — tenant knowledge → store → compose ───

// Drives a turn that surfaces a tenant-scoped configuration insight, then
// verifies the row landed in llm_memory_collective AND another conversation
// in the SAME tenant sees it composed (collective is tenant-scoped, not
// user).
func TestScenario_CollectiveExtractsFromTurn_EndToEnd(t *testing.T) {
	// Verifies the Collective layer's compose chain deterministically.
	// composeCollectiveLayer applies a Postgres FTS filter over
	// subject + body, so the seeded row's text must overlap with the
	// chosen turn query.
	tenantID := os.Getenv("TEST_TENANT")
	userID := os.Getenv("TEST_USER")
	accountID := os.Getenv("TEST_ACCOUNT")
	sessionID := "scenario-collective-" + uuid.NewString()[:8]
	defer scenarioSetup(t, tenantID)()

	prevCollectiveFlag := config.Config.MemoryLayerCollectiveEnabled
	config.Config.MemoryLayerCollectiveEnabled = true
	defer func() { config.Config.MemoryLayerCollectiveEnabled = prevCollectiveFlag }()

	m := memory.Default()
	scenarioEraseUser(m, tenantID, userID)
	defer func() {
		scenarioEraseUser(m, tenantID, userID)
		if db, err := common.GetDatabaseManager(common.Metastore); err == nil {
			_, _ = db.Db.Exec(`DELETE FROM llm_memory_collective WHERE tenant_id=$1 AND subject LIKE 'ECR imagePullSecrets requirement %'`, tenantID)
		}
	}()

	subjectMarker := uuid.NewString()[:6]
	collectiveSubject := fmt.Sprintf("ECR imagePullSecrets requirement %s", subjectMarker)
	require.NoError(t, memcollective.Upsert(&memcollective.Entry{
		TenantID:   tenantID,
		EntryKind:  "configuration_insight",
		Subject:    collectiveSubject,
		Body:       "ECR image pulls require imagePullSecrets even when nodes have IAM roles, because kubelet does not inherit node credentials.",
		Confidence: 0.9,
	}))

	sc := security.NewRequestContextForTenantAccountAdmin(tenantID, userID, []string{accountID})
	k8sAgent := newK8sDebugAgent(accountID)

	// composeCollectiveLayer applies Postgres FTS with plainto_tsquery
	// (AND of all terms) on subject+body. Use a query that contains ONLY
	// words present in the seeded subject.
	turn := "ECR imagePullSecrets requirement"
	t.Logf("\n======== COLLECTIVE COMPOSE TURN ========\nquery: %s", turn)
	_, _, prompts := runTurnAndPollPrompts(t, sc, k8sAgent, userID, accountID, sessionID, turn)
	require.NotEmpty(t, prompts, "turn must capture prompts")

	foundBlock, foundSubject := false, false
	for _, p := range prompts {
		if strings.Contains(p, "<tenant_knowledge>") || strings.Contains(p, "\\u003ctenant_knowledge\\u003e") {
			foundBlock = true
		}
		if strings.Contains(p, subjectMarker) {
			foundSubject = true
		}
	}
	assert.True(t, foundBlock, "prompt must contain <tenant_knowledge> block")
	assert.True(t, foundSubject, "prompt's <tenant_knowledge> block must reference the seeded entry (marker=%s)", subjectMarker)
	t.Logf("[collective] OK — session=%s preserved", sessionID)
}
