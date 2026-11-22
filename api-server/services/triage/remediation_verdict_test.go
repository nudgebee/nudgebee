package triage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func verdictMock(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	raw, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	return sqlx.NewDb(raw, "sqlmock"), mock
}

func expectTarget(mock sqlmock.Sqlmock, id, status, primary string) {
	mock.ExpectQuery(regexp.QuoteMeta(`AS primary_id`)).WithArgs(id, "acc").
		WillReturnRows(sqlmock.NewRows([]string{"id", "nb_status", "primary_id"}).AddRow(id, status, primary))
}

func expectLabel(mock sqlmock.Sqlmock, key, id string) {
	mock.ExpectExec(regexp.QuoteMeta(`SET labels = (CASE WHEN jsonb_typeof(labels) = 'object'`)).
		WithArgs(key, sqlmock.AnyArg(), id, "acc").WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectStatus(mock sqlmock.Sqlmock, id, prev, next string) {
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT nb_status FROM events WHERE id = $1`)).WithArgs(id, "acc").
		WillReturnRows(sqlmock.NewRows([]string{"nb_status"}).AddRow(prev))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE events`)).WithArgs(next, systemUserID, id, "acc").WillReturnResult(sqlmock.NewResult(0, 1))
}

// C5: a verdict resolves the run's event and, for a duplicate, its main event, with the system identity; both
// carry nb_resolved_by, written before the status.
func TestApplyRemediationVerdictResolvesTheDuplicateAndItsMainEvent(t *testing.T) {
	db, mock := verdictMock(t)
	mock.MatchExpectationsInOrder(false) // UpdateNBStatus logs its history in the background
	expectTarget(mock, "dup", NBStatusDuplicate, "main")
	expectTarget(mock, "main", NBStatusOpen, "")
	expectLabel(mock, LabelResolvedBy, "dup")
	expectStatus(mock, "dup", NBStatusDuplicate, NBStatusResolved)
	expectLabel(mock, LabelResolvedBy, "main")
	expectStatus(mock, "main", NBStatusOpen, NBStatusResolved)

	resp, err := ApplyRemediationVerdict(context.Background(), db, RemediationVerdictRequest{
		AccountID: "acc", EventID: "dup", NBStatus: NBStatusResolved, ReasonText: "Resolved by NudgeBee: the alert cleared",
		ResolvedBy: map[string]any{"run_id": "run-1", "verdict": "fixed", "cause_key": map[string]any{"what": "memory", "how": "oom", "against": "api"}},
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, []RemediationVerdictEvent{{"dup", NBStatusDuplicate, NBStatusResolved}, {"main", NBStatusOpen, NBStatusResolved}}, resp.Events)
}

// An event already resolved only gets the label; a dropped one is left alone; a missing run id is refused.
func TestApplyRemediationVerdictEdges(t *testing.T) {
	db, mock := verdictMock(t)
	expectTarget(mock, "ev", NBStatusResolved, "")
	expectLabel(mock, LabelResolvedBy, "ev")
	resp, err := ApplyRemediationVerdict(context.Background(), db, RemediationVerdictRequest{AccountID: "acc", EventID: "ev",
		NBStatus: NBStatusResolved, ResolvedBy: map[string]any{"run_id": "run-1", "verdict": "fixed"}})
	require.NoError(t, err)
	assert.Empty(t, resp.Events)

	expectTarget(mock, "gone", NBStatusDropped, "")
	resp, err = ApplyRemediationVerdict(context.Background(), db, RemediationVerdictRequest{AccountID: "acc", EventID: "gone",
		NBStatus: NBStatusResolved, ResolvedBy: map[string]any{"run_id": "run-1"}})
	require.NoError(t, err)
	assert.Empty(t, resp.Events)
	require.NoError(t, mock.ExpectationsWereMet())

	_, err = ApplyRemediationVerdict(context.Background(), db, RemediationVerdictRequest{AccountID: "acc", EventID: "ev", NBStatus: NBStatusResolved})
	assert.Error(t, err, "a resolve names its run")
	_, err = ApplyRemediationVerdict(context.Background(), db, RemediationVerdictRequest{AccountID: "acc", EventID: "ev", NBStatus: NBStatusOpen})
	assert.Error(t, err, "only RESOLVED and SUPPRESSED")
}

// A mute the person picked suppresses the event "chosen by <person>", with no resolve label.
func TestApplyRemediationVerdictPickedMute(t *testing.T) {
	db, mock := verdictMock(t)
	mock.MatchExpectationsInOrder(false)
	expectTarget(mock, "ev", NBStatusOpen, "")
	mock.ExpectQuery(regexp.QuoteMeta(`FROM users WHERE id = $1::uuid`)).WithArgs("77777777-7777-7777-7777-777777777777").
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("Asha"))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT nb_status FROM events WHERE id = $1`)).WillReturnRows(sqlmock.NewRows([]string{"nb_status"}).AddRow(NBStatusOpen))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE events`)).WithArgs(NBStatusSuppressed, systemUserID, "ev", "acc").WillReturnResult(sqlmock.NewResult(0, 1))
	resp, err := ApplyRemediationVerdict(context.Background(), db, RemediationVerdictRequest{AccountID: "acc", EventID: "ev",
		NBStatus: NBStatusSuppressed, ChosenBy: "77777777-7777-7777-7777-777777777777"})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, []RemediationVerdictEvent{{"ev", NBStatusOpen, NBStatusSuppressed}}, resp.Events)
	assert.Equal(t, "Bob", personName(context.Background(), db, "Bob"), "a name that is not a user id is kept")
}

// C6: a firing that starts within 24 h of the loop resolve (or was already firing) is a re-fire.
func TestIsRefire(t *testing.T) {
	resolved := time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := resolved.Add(d); return &v }
	assert.True(t, isRefire(at(time.Hour), &resolved))
	assert.True(t, isRefire(at(24*time.Hour), &resolved))
	assert.False(t, isRefire(at(25*time.Hour), &resolved))
	assert.True(t, isRefire(at(-time.Minute), &resolved), "already firing when it was resolved")
	assert.False(t, isRefire(nil, &resolved))
	assert.False(t, isRefire(at(time.Hour), nil))
}

// The labels are JSON objects stored as strings, as every event label value is.
func TestRemediationLabelShapes(t *testing.T) {
	value, err := refireLabelValue(RefireKindUnknown, "run-1", "")
	require.NoError(t, err)
	var refire map[string]string
	require.NoError(t, json.Unmarshal([]byte(value), &refire))
	assert.Equal(t, map[string]string{"kind": "unknown", "previous_run_id": "run-1"}, refire)
	assert.Equal(t, "run-1", resolvedRunID(`{"run_id":"run-1","verdict":"fixed","cause_key":null}`))
	assert.Equal(t, "", resolvedRunID(`not json`))
}

func TestUpdateRefireLabel(t *testing.T) {
	db, mock := verdictMock(t)
	mock.ExpectExec(regexp.QuoteMeta(`labels ? $1::text`)).WithArgs(LabelRefire, `{"kind":"fix_did_not_hold","previous_run_id":"run-1"}`, "ev", "acc").
		WillReturnResult(sqlmock.NewResult(0, 1))
	updated, err := UpdateRefireLabel(context.Background(), db, "acc", "ev", RefireKindFixDidNotHold, "run-1", "")
	require.NoError(t, err)
	assert.True(t, updated)
	_, err = UpdateRefireLabel(context.Background(), db, "acc", "ev", "different_cause", "run-1", "")
	assert.Error(t, err, "no different_cause in the first release")
	require.NoError(t, mock.ExpectationsWereMet())
}

// A compared re-fire carries the plan version it was compared on, the same cause or not.
func TestUpdateRefireLabelRecordsTheComparedPlan(t *testing.T) {
	db, mock := verdictMock(t)
	version := strings.Repeat("a", 64)
	mock.ExpectExec(regexp.QuoteMeta(`labels ? $1::text`)).
		WithArgs(LabelRefire, `{"compared_plan":"`+version+`","kind":"unknown","previous_run_id":"run-1"}`, "ev", "acc").
		WillReturnResult(sqlmock.NewResult(0, 1))
	updated, err := UpdateRefireLabel(context.Background(), db, "acc", "ev", RefireKindUnknown, "run-1", version)
	require.NoError(t, err)
	assert.True(t, updated)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A verdict moves the main event only when the remediation read followed it: the duplicate rule here is the
// read's own (llm-server events.PlanEventOf), and the two must not drift.
func TestLivePrimaryEventRuleMatchesTheRead(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	src, err := os.ReadFile(filepath.Join(root, "llm/llm-server/events/remediation_plan_event.go"))
	if err != nil {
		t.Skipf("llm-server source not available: %v", err)
	}
	m := regexp.MustCompile("(?s)const livePrimaryEventSQL = `([^`]*)`").FindSubmatch(src)
	require.NotNil(t, m, "llm-server's livePrimaryEventSQL was renamed")
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	assert.Equal(t, norm(string(m[1])), norm(livePrimaryEventSQL))
}

// A duplicate's main event whose alert is still firing (another fingerprint, or a re-fire) is not resolved with
// the duplicate: only the duplicate moves, and the main keeps no nb_resolved_by label.
func TestApplyRemediationVerdictLeavesAFiringMainEventOpen(t *testing.T) {
	db, mock := verdictMock(t)
	mock.MatchExpectationsInOrder(false) // UpdateNBStatus logs its history in the background
	expectTarget(mock, "dup", NBStatusDuplicate, "main")
	mock.ExpectQuery(regexp.QuoteMeta(`AS primary_id`)).WithArgs("main", "acc").
		WillReturnRows(sqlmock.NewRows([]string{"id", "nb_status", "primary_id", "firing"}).AddRow("main", NBStatusOpen, "", true))
	expectLabel(mock, LabelResolvedBy, "dup")
	expectStatus(mock, "dup", NBStatusDuplicate, NBStatusResolved)

	resp, err := ApplyRemediationVerdict(context.Background(), db, RemediationVerdictRequest{
		AccountID: "acc", EventID: "dup", NBStatus: NBStatusResolved, ReasonText: "Resolved by Asha",
		ResolvedBy: map[string]any{"run_id": "run-1", "verdict": "fixed"},
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet(), "no label or status write on the firing main event")
	assert.Equal(t, []RemediationVerdictEvent{{"dup", NBStatusDuplicate, NBStatusResolved}}, resp.Events)
}
