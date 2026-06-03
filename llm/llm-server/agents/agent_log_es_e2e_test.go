//go:build e2e

package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLogChain_ExecuteES(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	sessionId := "ut-es-chain-1"
	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")

	testCases :=
		[]struct {
			Query string
		}{
			{
				Query: "Get me recent logs of app llm-server in nudgebee namespace",
			},
			{
				Query: "Get me recent error logs of app llm-server in nudgebee namespace",
			},
			{
				Query: "Get me recent error logs of llm-server",
			},
			{
				Query: "Get me error logs of last 24hrs for llm-server",
			},
		}
	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	for _, tc := range testCases {
		logAgent := newESLogAgent(accountId)

		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, logAgent, userId, accountId, sessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, logAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func BenchmarkESQueryAgent_Accuracy(t *testing.B) {
	sc := security.NewRequestContextForSuperAdmin()

	// read benchmark file from data/benchmark/promtheus.json
	jsonData, err := os.ReadFile("../data/benchmark/es_agent_data.json")
	if err != nil {
		panic(err)
	}

	type esqlQueries struct {
		Question  string                 `json:"question"`
		Answer    string                 `json:"answer"`
		SessionId string                 `json:"conversation_id"`
		AccountId string                 `json:"account_id"`
		UserId    string                 `json:"user_id"`
		Config    toolcore.NBQueryConfig `json:"config"`
	}

	var testcases []esqlQueries
	err = common.UnmarshalJson(jsonData, &testcases)
	if err != nil {
		panic(err)
	}

	accountId := os.Getenv("TEST_ELASTIC_SEARCH_ACCOUNT")
	userId := os.Getenv("TEST_ELASTIC_SEARCH_USER")
	for i, q := range testcases {
		if q.AccountId == "" {
			testcases[i].AccountId = accountId
		}
		if q.UserId == "" {
			testcases[i].UserId = userId
		}
		if q.SessionId == "" {
			testcases[i].SessionId = fmt.Sprintf("ut-prom-bench-%d", i)
		}
		if q.Config.IsEmpty() {
			testcases[i].Config = toolcore.NBQueryConfig{}
		}

	}

	passedTests := 0
	for _, tc := range testcases {
		elasticSearchChain := newESLogAgent(accountId)
		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		resp, _ := core.HandleConversationSessionRequest(sc, elasticSearchChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Question, core.ConversationSessionRequestWithConfig(tc.Config))
		fmt.Printf("Response: %v\n", resp)
		if len(resp.Response) == 0 {
			t.Fail()
		}
	}

	t.Log("Passed", passedTests, "Total", len(testcases), "Accuracy", float64(passedTests)/float64(len(testcases))*100)
	if float64(passedTests)/float64(len(testcases))*100 < 100 {
		t.Fail()
	} else {
		t.Log("All test cases passed")
	}
}
