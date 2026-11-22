package query

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUsergroupUsersUserGroupsScopedToTenant_DB runs usergroup_users_v2 (the
// group members list) against a real Postgres. Each member row carries the
// member's groups; those must be the viewing tenant's groups only. Before
// #43747 the subquery matched on the user alone, so a tenant admin who put
// someone from another tenant in a group could read the names of that person's
// groups in every other tenant. Builds its own tables in a throwaway schema.
// DB-gated: skips when no database is reachable.
func TestUsergroupUsersUserGroupsScopedToTenant_DB(t *testing.T) {
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

	const schema = "zz_usergroup_users_tenant_scope_test"
	mustExec := func(q string, args ...any) {
		_, e := db.Exec(q, args...)
		require.NoError(t, e, q)
	}
	drop := func() { _, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`) }
	drop()
	t.Cleanup(drop)
	mustExec(`CREATE SCHEMA ` + schema)
	mustExec(`SET search_path TO ` + schema)
	mustExec(`CREATE TABLE users (id uuid PRIMARY KEY, display_name text, username text, status text)`)
	mustExec(`CREATE TABLE user_groups (id uuid PRIMARY KEY, tenant uuid NOT NULL, name text)`)
	mustExec(`CREATE TABLE usergroup_users (id uuid PRIMARY KEY, "group" uuid NOT NULL, "user" uuid NOT NULL)`)
	mustExec(`CREATE TABLE user_roles (id uuid PRIMARY KEY, role text, entity_type text, entity_id text, user_id uuid, tenant_id uuid)`)
	mustExec(`CREATE TABLE roles (value text PRIMARY KEY, display_name text)`)

	const (
		tenantA = "00000000-0000-0000-0000-00000000aaaa"
		tenantB = "00000000-0000-0000-0000-00000000bbbb"
		groupA1 = "00000000-0000-0000-0000-00000000a001"
		groupA2 = "00000000-0000-0000-0000-00000000a002"
		groupB1 = "00000000-0000-0000-0000-00000000b001"
		person  = "00000000-0000-0000-0000-000000000001"
	)
	mustExec(`INSERT INTO users VALUES ($1, 'Pat', 'pat@example.com', 'active')`, person)
	mustExec(`INSERT INTO user_groups VALUES ($1, $4, 'alpha-oncall'), ($2, $4, 'alpha-sre'), ($3, $5, 'beta-secret-project')`,
		groupA1, groupA2, groupB1, tenantA, tenantB)
	mustExec(`INSERT INTO usergroup_users VALUES
		('00000000-0000-0000-0000-0000000000f1', $1, $4),
		('00000000-0000-0000-0000-0000000000f2', $2, $4),
		('00000000-0000-0000-0000-0000000000f3', $3, $4)`, groupA1, groupA2, groupB1, person)

	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: db}, nil
	})

	tenantAdmin := scopedCtx(t, fmt.Sprintf(`{"TenantId":%q,"UserId":"u1","Roles":[%q]}`, tenantA, security.AUTH_TENANT_ADMIN_ROLE))
	resp, err := ExecuteQuery(tenantAdmin, QueryRequest{
		Table:   "usergroups_list_users",
		Columns: fromStringColsToQueryCols([]string{"user_id", "user_groups"}),
		Where:   QueryWhereClause{Binary: BinaryWhereClause{"group_id": {Eq: groupA1}}},
		Limit:   10,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Errors)
	require.Len(t, resp.Rows, 1)

	var groups []struct {
		UserGroup struct {
			Name string `json:"name"`
		} `json:"user_group"`
	}
	var raw []byte
	switch v := resp.Rows[0]["user_groups"].(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		raw, err = json.Marshal(v)
		require.NoError(t, err)
	}
	require.NoError(t, json.Unmarshal(raw, &groups), string(raw))
	names := []string{}
	for _, g := range groups {
		names = append(names, g.UserGroup.Name)
	}
	assert.ElementsMatch(t, []string{"alpha-oncall", "alpha-sre"}, names,
		"a member's groups in another tenant must not appear in this tenant's member list")
}
