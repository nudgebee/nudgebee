//go:build e2e

package tools

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
)

// End-to-end against a runbook-server with ?scope=readable and the real
// security context of real users (services-server). Set:
//
//	E2E_TENANT, E2E_ACCOUNT (the conversation's), E2E_USER (reads E2E_ACCOUNT
//	and E2E_OTHER_ACCOUNT), E2E_OTHER_ACCOUNT, E2E_OTHER_WORKFLOW_ID (an
//	AI-invocable automation there, safe to run), E2E_QUERY (matches it),
//	E2E_LIMITED_USER + E2E_LIMITED_ACCOUNT (reads only that account), and
//	optionally E2E_OTHER_WORKFLOW_INPUTS (JSON object of the run's inputs).
func e2eEnv(t *testing.T, names ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, n := range names {
		v := os.Getenv(n)
		if v == "" {
			t.Skipf("%s not set", n)
		}
		out[n] = v
	}
	return out
}

func e2eToolContext(t *testing.T, tenant, user, account string) core.NbToolContext {
	t.Helper()
	sc, err := security.NewSecurityContext(tenant, user)
	require.NoError(t, err)
	ctx := security.NewRequestContext(context.Background(), sc, slog.Default(), otel.GetTracerProvider().Tracer("e2e"), nil)
	return core.NbToolContext{AccountId: account, UserId: user, Ctx: ctx}
}

func TestE2EWorkflowSearchListsOtherAccountsApart(t *testing.T) {
	env := e2eEnv(t, "E2E_TENANT", "E2E_USER", "E2E_ACCOUNT", "E2E_OTHER_ACCOUNT", "E2E_OTHER_WORKFLOW_ID", "E2E_QUERY")
	ctx := e2eToolContext(t, env["E2E_TENANT"], env["E2E_USER"], env["E2E_ACCOUNT"])

	resp, err := WorkflowSearchTool{}.Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"query": env["E2E_QUERY"]}})
	require.NoError(t, err)
	t.Logf("workflow_search:\n%s", resp.Data)
	got := parseGrouped(t, resp.Data)

	assert.Equal(t, env["E2E_ACCOUNT"], got.ThisAccount.AccountID)
	var found bool
	for _, g := range got.OtherAccounts {
		assert.NotEqual(t, env["E2E_ACCOUNT"], g.AccountID, "the conversation's account is never listed as another")
		for _, w := range g.Workflows {
			if w["id"] == env["E2E_OTHER_WORKFLOW_ID"] {
				found = true
				assert.Equal(t, env["E2E_OTHER_ACCOUNT"], g.AccountID)
				assert.NotEmpty(t, g.AccountName)
			}
		}
	}
	assert.True(t, found, "the other account's automation is listed under other_accounts")
	assert.Contains(t, got.Note, "Running one acts on that account")

	list, err := WorkflowListTool{}.Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"limit": "5"}})
	require.NoError(t, err)
	listed := parseGrouped(t, list.Data)
	assert.Equal(t, env["E2E_ACCOUNT"], listed.ThisAccount.AccountID)
	assert.NotEmpty(t, listed.OtherAccounts)
}

func TestE2EWorkflowRunAndFollowUpsOnAnotherAccount(t *testing.T) {
	env := e2eEnv(t, "E2E_TENANT", "E2E_USER", "E2E_ACCOUNT", "E2E_OTHER_ACCOUNT", "E2E_OTHER_WORKFLOW_ID")
	ctx := e2eToolContext(t, env["E2E_TENANT"], env["E2E_USER"], env["E2E_ACCOUNT"])
	other, id := env["E2E_OTHER_ACCOUNT"], env["E2E_OTHER_WORKFLOW_ID"]
	ranOn := func(resp core.NBToolResponse) any {
		recorded, _ := resp.AdditionalDetails[core.MetadataExecutedTarget].(map[string]any)
		return recorded["account_id"]
	}

	got, err := WorkflowGetTool{}.Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"id": id, "account_id": other}})
	require.NoError(t, err)
	assert.Equal(t, other, ranOn(got))

	trigger := WorkflowTriggerTool{accountId: env["E2E_ACCOUNT"]}
	assert.Equal(t, other, trigger.RequestedAccountID(`{"id":"`+id+`","account_id":"`+other+`"}`))
	inputs := map[string]any{}
	if raw := os.Getenv("E2E_OTHER_WORKFLOW_INPUTS"); raw != "" {
		require.NoError(t, json.Unmarshal([]byte(raw), &inputs))
	}
	run, err := trigger.Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"id": id, "account_id": other, "inputs": inputs}})
	require.NoError(t, err)
	t.Logf("workflow_trigger:\n%s", run.Data)
	assert.Equal(t, other, ranOn(run))
	var outcome struct {
		ExecutionID string `json:"execution_id"`
		Status      string `json:"status"`
	}
	require.NoError(t, json.Unmarshal([]byte(run.Data), &outcome))
	require.NotEmpty(t, outcome.ExecutionID)
	assert.Equal(t, "COMPLETED", outcome.Status)

	runs, err := WorkflowExecutionsTool{}.Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"id": id, "limit": "5", "account_id": other}})
	require.NoError(t, err)
	assert.Contains(t, runs.Data, outcome.ExecutionID, "the run is listed on its own account")

	one, err := WorkflowExecutionGetTool{}.Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"id": id, "execution_id": outcome.ExecutionID, "account_id": other}})
	require.NoError(t, err)
	assert.Equal(t, other, ranOn(one))

	ownAccount, err := WorkflowExecutionsTool{}.Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"id": id, "limit": "5"}})
	require.NoError(t, err)
	assert.NotContains(t, ownAccount.Data, outcome.ExecutionID, "without account_id the call stays on the conversation's account, where the run is not")
}

func TestE2EWorkflowUserWhoCannotReadTheOtherAccount(t *testing.T) {
	env := e2eEnv(t, "E2E_TENANT", "E2E_LIMITED_USER", "E2E_LIMITED_ACCOUNT", "E2E_OTHER_ACCOUNT", "E2E_OTHER_WORKFLOW_ID")
	ctx := e2eToolContext(t, env["E2E_TENANT"], env["E2E_LIMITED_USER"], env["E2E_LIMITED_ACCOUNT"])

	resp, err := WorkflowSearchTool{}.Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{}})
	require.NoError(t, err)
	t.Logf("workflow_search (limited user):\n%s", resp.Data)
	for _, g := range parseGrouped(t, resp.Data).OtherAccounts {
		assert.NotEqual(t, env["E2E_OTHER_ACCOUNT"], g.AccountID)
	}

	for _, tool := range []core.NBTool{WorkflowGetTool{}, WorkflowTriggerTool{accountId: env["E2E_LIMITED_ACCOUNT"]}, WorkflowExecutionsTool{}} {
		_, err := tool.Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"id": env["E2E_OTHER_WORKFLOW_ID"], "account_id": env["E2E_OTHER_ACCOUNT"]}})
		require.Error(t, err, tool.Name())
		assert.Contains(t, err.Error(), "is not an account whose automations you can access", tool.Name())
	}
}
