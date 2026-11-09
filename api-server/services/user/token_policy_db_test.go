package user

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What only a real Postgres shows about a key's policy rows: that the update lock makes a
// concurrent edit wait instead of merging with it, and that the rows keep matching their key
// whatever form the session's ids take and after the owner switches tenant. The table is
// created from the V985 migration itself. Named _DB so the "PR lifecycle sweeps (real
// Postgres)" job runs it; skipped locally unless a database is configured.
func TestKeyGatewayPolicy_DB(t *testing.T) {
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
	ctx := context.Background()
	db, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("key_policy_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	_, err = db.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
		_ = db.Close()
	})
	// Each edit needs its own connection, so the schema is set per connection.
	connect := func(t *testing.T) *sqlx.Conn {
		c, err := db.Connx(ctx)
		require.NoError(t, err)
		_, err = c.ExecContext(ctx, `SET search_path TO `+schema)
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	base := connect(t)
	_, err = base.ExecContext(ctx, `CREATE TABLE user_auths (
		id uuid PRIMARY KEY DEFAULT gen_random_uuid(), "user" uuid NOT NULL, tenant_id uuid,
		provider_type text NOT NULL DEFAULT 'credentials', provider text NOT NULL DEFAULT 'token')`)
	require.NoError(t, err)
	migration, err := filepath.Glob("../../migrations/migrations/app/*_V985_create_llm_gateway_access_policies.up.sql")
	require.NoError(t, err)
	require.Len(t, migration, 1)
	ddl, err := os.ReadFile(migration[0])
	require.NoError(t, err)
	_, err = base.ExecContext(ctx, strings.ReplaceAll(string(ddl), "public.", ""))
	require.NoError(t, err)

	newKey := func(t *testing.T, tenant, user string) string {
		var id string
		require.NoError(t, base.QueryRowxContext(ctx,
			`INSERT INTO user_auths ("user", tenant_id) VALUES ($1, $2) RETURNING id::text`, user, tenant).Scan(&id))
		return id
	}
	// edit changes a key's policy the way UpdateUserAuthToken does: lock the key, then replace.
	edit := func(c *sqlx.Conn, tenant, user, key string, p *GatewayPolicy) error {
		tx, err := c.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		k, err := lockKeyForUpdate(tx, key, tenant, user)
		if err != nil {
			return err
		}
		if err := replaceKeyGatewayPolicy(tx, k, p); err != nil {
			return err
		}
		return tx.Commit()
	}
	policyOf := func(t *testing.T, key string) *GatewayPolicy {
		tx, err := base.BeginTxx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()
		got, err := loadGatewayPoliciesForKeys(tx, []string{key})
		require.NoError(t, err)
		require.LessOrEqual(t, len(got), 1, "only the asked key")
		return got[key]
	}
	const tenant, otherTenant = "aaaaaaaa-0000-4000-8000-000000000001", "aaaaaaaa-0000-4000-8000-000000000002"

	t.Run("a concurrent edit waits and replaces, never merges", func(t *testing.T) {
		const user = "bbbbbbbb-0000-4000-8000-000000000001"
		key := newKey(t, tenant, user)
		require.NoError(t, edit(base, tenant, user, key, &GatewayPolicy{Models: []string{"model-0"}}))

		first, err := connect(t).BeginTxx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = first.Rollback() }()
		k, err := lockKeyForUpdate(first, key, tenant, user)
		require.NoError(t, err)
		require.NoError(t, replaceKeyGatewayPolicy(first, k, &GatewayPolicy{Models: []string{"model-a"}}))

		second := connect(t)
		var secondPID int
		require.NoError(t, second.QueryRowxContext(ctx, `SELECT pg_backend_pid()`).Scan(&secondPID))
		done := make(chan error, 1)
		go func() { done <- edit(second, tenant, user, key, &GatewayPolicy{Models: []string{"model-b"}}) }()
		// Commit the first edit only once the second is blocked behind it.
		require.Eventually(t, func() bool {
			var waiting bool
			return db.QueryRowx(`SELECT EXISTS (SELECT 1 FROM pg_locks WHERE pid = $1 AND NOT granted)`, secondPID).
				Scan(&waiting) == nil && waiting
		}, 5*time.Second, 20*time.Millisecond, "the second edit should wait for the first")
		require.NoError(t, first.Commit())
		require.NoError(t, <-done)

		assert.Equal(t, []string{"model-b"}, policyOf(t, key).Models)
	})

	t.Run("rows follow the key when its owner switches tenant", func(t *testing.T) {
		const user = "bbbbbbbb-0000-4000-8000-000000000002"
		key := newKey(t, tenant, user)
		require.NoError(t, edit(base, tenant, user, key, &GatewayPolicy{Models: []string{"gpt-4o-mini"}}))
		// What UpdateUserAccessed does when the owner opens the app in another tenant.
		_, err := base.ExecContext(ctx, `UPDATE user_auths SET tenant_id = $1 WHERE "user" = $2`, otherTenant, user)
		require.NoError(t, err)
		assert.Equal(t, []string{"gpt-4o-mini"}, policyOf(t, key).Models)

		// An edit from the new tenant replaces the rows written in the old one.
		require.NoError(t, edit(base, otherTenant, user, key, &GatewayPolicy{Models: []string{"claude-haiku-4-5"}}))
		assert.Equal(t, []string{"claude-haiku-4-5"}, policyOf(t, key).Models)
	})

	t.Run("rows take the stored ids, whatever form the session sent", func(t *testing.T) {
		const user = "bbbbbbbb-0000-4000-8000-000000000003"
		key := newKey(t, tenant, user)
		require.NoError(t, edit(base, strings.ToUpper(tenant), strings.ToUpper(user), strings.ToUpper(key),
			&GatewayPolicy{Endpoints: []string{"chat"}}))
		assert.Equal(t, []string{"chat"}, policyOf(t, key).Endpoints)
		assert.Nil(t, policyOf(t, newKey(t, tenant, user)), "a key with no rows is unrestricted")
	})

	t.Run("a key row must name its user and a known dimension", func(t *testing.T) {
		_, err := base.ExecContext(ctx, `INSERT INTO llm_gateway_access_policies (tenant_id, user_id, api_key_id, dimension, value)
			VALUES ($1, '', 'k', 'model', 'm')`, tenant)
		assert.ErrorContains(t, err, "llm_gateway_access_policies_scope")
		_, err = base.ExecContext(ctx, `INSERT INTO llm_gateway_access_policies (tenant_id, user_id, api_key_id, dimension, value)
			VALUES ($1, 'u', 'k', 'cidr', '10.0.0.0/8')`, tenant)
		assert.ErrorContains(t, err, "llm_gateway_access_policies_dimension_check")
	})
}
