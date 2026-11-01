package integrations_test

// End-to-end lifecycle of the event webhook against a real Postgres:
// edge validation → store → ProcessStoredWebhook → investigate / resolve →
// replay. Everything in the webhook pipeline is real (token lookup, the
// settings query with the synthetic integration id, resolveEventQuery, the
// replay guard). The one stand-in is event.InvestigateEvent, replaced by an
// insert with the same `ON CONFLICT (tenant, cloud_account_id, finding_id)`
// identity its existing-finding_id branch relies on: it never creates a second
// row for a finding_id and never reopens a CLOSED one.
//
// Skipped unless NB_TEST_PG_DSN points at an empty database, e.g.
//
//	NB_TEST_PG_DSN='postgres://postgres@/nbtest?host=/tmp&port=5439&sslmode=disable' \
//	  go test ./integrations/ -run TestScenarioEventWebhook -v

import (
	"os"
	"testing"

	"nudgebee/services/config"
	eventtypes "nudgebee/services/event/types"
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	caTenant        = "7a1d0c3e-0000-4000-8000-000000000001"
	caAccount       = "7a1d0c3e-0000-4000-8000-0000000000a1"
	caIntegration   = "7a1d0c3e-0000-4000-8000-0000000000c1"
	caUnlinked      = "7a1d0c3e-0000-4000-8000-0000000000c2"
	caToken         = "event-scenario-token"
	caUnlinkedToken = "event-unlinked-token"
)

const caSchema = `
drop table if exists events, event_incoming_webhooks, integration_config_values,
  integrations_cloud_accounts, integrations, cloud_accounts cascade;
create table cloud_accounts (id uuid primary key, tenant uuid);
insert into cloud_accounts values ('` + caAccount + `', '` + caTenant + `');
create table integrations (id uuid primary key, tenant_id uuid not null, type text not null, name text, status text);
create table integrations_cloud_accounts (integration_id uuid, cloud_account_id uuid);
create table integration_config_values (integration_id uuid, name text, value text, is_encrypted boolean default false);
create table event_incoming_webhooks (
  id uuid primary key default gen_random_uuid(), tenant_id uuid not null, account_id uuid,
  integration_id uuid not null, integration_type text not null,
  webhook_id text not null, event_type text not null, event_id text not null,
  event_url text, event_status text, event_priority text,
  event_created_at timestamp not null, event_title text not null, event_description text, raw text,
  processing_status text not null default 'processed', request_url text, processing_error text);
create table events (
  id uuid primary key default gen_random_uuid(), tenant uuid not null, cloud_account_id uuid not null,
  finding_id text not null, fingerprint text, source text, status text not null,
  subject_name text, starts_at timestamp, ends_at timestamp);
create unique index events_cloudaccount_findingid on events (tenant, cloud_account_id, finding_id);
insert into integrations values
  ('` + caIntegration + `', '` + caTenant + `', 'event_webhook', 'site24x7', 'enabled'),
  ('` + caUnlinked + `', '` + caTenant + `', 'event_webhook', 'unlinked', 'enabled');
insert into integrations_cloud_accounts values ('` + caIntegration + `', '` + caAccount + `');
insert into integration_config_values(integration_id, name, value) values
  ('` + caIntegration + `', 'token', '` + caToken + `'),
  ('` + caUnlinked + `', 'token', '` + caUnlinkedToken + `');
`

// caInvestigations counts calls to the InvestigateEvent stand-in. The real
// function's existing-finding_id branch overwrites priority/labels/subject, so
// a resolve must never reach it on replay — counting calls catches that even
// though the stand-in itself leaves the row alone.
var caInvestigations int

type caEvent struct {
	FindingId   string `db:"finding_id"`
	Fingerprint string `db:"fingerprint"`
	Source      string `db:"source"`
	Status      string `db:"status"`
	SubjectName string `db:"subject_name"`
	Ended       bool   `db:"ended"`
}

func caSetup(t *testing.T) (*sqlx.DB, *security.RequestContext) {
	t.Helper()
	dsn := os.Getenv("NB_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("NB_TEST_PG_DSN not set; skipping live scenario")
	}
	db, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(caSchema)
	require.NoError(t, err)

	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: db}, nil
	})
	manager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err)
	manager.Db = db

	prevAsync, prevFn := config.Config.WebhookAsyncExecution, core.InvestigateEventFn
	config.Config.WebhookAsyncExecution = false
	core.InvestigateEventFn = func(_ *security.RequestContext, e eventtypes.Event, _ string) (string, error) {
		caInvestigations++
		_, err := db.Exec(`insert into events(tenant, cloud_account_id, finding_id, fingerprint, source, status, subject_name, starts_at, ends_at)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			on conflict (tenant, cloud_account_id, finding_id) do nothing`,
			e.Tenant, e.AccountId, e.FindingId, e.Fingerprint, e.Source, string(e.Status), e.SubjectName, e.StartsAt, e.EndsAt)
		return "", err
	}
	t.Cleanup(func() {
		config.Config.WebhookAsyncExecution = prevAsync
		core.InvestigateEventFn = prevFn
	})

	return db, security.NewRequestContextForTenantAdmin(caTenant, nil, nil, nil)
}

// deliver is what genericWebhookHandler does: validate+store, then process.
func deliver(t *testing.T, sc *security.RequestContext, path, token, payload string) (string, error) {
	t.Helper()
	rowID, err := core.ValidateAndStoreWebhook(sc, path+"?token="+token, nil, payload)
	if err != nil {
		return "", err
	}
	require.NoError(t, core.ProcessStoredWebhook(sc, rowID))
	return rowID, nil
}

func caEvents(t *testing.T, db *sqlx.DB) []caEvent {
	t.Helper()
	var rows []caEvent
	require.NoError(t, db.Select(&rows, `select finding_id, fingerprint, source, status, coalesce(subject_name, '') as subject_name,
		ends_at is not null as ended from events order by starts_at, finding_id`))
	return rows
}

func caFiring(id, fingerprint, startsAt string) string {
	return `{"id":"` + id + `","fingerprint":"` + fingerprint + `","status":"firing","title":"CPU above 85% on dc01",
		"rule_name":"cpu_high","severity":"high","starts_at":"` + startsAt + `",
		"subject":{"name":"dc01.example.com","type":"host"},"source_tool":"Site24x7"}`
}

func caResolved(id, fingerprint string) string {
	return `{"id":"` + id + `","fingerprint":"` + fingerprint + `","status":"resolved","title":"CPU above 85% on dc01","rule_name":"cpu_high"}`
}

func TestScenarioEventWebhookLifecycle(t *testing.T) {
	db, sc := caSetup(t)
	const path = "/api/webhooks/event"
	id := func(v string) string { return "ew:" + caIntegration + ":id:" + v }
	fp := func(v string) string { return "ew:" + caIntegration + ":fp:" + v }

	// 1. Firing → one event, source event_webhook, subject verbatim.
	_, err := deliver(t, sc, path, caToken, caFiring("evt-1", "cpu:dc01", "2026-09-29T10:00:00Z"))
	require.NoError(t, err)
	ev := caEvents(t, db)
	require.Len(t, ev, 1)
	assert.Equal(t, caEvent{FindingId: id("evt-1"), Fingerprint: fp("cpu:dc01"), Source: "event_webhook",
		Status: string(eventtypes.EventStatusFiring), SubjectName: "dc01.example.com"}, ev[0])

	// 2. Same id again → no new event.
	_, err = deliver(t, sc, path, caToken, caFiring("evt-1", "cpu:dc01", "2026-09-29T10:00:00Z"))
	require.NoError(t, err)
	require.Len(t, caEvents(t, db), 1)

	// 3. Resolve with id → CLOSED with ends_at.
	resolveRow, err := deliver(t, sc, path, caToken, caResolved("evt-1", "cpu:dc01"))
	require.NoError(t, err)
	ev = caEvents(t, db)
	require.Len(t, ev, 1)
	assert.Equal(t, "CLOSED", ev[0].Status)
	assert.True(t, ev[0].Ended, "ends_at must be stamped")

	// 4. Re-fire with a new id → new open event, same fingerprint.
	firingRow, err := deliver(t, sc, path, caToken, caFiring("evt-2", "cpu:dc01", "2026-09-29T12:00:00Z"))
	require.NoError(t, err)
	ev = caEvents(t, db)
	require.Len(t, ev, 2)
	assert.Equal(t, id("evt-2"), ev[1].FindingId)
	assert.Equal(t, ev[0].Fingerprint, ev[1].Fingerprint, "linked to the first in history")
	assert.Equal(t, string(eventtypes.EventStatusFiring), ev[1].Status)

	// 5. A late repeat of the first resolve must not touch the new firing.
	_, err = deliver(t, sc, path, caToken, caResolved("evt-1", "cpu:dc01"))
	require.NoError(t, err)
	assert.Equal(t, string(eventtypes.EventStatusFiring), caEvents(t, db)[1].Status)

	// 6. Replaying the stored resolve is a no-op; replaying a firing adds nothing.
	before := caInvestigations
	require.NoError(t, core.ReplayWebhookEvent(sc, resolveRow))
	assert.Equal(t, before, caInvestigations, "replaying a resolve must not re-investigate it as a firing")
	require.NoError(t, core.ReplayWebhookEvent(sc, firingRow))
	assert.Equal(t, before+1, caInvestigations, "replaying a firing re-investigates it")
	ev = caEvents(t, db)
	require.Len(t, ev, 2)
	assert.Equal(t, "CLOSED", ev[0].Status)
	assert.Equal(t, string(eventtypes.EventStatusFiring), ev[1].Status)

	// 7. Resolve the second firing → closes it.
	_, err = deliver(t, sc, path, caToken, caResolved("evt-2", "cpu:dc01"))
	require.NoError(t, err)
	assert.Equal(t, "CLOSED", caEvents(t, db)[1].Status)
}

// A sender using the same string as id and fingerprint must not have a resolve
// for one firing close the whole alert chain (resolveEventQuery matches
// fingerprint OR finding_id).
func TestScenarioEventWebhookIdEqualsFingerprint(t *testing.T) {
	db, sc := caSetup(t)
	const path = "/api/webhooks/event"

	for _, p := range []string{
		caFiring("disk", "disk", "2026-09-29T10:00:00Z"),
		caFiring("disk-2", "disk", "2026-09-29T11:00:00Z"),
		caResolved("disk", "disk"),
	} {
		_, err := deliver(t, sc, path, caToken, p)
		require.NoError(t, err)
	}
	ev := caEvents(t, db)
	require.Len(t, ev, 2)
	assert.Equal(t, "CLOSED", ev[0].Status)
	assert.Equal(t, string(eventtypes.EventStatusFiring), ev[1].Status, "the other firing must stay open")
}

func TestScenarioEventWebhookEdgeValidation(t *testing.T) {
	db, sc := caSetup(t)

	// A batch with one bad item is rejected whole, and the delivery is kept as
	// failed for support. Validation is keyed on the token's integration type,
	// so posting to another webhook path doesn't bypass it.
	bad := `{"events":[` + caFiring("ok", "f", "2026-09-29T10:00:00Z") + `,{"id":"x","status":"firing","title":"t","rule_name":"r"}]}`
	for _, path := range []string{"/api/webhooks/event", "/api/webhooks/grafana"} {
		_, err := core.ValidateAndStoreWebhook(sc, path+"?token="+caToken, nil, bad)
		var verr *core.WebhookValidationError
		require.ErrorAs(t, err, &verr, path)
		assert.Equal(t, []core.WebhookValidationItemError{{Index: 1, Field: "fingerprint", Reason: "is required"}}, verr.Items)
	}
	var failed []struct {
		Status string `db:"processing_status"`
		Error  string `db:"processing_error"`
	}
	require.NoError(t, db.Select(&failed, `select processing_status, coalesce(processing_error, '') as processing_error from event_incoming_webhooks`))
	require.Len(t, failed, 2)
	assert.Equal(t, "failed", failed[0].Status)
	assert.Contains(t, failed[0].Error, "fingerprint")
	assert.Empty(t, caEvents(t, db), "nothing from a rejected batch is processed")

	// An integration with no linked account is rejected instead of dropping
	// every event after a 200.
	_, err := core.ValidateAndStoreWebhook(sc, "/api/webhooks/event?token="+caUnlinkedToken, nil, caFiring("a", "f", "2026-09-29T10:00:00Z"))
	var verr *core.WebhookValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "integration", verr.Items[0].Field)
}
