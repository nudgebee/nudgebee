package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// The verdict (#40734, contract C4): before any follow-up model turn, code decides from the run row and the
// alert's own signal whether the change fixed the alert. Four things must hold for "fixed": a step of the plan was
// recorded (a run, a plan step approved in chat, or a step a person marked done; never a rollback, and never a chat
// write outside the plan), the alert's own firings are clear (the source closed or resolved every firing of the
// alert, the replacement pods of its workload included), it cleared after the change, and it was still clear at a
// re-check at least the rule's `for:` later (verdictHold). Neither code nor the model resolves the event: a fixed
// or cleared-before-change verdict is recorded as a suggestion ("Likely fixed"), and a person resolves the event
// from the Remediation tab (api-server events_apply_remediation_resolve). A mute the person picked suppresses it.
//
// Known gap: the verdict reads only the alert's own firings. It does not check that the agent, relay or data
// source is healthy, or that a silence or a scrape gap is not what cleared the alert; the tab says so.

// Verdict values, stored on the run row as data.followup.verdict.
const (
	verdictFixed               = "fixed"
	verdictClearedBeforeChange = "cleared_before_change"
	verdictNotFixed            = "not_fixed"
	verdictUnclear             = "unclear"
)

// How a follow-up ends when code settled it, with no model turn.
const (
	// followupLikelyFixed: the verdict is fixed or cleared_before_change; a person confirms it and resolves.
	followupLikelyFixed = "likely_fixed"
	followupMuted       = "muted"
	// followupSettled: a mute that was not applied, or a chat write that is not a step of the plan (recorded,
	// never judged: it is not the attempt the verdict is about).
	followupSettled = "settled"
)

// followupNotAPlanStep is the reason a chat write outside the plan ends its follow-up unjudged.
const followupNotAPlanStep = "not_a_plan_step"

// Tools of a chat run record (C1) that the verdict treats specially.
const (
	runToolMute = "mute"
)

// verdictRun is what the verdict reads from a run row's data.
type verdictRun struct {
	Source     string `json:"source"`
	Tool       string `json:"tool"`
	Slot       string `json:"slot"`
	Step       string `json:"step"`
	Manual     bool   `json:"manual"`
	Option     any    `json:"option"`
	ApprovedBy string `json:"approved_by"`
	// PlanVersion is the plan version the run carried out (C1 plan_version?, run_step), or "".
	PlanVersion string `json:"plan_version"`
	Followup    struct {
		Rechecks         int    `json:"rechecks"`
		ClaimedAt        string `json:"claimed_at"`
		VerdictCheckedAt string `json:"verdict_checked_at"`
		HoldUntil        string `json:"hold_until"`
		// HoldFrom is when the hold began: only a clear that started by then is the one the hold is for.
		HoldFrom string `json:"hold_from"`
		// CauseKey is the cause key recorded at the first look (snapshotCauseKey); CauseKeyRead says it was
		// recorded, null included.
		CauseKey     *events.CauseKey `json:"cause_key"`
		CauseKeyRead bool             `json:"cause_key_read"`
	} `json:"followup"`
}

func verdictFactsOf(data []byte) verdictRun {
	var f verdictRun
	_ = json.Unmarshal(data, &f)
	return f
}

// rollback reports whether the run undid an earlier step: it never counts as a fix.
func (f verdictRun) rollback() bool {
	return strings.EqualFold(f.Slot, RemediationSlotRollback) || strings.HasSuffix(f.Step, ":"+RemediationSlotRollback)
}

// planStep reports whether the run carries out a step of a plan (run_step, the panel, Slack, or a chat write
// tied to a plan option), as opposed to a chat write made outside any plan.
func (f verdictRun) planStep() bool {
	return f.Source != chatSource || f.Option != nil || f.Step != ""
}

// checkedThisClaim reports whether code already looked at this claim of the follow-up: the claim is re-made
// (claimed_at moves) at every re-check, and between them the verdict stands.
func (f verdictRun) checkedThisClaim() bool {
	if f.Followup.VerdictCheckedAt == "" || f.Followup.ClaimedAt == "" {
		return false
	}
	checked, err1 := time.Parse(time.RFC3339Nano, f.Followup.VerdictCheckedAt)
	claimed, err2 := time.Parse(time.RFC3339Nano, f.Followup.ClaimedAt)
	return err1 == nil && err2 == nil && !checked.Before(claimed)
}

// alertState is the alert's signal as the verdict reads it.
type alertState struct {
	// Known: the source reported a state for at least one firing of the alert.
	Known bool
	// Clear: the source closed or resolved every firing of the alert.
	Clear bool
	// ClearedAt is when it cleared; zero when that is not known.
	ClearedAt time.Time
	// FiredAgain: the alert fired again after the change.
	FiredAgain bool
}

type verdictInput struct {
	Status   string
	Rollback bool
	RanAt    time.Time
	// Rechecks counts the looks after the first, the model's schedule_recheck ones included.
	Rechecks int
	// Held: an earlier look saw the alert clear and set a hold (hold_until), that hold is over, and the alert has
	// been clear since before it began. Only then can a clear alert count as fixed, or as cleared before the change:
	// a re-check the model asked for, at which the alert is first seen clear, starts a hold, it does not end one,
	// and an old hold does not count for a later clear.
	Held   bool
	Signal alertState
}

// verdictResult is the verdict, or none yet (Verdict ""), with Hold set when the alert is clear and only the
// re-check is left before it counts as fixed (or as cleared before the change).
type verdictResult struct {
	Verdict string
	Reason  string
	Hold    bool
}

// decideVerdict is the rule, as a pure function.
func decideVerdict(in verdictInput) verdictResult {
	switch {
	case in.Rollback:
		return verdictResult{Verdict: verdictUnclear, Reason: "rollback_not_a_fix"}
	case in.Status == events.RunStatusFailed:
		return verdictResult{Verdict: verdictNotFixed, Reason: "change_failed"}
	case in.Status != events.RunStatusSuccess:
		return verdictResult{Verdict: verdictUnclear, Reason: "change_outcome_unknown"}
	case !in.Signal.Known:
		return verdictResult{Verdict: verdictUnclear, Reason: "alert_state_unread"}
	case in.Signal.FiredAgain:
		return verdictResult{Verdict: verdictNotFixed, Reason: "fired_again_after_change"}
	case in.Signal.Clear && in.Signal.ClearedAt.IsZero():
		return verdictResult{Verdict: verdictUnclear, Reason: "clear_time_unknown"}
	case in.Signal.Clear && !in.Held:
		// Clear, before or after the change: either way it must still be clear at the hold's re-check, so a
		// flapping alert in a quiet phase, or a replacement pod whose alert waits out its rule's for:, is not
		// taken for a fix.
		if in.Signal.ClearedAt.Before(in.RanAt) {
			return verdictResult{Hold: true, Reason: "cleared_before_change"}
		}
		return verdictResult{Hold: true, Reason: "cleared_after_change"}
	case in.Signal.Clear && in.Signal.ClearedAt.Before(in.RanAt):
		return verdictResult{Verdict: verdictClearedBeforeChange, Reason: "cleared_before_change"}
	case in.Signal.Clear:
		return verdictResult{Verdict: verdictFixed, Reason: "cleared_after_change_and_held"}
	case in.Rechecks >= 1:
		return verdictResult{Verdict: verdictNotFixed, Reason: "still_firing"}
	}
	return verdictResult{Reason: "too_early"}
}

// verdictReasonText is a verdict reason for a person.
var verdictReasonText = map[string]string{
	"rollback_not_a_fix":            "an undo of an earlier step is not a fix",
	"change_failed":                 "the change failed",
	"change_outcome_unknown":        "it is not known how the change ended",
	"alert_state_unread":            "the alert's state could not be read",
	"fired_again_after_change":      "the alert fired again after the change",
	"clear_time_unknown":            "the alert cleared, but the source did not say when",
	"cleared_before_change":         "the alert cleared before the change was made",
	"cleared_after_change_and_held": "the alert cleared after the change and was still clear at the re-check",
	"still_firing":                  "the alert was still firing at the re-check",
}

// verdictNow is the clock the verdict reads. A var so tests can set it.
var verdictNow = func() time.Time { return time.Now().UTC() }

// readAlertSignal reads the alert's signal for run. A var so tests can stub it.
var readAlertSignal = func(db *sqlx.DB, run followupRun) (events.AlertSignal, error) {
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	return events.ReadAlertSignal(ctx, db, run.AccountId, run.EventId)
}

// verdictSettleWindow is how long after the change its own echoes may still fire: a rollout terminating the old
// pod can report it failing once more (e.g. ImagePullBackOff on the old pod 6 s after a
// successful `kubectl rollout undo`, ended a second later). A firing that starts inside the window and has
// ended by the check is the change settling, not the alert firing again.
const verdictSettleWindow = 2 * time.Minute

// firingEnded reports whether the source closed or resolved a firing.
func firingEnded(f events.AlertFiring) bool {
	switch strings.ToUpper(strings.TrimSpace(f.Status)) {
	case "CLOSED", "RESOLVED":
		return true
	}
	return false
}

// verdictAlertState turns the alert's firings into the verdict's view of it, for a change made at ranAt:
//   - Clear: no firing is still open.
//   - FiredAgain: a firing started after the settling window, and is still open, or ended for a subject that still
//     exists (subjectGone reports one that no longer does: an old pod the change replaced). A firing that started
//     inside the window and has ended does not count. Twin rows of one transition count once: only "any" is asked.
//   - ClearedAt: when the incident's own firings (started by the change) ended. A source end time in the future
//     is not an end (some sources stamp one ahead). A point-in-time firing (it ends when it starts, such as a
//     Kubernetes warning) is closed from birth: it counts as cleared at the change.
//   - Known: a firing carries a state from the source; a row with none says nothing about the alert.
func verdictAlertState(s events.AlertSignal, ranAt, now time.Time, subjectGone func(events.AlertFiring) bool) alertState {
	st := alertState{Clear: true}
	for _, f := range s.Firings {
		if strings.TrimSpace(f.Status) != "" {
			st.Known = true
			break
		}
	}
	settled := ranAt.Add(verdictSettleWindow)
	var cleared time.Time
	for _, f := range s.Firings {
		afterChange := f.StartsAt != nil && f.StartsAt.After(ranAt)
		afterSettling := f.StartsAt != nil && f.StartsAt.After(settled)
		switch {
		case !firingEnded(f):
			st.Clear = false
			st.FiredAgain = st.FiredAgain || afterSettling
		case afterSettling:
			if !subjectGone(f) {
				st.FiredAgain = true
			}
		case afterChange:
			// The change settling: ignored.
		case f.EndsAt == nil || f.EndsAt.After(now.Add(time.Minute)):
			// An end not known.
		case f.StartsAt != nil && !f.EndsAt.After(*f.StartsAt):
			cleared = laterOf(cleared, ranAt)
		default:
			cleared = laterOf(cleared, f.EndsAt.UTC())
		}
	}
	if st.Clear {
		st.ClearedAt = cleared
	}
	return st
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// firingSubjectGone reports whether an ended firing's subject no longer exists: a Kubernetes pod the cluster no
// longer has. Anything else, or a read that fails, exists. A var so tests can stub it.
var firingSubjectGone = func(run followupRun, f events.AlertFiring) bool {
	if !strings.EqualFold(f.SubjectType, "pod") || f.SubjectName == "" || f.SubjectNS == "" ||
		!strings.EqualFold(tools.GetCloudProviderForAccount(run.AccountId), "k8s") {
		return false
	}
	ctx, err := systemContextFor(run.AccountId)
	if err != nil {
		return false
	}
	getPod, ok := commandFromArgv([]string{"kubectl", "get", "pod", f.SubjectName, "-n", f.SubjectNS, "-o", "name"})
	if !ok {
		return false
	}
	res, err := readInCluster(ctx, run.AccountId, getPod)
	return err == nil && !res.Success && rolloutCouldNotRun(res)
}

// subjectGoneOnce is firingSubjectGone for run, asked at most once per subject.
func subjectGoneOnce(run followupRun) func(events.AlertFiring) bool {
	seen := map[string]bool{}
	return func(f events.AlertFiring) bool {
		key := f.SubjectType + "\x1f" + f.SubjectNS + "\x1f" + f.SubjectName
		if gone, ok := seen[key]; ok {
			return gone
		}
		seen[key] = firingSubjectGone(run, f)
		return seen[key]
	}
}

// systemContextFor is NudgeBee's own context on accountId's tenant, limited to that account.
func systemContextFor(accountId string) (*security.RequestContext, error) {
	tenant, err := security.GetTenantIdFromAccountId(accountId)
	if err != nil || tenant == "" {
		return nil, fmt.Errorf("tenant of account %s: %w", accountId, errors.Join(err, errors.New("none")))
	}
	return security.NewRequestContextForTenantAccountAdmin(tenant, security.GetSystemUserId(), []string{accountId}), nil
}

// readInCluster runs a strict kubectl read on accountId through the relay, as the outcome check does.
func readInCluster(ctx *security.RequestContext, accountId, command string) (tools.RemediationExecutionResult, error) {
	if reason := checkRejection(ctx, accountId, "k8s", command); reason != "" {
		return tools.RemediationExecutionResult{}, errors.New(reason)
	}
	substrate := tools.RemediationSubstrateFor("k8s", command)
	nbTool, found := toolcore.GetNBTool(accountId, substrate.RelayTool)
	if substrate.RelayTool == "" || !found {
		return tools.RemediationExecutionResult{}, errors.New("no kubectl executor for this account")
	}
	sent := sendRemediationCommand(ctx, accountId, security.GetSystemUserId(), substrate, nbTool, command)
	if sent.Err != nil && !sent.ExitCodeReported {
		return sent.RemediationExecutionResult, sent.Err
	}
	return sent.RemediationExecutionResult, nil
}

// settleByVerdict applies the verdict to run's follow-up before any model turn, and reports whether it took
// the follow-up over (it ended it, or scheduled the next look); false means a model turn should check on it.
func settleByVerdict(db *sqlx.DB, run followupRun, logger *slog.Logger) bool {
	facts := verdictFactsOf(run.Data)
	if facts.checkedThisClaim() {
		return false
	}
	if strings.EqualFold(facts.Tool, runToolMute) {
		return settleMute(db, run, facts, logger)
	}
	if !facts.planStep() {
		// A write approved in the alert's chat that is no step of the plan stays recorded, but it is not the
		// attempt the verdict is about: no verdict, no cause key, no model turn.
		finishFollowup(db, run, map[string]any{"outcome": followupSettled, "reason": followupNotAPlanStep,
			"summary": "**Status:** recorded. A change made in the chat outside the plan is not checked."}, logger)
		return true
	}
	snapshotCauseKey(db, run, facts, logger)
	in := verdictInput{Status: run.Status, Rollback: facts.rollback(), RanAt: run.CreatedAt.UTC(), Rechecks: facts.Followup.Rechecks}
	hold := verdictHoldUnknownFor
	if !in.Rollback && in.Status == events.RunStatusSuccess {
		signal, err := readAlertSignal(db, run)
		if err != nil {
			logger.Warn("remediation_followups: alert state not read", "error", err)
		} else {
			in.Signal = verdictAlertState(signal, in.RanAt, verdictNow(), subjectGoneOnce(run))
			hold = verdictHold(signal)
		}
	}
	in.Held = facts.holdOver(verdictNow(), in.Signal.ClearedAt)
	v := decideVerdict(in)
	logger.Info("remediation_followups: verdict", "verdict", v.Verdict, "reason", v.Reason, "hold", v.Hold, "hold_minutes", int(hold.Minutes()))
	switch {
	case v.Verdict == verdictFixed || v.Verdict == verdictClearedBeforeChange:
		return suggestByVerdict(db, run, v, logger)
	case v.Hold:
		// Clear: look again once the hold is over, with no model turn.
		if at, err := requestHoldRecheck(db, run, hold); err == nil && at != "" {
			logger.Info("remediation_followups: holding for the re-check", "recheck_at", at)
			recordHoldUntil(db, run, at, logger)
			return true
		}
		markVerdictChecked(db, run, verdictResult{}, logger)
		return false
	}
	markVerdictChecked(db, run, v, logger)
	return false
}

// verdictHoldUnknownFor is the hold when the alert's rule `for:` is not known: 15 minutes, the `for:` of the
// common Kubernetes pod alerts (kube-prometheus KubePodCrashLooping, KubePodNotReady,
// KubeDeploymentReplicasMismatch). A replacement pod's alert only fires once it waited that out, so a shorter hold
// would see the alert clear while the new pod is failing the same way.
const verdictHoldUnknownFor = 15 * time.Minute

// verdictHoldMax bounds the hold for a rule with a very long `for:`.
const verdictHoldMax = 2 * time.Hour

// verdictHold is how long the alert must stay clear before it counts: its rule's `for:` plus the settling window
// when the rule is known (at least recheckMinMinutes), else verdictHoldUnknownFor.
func verdictHold(s events.AlertSignal) time.Duration {
	if !s.RuleForKnown {
		return verdictHoldUnknownFor
	}
	hold := time.Duration(s.RuleForSeconds)*time.Second + verdictSettleWindow
	return min(max(hold, time.Duration(recheckMinMinutes)*time.Minute), verdictHoldMax)
}

// verdictFields is the C4 record of v.
func verdictFields(v verdictResult) map[string]any {
	if v.Verdict == "" {
		return map[string]any{}
	}
	return map[string]any{"verdict": v.Verdict, "verdict_at": verdictNow().Format(time.RFC3339Nano), "verdict_reason": v.Reason}
}

// verdictEnd is how a follow-up ends when code settled it: outcome, the verdict, and a summary for the thread.
func verdictEnd(outcome string, v verdictResult) map[string]any {
	end := verdictFields(v)
	end["outcome"] = outcome
	status := verdictStatusText[outcome]
	if outcome == followupLikelyFixed && v.Verdict == verdictClearedBeforeChange {
		status = "cleared before the change, based on the alert's own firings"
	}
	if text := verdictReasonText[v.Reason]; text != "" && status != "" {
		end["summary"] = "**Status:** " + status + ": " + text + "."
		if next := verdictNextText[outcome]; next != "" {
			end["summary"] = end["summary"].(string) + "\n**Next:** " + next
		}
	}
	return end
}

var verdictStatusText = map[string]string{
	followupLikelyFixed: "likely fixed, based on the alert's own firings",
}

// verdictNextText is what a person does next, after the status line.
var verdictNextText = map[string]string{
	followupLikelyFixed: "a person confirms it and resolves the event on the Remediation tab.",
}

// holdOver reports whether a hold was set (hold_until) and has ended by now, for the clear that started at
// clearedAt: a clear that started after the hold began (the alert fired and cleared again) is not the one it held.
func (f verdictRun) holdOver(now, clearedAt time.Time) bool {
	until, err := time.Parse(time.RFC3339Nano, f.Followup.HoldUntil)
	if err != nil || now.Before(until) {
		return false
	}
	from, err := time.Parse(time.RFC3339Nano, f.Followup.HoldFrom)
	return err == nil && !clearedAt.IsZero() && !clearedAt.After(from)
}

// requestHoldRecheck schedules the next look once the hold is over. A hold may take one look beyond the model's
// re-checks, so a clear first seen at the model's last re-check still gets its hold instead of ending with no
// verdict.
func requestHoldRecheck(db *sqlx.DB, run followupRun, hold time.Duration) (string, error) {
	minutes := int((hold + time.Minute - 1) / time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	if _, err := events.RequestFollowupRecheck(ctx, db, run.Id, minutes, soakReason, followupMaxRechecks+1); err != nil {
		return "", err
	}
	return events.ScheduleFollowupRecheck(ctx, db, run.Id, "", minutes, minutes, followupMaxRechecks+1)
}

// recordHoldUntil records data.followup.hold_until (RFC3339, UTC): the alert is clear, and the fix counts once it
// is still clear at the re-check due then ("Verifying: the goal must hold until HH:MM"). It is
// left in place when the verdict lands, so the record keeps the time the fix had to hold until.
func recordHoldUntil(db *sqlx.DB, run followupRun, recheckAt string, logger *slog.Logger) {
	until, err := time.Parse(time.RFC3339Nano, recheckAt)
	if err != nil {
		logger.Warn("remediation_followups: re-check time not read", "recheck_at", recheckAt, "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	body, _ := json.Marshal(map[string]any{"hold_until": until.UTC().Format(time.RFC3339), "hold_from": verdictNow().UTC().Format(time.RFC3339Nano)})
	if _, err := events.SetFollowupVerdict(ctx, db, run.Id, string(body)); err != nil {
		logger.Error("remediation_followups: hold time not recorded", "error", err)
	}
}

// markVerdictChecked records that code looked at this claim, and the verdict when there is one.
func markVerdictChecked(db *sqlx.DB, run followupRun, v verdictResult, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	patch := verdictFields(v)
	patch["verdict_checked_at"] = verdictNow().Format(time.RFC3339Nano)
	body, _ := json.Marshal(patch)
	if _, err := events.SetFollowupVerdict(ctx, db, run.Id, string(body)); err != nil {
		logger.Error("remediation_followups: verdict not recorded", "error", err)
	}
}

// suggestByVerdict ends the follow-up with the verdict as a suggestion a person confirms ("Likely fixed"), with
// the plan's cause key, which the person's resolve puts on the event (nb_resolved_by) so a re-fire can be compared.
// The event is not resolved here. A cause key that cannot be read leaves the follow-up open, and the next tick
// tries again; after the give-up time a person decides.
func suggestByVerdict(db *sqlx.DB, run followupRun, v verdictResult, logger *slog.Logger) bool {
	causeKey, err := planCauseKeyOf(db, run)
	if err != nil {
		// A resolve without the plan's cause key would make every re-fire "fired again", never "fix did not hold":
		// wait for a read that works.
		logger.Error("remediation_followups: plan cause key not read, suggestion waits", "error", err, "verdict", v.Verdict)
		if claimedLongAgo(run, followupGiveUp) {
			end := verdictEnd(followupNeedsHuman, v)
			end["reason"] = "cause_key_unread"
			finishFollowup(db, run, end, logger)
		}
		return true
	}
	end := verdictEnd(followupLikelyFixed, v)
	end["cause_key"], end["cause_key_read"] = causeKey, true
	finishFollowup(db, run, end, logger)
	return true
}

// settleMute suppresses the event for a mute the person picked as the plan's option (C1 tool "mute"), and ends
// the follow-up: there is no fix to check.
func settleMute(db *sqlx.DB, run followupRun, facts verdictRun, logger *slog.Logger) bool {
	if run.Status != events.RunStatusSuccess {
		finishFollowup(db, run, map[string]any{"outcome": followupSettled, "reason": "mute_not_applied"}, logger)
		return true
	}
	err := applyRemediationVerdict(run.AccountId, map[string]any{
		"account_id": run.AccountId,
		"event_id":   run.EventId,
		"nb_status":  "SUPPRESSED",
		"chosen_by":  facts.ApprovedBy,
	})
	if err != nil {
		logger.Error("remediation_followups: event not suppressed", "error", err)
		if claimedLongAgo(run, followupGiveUp) {
			finishFollowup(db, run, map[string]any{"outcome": followupNeedsHuman, "reason": "suppress_failed"}, logger)
		}
		return true
	}
	finishFollowup(db, run, map[string]any{"outcome": followupMuted}, logger)
	return true
}

// Internal api-server actions (C5, C6): service-only, NudgeBee's system identity. A RESOLVED is never asked for
// from here: a person resolves through events_apply_remediation_resolve.
const (
	actionApplyRemediationVerdict = "events_apply_remediation_verdict"
	actionUpdateRefireLabel       = "events_update_refire_label"
)

// applyRemediationVerdict asks api-server to suppress an event for a mute the person picked. A var so tests can
// stub it.
var applyRemediationVerdict = func(accountId string, input map[string]any) error {
	return callTriageSystemAction(accountId, actionApplyRemediationVerdict, input)
}

// updateRefireLabel asks api-server to relabel a re-fire. A var so tests can stub it.
var updateRefireLabel = func(accountId string, input map[string]any) error {
	return callTriageSystemAction(accountId, actionUpdateRefireLabel, input)
}

func callTriageSystemAction(accountId, action string, input map[string]any) error {
	tenant, err := security.GetTenantIdFromAccountId(accountId)
	if err != nil || tenant == "" {
		return fmt.Errorf("%s: tenant of account %s: %w", action, accountId, errors.Join(err, errors.New("none")))
	}
	_, err = tools.DoApiServerSystemAction(tenant, "/rpc/triage", action, input)
	return err
}

// planCauseKeyOf is the normalized cause key (C8) of the plan the run carried out, for the resolve label:
//   - the key recorded on the run row at the follow-up's first look (snapshotCauseKey), so a replan while the
//     verdict waits (the soak, the model's re-checks) does not change it;
//   - else the plan stored now (events.NewestPlanOfEvent).
//
// nil with no error means the plan names no comparable cause (no cause_key, or a field empty): the event is then
// resolved with cause_key null on purpose, and a later re-fire can only read "fired again", never "fix did not
// hold". An error means the plan could not be read; the resolve waits for it.
func planCauseKeyOf(db *sqlx.DB, run followupRun) (*events.CauseKey, error) {
	if snap := verdictFactsOf(run.Data).Followup; snap.CauseKeyRead {
		return snap.CauseKey, nil
	}
	_, key, err := currentPlanCauseKey(db, run)
	return key, err
}

// currentPlanCauseKey is the version and normalized cause key of the plan the run's event (or its live main event)
// holds now; "" and nil without a plan.
func currentPlanCauseKey(db *sqlx.DB, run followupRun) (string, *events.CauseKey, error) {
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	text, err := events.NewestPlanOfEvent(ctx, db, run.AccountId, run.EventId)
	if err != nil {
		return "", nil, fmt.Errorf("plan cause key: %w", err)
	}
	if text == "" {
		return "", nil, nil
	}
	return events.PlanVersion(text), causeKeyOfPlan(text), nil
}

// snapshotCauseKey records on the run row, at the follow-up's first look, the cause key of the plan the run
// carried out: the stored plan while it is still the version the run names (data.plan_version), else (a run
// with no plan version, or the plan already replaced) the newest, with the version it came from. Stored plans
// keep no history, so this is the last moment the run's own plan can be read. A read that fails is retried at
// the next look; until then planCauseKeyOf reads the plan live. A var so tests can stub it.
var snapshotCauseKey = func(db *sqlx.DB, run followupRun, facts verdictRun, logger *slog.Logger) {
	if facts.Followup.CauseKeyRead {
		return
	}
	version, key, err := currentPlanCauseKey(db, run)
	if err != nil {
		logger.Warn("remediation_followups: plan cause key not recorded", "error", err)
		return
	}
	if facts.PlanVersion != "" && version != facts.PlanVersion {
		logger.Info("remediation_followups: the run's plan was replaced, recording the newest plan's cause",
			"run_plan_version", facts.PlanVersion, "plan_version", version)
	}
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	body, _ := json.Marshal(map[string]any{"cause_key": key, "cause_key_read": true, "cause_key_plan_version": version})
	if _, err := events.SetFollowupVerdict(ctx, db, run.Id, string(body)); err != nil {
		logger.Warn("remediation_followups: plan cause key not recorded", "error", err)
	}
}

// causeKeyOfPlan is the normalized cause key of a stored plan's text (events.NormalizeCauseKey), or nil when it
// has none or is not comparable (a field is empty).
func causeKeyOfPlan(plan string) *events.CauseKey {
	var p struct {
		CauseKey *events.CauseKey `json:"cause_key"`
	}
	if json.Unmarshal([]byte(plan), &p) != nil || p.CauseKey == nil || !p.CauseKey.Comparable() {
		return nil
	}
	normalized := events.NormalizeCauseKey(*p.CauseKey)
	return &normalized
}

// sameCause is events.SameCause for keys that may be missing: a missing key is never the same.
func sameCause(a, b *events.CauseKey) bool {
	return a != nil && b != nil && events.SameCause(*a, *b)
}

// Re-fire labels (C6).
const (
	refireFixDidNotHold = "fix_did_not_hold"
	refireUnknown       = "unknown"
)

// compareRefires labels a re-fire "fix did not hold" once its own plan names the same cause as the plan of the
// run that resolved the old chain. Triage labels every re-fire within 24 h of such a resolve "unknown" when it
// arrives; only an equal normalized cause key changes that.
// refiresToCompare reads the re-fires to compare. A var so tests can stub it.
var refiresToCompare = func(ctx context.Context, db *sqlx.DB) ([]events.RefireToCompare, error) {
	return events.RefiresToCompare(ctx, db, 50)
}

func compareRefires(db *sqlx.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	due, err := refiresToCompare(ctx, db)
	if err != nil {
		slog.Error("remediation_followups: re-fires not read", "error", err)
		return
	}
	for _, r := range due {
		// Every compared re-fire is marked with the plan it was compared on (compared_plan), the same cause or not, so
		// it is read again only when its plan changes.
		kind := refireUnknown
		if refireSameCause(r) {
			kind = refireFixDidNotHold
		}
		if err := updateRefireLabel(r.AccountId, map[string]any{"account_id": r.AccountId, "event_id": r.EventId,
			"kind": kind, "previous_run_id": r.PreviousRunId, "compared_plan": r.PlanVersion}); err != nil {
			slog.Error("remediation_followups: re-fire not labelled", "error", err, "event_id", r.EventId)
		}
	}
}

// refireSameCause reports whether the re-fire's plan and the resolve label name the same cause.
func refireSameCause(r events.RefireToCompare) bool {
	var resolved struct {
		CauseKey *events.CauseKey `json:"cause_key"`
	}
	if json.Unmarshal([]byte(r.ResolvedBy), &resolved) != nil {
		return false
	}
	return sameCause(causeKeyOfPlan(r.Plan), resolved.CauseKey)
}
