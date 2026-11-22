package tenant

import (
	"os"
	"sort"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #43747: an admin could add anyone with a login, from any tenant, to one of
// their groups by typing the email, and "User not found" versus success told
// them whether the email had an account.

const (
	mguTenant = "00000000-0000-0000-0000-0000000043a1"
	mguGroup  = "00000000-0000-0000-0000-0000000043a2"
	mguAdmin  = "00000000-0000-0000-0000-0000000043a3"
)

// The package mock registers as driver "postgresql", which sqlx does not map to
// $n placeholders, so these statements reach it with sqlx.In's "?" markers.
const (
	mockGroupAddLookup    = `SELECT u.id, u.username FROM users u JOIN tenant_users tu ON tu."user" = u.id AND tu.tenant = ? WHERE u.username IN (?)`
	mockGroupRemoveLookup = `SELECT u.id, u.username FROM users u JOIN usergroup_users ugu ON ugu."user" = u.id AND ugu."group" = ? WHERE u.username IN (?)`
)

func expectGroupInTenant() {
	ffMockDB.ExpectQuery("SELECT count(*) FROM user_groups WHERE tenant = $1 AND id = $2").
		WithArgs(mguTenant, mguGroup).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
}

func TestManageGroupUsers_AddRefusesSomeoneOutsideTheTenant(t *testing.T) {
	expectGroupInTenant()
	// The lookup is scoped to the caller's tenant; a login that exists only in
	// another tenant comes back as no row, exactly like an unknown email.
	ffMockDB.ExpectQuery(mockGroupAddLookup).
		WithArgs(mguTenant, "someone@othercompany.com").
		WillReturnRows(sqlmock.NewRows([]string{"id", "username"}))

	_, err := ManageGroupUsers(tenantAdminCtx(t, mguTenant, mguAdmin), ManageGroupUsersRequest{
		GroupId:      mguGroup,
		AddUsernames: []string{"someone@othercompany.com"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a member of this organization: someone@othercompany.com")
	assert.NotContains(t, err.Error(), "not found", "the old wording told the caller the email has no account")
	// No INSERT was expected, so reaching one would have failed with a different error.
	require.NoError(t, ffMockDB.ExpectationsWereMet())
}

func TestManageGroupUsers_AddResolvesATenantMemberAndInsertsThem(t *testing.T) {
	const memberId = "00000000-0000-0000-0000-0000000043b1"
	expectGroupInTenant()
	ffMockDB.ExpectQuery(mockGroupAddLookup).
		WithArgs(mguTenant, "member@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id", "username"}).AddRow(memberId, "member@example.com"))
	// Fail the insert so the call stops before the async audit write, which
	// would otherwise race the next test for this shared mock.
	ffMockDB.ExpectExec(`INSERT INTO usergroup_users ("group", "user", "created_by", "updated_by") VALUES (?, ?, ?, ?) ON CONFLICT ("user", "group") DO NOTHING`).
		WithArgs(mguGroup, memberId, mguAdmin, mguAdmin).
		WillReturnError(assert.AnError)

	_, err := ManageGroupUsers(tenantAdminCtx(t, mguTenant, mguAdmin), ManageGroupUsersRequest{
		GroupId:      mguGroup,
		AddUsernames: []string{"member@example.com"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Error adding users to group")
	require.NoError(t, ffMockDB.ExpectationsWereMet())
}

func TestManageGroupUsers_RemovingSomeoneNotInTheGroupIsANoOp(t *testing.T) {
	expectGroupInTenant()
	ffMockDB.ExpectQuery(mockGroupRemoveLookup).
		WithArgs(mguGroup, "nobody@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id", "username"}))

	resp, err := ManageGroupUsers(tenantAdminCtx(t, mguTenant, mguAdmin), ManageGroupUsersRequest{
		GroupId:         mguGroup,
		RemoveUsernames: []string{"nobody@example.com"},
	})
	require.NoError(t, err, "a remove must not answer differently for an email with no account")
	assert.Equal(t, "success", resp.Status)
	// No DELETE was expected: nothing to remove.
	require.NoError(t, ffMockDB.ExpectationsWereMet())
}

// TestManageGroupUsersLookups_DB runs the two lookups against a real Postgres:
// what decides who can be added or removed is the join, which sqlmock cannot
// evaluate. Builds its own tables in a throwaway schema. DB-gated: skips when no
// database is reachable.
func TestManageGroupUsersLookups_DB(t *testing.T) {
	url := os.Getenv("APP_DATABASE_URL")
	if url == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatal("REQUIRE_DB_TESTS is set but APP_DATABASE_URL is empty")
		}
		t.Skip("skipping: APP_DATABASE_URL is not set")
	}
	db, err := sqlx.Connect("postgres", url)
	if err != nil {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatalf("REQUIRE_DB_TESTS is set but the database is not accessible: %v", err)
		}
		t.Skipf("skipping: database not accessible: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	const schema = "zz_manage_group_users_test"
	mustExec := func(q string, args ...any) {
		_, e := db.Exec(q, args...)
		require.NoError(t, e, q)
	}
	drop := func() { _, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`) }
	drop()
	t.Cleanup(drop)
	mustExec(`CREATE SCHEMA ` + schema)
	mustExec(`SET search_path TO ` + schema)
	mustExec(`CREATE TABLE users (id uuid PRIMARY KEY, username text UNIQUE NOT NULL)`)
	mustExec(`CREATE TABLE tenant_users (tenant uuid NOT NULL, "user" uuid NOT NULL, UNIQUE (tenant, "user"))`)
	mustExec(`CREATE TABLE usergroup_users ("group" uuid NOT NULL, "user" uuid NOT NULL, UNIQUE ("user", "group"))`)

	const (
		tenantA = "00000000-0000-0000-0000-00000000aaaa"
		tenantB = "00000000-0000-0000-0000-00000000bbbb"
		groupA  = "00000000-0000-0000-0000-00000000a001"
		// member belongs to tenant A; outsider only to tenant B; planted only to
		// tenant B but was put in group A before the fix.
		member   = "00000000-0000-0000-0000-000000000001"
		outsider = "00000000-0000-0000-0000-000000000002"
		planted  = "00000000-0000-0000-0000-000000000003"
	)
	mustExec(`INSERT INTO users VALUES ($1, 'member@a.com'), ($2, 'outsider@b.com'), ($3, 'planted@b.com')`, member, outsider, planted)
	mustExec(`INSERT INTO tenant_users VALUES ($1, $2), ($3, $4), ($3, $5)`, tenantA, member, tenantB, outsider, planted)
	mustExec(`INSERT INTO usergroup_users VALUES ($1, $2)`, groupA, planted)

	lookup := func(query string, scope string, usernames ...string) []string {
		q, args, err := sqlx.In(query, scope, usernames)
		require.NoError(t, err)
		var rows []struct {
			Id       string `db:"id"`
			Username string `db:"username"`
		}
		require.NoError(t, db.Select(&rows, db.Rebind(q), args...))
		out := []string{}
		for _, r := range rows {
			out = append(out, r.Username)
		}
		sort.Strings(out)
		return out
	}

	// Add: only tenant A's members resolve. The outsider and an unknown email
	// are indistinguishable — both simply do not come back.
	assert.Equal(t, []string{"member@a.com"},
		lookup(groupAddLookupQuery, tenantA, "member@a.com", "outsider@b.com", "nobody@nowhere.com"))

	// Remove: only the group's current members resolve, so the member planted
	// from tenant B can still be taken out, and anyone else is a no-op.
	assert.Equal(t, []string{"planted@b.com"},
		lookup(groupRemoveLookupQuery, groupA, "planted@b.com", "member@a.com", "outsider@b.com", "nobody@nowhere.com"))
}
