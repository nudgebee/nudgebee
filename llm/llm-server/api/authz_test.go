package api

import (
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

const (
	authzAcctA   = "11111111-1111-1111-1111-111111111111"
	authzAcctB   = "22222222-2222-2222-2222-222222222222"
	authzForeign = "99999999-9999-9999-9999-999999999999"
)

func authzContext(t *testing.T, wire string) *security.SecurityContext {
	t.Helper()
	var sc security.SecurityContext
	if err := common.UnmarshalJson([]byte(wire), &sc); err != nil {
		t.Fatalf("unmarshal security context: %v", err)
	}
	return &sc
}

// The grant half of every llm-server gate. Built-in roles are unaffected by
// construction (call sites OR these in), so what matters is that a grant admits
// exactly its own module and class, on exactly the accounts it covers.
func TestGrantHelpers(t *testing.T) {
	kbWriter := authzContext(t, `{
		"TenantId":"t1","UserId":"u1","Roles":[],
		"AccountIds":["`+authzAcctA+`","`+authzAcctB+`"],
		"CustomPermissions":{"ai_kbs:Write":true}}`)

	if !grantedWrite(kbWriter, authzAcctA, moduleAiKbs) {
		t.Error("ai_kbs:Write should authorize a knowledge-base write")
	}
	if !granted(kbWriter, authzAcctA, moduleAiKbs, "Read", "Write") {
		t.Error("Write implies Read, as at the gateway")
	}
	if !grantedRun(kbWriter, authzAcctA, moduleAiKbs) {
		t.Error("ai_kbs:Write should authorize a sync (Execute class)")
	}
	// Module isolation: a KB grant is not an agents/tools/RCA grant.
	for _, module := range []string{moduleAiAgents, moduleAiTools, moduleAiRca, moduleAiMisc, moduleAiGeneration} {
		if grantedWrite(kbWriter, authzAcctA, module) {
			t.Errorf("ai_kbs:Write must not authorize writes on %s", module)
		}
	}
	// Tenant boundary: a tenant-global grant stops at the tenant's accounts.
	if grantedWrite(kbWriter, authzForeign, moduleAiKbs) {
		t.Error("a tenant-global grant must not reach an account outside the tenant")
	}

	// Execute-only: may run, may not mutate.
	runner := authzContext(t, `{
		"TenantId":"t1","UserId":"u2","Roles":[],
		"AccountIds":["`+authzAcctA+`"],
		"CustomPermissions":{"ai_generation:Execute":true}}`)
	if !grantedRun(runner, authzAcctA, moduleAiGeneration) {
		t.Error("ai_generation:Execute should authorize query generation")
	}
	if grantedWrite(runner, authzAcctA, moduleAiGeneration) {
		t.Error("ai_generation:Execute must NOT authorize a write")
	}

	// Read-only: neither write nor run.
	reader := authzContext(t, `{
		"TenantId":"t1","UserId":"u3","Roles":[],
		"AccountIds":["`+authzAcctA+`"],
		"CustomPermissions":{"ai_conversations:Read":true}}`)
	if !granted(reader, authzAcctA, moduleAiConversations, "Read", "Write") {
		t.Error("ai_conversations:Read should authorize a conversation read")
	}
	if grantedWrite(reader, authzAcctA, moduleAiConversations) || grantedRun(reader, authzAcctA, moduleAiConversations) {
		t.Error("ai_conversations:Read must NOT authorize write or run")
	}

	// Account-scoped grant applies only where it is bound.
	scoped := authzContext(t, `{
		"TenantId":"t1","UserId":"u4","Roles":[],
		"AccountIds":["`+authzAcctA+`","`+authzAcctB+`"],
		"ScopedCustomPermissions":{"`+authzAcctA+`":{"ai_tools:Write":true}}}`)
	if !grantedWrite(scoped, authzAcctA, moduleAiTools) {
		t.Error("account-scoped grant should authorize its own account")
	}
	if grantedWrite(scoped, authzAcctB, moduleAiTools) {
		t.Error("account-scoped grant must not leak to another account")
	}

	// No grants: every helper is false, so each call site collapses to the
	// built-in HasAccountAccess check it always had.
	plain := authzContext(t, `{"TenantId":"t1","UserId":"u5","Roles":["account_admin"],
		"AccountIds":["`+authzAcctA+`"]}`)
	for _, module := range []string{moduleAiKbs, moduleAiAgents, moduleAiTools, moduleAiFunctions,
		moduleAiGuardrails, moduleAiMemory, moduleAiMisc, moduleAiRca, moduleAiGeneration, moduleAiConversations} {
		if granted(plain, authzAcctA, module, "Read", "Write", "Execute") {
			t.Errorf("a user with no custom grants must get nothing from %s", module)
		}
	}
}
