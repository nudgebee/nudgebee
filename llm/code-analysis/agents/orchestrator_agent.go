package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/config"
	"nudgebee/code-analysis-agent/internal/credentials"
	"nudgebee/code-analysis-agent/internal/git"
	"nudgebee/code-analysis-agent/internal/gitprovider"
	"nudgebee/code-analysis-agent/internal/session"
	"nudgebee/code-analysis-agent/llm"
	"nudgebee/code-analysis-agent/planners"
	"nudgebee/code-analysis-agent/tools"
	"nudgebee/code-analysis-agent/tools/core"

	"github.com/tmc/langchaingo/llms"
)

// AnalysisMode controls whether the agent is allowed to mutate code.
// ModeExplore: read-only Q&A / RCA. The CodeFixerAgent never runs and no PR is created.
// ModeFix: full fix-and-PR pipeline. Specialist proposes a fix, fixer applies it,
// and (if RaisePR is also true) the orchestrator opens a pull request.
const (
	ModeExplore = "explore"
	ModeFix     = "fix"
)

// NBAgentRequest maintains compatibility with the handler
type NBAgentRequest struct {
	Query                 string         `json:"query"`
	AccountId             string         `json:"account_id"`
	ConversationId        string         `json:"conversation_id"`
	AgentId               string         `json:"agent_id"`
	ParentAgentId         string         `json:"parent_agent_id"`
	MessageId             string         `json:"message_id"`
	UserId                string         `json:"user_id"`
	ConversationContext   string         `json:"conversation_context"`
	QueryContext          string         `json:"query_context"`
	QueryConfig           map[string]any `json:"query_config"`
	EnableQueryRefinement bool           `json:"enable_query_refinement"`
	Mode                  string         `json:"mode,omitempty"`
	RaisePR               bool           `json:"raise_pr,omitempty"`
	EventId               string         `json:"event_id,omitempty"`
	RecommendationId      string         `json:"recommendation_id,omitempty"`
	// Skills carries operator-authored skills mapped to the code agent, already
	// rendered as a <skills> block by llm-server. code-analysis has no DB of its
	// own, so skills are resolved upstream and forwarded here for prompt injection.
	Skills string `json:"skills,omitempty"`
}

// shouldFailIncompleteFixRequest decides whether the orchestrator must reject a
// specialist response whose analysis_incomplete marker signals the planner
// resorted to its forced-submit hardcoded fallback. Returning the result of
// requires_fix would be honoring a fabricated value; we fail loudly so the
// caller sees a real error instead of the relevance-validator's off-topic
// placeholder. Only fires when the caller explicitly asked for a PR (RaisePR)
// and ran in fix mode — explore-mode requests still receive the partial
// findings.
func shouldFailIncompleteFixRequest(analysisIncomplete bool, mode string, request NBAgentRequest) bool {
	return analysisIncomplete && mode == ModeFix && request.RaisePR
}

// EffectiveMode returns the mode the orchestrator should run in.
// Explicit Mode wins. Otherwise fall back to RaisePR for backward compatibility:
// callers that opted into fix-mode by setting raise_pr=true continue to behave
// the same. Everything else is explore.
func (r NBAgentRequest) EffectiveMode() string {
	switch r.Mode {
	case ModeExplore, ModeFix:
		return r.Mode
	}
	if r.RaisePR {
		return ModeFix
	}
	return ModeExplore
}

type OrchestratorAgent struct {
	name              string
	description       string
	llmClient         *llm.Client
	config            *config.Config
	gitClient         *git.GitClient
	workspaceDir      string
	currentWorkingDir string // Centrally managed working directory for all operations
	logger            *common.Logger
	toolTracker       *common.ToolInvocationTracker // Shared tracker for all agents

	// Progress reporting fields (set per-execution)
	progressAnalysisId string
	progressAccountId  string

	// Specialist agents
	routerAgent              *RouterAgent
	codeAgent                *CodeAgent
	errorRCAAgent            *ErrorRCAAgent
	performanceDebuggerAgent *PerformanceDebuggerAgent
	codeFixerAgent           *CodeFixerAgent
	codeReviewAgent          *CodeReviewAgent
}

func NewOrchestratorAgent(cfg *config.Config, llmClient *llm.Client, gitClient *git.GitClient, logger *common.Logger) *OrchestratorAgent {
	workspaceDir := cfg.Analysis.WorkspaceDir

	// Initialize specialist agents - all should work in the same workspace
	// Note: Security queries are routed to ErrorRCAAgent which has all necessary tools
	routerAgent := NewRouterAgent(llmClient, logger)
	codeAgent := NewCodeAgent(cfg, llmClient, gitClient, logger, workspaceDir)
	errorRCAAgent := NewErrorRCAAgent(cfg, llmClient, gitClient, logger, workspaceDir)
	performanceDebuggerAgent := NewPerformanceDebuggerAgent(cfg, llmClient, gitClient, logger, workspaceDir)
	codeFixerAgent := NewCodeFixerAgent(llmClient, logger, workspaceDir, cfg)
	codeReviewAgent := NewCodeReviewAgent(cfg, llmClient, logger, workspaceDir)

	return &OrchestratorAgent{
		name:                     "orchestrator_agent",
		description:              "Routes tasks to specialist agents for code analysis and fixing.",
		llmClient:                llmClient,
		config:                   cfg,
		gitClient:                gitClient,
		workspaceDir:             cfg.Analysis.WorkspaceDir,
		currentWorkingDir:        cfg.Analysis.WorkspaceDir, // Initialize with default workspace
		logger:                   logger,
		routerAgent:              routerAgent,
		codeAgent:                codeAgent,
		errorRCAAgent:            errorRCAAgent,
		performanceDebuggerAgent: performanceDebuggerAgent,
		codeFixerAgent:           codeFixerAgent,
		codeReviewAgent:          codeReviewAgent,
	}
}

func (a *OrchestratorAgent) GetName() string {
	return a.name
}

func (a *OrchestratorAgent) SetLogger(logger *common.Logger) {
	a.logger = logger
	// Propagate logger to specialist agents
	if a.routerAgent != nil {
		a.routerAgent.logger = logger
	}
	if a.codeAgent != nil {
		a.codeAgent.SetLogger(logger)
	}
	if a.errorRCAAgent != nil {
		a.errorRCAAgent.SetLogger(logger)
	}
	if a.performanceDebuggerAgent != nil {
		a.performanceDebuggerAgent.SetLogger(logger)
	}
	if a.codeFixerAgent != nil {
		a.codeFixerAgent.logger = logger
	}
}

func (a *OrchestratorAgent) SetWorkspaceDir(workspaceDir string) {
	// If workspaceDir is empty (log-only analysis), keep using config default
	// but don't perform any file operations
	if workspaceDir == "" {
		a.workspaceDir = a.config.Analysis.WorkspaceDir
		a.logger.Log(common.EventAnalysisStart, "Using log-only analysis mode - file operations disabled", nil)
	} else {
		a.workspaceDir = workspaceDir
		// For local repos (no cloning), currentWorkingDir must also point to the repo.
		// For remote repos, updateWorkingDirectoryFromToolInvocations() will override this
		// after repo_clone sets the actual cloned path.
		a.currentWorkingDir = workspaceDir
	}

	// Recreate specialist agents with the new workspace directory
	a.codeAgent = NewCodeAgent(a.config, a.llmClient, a.gitClient, a.logger, a.workspaceDir)
	a.errorRCAAgent = NewErrorRCAAgent(a.config, a.llmClient, a.gitClient, a.logger, a.workspaceDir)
	a.performanceDebuggerAgent = NewPerformanceDebuggerAgent(a.config, a.llmClient, a.gitClient, a.logger, a.workspaceDir)
	a.codeFixerAgent = NewCodeFixerAgent(a.llmClient, a.logger, a.workspaceDir, a.config)
}

func (a *OrchestratorAgent) GetNameAliases() []string {
	return []string{"orchestrator_agent"}
}

func (a *OrchestratorAgent) GetDescription() string {
	return a.description
}

func (a *OrchestratorAgent) Execute(ctx context.Context, request NBAgentRequest) (string, error) {
	// Store progress fields for use in sub-methods
	a.progressAnalysisId = request.ConversationId
	a.progressAccountId = request.AccountId

	// 1. Create SessionContext
	sessionCtx, err := a.createSessionContext(request)
	if err != nil {
		return "", fmt.Errorf("failed to create session context: %w", err)
	}

	// Thread the request mode into the planner context so submit_analysis can
	// validate the explore-mode contract (answer + citations) at the tool
	// level. The ReAct planner naturally retries on tool errors, giving the
	// LLM a chance to correct a malformed submission.
	ctx = tools.WithMode(ctx, request.EffectiveMode())

	// 2. Call RouterAgent
	routeName, err := a.routerAgent.Execute(ctx, sessionCtx)
	if err != nil {
		// Default to code_agent if router fails
		routeName = "code_agent"
		a.logger.Log(common.EventStepFailure, "Router agent failed, defaulting to code_agent", map[string]any{"error": err})
	}

	// 3. Call specialist agent with retry for transient LLM failures
	// Note: Security queries are routed to error_rca which has all necessary tools including repo_clone
	var specialistResult string
	const maxSpecialistRetries = 1

	for attempt := 0; attempt <= maxSpecialistRetries; attempt++ {
		switch routeName {
		case "code_agent":
			if attempt == 0 {
				a.reportProgress("Running code analysis...")
			}
			specialistResult, err = a.codeAgent.Execute(ctx, sessionCtx)
		case "error_rca":
			if attempt == 0 {
				a.reportProgress("Running error root cause analysis...")
			}
			specialistResult, err = a.errorRCAAgent.Execute(ctx, sessionCtx)
		case "performance_debugger":
			if attempt == 0 {
				a.reportProgress("Running performance analysis...")
			}
			specialistResult, err = a.performanceDebuggerAgent.Execute(ctx, sessionCtx)
		default:
			if attempt == 0 {
				a.reportProgress("Running code analysis...")
			}
			a.logger.Log(common.EventStepFailure, fmt.Sprintf("Specialist agent '%s' not recognized, using code_agent as fallback.", routeName), nil)
			specialistResult, err = a.codeAgent.Execute(ctx, sessionCtx)
		}

		if err == nil {
			break
		}

		if attempt < maxSpecialistRetries && isTransientLLMError(err) {
			a.reportProgress(fmt.Sprintf("Retrying analysis (attempt %d)...", attempt+2))
			a.logger.Log(common.EventStepFailure, "Specialist agent failed with transient error, retrying", map[string]any{
				"attempt": attempt + 1,
				"error":   err.Error(),
				"agent":   routeName,
			})
			// No client-state reset needed: the next specialist Execute call
			// constructs a fresh ReActPlanner with its own per-Plan GenAISession.
			continue
		}

		break
	}

	if err != nil {
		return "", fmt.Errorf("specialist agent execution failed: %w", err)
	}

	// CRITICAL: Update working directory after specialist completes (e.g., after repo_clone)
	// This ensures CodeFixer uses the same working directory as the specialist agent
	a.updateWorkingDirectoryFromToolInvocations()

	// Parse specialist result with robust JSON extraction
	factsData, err := common.ExtractJSONFromLLMResponse(specialistResult, a.logger)
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to parse specialist result as JSON", map[string]any{"error": err, "result_preview": specialistResult[:min(200, len(specialistResult))]})
		return specialistResult, nil // Return raw result if parsing fails
	}

	// CRITICAL: Extract working directory from specialist result (ErrorRCA, code_agent, etc.)
	// This is a fallback if updateWorkingDirectoryFromToolInvocations() didn't find it
	if a.currentWorkingDir == "" {
		if workingDir, ok := factsData["working_directory"].(string); ok && workingDir != "" {
			a.logger.Log(common.EventStepComplete, "Updating orchestrator working directory from specialist factsData", map[string]any{
				"working_directory": workingDir,
				"specialist":        routeName,
			})
			a.SetWorkingDirectory(workingDir)
		}
	}

	// Report progress with actual analysis title
	if title, ok := factsData["title"].(string); ok && title != "" {
		a.reportProgress(fmt.Sprintf("Identified: %s", truncateProgress(title, 120)))
	} else {
		a.reportProgress("Analysis complete. Evaluating results...")
	}

	// 4. Decide what to do with the specialist result based on the request mode.
	//
	// In explore mode the orchestrator is read-only by contract — the specialist
	// answers the question, the fixer never runs, and the response is sanitized
	// of any fix-shaped fields the LLM may have populated opportunistically.
	//
	// In fix mode the specialist's `requires_fix` is the source of truth: a fix
	// runs only when it is true. The previous override that forced
	// `requires_fix=true` whenever RaisePR was set has been removed — it caused
	// the agent to invent fixes for read-only queries.
	mode := request.EffectiveMode()
	requiresFix, _ := factsData["requires_fix"].(bool)
	implementationInstructions := factsData["implementation_instructions"]
	analysisIncomplete, _ := factsData["analysis_incomplete"].(bool)
	incompleteReason, _ := factsData["incomplete_reason"].(string)

	gitDiffStr, _ := factsData["git_diff"].(string)
	a.logger.Log(common.EventStepComplete, "Specialist result parsed", map[string]any{
		"mode":                            mode,
		"requires_fix":                    requiresFix,
		"has_requires_fix_field":          factsData["requires_fix"] != nil,
		"has_implementation_instructions": implementationInstructions != nil,
		"raise_pr_requested":              request.RaisePR,
		"has_git_diff":                    gitDiffStr != "",
		"has_fixed_code":                  factsData["fixed_code"] != nil,
		"analysis_incomplete":             analysisIncomplete,
	})

	// Planner failure: forced-submit fallback fired because generateLLMSummary
	// errored, so requires_fix in factsData is a placeholder, not a real
	// determination. For fix-mode requests where the caller explicitly asked
	// for a PR, honoring the placeholder silently skips the fixer and surfaces
	// a misleading "no fix needed" result. Fail loudly instead so llm-server
	// (and the user) sees an actionable error rather than the off-topic
	// placeholder substituted later by the relevance-validator.
	if shouldFailIncompleteFixRequest(analysisIncomplete, mode, request) {
		a.logger.Log(common.EventStepFailure, "Specialist analysis incomplete - failing fix-mode request", map[string]any{
			"raise_pr_requested": request.RaisePR,
			"incomplete_reason":  incompleteReason,
		})
		return "", fmt.Errorf("specialist analysis incomplete: planner exhausted iterations before producing a structured fix (%s); re-run with a more focused query or higher react_max_iterations", incompleteReason)
	}

	if mode == ModeExplore {
		a.reportProgress("Analysis complete.")
		a.logger.Log(common.EventStepComplete, "Explore mode - skipping fixer and PR creation", map[string]any{
			"specialist_requires_fix": requiresFix,
			"raise_pr_requested":      request.RaisePR,
		})
		return sanitizeExploreResponse(specialistResult), nil
	}

	if !requiresFix {
		a.reportProgress("Analysis complete — no code fix required.")
		a.logger.Log(common.EventStepComplete, "Skipping fixer agent and PR creation - requires_fix is false", map[string]any{
			"reason":             "Specialist agent did not indicate fix is required",
			"raise_pr_requested": request.RaisePR,
			"returning":          "specialist_result_directly",
		})

		// Add PR creation status if RaisePR was requested
		if request.RaisePR {
			var resultData map[string]any
			if err := json.Unmarshal([]byte(specialistResult), &resultData); err == nil {
				resultData["pr_creation_status"] = "skipped"
				resultData["pr_creation_reason"] = "requires_fix=false - specialist determined no fix needed"
				resultData["mode"] = mode
				if modifiedJSON, err := json.Marshal(resultData); err == nil {
					return string(modifiedJSON), nil
				}
			}
		}

		return withMode(specialistResult, mode), nil
	}

	// Verify the file actually exists in the cloned repo before running CodeFixer
	// Use currentWorkingDir (updated after repo_clone) which points to the actual repo,
	// not workspaceDir which is the parent temp directory.
	//
	// EXCEPTION: when implementation_instructions contains an action="write"
	// entry, the whole point is to CREATE a file that doesn't yet exist. The
	// historical existence-check was a hallucination guard for the
	// edit-only era; with write_file in CodeFixer's toolset, treating
	// "file doesn't exist" as a fatal precondition would block every
	// legitimate scaffolding instruction. Skip the gate in that case and
	// let the fixer's tool-level error handling deal with bad paths
	// (write_file rejects absolute paths and workspace escapes).
	workDir := a.currentWorkingDir
	if workDir == "" {
		workDir = a.workspaceDir
	}
	hasWriteInstruction := instructionsRequireWrite(factsData)
	if !hasWriteInstruction {
		if filePath, ok := factsData["file_path"].(string); ok && filePath != "" && workDir != "" {
			fullPath := filepath.Join(workDir, filePath)
			if _, err := os.Stat(fullPath); os.IsNotExist(err) {
				a.logger.Log(common.EventStepComplete, "Skipping CodeFixer - file_path does not exist in repo", map[string]any{
					"file_path":     filePath,
					"workspace_dir": workDir,
				})

				// Add PR creation status if RaisePR was requested
				if request.RaisePR {
					var resultData map[string]any
					if err := json.Unmarshal([]byte(specialistResult), &resultData); err == nil {
						resultData["pr_creation_status"] = "skipped"
						resultData["pr_creation_reason"] = fmt.Sprintf("file_path does not exist in repository: %s", filePath)
						resultData["mode"] = mode
						if modifiedJSON, err := json.Marshal(resultData); err == nil {
							return string(modifiedJSON), nil
						}
					}
				}

				return withMode(specialistResult, mode), nil
			}
		}
	} else {
		a.logger.Log(common.EventStepComplete, "Bypassing file-exists precondition: implementation_instructions contain action=write", nil)
	}

	a.logger.Log(common.EventStepComplete, "Starting iterative fix-review process", map[string]any{"max_attempts": 3})

	// Pass RCA file observations to CodeFixer so it doesn't re-investigate the same files
	if a.toolTracker != nil {
		rcaObservations := a.extractRCAFileObservations()
		if rcaObservations != "" {
			factsData["_rca_file_observations"] = rcaObservations
			sessionCtx.AddToScratchpad("RCA-FileObservations", rcaObservations)
			a.logger.Log(common.EventStepComplete, "Passed RCA file observations to CodeFixer", map[string]any{
				"observation_length": len(rcaObservations),
			})
		}
	}

	if filePath, ok := factsData["file_path"].(string); ok && filePath != "" {
		a.reportProgress(fmt.Sprintf("Applying fix to %s...", filePath))
	} else {
		a.reportProgress("Applying code fix...")
	}
	fixerResult, err := a.executeFixAndReviewLoop(ctx, sessionCtx, factsData)
	if err != nil {
		factsData["description"] = fmt.Sprintf("%s\n\nNote: Fix-review process failed: %v", factsData["description"], err)
		factsData["mode"] = mode
		fallbackJSON, _ := json.Marshal(factsData)
		return string(fallbackJSON), nil
	}

	// 5. Merge results - ensure git_diff is always preserved
	mergedData := a.mergeAgentResults(factsData, fixerResult)

	// 6. Handle PR creation if requested and changes were approved
	if request.RaisePR {
		shouldCreate, reason := a.shouldCreatePR(mergedData)
		if shouldCreate {
			a.reportProgress("Creating pull request...")
			prInfo, err := a.createPullRequest(ctx, sessionCtx, mergedData)
			if err != nil {
				a.logger.Log(common.EventStepFailure, "Failed to create pull request", map[string]any{"error": err.Error()})
				mergedData["pr_creation_status"] = "failed"
				mergedData["pr_creation_reason"] = err.Error()
			} else {
				mergedData["fix_pr"] = prInfo
				mergedData["pr_creation_status"] = "success"
				if prURL, ok := prInfo["url"].(string); ok && prURL != "" {
					a.reportProgress(fmt.Sprintf("Pull request created: %s", prURL))
				} else {
					a.reportProgress("Pull request created successfully.")
				}
				a.logger.Log(common.EventStepComplete, "Successfully created pull request", map[string]any{"pr_url": prInfo["url"]})
			}
		} else {
			// PR creation was skipped - add reason to response
			a.logger.Log(common.EventStepComplete, "PR creation skipped", map[string]any{"reason": reason})
			mergedData["pr_creation_status"] = "skipped"
			mergedData["pr_creation_reason"] = reason
		}
	}

	// Log the final result to verify git_diff is present
	a.logger.Log(common.EventStepComplete, "Final merged result", map[string]any{
		"has_git_diff":       mergedData["git_diff"] != nil,
		"has_fix_pr":         mergedData["fix_pr"] != nil,
		"raise_pr_requested": request.RaisePR,
	})

	// Transform mergedData to match SubmitAnalysisInput structure
	finalResponse := a.buildFinalResponse(mergedData)
	finalResponse["mode"] = ModeFix

	mergedJSON, _ := json.Marshal(finalResponse)

	return string(mergedJSON), nil
}

// fixOnlyResponseFields lists keys that only make sense when the orchestrator
// has actually run the fixer / opened a PR. They are stripped from explore-mode
// responses so a read-only Q&A never leaks a stray diff or PR-shaped payload
// just because the LLM emitted one in submit_analysis.
var fixOnlyResponseFields = []string{
	"requires_fix",
	"fixed_code",
	"git_diff",
	"implementation_instructions",
	"alternative_fixes",
	"execution_status",
	"execution_summary",
	"files_modified",
	"verification_passed",
	"verification_details",
	"pr_info",
	"automated_fix_pr_info",
	"fix_pr",
	"pr_creation_status",
	"pr_creation_reason",
}

// sanitizeExploreResponse drops fix-only fields from a specialist's
// submit_analysis JSON. Unparseable input is returned untouched — the response
// already missed any sanitization the orchestrator could meaningfully do.
func sanitizeExploreResponse(specialistResult string) string {
	var data map[string]any
	if err := json.Unmarshal([]byte(specialistResult), &data); err != nil {
		return specialistResult
	}
	for _, key := range fixOnlyResponseFields {
		delete(data, key)
	}
	data["mode"] = ModeExplore
	out, err := json.Marshal(data)
	if err != nil {
		return specialistResult
	}
	return string(out)
}

// withMode parses a JSON object string and stamps the given mode onto it so
// downstream consumers see a consistent `mode` field on every orchestrator
// return path. Unparseable input passes through unchanged.
func withMode(jsonStr, mode string) string {
	var data map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return jsonStr
	}
	data["mode"] = mode
	out, err := json.Marshal(data)
	if err != nil {
		return jsonStr
	}
	return string(out)
}

// buildFinalResponse transforms mergedData to match SubmitAnalysisInput structure
func (a *OrchestratorAgent) buildFinalResponse(mergedData map[string]any) map[string]any {
	response := make(map[string]any)

	// Map required fields with proper defaults
	if title, ok := mergedData["title"].(string); ok {
		response["title"] = title
	}
	if description, ok := mergedData["description"].(string); ok {
		response["description"] = description
	}
	if filePath, ok := mergedData["file_path"].(string); ok && filePath != "" {
		response["file_path"] = filePath
	}
	if lineNumber, ok := mergedData["line_number"]; ok {
		response["line_number"] = lineNumber
	}
	if errorMessage, ok := mergedData["error_message"].(string); ok && errorMessage != "" {
		response["error_message"] = errorMessage
	}
	if originalCode, ok := mergedData["original_code"].(string); ok && originalCode != "" {
		response["original_code"] = originalCode
	}
	if fixedCode, ok := mergedData["fixed_code"].(string); ok && fixedCode != "" {
		response["fixed_code"] = fixedCode
	}
	if gitDiff, ok := mergedData["git_diff"].(string); ok && gitDiff != "" {
		response["git_diff"] = gitDiff
	}
	if commits, ok := mergedData["commits"]; ok {
		response["commits"] = commits
	}
	if prList, ok := mergedData["pr_list"]; ok {
		response["pr_list"] = prList
	}

	// Map PR info from fix_pr field to automated_fix_pr_info
	if fixPR, ok := mergedData["fix_pr"].(map[string]any); ok {
		response["automated_fix_pr_info"] = fixPR
	}

	if requiresFix, ok := mergedData["requires_fix"].(bool); ok {
		response["requires_fix"] = requiresFix
	}

	// Map CodeFixer execution fields
	if execStatus, ok := mergedData["execution_status"].(string); ok && execStatus != "" {
		response["execution_status"] = execStatus
	}
	if execSummary, ok := mergedData["execution_summary"].(string); ok && execSummary != "" {
		response["execution_summary"] = execSummary
	}
	if filesModified, ok := mergedData["files_modified"]; ok {
		response["files_modified"] = filesModified
	}

	// Include PR creation status and reason
	if prStatus, ok := mergedData["pr_creation_status"].(string); ok && prStatus != "" {
		response["pr_creation_status"] = prStatus
	}
	if prReason, ok := mergedData["pr_creation_reason"].(string); ok && prReason != "" {
		response["pr_creation_reason"] = prReason
	}

	// Include RCA analysis details
	if rca, ok := mergedData["root_cause_analysis"].(string); ok && rca != "" {
		response["root_cause_analysis"] = rca
	}
	if confidence, ok := mergedData["confidence_score"].(string); ok && confidence != "" {
		response["confidence_score"] = confidence
	}

	// Include review details — critical for understanding why a fix was rejected
	if reviewData, ok := mergedData["review"].(map[string]any); ok {
		response["review"] = reviewData
	}

	// Include build verification results — shows lint/build pass/fail with command output
	if buildVerification, ok := mergedData["build_verification"].(map[string]any); ok {
		response["build_verification"] = buildVerification
	}

	// Include verification_passed from CodeFixer's submit_analysis
	if vp, ok := mergedData["verification_passed"]; ok {
		response["verification_passed"] = vp
	}

	// Build a human-readable failure_summary when the pipeline didn't produce a clean PR
	response["failure_summary"] = a.buildFailureSummary(response)

	return response
}

// buildFailureSummary produces a concise human-readable string explaining what went wrong
// when the pipeline didn't produce a clean PR. Returns empty string when everything succeeded.
func (a *OrchestratorAgent) buildFailureSummary(response map[string]any) string {
	var reasons []string

	// Check PR creation outcome
	if prStatus, _ := response["pr_creation_status"].(string); prStatus != "" && prStatus != "success" {
		if prReason, _ := response["pr_creation_reason"].(string); prReason != "" {
			reasons = append(reasons, fmt.Sprintf("PR not created: %s", prReason))
		} else {
			reasons = append(reasons, fmt.Sprintf("PR status: %s", prStatus))
		}
	}

	// Check review rejection
	if reviewData, ok := response["review"].(map[string]any); ok {
		if approved, ok := reviewData["approved"].(bool); ok && !approved {
			feedback, _ := reviewData["feedback"].(string)
			if feedback != "" {
				reasons = append(reasons, fmt.Sprintf("Review rejected: %s", feedback))
			}
			if issues, ok := reviewData["issues"].([]string); ok && len(issues) > 0 {
				reasons = append(reasons, fmt.Sprintf("Issues found: %s", strings.Join(issues, "; ")))
			}
			if syntaxErrs, ok := reviewData["syntax_errors"].([]string); ok && len(syntaxErrs) > 0 {
				reasons = append(reasons, fmt.Sprintf("Syntax errors: %s", strings.Join(syntaxErrs, "; ")))
			}
			if fmtIssues, ok := reviewData["formatting_issues"].([]string); ok && len(fmtIssues) > 0 {
				reasons = append(reasons, fmt.Sprintf("Formatting issues: %s", strings.Join(fmtIssues, "; ")))
			}
		}
	}

	// Check build verification failures
	if buildData, ok := response["build_verification"].(map[string]any); ok {
		if passed, ok := buildData["overall_passed"].(bool); ok && !passed {
			// Extract failed steps — handle both []map[string]any and []any (from JSON round-trip)
			var failedSteps []map[string]any
			if steps, ok := buildData["steps"].([]map[string]any); ok {
				failedSteps = steps
			} else if stepsAny, ok := buildData["steps"].([]any); ok {
				for _, s := range stepsAny {
					if sm, ok := s.(map[string]any); ok {
						failedSteps = append(failedSteps, sm)
					}
				}
			}
			for _, step := range failedSteps {
				isFailed := false
				if status, ok := step["status"].(string); ok && status == "failed" {
					isFailed = true
				} else if passed, ok := step["passed"].(bool); ok && !passed {
					isFailed = true
				}
				if isFailed {
					cmd, _ := step["command"].(string)
					errMsg, _ := step["error"].(string)
					output, _ := step["output"].(string)
					detail := errMsg
					if detail == "" {
						detail = output
					}
					if len(detail) > 200 {
						detail = detail[:200] + "..."
					}
					reasons = append(reasons, fmt.Sprintf("Build failed [%s]: %s", cmd, detail))
				}
			}
		}
	}

	// Check execution status
	if execStatus, _ := response["execution_status"].(string); execStatus == "failed" || execStatus == "partial_success" {
		if execSummary, _ := response["execution_summary"].(string); execSummary != "" {
			reasons = append(reasons, fmt.Sprintf("Fix execution: %s — %s", execStatus, execSummary))
		}
	}

	// No git diff means no changes were made
	if _, hasDiff := response["git_diff"].(string); !hasDiff {
		if requiresFix, ok := response["requires_fix"].(bool); ok && requiresFix {
			reasons = append(reasons, "No code changes were produced despite fix being required")
		}
	}

	if len(reasons) == 0 {
		return ""
	}
	return strings.Join(reasons, " | ")
}

func (a *OrchestratorAgent) reportProgress(text string) {
	common.SetProgress(a.progressAnalysisId, text)
}

func truncateProgress(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func (a *OrchestratorAgent) createSessionContext(request NBAgentRequest) (*session.SessionContext, error) {
	repoCtx := &planners.RepositoryContext{}

	if request.QueryConfig != nil {
		if repoURL, ok := request.QueryConfig["repository_url"].(string); ok {
			repoCtx.URL = repoURL

			// Detect git provider from URL or use explicit override
			provider := gitprovider.DetectProvider(repoURL)
			if providerOverride, ok := request.QueryConfig["git_provider"].(string); ok && providerOverride != "" {
				provider = gitprovider.ParseProvider(providerOverride)
			}
			repoCtx.GitProvider = string(provider)

			// Extract repo identifier based on provider
			if provider == gitprovider.GitProviderGitLab {
				repoCtx.GitLabProject = a.extractGitLabProject(repoURL)
			} else {
				repoCtx.GitHubRepo = a.extractGitHubRepo(repoURL)
			}
		}
		if branch, ok := request.QueryConfig["branch"].(string); ok {
			repoCtx.Branch = branch
		}
		if repoPath, ok := request.QueryConfig["repository_path"].(string); ok {
			// Only set LocalPath if it's a real path, not "agent-managed"
			if repoPath != "agent-managed" {
				repoCtx.LocalPath = repoPath
			}
		}
	}

	var initialLogs string
	var sessionCredentials *credentials.ResolvedCredentials
	if request.QueryConfig != nil {
		if logs, ok := request.QueryConfig["logs"].(string); ok {
			initialLogs = logs
		}
		// Extract credentials from QueryConfig for secure tool access
		if creds, ok := request.QueryConfig["credentials"].(*credentials.ResolvedCredentials); ok {
			sessionCredentials = creds
		}
	}

	// Detect monorepo structure
	repoCtx.DetectMonorepoStructure()

	// Strip quotes from EventID if present (sometimes passed with extra quotes from CLI)
	eventID := strings.Trim(request.EventId, `"'`)

	// Extract recommendation metadata from QueryConfig (similar to EventID)
	var recommendationID, accountID string
	if request.QueryConfig != nil {
		if recID, ok := request.QueryConfig["recommendation_id"].(string); ok {
			recommendationID = strings.Trim(recID, `"'`)
		}
		if accID, ok := request.QueryConfig["account_id"].(string); ok {
			accountID = strings.Trim(accID, `"'`)
		}
	}

	a.logger.Log(common.EventStepStart, "Creating SessionContext with EventID and RecommendationID", map[string]any{
		"raw_event_id":       request.EventId,
		"cleaned_event_id":   eventID,
		"has_event_id":       eventID != "",
		"recommendation_id":  recommendationID,
		"has_recommendation": recommendationID != "",
		"account_id":         accountID,
	})

	// Extract build configuration if provided
	var buildConfig *session.BuildConfig
	if request.QueryConfig != nil {
		if bc, ok := request.QueryConfig["build_config"]; ok {
			// Handle both typed and untyped (from JSON) build config
			switch v := bc.(type) {
			case *session.BuildConfig:
				buildConfig = v
			case map[string]any:
				buildConfig = &session.BuildConfig{}
				if s, ok := v["setup_command"].(string); ok {
					buildConfig.SetupCommand = s
				}
				if s, ok := v["lint_command"].(string); ok {
					buildConfig.LintCommand = s
				}
				if s, ok := v["build_command"].(string); ok {
					buildConfig.BuildCommand = s
				}
				if s, ok := v["test_command"].(string); ok {
					buildConfig.TestCommand = s
				}
			}
		}
	}

	return &session.SessionContext{
		AnalysisID:       request.ConversationId, // Or generate a new one
		OriginalQuery:    request.Query,
		InitialLogs:      initialLogs,
		RepoContext:      repoCtx,
		Credentials:      sessionCredentials, // Pass secure credentials to tools
		EventID:          eventID,
		RecommendationID: recommendationID,
		AccountID:        accountID,
		BuildConfig:      buildConfig,
		Mode:             request.EffectiveMode(),
		SkillsContext:    request.Skills,
	}, nil
}

// extractRCAFileObservations extracts file_view observations from the RCA tool tracker
// so CodeFixer doesn't need to re-read the same files the RCA already analyzed.
func (a *OrchestratorAgent) extractRCAFileObservations() string {
	if a.toolTracker == nil {
		return ""
	}

	invocations := a.toolTracker.GetInvocations()
	var observations strings.Builder
	observations.WriteString("=== FILES ALREADY EXAMINED BY RCA AGENT ===\n")
	seenFiles := make(map[string]bool)
	observationCount := 0

	for _, inv := range invocations {
		if inv.ToolName != "file_view" || inv.Status != "success" {
			continue
		}

		filePath, _ := inv.Input["file_path"].(string)
		if filePath == "" || seenFiles[filePath] {
			continue
		}
		seenFiles[filePath] = true

		fmt.Fprintf(&observations, "\n--- %s ---\n", filePath)
		if outputStr, ok := inv.Output.(string); ok {
			// Truncate to 2000 chars per file to keep context manageable
			if len(outputStr) > 2000 {
				outputStr = outputStr[:2000] + "\n[... truncated ...]"
			}
			observations.WriteString(outputStr)
			observations.WriteString("\n")
			observationCount++
		}
	}

	if observationCount == 0 {
		return ""
	}

	fmt.Fprintf(&observations, "\n=== %d files already examined - use this context instead of re-reading ===\n", observationCount)
	return observations.String()
}

// extractBuildVerificationResults scans tool tracker for CLI invocations that look like
// build/lint/test commands and extracts their pass/fail results for the reviewer.
// sinceStep filters to only include invocations after the given step number (0 = all).
func (a *OrchestratorAgent) extractBuildVerificationResults(sinceStep int) map[string]any {
	if a.toolTracker == nil {
		return nil
	}

	// Build/lint/test command patterns to look for
	buildPatterns := []string{
		"make lint", "make build", "make validate", "make check", "make test", "make fmt",
		"go build", "go vet", "go test", "golangci-lint",
		"npm run lint", "npm run build", "npm test", "npm run test",
		"poetry run black", "poetry run flake8", "poetry run mypy", "poetry run pytest",
		"pip install", "flake8", "black --check", "mypy",
		"yarn lint", "yarn build", "yarn test",
		"pnpm lint", "pnpm build", "pnpm test",
	}

	invocations := a.toolTracker.GetInvocationsSince(sinceStep)
	var buildSteps []map[string]any
	overallPassed := true

	for _, inv := range invocations {
		if inv.ToolName != "cli" {
			continue
		}

		// Check if the command matches a build/lint/test pattern
		command, _ := inv.Input["command"].(string)
		if command == "" {
			continue
		}

		isBuildCommand := false
		for _, pattern := range buildPatterns {
			if strings.Contains(strings.ToLower(command), strings.ToLower(pattern)) {
				isBuildCommand = true
				break
			}
		}

		if !isBuildCommand {
			continue
		}

		passed := inv.Status == "success"
		if !passed {
			overallPassed = false
		}

		// Extract output summary (truncate for context)
		outputSummary := ""
		if inv.Output != nil {
			if outStr, ok := inv.Output.(string); ok {
				if len(outStr) > 2000 {
					outputSummary = outStr[:2000] + "... [truncated]"
				} else {
					outputSummary = outStr
				}
			}
		}

		buildSteps = append(buildSteps, map[string]any{
			"command":  command,
			"status":   inv.Status,
			"passed":   passed,
			"duration": inv.Duration,
			"output":   outputSummary,
			"error":    inv.Error,
		})
	}

	if len(buildSteps) == 0 {
		return nil
	}

	return map[string]any{
		"overall_passed": overallPassed,
		"steps_run":      len(buildSteps),
		"steps":          buildSteps,
	}
}

// extractCLIOutput extracts the stdout string from a CLITool or GHTool response,
// trying Data["result"].Stdout, Data["stdout"], then Observation parsing.
func (a *OrchestratorAgent) extractCLIOutput(resp core.NBToolResponse) string {
	if data, ok := resp.Data.(map[string]any); ok {
		if res, ok := data["result"]; ok {
			// Struct pointer (in-process)
			if cliOutput, ok := res.(*tools.CLIOutput); ok && cliOutput.Stdout != "" {
				return strings.TrimSpace(cliOutput.Stdout)
			}
			// map[string]any after JSON round-trip (tracker/cache)
			if resMap, ok := res.(map[string]any); ok {
				if stdout, ok := resMap["stdout"].(string); ok && stdout != "" {
					return strings.TrimSpace(stdout)
				}
			}
		}
		if stdout, ok := data["stdout"].(string); ok {
			return strings.TrimSpace(stdout)
		}
	}
	if idx := strings.Index(resp.Observation, "Output:\n"); idx >= 0 {
		return strings.TrimSpace(resp.Observation[idx+len("Output:\n"):])
	}
	return ""
}

func (a *OrchestratorAgent) extractGitHubRepo(url string) string {
	if url == "" {
		return ""
	}

	if strings.Contains(url, "github.com/") {
		start := strings.Index(url, "github.com/") + len("github.com/")
		end := len(url)
		if idx := strings.Index(url[start:], ".git"); idx > 0 {
			end = start + idx
		}
		return url[start:end]
	} else if strings.Contains(url, "github.com:") {
		start := strings.Index(url, "github.com:") + len("github.com:")
		end := len(url)
		if idx := strings.Index(url[start:], ".git"); idx > 0 {
			end = start + idx
		}
		return url[start:end]
	}
	return ""
}

// extractGitLabProject extracts the project path from a GitLab URL
// Handles nested groups: gitlab.com/group/subgroup/project -> group/subgroup/project
func (a *OrchestratorAgent) extractGitLabProject(url string) string {
	if url == "" {
		return ""
	}

	// Remove .git suffix
	url = strings.TrimSuffix(url, ".git")

	// Handle HTTPS format: https://gitlab.com/group/subgroup/project
	if strings.Contains(url, "gitlab.com/") {
		start := strings.Index(url, "gitlab.com/") + len("gitlab.com/")
		return url[start:]
	}

	// Handle SSH format: git@gitlab.com:group/subgroup/project
	if strings.Contains(url, "gitlab.com:") {
		start := strings.Index(url, "gitlab.com:") + len("gitlab.com:")
		return url[start:]
	}

	// Handle self-hosted GitLab (look for common patterns)
	// Try to extract path after the host
	if strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") {
		// Parse URL and extract path
		parts := strings.SplitN(url, "://", 2)
		if len(parts) == 2 {
			hostAndPath := parts[1]
			slashIdx := strings.Index(hostAndPath, "/")
			if slashIdx > 0 {
				return hostAndPath[slashIdx+1:]
			}
		}
	}

	// Handle SSH format for self-hosted: git@host:path
	if strings.HasPrefix(url, "git@") {
		colonIdx := strings.Index(url, ":")
		if colonIdx > 0 {
			return url[colonIdx+1:]
		}
	}

	return ""
}

func (a *OrchestratorAgent) mergeAgentResults(agent1Facts map[string]any, agent2Result map[string]any) map[string]any {
	merged := make(map[string]any)

	for k, v := range agent1Facts {
		merged[k] = v
	}

	// DO NOT merge title/description from CodeFixer - ErrorRCA owns the analysis
	// CodeFixer should only provide execution status and technical details
	if origCode, ok := agent2Result["original_code"].(string); ok && origCode != "" {
		merged["original_code"] = origCode
	}
	if fixedCode, ok := agent2Result["fixed_code"].(string); ok && fixedCode != "" {
		merged["fixed_code"] = fixedCode
	}
	if gitDiff, ok := agent2Result["git_diff"].(string); ok && gitDiff != "" {
		merged["git_diff"] = gitDiff
	}
	if fixPR, ok := agent2Result["fix_pr"]; ok {
		merged["fix_pr"] = fixPR
	}

	// Add CodeFixer execution status fields
	if execStatus, ok := agent2Result["execution_status"].(string); ok && execStatus != "" {
		merged["execution_status"] = execStatus
	}
	if execSummary, ok := agent2Result["execution_summary"].(string); ok && execSummary != "" {
		merged["execution_summary"] = execSummary
	}
	if filesModified, ok := agent2Result["files_modified"]; ok {
		merged["files_modified"] = filesModified
	}

	// Ensure all missing fields are preserved from specialist agent (agent1Facts)
	// These fields should come from the specialist agent, not the fixer agent
	if _, exists := merged["commits"]; !exists {
		if commits, ok := agent1Facts["commits"]; ok {
			merged["commits"] = commits
		}
	}
	if _, exists := merged["pr_list"]; !exists {
		if prList, ok := agent1Facts["pr_list"]; ok {
			merged["pr_list"] = prList
		}
	}
	if _, exists := merged["file_path"]; !exists {
		if filePath, ok := agent1Facts["file_path"]; ok {
			merged["file_path"] = filePath
		}
	}

	return merged
}

// executeFixAndReviewLoop implements iterative fix-review process with max 3 attempts
func (a *OrchestratorAgent) executeFixAndReviewLoop(ctx context.Context, sessionCtx *session.SessionContext, factsData map[string]any) (map[string]any, error) {
	maxAttempts := 3
	var lastFixerResult map[string]any
	var lastReviewResult ReviewResult

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			a.reportProgress(fmt.Sprintf("Reworking fix (attempt %d/%d)...", attempt, maxAttempts))
		}
		a.logger.Log(common.EventStepStart, fmt.Sprintf("Fix-review attempt %d/%d", attempt, maxAttempts), nil)

		// Record baseline step count so build verification only considers THIS attempt's invocations
		var baselineStep int
		if a.toolTracker != nil {
			baselineStep = a.toolTracker.GetCurrentStepCount()
		}

		// Run CodeFixer - check if revert is needed for this attempt
		var fixerResult map[string]any
		var err error

		// Check if review feedback is available
		reviewFeedback, hasFeedback := factsData["_review_feedback"].(string)
		useRevert, shouldRevert := factsData["_use_revert"].(bool)

		if hasFeedback && reviewFeedback != "" {
			// Use ExecuteWithRevert for any attempt with feedback (regardless of revert flag)
			if shouldRevert && useRevert {
				a.logger.Log(common.EventStepStart, "Executing CodeFixer with REVERT workflow", map[string]any{
					"attempt":  attempt,
					"feedback": reviewFeedback,
				})
			} else {
				a.logger.Log(common.EventStepStart, "Executing CodeFixer with FEEDBACK workflow", map[string]any{
					"attempt":  attempt,
					"feedback": reviewFeedback,
				})
			}
			fixerResult, err = a.codeFixerAgent.ExecuteWithRevert(ctx, sessionCtx, factsData, reviewFeedback)
			// Clear the revert flag for next iteration
			delete(factsData, "_use_revert")
		} else {
			// Normal workflow (first attempt, no feedback)
			a.logger.Log(common.EventStepStart, "Executing CodeFixer with normal workflow", map[string]any{
				"attempt":      attempt,
				"has_feedback": false,
			})
			fixerResult, err = a.codeFixerAgent.Execute(ctx, sessionCtx, factsData)
		}

		if err != nil {
			return nil, fmt.Errorf("fixer attempt %d failed: %w", attempt, err)
		}
		lastFixerResult = fixerResult

		// --- FIX COMPLETENESS VALIDATION ---
		// Check if the fixer executed a substantive portion of the instructions.
		// Prevents partial fixes (e.g., only adding imports without implementing logic).
		if execStatus, ok := fixerResult["execution_status"].(string); ok && execStatus == "partial_success" {
			a.logger.Log(common.EventStepFailure, "Fixer reported partial_success — some instructions were skipped", map[string]any{
				"attempt":          attempt,
				"execution_status": execStatus,
			})
		}
		a.validateFixCompleteness(fixerResult, factsData, attempt)

		// Extract build verification results from tool tracker
		// The CodeFixerAgent is prompted to run build/lint commands via cli tool
		a.reportProgress("Verifying build...")
		buildVerification := a.extractBuildVerificationResults(baselineStep)
		if buildVerification != nil {
			// Let the LLM's own verification judgment take precedence over tool-tracker pattern matching.
			// The LLM ran the commands, saw retries succeed/fail, and reported its conclusion via submit_analysis.
			// extractBuildVerificationResults uses simplistic "any failure = overall fail" logic which doesn't
			// account for the LLM's retry pattern (e.g., narrow scope fails → broad scope succeeds).
			trackerPassed := buildVerification["overall_passed"]
			if llmVerification, ok := fixerResult["verification_passed"].(bool); ok {
				buildVerification["overall_passed"] = llmVerification
				a.logger.Log(common.EventStepComplete, "Using LLM verification_passed as source of truth", map[string]any{
					"llm_verification_passed": llmVerification,
					"tracker_overall_passed":  trackerPassed,
					"steps_run":               buildVerification["steps_run"],
				})
			}
			fixerResult["build_verification"] = buildVerification
			a.logger.Log(common.EventStepComplete, "Build verification results extracted", map[string]any{
				"overall_passed": buildVerification["overall_passed"],
				"steps_run":      buildVerification["steps_run"],
			})
		} else {
			// LLM didn't run build verification — CodeReview will proceed with LLM-based review only
			a.logger.Log(common.EventStepComplete, "No build verification detected from LLM — CodeReview will use LLM-based review", map[string]any{"attempt": attempt})
		}

		// Extract git diff for review
		gitDiff, _ := fixerResult["git_diff"].(string)
		if gitDiff == "" {
			execStatus, _ := fixerResult["execution_status"].(string)
			if execStatus == "failed" || execStatus == "partial_success" {
				a.logger.Log(common.EventStepFailure, "Empty diff but execution reported failure", map[string]any{
					"attempt":           attempt,
					"execution_status":  execStatus,
					"execution_summary": fixerResult["execution_summary"],
				})
				if attempt < maxAttempts {
					factsData["_review_feedback"] = fmt.Sprintf(
						"Previous attempt produced no code changes. Status: %s. Summary: %v. Re-examine the target file and apply the fix correctly.",
						execStatus, fixerResult["execution_summary"],
					)
					continue
				}
			}
			a.logger.Log(common.EventStepComplete, "No changes to review — file may already be in correct state", map[string]any{"attempt": attempt})
			break
		}

		// Run ReviewAgent
		a.reportProgress("Running code review...")
		reviewResult, err := a.codeReviewAgent.Execute(ctx, sessionCtx, fixerResult, gitDiff, attempt)
		if err != nil {
			a.logger.Log(common.EventStepFailure, "Review agent failed with error", map[string]any{"attempt": attempt, "error": err.Error()})
			// Record the failure so shouldCreatePR can see review was attempted but crashed
			lastReviewResult = ReviewResult{
				Approved: false,
				Feedback: fmt.Sprintf("Review agent error: %s", err.Error()),
				Attempt:  attempt,
			}
			break
		}
		lastReviewResult = reviewResult

		// Check if approved
		if reviewResult.Approved {
			a.logger.Log(common.EventStepComplete, "Changes approved by reviewer", map[string]any{
				"attempt":    attempt,
				"confidence": reviewResult.Confidence,
			})
			break // Success!
		}

		// If not approved and we have more attempts, handle feedback and retries
		if attempt < maxAttempts {
			feedback := fmt.Sprintf("ATTEMPT %d REJECTED: %s", attempt, reviewResult.Feedback)
			sessionCtx.AddToScratchpad("ReviewAgent", feedback)

			// Log detailed review results for debugging
			a.logger.Log(common.EventStepFailure, "Changes rejected - analyzing feedback for retry strategy", map[string]any{
				"attempt":           attempt,
				"feedback":          reviewResult.Feedback,
				"issues":            reviewResult.Issues,
				"syntax_errors":     reviewResult.SyntaxErrors,
				"formatting_issues": reviewResult.FormattingIssues,
				"requires_revert":   reviewResult.RequiresRevert,
			})

			// Decide whether to revert or just rework based on review result
			if reviewResult.RequiresRevert {
				a.logger.Log(common.EventStepStart, "Review indicates REVERT needed - using revert workflow", map[string]any{
					"attempt":       attempt,
					"revert_reason": reviewResult.Feedback,
				})
				// Use CodeFixerAgent with revert for next attempt
				factsData["_use_revert"] = true
				factsData["_review_feedback"] = reviewResult.Feedback
			} else {
				a.logger.Log(common.EventStepStart, "Review indicates corrections needed - using iterative workflow", map[string]any{
					"attempt":  attempt,
					"feedback": reviewResult.Feedback,
				})
				// Add detailed feedback for next iteration
				factsData["_review_feedback"] = reviewResult.Feedback
			}
		} else {
			a.logger.Log(common.EventStepFailure, "Max attempts reached - accepting final fix", map[string]any{
				"final_attempt":  attempt,
				"final_feedback": reviewResult.Feedback,
			})
		}
	}

	// Ensure result reflects actual state when review was not approved
	if lastReviewResult.Attempt > 0 && !lastReviewResult.Approved {
		lastFixerResult["execution_status"] = "failed"
		lastFixerResult["execution_summary"] = fmt.Sprintf(
			"Fix applied but rejected by code review after %d attempt(s). Last feedback: %s",
			lastReviewResult.Attempt, lastReviewResult.Feedback,
		)
	}

	// Add review information to final result
	if lastReviewResult.Attempt > 0 {
		lastFixerResult["review"] = map[string]any{
			"approved":          lastReviewResult.Approved,
			"feedback":          lastReviewResult.Feedback,
			"attempt":           lastReviewResult.Attempt,
			"confidence":        lastReviewResult.Confidence,
			"issues":            lastReviewResult.Issues,
			"syntax_errors":     lastReviewResult.SyntaxErrors,
			"formatting_issues": lastReviewResult.FormattingIssues,
			"required_revert":   lastReviewResult.RequiresRevert,
			"build_verified":    lastReviewResult.BuildVerified,
		}
	}

	return lastFixerResult, nil
}

// shouldCreatePR determines if a PR should be created based on review results and changes
// Returns (shouldCreate bool, reason string)
func (a *OrchestratorAgent) shouldCreatePR(mergedData map[string]any) (bool, string) {
	// Check if there are actual changes
	gitDiff, hasChanges := mergedData["git_diff"].(string)
	if !hasChanges || gitDiff == "" {
		reason := "No git diff available - no changes were made to tracked files. This may indicate the file was already in the desired state or the error location was incorrect."
		a.logger.Log(common.EventStepComplete, "No changes to create PR for", nil)
		return false, reason
	}

	// Check review results if available
	if reviewData, hasReview := mergedData["review"].(map[string]any); hasReview {
		if approved, ok := reviewData["approved"].(bool); ok && !approved {
			reason := "Changes not approved by code reviewer"
			if feedback, ok := reviewData["feedback"].(string); ok {
				reason = fmt.Sprintf("%s: %s", reason, feedback)
			}
			a.logger.Log(common.EventStepFailure, "PR creation blocked - changes not approved by reviewer", map[string]any{
				"feedback": reviewData["feedback"],
			})
			return false, reason
		}
	}

	return true, ""
}

// createPullRequest creates a PR with the changes and NudgeBee branding
func (a *OrchestratorAgent) createPullRequest(ctx context.Context, sessionCtx *session.SessionContext, mergedData map[string]any) (map[string]any, error) {
	// Extract necessary information
	filePath, _ := mergedData["file_path"].(string)
	title, _ := mergedData["title"].(string)
	description, _ := mergedData["description"].(string)
	gitDiff, _ := mergedData["git_diff"].(string)

	a.logger.Log(common.EventStepStart, "Creating pull request with SessionContext", map[string]any{
		"has_session_ctx": sessionCtx != nil,
		"event_id":        sessionCtx.EventID,
		"title":           title,
	})

	if gitDiff == "" {
		return nil, fmt.Errorf("no git diff available for PR creation")
	}

	// --- PR DEDUPLICATION CHECK (DISABLED) ---
	// TODO: Re-enable when we have smarter duplicate detection that considers:
	// - Same CVE/issue being fixed (not just file overlap)
	// - PR title/description similarity
	// - Actual code change comparison
	// Current implementation only checks file overlap which causes false positives
	// when different issues affect the same file (e.g., two different CVEs in same Dockerfile)
	//
	// modifiedFiles := a.extractModifiedFiles(mergedData)
	// if len(modifiedFiles) > 0 {
	// 	if existingPRURL, isDuplicate := a.checkExistingPRs(ctx, sessionCtx, modifiedFiles); isDuplicate {
	// 		a.logger.Log(common.EventStepComplete, "Skipping PR creation — duplicate detected", map[string]any{
	// 			"existing_pr_url": existingPRURL,
	// 			"modified_files":  modifiedFiles,
	// 		})
	// 		return map[string]any{
	// 			"url":               existingPRURL,
	// 			"pr_url":            existingPRURL,
	// 			"status":            "skipped_duplicate",
	// 			"message":           fmt.Sprintf("PR creation skipped: an existing open PR already modifies the same files. See: %s", existingPRURL),
	// 			"files_modified":    modifiedFiles,
	// 			"duplicate_of":      existingPRURL,
	// 			"title":             title,
	// 			"branch_name":       "",
	// 			"pr_description":    description,
	// 			"formatted_pr_body": "",
	// 		}, nil
	// 	}
	// }

	// Analyze recent commits and PRs/MRs to match patterns
	recentCommits := a.analyzeRecentCommits()
	recentPRs := a.analyzeRecentPRs(sessionCtx)

	// Use LLM to intelligently detect the best commit pattern based on repository context and fix content
	detectedPattern := a.detectCommitPattern(ctx, recentCommits, recentPRs, title, description, sessionCtx.SkillsContext)

	// Apply pattern to title if not already present
	formattedTitle := a.applyPatternToTitle(title, detectedPattern)

	a.logger.Log(common.EventStepComplete, "Applied LLM-detected repository pattern to PR title", map[string]any{
		"original_title":   title,
		"formatted_title":  formattedTitle,
		"pattern":          detectedPattern,
		"detection_method": "llm",
	})

	// Use repository helper to find the actual git repository directory
	// Must use currentWorkingDir (updated after repo_clone), not workspaceDir (static init value)
	repoHelper := tools.NewRepositoryHelper()
	actualRepoDir := repoHelper.FindRepositoryDirectoryFromBase(a.currentWorkingDir)

	a.logger.Log(common.EventStepStart, "Generating NudgeBee-branded PR description", map[string]any{
		"has_session_ctx": sessionCtx != nil,
		"event_id":        sessionCtx.EventID,
		"repo_path":       actualRepoDir,
	})

	// Generate NudgeBee-branded PR description
	brandedDescription := a.generateNudgeBeePRDescription(ctx, description, sessionCtx, actualRepoDir, formattedTitle, gitDiff, filePath, formattedTitle)

	// Generate intelligent branch name based on formatted title
	branchName := a.generateBranchName(ctx, formattedTitle, sessionCtx.SkillsContext)

	// Create new branch and push changes using CLI tool
	cliTool := tools.NewCLITool(a.currentWorkingDir)

	// Resolve the base branch BEFORE creating the fix branch (after checkout -b, HEAD changes).
	// Prefer the branch from the session context; fall back to the currently checked-out branch.
	// Reject SHA-shaped values — `gh pr create --base <SHA>` fails with "Base ref must be a branch".
	baseBranch := ""
	if sessionCtx.RepoContext != nil && sessionCtx.RepoContext.Branch != "" && !looksLikeGitSHA(sessionCtx.RepoContext.Branch) {
		baseBranch = sessionCtx.RepoContext.Branch
	}
	if baseBranch == "" || baseBranch == "HEAD" {
		headResult := cliTool.Execute(ctx, map[string]any{
			"command":           "git rev-parse --abbrev-ref HEAD",
			"working_directory": actualRepoDir,
		})
		if headResult.Status == "success" {
			if parsed := a.extractCLIOutput(headResult); parsed != "" {
				baseBranch = parsed
			}
		}
	}

	// Detached HEAD (worktree with --detach) returns literal "HEAD".
	// Resolve the remote's default branch instead.
	if baseBranch == "" || baseBranch == "HEAD" {
		defaultResult := cliTool.Execute(ctx, map[string]any{
			"command":           "git symbolic-ref refs/remotes/origin/HEAD --short",
			"working_directory": actualRepoDir,
		})
		if defaultResult.Status == "success" {
			parsed := a.extractCLIOutput(defaultResult)
			parsed = strings.TrimPrefix(parsed, "origin/")
			if parsed != "" {
				baseBranch = parsed
			}
		}
	}

	// Final fallback
	if baseBranch == "" || baseBranch == "HEAD" {
		baseBranch = "main"
	}

	// Create and checkout new branch BEFORE committing
	branchResult := cliTool.Execute(ctx, map[string]any{
		"command":           fmt.Sprintf("git checkout -b %s", branchName),
		"working_directory": actualRepoDir,
	})
	if branchResult.Status != "success" {
		return nil, fmt.Errorf("failed to create branch: %s", branchResult.Error)
	}

	// Now commit the changes to the new branch with formatted title
	if err := a.commitChanges(ctx, formattedTitle, brandedDescription, sessionCtx.SkillsContext); err != nil {
		return nil, fmt.Errorf("failed to commit changes: %w", err)
	}

	// Check what commits exist on the branch before pushing
	logResult := cliTool.Execute(ctx, map[string]any{
		"command": "git log --oneline -n 5",
	})

	a.logger.Log(common.EventStepComplete, "Checking commits before push", map[string]any{
		"branch_name": branchName,
		"commits":     logResult.Result,
	})

	// Push new branch to origin
	a.logger.Log(common.EventStepStart, "Pushing branch to origin", map[string]any{
		"branch_name":   branchName,
		"command":       fmt.Sprintf("git push -u origin %s", branchName),
		"workspace_dir": a.workspaceDir,
	})

	// Reuse the repository helper variables from above

	pushResult := cliTool.Execute(ctx, map[string]any{
		"command":           fmt.Sprintf("git push -u origin %s", branchName),
		"working_directory": actualRepoDir,
	})

	a.logger.Log(common.EventStepComplete, "Push operation completed", map[string]any{
		"status":      pushResult.Status,
		"error":       pushResult.Error,
		"observation": pushResult.Observation,
		"result_type": fmt.Sprintf("%T", pushResult.Result),
		"result_data": pushResult.Result,
	})

	if pushResult.Status != "success" {
		a.logger.Log(common.EventStepFailure, "Failed to push branch", map[string]any{
			"branch_name": branchName,
			"error":       pushResult.Error,
		})
		return nil, fmt.Errorf("failed to push branch: %s", pushResult.Error)
	}

	a.logger.Log(common.EventStepComplete, "Branch pushed successfully", map[string]any{
		"branch_name": branchName,
	})

	// Determine git provider for PR/MR creation
	provider := gitprovider.GitProviderGitHub // Default
	if sessionCtx.RepoContext != nil && sessionCtx.RepoContext.GitProvider != "" {
		provider = gitprovider.ParseProvider(sessionCtx.RepoContext.GitProvider)
	}

	// Log PR/MR creation attempt with details
	mrTerminology := gitprovider.GetMergeRequestTerminology(provider)
	a.logger.Log(common.EventStepStart, fmt.Sprintf("Creating %s", gitprovider.GetMergeRequestFullTerminology(provider)), map[string]any{
		"title":                   formattedTitle,
		"branch_name":             branchName,
		"base_branch":             baseBranch,
		"provider":                string(provider),
		"recent_prs_analyzed":     len(recentPRs),
		"recent_commits_analyzed": len(recentCommits),
	})

	var prURL string
	var prResult core.NBToolResponse

	if provider == gitprovider.GitProviderGitLab {
		// Create MR using GitLab CLI
		// Get GitLab token from session credentials for authentication
		gitlabToken := ""
		if sessionCtx.Credentials != nil && sessionCtx.Credentials.Token != "" {
			gitlabToken = sessionCtx.Credentials.Token
		}
		glabTool := tools.NewGLabToolWithToken(a.workspaceDir, gitlabToken)
		glabArgs := []any{"mr", "create", "--title", formattedTitle, "--description", brandedDescription, "--source-branch", branchName, "--yes"}
		if baseBranch != "" {
			glabArgs = append(glabArgs, "--target-branch", baseBranch)
		}
		prResult = glabTool.Execute(ctx, map[string]any{
			"args":              glabArgs,
			"working_directory": actualRepoDir,
		})

		if prResult.Status != "success" {
			return nil, fmt.Errorf("failed to create MR: %s", prResult.Error)
		}

		// Extract MR URL from glab CLI output
		if prResult.Data != nil {
			if resultData, ok := prResult.Data.(map[string]any); ok {
				if stdout, ok := resultData["stdout"].(string); ok {
					// glab mr create outputs the MR URL
					lines := strings.Split(strings.TrimSpace(stdout), "\n")
					for _, line := range lines {
						line = strings.TrimSpace(line)
						if strings.Contains(line, "/-/merge_requests/") {
							prURL = line
							break
						}
					}
				}
			}
		}

		// Fallback to result field
		if prURL == "" && strings.Contains(prResult.Result, "/-/merge_requests/") {
			prURL = prResult.Result
		}

		if prURL == "" {
			return nil, fmt.Errorf("failed to create MR: GitLab CLI did not return MR URL")
		}
	} else {
		// Create PR using GitHub CLI
		// Get GitHub token from session credentials for authentication
		githubToken := ""
		if sessionCtx.Credentials != nil && sessionCtx.Credentials.Token != "" {
			githubToken = sessionCtx.Credentials.Token
		}
		ghTool := tools.NewGHToolWithToken(a.workspaceDir, githubToken)
		ghArgs := []any{"pr", "create", "--title", formattedTitle, "--body", brandedDescription, "--head", branchName}
		if baseBranch != "" {
			ghArgs = append(ghArgs, "--base", baseBranch)
		}
		prResult = ghTool.Execute(ctx, map[string]any{
			"args":              ghArgs,
			"working_directory": actualRepoDir,
		})

		if prResult.Status != "success" {
			return nil, fmt.Errorf("failed to create PR: %s", prResult.Error)
		}

		// Extract PR URL from gh CLI output
		if prResult.Data != nil {
			if resultData, ok := prResult.Data.(map[string]any); ok {
				if stdout, ok := resultData["stdout"].(string); ok {
					// gh pr create typically outputs the PR URL on the last line
					lines := strings.Split(strings.TrimSpace(stdout), "\n")
					if len(lines) > 0 {
						lastLine := strings.TrimSpace(lines[len(lines)-1])
						if strings.HasPrefix(lastLine, "https://github.com/") {
							prURL = lastLine
						}
					}
				}
			}
		}

		// Fallback to result field
		if prURL == "" && strings.HasPrefix(prResult.Result, "https://github.com/") {
			prURL = prResult.Result
		}

		if prURL == "" {
			return nil, fmt.Errorf("failed to create PR: GitHub CLI did not return PR URL")
		}
	}

	// Create the PR/MR info response
	prInfo := map[string]any{
		"status":      "success",
		"title":       formattedTitle,
		"description": brandedDescription,
		"branch":      branchName,
		"message":     fmt.Sprintf("%s created with NudgeBee branding and repository pattern", mrTerminology),
		"url":         prURL,
		"pattern":     detectedPattern,
		"provider":    string(provider),
	}

	a.logger.Log(common.EventStepComplete, fmt.Sprintf("%s creation with NudgeBee branding completed", mrTerminology), map[string]any{
		"file_path":             filePath,
		"has_git_diff":          true,
		"has_nudgebee_branding": true,
		"has_event_link":        sessionCtx.EventID != "",
		"provider":              string(provider),
	})

	return prInfo, nil
}

// generateNudgeBeePRDescription adds NudgeBee branding and event links to PR description
func (a *OrchestratorAgent) generateNudgeBeePRDescription(ctx context.Context, originalDescription string, sessionCtx *session.SessionContext, repoPath, title, gitDiff, filePath, commitMessage string) string {
	// First, analyze recent PRs to match format

	description := originalDescription

	description, err := a.generateImprovedPRDescription(ctx, repoPath, title, description, filePath, commitMessage, gitDiff, sessionCtx)
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to generate improved PR description", map[string]any{
			"error": err.Error(),
		})
		// Fallback to original description
		description = originalDescription
	}
	// Add NudgeBee branding section
	branding := "\n\n---\n\n🤖 **This PR was automatically generated by [NudgeBee](https://nudgebee.com) AI coding agent**\n\n"
	branding += "*Powered by AI-driven code analysis and automated fix generation*\n"

	// Add event investigation link if available
	a.logger.Log(common.EventStepStart, "Checking event and recommendation link requirements", map[string]any{
		"has_event_id":       sessionCtx.EventID != "",
		"event_id":           sessionCtx.EventID,
		"has_recommendation": sessionCtx.RecommendationID != "",
		"recommendation_id":  sessionCtx.RecommendationID,
		"account_id":         sessionCtx.AccountID,
		"has_base_url":       a.config.NudgeBee.BaseURL != "",
		"base_url":           a.config.NudgeBee.BaseURL,
	})

	// Add event investigation link (for event-driven fixes)
	if sessionCtx.EventID != "" && a.config.NudgeBee.BaseURL != "" {
		eventURL := fmt.Sprintf("%s/investigate?id=%s", a.config.NudgeBee.BaseURL, sessionCtx.EventID)
		branding += fmt.Sprintf("\n🔍 **[View Original Investigation](%s)**\n", eventURL)
		branding += "\n*Click the link above to see the full investigation and analysis that led to this fix*\n"

		a.logger.Log(common.EventStepComplete, "Added event investigation link to PR", map[string]any{
			"event_url": eventURL,
		})
	}

	// Add recommendation link (for recommendation-driven fixes)
	if sessionCtx.RecommendationID != "" && sessionCtx.AccountID != "" && a.config.NudgeBee.BaseURL != "" {
		recommendationURL := fmt.Sprintf("%s/kubernetes/details/%s?tab=1&subtab=0&id=%s#optimize/right-sizing", a.config.NudgeBee.BaseURL, sessionCtx.AccountID, sessionCtx.RecommendationID)
		branding += fmt.Sprintf("\n📊 **[View Full Recommendation](%s)**\n", recommendationURL)
		branding += "\n*View detailed resource analysis, usage patterns, and cost savings in NudgeBee*\n"

		a.logger.Log(common.EventStepComplete, "Added recommendation link to PR", map[string]any{
			"recommendation_url": recommendationURL,
			"recommendation_id":  sessionCtx.RecommendationID,
			"account_id":         sessionCtx.AccountID,
		})
	}

	conversationUrl := fmt.Sprintf("%s/ask-nudgebee?accountId=%s&session_id=%s", a.config.NudgeBee.BaseURL, sessionCtx.AccountID, sessionCtx.AnalysisID)
	branding += fmt.Sprintf("\nView Detailed **[Nubi Conversation](%s)**\n", conversationUrl)

	// Log if neither link was added
	if sessionCtx.EventID == "" && sessionCtx.RecommendationID == "" {
		a.logger.Log(common.EventStepFailure, "No context links added - missing both EventID and RecommendationID", map[string]any{
			"missing_event_id":       sessionCtx.EventID == "",
			"missing_recommendation": sessionCtx.RecommendationID == "",
			"missing_base_url":       a.config.NudgeBee.BaseURL == "",
			"hint":                   "Set BASE_URL environment variable and pass event_id or recommendation_id in QueryConfig",
		})
	}

	return description + branding
}

// PRInfo represents information from a recent PR
type PRInfo struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
}

// GitLabMRInfo represents information from a GitLab merge request
// GitLab uses "description" instead of "body" in JSON responses
type GitLabMRInfo struct {
	Number      int    `json:"iid"` // GitLab uses iid for MR number
	Title       string `json:"title"`
	Description string `json:"description"`
	State       string `json:"state"`
}

// CommitInfo represents information from a recent commit
type CommitInfo struct {
	Message string `json:"message"`
	Title   string `json:"title"`
}

// analyzeRecentPRs fetches and analyzes the latest 10 PRs/MRs to understand format patterns
// Supports both GitHub (PRs) and GitLab (MRs) based on the session context
func (a *OrchestratorAgent) analyzeRecentPRs(sessionCtx *session.SessionContext) []PRInfo {
	// Use repository helper to find the actual git repository directory
	repoHelper := tools.NewRepositoryHelper()
	actualRepoDir := repoHelper.FindRepositoryDirectoryFromBase(a.workspaceDir)

	ctx := context.Background()

	// Determine git provider for PR/MR listing
	provider := gitprovider.GitProviderGitHub // Default
	if sessionCtx != nil && sessionCtx.RepoContext != nil && sessionCtx.RepoContext.GitProvider != "" {
		provider = gitprovider.ParseProvider(sessionCtx.RepoContext.GitProvider)
	}

	mrTerminology := gitprovider.GetMergeRequestTerminology(provider)

	var result core.NBToolResponse
	if provider == gitprovider.GitProviderGitLab {
		// Use GitLab CLI (glab) to get recent MRs
		gitlabToken := ""
		if sessionCtx.Credentials != nil && sessionCtx.Credentials.Token != "" {
			gitlabToken = sessionCtx.Credentials.Token
		}
		glabTool := tools.NewGLabToolWithToken(a.workspaceDir, gitlabToken)
		result = glabTool.Execute(ctx, map[string]any{
			"args":              []any{"mr", "list", "--state", "merged", "-P", "10", "--json", "iid,title,description,state"},
			"working_directory": actualRepoDir,
		})
	} else {
		// Use GitHub CLI (gh) to get recent PRs
		// Use gh CLI to get recent PRs with token for authentication
		// Get GitHub token from session credentials for authenticated API calls
		githubToken := ""
		if sessionCtx.Credentials != nil && sessionCtx.Credentials.Token != "" {
			githubToken = sessionCtx.Credentials.Token
		}
		ghTool := tools.NewGHToolWithToken(a.workspaceDir, githubToken)
		result = ghTool.Execute(ctx, map[string]any{
			"args":              []any{"pr", "list", "--state", "MERGED", "--limit", "10", "--json", "number,title,body,state"},
			"working_directory": actualRepoDir,
		})
	}

	if result.Status != "success" {
		a.logger.Log(common.EventStepFailure, fmt.Sprintf("Failed to fetch recent %ss for format analysis", mrTerminology), map[string]any{
			"error":    result.Error,
			"provider": string(provider),
		})
		return []PRInfo{}
	}

	// Parse the JSON response
	var prs []PRInfo
	if result.Data != nil {
		if resultData, ok := result.Data.(map[string]any); ok {
			if stdout, ok := resultData["stdout"].(string); ok {
				if provider == gitprovider.GitProviderGitLab {
					// Parse GitLab MR JSON (uses "description" and "iid" instead of "body" and "number")
					var gitlabMRs []GitLabMRInfo
					if err := json.Unmarshal([]byte(stdout), &gitlabMRs); err != nil {
						a.logger.Log(common.EventStepFailure, "Failed to parse GitLab MR JSON", map[string]any{
							"error": err.Error(),
						})
						return []PRInfo{}
					}
					// Convert GitLab MRs to PRInfo format
					for _, mr := range gitlabMRs {
						prs = append(prs, PRInfo{
							Number: mr.Number,
							Title:  mr.Title,
							Body:   mr.Description, // Map description -> body
							State:  mr.State,
						})
					}
				} else {
					// Parse GitHub PR JSON
					if err := json.Unmarshal([]byte(stdout), &prs); err != nil {
						a.logger.Log(common.EventStepFailure, "Failed to parse GitHub PR JSON", map[string]any{
							"error": err.Error(),
						})
						return []PRInfo{}
					}
				}
			}
		}
	}

	a.logger.Log(common.EventStepComplete, fmt.Sprintf("Analyzed recent %ss for format matching", mrTerminology), map[string]any{
		fmt.Sprintf("%s_count", strings.ToLower(mrTerminology)): len(prs),
		"provider": string(provider),
	})

	return prs
}

// analyzeRecentCommits fetches and analyzes recent commit messages to understand title patterns
func (a *OrchestratorAgent) analyzeRecentCommits() []CommitInfo {
	// Use repository helper to find the actual git repository directory
	repoHelper := tools.NewRepositoryHelper()
	actualRepoDir := repoHelper.FindRepositoryDirectoryFromBase(a.workspaceDir)

	// Use CLI tool to get recent commit messages
	cliTool := tools.NewCLITool(a.workspaceDir)
	ctx := context.Background()

	result := cliTool.Execute(ctx, map[string]any{
		"command":           "git log -20 --pretty=format:%s",
		"working_directory": actualRepoDir,
	})

	if result.Status != "success" {
		a.logger.Log(common.EventStepFailure, "Failed to fetch recent commits for pattern analysis", map[string]any{
			"error": result.Error,
		})
		return []CommitInfo{}
	}

	// Parse commit messages
	var commits []CommitInfo
	if result.Data != nil {
		if resultData, ok := result.Data.(map[string]any); ok {
			if stdout, ok := resultData["stdout"].(string); ok {
				lines := strings.Split(strings.TrimSpace(stdout), "\n")
				for _, line := range lines {
					if strings.TrimSpace(line) != "" {
						commits = append(commits, CommitInfo{
							Message: line,
							Title:   line, // For commit messages, title is the same as message
						})
					}
				}
			}
		}
	}

	a.logger.Log(common.EventStepComplete, "Analyzed recent commits for pattern matching", map[string]any{
		"commit_count": len(commits),
	})

	return commits
}

// operatorSkillsPromptBlock renders the operator-authored skills (already a
// rendered <skills> block forwarded by llm-server) as a guidance section for
// the orchestrator's own LLM sub-prompts — PR description and commit/title
// pattern detection. PR creation is orchestrator-driven (not agent-driven), so
// these prompts are the only place a "how to create a PR" skill can influence
// the actual pull request. Returns "" when no skills are present so prompts
// stay byte-for-byte unchanged.
func operatorSkillsPromptBlock(skills string) string {
	if strings.TrimSpace(skills) == "" {
		return ""
	}
	return "\n<operator-skills>\n" +
		"The following operator-authored skills give authoritative, project-specific guidance on how this team creates pull requests (commit/PR conventions, required sections, branch/target rules, tone). Treat them as the highest-priority guidance and follow them wherever they apply, overriding the generic defaults below when they conflict.\n" +
		skills + "\n</operator-skills>\n\n"
}

// detectCommitPattern uses LLM to intelligently analyze recent commits and PRs to determine the best title format
func (a *OrchestratorAgent) detectCommitPattern(ctx context.Context, recentCommits []CommitInfo, recentPRs []PRInfo, title, description, skills string) string {
	// Build context for LLM analysis
	prompt := a.buildPatternDetectionPrompt(recentCommits, recentPRs, title, description, skills)

	a.logger.Log(common.EventStepStart, "Using LLM to detect commit pattern", map[string]any{
		"recent_commits":     len(recentCommits),
		"recent_prs":         len(recentPRs),
		"includes_pr_bodies": true,
		"analysis_method":    "llm",
	})

	// Call LLM to analyze patterns
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, prompt),
	}

	response, err := a.llmClient.GenerateContent(ctx, messages)
	if err != nil {
		a.logger.Log(common.EventStepFailure, "LLM pattern detection failed, using fallback", map[string]any{
			"error": err.Error(),
		})
		return "fix:" // Fallback to fix: if LLM fails
	}

	// Extract text from response
	responseText := ""
	if len(response.Choices) > 0 && response.Choices[0].Content != "" {
		responseText = response.Choices[0].Content
	}

	// Parse LLM response to extract pattern
	detectedPattern := a.parsePatternFromLLMResponse(responseText)

	a.logger.Log(common.EventStepComplete, "LLM detected commit pattern", map[string]any{
		"pattern":        detectedPattern,
		"total_analyzed": len(recentCommits) + len(recentPRs),
		"llm_based":      true,
	})

	return detectedPattern
}

// buildPatternDetectionPrompt creates a prompt for the LLM to analyze commit patterns
func (a *OrchestratorAgent) buildPatternDetectionPrompt(recentCommits []CommitInfo, recentPRs []PRInfo, title, description, skills string) string {
	var prompt strings.Builder

	prompt.WriteString("You are analyzing a GitHub repository to determine the appropriate commit/PR title pattern.\n\n")
	if block := operatorSkillsPromptBlock(skills); block != "" {
		prompt.WriteString(block)
	}
	prompt.WriteString("## Task\n")
	prompt.WriteString("Based on recent repository activity and the current fix, suggest the BEST conventional commit pattern to use.\n\n")

	// Add recent commits for context
	prompt.WriteString("## Recent Commits (last 20):\n")
	for i, commit := range recentCommits {
		if i < 15 { // Limit to 15 for brevity
			fmt.Fprintf(&prompt, "- %s\n", commit.Title)
		}
	}
	prompt.WriteString("\n")

	// Add recent PRs for context with descriptions
	if len(recentPRs) > 0 {
		prompt.WriteString("## Recent Merged PRs:\n")
		for _, pr := range recentPRs {
			fmt.Fprintf(&prompt, "### PR #%d: %s\n", pr.Number, pr.Title)
			if pr.Body != "" {
				// Truncate long descriptions to first 300 chars
				body := pr.Body
				if len(body) > 300 {
					body = body[:300] + "..."
				}
				fmt.Fprintf(&prompt, "Description: %s\n", body)
			}
			prompt.WriteString("\n")
		}
	}

	// Add current fix context
	prompt.WriteString("## Current Fix Being Applied:\n")
	fmt.Fprintf(&prompt, "**Title:** %s\n", title)
	fmt.Fprintf(&prompt, "**Description:** %s\n\n", description)

	// Instructions
	prompt.WriteString("## Instructions:\n")
	prompt.WriteString("1. Analyze the recent commits and PRs (both titles AND descriptions) to understand repository conventions\n")
	prompt.WriteString("2. Look for patterns in how the team describes their changes - this reveals their preferred style\n")
	prompt.WriteString("3. Consider the nature of the current fix (bug fix, new feature, maintenance, etc.)\n")
	prompt.WriteString("4. Choose the MOST APPROPRIATE pattern from:\n")
	prompt.WriteString("   - `fix:` - Bug fixes, error corrections, issue resolutions\n")
	prompt.WriteString("   - `feat:` - New features, capabilities, or enhancements\n")
	prompt.WriteString("   - `chore:` - Maintenance tasks, dependency updates, config changes\n")
	prompt.WriteString("   - `docs:` - Documentation updates\n")
	prompt.WriteString("   - `test:` - Test additions or modifications\n")
	prompt.WriteString("   - `refactor:` - Code refactoring without behavior changes\n")
	prompt.WriteString("   - `perf:` - Performance improvements\n")
	prompt.WriteString("   - `style:` - Code style/formatting changes\n\n")

	prompt.WriteString("## Response Format:\n")
	prompt.WriteString("Respond with ONLY the pattern (e.g., 'fix:' or 'feat:') followed by a brief one-line reason.\n")
	prompt.WriteString("Format: `PATTERN: REASON`\n\n")
	prompt.WriteString("Example responses:\n")
	prompt.WriteString("- `fix: Resolving a ValidationError for missing field`\n")
	prompt.WriteString("- `feat: Adding new notification capability`\n")
	prompt.WriteString("- `chore: Repository uses chore: for most maintenance tasks`\n\n")

	prompt.WriteString("Your response (pattern and reason):")

	return prompt.String()
}

// parsePatternFromLLMResponse extracts the pattern from LLM response
func (a *OrchestratorAgent) parsePatternFromLLMResponse(response string) string {
	// Clean response
	response = strings.TrimSpace(response)
	responseLower := strings.ToLower(response)

	// Define valid patterns
	validPatterns := []string{"fix:", "feat:", "chore:", "docs:", "test:", "refactor:", "perf:", "style:", "ci:", "build:"}

	// Try to find pattern at the start of response
	for _, pattern := range validPatterns {
		if strings.HasPrefix(responseLower, pattern) {
			return pattern
		}
	}

	// Try to find pattern anywhere in the response
	for _, pattern := range validPatterns {
		if strings.Contains(responseLower, pattern) {
			a.logger.Log(common.EventStepComplete, "Extracted pattern from LLM response", map[string]any{
				"pattern":  pattern,
				"response": response,
			})
			return pattern
		}
	}

	// If no pattern found, log and return default
	a.logger.Log(common.EventStepFailure, "Could not parse pattern from LLM response, using default", map[string]any{
		"response":         response,
		"fallback_pattern": "fix:",
	})

	return "fix:" // Default fallback
}

// applyPatternToTitle applies the detected pattern to the title if not already present
func (a *OrchestratorAgent) applyPatternToTitle(title, pattern string) string {
	titleLower := strings.ToLower(title)

	// Check if title already has a pattern prefix
	commonPatterns := []string{"fix:", "feat:", "chore:", "docs:", "test:", "refactor:", "style:", "perf:", "ci:", "build:"}
	for _, p := range commonPatterns {
		if strings.HasPrefix(titleLower, p) {
			// Title already has a pattern, return as is
			return title
		}
	}

	// Apply the detected pattern
	// Ensure first letter after pattern is lowercase (conventional commit style)
	if len(title) > 0 {
		firstChar := strings.ToLower(string(title[0]))
		restOfTitle := ""
		if len(title) > 1 {
			restOfTitle = title[1:]
		}
		return pattern + " " + firstChar + restOfTitle
	}

	return pattern + " " + title
}

// adaptDescriptionToRecentFormat adapts the description to match recent PR format patterns
func (a *OrchestratorAgent) generateImprovedPRDescription(ctx context.Context, repoPath, title, description, filePath, commitMessage string, gitDiff string, sessionCtx *session.SessionContext) (string, error) {
	// Determine git provider for PR/MR listing
	provider := gitprovider.GitProviderGitHub // Default
	if sessionCtx != nil && sessionCtx.RepoContext != nil && sessionCtx.RepoContext.GitProvider != "" {
		provider = gitprovider.ParseProvider(sessionCtx.RepoContext.GitProvider)
	}

	mrTerminology := gitprovider.GetMergeRequestTerminology(provider)

	// Get PR/MR history using provider-specific tool
	var prHistory string
	var result core.NBToolResponse
	if provider == gitprovider.GitProviderGitLab {
		// Use GitLab CLI (glab) to get recent MRs
		gitlabToken := ""
		if sessionCtx != nil && sessionCtx.Credentials != nil && sessionCtx.Credentials.Token != "" {
			gitlabToken = sessionCtx.Credentials.Token
		}
		glabTool := tools.NewGLabToolWithToken(a.workspaceDir, gitlabToken)
		result = glabTool.Execute(ctx, map[string]any{
			"args":              []any{"mr", "list", "--state", "all", "-P", "10", "--json", "iid,title,description,web_url,author"},
			"working_directory": repoPath,
		})
	} else {
		// Use GitHub CLI (gh) to get recent PRs
		githubToken := ""
		if sessionCtx != nil && sessionCtx.Credentials != nil && sessionCtx.Credentials.Token != "" {
			githubToken = sessionCtx.Credentials.Token
		}
		ghTool := tools.NewGHToolWithToken(a.workspaceDir, githubToken)
		result = ghTool.Execute(ctx, map[string]any{
			"args":              []any{"pr", "list", "--state", "all", "--limit", "10", "--json", "title,body,url,author"},
			"working_directory": repoPath,
		})
	}

	if result.Status != "success" {
		a.logger.Log(common.EventStepFailure, fmt.Sprintf("Failed to get %s list, will use PR template only", mrTerminology), map[string]any{
			"error":    result.Error,
			"provider": string(provider),
		})
		// Continue with empty prHistory — PR template file is more important
	} else {
		// Extract stdout from result
		if result.Data != nil {
			if resultData, ok := result.Data.(map[string]any); ok {
				if stdout, ok := resultData["stdout"].(string); ok {
					prHistory = stdout
				}
			}
		}
	}

	// Also try to get PR/MR template based on provider
	var prTemplateFiles []string
	if provider == gitprovider.GitProviderGitLab {
		// GitLab merge request template paths
		prTemplateFiles = []string{
			".gitlab/merge_request_templates/Default.md",
			".gitlab/merge_request_templates/default.md",
			".gitlab/merge_request_template.md",
			"docs/merge_request_template.md",
		}
	} else {
		// GitHub pull request template paths
		prTemplateFiles = []string{
			".github/pull_request_template.md",
			".github/PULL_REQUEST_TEMPLATE.md",
			".github/PULL_REQUEST_TEMPLATE/pull_request_template.md",
			"docs/pull_request_template.md",
		}
	}

	prTemplate := ""
	for _, templateFile := range prTemplateFiles {
		templatePath := filepath.Join(repoPath, templateFile)
		if content, err := os.ReadFile(templatePath); err == nil {
			prTemplate = fmt.Sprintf("PR TEMPLATE FROM %s:\n%s\n\n", templateFile, string(content))
			a.logger.Log(common.EventStepComplete, "Found PR template", map[string]any{
				"template_file": templateFile,
				"template_size": len(content),
			})
			break
		}
	}

	if prTemplate == "" {
		a.logger.Log(common.EventStepStart, "No PR template found, using recent PR examples only", nil)
	}

	// Extract original error details from session context
	originalQuery := ""
	originalLogs := ""
	if sessionCtx != nil {
		originalQuery = sessionCtx.OriginalQuery
		originalLogs = sessionCtx.InitialLogs
	}

	// Operator-authored skills (e.g. a "how to create a PR" skill) are the only
	// way to steer the orchestrator-driven PR description, which is otherwise
	// template-mimicking. Render them as a high-priority block when present.
	skillsBlock := ""
	if sessionCtx != nil {
		skillsBlock = operatorSkillsPromptBlock(sessionCtx.SkillsContext)
	}

	// Create prompt for LLM to generate PR description
	prompt := fmt.Sprintf(`You are a technical writer creating PR descriptions. Analyze the repository's PR template pattern and follow it exactly.
%s
STEP 1: ANALYZE the template and examples below to identify the exact structure
STEP 2: FOLLOW that exact pattern for the new PR description

%sPREVIOUS PR EXAMPLES (analyze these patterns):
%s

ORIGINAL PROBLEM BEING SOLVED:
- User Query: %s
- Error Logs/Details: %s

CHANGE DETAILS TO INTEGRATE INTO TEMPLATE:
- File: %s
- Title: %s
- Description: %s
- Commit Message: %s
- Git Diff changes: %s

REQUIREMENTS:
0. If an <operator-skills> section is present above, it OVERRIDES these defaults wherever it specifies how PRs should be written (required sections, content, tone, conventions) — apply it on top of the repository template.
1. STRICTLY FOLLOW the exact PR template structure from examples above
2. MIMIC only the structural formatting, sections, and style patterns used in recent PRs (headings, checklists, section order)
3. DO NOT create custom sections - use only the patterns found in examples
4. ADAPT the existing template to include the error details and fix information
5. PRESERVE the repository's established PR description conventions
6. If examples use specific headings/format, use EXACTLY the same structure
7. Insert error details and fix information into the existing template pattern
8. DO NOT copy footers, attribution lines, branding, signatures, or bot-generated metadata from the examples - only copy the template structure and section format

MANDATORY - INCLUDE ORIGINAL ERROR/LOGS:
You MUST include a collapsible section with the original error/logs that triggered this fix.
- If there are error logs provided above, include them VERBATIM (not summarized) in the PR description
- Use this exact format for the section:

## Original Error/Logs

<details>
<summary>Click to expand the original error that triggered this fix</summary>

`+"```"+`
[INSERT THE ACTUAL ERROR/LOG TEXT HERE VERBATIM - extract the key error message, stack trace, or log from the User Query or Error Logs above]
`+"```"+`

</details>

- Extract the actual error message, stack trace, or log content from either "User Query" or "Error Logs/Details"
- If both contain error information, include the most relevant/complete one
- Truncate if longer than 2000 characters but preserve the key error information
- Place this section AFTER the main description but BEFORE any testing/checklist sections

CRITICAL: DO NOT invent new sections or formats except for the mandatory error section. Follow existing patterns precisely.

OUTPUT: Just the PR description following the exact repository template with the mandatory error section included, nothing else.`,
		skillsBlock, prTemplate, prHistory, originalQuery, originalLogs, filePath, title, description, commitMessage, gitDiff)

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, prompt),
	}

	resp, err := a.llmClient.GenerateContent(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("LLM call failed: %w", err)
	}

	prDescription := strings.TrimSpace(resp.Choices[0].Content)

	// Strip any existing NudgeBee branding that the LLM may have copied from examples
	prDescription = a.stripExistingBranding(prDescription)

	// Validate PR description
	if len(prDescription) == 0 {
		return "", fmt.Errorf("LLM generated empty PR description")
	}

	// Ensure minimum length for meaningful description
	if len(prDescription) < 50 {
		return "", fmt.Errorf("LLM generated PR description too short: %d characters", len(prDescription))
	}

	return prDescription, nil
}

// stripExistingBranding removes any existing NudgeBee branding sections from the description
// This prevents duplicate branding when the LLM copies patterns from recent PRs
func (a *OrchestratorAgent) stripExistingBranding(description string) string {
	// Find the first occurrence of the branding separator (---, ___, or ***)
	// followed by NudgeBee-related content
	patterns := []string{
		"\n---\n",
		"\n___\n",
		"\n***\n",
		"\n--------\n",
	}

	result := description
	for _, pattern := range patterns {
		if idx := strings.Index(description, pattern); idx > 0 {
			// Check if this separator is followed by NudgeBee branding
			afterSeparator := description[idx:]
			if strings.Contains(strings.ToLower(afterSeparator), "nudgebee") ||
				strings.Contains(strings.ToLower(afterSeparator), "automatically generated") ||
				strings.Contains(strings.ToLower(afterSeparator), "ai coding agent") {
				// This looks like branding - cut it off
				result = strings.TrimSpace(description[:idx])
				a.logger.Log(common.EventStepComplete, "Stripped existing branding from LLM output", map[string]any{
					"original_length": len(description),
					"stripped_length": len(result),
					"removed_chars":   len(description) - len(result),
				})
				break
			}
		}
	}

	return result
}

// looksLikeGitSHA reports whether s is a 7-40 char lowercase hex string,
// matching abbreviated or full git commit SHAs. Used to guard PR-base
// resolution against callers that mistakenly pass a SHA where a branch is expected.
var gitSHARegex = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

func looksLikeGitSHA(s string) bool {
	return gitSHARegex.MatchString(s)
}

// instructionsRequireWrite reports whether any entry in
// factsData["implementation_instructions"] uses action="write" — the marker
// that the fix legitimately needs to CREATE a file rather than edit one.
// Used to bypass the file-must-exist precondition on the fixer dispatch path.
// Tolerant to malformed shapes: returns false on any decoding hiccup rather
// than panicking, because the precondition gate that calls this is just an
// optimisation — the fixer's own tool-level checks remain authoritative.
func instructionsRequireWrite(factsData map[string]any) bool {
	rawList, ok := factsData["implementation_instructions"].([]any)
	if !ok || len(rawList) == 0 {
		return false
	}
	for _, item := range rawList {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if action, _ := entry["action"].(string); action == "write" {
			return true
		}
	}
	return false
}

// generateBranchName creates an intelligent branch name based on the issue title.
// When operator skills are present it lets the LLM follow the team's branch
// convention (prefix, separators, casing); the result is sanitized to a valid
// git ref and given a uniqueness suffix. With no skills, or on any failure, it
// uses the deterministic "fix/<slug>-<ts>" default (unchanged behavior).
func (a *OrchestratorAgent) generateBranchName(ctx context.Context, title, skills string) string {
	if strings.TrimSpace(skills) != "" {
		if name := a.generateSkillAwareBranchName(ctx, title, skills); name != "" {
			return name
		}
	}

	// Use regex to sanitize branch name (same logic as CodeFixerAgent)
	var invalidChars = regexp.MustCompile(`[^a-z0-9-_/]+`)
	sanitizedTitle := strings.ReplaceAll(strings.ToLower(title), " ", "-")
	sanitizedTitle = invalidChars.ReplaceAllString(sanitizedTitle, "")
	if len(sanitizedTitle) > 40 {
		sanitizedTitle = sanitizedTitle[:40]
	}

	// Add timestamp suffix to ensure uniqueness
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	branchName := fmt.Sprintf("fix/%s-%s", sanitizedTitle, timestamp)

	return branchName
}

// sanitizeBranchRef coerces an arbitrary string into a valid lowercase git ref
// slug (letters, digits, '-', '_', '/'), trimmed and length-capped. Returns ""
// when nothing usable remains.
func sanitizeBranchRef(raw string) string {
	invalidChars := regexp.MustCompile(`[^a-z0-9-_/]+`)
	s := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), " ", "-")
	s = invalidChars.ReplaceAllString(s, "")
	s = strings.Trim(s, "-/")
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-/")
	}
	return s
}

// generateSkillAwareBranchName asks the LLM for a branch name that follows the
// operator skills' convention, then sanitizes it and appends a uniqueness
// suffix. Returns "" on any failure so the caller can fall back.
func (a *OrchestratorAgent) generateSkillAwareBranchName(ctx context.Context, title, skills string) string {
	prompt := fmt.Sprintf(`You generate a git branch name for an automated code fix.
%sRULES:
- Output ONLY the branch name on a single line, nothing else.
- Valid git ref: lowercase letters, digits, hyphens, underscores and slashes only; no spaces.
- Follow the branch-naming convention in the operator-skills section above (prefix, separators, casing). If it does not specify one, use a "fix/<short-slug>" style.
- Keep it under 60 characters. Do NOT add a date or numeric uniqueness suffix — one is appended automatically.

Change title: %s

Branch name:`, operatorSkillsPromptBlock(skills), title)

	resp, err := a.llmClient.GenerateContent(ctx, []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, prompt),
	})
	if err != nil || len(resp.Choices) == 0 {
		a.logger.Log(common.EventStepFailure, "Skill-aware branch name generation failed; using deterministic default", map[string]any{"error": fmt.Sprintf("%v", err)})
		return ""
	}
	firstLine := strings.SplitN(strings.TrimSpace(resp.Choices[0].Content), "\n", 2)[0]
	slug := sanitizeBranchRef(firstLine)
	if slug == "" {
		return ""
	}
	return fmt.Sprintf("%s-%d", slug, time.Now().Unix())
}

// generateSkillAwareCommitMessage asks the LLM for a commit message that follows
// the operator skills' convention (format, structure, trailers). Returns "" on
// any failure so the caller can fall back to the default title/description.
func (a *OrchestratorAgent) generateSkillAwareCommitMessage(ctx context.Context, title, description, skills string) string {
	// Keep the description context bounded — the commit message should be concise.
	if len(description) > 1500 {
		description = description[:1500]
	}
	prompt := fmt.Sprintf(`You write a git commit message for an automated code fix.
%sRULES:
- Output ONLY the commit message (subject line, then optional body), nothing else.
- Follow the commit-message convention in the operator-skills section above (subject format/prefix, line length, required trailers). If it does not specify one, use a concise conventional-commit subject plus a short body.
- Do not wrap the message in code fences or quotes.

Proposed title: %s
Change summary: %s

Commit message:`, operatorSkillsPromptBlock(skills), title, description)

	resp, err := a.llmClient.GenerateContent(ctx, []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, prompt),
	})
	if err != nil || len(resp.Choices) == 0 {
		a.logger.Log(common.EventStepFailure, "Skill-aware commit message generation failed; using default", map[string]any{"error": fmt.Sprintf("%v", err)})
		return ""
	}
	msg := strings.TrimSpace(resp.Choices[0].Content)
	// Defensively strip a leading/trailing code fence if the model added one.
	msg = strings.TrimSpace(strings.Trim(msg, "`"))
	return msg
}

// buildCommitHeredoc builds a `git commit -F -` command using a heredoc whose
// delimiter is guaranteed not to appear as a standalone line in the message. A
// colliding line would terminate the heredoc early and silently truncate the
// commit message — more likely now that the message can be LLM-generated from
// operator skills. Uses a single-quoted delimiter so the body is taken
// literally (no shell expansion).
func buildCommitHeredoc(commitMsg string) string {
	delimiter := "EOF_COMMIT_MSG"
	if commitMsgHasDelimiterLine(commitMsg, delimiter) {
		delimiter = fmt.Sprintf("EOF_COMMIT_MSG_%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("git commit -F - <<'%s'\n%s\n%s", delimiter, commitMsg, delimiter)
}

// commitMsgHasDelimiterLine reports whether any line of msg is exactly the
// heredoc delimiter (which is how a quoted heredoc decides to terminate).
func commitMsgHasDelimiterLine(msg, delimiter string) bool {
	for _, line := range strings.Split(msg, "\n") {
		if line == delimiter {
			return true
		}
	}
	return false
}

// commitChanges commits the current changes with appropriate message
func (a *OrchestratorAgent) commitChanges(ctx context.Context, title, description, skills string) error {
	// Use CLI tool for git operations
	// Must use currentWorkingDir (updated after repo_clone), not workspaceDir (static init value)
	cliTool := tools.NewCLITool(a.currentWorkingDir)

	// Use repository helper to find the actual git repository directory
	repoHelper := tools.NewRepositoryHelper()
	actualRepoDir := repoHelper.FindRepositoryDirectoryFromBase(a.currentWorkingDir)

	// Configure git user with bot credentials
	configResult := cliTool.Execute(ctx, map[string]any{
		"command":           "git config user.name 'nudgebee-bot' && git config user.email 'bot@nudgebee.com'",
		"working_directory": actualRepoDir,
	})
	if configResult.Status != "success" {
		return fmt.Errorf("failed to configure git user: %s", configResult.Error)
	}

	// Add all changes
	addResult := cliTool.Execute(ctx, map[string]any{
		"command":           "git add .",
		"working_directory": actualRepoDir,
	})
	if addResult.Status != "success" {
		return fmt.Errorf("failed to add changes: %s", addResult.Error)
	}

	// Create commit with title and description
	commitMsg := title
	if description != "" {
		commitMsg = fmt.Sprintf("%s\n\n%s", title, description)
	}

	// When operator skills are present, let them shape the commit message
	// (format, prefix, trailers). Fall back to the default above on failure.
	if strings.TrimSpace(skills) != "" {
		if msg := a.generateSkillAwareCommitMessage(ctx, title, description, skills); msg != "" {
			commitMsg = msg
		}
	}

	// Use heredoc to avoid shell escaping issues with special characters
	// This is safer than using -m with quoted strings
	heredocCmd := buildCommitHeredoc(commitMsg)

	commitResult := cliTool.Execute(ctx, map[string]any{
		"command":           heredocCmd,
		"working_directory": actualRepoDir,
	})
	if commitResult.Status != "success" {
		return fmt.Errorf("failed to commit changes: %s", commitResult.Error)
	}

	a.logger.Log(common.EventStepComplete, "Changes committed", map[string]any{
		"title":              title,
		"description_length": len(description),
	})

	return nil
}

// updateWorkingDirectoryFromToolInvocations checks tool tracker for repo_clone invocations
// and updates the working directory to the cloned repository path
func (a *OrchestratorAgent) updateWorkingDirectoryFromToolInvocations() {
	if a.toolTracker == nil {
		a.logger.Log(common.EventStepFailure, "Tool tracker is nil - cannot update working directory", map[string]any{
			"hint": "EnableToolTracking may not have been called",
		})
		return
	}

	// Check all tool invocations for repo_clone
	invocations := a.toolTracker.GetInvocations()
	a.logger.Log(common.EventStepStart, "Checking tool invocations for repo_clone", map[string]any{
		"total_invocations": len(invocations),
		"tool_tracker_id":   fmt.Sprintf("%p", a.toolTracker),
	})

	for _, inv := range invocations {
		if inv.ToolName == "repo_clone" {
			a.logger.Log(common.EventStepStart, "Found repo_clone invocation", map[string]any{
				"invocation_id": inv.ID,
				"status":        inv.Status,
				"has_output":    inv.Output != nil,
			})

			if inv.Status != "success" {
				a.logger.Log(common.EventStepFailure, "Skipping repo_clone - status is not success", map[string]any{
					"status": inv.Status,
					"error":  inv.Error,
				})
				continue
			}

			// Extract local_path from the tool output
			if outputMap, ok := inv.Output.(map[string]any); ok {
				a.logger.Log(common.EventStepStart, "repo_clone output is a map", map[string]any{
					"has_data_field": outputMap["data"] != nil,
					"output_keys":    fmt.Sprintf("%v", getMapKeys(outputMap)),
				})

				// UPDATED: Tool tracker stores result.Data directly, so local_path is at the top level
				// Try to extract local_path directly from outputMap first
				if localPath, ok := outputMap["local_path"].(string); ok && localPath != "" {
					a.logger.Log(common.EventStepComplete, "Detected repo_clone invocation - updating working directory from top-level local_path", map[string]any{
						"local_path":    localPath,
						"invocation_id": inv.ID,
					})
					a.SetWorkingDirectory(localPath)
					return // Use the first successful clone
				}

				// Fallback: Check for nested data field (legacy structure)
				if dataMap, ok := outputMap["data"].(map[string]any); ok {
					a.logger.Log(common.EventStepStart, "repo_clone data field is a map (legacy structure)", map[string]any{
						"has_local_path": dataMap["local_path"] != nil,
						"data_keys":      fmt.Sprintf("%v", getMapKeys(dataMap)),
					})

					if localPath, ok := dataMap["local_path"].(string); ok && localPath != "" {
						a.logger.Log(common.EventStepComplete, "Detected repo_clone invocation - updating working directory from nested data.local_path", map[string]any{
							"local_path":    localPath,
							"invocation_id": inv.ID,
						})
						a.SetWorkingDirectory(localPath)
						return // Use the first successful clone
					}
				}

				// If we get here, local_path wasn't found in either location
				a.logger.Log(common.EventStepFailure, "local_path not found in repo_clone output", map[string]any{
					"output_keys":          fmt.Sprintf("%v", getMapKeys(outputMap)),
					"has_top_level_path":   outputMap["local_path"] != nil,
					"has_nested_data":      outputMap["data"] != nil,
					"top_level_path_type":  fmt.Sprintf("%T", outputMap["local_path"]),
					"top_level_path_value": outputMap["local_path"],
				})
			} else {
				a.logger.Log(common.EventStepFailure, "repo_clone output is not a map", map[string]any{
					"output_type": fmt.Sprintf("%T", inv.Output),
				})
			}
		}
	}

	a.logger.Log(common.EventStepFailure, "No valid repo_clone invocation found - working directory not updated", map[string]any{
		"current_workspace_dir": a.workspaceDir,
		"current_working_dir":   a.currentWorkingDir,
	})
}

// SetWorkingDirectory updates the central working directory for all operations
func (a *OrchestratorAgent) SetWorkingDirectory(dir string) {
	if dir != "" {
		// Sanitize: remove embedded quotes and clean the path
		dir = strings.ReplaceAll(dir, `"`, "")
		dir = strings.TrimSpace(dir)
		dir = filepath.Clean(dir)

		a.currentWorkingDir = dir

		// Update CodeFixer agent's workspace directory and planner context
		if a.codeFixerAgent != nil {
			a.codeFixerAgent.WorkspaceDir = dir
			// Update the planner's secureContext with new working directory
			if a.codeFixerAgent.Planner != nil {
				a.codeFixerAgent.Planner.SetSecureContext("working_directory", dir)
			}
		}

		// Update CodeReviewAgent's workspace directory
		if a.codeReviewAgent != nil {
			a.codeReviewAgent.workspaceDir = dir
		}

		a.logger.Log(common.EventStepComplete, "Updated orchestrator working directory", map[string]any{
			"working_directory": dir,
		})
	}
}

// GetWorkingDirectory returns the current working directory
func (a *OrchestratorAgent) GetWorkingDirectory() string {
	return a.currentWorkingDir
}

// EnableToolTracking implements the ToolTrackable interface to accept a shared tracker from the handler
func (a *OrchestratorAgent) EnableToolTracking(tracker *common.ToolInvocationTracker, logger *common.Logger) {
	a.toolTracker = tracker
	a.logger.Log(common.EventAnalysisStart, "Orchestrator received shared tool tracker", map[string]any{
		"tracker_id": "shared_from_handler",
	})

	// Recreate specialist agents with the shared tracker
	a.codeAgent = NewCodeAgentWithTracker(a.config, a.llmClient, a.gitClient, a.logger, a.workspaceDir, tracker)
	a.errorRCAAgent = NewErrorRCAAgentWithTracker(a.config, a.llmClient, a.gitClient, a.logger, a.workspaceDir, tracker)
	a.performanceDebuggerAgent = NewPerformanceDebuggerAgentWithTracker(a.config, a.llmClient, a.gitClient, a.logger, a.workspaceDir, tracker)
	a.codeFixerAgent = NewCodeFixerAgentWithTracker(a.llmClient, a.logger, a.workspaceDir, a.config, tracker)

	// Set tool tracker on CodeAgent
	if a.codeAgent != nil {
		a.codeAgent.SetToolTracker(tracker)
		a.logger.Log(common.EventAnalysisStart, "Set tool tracker on CodeAgent", map[string]any{
			"code_agent_updated": true,
		})
	}

	a.logger.Log(common.EventAnalysisStart, "Recreated specialist agents with shared tracker", map[string]any{
		"code_agent_updated":  true,
		"rca_agent_updated":   true,
		"perf_agent_updated":  true,
		"fixer_agent_updated": true,
	})
}

// extractModifiedFiles extracts a list of modified file paths from the merged analysis data.
func (a *OrchestratorAgent) extractModifiedFiles(mergedData map[string]any) []string {
	var files []string

	// Try files_modified (array of strings or array of maps)
	if filesModified, ok := mergedData["files_modified"]; ok {
		switch fm := filesModified.(type) {
		case []any:
			for _, f := range fm {
				switch fv := f.(type) {
				case string:
					files = append(files, fv)
				case map[string]any:
					if fp, ok := fv["file_path"].(string); ok {
						files = append(files, fp)
					} else if fp, ok := fv["path"].(string); ok {
						files = append(files, fp)
					}
				}
			}
		case []string:
			files = append(files, fm...)
		}
	}

	// Fallback to single file_path
	if len(files) == 0 {
		if fp, ok := mergedData["file_path"].(string); ok && fp != "" {
			files = append(files, fp)
		}
	}

	return files
}

// checkExistingPRs checks for existing open PRs that modify the same files.
// Returns (existingPRURL, isDuplicate).
// DISABLED: This function is temporarily disabled because the current implementation
// only checks file overlap, which causes false positives when different issues
// affect the same file (e.g., two different CVEs in the same Dockerfile).
// TODO: Re-enable with smarter duplicate detection that considers:
// - Same CVE/issue being fixed (not just file overlap)
// - PR title/description similarity
// - Actual code change comparison
//
// func (a *OrchestratorAgent) checkExistingPRs(ctx context.Context, sessionCtx *session.SessionContext, modifiedFiles []string) (string, bool) {
// 	if len(modifiedFiles) == 0 {
// 		return "", false
// 	}
//
// 	repoHelper := tools.NewRepositoryHelper()
// 	actualRepoDir := repoHelper.FindRepositoryDirectoryFromBase(a.workspaceDir)
//
// 	// Use gh CLI to list open PRs with file information
// 	githubToken := ""
// 	if sessionCtx.Credentials != nil && sessionCtx.Credentials.Token != "" {
// 		githubToken = sessionCtx.Credentials.Token
// 	}
// 	ghTool := tools.NewGHToolWithToken(a.workspaceDir, githubToken)
// 	result := ghTool.Execute(ctx, map[string]any{
// 		"args":              []any{"pr", "list", "--state", "open", "--json", "number,title,files,url", "--limit", "30"},
// 		"working_directory": actualRepoDir,
// 	})
//
// 	if result.Status != "success" {
// 		a.logger.Log(common.EventStepFailure, "Failed to list existing PRs for dedup check", map[string]any{
// 			"error": result.Error,
// 		})
// 		return "", false // Don't block PR creation if we can't check
// 	}
//
// 	// Parse the JSON output
// 	var prs []struct {
// 		Number int    `json:"number"`
// 		Title  string `json:"title"`
// 		URL    string `json:"url"`
// 		Files  []struct {
// 			Path string `json:"path"`
// 		} `json:"files"`
// 	}
//
// 	// The result may be in Data, Result, or Observation
// 	jsonStr := result.Result
// 	if jsonStr == "" {
// 		jsonStr = result.Observation
// 	}
// 	if result.Data != nil {
// 		if dataMap, ok := result.Data.(map[string]any); ok {
// 			if stdout, ok := dataMap["stdout"].(string); ok {
// 				jsonStr = stdout
// 			}
// 		}
// 	}
//
// 	if err := json.Unmarshal([]byte(jsonStr), &prs); err != nil {
// 		a.logger.Log(common.EventStepFailure, "Failed to parse PR list JSON for dedup check", map[string]any{
// 			"error":    err.Error(),
// 			"raw_json": jsonStr[:min(len(jsonStr), 200)],
// 		})
// 		return "", false
// 	}
//
// 	// Check each open PR for file overlap
// 	// Note: Only exact path matching - no basename matching to avoid false positives
// 	// in monorepos where multiple services have files with same names (Dockerfile, etc.)
// 	modifiedFileSet := make(map[string]bool)
// 	for _, f := range modifiedFiles {
// 		modifiedFileSet[f] = true
// 	}
//
// 	for _, pr := range prs {
// 		overlapCount := 0
// 		for _, prFile := range pr.Files {
// 			if modifiedFileSet[prFile.Path] {
// 				overlapCount++
// 			}
// 		}
//
// 		// If more than half the files overlap, consider it a duplicate
// 		if overlapCount > 0 && (overlapCount >= len(modifiedFiles)/2 || overlapCount >= len(pr.Files)/2) {
// 			a.logger.Log(common.EventStepComplete, "Found existing PR with overlapping files", map[string]any{
// 				"existing_pr_number": pr.Number,
// 				"existing_pr_title":  pr.Title,
// 				"existing_pr_url":    pr.URL,
// 				"overlap_count":      overlapCount,
// 				"modified_files":     modifiedFiles,
// 			})
// 			return pr.URL, true
// 		}
// 	}
//
// 	return "", false
// }

// validateFixCompleteness checks if the fixer executed a substantive portion of the instructions.
// Logs warnings if the fix appears incomplete (e.g., only imports added, no logic changes).
func (a *OrchestratorAgent) validateFixCompleteness(fixerResult map[string]any, factsData map[string]any, attempt int) {
	// Count expected file-modifying instructions (exclude "verify" actions)
	expectedCount := 0
	if instructions, ok := factsData["implementation_instructions"].([]any); ok {
		for _, instr := range instructions {
			if instrMap, ok := instr.(map[string]any); ok {
				action, _ := instrMap["action"].(string)
				if action != "verify" {
					expectedCount++
				}
			}
		}
	}
	if expectedCount == 0 {
		return // No instructions to validate against
	}

	// Count files actually modified
	modifiedFiles := a.extractModifiedFiles(fixerResult)
	executedCount := len(modifiedFiles)

	// Check for import-only changes (common partial fix pattern)
	isImportOnly := true
	if execSummary, ok := fixerResult["execution_summary"].(string); ok {
		importKeywords := []string{"import", "require", "from ", "include"}
		hasImportMention := false
		for _, kw := range importKeywords {
			if strings.Contains(strings.ToLower(execSummary), kw) {
				hasImportMention = true
				break
			}
		}
		logicKeywords := []string{"retry", "wrap", "refactor", "replace", "fix", "update", "change", "modify", "add logic", "implement"}
		hasLogicMention := false
		for _, kw := range logicKeywords {
			if strings.Contains(strings.ToLower(execSummary), kw) {
				hasLogicMention = true
				break
			}
		}
		if hasLogicMention || !hasImportMention {
			isImportOnly = false
		}
	}

	// Log completeness assessment
	completenessRatio := float64(executedCount) / float64(expectedCount)
	if completenessRatio < 0.5 || isImportOnly {
		a.logger.Log(common.EventStepFailure, "WARNING: Fix may be incomplete", map[string]any{
			"attempt":            attempt,
			"expected_steps":     expectedCount,
			"files_modified":     executedCount,
			"completeness_ratio": completenessRatio,
			"import_only":        isImportOnly,
			"modified_files":     modifiedFiles,
		})
	} else {
		a.logger.Log(common.EventStepComplete, "Fix completeness check passed", map[string]any{
			"attempt":            attempt,
			"expected_steps":     expectedCount,
			"files_modified":     executedCount,
			"completeness_ratio": completenessRatio,
		})
	}
}

// isTransientLLMError checks if a specialist agent error was caused by a transient LLM issue.
// These errors bubble up through the chain: LLM client → planner → agent → orchestrator.
func isTransientLLMError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "LLM generation failed") {
		return false
	}
	return strings.Contains(errStr, "504") ||
		strings.Contains(errStr, "503") ||
		strings.Contains(errStr, "500") ||
		strings.Contains(errStr, "DEADLINE_EXCEEDED") ||
		strings.Contains(errStr, "deadline exceeded") ||
		strings.Contains(errStr, "service unavailable") ||
		strings.Contains(errStr, "connection reset")
}
