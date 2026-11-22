package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/config"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// Exercise the actual planner and handler against a model that returns prose
// instead of tool calls. Its budget exit synthesizes an insufficient-evidence
// submission; that synthetic step must not turn a tool-less run into a success.
func TestHandleAgenticAnalyze_ToolUse(t *testing.T) {
	for _, tc := range []struct {
		name       string
		repository bool
		readFile   bool
		wantFailed bool
	}{
		{"repository without tools", true, false, true},
		{"insufficient evidence after reading code", true, true, false},
		{"log-only without tools", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Tools []llms.Tool `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode model request: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				choice := &llms.ContentChoice{Content: "code_agent"}
				if len(request.Tools) > 0 {
					choice.Content = "I cannot confirm the cause."
					if tc.readFile {
						choice.ToolCalls = []llms.ToolCall{{
							ID: "read-1", Type: "function",
							FunctionCall: &llms.FunctionCall{Name: "file_view", Arguments: `{"file_path":"main.go"}`},
						}}
					}
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(llms.ContentResponse{Choices: []*llms.ContentChoice{choice}}); err != nil {
					t.Errorf("encode model response: %v", err)
				}
			}))
			defer srv.Close()
			t.Setenv("NB_LLM_SERVER_URL", srv.URL)
			t.Setenv("NB_WORKSPACE_TOKEN", "test-token")
			t.Setenv("NB_ACCOUNT_ID", "test-account")

			cfg := &config.Config{}
			cfg.LLM.Provider = "custom"
			cfg.LLM.Model = "fallback-model"
			cfg.Analysis.WorkspaceDir = t.TempDir()
			cfg.Analysis.MaxProcessingTime = 10 * time.Second
			cfg.Agent.ReActMaxIterations = 2
			handler, err := NewAgenticAnalyzeHandler(cfg, nil, nil)
			require.NoError(t, err)
			req := AgenticAnalyzeRequest{Prompt: "Investigate the crash", Logs: "process exited", Mode: "explore"}
			req.LLMConfig = &LLMConfigOverride{Model: "tenant-model"}
			if tc.repository {
				req.GitRepository.LocalPath = cfg.Analysis.WorkspaceDir
				require.NoError(t, os.WriteFile(filepath.Join(req.GitRepository.LocalPath, "main.go"), []byte("package main\n"), 0600))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			response, err := handler.HandleAgenticAnalyze(ctx, req)
			require.NoError(t, err)
			require.NotNil(t, response)
			if tc.wantFailed {
				require.False(t, response.Success)
				require.Contains(t, response.Error, "tenant-model")
				require.Contains(t, response.Error, "no repository tools")
				require.Nil(t, response.AgentResponse)

				// /analyze must expose this as a failed /status response, which
				// llm-server already treats as an error and guards against retry.
				req.CloudAccountID, req.Tenant = "test-account", "test-tenant"
				req.GitCredentials.Type = "none"
				body, err := json.Marshal(req)
				require.NoError(t, err)
				router := gin.New()
				router.POST("/analyze", handler.HandleAnalyze)
				router.GET("/status/:id", handler.HandleStatus)
				w := httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodPost, "/analyze", bytes.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				router.ServeHTTP(w, r)
				require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
				var accepted struct {
					AnalysisID string `json:"analysis_id"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &accepted))
				defer common.CleanupAnalysis(accepted.AnalysisID)
				type statusReply struct {
					Status          string           `json:"status"`
					Error           string           `json:"error"`
					ToolInvocations []ToolInvocation `json:"tool_invocations"`
				}
				var status statusReply
				require.Eventually(t, func() bool {
					w := httptest.NewRecorder()
					router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/status/"+accepted.AnalysisID, nil))
					var current statusReply
					if err := json.Unmarshal(w.Body.Bytes(), &current); err != nil {
						return false
					}
					status = current
					return status.Status != "running"
				}, 5*time.Second, 10*time.Millisecond)
				require.Equal(t, "failed", status.Status)
				require.Contains(t, status.Error, "tenant-model")
				require.Contains(t, status.Error, "no repository tools")
				require.Empty(t, status.ToolInvocations)
				t.Logf("/status: status=%s error=%q tools=%d", status.Status, status.Error, len(status.ToolInvocations))
				return
			}
			require.True(t, response.Success, response.Error)
			require.NotNil(t, response.AgentResponse)
			require.Contains(t, response.AgentResponse.Answer, "Insufficient evidence")
			if tc.readFile {
				require.NotEmpty(t, response.ToolInvocations)
				require.Equal(t, "file_view", response.ToolInvocations[0].ToolName)
				require.Equal(t, "success", response.ToolInvocations[0].Status)
				require.Contains(t, response.AgentResponse.Caveats, "Files examined: main.go")
			}
		})
	}
}
