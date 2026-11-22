package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// TestSqlExecuteQueryCannotEscapeReadOnly runs model-shaped SQL through
// sqlExecuteQuery against a real Postgres and checks that a second statement
// can neither end the read-only transaction nor write. Set
// METASTORE_SQL_TEST_DB_URL to a disposable database to run it; the test works
// in its own schema and drops it afterwards.
func TestSqlExecuteQueryCannotEscapeReadOnly(t *testing.T) {
	dsn := os.Getenv("METASTORE_SQL_TEST_DB_URL")
	if dsn == "" {
		t.Skip("METASTORE_SQL_TEST_DB_URL not set")
	}
	schema := fmt.Sprintf("metastore_sql_test_%d", time.Now().UnixNano())
	admin, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })
	admin.MustExec("CREATE SCHEMA " + schema)
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})
	admin.MustExec(fmt.Sprintf(`CREATE TABLE %s.events (id int); INSERT INTO %s.events VALUES (1), (2)`, schema, schema))

	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sqlx.Connect("postgres", dsn+sep+"search_path="+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	common.ResetDatabaseManager(common.Metastore)
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: db}, nil
	})
	t.Cleanup(func() { common.ResetDatabaseManager(common.Metastore) })

	ctx := security.NewRequestContext(context.Background(), nil, nil, nil, nil)
	const account = "550e8400-e29b-41d4-a716-446655440000"
	count := func() int {
		var n int
		require.NoError(t, admin.Get(&n, "SELECT count(*) FROM "+schema+".events"))
		return n
	}

	data, _, err := sqlExecuteQuery(ctx, "select id from events order by id", account, "events", "", 0)
	require.NoError(t, err, "a plain select still runs")
	assert.Len(t, data, 2)
	data, _, err = sqlExecuteQuery(ctx, "select id from events limit 1;", account, "events", "", 0)
	require.NoError(t, err, "a trailing semicolon is still one statement")
	assert.Len(t, data, 1)

	for _, q := range []string{
		"select * from events limit 1; commit; delete from events",
		"select * from events limit 1; delete from events",
		"select * from events; commit; delete from events",
		"select * from events limit 1; commit; drop table events",
	} {
		_, _, err := sqlExecuteQuery(ctx, q, account, "events", "", 0)
		assert.Error(t, err, q)
		assert.Equal(t, 2, count(), "rows must survive %q", q)
	}

	_, _, err = sqlExecuteQuery(ctx, "with d as (delete from events returning *) select * from events", account, "events", "", 0)
	assert.Error(t, err, "DML in a CTE is still refused by the read-only transaction")
	assert.Equal(t, 2, count())
}

// TestMetastoreSQLToolsScopedRole_DB runs every metastore SQL tool's view as
// role nb_llm_sql_tools against a fully migrated database (migration V1112).
// It fails when a tool view reads a column or table that the nb_llm_tools views
// lack, when another account's rows show through, or when the role can reach a
// table outside nb_llm_tools. CI runs it in migrations-validate:
//
//	TOOLS_TEST_DATABASE_URL=postgres://... REQUIRE_DB_TESTS=true go test ./tools/ -run '_DB$' -count=1
//
// It seeds its own tenant and accounts and removes them afterwards.
func TestMetastoreSQLToolsScopedRole_DB(t *testing.T) {
	dsn := os.Getenv("TOOLS_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatal("REQUIRE_DB_TESTS is set but TOOLS_TEST_DATABASE_URL is not")
		}
		t.Skip("set TOOLS_TEST_DATABASE_URL to a migrated database to run the scoped-role SQL tests")
	}
	db, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	common.ResetDatabaseManager(common.Metastore)
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: db}, nil
	})
	t.Cleanup(func() { common.ResetDatabaseManager(common.Metastore) })
	prev := config.Config.LlmServerAgentSqlScopedRole
	config.Config.LlmServerAgentSqlScopedRole = true
	t.Cleanup(func() { config.Config.LlmServerAgentSqlScopedRole = prev })

	user, tenant := uuid.NewString(), uuid.NewString()
	a, b := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM anomaly WHERE account_id IN ($1, $2)`,
			`DELETE FROM cloud_accounts WHERE id IN ($1, $2)`,
		} {
			if _, err := db.Exec(q, a, b); err != nil {
				t.Errorf("cleanup %q: %v", q, err)
			}
		}
		if _, err := db.Exec(`DELETE FROM tenant WHERE id = $1`, tenant); err != nil {
			t.Errorf("cleanup tenant: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM users WHERE id = $1`, user); err != nil {
			t.Errorf("cleanup user: %v", err)
		}
	})
	db.MustExec(`INSERT INTO users (id, username, display_name) VALUES ($1, $2, 'SQL tools test user')`, user, "sql-tools-test-"+user)
	db.MustExec(`INSERT INTO tenant (id, name, created_by, updated_by) VALUES ($1, $2, $3, $3)`, tenant, "sql-tools-test-"+tenant, user)
	for _, acct := range []string{a, b} {
		db.MustExec(`INSERT INTO cloud_accounts (id, account_name, account_type, tenant, created_by, updated_by, access_key)
			VALUES ($1, $2, 'K8s', $3, $4, $4, 'not-for-the-tools')`, acct, "sql-tools-test-"+acct, tenant, user)
		db.MustExec(`INSERT INTO anomaly (account_id, tenant, name, namespace, reference_value, current_value, anomaly_type, is_anomaly, evaluated_at)
			VALUES ($1, $2, $3, 'default', '{}', 1, 'CloudSpendService', true, now())`, acct, tenant, "anomaly-of-"+acct)
	}

	ctx := security.NewRequestContext(context.Background(), nil, nil, nil, nil)

	// The role is there: the CI database is migrated by a superuser.
	tx, err := db.BeginTxx(context.Background(), &sql.TxOptions{ReadOnly: true})
	require.NoError(t, err)
	scoped, err := sqlScopeToAccount(context.Background(), tx, a)
	_ = tx.Rollback()
	require.NoError(t, err)
	require.True(t, scoped, "V1112 should have created role %s and schema %s", sqlToolRole, sqlToolSchema)

	// Every tool view runs as the role: each column and table it reads exists
	// in nb_llm_tools.
	for _, tc := range scopedToolViewCases(a) {
		_, _, err := sqlExecuteQuery(ctx, tc.query, a, tc.table, tc.view, 0)
		assert.NoError(t, err, tc.name)
	}

	assertModelFilterStaysAboveFence(t, db, a)

	// With no account filter in the SQL at all, the database still returns
	// only account a's rows.
	data, _, err := sqlExecuteQuery(ctx, "SELECT name FROM anomaly", a, "anomaly", "", 0)
	require.NoError(t, err)
	if assert.Len(t, data, 1) {
		assert.Equal(t, "anomaly-of-"+a, data[0]["name"])
	}

	// SQL that got past the checks in sqlValidateReadOnly still cannot reach
	// anything outside nb_llm_tools.
	for _, q := range []string{
		"SELECT count(*) FROM public.users",
		"SELECT count(*) FROM public.llm_conversation_tool_calls",
		"SELECT count(*) FROM llm_conversation_tool_calls",
		"SELECT access_key FROM cloud_accounts",
		"SELECT count(*) FROM public.anomaly",
	} {
		tx, err := db.BeginTxx(context.Background(), &sql.TxOptions{ReadOnly: true})
		require.NoError(t, err)
		scoped, err := sqlScopeToAccount(context.Background(), tx, a)
		require.NoError(t, err)
		require.True(t, scoped)
		_, err = tx.Exec(q)
		assert.Error(t, err, q)
		_ = tx.Rollback()
	}
	tx, err = db.BeginTxx(context.Background(), &sql.TxOptions{ReadOnly: true})
	require.NoError(t, err)
	_, err = sqlScopeToAccount(context.Background(), tx, a)
	require.NoError(t, err)
	var accounts []string
	require.NoError(t, tx.Select(&accounts, "SELECT id::text FROM cloud_accounts UNION SELECT account_id::text FROM anomaly"))
	_ = tx.Rollback()
	assert.Equal(t, []string{a}, accounts, "the scoped views hold account a only")
}

type scopedToolViewCase struct{ name, query, table, view, probeColumn string }

// scopedToolViewCases is every view a metastore SQL tool binds, as the tool binds it.
func scopedToolViewCases(a string) []scopedToolViewCase {
	return []scopedToolViewCase{
		{"events_execute", "SELECT * FROM events", "events", resolveEventsViewPlaceholders(eventsViewWithRanking, a, false), "id"},
		{"events by id", "SELECT * FROM events", "events", byIDView(a, "SELECT * FROM events WHERE id = '"+uuid.NewString()+"'"), "id"},
		{"list_events", "SELECT id, title, event_count, evidences FROM events ORDER BY starts_at DESC", "events", buildListEventsView(a, []string{"starts_at >= now() - interval '1 day'"}, 10), "id"},
		{"aggregate_events", "SELECT priority, count(*) AS count FROM events GROUP BY priority", "events", resolveEventsViewPlaceholders(eventsViewWithId, a, true), "id"},
		{"anomaly_execute", "SELECT * FROM anomaly", "anomaly", strings.NewReplacer("__ACCOUNT_ID__", a, "__DATE_FILTER__", "2000-01-01").Replace(anomalyView), "id"},
		{"recommendation_execute", "SELECT * FROM recommendation_view", "recommendation_view", recommendationViewFor(a), "id"},
		{"recommendation_resolution_execute", "SELECT * FROM recommendation_resolution_view", "recommendation_resolution_view", recommendationResolutionView, "id"},
		{"security_execute", "SELECT * FROM security_view", "security_view", SecurityView, "id"},
		{"security_execute cis", "SELECT * FROM security_view", "security_view", cisScanView, "status"},
	}
}

const fenceProbeMarker = "fence-probe-marker"

// assertModelFilterStaysAboveFence asks Postgres where it evaluates a
// model-written WHERE on each tool view. It must run only on the fenced tool
// view's output, never below it next to the views' account filter, where a
// failing cast could print another account's row. Without the fence the same
// filter must sink below it somewhere, or this check could not catch a
// Postgres upgrade that stops treating OFFSET 0 as a fence.
func assertModelFilterStaysAboveFence(t *testing.T, db *sqlx.DB, account string) {
	t.Helper()
	sunkWithoutFence := 0
	for _, tc := range scopedToolViewCases(account) {
		query := fmt.Sprintf("SELECT * FROM %s WHERE %s::text <> '%s'", tc.table, tc.probeColumn, fenceProbeMarker)
		for _, fenced := range []bool{true, false} {
			config.Config.LlmServerAgentSqlScopedRole = fenced
			bound, source, err := sqlBindTable(query, account, tc.table, tc.view)
			config.Config.LlmServerAgentSqlScopedRole = true
			require.NoError(t, err, tc.name)

			tx, err := db.BeginTxx(context.Background(), &sql.TxOptions{ReadOnly: true})
			require.NoError(t, err)
			scoped, err := sqlScopeToAccount(context.Background(), tx, account)
			require.NoError(t, err)
			require.True(t, scoped)
			var planJSON string
			err = tx.Get(&planJSON, "EXPLAIN (FORMAT JSON) "+strings.Replace(bound, sqlTablePlaceholder, source, 1))
			_ = tx.Rollback()
			require.NoError(t, err, tc.name)

			var plan []struct {
				Plan map[string]any `json:"Plan"`
			}
			require.NoError(t, json.Unmarshal([]byte(planJSON), &plan), tc.name)
			nodes := probeFilterNodes(plan[0].Plan)
			require.NotEmpty(t, nodes, "%s (fenced=%v): the probe filter is missing from the plan", tc.name, fenced)
			atFence := true
			for _, n := range nodes {
				if n["Node Type"] != "Subquery Scan" || n["Alias"] != tc.table {
					atFence = false
				}
			}
			if fenced {
				assert.True(t, atFence, "%s: the model's WHERE runs below the OFFSET 0 fence: %v", tc.name, nodes)
			} else if !atFence {
				sunkWithoutFence++
			}
		}
	}
	assert.Positive(t, sunkWithoutFence, "without the fence the probe never sank below the tool view, so this check proves nothing")
}

// probeFilterNodes returns the plan nodes whose conditions mention fenceProbeMarker.
func probeFilterNodes(node map[string]any) []map[string]any {
	var found []map[string]any
	for _, key := range []string{"Filter", "Join Filter", "Index Cond", "Recheck Cond", "Hash Cond", "Merge Cond", "One-Time Filter"} {
		if cond, ok := node[key].(string); ok && strings.Contains(cond, fenceProbeMarker) {
			found = append(found, node)
			break
		}
	}
	if children, ok := node["Plans"].([]any); ok {
		for _, c := range children {
			if child, ok := c.(map[string]any); ok {
				found = append(found, probeFilterNodes(child)...)
			}
		}
	}
	return found
}
