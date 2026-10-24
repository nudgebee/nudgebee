package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	agentcore "nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
)

// Approved writes in an alert's conversation (#40734): when the remediation loop is on, every infra
// write a person approves in the alert's chat is recorded on the alert before it runs, through the
// same claim as the remediation panel's Run (events.ClaimRemediationDecision), so it runs once and its
// outcome is recorded. Keyed by the approval it was given under: <confirmation key>:<follow-up message
// id>. Tickets, notes, memory, creating an automation and anything else that is not an infra change
// run as before, unrecorded. So does every write outside an alert's conversation (an RCA chat, any
// other Nubi chat), and every write when the loop is off.
func init() {
	agentcore.ApprovedWriteRecorder = recordApprovedChatWrite
	agentcore.ApprovalCardQuestion = chatWriteCardQuestion
}

// Labels of the writes recorded in an alert's chat: data.tool of the run row.
const (
	chatWriteToolAutomation = "automation"
	chatWriteToolShell      = "shell"
)

// chatWrite is an approved call as the alert records it.
type chatWrite struct {
	// Tool is the label the row carries: the CLI or database the command runs with, "shell", or
	// "automation".
	Tool    string
	Command string
	// ExitCodes: a clean result from this tool means the command exited 0 (the workspace reports
	// how a command ended). An automation reports no exit code.
	ExitCodes bool
}

// chatWriteCLITools are the CLI tools whose approved writes are recorded, by tool name, with their
// label when the command names no CLI of its own.
var chatWriteCLITools = map[string]string{
	tools.ToolExecuteKubectlCommand:  "kubectl",
	tools.ToolExecuteHelmCommand:     "helm",
	tools.ToolExecuteArgoCDCommand:   "argocd",
	tools.ToolExecuteAwsCliCommand:   "aws",
	tools.ToolExecuteAzureCliCommand: "az",
	tools.ToolExecuteGcpCliCommand:   "gcloud",
	toolcore.ToolExecuteShellCommand: chatWriteToolShell,
	tools.ToolExecuteServerCommand:   chatWriteToolShell,
	tools.ToolVMExecute:              chatWriteToolShell,
}

// chatWriteDBTools are the database clients whose approved writes are recorded.
var chatWriteDBTools = map[string]string{
	tools.ToolExecutePostgresQuery:   "postgres",
	tools.ToolExecuteMySQLQuery:      "mysql",
	tools.ToolExecuteMSSQLQuery:      "mssql",
	tools.ToolExecuteOracleQuery:     "oracle",
	tools.ToolExecuteClickhouseQuery: "clickhouse",
	tools.ToolExecuteRedisCommand:    "redis",
	tools.ToolExecuteRabbitCommand:   "rabbitmq",
}

// knownCLIs label a shell command by the CLI it runs.
var knownCLIs = map[string]bool{"kubectl": true, "helm": true, "argocd": true, "aws": true, "az": true,
	"gcloud": true, "gsutil": true, "bq": true, "gh": true, "git": true}

// automationToolPrefix is the name prefix of a saved automation exposed as a tool
// (tools/tool_workflow_automation.go).
const automationToolPrefix = "automation_"

// chatWriteOf is how an approved call of tool with input is recorded, and whether it is: only infra
// writes are. github_execute only for the gh calls that change something (ghChanges).
func chatWriteOf(tool, input string) (chatWrite, bool) {
	tool = strings.TrimSpace(tool)
	switch {
	case chatWriteCLITools[tool] != "":
		command := strings.TrimSpace(toolcore.ParseNBToolCallRequestFromInput(input).Command)
		if command == "" {
			return chatWrite{}, false
		}
		label := chatWriteCLITools[tool]
		if first := firstWord(command); knownCLIs[first] && (label == chatWriteToolShell || tool == tools.ToolExecuteGcpCliCommand) {
			label = first
		}
		return chatWrite{Tool: label, Command: command, ExitCodes: true}, true
	case chatWriteDBTools[tool] != "":
		command := strings.TrimSpace(agentcore.ConfirmationDisplayInput(input))
		if parsed := strings.TrimSpace(toolcore.ParseNBToolCallRequestFromInput(command).Command); parsed != "" {
			command = parsed
		}
		if command == "" {
			return chatWrite{}, false
		}
		return chatWrite{Tool: chatWriteDBTools[tool], Command: command, ExitCodes: true}, true
	case tool == tools.ToolExecuteGithubCliCommand:
		command := strings.TrimSpace(toolcore.ParseNBToolCallRequestFromInput(input).Command)
		if !ghChanges(command) {
			return chatWrite{}, false
		}
		return chatWrite{Tool: firstWord(command), Command: command, ExitCodes: true}, true
	case strings.HasPrefix(tool, automationToolPrefix), tool == tools.ToolWorkflowTrigger:
		return chatWrite{Tool: chatWriteToolAutomation, Command: strings.TrimSpace(tool + " " + compactJSON(input))}, true
	}
	return chatWrite{}, false
}

// ghChanges reports whether a gh (or git) command changes something on the repository's host: a
// merge, a workflow run or re-run, a release, a repository change, or a push. Reads, and issue or PR
// comments (notes), are not infra writes.
func ghChanges(command string) bool {
	words := positionalWords(command)
	if len(words) < 2 {
		return false
	}
	switch words[0] {
	case "git":
		return words[1] == "push"
	case "gh":
	default:
		return false
	}
	sub := ""
	if len(words) > 2 {
		sub = words[2]
	}
	switch words[1] {
	case "pr":
		return sub == "merge"
	case "workflow":
		return sub == "run" || sub == "enable" || sub == "disable"
	case "run":
		return sub == "rerun" || sub == "cancel"
	case "release":
		return sub != "" && sub != "list" && sub != "view" && sub != "download"
	case "repo":
		return sub != "" && sub != "list" && sub != "view" && sub != "clone"
	case "api":
		return ghAPIPushes(command)
	}
	return false
}

// ghAPIPushes reports whether a gh api call writes code, refs or a merge, or dispatches a workflow.
func ghAPIPushes(command string) bool {
	fields := strings.Fields(command)
	method := ""
	for i, f := range fields {
		switch {
		case (f == "-X" || f == "--method") && i+1 < len(fields):
			method = strings.ToUpper(strings.Trim(fields[i+1], `"'`))
		case strings.HasPrefix(f, "--method="):
			method = strings.ToUpper(strings.Trim(strings.TrimPrefix(f, "--method="), `"'`))
		}
	}
	if method != "POST" && method != "PUT" && method != "PATCH" && method != "DELETE" {
		return false
	}
	for _, path := range []string{"/git/refs", "/contents/", "/merges", "/merge", "/dispatches"} {
		if strings.Contains(command, path) {
			return true
		}
	}
	return false
}

// positionalWords are a command's words that are not flags (flag values may remain).
func positionalWords(command string) []string {
	var out []string
	for _, f := range strings.Fields(command) {
		if !strings.HasPrefix(f, "-") {
			out = append(out, strings.Trim(f, `"'`))
		}
	}
	return out
}

func firstWord(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// compactJSON is input as compact JSON when it is JSON, else trimmed.
func compactJSON(input string) string {
	var v any
	if err := json.Unmarshal([]byte(input), &v); err != nil {
		return strings.TrimSpace(input)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return strings.TrimSpace(input)
	}
	return string(b)
}

// commandTool is the CLI a plan command runs with, as a chat write's data.tool names it; "shell" for
// any other command, "" for none.
func commandTool(command string) string {
	first := firstWord(command)
	switch {
	case first == "":
		return ""
	case knownCLIs[first]:
		return first
	}
	return chatWriteToolShell
}

// chatStepApproval is the approval a plan step decided in the alert's chat (run_step) was given under.
type chatStepApproval struct {
	// ApprovalKey is <confirmation key>:<follow-up message id> (C1).
	ApprovalKey string
	ApprovedBy  string
}

// chatStepApprovalOf is the approval a run_step call runs under when the loop is on, or nil: the call
// is then recorded as before.
func chatStepApprovalOf(ctx toolcore.NbToolContext, userId string) *chatStepApproval {
	approval, ok := agentcore.CallApproval(ctx.Ctx)
	if !ok || strings.TrimSpace(approval.Ref.MessageId) == "" || !events.RemediationLoopOn(ctx.AccountId) {
		return nil
	}
	return &chatStepApproval{
		ApprovalKey: chatWriteApprovalKey(approval.ConfirmationKey, approval.Ref.MessageId),
		ApprovedBy:  firstNonEmpty(strings.TrimSpace(approval.Ref.ApprovedBy), userId),
	}
}

// chatWriteApprovalKey is the approval a chat write is recorded under (C1).
func chatWriteApprovalKey(confirmationKey, followupMessageId string) string {
	return confirmationKey + ":" + followupMessageId
}

// ruleWriteApprovalKey is the approval a write a rule approved is recorded under: the rule's decision for
// that one call (rule, revision, message, agent, tool call), so a resume of the call does not run it again.
func ruleWriteApprovalKey(w agentcore.ApprovedWrite) string {
	return fmt.Sprintf("%s:rule:%s:%d:%s:%s:%s", w.ConfirmationKey, w.Rule.RuleID, w.Rule.Revision, w.MessageId, w.AgentId, w.Rule.ToolCallID)
}

// ruleApproverName is how a run approved by a rule names who approved it.
func ruleApproverName(rule *agentcore.RuleApproval) string {
	if name := strings.TrimSpace(rule.RuleName); name != "" {
		return fmt.Sprintf("approval rule %q", name)
	}
	return "an approval rule"
}

// alertSessionOfConversation is the session of an alert's conversation, or "" when the conversation
// is not one (an RCA chat, any other chat) or cannot be read. A var so tests can stub it.
var alertSessionOfConversation = func(conversationId string) string {
	dao := agentcore.GetConversationDao()
	if dao == nil || conversationId == "" {
		return ""
	}
	conversation, err := dao.GetConversation(conversationId)
	if err != nil {
		return ""
	}
	if _, alert := events.AlertFingerprintOfSession(conversation.SessionID); !alert {
		return ""
	}
	return conversation.SessionID
}

// chatWriteStore is what recording a chat write reads and writes. A var so tests can stub it.
var chatWriteStore interface {
	alertEvent(ctx *security.RequestContext, accountId, session string) (string, error)
	plan(ctx *security.RequestContext, accountId, eventId string) (storedPlan, bool)
	claim(ctx *security.RequestContext, d events.RemediationDecision) (string, *events.DecisionConflict, error)
	settle(ctx *security.RequestContext, runId, status string, patch map[string]any, message string) (bool, error)
} = dbChatWriteStore{}

type dbChatWriteStore struct{}

func (dbChatWriteStore) alertEvent(ctx *security.RequestContext, accountId, session string) (string, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", fmt.Errorf("alertEvent: %w", err)
	}
	eventId, _, err := alertOfSession(dbManager.Db, accountId, session)
	if err != nil {
		return "", fmt.Errorf("alertEvent: %w", err)
	}
	return eventId, nil
}

func (dbChatWriteStore) plan(ctx *security.RequestContext, accountId, eventId string) (storedPlan, bool) {
	return loadSavedPlan(ctx, accountId, eventId)
}

func (dbChatWriteStore) claim(ctx *security.RequestContext, d events.RemediationDecision) (string, *events.DecisionConflict, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", nil, fmt.Errorf("claim: %w", err)
	}
	return events.NewEventAnalysisRepository(dbManager).ClaimRemediationDecision(ctx, d)
}

func (dbChatWriteStore) settle(ctx *security.RequestContext, runId, status string, patch map[string]any, message string) (bool, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return false, fmt.Errorf("settle: %w", err)
	}
	return events.NewEventAnalysisRepository(dbManager).SettleRemediationRun(ctx, runId, status, patch, message)
}

// chatWriteNotRecorded refuses a write whose record could not be written: nothing ran.
const chatWriteNotRecorded = "This change was not run: it could not be recorded on the alert. Tell the person, and ask them to try again shortly."

// recordApprovedChatWrite is agentcore.ApprovedWriteRecorder: it claims an approved infra write in an
// alert's conversation before it runs, and returns how to settle it. A zero claim lets the call run
// as before, unrecorded.
func recordApprovedChatWrite(ctx *security.RequestContext, w agentcore.ApprovedWrite) agentcore.ApprovedWriteClaim {
	if w.Tool == toolcore.ToolRunStep {
		// run_step records its own decision, under this approval (chatStepApprovalOf); an approval it
		// cannot be recorded under is asked for again. Under a rule it records its step as it always did.
		if w.Rule == nil && strings.TrimSpace(w.Ref.MessageId) == "" && events.RemediationLoopOn(w.AccountId) && alertSessionOfConversation(w.ConversationId) != "" {
			return agentcore.ApprovedWriteClaim{Reask: true}
		}
		return agentcore.ApprovedWriteClaim{}
	}
	write, ok := chatWriteOf(w.Tool, w.Input)
	if !ok || !events.RemediationLoopOn(w.AccountId) {
		return agentcore.ApprovedWriteClaim{}
	}
	if target := strings.TrimSpace(w.TargetAccountId); target != "" && target != w.AccountId {
		// A write on another cluster or account is not an attempt on this alert: it must not take
		// this alert's command lock, verdict or resolve. It runs as before, unrecorded here.
		return agentcore.ApprovedWriteClaim{}
	}
	session := alertSessionOfConversation(w.ConversationId)
	if session == "" {
		return agentcore.ApprovedWriteClaim{}
	}
	logger := ctx.GetLogger().With("tool", w.Tool, "confirmation_key", w.ConfirmationKey)
	approvalKey, approver := "", ""
	switch {
	case w.Rule != nil:
		// A rule approved this one call: recorded under the rule's decision for it, in the rule's name.
		approvalKey = ruleWriteApprovalKey(w)
		approver = ruleApproverName(w.Rule)
	case strings.TrimSpace(w.Ref.MessageId) == "":
		// Which approval this is is not known, so it cannot be recorded under it: ask for one.
		logger.Info("remediation: approved chat write has no known approval, asking again")
		return agentcore.ApprovedWriteClaim{Reask: true}
	default:
		approvalKey = chatWriteApprovalKey(w.ConfirmationKey, w.Ref.MessageId)
		approver = strings.TrimSpace(w.Ref.ApprovedBy)
		if sc := ctx.GetSecurityContext(); approver == "" && sc != nil {
			approver = strings.TrimSpace(sc.GetUserId())
		}
	}
	// The person approved; nothing from here may be cancelled by the request going away.
	detached := ctx.WithContext(context.WithoutCancel(ctx.GetContext()))
	readCtx, cancelRead := context.WithTimeout(detached.GetContext(), decisionDBTimeout)
	eventId, err := chatWriteStore.alertEvent(detached.WithContext(readCtx), w.AccountId, session)
	cancelRead()
	if err != nil {
		logger.Error("remediation: alert of an approved chat write not found, not running it", "error", err)
		return agentcore.ApprovedWriteClaim{Refusal: chatWriteNotRecorded}
	}
	data := map[string]any{
		"source":      events.DecisionSourceChat,
		"tool":        write.Tool,
		"command":     write.Command,
		"approved_by": approver,
		"slot":        RemediationSlotExecute,
	}
	userId := approver
	if w.Rule != nil {
		data["approved_by_rule"] = map[string]any{"rule_id": w.Rule.RuleID, "rule_name": w.Rule.RuleName, "rule_revision": w.Rule.Revision}
		userId = "rule:" + w.Rule.RuleID
		if sc := ctx.GetSecurityContext(); sc != nil {
			data["requested_by"] = strings.TrimSpace(sc.GetUserId())
		}
	}
	stepKey := ""
	// A write a rule approved is an attempt, not a decided plan step: no person chose that step.
	if stored, ok := chatWriteStore.plan(detached, w.AccountId, eventId); ok && w.Rule == nil {
		if option, slot, found := planOptionOf(stored.Plan, write.Command); found {
			// A plan step run from chat decides that step, as run_step does: the step's own key goes
			// beside the approval's, so the step does not run again from the panel or Slack.
			step := fmt.Sprintf("%d:apply", option-1)
			if slot == RemediationSlotRollback {
				step = fmt.Sprintf("%d:rollback", option-1)
			}
			data["plan_version"], data["option"], data["slot"], data["step"] = stored.Version, option, slot, step
			stepKey = events.ApprovalKey(w.AccountId, eventId, stored.Version, step, write.Command)
		}
	}
	claimCtx, cancelClaim := context.WithTimeout(detached.GetContext(), decisionDBTimeout)
	runId, conflict, err := chatWriteStore.claim(detached.WithContext(claimCtx), events.RemediationDecision{
		AccountId:     w.AccountId,
		EventId:       eventId,
		UserId:        userId,
		Ref:           write.Command,
		ApprovalKey:   approvalKey,
		StepKey:       stepKey,
		Status:        events.RunStatusInProgress,
		Data:          data,
		StatusMessage: "Running from the alert's chat",
	})
	cancelClaim()
	if err != nil {
		logger.Error("remediation: approved chat write not recorded, not running it", "error", err)
		return agentcore.ApprovedWriteClaim{Refusal: chatWriteNotRecorded}
	}
	if conflict != nil {
		logger.Info("remediation: approved chat write refused", "reason", conflict.Reason, "row_id", conflict.RowId)
		return chatWriteConflict(conflict, write.Command)
	}
	logger.Info("remediation: approved chat write recorded", "run_id", runId, "event_id", eventId)
	return agentcore.ApprovedWriteClaim{Settle: func(resp toolcore.NBToolResponse, callErr error) {
		settleChatWrite(detached, runId, write, resp, callErr)
	}}
}

// chatWriteConflict answers a claim that was refused. An approval another command already used is
// asked for again: one approval runs one write.
func chatWriteConflict(c *events.DecisionConflict, command string) agentcore.ApprovedWriteClaim {
	switch c.Reason {
	case events.DecisionAlreadyDecided:
		if strings.TrimSpace(c.Ref) != strings.TrimSpace(command) {
			return agentcore.ApprovedWriteClaim{Reask: true}
		}
		return agentcore.ApprovedWriteClaim{Refusal: "This change was already run or decided for this alert (under this approval, or as the same step of the plan), so it was not run again. Report what happened; running it again needs a new plan or approval."}
	case events.DecisionWrongTarget:
		return agentcore.ApprovedWriteClaim{Refusal: "This change was not run: the alert does not belong to this account."}
	}
	return agentcore.ApprovedWriteClaim{Refusal: "This change was not run. " + strings.TrimPrefix(decisionConflictMessage(c), "remediation: ")}
}

// chatWriteOutcome is how an approved chat write ended, from what its tool returned.
func chatWriteOutcome(write chatWrite, resp toolcore.NBToolResponse, callErr error) (status string, patch map[string]any, message string) {
	sent := sentCommand{Err: callErr}
	var exitErr *tools.RelayCommandExitError
	notRun := false
	switch {
	case callErr == nil && resp.Status == toolcore.NBToolResponseStatusError:
		// The tool answered with a failure of its own and no exit code.
		status = events.RunStatusFailed
	case callErr == nil:
		sent.ExitCodeReported = write.ExitCodes
		status, _ = runSettlement(sent)
	default:
		if code, ok := workspace.ExitCodeFromFailure(callErr); ok {
			sent.ExitCode, sent.ExitCodeReported = code, true
		} else if errors.As(callErr, &exitErr) {
			sent.ExitCode, sent.ExitCodeReported = exitErr.ExitCode, true
		} else if errors.Is(callErr, workspace.ErrWorkspaceCommandFailed) {
			// The workspace refused it before running it (workspace.ExitCodeFromFailure).
			notRun = true
		}
		status, notRun = settleStatus(sent, notRun)
	}
	output := resp.Data
	patch = map[string]any{
		"success":            status == events.RunStatusSuccess,
		"exit_code_reported": sent.ExitCodeReported,
		"output":             truncateForRecord(output),
	}
	if sent.ExitCodeReported {
		patch["exit_code"] = sent.ExitCode
	}
	if notRun {
		patch["not_sent"] = true
	}
	switch {
	case callErr != nil:
		patch["error"] = truncateForRecord(callErr.Error())
		message = runStatusMessage(status, notRun, sent, "the alert's chat")
	case status == events.RunStatusFailed:
		patch["error"] = truncateForRecord(output)
		message = "Ran from the alert's chat and failed"
	case sent.ExitCodeReported:
		message = runStatusMessage(status, false, sent, "the alert's chat")
	default:
		message = "Ran from the alert's chat"
	}
	return status, patch, message
}

// settleStatus is runSettlement, with a run the workspace refused before running it recorded as not run.
func settleStatus(sent sentCommand, notRun bool) (string, bool) {
	if notRun {
		return events.RunStatusFailed, true
	}
	return runSettlement(sent)
}

// settleChatWrite records how a claimed chat write ended. A record that cannot be settled stays
// InProgress until the reaper marks it Unknown.
func settleChatWrite(ctx *security.RequestContext, runId string, write chatWrite, resp toolcore.NBToolResponse, callErr error) {
	status, patch, message := chatWriteOutcome(write, resp, callErr)
	settleCtx, cancel := context.WithTimeout(ctx.GetContext(), decisionDBTimeout)
	defer cancel()
	settled, err := chatWriteStore.settle(ctx.WithContext(settleCtx), runId, status, patch, message)
	if err != nil || !settled {
		ctx.GetLogger().Error("remediation: approved chat write not settled", "run_id", runId, "observed", status, "settled", settled, "error", err)
	}
}

// planOptionOf is the option (1-based) of the plan whose command, or undo command, is command.
func planOptionOf(plan RemediationPlan, command string) (option int, slot string, found bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		return 0, "", false
	}
	for i, a := range plan.Actions {
		if strings.TrimSpace(a.ExecuteCommand) == command {
			return i + 1, RemediationSlotExecute, true
		}
	}
	for i, a := range plan.Actions {
		if strings.TrimSpace(a.RollbackCommand) == command {
			return i + 1, RemediationSlotRollback, true
		}
	}
	return 0, "", false
}

// chatWriteCardQuestion is agentcore.ApprovalCardQuestion: in an alert's conversation with the loop
// on, an infra write's (or a plan step's) approval card says which option of the plan it is, where it
// runs and the exact command, instead of the tool's raw input. "" keeps the default card.
func chatWriteCardQuestion(ctx *security.RequestContext, w agentcore.ApprovedWrite) string {
	command, label, step, undo, ok := cardSubject(w)
	if !ok || !events.RemediationLoopOn(w.AccountId) {
		return ""
	}
	session := alertSessionOfConversation(w.ConversationId)
	if session == "" {
		return ""
	}
	where := strings.TrimSpace(w.Destination)
	if where == "" {
		readCtx, cancel := context.WithTimeout(ctx.GetContext(), decisionDBTimeout)
		if target, err := remediationTarget(ctx.WithContext(readCtx), w.AccountId); err == nil && target.AccountName != "" {
			where = "**" + target.AccountName + "**"
		}
		cancel()
	}
	option := ""
	readCtx, cancel := context.WithTimeout(ctx.GetContext(), decisionDBTimeout)
	defer cancel()
	if eventId, err := chatWriteStore.alertEvent(ctx.WithContext(readCtx), w.AccountId, session); err == nil {
		if stored, found := chatWriteStore.plan(ctx.WithContext(readCtx), w.AccountId, eventId); found {
			option = planOptionText(stored.Plan, command, step, undo)
		}
	}
	return approvalCardText(label, where, option, command)
}

// cardSubject is what an approval card describes: the exact command and what runs it; for run_step,
// the plan step it acts on (1-based, 0 otherwise), whether it is the step's undo, and the action.
func cardSubject(w agentcore.ApprovedWrite) (command, label string, step int, undo, ok bool) {
	if w.Tool == toolcore.ToolRunStep {
		request := runStepInput(toolcore.ParseNBToolCallRequestFromInput(w.Input))
		return strings.TrimSpace(request.Command), request.Action, request.Step, request.Action == "rollback", request.Step > 0
	}
	write, ok := chatWriteOf(w.Tool, w.Input)
	return write.Command, write.Tool, 0, false, ok
}

// planOptionText names the plan option a command (or a run_step step) is, or says it is not one.
func planOptionText(plan RemediationPlan, command string, step int, undo bool) string {
	index, slot := step, ""
	if undo {
		slot = RemediationSlotRollback
	}
	if index <= 0 {
		var found bool
		if index, slot, found = planOptionOf(plan, command); !found {
			return "Not one of the plan's options."
		}
	}
	if index > len(plan.Actions) {
		return ""
	}
	a := plan.Actions[index-1]
	text := fmt.Sprintf("Option %d of the plan: %s", index, firstNonEmpty(a.Title, a.Action))
	if slot == RemediationSlotRollback {
		text += " (undo)"
	}
	return text
}

// approvalCardText is the approval card: what runs where, which option, and the exact command. The
// last line keeps the "Command - " form the audit reads the command from.
func approvalCardText(label, where, option, command string) string {
	var b strings.Builder
	b.WriteString("Approve this change")
	if where != "" {
		b.WriteString(" on " + where)
	}
	if label != "" {
		b.WriteString(" (" + label + ")")
	}
	b.WriteString("?")
	if option != "" {
		b.WriteString("\n" + option)
	}
	if command != "" {
		b.WriteString("\nCommand - " + command)
	}
	return b.String()
}
