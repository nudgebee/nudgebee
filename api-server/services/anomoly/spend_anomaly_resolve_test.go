package anomoly

import (
	"database/sql/driver"
	"regexp"
	"testing"
	"time"

	"nudgebee/services/config"
	"nudgebee/services/internal/database"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	resolveTestTenant  = "5c0a2b6e-0d7b-4e57-9d0a-3c4f7a1f9b10"
	resolveTestAccount = "0b6f4f0a-52a1-4d5e-9b53-6d2f1c1f7a11"
	resolveTestAnomaly = "8422a701-a0a2-443b-8fc0-692a4360de4a"
)

// Positions of the columns under test in InsertEvent's VALUES list ($1-based).
const (
	eventsArgCount    = 30
	eventsArgStatus   = 17
	eventsArgStartsAt = 20
	eventsArgEndsAt   = 23
)

// captureArg records the value bound to one placeholder of the events upsert.
type captureArg struct{ got driver.Value }

func (c *captureArg) Match(v driver.Value) bool {
	c.got = v
	return true
}

// A spend anomaly that recovers must reach the events table as a RESOLVED update
// of a condition that ran and stopped: starts_at on the day it began, ends_at
// after it. resolveAnomalyEvent used to stamp starts_at with the evaluation time,
// which is later than the day the spend returned to normal, so the interval was
// inverted. InsertEvent reads an inverted interval as a moment that has already
// passed, rewrites the status to CLOSED and re-delivers the row to event-trigger
// workflows instead of publishing the resolved notification.
//
// Driven through resolveRecoveredAnomalies so the end date is the one production
// computes. sqlmock fails on any statement not expected.
func TestResolveRecoveredAnomalies_SendsTheAnomalyIntervalAsResolved(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	manager := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return manager, nil
	})
	// Drop the stub so the live-database tests in this package get the real
	// metastore back rather than this closed handle.
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

	prevPointInTime := config.Config.FeatureEventPointInTimeCloseEnabled
	config.Config.FeatureEventPointInTimeCloseEnabled = true // the production default
	t.Cleanup(func() { config.Config.FeatureEventPointInTimeCloseEnabled = prevPointInTime })

	// Spend rose on the 20th for three days and has been normal on the 23rd and
	// 24th, which meets the two-day resolution threshold on the 24th.
	targetDate := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	cfg := spendAnomalyConfig{BaselineDays: 30, ResolutionConsecutiveDays: 2, TargetDate: &targetDate}
	reference := `{"frozen_mean": 100, "frozen_stddev": 10, "start_date": "2026-09-20", "total_impact": 240,
		"max_daily_impact": 90, "anomaly_days": 3, "consecutive_normal_days": 2}`

	mock.ExpectQuery("FROM anomaly").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant", "account_id", "name", "namespace", "anomaly_type", "anomaly_status", "reference_value", "current_value"}).
			AddRow(resolveTestAnomaly, resolveTestTenant, resolveTestAccount, "billing-prod", nil, string(MetricAnomolyTypeCloudSpendAccount), string(SpendAnomalyStatusOpen), []byte(reference), 105.0))
	mock.ExpectExec("UPDATE anomaly SET anomaly_status").WillReturnResult(sqlmock.NewResult(0, 1))
	// Evidence queries: the root-cause breakdown and the spend trend. Empty is fine.
	mock.ExpectQuery("yesterday_by_service").
		WillReturnRows(sqlmock.NewRows([]string{"name", "yesterday", "avg", "change", "is_new"}))
	mock.ExpectQuery("spend_date").
		WillReturnRows(sqlmock.NewRows([]string{"spend_date", "daily_amount"}))

	status, startsAt, endsAt := &captureArg{}, &captureArg{}, &captureArg{}
	args := make([]driver.Value, eventsArgCount)
	for i := range args {
		args[i] = sqlmock.AnyArg()
	}
	args[eventsArgStatus-1], args[eventsArgStartsAt-1], args[eventsArgEndsAt-1] = status, startsAt, endsAt
	// is_insert = false: the FIRING event for this anomaly is already stored.
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO events")).WithArgs(args...).
		WillReturnRows(sqlmock.NewRows([]string{"is_insert"}).AddRow(false))

	resolveRecoveredAnomalies(nil, manager, resolveTestAccount, resolveTestTenant, "billing-prod", "AWS", cfg)

	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, "RESOLVED", status.got,
		"the event must be stored with the status the producer sent; CLOSED means InsertEvent took it for a point-in-time event and skipped the resolved notification")

	started, ok := startsAt.got.(time.Time)
	require.True(t, ok, "starts_at = %v", startsAt.got)
	ended, ok := endsAt.got.(time.Time)
	require.True(t, ok, "ends_at = %v", endsAt.got)
	assert.Equal(t, time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC), started, "starts_at is the day the anomaly began")
	assert.Equal(t, time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC), ended, "ends_at is the first day spend was back to normal")
	assert.True(t, ended.After(started), "ends_at %s must be after starts_at %s", ended, started)
}
