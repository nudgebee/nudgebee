package dashboard

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	conflictTenant    = "6f1c2d3e-0000-4000-8000-0000000000aa"
	conflictUser      = "6f1c2d3e-0000-4000-8000-0000000000bb"
	otherUser         = "6f1c2d3e-0000-4000-8000-0000000000bd"
	conflictDashboard = "6f1c2d3e-0000-4000-8000-0000000000cc"
)

// mockMetastore points the Metastore manager at a sqlmock for one test.
func mockMetastore(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	manager := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return manager, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })
	return mock
}

func conflictCtx(t *testing.T) *security.RequestContext {
	return userCtx(t, conflictUser)
}

// userCtx builds a caller in the test tenant from its wire form; the
// tenant-admin constructors look accounts up in the database.
func userCtx(t *testing.T, userId string) *security.RequestContext {
	t.Helper()
	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(`{"TenantId":"`+conflictTenant+`","UserId":"`+userId+`"}`), &sc))
	return security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
}

func updateRequest(expectedVersion int) SaveRequest {
	return SaveRequest{Id: conflictDashboard, Title: "Checkout latency", ExpectedVersion: expectedVersion}
}

func expectLockedRow(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT is_builtin FROM dashboards WHERE id = \$1 AND tenant_id = \$2 FOR UPDATE`).
		WithArgs(conflictDashboard, conflictTenant).
		WillReturnRows(sqlmock.NewRows([]string{"is_builtin"}).AddRow(false))
}

func expectLatestVersion(mock sqlmock.Sqlmock, version int) {
	mock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\) FROM dashboard_versions`).
		WithArgs(conflictDashboard).
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(version))
}

// expectWrite covers the UPDATE, the new revision row, the commit and the Get
// that Save answers with.
func expectWrite(mock sqlmock.Sqlmock, nextVersion int) {
	mock.ExpectExec(`UPDATE dashboards SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\) \+ 1 FROM dashboard_versions`).
		WithArgs(conflictDashboard).
		WillReturnRows(sqlmock.NewRows([]string{"next"}).AddRow(nextVersion))
	mock.ExpectExec(`INSERT INTO dashboard_versions`).
		WithArgs(conflictDashboard, nextVersion, sqlmock.AnyArg(), "", conflictUser).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	expectGet(mock, nextVersion, "Ada Lovelace")
}

func expectGet(mock sqlmock.Sqlmock, version int, updatedByName any) {
	now := time.Now()
	mock.ExpectQuery(`FROM dashboard_versions v WHERE v.dashboard_id = dashboards.id\),\s+\(SELECT COALESCE\(u.display_name, u.username\) FROM users u WHERE u.id = dashboards.updated_by\)\s+FROM dashboards WHERE id = \$1 AND tenant_id = \$2`).
		WithArgs(conflictDashboard, conflictTenant).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "slug", "title", "description",
			"definition", "schema_version", "tags", "status", "is_builtin", "created_by", "updated_by",
			"created_at", "updated_at", "version", "updated_by_name"}).
			AddRow(conflictDashboard, conflictTenant, "checkout-latency", "Checkout latency", nil,
				[]byte(`{"panels":[]}`), 1, []byte(`[]`), StatusActive, false, conflictUser, conflictUser,
				now, now, version, updatedByName))
}

func TestSave_RefusesAStaleCopy(t *testing.T) {
	mock := mockMetastore(t)
	expectLockedRow(mock)
	// Someone else saved v4 after this caller read v3.
	expectLatestVersion(mock, 4)
	mock.ExpectRollback()

	_, err := Save(conflictCtx(t), updateRequest(3))

	assert.ErrorIs(t, err, ErrConflict)
	// No UPDATE and no revision row: the other person's save stands.
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSave_WritesWhenTheCopyIsCurrent(t *testing.T) {
	mock := mockMetastore(t)
	expectLockedRow(mock)
	expectLatestVersion(mock, 3)
	expectWrite(mock, 4)

	got, err := Save(conflictCtx(t), updateRequest(3))

	require.NoError(t, err)
	assert.Equal(t, 4, got.Version)
	assert.Equal(t, "Ada Lovelace", got.UpdatedByName)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSave_WithoutExpectedVersionSkipsTheCheck(t *testing.T) {
	mock := mockMetastore(t)
	expectLockedRow(mock)
	// No version read before the UPDATE: callers that predate the field keep
	// last-write-wins rather than starting to fail.
	expectWrite(mock, 5)

	_, err := Save(conflictCtx(t), updateRequest(0))

	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGet_ReadsVersionAndEditorInOneStatement(t *testing.T) {
	mock := mockMetastore(t)
	// A user row that no longer resolves leaves the name empty, not an error.
	expectGet(mock, 7, nil)

	got, err := Get(conflictCtx(t), GetRequest{Id: conflictDashboard})

	require.NoError(t, err)
	assert.Equal(t, 7, got.Version)
	assert.Empty(t, got.UpdatedByName)
	// sqlmock fails on any statement it was not told to expect, so a second
	// round trip for the version would surface here.
	assert.NoError(t, mock.ExpectationsWereMet())
}
