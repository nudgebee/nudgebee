package core

import (
	"errors"
	"log/slog"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"os"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testTenant = testenv.FakeTenantID

// Package-level sqlmock + DB so the database.Metastore cache (which is
// global state, see internal/database/rdbms.go) keeps pointing at a
// valid handle for every test. TestMain initializes once; individual
// tests reset expectations and set their own.
var (
	pkgRawDB *sqlmockDBHandle
	pkgMock  sqlmock.Sqlmock
)

type sqlmockDBHandle struct {
	close func() error
}

func TestMain(m *testing.M) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		slog.Error("integration_account_lookup_test: sqlmock.New failed", "error", err)
		os.Exit(1)
	}
	mock.MatchExpectationsInOrder(false)

	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgresql")}, nil
	})

	pkgRawDB = &sqlmockDBHandle{close: rawDB.Close}
	pkgMock = mock

	code := m.Run()
	_ = pkgRawDB.close()
	os.Exit(code)
}

// ctxForTenant builds a tenant-scoped RequestContext. The tenant-admin
// constructor calls GetAccountIdsByTenantId internally, so we preset that
// query with a benign empty result before delegating.
func ctxForTenant(t *testing.T, tenant string) *security.RequestContext {
	t.Helper()
	pkgMock.ExpectQuery(`SELECT id FROM cloud_accounts WHERE tenant`).
		WithArgs(tenant).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	return security.NewRequestContextForTenantAdmin(tenant, slog.Default(), nil, nil)
}

// matchSourceQuery / matchNoSourceQuery loosely match the two SQL branches
// of ListLinkedCloudAccountIDs. Whitespace differences shouldn't fail; the
// WHERE clause shape matters.
var (
	matchSourceQuery   = regexp.MustCompile(`(?s)SELECT ica\.cloud_account_id::text.*FROM integrations i.*JOIN integrations_cloud_accounts ica.*lower\(i\.source\) = lower\(\$4\)`)
	matchNoSourceQuery = regexp.MustCompile(`(?s)SELECT ica\.cloud_account_id::text.*FROM integrations i.*JOIN integrations_cloud_accounts ica.*lower\(i\.name\) = lower\(\$3\)\s*$`)
)

func TestListLinkedCloudAccountIDs(t *testing.T) {
	t.Run("happy path returns all linked accounts", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{"cloud_account_id"}).
			AddRow("acc-1").
			AddRow("acc-2").
			AddRow("acc-3")
		pkgMock.ExpectQuery(matchSourceQuery.String()).
			WithArgs(testTenant, "mcp", "my-mcp", "user").
			WillReturnRows(rows)

		got, err := ListLinkedCloudAccountIDs(ctxForTenant(t, testTenant), "mcp", "my-mcp", "user")
		require.NoError(t, err)
		assert.Equal(t, []string{"acc-1", "acc-2", "acc-3"}, got)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("zero matches returns empty slice (not error)", func(t *testing.T) {
		pkgMock.ExpectQuery(matchSourceQuery.String()).
			WithArgs(testTenant, "mcp", "missing", "user").
			WillReturnRows(sqlmock.NewRows([]string{"cloud_account_id"}))

		got, err := ListLinkedCloudAccountIDs(ctxForTenant(t, testTenant), "mcp", "missing", "user")
		require.NoError(t, err)
		assert.Empty(t, got, "no rows must produce empty slice (not nil error)")
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("empty source uses no-source branch (3-arg query)", func(t *testing.T) {
		pkgMock.ExpectQuery(matchNoSourceQuery.String()).
			WithArgs(testTenant, "mcp", "my-mcp").
			WillReturnRows(sqlmock.NewRows([]string{"cloud_account_id"}).AddRow("acc-1"))

		got, err := ListLinkedCloudAccountIDs(ctxForTenant(t, testTenant), "mcp", "my-mcp", "")
		require.NoError(t, err)
		assert.Equal(t, []string{"acc-1"}, got)
		assert.NoError(t, pkgMock.ExpectationsWereMet(), "must use the no-source SQL branch when source is empty")
	})

	t.Run("tenant scoping is applied via SQL arg", func(t *testing.T) {
		// sqlmock's WithArgs assertion fails the test if the call leaks a
		// different tenant value into the query. That's the security
		// invariant we're locking down here.
		pkgMock.ExpectQuery(matchSourceQuery.String()).
			WithArgs(testTenant, "mcp", "shared-name", "user").
			WillReturnRows(sqlmock.NewRows([]string{"cloud_account_id"}).AddRow("acc-of-test-tenant"))

		got, err := ListLinkedCloudAccountIDs(ctxForTenant(t, testTenant), "mcp", "shared-name", "user")
		require.NoError(t, err)
		assert.Equal(t, []string{"acc-of-test-tenant"}, got)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("query error is wrapped (errors.Is preserves underlying)", func(t *testing.T) {
		dbErr := errors.New("connection refused")
		pkgMock.ExpectQuery(matchSourceQuery.String()).
			WithArgs(testTenant, "mcp", "my-mcp", "user").
			WillReturnError(dbErr)

		got, err := ListLinkedCloudAccountIDs(ctxForTenant(t, testTenant), "mcp", "my-mcp", "user")
		assert.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "query failed")
		assert.ErrorIs(t, err, dbErr, "must wrap the underlying error so callers can errors.Is it")
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("scan error is wrapped", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{"cloud_account_id"}).
			AddRow("acc-1").
			RowError(0, errors.New("bad row"))
		pkgMock.ExpectQuery(matchSourceQuery.String()).
			WithArgs(testTenant, "mcp", "my-mcp", "user").
			WillReturnRows(rows)

		got, err := ListLinkedCloudAccountIDs(ctxForTenant(t, testTenant), "mcp", "my-mcp", "user")
		assert.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ListLinkedCloudAccountIDs:")
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("uppercase args are forwarded as-is (SQL applies lower())", func(t *testing.T) {
		// The query uses lower($N) on both sides of the comparison; we
		// must not pre-lowercase the args at the Go layer (would mask any
		// future bug where the SQL drops lower() on the right side).
		pkgMock.ExpectQuery(matchSourceQuery.String()).
			WithArgs(testTenant, "MCP", "MyMcp", "User").
			WillReturnRows(sqlmock.NewRows([]string{"cloud_account_id"}).AddRow("acc-1"))

		got, err := ListLinkedCloudAccountIDs(ctxForTenant(t, testTenant), "MCP", "MyMcp", "User")
		require.NoError(t, err)
		assert.Equal(t, []string{"acc-1"}, got)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})
}
