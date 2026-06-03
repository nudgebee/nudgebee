//go:build e2e

package agents

import (
	"context"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// TODO mock DBs
// TODO mock Tool Execution
func TestK8sAgentExecute(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "get the pods of app coredns in kube-system namespace.",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newKubectlAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		fmt.Println("response - ", resp.Response)
		fmt.Println("tools - ", resp.AgentStepResponse)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentExecute1(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-1.1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "get the pods of app rag-server from all namespaces and get the status of each pod",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newKubectlAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		fmt.Println("response - ", resp.Response)
		fmt.Println("tools - ", resp.AgentStepResponse)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentExecute2(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-1.2",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "get the details for pod with IPs 10.0.0.20, 10.0.0.21, 10.0.0.22 across all namespaces",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newKubectlAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		fmt.Println("response - ", resp.Response)
		fmt.Println("tools - ", resp.AgentStepResponse)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentExecuteDirectCommand(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-2",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "kubectl exec deployment/my-agent-runner -n my-agent -- ls",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newKubectlAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)

		assert.Equal(t, resp.Status, core.ConversationStatusWaiting)

		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)
		resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "yes", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
			UUID:  messageId,
			Valid: true,
		}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
			UUID:  agentId,
			Valid: true,
		}))

		fmt.Println("response - ", resp.Response)
		fmt.Println("tools - ", resp.AgentStepResponse)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentExecuteCRUD(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-3",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Can you check network connectivity to google.com by launching new pod using ubuntu image in my-agent namespace ?",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newKubectlAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)

		// based on response return followup response and wait for processing
		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		assert.Equal(t, resp.Status, core.ConversationStatusWaiting)

		resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "yes", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
			UUID:  messageId,
			Valid: true,
		}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
			UUID:  agentId,
			Valid: true,
		}))

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentExecuteCRUDNo(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-3-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Can you check network connectivity to google.com by launching new pod using ubuntu image in my-agent namespace ?",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newKubectlAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)

		// based on response return followup response and wait for processing
		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		assert.Equal(t, resp.Status, core.ConversationStatusWaiting)

		resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "no", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
			UUID:  messageId,
			Valid: true,
		}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
			UUID:  agentId,
			Valid: true,
		}))

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
	}

}

func TestFollowupCanAgentProcessToolRequest(t *testing.T) {
	testCases :=
		[]struct {
			SessionId         string
			Query             string
			Command           string
			AccountId         string
			UserId            string
			AdditionalContext string
		}{
			{
				SessionId:         "ut-k8s-chain-4",
				AccountId:         os.Getenv("TEST_FOLLOWUP_ACCOUNT"),
				UserId:            os.Getenv("TEST_FOLLOWUP_USER"),
				Query:             "Can you scaledown abc deployment in nudgebee namespace ?",
				Command:           "kubectl scale --replicas 0 deployment/abc --namespace nudgebee",
				AdditionalContext: "",
			},
		}

	for _, tc := range testCases {
		k8sAgent := newKubectlAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		sc := security.NewRequestContext(context.Background(), security.NewSecurityContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{}), nil, nil, nil)

		finish, reqeustType, err := core.IsAgentToolAuthorizedToProcessRequest(sc, k8sAgent, core.NBAgentRequest{
			Query:     tc.Query,
			AccountId: tc.AccountId,
			UserId:    tc.UserId,
		}, core.NBAgentPlannerToolAction{
			Tool:      tools.ToolExecuteKubectlCommand,
			ToolInput: tc.Command,
		})

		assert.Nil(t, err)
		assert.NotNil(t, finish)
		assert.NotNil(t, reqeustType)
		assert.NotEqual(t, *reqeustType, toolcore.ToolRequestTypeUpdate)
	}
}

func TestFollowupCanAgentProcessToolRequest2(t *testing.T) {
	testCases :=
		[]struct {
			SessionId         string
			Query             string
			AccountId         string
			UserId            string
			AdditionalContext string
		}{
			{
				SessionId:         "ut-k8s-chain-5",
				AccountId:         os.Getenv("TEST_ACCOUNT"),
				UserId:            os.Getenv("TEST_USER"),
				Query:             "Can you get the pods using nginx",
				AdditionalContext: "",
			},
		}

	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newKubectlAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		fmt.Println("response - ", resp.Response)
		fmt.Println("tools - ", resp.AgentStepResponse)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

func TestK8sAgentExecuteMultiCommand(t *testing.T) {
	testCases :=
		[]struct {
			SessionId         string
			Query             string
			AccountId         string
			UserId            string
			AdditionalContext string
		}{
			{
				SessionId:         "ut-k8s-chain-6",
				AccountId:         os.Getenv("TEST_ACCOUNT"),
				UserId:            os.Getenv("TEST_USER"),
				Query:             "Get a summary of all Deployments, StatefulSets, DaemonSets, and Jobs running in the cluster. Include their names, namespaces, replica counts, and status.",
				AdditionalContext: "",
			},
		}

	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newKubectlAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		fmt.Println("response - ", resp.Response)
		fmt.Println("tools - ", resp.AgentStepResponse)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

func TestK8sAgentExecuteLogTruncate(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	testCases :=
		[]struct {
			SessionId         string
			Query             string
			AccountId         string
			UserId            string
			AdditionalContext string
		}{
			{
				SessionId:         "ut-k8s-chain-7",
				AccountId:         os.Getenv("TEST_ACCOUNT"),
				UserId:            os.Getenv("TEST_USER"),
				Query:             "get logs of services-server in nudgebee namespace",
				AdditionalContext: "",
			},
		}

	for _, tc := range testCases {
		k8sAgent := newKubectlAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		fmt.Println("response - ", resp.Response)
		fmt.Println("tools - ", resp.AgentStepResponse)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

func TestK8sAgentExecuteDiscovery(t *testing.T) {
	testCases :=
		[]struct {
			SessionId         string
			Query             string
			AccountId         string
			UserId            string
			AdditionalContext string
		}{
			{
				SessionId:         "ut-k8s-chain-8",
				AccountId:         os.Getenv("TEST_ACCOUNT"),
				UserId:            os.Getenv("TEST_USER"),
				Query:             "Get events of rag-server pods in nudgebee namespace",
				AdditionalContext: "",
			},
		}

	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newKubectlAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		fmt.Println("response - ", resp.Response)
		fmt.Println("tools - ", resp.AgentStepResponse)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

func TestK8sAgentExecuteRefine(t *testing.T) {
	testCases :=
		[]struct {
			SessionId         string
			Query             string
			AccountId         string
			UserId            string
			AdditionalContext string
		}{
			{
				SessionId:         "ut-k8s-chain-19",
				AccountId:         os.Getenv("TEST_ACCOUNT"),
				UserId:            os.Getenv("TEST_USER"),
				Query:             "get llm-server pod logs",
				AdditionalContext: "",
			},
		}

	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newKubectlAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, err)

		// based on response return followup response and wait for processing
		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		assert.Nil(t, resp.Status, core.ConversationStatusWaiting)

		resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "nudgebee", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
			UUID:  messageId,
			Valid: true,
		}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
			UUID:  agentId,
			Valid: true,
		}))

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}
