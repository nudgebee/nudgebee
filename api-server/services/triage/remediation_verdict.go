package triage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nudgebee/services/internal/database/models"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// The remediation loop (#40734): llm-server decides in code whether a recorded fix held and records the verdict
// on the run row as a suggestion ("Likely fixed"); a person confirms it and resolves the event
// (ConfirmRemediationResolve, the user action events_apply_remediation_resolve). A mute the person picked as the
// plan's option is applied by llm-server through the service-only action (api/actions_remediation_internal.go),
// as NudgeBee's system identity. api-server is the only writer of events.

// Labels the remediation loop writes on events. Values are JSON objects encoded as label strings: every label
// value on an event is a string.
const (
	// LabelResolvedBy marks an event resolved for the loop: {run_id, verdict, cause_key, confirmed_by,
	// confirmed_by_name}, the person who confirmed the verdict. A duplicate chain breaks at such an event whatever its occurrence number
	// (detectAndRecordDuplicate).
	LabelResolvedBy = "nb_resolved_by"
	// LabelRefire marks the first firing of a chain that started within RefireWindow of a loop resolve of the
	// old chain: {kind, previous_run_id}.
	LabelRefire = "nb_refire"
)

// Re-fire kinds: fix_did_not_hold only when the cause keys of both plans are equal; unknown ("fired again")
// otherwise.
const (
	RefireKindFixDidNotHold = "fix_did_not_hold"
	RefireKindUnknown       = "unknown"
)

// RemediationVerdictRequest asks to resolve or suppress an event for the remediation loop.
type RemediationVerdictRequest struct {
	AccountID string
	EventID   string
	// NBStatus is RESOLVED (a fix held, or the alert cleared before the change) or SUPPRESSED (a picked mute).
	NBStatus string
	// ReasonText goes to the event history.
	ReasonText string
	// ResolvedBy is the nb_resolved_by label for a RESOLVED: run_id, verdict, cause_key.
	ResolvedBy map[string]any
	// ChosenBy is the person who picked the mute (a user id, or a name), for a SUPPRESSED.
	ChosenBy string
	// ChangedBy is the user the event history names for the change; "" is NudgeBee's system identity.
	ChangedBy string
}

// RemediationVerdictResponse lists the events the verdict changed.
type RemediationVerdictResponse struct {
	Events []RemediationVerdictEvent `json:"events"`
}

// RemediationVerdictEvent is one event a verdict applied to.
type RemediationVerdictEvent struct {
	EventID    string `json:"event_id"`
	PrevStatus string `json:"prev_status"`
	NewStatus  string `json:"new_status"`
}

// remediationTarget is an event a verdict applies to, as read before it does.
type remediationTarget struct {
	ID       string `db:"id"`
	NBStatus string `db:"nb_status"`
	Primary  string `db:"primary_id"`
	// Firing: the event's alert is firing now.
	Firing bool `db:"firing"`
}

// ApplyRemediationVerdict resolves (or suppresses) the event and, when it is a duplicate, its main event too: a
// main event and its duplicates move together. Each change goes through UpdateNBStatus, so the event history
// shows it, with the system identity. A DROPPED event is left alone; an event already in the status only gets
// the label. A main event whose alert is still firing is left open (and unlabelled): resolving it with its
// duplicate would close an alert, often of another fingerprint, that has not cleared.
func ApplyRemediationVerdict(ctx context.Context, db *sqlx.DB, req RemediationVerdictRequest) (*RemediationVerdictResponse, error) {
	if req.NBStatus != NBStatusResolved && req.NBStatus != NBStatusSuppressed {
		return nil, fmt.Errorf("remediation verdict: nb_status must be %s or %s", NBStatusResolved, NBStatusSuppressed)
	}
	if runID, _ := req.ResolvedBy["run_id"].(string); req.NBStatus == NBStatusResolved && strings.TrimSpace(runID) == "" {
		return nil, errors.New("remediation verdict: resolved_by.run_id is required")
	}
	event, err := readRemediationTarget(ctx, db, req.AccountID, req.EventID)
	if err != nil {
		return nil, err
	}
	targets := []remediationTarget{event}
	if event.Primary != "" && event.Primary != event.ID {
		main, err := readRemediationTarget(ctx, db, req.AccountID, event.Primary)
		if err != nil {
			return nil, err
		}
		targets = append(targets, main)
	}
	reason := strings.TrimSpace(req.ReasonText)
	label := ""
	if req.NBStatus == NBStatusSuppressed {
		reason = "chosen by " + personName(ctx, db, req.ChosenBy)
	} else {
		raw, err := json.Marshal(req.ResolvedBy)
		if err != nil {
			return nil, fmt.Errorf("remediation verdict: label: %w", err)
		}
		label = string(raw)
	}
	resp := &RemediationVerdictResponse{Events: []RemediationVerdictEvent{}}
	for i, t := range targets {
		if t.NBStatus == NBStatusDropped {
			continue
		}
		if i > 0 && t.Firing && req.NBStatus == NBStatusResolved {
			slog.InfoContext(ctx, "remediation verdict: main event still firing, not resolved with its duplicate", "event_id", req.EventID, "main_event_id", t.ID)
			continue
		}
		// The label first: the chain breaks only at an event that is both labelled and RESOLVED, so a
		// failure between the two writes never breaks a chain at an event that is still open.
		if label != "" {
			if err := setEventLabel(ctx, db, req.AccountID, t.ID, LabelResolvedBy, label); err != nil {
				return nil, err
			}
		}
		if t.NBStatus == req.NBStatus {
			continue
		}
		r := reason
		changedBy := systemUserID
		if strings.TrimSpace(req.ChangedBy) != "" {
			changedBy = req.ChangedBy
		}
		if _, err := UpdateNBStatus(ctx, db, UpdateNBStatusRequest{EventID: t.ID, NBStatus: req.NBStatus, ReasonText: &r}, req.AccountID, changedBy); err != nil {
			return nil, fmt.Errorf("remediation verdict: %w", err)
		}
		resp.Events = append(resp.Events, RemediationVerdictEvent{EventID: t.ID, PrevStatus: t.NBStatus, NewStatus: req.NBStatus})
	}
	return resp, nil
}

// Verdicts llm-server records that a person may confirm (data.followup.verdict of a run row).
const (
	VerdictFixed               = "fixed"
	VerdictClearedBeforeChange = "cleared_before_change"
)

// ErrNoVerdictToConfirm: the run has no likely-fixed verdict for this event, so there is nothing to confirm.
var ErrNoVerdictToConfirm = errors.New("remediation resolve: this run has no likely-fixed verdict for the event")

// ErrFiredAgainAfterVerdict: the alert fired again after the run's check ended, so its "Likely fixed" no longer stands.
// The Remediation tab hides Resolve then ("came back"); the server refuses it too, for every surface (Slack included).
var ErrFiredAgainAfterVerdict = errors.New("remediation resolve: the alert fired again after this fix was checked, so it is not confirmed as fixed; look at the alert again before resolving")

// confirmableRun is a run row as a person's resolve reads it.
type confirmableRun struct {
	EventID  string `db:"event_id"`
	Verdict  string `db:"verdict"`
	CauseKey string `db:"cause_key"`
	// FiredAgain: a firing of the run's alert (its fingerprint, the run's event, or a duplicate of it) started after the
	// run's check ended (data.followup.verdict_at, else done_at) — the rule llm-server's remediation read uses for
	// loop.refire_after_run (loadPlanFacts). Event times are naive UTC, so the RFC 3339 check time is read in UTC
	// whatever the session's time zone.
	FiredAgain bool `db:"fired_again"`
}

// confirmableRunSQL reads a run for a person's resolve ($1 run, $2 account).
const confirmableRunSQL = `SELECT er.event_id::text AS event_id,
		       COALESCE(er.data->'followup'->>'verdict', '') AS verdict,
		       COALESCE(er.data->'followup'->'cause_key', 'null'::jsonb)::text AS cause_key,
		       EXISTS (SELECT 1 FROM events n
		         WHERE n.cloud_account_id = e.cloud_account_id
		           AND (n.id = e.id OR (COALESCE(e.fingerprint, '') <> '' AND n.fingerprint = e.fingerprint)
		             OR (n.labels IS NOT NULL AND n.labels <> '{}'::jsonb AND n.labels @> jsonb_build_object('nb_duplicate_of', e.id::text)))
		           AND COALESCE(n.starts_at, n.created_at) > COALESCE(NULLIF(er.data->'followup'->>'verdict_at', ''), NULLIF(er.data->'followup'->>'done_at', ''))::timestamptz AT TIME ZONE 'UTC') AS fired_again
		  FROM event_resolution er JOIN events e ON e.id = er.event_id
		 WHERE er.id = $1::uuid AND e.cloud_account_id = $2::uuid AND er.type = 'CommandExecution'`

// ConfirmRemediationResolve resolves an event whose run llm-server judged likely fixed (or cleared before the
// change), because a person confirmed it. The label is the verdict's, read from the run row, not from the caller:
// {run_id, verdict, cause_key, confirmed_by, confirmed_by_name}. The run must be on the event or on the main event it follows. The
// history names the person.
func ConfirmRemediationResolve(ctx context.Context, db *sqlx.DB, accountID, eventID, runID, userID string) (*RemediationVerdictResponse, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, errors.New("remediation resolve: a person confirms the verdict")
	}
	if _, err := uuid.Parse(runID); err != nil {
		return nil, ErrNoVerdictToConfirm
	}
	var run confirmableRun
	err := db.GetContext(ctx, &run, confirmableRunSQL, runID, accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoVerdictToConfirm
	}
	if err != nil {
		return nil, fmt.Errorf("remediation resolve: read run: %w", err)
	}
	if run.Verdict != VerdictFixed && run.Verdict != VerdictClearedBeforeChange {
		return nil, ErrNoVerdictToConfirm
	}
	if run.FiredAgain {
		return nil, ErrFiredAgainAfterVerdict
	}
	if run.EventID != eventID {
		target, err := readRemediationTarget(ctx, db, accountID, eventID)
		if err != nil {
			return nil, err
		}
		if target.Primary != run.EventID {
			return nil, ErrNoVerdictToConfirm
		}
	}
	causeKey := json.RawMessage(run.CauseKey)
	if !json.Valid(causeKey) {
		causeKey = json.RawMessage("null")
	}
	why := "likely fixed: the alert cleared after the change and stayed clear"
	if run.Verdict == VerdictClearedBeforeChange {
		why = "the alert cleared before the change"
	}
	name := personName(ctx, db, userID)
	return ApplyRemediationVerdict(ctx, db, RemediationVerdictRequest{
		AccountID:  accountID,
		EventID:    eventID,
		NBStatus:   NBStatusResolved,
		ReasonText: "Resolved by " + name + ", " + why,
		ResolvedBy: map[string]any{"run_id": runID, "verdict": run.Verdict, "cause_key": causeKey, "confirmed_by": userID, "confirmed_by_name": name,
			"confirmed_at": resolveNow().UTC().Format(time.RFC3339)},
		ChangedBy: userID,
	})
}

// resolveNow is the clock a confirmed resolve is stamped with (confirmed_at). A var so tests can set it.
var resolveNow = time.Now

// livePrimaryEventSQL is the main event of e when e is a duplicate of a live one, NULL otherwise. It is the rule the
// remediation read applies (llm-server events.PlanEventOf, remediation_plan_event.go), kept the same here so a
// verdict moves only the main event the read showed: the main is labels.nb_duplicate_of, else the chain's first
// event for a later occurrence triage marked DUPLICATE or SUPPRESSED; it is live when not resolved or suppressed,
// and still firing or worked on (an investigation or a plan of its own). A main with no plan does not take over a
// duplicate that has one.
const livePrimaryEventSQL = `(SELECT m.id FROM events m
	 WHERE m.cloud_account_id = e.cloud_account_id AND m.id <> e.id
	   AND m.id = COALESCE(
	       CASE WHEN e.nb_status = 'DUPLICATE' AND jsonb_typeof(e.labels) = 'object'
	                 AND e.labels->>'nb_duplicate_of' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
	            THEN (e.labels->>'nb_duplicate_of')::uuid END,
	       (SELECT d.first_event_id FROM event_duplicates d
	         WHERE d.event_id = e.id AND d.cloud_account_id = e.cloud_account_id AND d.occurrence_number > 1
	           AND e.nb_status IN ('DUPLICATE', 'SUPPRESSED') LIMIT 1))
	   AND COALESCE(m.nb_status, '') NOT IN ('RESOLVED', 'SUPPRESSED')
	   AND (COALESCE(m.status, '') = 'FIRING'
	     OR EXISTS (SELECT 1 FROM event_log_analysis ma
	         WHERE ma.event_id = m.id AND ma.analysis_type IN ('investigation', 'remediation') AND ma.status = 'COMPLETED')
	     OR EXISTS (SELECT 1 FROM event_analysis_mapping mm JOIN event_log_analysis ma ON ma.id = mm.analysis_id
	         WHERE mm.event_id = m.id AND mm.analysis_type IN ('investigation', 'remediation') AND ma.status = 'COMPLETED')))`

// readRemediationTarget reads an event of accountID with the live main event it follows, if any
// (livePrimaryEventSQL).
func readRemediationTarget(ctx context.Context, db *sqlx.DB, accountID, eventID string) (remediationTarget, error) {
	var t remediationTarget
	err := db.GetContext(ctx, &t, `SELECT e.id::text AS id, COALESCE(e.nb_status, 'OPEN') AS nb_status,
		       CASE WHEN p.main IS NULL THEN ''
		            WHEN NOT EXISTS (SELECT 1 FROM event_log_analysis pa WHERE pa.event_id = p.main AND pa.analysis_type = 'remediation' AND COALESCE(pa.analysis, '') <> '')
		             AND EXISTS (SELECT 1 FROM event_log_analysis oa WHERE oa.event_id = e.id AND oa.analysis_type = 'remediation' AND COALESCE(oa.analysis, '') <> '')
		              THEN ''
		            ELSE p.main::text END AS primary_id,
		       COALESCE(e.status, '') = 'FIRING' AS firing
		  FROM events e CROSS JOIN LATERAL (SELECT `+livePrimaryEventSQL+` AS main) p
		 WHERE e.id = $1::uuid AND e.cloud_account_id = $2::uuid`, eventID, accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return t, fmt.Errorf("remediation verdict: event %s not found on the account", eventID)
	}
	if err != nil {
		return t, fmt.Errorf("remediation verdict: read event: %w", err)
	}
	return t, nil
}

// setEventLabel sets one label on an event, keeping the others.
func setEventLabel(ctx context.Context, db sqlx.ExecerContext, accountID, eventID, key, value string) error {
	if _, err := db.ExecContext(ctx, `UPDATE events
		   SET labels = (CASE WHEN jsonb_typeof(labels) = 'object' THEN labels ELSE '{}'::jsonb END) || jsonb_build_object($1::text, $2::text),
		       updated_at = NOW()
		 WHERE id = $3::uuid AND cloud_account_id = $4::uuid`, key, value, eventID, accountID); err != nil {
		return fmt.Errorf("remediation: label %s: %w", key, err)
	}
	return nil
}

// personName is the display name of the user chosenBy names, or chosenBy itself when it is not a known user.
func personName(ctx context.Context, db *sqlx.DB, chosenBy string) string {
	chosenBy = strings.TrimSpace(chosenBy)
	if chosenBy == "" {
		return "a person"
	}
	if _, err := uuid.Parse(chosenBy); err != nil {
		return chosenBy
	}
	var name string
	if err := db.GetContext(ctx, &name, `SELECT COALESCE(NULLIF(display_name, ''), username::text, '') FROM users WHERE id = $1::uuid`, chosenBy); err != nil || name == "" {
		return chosenBy
	}
	return name
}

// UpdateRefireLabel sets the kind of an event's re-fire label (the loop compares the plans' cause keys once the
// new chain has one). Only an event triage already labelled a re-fire is changed.
// comparedPlan is the version of the new chain's plan the loop compared (its compared_plan), so a re-fire whose
// cause did not match is not compared again until its plan changes; "" leaves it out.
func UpdateRefireLabel(ctx context.Context, db *sqlx.DB, accountID, eventID, kind, previousRunID, comparedPlan string) (bool, error) {
	if kind != RefireKindFixDidNotHold && kind != RefireKindUnknown {
		return false, fmt.Errorf("refire label: unknown kind %q", kind)
	}
	value, err := refireLabelValue(kind, previousRunID, comparedPlan)
	if err != nil {
		return false, err
	}
	res, err := db.ExecContext(ctx, `UPDATE events
		   SET labels = labels || jsonb_build_object($1::text, $2::text), updated_at = NOW()
		 WHERE id = $3::uuid AND cloud_account_id = $4::uuid AND jsonb_typeof(labels) = 'object' AND labels ? $1::text`,
		LabelRefire, value, eventID, accountID)
	if err != nil {
		return false, fmt.Errorf("refire label: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("refire label: %w", err)
	}
	return n == 1, nil
}

// refireLabelValue is the nb_refire label: {kind, previous_run_id}, and compared_plan once the loop compared it.
func refireLabelValue(kind, previousRunID, comparedPlan string) (string, error) {
	label := map[string]string{"kind": kind, "previous_run_id": previousRunID}
	if comparedPlan != "" {
		label["compared_plan"] = comparedPlan
	}
	raw, err := json.Marshal(label)
	if err != nil {
		return "", fmt.Errorf("refire label: %w", err)
	}
	return string(raw), nil
}

// RefireWindow is how soon after a loop resolve a new chain of the same alert counts as a re-fire of it.
const RefireWindow = 24 * time.Hour

// labelRefire labels the first firing of a chain that broke at a loop resolve (resolvedBy, at resolvedAt) as a
// re-fire, "unknown" until the loop compares the two plans' cause keys. Additive: a failure never fails triage.
func labelRefire(ctx context.Context, db sqlx.ExecerContext, event *models.Event, resolvedBy string, resolvedAt *time.Time) {
	if !isRefire(event.StartsAt, resolvedAt) || event.CloudAccountId == nil {
		return
	}
	value, err := refireLabelValue(RefireKindUnknown, resolvedRunID(resolvedBy), "")
	if err == nil {
		err = setEventLabel(ctx, db, *event.CloudAccountId, event.Id, LabelRefire, value)
	}
	if err != nil {
		slog.WarnContext(ctx, "triage: re-fire not labelled", "error", err, "event_id", event.Id)
	}
}

// isRefire reports whether a firing that started at startsAt is within RefireWindow of a resolve at resolvedAt.
// One that started before the resolve was firing when it was resolved: also a re-fire.
func isRefire(startsAt, resolvedAt *time.Time) bool {
	return startsAt != nil && resolvedAt != nil && startsAt.Sub(*resolvedAt) <= RefireWindow
}

// resolvedRunID is the run_id of an nb_resolved_by label, or "".
func resolvedRunID(label string) string {
	var v struct {
		RunID string `json:"run_id"`
	}
	_ = json.Unmarshal([]byte(label), &v)
	return v.RunID
}
