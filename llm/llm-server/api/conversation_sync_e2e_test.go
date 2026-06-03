//go:build e2e

package api

import (
	"fmt"
	"nudgebee/llm/agents"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConversationSync_Test1(t *testing.T) {
	k8sAgent := agents.KubectlAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-restart-chain-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Can you list all the pods in the namespace kube-system?",
			},
		}
	for _, tc := range testCases {

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

		msg, err := core.GetConversationDao().GetConversationMessage(resp.MessageId, tc.AccountId, resp.ConversationId)
		assert.Nil(t, err)
		assert.NotNil(t, msg.WorkerName)
		assert.NotNil(t, msg.AgentName)

		resp, err = core.HandleConversationMessageRequest(tc.AccountId, resp.ConversationId, resp.MessageId)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)

	}
}
