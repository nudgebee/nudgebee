//go:build e2e

// Integration (e2e) tests for scratchpad compression visibility against a real
// PostgreSQL database and a real LLM provider. They validate the behavior change
// in #32987: scratchpad/observation compression is gated on context-window
// pressure (scratchpadBudget / activation fraction), NOT on a flat step count.
//
// Why these are integration tests rather than pure unit tests: the gating
// decision (resolveMaxContextTokens -> scratchpadBudget) and the visibility
// write (SaveCompressionVisibility -> llm_conversation_tool_calls) only have
// teeth end-to-end — against a resolvable model window and a real conversation
// row chain (conversation -> message -> agent) whose FKs the tool_calls row
// requires. CountCompressionVisibilityRecords reads that row back.
//
// They use TEST_TENANT / TEST_ACCOUNT / TEST_USER and the LLM provider config
// from .env (same pattern as the other *_e2e_test.go files; env is loaded by
// the shared TestMain in egressfilter_e2e_test.go). They skip cleanly when the
// required env is absent.
//
// Run:
//
//	go test -tags=e2e -v -run TestE2E_ScratchpadCompression ./agents/core/...
//
// No internal identifiers appear in this file — everything that varies between
// deployments comes from environment variables.
package core

import (
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// compressionE2EEnv carries the tenant/account/user needed to drive a real
// conversation row chain. All values come from environment variables.
type compressionE2EEnv struct {
	tenant  string
	account string
	user    string
}

func loadCompressionE2EEnv(t *testing.T) compressionE2EEnv {
	t.Helper()
	env := compressionE2EEnv{
		tenant:  os.Getenv("TEST_TENANT"),
		account: os.Getenv("TEST_ACCOUNT"),
		user:    os.Getenv("TEST_USER"),
	}
	if env.tenant == "" || env.account == "" || env.user == "" {
		t.Skip("TEST_TENANT / TEST_ACCOUNT / TEST_USER must be set for compression e2e tests")
	}
	if config.Config.LlmServerDBUrl == "" {
		t.Skip("LLM_SERVER_DB_URL must be set for compression e2e tests")
	}
	return env
}

// compressionFixture is a real conversation -> message -> agent chain persisted
// to PostgreSQL, plus the NBAgentRequest and RequestContext that point a
// ConstructScratchPad call at it. Closing it deletes the conversation (cascades
// to messages / agents / tool_calls).
type compressionFixture struct {
	ctx       *security.RequestContext
	req       NBAgentRequest
	convID    string
	sessionID string
	env       compressionE2EEnv
}

// newCompressionFixture creates a fresh conversation row chain so each sub-test
// asserts against an isolated conversation_id (the visibility record count is
// per-conversation). When keep is false the chain is deleted on cleanup; when
// true it is preserved (and saved COMPLETED) so it can be inspected in the UI.
func newCompressionFixture(t *testing.T, env compressionE2EEnv, keep bool) *compressionFixture {
	t.Helper()
	dao := GetConversationDao()

	sessionID := "ut-compression-" + uuid.NewString()[:8]
	// Idempotent: clear any prior row chain for this session before creating.
	_ = DeleteConversationBySession(sessionID, env.account, env.user)

	// A kept conversation renders in the UI list only once it isn't IN_PROGRESS.
	status := ConversationStatusInProgress
	if keep {
		status = ConversationStatusCompleted
	}

	convUUID, err := dao.SaveConversation(
		uuid.NewString(), sessionID, env.tenant, env.account, env.user,
		"", "compression e2e", status,
		ConversationSourceUserInvestigation,
		config.Config.LlmProvider, config.Config.LlmModel, nil)
	require.NoError(t, err, "SaveConversation must succeed (DB reachable + tenant/account valid)")
	convID := convUUID.String()

	msgUUID, err := dao.SaveConversationMessage(
		uuid.NewString(), convID, env.account, env.user,
		MessageRoleHuman, MessageTypeGeneration,
		"investigate something that takes many steps", "",
		"LLM", uuid.Nil, nil, "",
		config.Config.LlmProvider, config.Config.LlmModel)
	require.NoError(t, err, "SaveConversationMessage must succeed")
	msgID := msgUUID.String()

	agentUUID, err := dao.SaveConversationAgentCall(
		convID, msgID, env.account, env.user,
		"k8s_debug", "", "investigate", "", "", "",
		toolcore.NBQueryConfig{})
	require.NoError(t, err, "SaveConversationAgentCall must succeed")
	agentID := agentUUID.String()

	// ParentAgentId == AgentId: SaveCompressionVisibility keys the synthetic
	// tool_call row on ParentAgentId, which must reference a real agent row.
	req := NBAgentRequest{
		AccountId:      env.account,
		ConversationId: convID,
		MessageId:      msgID,
		AgentId:        agentID,
		ParentAgentId:  agentID,
		UserId:         env.user,
	}

	ctx := security.NewRequestContextForTenantAccountAdmin(env.tenant, env.user, []string{env.account})

	if keep {
		// A real chat flow flips the message + agent rows to a terminal state at
		// the end of execution AND writes a response; this synthetic chain never
		// runs that step, so the UI would otherwise render the kept conversation
		// as perpetually in-progress, and an empty response renders as "killed
		// before it could complete". Mark them terminal and set a response so it
		// shows as a clean completed conversation for inspection. (The compression
		// tool_call card is already persisted as 'success'.)
		const keptResponse = "Investigation complete. Older tool observations were compressed to stay " +
			"within the context window — see the context_compression card for the per-step reduction."
		if cd, ok := dao.(*ConversationDao); ok {
			_, _ = cd.dbManager.Db.Exec(
				`UPDATE llm_conversation_messages SET status='COMPLETED', response=$2, updated_at=now()
				 WHERE conversation_id=$1 AND status='IN_PROGRESS'`, convID, keptResponse)
			_, _ = cd.dbManager.Db.Exec(
				`UPDATE llm_conversation_agent SET status='success', response=$2, updated_at=now()
				 WHERE conversation_id=$1 AND status='in_progress'`, convID, keptResponse)
		}
	} else {
		t.Cleanup(func() {
			_ = DeleteConversationBySession(sessionID, env.account, env.user)
		})
	}

	return &compressionFixture{ctx: ctx, req: req, convID: convID, sessionID: sessionID, env: env}
}

// makeToolSteps builds n SUCCESS tool steps, each with an observation of
// obsChars bytes. The observations are large enough (>500) to be eligible for
// compression so the gate — not the per-observation floor — is what decides.
func makeToolSteps(n, obsChars int) []NBAgentPlannerToolActionStep {
	steps := make([]NBAgentPlannerToolActionStep, 0, n)
	for i := 0; i < n; i++ {
		id := "E" + uuid.NewString()[:6]
		steps = append(steps, NBAgentPlannerToolActionStep{
			Action: NBAgentPlannerToolAction{
				Tool:      "kubectl",
				ToolID:    id,
				ToolInput: "get pods -A",
				Log:       "checking workloads",
			},
			Observation: strings.Repeat("x", obsChars),
			Status:      ToolStatusSuccess,
		})
	}
	return steps
}

// TestE2E_ScratchpadCompression_NoCardWhenUnderWindow is the regression guard
// for #32987. A multi-step investigation (more than recentStepsFullContext=10
// steps) whose total scratchpad is far below the model's context window must
// produce ZERO context_compression visibility cards — proving compression is no
// longer triggered by step count alone. Summarization is enabled so a count of
// 0 means "the gate did not fire", not "the feature is off".
func TestE2E_ScratchpadCompression_NoCardWhenUnderWindow(t *testing.T) {
	env := loadCompressionE2EEnv(t)

	prev := config.Config.LlmServerScratchpadSummarizationEnabled
	config.Config.LlmServerScratchpadSummarizationEnabled = true
	t.Cleanup(func() { config.Config.LlmServerScratchpadSummarizationEnabled = prev })

	fx := newCompressionFixture(t, env, false)

	tokens := resolveMaxContextTokens(fx.ctx, env.account, "", fx.convID)
	activation, hardCap := scratchpadBudget(tokens)
	t.Logf("window: tokens=%d activation=%d hardCap=%d", tokens, activation, hardCap)

	// 12 steps (> the 10 recent-steps threshold) of small observations: well
	// under any activation budget (window-derived or legacy fallback).
	steps := makeToolSteps(12, 600)
	out := ConstructScratchPad(steps, ScratchpadContext{Ctx: fx.ctx, Request: fx.req})
	require.NotEmpty(t, out)

	// None of the steps should have been marked compressed.
	for i, s := range steps {
		assert.Empty(t, s.CompressedObservation, "step %d must not be compressed under window", i)
	}

	got := CountCompressionVisibilityRecords(fx.convID)
	assert.Equal(t, 0, got,
		"a sub-window investigation with >10 steps must persist no context_compression cards")
}

// TestE2E_ScratchpadCompression_CardWhenOverWindow is the positive end-to-end:
// when the scratchpad genuinely approaches the context window, older
// observations are compressed and exactly that fact is persisted as a
// context_compression card readable via CountCompressionVisibilityRecords.
//
// To keep the test deterministic and cheap regardless of the resolved window
// size, it lowers the activation fraction (a real, supported knob) so the
// window-relative budget is small, then sizes the observations to exceed the
// resulting activation. This exercises the same window-gated code path — just
// at a lower threshold — and bounds the number of real LLM summarization calls.
func TestE2E_ScratchpadCompression_CardWhenOverWindow(t *testing.T) {
	env := loadCompressionE2EEnv(t)

	prevSummarize := config.Config.LlmServerScratchpadSummarizationEnabled
	prevFraction := config.Config.LlmServerScratchpadCompressionActivationFraction
	config.Config.LlmServerScratchpadSummarizationEnabled = true
	config.Config.LlmServerScratchpadCompressionActivationFraction = 0.02
	t.Cleanup(func() {
		config.Config.LlmServerScratchpadSummarizationEnabled = prevSummarize
		config.Config.LlmServerScratchpadCompressionActivationFraction = prevFraction
	})

	fx := newCompressionFixture(t, env, false)

	tokens := resolveMaxContextTokens(fx.ctx, env.account, "", fx.convID)
	activation, hardCap := scratchpadBudget(tokens)
	t.Logf("window: tokens=%d activation=%d hardCap=%d (fraction=0.02)", tokens, activation, hardCap)

	// Size observations under the per-observation hard cap, then add enough
	// steps to exceed `activation` with margin so at least one older step is
	// compressed (and its visibility card written).
	perObs := getMaxObservationChars() - 4096
	if perObs > 60000 {
		perObs = 60000
	}
	nSteps := activation/perObs + 4
	t.Logf("building %d steps of %d chars (~%d total) vs activation %d",
		nSteps, perObs, nSteps*perObs, activation)

	steps := makeToolSteps(nSteps, perObs)
	out := ConstructScratchPad(steps, ScratchpadContext{Ctx: fx.ctx, Request: fx.req})
	require.NotEmpty(t, out)

	// At least one of the older steps must have been compressed.
	compressed := 0
	for _, s := range steps {
		if s.CompressedObservation != "" {
			compressed++
		}
	}
	require.Greater(t, compressed, 0,
		"scratchpad over the activation budget must compress at least one older observation")

	got := CountCompressionVisibilityRecords(fx.convID)
	assert.GreaterOrEqual(t, got, 1,
		"compression under window pressure must persist a context_compression card")
}

// TestE2E_ScratchpadCompression_KeepRowForUI is a manual helper: it creates a
// COMPLETED conversation with a real context_compression card and DOES NOT
// delete it, logging the session_id / conversation_id so the row can be opened
// in the UI to eyeball the compression card. It is skipped unless
// COMPRESSION_E2E_KEEP=1 so normal e2e runs leave no orphaned rows behind.
//
// Run:
//
//	COMPRESSION_E2E_KEEP=1 go test -tags=e2e -v \
//	  -run TestE2E_ScratchpadCompression_KeepRowForUI ./agents/core/...
func TestE2E_ScratchpadCompression_KeepRowForUI(t *testing.T) {
	if os.Getenv("COMPRESSION_E2E_KEEP") != "1" {
		t.Skip("set COMPRESSION_E2E_KEEP=1 to create a persistent conversation for UI inspection")
	}
	env := loadCompressionE2EEnv(t)

	prevSummarize := config.Config.LlmServerScratchpadSummarizationEnabled
	prevFraction := config.Config.LlmServerScratchpadCompressionActivationFraction
	config.Config.LlmServerScratchpadSummarizationEnabled = true
	config.Config.LlmServerScratchpadCompressionActivationFraction = 0.02
	t.Cleanup(func() {
		config.Config.LlmServerScratchpadSummarizationEnabled = prevSummarize
		config.Config.LlmServerScratchpadCompressionActivationFraction = prevFraction
	})

	fx := newCompressionFixture(t, env, true) // keep = true: row is NOT deleted

	tokens := resolveMaxContextTokens(fx.ctx, env.account, "", fx.convID)
	activation, _ := scratchpadBudget(tokens)
	perObs := getMaxObservationChars() - 4096
	if perObs > 60000 {
		perObs = 60000
	}
	nSteps := activation/perObs + 4

	steps := makeToolSteps(nSteps, perObs)
	out := ConstructScratchPad(steps, ScratchpadContext{Ctx: fx.ctx, Request: fx.req})
	require.NotEmpty(t, out)

	got := CountCompressionVisibilityRecords(fx.convID)
	require.GreaterOrEqual(t, got, 1, "expected a persisted context_compression card")

	t.Logf("KEPT CONVERSATION FOR UI INSPECTION:")
	t.Logf("  session_id       : %s   <-- look this up in the UI", fx.sessionID)
	t.Logf("  conversation_id  : %s", fx.convID)
	t.Logf("  compression cards: %d", got)
}
