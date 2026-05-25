package aws

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestAWSAgent_Execute(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-aws-agent-1",
			AccountId: os.Getenv("TEST_AWS_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "get me buckets is us-east-1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			awsAgent := newAwsAgent(tc.AccountId)
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.NoError(t, err, "Cleanup failed for session %s", tc.SessionId)

			resp, err := core.HandleConversationSessionRequest(sc, awsAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.NoError(t, err, "Failed to handle conversation session")
			assert.NotNil(t, resp, "Response should not be nil")

			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			if resp.Status == core.ConversationStatusWaiting {
				resp, err = core.HandleConversationSessionRequest(sc, awsAgent, tc.UserId, tc.AccountId, tc.SessionId, "aws-dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
					UUID:  messageId,
					Valid: true,
				}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
					UUID:  agentId,
					Valid: true,
				}))
				assert.Nil(t, err)
				assert.NotNil(t, resp)
			}

			assert.Equal(t, resp.AgentName, awsAgent.GetName(), "Agent name mismatch")

			assert.NotEmpty(t, resp.Query, "Query should not be empty")

			assert.NotNil(t, resp.AgentStepResponse, "Agent step response should not be nil")

			assert.Greater(t, len(resp.Response), 0, "Response should not be empty")
		})
	}
}

func TestAWSAgent_Execute2(t *testing.T) {
	awsAgent := AwsAgent{}
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-aws-agent-2",
			AccountId: os.Getenv("TEST_AWS_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "can you check ECR and how many repos dont have secret scanning enabled",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.NoError(t, err, "Cleanup failed for session %s", tc.SessionId)

			resp, err := core.HandleConversationSessionRequest(sc, awsAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.NoError(t, err, "Failed to handle conversation session")
			assert.NotNil(t, resp, "Response should not be nil")

			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			if resp.Status == core.ConversationStatusWaiting {
				resp, err = core.HandleConversationSessionRequest(sc, awsAgent, tc.UserId, tc.AccountId, tc.SessionId, "aws-dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
					UUID:  messageId,
					Valid: true,
				}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
					UUID:  agentId,
					Valid: true,
				}))
				assert.Nil(t, err)
				assert.NotNil(t, resp)
			}

			assert.Equal(t, resp.AgentName, awsAgent.GetName(), "Agent name mismatch")

			assert.NotEmpty(t, resp.Query, "Query should not be empty")

			assert.NotNil(t, resp.AgentStepResponse, "Agent step response should not be nil")

			assert.Greater(t, len(resp.Response), 0, "Response should not be empty")
		})
	}
}

func TestAWSAgent_Execute3(t *testing.T) {
	awsAgent := AwsAgent{}
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-aws-agent-3",
			AccountId: os.Getenv("TEST_AWS_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "can you get me billing recomemndations for this account",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.NoError(t, err, "Cleanup failed for session %s", tc.SessionId)

			resp, err := core.HandleConversationSessionRequest(sc, awsAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.NoError(t, err, "Failed to handle conversation session")
			assert.NotNil(t, resp, "Response should not be nil")

			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			if resp.Status == core.ConversationStatusWaiting {
				resp, err = core.HandleConversationSessionRequest(sc, awsAgent, tc.UserId, tc.AccountId, tc.SessionId, "aws-dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
					UUID:  messageId,
					Valid: true,
				}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
					UUID:  agentId,
					Valid: true,
				}))
				assert.Nil(t, err)
				assert.NotNil(t, resp)
			}
			assert.Equal(t, resp.AgentName, awsAgent.GetName(), "Agent name mismatch")

			assert.NotEmpty(t, resp.Query, "Query should not be empty")

			assert.NotNil(t, resp.AgentStepResponse, "Agent step response should not be nil")

			assert.Greater(t, len(resp.Response), 0, "Response should not be empty")
		})
	}
}

func TestAWSAgent_Execute4(t *testing.T) {
	awsAgent := AwsAgent{}
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-aws-agent-4",
			AccountId: os.Getenv("TEST_AWS_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "can you summarize bill of last month based on service usage",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.NoError(t, err, "Cleanup failed for session %s", tc.SessionId)

			resp, err := core.HandleConversationSessionRequest(sc, awsAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.NoError(t, err, "Failed to handle conversation session")
			assert.NotNil(t, resp, "Response should not be nil")

			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			if resp.Status == core.ConversationStatusWaiting {
				resp, err = core.HandleConversationSessionRequest(sc, awsAgent, tc.UserId, tc.AccountId, tc.SessionId, "aws-dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
					UUID:  messageId,
					Valid: true,
				}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
					UUID:  agentId,
					Valid: true,
				}))
				assert.Nil(t, err)
				assert.NotNil(t, resp)
			}
			assert.Equal(t, resp.AgentName, awsAgent.GetName(), "Agent name mismatch")

			assert.NotEmpty(t, resp.Query, "Query should not be empty")

			assert.NotNil(t, resp.AgentStepResponse, "Agent step response should not be nil")

			assert.Greater(t, len(resp.Response), 0, "Response should not be empty")
		})
	}
}

func TestAWSInvestigationAgent(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), nil)
	tests := []struct {
		UserId    string
		AccountId string
		Query     string
		SessionId string
	}{
		{
			UserId:    os.Getenv("TEST_USER"),
			AccountId: os.Getenv("TEST_AWS_ACCOUNT"),
			Query:     "i am having issue with http://demo-frontend-alb-1435077682.us-east-1.elb.amazonaws.com/, I see 500 error. can you investigate?",
			SessionId: "ut-aws-agent-10",
		},
	}

	for _, tc := range tests {
		awsAgentent := AwsAgent{}

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, awsAgentent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, awsAgentent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.Greater(t, len(resp.AgentStepResponse), 0)
	}
}
