package security

import (
	"strings"
	"testing"

	"nudgebee/services/common"
)

// api-server is the authority that EMITS the SecurityContext JSON consumed by
// llm-server and runbook-server (via /v1/authz/get_security_context). These
// tests pin the wire field NAMES it produces — the golden contract. A rename
// here silently 403s account-scoped users downstream (HasAccountAccess false)
// or drops custom grants (HasPermission false). Keep the field-name set in
// lockstep with llm/runbook scPub structs + their wire tests.
func TestSecurityContextWire_GoldenFieldNames(t *testing.T) {
	const acctA = "11111111-1111-1111-1111-111111111111"

	wire := []byte(`{
		"TenantId": "t1",
		"UserId": "u1",
		"AccountIds": ["` + acctA + `"],
		"Roles": ["account_admin"],
		"ScopedEntityIds": {"account_admin": ["` + acctA + `"]},
		"CustomPermissions": {"notifications:Write": true}
	}`)

	var sc SecurityContext
	if err := common.UnmarshalJson(wire, &sc); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	out, err := sc.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	emitted := string(out)
	// The canonical wire field names the downstream services parse.
	for _, field := range []string{
		"TenantId", "UserId", "AccountIds", "Roles",
		"ScopedEntityIds", "K8sUser", "K8sGroup", "K8sNamespaces",
		"CustomPermissions", "IsServerInternal",
	} {
		if !strings.Contains(emitted, `"`+field+`"`) {
			t.Errorf("emitted wire JSON missing field %q (downstream drift): %s", field, emitted)
		}
	}
}

// TestSecurityContextWire_ScopedEntityIds is the round-trip behavior guard,
// mirrored across all three v2 services.
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

	if !sc.HasAccountAccess(acctA, SecurityAccessTypeUpdate) {
		t.Errorf("account_admin should have write access to scoped account A")
	}
	if sc.HasAccountAccess(acctB, SecurityAccessTypeUpdate) {
		t.Errorf("account_admin_readonly must NOT have write access to account B")
	}
	ids := sc.ListAccountIds()
	if len(ids) != 2 || ids[0] != acctA || ids[1] != acctB {
		t.Errorf("ListAccountIds expected [%s, %s] in role order, got %v", acctA, acctB, ids)
	}
}

// TestSecurityContextWire_CustomPermissions guards the dynamic-RBAC grant field.
func TestSecurityContextWire_CustomPermissions(t *testing.T) {
	wire := []byte(`{
		"TenantId": "t1",
		"UserId": "u1",
		"Roles": [],
		"CustomPermissions": {"notifications:Write": true, "workflows:Read": true}
	}`)

	var sc SecurityContext
	if err := common.UnmarshalJson(wire, &sc); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if !sc.HasPermission("notifications", "Write") || !sc.HasPermission("workflows", "Read") {
		t.Errorf("expected granted notifications:Write and workflows:Read")
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

// TestSecurityContextWire_ScopedCustomPermissions guards the account-scoped
// grant field (ScopedCustomPermissions: accountId -> "<module>:<class>" -> bool)
// that carries per-account custom-role grants across the cross-service wire.
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

// TestScopedAccountIdsForModule pins the account set the query engine uses to
// restrict a scoped-grant read (V798 scope-on-grant). Write implies Read;
// tenant-global grants are NOT returned here (they authorize a tenant-wide read
// via a different branch), and an unrelated module returns nothing.
func TestScopedAccountIdsForModule(t *testing.T) {
	wire := []byte(`{
		"TenantId": "t1",
		"UserId": "u1",
		"Roles": [],
		"CustomPermissions": { "events:Read": true },
		"ScopedCustomPermissions": {
			"acct-A": { "events:Read": true },
			"acct-B": { "events:Write": true, "tickets:Read": true }
		}
	}`)

	var sc SecurityContext
	if err := common.UnmarshalJson(wire, &sc); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	got := sc.ScopedAccountIdsForModule("events")
	want := map[string]bool{"acct-A": true, "acct-B": true} // Write implies Read
	if len(got) != len(want) {
		t.Fatalf("events: got %v, want keys %v", got, want)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("events: unexpected account %q", id)
		}
	}

	if ids := sc.ScopedAccountIdsForModule("tickets"); len(ids) != 1 || ids[0] != "acct-B" {
		t.Errorf("tickets: got %v, want [acct-B]", ids)
	}
	// A module with no scoped grant returns nothing — even though the caller has a
	// tenant-global events:Read (that path is handled by the tenant-wide branch,
	// not this per-account restriction).
	if ids := sc.ScopedAccountIdsForModule("recommendations"); len(ids) != 0 {
		t.Errorf("recommendations: expected no scoped accounts, got %v", ids)
	}
}

// TestGetScopedPermissionKeys pins the deduped union of scoped grant keys baked
// into the session (prefixed by the gateway). The account dimension is dropped
// (gateway = may-run; the query engine enforces the account); tenant-global
// grants are NOT included (GetCustomPermissions returns those).
func TestGetScopedPermissionKeys(t *testing.T) {
	wire := []byte(`{
		"TenantId": "t1",
		"UserId": "u1",
		"Roles": [],
		"CustomPermissions": { "notifications:Write": true },
		"ScopedCustomPermissions": {
			"acct-A": { "events:Read": true },
			"acct-B": { "events:Read": true, "tickets:Write": true }
		}
	}`)

	var sc SecurityContext
	if err := common.UnmarshalJson(wire, &sc); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	keys := sc.GetScopedPermissionKeys()
	got := map[string]bool{}
	for _, k := range keys {
		if got[k] {
			t.Errorf("duplicate scoped key %q (union must dedupe across accounts)", k)
		}
		got[k] = true
	}
	want := map[string]bool{"events:Read": true, "tickets:Write": true}
	if len(got) != len(want) {
		t.Fatalf("scoped keys = %v, want %v", keys, want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing scoped key %q", k)
		}
	}
	if got["notifications:Write"] {
		t.Errorf("tenant-global grant must not appear in scoped keys")
	}
}
