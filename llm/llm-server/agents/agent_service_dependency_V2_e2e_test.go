//go:build e2e

package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServiceDependencyV2KGExecute(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" {
		t.Skip("TEST_ACCOUNT / TEST_USER not set — skipping integration test")
	}
	agent := ServiceDependencyGraphAgentV2{accountId: os.Getenv("TEST_ACCOUNT")}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		// --- K8s cases ---
		// {
		// 	SessionId: "ut-service_dependency_graph_v2-kg-calls-downstream",
		// 	AccountId: os.Getenv("TEST_ACCOUNT"),
		// 	UserId:    os.Getenv("TEST_USER"),
		// 	Query:     "how does app-dev in nudgebee namespace getting traffic from internet?",
		// },
		{
			SessionId: "session_test_sdg_v2_test",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "tell me all the communication happening in nudgebee ns",
		},
		// {
		// 	SessionId: "ut-service_dependency_graph_v2-kg-topology",
		// 	AccountId: os.Getenv("TEST_ACCOUNT"),
		// 	UserId:    os.Getenv("TEST_USER"),
		// 	Query:     "Which namespace and cluster host the llm-server workload?",
		// },
		// {
		// 	SessionId: "ut-service_dependency_graph_v2-kg-calls-upstream",
		// 	AccountId: os.Getenv("TEST_ACCOUNT"),
		// 	UserId:    os.Getenv("TEST_USER"),
		// 	Query:     "Which services call into llm-server in the nudgebee namespace?",
		// },
		// // --- AWS cases ---
		// {
		// 	SessionId: "ut-service_dependency_graph_v2-kg-cloud-rds",
		// 	AccountId: os.Getenv("TEST_ACCOUNT"),
		// 	UserId:    os.Getenv("TEST_USER"),
		// 	Query:     "Find all RDS databases across our AWS accounts.",
		// },
		// {
		// 	SessionId: "ut-service_dependency_graph_v2-kg-lb-routing",
		// 	AccountId: os.Getenv("TEST_ACCOUNT"),
		// 	UserId:    os.Getenv("TEST_USER"),
		// 	Query:     "Which workloads does the api-server load balancer route to?",
		// },
		// {
		// 	SessionId: "ut-service_dependency_graph_v2-kg-aws-vpc-hosting",
		// 	AccountId: os.Getenv("TEST_ACCOUNT"),
		// 	UserId:    os.Getenv("TEST_USER"),
		// 	Query:     "Which VPC and subnet host the production EKS cluster?",
		// },
		// {
		// 	SessionId: "ut-service_dependency_graph_v2-kg-aws-sg-attachment",
		// 	AccountId: os.Getenv("TEST_ACCOUNT"),
		// 	UserId:    os.Getenv("TEST_USER"),
		// 	Query:     "What security groups are attached to the api-server load balancer?",
		// },
		// // --- GCP / Azure / cross-cloud cases ---
		// {
		// 	SessionId: "ut-service_dependency_graph_v2-kg-gcp-compute",
		// 	AccountId: os.Getenv("TEST_ACCOUNT"),
		// 	UserId:    os.Getenv("TEST_USER"),
		// 	Query:     "List all GCP compute instances in our project.",
		// },
		// {
		// 	SessionId: "ut-service_dependency_graph_v2-kg-azure-sql",
		// 	AccountId: os.Getenv("TEST_ACCOUNT"),
		// 	UserId:    os.Getenv("TEST_USER"),
		// 	Query:     "Find all Azure SQL databases in our subscription.",
		// },
		// {
		// 	SessionId: "ut-service_dependency_graph_v2-kg-cross-cloud-databases",
		// 	AccountId: os.Getenv("TEST_ACCOUNT"),
		// 	UserId:    os.Getenv("TEST_USER"),
		// 	Query:     "List all databases across AWS, GCP, and Azure.",
		// },
	}
	for _, tc := range testCases {
		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, agent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}
