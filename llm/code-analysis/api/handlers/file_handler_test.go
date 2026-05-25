package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nudgebee/code-analysis-agent/config"

	"github.com/gin-gonic/gin"
)

func TestResolvePath(t *testing.T) {
	// Setup configuration with a mock workspace directory
	workspaceDir := "/tmp/code-analysis"
	execDir := "/tmp/code-analysis/exec_workspaces"

	cfg := &config.Config{
		Analysis: config.AnalysisConfig{
			WorkspaceDir: workspaceDir,
		},
		Execution: config.ExecutionConfig{
			WorkspaceDir: execDir,
		},
	}
	handler := NewFileHandler(cfg)

	tests := []struct {
		name           string
		inputPath      string
		conversationID string
		expectedPath   string
		expectError    bool
	}{
		{
			name:         "Simple relative path",
			inputPath:    "src/main.go",
			expectedPath: "/tmp/code-analysis/src/main.go",
			expectError:  false,
		},
		{
			name:         "Root relative path",
			inputPath:    "./src/main.go",
			expectedPath: "/tmp/code-analysis/src/main.go",
			expectError:  false,
		},
		{
			name:           "Conversation scoped path",
			inputPath:      "out.txt",
			conversationID: "conv123",
			expectedPath:   "/tmp/code-analysis/exec_workspaces/conv123/out.txt",
			expectError:    false,
		},
		{
			name:         "Dot dot traversal attempt",
			inputPath:    "../etc/passwd",
			expectedPath: "",
			expectError:  true,
		},
		{
			name:           "Dot dot traversal attempt in conversation",
			inputPath:      "../../etc/passwd",
			conversationID: "conv123",
			expectedPath:   "",
			expectError:    true,
		},
		{
			name:         "Workspace root",
			inputPath:    ".",
			expectedPath: "/tmp/code-analysis",
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, err := handler.resolvePath(tt.inputPath, tt.conversationID)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error for input '%s', but got none", tt.inputPath)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for input '%s': %v", tt.inputPath, err)
				}
				if path != tt.expectedPath {
					t.Errorf("Expected path '%s', got '%s'", tt.expectedPath, path)
				}
			}
		})
	}
}

func TestFileOperations(t *testing.T) {
	// Setup temporary workspace
	tempDir, err := os.MkdirTemp("", "code-analysis-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	cfg := &config.Config{
		Analysis: config.AnalysisConfig{
			WorkspaceDir: tempDir,
		},
	}
	handler := NewFileHandler(cfg)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/files/save", handler.HandleSaveFile)
	r.GET("/files/list", handler.HandleListFiles)
	r.POST("/files/read-batch", handler.HandleBatchReadFile)
	r.DELETE("/files/delete", handler.HandleDeleteFile)

	// 1. Test Save File
	t.Run("SaveFile", func(t *testing.T) {
		body := strings.NewReader(`{"path": "test.txt", "content": "hello world"}`)
		req, _ := http.NewRequest("POST", "/files/save", body)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected 200 OK, got %d", w.Code)
		}

		content, err := os.ReadFile(filepath.Join(tempDir, "test.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != "hello world" {
			t.Errorf("Expected 'hello world', got '%s'", string(content))
		}
	})

	// 2. Test List Files
	t.Run("ListFiles", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/files/list?path=.", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected 200 OK, got %d", w.Code)
		}

		var resp map[string][]FileInfo
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}

		if len(resp["files"]) != 1 {
			t.Errorf("Expected 1 file, got %d", len(resp["files"]))
		}
		if resp["files"][0].Name != "test.txt" {
			t.Errorf("Expected 'test.txt', got '%s'", resp["files"][0].Name)
		}
	})

	// 3. Test List Files with Regex
	t.Run("ListFilesRegex", func(t *testing.T) {
		// Create another file
		if err := os.WriteFile(filepath.Join(tempDir, "other.log"), []byte("log"), 0644); err != nil {
			t.Fatal(err)
		}

		req, _ := http.NewRequest("GET", "/files/list?path=.&pattern=.*\\.txt$", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected 200 OK, got %d", w.Code)
		}

		var resp map[string][]FileInfo
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}

		// Should only find test.txt
		foundTxt := false
		for _, f := range resp["files"] {
			if f.Name == "test.txt" {
				foundTxt = true
			}
			if f.Name == "other.log" {
				t.Error("Regex should have filtered out other.log")
			}
		}
		if !foundTxt {
			t.Error("Regex should have matched test.txt")
		}
	})

	// 4. Test Recursive List
	t.Run("ListFilesRecursive", func(t *testing.T) {
		// Create nested structure
		subDir := filepath.Join(tempDir, "subdir")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(subDir, "nested.txt"), []byte("nested"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tempDir, "root.txt"), []byte("root"), 0644); err != nil {
			t.Fatal(err)
		}

		// Test recursive=true
		req, _ := http.NewRequest("GET", "/files/list?path=.&recursive=true", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected 200 OK, got %d", w.Code)
		}

		var resp map[string][]FileInfo
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}

		// Should find root.txt, subdir, and nested.txt
		foundNested := false
		for _, f := range resp["files"] {
			if f.Name == "nested.txt" {
				foundNested = true
				if f.Path != "subdir/nested.txt" {
					t.Errorf("Expected path 'subdir/nested.txt', got '%s'", f.Path)
				}
			}
		}
		if !foundNested {
			t.Error("Recursive list should have found nested.txt")
		}

		// Test with regex
		req2, _ := http.NewRequest("GET", "/files/list?path=.&recursive=true&pattern=nested.*", nil)
		w2 := httptest.NewRecorder()
		r.ServeHTTP(w2, req2)

		if err := json.Unmarshal(w2.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		for _, f := range resp["files"] {
			if f.Name == "root.txt" {
				t.Error("Regex should filter out root.txt in recursive mode")
			}
		}
	})

	// 5. Test Batch Read
	t.Run("BatchRead", func(t *testing.T) {
		// Ensure files exist
		if err := os.WriteFile(filepath.Join(tempDir, "file1.txt"), []byte("content1"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tempDir, "file2.txt"), []byte("content2"), 0644); err != nil {
			t.Fatal(err)
		}

		body := strings.NewReader(`{"paths": ["file1.txt", "file2.txt", "missing.txt"]}`)
		req, _ := http.NewRequest("POST", "/files/read-batch", body)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected 200 OK, got %d", w.Code)
		}

		var resp map[string][]FileContent
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}

		if len(resp["files"]) != 3 {
			t.Errorf("Expected 3 results, got %d", len(resp["files"]))
		}

		results := make(map[string]FileContent)
		for _, f := range resp["files"] {
			results[f.Path] = f
		}

		if results["file1.txt"].Content != "content1" {
			t.Errorf("file1 content mismatch")
		}
		if results["file2.txt"].Content != "content2" {
			t.Errorf("file2 content mismatch")
		}
		if results["missing.txt"].Error == "" {
			t.Errorf("Expected error for missing file")
		}
	})

	// 6. Test Delete File
	t.Run("DeleteFile", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", "/files/delete?path=test.txt", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected 200 OK, got %d", w.Code)
		}

		if _, err := os.Stat(filepath.Join(tempDir, "test.txt")); !os.IsNotExist(err) {
			t.Error("File should have been deleted")
		}
	})

	// 7. Test Batch Read Limits and Binary
	t.Run("BatchReadLimitsAndBinary", func(t *testing.T) {
		// Create a large file (over 10MB)
		largeFile := filepath.Join(tempDir, "large.txt")
		f, err := os.Create(largeFile)
		if err != nil {
			t.Fatal(err)
		}
		// Write 11MB dummy data (sparse file is fast)
		if err := f.Truncate(11 * 1024 * 1024); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}

		// Create a binary file
		binaryFile := filepath.Join(tempDir, "binary.bin")
		// 0xFF 0xFE is a valid UTF-16 BOM, but 0xFF is invalid in UTF-8
		if err := os.WriteFile(binaryFile, []byte{0xFF, 0xFE, 0xFD}, 0644); err != nil {
			t.Fatal(err)
		}

		body := strings.NewReader(`{"paths": ["large.txt", "binary.bin"]}`)
		req, _ := http.NewRequest("POST", "/files/read-batch", body)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected 200 OK, got %d", w.Code)
		}

		var resp map[string][]FileContent
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}

		pathMap := make(map[string]FileContent)
		for _, fc := range resp["files"] {
			pathMap[fc.Path] = fc
		}

		if fc, ok := pathMap["large.txt"]; ok {
			if !strings.Contains(strings.ToLower(fc.Error), "too large") {
				t.Errorf("Expected 'too large' error for large.txt, got '%s'", fc.Error)
			}
		} else {
			t.Error("Missing result for large.txt")
		}

		if fc, ok := pathMap["binary.bin"]; ok {
			if !strings.Contains(strings.ToLower(fc.Error), "binary") {
				t.Errorf("Expected 'binary' error for binary.bin, got '%s'", fc.Error)
			}
		} else {
			t.Error("Missing result for binary.bin")
		}
	})
}
