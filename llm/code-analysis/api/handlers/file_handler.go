package handlers

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"nudgebee/code-analysis-agent/config"

	"github.com/gin-gonic/gin"
)

type FileHandler struct {
	Config *config.Config
}

func NewFileHandler(cfg *config.Config) *FileHandler {
	return &FileHandler{
		Config: cfg,
	}
}

// FileInfo represents a file or directory
type FileInfo struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Type  string `json:"type"` // "file" or "dir"
	Size  int64  `json:"size"`
	Mtime string `json:"mtime"`
}

type SaveFileRequest struct {
	Path           string `json:"path" binding:"required"`
	Content        string `json:"content"`
	ConversationID string `json:"conversation_id,omitempty"`
}

type BatchReadFileRequest struct {
	Paths          []string `json:"paths" binding:"required"`
	ConversationID string   `json:"conversation_id,omitempty"`
}

type FileContent struct {
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

// getWorkspaceDir returns the configured workspace directory based on context
func (h *FileHandler) getWorkspaceDir(conversationID string) string {
	// If conversation ID is provided, use execution workspace
	if conversationID != "" {
		baseDir := h.Config.Execution.WorkspaceDir
		if baseDir == "" {
			if h.Config.Analysis.WorkspaceDir != "" {
				baseDir = filepath.Join(h.Config.Analysis.WorkspaceDir, "exec_workspaces")
			} else {
				baseDir = "/tmp/code-analysis/exec_workspaces"
			}
		}
		return filepath.Join(baseDir, conversationID)
	}

	// Default to analysis workspace
	if h.Config.Analysis.WorkspaceDir != "" {
		return h.Config.Analysis.WorkspaceDir
	}
	return "/tmp/code-analysis"
}

// resolvePath resolves the requested path relative to the workspace directory
// and ensures it doesn't escape the workspace
func (h *FileHandler) resolvePath(requestedPath, conversationID string) (string, error) {
	if conversationID != "" && !safePathRe.MatchString(conversationID) {
		return "", fmt.Errorf("invalid conversation ID format")
	}

	workspaceDir := h.getWorkspaceDir(conversationID)

	// Clean the requested path
	cleanPath := filepath.Clean(requestedPath)

	// If path is absolute, try to make it relative to workspace or check if it's already in workspace
	// But simplest is to join with workspaceDir

	// However, users might pass "src/main.go" or "/src/main.go".
	// filepath.Join handles leading slashes by treating them as relative if joining
	// Wait, filepath.Join("/tmp", "/src") -> "/tmp/src" on Unix? No, it might be absolute.
	// We should trim leading slash.

	cleanPath = strings.TrimPrefix(cleanPath, "/")

	fullPath := filepath.Join(workspaceDir, cleanPath)

	// Security check: ensure fullPath is within workspaceDir
	// Note: filepath.Join calls Clean, so ".." are resolved.
	// But we should double check Rel.
	rel, err := filepath.Rel(workspaceDir, fullPath)
	if err != nil {
		return "", fmt.Errorf("invalid path")
	}
	if strings.HasPrefix(rel, "..") || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("path outside workspace")
	}

	// Defense in depth against symlink-based traversal: filepath.Rel is purely lexical,
	// so a symlink inside workspaceDir that points outside would bypass the check above.
	// Resolve symlinks and re-check containment. Tolerate ENOENT for paths that don't yet
	// exist (callers like file-create rely on resolving the parent only).
	if resolved, evalErr := filepath.EvalSymlinks(fullPath); evalErr == nil {
		resolvedWorkspace, wsErr := filepath.EvalSymlinks(workspaceDir)
		if wsErr != nil {
			resolvedWorkspace = workspaceDir
		}
		relResolved, relErr := filepath.Rel(resolvedWorkspace, resolved)
		if relErr != nil || strings.HasPrefix(relResolved, "..") || strings.HasPrefix(relResolved, "/") {
			return "", fmt.Errorf("path outside workspace")
		}
	}

	return fullPath, nil
}

// HandleListFiles lists files in a directory
func (h *FileHandler) HandleListFiles(c *gin.Context) {
	dirPath := c.Query("path")
	if dirPath == "" {
		dirPath = "."
	}

	conversationID := c.Query("conversation_id")
	recursive := c.Query("recursive") == "true"

	// Optional regex pattern for filtering
	pattern := c.Query("pattern")
	var regex *regexp.Regexp
	var err error
	if pattern != "" {
		regex, err = regexp.Compile(pattern)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid regex pattern: %v", err)})
			return
		}
	}

	fullPath, err := h.resolvePath(dirPath, conversationID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Directory not found"})
			return
		}
		slog.Error("file_handler: failed to access path", "path", fullPath, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to access directory"})
		return
	}

	if !info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Path is not a directory"})
		return
	}

	var files []FileInfo
	maxFiles := 1000 // Limit results to prevent overwhelming response

	if recursive {
		err = filepath.WalkDir(fullPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // Skip errors
			}
			if path == fullPath {
				return nil // Skip root
			}
			if len(files) >= maxFiles {
				return filepath.SkipAll
			}

			// Filter hidden directories/files unless explicitly requested
			if strings.HasPrefix(d.Name(), ".") && d.Name() != "." && d.Name() != ".." {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			// Apply regex filter to filename
			if regex != nil && !regex.MatchString(d.Name()) {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				return nil
			}

			fileType := "file"
			if d.IsDir() {
				fileType = "dir"
			}

			// Calculate relative path from the listing root
			relPath, _ := filepath.Rel(fullPath, path)
			// Prepend the requested dirPath to maintain context
			finalPath := filepath.Join(dirPath, relPath)
			finalPath = strings.TrimPrefix(finalPath, "./")

			files = append(files, FileInfo{
				Name:  d.Name(),
				Path:  finalPath,
				Type:  fileType,
				Size:  info.Size(),
				Mtime: info.ModTime().Format("2006-01-02T15:04:05Z07:00"),
			})
			return nil
		})
	} else {
		var entries []os.DirEntry
		entries, err = os.ReadDir(fullPath)
		if err == nil {
			for _, entry := range entries {
				// Filter by regex if pattern provided
				if regex != nil && !regex.MatchString(entry.Name()) {
					continue
				}

				info, err := entry.Info()
				if err != nil {
					continue
				}

				fileType := "file"
				if entry.IsDir() {
					fileType = "dir"
				}

				// Calculate relative path for response
				relPath := filepath.Join(dirPath, entry.Name())
				relPath = strings.TrimPrefix(relPath, "./")

				files = append(files, FileInfo{
					Name:  entry.Name(),
					Path:  relPath,
					Type:  fileType,
					Size:  info.Size(),
					Mtime: info.ModTime().Format("2006-01-02T15:04:05Z07:00"),
				})
			}
		}
	}

	if err != nil {
		slog.Error("file_handler: failed to list directory", "path", fullPath, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list directory contents"})
		return
	}

	// Sort: directories first, then files (alphabetical)
	sort.Slice(files, func(i, j int) bool {
		if files[i].Type != files[j].Type {
			return files[i].Type == "dir"
		}
		return files[i].Name < files[j].Name
	})

	c.JSON(http.StatusOK, gin.H{"files": files})
}

// HandleSaveFile creates or updates a file
func (h *FileHandler) HandleSaveFile(c *gin.Context) {
	var req SaveFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	fullPath, err := h.resolvePath(req.Path, req.ConversationID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Ensure directory exists
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		slog.Error("file_handler: failed to create directory", "path", dir, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create directory"})
		return
	}

	// Write file
	if err := os.WriteFile(fullPath, []byte(req.Content), 0644); err != nil {
		slog.Error("file_handler: failed to write file", "path", fullPath, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write file"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "path": req.Path})
}

// HandleDeleteFile deletes a file or directory
func (h *FileHandler) HandleDeleteFile(c *gin.Context) {
	filePath := c.Query("path")
	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}
	conversationID := c.Query("conversation_id")

	fullPath, err := h.resolvePath(filePath, conversationID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Basic safety check: don't delete workspace root
	if fullPath == filepath.Clean(h.getWorkspaceDir(conversationID)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Cannot delete workspace root"})
		return
	}

	if err := os.RemoveAll(fullPath); err != nil {
		slog.Error("file_handler: failed to delete path", "path", fullPath, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete path"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "path": filePath})
}

// HandleGetFile returns file content
func (h *FileHandler) HandleGetFile(c *gin.Context) {
	filePath := c.Query("path")
	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}
	conversationID := c.Query("conversation_id")

	fullPath, err := h.resolvePath(filePath, conversationID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	file, err := os.Open(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
			return
		}
		slog.Error("file_handler: failed to open file", "path", fullPath, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open file"})
		return
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		slog.Error("file_handler: failed to read file info", "path", fullPath, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read file info"})
		return
	}

	if info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Path is a directory, use /files/list instead"})
		return
	}

	// Stream content
	c.Header("Content-Type", "text/plain")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", info.Name()))

	_, err = io.Copy(c.Writer, file)
	if err != nil {
		// Cannot write JSON error if streaming started, but good to log
		slog.Error("file_handler: failed to stream file", "error", err)
	}
}

// HandleBatchReadFile reads multiple files
func (h *FileHandler) HandleBatchReadFile(c *gin.Context) {
	var req BatchReadFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	results := make([]FileContent, 0, len(req.Paths))
	var totalSize int64

	for _, path := range req.Paths {
		result := FileContent{Path: path}

		fullPath, err := h.resolvePath(path, req.ConversationID)
		if err != nil {
			result.Error = err.Error()
			results = append(results, result)
			continue
		}

		// Security: check file size before reading to prevent memory exhaustion
		info, err := os.Stat(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				result.Error = "File not found"
			} else {
				slog.Error("file_handler: failed to access file", "path", fullPath, "error", err)
				result.Error = "Failed to access file"
			}
			results = append(results, result)
			continue
		}

		if totalSize+info.Size() > 50*1024*1024 { // 50MB total limit
			result.Error = "Total batch size limit exceeded"
			results = append(results, result)
			break
		}
		totalSize += info.Size()

		if info.Size() > 10*1024*1024 { // 10MB limit per file
			result.Error = "File too large for batch read"
			results = append(results, result)
			continue
		}

		content, err := os.ReadFile(fullPath)
		if err != nil {
			slog.Error("file_handler: failed to read file", "path", fullPath, "error", err)
			result.Error = "Failed to read file"
			results = append(results, result)
			continue
		}

		// Ensure content is valid UTF-8 for JSON compatibility
		if !utf8.Valid(content) {
			result.Error = "File appears to be binary and cannot be returned as text"
			results = append(results, result)
			continue
		}

		result.Content = string(content)
		results = append(results, result)
	}

	c.JSON(http.StatusOK, gin.H{"files": results})
}
