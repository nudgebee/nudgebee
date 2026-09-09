package anomoly

import (
	"testing"
	"time"

	"nudgebee/services/event"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/testenv"
)

// insertFiringAnomalyEvent creates a FIRING event via the real InsertEvent
// path (not hand-rolled SQL) so the fixture matches exactly what
// GenerateAnomalyEvent would have produced, using the same fingerprint
// function the code under test uses. The row is deleted in t.Cleanup.
func insertFiringAnomalyEvent(t *testing.T, dbms *database.DatabaseManager, tenant, account string, anomalyType AnomalyType, name, namespace, category string) string {
	t.Helper()
	now := time.Now().UTC()
	fingerprint := anomalyFingerprint(account, anomalyType, name, namespace)
	id, err := event.InsertEvent(event.Event{
		AccountId:        account,
		Tenant:           tenant,
		Source:           "anomaly",
		Title:            "test fixture: " + fingerprint,
		Description:      "test fixture: " + fingerprint,
		FindingType:      "Anomaly",
		Category:         category,
		Priority:         "HIGH",
		SubjectName:      name,
		SubjectNamespace: namespace,
		SubjectType:      "deployment",
		FindingId:        fingerprint,
		AggregationKey:   "Anomaly",
		Status:           "FIRING",
		StartsAt:         &now,
		Fingerprint:      fingerprint,
	}, "")
	if err != nil {
		t.Fatalf("failed to insert fixture event for %s: %v", fingerprint, err)
	}
	t.Cleanup(func() {
		_, _ = dbms.Db.Exec(`DELETE FROM events WHERE id = $1`, id)
	})
	return id
}

// insertTestCloudAccount creates a throwaway cloud_accounts row so tests that
// need "some other real account" (events.cloud_account_id has a FK to
// cloud_accounts, and cloud_accounts.created_by/updated_by each have a FK to
// users) don't depend on a specific pre-seeded id that may not exist in every
// environment — user should be the third value from testenv.RequireTenant.
// Cleaned up in t.Cleanup.
func insertTestCloudAccount(t *testing.T, dbms *database.DatabaseManager, tenant, user string) string {
	t.Helper()
	var id string
	err := dbms.Db.Get(&id, `
		INSERT INTO cloud_accounts (account_name, tenant, account_type, created_by, updated_by)
		VALUES ('close_test fixture account', $1, 'K8s', $2, $2)
		RETURNING id`, tenant, user)
	if err != nil {
		t.Fatalf("failed to insert fixture cloud account: %v", err)
	}
	t.Cleanup(func() {
		_, _ = dbms.Db.Exec(`DELETE FROM cloud_accounts WHERE id = $1`, id)
	})
	return id
}

func anomalyEventStatus(t *testing.T, dbms *database.DatabaseManager, id string) string {
	t.Helper()
	var status string
	if err := dbms.Db.Get(&status, `SELECT status FROM events WHERE id = $1`, id); err != nil {
		t.Fatalf("failed to read back status for event %s: %v", id, err)
	}
	return status
}

// TestCloseAnomalyEventIfOpen_ClosesOnlyTheMatchingFingerprint is the core
// "are the correct ones being closed" test: several open events sharing the
// same account but differing in exactly one identity field must NOT be
// affected when one specific instance is closed.
func TestCloseAnomalyEventIfOpen_ClosesOnlyTheMatchingFingerprint(t *testing.T) {
	dbms := testenv.RequireMetastore(t)
	tenant, account, _ := testenv.RequireTenant(t)

	target := insertFiringAnomalyEvent(t, dbms, tenant, account, MetricAnomolyTypeCPU, "checkout", "prod", "Anomaly")
	sameAccountDifferentType := insertFiringAnomalyEvent(t, dbms, tenant, account, MetricAnomolyTypeMemory, "checkout", "prod", "Anomaly")
	sameAccountDifferentName := insertFiringAnomalyEvent(t, dbms, tenant, account, MetricAnomolyTypeCPU, "worker", "prod", "Anomaly")
	sameAccountDifferentNamespace := insertFiringAnomalyEvent(t, dbms, tenant, account, MetricAnomolyTypeCPU, "checkout", "staging", "Anomaly")

	closeAnomalyEventIfOpen(account, tenant, MetricAnomolyTypeCPU, "checkout", "prod")

	if got := anomalyEventStatus(t, dbms, target); got != "RESOLVED" {
		t.Errorf("target event: status = %q, want RESOLVED", got)
	}
	for label, id := range map[string]string{
		"different type":      sameAccountDifferentType,
		"different name":      sameAccountDifferentName,
		"different namespace": sameAccountDifferentNamespace,
	} {
		if got := anomalyEventStatus(t, dbms, id); got != "FIRING" {
			t.Errorf("%s event: status = %q, want FIRING (must not be closed by an unrelated fingerprint)", label, got)
		}
	}
}

// TestCloseAnomalyEventIfOpen_NoOpWhenNothingOpen exercises the sql.ErrNoRows
// path: closing an identity with no open event must not panic or error out
// visibly, and must not touch unrelated rows.
func TestCloseAnomalyEventIfOpen_NoOpWhenNothingOpen(t *testing.T) {
	dbms := testenv.RequireMetastore(t)
	tenant, account, _ := testenv.RequireTenant(t)

	unrelated := insertFiringAnomalyEvent(t, dbms, tenant, account, MetricAnomolyTypeCPU, "checkout", "prod", "Anomaly")

	closeAnomalyEventIfOpen(account, tenant, MetricAnomolyTypeLatency, "nonexistent", "nowhere")

	if got := anomalyEventStatus(t, dbms, unrelated); got != "FIRING" {
		t.Errorf("unrelated event: status = %q, want FIRING (a no-op close must not touch other rows)", got)
	}
}

// TestCloseAnomalyEventIfOpen_IdempotentOnAlreadyResolved calling close twice
// for the same identity must be safe — the second call finds status='FIRING'
// matches nothing (the row is already RESOLVED) and no-ops, rather than
// erroring or re-resolving with a new ends_at.
func TestCloseAnomalyEventIfOpen_IdempotentOnAlreadyResolved(t *testing.T) {
	dbms := testenv.RequireMetastore(t)
	tenant, account, _ := testenv.RequireTenant(t)

	id := insertFiringAnomalyEvent(t, dbms, tenant, account, MetricAnomolyTypeCPU, "checkout", "prod", "Anomaly")

	closeAnomalyEventIfOpen(account, tenant, MetricAnomolyTypeCPU, "checkout", "prod")
	if got := anomalyEventStatus(t, dbms, id); got != "RESOLVED" {
		t.Fatalf("after first close: status = %q, want RESOLVED", got)
	}

	closeAnomalyEventIfOpen(account, tenant, MetricAnomolyTypeCPU, "checkout", "prod")
	if got := anomalyEventStatus(t, dbms, id); got != "RESOLVED" {
		t.Errorf("after second close: status = %q, want RESOLVED (idempotent no-op)", got)
	}
}

type eventHistoryEntry struct {
	ChangeType   string `db:"change_type"`
	ChangeReason string `db:"change_reason"`
}

func eventHistoryEntries(t *testing.T, dbms *database.DatabaseManager, eventID string) []eventHistoryEntry {
	t.Helper()
	var rows []eventHistoryEntry
	if err := dbms.Db.Select(&rows, `SELECT change_type, change_reason FROM event_history WHERE event_id = $1 ORDER BY change_type`, eventID); err != nil {
		t.Fatalf("failed to read event_history for event %s: %v", eventID, err)
	}
	return rows
}

// TestCloseAnomalyEventIfOpen_RecordsEventHistory verifies closing an anomaly
// event is picked up by the pre-existing DB-level audit trigger
// (fn_event_history_trigger, migration V700, AFTER UPDATE ON events) — no
// Go-side audit call is needed here, the same way resolveEvent() (webhook
// resolve) and spend_anomaly.go's resolveAnomalyEvent don't make one either;
// the trigger fires on the raw UPDATE regardless of caller. This is a
// regression test for that wiring, not a test of the trigger's own logic.
func TestCloseAnomalyEventIfOpen_RecordsEventHistory(t *testing.T) {
	dbms := testenv.RequireMetastore(t)
	tenant, account, _ := testenv.RequireTenant(t)

	id := insertFiringAnomalyEvent(t, dbms, tenant, account, MetricAnomolyTypeCPU, "checkout", "prod", "Anomaly")
	closeAnomalyEventIfOpen(account, tenant, MetricAnomolyTypeCPU, "checkout", "prod")

	byType := make(map[string]string, 2) // change_type -> change_reason
	for _, e := range eventHistoryEntries(t, dbms, id) {
		byType[e.ChangeType] = e.ChangeReason
	}

	if reason, ok := byType["status"]; !ok {
		t.Errorf("no event_history row for change_type=status")
	} else if reason != "resolution_applied" {
		t.Errorf("status history entry: change_reason = %q, want resolution_applied", reason)
	}
	if _, ok := byType["ends_at"]; !ok {
		t.Errorf("no event_history row for change_type=ends_at")
	}
	// priority is untouched by the close, so it must not generate a spurious
	// history row (the trigger only logs columns that actually changed).
	if _, ok := byType["priority"]; ok {
		t.Errorf("unexpected event_history row for change_type=priority; close does not change priority")
	}
}

// TestCloseAnomalyEventsUpdatedBefore_RecordsEventHistory is the same check
// for the orphan-backstop sweep's raw batch UPDATE. The trigger fires
// per-row even inside a multi-row UPDATE, and — unlike a bulk close to
// CLOSED — a FIRING->RESOLVED transition is never subject to the trigger's
// "skip bulk closures" guard (that guard only suppresses logging when
// NEW.status = 'CLOSED' with priority unchanged), so this must be logged the
// same as a single explicit close.
func TestCloseAnomalyEventsUpdatedBefore_RecordsEventHistory(t *testing.T) {
	dbms := testenv.RequireMetastore(t)
	tenant, account, _ := testenv.RequireTenant(t)

	id := insertFiringAnomalyEvent(t, dbms, tenant, account, MetricAnomolyTypeCPU, "checkout", "prod", "Anomaly")
	closeAnomalyEventsUpdatedBefore([]string{account}, time.Now().UTC().Add(time.Hour))

	byType := make(map[string]string, 2)
	for _, e := range eventHistoryEntries(t, dbms, id) {
		byType[e.ChangeType] = e.ChangeReason
	}
	if reason, ok := byType["status"]; !ok {
		t.Errorf("no event_history row for change_type=status")
	} else if reason != "resolution_applied" {
		t.Errorf("status history entry: change_reason = %q, want resolution_applied", reason)
	}
}

// TestCloseAnomalyEventsUpdatedBefore_ScopesCorrectly is the "correct ones
// being closed" test for the orphan-cleanup backstop sweep. It must close a
// stale K8s metric anomaly event, but leave alone: a stale spend anomaly
// event (category guard — spend has its own OPEN/RESOLVED lifecycle), an
// event belonging to a different account (account scoping), and a
// non-stale event (the cutoff comparison itself).
//
// Rows can't be backdated via SQL (the updated_at trigger forces NOW() on
// every UPDATE), so instead the cutoff is placed in the FUTURE relative to
// insert time for the "stale" cases and in the PAST for the "fresh" case —
// this exercises the same "updated_at < staleBefore" comparison without
// needing real elapsed time.
func TestCloseAnomalyEventsUpdatedBefore_ScopesCorrectly(t *testing.T) {
	dbms := testenv.RequireMetastore(t)
	tenant, account, user := testenv.RequireTenant(t)

	// events.cloud_account_id has a real FK to cloud_accounts (events_cloud_account_id_fkey)
	// — a fabricated UUID 23503s here. Use a throwaway real row instead of depending
	// on some other pre-seeded account id existing in every environment.
	otherAccount := insertTestCloudAccount(t, dbms, tenant, user)

	staleK8sAnomaly := insertFiringAnomalyEvent(t, dbms, tenant, account, MetricAnomolyTypeCPU, "checkout", "prod", "Anomaly")
	staleSpendAnomaly := insertFiringAnomalyEvent(t, dbms, tenant, account, MetricAnomolyTypeCloudSpendService, "checkout", "prod", "CostAnomaly")
	staleOtherAccountAnomaly := insertFiringAnomalyEvent(t, dbms, tenant, otherAccount, MetricAnomolyTypeCPU, "checkout", "prod", "Anomaly")

	future := time.Now().UTC().Add(time.Hour)

	// Sweep with a future cutoff over just `account`: the K8s anomaly is
	// "stale" relative to it and must close; the CostAnomaly-category row
	// must not, regardless of staleness; a same-fingerprint row under a
	// DIFFERENT account (not in the accountIds list passed to the sweep)
	// must not either.
	closeAnomalyEventsUpdatedBefore([]string{account}, future)

	if got := anomalyEventStatus(t, dbms, staleK8sAnomaly); got != "RESOLVED" {
		t.Errorf("stale K8s anomaly: status = %q, want RESOLVED", got)
	}
	if got := anomalyEventStatus(t, dbms, staleSpendAnomaly); got != "FIRING" {
		t.Errorf("spend (CostAnomaly) anomaly: status = %q, want FIRING (category guard must exclude it)", got)
	}
	if got := anomalyEventStatus(t, dbms, staleOtherAccountAnomaly); got != "FIRING" {
		t.Errorf("other-account anomaly: status = %q, want FIRING (account scoping must exclude it)", got)
	}

	// Insert the "fresh" row only now, AFTER the future-cutoff sweep already
	// ran — inserting it earlier would let that first sweep catch it too
	// (its updated_at would also be "before" a future cutoff), making this
	// second assertion meaningless.
	freshK8sAnomaly := insertFiringAnomalyEvent(t, dbms, tenant, account, MetricAnomolyTypeMemory, "checkout", "prod", "Anomaly")
	past := time.Now().UTC().Add(-time.Hour)

	// Sweep the same account again with a past cutoff: the just-inserted K8s
	// anomaly was updated after `past`, so it's not stale relative to it and
	// must be left FIRING.
	closeAnomalyEventsUpdatedBefore([]string{account}, past)
	if got := anomalyEventStatus(t, dbms, freshK8sAnomaly); got != "FIRING" {
		t.Errorf("fresh K8s anomaly: status = %q, want FIRING (not stale relative to a past cutoff)", got)
	}
}
