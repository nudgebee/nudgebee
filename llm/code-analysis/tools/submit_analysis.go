package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"nudgebee/code-analysis-agent/tools/core"
)

type SubmitAnalysisTool struct{}

type CommitInfo struct {
	Hash    string `json:"hash"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Message string `json:"message,omitempty"`
	Changes string `json:"changes,omitempty"` // Git diff output from git show <commit> -- <file>
}

// Citation is a structured pointer to a specific code location that backs a
// claim in the answer. Explore-mode responses MUST include at least one
// citation per claim — markdown-embedded references in `description` are not
// a substitute (they're not machine-renderable as clickable links).
type Citation struct {
	FilePath  string `json:"file_path"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end,omitempty"` // defaults to LineStart when zero
	Snippet   string `json:"snippet"`            // the actual code lines being cited
	Note      string `json:"note,omitempty"`     // why this citation matters (1 line)
}

type SubmitAnalysisInput struct {
	// EXISTING FIELDS - Must maintain backward compatibility
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	FilePath     string         `json:"file_path,omitempty"`
	LineNumber   int            `json:"line_number,omitempty"`
	ErrorMessage string         `json:"error_message,omitempty"`
	OriginalCode string         `json:"original_code,omitempty"`
	FixedCode    string         `json:"fixed_code,omitempty"`
	CodeContext  string         `json:"code_context,omitempty"`
	GitDiff      string         `json:"git_diff,omitempty"`
	Commits      []CommitInfo   `json:"commits,omitempty"`
	PRList       []any          `json:"pr_list,omitempty"`
	PRInfo       map[string]any `json:"pr_info,omitempty"` // Information about created PR
	RequiresFix  bool           `json:"requires_fix,omitempty"`

	// EXPLORE-MODE STRUCTURED CONTRACT
	// Answer is the headline response in plain prose (no markdown). Required
	// in explore mode. Citations are the structured evidence — at least one
	// is required in explore mode. Caveats and FollowUpSuggestions are
	// optional context the LLM is encouraged to provide.
	Answer              string     `json:"answer,omitempty"`
	Citations           []Citation `json:"citations,omitempty"`
	Caveats             []string   `json:"caveats,omitempty"`
	FollowUpSuggestions []string   `json:"follow_up_suggestions,omitempty"`

	// NEW ENHANCEMENT FIELDS - Safe to add with omitempty
	ConfidenceScore    string   `json:"confidence_score,omitempty"`
	InvestigationTrail []string `json:"investigation_trail,omitempty"`
	RootCauseAnalysis  string   `json:"root_cause_analysis,omitempty"`
	AffectedComponents []string `json:"affected_components,omitempty"`
	RelatedIssues      []string `json:"related_issues,omitempty"`
	AlternativeFixes   []any    `json:"alternative_fixes,omitempty"`
	SemanticAnalysis   any      `json:"semantic_analysis,omitempty"`

	// IMPLEMENTATION INSTRUCTIONS - For RCA to CodeFixer handoff
	ImplementationInstructions []any `json:"implementation_instructions,omitempty"`

	// CODEFIXER EXECUTION FIELDS - For CodeFixer to report execution status
	ExecutionStatus     string   `json:"execution_status,omitempty"`     // "success" or "failed"
	ExecutionSummary    string   `json:"execution_summary,omitempty"`    // Brief summary of changes made
	FilesModified       []string `json:"files_modified,omitempty"`       // List of files that were changed
	VerificationPassed  bool     `json:"verification_passed,omitempty"`  // Whether syntax/build verification passed
	VerificationDetails string   `json:"verification_details,omitempty"` // Details of verification checks performed
}

// UnmarshalJSON handles the line_number field which can be either int or "N/A"
func (s *SubmitAnalysisInput) UnmarshalJSON(data []byte) error {
	// Define an auxiliary struct with string line_number for initial parsing
	type Aux struct {
		Title                      string         `json:"title"`
		Description                string         `json:"description"`
		FilePath                   string         `json:"file_path,omitempty"`
		LineNumber                 interface{}    `json:"line_number,omitempty"`
		ErrorMessage               string         `json:"error_message,omitempty"`
		OriginalCode               string         `json:"original_code,omitempty"`
		FixedCode                  string         `json:"fixed_code,omitempty"`
		CodeContext                string         `json:"code_context,omitempty"`
		GitDiff                    string         `json:"git_diff,omitempty"`
		Commits                    []CommitInfo   `json:"commits,omitempty"`
		PRList                     []any          `json:"pr_list,omitempty"`
		PRInfo                     map[string]any `json:"pr_info,omitempty"`
		RequiresFix                bool           `json:"requires_fix,omitempty"`
		ConfidenceScore            string         `json:"confidence_score,omitempty"`
		InvestigationTrail         []string       `json:"investigation_trail,omitempty"`
		RootCauseAnalysis          string         `json:"root_cause_analysis,omitempty"`
		AffectedComponents         []string       `json:"affected_components,omitempty"`
		SemanticAnalysis           any            `json:"semantic_analysis,omitempty"`
		ImplementationInstructions []any          `json:"implementation_instructions,omitempty"`
		ExecutionStatus            string         `json:"execution_status,omitempty"`
		ExecutionSummary           string         `json:"execution_summary,omitempty"`
		FilesModified              []string       `json:"files_modified,omitempty"`
		VerificationPassed         bool           `json:"verification_passed,omitempty"`
		VerificationDetails        string         `json:"verification_details,omitempty"`
		RelatedIssues              []string       `json:"related_issues,omitempty"`
		AlternativeFixes           []any          `json:"alternative_fixes,omitempty"`
		Answer                     string         `json:"answer,omitempty"`
		Citations                  []Citation     `json:"citations,omitempty"`
		Caveats                    []string       `json:"caveats,omitempty"`
		FollowUpSuggestions        []string       `json:"follow_up_suggestions,omitempty"`
	}

	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Copy all fields except LineNumber
	s.Title = aux.Title
	s.Description = aux.Description
	s.FilePath = aux.FilePath
	s.ErrorMessage = aux.ErrorMessage
	s.OriginalCode = aux.OriginalCode
	s.FixedCode = aux.FixedCode
	s.CodeContext = aux.CodeContext
	s.GitDiff = aux.GitDiff
	s.Commits = aux.Commits
	s.PRList = aux.PRList
	s.PRInfo = aux.PRInfo
	s.RequiresFix = aux.RequiresFix
	s.ConfidenceScore = aux.ConfidenceScore
	s.InvestigationTrail = aux.InvestigationTrail
	s.RootCauseAnalysis = aux.RootCauseAnalysis
	s.AffectedComponents = aux.AffectedComponents
	s.SemanticAnalysis = aux.SemanticAnalysis
	s.ImplementationInstructions = aux.ImplementationInstructions
	s.ExecutionStatus = aux.ExecutionStatus
	s.ExecutionSummary = aux.ExecutionSummary
	s.FilesModified = aux.FilesModified
	s.VerificationPassed = aux.VerificationPassed
	s.VerificationDetails = aux.VerificationDetails
	s.RelatedIssues = aux.RelatedIssues
	s.AlternativeFixes = aux.AlternativeFixes
	s.Answer = aux.Answer
	s.Citations = aux.Citations
	s.Caveats = aux.Caveats
	s.FollowUpSuggestions = aux.FollowUpSuggestions

	// Handle LineNumber specially - can be int, float64 (from JSON), or string
	switch v := aux.LineNumber.(type) {
	case int:
		s.LineNumber = v
	case float64:
		s.LineNumber = int(v)
	case string:
		if v == "N/A" || v == "" {
			s.LineNumber = 0 // Default to 0 for N/A
		} else if num, err := strconv.Atoi(v); err == nil {
			s.LineNumber = num
		} else {
			s.LineNumber = 0 // Default to 0 for unparseable strings
		}
	default:
		s.LineNumber = 0 // Default to 0 for other types
	}

	return nil
}

func NewSubmitAnalysisTool() *SubmitAnalysisTool {
	return &SubmitAnalysisTool{}
}

func (t *SubmitAnalysisTool) Name() string {
	return "submit_analysis"
}

func (t *SubmitAnalysisTool) Description() string {
	return "Submit your final analysis results. REQUIRED: Every analysis must end by calling this tool with all findings. This completes the analysis task."
}

func (t *SubmitAnalysisTool) InputSchema() core.ToolSchema {
	return core.CreateToolSchema(
		"object",
		"Submit final analysis with all findings and recommendations",
		map[string]any{
			"title": map[string]any{
				"type":        "string",
				"description": "Brief, clear title describing the issue or analysis",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Detailed explanation of findings, root cause, and solution",
			},
			"file_path": map[string]any{
				"type":        "string",
				"description": "Path to the relevant file (if applicable)",
			},
			"line_number": map[string]any{
				"type":        "integer",
				"description": "Specific line number (if applicable)",
			},
			"error_message": map[string]any{
				"type":        "string",
				"description": "The actual error message from logs (if applicable)",
			},
			"original_code": map[string]any{
				"type":        "string",
				"description": "The problematic code (if applicable)",
			},
			"fixed_code": map[string]any{
				"type":        "string",
				"description": "The corrected code (if applicable)",
			},
			"code_context": map[string]any{
				"type":        "string",
				"description": "Code context with ±50 lines around the error line with clear file and line labeling",
			},
			"git_diff": map[string]any{
				"type":        "string",
				"description": "Git diff format showing the proposed changes",
			},
			"commits": map[string]any{
				"type":        "array",
				"description": "List of related commits with their changes (from git blame and git show)",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"hash":    map[string]any{"type": "string", "description": "Commit hash"},
						"author":  map[string]any{"type": "string", "description": "Commit author"},
						"date":    map[string]any{"type": "string", "description": "Commit date"},
						"message": map[string]any{"type": "string", "description": "Commit message"},
						"changes": map[string]any{"type": "string", "description": "Summary of changes performed by this commit"},
					},
				},
			},
			"pr_list": map[string]any{
				"type":        "array",
				"description": "List of related Pull Requests with details like number, title, author, url, state, created_at, merged_at",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"number":     map[string]any{"type": "integer"},
						"title":      map[string]any{"type": "string"},
						"author":     map[string]any{"type": "string"},
						"url":        map[string]any{"type": "string"},
						"state":      map[string]any{"type": "string"},
						"created_at": map[string]any{"type": "string"},
						"merged_at":  map[string]any{"type": "string"},
					},
				},
			},
			"requires_fix": map[string]any{
				"type":        "boolean",
				"description": "Set to true if this is a bug analysis that needs a diff generated. Set to false for simple read-only queries.",
			},
			// NEW ENHANCEMENT FIELDS
			"confidence_score": map[string]any{
				"type":        "string",
				"description": "Confidence level: High, Medium, or Low based on available semantic analysis tools",
			},
			"investigation_trail": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Step-by-step summary of investigation process and evidence gathering",
			},
			"root_cause_analysis": map[string]any{
				"type":        "string",
				"description": "Deep analysis of the underlying root cause vs surface symptoms",
			},
			"affected_components": map[string]any{
				"type":        "array",
				"description": "List of components, functions, or services affected by this issue",
				"items":       map[string]any{"type": "string"},
			},
			"related_issues": map[string]any{
				"type":        "array",
				"description": "List of related issue IDs, error patterns, or similar problems",
				"items":       map[string]any{"type": "string"},
			},
			"alternative_fixes": map[string]any{
				"type":        "array",
				"description": "Multiple fix options with pros, cons, and complexity analysis",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"approach":   map[string]any{"type": "string", "description": "Fix approach name"},
						"code":       map[string]any{"type": "string", "description": "Code implementation"},
						"pros":       map[string]any{"type": "string", "description": "Advantages of this approach"},
						"cons":       map[string]any{"type": "string", "description": "Disadvantages or risks"},
						"complexity": map[string]any{"type": "string", "description": "Implementation complexity: Low, Medium, High"},
					},
				},
			},
			"semantic_analysis": map[string]any{
				"type":        "object",
				"description": "Results from semantic code analysis tools",
				"properties": map[string]any{
					"function_definition": map[string]any{"type": "string", "description": "Function signature and definition"},
					"call_hierarchy":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"dependencies":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
			},
			"implementation_instructions": map[string]any{
				"type":        "array",
				"description": "Array of step-by-step implementation instructions for CodeFixer agent",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"step":                 map[string]any{"type": "integer", "description": "Step number (1, 2, 3, etc.)"},
						"file_path":            map[string]any{"type": "string", "description": "Path to the file to modify"},
						"action":               map[string]any{"type": "string", "description": "Action: replace (surgical edit, requires old_string), write (create new file, new_string is full body), or verify (assert pattern)"},
						"old_string":           map[string]any{"type": "string", "description": "Optional: exact string to find and replace. For insertions, include the anchor line as old_string and new code + anchor as new_string. Omit if you cannot express the exact code — provide detailed purpose instead."},
						"new_string":           map[string]any{"type": "string", "description": "Optional: replacement string content. Omit if you cannot express the exact code — provide detailed purpose instead."},
						"verification_pattern": map[string]any{"type": "string", "description": "Pattern to verify (for verify action)"},
						"purpose":              map[string]any{"type": "string", "description": "REQUIRED: explanation of what to change and why. Must be detailed enough for CodeFixer to implement if old_string/new_string are omitted."},
					},
				},
			},
			// CODEFIXER EXECUTION FIELDS - For CodeFixer to report execution status
			"execution_status": map[string]any{
				"type":        "string",
				"description": "Execution status: 'success', 'partial_success', or 'failed' (CodeFixer mode only)",
				"enum":        []string{"success", "partial_success", "failed"},
			},
			"execution_summary": map[string]any{
				"type":        "string",
				"description": "Brief summary of what was executed or attempted (CodeFixer mode)",
			},
			"comment_responses": map[string]any{
				"type":        "array",
				"description": "Per-comment responses for PR followup mode. Each entry maps a review comment ID to an action and reply.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"comment_id": map[string]any{"type": "integer", "description": "The review comment ID"},
						"action":     map[string]any{"type": "string", "description": "Action taken: fixed, acknowledged, or wont_fix"},
						"reply":      map[string]any{"type": "string", "description": "Reply text for this comment"},
					},
				},
			},
			"files_modified": map[string]any{
				"type":        "array",
				"description": "List of files that were modified during execution (CodeFixer mode)",
				"items":       map[string]any{"type": "string"},
			},
			// EXPLORE-MODE CONTRACT - the orchestrator validates these fields are
			// populated and rejects empty submissions in explore mode, forcing the
			// LLM to retry with proper structured evidence.
			"answer": map[string]any{
				"type":        "string",
				"description": "REQUIRED in explore mode. The headline answer in plain prose (1–3 sentences). NO markdown, NO embedded `path:line` references — those go in citations[]. Example: 'The Bitnami postgres chart defaults max_connections to 100; the Nudgebee chart does not override it.'",
			},
			"citations": map[string]any{
				"type":        "array",
				"description": "REQUIRED in explore mode (at least one entry). Every claim in `answer` must be backed by a citation. Use this instead of inlining file/line references in answer markdown.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"file_path":  map[string]any{"type": "string", "description": "Repo-relative path"},
						"line_start": map[string]any{"type": "integer", "description": "Starting line number (1-indexed)"},
						"line_end":   map[string]any{"type": "integer", "description": "Ending line number (omit if single line)"},
						"snippet":    map[string]any{"type": "string", "description": "The actual code at those lines"},
						"note":       map[string]any{"type": "string", "description": "Optional 1-line explanation of why this citation matters"},
					},
				},
			},
			"caveats": map[string]any{
				"type":        "array",
				"description": "Optional. Things you couldn't verify, ambiguity in the user's question, or limitations of your investigation.",
				"items":       map[string]any{"type": "string"},
			},
			"follow_up_suggestions": map[string]any{
				"type":        "array",
				"description": "Optional. Natural next questions the user might ask, or escalations like 'open a PR raising max_connections to 500'. Useful for chat UI affordances.",
				"items":       map[string]any{"type": "string"},
			},
		},
		[]string{}, // No required fields - validation is flexible based on mode
	)
}

// modeContextKey is the context.Value key used to thread the request mode
// (explore | fix) from the orchestrator into submit_analysis without coupling
// the tool to the agents package.
type modeContextKey struct{}

// WithMode returns a context that carries the requested analysis mode. The
// orchestrator wraps the planner's context with this so submit_analysis can
// validate mode-specific contracts.
func WithMode(ctx context.Context, mode string) context.Context {
	return context.WithValue(ctx, modeContextKey{}, mode)
}

// ModeFromContext returns the mode set by WithMode, or "" if none. Exported so
// the planner can build a mode-aware goal/system prompt without coupling to the
// submit_analysis tool implementation.
func ModeFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if m, ok := ctx.Value(modeContextKey{}).(string); ok {
		return m
	}
	return ""
}

// truncate returns s clipped to at most n runes, ellipsised if it was longer.
// Used when explore-mode auto-fills title from answer. Operates on runes
// rather than bytes so multi-byte UTF-8 characters never get cut in half.
func truncate(s string, n int) string {
	if n <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 3 {
		return string(runes[:n])
	}
	return strings.TrimSpace(string(runes[:n-3])) + "..."
}

// validateExploreContract returns the list of validation errors for an
// explore-mode submission. Empty list means the submission is valid.
// The contract is intentionally minimal: a non-empty `answer` and at least
// one structurally complete `citation`. Everything else is optional.
func validateExploreContract(p SubmitAnalysisInput) []string {
	var errs []string
	if strings.TrimSpace(p.Answer) == "" {
		errs = append(errs, "explore mode requires `answer` to be a non-empty plain-prose response (1–3 sentences, no markdown).")
	}
	if len(p.Citations) == 0 {
		errs = append(errs, "explore mode requires at least one entry in `citations` — every claim in `answer` must be backed by a structured citation, not inlined as markdown.")
	}
	for i, c := range p.Citations {
		if strings.TrimSpace(c.FilePath) == "" {
			errs = append(errs, fmt.Sprintf("citations[%d].file_path is empty.", i))
		}
		if c.LineStart <= 0 {
			errs = append(errs, fmt.Sprintf("citations[%d].line_start must be a positive line number (got %d).", i, c.LineStart))
		}
		// line_end is optional (defaults to line_start when zero), but if the
		// LLM does provide it, the range must be coherent. Catching this
		// early keeps frontends from rendering a backwards highlight band.
		if c.LineEnd != 0 && c.LineEnd < c.LineStart {
			errs = append(errs, fmt.Sprintf("citations[%d].line_end (%d) must be >= line_start (%d).", i, c.LineEnd, c.LineStart))
		}
		if strings.TrimSpace(c.Snippet) == "" {
			errs = append(errs, fmt.Sprintf("citations[%d].snippet is empty — include the actual code lines you're citing.", i))
		}
	}
	return errs
}

func (t *SubmitAnalysisTool) Execute(ctx context.Context, input map[string]any) core.NBToolResponse {
	var params SubmitAnalysisInput
	if err := core.ParseInput(input, &params); err != nil {
		// Fallback: manually extract key fields from raw input map
		// This prevents discarding valid analysis when the LLM sends slightly malformed JSON
		params.Title = getString(input["title"])
		params.Description = getString(input["description"])
		params.ExecutionStatus = getString(input["execution_status"])
		params.ExecutionSummary = getString(input["execution_summary"])
		params.FilePath = getString(input["file_path"])
		params.ErrorMessage = getString(input["error_message"])
		params.RootCauseAnalysis = getString(input["root_cause_analysis"])
		params.ConfidenceScore = getString(input["confidence_score"])

		if params.Title == "" && params.Description == "" && params.ExecutionStatus == "" {
			return core.CreateErrorResponse(
				fmt.Sprintf("Invalid input parameters: %v", err),
				"Failed to parse analysis submission",
			)
		}
		// Continue with partially parsed data rather than discarding everything
	}

	// Handle backward compatibility - check for legacy "analysis" field
	if analysisValue, ok := input["analysis"].(string); ok && analysisValue != "" {
		if params.Title == "" {
			params.Title = "Analysis Result"
		}
		if params.Description == "" {
			params.Description = analysisValue
		}
	}

	// Handle error case - if agent submits an error object instead of analysis
	if errorValue, ok := input["error"].(string); ok && errorValue != "" {
		// Convert error to valid analysis format
		params.Title = "Analysis Error"
		params.Description = fmt.Sprintf("Analysis encountered an error: %s", errorValue)
		params.RequiresFix = false // Can't fix if we couldn't analyze
		params.ConfidenceScore = "Low"
	}

	// Flexible validation: Support Explore mode, ErrorRCA mode, CodeFixer mode, and PR Followup mode.
	_, hasCommentResponses := input["comment_responses"]
	isExploreMode := ModeFromContext(ctx) == "explore"
	isFollowupMode := hasCommentResponses
	isCodeFixerMode := params.ExecutionStatus != ""
	isErrorRCAMode := params.Title != "" || params.Description != ""

	if isExploreMode {
		// Explore mode owns its own contract: a structured `answer` and at
		// least one `citation`. Failures return a tool error so the ReAct
		// planner can retry the specialist with actionable feedback.
		if errs := validateExploreContract(params); len(errs) > 0 {
			return core.CreateErrorResponse(
				"Explore-mode submission failed contract validation:\n  - "+strings.Join(errs, "\n  - "),
				"Re-call submit_analysis with the corrections above. Required: a plain-prose `answer` (no markdown) AND at least one entry in `citations` with file_path + line_start + snippet.",
			)
		}
		// title/description are useful for legacy consumers (relevance check,
		// chat preview rendering, etc.). Derive them from `answer` if the LLM
		// didn't supply them — the answer IS the human-readable explanation.
		if params.Title == "" {
			params.Title = truncate(params.Answer, 80)
		}
		if params.Description == "" {
			params.Description = params.Answer
		}
	} else if isFollowupMode {
		// PR Followup mode: comment_responses is present, no strict requirements
		// The agent provides structured per-comment responses; title/summary are optional
	} else if isCodeFixerMode {
		// CodeFixer mode: Requires execution_status and execution_summary
		if params.ExecutionSummary == "" {
			return core.CreateErrorResponse("execution_summary is required", "Missing execution summary")
		}
		// Validate execution_status values
		if params.ExecutionStatus != "success" && params.ExecutionStatus != "partial_success" && params.ExecutionStatus != "failed" {
			return core.CreateErrorResponse("execution_status must be 'success', 'partial_success', or 'failed'", "Invalid execution status")
		}
	} else if isErrorRCAMode {
		// ErrorRCA mode: Requires title and description (original behavior)
		if params.Title == "" {
			return core.CreateErrorResponse("title is required", "Missing analysis title")
		}
		if params.Description == "" {
			return core.CreateErrorResponse("description is required", "Missing analysis description")
		}
	} else {
		// Neither mode detected - require at least basic fields
		return core.CreateErrorResponse("either (title + description) or (execution_status + execution_summary) required", "Missing required analysis fields")
	}

	// Auto-populate fields from tool outputs (structured memory approach)
	params = t.enhanceWithToolOutputs(input, params)

	// Normalize file paths to be relative to repository root
	params.FilePath = t.normalizeFilePath(params.FilePath)

	// Create the structured result - this is what gets extracted by the system
	result := map[string]any{
		// EXISTING FIELDS - Must maintain exact compatibility
		"title":         params.Title,
		"description":   params.Description,
		"file_path":     params.FilePath,
		"line_number":   params.LineNumber,
		"error_message": params.ErrorMessage,
		"original_code": params.OriginalCode,
		"fixed_code":    params.FixedCode,
		"code_context":  params.CodeContext,
		"git_diff":      params.GitDiff,
		"commits":       params.Commits,
		"pr_list":       params.PRList,
		"requires_fix":  params.RequiresFix,

		// NEW ENHANCEMENT FIELDS - Only include if not empty
		"confidence_score":    params.ConfidenceScore,
		"investigation_trail": params.InvestigationTrail,
		"root_cause_analysis": params.RootCauseAnalysis,
		"affected_components": params.AffectedComponents,
		"related_issues":      params.RelatedIssues,
		"alternative_fixes":   params.AlternativeFixes,
		"semantic_analysis":   params.SemanticAnalysis,

		// IMPLEMENTATION INSTRUCTIONS - Critical for RCA to CodeFixer handoff
		"implementation_instructions": params.ImplementationInstructions,

		// CODEFIXER EXECUTION FIELDS - For execution status reporting
		"execution_status":     params.ExecutionStatus,
		"execution_summary":    params.ExecutionSummary,
		"files_modified":       params.FilesModified,
		"verification_passed":  params.VerificationPassed,
		"verification_details": params.VerificationDetails,

		// EXPLORE-MODE STRUCTURED CONTRACT
		"answer":                params.Answer,
		"citations":             params.Citations,
		"caveats":               params.Caveats,
		"follow_up_suggestions": params.FollowUpSuggestions,
	}

	// Pass through extra fields from raw input that callers may need
	// (e.g., comment_responses from PRFollowupAgent)
	for _, key := range []string{"comment_responses"} {
		if v, ok := input[key]; ok && v != nil {
			result[key] = v
		}
	}

	// Create appropriate observation message based on mode
	var observation string
	if params.ExecutionStatus != "" {
		// CodeFixer mode
		observation = fmt.Sprintf("✅ Code execution %s: %s", params.ExecutionStatus, params.ExecutionSummary)
	} else {
		// ErrorRCA mode (original behavior)
		observation = fmt.Sprintf("✅ Analysis completed successfully: %s", params.Title)
	}

	return core.CreateSuccessResponse(
		"Analysis submitted and task completed",
		observation,
		result,
	)
}

func (t *SubmitAnalysisTool) GetType() core.NBToolType {
	return core.NBToolTypeCodeAnalysis
}

func (t *SubmitAnalysisTool) enhanceWithToolOutputs(input map[string]any, params SubmitAnalysisInput) SubmitAnalysisInput {
	// file_path
	if params.FilePath == "" {
		if v, ok := input["file_path"].(string); ok && v != "" {
			params.FilePath = v
		}
	}

	// line_number
	if params.LineNumber == 0 {
		if v, ok := input["line_number"].(float64); ok { // JSON numbers are float64
			params.LineNumber = int(v)
		}
	}

	// error_message
	if params.ErrorMessage == "" {
		if v, ok := input["error_message"].(string); ok && v != "" {
			params.ErrorMessage = v
		}
	}

	// original_code
	if params.OriginalCode == "" {
		if v, ok := input["original_code"].(string); ok && v != "" {
			params.OriginalCode = v
		}
	}

	// fixed_code
	if params.FixedCode == "" {
		if v, ok := input["fixed_code"].(string); ok && v != "" {
			params.FixedCode = v
		}
	}

	// code_context
	if params.CodeContext == "" {
		if v, ok := input["code_context"].(string); ok && v != "" {
			params.CodeContext = v
		}
	}

	// git_diff
	if params.GitDiff == "" {
		if v, ok := input["git_diff"].(string); ok && v != "" {
			params.GitDiff = v
		}
	}

	// commits - handle as array or construct from individual fields
	if len(params.Commits) == 0 {
		if commits, ok := input["commits"].([]any); ok {
			for _, commit := range commits {
				if commitMap, ok := commit.(map[string]any); ok {
					params.Commits = append(params.Commits, CommitInfo{
						Hash:    getString(commitMap["hash"]),
						Author:  getString(commitMap["author"]),
						Date:    getString(commitMap["date"]),
						Message: getString(commitMap["message"]),
						Changes: getString(commitMap["changes"]),
					})
				}
			}
		} else {
			// Fallback: try to construct from individual fields
			hash := getString(input["commit_hash"])
			author := getString(input["author"])
			date := getString(input["commit_date"])
			message := getString(input["commit_message"])
			changes := getString(input["commit_changes"])

			if hash != "" || author != "" || date != "" || message != "" || changes != "" {
				params.Commits = []CommitInfo{{
					Hash:    hash,
					Author:  author,
					Date:    date,
					Message: message,
					Changes: changes,
				}}
			}
		}
	}

	// pr_list - normalize any JSON strings into structured []any
	if len(params.PRList) == 0 {
		if raw, ok := input["pr_list"]; ok {
			switch v := raw.(type) {
			case string:
				// Try parsing string as JSON array
				var parsed []map[string]any
				if err := json.Unmarshal([]byte(v), &parsed); err == nil {
					for _, pr := range parsed {
						params.PRList = append(params.PRList, pr)
					}
				}
			case []any:
				params.PRList = v
			case []map[string]any:
				for _, pr := range v {
					params.PRList = append(params.PRList, pr)
				}
			}
		}
	}

	// Auto-populate pr_list if still empty but file_path and line_number exist
	// This ensures pr_list (blame history) is always populated when we have a file location
	if len(params.PRList) == 0 && params.FilePath != "" && params.LineNumber > 0 {
		// Get working directory from input (passed by planner/agent)
		workingDir := getString(input["working_directory"])
		if workingDir != "" {
			autoPopulated := t.autoPopulatePRList(context.Background(), params.FilePath, params.LineNumber, workingDir)
			if len(autoPopulated) > 0 {
				params.PRList = autoPopulated
			}
		}
	}

	// requires_fix - defaults to false if not specified
	if v, ok := input["requires_fix"].(bool); ok {
		params.RequiresFix = v
	}

	// NEW ENHANCEMENT FIELDS
	if params.ConfidenceScore == "" {
		if v, ok := input["confidence_score"].(string); ok && v != "" {
			params.ConfidenceScore = v
		}
	}

	if len(params.InvestigationTrail) == 0 {
		if v, ok := input["investigation_trail"].([]interface{}); ok && len(v) > 0 {
			// Convert []interface{} to []string
			for _, item := range v {
				if str, ok := item.(string); ok {
					params.InvestigationTrail = append(params.InvestigationTrail, str)
				}
			}
		}
	}

	if params.RootCauseAnalysis == "" {
		if v, ok := input["root_cause_analysis"].(string); ok && v != "" {
			params.RootCauseAnalysis = v
		}
	}

	// affected_components array
	if len(params.AffectedComponents) == 0 {
		if components, ok := input["affected_components"].([]any); ok {
			for _, comp := range components {
				if str, ok := comp.(string); ok {
					params.AffectedComponents = append(params.AffectedComponents, str)
				}
			}
		}
	}

	// related_issues array
	if len(params.RelatedIssues) == 0 {
		if issues, ok := input["related_issues"].([]any); ok {
			for _, issue := range issues {
				if str, ok := issue.(string); ok {
					params.RelatedIssues = append(params.RelatedIssues, str)
				}
			}
		}
	}

	// alternative_fixes array
	if len(params.AlternativeFixes) == 0 {
		if fixes, ok := input["alternative_fixes"].([]any); ok {
			params.AlternativeFixes = fixes
		}
	}

	// semantic_analysis object
	if params.SemanticAnalysis == nil {
		if semantic, ok := input["semantic_analysis"]; ok {
			params.SemanticAnalysis = semantic
		}
	}

	return params
}

// getString safely extracts string from interface{}
func getString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// normalizeFilePath removes temporary clone directory prefixes from file paths
func (t *SubmitAnalysisTool) normalizeFilePath(filePath string) string {
	if filePath == "" {
		return filePath
	}

	// Clean the path first
	cleaned := filepath.Clean(filePath)

	// Split the path into components
	pathParts := strings.Split(cleaned, string(filepath.Separator))

	// Look for temporary clone directory patterns (repo_XXXXX)
	for i, part := range pathParts {
		if strings.HasPrefix(part, "repo_") && len(part) > 5 {
			// Found temp repo directory, return everything after it
			if i+1 < len(pathParts) {
				return strings.Join(pathParts[i+1:], string(filepath.Separator))
			}
		}
	}

	// Also handle absolute paths that might contain temp directories
	if filepath.IsAbs(cleaned) {
		// For absolute paths, try to extract just the repository-relative part
		// Look for common patterns like /tmp/code-analysis/repo_XXXXX/...
		if strings.Contains(cleaned, "/tmp/") || strings.Contains(cleaned, "\\tmp\\") {
			// Find the last occurrence of a repo_ pattern
			for i := len(pathParts) - 1; i >= 0; i-- {
				if strings.HasPrefix(pathParts[i], "repo_") && len(pathParts[i]) > 5 {
					if i+1 < len(pathParts) {
						return strings.Join(pathParts[i+1:], string(filepath.Separator))
					}
				}
			}
		}
	}

	// If no temp directory pattern found, return the original path
	// but make it relative if it was absolute
	if filepath.IsAbs(cleaned) {
		// Try to make it relative by removing leading slash/drive
		if len(pathParts) > 0 && pathParts[0] == "" {
			// Unix-style absolute path
			return strings.Join(pathParts[1:], string(filepath.Separator))
		} else if len(pathParts) > 0 && strings.Contains(pathParts[0], ":") {
			// Windows-style absolute path with drive letter
			return strings.Join(pathParts[1:], string(filepath.Separator))
		}
	}

	return cleaned
}

// autoPopulatePRList fetches PR information using git blame and gh pr list
// when pr_list is empty but file_path and line_number are available
func (t *SubmitAnalysisTool) autoPopulatePRList(ctx context.Context, filePath string, lineNumber int, workingDir string) []any {
	if filePath == "" || lineNumber <= 0 || workingDir == "" {
		return nil
	}

	// Step 1: Run git blame to get the commit hash for the specific line
	commitHash := t.getCommitHashFromBlame(workingDir, filePath, lineNumber)
	if commitHash == "" {
		return nil
	}

	// Step 2: Run gh pr list to find PRs associated with the commit
	prList := t.getPRsForCommit(workingDir, commitHash)
	return prList
}

// getCommitHashFromBlame runs git blame and extracts the commit hash for a specific line
func (t *SubmitAnalysisTool) getCommitHashFromBlame(workingDir, filePath string, lineNumber int) string {
	// Run: git blame -L {lineNumber},{lineNumber} {filePath} --porcelain
	cmd := exec.Command("git", "blame", "-L", fmt.Sprintf("%d,%d", lineNumber, lineNumber), filePath, "--porcelain")
	cmd.Dir = workingDir

	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	// Parse porcelain output - first line contains the commit hash
	lines := strings.Split(string(output), "\n")
	if len(lines) == 0 {
		return ""
	}

	// First line format: <commit_hash> <original_line> <final_line> <num_lines>
	parts := strings.Fields(lines[0])
	if len(parts) > 0 {
		commitHash := parts[0]
		// Validate it looks like a commit hash (40 hex chars or abbreviated)
		if len(commitHash) >= 7 && isHexString(commitHash) {
			return commitHash
		}
	}

	return ""
}

// getPRsForCommit runs gh pr list to find PRs associated with a commit
func (t *SubmitAnalysisTool) getPRsForCommit(workingDir, commitHash string) []any {
	// Run: gh pr list --search "SHA:{commitHash}" --state all --json number,title,url,state,author,createdAt,mergedAt --limit 5
	cmd := exec.Command("gh", "pr", "list",
		"--search", fmt.Sprintf("SHA:%s", commitHash),
		"--state", "all",
		"--json", "number,title,url,state,author,createdAt,mergedAt",
		"--limit", "5",
	)
	cmd.Dir = workingDir

	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	// Parse JSON output
	var prs []map[string]any
	if err := json.Unmarshal(output, &prs); err != nil {
		return nil
	}

	// Convert to []any and normalize author field
	result := make([]any, 0, len(prs))
	for _, pr := range prs {
		normalizedPR := map[string]any{
			"number":     pr["number"],
			"title":      pr["title"],
			"url":        pr["url"],
			"state":      pr["state"],
			"created_at": pr["createdAt"],
			"merged_at":  pr["mergedAt"],
		}

		// Handle author which can be a string or object
		if author, ok := pr["author"].(map[string]any); ok {
			if login, ok := author["login"].(string); ok {
				normalizedPR["author"] = login
			} else if name, ok := author["name"].(string); ok {
				normalizedPR["author"] = name
			}
		} else if authorStr, ok := pr["author"].(string); ok {
			normalizedPR["author"] = authorStr
		}

		result = append(result, normalizedPR)
	}

	return result
}

// isHexString checks if a string contains only hexadecimal characters
func isHexString(s string) bool {
	matched, _ := regexp.MatchString("^[0-9a-fA-F]+$", s)
	return matched
}
