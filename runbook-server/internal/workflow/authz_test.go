package workflow

import (
	"testing"

	"nudgebee/runbook/common"
	"nudgebee/runbook/services/security"
)

const (
	acctA   = "11111111-1111-1111-1111-111111111111"
	acctB   = "22222222-2222-2222-2222-222222222222"
	foreign = "99999999-9999-9999-9999-999999999999"
)

func scFromWire(t *testing.T, wire string) *security.SecurityContext {
	t.Helper()
	var sc security.SecurityContext
	if err := common.UnmarshalJson([]byte(wire), &sc); err != nil {
		t.Fatalf("unmarshal security context: %v", err)
	}
	return &sc
}

// The grant half of every workflow gate. Built-in roles are unaffected by these
// helpers by construction (call sites OR them in), so what matters is that a
// grant admits exactly the classes the gateway admitted it for, on exactly the
// accounts it covers.
func TestWorkflowGrantHelpers(t *testing.T) {
	// A tenant-global workflows:Write grant, no built-in role at all.
	writer := scFromWire(t, `{
		"TenantId":"t1","UserId":"u1","Roles":[],
		"AccountIds":["`+acctA+`","`+acctB+`"],
		"CustomPermissions":{"workflows:Write":true}}`)

	for _, acct := range []string{acctA, acctB} {
		if !canWriteWorkflows(writer, acct) {
			t.Errorf("workflows:Write should authorize writes on %s", acct)
		}
		if !canRunWorkflows(writer, acct) {
			t.Errorf("workflows:Write should authorize running on %s (Write implies Execute here)", acct)
		}
		if !canInspectWorkflows(writer, acct) {
			t.Errorf("workflows:Write should authorize validate on %s", acct)
		}
	}
	// A tenant-global grant stops at the tenant boundary.
	if canWriteWorkflows(writer, foreign) {
		t.Error("a tenant-global grant must not reach an account outside the tenant")
	}

	// Execute-only: may run, may not rewrite.
	runner := scFromWire(t, `{
		"TenantId":"t1","UserId":"u2","Roles":[],
		"AccountIds":["`+acctA+`"],
		"CustomPermissions":{"workflows:Execute":true}}`)
	if !canRunWorkflows(runner, acctA) {
		t.Error("workflows:Execute should authorize running")
	}
	if canWriteWorkflows(runner, acctA) {
		t.Error("workflows:Execute must NOT authorize a write")
	}

	// Read-only: may validate, nothing else.
	reader := scFromWire(t, `{
		"TenantId":"t1","UserId":"u3","Roles":[],
		"AccountIds":["`+acctA+`"],
		"CustomPermissions":{"workflows:Read":true}}`)
	if !canInspectWorkflows(reader, acctA) {
		t.Error("workflows:Read should authorize validate/check")
	}
	if canWriteWorkflows(reader, acctA) || canRunWorkflows(reader, acctA) {
		t.Error("workflows:Read must NOT authorize write or run")
	}

	// Account-scoped grant: only where it is bound.
	scoped := scFromWire(t, `{
		"TenantId":"t1","UserId":"u4","Roles":[],
		"AccountIds":["`+acctA+`","`+acctB+`"],
		"ScopedCustomPermissions":{"`+acctA+`":{"workflows:Write":true}}}`)
	if !canWriteWorkflows(scoped, acctA) {
		t.Error("account-scoped grant should authorize its own account")
	}
	if canWriteWorkflows(scoped, acctB) {
		t.Error("account-scoped grant must not leak to another account")
	}

	// No grants: every helper is false, so each call site collapses to the
	// built-in HasAccountAccess check it always had.
	plain := scFromWire(t, `{"TenantId":"t1","UserId":"u5","Roles":["account_admin"],"AccountIds":["`+acctA+`"]}`)
	if canWriteWorkflows(plain, acctA) || canRunWorkflows(plain, acctA) || canInspectWorkflows(plain, acctA) {
		t.Error("a user with no custom grants must get nothing from the grant helpers")
	}
	// An unrelated module's grant does not bleed into workflows.
	other := scFromWire(t, `{"TenantId":"t1","UserId":"u6","Roles":[],"AccountIds":["`+acctA+`"],
		"CustomPermissions":{"events:Write":true}}`)
	if canWriteWorkflows(other, acctA) || canRunWorkflows(other, acctA) {
		t.Error("events:Write must not authorize workflow operations")
	}
}
