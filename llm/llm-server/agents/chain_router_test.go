package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// TODO mock DBs
// TODO mock Tool Execution
func TestRouterAgent_PostgresAgent(t *testing.T) {
	routerAgent := RouterAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	// Get me recent traces where savings is more than 1$
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-route-1",
				AccountId: os.Getenv("TEST_PROMETHEUSCHAIN_ACCOUNT"),
				UserId:    os.Getenv("TEST_PROMETHEUSCHAIN_USER"),
				Query:     "How many postgres connections I have",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		// Route Chain expects conversationID as SessionId
		resp, err := routerAgent.Execute(sc, core.NBAgentRequest{
			Query:          tc.Query,
			AccountId:      tc.AccountId,
			ConversationId: uuid.New().String(),
			UserId:         tc.UserId,
			MessageId:      uuid.New().String(),
			ParentAgentId:  uuid.New().String(),
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp, "PostgresAgent")

		chain, ok := getAgent(sc, resp.Response[0], os.Getenv("TEST_ROUTERCHAIN_ACCOUNT"))
		assert.True(t, ok)
		assert.NotNil(t, chain)

	}

}

func TestRouterAgentGeneralQuestion(t *testing.T) {
	routerAgent := RouterAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	// Get me recent traces where savings is more than 1$
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-route-2",
				AccountId: os.Getenv("TEST_ROUTERCHAIN_ACCOUNT"),
				UserId:    os.Getenv("TEST_ROUTERCHAIN_USER"),
				Query:     "How is weather today?",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		// Route Chain expects conversationID as SessionId
		resp, err := routerAgent.Execute(sc, core.NBAgentRequest{
			Query:          tc.Query,
			AccountId:      tc.AccountId,
			ConversationId: uuid.New().String(),
			UserId:         tc.UserId,
			MessageId:      uuid.New().String(),
			ParentAgentId:  uuid.New().String(),
		})
		cleanUpErr := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, cleanUpErr)
		assert.Nil(t, err)
		assert.Equal(t, resp, "GeneralAgent")
	}

}

func TestRouterAgentDebugAgent(t *testing.T) {
	routerAgent := RouterAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	// Get me recent traces where savings is more than 1$
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-route-3",
				AccountId: os.Getenv("TEST_ROUTERCHAIN_ACCOUNT"),
				UserId:    os.Getenv("TEST_ROUTERCHAIN_USER"),
				Query:     "can you update cpu resources of app-dev worklod in nudgebee namespace",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		// Route Chain expects conversationID as SessionId
		resp, err := routerAgent.Execute(sc, core.NBAgentRequest{
			Query:          tc.Query,
			AccountId:      tc.AccountId,
			ConversationId: uuid.New().String(),
			UserId:         tc.UserId,
			MessageId:      uuid.New().String(),
			ParentAgentId:  uuid.New().String(),
		})
		cleanUpErr := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, cleanUpErr)
		assert.Nil(t, err)
		assert.Equal(t, resp, "InvestigateAgent")

		chain, ok := getAgent(sc, resp.Response[0], os.Getenv("TEST_ROUTERCHAIN_ACCOUNT"))
		assert.True(t, ok)
		assert.NotNil(t, chain)

	}

}

func TestRouterAgentPrometheusAgent(t *testing.T) {
	routerAgent := RouterAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	// Get me recent traces where savings is more than 1$
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-route-3",
				AccountId: os.Getenv("TEST_ROUTERCHAIN_ACCOUNT"),
				UserId:    os.Getenv("TEST_ROUTERCHAIN_USER"),
				Query:     "@prometheus can you get me memory usage of cluster",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		// Route Chain expects conversationID as SessionId
		resp, err := routerAgent.Execute(sc, core.NBAgentRequest{
			Query:          tc.Query,
			AccountId:      tc.AccountId,
			ConversationId: uuid.New().String(),
			UserId:         tc.UserId,
			MessageId:      uuid.New().String(),
			ParentAgentId:  uuid.New().String(),
		})
		cleanUpErr := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, cleanUpErr)
		assert.Nil(t, err)
		assert.Equal(t, resp, "prometheus")

		chain, ok := getAgent(sc, resp.Response[0], os.Getenv("TEST_ROUTERCHAIN_ACCOUNT"))
		assert.True(t, ok)
		assert.NotNil(t, chain)

	}

}

func TestRouterAgentLogsAgent(t *testing.T) {
	routerAgent := RouterAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	// Get me recent traces where savings is more than 1$
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-route-3",
				AccountId: os.Getenv("TEST_ROUTERCHAIN_ACCOUNT"),
				UserId:    os.Getenv("TEST_ROUTERCHAIN_USER"),
				Query:     "@logs can you get me recent error logs in nudgebee namespace ?",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		// Route Chain expects conversationID as SessionId
		resp, err := routerAgent.Execute(sc, core.NBAgentRequest{
			Query:          tc.Query,
			AccountId:      tc.AccountId,
			ConversationId: uuid.New().String(),
			UserId:         tc.UserId,
			MessageId:      uuid.New().String(),
			ParentAgentId:  uuid.New().String(),
		})
		cleanUpErr := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, cleanUpErr)
		assert.Nil(t, err)
		assert.Equal(t, resp, "logs")

		chain, ok := getAgent(sc, resp.Response[0], os.Getenv("TEST_ROUTERCHAIN_ACCOUNT"))
		assert.True(t, ok)
		assert.NotNil(t, chain)
	}

}

func TestRouterAgentRabbitMqAgent(t *testing.T) {
	routerAgent := RouterAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	// Get me recent traces where savings is more than 1$
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-route-3",
				AccountId: os.Getenv("TEST_ROUTERCHAIN_ACCOUNT"),
				UserId:    os.Getenv("TEST_ROUTERCHAIN_USER"),
				Query:     "Can you check for number of connections in rabbitmq",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		// Route Chain expects conversationID as SessionId
		resp, err := routerAgent.Execute(sc, core.NBAgentRequest{
			Query:          tc.Query,
			AccountId:      tc.AccountId,
			ConversationId: uuid.New().String(),
			UserId:         tc.UserId,
			MessageId:      uuid.New().String(),
			ParentAgentId:  uuid.New().String(),
		})
		cleanUpErr := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, cleanUpErr)
		assert.Nil(t, err)
		assert.Equal(t, resp, RabbitMQAgentName)

		chain, ok := getAgent(sc, resp.Response[0], os.Getenv("TEST_ROUTERCHAIN_ACCOUNT"))
		assert.True(t, ok)
		assert.NotNil(t, chain)
	}

}

func TestRouterAgentHelpAgent(t *testing.T) {
	routerAgent := HelpAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	// Get me recent traces where savings is more than 1$
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-help-1",
				AccountId: os.Getenv("TEST_ROUTERCHAIN_ACCOUNT"),
				UserId:    os.Getenv("TEST_ROUTERCHAIN_USER"),
				Query:     "hello",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		// Route Chain expects conversationID as SessionId
		resp, err := routerAgent.Execute(sc, core.NBAgentRequest{
			Query:          tc.Query,
			AccountId:      tc.AccountId,
			ConversationId: uuid.New().String(),
			UserId:         tc.UserId,
			MessageId:      uuid.New().String(),
			ParentAgentId:  uuid.New().String(),
		})
		assert.Nil(t, err)
		assert.NotEmpty(t, resp.Response[0])
	}

}

func TestRouterAgentHelpAgent2(t *testing.T) {
	routerAgent := HelpAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	// Get me recent traces where savings is more than 1$
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-help-1",
				AccountId: os.Getenv("TEST_ROUTERCHAIN_ACCOUNT"),
				UserId:    os.Getenv("TEST_ROUTERCHAIN_USER"),
				Query:     "Can you tell me abt kubectl agent",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		// Route Chain expects conversationID as SessionId
		resp, err := routerAgent.Execute(sc, core.NBAgentRequest{
			Query:          tc.Query,
			AccountId:      tc.AccountId,
			ConversationId: uuid.New().String(),
			UserId:         tc.UserId,
			MessageId:      uuid.New().String(),
			ParentAgentId:  uuid.New().String(),
		})
		assert.Nil(t, err)
		assert.NotEmpty(t, resp.Response[0])
	}

}

func TestRouterAgentCodeAgent(t *testing.T) {
	routerAgent := RouterAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-route-code-1",
				AccountId: os.Getenv("TEST_ROUTERCHAIN_ACCOUNT"),
				UserId:    os.Getenv("TEST_ROUTERCHAIN_USER"),
				Query:     "@code can you fix this CI failure and raise a PR",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		resp, err := routerAgent.Execute(sc, core.NBAgentRequest{
			Query:          tc.Query,
			AccountId:      tc.AccountId,
			ConversationId: uuid.New().String(),
			UserId:         tc.UserId,
			MessageId:      uuid.New().String(),
			ParentAgentId:  uuid.New().String(),
		})
		cleanUpErr := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, cleanUpErr)
		assert.Nil(t, err)
		// @code should be extracted as "code" by the router
		assert.Equal(t, "code", resp.Response[0])

		chain, ok := getAgent(sc, resp.Response[0], os.Getenv("TEST_ROUTERCHAIN_ACCOUNT"))
		assert.True(t, ok)
		assert.NotNil(t, chain)
		assert.Equal(t, AgentCode2, chain.GetName())
	}

}

func TestRouterAgen5(t *testing.T) {
	routerAgent := RouterAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	// Get me recent traces where savings is more than 1$
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-rout-5",
				AccountId: os.Getenv("TEST_ROUTERCHAIN_ACCOUNT"),
				UserId:    os.Getenv("TEST_ROUTERCHAIN_USER"),
				Query:     "Can your check otel-demo namespace for pod restarts and investigate",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		// Route Chain expects conversationID as SessionId
		resp, err := routerAgent.Execute(sc, core.NBAgentRequest{
			Query:          tc.Query,
			AccountId:      tc.AccountId,
			ConversationId: uuid.New().String(),
			UserId:         tc.UserId,
			MessageId:      uuid.New().String(),
			ParentAgentId:  uuid.New().String(),
		})
		assert.Nil(t, err)
		assert.Equal(t, AgentK8sDebugName, resp)
		chain, ok := getAgent(sc, resp.Response[0], os.Getenv("TEST_ROUTERCHAIN_ACCOUNT"))
		assert.True(t, ok)
		assert.NotNil(t, chain)
	}

}

func TestRouterAgen6(t *testing.T) {
	routerAgent := RouterAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	// Get me recent traces where savings is more than 1$
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-rout-6",
				AccountId: os.Getenv("TEST_ROUTERCHAIN_ACCOUNT"),
				UserId:    os.Getenv("TEST_ROUTERCHAIN_USER"),
				Query:     "Can you provide specific examples of false positives or negatives that have been observed with Wazuh Vulnerability Detector on Ubuntu 22.04?",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		// Route Chain expects conversationID as SessionId
		resp, err := routerAgent.Execute(sc, core.NBAgentRequest{
			Query:          tc.Query,
			AccountId:      tc.AccountId,
			ConversationId: uuid.New().String(),
			UserId:         tc.UserId,
			MessageId:      uuid.New().String(),
			ParentAgentId:  uuid.New().String(),
		})
		assert.Nil(t, err)
		assert.Equal(t, AgentK8sDebugName, resp)
		chain, ok := getAgent(sc, resp.Response[0], os.Getenv("TEST_ROUTERCHAIN_ACCOUNT"))
		assert.True(t, ok)
		assert.NotNil(t, chain)
	}

}
