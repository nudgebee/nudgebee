package query

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scopedCtx builds a RequestContext for a PURE account-scoped custom-role user
// (no built-in roles) via the SecurityContext JSON wire, mirroring what the
// resolver produces for a V778 scope-on-grant assignment.
func scopedCtx(t *testing.T, scopedJSON string) *security.RequestContext {
	t.Helper()
	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(scopedJSON), &sc))
	return security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
}

// TestExecuteQuery_ScopedGrantAppliesAccountFilter is the end-to-end proof that a
// pure account-scoped custom grant (events:Read on acctA, no built-in roles) is
// honored by the query engine: the read reaches the DB restricted to the granted
// account (account_id IN acctA) under the tenant filter — not tenant-wide, not
// denied. Uses a sqlmock capture matcher so the assertion is on the actual SQL,
// not a brittle regex.
func TestExecuteQuery_ScopedGrantAppliesAccountFilter(t *testing.T) {
	var capturedSQL string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(_ string, actual string) error {
		capturedSQL = actual
		return nil // match any query; we assert on the captured text below
	})))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
	})
	mock.ExpectQuery("").WillReturnRows(sqlmock.NewRows([]string{"account_id"}))

	ctx := scopedCtx(t, `{
		"TenantId":"t1","UserId":"u1","Roles":[],"AccountIds":["acctA"],
		"ScopedCustomPermissions":{"acctA":{"events:Read":true}}
	}`)

	resp, err := ExecuteQuery(ctx, QueryRequest{
		Table:   "events_v2",
		Columns: fromStringColsToQueryCols([]string{"account_id"}),
		Limit:   10,
	})
	assert.NoError(t, err)
	assert.Empty(t, resp.Errors, "a scoped user must NOT be Access-Denied on their granted module")
	assert.Contains(t, capturedSQL, "acctA", "read must be restricted to the granted account")
	assert.Contains(t, capturedSQL, "t1", "the tenant filter must still apply")
}

// TestExecuteQuery_ScopedGrantForOtherModuleDenied proves the branch is
// fail-closed: a user scoped to a DIFFERENT module (tickets) with no events grant
// and no built-in role is denied on events — no query is executed.
func TestExecuteQuery_ScopedGrantForOtherModuleDenied(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
	})

	ctx := scopedCtx(t, `{
		"TenantId":"t1","UserId":"u1","Roles":[],"AccountIds":["acctA"],
		"ScopedCustomPermissions":{"acctA":{"tickets:Read":true}}
	}`)

	resp, err := ExecuteQuery(ctx, QueryRequest{
		Table:   "events_v2",
		Columns: fromStringColsToQueryCols([]string{"account_id"}),
		Limit:   10,
	})
	assert.NoError(t, err)
	assert.NotEmpty(t, resp.Errors, "a scoped grant for another module must be denied on events")
}
