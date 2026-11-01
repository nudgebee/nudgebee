package integrations_test

// ServiceNow deliveries that end an incident or an Event Management event, against
// a real Postgres, on the harness in event_webhook_scenario_test.go: the token
// lookup, the ServiceNow parser, routing and resolveEventQuery are all real.
// Skipped unless NB_TEST_PG_DSN is set (see that file).

import (
	"testing"

	eventtypes "nudgebee/services/event/types"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	snIntegration = "7a1d0c3e-0000-4000-8000-0000000000e1"
	snToken       = "servicenow-scenario-token"
	snPath        = "/api/webhooks/servicenow"
)

func snSetup(t *testing.T) (*sqlx.DB, *security.RequestContext) {
	t.Helper()
	db, sc := caSetup(t)
	// integrations.updated_at is read by the incident enrichment's credential lookup.
	_, err := db.Exec(`
insert into integrations values ('` + snIntegration + `', '` + caTenant + `', 'servicenow_webhook', 'servicenow', 'enabled');
insert into integrations_cloud_accounts values ('` + snIntegration + `', '` + caAccount + `');
insert into integration_config_values(integration_id, name, value) values ('` + snIntegration + `', 'token', '` + snToken + `');
alter table integrations add column updated_at timestamp;`)
	require.NoError(t, err)
	return db, sc
}

// snIncident is an incident delivery as a Business Rule sends it; state is the
// incident_state code.
func snIncident(number, state string) string {
	return `{"number":"` + number + `","sys_id":"46d44a5dc0a8010e0034d3fd6b2e1a2c","short_description":"Disk full on db01",
		"incident_state":"` + state + `","priority":"2","opened_at":"2026-09-29 10:00:00"}`
}

// snEmEvent is an Event Management (em_event) delivery.
func snEmEvent(messageKey, state string) string {
	return `{"source":"SCOM","event_class":"disk","message_key":"` + messageKey + `","node":"db01",
		"description":"Disk full on db01","severity":"2","state":"` + state + `"}`
}

// ServiceNow ends an incident as Resolved (6), Closed (7) or Canceled (8), and an
// em_event as Closing or Closed. Each is the alert ending, so each closes the event
// its firing delivery opened. Closed and Canceled are parsed to CLOSED, which
// routing did not know: the event stayed FIRING for good.
func TestScenarioServiceNowEndingDeliveryClosesTheEvent(t *testing.T) {
	for _, tc := range []struct{ name, firing, ending string }{
		{"incident resolved", snIncident("INC0010001", "1"), snIncident("INC0010001", "6")},
		{"incident closed with no resolved delivery", snIncident("INC0010002", "2"), snIncident("INC0010002", "7")},
		{"incident canceled", snIncident("INC0010003", "1"), snIncident("INC0010003", "8")},
		{"em_event closing", snEmEvent("scom:disk:db01", "Ready"), snEmEvent("scom:disk:db01", "Closing")},
		{"em_event closed", snEmEvent("scom:disk:db02", "Ready"), snEmEvent("scom:disk:db02", "Closed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, sc := snSetup(t)

			_, err := deliver(t, sc, snPath, snToken, tc.firing)
			require.NoError(t, err)
			ev := caEvents(t, db)
			require.Len(t, ev, 1)
			require.Equal(t, string(eventtypes.EventStatusFiring), ev[0].Status)
			require.False(t, ev[0].Ended)

			_, err = deliver(t, sc, snPath, snToken, tc.ending)
			require.NoError(t, err)
			ev = caEvents(t, db)
			require.Len(t, ev, 1, "an ending delivery closes an event, it does not add one")
			assert.Equal(t, "CLOSED", ev[0].Status)
			assert.True(t, ev[0].Ended, "ends_at must be stamped")
		})
	}
}

// A Closed delivery for an incident that never fired here has no event to close,
// and must not store one.
func TestScenarioServiceNowClosedDeliveryForUnknownIncidentAddsNoEvent(t *testing.T) {
	db, sc := snSetup(t)

	_, err := deliver(t, sc, snPath, snToken, snIncident("INC0010009", "7"))
	require.NoError(t, err)
	assert.Empty(t, caEvents(t, db))
}
