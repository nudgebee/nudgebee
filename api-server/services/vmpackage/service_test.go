package vmpackage

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyCloudResource_NotFound(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery("SELECT resourse_id FROM cloud_resourses").
		WithArgs("resource-1", "account-1", "tenant-1").
		WillReturnError(sql.ErrNoRows)

	_, err := verifyCloudResource("resource-1", "account-1", "tenant-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found for this account")
}

func TestVerifyCloudResource_DBErrorNotMaskedAsNotFound(t *testing.T) {
	mock := withMockDB(t)
	dbErr := errors.New("connection reset by peer")
	mock.ExpectQuery("SELECT resourse_id FROM cloud_resourses").
		WithArgs("resource-1", "account-1", "tenant-1").
		WillReturnError(dbErr)

	_, err := verifyCloudResource("resource-1", "account-1", "tenant-1")
	require.Error(t, err)
	// A real DB failure must surface as such, not get mislabeled "not found"
	// — the latter sends operators chasing the wrong problem.
	assert.NotContains(t, err.Error(), "not found for this account")
	assert.Contains(t, err.Error(), "connection reset by peer")
}

func TestVerifyCloudResource_Found(t *testing.T) {
	mock := withMockDB(t)
	rows := sqlmock.NewRows([]string{"resourse_id"}).AddRow("vm-172.31.0.11")
	mock.ExpectQuery("SELECT resourse_id FROM cloud_resourses").
		WithArgs("resource-1", "account-1", "tenant-1").
		WillReturnRows(rows)

	ip, err := verifyCloudResource("resource-1", "account-1", "tenant-1")
	assert.NoError(t, err)
	assert.Equal(t, "172.31.0.11", ip)
}
