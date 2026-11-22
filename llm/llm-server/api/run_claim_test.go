package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// claimTestPolicy turns any claim into a profile pinned to the claimed agent.
type claimTestPolicy struct {
	core.NoRunPolicy
	claims []core.RunClaim
}

func (p *claimTestPolicy) PrepareRun(_ *security.RequestContext, claim core.RunClaim, _ string) (core.RunProfile, error) {
	p.claims = append(p.claims, claim)
	return core.RunProfile(claim), nil
}

func claimContext(headers map[string]string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/completions/chat", nil)
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	return c
}

var fullClaim = map[string]string{
	core.RunClaimHeaderExtension: "claim_test_ext",
	core.RunClaimHeaderRef:       "run-1",
	core.RunClaimHeaderAgent:     "pinned_agent",
}

// A run claim becomes the new conversation's profile, and only with the
// service token checked; a conversation the request continues keeps its
// stored profile and ignores any claim; no claim is an ordinary chat.
func TestChatRunProfile(t *testing.T) {
	policy := &claimTestPolicy{}
	core.RegisterAgentRunPolicy("claim_test_ext", policy)
	t.Cleanup(func() { core.RegisterAgentRunPolicy("claim_test_ext", nil) })
	prevToken := config.Config.LlmServerToken
	t.Cleanup(func() { config.Config.LlmServerToken = prevToken })
	ctx := security.NewRequestContextForSuperAdmin()
	account := uuid.NewString()

	config.Config.LlmServerToken = ""
	_, _, err := chatRunProfile(claimContext(fullClaim), ctx, nil, account)
	require.ErrorIs(t, err, errRunClaimNeedsServiceToken, "a claim is never ignored: without the token it is refused")
	_, _, err = chatRunProfile(claimContext(map[string]string{core.RunClaimHeaderRef: "run-1"}), ctx, nil, account)
	require.ErrorIs(t, err, errRunClaimNeedsServiceToken, "any one claim header counts as a claim")
	profile, created, err := chatRunProfile(claimContext(nil), ctx, nil, account)
	require.NoError(t, err)
	assert.Nil(t, profile, "no claim: an ordinary chat, token or not")
	assert.Nil(t, created)

	config.Config.LlmServerToken = "service-token"
	profile, created, err = chatRunProfile(claimContext(fullClaim), ctx, nil, account)
	require.NoError(t, err)
	want := &core.RunProfile{Extension: "claim_test_ext", Ref: "run-1", Agent: "pinned_agent"}
	assert.Equal(t, want, profile)
	assert.Equal(t, want, created, "written on the new conversation")

	stored := &core.RunProfile{Extension: "claim_test_ext", Ref: "run-0", Agent: "first_agent"}
	existing := &core.Conversation{ID: uuid.New(), RunProfile: stored}
	profile, created, err = chatRunProfile(claimContext(fullClaim), ctx, existing, account)
	require.NoError(t, err)
	assert.Same(t, stored, profile, "the row's profile wins over a claim")
	assert.Nil(t, created, "an existing conversation is never re-stamped")
	profile, _, err = chatRunProfile(claimContext(fullClaim), ctx, &core.Conversation{ID: uuid.New()}, account)
	require.NoError(t, err)
	assert.Nil(t, profile, "a claim cannot turn an ordinary conversation into a governed run")

	_, _, err = chatRunProfile(claimContext(map[string]string{core.RunClaimHeaderExtension: "no_such_ext", core.RunClaimHeaderRef: "r", core.RunClaimHeaderAgent: "a"}), ctx, nil, account)
	require.Error(t, err, "an unknown extension is refused")
	assert.Len(t, policy.claims, 1)
}

// namedAgent is an agent known only by its name.
type namedAgent struct {
	core.NBAgent
	name string
}

func (a namedAgent) GetName() string { return a.name }

// A governed run's turn runs the profile's agent, whatever was chosen for it
// before (the agent an @mention or the history picked); any other turn keeps
// the agent it had.
func TestPinRunAgent(t *testing.T) {
	prev := getRunAgent
	t.Cleanup(func() { getRunAgent = prev })
	var looked []string
	getRunAgent = func(_ *security.RequestContext, name, _ string, _ core.AgentStatus) (core.NBAgent, bool) {
		looked = append(looked, name)
		return namedAgent{name: name}, name == "pinned_agent"
	}
	ctx := security.NewRequestContextForSuperAdmin()
	mentioned := namedAgent{name: "mentioned_agent"}
	profile := &core.RunProfile{Extension: "claim_test_ext", Ref: "run-1", Agent: "pinned_agent"}

	agent, err := pinRunAgent(ctx, profile, "acc", mentioned)
	require.NoError(t, err)
	assert.Equal(t, "pinned_agent", agent.GetName(), "the pin replaces the agent an @mention or the history chose")
	agent, err = pinRunAgent(ctx, profile, "acc", nil)
	require.NoError(t, err)
	assert.Equal(t, "pinned_agent", agent.GetName(), "and stands in for routing")
	assert.Equal(t, []string{"pinned_agent", "pinned_agent"}, looked, "only the profile's agent is looked up")

	agent, err = pinRunAgent(ctx, nil, "acc", mentioned)
	require.NoError(t, err)
	assert.Equal(t, mentioned, agent, "an ordinary turn keeps its agent")

	_, err = pinRunAgent(ctx, &core.RunProfile{Extension: "claim_test_ext", Ref: "run-1", Agent: "gone"}, "acc", mentioned)
	assert.ErrorIs(t, err, errRunAgentNotFound, "a missing pinned agent is refused, not replaced by the mention")
}
