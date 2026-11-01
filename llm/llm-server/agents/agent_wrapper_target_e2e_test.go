//go:build e2e

package agents

import (
	"encoding/json"
	"os"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stampedCall is one tool call with the "ran on" target it recorded, if any.
type stampedCall struct {
	AgentName, ToolName, Status, Response string
	Target                                *toolcore.ExecutionTarget
}

func fetchStampedCalls(t *testing.T, conversationId string) []stampedCall {
	t.Helper()
	dbm, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	rows, err := dbm.Db.Query(`
		SELECT a.agent_name, t.tool_name, COALESCE(t.status, ''), COALESCE(t.response, ''),
		       COALESCE(t.metadata::jsonb->'nb_executed_target', 'null'::jsonb)::text
		FROM llm_conversation_tool_calls t
		JOIN llm_conversation_agent a ON a.id = t.agent_id
		WHERE t.conversation_id = $1
		ORDER BY t.created_at`, conversationId)
	require.NoError(t, err)
	defer rows.Close()
	var calls []stampedCall
	for rows.Next() {
		var c stampedCall
		var target string
		require.NoError(t, rows.Scan(&c.AgentName, &c.ToolName, &c.Status, &c.Response, &target))
		require.NoError(t, json.Unmarshal([]byte(target), &c.Target))
		name := ""
		if c.Target != nil {
			name = c.Target.Name
		}
		t.Logf("call: agent=%s tool=%s status=%s ran_on=%q", c.AgentName, c.ToolName, c.Status, name)
		calls = append(calls, c)
	}
	require.NoError(t, rows.Err())
	return calls
}

func requireNoTargetFailures(t *testing.T, calls []stampedCall) {
	t.Helper()
	for _, c := range calls {
		for _, msg := range knownTargetFailures {
			assert.NotContains(t, c.Response, msg, "%s refused the target", c.ToolName)
		}
	}
}

// TestWrapperAgent_PrometheusFollowsHandedTarget: the Prometheus agent, handed
// k8s-prod from a k8s-dev conversation, must read k8s-prod's metrics and label
// each step with where it ran. Before #39779 the agent refused a target.
func TestWrapperAgent_PrometheusFollowsHandedTarget(t *testing.T) {
	requireDelegateE2EEnv(t)
	accountId := os.Getenv("TEST_ACCOUNT")
	target := delegateE2ETarget("TEST_DELEGATE_K8S_TARGET", "k8s-prod")
	origin := delegateE2ETarget("TEST_DELEGATE_K8S_ORIGIN", "k8s-dev")
	targetNodes, originNodes := clusterNodes(t, target), clusterNodes(t, origin)
	resolved, err := tools.ResolveAgentHandoffTarget(tenantWideE2EContext(), accountId, &toolcore.ExecutionTarget{Provider: "kubernetes", Name: target})
	require.NoError(t, err)

	resp, err := core.HandleConversationSessionRequest(tenantWideE2EContext(), newPrometheusAgent(accountId), os.Getenv("TEST_USER"), accountId,
		"wrapper-prometheus-target-"+uuidSuffix(), "Which nodes have the highest CPU usage right now?",
		core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{DelegatedTarget: resolved}))
	require.NoError(t, err)
	t.Logf("Preserved conversationId=%s status=%s", resp.ConversationId, resp.Status)

	calls := fetchStampedCalls(t, resp.ConversationId)
	requireNoTargetFailures(t, calls)
	var onTarget int
	for _, c := range calls {
		if c.ToolName != "prometheus_execute" || c.Status != "success" {
			continue
		}
		assert.Empty(t, nodesIn(c.Response, originNodes), "a Prometheus step returned %s's nodes", origin)
		if len(nodesIn(c.Response, targetNodes)) > 0 {
			onTarget++
		}
		if assert.NotNil(t, c.Target, "a Prometheus step on %s must record where it ran", target) {
			assert.Equal(t, target, c.Target.Name)
			assert.Equal(t, resolved.AccountID, c.Target.AccountID)
		}
	}
	assert.NotZero(t, onTarget, "the agent must return %s's nodes", target)
	answer := conversationAnswer(t, resp.ConversationId)
	assert.NotEmpty(t, nodesIn(answer, targetNodes), "the answer must name %s's nodes", target)
	assert.Empty(t, nodesIn(answer, originNodes), "the answer must not name %s's nodes", origin)
}

// TestWrapperAgent_PromqlFollowsHandedTarget: the PromQL agent only writes
// queries; handed k8s-prod, it must run, and any lookup it makes must be on
// k8s-prod. Before #39779 it refused a target.
func TestWrapperAgent_PromqlFollowsHandedTarget(t *testing.T) {
	requireDelegateE2EEnv(t)
	accountId := os.Getenv("TEST_ACCOUNT")
	target := delegateE2ETarget("TEST_DELEGATE_K8S_TARGET", "k8s-prod")
	resolved, err := tools.ResolveAgentHandoffTarget(tenantWideE2EContext(), accountId, &toolcore.ExecutionTarget{Provider: "kubernetes", Name: target})
	require.NoError(t, err)

	resp, err := core.HandleConversationSessionRequest(tenantWideE2EContext(), &PromqlAgent{accountId: accountId}, os.Getenv("TEST_USER"), accountId,
		"wrapper-promql-target-"+uuidSuffix(), "Write a query for memory usage per pod in the nudgebee namespace",
		core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{DelegatedTarget: resolved}))
	require.NoError(t, err)
	t.Logf("Preserved conversationId=%s status=%s", resp.ConversationId, resp.Status)

	calls := fetchStampedCalls(t, resp.ConversationId)
	requireNoTargetFailures(t, calls)
	for _, c := range calls {
		if c.Target != nil {
			assert.Equal(t, target, c.Target.Name)
		}
	}
	answer := conversationAnswer(t, resp.ConversationId)
	assert.Contains(t, answer, "promql", "the agent must return a query")
}

// TestWrapperAgent_RanOnFromOrchestrator is the user's view: from a k8s-dev
// conversation, a question about k8s-prod's metrics is labelled k8s-prod on
// every step that ran there, and a question that names no other cluster is
// labelled nowhere.
func TestWrapperAgent_RanOnFromOrchestrator(t *testing.T) {
	requireDelegateE2EEnv(t)
	accountId := os.Getenv("TEST_ACCOUNT")
	target := delegateE2ETarget("TEST_DELEGATE_K8S_TARGET", "k8s-prod")
	origin := delegateE2ETarget("TEST_DELEGATE_K8S_ORIGIN", "k8s-dev")
	targetNodes, originNodes := clusterNodes(t, target), clusterNodes(t, origin)

	t.Run("other cluster", func(t *testing.T) {
		resp := runPreservedE2EConversation(t, tenantWideE2EContext(), newK8sOrchestratorAgent(accountId), "wrapper-ranon-other",
			"Which nodes on "+target+" have the highest CPU usage right now?")
		calls := fetchStampedCalls(t, resp.ConversationId)
		requireNoTargetFailures(t, calls)
		var stamped int
		for _, c := range calls {
			if c.Target == nil {
				continue
			}
			stamped++
			assert.Equal(t, target, c.Target.Name, "%s ran somewhere other than %s", c.ToolName, target)
		}
		assert.NotZero(t, stamped, "steps on %s must say where they ran", target)
		answer := conversationAnswer(t, resp.ConversationId)
		assert.NotEmpty(t, nodesIn(answer, targetNodes), "the answer must name %s's nodes", target)
		assert.Empty(t, nodesIn(answer, originNodes), "the answer must not name %s's nodes", origin)
	})

	// A metrics trend goes to an agent called as a tool (metrics, prometheus or
	// a delegate) with the target in its input; that step is labelled too.
	t.Run("metrics agent as a tool", func(t *testing.T) {
		resp := runPreservedE2EConversation(t, tenantWideE2EContext(), newK8sOrchestratorAgent(accountId), "wrapper-ranon-agent-tool",
			"How has node CPU on "+target+" trended over the last hour, from its Prometheus metrics?")
		calls := fetchStampedCalls(t, resp.ConversationId)
		requireNoTargetFailures(t, calls)
		var agentSteps int
		for _, c := range calls {
			if c.Target != nil {
				assert.Equal(t, target, c.Target.Name, "%s ran somewhere other than %s", c.ToolName, target)
			}
			if lo.Contains([]string{"metrics", "prometheus", "promql_query", "delegate_agent"}, c.ToolName) && c.Status == "success" {
				agentSteps++
				assert.NotNil(t, c.Target, "the %s step ran on %s and must say so", c.ToolName, target)
			}
			if c.ToolName == "prometheus_execute" && c.Status == "success" {
				assert.Empty(t, nodesIn(c.Response, originNodes), "a Prometheus step returned %s's nodes", origin)
			}
		}
		require.NotZero(t, agentSteps, "the question must reach a metrics agent called as a tool")
	})

	t.Run("own cluster", func(t *testing.T) {
		resp := runPreservedE2EConversation(t, tenantWideE2EContext(), newK8sOrchestratorAgent(accountId), "wrapper-ranon-own",
			"Which nodes have the highest CPU usage right now?")
		for _, c := range fetchStampedCalls(t, resp.ConversationId) {
			assert.Nil(t, c.Target, "%s ran on the conversation's own cluster; it must not be labelled", c.ToolName)
		}
		answer := conversationAnswer(t, resp.ConversationId)
		assert.Empty(t, nodesIn(answer, targetNodes), "the answer must not name %s's nodes", target)
	})
}

func uuidSuffix() string {
	return uuid.NewString()[:8]
}
