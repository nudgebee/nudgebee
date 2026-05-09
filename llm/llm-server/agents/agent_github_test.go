package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestGithubAgent_HasCodeAgentTool(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_ACCOUNT")
	githubAgent := newGithubAgent(accountId)

	supportedTools := githubAgent.GetSupportedTools(sc)
	toolNames := make([]string, len(supportedTools))
	for i, tool := range supportedTools {
		toolNames[i] = tool.Name()
	}

	assert.Contains(t, toolNames, "github_execute", "should have github_execute tool")
	assert.Contains(t, toolNames, AgentCode2, "should have agent_code_2 tool for code fixes and PR creation")
}

// TODO mock DBs
// TODO mock Tool Execution
func TestGithubDebugAgent_Execute(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-github-chain-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "show me repos I have access...",
			},
		}
	for _, tc := range testCases {
		githubChain := newGithubAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, githubChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)

		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)
		if resp.Status == core.ConversationStatusWaiting {
			resp, err = core.HandleConversationSessionRequest(sc, githubChain, tc.UserId, tc.AccountId, tc.SessionId, "GithubVans", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

		assert.Equal(t, resp.AgentName, githubChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestGithubDebugAgent_Debugging(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-github-chain-2",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Migrations-Test-Civo CI has failed &lt;|View Workflow&gt; <!subteam^S068VH1B9JA> (Linked Repo <https://github.com/nudgebee/nudgebee|nudgebee/nudgebee>)",
			},
		}
	for _, tc := range testCases {
		githubChain := newGithubAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, githubChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)

		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)
		if resp.Status == core.ConversationStatusWaiting {
			resp, err = core.HandleConversationSessionRequest(sc, githubChain, tc.UserId, tc.AccountId, tc.SessionId, "Kankshit", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

		assert.Equal(t, resp.AgentName, githubChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestGithubDebugAgent_LogProcessing(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-github-chain-3",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Get the full log output for run ID 19135807546 in repository nudgebee/nudgebee.",
			},
		}
	for _, tc := range testCases {
		githubChain := newGithubAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, githubChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)

		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)
		if resp.Status == core.ConversationStatusWaiting {
			resp, err = core.HandleConversationSessionRequest(sc, githubChain, tc.UserId, tc.AccountId, tc.SessionId, "Kankshit", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

		assert.Equal(t, resp.AgentName, githubChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}
