package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runbookCall struct {
	path, scope, account string
}

// fakeRunbook answers a listing for the conversation's account with own and
// one with ?scope=readable with others ("" = a server that ignores scope and
// answers own again), and records every call.
func fakeRunbook(t *testing.T, own, others string) *[]runbookCall {
	t.Helper()
	var mu sync.Mutex
	calls := &[]runbookCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*calls = append(*calls, runbookCall{path: r.URL.Path, scope: r.URL.Query().Get("scope"), account: r.Header.Get("X-Account-ID")})
		mu.Unlock()
		if r.URL.Query().Get("scope") == "readable" && others != "" {
			_, _ = w.Write([]byte(others))
			return
		}
		_, _ = w.Write([]byte(own))
	}))
	t.Cleanup(srv.Close)
	endpoint, base := config.Config.WorkflowServerEndpoint, config.Config.BaseUrl
	config.Config.WorkflowServerEndpoint, config.Config.BaseUrl = srv.URL, "https://app.example.com/"
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint, config.Config.BaseUrl = endpoint, base })
	StubAccountTargets(t, map[string]*core.ExecutionTarget{
		"acct":   {Provider: "kubernetes", Name: "k8s-dev", AccountID: "acct"},
		"acct-b": {Provider: "aws", Name: "aws-prod", AccountID: "acct-b"},
	})
	return calls
}

// A user who reads acct (the conversation's) and acct-b.
func workflowAccountsContext(userID string) core.NbToolContext {
	return core.NbToolContext{AccountId: "acct", Ctx: security.NewRequestContextForTenantAccountAdmin("tenant-1", userID, []string{"acct", "acct-b"})}
}

type groupedListing struct {
	ThisAccount struct {
		AccountID   string           `json:"account_id"`
		AccountName string           `json:"account_name"`
		Workflows   []map[string]any `json:"workflows"`
		Note        string           `json:"note"`
	} `json:"this_account"`
	OtherAccounts []struct {
		AccountID   string           `json:"account_id"`
		AccountName string           `json:"account_name"`
		Workflows   []map[string]any `json:"workflows"`
	} `json:"other_accounts"`
	Note string `json:"note"`
}

func parseGrouped(t *testing.T, data string) groupedListing {
	t.Helper()
	var got groupedListing
	require.NoError(t, json.Unmarshal([]byte(data), &got), data)
	return got
}

func TestWorkflowSearchGroupsOtherAccountsApartFromThisOne(t *testing.T) {
	calls := fakeRunbook(t,
		`{"workflows":[{"id":"wf-a","account_id":"acct","name":"restart-a"}],"total_invocable":1}`,
		// The conversation's own account and a row without an account are not
		// another account's, whatever the server returns.
		`{"workflows":[{"id":"wf-b","account_id":"acct-b","name":"restart-b"},{"id":"wf-a","account_id":"acct"},{"id":"wf-x"}],"total_invocable":3}`)

	resp, err := WorkflowSearchTool{}.Call(workflowAccountsContext("user-1"), core.NBToolCallRequest{Arguments: map[string]any{"query": "restart"}})
	require.NoError(t, err)
	got := parseGrouped(t, resp.Data)

	assert.Equal(t, "acct", got.ThisAccount.AccountID)
	assert.Equal(t, "k8s-dev", got.ThisAccount.AccountName)
	require.Len(t, got.ThisAccount.Workflows, 1)
	assert.Equal(t, "https://app.example.com/automation/wf-a?accountId=acct", got.ThisAccount.Workflows[0]["url"])

	require.Len(t, got.OtherAccounts, 1)
	assert.Equal(t, "acct-b", got.OtherAccounts[0].AccountID)
	assert.Equal(t, "aws-prod", got.OtherAccounts[0].AccountName)
	require.Len(t, got.OtherAccounts[0].Workflows, 1)
	assert.Equal(t, "https://app.example.com/automation/wf-b?accountId=acct-b", got.OtherAccounts[0].Workflows[0]["url"],
		"a link opens the automation on its own account")
	assert.Contains(t, got.Note, otherAccountsNote)
	assert.Contains(t, got.Note, "showing 1 of 3")

	require.Len(t, *calls, 2)
	assert.Equal(t, "", (*calls)[0].scope)
	assert.Equal(t, "readable", (*calls)[1].scope)
	assert.Equal(t, "acct", (*calls)[1].account)
}

func TestWorkflowSearchSaysWhenOnlyOtherAccountsMatched(t *testing.T) {
	fakeRunbook(t, `{"workflows":[],"total_invocable":0}`,
		`{"workflows":[{"id":"wf-b","account_id":"acct-b","name":"restart-b"}],"total_invocable":1}`)

	resp, err := WorkflowSearchTool{}.Call(workflowAccountsContext("user-1"), core.NBToolCallRequest{Arguments: map[string]any{"query": "restart"}})
	require.NoError(t, err)
	got := parseGrouped(t, resp.Data)
	assert.Empty(t, got.ThisAccount.Workflows)
	assert.Equal(t, noMatchNote, got.ThisAccount.Note)
	assert.Contains(t, got.Note, "Nothing in this conversation's account matched; 1 automation(s) in other accounts did.")
}

func TestWorkflowSearchAgainstAServerWithoutOtherAccounts(t *testing.T) {
	// A runbook-server from before ?scope=readable answers for the
	// conversation's account twice; nothing is listed as another account's.
	fakeRunbook(t, `{"workflows":[{"id":"wf-a","name":"restart-a"}],"total_invocable":1}`, "")

	resp, err := WorkflowSearchTool{}.Call(workflowAccountsContext("user-1"), core.NBToolCallRequest{Arguments: map[string]any{"query": "restart"}})
	require.NoError(t, err)
	got := parseGrouped(t, resp.Data)
	require.Len(t, got.ThisAccount.Workflows, 1)
	assert.Empty(t, got.OtherAccounts)
	assert.Empty(t, got.Note)
}

func TestWorkflowSearchWithNoUserSearchesOnlyThisAccount(t *testing.T) {
	calls := fakeRunbook(t, `{"workflows":[],"total_invocable":0}`, `{"workflows":[{"id":"wf-b","account_id":"acct-b"}]}`)

	_, err := WorkflowSearchTool{}.Call(workflowAccountsContext(""), core.NBToolCallRequest{Arguments: map[string]any{}})
	require.NoError(t, err)
	require.Len(t, *calls, 1, "whose other accounts to search is the user's question")
}

func TestWorkflowListGroupsOtherAccounts(t *testing.T) {
	fakeRunbook(t,
		`{"workflows":[{"id":"wf-a","name":"a","status":"ACTIVE","account_id":"acct","tenant_id":"t"}],"total_count":1}`,
		`{"workflows":[{"id":"wf-b","name":"b","status":"PAUSED","account_id":"acct-b","tenant_id":"t"}],"total_count":1}`)

	resp, err := WorkflowListTool{}.Call(workflowAccountsContext("user-1"), core.NBToolCallRequest{Arguments: map[string]any{}})
	require.NoError(t, err)
	got := parseGrouped(t, resp.Data)
	require.Len(t, got.ThisAccount.Workflows, 1)
	require.Len(t, got.OtherAccounts, 1)
	b := got.OtherAccounts[0].Workflows[0]
	assert.Equal(t, "PAUSED", b["status"])
	assert.NotContains(t, b, "tenant_id", "other accounts' rows are projected like this account's")
	assert.Equal(t, "https://app.example.com/automation/wf-b?accountId=acct-b", b["url"])
}

func TestWorkflowGetOnAnotherAccount(t *testing.T) {
	calls := fakeRunbook(t, `{"id":"wf-b","name":"restart-b","definition":{}}`, "")

	resp, err := WorkflowGetTool{}.Call(workflowAccountsContext("user-1"),
		core.NBToolCallRequest{Arguments: map[string]any{"id": "wf-b", "account_id": "acct-b"}})
	require.NoError(t, err)
	require.NotEmpty(t, *calls)
	for _, call := range *calls {
		assert.Equal(t, "acct-b", call.account, call.path)
	}
	ran, _ := resp.AdditionalDetails[core.MetadataExecutedTarget].(map[string]any)
	assert.Equal(t, "acct-b", ran["account_id"], "the conversation shows the call ran on acct-b")
	require.Len(t, resp.References, 1)
	assert.Contains(t, resp.References[0].Url, "accountId=acct-b")
}

func TestWorkflowCallsRefuseAnAccountTheUserCannotRead(t *testing.T) {
	calls := fakeRunbook(t, `{}`, "")
	ctx := workflowAccountsContext("user-1")
	foreign := map[string]any{"id": "wf-c", "execution_id": "ex-1", "account_id": "acct-c"}

	for _, tool := range []core.NBTool{WorkflowGetTool{}, WorkflowTriggerTool{accountId: "acct"}, WorkflowExecutionGetTool{},
		WorkflowExecutionsTool{}, WorkflowExecutionRetriggerTool{accountId: "acct"}, WorkflowExecutionCancelTool{accountId: "acct"}} {
		_, err := tool.Call(ctx, core.NBToolCallRequest{Arguments: foreign})
		require.Error(t, err, tool.Name())
		assert.Contains(t, err.Error(), `account_id "acct-c"`)
	}
	assert.Empty(t, *calls, "nothing is asked of runbook-server for an account the user cannot read")
}

func TestWorkflowCallsWithoutAccountStayOnTheConversationsAccount(t *testing.T) {
	calls := fakeRunbook(t, `{"id":"ex-1","status":"COMPLETED"}`, "")

	for _, args := range []map[string]any{
		{"id": "wf-a", "execution_id": "ex-1"},
		{"id": "wf-a", "execution_id": "ex-1", "account_id": "acct"},
	} {
		resp, err := WorkflowExecutionGetTool{}.Call(workflowAccountsContext("user-1"), core.NBToolCallRequest{Arguments: args})
		require.NoError(t, err)
		assert.NotContains(t, resp.AdditionalDetails, core.MetadataExecutedTarget)
	}
	require.Len(t, *calls, 2)
	assert.Equal(t, "acct", (*calls)[0].account)
	assert.Equal(t, "acct", (*calls)[1].account)
}

func TestWorkflowTriggerNamesTheAccountItRunsOn(t *testing.T) {
	trigger := WorkflowTriggerTool{accountId: "acct"}
	assert.Equal(t, "acct-b", trigger.RequestedAccountID(`{"id":"wf-b","account_id":"acct-b"}`))
	assert.Equal(t, "", trigger.RequestedAccountID(`{"id":"wf-a","account_id":"acct"}`), "the conversation's own account")
	assert.Equal(t, "", trigger.RequestedAccountID(`{"id":"wf-a"}`))
	assert.Contains(t, trigger.InputSchema().Properties, workflowAccountArg)
	assert.NotContains(t, core.ModelInputSchema(trigger).Properties, "target", "an automation is addressed by account, not by connection")
}

// After a run on another account, its follow-ups (list runs, retry, cancel)
// go to that account too.
func TestWorkflowRunFollowUpsOnAnotherAccount(t *testing.T) {
	calls := fakeRunbook(t, `{"ok":true}`, "")
	args := map[string]any{"id": "wf-b", "execution_id": "ex-1", "account_id": "acct-b"}

	for _, tool := range []core.NBTool{WorkflowExecutionsTool{}, WorkflowExecutionRetriggerTool{accountId: "acct"}, WorkflowExecutionCancelTool{accountId: "acct"}} {
		resp, err := tool.Call(workflowAccountsContext("user-1"), core.NBToolCallRequest{Arguments: args})
		require.NoError(t, err, tool.Name())
		ran, _ := resp.AdditionalDetails[core.MetadataExecutedTarget].(map[string]any)
		assert.Equal(t, "acct-b", ran["account_id"], tool.Name())
	}
	require.Len(t, *calls, 3)
	for _, call := range *calls {
		assert.Equal(t, "acct-b", call.account, call.path)
	}

	for _, tool := range []core.AccountArgTool{WorkflowExecutionRetriggerTool{accountId: "acct"}, WorkflowExecutionCancelTool{accountId: "acct"}} {
		assert.Equal(t, "acct-b", tool.RequestedAccountID(`{"id":"wf-b","execution_id":"ex-1","account_id":"acct-b"}`))
		assert.Equal(t, "", tool.RequestedAccountID(`{"id":"wf-a","execution_id":"ex-1"}`))
	}
}

func TestWorkflowSearchSaysWhenOtherAccountsCouldNotBeRead(t *testing.T) {
	fakeRunbook(t, `{"workflows":[{"id":"wf-a","name":"restart-a"}],"total_invocable":1}`, `not json`)

	resp, err := WorkflowSearchTool{}.Call(workflowAccountsContext("user-1"), core.NBToolCallRequest{Arguments: map[string]any{"query": "restart"}})
	require.NoError(t, err)
	got := parseGrouped(t, resp.Data)
	require.Len(t, got.ThisAccount.Workflows, 1)
	assert.Contains(t, got.Note, "Other accounts could not be searched")
}
