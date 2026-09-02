package vmpackage

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Re-pointing a discovery datasource used to strand everything the previous
// target had accumulated — nothing scans that account again, so its findings
// sat at whatever the last scan wrote, forever, alongside the corrected rows in
// the same tenant. These tests pin when the cleanup runs and, just as
// importantly, when it must not.

func TestRetireOrphanedVMScanArtifacts_ArchivesWhenNoLongerTargeted(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM integrations_cloud_accounts").
		WithArgs("old-account", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("UPDATE recommendation SET status = 'Archive'").
		WithArgs("tenant-1", "old-account", recommendationRuleName, recommendationCategory).
		WillReturnResult(sqlmock.NewResult(0, 140))
	mock.ExpectExec("UPDATE vm_package SET is_active = false").
		WithArgs("tenant-1", "old-account").
		WillReturnResult(sqlmock.NewResult(0, 494))
	mock.ExpectExec("UPDATE cloud_resourses SET is_active = false").
		WithArgs("tenant-1", "old-account", vmResourceIDPrefix+"%").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	_, err := mockDBManager.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		return nil, retireOrphanedVMScanArtifacts(tx, "tenant-1", "old-account")
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// One target account may serve several datasources. Only the last one to leave
// orphans anything, so a still-targeted account must be left completely alone —
// otherwise re-pointing datasource A would wipe datasource B's live findings.
func TestRetireOrphanedVMScanArtifacts_SkipsWhenStillTargeted(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM integrations_cloud_accounts").
		WithArgs("shared-account", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	// No UPDATE of any kind may follow.
	mock.ExpectCommit()

	_, err := mockDBManager.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		return nil, retireOrphanedVMScanArtifacts(tx, "tenant-1", "shared-account")
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
