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
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
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
	// After names the action that must be applied before this one, when the change only works in that
	// order (a restart after the config it reloads, a redrive after the permission it needs). Actions
	// without it are alternative strategies.
	After string `json:"after,omitempty"`
}

// HardeningItem is an improvement the incident does not require; kept apart from actions so it never reads as the fix.
type HardeningItem struct {
	Target string `json:"target"`
	Change string `json:"change"`
	Reason string `json:"reason"`
}

// RequiredChange says where a durable fix is made and what it is.
type RequiredChange struct {
	Where  string `json:"where"`
	Target string `json:"target"`
	Change string `json:"change"`
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
	kept := plan.Actions[:0]
	for i := range plan.Actions {
		a := &plan.Actions[i]
		// A rule change's kind follows from what it does, not from the model's label, and
		// must be set before the mitigation cap below reads it.
		if a.AlertRuleChange != nil {
			a.Kind = alertRuleActionKind(strings.ToLower(strings.TrimSpace(a.AlertRuleChange.Operation)))
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
		return plan.Actions[i].Confidence > plan.Actions[j].Confidence
	})
	return append(notes, keepDependentsAfter(plan)...)
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
	// Dropped lists alert-rule actions the server removed and why. Not rendered.
	Dropped []DroppedAction `json:"dropped,omitempty"`
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
}

// Command slots within one action.
const (
	RemediationSlotExecute  = "execute"
	RemediationSlotVerify   = "verify"
	RemediationSlotRollback = "rollback"
)

// processRemediationGenerate turns the completed investigation into a structured remediation plan
// (root cause + execute/verify/rollback commands). It reuses the LLM generation + JSON-extraction
// infra; only the prompt (PromptToolRemediationGenerateJson) is remediation-specific. RBAC: read.
func processRemediationGenerate(c *gin.Context, tracer trace.Tracer, meter metric.Meter) {
	var request RemediationGenerateRequest
	var actionRequest ActionRequest
	if err := c.ShouldBindJSON(&actionRequest); err != nil {
		slog.Error(errorBindingMessage, "error", err)
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	payload := actionRequest.Input
	if v, ok := payload["request"].(map[string]any); ok {
		payload = v
	}
	if err := common.DecodeMapToStruct(payload, &request); err != nil {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	logger := slog.With("account_id", request.AccountId, "event_id", request.EventId)
	ctx, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	sc := ctx.GetSecurityContext()
	if sc == nil || (!sc.HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) &&
		!grantedRun(sc, request.AccountId, moduleAiMisc)) {
		c.JSON(403, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
		return
	}

	conversationId := remediationConversationID(ctx, request.AccountId, request.EventId)
	plan, trace, err := generateRemediationPlan(ctx, request, remediationLLM(ctx, sc.GetUserId(), request.AccountId, conversationId))
	if err != nil {
		var input errRemediationInput
		if errors.As(err, &input) {
			c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}
		ctx.GetLogger().Error("remediation_generate: failed", "error", err)
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: "failed to generate remediation plan"}}))
		return
	}

	respondWithStoredPlan(c, ctx.GetLogger(), plan, func() error {
		return persistRemediationPlan(ctx, request.AccountId, request.EventId, plan, trace)
	})
}

// respondWithStoredPlan returns the plan only once it is stored. The stored plan is what Run is checked
// against (planCommandRejection), so a plan that could not be saved has commands that cannot run.
func respondWithStoredPlan(c *gin.Context, logger *slog.Logger, plan RemediationPlan, save func() error) {
	if err := save(); err != nil {
		logger.Error("remediation_generate: failed to persist plan", "error", err)
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: "failed to save remediation plan"}}))
		return
	}
	c.JSON(200, buildApiResponse(plan, nil))
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
		resp, err := agentcore.GenerateAndTrackLLMContent(toolcore.WithPromptCachingDisabled(ctx), userId, accountId, conversationId, "", remediationPlannerAgent, false, []llms.MessageContent{
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
	if facts := buildRemediationFacts(ctx, request.AccountId, request.EventId); facts != "" {
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

	trace.Validation = normalizePlan(&plan, artifacts, request.AlertRule)
	for _, d := range plan.Dropped {
		ctx.GetLogger().Info("remediation_generate: dropped alert-rule action", "action", d.Action, "reason", d.Reason)
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
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	payload := actionRequest.Input
	if v, ok := payload["request"].(map[string]any); ok {
		payload = v
	}
	if err := common.DecodeMapToStruct(payload, &request); err != nil {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	logger := slog.With("account_id", request.AccountId, "event_id", request.EventId)
	ctx, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	sc := ctx.GetSecurityContext()
	if sc == nil || (!sc.HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) &&
		!granted(sc, request.AccountId, moduleAiMisc, "Read", "Write")) {
		c.JSON(403, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
		return
	}

	var plan RemediationPlan
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Warn("remediation_get: database unavailable", "error", err)
		c.JSON(200, buildApiResponse(plan, nil))
		return
	}
	repo := events.NewEventAnalysisRepository(dbManager)
	info, err := repo.GetEventInfo(ctx, request.EventId, request.AccountId)
	if err != nil || info == nil {
		c.JSON(200, buildApiResponse(plan, nil))
		return
	}
	saved, err := repo.GetEventAnalysis(ctx, request.EventId, info.Fingerprint, info.AggregationKey, request.AccountId, events.AnalysisTypeRemediation)
	if err != nil || saved == nil || strings.TrimSpace(saved.Analysis) == "" {
		c.JSON(200, buildApiResponse(plan, nil))
		return
	}
	// When this event has no plan of its own, the lookup falls back to the newest plan saved for the
	// same alert - one generated from an earlier firing's investigation. The panel treats any plan it
	// gets back as already generated, so restoring that one shows the operator another incident's root
	// cause, confidence and commands, and never builds a plan for this incident. Returning no plan here
	// makes the panel generate one from this event's own investigation.
	if !planSavedForEvent(saved, request.EventId) {
		c.JSON(200, buildApiResponse(plan, nil))
		return
	}
	// A plan is built from the investigation and its report. When either was re-run after the
	// plan was saved, the plan describes the previous analysis (dev event db61122a: the report
	// said "configuration error", the restored plan still said "undetermined"). Returning no plan
	// makes the panel build one from the current analysis, as above.
	var inputs []*events.EventAnalysis
	for _, t := range []events.EventAnalysisType{events.AnalysisTypeInvestigation, events.AnalysisTypeDetailedResponse} {
		a, aErr := repo.GetEventAnalysis(ctx, request.EventId, info.Fingerprint, info.AggregationKey, request.AccountId, t)
		if aErr != nil {
			ctx.GetLogger().Warn("remediation_get: unable to read analysis to check the plan's age", "error", aErr, "analysis_type", t)
			continue
		}
		if a != nil {
			inputs = append(inputs, a)
		}
	}
	if planOlderThanAnalysis(saved, inputs...) {
		ctx.GetLogger().Info("remediation_get: saved plan predates the current analysis, regenerating")
		c.JSON(200, buildApiResponse(plan, nil))
		return
	}
	if err := json.Unmarshal([]byte(saved.Analysis), &plan); err != nil {
		ctx.GetLogger().Warn("remediation_get: failed to parse saved plan", "error", err)
		plan = RemediationPlan{}
	}
	c.JSON(200, buildApiResponse(plan, nil))
}

// planCommandRejection returns why a command may not run, or "" when it is the stored plan's command for
// its slot. A verify must name its own action's execute, so its result lands on the right attempt.
func planCommandRejection(ctx *security.RequestContext, request RemediationExecuteRequest) string {
	return planCommandRejectionFor(request, func() (RemediationPlan, bool) {
		return loadSavedPlan(ctx, request.AccountId, request.EventId)
	})
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
			if strings.TrimSpace(a.ExecuteCommand) == command {
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
// firing of the same alert).
func loadSavedPlan(ctx *security.RequestContext, accountId, eventId string) (RemediationPlan, bool) {
	var plan RemediationPlan
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return plan, false
	}
	repo := events.NewEventAnalysisRepository(dbManager)
	info, err := repo.GetEventInfo(ctx, eventId, accountId)
	if err != nil || info == nil {
		return plan, false
	}
	saved, err := repo.GetEventAnalysis(ctx, eventId, info.Fingerprint, info.AggregationKey, accountId, events.AnalysisTypeRemediation)
	if err != nil || saved == nil || !planSavedForEvent(saved, eventId) {
		return plan, false
	}
	if err := json.Unmarshal([]byte(saved.Analysis), &plan); err != nil {
		return plan, false
	}
	return plan, true
}

// planSavedForEvent reports whether a stored plan was generated for eventId. A row with no event id
// cannot be attributed to any incident, so it is not restored either.
func planSavedForEvent(saved *events.EventAnalysis, eventId string) bool {
	owner := strings.TrimSpace(saved.RelatedEventId)
	return owner != "" && strings.EqualFold(owner, strings.TrimSpace(eventId))
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
// type (a sibling of rca_analysis), keyed by the event's fingerprint + aggregation key.
func persistRemediationPlan(ctx *security.RequestContext, accountId, eventId string, plan RemediationPlan, trace RemediationTrace) error {
	if eventId == "" {
		return errors.New("event id is required")
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("database unavailable: %w", err)
	}
	repo := events.NewEventAnalysisRepository(dbManager)
	info, err := repo.GetEventInfo(ctx, eventId, accountId)
	if err != nil {
		return fmt.Errorf("event info unavailable: %w", err)
	}
	if info == nil {
		return errors.New("event info unavailable: event not found")
	}
	// The trace rides in the same row; processRemediationGet unmarshals into RemediationPlan and ignores it.
	data, err := json.Marshal(struct {
		RemediationPlan
		Generation RemediationTrace `json:"generation"`
	}{plan, trace})
	if err != nil {
		return err
	}
	return repo.UpsertEventAnalysis(ctx, eventId, string(data), plan.Summary, string(events.AnalysisStatusCompleted), info.Fingerprint, accountId, info.AggregationKey, events.AnalysisTypeRemediation)
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
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	payload := actionRequest.Input
	if v, ok := payload["request"].(map[string]any); ok {
		payload = v
	}
	if err := common.DecodeMapToStruct(payload, &request); err != nil {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	command := strings.TrimSpace(request.Command)
	if command == "" {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: "command is required"}}))
		return
	}
	if reason := remediationCommandRejection(command); reason != "" {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: reason}}))
		return
	}

	logger := slog.With("account_id", request.AccountId)
	ctx, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	// Every remediation command runs against the target account's cluster, so every command requires
	// write (create) access on that account. HasAccountAccess is per-account, so this blocks the
	// cross-account case where a user holds write on one account but only read on the target.
	sc := ctx.GetSecurityContext()
	if sc == nil || (!sc.HasAccountAccess(request.AccountId, security.SecurityAccessTypeCreate) &&
		!grantedRun(sc, request.AccountId, moduleAiMisc)) {
		c.JSON(403, buildApiResponse(nil, []error{errors.New("remediation: write access is required to run remediation commands")}))
		return
	}

	// Only a command from this event's stored plan runs. Generation is grounded server-side; accepting
	// any command string here would let execution drift from what was generated and reviewed.
	if reason := planCommandRejection(ctx, request); reason != "" {
		ctx.GetLogger().Warn("remediation_execute: command is not in the event's plan", "command", command, "reason", reason)
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: "remediation: " + reason}}))
		return
	}

	// A stand-in value must never reach the cluster. The generation prompt asks the model to emit a
	// clearly-named placeholder rather than a secret, and the panel runs what it is given verbatim:
	// the audit found `kubectl set env deployment/app-prod GITHUB_WEBHOOK_SECRET=
	// GITHUB_WEBHOOK_SECRET_PLACEHOLDER` offered as a fix against production, which would have
	// replaced a live secret with that literal string and rolled the deployment. normalizePlan
	// already demotes such an action off the fix ranking; this is the block that makes it safe.
	if commandHasPlaceholder(command) {
		ctx.GetLogger().Warn("remediation_execute: command carries a placeholder value", "command", command)
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: "remediation: this command contains a placeholder value and was not run. Substitute the real value and run it yourself — applying it as written would overwrite the setting with the placeholder text."}}))
		return
	}

	if err := tools.ValidateCommandSafety(command); err != nil {
		ctx.GetLogger().Warn("remediation_execute: unsafe command rejected", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	// The executor is chosen from the ACCOUNT'S PROVIDER, then narrowed by the command. A cloud CLI
	// talks to a public API endpoint and only needs credentials, so it runs in a workspace pod here;
	// kubectl/helm/argocd/shell target hosts inside the customer network and go down the relay.
	//
	// Reading the command's first word alone used to decide this, which sent kubectl from an AWS
	// account to a relay agent that cannot exist there ("agent not connected", a connectivity error
	// for what is really a category error) and dropped GCP's `bq` through to an uncredentialed shell.
	substrate := tools.RemediationSubstrateFor(tools.GetCloudProviderForAccount(request.AccountId), command)
	if substrate.Reject != "" {
		ctx.GetLogger().Warn("remediation_execute: command does not match the account's provider",
			"command", command, "reason", substrate.Reject)
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: "remediation: " + substrate.Reject}}))
		return
	}

	registeredToolName := substrate.CloudCliTool
	ranOnWorkspace := substrate.CloudCliTool != ""
	start := time.Now()
	var raw string
	var execErr error

	if substrate.CloudCliTool != "" {
		// A Run click has no conversation behind it, so the workspace gets the per-account
		// directory rather than "" -- which it rejects with "Conversation ID is empty".
		raw, execErr = tools.ExecuteCloudCli(ctx, substrate.CloudCliTool, request.AccountId, tools.DefaultCloudCliConversationId(request.AccountId), command)
	} else {
		registeredToolName = substrate.RelayTool
		relayJob := substrate.RelayJob
		nbTool, found := toolcore.GetNBTool(request.AccountId, registeredToolName)
		if !found {
			c.JSON(404, buildApiResponse(nil, []error{common.Error{Message: "execution tool is not configured for this account"}}))
			return
		}

		var queryConfig toolcore.NBQueryConfig
		if request.ConfigName != "" {
			queryConfig = toolcore.NBQueryConfig{ToolConfigs: map[string]string{nbTool.Name(): request.ConfigName}}
		}

		toolCtx := toolcore.NewNbToolContext(ctx, nbTool, request.AccountId, sc.GetUserId(), "", "", "", command, nil, "", queryConfig, "")

		var result any
		result, execErr = tools.ExecuteContainerJob(toolCtx, relayJob, command, request.AccountId, map[string]any{}, true)
		if s, ok := result.(string); ok {
			raw = s
		}
	}

	response := tools.RemediationExecutionResult{
		Command:    command,
		ExecutedAt: start.Format(time.RFC3339),
		Duration:   time.Since(start).String(),
	}

	// Whether the executor actually told us how the command exited. False means "ran, outcome
	// unknown" rather than "ran and succeeded".
	//
	// The two substrates report that in different shapes, so reportedness is decided per substrate.
	// The relay returns a JSON envelope carrying exit_code; the workspace has no exit_code field at
	// all, so running its output through parseRelayExecResult never parsed and every cloud command
	// was recorded "outcome unverified" even when the outcome was stated plainly.
	exitCodeReported := false
	var exitErr *tools.RelayCommandExitError
	if ranOnWorkspace {
		response.Stdout, response.Stderr, response.ExitCode, response.Success, exitCodeReported = workspaceOutcome(execErr, raw)
		if execErr != nil {
			ctx.GetLogger().Error("remediation_execute: command failed")
			response.Error = execErr.Error()
		}
	} else if errors.As(execErr, &exitErr) {
		// The command ran and the relay reported a non-zero exit: a definitive failure
		// with a real exit code, not a transport error.
		stdout, stderr, _, parsed := parseRelayExecResult(raw)
		if !parsed {
			stdout = raw
		}
		response.Stdout = stdout
		response.Stderr = stderr
		response.ExitCode = exitErr.ExitCode
		response.Success = false
		response.Error = stderr
		if response.Error == "" {
			response.Error = exitErr.Error()
		}
		exitCodeReported = true
	} else if execErr != nil {
		// Transport failure — the command may not have run at all.
		ctx.GetLogger().Error("remediation_execute: command failed")
		response.Success = false
		response.ExitCode = 1
		response.Error = execErr.Error()
		response.Stderr = execErr.Error()
		response.Stdout = raw
	} else {
		// The relay reports its real exit code in a JSON envelope. ExecuteContainerJob passes it
		// through when it can't merge stdout/stderr, so a non-zero exit otherwise surfaces as a
		// green "success" with the raw envelope in stdout. Parse it and map exit_code != 0 to failure.
		stdout, stderr, exitCode, parsed := parseRelayExecResult(raw)
		response.Stdout = stdout
		response.Stderr = stderr
		response.ExitCode = exitCode
		// An unparsed result means the executor merged stdout/stderr to text and discarded the exit
		// code, so there is nothing to judge by. Treating that as success is the only workable
		// default — flipping it to failure would mark every merged-output command failed — but the
		// resolution must not then claim the command was verified. exitCodeReported carries that
		// distinction to the record; see persistRemediationExecution.
		exitCodeReported = parsed
		response.Success = !parsed || exitCode == 0
		if !response.Success && response.Error == "" {
			response.Error = stderr
		}
	}

	// Audit covers every command regardless of slot — that is the record of what was run.
	writeRemediationAudit(ctx, request.AccountId, command, registeredToolName, response.Success)
	// A resolution says how the event was acted on, so only the state-changing execute command earns
	// one. A verify observes and a rollback reverses; filing those as resolutions counted one
	// remediation attempt three times and listed an undo as though it resolved the event.
	//
	// Failures DO earn one. Gating the insert on response.Success meant a command that ran and
	// failed left no product-visible trace at all: the resolutions list showed only the attempts
	// that worked, so "nothing was tried here" and "three things were tried and all failed" looked
	// identical. The operator needs the second one most.
	if shouldPersistRemediationResolution(request.EventId, request.Slot) {
		persistRemediationExecution(ctx, request.EventId, sc.GetUserId(), command, response.ExitCode, response.Success, exitCodeReported)
	} else if isVerifySlot(request.Slot) && request.EventId != "" && request.ExecuteCommand != "" {
		// A verify does not earn its own resolution — that was the triple-counting bug. It annotates
		// the attempt it checked, so "it ran" and "the check confirmed it" are one record.
		persistRemediationVerification(ctx, request.EventId, request.ExecuteCommand, command, response, exitCodeReported)
	}
	c.JSON(200, buildApiResponse(response, nil))
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

// shouldPersistRemediationResolution decides whether a command run earns an event_resolution row.
// Deliberately independent of whether the command succeeded: a failed execute is exactly the run an
// operator most needs to see recorded. Slot still gates it, because a verify observes and a rollback
// reverses — filing those as resolutions counted one attempt three times and listed an undo as
// though it resolved the event.
func shouldPersistRemediationResolution(eventId, slot string) bool {
	if eventId == "" {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(slot))
	return normalized == "" || normalized == RemediationSlotExecute
}

// persistRemediationExecution records a command run as an event_resolution row so the UI can mark the
// action already-applied, or show that it was tried and failed. Best-effort: a DB failure must not
// fail the (already-run) command.
func persistRemediationExecution(ctx *security.RequestContext, eventId, userId, command string, exitCode int, success, exitCodeReported bool) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Warn("remediation: database unavailable, execution not persisted", "error", err)
		return
	}
	repo := events.NewEventAnalysisRepository(dbManager)
	data, err := json.Marshal(map[string]any{
		"command":            command,
		"exit_code":          exitCode,
		"success":            success,
		"exit_code_reported": exitCodeReported,
	})
	if err != nil {
		return
	}
	// The resolutions list shows status_message next to the row. A fixed string there told the reader
	// nothing they could not already see from the row's type, so record the outcome instead.
	// Say which of the two happened. "exit code 0" and "we never learned the exit code" are very
	// different facts, and recording the second as the first is how a command that quietly failed
	// ends up on the page as a success.
	statusMessage := fmt.Sprintf("Ran from the remediation panel, exit code %d", exitCode)
	if !exitCodeReported {
		statusMessage = "Ran from the remediation panel — the executor reported no exit code, so the outcome is unverified"
	}
	if err := repo.InsertRemediationExecution(ctx, eventId, userId, command, string(data), statusMessage, success); err != nil {
		ctx.GetLogger().Warn("remediation: failed to persist execution", "error", err)
	}
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
func writeRemediationAudit(ctx *security.RequestContext, accountId, command, tool string, success bool) {
	var tenantId, userId string
	if sc := ctx.GetSecurityContext(); sc != nil {
		tenantId = sc.GetTenantId()
		userId = sc.GetUserId()
	}
	status := audit.EventStatusSuccess
	if !success {
		status = audit.EventStatusFailure
	}
	auditReq := &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				AccountId:     accountId,
				TenantId:      tenantId,
				UserId:        userId,
				EventTime:     time.Now(),
				EventCategory: audit.EventCategoryK8sRelay,
				EventType:     audit.EventTypeK8sRelayTask,
				EventActor:    audit.EventActorK8sAgent,
				EventAction:   audit.EventActionCreate,
				EventTarget:   tool,
				EventStatus:   status,
				EventState:    map[string]any{"command": command},
				EventAttr: map[string]any{
					"command": command,
					"tool":    tool,
					"source":  "remediation-panel",
				},
			},
		},
	}
	go func() { _ = audit.CreateAudit(ctx, auditReq) }()
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
