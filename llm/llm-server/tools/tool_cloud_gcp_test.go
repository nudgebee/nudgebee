package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGcloudErrorHint_Patterns pins the two hint classes surfaced by the
// 2026-07-02 DB sweep: 38 of 83 gcloud errors were the shell-syntax class
// (`--format=table(...)` parens interpreted as subshell by `sh -c`), and
// a recurring cluster of per-project API-not-enabled 403s. Everything else
// passes through unchanged so raw gcloud output remains authoritative.
func TestGcloudErrorHint_Patterns(t *testing.T) {
	cases := []struct {
		name     string
		rawError string
		want     string // substring expected in hint; "" = no hint
	}{
		{
			name:     "sh syntax error on --format=table(...) parens — dominant shape",
			rawError: `sh: syntax error: unexpected "("`,
			want:     "single quotes",
		},
		{
			name:     "bash-style variant of the same shell-syntax class",
			rawError: "bash: syntax error near unexpected token `('",
			want:     "single quotes",
		},
		{
			name:     "kubernetes engine API not enabled on a project",
			rawError: `ERROR: (gcloud.container.clusters.list) ResponseError: code=403, message=Kubernetes Engine API has not been used in project rackspace-488209 before or it is disabled.`,
			want:     "Do NOT retry the same command on this project",
		},
		{
			name:     "compute API not enabled — PERMISSION_DENIED variant",
			rawError: `ERROR: (gcloud.compute.instances.list) PERMISSION_DENIED: Compute Engine API has not been used in project X before or it is disabled.`,
			want:     "Do NOT retry the same command on this project",
		},
		{
			name:     "quota exceeded — separate class",
			rawError: `ERROR: (gcloud.compute.instances.list) Quota exceeded for quota metric 'ComputeEngineAPI'`,
			want:     "quota exceeded",
		},
		{
			name:     "billing not enabled — deliberately NOT hinted (user-actionable but distinct wording)",
			rawError: `ERROR: (gcloud.container.clusters.list) ResponseError: code=403, message=This API method requires billing to be enabled.`,
			want:     "",
		},
		{
			name:     "real permission error on a specific resource — no hint, raw is already actionable",
			rawError: `ERROR: (gcloud.compute.instances.list) PERMISSION_DENIED: The caller does not have permission on projects/foo.`,
			want:     "",
		},
		{
			name:     "empty raw — no hint",
			rawError: "",
			want:     "",
		},
		{
			name:     "case-insensitive on the shell-syntax class",
			rawError: `SH: SYNTAX ERROR: UNEXPECTED "("`,
			want:     "single quotes",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := gcloudErrorHint(c.rawError)
			if c.want == "" {
				assert.Empty(t, got, "expected no hint, got: %s", got)
				return
			}
			assert.Contains(t, got, c.want)
		})
	}
}

// TestGcloudErrorHint_EnvelopeShape verifies the wrapCliError envelope that
// consumes gcloudErrorHint. Same shape as PR #33404's github tests: hint
// present → JSON envelope round-trips; no hint → raw passthrough.
func TestGcloudErrorHint_EnvelopeShape(t *testing.T) {
	t.Run("shell-syntax hit: envelope contains both hint and original_error", func(t *testing.T) {
		raw := `sh: syntax error: unexpected "("`
		wrapped := wrapCliError(raw, gcloudErrorHint(raw))
		var env map[string]string
		err := json.Unmarshal([]byte(wrapped), &env)
		assert.NoError(t, err)
		assert.Contains(t, env["error_hint"], "single quotes")
		assert.Equal(t, raw, env["original_error"])
	})

	t.Run("no pattern match: pass-through unchanged", func(t *testing.T) {
		raw := `ERROR: (gcloud.compute.disks.list) INVALID_ARGUMENT: unknown zone.`
		wrapped := wrapCliError(raw, gcloudErrorHint(raw))
		assert.Equal(t, raw, wrapped)
	})
}
