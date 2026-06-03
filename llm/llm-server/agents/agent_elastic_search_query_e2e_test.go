//go:build e2e

package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"os"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TODO mock DBs
// TODO mock Tool Execution

func CompareJSON(json1, json2 string) (bool, error) {
	var obj1, obj2 map[string]any

	// Unmarshal the first JSON string into a map
	if err := common.UnmarshalJson([]byte(json1), &obj1); err != nil {
		return false, fmt.Errorf("error unmarshalling json1: %v", err)
	}

	// Unmarshal the second JSON string into a map
	if err := common.UnmarshalJson([]byte(json2), &obj2); err != nil {
		return false, fmt.Errorf("error unmarshalling json2: %v", err)
	}

	// Compare the two maps
	return reflect.DeepEqual(obj1, obj2), nil
}
func TestElasticSearchAgent_Execute(t *testing.T) {
	eventsAgent := ElasticSearchQueryAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
			Config    toolcore.NBQueryConfig
		}{
			{
				SessionId: "ut-agent-es-query-1",
				AccountId: os.Getenv("TEST_ESCHAIN_ACCOUNT"),
				UserId:    os.Getenv("TEST_ESCHAIN_USER"),
				Query:     "show me errors for llm-server",
				Config:    toolcore.NBQueryConfig{}},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		resp, err := core.HandleConversationSessionRequest(sc, eventsAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query, core.ConversationSessionRequestWithConfig(tc.Config))
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, eventsAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func BenchmarkElasticSearchQueryAgent_Accuracy(t *testing.B) {
	elasticSearchChain := ElasticSearchQueryAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	// read benchmark file from data/benchmark/promtheus.json
	jsonData, err := os.ReadFile("../data/benchmark/elastic_search.json")
	if err != nil {
		panic(err)
	}

	type esqlQueries struct {
		Question  string                 `json:"question"`
		Answers   []string               `json:"answers"`
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

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, _ := core.HandleConversationSessionRequest(sc, elasticSearchChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Question, core.ConversationSessionRequestWithConfig(tc.Config))
		var answerQueryJson map[string]any
		completed := false
		if len(resp.Response) == 0 {
			t.Fail()
		}
		var respQueryJson map[string]any
		err = common.UnmarshalJson([]byte(resp.Response[0]), &respQueryJson)
		if err != nil {
			t.Log("Failed to parse response json", resp.Response[0])
		}
		for _, answer := range tc.Answers {
			err = common.UnmarshalJson([]byte(answer), &answerQueryJson)
			if err != nil {
				t.Log("Failed to parse answer json", answer)
			}
			match, err := CompareJSON(resp.Response[0], answer)
			if err != nil {
				t.Log("Error comparing JSON", err)
				t.Fail()
			}
			if match {
				passedTests = passedTests + 1
				completed = true
				break
			}
		}
		if !completed {
			t.Log("Failed Query - ", tc.Question, "Expected", tc.Answers, "Got", resp.Response[0])
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
