package workflow

import (
	"context"
	"errors"
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
)

// Saving refuses an AI step aimed at an account outside the caller's tenant or
// one the caller cannot read. A custom-role grant counts as read access; a
// templated account is left to the run-time check.
func TestValidateAIStepAccount(t *testing.T) {
	const (
		hidden = "33333333-3333-3333-3333-333333333333"
		ghost  = "44444444-4444-4444-4444-444444444444"
	)
	tenants := map[string]string{acctA: "t1", acctB: "t1", hidden: "t1", foreign: "t2"}
	orig := tenantForAccount
	tenantForAccount = func(id string) (string, error) {
		if tenant, ok := tenants[id]; ok {
			return tenant, nil
		}
		return "", errors.New("account not found")
	}
	t.Cleanup(func() { tenantForAccount = orig })

	// account_admin on A and B; `hidden` is in the tenant but outside that scope.
	accountAdmin := accountAdminContext(t, []string{acctA, acctB, hidden}, []string{acctA, acctB})
	// No built-in role at all, only a tenant-global workflows:Read grant.
	granted := security.NewRequestContext(context.Background(), scFromWire(t, `{
		"TenantId":"t1","UserId":"u2","Roles":[],
		"AccountIds":["`+acctA+`","`+acctB+`","`+hidden+`"],
		"CustomPermissions":{"workflows:Read":true}}`), nil, nil, nil)

	step := func(taskType string, account any) model.Task {
		params := map[string]any{"message": "hi"}
		if account != nil {
			params["account_id"] = account
		}
		return model.Task{ID: "step", Type: taskType, Params: params}
	}

	for _, tc := range []struct {
		name    string
		ctx     *security.RequestContext
		task    model.Task
		wantErr string
	}{
		{"no account", accountAdmin, step("llm.nubi", nil), ""},
		{"empty account", accountAdmin, step("llm.nubi", ""), ""},
		{"the workflow's own account", accountAdmin, step("llm.nubi", acctA), ""},
		{"an account the caller can read", accountAdmin, step("llm.investigate", acctB), ""},
		{"an account the caller cannot read", accountAdmin, step("llm.investigate", hidden), hidden},
		{"an account in another tenant", accountAdmin, step("llm.mcp_call", foreign), foreign},
		{"an unknown account", accountAdmin, step("llm.mcp_call", ghost), ghost},
		{"a templated account is checked at run time", accountAdmin, step("llm.nubi", "{{ event.cloud_account_id }}"), ""},
		{"a statement-templated account is checked at run time", accountAdmin, step("llm.nubi", "{% if Inputs.dr %}"+hidden+"{% endif %}"), ""},
		{"a custom-role grant reads the account", granted, step("llm.nubi", hidden), ""},
		{"a custom-role grant stops at the tenant", granted, step("llm.nubi", foreign), foreign},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAIStepAccount(tc.ctx, acctA, tc.task)
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tc.wantErr)
			assert.ErrorContains(t, err, "not accessible")
		})
	}
}

// The check reaches a step wherever it sits, including the places
// validateTaskTypes does not descend into: router and legacy switch branches,
// and hook actions.
func TestValidateAIStepAccounts_Nested(t *testing.T) {
	const hidden = "33333333-3333-3333-3333-333333333333"
	orig := tenantForAccount
	tenantForAccount = func(string) (string, error) { return "t1", nil }
	t.Cleanup(func() { tenantForAccount = orig })
	ctx := accountAdminContext(t, []string{acctA, acctB, hidden}, []string{acctA, acctB})

	// A step as it arrives from JSON inside a container's params.
	embedded := func(account string) map[string]any {
		return map[string]any{"id": "inner", "type": "llm.nubi", "params": map[string]any{"message": "hi", "account_id": account}}
	}
	typed := func(account string) model.Task {
		return model.Task{ID: "inner", Type: "llm.investigate", Params: map[string]any{"message": "hi", "account_id": account}}
	}
	containers := map[string]func(account string) model.Task{
		"top level": typed,
		"group (Tasks)": func(account string) model.Task {
			return model.Task{ID: "outer", Type: "core.group", Tasks: []model.Task{typed(account)}}
		},
		"foreach (params.tasks)": func(account string) model.Task {
			return model.Task{ID: "outer", Type: "core.foreach", Params: map[string]any{"tasks": []any{embedded(account)}}}
		},
		"router branch": func(account string) model.Task {
			return model.Task{ID: "outer", Type: "llm.router", Params: map[string]any{
				"prompt":   "route",
				"branches": []any{map[string]any{"name": "b1", "tasks": []any{embedded(account)}}},
			}}
		},
		"legacy switch branch inside a group": func(account string) model.Task {
			return model.Task{ID: "outer", Type: "core.group", Tasks: []model.Task{{ID: "sw", Type: "core.switch", Params: map[string]any{
				"branches": []any{map[string]any{"condition": "true", "tasks": []any{embedded(account)}}},
			}}}}
		},
	}
	for name, build := range containers {
		t.Run(name, func(t *testing.T) {
			assert.NoError(t, validateAIStepAccounts(ctx, acctA, model.WorkflowDefinition{Tasks: []model.Task{build(acctB)}}))
			assert.ErrorContains(t, validateAIStepAccounts(ctx, acctA, model.WorkflowDefinition{Tasks: []model.Task{build(hidden)}}), hidden)
		})
	}

	hook := func(account string) *model.Hooks {
		return &model.Hooks{Always: []model.Action{{Type: "llm.investigate", Params: map[string]any{"message": "hi", "account_id": account}}}}
	}
	hooked := map[string]func(account string) model.WorkflowDefinition{
		"workflow hook": func(account string) model.WorkflowDefinition {
			return model.WorkflowDefinition{Hooks: hook(account)}
		},
		"task hook": func(account string) model.WorkflowDefinition {
			return model.WorkflowDefinition{Tasks: []model.Task{{ID: "t1", Type: "core.print", Hooks: hook(account)}}}
		},
	}
	for name, build := range hooked {
		t.Run(name, func(t *testing.T) {
			assert.NoError(t, validateAIStepAccounts(ctx, acctA, build(acctB)))
			assert.ErrorContains(t, validateAIStepAccounts(ctx, acctA, build(hidden)), hidden)
		})
	}

	// Other tasks keep the existing tenant-only rule; this check leaves them alone.
	kubectl := model.Task{ID: "k", Type: "k8s.cli", Params: map[string]any{"account_id": hidden, "command": "get pods"}}
	assert.NoError(t, validateAIStepAccounts(ctx, acctA, model.WorkflowDefinition{Tasks: []model.Task{kubectl}}))
}

// Only llm.mcp_call looks its integration up in the step's account; every other
// task keeps validating against the workflow's. A templated MCP account leaves
// nothing to validate against until the run.
func TestIntegrationLookupAccount(t *testing.T) {
	task := func(taskType, account string) model.Task {
		return model.Task{ID: "step", Type: taskType, Params: map[string]any{"account_id": account}}
	}
	for _, tc := range []struct {
		name      string
		task      model.Task
		want      string
		wantKnown bool
	}{
		{"mcp call with another account", task("llm.mcp_call", acctB), acctB, true},
		{"mcp call without an account", task("llm.mcp_call", ""), acctA, true},
		{"mcp call with a templated account", task("llm.mcp_call", "{{ event.cloud_account_id }}"), "", false},
		{"any other task", task("cicd.argocd.cli", acctB), acctA, true},
		{"any other task with a templated account", task("cicd.argocd.cli", "{{ event.cloud_account_id }}"), acctA, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, known := integrationLookupAccount(acctA, tc.task)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantKnown, known)
		})
	}
}
