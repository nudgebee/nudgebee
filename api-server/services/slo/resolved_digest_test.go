package slo

import (
	"testing"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What notifications-server's SLO digest model (SLOAlertParams) refuses to
// build without. One item that fails it fails the whole channel's digest, so
// every item this package queues for the digest must carry them all.
var sloDigestRequiredFields = []string{
	"account_id", "account_name", "namespace", "workload", "status", "slo_name", "slo_type",
	"slo_target", "current_value", "firing_since", "bad_event_count", "good_event_count", "threshold",
}

const closeSLOEventSQL = "UPDATE events SET status = 'RESOLVED'"

type publishedMessage struct {
	exchange string
	queue    string
	message  map[string]any
}

// capturePublishes records what the package hands to the broker.
func capturePublishes(t *testing.T) *[]publishedMessage {
	t.Helper()
	sent := []publishedMessage{}
	orig := mqPublish
	mqPublish = func(exchange string, queue string, message any, _ ...common.MqPublishOption) error {
		body, _ := message.(map[string]any)
		sent = append(sent, publishedMessage{exchange: exchange, queue: queue, message: body})
		return nil
	}
	t.Cleanup(func() { mqPublish = orig })
	return &sent
}

// digestItems are the parameters of the messages sent to notifications-server.
func digestItems(t *testing.T, sent []publishedMessage) []map[string]any {
	t.Helper()
	items := []map[string]any{}
	for _, m := range sent {
		if m.queue != config.Config.RabbitMqNotificationsQueue {
			continue
		}
		assert.Equal(t, config.Config.RabbitMqNotificationsExchange, m.exchange)
		assert.Equal(t, "notification", m.message["kind"])
		assert.Equal(t, "slo", m.message["source"])
		assert.Equal(t, "slo_alert", m.message["type"])
		assert.Equal(t, testenv.FakeTenantID, m.message["tenant_id"])
		parameters, ok := m.message["parameters"].(map[string]any)
		require.True(t, ok, "digest item has no parameters")
		items = append(items, parameters)
	}
	return items
}

// resolvedEventNotices are the event ids sent to the event pipeline as resolved,
// the publish every close has always made.
func resolvedEventNotices(sent []publishedMessage) []any {
	ids := []any{}
	for _, m := range sent {
		if m.queue == config.Config.RabbitMqEventPostProcessQueue && m.message["notify_resolved"] == true {
			ids = append(ids, m.message["event_id"])
		}
	}
	return ids
}

func setResolvedDigestFlag(t *testing.T, enabled bool) {
	t.Helper()
	prev := config.Config.FeatureSLOResolvedDigestEnabled
	config.Config.FeatureSLOResolvedDigestEnabled = enabled
	t.Cleanup(func() { config.Config.FeatureSLOResolvedDigestEnabled = prev })
}

// mockSLOMetastore serves the metastore from sqlmock, to code handed the manager
// and to code that looks it up. sqlmock fails on any statement not expected.
func mockSLOMetastore(t *testing.T) (*database.DatabaseManager, sqlmock.Sqlmock) {
	t.Helper()
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
	return manager, mock
}

// expectCloseSLOEvent expects the close of fingerprint's FIRING event; closedIds
// are the rows the UPDATE changed.
func expectCloseSLOEvent(mock sqlmock.Sqlmock, fingerprint string, closedIds ...string) {
	rows := sqlmock.NewRows([]string{"id"})
	for _, id := range closedIds {
		rows.AddRow(id)
	}
	mock.ExpectQuery(closeSLOEventSQL).
		WithArgs(testenv.FakeTenantID, testenv.FakeAccountID, fingerprint, sqlmock.AnyArg()).
		WillReturnRows(rows)
}

func latencyConfig() DBSLOConfig {
	return DBSLOConfig{
		Id: "cfg-1", CloudAccountId: testenv.FakeAccountID, TenantId: testenv.FakeTenantID,
		Name: "latency", WorkloadName: "checkout", Namespace: "prod", Goal: 0.99, ThresholdBucket: 500, Window: 3600,
	}
}

func recoveredReport() SLOReport {
	return SLOReport{
		Workload: "checkout", Namespace: "prod", Valid: true, Gap: 0.25, BadEventsCount: 3, GoodEventsCount: 997,
		EndTime: float64(time.Date(2026, 7, 14, 6, 50, 12, 0, time.UTC).Unix()),
	}
}

func slosAdminContext(t *testing.T, mock sqlmock.Sqlmock) *security.RequestContext {
	t.Helper()
	mock.ExpectQuery("SELECT id FROM cloud_accounts WHERE tenant").
		WithArgs(testenv.FakeTenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testenv.FakeAccountID))
	ctx := security.NewRequestContextForUserTenant(testenv.FakeUserID, testenv.FakeTenantID, nil, nil, nil)
	require.NotNil(t, ctx.GetSecurityContext())
	return ctx
}

// The digest keeps one line per finding_id, its latest state, so a firing item
// and the resolve that follows it must name the same finding: the SLO event's
// own finding id, which GenerateSLOEvent sets to sloFingerprint and
// closeSLOEventByFingerprint closes by.
func TestTriggerNotification_FiringItemCarriesTheEventFindingId(t *testing.T) {
	_, mock := mockSLOMetastore(t)
	sent := capturePublishes(t)
	mock.ExpectQuery("FROM slo_report").WillReturnRows(sqlmock.NewRows([]string{"id"}))

	firing := recoveredReport()
	firing.Alert = true
	require.NoError(t, triggerNotification(latencyConfig(), firing, "prod-eu"))

	items := digestItems(t, *sent)
	require.Len(t, items, 1)
	assert.Equal(t, "FIRING", items[0]["status"])
	assert.Equal(t, sloFingerprint(testenv.FakeAccountID, "latency", "checkout", "prod"), items[0]["finding_id"])
	for _, field := range sloDigestRequiredFields {
		assert.Contains(t, items[0], field)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

// An SLO that recovers joins the next SLO digest as a RESOLVED item: the item
// the firing path publishes, under the same finding_id, filled from the config
// and the report the evaluation already holds.
func TestCloseSLOEventIfRecovered_PublishesOneResolvedDigestItem(t *testing.T) {
	dbms, mock := mockSLOMetastore(t)
	sent := capturePublishes(t)
	setResolvedDigestFlag(t, true)
	fingerprint := sloFingerprint(testenv.FakeAccountID, "latency", "checkout", "prod")
	expectCloseSLOEvent(mock, fingerprint, "evt-1")

	before := time.Now().UTC().Truncate(time.Second)
	closeSLOEventIfRecovered(dbms, latencyConfig(), []SLOReport{recoveredReport()}, "prod-eu")

	items := digestItems(t, *sent)
	require.Len(t, items, 1)
	item := items[0]
	endsAt, err := time.Parse(time.RFC3339, item["ends_at"].(string))
	require.NoError(t, err, "ends_at must be RFC 3339")
	assert.False(t, endsAt.Before(before), "ends_at %s is before the close", endsAt)
	assert.WithinDuration(t, time.Now().UTC(), endsAt, time.Minute)
	delete(item, "ends_at")
	assert.Equal(t, map[string]any{
		"account_id":       testenv.FakeAccountID,
		"account_name":     "prod-eu",
		"namespace":        "prod",
		"workload":         "checkout",
		"slo_name":         "latency",
		"slo_type":         "latency",
		"status":           "RESOLVED",
		"finding_id":       fingerprint,
		"slo_target":       0.99,
		"current_value":    "0.25",
		"firing_since":     "2026-07-14 06:00:00",
		"threshold":        500.0,
		"bad_event_count":  3,
		"good_event_count": 997,
	}, item)
	for _, field := range sloDigestRequiredFields {
		assert.Contains(t, item, field)
	}
	// The event pipeline still hears about the close, as before.
	assert.Equal(t, []any{"evt-1"}, resolvedEventNotices(*sent))
	assert.Len(t, *sent, 2)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCloseSLOEventIfRecovered_FlagOffPublishesNoDigestItem(t *testing.T) {
	dbms, mock := mockSLOMetastore(t)
	sent := capturePublishes(t)
	setResolvedDigestFlag(t, false)
	expectCloseSLOEvent(mock, sloFingerprint(testenv.FakeAccountID, "latency", "checkout", "prod"), "evt-1")

	closeSLOEventIfRecovered(dbms, latencyConfig(), []SLOReport{recoveredReport()}, "prod-eu")

	assert.Empty(t, digestItems(t, *sent))
	assert.Equal(t, []any{"evt-1"}, resolvedEventNotices(*sent))
	assert.Len(t, *sent, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A healthy SLO reports OK on every run. Only the run that closes a FIRING event
// is a recovery; the ones after it change no row and must publish nothing.
func TestCloseSLOEventIfRecovered_NothingClosedPublishesNothing(t *testing.T) {
	dbms, mock := mockSLOMetastore(t)
	sent := capturePublishes(t)
	setResolvedDigestFlag(t, true)
	expectCloseSLOEvent(mock, sloFingerprint(testenv.FakeAccountID, "latency", "checkout", "prod"))

	closeSLOEventIfRecovered(dbms, latencyConfig(), []SLOReport{recoveredReport()}, "prod-eu")

	assert.Empty(t, *sent)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Disabling an SLO closes its open event, but the SLO did not recover: the
// digest must not announce it as resolved.
func TestCreateOrUpdateSLOConfig_DisableClosesWithoutADigestItem(t *testing.T) {
	_, mock := mockSLOMetastore(t)
	sent := capturePublishes(t)
	setResolvedDigestFlag(t, true)
	ctx := slosAdminContext(t, mock)
	mock.ExpectExec("INSERT INTO slo_config").WillReturnResult(sqlmock.NewResult(0, 1))
	expectCloseSLOEvent(mock, sloFingerprint(testenv.FakeAccountID, "latency", "checkout", "prod"), "evt-1")

	_, err := CreateOrUpdateSLOConfig(ctx, SLORequest{
		AccountId: testenv.FakeAccountID, WorkloadName: "checkout", Namespace: "prod",
		Config: []SLOConfigRequest{{Name: "latency", Threshold: 500, Goal: 0.99, Enabled: false}},
	})

	require.NoError(t, err)
	assert.Empty(t, digestItems(t, *sent))
	assert.Equal(t, []any{"evt-1"}, resolvedEventNotices(*sent))
	assert.Len(t, *sent, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Deleting an SLO closes its open event too, and is no recovery either.
func TestDeleteSLOConfig_ClosesWithoutADigestItem(t *testing.T) {
	_, mock := mockSLOMetastore(t)
	sent := capturePublishes(t)
	setResolvedDigestFlag(t, true)
	ctx := slosAdminContext(t, mock)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT "name" FROM slo_config`).WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("latency"))
	mock.ExpectExec("DELETE FROM slo_report").WillReturnResult(sqlmock.NewResult(0, 4))
	mock.ExpectExec("DELETE FROM slo_config").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	expectCloseSLOEvent(mock, sloFingerprint(testenv.FakeAccountID, "latency", "checkout", "prod"), "evt-1")

	_, err := DeleteSLOConfig(ctx, SLODeleteRequest{AccountId: testenv.FakeAccountID, WorkloadName: "checkout", Namespace: "prod"})

	require.NoError(t, err)
	assert.Empty(t, digestItems(t, *sent))
	assert.Equal(t, []any{"evt-1"}, resolvedEventNotices(*sent))
	assert.Len(t, *sent, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}
