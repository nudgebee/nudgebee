package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
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

	data, err := sqlExecuteQuery(ctx, "select id from events order by id", account, "events", "", 0)
	require.NoError(t, err, "a plain select still runs")
	assert.Len(t, data, 2)
	data, err = sqlExecuteQuery(ctx, "select id from events limit 1;", account, "events", "", 0)
	require.NoError(t, err, "a trailing semicolon is still one statement")
	assert.Len(t, data, 1)

	for _, q := range []string{
		"select * from events limit 1; commit; delete from events",
		"select * from events limit 1; delete from events",
		"select * from events; commit; delete from events",
		"select * from events limit 1; commit; drop table events",
	} {
		_, err := sqlExecuteQuery(ctx, q, account, "events", "", 0)
		assert.Error(t, err, q)
		assert.Equal(t, 2, count(), "rows must survive %q", q)
	}

	_, err = sqlExecuteQuery(ctx, "with d as (delete from events returning *) select * from events", account, "events", "", 0)
	assert.Error(t, err, "DML in a CTE is still refused by the read-only transaction")
	assert.Equal(t, 2, count())
}
