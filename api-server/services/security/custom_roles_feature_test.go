package security

import (
	"testing"
)

// The deployment-level kill switch must be answerable without a database — it is
// the escape hatch used when the metastore (or the flag rows themselves) are the
// problem, so it has to short-circuit before any query is attempted.
func TestCustomRolesEnabled_EnvKillSwitch(t *testing.T) {
	for _, value := range []string{"true", "TRUE", "True"} {
		t.Setenv(EnvDisableCustomRoles, value)
		if CustomRolesEnabled("11111111-1111-1111-1111-111111111111") {
			t.Fatalf("expected custom roles disabled with %s=%q", EnvDisableCustomRoles, value)
		}
	}
}

// An empty tenant id can never carry a per-tenant flag, so it resolves to off
// without touching the database (NewSecurityContextForSuperAdmin-style synthetic
// contexts have no tenant).
func TestCustomRolesEnabled_NoTenant(t *testing.T) {
	t.Setenv(EnvDisableCustomRoles, "")
	if CustomRolesEnabled("") {
		t.Fatal("expected custom roles disabled for an empty tenant id")
	}
}

// With no grants resolved (the state every tenant is in while the feature is
// off), the gate helpers must reduce to exactly their built-in-role behavior.
// This is the property the whole plug-and-play design rests on.
func TestGatesReduceToBuiltInRolesWithoutGrants(t *testing.T) {
	const acct = "22222222-2222-2222-2222-222222222222"

	admin := SecurityContext{tenantId: "t1", userId: "u1", roles: []string{AUTH_TENANT_ADMIN_ROLE}, accountIds: []string{acct}}
	if !admin.CanManage("notifications", "Write") {
		t.Fatal("tenant admin must still manage tenant config with no custom grants")
	}
	if !admin.CanReadAccountData(acct, "traces") {
		t.Fatal("tenant admin must still read account data with no custom grants")
	}

	accountRead := SecurityContext{
		tenantId:        "t1",
		userId:          "u2",
		roles:           []string{AUTH_ACCOUNT_READ_ADMIN_ROLE},
		accountIds:      []string{acct},
		scopedEntityIds: map[string][]string{AUTH_ACCOUNT_READ_ADMIN_ROLE: {acct}},
	}
	if accountRead.CanManage("notifications", "Write") {
		t.Fatal("a read-only account role must not manage tenant config")
	}
	if !accountRead.CanReadAccountData(acct, "traces") {
		t.Fatal("a read-only account role must keep reading its own account")
	}
	if accountRead.HasPermission("traces", "Read") {
		t.Fatal("HasPermission must be false with no resolved grants")
	}
	if len(accountRead.ScopedAccountIdsForModule("traces")) != 0 {
		t.Fatal("ScopedAccountIdsForModule must be empty with no resolved grants")
	}
	if len(accountRead.GetCustomPermissions()) != 0 || len(accountRead.GetScopedPermissionKeys()) != 0 {
		t.Fatal("no grant keys may be baked into the session with no resolved grants")
	}
}
