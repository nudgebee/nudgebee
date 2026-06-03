//go:build e2e

package api

import (
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestAgents_List(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	agents := core.ListAgents(sc, os.Getenv("TEST_ACCOUNT"), true)
	assert.NotEmpty(t, agents)
}

func TestAgents_CustomAgent(t *testing.T) {

	userId := os.Getenv("TEST_USER")
	accountId := os.Getenv("TEST_ACCOUNT")
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userId, []string{accountId})

	customAgent := core.AgentDto{
		Name:                  "custom_testingagentone",
		Description:           "Custom agent for testing",
		Type:                  core.AgentTypeCustom,
		ExecutorType:          core.AgentPlannerTypeTool,
		Tools:                 []string{},
		SystemPrompt:          `You are expert in Kubernetes/Helm and other related technologies. Provide the answers to questions related to Kubernetes.`,
		SystemPromptVariables: []string{},
	}
	customAgentRag := core.AgentRagDto{
		Data: `question: How do you list all Compute Engine instances in a specific project and zone using the gcloud CLI?
		answer: gcloud compute instances list --project=[YOUR_PROJECT_ID] --zone=[YOUR_ZONE]
		question: Get All projects
		answer: gcloud compute instances list --project=my-awesome-project --zone=us-central1-a`,
		Format:   `text`,
		Filename: `testfile`,
	}

	err := core.DeleteCustomAgent(sc, accountId, customAgent.Name)
	if err != nil && err.Error() != "agent: agent not found" {
		slog.Error("Error deleting custom agent", "error", err)
		return
	}

	customAgent, err = core.CreateCustomAgent(sc, accountId, customAgent, []core.AgentRagDto{customAgentRag}, false)
	assert.Nil(t, err)
	if err != nil {
		slog.Error("Error creating custom agent", "error", err)
		return
	}
	assert.NotEmpty(t, customAgent.Id)

	agents := core.ListCustomAgents(sc, accountId, false)
	assert.NotEmpty(t, agents)

	foundCustomAgent, ok := core.GetCustomNbAgent(sc, accountId, customAgent.Name, "")
	assert.True(t, ok)
	assert.Equal(t, customAgent.Name, foundCustomAgent.GetName())

	agentResponse, err := core.HandleConversationSessionRequest(sc, foundCustomAgent, userId, accountId, "test-custom-agent", "what is pod disruption budget")
	assert.Nil(t, err)
	assert.NotEmpty(t, agentResponse)
}

func TestAgents_CustomAgentUpdate(t *testing.T) {

	userId := os.Getenv("TEST_USER")
	accountId := os.Getenv("TEST_ACCOUNT")
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userId, []string{accountId})

	name := "UpdateAgent"
	description := "UpdateCustom agent for testing123"
	systemPrompt := `You are expert in Kubernetes/Helm and other related technologies. Provide the answers to questions related to Kubernetes.`

	customAgentID := os.Getenv("TEST_CUSTOM_AGENT_ID")
	if customAgentID == "" {
		t.Skip("skipping: TEST_CUSTOM_AGENT_ID not set")
	}
	customAgent := core.AgentUpdateDto{
		Id:                    customAgentID,
		Name:                  &name,
		Description:           &description,
		Tools:                 []string{"tool1", "tool2"},
		SystemPrompt:          &systemPrompt,
		SystemPromptVariables: []string{},
		Status:                "enabled",
		UpdatedBy:             "user@example.com",
		UpdatedAt:             time.Now().UTC(),
	}

	var err error

	customAgent, err = core.UpdateCustomAgent(sc, accountId, customAgent)
	assert.Nil(t, err)
	if err != nil {
		slog.Error("Error updating custom agent", "error", err)
		return
	}
	assert.NotEmpty(t, customAgent.Id)

	agents := core.ListCustomAgents(sc, accountId, false)
	assert.NotEmpty(t, agents)
}

func TestCustomAgent_Execute4(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-custom-agent-1",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "what is 10+1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.NoError(t, err, "Cleanup failed for session %s", tc.SessionId)

			agent, found := core.GetCustomNbAgent(sc, tc.AccountId, "shell_execute_agent", "")
			if !found {
				assert.FailNow(t, "Agent not found")
			}

			resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.NoError(t, err, "Failed to handle conversation session")
			assert.NotNil(t, resp, "Response should not be nil")

			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			if resp.Status == core.ConversationStatusWaiting {
				resp, err = core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query, core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
					UUID:  messageId,
					Valid: true,
				}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
					UUID:  agentId,
					Valid: true,
				}))
				assert.Nil(t, err)
				assert.NotNil(t, resp)
			}
			assert.Equal(t, resp.AgentName, agent.GetName(), "Agent name mismatch")

			assert.NotEmpty(t, resp.Query, "Query should not be empty")

			assert.NotNil(t, resp.AgentStepResponse, "Agent step response should not be nil")

			assert.Greater(t, len(resp.Response), 0, "Response should not be empty")
		})
	}
}

func TestCustomAgent_Execute5(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-custom-agent-2",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "I am observing slowness in the app response, can you investigate",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.NoError(t, err, "Cleanup failed for session %s", tc.SessionId)

			agent, found := core.GetCustomNbAgent(sc, tc.AccountId, "nudgebee_debug", "")
			if !found {
				assert.FailNow(t, "Agent not found")
			}

			resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.NoError(t, err, "Failed to handle conversation session")
			assert.NotNil(t, resp, "Response should not be nil")

			assert.Equal(t, resp.AgentName, agent.GetName(), "Agent name mismatch")

			assert.NotEmpty(t, resp.Query, "Query should not be empty")

			assert.NotNil(t, resp.AgentStepResponse, "Agent step response should not be nil")

			assert.Greater(t, len(resp.Response), 0, "Response should not be empty")
		})
	}
}

func TestCustomAgent_Execute6(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-custom-agent-3",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "@rabbit_debug can you review rabbitmq in rabbit namespace",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.NoError(t, err, "Cleanup failed for session %s", tc.SessionId)

			agent, found := core.GetCustomNbAgent(sc, tc.AccountId, "rabbit_debug", "")
			if !found {
				assert.FailNow(t, "Agent not found")
			}

			resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.NoError(t, err, "Failed to handle conversation session")
			assert.NotNil(t, resp, "Response should not be nil")

			assert.Equal(t, resp.AgentName, agent.GetName(), "Agent name mismatch")

			assert.NotEmpty(t, resp.Query, "Query should not be empty")

			assert.NotNil(t, resp.AgentStepResponse, "Agent step response should not be nil")

			assert.Greater(t, len(resp.Response), 0, "Response should not be empty")
		})
	}
}

func TestCustomAgent_Execute7(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-custom-agent-4",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "@nudgebee_health_analyzer can you check health status",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.NoError(t, err, "Cleanup failed for session %s", tc.SessionId)

			agent, found := core.GetCustomNbAgent(sc, tc.AccountId, "nudgebee_health_analyzer", "")
			if !found {
				assert.FailNow(t, "Agent not found")
			}

			resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

			if resp.Status == core.ConversationStatusWaiting {
				messageId, err := uuid.Parse(resp.MessageId)
				assert.Nil(t, err)

				agentId, err := uuid.Parse(resp.AgentId)
				assert.Nil(t, err)
				resp, err = core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, "dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
					UUID:  messageId,
					Valid: true,
				}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
					UUID:  agentId,
					Valid: true,
				}))
				assert.Nil(t, err)
				assert.NotNil(t, resp)
			}

			if resp.Status == core.ConversationStatusWaiting {
				messageId, err := uuid.Parse(resp.MessageId)
				assert.Nil(t, err)

				agentId, err := uuid.Parse(resp.AgentId)
				assert.Nil(t, err)
				resp, err = core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, "dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
					UUID:  messageId,
					Valid: true,
				}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
					UUID:  agentId,
					Valid: true,
				}))
				assert.Nil(t, err)
				assert.NotNil(t, resp)
			}

			assert.NoError(t, err, "Failed to handle conversation session")
			assert.NotNil(t, resp, "Response should not be nil")

			assert.Equal(t, resp.AgentName, agent.GetName(), "Agent name mismatch")

			assert.NotEmpty(t, resp.Query, "Query should not be empty")

			assert.NotNil(t, resp.AgentStepResponse, "Agent step response should not be nil")

			assert.Greater(t, len(resp.Response), 0, "Response should not be empty")
		})
	}
}

func TestCreateAgentExtension(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})
	userId := os.Getenv("TEST_USER")
	accountId := os.Getenv("TEST_ACCOUNT")

	testCases := []struct {
		name      string
		accountId string
		agent     core.AgentExtension
		createdBy string
		wantErr   bool
	}{
		{
			name:      "redis",
			accountId: accountId,
			agent: core.AgentExtension{
				AgentName: "redis",
				Prompt:    "Test prompt for agent extension",
				Tools:     []string{"tool1", "tool2"},
			},
			createdBy: userId,
			wantErr:   false,
		},
		{
			name:      "Empty account ID",
			accountId: "",
			agent: core.AgentExtension{
				AgentName: "test-agent-extension-2",
				Prompt:    "Test prompt",
				Tools:     []string{"tool1"},
			},
			createdBy: userId,
			wantErr:   true,
		},
		{
			name:      "Empty agent name",
			accountId: accountId,
			agent: core.AgentExtension{
				AgentName: "",
				Prompt:    "Test prompt",
				Tools:     []string{"tool1"},
			},
			createdBy: userId,
			wantErr:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := core.CreateAgentExtension(sc, tc.accountId, tc.agent, tc.createdBy)

			if tc.wantErr {
				assert.Error(t, err, "Expected error for test case: %s", tc.name)
				assert.Equal(t, core.AgentExtension{}, result, "Result should be empty on error")
			} else {
				assert.NoError(t, err, "Unexpected error for test case: %s", tc.name)
				assert.Equal(t, tc.agent.AgentName, result.AgentName, "Agent name should match")
				assert.Equal(t, tc.agent.Prompt, result.Prompt, "Prompt should match")
				assert.Equal(t, tc.agent.Tools, result.Tools, "Tools should match")
			}
		})
	}
}

func TestUpdateAgentExtension(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})
	userId := os.Getenv("TEST_USER")
	accountId := os.Getenv("TEST_ACCOUNT")

	// First create an agent extension to update
	createAgent := core.AgentExtension{
		AgentName: "redis1",
		Prompt:    "Initial prompt for agent extension",
		Tools:     []string{"tool1"},
	}
	_, err := core.CreateAgentExtension(sc, accountId, createAgent, userId)
	assert.NoError(t, err, "Setup: Failed to create agent extension for update test")

	testCases := []struct {
		name      string
		accountId string
		agent     core.AgentExtension
		updatedBy string
		wantErr   bool
	}{
		{
			name:      "Update existing agent extension",
			accountId: accountId,
			agent: core.AgentExtension{
				AgentName: "redis1",
				Prompt:    "Updated prompt for agent extension",
				Tools:     []string{"tool1", "tool2", "tool3"},
			},
			updatedBy: userId,
			wantErr:   false,
		},
		{
			name:      "Empty account ID",
			accountId: "",
			agent: core.AgentExtension{
				AgentName: "redis",
				Prompt:    "Test prompt",
				Tools:     []string{"tool1"},
			},
			updatedBy: userId,
			wantErr:   true,
		},
		{
			name:      "Empty agent name",
			accountId: accountId,
			agent: core.AgentExtension{
				AgentName: "",
				Prompt:    "Test prompt",
				Tools:     []string{"tool1"},
			},
			updatedBy: userId,
			wantErr:   true,
		},
		{
			name:      "Non-existent agent extension",
			accountId: accountId,
			agent: core.AgentExtension{
				AgentName: "non-existent-agent-" + uuid.New().String(),
				Prompt:    "Test prompt",
				Tools:     []string{"tool1"},
			},
			updatedBy: userId,
			wantErr:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := core.UpdateAgentExtension(sc, tc.accountId, tc.agent, tc.updatedBy)
			if tc.wantErr {
				assert.Error(t, err, "Expected error for test case: %s", tc.name)
				assert.Equal(t, core.AgentExtension{}, result, "Result should be empty on error")
			} else {
				assert.NoError(t, err, "Unexpected error for test case: %s", tc.name)
				assert.Equal(t, tc.agent.AgentName, result.AgentName, "Agent name should match")
				assert.Equal(t, tc.agent.Prompt, result.Prompt, "Prompt should match")
				assert.Equal(t, tc.agent.Tools, result.Tools, "Tools should match")
			}
		})
	}
}

func TestDeleteAgentExtension(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})
	userId := os.Getenv("TEST_USER")
	accountId := os.Getenv("TEST_ACCOUNT")

	// First create an agent extension to delete
	createAgent := core.AgentExtension{
		AgentName: "postgres",
		Prompt:    "Test prompt for deletion",
		Tools:     []string{"tool1"},
	}
	_, err := core.CreateAgentExtension(sc, accountId, createAgent, userId)
	assert.NoError(t, err, "Setup: Failed to create agent extension for delete test")

	testCases := []struct {
		name      string
		accountId string
		agentName string
		wantErr   bool
	}{
		{
			name:      "Delete existing agent extension",
			accountId: accountId,
			agentName: "postgres",
			wantErr:   false,
		},
		{
			name:      "Empty account ID",
			accountId: "",
			agentName: "postgres",
			wantErr:   true,
		},
		{
			name:      "Empty agent name",
			accountId: accountId,
			agentName: "",
			wantErr:   true,
		},
		{
			name:      "Non-existent agent extension",
			accountId: accountId,
			agentName: "non-existent-agent-" + uuid.New().String(),
			wantErr:   true,
		},
		{
			name:      "Already deleted agent extension",
			accountId: accountId,
			agentName: "postgres",
			wantErr:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := core.DeleteAgentExtension(sc, tc.accountId, tc.agentName)
			if tc.wantErr {
				assert.Error(t, err, "Expected error for test case: %s", tc.name)
			} else {
				assert.NoError(t, err, "Unexpected error for test case: %s", tc.name)
			}
		})
	}
}
