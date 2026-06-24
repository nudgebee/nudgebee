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

// TestHandleExecute_IsolatedEnv pins the per-conversation isolation
// contract: TMPDIR / TMP / TEMP / HOME / PWD all point at the
// per-conversation work directory (so Python tempfile, mktemp, sort -T,
// pip cache, npm cache, kubeconfig, etc. all default into the
// conversation dir, not /tmp or some other shared location), and
// LC_ALL=C.UTF-8 is set for cross-pod determinism. The contract is the
// server's, so req.Env may not override these — even if the caller
// asks.
//
// This guards the workspace contract that the llm-server publishes to
// the LLM via ShellTool.Description():
//
//   - "per-conversation directory"
//   - "/tmp/ … shared with other conversations on the same account"
//   - ".nb_profile"
//
// If TMPDIR/TMP/TEMP stop pointing at workDir, Python tempfile leaks
// to /tmp and the prompt is silently lying again. This test catches
// that.
func TestHandleExecute_IsolatedEnv(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tempDir, err := os.MkdirTemp("", "exec_test_env")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	cfg := &config.Config{}
	cfg.Analysis.WorkspaceDir = tempDir
	cfg.Execution.WorkspaceDir = filepath.Join(tempDir, "exec_workspaces")

	handler := NewExecutionHandler(cfg)
	router := gin.New()
	router.POST("/execute", handler.HandleExecute)

	exec := func(req ExecutionRequest) ExecutionResponse {
		t.Helper()
		body, _ := json.Marshal(req)
		httpReq, _ := http.NewRequest(http.MethodPost, "/execute", bytes.NewBuffer(body))
		httpReq.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httpReq)
		var resp ExecutionResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		return resp
	}

	convID := "convo-env-isolation"
	expectedWorkDir := filepath.Join(tempDir, "exec_workspaces", convID)

	t.Run("defaults: TMPDIR/TMP/TEMP/HOME/PWD all point at the per-conversation directory", func(t *testing.T) {
		// One echo emits all five values on separate lines so the test
		// fails informatively when only one drifts.
		cmd := `echo "HOME=$HOME"; echo "PWD=$PWD"; echo "TMPDIR=$TMPDIR"; echo "TMP=$TMP"; echo "TEMP=$TEMP"`
		resp := exec(ExecutionRequest{Command: cmd, ConversationID: convID})

		assert.Equal(t, "success", resp.CommandStatus, "response: %+v", resp)
		assert.Contains(t, resp.Response, "HOME="+expectedWorkDir)
		assert.Contains(t, resp.Response, "PWD="+expectedWorkDir)
		assert.Contains(t, resp.Response, "TMPDIR="+expectedWorkDir,
			"TMPDIR must point at the per-conversation directory so Python tempfile / mktemp default into it")
		assert.Contains(t, resp.Response, "TMP="+expectedWorkDir,
			"TMP must point at the per-conversation directory for cross-platform tools that check TMP not TMPDIR")
		assert.Contains(t, resp.Response, "TEMP="+expectedWorkDir,
			"TEMP must point at the per-conversation directory for cross-platform tools that check TEMP")
	})

	t.Run("LC_ALL is C.UTF-8 for cross-pod determinism", func(t *testing.T) {
		resp := exec(ExecutionRequest{Command: `echo "LC_ALL=$LC_ALL"`, ConversationID: convID})
		assert.Equal(t, "success", resp.CommandStatus, "response: %+v", resp)
		assert.Contains(t, resp.Response, "LC_ALL=C.UTF-8")
	})

	t.Run("req.Env cannot override the isolation contract", func(t *testing.T) {
		// The caller asks for TMPDIR=/etc and HOME=/etc — both should be
		// ignored and the server's per-conversation values should win.
		resp := exec(ExecutionRequest{
			Command:        `echo "TMPDIR=$TMPDIR"; echo "HOME=$HOME"; echo "LC_ALL=$LC_ALL"`,
			ConversationID: convID,
			Env: map[string]string{
				"TMPDIR": "/etc",
				"TMP":    "/etc",
				"TEMP":   "/etc",
				"HOME":   "/etc",
				"LC_ALL": "en_US.UTF-8",
			},
		})
		assert.Equal(t, "success", resp.CommandStatus, "response: %+v", resp)
		assert.NotContains(t, resp.Response, "/etc",
			"req.Env must NOT override the per-conversation isolation contract; rendered env contained %q", resp.Response)
		assert.Contains(t, resp.Response, "TMPDIR="+expectedWorkDir)
		assert.Contains(t, resp.Response, "HOME="+expectedWorkDir)
		assert.Contains(t, resp.Response, "LC_ALL=C.UTF-8")
	})

	t.Run("non-reserved env vars in req.Env pass through unchanged", func(t *testing.T) {
		// A caller-provided variable that is NOT part of the isolation
		// contract is allowed through — otherwise we lose legitimate
		// per-call env injection (CLI config, feature flags, etc.).
		resp := exec(ExecutionRequest{
			Command:        `echo "MY_CUSTOM_VAR=$MY_CUSTOM_VAR"`,
			ConversationID: convID,
			Env:            map[string]string{"MY_CUSTOM_VAR": "caller-provided"},
		})
		assert.Equal(t, "success", resp.CommandStatus, "response: %+v", resp)
		assert.Contains(t, resp.Response, "MY_CUSTOM_VAR=caller-provided")
	})
}
