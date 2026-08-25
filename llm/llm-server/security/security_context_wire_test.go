package security

import (
	"testing"

	"nudgebee/llm/common"
)

// TestSecurityContextWire_ScopedEntityIds guards the cross-service wire
// contract with api-server: the SecurityContext JSON it produces carries
// scoped accounts under "ScopedEntityIds" (a map keyed by role). If this
// struct drifts back to flat per-role slices, account-scoped users silently
// lose access (HasAccountAccess returns false). llm-server fetches this context
// from api-server (loadSecurityContextFromServicesServer), so it is directly on
// the receive path for this contract.
func TestSecurityContextWire_ScopedEntityIds(t *testing.T) {
	const acctA = "11111111-1111-1111-1111-111111111111"
	const acctB = "22222222-2222-2222-2222-222222222222"

	wire := []byte(`{
		"TenantId": "t1",
		"UserId": "u1",
		"AccountIds": ["` + acctA + `", "` + acctB + `"],
		"Roles": ["account_admin", "account_admin_readonly"],
		"ScopedEntityIds": {
			"account_admin": ["` + acctA + `"],
			"account_admin_readonly": ["` + acctB + `"]
		}
	}`)

	var sc SecurityContext
	if err := common.UnmarshalJson(wire, &sc); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if !sc.HasAccountAccess(acctA, SecurityAccessTypeRead) {
		t.Errorf("account_admin should have read access to scoped account A")
	}
	if !sc.HasAccountAccess(acctA, SecurityAccessTypeUpdate) {
		t.Errorf("account_admin should have write access to scoped account A")
	}
	if !sc.HasAccountAccess(acctB, SecurityAccessTypeRead) {
		t.Errorf("account_admin_readonly should have read access to scoped account B")
	}
	if sc.HasAccountAccess(acctB, SecurityAccessTypeUpdate) {
		t.Errorf("account_admin_readonly must NOT have write access to account B")
	}

	// Round-trip back out must keep ScopedEntityIds intact.
	out, err := sc.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var sc2 SecurityContext
	if err := common.UnmarshalJson(out, &sc2); err != nil {
		t.Fatalf("re-unmarshal failed: %v", err)
	}
	if !sc2.HasAccountAccess(acctA, SecurityAccessTypeRead) {
		t.Errorf("round-tripped context lost account_admin scope")
	}
}

// TestSecurityContextWire_CustomPermissions guards the dynamic-RBAC grant field
// on the wire. api-server emits custom-role grants under "CustomPermissions" (a
// "<module>:<class>" set). If this struct drops the field, every custom grant
// is silently lost on the wire and HasPermission / CanManage fail closed for
// grant-only access — the drift this change fixes (llm-server previously lacked
// the field entirely).
func TestSecurityContextWire_CustomPermissions(t *testing.T) {
	wire := []byte(`{
		"TenantId": "t1",
		"UserId": "u1",
		"Roles": [],
		"CustomPermissions": {
			"notifications:Write": true,
			"workflows:Read": true
		}
	}`)

	var sc SecurityContext
	if err := common.UnmarshalJson(wire, &sc); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if !sc.HasPermission("notifications", "Write") {
		t.Errorf("expected granted notifications:Write")
	}
	if !sc.HasPermission("workflows", "Read") {
		t.Errorf("expected granted workflows:Read")
	}
	if sc.HasPermission("notifications", "Read") {
		t.Errorf("notifications:Read was not granted; must be false")
	}
	if !sc.CanManage("notifications", "Write") {
		t.Errorf("CanManage should accept the notifications:Write grant")
	}

	out, err := sc.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var sc2 SecurityContext
	if err := common.UnmarshalJson(out, &sc2); err != nil {
		t.Fatalf("re-unmarshal failed: %v", err)
	}
	if !sc2.HasPermission("notifications", "Write") {
		t.Errorf("round-tripped context lost CustomPermissions grant")
	}
}

// TestSecurityContextWire_MissingCustomPermissions ensures a payload with no
// CustomPermissions (nil map) fails closed — no panic, no grant.
func TestSecurityContextWire_MissingCustomPermissions(t *testing.T) {
	wire := []byte(`{"TenantId": "t1", "UserId": "u1", "Roles": []}`)

	var sc SecurityContext
	if err := common.UnmarshalJson(wire, &sc); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if sc.HasPermission("notifications", "Write") {
		t.Errorf("expected no grant when CustomPermissions is missing (fail closed)")
	}
}

// TestSecurityContextWire_ScopedCustomPermissions guards the account-scoped
// grant field (ScopedCustomPermissions: accountId -> "<module>:<class>" -> bool)
// on the cross-service wire contract.
func TestSecurityContextWire_ScopedCustomPermissions(t *testing.T) {
	wire := []byte(`{
		"TenantId": "t1",
		"UserId": "u1",
		"Roles": [],
		"AccountIds": ["acct-A", "acct-B"],
		"CustomPermissions": { "notifications:Write": true },
		"ScopedCustomPermissions": { "acct-A": { "workflows:Write": true } }
	}`)

	var sc SecurityContext
	if err := common.UnmarshalJson(wire, &sc); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if !sc.HasScopedPermission("acct-A", "workflows", "Write") {
		t.Errorf("expected workflows:Write scoped to acct-A")
	}
	if sc.HasScopedPermission("acct-B", "workflows", "Write") {
		t.Errorf("workflows:Write must NOT apply to acct-B")
	}
	if !sc.HasScopedPermission("acct-B", "notifications", "Write") {
		t.Errorf("tenant-global notifications:Write should apply to any account IN THE TENANT")
	}
	// A tenant-global grant is bounded by tenant membership: an account id that is
	// not in AccountIds (e.g. another tenant's, passed in a crafted request) must
	// not be authorized by it.
	if sc.HasScopedPermission("acct-foreign", "notifications", "Write") {
		t.Errorf("tenant-global grant must NOT apply to an account outside the tenant")
	}
	if sc.HasPermission("workflows", "Write") {
		t.Errorf("scoped grant must not register as tenant-global HasPermission")
	}

	out, err := sc.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var sc2 SecurityContext
	if err := common.UnmarshalJson(out, &sc2); err != nil {
		t.Fatalf("re-unmarshal failed: %v", err)
	}
	if !sc2.HasScopedPermission("acct-A", "workflows", "Write") {
		t.Errorf("round-trip lost ScopedCustomPermissions")
	}
	if sc2.HasScopedPermission("acct-B", "workflows", "Write") {
		t.Errorf("round-trip must not widen scope to acct-B")
	}
}

// Model-pricing read access is granted in app/src/lib/actions.yaml to
// tenant_admin, tenant_admin_readonly, account_admin and account_admin_readonly.
// The llm-server handler must accept the same set, or an account admin passes
// the gateway and is then refused by the service on a tab they can already see.
// Writing rates is deliberately narrower — tenant_admin only.
func TestPricingRolePredicates_MatchActionsYamlGrants(t *testing.T) {
	for _, tc := range []struct {
		role     string
		canRead  bool
		canWrite bool
	}{
		{AUTH_TENANT_ADMIN_ROLE, true, true},
		{AUTH_TENANT_READ_ADMIN_ROLE, true, false},
		{AUTH_ACCOUNT_ADMIN_ROLE, true, false},
		{AUTH_ACCOUNT_READ_ADMIN_ROLE, true, false},
		{AUTH_K8S_NAMESPACE_ADMIN_ROLE, false, false},
		{AUTH_TENANT_USAGE_ROLE, false, false},
		{AUTH_ACCOUNT_USAGE_ROLE, false, false},
	} {
		t.Run(tc.role, func(t *testing.T) {
			sc := &SecurityContext{roles: []string{tc.role}}

			// Mirrors the gate in api/conversation.go ai_list_model_pricing.
			read := sc.IsTenantAdmin() || sc.IsTenantReadAdmin() || sc.IsAccountAdmin() ||
				sc.IsAccountReadAdmin() || sc.IsSuperAdmin() || sc.IsSuperAdminReadonly()
			if read != tc.canRead {
				t.Errorf("read access for %s = %v, want %v", tc.role, read, tc.canRead)
			}

			// Mirrors the gate in ai_upsert_model_pricing.
			if write := sc.IsTenantAdmin(); write != tc.canWrite {
				t.Errorf("write access for %s = %v, want %v", tc.role, write, tc.canWrite)
			}
		})
	}
}
