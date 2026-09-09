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
	"nudgebee/llm/workspace"

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
	// ExecuteCommand is the execute slot's command, sent when Slot is "verify". It is the
	// correlation key back to the attempt being verified: the execute run stored it as the
	// resolution's type_reference_id. Without it a verify result has no attempt to attach to.
	ExecuteCommand string `json:"execute_command"`
}

// Command slots within one action.
const (
	RemediationSlotExecute = "execute"
	RemediationSlotVerify  = "verify"
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
	// The prompt asks the model to match the CLI to what the action targets and to put the event's
	// region on every cloud command, but the only thing it can infer either from is the investigation
	// prose — which names the provider rarely and the region inconsistently. Stating both outright
	// makes them facts rather than guesses: it is what stops a kubectl command being proposed against
	// an EC2 instance, and what stops an `aws` command going out without --region and failing with
	// "You must specify a region" having done nothing.
	humanContent := investigationContext
	if account := describeRemediationAccount(request.AccountId, request.EventId); account != "" {
		humanContent = account + "\n\n" + investigationContext
	}

	resp, err := agentcore.GenerateAndTrackLLMContent(ctx, sc.GetUserId(), request.AccountId, "", "", "", false, []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: systemPrompt}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: humanContent}}},
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
	// Checked before the metacharacter guard: that guard is quote-aware for cloud CLI commands, and
	// stripping quoted content is only sound once the quotes are known to be balanced. An unbalanced
	// quote would otherwise let the stripper swallow the rest of the command, metacharacters included.
	if isStructurallyTruncated(command) {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: "remediation: command has unbalanced quotes or braces and looks truncated; regenerate the plan"}}))
		return
	}
	cloudCliTool := tools.CloudCliToolFor(command)
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
	if cloudCliTool != "" {
		metaCheckTarget = tools.StripQuotedContentForShellCheck(command)
	}
	if containsShellMetacharacters(metaCheckTarget) {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: "remediation: command contains shell metacharacters (; & | < > ( ) ` $ or newlines) and was rejected; run a single command"}}))
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
	if ranOnWorkspace {
		response.Stdout, response.Stderr, response.ExitCode, response.Success, exitCodeReported = workspaceOutcome(execErr, raw)
		if execErr != nil {
			ctx.GetLogger().Error("remediation_execute: command failed")
			response.Error = execErr.Error()
		}
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
