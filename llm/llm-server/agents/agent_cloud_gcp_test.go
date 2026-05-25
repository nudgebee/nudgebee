package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestGCPAgent_Execute(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-gcp-agent-1",
			AccountId: os.Getenv("TEST_GCP_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "list all compute engine instances in project nudgebee-dev",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			gcpAgent := newGcpAgent(tc.AccountId)
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.NoError(t, err, "Cleanup failed for session %s", tc.SessionId)

			resp, err := core.HandleConversationSessionRequest(sc, gcpAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.NoError(t, err, "Failed to handle conversation session")
			assert.NotNil(t, resp, "Response should not be nil")

			assert.Equal(t, resp.AgentName, gcpAgent.GetName(), "Agent name mismatch")

			assert.NotEmpty(t, resp.Query, "Query should not be empty")

			assert.NotNil(t, resp.AgentStepResponse, "Agent step response should not be nil")

			assert.Greater(t, len(resp.Response), 0, "Response should not be empty")
		})
	}
}

func TestGCPAgent_ExecuteCrossAccount(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-gcp-agent-1-1",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "list all compute engine instances in project nudgebee-dev.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			gcpAgent := newGcpAgent(tc.AccountId)
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.NoError(t, err, "Cleanup failed for session %s", tc.SessionId)

			resp, err := core.HandleConversationSessionRequest(sc, gcpAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.NoError(t, err, "Failed to handle conversation session")
			assert.NotNil(t, resp, "Response should not be nil")

			if resp.Status == core.ConversationStatusWaiting {
				messageId, err := uuid.Parse(resp.MessageId)
				assert.Nil(t, err)

				agentId, err := uuid.Parse(resp.AgentId)
				assert.Nil(t, err)
				resp, err = core.HandleConversationSessionRequest(sc, gcpAgent, tc.UserId, tc.AccountId, tc.SessionId, "gcp-dev - nudgebee-dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
					UUID:  messageId,
					Valid: true,
				}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
					UUID:  agentId,
					Valid: true,
				}))
				assert.Nil(t, err)

			}

			assert.Equal(t, resp.AgentName, gcpAgent.GetName(), "Agent name mismatch")

			assert.NotEmpty(t, resp.Query, "Query should not be empty")

			assert.NotNil(t, resp.AgentStepResponse, "Agent step response should not be nil")

			assert.Greater(t, len(resp.Response), 0, "Response should not be empty")
		})
	}
}

func TestGCPAgent_Execute2(t *testing.T) {
	gcpAgent := GcpAgent{}
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-gcp-agent-2",
			AccountId: os.Getenv("TEST_GCP_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "list all storage buckets in project my-gcp-project",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.NoError(t, err, "Cleanup failed for session %s", tc.SessionId)

			resp, err := core.HandleConversationSessionRequest(sc, gcpAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.NoError(t, err, "Failed to handle conversation session")
			assert.NotNil(t, resp, "Response should not be nil")

			assert.Equal(t, resp.AgentName, gcpAgent.GetName(), "Agent name mismatch")

			assert.NotEmpty(t, resp.Query, "Query should not be empty")

			assert.NotNil(t, resp.AgentStepResponse, "Agent step response should not be nil")

			assert.Greater(t, len(resp.Response), 0, "Response should not be empty")
		})
	}
}

func TestGCPAgent_Execute3(t *testing.T) {
	gcpAgent := GcpAgent{}
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-gcp-agent-3",
			AccountId: os.Getenv("TEST_GCP_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "list all cloud functions in project my-gcp-project and region us-east1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.NoError(t, err, "Cleanup failed for session %s", tc.SessionId)

			resp, err := core.HandleConversationSessionRequest(sc, gcpAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.NoError(t, err, "Failed to handle conversation session")
			assert.NotNil(t, resp, "Response should not be nil")

			assert.Equal(t, resp.AgentName, gcpAgent.GetName(), "Agent name mismatch")

			assert.NotEmpty(t, resp.Query, "Query should not be empty")

			assert.NotNil(t, resp.AgentStepResponse, "Agent step response should not be nil")

			assert.Greater(t, len(resp.Response), 0, "Response should not be empty")
		})
	}
}

func TestGCPAgent_Execute4(t *testing.T) {
	gcpAgent := GcpAgent{}
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-gcp-agent-4",
			AccountId: os.Getenv("TEST_GCP_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "list all GKE clusters in project my-gcp-project and zone us-central1-a",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.NoError(t, err, "Cleanup failed for session %s", tc.SessionId)

			resp, err := core.HandleConversationSessionRequest(sc, gcpAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.NoError(t, err, "Failed to handle conversation session")
			assert.NotNil(t, resp, "Response should not be nil")

			assert.Equal(t, resp.AgentName, gcpAgent.GetName(), "Agent name mismatch")

			assert.NotEmpty(t, resp.Query, "Query should not be empty")

			assert.NotNil(t, resp.AgentStepResponse, "Agent step response should not be nil")

			assert.Greater(t, len(resp.Response), 0, "Response should not be empty")
		})
	}
}
