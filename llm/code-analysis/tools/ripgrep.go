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

type RipgrepTool struct {
	workspaceDir string
}

func NewRipgrepTool(workspaceDir string) *RipgrepTool {
	return &RipgrepTool{
		workspaceDir: workspaceDir,
	}
}

func (t *RipgrepTool) Name() string {
	return "rg"
}

func (t *RipgrepTool) GetType() core.NBToolType {
	return core.NBToolTypeCodeAnalysis
}

func (t *RipgrepTool) IsReadOnly() bool { return true }

func (t *RipgrepTool) Description() string {
	return `Fast text search using ripgrep (rg). RECOMMENDED for large codebases and monorepos. 10-100x faster than grep.

KEY FEATURES:
- Automatically respects .gitignore
- Smart defaults for code search
- Parallel search across files
- Best for searching multiple files quickly

PARAMETERS:
  pattern (required):      Regex pattern to search
  path (optional):         Directory to search (default: ".")
                           Use "." for repo root, or relative paths like "api/services"
                           ❌ DON'T include repo name prefix in path
                           ✅ USE relative from root: "api" or "."
  type (optional):         File type: py, js, go, md, etc.
  glob (optional):         Glob pattern: "*.py", "**/*.test.js"
  case_insensitive:        Case-insensitive (default: false)
  context:                 Lines of context around match (default: 0)
  max_results:             Limit matches (default: 100)
  files_only:              Only show filenames (default: false)

EXAMPLES:
  Search Python files:           {"pattern": "class.*Handler", "type": "py"}
  Search with glob:              {"pattern": "TODO", "glob": "**/*.go"}
  Case-insensitive:              {"pattern": "database", "case_insensitive": true, "type": "js"}
  With context:                  {"pattern": "error", "context": 3, "type": "py"}
  Files only (monorepo):         {"pattern": "failed to cache", "files_only": true}

USE WHEN:
- Searching large codebases (1000+ files)
- Need to find files containing pattern in monorepos
- Want fast results across many files
- Searching by file type (Python, Go, JS, etc.)

WORKFLOW WITH file_view:
1. Use rg to FIND where code exists:
   {"pattern": "func HandleError", "type": "go"}
   → Result: "api/errors.go:145"

2. Use file_view to READ the full implementation:
   {"file_path": "api/errors.go", "start_line": 145, "end_line": 180}

MONOREPO PATTERN (when multiple files match):
1. Find which files contain the code:
   {"pattern": "class UserService", "files_only": true}
   → Result: "backend/services/user.py", "api/services/user.py"

2. Search in specific file for context:
   {"pattern": "class UserService", "path": "backend/services/user.py"}
   → Result: "Line 42"

3. Read full class:
   {"file_path": "backend/services/user.py", "start_line": 42, "end_line": 120}`
}

func (t *RipgrepTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: "object",
		Properties: map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Pattern to search for (supports regex)",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Directory or file to search in (default: current directory)",
				"default":     ".",
			},
			"type": map[string]any{
				"type":        "string",
				"description": "File type filter (e.g., 'py', 'js', 'go', 'md'). Use 'rg --type-list' to see all types.",
			},
			"glob": map[string]any{
				"type":        "string",
				"description": "Glob pattern for files to include (e.g., '*.py', '**/*.test.js')",
			},
			"case_insensitive": map[string]any{
				"type":        "boolean",
				"description": "Case insensitive search",
				"default":     false,
			},
			"context": map[string]any{
				"type":        "integer",
				"description": "Number of context lines to show around matches",
				"default":     0,
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Maximum number of matches to return (default: 100)",
				"default":     100,
			},
			"files_only": map[string]any{
				"type":        "boolean",
				"description": "Only show filenames that contain matches",
				"default":     false,
			},
		},
		Required: []string{"pattern"},
	}
}

func (t *RipgrepTool) Execute(ctx context.Context, input map[string]any) core.NBToolResponse {
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

	fileType, _ := input["type"].(string)
	glob, _ := input["glob"].(string)
	caseInsensitive, _ := input["case_insensitive"].(bool)
	context, _ := input["context"].(float64)
	maxResults, _ := input["max_results"].(float64)
	filesOnly, _ := input["files_only"].(bool)

	if maxResults == 0 {
		maxResults = 100
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
				log.Printf("ERROR RipgrepTool: Failed to walk directory '%s': %v\n", repoDir, err)
			}

			// If we found suggestions, use the first one and inform the agent
			if len(suggestions) > 0 {
				searchPath = filepath.Join(repoDir, suggestions[0])
				fmt.Printf("INFO RipgrepTool: Path '%s' not found. Using similar path: '%s'\n", path, suggestions[0])
			} else {
				// No suggestions found - search from root instead
				searchPath = repoDir
				fmt.Printf("INFO RipgrepTool: Path '%s' not found. Searching from repository root instead.\n", path)
			}
		}
	}

	// Check if ripgrep is available
	if !t.isRipgrepAvailable() {
		return core.NBToolResponse{
			Status: "error",
			Error:  "ripgrep (rg) is not installed or not available in PATH",
		}
	}

	// Build ripgrep command
	args := []string{
		"--line-number", // Always show line numbers
		"--no-heading",  // Don't group by file
		"--color=never", // No color output
	}

	if caseInsensitive {
		args = append(args, "--ignore-case")
	}

	if context > 0 {
		args = append(args, fmt.Sprintf("--context=%d", int(context)))
	}

	if filesOnly {
		args = append(args, "--files-with-matches")
	}

	if fileType != "" {
		args = append(args, fmt.Sprintf("--type=%s", fileType))
	}

	if glob != "" {
		args = append(args, fmt.Sprintf("--glob=%s", glob))
	}

	// Limit output
	args = append(args, fmt.Sprintf("--max-count=%d", int(maxResults)))

	args = append(args, pattern, searchPath)

	cmd := exec.Command("rg", args...)
	cmd.Dir = t.workspaceDir

	output, err := cmd.CombinedOutput() // Capture both stdout and stderr
	if err != nil {
		// Check if it's just "no matches found"
		if exitError, ok := err.(*exec.ExitError); ok {
			if exitError.ExitCode() == 1 {
				return core.NBToolResponse{
					Status:      "success",
					Observation: "No matches found",
					Data:        map[string]any{"matches": []string{}},
				}
			}
			// Exit code 2 = actual error (invalid pattern, bad path, etc.)
			// Include the stderr output so agent knows what went wrong
			errorMsg := fmt.Sprintf("ripgrep failed with exit code %d", exitError.ExitCode())
			if len(output) > 0 {
				errorMsg += fmt.Sprintf("\n\nError details:\n%s", string(output))
			}
			return core.NBToolResponse{
				Status: "error",
				Error:  errorMsg,
			}
		}
		// Generic error (e.g., command not found)
		return core.NBToolResponse{
			Status: "error",
			Error:  fmt.Sprintf("ripgrep execution failed: %v", err),
		}
	}

	return t.formatResults(string(output), filesOnly, int(maxResults))
}

func (t *RipgrepTool) isRipgrepAvailable() bool {
	cmd := exec.Command("rg", "--version")
	return cmd.Run() == nil
}

func (t *RipgrepTool) formatResults(output string, filesOnly bool, maxResults int) core.NBToolResponse {
	if strings.TrimSpace(output) == "" {
		return core.NBToolResponse{
			Status:      "success",
			Observation: "No matches found",
			Data:        map[string]any{"matches": []string{}},
		}
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")

	var matches []map[string]any
	var observation string

	if filesOnly {
		observation = fmt.Sprintf("Found %d files with matches:\n\n", len(lines))
		for i, line := range lines {
			if line == "" {
				continue
			}

			// Make file path relative to workspace
			file := line
			if strings.HasPrefix(file, t.workspaceDir) {
				file = strings.TrimPrefix(file, t.workspaceDir+"/")
			}

			matches = append(matches, map[string]any{
				"file": file,
			})

			observation += fmt.Sprintf("%d. %s\n", i+1, file)
		}
	} else {
		observation = fmt.Sprintf("Found %d matches:\n\n", len(lines))

		for i, line := range lines {
			if line == "" {
				continue
			}

			// Parse ripgrep output: file:line:content
			parts := strings.SplitN(line, ":", 3)
			if len(parts) >= 3 {
				file := parts[0]
				lineNum := parts[1]
				content := strings.TrimSpace(parts[2])

				// Make file path relative to workspace
				if strings.HasPrefix(file, t.workspaceDir) {
					file = strings.TrimPrefix(file, t.workspaceDir+"/")
				}

				matches = append(matches, map[string]any{
					"file":    file,
					"line":    lineNum,
					"content": content,
				})

				observation += fmt.Sprintf("%d. **%s:%s**\n", i+1, file, lineNum)
				observation += fmt.Sprintf("   `%s`\n\n", content)
			}
		}
	}

	if len(lines) == maxResults {
		observation += fmt.Sprintf("*(Limited to %d results)*\n", maxResults)
	}

	return core.NBToolResponse{
		Status:      "success",
		Observation: observation,
		Data: map[string]any{
			"matches":     matches,
			"total_count": len(matches),
			"truncated":   len(lines) == maxResults,
		},
	}
}
