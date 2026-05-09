package agents

import (
	"bytes"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTicketAgent_Execute(t *testing.T) {
	ticketAgent := TicketMaster{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-ticket-1",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "@tickets Find all high priority bugs in the Integrations project",
		},
		{
			SessionId: "ut-ticket-2",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "@tickets Show me open issues assigned to " + os.Getenv("TEST_TICKET_ASSIGNEE_EMAIL"),
		},
	}

	for _, tc := range testCases {
		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, ticketAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, "tickets", ticketAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

func TestTicketAgent_InvalidQueries(t *testing.T) {
	ticketAgent := TicketMaster{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-ticket-invalid-1",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Show me all issues with limit 10",
		},
		{
			SessionId: "ut-ticket-invalid-2",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Find tickets using GROUP BY status",
		},
	}

	for _, tc := range testCases {
		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, ticketAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)

		assert.NotContains(t, resp.Query, "LIMIT")
		assert.NotContains(t, resp.Query, "GROUP BY")
	}
}

func BenchmarkTicketAgent_Accuracy(t *testing.B) {
	err := os.Setenv("AWS_REGION", config.Config.LlmProviderRegion)
	assert.Nil(t, err)

	ticketAgent := TicketMaster{}
	sc := security.NewRequestContextForSuperAdmin()

	// Read benchmark queries from data file
	jsonData, err := os.ReadFile("../data/benchmark/jira.json")
	if err != nil {
		t.Errorf("Failed to read benchmark data file: %v", err)
	}
	// Substitute placeholder with the configured assignee email so the
	// benchmark works against a real Jira test user without baking the
	// address into the fixture.
	jsonData = bytes.ReplaceAll(jsonData, []byte("__TEST_TICKET_ASSIGNEE_EMAIL__"), []byte(os.Getenv("TEST_TICKET_ASSIGNEE_EMAIL")))

	type jiraQueries struct {
		Question  string `json:"question"`
		Answer    string `json:"answer"`
		SessionId string `json:"conversation_id"`
	}

	var queries []jiraQueries
	err = common.UnmarshalJson(jsonData, &queries)
	if err != nil {
		t.Errorf("Failed to read benchmark data file: %v", err)
	}

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")

	for _, q := range queries {
		t.Run(fmt.Sprintf("Query: %s", q.Question), func(b *testing.B) {
			err := core.DeleteConversationBySession(q.SessionId, accountId, userId)
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, ticketAgent, userId, accountId, q.SessionId, q.Question)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, resp.AgentName, ticketAgent.GetName())

			assert.Equal(t, q.Answer, resp.Query)
		})
	}
}
