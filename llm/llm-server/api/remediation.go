package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	agentcore "nudgebee/llm/agents/core"
	"nudgebee/llm/audit"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	llmruntime "nudgebee/llm/llms/runtime"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"nudgebee/llm/security/egressfilter"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// maxRemediationCommandLen bounds the command a single execute call will run, so a malformed or
// abusive plan can't ship an unbounded string to the relay.
const maxRemediationCommandLen = 4096

// RemediationGenerateRequest is the payload for ai_remediation_generate. The investigation text is
// passed in from the client (it already holds it from ai_get_recommendation) so we do not
// re-investigate; event_id is used only for logging/correlation.
type RemediationGenerateRequest struct {
	AccountId string `json:"account_id"`
	EventId   string `json:"event_id"`
	Context   string `json:"context"`
	// AvailableArtifacts names the remediation surfaces that hold a real, applicable artifact for
	// this event — a code fix produced by code analysis, a threshold suggestion computed by triage.
	// An action with no command can only be carried out through one of these, so an empty list means
	// no such action is actionable, whatever the model proposes.
	AvailableArtifacts []string `json:"available_artifacts"`
	// AlertRule is the rule the event came from and what may be changed on it; an
	// action may propose one of those changes (see remediation_alert_rule.go).
	AlertRule *RemediationAlertRule `json:"alert_rule,omitempty"`
	// automations are the account's automations an action may run (remediation_automation.go), read once per
	// generation; never stored with the request.
	automations []tools.AutomationOption
}

// Confidence is the model's 0-100 self-reported estimate. The model is unreliable about the exact
// JSON shape, so UnmarshalJSON accepts a number or a numeric string and normalizes server-side: a
// fraction in (0,1] is read as a percentage, and everything is rounded and clamped to [0,100]. A
// value it cannot parse becomes 0 rather than failing the whole plan.
type Confidence int

func (c *Confidence) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(strings.Trim(string(data), `"`))
	if s == "" || s == "null" {
		*c = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*c = 0
		return nil
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		// "NaN"/"Infinity" parse without error but would produce a garbage int cast; treat as 0.
		*c = 0
		return nil
	}
	// Treat a strict fraction (0,1) as a percentage (0.85 -> 85); a bare 1 is 1%, not 100%, since the
	// prompt asks for whole percentages and 100 is the max.
	if f > 0 && f < 1 {
		f *= 100
	}
	f = math.Round(f)
	if f < 0 {
		f = 0
	}
	if f > 100 {
		f = 100
	}
	*c = Confidence(f)
	return nil
}

// StringList holds the reasoning as a clean list of short points. The model may emit it as a JSON
// array of points or as a single string (which we split on newlines and strip bullet markers from),
// so the UI always receives a list it can render point-wise.
type StringList []string

func (s *StringList) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*s = nil
		return nil
	}
	if trimmed[0] == '[' {
		var arr []string
		if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
			return err
		}
		*s = cleanPoints(arr)
		return nil
	}
	var str string
	if err := json.Unmarshal([]byte(trimmed), &str); err != nil {
		*s = nil
		return nil
	}
	*s = cleanPoints(strings.Split(str, "\n"))
	return nil
}

// cleanPoints trims each entry, strips a single leading bullet marker, and drops empties.
func cleanPoints(in []string) StringList {
	out := StringList{}
	for _, p := range in {
		p = strings.TrimSpace(p)
		for _, m := range []string{"- ", "* ", "• ", "· ", "– "} {
			if strings.HasPrefix(p, m) {
				p = strings.TrimSpace(p[len(m):])
				break
			}
		}
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// RemediationHypothesis is one candidate explanation for the root cause, with the reasoning/evidence
// behind it (as short points) and the model's 0-100 confidence. The plan derives these from the
// investigation before proposing actions: when none is confident enough, the plan recommends no remediation.
type RemediationHypothesis struct {
	Hypothesis string     `json:"hypothesis"`
	Reasoning  StringList `json:"reasoning"`
	Confidence Confidence `json:"confidence"`
}

// RemediationAction is one candidate recovery strategy, or one change of a strategy whose changes must
// be applied in order (After). The name and confidence are model-generated (never a fixed vocabulary): action
// is a short specific name for the strategy, confidence is the model's 0-100 estimate that it
// durably resolves the root cause. Kind separates a real fix from a mitigation that only restores
// service while the cause survives — investigations routinely label the latter an "Immediate Fix",
// so the distinction is carried explicitly rather than left to the reader. Hypothesis names which
// candidate cause the action addresses. execute/verify/rollback are the commands that carry it out.
type RemediationAction struct {
	Hypothesis      string     `json:"hypothesis"`
	Action          string     `json:"action"`
	Kind            string     `json:"kind"`
	Title           string     `json:"title"`
	Confidence      Confidence `json:"confidence"`
	ExecuteCommand  string     `json:"execute_command"`
	VerifyCommand   string     `json:"verify_command"`
	RollbackCommand string     `json:"rollback_command"`
	// Commands as argument lists; the server builds the strings above (commandFromArgv). As strings,
	// a JSON payload came back cut at its first inner quote.
	ExecuteArgv  []string `json:"execute_argv,omitempty"`
	VerifyArgv   []string `json:"verify_argv,omitempty"`
	RollbackArgv []string `json:"rollback_argv,omitempty"`
	// AlertRuleChange, when set, is a change to the alert rule itself, applied from
	// the card rather than by a command.
	AlertRuleChange *AlertRuleChange `json:"alert_rule_change,omitempty"`
	// RequiredChange, when set, is a fix no command applies durably - a code, chart-values,
	// infrastructure, dependency or process change - stated so the operator can make it.
	RequiredChange *RequiredChange `json:"required_change,omitempty"`
	// Delivery is how this action reaches the system (run / operator / source), set by the server from
	// the account's executor each time the plan is returned; DeliveryReason says why when it is not run.
	Delivery       string `json:"delivery,omitempty"`
	DeliveryReason string `json:"delivery_reason,omitempty"`
	// DeliveryRetry marks an operator step that comes from a past permission refusal: permissions can be
	// granted since, so NudgeBee may run it after all, and a successful run clears the refusal.
	DeliveryRetry bool `json:"delivery_retry,omitempty"`
	// VerifyReason, set when the plan is returned, says why NudgeBee will not run the verify command
	// without a confirmation (it is not a strict kubectl read); the person checks it themselves.
	VerifyReason string `json:"verify_reason,omitempty"`
	// After names the action that must be applied before this one, when the change only works in that
	// order (a restart after the config it reloads, a redrive after the permission it needs). Actions
	// without it are alternative strategies.
	After string `json:"after,omitempty"`
	// TradeOff names what the action gives up: what it stops, deletes, suspends, scales away or re-creates,
	// or the deliberate change by a person it reverses. Shown on the card; such a step is a mitigation.
	TradeOff string `json:"trade_off,omitempty"`
	// Automation, when set, is a run of one of the account's own automations in place of a command
	// (remediation_automation.go); a step has one or the other.
	Automation *PlanAutomation `json:"automation,omitempty"`
	// Source, when set, is the runbook line this step follows, checked against the document (remediation_source.go).
	Source *PlanSource `json:"source,omitempty"`
}

// HardeningItem is an improvement the incident does not require; kept apart from actions so it never reads as the fix.
type HardeningItem struct {
	Target string `json:"target"`
	Change string `json:"change"`
	Reason string `json:"reason"`
}

// RequiredChange says where a durable fix is made and what it is. Repo is the repository it is made in, when known
// (a change in code or a deployment source there is raised as a pull request, remediation_code_fix.go); Reason is
// why the change fixes the cause.
type RequiredChange struct {
	Where  string `json:"where"`
	Target string `json:"target"`
	Change string `json:"change"`
	Repo   string `json:"repo,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// actionable reports whether a required change names both what to change and how.
func (r *RequiredChange) actionable() bool {
	return r != nil && strings.TrimSpace(r.Target) != "" && strings.TrimSpace(r.Change) != ""
}

// Action kinds. A fix removes the root cause; a mitigation restores service with the cause still
// present, so the problem recurs.
const (
	RemediationKindFix        = "fix"
	RemediationKindMitigation = "mitigation"
)

// maxMitigationConfidence caps a mitigation's confidence. Confidence means "durably resolves the
// root cause", and a mitigation by definition does not — so a model that reports a restart at 95%
// is answering the wrong question. Clamping server-side keeps the ranking honest even when the
// model ignores the prompt rule.
const maxMitigationConfidence = 50

// normalizePlan canonicalizes model-generated fields that the UI ranks and renders on. Kind is
// free text from the LLM, so anything that isn't recognizably a fix is treated as a mitigation:
// the safe default is to under-claim, since presenting a mitigation as a fix is the failure mode
// that misleads an operator.
// It returns every correction made, which the generation trace records.
func normalizePlan(plan *RemediationPlan, availableArtifacts []string, alertRule *RemediationAlertRule) []planValidationNote {
	// An action inherits the uncertainty of the hypothesis it addresses: if the cause is only 95%
	// likely, no action against it can be more than 95% likely to resolve the incident. The model
	// scores the two independently and routinely emits a 100% action under a 95% hypothesis, which
	// reads as a contradiction on the card. Clamp so the action never outruns its own premise.
	hypConfidence := make(map[string]Confidence, len(plan.Hypotheses))
	for _, h := range plan.Hypotheses {
		hypConfidence[strings.ToLower(strings.TrimSpace(h.Hypothesis))] = h.Confidence
	}

	// The alert-rule controls admit only actions that carry alert_rule_change; every
	// other command-less action still needs a prepared artifact (a code fix, a
	// threshold suggestion). One flag for all of them would let unprepared work back in.
	hasArtifact := false
	for _, a := range availableArtifacts {
		if name := strings.TrimSpace(a); name != "" && name != artifactAlertRule {
			hasArtifact = true
			break
		}
	}

	notes := []planValidationNote{}
	for i := range plan.Actions {
		notes = append(notes, applyArgv(&plan.Actions[i])...)
	}
	// A step another step needs ("delete the job, then re-create it fixed") is part of a fix, not a
	// trade-off. Only an after naming another action in the plan links two steps.
	key := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	names := map[string]bool{}
	for _, a := range plan.Actions {
		names[key(a.Action)] = true
	}
	linksTo := func(a RemediationAction) bool {
		after := key(a.After)
		return after != "" && after != key(a.Action) && names[after]
	}
	dependedOn := map[string]bool{}
	for _, a := range plan.Actions {
		if linksTo(a) {
			dependedOn[key(a.After)] = true
		}
	}
	kept := plan.Actions[:0]
	for i := range plan.Actions {
		a := &plan.Actions[i]
		// A rule change's kind follows from what it does, not from the model's label, and
		// must be set before the mitigation cap below reads it.
		if a.AlertRuleChange != nil {
			a.Kind = alertRuleActionKind(strings.ToLower(strings.TrimSpace(a.AlertRuleChange.Operation)))
		}
		// A step in a chain, needed by another or needing one, keeps its kind but still states its cost.
		chained := dependedOn[key(a.Action)] || linksTo(*a)
		if a.AlertRuleChange == nil {
			if what := destructiveEffect(a); what != "" && strings.TrimSpace(a.TradeOff) == "" {
				a.TradeOff = what
				notes = append(notes, planValidationNote{Action: a.Action, Rule: "trade_off_from_command", Detail: what})
			}
		}
		if strings.TrimSpace(a.TradeOff) != "" && !chained && strings.EqualFold(strings.TrimSpace(a.Kind), RemediationKindFix) {
			// Giving something up to quiet the incident is not removing its cause.
			notes = append(notes, planValidationNote{Action: a.Action, Rule: "trade_off_is_a_mitigation", Detail: a.TradeOff})
			a.Kind = RemediationKindMitigation
		}
		if strings.EqualFold(strings.TrimSpace(a.Kind), RemediationKindFix) {
			a.Kind = RemediationKindFix
		} else {
			a.Kind = RemediationKindMitigation
			if int(a.Confidence) > maxMitigationConfidence {
				a.Confidence = Confidence(maxMitigationConfidence)
			}
		}
		// Only clamp against a hypothesis we can actually resolve; an unmatched reference means the
		// model named a hypothesis it did not list, and inventing a ceiling from that would be worse
		// than leaving the value alone.
		if ceiling, ok := hypConfidence[strings.ToLower(strings.TrimSpace(a.Hypothesis))]; ok && a.Confidence > ceiling {
			a.Confidence = ceiling
		}

		if a.AlertRuleChange != nil {
			if reason := checkAlertRuleChange(a.AlertRuleChange, alertRule); reason != "" {
				plan.Dropped = append(plan.Dropped, DroppedAction{Action: a.Action, Reason: reason})
				continue
			}
			a.Kind = alertRuleActionKind(a.AlertRuleChange.Operation)
			if a.Kind == RemediationKindMitigation && int(a.Confidence) > maxMitigationConfidence {
				a.Confidence = Confidence(maxMitigationConfidence)
			}
			// Applied from the card, not by a command.
			a.ExecuteCommand, a.VerifyCommand, a.RollbackCommand = "", "", ""
			kept = append(kept, *a)
			continue
		}

		if a.Automation != nil {
			// It runs the automation (checkPlanAutomations kept only a valid one); there is no command to check or undo.
			a.VerifyCommand, a.RollbackCommand = "", ""
			kept = append(kept, *a)
			continue
		}
		if strings.TrimSpace(a.ExecuteCommand) == "" {
			// Command-less -> kept only with an artifact or a stated required change (code / chart / process
			// fixes are still the fix); otherwise it points at nothing.
			if !hasArtifact && !a.RequiredChange.actionable() {
				notes = append(notes, planValidationNote{Action: a.Action, Rule: "no_command_and_no_artifact_dropped", Detail: a.Title})
				continue
			}
			// Verify and rollback describe checking and undoing a command that ran. Nothing ran here,
			// so they would be run against unchanged state and report on something else entirely.
			a.VerifyCommand = ""
			a.RollbackCommand = ""
		}
		kept = append(kept, *a)
	}
	plan.Actions = kept

	// Deterministic command contract. Runs after kind and confidence are canonical (so a demotion
	// here is not undone) and before ranking (so a demoted action also moves down the list). See
	// remediation_validate.go for why these are checks rather than additional prompt rules.
	notes = append(notes, validatePlanCommands(plan)...)

	// Rank by what the action is worth, not by how confident the model felt: anything that removes
	// the cause outranks anything that merely restores service, however reliable the latter is. The
	// prompt asks for this ordering and the model does not reliably obey it, so enforce it here.
	// Stable, so the model's own ordering survives within each group as a tiebreak.
	sort.SliceStable(plan.Actions, func(i, j int) bool {
		iFix := plan.Actions[i].Kind == RemediationKindFix
		jFix := plan.Actions[j].Kind == RemediationKindFix
		if iFix != jFix {
			return iFix
		}
		// A step that gives something up comes after every one that doesn't.
		if iCost, jCost := plan.Actions[i].TradeOff != "", plan.Actions[j].TradeOff != ""; iCost != jCost {
			return jCost
		}
		return plan.Actions[i].Confidence > plan.Actions[j].Confidence
	})
	return append(notes, keepDependentsAfter(plan)...)
}

// finalizePlan runs the inventory checks and then normalizePlan; use it for any plan the model wrote or
// rewrote, so no path skips a check. replicas may be nil when inventory is unavailable.
func finalizePlan(plan *RemediationPlan, availableArtifacts []string, alertRule *RemediationAlertRule, replicas func(kind, name, ns string) string,
	automations []tools.AutomationOption) []planValidationNote {
	if replicas != nil {
		markScaleFromZero(plan, replicas)
	}
	notes := checkPlanAutomations(plan, automations)
	return append(notes, normalizePlan(plan, availableArtifacts, alertRule)...)
}

// keepDependentsAfter places each action directly after the one its After names, so ranking never
// moves the second half of a change ahead of, or away from, its first. After naming no kept action
// (dropped, misspelt, itself, or a cycle) is cleared: the action is then an alternative like any other.
func keepDependentsAfter(plan *RemediationPlan) []planValidationNote {
	notes := []planValidationNote{}
	index := map[string]int{}
	for i, a := range plan.Actions {
		index[strings.ToLower(strings.TrimSpace(a.Action))] = i
	}
	parent := func(i int) (int, bool) {
		p, ok := index[strings.ToLower(strings.TrimSpace(plan.Actions[i].After))]
		return p, ok && p != i
	}
	for i := range plan.Actions {
		if strings.TrimSpace(plan.Actions[i].After) == "" {
			continue
		}
		// Walk up: a name that is not kept, or a loop back to i, means there is no order to keep. A loop
		// further up is cleared when its own members are visited, so i keeps its order.
		ok := true
		seen := map[int]bool{i: true}
		for j := i; ; {
			p, found := parent(j)
			if !found || p == i {
				ok = false
				break
			}
			if seen[p] || strings.TrimSpace(plan.Actions[p].After) == "" {
				break
			}
			seen[p] = true
			j = p
		}
		if !ok {
			notes = append(notes, planValidationNote{Action: plan.Actions[i].Action, Rule: "after_cleared", Detail: plan.Actions[i].After})
			plan.Actions[i].After = ""
		}
	}
	// The card matches After against the name as written, so store the parent's exact name.
	for i := range plan.Actions {
		if p, ok := parent(i); ok && strings.TrimSpace(plan.Actions[i].After) != "" {
			plan.Actions[i].After = plan.Actions[p].Action
		}
	}
	children := map[int][]int{}
	roots := []int{}
	for i := range plan.Actions {
		if p, ok := parent(i); ok && strings.TrimSpace(plan.Actions[i].After) != "" {
			children[p] = append(children[p], i)
		} else {
			roots = append(roots, i)
		}
	}
	ordered := make([]RemediationAction, 0, len(plan.Actions))
	var visit func(int)
	visit = func(i int) {
		ordered = append(ordered, plan.Actions[i])
		for _, c := range children[i] {
			visit(c)
		}
	}
	for _, r := range roots {
		visit(r)
	}
	plan.Actions = ordered
	return notes
}

// shellSafeArg matches an argument that needs no quoting.
var shellSafeArg = regexp.MustCompile(`^[A-Za-z0-9_./:=,@%+-]+$`)

// commandFromArgv joins an argument list for the executor: plain args as-is, others single-quoted, or
// double-quoted if they hold a '. ok=false when neither quoting carries the arg literally.
func commandFromArgv(argv []string) (string, bool) {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		switch {
		case shellSafeArg.MatchString(arg):
			parts = append(parts, arg)
		case !strings.Contains(arg, "'"):
			parts = append(parts, "'"+arg+"'")
		case !strings.ContainsAny(arg, "\"$`\\"):
			parts = append(parts, `"`+arg+`"`)
		default:
			return "", false
		}
	}
	return strings.Join(parts, " "), true
}

// applyArgv sets each command of an action from its argument list, when the model gave one. An
// argument list that cannot be expressed is reported, and leaves the command as the model wrote it (usually empty).
func applyArgv(a *RemediationAction) []planValidationNote {
	notes := []planValidationNote{}
	for _, slot := range []struct {
		argv []string
		cmd  *string
	}{{a.ExecuteArgv, &a.ExecuteCommand}, {a.VerifyArgv, &a.VerifyCommand}, {a.RollbackArgv, &a.RollbackCommand}} {
		if len(slot.argv) == 0 {
			continue
		}
		cmd, ok := commandFromArgv(slot.argv)
		if !ok {
			// Keep any command the model also wrote; with none, the slot stays empty.
			notes = append(notes, planValidationNote{Action: a.Action, Rule: "argv_not_representable", Detail: strings.Join(slot.argv, " ")})
			continue
		}
		*slot.cmd = cmd
	}
	return notes
}

// RemediationPlan is the structured plan returned to the UI: the root cause, the candidate hypotheses
// considered, and a ranked set of recovery actions. When the investigation does not support a
// confident root cause, Actions is empty and Summary explains why no remediation is recommended.
type RemediationPlan struct {
	RootCause  string                  `json:"root_cause"`
	Summary    string                  `json:"summary"`
	Hypotheses []RemediationHypothesis `json:"hypotheses"`
	Actions    []RemediationAction     `json:"actions"`
	// OptionalHardening lists improvements the incident does not require. Never ranked with actions.
	OptionalHardening []HardeningItem `json:"optional_hardening,omitempty"`
	// OutcomeCheck reads the incident's own signal after the last step (remediation_outcome.go).
	OutcomeCheck *OutcomeCheck `json:"outcome_check,omitempty"`
	// Dropped lists alert-rule actions the server removed and why. Not rendered.
	Dropped []DroppedAction `json:"dropped,omitempty"`
	// PlanVersion is the hash of the stored plan text (events.PlanVersion), set when a stored plan is
	// returned. Every decision on a step is recorded against it. Never stored: it is empty when saved.
	PlanVersion string `json:"plan_version,omitempty"`
	// Target is where the plan's commands run, from the event's account, shown in the confirmation.
	// Set when a plan is returned; never stored.
	Target *RemediationTarget `json:"target,omitempty"`
	// CauseKey names the plan's cause so the alert's plans compare without a model call (events.CauseKey, C8):
	// stored normalized, with empty fields when the cause is unclear, on every plan written with the loop on.
	CauseKey *events.CauseKey `json:"cause_key,omitempty"`
	// The rest are set on every plan written with the loop on (stampLoopPlan, remediation_planning.go).
	// GeneratedAt is when this version was written; Version counts the event's plans from 1.
	GeneratedAt *time.Time `json:"generated_at,omitempty"`
	Version     int        `json:"version,omitempty"`
	// Goal is the "done when", in one plain sentence, from the alert's own signal; the model writes it.
	Goal string `json:"goal,omitempty"`
	// Revision is what changed from the event's previous plan, and the request that wrote this one.
	Revision *PlanRevision `json:"revision,omitempty"`
}

// RemediationExecuteRequest is the payload for ai_remediation_execute (one command per play-button click).
type RemediationExecuteRequest struct {
	AccountId  string `json:"account_id"`
	EventId    string `json:"event_id"`
	Command    string `json:"command"`
	ConfigName string `json:"config_name"`
	// Slot is which of an action's three commands this is: execute, verify or rollback. Only the
	// execute command changes state towards resolving the event, so only it is recorded as a
	// resolution — running an action used to file three, including a read-only verify and a rollback
	// that undid the fix. Every slot is still audited. Empty is treated as execute so a caller that
	// omits it keeps its resolution.
	Slot string `json:"slot"`
	// ExecuteCommand is the execute slot's command, sent when Slot is "verify". It is the
	// correlation key back to the attempt being verified: the execute run stored it as the
	// resolution's type_reference_id. Without it a verify result has no attempt to attach to.
	ExecuteCommand string `json:"execute_command"`
	// Action names the plan action an operator marked done (slot "manual"); it identifies a
	// command-less source change, which has no command to match.
	Action string `json:"action"`
	// AcknowledgeUnknown: the person confirmed that the last run of this command, whose outcome is
	// unknown, may have run. Only then (and 10 minutes after that run) is it sent again.
	AcknowledgeUnknown bool `json:"acknowledge_unknown"`
	// PlanVersion is the plan version the person confirmed. Every decision (Run, Decline, "I ran this")
	// carries it, and is refused when the event's plan is no longer that version.
	PlanVersion string `json:"plan_version"`
	// Reason is the person's optional reason for declining a step.
	Reason string `json:"reason"`
	// Step is the plan step (0-based) a Slack step message decides, sent in place of Command and Action.
	Step *int `json:"step"`
	// Source is where the person decided: empty for the remediation panel, "slack" for the alert's Slack
	// thread. It only names the surface on the record; who decided is the caller's x-user-id either way.
	Source string `json:"source"`
	// ChatApproval is the approval a step decided in the alert's chat was given under (run_step with the
	// loop on); the decision is recorded under it (C1). Never read from a request body.
	ChatApproval *chatStepApproval `json:"-"`
}

// Command slots within one action.
const (
	RemediationSlotExecute  = "execute"
	RemediationSlotVerify   = "verify"
	RemediationSlotRollback = "rollback"
	// RemediationSlotManual records that the operator carried out an action outside NudgeBee; nothing runs.
	RemediationSlotManual = "manual"
	// RemediationSlotDecline records that a person declined an action; nothing runs.
	RemediationSlotDecline = "decline"
)

// processRemediationGenerate turns the completed investigation into a structured remediation plan
// (root cause + execute/verify/rollback commands). It reuses the LLM generation + JSON-extraction
// infra; only the prompt (PromptToolRemediationGenerateJson) is remediation-specific. RBAC: read.
func processRemediationGenerate(c *gin.Context, tracer trace.Tracer, meter metric.Meter) {
	var request RemediationGenerateRequest
	var actionRequest ActionRequest
	if err := c.ShouldBindJSON(&actionRequest); err != nil {
		slog.Error(errorBindingMessage, "error", err)
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	payload := actionRequest.Input
	if v, ok := payload["request"].(map[string]any); ok {
		payload = v
	}
	if err := common.DecodeMapToStruct(payload, &request); err != nil {
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	logger := slog.With("account_id", request.AccountId, "event_id", request.EventId)
	ctx, err := BuildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
	if err != nil {
		c.JSON(500, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	sc := ctx.GetSecurityContext()
	if sc == nil || (!sc.HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) &&
		!grantedRun(sc, request.AccountId, moduleAiMisc)) {
		c.JSON(403, BuildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
		return
	}

	// With the loop on Nubi writes the plan in the alert's conversation; asking here queues that turn.
	if events.RemediationLoopOn(request.AccountId) {
		respondGenerateWithLoop(c, ctx, request.AccountId, request.EventId)
		return
	}

	// One plan per alert at a time: a plan already being built for it (after its investigation, or by
	// another open page) is the answer when it ends, rather than a second model call and a second plan.
	waited, err := planBuilds.claim(ctx.GetContext(), request.EventId)
	if err != nil {
		c.JSON(503, BuildApiResponse(nil, []error{common.Error{Message: "a remediation plan is still being built for this alert; try again shortly"}}))
		return
	}
	defer planBuilds.release(request.EventId)
	if waited {
		if built, version, current := loadSlackPlan(ctx, request.AccountId, request.EventId); current {
			respondWithStoredPlan(c, ctx.GetLogger(), built, func() (string, error) { return version, nil })
			return
		}
	}

	conversationId := remediationConversationID(ctx, request.AccountId, request.EventId)
	plan, trace, _, err := newRemediationPlanWith(ctx, request, conversationId, remediationLLM(ctx, ctx.GetSecurityContext().GetUserId(), request.AccountId, conversationId))
	if err != nil {
		var input errRemediationInput
		if errors.As(err, &input) {
			c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}
		ctx.GetLogger().Error("remediation_generate: failed", "error", err)
		c.JSON(500, BuildApiResponse(nil, []error{common.Error{Message: "failed to generate remediation plan"}}))
		return
	}

	// The planner model has no turn, so nothing backs a source it wrote.
	trace.Validation = append(trace.Validation, resolvePlanSources(ctx, request.AccountId, planTurn{}, &plan)...)
	respondWithStoredPlan(c, ctx.GetLogger(), plan, func() (string, error) {
		return storeRemediationPlan(ctx, conversationId, request, plan, trace)
	})
}

// newRemediationPlanWith generates the event's plan for request, written by llm: the planner model, or an
// agent's own plan (writtenPlan).
func newRemediationPlanWith(ctx *security.RequestContext, request RemediationGenerateRequest, conversationId string, llm func(system, human string) (string, error)) (RemediationPlan, RemediationTrace, string, error) {
	plan, trace, err := generateRemediationPlan(ctx, request, llm)
	if err != nil {
		return RemediationPlan{}, RemediationTrace{}, "", err
	}
	assignDelivery(ctx, request.AccountId, &plan)
	plan.Target = targetOrNil(ctx, request.AccountId)
	inputs := request
	trace.Request = &inputs
	return plan, trace, conversationId, nil
}

// storeRemediationPlan stores a plan from newRemediationPlanWith and adds it to the event's conversation.
func storeRemediationPlan(ctx *security.RequestContext, conversationId string, request RemediationGenerateRequest, plan RemediationPlan, trace RemediationTrace) (string, error) {
	version, err := persistRemediationPlan(ctx, request.AccountId, request.EventId, plan, trace)
	if err != nil {
		return "", err
	}
	appendPlanToConversation(ctx, conversationId, request, plan)
	return version, nil
}

// appendPlanToConversation adds the plan as the event conversation's closing message, after the
// summary / RCA / log-analysis ones, so Ask Nudgebee shows it too. Best effort: the plan is already
// stored, and the investigation page does not read this message. A var so tests can count it.
var appendPlanToConversation = func(ctx *security.RequestContext, conversationId string, request RemediationGenerateRequest, plan RemediationPlan) {
	dao := agentcore.GetConversationDao()
	if conversationId == "" || dao == nil {
		return
	}
	// No user id: nobody typed this message, and the investigation messages beside it carry none either.
	_, err := dao.SaveConversationMessage("", conversationId, request.AccountId, "", agentcore.MessageRoleHuman, agentcore.MessageTypeGeneration,
		"Suggest a remediation for the event with id "+request.EventId, planMarkdown(plan), remediationPlannerAgent, uuid.Nil, nil, "", "", "", agentcore.ConversationStatusCompleted)
	if err != nil {
		ctx.GetLogger().Warn("remediation_generate: plan not appended to conversation", "conversation_id", conversationId, "error", err)
	}
}

// planMarkdown renders the plan for the chat transcript: summary, root cause, then each action
// with its commands.
func planMarkdown(plan RemediationPlan) string {
	var b strings.Builder
	b.WriteString("## Remediation plan\n\n" + plan.Summary)
	if plan.RootCause != "" {
		fmt.Fprintf(&b, "\n\n### Root cause\n%s", plan.RootCause)
	}
	if len(plan.Actions) > 0 {
		b.WriteString("\n\n### Recommended actions")
	}
	for i, a := range plan.Actions {
		name, detail := a.Action, a.Title
		if name == "" {
			name, detail = a.Title, ""
		}
		badge := "⚠️ **Mitigation**"
		if a.Kind == RemediationKindFix {
			badge = "✅ **Fix**"
		}
		if i > 0 {
			b.WriteString("\n\n---")
		}
		fmt.Fprintf(&b, "\n\n#### %d. %s\n%s · %d%% confidence", i+1, name, badge, int(a.Confidence))
		if detail != "" && detail != name {
			fmt.Fprintf(&b, "\n\n%s", detail)
		}
		if a.Automation != nil {
			fmt.Fprintf(&b, "\n\n**Runs automation** %s", automationLine(*a.Automation))
		}
		for _, cmd := range [][2]string{{"Run", a.ExecuteCommand}, {"Verify", a.VerifyCommand}, {"Rollback", a.RollbackCommand}} {
			if cmd[1] != "" {
				fmt.Fprintf(&b, "\n\n**%s**\n```bash\n%s\n```", cmd[0], cmd[1])
			}
		}
		if a.RequiredChange.actionable() {
			where := ""
			if a.RequiredChange.Where != "" {
				where = " (" + a.RequiredChange.Where + ")"
			}
			fmt.Fprintf(&b, "\n\n**Change in source** · `%s`%s\n%s", a.RequiredChange.Target, where, a.RequiredChange.Change)
		}
		if a.TradeOff != "" {
			fmt.Fprintf(&b, "\n\n> **Trade-off:** %s", a.TradeOff)
		}
		if line := sourceMarkdown(a.Source); line != "" {
			fmt.Fprintf(&b, "\n\n%s", line)
		}
	}
	return b.String()
}

// respondWithStoredPlan returns the plan only once it is stored. The stored plan is what Run is checked
// against (planCommandRejection), so a plan that could not be saved has commands that cannot run.
func respondWithStoredPlan(c *gin.Context, logger *slog.Logger, plan RemediationPlan, save func() (string, error)) {
	version, err := save()
	if err != nil {
		logger.Error("remediation_generate: failed to persist plan", "error", err)
		c.JSON(500, BuildApiResponse(nil, []error{common.Error{Message: "failed to save remediation plan"}}))
		return
	}
	plan.PlanVersion = version
	c.JSON(200, BuildApiResponse(plan, nil))
}

// remediationPlannerAgent names the plan-generation call in token usage, which
// otherwise records it with an empty agent name.
const remediationPlannerAgent = "remediation_planner"

// remediationEventFingerprint and remediationConversationBySession are the
// lookups remediationConversationID needs; vars so tests can stub them.
var remediationEventFingerprint = func(ctx *security.RequestContext, accountId, eventId string) (string, bool, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", false, fmt.Errorf("database: %w", err)
	}
	info, err := events.NewEventAnalysisRepository(dbManager).GetEventInfo(ctx, eventId, accountId)
	if err != nil {
		return "", false, fmt.Errorf("event: %w", err)
	}
	if info == nil {
		return "", false, nil
	}
	return info.Fingerprint, true, nil
}

var remediationConversationBySession = func(accountId, sessionId string) (agentcore.Conversation, error) {
	dao := agentcore.GetConversationDao()
	if dao == nil {
		return agentcore.Conversation{}, errors.New("conversation dao unavailable")
	}
	return dao.GetConversationBySession(accountId, sessionId)
}

// remediationConversationID returns the event's investigation conversation, so
// plan generation counts against the same budget as the investigation it
// builds on. The investigation's session is keyed on the event fingerprint (see
// event_analyzer.go), falling back to the event id when there is none. Best
// effort: on any miss the call is recorded unattributed, as before.
func remediationConversationID(ctx *security.RequestContext, accountId, eventId string) string {
	if eventId == "" {
		return ""
	}
	logMiss := func(err error) string {
		ctx.GetLogger().Warn("remediation_generate: usage not attributed to a conversation",
			"event_id", eventId, "account_id", accountId, "error", err)
		return ""
	}
	fingerprint, found, err := remediationEventFingerprint(ctx, accountId, eventId)
	if err != nil {
		return logMiss(err)
	}
	if !found {
		return ""
	}
	if fingerprint == "" {
		fingerprint = eventId
	}
	conversation, err := remediationConversationBySession(accountId, events.SessionIdPrefixEvent+fingerprint)
	if err != nil {
		return logMiss(fmt.Errorf("conversation: %w", err))
	}
	if conversation.ID == uuid.Nil {
		return "" // no investigation conversation for this event
	}
	return conversation.ID.String()
}

// remediationLLM is the production model call for plan generation.
func remediationLLM(ctx *security.RequestContext, userId, accountId, conversationId string) func(system, human string) (string, error) {
	return func(system, human string) (string, error) {
		// conversationId only bills the call; a one-shot plan prompt must not
		// create a conversation cache entry.
		resp, err := llmruntime.GenerateAndTrackLLMContent(toolcore.WithPromptCachingDisabled(ctx), userId, accountId, conversationId, "", remediationPlannerAgent, false, []llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: system}}},
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: human}}},
		}, true)
		if err != nil {
			return "", err
		}
		if resp == nil || len(resp.Choices) == 0 {
			return "", errors.New("llm returned no choices")
		}
		return resp.Choices[0].Content, nil
	}
}

// errRemediationInput is a request the caller has to fix; everything else is a server failure.
type errRemediationInput struct{ msg string }

func (e errRemediationInput) Error() string { return e.msg }

// RemediationTrace records exactly what produced a plan, stored beside it. Without it a wrong plan
// cannot be explained: the audit could not recover the input of two of its six failed plans.
type RemediationTrace struct {
	EvidenceSource string               `json:"evidence_source"`
	Prompt         string               `json:"prompt"`
	PromptSHA256   string               `json:"prompt_sha256"`
	HumanMessage   string               `json:"human_message"`
	RawOutput      string               `json:"raw_output"`
	ModelPlan      *RemediationPlan     `json:"model_plan,omitempty"`
	Validation     []planValidationNote `json:"validation"`
	GeneratedAt    time.Time            `json:"generated_at"`
	// The read-and-repair pass (remediation_targets.go): what was read, how long it took, and, when
	// the plan was rewritten against it, the draft it replaced and the repair's own output and notes.
	TargetReads      []targetRead         `json:"target_reads,omitempty"`
	TargetReadMillis int64                `json:"target_read_ms,omitempty"`
	DraftPlan        *RemediationPlan     `json:"draft_plan,omitempty"`
	RepairRawOutput  string               `json:"repair_raw_output,omitempty"`
	RepairValidation []planValidationNote `json:"repair_validation,omitempty"`
	// Request is what the plan was generated from: the browser's context, artifacts and alert rule.
	Request *RemediationGenerateRequest `json:"request,omitempty"`
	// StateReadRequests are the reads the draft asked for (TargetReads says what each returned,
	// including those refused).
	StateReadRequests []stateReadRequest `json:"state_read_requests,omitempty"`
}

// generateRemediationPlan turns this event's investigation into a validated plan. llm is the model
// call, injected so the replay harness exercises this exact path with the real model and no writes.
func generateRemediationPlan(ctx *security.RequestContext, request RemediationGenerateRequest, llm func(system, human string) (string, error)) (RemediationPlan, RemediationTrace, error) {
	trace := RemediationTrace{Prompt: string(prompts.PromptRemediationGenerateJson), GeneratedAt: time.Now().UTC()}
	evidence, hasCodeFix, err := remediationEvidence(ctx, request.AccountId, request.EventId, strings.TrimSpace(request.Context))
	if err != nil {
		return RemediationPlan{}, trace, err
	}
	trace.EvidenceSource = evidenceFromStoredAnalysis
	artifacts := remediationArtifacts(request.AvailableArtifacts, hasCodeFix)
	request.automations = planAutomations(request.AccountId)
	if strings.TrimSpace(evidence) == "" {
		return RemediationPlan{}, trace, errRemediationInput{"investigation context is required"}
	}

	systemPrompt, err := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptRemediationGenerateJson, request.AccountId)
	if err != nil {
		return RemediationPlan{}, trace, fmt.Errorf("loading prompt: %w", err)
	}
	sum := sha256.Sum256([]byte(systemPrompt))
	trace.PromptSHA256 = hex.EncodeToString(sum[:])

	// The prompt asks the model to match the CLI to what the action targets and to put the event's
	// region on every cloud command, but the only thing it can infer either from is the investigation
	// prose — which names the provider rarely and the region inconsistently. Stating both outright
	// makes them facts rather than guesses: it is what stops a kubectl command being proposed against
	// an EC2 instance, and what stops an `aws` command going out without --region and failing with
	// "You must specify a region" having done nothing.
	var preamble []string
	if account := describeRemediationAccount(request.AccountId, request.EventId); account != "" {
		preamble = append(preamble, account)
	}
	// The rest of what a runnable, non-repeating plan needs and the prose does not carry: how long
	// this has been going on, exactly what to act on, the target's current configuration, and what
	// has already been tried. See remediation_context.go.
	facts, ev := buildRemediationFacts(ctx, request.AccountId, request.EventId)
	if facts != "" {
		preamble = append(preamble, facts)
	}
	trace.HumanMessage = strings.Join(append(preamble, evidence), "\n\n")

	raw, err := llm(systemPrompt, trace.HumanMessage)
	trace.RawOutput = raw
	if err != nil {
		return RemediationPlan{}, trace, fmt.Errorf("llm generation: %w", err)
	}
	var plan RemediationPlan
	if err := common.ExtractAndUnmarshalJSON([]byte(raw), &plan); err != nil {
		return RemediationPlan{}, trace, fmt.Errorf("parsing plan json: %w", err)
	}
	modelPlan := plan
	modelPlan.Actions = append([]RemediationAction(nil), plan.Actions...)
	trace.ModelPlan = &modelPlan

	trace.Validation = finalizePlan(&plan, artifacts, request.AlertRule, inventoryReplicas(request.AccountId), request.automations)
	plan = repairAgainstTargetState(ctx, request, llm, systemPrompt, plan, &trace, artifacts)
	for _, d := range plan.Dropped {
		ctx.GetLogger().Info("remediation_generate: dropped alert-rule action", "action", d.Action, "reason", d.Reason)
	}
	// Last, so it describes the plan that is returned.
	if len(plan.Actions) > 0 {
		plan.OutcomeCheck = deriveOutcomeCheck(ev, tools.GetCloudProviderForAccount(request.AccountId))
	}
	return plan, trace, nil
}

// processRemediationGet returns the saved remediation plan for an event (or an empty plan when none
// has been generated yet), so the panel can restore it on reload instead of regenerating. RBAC: read.
func processRemediationGet(c *gin.Context, tracer trace.Tracer, meter metric.Meter) {
	var request RemediationGenerateRequest
	var actionRequest ActionRequest
	if err := c.ShouldBindJSON(&actionRequest); err != nil {
		slog.Error(errorBindingMessage, "error", err)
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	payload := actionRequest.Input
	if v, ok := payload["request"].(map[string]any); ok {
		payload = v
	}
	if err := common.DecodeMapToStruct(payload, &request); err != nil {
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	logger := slog.With("account_id", request.AccountId, "event_id", request.EventId)
	ctx, err := BuildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
	if err != nil {
		c.JSON(500, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	sc := ctx.GetSecurityContext()
	if sc == nil || (!sc.HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) &&
		!granted(sc, request.AccountId, moduleAiMisc, "Read", "Write")) {
		c.JSON(403, BuildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
		return
	}

	var plan RemediationPlan
	repo, info, err := remediationReadEvent(ctx, request.AccountId, request.EventId)
	if err != nil {
		// A read that could not be made is an error, not an empty answer: an empty answer has no loop state,
		// and the tab would take it for "the loop is off" and fall back to the one-shot panel, which plans on
		// mount.
		ctx.GetLogger().Warn("remediation_get: not read", "error", err)
		c.JSON(503, BuildApiResponse(nil, []error{common.Error{Message: "remediation could not be read, try again"}}))
		return
	}
	if info == nil {
		c.JSON(200, BuildApiResponse(plan, nil))
		return
	}
	// With the loop on, a duplicate shows its main event's plan: planEvent and planInfo are the event whose
	// plan this read returns (the event itself otherwise).
	planEvent, planInfo := planEventAndInfo(ctx, repo, request.AccountId, request.EventId, info)
	saved, err := repo.GetEventAnalysis(ctx, planEvent, planInfo.Fingerprint, planInfo.AggregationKey, request.AccountId, events.AnalysisTypeRemediation)
	if err != nil || saved == nil || strings.TrimSpace(saved.Analysis) == "" {
		c.JSON(200, BuildApiResponse(remediationGetBody(ctx, request.AccountId, request.EventId, info, planEvent, planInfo, plan), nil))
		return
	}
	// When this event has no plan of its own, the lookup falls back to the newest plan saved for the
	// same alert - one generated from an earlier firing's investigation. The panel treats any plan it
	// gets back as already generated, so restoring that one shows the operator another incident's root
	// cause, confidence and commands, and never builds a plan for this incident. Returning no plan here
	// makes the panel generate one from this event's own investigation.
	if !savedPlanShown(request.AccountId, planEvent, saved, func() bool {
		return planPredatesAnalysis(ctx, repo, planEvent, request.AccountId, planInfo, saved)
	}) {
		c.JSON(200, BuildApiResponse(remediationGetBody(ctx, request.AccountId, request.EventId, info, planEvent, planInfo, plan), nil))
		return
	}
	if err := json.Unmarshal([]byte(saved.Analysis), &plan); err != nil {
		ctx.GetLogger().Warn("remediation_get: failed to parse saved plan", "error", err)
		plan = RemediationPlan{}
	} else {
		plan.PlanVersion = events.PlanVersion(saved.Analysis)
		plan.Target = targetOrNil(ctx, request.AccountId)
	}
	// Delivery describes the executor now, not when the plan was saved.
	assignDelivery(ctx, request.AccountId, &plan)
	c.JSON(200, BuildApiResponse(remediationGetBody(ctx, request.AccountId, request.EventId, info, planEvent, planInfo, plan), nil))
}

// remediationReadEvent reads what the remediation read starts from: the event, and that the loop flag can be
// read. err means a read failed, which the caller must not answer as "no plan"; a nil info with no error means
// the event does not exist (for this account).
func remediationReadEvent(ctx *security.RequestContext, accountId, eventId string) (*events.EventAnalysisRepository, *events.EventInfo, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, nil, fmt.Errorf("database unavailable: %w", err)
	}
	loopOn, err := events.RemediationLoopRead(accountId)
	if err != nil {
		return nil, nil, fmt.Errorf("loop flag not read: %w", err)
	}
	repo := events.NewEventAnalysisRepository(dbManager)
	info, err := repo.GetEventInfo(ctx, eventId, accountId)
	if errors.Is(err, events.ErrEventNotFound) {
		return repo, nil, nil
	}
	if err != nil {
		if !loopOn {
			// With the loop off the read answers as it always did: no plan, which the one-shot panel handles.
			ctx.GetLogger().Warn("remediation_get: event not read", "error", err)
			return repo, nil, nil
		}
		return nil, nil, fmt.Errorf("event not read: %w", err)
	}
	return repo, info, nil
}

// planPredatesAnalysis reports whether a completed analysis the stored plan was built from (the
// investigation, its report) was re-run after the plan was saved.
func planPredatesAnalysis(ctx *security.RequestContext, repo *events.EventAnalysisRepository, eventId, accountId string, info *events.EventInfo, saved *events.EventAnalysis) bool {
	var inputs []*events.EventAnalysis
	for _, t := range []events.EventAnalysisType{events.AnalysisTypeInvestigation, events.AnalysisTypeDetailedResponse} {
		a, err := repo.GetEventAnalysis(ctx, eventId, info.Fingerprint, info.AggregationKey, accountId, t)
		if err != nil {
			ctx.GetLogger().Warn("remediation: unable to read analysis to check the plan's age", "error", err, "analysis_type", t)
			continue
		}
		if a != nil {
			inputs = append(inputs, a)
		}
	}
	return planOlderThanAnalysis(saved, inputs...)
}

// planCommandRejection returns why a command may not run, or "" when it is the stored plan's command for
// its slot. A verify must name its own action's execute, so its result lands on the right attempt.
// It also returns the plan it read and its version, so the caller does not read it again. A decision
// also re-checks the version inside its claim, under FOR SHARE (ClaimRemediationDecision).
func planCommandRejection(ctx *security.RequestContext, request RemediationExecuteRequest) (stored storedPlan, reason string) {
	reason = planCommandRejectionFor(request, func() (RemediationPlan, bool) {
		var ok bool
		stored, ok = loadSavedPlan(ctx, request.AccountId, request.EventId)
		return stored.Plan, ok
	})
	return stored, reason
}

// planCommandRejectionFor is planCommandRejection with the stored-plan read passed in.
func planCommandRejectionFor(request RemediationExecuteRequest, loadPlan func() (RemediationPlan, bool)) string {
	if strings.TrimSpace(request.EventId) == "" {
		return "a remediation command runs only as part of an event's plan"
	}
	plan, ok := loadPlan()
	if !ok {
		return "this event has no remediation plan to run commands from"
	}
	if slot := strings.ToLower(strings.TrimSpace(request.Slot)); slot == RemediationSlotManual || slot == RemediationSlotDecline {
		if planOperatorStep(plan, request.Command, request.Action) {
			return ""
		}
		return "this step is not part of the event's remediation plan"
	}
	if isOutcomeSlot(request.Slot) {
		if plan.OutcomeCheck != nil && strings.TrimSpace(plan.OutcomeCheck.Command) == strings.TrimSpace(request.Command) {
			return ""
		}
		return "this is not the plan's outcome check"
	}
	if planHasCommand(plan, request.Slot, request.Command, request.ExecuteCommand) {
		return ""
	}
	return "this command is not part of the event's remediation plan"
}

// planHasCommand reports whether command is the plan's command for that slot.
func planHasCommand(plan RemediationPlan, slot, command, executeCommand string) bool {
	command, executeCommand = strings.TrimSpace(command), strings.TrimSpace(executeCommand)
	for _, a := range plan.Actions {
		switch strings.ToLower(strings.TrimSpace(slot)) {
		case "", RemediationSlotExecute:
			if stepRef(a) != "" && stepRef(a) == command {
				return true
			}
		case RemediationSlotVerify:
			if strings.TrimSpace(a.VerifyCommand) == command && strings.TrimSpace(a.ExecuteCommand) == executeCommand {
				return true
			}
		case RemediationSlotRollback:
			if strings.TrimSpace(a.RollbackCommand) == command {
				return true
			}
		}
	}
	return false
}

// loadSavedPlan returns the plan stored for this event itself (not one inherited from an earlier
// firing of the same alert), and its version. With the loop on, a duplicate's plan is its main
// event's (planEventAndInfo): duplicates never get a plan of their own.
func loadSavedPlan(ctx *security.RequestContext, accountId, eventId string) (storedPlan, bool) {
	var stored storedPlan
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return stored, false
	}
	repo := events.NewEventAnalysisRepository(dbManager)
	info, err := repo.GetEventInfo(ctx, eventId, accountId)
	if err != nil || info == nil {
		return stored, false
	}
	eventId, info = planEventAndInfo(ctx, repo, accountId, eventId, info)
	saved, err := repo.GetEventAnalysis(ctx, eventId, info.Fingerprint, info.AggregationKey, accountId, events.AnalysisTypeRemediation)
	if err != nil || saved == nil || !planSavedForEvent(saved, eventId) {
		return stored, false
	}
	if err := json.Unmarshal([]byte(saved.Analysis), &stored.Plan); err != nil {
		return stored, false
	}
	stored.Version, stored.RowId = events.PlanVersion(saved.Analysis), saved.ID
	stored.Stale = planPredatesAnalysis(ctx, repo, eventId, accountId, info, saved)
	return stored, true
}

// storedPlan is the event's stored plan as a decision binds it.
type storedPlan struct {
	Plan RemediationPlan
	// Version is the hash of the stored text; RowId the event_log_analysis row holding it.
	Version, RowId string
	// Stale: an analysis the plan was built from was re-run after it was saved.
	Stale bool
}

// planSavedForEvent reports whether a stored plan was generated for eventId. A row with no event id
// cannot be attributed to any incident, so it is not restored either.
func planSavedForEvent(saved *events.EventAnalysis, eventId string) bool {
	owner := strings.TrimSpace(saved.RelatedEventId)
	return owner != "" && strings.EqualFold(owner, strings.TrimSpace(eventId))
}

// savedPlanShown reports whether the remediation read returns the saved plan of planEvent:
//   - Only a plan saved for planEvent itself. Otherwise the lookup fell back to the newest plan saved for
//     the same alert, one generated from an earlier firing's investigation; the panel treats any plan it
//     gets back as already generated, so restoring that one shows another incident's root cause and
//     commands. Returning no plan makes the panel generate one from this event's own investigation.
//   - With the loop off, not a plan older than a re-run of the analysis it was built from (predates): it
//     describes the previous analysis (e.g. the report said "configuration error", the
//     restored plan still said "undetermined"), and returning none makes the panel build a fresh one.
//     With the loop on the page never generates: the plan is returned, and the loop's relevance says it
//     is stale (fired again, closed after the plan) so a person can ask Nubi to re-check it.
func savedPlanShown(accountId, planEvent string, saved *events.EventAnalysis, predates func() bool) bool {
	if !planSavedForEvent(saved, planEvent) {
		return false
	}
	if events.RemediationLoopOn(accountId) {
		return true
	}
	if predates() {
		slog.Info("remediation_get: saved plan predates the current analysis, regenerating", "account_id", accountId, "event_id", planEvent)
		return false
	}
	return true
}

// planEventAndInfo is the event whose plan eventId reads and acts on, with its info: with the loop on, the shared
// rule (planEventOf, events.PlanEventOf: a duplicate's live main event, unless the main has no plan and the
// duplicate has one; contract C7 primary_event_id); otherwise eventId and info unchanged.
func planEventAndInfo(ctx *security.RequestContext, repo *events.EventAnalysisRepository, accountId, eventId string, info *events.EventInfo) (string, *events.EventInfo) {
	if !events.RemediationLoopOn(accountId) {
		return eventId, info
	}
	primary := primaryEventId(ctx, accountId, eventId)
	if primary == "" {
		return eventId, info
	}
	primaryInfo, err := repo.GetEventInfo(ctx, primary, accountId)
	if err != nil || primaryInfo == nil {
		return eventId, info
	}
	return primary, primaryInfo
}

// planOlderThanAnalysis reports whether a completed analysis the plan was built from was
// updated after the plan was saved.
func planOlderThanAnalysis(plan *events.EventAnalysis, inputs ...*events.EventAnalysis) bool {
	for _, a := range inputs {
		if a != nil && a.Status == string(events.AnalysisStatusCompleted) && a.UpdatedAt.After(plan.UpdatedAt) {
			return true
		}
	}
	return false
}

// persistRemediationPlan upserts the plan into event_log_analysis under the `remediation` analysis
// type (a sibling of rca_analysis), keyed by the event's fingerprint + aggregation key. It returns the
// stored plan's version.
func persistRemediationPlan(ctx *security.RequestContext, accountId, eventId string, plan RemediationPlan, trace RemediationTrace) (string, error) {
	if eventId == "" {
		return "", errors.New("event id is required")
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", fmt.Errorf("database unavailable: %w", err)
	}
	repo := events.NewEventAnalysisRepository(dbManager)
	info, err := repo.GetEventInfo(ctx, eventId, accountId)
	if err != nil {
		return "", fmt.Errorf("event info unavailable: %w", err)
	}
	if info == nil {
		return "", errors.New("event info unavailable: event not found")
	}
	// The trace rides in the same row; processRemediationGet unmarshals into RemediationPlan and ignores it.
	plan.PlanVersion, plan.Target = "", nil // a version names stored text, and the target is the account's now
	data, err := json.Marshal(struct {
		RemediationPlan
		Generation RemediationTrace `json:"generation"`
	}{plan, trace})
	if err != nil {
		return "", err
	}
	if err := repo.UpsertEventAnalysis(ctx, eventId, string(data), plan.Summary, string(events.AnalysisStatusCompleted), info.Fingerprint, accountId, info.AggregationKey, events.AnalysisTypeRemediation); err != nil {
		return "", err
	}
	syncAlertThread(ctx, accountId, eventId)
	return events.PlanVersion(string(data)), nil
}

// processRemediationExecute runs a single remediation command against the account's cluster via relay.
// The play-button click is the human approval; the server enforces the guardrails independently and
// never trusts the client: it rejects shell metacharacters (no chaining/redirection/substitution),
// requires write RBAC (SecurityAccessTypeCreate) on the target account, and applies the
// destructive-pattern hard block. Mirrors handleWorkspaceExecute but adds the write gate.
func processRemediationExecute(c *gin.Context, tracer trace.Tracer, meter metric.Meter) {
	var request RemediationExecuteRequest
	var actionRequest ActionRequest
	if err := c.ShouldBindJSON(&actionRequest); err != nil {
		slog.Error(errorBindingMessage, "error", err)
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	payload := actionRequest.Input
	if v, ok := payload["request"].(map[string]any); ok {
		payload = v
	}
	if err := common.DecodeMapToStruct(payload, &request); err != nil {
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	slot := strings.ToLower(strings.TrimSpace(request.Slot))
	command := strings.TrimSpace(request.Command)
	manual, decline := slot == RemediationSlotManual, slot == RemediationSlotDecline
	decision := manual || decline || isRunSlot(slot)
	// A command runs on the event's account through that account's own agent; a client cannot choose
	// another tool configuration (another cluster's credentials) for it.
	if strings.TrimSpace(request.ConfigName) != "" {
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: "remediation: config_name is not accepted; a command runs on the event's own account"}}))
		return
	}
	if request.Source != "" && request.Source != slackSource {
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: "remediation: unknown source"}}))
		return
	}
	if decision {
		if code, msg := decisionCallerRejection(c, &actionRequest, payload); code != 0 {
			c.JSON(code, BuildApiResponse(nil, []error{common.Error{Message: msg}}))
			return
		}
		if strings.TrimSpace(request.PlanVersion) == "" {
			c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: "remediation: plan_version is required; review the plan and confirm again"}}))
			return
		}
	}
	// A Slack step message names its step instead (decideStep reads its command from the plan).
	if command == "" && !manual && !decline && (!decision || request.Step == nil) {
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: "command is required"}}))
		return
	}

	logger := slog.With("account_id", request.AccountId)
	ctx, err := BuildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
	if err != nil {
		c.JSON(500, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	// Every remediation command runs against the target account's cluster, so every command requires
	// write (create) access on that account. HasAccountAccess is per-account, so this blocks the
	// cross-account case where a user holds write on one account but only read on the target.
	sc := ctx.GetSecurityContext()
	if sc == nil || (!sc.HasAccountAccess(request.AccountId, security.SecurityAccessTypeCreate) &&
		!grantedRun(sc, request.AccountId, moduleAiMisc)) {
		c.JSON(403, BuildApiResponse(nil, []error{errors.New("remediation: write access is required to run remediation commands")}))
		return
	}

	// The caller gate passed, so this is the person the gateway named.
	if decision {
		decideStep(ctx, request, sc.GetUserId()).write(c)
		return
	}

	if reason := remediationCommandRejection(command); reason != "" {
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: reason}}))
		return
	}
	// Only a command from this event's stored plan runs. Generation is grounded server-side; accepting
	// any command string here would let execution drift from what was generated and reviewed.
	stored, reason := planCommandRejection(ctx, request)
	if reason != "" {
		ctx.GetLogger().Warn("remediation_execute: command is not in the event's plan", "command", command, "reason", reason)
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: "remediation: " + reason}}))
		return
	}
	if reason := commandRunRejection(ctx, command); reason != "" {
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: reason}}))
		return
	}

	// What remains is a verify or the outcome check: a read NudgeBee runs without a confirmation, so only
	// a strict read (checkRejection). Anything else the person checks themselves.
	provider := tools.GetCloudProviderForAccount(request.AccountId)
	if reason := checkRejection(ctx, request.AccountId, provider, command); reason != "" {
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: "remediation: " + reason}}))
		return
	}
	substrate := tools.RemediationSubstrateFor(provider, command)
	var nbTool toolcore.NBTool
	if substrate.RelayTool != "" {
		var found bool
		if nbTool, found = toolcore.GetNBTool(request.AccountId, substrate.RelayTool); !found {
			c.JSON(404, BuildApiResponse(nil, []error{common.Error{Message: "execution tool is not configured for this account"}}))
			return
		}
	}
	sent := sendRemediationCommand(ctx, request.AccountId, sc.GetUserId(), substrate, nbTool, command)
	response, exitCodeReported, registeredToolName := sent.RemediationExecutionResult, sent.ExitCodeReported, sent.Tool
	if sent.Err != nil {
		ctx.GetLogger().Error("remediation_execute: check failed", "error", sent.Err)
		response.Error = firstNonEmpty(response.Stderr, sent.Err.Error())
	} else if !response.Success {
		response.Error = response.Stderr
	}

	// Audit covers every command regardless of slot — that is the record of what was run.
	writeRemediationAudit(ctx, request.AccountId, command, registeredToolName, response.Success, audit.EventActionCreate, "remediation-panel")
	// The outcome check observes the incident; it is recorded on the latest attempt, not as one.
	// planCommandRejection passed, so the plan has this outcome check.
	if isOutcomeSlot(request.Slot) {
		outcome := newOutcomeResult(stored.Plan.OutcomeCheck, response, exitCodeReported)
		recordOutcome(ctx, request.EventId, outcome)
		c.JSON(200, BuildApiResponse(outcome, nil))
		return
	}
	// A verify does not earn its own resolution — that was the triple-counting bug. It annotates
	// the attempt it checked, so "it ran" and "the check confirmed it" are one record.
	if isVerifySlot(request.Slot) && request.EventId != "" && request.ExecuteCommand != "" {
		persistRemediationVerification(ctx, request.EventId, request.ExecuteCommand, command, response, exitCodeReported)
	}
	c.JSON(200, BuildApiResponse(response, nil))
}

// decideStep records a person's decision on a step of the event's stored plan (Run, Decline, "I ran this"),
// from the panel, a Slack card or the alert's chat (run_step), and runs the step for Run. The request names
// the plan version the person saw; userId is that person.
func decideStep(ctx *security.RequestContext, request RemediationExecuteRequest, userId string) stepAnswer {
	slot := strings.ToLower(strings.TrimSpace(request.Slot))
	if code, refusal := loopRunRefusal(request); refusal != "" {
		return refuse(code, refusal)
	}
	stored, hasPlan := loadSavedPlan(ctx, request.AccountId, request.EventId)
	if request.Step != nil && strings.TrimSpace(request.Command) == "" && hasPlan {
		// A Slack step message names its step: its command and action are the stored plan's own, read after
		// the caller was checked. A plan changed since is refused below by its plan_version.
		action, err := planAction(stored.Plan, *request.Step)
		if err != nil {
			return refuse(400, "remediation: "+err.Error())
		}
		request.Command, request.Action = stepRef(action), action.Action
	}
	command := strings.TrimSpace(request.Command)
	// Only a command from this event's stored plan runs. Generation is grounded server-side; accepting
	// any command string here would let execution drift from what was generated and reviewed.
	reason := planCommandRejectionFor(request, func() (RemediationPlan, bool) { return stored.Plan, hasPlan })
	if reason != "" {
		ctx.GetLogger().Warn("remediation_execute: command is not in the event's plan", "command", command, "reason", reason)
		return refuse(400, "remediation: "+reason)
	}
	// A decision binds the plan the person saw: a regenerated plan, or one older than the analysis it
	// was built from, needs a fresh look. The claim checks the version again under FOR SHARE.
	// With the remediation loop on a newer investigation does not void the plan: the step the person approved is the
	// one they saw, and Nubi re-checks the plan once the approval is answered (remediation_planning.go).
	if (stored.Stale && !events.RemediationLoopOn(request.AccountId)) || stored.Version != strings.TrimSpace(request.PlanVersion) {
		return refuse(409, planChangedMessage)
	}
	switch slot {
	case RemediationSlotManual:
		return recordOperatorStep(ctx, request, userId, stored)
	case RemediationSlotDecline:
		return recordDecline(ctx, request, userId, stored)
	}
	if _, ok := automationRefID(command); ok {
		// A run of the account's automation (remediation_automation.go): no command to check, and it has no undo.
		if strings.EqualFold(slot, RemediationSlotRollback) {
			return refuse(400, "remediation: an automation step has no undo")
		}
		if reason := stepOrderRejection(ctx, request.EventId, stored, planStep(stored.Plan, slot, command, "")); reason != "" {
			return refuse(409, reason)
		}
		return runAutomationStep(ctx, request, stored, command, userId)
	}
	if reason := remediationCommandRejection(command); reason != "" {
		return refuse(400, reason)
	}
	if reason := commandRunRejection(ctx, command); reason != "" {
		return refuse(400, reason)
	}
	if !strings.EqualFold(slot, RemediationSlotRollback) {
		if reason := stepOrderRejection(ctx, request.EventId, stored, planStep(stored.Plan, slot, command, "")); reason != "" {
			return refuse(409, reason)
		}
	}
	return runRemediationStep(ctx, request, stored, command, userId)
}

// loopRunNeedsApproval is the refusal of a Run or undo pressed outside Nubi's approval with the remediation loop on.
const loopRunNeedsApproval = "remediation: with the remediation loop on, a step runs only after Nubi asks for approval and a person approves it. Pick the fix on the Remediation tab, or ask in the alert's chat."

// loopRunRefusal says why a decision that runs a command (Run, undo) is refused, or "". With the remediation loop on
// every write on the alert goes through Nubi's run_step approval (#40734): only run_step's own decision (source chat,
// set in code by runStepFromChat; a request body can only name "" or "slack") may run a step. The panel's Run and a
// Slack step message's Run are refused before anything is read or recorded. Decline, "I ran this", verify and the
// outcome check run nothing on the account and are unaffected. With the loop off nothing changes.
//
// The flag is read without falling back to off: if it cannot be read, a Run is refused (503) rather than let through
// without an approval.
func loopRunRefusal(request RemediationExecuteRequest) (int, string) {
	if !isRunSlot(request.Slot) || request.Source == chatSource {
		return 0, ""
	}
	on, err := events.RemediationLoopRead(request.AccountId)
	if err != nil {
		return 503, loopRunFlagUnread
	}
	if !on {
		return 0, ""
	}
	return 409, loopRunNeedsApproval
}

// loopRunFlagUnread is the refusal of a Run when the remediation loop flag could not be read.
const loopRunFlagUnread = "remediation: could not check this account's settings, so nothing ran; try again"

// commandRunRejection says why a command of the plan is not sent as written, or "".
func commandRunRejection(ctx *security.RequestContext, command string) string {
	// A placeholder must never reach the cluster: the panel runs commands verbatim, and the audit found
	// `kubectl set env deployment/app-prod GITHUB_WEBHOOK_SECRET=GITHUB_WEBHOOK_SECRET_PLACEHOLDER` offered
	// against production. normalizePlan only demotes such an action; this is the block.
	if commandHasPlaceholder(command) {
		ctx.GetLogger().Warn("remediation_execute: command carries a placeholder value", "command", command)
		return "remediation: this command contains a placeholder value and was not run. Substitute the real value and run it yourself — applying it as written would overwrite the setting with the placeholder text."
	}
	if err := tools.ValidateCommandSafety(command); err != nil {
		ctx.GetLogger().Warn("remediation_execute: unsafe command rejected", "error", err)
		return err.Error()
	}
	return ""
}

// remediationCommandRejection returns why the Run button refuses this command before it runs, or ""
// when it goes through. Plan generation checks every command with it as well (validatePlanCommands),
// so the panel is not handed a command its own executor rejects. Account-scoped checks — RBAC, the
// account's provider — need a request, so they stay in processRemediationExecute.
func remediationCommandRejection(command string) string {
	if len(command) > maxRemediationCommandLen {
		return "remediation: command is too long"
	}
	// Checked before the metacharacter guard: that guard is quote-aware for cloud CLI commands, and
	// stripping quoted content is only sound once the quotes are known to be balanced. An unbalanced
	// quote would otherwise let the stripper swallow the rest of the command, metacharacters included.
	if isStructurallyTruncated(command) {
		return "remediation: command has unbalanced quotes or braces and looks truncated; regenerate the plan"
	}
	// A remediation command is a single invocation. Reject shell metacharacters up front: they let a
	// single string smuggle a second command (e.g. "kubectl get pods; kubectl delete ns prod") past
	// the safety blocklist while the shell on the workspace pod still evaluates it. This also blunts
	// indirect prompt injection, since the plan is seeded from attacker-influencable investigation text.
	//
	// Cloud CLI commands are checked with quoted content removed. JMESPath carries ( ) | inside
	// --query arguments routinely ("Reservations[].Instances[?State.Name=='running']"), and rejecting
	// those leaves the cloud plan unable to express most of what it needs. Quoted text cannot start a
	// second command precisely because it stays quoted, so only unquoted metacharacters are a smuggling
	// risk — and those the stripped check still catches.
	metaCheckTarget := command
	if tools.CloudCliToolFor(command) != "" {
		metaCheckTarget = tools.StripQuotedContentForShellCheck(command)
	}
	if containsShellMetacharacters(metaCheckTarget) {
		return "remediation: command contains shell metacharacters (; & | < > ( ) ` $ or newlines) and was rejected; run a single command"
	}
	// The kubectl relay applies its own grammar (ExecuteContainerJob), stricter than the guard above:
	// it also rejects unquoted { }, so `-o jsonpath={.spec.replicas}` fails there as a transport
	// error. Run the same check here so the refusal says why, and so generation can drop the command.
	if fields := strings.Fields(command); len(fields) > 0 && fields[0] == "kubectl" {
		if err := tools.ValidateKubectlRelayCommand(command); err != nil {
			return "remediation: " + err.Error()
		}
	}
	return ""
}

// isVerifySlot reports whether this run is an action's verify command.
func isVerifySlot(slot string) bool {
	return strings.EqualFold(strings.TrimSpace(slot), RemediationSlotVerify)
}

// isOutcomeSlot reports whether this run is the plan's outcome check.
func isOutcomeSlot(slot string) bool {
	return strings.EqualFold(strings.TrimSpace(slot), RemediationSlotOutcome)
}

// verificationPassed decides what a verify run proved. Three-valued on purpose:
//
//	true  — the check ran and observed something consistent with the fix
//	false — the check ran and failed
//	nil   — the check ran and proved nothing
//
// nil is the case worth having. A verify asserts something about observed state, so a command that
// exits 0 having observed nothing has verified nothing: a selector matching no object, a query
// returning no rows and a log tail with no lines all exit 0. Reporting that as success tells the
// operator the fix held when nothing was actually checked.
func verificationPassed(response tools.RemediationExecutionResult, exitCodeReported bool) any {
	switch {
	case !exitCodeReported:
		// The executor merged output and discarded the exit code; there is nothing to judge by.
		return nil
	case !response.Success:
		return false
	case strings.TrimSpace(response.Stdout) == "":
		return nil
	default:
		return true
	}
}

// persistRemediationVerification records the outcome of a verify command onto the execute attempt
// it checked, under data.verify. Best-effort: the command has already run, and failing to annotate
// it must not fail the response.
//
// "passed" is deliberately three-valued. A verify asserts something about observed state, so a
// command that exits 0 having observed nothing has not verified anything — a selector matching no
// object and a query returning no rows both exit 0. That case records passed=null, which the UI
// reads as "needs checking" rather than as confirmation.
func persistRemediationVerification(ctx *security.RequestContext, eventId, executeCommand, verifyCommand string, response tools.RemediationExecutionResult, exitCodeReported bool) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Warn("remediation: database unavailable, verification not persisted", "error", err)
		return
	}
	passed := verificationPassed(response, exitCodeReported)
	verify := map[string]any{
		"ran":     true,
		"passed":  passed,
		"command": verifyCommand,
		"output":  truncateForRecord(response.Stdout),
		"at":      time.Now().UTC().Format(time.RFC3339),
	}
	repo := events.NewEventAnalysisRepository(dbManager)
	if err := repo.RecordRemediationVerification(ctx, eventId, executeCommand, verify); err != nil {
		ctx.GetLogger().Warn("remediation: failed to persist verification", "error", err)
	}
}

// truncateForRecord caps command output stored on a resolution. The row is read to render one line
// in the UI, not to archive logs.
func truncateForRecord(out string) string {
	const max = 2000
	out = strings.TrimSpace(out)
	if len(out) > max {
		return out[:max] + "…"
	}
	return out
}

// relayExecEnvelope is the JSON the relay executors return for a command run. Keys vary by executor,
// so every field is optional; presence of exit_code is what tells us this is a real envelope (rather
// than plain command output) and therefore that the exit code is trustworthy.
type relayExecEnvelope struct {
	Stdout   *string `json:"stdout"`
	Stderr   *string `json:"stderr"`
	Output   *string `json:"output"`
	ExitCode *int    `json:"exit_code"`
	// Error is how the agent refuses a command without running it, e.g. a write verb on an agent
	// whose kubectl is read-only: {"error":"kubectl: verb \"set\" not in read-only allowlist; ..."}.
	Error *string `json:"error"`
}

// parseRelayExecResult extracts stdout/stderr/exit_code from a relay result string. It returns
// parsed=true only when the string is a JSON envelope carrying an exit_code — in that case exitCode
// is authoritative. Otherwise the string is treated as plain output (best effort: success assumed,
// since ExecuteContainerJob has already discarded the exit code by the time it merges to text).
func parseRelayExecResult(raw string) (stdout, stderr string, exitCode int, parsed bool) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "{") {
		var env relayExecEnvelope
		err := json.Unmarshal([]byte(trimmed), &env)
		// A refusal carries no exit code; without this it read as plain output and was recorded as a
		// successful remediation although nothing ran.
		if err == nil && env.ExitCode == nil && env.Stdout == nil && env.Output == nil && env.Error != nil && strings.TrimSpace(*env.Error) != "" {
			return "", strings.TrimSpace(*env.Error), 1, true
		}
		if err == nil && env.ExitCode != nil {
			out := ""
			if env.Stdout != nil {
				out = *env.Stdout
			} else if env.Output != nil {
				out = *env.Output
			}
			errOut := ""
			if env.Stderr != nil {
				errOut = *env.Stderr
			}
			return out, errOut, *env.ExitCode, true
		}
	}
	return raw, "", 0, false
}

// writeRemediationAudit records who ran which command against the account's cluster. This RPC is
// browser-reachable and runs write commands, so — like handleWorkspaceExecute — every call is
// audited. Fired in the background so auditing never blocks the response.
func writeRemediationAudit(ctx *security.RequestContext, accountId, command, tool string, success bool, action audit.EventAction, source string) {
	auditReq := remediationAudit(ctx, accountId, command, tool, success, action, source)
	go func() { _ = audit.CreateAudit(ctx, auditReq) }()
}

func remediationAudit(ctx *security.RequestContext, accountId, command, tool string, success bool, action audit.EventAction, source string) *audit.AuditRequest {
	var tenantId, userId string
	if sc := ctx.GetSecurityContext(); sc != nil {
		tenantId = sc.GetTenantId()
		userId = sc.GetUserId()
	}
	status := audit.EventStatusSuccess
	if !success {
		status = audit.EventStatusFailure
	}
	// Credentials in the command are masked as in workspaceExecuteAudit.
	command = egressfilter.RedactSecrets(command)
	return &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				AccountId:     accountId,
				TenantId:      tenantId,
				UserId:        userId,
				EventTime:     time.Now(),
				EventCategory: audit.EventCategoryK8sRelay,
				EventType:     audit.EventTypeK8sRelayTask,
				EventActor:    audit.EventActorK8sAgent,
				EventAction:   action,
				EventTarget:   tool,
				EventStatus:   status,
				EventState:    map[string]any{"command": command},
				EventAttr: map[string]any{
					"command": command,
					"tool":    tool,
					"source":  source,
				},
			},
		},
	}
}

// shellMetacharacters are command-separator, redirection, and substitution characters. A remediation
// command is a single invocation, so any of these means the string is trying to chain or rewrite the
// command the shell on the workspace pod ultimately evaluates — we reject rather than try to parse it.
const shellMetacharacters = ";&|<>()`$\n\r"

// containsShellMetacharacters reports whether the command carries any shell metacharacter, i.e. it is
// not a single plain invocation. Used to reject chained/redirected/substituted commands before they
// reach the relay.
func containsShellMetacharacters(command string) bool {
	return strings.ContainsAny(command, shellMetacharacters)
}

// isStructurallyTruncated reports whether the command's quoting or bracing is unbalanced.
//
// The plan arrives as model-generated JSON, so a command carrying a JSON payload has to survive
// being escaped inside a JSON string — and in practice it does not: the payload's first inner quote
// terminates the string early and the command arrives cut off, e.g.
//
//	kubectl patch configmap flagd-config -n demo -p '{
//
// That has no metacharacters and matches no destructive pattern, so every other guard passes it and
// the operator gets an opaque failure from the relay instead of an explanation. Counting delimiters
// is enough to catch it: a truncated command is always left with an unclosed quote or brace.
func isStructurallyTruncated(command string) bool {
	return quoteLeftOpen(command) ||
		strings.Count(command, "{") != strings.Count(command, "}") ||
		strings.Count(command, "[") != strings.Count(command, "]")
}

// quoteLeftOpen reports a quote still open at the end, read as a shell reads it. Counting each quote
// char alone rejected "it's done" as truncated.
func quoteLeftOpen(command string) bool {
	var quote rune
	escaped := false
	for _, r := range command {
		switch {
		case escaped:
			escaped = false
		case quote != '\'' && r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		}
	}
	return quote != 0
}

// workspaceOutcome interprets a workspace-run command's result: stdout, stderr, exit code, whether
// it succeeded, and whether the executor actually stated how it exited.
//
// The workspace has no exit_code field, and ErrWorkspaceCommandFailed is NOT "ran and exited
// non-zero": classifyExecuteResponse raises it for any command_status:"failed", which the agent also
// uses for its own pre-execution rejections — an empty command, a bad workspace path, the security
// validator refusing an absolute path. Nothing ran in those cases, so their exit code is not ours to
// state; only "exit status N" proves the command reached cmd.Run(), and it carries the real code.
// Reporting a flat 1 for all of them replaced a vague caption with a specific wrong number.
func workspaceOutcome(execErr error, raw string) (stdout, stderr string, exitCode int, success, reported bool) {
	if execErr == nil {
		// command_status success, which is as definitive as an exit code. Returned unexamined: all
		// three cloud tools build the recovery envelope only alongside NBToolResponseStatusError, so
		// a success never carries one -- and this is the path where the payload is large (a full
		// describe-instances response), so it is also the one worth not parsing.
		return raw, "", 0, true, true
	}

	stdout = unwrapCliRecoveryEnvelope(raw)
	stderr = execErr.Error()
	exitCode = 1
	// Prefer the workspace's own message over Go's wrapping chain, but do not blank the pane if the
	// failure arrived without one.
	var failure *workspace.CommandFailure
	if errors.As(execErr, &failure) && strings.TrimSpace(failure.StdErr) != "" {
		stderr = failure.StdErr
	}
	// Only the workspace knows its agent's stderr format; asking it keeps this from becoming a second
	// parser that a format change would silently miss.
	if code, ok := workspace.ExitCodeFromFailure(execErr); ok {
		return stdout, stderr, code, false, true
	}
	// It failed, but nothing told us it ran — do not claim an exit code for it.
	return stdout, stderr, exitCode, false, false
}

// unwrapCliRecoveryEnvelope returns the original CLI output from the JSON the cloud tools wrap a
// failure in. That envelope's error_hint is written to steer the model ("read the error before
// switching commands"); showing it in an operator's Output pane is showing them someone else's
// instructions instead of what their command printed.
func unwrapCliRecoveryEnvelope(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") {
		return raw
	}
	var envelope struct {
		ErrorHint     string `json:"error_hint"`
		OriginalError string `json:"original_error"`
	}
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil || envelope.ErrorHint == "" {
		return raw
	}
	return envelope.OriginalError
}

// describeRemediationAccount states the facts a runnable command needs and the investigation prose
// does not reliably carry: which cloud the account is on, and which region the event happened in.
// Returns "" when neither is known, leaving the context exactly as it was.
//
// Written as labelled values rather than sentences: the values are substituted in, and any phrasing
// with an article reads as "a AWS account" for some of them.
func describeRemediationAccount(accountId, eventId string) string {
	lines := []string{}
	provider := strings.TrimSpace(tools.GetCloudProviderForAccount(accountId))
	if provider != "" {
		lines = append(lines, fmt.Sprintf("Account provider: %s. Act on this event using the CLI for this provider.", provider))
	}
	if region := eventRegion(provider, accountId, eventId); region != "" {
		lines = append(lines, fmt.Sprintf("Region: %s. Every command that acts on a regional resource must carry this region.", region))
	}
	if len(lines) == 0 {
		return ""
	}
	return "## Account\n" + strings.Join(lines, "\n")
}

// eventRegion returns the region a cloud event happened in. On a cloud account subject_node holds
// the region rather than a node -- it is what the event page shows as "Node", and agent_workflow_builder
// documents it in the same words -- so it is the region the remediation must target.
//
// Empty for a Kubernetes event, where subject_node really is a node name and offering it as a region
// would produce commands that fail in a new way, and empty on any lookup failure: a missing region
// degrades the prompt, it must not fail generation.
func eventRegion(provider, accountId, eventId string) string {
	if strings.TrimSpace(eventId) == "" || strings.TrimSpace(accountId) == "" {
		return ""
	}
	if provider == "" || strings.EqualFold(provider, "k8s") {
		return ""
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return ""
	}
	var node string
	if err := dbms.Db.Get(&node, "SELECT COALESCE(subject_node, '') FROM events WHERE id = $1 AND cloud_account_id = $2", eventId, accountId); err != nil {
		return ""
	}
	return strings.TrimSpace(node)
}

// planAction is step index (0-based) of plan.
func planAction(plan RemediationPlan, index int) (RemediationAction, error) {
	if index < 0 || index >= len(plan.Actions) {
		return RemediationAction{}, fmt.Errorf("the plan has steps 1 to %d", len(plan.Actions))
	}
	return plan.Actions[index], nil
}
