package user

import (
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

// Re-saving a key keeps the alert state of its unchanged limits and clears a changed one's.
func TestReplaceKeyLimits_KeepsAlertStateOfUnchangedLimits_DB(t *testing.T) {
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
	db, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	schema := fmt.Sprintf("key_limits_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	_, err = db.Exec(fmt.Sprintf(`CREATE SCHEMA %s; SET search_path TO %s;
		CREATE TABLE notification_source_type (value text PRIMARY KEY)`, schema, schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
		_ = db.Close()
	})
	for _, m := range []string{"V803_llm_gateway_tables", "V1039_add_llm_gateway_rate_limit_soft_pct"} {
		files, err := filepath.Glob("../../migrations/migrations/app/*_" + m + ".up.sql")
		require.NoError(t, err)
		require.Len(t, files, 1, m)
		ddl, err := os.ReadFile(files[0])
		require.NoError(t, err)
		_, err = db.Exec(strings.NewReplacer(`"public".`, "", "public.", "").Replace(string(ddl)))
		require.NoError(t, err, m)
	}
	// V964's quota columns, without the rest of that migration (it also alters user_auths).
	_, err = db.Exec(`ALTER TABLE llm_gateway_rate_limits ADD COLUMN api_key_id text NOT NULL DEFAULT '',
		ADD COLUMN model text NOT NULL DEFAULT ''`)
	require.NoError(t, err)
	const tenant, owner, key = "t1", "u1", "k1"
	pct := func(v int) *int { return &v }
	save := func(limits []TokenLimit) {
		tx, err := db.Beginx()
		require.NoError(t, err)
		require.NoError(t, replaceKeyLimits(tx, tenant, owner, key, limits))
		require.NoError(t, tx.Commit())
	}
	alerted := func(metric string) *int {
		var p *int
		require.NoError(t, db.Get(&p, `SELECT alerted_pct FROM llm_gateway_rate_limits WHERE api_key_id = $1 AND metric = $2`, key, metric))
		return p
	}
	limits := []TokenLimit{
		{Metric: "cost", Period: "day", Value: 50, SoftPct: pct(80)},
		{Metric: "tokens", Period: "day", Value: 1000, SoftPct: pct(80)},
	}
	save(limits)
	_, err = db.Exec(`UPDATE llm_gateway_rate_limits SET alerted_window = now(), alerted_pct = 80 WHERE api_key_id = $1`, key)
	require.NoError(t, err)

	limits[1].Value = 2000
	save(limits)
	assert.Equal(t, pct(80), alerted("cost"), "an unchanged limit keeps its alert state")
	assert.Nil(t, alerted("tokens"), "a raised limit may alert again")
}
