package user

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "nudgebee/services/integrations/core"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A source that returns the same user twice must not fail the batched upsert: Postgres
// rejects a multi-row ON CONFLICT DO UPDATE that hits one key twice. Named _DB for the
// real-Postgres CI job.
func TestUpsertAccounts_DuplicateExternalUser_DB(t *testing.T) {
	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("TEST_POSTGRES_DSN")
	}
	if dsn == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatal("REQUIRE_DB_TESTS is set but neither APP_DATABASE_URL nor TEST_POSTGRES_DSN is")
		}
		t.Skip("no database configured; export APP_DATABASE_URL to run this suite")
	}
	admin, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("identity_sync_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	_, err = admin.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = admin.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
		_ = admin.Close()
	})
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sqlx.Connect("postgres", dsn+sep+"search_path="+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Only the ids the table's foreign keys point at.
	_, err = db.Exec(`CREATE TABLE cloud_accounts (id uuid PRIMARY KEY);
		CREATE TABLE integrations (id uuid PRIMARY KEY);
		CREATE TABLE users (id uuid PRIMARY KEY)`)
	require.NoError(t, err)
	files, err := filepath.Glob("../../migrations/migrations/app/*_V771_create_integration_user_accounts.up.sql")
	require.NoError(t, err)
	require.Len(t, files, 1)
	ddl, err := os.ReadFile(files[0])
	require.NoError(t, err)
	_, err = db.Exec(strings.ReplaceAll(string(ddl), `"public".`, ""))
	require.NoError(t, err)

	const (
		tenant        = "aaaaaaaa-0000-0000-0000-000000000001"
		integrationId = "bbbbbbbb-0000-0000-0000-000000000001"
		accountA      = "cccccccc-0000-0000-0000-000000000001"
		accountB      = "cccccccc-0000-0000-0000-000000000002"
	)
	_, err = db.Exec(`INSERT INTO integrations (id) VALUES ($1)`, integrationId)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO cloud_accounts (id) VALUES ($1), ($2)`, accountA, accountB)
	require.NoError(t, err)

	users := []core.ExternalUser{
		{ID: "u1", Username: "one", Email: "one@example.com", DisplayName: "One"},
		{ID: "u2", Username: "two", Email: "two@example.com", DisplayName: "Two"},
		{ID: "u1", Username: "one", Email: "one@example.com", DisplayName: "One"},
	}
	tctx := security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil)
	count := func(accountScoped bool) int {
		var n int
		require.NoError(t, db.Get(&n, `SELECT count(*) FROM integration_user_accounts
			WHERE tenant_id = $1 AND (account_id IS NOT NULL) = $2`, tenant, accountScoped))
		return n
	}

	t.Run("tenant-scoped", func(t *testing.T) {
		upserted, seen := upsertAccounts(tctx, db, tenant, "servicenow", integrationId, []string{""}, users)
		assert.Equal(t, 2, upserted)
		assert.Equal(t, []string{"u1", "u2"}, seen)
		assert.Equal(t, 2, count(false))
	})

	t.Run("account-scoped fan-out", func(t *testing.T) {
		upserted, seen := upsertAccounts(tctx, db, tenant, "servicenow", integrationId, []string{accountA, accountB}, users)
		assert.Equal(t, 4, upserted)
		assert.Equal(t, []string{"u1", "u2"}, seen)
		assert.Equal(t, 4, count(true))
	})
}
