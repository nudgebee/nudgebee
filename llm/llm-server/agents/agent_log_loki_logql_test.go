package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TODO mock DBs
// TODO mock Tool Execution
func TestLogqlAgentExecute(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-logql-chain-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Get me recent logs of app services-server in nudgebee namespace and add limit 100",
			},
		}
	for _, tc := range testCases {
		logqlAgent := &LogqlAgent{accountId: tc.AccountId}

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		resp, err := core.HandleConversationSessionRequest(sc, logqlAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, logqlAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestLogqlAgentExecute2(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "can you get me recent logs of llm-server in nudgebee namespace",
			},
		}
	for _, tc := range testCases {
		lokiAgent := &LogqlAgent{}

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, lokiAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, lokiAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func BenchmarkLogQueryAgentAccuracyLoki(t *testing.B) {
	err := os.Setenv("AWS_REGION", config.Config.LlmProviderRegion)
	assert.Nil(t, err)

	logAgent := &LogqlAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	// read benchmark file from data/benchmark/promtheus.json
	jsonData, err := os.ReadFile("../data/benchmark/loki.json")
	if err != nil {
		panic(err)
	}

	type logqlQueries struct {
		Question  string `json:"question"`
		Answer    string `json:"answer"`
		SessionId string `json:"conversation_id"`
		AccountId string `json:"account_id"`
		UserId    string `json:"user_id"`
	}

	var testcases []logqlQueries
	err = common.UnmarshalJson(jsonData, &testcases)
	if err != nil {
		panic(err)
	}

	accountId := os.Getenv("TEST_LOKICHAIN_ACCOUNT")
	userId := os.Getenv("TEST_LOKICHAIN_USER")

	for i, q := range testcases {
		if q.AccountId == "" {
			testcases[i].AccountId = accountId
		}
		if q.UserId == "" {
			testcases[i].UserId = userId
		}
		if q.SessionId == "" {
			testcases[i].SessionId = fmt.Sprintf("ut-log-bench-%d", i)
		}
	}

	passedTests := 0
	for _, tc := range testcases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, _ := core.HandleConversationSessionRequest(sc, logAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Question)

		match, err := regexp.MatchString(tc.Answer, resp.Response[0])
		if len(resp.Response) > 0 && (match || tc.Answer == resp.Response[0]) && err == nil {
			passedTests = passedTests + 1
			t.Log("Passed Query - ", tc.Question, "Expected", tc.Answer, "Got", resp.Response[0])
		} else {
			t.Log("Failed Query - ", tc.Question, "Expected", tc.Answer, "Got", resp.Response[0])
		}

		// invocations, ok := resp.AgentStepResponse.([]any)
		// if ok && len(invocations) > 0 {
		// 	invocationAny := invocations[len(invocations)-1]
		// 	invocation, ok := invocationAny.(core.ToolInvocation)
		// 	if ok && invocation.Call.FunctionCall.Arguments == tc.Answer {
		// 		passedTests = passedTests + 1
		// 	} else {
		// 		t.Log("Failed Query - ", tc.Question, "Expected", tc.Answer, "Got", invocation.Call.FunctionCall.Arguments)
		// 	}
		// } else {
		// 	t.Log("Failed Query - ", tc.Question, "Expected", tc.Answer, "Got", "No Response")
		// }
	}

	t.Error("Passed", passedTests, "Total", len(testcases), "Accuracy", float64(passedTests)/float64(len(testcases))*100)

}
