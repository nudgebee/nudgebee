package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	toolcore "nudgebee/llm/tools/core"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestLogAgent_BuildToolList: the LLM-visible tool surface must be uniform
// across providers — provider-specific dispatch lives inside fetch_logs.
// Re-introducing any of the mustNotHave tools would expose the per-provider
// pipeline to the LLM and bypass the investigation classifier.
func TestLogAgent_BuildToolList(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	mustHave := []string{ResourceSearchAgentName, FetchLogsAgentName}
	mustNotHave := []string{
		"query_generator",
		"datadog_log_query",
		"kubectl_intent_generator",
		"kubectl_execute",
	}

	cases := []struct {
		name     string
		provider string
	}{
		{"loki provider", "loki"},
		{"signoz provider", "signoz"},
		{"es provider", "es"},
		{"elasticsearch provider", "elasticsearch"},
		{"datadog provider", "datadog"},
		{"no provider falls back to kubectl-only path", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent := newLogAgent("test-account",
				services_server.ObservabilityProvider{Provider: tc.provider})
			tools := agent.GetSupportedTools(ctx)
			names := toolNamesForTest(tools)

			for _, expected := range mustHave {
				assert.Contains(t, names, expected,
					"provider %q must expose %q (uniform across all providers)", tc.provider, expected)
			}
			for _, forbidden := range mustNotHave {
				assert.NotContains(t, names, forbidden,
					"provider %q must NOT expose %q — it should be hidden behind fetch_logs", tc.provider, forbidden)
			}
		})
	}
}

// TestFetchLogsAgent_Registered ensures fetch_logs is in the system registry
// so LogAgent.GetSupportedTools can resolve it by name.
func TestFetchLogsAgent_Registered(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent, ok := core.GetNBAgent(ctx, FetchLogsAgentName, "test-account", "")
	assert.True(t, ok, "fetch_logs must be registered as a system agent")
	assert.NotNil(t, agent)
	assert.Equal(t, FetchLogsAgentName, agent.GetName())
}

func toolNamesForTest(ts []toolcore.NBTool) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, strings.TrimSpace(t.Name()))
	}
	return out
}

// TestClassifyLogMode pins the deterministic intent classifier the LogAgent
// uses to decide whether to follow the routine, investigation, or enumeration
// branch of its prompt. The classifier prefers OriginalQuery over Query so
// that a parent planner's paraphrase ("Get logs for X") doesn't downgrade an
// investigation ("Were there issues with X?") to routine.
func TestClassifyLogMode(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		original string
		want     logMode
	}{
		// Routine
		{"tail logs", "show me last logs for api-server", "", logModeRoutine},
		{"recent logs", "get recent logs of llm-server in nudgebee namespace", "", logModeRoutine},
		{"time range only", "logs between 10:00 and 11:00", "", logModeRoutine},

		// Investigation
		{"why is X broken", "why is api-service in app-108 failing", "", logModeInvestigation},
		{"diagnose", "diagnose checkout-api connection refused errors", "", logModeInvestigation},
		{"what caused", "what caused the outage in payment-api", "", logModeInvestigation},
		{"were there issues", "were there issues with cron-scheduler in ns-73b", "", logModeInvestigation},
		{"root cause", "find the root cause of the crash in worker-7", "", logModeInvestigation},

		// Enumeration
		{"list errors", "list all errors in payment-api last 1h", "", logModeEnumeration},
		{"show errors", "show me the errors in my-app-51", "", logModeEnumeration},
		{"distinct errors", "what distinct errors appear in checkout-api", "", logModeEnumeration},
		{"summarise errors", "summarize errors in cron-scheduler", "", logModeEnumeration},

		// OriginalQuery wins over per-step paraphrase — this is the case that
		// regressed in production today: parent emitted "Get logs for pod X"
		// and the LLM treated it as routine, skipping the investigation flow.
		{
			name:     "parent paraphrase loses intent without classifier",
			query:    "Get logs for pod cron-scheduler-xtcwd in namespace-73b",
			original: "Were there issues with the cron-scheduler pod in namespace-73b?",
			want:     logModeInvestigation,
		},
		{
			name:     "empty original falls back to query",
			query:    "why is api-server failing",
			original: "",
			want:     logModeInvestigation,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyLogMode(tc.query, tc.original)
			assert.Equalf(t, tc.want, got,
				"classifyLogMode(%q, %q) = %s; want %s",
				tc.query, tc.original, logModeName(got), logModeName(tc.want))
		})
	}
}

// TestSystemPrompt_NarrowsToOneMode verifies the prompt assembled by
// GetSystemPrompt contains ONLY the matching mode's workflow. With the
// previous "all three modes inline + MODE flag" approach the LLM
// occasionally followed the wrong block; with mode-specific assembly the
// wrong block isn't even visible.
func TestSystemPrompt_NarrowsToOneMode(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	cases := []struct {
		name     string
		query    string
		original string
		wantMode string
		mustHave []string // substrings that must appear
		mustMiss []string // substrings that must NOT appear (other modes' content)
	}{
		{
			name:     "investigation prompt has investigation block, no routine/enumeration",
			query:    "Get logs for pod cron-scheduler-xtcwd in namespace-73b",
			original: "Were there issues with the cron-scheduler pod in namespace-73b?",
			wantMode: "INVESTIGATION",
			mustHave: []string{
				"MODE = INVESTIGATION",
				"last 24h, limit 5000",
				"Mandatory shell_execute pass",
				"Time-window framing",
				"Label anchor",
				"all logs for app <name>",
				"all logs for pod <name>",
			},
			mustMiss: []string{
				"Workflow (Routine):",
				"Workflow (Enumeration):",
				"Per-signature aggregation pipeline",
			},
		},
		{
			name:     "routine prompt has routine block, no investigation/enumeration",
			query:    "show me last 30m logs for api-server",
			original: "",
			wantMode: "ROUTINE",
			mustHave: []string{
				"MODE = ROUTINE",
				"Workflow (Routine):",
				"Single fetch is enough",
				"Label anchor",
			},
			mustMiss: []string{
				"Mandatory shell_execute pass",
				"last 24h, limit 5000",
				"Per-signature aggregation pipeline",
				"Time-window framing",
			},
		},
		{
			name:     "enumeration prompt has enumeration block, no routine/investigation",
			query:    "list all errors in service-y",
			original: "",
			wantMode: "ENUMERATION",
			mustHave: []string{
				"MODE = ENUMERATION",
				"Workflow (Enumeration):",
				// New grep-on-text aggregation pipeline (replaces the jq one).
				"grep -iE 'error|fail|warn|exception|fatal|timeout|refused|denied'",
				"sort | uniq -c",
				"Label anchor",
			},
			mustMiss: []string{
				"Workflow (Routine):",
				"Mandatory shell_execute pass",
				"last 24h, limit 5000",
				"Time-window framing",
				// jq pipeline was removed (broke after JSONL flatten).
				"jq -r '.logs[].message'",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent := newLogAgent("test-account", services_server.ObservabilityProvider{Provider: "loki"})
			prompt := agent.GetSystemPrompt(ctx, core.NBAgentRequest{
				Query:         tc.query,
				OriginalQuery: tc.original,
			})
			body := strings.Join(prompt.Instructions, "\n")
			for _, sub := range tc.mustHave {
				assert.Contains(t, body, sub,
					"%s prompt should contain %q", tc.wantMode, sub)
			}
			for _, sub := range tc.mustMiss {
				assert.NotContains(t, body, sub,
					"%s prompt should NOT contain %q (belongs to a different mode)", tc.wantMode, sub)
			}
		})
	}
}

// TestSystemPrompt_NoFixtureLeaks fails if specific benchmark fixture names,
// fixture-answer time windows, or literal benchmark prompt phrasings appear
// in the LLM-visible system prompt or the agent description. The prompt
// drives behaviour on real production workloads; if it embeds fixture
// names, the LLM may treat those as canonical and behave differently for
// non-fixture queries.
func TestSystemPrompt_NoFixtureLeaks(t *testing.T) {
	// Names and phrases that exist as benchmark fixtures in
	// llm/benchmark/llm/agents/nubi/fixtures/.
	leaks := []string{
		// Pod / deployment names from the time-window-anomaly fixtures
		"cron-scheduler",
		"task-scheduler",
		// Fixture 51's deployment
		"my-app-51",
		"my-app",
		// Fixture 100a / 101 / 108
		"payment-api",
		"api-service",
		"app-108",
		// Fixture answer windows (would teach the LLM the answer)
		"03:00–03:05",
		"03:00-03:05",
		// Specific pod hashes from prior fixture runs
		"7d8b598678",
		"6fb6dd6b8c",
		// Literal benchmark prompts (verbatim user questions in test_case.yaml)
		"Were there issues with",
	}

	ctx := security.NewRequestContextForSuperAdmin()
	agent := newLogAgent("test-account", services_server.ObservabilityProvider{Provider: "loki"})
	tool := LogAgentTool{}

	cases := []struct {
		name    string
		surface string
	}{
		{
			"investigation prompt body",
			strings.Join(agent.GetSystemPrompt(ctx, core.NBAgentRequest{
				Query:         "Get logs for service-x",
				OriginalQuery: "why is service-x failing",
			}).Instructions, "\n"),
		},
		{
			"routine prompt body",
			strings.Join(agent.GetSystemPrompt(ctx, core.NBAgentRequest{
				Query: "show me last 30m logs for service-x",
			}).Instructions, "\n"),
		},
		{
			"enumeration prompt body",
			strings.Join(agent.GetSystemPrompt(ctx, core.NBAgentRequest{
				Query: "list errors in service-x",
			}).Instructions, "\n"),
		},
		{"agent description (registry)", agent.GetDescription()},
		{"agent description (tool wrapper)", tool.Description()},
		// fetch_logs's tool description and few-shot examples are also
		// LLM-visible surfaces — round-3 review caught a regression where
		// fixture names re-appeared here after they were scrubbed from
		// agent_log.go. Walk the entire agent_log_fetch.go source file to
		// catch leaks in tool descriptions, prompt strings, and production
		// code comments.
		{"agent_log_fetch.go source (covers tool desc, prompts, comments)", readAgentLogFetchSource(t)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, leak := range leaks {
				assert.NotContains(t, tc.surface, leak,
					"%s leaks fixture identifier %q — replace with an abstract pattern (e.g. `<workload>-<6-10 hex>-<5 alnum>`)",
					tc.name, leak)
			}
		})
	}
}

// readAgentLogFetchSource returns the contents of agent_log_fetch.go so
// TestSystemPrompt_NoFixtureLeaks can scan tool descriptions, few-shot
// examples, and production code comments for fixture-name leakage. Walking
// the source file is a more reliable guard than only sampling
// LLM-visible strings via accessor methods — comments are not LLM-visible
// but they document the LLM-visible code, and comment leaks rot when
// fixtures move.
func readAgentLogFetchSource(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("agent_log_fetch.go")
	if err != nil {
		t.Fatalf("readAgentLogFetchSource: %v", err)
	}
	return string(body)
}

// TODO mock DBs
// TODO mock Tool Execution

func TestGetLogAgent(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	agent, err := getLogAgent(sc, os.Getenv("TEST_ACCOUNT"))
	assert.Nil(t, err)
	assert.NotNil(t, agent)
	assert.Equal(t, LogsAgentName, agent.GetName())
}

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
