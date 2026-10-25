package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"nudgebee/llm/tools/core"

	"github.com/google/uuid"
)

// Automations belong to an account. workflow_search and workflow_list show the
// conversation's own account's automations under this_account and those of the
// other accounts the user can read under other_accounts, each group with its
// account id and name; workflow_get, workflow_trigger and
// workflow_execution_get then act on one of those through account_id.
//
// The other accounts are found by runbook-server (?scope=readable), which owns
// who may read which account's automations (built-in roles and workflows
// grants) and checks it again on every call. They are fetched apart from the
// conversation's own account, so they never crowd it out of a result.

// workflowAccountArg names the account a workflow call acts on.
const workflowAccountArg = "account_id"

// workflowsModule is the permission module automations are granted under.
const workflowsModule = "workflows"

func workflowAccountSchemaProperty() core.ToolSchemaProperty {
	return core.ToolSchemaProperty{
		Type: core.ToolSchemaTypeString,
		Description: "Account the automation belongs to: the account_id of its group under other_accounts in " +
			"workflow_search or workflow_list. Omit for an automation of this conversation's account.",
	}
}

// requestedWorkflowAccount is the other account a call names; "" when it names
// none or the conversation's own.
func requestedWorkflowAccount(args map[string]any, origin string) string {
	id, _ := args[workflowAccountArg].(string)
	if id = strings.TrimSpace(id); id == origin {
		return ""
	}
	return id
}

// workflowCallAccount is the account a workflow call acts on: the one it
// names, if the user may read automations there, else the conversation's own.
func workflowCallAccount(ctx core.NbToolContext, input core.NBToolCallRequest) (string, error) {
	id := requestedWorkflowAccount(input.Arguments, ctx.AccountId)
	if id == "" {
		return ctx.AccountId, nil
	}
	if ctx.Ctx == nil || ctx.Ctx.GetSecurityContext() == nil ||
		!ctx.Ctx.GetSecurityContext().CanReadAccountData(id, workflowsModule) {
		return "", fmt.Errorf("account_id %q is not an account whose automations you can access; "+
			"use the account_id of a group under other_accounts in workflow_search or workflow_list", id)
	}
	return id, nil
}

// workflowAccountTarget names an account for the approval card and the
// conversation view; a bare id when the account cannot be described.
func workflowAccountTarget(accountID string) *core.ExecutionTarget {
	if target := AccountTarget(accountID); target != nil {
		return target
	}
	return &core.ExecutionTarget{Name: accountID, AccountID: accountID}
}

// ranOnWorkflowAccount records on a response the account a call ran on when it
// is not the conversation's own, so the conversation shows where it ran.
func ranOnWorkflowAccount(resp core.NBToolResponse, ctx core.NbToolContext, accountID string) core.NBToolResponse {
	if accountID == "" || accountID == ctx.AccountId {
		return resp
	}
	resp.AdditionalDetails = core.WithExecutedTarget(resp.AdditionalDetails, workflowAccountTarget(accountID))
	return resp
}

// workflowAccountName is an account's name, "" when it cannot be read.
func workflowAccountName(accountID string) string {
	if target := AccountTarget(accountID); target != nil {
		return target.Name
	}
	return ""
}

// workflowAccountGroup is one other account's automations.
type workflowAccountGroup struct {
	AccountID   string `json:"account_id"`
	AccountName string `json:"account_name,omitempty"`
	Workflows   []any  `json:"workflows"`
}

// otherAccountsNote travels with automations found in other accounts.
const otherAccountsNote = "Automations under other_accounts belong to another account the user can access, not to this " +
	"conversation's account. Say which account an automation is in when you mention it, and pass that group's " +
	"account_id to workflow_get or workflow_trigger to use it. Running one acts on that account, not on this " +
	"conversation's systems: offer it only when the user's problem is in that account."

// otherWorkflowAccounts fetches path (a listing with its query string, if any)
// for the user's other readable accounts and groups the rows by account, each
// row shaped by project for its own account. It never fails the listing: a
// problem comes back as a note, since the conversation's own account answered.
func otherWorkflowAccounts(ctx core.NbToolContext, path string, project func(raw []byte, accountID string) []byte) (groups []workflowAccountGroup, total int, problem string) {
	sc := ctx.Ctx.GetSecurityContext()
	if userID := sc.GetUserId(); userID == "" || userID == uuid.Nil.String() {
		// Whose accounts would be searched is the user's question; with no
		// user there is no answer, and runbook-server refuses to guess.
		return nil, 0, ""
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	raw, err := DoRunbookRequestWithContext(ctx.GoContext(), "GET", path+sep+"scope=readable", nil,
		ctx.AccountId, sc.GetTenantId(), sc.GetUserId())
	if err != nil {
		ctx.Ctx.GetLogger().Warn("workflow: other accounts could not be listed", "path", path, "error", err)
		return nil, 0, "Other accounts could not be searched; only this conversation's account is shown."
	}
	var parsed struct {
		Workflows      []map[string]any `json:"workflows"`
		TotalInvocable *int             `json:"total_invocable"`
		TotalCount     *int             `json:"total_count"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		ctx.Ctx.GetLogger().Warn("workflow: other accounts' listing could not be read", "path", path, "error", err)
		return nil, 0, "Other accounts could not be searched; only this conversation's account is shown."
	}

	byAccount := map[string][]map[string]any{}
	var order []string
	for _, row := range parsed.Workflows {
		id, _ := row["account_id"].(string)
		// A server that does not know scope=readable answers for the
		// conversation's account again, without account ids: nothing to add.
		if id == "" || id == ctx.AccountId {
			continue
		}
		if _, seen := byAccount[id]; !seen {
			order = append(order, id)
		}
		byAccount[id] = append(byAccount[id], row)
	}
	for _, id := range order {
		body, err := json.Marshal(map[string]any{"workflows": byAccount[id]})
		if err != nil {
			continue
		}
		var shaped struct {
			Workflows []any `json:"workflows"`
		}
		if err := json.Unmarshal(project(body, id), &shaped); err != nil {
			continue
		}
		groups = append(groups, workflowAccountGroup{AccountID: id, AccountName: workflowAccountName(id), Workflows: shaped.Workflows})
	}
	switch {
	case parsed.TotalInvocable != nil:
		total = *parsed.TotalInvocable
	case parsed.TotalCount != nil:
		total = *parsed.TotalCount
	}
	return groups, total, ""
}

// withWorkflowAccounts puts a listing of the conversation's own account (own,
// already shaped) under this_account, named, and the other accounts' groups
// under other_accounts, with a note that says which is which. A response that
// is not the expected shape is returned unchanged.
func withWorkflowAccounts(own []byte, origin string, groups []workflowAccountGroup, otherTotal int, problem string) []byte {
	var this map[string]any
	if err := json.Unmarshal(own, &this); err != nil || this == nil {
		return own
	}
	this["account_id"] = origin
	if name := workflowAccountName(origin); name != "" {
		this["account_name"] = name
	}
	out := map[string]any{"this_account": this}

	var notes []string
	if len(groups) > 0 {
		shown := 0
		for _, g := range groups {
			shown += len(g.Workflows)
		}
		out["other_accounts"] = groups
		if ownRows, _ := this["workflows"].([]any); len(ownRows) == 0 {
			notes = append(notes, fmt.Sprintf("Nothing in this conversation's account matched; %d automation(s) in other accounts did.", shown))
		}
		notes = append(notes, otherAccountsNote)
		if otherTotal > shown {
			notes = append(notes, fmt.Sprintf("Other accounts: showing %d of %d; narrow with query (workflow_search) or name (workflow_list) to see the rest.", shown, otherTotal))
		}
	}
	if problem != "" {
		notes = append(notes, problem)
	}
	if len(notes) > 0 {
		out["note"] = strings.Join(notes, " ")
	}
	b, err := marshalNoHTMLEscape(out, "")
	if err != nil {
		return own
	}
	return b
}
