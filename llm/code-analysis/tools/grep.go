package tools

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"nudgebee/code-analysis-agent/tools/core"
)

type GrepTool struct {
	workspaceDir string
}

func NewGrepTool(workspaceDir string) *GrepTool {
	return &GrepTool{
		workspaceDir: workspaceDir,
	}
}

func (t *GrepTool) Name() string {
	return "grep"
}

func (t *GrepTool) GetType() core.NBToolType {
	return core.NBToolTypeCodeAnalysis
}

func (t *GrepTool) IsReadOnly() bool { return true }

func (t *GrepTool) Description() string {
	return `Search for text patterns across files using grep. Automatically tries git grep first, then falls back to system grep.

PARAMETERS:
  pattern (required):      Regex pattern to search for
  path (optional):         Directory to search (default: ".")
                           Use "." for repo root, or relative paths like "src/components"
                           ❌ DON'T include repo name prefix in path
                           ✅ USE relative from root: "src" or "."
  include (optional):      File pattern like "*.py" or "*.js"
  case_insensitive:        Case-insensitive search (default: false)
  line_numbers:            Show line numbers (default: true)
  max_results:             Limit matches (default: 50)

EXAMPLES:
  Search for function:     {"pattern": "def calculate", "include": "*.py"}
  Find error strings:      {"pattern": "error|exception", "case_insensitive": true}
  Search in directory:     {"pattern": "TODO", "path": "src/components"}
  Case-insensitive:        {"pattern": "database", "case_insensitive": true, "include": "*.go"}

USE CASES:
- Find function/class definitions
- Search for error messages or log strings
- Locate TODO/FIXME comments
- Find usage of specific variables/imports

WORKFLOW WITH file_view:
1. Use grep to LOCATE code:
   {"pattern": "def calculate_metrics", "include": "*.py"}
   → Result: "utils/metrics.py:89"

2. Use file_view to READ complete function:
   {"file_path": "utils/metrics.py", "start_line": 89, "end_line": 120}

TIP: For large codebases or monorepos, use 'rg' tool instead (10-100x faster)`
}

func (t *GrepTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: "object",
		Properties: map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regular expression pattern to search for",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Directory to search in (default: current directory)",
				"default":     ".",
			},
			"include": map[string]any{
				"type":        "string",
				"description": "File pattern to include (e.g., '*.py', '*.js'). Optional.",
			},
			"case_insensitive": map[string]any{
				"type":        "boolean",
				"description": "Perform case-insensitive search",
				"default":     false,
			},
			"line_numbers": map[string]any{
				"type":        "boolean",
				"description": "Show line numbers in results",
				"default":     true,
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Maximum number of matches to return (default: 50)",
				"default":     50,
			},
		},
		Required: []string{"pattern"},
	}
}

func (t *GrepTool) Execute(ctx context.Context, input map[string]any) core.NBToolResponse {
	pattern, ok := input["pattern"].(string)
	if !ok {
		return core.NBToolResponse{
			Status: "error",
			Error:  "Invalid input: 'pattern' must be a string.",
		}
	}

	path, _ := input["path"].(string)
	if path == "" {
		path = "."
	}

	include, _ := input["include"].(string)
	caseInsensitive, _ := input["case_insensitive"].(bool)
	lineNumbers, _ := input["line_numbers"].(bool)
	maxResults, _ := input["max_results"].(float64)

	if maxResults == 0 {
		maxResults = 50
	}

	// Use working directory from orchestrator, fallback to tool workspace
	repoDir := t.workspaceDir
	if workingDir, ok := input["working_directory"].(string); ok && workingDir != "" {
		repoDir = workingDir
	}

	// Normalize path to prevent duplication
	// If agent provides "nudgebee/subdir" when repoDir is "/path/to/nudgebee", strip "nudgebee/" prefix
	repoName := filepath.Base(repoDir)
	if path == repoName {
		// Exact match: "nudgebee" → "."
		path = "."
	} else if strings.HasPrefix(path, repoName+"/") {
		// Starts with repo name: "nudgebee/llm/..." → "llm/..."
		path = strings.TrimPrefix(path, repoName+"/")
	}

	// Ensure path is relative to repository directory
	searchPath := path
	if !filepath.IsAbs(path) {
		searchPath = filepath.Join(repoDir, path)
	}

	// Check if the path exists - if not, try to find similar paths or search from root
	if path != "." && !filepath.IsAbs(path) {
		if _, err := os.Stat(searchPath); os.IsNotExist(err) {
			// Path doesn't exist - try to find similar paths
			baseSearchPath := filepath.Base(path)
			var suggestions []string

			// Walk the repo to find directories matching the base name
			err = filepath.Walk(repoDir, func(walkPath string, info os.FileInfo, err error) error {
				if err != nil || !info.IsDir() {
					return nil
				}
				if filepath.Base(walkPath) == baseSearchPath {
					if relPath, err := filepath.Rel(repoDir, walkPath); err == nil {
						suggestions = append(suggestions, relPath)
					}
				}
				return nil
			})
			if err != nil {
				log.Printf("ERROR GrepTool: Failed to walk directory '%s': %v\n", repoDir, err)
			}

			// If we found suggestions, use the first one and inform the agent
			if len(suggestions) > 0 {
				searchPath = filepath.Join(repoDir, suggestions[0])
				fmt.Printf("INFO GrepTool: Path '%s' not found. Using similar path: '%s'\n", path, suggestions[0])
			} else {
				// No suggestions found - search from root instead
				searchPath = repoDir
				fmt.Printf("INFO GrepTool: Path '%s' not found. Searching from repository root instead.\n", path)
			}
		}
	}

	// Strategy 1: git grep first (authoritative in a git repo).
	result := t.tryGitGrep(pattern, include, caseInsensitive, lineNumbers, int(maxResults))
	if result.Status == "success" {
		return result
	}

	// Strategy 2: system grep fallback (rg is the primary/recommended search
	// tool; grep is a single fallback for non-git or rg-less environments).
	return t.trySystemGrep(pattern, searchPath, include, caseInsensitive, lineNumbers, int(maxResults))
}

// isNoMatchExit reports whether an exec error is grep's "no lines selected"
// (exit code 1), which is a successful empty search, not a failure.
func isNoMatchExit(err error) bool {
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode() == 1
	}
	return false
}

func (t *GrepTool) tryGitGrep(pattern, include string, caseInsensitive, lineNumbers bool, maxResults int) core.NBToolResponse {
	args := []string{"grep"}

	if lineNumbers {
		args = append(args, "-n")
	}
	if caseInsensitive {
		args = append(args, "-i")
	}

	args = append(args, pattern)

	if include != "" {
		args = append(args, "--", fmt.Sprintf(":%s", include))
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = t.workspaceDir

	output, err := cmd.CombinedOutput() // Capture both stdout and stderr
	if err != nil {
		if isNoMatchExit(err) {
			return noMatchResponse(map[string]any{"method": "git grep"})
		}
		errorMsg := fmt.Sprintf("git grep failed: %v", err)
		if len(output) > 0 {
			errorMsg += fmt.Sprintf("\nError details: %s", string(output))
		}
		return core.NBToolResponse{Status: "error", Error: errorMsg}
	}

	return t.formatResults("git grep", string(output), maxResults)
}

func (t *GrepTool) trySystemGrep(pattern, searchPath, include string, caseInsensitive, lineNumbers bool, maxResults int) core.NBToolResponse {
	args := []string{"-r"}

	if lineNumbers {
		args = append(args, "-n")
	}
	if caseInsensitive {
		args = append(args, "-i")
	}

	if include != "" {
		args = append(args, "--include", include)
	}

	args = append(args, pattern, searchPath)

	cmd := exec.Command("grep", args...)
	output, err := cmd.CombinedOutput() // Capture both stdout and stderr
	if err != nil {
		if isNoMatchExit(err) {
			return noMatchResponse(map[string]any{"method": "grep"})
		}
		errorMsg := fmt.Sprintf("grep failed: %v", err)
		if len(output) > 0 {
			errorMsg += fmt.Sprintf("\nError details: %s", string(output))
		}
		return core.NBToolResponse{Status: "error", Error: errorMsg}
	}

	return t.formatResults("grep", string(output), maxResults)
}

func (t *GrepTool) formatResults(method, output string, maxResults int) core.NBToolResponse {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return noMatchResponse(map[string]any{"method": method})
	}

	lines := strings.Split(trimmed, "\n")
	matches, order, byFile := parseMatchLines(lines, t.workspaceDir)
	return renderContentMatches(matches, order, byFile, maxResults, map[string]any{"method": method})
}
