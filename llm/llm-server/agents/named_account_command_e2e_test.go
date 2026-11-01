//go:build e2e

package agents

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNaturalQuestion_CommandInNamedAwsAccount (#39826): from a k8s cluster
// conversation, which has no AWS config of its own, a command in a named AWS
// account must run there instead of being refused as unconfigured.
func TestNaturalQuestion_CommandInNamedAwsAccount(t *testing.T) {
	requireDelegateE2EEnv(t)
	target := delegateE2ETarget("TEST_DELEGATE_AWS_TARGET", "aws-prod")
	selected, err := tools.AccountTargetByName(tenantWideE2EContext(), os.Getenv("TEST_ACCOUNT"), tools.ToolExecuteAwsCliCommand, target)
	require.NoError(t, err, "%s must be a connected AWS account in this tenant", target)
	expected := ""
	for _, v := range selected.Values {
		if v.Name == "account_number" {
			expected = v.Value
		}
	}
	require.NotEmpty(t, expected)

	resp := runPreservedE2EConversation(t, tenantWideE2EContext(), newK8sOrchestratorAgent(os.Getenv("TEST_ACCOUNT")), "named-aws-command",
		"Run aws sts get-caller-identity in the "+target+" AWS account and tell me its account number.")
	calls := fetchConversationToolCalls(t, resp.ConversationId)
	logRoute(t, calls)
	var ranThere int
	for _, c := range calls {
		assert.NotContains(t, c.Response, "requires configuration", "%s was refused as unconfigured: %s", c.ToolName, c.Parameters)
		if isRemoteCall(c, "aws") && ranOn(c, target) && c.Status == "success" {
			ranThere++
		}
	}
	assert.NotZero(t, ranThere, "an aws command must run in %s", target)
	assert.Contains(t, conversationAnswer(t, resp.ConversationId), expected, "the answer must give %s's account number", target)
}

// TestNaturalQuestion_VMsInAnotherAccount (#39894): from a k8s cluster
// conversation, a question about the VMs of a connected VM account must reach
// the VM tools on that account, not the conversation's own cluster.
func TestNaturalQuestion_VMsInAnotherAccount(t *testing.T) {
	requireDelegateE2EEnv(t)
	target := delegateE2ETarget("TEST_DELEGATE_VM_TARGET", "vulnerable-vms-test")
	resp := runPreservedE2EConversation(t, tenantWideE2EContext(), newK8sOrchestratorAgent(os.Getenv("TEST_ACCOUNT")), "natural-vms-other-account",
		"List the VMs in the "+target+" VM account and run uptime on one of them.")
	calls := fetchConversationToolCalls(t, resp.ConversationId)
	logRoute(t, calls)
	var onTarget int
	for _, c := range calls {
		if (c.ToolName == tools.ToolVMList || c.ToolName == tools.ToolVMExecute) && c.Status == "success" {
			onTarget++
		}
	}
	assert.NotZero(t, onTarget, "a VM tool must run on %s", target)
	assert.Contains(t, conversationAnswer(t, resp.ConversationId), "load average", "the answer must include uptime from a VM in %s", target)
}

// TestNaturalQuestion_PickTargetIntegration (#39906): asked about "the postgres
// database" on an account with several, Nubi asks the user which one, as it
// does for the conversation's own account, and runs the query on the pick.
func TestNaturalQuestion_PickTargetIntegration(t *testing.T) {
	requireDelegateE2EEnv(t)
	sc := tenantWideE2EContext()
	accountId, userId := os.Getenv("TEST_ACCOUNT"), os.Getenv("TEST_USER")
	sessionId := fmt.Sprintf("pick-target-integration-%d", time.Now().Unix())
	resp1, err := core.HandleConversationSessionRequest(sc, newK8sOrchestratorAgent(accountId), userId, accountId, sessionId,
		"On k8s-prod, run select current_database(), inet_server_addr() on postgres database.")
	require.NoError(t, err)
	t.Logf("Preserved sessionId=%s conversationId=%s status=%s question=%q options=%v", sessionId, resp1.ConversationId, resp1.Status, resp1.FollowupRequest.Question, resp1.FollowupRequest.FollowupOptions)
	require.Equal(t, core.ConversationStatusWaiting, resp1.Status, "Nubi must ask which k8s-prod database, not guess")
	assert.ElementsMatch(t, []string{"prod", "prod-pg"}, resp1.FollowupRequest.FollowupOptions, "the choices are k8s-prod's integrations")

	waiting := resp1.FollowupRequest.AgentId
	resolvedAgent, _, _, err := core.ResolveAgentByConversationAgentId(sc, waiting, accountId)
	require.NoError(t, err)
	_, err = core.HandleConversationSessionRequest(sc, resolvedAgent, userId, accountId, sessionId, "prod",
		core.ConversationSessionRequestWithConversationId(uuid.NullUUID{UUID: uuid.MustParse(resp1.ConversationId), Valid: true}),
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: uuid.MustParse(resp1.MessageId), Valid: true}),
		core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: waiting, Valid: true}),
		core.ConversationSessionRequestWithIsResume(true),
	)
	require.NoError(t, err)
	final := waitForTerminalMessageStatus(t, accountId, resp1.ConversationId, resp1.MessageId, 240*time.Second)
	t.Logf("final status after picking prod: %s", final)

	calls := fetchConversationToolCalls(t, resp1.ConversationId)
	logRoute(t, calls)
	var onPick int
	for _, c := range calls {
		if c.ToolName == tools.ToolExecutePostgresQuery && c.Status == "success" && strings.Contains(c.Response, "172.31.4.191") {
			onPick++
		}
	}
	assert.NotZero(t, onPick, "the query must run on k8s-prod's prod database after the pick")
}

// TestNaturalQuestion_StepsSayWhereTheyRan (#38588): metrics and logs about
// another cluster run in sub-agents and tools under that cluster's account.
// Every such step, succeeded or failed, says which account it ran on unless it
// named the conversation's own cluster, and the integration it names is one it
// ran with.
func TestNaturalQuestion_StepsSayWhereTheyRan(t *testing.T) {
	requireDelegateE2EEnv(t)
	target := delegateE2ETarget("TEST_DELEGATE_K8S_TARGET", "k8s-prod")
	resp := runPreservedE2EConversation(t, tenantWideE2EContext(), newK8sOrchestratorAgent(os.Getenv("TEST_ACCOUNT")), "steps-say-where-they-ran",
		"On "+target+", how much CPU did the pods in kube-system use over the last hour, and were there any error logs from them?")

	dbm, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	rows, err := dbm.Db.Query(`
		SELECT t.tool_name, COALESCE(t.status, ''),
		       -- Run under the target's account, and not aimed back at the conversation's own cluster.
		       t.account_id::text <> c.account_id::text
		         AND position(lower(own.account_name) in lower(COALESCE(NULLIF(t.params_sql, ''), t.parameters, ''))) = 0,
		       COALESCE(t.metadata->'nb_executed_target'->>'name', ''), COALESCE(t.metadata->>'nb_resolved_target', '')
		FROM llm_conversation_tool_calls t JOIN llm_conversations c ON c.id = t.conversation_id
		JOIN cloud_accounts own ON own.id::text = c.account_id::text
		WHERE t.conversation_id = $1 AND COALESCE(t.status, '') NOT IN ('in_progress', 'waiting')`, resp.ConversationId)
	require.NoError(t, err)
	defer rows.Close()
	var elsewhere int
	for rows.Next() {
		var tool, status, ranOn, integration string
		var underOther bool
		require.NoError(t, rows.Scan(&tool, &status, &underOther, &ranOn, &integration))
		t.Logf("%s status=%s under_other_account=%v ran_on=%q integration=%q", tool, status, underOther, ranOn, integration)
		assert.NotContains(t, []string{"NoConfigToolsFound", "Agent"}, integration, "%s names a placeholder as its integration", tool)
		if underOther {
			elsewhere++
			assert.Equal(t, target, ranOn, "%s ran under %s's account but does not say so", tool, target)
		}
	}
	require.NoError(t, rows.Err())
	assert.NotZero(t, elsewhere, "metrics or logs for %s run under its account", target)
}

// TestNaturalQuestion_WriteOnAnotherClusterIsApprovedThere (#38588): from a
// k8s-dev conversation, a write on k8s-prod asks for approval naming
// k8s-prod, runs there after "yes", and leaves k8s-dev untouched. The
// configmap is a throwaway the test deletes.
func TestNaturalQuestion_WriteOnAnotherClusterIsApprovedThere(t *testing.T) {
	requireDelegateE2EEnv(t)
	sc := tenantWideE2EContext()
	accountId, userId := os.Getenv("TEST_ACCOUNT"), os.Getenv("TEST_USER")
	target := delegateE2ETarget("TEST_DELEGATE_K8S_TARGET", "k8s-prod")
	origin := delegateE2ETarget("TEST_DELEGATE_K8S_ORIGIN", "k8s-dev")
	name := fmt.Sprintf("nb-e2e-write-%d", time.Now().Unix())
	kubectl := func(cluster, command string) string {
		tool, ok := toolcore.GetNBTool(accountId, tools.ToolExecuteKubectlCommand)
		require.True(t, ok)
		ctx := toolcore.NewNbToolContext(sc, tool, accountId, userId, uuid.NewString(), uuid.NewString(), uuid.NewString(), "", nil, "", toolcore.NBQueryConfig{}, "oracle-"+cluster)
		resp, err := tool.Call(ctx, toolcore.NBToolCallRequest{Command: command, Arguments: map[string]any{"cluster": cluster}})
		require.NoError(t, err)
		return fmt.Sprint(resp.Data)
	}
	t.Cleanup(func() { kubectl(target, "kubectl delete configmap "+name+" -n default --ignore-not-found") })

	sessionId := fmt.Sprintf("write-other-cluster-%d", time.Now().Unix())
	resp1, err := core.HandleConversationSessionRequest(sc, newK8sOrchestratorAgent(accountId), userId, accountId, sessionId,
		fmt.Sprintf("On %s, create a configmap named %s in the default namespace with key owner=nudgebee-e2e.", target, name))
	require.NoError(t, err)
	t.Logf("Preserved sessionId=%s conversationId=%s status=%s question=%q", sessionId, resp1.ConversationId, resp1.Status, resp1.FollowupRequest.Question)
	require.Equal(t, core.ConversationStatusWaiting, resp1.Status, "a write must wait for approval")
	assert.Contains(t, resp1.FollowupRequest.Question, target, "the approval card must name the cluster the write lands on")

	waiting := resp1.FollowupRequest.AgentId
	resolvedAgent, _, _, err := core.ResolveAgentByConversationAgentId(sc, waiting, accountId)
	require.NoError(t, err)
	_, err = core.HandleConversationSessionRequest(sc, resolvedAgent, userId, accountId, sessionId, "yes",
		core.ConversationSessionRequestWithConversationId(uuid.NullUUID{UUID: uuid.MustParse(resp1.ConversationId), Valid: true}),
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: uuid.MustParse(resp1.MessageId), Valid: true}),
		core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: waiting, Valid: true}),
		core.ConversationSessionRequestWithIsResume(true),
	)
	require.NoError(t, err)
	final := waitForTerminalMessageStatus(t, accountId, resp1.ConversationId, resp1.MessageId, 240*time.Second)
	t.Logf("final status after approving: %s", final)

	assert.Contains(t, kubectl(target, "kubectl get configmap "+name+" -n default -o name"), "configmap/"+name, "the write must run on %s", target)
	assert.NotContains(t, kubectl(origin, "kubectl get configmap "+name+" -n default -o name --ignore-not-found"), "configmap/"+name, "%s must be untouched", origin)
}

// TestNaturalQuestion_WriteApprovalVariations (#38588): writes on another
// account from a k8s-dev conversation, approved for that account. Every
// object written is a throwaway the test removes.
func TestNaturalQuestion_WriteApprovalVariations(t *testing.T) {
	requireDelegateE2EEnv(t)
	sc := tenantWideE2EContext()
	accountId, userId := os.Getenv("TEST_ACCOUNT"), os.Getenv("TEST_USER")
	prod := delegateE2ETarget("TEST_DELEGATE_K8S_TARGET", "k8s-prod")
	dev := delegateE2ETarget("TEST_DELEGATE_K8S_ORIGIN", "k8s-dev")
	stamp := time.Now().Unix()

	kubectl := func(cluster, command string) string {
		tool, ok := toolcore.GetNBTool(accountId, tools.ToolExecuteKubectlCommand)
		require.True(t, ok)
		ctx := toolcore.NewNbToolContext(sc, tool, accountId, userId, uuid.NewString(), uuid.NewString(), uuid.NewString(), "", nil, "", toolcore.NBQueryConfig{}, "oracle-"+cluster)
		resp, err := tool.Call(ctx, toolcore.NBToolCallRequest{Command: command, Arguments: map[string]any{"cluster": cluster}})
		require.NoError(t, err)
		return fmt.Sprint(resp.Data)
	}
	configmapOn := func(cluster, name string) bool {
		return strings.Contains(kubectl(cluster, "kubectl get configmap "+name+" -n default -o name --ignore-not-found"), "configmap/"+name)
	}
	cleanupConfigmap := func(cluster, name string) {
		t.Cleanup(func() { kubectl(cluster, "kubectl delete configmap "+name+" -n default --ignore-not-found") })
	}

	type turn struct {
		sessionId, conversationId string
		resp                      core.NBAgentResponse
	}
	ask := func(t *testing.T, tr *turn, query string) {
		t.Helper()
		var opts []core.ConversationSessionRequestConfig
		if tr.conversationId != "" {
			opts = append(opts, core.ConversationSessionRequestWithConversationId(uuid.NullUUID{UUID: uuid.MustParse(tr.conversationId), Valid: true}))
		}
		resp, err := core.HandleConversationSessionRequest(sc, newK8sOrchestratorAgent(accountId), userId, accountId, tr.sessionId, query, opts...)
		require.NoError(t, err)
		if tr.conversationId == "" {
			tr.conversationId = resp.ConversationId
			t.Logf("Preserved sessionId=%s conversationId=%s", tr.sessionId, tr.conversationId)
		}
		tr.resp = resp
		t.Logf("status=%s question=%q target=%+v crossAccount=%v", resp.Status, resp.FollowupRequest.Question, resp.FollowupRequest.Target, resp.FollowupRequest.CrossAccount)
	}
	// pending is the question the conversation is waiting on: its stored config
	// and the agent that asked it.
	pending := func(t *testing.T, tr *turn) (map[string]any, uuid.UUID) {
		t.Helper()
		dbm, err := common.GetDatabaseManager(common.Metastore)
		require.NoError(t, err)
		var raw, agentId string
		require.NoError(t, dbm.Db.QueryRow(`SELECT COALESCE(message_config, '{}'), parent_agent_id::text FROM llm_conversation_messages
			WHERE conversation_id = $1 AND message_type = 'followup' AND status = 'WAITING' ORDER BY created_at DESC LIMIT 1`, tr.conversationId).Scan(&raw, &agentId))
		cfg := map[string]any{}
		require.NoError(t, json.Unmarshal([]byte(raw), &cfg))
		t.Logf("waiting on %v: %v (target %v)", cfg["followupType"], cfg["question"], cfg["target"])
		return cfg, uuid.MustParse(agentId)
	}
	answer := func(t *testing.T, tr *turn, waiting uuid.UUID, text string) {
		t.Helper()
		agent, _, _, err := core.ResolveAgentByConversationAgentId(sc, waiting, accountId)
		require.NoError(t, err)
		_, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, tr.sessionId, text,
			core.ConversationSessionRequestWithConversationId(uuid.NullUUID{UUID: uuid.MustParse(tr.conversationId), Valid: true}),
			core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: uuid.MustParse(tr.resp.MessageId), Valid: true}),
			core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: waiting, Valid: true}),
			core.ConversationSessionRequestWithIsResume(true),
		)
		require.NoError(t, err)
		t.Logf("after %q: %s", text, waitForTerminalMessageStatus(t, accountId, tr.conversationId, tr.resp.MessageId, 240*time.Second))
	}
	approve := func(t *testing.T, tr *turn) {
		t.Helper()
		waiting := tr.resp.FollowupRequest.AgentId
		agent, _, _, err := core.ResolveAgentByConversationAgentId(sc, waiting, accountId)
		require.NoError(t, err)
		_, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, tr.sessionId, "yes",
			core.ConversationSessionRequestWithConversationId(uuid.NullUUID{UUID: uuid.MustParse(tr.conversationId), Valid: true}),
			core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: uuid.MustParse(tr.resp.MessageId), Valid: true}),
			core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: waiting, Valid: true}),
			core.ConversationSessionRequestWithIsResume(true),
		)
		require.NoError(t, err)
		t.Logf("after yes: %s", waitForTerminalMessageStatus(t, accountId, tr.conversationId, tr.resp.MessageId, 240*time.Second))
	}
	requireApprovalFor := func(t *testing.T, tr *turn, name string, crossAccount bool) {
		t.Helper()
		require.Equal(t, core.ConversationStatusWaiting, tr.resp.Status, "a write must wait for approval")
		require.NotNil(t, tr.resp.FollowupRequest.Target, "the approval must say where the write lands")
		assert.Equal(t, name, tr.resp.FollowupRequest.Target.Name)
		assert.Equal(t, crossAccount, tr.resp.FollowupRequest.CrossAccount)
		assert.Contains(t, tr.resp.FollowupRequest.Question, "**"+name+"**")
	}

	t.Run("approval is per account", func(t *testing.T) {
		a, b, c := fmt.Sprintf("nb-e2e-a-%d", stamp), fmt.Sprintf("nb-e2e-b-%d", stamp), fmt.Sprintf("nb-e2e-c-%d", stamp)
		cleanupConfigmap(prod, a)
		cleanupConfigmap(dev, b)
		cleanupConfigmap(prod, c)
		tr := &turn{sessionId: fmt.Sprintf("write-approval-per-account-%d", stamp)}

		ask(t, tr, fmt.Sprintf("On %s, create a configmap named %s in the default namespace with key owner=nudgebee-e2e.", prod, a))
		requireApprovalFor(t, tr, prod, true)
		approve(t, tr)
		assert.True(t, configmapOn(prod, a), "%s on %s", a, prod)

		ask(t, tr, fmt.Sprintf("Now create a configmap named %s in the default namespace on %s with the same key.", b, dev))
		requireApprovalFor(t, tr, dev, false) // the k8s-prod approval does not cover k8s-dev
		approve(t, tr)
		assert.True(t, configmapOn(dev, b), "%s on %s", b, dev)
		assert.False(t, configmapOn(prod, b), "%s must not land on %s", b, prod)

		ask(t, tr, fmt.Sprintf("Also create a configmap named %s in the default namespace on %s with the same key.", c, prod))
		if tr.resp.Status == core.ConversationStatusWaiting {
			t.Logf("a later turn asked again for %s (approvals did not carry across turns)", prod)
			requireApprovalFor(t, tr, prod, true)
			approve(t, tr)
		}
		assert.True(t, configmapOn(prod, c), "%s on %s", c, prod)
	})

	t.Run("postgres on another account", func(t *testing.T) {
		tr := &turn{sessionId: fmt.Sprintf("write-approval-postgres-%d", stamp)}
		ask(t, tr, fmt.Sprintf("On %s, in its prod postgres database, run exactly: create temporary table nb_e2e_tmp_%d (id int)", prod, stamp))
		require.Equal(t, core.ConversationStatusWaiting, tr.resp.Status)
		cfg, waiting := pending(t, tr)
		if cfg["followupType"] == string(core.FollowupTypeToolConfig) {
			// k8s-prod has two Postgres integrations: the database is asked
			// for first, so the approval that follows can name it.
			answer(t, tr, waiting, "prod")
			cfg, waiting = pending(t, tr)
		}
		require.Equal(t, string(core.FollowupTypeToolConfirmation), cfg["followupType"], "a write must wait for approval")
		target, _ := cfg["target"].(map[string]any)
		assert.Equal(t, prod, target["name"])
		assert.Equal(t, "prod", target["integration"], "the card names the database")
		assert.Equal(t, true, cfg["crossAccount"])
		assert.Contains(t, cfg["question"], "**"+prod+"** · prod")
		answer(t, tr, waiting, "yes")
	})

	t.Run("delegated sub-agent", func(t *testing.T) {
		d := fmt.Sprintf("nb-e2e-d-%d", stamp)
		cleanupConfigmap(prod, d)
		tr := &turn{sessionId: fmt.Sprintf("write-approval-delegated-%d", stamp)}
		ask(t, tr, fmt.Sprintf("Use delegate_agent to hand this to a sub-agent: on %s, create a configmap named %s in the default namespace with key owner=nudgebee-e2e.", prod, d))
		requireApprovalFor(t, tr, prod, true)
		approve(t, tr)
		assert.True(t, configmapOn(prod, d), "%s on %s", d, prod)
	})

	t.Run("vm account", func(t *testing.T) {
		vms := delegateE2ETarget("TEST_DELEGATE_VM_TARGET", "vulnerable-vms-test")
		file := fmt.Sprintf("/tmp/nb-e2e-%d", stamp)
		tr := &turn{sessionId: fmt.Sprintf("write-approval-vm-%d", stamp)}
		ask(t, tr, fmt.Sprintf("In the %s VM account, run `touch %s` on one of its VMs, then `rm -f %s`.", vms, file, file))
		requireApprovalFor(t, tr, vms, true)
		approve(t, tr)
	})
}

// TestNaturalQuestion_SwitchOwnDatabaseConnection (#38596): in one
// conversation, two of the conversation's own connections of one kind are
// queried by name; each step runs on the one it names, with no question
// about which connection to use. The connection a step ran with is recorded
// even when the query fails, so an unreachable dev database still shows
// whether the switch happened.
func TestNaturalQuestion_SwitchOwnDatabaseConnection(t *testing.T) {
	requireDelegateE2EEnv(t)
	sc := tenantWideE2EContext()
	accountId, userId := os.Getenv("TEST_ACCOUNT"), os.Getenv("TEST_USER")
	for _, tc := range []struct{ kind, tool, first, second, query string }{
		{"postgres", tools.ToolExecutePostgresQuery, envOr("TEST_OWN_PG_FIRST", "dev-pg"), envOr("TEST_OWN_PG_SECOND", "test-pg"), "select current_database()"},
		{"mysql", "mysql_query_execute", envOr("TEST_OWN_MYSQL_FIRST", "mysql-1"), envOr("TEST_OWN_MYSQL_SECOND", "mysql-2"), "select database()"},
		{"mssql", "mssql_query_execute", envOr("TEST_OWN_MSSQL_FIRST", "mssql-vk"), envOr("TEST_OWN_MSSQL_SECOND", "mssql-vk2"), "select db_name()"},
		{"oracle", "oracle_query_execute", envOr("TEST_OWN_ORACLE_FIRST", "oracle-1"), envOr("TEST_OWN_ORACLE_SECOND", "oracle-2"), "select sys_context('userenv','db_name') from dual"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			sessionId := fmt.Sprintf("switch-own-%s-%d", tc.kind, time.Now().Unix())
			conversationId := ""
			for _, name := range []string{tc.first, tc.second} {
				var opts []core.ConversationSessionRequestConfig
				if conversationId != "" {
					opts = append(opts, core.ConversationSessionRequestWithConversationId(uuid.NullUUID{UUID: uuid.MustParse(conversationId), Valid: true}))
				}
				resp, err := core.HandleConversationSessionRequest(sc, newK8sOrchestratorAgent(accountId), userId, accountId, sessionId,
					fmt.Sprintf("On the %s %s connection, run %s and tell me the result.", name, tc.kind, tc.query), opts...)
				require.NoError(t, err)
				if conversationId == "" {
					conversationId = resp.ConversationId
					t.Logf("Preserved sessionId=%s conversationId=%s", sessionId, conversationId)
				}
				assert.NotEqual(t, core.ConversationStatusWaiting, resp.Status, "naming the connection must not ask which one: %q", resp.FollowupRequest.Question)
			}

			dbm, err := common.GetDatabaseManager(common.Metastore)
			require.NoError(t, err)
			rows, err := dbm.Db.Query(`SELECT COALESCE(t.metadata->>'nb_resolved_target', ''), COALESCE(t.status, ''), COALESCE(NULLIF(t.params_sql, ''), t.parameters, ''), LEFT(COALESCE(t.response, ''), 160)
				FROM llm_conversation_tool_calls t WHERE t.conversation_id = $1 AND t.tool_name = $2 ORDER BY t.created_at`, conversationId, tc.tool)
			require.NoError(t, err)
			defer rows.Close()
			ran := map[string]bool{}
			for rows.Next() {
				var integration, status, params, response string
				require.NoError(t, rows.Scan(&integration, &status, &params, &response))
				t.Logf("%s on %q status=%s params=%.160s response=%s", tc.tool, integration, status, params, response)
				ran[integration] = true
			}
			require.NoError(t, rows.Err())
			assert.True(t, ran[tc.first], "a %s call must run on %s", tc.tool, tc.first)
			assert.True(t, ran[tc.second], "a %s call must run on %s in the same conversation", tc.tool, tc.second)
		})
	}
}
