package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
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

	"github.com/gin-gonic/gin"
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

// RemediationAction is one candidate recovery strategy — a self-contained execution plan, not a
// sequential step. The name and confidence are model-generated (never a fixed vocabulary): action
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
func normalizePlan(plan *RemediationPlan, availableArtifacts []string) {
	// An action inherits the uncertainty of the hypothesis it addresses: if the cause is only 95%
	// likely, no action against it can be more than 95% likely to resolve the incident. The model
	// scores the two independently and routinely emits a 100% action under a 95% hypothesis, which
	// reads as a contradiction on the card. Clamp so the action never outruns its own premise.
	hypConfidence := make(map[string]Confidence, len(plan.Hypotheses))
	for _, h := range plan.Hypotheses {
		hypConfidence[strings.ToLower(strings.TrimSpace(h.Hypothesis))] = h.Confidence
	}

	hasArtifact := false
	for _, a := range availableArtifacts {
		if strings.TrimSpace(a) != "" {
			hasArtifact = true
			break
		}
	}

	kept := plan.Actions[:0]
	for i := range plan.Actions {
		a := &plan.Actions[i]
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

		if strings.TrimSpace(a.ExecuteCommand) == "" {
			// An action with no command is carried out on another surface, so it is only real if that
			// surface actually holds something to apply. With no artifact the card becomes a dead end:
			// it tells the operator a fix exists and points at a panel with nothing in it. Drop it —
			// the summary still describes the durable fix, which is where an unactionable one belongs.
			if !hasArtifact {
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
}

// RemediationPlan is the structured plan returned to the UI: the root cause, the candidate hypotheses
// considered, and a ranked set of recovery actions. When the investigation does not support a
// confident root cause, Actions is empty and Summary explains why no remediation is recommended.
type RemediationPlan struct {
	RootCause  string                  `json:"root_cause"`
	Summary    string                  `json:"summary"`
	Hypotheses []RemediationHypothesis `json:"hypotheses"`
	Actions    []RemediationAction     `json:"actions"`
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
}

// Command slots within one action.
const (
	RemediationSlotExecute = "execute"
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

	investigationContext := strings.TrimSpace(request.Context)
	if investigationContext == "" {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: "investigation context is required"}}))
		return
	}

	systemPrompt, promptErr := prompts.GetPromptStrict(c.Request.Context(), prompts.PromptRemediationGenerateJson, request.AccountId)
	if promptErr != nil {
		ctx.GetLogger().Error("processRemediationGenerate: loading prompt failed", "error", promptErr)
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: "failed to load remediation prompt"}}))
		return
	}
	resp, err := agentcore.GenerateAndTrackLLMContent(ctx, sc.GetUserId(), request.AccountId, "", "", "", false, []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: systemPrompt}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: investigationContext}}},
	}, true)
	if err != nil {
		ctx.GetLogger().Error("remediation_generate: llm generation failed", "error", err)
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: "failed to generate remediation plan"}}))
		return
	}
	if resp == nil || len(resp.Choices) == 0 {
		ctx.GetLogger().Error("remediation_generate: llm returned no choices")
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: "failed to generate remediation plan"}}))
		return
	}

	var plan RemediationPlan
	if err := common.ExtractAndUnmarshalJSON([]byte(resp.Choices[0].Content), &plan); err != nil {
		ctx.GetLogger().Error("remediation_generate: failed to parse plan json", "error", err)
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: "failed to parse remediation plan"}}))
		return
	}

	normalizePlan(&plan, request.AvailableArtifacts)

	// Persist the plan so it survives reload (best-effort — a DB hiccup must not fail generation).
	persistRemediationPlan(ctx, request.AccountId, request.EventId, plan)

	c.JSON(200, buildApiResponse(plan, nil))
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
	if err := json.Unmarshal([]byte(saved.Analysis), &plan); err != nil {
		ctx.GetLogger().Warn("remediation_get: failed to parse saved plan", "error", err)
		plan = RemediationPlan{}
	}
	c.JSON(200, buildApiResponse(plan, nil))
}

// persistRemediationPlan upserts the plan into event_log_analysis under the `remediation` analysis
// type (a sibling of rca_analysis), keyed by the event's fingerprint + aggregation key.
func persistRemediationPlan(ctx *security.RequestContext, accountId, eventId string, plan RemediationPlan) {
	if eventId == "" {
		return
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Warn("remediation: database unavailable, plan not persisted", "error", err)
		return
	}
	repo := events.NewEventAnalysisRepository(dbManager)
	info, err := repo.GetEventInfo(ctx, eventId, accountId)
	if err != nil || info == nil {
		ctx.GetLogger().Warn("remediation: event info unavailable, plan not persisted", "error", err)
		return
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return
	}
	if err := repo.UpsertEventAnalysis(ctx, eventId, string(data), plan.Summary, string(events.AnalysisStatusCompleted), info.Fingerprint, accountId, info.AggregationKey, events.AnalysisTypeRemediation); err != nil {
		ctx.GetLogger().Warn("remediation: failed to persist plan", "error", err)
	}
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
	if len(command) > maxRemediationCommandLen {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: "remediation: command is too long"}}))
		return
	}
	// A remediation command is a single invocation. Reject shell metacharacters up front: they let a
	// single string smuggle a second command (e.g. "kubectl get pods; kubectl delete ns prod") past
	// the safety blocklist while the shell on the workspace pod still evaluates it. This also blunts
	// indirect prompt injection, since the plan is seeded from attacker-influencable investigation text.
	if containsShellMetacharacters(command) {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: "remediation: command contains shell metacharacters (; & | < > ( ) ` $ or newlines) and was rejected; run a single command"}}))
		return
	}
	if isStructurallyTruncated(command) {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: "remediation: command has unbalanced quotes or braces and looks truncated; regenerate the plan"}}))
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

	if err := tools.ValidateCommandSafety(command); err != nil {
		ctx.GetLogger().Warn("remediation_execute: unsafe command rejected", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	relayJob, registeredToolName := remediationRelayModule(command)
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

	start := time.Now()
	result, execErr := tools.ExecuteContainerJob(toolCtx, relayJob, command, request.AccountId, map[string]any{}, true)
	response := tools.RemediationExecutionResult{
		Command:    command,
		ExecutedAt: start.Format(time.RFC3339),
		Duration:   time.Since(start).String(),
	}

	raw := ""
	if s, ok := result.(string); ok {
		raw = s
	}
	if execErr != nil {
		// Transport failure — the command may not have run at all.
		ctx.GetLogger().Error("remediation_execute: command failed", "error", execErr)
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
	slot := strings.ToLower(strings.TrimSpace(request.Slot))
	isExecuteSlot := slot == "" || slot == RemediationSlotExecute
	if response.Success && request.EventId != "" && isExecuteSlot {
		persistRemediationExecution(ctx, request.EventId, sc.GetUserId(), command, response.ExitCode)
	}
	c.JSON(200, buildApiResponse(response, nil))
}

// persistRemediationExecution records a successful command run as an event_resolution row so the UI
// can mark the action already-applied. Best-effort: a DB failure must not fail the (already-run) command.
func persistRemediationExecution(ctx *security.RequestContext, eventId, userId, command string, exitCode int) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Warn("remediation: database unavailable, execution not persisted", "error", err)
		return
	}
	repo := events.NewEventAnalysisRepository(dbManager)
	data, err := json.Marshal(map[string]any{"command": command, "exit_code": exitCode})
	if err != nil {
		return
	}
	// The resolutions list shows status_message next to the row. A fixed string there told the reader
	// nothing they could not already see from the row's type, so record the outcome instead.
	statusMessage := fmt.Sprintf("Ran from the remediation panel, exit code %d", exitCode)
	if err := repo.InsertRemediationExecution(ctx, eventId, userId, command, string(data), statusMessage, true); err != nil {
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
}

// parseRelayExecResult extracts stdout/stderr/exit_code from a relay result string. It returns
// parsed=true only when the string is a JSON envelope carrying an exit_code — in that case exitCode
// is authoritative. Otherwise the string is treated as plain output (best effort: success assumed,
// since ExecuteContainerJob has already discarded the exit code by the time it merges to text).
func parseRelayExecResult(raw string) (stdout, stderr string, exitCode int, parsed bool) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "{") {
		var env relayExecEnvelope
		if err := json.Unmarshal([]byte(trimmed), &env); err == nil && env.ExitCode != nil {
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
	return strings.Count(command, "'")%2 != 0 ||
		strings.Count(command, `"`)%2 != 0 ||
		strings.Count(command, "{") != strings.Count(command, "}") ||
		strings.Count(command, "[") != strings.Count(command, "]")
}

// remediationRelayModule maps a command to its relay job type and the registered tool that carries
// the account's cluster credentials. Mirrors the prefix dispatch in tools/tool_remediation.go. The
// match is case-insensitive so routing agrees with the rest of the command handling.
func remediationRelayModule(command string) (tools.RelayJob, string) {
	lower := strings.ToLower(strings.TrimSpace(command))
	switch {
	case strings.HasPrefix(lower, "kubectl"):
		return tools.RelayJobKubectl, tools.ToolExecuteKubectlCommand
	case strings.HasPrefix(lower, "helm"):
		return tools.RelayJobHelm, tools.ToolExecuteHelmCommand
	case strings.HasPrefix(lower, "argocd"):
		return tools.RelayJobArgoCD, tools.ToolExecuteArgoCDCommand
	default:
		return tools.RelayJobShell, tools.ToolExecuteServerCommand
	}
}
