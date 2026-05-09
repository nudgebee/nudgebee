package agents

import (
	"encoding/json"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// TODO mock DBs
// TODO mock Tool Execution

var javaHeapDump string

func init() {
	content, _ := os.ReadFile("../testdata/java_heap_dumo.txt")
	javaHeapDump = string(content)
}

func TestK8sAgentReWoo_mcpInvocationTest(t *testing.T) {
	if os.Getenv("TEST_MCP_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" || os.Getenv("TEST_TENANT") == "" {
		t.Skip("skipping integration test: TEST_MCP_ACCOUNT, TEST_USER, TEST_TENANT not set")
	}
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-qg-chain-mcp",
				AccountId: os.Getenv("TEST_MCP_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Can you check https://nudgebee.com && provide summary using remote_fetch_mcp_fetch tool.`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		if err != nil {
			continue
		}

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Contains(t, string(invocationLog), "remote_fetch_mcp_fetch")
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_crossAccountTest(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" || os.Getenv("TEST_TENANT") == "" {
		t.Skip("skipping integration test: TEST_ACCOUNT, TEST_USER, TEST_TENANT not set")
	}
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-qg-chain-91",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query: `Can you investigate following GKE alert.

- Focus on dev postgres instance for query/connections etc
- GKE Cloud SQL for getting metrices, use 'gcp-dev - nudgebee-dev' account



{
  "incident": {
    "condition": {
      "conditionThreshold": {
        "aggregations": [
          {
            "alignmentPeriod": "600s",
            "perSeriesAligner": "ALIGN_SUM"
          }
        ],
        "comparison": "COMPARISON_GT",
        "duration": "0s",
        "filter": "resource.type = \"cloudsql_database\" AND metric.type = \"logging.googleapis.com/user/dev-pg-slow-queries\"",
        "thresholdValue": 5,
        "trigger": {
          "count": 1
        }
      },
      "displayName": "Slow queries (>10s) on dev PG > 5 in 10 min",
      "name": "projects/nudgebee-dev/alertPolicies/6563919164319058335/conditions/6563919164319059926"
    },
    "condition_name": "Slow queries (>10s) on dev PG > 5 in 10 min",
    "documentation": {
      "content": "More than 5 queries exceeding 10s execution time detected on beehive-dev-pg in the last 10 minutes. Check pg_stat_activity and Query Insights for details.",
      "mime_type": "text/markdown",
      "subject": "[RESOLVED - Warning] dev-pg-slow-queries"
    },
    "ended_at": 1774487730,
    "incident_id": "0.o5ws9jd5fksv",
    "metadata": {
      "system_labels": {},
      "user_labels": {}
    },
    "metric": {
      "displayName": "logging/user/dev-pg-slow-queries",
      "labels": {
        "log": "cloudsql.googleapis.com/postgres.log"
      },
      "type": "logging.googleapis.com/user/dev-pg-slow-queries"
    },
    "observed_value": "5.000",
    "policy_name": "dev-pg-slow-queries",
    "resource": {
      "labels": {
        "database_id": "nudgebee-dev:beehive-dev-pg",
        "project_id": "nudgebee-dev",
        "region": "us-central"
      },
      "type": "cloudsql_database"
    },
    "resource_display_name": "beehive-dev-pg",
    "resource_id": "",
    "resource_name": "nudgebee-dev beehive-dev-pg",
    "resource_type_display_name": "Cloud SQL Database",
    "scoping_project_id": "nudgebee-dev",
    "scoping_project_number": 137143602736,
    "severity": "Warning",
    "started_at": 1774487709,
    "state": "closed",
    "summary": "logging/user/dev-pg-slow-queries for nudgebee-dev beehive-dev-pg with metric labels {log=cloudsql.googleapis.com/postgres.log} returned to normal with a value of 5.000.",
    "threshold_value": "5",
    "url": "https://console.cloud.google.com/monitoring/alerting/alerts/0.o5ws9jd5fksv?channelType=webhook&project=nudgebee-dev"
  },
  "version": "1.2"
} `,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		if err != nil {
			continue
		}

		if resp.Status == core.ConversationStatusWaiting {
			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "gcp-dev - nudgebee-dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
		}

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_helloWorld(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-qg-chain-0",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `hello`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		if err != nil {
			continue
		}

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_ExtractUserMemory(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-qg-chain-mem-0",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `can you remember that  if i ask you to fetch logs of llm server you should alwyas look for llm-server deployment in nudgebee namespace`,
			},
			{
				SessionId: "ut-qg-chain-mem-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `can you remember that  if i ask you to fetch logs of llm server you should alwyas look for llm-server deployment in nudgebee namespace`,
			},
		}
	accountId := testCases[0].AccountId

	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		if err != nil {
			continue
		}

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)

		// Wait for async extraction to complete (Polling up to 60 seconds)
		fmt.Println("Waiting for async memory extraction (polling up to 60s)...")
		found := false
		maxAttempts := 12
		for i := 0; i < maxAttempts; i++ {
			// Verify memory was saved to DB
			memories, err := core.GetConversationDao().ListLongTermMemories(tc.AccountId, "", "", "", "", 10, 0)
			if err == nil {
				for _, m := range memories {
					// Look for our specific instruction keywords
					if strings.Contains(m.Content, "llm-server") && strings.Contains(m.Content, "nudgebee") {
						found = true
						// Verify it was categorized correctly as a preference
						assert.Equal(t, core.MemoryTypeUserPreference, m.MemoryType)
						fmt.Println("Successfully verified memory in DB on attempt", i+1, ":", m.Content)
						break
					}
				}
			}
			if found {
				break
			}
			fmt.Printf("Attempt %d: Memory not found yet, sleeping 5s...\n", i+1)
			time.Sleep(5 * time.Second)
		}
		assert.True(t, found, "Memory was not found in database after 60 seconds")
	}

	// After both runs: wait for any in-flight async extraction to finish, then
	// verify that dedup prevented duplicates — exactly 1 matching memory should exist.
	fmt.Println("Waiting 15s for any in-flight async extraction to complete...")
	time.Sleep(15 * time.Second)

	memories, err := core.GetConversationDao().ListLongTermMemories(accountId, "", "", "", "", 50, 0)
	assert.Nil(t, err)
	matchCount := 0
	for _, m := range memories {
		if strings.Contains(m.Content, "llm-server") && strings.Contains(m.Content, "nudgebee") {
			matchCount++
			fmt.Printf("Found memory [%s] type=%s content=%s\n", m.ID, m.MemoryType, m.Content)
		}
	}
	assert.Equal(t, 1, matchCount, "Expected exactly 1 matching memory after 2 identical runs, got %d (dedup failure)", matchCount)
}

func TestK8sAgentReWoo_helloWorld_Caching(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-qg-chain-0-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `give me the list of running pods in nudgebee namespace`,
			},
			{
				SessionId: "ut-qg-chain-0-1-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `give me the list of running pods in nudgebee namespace`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		if err != nil {
			continue
		}

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_ExecuteSlowness(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-qg-chain-8",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `fetch logs llm-server in nudgebee namespace..`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))

		if resp.Status == core.ConversationStatusWaiting {
			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_ExecuteGenericSearch(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-qg-chain-8-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `pl matches live score`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))

		if resp.Status == core.ConversationStatusWaiting {
			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_ExecuteRestarts(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "d3f9d86c-0cf3-41d3-b98e-09d31969d393",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `troubleshoot why test-pod-delete-force-deploy pods restarted in default namespace.`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_PodFailures(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-1.3-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `troubleshoot why nudgebee-agent-node-agent restarted in nudgebee-agent namespace?`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_HealthChecksWithDelegation(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-1.4-rewoo-delegate",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query: `Run a full dev-namespace health check as three parallel
  specialist investigations:

  1. Workload health — correlate pod restarts, OOM kills, and recent
     warning/error events to identify unstable workloads and root cause.
  2. Resource pressure — correlate node CPU/memory pressure with pod
     evictions and HPA scaling events in the dev namespace.
  3. Dependency health — check databases (postgres) reachability,
     connection pool saturation, and recent slow queries from dev workloads.
	 
	 use - 'dev-pg' instance for postgres
	 use - 'dev-aws' instance for aws
	 
	 `,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		if resp.Status == core.ConversationStatusWaiting {
			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

		if resp.Status == core.ConversationStatusWaiting {
			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

		if resp.Status == core.ConversationStatusWaiting {
			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

func TestK8sAgentReWoo_ScratchPadTest(t *testing.T) {
	// Use a moderate scratchpad budget to trigger compression once sub-agents
	// accumulate several tool call observations, while leaving enough readable
	// context for the agent to reason and emit <finish>.
	config.Config.LlmServerAgentMaxScratchpadChars = 5000
	config.Config.LlmServerScratchpadSummarizationEnabled = true

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-1.5-rewoo-scratchpad",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Can you do health check for nudgebee-test namespace, identify rootcause for failures`,
			},
		}
	for _, tc := range testCases {
		config.Config.LlmServerAgentMaxScratchpadChars = 5000
		config.Config.LlmServerScratchpadSummarizationEnabled = true
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		if resp.Status == core.ConversationStatusWaiting {
			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

		if resp.Status == core.ConversationStatusWaiting {
			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

		if resp.Status == core.ConversationStatusWaiting {
			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)

		// Validate compression visibility: with the lowered scratchpad budget (5000 chars),
		// compression should have been triggered and a context_compression record persisted.
		compressionCount := core.CountCompressionVisibilityRecords(resp.ConversationId)
		t.Logf("compression visibility records: %d", compressionCount)
		assert.Greater(t, compressionCount, 0, "expected at least one compression visibility record in the conversation")
	}

}

func TestK8sAgentReWoo_HealthChecksWithAdditionalContext(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "0458e085-f7a6-40e8-b13d-a9b1bc1c9e70",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Can you do health check for nudgebee-test namespace, identify rootcause for failures`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		if resp.Status == core.ConversationStatusWaiting {
			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

		if resp.Status == core.ConversationStatusWaiting {
			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

		if resp.Status == core.ConversationStatusWaiting {
			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_BreakDownSubProblemsToOneTaskForTool(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-2.1-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Get a summary of all Deployments, StatefulSets, DaemonSets, and Jobs running in the cluster. Include their names, namespaces, replica counts, and status.`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		if err != nil {
			continue
		}

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_ExecuteSecurity(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-1.15-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `list all cis security issues ?`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		if err != nil {
			continue
		}

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_ExecuteOom(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-1-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Can you investigate recent oom issue in redis namespace`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		if err != nil {
			continue
		}

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_RepeatedCommandsReWoo(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-15-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "show me recent issues in my cluster.",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_InvestigateRestarts(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-15-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Can you investigate restarts of ml-k8s-server in nudgebee namespace.",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_ExecuteOomCluster(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-21-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Can you check for recent out of memory issues and investigate them",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sChain := newK8sDebugAgent(tc.AccountId)

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

func TestPostgresDebugAgent_IdentifyCorrectIndex2(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-postgres-chain-3-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "can you check postgres events table does it require any additional indexes.",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		debugAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, debugAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)

		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)
		if resp.Status == core.ConversationStatusWaiting {
			resp, err = core.HandleConversationSessionRequest(sc, debugAgent, tc.UserId, tc.AccountId, tc.SessionId, "dev-pg", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

		assert.Equal(t, resp.AgentName, debugAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_ExecuteOom2(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-2-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Can you investigate recent oom issue in rabbit-test namespace`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		if err != nil {
			continue
		}

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_ExecutePVCDetails(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-3-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Can you review current PVC storage and available storage across all the PVCs`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		if err != nil {
			continue
		}

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_ExecuteGenerateSampleYaml(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-4-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Can you generate sample yaml file for running nginx container`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		if err != nil {
			continue
		}

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_ExecuteRCAForEventId(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-5-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Investigate event with Id 3bc76326-fb21-4195-b3ac-2db1f56db268`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query, core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{
			ToolConfigs: map[string]string{"rabbit_execute": "dev"},
		}))

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_ExecuteRCAForEventId2(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-5.2-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Investigate event with Id b8cd903e-ebc2-4dc8-b1bd-bfad2e75a13f`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query, core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{
			ToolConfigs: map[string]string{"rabbit_execute": "dev"},
		}))

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_ExecutePostgresTool(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-6-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `can you tell me number of postgres connections for nudgebee database`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)
		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query, core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{
			ToolConfigs: map[string]string{"postgres_query_execute": "dev-pg"},
		}))

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_RepeatedCommands(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-7-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "show me recent issues in my cluster",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sChain := newK8sDebugAgent(tc.AccountId)

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

func TestK8sAgentReWoo_PostgresEvent(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-8-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Describe table schema for events table",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sChain := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)

		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)
		resp, err = core.HandleConversationSessionRequest(sc, k8sChain, tc.UserId, tc.AccountId, tc.SessionId, "dev-pg", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
			UUID:  messageId,
			Valid: true,
		}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
			UUID:  agentId,
			Valid: true,
		}))
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_Prometheus(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-10-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Can you tell me max memory used by llm-server in nudgebee namespace",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sChain := newK8sDebugAgent(tc.AccountId)

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

func TestK8sAgentReWoo_PodRestart(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-11-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Can you investigate recent pod restart of nudgebee-reddit in nudgebee namespace",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sChain := newK8sDebugAgent(tc.AccountId)

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

func TestK8sAgentReWoo_PodLogs2(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-12-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Can you review services-server logs in nudgebee namespace , identify issues & recommend solution",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sChain := newK8sDebugAgent(tc.AccountId)

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

func TestK8sAgentReWoo_PodRestart2(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-13-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "my cloud-collector-server app in nudgebee namespace recently restarted, can you investigate why and suggest resolution",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sChain := newK8sDebugAgent(tc.AccountId)

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

func TestK8sAgentReWoo_NodeIssue(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-14-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "My k8s Nodes are getting frequently removed, can you investigate why ? I am using karpenter so check that as well in karpenter namespace",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sChain := newK8sDebugAgent(tc.AccountId)

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

func TestK8sAgentReWoo_GetVersion(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-15-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "what version of nudgebee-agent-runner is running in my cluster",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sChain := newK8sDebugAgent(tc.AccountId)

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

func TestK8sAgentReWoo_GetVersion2(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-16-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "what image version of nudgebee-agent-runner is running in my cluster",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sChain := newK8sDebugAgent(tc.AccountId)

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

func TestK8sAgentReWoo_GetVersion3(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-17-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "what is current version of app-dev",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sChain := newK8sDebugAgent(tc.AccountId)

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

func TestK8sAgentReWoo_WithPlannerSlowness(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-18-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "I am observing slowness on app-dev in nudgebee namespace, can you debug",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sChain := newK8sDebugAgent(tc.AccountId)

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

func TestK8sAgentReWoo_WithPlannerRestart(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-19-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Investigate Recent Restarts of the application",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sChain := newK8sDebugAgent(tc.AccountId)

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

func TestK8sAgentReWoo_WithPlannerTraces(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-20-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "can you analyze api failures for k8s collector in nudgebee namespace and suggest code fixes with proper formatting",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sChain := newK8sDebugAgent(tc.AccountId)

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

func TestK8sAgentReWoo_WithJobDebug(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-21-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "I have some Kubernetes Jobs that are not completing successfully. How can I troubleshoot this?",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sChain := newK8sDebugAgent(tc.AccountId)

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

func TestK8sAgentReWoo_WithJobDebug_ExecuteCRUD(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-22-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Can you check network connectivity to google.com by launching new pod using 'weibeld/ubuntu-networking' image in nudgebee-agent namespace ?",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)

		// based on response return followup response and wait for processing
		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		assert.Nil(t, resp.Status, core.ConversationStatusWaiting)

		resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "yes", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
			UUID:  messageId,
			Valid: true,
		}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
			UUID:  agentId,
			Valid: true,
		}))

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_WithPrometheusTesting(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-24-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "tell me number of request per hour for auto pilot server in nudgebee namespace",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		prometheusChain := newPrometheusAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		fmt.Println("response - ", resp.Response)
		fmt.Println("tools - ", resp.AgentStepResponse)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_RAGTEsting(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-25-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "My app is slow how I can troubleshoot this",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		template := k8sAgent.GetSystemPrompt(sc, core.NBAgentRequest{
			Query:          tc.Query,
			ConversationId: tc.SessionId,
			AccountId:      tc.AccountId,
			UserId:         tc.UserId,
		})
		fmt.Println("template - ", template)

	}
}

func TestK8sAgentReWoo_DebugPodRestart(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-25-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Can you investigate why services-server-8457954bf7-f8f4d restarted ?",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWoo_DebugPodTraversingOtherPodsForIssue(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-28-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "I am observing errors in frontend in od namespace, can you investigate",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWoo_PlannerWithoutToolUsage(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-29-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "command to retrive rabbitmq password and username from secret",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWoo_PlannerWithoutToolUsage1(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-30-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "What is PDB",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWoo_PlannerWithoutToolUsage2(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-31-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "I have done helm installation sometime back on one of my namespace.. recently observed that help is complaining that no installation found.. even thougn i can see workloads running any idea ?",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, err)

		// based on response return followup response and wait for processing
		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		assert.Nil(t, resp.Status, core.ConversationStatusWaiting)

		resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "victoria", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
			UUID:  messageId,
			Valid: true,
		}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
			UUID:  agentId,
			Valid: true,
		}))

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWooPlannerWithoutToolUsage3(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-31-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "recently relay server has started going OOM , can you check any recent change that might have caused the issue ?",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, err)

		// based on response return followup response and wait for processing
		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		assert.Nil(t, resp.Status, core.ConversationStatusWaiting)

		resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "nudgebee", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
			UUID:  messageId,
			Valid: true,
		}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
			UUID:  agentId,
			Valid: true,
		}))

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWoo_PlannerWithGithubIssuesIntegration(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-32-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query: `@k8s_debug  Can you review #15192 issue in nudgebee/nudgebee. 
- Provide relevant shell script which can be used to solve this issue.
- Add comment on github issue with proposed shell script, so that developer can use it for implementation. You need to add solution in the comments`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, err)

		// based on response return followup response and wait for processing
		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		assert.Nil(t, resp.Status, core.ConversationStatusWaiting)

		resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "yes", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
			UUID:  messageId,
			Valid: true,
		}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
			UUID:  agentId,
			Valid: true,
		}))

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWoo_PlannerWithCondition(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-33-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `"If the 'kube-system' namespace exists, then list pods in the 'default' namespace. If listing pods is successful and pods are found, then get logs for the first pod found in 'default'."`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWoo_PlannerWithCondition2(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-34-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query: `check for rabbitmq messages in queue auto_playbook_task, if messages are greater than 20, 
							then check for pod status of auto-pilot-worker in nudgebee namespace if it looks ok, then scale it by 1. 
							if pod has errors then check for logs to identify issue`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)

		// based on response return followup response and wait for processing
		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		assert.Equal(t, resp.Status, core.ConversationStatusWaiting)

		resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
			UUID:  messageId,
			Valid: true,
		}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
			UUID:  agentId,
			Valid: true,
		}))

		assert.Nil(t, err)
		assert.NotNil(t, resp)

		if resp.Status == core.ConversationStatusWaiting {
			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)

			assert.Equal(t, resp.Status, core.ConversationStatusWaiting)

			resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "yes", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))

			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

	}
}

func TestK8sAgentReWoo_PlannerWithCondition2_1(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-34_1-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query: `check for rabbitmq messages in queue auto_playbook_task, if messages are greater than 100, 
							then check for pod status of auto-pilot-worker in nudgebee namespace if it looks ok, then scale it by 1. 
							if pod has errors then check for logs to identify issue`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)

		// based on response return followup response and wait for processing
		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		assert.Nil(t, resp.Status, core.ConversationStatusWaiting)

		resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
			UUID:  messageId,
			Valid: true,
		}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
			UUID:  agentId,
			Valid: true,
		}))

		assert.Nil(t, err)
		assert.NotNil(t, resp)

	}
}

func TestK8sAgent_WithServiceMap(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-35-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Can you get me dependencies of services-server in nudgebee namespace",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sChain := newK8sDebugAgent(tc.AccountId)

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

func TestK8sAgent_WithServiceMap2(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-35.1-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Can you get me dependencies of pod app-dev-777b8f7c75-vbd6c in nudgebee namespace",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sChain := newK8sDebugAgent(tc.AccountId)

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

func TestK8sAgentReWooFollowupApproval(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-36-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Update rag-server in nudgebee namespace to 2 replicas`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)

		// based on response return followup response and wait for processing
		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		assert.Equal(t, resp.Status, core.ConversationStatusWaiting)

		resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "yes", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
			UUID:  messageId,
			Valid: true,
		}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
			UUID:  agentId,
			Valid: true,
		}))

		assert.Nil(t, err)
		assert.NotNil(t, resp)

		if resp.Status == core.ConversationStatusWaiting {
			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)

			assert.Equal(t, resp.Status, core.ConversationStatusWaiting)

			resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "yes", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))

			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

	}
}

func TestK8sAgentReWooTroubleshotE2E(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-37-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Can you analyze recent OOM error of cloud-collector-server-666c9856bb-thtzb in nudgebee namespace`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWooTroubleshotE2E2(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-37.1-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `My hasura deployment in nudgebee namespace restarted recently, can you investigate why?`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWooTroubleshotE2E3(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-37.2-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Can you investigate issue with pod nb-node-sample-app-deployment-5ddf9584c5-g5x7j in nudgebee-test namespace`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWooTroubleshotE2E4(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-37.3-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Can you investigate recent restarts with hasura-679fc7c5c6-dj9vv pod in nudgebee namespace`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWooTroubleshotE2E5(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-37.4-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Can you investigate recent failures with 'ad' deployment in otel-demo namespace`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWooTroubleshotE2E6(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-37.5-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Get memory, cpu, logs, traces for llm-server in nudgebee namespace parallel and summarise them`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWooTroubleshot(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-37.6-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Get memory, cpu, logs, traces for llm-server in nudgebee namespace parallel and summarise them`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWooTroubleshotE2E7(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-37.6-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Can you execute curl reques to test internet connectivity  using 'shell_execute_agent' tool`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWooTroubleshotE2E8(t *testing.T) {

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-37.7-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Can you trigger trigger_pluto_scan`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWooTroubleshot_RegistryStatus(t *testing.T) {

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-37.7.1-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Can you help me check the status of the 'registry.nudgebee.com' registry for any ongoing issues?`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWooTroubleshotE2E9_LargePrompts(t *testing.T) {

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-37.8.1-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query: `Context:
You are a Kubernetes assistant responsible for monitoring and diagnosing the health of applications running in the 'nudgebee' namespace. These applications depend on PostgreSQL and RabbitMQ. Metrics are available via Prometheus. Tracing data (if available) is exposed via OpenTelemetry-compatible backends.

Task:
Evaluate the health of the applications in the 'nudgebee' namespace by performing the following steps:

Instruction (Conditional Execution Flow):

1. **Check Application Status**:
   - List all pods in 'nudgebee' namespace.
   - For each pod, check if the pod status is not 'Running' or 'Completed'.
   - If any pod is in a CrashLoopBackOff or OOMKilled state:
     - Fetch the last 50 lines of logs for the failing container.
     - Report the reason and error message.

2. **Auto-discover Dependencies**:
   - Identify if 'postgres' and 'rabbitmq' deployments or statefulsets are present in the 'nudgebee' namespace or referenced via service names.
   - If found, tag them as dependencies and evaluate their health using the relevant metrics.

3. **Check Prometheus Metrics**:

   3.1 **General App Health**:
   - 'up{namespace="nudgebee"}' — Ensure all targets are up.
   - 'container_cpu_usage_seconds_total', 'container_memory_usage_bytes' for resource usage over time.

   3.2 **PostgreSQL Health**:
   - 'pg_stat_activity_count' or 'pg_stat_activity{datname="*"}'
   - 'pg_database_size_bytes', 'pg_locks_count', 'pg_stat_database_blks_hit / (pg_stat_database_blks_hit + pg_stat_database_blks_read)' (cache hit rate)
   - Alert if cache hit ratio < 0.99 or if connections exceed configured limits.

   3.3 **RabbitMQ Health**:
   - 'rabbitmq_queue_messages', 'rabbitmq_queue_messages_ready', 'rabbitmq_queue_messages_unacked'
   - 'rabbitmq_channel_messages_published_total', 'rabbitmq_channel_messages_delivered_total'
   - 'rabbitmq_disk_space_available_bytes', 'rabbitmq_build_info'

   3.4 **Error Rate and Latency**:
   - 'container_http_requests_total{destination_workload_namespace="nudgebee", status=~"5.."}'
   - 'rate(container_http_requests_total{destination_workload_namespace="nudgebee",status=~"5.."}[5m])' > threshold (e.g., 1)
   - 'histogram_quantile(0.95, rate(container_http_requests_duration_seconds_total_bucket{destination_workload_namespace="nudgebee"}[5m]))' for high latency

4. **Check for Spikes and Anomalies in Metrics**:
   - Identify any recent spike in error rate, memory, CPU, or request latency over the past 1 hour.
   - Highlight deltas compared to a 1-day baseline.

5. **Traces Analysis (if enabled)**:
   - Check traces for high-latency spans or spans with errors in the last 30 minutes.
   - Identify top slow operations or repeated failed DB queries.
   - Suggest code-level optimizations if bottlenecks are found.

6. **Final Output**:
   - Summary: Healthy / Degraded / Failing
   - Key Issues:
     - Pod-level failures
     - Postgres/RabbitMQ issues
     - Latency/Error anomalies
     - Optimization insights from traces
   - Recommendation:
     - e.g., “Postgres cache hit ratio is below optimal. Add indexes or increase memory.”
     - “RabbitMQ unacked messages are piling up — check consumer throughput.”

Scenario:
The assistant should adapt the depth of analysis depending on the presence or absence of metrics/logs. If traces are not available, skip step 5. If no anomalies are found, return a healthy status with usage summaries.

History:
Past reports have flagged RabbitMQ memory pressure and high latency in API endpoints during peak traffic hours.
`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWooTroubleshotE2E10_Datamatch(t *testing.T) {

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-37.9-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query: `can you get me all the pods with IPs, across all namespaces -- [{"client_addr":"192.168.1.10","count":"4"},
{"client_addr":"103.197.75.144","count":"3"},
{"client_addr":"192.168.1.2","count":"4"},
{"client_addr":"192.168.1.7","count":"5"},
{"client_addr":"192.168.1.8","count":"5"},
{"client_addr":"192.168.1.3","count":"8"},
{"client_addr":"192.168.1.11","count":"9"},
{"client_addr":"192.168.1.5","count":"1"},
{"client_addr":"192.168.1.6","count":"16"}]`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgent_ExternalNodes(t *testing.T) {

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-40-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Can you check filesystem usage of host nb-dev-db`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWooInvestigate1(t *testing.T) {

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-39-rewoo-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Can you investigate the performance issues in the cluster?`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWooInvestigateDebuggerFromDB(t *testing.T) {

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-39-rewoo-debugger-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Can you investigate the performance issues in the cluster?`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent, ok := core.GetNBAgent(sc, AgentK8sDebugName, tc.AccountId, core.AgentStatusEnabled)
		assert.True(t, ok)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sDebuggerTracesAnalysis(t *testing.T) {

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-trace-debug-3",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `can you investigate why /api/web/search/analytics for frontend-service app in demo namespace was slow since last week?`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sDebuggerWebSearch(t *testing.T) {

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-websearch-debug-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Can you help me check the status of the "registry.nudgebee.com" registry for any ongoing issues?`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sDebuggerLogs(t *testing.T) {

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-websearch-debug-2",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Get logs for llm-server in nudgebee namespace and summarize any errors found`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sDebuggerStatus(t *testing.T) {

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-status-debug-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Why logs are not connected ?`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sDebugOd(t *testing.T) {

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-debug-od-2",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `investigate flagd in od namespace`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sDebugMultistep2(t *testing.T) {

	testCases :=
		[]struct {
			SessionId string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-debug-multi-conversations-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "can u get me status of nudgebee namespace ?")
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.Status, core.ConversationStatusCompleted)
		resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "was there any recent restarts ?")
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.Status, core.ConversationStatusCompleted)
		resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "ok.. can you check max memory usage of llm-server in last 24 hours ?")
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.Status, core.ConversationStatusCompleted)
	}
}

func TestK8sAgentReWoo_FunctionExecution(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-40-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "/call check_ci_failure investigate failure of action `llm-server-dev-Civo CI` in nudgebee/nudgebee repo",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		if resp.Status == core.ConversationStatusWaiting {
			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, "Kankshit", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWoo_JavaHeapDump(t *testing.T) {

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-38-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     fmt.Sprintf(`Can you analyze below java heap dump and provide insights. Do not investigate \n\n %s`, javaHeapDump),
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWoo_VisualizationArchitecture(t *testing.T) {

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-viz-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Visualize the architecture of llm-server deployment in nudgebee namespace using a diagram",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWoo_VisualizationCharts(t *testing.T) {

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-viz-2",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Get me a chart of memory usage of ml-k8s-server deployment in nudgebee namespace",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgent_ResposnseContentFormatJson(t *testing.T) {

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-48-rewoo",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `get pod list in nudgebee namespace in json format only with fields name, status, node, ip address`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Greater(t, len(resp.Response), 0)

		// Check if the response is valid JSON (stripping Markdown code blocks if present)
		for _, r := range resp.Response {
			// Strip markdown code blocks
			cleaned := r
			if len(cleaned) >= 7 && cleaned[:3] == "```" {
				// Find the first newline to strip the language identifier (e.g., ```json)
				if idx := 0; idx < len(cleaned) {
					// finding first new line
					for i, c := range cleaned {
						if c == '\n' {
							idx = i
							break
						}
					}
					if idx > 0 {
						cleaned = cleaned[idx+1:]
					}
				}
				// Remove trailing ```
				if len(cleaned) >= 3 && cleaned[len(cleaned)-3:] == "```" {
					cleaned = cleaned[:len(cleaned)-3]
				}
			}

			var js map[string]interface{}
			err = json.Unmarshal([]byte(cleaned), &js)
			if err != nil {
				// Try array of objects
				var jsArr []map[string]interface{}
				err = json.Unmarshal([]byte(cleaned), &jsArr)
			}
			assert.Nil(t, err, "Response should be valid JSON: %s", r)
		}
	}
}

func TestK8sAgentReWoo_CustomAgent(t *testing.T) {

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-custom-agent-3",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `check pods workflow-server-74546c54bb-tghp7 and get me complete detail`,
			},
		}
	for _, tc := range testCases {

		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		agent, ok := core.GetNBAgent(sc, "test_code_agent", tc.AccountId, core.AgentStatusEnabled)
		if !ok {
			t.Fatalf("test_code_agent not found")
		}
		resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWoo_Workspace1(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("TEST_ACCOUNT not set")
	}
	originalVal := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = true
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = originalVal })

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-workspace-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `can you create a todo file for me in my workspace specifying security vulns i have.`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		agent := newK8sDebugAgent(tc.AccountId)
		resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWoo_ThinkTool(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("TEST_ACCOUNT not set")
	}
	originalVal := config.Config.LlmServerThinkToolEnabled
	config.Config.LlmServerThinkToolEnabled = true
	t.Cleanup(func() { config.Config.LlmServerThinkToolEnabled = originalVal })

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-k8s-think-tool-1",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			// Conflicting signals query — designed to trigger the think tool
			Query: `Pod checkout-svc is in CrashLoopBackOff with OOMKilled, but memory metrics show usage at only 40% of limit. Other pods on the same node are fine. What is the root cause?`,
		},
	}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		agent := newK8sDebugAgent(tc.AccountId)
		resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		if err != nil {
			continue
		}

		assert.NotNil(t, resp)
		assert.Greater(t, len(resp.Response), 0)

		// Log tool invocations for manual inspection
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		t.Log("tools - ", string(invocationLog))

		// Log which tools were called — the LLM may or may not choose to call think
		toolNames := []string{}
		for _, step := range resp.AgentStepResponse {
			toolNames = append(toolNames, step.Response.Name)
		}
		t.Log("tool names used: ", strings.Join(toolNames, ", "))
	}
}

func TestK8sAgentReWoo_Workspace2(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("TEST_ACCOUNT not set")
	}
	originalVal := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = true
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = originalVal })

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-k8s-chain-workspace-2",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `can you check if internet is working fine?`,
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		agent := newK8sDebugAgent(tc.AccountId)
		resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestK8sAgentReWoo_crossAccountTest_AWS(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" || os.Getenv("TEST_TENANT") == "" || os.Getenv("TEST_AWS_ACCOUNT") == "" {
		t.Skip("skipping integration test: TEST_ACCOUNT, TEST_USER, TEST_TENANT, or TEST_AWS_ACCOUNT not set")
	}
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-qg-chain-aws-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Using the shell tool, check the current AWS caller identity by running `aws sts get-caller-identity`.",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId, os.Getenv("TEST_AWS_ACCOUNT")})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query, core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{
			ToolConfigs: map[string]string{
				"aws_execute": "dev-aws",
			},
		}))

		assert.Nil(t, err)
		if err != nil {
			continue
		}

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_crossAccountTest_Azure(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" || os.Getenv("TEST_TENANT") == "" || os.Getenv("TEST_AZURE_ACCOUNT") == "" {
		t.Skip("skipping integration test: TEST_ACCOUNT, TEST_USER, TEST_TENANT, or TEST_AZURE_ACCOUNT not set")
	}
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-qg-chain-az-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Run `az account show` using the shell tool. If you have multiple Azure accounts, select the primary one.",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId, os.Getenv("TEST_AZURE_ACCOUNT")})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query, core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{
			ToolConfigs: map[string]string{
				"azure_execute": "nudgebee-azure",
			},
		}))

		assert.Nil(t, err)
		if err != nil {
			continue
		}

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestK8sAgentReWoo_crossAccountTest_GCP(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" || os.Getenv("TEST_TENANT") == "" || os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("skipping integration test: TEST_ACCOUNT, TEST_USER, TEST_TENANT, or TEST_ACCOUNT not set")
	}
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-qg-chain-gcp-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Run `gcloud auth list` using the shell tool. If you have multiple GCP accounts, select the primary one.",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId, os.Getenv("TEST_ACCOUNT")})

		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query, core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{
			ToolConfigs: map[string]string{
				"gcloud_execute": "gcp-dev - nudgebee-dev",
			},
		}))

		assert.Nil(t, err)
		if err != nil {
			continue
		}

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))
		t.Log("tools - ", string(invocationLog))
		assert.Equal(t, resp.AgentName, k8sAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

// TestK8sAgentReWoo_AllowedToolsCapability validates the allowed_tools capability end-to-end
// across three scenarios identified during code review:
//
//  1. Narrow allowlist — only "metrics" is allowed on a broad cluster-health query.
//     Verifies that kubectl/logs/events are blocked and metrics is invoked.
//
//  2. shell_execute injection gate — "kubectl" and "logs" are allowed but NOT shell_execute.
//     FilterAndInjectDefaultTools injects shell_execute as a default when the feature flag
//     is on; step-4 re-filtering must strip it before the planner sees it.
//     Known nuance: the k8s_debug system prompt's tool_usage_instructions still mentions all
//     tools (including blocked ones) because it is built from GetSupportedTools() before
//     capabilities are applied. For react_3 this is harmless — function schemas are the hard
//     enforcement boundary. For ReWOO the plan critiquer re-validates against filtered tools
//     and rejects any plan step referencing a blocked tool.
//
//  3. Empty allowed_tools — passing allowed_tools:[] must be a no-op (no filtering).
//     Regression guard: an empty list must NOT mean "block everything".
func TestK8sAgentReWoo_AllowedToolsCapability(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" || os.Getenv("TEST_TENANT") == "" {
		t.Skip("skipping integration test: TEST_ACCOUNT, TEST_USER, TEST_TENANT not set")
	}

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	tenant := os.Getenv("TEST_TENANT")

	// toolInvoked returns true if the named tool appears as an actual function call
	// in the invocation log. Uses the JSON field pattern "name":"<tool>" to avoid
	// false positives from tool names appearing inside response content text
	// (e.g. "logs" appearing in a metrics response, "kubectl" in an explanation).
	toolInvoked := func(invocationStr, toolName string) bool {
		return strings.Contains(invocationStr, fmt.Sprintf(`"name":"%s"`, toolName))
	}

	runCase := func(t *testing.T, sessionId, query string, capabilities map[string]any) (resp core.NBAgentResponse, invocationStr string) {
		t.Helper()
		sc := security.NewRequestContextForTenantAccountAdmin(tenant, userId, []string{accountId})
		k8sAgent := newK8sDebugAgent(accountId)

		err := core.DeleteConversationBySession(sessionId, accountId, userId)
		assert.Nil(t, err)

		var opts []core.ConversationSessionRequestConfig
		if capabilities != nil {
			opts = append(opts, core.ConversationSessionRequestWithCapabilities(capabilities))
		}

		resp, err = core.HandleConversationSessionRequest(sc, k8sAgent, userId, accountId, sessionId, query, opts...)
		assert.Nil(t, err)

		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		invocationStr = string(invocationLog)
		fmt.Printf("[%s] response: %s\n", sessionId, resp.Response)
		fmt.Printf("[%s] tools invoked: %s\n", sessionId, invocationStr)
		t.Logf("[%s] tools invoked: %s", sessionId, invocationStr)

		assert.Equal(t, AgentK8sDebugName, resp.AgentName)
		assert.Greater(t, len(resp.Response), 0)
		return
	}

	// ── Case 1: narrow allowlist ──────────────────────────────────────────────
	// Query normally triggers kubectl + logs + events + metrics.
	// Only "metrics" is allowed; all others must be absent from the invocation log.
	t.Run("narrow_allowlist_metrics_only", func(t *testing.T) {
		_, invocationStr := runCase(t,
			"ut-allowed-tools-metrics-only",
			`Check the overall health of the cluster — CPU and memory usage trends over the last hour.`,
			map[string]any{"allowed_tools": []string{MetricsAgentName}},
		)

		// Positive: the allowed tool must have been invoked.
		assert.True(t, toolInvoked(invocationStr, MetricsAgentName),
			"metrics must be invoked when it is the only allowed tool")

		// Negative: blocked tools must not appear as function calls.
		for _, blocked := range []string{KubectlAgentName, LogsAgentName, EventsAgentName, toolcore.ToolExecuteShellCommand} {
			assert.False(t, toolInvoked(invocationStr, blocked),
				"tool %q must not be invoked when allowed_tools=[%s]", blocked, MetricsAgentName)
		}
	})

	// ── Case 2: shell_execute injection gate ─────────────────────────────────
	// kubectl and logs are allowed, but shell_execute is NOT in the allowlist.
	// FilterAndInjectDefaultTools injects shell_execute as a default when the
	// feature flag is on; the step-4 re-filter must strip it before the planner.
	t.Run("shell_execute_blocked_after_injection", func(t *testing.T) {
		_, invocationStr := runCase(t,
			"ut-allowed-tools-no-shell",
			`Check pod restart counts and recent error logs for the default namespace.`,
			map[string]any{"allowed_tools": []string{KubectlAgentName, LogsAgentName}},
		)

		// shell_execute must be blocked even though it can be injected as a default.
		assert.False(t, toolInvoked(invocationStr, toolcore.ToolExecuteShellCommand),
			"shell_execute must not be invoked when it is absent from allowed_tools")

		// At least one of the allowed tools must have been used.
		assert.True(t, toolInvoked(invocationStr, KubectlAgentName) || toolInvoked(invocationStr, LogsAgentName),
			"at least one of kubectl/logs must be invoked when they are the only allowed tools")
	})

	// ── Case 3: empty allowed_tools = no restriction ─────────────────────────
	// An empty slice must be treated as "no filter", not "block everything".
	// This is a regression guard: len(allowedTools)==0 must short-circuit filtering.
	t.Run("empty_allowed_tools_no_restriction", func(t *testing.T) {
		resp, _ := runCase(t,
			"ut-allowed-tools-empty",
			`What is the current status of the cluster?`,
			map[string]any{"allowed_tools": []string{}},
		)

		// With no restriction the agent should respond meaningfully.
		assert.Greater(t, len(resp.Response), 50,
			"empty allowed_tools must not block all tools — agent should produce a real response")
	})
}
