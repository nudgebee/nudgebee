package triage

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	resolveRunID  = "11111111-1111-1111-1111-111111111111"
	resolveUserID = "77777777-7777-7777-7777-777777777777"
)

func expectRun(mock sqlmock.Sqlmock, eventID, verdict, causeKey string) {
	expectRunFired(mock, eventID, verdict, causeKey, false)
}

func expectRunFired(mock sqlmock.Sqlmock, eventID, verdict, causeKey string, firedAgain bool) {
	mock.ExpectQuery(regexp.QuoteMeta(`FROM event_resolution er JOIN events e ON e.id = er.event_id`)).WithArgs(resolveRunID, "acc").
		WillReturnRows(sqlmock.NewRows([]string{"event_id", "verdict", "cause_key", "fired_again"}).AddRow(eventID, verdict, causeKey, firedAgain))
}

// labelWith captures the nb_resolved_by label written.
type labelWith struct{ got *map[string]any }

func (l labelWith) Match(v driver.Value) bool {
	s, ok := v.(string)
	return ok && json.Unmarshal([]byte(s), l.got) == nil
}

// A person confirms "Likely fixed": the event is resolved in their name, labelled with the run's own verdict and
// cause key (read from the run, not the caller) and the person who confirmed.
func TestConfirmRemediationResolveResolvesInThePersonsName(t *testing.T) {
	oldNow := resolveNow
	resolveNow = func() time.Time { return time.Date(2026, 10, 24, 10, 54, 52, 0, time.UTC) }
	t.Cleanup(func() { resolveNow = oldNow })
	db, mock := verdictMock(t)
	mock.MatchExpectationsInOrder(false) // UpdateNBStatus logs its history in the background
	expectRun(mock, "ev", VerdictFixed, `{"what": "memory", "how": "oom", "against": "deploy api"}`)
	expectTarget(mock, "ev", NBStatusOpen, "")
	mock.ExpectQuery(regexp.QuoteMeta(`FROM users WHERE id = $1::uuid`)).WithArgs(resolveUserID).
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("Asha"))
	label := map[string]any{}
	mock.ExpectExec(regexp.QuoteMeta(`SET labels = (CASE WHEN jsonb_typeof(labels) = 'object'`)).
		WithArgs(LabelResolvedBy, labelWith{&label}, "ev", "acc").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT nb_status FROM events WHERE id = $1`)).WithArgs("ev", "acc").
		WillReturnRows(sqlmock.NewRows([]string{"nb_status"}).AddRow(NBStatusOpen))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE events`)).WithArgs(NBStatusResolved, resolveUserID, "ev", "acc").WillReturnResult(sqlmock.NewResult(0, 1))

	resp, err := ConfirmRemediationResolve(context.Background(), db, "acc", "ev", resolveRunID, resolveUserID)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, []RemediationVerdictEvent{{"ev", NBStatusOpen, NBStatusResolved}}, resp.Events)
	assert.Equal(t, map[string]any{"run_id": resolveRunID, "verdict": VerdictFixed, "confirmed_by": resolveUserID, "confirmed_by_name": "Asha",
		"confirmed_at": "2026-10-24T10:54:52Z", "cause_key": map[string]any{"what": "memory", "how": "oom", "against": "deploy api"}}, label)
	assert.Equal(t, resolveRunID, resolvedRunID(mustJSON(t, label)), "triage reads the run of the label, so a re-fire is still labelled")
}

// Nothing to confirm: a run with no likely-fixed verdict, a run of another event, no person, or no run.
func TestConfirmRemediationResolveRefuses(t *testing.T) {
	db, mock := verdictMock(t)
	expectRun(mock, "ev", "not_fixed", "null")
	_, err := ConfirmRemediationResolve(context.Background(), db, "acc", "ev", resolveRunID, resolveUserID)
	assert.ErrorIs(t, err, ErrNoVerdictToConfirm)

	expectRun(mock, "other", VerdictFixed, "null")
	expectTarget(mock, "ev", NBStatusOpen, "")
	_, err = ConfirmRemediationResolve(context.Background(), db, "acc", "ev", resolveRunID, resolveUserID)
	assert.ErrorIs(t, err, ErrNoVerdictToConfirm, "a run of an event this one does not follow")
	require.NoError(t, mock.ExpectationsWereMet())

	// The alert came back after the check: "Likely fixed" no longer stands, on any surface.
	expectRunFired(mock, "ev", VerdictFixed, "null", true)
	_, err = ConfirmRemediationResolve(context.Background(), db, "acc", "ev", resolveRunID, resolveUserID)
	assert.ErrorIs(t, err, ErrFiredAgainAfterVerdict)
	require.NoError(t, mock.ExpectationsWereMet())

	_, err = ConfirmRemediationResolve(context.Background(), db, "acc", "ev", resolveRunID, "")
	assert.Error(t, err, "a person confirms")
	_, err = ConfirmRemediationResolve(context.Background(), db, "acc", "ev", "not-a-run", resolveUserID)
	assert.ErrorIs(t, err, ErrNoVerdictToConfirm)
}

// A duplicate's run is on its main event: confirming on the duplicate resolves both.
func TestConfirmRemediationResolveOnADuplicate(t *testing.T) {
	db, mock := verdictMock(t)
	mock.MatchExpectationsInOrder(false)
	expectRun(mock, "main", VerdictClearedBeforeChange, "null")
	expectTarget(mock, "dup", NBStatusDuplicate, "main")
	mock.ExpectQuery(regexp.QuoteMeta(`FROM users WHERE id = $1::uuid`)).WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("Asha"))
	expectTarget(mock, "dup", NBStatusDuplicate, "main")
	expectTarget(mock, "main", NBStatusOpen, "")
	for _, id := range []string{"dup", "main"} {
		expectLabel(mock, LabelResolvedBy, id)
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT nb_status FROM events WHERE id = $1`)).WithArgs(id, "acc").
			WillReturnRows(sqlmock.NewRows([]string{"nb_status"}).AddRow(NBStatusOpen))
		mock.ExpectExec(regexp.QuoteMeta(`UPDATE events`)).WithArgs(NBStatusResolved, resolveUserID, id, "acc").WillReturnResult(sqlmock.NewResult(0, 1))
	}
	resp, err := ConfirmRemediationResolve(context.Background(), db, "acc", "dup", resolveRunID, resolveUserID)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Len(t, resp.Events, 2)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return string(raw)
}
