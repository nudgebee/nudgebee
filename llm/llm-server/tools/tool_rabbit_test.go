package tools

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// TestRabbitExecute_RoutesThroughRelay pins the behavioral contract of
// PR #34906: every rabbit_execute command — rabbitmqadmin and curl alike —
// must be routed through ExecuteContainerJob → relay → customer-env pod,
// regardless of LlmServerWorkspaceEnabled. The prior implementation
// branched on that flag and sent non-curl commands to the workspace pod,
// where curl (un-shimmed) executed locally against no RabbitMQ.
//
// Assertions use Contains so the test survives downstream credential
// injection in the relay adapter (which prefixes rabbitmqadmin with
// --host/--port/--username/--password and curl with -s -u user:pass);
// those transforms live outside this PR's scope.
func TestRabbitExecute_RoutesThroughRelay(t *testing.T) {
	tests := []struct {
		name       string
		args       map[string]any
		wantSubstr []string // all must appear in the captured relay command
		wantAbsent []string // none may appear
	}{
		{
			name:       "default command uses rabbitmqadmin as the CLI",
			args:       map[string]any{"instance": "prod", "args": "list queues"},
			wantSubstr: []string{"rabbitmqadmin", "list queues"},
		},
		{
			name:       "explicit command overrides the default",
			args:       map[string]any{"instance": "prod", "args": "list_queues", "command": "rabbitmqctl"},
			wantSubstr: []string{"rabbitmqctl", "list_queues"},
			wantAbsent: []string{"rabbitmqadmin"},
		},
		{
			name:       "curl args reach the relay without a CLI prefix",
			args:       map[string]any{"args": "curl http://$RABBITMQ_HOST:$RABBITMQ_PORT/api/overview"},
			wantSubstr: []string{"curl", "/api/overview", "$RABBITMQ_HOST", "$RABBITMQ_PORT"},
			wantAbsent: []string{"rabbitmqadmin", "rabbitmqctl"},
		},
		{
			name: "curl pipe to jq is preserved end-to-end (shell runs in relay pod)",
			args: map[string]any{
				"args": "curl http://$RABBITMQ_HOST:$RABBITMQ_PORT/api/queues | jq '.[].name'",
			},
			wantSubstr: []string{"curl", "/api/queues", "| jq"},
		},
		{
			name:       "curl detection tolerates leading whitespace",
			args:       map[string]any{"args": "   curl http://$RABBITMQ_HOST:$RABBITMQ_PORT/api/aliveness-test/%2F"},
			wantSubstr: []string{"curl", "/api/aliveness-test/%2F"},
			wantAbsent: []string{"rabbitmqadmin"},
		},
	}

	// Core PR claim: the workspace-vs-relay branch is gone. Both settings
	// of LlmServerWorkspaceEnabled must send every command to the relay.
	for _, workspaceEnabled := range []bool{false, true} {
		wsLabel := "workspace_off"
		if workspaceEnabled {
			wsLabel = "workspace_on"
		}
		t.Run(wsLabel, func(t *testing.T) {
			origWS := config.Config.LlmServerWorkspaceEnabled
			config.Config.LlmServerWorkspaceEnabled = workspaceEnabled
			defer func() { config.Config.LlmServerWorkspaceEnabled = origWS }()

			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					captured := captureRelayCommand(t, func(toolCtx core.NbToolContext) {
						_, _ = RabbitExecuteTool{}.Call(toolCtx, core.NBToolCallRequest{Arguments: tc.args})
					})
					for _, want := range tc.wantSubstr {
						assert.Contains(t, captured, want,
							"expected relay command to contain %q; got %q", want, captured)
					}
					for _, absent := range tc.wantAbsent {
						assert.NotContains(t, captured, absent,
							"relay command must NOT contain %q; got %q", absent, captured)
					}
				})
			}
		})
	}
}

// TestRabbitExecute_ValidationErrors covers the input-validation branches
// that must fail BEFORE the relay is contacted. Guards against a
// regression where an empty tool config or missing args would silently
// dispatch to the customer environment.
func TestRabbitExecute_ValidationErrors(t *testing.T) {
	t.Run("missing tool config errors before any relay call", func(t *testing.T) {
		relayCalled := false
		toolCtx := newRabbitTestContext(t, "", func(_ string) { relayCalled = true })

		_, err := RabbitExecuteTool{}.Call(toolCtx, core.NBToolCallRequest{
			Arguments: map[string]any{"args": "list queues"},
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no tool configs found")
		assert.False(t, relayCalled, "relay must NOT be called when tool config is missing")
	})

	t.Run("missing args errors before any relay call", func(t *testing.T) {
		relayCalled := false
		toolCtx := newRabbitTestContext(t, "prod", func(_ string) { relayCalled = true })

		_, err := RabbitExecuteTool{}.Call(toolCtx, core.NBToolCallRequest{
			Arguments: map[string]any{"instance": "prod"},
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing 'args' parameter")
		assert.False(t, relayCalled, "relay must NOT be called when args is empty")
	})
}

// TestRabbitExecute_ParsesInputCommandFallback covers the legacy JSON-in-Command
// input path. The executor sometimes hands the tool call in the old shape
// (input.Command = JSON blob, input.Arguments = nil); PR #34906 didn't touch
// this branch but its dispatch relies on the parsed args landing correctly,
// so pin it here.
func TestRabbitExecute_ParsesInputCommandFallback(t *testing.T) {
	captured := captureRelayCommand(t, func(toolCtx core.NbToolContext) {
		_, _ = RabbitExecuteTool{}.Call(toolCtx, core.NBToolCallRequest{
			Command: `{"instance":"prod","args":"list bindings"}`,
		})
	})
	assert.Contains(t, captured, "rabbitmqadmin",
		"legacy input.Command shape should still be parsed; got %q", captured)
	assert.Contains(t, captured, "list bindings",
		"legacy input.Command args should still reach the relay; got %q", captured)
}

// captureRelayCommand spins up an httptest relay, points config at it,
// invokes fn with a valid tool context, and returns the command string
// the relay received. Fails the test if the relay was not called.
func captureRelayCommand(t *testing.T, fn func(core.NbToolContext)) string {
	t.Helper()
	var got string
	toolCtx := newRabbitTestContext(t, "prod", func(cmd string) { got = cmd })
	fn(toolCtx)
	require.NotEmpty(t, strings.TrimSpace(got), "relay received no command")
	return got
}

// newRabbitTestContext builds a valid NbToolContext pointing at an
// httptest relay that invokes onCommand with the captured command string
// and returns a minimal success envelope. The server is closed on t.Cleanup.
func newRabbitTestContext(t *testing.T, toolConfigName string, onCommand func(string)) core.NbToolContext {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if bodyMap, ok := req["body"].(map[string]any); ok {
			if params, ok := bodyMap["action_params"].(map[string]any); ok {
				if cmd, ok := params["command"].(string); ok {
					onCommand(cmd)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"response": "ok", "exit_code": 0},
		})
	}))
	t.Cleanup(srv.Close)

	origEndpoint := config.Config.RelayServerEndpoint
	config.Config.RelayServerEndpoint = srv.URL
	t.Cleanup(func() { config.Config.RelayServerEndpoint = origEndpoint })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	secCtx := security.NewSecurityContextForSuperAdmin()
	reqCtx := security.NewRequestContext(context.Background(), secCtx, logger, nil, nil)

	return core.NbToolContext{
		AccountId: "test-account",
		Ctx:       reqCtx,
		ToolConfig: core.ToolConfig{
			Name: toolConfigName,
			Values: []core.ToolConfigValue{
				{Name: "k8s_secret", Value: "default/rabbitmq-secret"},
			},
		},
	}
}
