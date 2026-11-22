package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"nudgebee/llm/audit"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	"nudgebee/llm/watch"
)

// A fix option can run one of the account's own automations (#42459): an AI-invocable automation whose run never
// stops for a person's approval of its own. The planning turn sees them (automationCatalogText), a plan may name one
// (checkPlanAutomations keeps only a valid one), a person approves the step like any other, and NudgeBee starts one
// run as that person (runAutomationStep). The run's row stays InProgress, and the wait for the run to end is a watch on
// Nubi's watcher (#42461); when it ends the follow-ups job settles the row (settleAutomationRuns) and the follow-up
// check runs as for a command.

// PlanAutomation is the automation a step runs and the inputs it runs with. The model names the id and inputs; the
// server stamps the name and the live version it validated.
type PlanAutomation struct {
	ID      string         `json:"id"`
	Name    string         `json:"name,omitempty"`
	Version string         `json:"version,omitempty"`
	Inputs  map[string]any `json:"inputs,omitempty"`
}

// automationRefPrefix opens the reference of an automation step: what a command step's command is everywhere a step is
// identified (run_step, the decision's ref and approval key, the plan's option of a chat run).
const automationRefPrefix = "automation:"

// stepRef is how a step is identified when it runs: its command, or automation:<workflow id>.
func stepRef(a RemediationAction) string {
	if a.Automation != nil {
		return automationRefPrefix + strings.TrimSpace(a.Automation.ID)
	}
	return strings.TrimSpace(a.ExecuteCommand)
}

// automationRefID is the workflow id an automation step's reference names.
func automationRefID(ref string) (string, bool) {
	id, ok := strings.CutPrefix(strings.TrimSpace(ref), automationRefPrefix)
	return strings.TrimSpace(id), ok && strings.TrimSpace(id) != ""
}

// automationLine is how an automation step reads on a card: its name and inputs.
func automationLine(a PlanAutomation) string {
	text := fmt.Sprintf("%q", firstNonEmpty(strings.TrimSpace(a.Name), a.ID))
	if inputs := automationInputsText(a.Inputs); inputs != "" {
		text += " with " + inputs
	}
	return text
}

// automationInputsOf is the inputs a's automation runs with; nil for a step that runs none.
func automationInputsOf(a RemediationAction) map[string]any {
	if a.Automation == nil {
		return nil
	}
	return a.Automation.Inputs
}

// automationInputsText is "a=1, b=x", sorted by name.
func automationInputsText(inputs map[string]any) string {
	keys := make([]string, 0, len(inputs))
	for k := range inputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := inputs[k]
		text, ok := v.(string)
		if !ok {
			b, _ := json.Marshal(v)
			text = string(b)
		}
		parts = append(parts, k+"="+text)
	}
	return strings.Join(parts, ", ")
}

// planAutomations are the automations an action may run on accountId: the cached list the automation tools come from.
// A var so tests can stub it.
var planAutomations = func(accountId string) []tools.AutomationOption {
	var out []tools.AutomationOption
	for _, a := range tools.AccountAutomations(accountId) {
		if !a.HasApprovalStep {
			out = append(out, a)
		}
	}
	return out
}

// checkPlanAutomations drops every automation step that may not run as written: an automation that is not among
// automations (or that stops for an approval of its own), inputs that are missing or not declared, a kind other
// than fix or mitigation, or a step that also carries a command or a change. A plan an agent wrote is then refused
// with the reason (writtenPlanRefusal), so a stored plan never carries one. A kept step gets the automation's name
// and live version.
func checkPlanAutomations(plan *RemediationPlan, automations []tools.AutomationOption) []planValidationNote {
	var notes []planValidationNote
	kept := plan.Actions[:0]
	offered := map[string]bool{}
	for i := range plan.Actions {
		a := &plan.Actions[i]
		if a.Automation == nil {
			kept = append(kept, *a)
			continue
		}
		reason := planAutomationRefusal(a, automations)
		if reason == "" && offered[a.Automation.ID] {
			// A step is identified by its automation (stepRef): a second step of the same automation could not be
			// approved apart from the first.
			reason = fmt.Sprintf("automation %q is already a step of this plan; offer each automation once", a.Automation.Name)
		}
		if reason == "" {
			offered[a.Automation.ID] = true
		}
		if reason == "" {
			kept = append(kept, *a)
			continue
		}
		name := firstNonEmpty(a.Action, a.Title)
		plan.Dropped = append(plan.Dropped, DroppedAction{Action: name, Reason: reason})
		notes = append(notes, planValidationNote{Action: name, Rule: "automation_dropped", Detail: reason})
	}
	plan.Actions = kept
	return notes
}

// planAutomationRefusal says why a's automation step may not be stored, or "" (and stamps its name and version).
func planAutomationRefusal(a *RemediationAction, automations []tools.AutomationOption) string {
	if strings.TrimSpace(a.ExecuteCommand) != "" || len(a.ExecuteArgv) > 0 || a.RequiredChange != nil || a.AlertRuleChange != nil {
		return "a step runs either an automation or a command (or is a change a person makes), not both: put the automation in a step of its own"
	}
	kind := strings.ToLower(strings.TrimSpace(a.Kind))
	if kind != RemediationKindFix && kind != RemediationKindMitigation {
		return "an automation is offered only as the fix or a mitigation for the cause (kind fix or mitigation), never to diagnose"
	}
	option, found := automationOf(automations, a.Automation.ID)
	if !found {
		return fmt.Sprintf("automation %q is not one of the account's automations NudgeBee may run: use an id from the list of the account's automations", a.Automation.ID)
	}
	if reason := automationInputsRefusal(option, a.Automation.Inputs); reason != "" {
		return reason
	}
	a.Automation.ID, a.Automation.Name, a.Automation.Version = option.ID, option.Name, option.Version
	return ""
}

// automationDeclares reports whether option declares the input id.
func automationDeclares(option tools.AutomationOption, id string) bool {
	for _, in := range option.Inputs {
		if in.ID == id {
			return true
		}
	}
	return false
}

// automationOf finds the automation with id among automations.
func automationOf(automations []tools.AutomationOption, id string) (tools.AutomationOption, bool) {
	id = strings.TrimSpace(id)
	for _, a := range automations {
		if id != "" && a.ID == id {
			return a, true
		}
	}
	return tools.AutomationOption{}, false
}

// automationInputsRefusal says why inputs cannot run option, or "": every input it requires is given, and every input
// given is one it declares.
func automationInputsRefusal(option tools.AutomationOption, inputs map[string]any) string {
	declared := map[string]bool{}
	var names []string
	for _, in := range option.Inputs {
		if in.ID != "" {
			declared[in.ID] = true
			names = append(names, in.ID)
		}
	}
	var missing, unknown []string
	for _, id := range option.RequiredInputs() {
		v, ok := inputs[id]
		if s, isText := v.(string); !ok || v == nil || (isText && strings.TrimSpace(s) == "") {
			missing = append(missing, id)
		}
	}
	for k := range inputs {
		if !declared[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(unknown)
	sort.Strings(names)
	switch {
	case len(missing) > 0:
		return fmt.Sprintf("automation %q needs the inputs %s", option.Name, strings.Join(missing, ", "))
	case len(unknown) > 0:
		accepts := "it takes no inputs"
		if len(names) > 0 {
			accepts = "it takes " + strings.Join(names, ", ")
		}
		return fmt.Sprintf("automation %q has no input %s (%s)", option.Name, strings.Join(unknown, ", "), accepts)
	}
	return ""
}

// automationCatalogText is the planning turn's list of the automations a step may run, "" when there are none: a plan
// written without it never names one.
func automationCatalogText(automations []tools.AutomationOption) string {
	if len(automations) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## The account's own automations\n")
	b.WriteString("The team keeps these automations and allowed NudgeBee to run them. When one of them is the fix, or a mitigation, " +
		"for the cause the investigation established, offer it as a step instead of a command you write: " +
		`"automation": {"id": "<id>", "inputs": {...}}, with an empty execute_command and no required_change, kind "fix" or "mitigation", and in trade_off what it gives up. ` +
		"Give every input it requires, and only inputs it declares, with real values. Never offer one to diagnose or collect information. " +
		"It makes a change on the account; it runs only after a person approves it, as that person.\n")
	for _, a := range automations {
		fmt.Fprintf(&b, "\n- id %s: %q. %s", a.ID, a.Name, strings.TrimSpace(a.Description))
		if len(a.Inputs) == 0 {
			b.WriteString("\n  Inputs: none.")
			continue
		}
		required := map[string]bool{}
		for _, id := range a.RequiredInputs() {
			required[id] = true
		}
		b.WriteString("\n  Inputs:")
		for _, in := range a.Inputs {
			if in.ID == "" {
				continue
			}
			need := "optional"
			if required[in.ID] {
				need = "required"
			}
			fmt.Fprintf(&b, "\n  - %s (%s, %s)", in.ID, firstNonEmpty(in.Type, "string"), need)
			if d := strings.TrimSpace(in.Description); d != "" {
				b.WriteString(": " + d)
			}
		}
	}
	return b.String()
}

// automationsAsApprover reads the automations the approver may run now. A var so tests can stub it.
var automationsAsApprover = func(ctx context.Context, accountId, tenantId, userId string) ([]tools.AutomationOption, error) {
	return tools.AutomationsAsUser(ctx, accountId, tenantId, userId)
}

// triggerAutomation starts one run as the approver. A var so tests can stub it.
var triggerAutomation = tools.TriggerAutomation

// createAutomationWatch creates the watch a run's wait is, automationWatchRoom says whether one can be created now, and
// cancelAutomationWatch cancels one. Vars so tests can stub them.
var (
	createAutomationWatch = func(ctx context.Context, in watch.CreateInput) (*watch.Watch, error) {
		return watch.NewManager().Create(ctx, in)
	}
	automationWatchRoom = func(ctx context.Context, tenantId string) error {
		tenant, err := uuid.Parse(tenantId)
		if err != nil {
			return fmt.Errorf("tenant: %w", err)
		}
		return watch.NewManager().OriginRoomLeft(ctx, tenant)
	}
	cancelAutomationWatch = func(ctx context.Context, tenantId string, watchId uuid.UUID) error {
		tenant, err := uuid.Parse(tenantId)
		if err != nil {
			return fmt.Errorf("tenant: %w", err)
		}
		return watch.NewManager().Cancel(ctx, tenant, watchId)
	}
)

// How a run's wait polls, and how long it waits before the step is reported as still running. Vars so tests can
// change them.
var (
	automationPollSeconds = 30
	automationWaitMinutes = 60
)

// moduleWorkflows is the module whose Execute grant runs an automation.
const moduleWorkflows = "workflows"

// automationApprovalRefusal says why the approver may not run plan step a's automation now, or "": checked as that
// person, against the automation as it is now, before anything is recorded.
func automationApprovalRefusal(ctx *security.RequestContext, accountId string, a PlanAutomation) (tools.AutomationOption, int, string) {
	sc := ctx.GetSecurityContext()
	if sc == nil || (!sc.HasAccountAccess(accountId, security.SecurityAccessTypeCreate) && !grantedRun(sc, accountId, moduleWorkflows)) {
		return tools.AutomationOption{}, 403, "remediation: running this automation needs permission to run the account's automations, so nothing was run"
	}
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx.GetContext()), decisionDBTimeout)
	defer cancel()
	current, err := automationsAsApprover(readCtx, accountId, sc.GetTenantId(), sc.GetUserId())
	if err != nil {
		ctx.GetLogger().Warn("remediation: automation not re-read before its run", "error", err)
		return tools.AutomationOption{}, 503, "remediation: the automation could not be checked, so nothing was run; try again"
	}
	option, found := automationOf(current, a.ID)
	name := firstNonEmpty(a.Name, a.ID)
	switch {
	case !found:
		return option, 409, fmt.Sprintf("remediation: the automation %q can no longer be run by NudgeBee (paused, removed, or not enabled for the AI assistant), so nothing was run. Ask for new fixes.", name)
	case option.HasApprovalStep:
		return option, 409, fmt.Sprintf("remediation: the automation %q now stops for an approval of its own, which NudgeBee does not run from a fix. Run it from Automations, or ask for new fixes.", name)
	case a.Version != "" && option.Version != a.Version:
		return option, 409, fmt.Sprintf("remediation: the automation %q changed since these fixes were written, so nothing was run. Ask for new fixes.", name)
	}
	if reason := automationInputsRefusal(option, a.Inputs); reason != "" {
		return option, 409, "remediation: the automation's inputs changed since these fixes were written (" + reason + "), so nothing was run. Ask for new fixes."
	}
	return option, 0, ""
}

// runAutomationStep starts the automation of an approved plan step once, as the approver: the decision is recorded
// before the run is started, and only the request that recorded it starts it. The row stays InProgress while the run
// goes on; its watch's end settles it (settleAutomationRuns).
func runAutomationStep(ctx *security.RequestContext, request RemediationExecuteRequest, stored storedPlan, ref, userId string) stepAnswer {
	step := planStep(stored.Plan, RemediationSlotExecute, ref, "")
	index := stepOption(step) - 1
	if index < 0 || index >= len(stored.Plan.Actions) || stored.Plan.Actions[index].Automation == nil {
		return refuse(400, "remediation: this automation is not a step of the event's plan")
	}
	automation := *stored.Plan.Actions[index].Automation
	option, code, reason := automationApprovalRefusal(ctx, request.AccountId, automation)
	if reason != "" {
		return refuse(code, reason)
	}
	// The run's wait lives in the alert's conversation: without one, nothing is started that NudgeBee cannot follow.
	conversation, err := alertConversationOfEvent(ctx, request.AccountId, request.EventId)
	if err != nil {
		ctx.GetLogger().Warn("remediation: alert conversation not found for an automation step", "error", err)
		return refuse(409, "remediation: this alert has no conversation to follow the automation's run in, so nothing was run. Ask for the fix in the alert's chat.")
	}
	// The run's wait takes one of NudgeBee's own watches: checked before the run starts, so a full tenant starts nothing
	// it cannot follow (best effort; a race still settles Unknown with the run's link below).
	roomCtx, cancelRoom := context.WithTimeout(context.WithoutCancel(ctx.GetContext()), decisionDBTimeout)
	err = automationWatchRoom(roomCtx, ctx.GetSecurityContext().GetTenantId())
	cancelRoom()
	if err != nil {
		ctx.GetLogger().Warn("remediation: no room for an automation run's wait", "error", err)
		return refuse(503, "remediation: NudgeBee is already following as many runs and checks as it can on this account, so nothing was run; try again in a few minutes")
	}
	detached := context.WithoutCancel(ctx.GetContext())
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return decisionFailure(ctx.GetLogger(), nil, err)
	}
	repo := events.NewEventAnalysisRepository(dbManager)
	surface, auditSource := decisionSurface(request.Source)
	runId, conflict, err := claimStepDecision(ctx, repo, request, userId, stored, step, ref, ref, events.RunStatusInProgress, "Starting from "+surface,
		map[string]any{"command": ref, "slot": RemediationSlotExecute, "kind": events.RunKindAutomation, "workflow_id": automation.ID,
			"name": automation.Name, "version": automation.Version, "inputs": automation.Inputs})
	if err != nil || conflict != nil {
		return decisionFailure(ctx.GetLogger(), conflict, err)
	}
	ctx = ctx.WithContext(detached)
	logger := ctx.GetLogger().With("run_id", runId, "workflow_id", automation.ID)
	sc := ctx.GetSecurityContext()
	inputs := map[string]any{}
	for k, v := range automation.Inputs {
		inputs[k] = v
	}
	// Tags the run with the alert's event, as the automation tool does, so it shows in the event's automation history;
	// an automation that declares an input of that name keeps its own value.
	if !automationDeclares(option, "event") {
		inputs["event"] = map[string]any{"id": request.EventId}
	}
	sendCtx, cancelSend := context.WithTimeout(detached, decisionDBTimeout)
	executionId, err := triggerAutomation(sendCtx, request.AccountId, sc.GetTenantId(), userId, automation.ID, inputs)
	cancelSend()
	var notStarted *tools.AutomationNotStartedError
	switch {
	case errors.As(err, &notStarted):
		writeRemediationAudit(ctx, request.AccountId, ref, "automation", false, audit.EventActionCreate, auditSource)
		return settleAutomationStart(ctx, repo, request, runId, events.RunStatusFailed, "Not run: "+notStarted.Reason,
			map[string]any{"success": false, "not_sent": true, "error": truncateForRecord(notStarted.Reason)}, "")
	case err != nil:
		logger.Warn("remediation: automation run start not confirmed", "error", err)
		writeRemediationAudit(ctx, request.AccountId, ref, "automation", false, audit.EventActionCreate, auditSource)
		message := "No answer came back from Automations. The automation may have started: " + truncateForRecord(err.Error())
		return settleAutomationStart(ctx, repo, request, runId, events.RunStatusUnknown, message,
			map[string]any{"success": false, "error": truncateForRecord(err.Error())}, "")
	}
	writeRemediationAudit(ctx, request.AccountId, ref, "automation", true, audit.EventActionCreate, auditSource)
	link := tools.AutomationRunURL(request.AccountId, automation.ID, executionId)
	watchId, err := watchAutomationRun(ctx, request.AccountId, conversation, userId, automation, executionId)
	if err != nil {
		logger.Error("remediation: automation run started but its wait was not set up", "execution_id", executionId, "error", err)
		message := "The automation started, but NudgeBee could not follow the run to its end. Check the run: " + link
		return settleAutomationStart(ctx, repo, request, runId, events.RunStatusUnknown, message,
			map[string]any{"success": false, "execution_id": executionId, "url": link, "error": truncateForRecord(err.Error())}, link)
	}
	writeCtx, cancelWrite := context.WithTimeout(detached, decisionDBTimeout)
	set, err := events.SetAutomationRunWatch(writeCtx, dbManager.Db, runId, executionId, link, watchId.String(),
		fmt.Sprintf("Running from %s: automation %q", surface, firstNonEmpty(automation.Name, automation.ID)))
	cancelWrite()
	if err != nil || !set {
		// Without the watch on the row nothing settles it from the run's end: give the wait up and say so now.
		logger.Error("remediation: automation run's wait not recorded", "execution_id", executionId, "watch_id", watchId.String(), "set", set, "error", err)
		cancelCtx, cancelCancel := context.WithTimeout(detached, decisionDBTimeout)
		if cerr := cancelAutomationWatch(cancelCtx, sc.GetTenantId(), watchId); cerr != nil {
			logger.Warn("remediation: unused automation wait not cancelled", "watch_id", watchId.String(), "error", cerr)
		}
		cancelCancel()
		message := "The automation started, but NudgeBee could not follow the run to its end. Check the run: " + link
		return settleAutomationStart(ctx, repo, request, runId, events.RunStatusUnknown, message,
			map[string]any{"success": false, "execution_id": executionId, "url": link}, link)
	}
	syncAlertThread(ctx, request.AccountId, request.EventId)
	return stepAnswer{Code: 200, Data: runResult{Status: events.RunStatusInProgress, RunId: runId, AutomationURL: link,
		RemediationExecutionResult: tools.RemediationExecutionResult{Command: ref}}}
}

// settleAutomationStart settles a run whose automation did not start, or whose wait could not be set up.
func settleAutomationStart(ctx *security.RequestContext, repo *events.EventAnalysisRepository, request RemediationExecuteRequest, runId, status, message string,
	patch map[string]any, link string) stepAnswer {
	patch["settled_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx.GetContext()), decisionDBTimeout)
	settled, err := repo.SettleRemediationRun(ctx.WithContext(settleCtx), runId, status, patch, message)
	cancel()
	if err != nil || !settled {
		ctx.GetLogger().Error("remediation: automation run not settled", "run_id", runId, "observed", status, "settled", settled, "error", err)
		status, message = events.RunStatusUnknown, runNotSettledMessage
	}
	syncAlertThread(ctx, request.AccountId, request.EventId)
	result := runResult{Status: status, RunId: runId, AutomationURL: link}
	result.Error = message
	return stepAnswer{Code: 200, Data: result}
}

// watchAutomationRun sets up the wait for a run to end: a watch on Nubi's watcher, read as the approver, in the
// alert's conversation (NudgeBee's own watch: it posts nothing there).
func watchAutomationRun(ctx *security.RequestContext, accountId string, conversation uuid.UUID, userId string, automation PlanAutomation, executionId string) (uuid.UUID, error) {
	tenant, err := uuid.Parse(ctx.GetSecurityContext().GetTenantId())
	if err != nil {
		return uuid.Nil, fmt.Errorf("tenant: %w", err)
	}
	account, err := uuid.Parse(accountId)
	if err != nil {
		return uuid.Nil, fmt.Errorf("account: %w", err)
	}
	user, err := uuid.Parse(userId)
	if err != nil {
		return uuid.Nil, fmt.Errorf("approver: %w", err)
	}
	cfg, _ := json.Marshal(watch.AutomationRunSourceConfig{WorkflowID: automation.ID, ExecutionID: executionId, Name: automation.Name})
	createCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx.GetContext()), decisionDBTimeout)
	defer cancel()
	w, err := createAutomationWatch(createCtx, watch.CreateInput{ConversationID: conversation, AccountID: account, TenantID: tenant, UserID: &user,
		Origin: watch.OriginRemediation, SourceKind: watch.SourceKindAutomationRun, SourceConfig: cfg,
		PredicateKind: watch.PredicateKindRegex, PredicateExpr: tools.AutomationRunEndedPattern,
		PollIntervalSec: automationPollSeconds, MaxDurationSec: automationWaitMinutes * 60})
	if err != nil {
		return uuid.Nil, fmt.Errorf("the watch: %w", err)
	}
	return w.ID, nil
}

// alertConversationOfEvent is the id of the alert's conversation (event-<fingerprint>) of eventId. A var so tests can
// stub it.
var alertConversationOfEvent = func(ctx *security.RequestContext, accountId, eventId string) (uuid.UUID, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return uuid.Nil, err
	}
	info, err := events.NewEventAnalysisRepository(dbManager).GetEventInfo(ctx, eventId, accountId)
	if err != nil || info == nil {
		return uuid.Nil, fmt.Errorf("the alert's event: %w", errors.Join(err, errors.New("not found")))
	}
	id, err := alertConversationIdOf(accountId, events.SessionIdPrefixEvent+firstNonEmpty(info.Fingerprint, eventId))
	if err != nil || id == "" {
		return uuid.Nil, fmt.Errorf("the alert's conversation: %w", errors.Join(err, errors.New("none")))
	}
	return uuid.Parse(id)
}

// settleAutomationRuns settles every automation run whose watch ended (run by the follow-ups job, before follow-ups
// are claimed, so the follow-up of a settled run starts on this tick).
func settleAutomationRuns(db *sqlx.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	ended, err := events.AutomationRunsToSettle(ctx, db, waitGraceMinutes, 20)
	if err != nil {
		slog.Warn("remediation_followups: automation runs not read", "error", err)
		return
	}
	for _, run := range ended {
		status, message, patch := automationRunOutcome(run)
		// Each settle gets its own deadline: a slow one must not leave the rest unsettled.
		settleCtx, cancelSettle := context.WithTimeout(context.Background(), decisionDBTimeout)
		n, err := events.SettleAutomationRun(settleCtx, db, run.Id, status, patch, message)
		cancelSettle()
		if err != nil || n == 0 {
			if err != nil {
				slog.Warn("remediation_followups: automation run not settled", "run_id", run.Id, "error", err)
			}
			continue
		}
		syncCtx, err := systemContextFor(run.AccountId)
		if err == nil {
			syncAlertThread(syncCtx, run.AccountId, run.EventId)
		}
	}
}

// automationRunOutcome is how a run whose watch ended settles: its status, the message beside it and the facts it
// records.
func automationRunOutcome(run events.AutomationRunToSettle) (status, message string, patch map[string]any) {
	var d struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	_ = json.Unmarshal(run.Data, &d)
	var seen tools.AutomationRunObservation
	_ = json.Unmarshal([]byte(run.Observed), &seen)
	name := firstNonEmpty(d.Name, "the automation")
	link := ""
	if d.URL != "" {
		link = " Run: " + d.URL
	}
	patch = map[string]any{"run_status": seen.Status}
	switch {
	case run.WatchStatus == string(watch.StatusCompleted) && seen.Status == "COMPLETED":
		patch["success"], patch["automation_completed"] = true, true
		return events.RunStatusSuccess, fmt.Sprintf("Automation %q completed.", name), patch
	case run.WatchStatus == string(watch.StatusCompleted) && seen.WaitingApproval:
		patch["success"] = false
		return events.RunStatusUnknown, fmt.Sprintf("Automation %q is waiting on an approval of its own; approve or cancel it in Automations.%s", name, link), patch
	case run.WatchStatus == string(watch.StatusCompleted) && seen.Ended:
		patch["success"] = false
		message = fmt.Sprintf("Automation %q ended %s", name, strings.ToLower(firstNonEmpty(seen.Status, "without completing")))
		if seen.Step != "" {
			message += ", stopped at step " + seen.Step
		}
		if seen.Error != "" {
			patch["error"] = truncateForRecord(seen.Error)
			message += ": " + truncate(seen.Error, 300)
		}
		return events.RunStatusFailed, message + "." + link, patch
	case run.WatchStatus == string(watch.StatusExpired):
		patch["success"] = false
		return events.RunStatusUnknown, fmt.Sprintf("Automation %q was still running after %d minutes; NudgeBee stopped following it.%s", name, automationWaitMinutes, link), patch
	default:
		patch["success"] = false
		if run.WatchError != "" {
			patch["error"] = truncateForRecord(run.WatchError)
		}
		return events.RunStatusUnknown, fmt.Sprintf("NudgeBee could not read how automation %q ended.%s", name, link), patch
	}
}
