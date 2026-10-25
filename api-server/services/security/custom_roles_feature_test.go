package security

import (
	"errors"
	"nudgebee/services/internal/database"
	"testing"

	"github.com/lib/pq"
)

// With no grants resolved (a tenant that assigns no custom role), the gate
// helpers must reduce to exactly their built-in-role behavior.
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

// Custom roles resolve for every tenant, so a database whose custom-role tables are not
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
