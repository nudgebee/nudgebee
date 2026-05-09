package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"nudgebee/code-analysis-agent/tools/core"
)

// ReplaceTool is a tool for safely replacing text in a file.
type ReplaceTool struct {
	workspaceDir      string
	editCorrectionSvc *EditCorrectionService
	readTracker       *FileReadTracker
}

// SetReadTracker sets the shared read tracker for enforcing read-before-edit.
func (t *ReplaceTool) SetReadTracker(tracker *FileReadTracker) {
	t.readTracker = tracker
}

// NewReplaceTool creates a new ReplaceTool.
func NewReplaceTool() *ReplaceTool {
	return &ReplaceTool{}
}

// NewReplaceToolWithWorkspace creates a new ReplaceTool with workspace context.
func NewReplaceToolWithWorkspace(workspaceDir string) *ReplaceTool {
	return &ReplaceTool{
		workspaceDir: workspaceDir,
	}
}

// SetEditCorrectionService sets the LLM-based edit correction service for self-healing on failed replacements.
func (t *ReplaceTool) SetEditCorrectionService(svc *EditCorrectionService) {
	t.editCorrectionSvc = svc
}

// Name returns the name of the tool.
func (t *ReplaceTool) Name() string {
	return "replace"
}

// Description returns a description of the tool.
func (t *ReplaceTool) Description() string {
	return `Modifies file content using exact string matching. REQUIRES old_string to uniquely identify the code to change.

RECOMMENDED WORKFLOW:
1. Use file_view to READ the file and find the exact code to change
2. Use replace with old_string (exact text from file) and new_string (replacement)

OPERATION MODES:

1. **STRING REPLACEMENT** (Primary - Most Reliable)
   Replace exact string match. The old_string must uniquely identify the location.
   Include 2-3 lines of surrounding context in old_string to ensure uniqueness.

   Example - Fix function signature:
   {"file_path": "api.py", "old_string": "def calculate(self, data):", "new_string": "def calculate(self, account_id, data):"}

   Example - Multi-line replacement:
   {"file_path": "utils.py", "old_string": "    result = compute(x)\n    return result", "new_string": "    result = compute(x, validate=True)\n    if result is None:\n        raise ValueError('computation failed')\n    return result"}

2. **INSERT BEFORE** (Adding Code Before a Line)
   Include the target line in old_string, and prepend new code in new_string:
   {"file_path": "app.py", "old_string": "import os", "new_string": "import logging\nimport os"}

3. **INSERT AFTER** (Adding Code After a Line)
   Include the target line in old_string, and append new code in new_string:
   {"file_path": "task.py", "old_string": "content[\"channels\"] = get_channels()", "new_string": "content[\"channels\"] = get_channels()\ncontent[\"account_id\"] = str(self.account_id)"}

4. **REPLACE ALL** (Multiple Occurrences)
   Replace all occurrences of a string across the file:
   {"file_path": "app.py", "old_string": "old_func()", "new_string": "new_func()", "replace_all": true}

5. **VERIFY** (Check Code Exists)
   Verify that a pattern exists before attempting modifications.
   {"file_path": "test.py", "action": "verify", "verification_pattern": "def test_"}

MATCHING STRATEGY (automatic fallback):
1. Exact match — literal old_string found in file
2. Flexible match — strips whitespace, compares trimmed content
3. Regex match — tokenizes old_string, matches with flexible whitespace
4. LLM self-correction — asks LLM to find the correct string if all above fail

PARAMETERS:
  file_path (required):        File to modify
  old_string (required):       Exact string to find and replace. Must be verbatim from the file.
                               Include 2-3 lines of context to ensure uniqueness.
  new_string (required):       Replacement text. For insertions, include the original line plus new code.
  replace_all:                 Replace all occurrences (default: false)
  action:                      'replace' (default) or 'verify'
  verification_pattern:        Pattern for verify action

IMPORTANT:
- ALWAYS use file_view first to find the exact code
- Include enough context in old_string (2-3 surrounding lines) to make it unique
- For insertions: include the anchor line in BOTH old_string and new_string
- Each edit reads fresh file content — safe for multi-step edits
- Default is single replacement (errors if multiple matches found)

PITFALLS TO AVOID:
- Using too little context in old_string (causes ambiguous matches)
- Not re-reading the file after previous edits (content may have changed)
- Replacing imports without checking usage (tool blocks this automatically)`
}

// InputSchema returns the input schema for the tool.
func (t *ReplaceTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: "object",
		Properties: map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The relative path to the file to modify (relative to workspace).",
			},
			"action": map[string]any{
				"type":        "string",
				"description": "The action to perform: 'replace' (default) or 'verify'",
				"enum":        []string{"replace", "verify"},
			},
			"old_string": map[string]any{
				"type":        "string",
				"description": "The exact string or code block to find and replace. Must be a verbatim copy from the file. Include 2-3 lines of surrounding context to ensure uniqueness. Required for 'replace' action.",
			},
			"new_string": map[string]any{
				"type":        "string",
				"description": "The new string or code block to replace old_string with. For insertions, include the original anchor line plus the new code. Required for 'replace' action.",
			},
			"verification_pattern": map[string]any{
				"type":        "string",
				"description": "Pattern to search for verification. Required for 'verify' action.",
			},
			"replace_all": map[string]any{
				"type":        "boolean",
				"description": "Set to true to replace all occurrences of old_string. Defaults to false, which enforces a single, unique match.",
			},
			"purpose": map[string]any{
				"type":        "string",
				"description": "Semantic description of what this change accomplishes. Used for intelligent error recovery and self-healing. Examples: 'Add error handling after database call', 'Insert new import before existing imports', 'Replace deprecated function call'.",
			},
		},
		Required: []string{"file_path"},
	}
}

// GetType returns the type of the tool.
func (t *ReplaceTool) GetType() core.NBToolType {
	return core.NBToolTypeCodeAnalysis
}

// Execute executes the tool with safety checks.
func (t *ReplaceTool) Execute(ctx context.Context, input map[string]any) core.NBToolResponse {
	filePath, _ := input["file_path"].(string)
	action, _ := input["action"].(string)

	if filePath == "" {
		return core.NBToolResponse{Status: "error", Error: "file_path is required"}
	}

	// Use working directory from orchestrator, fallback to tool workspace
	repoDir := t.workspaceDir
	if workingDir, ok := input["working_directory"].(string); ok && workingDir != "" {
		repoDir = workingDir
	}

	// Default action is replace
	if action == "" {
		action = "replace"
	}

	// Resolve file path relative to repository directory
	if !filepath.IsAbs(filePath) {
		// Remove repository name prefix if it exists to avoid duplication
		repoName := filepath.Base(repoDir)
		if strings.HasPrefix(filePath, repoName+"/") {
			filePath = strings.TrimPrefix(filePath, repoName+"/")
		}
		// First try the direct path
		directPath := filepath.Join(repoDir, filePath)
		if _, err := os.Stat(directPath); err == nil {
			filePath = directPath
		} else {
			// If direct path fails, search for the file recursively
			foundPath := t.searchForFile(repoDir, filePath)
			if foundPath != "" {
				filePath = foundPath
			} else {
				filePath = directPath // Keep original for error reporting
			}
		}
	}

	// Dispatch to appropriate action handler
	switch action {
	case "verify":
		return t.executeVerify(filePath, input)
	case "replace":
		return t.executeReplace(ctx, filePath, input)
	default:
		return core.NBToolResponse{Status: "error", Error: fmt.Sprintf("Unknown action: %s. Supported actions: replace, verify", action)}
	}
}

// executeReplace handles the replace functionality using string-based matching
func (t *ReplaceTool) executeReplace(ctx context.Context, filePath string, input map[string]any) core.NBToolResponse {
	// Read-before-edit gate: reject edits on files not previously read via file_view
	if t.readTracker != nil && !t.readTracker.WasRead(filePath) {
		return core.NBToolResponse{
			Status: "error",
			Error:  fmt.Sprintf("Cannot edit '%s': file has not been read yet. Use file_view to read the file first, then retry the replacement.", filePath),
		}
	}

	oldString, _ := input["old_string"].(string)
	newString, _ := input["new_string"].(string)
	replaceAll, _ := input["replace_all"].(bool)
	purpose, _ := input["purpose"].(string)

	if oldString == "" {
		return core.NBToolResponse{Status: "error", Error: "old_string is required. Use file_view to find the exact code, then provide it as old_string."}
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return core.NBToolResponse{Status: "error", Error: fmt.Sprintf("failed to read file '%s': %v", filePath, err)}
	}
	fileContent := string(content)

	// INTELLIGENT VALIDATION AND PATTERN DETECTION
	validationResult := t.validateReplacement(filePath, fileContent, oldString, newString)
	if validationResult.Status == "error" {
		return validationResult
	}
	if validationResult.Status == "suggestion" {
		// Return suggestion but allow proceeding if user wants
		validationResult.Status = "warning"
	}

	// SELF-HEALING: Anchor preservation detection (catches PR 23575 pattern)
	// Skip for replace_all since user explicitly wants to replace all occurrences
	anchorCorrected := false
	if !replaceAll {
		correctedNewString, anchorWarning := t.checkAnchorPreservation(oldString, newString, purpose)
		if anchorWarning != "" {
			// No purpose or unclear intent — return error to prompt LLM to reconsider
			return core.NBToolResponse{Status: "error", Error: anchorWarning}
		}
		if correctedNewString != "" {
			newString = correctedNewString
			anchorCorrected = true
		}
	}

	// Strategy 1: Exact Match (current behavior)
	count := strings.Count(fileContent, oldString)
	if count > 0 {
		if !replaceAll && count > 1 {
			// Find the line numbers where matches occur for better guidance
			lines := strings.Split(fileContent, "\n")
			matchLines := []int{}
			for i, line := range lines {
				if strings.Contains(line, oldString) {
					matchLines = append(matchLines, i+1)
				}
			}
			matchLinesStr := ""
			if len(matchLines) > 0 {
				matchLinesStr = fmt.Sprintf(" Found on lines: %v.", matchLines)
			}
			return core.NBToolResponse{Status: "error", Error: fmt.Sprintf("Ambiguous match: 'old_string' found %d times in %s.%s\n\nSOLUTIONS:\n1. Add more surrounding context lines to old_string to make it unique (include 2-3 lines before/after)\n2. Set 'replace_all': true if you want to replace all occurrences\n\nUse file_view to read the surrounding context and include more lines in old_string.", count, filePath, matchLinesStr)}
		}
		if replaceAll {
			newContent := strings.ReplaceAll(fileContent, oldString, newString)
			if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
				return core.NBToolResponse{Status: "error", Error: fmt.Sprintf("failed to write to file '%s': %v", filePath, err)}
			}
			return core.NBToolResponse{Status: "success", Observation: fmt.Sprintf("Successfully replaced all %d instances in %s. (strategy: exact)", count, filePath)}
		}
		// Compute match line for post-replace checks
		matchIdx := strings.Index(fileContent, oldString)
		matchLine := strings.Count(fileContent[:matchIdx], "\n") + 1

		// Post-replace validation
		postWarnings := t.postReplaceCheck(fileContent, oldString, newString, matchLine)

		newContent := strings.Replace(fileContent, oldString, newString, 1)
		if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
			return core.NBToolResponse{Status: "error", Error: fmt.Sprintf("failed to write to file '%s': %v", filePath, err)}
		}

		obs := fmt.Sprintf("Successfully made a single replacement in %s. (strategy: exact)", filePath)
		if anchorCorrected {
			obs += "\nNote: Auto-corrected to preserve anchor line during insertion (purpose indicated insert intent)."
		}
		if postWarnings != "" {
			obs += "\n" + postWarnings
		}
		return core.NBToolResponse{Status: "success", Observation: obs}
	}

	// Strategy 2: Flexible Match — strip whitespace, match content, reapply indentation
	if newContent, matchLine, ok := t.flexibleMatch(fileContent, oldString, newString); ok {
		// Post-replace validation
		postWarnings := t.postReplaceCheck(fileContent, oldString, newString, matchLine)

		if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
			return core.NBToolResponse{Status: "error", Error: fmt.Sprintf("failed to write to file '%s': %v", filePath, err)}
		}

		obs := fmt.Sprintf("Successfully made replacement in %s near line %d. (strategy: flexible — matched by content after ignoring whitespace differences)", filePath, matchLine)
		if anchorCorrected {
			obs += "\nNote: Auto-corrected to preserve anchor line during insertion (purpose indicated insert intent)."
		}
		if postWarnings != "" {
			obs += "\n" + postWarnings
		}
		return core.NBToolResponse{Status: "success", Observation: obs}
	}

	// Strategy 3: Regex Match — tokenize and match with flexible whitespace
	if newContent, matchLine, ok := t.regexMatch(fileContent, oldString, newString); ok {
		// Post-replace validation
		postWarnings := t.postReplaceCheck(fileContent, oldString, newString, matchLine)

		if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
			return core.NBToolResponse{Status: "error", Error: fmt.Sprintf("failed to write to file '%s': %v", filePath, err)}
		}

		obs := fmt.Sprintf("Successfully made replacement in %s near line %d. (strategy: regex — matched by tokenized content with flexible whitespace)", filePath, matchLine)
		if anchorCorrected {
			obs += "\nNote: Auto-corrected to preserve anchor line during insertion (purpose indicated insert intent)."
		}
		if postWarnings != "" {
			obs += "\n" + postWarnings
		}
		return core.NBToolResponse{Status: "success", Observation: obs}
	}

	// Strategy 4: LLM Self-Correction — ask LLM to fix the search string
	if t.editCorrectionSvc != nil {
		llmPurpose := purpose
		if llmPurpose == "" {
			llmPurpose = "Replace code in file"
		}
		errorMsg := fmt.Sprintf("old_string not found in %s after exact, flexible, and regex matching", filePath)

		correction, err := t.editCorrectionSvc.FixFailedEdit(ctx, llmPurpose, oldString, newString, errorMsg, fileContent)
		if err == nil && correction != nil {
			if correction.NoChangesRequired {
				return core.NBToolResponse{Status: "success", Observation: fmt.Sprintf("No changes required in %s: %s (strategy: llm-correction)", filePath, correction.Explanation)}
			}

			// Retry with corrected search string — try exact match first
			correctedCount := strings.Count(fileContent, correction.Search)
			if correctedCount == 1 {
				correctedReplace := correction.Replace
				if correctedReplace == "" {
					correctedReplace = newString // Use original if LLM didn't change it
				}

				// Compute match line for post-replace checks
				matchIdx := strings.Index(fileContent, correction.Search)
				matchLine := strings.Count(fileContent[:matchIdx], "\n") + 1
				postWarnings := t.postReplaceCheck(fileContent, correction.Search, correctedReplace, matchLine)

				newContent := strings.Replace(fileContent, correction.Search, correctedReplace, 1)
				if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
					return core.NBToolResponse{Status: "error", Error: fmt.Sprintf("failed to write to file '%s': %v", filePath, err)}
				}

				obs := fmt.Sprintf("Successfully made replacement in %s. (strategy: llm-correction — %s)", filePath, correction.Explanation)
				if anchorCorrected {
					obs += "\nNote: Auto-corrected to preserve anchor line during insertion (purpose indicated insert intent)."
				}
				if postWarnings != "" {
					obs += "\n" + postWarnings
				}
				return core.NBToolResponse{Status: "success", Observation: obs}
			}
			// If corrected string also doesn't match exactly, try flexible match with it
			if correctedContent, matchLine, ok := t.flexibleMatch(fileContent, correction.Search, newString); ok {
				// Post-replace validation
				postWarnings := t.postReplaceCheck(fileContent, correction.Search, newString, matchLine)

				if err := os.WriteFile(filePath, []byte(correctedContent), 0644); err != nil {
					return core.NBToolResponse{Status: "error", Error: fmt.Sprintf("failed to write to file '%s': %v", filePath, err)}
				}

				obs := fmt.Sprintf("Successfully made replacement in %s near line %d. (strategy: llm-correction+flexible — %s)", filePath, matchLine, correction.Explanation)
				if anchorCorrected {
					obs += "\nNote: Auto-corrected to preserve anchor line during insertion (purpose indicated insert intent)."
				}
				if postWarnings != "" {
					obs += "\n" + postWarnings
				}
				return core.NBToolResponse{Status: "success", Observation: obs}
			}
		}
	}

	// All strategies failed — provide closest matches so the LLM can self-correct
	closestMatches := t.findClosestMatches(fileContent, oldString, 3)
	hint := ""
	if len(closestMatches) > 0 {
		var hintBuilder strings.Builder
		hintBuilder.WriteString("\n\nClosest matches in file:")
		for _, m := range closestMatches {
			fmt.Fprintf(&hintBuilder, "\n  Line %d: %s", m.lineNum, truncateStr(m.content, 120))
		}
		hintBuilder.WriteString("\n\nUse the exact text from one of these lines as old_string, or include more surrounding context.")
		hint = hintBuilder.String()
	}
	return core.NBToolResponse{Status: "error", Error: fmt.Sprintf("The 'old_string' was not found in %s after trying exact, flexible, regex, and LLM self-correction strategies.%s", filePath, hint)}
}

// flexibleMatch tries to match old_string against fileContent by stripping whitespace from both sides.
// When a match is found, it detects the indentation of the first matched line in the file
// and applies that indentation to all lines of new_string.
// Returns: (newFileContent, matchLineNumber, success)
func (t *ReplaceTool) flexibleMatch(fileContent, oldString, newString string) (string, int, bool) {
	fileLines := strings.Split(fileContent, "\n")
	searchLines := strings.Split(oldString, "\n")

	// Strip trailing empty lines from search
	for len(searchLines) > 0 && strings.TrimSpace(searchLines[len(searchLines)-1]) == "" {
		searchLines = searchLines[:len(searchLines)-1]
	}

	if len(searchLines) == 0 {
		return "", 0, false
	}

	// Normalize search lines for comparison (collapse internal whitespace)
	searchLinesNormalized := make([]string, len(searchLines))
	for i, line := range searchLines {
		searchLinesNormalized[i] = normalizeWhitespace(line)
	}

	// Slide a window over fileLines looking for a content match
	matchCount := 0
	var matchStart int
	for i := 0; i <= len(fileLines)-len(searchLines); i++ {
		matched := true
		for j := 0; j < len(searchLines); j++ {
			if normalizeWhitespace(fileLines[i+j]) != searchLinesNormalized[j] {
				matched = false
				break
			}
		}
		if matched {
			matchCount++
			matchStart = i
			if matchCount > 1 {
				// Ambiguous — multiple flexible matches found, bail out
				return "", 0, false
			}
		}
	}

	if matchCount != 1 {
		return "", 0, false
	}

	// Detect indentation from the first matched line in the file
	firstMatchedLine := fileLines[matchStart]
	indentation := ""
	for _, ch := range firstMatchedLine {
		if ch == ' ' || ch == '\t' {
			indentation += string(ch)
		} else {
			break
		}
	}
	fileUsesTabs := len(indentation) > 0 && indentation[0] == '\t'

	// Apply detected indentation to new_string lines.
	// Always use new_string's own relative indentation — the LLM expresses
	// the intended structure in new_string, and 1:1 line mapping with old_string
	// breaks when line counts differ.
	replaceLines := strings.Split(newString, "\n")
	adjustedLines := make([]string, len(replaceLines))
	newFirstWidth := leadingWhitespaceWidth(replaceLines[0])
	for i, line := range replaceLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			adjustedLines[i] = ""
		} else if i == 0 {
			// First line gets the detected indentation
			adjustedLines[i] = indentation + trimmed
		} else {
			// Subsequent lines: compute relative indent from new_string itself
			newThisWidth := leadingWhitespaceWidth(line)
			extraWidth := newThisWidth - newFirstWidth
			relativeIndent := ""
			if extraWidth > 0 {
				relativeIndent = indentString(extraWidth, fileUsesTabs)
			}
			adjustedLines[i] = indentation + relativeIndent + trimmed
		}
	}

	// Build new file content by replacing the matched range
	result := make([]string, 0, len(fileLines)-len(searchLines)+len(adjustedLines))
	result = append(result, fileLines[:matchStart]...)
	result = append(result, adjustedLines...)
	result = append(result, fileLines[matchStart+len(searchLines):]...)

	return strings.Join(result, "\n"), matchStart + 1, true
}

// regexMatch tries to match old_string by tokenizing it and building a regex with flexible whitespace.
// Returns: (newFileContent, matchLineNumber, success)
func (t *ReplaceTool) regexMatch(fileContent, oldString, newString string) (string, int, bool) {
	// Tokenize old_string by splitting on common delimiters
	delimiters := regexp.MustCompile(`([\s\(\)\:\[\]\{\}\>\<\=\,\;]+)`)
	tokens := delimiters.Split(oldString, -1)

	// Filter out empty tokens
	var nonEmptyTokens []string
	for _, token := range tokens {
		trimmed := strings.TrimSpace(token)
		if trimmed != "" {
			nonEmptyTokens = append(nonEmptyTokens, trimmed)
		}
	}

	if len(nonEmptyTokens) < 2 {
		// Too few tokens for meaningful regex matching
		return "", 0, false
	}

	// Build regex: escape each token, join with \s* for flexible whitespace
	var regexParts []string
	for _, token := range nonEmptyTokens {
		regexParts = append(regexParts, regexp.QuoteMeta(token))
	}

	// Prepend ^(\s*) to capture leading indentation (multiline)
	pattern := `(?m)^([ \t]*)` + strings.Join(regexParts, `[\s\(\)\:\[\]\{\}\>\<\=\,\;]*`)

	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", 0, false
	}

	matches := re.FindAllStringIndex(fileContent, -1)
	if len(matches) != 1 {
		// Must be exactly one match to avoid ambiguity
		return "", 0, false
	}

	matchStart := matches[0][0]
	matchEnd := matches[0][1]

	// Convert byte offsets to line numbers for line-based replacement
	matchStartLine := strings.Count(fileContent[:matchStart], "\n")
	matchEndLine := strings.Count(fileContent[:matchEnd], "\n")

	fileLines := strings.Split(fileContent, "\n")
	numMatchedLines := matchEndLine - matchStartLine + 1

	// Detect indentation from the first matched line in the file
	indentation := ""
	for _, ch := range fileLines[matchStartLine] {
		if ch == ' ' || ch == '\t' {
			indentation += string(ch)
		} else {
			break
		}
	}
	fileUsesTabs := len(indentation) > 0 && indentation[0] == '\t'

	// Apply indentation to new_string using visual width
	replaceLines := strings.Split(newString, "\n")
	adjustedLines := make([]string, len(replaceLines))
	for i, line := range replaceLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			adjustedLines[i] = ""
		} else if i == 0 {
			adjustedLines[i] = indentation + trimmed
		} else {
			// Preserve relative indentation from new_string using visual width
			newFirstWidth := leadingWhitespaceWidth(replaceLines[0])
			newThisWidth := leadingWhitespaceWidth(line)
			extraWidth := newThisWidth - newFirstWidth
			extra := ""
			if extraWidth > 0 {
				extra = indentString(extraWidth, fileUsesTabs)
			}
			adjustedLines[i] = indentation + extra + trimmed
		}
	}

	// Line-based replacement (safe boundary handling — no byte-offset artifacts)
	result := make([]string, 0, len(fileLines)-numMatchedLines+len(adjustedLines))
	result = append(result, fileLines[:matchStartLine]...)
	result = append(result, adjustedLines...)
	result = append(result, fileLines[matchStartLine+numMatchedLines:]...)
	newContent := strings.Join(result, "\n")

	return newContent, matchStartLine + 1, true
}

// normalizeWhitespace collapses runs of internal whitespace to a single space, then trims.
// Used for flexible matching where tabs vs spaces and multiple spaces shouldn't matter.
var wsCollapser = regexp.MustCompile(`\s+`)

func normalizeWhitespace(s string) string {
	return strings.TrimSpace(wsCollapser.ReplaceAllString(s, " "))
}

// leadingWhitespaceWidth returns the visual width of leading whitespace (tab = 4 spaces).
func leadingWhitespaceWidth(s string) int {
	width := 0
	for _, ch := range s {
		switch ch {
		case '\t':
			width += 4
		case ' ':
			width++
		default:
			return width
		}
	}
	return width
}

// indentString generates an indentation string of the given visual width using the specified character.
func indentString(width int, useTabs bool) string {
	if useTabs {
		tabs := width / 4
		spaces := width % 4
		return strings.Repeat("\t", tabs) + strings.Repeat(" ", spaces)
	}
	return strings.Repeat(" ", width)
}

// validateReplacement performs intelligent validation and pattern detection
func (t *ReplaceTool) validateReplacement(filePath, fileContent string, oldString, newString string) core.NBToolResponse {
	// 0. CHECK FOR NO-OP REPLACEMENTS (identical old/new strings)
	if oldString != "" && newString != "" {
		if strings.TrimSpace(oldString) == strings.TrimSpace(newString) {
			return core.NBToolResponse{
				Status: "error",
				Error:  fmt.Sprintf("BLOCKED: No actual change detected. old_string and new_string are identical.\n\nString: %s\n\nThis suggests the file may already be fixed. Please re-read the file and verify the error location.", strings.TrimSpace(oldString)),
			}
		}
	}

	// 1. IMPORT SAFETY CHECK
	if t.isImportRemoval(oldString, newString) {
		importName := t.extractImportName(oldString)
		if importName != "" && t.isImportUsed(fileContent, importName, 0) {
			return core.NBToolResponse{
				Status: "error",
				Error:  fmt.Sprintf("BLOCKED: Attempting to remove import '%s' that is still used in the code. This would break the file.\n\nSUGGESTION: If you need to add imports, add them to the existing import section instead of replacing existing imports.", importName),
			}
		}
	}

	// 2. PATTERN DETECTION FOR COMMON FIXES
	patternSuggestion := t.detectCommonPatterns(filePath, fileContent, oldString, newString)
	if patternSuggestion != "" {
		return core.NBToolResponse{
			Status: "suggestion",
			Error:  patternSuggestion,
		}
	}

	// 3. STRUCTURAL VALIDATION
	if t.isSevereStructuralMistake(oldString, newString) {
		return core.NBToolResponse{
			Status: "error",
			Error:  fmt.Sprintf("BLOCKED: This replacement would create invalid code structure.\n\nOld: %s\nNew: %s\n\nSUGGESTION: Review the context and ensure the replacement makes structural sense.", strings.TrimSpace(oldString), strings.TrimSpace(newString)),
		}
	}

	return core.NBToolResponse{Status: "ok"}
}

// isImportRemoval checks if we're removing an import statement
func (t *ReplaceTool) isImportRemoval(oldLine, newString string) bool {
	oldTrimmed := strings.TrimSpace(oldLine)
	newTrimmed := strings.TrimSpace(newString)

	// Check if old line is an import and new line is not
	isOldImport := strings.HasPrefix(oldTrimmed, "import ") || strings.HasPrefix(oldTrimmed, "from ")
	isNewImport := strings.HasPrefix(newTrimmed, "import ") || strings.HasPrefix(newTrimmed, "from ")

	return isOldImport && !isNewImport
}

// extractImportName extracts the imported module/package name
func (t *ReplaceTool) extractImportName(importLine string) string {
	trimmed := strings.TrimSpace(importLine)

	// Handle Python imports
	if strings.HasPrefix(trimmed, "import ") {
		parts := strings.Fields(trimmed)
		if len(parts) >= 2 {
			return parts[1]
		}
	}
	if strings.HasPrefix(trimmed, "from ") {
		parts := strings.Fields(trimmed)
		if len(parts) >= 4 && parts[2] == "import" {
			return parts[3]
		}
	}

	// Handle other language imports (Go, JS, etc.)
	if strings.Contains(trimmed, "\"") {
		// Extract quoted import path
		start := strings.Index(trimmed, "\"")
		end := strings.LastIndex(trimmed, "\"")
		if start != -1 && end != -1 && start != end {
			return trimmed[start+1 : end]
		}
	}

	return ""
}

// isImportUsed checks if an import is used elsewhere in the file
func (t *ReplaceTool) isImportUsed(fileContent, importName string, skipLine int) bool {
	if importName == "" {
		return false
	}

	lines := strings.Split(fileContent, "\n")
	for i, line := range lines {
		if i+1 == skipLine {
			continue // Skip the import line itself
		}

		// Look for usage of the imported name (including module.function usage)
		if strings.Contains(line, importName) {
			// Additional checks to avoid false positives
			trimmedLine := strings.TrimSpace(line)
			if trimmedLine != "" && !strings.HasPrefix(trimmedLine, "#") && !strings.HasPrefix(trimmedLine, "import ") && !strings.HasPrefix(trimmedLine, "from ") {
				return true
			}
		}
	}

	return false
}

// detectCommonPatterns detects common fix patterns and suggests better approaches (language-agnostic)
func (t *ReplaceTool) detectCommonPatterns(filePath, fileContent string, oldString, newString string) string {
	// Dependency/import pattern detection (language-agnostic)
	dependencyKeywords := []string{"import", "require", "include", "using", "from"}
	for _, keyword := range dependencyKeywords {
		if strings.Contains(oldString, keyword) && strings.Contains(newString, keyword) {
			if oldString != newString {
				return "PATTERN DETECTED: Modifying dependencies/imports. Consider adding new dependencies to the existing dependency section instead of replacing existing ones.\n\nBETTER APPROACH:\n1. Find the dependency section (usually at the top of the file)\n2. Add new dependencies there following existing patterns\n3. Don't replace existing dependencies unless they're truly unused"
			}
		}
	}

	return ""
}

// isSevereStructuralMistake checks only for severe structural errors (relaxed validation)
func (t *ReplaceTool) isSevereStructuralMistake(oldLine, newString string) bool {
	oldTrimmed := strings.TrimSpace(oldLine)
	newTrimmed := strings.TrimSpace(newString)

	// Only block obvious disasters - allow content-preserving changes
	// Block replacing class/function definitions with random content
	majorStructures := []string{"class ", "def ", "async def "}
	for _, structure := range majorStructures {
		if strings.HasPrefix(oldTrimmed, structure) && !strings.HasPrefix(newTrimmed, structure) && newTrimmed != "" {
			return true
		}
	}

	// Block replacing control flow with totally unrelated content
	if (strings.HasSuffix(oldTrimmed, ":") &&
		(strings.HasPrefix(oldTrimmed, "if ") || strings.HasPrefix(oldTrimmed, "for ") ||
			strings.HasPrefix(oldTrimmed, "while ") || strings.HasPrefix(oldTrimmed, "try:") ||
			strings.HasPrefix(oldTrimmed, "except") || strings.HasPrefix(oldTrimmed, "finally:"))) &&
		!strings.Contains(newTrimmed, ":") && newTrimmed != "" {
		return true
	}

	return false
}

// ============================================================================
// SELF-HEALING FUNCTIONS (Gemini CLI-inspired)
// ============================================================================

// checkAnchorPreservation detects when an LLM is accidentally deleting an anchor line
// during what should be an insertion operation. This catches the PR 23575 pattern.
// Returns: (correctedNewString, warningMessage)
// If correctedNewString is non-empty, use it instead of the original newString
// If warningMessage is non-empty, return it to the LLM as an error to prompt correction
func (t *ReplaceTool) checkAnchorPreservation(oldString, newString, purpose string) (string, string) {
	oldLines := strings.Split(strings.TrimSpace(oldString), "\n")

	// Only check for short old_strings (1-3 lines) — these are likely anchor lines
	if len(oldLines) > 3 || len(oldLines) == 0 {
		return "", ""
	}

	oldTrimmed := strings.TrimSpace(oldString)
	newTrimmed := strings.TrimSpace(newString)

	// If new_string contains old_string, anchor is preserved — correct pattern
	if strings.Contains(newTrimmed, oldTrimmed) {
		return "", ""
	}

	// If new_string is empty, this is a deletion — not an anchor preservation concern
	if newTrimmed == "" {
		return "", ""
	}

	// If old_string is very short, skip check (likely a simple token replace)
	if len(oldTrimmed) < 15 {
		return "", ""
	}

	// Check if they share the first significant token (same command/keyword)
	oldWords := strings.Fields(oldTrimmed)
	newWords := strings.Fields(newTrimmed)
	if len(oldWords) > 0 && len(newWords) > 0 {
		if oldWords[0] == newWords[0] {
			return "", "" // Same leading keyword — likely a modification, not anchor deletion
		}
	}

	// Check word overlap — if they share significant words, it's probably a modification
	wordOverlap := computeWordOverlap(oldWords, newWords)
	if wordOverlap > 0.3 {
		return "", "" // Enough shared content — likely a modification
	}

	// At this point: short old_string, completely different new_string, no shared content
	// This strongly suggests an insertion where the LLM forgot to include the anchor

	purposeLower := strings.ToLower(purpose)

	// Check if purpose indicates explicit replace intent — allow without warning
	replaceIntentWords := []string{"replace", "change", "modify", "update", "fix", "correct", "rename", "remove", "delete"}
	for _, word := range replaceIntentWords {
		if strings.Contains(purposeLower, word) {
			return "", "" // Explicit replace intent — proceed without warning
		}
	}

	// Check if purpose indicates insert intent — auto-correct
	insertIntentWords := []string{"add", "insert", "after", "before", "append", "include", "upgrade", "prepend"}
	for _, word := range insertIntentWords {
		if strings.Contains(purposeLower, word) {
			// Auto-correct: preserve old_string + add new content after it
			corrected := strings.TrimRight(oldString, "\n") + "\n" + newString
			return corrected, ""
		}
	}

	// No purpose or unclear — return warning to prompt LLM to reconsider
	warning := fmt.Sprintf(
		"WARNING: This replacement completely removes the original code (%d line(s)).\n"+
			"  old_string: %s\n"+
			"  new_string: %s\n\n"+
			"If you intended to INSERT new code while keeping the original, re-submit with:\n"+
			"  new_string = old_string + \"\\n\" + new_code\n\n"+
			"To confirm this is an intentional replacement, add purpose: \"replace <description>\".",
		len(oldLines), truncateStr(oldTrimmed, 100), truncateStr(newTrimmed, 100))

	return "", warning
}

// computeWordOverlap calculates Jaccard similarity between two word sets
func computeWordOverlap(words1, words2 []string) float64 {
	if len(words1) == 0 || len(words2) == 0 {
		return 0
	}
	set := make(map[string]bool)
	for _, w := range words1 {
		set[w] = true
	}
	shared := 0
	for _, w := range words2 {
		if set[w] {
			shared++
		}
	}
	total := len(words1) + len(words2) - shared
	if total == 0 {
		return 0
	}
	return float64(shared) / float64(total)
}

// checkDuplicateContent checks if the new content already exists near the replacement location
// Returns a warning string if duplicate is detected, empty string otherwise
func (t *ReplaceTool) checkDuplicateContent(originalContent, newString string, matchLine int) string {
	newLines := strings.Split(strings.TrimSpace(newString), "\n")

	// Collect first 3 non-empty lines of new_string
	var meaningfulLines []string
	for _, line := range newLines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			meaningfulLines = append(meaningfulLines, trimmed)
		}
		if len(meaningfulLines) >= 3 {
			break
		}
	}

	if len(meaningfulLines) < 2 {
		return "" // Too short to detect duplicates meaningfully
	}

	// Check if these lines already exist near the match location (within 20 lines)
	fileLines := strings.Split(originalContent, "\n")
	searchStart := maxInt(0, matchLine-20)
	searchEnd := minInt(len(fileLines), matchLine+20)

	for i := searchStart; i < searchEnd; i++ {
		if strings.TrimSpace(fileLines[i]) == meaningfulLines[0] {
			// Found first line match, check subsequent lines
			allMatch := true
			for j := 1; j < len(meaningfulLines) && i+j < len(fileLines); j++ {
				if strings.TrimSpace(fileLines[i+j]) != meaningfulLines[j] {
					allMatch = false
					break
				}
			}
			if allMatch {
				return fmt.Sprintf("WARNING: The replacement content already appears near line %d in this file. This may create duplicate code. Verify this is intentional.", i+1)
			}
		}
	}

	return ""
}

// checkBracketBalance warns if the replacement significantly changes bracket balance
func (t *ReplaceTool) checkBracketBalance(oldString, newString string) string {
	type bracketPair struct{ open, close byte }
	brackets := []bracketPair{
		{'{', '}'},
		{'(', ')'},
		{'[', ']'},
	}

	totalImbalance := 0
	for _, b := range brackets {
		oldOpen := strings.Count(oldString, string(b.open))
		oldClose := strings.Count(oldString, string(b.close))
		newOpen := strings.Count(newString, string(b.open))
		newClose := strings.Count(newString, string(b.close))

		deltaOpen := newOpen - oldOpen
		deltaClose := newClose - oldClose

		imbalance := absInt(deltaOpen - deltaClose)
		totalImbalance += imbalance
	}

	if totalImbalance > 2 {
		return fmt.Sprintf("WARNING: Bracket balance changed significantly (net imbalance: %d). Re-read the file to verify structural integrity of the edit.", totalImbalance)
	}

	return ""
}

// postReplaceCheck runs post-replacement validations and returns combined warnings
func (t *ReplaceTool) postReplaceCheck(originalContent, oldString, newString string, matchLine int) string {
	var warnings []string

	if w := t.checkDuplicateContent(originalContent, newString, matchLine); w != "" {
		warnings = append(warnings, w)
	}

	if w := t.checkBracketBalance(oldString, newString); w != "" {
		warnings = append(warnings, w)
	}

	return strings.Join(warnings, "\n")
}

// Helper functions
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// closestMatch holds a candidate line that partially matches a failed old_string search.
type closestMatch struct {
	lineNum int
	content string
	score   float64
}

// findClosestMatches finds the lines in fileContent most similar to searchStr.
// Uses word-overlap (Jaccard) scoring. Returns up to maxResults matches, sorted by score descending.
func (t *ReplaceTool) findClosestMatches(fileContent, searchStr string, maxResults int) []closestMatch {
	searchWords := strings.Fields(strings.TrimSpace(searchStr))
	if len(searchWords) == 0 {
		return nil
	}

	// For multi-line old_string, join into single search term set
	searchSet := make(map[string]bool, len(searchWords))
	for _, w := range searchWords {
		searchSet[strings.ToLower(w)] = true
	}

	lines := strings.Split(fileContent, "\n")
	var candidates []closestMatch

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lineWords := strings.Fields(trimmed)
		if len(lineWords) == 0 {
			continue
		}

		// Deduplicate line words into a set for correct Jaccard similarity
		lineWordSet := make(map[string]bool, len(lineWords))
		for _, w := range lineWords {
			lineWordSet[strings.ToLower(w)] = true
		}

		shared := 0
		for w := range lineWordSet {
			if searchSet[w] {
				shared++
			}
		}
		if shared == 0 {
			continue
		}

		// Jaccard similarity
		total := len(searchSet) + len(lineWordSet) - shared
		score := float64(shared) / float64(total)

		// Only consider lines with meaningful overlap
		if score < 0.15 {
			continue
		}

		candidates = append(candidates, closestMatch{
			lineNum: i + 1,
			content: trimmed,
			score:   score,
		})
	}

	// Sort by score descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	if len(candidates) > maxResults {
		candidates = candidates[:maxResults]
	}
	return candidates
}

// executeVerify verifies that a pattern exists in the file
func (t *ReplaceTool) executeVerify(filePath string, input map[string]any) core.NBToolResponse {
	verificationPattern, _ := input["verification_pattern"].(string)

	if verificationPattern == "" {
		return core.NBToolResponse{Status: "error", Error: "verification_pattern is required for verify action"}
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return core.NBToolResponse{Status: "error", Error: fmt.Sprintf("failed to read file '%s': %v", filePath, err)}
	}
	fileContent := string(content)

	// Check if pattern looks like a regex (contains regex metacharacters)
	looksLikeRegex := strings.ContainsAny(verificationPattern, ".*+?[]{}()^$|\\")

	var found bool
	var matchLines []int
	lines := strings.Split(fileContent, "\n")

	// Try as regex pattern only if it looks like regex
	if looksLikeRegex {
		if regex, err := regexp.Compile(verificationPattern); err == nil {
			// First try line-by-line matching
			for i, line := range lines {
				if regex.MatchString(line) {
					found = true
					matchLines = append(matchLines, i+1) // 1-based line numbers
				}
			}

			// If no line-by-line match, try full content match (for multi-line patterns)
			if !found && regex.MatchString(fileContent) {
				found = true
				// Try to find approximate line numbers
				match := regex.FindString(fileContent)
				if match != "" {
					beforeMatch := fileContent[:strings.Index(fileContent, match)]
					lineNum := strings.Count(beforeMatch, "\n") + 1
					matchLines = append(matchLines, lineNum)
				}
			}
		}
	}

	// Fall back to simple string contains (always try this if regex didn't work)
	if !found {
		for i, line := range lines {
			if strings.Contains(line, verificationPattern) {
				found = true
				matchLines = append(matchLines, i+1) // 1-based line numbers
			}
		}
	}

	if found {
		return core.NBToolResponse{
			Status:      "success",
			Observation: fmt.Sprintf("Verification successful: pattern found in %s on lines: %v", filePath, matchLines),
		}
	} else {
		return core.NBToolResponse{
			Status: "error",
			Error:  fmt.Sprintf("Verification failed: pattern not found in %s.\n\nTip: Use simple string matching instead of complex regex patterns. For example, instead of 'args := map[string]any{...}.*err = dbms.Db.Get', just search for 'args := map[string]any' or 'dbms.Db.Get' separately.", filePath),
		}
	}
}

// searchForFile recursively searches for a file matching the target path structure
func (t *ReplaceTool) searchForFile(baseDir, targetPath string) string {
	targetFilename := filepath.Base(targetPath)
	targetDirStructure := filepath.Dir(targetPath)

	var foundPath string
	err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// Skip hidden directories and common build/temp directories
		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" ||
				name == "vendor" || name == "build" || name == "dist" ||
				name == "__pycache__" || name == "target" {
				return filepath.SkipDir
			}
		}

		if !info.IsDir() && info.Name() == targetFilename {
			// Check if the directory structure matches (allowing for different prefixes)
			relPath, err := filepath.Rel(baseDir, path)
			if err == nil {
				relDir := filepath.Dir(relPath)
				// If the target path contains directory structure, check if it matches
				if targetDirStructure != "." && strings.HasSuffix(relDir, targetDirStructure) {
					foundPath = path
					return filepath.SkipAll // Stop searching once found
				}
				// If no directory structure specified, any match works
				if targetDirStructure == "." {
					foundPath = path
					return filepath.SkipAll
				}
			}
		}

		return nil
	})

	if err != nil {
		return ""
	}

	return foundPath
}
