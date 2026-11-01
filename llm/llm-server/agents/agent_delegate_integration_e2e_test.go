//go:build e2e

package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// TestDelegateAgent_PostgresConnectionPoolInvestigation tests that the k8s_debug
// agent can leverage delegate_agent to spawn a specialist sub-agent for cross-domain
// investigation combining PostgreSQL internals with infrastructure metrics.
func TestDelegateAgent_PostgresConnectionPoolInvestigation(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" || os.Getenv("TEST_TENANT") == "" {
		t.Skip("skipping integration test: TEST_ACCOUNT, TEST_USER, TEST_TENANT not set")
	}

	testCases := []struct {
		Name      string
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			Name:      "postgres_pool_exhaustion_with_delegate",
			SessionId: "ut-delegate-agent-pg-pool-1",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query: `The checkout-svc in production namespace is experiencing intermittent 500 errors.
We suspect PostgreSQL connection pool exhaustion. The service uses pgbouncer with max_client_conn=100.

Investigate:
1. Check the pod status and recent events for checkout-svc
2. Analyze PostgreSQL connections - active vs idle, waiting clients, pool saturation
3. Correlate with application logs for connection timeout errors

Use a specialist to deep-dive into the database performance if needed.`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
			k8sAgent := newK8sOrchestratorAgent(tc.AccountId)

			// Verify delegate_agent is in the supported tools
			tools := k8sAgent.GetSupportedTools(sc)
			hasDelegateAgent := false
			for _, tool := range tools {
				if tool.Name() == DelegateAgentToolName {
					hasDelegateAgent = true
					break
				}
			}
			assert.True(t, hasDelegateAgent, "k8s_debug agent should have delegate_agent in its supported tools")

			sessionId := fmt.Sprintf("%s-%d", tc.SessionId, time.Now().Unix())
			t.Logf("Running with preserved sessionId: %s", sessionId)

			resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, sessionId, tc.Query)

			assert.Nil(t, err)
			if err != nil {
				return
			}
			assert.NotNil(t, resp)

			// If Turn 1 paused for approval or database selection (WAITING), execute Turn 2
			// to complete the investigation end-to-end.
			if resp.Status == core.ConversationStatusWaiting {
				t.Logf("Turn 1 is waiting for selection/approval: %s", resp.FollowupRequest.Question)
				selectedOption := "dev-pg"
				if len(resp.FollowupRequest.FollowupOptions) > 0 {
					if lo.Contains(resp.FollowupRequest.FollowupOptions, "dev-pg") {
						selectedOption = "dev-pg"
					} else {
						selectedOption = resp.FollowupRequest.FollowupOptions[0]
					}
				}
				subAgentId := resp.FollowupRequest.AgentId
				if subAgentId == uuid.Nil {
					subAgentId = fetchLatestWaitingDelegateSubAgentId(t, resp.ConversationId)
				}
				require.NotEqual(t, uuid.Nil, subAgentId, "sub-agent ID must not be nil for waiting conversation")

				resumedAgent, _, _, rErr := core.ResolveAgentByConversationAgentId(sc, subAgentId, tc.AccountId)
				require.NoError(t, rErr, "failed to resolve resumed agent")
				require.NotNil(t, resumedAgent, "resumed agent must not be nil")

				resp2, rErr := core.HandleConversationSessionRequest(sc, resumedAgent, tc.UserId, tc.AccountId, sessionId, selectedOption,
					core.ConversationSessionRequestWithConversationId(uuid.NullUUID{UUID: uuid.MustParse(resp.ConversationId), Valid: true}),
					core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: uuid.MustParse(resp.MessageId), Valid: true}),
					core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: subAgentId, Valid: true}),
					core.ConversationSessionRequestWithIsResume(true),
				)
				require.NoError(t, rErr, "Turn 2 resume must not error")
				t.Logf("Turn 2 response status: %s", resp2.Status)
				resp = resp2
			}

			fmt.Println("response - ", resp.Response)
			invocationLog, _ := json.Marshal(resp.AgentStepResponse)
			fmt.Println("tools - ", string(invocationLog))
			t.Log("tools - ", string(invocationLog))

			assert.NotEmpty(t, resp.Query)
			assert.NotNil(t, resp.AgentStepResponse)
			assert.Greater(t, len(resp.Response), 0)

			// Verify the response addresses postgres/database concerns
			responseText := strings.Join(resp.Response, " ")
			responseLower := strings.ToLower(responseText)
			invocationLogStr := strings.ToLower(string(invocationLog))

			// The planner should have used postgres-related tools or delegate_agent
			usedPostgres := strings.Contains(invocationLogStr, "postgres")
			usedDelegate := strings.Contains(invocationLogStr, "delegate_agent")
			t.Logf("Used postgres tool: %v, Used delegate_agent: %v", usedPostgres, usedDelegate)

			// At minimum, the response should discuss postgres/database/connection
			hasDbContext := strings.Contains(responseLower, "postgres") ||
				strings.Contains(responseLower, "database") ||
				strings.Contains(responseLower, "connection")
			assert.True(t, hasDbContext, "Response should address database/postgres/connection concerns")
		})
	}
}

// TestDelegateAgent_ParallelSpecialistInvestigation tests that the debug agent
// can dispatch multiple delegate_agent calls (or mixed tool calls) for parallel
// investigation of independent sub-problems.
func TestDelegateAgent_ParallelSpecialistInvestigation(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" || os.Getenv("TEST_TENANT") == "" {
		t.Skip("skipping integration test: TEST_ACCOUNT, TEST_USER, TEST_TENANT not set")
	}

	testCases := []struct {
		Name      string
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			Name:      "parallel_db_and_infra_investigation",
			SessionId: "ut-delegate-agent-parallel-1",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query: `The order-processing service in production is failing. We see two independent issues:
1. PostgreSQL: "too many connections" errors in the logs - need to check pg_stat_activity, connection pool config, and slow queries
2. Pod health: pods are restarting with OOMKilled - need to check memory limits, actual usage, and container resource metrics

These are independent problems. Investigate both in parallel.`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
			k8sAgent := newK8sOrchestratorAgent(tc.AccountId)

			sessionId := fmt.Sprintf("%s-%d", tc.SessionId, time.Now().Unix())
			t.Logf("Running with preserved sessionId: %s", sessionId)

			resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, sessionId, tc.Query)

			assert.Nil(t, err)
			if err != nil {
				return
			}
			assert.NotNil(t, resp)

			// If Turn 1 paused for approval or database selection (WAITING), execute Turn 2
			if resp.Status == core.ConversationStatusWaiting {
				t.Logf("Turn 1 is waiting for selection/approval: %s", resp.FollowupRequest.Question)
				selectedOption := "dev-pg"
				if len(resp.FollowupRequest.FollowupOptions) > 0 {
					if lo.Contains(resp.FollowupRequest.FollowupOptions, "dev-pg") {
						selectedOption = "dev-pg"
					} else {
						selectedOption = resp.FollowupRequest.FollowupOptions[0]
					}
				}
				subAgentId := resp.FollowupRequest.AgentId
				if subAgentId == uuid.Nil {
					subAgentId = fetchLatestWaitingDelegateSubAgentId(t, resp.ConversationId)
				}
				if subAgentId != uuid.Nil {
					resumedAgent, _, _, rErr := core.ResolveAgentByConversationAgentId(sc, subAgentId, tc.AccountId)
					if rErr == nil && resumedAgent != nil {
						resp2, rErr := core.HandleConversationSessionRequest(sc, resumedAgent, tc.UserId, tc.AccountId, sessionId, selectedOption,
							core.ConversationSessionRequestWithConversationId(uuid.NullUUID{UUID: uuid.MustParse(resp.ConversationId), Valid: true}),
							core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: uuid.MustParse(resp.MessageId), Valid: true}),
							core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: subAgentId, Valid: true}),
							core.ConversationSessionRequestWithIsResume(true),
						)
						if rErr == nil {
							t.Logf("Turn 2 response status: %s", resp2.Status)
							resp = resp2
						}
					}
				}
			}

			fmt.Println("response - ", resp.Response)
			invocationLog, _ := json.Marshal(resp.AgentStepResponse)
			fmt.Println("tools - ", string(invocationLog))
			t.Log("tools - ", string(invocationLog))

			assert.NotEmpty(t, resp.Query)
			assert.NotNil(t, resp.AgentStepResponse)
			assert.Greater(t, len(resp.Response), 0)

			// Response should cover both postgres and k8s/OOM aspects
			responseText := strings.Join(resp.Response, " ")
			responseLower := strings.ToLower(responseText)

			hasDbContext := strings.Contains(responseLower, "postgres") ||
				strings.Contains(responseLower, "connection") ||
				strings.Contains(responseLower, "database")
			hasK8sContext := strings.Contains(responseLower, "oom") ||
				strings.Contains(responseLower, "memory") ||
				strings.Contains(responseLower, "restart")

			t.Logf("Has DB context: %v, Has K8s context: %v", hasDbContext, hasK8sContext)
			assert.True(t, hasDbContext || hasK8sContext, "Response should address at least one of the two investigation areas")
		})
	}
}

// delegateE2ETarget returns a connected target name for the targeted-handoff
// tests, overridable per environment. Conversations are kept for review.
func delegateE2ETarget(envKey, fallback string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return fallback
}

type e2eToolCall struct {
	MessageId, AgentName, DelegatedTarget, ToolName, Status, Parameters, Response string
	// AgentAccount is the account the calling agent runs under. The metrics and
	// traces sub-agents run under the target's own account with no delegated
	// target set.
	AgentAccount string
}

// fetchConversationToolCalls returns every tool call in a conversation with
// its agent and that agent's persisted delegated target.
func fetchConversationToolCalls(t *testing.T, conversationId string) []e2eToolCall {
	t.Helper()
	dbm, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	rows, err := dbm.Db.Query(`
		SELECT COALESCE(t.message_id::text, ''), a.agent_name, COALESCE(ca.account_name, ''),
		       COALESCE(a.query_config::jsonb->'delegated_target'->>'name', ''),
		       t.tool_name, COALESCE(t.status, ''),
		       -- SQL tools persist their real input (SQL plus any target) in params_sql.
		       COALESCE(NULLIF(t.params_sql, ''), t.parameters, ''), COALESCE(t.response, '')
		FROM llm_conversation_tool_calls t
		JOIN llm_conversation_agent a ON a.id = t.agent_id
		LEFT JOIN cloud_accounts ca ON ca.id = a.account_id
		WHERE t.conversation_id = $1
		ORDER BY t.created_at`, conversationId)
	require.NoError(t, err)
	defer rows.Close()
	var calls []e2eToolCall
	for rows.Next() {
		var c e2eToolCall
		require.NoError(t, rows.Scan(&c.MessageId, &c.AgentName, &c.AgentAccount, &c.DelegatedTarget, &c.ToolName, &c.Status, &c.Parameters, &c.Response))
		t.Logf("tool call: agent=%s target=%q tool=%s status=%s params=%s", c.AgentName, c.DelegatedTarget, c.ToolName, c.Status, c.Parameters)
		calls = append(calls, c)
	}
	require.NoError(t, rows.Err())
	return calls
}

// tenantWideE2EContext can reach every connection in the tenant, as a user
// with access to the other cluster/account would.
func tenantWideE2EContext() *security.RequestContext {
	return security.NewRequestContextForTenantAdminWithUser(context.Background(), os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"))
}

func runPreservedE2EConversation(t *testing.T, sc *security.RequestContext, agent core.NBAgent, sessionPrefix, query string) core.NBAgentResponse {
	t.Helper()
	accountId, userId := os.Getenv("TEST_ACCOUNT"), os.Getenv("TEST_USER")
	sessionId := fmt.Sprintf("%s-%d", sessionPrefix, time.Now().Unix())
	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId, query)
	require.NoError(t, err)
	t.Logf("Preserved sessionId=%s conversationId=%s status=%s", sessionId, resp.ConversationId, resp.Status)
	return resp
}

func requireDelegateE2EEnv(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" || os.Getenv("TEST_TENANT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT, TEST_USER, and TEST_TENANT env vars are not set")
	}
}

// awsAccountLine matches one sts get-caller-identity "Account" field in raw or
// escaped JSON (the ARN's "CrossAccountRole" must not count).
var awsAccountLine = regexp.MustCompile(`Account\W+\d{12}`)

// TestShellExecute_AwsRegionLoopWithoutTarget is the regression found in
// review: an untargeted aws region loop was rejected as "aws-led" ambiguous.
// It calls shell_execute directly (real workspace shim + cloud-collector) so
// the loop cannot be rewritten by a model into a single command.
func TestShellExecute_AwsRegionLoopWithoutTarget(t *testing.T) {
	requireDelegateE2EEnv(t)
	accountId := os.Getenv("TEST_ACCOUNT")
	profile := delegateE2ETarget("TEST_DELEGATE_AWS_TARGET", "aws-prod")
	shell, ok := toolcore.GetNBTool(accountId, toolcore.ToolExecuteShellCommand)
	require.True(t, ok, "shell_execute must be registered")
	conversationId := uuid.NewString()
	t.Logf("workspace conversationId=%s", conversationId)
	ctx := toolcore.NewNbToolContext(tenantWideE2EContext(), shell, accountId, os.Getenv("TEST_USER"), conversationId, uuid.NewString(), uuid.NewString(), "", nil, "", toolcore.NBQueryConfig{}, "aws-region-loop")
	command := fmt.Sprintf(`for r in us-east-1 us-west-2; do echo "== $r"; aws sts get-caller-identity --region "$r" --profile %s --output json; done`, profile)

	resp, err := shell.Call(ctx, toolcore.NBToolCallRequest{Command: command})
	t.Logf("status=%s output=%v", resp.Status, resp.Data)
	require.NoError(t, err)
	output := fmt.Sprintf("%v", resp.Data)
	assert.NotContains(t, output, "aws-led command")
	assert.Contains(t, output, "== us-west-2", "loop must reach its second region")
	assert.Len(t, awsAccountLine.FindAllString(output, -1), 2, "both invocations must authenticate")
}

// The tests below ask the way a user would, from a conversation that belongs
// to TEST_ACCOUNT, and never tell the planner which tool, agent or argument to
// use. The planner may delegate or call a tool directly; the assertions are on
// the outcome (where commands actually ran), and the route taken is logged.
// Exact handoff mechanics are covered by deterministic unit tests.

// ranOn reports whether a persisted tool call executed against the named
// connection. Persisted parameters are the prepared input, so an inherited
// delegated target is visible there too.
func ranOn(c e2eToolCall, name string) bool {
	params, resp, n := strings.ToLower(c.Parameters), strings.ToLower(c.Response), strings.ToLower(name)
	for _, marker := range []string{
		fmt.Sprintf(`"cluster":%q`, n), fmt.Sprintf(`"account":%q`, n),
		"--context " + n, "--kube-context " + n, "--profile " + n,
	} {
		if strings.Contains(params, marker) {
			return true
		}
	}
	if strings.Contains(params, `"target":`) && strings.Contains(params, fmt.Sprintf(`"name":%q`, n)) {
		return true // observability tools carry {"target":{"provider":…,"name":…}}
	}
	return strings.Contains(resp, fmt.Sprintf(`"cluster":%q`, n))
}

func isRemoteCall(c e2eToolCall, cli string) bool {
	switch c.ToolName {
	case "kubectl_execute", "helm_execute":
		return cli == "kubectl"
	case "aws_execute":
		return cli == "aws"
	case "gcloud_execute":
		return cli == "gcloud"
	case "shell_execute":
		return strings.Contains(c.Parameters, cli+" ")
	}
	return false
}

func logRoute(t *testing.T, calls []e2eToolCall) {
	t.Helper()
	for _, c := range calls {
		if c.ToolName == "delegate_agent" {
			t.Logf("route: planner delegated (params=%s)", c.Parameters)
		}
		if c.DelegatedTarget != "" {
			t.Logf("route: %s ran %s inside a child targeted to %q", c.AgentName, c.ToolName, c.DelegatedTarget)
			return
		}
	}
	t.Log("route: no targeted child; commands named their target per call")
}

// TestNaturalQuestion_OtherClusterRunsThere: a user in the dev cluster's
// conversation asks about another connected cluster by name.
func TestNaturalQuestion_OtherClusterRunsThere(t *testing.T) {
	requireDelegateE2EEnv(t)
	target := delegateE2ETarget("TEST_DELEGATE_K8S_TARGET", "k8s-prod")
	query := fmt.Sprintf("What namespaces are there on %s, and are any of them stuck terminating?", target)
	resp := runPreservedE2EConversation(t, tenantWideE2EContext(), newK8sOrchestratorAgent(os.Getenv("TEST_ACCOUNT")), "natural-other-cluster", query)

	calls := fetchConversationToolCalls(t, resp.ConversationId)
	logRoute(t, calls)
	var onTarget int
	for _, c := range calls {
		if isRemoteCall(c, "kubectl") && ranOn(c, target) && c.Status == "success" {
			onTarget++
		}
	}
	require.NotZero(t, onTarget, "at least one kubectl call must run on %s", target)
	for _, c := range calls {
		if c.DelegatedTarget != "" {
			assert.Equal(t, target, c.DelegatedTarget, "a targeted child must carry the cluster the user named")
		}
	}
}

// toolDataString returns a tool response's data as text: a string as is,
// anything else as JSON (not Go's %v formatting, which the helpers below
// could not parse).
func toolDataString(data any) string {
	if str, ok := data.(string); ok {
		return str
	}
	if encoded, err := json.Marshal(data); err == nil {
		return string(encoded)
	}
	return fmt.Sprintf("%v", data)
}

// clusterNodes is ground truth for node-level metrics: the node names kubectl
// reports on a named cluster.
func clusterNodes(t *testing.T, cluster string) []string {
	t.Helper()
	accountId := os.Getenv("TEST_ACCOUNT")
	kubectl, ok := toolcore.GetNBTool(accountId, "kubectl_execute")
	require.True(t, ok)
	ctx := toolcore.NewNbToolContext(tenantWideE2EContext(), kubectl, accountId, os.Getenv("TEST_USER"), uuid.NewString(), uuid.NewString(), uuid.NewString(), "", nil, "", toolcore.NBQueryConfig{}, "oracle-nodes-"+cluster)
	resp, err := kubectl.Call(ctx, toolcore.NBToolCallRequest{
		Command:   "kubectl get nodes --no-headers -o custom-columns=:metadata.name",
		Arguments: map[string]any{"cluster": cluster},
	})
	require.NoError(t, err)
	out := toolDataString(resp.Data)
	var envelope struct{ Stdout string }
	if json.Unmarshal([]byte(out), &envelope) == nil && envelope.Stdout != "" {
		out = envelope.Stdout
	}
	nodes := strings.Fields(out)
	require.NotEmpty(t, nodes, "oracle must list nodes on %s", cluster)
	return nodes
}

func nodesIn(response string, nodes []string) []string {
	var found []string
	for _, n := range nodes {
		if strings.Contains(response, n) {
			found = append(found, n)
		}
	}
	return found
}

// TestPrometheusExecute_PerCallTarget pins the bug where a top-level agent
// called prometheus_execute for another cluster and got the conversation's
// own cluster's metrics (conversation e70d5577). It calls the tool directly,
// so the result does not depend on the route a model picks, and checks the
// node names in the series against kubectl on each cluster.
func TestPrometheusExecute_PerCallTarget(t *testing.T) {
	requireDelegateE2EEnv(t)
	accountId := os.Getenv("TEST_ACCOUNT")
	target := delegateE2ETarget("TEST_DELEGATE_K8S_TARGET", "k8s-prod")
	origin := delegateE2ETarget("TEST_DELEGATE_K8S_ORIGIN", "k8s-dev")
	targetNodes, originNodes := clusterNodes(t, target), clusterNodes(t, origin)

	prom, ok := toolcore.GetNBTool(accountId, "prometheus_execute")
	require.True(t, ok)
	call := func(args map[string]any) string {
		ctx := toolcore.NewNbToolContext(tenantWideE2EContext(), prom, accountId, os.Getenv("TEST_USER"), uuid.NewString(), uuid.NewString(), uuid.NewString(), "", nil, "", toolcore.NBQueryConfig{}, "prometheus-per-call-target")
		request := toolcore.NBToolCallRequest{Command: "node_uname_info", Arguments: args}
		// The executor resolves the call's target before a follows-target
		// tool runs; do the same for a direct call.
		ctx, err := tools.ResolveCallTarget(ctx, request)
		require.NoError(t, err)
		resp, err := prom.Call(ctx, request)
		require.NoError(t, err)
		require.NotEqual(t, toolcore.NBToolResponseStatusError, resp.Status, "%v", resp.Data)
		return fmt.Sprintf("%v", resp.Data)
	}

	withTarget := call(map[string]any{"range": "1h", "target": map[string]any{"provider": "kubernetes", "name": target}})
	t.Logf("with target: %d %s nodes, %d %s nodes", len(nodesIn(withTarget, targetNodes)), target, len(nodesIn(withTarget, originNodes)), origin)
	assert.NotEmpty(t, nodesIn(withTarget, targetNodes), "series must come from %s's nodes", target)
	assert.Empty(t, nodesIn(withTarget, originNodes), "no series may come from the conversation's own cluster %s", origin)

	withoutTarget := call(map[string]any{"range": "1h"})
	assert.NotEmpty(t, nodesIn(withoutTarget, originNodes), "without a target the conversation's own cluster is queried")
	assert.Empty(t, nodesIn(withoutTarget, targetNodes))
}

// clusterNamespaces is the test's own ground truth: it runs kubectl_execute
// directly (no LLM) against a named cluster.
func clusterNamespaces(t *testing.T, cluster string) map[string]bool {
	t.Helper()
	accountId := os.Getenv("TEST_ACCOUNT")
	kubectl, ok := toolcore.GetNBTool(accountId, "kubectl_execute")
	require.True(t, ok)
	ctx := toolcore.NewNbToolContext(tenantWideE2EContext(), kubectl, accountId, os.Getenv("TEST_USER"), uuid.NewString(), uuid.NewString(), uuid.NewString(), "", nil, "", toolcore.NBQueryConfig{}, "oracle-"+cluster)
	resp, err := kubectl.Call(ctx, toolcore.NBToolCallRequest{
		Command:   "kubectl get namespaces --no-headers -o custom-columns=:metadata.name",
		Arguments: map[string]any{"cluster": cluster},
	})
	require.NoError(t, err)
	out := toolDataString(resp.Data)
	var envelope struct{ Stdout string }
	if json.Unmarshal([]byte(out), &envelope) == nil && envelope.Stdout != "" {
		out = envelope.Stdout
	}
	names := map[string]bool{}
	for _, line := range strings.Fields(out) {
		names[line] = true
	}
	require.NotEmpty(t, names, "oracle must list namespaces on %s", cluster)
	return names
}

// TestNaturalQuestion_CompareTwoClusters: one question spanning the
// conversation's own cluster and another one. The planner may use per-call
// targets, a delegate, or a script over both contexts; the answer is checked
// against namespaces the test lists itself.
func TestNaturalQuestion_CompareTwoClusters(t *testing.T) {
	requireDelegateE2EEnv(t)
	target := delegateE2ETarget("TEST_DELEGATE_K8S_TARGET", "k8s-prod")
	origin := delegateE2ETarget("TEST_DELEGATE_K8S_ORIGIN", "k8s-dev")
	originNS, targetNS := clusterNamespaces(t, origin), clusterNamespaces(t, target)
	var onlyTarget []string
	for ns := range targetNS {
		if !originNS[ns] {
			onlyTarget = append(onlyTarget, ns)
		}
	}
	require.NotEmpty(t, onlyTarget, "fixture needs at least one namespace unique to %s", target)
	t.Logf("oracle: only in %s = %v", target, onlyTarget)

	query := fmt.Sprintf("Compare the namespaces in %s and %s. Which ones exist only in %s?", origin, target, target)
	resp := runPreservedE2EConversation(t, tenantWideE2EContext(), newK8sOrchestratorAgent(os.Getenv("TEST_ACCOUNT")), "natural-compare-clusters", query)
	logRoute(t, fetchConversationToolCalls(t, resp.ConversationId))

	answer := strings.Join(resp.Response, " ")
	for _, ns := range onlyTarget {
		assert.Contains(t, answer, ns, "answer must name every namespace unique to %s", target)
	}
}

// TestNaturalQuestion_OtherAwsAccount: a question about a named AWS account
// from a Kubernetes conversation; every identity it reports must be that
// account's, never the conversation's.
func TestNaturalQuestion_OtherAwsAccount(t *testing.T) {
	requireDelegateE2EEnv(t)
	target := delegateE2ETarget("TEST_DELEGATE_AWS_TARGET", "aws-prod")
	selected, err := tools.AccountTargetByName(tenantWideE2EContext(), os.Getenv("TEST_ACCOUNT"), tools.ToolExecuteAwsCliCommand, target)
	require.NoError(t, err, "%s must be a connected AWS account in this tenant", target)
	expectedNumber := ""
	for _, v := range selected.Values {
		if v.Name == "account_number" {
			expectedNumber = v.Value
		}
	}

	query := fmt.Sprintf("Which AWS account number is %s, and does it have any load balancers in us-east-1 or us-west-2?", target)
	resp := runPreservedE2EConversation(t, tenantWideE2EContext(), newK8sOrchestratorAgent(os.Getenv("TEST_ACCOUNT")), "natural-other-aws", query)

	calls := fetchConversationToolCalls(t, resp.ConversationId)
	logRoute(t, calls)
	var onTarget int
	for _, c := range calls {
		if !isRemoteCall(c, "aws") {
			continue
		}
		assert.NotContains(t, c.Response, "aws-led command")
		if ranOn(c, target) && c.Status == "success" {
			onTarget++
		}
		if expectedNumber == "" {
			continue
		}
		for _, field := range awsAccountLine.FindAllString(c.Response, -1) {
			assert.Contains(t, field, expectedNumber, "every reported AWS identity must be %s's", target)
		}
	}
	// The planner may answer from the AWS CLI or from the inventory; either is
	// fine as long as it describes the named account, not the conversation's.
	if onTarget == 0 {
		require.NotEmpty(t, expectedNumber, "without an aws call the answer can only be checked by account number")
		answer := conversationAnswer(t, resp.ConversationId)
		assert.Contains(t, answer, expectedNumber, "the answer must give %s's account number", target)
	}
}

// conversationAnswer is the top-level agent's final answer.
func conversationAnswer(t *testing.T, conversationId string) string {
	t.Helper()
	dbm, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	var answer string
	require.NoError(t, dbm.Db.QueryRow(`SELECT COALESCE(response::text, '') FROM llm_conversation_agent
		WHERE conversation_id = $1 AND parent_agent_id = '00000000-0000-0000-0000-000000000000'
		ORDER BY created_at DESC LIMIT 1`, conversationId).Scan(&answer))
	return answer
}

// TestNaturalQuestion_InaccessibleCluster: a user who can reach only their own
// account asks about a cluster outside it; nothing may run there, and nothing
// may be answered from the conversation's own cluster instead.
func TestNaturalQuestion_InaccessibleCluster(t *testing.T) {
	requireDelegateE2EEnv(t)
	target := delegateE2ETarget("TEST_DELEGATE_K8S_TARGET", "k8s-prod")
	accountScoped := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})
	query := fmt.Sprintf("Can you check what's running on %s?", target)
	resp := runPreservedE2EConversation(t, accountScoped, newK8sOrchestratorAgent(os.Getenv("TEST_ACCOUNT")), "natural-inaccessible-cluster", query)

	calls := fetchConversationToolCalls(t, resp.ConversationId)
	logRoute(t, calls)
	for _, c := range calls {
		if isRemoteCall(c, "kubectl") && ranOn(c, target) {
			assert.NotEqual(t, "success", c.Status, "nothing may run on an inaccessible cluster: %s", c.Parameters)
		}
		assert.Empty(t, c.DelegatedTarget, "no child may start for an inaccessible target")
	}
	answer := strings.ToLower(strings.Join(resp.Response, " "))
	t.Logf("answer: %s", answer)
	assert.Contains(t, answer, strings.ToLower(target), "the answer must address the cluster the user asked about")
}

// targetObject strips the structured {"target":{...}} a call carries, so only
// the query itself is inspected.
var targetObject = regexp.MustCompile(`"target":\{[^}]*\}`)

// backendQueryTools write a concrete backend query (LogQL/PromQL/SQL filters).
var backendQueryTools = []string{"fetch_logs_direct", "logs_execute_v2", "prometheus_execute", "traces_execute"}

// filtersOnTargetName reports whether an executed backend query used the
// target's own name as a filter value (e.g. namespace="k8s-prod"). The
// target is already the scope, so its name must never appear as a value.
func filtersOnTargetName(c e2eToolCall, name string) bool {
	params := targetObject.ReplaceAllString(c.Parameters, "")
	for _, text := range []string{params, c.Response} {
		for _, quoted := range []string{`"` + name + `"`, `\"` + name + `\"`} {
			for _, op := range []string{"=", "=~", ":", `"_eq":`, `"_ilike":`, `"_like":`} {
				if strings.Contains(text, op+quoted) {
					return true
				}
			}
		}
	}
	return false
}

// TestNaturalQuestion_OtherClusterObservability asks about logs, metrics and
// traces of another connected cluster, phrased so that only the backend
// (history, trends) can answer; a kubectl fallback does not satisfy it. Depending on what that cluster has
// configured, the answer must come from its own backend, or the tool must
// refuse — never from the conversation's cluster (#38588 Option B).
func TestNaturalQuestion_OtherClusterObservability(t *testing.T) {
	requireDelegateE2EEnv(t)
	target := delegateE2ETarget("TEST_DELEGATE_K8S_TARGET", "k8s-prod")
	resolved, err := tools.ResolveAgentHandoffTarget(tenantWideE2EContext(), os.Getenv("TEST_ACCOUNT"), &toolcore.ExecutionTarget{Provider: "kubernetes", Name: target})
	require.NoError(t, err, "%s must be a connected cluster in this tenant", target)

	for _, tc := range []struct {
		signal   string
		question string
		tools    []string
		provider func(string) (string, error)
	}{
		{"logs", "Any errors in the nudgebee namespace logs on %s over the last 6 hours?", []string{"fetch_logs_direct", "logs_execute_v2"}, func(id string) (string, error) {
			p, err := tools.GetLogProvider(id)
			if p.Provider == "" && tools.HasConnectedK8sAgent(id) {
				return "k8s", err
			}
			return p.Provider, err
		}},
		{"metrics", "How has CPU usage on %s trended over the last 6 hours? Any spikes?", []string{"metrics", "prometheus_execute"}, func(id string) (string, error) {
			p, err := tools.GetMetricsProvider(id)
			return p.Provider, err
		}},
		{"traces", "Which endpoints on %s have been slowest over the last hour?", []string{"traces", "traces_execute", "traces_execute_v2", "traces_execute_clickhouse", "traces_execute_default", "traces_execute_jaeger", "traces_execute_chronosphere"}, func(id string) (string, error) {
			p, err := tools.GetTraceProvider(id)
			return p.Provider, err
		}},
	} {
		t.Run(tc.signal, func(t *testing.T) {
			provider, perr := tc.provider(resolved.AccountID)
			configured := perr == nil && provider != ""
			t.Logf("%s: %s provider on %s = %q (configured=%v, err=%v)", tc.signal, tc.signal, target, provider, configured, perr)

			resp := runPreservedE2EConversation(t, tenantWideE2EContext(), newK8sOrchestratorAgent(os.Getenv("TEST_ACCOUNT")), "natural-"+tc.signal+"-other-cluster", fmt.Sprintf(tc.question, target))
			calls := fetchConversationToolCalls(t, resp.ConversationId)
			logRoute(t, calls)

			var onTargetOK, refused int
			for _, c := range calls {
				if lo.Contains(backendQueryTools, c.ToolName) && filtersOnTargetName(c, target) {
					t.Errorf("%s filtered on the target's own name %q, which is already the scope: %s", c.ToolName, target, c.Parameters)
				}
				if !lo.Contains(tc.tools, c.ToolName) {
					continue
				}
				targeted := ranOn(c, target) || strings.EqualFold(c.DelegatedTarget, target) || strings.EqualFold(c.AgentAccount, target)
				switch {
				case targeted && c.Status == "success":
					onTargetOK++
				case strings.Contains(c.Response, "will not fall back"):
					refused++
				case !targeted && c.Status == "success":
					t.Errorf("%s answered from the conversation's own account, not %s: %s", c.ToolName, target, c.Parameters)
				}
			}
			if configured {
				assert.NotZero(t, onTargetOK, "%s must be queried on %s's own backend", tc.signal, target)
			} else {
				assert.Zero(t, onTargetOK, "%s has no %s backend; nothing may succeed there", target, tc.signal)
				t.Logf("refusals observed: %d", refused)
			}
		})
	}
}

// originOnlyTools answer from the conversation's own account whatever the
// child's target is; inside a child targeted elsewhere they must be refused.
var originOnlyTools = []string{
	"remediation", "remediation_generate", "remediation_execute", "memory", "workflow",
	"recommendation_apply", "recommendation_execute_cli", "recommendation_set_dismissal",
}

// eventTools read the events store; under a target they must read the
// target's account.
var eventTools = []string{"list_events", "aggregate_events", "get_event_by_id", "events_execute", "anomaly_execute", "get_event_evidence"}

var uuidPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// eventAccounts is the ground truth for event tool output: the account each
// event id in the responses belongs to, read from the events table.
func eventAccounts(t *testing.T, calls []e2eToolCall) map[string]string {
	t.Helper()
	var ids []string
	for _, c := range calls {
		if lo.Contains(eventTools, c.ToolName) && c.Status == "success" {
			ids = append(ids, uuidPattern.FindAllString(c.Response, -1)...)
		}
	}
	accounts := map[string]string{}
	if len(ids) == 0 {
		return accounts
	}
	dbm, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	rows, err := dbm.Db.Query(`SELECT id::text, cloud_account_id::text FROM events WHERE id::text = ANY($1)`, pq.Array(lo.Uniq(ids)))
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var id, account string
		require.NoError(t, rows.Scan(&id, &account))
		accounts[id] = account
	}
	require.NoError(t, rows.Err())
	return accounts
}

// recommendationTools read recommendation rows.
var recommendationTools = []string{"recommendation_execute", "recommendation_resolution_execute"}

// recommendationAccounts is the ground truth for recommendation output: the
// account each recommendation id in the responses belongs to.
func recommendationAccounts(t *testing.T, calls []e2eToolCall) map[string]string {
	t.Helper()
	var ids []string
	for _, c := range calls {
		if lo.Contains(recommendationTools, c.ToolName) && c.Status == "success" {
			ids = append(ids, uuidPattern.FindAllString(c.Response, -1)...)
		}
	}
	accounts := map[string]string{}
	if len(ids) == 0 {
		return accounts
	}
	dbm, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	rows, err := dbm.Db.Query(`SELECT id::text, cloud_account_id::text FROM recommendation WHERE id::text = ANY($1)`, pq.Array(lo.Uniq(ids)))
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var id, account string
		require.NoError(t, rows.Scan(&id, &account))
		accounts[id] = account
	}
	require.NoError(t, rows.Err())
	return accounts
}

// TestNaturalQuestion_OtherClusterRecommendations: before the fix, this
// question answered with the conversation's own cluster's recommendations
// labelled as the other cluster's (conversation 78722bae). Every
// recommendation returned must belong to the cluster the user named.
func TestNaturalQuestion_OtherClusterRecommendations(t *testing.T) {
	requireDelegateE2EEnv(t)
	accountId := os.Getenv("TEST_ACCOUNT")
	target := delegateE2ETarget("TEST_DELEGATE_K8S_TARGET", "k8s-prod")
	resolved, err := tools.ResolveAgentHandoffTarget(tenantWideE2EContext(), accountId, &toolcore.ExecutionTarget{Provider: "kubernetes", Name: target})
	require.NoError(t, err)

	resp := runPreservedE2EConversation(t, tenantWideE2EContext(), newK8sOrchestratorAgent(accountId), "natural-other-cluster-recommendations",
		fmt.Sprintf("What are the top cost-saving recommendations for %s?", target))
	calls := fetchConversationToolCalls(t, resp.ConversationId)
	logRoute(t, calls)

	accounts := recommendationAccounts(t, calls)
	t.Logf("recommendation ids in responses: %d", len(accounts))
	for id, account := range accounts {
		assert.Equal(t, resolved.AccountID, account, "recommendation %s belongs to another account than %s", id, target)
	}
	var recommendationCalls int
	for _, c := range calls {
		if lo.Contains(recommendationTools, c.ToolName) && c.Status == "success" {
			recommendationCalls++
			if c.DelegatedTarget == "" && !ranOn(c, target) {
				t.Errorf("%s ran without the target: %s", c.ToolName, c.Parameters)
			}
		}
	}
	require.NotZero(t, recommendationCalls, "recommendations are only in the recommendation store; it must be queried")
}

// TestNaturalQuestion_OtherClusterSecurityAndDependencies asks about another
// cluster's security findings and service dependencies. Whichever agent or
// tool answers must run on that cluster; nothing may answer from the
// conversation's own account.
func TestNaturalQuestion_OtherClusterSecurityAndDependencies(t *testing.T) {
	requireDelegateE2EEnv(t)
	accountId := os.Getenv("TEST_ACCOUNT")
	target := delegateE2ETarget("TEST_DELEGATE_K8S_TARGET", "k8s-prod")
	resolved, err := tools.ResolveAgentHandoffTarget(tenantWideE2EContext(), accountId, &toolcore.ExecutionTarget{Provider: "kubernetes", Name: target})
	require.NoError(t, err)
	for _, tc := range []struct{ name, question string }{
		{"security", "Are there any critical security findings on %s I should look at first?"},
		{"dependencies", "What does app-prod depend on in %s, and what depends on it?"},
		{"agent-health", "Is the Nudgebee agent on %s healthy and connected?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := runPreservedE2EConversation(t, tenantWideE2EContext(), newK8sOrchestratorAgent(accountId), "natural-other-cluster-"+tc.name, fmt.Sprintf(tc.question, target))
			calls := fetchConversationToolCalls(t, resp.ConversationId)
			logRoute(t, calls)
			accountScoped := append([]string{"security", "security_execute", "service_dependency_graph", "service_dependency_graph_execute", "nudgebee_agent_health_get"}, recommendationTools...)
			var onTarget int
			for _, c := range calls {
				if c.DelegatedTarget != "" {
					assert.Equal(t, target, c.DelegatedTarget)
				}
				if c.Status != "success" {
					continue
				}
				targeted := strings.EqualFold(c.DelegatedTarget, target) || ranOn(c, target) || strings.EqualFold(c.AgentAccount, target)
				if lo.Contains(accountScoped, c.ToolName) && !targeted {
					t.Errorf("%s answered without the target, i.e. from the conversation's own account: %s", c.ToolName, c.Parameters)
				}
				if targeted {
					onTarget++
				}
				for _, failure := range knownTargetFailures {
					assert.NotContains(t, c.Response, failure)
				}
			}
			for id, account := range recommendationAccounts(t, calls) {
				assert.Equal(t, resolved.AccountID, account, "recommendation %s belongs to another account than %s", id, target)
			}
			require.NotZero(t, onTarget, "the answer must come from %s", target)
		})
	}
}

// TestNaturalQuestion_OtherClusterAlerts asks about another cluster's alerts,
// which only the events store answers. Every event reported must be one of
// that cluster's events, never the conversation's own.
func TestNaturalQuestion_OtherClusterAlerts(t *testing.T) {
	requireDelegateE2EEnv(t)
	accountId := os.Getenv("TEST_ACCOUNT")
	target := delegateE2ETarget("TEST_DELEGATE_K8S_TARGET", "k8s-prod")
	resolved, err := tools.ResolveAgentHandoffTarget(tenantWideE2EContext(), accountId, &toolcore.ExecutionTarget{Provider: "kubernetes", Name: target})
	require.NoError(t, err)

	resp := runPreservedE2EConversation(t, tenantWideE2EContext(), newK8sOrchestratorAgent(accountId), "natural-other-cluster-alerts",
		fmt.Sprintf("Which alerts fired on %s in the last 24 hours, and which were the noisiest?", target))
	calls := fetchConversationToolCalls(t, resp.ConversationId)
	logRoute(t, calls)

	accounts := eventAccounts(t, calls)
	t.Logf("event ids in event tool responses: %d", len(accounts))
	for id, account := range accounts {
		assert.Equal(t, resolved.AccountID, account, "event %s belongs to another account than %s", id, target)
	}
	var eventCalls int
	for _, c := range calls {
		if lo.Contains(eventTools, c.ToolName) {
			eventCalls++
			if c.DelegatedTarget == "" && !ranOn(c, target) {
				t.Errorf("%s ran without the target: %s", c.ToolName, c.Parameters)
			}
		}
	}
	require.NotZero(t, eventCalls, "alerts are only in the events store; it must be queried")
}

// TestNaturalQuestion_OtherClusterHealth asks a broad health question about
// another cluster, the kind that tempts a planner to reach for the events
// agent and the logs agent. Anything a child targeted at that cluster runs
// must come from that cluster; tools that can only read the conversation's
// own account must be refused there, not answer for it.
func TestNaturalQuestion_OtherClusterHealth(t *testing.T) {
	requireDelegateE2EEnv(t)
	target := delegateE2ETarget("TEST_DELEGATE_K8S_TARGET", "k8s-prod")
	query := fmt.Sprintf("Is anything unhealthy on %s right now? Any warning events or errors in the logs?", target)
	resp := runPreservedE2EConversation(t, tenantWideE2EContext(), newK8sOrchestratorAgent(os.Getenv("TEST_ACCOUNT")), "natural-other-cluster-health", query)

	calls := fetchConversationToolCalls(t, resp.ConversationId)
	logRoute(t, calls)
	var onTarget int
	for _, c := range calls {
		if c.DelegatedTarget != "" {
			assert.Equal(t, target, c.DelegatedTarget, "every targeted child, nested ones included, must carry the cluster the user named")
		}
		if strings.EqualFold(c.DelegatedTarget, target) && lo.Contains(originOnlyTools, c.ToolName) && c.Status == "success" {
			t.Errorf("%s answered inside a child targeted at %s, but it can only read the conversation's own account: %s", c.ToolName, target, c.Parameters)
		}
		if (isRemoteCall(c, "kubectl") && ranOn(c, target) || lo.Contains(backendQueryTools, c.ToolName) && strings.EqualFold(c.DelegatedTarget, target)) && c.Status == "success" {
			onTarget++
		}
		for _, failure := range knownTargetFailures {
			assert.NotContains(t, c.Response, failure)
		}
	}
	require.NotZero(t, onTarget, "the health check must actually look at %s", target)
}

// TestTargetedChild_HealthCheck runs the orchestrator in the state a child
// is in after a handoff: the conversation belongs to TEST_ACCOUNT, the agent
// is aimed at another cluster. The question is natural. Agents it calls
// without naming a target (logs) must inherit the target, and tools that can
// only read the conversation's own account (events) must be refused.
func TestTargetedChild_HealthCheck(t *testing.T) {
	requireDelegateE2EEnv(t)
	accountId := os.Getenv("TEST_ACCOUNT")
	target := delegateE2ETarget("TEST_DELEGATE_K8S_TARGET", "k8s-prod")
	sc := tenantWideE2EContext()
	resolved, err := tools.ResolveAgentHandoffTarget(sc, accountId, &toolcore.ExecutionTarget{Provider: "kubernetes", Name: target})
	require.NoError(t, err)
	require.NotEqual(t, accountId, resolved.AccountID, "the target must be a different account than the conversation's")

	sessionId := fmt.Sprintf("targeted-child-health-%d", time.Now().Unix())
	resp, err := core.HandleConversationSessionRequest(sc, newK8sOrchestratorAgent(accountId), os.Getenv("TEST_USER"), accountId, sessionId,
		"Is anything unhealthy right now? Any warning events or errors in the nudgebee namespace logs?",
		core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{DelegatedTarget: resolved}))
	require.NoError(t, err)
	t.Logf("Preserved sessionId=%s conversationId=%s status=%s", sessionId, resp.ConversationId, resp.Status)

	calls := fetchConversationToolCalls(t, resp.ConversationId)
	var onTarget int
	for _, c := range calls {
		if c.DelegatedTarget != "" {
			assert.Equal(t, target, c.DelegatedTarget, "nested agents must keep the target")
		}
		if lo.Contains(originOnlyTools, c.ToolName) && c.Status == "success" {
			t.Errorf("%s answered for %s from the conversation's own account: %s", c.ToolName, target, c.Parameters)
		}
		if c.AgentName == "logs" && c.DelegatedTarget == "" {
			t.Errorf("the logs agent lost the target: %s %s", c.ToolName, c.Parameters)
		}
		if (isRemoteCall(c, "kubectl") && ranOn(c, target) || lo.Contains(backendQueryTools, c.ToolName) && strings.EqualFold(c.DelegatedTarget, target)) && c.Status == "success" {
			onTarget++
		}
	}
	for id, account := range eventAccounts(t, calls) {
		assert.Equal(t, resolved.AccountID, account, "event %s belongs to another account than %s", id, target)
	}
	require.NotZero(t, onTarget, "the check must actually look at %s", target)
}

// knownTargetFailures are messages this feature must never surface for a
// connection the user named and can access.
var knownTargetFailures = []string{"aws-led command", "changed since handoff", "does not support a delegated execution target", "does not accept an execution target"}

// TestRegression_MultiTargetSession replays a real dev conversation (session
// e9bfb115, 2026-09-29) verbatim, typos included: one session hopping between
// the conversation's cluster, another cluster, an AWS account and a GCP
// project. Each turn is checked against where its commands ran in that
// baseline, not against the wording of the answer.
func TestRegression_MultiTargetSession(t *testing.T) {
	requireDelegateE2EEnv(t)
	k8sTarget := delegateE2ETarget("TEST_DELEGATE_K8S_TARGET", "k8s-prod")
	awsTarget := delegateE2ETarget("TEST_DELEGATE_AWS_TARGET", "aws-prod")
	// GCP connection and project names are environment-specific; keep them out
	// of source (internal-leaks gitleaks rule) and take them from the env.
	gcpTarget, gcpProject := os.Getenv("TEST_DELEGATE_GCP_TARGET"), os.Getenv("TEST_DELEGATE_GCP_PROJECT")
	if gcpTarget == "" || gcpProject == "" {
		t.Skip("set TEST_DELEGATE_GCP_TARGET (connection name) and TEST_DELEGATE_GCP_PROJECT (its project id) to replay the GCP turns")
	}
	accountId, userId := os.Getenv("TEST_ACCOUNT"), os.Getenv("TEST_USER")
	sc := tenantWideE2EContext()

	awsConfig, err := tools.AccountTargetByName(sc, accountId, tools.ToolExecuteAwsCliCommand, awsTarget)
	require.NoError(t, err, "%s must be connected", awsTarget)
	awsNumber := ""
	for _, v := range awsConfig.Values {
		if v.Name == "account_number" {
			awsNumber = v.Value
		}
	}
	prodPods := clusterPods(t, k8sTarget, "nudgebee")

	type turn struct {
		query string
		check func(t *testing.T, answer string, calls []e2eToolCall)
	}
	remoteOn := func(t *testing.T, calls []e2eToolCall, cli, target string) {
		t.Helper()
		for _, c := range calls {
			if isRemoteCall(c, cli) && ranOn(c, target) && c.Status == "success" {
				return
			}
		}
		t.Errorf("no successful %s call ran on %q", cli, target)
	}
	turns := []turn{
		{"can you get me available contexts you have ?", func(t *testing.T, answer string, _ []e2eToolCall) {
			assert.Contains(t, answer, k8sTarget)
		}},
		{"can you check pods running in k8s-prod, nudgebee namesapce ?\n", func(t *testing.T, answer string, calls []e2eToolCall) {
			remoteOn(t, calls, "kubectl", k8sTarget)
			named := lo.Filter(prodPods, func(p string, _ int) bool { return strings.Contains(answer, p) })
			assert.NotEmpty(t, named, "answer must name real %s/nudgebee pods", k8sTarget)
		}},
		{"ok and can you check what all aws accounts you have acecss to ?\n", func(t *testing.T, answer string, _ []e2eToolCall) {
			assert.Contains(t, answer, awsTarget)
		}},
		{"ok, can you check VMs running `aws-prod` ?\nalso review if we have any piublic buckeyt\n", func(t *testing.T, _ string, calls []e2eToolCall) {
			remoteOn(t, calls, "aws", awsTarget)
			if awsNumber == "" {
				return
			}
			for _, c := range calls {
				if !isRemoteCall(c, "aws") {
					continue
				}
				for _, field := range awsAccountLine.FindAllString(c.Response, -1) {
					assert.Contains(t, field, awsNumber, "every AWS identity must be %s's", awsTarget)
				}
			}
		}},
		{"what all google cloud, profiels you have access. ?\n", func(t *testing.T, answer string, _ []e2eToolCall) {
			assert.Contains(t, answer, gcpProject)
		}},
		{"ok, can you chekc VMs runninhg in " + gcpProject + " project ?\n", func(t *testing.T, _ string, calls []e2eToolCall) {
			remoteOn(t, calls, "gcloud", gcpTarget)
		}},
		{"and does it have any Storage buckets available ?", func(t *testing.T, _ string, calls []e2eToolCall) {
			remoteOn(t, calls, "gcloud", gcpTarget)
		}},
	}

	sessionId := fmt.Sprintf("regression-multi-target-%d", time.Now().Unix())
	conversationId := ""
	for i, tc := range turns {
		var opts []core.ConversationSessionRequestConfig
		if conversationId != "" {
			opts = append(opts, core.ConversationSessionRequestWithConversationId(uuid.NullUUID{UUID: uuid.MustParse(conversationId), Valid: true}))
		}
		resp, err := core.HandleConversationSessionRequest(sc, newK8sOrchestratorAgent(accountId), userId, accountId, sessionId, tc.query, opts...)
		require.NoError(t, err, "turn %d", i+1)
		if conversationId == "" {
			conversationId = resp.ConversationId
			t.Logf("Preserved sessionId=%s conversationId=%s", sessionId, conversationId)
		}
		require.Equal(t, conversationId, resp.ConversationId, "all turns must stay in one conversation")

		var turnCalls []e2eToolCall
		for _, c := range fetchConversationToolCalls(t, conversationId) {
			if c.MessageId == resp.MessageId {
				turnCalls = append(turnCalls, c)
			}
		}
		answer := strings.Join(resp.Response, " ")
		t.Run(fmt.Sprintf("turn%d", i+1), func(t *testing.T) {
			t.Logf("status=%s calls=%d query=%q", resp.Status, len(turnCalls), tc.query)
			for _, c := range turnCalls {
				for _, failure := range knownTargetFailures {
					assert.NotContains(t, c.Response, failure, "%s: %s", c.ToolName, c.Parameters)
				}
			}
			tc.check(t, answer, turnCalls)
		})
	}
}

// clusterPods lists pod names in a namespace with a direct kubectl_execute
// call (no LLM), as ground truth for answers about that cluster.
func clusterPods(t *testing.T, cluster, namespace string) []string {
	t.Helper()
	accountId := os.Getenv("TEST_ACCOUNT")
	kubectl, ok := toolcore.GetNBTool(accountId, "kubectl_execute")
	require.True(t, ok)
	ctx := toolcore.NewNbToolContext(tenantWideE2EContext(), kubectl, accountId, os.Getenv("TEST_USER"), uuid.NewString(), uuid.NewString(), uuid.NewString(), "", nil, "", toolcore.NBQueryConfig{}, "oracle-pods-"+cluster)
	resp, err := kubectl.Call(ctx, toolcore.NBToolCallRequest{
		Command:   "kubectl get pods -n " + namespace + " --no-headers -o custom-columns=:metadata.name",
		Arguments: map[string]any{"cluster": cluster},
	})
	require.NoError(t, err)
	out := toolDataString(resp.Data)
	var envelope struct{ Stdout string }
	if json.Unmarshal([]byte(out), &envelope) == nil && envelope.Stdout != "" {
		out = envelope.Stdout
	}
	pods := strings.Fields(out)
	require.NotEmpty(t, pods, "oracle must list pods in %s/%s", cluster, namespace)
	return pods
}
