//go:build e2e

package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// testRabbitConfigName returns the tool-config name to use as the
// clarification followup when the agent asks which RabbitMQ instance to
// target. Read from TEST_RABBITMQ_CONFIG_NAME so different environments
// (each with different config names in the DB) can be tested without
// editing the test. Defaults to "dev" for back-compat with the prior
// hardcoded value.
func testRabbitConfigName() string {
	if v := os.Getenv("TEST_RABBITMQ_CONFIG_NAME"); v != "" {
		return v
	}
	return "dev"
}

// TODO mock DBs
// TODO mock Tool Execution
func TestRabbitmqAgent1(t *testing.T) {
	k8sAgent := RabbitMQAgent{}
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_ACCOUNT"), []string{os.Getenv("TEST_USER")})

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-rabbitmq-chain-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "get the list of queues.",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)

		assert.Equal(t, resp.AgentName, k8sAgent.GetName())

		if resp.Status == core.ConversationStatusCompleted {
			continue
		}
		assert.Equal(t, resp.Status, core.ConversationStatusWaiting)

		// based on response return followup response and wait for processing
		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, testRabbitConfigName(), core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
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

func TestRabbitmqAgent2(t *testing.T) {
	k8sAgent := RabbitMQAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-rabbitmq-chain-status-2",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_LOKICHAIN_USER"),
				Query:     "get the rabbit connection status",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)

		assert.Equal(t, resp.AgentName, k8sAgent.GetName())

		if resp.Status == core.ConversationStatusCompleted {
			continue
		}

		assert.Equal(t, resp.Status, core.ConversationStatusWaiting)

		// based on response return followup response and wait for processing
		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, testRabbitConfigName(), core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
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

func TestRabbitmqAgent3(t *testing.T) {
	k8sAgent := RabbitMQAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-rabbitmq-chain-status-3",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_LOKICHAIN_USER"),
				Query:     "get the rabbit connection status",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)

		assert.Equal(t, resp.AgentName, k8sAgent.GetName())

		if resp.Status == core.ConversationStatusCompleted {
			continue
		}

		assert.Equal(t, resp.Status, core.ConversationStatusWaiting)

		// based on response return followup response and wait for processing
		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, testRabbitConfigName(), core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
			UUID:  messageId,
			Valid: true,
		}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
			UUID:  agentId,
			Valid: true,
		}))
		assert.Nil(t, err)
		assert.NotNil(t, resp)

		fmt.Println("response - ", resp.Response)
		fmt.Println("tools - ", resp.AgentStepResponse)

		// should not trigger followup
		resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.Status, core.ConversationStatusCompleted)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}
