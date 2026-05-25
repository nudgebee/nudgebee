package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReportAgent_ExecuteWithId(t *testing.T) {
	eventsAgent := AgentEventRCAReport{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			ConversationId string
			Query          string
			AccountId      string
			UserId         string
		}{
			{
				ConversationId: "ut-events-chain-1",
				AccountId:      os.Getenv("TEST_ESCHAIN_ACCOUNT"),
				UserId:         os.Getenv("TEST_K8SCHAIN_USER"),
				Query:          "Provide RCA report for event Id f5a7946a-6a2a-4f5d-aca4-8c0b69b85fe4",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.ConversationId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, eventsAgent, tc.UserId, tc.AccountId, tc.ConversationId, tc.Query)
		if err != nil {
			t.Fatalf("Failed to handle conversation session request: %v", err)
		}
		fmt.Println(resp.Response)
		jsonResp, err := common.MarshalJson(resp)
		if err != nil {
			t.Fatalf("Failed to marshal response to JSON: %v", err)
		}
		fmt.Println(string(jsonResp))
		err = os.WriteFile("output.json", jsonResp, 0644)
		if err != nil {
			t.Fatalf("Failed to write response to file: %v", err)
		}
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, eventsAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestEvidenceReportAgent(t *testing.T) {
	eventsAgent := AgentEventRCAReport{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			ConversationId string
			Query          string
			AccountId      string
			UserId         string
		}{
			{
				ConversationId: "ut-events-chain-1",
				AccountId:      os.Getenv("TEST_ESCHAIN_ACCOUNT"),
				UserId:         os.Getenv("TEST_K8SCHAIN_USER"),
				Query:          "Provide RCA report for event Id f5a7946a-6a2a-4f5d-aca4-8c0b69b85fe4",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.ConversationId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, eventsAgent, tc.UserId, tc.AccountId, tc.ConversationId, tc.Query)
		if err != nil {
			t.Fatalf("Failed to handle conversation session request: %v", err)
		}
		fmt.Println(resp.Response)
		jsonResp, err := common.MarshalJson(resp)
		if err != nil {
			t.Fatalf("Failed to marshal response to JSON: %v", err)
		}
		fmt.Println(string(jsonResp))
		err = os.WriteFile("output.json", jsonResp, 0644)
		if err != nil {
			t.Fatalf("Failed to write response to file: %v", err)
		}
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, eventsAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}
