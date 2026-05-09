// In a file like: agents/code_fixer_agent.go
package agents

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/config"
	"nudgebee/code-analysis-agent/internal/session"
	"nudgebee/code-analysis-agent/llm"
	"nudgebee/code-analysis-agent/models"
	"nudgebee/code-analysis-agent/planners"
	"nudgebee/code-analysis-agent/tools"
	"nudgebee/code-analysis-agent/tools/core"
)

type CodeFixerAgent struct {
	llmClient    *llm.Client
	logger       *common.Logger
	WorkspaceDir string // Made public so orchestrator can update it
	config       *config.Config
	promptLoader *common.PromptLoader
	tools        []core.NBTool
	Planner      *planners.ReActPlanner // Made public so orchestrator can update its context
}

func NewCodeFixerAgent(llmClient *llm.Client, logger *common.Logger, workspaceDir string, cfg *config.Config) *CodeFixerAgent {
	// Create a default tracker for standalone usage
	tracker := common.NewToolInvocationTracker("code_fixer_agent")
	return NewCodeFixerAgentWithTracker(llmClient, logger, workspaceDir, cfg, tracker)
}

// NewCodeFixerAgentWithTracker creates a CodeFixerAgent with a shared tracker (used by orchestrator)
func NewCodeFixerAgentWithTracker(llmClient *llm.Client, logger *common.Logger, workspaceDir string, cfg *config.Config, tracker *common.ToolInvocationTracker) *CodeFixerAgent {
	// Setup tools for CodeFixer: execute implementation_instructions from RCA + verify fixes
	// Shared read tracker enforces read-before-edit at the tool level
	readTracker := tools.NewFileReadTracker()

	fileViewTool := tools.NewFileViewTool(workspaceDir)
	fileViewTool.SetReadTracker(readTracker)

	replaceTool := tools.NewReplaceToolWithWorkspace(workspaceDir)
	replaceTool.SetReadTracker(readTracker)
	// Enable LLM self-correction on the replace tool so failed edits can be auto-fixed
	replaceTool.SetEditCorrectionService(tools.NewEditCorrectionService(llmClient))

	cliTool := tools.NewCLITool(workspaceDir)
	cliTool.SetRestrictPROperations(true) // Only orchestrator creates PRs

	validTools := []core.NBTool{
		fileViewTool,                        // Read files to find exact code and verify changes
		tools.NewFileFindTool(workspaceDir), // Find files to verify RCA's target paths and resolve ambiguities
		replaceTool,                         // Modify files (replace via old_string/new_string, verify)
		tools.NewSubmitAnalysisTool(),       // Report execution status (success/failed)
		cliTool,                             // Run build/lint/test commands to verify fixes
	}
	// Note: grep/rg still excluded - RCA provides exact paths/line numbers.
	// file_find is included to enable pre-flight verification of target file paths.
	// cli_tool is included for build/lint verification after applying fixes.

	// Use the provided shared tracker and wrap tools for comprehensive tracking
	trackedTools := make([]core.NBTool, len(validTools))
	for i, tool := range validTools {
		trackedTools[i] = tools.NewTrackedToolWrapper(tool, tracker, logger)
	}

	logger.Log(common.EventStepStart, "CodeFixerAgent using shared tool tracker", map[string]any{
		"tool_count": len(trackedTools),
		"tracker_id": "shared_from_orchestrator",
	})

	// Initialize ReAct planner with tracked tools
	planner := planners.NewReActPlanner(llmClient, trackedTools, cfg.Agent.ReActMaxIterations)
	planner.SetLogger(logger)

	return &CodeFixerAgent{
		llmClient:    llmClient,
		logger:       logger,
		WorkspaceDir: workspaceDir,
		config:       cfg,
		promptLoader: common.NewPromptLoader(),
		tools:        trackedTools,
		Planner:      planner,
	}
}

// Execute implements fixes suggested by specialist agents using LLM-driven file_view + replace workflow
func (a *CodeFixerAgent) Execute(ctx context.Context, sessionCtx *session.SessionContext, auditFindings map[string]any) (map[string]any, error) {
	return a.executeWithOptions(ctx, sessionCtx, auditFindings, false, "")
}

// ExecuteWithRevert reverts previous changes and reworks the fix
func (a *CodeFixerAgent) ExecuteWithRevert(ctx context.Context, sessionCtx *session.SessionContext, auditFindings map[string]any, reviewFeedback string) (map[string]any, error) {
	return a.executeWithOptions(ctx, sessionCtx, auditFindings, true, reviewFeedback)
}

// executeWithOptions is the internal implementation with revert capabilities
func (a *CodeFixerAgent) executeWithOptions(ctx context.Context, sessionCtx *session.SessionContext, auditFindings map[string]any, shouldRevert bool, reviewFeedback string) (map[string]any, error) {
	actionType := "implementing fix"
	if shouldRevert {
		actionType = "reverting and reworking fix"
	}
	a.logger.Log(common.EventStepStart, fmt.Sprintf("CodeFixerAgent %s using optimized ReAct approach", actionType), map[string]any{
		"should_revert": shouldRevert,
		"has_feedback":  reviewFeedback != "",
	})

	// Set repository context and credentials in the planner
	a.Planner.SetRepositoryContext(sessionCtx.RepoContext)
	if sessionCtx.Credentials != nil {
		// Convert ResolvedCredentials to models.Credentials format expected by repo_clone tool
		modelCreds := &models.Credentials{
			Type:     sessionCtx.Credentials.Type,
			Value:    sessionCtx.Credentials.Token,
			Username: sessionCtx.Credentials.Username,
			Password: sessionCtx.Credentials.Password,
		}
		a.Planner.SetSecureContext("credentials", modelCreds)
	}

	// Handle revert if needed.
	//
	// Revert is a janitorial step that resets the workspace before the next
	// fix attempt. It must not gate the fix-success contract: if revert
	// fails, the next fix attempt's git diff will simply include leftover
	// changes from the previous attempt — the LLM owns the target state and
	// will overwrite/correct as needed. Aborting the entire pipeline because
	// cleanup failed has historically masked successful fixes (e.g. a clean
	// merge-conflict resolution wiped because one revert path errored).
	if shouldRevert {
		if err := a.performRevert(ctx, reviewFeedback, sessionCtx); err != nil {
			a.logger.Log(common.EventStepFailure, "performRevert failed; continuing with fix attempt on dirty workspace", map[string]any{
				"error":    err.Error(),
				"feedback": reviewFeedback,
			})
			sessionCtx.AddToScratchpad("CodeFixerAgent", fmt.Sprintf("REVERT FAILED (continuing): %v", err))
		}
	}

	// Build template-based prompt for systematic fixing
	enhancedQuery := a.buildEnhancedQueryWithFeedback(auditFindings, reviewFeedback, shouldRevert)
	systemPrompt, err := a.buildTemplatePrompt(auditFindings, sessionCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to build template prompt: %w", err)
	}

	// Use ReAct planner with optimized prompts for efficiency
	planResult, err := a.Planner.Plan(ctx, enhancedQuery, systemPrompt)
	if err != nil {
		return nil, fmt.Errorf("ReAct planner execution failed: %w", err)
	}

	planningSuccessful := planResult.Status == "completed"

	// Record implementation process in scratchpad
	implementationNotes := fmt.Sprintf("Implementation Query: %s\n\nSteps Taken: %d\nStatus: %s\nSuccessful: %t",
		enhancedQuery, len(planResult.Steps), planResult.Status, planningSuccessful)
	sessionCtx.AddToScratchpad("CodeFixerAgent", implementationNotes)

	// Generate final git diff after all LLM changes are complete
	// Use repository helper to find the actual git repository directory
	repoHelper := tools.NewRepositoryHelper()
	repoDir := repoHelper.FindRepositoryDirectoryFromBase(a.WorkspaceDir)

	// Check if we're actually in a git repository before trying to generate diff
	var finalGitDiff string
	gitDirPath := filepath.Join(repoDir, ".git")
	if _, err := os.Stat(gitDirPath); os.IsNotExist(err) {
		a.logger.Log(common.EventStepFailure, "Skipping git diff - not in a git repository", map[string]any{
			"workspace_dir": a.WorkspaceDir,
			"repo_dir":      repoDir,
			"reason":        "Repository clone may have failed or no repository was provided",
		})
		finalGitDiff = ""
	} else {
		// Stage only files the CodeFixer actually modified, not the entire repo.
		// Using git add -A would stage unrelated dirty files, causing the review agent
		// to flag them as collateral damage and waste revert+retry cycles.
		modifiedFiles := a.getModifiedFiles()
		if len(modifiedFiles) > 0 {
			for _, f := range modifiedFiles {
				absPath := f
				if !filepath.IsAbs(f) {
					absPath = filepath.Join(repoDir, f)
				}
				if _, stageErr := a.runCommandWithOutput(repoDir, "git", "add", absPath); stageErr != nil {
					a.logger.Log(common.EventStepFailure, "Failed to stage file", map[string]any{"file": f, "error": stageErr.Error()})
				}
			}
			a.logger.Log(common.EventStepComplete, "Staged specific modified files", map[string]any{"files": modifiedFiles})
		} else {
			// Fallback: no files_modified reported, stage everything
			if _, stageErr := a.runCommandWithOutput(repoDir, "git", "add", "-A"); stageErr != nil {
				a.logger.Log(common.EventStepFailure, "Failed to stage changes", map[string]any{"error": stageErr.Error()})
			}
			a.logger.Log(common.EventStepComplete, "No files_modified list available, staged all changes", nil)
		}

		// Generate diff of staged changes against HEAD
		gitDiffOutput, err := a.runCommandWithOutput(repoDir, "git", "diff", "--cached", "HEAD")
		if err != nil {
			a.logger.Log(common.EventStepFailure, "Failed to generate final git diff", map[string]any{"error": err.Error()})
			finalGitDiff = ""
		} else {
			finalGitDiff = gitDiffOutput
			a.logger.Log(common.EventStepComplete, "Generated final git diff", map[string]any{"diff_length": len(finalGitDiff)})

			if len(finalGitDiff) == 0 {
				a.logger.Log(common.EventStepFailure, "Empty git diff detected - no changes found", map[string]any{
					"possible_reasons": []string{
						"File was already in the desired state",
						"Replace tool made identical old/new replacement",
						"Error location was incorrect",
					},
				})
			}
		}
	}

	// Create final result preserving specialist agent analysis
	filePath, _ := auditFindings["file_path"].(string)
	fixResult := map[string]any{
		"title":               auditFindings["title"],
		"description":         auditFindings["description"],
		"file_path":           filePath,
		"git_diff":            finalGitDiff,
		"requires_fix":        false, // Now fixed by LLM
		"confidence_score":    auditFindings["confidence_score"],
		"commits":             auditFindings["commits"],
		"pr_list":             auditFindings["pr_list"],
		"root_cause_analysis": auditFindings["root_cause_analysis"],
	}

	// Merge submit_analysis data from planner (execution_summary, files_modified, etc.)
	// This ensures the orchestrator can access CodeFixer's reported status for validation.
	if submitData := a.Planner.GetSubmitAnalysisData(); submitData != nil {
		if data, ok := submitData.(map[string]any); ok {
			if es, ok := data["execution_summary"].(string); ok {
				fixResult["execution_summary"] = es
			}
			if es, ok := data["execution_status"].(string); ok {
				fixResult["execution_status"] = es
			}
			if fm, ok := data["files_modified"]; ok {
				fixResult["files_modified"] = fm
			}
			if vp, ok := data["verification_passed"]; ok {
				fixResult["verification_passed"] = vp
			}
		}
	}

	return fixResult, nil
}

// runCommand executes a command and returns an error if it fails.
func (a *CodeFixerAgent) runCommand(workDir, command string, args ...string) error {
	cmd := exec.Command(command, args...)
	cmd.Dir = workDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("command '%s %s' failed: %w\nStderr: %s", command, strings.Join(args, " "), err, stderr.String())
	}
	return nil
}

// runCommandWithOutput executes a command and returns its stdout or an error.
func (a *CodeFixerAgent) runCommandWithOutput(workDir, command string, args ...string) (string, error) {
	cmd := exec.Command(command, args...)
	cmd.Dir = workDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("command '%s %s' failed: %w\nStderr: %s", command, strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

// getModifiedFiles returns the list of files the CodeFixer actually changed.
// Pulls from submit_analysis data (files_modified) reported by the LLM.
func (a *CodeFixerAgent) getModifiedFiles() []string {
	if a.Planner == nil {
		return nil
	}
	submitData := a.Planner.GetSubmitAnalysisData()
	if submitData == nil {
		return nil
	}
	data, ok := submitData.(map[string]any)
	if !ok {
		return nil
	}
	fm, ok := data["files_modified"]
	if !ok {
		return nil
	}
	switch v := fm.(type) {
	case []any:
		files := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				files = append(files, s)
			}
		}
		return files
	case []string:
		return v
	}
	return nil
}

// Git operations removed - all git functionality moved to orchestrator

// Helper functions for optimized ReAct approach

func (a *CodeFixerAgent) buildTemplatePrompt(auditFindings map[string]any, sessionCtx *session.SessionContext) (string, error) {
	// LOG: Check if RCA provided implementation_instructions
	implementationInstructions, hasInstructions := auditFindings["implementation_instructions"]
	if hasInstructions {
		a.logger.Log(common.EventStepStart, "CodeFixer received implementation_instructions from RCA", map[string]any{
			"has_implementation_instructions": true,
			"instructions_type":               fmt.Sprintf("%T", implementationInstructions),
		})

		// Log each instruction step for debugging - handle both old and new formats
		if instrArray, ok := implementationInstructions.([]any); ok {
			// NEW FORMAT: Array of instructions
			stepCount := len(instrArray)
			a.logger.Log(common.EventStepStart, "RCA implementation_instructions details (array format)", map[string]any{
				"step_count": stepCount,
				"format":     "array",
			})

			// Log individual steps for clarity
			for i, stepValue := range instrArray {
				if stepMap, ok := stepValue.(map[string]any); ok {
					a.logger.Log(common.EventStepStart, fmt.Sprintf("RCA instruction step %d", i+1), map[string]any{
						"step":      stepMap["step"],
						"action":    stepMap["action"],
						"file_path": stepMap["file_path"],
						"purpose":   stepMap["purpose"],
					})
				}
			}
		} else if instrMap, ok := implementationInstructions.(map[string]any); ok {
			// OLD FORMAT: Object with step_1, step_2, etc.
			stepCount := len(instrMap)
			a.logger.Log(common.EventStepStart, "RCA implementation_instructions details (object format)", map[string]any{
				"step_count": stepCount,
				"format":     "object",
				"steps":      instrMap,
			})

			// Log individual steps for clarity
			for stepKey, stepValue := range instrMap {
				if stepMap, ok := stepValue.(map[string]any); ok {
					a.logger.Log(common.EventStepStart, fmt.Sprintf("RCA instruction %s", stepKey), map[string]any{
						"action":  stepMap["action"],
						"target":  stepMap["target"],
						"purpose": stepMap["purpose"],
					})
				}
			}
		} else {
			a.logger.Log(common.EventStepStart, "RCA implementation_instructions format unrecognized", map[string]any{
				"type":  fmt.Sprintf("%T", implementationInstructions),
				"value": implementationInstructions,
			})
		}
	} else {
		a.logger.Log(common.EventStepStart, "CodeFixer received NO implementation_instructions from RCA", map[string]any{
			"has_implementation_instructions": false,
			"audit_findings_keys":             getMapKeys(auditFindings),
		})
	}

	// Prepare template data (tools are now passed via native function calling, not prompt text)
	templateData := map[string]any{
		"AuditFindings":        auditFindings,
		"OriginalQuery":        sessionCtx.OriginalQuery,
		"InvestigationHistory": sessionCtx.GetScratchpad(),
		"BuildConfig":          sessionCtx.BuildConfig,
		"BuildVerifyEnabled":   a.config.Agent.BuildVerifyEnabled,
	}

	// Load and execute template
	promptTemplate, err := a.promptLoader.LoadPrompt("code_fixer", templateData)
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to load code_fixer prompt template", map[string]any{"error": err.Error()})
		return "", fmt.Errorf("failed to load code_fixer prompt template: %w", err)
	}

	return promptTemplate, nil
}

// Helper function to get map keys for logging
func getMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// performRevert resets the workspace to HEAD before a rework attempt.
//
// Uses two generic git primitives that together handle every porcelain status
// uniformly — no per-status case analysis, no future "what about renames /
// staged-add / submodules" bug:
//
//  1. git restore --source=HEAD --staged --worktree :/
//     → restores all tracked files (staged + worktree) to HEAD. Handles
//     M, MM, D, R, C, U, and unstages A.
//  2. git clean -fd
//     → removes untracked files (??) and empty dirs left behind. Respects
//     .gitignore (no -x) so workspace-local config is preserved.
//
// The previous implementation parsed `git status --porcelain` by hand and
// passed every path to `git checkout HEAD -- <path>`, which fails on
// untracked files (the path doesn't exist in HEAD) and aborts the entire
// rework cycle even when the conflict resolution itself was correct.
//
// This is safe in production because the workspace is a fresh os.MkdirTemp
// per /analyze request — there is no unrelated user work to lose.
func (a *CodeFixerAgent) performRevert(ctx context.Context, reviewFeedback string, sessionCtx *session.SessionContext) error {
	a.logger.Log(common.EventStepStart, "Performing git revert due to review feedback", map[string]any{
		"feedback": reviewFeedback,
	})

	repoHelper := tools.NewRepositoryHelper()
	repoDir := repoHelper.FindRepositoryDirectoryFromBase(a.WorkspaceDir)

	if err := a.runCommand(repoDir, "git", "restore", "--source=HEAD", "--staged", "--worktree", ":/"); err != nil {
		return fmt.Errorf("git restore failed: %w", err)
	}
	if err := a.runCommand(repoDir, "git", "clean", "-fd"); err != nil {
		return fmt.Errorf("git clean failed: %w", err)
	}

	revertNotes := fmt.Sprintf("REVERT PERFORMED:\nReason: %s\nAction: git restore + git clean reset workspace to HEAD", reviewFeedback)
	sessionCtx.AddToScratchpad("CodeFixerAgent", revertNotes)

	a.logger.Log(common.EventStepComplete, "Successfully reverted changes", nil)
	return nil
}

// buildEnhancedQueryWithFeedback builds a query incorporating review feedback
func (a *CodeFixerAgent) buildEnhancedQueryWithFeedback(auditFindings map[string]any, reviewFeedback string, isRework bool) string {
	var query strings.Builder

	if isRework {
		query.WriteString("=== REWORK FIX REQUEST (After Review Feedback) ===\n")
	} else {
		query.WriteString("=== SPECIALIST AGENT FIX REQUEST ===\n")
	}

	if title, ok := auditFindings["title"].(string); ok {
		fmt.Fprintf(&query, "Issue: %s\n", title)
	}

	if description, ok := auditFindings["description"].(string); ok {
		fmt.Fprintf(&query, "Solution: %s\n", description)
	}

	if filePath, ok := auditFindings["file_path"].(string); ok {
		fmt.Fprintf(&query, "Target File: %s\n", filePath)
	}

	if rootCause, ok := auditFindings["root_cause_analysis"].(string); ok {
		fmt.Fprintf(&query, "Root Cause: %s\n", rootCause)
	}

	// Reference implementation instructions from system prompt (avoid double rendering)
	// The detailed step-by-step instructions are already in the system prompt template.
	// Duplicating them here with %q escaping wastes context and confuses the LLM.
	if _, ok := auditFindings["implementation_instructions"]; ok {
		query.WriteString("\nIMPORTANT: Follow the implementation instructions provided in your system prompt exactly. Do NOT create your own implementation approach.\n")
	}

	// Add review feedback if this is a rework
	if isRework && reviewFeedback != "" {
		fmt.Fprintf(&query, "\n=== REVIEW FEEDBACK TO ADDRESS ===\n%s\n", reviewFeedback)
		query.WriteString("\nIMPORTANT: Address ALL issues mentioned in the review feedback above.\n")
		query.WriteString("Focus on syntax errors, formatting issues, and correctness problems.\n")
	}

	query.WriteString("\nImplement the fix using the provided tools efficiently.")

	if isRework {
		query.WriteString(" Ensure proper syntax, formatting, and address all review concerns.")
	}

	return query.String()
}
