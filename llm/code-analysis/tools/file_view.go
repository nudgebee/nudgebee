package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"nudgebee/code-analysis-agent/tools/core"
)

const maxLinesDefault = 1000 // Max lines to read in one chunk

type FileViewTool struct {
	workspaceDir string
	readTracker  *FileReadTracker
}

func NewFileViewTool(workspaceDir string) *FileViewTool {
	return &FileViewTool{
		workspaceDir: workspaceDir,
	}
}

// SetReadTracker sets the shared read tracker for recording file reads.
func (t *FileViewTool) SetReadTracker(tracker *FileReadTracker) {
	t.readTracker = tracker
}

func (t *FileViewTool) Name() string {
	return "file_view"
}

func (t *FileViewTool) GetType() core.NBToolType {
	return core.NBToolTypeCodeAnalysis
}

func (t *FileViewTool) IsReadOnly() bool { return true }

func (t *FileViewTool) Description() string {
	return `Reads and displays file content with line numbers. Use for reading complete functions, classes, or specific code sections.

USAGE PATTERNS:

1. **Read entire small file** (< 2000 lines):
   {"file_path": "utils.py"}

2. **Read specific line range** (functions, classes):
   {"file_path": "api.go", "start_line": 145, "end_line": 200}

3. **Find then read** (two-step pattern for functions):
   Step 1: {"file_path": "api.go", "search_pattern": "func HandleRequest"}
   Result: ">>> MATCH at line 145"
   Step 2: {"file_path": "api.go", "start_line": 145, "end_line": 200}

IMPORTANT:
- search_pattern shows 10 lines of context around matches (good for locating, NOT for reading full implementations)
- After finding a match, ALWAYS use start_line/end_line to read the complete function/class
- For large files (> 2000 lines), you MUST use start_line/end_line

EXAMPLES:

Reading a complete function you found:
1. {"file_path": "agent.go", "search_pattern": "func GetAgent"}  → Found at line 81
2. {"file_path": "agent.go", "start_line": 81, "end_line": 120}  → Read full implementation

Reading error context:
{"file_path": "server.py", "start_line": 485, "end_line": 515}  // Error on line 500

Directory listing:
{"file_path": "src/models"}`
}

func (t *FileViewTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: "object",
		Properties: map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Relative path to the file (e.g., 'src/agent.go', 'api/server.py')",
			},
			"start_line": map[string]any{
				"type":        "integer",
				"description": "Starting line number (1-based). Use after search_pattern to read full function. Example: After finding match at line 81, use start_line: 81",
			},
			"end_line": map[string]any{
				"type":        "integer",
				"description": "Ending line number (1-based). Use with start_line to read complete functions/classes. Example: end_line: 120 to read lines 81-120",
			},
			"search_pattern": map[string]any{
				"type":        "string",
				"description": "Find lines matching pattern (regex supported). Returns match locations with 10 lines context. Use to LOCATE code, then follow with start_line/end_line to READ full implementation. Example: 'func GetAgent' finds function, then read full body with start/end lines",
			},
		},
		Required: []string{"file_path"},
	}
}

func (t *FileViewTool) Execute(ctx context.Context, input map[string]any) core.NBToolResponse {
	filePath, ok := input["file_path"].(string)
	if !ok {
		return core.NBToolResponse{
			Status: "error",
			Error:  "Invalid input: 'file_path' must be a string.",
		}
	}

	// Use working directory from orchestrator, fallback to tool workspace
	repoDir := t.workspaceDir
	if workingDir, ok := input["working_directory"].(string); ok && workingDir != "" {
		repoDir = workingDir
	}

	// Check if search pattern is provided
	searchPattern, hasSearch := input["search_pattern"].(string)

	startLine := 0
	if val, ok := input["start_line"].(float64); ok {
		startLine = int(val)
	}

	endLine := 0
	if val, ok := input["end_line"].(float64); ok {
		endLine = int(val)
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
				// Only do basename search for bare filenames (no directory in path).
				// If a full path was given and not found, skip to "File not found" handler.
				if filepath.Dir(filePath) == "." {
					allMatches := t.findAllMatches(repoDir, filepath.Base(filePath))
					if len(allMatches) > 1 {
						var matchList strings.Builder
						fmt.Fprintf(&matchList, "Multiple files named '%s' found. Please specify the full path:\n", filepath.Base(filePath))
						for _, m := range allMatches {
							fmt.Fprintf(&matchList, "  - %s\n", m)
						}
						return core.NBToolResponse{
							Status:      "error",
							Error:       matchList.String(),
							Observation: matchList.String(),
						}
					}
				}
				filePath = directPath // Keep original for error reporting
			}
		}
	}

	// Check if path is a directory
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		// If file not found, try to suggest alternatives
		if os.IsNotExist(err) {
			originalPath := input["file_path"].(string)
			suggestions := t.findSimilarFiles(originalPath)
			if len(suggestions) > 0 {
				suggestionText := strings.Join(suggestions, "\n  - ")
				return core.NBToolResponse{
					Status: "error",
					Error:  fmt.Sprintf("File not found: %s\n\nDid you mean one of these?\n  - %s\n\nTry using file_view with one of the suggested paths above.", originalPath, suggestionText),
				}
			}
		}
		return core.NBToolResponse{
			Status: "error",
			Error:  fmt.Sprintf("Error accessing path: %v", err),
		}
	}

	// If it's a directory, list its contents
	if fileInfo.IsDir() {
		return t.listDirectory(filePath, hasSearch, searchPattern)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return core.NBToolResponse{
			Status: "error",
			Error:  fmt.Sprintf("Error opening file: %v", err),
		}
	}
	defer func() { _ = file.Close() }()

	// If search pattern is provided, use search mode
	if hasSearch && searchPattern != "" {
		if t.readTracker != nil {
			t.readTracker.RecordRead(filePath)
		}
		return t.searchInFile(filePath, searchPattern)
	}

	// Get total line count for large file check
	lineCount, err := countLines(filePath)
	if err != nil {
		return core.NBToolResponse{
			Status: "error",
			Error:  fmt.Sprintf("Error counting lines in file: %v", err),
		}
	}

	// If no range is specified, check if the file is too large to display fully
	if startLine == 0 && endLine == 0 && lineCount > maxLinesDefault {
		return core.NBToolResponse{
			Status: "error",
			Error:  fmt.Sprintf("File '%s' is too large (%d lines). Please specify a smaller chunk using 'start_line' and 'end_line' (e.g., a 200-line chunk).", filePath, lineCount),
		}
	}

	// If a range is specified, check if it's too large
	if endLine > 0 && startLine > 0 && (endLine-startLine+1) > maxLinesDefault {
		return core.NBToolResponse{
			Status: "error",
			Error:  fmt.Sprintf("The requested line range (%d lines) is too large. Please request a smaller chunk (max %d lines).", (endLine - startLine + 1), maxLinesDefault),
		}
	}

	// Read the specified lines
	scanner := bufio.NewScanner(file)
	var content string
	currentLine := 1
	for scanner.Scan() {
		if (startLine == 0 && endLine == 0) || (currentLine >= startLine && (endLine == 0 || currentLine <= endLine)) {
			content += fmt.Sprintf("%d: %s\n", currentLine, scanner.Text())
		}
		if endLine > 0 && currentLine >= endLine {
			break
		}
		currentLine++
	}

	if err := scanner.Err(); err != nil {
		return core.NBToolResponse{
			Status: "error",
			Error:  fmt.Sprintf("Error reading file: %v", err),
		}
	}

	// Record successful read
	if t.readTracker != nil {
		t.readTracker.RecordRead(filePath)
	}

	// Include actual file path in observation when it differs from requested path
	observation := content
	originalPath := input["file_path"].(string)

	// Get the relative path from working directory
	var actualRelativePath string
	if workingDir, ok := input["working_directory"].(string); ok {
		if strings.HasPrefix(filePath, workingDir+"/") {
			actualRelativePath = strings.TrimPrefix(filePath, workingDir+"/")
		}
	}

	// If we found the file at a different path, inform the agent
	if actualRelativePath != "" && actualRelativePath != originalPath {
		observation = fmt.Sprintf("File found at: %s\n\n%s", actualRelativePath, content)
	}

	return core.NBToolResponse{
		Status:      "success",
		Observation: observation,
		Data:        map[string]any{"file_path": filePath, "lines_read": currentLine - 1},
	}
}

func countLines(filePath string) (int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		count++
	}

	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return count, nil
}

// searchInFile searches for a pattern in the file and returns matching lines with context
func (t *FileViewTool) searchInFile(filePath, pattern string) core.NBToolResponse {
	file, err := os.Open(filePath)
	if err != nil {
		return core.NBToolResponse{
			Status: "error",
			Error:  fmt.Sprintf("Error opening file for search: %v", err),
		}
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	var lines []string
	lineNum := 1

	// Read all lines first
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		lineNum++
	}

	if err := scanner.Err(); err != nil {
		return core.NBToolResponse{
			Status: "error",
			Error:  fmt.Sprintf("Error reading file for search: %v", err),
		}
	}

	// Find matching lines
	var results []string
	matchCount := 0
	contextLines := 10 // Increased from 3 to show more function body context

	for i, line := range lines {
		if matchesPattern(line, pattern) {
			matchCount++

			// Add context before
			start := max(0, i-contextLines)
			end := min(len(lines), i+contextLines+1)

			// Add separator if not first match
			if len(results) > 0 {
				results = append(results, "")
				results = append(results, "--- Next Match ---")
			}

			// Add lines with context
			for j := start; j < end; j++ {
				lineNumber := j + 1
				marker := ""
				if j == i {
					marker = " >>> MATCH: "
				} else {
					marker = "     "
				}
				results = append(results, fmt.Sprintf("%d:%s%s", lineNumber, marker, lines[j]))
			}
		}
	}

	if matchCount == 0 {
		return core.NBToolResponse{
			Status:      "success",
			Observation: fmt.Sprintf("No matches found for pattern '%s' in %s", pattern, filePath),
			Data: map[string]any{
				"pattern":   pattern,
				"matches":   0,
				"file_path": filePath,
			},
		}
	}

	content := fmt.Sprintf("Found %d matches for pattern '%s':\n\n%s\n\n💡 TIP: To read the complete function/class, use file_view again with start_line and end_line parameters.",
		matchCount, pattern, joinStrings(results, "\n"))

	return core.NBToolResponse{
		Status:      "success",
		Observation: content,
		Data: map[string]any{
			"pattern":   pattern,
			"matches":   matchCount,
			"file_path": filePath,
		},
	}
}

// Helper functions
func matchesPattern(line, pattern string) bool {
	// Try regex first if pattern looks like regex (contains special chars)
	if strings.ContainsAny(pattern, "^$.*+?()[]{}|\\") {
		if regex, err := regexp.Compile(pattern); err == nil {
			return regex.MatchString(line)
		}
	}

	// Fallback to literal string matching
	return strings.Contains(line, pattern)
}

// listDirectory lists the contents of a directory
func (t *FileViewTool) listDirectory(dirPath string, hasSearch bool, searchPattern string) core.NBToolResponse {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return core.NBToolResponse{
			Status: "error",
			Error:  fmt.Sprintf("Error reading directory: %v", err),
		}
	}

	var content strings.Builder
	fmt.Fprintf(&content, "Directory listing for: %s\n\n", dirPath)

	if len(entries) == 0 {
		content.WriteString("(empty directory)\n")
	} else {
		// If search pattern is provided, filter entries
		if hasSearch && searchPattern != "" {
			var filteredEntries []os.DirEntry
			for _, entry := range entries {
				if strings.Contains(strings.ToLower(entry.Name()), strings.ToLower(searchPattern)) {
					filteredEntries = append(filteredEntries, entry)
				}
			}
			entries = filteredEntries

			if len(entries) == 0 {
				fmt.Fprintf(&content, "No entries found matching pattern '%s'\n", searchPattern)
			} else {
				fmt.Fprintf(&content, "Entries matching pattern '%s':\n\n", searchPattern)
			}
		}

		for _, entry := range entries {
			if entry.IsDir() {
				fmt.Fprintf(&content, "[DIR]  %s/\n", entry.Name())
			} else {
				info, err := entry.Info()
				if err == nil {
					fmt.Fprintf(&content, "[FILE] %s (%d bytes)\n", entry.Name(), info.Size())
				} else {
					fmt.Fprintf(&content, "[FILE] %s\n", entry.Name())
				}
			}
		}
	}

	return core.NBToolResponse{
		Status:      "success",
		Observation: content.String(),
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	if len(strs) == 1 {
		return strs[0]
	}

	var result string
	for i, s := range strs {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}

// findSimilarFiles searches for files with similar names when exact path fails
func (t *FileViewTool) findSimilarFiles(originalPath string) []string {
	var suggestions []string

	// Extract the filename from the path
	filename := filepath.Base(originalPath)

	// Common path transformations for Docker/container paths
	pathTransforms := []string{
		// Remove leading /app and try relative paths
		strings.TrimPrefix(originalPath, "/app/"),
		strings.TrimPrefix(originalPath, "/usr/local/"),
		strings.TrimPrefix(originalPath, "/opt/"),
		// Try common source directories
		"src/" + strings.TrimPrefix(originalPath, "/app/"),
		"app/" + strings.TrimPrefix(originalPath, "/app/"),
		"backend/" + strings.TrimPrefix(originalPath, "/app/"),
		"server/" + strings.TrimPrefix(originalPath, "/app/"),
		"api/" + strings.TrimPrefix(originalPath, "/app/"),
	}

	// Check transformed paths
	for _, transformedPath := range pathTransforms {
		if transformedPath != originalPath && transformedPath != "" {
			fullPath := filepath.Join(t.workspaceDir, transformedPath)
			if _, err := os.Stat(fullPath); err == nil {
				suggestions = append(suggestions, transformedPath)
			}
		}
	}

	// If we still haven't found anything, do a recursive search
	if len(suggestions) == 0 {
		// Check if the original path likely refers to a directory (no file extension)
		if filepath.Ext(filename) == "" {
			// Search for directories first
			matches := t.recursiveDirSearch(t.workspaceDir, filename)
			for _, match := range matches {
				// Make path relative to workspace
				if relPath, err := filepath.Rel(t.workspaceDir, match); err == nil {
					suggestions = append(suggestions, relPath)
				}
			}
		}

		// Also search for files (or if no directory matches found)
		if len(suggestions) < 3 { // Leave room for file matches too
			matches := t.recursiveFileSearch(t.workspaceDir, filename)
			for _, match := range matches {
				// Make path relative to workspace
				if relPath, err := filepath.Rel(t.workspaceDir, match); err == nil {
					suggestions = append(suggestions, relPath)
				}
			}
		}
	}

	// Limit to 5 suggestions to avoid overwhelming the LLM
	if len(suggestions) > 5 {
		suggestions = suggestions[:5]
	}

	return suggestions
}

// recursiveFileSearch searches for files with matching filename recursively
func (t *FileViewTool) recursiveFileSearch(dir, filename string) []string {
	var matches []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
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

		if !info.IsDir() && info.Name() == filename {
			matches = append(matches, path)
		}

		// Limit search results to prevent excessive searching
		if len(matches) >= 10 {
			return filepath.SkipAll
		}

		return nil
	})

	if err != nil {
		return nil
	}

	return matches
}

// recursiveDirSearch searches for directories with matching directory name recursively
func (t *FileViewTool) recursiveDirSearch(dir, dirname string) []string {
	var matches []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
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

			// If this directory matches our target, add it
			if name == dirname {
				matches = append(matches, path)
			}
		}

		// Limit search results to prevent excessive searching
		if len(matches) >= 10 {
			return filepath.SkipAll
		}

		return nil
	})

	if err != nil {
		return nil
	}

	return matches
}

// searchForFile recursively searches for a file with the same relative path structure.
// For bare filenames (no directory path), returns the match only if exactly one is found.
// Returns empty string if zero or multiple matches exist (caller should use findAllMatches for details).
func (t *FileViewTool) searchForFile(baseDir, targetPath string) string {
	targetFilename := filepath.Base(targetPath)
	targetDirStructure := filepath.Dir(targetPath)

	var matches []string
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
					matches = []string{path}
					return filepath.SkipAll // Exact directory match - return immediately
				}
				// If no directory structure specified, collect all matches
				if targetDirStructure == "." {
					matches = append(matches, path)
				}
			}
		}

		return nil
	})

	if err != nil {
		return ""
	}

	// Return path only if exactly one match found
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

// findAllMatches finds all files matching the given filename in the directory tree
func (t *FileViewTool) findAllMatches(baseDir, filename string) []string {
	var matches []string
	_ = filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" ||
				name == "vendor" || name == "build" || name == "dist" ||
				name == "__pycache__" || name == "target" {
				return filepath.SkipDir
			}
		}
		if !info.IsDir() && info.Name() == filename {
			if relPath, err := filepath.Rel(baseDir, path); err == nil {
				matches = append(matches, relPath)
			}
		}
		return nil
	})
	return matches
}
