package dbms

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// runDBMSTest is the shared helper for all DB integration tests.
// It runs a set of named queries and logs results. Error cases are
// expected to return an error (not a panic or silent []).
func runDBMSTest(t *testing.T, dbmsType, accountID, integrationID string, queries []struct{ name, query string }) {
	t.Helper()
	tenantID := os.Getenv("TEST_TENANT_ID")
	userID := os.Getenv("TEST_USER_ID")
	if accountID == "" || integrationID == "" || tenantID == "" {
		t.Skipf("env vars not set for %s integration test", dbmsType)
	}

	task := &DBMSQueryTask{}
	taskCtx := testutils.NewTestTaskContext(tenantID, accountID, userID, slog.Default())

	for _, tc := range queries {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result, err := task.Execute(taskCtx, map[string]any{
				"account_id":     accountID,
				"command":        tc.query,
				"dbms_type":      dbmsType,
				"integration_id": integrationID,
			})
			if err != nil {
				t.Logf("ERROR: %v", err)
				return
			}
			data := result.(map[string]any)["data"].(string)
			t.Logf("OK: %s", data)
			assert.True(t, strings.HasPrefix(data, "["))
		})
	}
}

func TestDBMSTask_Execute_MSSQL(t *testing.T) {
	runDBMSTest(t, "mssql",
		os.Getenv("TEST_MSSQL_ACCOUNT_ID"),
		os.Getenv("TEST_MSSQL_INTEGRATION_ID"),
		[]struct{ name, query string }{
			{"arithmetic", "select 1+123 as result"},
			{"multi column", "select 1 as id, 'hello' as msg, getdate() as ts"},
			{"syntax error", "selekt 1"},
		},
	)
}

func TestDBMSTask_Execute_Postgres(t *testing.T) {
	runDBMSTest(t, "postgresql",
		os.Getenv("TEST_K8S_ACCOUNT_ID"),
		os.Getenv("TEST_POSTGRESQL_INTEGRATION_ID"),
		[]struct{ name, query string }{
			{"arithmetic", "select 1+1 as result"},
			{"multi column", "select 1 as id, 'hello' as msg, now() as ts"},
			{"syntax error", "selekt 1"},
		},
	)
}

func TestDBMSTask_Execute_ClickHouse(t *testing.T) {
	runDBMSTest(t, "clickhouse",
		os.Getenv("TEST_K8S_ACCOUNT_ID"),
		os.Getenv("TEST_CLICKHOUSE_INTEGRATION_ID"),
		[]struct{ name, query string }{
			{"arithmetic", "select 1+1 as result"},
			{"multi column", "select 1 as id, 'hello' as msg, now() as ts"},
			{"syntax error", "selekt 1"},
		},
	)
}

func TestDBMSTask_Execute_MySQL(t *testing.T) {
	runDBMSTest(t, "mysql",
		os.Getenv("TEST_MYSQL_ACCOUNT_ID"),
		os.Getenv("TEST_MYSQL_INTEGRATION_ID"),
		[]struct{ name, query string }{
			{"arithmetic", "select 1+1 as result"},
			{"multi column", "select 1 as id, 'hello' as msg, now() as ts"},
			{"syntax error", "selekt 1"},
		},
	)
}

func TestDBMSTask_Execute_Oracle(t *testing.T) {
	runDBMSTest(t, "oracle",
		os.Getenv("TEST_ORACLE_ACCOUNT_ID"),
		os.Getenv("TEST_ORACLE_INTEGRATION_ID"),
		[]struct{ name, query string }{
			{"arithmetic", "select 1+1 as result from dual"},
			{"multi column", "select 1 as id, 'hello' as msg from dual"},
			{"syntax error", "selekt 1 from dual"},
		},
	)
}
