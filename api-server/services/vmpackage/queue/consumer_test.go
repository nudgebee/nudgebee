package queue

import (
	"context"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDBManager is registered once for this package's test binary. See
// vmpackage/persist_test.go for why the pointer (not a fresh manager per
// test) must be reused.
var mockDBManager = &database.DatabaseManager{}

func init() {
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return mockDBManager, nil
	})
}

func withMockDB(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mockDBManager.Db = sqlx.NewDb(db, "postgres")
	return mock
}

func TestProcessVMScanMessage_MalformedMessage(t *testing.T) {
	err := processVMScanMessage(context.Background(), []byte("not json"))
	assert.NoError(t, err, "malformed messages should be acked, not requeued")
}

func TestProcessVMScanMessage_DatasourceNoLongerEligible(t *testing.T) {
	mock := withMockDB(t)
	rows := sqlmock.NewRows([]string{"tenant_id", "labels"})
	mock.ExpectQuery("SELECT i.tenant_id::varchar AS tenant_id").
		WithArgs("int-1", "account-1").
		WillReturnRows(rows)

	message := VMScanMessage{IntegrationID: "int-1", TenantID: "tenant-1", AccountID: "account-1", Source: "cron"}
	data, err := common.MarshalJson(message)
	require.NoError(t, err)

	err = processVMScanMessage(context.Background(), data)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
