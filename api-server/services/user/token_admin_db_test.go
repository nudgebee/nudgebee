package user

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nudgebee/services/internal/database"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What only a real Postgres shows about the admin key list and revoke: tenant scoping, the
// newest-usage model and the cleanup of a revoked key's rows. Named _DB for the real-Postgres CI job.
func TestTenantTokens_DB(t *testing.T) {
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
	schema := fmt.Sprintf("tenant_tokens_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	_, err = admin.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = admin.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
		_ = admin.Close()
	})
	// Every pooled connection, including DeleteTenantToken's, starts in the test schema.
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sqlx.Connect("postgres", dsn+sep+"search_path="+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: db}, nil
	})

	// users and user_auths keep V0's column types, without the lookup-table foreign keys.
	_, err = db.Exec(`CREATE TABLE users (id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			username varchar(255) NOT NULL, display_name varchar(255) NOT NULL);
		CREATE TABLE user_auths (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), credential text NOT NULL DEFAULT '',
			provider_type text NOT NULL, "user" uuid NOT NULL, created_at timestamp NOT NULL DEFAULT now(),
			updated_at timestamp NOT NULL DEFAULT now(), name varchar(255), expires_at timestamptz,
			status text, provider text NOT NULL, accessed_at timestamp, tenant_id uuid)`)
	require.NoError(t, err)
	for _, m := range []string{"V783_llm_gateway_tables", "V964_api_key_policy", "V967_add_user_auths_scopes",
		"V985_create_llm_gateway_access_policies", "V1012_user_auths_token_suffix"} {
		files, err := filepath.Glob("../../migrations/migrations/app/*_" + m + ".up.sql")
		require.NoError(t, err)
		require.Len(t, files, 1, m)
		ddl, err := os.ReadFile(files[0])
		require.NoError(t, err)
		_, err = db.Exec(strings.ReplaceAll(string(ddl), "public.", ""))
		require.NoError(t, err, m)
	}
	// V793's backfill reads the pricing catalog, so only its column is added here.
	_, err = db.Exec(`ALTER TABLE llm_gateway_usage ADD COLUMN IF NOT EXISTS cost_usd double precision NOT NULL DEFAULT 0`)
	require.NoError(t, err)

	const tenantA, tenantB = storedTenant, "aaaaaaaa-0000-0000-0000-000000000002"
	newUser := func(name string) string {
		var id string
		require.NoError(t, db.QueryRowx(`INSERT INTO users (username, display_name) VALUES ($1, $1) RETURNING id::text`, name).Scan(&id))
		return id
	}
	newKey := func(tenant, user, name, kind string, created time.Time) string {
		var id string
		require.NoError(t, db.QueryRowx(`INSERT INTO user_auths (provider_type, provider, "user", tenant_id, name, kind, status, created_at)
			VALUES ('credentials', 'token', $1, $2, $3, $4, 'active', $5) RETURNING id::text`,
			user, tenant, name, kind, created).Scan(&id))
		return id
	}
	use := func(tenant, key, model string, at time.Time) {
		_, err := db.Exec(`INSERT INTO llm_gateway_usage (id, created_at, tenant_id, api_key_id, provider, model, method, path, status_code)
			VALUES (gen_random_uuid(), $1, $2, $3, 'openai', $4, 'POST', '/v1/chat/completions', 200)`, at, tenant, key, model)
		require.NoError(t, err)
	}
	quota := func(tenant, user, key string) {
		_, err := db.Exec(`INSERT INTO llm_gateway_rate_limits (tenant_id, user_id, api_key_id, metric, period, limit_value)
			VALUES ($1, $2, $3, 'cost', 'month', 10)`, tenant, user, key)
		require.NoError(t, err)
	}
	restrict := func(tenant, user, key string) {
		_, err := db.Exec(`INSERT INTO llm_gateway_access_policies (tenant_id, user_id, api_key_id, dimension, value)
			VALUES ($1, $2, $3, 'model', 'gpt-4o-mini')`, tenant, user, key)
		require.NoError(t, err)
	}
	count := func(table, key string) int {
		var n int
		require.NoError(t, db.Get(&n, `SELECT count(*) FROM `+table+` WHERE api_key_id = $1`, key))
		return n
	}

	now := time.Now().UTC()
	asha, bo, carl := newUser("asha"), newUser("bo"), newUser("carl")
	ashaKey := newKey(tenantA, asha, "laptop", KindGateway, now.Add(-2*time.Hour))
	boKey := newKey(tenantA, bo, "ci", KindGateway, now.Add(-time.Hour))
	carlKey := newKey(tenantB, carl, "other-tenant", KindPlatform, now)
	use(tenantA, ashaKey, "gpt-4o-mini", now.Add(-30*time.Minute))
	use(tenantA, ashaKey, "claude-haiku-4-5", now.Add(-5*time.Minute))
	use(tenantA, ashaKey, "gpt-4o", now.Add(-50*time.Minute))
	for _, k := range []struct{ user, key string }{{asha, ashaKey}, {bo, boKey}} {
		quota(tenantA, k.user, k.key)
		restrict(tenantA, k.user, k.key)
	}

	t.Run("lists every owner's keys in the tenant and none from another", func(t *testing.T) {
		got, err := listTokens(db, tokenFilter{TenantID: tenantA})
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, []string{boKey, ashaKey}, []string{got[0].Id, got[1].Id}, "newest first")
		assert.Equal(t, []string{"bo", "asha"}, []string{got[0].OwnerName, got[1].OwnerName})
		n, err := countTokens(db, tokenFilter{TenantID: tenantA})
		require.NoError(t, err)
		assert.Equal(t, 2, n)
	})

	t.Run("last model is the newest usage row; empty with no usage", func(t *testing.T) {
		got, err := listTokens(db, tokenFilter{TenantID: tenantA})
		require.NoError(t, err)
		byID := map[string]UserAuthToken{got[0].Id: got[0], got[1].Id: got[1]}
		assert.Equal(t, "claude-haiku-4-5", byID[ashaKey].LastModel)
		assert.Equal(t, "", byID[boKey].LastModel)
		assert.Equal(t, []string{"gpt-4o-mini"}, byID[ashaKey].GatewayPolicy.Models)
		assert.Len(t, byID[boKey].Limits, 1)
	})

	t.Run("upper-case tenant and owner ids still match", func(t *testing.T) {
		got, err := listTokens(db, tokenFilter{TenantID: strings.ToUpper(tenantA), OwnerID: strings.ToUpper(bo)})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, boKey, got[0].Id)
	})

	t.Run("a revoke removes the key and its rows, and only its rows", func(t *testing.T) {
		resp, err := DeleteTenantToken(signedInAs(t, adminUser, "tenant_admin"), TenantTokenDeleteRequest{Id: strings.ToUpper(boKey)})
		require.NoError(t, err)
		assert.Equal(t, TenantTokenDeleteResponse{Id: boKey, Name: "ci", OwnerId: bo, Status: "deleted"}, resp)
		var left int
		require.NoError(t, db.Get(&left, `SELECT count(*) FROM user_auths WHERE id = $1`, boKey))
		assert.Equal(t, 0, left)
		assert.Equal(t, 0, count("llm_gateway_rate_limits", boKey))
		assert.Equal(t, 0, count("llm_gateway_access_policies", boKey))
		assert.Equal(t, 1, count("llm_gateway_rate_limits", ashaKey))
		assert.Equal(t, 1, count("llm_gateway_access_policies", ashaKey))
	})

	t.Run("a key in another tenant reads as not found", func(t *testing.T) {
		_, err := DeleteTenantToken(signedInAs(t, adminUser, "tenant_admin"), TenantTokenDeleteRequest{Id: carlKey})
		assert.ErrorIs(t, err, errTokenNotFound)
		var left int
		require.NoError(t, db.Get(&left, `SELECT count(*) FROM user_auths WHERE id = $1`, carlKey))
		assert.Equal(t, 1, left)
	})
}
