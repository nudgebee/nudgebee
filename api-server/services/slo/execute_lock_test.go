package slo

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func lockMock(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	raw, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	return sqlx.NewDb(raw, "postgres"), mock
}

func TestTryExecuteLock_TakesAndReleasesTheAdvisoryLock(t *testing.T) {
	db, mock := lockMock(t)
	mock.ExpectQuery(`SELECT pg_try_advisory_lock\(hashtext\(\$1\)\)`).WithArgs(executeLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
	mock.ExpectExec(`SELECT pg_advisory_unlock\(hashtext\(\$1\)\)`).WithArgs(executeLockKey).
		WillReturnResult(sqlmock.NewResult(0, 0))

	unlock, ok, err := tryExecuteLock(context.Background(), db)
	require.NoError(t, err)
	require.True(t, ok)
	unlock()
	assert.NoError(t, mock.ExpectationsWereMet(), "the lock is released on the connection that took it")
}

func TestTryExecuteLock_HeldElsewhereSkipsTheRun(t *testing.T) {
	db, mock := lockMock(t)
	mock.ExpectQuery(`SELECT pg_try_advisory_lock`).WithArgs(executeLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(false))

	unlock, ok, err := tryExecuteLock(context.Background(), db)
	require.NoError(t, err)
	assert.False(t, ok, "another pod's run holds it")
	assert.Nil(t, unlock)
	assert.NoError(t, mock.ExpectationsWereMet(), "nothing to unlock when the lock was not taken")
}

func TestConfigsOfPass_SplitsForecastsFromClassicSLOs(t *testing.T) {
	configs := []DBSLOConfig{
		{Id: "a", Name: "availability", Method: "good_bad_ratio"},
		{Id: "c", Name: "cpu_throttling", Method: forecastMethod},
		{Id: "l", Name: "latency", Method: "distribution_cut"},
	}
	ids := func(cs []DBSLOConfig) (out []string) {
		for _, c := range cs {
			out = append(out, c.Id)
		}
		return out
	}
	assert.Equal(t, []string{"a", "l"}, ids(configsOfPass(configs, false)), "classic SLOs, in order")
	assert.Equal(t, []string{"c"}, ids(configsOfPass(configs, true)), "forecasts, in their own pass")
}
