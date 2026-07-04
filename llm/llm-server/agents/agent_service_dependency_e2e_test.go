//go:build e2e

package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TODO mock DBs
// TODO mock Tool Execution
func TestServiceDependencyForCM(t *testing.T) {
	serviceDependencyAgent := ServiceDependencyGraphAgent{}
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
				SessionId: "ut-service_dependency_graph-5",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "show me service dependency of configmap nudgebee-reddit-config in nudgebee namespace",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, serviceDependencyAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, serviceDependencyAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestServiceDependencyForWorkload(t *testing.T) {
	serviceDependencyAgent := ServiceDependencyGraphAgent{}
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
				SessionId: "ut-service_dependency_graph-6",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "show me service dependency of worklaod cloud-collector-server in nudgebee namespace..",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, serviceDependencyAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, serviceDependencyAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

// TestServiceDependencyKGExecute exercises the KG tool routing paths enabled
// when KGToolsEnabled=true: kg_traverse for CALLS edges and static topology,
// and kg_search_nodes for discovery by type/namespace. Each case targets a
// question that the runtime-metrics service_dependency_graph_execute tool
// would not answer, so a non-empty response confirms the agent routed to the
// KG tools rather than falling back.
func TestServiceDependencyKGExecute(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" {
		t.Skip("TEST_ACCOUNT / TEST_USER not set — skipping integration test")
	}
	serviceDependencyAgent := ServiceDependencyGraphAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-service_dependency_graph-kg-calls-downstream",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "What services does llm-server call downstream in the nudgebee namespace?",
			},
			{
				SessionId: "ut-service_dependency_graph-kg-discovery",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Find all databases running in the nudgebee namespace.",
			},
			{
				SessionId: "ut-service_dependency_graph-kg-topology",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Which namespace and cluster host the llm-server workload?",
			},
			{
				SessionId: "ut-service_dependency_graph-kg-calls-upstream",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Which services call into llm-server in the nudgebee namespace?",
			},
			{
				SessionId: "ut-service_dependency_graph-kg-lb-routing",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Which workloads does the api-server load balancer route to?",
			},
		}
	if nodeID := os.Getenv("TEST_KG_NODE_ID"); nodeID != "" {
		testCases = append(testCases, struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			SessionId: "ut-service_dependency_graph-kg-get-node",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     fmt.Sprintf("Show me the full details of node %s", nodeID),
		})
	}
	for _, tc := range testCases {
		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, serviceDependencyAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, serviceDependencyAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

// TestServiceDependencyKGCommunicationBrowse is the aggregated "browse all
// communication" smoke test from the (deleted) V2 e2e file. Kept alongside the
// per-question KGExecute cases so both discovery shapes have integration
// coverage.
func TestServiceDependencyKGCommunicationBrowse(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" {
		t.Skip("TEST_ACCOUNT / TEST_USER not set — skipping integration test")
	}
	agent := ServiceDependencyGraphAgent{accountId: os.Getenv("TEST_ACCOUNT")}
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
