package agents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/utils"
	"nudgebee/llm/workspace"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/tmc/langchaingo/llms"
)

// AgentCodeAnalyzer is the canonical registered name for the code-analysis
// agent (deep code analysis, debugging, RCA, and optional PR creation). It
// follows the repo's bare-noun agent-naming convention.
const AgentCodeAnalyzer = "code_analyzer"

// agentCodeAnalyzerLegacyName is the pre-rename registered name. It is kept as
// a back-compat agent alias so stored conversation history, explicit
// @agent_code_2 invocations, and api-server's hardcoded prompt prefix keep
// resolving across deploys. Do not use in new code.
const agentCodeAnalyzerLegacyName = "agent_code_2"

// sanitizeWorkspacePathID maps an ID to the workspace's safe path charset
// ([A-Za-z0-9_-]; everything else becomes '_') — the SAME mapping the workspace
// analyze handler applies when naming its temp directories. Slack session IDs
// contain a '.', so passing them raw both fails the workspace's conversation-id
// validation and misses the sanitized directory names.
func sanitizeWorkspacePathID(id string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, id)
}

// Mode constants mirror llm/code-analysis. Kept inline (not imported) because
// llm-server doesn't take a Go-module dependency on llm/code-analysis.
// code_analyzer only translates the upstream RaisePr flag into the mode field
// it sends to /analyze — it does NOT classify or override the user's intent.
const (
	codeAgentModeExplore = "explore"
	codeAgentModeFix     = "fix"
)

// defaultForwardedSkillTopK is the number of query-relevant skills forwarded to
// the code-analysis service when an operator has not set LlmServerSkillSelectionTopK.
// The code-analysis path cannot lazily load_skills, so it always narrows to the
// top-K most relevant skills rather than forwarding every mapped skill.
const defaultForwardedSkillTopK = 5

// maxForwardedSkillBytes is a final byte-size backstop on the rendered <skills>
// block forwarded to code-analysis (~6k tokens). Query-aware top-K selection is
// the primary control; this only guards against a handful of very large skills
// still bloating every downstream code-analysis agent and PR sub-prompt.
const maxForwardedSkillBytes = 24000

// irrelevantAnalysisMarker is the phrase emitted by the code-analysis service when
// the analysis is not relevant to the user's query. Must match the output in llm/code-analysis.
const irrelevantAnalysisMarker = "may not be directly addressing your specific issue"

// codeAgentConversationFailures is a cache namespace for tracking conversations
// where code_analyzer already failed or returned an irrelevant analysis.
// Entries expire after 24 hours to prevent unbounded memory growth.
const codeAgentFailuresCacheNS = "code_agent_conv_failures"

// codeAnalysisNoOpStatus is the execution_status the code-analysis service emits
// for a terminal "no change required" outcome — the requested fix is already
// present, so no diff and no PR were produced. It is a SUCCESS, not a failure.
const codeAnalysisNoOpStatus = "no_op"

// noopGuardPrefix tags a cached no-op terminal answer in codeAgentFailuresCacheNS,
// distinguishing it from a failure-guard entry. A re-dispatch within the same
// message replays this answer as a success instead of re-running the analysis,
// which is what previously drove a re-dispatch loop and a duplicate PR.
const noopGuardPrefix = "NOOP:"

func init() {
	common.CacheCreateNamespace(codeAgentFailuresCacheNS, common.CacheNamespaceWithExpiration(24*time.Hour))

	toolDescription := "Expert AI agent for Deep Code Analysis, Debugging, and Root Cause Analysis (RCA). Correlates logs with source code to find bugs and propose fixes. Requires Git repository access (GitHub or GitLab)."
	toolInput := `Accepts JSON or plain text input.

JSON format (all fields except 'query' are optional):
{
  "query": "Analyze the database insertion error and suggest fixes",
  "errors": ["Error log line 1", "Error log line 2"],
  "git_repo": "https://github.com/owner/repo",
  "git_commit": "abc123def456",
  "target_branch": "prod",
  "namespace": "default",
  "workload": "my-deployment",
  "mode": "explore",
  "raise_pr": false,
  "base_diff": ""
}

Plain text format:
"Analyze why the service is crashing and create a PR to fix it"

The 'query' field (or plain text) is REQUIRED and describes the analytical task. Use this when simple shell commands are insufficient for diagnosing an issue. Set 'target_branch' to the branch the PR should be opened against (e.g. 'prod', 'main', 'release/1.x'); when omitted, the repository default branch is used.

Each call runs a full (expensive) analysis — decide the intended outcome first and make ONE call: question → 'mode': 'explore' (default); code change without a PR → 'mode': 'fix' with 'raise_pr': false (implements the fix and returns its 'git_diff', no PR); code change with a PR → 'raise_pr': true. When a previous call in this conversation already returned a 'git_diff' for the same fix and a PR is now wanted, pass that diff in 'base_diff' with 'raise_pr': true — the pipeline verifies and re-applies it instead of re-deriving the fix.`
	toolOutput := "Structured JSON containing: 'root_cause' (summary), 'affected_files' (array with paths/line numbers), 'suggested_fixes' (remediation steps), 'analysis_details' (comprehensive explanation), 'source_details' (repo and commit), and optional 'pr_url' if raise_pr was enabled."

	codeAnalyzerFactory := func(accountId string) (core.NBAgent, error) {
		return newCodeAgent(accountId), nil
	}
	core.RegisterNBAgentFactoryAndTool(AgentCodeAnalyzer, codeAnalyzerFactory, toolDescription, toolInput, toolOutput)
	// Back-compat: keep the pre-rename "agent_code_2" name resolving as an agent.
	// getSystemAgent does a direct registry lookup and does not honor
	// GetNameAliases, so the legacy name must be a real factory key. Registered
	// agent-only (not as a tool) so it is never re-offered to the model as a
	// second tool — new tool callers use AgentCodeAnalyzer.
	core.RegisterNBAgentFactory(agentCodeAnalyzerLegacyName, codeAnalyzerFactory)
}

func forwardedLLMConfigToMap(c *core.ForwardedLLMConfig) map[string]any {
	m := map[string]any{"provider": c.Provider}
	if c.Model != "" {
		m["model"] = c.Model
	}
	if c.ApiKey != "" {
		m["api_key"] = c.ApiKey
	}
	if c.ApiEndpoint != "" {
		m["endpoint"] = c.ApiEndpoint
	}
	if c.ApiVersion != "" {
		m["api_version"] = c.ApiVersion
	}
	if c.ApiType != "" {
		m["api_type"] = c.ApiType
	}
	if c.Region != "" {
		m["region"] = c.Region
	}
	// Bedrock's credential triple. Sent as a unit — ResolveLLMConfigForForwarding
	// already blanks all three unless the access/secret pair is complete, and a
	// half-set static provider is a hard error in the AWS SDK rather than a
	// fall-through to the pod's own credential chain.
	if c.AccessKey != "" && c.SecretKey != "" {
		m["access_key"] = c.AccessKey
		m["secret_key"] = c.SecretKey
		if c.SessionToken != "" {
			m["session_token"] = c.SessionToken
		}
	}
	// Per-role tier models. Omitting these was why per-role tiering never took
	// effect on the workspace path: ResolveLLMConfigForForwarding populates
	// Tiers and the code-analysis handler consumes it, but this hop dropped it,
	// so every role — router, fixer, reviewer, reflection — fell back to the
	// single run model. Tests existed on both sides of the seam and both passed.
	if len(c.Tiers) > 0 {
		tiers := make(map[string]any, len(c.Tiers))
		for tier, model := range c.Tiers {
			tiers[tier] = model
		}
		m["tiers"] = tiers
	}
	return m
}

// instead of launching a new pod per request.
func evaluateCodeUsingWorkspace(ctx *security.RequestContext, agentRequest core.NBAgentRequest, request CodeAgent2Request, creds []GitCredentials, provider string) (codeAnalysisResult, error) {
	logger := ctx.GetLogger()

	gitToken := resolveGitToken(ctx, creds, request.GitRepo, provider)

	// Build the request body matching the code-analysis server's AgenticAnalyzeRequest
	tenantId := ""
	if ctx.GetSecurityContext() != nil {
		tenantId = ctx.GetSecurityContext().GetTenantId()
	}

	// Fall back to agentRequest.AccountId when the tool input didn't include account_id
	if request.AccountId == "" {
		request.AccountId = agentRequest.AccountId
	}

	workloadName := agentRequest.QueryConfig.Workload
	if workloadName == "" {
		workloadName = request.Workload
	}
	if workloadName == "" {
		workloadName = "unknown"
	}
	workloadNamespace := agentRequest.QueryConfig.Namespace
	if workloadNamespace == "" {
		workloadNamespace = request.Namespace
	}
	if workloadNamespace == "" {
		workloadNamespace = "unknown"
	}

	// When errors are empty (e.g. plain text tool input), use the query as logs
	logs := strings.Join(request.Errors, "\n")
	if logs == "" {
		logs = request.Query
	}

	// Branch to clone from and base the PR against. Sent only when the caller
	// has an actual branch name; otherwise leave it empty so the orchestrator
	// resolves the repo's default branch via `git symbolic-ref refs/remotes/origin/HEAD`.
	// Never fall back to GitCommit here — it's a SHA, not a branch, and passing
	// it through caused `gh pr create --base <SHA>` to fail with "Base ref must be a branch".
	branch := request.TargetBranch

	// Mode selection:
	//   - An explicit request.Mode wins (mirrors code-analysis' EffectiveMode).
	//     This lets a caller run "propose" mode — fix without a PR (mode=fix,
	//     raise_pr=false): the fixer runs and returns a git_diff, but no PR is
	//     opened and the diff is NOT stripped (unlike explore mode). The event
	//     log-analysis step uses this to eagerly generate + store a proposed fix.
	//   - Otherwise fall back to the entrypoint signal (RaisePr): callers that
	//     want a fix+PR set raise_pr=true (recommendation-apply, event auto-raise);
	//     chat mentions don't, and stay in explore mode.
	mode := codeAgentModeExplore
	switch request.Mode {
	case codeAgentModeExplore, codeAgentModeFix:
		mode = request.Mode
	default:
		if request.RaisePr {
			mode = codeAgentModeFix
		}
	}

	// Resolve operator-authored skills mapped to the code agent and forward them to
	// the stateless code-analysis service, which has no skills DB of its own. Unlike
	// the in-process ReAct planners — which lazily load_skills mid-loop and so
	// only pull a skill body when the task needs it — code-analysis receives skill
	// bodies up front over HTTP. Forwarding every mapped skill would bloat every
	// downstream code-analysis prompt and dilute relevance, so we always run
	// question-aware top-K selection here and forward only the skills relevant to the
	// user's question (own and inherited alike). LlmServerSkillSelectionTopK overrides
	// the default K when an operator has tuned it. Failures degrade gracefully —
	// analysis must never be blocked on skills.
	skillsBlock := ""
	{
		// Include the legacy name so knowledge bases mapped to "agent_code_2" in
		// llm_kb_agent_mappings before the rename are still inherited (the lookup
		// UNIONs both names and dedupes by kb id).
		skillAgentNames := append([]string{AgentCodeAnalyzer, agentCodeAnalyzerLegacyName}, agentRequest.InheritSkillsFromAgents...)
		skillQuery := agentRequest.OriginalQuery
		if skillQuery == "" {
			skillQuery = request.Query
		}
		topK := config.Config.LlmServerSkillSelectionTopK
		if topK <= 0 {
			topK = defaultForwardedSkillTopK
		}
		candidates, cErr := toolcore.ListActiveAgentSkillCandidates(ctx, request.AccountId, skillAgentNames)
		if cErr != nil {
			logger.Warn("code: skill candidate fetch failed; skipping operator skills", "error", cErr)
		} else if len(candidates) > 0 {
			// SelectRelevantSkills returns all candidate ids when the query is empty
			// or candidates <= topK, the top-K ids when it can rank, and an empty
			// (non-nil) slice when nothing matched — in which case we forward nothing.
			selectedIds := toolcore.SelectRelevantSkills(skillQuery, candidates, topK)
			block, _, sErr := toolcore.LoadAgentSkillContentsByIDs(ctx, request.AccountId, selectedIds)
			if sErr != nil {
				logger.Warn("code: failed to load selected skill contents", "error", sErr)
			} else if block != "" {
				// Final safety bound: even after top-K narrowing, a few very large
				// skills could bloat every downstream code-analysis prompt. Truncate
				// as a backstop (selection, not truncation, is the primary control).
				if len(block) > maxForwardedSkillBytes {
					logger.Warn("code: forwarded skills block exceeds cap; truncating", "size", len(block), "cap", maxForwardedSkillBytes)
					block = core.TruncateHead(block, maxForwardedSkillBytes) + "\n[operator skills truncated to fit context]"
				}
				skillsBlock = block
				logger.Info("code: forwarding query-relevant operator skills to workspace analysis",
					"size", len(block), "candidates", len(candidates), "selected", len(selectedIds), "top_k", topK)
			}
		}
	}

	analyzeRequest := map[string]any{
		"cloud_account_id":   request.AccountId,
		"tenant":             tenantId,
		"workload_name":      workloadName,
		"workload_namespace": workloadNamespace,
		"workload_kind":      "Deployment",
		"logs":               logs,
		"prompt":             request.Query,
		"git_repository": map[string]any{
			"url":      request.GitRepo,
			"branch":   branch,
			"provider": provider,
		},
		"mode":              mode,
		"raise_pr":          request.RaisePr,
		"event_id":          request.EventId,
		"recommendation_id": request.RecommendationId,
		"workflow_id":       request.WorkflowId,
		"account_id":        request.AccountId,
		"conversation_id":   agentRequest.SessionId,
		"message_id":        agentRequest.MessageId,
	}

	if request.Agent != "" {
		analyzeRequest["agent_id"] = request.Agent
	}

	// Cap mirrors the observation limit — the parent can never legitimately
	// hold a bigger diff than it was shown.
	if baseDiff := strings.TrimSpace(request.BaseDiff); baseDiff != "" {
		analyzeRequest["base_diff"] = core.TruncateMiddle(baseDiff, 48*1024, 16*1024)
	}

	if skillsBlock != "" {
		analyzeRequest["skills"] = skillsBlock
	}

	// Add git credentials
	if gitToken != "" {
		analyzeRequest["git_credentials"] = map[string]any{
			"type":  "token",
			"token": gitToken,
		}
	}

	// Forward the resolved, decrypted LLM config so the stateless code-analysis
	// service runs on the tenant's own LLM integration — or, absent one, on the
	// same default llm-server itself resolved — instead of whatever its startup
	// env happens to name. Degrade gracefully: on any failure, or when no
	// provider resolves at all, omit the block and let the pod use its
	// fallback. The API key is plaintext — never log it.
	if llmCfg, lerr := core.ResolveLLMConfigForForwarding(ctx, agentRequest.AccountId, AgentCodeAnalyzer, agentRequest.ConversationId); lerr != nil {
		logger.Warn("code: failed to resolve LLM config for forwarding; using pod fallback", "error", lerr)
	} else if llmCfg != nil {
		analyzeRequest["llm_config"] = forwardedLLMConfigToMap(llmCfg)
		logger.Info("code: forwarding resolved LLM config to workspace analysis", "provider", llmCfg.Provider, "model", llmCfg.Model, "has_api_key", llmCfg.ApiKey != "")
	}

	// Pre-flight: verify workspace pod is reachable before dispatching analysis
	healthWm := workspace.NewWorkspaceManagerWithTimeout(10 * time.Second)
	if _, healthErr := healthWm.CallAPI(ctx, agentRequest.AccountId, "GET", "/health", nil, nil); healthErr != nil {
		logger.Warn("code: workspace health check failed, attempting recovery", "error", healthErr)
		recoveryWm := workspace.NewWorkspaceManagerWithTimeout(60 * time.Second)
		if _, recoveryErr := recoveryWm.CallAPIOrLazyCreate(ctx, agentRequest.AccountId, "GET", "/health", nil, nil); recoveryErr != nil {
			return codeAnalysisResult{}, fmt.Errorf("workspace pod not healthy after recovery attempt: %w", recoveryErr)
		}
		logger.Info("code: workspace pod recovered successfully")
	}

	// Use workspace manager with short timeout for the initial async POST
	wm := workspace.NewWorkspaceManagerWithTimeout(60 * time.Second)

	logger.Info("code: executing analysis via workspace", "account_id", agentRequest.AccountId, "repo", request.GitRepo, "target_branch", branch)

	// Step 1: POST /analyze — code-analysis returns 202 with analysis_id
	respBytes, err := wm.CallAPIOrLazyCreate(ctx, agentRequest.AccountId, "POST", "/analyze", nil, analyzeRequest)
	if err != nil {
		return codeAnalysisResult{}, fmt.Errorf("workspace /analyze call failed: %w", err)
	}

	var asyncResp map[string]any
	if err := json.Unmarshal(respBytes, &asyncResp); err != nil {
		return codeAnalysisResult{}, fmt.Errorf("workspace /analyze returned invalid JSON: %w", err)
	}

	// Backward compat: if response already has agent_response, it's a sync response
	if _, hasResult := asyncResp["agent_response"]; hasResult {
		return extractAgentResponseWithTokenUsage(respBytes), nil
	}

	if errMsg, _ := asyncResp["error"].(string); errMsg != "" {
		return codeAnalysisResult{}, fmt.Errorf("workspace /analyze failed: %s", errMsg)
	}

	analysisID, _ := asyncResp["analysis_id"].(string)
	status, _ := asyncResp["status"].(string)
	if analysisID == "" || status != "running" {
		return codeAnalysisResult{}, fmt.Errorf("unexpected workspace /analyze response: status=%q analysis_id=%q", status, analysisID)
	}

	// Step 2: Poll /status/{id} every 5s until completed or failed
	logger.Info("code: analysis accepted, polling for progress", "analysis_id", analysisID)
	statusEndpoint := fmt.Sprintf("/status/%s", url.PathEscape(analysisID))
	pollWm := workspace.NewWorkspaceManagerWithTimeout(30 * time.Second)
	lastProgress := ""

	// Tracks the last status persisted per step (keyed by tool_id) so we only
	// write a row when a step first appears or transitions to a terminal state —
	// collapsing per-poll re-writes to ~2 per step regardless of poll count.
	persistedStepStatus := map[string]string{}

	const maxConsecutiveErrors = 12 // 12 * 5s = 60s of consecutive failures before giving up
	const maxPollDuration = 30 * time.Minute
	consecutiveErrors := 0
	pollDeadline := time.Now().Add(maxPollDuration)

	for {
		select {
		case <-ctx.GetContext().Done():
			return codeAnalysisResult{}, fmt.Errorf("analysis timed out while polling for results")
		case <-time.After(5 * time.Second):
		}

		if time.Now().After(pollDeadline) {
			return codeAnalysisResult{}, fmt.Errorf("analysis polling exceeded maximum duration of %v", maxPollDuration)
		}

		statusBytes, err := pollWm.CallAPI(ctx, agentRequest.AccountId, "GET", statusEndpoint, nil, nil)
		if err != nil {
			consecutiveErrors++
			logger.Warn("code: failed to poll analysis status", "error", err, "analysis_id", analysisID,
				"consecutive_errors", consecutiveErrors, "max_consecutive_errors", maxConsecutiveErrors)
			if consecutiveErrors >= maxConsecutiveErrors {
				return codeAnalysisResult{}, fmt.Errorf("analysis polling abandoned after %d consecutive errors: %w", consecutiveErrors, err)
			}
			continue
		}
		consecutiveErrors = 0

		var statusResp map[string]any
		if err := json.Unmarshal(statusBytes, &statusResp); err != nil {
			logger.Warn("code: failed to parse status response", "error", err)
			continue
		}

		// Update progress in DB if changed
		progress, _ := statusResp["progress"].(string)
		if progress != "" && progress != lastProgress {
			lastProgress = progress
			if agentRequest.MessageId != "" {
				core.GetConversationDao().UpdateConversationMessageAsync(
					agentRequest.MessageId, progress, core.ConversationStatusInProgress,
				)
			}
		}

		// Persist the steps taken so far as tool-call rows so the UI shows them
		// live, like other agents. Idempotent + terminal-safe via the dedup map.
		if invs, ok := statusResp["tool_invocations"].([]any); ok {
			persistCodeAnalysisSteps(ctx, agentRequest, invs, persistedStepStatus)
		}

		pollStatus, _ := statusResp["status"].(string)
		switch pollStatus {
		case "completed":
			result, ok := statusResp["result"]
			if !ok {
				return codeAnalysisResult{}, fmt.Errorf("analysis completed but no result returned")
			}
			resultBytes, err := json.Marshal(result)
			if err != nil {
				return codeAnalysisResult{}, fmt.Errorf("failed to marshal analysis result: %w", err)
			}
			logger.Info("code: analysis completed", "analysis_id", analysisID)

			// Fire-and-forget cleanup of cloned repos
			go func() {
				cleanupCtx := security.NewRequestContext(
					context.Background(),
					ctx.GetSecurityContext(),
					ctx.GetLogger(),
					ctx.GetTracer(),
					ctx.GetMeter(),
				)
				// Sanitize with the SAME character mapping the workspace analyze
				// handler uses for its temp dirs (anything outside [A-Za-z0-9_-]
				// becomes '_'). Slack session IDs contain a '.', so the raw ID both
				// failed the workspace's conversation-id validation AND wouldn't
				// have matched the sanitized directory names — every Slack-origin
				// analysis leaked its clone until the pod was replaced.
				cleanID := sanitizeWorkspacePathID(agentRequest.SessionId)
				if cleanID == "" {
					logger.Warn("code: workspace cleanup skipped — empty session id")
					return
				}
				cleanupCmd := fmt.Sprintf("rm -rf /tmp/code-analysis-%s-*", cleanID)
				// The sanitized ID is passed as the conversation_id arg: the
				// workspace pod rejects empty conversation_id (validates non-empty
				// + safe path charset) and would silently no-op the cleanup otherwise.
				if _, cleanupErr := wm.ExecuteCommand(cleanupCtx, agentRequest.AccountId, cleanID, cleanupCmd, nil); cleanupErr != nil {
					logger.Warn("code: workspace cleanup failed", "error", cleanupErr)
				}
			}()

			return extractAgentResponseWithTokenUsage(resultBytes), nil
		case "failed":
			errMsg, _ := statusResp["error"].(string)
			return codeAnalysisResult{}, fmt.Errorf("analysis failed: %s", errMsg)
		}
		// status == "running" → keep polling
	}
}

// codeAnalysisTokenUsage holds token usage data extracted from the code-analysis service response.
type codeAnalysisTokenUsage struct {
	PromptTokens        int
	CompletionTokens    int
	TotalTokens         int
	CachedContentTokens int
	CacheCreationTokens int // Anthropic cache_creation tokens / Gemini new-cache write — billed at provider creation rate
	ThinkingTokens      int // Gemini ThoughtsTokenCount — billed at output rate, otherwise silently $0
	Model               string
	Provider            string
	// Calls, when present, carries one record per LLM API call of the run so
	// each row is priced at its own long-context tier — pricing the run
	// aggregate (1M+ prompt tokens summed over ~50 calls) tiers nearly every
	// run into Gemini's >200K surcharge and ~doubles reported cost.
	Calls        []codeAnalysisTokenUsageCall
	CallsDropped int
}

// codeAnalysisTokenUsageCall is one LLM API call's usage within a run.
type codeAnalysisTokenUsageCall struct {
	PromptTokens        int
	CompletionTokens    int
	TotalTokens         int
	CachedContentTokens int
	CacheCreationTokens int
	ThinkingTokens      int
	LatencySeconds      float64
	Model               string
	Provider            string
	// ModelTier is the tier this individual call ran on. Per-call rather than
	// per-run because a run's roles are tiered independently (router/fixer on
	// retrieval, review on summary, specialists on reasoning).
	ModelTier string
	// TaskType labels non-main-loop calls by phase (reflection, compaction,
	// final_answer). Empty for main-loop calls, which inherit the turn-level
	// query/investigation classification.
	TaskType string
}

// codeAnalysisResult bundles the agent response with optional token usage data.
type codeAnalysisResult struct {
	AgentResponse string
	TokenUsage    *codeAnalysisTokenUsage
}

// parseTokenUsageMap extracts token usage fields from a map[string]any.
func parseTokenUsageMap(tuRaw map[string]any) *codeAnalysisTokenUsage {
	if tuRaw == nil {
		return nil
	}
	tu := &codeAnalysisTokenUsage{}
	if v, ok := tuRaw["prompt_tokens"].(float64); ok {
		tu.PromptTokens = int(v)
	}
	if v, ok := tuRaw["completion_tokens"].(float64); ok {
		tu.CompletionTokens = int(v)
	}
	if v, ok := tuRaw["total_tokens"].(float64); ok {
		tu.TotalTokens = int(v)
	}
	if v, ok := tuRaw["cached_content_tokens"].(float64); ok {
		tu.CachedContentTokens = int(v)
	}
	if v, ok := tuRaw["cache_creation_tokens"].(float64); ok {
		tu.CacheCreationTokens = int(v)
	}
	// Accept both naming conventions: `thinking_tokens` (our DB column) and
	// `thoughts_token_count` (Gemini SDK field name) so the Python service
	// can emit either as it migrates.
	if v, ok := tuRaw["thinking_tokens"].(float64); ok {
		tu.ThinkingTokens = int(v)
	} else if v, ok := tuRaw["thoughts_token_count"].(float64); ok {
		tu.ThinkingTokens = int(v)
	}
	if v, ok := tuRaw["model"].(string); ok {
		tu.Model = v
	}
	if v, ok := tuRaw["provider"].(string); ok {
		tu.Provider = v
	}
	if rawCalls, ok := tuRaw["calls"].([]any); ok {
		for _, rc := range rawCalls {
			cm, ok := rc.(map[string]any)
			if !ok {
				continue
			}
			tu.Calls = append(tu.Calls, parseTokenUsageCallMap(cm))
		}
	}
	if v, ok := tuRaw["calls_dropped"].(float64); ok {
		tu.CallsDropped = int(v)
	}
	return tu
}

// parseTokenUsageCallMap extracts one per-call usage record. Same key set as
// the aggregate plus latency_seconds; missing/mistyped fields default to zero.
func parseTokenUsageCallMap(cm map[string]any) codeAnalysisTokenUsageCall {
	call := codeAnalysisTokenUsageCall{}
	if v, ok := cm["prompt_tokens"].(float64); ok {
		call.PromptTokens = int(v)
	}
	if v, ok := cm["completion_tokens"].(float64); ok {
		call.CompletionTokens = int(v)
	}
	if v, ok := cm["total_tokens"].(float64); ok {
		call.TotalTokens = int(v)
	}
	if v, ok := cm["cached_content_tokens"].(float64); ok {
		call.CachedContentTokens = int(v)
	}
	if v, ok := cm["cache_creation_tokens"].(float64); ok {
		call.CacheCreationTokens = int(v)
	}
	if v, ok := cm["thinking_tokens"].(float64); ok {
		call.ThinkingTokens = int(v)
	} else if v, ok := cm["thoughts_token_count"].(float64); ok {
		call.ThinkingTokens = int(v)
	}
	if v, ok := cm["latency_seconds"].(float64); ok {
		call.LatencySeconds = v
	}
	if v, ok := cm["model"].(string); ok {
		call.Model = v
	}
	if v, ok := cm["provider"].(string); ok {
		call.Provider = v
	}
	if v, ok := cm["model_tier"].(string); ok {
		call.ModelTier = v
	}
	if v, ok := cm["task_type"].(string); ok {
		call.TaskType = v
	}
	return call
}

// codeAnalysisThinkingModelPattern matches Gemini families that produce
// thinking tokens. Used to warn when the code-analysis Python service hasn't
// started emitting `thinking_tokens` for a thinking-class model — silently
// undercounts cost otherwise.
func isCodeAnalysisThinkingModel(model string) bool {
	if model == "" {
		return false
	}
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "gemini-2.5") || strings.HasPrefix(m, "gemini-3")
}

// extractAgentResponseWithTokenUsage extracts agent_response and token_usage from a
// code-analysis response. Falls back to the raw response if parsing fails.
func extractAgentResponseWithTokenUsage(respBytes []byte) codeAnalysisResult {
	var resp map[string]any
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return codeAnalysisResult{AgentResponse: string(respBytes)}
	}

	tuRaw, _ := resp["token_usage"].(map[string]any)
	tu := parseTokenUsageMap(tuRaw)

	// Extract agent_response
	agentResp := string(respBytes)
	if agentResponse, ok := resp["agent_response"]; ok && agentResponse != nil {
		if responseBytes, err := json.Marshal(agentResponse); err == nil {
			agentResp = string(responseBytes)
		}
	}

	return codeAnalysisResult{AgentResponse: agentResp, TokenUsage: tu}
}

// maxCodeStepFieldBytes caps the size of the input/output stored per persisted
// code-analysis step row, bounding DB row size and UI payload.
const maxCodeStepFieldBytes = 16384

// minCodeStepValueCap is the floor for the per-value shrink loop in
// sanitizeCodeStepArgs; below this the map itself is pathological (thousands of
// keys) and we give up on JSON validity rather than loop forever.
const minCodeStepValueCap = 256

// sanitizeCodeStepArgs renders a code-analysis step's input for persistence.
// Two guarantees over a plain TruncateHead(asJSONString(...)):
//   - planner working-memory keys ("_tool_outputs", "__intention") are dropped —
//     they are internal plumbing (the thought is persisted separately) and can
//     arrive from older code-analysis builds that still track them;
//   - the result stays valid JSON under maxCodeStepFieldBytes by shrinking
//     individual values instead of slicing the serialized blob, which left an
//     unparseable fragment the UI could only show as a raw escaped dump.
func sanitizeCodeStepArgs(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return core.TruncateHead(asJSONString(v), maxCodeStepFieldBytes)
	}

	clean := make(map[string]any, len(m))
	for k, val := range m {
		if k == "_tool_outputs" || k == "__intention" {
			continue
		}
		clean[k] = val
	}

	valueCap := maxCodeStepFieldBytes
	for {
		b, err := json.Marshal(clean)
		if err != nil {
			return core.TruncateHead(fmt.Sprintf("%v", clean), maxCodeStepFieldBytes)
		}
		if len(b) <= maxCodeStepFieldBytes {
			return string(b)
		}
		if valueCap < minCodeStepValueCap {
			return core.TruncateHead(string(b), maxCodeStepFieldBytes)
		}
		for k, val := range clean {
			s, isStr := val.(string)
			if !isStr {
				// Oversized non-string values (nested maps/arrays) are replaced by a
				// truncated string of their JSON — a type change, but the row is for
				// display and the alternative is dropping the value entirely.
				if enc := asJSONString(val); len(enc) > valueCap {
					clean[k] = core.TruncateHead(enc, valueCap) + "… [truncated]"
				}
				continue
			}
			if len(s) > valueCap {
				clean[k] = core.TruncateHead(s, valueCap) + "… [truncated]"
			}
		}
		valueCap /= 2
	}
}

// mapCodeStepStatus maps a code-analysis tool-invocation status string to the
// llm-server tool-call status enum. A still-running step is in_progress; an
// errored/failed step is error; anything terminal-and-successful is success.
func mapCodeStepStatus(status string) toolcore.NBToolResponseStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "in_progress", "pending":
		return toolcore.NBToolResponseStatusInProgress
	case "error", "failed", "failure":
		return toolcore.NBToolResponseStatusError
	default: // "", "success", "completed", "complete", "ok"
		return toolcore.NBToolResponseStatusSuccess
	}
}

// persistCodeAnalysisSteps upserts the code-analysis service's tool invocations
// as llm_conversation_tool_calls rows under code_analyzer's agent row, so the UI
// can render the steps the coding agent took live (like other agents). It is
// called on every /status poll; the `persisted` map (toolId → last status)
// de-dupes so a step is only written when it first appears or transitions to a
// terminal state. Non-fatal: failures are logged and skipped.
func persistCodeAnalysisSteps(ctx *security.RequestContext, query core.NBAgentRequest, invocations []any, persisted map[string]string) {
	// Fail-fast on missing tenant/agent scope: AccountId scopes every row to a
	// tenant (an unscoped write would risk cross-tenant leakage), and AgentId is
	// the row these steps hang off of.
	if query.AccountId == "" || query.AgentId == "" || len(invocations) == 0 {
		return
	}

	for i, raw := range invocations {
		inv, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		toolName, _ := inv["tool_name"].(string)
		if toolName == "" {
			toolName = "code_analysis_step"
		}

		// Stable per-step identity across polls: prefer the tracker's step_number,
		// fall back to slice index (append order is stable within a run).
		stepNumber := i + 1
		if sn, ok := inv["step_number"].(float64); ok && sn > 0 {
			stepNumber = int(sn)
		}
		toolId := fmt.Sprintf("code-%03d-%s", stepNumber, toolName)

		status := mapCodeStepStatus(asString(inv["status"]))
		statusStr := string(status)

		// Skip if we've already persisted this step at this (or a later, terminal)
		// status. Once terminal, never rewrite — the DAO upsert also guards this.
		if prev, seen := persisted[toolId]; seen {
			if prev == statusStr {
				continue
			}
			if prev == string(toolcore.NBToolResponseStatusSuccess) ||
				prev == string(toolcore.NBToolResponseStatusError) {
				continue
			}
		}

		toolArgs := sanitizeCodeStepArgs(inv["input"])
		toolResult := core.TruncateHead(asString(inv["output"]), maxCodeStepFieldBytes)
		thought, _ := inv["thought"].(string)
		metadataBytes := buildCodeStepMetadata(inv)

		err := core.GetConversationDao().SaveConversationToolCall(
			query.ConversationId,
			query.AccountId,
			query.UserId,
			query.MessageId,
			query.AgentId,
			toolId,
			toolName,
			toolArgs,
			thought,
			"", // toolArgsSql
			toolResult,
			status,
			toolcore.NBToolTypeTool,
			nil,           // childAgentId
			nil,           // references
			metadataBytes, // metadata (e.g. real per-step duration_ns)
			nil,           // memoryRefs — code2 analysis steps aren't ReAct-attributed
		)
		if err != nil {
			ctx.GetLogger().Warn("code: failed to persist analysis step",
				"error", err, "tool_id", toolId, "tool_name", toolName)
			continue
		}
		persisted[toolId] = statusStr
	}
}

// buildCodeStepMetadata serializes per-step metadata for the tool-call row —
// currently the real tool runtime in nanoseconds. The UI prefers this over the
// row-timestamp diff, which for polled steps reflects poll cadence (or zero when
// a fast step completes within one poll), not the actual runtime. The duration
// arrives as a Go duration string (e.g. "2.275206ms"). Returns nil when there is
// nothing usable to store.
func buildCodeStepMetadata(inv map[string]any) []byte {
	durStr, _ := inv["duration"].(string)
	if durStr == "" {
		return nil
	}
	d, err := time.ParseDuration(durStr)
	if err != nil || d <= 0 {
		return nil
	}
	b, err := json.Marshal(map[string]any{"duration_ns": d.Nanoseconds()})
	if err != nil {
		return nil
	}
	return b
}

// asString renders a value as a display string: strings pass through; everything
// else is JSON-encoded (falling back to fmt for unmarshalable values).
func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return asJSONString(v)
}

// asJSONString JSON-encodes a value, falling back to fmt on error.
func asJSONString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if b, err := json.Marshal(v); err == nil {
		return string(b)
	}
	return fmt.Sprintf("%v", v)
}

// recordCodeAnalysisTokenUsage inserts token usage record(s) for code-analysis
// work. When the service reported per-call records, one row is inserted per
// LLM call so each is priced at its own long-context tier — pricing the run
// aggregate (1M+ prompt tokens summed across ~50 calls) trips Gemini's >200K
// surcharge on every component and ~doubles reported cost (the per-row
// mandate is documented on GetConversationTokenUsage). Without per-call
// records (older pod image), the previous single-aggregate-row behavior is
// kept unchanged.
// modelTier and taskType are the tier attribution for this turn, resolved by the
// CALLER via core.TierAttributionForRecord while the request context is still
// live — this function runs in a goroutine that outlives the request, so it must
// not read the context itself. Both nil on uninstrumented paths, which writes
// NULL and keeps "legacy row" distinguishable from a real tier.
func recordCodeAnalysisTokenUsage(query core.NBAgentRequest, tu *codeAnalysisTokenUsage, latency float64,
	modelTier, taskType *string) {
	if tu == nil || (tu.TotalTokens == 0 && len(tu.Calls) == 0) {
		return
	}

	provider := tu.Provider
	if provider == "" {
		provider = config.Config.LlmProvider
	}
	model := tu.Model
	if model == "" {
		model = config.Config.LlmModel
	}

	var agentUUID *string
	if query.AgentId != "" {
		agentUUID = &query.AgentId
	}

	// Defensive: if the model is in the thinking class but the service
	// didn't emit `thinking_tokens`, cost will silently undercount by
	// output_rate × thinking_tokens. Warn so the gap is visible while the
	// cross-service emission catches up (#30262 sub-item).
	if tu.ThinkingTokens == 0 && isCodeAnalysisThinkingModel(model) && tu.CompletionTokens > 0 {
		slog.Warn("code: code-analysis response missing thinking_tokens for thinking-class model; cost will undercount",
			"model", model,
			"provider", provider,
			"prompt_tokens", tu.PromptTokens,
			"completion_tokens", tu.CompletionTokens,
			"conversation_id", query.ConversationId)
	}
	dao := core.GetConversationDao()

	if dao == nil {
		slog.Debug("code: skipping token usage tracking — conversation DAO unavailable")
		return
	}

	if len(tu.Calls) == 0 {
		latencyPtr := &latency
		record := buildCodeAnalysisUsageRecord(query, agentUUID, dao, provider, model,
			tu.PromptTokens, tu.CachedContentTokens, tu.CacheCreationTokens, tu.CompletionTokens, tu.ThinkingTokens, latencyPtr,
			modelTier, taskType)
		if err := dao.InsertTokenUsage(record); err != nil {
			slog.Error("code: failed to insert token usage",
				"error", err,
				"conversation_id", query.ConversationId,
				"message_id", query.MessageId,
				"account_id", query.AccountId,
			)
		}
		return
	}

	if tu.CallsDropped > 0 {
		slog.Warn("code: code-analysis dropped per-call usage records at its cap; a residual row reconciles the difference",
			"calls_dropped", tu.CallsDropped, "conversation_id", query.ConversationId)
	}

	// Component sums over inserted calls, to reconcile against the aggregate.
	var sumPrompt, sumCached, sumCreation, sumCompletion, sumThinking int
	for i, call := range tu.Calls {
		if call.PromptTokens == 0 && call.CompletionTokens == 0 && call.ThinkingTokens == 0 {
			continue
		}
		callProvider := call.Provider
		if callProvider == "" {
			callProvider = provider
		}
		callModel := call.Model
		if callModel == "" {
			callModel = model
		}
		var latencyPtr *float64
		if call.LatencySeconds > 0 {
			l := call.LatencySeconds
			latencyPtr = &l
		}
		// A call's own attribution wins over the turn-level default: roles are
		// tiered independently, and reflection/compaction calls carry a phase the
		// turn classification cannot express. An unstamped call (older image)
		// falls back, so version skew degrades to turn-level rather than NULL.
		// Copy before taking an address: pointing at the loop variable's fields
		// would force the whole call struct onto the heap each iteration.
		callTier, callTask := modelTier, taskType
		if call.ModelTier != "" {
			tier := call.ModelTier
			callTier = &tier
		}
		if call.TaskType != "" {
			task := call.TaskType
			callTask = &task
		}
		record := buildCodeAnalysisUsageRecord(query, agentUUID, dao, callProvider, callModel,
			call.PromptTokens, call.CachedContentTokens, call.CacheCreationTokens, call.CompletionTokens, call.ThinkingTokens, latencyPtr,
			callTier, callTask)
		err := dao.InsertTokenUsage(record)
		// The FK retry inside InsertTokenUsage nils record.AgentID when the
		// agent row is missing — propagate BEFORE the error check (the record
		// is mutated even when the retry itself fails) so subsequent rows skip
		// the doomed exec instead of repeating it per call.
		agentUUID = record.AgentID
		if err != nil {
			slog.Error("code: failed to insert per-call token usage",
				"error", err, "call_index", i,
				"conversation_id", query.ConversationId,
				"message_id", query.MessageId,
			)
			continue
		}
		sumPrompt += call.PromptTokens
		sumCached += call.CachedContentTokens
		sumCreation += call.CacheCreationTokens
		sumCompletion += call.CompletionTokens
		sumThinking += call.ThinkingTokens
	}

	// Residual row: only when the aggregate exceeds the recorded calls (cap
	// drop or version skew) — keeps SUM(rows) == aggregate for billing.
	resPrompt := max(0, tu.PromptTokens-sumPrompt)
	resCached := max(0, tu.CachedContentTokens-sumCached)
	resCreation := max(0, tu.CacheCreationTokens-sumCreation)
	resCompletion := max(0, tu.CompletionTokens-sumCompletion)
	resThinking := max(0, tu.ThinkingTokens-sumThinking)
	if resPrompt > 0 || resCached > 0 || resCreation > 0 || resCompletion > 0 || resThinking > 0 {
		record := buildCodeAnalysisUsageRecord(query, agentUUID, dao, provider, model,
			resPrompt, resCached, resCreation, resCompletion, resThinking, nil,
			modelTier, taskType)
		if err := dao.InsertTokenUsage(record); err != nil {
			slog.Error("code: failed to insert residual token usage row",
				"error", err, "conversation_id", query.ConversationId)
		}
	}
}

// buildCodeAnalysisUsageRecord assembles one llm_conversation_token_usage row
// with cost computed at insert time from THIS row's own token counts — the
// long-context tier switch inside GetConversationCost then applies per call,
// as billed by the provider. cache_ttl_minutes is no longer written — see
// trackTokenUsage rationale; storage cost lives in llm_cache_lifecycle.
func buildCodeAnalysisUsageRecord(query core.NBAgentRequest, agentUUID *string, dao core.IConversationDao,
	provider, model string, promptTokens, cachedTokens, creationTokens, completionTokens, thinkingTokens int,
	latencySeconds *float64, modelTier, taskType *string) *core.TokenUsageRecord {
	nonCachedPromptTokens := promptTokens - cachedTokens
	if nonCachedPromptTokens < 0 {
		nonCachedPromptTokens = 0
	}
	var costUsd *float64
	if cost, err := dao.GetConversationCost(provider, model, nonCachedPromptTokens, cachedTokens, creationTokens, completionTokens, thinkingTokens, core.TenantForPricing(query.AccountId)); err == nil {
		costUsd = &cost
	} else {
		slog.Debug("code: no pricing data for cost calc", "provider", provider, "model", model, "error", err)
	}

	record := &core.TokenUsageRecord{
		ConversationID:      query.ConversationId,
		MessageID:           query.MessageId,
		AgentID:             agentUUID,
		AgentName:           AgentCodeAnalyzer,
		AccountID:           query.AccountId,
		UserID:              query.UserId,
		LLMProvider:         provider,
		LLMModel:            model,
		InputTokens:         promptTokens,
		OutputTokens:        completionTokens,
		CachedInputTokens:   cachedTokens,
		CacheCreationTokens: creationTokens,
		CostUsd:             costUsd,
		IsCacheHit:          cachedTokens > 0,
		LatencySeconds:      latencySeconds,
		RequestStatus:       "success",
		ModelTier:           modelTier,
		TaskType:            taskType,
	}
	// Thinking tokens stored only when non-zero — distinguishes "model didn't
	// think" from "service didn't emit it". Mirrors trackTokenUsage:2159-2162.
	if thinkingTokens > 0 {
		tt := thinkingTokens
		record.ThinkingTokens = &tt
	}
	return record
}

func newCodeAgent(accountId string) CodeAgent2 {
	return CodeAgent2{
		accountId: accountId,
	}

}

// based on error logs, generate diff if possible
// or provide code-based analysis
type CodeAgent2 struct {
	accountId string
}

func (l CodeAgent2) GetName() string {
	return AgentCodeAnalyzer
}

func (l CodeAgent2) GetNameAliases() []string {
	return []string{agentCodeAnalyzerLegacyName, "code_debugger", "code_error_analyzer", "code_rca_agent"}
}

func (l CodeAgent2) GetDescription() string {
	desc := "Expert AI agent for Deep Code Analysis, Debugging, and Root Cause Analysis (RCA).\n" +
		"Use this agent when the user asks to:\n" +
		"* Debug errors or find the root cause of an issue.\n" +
		"* Analyze service failures by correlating logs with source code.\n" +
		"* Identify bugs and propose code fixes or create Pull Requests (PRs)."

	desc += "\n\n**Do NOT use for:**\n" +
		"* Simple file lookups or running basic shell commands (use 'shell_execute').\n" +
		"* Checking network connectivity or infrastructure state (use 'shell_execute')."

	return desc
}

func (l CodeAgent2) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (l CodeAgent2) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"Your primary goal is to analyze error logs and correlate them with source code to identify root causes and debug issues.",
		"You execute a sophisticated multi-agent system (orchestrator, router, and specialist agents) that uses ReAct planning to analyze repositories.",
		"The analysis includes: cloning the repository, searching for relevant code, correlating logs with code patterns, identifying root causes, and optionally proposing fixes.",
		"If 'raise_pr' is enabled, the system will automatically create a pull request (GitHub) or merge request (GitLab) with the proposed fixes after review.",
		"You have access to Git repositories (GitHub and GitLab) and can analyze code across multiple languages and frameworks.",
		"Provide structured analysis results including: root cause summary, affected files/lines, suggested fixes, and reproduction steps if available.",
		"If an earlier code_analyzer call in this conversation already returned a 'git_diff' for the same fix and a PR is now wanted, call again with 'raise_pr': true and pass that diff in 'base_diff' — the fix pipeline verifies and re-applies it against current code instead of re-deriving the whole fix (a full re-analysis costs hundreds of thousands of tokens).",
	}
	constraints := []string{
		"Requires Git repository access via configured credentials (token or GitHub/GitLab App).",
		"Analysis is performed by spawning either a CLI process (local mode) or Kubernetes pod (cluster mode).",
		"Can automatically detect Git repository from Kubernetes workload annotations if not explicitly provided.",
		"Respect repository size limits and analysis timeouts configured in the system.",
		"Only create PRs/MRs when explicitly requested via 'raise_pr' flag or when the feature is enabled for the tenant.",
	}
	examples := []core.NBAgentPromptExample{}
	return core.NBAgentPrompt{
		Role:         "an expert Root Cause Analysis and Debugging Assistant with deep knowledge of software engineering and error diagnosis",
		Instructions: instructions,
		Constraints:  constraints,
		Examples:     examples,
		OutputFormat: "Structured JSON with analysis results, root causes, affected code locations, and optional PR details",
		Variables:    []string{"query", "errors", "git_repo", "git_commit", "target_branch", "base_diff", "event_id"},
	}
}

type CodeAgent2Request struct {
	Query        string           `json:"query" validate:"required"`
	Errors       []string         `json:"errors"`
	Files        []map[string]any `json:"files"`
	GitRepo      string           `json:"git_repo"`      // Accepts GitHub or GitLab URLs (JSON key kept for backward compatibility)
	GitCommit    string           `json:"git_commit"`    // Git commit hash (JSON key kept for backward compatibility)
	TargetBranch string           `json:"target_branch"` // Base branch for the PR (e.g. "prod", "main"). Empty → repo default branch.
	Agent        string           `json:"agent"`
	RaisePr      bool             `json:"raise_pr"`
	// Mode explicitly selects "explore" (read-only) or "fix". When set it wins
	// over RaisePr, enabling "propose" mode (mode=fix, raise_pr=false): generate
	// and return a diff without opening a PR. Empty → derived from RaisePr.
	Mode string `json:"mode"`
	// BaseDiff is an optional unified diff previously produced for the SAME
	// fix (e.g. a propose-mode run's git_diff earlier in this conversation).
	// The fix pipeline verifies it against current code and adapts it instead
	// of re-deriving the fix from scratch — a "fix then raise PR" sequence
	// costs a verification pass, not a full second analysis.
	BaseDiff         string `json:"base_diff"`
	EventId          string `json:"event_id"`
	RecommendationId string `json:"recommendation_id"`
	WorkflowId       string `json:"workflow_id"` // Originating workflow definition id; forwarded so a raised PR links back to the workflow.
	AccountId        string `json:"account_id"`
	Namespace        string `json:"namespace"`
	Workload         string `json:"workload"`

	// PR followup fields — used when re-executing to address CI failures or review comments
	Followup bool   `json:"followup"`
	PRURL    string `json:"pr_url"`
	PRBranch string `json:"pr_branch"`
}

func (l CodeAgent2) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

func (l CodeAgent2) Execute(ctx *security.RequestContext, query core.NBAgentRequest) (core.NBAgentResponse, error) {
	// Message-scoped retry guard: if a previous call in this message already failed
	// or returned an irrelevant analysis, skip immediately to avoid wasting tokens/time.
	// Scoped to message (not conversation) so new user messages can retry with better input.
	// Only consult the message-scoped guard when both identifiers are present.
	// An empty conversation/message id would collapse the key to ":", colliding
	// across sessions and risking replay of one session's cached answer into
	// another — skip the guard entirely in that case.
	guardKey, hasGuardKey := codeAgentGuardKey(query.ConversationId, query.MessageId)
	if hasGuardKey {
		if prevReason, ok := common.CacheGet(codeAgentFailuresCacheNS, guardKey); ok {
			reason := string(prevReason)
			// A no-op guard means an earlier call in this message already determined the
			// change is already present. Replay that terminal answer as a SUCCESS so the
			// planner stops re-dispatching instead of re-running the whole analysis.
			if answer, isNoOp := strings.CutPrefix(reason, noopGuardPrefix); isNoOp {
				ctx.GetLogger().Info("code: skipping re-dispatch — change already present (no-op) earlier in this message",
					"conversation_id", query.ConversationId, "message_id", query.MessageId)
				return core.NBAgentResponse{Response: []string{answer}}, nil
			}
			ctx.GetLogger().Info("code: skipping — previous analysis in this message was not useful",
				"conversation_id", query.ConversationId, "message_id", query.MessageId, "reason", reason)
			return core.NBAgentResponse{}, fmt.Errorf("code analysis already attempted in this message: %s", reason)
		}
	}

	codeAgentRequest := CodeAgent2Request{}
	err := common.UnmarshalJson([]byte(query.Query), &codeAgentRequest)
	if err != nil {
		// query is not a valid json, pass query directly for analysis
		ctx.GetLogger().Info("code: query is not valid JSON, using as plain text", "unmarshal_error", err, "query_length", len(query.Query))
		codeAgentRequest.Query = query.Query
	} else if codeAgentRequest.Query == "" {
		// JSON unmarshal succeeded but Query field is empty, use the original query text
		ctx.GetLogger().Info("code: JSON unmarshal succeeded but Query field is empty, using original query", "query_length", len(query.Query))
		codeAgentRequest.Query = query.Query
	}

	// Final check: ensure Query field is not empty after all fallback logic
	if codeAgentRequest.Query == "" {
		ctx.GetLogger().Error("code: Query field is required but empty after all processing",
			"original_query_length", len(query.Query),
			"has_errors", len(codeAgentRequest.Errors) > 0,
			"has_github_repo", codeAgentRequest.GitRepo != "",
			"unmarshal_error", err)
		return core.NBAgentResponse{}, errors.New("query is required: provide either a 'query' field in JSON input or a plain text description of the analysis task")
	}

	err = common.ValidateStruct(codeAgentRequest)
	if err != nil {
		ctx.GetLogger().Error("code: validation failed", "error", err, "query_length", len(codeAgentRequest.Query), "has_errors", len(codeAgentRequest.Errors) > 0)
		return core.NBAgentResponse{}, err
	}

	// PR followup mode — skip repo detection and use workspace /analyze with followup fields
	if codeAgentRequest.Followup && codeAgentRequest.PRURL != "" {
		ctx.GetLogger().Info("code: PR followup mode", "pr_url", codeAgentRequest.PRURL, "pr_branch", codeAgentRequest.PRBranch)
		return l.executeFollowup(ctx, query, codeAgentRequest)
	}

	// Extract event_id, recommendation_id, workflow_id, account_id, and git_repo from QueryConfig
	eventId := query.QueryConfig.EventId
	recommendationId := query.QueryConfig.RecommendationId
	workflowId := query.QueryConfig.WorkflowId
	accountId := query.QueryConfig.AccountId

	// RaisePr is intentionally NOT hardcoded here. The entrypoint that built
	// the request (recommendation-apply, event resolution, PR followup, frontend
	// chat) is the source of truth — code_analyzer must pass it through unchanged.
	// Hardcoding `RaisePr = true` here was the cause of PR #29338, where a
	// pure exploration question ("what is the default Postgres connection
	// limit?") got promoted to fix-mode and produced a spurious PR.

	// Check if user selected a repo via followup response
	if codeAgentRequest.GitRepo == "" && query.QueryConfig.ToolConfigs != nil {
		if selectedRepo, ok := query.QueryConfig.ToolConfigs["git_repo"]; ok && selectedRepo != "" {
			codeAgentRequest.GitRepo = selectedRepo
			ctx.GetLogger().Info("code: using git_repo from followup selection", "repo", selectedRepo)
		}
	}

	// Check Config for explicit git_repo before auto-detecting
	if codeAgentRequest.GitRepo == "" && query.QueryConfig.GitRepo != "" {
		codeAgentRequest.GitRepo = query.QueryConfig.GitRepo
		ctx.GetLogger().Info("code: using git_repo from request config", "repo", query.QueryConfig.GitRepo)
	}

	// When input is plain text, try to extract a git URL embedded in the text
	if codeAgentRequest.GitRepo == "" {
		if repoURL := extractGitURLFromText(codeAgentRequest.Query); repoURL != "" {
			codeAgentRequest.GitRepo = repoURL
			ctx.GetLogger().Info("code: extracted git_repo from plain text input", "repo", repoURL)
		}
	}

	// Try to detect GitHub repo if not provided
	if codeAgentRequest.GitRepo == "" {
		ctx.GetLogger().Info("code: git_repo not provided, attempting to detect from annotations and ArgoCD")

		// Extract k8s info from QueryConfig first (higher priority)
		var k8sInfoList []map[string]string
		var namespace, workloadName string

		if query.QueryConfig.Namespace != "" && query.QueryConfig.Workload != "" {
			namespace = query.QueryConfig.Namespace
			workloadName = query.QueryConfig.Workload
			k8sInfoList = append(k8sInfoList, map[string]string{
				"pod_name":      "",
				"namespace":     namespace,
				"workload_name": workloadName,
			})
		} else if codeAgentRequest.Namespace != "" && codeAgentRequest.Workload != "" {
			// Fallback: use namespace/workload from tool input JSON when QueryConfig is empty
			// (QueryConfig comes from the original user request and is empty when code_analyzer is invoked as a tool)
			namespace = codeAgentRequest.Namespace
			workloadName = codeAgentRequest.Workload
			k8sInfoList = append(k8sInfoList, map[string]string{
				"pod_name":      "",
				"namespace":     namespace,
				"workload_name": workloadName,
			})
			ctx.GetLogger().Info("code: using namespace/workload from tool input", "namespace", namespace, "workload", workloadName)
		}

		// If no k8s info from QueryConfig, try to extract from query using LLM
		if len(k8sInfoList) == 0 && codeAgentRequest.Query != "" {
			agentId := query.ParentAgentId
			k8sInfoList, err = l.extractK8sInfo(ctx, query.AccountId, query.ConversationId, query.MessageId, agentId, codeAgentRequest.Query, query.UserId)
			if err != nil {
				ctx.GetLogger().Error("code: failed to extract k8s info", "error", err.Error())
			}
			// Extract first k8s info for ArgoCD detection
			if len(k8sInfoList) > 0 {
				namespace = k8sInfoList[0]["namespace"]
				workloadName = k8sInfoList[0]["workload_name"]
			}
		}

		// Strategy 1: Try GetSourceCodeRepo (includes both Nudgebee annotations AND ArgoCD detection)
		if (namespace != "" && workloadName != "") || eventId != "" {
			ctx.GetLogger().Info("code: attempting source code detection via GetSourceCodeRepo",
				"namespace", namespace, "workload", workloadName, "eventId", eventId)

			sourceCodeInfo := services_server.GetSourceCodeRepo(ctx, query.AccountId, services_server.SourceCodeAnnotationOptions{
				EventId:      eventId,
				WorkloadName: workloadName,
				Namespace:    namespace,
			})

			// Check if we got repo info from any source (Nudgebee annotations, ArgoCD, or both)
			if sourceCodeInfo.CodeRepo != "" {
				codeAgentRequest.GitRepo = sourceCodeInfo.CodeRepo
				ctx.GetLogger().Info("code: detected git repo from Nudgebee annotations", "repo", sourceCodeInfo.CodeRepo, "provider", detectGitProvider(sourceCodeInfo.CodeRepo))

				if sourceCodeInfo.CodeRepoCommitHash != "" {
					codeAgentRequest.GitCommit = sourceCodeInfo.CodeRepoCommitHash
				}
			} else if sourceCodeInfo.ValuesRepoURL != "" {
				// If CodeRepo is empty but we have ArgoCD values repo, use that
				codeAgentRequest.GitRepo = sourceCodeInfo.ValuesRepoURL
				ctx.GetLogger().Info("code: detected git repo from ArgoCD values repo", "repo", sourceCodeInfo.ValuesRepoURL, "argocd_app", sourceCodeInfo.ArgoCDApp, "provider", detectGitProvider(sourceCodeInfo.ValuesRepoURL))

				// Store additional ArgoCD metadata for context
				if sourceCodeInfo.TargetRevision != "" {
					ctx.GetLogger().Info("code: ArgoCD target revision", "revision", sourceCodeInfo.TargetRevision)
				}
				if len(sourceCodeInfo.ValuesFiles) > 0 {
					ctx.GetLogger().Info("code: ArgoCD values files", "files", sourceCodeInfo.ValuesFiles, "path", sourceCodeInfo.ValuesPath)
				}
			}

			// Enhance the query with Helm values file context if available
			if len(sourceCodeInfo.ValuesFiles) > 0 || sourceCodeInfo.HelmChartName != "" {
				var contextParts []string

				// Add Helm chart information
				if sourceCodeInfo.HelmChartName != "" {
					contextParts = append(contextParts, fmt.Sprintf("This workload is deployed using Helm chart '%s' from '%s'.",
						sourceCodeInfo.HelmChartName, sourceCodeInfo.HelmChartRepo))
					ctx.GetLogger().Info("code: Helm chart detected",
						"chart_repo", sourceCodeInfo.HelmChartRepo,
						"chart_name", sourceCodeInfo.HelmChartName,
						"release_name", sourceCodeInfo.HelmReleaseName)
				}

				// Add values file information
				if len(sourceCodeInfo.ValuesFiles) > 0 {
					valuesFilePaths := make([]string, len(sourceCodeInfo.ValuesFiles))
					for i, vf := range sourceCodeInfo.ValuesFiles {
						// Extract filename from $values/path/to/file.yaml format
						vf = strings.TrimPrefix(vf, "$values/")
						if sourceCodeInfo.ValuesPath != "" {
							valuesFilePaths[i] = sourceCodeInfo.ValuesPath + "/" + vf
						} else {
							valuesFilePaths[i] = vf
						}
					}

					branchInfo := ""
					if sourceCodeInfo.TargetRevision != "" {
						branchInfo = fmt.Sprintf(" (branch: %s)", sourceCodeInfo.TargetRevision)
					}

					contextParts = append(contextParts, fmt.Sprintf("Configuration values are in: %s from repository %s%s.",
						strings.Join(valuesFilePaths, ", "),
						sourceCodeInfo.ValuesRepoURL,
						branchInfo))
				}

				// Append context to the query
				if len(contextParts) > 0 {
					var builder strings.Builder
					builder.WriteString(codeAgentRequest.Query)
					builder.WriteString("\n\nDeployment Configuration:\n")
					builder.WriteString(strings.Join(contextParts, " "))
					codeAgentRequest.Query = builder.String()
					ctx.GetLogger().Info("code: enhanced query with deployment context")
				}
			}
		}

		// Strategy 2: Fallback to old method (GetSourceCodeAnnotations) if GetSourceCodeRepo didn't find anything
		if codeAgentRequest.GitRepo == "" {
			ctx.GetLogger().Info("code: falling back to direct annotation lookup")
			meta, err := l.GetSourceCodeAnnotations(ctx, query, k8sInfoList, eventId)
			if err != nil {
				ctx.GetLogger().Info("code: unable to get source code annotations", "error", err)
			}

			// Extract GitHub repo from annotations
			if meta != nil {
				// Try workloads.nudgebee.com prefix first
				if repo, exists := meta["workloads.nudgebee.com/git.repo"]; exists && repo != "" {
					codeAgentRequest.GitRepo = repo
					ctx.GetLogger().Info("code: detected git repo from fallback annotations", "repo", repo, "provider", detectGitProvider(repo))

					if commit, exists := meta["workloads.nudgebee.com/git.hash"]; exists {
						codeAgentRequest.GitCommit = commit
					}
				} else if repo, exists := meta["ci.nudgebee.com/git.repo"]; exists && repo != "" {
					// Fallback to ci.nudgebee.com prefix
					codeAgentRequest.GitRepo = repo
					ctx.GetLogger().Info("code: detected git repo from ci annotations", "repo", repo, "provider", detectGitProvider(repo))

					if commit, exists := meta["ci.nudgebee.com/git.hash"]; exists {
						codeAgentRequest.GitCommit = commit
					}
				}
			}
		}

		// Strategy 3: Final fallback - Try to extract Git repo from user question using LLM
		if codeAgentRequest.GitRepo == "" && codeAgentRequest.Query != "" {
			ctx.GetLogger().Info("code: attempting to extract git repo from user question")
			agentId := query.ParentAgentId
			extractedRepo, _, err := l.extractGitRepoFromQuery(ctx, query.AccountId, query.ConversationId, query.MessageId, agentId, codeAgentRequest.Query, query.UserId)
			if err != nil {
				ctx.GetLogger().Error("code: failed to extract git repo from query", "error", err.Error())
			} else if extractedRepo != "" && isValidGitURL(extractedRepo) {
				codeAgentRequest.GitRepo = extractedRepo
				ctx.GetLogger().Info("code: extracted git repo from LLM query extraction", "repo", extractedRepo)
			} else if extractedRepo != "" {
				ctx.GetLogger().Warn("code: LLM extracted repo is not a valid git URL, discarding", "extracted", extractedRepo)
			}
		}
	}

	// Assign extracted IDs to request
	codeAgentRequest.EventId = eventId
	codeAgentRequest.RecommendationId = recommendationId
	codeAgentRequest.WorkflowId = workflowId
	codeAgentRequest.AccountId = accountId

	var creds []GitCredentials
	creds, repoUrl, provider, err := l.getGitCredentials(ctx, codeAgentRequest.GitRepo, query.AccountId)

	if codeAgentRequest.GitRepo == "" && repoUrl != "" {
		codeAgentRequest.GitRepo = repoUrl
	}
	if err != nil {
		ctx.GetLogger().Error("code: unable to get git creds", "error", err)
		return core.NBAgentResponse{}, err
	}
	if len(creds) == 0 {
		return core.NBAgentResponse{}, errors.New("git credentials are required but none were found for github or gitlab")
	}

	// If repo is still unknown, check if there are multiple projects to ask the user
	if codeAgentRequest.GitRepo == "" {
		var allProjectURLs []string
		for _, cred := range creds {
			for _, project := range cred.Projects {
				if repoURL := resolveProjectRepoURL(project, cred); repoURL != "" {
					allProjectURLs = append(allProjectURLs, repoURL)
				}
			}
		}
		if len(allProjectURLs) > 1 {
			// Try auto-detection: fuzzy match workload name against project URLs
			detectedWorkload := codeAgentRequest.Workload
			if detectedWorkload == "" {
				detectedWorkload = query.QueryConfig.Workload
			}
			if matched := fuzzyMatchRepo(detectedWorkload, allProjectURLs); matched != "" {
				ctx.GetLogger().Info("code: auto-resolved repository from workload name",
					"workload", detectedWorkload, "matched_repo", matched, "candidates", len(allProjectURLs))
				codeAgentRequest.GitRepo = matched
			}

			if codeAgentRequest.GitRepo == "" {
				if matched := l.selectRepoFromConversationContext(ctx, query, codeAgentRequest.Query, allProjectURLs); matched != "" {
					codeAgentRequest.GitRepo = matched
				}
			}

			// If auto-detection failed, ask the user via followup
			if codeAgentRequest.GitRepo == "" {
				ctx.GetLogger().Info("code: asking user to select repository", "count", len(allProjectURLs))
				return core.NBAgentResponse{
					Response: []string{"I found multiple repositories in your git integration. Which repository should I analyze?"},
					Status:   core.ConversationStatusWaiting,
					FollowupRequest: core.FollowupRequest{
						Question:        "Which repository should I analyze?",
						FollowupType:    core.FollowupTypeToolConfig,
						FollowupOptions: allProjectURLs,
						AgentName:       l.GetName(),
						ToolName:        "git_repo",
					},
				}, nil
			}

			// Repo was resolved from the candidate list (fuzzy or context LLM) — refresh
			// creds/repoUrl/provider so downstream uses provider-aware credentials and
			// logging instead of the stale empty-repo values from the first call.
			refreshedCreds, refreshedRepoUrl, refreshedProvider, refreshErr := l.getGitCredentials(ctx, codeAgentRequest.GitRepo, query.AccountId)
			if refreshErr != nil {
				ctx.GetLogger().Error("code: unable to refresh git creds after repo auto-resolution", "error", refreshErr, "repo", codeAgentRequest.GitRepo)
				return core.NBAgentResponse{}, refreshErr
			}
			if len(refreshedCreds) == 0 {
				return core.NBAgentResponse{}, errors.New("git credentials are required but none were found for the auto-resolved repository")
			}
			creds = refreshedCreds
			repoUrl = refreshedRepoUrl
			provider = refreshedProvider
		}
	}

	ctx.GetLogger().Info("code: using git provider", "provider", provider, "repo", repoUrl)

	// A second fix request in the same conversation is a retry, not a new bug.
	// Amend the PR we already opened instead of cutting another branch and
	// opening a duplicate. Routed through the followup path, which checks out
	// the PR's head branch and commits there; request.Query — the caller's
	// "the previous fix was rejected because …" instruction — is forwarded to
	// the workspace as the prompt, so the retry's intent is not lost.
	if codeAgentRequest.RaisePr && !codeAgentRequest.Followup {
		if prURL, prBranch := findOpenPRForRequest(ctx, query, codeAgentRequest.GitRepo); prURL != "" {
			ctx.GetLogger().Info("code: reusing the open PR this conversation already raised",
				"pr_url", prURL, "pr_branch", prBranch, "repo", codeAgentRequest.GitRepo)
			codeAgentRequest.Followup = true
			codeAgentRequest.PRURL = prURL
			codeAgentRequest.PRBranch = prBranch
			return l.executeFollowup(ctx, query, codeAgentRequest)
		}
	}

	// execute via workspace /analyze endpoint
	startTime := time.Now()
	wsResult, err := evaluateCodeUsingWorkspace(ctx, query, codeAgentRequest, creds, provider)
	latency := time.Since(startTime).Seconds()
	if err != nil {
		ctx.GetLogger().Error("code: failed to execute via workspace", "error", err.Error())
		// Write the message-scoped guard on failure too. A poll timeout here
		// usually means the analysis is STILL RUNNING server-side (the workspace
		// pod does not cancel it when polling stops) — without the guard, the
		// planner's natural "tool errored, retry" reaction dispatches a second
		// /analyze and two full analyses run for one message.
		if guardKey, ok := codeAgentGuardKey(query.ConversationId, query.MessageId); ok {
			_ = common.CacheSet(codeAgentFailuresCacheNS, guardKey,
				[]byte(fmt.Sprintf("previous attempt failed and may still be running server-side: %v", err)))
		}
		return core.NBAgentResponse{}, err
	}
	ctx.GetLogger().Info("Workspace /analyze Output", "output_length", len(wsResult.AgentResponse))

	// Record token usage from code-analysis service (fire-and-forget). Tier
	// attribution is resolved here, on the request goroutine, because the
	// recorder outlives the request context.
	modelTier, taskType := core.TierAttributionForRecord(ctx)
	go recordCodeAnalysisTokenUsage(query, wsResult.TokenUsage, latency, modelTier, taskType)

	// workspace path returns agent_response directly — parse and enrich
	var actualResponse map[string]any
	if err := json.Unmarshal([]byte(wsResult.AgentResponse), &actualResponse); err != nil {
		return core.NBAgentResponse{
			Response: []string{wsResult.AgentResponse},
		}, nil
	}
	actualResponse["source_details"] = map[string]any{
		"workloads.nudgebee.com/git.hash": codeAgentRequest.GitCommit,
		"workloads.nudgebee.com/git.repo": codeAgentRequest.GitRepo,
	}
	jsonResponse, err := json.Marshal(actualResponse)
	if err != nil {
		return core.NBAgentResponse{}, err
	}
	responseStr := string(jsonResponse)
	finalResponse := handleAnalysisResult(ctx, query.ConversationId, query.MessageId, responseStr)
	go trackPRInResolution(ctx, query, responseStr, codeAgentRequest.GitRepo, provider)
	return core.NBAgentResponse{
		Response: []string{finalResponse},
	}, nil
}

// handleAnalysisResult inspects the analysis result, maintains the message-scoped
// guard, and returns the response string to surface to the planner. For a terminal
// no-op (the change is already present) it returns an explanatory answer and caches
// it so the same message isn't re-dispatched; for an irrelevant analysis it stores a
// failure guard; otherwise it clears any prior guard so the message can recover.
func handleAnalysisResult(ctx *security.RequestContext, conversationId, messageId, responseStr string) string {
	// The guard is keyed on conversation+message; without both we can't form a
	// collision-free key, so we still surface the right response but skip caching.
	guardKey, hasGuardKey := codeAgentGuardKey(conversationId, messageId)
	if isIrrelevantAnalysis(responseStr) {
		ctx.GetLogger().Info("code: analysis was not relevant to user query, storing for retry guard",
			"conversation_id", conversationId, "message_id", messageId)
		if hasGuardKey {
			_ = common.CacheSet(codeAgentFailuresCacheNS, guardKey,
				[]byte("analysis was not relevant to the user's issue"))
		}
		return responseStr
	}
	if answer, isNoOp := noOpTerminalAnswer(responseStr); isNoOp {
		ctx.GetLogger().Info("code: analysis was a no-op (change already present), surfacing terminal answer",
			"conversation_id", conversationId, "message_id", messageId)
		// Cache the terminal answer so a same-message re-dispatch replays it as success.
		if hasGuardKey {
			_ = common.CacheSet(codeAgentFailuresCacheNS, guardKey, []byte(noopGuardPrefix+answer))
		}
		return answer
	}
	// Genuine success — clear any previous failure so the same message can recover
	if hasGuardKey {
		_ = common.CacheDelete(codeAgentFailuresCacheNS, guardKey)
	}
	return responseStr
}

// codeAgentGuardKey builds the message-scoped guard cache key and reports whether
// it is usable. Both identifiers must be non-empty — otherwise the key would
// collapse to ":" and collide across unrelated sessions.
func codeAgentGuardKey(conversationId, messageId string) (string, bool) {
	if conversationId == "" || messageId == "" {
		return "", false
	}
	return conversationId + ":" + messageId, true
}

// isIrrelevantAnalysis checks if the code-analysis response indicates the analysis
// was not relevant to the user's query. This is determined by the code-analysis
// service's relevance check (not a failure — the analysis ran but was off-topic).
// NOTE: The marker phrase must match the relevance check output in llm/code-analysis.
func isIrrelevantAnalysis(response string) bool {
	return strings.Contains(response, irrelevantAnalysisMarker)
}

// noOpTerminalAnswer reports whether the code-analysis result is a terminal no-op
// — the requested change is already present, so no diff and no PR were produced —
// and, if so, returns an explanatory answer composed from the agent's findings.
//
// A no-op is a SUCCESS, not a failure: surfacing it as a clear terminal answer (with
// the reasoning the agent actually found) stops the planner from re-dispatching the
// tool in search of a PR that should never be created. The authoritative signal is
// execution_status == "no_op" emitted by the code-analysis orchestrator.
func noOpTerminalAnswer(responseStr string) (string, bool) {
	var resp map[string]any
	if err := json.Unmarshal([]byte(responseStr), &resp); err != nil {
		return "", false
	}
	if execStatus, _ := resp["execution_status"].(string); execStatus != codeAnalysisNoOpStatus {
		return "", false
	}

	// Compose an explanatory message from whatever findings the agent produced, so a
	// human sees the reasoning (what already satisfies the request and where), not a
	// silent skip. Never hardcode a specific case — use the agent's own fields.
	detail := firstNonEmptyField(resp, "pr_creation_reason", "description", "root_cause_analysis", "execution_summary")
	var b strings.Builder
	b.WriteString("No pull request was created — the requested change is already present in the repository, so no modifications were needed.")
	if detail != "" {
		b.WriteString("\n\n")
		b.WriteString(detail)
	}
	message := b.String()

	// Return a structured envelope rather than bare prose so programmatic callers
	// (e.g. the api-server code-agent PR flow) key off execution_status == "no_op"
	// instead of parsing the sentence, while humans still get the readable `message`.
	// Preserve the orchestrator's original fields (execution_status, pr_creation_*).
	resp["message"] = message
	envelope, err := json.Marshal(resp)
	if err != nil {
		return message, true // fall back to prose if the envelope can't be marshaled
	}
	return string(envelope), true
}

// firstNonEmptyField returns the first non-empty string value among the given keys.
func firstNonEmptyField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// trackPRInResolution inserts an event_resolution row when code_analyzer creates a PR.
// This enables the pr-lifecycle-check cron to detect the PR and trigger automated
// follow-up for CI failures and review comments. Runs as fire-and-forget.
func trackPRInResolution(ctx *security.RequestContext, query core.NBAgentRequest, responseStr string, gitRepo string, provider string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("code: panic in trackPRInResolution", "recover", r)
		}
	}()

	var response map[string]any
	if err := json.Unmarshal([]byte(responseStr), &response); err != nil {
		return
	}

	prInfoRaw, ok := response["automated_fix_pr_info"]
	if !ok || prInfoRaw == nil {
		return
	}
	prMap, ok := prInfoRaw.(map[string]any)
	if !ok {
		return
	}

	prURL, _ := prMap["url"].(string)
	if prURL == "" {
		return
	}

	prNumber, _ := prMap["number"].(float64)
	branch, _ := prMap["branch"].(string)

	// Parse org/repo from git URL (e.g. "https://github.com/nudgebee/nudgebee-infra" → "nudgebee", "nudgebee-infra")
	org, repo := parseOrgRepo(gitRepo)

	tenantId := ""
	if ctx.GetSecurityContext() != nil {
		tenantId = ctx.GetSecurityContext().GetTenantId()
	}

	// Build metadata matching prMetadata struct in api-server/services/account/adapter/pr_lifecycle.go
	metadata := map[string]any{
		"pr_url":     prURL,
		"pr_number":  int(prNumber),
		"repo_url":   gitRepo,
		"branch":     branch,
		"pr_branch":  branch,
		"provider":   provider,
		"org":        org,
		"repo":       repo,
		"tenant_id":  tenantId,
		"account_id": query.AccountId,
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		ctx.GetLogger().Error("code: failed to marshal PR metadata for resolution", "error", err)
		return
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("code: failed to get DB for PR resolution", "error", err)
		return
	}

	// Resolve the event id to link the PR against. event_resolution.event_id
	// is NOT NULL, so we always need a value. resolvePRTrackingEventId returns
	// hadEventAnchor=true when the request points at a specific event (explicit
	// QueryConfig.EventId, or session_id of the form `event-<fp>`). In that
	// case, an empty result means we lost an event we should have found — bail
	// rather than write a mislinked row that breaks the UI's event lookup. With
	// hadEventAnchor=false the request has no event signal at all (Slack
	// instant-notification flow, plain user chat), so falling back to the
	// conversation id is honest: the row is conversation-scoped and the
	// pr-lifecycle cron picks it up via meta.AccountID/TenantID.
	eventId, hadEventAnchor := resolvePRTrackingEventId(ctx, dbms.Db, query)
	if eventId == "" {
		if hadEventAnchor {
			ctx.GetLogger().Warn("code: event anchor present but lookup failed, skipping event_resolution insert",
				"pr_url", prURL,
				"session_id", query.SessionId,
				"conversation_id", query.ConversationId,
				"account_id", query.AccountId,
			)
			return
		}
		if query.ConversationId == "" {
			ctx.GetLogger().Warn("code: no event anchor and no conversation id, skipping event_resolution insert",
				"pr_url", prURL,
				"session_id", query.SessionId,
				"account_id", query.AccountId,
			)
			return
		}
		eventId = query.ConversationId
	}

	_, err = dbms.Db.Exec(
		`INSERT INTO event_resolution (id, event_id, type, data, status, type_reference_id, resolver_type, resolver_id, status_message, pr_lifecycle_state, pr_iteration_count)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		common.GenerateUUID(),
		eventId,
		"PullRequest",
		string(metadataJSON),
		"InProgress",
		prURL,
		"NBLLM",
		query.ConversationId,
		"PR raised successfully",
		"created",
		0,
	)
	if err != nil {
		ctx.GetLogger().Error("code: failed to insert PR resolution row", "error", err, "pr_url", prURL)
		return
	}

	ctx.GetLogger().Info("code: PR resolution row created for lifecycle tracking",
		"pr_url", prURL, "event_id", eventId, "conversation_id", query.ConversationId)
}

// findOpenPRForRequest returns the url and head branch of a PR that this same
// conversation (or event) already opened against gitRepo and that is still
// open. Returns two empty strings when there is none.
//
// code_analyzer keeps no state between calls: nothing stopped an orchestrator
// that reviewed its own fix and asked again from getting a fresh clone, a fresh
// fix/<slug>-<unix-ts> branch, and a second PR for the same bug. That is how
// PRs #35092 and #35094 came to fix one missing integrationId sixteen minutes
// apart — while #35092's own followup agent was fixing it on the other branch.
// The resolution rows trackPRInResolution writes are the memory we were
// missing, so read them back before opening anything new.
//
// Fails open: any lookup error returns no PR, so a retry still produces a fix
// rather than nothing.
func findOpenPRForRequest(ctx *security.RequestContext, query core.NBAgentRequest, gitRepo string) (string, string) {
	if gitRepo == "" {
		return "", ""
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Warn("code: cannot check for an existing open PR", "error", err)
		return "", ""
	}

	// Same anchor the writer uses, so we read back exactly what it wrote. An
	// event-anchored request that failed to resolve must NOT fall back to the
	// conversation id — see resolvePRTrackingEventId.
	anchorId, hadEventAnchor := resolvePRTrackingEventId(ctx, dbms.Db, query)
	if anchorId == "" {
		if hadEventAnchor {
			return "", ""
		}
		anchorId = query.ConversationId
	}
	if anchorId == "" {
		return "", ""
	}

	var row struct {
		PRURL    string `db:"pr_url"`
		PRBranch string `db:"pr_branch"`
	}
	// The lifecycle states below mirror the cron's own selection in
	// api-server/services/account/adapter/pr_lifecycle.go. 'addressing' is
	// included because a followup already in flight is still an open PR to
	// amend, not a reason to open a second one.
	err = dbms.Db.Get(&row,
		`SELECT COALESCE(data->>'pr_url', '')    AS pr_url,
		        COALESCE(data->>'pr_branch', '') AS pr_branch
		   FROM event_resolution
		  WHERE event_id = $1
		    AND type = 'PullRequest'
		    AND status = 'InProgress'
		    AND pr_lifecycle_state IN ('created', 'needs_followup', 'addressing')
		    AND data->>'repo_url' = $2
		  ORDER BY created_at DESC
		  LIMIT 1`,
		anchorId, gitRepo,
	)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			ctx.GetLogger().Warn("code: existing-PR lookup failed", "error", err, "anchor_id", anchorId)
		}
		return "", ""
	}
	// A row with no branch recorded is useless for amending — we would not know
	// what to check out. Treat it as no PR and let a new one be opened.
	if row.PRURL == "" || row.PRBranch == "" {
		return "", ""
	}
	return row.PRURL, row.PRBranch
}

// prTrackingEventLookup is the narrow DB interface resolvePRTrackingEventId
// needs. sqlx.DB satisfies this; tests provide a fake.
type prTrackingEventLookup interface {
	QueryRow(query string, args ...interface{}) *sql.Row
	Get(dest interface{}, query string, args ...interface{}) error
}

// resolvePRTrackingEventId returns the events.id that a PR-tracking row should
// point at, plus a flag indicating whether the request had an event anchor.
// Resolution order:
//
//  1. query.QueryConfig.EventId — explicit event origin (most common).
//  2. query.SessionId parsed as `event-<fingerprint>` → most recent
//     events.id for that fingerprint on this account. Covers the
//     investigation-session flow where QueryConfig.EventId isn't threaded
//     through but the conversation is rooted on an event.
//  3. query.ConversationId → llm_conversations.session_id → same fingerprint
//     lookup as above. Belt-and-braces for paths that don't set SessionId
//     on the agent request.
//
// hadEventAnchor=true means the request *should* have resolved to an event:
// either QueryConfig.EventId was set, or the session_id (direct or via
// conversation lookup) had the `event-` prefix. If we still couldn't resolve,
// callers MUST NOT fall back to the conversation id — writing
// llm_conversations.id into an event_id column silently poisons the UI
// lookup and the pr-lifecycle cron.
//
// hadEventAnchor=false means the request had no event signal at all (Slack
// InstantNotification flow, plain user chat). Callers may legitimately use
// the conversation id as the row's anchor: there is no event UUID to lose,
// and the pr-lifecycle cron resolves tenant/account from metadata.
func resolvePRTrackingEventId(ctx *security.RequestContext, db prTrackingEventLookup, query core.NBAgentRequest) (string, bool) {
	if query.QueryConfig.EventId != "" {
		return query.QueryConfig.EventId, true
	}
	hadAnchor := strings.HasPrefix(query.SessionId, "event-")
	if id := lookupEventIdBySessionId(ctx, db, query.SessionId, query.AccountId); id != "" {
		return id, true
	}
	if query.ConversationId != "" {
		var sessionId string
		err := db.Get(&sessionId,
			`SELECT session_id FROM llm_conversations WHERE id = $1`,
			query.ConversationId,
		)
		if err == nil && sessionId != "" {
			if strings.HasPrefix(sessionId, "event-") {
				hadAnchor = true
			}
			if id := lookupEventIdBySessionId(ctx, db, sessionId, query.AccountId); id != "" {
				return id, true
			}
		}
	}
	return "", hadAnchor
}

// lookupEventIdBySessionId extracts an events.id from a session id of the
// form `event-<fingerprint>`. Picks the most recent event with that
// fingerprint for the account — that is the occurrence the active
// investigation is about (deduped events share fingerprints, and the LLM
// context was assembled from the latest one). Returns "" for non-matching
// session formats or when the lookup fails.
func lookupEventIdBySessionId(ctx *security.RequestContext, db prTrackingEventLookup, sessionId, accountId string) string {
	const prefix = "event-"
	if !strings.HasPrefix(sessionId, prefix) {
		return ""
	}
	fingerprint := strings.TrimPrefix(sessionId, prefix)
	if fingerprint == "" || accountId == "" {
		return ""
	}
	var eventId string
	err := db.Get(&eventId,
		`SELECT id::text FROM events
		 WHERE fingerprint = $1 AND cloud_account_id = $2
		 ORDER BY created_at DESC LIMIT 1`,
		fingerprint, accountId,
	)
	if err != nil {
		ctx.GetLogger().Debug("code: no event found for session fingerprint",
			"session_id", sessionId, "account_id", accountId, "error", err)
		return ""
	}
	return eventId
}

// parseOrgRepo extracts org and repo name from a git URL.
// e.g. "https://github.com/nudgebee/nudgebee-infra" → ("nudgebee", "nudgebee-infra")
func parseOrgRepo(gitURL string) (string, string) {
	gitURL = strings.TrimSuffix(gitURL, ".git")
	parsed, err := url.Parse(gitURL)
	if err != nil {
		return "", ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2], parts[len(parts)-1]
	}
	return "", ""
}

// fuzzyMatchRepo attempts to match a workload name against a list of project URLs.
// It filters out infra repos and uses substring matching on the repo path components.
// Returns the best-matching URL, or empty string if no confident match is found.
func fuzzyMatchRepo(workloadName string, projectURLs []string) string {
	if workloadName == "" {
		return ""
	}

	workloadLower := strings.ToLower(workloadName)
	// Normalize: "llm-server" → "llm", "server", "llm-server"
	workloadParts := strings.Split(workloadLower, "-")

	var nonInfraURLs []string
	for _, u := range projectURLs {
		uLower := strings.ToLower(u)
		if strings.Contains(uLower, "infra") || strings.Contains(uLower, "infrastructure") || strings.Contains(uLower, "devops") || strings.Contains(uLower, "helm-charts") {
			continue
		}
		nonInfraURLs = append(nonInfraURLs, u)
	}

	// Try exact substring match of workload name against repo path
	var matches []string
	for _, u := range nonInfraURLs {
		repoPath := strings.ToLower(u)
		// Extract repo name from URL: "https://github.com/org/repo-name" → "repo-name"
		parts := strings.Split(strings.TrimSuffix(repoPath, ".git"), "/")
		repoName := ""
		if len(parts) > 0 {
			repoName = parts[len(parts)-1]
		}

		// Match: workload name contains repo name, or repo name contains workload name
		if repoName != "" && (strings.Contains(workloadLower, repoName) || strings.Contains(repoName, workloadLower)) {
			matches = append(matches, u)
			continue
		}
		// Match: any workload part (e.g., "llm" from "llm-server") matches repo name
		for _, part := range workloadParts {
			if len(part) >= 3 && strings.Contains(repoName, part) {
				matches = append(matches, u)
				break
			}
		}
	}

	if len(matches) == 1 {
		return matches[0]
	}

	// Multiple matches — ambiguous, return empty to trigger user selection
	if len(matches) > 1 {
		return ""
	}

	// No matches but only one non-infra repo — safe to use
	if len(nonInfraURLs) == 1 {
		return nonInfraURLs[0]
	}

	// No confident match — return empty to trigger user selection
	return ""
}

// resolveGitToken produces the token the workspace clone authenticates with.
// Both entrypoints that call the code-analysis workspace — the analysis run and
// the PR followup — need the identical resolution, and they previously carried
// two copies of it that had already drifted apart in their logging.
func resolveGitToken(ctx *security.RequestContext, creds []GitCredentials, repoURL string, provider string) string {
	logger := ctx.GetLogger()

	gitToken := ""
	if len(creds) > 0 {
		cred := selectGitCredential(logger, creds, repoURL, provider)
		switch cred.AuthType {
		case "token":
			gitToken = cred.Password
		case "application":
			if provider == "github" {
				installationID := int64(0)
				if _, err := fmt.Sscanf(cred.Password, "%d", &installationID); err != nil {
					// A GitHub App integration stores the installation ID in the
					// password field; if it is not a plain integer we cannot mint a
					// token. Swallowing this silently produced an invisible failure:
					// the workspace clone then ran unauthenticated and the analysis
					// abstained with "repository inaccessible".
					logger.Warn("code: github app credential password is not a numeric installation id; cannot mint token", "error", err)
				} else {
					token, err := utils.GetGithubAppInstallationToken(ctx.GetContext(), cred.Url, installationID)
					if err != nil {
						logger.Warn("code: failed to mint github app installation token; clone will be unauthenticated", "error", err, "installation_id", installationID)
					} else {
						gitToken = token
					}
				}
			} else {
				gitToken = cred.Password
			}
		}
	}

	// Fallback to env var when no credentials are provided (e.g. local testing)
	if gitToken == "" {
		if provider == "gitlab" {
			gitToken = os.Getenv("GITLAB_TOKEN")
		} else {
			gitToken = os.Getenv("GITHUB_TOKEN")
		}
	}
	return gitToken
}

// selectGitCredential picks the credential to authenticate the clone of repoURL.
//
// getGitCredentials returns every enabled git integration on the tenant, ordered
// only by provider, so creds[0] is arbitrary whenever a tenant has more than one.
// Cloning a private repo with the wrong org's token fails as "Repository not
// found" — indistinguishable from a typo, and the analysis then abstains with a
// misleading "nothing to change". Match on the repo list the integration
// enumerated at connect time; fall back to provider, then to the first
// credential, so a tenant whose project list is empty or stale behaves exactly
// as before.
func selectGitCredential(logger *slog.Logger, creds []GitCredentials, repoURL string, provider string) GitCredentials {
	if len(creds) == 1 {
		return creds[0]
	}

	if org, repo := parseOrgRepo(repoURL); org != "" && repo != "" {
		want := strings.ToLower(org + "/" + repo)
		for _, cred := range creds {
			if !credentialCoversRepo(cred, want) {
				continue
			}
			if logger != nil {
				logger.Info("code: selected git credential covering the target repository",
					"repo", want, "integration_user", cred.Username, "provider", cred.Provider)
			}
			return cred
		}
		if logger != nil {
			// Worth a warning: the clone is about to run with a credential that
			// does not list this repo, which is the failure mode above.
			logger.Warn("code: no git credential lists the target repository; falling back",
				"repo", want, "credential_count", len(creds), "provider", provider)
		}
	}

	for _, cred := range creds {
		if cred.Provider == provider {
			return cred
		}
	}
	return creds[0]
}

// credentialCoversRepo reports whether cred's enumerated projects include the
// "org/repo" identity in want (lowercased). Projects carry either a full URL or
// a bare "org/repo" key, so both are normalized through parseOrgRepo.
func credentialCoversRepo(cred GitCredentials, want string) bool {
	for _, project := range cred.Projects {
		candidate := resolveProjectRepoURL(project, cred)
		if candidate == "" {
			continue
		}
		if org, repo := parseOrgRepo(candidate); org != "" && repo != "" {
			if strings.ToLower(org+"/"+repo) == want {
				return true
			}
		}
	}
	return false
}

// resolveProjectRepoURL extracts and constructs a full repository URL from a project map entry.
func resolveProjectRepoURL(project map[string]string, cred GitCredentials) string {
	// Try "repository" key first
	if repoUrl, exists := project["repository"]; exists && repoUrl != "" {
		return repoUrl
	}
	// Try "repo" key
	if repoUrl, exists := project["repo"]; exists && repoUrl != "" {
		return repoUrl
	}
	// Try "key" key and construct full URL
	repoKey, exists := project["key"]
	if !exists || repoKey == "" {
		return ""
	}
	if strings.HasPrefix(repoKey, "https://") || strings.HasPrefix(repoKey, "http://") {
		return repoKey
	}
	baseURL := cred.Url
	switch cred.Provider {
	case "gitlab":
		if baseURL == "" {
			baseURL = "https://gitlab.com"
		}
		return strings.TrimSuffix(baseURL, "/") + "/" + repoKey
	case "github":
		if baseURL == "" {
			baseURL = "https://github.com"
		}
		baseURL = strings.Replace(baseURL, "api.github.com", "https://github.com", 1)
		if !strings.HasPrefix(baseURL, "https://") && !strings.HasPrefix(baseURL, "http://") {
			baseURL = "https://" + baseURL
		}
		return strings.TrimSuffix(baseURL, "/") + "/" + repoKey
	default:
		return "https://github.com/" + repoKey
	}
}

func (l CodeAgent2) getGitCredentials(ctx *security.RequestContext, repo string, accountId string) ([]GitCredentials, string, string, error) {

	credentials := []GitCredentials{}
	actualRepo := repo
	detectedProvider := ""

	// Detect provider from repo URL if provided
	if repo != "" {
		detectedProvider = detectGitProvider(repo)
		repoSplits := strings.Split(repo, "/")
		if len(repoSplits) >= 2 {
			actualRepo = repoSplits[len(repoSplits)-2] + "/" + repoSplits[len(repoSplits)-1]
		}
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return credentials, actualRepo, detectedProvider, err
	}

	// Determine preferred provider for ordering results
	preferredProvider := detectedProvider
	if preferredProvider == "" {
		preferredProvider = "github" // Default preference
	}

	rows, err := dbms.Db.Queryx(`
		SELECT
			i.type as provider,
			MAX(CASE WHEN icv.name = 'username' THEN icv.value END) as username,
			MAX(CASE WHEN icv.name = 'url' THEN icv.value END) as url,
			MAX(CASE WHEN icv.name = 'password' THEN icv.value END) as password,
			BOOL_OR(CASE WHEN icv.name = 'password' THEN icv.is_encrypted ELSE false END) as password_is_encrypted,
			MAX(CASE WHEN icv.name = 'auth_type' THEN icv.value END) as auth_type,
			MAX(CASE WHEN icv.name = 'projects' THEN icv.value END) as projects
		FROM integrations i
		JOIN integration_config_values icv ON i.id = icv.integration_id
		WHERE i.tenant_id IN (SELECT tenant FROM cloud_accounts WHERE id = $1)
		  AND i.status = 'enabled'
		  AND i.type IN ('github', 'gitlab')
		GROUP BY i.id, i.type
		ORDER BY CASE WHEN i.type = $2 THEN 0 ELSE 1 END
	`, accountId, preferredProvider)
	if err != nil {
		ctx.GetLogger().Error("unable to query integrations for git config", "error", err)
		return credentials, actualRepo, detectedProvider, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("code: unable to close rows", "error", err)
		}
	}()

	for rows.Next() {
		var provider string
		var username string
		var url string
		var password string
		var passwordIsEncrypted bool
		var authType *string
		var projects *string

		err := rows.Scan(&provider, &username, &url, &password, &passwordIsEncrypted, &authType, &projects)
		if err != nil {
			return credentials, actualRepo, detectedProvider, err
		}

		// Skip if required fields are missing
		if username == "" || url == "" || password == "" {
			ctx.GetLogger().Warn("skipping integration with missing credentials", "provider", provider)
			continue
		}

		// Parse projects JSON if present
		projectsMap := []map[string]string{}
		if projects != nil && *projects != "" {
			err = common.UnmarshalJson([]byte(*projects), &projectsMap)
			if err != nil {
				ctx.GetLogger().Warn("unable to parse projects JSON", "error", err, "provider", provider)
				// Continue with empty projects instead of skipping entire credential
			}
		}

		// Decrypt password if it's encrypted
		decryptedPassword := password
		if passwordIsEncrypted && password != "" {
			decryptedPassword, err = common.Decrypt(password)
			if err != nil {
				ctx.GetLogger().Error("error decrypting password", "error", err)
				return credentials, actualRepo, detectedProvider, common.ErrorInternal("error: unable to process request")
			}
		}

		// Default auth_type to "token" if not specified
		finalAuthType := "token"
		if authType != nil && *authType != "" {
			finalAuthType = *authType
		}

		credentials = append(credentials, GitCredentials{
			Username: username,
			Url:      url,
			Password: decryptedPassword,
			AuthType: finalAuthType,
			Projects: projectsMap,
			Provider: provider,
		})
	}

	// If repo is empty, collect all available project URLs from credentials
	if actualRepo == "" && len(credentials) > 0 {
		var allProjectURLs []string
		var firstProvider string
		for _, cred := range credentials {
			for _, project := range cred.Projects {
				if repoUrl := resolveProjectRepoURL(project, cred); repoUrl != "" {
					allProjectURLs = append(allProjectURLs, repoUrl)
					if firstProvider == "" {
						firstProvider = cred.Provider
					}
				}
			}
		}

		if len(allProjectURLs) == 1 {
			// Single repo — use it directly
			actualRepo = allProjectURLs[0]
			detectedProvider = firstProvider
			ctx.GetLogger().Info("code: using only available repository from credentials", "repo", actualRepo, "provider", detectedProvider)
		} else if len(allProjectURLs) > 1 {
			// Multiple repos — don't pick blindly, let the caller handle selection
			ctx.GetLogger().Info("code: multiple repositories found in credentials, caller should ask user", "count", len(allProjectURLs), "repos", allProjectURLs)
		}
	}

	// If provider still not detected, try to detect from actualRepo
	if detectedProvider == "" && actualRepo != "" {
		detectedProvider = detectGitProvider(actualRepo)
	}

	return credentials, actualRepo, detectedProvider, nil
}

// detectGitProvider determines the Git provider from a repository URL
// Supports both cloud-hosted and self-hosted instances
func detectGitProvider(repoURL string) string {
	if repoURL == "" {
		return "github" // Default for backward compatibility
	}

	lowerURL := strings.ToLower(repoURL)

	// Check for GitLab-specific patterns first (more specific)
	// 1. gitlab.com (cloud)
	// 2. GitLab-specific URL pattern "/-/" in path (e.g., /-/merge_requests/)
	if strings.Contains(lowerURL, "gitlab.com") ||
		strings.Contains(lowerURL, "/-/") {
		return "gitlab"
	}

	// Parse URL to check hostname for self-hosted GitLab
	if strings.HasPrefix(lowerURL, "http://") || strings.HasPrefix(lowerURL, "https://") {
		if parsed, err := url.Parse(lowerURL); err == nil {
			host := strings.ToLower(parsed.Host)
			if strings.HasPrefix(host, "gitlab.") || strings.Contains(host, "gitlab") {
				return "gitlab"
			}
		}
	}

	// Check for GitHub patterns
	if strings.Contains(lowerURL, "github.com") ||
		strings.Contains(lowerURL, "github:") {
		return "github"
	}

	// Default to GitHub for backward compatibility
	return "github"
}

// K8s info extraction methods copied from LogAnalysisAgent
func (l CodeAgent2) extractK8sInfo(ctx *security.RequestContext, accountId string, conversationId string, messageId string, agentId string, logData string, userId string) ([]map[string]string, error) {
	logger := ctx.GetLogger()
	logger.Debug("Extracting K8s info from log data", "data_length", len(logData))

	// Use the constant from log analysis agent
	const PROMPT_CHAIN_LOG_EXTRACT_K8S_INFO = `Extract all Kubernetes resource information from the provided log data. If multiple resources are mentioned, include them all.

Return the result as a valid JSON array with the following format:
[
  {
    "namespace": "<namespace>",
    "pod_name": "<pod_name>",
    "workload_name": "<workload_name>"
  }
]

- Include at least the "namespace" and "pod_name" fields if they can be confidently determined.
- Only include "workload_name" if it is clearly identifiable.
- Do not confuse the workload name with the workload type (e.g., Deployment, StatefulSet).
- If no valid Kubernetes resources are found, return an empty array.
- If resource identification is uncertain, omit the entry entirely.

DO NOT ASSUME THE K8S INFO IF NOT MENTIONED IN THE LOG.

Log data: %v`

	// Prepare the prompt for the LLM
	messageHistory := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf(PROMPT_CHAIN_LOG_EXTRACT_K8S_INFO, logData)),
	}

	// Generate completion with temperature 0 for more deterministic results
	completion, err := core.GenerateAndTrackLLMContent(ctx, userId, accountId, conversationId, messageId, agentId, false, messageHistory, true, llms.WithTemperature(0.0))
	if err != nil {
		return nil, fmt.Errorf("failed to extract k8s info from log data: %w", err)
	}

	llmResponse := completion.Choices[0].Content
	logger.Debug("Received LLM response for K8s info extraction", "response_length", len(llmResponse))

	// First try to extract JSON using a more robust approach
	jsonString := l.extractJSONFromText(llmResponse)
	if jsonString == "" {
		logger.Warn("No JSON array found in LLM response, returning empty result")
		return []map[string]string{}, nil
	}

	// Parse the JSON array
	var k8sInfoList []map[string]string
	err = common.UnmarshalJson([]byte(jsonString), &k8sInfoList)
	if err != nil {
		logger.Error("Failed to parse JSON from LLM response", "error", err, "json_string", jsonString)
		return nil, fmt.Errorf("failed to parse k8s info: %w", err)
	}

	return k8sInfoList, nil
}

// bareRepoPattern matches a response that is exactly "owner/repo" and nothing else.
// Anchored on purpose: a chain-of-thought reply mentioning a file path such as
// "deploy/kubernetes/values.yaml" must not be read as a repository reference.
var bareRepoPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// gitURLHostPath splits an https:// or scp-like git@host:path repository URL into its
// host and path. Returns empty strings when raw is shaped like neither, so callers can
// treat "unparseable" and "not a repository URL" identically.
func gitURLHostPath(raw string) (host, path string) {
	if rest, ok := strings.CutPrefix(raw, "git@"); ok {
		h, p, found := strings.Cut(rest, ":")
		if !found {
			return "", ""
		}
		return h, p
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", ""
	}
	return u.Hostname(), u.Path
}

// isKnownGitHost reports whether host is a repository host we recognise. Self-hosted
// GitLab is matched on hostname because the extraction prompt explicitly invites it.
// A leading "www." is ignored so the hosted providers match either way.
func isKnownGitHost(host string) bool {
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	switch host {
	case "github.com", "gitlab.com", "bitbucket.org":
		return true
	}
	return strings.Contains(host, "gitlab")
}

// extractGitURLFromText extracts a git repository URL from plain text input using regex.
// Handles URLs like https://github.com/owner/repo, git@github.com:owner/repo, etc.
// The host is matched on the parsed hostname rather than as a substring of the whole
// match, so "https://github.com.example.net/x" is not mistaken for GitHub.
func extractGitURLFromText(text string) string {
	pattern := regexp.MustCompile(`(?:https?://|git@)[^\s,;'"]+`)
	matches := pattern.FindAllString(text, -1)
	for _, match := range matches {
		// Strip trailing punctuation that's likely not part of the URL
		match = strings.TrimRight(match, `.,;:!?)\"`)
		if host, _ := gitURLHostPath(match); isKnownGitHost(host) {
			return match
		}
	}
	return ""
}

// isValidGitURL checks if a string is a well-formed git repository URL. It parses
// rather than prefix-matches: the previous HasPrefix check accepted anything that
// merely STARTED with a scheme, which included the "https://" that the repo extractor
// itself had just prepended to a chain-of-thought blob (#35703).
func isValidGitURL(s string) bool {
	if s == "" || s != strings.TrimSpace(s) {
		return false
	}
	if strings.IndexFunc(s, unicode.IsSpace) >= 0 || strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return false
	}
	host, path := gitURLHostPath(s)
	if host == "" {
		return false
	}
	segments := 0
	for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
		if segment != "" {
			segments++
		}
	}
	return segments >= 2
}

// normalizeExtractedRepo turns an LLM repo-extraction reply into a repository URL and
// provider, or ("", "") when the reply does not confidently name one. Pure function —
// kept separate from the LLM call so it can be unit tested without mocks, the same
// split as validateRepoSelection.
//
// Reasoning models answer this prompt with a numbered chain-of-thought that often ends
// in the correct answer ("6. Final output: Empty string."). Substring checks read that
// as a repository, so the entire blob became the URL and reached `git push` (#35703).
// Every branch below either yields an anchored match or yields nothing; there is
// deliberately no fallback that treats the raw reply as a URL.
func normalizeExtractedRepo(llmResponse string) (repo, provider string) {
	resp := strings.TrimSpace(llmResponse)
	resp = strings.Trim(resp, "`\"'")
	resp = strings.TrimSpace(resp)
	if resp == "" {
		return "", ""
	}
	// A reasoning reply restates the prompt while it thinks, and the prompt contains
	// example URLs. Searching the whole reply would happily return one of those
	// placeholders. Consider only the final non-empty line — the model's answer —
	// which for a single-line reply is the reply itself.
	resp = lastNonEmptyLine(resp)
	if resp == "" {
		return "", ""
	}
	switch strings.ToLower(resp) {
	case "none", "empty", "null", "nil", "uncertain":
		return "", ""
	}

	// A URL on the answer line, tolerating a "Final output: <url>" style prefix.
	// extractGitURLFromText is anchored and host-checked, and returns "" on no match.
	if repoURL := extractGitURLFromText(resp); repoURL != "" {
		if isPlaceholderRepoURL(repoURL) {
			return "", ""
		}
		return repoURL, detectGitProvider(repoURL)
	}

	// Bare "owner/repo", accepted only when it is the whole answer line.
	if bareRepoPattern.MatchString(resp) && !isPlaceholderRepoURL("https://github.com/"+resp) {
		return "https://github.com/" + resp, "github"
	}

	return "", ""
}

// lastNonEmptyLine returns the final line of s that carries content once surrounding
// whitespace and quoting are removed. Returns "" when s has no such line.
//
// Quotes are stripped before the second trim because a quoted answer keeps its own
// padding — `" owner/repo "` unquotes to " owner/repo ", which the anchored
// bareRepoPattern would then reject. A line that is nothing but quotes collapses to
// empty and the scan continues to the line above.
func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if trimmed := strings.TrimSpace(strings.Trim(line, "`\"'")); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// isPlaceholderRepoURL reports whether repoURL is one of the illustrative repositories
// named in PROMPT_EXTRACT_GIT_REPO. A model that echoes the instructions back would
// otherwise have us clone and push to a repository that does not exist. Keep this in
// step with the examples in that prompt.
func isPlaceholderRepoURL(repoURL string) bool {
	_, path := gitURLHostPath(repoURL)
	switch strings.ToLower(strings.Trim(path, "/")) {
	case "owner/repo", "group/project":
		return true
	}
	return false
}

// validateRepoSelection normalizes an LLM repo-selection response and returns
// the matching candidate URL, or "" if the response is empty, "UNCERTAIN", or
// not in the candidate set. Pure function — kept separate from the LLM call so
// it can be unit tested without mocks.
func validateRepoSelection(llmResponse string, candidates []string) string {
	resp := strings.TrimSpace(llmResponse)
	resp = strings.Trim(resp, "\"'`")
	if resp == "" {
		return ""
	}
	if strings.EqualFold(resp, "UNCERTAIN") || strings.EqualFold(resp, "NONE") {
		return ""
	}
	for _, c := range candidates {
		if c == resp {
			return c
		}
	}
	// Tolerate a trailing slash mismatch from the LLM.
	respTrim := strings.TrimRight(resp, "/")
	for _, c := range candidates {
		if strings.TrimRight(c, "/") == respTrim {
			return c
		}
	}
	return ""
}

// truncateForPrompt clips a string to maxLen runes (UTF-8 safe) with a marker.
func truncateForPrompt(s string, maxLen int) string {
	const marker = " [...]"
	markerRunes := len([]rune(marker))
	runes := []rune(s)
	if maxLen <= 0 || len(runes) <= maxLen {
		return s
	}
	if maxLen <= markerRunes {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-markerRunes]) + marker
}

// selectRepoFromConversationContext uses recent conversation messages plus the
// current query to pick a repository from the candidate set. It mirrors the
// pattern used by plannerExecutor.selectConfigUsingLLM for normal-agent
// configs: feed prior context + candidates to the LLM, validate the response
// against the candidate set, and return "" on uncertainty so callers can fall
// back to the existing followup.
//
// Returns the selected URL, or "" on any failure / uncertainty.
func (l CodeAgent2) selectRepoFromConversationContext(
	ctx *security.RequestContext,
	query core.NBAgentRequest,
	currentQuery string,
	candidates []string,
) string {
	logger := ctx.GetLogger()
	if len(candidates) <= 1 || query.ConversationId == "" {
		return ""
	}

	const (
		maxHistoryMessages   = 6
		maxPerMessageRunes   = 2000
		maxCurrentQueryRunes = 4000
	)

	chatHistory, err := core.GetConversationDao().LoadConversationMessages(
		query.AccountId, query.ConversationId, "", "", maxHistoryMessages+1,
	)
	if err != nil {
		logger.Warn("code: failed to load conversation history for repo selection", "error", err)
		return ""
	}
	// LoadConversationMessages returns DESC by created_at — reverse to chronological
	// and drop the current message so we don't bias the LLM with its own input.
	var history []map[string]string
	for i := len(chatHistory) - 1; i >= 0; i-- {
		m := chatHistory[i]
		if m["id"] == query.MessageId {
			continue
		}
		history = append(history, m)
	}
	if len(history) > maxHistoryMessages {
		history = history[len(history)-maxHistoryMessages:]
	}
	if len(history) == 0 {
		return ""
	}

	var historyBuilder strings.Builder
	for _, m := range history {
		role := m["role"]
		content := truncateForPrompt(m["content"], maxPerMessageRunes)
		response := truncateForPrompt(m["response"], maxPerMessageRunes)
		if content != "" {
			fmt.Fprintf(&historyBuilder, "- [%s] %s\n", role, content)
		}
		if response != "" {
			fmt.Fprintf(&historyBuilder, "  [ai-response] %s\n", response)
		}
	}

	var candidatesBuilder strings.Builder
	for i, c := range candidates {
		fmt.Fprintf(&candidatesBuilder, "%d. %s\n", i+1, c)
	}

	const promptTemplate = `You are selecting the most likely Git repository to analyze for a follow-up code-analysis request.

Candidate repositories (you MUST pick one of these verbatim, or reply UNCERTAIN):
%s
Recent conversation (oldest first):
%s
Current user request:
%s

Pick the single repository URL most clearly indicated by the conversation. Strong signals: a prior message names an "owner/repo" or full URL that matches a candidate, or references an issue/PR/commit in a candidate repository. If there is no clear signal, reply with the single word UNCERTAIN.

Reply with ONLY the chosen URL (exactly as listed above) or the single word UNCERTAIN. No explanation.`

	prompt := fmt.Sprintf(
		promptTemplate,
		candidatesBuilder.String(),
		historyBuilder.String(),
		truncateForPrompt(currentQuery, maxCurrentQueryRunes),
	)

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, prompt),
	}

	completion, err := core.GenerateAndTrackLLMContent(
		ctx, query.UserId, query.AccountId, query.ConversationId, query.MessageId,
		query.AgentId, false, messages, true, llms.WithTemperature(0.0),
	)
	if err != nil {
		logger.Warn("code: repo context selection LLM call failed", "error", err)
		return ""
	}
	if completion == nil || len(completion.Choices) == 0 {
		logger.Warn("code: repo context selection returned empty response")
		return ""
	}

	selected := validateRepoSelection(completion.Choices[0].Content, candidates)
	if selected == "" {
		logger.Info("code: repo context selection uncertain, will ask user",
			"raw_response", strings.TrimSpace(completion.Choices[0].Content),
			"candidates", len(candidates))
		return ""
	}
	logger.Info("code: repo context selection picked candidate from conversation",
		"repo", selected, "candidates", len(candidates), "history_messages", len(history))
	return selected
}

// extractGitRepoFromQuery attempts to extract Git repository URL (GitHub or GitLab) from user query using LLM
func (l CodeAgent2) extractGitRepoFromQuery(ctx *security.RequestContext, accountId string, conversationId string, messageId string, agentId string, query string, userId string) (string, string, error) {
	logger := ctx.GetLogger()
	logger.Debug("Extracting Git repo from user query", "query_length", len(query))

	const PROMPT_EXTRACT_GIT_REPO = `Extract the Git repository URL from the provided text. Look for:
- GitHub URLs (e.g., https://github.com/owner/repo)
- GitLab URLs (e.g., https://gitlab.com/group/project)
- Self-hosted GitLab instances (URLs containing "gitlab" in hostname, e.g., https://gitlab.company.com/group/project)
- Repository references in owner/repo or group/project format

Return ONLY the repository URL in one of these formats:
- "https://github.com/owner/repo" for GitHub
- "https://gitlab.com/group/project" for GitLab (or full URL for self-hosted instances)
- "owner/repo" if the provider is unclear (will default to GitHub)

If no Git repository is mentioned or can be confidently identified, return an empty string.

DO NOT make assumptions or guess repository names.

Text: %v`

	// Prepare the prompt for the LLM
	messageHistory := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf(PROMPT_EXTRACT_GIT_REPO, query)),
	}

	// Generate completion with temperature 0 for more deterministic results
	completion, err := core.GenerateAndTrackLLMContent(ctx, userId, accountId, conversationId, messageId, agentId, false, messageHistory, true, llms.WithTemperature(0.0))
	if err != nil {
		return "", "", fmt.Errorf("failed to extract git repo from query: %w", err)
	}

	llmResponse := strings.TrimSpace(completion.Choices[0].Content)
	logger.Debug("Received LLM response for Git repo extraction", "response", llmResponse)

	repoURL, provider := normalizeExtractedRepo(llmResponse)
	if repoURL == "" {
		logger.Debug("No repository confidently identified in LLM extraction response")
		return "", "", nil
	}

	return repoURL, provider, nil
}

// extractJSONFromText attempts to extract a JSON array from text using multiple methods
func (l CodeAgent2) extractJSONFromText(text string) string {
	// First try to find JSON array using brackets
	startIdx := strings.Index(text, "[")
	endIdx := strings.LastIndex(text, "]")

	if startIdx != -1 && endIdx != -1 && startIdx < endIdx {
		return strings.TrimSpace(text[startIdx : endIdx+1])
	}

	// If that fails, try with regex for more complex cases
	re := regexp.MustCompile(`\[\s*(?:\{[^{}]*\}(?:\s*,\s*\{[^{}]*\})*)\s*\]`)
	jsonString := strings.TrimSpace(re.FindString(text))

	return jsonString
}

func (l CodeAgent2) GetSourceCodeAnnotations(ctx *security.RequestContext, request core.NBAgentRequest, k8sInfo []map[string]string, eventId string) (map[string]string, error) {
	if len(k8sInfo) == 0 && eventId == "" {
		return nil, nil
	}

	ctx.GetLogger().Info("Getting source code annotations", "pod_count", len(k8sInfo), "eventId", eventId)

	// Get the database connection
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, err
	}

	// First try to get annotations by eventId if available
	if eventId != "" {
		ctx.GetLogger().Info("Attempting to get annotations using eventId", "eventId", eventId)

		workloadName := request.QueryConfig.Workload
		namespace := request.QueryConfig.Namespace

		if workloadName == "" {
			rows, err := dbManager.Db.Queryx("select subject_owner, subject_namespace from events where id = $1", eventId)
			if err != nil {
				ctx.GetLogger().Warn("failed to query event for workload extraction", "error", err)
			}
			defer func() {
				if err := rows.Close(); err != nil {
					ctx.GetLogger().Error("code: unable to close rows", "error", err)
				}
			}()
			for rows.Next() {
				err := rows.Scan(&workloadName, &namespace)
				if err != nil {
					ctx.GetLogger().Warn("failed to scan event row for workload extraction", "error", err)
				}
			}
		}

		annotations, err := services_server.GetSourceCodeAnnotations(ctx, dbManager, request.AccountId, services_server.SourceCodeAnnotationOptions{
			EventId:      eventId,
			WorkloadName: workloadName,
			Namespace:    namespace,
		})
		if err == nil && len(annotations) > 0 {
			ctx.GetLogger().Info("Successfully retrieved annotations using eventId", "count", len(annotations))
			return annotations, nil
		}
		ctx.GetLogger().Info("No annotations found using eventId, falling back to pod/workload names")
	}

	var k8sInfoObjects []map[string]string
	for _, info := range k8sInfo {
		obj := map[string]string{
			"pod_name":      info["pod_name"],
			"namespace":     info["namespace"],
			"workload_name": info["workload_name"],
		}
		k8sInfoObjects = append(k8sInfoObjects, obj)
	}
	for _, i := range k8sInfoObjects {
		annotations, err := services_server.GetSourceCodeAnnotations(ctx, dbManager, request.AccountId, services_server.SourceCodeAnnotationOptions{
			PodName:      i["pod_name"],
			WorkloadName: i["workload_name"],
			Namespace:    i["namespace"],
		})
		if err != nil {
			ctx.GetLogger().Info("Failed to get source code annotations", "error", err, "pod_name", i["pod_name"], "workload_name", i["workload_name"])
		}
		if len(annotations) > 0 && (annotations["workloads.nudgebee.com/git.repo"] != "" || annotations["workloads.nudgebee.com/git.hash"] != "" || annotations["ci.nudgebee.com/git.hash"] != "" || annotations["ci.nudgebee.com/git.repo"] != "") {
			return annotations, nil
		}
	}
	return nil, nil
}

// executeFollowup handles PR followup mode — calls workspace /analyze with followup fields
// to address CI failures and review comments on agent-created PRs.
func (l CodeAgent2) executeFollowup(ctx *security.RequestContext, query core.NBAgentRequest, request CodeAgent2Request) (core.NBAgentResponse, error) {
	logger := ctx.GetLogger()

	// Get git credentials for the repo
	creds, repoUrl, provider, err := l.getGitCredentials(ctx, request.GitRepo, query.AccountId)
	if err != nil {
		logger.Error("code followup: unable to get git creds", "error", err)
		return core.NBAgentResponse{}, err
	}
	if request.GitRepo == "" && repoUrl != "" {
		request.GitRepo = repoUrl
	}
	if len(creds) == 0 {
		return core.NBAgentResponse{}, errors.New("git credentials are required for PR followup")
	}

	gitToken := resolveGitToken(ctx, creds, request.GitRepo, provider)

	tenantId := ""
	if ctx.GetSecurityContext() != nil {
		tenantId = ctx.GetSecurityContext().GetTenantId()
	}
	if request.AccountId == "" {
		request.AccountId = query.AccountId
	}

	// Build workspace /analyze request with followup fields
	analyzeRequest := map[string]any{
		"cloud_account_id":   request.AccountId,
		"tenant":             tenantId,
		"workload_name":      "unknown",
		"workload_namespace": "unknown",
		"workload_kind":      "Deployment",
		"logs":               request.Query,
		"prompt":             request.Query,
		"git_repository": map[string]any{
			"url":      request.GitRepo,
			"branch":   request.PRBranch,
			"provider": provider,
		},
		// PR-lifecycle followup is fix-mode by definition: the cron only fires
		// to iterate on an existing NB-raised PR (CI failure or unresolved
		// review comment), never for exploration.
		"mode":            codeAgentModeFix,
		"raise_pr":        true,
		"conversation_id": query.SessionId,
		"message_id":      query.MessageId,
		// Followup-specific fields
		"followup":  true,
		"pr_url":    request.PRURL,
		"pr_branch": request.PRBranch,
	}

	if gitToken != "" {
		analyzeRequest["git_credentials"] = map[string]any{
			"type":  "token",
			"token": gitToken,
		}
	}

	// Forward the resolved LLM config, exactly as the main analysis path does.
	// Without this the workspace pod falls back to its global LLM_* secret env,
	// which is not guaranteed to name a provider code-analysis supports — every
	// followup then fails at client construction before doing any work. Degrade
	// gracefully: on any failure, or when no provider resolves at all, omit the
	// block. The API key is plaintext — never log it.
	if llmCfg, lerr := core.ResolveLLMConfigForForwarding(ctx, query.AccountId, AgentCodeAnalyzer, query.ConversationId); lerr != nil {
		logger.Warn("code followup: failed to resolve LLM config for forwarding; using pod fallback", "error", lerr)
	} else if llmCfg != nil {
		analyzeRequest["llm_config"] = forwardedLLMConfigToMap(llmCfg)
		logger.Info("code followup: forwarding resolved LLM config to workspace analysis", "provider", llmCfg.Provider, "model", llmCfg.Model, "has_api_key", llmCfg.ApiKey != "")
	}

	// Pre-flight: verify workspace pod is reachable
	healthWm := workspace.NewWorkspaceManagerWithTimeout(10 * time.Second)
	if _, healthErr := healthWm.CallAPI(ctx, query.AccountId, "GET", "/health", nil, nil); healthErr != nil {
		logger.Warn("code followup: workspace health check failed, attempting recovery", "error", healthErr)
		recoveryWm := workspace.NewWorkspaceManagerWithTimeout(60 * time.Second)
		if _, recoveryErr := recoveryWm.CallAPIOrLazyCreate(ctx, query.AccountId, "GET", "/health", nil, nil); recoveryErr != nil {
			return core.NBAgentResponse{}, fmt.Errorf("workspace pod not healthy after recovery attempt: %w", recoveryErr)
		}
		logger.Info("code followup: workspace pod recovered successfully")
	}

	wm := workspace.NewWorkspaceManagerWithTimeout(60 * time.Second)
	logger.Info("code followup: executing via workspace", "account_id", query.AccountId, "pr_url", request.PRURL)

	// POST /analyze — code-analysis returns 202 with analysis_id
	followupStart := time.Now()
	respBytes, err := wm.CallAPIOrLazyCreate(ctx, query.AccountId, "POST", "/analyze", nil, analyzeRequest)
	if err != nil {
		return core.NBAgentResponse{}, fmt.Errorf("workspace /analyze followup call failed: %w", err)
	}

	var asyncResp map[string]any
	if err := json.Unmarshal(respBytes, &asyncResp); err != nil {
		result := extractAgentResponseWithTokenUsage(respBytes)
		modelTier, taskType := core.TierAttributionForRecord(ctx)
		go recordCodeAnalysisTokenUsage(query, result.TokenUsage, time.Since(followupStart).Seconds(), modelTier, taskType)
		return core.NBAgentResponse{Response: []string{result.AgentResponse}}, nil
	}

	// Sync response (backward compat)
	if _, hasResult := asyncResp["agent_response"]; hasResult {
		result := extractAgentResponseWithTokenUsage(respBytes)
		modelTier, taskType := core.TierAttributionForRecord(ctx)
		go recordCodeAnalysisTokenUsage(query, result.TokenUsage, time.Since(followupStart).Seconds(), modelTier, taskType)
		return core.NBAgentResponse{Response: []string{result.AgentResponse}}, nil
	}

	if errMsg, _ := asyncResp["error"].(string); errMsg != "" {
		return core.NBAgentResponse{}, fmt.Errorf("workspace /analyze followup failed: %s", errMsg)
	}

	analysisID, _ := asyncResp["analysis_id"].(string)
	status, _ := asyncResp["status"].(string)
	if analysisID == "" || status != "running" {
		return core.NBAgentResponse{}, fmt.Errorf("unexpected workspace /analyze followup response: status=%q analysis_id=%q", status, analysisID)
	}

	// Poll /status/{id} every 5s until completed or failed
	logger.Info("code followup: analysis accepted, polling for progress", "analysis_id", analysisID)
	statusEndpoint := fmt.Sprintf("/status/%s", url.PathEscape(analysisID))
	pollWm := workspace.NewWorkspaceManagerWithTimeout(30 * time.Second)
	lastProgress := ""
	persistedStepStatus := map[string]string{}
	const maxConsecutiveErrors = 12
	const maxPollDuration = 30 * time.Minute
	consecutiveErrors := 0
	pollDeadline := time.Now().Add(maxPollDuration)

	for {
		select {
		case <-ctx.GetContext().Done():
			return core.NBAgentResponse{}, fmt.Errorf("followup analysis timed out while polling for results")
		case <-time.After(5 * time.Second):
		}

		if time.Now().After(pollDeadline) {
			return core.NBAgentResponse{}, fmt.Errorf("followup analysis polling exceeded maximum duration of %v", maxPollDuration)
		}

		statusBytes, err := pollWm.CallAPI(ctx, query.AccountId, "GET", statusEndpoint, nil, nil)
		if err != nil {
			consecutiveErrors++
			logger.Warn("code followup: failed to poll status", "error", err, "analysis_id", analysisID,
				"consecutive_errors", consecutiveErrors, "max_consecutive_errors", maxConsecutiveErrors)
			if consecutiveErrors >= maxConsecutiveErrors {
				return core.NBAgentResponse{}, fmt.Errorf("followup polling abandoned after %d consecutive errors: %w", consecutiveErrors, err)
			}
			continue
		}
		consecutiveErrors = 0

		var statusResp map[string]any
		if err := json.Unmarshal(statusBytes, &statusResp); err != nil {
			logger.Warn("code followup: failed to parse status response", "error", err)
			continue
		}

		// Update progress in DB if changed
		progress, _ := statusResp["progress"].(string)
		if progress != "" && progress != lastProgress {
			lastProgress = progress
			if query.MessageId != "" {
				core.GetConversationDao().UpdateConversationMessageAsync(
					query.MessageId, progress, core.ConversationStatusInProgress,
				)
			}
		}

		// Persist the steps taken so far as tool-call rows (live display).
		if invs, ok := statusResp["tool_invocations"].([]any); ok {
			persistCodeAnalysisSteps(ctx, query, invs, persistedStepStatus)
		}

		pollStatus, _ := statusResp["status"].(string)
		switch pollStatus {
		case "completed":
			result, ok := statusResp["result"]
			if !ok {
				return core.NBAgentResponse{}, fmt.Errorf("followup analysis completed but no result returned")
			}
			resultBytes, err := json.Marshal(result)
			if err != nil {
				return core.NBAgentResponse{}, fmt.Errorf("failed to marshal followup result: %w", err)
			}
			logger.Info("code followup: analysis completed", "analysis_id", analysisID)
			caResult := extractAgentResponseWithTokenUsage(resultBytes)
			modelTier, taskType := core.TierAttributionForRecord(ctx)
			go recordCodeAnalysisTokenUsage(query, caResult.TokenUsage, time.Since(followupStart).Seconds(), modelTier, taskType)
			return core.NBAgentResponse{Response: []string{caResult.AgentResponse}}, nil
		case "failed":
			errMsg, _ := statusResp["error"].(string)
			return core.NBAgentResponse{}, fmt.Errorf("followup analysis failed: %s", errMsg)
		}
		// status == "running" → keep polling
	}
}

func (l CodeAgent2) GetModelCategory() core.ModelTier {
	return core.ModelTierReasoning
}
