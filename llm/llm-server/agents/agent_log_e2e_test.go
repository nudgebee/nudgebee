//go:build e2e

package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLogAgent(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-log-chain-1-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Get me recent logs of app llm-server in nudgebee namespace",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		logAgent, err := getLogAgent(sc, tc.AccountId)
		assert.Nil(t, err)

		err = core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		resp, err := core.HandleConversationSessionRequest(sc, logAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, logAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestLogChainExecuteLogLoki2(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-log-loki-chain-2",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Get me recent logs of app kube-dns in nudgebee kube-system namespace and add limit 100",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		logAgent, err := getLogAgent(sc, tc.AccountId)
		assert.Nil(t, err)
		err = core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		resp, err := core.HandleConversationSessionRequest(sc, logAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, logAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

func TestLogChainExecuteLogLoki4(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-log-loki-chain-4",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Get me recent logs of app that has error in nudgebee namespace",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		logAgent, err := getLogAgent(sc, tc.AccountId)
		assert.Nil(t, err)
		err = core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		resp, err := core.HandleConversationSessionRequest(sc, logAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, logAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

func TestLogChainExecuteLogLoki6(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-log-loki-chain-6",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Get logs from the last 30 minutes",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		logAgent, err := getLogAgent(sc, tc.AccountId)
		assert.Nil(t, err)
		err = core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		resp, err := core.HandleConversationSessionRequest(sc, logAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, logAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

func TestLogChainExecuteBenchmarks(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-log-loki-bench-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "logs from the nudgebee namespace within the last 5 minutes?",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		logAgent, err := getLogAgent(sc, tc.AccountId)
		assert.Nil(t, err)
		err = core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		resp, err := core.HandleConversationSessionRequest(sc, logAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, logAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

func TestLogChainExecuteLogLokiTimeScenarios(t *testing.T) {
	currentTime := time.Now()
	currentTime30MinsBefore := currentTime.Add(-30 * time.Minute)

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-log-loki-chain-time-filters",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Show me error logs for app kube-dns in kube-system namespace from the last 2 hours",
			},
			{
				SessionId: "ut-log-loki-chain-absolute-time",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     fmt.Sprintf("Get logs of app  kube-dns  between %s and %s", currentTime30MinsBefore.Format(time.RFC3339), currentTime.Format(time.RFC3339)),
			},
			{
				SessionId: "ut-log-loki-chain-around-time",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     fmt.Sprintf("Get logs of app  kube-dns around %s", currentTime.Format("2006-01-02 15:04:05")),
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		logAgent, err := getLogAgent(sc, tc.AccountId)
		assert.Nil(t, err)
		err = core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		resp, err := core.HandleConversationSessionRequest(sc, logAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, logAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

func TestLogChainExecuteLogLokiTimezoneScenarios(t *testing.T) {
	currentTime := time.Now()
	currentTime30MinsBefore := currentTime.Add(-30 * time.Minute)
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-log-loki-chain-no-timezone",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     fmt.Sprintf("Get logs between %s and %s", currentTime30MinsBefore.Format(time.RFC3339), currentTime.Format(time.RFC3339)),
			},
			{
				SessionId: "ut-log-loki-chain-date-only",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     fmt.Sprintf("Get logs for the day %s", currentTime.Format("2006-01-02")),
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		logAgent, err := getLogAgent(sc, tc.AccountId)
		assert.Nil(t, err)
		err = core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		resp, err := core.HandleConversationSessionRequest(sc, logAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, logAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

func TestLogChainExecuteESIndexScenarios(t *testing.T) {
	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-log-es-index-mapping",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Show me nginx access logs from the last hour",
		},
		{
			SessionId: "ut-log-es-index-explicit",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Get logs from the index 'app-logs-*' where level is error",
		},
		{
			SessionId: "ut-log-es-index-complex",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Find 404 errors in nginx logs for the last 30 minutes",
		},
	}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		logAgent, err := getLogAgent(sc, tc.AccountId)
		assert.Nil(t, err)

		err = core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, logAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, logAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)

		// Verify that at least one generated tool call contains the index field
		foundIndex := false
		for _, invocation := range resp.AgentStepResponse {
			if invocation.Call.FunctionCall != nil && invocation.Call.FunctionCall.Arguments != "" {
				if strings.Contains(invocation.Call.FunctionCall.Arguments, `"index"`) {
					foundIndex = true
					break
				}
			}
		}
		assert.True(t, foundIndex, "Expected at least one tool call to contain the 'index' parameter")
	}
}
