package event

import (
	"context"
	"database/sql"
	"log/slog"
	"regexp"
	"testing"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	eventtypes "nudgebee/services/event/types"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #39585: an automation raises an alert through Store Event (trigger_investigation
// → InvestigateEvent) and later reports it resolved. Before the fix the existing-
// finding_id branch updated labels and returned, so the RESOLVED never closed anything.
// These tests drive InvestigateEvent against a sqlmock metastore; sqlmock fails on any
// statement not expected, which is how "untouched" and "nothing inserted" are asserted.

const (
	resolveTestTenant  = "5c0a2b6e-0d7b-4e57-9d0a-3c4f7a1f9b10"
	resolveTestAccount = testActiveAccountID
	resolveTestEventID = "8422a701-a0a2-443b-8fc0-692a4360de4a"
)

var (
	findingLookupSQL  = regexp.QuoteMeta("SELECT id, subject_name, source FROM events WHERE tenant = $1 AND cloud_account_id = $2 AND finding_id = $3")
	labelUpdateSQL    = regexp.QuoteMeta(repeatDeliveryUpdateSQL)
	closeByIdSQL      = regexp.QuoteMeta(eventtypes.CloseEventByIdQuery)
	closeByFingerprin = regexp.QuoteMeta(eventtypes.CloseEventsByFingerprintQuery)
)

// setupResolveTest points the Metastore at a fresh sqlmock, expects the disabled-
// account check every InvestigateEvent makes, and records resolved notifications.
func setupResolveTest(t *testing.T) (sqlmock.Sqlmock, *[]string) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	manager := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return manager, nil
	})

	clearAccountStatusCache(t)
	mock.ExpectQuery("SELECT COALESCE\\(status").
		WithArgs(resolveTestAccount).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("active"))

	var notified []string
	orig := publishResolved
	publishResolved = func(_ context.Context, ids []string) { notified = append(notified, ids...) }
	t.Cleanup(func() { publishResolved = orig })
	return mock, &notified
}

// automationEvent is what Store Event sends. SubjectOwner is set and the namespace is
// empty so InvestigateEvent skips its pod-owner and cloud-resource lookups.
func automationEvent(status EventStatus, findingId, fingerprint string) Event {
	return Event{
		Tenant:         resolveTestTenant,
		AccountId:      resolveTestAccount,
		FindingId:      findingId,
		Fingerprint:    fingerprint,
		Source:         "automation",
		Status:         status,
		Title:          "calico-node Unavailable Replicas exceeds 0",
		AggregationKey: "site24x7",
		FindingType:    "issue",
		Priority:       EventPriorityHigh,
		SubjectName:    "calico-node",
		SubjectOwner:   "calico-node",
		Cluster:        "solar winds POC",
		Labels:         map[string]string{"email_kind": "recovery"},
	}
}

func resolveTestContext() *security.RequestContext {
	return security.NewRequestContext(context.Background(),
		security.NewSecurityContextForAuditRelay(resolveTestTenant, ""), slog.Default(), nil, nil)
}

func existingRow(source string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "subject_name", "source"}).AddRow(resolveTestEventID, "calico-node", source)
}

// AC1: FIRING then RESOLVED with the same finding_id → CLOSED with the reported end
// time, exactly one resolved notification.
func TestInvestigateEvent_ResolveClosesExistingFiring(t *testing.T) {
	mock, notified := setupResolveTest(t)
	endsAt := time.Date(2026, 9, 29, 10, 28, 40, 0, time.UTC)
	e := automationEvent(EventStatusResolved, "site24x7-calico-1", "calico-node:unavailable")
	e.EndsAt = &endsAt

	mock.ExpectQuery(findingLookupSQL).WithArgs(resolveTestTenant, resolveTestAccount, e.FindingId).WillReturnRows(existingRow("automation"))
	mock.ExpectExec(labelUpdateSQL).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(closeByIdSQL).WithArgs(resolveTestEventID, "CLOSED", endsAt, "automation").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(resolveTestEventID))

	id, err := InvestigateEvent(resolveTestContext(), e, "")
	require.NoError(t, err)
	assert.Equal(t, resolveTestEventID, id)
	assert.Equal(t, []string{resolveTestEventID}, *notified)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// AC2: a second RESOLVED finds the row already CLOSED; the guarded UPDATE returns no
// row, so nothing is re-notified.
func TestInvestigateEvent_RepeatResolveDoesNotRenotify(t *testing.T) {
	mock, notified := setupResolveTest(t)
	e := automationEvent(EventStatusResolved, "site24x7-calico-1", "")

	mock.ExpectQuery(findingLookupSQL).WillReturnRows(existingRow("automation"))
	mock.ExpectExec(labelUpdateSQL).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(closeByIdSQL).WithArgs(resolveTestEventID, "CLOSED", sqlmock.AnyArg(), "automation").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	id, err := InvestigateEvent(resolveTestContext(), e, "")
	require.NoError(t, err)
	assert.Equal(t, resolveTestEventID, id)
	assert.Empty(t, *notified)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// AC3: FIRING on an existing (possibly CLOSED) finding_id keeps today's behaviour —
// labels update, no status change, no notification. A new firing needs a new id.
func TestInvestigateEvent_FiringDoesNotReopen(t *testing.T) {
	mock, notified := setupResolveTest(t)
	e := automationEvent(EventStatusFiring, "site24x7-calico-1", "calico-node:unavailable")

	mock.ExpectQuery(findingLookupSQL).WillReturnRows(existingRow("automation"))
	mock.ExpectExec(labelUpdateSQL).WillReturnResult(sqlmock.NewResult(0, 1))

	id, err := InvestigateEvent(resolveTestContext(), e, "")
	require.NoError(t, err)
	assert.Equal(t, resolveTestEventID, id)
	assert.Empty(t, *notified)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// AC4: a RESOLVED whose finding_id belongs to another source's event leaves that
// event untouched — no label update, no close, and no insert (whose upsert on the
// same finding_id would overwrite it).
func TestInvestigateEvent_ResolveLeavesOtherSourceUntouched(t *testing.T) {
	mock, notified := setupResolveTest(t)
	e := automationEvent(EventStatusResolved, "shared-id", "")

	mock.ExpectQuery(findingLookupSQL).WillReturnRows(existingRow("alertmanager"))

	id, err := InvestigateEvent(resolveTestContext(), e, "")
	require.NoError(t, err)
	assert.Empty(t, id)
	assert.Empty(t, *notified)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// With a fingerprint, a RESOLVED whose finding_id is another source's closes its own
// source's open events of that fingerprint instead.
func TestInvestigateEvent_ResolveOnOtherSourceIdFallsBackToFingerprint(t *testing.T) {
	mock, notified := setupResolveTest(t)
	e := automationEvent(EventStatusResolved, "shared-id", "calico-node:unavailable")

	mock.ExpectQuery(findingLookupSQL).WillReturnRows(existingRow("alertmanager"))
	mock.ExpectQuery(closeByFingerprin).
		WithArgs(resolveTestTenant, resolveTestAccount, e.Fingerprint, "CLOSED", sqlmock.AnyArg(), "automation").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	id, err := InvestigateEvent(resolveTestContext(), e, "")
	require.NoError(t, err)
	assert.Empty(t, id)
	assert.Empty(t, *notified)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// AC8: two open automation events of fingerprint F and a RESOLVED with no finding_id
// → both closed, one notification each. The same-source predicate in the statement
// (asserted by the "automation" argument) is what leaves an Alertmanager event with
// the same fingerprint untouched. No finding_id lookup runs at all.
func TestInvestigateEvent_ResolveWithoutFindingIdClosesByFingerprint(t *testing.T) {
	mock, notified := setupResolveTest(t)
	e := automationEvent(EventStatusResolved, "", "calico-node:unavailable")

	mock.ExpectQuery(closeByFingerprin).
		WithArgs(resolveTestTenant, resolveTestAccount, e.Fingerprint, "CLOSED", sqlmock.AnyArg(), "automation").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("event-a").AddRow("event-b"))

	id, err := InvestigateEvent(resolveTestContext(), e, "")
	require.NoError(t, err)
	assert.Equal(t, "event-a", id)
	assert.Equal(t, []string{"event-a", "event-b"}, *notified)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// AC9: a RESOLVED with an unknown finding_id and nothing open for its fingerprint
// inserts nothing. Before the fix it stored a new, already-resolved event.
func TestInvestigateEvent_ResolveForUnknownFindingIdInsertsNothing(t *testing.T) {
	mock, notified := setupResolveTest(t)
	e := automationEvent(EventStatusResolved, "never-seen", "calico-node:unavailable")

	mock.ExpectQuery(findingLookupSQL).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(closeByFingerprin).
		WithArgs(resolveTestTenant, resolveTestAccount, e.Fingerprint, "CLOSED", sqlmock.AnyArg(), "automation").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	id, err := InvestigateEvent(resolveTestContext(), e, "")
	require.NoError(t, err)
	assert.Empty(t, id)
	assert.Empty(t, *notified)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A CLOSED under an unknown finding_id is a record born closed, not a recovery.
// cloud-collector sends CloudTrail, EventBridge and Cloud Foundry events that way,
// fingerprint set, and they must keep being inserted — so only RESOLVED falls back
// to the fingerprint close. An empty finding_id names no record, so either status
// closes by fingerprint there.
func TestResolveFingerprintFallbackRules(t *testing.T) {
	withFp := func(status EventStatus, findingId string) Event {
		return Event{Status: status, FindingId: findingId, Fingerprint: "fp"}
	}

	assert.True(t, resolvesUnmatchedByFingerprint(withFp(EventStatusResolved, "x")))
	assert.True(t, resolvesUnmatchedByFingerprint(withFp("resolved", "x")), "status is matched case-insensitively")
	assert.False(t, resolvesUnmatchedByFingerprint(withFp(EventStatusClosed, "x")), "one-shot CLOSED records must still insert")
	assert.False(t, resolvesUnmatchedByFingerprint(withFp(EventStatusFiring, "x")))
	assert.False(t, resolvesUnmatchedByFingerprint(Event{Status: EventStatusResolved, FindingId: "x"}), "no fingerprint, nothing to close by")

	assert.True(t, IsResolveByFingerprint(withFp(EventStatusResolved, "")))
	assert.True(t, IsResolveByFingerprint(withFp(EventStatusClosed, "")))
	assert.False(t, IsResolveByFingerprint(withFp(EventStatusFiring, "")))
	assert.False(t, IsResolveByFingerprint(Event{Status: EventStatusResolved}))
}
