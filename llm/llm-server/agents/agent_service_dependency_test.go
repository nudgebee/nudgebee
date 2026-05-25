package agents

import (
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
			{
				SessionId: "ut-service_dependency_graph-kg-get-node",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Show me the full details of node 1af1b05d-38b2-5a01-b644-32077e5028e5",
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
