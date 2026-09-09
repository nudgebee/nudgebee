package slo

import (
	"testing"
	"time"

	"nudgebee/services/event"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/testenv"
)

// insertFiringSLOEvent creates a FIRING SLOViolation event via the real
// InsertEvent path (not hand-rolled SQL) so the fixture matches exactly what
// GenerateSLOEvent would have produced, using the same fingerprint function
// the code under test uses. The row is deleted in t.Cleanup.
func insertFiringSLOEvent(t *testing.T, dbms *database.DatabaseManager, tenant, account, configName, workload, namespace string) string {
	t.Helper()
	now := time.Now().UTC()
	fingerprint := sloFingerprint(account, configName, workload, namespace)
	id, err := event.InsertEvent(event.Event{
		AccountId:        account,
		Tenant:           tenant,
		Source:           "slo",
		Title:            "test fixture: " + fingerprint,
		Description:      "test fixture: " + fingerprint,
		FindingType:      "SLO",
		Category:         "SLO",
		Priority:         "HIGH",
		SubjectName:      workload,
		SubjectNamespace: namespace,
		SubjectType:      "deployment",
		FindingId:        fingerprint,
		AggregationKey:   "SLOViolation",
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

func sloEventStatus(t *testing.T, dbms *database.DatabaseManager, id string) string {
	t.Helper()
	var status string
	if err := dbms.Db.Get(&status, `SELECT status FROM events WHERE id = $1`, id); err != nil {
		t.Fatalf("failed to read back status for event %s: %v", id, err)
	}
	return status
}

func okReport(workload, namespace string) SLOReport {
	return SLOReport{Workload: workload, Namespace: namespace, Valid: true}
}

// TestCloseSLOEventIfRecovered_ClosesOnlyTheMatchingFingerprint is the core
// "are the correct ones being closed" test: several open SLO violation
// events sharing the same account but differing in exactly one identity
// field must NOT be affected when one specific config's violation recovers.
func TestCloseSLOEventIfRecovered_ClosesOnlyTheMatchingFingerprint(t *testing.T) {
	dbms := testenv.RequireMetastore(t)
	tenant, account, _ := testenv.RequireTenant(t)

	target := insertFiringSLOEvent(t, dbms, tenant, account, "latency", "checkout", "prod")
	sameAccountDifferentConfig := insertFiringSLOEvent(t, dbms, tenant, account, "availability", "checkout", "prod")
	sameAccountDifferentWorkload := insertFiringSLOEvent(t, dbms, tenant, account, "latency", "worker", "prod")
	sameAccountDifferentNamespace := insertFiringSLOEvent(t, dbms, tenant, account, "latency", "checkout", "staging")

	targetConfig := DBSLOConfig{CloudAccountId: account, TenantId: tenant, Name: "latency", WorkloadName: "checkout", Namespace: "prod"}
	closeSLOEventIfRecovered(dbms, targetConfig, []SLOReport{okReport("checkout", "prod")})

	if got := sloEventStatus(t, dbms, target); got != "RESOLVED" {
		t.Errorf("target event: status = %q, want RESOLVED", got)
	}
	for label, id := range map[string]string{
		"different config name": sameAccountDifferentConfig,
		"different workload":    sameAccountDifferentWorkload,
		"different namespace":   sameAccountDifferentNamespace,
	} {
		if got := sloEventStatus(t, dbms, id); got != "FIRING" {
			t.Errorf("%s event: status = %q, want FIRING (must not be closed by an unrelated fingerprint)", label, got)
		}
	}
}

// TestCloseSLOEventIfRecovered_NoOpWithoutExplicitOK verifies the "silence
// isn't recovery" guard using the real function: an empty report set (agent
// not connected / relay failure — see executeSlo) and a NO_DATA-only report
// set must both leave the event open, since neither is a genuine recovery
// signal.
func TestCloseSLOEventIfRecovered_NoOpWithoutExplicitOK(t *testing.T) {
	dbms := testenv.RequireMetastore(t)
	tenant, account, _ := testenv.RequireTenant(t)
	config := DBSLOConfig{CloudAccountId: account, TenantId: tenant, Name: "latency", WorkloadName: "checkout", Namespace: "prod"}

	t.Run("empty report set", func(t *testing.T) {
		id := insertFiringSLOEvent(t, dbms, tenant, account, "latency", "checkout", "prod")
		closeSLOEventIfRecovered(dbms, config, nil)
		if got := sloEventStatus(t, dbms, id); got != "FIRING" {
			t.Errorf("status = %q, want FIRING (empty report set carries no recovery information)", got)
		}
	})

	t.Run("NO_DATA-only report set", func(t *testing.T) {
		id := insertFiringSLOEvent(t, dbms, tenant, account, "latency", "checkout", "prod")
		closeSLOEventIfRecovered(dbms, config, []SLOReport{{Workload: "checkout", Namespace: "prod", Valid: false}})
		if got := sloEventStatus(t, dbms, id); got != "FIRING" {
			t.Errorf("status = %q, want FIRING (NO_DATA is not a recovery signal)", got)
		}
	})
}

// TestCloseSLOEventIfRecovered_ClosesOnExplicitOK is the positive
// counterpart: a real SLOStatusOK report must close the event.
func TestCloseSLOEventIfRecovered_ClosesOnExplicitOK(t *testing.T) {
	dbms := testenv.RequireMetastore(t)
	tenant, account, _ := testenv.RequireTenant(t)
	config := DBSLOConfig{CloudAccountId: account, TenantId: tenant, Name: "latency", WorkloadName: "checkout", Namespace: "prod"}

	id := insertFiringSLOEvent(t, dbms, tenant, account, "latency", "checkout", "prod")
	closeSLOEventIfRecovered(dbms, config, []SLOReport{okReport("checkout", "prod")})
	if got := sloEventStatus(t, dbms, id); got != "RESOLVED" {
		t.Errorf("status = %q, want RESOLVED", got)
	}
}

// TestCloseSLOEventByFingerprint_NoOpWhenNothingOpen exercises the
// sql.ErrNoRows path directly: closing a fingerprint with no open event must
// not error out visibly and must not touch unrelated rows.
func TestCloseSLOEventByFingerprint_NoOpWhenNothingOpen(t *testing.T) {
	dbms := testenv.RequireMetastore(t)
	tenant, account, _ := testenv.RequireTenant(t)

	unrelated := insertFiringSLOEvent(t, dbms, tenant, account, "latency", "checkout", "prod")

	closeSLOEventByFingerprint(dbms, tenant, account, sloFingerprint(account, "availability", "nonexistent", "nowhere"))

	if got := sloEventStatus(t, dbms, unrelated); got != "FIRING" {
		t.Errorf("unrelated event: status = %q, want FIRING (a no-op close must not touch other rows)", got)
	}
}

// TestCloseSLOEventByFingerprint_IdempotentOnAlreadyResolved: closing twice
// must be safe — the second call's status='FIRING' predicate matches nothing.
func TestCloseSLOEventByFingerprint_IdempotentOnAlreadyResolved(t *testing.T) {
	dbms := testenv.RequireMetastore(t)
	tenant, account, _ := testenv.RequireTenant(t)
	fingerprint := sloFingerprint(account, "latency", "checkout", "prod")

	id := insertFiringSLOEvent(t, dbms, tenant, account, "latency", "checkout", "prod")

	closeSLOEventByFingerprint(dbms, tenant, account, fingerprint)
	if got := sloEventStatus(t, dbms, id); got != "RESOLVED" {
		t.Fatalf("after first close: status = %q, want RESOLVED", got)
	}

	closeSLOEventByFingerprint(dbms, tenant, account, fingerprint)
	if got := sloEventStatus(t, dbms, id); got != "RESOLVED" {
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

// TestCloseSLOEventByFingerprint_RecordsEventHistory verifies closing an SLO
// violation event is picked up by the pre-existing DB-level audit trigger
// (fn_event_history_trigger, migration V700, AFTER UPDATE ON events) — no
// Go-side audit call is needed here, the same way resolveEvent() (webhook
// resolve) doesn't make one either; the trigger fires on the raw UPDATE
// regardless of caller. This is a regression test for that wiring, not a
// test of the trigger's own logic.
func TestCloseSLOEventByFingerprint_RecordsEventHistory(t *testing.T) {
	dbms := testenv.RequireMetastore(t)
	tenant, account, _ := testenv.RequireTenant(t)
	fingerprint := sloFingerprint(account, "latency", "checkout", "prod")

	id := insertFiringSLOEvent(t, dbms, tenant, account, "latency", "checkout", "prod")
	closeSLOEventByFingerprint(dbms, tenant, account, fingerprint)

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
