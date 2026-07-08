//go:build e2e

package agents

import (
	"encoding/json"
	"fmt"
	"nudgebee/llm/agents/asserts"
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

// ============================================================
// Reproducibility tiers for the k8s_debug integration tests
// ============================================================
//
// NOTE — read `TESTING.md` in this directory before adding or modifying tests.
// Most tests here are smoke tests against a populated test cluster, not
// self-contained regressions. The fixture-backed pattern (benchmark_fixture_test.go
// + flagd_controller_test.go) is the migration target for tests that need to
// reproduce failure modes deterministically. The taxonomy below classifies
// where each test in this file currently sits.
//
// Each test in this file falls into one of four reproducibility tiers.
// The tier determines what "validation" can mean and where the test should
// ultimately live. This taxonomy guides ongoing migration — not every test
// is in its final home yet.
//
//   Tier A — Self-verifying (ground truth computable).
//     A direct API call produces the correct answer; the test asserts the
//     agent's answer matches. These stay in-process Go tests and run on
//     every PR. Examples below: TestK8sAgent_Caching (pod list), the
//     version-query cases in TestK8sAgent_ClusterOperations, the Postgres
//     count cases in TestK8sAgent_SkillExecution.
//     [TODO] Wire real live-query validators. Tracked as Phase 1 follow-up.
//
//   Tier B — Deterministic fixture (input embedded in prompt).
//     No cluster state required; assertions target structure / keywords.
//     Examples: TestK8sAgent_DirectAnswer, TestK8sAgent_LargeData,
//     TestK8sAgent_CrossAccount (GKE alert JSON in prompt),
//     TestK8sAgent_ResponseFormatJSON.
//     These are already strong and stay here.
//
//   Tier C — Requires a reproducible failure scenario.
//     Default home: benchmark fixtures whose `before_test` shell hook
//     deploys a fault-injecting manifest into an isolated namespace
//     (llm/benchmark/llm/agents/nubi/fixtures/). See
//     agent_k8s_debug_fixtures_e2e_test.go for working examples.
//     Tests in this file below tagged [Tier C → migrate] should be ported
//     onto benchmark fixtures and deleted from here.
//     Affected groups: TestK8sAgent_PodDebugging, TestK8sAgent_MemoryUsage,
//     TestK8sAgent_Observability, and the OOM / pod-restart cases inside
//     TestK8sAgent_ClusterOperations.
//     Until migrated, these run against whatever happens to be in dev —
//     they smoke-test the path but can't fail meaningfully on content.
//
//   Tier D — Mock-based (only when reality cannot be reproduced).
//     Reserved for LLM provider 5xx, budget exhaustion, and critiquer
//     rejection loops. Not yet implemented; strictly small in scope.
//
// ============================================================
// Test data
// ============================================================

var (
	javaHeapDump           string
	largeHealthCheckPrompt string
)

func init() {
	if b, err := os.ReadFile("../testdata/java_heap_dump.txt"); err == nil {
		javaHeapDump = string(b)
	}
	if b, err := os.ReadFile("../testdata/large_healthcheck_prompt.txt"); err == nil {
		largeHealthCheckPrompt = string(b)
	}
}

// ============================================================
// Shared helpers
// ============================================================

// envOr returns the value of the named env var, or fallback if unset/empty.
// Used to parametrize account-specific workspace bindings (postgres, rabbit,
// dev env names) without forcing every reader to set them — defaults match
// the original hardcoded values.
func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// k8sTestCase holds all parameters for a single integration scenario.
type k8sTestCase struct {
	Name              string
	SessionId         string
	Query             string
	AccountId         string
	UserId            string
	ToolConfigs       map[string]string // optional per-tool workspace config
	ApprovalResponses []string          // optional: text answers for successive Waiting rounds

	// --- Expectation knobs (all optional) ---

	// WantStatus is the expected final ConversationStatus. Defaults to Completed
	// when zero-valued. Set explicitly when a test legitimately ends in Waiting.
	WantStatus core.ConversationStatus

	// WantNoTools asserts that AgentStepResponse is empty (planner answered direct).
	WantNoTools bool

	// WantMinToolCalls asserts at least N tool invocations occurred.
	WantMinToolCalls int

	// WantMaxToolCalls asserts at most N tool invocations occurred.
	// Use for efficiency-sensitive fixtures (e.g. fast-path queries).
	// Zero means unbounded.
	WantMaxToolCalls int

	// WantToolInvoked asserts a specific tool name appears in the invocation log.
	WantToolInvoked string

	// WantAnyToolMatching asserts at least one tool whose name contains any of
	// these substrings was invoked. Use for tool-family checks ("any
	// prometheus tool").
	WantAnyToolMatching []string

	// WantForbiddenTools asserts none of these tools were invoked. Use to
	// catch fallback paths the fixture explicitly disallows
	// (e.g. metrics-tier fixtures forbid shell_execute).
	WantForbiddenTools []string

	// WantContainsAny asserts at least one of these substrings appears in the
	// concatenated response body (case-insensitive). Use for content presence
	// checks like "diagram" / "mermaid" / "```".
	WantContainsAny []string

	// WantLLMClaims is a list of expected claims an LLM judge will evaluate
	// against the response. Each claim should be a single sentence describing
	// what a satisfying answer must support. Costs ~$0.001 per test when
	// non-empty; leave empty to skip the semantic check.
	WantLLMClaims []string
}

// defaultCase returns a k8sTestCase pre-filled with env-var credentials.
func defaultCase(name, sessionId, query string) k8sTestCase {
	return k8sTestCase{
		Name:      name,
		SessionId: sessionId,
		Query:     query,
		AccountId: os.Getenv("TEST_ACCOUNT"),
		UserId:    os.Getenv("TEST_USER"),
	}
}

// newSC creates a RequestContext for the test case.
func newSC(tc k8sTestCase) *security.RequestContext {
	return security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
}

// buildRequestOpts converts a k8sTestCase into optional request configs.
func buildRequestOpts(tc k8sTestCase) []core.ConversationSessionRequestConfig {
	if len(tc.ToolConfigs) == 0 {
		return nil
	}
	return []core.ConversationSessionRequestConfig{
		core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{ToolConfigs: tc.ToolConfigs}),
	}
}

// sendApproval submits an approval/follow-up message for a waiting conversation.
func sendApproval(
	t *testing.T,
	sc *security.RequestContext,
	agent core.NBAgent,
	resp core.NBAgentResponse,
	tc k8sTestCase,
	approval string,
) (core.NBAgentResponse, error) {
	t.Helper()
	messageId, err := uuid.Parse(resp.MessageId)
	if !assert.Nil(t, err, "parse message id") {
		return resp, err
	}
	agentId, err := uuid.Parse(resp.AgentId)
	if !assert.Nil(t, err, "parse agent id") {
		return resp, err
	}
	return core.HandleConversationSessionRequest(
		sc, agent, tc.UserId, tc.AccountId, tc.SessionId, approval,
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
		core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}),
	)
}

// assertExpectations checks all opt-in WantXxx fields on tc against resp.
// Always-on baseline assertions (agent name, non-empty query, response length)
// belong in runTest, not here.
//
// Implementation note: this function is a thin orchestrator over the
// agents/asserts package. Each WantXxx field maps to one asserts.* call.
// To add a new expectation knob, prefer adding a function to agents/asserts
// and a delegation here, rather than inlining assertion logic.
func assertExpectations(t *testing.T, tc k8sTestCase, resp core.NBAgentResponse) {
	t.Helper()

	// Final status check (defaults to Completed)
	wantStatus := tc.WantStatus
	if wantStatus == "" {
		wantStatus = core.ConversationStatusCompleted
	}
	asserts.ResponseStatus(t, resp, wantStatus)

	// Tool count expectations
	if tc.WantNoTools {
		asserts.NoTools(t, resp)
	}
	if tc.WantMinToolCalls > 0 {
		asserts.MinToolCalls(t, resp, tc.WantMinToolCalls)
	}
	if tc.WantMaxToolCalls > 0 {
		asserts.MaxToolCalls(t, resp, tc.WantMaxToolCalls)
	}

	// Specific tool invocation
	if tc.WantToolInvoked != "" {
		asserts.ToolUsed(t, resp, tc.WantToolInvoked)
	}

	// Tool-family OR check
	if len(tc.WantAnyToolMatching) > 0 {
		asserts.AnyToolMatching(t, resp, tc.WantAnyToolMatching)
	}

	// Forbidden-tools check
	if len(tc.WantForbiddenTools) > 0 {
		asserts.ForbiddenTools(t, resp, tc.WantForbiddenTools)
	}

	// Response content keyword check
	if len(tc.WantContainsAny) > 0 {
		asserts.ResponseContainsAny(t, resp, tc.WantContainsAny)
	}

	// LLM-as-judge semantic check (opt-in; costs an LLM call when non-empty)
	if len(tc.WantLLMClaims) > 0 {
		body := strings.Join(resp.Response, "\n")
		asserts.LLMClaims(t, newSC(tc), tc.WantLLMClaims, body)
	}
}

// runTest executes a k8sTestCase and applies full standard assertions plus any
// opt-in expectations declared on the test case.
func runTest(t *testing.T, agent core.NBAgent, tc k8sTestCase) core.NBAgentResponse {
	t.Helper()
	sc := newSC(tc)

	err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
	assert.Nil(t, err)

	opts := buildRequestOpts(tc)
	resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query, opts...)

	// Drive through any approval rounds
	for _, approval := range tc.ApprovalResponses {
		if resp.Status == core.ConversationStatusWaiting {
			resp, err = sendApproval(t, sc, agent, resp, tc, approval)
		}
	}

	assert.Nil(t, err)
	assert.Equal(t, agent.GetName(), resp.AgentName)
	assert.NotEmpty(t, resp.Query)
	assert.Greater(t, len(resp.Response), 0)

	fmt.Printf("[%s] response: %s\n", tc.Name, resp.Response)
	fmt.Printf("[%s] tools: %d invocations\n", tc.Name, len(resp.AgentStepResponse))

	assertExpectations(t, tc, resp)
	return resp
}

// waitForMemory polls the long-term memory store for entries matching every keyword
// in `keywords`. Returns the matched memory and true on the first hit, or zero-value
// and false after `attempts` rounds (`interval` between rounds).
func waitForMemory(t *testing.T, accountId string, keywords []string, attempts int, interval time.Duration) (core.LongTermMemory, bool) {
	t.Helper()
	for i := 0; i < attempts; i++ {
		memories, err := core.GetConversationDao().ListLongTermMemories(accountId, "", "", "", "", 50, 0)
		if err == nil {
			for _, m := range memories {
				if containsAll(m.Content, keywords) {
					fmt.Printf("memory found on attempt %d: %s\n", i+1, m.Content)
					return m, true
				}
			}
		}
		fmt.Printf("attempt %d: memory not yet present, retrying in %s…\n", i+1, interval)
		time.Sleep(interval)
	}
	return core.LongTermMemory{}, false
}

// countMemoriesMatching returns the number of long-term memories whose content
// contains every keyword in `keywords`.
func countMemoriesMatching(t *testing.T, accountId string, keywords []string) int {
	t.Helper()
	memories, err := core.GetConversationDao().ListLongTermMemories(accountId, "", "", "", "", 50, 0)
	assert.Nil(t, err)
	count := 0
	for _, m := range memories {
		if containsAll(m.Content, keywords) {
			count++
			fmt.Printf("dedup check — memory [%s] type=%s content=%s\n", m.ID, m.MemoryType, m.Content)
		}
	}
	return count
}

// containsAll returns true if `s` contains every substring in `subs`.
func containsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// stripCodeFence removes a leading ```lang and trailing ``` from a markdown code block.
// If `s` is not a fenced block, the original string is returned unchanged.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if idx := strings.Index(s, "\n"); idx != -1 {
		s = s[idx+1:]
	}
	if i := strings.LastIndex(s, "```"); i != -1 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// runTestMinimal executes a k8sTestCase with the minimal baseline assertions
// (err==nil) plus any opt-in expectations declared on the test case. Prefer
// runTest when full standard assertions are appropriate.
func runTestMinimal(t *testing.T, agent core.NBAgent, tc k8sTestCase) core.NBAgentResponse {
	t.Helper()
	sc := newSC(tc)

	err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
	assert.Nil(t, err)

	opts := buildRequestOpts(tc)
	resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query, opts...)

	for _, approval := range tc.ApprovalResponses {
		if resp.Status == core.ConversationStatusWaiting {
			resp, err = sendApproval(t, sc, agent, resp, tc, approval)
		}
	}

	assert.Nil(t, err)
	fmt.Printf("[%s] response: %s\n", tc.Name, resp.Response)

	assertExpectations(t, tc, resp)
	return resp
}

// ============================================================
// 1. Direct Answer — no tool invocation expected
// ============================================================

// TestK8sAgent_DirectAnswer covers queries the planner answers without calling tools.
func TestK8sAgent_DirectAnswer(t *testing.T) {
	skipIfNoFixtureEnv(t)
	agent := newK8sDebugAgent(os.Getenv("TEST_ACCOUNT"))

	mk := func(name, sessionId, query string) k8sTestCase {
		c := defaultCase(name, sessionId, query)
		c.WantNoTools = true // direct-answer queries must not invoke tools
		return c
	}

	cases := []k8sTestCase{
		mk("hello", "ut-da-hello-0", `hello`),
		mk("what_is_pdb", "ut-da-pdb-0", `What is PDB`),
		mk("rabbitmq_secret_command", "ut-da-mq-secret-0",
			`command to retrieve rabbitmq password and username from secret`),
		// helm_install_lost may genuinely require investigation; leave WantNoTools off
		defaultCase("helm_install_lost", "ut-da-helm-0",
			`I have done helm installation sometime back on one of my namespace.. recently observed that helm is complaining that no installation found.. even though i can see workloads running any idea ?`),
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			runTest(t, agent, tc)
		})
	}
}

// ============================================================
// 2. Memory Creation — new memories are extracted and deduplicated
// ============================================================

// TestK8sAgent_MemoryCreation verifies that user preferences are stored in long-term
// memory exactly once (deduplication guard) across two identical sessions.
func TestK8sAgent_MemoryCreation(t *testing.T) {
	skipIfNoFixtureEnv(t)

	query := `can you remember that if i ask you to fetch logs of llm server you should always look for llm-server deployment in nudgebee namespace`
	accountId := os.Getenv("TEST_ACCOUNT")
	keywords := []string{"llm-server", "nudgebee"}
	agent := newK8sDebugAgent(accountId)

	for _, sessionId := range []string{"ut-mem-create-0", "ut-mem-create-1"} {
		tc := defaultCase("memory_create_"+sessionId, sessionId, query)
		runTest(t, agent, tc)

		mem, found := waitForMemory(t, accountId, keywords, 12, 5*time.Second)
		assert.True(t, found, "memory not found in DB within 60 s")
		if found {
			assert.Equal(t, core.MemoryTypeUserPreference, mem.MemoryType)
		}
	}

	// Allow in-flight async work to settle, then assert deduplication
	fmt.Println("waiting 15 s for any trailing async extraction…")
	time.Sleep(15 * time.Second)

	matchCount := countMemoriesMatching(t, accountId, keywords)
	assert.Equal(t, 1, matchCount,
		"expected exactly 1 matching memory after 2 identical runs (dedup failure), got %d", matchCount)
}

// ============================================================
// 3. Memory Usage — query Prometheus / cluster memory metrics
// ============================================================

// TestK8sAgent_MemoryUsage covers queries that read or report on memory consumption.
func TestK8sAgent_MemoryUsage(t *testing.T) {
	skipIfNoFixtureEnv(t)
	agent := newK8sDebugAgent(os.Getenv("TEST_ACCOUNT"))

	// Any memory-related investigation should reach for at least one of these
	// tool families. Substring match keeps the assertion robust against the
	// model picking different but equivalent tools.
	memoryTools := []string{"prometheus", "metric", "memory", "kubectl_top", "kubectl_describe"}

	mk := func(name, sessionId, query string) k8sTestCase {
		c := defaultCase(name, sessionId, query)
		c.WantAnyToolMatching = memoryTools
		c.WantMinToolCalls = 1
		return c
	}

	cases := []k8sTestCase{
		mk("prometheus_max_memory", "ut-mem-usage-prom-0",
			`Can you tell me max memory used by llm-server in nudgebee namespace`),
		mk("oom_redis_namespace", "ut-mem-usage-oom-redis-0",
			`Can you investigate recent oom issue in redis namespace`),
		mk("oom_cluster_wide", "ut-mem-usage-oom-cluster-0",
			`Can you check for recent out of memory issues and investigate them`),
		mk("oom_rabbit_test", "ut-mem-usage-oom-rabbit-0",
			`Can you investigate recent oom issue in rabbit-test namespace`),
		mk("oom_cloud_collector_rca", "ut-mem-usage-oom-cc-0",
			`Can you analyze recent OOM errors of the cloud-collector-server deployment in nudgebee namespace`),
		mk("relay_oom_code_change", "ut-mem-usage-relay-oom-0",
			`recently relay server has started going OOM, can you check any recent change that might have caused the issue ?`),
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			runTestMinimal(t, agent, tc)
		})
	}
}

// ============================================================
// 4. Pod Debugging — restarts, failures, log analysis
// ============================================================

// TestK8sAgent_PodDebugging covers pod-level failure scenarios.
func TestK8sAgent_PodDebugging(t *testing.T) {
	skipIfNoFixtureEnv(t)
	agent := newK8sDebugAgent(os.Getenv("TEST_ACCOUNT"))

	// Pod debugging should reach for kubectl describe / events / logs.
	podTools := []string{"kubectl", "describe", "logs", "event", "k8s_"}

	mk := func(name, sessionId, query string) k8sTestCase {
		c := defaultCase(name, sessionId, query)
		c.WantAnyToolMatching = podTools
		c.WantMinToolCalls = 1
		return c
	}

	cases := []k8sTestCase{
		mk("pod_restart_nudgebee_agent", "ut-pod-restart-agent-0",
			`troubleshoot why my-agent-node-runner restarted in my-agent namespace?`),
		mk("pod_restart_test_pod", "ut-pod-restart-test-0",
			`troubleshoot why test-pod-delete-force-deploy pods restarted in default namespace.`),
		mk("pod_restart_ml_server", "ut-pod-restart-ml-0",
			`Can you investigate restarts of ml-k8s-server in nudgebee namespace.`),
		mk("pod_restart_reddit", "ut-pod-restart-reddit-0",
			`Can you investigate recent pod restart of nudgebee-reddit in nudgebee namespace`),
		mk("pod_restart_cloud_collector", "ut-pod-restart-cc-0",
			`my cloud-collector-server app in nudgebee namespace recently restarted, can you investigate why and suggest resolution`),
		mk("pod_restart_services_server_named", "ut-pod-restart-svc-named-0",
			`Can you investigate recent restarts of the services-server deployment in nudgebee namespace ?`),
		mk("pod_restart_recent_generic", "ut-pod-restart-recent-0",
			`Investigate Recent Restarts of the application`),
		mk("pod_failure_llm_server", "ut-pod-failure-llm-0",
			`My llm-server deployment in nudgebee namespace restarted recently, can you investigate why?`),
		mk("pod_failure_nb_sample", "ut-pod-failure-nb-sample-0",
			`Can you investigate issues with the nb-node-sample-app-deployment in staging namespace`),
		mk("pod_failure_ad_otel", "ut-pod-failure-ad-otel-0",
			`Can you investigate recent failures with 'ad' deployment in otel-demo namespace`),
		mk("pod_logs_services_server", "ut-pod-logs-svc-0",
			`Can you review services-server logs in nudgebee namespace, identify issues & recommend solution`),
		mk("pod_logs_llm_server", "ut-pod-logs-llm-0",
			`Get logs for llm-server in nudgebee namespace and summarize any errors found`),
		mk("errors_frontend_od", "ut-pod-errors-fe-0",
			`I am observing errors in frontend in od namespace, can you investigate`),
		mk("traversal_od_flagd", "ut-pod-traversal-od-0",
			`investigate flagd in od namespace`),
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			runTestMinimal(t, agent, tc)
		})
	}
}

// ============================================================
// 5. Skill Execution — tool-driven actions
// ============================================================

// TestK8sAgent_SkillExecution covers cases that rely on specific tool invocations.
func TestK8sAgent_SkillExecution(t *testing.T) {
	skipIfNoFixtureEnv(t)
	agent := newK8sDebugAgent(os.Getenv("TEST_ACCOUNT"))

	cases := []k8sTestCase{
		{
			Name:                "fetch_logs_llm_server",
			SessionId:           "ut-skill-logs-llm-0",
			AccountId:           os.Getenv("TEST_ACCOUNT"),
			UserId:              os.Getenv("TEST_USER"),
			Query:               `fetch logs llm-server in nudgebee namespace..`,
			WantAnyToolMatching: []string{"logs", "loki"},
			WantMinToolCalls:    1,
		},
		{
			Name:            "generate_nginx_yaml",
			SessionId:       "ut-skill-yaml-nginx-0",
			AccountId:       os.Getenv("TEST_ACCOUNT"),
			UserId:          os.Getenv("TEST_USER"),
			Query:           `Can you generate sample yaml file for running nginx container`,
			WantContainsAny: []string{"apiVersion", "kind:", "nginx"},
		},
		{
			Name:            "postgres_connections",
			SessionId:       "ut-skill-pg-conn-0",
			AccountId:       os.Getenv("TEST_ACCOUNT"),
			UserId:          os.Getenv("TEST_USER"),
			Query:           `can you tell me number of postgres connections for nudgebee database`,
			ToolConfigs:     map[string]string{"postgres_query_execute": envOr("TEST_PG_CONFIG_NAME", "dev-pg")},
			WantToolInvoked: "postgres_query_execute",
		},
		{
			Name:            "postgres_events_table_index",
			SessionId:       "ut-skill-pg-index-0",
			AccountId:       os.Getenv("TEST_ACCOUNT"),
			UserId:          os.Getenv("TEST_USER"),
			Query:           `can you check postgres events table does it require any additional indexes.`,
			ToolConfigs:     map[string]string{"postgres_query_execute": envOr("TEST_PG_CONFIG_NAME", "dev-pg")},
			WantToolInvoked: "postgres_query_execute",
		},
		defaultCase("trigger_pluto_scan", "ut-skill-pluto-0",
			`Can you trigger trigger_pluto_scan`),
		{
			Name:                "cluster_summary_deployments",
			SessionId:           "ut-skill-cluster-summary-0",
			AccountId:           os.Getenv("TEST_ACCOUNT"),
			UserId:              os.Getenv("TEST_USER"),
			Query:               `Get a summary of all Deployments, StatefulSets, DaemonSets, and Jobs running in the cluster. Include their names, namespaces, replica counts, and status.`,
			WantAnyToolMatching: []string{"kubectl", "k8s_"},
			// Asks for 4 resource types — should drive multiple tool calls
			WantMinToolCalls: 2,
		},
		defaultCase("performance_cluster", "ut-skill-perf-cluster-0",
			`Can you investigate the performance issues in the cluster?`),
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			runTestMinimal(t, agent, tc)
		})
	}

	// check_ci_failure references a specific repo + named action that won't
	// exist on a fresh setup. Gate behind env vars so OSS readers don't see
	// the internal `nudgebee/nudgebee` repo hardcoded.
	t.Run("function_call_ci_failure", func(t *testing.T) {
		repo, action := os.Getenv("TEST_CI_REPO"), os.Getenv("TEST_CI_ACTION")
		if repo == "" || action == "" {
			t.Skip("skipping: TEST_CI_REPO / TEST_CI_ACTION not set")
		}
		tc := defaultCase("function_call_ci_failure", "ut-skill-fn-ci-0",
			fmt.Sprintf(`/call check_ci_failure investigate failure of action %q in %s repo`, action, repo))
		runTestMinimal(t, agent, tc)
	})

	// Event-RCA case: fetch a recent event from the test DB and verify the
	// agent can investigate it. Replaces the prior TEST_EVENT_ID_1/2 hardcoded
	// UUIDs — the assertion (a tool from the event/rca family was invoked) is
	// event-agnostic, so a fresh DB-resident event is sufficient.
	t.Run("rca_event", func(t *testing.T) {
		id := FetchRecentEventID(t, os.Getenv("TEST_ACCOUNT"))
		tc := defaultCase("rca_event", "ut-skill-rca-event",
			fmt.Sprintf(`Investigate event with Id %s`, id))
		tc.WantAnyToolMatching = []string{"event", "rca"}
		tc.WantMinToolCalls = 1
		runTestMinimal(t, agent, tc)
	})
}

// TestK8sAgent_RCAWithRabbitMQ covers event-driven RCA that needs RabbitMQ workspace.
// Fetches a recent event from the test DB and uses the configured rabbit
// workspace; skips when DB has no events.
func TestK8sAgent_RCAWithRabbitMQ(t *testing.T) {
	skipIfNoFixtureEnv(t)
	eventID := FetchRecentEventID(t, os.Getenv("TEST_ACCOUNT"))
	agent := newK8sDebugAgent(os.Getenv("TEST_ACCOUNT"))

	tc := k8sTestCase{
		Name:        "rca_event_rabbit_workspace",
		SessionId:   "ut-rca-rabbit-0",
		AccountId:   os.Getenv("TEST_ACCOUNT"),
		UserId:      os.Getenv("TEST_USER"),
		Query:       fmt.Sprintf(`Investigate event with Id %s`, eventID),
		ToolConfigs: map[string]string{"rabbit_execute": envOr("TEST_RABBIT_CONFIG_NAME", "dev")},
		// The whole point of this test is the RabbitMQ workspace plumbing —
		// without this assertion, the test would pass even if rabbit_execute
		// was never invoked.
		WantAnyToolMatching: []string{"rabbit", "event"},
		WantMinToolCalls:    1,
	}
	t.Run(tc.Name, func(t *testing.T) {
		runTest(t, agent, tc)
	})
}

// ============================================================
// 6. Approval Flows — responses that require user confirmation
// ============================================================

// TestK8sAgent_ApprovalFlows covers scenarios where the agent pauses for user
// input before executing write/mutating operations.
//
// These tests intentionally mutate cluster state (scaling deployments, creating
// pods) — that's what they're validating. Each subtest is responsible for its
// own cleanup via t.Cleanup so the dev cluster doesn't accumulate side-effects
// across runs.
func TestK8sAgent_ApprovalFlows(t *testing.T) {
	skipIfNoFixtureEnv(t)
	agent := newK8sDebugAgent(os.Getenv("TEST_ACCOUNT"))

	t.Run("scale_rag_server", func(t *testing.T) {
		const ns, deploy = "nudgebee", "rag-server"
		orig, err := getDeploymentReplicas(t, ns, deploy)
		if err != nil {
			t.Skipf("cannot snapshot replicas of %s/%s (%v); skipping to avoid leaking scale", ns, deploy, err)
		}
		t.Cleanup(func() {
			if _, err := kubectl(t, "scale", "deployment", deploy,
				"-n", ns, fmt.Sprintf("--replicas=%d", orig)); err != nil {
				t.Logf("warning: failed to restore %s/%s to %d replicas: %v", ns, deploy, orig, err)
			}
		})
		tc := k8sTestCase{
			Name:              "scale_rag_server",
			SessionId:         "ut-approval-scale-rag-0",
			AccountId:         os.Getenv("TEST_ACCOUNT"),
			UserId:            os.Getenv("TEST_USER"),
			Query:             `Update rag-server in nudgebee namespace to 2 replicas`,
			ApprovalResponses: []string{"yes", "yes"},
		}
		runTestMinimal(t, agent, tc)
	})

	t.Run("network_pod_launch", func(t *testing.T) {
		const ns = "my-agent"
		before, err := listPodNames(t, ns)
		if err != nil {
			t.Skipf("cannot snapshot pods in %s (%v); skipping to avoid leaking a pod", ns, err)
		}
		seen := make(map[string]bool, len(before))
		for _, p := range before {
			seen[p] = true
		}
		t.Cleanup(func() {
			after, err := listPodNames(t, ns)
			if err != nil {
				t.Logf("warning: cannot list pods for cleanup: %v", err)
				return
			}
			for _, p := range after {
				if seen[p] {
					continue
				}
				if _, err := kubectl(t, "delete", "pod", p,
					"-n", ns, "--grace-period=0", "--force"); err != nil {
					t.Logf("warning: failed to delete leaked pod %s/%s: %v", ns, p, err)
				}
			}
		})
		tc := k8sTestCase{
			Name:              "network_pod_launch",
			SessionId:         "ut-approval-netpod-0",
			AccountId:         os.Getenv("TEST_ACCOUNT"),
			UserId:            os.Getenv("TEST_USER"),
			Query:             `Can you check network connectivity to google.com by launching new pod using 'weibeld/ubuntu-networking' image in my-agent namespace ?`,
			ApprovalResponses: []string{"yes"},
		}
		runTestMinimal(t, agent, tc)
	})

	t.Run("github_issue_comment", func(t *testing.T) {
		// Posts a real comment to a real GitHub issue — must point at a
		// sandbox repo/issue, not hardcode an internal one.
		repo := os.Getenv("TEST_GITHUB_REPO")
		issue := os.Getenv("TEST_GITHUB_ISSUE")
		if repo == "" || issue == "" {
			t.Skip("skipping: TEST_GITHUB_REPO / TEST_GITHUB_ISSUE not set")
		}
		tc := k8sTestCase{
			Name:      "github_issue_comment",
			SessionId: "ut-approval-github-0",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query: fmt.Sprintf(`@k8s_debug  Can you review #%s issue in %s.
- Provide relevant shell script which can be used to solve this issue.
- Add comment on github issue with proposed shell script, so that developer can use it for implementation. You need to add solution in the comments`, issue, repo),
			ApprovalResponses: []string{"yes"},
		}
		runTestMinimal(t, agent, tc)
	})
}

// TestK8sAgent_PostgresApprovalFlow tests a multi-round approval for database selection.
func TestK8sAgent_PostgresApprovalFlow(t *testing.T) {
	skipIfNoFixtureEnv(t)
	agent := newK8sDebugAgent(os.Getenv("TEST_ACCOUNT"))

	tc := k8sTestCase{
		Name:      "postgres_event_schema",
		SessionId: "ut-approval-pg-schema-0",
		AccountId: os.Getenv("TEST_ACCOUNT"),
		UserId:    os.Getenv("TEST_USER"),
		Query:     `Describe table schema for events table`,
	}

	sc := newSC(tc)
	err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
	assert.Nil(t, err)

	resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
	assert.Nil(t, err)

	// Either the agent asks which database to use (the deterministic path we
	// want to exercise) or it picks one and answers directly. Both are valid;
	// what matters is that we end up Completed with a tool invocation and a
	// non-empty response.
	if resp.Status == core.ConversationStatusWaiting {
		resp, err = sendApproval(t, sc, agent, resp, tc, envOr("TEST_PG_CONFIG_NAME", "dev-pg"))
		assert.Nil(t, err)
	}
	assert.Equal(t, core.ConversationStatusCompleted, resp.Status, "expected final status Completed")
	assert.Equal(t, agent.GetName(), resp.AgentName)
	assert.NotEmpty(t, resp.Query)
	assert.NotEmpty(t, resp.AgentStepResponse, "expected at least one tool call")
	assert.Greater(t, len(resp.Response), 0)
}

// TestK8sAgent_HealthCheckMultiApproval is a "stuck-detector" smoke test: the
// query is intentionally vague, and if the agent enters any clarifying
// approval flow we type a known-good fallback config-name so the conversation
// can make progress. The test passes as long as we reach a terminal response
// within 3 rounds. It is NOT validating an "environment" concept — NB has
// accounts, not envs — only the multi-approval plumbing itself.
func TestK8sAgent_HealthCheckMultiApproval(t *testing.T) {
	skipIfNoFixtureEnv(t)
	agent := newK8sDebugAgent(os.Getenv("TEST_ACCOUNT"))

	tc := k8sTestCase{
		Name:      "health_check_dev",
		SessionId: "ut-approval-health-0",
		AccountId: os.Getenv("TEST_ACCOUNT"),
		UserId:    os.Getenv("TEST_USER"),
		Query:     `Dev environment healthcheck?`,
	}

	sc := newSC(tc)
	err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
	assert.Nil(t, err)

	resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
	assert.Nil(t, err)

	// Up to 3 approval rounds for environment selection
	fallbackConfigName := envOr("TEST_FALLBACK_CONFIG_NAME", "dev")
	for i := 0; i < 3 && resp.Status == core.ConversationStatusWaiting; i++ {
		resp, err = sendApproval(t, sc, agent, resp, tc, fallbackConfigName)
		assert.Nil(t, err)
	}

	assert.Equal(t, agent.GetName(), resp.AgentName)
	assert.NotEmpty(t, resp.Query)
	assert.Greater(t, len(resp.Response), 0)
}

// ============================================================
// 7. Conditional Execution — plan steps with branch logic
// ============================================================

// TestK8sAgent_ConditionalExecution validates that the planner correctly evaluates
// conditions and skips or executes downstream steps accordingly.
func TestK8sAgent_ConditionalExecution(t *testing.T) {
	skipIfNoFixtureEnv(t)
	agent := newK8sDebugAgent(os.Getenv("TEST_ACCOUNT"))

	t.Run("kube_system_then_default_pods", func(t *testing.T) {
		tc := defaultCase("kube_system_then_default_pods", "ut-cond-kubesys-0",
			`"If the 'kube-system' namespace exists, then list pods in the 'default' namespace. If listing pods is successful and pods are found, then get logs for the first pod found in 'default'."`)
		runTestMinimal(t, agent, tc)
	})

	t.Run("rabbitmq_queue_gt20_scale", func(t *testing.T) {
		tc := k8sTestCase{
			Name:      "rabbitmq_queue_gt20_scale",
			SessionId: "ut-cond-mq-scale-0",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query: `check for rabbitmq messages in queue auto_playbook_task, if messages are greater than 20, 
				then check for pod status of auto-pilot-worker in nudgebee namespace if it looks ok, then scale it by 1. 
				if pod has errors then check for logs to identify issue`,
			// First approval selects the RabbitMQ workspace; second confirms scale action
			ApprovalResponses: []string{envOr("TEST_RABBIT_CONFIG_NAME", "dev"), "yes"},
		}
		// The agent may ask for workspace selection (most common path) OR
		// pick a default and proceed. Drive any approval rounds that appear;
		// final correctness is checked on the terminal response.
		sc := newSC(tc)
		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)

		for _, approval := range tc.ApprovalResponses {
			if resp.Status == core.ConversationStatusWaiting {
				resp, err = sendApproval(t, sc, agent, resp, tc, approval)
				assert.Nil(t, err)
			}
		}
		assert.Greater(t, len(resp.Response), 0, "expected non-empty terminal response")
	})

	t.Run("rabbitmq_queue_gt100_noop", func(t *testing.T) {
		tc := k8sTestCase{
			Name:      "rabbitmq_queue_gt100_noop",
			SessionId: "ut-cond-mq-noop-0",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query: `check for rabbitmq messages in queue auto_playbook_task, if messages are greater than 100,
				then check for pod status of auto-pilot-worker in nudgebee namespace if it looks ok, then scale it by 1.
				if pod has errors then check for logs to identify issue`,
			ApprovalResponses: []string{envOr("TEST_RABBIT_CONFIG_NAME", "dev")},
		}
		runTestMinimal(t, agent, tc)
	})
}

// ============================================================
// 8. Parallel Execution — multiple data sources fetched simultaneously
// ============================================================

// TestK8sAgent_ParallelExecution verifies that the planner can collect memory, CPU,
// logs, and traces for a service concurrently and synthesize the results.
func TestK8sAgent_ParallelExecution(t *testing.T) {
	skipIfNoFixtureEnv(t)
	agent := newK8sDebugAgent(os.Getenv("TEST_ACCOUNT"))

	cases := []k8sTestCase{
		{
			Name:      "parallel_mem_cpu_logs_traces",
			SessionId: "ut-parallel-all-0",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     `Get memory, cpu, logs, traces for llm-server in nudgebee namespace parallel and summarise them`,
			// Asking for 4 distinct data sources should drive at least 3 tool calls
			WantMinToolCalls:    3,
			WantAnyToolMatching: []string{"prometheus", "metric", "logs", "trace"},
		},
		{
			Name:                "internet_curl_shell_agent",
			SessionId:           "ut-parallel-curl-0",
			AccountId:           os.Getenv("TEST_ACCOUNT"),
			UserId:              os.Getenv("TEST_USER"),
			Query:               `Can you execute curl request to test internet connectivity using 'shell_execute_agent' tool`,
			WantAnyToolMatching: []string{"shell_execute"},
			WantMinToolCalls:    1,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			runTestMinimal(t, agent, tc)
		})
	}
}

// ============================================================
// 9. Large Data — agent handles large input payloads
// ============================================================

// TestK8sAgent_LargeData verifies agent stability with large or complex query payloads.
func TestK8sAgent_LargeData(t *testing.T) {
	skipIfNoFixtureEnv(t)
	agent := newK8sDebugAgent(os.Getenv("TEST_ACCOUNT"))

	t.Run("java_heap_dump", func(t *testing.T) {
		tc := defaultCase("java_heap_dump", "ut-large-heap-0",
			fmt.Sprintf(`Can you analyze below java heap dump and provide insights. Do not investigate \n\n %s`, javaHeapDump))
		runTestMinimal(t, agent, tc)
	})

	t.Run("large_health_check_prompt", func(t *testing.T) {
		if largeHealthCheckPrompt == "" {
			t.Skip("testdata/large_healthcheck_prompt.txt not present")
		}
		tc := defaultCase("large_health_check_prompt", "ut-large-healthcheck-0", largeHealthCheckPrompt)
		runTestMinimal(t, agent, tc)
	})

	t.Run("pods_with_ip_datamatch", func(t *testing.T) {
		tc := defaultCase("pods_with_ip_datamatch", "ut-large-ip-match-0",
			`can you get me all the pods with IPs, across all namespaces -- [{"client_addr":"192.168.1.10","count":"4"},
{"client_addr":"103.197.75.144","count":"3"},
{"client_addr":"192.168.1.2","count":"4"},
{"client_addr":"192.168.1.7","count":"5"},
{"client_addr":"192.168.1.8","count":"5"},
{"client_addr":"192.168.1.3","count":"8"},
{"client_addr":"192.168.1.11","count":"9"},
{"client_addr":"192.168.1.5","count":"1"},
{"client_addr":"192.168.1.6","count":"16"}]`)
		runTestMinimal(t, agent, tc)
	})
}

// ============================================================
// 10. Workspace Features — persistent file/shell in workspace pod
// ============================================================

// TestK8sAgent_WorkspaceFeatures verifies workspace pod creation and shell access.
func TestK8sAgent_WorkspaceFeatures(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("TEST_ACCOUNT not set")
	}

	// Enable the shell tool for these tests
	orig := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = true
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = orig })

	agent := newK8sDebugAgent(os.Getenv("TEST_ACCOUNT"))

	shellTools := []string{"shell_execute", "workspace"}
	mk := func(name, sessionId, query string) k8sTestCase {
		c := defaultCase(name, sessionId, query)
		c.WantAnyToolMatching = shellTools
		c.WantMinToolCalls = 1
		return c
	}
	cases := []k8sTestCase{
		mk("workspace_create_todo", "ut-workspace-todo-0",
			`can you create a todo file for me in my workspace specifying security vulns i have.`),
		mk("workspace_internet_check", "ut-workspace-internet-0",
			`can you check if internet is working fine?`),
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			runTestMinimal(t, agent, tc)
		})
	}
}

// ============================================================
// 11. Observability — metrics, traces, logs analysis
// ============================================================

// TestK8sAgent_Observability covers queries that use observability tooling
// (Prometheus, Loki/log backends, distributed tracing).
func TestK8sAgent_Observability(t *testing.T) {
	skipIfNoFixtureEnv(t)
	agent := newK8sDebugAgent(os.Getenv("TEST_ACCOUNT"))

	obsTools := []string{"trace", "loki", "logs", "prometheus", "metric", "tempo"}
	mk := func(name, sessionId, query string) k8sTestCase {
		c := defaultCase(name, sessionId, query)
		c.WantAnyToolMatching = obsTools
		c.WantMinToolCalls = 1
		return c
	}
	cases := []k8sTestCase{
		mk("traces_api_failures", "ut-obs-traces-api-0",
			`can you analyze api failures for k8s collector in nudgebee namespace and suggest code fixes with proper formatting`),
		mk("traces_slow_endpoint", "ut-obs-traces-slow-0",
			`can you investigate why /api/web/search/analytics for frontend-service app in demo namespace was slow since last week?`),
		// "Why logs are not connected" is a meta/diagnostic question — may not call tools
		defaultCase("logs_status_not_connected", "ut-obs-logs-status-0",
			`Why logs are not connected ?`),
		mk("slowness_app_dev", "ut-obs-slowness-0",
			`I am observing slowness on app-dev in nudgebee namespace, can you debug`),
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			runTestMinimal(t, agent, tc)
		})
	}
}

// TestK8sAgent_PrometheusAgent validates the dedicated Prometheus sub-agent.
func TestK8sAgent_PrometheusAgent(t *testing.T) {
	skipIfNoFixtureEnv(t)
	prometheusAgent := newPrometheusAgent(os.Getenv("TEST_ACCOUNT"))

	tc := defaultCase("requests_per_hour", "ut-prom-rph-0",
		`tell me number of request per hour for auto pilot server in nudgebee namespace`)
	tc.WantAnyToolMatching = []string{"prometheus", "metric"}
	tc.WantMinToolCalls = 1

	sc := newSC(tc)
	err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
	assert.Nil(t, err)

	resp, err := core.HandleConversationSessionRequest(sc, prometheusAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
	fmt.Println("response -", resp.Response)
	fmt.Println("tools -", resp.AgentStepResponse)

	assert.Nil(t, err)
	assert.Equal(t, prometheusAgent.GetName(), resp.AgentName)
	assert.NotEmpty(t, resp.Query)
	assert.Greater(t, len(resp.Response), 0)
	assertExpectations(t, tc, resp)
}

// ============================================================
// 12. Cluster Operations — versions, security, node issues, service map
// ============================================================

// TestK8sAgent_ClusterOperations covers general cluster-level queries.
func TestK8sAgent_ClusterOperations(t *testing.T) {
	skipIfNoFixtureEnv(t)
	agent := newK8sDebugAgent(os.Getenv("TEST_ACCOUNT"))

	clusterTools := []string{"kubectl", "k8s_", "describe", "get_", "list_"}
	clusterCase := func(name, sessionId, query string) k8sTestCase {
		c := defaultCase(name, sessionId, query)
		c.WantAnyToolMatching = clusterTools
		c.WantMinToolCalls = 1
		return c
	}

	cases := []k8sTestCase{
		clusterCase("recent_issues", "ut-cluster-issues-0",
			`show me recent issues in my cluster.`),
		clusterCase("security_cis_issues", "ut-cluster-cis-0",
			`list all cis security issues ?`),
		clusterCase("node_removal_karpenter", "ut-cluster-node-karpenter-0",
			`My k8s Nodes are getting frequently removed, can you investigate why ? I am using karpenter so check that as well in karpenter namespace`),
		clusterCase("version_nudgebee_agent_runner", "ut-cluster-version-nar-0",
			`what version of my-agent-runner is running in my cluster`),
		clusterCase("image_version_nudgebee_agent_runner", "ut-cluster-imgver-nar-0",
			`what image version of my-agent-runner is running in my cluster`),
		clusterCase("version_app_dev", "ut-cluster-ver-appdev-0",
			`what is current version of app-dev`),
		// "How can I troubleshoot" is conceptual — may not invoke tools
		defaultCase("kubernetes_jobs_failing", "ut-cluster-jobs-0",
			`I have some Kubernetes Jobs that are not completing successfully. How can I troubleshoot this?`),
		clusterCase("pvc_storage_review", "ut-cluster-pvc-0",
			`Can you review current PVC storage and available storage across all the PVCs`),
		clusterCase("filesystem_external_host", "ut-cluster-fs-host-0",
			`Can you check filesystem usage of host nb-dev-db`),
		clusterCase("service_dependencies", "ut-cluster-svc-deps-0",
			`Can you get me dependencies of services-server in nudgebee namespace`),
		clusterCase("pod_dependencies", "ut-cluster-pod-deps-0",
			`Can you get me dependencies of the app-dev deployment in nudgebee namespace`),
		// Off-topic query — agent should refuse or answer directly without cluster tools
		defaultCase("generic_search", "ut-cluster-search-0",
			`pl matches live score`),
		clusterCase("registry_status", "ut-cluster-registry-0",
			`Can you help me check the status of the 'registry.example.com' registry for any ongoing issues?`),
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			runTestMinimal(t, agent, tc)
		})
	}
}

// ============================================================
// 13. Multi-Step Conversation — context preserved across turns
// ============================================================

// TestK8sAgent_MultiStepConversation verifies that follow-up queries within the same
// session correctly reference earlier results.
func TestK8sAgent_MultiStepConversation(t *testing.T) {
	skipIfNoFixtureEnv(t)

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	sessionId := "ut-multistep-conv-0"
	agent := newK8sDebugAgent(accountId)
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userId, []string{accountId})

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	// Turn 1 — broad status (must use kubectl-style tool)
	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		`can u get me status of nudgebee namespace ?`)
	assert.Nil(t, err)
	assert.Equal(t, core.ConversationStatusCompleted, resp.Status)
	assert.Greater(t, len(resp.AgentStepResponse), 0, "turn 1 should invoke at least one tool")

	// Turn 2 — follow-up using context from turn 1
	resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		`was there any recent restarts ?`)
	assert.Nil(t, err)
	assert.Equal(t, core.ConversationStatusCompleted, resp.Status)
	assert.Greater(t, len(resp.Response), 0)

	// Turn 3 — metric query in same session (should invoke prometheus/metric tooling)
	resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		`ok.. can you check max memory usage of llm-server in last 24 hours ?`)
	assert.Nil(t, err)
	assert.Equal(t, core.ConversationStatusCompleted, resp.Status)

	turn3Tools, _ := json.Marshal(resp.AgentStepResponse)
	turn3Log := string(turn3Tools)
	matched := false
	for _, sub := range []string{"prometheus", "metric", "memory"} {
		if strings.Contains(turn3Log, sub) {
			matched = true
			break
		}
	}
	assert.True(t, matched, "turn 3 should invoke a metric/memory tool, got: %s", turn3Log)
}

// ============================================================
// 14. Repeated Queries — identical queries in successive sessions
// ============================================================

// TestK8sAgent_RepeatedQueries smoke-tests that the same query in two distinct
// sessions both succeed and reach the same tool family. It does NOT directly
// assert prompt-cache hit rate (that requires token telemetry plumbed into the
// response, which we don't have access to here). This is a regression guard
// for "second identical run blows up" — true cache validation belongs in the
// Python benchmark harness which captures per-run token cost.
func TestK8sAgent_RepeatedQueries(t *testing.T) {
	skipIfNoFixtureEnv(t)
	agent := newK8sDebugAgent(os.Getenv("TEST_ACCOUNT"))

	sessions := []string{"ut-repeat-pods-0", "ut-repeat-pods-1"}
	for i, sessionId := range sessions {
		tc := k8sTestCase{
			Name:                fmt.Sprintf("list_pods_run_%d", i+1),
			SessionId:           sessionId,
			AccountId:           os.Getenv("TEST_ACCOUNT"),
			UserId:              os.Getenv("TEST_USER"),
			Query:               `give me the list of running pods in nudgebee namespace`,
			WantAnyToolMatching: []string{"kubectl", "k8s_", "list_"},
			WantMinToolCalls:    1,
		}
		t.Run(tc.Name, func(t *testing.T) {
			runTest(t, agent, tc)
		})
	}
}

// ============================================================
// 15. Visualization — architecture diagrams and charts
// ============================================================

// TestK8sAgent_Visualization validates that the agent can produce visual output
// (architecture diagrams, metric charts).
func TestK8sAgent_Visualization(t *testing.T) {
	skipIfNoFixtureEnv(t)
	agent := newK8sDebugAgent(os.Getenv("TEST_ACCOUNT"))

	cases := []k8sTestCase{
		{
			Name:      "architecture_diagram",
			SessionId: "ut-viz-arch-0",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     `Visualize the architecture of llm-server deployment in nudgebee namespace using a diagram`,
			// Diagram output should appear as a code-fenced block, mermaid, or graphviz
			WantContainsAny: []string{"mermaid", "graph", "```", "digraph", "flowchart"},
		},
		{
			Name:                "memory_chart",
			SessionId:           "ut-viz-chart-0",
			AccountId:           os.Getenv("TEST_ACCOUNT"),
			UserId:              os.Getenv("TEST_USER"),
			Query:               `Get me a chart of memory usage of ml-k8s-server deployment in nudgebee namespace`,
			WantAnyToolMatching: []string{"prometheus", "metric", "chart"},
			WantMinToolCalls:    1,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			runTestMinimal(t, agent, tc)
		})
	}
}

// ============================================================
// 16. Response Format — structured output validation
// ============================================================

// TestK8sAgent_ResponseFormatJSON validates that the agent returns well-formed JSON
// when explicitly requested.
func TestK8sAgent_ResponseFormatJSON(t *testing.T) {
	skipIfNoFixtureEnv(t)
	agent := newK8sDebugAgent(os.Getenv("TEST_ACCOUNT"))

	tc := defaultCase("pods_json_format", "ut-fmt-json-pods-0",
		`get pod list in nudgebee namespace in json format only with fields name, status, node, ip address`)

	sc := newSC(tc)
	err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
	assert.Nil(t, err)

	resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
	assert.Nil(t, err)
	assert.Greater(t, len(resp.Response), 0)

	// At least one response block must parse as JSON (stripping Markdown fences
	// if present). LLMs commonly bracket JSON output with explanatory prose, so
	// asserting *every* block parses is too strict; asserting "JSON appears
	// somewhere in the answer" is the right contract for a "give me JSON"
	// query.
	hasJSON := false
	for _, r := range resp.Response {
		cleaned := stripCodeFence(r)
		var jsObj map[string]interface{}
		var jsArr []map[string]interface{}
		if json.Unmarshal([]byte(cleaned), &jsObj) == nil ||
			json.Unmarshal([]byte(cleaned), &jsArr) == nil {
			hasJSON = true
			break
		}
	}
	assert.True(t, hasJSON, "expected at least one response block to be valid JSON; got blocks: %v", resp.Response)
}

// ============================================================
// 17. Cross-Account & MCP Integration
// ============================================================

// TestK8sAgent_MCPInvocation tests the MCP remote-fetch tool integration.
func TestK8sAgent_MCPInvocation(t *testing.T) {
	if os.Getenv("TEST_MCP_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" || os.Getenv("TEST_TENANT") == "" {
		t.Skip("skipping: TEST_MCP_ACCOUNT, TEST_USER, TEST_TENANT not set")
	}

	accountId := os.Getenv("TEST_MCP_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	sessionId := "ut-mcp-fetch-0"
	agent := newK8sDebugAgent(accountId)
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userId, []string{accountId})

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	// External URL the MCP fetch tool will retrieve; defaults to example.com so
	// the test isn't pointed at a specific company's prod site.
	fetchURL := envOr("TEST_MCP_FETCH_URL", "https://example.com")
	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		fmt.Sprintf(`Can you check %s && provide summary using remote_fetch_mcp_fetch tool.`, fetchURL))
	assert.Nil(t, err)
	assert.Equal(t, core.ConversationStatusCompleted, resp.Status)
	assert.Equal(t, agent.GetName(), resp.AgentName)
	assert.NotEmpty(t, resp.Query)
	assert.Greater(t, len(resp.Response), 0)

	// Verifies the MCP tool was actually invoked (the whole point of the test).
	// Implies AgentStepResponse is non-empty — no need for a separate check.
	invocationLog, _ := json.Marshal(resp.AgentStepResponse)
	assert.Contains(t, string(invocationLog), "remote_fetch_mcp_fetch",
		"expected remote_fetch_mcp_fetch tool to be invoked")
}

// TestK8sAgent_CrossAccount tests that an agent can be directed to use a different
// cloud account's data source for investigation (e.g., GCP Cloud SQL metrics).
func TestK8sAgent_CrossAccount(t *testing.T) {
	skipIfNoFixtureEnv(t)

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	sessionId := "ut-xacct-gcp-sql-0"
	agent := newK8sDebugAgent(accountId)
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userId, []string{accountId})

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	// The cross-account workspace name is account-specific. Without it set we
	// can't drive the approval round that selects the GCP account, so skip
	// rather than ship an internal value as the default.
	gcpConfigName := os.Getenv("TEST_GCP_CONFIG_NAME")
	if gcpConfigName == "" {
		t.Skip("skipping: TEST_GCP_CONFIG_NAME not set")
	}

	// Alert payload uses placeholder identifiers — the test cares about the
	// agent's handling of an alert *shape*, not the specific project / policy
	// IDs in the original payload (which named internal infra).
	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		fmt.Sprintf(`Can you investigate following GKE alert.

- Focus on dev postgres instance for query/connections etc
- GKE Cloud SQL for getting metrices, use %q account

{
  "incident": {
    "condition": {
      "conditionThreshold": {
        "aggregations": [{"alignmentPeriod": "600s", "perSeriesAligner": "ALIGN_SUM"}],
        "comparison": "COMPARISON_GT",
        "duration": "0s",
        "filter": "resource.type = \"cloudsql_database\" AND metric.type = \"logging.googleapis.com/user/pg-slow-queries\"",
        "thresholdValue": 5,
        "trigger": {"count": 1}
      },
      "displayName": "Slow queries (>10s) on dev PG > 5 in 10 min",
      "name": "projects/<gcp-project>/alertPolicies/<policy-id>/conditions/<condition-id>"
    },
    "condition_name": "Slow queries (>10s) on dev PG > 5 in 10 min",
    "policy_name": "pg-slow-queries",
    "resource": {"labels": {"database_id": "<gcp-project>:<db-instance>"}, "type": "cloudsql_database"},
    "severity": "Warning",
    "state": "closed",
    "summary": "logging/user/pg-slow-queries returned to normal with a value of 5.000."
  },
  "version": "1.2"
}`, gcpConfigName))
	assert.Nil(t, err)

	// If the agent asks which account to use, provide the cross-account answer
	if resp.Status == core.ConversationStatusWaiting {
		resp, err = sendApproval(t, sc, agent, resp, k8sTestCase{
			SessionId: sessionId, AccountId: accountId, UserId: userId,
		}, gcpConfigName)
		assert.Nil(t, err)
	}

	assert.Equal(t, agent.GetName(), resp.AgentName)
	assert.NotEmpty(t, resp.Query)
	assert.NotNil(t, resp.AgentStepResponse)
	assert.Greater(t, len(resp.Response), 0)
}

// ============================================================
// 18. Custom Agent from DB
// ============================================================

// TestK8sAgent_CustomAgentFromDB tests loading a custom agent configuration from the
// database (as opposed to a hard-coded agent factory registration).
func TestK8sAgent_CustomAgentFromDB(t *testing.T) {
	skipIfNoFixtureEnv(t)

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	sessionId := "ut-custom-agent-db-0"
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userId, []string{accountId})

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	// Agent loaded dynamically from DB
	agent, ok := core.GetNBAgent(sc, AgentK8sDebugName, accountId, core.AgentStatusEnabled)
	assert.True(t, ok, "k8s_debug agent should be retrievable from DB")

	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		`Can you investigate the performance issues in the cluster?`)
	assert.Nil(t, err)
	assert.Equal(t, agent.GetName(), resp.AgentName)
	assert.Greater(t, len(resp.Response), 0)
}

// TestK8sAgent_CustomCodeAgent tests using a custom agent named "test_code_agent".
func TestK8sAgent_CustomCodeAgent(t *testing.T) {
	skipIfNoFixtureEnv(t)

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	sessionId := "ut-custom-code-agent-0"
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userId, []string{accountId})

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	agent, ok := core.GetNBAgent(sc, "test_code_agent", accountId, core.AgentStatusEnabled)
	if !ok {
		t.Skip("test_code_agent not configured for this account")
	}

	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		`check the workflow-server deployment in nudgebee namespace and get me complete detail`)
	assert.Nil(t, err)
	assert.Equal(t, "test_code_agent", resp.AgentName)
	assert.Greater(t, len(resp.Response), 0)
}

// ============================================================
// 19. RAG Integration — system-prompt / knowledge-base wiring
// ============================================================

// TestK8sAgent_RAGIntegration verifies that the agent's system prompt is
// properly assembled with RAG wiring intact: the Role/Instructions are
// populated AND the Rag.Module field is set so the planner knows which KB
// to retrieve from. Without the Rag.Module check, this test would pass on a
// trivially empty prompt or a non-RAG agent.
func TestK8sAgent_RAGIntegration(t *testing.T) {
	skipIfNoFixtureEnv(t)

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	sessionId := "ut-rag-prompt-0"
	query := `My app is slow how I can troubleshoot this`

	agent := newK8sDebugAgent(accountId)
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userId, []string{accountId})

	prompt := agent.GetSystemPrompt(sc, core.NBAgentRequest{
		Query:          query,
		ConversationId: sessionId,
		AccountId:      accountId,
		UserId:         userId,
	})

	assert.NotEmpty(t, prompt.Role, "expected Role to be assembled")
	assert.NotEmpty(t, prompt.Instructions, "expected Instructions to be populated")
	assert.NotEmpty(t, prompt.Rag.Module,
		"expected Rag.Module to be set so retrieval is wired; got empty Rag struct")
}

// ============================================================
// 20. Function Execution with Approval
// ============================================================

// TestK8sAgent_FunctionExecutionWithApproval tests invoking a custom function (/call)
// that may request additional context before execution. The CI repo / action /
// approval value are taken from env vars so the test isn't tied to internal
// infra; the test skips when they aren't set.
func TestK8sAgent_FunctionExecutionWithApproval(t *testing.T) {
	skipIfNoFixtureEnv(t)

	repo, action := os.Getenv("TEST_CI_REPO"), os.Getenv("TEST_CI_ACTION")
	if repo == "" || action == "" {
		t.Skip("skipping: TEST_CI_REPO / TEST_CI_ACTION not set")
	}
	approval := os.Getenv("TEST_CI_APPROVAL_NAME")
	if approval == "" {
		approval = "test-user"
	}

	agent := newK8sDebugAgent(os.Getenv("TEST_ACCOUNT"))

	tc := k8sTestCase{
		Name:              "ci_failure_investigation",
		SessionId:         "ut-fn-ci-approval-0",
		AccountId:         os.Getenv("TEST_ACCOUNT"),
		UserId:            os.Getenv("TEST_USER"),
		Query:             fmt.Sprintf("/call check_ci_failure investigate failure of action `%s` in %s repo", action, repo),
		ApprovalResponses: []string{approval},
	}
	runTestMinimal(t, agent, tc)
}
