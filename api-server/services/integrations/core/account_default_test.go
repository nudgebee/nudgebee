package core

import (
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// A default the user turns on from the Integrations UI is marked on its link, so
// the k8s collector's heartbeat leaves it alone (user_set_defaults). Only the
// account the request changes is marked: a link the request merely re-sends as
// already on, or one the request creates, carries no user choice.

const (
	defTenant = "tenant-1"
	defConfig = "int-ch"
	defAcc    = "acc-1"
	defColumn = "default_log_provider"
)

func defaultTx(t *testing.T) (*sql.Tx, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	tx, err := db.Begin()
	require.NoError(t, err)
	return tx, mock
}

func expectCurrent(mock sqlmock.Sqlmock, rows *sqlmock.Rows) {
	mock.ExpectQuery(`SELECT default_log_provider FROM integrations_cloud_accounts\s+WHERE integration_id = \$1 AND cloud_account_id = \$2 AND tenant_id = \$3 AND link_role = 'own'`).
		WithArgs(defConfig, defAcc, defTenant).WillReturnRows(rows)
}

func expectUpsertOn(mock sqlmock.Sqlmock) {
	mock.ExpectExec(`INSERT INTO integrations_cloud_accounts`).
		WithArgs(defConfig, defAcc, defTenant).WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectMark(mock sqlmock.Sqlmock) {
	mock.ExpectExec(`UPDATE integrations_cloud_accounts\s+SET user_set_defaults = array_append\(array_remove\(user_set_defaults, \$4\), \$4\)\s+WHERE integration_id = \$1 AND cloud_account_id = \$2 AND tenant_id = \$3 AND link_role = 'own'`).
		WithArgs(defConfig, defAcc, defTenant, defColumn).WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectResetOthers(mock sqlmock.Sqlmock) {
	mock.ExpectExec(`UPDATE integrations_cloud_accounts\s+SET default_log_provider = false, user_set_defaults = array_remove\(user_set_defaults, \$3\)\s+WHERE cloud_account_id = \$1 AND integration_id != \$2`).
		WithArgs(defAcc, defConfig, defColumn).WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestWriteAccountDefault_TurningOnAnExistingLinkMarksIt(t *testing.T) {
	tx, mock := defaultTx(t)
	expectCurrent(mock, sqlmock.NewRows([]string{defColumn}).AddRow(false))
	expectUpsertOn(mock)
	expectMark(mock)
	expectResetOthers(mock)

	require.NoError(t, writeAccountDefault(tx, defTenant, defConfig, defAcc, defColumn, true))
	require.NoError(t, mock.ExpectationsWereMet())
}

// The UI used to re-send every account's value as it was when the modal opened;
// an account whose default was already on is not a choice made in this request.
func TestWriteAccountDefault_AlreadyOnIsNotMarked(t *testing.T) {
	tx, mock := defaultTx(t)
	expectCurrent(mock, sqlmock.NewRows([]string{defColumn}).AddRow(true))
	expectUpsertOn(mock)
	expectResetOthers(mock)

	require.NoError(t, writeAccountDefault(tx, defTenant, defConfig, defAcc, defColumn, true))
	require.NoError(t, mock.ExpectationsWereMet())
}

// A link the save creates was not one the user could see and toggle, e.g. one
// the collector removed while a stale modal stayed open; the next heartbeat may
// still reset it, as before.
func TestWriteAccountDefault_NewLinkIsNotMarked(t *testing.T) {
	tx, mock := defaultTx(t)
	expectCurrent(mock, sqlmock.NewRows([]string{defColumn}))
	expectUpsertOn(mock)
	expectResetOthers(mock)

	require.NoError(t, writeAccountDefault(tx, defTenant, defConfig, defAcc, defColumn, true))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWriteAccountDefault_TurningOffClearsTheMark(t *testing.T) {
	tx, mock := defaultTx(t)
	mock.ExpectExec(`UPDATE integrations_cloud_accounts\s+SET default_log_provider = false, user_set_defaults = array_remove\(user_set_defaults, \$3\)\s+WHERE integration_id = \$1 AND cloud_account_id = \$2`).
		WithArgs(defConfig, defAcc, defColumn).WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, writeAccountDefault(tx, defTenant, defConfig, defAcc, defColumn, false))
	require.NoError(t, mock.ExpectationsWereMet())
}

// The LLM default has its own tenant-level handling and no heartbeat claims it,
// so its column is written without reading or marking the link.
func TestWriteAccountDefault_LLMColumnIsNotMarked(t *testing.T) {
	tx, mock := defaultTx(t)
	mock.ExpectExec(`INSERT INTO integrations_cloud_accounts`).
		WithArgs(defConfig, defAcc, defTenant).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE integrations_cloud_accounts\s+SET default_llm_provider = false, user_set_defaults = array_remove\(user_set_defaults, \$3\)`).
		WithArgs(defAcc, defConfig, "default_llm_provider").WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, writeAccountDefault(tx, defTenant, defConfig, defAcc, "default_llm_provider", true))
	require.NoError(t, mock.ExpectationsWereMet())
}
