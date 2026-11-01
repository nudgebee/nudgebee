//go:build e2e

package agents

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Scenarios for the cross-account work (#38588, #38596) beyond the
// per-feature tests: the conversation's own account is unchanged, unattended
// runs neither wait nor answer from the wrong account, an ambiguous account
// name is not guessed silently, and writes on two accounts in one turn are
// each approved for their own account. Every object written is a throwaway
// the test removes.

// scenarioCall is one tool call of a conversation, with where it ran.
type scenarioCall struct {
	Tool, Status, Params   string
	UnderAnotherAccount    bool
	RanOn, Integration     string
	RanOnAnotherAccountKey string
}

func scenarioCalls(t *testing.T, conversationId string) []scenarioCall {
	t.Helper()
	dbm, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	rows, err := dbm.Db.Query(`
		SELECT t.tool_name, COALESCE(t.status, ''), LEFT(COALESCE(NULLIF(t.params_sql, ''), t.parameters, ''), 300),
		       t.account_id::text <> c.account_id::text,
		       COALESCE(t.metadata->'nb_executed_target'->>'name', ''), COALESCE(t.metadata->>'nb_resolved_target', ''),
		       COALESCE(t.metadata->'nb_executed_target'->>'account_id', '')
		FROM llm_conversation_tool_calls t JOIN llm_conversations c ON c.id = t.conversation_id
		WHERE t.conversation_id = $1 ORDER BY t.created_at`, conversationId)
	require.NoError(t, err)
	defer rows.Close()
	var calls []scenarioCall
	for rows.Next() {
		var c scenarioCall
		require.NoError(t, rows.Scan(&c.Tool, &c.Status, &c.Params, &c.UnderAnotherAccount, &c.RanOn, &c.Integration, &c.RanOnAnotherAccountKey))
		t.Logf("  %s status=%s ran_on=%q integration=%q params=%.160s", c.Tool, c.Status, c.RanOn, c.Integration, c.Params)
		calls = append(calls, c)
	}
	require.NoError(t, rows.Err())
	return calls
}

// scenarioPending is the question a conversation is waiting on: its stored
// config and the agent that asked it; ok is false when nothing is waiting.
func scenarioPending(t *testing.T, conversationId string) (map[string]any, uuid.UUID, bool) {
	t.Helper()
	dbm, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	var raw, agentId string
	err = dbm.Db.QueryRow(`SELECT COALESCE(message_config, '{}'), parent_agent_id::text FROM llm_conversation_messages
		WHERE conversation_id = $1 AND message_type = 'followup' AND status = 'WAITING' ORDER BY created_at DESC LIMIT 1`, conversationId).Scan(&raw, &agentId)
	if err != nil {
		return nil, uuid.Nil, false
	}
	cfg := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(raw), &cfg))
	return cfg, uuid.MustParse(agentId), true
}

// scenarioLastQuestionAt is when the conversation's newest question was asked,
// on the DB's clock; take it before answering.
func scenarioLastQuestionAt(t *testing.T, conversationId string) string {
	t.Helper()
	dbm, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	var at string
	require.NoError(t, dbm.Db.QueryRow(`SELECT COALESCE(MAX(created_at)::text, '') FROM llm_conversation_messages
		WHERE conversation_id = $1 AND message_type = 'followup'`, conversationId).Scan(&at))
	return at
}

// scenarioWaitAfterAnswer waits until a resumed run finishes or asks a
// question newer than askedAt; a new question is not terminal, so the generic
// wait would sit on it until its timeout.
func scenarioWaitAfterAnswer(t *testing.T, accountId, conversationId, messageId, askedAt string, timeout time.Duration) string {
	t.Helper()
	dbm, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var asked bool
		if err := dbm.Db.QueryRow(`SELECT EXISTS (SELECT 1 FROM llm_conversation_messages WHERE conversation_id = $1
			AND message_type = 'followup' AND status = 'WAITING' AND created_at > NULLIF($2, '')::timestamp)`, conversationId, askedAt).Scan(&asked); err == nil && asked {
			return "asked again"
		}
		if msg, err := core.GetConversationDao().GetConversationMessage(messageId, accountId, conversationId); err == nil {
			switch msg.Status {
			case core.ConversationStatusCompleted, core.ConversationStatusFailed, core.ConversationStatusTerminated, core.ConversationStatusKilled:
				return string(msg.Status)
			}
		}
		time.Sleep(time.Second)
	}
	return "timed out"
}

// scenarioKubectl runs a kubectl command directly (no LLM) on a cluster, as
// ground truth and for cleanup.
func scenarioKubectl(t *testing.T, cluster, command string) string {
	t.Helper()
	accountId, userId := os.Getenv("TEST_ACCOUNT"), os.Getenv("TEST_USER")
	tool, ok := toolcore.GetNBTool(accountId, tools.ToolExecuteKubectlCommand)
	require.True(t, ok)
	ctx := toolcore.NewNbToolContext(tenantWideE2EContext(), tool, accountId, userId, uuid.NewString(), uuid.NewString(), uuid.NewString(), "", nil, "", toolcore.NBQueryConfig{}, "oracle-"+cluster)
	resp, err := tool.Call(ctx, toolcore.NBToolCallRequest{Command: command, Arguments: map[string]any{"cluster": cluster}})
	require.NoError(t, err)
	return fmt.Sprint(resp.Data)
}

// TestCrossAccountScenario_OwnAccountUnchanged: questions about the conversation's own
// account only — the path almost all traffic takes — never ask about an
// account or a connection, never carry a "ran on" label, and never run under
// another account.
func TestCrossAccountScenario_OwnAccountUnchanged(t *testing.T) {
	requireDelegateE2EEnv(t)
	sc := tenantWideE2EContext()
	accountId := os.Getenv("TEST_ACCOUNT")
	for name, question := range map[string]string{
		"kubectl":  "How many pods are running in the nudgebee namespace?",
		"postgres": "On the " + envOr("TEST_OWN_PG_FIRST", "dev-pg") + " postgres connection, run select current_database() and tell me the result.",
		"logs":     "Were there any error logs in the nudgebee namespace in the last 30 minutes?",
		"traces":   "Which endpoints were the slowest in the last hour?",
	} {
		t.Run(name, func(t *testing.T) {
			resp := runPreservedE2EConversation(t, sc, newK8sOrchestratorAgent(accountId), "own-account-"+name, question)
			assert.NotEqual(t, core.ConversationStatusWaiting, resp.Status, "a question about the conversation's own account must not ask: %q", resp.FollowupRequest.Question)
			for _, c := range scenarioCalls(t, resp.ConversationId) {
				assert.Empty(t, c.RanOn, "%s is labelled as run on another account", c.Tool)
				assert.False(t, c.UnderAnotherAccount, "%s ran under another account", c.Tool)
			}
		})
	}
}

// TestCrossAccountScenario_UnattendedRunOnAnotherAccount: an automation run about another
// account behaves as one about its own. A read with one place to run must not
// wait on a question; several integrations there get the same question the own
// account gets (unanswered automation questions are #38728's timeout work), and
// nothing answers from the conversation's own account instead.
func TestCrossAccountScenario_UnattendedRunOnAnotherAccount(t *testing.T) {
	requireDelegateE2EEnv(t)
	sc := tenantWideE2EContext()
	accountId, userId := os.Getenv("TEST_ACCOUNT"), os.Getenv("TEST_USER")
	prod := delegateE2ETarget("TEST_DELEGATE_K8S_TARGET", "k8s-prod")
	for name, question := range map[string]string{
		"pods":     "How many pods are running in the nudgebee namespace on " + prod + "?",
		"postgres": "On " + prod + ", run select current_database() on its postgres database.", // two integrations there, none named
	} {
		t.Run(name, func(t *testing.T) {
			sessionId := fmt.Sprintf("unattended-%s-%d", name, time.Now().Unix())
			resp, err := core.HandleConversationSessionRequest(sc, newK8sOrchestratorAgent(accountId), userId, accountId, sessionId, question,
				core.ConversationSessionRequestWithSource(core.ConversationSourceAutomation))
			require.NoError(t, err)
			t.Logf("Preserved sessionId=%s conversationId=%s status=%s", sessionId, resp.ConversationId, resp.Status)
			if cfg, _, waiting := scenarioPending(t, resp.ConversationId); waiting {
				question, _ := cfg["question"].(string)
				if name == "pods" {
					t.Errorf("a read with one place to run waits on a question: %s", question)
				} else {
					assert.Contains(t, question, prod, "the integration question does not name %s", prod)
				}
			}
			for _, c := range scenarioCalls(t, resp.ConversationId) {
				if c.Status != "success" || c.Tool == "delegate_agent" {
					continue
				}
				if c.Tool == tools.ToolExecutePostgresQuery || c.Tool == tools.ToolExecuteKubectlCommand {
					assert.Equal(t, prod, c.RanOn, "%s answered from an account other than %s", c.Tool, prod)
				}
			}
		})
	}
}

// TestCrossAccountScenario_AmbiguousAccountName: "prod" matches several connected
// accounts. Nubi either asks which one, or works on exactly one and says
// which in its answer — never a silent pick or a mix.
func TestCrossAccountScenario_AmbiguousAccountName(t *testing.T) {
	requireDelegateE2EEnv(t)
	sc := tenantWideE2EContext()
	accountId := os.Getenv("TEST_ACCOUNT")
	resp := runPreservedE2EConversation(t, sc, newK8sOrchestratorAgent(accountId), "ambiguous-prod", "Check the pods in the nudgebee namespace on prod.")
	if resp.Status == core.ConversationStatusWaiting {
		t.Logf("asked: %q options=%v", resp.FollowupRequest.Question, resp.FollowupRequest.FollowupOptions)
		return
	}
	ranOn := map[string]bool{}
	for _, c := range scenarioCalls(t, resp.ConversationId) {
		if c.Status == "success" && c.RanOn != "" && c.Tool != "delegate_agent" {
			ranOn[c.RanOn] = true
		}
	}
	require.LessOrEqual(t, len(ranOn), 1, "work on prod must not mix accounts: %v", ranOn)
	answer := conversationAnswer(t, resp.ConversationId)
	for account := range ranOn {
		assert.Contains(t, answer, account, "the answer must say which account %q it used", account)
	}
}

// TestCrossAccountScenario_WritesOnTwoAccountsInOneTurn: one request to write on the
// conversation's own cluster and on another is approved separately for each
// cluster, each card names its cluster, and each write lands only there.
func TestCrossAccountScenario_WritesOnTwoAccountsInOneTurn(t *testing.T) {
	requireDelegateE2EEnv(t)
	sc := tenantWideE2EContext()
	accountId, userId := os.Getenv("TEST_ACCOUNT"), os.Getenv("TEST_USER")
	prod := delegateE2ETarget("TEST_DELEGATE_K8S_TARGET", "k8s-prod")
	dev := delegateE2ETarget("TEST_DELEGATE_K8S_ORIGIN", "k8s-dev")
	name := fmt.Sprintf("nb-e2e-two-%d", time.Now().Unix())
	for _, cluster := range []string{prod, dev} {
		cluster := cluster
		t.Cleanup(func() { scenarioKubectl(t, cluster, "kubectl delete configmap "+name+" -n default --ignore-not-found") })
	}
	sessionId := fmt.Sprintf("writes-two-accounts-%d", time.Now().Unix())
	resp, err := core.HandleConversationSessionRequest(sc, newK8sOrchestratorAgent(accountId), userId, accountId, sessionId,
		fmt.Sprintf("Create a configmap named %s in the default namespace on both %s and %s, with key owner=nudgebee-e2e.", name, dev, prod))
	require.NoError(t, err)
	t.Logf("Preserved sessionId=%s conversationId=%s status=%s", sessionId, resp.ConversationId, resp.Status)

	approvedFor := map[string]bool{}
	for i := 0; i < 4; i++ {
		cfg, waiting, ok := scenarioPending(t, resp.ConversationId)
		if !ok {
			break
		}
		require.Equal(t, string(core.FollowupTypeToolConfirmation), cfg["followupType"], "only approvals are expected: %v", cfg["question"])
		target, _ := cfg["target"].(map[string]any)
		cluster, _ := target["name"].(string)
		require.NotEmpty(t, cluster, "every approval must say where the write lands: %v", cfg["question"])
		assert.Equal(t, cluster != dev, cfg["crossAccount"] == true, "crossAccount marks %s correctly", cluster)
		assert.False(t, approvedFor[cluster], "%s is asked twice", cluster)
		approvedFor[cluster] = true
		t.Logf("approving the write on %s", cluster)
		askedAt := scenarioLastQuestionAt(t, resp.ConversationId)
		agent, _, _, err := core.ResolveAgentByConversationAgentId(sc, waiting, accountId)
		require.NoError(t, err)
		_, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId, "yes",
			core.ConversationSessionRequestWithConversationId(uuid.NullUUID{UUID: uuid.MustParse(resp.ConversationId), Valid: true}),
			core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: uuid.MustParse(resp.MessageId), Valid: true}),
			core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: waiting, Valid: true}),
			core.ConversationSessionRequestWithIsResume(true),
		)
		require.NoError(t, err)
		t.Logf("after yes: %s", scenarioWaitAfterAnswer(t, accountId, resp.ConversationId, resp.MessageId, askedAt, 240*time.Second))
	}
	assert.True(t, approvedFor[prod] && approvedFor[dev], "one approval per cluster, got %v", approvedFor)
	for _, cluster := range []string{prod, dev} {
		assert.True(t, strings.Contains(scenarioKubectl(t, cluster, "kubectl get configmap "+name+" -n default -o name --ignore-not-found"), "configmap/"+name), "the write must land on %s", cluster)
	}
	for _, c := range scenarioCalls(t, resp.ConversationId) {
		if c.Status == "success" && strings.Contains(c.Params, name) && strings.Contains(c.Params, "create") {
			assert.True(t, c.RanOn == prod || c.RanOn == "", "%s labelled %q", c.Tool, c.RanOn)
		}
	}
}
