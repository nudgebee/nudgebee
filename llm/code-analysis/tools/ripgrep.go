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

SEARCH IS FOR LOCATING CODE, NOT FOR CITING IT. A grep hit is a lead, not evidence.
Always follow the funnel: (1) locate the file → (2) open it with file_view → (3) cite only what you READ.
Never cite a file:line you have not opened with file_view.

KEY FEATURES:
- Automatically respects .gitignore
- By default EXCLUDES test files, vendor/, node_modules/, generated & lock files (they are noise, not evidence).
  Set "include_tests": true only if the answer genuinely lives in a test.
- Reference-first: broad searches return a per-file match summary (not every line) and tell you to narrow.

PARAMETERS:
  pattern (required):      Regex pattern to search
  path (optional):         Directory to search (default: ".") — SCOPE TO ONE MODULE when you can.
                           Use "." for repo root, or relative paths like "api/services"
                           ❌ DON'T include repo name prefix in path
                           ✅ USE relative from root: "api" or "."
  type (optional):         File type: py, js, go, md, ts, tsx, etc.
  glob (optional):         Glob pattern: "*.py", "**/*.test.js"
  case_insensitive:        Case-insensitive (default: false)
  context:                 Lines of context around match (default: 0)
  max_results:             Limit matches (default: 100)
  files_only:              Only show filenames (default: false) — best FIRST step in a large repo.
  include_tests:           Include test/vendor/generated/lock files (default: false)

FUNNEL WORKFLOW (do this, in order):
1. Locate the files (reference-first):
   {"pattern": "PaymentProcessor", "files_only": true, "type": "go"}
   → billing/payment_processor.go
2. Scope to the module and find the line:
   {"pattern": "func.*Charge", "path": "billing"}
3. READ it before citing:
   file_view {"file_path": "billing/payment_processor.go", "start_line": 1, "end_line": 80}

If a broad term floods (matching more than you intend), the result is truncated: add a path/type filter or a
more specific pattern. If a search returns nothing, broaden the pattern or drop a filter — do NOT re-run the
same search with only a case/glob tweak.`
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
			"include_tests": map[string]any{
				"type":        "boolean",
				"description": "Include test/vendor/generated/lock files, which are excluded by default as low-signal noise",
				"default":     false,
			},
		},
		Required: []string{"pattern"},
	}
}

// lowSignalExcludeGlobs are ripgrep negative globs applied by default so broad
// searches don't surface (and get cited as) test scaffolding, vendored code,
// generated stubs, or lockfiles — none of which are evidence for a root cause.
var lowSignalExcludeGlobs = []string{
	"!**/*_test.go",
	"!**/*_test.py",
	"!**/*.test.ts", "!**/*.test.tsx", "!**/*.test.js", "!**/*.test.jsx",
	"!**/*.spec.ts", "!**/*.spec.tsx", "!**/*.spec.js", "!**/*.spec.jsx",
	"!**/testdata/**", "!**/__tests__/**", "!**/__mocks__/**",
	"!**/vendor/**", "!**/node_modules/**",
	"!**/*.min.js", "!**/dist/**",
	"!**/*.pb.go", "!**/*_pb2.py", "!**/*.generated.*", "!**/mock_*.go",
	"!**/*.lock", "!**/package-lock.json", "!**/go.sum",
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
	includeTests, _ := input["include_tests"].(bool)

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

	// Exclude low-signal files (tests, vendor, generated, locks) by default so
	// they can't be surfaced and cited as evidence. Opt back in with include_tests.
	if !includeTests {
		for _, g := range lowSignalExcludeGlobs {
			args = append(args, fmt.Sprintf("--glob=%s", g))
		}
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
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return noMatchResponse(nil)
	}

	lines := strings.Split(trimmed, "\n")

	if filesOnly {
		files := make([]string, 0, len(lines))
		for _, l := range lines {
			if l == "" {
				continue
			}
			files = append(files, relToWorkspace(l, t.workspaceDir))
		}
		return renderFilesList(files, maxResults, nil)
	}

	matches, order, byFile := parseMatchLines(lines, t.workspaceDir)
	return renderContentMatches(matches, order, byFile, maxResults, nil)
}
