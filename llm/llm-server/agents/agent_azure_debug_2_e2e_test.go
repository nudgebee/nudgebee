//go:build e2e

package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAzureAgentReWoo_VMStatus(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), nil)
	tests := []struct {
		UserId    string
		AccountId string
		Query     string
		SessionId string
	}{
		{
			UserId:    os.Getenv("TEST_USER"),
			AccountId: os.Getenv("TEST_AZURE_ACCOUNT"),
			Query:     "Can you check all the VMs and identify if there are any stopped VMs",
			SessionId: "ut-azure-1",
		},
	}

	for _, tc := range tests {

		azureDebugAgentent := newAzureDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, azureDebugAgentent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, azureDebugAgentent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.Greater(t, len(resp.AgentStepResponse), 0)
	}
}

func TestAzureAgentReWoo_CostBreakDown(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), nil)
	tests := []struct {
		UserId    string
		AccountId string
		Query     string
		SessionId string
	}{
		{
			UserId:    os.Getenv("TEST_USER"),
			AccountId: os.Getenv("TEST_AZURE_ACCOUNT"),
			Query:     "Can you get me cost breakdown on last month ?",
			SessionId: "ut-azure-2",
		},
	}

	for _, tc := range tests {
		azureDebugAgentent := newAzureDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, azureDebugAgentent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, azureDebugAgentent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.Greater(t, len(resp.AgentStepResponse), 0)
	}
}

func TestAzureAgentReWoo_AKSDetails(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), nil)
	tests := []struct {
		UserId    string
		AccountId string
		Query     string
		SessionId string
	}{
		{
			UserId:    os.Getenv("TEST_USER"),
			AccountId: os.Getenv("TEST_AZURE_ACCOUNT"),
			Query:     "How many AKS clusters and I have, and how many nodepools they are running and how many nodes each nodepool has?",
			SessionId: "ut-azure-3",
		},
	}

	for _, tc := range tests {
		azureDebugAgentent := newAzureDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, azureDebugAgentent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, azureDebugAgentent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.Greater(t, len(resp.AgentStepResponse), 0)
	}
}

func TestAzureAgentReWoo_AzureMonitor(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), nil)
	tests := []struct {
		UserId    string
		AccountId string
		Query     string
		SessionId string
	}{
		{
			UserId:    os.Getenv("TEST_USER"),
			AccountId: os.Getenv("TEST_AZURE_ACCOUNT"),
			Query:     "can you investigate recent azure events for VM " + envOr("TEST_AZURE_VM", "my-vm-dev") + "?",
			SessionId: "ut-azure-4",
		},
	}

	for _, tc := range tests {
		azureDebugAgentent := newAzureDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, azureDebugAgentent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, azureDebugAgentent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.Greater(t, len(resp.AgentStepResponse), 0)
	}
}
