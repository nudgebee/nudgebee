package api

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

var verdictT0 = time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)

func TestDecideVerdict(t *testing.T) {
	ran := verdictT0
	clearAfter := alertState{Known: true, Clear: true, ClearedAt: ran.Add(2 * time.Minute)}
	for _, tc := range []struct {
		name string
		in   verdictInput
		want verdictResult
	}{
		{"a rollback never counts", verdictInput{Status: "Success", Rollback: true, RanAt: ran, Rechecks: 1, Signal: clearAfter},
			verdictResult{Verdict: verdictUnclear, Reason: "rollback_not_a_fix"}},
		{"a failed change is not a fix", verdictInput{Status: "Failed", RanAt: ran, Signal: clearAfter},
			verdictResult{Verdict: verdictNotFixed, Reason: "change_failed"}},
		{"an unknown outcome is unclear", verdictInput{Status: "Unknown", RanAt: ran, Signal: clearAfter},
			verdictResult{Verdict: verdictUnclear, Reason: "change_outcome_unknown"}},
		{"an unread alert is unclear", verdictInput{Status: "Success", RanAt: ran},
			verdictResult{Verdict: verdictUnclear, Reason: "alert_state_unread"}},
		{"fired again after the change", verdictInput{Status: "Success", RanAt: ran, Signal: alertState{Known: true, Clear: true, ClearedAt: ran.Add(time.Minute), FiredAgain: true}},
			verdictResult{Verdict: verdictNotFixed, Reason: "fired_again_after_change"}},
		{"cleared before the change waits for the re-check too", verdictInput{Status: "Success", RanAt: ran, Signal: alertState{Known: true, Clear: true, ClearedAt: ran.Add(-time.Minute)}},
			verdictResult{Hold: true, Reason: "cleared_before_change"}},
		{"cleared before the change and held at the re-check", verdictInput{Status: "Success", RanAt: ran, Rechecks: 1, Held: true, Signal: alertState{Known: true, Clear: true, ClearedAt: ran.Add(-time.Minute)}},
			verdictResult{Verdict: verdictClearedBeforeChange, Reason: "cleared_before_change"}},
		{"cleared after the change waits for the re-check", verdictInput{Status: "Success", RanAt: ran, Signal: clearAfter},
			verdictResult{Hold: true, Reason: "cleared_after_change"}},
		{"cleared after the change and held at the re-check", verdictInput{Status: "Success", RanAt: ran, Rechecks: 1, Held: true, Signal: clearAfter},
			verdictResult{Verdict: verdictFixed, Reason: "cleared_after_change_and_held"}},
		{"first seen clear at a re-check the model asked for: a hold starts, not fixed", verdictInput{Status: "Success", RanAt: ran, Rechecks: 1, Signal: clearAfter},
			verdictResult{Hold: true, Reason: "cleared_after_change"}},
		{"no clear time", verdictInput{Status: "Success", RanAt: ran, Signal: alertState{Known: true, Clear: true}},
			verdictResult{Verdict: verdictUnclear, Reason: "clear_time_unknown"}},
		{"still firing right after the change: too early", verdictInput{Status: "Success", RanAt: ran, Signal: alertState{Known: true}},
			verdictResult{Reason: "too_early"}},
		{"still firing at the re-check", verdictInput{Status: "Success", RanAt: ran, Rechecks: 1, Signal: alertState{Known: true}},
			verdictResult{Verdict: verdictNotFixed, Reason: "still_firing"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, decideVerdict(tc.in))
		})
	}
}

// verdictAt is verdictT0 moved by d.
func verdictAt(d time.Duration) *time.Time { v := verdictT0.Add(d); return &v }

// firing is one event of the alert from start to end (nil end: still firing).
func firing(id string, start time.Duration, end *time.Duration) events.AlertFiring {
	f := events.AlertFiring{Id: id, Status: "FIRING", StartsAt: verdictAt(start), SubjectType: "pod", SubjectName: "api-1", SubjectNS: "shop"}
	if end != nil {
		f.Status, f.EndsAt = "CLOSED", verdictAt(*end)
	}
	return f
}

func ended(d time.Duration) *time.Duration { return &d }

func signalOf(firings ...events.AlertFiring) events.AlertSignal {
	return events.AlertSignal{SubjectType: "pod", SubjectName: "api-1", SubjectNS: "shop", Owner: "api", OwnerKind: "Deployment", Firings: firings}
}

func noneGone(events.AlertFiring) bool { return false }

func TestVerdictAlertState(t *testing.T) {
	ran, now := verdictT0, verdictT0.Add(10*time.Minute)

	st := verdictAlertState(signalOf(firing("e", -time.Hour, ended(3*time.Minute))), ran, now, noneGone)
	assert.Equal(t, alertState{Known: true, Clear: true, ClearedAt: ran.Add(3 * time.Minute)}, st)

	st = verdictAlertState(signalOf(firing("e", -time.Hour, nil)), ran, now, noneGone)
	assert.Equal(t, alertState{Known: true}, st, "a firing alert is not clear")

	st = verdictAlertState(signalOf(firing("e", -time.Hour, ended(time.Hour))), ran, now, noneGone)
	assert.True(t, st.Clear)
	assert.True(t, st.ClearedAt.IsZero(), "an end in the future is not a known end")

	st = verdictAlertState(signalOf(firing("e", -time.Hour, ended(-time.Hour))), ran, now, noneGone)
	assert.Equal(t, ran, st.ClearedAt, "a point-in-time alert counts as clear from the change on")

	assert.False(t, verdictAlertState(events.AlertSignal{}, ran, now, noneGone).Known, "no event read")
	noState := firing("e", -time.Hour, nil)
	noState.Status = ""
	assert.False(t, verdictAlertState(signalOf(noState), ran, now, noneGone).Known, "a row with no state from the source says nothing")
}

// Settling window: a firing that starts within 2 minutes of the change and has ended is the change settling.
// One that is still open makes the alert not clear; one after the window is a re-fire, unless it ended for a
// subject that no longer exists.
func TestVerdictAlertStateSettling(t *testing.T) {
	ran, now := verdictT0, verdictT0.Add(6*time.Minute)

	st := verdictAlertState(signalOf(firing("e", -time.Hour, ended(7*time.Second)), firing("echo", 6*time.Second, ended(7*time.Second))), ran, now, noneGone)
	assert.Equal(t, alertState{Known: true, Clear: true, ClearedAt: ran.Add(7 * time.Second)}, st, "an echo inside the window is ignored")

	st = verdictAlertState(signalOf(firing("e", -time.Hour, ended(7*time.Second)), firing("echo", 6*time.Second, nil)), ran, now, noneGone)
	assert.Equal(t, alertState{Known: true}, st, "an echo still open: not clear, not fired again")

	st = verdictAlertState(signalOf(firing("e", -time.Hour, ended(7*time.Second)), firing("refire", 3*time.Minute, ended(4*time.Minute))), ran, now, noneGone)
	assert.True(t, st.FiredAgain, "after the window, an ended firing of an existing subject is a re-fire")

	st = verdictAlertState(signalOf(firing("e", -time.Hour, ended(7*time.Second)), firing("old-pod", 3*time.Minute, ended(4*time.Minute))), ran, now,
		func(f events.AlertFiring) bool { return f.Id == "old-pod" })
	assert.False(t, st.FiredAgain, "an ended firing of a subject that no longer exists is not a re-fire")
	assert.True(t, st.Clear)

	st = verdictAlertState(signalOf(firing("e", -time.Hour, ended(7*time.Second)), firing("refire", 3*time.Minute, nil)), ran, now,
		func(events.AlertFiring) bool { return true })
	assert.True(t, st.FiredAgain, "a firing still open after the window is a re-fire, whatever its subject")
	assert.False(t, st.Clear)
}

// Fixtures shaped exactly like contract C1 (chat run record) and the run_step / panel rows.
func c1Data(t *testing.T, extra map[string]any) []byte {
	t.Helper()
	d := map[string]any{"source": "chat", "tool": "kubectl", "command": "kubectl scale deploy/api -n shop --replicas=0",
		"approval_key": "kubectl:abc:msg-1", "approved_by": "user-1", "exit_code": 0, "exit_code_reported": true, "slot": "execute"}
	for k, v := range extra {
		d[k] = v
	}
	raw, err := json.Marshal(d)
	require.NoError(t, err)
	return raw
}

func TestVerdictRunFacts(t *testing.T) {
	chat := verdictFactsOf(c1Data(t, nil))
	assert.False(t, chat.planStep(), "a chat write with no plan option has no plan step")
	assert.False(t, chat.rollback())
	assert.True(t, verdictFactsOf(c1Data(t, map[string]any{"plan_version": "v1", "option": 2})).planStep())
	assert.True(t, verdictFactsOf(c1Data(t, map[string]any{"slot": "rollback"})).rollback())
	assert.True(t, verdictFactsOf([]byte(`{"step":"1:rollback","slot":"rollback"}`)).rollback())
	assert.True(t, verdictFactsOf([]byte(`{"step":"0:execute","source":"chat"}`)).planStep(), "run_step rows are plan steps")
	assert.True(t, verdictFactsOf([]byte(`{"manual":true,"slot":"manual"}`)).planStep(), "a step marked done (C2) is a plan step")

	claimed := verdictFactsOf([]byte(`{"followup":{"claimed_at":"2026-10-22T11:00:00.5+05:30","verdict_checked_at":"2026-10-22T05:31:00Z"}}`))
	assert.True(t, claimed.checkedThisClaim())
	reclaimed := verdictFactsOf([]byte(`{"followup":{"claimed_at":"2026-10-22T11:10:00+05:30","verdict_checked_at":"2026-10-22T05:31:00Z"}}`))
	assert.False(t, reclaimed.checkedThisClaim(), "a re-check is a new claim")
}

// The verdict reads the plan's cause key through the shared C8 normalizer (events.NormalizeCauseKey).
func TestCauseKeyOfPlan(t *testing.T) {
	got := causeKeyOfPlan(`{"summary":"x","cause_key":{"what":" Container  Memory ","how":"OOMKilled","against":"deploy/api-server"}}`)
	require.NotNil(t, got)
	assert.Equal(t, events.NormalizeCauseKey(events.CauseKey{What: "container memory", How: "oom", Against: "deploy api server"}), *got)
	assert.Equal(t, "oom", got.How)
	assert.Nil(t, causeKeyOfPlan(`{"summary":"x"}`))
	assert.Nil(t, causeKeyOfPlan(`{"cause_key":{"what":"memory","how":"","against":"api"}}`), "an empty field is not comparable")

	assert.True(t, sameCause(&events.CauseKey{What: "memory", How: "OOM_killed", Against: "Deploy/API"}, &events.CauseKey{What: "Memory", How: "oom-killed", Against: "deploy api"}))
	assert.False(t, sameCause(&events.CauseKey{What: "memory", How: "oom", Against: "api"}, &events.CauseKey{What: "cpu", How: "throttled", Against: "api"}))
	assert.False(t, sameCause(&events.CauseKey{What: "memory", How: "oom", Against: "api"}, nil))
}

// Every compared re-fire is marked with the plan it was compared on, so
// one that stays "unknown" is not read again every minute and newer ones get their turn.
func TestCompareRefiresMarksEveryComparedRefire(t *testing.T) {
	resolvedBy := `{"run_id":"run-1","verdict":"fixed","cause_key":{"what":"deploy api","how":"oom","against":"memory limit"}}`
	swap(t, &refiresToCompare, func(context.Context, *sqlx.DB) ([]events.RefireToCompare, error) {
		return []events.RefireToCompare{
			{EventId: "same", AccountId: "acc", PreviousRunId: "run-1", PlanVersion: "v-same", ResolvedBy: resolvedBy,
				Plan: `{"cause_key":{"what":"deploy api","how":"OOMKilled","against":"memory limit"}}`},
			{EventId: "other", AccountId: "acc", PreviousRunId: "run-1", PlanVersion: "v-other", ResolvedBy: resolvedBy,
				Plan: `{"cause_key":{"what":"deploy api","how":"crashloop","against":"bad config"}}`},
		}, nil
	})
	var labelled []map[string]any
	swap(t, &updateRefireLabel, func(_ string, input map[string]any) error {
		labelled = append(labelled, input)
		return nil
	})
	compareRefires(nil)
	assert.Equal(t, []map[string]any{
		{"account_id": "acc", "event_id": "same", "kind": refireFixDidNotHold, "previous_run_id": "run-1", "compared_plan": "v-same"},
		{"account_id": "acc", "event_id": "other", "kind": refireUnknown, "previous_run_id": "run-1", "compared_plan": "v-other"},
	}, labelled)
}

// RefiresToCompare computes a plan's version in SQL (sha256 of its text, hex); it must equal events.PlanVersion.
func TestPlanVersionMatchesTheSQLHash(t *testing.T) {
	// encode(sha256(convert_to('{"a":"é"}', 'UTF8')), 'hex') in Postgres.
	assert.Equal(t, "b3a092a6af48807fa9482b2ee140105575daa26d5b24b3c0e60a7e2dee6683b1", events.PlanVersion(`{"a":"é"}`))
}

func TestRefireSameCause(t *testing.T) {
	plan := `{"cause_key":{"what":"memory","how":"OOMKilled","against":"deploy/api"}}`
	same := `{"run_id":"run-1","verdict":"fixed","cause_key":{"what":"memory","how":"oom","against":"deploy api"}}`
	other := `{"run_id":"run-1","verdict":"fixed","cause_key":{"what":"cpu","how":"throttled","against":"deploy api"}}`
	none := `{"run_id":"run-1","verdict":"fixed","cause_key":null}`
	assert.True(t, refireSameCause(events.RefireToCompare{Plan: plan, ResolvedBy: same}))
	assert.False(t, refireSameCause(events.RefireToCompare{Plan: plan, ResolvedBy: other}))
	assert.False(t, refireSameCause(events.RefireToCompare{Plan: plan, ResolvedBy: none}))
	assert.False(t, refireSameCause(events.RefireToCompare{Plan: plan, ResolvedBy: ""}))
}

// verdictDB is a mock database for the verdict's writes, which accepts any statement it is told about.
func verdictDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	raw, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	return sqlx.NewDb(raw, "sqlmock"), mock
}

func stubVerdict(t *testing.T, signal events.AlertSignal) *[]map[string]any {
	t.Helper()
	calls := &[]map[string]any{}
	swap(t, &verdictNow, func() time.Time { return verdictT0.Add(6 * time.Minute) })
	swap(t, &readAlertSignal, func(*sqlx.DB, followupRun) (events.AlertSignal, error) { return signal, nil })
	swap(t, &firingSubjectGone, func(followupRun, events.AlertFiring) bool { return false })
	swap(t, &snapshotCauseKey, func(*sqlx.DB, followupRun, verdictRun, *slog.Logger) {})
	swap(t, &applyRemediationVerdict, func(_ string, input map[string]any) error {
		*calls = append(*calls, input)
		return nil
	})
	return calls
}

// stepData is a plan step run from the alert's chat (C1 with its plan option).
func stepData(t *testing.T, extra map[string]any) []byte {
	t.Helper()
	d := map[string]any{"plan_version": "v1", "option": 1}
	maps.Copy(d, extra)
	return c1Data(t, d)
}

// mergedInto is a statement argument holding a JSON patch of data.followup: it merges the patch into record,
// as the statement's jsonb || does, so a test can read the follow-up record a run of patches leaves.
type mergedInto struct {
	t      *testing.T
	record map[string]any
}

func (m mergedInto) Match(v driver.Value) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	var patch map[string]any
	if err := json.Unmarshal([]byte(s), &patch); err != nil {
		m.t.Errorf("patch is not a JSON object: %q", s)
		return false
	}
	maps.Copy(m.record, patch)
	return true
}

func expectNoPlan(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta(`AS main) p`)).WillReturnRows(sqlmock.NewRows([]string{"plan_event"}).AddRow("ev"))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT a.analysis FROM event_log_analysis a`)).WillReturnRows(sqlmock.NewRows([]string{"analysis"}).
		AddRow(`{"cause_key":{"what":"memory","how":"OOMKilled","against":"deploy/api"}}`))
}

// heldData is a plan step's follow-up at the re-check that ends its hold.
func heldData(t *testing.T, extra map[string]any) []byte {
	t.Helper()
	return stepData(t, map[string]any{"followup": map[string]any{"claimed_at": verdictT0.Add(15 * time.Minute).Format(time.RFC3339Nano),
		"rechecks": 1, "hold_until": verdictT0.Add(15 * time.Minute).Format(time.RFC3339), "hold_from": verdictT0.Add(6 * time.Minute).Format(time.RFC3339Nano)}})
}

// An alert that closed after a plan step waits out the hold with no model turn, then ends as a suggestion
// ("Likely fixed") with the verdict and the plan's cause key on the run row (C4). Nothing resolves the event: a
// person does, from the tab.
func TestSettleByVerdictSuggestsAFixThatHeld(t *testing.T) {
	calls := stubVerdict(t, signalOf(firing("ev", -time.Hour, ended(2*time.Minute))))
	db, mock := verdictDB(t)
	run := followupRun{Id: "run-1", EventId: "ev", AccountId: "acc", Status: "Success", CreatedAt: verdictT0,
		Data: stepData(t, map[string]any{"followup": map[string]any{"claimed_at": verdictT0.Format(time.RFC3339Nano)}})}

	// First look: the hold's re-check is scheduled (the rule's for: is not known: 15 min), and the run row says until
	// when the fix must hold.
	record := map[string]any{}
	mock.ExpectExec(regexp.QuoteMeta(`'recheck_request'`)).WithArgs("run-1", 15, soakReason, followupMaxRechecks+1).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`'recheck_at'`)).WithArgs("run-1", "", 15, 15, followupMaxRechecks+1).
		WillReturnRows(sqlmock.NewRows([]string{"at"}).AddRow("2026-10-22T16:51:00.123456+05:30"))
	mock.ExpectExec(regexp.QuoteMeta(`data->'followup' || $2::jsonb`)).WithArgs("run-1", mergedInto{t, record}).WillReturnResult(sqlmock.NewResult(0, 1))
	assert.True(t, settleByVerdict(db, run, slog.Default()), "no model turn")
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, map[string]any{"hold_until": "2026-10-22T11:21:00Z", "hold_from": "2026-10-22T11:06:00Z"}, record, "the re-check time, in UTC, and when the hold began")

	// The re-check: the follow-up ends as a suggestion with the verdict and the cause key; hold_until stays.
	swap(t, &verdictNow, func() time.Time { return verdictT0.Add(16 * time.Minute) })
	run.Data = heldData(t, nil)
	record["hold_until"] = "2026-10-22T11:21:00Z"
	expectNoPlan(mock)
	mock.ExpectExec(regexp.QuoteMeta(`'done_at', now()`)).WithArgs("run-1", mergedInto{t, record}).WillReturnResult(sqlmock.NewResult(0, 1))
	assert.True(t, settleByVerdict(db, run, slog.Default()))
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, followupLikelyFixed, record["outcome"])
	assert.Equal(t, verdictFixed, record["verdict"])
	assert.Equal(t, "cleared_after_change_and_held", record["verdict_reason"])
	assert.Equal(t, true, record["cause_key_read"])
	assert.Equal(t, map[string]any{"what": "memory", "how": "oom", "against": "deploy api"}, record["cause_key"])
	assert.Equal(t, "2026-10-22T11:21:00Z", record["hold_until"], "the record keeps the time the fix had to hold until")
	assert.Empty(t, *calls, "the event is never resolved by the verdict")
}

// The hold is the rule's for: (plus the settling window) when the rule is known, 15 min when it is not.
func TestVerdictHold(t *testing.T) {
	assert.Equal(t, 15*time.Minute, verdictHold(events.AlertSignal{}))
	assert.Equal(t, 17*time.Minute, verdictHold(events.AlertSignal{RuleForKnown: true, RuleForSeconds: 15 * 60}))
	assert.Equal(t, 5*time.Minute, verdictHold(events.AlertSignal{RuleForKnown: true}), "a rule that fires at once: the 5-min minimum")
	assert.Equal(t, 2*time.Hour, verdictHold(events.AlertSignal{RuleForKnown: true, RuleForSeconds: 6 * 3600}))
}

// A rollout undo: the fix ran at T; the rollout's echo for the old pod fired from T+6 s to
// T+7 s, twice (the source writes each transition as two rows); the alert's own event closed at T+7 s. The
// first look holds for the re-check, and the re-check suggests it fixed.
func TestSettleByVerdictRolloutEchoIsNotARefire(t *testing.T) {
	signal := signalOf(
		firing("echo-twin", 6*time.Second, ended(7*time.Second)),
		firing("echo", 6*time.Second, ended(7*time.Second)),
		firing("ev", -11*time.Minute, ended(7*time.Second)),
	)
	calls := stubVerdict(t, signal)
	swap(t, &verdictNow, func() time.Time { return verdictT0.Add(20 * time.Second) })
	db, mock := verdictDB(t)
	run := followupRun{Id: "run-1", EventId: "ev", AccountId: "acc", Status: "Success", CreatedAt: verdictT0,
		Data: stepData(t, map[string]any{"command": "kubectl rollout undo deploy/api -n shop", "followup": map[string]any{"claimed_at": verdictT0.Format(time.RFC3339Nano)}})}

	mock.ExpectExec(regexp.QuoteMeta(`'recheck_request'`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`'recheck_at'`)).WillReturnRows(sqlmock.NewRows([]string{"at"}).AddRow("2026-10-22T11:15:20Z"))
	mock.ExpectExec(regexp.QuoteMeta(`data->'followup' || $2::jsonb`)).WithArgs("run-1", `{"hold_from":"2026-10-22T11:00:20Z","hold_until":"2026-10-22T11:15:20Z"}`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	assert.True(t, settleByVerdict(db, run, slog.Default()), "the first look holds for the re-check, no model turn")
	require.NoError(t, mock.ExpectationsWereMet())

	swap(t, &verdictNow, func() time.Time { return verdictT0.Add(15*time.Minute + 20*time.Second) })
	run.Data = heldData(t, nil)
	record := map[string]any{}
	expectNoPlan(mock)
	mock.ExpectExec(regexp.QuoteMeta(`'done_at', now()`)).WithArgs("run-1", mergedInto{t, record}).WillReturnResult(sqlmock.NewResult(0, 1))
	assert.True(t, settleByVerdict(db, run, slog.Default()))
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, verdictFixed, record["verdict"])
	assert.Empty(t, *calls)
}

// A replacement pod: the change replaced api-1 by api-2, whose alert (same rule, same workload, its own fingerprint)
// fires once it waited out the rule's for:. At the re-check that firing is still open: the alert fired again, not
// fixed.
func TestSettleByVerdictReplacementPodFiringIsNotFixed(t *testing.T) {
	replacement := firing("api-2", 12*time.Minute, nil)
	replacement.SubjectName = "api-2"
	calls := stubVerdict(t, signalOf(replacement, firing("ev", -time.Hour, ended(time.Minute))))
	swap(t, &verdictNow, func() time.Time { return verdictT0.Add(16 * time.Minute) })
	db, mock := verdictDB(t)
	run := followupRun{Id: "run-1", EventId: "ev", AccountId: "acc", Status: "Success", CreatedAt: verdictT0, Data: heldData(t, nil)}
	record := map[string]any{}
	mock.ExpectExec(regexp.QuoteMeta(`'{followup}'`)).WithArgs("run-1", mergedInto{t, record}).WillReturnResult(sqlmock.NewResult(0, 1))
	assert.False(t, settleByVerdict(db, run, slog.Default()), "the model turn reports it")
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, verdictNotFixed, record["verdict"])
	assert.Equal(t, "fired_again_after_change", record["verdict_reason"])
	assert.Empty(t, *calls)
}

// A real re-fire: the alert fires again at T+3 min, after the settling window, and is still firing at the
// re-check: not_fixed, recorded, and the model turn reports it.
func TestSettleByVerdictRealRefireAfterTheWindow(t *testing.T) {
	calls := stubVerdict(t, signalOf(
		firing("refire", 3*time.Minute, nil),
		firing("ev", -11*time.Minute, ended(7*time.Second)),
	))
	swap(t, &verdictNow, func() time.Time { return verdictT0.Add(5*time.Minute + 20*time.Second) })
	db, mock := verdictDB(t)
	run := followupRun{Id: "run-1", EventId: "ev", AccountId: "acc", Status: "Success", CreatedAt: verdictT0,
		Data: stepData(t, map[string]any{"followup": map[string]any{"claimed_at": verdictT0.Add(5 * time.Minute).Format(time.RFC3339Nano), "rechecks": 1}})}
	mock.ExpectExec(regexp.QuoteMeta(`'{followup}'`)).WithArgs("run-1", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	assert.False(t, settleByVerdict(db, run, slog.Default()))
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Empty(t, *calls, "not resolved")
	assert.Equal(t, verdictResult{Verdict: verdictNotFixed, Reason: "fired_again_after_change"},
		decideVerdict(verdictInput{Status: "Success", RanAt: verdictT0, Rechecks: 1,
			Signal: verdictAlertState(signalOf(firing("refire", 3*time.Minute, nil), firing("ev", -11*time.Minute, ended(7*time.Second))),
				verdictT0, verdictT0.Add(5*time.Minute+20*time.Second), noneGone)}))
}

// A change made after the alert had already cleared waits out the hold too (a flapping alert in a quiet phase is
// not taken for clear), then is suggested as cleared before the change, with no fix credit.
func TestSettleByVerdictClearedBeforeChange(t *testing.T) {
	calls := stubVerdict(t, signalOf(firing("ev", -time.Hour, ended(-time.Minute))))
	db, mock := verdictDB(t)
	run := followupRun{Id: "run-1", EventId: "ev", AccountId: "acc", Status: "Success", CreatedAt: verdictT0, Data: stepData(t, nil)}
	mock.ExpectExec(regexp.QuoteMeta(`'recheck_request'`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`'recheck_at'`)).WillReturnRows(sqlmock.NewRows([]string{"at"}).AddRow("2026-10-22T11:21:00Z"))
	mock.ExpectExec(regexp.QuoteMeta(`data->'followup' || $2::jsonb`)).WillReturnResult(sqlmock.NewResult(0, 1))
	assert.True(t, settleByVerdict(db, run, slog.Default()), "held first, no suggestion yet")
	require.NoError(t, mock.ExpectationsWereMet())

	swap(t, &verdictNow, func() time.Time { return verdictT0.Add(16 * time.Minute) })
	run.Data = heldData(t, nil)
	record := map[string]any{}
	expectNoPlan(mock)
	mock.ExpectExec(regexp.QuoteMeta(`'done_at', now()`)).WithArgs("run-1", mergedInto{t, record}).WillReturnResult(sqlmock.NewResult(0, 1))
	assert.True(t, settleByVerdict(db, run, slog.Default()))
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, followupLikelyFixed, record["outcome"])
	assert.Equal(t, verdictClearedBeforeChange, record["verdict"])
	assert.Empty(t, *calls)
}

// A write approved in the alert's chat that is no step of the plan stays recorded, but it is not the attempt the
// verdict is about: its follow-up ends unjudged, with no verdict and no cause key, whatever the alert did.
func TestSettleByVerdictChatWriteOutsideThePlanIsNotJudged(t *testing.T) {
	for _, status := range []string{"Success", "Failed"} {
		t.Run(status, func(t *testing.T) {
			calls := stubVerdict(t, signalOf(firing("ev", -time.Hour, ended(2*time.Minute))))
			swap(t, &readAlertSignal, func(*sqlx.DB, followupRun) (events.AlertSignal, error) {
				t.Fatal("the alert is not read for a write outside the plan")
				return events.AlertSignal{}, nil
			})
			swap(t, &snapshotCauseKey, func(*sqlx.DB, followupRun, verdictRun, *slog.Logger) {
				t.Fatal("a write outside the plan never gets the plan's cause key")
			})
			db, mock := verdictDB(t)
			run := followupRun{Id: "run-1", EventId: "ev", AccountId: "acc", Status: status, CreatedAt: verdictT0, Data: c1Data(t, nil)}
			record := map[string]any{}
			mock.ExpectExec(regexp.QuoteMeta(`'done_at', now()`)).WithArgs("run-1", mergedInto{t, record}).WillReturnResult(sqlmock.NewResult(0, 1))
			assert.True(t, settleByVerdict(db, run, slog.Default()), "no model turn")
			require.NoError(t, mock.ExpectationsWereMet())
			assert.Equal(t, followupSettled, record["outcome"])
			assert.Equal(t, followupNotAPlanStep, record["reason"])
			assert.NotContains(t, record, "verdict")
			assert.NotContains(t, record, "cause_key")
			assert.Empty(t, *calls)
		})
	}
}

// A failed plan step gets the model turn, as before the loop, and is never resolved.
func TestSettleByVerdictFailedRunsNeverResolve(t *testing.T) {
	calls := stubVerdict(t, signalOf(firing("ev", -time.Hour, ended(2*time.Minute))))
	db, mock := verdictDB(t)
	step := followupRun{Id: "run-2", EventId: "ev", AccountId: "acc", Status: "Failed", CreatedAt: verdictT0, Data: []byte(`{"step":"0:execute","slot":"execute"}`)}
	mock.ExpectExec(regexp.QuoteMeta(`'{followup}'`)).WillReturnResult(sqlmock.NewResult(0, 1))
	assert.False(t, settleByVerdict(db, step, slog.Default()), "the model turn checks a failed plan step")
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Empty(t, *calls)
}

// An alert still firing right after a plan step: the model turn runs, as before.
func TestSettleByVerdictTooEarly(t *testing.T) {
	stubVerdict(t, signalOf(firing("ev", -time.Hour, nil)))
	db, mock := verdictDB(t)
	step := followupRun{Id: "run-1", EventId: "ev", AccountId: "acc", Status: "Success", CreatedAt: verdictT0, Data: []byte(`{"step":"0:execute"}`)}
	mock.ExpectExec(regexp.QuoteMeta(`'{followup}'`)).WillReturnResult(sqlmock.NewResult(0, 1))
	assert.False(t, settleByVerdict(db, step, slog.Default()))
	require.NoError(t, mock.ExpectationsWereMet())
}

// The source still says FIRING: the alert is not clear, whatever the cluster shows (a pod gone and a healthy
// rollout is not a clear), so at the re-check it is not fixed.
func TestSettleByVerdictFiringAtTheSourceIsNotClear(t *testing.T) {
	calls := stubVerdict(t, signalOf(firing("ev", -time.Hour, nil)))
	swap(t, &firingSubjectGone, func(followupRun, events.AlertFiring) bool { return true })
	db, mock := verdictDB(t)
	run := followupRun{Id: "run-1", EventId: "ev", AccountId: "acc", Status: "Success", CreatedAt: verdictT0, Data: heldData(t, nil)}
	record := map[string]any{}
	mock.ExpectExec(regexp.QuoteMeta(`'{followup}'`)).WithArgs("run-1", mergedInto{t, record}).WillReturnResult(sqlmock.NewResult(0, 1))
	assert.False(t, settleByVerdict(db, run, slog.Default()))
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, verdictNotFixed, record["verdict"])
	assert.Equal(t, "still_firing", record["verdict_reason"])
	assert.Empty(t, *calls)
}

// A mute the person picked suppresses the event, chosen by them; it is not a fix.
func TestSettleByVerdictPickedMute(t *testing.T) {
	calls := stubVerdict(t, signalOf(firing("ev", -time.Hour, nil)))
	db, mock := verdictDB(t)
	run := followupRun{Id: "run-1", EventId: "ev", AccountId: "acc", Status: "Success", CreatedAt: verdictT0,
		Data: c1Data(t, map[string]any{"tool": "mute", "command": "mute 4h", "option": 2, "plan_version": "v1", "approved_by": "user-7"})}
	mock.ExpectExec(regexp.QuoteMeta(`'done_at', now()`)).WillReturnResult(sqlmock.NewResult(0, 1))
	assert.True(t, settleByVerdict(db, run, slog.Default()))
	require.NoError(t, mock.ExpectationsWereMet())
	require.Len(t, *calls, 1)
	assert.Equal(t, map[string]any{"account_id": "acc", "event_id": "ev", "nb_status": "SUPPRESSED", "chosen_by": "user-7"}, (*calls)[0])
}

// Once code looked at a claim and left it to the model, later ticks go straight to the turn.
func TestSettleByVerdictOncePerClaim(t *testing.T) {
	calls := stubVerdict(t, events.AlertSignal{})
	swap(t, &readAlertSignal, func(*sqlx.DB, followupRun) (events.AlertSignal, error) {
		t.Fatal("the alert is not read again within one claim")
		return events.AlertSignal{}, nil
	})
	db, _ := verdictDB(t)
	run := followupRun{Id: "run-1", EventId: "ev", AccountId: "acc", Status: "Success", CreatedAt: verdictT0,
		Data: []byte(`{"followup":{"claimed_at":"2026-10-22T11:00:00Z","verdict_checked_at":"2026-10-22T11:00:30Z"}}`)}
	assert.False(t, settleByVerdict(db, run, slog.Default()))
	assert.Empty(t, *calls)
}

// A duplicate's remediation read names its main event (C7 primary_event_id); a main event names none.
func TestRemediationGetBody_DuplicateNamesItsMainEvent(t *testing.T) {
	stubLoopOn(t, true)
	swap(t, &eventFingerprintOf, func(*security.RequestContext, string, string) string { return "fp" })
	swap(t, &primaryEventId, func(_ *security.RequestContext, accountId, eventId string) string {
		if eventId == "dup" {
			return "main"
		}
		return ""
	})
	resp, ok := remediationGetBody(nil, "acct", "dup", &events.EventInfo{Fingerprint: "fp"}, "main", &events.EventInfo{Fingerprint: "fp"}, RemediationPlan{RootCause: "x"}).(remediationGetResponse)
	require.True(t, ok)
	assert.Equal(t, "main", resp.Loop.PrimaryEventId)
	resp, ok = remediationGetBody(nil, "acct", "main", &events.EventInfo{Fingerprint: "fp"}, "main", &events.EventInfo{Fingerprint: "fp"}, RemediationPlan{RootCause: "x"}).(remediationGetResponse)
	require.True(t, ok)
	assert.Empty(t, resp.Loop.PrimaryEventId)
}

func TestVerdictEnd(t *testing.T) {
	end := verdictEnd(followupLikelyFixed, verdictResult{Verdict: verdictFixed, Reason: "cleared_after_change_and_held"})
	assert.Equal(t, followupLikelyFixed, end["outcome"])
	assert.Equal(t, verdictFixed, end["verdict"])
	assert.Equal(t, "cleared_after_change_and_held", end["verdict_reason"])
	assert.NotEmpty(t, end["verdict_at"])
	assert.Equal(t, "**Status:** likely fixed, based on the alert's own firings: the alert cleared after the change and was still clear at the re-check.\n"+
		"**Next:** a person confirms it and resolves the event on the Remediation tab.", end["summary"])
	cleared := verdictEnd(followupLikelyFixed, verdictResult{Verdict: verdictClearedBeforeChange, Reason: "cleared_before_change"})
	assert.Contains(t, cleared["summary"], "**Status:** cleared before the change, based on the alert's own firings")
	assert.NotContains(t, verdictEnd(followupNeedsHuman, verdictResult{Verdict: verdictFixed, Reason: "cleared_after_change_and_held"}), "summary",
		"the thread's own text for a person's decision stands")
}

// With the loop off a duplicate reads its own plan, as before.
func TestPlanEventOf_LoopOffKeepsTheEvent(t *testing.T) {
	stubLoopOn(t, false)
	swap(t, &primaryEventId, func(*security.RequestContext, string, string) string {
		t.Fatal("not looked up with the loop off")
		return ""
	})
	info := &events.EventInfo{ID: "dup"}
	id, got := planEventAndInfo(nil, nil, "acct", "dup", info)
	assert.Equal(t, "dup", id)
	assert.Same(t, info, got)
}

// The suggestion carries the plan's cause key (C5/C8), which the person's resolve puts on the event. A plan that
// cannot be read holds the suggestion for the next tick (a resolve must not carry cause_key null because the read
// failed); a plan with no comparable key is suggested with null; a plan with one, with it.
func TestSuggestByVerdictCauseKey(t *testing.T) {
	heldRun := func(t *testing.T) followupRun {
		return followupRun{Id: "run-1", EventId: "ev", AccountId: "acc", Status: "Success", CreatedAt: verdictT0, Data: heldData(t, nil)}
	}
	signal := signalOf(firing("ev", -time.Hour, ended(2*time.Minute)))
	now := func(t *testing.T) { swap(t, &verdictNow, func() time.Time { return verdictT0.Add(16 * time.Minute) }) }

	t.Run("plan not read: no suggestion this tick", func(t *testing.T) {
		stubVerdict(t, signal)
		now(t)
		db, mock := verdictDB(t)
		mock.ExpectQuery(regexp.QuoteMeta(`AS main) p`)).WillReturnError(errors.New("operator does not exist: text = uuid"))
		assert.True(t, settleByVerdict(db, heldRun(t), slog.Default()), "no model turn either")
		require.NoError(t, mock.ExpectationsWereMet(), "nothing finishes the follow-up")
	})

	t.Run("plan not read past the give-up: a person decides", func(t *testing.T) {
		stubVerdict(t, signal)
		now(t)
		db, mock := verdictDB(t)
		run := heldRun(t)
		run.ClaimedAt = verdictT0.Add(-3 * time.Hour).Format(time.RFC3339Nano)
		mock.ExpectQuery(regexp.QuoteMeta(`AS main) p`)).WillReturnError(errors.New("connection reset"))
		record := map[string]any{}
		mock.ExpectExec(regexp.QuoteMeta(`'done_at', now()`)).WithArgs("run-1", mergedInto{t, record}).WillReturnResult(sqlmock.NewResult(0, 1))
		assert.True(t, settleByVerdict(db, run, slog.Default()))
		require.NoError(t, mock.ExpectationsWereMet())
		assert.Equal(t, followupNeedsHuman, record["outcome"])
		assert.Equal(t, "cause_key_unread", record["reason"])
	})

	for _, tc := range []struct {
		name string
		plan string
		want any
	}{
		{"no comparable key: suggested with null", `{"root_cause":"bad tag","cause_key":{"what":"deploy api","how":"","against":"tag"}}`, nil},
		{"no key at all: suggested with null", `{"root_cause":"bad tag"}`, nil},
		{"a key: suggested with it", `{"cause_key":{"what":"deploy demo app","how":"image pull backoff","against":"non existent image tag"}}`,
			map[string]any{"what": "deploy demo app", "how": "image pull", "against": "non existent image tag"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := stubVerdict(t, signal)
			now(t)
			db, mock := verdictDB(t)
			mock.ExpectQuery(regexp.QuoteMeta(`AS main) p`)).WillReturnRows(sqlmock.NewRows([]string{"plan_event"}).AddRow("ev"))
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT a.analysis FROM event_log_analysis a`)).WillReturnRows(sqlmock.NewRows([]string{"analysis"}).AddRow(tc.plan))
			record := map[string]any{}
			mock.ExpectExec(regexp.QuoteMeta(`'done_at', now()`)).WithArgs("run-1", mergedInto{t, record}).WillReturnResult(sqlmock.NewResult(0, 1))
			assert.True(t, settleByVerdict(db, heldRun(t), slog.Default()))
			require.NoError(t, mock.ExpectationsWereMet())
			assert.Equal(t, followupLikelyFixed, record["outcome"])
			assert.Equal(t, true, record["cause_key_read"])
			assert.Equal(t, tc.want, record["cause_key"])
			assert.Empty(t, *calls)
		})
	}
}

// A re-check the model asked for (schedule_recheck) is not a hold. The
// alert first seen clear there starts a hold, scheduled even at the model's last re-check, and is not fixed yet.
func TestSettleByVerdictModelRecheckStartsAHold(t *testing.T) {
	calls := stubVerdict(t, signalOf(firing("ev", -time.Hour, ended(8*time.Minute))))
	swap(t, &verdictNow, func() time.Time { return verdictT0.Add(10 * time.Minute) })
	db, mock := verdictDB(t)
	run := followupRun{Id: "run-1", EventId: "ev", AccountId: "acc", Status: "Success", CreatedAt: verdictT0,
		Data: []byte(`{"step":"0:execute","followup":{"claimed_at":"2026-10-22T11:10:00Z","rechecks":2,"recheck_reason":"pods still starting"}}`)}
	mock.ExpectExec(regexp.QuoteMeta(`'recheck_request'`)).WithArgs("run-1", 15, soakReason, followupMaxRechecks+1).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`'recheck_at'`)).WillReturnRows(sqlmock.NewRows([]string{"at"}).AddRow("2026-10-22T11:25:00Z"))
	mock.ExpectExec(regexp.QuoteMeta(`data->'followup' || $2::jsonb`)).WithArgs("run-1", `{"hold_from":"2026-10-22T11:10:00Z","hold_until":"2026-10-22T11:25:00Z"}`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	assert.True(t, settleByVerdict(db, run, slog.Default()), "the hold is code's: no model turn")
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Empty(t, *calls, "not resolved before the hold ends")
}

// The resolve label names the cause of the plan the run carried out, as
// recorded at the follow-up's first look, not of a plan written while the verdict waited.
func TestCauseKeyOfTheRunsPlan(t *testing.T) {
	executed := `{"cause_key":{"what":"deploy api","how":"OOMKilled","against":"memory limit"}}`
	replanned := `{"cause_key":{"what":"deploy api","how":"crashloop","against":"bad config"}}`
	expectPlan := func(mock sqlmock.Sqlmock, text string) {
		mock.ExpectQuery(regexp.QuoteMeta(`AS main) p`)).WillReturnRows(sqlmock.NewRows([]string{"plan_event"}).AddRow("ev"))
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT a.analysis FROM event_log_analysis a`)).WillReturnRows(sqlmock.NewRows([]string{"analysis"}).AddRow(text))
	}

	t.Run("first look records the executed plan's key and version", func(t *testing.T) {
		db, mock := verdictDB(t)
		record := map[string]any{}
		expectPlan(mock, executed)
		mock.ExpectExec(regexp.QuoteMeta(`data->'followup' || $2::jsonb`)).WithArgs("run-1", mergedInto{t, record}).WillReturnResult(sqlmock.NewResult(0, 1))
		run := followupRun{Id: "run-1", EventId: "ev", AccountId: "acc", Data: []byte(`{"plan_version":"` + events.PlanVersion(executed) + `","followup":{}}`)}
		snapshotCauseKey(db, run, verdictFactsOf(run.Data), slog.Default())
		require.NoError(t, mock.ExpectationsWereMet())
		assert.Equal(t, true, record["cause_key_read"])
		assert.Equal(t, events.PlanVersion(executed), record["cause_key_plan_version"])
		assert.Equal(t, map[string]any{"what": "deploy api", "how": "oom", "against": "memory limit"}, record["cause_key"])
	})

	t.Run("after a replan the recorded key stands", func(t *testing.T) {
		db, mock := verdictDB(t) // no query: the plan stored now (replanned) is not read
		run := followupRun{Id: "run-1", EventId: "ev", AccountId: "acc",
			Data: []byte(`{"followup":{"cause_key_read":true,"cause_key":{"what":"deploy api","how":"oom","against":"memory limit"}}}`)}
		key, err := planCauseKeyOf(db, run)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
		assert.Equal(t, &events.CauseKey{What: "deploy api", How: "oom", Against: "memory limit"}, key, "not the replanned crashloop cause")
	})

	t.Run("recorded as no comparable key: resolved with null, on purpose", func(t *testing.T) {
		db, mock := verdictDB(t)
		run := followupRun{Id: "run-1", EventId: "ev", AccountId: "acc", Data: []byte(`{"followup":{"cause_key_read":true,"cause_key":null}}`)}
		key, err := planCauseKeyOf(db, run)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
		assert.Nil(t, key)
	})

	t.Run("plan already replaced at the first look: the newest, with its version", func(t *testing.T) {
		db, mock := verdictDB(t)
		record := map[string]any{}
		expectPlan(mock, replanned)
		mock.ExpectExec(regexp.QuoteMeta(`data->'followup' || $2::jsonb`)).WithArgs("run-1", mergedInto{t, record}).WillReturnResult(sqlmock.NewResult(0, 1))
		run := followupRun{Id: "run-1", EventId: "ev", AccountId: "acc", Data: []byte(`{"plan_version":"` + events.PlanVersion(executed) + `","followup":{}}`)}
		snapshotCauseKey(db, run, verdictFactsOf(run.Data), slog.Default())
		require.NoError(t, mock.ExpectationsWereMet())
		assert.Equal(t, events.PlanVersion(replanned), record["cause_key_plan_version"])
	})

	t.Run("not recorded yet: the plan is read now", func(t *testing.T) {
		db, mock := verdictDB(t)
		expectPlan(mock, executed)
		key, err := planCauseKeyOf(db, followupRun{Id: "run-1", EventId: "ev", AccountId: "acc", Data: []byte(`{}`)})
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
		assert.Equal(t, "oom", key.How)
	})
}

// An expired hold counts only for the clear it was set for. A clear that started after the hold began (the alert
// fired and cleared again since) starts a hold of its own instead of counting as fixed at once.
func TestSettleByVerdictOldHoldDoesNotCountForALaterClear(t *testing.T) {
	calls := stubVerdict(t, signalOf(firing("ev", -time.Hour, ended(30*time.Minute))))
	swap(t, &verdictNow, func() time.Time { return verdictT0.Add(40 * time.Minute) })
	db, mock := verdictDB(t)
	run := followupRun{Id: "run-1", EventId: "ev", AccountId: "acc", Status: "Success", CreatedAt: verdictT0,
		Data: stepData(t, map[string]any{"followup": map[string]any{"claimed_at": verdictT0.Add(35 * time.Minute).Format(time.RFC3339Nano), "rechecks": 2,
			"hold_from": verdictT0.Add(6 * time.Minute).Format(time.RFC3339Nano), "hold_until": verdictT0.Add(21 * time.Minute).Format(time.RFC3339)}})}
	record := map[string]any{}
	mock.ExpectExec(regexp.QuoteMeta(`'recheck_request'`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`'recheck_at'`)).WillReturnRows(sqlmock.NewRows([]string{"at"}).AddRow("2026-10-22T11:55:00Z"))
	mock.ExpectExec(regexp.QuoteMeta(`data->'followup' || $2::jsonb`)).WithArgs("run-1", mergedInto{t, record}).WillReturnResult(sqlmock.NewResult(0, 1))
	assert.True(t, settleByVerdict(db, run, slog.Default()))
	require.NoError(t, mock.ExpectationsWereMet(), "a new hold, not a suggestion")
	assert.Equal(t, map[string]any{"hold_until": "2026-10-22T11:55:00Z", "hold_from": "2026-10-22T11:40:00Z"}, record)
	assert.Empty(t, *calls)

	held := verdictRun{}
	held.Followup.HoldUntil, held.Followup.HoldFrom = "2026-10-22T11:21:00Z", "2026-10-22T11:06:00Z"
	assert.True(t, held.holdOver(verdictT0.Add(22*time.Minute), verdictT0.Add(2*time.Minute)), "clear since before the hold began")
	assert.False(t, held.holdOver(verdictT0.Add(22*time.Minute), verdictT0.Add(10*time.Minute)), "cleared after the hold began")
	assert.False(t, held.holdOver(verdictT0.Add(20*time.Minute), verdictT0.Add(2*time.Minute)), "the hold is not over")
}
