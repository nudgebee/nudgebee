package scan_orchestrator

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const heartbeatAccountStatusQuery = `SELECT status FROM cloud_accounts WHERE id = \$1 AND tenant = \$2`

// A disabled account's agent keeps sending telemetry. Its heartbeat must not
// queue scans, because each scan's upsert re-opens archived recommendations.
func TestEvaluateHeartbeat_SkipsAccountNotActive(t *testing.T) {
	mock := mockMetastore(t)
	mock.ExpectQuery(heartbeatAccountStatusQuery).
		WithArgs("account-1", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("disabled"))

	result := EvaluateHeartbeat(persistTestCtx(), "account-1", "tenant-1")

	assert.Equal(t, HeartbeatResult{Evaluated: false, Reason: "account not active"}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEvaluateHeartbeat_SkipsAccountNotInTenant(t *testing.T) {
	mock := mockMetastore(t)
	mock.ExpectQuery(heartbeatAccountStatusQuery).
		WithArgs("account-1", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"status"}))

	result := EvaluateHeartbeat(persistTestCtx(), "account-1", "tenant-1")

	assert.Equal(t, HeartbeatResult{Evaluated: false, Reason: "account not active"}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEvaluateHeartbeat_StatusReadErrorQueuesNothing(t *testing.T) {
	mock := mockMetastore(t)
	mock.ExpectQuery(heartbeatAccountStatusQuery).
		WithArgs("account-1", "tenant-1").
		WillReturnError(errors.New("connection reset by peer"))

	result := EvaluateHeartbeat(persistTestCtx(), "account-1", "tenant-1")

	assert.Equal(t, HeartbeatResult{Evaluated: false, Reason: "account status read failed"}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEvaluateHeartbeat_ActiveAccountReadsScheduleJobs(t *testing.T) {
	mock := mockMetastore(t)
	mock.ExpectQuery(heartbeatAccountStatusQuery).
		WithArgs("account-1", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("active"))
	mock.ExpectQuery("FROM agent").
		WillReturnError(errors.New("stop here"))

	result := EvaluateHeartbeat(persistTestCtx(), "account-1", "tenant-1")

	assert.Equal(t, HeartbeatResult{Evaluated: false, Reason: "schedule_jobs read failed"}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}
