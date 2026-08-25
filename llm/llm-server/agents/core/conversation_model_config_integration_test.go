package core

import (
	"context"
	"os"
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Postgres-gated round-trip test for the per-conversation model-config flow.
//
//	dispatch(blanket)  → row state + ResolveLLMConfig
//	dispatch(per-tier) → row state + ResolveLLMConfig (per active tier)
//	dispatch(blanket)  → row state + ResolveLLMConfig
//
// Catches what unit tests can't: cache invalidation, JSONB encoding, SQL
// drift, and resolver precedence — all together against a real schema.
//
// Run with:
//
//	RUN_MODEL_CONFIG_INTEGRATION=true \
//	  LLM_SERVER_DB_URL='postgres://...' \
//	  go test -run TestModelConfigRoundTrip ./agents/core/

func skipIfNoModelConfigIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("RUN_MODEL_CONFIG_INTEGRATION") != "true" {
		t.Skip("set RUN_MODEL_CONFIG_INTEGRATION=true to run (needs Postgres with V746 applied)")
	}
	if os.Getenv("LLM_SERVER_DB_URL") == "" {
		t.Skip("LLM_SERVER_DB_URL not set")
	}
	if os.Getenv("TEST_TENANT") == "" || os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" {
		t.Skip("TEST_TENANT, TEST_ACCOUNT, TEST_USER required (must reference existing rows; FK-enforced)")
	}
	if _, err := common.GetDatabaseManager(common.Metastore); err != nil {
		t.Skipf("metastore unreachable: %v", err)
	}
}

func TestModelConfigRoundTrip(t *testing.T) {
	skipIfNoModelConfigIntegration(t)

	dao := GetConversationDao()
	require.NotNil(t, dao, "conversation DAO must be initialised")

	// FK-enforced uuid columns — reuse known good rows from env.
	tenantID := os.Getenv("TEST_TENANT")
	accountID := os.Getenv("TEST_ACCOUNT")
	userID := os.Getenv("TEST_USER")
	// Unique per run for safe cleanup.
	sessionID := "rtt-session-" + uuid.NewString()
	conversationID := uuid.NewString()

	// Seed a bare conversation row (no model config yet). Each dispatch
	// edits it in place, exactly like the chat flow would.
	_, err := dao.SaveConversation(
		conversationID, sessionID, tenantID, accountID, userID,
		"", "round-trip-test",
		ConversationStatusCompleted, ConversationSourceUserInvestigation,
		"", "", nil, "",
	)
	require.NoError(t, err, "seed conversation")
	// Cleanup unless KEEP_TEST_CONVERSATION=true — useful when the operator
	// wants the row to persist so they can open it in the UI to verify the
	// per-tier model config flows end-to-end.
	if os.Getenv("KEEP_TEST_CONVERSATION") != "true" {
		t.Cleanup(func() {
			dbms, err := common.GetDatabaseManager(common.Metastore)
			if err != nil {
				return
			}
			_, _ = dbms.Db.Exec(`DELETE FROM llm_conversations WHERE id = $1`, conversationID)
		})
	} else {
		t.Logf("KEEP_TEST_CONVERSATION=true — leaving row in place. conversation_id=%s session_id=%s", conversationID, sessionID)
	}

	ctx := security.NewRequestContextForSuperAdmin()

	// --- Round 1: dispatch blanket. Row should hold provider+model; tier NULL.
	applyConversationModelConfig(ctx, dao, conversationID, "openai", "gpt-4o-mini", ConversationTierOverrides{}, "", false)

	conv, err := dao.GetConversation(conversationID)
	require.NoError(t, err)
	assertBlanket(t, conv, "openai", "gpt-4o-mini")
	assertNoTierOverrides(t, conv)

	// Resolver should pick the blanket from this conversation (untagged call).
	res, err := ResolveLLMConfig(ctx, accountID, "k8s_debug", conversationID)
	require.NoError(t, err)
	assert.Equal(t, "openai", res.Provider)
	assert.Equal(t, "gpt-4o-mini", res.Model)
	assert.True(t, res.IsOverridden, "blanket conversation pick must be flagged as override")

	// --- Round 2: dispatch tier picks. Blanket must clear; tier_overrides must persist.
	tierPicks := ConversationTierOverrides{Picks: map[string]TierModelPick{
		string(ModelTierReasoning): {Provider: "googleai", Model: "gemini-2.5-pro"},
		string(ModelTierRetrieval): {Provider: "openai", Model: "gpt-4o-mini"},
	}}
	applyConversationModelConfig(ctx, dao, conversationID, "", "", tierPicks, "", false)

	conv, err = dao.GetConversation(conversationID)
	require.NoError(t, err)
	assertNoBlanket(t, conv)
	assertTierOverrides(t, conv, tierPicks)

	// Resolver, scoped to the reasoning tier, should pick the per-tier model.
	ctxReason := withTier(ctx, ModelTierReasoning)
	res, err = ResolveLLMConfig(ctxReason, accountID, "k8s_debug", conversationID)
	require.NoError(t, err)
	assert.Equal(t, "googleai", res.Provider)
	assert.Equal(t, "gemini-2.5-pro", res.Model)

	ctxRetrieve := withTier(ctx, ModelTierRetrieval)
	res, err = ResolveLLMConfig(ctxRetrieve, accountID, "k8s_debug", conversationID)
	require.NoError(t, err)
	assert.Equal(t, "openai", res.Provider)
	assert.Equal(t, "gpt-4o-mini", res.Model)

	// --- Round 3: dispatch blanket again. Tier must clear; blanket replaces.
	applyConversationModelConfig(ctx, dao, conversationID, "anthropic", "claude-opus-4-7", ConversationTierOverrides{}, "", false)

	conv, err = dao.GetConversation(conversationID)
	require.NoError(t, err)
	assertBlanket(t, conv, "anthropic", "claude-opus-4-7")
	assertNoTierOverrides(t, conv)

	res, err = ResolveLLMConfig(ctx, accountID, "k8s_debug", conversationID)
	require.NoError(t, err)
	assert.Equal(t, "anthropic", res.Provider)
	assert.Equal(t, "claude-opus-4-7", res.Model)
}

// --- assertion helpers (concise, read-once-per-failure) -----------------------

func assertBlanket(t *testing.T, conv Conversation, provider, model string) {
	t.Helper()
	require.NotNil(t, conv.LlmProvider, "llm_provider must be set")
	require.NotNil(t, conv.LlmModel, "llm_model must be set")
	assert.Equal(t, provider, *conv.LlmProvider)
	assert.Equal(t, model, *conv.LlmModel)
}

func assertNoBlanket(t *testing.T, conv Conversation) {
	t.Helper()
	if conv.LlmProvider != nil {
		assert.Empty(t, *conv.LlmProvider, "llm_provider must be NULL or empty")
	}
	if conv.LlmModel != nil {
		assert.Empty(t, *conv.LlmModel, "llm_model must be NULL or empty")
	}
}

func assertTierOverrides(t *testing.T, conv Conversation, want ConversationTierOverrides) {
	t.Helper()
	require.NotNil(t, conv.LlmTierOverrides, "llm_tier_overrides must be loaded")
	assert.Equal(t, want.Picks, conv.LlmTierOverrides.Picks)
}

func assertNoTierOverrides(t *testing.T, conv Conversation) {
	t.Helper()
	if conv.LlmTierOverrides == nil {
		return
	}
	assert.False(t, conv.LlmTierOverrides.HasAny(), "llm_tier_overrides must be NULL or empty")
}

func withTier(ctx *security.RequestContext, tier ModelTier) *security.RequestContext {
	goCtx := ctx.GetContext()
	if goCtx == nil {
		goCtx = context.Background()
	}
	return security.NewRequestContext(
		context.WithValue(goCtx, ContextKeyModelTier, tier),
		ctx.GetSecurityContext(), ctx.GetLogger(), nil, nil,
	)
}
