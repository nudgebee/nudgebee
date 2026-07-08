//go:build e2e

package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGcpAgentReWoo_GCSBucketPublic tests the GCP agent's ability to generate a plan
// for identifying public GCS buckets.
func TestGcpAgentReWoo_GCSBucketPublic(t *testing.T) {
	// Ensure TEST_TENANT, TEST_ACCOUNT, TEST_USER, and TEST_GCP_ACCOUNT are set in your environment
	// For example:
	// export TEST_TENANT="your_tenant_id"
	// export TEST_ACCOUNT="your_account_id" // This might be a general account ID for testing framework
	// export TEST_USER="your_user_id"
	// export TEST_GCP_ACCOUNT="your_gcp_project_id_or_specific_account_for_gcp"

	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_ACCOUNT"), nil)
	tests := []struct {
		UserId    string
		AccountId string // This will be used as the GCP Project ID or relevant GCP account identifier for the agent
		Query     string
		SessionId string
	}{
		{
			UserId:    os.Getenv("TEST_USER"),
			AccountId: os.Getenv("TEST_GCP_ACCOUNT"), // Using TEST_GCP_ACCOUNT for GCP context
			Query:     "Can you check all the GCS buckets and identify if there are any public buckets?",
			SessionId: "ut-gcp-chain-gcs-rewoo",
		},
	}

	for _, tc := range tests {
		if tc.AccountId == "" {
			t.Skip("TEST_GCP_ACCOUNT environment variable is not set, skipping test.")
		}

		gcpDebugAgent := newGcpDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, gcpDebugAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, gcpDebugAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.Greater(t, len(resp.AgentStepResponse), 0)
	}
}

// TestGcpAgentReWoo_BillingExport tests the GCP agent's ability to generate a plan
// for checking billing export configurations.
func TestGcpAgentReWoo_BillingExport(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_ACCOUNT"), nil)
	tests := []struct {
		UserId    string
		AccountId string
		Query     string
		SessionId string
	}{
		{
			UserId:    os.Getenv("TEST_USER"),
			AccountId: os.Getenv("TEST_GCP_ACCOUNT"),
			Query:     "Can you show me how my billing export is configured?",
			SessionId: "ut-gcp-chain-billing-rewoo",
		},
	}

	for _, tc := range tests {
		if tc.AccountId == "" {
			t.Skip("TEST_GCP_ACCOUNT environment variable is not set, skipping test.")
		}
		gcpDebugAgent := newGcpDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, gcpDebugAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, gcpDebugAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.Greater(t, len(resp.AgentStepResponse), 0)
	}
}

// TestGcpAgentReWoo_GCEInstanceDetails tests the GCP agent's ability to generate a plan
// for getting details about GCE instances.
func TestGcpAgentReWoo_GCEInstanceDetails(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_ACCOUNT"), nil)
	tests := []struct {
		UserId    string
		AccountId string
		Query     string
		SessionId string
	}{
		{
			UserId:    os.Getenv("TEST_USER"),
			AccountId: os.Getenv("TEST_GCP_ACCOUNT"),
			Query:     "How many GCE instances do I have, what are their types, and in which zones are they running in project " + envOr("TEST_GCP_PROJECT", "my-gcp-project-dev") + " ?",
			SessionId: "ut-gcp-chain-gce-rewoo",
		},
	}

	for _, tc := range tests {
		if tc.AccountId == "" {
			t.Skip("TEST_GCP_ACCOUNT environment variable is not set, skipping test.")
		}
		gcpDebugAgent := newGcpDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, gcpDebugAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, gcpDebugAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.Greater(t, len(resp.AgentStepResponse), 0)
	}
}
