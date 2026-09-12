package event

import (
	"context"
	"os"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// CloseEventsForInactiveResources is GLOBAL -- it has no tenant or account
// scope, because the cron that drives it reconciles the whole estate. Pointing
// it at a shared database therefore closes that database's real alerts, so this
// test refuses to run against the configured metastore and takes an explicit
// disposable DSN instead:
//
//	docker run -d --name nb-sweep-test -e POSTGRES_PASSWORD=postgres \
//	  -e POSTGRES_DB=appdb -p 55441:5432 postgres:15
//	APP_DATABASE_URL="postgres://postgres:postgres@localhost:55441/appdb?sslmode=disable" \
//	  NB_SWEEP_DESTRUCTIVE_TEST=1 go test ./event/ -run TestCloseEventsForInactiveResources
//
// APP_DATABASE_URL has to be a real process variable: config is read once at
// package init, so t.Setenv would be too late to redirect the connection.
//
// The fixture below builds only the three tables the sweep touches.
const sweepTestSchema = `
CREATE TABLE IF NOT EXISTS cloud_resourses (id uuid PRIMARY KEY, is_active boolean, last_seen timestamp, type text);
CREATE TABLE IF NOT EXISTS events (
    id uuid PRIMARY KEY, created_at timestamp NOT NULL DEFAULT now(), updated_at timestamp NOT NULL DEFAULT now(),
    source text, finding_type text, status text, starts_at timestamp, ends_at timestamp,
    tenant uuid, cloud_account_id uuid, cloud_resource_id uuid, nb_status text DEFAULT 'OPEN');
CREATE TABLE IF NOT EXISTS event_history (
    id uuid PRIMARY KEY, changed_at timestamptz NOT NULL DEFAULT now(), changed_by uuid,
    change_type text, old_value jsonb, new_value jsonb, change_reason text, metadata jsonb,
    tenant_id uuid NOT NULL, cloud_account_id uuid NOT NULL,
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE);
TRUNCATE event_history, events, cloud_resourses;

INSERT INTO cloud_resourses VALUES
  ('a0000000-0000-0000-0000-0000000000d1', false, now() - interval '5 days', 'Job'),
  ('a0000000-0000-0000-0000-00000000a11e', true,  now(),                     'Deployment');

INSERT INTO events (id, created_at, source, finding_type, status, starts_at, ends_at, tenant, cloud_account_id, cloud_resource_id) VALUES
  ('e0000000-0000-0000-0000-000000000001', now()-interval '5 days','kubernetes_api_server','issue','FIRING', now()-interval '5 days', NULL,'11111111-1111-1111-1111-111111111111','22222222-2222-2222-2222-222222222222','a0000000-0000-0000-0000-0000000000d1'),
  ('e0000000-0000-0000-0000-000000000002', now()-interval '2 hours','kubernetes_api_server','issue','FIRING', now()-interval '2 hours', NULL,'11111111-1111-1111-1111-111111111111','22222222-2222-2222-2222-222222222222','a0000000-0000-0000-0000-0000000000d1'),
  ('e0000000-0000-0000-0000-000000000003', now()-interval '5 days','prometheus','issue','FIRING', now()-interval '5 days', NULL,'11111111-1111-1111-1111-111111111111','22222222-2222-2222-2222-222222222222','a0000000-0000-0000-0000-0000000000d1'),
  ('e0000000-0000-0000-0000-000000000004', now()-interval '5 days','kubernetes_api_server','configuration_change','FIRING', now()-interval '5 days', NULL,'11111111-1111-1111-1111-111111111111','22222222-2222-2222-2222-222222222222','a0000000-0000-0000-0000-0000000000d1'),
  ('e0000000-0000-0000-0000-000000000005', now()-interval '5 days','kubernetes_api_server','issue','FIRING', now()-interval '5 days', NULL,'11111111-1111-1111-1111-111111111111','22222222-2222-2222-2222-222222222222','a0000000-0000-0000-0000-00000000a11e'),
  ('e0000000-0000-0000-0000-000000000006', now()-interval '5 days','kubernetes_api_server','issue','CLOSED', now()-interval '5 days', now()-interval '4 days','11111111-1111-1111-1111-111111111111','22222222-2222-2222-2222-222222222222','a0000000-0000-0000-0000-0000000000d1'),
  ('e0000000-0000-0000-0000-000000000007', now()-interval '5 days','kubernetes_api_server','issue','FIRING', now()-interval '5 days', NULL, NULL,'22222222-2222-2222-2222-222222222222','a0000000-0000-0000-0000-0000000000d1');
`

func TestCloseEventsForInactiveResources(t *testing.T) {
	if os.Getenv("NB_SWEEP_DESTRUCTIVE_TEST") == "" {
		t.Skip("set NB_SWEEP_DESTRUCTIVE_TEST=1 and point APP_DATABASE_URL at a DISPOSABLE postgres; " +
			"this sweep is global and would close a real database's alerts")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatalf("connecting to the disposable database: %v", err)
	}

	// The fixture TRUNCATEs, and GetDatabaseManager caches its handle process-wide
	// -- so if anything in this package connected first, this could be pointing at
	// a real database. Refuse before destroying anything: a disposable fixture
	// database has either no events table or a handful of rows, never a real
	// estate's worth.
	// Two statements, not one CASE: Postgres resolves relation names during parse
	// analysis, so a single query mentioning `events` fails with `relation "events"
	// does not exist` on a fresh database no matter which CASE branch would run --
	// and a fresh database is exactly the documented way to run this.
	var eventsTableExists bool
	if err := dbms.Db.Get(&eventsTableExists, `SELECT to_regclass('public.events') IS NOT NULL`); err != nil {
		t.Fatalf("probing the target database: %v", err)
	}
	if eventsTableExists {
		var existingEvents int
		if err := dbms.Db.Get(&existingEvents, `SELECT count(*) FROM events`); err != nil {
			t.Fatalf("counting existing events: %v", err)
		}
		if existingEvents > 100 {
			t.Fatalf("refusing to run: %d rows in events, this is not a disposable database", existingEvents)
		}
	}

	if _, err := dbms.Db.Exec(sweepTestSchema); err != nil {
		t.Fatalf("loading fixture: %v", err)
	}

	ctx := security.NewRequestContext(context.Background(), nil, nil, nil, nil)
	closed, err := CloseEventsForInactiveResources(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if closed != 1 {
		t.Errorf("closed %d alerts, want exactly 1 (the aged agent issue on a dead resource)", closed)
	}

	// Only the leaked alert moves. Each of the others is excluded for its own
	// reason, and a regression in any one of them is a different bug.
	for _, tc := range []struct {
		id, want, why string
	}{
		{"e0000000-0000-0000-0000-000000000001", "CLOSED", "aged agent issue on a resource that no longer exists"},
		{"e0000000-0000-0000-0000-000000000002", "FIRING", "under the 24h floor: its successor may still be arriving"},
		{"e0000000-0000-0000-0000-000000000003", "FIRING", "prometheus delivers its own resolve and owns its lifecycle"},
		{"e0000000-0000-0000-0000-000000000004", "FIRING", "configuration_change describes a past moment, not a condition"},
		{"e0000000-0000-0000-0000-000000000005", "FIRING", "its resource is still alive"},
		{"e0000000-0000-0000-0000-000000000006", "CLOSED", "already closed, must not be touched again"},
		{"e0000000-0000-0000-0000-000000000007", "FIRING", "null tenant cannot get a history row"},
	} {
		var got string
		if err := dbms.Db.Get(&got, `SELECT status FROM events WHERE id = $1`, tc.id); err != nil {
			t.Fatalf("reading back %s: %v", tc.id, err)
		}
		if got != tc.want {
			t.Errorf("event ...%s: status = %q, want %q (%s)", tc.id[len(tc.id)-4:], got, tc.want, tc.why)
		}
	}

	// ends_at is when the RESOURCE was last seen, not when the sweep ran:
	// stamping now() would report every one of these as an incident that lasted
	// until the cron fired.
	var endsAtIsHistorical bool
	if err := dbms.Db.Get(&endsAtIsHistorical, `
		SELECT ends_at < (now() AT TIME ZONE 'utc') - interval '1 day'
		  FROM events WHERE id = 'e0000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatalf("reading back ends_at: %v", err)
	}
	if !endsAtIsHistorical {
		t.Error("ends_at was stamped with the sweep time; it must be the resource's last-seen time")
	}

	// The close and its audit row commit together.
	var reason string
	if err := dbms.Db.Get(&reason, `
		SELECT change_reason FROM event_history
		 WHERE event_id = 'e0000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatalf("reading back history: %v", err)
	}
	if reason != "resource_inactive_sweep" {
		t.Errorf("change_reason = %q, want resource_inactive_sweep", reason)
	}

	// Idempotent: a second run finds nothing and writes no duplicate history.
	again, err := CloseEventsForInactiveResources(ctx)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if again != 0 {
		t.Errorf("second run closed %d alerts, want 0", again)
	}
}
