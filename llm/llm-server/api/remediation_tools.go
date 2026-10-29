package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	agentcore "nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// The alert's tools (#40734): what the agents in an alert's conversation call to read its plan, act on a
// step, propose a different plan and, in the follow-up check, ask to look again. Each refuses outside the
// conversation or turn it is for.
func init() {
	toolcore.RegisterNBToolFactory(toolcore.ToolGetRemediationPlan, func(string) (toolcore.NBTool, error) { return GetRemediationPlanTool{}, nil })
	toolcore.RegisterNBToolFactory(toolcore.ToolRunStep, func(string) (toolcore.NBTool, error) { return RunStepTool{}, nil })
	toolcore.RegisterNBToolFactory(toolcore.ToolProposeFix, func(string) (toolcore.NBTool, error) { return ProposeFixTool{}, nil })
	toolcore.RegisterNBToolFactory(toolcore.ToolScheduleRecheck, func(string) (toolcore.NBTool, error) { return ScheduleRecheckTool{}, nil })
}

func toolErrorResponse(message string) toolcore.NBToolResponse {
	return toolcore.NBToolResponse{Data: message, Status: toolcore.NBToolResponseStatusError}
}

// GetRemediationPlanTool reads NudgeBee's own record of the alert; it changes nothing.
type GetRemediationPlanTool struct{ toolcore.ReadOnlyTool }

func (GetRemediationPlanTool) Name() string { return toolcore.ToolGetRemediationPlan }

func (GetRemediationPlanTool) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }

func (GetRemediationPlanTool) Description() string {
	return `Returns this alert's current remediation plan: the cause, each step's command and what was decided about it (run and how it ended, declined, marked done, or not decided yet), and what each follow-up check after a run found. Use it before answering a question about the fix, what was tried, or why something did not work. It takes no input; the alert is this conversation's own.`
}

func (GetRemediationPlanTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{Type: toolcore.ToolSchemaTypeObject, Properties: map[string]toolcore.ToolSchemaProperty{}}
}

func (GetRemediationPlanTool) Call(ctx toolcore.NbToolContext, _ toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	out, err := remediationPlanText(ctx)
	if err != nil {
		return toolErrorResponse(err.Error()), err
	}
	return toolcore.NBToolResponse{Data: out, Status: toolcore.NBToolResponseStatusSuccess}, nil
}

// ProposeFixTool is classified as a read: it changes NudgeBee's own plan for the alert, never anything in
// the customer's environment, and every command in the plan still needs a person's confirmation.
type ProposeFixTool struct{ toolcore.ReadOnlyTool }

func (ProposeFixTool) Name() string { return toolcore.ToolProposeFix }

func (ProposeFixTool) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }

func (ProposeFixTool) Description() string {
	return `Proposes a new remediation plan for this alert, written by you; it replaces the current plan. In a follow-up check after a remediation step: at most once, when the problem persists and you are confident about a different, safe fix. In the alert's chat: when the person asks for a different fix, after reading the current plan (get_remediation_plan).
Pass your findings (what you checked, what it showed, why the current plan does not fit) and the plan as a JSON object:
{"root_cause": "...", "summary": "...", "hypotheses": [{"hypothesis": "...", "reasoning": ["..."], "confidence": 0-100}],
 "actions": [{"action": "short name", "title": "...", "hypothesis": "the hypothesis it addresses", "kind": "fix" or "mitigation", "confidence": 0-100,
   "execute_command": "one command", "verify_command": "a read that shows it worked", "rollback_command": "how to undo it",
   "after": "the action name of the step this one runs after, if any", "trade_off": "...",
   "required_change": {"where": "code" | "deployment_source" | "infrastructure" | "dependency" | "process", "target": "the file, values key or resource", "change": "the exact change", "repo": "the repository, when known", "reason": "why it fixes the cause"}}],
 "cause_key": {"what": "...", "how": "...", "against": "..."},
 "optional_hardening": [{"target": "...", "change": "...", "reason": "..."}]}
cause_key names the cause in a few words, without numbers: what failed (kind and name), how it failed (oom, crashloop, timeout), and against what (the limit, dependency or change it hit). Write it the same way for the same cause on every firing; leave a part empty when it is not established.
A change the incident needs that no command applies durably (in code, a deployment source such as chart values or a Deployment spec, infrastructure, a dependency or a process) is a step with an empty execute_command and a required_change; never put it in optional_hardening. While the alert fires, also offer a mitigation command that buys time when one exists, kind "mitigation", stating in trade_off what it does not fix.
optional_hardening lists improvements the incident does not need to resolve (prevention), never a fix step; it may be empty.
Each command is one invocation on this alert's own account (kubectl, helm or argocd on a cluster; aws, az or gcloud on a cloud account), with real values: no placeholders, no shell syntax, no flags that choose another cluster or identity. Each execute_command makes its change itself: it never fetches a file or manifest from a URL, and a patch to a value that holds a whole document (a ConfigMap key holding JSON or YAML) passes the whole document with only your change. The summary describes exactly these commands. NudgeBee validates the plan as it validates its own; a refusal says what to correct. A person must confirm each command before it runs. The event and account are this conversation's own.`
}

func (ProposeFixTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"findings": {Type: toolcore.ToolSchemaTypeString, Description: "What you checked, what it showed, and why the current plan does not fit"},
			"plan":     {Type: toolcore.ToolSchemaTypeObject, Description: "The plan you propose, as described above"},
		},
		Required: []string{"findings", "plan"},
	}
}

func (ProposeFixTool) Call(ctx toolcore.NbToolContext, input toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	findings, _ := input.Arguments["findings"].(string)
	planJSON := ""
	switch plan := input.Arguments["plan"].(type) {
	case string:
		planJSON = plan
	case nil:
	default:
		if b, err := json.Marshal(plan); err == nil {
			planJSON = string(b)
		}
	}
	out, err := proposeFix(ctx, strings.TrimSpace(findings), strings.TrimSpace(planJSON))
	if err != nil {
		return toolErrorResponse(err.Error()), err
	}
	return toolcore.NBToolResponse{Data: out, Status: toolcore.NBToolResponseStatusSuccess}, nil
}

// ScheduleRecheckTool is classified as a read: it changes only when NudgeBee looks at the alert again.
type ScheduleRecheckTool struct{ toolcore.ReadOnlyTool }

func (ScheduleRecheckTool) Name() string { return toolcore.ToolScheduleRecheck }

func (ScheduleRecheckTool) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }

func (ScheduleRecheckTool) Description() string {
	return `Asks NudgeBee to run this follow-up check again later, during a follow-up check after a remediation step. Call it when it is too early to tell whether the step worked: for example the metric behind the alert is averaged over a window that still includes the time before the step, a rollout is still in progress, or the alert has cleared and must stay clear for a while to count as fixed. Pass how many minutes to wait at most (5 to 120) and what the next check should look at. To look again as soon as something is true in the alert's cluster (a rollout finished, all pods ready, no new restarts), also pass until_command, a kubectl read of the alert's own cluster, and until_expect, a regular expression its output must match (until_negate: until it stops matching); NudgeBee runs it every minute. Without until_command the next check runs when the minutes pass. Your current turn still ends with your report; the next check runs in this conversation. It is refused after you proposed a new plan and after the follow-up's re-checks are used up.`
}

func (ScheduleRecheckTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"minutes":       {Type: toolcore.ToolSchemaTypeInteger, Description: "Minutes to wait at most before the next check, 5 to 120"},
			"reason":        {Type: toolcore.ToolSchemaTypeString, Description: "Why it is too early to tell, and what the next check should look at"},
			"until_command": {Type: toolcore.ToolSchemaTypeString, Description: "Optional. A kubectl read of the alert's own cluster (no cluster or context flag) whose output says the next check can run now"},
			"until_expect":  {Type: toolcore.ToolSchemaTypeString, Description: "With until_command: a regular expression the command's output must match"},
			"until_negate":  {Type: toolcore.ToolSchemaTypeBoolean, Description: "With until_command: look again once the output stops matching until_expect"},
		},
		Required: []string{"minutes", "reason"},
	}
}

func (ScheduleRecheckTool) Call(ctx toolcore.NbToolContext, input toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	fail := func(err error) (toolcore.NBToolResponse, error) {
		return toolErrorResponse(err.Error()), err
	}
	var minutes int
	switch m := input.Arguments["minutes"].(type) {
	case float64:
		minutes = int(m)
	case int:
		minutes = m
	default:
		return fail(fmt.Errorf("schedule_recheck: minutes must be a number from %d to %d, got %v", recheckMinMinutes, recheckMaxMinutes, input.Arguments["minutes"]))
	}
	reason, _ := input.Arguments["reason"].(string)
	command, _ := input.Arguments["until_command"].(string)
	expect, _ := input.Arguments["until_expect"].(string)
	negate, _ := input.Arguments["until_negate"].(bool)
	out, err := scheduleRecheck(ctx, minutes, strings.TrimSpace(reason),
		events.RecheckCondition{Command: strings.TrimSpace(command), Expect: strings.TrimSpace(expect), Negate: negate})
	if err != nil {
		return fail(err)
	}
	return toolcore.NBToolResponse{Data: out, Status: toolcore.NBToolResponseStatusSuccess}, nil
}

// runStepRequest is a run_step call: a step of the plan version the model read, and what to do with it.
type runStepRequest struct {
	PlanVersion string
	Step        int    // 1-based
	Action      string // apply, rollback, decline, done
	Command     string // the step's command for the action, as the plan has it
	Reason      string // a decline's reason
	Checked     bool   // the person checked an earlier run whose outcome is unknown
}

// RunStepTool is a write: core asks the person to confirm every call, and only the confirmed call acts.
type RunStepTool struct{}

func (RunStepTool) Name() string { return toolcore.ToolRunStep }

func (RunStepTool) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }

func (RunStepTool) Description() string {
	return `Acts on one step of this alert's remediation plan, as the person in this conversation, after they confirm: apply runs the step's command, rollback runs its undo command, decline records that they decided against it (with their reason), done records that they made the change themselves. It is recorded on the alert exactly like the Run, Decline and "I ran this" buttons, so a step runs at most once from any surface.
Read the plan first (get_remediation_plan) and pass its plan_version, the step number, and the step's command for the action exactly as the plan shows it. Call it only when the person asked for this step; they confirm the call before it acts. To run a step of the plan, use this tool rather than a command tool: a command run another way is not recorded on the plan, so the plan's Run would run it again.
Refused with the reason: a plan that changed since you read it, a step already decided, a step that runs after one that has not succeeded, and a command whose last run has an unknown outcome (pass checked=true only when the person says they checked it). The result says how the command ended; use it to decide what to do next with the person.`
}

// ConfirmationKey makes each step's action its own approval (toolcore.ToolConfirmationScope): without it the key
// is the tool's name, and one "yes" in the turn approved every later run_step call, another step included.
func (RunStepTool) ConfirmationKey(toolInput string) string {
	var in struct {
		PlanVersion string `json:"plan_version"`
		Step        any    `json:"step"`
		Action      string `json:"action"`
	}
	if err := json.Unmarshal([]byte(toolInput), &in); err != nil {
		return toolcore.ToolRunStep + ":" + toolInput
	}
	return fmt.Sprintf("%s:%s:%v:%s", toolcore.ToolRunStep, in.PlanVersion, in.Step, strings.ToLower(strings.TrimSpace(in.Action)))
}

func (RunStepTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"plan_version": {Type: toolcore.ToolSchemaTypeString, Description: "The plan_version get_remediation_plan returned"},
			"step":         {Type: toolcore.ToolSchemaTypeInteger, Description: "The step number, from 1"},
			"action":       {Type: toolcore.ToolSchemaTypeString, Enum: []any{"apply", "rollback", "decline", "done"}, Description: "What to do with the step"},
			"command":      {Type: toolcore.ToolSchemaTypeString, Description: "The step's command for apply (its command) or rollback (its undo command), exactly as the plan has it; empty for decline and done"},
			"reason":       {Type: toolcore.ToolSchemaTypeString, Description: "For decline: the person's reason"},
			"checked":      {Type: toolcore.ToolSchemaTypeBoolean, Description: "True only when the person says they checked an earlier run of this command whose outcome is unknown"},
		},
		Required: []string{"plan_version", "step", "action"},
	}
}

// InferToolRequestType: every call acts on the person's behalf, so every call is confirmed by them.
func (RunStepTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (toolcore.ToolRequestType, error) {
	return toolcore.ToolRequestTypeCreate, nil
}

func (RunStepTool) Call(ctx toolcore.NbToolContext, input toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	out, err := runStepFromChat(ctx, runStepInput(input))
	if err != nil {
		return toolErrorResponse(err.Error()), err
	}
	return toolcore.NBToolResponse{Data: out, Status: toolcore.NBToolResponseStatusSuccess}, nil
}

// runStepInput reads a run_step call. The executor moves an input's "command" into input.Command and passes
// the other fields as Arguments; a direct call may pass the command in Arguments.
func runStepInput(input toolcore.NBToolCallRequest) runStepRequest {
	request := runStepRequest{Command: input.Command}
	request.PlanVersion, _ = input.Arguments["plan_version"].(string)
	request.Action, _ = input.Arguments["action"].(string)
	request.Action = strings.ToLower(strings.TrimSpace(request.Action))
	if command, ok := input.Arguments["command"].(string); ok && command != "" {
		request.Command = command
	}
	request.Reason, _ = input.Arguments["reason"].(string)
	request.Checked, _ = input.Arguments["checked"].(bool)
	switch step := input.Arguments["step"].(type) {
	case float64:
		request.Step = int(step)
	case int:
		request.Step = step
	}
	return request
}

// The alert's remediation chat (#40734), event-<fingerprint> on the web and in Slack: the agent reads the plan
// (get_remediation_plan), writes a new one on request (propose_fix) and runs a confirmed step (run_step),
// recorded like the panel's and Slack card's buttons.

// chatSource marks a decision made in the alert's chat.
const chatSource = "chat"

// sessionOf is the session of the conversation a tool call belongs to.
func sessionOf(ctx toolcore.NbToolContext) (string, error) {
	dao := agentcore.GetConversationDao()
	if dao == nil {
		return "", errors.New("conversations are unavailable")
	}
	conversation, err := dao.GetConversation(ctx.ConversationId)
	if err != nil {
		return "", fmt.Errorf("conversation: %w", err)
	}
	return conversation.SessionID, nil
}

// alertOfCall is the event a tool call's conversation is about, on the call's account, never from the
// model: the run's event in a follow-up turn; otherwise the event holding the fingerprint's newest plan (a
// re-fire creates a newer event that has none), else the newest event of the fingerprint.
func alertOfCall(ctx toolcore.NbToolContext, db *sqlx.DB) (eventId, fingerprint string, err error) {
	if agentcore.IsReadOnlyRun(ctx.Ctx) && ctx.MessageId != "" {
		readCtx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
		defer cancel()
		if run, err := events.OpenFollowupOfTurn(readCtx, db, ctx.MessageId, ctx.AccountId); err == nil {
			return run.EventId, run.Fingerprint, nil
		}
		// A planning turn plans its request's event (remediation_planning.go).
		if ctx.QueryConfig.RemediationPlanTurn != "" {
			if row, err := events.PlanningOfTurn(readCtx, db, ctx.AccountId, ctx.MessageId); err == nil {
				return row.EventId, row.Alert, nil
			}
		}
	}
	session, err := sessionOf(ctx)
	if err != nil {
		return "", "", err
	}
	return alertOfSession(db, ctx.AccountId, session)
}

// alertOfSession is alertOfCall for the alert's conversation, event-<fingerprint>.
func alertOfSession(db *sqlx.DB, accountId, session string) (eventId, fingerprint string, err error) {
	fingerprint, ok := events.AlertFingerprintOfSession(session)
	if !ok {
		return "", "", errors.New("this conversation is not about an alert")
	}
	readCtx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	err = db.GetContext(readCtx, &eventId, `SELECT id FROM (SELECT COALESCE(
		   (SELECT a.event_id::text FROM event_log_analysis a JOIN events e ON e.id = a.event_id
		     WHERE a.cloud_account_id = $1::uuid AND a.event_fingerprint = $2 AND a.analysis_type = $3 AND e.cloud_account_id = $1::uuid
		     ORDER BY COALESCE(a.updated_at, a.recorded_at) DESC LIMIT 1),
		   (SELECT id::text FROM events WHERE cloud_account_id = $1::uuid AND fingerprint = $2 ORDER BY created_at DESC LIMIT 1)) AS id) alert
		 WHERE id IS NOT NULL`, accountId, fingerprint, events.AnalysisTypeRemediation)
	if errors.Is(err, sql.ErrNoRows) {
		// An event with no fingerprint is its own alert: its conversation is event-<id>.
		if id, perr := uuid.Parse(fingerprint); perr == nil {
			err = db.GetContext(readCtx, &eventId, `SELECT id::text FROM events
				 WHERE id = $2 AND cloud_account_id = $1::uuid AND COALESCE(fingerprint, '') = ''`, accountId, id)
		}
	}
	return eventId, fingerprint, err
}

// remediationPlanText is get_remediation_plan: the alert's current plan with each step's decision, and each
// run's follow-up, as the remediation panel and the Slack cards show them.
func remediationPlanText(ctx toolcore.NbToolContext) (string, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", fmt.Errorf("get_remediation_plan: %w", err)
	}
	eventId, _, err := alertOfCall(ctx, dbManager.Db)
	if err != nil {
		return "", fmt.Errorf("get_remediation_plan: the alert of this conversation was not found: %w", err)
	}
	readCtx, cancel := context.WithTimeout(ctx.Ctx.GetContext(), decisionDBTimeout)
	defer cancel()
	cards, err := buildRemediationSlackCards(ctx.Ctx.WithContext(readCtx), dbManager.Db, ctx.AccountId, eventId)
	if err != nil {
		return "", fmt.Errorf("get_remediation_plan: %w", err)
	}
	return renderPlanCards(cards), nil
}

// renderPlanCards writes the cards as text for the model.
func renderPlanCards(cards slackCards) string {
	var b strings.Builder
	plan := false
	for _, c := range cards.Cards {
		if c.Kind != "plan" {
			continue
		}
		plan = true
		fmt.Fprintf(&b, "Current plan for this alert (plan_version %s).\nCause: %s\n%s\n", c.PlanVersion, c.RootCause, c.Summary)
		for _, s := range c.Steps {
			fmt.Fprintf(&b, "\nStep %d: %s (%s, %d%% likely to resolve)", s.Index+1, s.Title, firstNonEmpty(s.Kind, "unclassified"), s.Confidence)
			if s.Automation != "" {
				fmt.Fprintf(&b, "\n  Runs the account's automation %s", s.Automation)
			}
			if s.Command != "" {
				fmt.Fprintf(&b, "\n  Command: %s", s.Command)
			}
			if s.Rollback != "" {
				fmt.Fprintf(&b, "\n  Undo: %s", s.Rollback)
			}
			if s.AfterStep > 0 {
				fmt.Fprintf(&b, "\n  Runs after step %d", s.AfterStep)
			}
			fmt.Fprintf(&b, "\n  Decision: %s", stepDecisionText(s))
		}
		b.WriteString("\n")
	}
	if !plan {
		b.WriteString("This alert has no current remediation plan.\n")
	}
	for _, c := range cards.Cards {
		if c.Kind != "followup" {
			continue
		}
		state := firstNonEmpty(c.Outcome, "still checking")
		if c.Outcome == "" && c.RecheckAt != "" {
			state = "checking again at " + c.RecheckAt + ": " + c.RecheckReason
		}
		fmt.Fprintf(&b, "\nFollow-up on %s: %s\n%s\n", c.Command, state, truncate(c.Summary, 1500))
	}
	return strings.TrimSpace(b.String())
}

func stepDecisionText(s slackCardStep) string {
	when := ""
	if s.At != nil {
		when = " at " + s.At.UTC().Format("2006-01-02 15:04 UTC")
	}
	switch {
	case s.State == "":
		if !s.Runnable && s.SourceChange != "" {
			return "none yet; " + s.SourceChange + ": made in source (or raised as a pull request), then marked done"
		}
		if !s.Runnable && s.DeliveryReason != "" {
			return "none yet; a person makes this change outside NudgeBee (" + s.DeliveryReason + ")"
		}
		return "none yet"
	case s.State == events.RunStatusSuccess && s.Manual:
		return "marked done by a person" + when
	case s.State == events.RunStatusSuccess && s.ExitCodeReported:
		return "ran and exited 0" + when
	case s.State == events.RunStatusSuccess:
		return "ran, outcome unverified (no exit code)" + when
	case s.State == events.RunStatusFailed:
		return "ran and failed" + when + ": " + truncate(s.Message, 300)
	case s.State == events.RunStatusUnknown:
		return "ran, outcome unknown: it may have taken effect" + when
	case s.State == events.RunStatusDeclined:
		return "declined by a person" + when
	case s.State == events.RunStatusInProgress:
		return "running now"
	}
	return s.State
}

// proposeFromChat is propose_fix in the alert's chat: a person asked for a different fix. Hard limits
// refuse it, each with the reason the model reads: a run of the alert in flight or its follow-up still
// checking (one proposer at a time), and the alert's 24-hour cap on proposals from chat.
func proposeFromChat(ctx toolcore.NbToolContext, findings, planJSON string) (string, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", fmt.Errorf("propose_fix: %w", err)
	}
	db := dbManager.Db
	session, err := sessionOf(ctx)
	if err != nil {
		return "", fmt.Errorf("propose_fix: %w", err)
	}
	eventId, fingerprint, err := alertOfSession(db, ctx.AccountId, session)
	if err != nil {
		return "", fmt.Errorf("propose_fix: the alert of this conversation was not found: %w", err)
	}
	if reason, err := chatProposalRefusal(ctx, db, session, fingerprint); err != nil {
		return "", fmt.Errorf("propose_fix: %w", err)
	} else if reason != "" {
		return "", errors.New("propose_fix: " + reason)
	}
	plan, _, err := proposePlan(ctx.Ctx, ctx.AccountId, eventId, planJSON)
	if err != nil {
		return "", fmt.Errorf("propose_fix: the plan was not stored (%v). Correct it and call propose_fix again", err)
	}
	ctx.Ctx.GetLogger().Info("remediation: plan proposed from the alert's chat", "event_id", eventId, "findings", truncate(findings, 300))
	return proposalText(plan), nil
}

// chatProposalRefusal is why a proposal from the alert's chat is refused now, or "".
func chatProposalRefusal(ctx toolcore.NbToolContext, db *sqlx.DB, session, fingerprint string) (string, error) {
	readCtx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	busy, err := events.AlertRunInFlight(readCtx, db, ctx.AccountId, fingerprint)
	if err != nil {
		return "", fmt.Errorf("reading the alert's runs: %w", err)
	}
	if busy {
		return "a step of this alert is running or its follow-up check has not finished; a new plan now would replace the one being checked. Say so, and suggest asking again when the check reports", nil
	}
	proposed, err := chatProposalCount(readCtx, db, session, ctx.AccountId)
	if err != nil {
		return "", err
	}
	if max := config.Config.RemediationFollowupMaxRuns; max > 0 && proposed >= max {
		return fmt.Sprintf("%d plans were already proposed from this alert's chat in 24 hours; a person decides what to try next in the remediation panel", proposed), nil
	}
	return "", nil
}

// chatProposalCount is how many plans were proposed in the alert's conversation in the last 24 hours: by a
// person's turn (chat, or a replan) or a follow-up check. The background planner's own turns (after an
// investigation, Ask Nubi, re-check) are not a person's and do not count.
func chatProposalCount(ctx context.Context, db *sqlx.DB, session, accountId string) (int, error) {
	var proposed int
	if err := db.GetContext(ctx, &proposed, `SELECT count(*) FROM llm_conversation_tool_calls tc JOIN llm_conversations c ON c.id = tc.conversation_id
		  LEFT JOIN llm_conversation_messages m ON m.id = tc.message_id
		 WHERE c.session_id = $1 AND c.account_id = $2::uuid AND tc.tool_name = $3 AND tc.status = 'success'
		   AND tc.created_at > now() - interval '24 hours'
		   AND COALESCE(m.message_config, '') !~ '"remediation_plan_turn"\s*:\s*"(auto|ask|recheck)"'`, session, accountId, toolcore.ToolProposeFix); err != nil {
		return 0, fmt.Errorf("counting proposals: %w", err)
	}
	return proposed, nil
}

// runStepFromChat is run_step: the person in the alert's chat, who confirmed this call, decides a step of
// the alert's plan. It goes through decideStep, as the panel's and Slack's buttons do, as that person.
func runStepFromChat(ctx toolcore.NbToolContext, request runStepRequest) (string, error) {
	if agentcore.IsReadOnlyRun(ctx.Ctx) {
		return "", errors.New("run_step: this turn only reads; report which step a person should run")
	}
	sc := ctx.Ctx.GetSecurityContext()
	userId := ""
	if sc != nil {
		userId = strings.TrimSpace(sc.GetUserId())
	}
	if userId == "" || userId == security.GetSystemUserId() {
		return "", errors.New("run_step: only a person can decide a step, and nobody is in this conversation")
	}
	if !sc.HasAccountAccess(ctx.AccountId, security.SecurityAccessTypeCreate) && !grantedRun(sc, ctx.AccountId, moduleAiMisc) {
		return "", errors.New("run_step: the person in this conversation has no write access to this account; they cannot run or decide its steps")
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", fmt.Errorf("run_step: %w", err)
	}
	eventId, _, err := alertOfCall(ctx, dbManager.Db)
	if err != nil {
		return "", fmt.Errorf("run_step: the alert of this conversation was not found: %w", err)
	}
	stored, ok := loadSavedPlan(ctx.Ctx, ctx.AccountId, eventId)
	if !ok {
		return "", errors.New("run_step: this alert has no stored plan; propose one first")
	}
	execute, err := stepRequest(stored.Plan, ctx.AccountId, eventId, request)
	if err != nil {
		return "", err
	}
	// With the loop on, the step is recorded as the run of the approval the person gave (C1).
	execute.ChatApproval = chatStepApprovalOf(ctx, userId)
	answer := decideStep(ctx.Ctx, execute, userId)
	if answer.Message != "" {
		return "", errors.New("run_step: " + strings.TrimPrefix(answer.Message, "remediation: "))
	}
	return stepResultText(request, answer.Data), nil
}

// stepRequest turns a run_step call into the decision request a button sends, checked against the stored
// plan: the step exists, and the command the model passed is the step's own for the action.
func stepRequest(plan RemediationPlan, accountId, eventId string, request runStepRequest) (RemediationExecuteRequest, error) {
	action, err := planAction(plan, request.Step-1)
	if err != nil {
		return RemediationExecuteRequest{}, fmt.Errorf("run_step: %w", err)
	}
	execute := RemediationExecuteRequest{AccountId: accountId, EventId: eventId, PlanVersion: strings.TrimSpace(request.PlanVersion),
		Source: chatSource, AcknowledgeUnknown: request.Checked}
	want := ""
	switch request.Action {
	case "apply":
		execute.Slot, want = RemediationSlotExecute, stepRef(action)
		if strings.TrimSpace(want) == "" {
			return execute, fmt.Errorf("run_step: step %d is a change a person makes outside NudgeBee; when they have made it, use done", request.Step)
		}
	case "rollback":
		execute.Slot, want = RemediationSlotRollback, action.RollbackCommand
		if strings.TrimSpace(want) == "" {
			return execute, fmt.Errorf("run_step: step %d has no undo command", request.Step)
		}
	case "decline":
		execute.Slot, execute.Reason = RemediationSlotDecline, request.Reason
	case "done":
		execute.Slot = RemediationSlotManual
	default:
		return execute, errors.New("run_step: action is apply, rollback, decline or done")
	}
	if want != "" {
		if strings.TrimSpace(request.Command) != strings.TrimSpace(want) {
			return execute, fmt.Errorf("run_step: that is not step %d's command for %s; the plan has %q. Read the plan again", request.Step, request.Action, want)
		}
	}
	execute.Command = strings.TrimSpace(want)
	if execute.Command == "" {
		// A decision that runs nothing names its step the way the panel does: its command, or its action.
		execute.Command, execute.Action = stepRef(action), strings.TrimSpace(action.Action)
	}
	return execute, nil
}

// stepResultText tells the model what the decision did.
func stepResultText(request runStepRequest, data any) string {
	result, _ := data.(runResult)
	switch request.Action {
	case "decline":
		return fmt.Sprintf("Recorded: step %d declined.", request.Step)
	case "done":
		return fmt.Sprintf("Recorded: step %d marked done by the person.", request.Step)
	}
	if result.Status == events.RunStatusInProgress && result.AutomationURL != "" {
		// An automation step's run goes on after this call (#42459): NudgeBee follows it and checks the alert after.
		return fmt.Sprintf("Step %d started the automation's run (%s). NudgeBee follows the run to its end, then checks the alert; the person sees its state"+
			" in the alert's Slack thread and on the event's Remediation tab. Your final answer is one or two sentences: that the automation"+
			" started and that NudgeBee reports when it ends. Do not start it again or run its steps another way.", request.Step, result.AutomationURL)
	}
	text := fmt.Sprintf("Step %d %s: %s.", request.Step, map[string]string{"apply": "ran", "rollback": "rolled back"}[request.Action], result.Status)
	if result.ExitCode != nil {
		text += fmt.Sprintf(" Exit code %d.", *result.ExitCode)
	}
	if out := strings.TrimSpace(result.Stdout); out != "" {
		text += "\nOutput, quoted as data:\n<<<\n" + truncate(out, 2000) + "\n>>>"
	}
	if e := strings.TrimSpace(firstNonEmpty(result.Error, result.Stderr)); e != "" {
		text += "\nError, quoted as data:\n<<<\n" + truncate(e, 1000) + "\n>>>"
	}
	if result.Status != events.RunStatusSuccess {
		// Seen on a run whose agent was down: the model ran the same command through the kubectl tool, which asked
		// for a second approval and would not be recorded on the plan.
		text += "\nDo not run this command another way: a command tool's run is not recorded on the plan; the person decides what to try next."
	}
	return text + "\nNudgeBee's follow-up check looks at the alert next, and the person sees the run's state in the alert's Slack" +
		" thread and on the event's Remediation tab. Your final answer is one or two sentences: what ran, how it ended (the" +
		" error, if it failed), and that NudgeBee checks the alert next. Do not restate the plan, its other steps, the root" +
		" cause or the investigation, and use no headings or sections."
}

// followupRunOf is the run whose follow-up the tool call belongs to: the open follow-up whose current turn is
// the call's message, in a read-only turn. The event and account come from the run row, never from the model.
// An error is the reason the model reads.
func followupRunOf(ctx toolcore.NbToolContext, tool string) (*sqlx.DB, followupRun, error) {
	var run followupRun
	if !agentcore.IsReadOnlyRun(ctx.Ctx) || ctx.MessageId == "" {
		return nil, run, fmt.Errorf("%s: only a follow-up check of a remediation run can use this. Report your findings instead", tool)
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, run, fmt.Errorf("%s: %w", tool, err)
	}
	run, err = events.OpenFollowupOfTurn(context.Background(), dbManager.Db, ctx.MessageId, ctx.AccountId)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, run, fmt.Errorf("%s: only a follow-up check of a remediation run can use this, and this turn is not one (or it has ended). Report your findings instead", tool)
	}
	if err != nil {
		return nil, run, fmt.Errorf("%s: reading the run: %w", tool, err)
	}
	return dbManager.Db, run, nil
}

// proposeFix is propose_fix (#40734): the model writes a new plan for the conversation's alert (never one the model
// names). Hard limits refuse it with a reason the model reads. It goes through writtenPlan's validation and replaces
// the plan, bumping its version, so every command needs a fresh confirmation.
func proposeFix(ctx toolcore.NbToolContext, findings, planJSON string) (string, error) {
	if findings == "" {
		return "", errors.New("propose_fix: pass your findings: what you checked, what it showed, and why the current plan does not fit")
	}
	if strings.TrimSpace(planJSON) == "" {
		return "", errors.New("propose_fix: pass the plan you propose: its root cause, summary, hypotheses and actions with their commands")
	}
	if ctx.QueryConfig.RemediationPlanTurn != "" && agentcore.IsReadOnlyRun(ctx.Ctx) {
		return proposeFromPlanningTurn(ctx, findings, planJSON) // NudgeBee's background planning turn
	}
	if !agentcore.IsReadOnlyRun(ctx.Ctx) {
		return proposeFromChat(ctx, findings, planJSON) // a person's turn in the alert's chat
	}
	db, run, err := followupRunOf(ctx, "propose_fix")
	if err != nil {
		return "", err
	}
	newer, runs, err := alertRuns(db, run)
	switch {
	case err != nil:
		return "", fmt.Errorf("propose_fix: %w", err)
	case newer:
		return "", errors.New("propose_fix: a newer remediation run of this alert replaced this check. Report your findings instead")
	case config.Config.RemediationFollowupMaxRuns > 0 && runs >= config.Config.RemediationFollowupMaxRuns:
		return "", fmt.Errorf("propose_fix: %d plans of this alert were already checked in 24 hours; a person decides what to try next. Report your findings instead", runs)
	}
	// One proposal per follow-up: taken before the plan is written, so a parallel or repeated call loses.
	n, err := events.ClaimFollowupProposal(context.Background(), db, run.Id, truncate(findings, 2000))
	if err != nil {
		return "", fmt.Errorf("propose_fix: recording the proposal: %w", err)
	}
	if n != 1 {
		return "", errors.New("propose_fix: this follow-up already proposed a plan; it proposes once")
	}
	plan, version, err := proposePlan(ctx.Ctx, run.AccountId, run.EventId, planJSON)
	if err != nil {
		// Nothing was proposed: free the slot so the agent may correct the plan or report.
		_ = events.ReleaseFollowupProposal(context.Background(), db, run.Id)
		return "", fmt.Errorf("propose_fix: the plan was not stored (%v). Correct it and call propose_fix again, or report your findings", err)
	}
	if err := events.LinkFollowupProposal(context.Background(), db, run.Id, version); err != nil {
		return "", fmt.Errorf("propose_fix: the plan is stored but not linked to this follow-up: %w", err)
	}
	return proposalText(plan), nil
}

// proposePlan stores planJSON, written by an agent, as the event's new plan and returns it with its version.
// A var so tests can stub it.
var proposePlan = func(ctx *security.RequestContext, accountId, eventId, planJSON string) (RemediationPlan, string, error) {
	var written struct {
		Actions []json.RawMessage `json:"actions"`
	}
	_ = json.Unmarshal([]byte(planJSON), &written)
	return storeNewPlan(ctx, accountId, eventId, writtenPlan(planJSON), len(written.Actions))
}

// storeNewPlan rebuilds the event's plan from lastPlanInputs via write (nil: the planner model) and stores it. An
// agent-written plan that validation stripped an action from is refused with what was removed, so the agent fixes
// it rather than a person getting a short plan; the planner's "nothing to run" is stored.
func storeNewPlan(ctx *security.RequestContext, accountId, eventId string, write func(system, human string) (string, error), written int) (RemediationPlan, string, error) {
	return storeNewPlanWith(ctx, accountId, eventId, write, written, planStoreOptions{})
}

// planStoreOptions are what a planning turn adds to storeNewPlan.
type planStoreOptions struct {
	// allowEmpty stores an agent's plan written with no actions (it found nothing to run).
	allowEmpty bool
	// revision is the request's part of the plan's revision (kind, reasons, note, findings).
	revision *PlanRevision
	// adjust changes the plan last, before it is stored.
	adjust func(*RemediationPlan)
}

// storeNewPlanWith is storeNewPlan with options. With the loop on, an agent's plan also goes through the repair
// pass (targetStateRefusal), and every plan gets its cause key and revision (stampLoopPlan).
func storeNewPlanWith(ctx *security.RequestContext, accountId, eventId string, write func(system, human string) (string, error), written int, opts planStoreOptions) (RemediationPlan, string, error) {
	request := lastPlanInputs(ctx, accountId, eventId)
	conversationId := remediationConversationID(ctx, accountId, eventId)
	agentWrote := write != nil
	if write == nil {
		write = remediationLLM(ctx, ctx.GetSecurityContext().GetUserId(), accountId, conversationId)
	}
	plan, trace, _, err := newRemediationPlanWith(ctx, request, conversationId, write)
	if err != nil {
		return RemediationPlan{}, "", err
	}
	loop := events.RemediationLoopOn(accountId)
	if agentWrote {
		if !opts.allowEmpty || written != 0 {
			if err := writtenPlanRefusal(plan, trace, written); err != nil {
				return RemediationPlan{}, "", err
			}
		}
		if loop {
			if err := targetStateRefusal(eventId, trace); err != nil {
				return RemediationPlan{}, "", err
			}
			if err := firingPlanRefusal(ctx.GetContext(), accountId, eventId, plan); err != nil {
				return RemediationPlan{}, "", err
			}
		}
	}
	if loop {
		var prev *RemediationPlan
		prevVersion := ""
		if stored, ok := loadSavedPlan(ctx, accountId, eventId); ok {
			prev, prevVersion = &stored.Plan, stored.Version
		}
		stampLoopPlan(&plan, prev, prevVersion, opts.revision, time.Now())
		if opts.adjust != nil {
			opts.adjust(&plan)
		}
	}
	// With the loop on, a plan an agent wrote came from a turn in the alert's conversation (a planning turn, a
	// follow-up check, or a person's chat), whose answer is the record there; only the planner model's plan, which
	// has no turn, is added to the conversation as its own message. With it off every plan is added, as before.
	var version string
	if agentWrote && loop {
		version, err = persistRemediationPlan(ctx, accountId, eventId, plan, trace)
	} else {
		version, err = storeRemediationPlan(ctx, conversationId, request, plan, trace)
	}
	if err != nil {
		return RemediationPlan{}, "", err
	}
	return plan, version, nil
}

// writtenPlan stands in for the planner model when an agent wrote the plan: generation's first call gets the
// agent's plan, and the repair pass, which would ask a model to rewrite it, keeps it as written.
func writtenPlan(planJSON string) func(system, human string) (string, error) {
	calls := 0
	return func(string, string) (string, error) {
		calls++
		if calls > 1 {
			return "", errors.New("the agent's plan is kept as written")
		}
		return planJSON, nil
	}
}

// writtenPlanRefusal refuses a plan an agent wrote with written actions when validation left none, or fewer:
// the agent hears what was removed and corrects it.
func writtenPlanRefusal(plan RemediationPlan, trace RemediationTrace, written int) error {
	if len(plan.Actions) == 0 || len(plan.Actions) < written {
		return fmt.Errorf("%d of %d actions survived validation%s", len(plan.Actions), written, droppedText(plan, trace))
	}
	return nil
}

// droppedText says which actions validation removed from a plan and why, for the model to correct. Corrections that
// kept their action (a trade-off filled in, an after cleared) are left out: listed beside the removals, with only the
// command or value they touched, the model took them for the reason and corrected the wrong action.
func droppedText(plan RemediationPlan, trace RemediationTrace) string {
	var parts []string
	for _, d := range plan.Dropped {
		parts = append(parts, d.Action+": "+d.Reason)
	}
	for _, n := range trace.Validation {
		if why := droppedWhy(n); why != "" {
			parts = append(parts, firstNonEmpty(n.Action, "plan")+": "+why)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

// droppedWhy is why a validation note's rule removed its action, or "" when the rule kept it. An automation's removal
// is in plan.Dropped with its reason.
func droppedWhy(n planValidationNote) string {
	switch n.Rule {
	case "execute_rejected_by_executor_dropped":
		return "its command would be refused when run: " + strings.TrimPrefix(remediationCommandRejection(n.Detail), "remediation: ")
	case "read_only_action_dropped":
		return "its command only reads state, so it changes nothing"
	case "no_command_and_no_artifact_dropped":
		return "it has no command, prepared artifact or required_change to apply"
	}
	return ""
}

// lastPlanInputs is the generate request the event's stored plan was built from, for this account and
// event: the panel's context, available artifacts and alert rule. None when there is no stored plan or it
// predates keeping them; the plan is then built from the stored analysis alone.
func lastPlanInputs(ctx *security.RequestContext, accountId, eventId string) RemediationGenerateRequest {
	request := RemediationGenerateRequest{AccountId: accountId, EventId: eventId}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return request
	}
	repo := events.NewEventAnalysisRepository(dbManager)
	info, err := repo.GetEventInfo(ctx, eventId, accountId)
	if err != nil || info == nil {
		return request
	}
	saved, err := repo.GetEventAnalysis(ctx, eventId, info.Fingerprint, info.AggregationKey, accountId, events.AnalysisTypeRemediation)
	if err != nil || saved == nil || !planSavedForEvent(saved, eventId) {
		return request
	}
	var stored struct {
		Generation struct {
			Request *RemediationGenerateRequest `json:"request"`
		} `json:"generation"`
	}
	if json.Unmarshal([]byte(saved.Analysis), &stored) != nil || stored.Generation.Request == nil {
		return request
	}
	last := *stored.Generation.Request
	last.AccountId, last.EventId = accountId, eventId
	return last
}

// proposalText is propose_fix's result: what was stored, where the person sees it, and how to answer. The fix options
// are shown to the person on their own (the Remediation tab, the alert's Slack thread), so an answer that restates
// them only repeats them, in chat and in Slack beside the options message.
func proposalText(plan RemediationPlan) string {
	version := "a new version"
	if plan.Version > 0 {
		version = fmt.Sprintf("version %d", plan.Version)
	}
	options := fmt.Sprintf("%d fix options", len(plan.Actions))
	if len(plan.Actions) == 1 {
		options = "1 fix option"
	}
	var b strings.Builder
	if len(plan.Actions) == 0 {
		fmt.Fprintf(&b, "Stored %s of the alert's fixes, with no fix options: nothing NudgeBee can carry out.", version)
	} else {
		fmt.Fprintf(&b, "Stored %s of the alert's fixes, with %s. A person must confirm each command before it runs.", version, options)
	}
	b.WriteString(" The person already sees the fix options, each with its own button, on the event's Remediation tab and in the alert's Slack thread, and the investigation is there too.")
	fmt.Fprintf(&b, " Your final answer is one sentence that the fixes were updated (%s, %s) and can be picked there."+
		" Do not restate, list or summarise the fixes, their commands, the root cause or the investigation, and use no headings or sections.", version, options)
	return b.String()
}

// How long the agent may ask to wait at most before the next check, and how many re-checks one follow-up gets. The
// longest wait covers a hold (an alert rule's for: plus a settling window, at most 2 hours). Vars so tests can change
// them.
var (
	recheckMinMinutes   = 5
	recheckMaxMinutes   = 120
	followupMaxRechecks = 3
)

// recheckUntilMaxCommand bounds a re-check condition's command, and recheckReasonMax the reason.
const (
	recheckUntilMaxCommand = 500
	recheckReasonMax       = 1000
)

// scheduleRecheck is schedule_recheck (#40734): in a follow-up the agent asks to look again, at most N minutes on.
// It only records the request; at turn end the job turns it into a wait (requestRecheck: a watch, #42461), or drops
// it if the turn proposed. With until the watch ends as soon as a strict kubectl read of the alert's own cluster
// matches; without it, when the minutes pass. Hard limits (not a follow-up, ended/proposed, newer run, run cap,
// re-check cap, a condition that is not a strict read) refuse with a reason the model reads.
func scheduleRecheck(ctx toolcore.NbToolContext, minutes int, reason string, until events.RecheckCondition) (string, error) {
	if minutes < recheckMinMinutes || minutes > recheckMaxMinutes {
		return "", fmt.Errorf("schedule_recheck: minutes must be from %d to %d, got %d", recheckMinMinutes, recheckMaxMinutes, minutes)
	}
	if reason == "" {
		return "", errors.New("schedule_recheck: pass the reason: why it is too early to tell, and what the next check should look at")
	}
	if (until.Command == "") != (until.Expect == "") {
		return "", errors.New("schedule_recheck: pass until_command and until_expect together (or neither, to wait for the minutes)")
	}
	db, run, err := followupRunOf(ctx, "schedule_recheck")
	if err != nil {
		return "", err
	}
	if until.Command != "" {
		if refusal := recheckConditionRefusal(ctx, run.AccountId, until); refusal != "" {
			return "", errors.New("schedule_recheck: " + refusal)
		}
	}
	newer, _, err := alertRuns(db, run)
	switch {
	case err != nil:
		return "", fmt.Errorf("schedule_recheck: %w", err)
	case newer:
		return "", errors.New("schedule_recheck: a newer remediation run of this alert replaced this check. Report your findings instead")
	}
	n, err := events.RequestFollowupRecheck(context.Background(), db, run.Id, minutes, truncate(reason, recheckReasonMax), until, followupMaxRechecks)
	if err != nil {
		return "", fmt.Errorf("schedule_recheck: recording the request: %w", err)
	}
	if n != 1 {
		return "", fmt.Errorf("schedule_recheck: refused: this follow-up proposed a new plan, or used its %d re-checks. Report your findings instead", followupMaxRechecks)
	}
	if until.Command != "" {
		return fmt.Sprintf("NudgeBee will check this alert again once `%s` %s, and in about %d minutes at the latest, in this conversation. End this turn with what you found so far and what the next check should confirm.", until.Command, conditionText(until), minutes), nil
	}
	return fmt.Sprintf("NudgeBee will check this alert again in about %d minutes, in this conversation. End this turn with what you found so far and what the next check should confirm.", minutes), nil
}

// recheckAccountProvider is the cloud provider of an account ("k8s" for a cluster). A var so tests can stub it.
var recheckAccountProvider = tools.GetCloudProviderForAccount

// recheckConditionRefusal says why until cannot be a re-check condition on accountId, or "" when it can: only a
// strict kubectl read of a Kubernetes account's own cluster, which NudgeBee runs with no one to confirm it.
func recheckConditionRefusal(ctx toolcore.NbToolContext, accountId string, until events.RecheckCondition) string {
	if !strings.EqualFold(recheckAccountProvider(accountId), "k8s") {
		return "until_command only works on a Kubernetes account. Pass minutes alone"
	}
	if len(until.Command) > recheckUntilMaxCommand {
		return fmt.Sprintf("until_command is longer than %d characters", recheckUntilMaxCommand)
	}
	if reason := checkRejection(ctx.Ctx, accountId, "k8s", until.Command); reason != "" {
		return "until_command: " + reason + " Use a plain read such as kubectl get"
	}
	if _, err := regexp.Compile(until.Expect); err != nil {
		return "until_expect is not a valid regular expression: " + err.Error()
	}
	return ""
}

// conditionText is what a re-check condition waits for, after its command.
func conditionText(until events.RecheckCondition) string {
	if until.Negate {
		return "stops matching `" + until.Expect + "`"
	}
	return "matches `" + until.Expect + "`"
}

// recheckNote is what a re-check turn is told about why it runs.
func recheckNote(reason string, n int) string {
	if strings.TrimSpace(reason) == "" {
		return ""
	}
	return fmt.Sprintf("\nThis is re-check %d, which you asked for: %s", n, reason)
}
