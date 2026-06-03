//go:build e2e

package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TODO mock DBs
// TODO mock Tool Execution
func TestEventAgentExecute(t *testing.T) {
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
				AccountId:      os.Getenv("TEST_ACCOUNT"),
				UserId:         os.Getenv("TEST_USER"),
				Query:          "Get events of pod frontend-5b4f8b9668-h2mnq in namespace coroot-multi-svc-app",
			},
		}
	for _, tc := range testCases {
		eventsAgent := AgentEvents{}

		err := core.DeleteConversationBySession(tc.ConversationId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, eventsAgent, tc.UserId, tc.AccountId, tc.ConversationId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, eventsAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

// TestEventAgentExecuteWithIdLogs fetches a recent event from the test DB and
// asks the agent to produce an RCA. Skipped if the DB has no events.
func TestEventAgentExecuteWithIdLogs(t *testing.T) {
	eventID := FetchRecentEventID(t, os.Getenv("TEST_ACCOUNT"))
	eventsAgent := AgentEvents{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-events-chain-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     fmt.Sprintf("Get RCA of Event with Id %s", eventID),
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, eventsAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		fmt.Println(resp.Response)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, eventsAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestEventAgentPostgresEvent(t *testing.T) {
	k8sChain := AgentEvents{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-events-chain-7",
				AccountId: os.Getenv("TEST_K8SCHAIN_ACCOUNT"),
				UserId:    os.Getenv("TEST_K8SCHAIN_USER"),
				Query:     "Describe table schema for events table",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestEventAgentRecentEvent(t *testing.T) {
	k8sChain := AgentEvents{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-events-chain-8",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "show me any 2 recent events",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestEventAgentAnamolyDetection(t *testing.T) {
	k8sChain := AgentEvents{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-events-chain-9",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Get me latest anomalies in the system",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestEventAgentSLODetection(t *testing.T) {
	k8sChain := AgentEvents{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-events-chain-10",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Get me latest SLO violations in the system",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestEventAgentAnamolyCount(t *testing.T) {
	k8sChain := AgentEvents{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-events-chain-10",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "get me total anomalies count in the system",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestEventNotFoundIssue(t *testing.T) {
	k8sChain := AgentEvents{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-events-chain-11",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				// Intentionally non-existent UUID — test verifies not-found handling.
				Query: "Get the details of Event with id - aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestEventAgentAnomalyByWorkload(t *testing.T) {
	eventsAgent := AgentEvents{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-events-chain-12",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Show me memory anomalies for llm-server in nudgebee namespace",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, eventsAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		fmt.Println(resp.Response)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, eventsAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

// TestEventAgentReviewLast24Hours tests the multi-event ReAct flow:
// >5 events should trigger manifest mode, then the agent drills into critical ones.
func TestEventAgentReviewLast24Hours(t *testing.T) {
	eventsAgent := AgentEvents{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-events-review-24h",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Review events from the last 24 hours. Identify the most critical ones and summarize what happened.",
		},
	}
	for _, tc := range testCases {
		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, eventsAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		fmt.Println(resp.Response)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, eventsAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

// TestEventAgentGroupRelatedAlerts tests grouping events by aggregation_key / pattern.
func TestEventAgentGroupRelatedAlerts(t *testing.T) {
	eventsAgent := AgentEvents{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-events-group-alerts",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Group all HIGH priority events from the last 7 days by their aggregation key. Show the count per group and which groups are most active.",
		},
	}
	for _, tc := range testCases {
		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, eventsAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		fmt.Println(resp.Response)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, eventsAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

// TestEventAgentFlagNoisyAlerts tests identifying noisy/repetitive alerts.
func TestEventAgentFlagNoisyAlerts(t *testing.T) {
	eventsAgent := AgentEvents{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-events-noisy-alerts",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Which alerts are the noisiest in the last 7 days? Find the ones that keep repeating.",
		},
	}
	for _, tc := range testCases {
		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, eventsAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		fmt.Println(resp.Response)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, eventsAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

// TestEventAgentIdentifyCritical tests identifying critical events that need immediate attention.
func TestEventAgentIdentifyCritical(t *testing.T) {
	eventsAgent := AgentEvents{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-events-critical",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Show me all CRITICAL and HIGH priority events from the last 24 hours. For each, tell me the resource affected and whether it has logs or deployment changes in its evidence.",
		},
	}
	for _, tc := range testCases {
		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, eventsAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		fmt.Println(resp.Response)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, eventsAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

func TestEventAgentAnomalyByType(t *testing.T) {
	eventsAgent := AgentEvents{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-events-chain-13",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "What CPU and latency anomalies have been detected in the last week?",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, eventsAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		fmt.Println(resp.Response)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, eventsAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}
