package integrations_test

// Lifecycle of Datadog monitor deliveries against a real Postgres, on the harness
// in event_webhook_scenario_test.go: the token lookup, the Datadog parser, routing
// and resolveEventQuery are all real. InvestigateEvent's stand-in inserts through
// the real event.InsertEvent, so its point-in-time rule decides the stored status
// exactly as in production, and hands a finding_id already stored to the real
// event.InvestigateEvent, whose existing-row branch updates that event. Skipped
// unless NB_TEST_PG_DSN is set (see that file).

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"nudgebee/services/config"
	"nudgebee/services/event"
	eventtypes "nudgebee/services/event/types"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	ddIntegration = "7a1d0c3e-0000-4000-8000-0000000000d1"
	ddToken       = "datadog-scenario-token"
	ddPath        = "/api/webhooks/datadog"
	ddMonitor     = "8249055"
)

func ddSetup(t *testing.T) (*sqlx.DB, *security.RequestContext) {
	t.Helper()
	db, sc := caSetup(t)
	_, err := db.Exec(`
insert into integrations values ('` + ddIntegration + `', '` + caTenant + `', 'datadog_webhook', 'datadog', 'enabled');
insert into integrations_cloud_accounts values ('` + ddIntegration + `', '` + caAccount + `');
insert into integration_config_values(integration_id, name, value) values ('` + ddIntegration + `', 'token', '` + ddToken + `');
alter table events
  add column title text, add column description text, add column aggregation_key text, add column priority text,
  add column subject_type text, add column subject_namespace text, add column subject_node text, add column service_key text,
  add column evidences jsonb, add column finding_type text, add column cluster text, add column labels jsonb,
  add column updated_at timestamp, add column cloud_resource_id text, add column category text, add column created_at timestamp,
  add column urgency text, add column subject_owner text, add column subject_owner_kind text, add column nb_status text,
  add column principal text;
alter table cloud_accounts add column status text;`)
	require.NoError(t, err)

	prevPointInTime, prevFn := config.Config.FeatureEventPointInTimeCloseEnabled, core.InvestigateEventFn
	config.Config.FeatureEventPointInTimeCloseEnabled = true // the production default
	// A finding_id already stored goes to the real InvestigateEvent, whose
	// existing-row branch updates that event and never adds or reopens one. A new
	// one is inserted by the real InsertEvent, without InvestigateEvent's
	// enrichment.
	core.InvestigateEventFn = func(sc *security.RequestContext, e eventtypes.Event, id string) (string, error) {
		var seen int
		if err := db.Get(&seen, `select count(*) from events where tenant = $1 and cloud_account_id = $2 and finding_id = $3`, e.Tenant, e.AccountId, e.FindingId); err != nil {
			return "", err
		}
		if seen > 0 {
			return event.InvestigateEvent(sc, e, id)
		}
		return event.InsertEvent(e, "", event.WithDeferredPostProcess(func(string) {}), event.WithoutWorkflowRefire())
	}
	t.Cleanup(func() {
		config.Config.FeatureEventPointInTimeCloseEnabled = prevPointInTime
		core.InvestigateEventFn = prevFn
	})
	return db, sc
}

// ddDelivery is a monitor notification in the shape of the captured
// error-tracking deliveries in datadog_webhook_test.go: a template without
// $AGGREG_KEY, the group only in the title and the monitor-status link, and
// last_updated equal to date as Datadog sends it.
func ddDelivery(id, transition, issue string, ms int64) string {
	group := url.QueryEscape("issue.id:" + issue)
	return fmt.Sprintf(`{"id":"%s","last_updated":"%d","event_type":"error_tracking_alert",
		"title":"[P2] [%s on {issue.id:%s}] [New issue] New issue to review","date":"%d",
		"org":{"id":"1300568798","name":"Nudgebee"},
		"body":"%%%%%%\nA new issue has been detected.\n\n- - -\n\n[[Monitor Status](https://us5.datadoghq.com/monitors/%s?group=%s&from_ts=%d&to_ts=%d&event_id=%s&link_source=monitor_notif)] · [[Edit Monitor](https://us5.datadoghq.com/monitors/%s/edit?link_source=monitor_notif)]\n%%%%%%"}`,
		id, ms, transition, issue, ms, ddMonitor, group, ms-900000, ms+300000, id, ddMonitor)
}

type ddEvent struct {
	FindingId   string `db:"finding_id"`
	Fingerprint string `db:"fingerprint"`
	Status      string `db:"status"`
	NbStatus    string `db:"nb_status"`
	Ended       bool   `db:"ended"`
	Priority    string `db:"priority"`
	// LastDelivery is the $ID of the latest delivery written to the event.
	LastDelivery string `db:"last_delivery"`
}

// ddEvents returns the stored events keyed by the $ID of the delivery that opened
// each: the finding_id, or its part before the firing key ("<$ID>-<key>").
func ddEvents(t *testing.T, db *sqlx.DB) map[string]ddEvent {
	t.Helper()
	var rows []ddEvent
	require.NoError(t, db.Select(&rows, `select finding_id, coalesce(fingerprint, '') as fingerprint, status,
		coalesce(nb_status, '') as nb_status, ends_at is not null as ended, coalesce(priority, '') as priority,
		coalesce(labels->>'nb_webhook_id', '') as last_delivery from events`))
	byId := make(map[string]ddEvent, len(rows))
	for _, r := range rows {
		id, _, _ := strings.Cut(r.FindingId, "-")
		require.NotContains(t, byId, id, "two events opened by delivery %s", id)
		byId[id] = r
	}
	return byId
}

func ddDeliver(t *testing.T, sc *security.RequestContext, payload string) {
	t.Helper()
	_, err := deliver(t, sc, ddPath, ddToken, payload)
	require.NoError(t, err)
}

// A [Triggered] delivery is an alert that is firing now. With last_updated passed
// on as ends_at it arrived as a zero-length interval, and InsertEvent stored it
// CLOSED / RESOLVED on arrival.
func TestScenarioDatadogFiringIsStoredOpen(t *testing.T) {
	db, sc := ddSetup(t)
	ddDeliver(t, sc, ddDelivery("1001", "Triggered", "aaaa", 1751802855000))

	ev := ddEvents(t, db)["1001"]
	assert.Equal(t, string(eventtypes.EventStatusFiring), ev.Status)
	assert.Equal(t, "OPEN", ev.NbStatus)
	assert.False(t, ev.Ended, "a firing alert has no end yet")
}

// Two groups of one monitor fire, each on an event of its own; a re-notification
// of one updates that group's event. The recovery of one group closes its event
// and leaves the other open until its own recovery.
func TestScenarioDatadogRecoveryClosesOnlyItsGroup(t *testing.T) {
	db, sc := ddSetup(t)
	ddDeliver(t, sc, ddDelivery("1001", "Triggered", "aaaa", 1751802855000))
	ddDeliver(t, sc, ddDelivery("1002", "Triggered", "bbbb", 1751802860000))
	ddDeliver(t, sc, ddDelivery("1003", "Re-Triggered", "aaaa", 1751806455000))

	ev := ddEvents(t, db)
	require.Len(t, ev, 2, "one event per group")
	assert.NotEqual(t, ev["1001"].Fingerprint, ev["1002"].Fingerprint, "each group has its own fingerprint")
	assert.Equal(t, "1003", ev["1001"].LastDelivery, "the re-notification lands on its own group's event")
	assert.Equal(t, "1002", ev["1002"].LastDelivery)
	for _, id := range []string{"1001", "1002"} {
		assert.Equal(t, string(eventtypes.EventStatusFiring), ev[id].Status, id)
	}

	ddDeliver(t, sc, ddDelivery("1004", "Recovered", "aaaa", 1751810055000))
	ev = ddEvents(t, db)
	require.Len(t, ev, 2, "a recovery closes events, it does not add one")
	assert.Equal(t, "CLOSED", ev["1001"].Status)
	assert.True(t, ev["1001"].Ended)
	assert.Equal(t, string(eventtypes.EventStatusFiring), ev["1002"].Status, "another group's recovery must leave this one open")

	ddDeliver(t, sc, ddDelivery("1005", "Recovered", "bbbb", 1751813655000))
	assert.Equal(t, "CLOSED", ddEvents(t, db)["1002"].Status)
}

// Events opened before this change carry the monitor-wide fingerprint
// "<monitorID>-". A recovery now arrives under its group's fingerprint, and must
// still close them, or they would stay open for good — also when the group has
// re-notified since, so the recovery does match a new-style event as well.
func TestScenarioDatadogRecoveryClosesLegacyFingerprintEvent(t *testing.T) {
	db, sc := ddSetup(t)
	_, err := db.Exec(`insert into events(tenant, cloud_account_id, finding_id, fingerprint, source, status, starts_at)
		values ($1, $2, '900', $3, 'datadog_webhook', 'FIRING', '2025-07-06 11:54:15')`, caTenant, caAccount, ddMonitor+"-")
	require.NoError(t, err)
	ddDeliver(t, sc, ddDelivery("1003", "Re-Triggered", "aaaa", 1751806455000))

	ddDeliver(t, sc, ddDelivery("1004", "Recovered", "aaaa", 1751810055000))

	ev := ddEvents(t, db)
	for _, id := range []string{"900", "1003"} {
		assert.Equal(t, "CLOSED", ev[id].Status, id)
		assert.True(t, ev[id].Ended, id)
	}
}

// ddAggregDelivery is ddDelivery from a template that sends $AGGREG_KEY, as the
// documented one does, so the fingerprint is "<monitorID>-<aggreg_key>".
func ddAggregDelivery(id, transition, issue string, ms int64) string {
	return strings.Replace(ddDelivery(id, transition, issue, ms), `"org":`, `"event":{"aggreg_key":"agg-`+issue+`"},"org":`, 1)
}

// A re-notification of a group that is still firing is the same alert. Datadog
// gives every notification a new $ID, so each [Re-Triggered] used to open an
// event of its own beside the open one, with its own AI investigation and, at
// recovery, its own resolved notification. It now updates the open event, and a
// retried delivery still adds nothing.
func TestScenarioDatadogReNotificationUpdatesTheOpenEvent(t *testing.T) {
	db, sc := ddSetup(t)
	ddDeliver(t, sc, ddDelivery("1001", "Triggered", "aaaa", 1751802855000))
	ddDeliver(t, sc, ddDelivery("1003", "Re-Triggered", "aaaa", 1751806455000))
	ddDeliver(t, sc, ddDelivery("1003", "Re-Triggered", "aaaa", 1751806455000)) // Datadog retry
	ddDeliver(t, sc, ddDelivery("1005", "Re-Triggered", "aaaa", 1751810055000))

	ev := ddEvents(t, db)
	require.Len(t, ev, 1, "re-notifications of an open group must not open more events")
	assert.Equal(t, string(eventtypes.EventStatusFiring), ev["1001"].Status)
	assert.False(t, ev["1001"].Ended)
	assert.Equal(t, "1005", ev["1001"].LastDelivery, "the event carries the latest delivery's labels")
}

// A [Warn] that escalates to [Triggered] on the same group is that alert getting
// worse, not a second alert. The open event takes the latest delivery's priority.
func TestScenarioDatadogEscalationUpdatesTheOpenEvent(t *testing.T) {
	db, sc := ddSetup(t)
	ddDeliver(t, sc, strings.Replace(ddDelivery("1001", "Warn", "aaaa", 1751802855000), "[P2]", "[P3]", 1))
	require.Equal(t, string(eventtypes.EventPriorityLow), ddEvents(t, db)["1001"].Priority)

	ddDeliver(t, sc, strings.Replace(ddDelivery("1003", "Triggered", "aaaa", 1751803155000), "[P2]", "[P1]", 1))

	ev := ddEvents(t, db)
	require.Len(t, ev, 1, "an escalation must not open a second event for the group")
	assert.Equal(t, string(eventtypes.EventStatusFiring), ev["1001"].Status)
	assert.Equal(t, string(eventtypes.EventPriorityHigh), ev["1001"].Priority)
	assert.Equal(t, "1003", ev["1001"].LastDelivery)
}

// Once a group has recovered, its next trigger is a new alert: a new event, with
// the recovered one left closed. A late retry of the delivery that opened the
// recovered event neither reopens it nor adds one.
func TestScenarioDatadogTriggerAfterRecoveryOpensANewEvent(t *testing.T) {
	db, sc := ddSetup(t)
	ddDeliver(t, sc, ddDelivery("1001", "Triggered", "aaaa", 1751802855000))
	ddDeliver(t, sc, ddDelivery("1003", "Re-Triggered", "aaaa", 1751806455000))
	ddDeliver(t, sc, ddDelivery("1004", "Recovered", "aaaa", 1751810055000))
	ddDeliver(t, sc, ddDelivery("1001", "Triggered", "aaaa", 1751802855000)) // late Datadog retry

	ev := ddEvents(t, db)
	require.Len(t, ev, 1)
	assert.Equal(t, "CLOSED", ev["1001"].Status)

	ddDeliver(t, sc, ddDelivery("1006", "Triggered", "aaaa", 1751813655000))
	ddDeliver(t, sc, ddDelivery("1007", "Re-Triggered", "aaaa", 1751817255000))

	ev = ddEvents(t, db)
	require.Len(t, ev, 2, "a trigger after recovery opens one new event, which its re-notification updates")
	assert.Equal(t, "CLOSED", ev["1001"].Status, "the recovered event stays closed")
	assert.True(t, ev["1001"].Ended)
	assert.Equal(t, string(eventtypes.EventStatusFiring), ev["1006"].Status)
	assert.Equal(t, "1007", ev["1006"].LastDelivery)
	assert.Equal(t, ev["1001"].Fingerprint, ev["1006"].Fingerprint, "both are the same group's alert")
}

// The documented template sends $AGGREG_KEY. Its fingerprints follow the same
// rule: one open event per group, updated by re-notifications, closed by the
// group's recovery, and a new one for the next trigger.
func TestScenarioDatadogAggregKeyReNotificationUpdatesTheOpenEvent(t *testing.T) {
	db, sc := ddSetup(t)
	ddDeliver(t, sc, ddAggregDelivery("1001", "Triggered", "aaaa", 1751802855000))
	ddDeliver(t, sc, ddAggregDelivery("1002", "Triggered", "bbbb", 1751802860000))
	ddDeliver(t, sc, ddAggregDelivery("1003", "Re-Triggered", "aaaa", 1751806455000))

	ev := ddEvents(t, db)
	require.Len(t, ev, 2, "one event per group")
	assert.Equal(t, ddMonitor+"-agg-aaaa", ev["1001"].Fingerprint)
	assert.Equal(t, "1003", ev["1001"].LastDelivery)

	ddDeliver(t, sc, ddAggregDelivery("1004", "Recovered", "aaaa", 1751810055000))
	ddDeliver(t, sc, ddAggregDelivery("1006", "Triggered", "aaaa", 1751813655000))

	ev = ddEvents(t, db)
	require.Len(t, ev, 3)
	assert.Equal(t, "CLOSED", ev["1001"].Status)
	assert.Equal(t, string(eventtypes.EventStatusFiring), ev["1002"].Status)
	assert.Equal(t, string(eventtypes.EventStatusFiring), ev["1006"].Status)
}

// A one-shot "notification" delivery is not a monitor alert, and no Recovered
// follows it: each one is still its own event, stored closed on arrival.
func TestScenarioDatadogOneShotNotificationsStayClosedAndSeparate(t *testing.T) {
	db, sc := ddSetup(t)
	for i, id := range []string{"1001", "1003"} {
		payload := ddDelivery(id, "Triggered", "aaaa", 1751802855000+int64(i)*3600000)
		ddDeliver(t, sc, strings.Replace(payload, `"event_type":"error_tracking_alert"`, `"event_type":"notification"`, 1))
	}

	ev := ddEvents(t, db)
	require.Len(t, ev, 2)
	for _, id := range []string{"1001", "1003"} {
		assert.Equal(t, "CLOSED", ev[id].Status, id)
		assert.True(t, ev[id].Ended, id)
	}
}

// An event opened before this change is stored under the bare $ID. A
// re-notification of its group after the change still finds it as the group's
// open event, and updates it.
func TestScenarioDatadogReNotificationUpdatesAnEventStoredUnderTheBareId(t *testing.T) {
	db, sc := ddSetup(t)
	_, err := db.Exec(`insert into events(tenant, cloud_account_id, finding_id, fingerprint, source, status, starts_at)
		values ($1, $2, '1001', $3, 'datadog_webhook', 'FIRING', '2025-07-06 11:54:15')`, caTenant, caAccount, ddMonitor+"-issue.id:aaaa")
	require.NoError(t, err)

	ddDeliver(t, sc, ddDelivery("1003", "Re-Triggered", "aaaa", 1751806455000))

	ev := ddEvents(t, db)
	require.Len(t, ev, 1)
	assert.Equal(t, "1001", ev["1001"].FindingId)
	assert.Equal(t, "1003", ev["1001"].LastDelivery)
}
