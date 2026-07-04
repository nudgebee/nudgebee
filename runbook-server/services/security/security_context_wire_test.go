package security

import (
	"testing"

	"nudgebee/runbook/common"
)

// TestSecurityContextWire_ScopedEntityIds guards the cross-service wire
// contract with api-server: the SecurityContext JSON it produces carries
// scoped accounts under "ScopedEntityIds" (a map keyed by role). If this
// struct drifts back to flat per-role slices, account-scoped users silently
// lose access (HasAccountAccess returns false) — e.g. "failed to list
// workflows" for account_admin.
func TestSecurityContextWire_ScopedEntityIds(t *testing.T) {
	const acctA = "11111111-1111-1111-1111-111111111111"
	const acctB = "22222222-2222-2222-2222-222222222222"

	// Wire payload as api-server's SecurityContext.MarshalJSON emits it:
	// account_admin scoped to acctA, account_admin_readonly scoped to acctB.
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

	// account_admin → read+write on its scoped account.
	if !sc.HasAccountAccess(acctA, SecurityAccessTypeRead) {
		t.Errorf("account_admin should have read access to scoped account A")
	}
	if !sc.HasAccountAccess(acctA, SecurityAccessTypeUpdate) {
		t.Errorf("account_admin should have write access to scoped account A")
	}

	// account_admin_readonly → read only on its scoped account.
	if !sc.HasAccountAccess(acctB, SecurityAccessTypeRead) {
		t.Errorf("account_admin_readonly should have read access to scoped account B")
	}
	if sc.HasAccountAccess(acctB, SecurityAccessTypeUpdate) {
		t.Errorf("account_admin_readonly must NOT have write access to account B")
	}

	// ListAccountIds returns the union of both scoped roles' accounts, in
	// role-iteration order (account_admin before account_admin_readonly).
	ids := sc.ListAccountIds()
	if len(ids) != 2 {
		t.Fatalf("ListAccountIds expected 2 accounts (union), got %d: %v", len(ids), ids)
	}
	if ids[0] != acctA || ids[1] != acctB {
		t.Errorf("ListAccountIds expected [%s, %s], got %v", acctA, acctB, ids)
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

// TestSecurityContextWire_MissingScopedEntityIds ensures a payload with no
// ScopedEntityIds (nil map) fails closed — no panic, and a scoped role is
// denied rather than silently granted.
func TestSecurityContextWire_MissingScopedEntityIds(t *testing.T) {
	const acctA = "11111111-1111-1111-1111-111111111111"

	wire := []byte(`{
		"TenantId": "t1",
		"UserId": "u1",
		"AccountIds": ["` + acctA + `"],
		"Roles": ["account_admin"]
	}`)

	var sc SecurityContext
	if err := common.UnmarshalJson(wire, &sc); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if sc.HasAccountAccess(acctA, SecurityAccessTypeRead) {
		t.Errorf("expected no access when ScopedEntityIds is missing (fail closed)")
	}
	if len(sc.ListAccountIds()) != 0 {
		t.Errorf("expected empty account IDs when ScopedEntityIds is missing")
	}
}
