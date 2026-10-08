package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nudgebee/llm-gateway/common"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The token lookup's policy join on a real Postgres, which sqlmock cannot show: which rows the
// join picks up, that a key with none reads as unrestricted (array_agg over no rows is NULL),
// and that the restriction survives the owner switching tenant. The tables are created from the
// V985 and V1004 migrations themselves. Named _DB like the api-server's real-Postgres tests;
// skipped unless a database is configured.
func TestUserAuthsValidator_KeyPolicy_DB(t *testing.T) {
	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("TEST_POSTGRES_DSN")
	}
	if dsn == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatal("REQUIRE_DB_TESTS is set but neither APP_DATABASE_URL nor TEST_POSTGRES_DSN is")
		}
		t.Skip("no database configured; export TEST_POSTGRES_DSN to run this suite")
	}
	db, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1) // the schema is set per connection
	schema := fmt.Sprintf("gateway_key_policy_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	_, err = db.Exec(fmt.Sprintf(`CREATE SCHEMA %s; SET search_path TO %s`, schema, schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
		_ = db.Close()
	})
	_, err = db.Exec(`CREATE TABLE user_auths (
		id uuid PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id uuid, "user" uuid NOT NULL, name text,
		provider_type text NOT NULL DEFAULT 'credentials', provider text NOT NULL DEFAULT 'token',
		kind text NOT NULL DEFAULT 'gateway', status text NOT NULL DEFAULT 'active',
		expires_at timestamp, token_sha256 text)`)
	require.NoError(t, err)
	migration, err := filepath.Glob("../../../api-server/migrations/migrations/app/*_V985_create_llm_gateway_access_policies.up.sql")
	require.NoError(t, err)
	require.Len(t, migration, 1)
	ddl, err := os.ReadFile(migration[0])
	require.NoError(t, err)
	_, err = db.Exec(strings.ReplaceAll(string(ddl), "public.", ""))
	require.NoError(t, err)
	accessMigration, err := filepath.Glob("../../../api-server/migrations/migrations/app/*_create_llm_gateway_user_access.up.sql")
	require.NoError(t, err)
	require.Len(t, accessMigration, 1)
	accessDDL, err := os.ReadFile(accessMigration[0])
	require.NoError(t, err)
	_, err = db.Exec(strings.ReplaceAll(string(accessDDL), "public.", ""))
	require.NoError(t, err)

	const tenant, otherTenant, user = "aaaaaaaa-0000-4000-8000-000000000001", "aaaaaaaa-0000-4000-8000-000000000002", "bbbbbbbb-0000-4000-8000-000000000001"
	addKey := func(raw string) string {
		var id string
		require.NoError(t, db.QueryRowx(`INSERT INTO user_auths (tenant_id, "user", name, token_sha256)
			VALUES ($1, $2, $3, $4) RETURNING id::text`, tenant, user, raw, sha256hex(raw)).Scan(&id))
		return id
	}
	allow := func(tenantID, userID, keyID, dimension, value string) {
		_, err := db.Exec(`INSERT INTO llm_gateway_access_policies (tenant_id, user_id, api_key_id, dimension, value)
			VALUES ($1, $2, $3, $4, $5)`, tenantID, userID, keyID, dimension, value)
		require.NoError(t, err)
	}
	restricted := addKey("raw-restricted")
	addKey("raw-unrestricted")
	allow(tenant, user, restricted, "model", "gpt-4o-mini")
	allow(tenant, user, restricted, "endpoint", "chat")
	// User-wide rows reach every key of the user in that tenant; tenant-wide rows are not built yet.
	allow(tenant, user, "", "model", "user-wide-model")
	allow(tenant, "", "", "provider", "openai")
	_, err = db.Exec(`INSERT INTO llm_gateway_user_access (tenant_id, user_id, suspended) VALUES ($1, $2, true)`, tenant, user)
	require.NoError(t, err)

	v := &userAuthsValidator{
		db:           func() (*common.DatabaseManager, error) { return &common.DatabaseManager{Db: db}, nil },
		recordAccess: func(*common.DatabaseManager, string) {},
	}
	id, ok := v.Validate("raw-restricted")
	require.True(t, ok)
	assert.Equal(t, KeyPolicy{Models: []string{"gpt-4o-mini"}, Endpoints: []string{"chat"}}, id.Policy)
	assert.Equal(t, KeyPolicy{Models: []string{"user-wide-model"}}, id.UserPolicy, "tenant-wide rows stay ignored")
	assert.True(t, id.Access.Suspended)

	id, ok = v.Validate("raw-unrestricted")
	require.True(t, ok)
	assert.False(t, id.Policy.Restricted(), "a key with no rows is unrestricted")
	assert.Equal(t, []string{"user-wide-model"}, id.UserPolicy.Models, "the user layer reaches every key")

	// The owner opens the app in another tenant, and UpdateUserAccessed moves their keys there.
	_, err = db.Exec(`UPDATE user_auths SET tenant_id = $1 WHERE "user" = $2`, otherTenant, user)
	require.NoError(t, err)
	id, ok = v.Validate("raw-restricted")
	require.True(t, ok)
	assert.Equal(t, otherTenant, id.TenantID)
	assert.Equal(t, []string{"gpt-4o-mini"}, id.Policy.Models, "the restriction follows the key")
	assert.False(t, id.UserPolicy.Restricted(), "tenant A's user rules do not follow the owner into tenant B")
	assert.False(t, id.Access.Suspended, "nor does tenant A's suspension")

	// A gateway deployed before V1004 still serves keys (and their key and user rules).
	_, err = db.Exec(`DROP TABLE llm_gateway_user_access`)
	require.NoError(t, err)
	id, ok = v.Validate("raw-restricted")
	require.True(t, ok, "missing access table falls back instead of refusing every key")
	assert.Equal(t, []string{"gpt-4o-mini"}, id.Policy.Models)
	assert.False(t, id.Access.Suspended)
}
