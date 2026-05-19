package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"nudgebee/code-analysis-agent/config"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestHandleExecute(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Setup temp dir for workspace
	tempDir, err := os.MkdirTemp("", "exec_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	cfg := &config.Config{}
	cfg.Analysis.WorkspaceDir = tempDir
	// Set execution workspace dir explicitly for test isolation
	cfg.Execution.WorkspaceDir = filepath.Join(tempDir, "exec_workspaces")

	handler := NewExecutionHandler(cfg)
	router := gin.New()
	router.POST("/execute", handler.HandleExecute)

	tests := []struct {
		name          string
		req           ExecutionRequest
		wantStatus    int
		wantCmdStatus string
		wantOutput    string
		checkFile     string
	}{
		{
			name: "Basic Command",
			req: ExecutionRequest{
				Command:        "echo hello world",
				ConversationID: "test-convo-1",
			},
			wantStatus:    http.StatusOK,
			wantCmdStatus: "success",
			wantOutput:    "hello world\n",
		},
		{
			name: "Directory Isolation",
			req: ExecutionRequest{
				Command:        "touch testfile.txt",
				ConversationID: "test-convo-2",
			},
			wantStatus:    http.StatusOK,
			wantCmdStatus: "success",
			checkFile:     filepath.Join(tempDir, "exec_workspaces", "test-convo-2", "testfile.txt"),
		},
		{
			name: "Environment Variables",
			req: ExecutionRequest{
				Command:        "echo $TEST_VAR",
				ConversationID: "test-convo-3",
				Env:            map[string]string{"TEST_VAR": "custom_val"},
			},
			wantStatus:    http.StatusOK,
			wantCmdStatus: "success",
			wantOutput:    "custom_val\n",
		},
		{
			name: "Invalid Conversation ID",
			req: ExecutionRequest{
				Command:        "ls",
				ConversationID: "../invalid",
			},
			wantStatus:    http.StatusOK,
			wantCmdStatus: "failed",
		},
		{
			name: "Empty Command",
			req: ExecutionRequest{
				Command:        "",
				ConversationID: "test-convo-4",
			},
			wantStatus:    http.StatusOK,
			wantCmdStatus: "failed",
		},
		{
			name: "Path Traversal in Command",
			req: ExecutionRequest{
				Command:        "ls ../",
				ConversationID: "test-convo-5",
			},
			wantStatus:    http.StatusOK,
			wantCmdStatus: "failed",
		},
		{
			name: "Sensitive Path Access",
			req: ExecutionRequest{
				Command:        "cat /etc/passwd",
				ConversationID: "test-convo-6",
			},
			wantStatus:    http.StatusOK,
			wantCmdStatus: "failed",
		},
		{
			name: "Dangerous Command",
			req: ExecutionRequest{
				Command:        "sudo rm -rf /",
				ConversationID: "test-convo-7",
			},
			wantStatus:    http.StatusOK,
			wantCmdStatus: "failed",
		},
		// Bypass pattern tests
		{
			name: "Base64 Decode to Shell",
			req: ExecutionRequest{
				Command:        `echo "cm0gLXJmIC8=" | base64 -d | sh`,
				ConversationID: "test-bypass-1",
			},
			wantStatus:    http.StatusOK,
			wantCmdStatus: "failed",
		},
		{
			name: "Hex Escape Sequence",
			req: ExecutionRequest{
				Command:        `$'\x72\x6d' -rf /`,
				ConversationID: "test-bypass-3",
			},
			wantStatus:    http.StatusOK,
			wantCmdStatus: "failed",
		},
		{
			name: "Eval Wrapper",
			req: ExecutionRequest{
				Command:        `eval "rm -rf /"`,
				ConversationID: "test-bypass-4",
			},
			wantStatus:    http.StatusOK,
			wantCmdStatus: "failed",
		},
		{
			name: "Symlink to Sensitive Path",
			req: ExecutionRequest{
				Command:        `ln -s /etc/passwd link && cat link`,
				ConversationID: "test-bypass-5",
			},
			wantStatus:    http.StatusOK,
			wantCmdStatus: "failed",
		},
		{
			name: "Pipe to Bash",
			req: ExecutionRequest{
				Command:        `cat malicious.sh | bash`,
				ConversationID: "test-bypass-6",
			},
			wantStatus:    http.StatusOK,
			wantCmdStatus: "failed",
		},
		{
			name: "Pipe to Absolute Path Shell",
			req: ExecutionRequest{
				Command:        `echo "test" | /bin/sh`,
				ConversationID: "test-bypass-abs-pipe",
			},
			wantStatus:    http.StatusOK,
			wantCmdStatus: "failed",
		},
		{
			name: "Pipe to Absolute Path Bash",
			req: ExecutionRequest{
				Command:        `echo "test" | /usr/bin/bash`,
				ConversationID: "test-bypass-abs-pipe-bash",
			},
			wantStatus:    http.StatusOK,
			wantCmdStatus: "failed",
		},
		{
			name: "Direct Shell Invocation with -c",
			req: ExecutionRequest{
				Command:        `bash -c "echo hello"`,
				ConversationID: "test-bypass-shell-c",
			},
			wantStatus:    http.StatusOK,
			wantCmdStatus: "failed",
		},
		{
			name: "Absolute Path Shell Invocation with -c",
			req: ExecutionRequest{
				Command:        `/bin/bash -c "echo hello"`,
				ConversationID: "test-bypass-abs-shell-c",
			},
			wantStatus:    http.StatusOK,
			wantCmdStatus: "failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.req)
			req, _ := http.NewRequest(http.MethodPost, "/execute", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.wantStatus, w.Code)

			var resp ExecutionResponse
			err := json.Unmarshal(w.Body.Bytes(), &resp)
			assert.NoError(t, err)

			assert.Equal(t, tt.wantCmdStatus, resp.CommandStatus)
			if tt.wantOutput != "" {
				assert.Equal(t, tt.wantOutput, resp.Response)
			}

			if tt.checkFile != "" {
				_, err := os.Stat(tt.checkFile)
				assert.NoError(t, err, "Expected file to exist: %s", tt.checkFile)
			}
		})
	}
}
