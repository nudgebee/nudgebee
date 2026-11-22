package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"nudgebee/llm/audit"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/relay"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// A remediation run is one person's approval of one plan step (#40734): written InProgress before sending, sent once
// (only by the request that wrote it) on a context the browser can't cancel, then settled from the reply. An unknown
// outcome is recorded Unknown and never auto-retried.

// isRunSlot reports whether a slot is a step a person approves by pressing Run: an action's execute
// command (an empty slot is execute) or its rollback. Verify and outcome are reads that annotate a run.
func isRunSlot(slot string) bool {
	switch strings.ToLower(strings.TrimSpace(slot)) {
	case "", RemediationSlotExecute, RemediationSlotRollback:
		return true
	}
	return false
}

// planStep is a step's canonical identity within a plan version: action index + "apply" (execute command or
// command-less change) or "rollback". Run, Decline and "I ran this" share it. "" if absent (caller checks
// planCommandRejection first).
func planStep(plan RemediationPlan, slot, command, action string) string {
	command, action = strings.TrimSpace(command), strings.TrimSpace(action)
	rollback := strings.EqualFold(strings.TrimSpace(slot), RemediationSlotRollback)
	for i, a := range plan.Actions {
		exec := stepRef(a)
		switch {
		case rollback:
			if command != "" && strings.TrimSpace(a.RollbackCommand) == command {
				return fmt.Sprintf("%d:rollback", i)
			}
		case command != "":
			if exec == command {
				return fmt.Sprintf("%d:apply", i)
			}
		case exec == "" && action != "" && strings.TrimSpace(a.Action) == action:
			return fmt.Sprintf("%d:apply", i)
		}
	}
	return ""
}

// stepOrderRejection says why the plan step (planStep's key) may not run yet, or "": a step that runs after
// another (its After names that step's action) runs only once that step succeeded on the same plan, by
// NudgeBee or by a person. A failed or declined earlier step needs a new plan, not the next step.
func stepOrderRejection(ctx *security.RequestContext, eventId string, stored storedPlan, step string) string {
	var index int
	if _, err := fmt.Sscanf(step, "%d:apply", &index); err != nil || index < 0 || index >= len(stored.Plan.Actions) {
		return ""
	}
	before := planRunsAfter(stored.Plan)[index]
	if before < 0 {
		return ""
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "remediation: the order of the plan's steps could not be checked, so nothing was run"
	}
	readCtx, cancel := context.WithTimeout(ctx.GetContext(), decisionDBTimeout)
	defer cancel()
	statuses, err := events.StepStatuses(readCtx, dbManager.Db, eventId, stored.Version)
	if err != nil {
		ctx.GetLogger().Error("remediation: step order not read", "error", err)
		return "remediation: the order of the plan's steps could not be checked, so nothing was run"
	}
	if statuses[fmt.Sprintf("%d:apply", before)] == events.RunStatusSuccess {
		return ""
	}
	return fmt.Sprintf("remediation: step %d runs after step %d (%s), which has not succeeded on this plan. Run step %d first, or ask for a new plan if it failed or was declined.",
		index+1, before+1, firstNonEmpty(stored.Plan.Actions[before].Title, stored.Plan.Actions[before].Action), before+1)
}

// runFacts reads what a CommandExecution row's data says about how it ended.
type runFacts struct {
	ExitCodeReported bool `json:"exit_code_reported"`
	Manual           bool `json:"manual"`
	NotSent          bool `json:"not_sent"`
	// AutomationCompleted: the automation a step ran reported its run COMPLETED (#42459).
	AutomationCompleted bool `json:"automation_completed"`
}

func readRunFacts(data []byte) runFacts {
	var f runFacts
	_ = json.Unmarshal(data, &f)
	return f
}

// verifiedSuccess reports whether a run row shows NudgeBee ran the command and the agent reported exit
// code 0, or ran the automation and its run completed. A run without a reported exit code, a run whose outcome
// is unknown and a step the operator marked done are not verified, and nothing may count them as a fix that worked.
func verifiedSuccess(status string, f runFacts) bool {
	return status == events.RunStatusSuccess && (f.ExitCodeReported || f.AutomationCompleted) && !f.Manual
}

// sentCommand is what NudgeBee learned from sending one approved command to its executor.
type sentCommand struct {
	tools.RemediationExecutionResult
	// ExitCodeReported: the executor said how the command exited. Without it a clean reply is a run
	// whose outcome is unverified, and an error says nothing about whether it ran.
	ExitCodeReported bool
	// Tool is the registered tool the command ran through, for the audit.
	Tool string
	Err  error
}

// sendRemediationCommand sends one approved command once, through the executor the account's provider
// and the command pick (tools.RemediationSubstrateFor): a cloud CLI in a workspace pod, kubectl, helm,
// argocd and shell through the relay. Nothing here retries.
func sendRemediationCommand(ctx *security.RequestContext, accountId, userId string, substrate tools.RemediationSubstrate, nbTool toolcore.NBTool, command string) sentCommand {
	sent := sentCommand{Tool: firstNonEmpty(substrate.CloudCliTool, substrate.RelayTool)}
	start := time.Now()
	var raw string
	if substrate.CloudCliTool != "" {
		// A Run click has no conversation behind it, so the workspace gets the per-account directory.
		raw, sent.Err = tools.ExecuteCloudCli(ctx, substrate.CloudCliTool, accountId, tools.DefaultCloudCliConversationId(accountId), command)
		sent.Stdout, sent.Stderr, sent.ExitCode, sent.Success, sent.ExitCodeReported = workspaceOutcome(sent.Err, raw)
	} else {
		toolCtx := toolcore.NewNbToolContext(ctx, nbTool, accountId, userId, "", "", "", command, nil, "", toolcore.NBQueryConfig{}, "")
		var result any
		result, sent.Err = tools.ExecuteContainerJob(toolCtx, substrate.RelayJob, command, accountId, map[string]any{}, true)
		raw, _ = result.(string)
		var exitErr *tools.RelayCommandExitError
		switch {
		case errors.As(sent.Err, &exitErr):
			// It ran and the agent reported a non-zero exit.
			stdout, stderr, _, parsed := parseRelayExecResult(raw)
			if !parsed {
				stdout = raw
			}
			sent.Stdout, sent.Stderr, sent.ExitCode, sent.ExitCodeReported = stdout, stderr, exitErr.ExitCode, true
		case sent.Err != nil:
			sent.Stdout, sent.Stderr = raw, sent.Err.Error()
		default:
			// An unparsed reply means the executor merged its output and dropped the exit code.
			sent.Stdout, sent.Stderr, sent.ExitCode, sent.ExitCodeReported = parseRelayExecResult(raw)
			sent.Success = !sent.ExitCodeReported || sent.ExitCode == 0
		}
	}
	sent.Command, sent.ExecutedAt, sent.Duration = command, start.Format(time.RFC3339), time.Since(start).String()
	return sent
}

// runSettlement classifies a run from its reply. Only proof it didn't run is notSent (agent not connected, relay
// not dialled, agent refused before starting); any other error without an exit code is Unknown: it may have run.
func runSettlement(sent sentCommand) (status string, notSent bool) {
	var refused *tools.RelayCommandRefusedError
	var dial *net.OpError
	switch {
	case sent.ExitCodeReported && sent.Err == nil && sent.ExitCode == 0:
		return events.RunStatusSuccess, false
	case sent.ExitCodeReported:
		return events.RunStatusFailed, false
	case sent.Err == nil:
		// It ran and nothing says how it ended: recorded as done, not verified (exit_code_reported false).
		return events.RunStatusSuccess, false
	case errors.Is(sent.Err, relay.ErrAgentNotConnected):
		// Refused before any request was sent.
		return events.RunStatusFailed, true
	case errors.As(sent.Err, &dial) && dial.Op == "dial":
		// No connection, so no byte of the request was written; net/http re-sends a POST only when
		// nothing was written either.
		return events.RunStatusFailed, true
	case errors.As(sent.Err, &refused) && agentRefusedBeforeRunning(refused):
		return events.RunStatusFailed, true
	default:
		return events.RunStatusUnknown, false
	}
}

// agentRefusedBeforeRunning reports whether the reply status proves the agent never started the command: its
// dispatcher returns 400/401/404/503 before any handler, 500/504 only after (k8s-agent runner/pkg/dispatch/dispatch.go).
// Only the status code counts, never the error wording.
func agentRefusedBeforeRunning(e *tools.RelayCommandRefusedError) bool {
	switch e.StatusCode {
	case 400, 401, 404, 503:
		return true
	}
	return false
}

// runResult answers a decision on a step: the executor's result, the run's recorded status and row, and
// the exit code only when the agent reported one (an Unknown or not-run result has none to show).
type runResult struct {
	tools.RemediationExecutionResult
	ExitCode *int   `json:"exit_code"`
	Status   string `json:"status"`
	RunId    string `json:"run_id,omitempty"`
	// AutomationURL links the run of an automation step (#42459).
	AutomationURL string `json:"automation_url,omitempty"`
}

// runStatusMessage is what the resolutions list shows beside a settled run.
func runStatusMessage(status string, notSent bool, sent sentCommand, surface string) string {
	switch {
	case sent.ExitCodeReported:
		return fmt.Sprintf("Ran from %s, exit code %d", surface, sent.ExitCode)
	case notSent:
		return "Not run: " + truncateForRecord(sent.Err.Error())
	case status == events.RunStatusSuccess:
		return fmt.Sprintf("Ran from %s; the executor reported no exit code, so the outcome is unverified", surface)
	default:
		return "No result came back. The command may have run: " + truncateForRecord(sent.Err.Error())
	}
}

// decisionConflictMessage says why a decision was not recorded and what the person can do.
func decisionConflictMessage(c *events.DecisionConflict) string {
	switch c.Reason {
	case events.DecisionPlanChanged:
		return planChangedMessage
	case events.DecisionWrongTarget:
		return "remediation: this event does not belong to that account, so nothing was recorded or run."
	case events.DecisionAlreadyRunning:
		return "remediation: this command is already running on this account. Wait for its result."
	case events.DecisionUnknownOutcome:
		msg := "remediation: the last run of this command on this account has an unknown outcome. It may have run, and its effects may still be in progress. Check the cluster before running it again"
		if c.WaitSeconds > 0 {
			return fmt.Sprintf("%s; you can run it again in %d min.", msg, (c.WaitSeconds+59)/60)
		}
		return msg + ", then confirm that you checked."
	}
	what := map[string]string{
		events.RunStatusInProgress: "it is running",
		events.RunStatusSuccess:    "it ran",
		events.RunStatusFailed:     "it ran and failed",
		events.RunStatusDeclined:   "it was declined",
	}[c.Status]
	if c.Manual {
		what = "it was marked done"
	}
	if what == "" {
		return "remediation: this step of the plan was already decided."
	}
	return fmt.Sprintf("remediation: this step of the plan was already decided: %s. A new proposal is needed to act on it again.", what)
}

// stepAnswer is the answer to a decision on a plan step, from any surface: a refusal (Message) or Data.
type stepAnswer struct {
	Code    int
	Message string
	Data    any
}

func refuse(code int, message string) stepAnswer { return stepAnswer{Code: code, Message: message} }

// write sends the answer as the HTTP response.
func (a stepAnswer) write(c *gin.Context) {
	if a.Message != "" {
		c.JSON(a.Code, BuildApiResponse(nil, []error{common.Error{Message: a.Message}}))
		return
	}
	c.JSON(a.Code, BuildApiResponse(a.Data, nil))
}

// decisionFailure answers a decision that could not be recorded. Nothing has been sent.
func decisionFailure(logger *slog.Logger, conflict *events.DecisionConflict, err error) stepAnswer {
	if err != nil {
		logger.Error("remediation: decision not recorded", "error", err)
		return refuse(500, "remediation: the decision could not be recorded, so nothing was run")
	}
	logger.Info("remediation: decision refused", "reason", conflict.Reason, "row_id", conflict.RowId)
	return refuse(409, decisionConflictMessage(conflict))
}

// runRemediationStep runs one approved execute or rollback command once: its decision is recorded
// before it is sent, and only the request that recorded it sends it.
func runRemediationStep(ctx *security.RequestContext, request RemediationExecuteRequest, stored storedPlan, command, userId string) stepAnswer {
	// The person approved by pressing Run; closing the page must cancel nothing from here on. Each step
	// gets its own deadline instead, so a stalled database or relay cannot hold the request indefinitely.
	detached := context.WithoutCancel(ctx.GetContext())
	// Where it runs is the event's account, read now; when that cannot be read, nothing is run.
	targetCtx, cancelTarget := context.WithTimeout(detached, decisionDBTimeout)
	target, err := remediationTarget(ctx.WithContext(targetCtx), request.AccountId)
	cancelTarget()
	if err != nil {
		ctx.GetLogger().Error("remediation: run target not established", "error", err)
		return refuse(503, "remediation: the account this command would run on could not be confirmed, so nothing was run")
	}
	// The executor is chosen from the account's provider, then narrowed by the command. Every refusal
	// below comes before the decision is recorded, so a command that cannot run leaves no row.
	substrate := tools.RemediationSubstrateFor(target.Provider, command)
	if substrate.Reject != "" {
		return refuse(400, "remediation: "+substrate.Reject)
	}
	var nbTool toolcore.NBTool
	if substrate.CloudCliTool == "" {
		var found bool
		if nbTool, found = toolcore.GetNBTool(request.AccountId, substrate.RelayTool); !found {
			return refuse(404, "execution tool is not configured for this account")
		}
	}
	if substrate.RelayJob == tools.RelayJobKubectl {
		// The agent runs it on its own cluster as itself; a flag must not send it to another cluster or identity.
		// (The relay's kubectl grammar is checked in remediationCommandRejection and again by ExecuteContainerJob.)
		if flag := tools.KubectlTargetOverride(command); flag != "" {
			return refuse(400, fmt.Sprintf("remediation: %s would point this command at another cluster or identity; NudgeBee runs it only on the account's own cluster", flag))
		}
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return decisionFailure(ctx.GetLogger(), nil, err)
	}
	repo := events.NewEventAnalysisRepository(dbManager)
	slot := RemediationSlotExecute
	if strings.EqualFold(strings.TrimSpace(request.Slot), RemediationSlotRollback) {
		slot = RemediationSlotRollback
	}
	surface, auditSource := decisionSurface(request.Source)
	step := planStep(stored.Plan, slot, command, "")
	runId, conflict, err := claimStepDecision(ctx, repo, request, userId, stored, step, command, command,
		events.RunStatusInProgress, "Running from "+surface, map[string]any{"command": command, "slot": slot, "target": target})
	if err != nil || conflict != nil {
		return decisionFailure(ctx.GetLogger(), conflict, err)
	}

	ctx = ctx.WithContext(detached)
	logger := ctx.GetLogger().With("run_id", runId)
	// The thread says it is running while it runs.
	syncAlertThread(ctx, request.AccountId, request.EventId)
	sendCtx, cancelSend := context.WithTimeout(detached, runSendTimeout)
	sent := sendRemediationCommand(ctx.WithContext(sendCtx), request.AccountId, userId, substrate, nbTool, command)
	cancelSend()
	status, notSent := runSettlement(sent)

	response := sent.RemediationExecutionResult
	response.Success = status == events.RunStatusSuccess
	message := runStatusMessage(status, notSent, sent, surface)
	switch {
	case status == events.RunStatusSuccess:
	case notSent:
		response.Error = sent.Err.Error()
	case status == events.RunStatusFailed:
		response.Error = firstNonEmpty(strings.TrimSpace(sent.Stderr), message)
	default:
		response.Error = message
	}
	patch := map[string]any{
		"success":            response.Success,
		"exit_code_reported": sent.ExitCodeReported,
		// The executor's own words on a failure. A permission refusal here is how the next plan knows
		// to give this verb to the operator (remediation_delivery.go).
		"error": truncateForRecord(failureText(response)),
		// What it printed, for the follow-up and the alert's chat.
		"output": truncateForRecord(response.Stdout),
	}
	if sent.ExitCodeReported {
		patch["exit_code"] = sent.ExitCode
	}
	if notSent {
		patch["not_sent"] = true
	}
	if sent.Err != nil {
		logger.Warn("remediation: run ended without a clean result", "status", status, "not_sent", notSent, "error", sent.Err)
	}
	settleCtx, cancelSettle := context.WithTimeout(detached, decisionDBTimeout)
	settled, settleErr := repo.SettleRemediationRun(ctx.WithContext(settleCtx), runId, status, patch, message)
	cancelSettle()
	observed := status
	if settleErr != nil || !settled {
		// Whether, and how, this run's record was written is not known: the write failed or timed out
		// (it may still have happened), or another writer changed the row first. History and the next
		// decision go by the record, so this answer claims no outcome. The observed outcome is logged.
		logger.Error("remediation: run not settled", "observed", observed, "exit_code", sent.ExitCode, "settled", settled, "error", settleErr)
		status = events.RunStatusUnknown
		response.Success = false
		response.Error = runNotSettledMessage
	}
	// The audit records what the command did, which a failed save does not change.
	writeRemediationAudit(ctx, request.AccountId, command, sent.Tool, observed == events.RunStatusSuccess, audit.EventActionCreate, auditSource)
	syncAlertThread(ctx, request.AccountId, request.EventId)
	result := runResult{RemediationExecutionResult: response, Status: status, RunId: runId}
	if sent.ExitCodeReported {
		result.ExitCode = &sent.ExitCode
	}
	return stepAnswer{Code: 200, Data: result}
}

// How long each part of a run may take once the person has pressed Run. The send's is an outer bound:
// the executors' own timeouts end a send first, and after it the outcome is Unknown. Vars so tests can
// shorten them.
var (
	decisionDBTimeout = 15 * time.Second
	runSendTimeout    = events.RunSendTimeout
)

// planChangedMessage refuses a decision on a plan that is no longer the one the person saw.
const planChangedMessage = "remediation: the plan changed since you reviewed it. Review it again."

// RemediationTarget is where a plan's commands run: the event's account. A Kubernetes account is one
// cluster, reached only through its own agent.
type RemediationTarget struct {
	AccountId   string `json:"account_id" db:"account_id"`
	AccountName string `json:"account_name" db:"account_name"`
	Provider    string `json:"provider" db:"provider"`
}

// remediationTarget reads the account a decision acts on. An error means it could not be established.
func remediationTarget(ctx *security.RequestContext, accountId string) (RemediationTarget, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return RemediationTarget{}, fmt.Errorf("remediationTarget: %w", err)
	}
	var t RemediationTarget
	if err := dbManager.Db.GetContext(ctx.GetContext(), &t, `SELECT id::text AS account_id, COALESCE(account_name, '') AS account_name,
		       COALESCE(cloud_provider, '') AS provider FROM cloud_accounts WHERE id = $1`, accountId); err != nil {
		return RemediationTarget{}, fmt.Errorf("remediationTarget: %w", err)
	}
	return t, nil
}

// targetOrNil is the target for a plan returned to the panel, or nil when it cannot be read.
func targetOrNil(ctx *security.RequestContext, accountId string) *RemediationTarget {
	t, err := remediationTarget(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Warn("remediation: plan target not read", "error", err)
		return nil
	}
	return &t
}

// checkRejection says why NudgeBee will not run a verify or outcome command without a confirmation, or
// "" when it will: a strict kubectl read (tools.ValidateKubectlReadOnly), or a cloud CLI call its tool's
// own rules classify as a read (no model decides).
func checkRejection(ctx *security.RequestContext, accountId, provider, command string) string {
	substrate := tools.RemediationSubstrateFor(provider, command)
	switch {
	case substrate.Reject != "":
		return substrate.Reject + " Check it yourself."
	case substrate.RelayJob == tools.RelayJobKubectl:
		if err := tools.ValidateKubectlReadOnly(command); err != nil {
			return "this check is not a read NudgeBee runs without a confirmation (" + err.Error() + "). Check it yourself."
		}
		return ""
	case substrate.CloudCliTool != "":
		// The follow-up check's rule for cloud reads: no shell syntax, no call that returns credentials or
		// secrets or changes the CLI's identity, and only what the CLI's own rules classify as a read.
		if _, err := tools.CloudReadRejection(ctx, accountId, provider, command); err != nil {
			return "this check is not a read NudgeBee runs without a confirmation (" + err.Error() + "). Check it yourself."
		}
		return ""
	}
	return "NudgeBee runs checks only as kubectl or cloud CLI reads. Check it yourself."
}

// runNotSettledMessage answers a run whose result could not be confirmed as saved.
const runNotSettledMessage = "This run's outcome could not be confirmed: saving its result failed or could not be verified, so its recorded state may not match what happened. Check the cluster, and do not run this step again until its recorded state is reconciled."

// RegisterRemediationRunReaper starts the leader job that marks Unknown every run still InProgress
// events.UnknownRerunWait after it was claimed: the process that claimed it died, or its settle failed.
func RegisterRemediationRunReaper() error {
	return common.NewLeaderIntervalJob("remediation_run_reaper", reapRemediationRuns, time.Minute)
}

func reapRemediationRuns() error {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("remediation_run_reaper: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	reaped, err := events.ReapRemediationRuns(ctx, dbManager.Db)
	if err != nil {
		return fmt.Errorf("remediation_run_reaper: %w", err)
	}
	if reaped > 0 {
		slog.Warn("remediation_run_reaper: runs with no recorded result marked Unknown", "count", reaped)
	}
	return nil
}

// decisionSurface names where a decision was made, for its status message and audit.
func decisionSurface(source string) (words, audit string) {
	switch source {
	case slackSource:
		return "Slack", "slack"
	case chatSource:
		return "the alert's chat", "chat"
	}
	return "the remediation panel", "remediation-panel"
}

// claimStepDecision records a decision on one plan step before anything acts on it: under the step's
// approval key for the plan version the person saw, with the step, plan version and source in its data.
// It runs on a context closing the page cannot cancel.
func claimStepDecision(ctx *security.RequestContext, repo *events.EventAnalysisRepository, request RemediationExecuteRequest, userId string, stored storedPlan,
	step, command, ref, status, message string, data map[string]any) (string, *events.DecisionConflict, error) {
	claimCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx.GetContext()), decisionDBTimeout)
	defer cancel()
	return repo.ClaimRemediationDecision(ctx.WithContext(claimCtx), stepDecision(request, userId, stored, step, command, ref, status, message, data))
}

// stepDecision is the record of a decision on one plan step. A step decided in the alert's chat under a
// person's approval (request.ChatApproval) is recorded as that approval's run (C1): keyed by the
// approval, with who approved it, the CLI it runs with and the option (1-based) it is, and the step's own
// key beside it so the step is still decided once from any surface.
func stepDecision(request RemediationExecuteRequest, userId string, stored storedPlan, step, command, ref, status, message string, data map[string]any) events.RemediationDecision {
	data["step"], data["plan_version"] = step, stored.Version
	if request.Source != "" {
		data["source"] = request.Source
	}
	d := events.RemediationDecision{
		AccountId:          request.AccountId,
		EventId:            request.EventId,
		UserId:             userId,
		Ref:                ref,
		ApprovalKey:        events.ApprovalKey(request.AccountId, request.EventId, stored.Version, step, command),
		Status:             status,
		Data:               data,
		StatusMessage:      message,
		AcknowledgeUnknown: request.AcknowledgeUnknown,
		PlanRowId:          stored.RowId,
		PlanVersion:        stored.Version,
	}
	if a := request.ChatApproval; a != nil && a.ApprovalKey != "" {
		data["approved_by"] = firstNonEmpty(a.ApprovedBy, userId)
		withApprovalAnswer(data, a.Answer)
		if tool := commandTool(firstNonEmpty(command, request.Command)); tool != "" {
			data["tool"] = tool
		}
		if option := stepOption(step); option > 0 {
			data["option"] = option
		}
		d.StepKey, d.ApprovalKey = d.ApprovalKey, a.ApprovalKey
	}
	return d
}

// stepOption is the option (1-based) a plan step key ("0:apply", "1:rollback") names, or 0.
func stepOption(step string) int {
	index, _, ok := strings.Cut(step, ":")
	if !ok {
		return 0
	}
	var n int
	if _, err := fmt.Sscanf(index, "%d", &n); err != nil || n < 0 {
		return 0
	}
	return n + 1
}

// decisionCallerRejection returns the HTTP status and message refusing a decision on a plan step, or 0. The decider
// is the person in the x-tenant-id / x-user-id headers (LLM_SERVER_TOKEN is checked by authHandlerMiddleware);
// identity in the body is never used and must agree with the headers.
func decisionCallerRejection(c *gin.Context, h *ActionRequest, payload map[string]any) (int, string) {
	tenant, user := strings.TrimSpace(c.Request.Header.Get("x-tenant-id")), strings.TrimSpace(c.Request.Header.Get("x-user-id"))
	if tenant == "" || user == "" || user == uuid.Nil.String() {
		return 401, "remediation: a decision needs the signed-in person named by the gateway (x-tenant-id, x-user-id)"
	}
	for _, body := range []map[string]any{h.SessionVariables, payload} {
		if v := sessionString(body, "user_id"); v != "" && v != user {
			return 400, "remediation: the request body names a different person than the caller"
		}
		if v := sessionString(body, "tenant_id"); v != "" && v != tenant {
			return 400, "remediation: the request body names a different tenant than the caller"
		}
	}
	return 0, ""
}
