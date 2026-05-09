package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSearchAgent_Execute1(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	// Get me recent recommendations where savings is more than 1$
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-search-chain-3",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "loki",
			}, {
				SessionId: "ut-search-chain-4",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "release notes of latest version of kubernetes",
			},
			{
				SessionId: "ut-search-chain-5",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Latest trends for agentic workflows",
			},
		}
	for _, tc := range testCases {
		if tc.AccountId == "" {
			continue
		}
		searchAgent := newSearchAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, searchAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, searchAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.Contains(t, resp.Response[0], "References")
	}
}

func TestSearchAgent_Execute2(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	// Get me recent recommendations where savings is more than 1$
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-search-chain-7",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Kubernetes "pull access denied" "authorization failed" docker image loki secrets`,
			},
		}
	for _, tc := range testCases {
		if tc.AccountId == "" {
			continue
		}
		searchAgent := newSearchAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, searchAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, searchAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.Contains(t, resp.Response[0], "References")
	}
}

func TestSearchAgent_SlowDocPage(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-search-slow-doc",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Can you review docs in https://docs.nudgebee.com/docs/features/api/ and let us know if you find issues from usability perspective",
		},
	}
	for _, tc := range testCases {
		if tc.AccountId == "" {
			t.Skip("Skipping test as TEST_ACCOUNT is not set")
		}
		searchAgent := newSearchAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, searchAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, searchAgent.GetName())
		assert.Greater(t, len(resp.Response), 0)
		assert.Contains(t, resp.Response[0], "References")
	}
}
