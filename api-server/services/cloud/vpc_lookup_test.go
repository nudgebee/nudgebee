package cloud

import (
	"errors"
	"regexp"
	"testing"

	"nudgebee/services/internal/database"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetVpcIdForResourceScopesByTenantAccounts pins #36387: the lookup must
// reach cloud_resourses through the tenant's accounts, so Postgres can use the
// (account, resourse_id, ...) unique index. With tenant = $2 alone it scanned
// the tenant's whole cloud_resourses set.
func TestGetVpcIdForResourceScopesByTenantAccounts(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgresql")}, nil
	})
	t.Cleanup(func() {
		database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
			return nil, errors.New("no metastore in unit tests")
		})
		_ = rawDB.Close()
	})

	mock.ExpectQuery(regexp.QuoteMeta("WHERE account IN (SELECT id FROM cloud_accounts WHERE tenant = $2) AND resourse_id = $1 AND tenant = $2")).
		WithArgs("i-0abc", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"regexp_match"}).AddRow("vpc-0123"))

	assert.Equal(t, "vpc-0123", getVpcIdForResource("i-0abc", "tenant-1"))
	assert.NoError(t, mock.ExpectationsWereMet())
}
