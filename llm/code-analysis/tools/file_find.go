package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"nudgebee/code-analysis-agent/tools/core"
)

type FileFindTool struct {
	workspaceDir string
}

func NewFileFindTool(workspaceDir string) *FileFindTool {
	return &FileFindTool{
		workspaceDir: workspaceDir,
	}
}

func (t *FileFindTool) Name() string {
	return "file_find"
}

func (t *FileFindTool) GetType() core.NBToolType {
	return core.NBToolTypeCodeAnalysis
}

func (t *FileFindTool) IsReadOnly() bool { return true }

func (t *FileFindTool) Description() string {
	return `Finds files by name or path pattern. Use when you know the filename but not the full path.

WHEN TO USE:
- Error log shows "error in utils.py" → Find which utils.py
- Need to locate "config.yaml" in monorepo → Find all config files
- Know class is in "user_service.py" → Find exact path

VS OTHER TOOLS:
- Use file_find when: You know filename, need to find path
- Use rg when: You know code content, need to find file+location

MONOREPO PATTERN (multiple matches):
Error: "TypeError in message.py line 45"

Step 1: Find ALL message.py files
{"path_glob": "message.py"}
→ Result: "api/message.py", "workers/message.py", "tests/message.py"

Step 2: Use file_view on each to find which has the error
{"file_path": "api/message.py", "start_line": 40, "end_line": 50"}

Step 3: Use rg to narrow down (if needed)
{"pattern": "TypeError", "path": "api/message.py"}

GLOB PATTERNS:
Exact filename:     {"path_glob": "utils.py"}
All Python files:   {"path_glob": "*.py"}
In subdirectory:    {"path_glob": "services/*.py"}
Recursive:          {"path_glob": "**/utils.py"}        # All utils.py in any directory
Specific path:      {"path_glob": "api/services/*.py"}  # All .py in api/services

EXAMPLES:
Find config:        {"path_glob": "config.yaml"}
Find test files:    {"path_glob": "**/*_test.go"}
Find in directory:  {"path_glob": "src/models/*.py"}`
}

func (t *FileFindTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: "object",
		Properties: map[string]any{
			"path_glob": map[string]any{
				"type":        "string",
				"description": "A full or partial file path to search for (e.g., 'services/cache.py', '/usr/src/app/services/cache.py').",
			},
		},
		Required: []string{"path_glob"},
	}
}

func (t *FileFindTool) Execute(ctx context.Context, input map[string]any) core.NBToolResponse {
	pathGlob, ok := input["path_glob"].(string)
	if !ok {
		return core.NBToolResponse{
			Status: "error",
			Error:  "Invalid input: 'path_glob' must be a string.",
		}
	}

	// Use working directory from orchestrator, fallback to tool workspace
	repoDir := t.workspaceDir
	if workingDir, ok := input["working_directory"].(string); ok && workingDir != "" {
		repoDir = workingDir
	}

	// Get all files recursively using filepath.Walk instead of git ls-files
	// This works regardless of git repository structure
	var allFiles []string
	err := filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Continue walking even if there are errors
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

		// Only include files, not directories
		if !info.IsDir() {
			// Make path relative to repository directory
			if relPath, err := filepath.Rel(repoDir, path); err == nil {
				allFiles = append(allFiles, relPath)
			}
		}

		return nil
	})

	if err != nil {
		return core.NBToolResponse{
			Status: "error",
			Error:  fmt.Sprintf("Failed to scan workspace directory: %v", err),
		}
	}

	// Find matches using proper glob pattern matching
	var matches []string
	for _, filePath := range allFiles {
		// Try exact glob match first
		if matched, _ := filepath.Match(pathGlob, filePath); matched {
			matches = append(matches, filePath)
			continue
		}

		// Try glob match on just the filename for patterns like "*.py"
		if matched, _ := filepath.Match(pathGlob, filepath.Base(filePath)); matched {
			matches = append(matches, filePath)
			continue
		}

		// Handle double-star patterns like "**/*.py" by checking if the file extension matches
		if strings.HasPrefix(pathGlob, "**/") {
			pattern := pathGlob[3:] // Remove "**/" prefix
			if matched, _ := filepath.Match(pattern, filepath.Base(filePath)); matched {
				matches = append(matches, filePath)
				continue
			}
		}

		// Fallback to substring matching for partial paths
		if strings.Contains(filePath, pathGlob) ||
			strings.Contains(strings.ToLower(filePath), strings.ToLower(pathGlob)) {
			matches = append(matches, filePath)
		}
	}

	if len(allFiles) == 0 {
		return core.NBToolResponse{
			Status: "error",
			Error:  "No files found in workspace directory",
		}
	}

	if len(matches) == 0 {
		return core.NBToolResponse{
			Status:      "success",
			Observation: fmt.Sprintf("No file matching '%s' found in %d files. Consider checking the filename or path.", pathGlob, len(allFiles)),
			Data:        map[string]any{"paths": []string{}, "total_files": len(allFiles)},
		}
	}

	// Build detailed file information for LLM decision-making
	var fileDetails []map[string]any
	observation := fmt.Sprintf("Found %d file(s) matching '%s':\n\n", len(matches), pathGlob)

	for i, match := range matches {
		fullPath := filepath.Join(repoDir, match)
		lineCount := t.getFileLineCount(fullPath)

		// Get file content preview for LLM analysis
		contentPreview := t.getFileContentPreview(fullPath, 20) // First 20 lines

		fileDetails = append(fileDetails, map[string]any{
			"path":            match,
			"line_count":      lineCount,
			"content_preview": contentPreview,
		})

		observation += fmt.Sprintf("%d. **%s**\n", i+1, match)
		observation += fmt.Sprintf("   • Path: %s\n", match)
		observation += fmt.Sprintf("   • Lines: %d\n", lineCount)
		observation += fmt.Sprintf("   • Directory: %s\n", filepath.Dir(match))
		observation += fmt.Sprintf("   • Content preview (first 20 lines):\n```\n%s\n```\n", contentPreview)

		observation += "\n"
	}

	if len(matches) > 1 {
		observation += "**Multiple files found.** Consider:\n"
		observation += "• Check which directory/module the error relates to\n"
		observation += "• Verify file has sufficient lines for the error line number\n"
		observation += "• Review content previews to identify the most relevant file\n"
		observation += "• Use grep or rg tools to search within files for specific patterns\n"
	}

	return core.NBToolResponse{
		Status:      "success",
		Observation: observation,
		Data: map[string]any{
			"paths":   matches,
			"details": fileDetails,
		},
	}
}

// getFileLineCount counts the number of lines in a file
func (t *FileFindTool) getFileLineCount(filePath string) int {
	file, err := os.Open(filePath)
	if err != nil {
		return 0
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	lines := 0
	for scanner.Scan() {
		lines++
	}

	return lines
}

// getFileContentPreview reads the first N lines of a file for LLM analysis
func (t *FileFindTool) getFileContentPreview(filePath string, maxLines int) string {
	file, err := os.Open(filePath)
	if err != nil {
		return "Unable to read file content"
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	var lines []string
	lineCount := 0

	for scanner.Scan() && lineCount < maxLines {
		lines = append(lines, scanner.Text())
		lineCount++
	}

	if err := scanner.Err(); err != nil {
		return "Error reading file content"
	}

	preview := strings.Join(lines, "\n")
	if lineCount == maxLines {
		preview += "\n... (truncated)"
	}

	return preview
}
