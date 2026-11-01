package event

import (
	"context"
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

// An event that arrives already ended must be stored CLOSED. Cloud webhooks
// describe instants -- "ECS Task State Change", "GKE Resource Upgrade Started",
// "Kubernetes Secret Modified" -- and say so by sending ends_at == starts_at.
// Stored FIRING they assert an ongoing condition the producer never claimed,
// and no resolve ever follows for a moment that has passed: on an anonymized production
// estate 18,357 `ECS Task State Change` events sat FIRING at a median age of 52
// days, 69% of everything stuck open there.
func TestIsPointInTime(t *testing.T) {
	base := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	later := base.Add(30 * time.Minute)
	earlier := base.Add(-30 * time.Minute)

	for _, tc := range []struct {
		name        string
		findingType string
		startsAt    *time.Time
		endsAt      *time.Time
		want        bool
		why         string
	}{
		{
			name: "zero-length interval is a moment", findingType: "issue",
			startsAt: &base, endsAt: &base, want: true,
			why: "ECS Task State Change and friends send ends_at == starts_at",
		},
		{
			name: "inverted interval is still a moment", findingType: "issue",
			startsAt: &base, endsAt: &earlier, want: true,
			why: "an end before the start cannot describe an ongoing condition",
		},
		{
			name: "configuration_change with no end time", findingType: "configuration_change",
			startsAt: &base, endsAt: nil, want: true,
			why: "describes a change that already happened (#36597)",
		},
		{
			name: "real interval is NOT point in time", findingType: "issue",
			startsAt: &base, endsAt: &later, want: false,
			why: "a condition that ran and stopped; its producer owns the resolve",
		},
		{
			name: "open-ended issue is NOT point in time", findingType: "issue",
			startsAt: &base, endsAt: nil, want: false,
			why: "the ordinary firing alert, which must stay open",
		},
		{
			name: "missing starts_at is NOT point in time", findingType: "issue",
			startsAt: nil, endsAt: &base, want: false,
			why: "nothing to compare against; never guess a close",
		},
		{
			name: "no timestamps at all is NOT point in time", findingType: "issue",
			startsAt: nil, endsAt: nil, want: false,
			why: "must not panic, and must not close",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPointInTime(tc.findingType, tc.startsAt, tc.endsAt); got != tc.want {
				t.Errorf("isPointInTime = %v, want %v (%s)", got, tc.want, tc.why)
			}
		})
	}
}

// Positions of status and nb_status in InsertEvent's VALUES list ($1-based).
const (
	insertEventArgCount    = 30
	insertEventArgStatus   = 17
	insertEventArgNbStatus = 30
)

// captureArg records the value InsertEvent bound to one placeholder.
type captureArg struct{ got driver.Value }

func (c *captureArg) Match(v driver.Value) bool {
	c.got = v
	return true
}

// insertEventOutcome is what one InsertEvent call stored and handed on.
type insertEventOutcome struct {
	status, nbStatus driver.Value
	// postProcessed: the event went to the full pipeline (triage, llm, notification).
	postProcessed bool
	// resolvedNotified: the event ids a resolved notification was published for.
	resolvedNotified []string
}

// runInsertEvent drives InsertEvent against a sqlmock metastore, with the
// point-in-time flag at its production default. isInsert is what the upsert
// reports: true for a new row, false for an update of a stored finding_id.
func runInsertEvent(t *testing.T, e Event, isInsert bool) insertEventOutcome {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	manager := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return manager, nil
	})

	prevPointInTime := config.Config.FeatureEventPointInTimeCloseEnabled
	config.Config.FeatureEventPointInTimeCloseEnabled = true
	t.Cleanup(func() { config.Config.FeatureEventPointInTimeCloseEnabled = prevPointInTime })

	var out insertEventOutcome
	orig := publishResolved
	publishResolved = func(_ context.Context, ids []string) { out.resolvedNotified = append(out.resolvedNotified, ids...) }
	t.Cleanup(func() { publishResolved = orig })

	status, nbStatus := &captureArg{}, &captureArg{}
	args := make([]driver.Value, insertEventArgCount)
	for i := range args {
		args[i] = sqlmock.AnyArg()
	}
	args[insertEventArgStatus-1], args[insertEventArgNbStatus-1] = status, nbStatus
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO events")).WithArgs(args...).
		WillReturnRows(sqlmock.NewRows([]string{"is_insert"}).AddRow(isInsert))

	id, err := InsertEvent(e, resolveTestEventID, WithDeferredPostProcess(func(string) { out.postProcessed = true }))
	require.NoError(t, err)
	require.Equal(t, resolveTestEventID, id)
	require.NoError(t, mock.ExpectationsWereMet())
	out.status, out.nbStatus = status.got, nbStatus.got
	return out
}

// InsertEvent stores an event that arrives already ended as CLOSED, whatever
// status its producer sent, and sends it through the pipeline once. The producers
// that depend on it: cloud alarms relayed as an instant (#38289), Datadog one-shot
// notifications that no Recovered will ever follow, and records born closed.
func TestInsertEvent_StoresAnEventThatArrivesAlreadyEndedClosed(t *testing.T) {
	at := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name, source string
		status       EventStatus
	}{
		{name: "cloud alarm sent as an instant, because no poll can end it", source: "AWS_CloudWatch_Alarm", status: EventStatusFiring},
		{name: "Datadog one-shot notification", source: "datadog_webhook", status: EventStatusFiring},
		{name: "resolve that names no stored event", source: "automation", status: EventStatusResolved},
		{name: "record born closed", source: "AWS_EventBridge", status: EventStatusClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := runInsertEvent(t, Event{
				Tenant: resolveTestTenant, AccountId: resolveTestAccount, FindingId: "already-ended-1",
				Source: tc.source, FindingType: "issue", Status: tc.status, StartsAt: &at, EndsAt: &at,
			}, true)

			assert.Equal(t, "CLOSED", out.status)
			assert.Equal(t, "RESOLVED", out.nbStatus)
			assert.True(t, out.postProcessed, "a new event still goes through the pipeline once")
			assert.Empty(t, out.resolvedNotified, "nothing was open, so there is no thread to reply to")
		})
	}
}

// An alert that is firing now has no end, and stays open. That holds for a cloud
// alarm too: the cloud collector sends one with no end time when a poll of the
// account can end it, by either delivery path.
func TestInsertEvent_StoresAFiringAlertOpen(t *testing.T) {
	at := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	for _, source := range []string{"datadog_webhook", "AWS_CloudWatch_Alarm", "AWS_EventBridge"} {
		t.Run(source, func(t *testing.T) {
			out := runInsertEvent(t, Event{
				Tenant: resolveTestTenant, AccountId: resolveTestAccount, FindingId: "firing-1",
				Source: source, FindingType: "issue", Status: EventStatusFiring, StartsAt: &at,
			}, true)

			assert.Equal(t, "FIRING", out.status)
			assert.Equal(t, "OPEN", out.nbStatus)
			assert.True(t, out.postProcessed)
			assert.Empty(t, out.resolvedNotified)
		})
	}
}

// A RESOLVED update of a stored event whose interval is real -- ends_at after
// starts_at, the shape a spend-anomaly resolve sends -- is a condition that ran
// and stopped. It keeps the status its producer sent and gets the resolved
// notification, and it is not sent through the pipeline a second time.
func TestInsertEvent_ResolveOfARealIntervalPublishesTheResolvedNotification(t *testing.T) {
	started := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	ended := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	out := runInsertEvent(t, Event{
		Tenant: resolveTestTenant, AccountId: resolveTestAccount, FindingId: "spend-anomaly-1",
		Source: "anomaly", FindingType: "Anomaly", Status: EventStatusResolved, StartsAt: &started, EndsAt: &ended,
	}, false)

	assert.Equal(t, "RESOLVED", out.status)
	assert.Equal(t, []string{resolveTestEventID}, out.resolvedNotified)
	assert.False(t, out.postProcessed)
}
