package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPostgresErrorHint_Patterns pins the postgres-side hint discriminator —
// the same shape as kubectlErrorHint (issue #32240) but tuned to the postgres
// 30-day error distribution sampled 2026-06-22 (148/193 errors matched the
// 400-wrapped-in-500 shape). Two patterns hit, anything else falls through to "".
func TestPostgresErrorHint_Patterns(t *testing.T) {
	cases := []struct {
		name     string
		rawError string
		want     string // substring expected in hint; "" = no hint
	}{
		{
			name:     "relay 500 wraps a proxy db_query 400 Bad Request — dominant shape",
			rawError: `Error: Server returned 500: {"error":"proxy agent db_query failed: status: 400 Bad Request","result":null}`,
			want:     "relay rejected this postgres query",
		},
		{
			name:     "relay 500 wraps a different proxy db_query 400 phrasing",
			rawError: `Error: Server returned 500: {"error":"proxy db_query error: status: 400 Bad Request","result":""}`,
			want:     "relay rejected this postgres query",
		},
		{
			name:     "shim 500 wraps the legacy findings-not-found parser bail",
			rawError: `Error: Server returned 500: {"error":"findings field not found or is nil from data","result":null}`,
			want:     "response shape the parser didn't recognize",
		},
		{
			name: "real SQL execution error (column does not exist) — NO hint, the raw " +
				"message is already actionable for the LLM (e.g. 'column \"total_time\" does not exist')",
			rawError: `Error: Server returned 500: {"error":"proxy db_query error: query execution failed: ERROR: column \"total_time\" does not exist (SQLSTATE 42703)","result":""}`,
			want:     "",
		},
		{
			name:     "permission denied — NO hint, raw SQLSTATE 42501 is already specific",
			rawError: `Error: Server returned 500: {"error":"proxy db_query error: query execution failed: ERROR: permission denied for table schema_migrations (SQLSTATE 42501)","result":""}`,
			want:     "",
		},
		{
			name:     "empty raw — no hint",
			rawError: "",
			want:     "",
		},
		{
			name:     "case-insensitive match on the 400-wrap shim",
			rawError: `error: server returned 500: {"error":"proxy agent db_query failed: status: 400 bad request"}`,
			want:     "relay rejected this postgres query",
		},
		// Decoupling guard: the hint must fire whether the error reaches us
		// shim-wrapped or directly from a parser. Same shape as the
		// kubectl-side guard added in PR #32243's Gemini review pass.
		{
			name:     "direct 400-Bad-Request error (no shim wrapper) — hint still fires",
			rawError: "postgres query failed: status: 400 Bad Request",
			want:     "relay rejected this postgres query",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := postgresErrorHint(c.rawError)
			if c.want == "" {
				assert.Empty(t, got, "expected no hint, got: %s", got)
				return
			}
			assert.Contains(t, got, c.want)
		})
	}
}

// TestWrapPostgresError_EnvelopeShape pins the JSON envelope shape on
// patterns that hit + verifies pass-through on patterns that don't.
func TestWrapPostgresError_EnvelopeShape(t *testing.T) {
	t.Run("400 wrap: envelope contains both hint and original_error", func(t *testing.T) {
		raw := `Error: Server returned 500: {"error":"proxy agent db_query failed: status: 400 Bad Request","result":null}`
		wrapped := wrapPostgresError(raw)
		assert.Contains(t, wrapped, `"error_hint"`)
		assert.Contains(t, wrapped, `"original_error"`)
		assert.Contains(t, wrapped, "relay rejected this postgres query")
		// The raw error must round-trip verbatim under original_error.
		var env map[string]string
		err := json.Unmarshal([]byte(wrapped), &env)
		assert.NoError(t, err)
		assert.Equal(t, raw, env["original_error"])
	})

	t.Run("no pattern match: pass-through unchanged", func(t *testing.T) {
		raw := `Error: Server returned 500: {"error":"proxy db_query error: query execution failed: ERROR: column \"total_time\" does not exist","result":""}`
		wrapped := wrapPostgresError(raw)
		assert.Equal(t, raw, wrapped)
	})

	t.Run("empty raw: pass-through empty", func(t *testing.T) {
		wrapped := wrapPostgresError("")
		assert.Equal(t, "", wrapped)
	})
}
