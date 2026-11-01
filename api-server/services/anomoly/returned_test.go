package anomoly

import (
	"database/sql/driver"
	"regexp"
	"strings"
	"testing"
	"time"

	"nudgebee/services/config"
	"nudgebee/services/event"
	"nudgebee/services/internal/database"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	returnedTestStoredEvent = "3f0e6c1e-6a52-4b0c-8a3f-6c1d2e9b7a12"

	storedStatusLookup = "SELECT id, status FROM events"
	eventsUpsert       = "INSERT INTO events"
)

// returnedOutcome is what one upsertAnomalyEvent call did.
type returnedOutcome struct {
	// statements: which of the two statements reached the database, in order.
	statements []string
	// published: the post-process messages the producer itself published.
	// InsertEvent's own publish goes straight to the broker and is not counted.
	published []map[string]any
}

// setupReturnedTest points the Metastore at a fresh sqlmock, sets the flag and
// records the statements run and the messages published.
func setupReturnedTest(t *testing.T, flagOn bool) (*database.DatabaseManager, sqlmock.Sqlmock, *returnedOutcome) {
	t.Helper()
	out := &returnedOutcome{}

	// sqlmock answers an unexpected statement with an error, which the producer
	// logs and survives, so a stray lookup would go unnoticed. Record every
	// statement the mock is asked to match instead.
	recording := sqlmock.QueryMatcherFunc(func(expected, actual string) error {
		switch {
		case strings.Contains(actual, storedStatusLookup):
			out.statements = append(out.statements, storedStatusLookup)
		case strings.Contains(actual, eventsUpsert):
			out.statements = append(out.statements, eventsUpsert)
		}
		return sqlmock.QueryMatcherRegexp.Match(expected, actual)
	})
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(recording))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	manager := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return manager, nil
	})
	// Drop the stub so the live-database tests in this package get the real
	// metastore back rather than this closed handle.
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

	prevFlag := config.Config.FeatureAnomalyReturnedDigestEnabled
	config.Config.FeatureAnomalyReturnedDigestEnabled = flagOn
	t.Cleanup(func() { config.Config.FeatureAnomalyReturnedDigestEnabled = prevFlag })

	orig := publishEventPostProcess
	publishEventPostProcess = func(message map[string]any) { out.published = append(out.published, message) }
	t.Cleanup(func() { publishEventPostProcess = orig })

	return manager, mock, out
}

// expectEventsUpsert expects InsertEvent's upsert. isInsert is what it reports:
// true for a new row, false for an update of the stored finding.
func expectEventsUpsert(mock sqlmock.Sqlmock, isInsert bool) {
	args := make([]driver.Value, eventsArgCount)
	for i := range args {
		args[i] = sqlmock.AnyArg()
	}
	mock.ExpectQuery(regexp.QuoteMeta(eventsUpsert)).WithArgs(args...).
		WillReturnRows(sqlmock.NewRows([]string{"is_insert"}).AddRow(isInsert))
}

// returnedTestEvent is the event GenerateAnomalyEvent builds for a CPU anomaly.
func returnedTestEvent() event.Event {
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	fingerprint := anomalyFingerprint(resolveTestAccount, MetricAnomolyTypeCPU, "checkout", "prod")
	return event.Event{
		AccountId:   resolveTestAccount,
		Tenant:      resolveTestTenant,
		Source:      "anomaly",
		Title:       "CPU Anomaly detected for checkout in namespace prod",
		FindingType: "Anomaly",
		Category:    "Anomaly",
		Priority:    "HIGH",
		FindingId:   fingerprint,
		Fingerprint: fingerprint,
		Status:      "FIRING",
		StartsAt:    &now,
	}
}

// runUpsertAnomalyEvent drives upsertAnomalyEvent with the flag as given.
// storedStatus is the status of the event already stored for the finding; ""
// means there is none, so the upsert inserts.
func runUpsertAnomalyEvent(t *testing.T, flagOn bool, storedStatus string) *returnedOutcome {
	t.Helper()
	manager, mock, out := setupReturnedTest(t, flagOn)
	eventObj := returnedTestEvent()

	if flagOn {
		rows := sqlmock.NewRows([]string{"id", "status"})
		if storedStatus != "" {
			rows.AddRow(returnedTestStoredEvent, storedStatus)
		}
		mock.ExpectQuery(regexp.QuoteMeta(storedStatusLookup)).
			WithArgs(resolveTestTenant, resolveTestAccount, eventObj.FindingId).
			WillReturnRows(rows)
	}
	expectEventsUpsert(mock, storedStatus == "")

	require.NoError(t, upsertAnomalyEvent(manager, eventObj))
	require.NoError(t, mock.ExpectationsWereMet())
	return out
}

// An anomaly that had cleared and fires again is announced once. The message
// names the stored row: InsertEvent returns the id it generated for an insert
// even when the upsert updated that row instead.
func TestUpsertAnomalyEvent_AnnouncesAnAnomalyThatHadCleared(t *testing.T) {
	for _, stored := range []string{"RESOLVED", "CLOSED", "resolved", "Closed"} {
		t.Run(stored, func(t *testing.T) {
			out := runUpsertAnomalyEvent(t, true, stored)

			assert.Equal(t, []string{storedStatusLookup, eventsUpsert}, out.statements,
				"the stored status is read before the upsert overwrites it")
			assert.Equal(t, []map[string]any{{
				"event_id":      returnedTestStoredEvent,
				"workflow_only": true,
				"phase":         "returned",
			}}, out.published)
		})
	}
}

// The detection job re-confirms an open anomaly every hour. That upsert finds
// the row still FIRING and must stay as silent as it is today.
func TestUpsertAnomalyEvent_HourlyReDetectionOfAnOpenAnomalyStaysSilent(t *testing.T) {
	for _, stored := range []string{"FIRING", "firing"} {
		t.Run(stored, func(t *testing.T) {
			out := runUpsertAnomalyEvent(t, true, stored)

			assert.Equal(t, []string{storedStatusLookup, eventsUpsert}, out.statements)
			assert.Empty(t, out.published)
		})
	}
}

// A first firing has no stored row; InsertEvent sends it through the full
// pipeline, which announces it.
func TestUpsertAnomalyEvent_FirstFiringPublishesNothingNew(t *testing.T) {
	out := runUpsertAnomalyEvent(t, true, "")

	assert.Equal(t, []string{storedStatusLookup, eventsUpsert}, out.statements)
	assert.Empty(t, out.published)
}

// Off is the default: no lookup, no message, whatever the stored status.
func TestUpsertAnomalyEvent_FlagOffDoesNoLookupAndPublishesNothing(t *testing.T) {
	for _, stored := range []string{"RESOLVED", "CLOSED", "FIRING", ""} {
		t.Run("stored "+stored, func(t *testing.T) {
			out := runUpsertAnomalyEvent(t, false, stored)

			assert.Equal(t, []string{eventsUpsert}, out.statements, "the flag gates the extra SELECT too")
			assert.Empty(t, out.published)
		})
	}
}

// A failed lookup must not cost the event its upsert: it is stored as before
// and the return goes unannounced.
func TestUpsertAnomalyEvent_FailedLookupStillStoresTheEvent(t *testing.T) {
	manager, mock, out := setupReturnedTest(t, true)

	mock.ExpectQuery(regexp.QuoteMeta(storedStatusLookup)).WillReturnError(assert.AnError)
	expectEventsUpsert(mock, false)

	require.NoError(t, upsertAnomalyEvent(manager, returnedTestEvent()))
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, []string{storedStatusLookup, eventsUpsert}, out.statements)
	assert.Empty(t, out.published)
}

// A failed upsert announces nothing: there is no re-fired event to point at.
func TestUpsertAnomalyEvent_FailedUpsertPublishesNothing(t *testing.T) {
	manager, mock, out := setupReturnedTest(t, true)

	mock.ExpectQuery(regexp.QuoteMeta(storedStatusLookup)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(returnedTestStoredEvent, "RESOLVED"))
	mock.ExpectQuery(regexp.QuoteMeta(eventsUpsert)).WillReturnError(assert.AnError)

	require.Error(t, upsertAnomalyEvent(manager, returnedTestEvent()))
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Empty(t, out.published)
}
