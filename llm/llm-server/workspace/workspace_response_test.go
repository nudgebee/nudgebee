package workspace

import (
	stderrors "errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClassifyExecuteResponse locks the failure-detector truth table.
// Every failure row must wrap ErrWorkspaceCommandFailed so callers can
// errors.Is-discriminate workspace failures from transport errors.
func TestClassifyExecuteResponse(t *testing.T) {
	tests := []struct {
		name          string
		commandStatus string
		errMsg        string
		wantErr       bool
	}{
		// Success shapes.
		{"success status, no error", "success", "", false},
		{"empty status, no error (legacy/minimal agent)", "", "", false},

		// Workspace-reported failures — the bug fixes precisely this row family.
		{"failed status, no error", "failed", "", true},
		{"failed status, error message", "failed", "kubectl: pods 'app-108' not found", true},
		{"empty status, error message", "", "transport hiccup", true},
		{"success status, error message (defensive — agent contradicts itself)", "success", "stderr captured", true},
		{"unknown status string", "weird-state", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyExecuteResponse(executeResponse{
				CommandStatus: tc.commandStatus,
				Error:         tc.errMsg,
			})
			if tc.wantErr {
				require.Error(t, got)
				assert.ErrorIs(t, got, ErrWorkspaceCommandFailed,
					"every workspace command failure must wrap the sentinel so callers can discriminate via errors.Is")
				if tc.commandStatus != "" {
					assert.Contains(t, got.Error(), tc.commandStatus, "error message should surface the workspace status so callers/logs can triage")
				}
				if tc.errMsg != "" {
					assert.Contains(t, got.Error(), tc.errMsg, "error message should surface the workspace error so the LLM doesn't hallucinate from infra error text")
				}
			} else {
				assert.NoError(t, got)
			}
		})
	}
}

// TestIsAgentContractViolation pins the narrow Warn-vs-Info split:
// only status="success" + non-empty Error warrants Warn.
func TestIsAgentContractViolation(t *testing.T) {
	tests := []struct {
		name   string
		status string
		errMsg string
		want   bool
	}{
		{"success + error (the contradiction)", "success", "stderr captured", true},
		{"success + no error", "success", "", false},
		{"failed + error (routine command failure)", "failed", "kubectl 404", false},
		{"empty + error (legacy agent reporting failure)", "", "boom", false},
		{"failed + no error", "failed", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isAgentContractViolation(executeResponse{
				CommandStatus: tc.status,
				Error:         tc.errMsg,
			})
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestIsRecoverableError_WorkspaceCommandFailedSentinel guards against
// the keyword-collision regression: a workspace failure containing
// "unknown" / "eof" / "not ready" in its message must NOT trigger the
// pod-recreate path. The errors.Is short-circuit handles it.
func TestIsRecoverableError_WorkspaceCommandFailedSentinel(t *testing.T) {
	mgr := &workspaceManager{}

	t.Run("sentinel error is not recoverable, even with collision keywords in the message", func(t *testing.T) {
		// kubectl-apply-unknown-field shape — the canonical collision case.
		err := fmt.Errorf("%w: status=%q error=%q",
			ErrWorkspaceCommandFailed, "failed",
			"error validating data: ValidationError(Pod): unknown field \"spec.foo\"")
		assert.False(t, mgr.isRecoverableError(err),
			"sentinel must short-circuit before substring matcher")
	})

	t.Run("nil error is not recoverable", func(t *testing.T) {
		assert.False(t, mgr.isRecoverableError(nil))
	})

	t.Run("plain transport error containing recoverable keyword still recovers", func(t *testing.T) {
		// Non-sentinel keyword match — must still recover.
		err := stderrors.New("dial tcp: i/o timeout")
		assert.True(t, mgr.isRecoverableError(err))
	})
}

// TestExecuteDirect_WiresClassifierOntoDecodedResponse drives the
// executeDirect → unmarshal → classify path against an httptest fake
// workspace. Regression guard against a future refactor that drops the
// classify call from the unmarshal block.
func TestExecuteDirect_WiresClassifierOntoDecodedResponse(t *testing.T) {
	tests := []struct {
		name           string
		responseBody   string
		wantResponse   string
		wantErr        bool
		wantErrContain []string
	}{
		{
			name:         "successful command",
			responseBody: `{"command_status":"success","response":"hello world","conversation_id":"c1"}`,
			wantResponse: "hello world",
			wantErr:      false,
		},
		{
			name:         "successful command with empty stdout",
			responseBody: `{"command_status":"success","response":"","conversation_id":"c1"}`,
			wantResponse: "",
			wantErr:      false,
		},
		{
			name:         "legacy / minimal agent without command_status",
			responseBody: `{"response":"legacy output","conversation_id":"c1"}`,
			wantResponse: "legacy output",
			wantErr:      false,
		},
		{
			name:           "workspace-reported failure (the #30135 leak)",
			responseBody:   `{"command_status":"failed","response":"","error":"kubectl: pods 'app-108' not found","conversation_id":"c1"}`,
			wantResponse:   "",
			wantErr:        true,
			wantErrContain: []string{"failed", "pods 'app-108' not found"},
		},
		{
			name:           "workspace-reported failure with partial stdout",
			responseBody:   `{"command_status":"failed","response":"partial output before error","error":"oom-killed","conversation_id":"c1"}`,
			wantResponse:   "partial output before error",
			wantErr:        true,
			wantErrContain: []string{"failed", "oom-killed"},
		},
		{
			name:           "error field set even though status says success (defensive)",
			responseBody:   `{"command_status":"success","response":"x","error":"stderr captured","conversation_id":"c1"}`,
			wantResponse:   "x",
			wantErr:        true,
			wantErrContain: []string{"stderr captured"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.responseBody))
			}))
			defer srv.Close()

			mgr := &workspaceManager{httpClient: srv.Client()}
			ctx := security.NewRequestContextForSuperAdmin()

			gotResp, gotErr := mgr.executeDirect(ctx, srv.URL, []byte(`{"command":"test"}`), "test-token", "test-pod")

			assert.Equal(t, tc.wantResponse, gotResp, "response string must always be returned, even alongside an error, so callers can include partial stdout in error messages")
			if tc.wantErr {
				require.Error(t, gotErr)
				assert.ErrorIs(t, gotErr, ErrWorkspaceCommandFailed,
					"executeDirect must propagate the sentinel through the wrapper so isRecoverableError and the proxy fallback can short-circuit")
				for _, want := range tc.wantErrContain {
					assert.Contains(t, gotErr.Error(), want, "synthesized error must surface the workspace-reported context for triage")
				}
			} else {
				assert.NoError(t, gotErr)
			}
		})
	}
}

// TestExecuteDirect_TransportFailureNotMistakenForCommandFailure pins
// the inverse — transport / decode failures must NOT wrap the sentinel,
// so the proxy fallback still fires for them.
func TestExecuteDirect_TransportFailureNotMistakenForCommandFailure(t *testing.T) {
	t.Run("non-200 status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer srv.Close()

		mgr := &workspaceManager{httpClient: srv.Client()}
		ctx := security.NewRequestContextForSuperAdmin()

		gotResp, gotErr := mgr.executeDirect(ctx, srv.URL, []byte(`{"command":"test"}`), "", "test-pod")
		assert.Equal(t, "", gotResp)
		require.Error(t, gotErr)
		assert.NotErrorIs(t, gotErr, ErrWorkspaceCommandFailed,
			"transport failures must NOT wrap the sentinel — otherwise the proxy fallback in ExecuteCommand short-circuits when it should retry")
		assert.Contains(t, gotErr.Error(), "500", "non-200 must surface the HTTP status")
	})

	t.Run("invalid JSON body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`not-json`))
		}))
		defer srv.Close()

		mgr := &workspaceManager{httpClient: srv.Client()}
		ctx := security.NewRequestContextForSuperAdmin()

		gotResp, gotErr := mgr.executeDirect(ctx, srv.URL, []byte(`{"command":"test"}`), "", "test-pod")
		assert.Equal(t, "", gotResp)
		require.Error(t, gotErr)
		assert.NotErrorIs(t, gotErr, ErrWorkspaceCommandFailed,
			"decode failures must NOT wrap the sentinel — otherwise the proxy fallback short-circuits and the legitimately recoverable transport error is lost")
	})
}
