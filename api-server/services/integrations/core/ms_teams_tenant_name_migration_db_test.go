package core

import (
	"os"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const msTeamsTeamMigration = "../../../migrations/migrations/app/1791268832476_V988_add_ms_teams_team_integration_type.up.sql"

const (
	testNbTenant  = "aaaaaaaa-0000-4000-8000-000000000001"
	testAadTenant = "bbbbbbbb-0000-4000-8000-000000000002"
	testObjectA   = "cccccccc-0000-4000-8000-000000000003"
	testObjectB   = "dddddddd-0000-4000-8000-000000000004"
)

// openMsTeamsMigrationTestDB returns a connection whose search_path is a fresh
// throwaway schema holding integration_types, so the real tables of whatever
// database it is pointed at are never touched. DB-gated: skips with no
// database, fails in the job that provisions one.
func openMsTeamsMigrationTestDB(t *testing.T, schema string) *sqlx.DB {
	t.Helper()
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
	drop := func() { _, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`) }
	drop()
	t.Cleanup(drop)
	for _, q := range []string{
		`CREATE SCHEMA ` + schema,
		`SET search_path TO ` + schema,
		`CREATE TABLE integration_types (name text PRIMARY KEY, category text, description text)`,
	} {
		_, err := db.Exec(q)
		require.NoError(t, err)
	}
	return db
}

func runMsTeamsTeamMigration(t *testing.T, db *sqlx.DB) {
	t.Helper()
	migration, err := os.ReadFile(msTeamsTeamMigration)
	require.NoError(t, err)
	_, err = db.Exec(strings.ReplaceAll(string(migration), `"public".`, ""))
	require.NoError(t, err)
}

// TestMsTeamsTenantNameMigration_DB runs the real migration against Postgres.
// What it protects lives in SQL: renaming ms_teams integrations to the
// Microsoft tenant id without colliding with integrations' existing unique key,
// and the unique indexes that keep a Microsoft tenant and a Teams team bound to
// one NudgeBee tenant. A failure here aborts the post-upgrade migration hook.
func TestMsTeamsTenantNameMigration_DB(t *testing.T) {
	db := openMsTeamsMigrationTestDB(t, "zz_ms_teams_tenant_name_test")
	mustExec := func(q string, args ...any) {
		_, e := db.Exec(q, args...)
		require.NoError(t, e)
	}
	// The real table's (source, type, name, tenant_id) key is what a careless
	// rename collides with when one tenant holds two rows for one Microsoft tenant.
	mustExec(`CREATE TABLE integrations (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id uuid, source text,
		type text, name text, updated_at timestamp,
		CONSTRAINT integrations_source_type_name_tenant_id_key UNIQUE (source, type, name, tenant_id))`)
	mustExec(`INSERT INTO integrations (tenant_id, source, type, name, updated_at) VALUES
		($1, 'user', 'ms_teams', $2, now()),
		($1, 'user', 'ms_teams', $3, now() - interval '1 day'),
		($1, 'user', 'ms_teams', 'contoso.com', now()),
		($1, 'user', 'slack', 'T1.x', now())`,
		testNbTenant, testObjectA+"."+testAadTenant, testObjectB+"."+testAadTenant)
	runMsTeamsTeamMigration(t, db)

	t.Run("ms_teams names become the Microsoft tenant id", func(t *testing.T) {
		var names []string
		require.NoError(t, db.Select(&names, `SELECT type || ':' || name FROM integrations ORDER BY type, name`))
		assert.Equal(t, []string{
			"ms_teams:" + testAadTenant,
			"ms_teams:contoso.com",                          // not a home_account_id
			"ms_teams:" + testObjectB + "." + testAadTenant, // the older duplicate keeps its name
			"slack:T1.x",
		}, names)
	})

	t.Run("a rerun is a no-op", func(t *testing.T) {
		runMsTeamsTeamMigration(t, db)
		var n int
		require.NoError(t, db.Get(&n, `SELECT count(*) FROM integrations WHERE type = 'ms_teams' AND name = $1`, testAadTenant))
		assert.Equal(t, 1, n)
	})

	t.Run("a Microsoft tenant links to one NudgeBee tenant", func(t *testing.T) {
		_, err := db.Exec(`INSERT INTO integrations (tenant_id, source, type, name) VALUES (gen_random_uuid(), 'user', 'ms_teams', $1)`, testAadTenant)
		assert.ErrorContains(t, err, "integrations_ms_teams_name_uk")
	})

	t.Run("a Teams team is bound to one NudgeBee tenant", func(t *testing.T) {
		var types int
		require.NoError(t, db.Get(&types, `SELECT count(*) FROM integration_types WHERE name = 'ms_teams_team'`))
		assert.Equal(t, 1, types)
		mustExec(`INSERT INTO integrations (tenant_id, source, type, name) VALUES ($1, 'user', 'ms_teams_team', 'group-1')`, testNbTenant)
		_, err := db.Exec(`INSERT INTO integrations (tenant_id, source, type, name) VALUES (gen_random_uuid(), 'user', 'ms_teams_team', 'group-1')`)
		assert.ErrorContains(t, err, "integrations_ms_teams_team_name_uk")
	})
}

// Two NudgeBee tenants already pointing at one Microsoft tenant is bad data, but
// it must not fail the deploy hook: the rename proceeds and the index is skipped.
func TestMsTeamsTenantNameMigrationSkipsIndexOnSharedTenant_DB(t *testing.T) {
	db := openMsTeamsMigrationTestDB(t, "zz_ms_teams_tenant_name_shared_test")
	_, err := db.Exec(`CREATE TABLE integrations (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id uuid,
		source text, type text, name text, updated_at timestamp, UNIQUE (source, type, name, tenant_id))`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO integrations (tenant_id, source, type, name) VALUES
		($1, 'user', 'ms_teams', $2), (gen_random_uuid(), 'user', 'ms_teams', $3)`,
		testNbTenant, testObjectA+"."+testAadTenant, testObjectB+"."+testAadTenant)
	require.NoError(t, err)
	runMsTeamsTeamMigration(t, db)

	var renamed, indexes int
	require.NoError(t, db.Get(&renamed, `SELECT count(*) FROM integrations WHERE name = $1`, testAadTenant))
	require.NoError(t, db.Get(&indexes, `SELECT count(*) FROM pg_indexes WHERE schemaname = 'zz_ms_teams_tenant_name_shared_test' AND indexname = 'integrations_ms_teams_name_uk'`))
	assert.Equal(t, 2, renamed)
	assert.Equal(t, 0, indexes)
}
