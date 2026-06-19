//go:build e2e

package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TODO mock DBs
// TODO mock Tool Execution
func TestConversationalAgent(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_CONVERSATIONAL_AGENT_ACCOUNT")
	userId := os.Getenv("TEST_CONVERSATIONAL_AGENT_USER")
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-conversational-chain-1",
				AccountId: accountId,
				UserId:    userId,
				Query:     "what is my current cluster mem usage?",
			},
			{
				SessionId: "ut-conversational-chain-1",
				AccountId: accountId,
				UserId:    userId,
				Query:     "Provide the ans in human readable format",
			},
		}
	for _, tc := range testCases {
		k8sDebugAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sDebugAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sDebugAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestConversationalAgentMultiMessages(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_CONVERSATIONAL_AGENT_ACCOUNT")
	userId := os.Getenv("TEST_CONVERSATIONAL_AGENT_USER")
	sessionId := "ut-conversational-chain-2"
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: sessionId,
				AccountId: accountId,
				UserId:    userId,
				Query:     "Can you show pods in Nudgebee namespace",
			},
			{
				SessionId: sessionId,
				AccountId: accountId,
				UserId:    userId,
				Query:     "Can you show recent restarted pods in nudgebee namespace",
			},
		}
	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	for _, tc := range testCases {
		prometheusChain := newPrometheusAgent(accountId)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestConversationalAgentMultiMessagesWithPreviousContext(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_CONVERSATIONAL_AGENT_ACCOUNT")
	userId := os.Getenv("TEST_CONVERSATIONAL_AGENT_USER")
	sessionId := "ut-conversational-chain-3"
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: sessionId,
				AccountId: accountId,
				UserId:    userId,
				Query:     "Can you show memory usage of app-dev in nudgebee namespace",
			},
			{
				SessionId: sessionId,
				AccountId: accountId,
				UserId:    userId,
				Query:     "Can you show logs as well",
			},
		}
	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	for _, tc := range testCases {
		k8sDebug := newK8sDebugAgent(tc.AccountId)

		resp, err := core.HandleConversationSessionRequest(sc, k8sDebug, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sDebug.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

// TestConversationalAgentModelConfigRoundTrip exercises the per-conversation
// model-config dispatch through the real chat-flow entrypoint. Each call
// goes through HandleConversationSessionRequest exactly like the UI does,
// so the conversation appears in the user's chat list and the picker can
// be flipped against it.
//
// Three rounds: blanket → per-tier → blanket. After each call we re-read
// the row and assert which side of the mutual-exclusivity columns is set.
// The conversation is intentionally not deleted: open it in the UI to
// verify the picker reflects the persisted state.
func TestConversationalAgentModelConfigRoundTrip(t *testing.T) {
	accountId := os.Getenv("TEST_CONVERSATIONAL_AGENT_ACCOUNT")
	userId := os.Getenv("TEST_CONVERSATIONAL_AGENT_USER")
	if accountId == "" || userId == "" {
		t.Skip("TEST_CONVERSATIONAL_AGENT_ACCOUNT / TEST_CONVERSATIONAL_AGENT_USER required")
	}

	sc := security.NewRequestContextForSuperAdmin()
	sessionId := "rtt-modelcfg-" + uuid.NewString()
	t.Logf("session_id=%s account=%s user=%s — open this in the UI after the test runs", sessionId, accountId, userId)

	k8sDebugAgent := newK8sDebugAgent(accountId)

	// Round 1: blanket model on the very first turn — creates the conversation.
	// Queries are k8s-shaped so the k8s_debug agent actually plans + executes
	// instead of giving up on an off-topic question.
	blanket1 := toolcore.NBQueryConfig{LlmProvider: "googleai", LlmModelName: "gemini-2.5-flash"}
	resp, err := core.HandleConversationSessionRequest(sc, k8sDebugAgent, userId, accountId, sessionId, "Hi there, who are you?",
		core.ConversationSessionRequestWithConfig(blanket1))
	require.NoError(t, err, "round-1 (blanket) chat call failed")
	require.NotNil(t, resp)

	conv, err := core.GetConversationDao().GetConversationBySession(accountId, sessionId)
	require.NoError(t, err)
	require.NotNil(t, conv.LlmProvider, "round-1: llm_provider must be persisted")
	require.NotNil(t, conv.LlmModel, "round-1: llm_model must be persisted")
	assert.Equal(t, "googleai", *conv.LlmProvider)
	assert.Equal(t, "gemini-2.5-flash", *conv.LlmModel)
	if conv.LlmTierOverrides != nil {
		assert.False(t, conv.LlmTierOverrides.HasAny(), "round-1: tier_overrides must be NULL/empty")
	}

	// conversation_id is required on follow-up turns: the dispatch runs only
	// when handleConversationRequest can load an existing row via the id.
	convIDNull := uuid.NullUUID{UUID: conv.ID, Valid: true}

	// Round 2: per-tier picks on a follow-up turn — must clear blanket.
	tierPicks := map[string]toolcore.TierModelPick{
		"reasoning": {Provider: "googleai", Model: "gemini-2.5-flash"},
		"retrieval": {Provider: "googleai", Model: "gemini-2.5-flash"},
	}
	_, err = core.HandleConversationSessionRequest(sc, k8sDebugAgent, userId, accountId, sessionId, "What is 2 plus 2?",
		core.ConversationSessionRequestWithConversationId(convIDNull),
		core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{LlmTierModels: tierPicks}))
	require.NoError(t, err, "round-2 (per-tier) chat call failed")

	conv, err = core.GetConversationDao().GetConversationBySession(accountId, sessionId)
	require.NoError(t, err)
	require.NotNil(t, conv.LlmTierOverrides, "round-2: tier_overrides must be persisted")
	assert.True(t, conv.LlmTierOverrides.HasAny())
	assert.Equal(t, "googleai", conv.LlmTierOverrides.Picks["reasoning"].Provider)
	assert.Equal(t, "gemini-2.5-flash", conv.LlmTierOverrides.Picks["reasoning"].Model)
	assert.Equal(t, "googleai", conv.LlmTierOverrides.Picks["retrieval"].Provider)
	assert.Equal(t, "gemini-2.5-flash", conv.LlmTierOverrides.Picks["retrieval"].Model)
	if conv.LlmProvider != nil {
		assert.Empty(t, *conv.LlmProvider, "round-2: llm_provider must be cleared")
	}
	if conv.LlmModel != nil {
		assert.Empty(t, *conv.LlmModel, "round-2: llm_model must be cleared")
	}

	// Round 3: blanket again — must clear tier_overrides. Real model name
	// (gemini-2.5-flash), distinct from Round 1 so we can see the change in the
	// row.
	blanket2 := toolcore.NBQueryConfig{LlmProvider: "googleai", LlmModelName: "gemini-2.5-flash"}
	_, err = core.HandleConversationSessionRequest(sc, k8sDebugAgent, userId, accountId, sessionId, "Tell me a one-line fun fact",
		core.ConversationSessionRequestWithConversationId(convIDNull),
		core.ConversationSessionRequestWithConfig(blanket2))
	require.NoError(t, err, "round-3 (blanket again) chat call failed")

	conv, err = core.GetConversationDao().GetConversationBySession(accountId, sessionId)
	require.NoError(t, err)
	require.NotNil(t, conv.LlmProvider)
	require.NotNil(t, conv.LlmModel)
	assert.Equal(t, "googleai", *conv.LlmProvider)
	assert.Equal(t, "gemini-2.5-flash", *conv.LlmModel)
	if conv.LlmTierOverrides != nil {
		assert.False(t, conv.LlmTierOverrides.HasAny(), "round-3: tier_overrides must be cleared")
	}

	t.Logf("DONE — open session %q in the UI; picker should reopen in All-calls mode with model %s",
		sessionId, "gemini-2.5-flash")
}

// TestGemini25SingleQuestion sends ONE simple question to the k8s_debug
// agent and reports whether the LLM returned content. Model is read from
// NB_PROBE_MODEL env var so we can run the same flow against different
// models without code changes.
//
//	NB_PROBE_MODEL=gemini-2.5-flash go test -tags=e2e -run TestGemini25SingleQuestion ./agents -v
//	NB_PROBE_MODEL=gemini-2.5-pro   go test -tags=e2e -run TestGemini25SingleQuestion ./agents -v
//	NB_PROBE_MODEL=gemini-3-flash-preview go test -tags=e2e -run TestGemini25SingleQuestion ./agents -v
func TestGemini25SingleQuestion(t *testing.T) {
	model := os.Getenv("NB_PROBE_MODEL")
	if model == "" {
		t.Skip("set NB_PROBE_MODEL to the model to probe")
	}
	accountId := os.Getenv("TEST_CONVERSATIONAL_AGENT_ACCOUNT")
	userId := os.Getenv("TEST_CONVERSATIONAL_AGENT_USER")
	if accountId == "" || userId == "" {
		t.Skip("TEST_CONVERSATIONAL_AGENT_ACCOUNT / TEST_CONVERSATIONAL_AGENT_USER required")
	}

	sc := security.NewRequestContextForSuperAdmin()
	sessionId := "probe-" + model + "-" + uuid.NewString()
	t.Logf("model=%s session_id=%s", model, sessionId)

	resp, err := core.HandleConversationSessionRequest(sc, newK8sDebugAgent(accountId), userId, accountId, sessionId,
		"Hi there, what can you help me with?",
		core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{LlmProvider: "googleai", LlmModelName: model}))
	require.NoError(t, err, "chat call failed")
	require.NotNil(t, resp)

	conv, err := core.GetConversationDao().GetConversationBySession(accountId, sessionId)
	require.NoError(t, err)

	respLen := len(resp.Response)
	t.Logf("RESULT model=%s response_len=%d status=%s", model, respLen, conv.Status)
	if respLen == 0 {
		t.Errorf("EMPTY response from %s", model)
	}
}
