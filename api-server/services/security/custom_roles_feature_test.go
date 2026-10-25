package security

import (
	"errors"
	"nudgebee/services/internal/database"
	"testing"

	"github.com/lib/pq"
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

// Custom roles apply to every tenant: no per-tenant flag is read, so the
// answer needs no database at all; only the kill switch turns them off.
func TestCustomRolesEnabled_OnForEveryTenant(t *testing.T) {
	t.Setenv(EnvDisableCustomRoles, "")
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		t.Fatal("CustomRolesEnabled must not read the database")
		return nil, errors.New("unreachable")
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })
	if !CustomRolesEnabled("11111111-1111-1111-1111-111111111111") {
		t.Fatal("expected custom roles on for a tenant")
	}
}

// With custom roles on for every tenant, a database whose custom-role tables are not
// there yet (an upgrade whose migration Job has not run) resolves no grants
// instead of failing every security context.
func TestResolveCustomGrants_MissingTablesMeanNoGrants(t *testing.T) {
	db, mock, err := mockPg()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	mock.ExpectQuery("select crp.module").WillReturnError(&pq.Error{Code: "42P01", Message: `relation "custom_role_permissions" does not exist`})
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatal(err)
	}
	perms, scoped, err := resolveCustomGrants(dbms, "t1", "u1")
	if err != nil || len(perms) != 0 || len(scoped) != 0 {
		t.Fatalf("got perms=%v scoped=%v err=%v, want no grants and no error", perms, scoped, err)
	}

	mock.ExpectQuery("select crp.module").WillReturnError(errors.New("connection refused"))
	if _, _, err := resolveCustomGrants(dbms, "t1", "u1"); err == nil {
		t.Fatal("any other error must still fail the resolve")
	}
}
