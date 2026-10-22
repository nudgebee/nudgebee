package events

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// Statuses of a CommandExecution row in event_resolution. Each row is one person's decision on one plan
// step: Run (InProgress until it settles), "I ran this" (Success, data.manual) or Decline.
const (
	RunStatusInProgress = "InProgress"
	RunStatusSuccess    = "Success"
	RunStatusFailed     = "Failed"
	// RunStatusUnknown: the command may have run, and NudgeBee does not know how it ended. Never retried
	// automatically.
	RunStatusUnknown  = "Unknown"
	RunStatusDeclined = "Declined"
)

// SettledRunStatuses is the SQL list of the statuses of a run that ended: it ran, or may have (not one
// still running, nor a declined step). For "status IN " + SettledRunStatuses.
const SettledRunStatuses = "('Success', 'Failed', 'Unknown')"

// UnknownRerunWait: how long after dispatch an Unknown run may be re-run, and how long InProgress lasts before the
// reaper marks it Unknown. Exceeds kubectl's 180 s agent timeout (k8s-agent runner/pkg/kube/exec.go) and
// RunSendTimeout; helm/argocd/shell pods may outlive it, hence a re-run also needs the person's acknowledgement.
const UnknownRerunWait = RunSendTimeout + 5*time.Minute

// RunSendTimeout is the most llm-server waits for one run's send once the person pressed Run.
const RunSendTimeout = 10 * time.Minute

// ApprovalKey identifies one decision opportunity: one step of one plan version of one event, shared by Run,
// Decline and "I ran this" from any surface, so a step is decided once.
// step is the step's canonical identity (action index + which command); command is the exact command, or ""
// for a command-less change.
func ApprovalKey(accountId, eventId, planVersion, step, command string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{accountId, eventId, planVersion, step, strings.TrimSpace(command)}, "\x1f")))
	return hex.EncodeToString(sum[:])
}

// PlanVersion is the version of a stored plan: the hash of the exact text stored for it.
func PlanVersion(storedPlan string) string {
	sum := sha256.Sum256([]byte(storedPlan))
	return hex.EncodeToString(sum[:])
}

// RemediationDecision is one person's decision on one plan step, as ClaimRemediationDecision records it.
type RemediationDecision struct {
	AccountId string
	EventId   string
	UserId    string
	// Ref is the row's type_reference_id: the command, or "change:<action>" for a command-less change.
	Ref         string
	ApprovalKey string
	// Status is RunStatusInProgress for a Run, RunStatusSuccess for "I ran this", RunStatusDeclined.
	Status        string
	Data          map[string]any
	StatusMessage string
	// AcknowledgeUnknown: the person confirmed that an earlier run of this command, whose outcome is
	// unknown, may have run. Only then, and only UnknownRerunWait after it, is a Run sent again.
	AcknowledgeUnknown bool
	// PlanRowId and PlanVersion bind the decision to the plan the person saw: the event_log_analysis row
	// holding the event's plan, and the version (PlanVersion of its text) they confirmed. The claim
	// re-reads the row under FOR SHARE, so a regenerate either waits for it or makes it fail.
	PlanRowId   string
	PlanVersion string
}

const (
	// DecisionAlreadyDecided: this step of this plan version already has a decision.
	DecisionAlreadyDecided = "already_decided"
	// DecisionAlreadyRunning: the same command is running on this account right now.
	DecisionAlreadyRunning = "already_running"
	// DecisionUnknownOutcome: the last run of this command on this account has an unknown outcome and
	// the request did not acknowledge it, or came before UnknownRerunWait passed.
	DecisionUnknownOutcome = "unknown_outcome"
	// DecisionPlanChanged: the event's plan is no longer the version the person confirmed.
	DecisionPlanChanged = "plan_changed"
	// DecisionWrongTarget: the event does not belong to the account the decision would act on.
	DecisionWrongTarget = "wrong_target"
)

// DecisionConflict is a decision that was not recorded, and the row that stopped it.
type DecisionConflict struct {
	Reason string
	RowId  string
	Status string
	// Manual: the deciding row is a step the operator marked done.
	Manual bool
	// WaitSeconds is how long until an Unknown run may be run again (DecisionUnknownOutcome only).
	WaitSeconds int
}

// decisionRow is a CommandExecution row as the claim reads it.
type decisionRow struct {
	Id      string `db:"id"`
	Status  string `db:"status"`
	NotSent bool   `db:"not_sent"`
	Manual  bool   `db:"manual"`
	// WaitSeconds is how much of UnknownRerunWait is left since the row was created.
	WaitSeconds int `db:"wait_seconds"`
}

// decides reports whether a row is a decision that closes its step. An Unknown run and a run that
// provably never ran (not_sent) do not: the first may be re-run after acknowledgement, the second is
// simply retried. The partial unique index on data->>'approval_key' encodes the same rule.
func (r decisionRow) decides() bool {
	return r.Status != RunStatusUnknown && (r.Status != RunStatusFailed || !r.NotSent)
}

func (r decisionRow) conflict(reason string) *DecisionConflict {
	return &DecisionConflict{Reason: reason, RowId: r.Id, Status: r.Status, Manual: r.Manual, WaitSeconds: r.WaitSeconds}
}

// ClaimRemediationDecision records a decision on a plan step before anything acts on it, or returns the refusing
// conflict. For a Run the row is InProgress and only its holder may send the command. Serialised per account by an
// advisory lock, backed by V1055's unique index on data->>'approval_key'. Every statement runs under ctx: at its
// deadline the transaction rolls back and nothing is recorded, so nothing may be sent.
func (r *EventAnalysisRepository) ClaimRemediationDecision(ctx *security.RequestContext, d RemediationDecision) (string, *DecisionConflict, error) {
	if d.AccountId == "" || d.EventId == "" || d.ApprovalKey == "" || d.PlanRowId == "" || d.PlanVersion == "" {
		return "", nil, errors.New("ClaimRemediationDecision: account, event, approval key and plan are required")
	}
	data := map[string]any{}
	for k, v := range d.Data {
		data[k] = v
	}
	data["approval_key"] = d.ApprovalKey
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return "", nil, fmt.Errorf("ClaimRemediationDecision: marshal: %w", err)
	}
	goCtx := ctx.GetContext()
	// database/sql rolls the transaction back if goCtx ends first, so a commit after the deadline fails
	// rather than records.
	tx, err := r.dbManager.Db.BeginTxx(goCtx, nil)
	if err != nil {
		return "", nil, fmt.Errorf("ClaimRemediationDecision: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // a no-op once committed
	id, conflict, err := claimDecisionInTx(goCtx, tx, d, string(dataJSON))
	if err != nil {
		return "", nil, fmt.Errorf("ClaimRemediationDecision: %w", err)
	}
	if conflict != nil {
		return "", conflict, nil
	}
	if err := tx.Commit(); err != nil {
		return "", nil, fmt.Errorf("ClaimRemediationDecision: commit: %w", err)
	}
	return id, nil, nil
}

// claimDecisionInTx is ClaimRemediationDecision's work inside its transaction: the new row's id, or the
// conflict that refused it.
func claimDecisionInTx(goCtx context.Context, tx *sqlx.Tx, d RemediationDecision, dataJSON string) (string, *DecisionConflict, error) {
	if _, err := tx.ExecContext(goCtx, `SELECT pg_advisory_xact_lock(hashtextextended('remediation-run:' || $1, 0))`, d.AccountId); err != nil {
		return "", nil, fmt.Errorf("lock: %w", err)
	}
	// The target is the event's own account: a decision on any other account is refused.
	var owned []int
	if err := tx.SelectContext(goCtx, &owned, `SELECT 1 FROM events WHERE id = $1 AND cloud_account_id = $2`, d.EventId, d.AccountId); err != nil {
		return "", nil, fmt.Errorf("read event: %w", err)
	}
	if len(owned) != 1 {
		return "", &DecisionConflict{Reason: DecisionWrongTarget}, nil
	}
	var stored []string
	if err := tx.SelectContext(goCtx, &stored, `SELECT COALESCE(analysis, '') FROM event_log_analysis WHERE id = $1 AND event_id = $2 FOR SHARE`,
		d.PlanRowId, d.EventId); err != nil {
		return "", nil, fmt.Errorf("read plan: %w", err)
	}
	if len(stored) != 1 || PlanVersion(stored[0]) != d.PlanVersion {
		return "", &DecisionConflict{Reason: DecisionPlanChanged}, nil
	}
	var rows []decisionRow
	if err := tx.SelectContext(goCtx, &rows, `SELECT id, status, 0 AS wait_seconds,
		       COALESCE(data->>'not_sent', '') = 'true' AS not_sent, COALESCE(data->>'manual', '') = 'true' AS manual
		  FROM event_resolution
		 WHERE event_id = $1 AND type = 'CommandExecution' AND data->>'approval_key' = $2
		 ORDER BY created_at DESC`, d.EventId, d.ApprovalKey); err != nil {
		return "", nil, fmt.Errorf("read step: %w", err)
	}
	for _, row := range rows {
		if row.decides() {
			return "", row.conflict(DecisionAlreadyDecided), nil
		}
		// Declining says nothing ran; after a run whose outcome is unknown that may be untrue, so the
		// person checks the cluster and runs it again or marks it done instead.
		if d.Status == RunStatusDeclined && row.Status == RunStatusUnknown {
			return "", row.conflict(DecisionUnknownOutcome), nil
		}
	}
	if d.Status == RunStatusInProgress {
		// Newest run of this command on the account from any event: two alerts may propose it, and neither may send
		// while the other is running or unresolved. not_sent runs are skipped so they don't hide an Unknown run.
		// ponytail: type_reference_id is not indexed; CommandExecution rows are few. Index it if they are not.
		var last []decisionRow
		if err := tx.SelectContext(goCtx, &last, `SELECT er.id, er.status, false AS not_sent, false AS manual,
			       GREATEST(0, CEIL(EXTRACT(EPOCH FROM (er.created_at + $3::interval - LOCALTIMESTAMP))))::int AS wait_seconds
			  FROM event_resolution er JOIN events e ON e.id = er.event_id
			 WHERE e.cloud_account_id = $1 AND er.type = 'CommandExecution' AND er.type_reference_id = $2
			   AND COALESCE(er.data->>'manual', '') <> 'true' AND er.status <> 'Declined'
			   AND COALESCE(er.data->>'not_sent', '') <> 'true'
			 ORDER BY er.created_at DESC LIMIT 1`,
			d.AccountId, d.Ref, fmt.Sprintf("%d seconds", int(UnknownRerunWait.Seconds()))); err != nil {
			return "", nil, fmt.Errorf("read last run: %w", err)
		}
		if len(last) == 1 {
			switch {
			case last[0].Status == RunStatusInProgress:
				return "", last[0].conflict(DecisionAlreadyRunning), nil
			case last[0].Status == RunStatusUnknown && (!d.AcknowledgeUnknown || last[0].WaitSeconds > 0):
				return "", last[0].conflict(DecisionUnknownOutcome), nil
			}
		}
	}
	id := common.GenerateUUID()
	// DO NOTHING rather than catching the unique violation, which would abort the transaction.
	res, err := tx.ExecContext(goCtx, `INSERT INTO event_resolution (id, event_id, type, data, status, type_reference_id, resolver_type, resolver_id, status_message)
		VALUES ($1, $2, 'CommandExecution', $3, $4, $5, 'User', $6, $7) ON CONFLICT DO NOTHING`,
		id, d.EventId, dataJSON, d.Status, d.Ref, d.UserId, d.StatusMessage)
	if err != nil {
		return "", nil, fmt.Errorf("insert: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return "", &DecisionConflict{Reason: DecisionAlreadyDecided}, nil
	}
	return id, nil, nil
}

// SettleRemediationRun records how a claimed run ended, only if still InProgress (first writer wins vs the reaper),
// and reports whether it did. Under ctx: a stalled database fails at the deadline; the reaper later marks it Unknown.
func (r *EventAnalysisRepository) SettleRemediationRun(ctx *security.RequestContext, runId, status string, patch map[string]any, statusMessage string) (bool, error) {
	if patch == nil {
		patch = map[string]any{} // jsonb || 'null' would turn the row's data into an array
	}
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return false, fmt.Errorf("SettleRemediationRun: marshal: %w", err)
	}
	res, err := r.dbManager.Db.ExecContext(ctx.GetContext(), `UPDATE event_resolution
		    SET status = $2, data = COALESCE(NULLIF(data, 'null'::jsonb), '{}'::jsonb) || $3::jsonb, status_message = $4, updated_at = NOW()
		  WHERE id = $1 AND type = 'CommandExecution' AND status = 'InProgress'`, runId, status, string(patchJSON), statusMessage)
	if err != nil {
		return false, fmt.Errorf("SettleRemediationRun: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("SettleRemediationRun: %w", err)
	}
	return n == 1, nil
}

// ReapRemediationRuns marks Unknown every run still InProgress UnknownRerunWait after it was claimed: its
// settle never happened (the process died, the database was down). It returns how many it marked.
func ReapRemediationRuns(ctx context.Context, db *sqlx.DB) (int64, error) {
	n, err := affected(db.ExecContext(ctx, `UPDATE event_resolution
		    SET status = 'Unknown', data = COALESCE(NULLIF(data, 'null'::jsonb), '{}'::jsonb) || '{"reaped": true}'::jsonb,
		        status_message = 'No result was recorded. The command may have run.', updated_at = NOW()
		  WHERE type = 'CommandExecution' AND status = 'InProgress' AND created_at < LOCALTIMESTAMP - $1::interval`,
		fmt.Sprintf("%d seconds", int(UnknownRerunWait.Seconds()))))
	if err != nil {
		return 0, fmt.Errorf("ReapRemediationRuns: %w", err)
	}
	return n, nil
}

// StepStatuses is the status of the newest decision on each step of an event's plan version, by step
// ("0:apply", "1:rollback"), with a run that provably never ran left out. A person's "I ran this" is a
// Success.
func StepStatuses(ctx context.Context, db *sqlx.DB, eventId, planVersion string) (map[string]string, error) {
	var rows []struct {
		Step   string `db:"step"`
		Status string `db:"status"`
	}
	if err := db.SelectContext(ctx, &rows, `SELECT DISTINCT ON (data->>'step') data->>'step' AS step, status::text AS status
		  FROM event_resolution
		 WHERE event_id::text = $1 AND type = 'CommandExecution' AND data->>'plan_version' = $2
		   AND COALESCE(data->>'step', '') <> '' AND COALESCE(data->>'not_sent', '') <> 'true'
		 ORDER BY data->>'step', created_at DESC`, eventId, planVersion); err != nil {
		return nil, fmt.Errorf("StepStatuses: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Step] = r.Status
	}
	return out, nil
}
