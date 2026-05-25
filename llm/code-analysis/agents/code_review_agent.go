package agents

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/config"
	"nudgebee/code-analysis-agent/internal/session"
	"nudgebee/code-analysis-agent/llm"
	"nudgebee/code-analysis-agent/tools"

	"github.com/tmc/langchaingo/llms"
)

// CodeReviewAgent reviews code changes and provides feedback
type CodeReviewAgent struct {
	llmClient    *llm.Client
	config       *config.Config
	logger       *common.Logger
	promptLoader *common.PromptLoader
	workspaceDir string
}

// ReviewResult represents the outcome of a code review
type ReviewResult struct {
	Approved         bool     `json:"approved"`
	Feedback         string   `json:"feedback"`
	Issues           []string `json:"issues"`
	Confidence       float64  `json:"confidence"`
	Attempt          int      `json:"attempt"`
	SyntaxErrors     []string `json:"syntax_errors,omitempty"`
	FormattingIssues []string `json:"formatting_issues,omitempty"`
	RequiresRevert   bool     `json:"requires_revert,omitempty"`
	BuildVerified    bool     `json:"build_verified,omitempty"`
}

// NewCodeReviewAgent creates a new code review agent
func NewCodeReviewAgent(cfg *config.Config, llmClient *llm.Client, logger *common.Logger, workspaceDir string) *CodeReviewAgent {
	return &CodeReviewAgent{
		llmClient:    llmClient,
		config:       cfg,
		logger:       logger,
		promptLoader: common.NewPromptLoader(),
		workspaceDir: workspaceDir,
	}
}

// Execute reviews code changes and provides feedback
func (a *CodeReviewAgent) Execute(ctx context.Context, sessionCtx *session.SessionContext,
	fixerResult map[string]any, gitDiff string, attempt int) (ReviewResult, error) {

	a.logger.Log(common.EventStepStart, "CodeReviewAgent reviewing changes", map[string]any{
		"attempt":     attempt,
		"diff_length": len(gitDiff),
	})

	// Get full file context for better review (AFTER state)
	fileContext := a.getFileContext(gitDiff)

	// Build BEFORE/AFTER context for intelligent comparison
	beforeAfterContext := a.buildBeforeAfterContext(gitDiff, fileContext)

	// Run basic linting on modified files
	lintIssues := a.runBasicLinting(gitDiff, fileContext)

	// ============================================
	// INTELLIGENT MULTI-PASS REVIEW APPROACH
	// ============================================

	// Pass 0: Build Verification Hard Gate
	// If the fixer ran build verification and it failed, reject immediately.
	// No point running LLM review on code that doesn't compile.
	if buildVerification, ok := fixerResult["build_verification"].(map[string]any); ok {
		if overallPassed, ok := buildVerification["overall_passed"].(bool); ok && !overallPassed {
			// Extract build error details for feedback
			buildDetails := "Build/lint verification failed."
			// Normalize steps to []map[string]any regardless of source
			var stepMaps []map[string]any
			if steps, ok := buildVerification["steps"].([]any); ok {
				for _, step := range steps {
					if sm, ok := step.(map[string]any); ok {
						stepMaps = append(stepMaps, sm)
					}
				}
			} else if steps, ok := buildVerification["steps"].([]map[string]any); ok {
				stepMaps = steps
			}
			for _, stepMap := range stepMaps {
				// Check both "status"=="failed" and "passed"==false patterns
				isFailed := false
				if status, ok := stepMap["status"].(string); ok && status == "failed" {
					isFailed = true
				} else if passed, ok := stepMap["passed"].(bool); ok && !passed {
					isFailed = true
				}
				if isFailed {
					stepName, _ := stepMap["command"].(string)
					stepError, _ := stepMap["error"].(string)
					if stepError == "" {
						stepError, _ = stepMap["output"].(string)
					}
					if stepName != "" {
						buildDetails += fmt.Sprintf("\n- Command `%s` failed: %s", stepName, stepError)
					}
				}
			}

			a.logger.Log(common.EventStepComplete, "Build verification hard-gate: REJECTED", map[string]any{
				"attempt": attempt,
				"details": buildDetails,
			})
			return ReviewResult{
				Approved:       false,
				Feedback:       "BUILD VERIFICATION FAILED — changes do not compile or pass linting.\n\n" + buildDetails,
				Attempt:        attempt,
				Confidence:     0.99,
				BuildVerified:  false,
				RequiresRevert: true,
			}, nil
		}
	} else if fixerResult != nil {
		// Build verification was not performed — log a warning
		// Check if cli tool was used at all (build commands would use cli)
		hasBuildStep := false
		if execSummary, ok := fixerResult["execution_summary"].(string); ok {
			buildKeywords := []string{"make ", "go build", "go vet", "npm run build", "npm run lint", "poetry run", "flake8", "golangci-lint"}
			for _, kw := range buildKeywords {
				if strings.Contains(execSummary, kw) {
					hasBuildStep = true
					break
				}
			}
		}
		if !hasBuildStep {
			a.logger.Log(common.EventStepStart, "WARNING: Build verification was not performed by fixer", map[string]any{
				"attempt": attempt,
			})
		}
	}

	// Pass 0.5: Target Correctness Check
	// Verify the modified file is the correct target for this fix.
	// This catches cases where RCA identified the wrong file (e.g., wrong Dockerfile, wrong service).
	a.logger.Log(common.EventStepStart, "Running target correctness check", map[string]any{"attempt": attempt})
	targetResult := a.performTargetCorrectnessCheck(ctx, sessionCtx, fixerResult, gitDiff)
	if !targetResult.Passed {
		a.logger.Log(common.EventStepComplete, "Target correctness check failed", map[string]any{
			"issues": targetResult.Issues,
		})
		return ReviewResult{
			Approved:       false,
			Feedback:       "TARGET CORRECTNESS CHECK FAILED — the modified file may not be the correct target for this fix.\n\n" + targetResult.Issues,
			Attempt:        attempt,
			Confidence:     0.90,
			RequiresRevert: true,
		}, nil
	}

	// Pass 1: Coherence Check (Most Important)
	// This pass shows LLM the BEFORE/AFTER state and asks it to verify coherence
	a.logger.Log(common.EventStepStart, "Running coherence check", map[string]any{"attempt": attempt})
	coherenceResult := a.performCoherenceCheck(ctx, beforeAfterContext, gitDiff)
	if !coherenceResult.Passed {
		a.logger.Log(common.EventStepComplete, "Coherence check failed", map[string]any{
			"issues": coherenceResult.Issues,
		})
		return ReviewResult{
			Approved:         false,
			Feedback:         "COHERENCE CHECK FAILED:\n" + coherenceResult.Issues,
			Attempt:          attempt,
			Confidence:       0.95,
			FormattingIssues: []string{coherenceResult.Issues},
		}, nil
	}

	// Pass 2: Variable scope analysis
	scopeResult := a.performScopeAnalysis(ctx, gitDiff, fileContext)
	if !scopeResult.Passed {
		return ReviewResult{
			Approved:     false,
			Feedback:     scopeResult.Issues,
			Attempt:      attempt,
			Confidence:   0.95,
			SyntaxErrors: []string{scopeResult.Issues},
		}, nil
	}

	// Pass 3: Syntax validation
	syntaxResult := a.performSyntaxAnalysis(ctx, gitDiff, fileContext)
	if !syntaxResult.Passed {
		return ReviewResult{
			Approved:         false,
			Feedback:         syntaxResult.Issues,
			Attempt:          attempt,
			Confidence:       0.95,
			FormattingIssues: []string{syntaxResult.Issues},
		}, nil
	}

	// Pass 4: Overall logic review with enhanced context
	reviewPrompt, err := a.buildReviewPromptWithBeforeAfter(sessionCtx, fixerResult, gitDiff, attempt, fileContext, lintIssues, beforeAfterContext)
	if err != nil {
		return ReviewResult{}, fmt.Errorf("failed to build review prompt: %w", err)
	}

	systemPrompt := a.buildSystemPrompt()
	messages := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: systemPrompt}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: reviewPrompt}}},
	}

	response, err := a.llmClient.GenerateContent(ctx, messages)
	if err != nil {
		return ReviewResult{}, fmt.Errorf("review execution failed: %w", err)
	}

	if len(response.Choices) == 0 {
		return ReviewResult{}, fmt.Errorf("no response from LLM")
	}

	// Parse review result
	reviewResult := a.parseReviewResult(response.Choices[0].Content, attempt)

	// Set build verification status from fixer result
	if buildVerification, ok := fixerResult["build_verification"].(map[string]any); ok {
		if overallPassed, ok := buildVerification["overall_passed"].(bool); ok {
			reviewResult.BuildVerified = overallPassed
		}
	}

	// Record review in scratchpad
	reviewNotes := fmt.Sprintf("Attempt %d Review:\nApproved: %t\nBuildVerified: %t\nFeedback: %s",
		attempt, reviewResult.Approved, reviewResult.BuildVerified, reviewResult.Feedback)
	sessionCtx.AddToScratchpad("CodeReviewAgent", reviewNotes)

	a.logger.Log(common.EventStepComplete, "CodeReviewAgent completed review", map[string]any{
		"approved":   reviewResult.Approved,
		"attempt":    attempt,
		"confidence": reviewResult.Confidence,
	})

	return reviewResult, nil
}

// buildReviewPromptWithBeforeAfter builds review prompt with BEFORE/AFTER context
func (a *CodeReviewAgent) buildReviewPromptWithBeforeAfter(sessionCtx *session.SessionContext,
	fixerResult map[string]any, gitDiff string, attempt int, fileContext map[string]string, lintIssues []string, beforeAfterContext string) (string, error) {

	// Extract key information
	originalQuery := sessionCtx.OriginalQuery
	investigationHistory := sessionCtx.GetScratchpad()

	// Extract ErrorRCA analysis from fixerResult
	issueTitle, _ := fixerResult["title"].(string)
	issueDescription, _ := fixerResult["description"].(string)
	rootCauseAnalysis, _ := fixerResult["root_cause_analysis"].(string)
	targetFilePath, _ := fixerResult["file_path"].(string)
	confidenceScore, _ := fixerResult["confidence_score"].(string)

	// Build lint issues section
	lintSection := ""
	if len(lintIssues) > 0 {
		lintSection = "\n\n## STATIC ANALYSIS FINDINGS:\n"
		for _, issue := range lintIssues {
			lintSection += fmt.Sprintf("- %s\n", issue)
		}
	}

	// Build verification section
	buildSection := ""
	if buildVerification, ok := fixerResult["build_verification"].(map[string]any); ok {
		overallPassed, _ := buildVerification["overall_passed"].(bool)
		if overallPassed {
			buildSection = "\n\n## BUILD VERIFICATION: PASSED\n"
		} else {
			buildSection = "\n\n## BUILD VERIFICATION: FAILED\n"
		}
		// Normalize steps to []map[string]any regardless of source type
		var stepMaps []map[string]any
		if steps, ok := buildVerification["steps"].([]map[string]any); ok {
			stepMaps = steps
		} else if stepsAny, ok := buildVerification["steps"].([]any); ok {
			for _, s := range stepsAny {
				if sm, ok := s.(map[string]any); ok {
					stepMaps = append(stepMaps, sm)
				}
			}
		}
		for _, step := range stepMaps {
			cmd, _ := step["command"].(string)
			// Derive status from either "status" field or "passed" bool
			status, _ := step["status"].(string)
			if status == "" {
				if passed, ok := step["passed"].(bool); ok && !passed {
					status = "failed"
				} else {
					status = "passed"
				}
			}
			buildSection += fmt.Sprintf("- `%s`: %s\n", cmd, status)
			if errMsg, ok := step["error"].(string); ok && errMsg != "" {
				buildSection += fmt.Sprintf("  Error: %s\n", errMsg)
			}
			if output, ok := step["output"].(string); ok && output != "" {
				buildSection += fmt.Sprintf("  Output: %s\n", output)
			}
		}
	}

	// Build comprehensive review query with BEFORE/AFTER context
	reviewQuery := fmt.Sprintf(`# CODE REVIEW REQUEST

## ISSUE BEING FIXED
**Title:** %s
**Confidence:** %s
**Target File:** %s

**Description:**
%s

**Root Cause Analysis:**
%s

## ORIGINAL REQUEST
%s

## INVESTIGATION HISTORY
%s

## BEFORE AND AFTER COMPARISON
%s

## GIT DIFF
%s
%s
%s

## REVIEW TASK

You have already passed the coherence check. Now do a final comprehensive review:

1. **CORRECTNESS**: Does the change fix the root cause identified above?
2. **COMPLETENESS**: Are all edge cases handled?
3. **QUALITY**: Is the code production-ready?
4. **SCOPE**: Are ALL changes in this diff related to the stated issue? If the diff modifies files or functions that are unrelated to the root cause, REJECT with "scope creep" and list the unrelated changes. A fix for one error should not bundle changes for other errors.
5. **DESCRIPTION ACCURACY**: Does the title and description accurately describe what the code change actually does? If the description says "add backoff logic" but the code just adds a log line, REJECT with "description mismatch".
6. **BUILD VERIFICATION**: If the BUILD VERIFICATION section above shows FAILED, you MUST reject. Broken builds should never be approved regardless of code quality.

## ATTEMPT NUMBER: %d

## DECISION REQUIRED

**DECISION:** APPROVED | REJECTED | REVERT

**REASONING:** [Your analysis]

**ISSUES (if any):** [Specific problems with line numbers]

**RECOMMENDATION:** [What to do next if rejected]`,
		issueTitle,
		confidenceScore,
		targetFilePath,
		issueDescription,
		rootCauseAnalysis,
		originalQuery,
		investigationHistory,
		beforeAfterContext,
		gitDiff,
		lintSection,
		buildSection,
		attempt)

	return reviewQuery, nil
}

func (a *CodeReviewAgent) buildSystemPrompt() string {
	return `You are an expert code reviewer. Your job is to catch ANY issue in the code change - not by pattern matching, but by THINKING through the code like a human expert would.

HOW TO REVIEW CODE (Your Mental Framework):

1. **UNDERSTAND THE INTENT**
   - What is this change trying to accomplish?
   - What was the original problem/bug?
   - Does the change address the root cause?

2. **VERIFY STRUCTURAL COHERENCE**
   - Compare BEFORE and AFTER code structure side by side
   - Is the AFTER code syntactically valid?
   - Is indentation consistent with surrounding code?
   - Are all blocks properly opened and closed?
   - If a block structure was changed (e.g., 'with' removed, 'try' removed):
     * Was the body properly adjusted (de-indented if needed)?
     * Are resources still properly managed?

3. **VERIFY LOGICAL COHERENCE**
   - Mentally trace through the code execution
   - Check if variables are defined before they're used
   - Check if the control flow makes sense
   - Look for duplicate or redundant code (same statement appearing multiple times)
   - Look for contradictory logic (checking the same condition twice, etc.)
   - Look for dead code or unreachable code paths

4. **VERIFY SEMANTIC COHERENCE**
   - Does the change actually fix the issue?
   - Does it introduce new problems?
   - Are edge cases handled (null, empty, boundary conditions)?
   - Are resources (files, connections, sessions) properly acquired and released?

5. **COMPARE BEFORE AND AFTER**
   - What exactly changed between BEFORE and AFTER?
   - Is every change intentional and correct?
   - Was anything accidentally broken or left in an inconsistent state?

THINK, DON'T PATTERN MATCH:
- Don't just check off a list of rules
- Actually read and understand the code
- If something looks wrong or feels off, investigate it
- Trust your expert judgment
- A good reviewer catches issues that surprise even the author

COMMON ISSUES TO WATCH FOR (but don't limit yourself to these):
- Indentation errors when code structure changes
- Duplicate statements (copy-paste errors)
- Redundant checks or contradictory logic
- Variables used before definition
- Resources not properly cleaned up
- Logic that doesn't match the stated intent
- Scope creep: changes to files/functions unrelated to the stated issue
- Description mismatch: title/description claims something the code doesn't actually do
- Build failures: if build verification failed, the change is not production-ready

DECISION CRITERIA:
- APPROVED: The change is correct, coherent, and production-ready
- REJECTED: There are fixable issues - explain clearly what's wrong and how to fix it
- REVERT: Fundamental problems - the approach is wrong, suggest starting over

RESPONSE FORMAT:
Be SPECIFIC with line numbers and clear explanations.
If rejecting, provide actionable guidance for the fix.
Focus on actual problems, not style preferences.

You are the LAST line of defense before code reaches production. Be thorough but fair.`
}

func (a *CodeReviewAgent) parseReviewResult(response string, attempt int) ReviewResult {
	response = strings.TrimSpace(response)

	result := ReviewResult{
		Attempt:    attempt,
		Confidence: 0.8, // Default confidence
	}

	// Check for approval/rejection/revert
	upperResponse := strings.ToUpper(response)
	if strings.Contains(upperResponse, "APPROVED") {
		result.Approved = true
		result.Confidence = 0.9
	} else if strings.Contains(upperResponse, "REVERT") {
		result.Approved = false
		result.RequiresRevert = true
		result.Confidence = 0.95
	} else if strings.Contains(upperResponse, "REJECTED") {
		result.Approved = false
		result.Confidence = 0.9
	} else {
		// Unclear response - default to rejected for safety
		result.Approved = false
		result.Confidence = 0.5
		result.Feedback = "Unclear review response: " + response
		return result
	}

	// Extract feedback (everything after APPROVED/REJECTED/REVERT)
	if colonIndex := strings.Index(response, ":"); colonIndex != -1 {
		result.Feedback = strings.TrimSpace(response[colonIndex+1:])
	} else {
		result.Feedback = response
	}

	// Extract specific issues for rejected changes
	if !result.Approved {
		result.Issues = a.extractIssues(result.Feedback)
		result.SyntaxErrors = a.extractSyntaxErrors(result.Feedback)
		result.FormattingIssues = a.extractFormattingIssues(result.Feedback)
	}

	return result
}

func (a *CodeReviewAgent) extractIssues(feedback string) []string {
	issues := []string{}

	// Common issue patterns
	issuePatterns := []string{
		// Correctness issues
		"wrong file", "incorrect file", "wrong location",
		"doesn't address", "doesn't fix", "not solved",
		"missing edge case", "edge case", "null check",
		"new bug", "introduces bug",

		// Efficiency issues
		"inefficient", "performance", "o(n²)", "unnecessary loop",
		"could be simpler", "overly complex", "redundant",
		"missing optimization", "cache", "memoization",

		// Quality issues
		"too broad", "unnecessary", "not minimal",
		"missing error", "error handling", "hardcoded",
		"unrelated change", "scope creep",
	}

	feedbackLower := strings.ToLower(feedback)
	for _, pattern := range issuePatterns {
		if strings.Contains(feedbackLower, pattern) {
			issues = append(issues, pattern)
		}
	}

	return issues
}

func (a *CodeReviewAgent) extractSyntaxErrors(feedback string) []string {
	syntaxErrors := []string{}

	// Syntax error patterns
	syntaxPatterns := []string{
		"syntax error", "missing bracket", "missing semicolon", "missing quote",
		"unclosed bracket", "unclosed parenthesis", "unclosed quote",
		"invalid syntax", "parse error", "compilation error",
		"missing comma", "unexpected token", "unexpected character",
		"indentation error", "missing colon", "undefined variable",
		"import error", "missing import", "circular import",
	}

	feedbackLower := strings.ToLower(feedback)
	for _, pattern := range syntaxPatterns {
		if strings.Contains(feedbackLower, pattern) {
			syntaxErrors = append(syntaxErrors, pattern)
		}
	}

	return syntaxErrors
}

func (a *CodeReviewAgent) extractFormattingIssues(feedback string) []string {
	formattingIssues := []string{}

	// Formatting issue patterns
	formattingPatterns := []string{
		"formatting issue", "indentation", "inconsistent spacing",
		"tabs vs spaces", "line ending", "trailing whitespace",
		"inconsistent indentation", "mixed indentation", "style violation",
		"code style", "formatting inconsistency", "whitespace issue",
		"line too long", "missing newline", "extra blank lines",
	}

	feedbackLower := strings.ToLower(feedback)
	for _, pattern := range formattingPatterns {
		if strings.Contains(feedbackLower, pattern) {
			formattingIssues = append(formattingIssues, pattern)
		}
	}

	return formattingIssues
}

// GetName returns the agent's name
func (a *CodeReviewAgent) GetName() string {
	return "code_review_agent"
}

// SetLogger sets the logger for the agent
func (a *CodeReviewAgent) SetLogger(logger *common.Logger) {
	a.logger = logger
}

// performTargetCorrectnessCheck verifies that the modified file is the correct target for the fix
func (a *CodeReviewAgent) performTargetCorrectnessCheck(ctx context.Context, sessionCtx *session.SessionContext, fixerResult map[string]any, gitDiff string) AnalysisResult {
	// Extract context about what was supposed to be fixed
	var issueTitle, issueDescription string
	if fixerResult != nil {
		issueTitle, _ = fixerResult["title"].(string)
		issueDescription, _ = fixerResult["description"].(string)
	}
	originalQuery := sessionCtx.OriginalQuery

	modifiedFiles := a.extractModifiedFiles(gitDiff)
	modifiedFilesList := strings.Join(modifiedFiles, ", ")

	targetPrompt := fmt.Sprintf(`You are a senior engineer reviewing whether the correct file was modified.

ORIGINAL REQUEST:
%s

ISSUE BEING FIXED:
Title: %s
Description: %s

FILES MODIFIED:
%s

GIT DIFF:
%s

YOUR TASK:
Determine whether the modified file(s) are the CORRECT target for this fix.

Think step by step:
1. What entity (service, component, image, package) does the issue describe?
2. Based on the file path(s) and diff content, do the modified files belong to that entity?
3. Are there any red flags suggesting the wrong file was modified? For example:
   - Issue mentions service X but the modified file belongs to service Y
   - Issue describes a Python service but the modified file is a Go/Node.js file
   - Issue references an image name but the modified Dockerfile builds a different image
   - The file path contains a different service/component name than what the issue describes

RESPOND WITH:
- "PASS: Target file is correct" if the modified files clearly belong to the entity described in the issue
- "FAIL: [Explanation of why the target appears wrong]" if there's a mismatch

Be decisive. Only fail if there's a clear mismatch between the issue's subject and the modified file(s).`,
		originalQuery, issueTitle, issueDescription, modifiedFilesList, gitDiff)

	messages := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: targetPrompt}}},
	}

	response, err := a.llmClient.GenerateContent(ctx, messages)
	if err != nil || len(response.Choices) == 0 {
		return AnalysisResult{Passed: true, Issues: ""} // Don't block on failure
	}

	result := strings.TrimSpace(response.Choices[0].Content)
	if strings.HasPrefix(strings.ToUpper(result), "PASS") {
		return AnalysisResult{Passed: true, Issues: ""}
	}

	return AnalysisResult{Passed: false, Issues: result}
}

// extractModifiedFiles extracts file paths from git diff
func (a *CodeReviewAgent) extractModifiedFiles(gitDiff string) []string {
	var files []string
	lines := strings.Split(gitDiff, "\n")

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") {
			// Extract file path from "diff --git a/path/to/file b/path/to/file"
			parts := strings.Split(line, " ")
			if len(parts) >= 4 {
				filePath := strings.TrimPrefix(parts[3], "b/")
				files = append(files, filePath)
			}
		}
	}

	return files
}

// getFileContext reads the full content of modified files for better review context
func (a *CodeReviewAgent) getFileContext(gitDiff string) map[string]string {
	fileContext := make(map[string]string)
	modifiedFiles := a.extractModifiedFiles(gitDiff)

	// Use repository helper to find the correct repository directory
	repoHelper := tools.NewRepositoryHelper()
	repoDir := repoHelper.FindRepositoryDirectoryFromBase(a.workspaceDir)

	for _, filePath := range modifiedFiles {
		fullPath := filepath.Join(repoDir, filePath)
		if content, err := os.ReadFile(fullPath); err == nil {
			fileContext[filePath] = string(content)
		} else {
			// If we can't read the file, note that in the context
			fileContext[filePath] = fmt.Sprintf("[Error reading file: %v]", err)
		}
	}

	return fileContext
}

// runBasicLinting performs basic static analysis on modified files
func (a *CodeReviewAgent) runBasicLinting(gitDiff string, fileContext map[string]string) []string {
	var lintIssues []string

	for filePath, content := range fileContext {
		// Skip error messages
		if strings.HasPrefix(content, "[Error reading file:") {
			continue
		}

		// Language-agnostic linting checks
		issues := a.performBasicLinting(filePath, content)
		lintIssues = append(lintIssues, issues...)
	}

	return lintIssues
}

// performBasicLinting performs basic language-agnostic static analysis
func (a *CodeReviewAgent) performBasicLinting(filePath, content string) []string {
	var issues []string
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		lineNum := i + 1

		// Check for mixed indentation (tabs and spaces)
		if strings.Contains(line, "\t") && strings.Contains(line, "  ") {
			issues = append(issues, fmt.Sprintf("%s:%d - Mixed tabs and spaces detected", filePath, lineNum))
		}

		// Check for trailing whitespace
		if len(line) > 0 && (strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t")) {
			issues = append(issues, fmt.Sprintf("%s:%d - Trailing whitespace detected", filePath, lineNum))
		}

		// Check for very long lines (>120 characters as a reasonable default)
		if len(line) > 120 {
			issues = append(issues, fmt.Sprintf("%s:%d - Line too long (%d characters)", filePath, lineNum, len(line)))
		}

		// Check for potential security issues
		if strings.Contains(strings.ToLower(line), "password") && (strings.Contains(line, "=") || strings.Contains(line, ":")) {
			issues = append(issues, fmt.Sprintf("%s:%d - Potential hardcoded password", filePath, lineNum))
		}

		if strings.Contains(strings.ToLower(line), "api_key") || strings.Contains(strings.ToLower(line), "secret") {
			issues = append(issues, fmt.Sprintf("%s:%d - Potential hardcoded secret/API key", filePath, lineNum))
		}

		// Check for common syntax issues (language-agnostic)
		openBrackets := strings.Count(line, "{") + strings.Count(line, "[") + strings.Count(line, "(")
		closeBrackets := strings.Count(line, "}") + strings.Count(line, "]") + strings.Count(line, ")")
		if openBrackets != closeBrackets && openBrackets > 0 && closeBrackets > 0 {
			issues = append(issues, fmt.Sprintf("%s:%d - Potential bracket mismatch", filePath, lineNum))
		}
	}

	return issues
}

// AnalysisResult represents the result of a focused analysis pass
type AnalysisResult struct {
	Passed bool
	Issues string
}

// performScopeAnalysis performs focused variable scope analysis
func (a *CodeReviewAgent) performScopeAnalysis(ctx context.Context, gitDiff string, fileContext map[string]string) AnalysisResult {
	scopePrompt := `You are a variable scope analyzer. Your ONLY job is to check if variables are used before they are defined.

ANALYZE THIS GIT DIFF for variable scope errors:
` + gitDiff + `

FILE CONTEXT (check variable declarations here):
` + a.buildFileContextForScope(fileContext) + `

For each line starting with '+' (new/modified lines):
1. Extract all variable names used in that line
2. Check if each variable is defined BEFORE that line in the FULL FILE CONTEXT above (including DECLARE blocks, function parameters, imports, etc.)
3. Only report "FAIL" if the variable is truly not defined anywhere in the file — do NOT report variables that are declared in a DECLARE block, function signature, or earlier in the file
4. If ANY variable is genuinely used before definition, return "FAIL: Variable 'name' used before definition"

If all variables are properly defined before use, return "PASS: Variable scope verified"`

	messages := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: scopePrompt}}},
	}

	response, err := a.llmClient.GenerateContent(ctx, messages)
	if err != nil || len(response.Choices) == 0 {
		return AnalysisResult{Passed: false, Issues: "Scope analysis failed"}
	}

	result := strings.TrimSpace(response.Choices[0].Content)
	if strings.HasPrefix(strings.ToUpper(result), "PASS") {
		return AnalysisResult{Passed: true, Issues: ""}
	}

	return AnalysisResult{Passed: false, Issues: result}
}

// performSyntaxAnalysis performs focused syntax validation
func (a *CodeReviewAgent) performSyntaxAnalysis(ctx context.Context, gitDiff string, fileContext map[string]string) AnalysisResult {
	syntaxPrompt := `You are a syntax validator. Your ONLY job is to check for syntax errors.

ANALYZE THIS GIT DIFF for syntax errors:
` + gitDiff + `

FILE CONTEXT (for indentation reference):
` + a.buildFileContextForSyntax(fileContext) + `

Check each line starting with '+' for:
1. Missing brackets: (), [], {}
2. Missing quotes: ", '
3. Missing commas in dictionaries/lists
4. Indentation inconsistency with surrounding code
5. Mixed tabs and spaces
6. Incorrect nesting levels

CRITICAL: Compare indentation of new lines (+) with surrounding context lines.
If indentation doesn't match the pattern, it's a syntax error.

If ANY syntax errors exist, return "FAIL: SyntaxError - [specific error]"
If syntax is correct, return "PASS: Syntax validated"`

	messages := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: syntaxPrompt}}},
	}

	response, err := a.llmClient.GenerateContent(ctx, messages)
	if err != nil || len(response.Choices) == 0 {
		return AnalysisResult{Passed: false, Issues: "Syntax analysis failed"}
	}

	result := strings.TrimSpace(response.Choices[0].Content)
	if strings.HasPrefix(strings.ToUpper(result), "PASS") {
		return AnalysisResult{Passed: true, Issues: ""}
	}

	return AnalysisResult{Passed: false, Issues: result}
}

// buildFileContextForScope builds file context for variable scope analysis.
// Shows more lines than syntax context since variable declarations can appear anywhere in the file.
func (a *CodeReviewAgent) buildFileContextForScope(fileContext map[string]string) string {
	if len(fileContext) == 0 {
		return ""
	}

	var contextBuilder strings.Builder
	for filePath, content := range fileContext {
		lines := strings.Split(content, "\n")
		maxLines := 150
		if len(lines) < maxLines {
			maxLines = len(lines)
		}

		fmt.Fprintf(&contextBuilder, "\n=== %s (first %d lines) ===\n", filePath, maxLines)
		for i := 0; i < maxLines; i++ {
			fmt.Fprintf(&contextBuilder, "%d: %s\n", i+1, lines[i])
		}
	}

	return contextBuilder.String()
}

// buildFileContextForSyntax builds focused context for syntax checking
func (a *CodeReviewAgent) buildFileContextForSyntax(fileContext map[string]string) string {
	if len(fileContext) == 0 {
		return ""
	}

	var contextBuilder strings.Builder
	for filePath, content := range fileContext {
		// Show first 50 lines for indentation pattern
		lines := strings.Split(content, "\n")
		maxLines := 50
		if len(lines) < maxLines {
			maxLines = len(lines)
		}

		fmt.Fprintf(&contextBuilder, "\n=== %s (first %d lines) ===\n", filePath, maxLines)
		for i := 0; i < maxLines; i++ {
			fmt.Fprintf(&contextBuilder, "%d: %s\n", i+1, lines[i])
		}
	}

	return contextBuilder.String()
}

// getBeforeContent retrieves file content before the current changes using git
func (a *CodeReviewAgent) getBeforeContent(filePath string) (string, error) {
	repoHelper := tools.NewRepositoryHelper()
	repoDir := repoHelper.FindRepositoryDirectoryFromBase(a.workspaceDir)

	// Use git show HEAD:filepath to get the content before changes
	cmd := exec.Command("git", "show", "HEAD:"+filePath)
	cmd.Dir = repoDir

	output, err := cmd.Output()
	if err != nil {
		// File might be new, return empty
		return "", nil
	}

	return string(output), nil
}

// buildBeforeAfterContext creates a clear BEFORE/AFTER comparison for LLM review
func (a *CodeReviewAgent) buildBeforeAfterContext(gitDiff string, fileContext map[string]string) string {
	var builder strings.Builder

	modifiedFiles := a.extractModifiedFiles(gitDiff)

	for _, filePath := range modifiedFiles {
		afterContent, hasAfter := fileContext[filePath]
		if !hasAfter || strings.HasPrefix(afterContent, "[Error reading file:") {
			continue
		}

		beforeContent, _ := a.getBeforeContent(filePath)

		// Extract the relevant section around changes
		changedLines := a.extractChangedLineNumbers(gitDiff, filePath)
		if len(changedLines) == 0 {
			continue
		}

		// Get context around the changes (50 lines before and after the changed region)
		minLine := changedLines[0]
		maxLine := changedLines[len(changedLines)-1]
		contextPadding := 30

		startLine := minLine - contextPadding
		if startLine < 1 {
			startLine = 1
		}
		endLine := maxLine + contextPadding

		fmt.Fprintf(&builder, "\n=== FILE: %s ===\n", filePath)

		// BEFORE section
		builder.WriteString("\n--- BEFORE (relevant section) ---\n")
		if beforeContent != "" {
			beforeSection := a.extractLineRange(beforeContent, startLine, endLine)
			builder.WriteString(beforeSection)
		} else {
			builder.WriteString("[New file - no previous content]\n")
		}

		// AFTER section
		builder.WriteString("\n--- AFTER (relevant section) ---\n")
		afterSection := a.extractLineRange(afterContent, startLine, endLine+10) // +10 to account for added lines
		builder.WriteString(afterSection)

		builder.WriteString("\n")
	}

	return builder.String()
}

// extractChangedLineNumbers extracts line numbers that were modified in the diff
func (a *CodeReviewAgent) extractChangedLineNumbers(gitDiff, targetFile string) []int {
	var lineNumbers []int

	lines := strings.Split(gitDiff, "\n")
	inTargetFile := false
	currentLine := 0

	// Regex to parse hunk header: @@ -oldStart,oldCount +newStart,newCount @@
	hunkRegex := regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

	for _, line := range lines {
		// Check if we're entering the target file's diff
		if strings.HasPrefix(line, "diff --git") {
			inTargetFile = strings.Contains(line, targetFile)
			continue
		}

		if !inTargetFile {
			continue
		}

		// Parse hunk header to get starting line number
		if matches := hunkRegex.FindStringSubmatch(line); len(matches) >= 2 {
			currentLine, _ = strconv.Atoi(matches[1])
			continue
		}

		// Track additions and context lines
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			lineNumbers = append(lineNumbers, currentLine)
			currentLine++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			// Removal - don't increment line number
		} else if strings.HasPrefix(line, " ") {
			// Context line
			currentLine++
		}
	}

	return lineNumbers
}

// extractLineRange extracts a range of lines from content with line numbers
func (a *CodeReviewAgent) extractLineRange(content string, startLine, endLine int) string {
	lines := strings.Split(content, "\n")
	var builder strings.Builder

	// Adjust for 0-based indexing
	start := startLine - 1
	end := endLine - 1

	if start < 0 {
		start = 0
	}
	if end >= len(lines) {
		end = len(lines) - 1
	}

	for i := start; i <= end && i < len(lines); i++ {
		fmt.Fprintf(&builder, "%4d: %s\n", i+1, lines[i])
	}

	return builder.String()
}

// performCoherenceCheck performs an intelligent coherence verification of the changes
func (a *CodeReviewAgent) performCoherenceCheck(ctx context.Context, beforeAfterContext string, gitDiff string) AnalysisResult {
	coherencePrompt := `You are an expert code reviewer. I will show you the BEFORE and AFTER state of code changes.

Your job is to verify the change is COHERENT - meaning it makes sense structurally and logically.

` + beforeAfterContext + `

GIT DIFF:
` + gitDiff + `

VERIFICATION STEPS (think step by step):

1. **STRUCTURAL COHERENCE**:
   - Compare the BEFORE and AFTER code structure
   - Is the AFTER code properly indented relative to surrounding code?
   - Are all blocks (if/for/while/try/with/function) properly opened and closed?
   - If a block was removed (like 'with' or 'try'), was the body properly de-indented?

2. **LOGICAL COHERENCE**:
   - Does the AFTER code make logical sense?
   - Are there any duplicate statements that shouldn't be there?
   - Are there any contradictory or redundant checks?
   - Is there any dead code or unreachable code?

3. **SEMANTIC COHERENCE**:
   - Does the change preserve the intended behavior?
   - Are variables still defined before they're used?
   - Are resources (files, connections, sessions) properly managed?

Think through each aspect carefully. If you find ANY issue, explain it clearly with line numbers.

RESPOND WITH:
- "PASS: Code is coherent" if the change is structurally and logically sound
- "FAIL: [Specific issue with line numbers]" if there are problems

Be thorough but fair - focus on actual problems, not style preferences.`

	messages := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: coherencePrompt}}},
	}

	response, err := a.llmClient.GenerateContent(ctx, messages)
	if err != nil || len(response.Choices) == 0 {
		return AnalysisResult{Passed: false, Issues: "Coherence check failed to execute"}
	}

	result := strings.TrimSpace(response.Choices[0].Content)
	if strings.HasPrefix(strings.ToUpper(result), "PASS") {
		return AnalysisResult{Passed: true, Issues: ""}
	}

	return AnalysisResult{Passed: false, Issues: result}
}
