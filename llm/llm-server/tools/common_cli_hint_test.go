package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGenericCliRecoveryHint_FiresOnCliSignal pins the trigger contract: fire
// only when the raw error carries CLI signal (usage, unknown, invalid, etc.).
// Genuinely opaque errors (network timeouts, empty output) must NOT get
// wrapped in noise.
func TestGenericCliRecoveryHint_FiresOnCliSignal(t *testing.T) {
	shouldFire := []struct {
		name string
		raw  string
	}{
		{"unknown flag", "unknown flag: --foo"},
		{"usage line", "Usage: mytool [flags]\n\nRun 'mytool --help' for more"},
		{"available options list", "invalid argument. Available options: a, b, c"},
		{"did you mean suggestion", "unknown command 'lst'. Did you mean 'list'?"},
		{"expected token", "expected identifier at position 1"},
		{"not a valid value", "'foo' is not a valid value for --tier"},
		{"lowercase error: prefix", "error: could not parse configuration"},
		{"fatal: prefix (git-style)", "fatal: repository not found"},
		{"syntax error phrasing", "syntax error at or near \"SELECT\""},
		// Relay-wrapped stderr — outer `Error:` prefix legitimately signals
		// CLI content even though a `"error":` JSON key sits inside. The
		// boundary anchor matches the outer `Error:` (start-of-string, then
		// `error:` after ToLower) so the wrap still fires here.
		{"relay-wrapped error preserves outer signal despite inner json key",
			`Error: Server returned 500: {"error":"proxy db_query failed"}`},
		// Multiline stderr where `error:` starts a line — newline before
		// the prefix must still count as a boundary.
		{"newline-preceded error: prefix on second line",
			"initializing plugin...\nerror: plugin binary corrupted"},
	}
	for _, c := range shouldFire {
		t.Run(c.name, func(t *testing.T) {
			got := genericCliRecoveryHint(c.raw, "mytool", "mytool --help")
			assert.NotEmpty(t, got, "CLI-signal error should trigger the fallback hint")
			assert.Contains(t, got, "re-read the raw output")
			assert.Contains(t, got, "mytool --help", "help invocation must land verbatim when provided")
		})
	}

	shouldNotFire := []struct {
		name string
		raw  string
	}{
		{"empty error", ""},
		{"whitespace only", "   \n\t "},
		{"opaque backend timeout", "connection timeout after 30s"},
		{"generic 500", "Server returned 500"},
		{"tls handshake", "tls: handshake failure"},
		// JSON-payload false-positive class — the `"error":` key in a
		// relay-wrapped opaque error would previously trigger `error:`
		// substring match. Post-fix, the boundary-anchored regex requires
		// whitespace / start-of-string BEFORE the prefix (`"` doesn't count),
		// so bare JSON payloads pass through as opaque.
		{"json error key with opaque payload — was false-positive", `{"error":"connection timeout"}`},
		{"json fatal key with opaque payload", `{"fatal":"tls handshake"}`},
		{"json usage key inside object", `{"usage":"quota exceeded"}`},
		{"nested json error key", `response: {"data":null,"error":"pipe closed"}`},
	}
	for _, c := range shouldNotFire {
		t.Run(c.name, func(t *testing.T) {
			got := genericCliRecoveryHint(c.raw, "mytool", "mytool --help")
			assert.Empty(t, got, "opaque errors must NOT trigger the fallback hint (would wrap noise)")
		})
	}
}

// TestGenericCliRecoveryHint_HelpInvocationHandling pins the per-tool help
// syntax discipline. NOT ALL CLIs USE --help: sqlcmd uses -?, sqlplus uses
// -H, mysql/psql have \h/\? in interactive mode, and SQL query wrappers have
// no CLI help at all. The helper must land the caller's exact invocation
// verbatim rather than hardcode any convention.
func TestGenericCliRecoveryHint_HelpInvocationHandling(t *testing.T) {
	raw := "unknown flag: --foo"

	t.Run("cobra --help convention", func(t *testing.T) {
		got := genericCliRecoveryHint(raw, "kubectl", "kubectl <command> --help")
		assert.Contains(t, got, "kubectl <command> --help")
		assert.NotContains(t, got, "-?", "must not leak sqlcmd convention into a Cobra tool")
	})
	t.Run("sqlcmd -? convention", func(t *testing.T) {
		got := genericCliRecoveryHint(raw, "sqlcmd", "sqlcmd -?")
		assert.Contains(t, got, "sqlcmd -?")
		assert.NotContains(t, got, "--help", "must not leak Cobra convention into sqlcmd")
	})
	t.Run("sqlplus -H convention", func(t *testing.T) {
		got := genericCliRecoveryHint(raw, "sqlplus", "sqlplus -H")
		assert.Contains(t, got, "sqlplus -H")
	})
	t.Run("interactive SQL — \\h notation preserved", func(t *testing.T) {
		got := genericCliRecoveryHint(raw, "mysql", "mysql --help or \\h in interactive mode")
		assert.Contains(t, got, `mysql --help or \h in interactive mode`)
	})
	t.Run("no help invocation — hint fires without help suggestion", func(t *testing.T) {
		got := genericCliRecoveryHint(raw, "some_sql_tool", "")
		assert.NotEmpty(t, got, "hint should still fire on CLI signal even without help ref")
		assert.NotContains(t, got, "--help")
		assert.NotContains(t, got, "unsure of the syntax", "no help ref means no help suggestion at all")
	})
	t.Run("empty tool name AND empty help — hint fires but no tool-specific suffix", func(t *testing.T) {
		got := genericCliRecoveryHint(raw, "", "")
		assert.NotEmpty(t, got)
		assert.NotContains(t, got, "unsure of the syntax")
	})
}

// TestCliRecoveryEnvelope_Precedence pins the compose semantics: specific
// hint always wins when present; generic fallback fires only when specific
// is empty AND the raw error carries CLI signal. When both are empty, the
// raw error passes through byte-for-byte (no envelope, matching wrapCliError).
func TestCliRecoveryEnvelope_Precedence(t *testing.T) {
	rawWithSignal := "unknown flag: --foo"
	rawOpaque := "connection timeout"

	t.Run("specific hint wins over generic", func(t *testing.T) {
		wrapped := cliRecoveryEnvelope(rawWithSignal, "specific hint for --foo", "mytool", "mytool --help")
		var env map[string]string
		assert.NoError(t, json.Unmarshal([]byte(wrapped), &env))
		assert.Equal(t, "specific hint for --foo", env["error_hint"])
		assert.NotContains(t, env["error_hint"], "re-read the raw output",
			"specific hint present → generic must not fire")
	})

	t.Run("empty specific + CLI-signal raw → generic fires", func(t *testing.T) {
		wrapped := cliRecoveryEnvelope(rawWithSignal, "", "mytool", "mytool --help")
		var env map[string]string
		assert.NoError(t, json.Unmarshal([]byte(wrapped), &env))
		assert.Contains(t, env["error_hint"], "re-read the raw output")
		assert.Contains(t, env["error_hint"], "mytool --help")
		assert.Equal(t, rawWithSignal, env["original_error"])
	})

	t.Run("empty specific + opaque raw → no envelope, raw passthrough", func(t *testing.T) {
		wrapped := cliRecoveryEnvelope(rawOpaque, "", "mytool", "mytool --help")
		assert.Equal(t, rawOpaque, wrapped, "opaque + no specific → raw passthrough")
	})

	t.Run("whitespace-only specific triggers generic (matches wrapCliError contract)", func(t *testing.T) {
		wrapped := cliRecoveryEnvelope(rawWithSignal, "   \n", "mytool", "mytool --help")
		var env map[string]string
		assert.NoError(t, json.Unmarshal([]byte(wrapped), &env))
		assert.Contains(t, env["error_hint"], "re-read the raw output")
	})
}
