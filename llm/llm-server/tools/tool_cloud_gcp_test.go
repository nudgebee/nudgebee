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
		{
			// Dominant hallucination class from 2026-07 sweep: model invents
			// subcommand names (`gcloud sql insights`, `gcloud monitoring
			// timeseries`). gcloud's own output lists valid alternatives
			// under "Maybe you meant:" — the hint tells the model to read
			// it rather than guess a second time.
			name: "invalid choice with maybe-you-meant list — steer to gcloud's own suggestions",
			rawError: `ERROR: (gcloud.sql) Invalid choice: 'insights'.
Maybe you meant:
  gcloud sql instances`,
			want: "Maybe you meant",
		},
		{
			// Same class without the maybe-you-meant list — fallback hint
			// tells the model to extract the parent path and run --help.
			name:     "invalid choice without maybe-you-meant — steer to --help on parent",
			rawError: `ERROR: (gcloud.monitoring) Invalid choice: 'timeseries'.`,
			want:     "gcloud <parent> --help",
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

// TestRejectNonGcloudShapes covers the route-hint guard that catches
// tool-mismatched inputs before the auto-prefix silently rewrites them
// into invalid gcloud commands. gsutil is explicitly kept legitimate
// (Cloud Storage ACLs) per the agent prompt.
func TestRejectNonGcloudShapes(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string // substring expected in hint; "" = accepted (no guard fire)
	}{
		// --- passes through (empty hint = accepted) ---
		{"gcloud command accepted", "gcloud compute instances list --project=X", ""},
		{"gcloud with pipe accepted (first token is gcloud)", "gcloud storage ls gs://foo | jq '.[]'", ""},
		{"gsutil accepted — legitimate for Cloud Storage ACLs", "gsutil acl get gs://my-bucket", ""},
		{"unknown short-token accepted — auto-prefix will make it `gcloud X`", "compute instances list", ""},
		{"empty input accepted (existing paths handle it)", "", ""},

		// --- rejected with routing hints ---
		{
			name:    "curl → shell_execute hint",
			command: `curl -s -H "Authorization: Bearer $(gcloud auth print-access-token)" https://monitoring.googleapis.com/v3/...`,
			want:    "shell_execute",
		},
		{
			name:    "wget → same routing hint as curl",
			command: "wget https://storage.googleapis.com/foo",
			want:    "shell_execute",
		},
		{
			name:    "kubectl → wrong tool",
			command: "kubectl get pods -n default",
			want:    "kubectl` tool for Kubernetes",
		},
		{
			name:    "bq → shell_execute for BigQuery",
			command: `bq query --nouse_legacy_sql "SELECT 1"`,
			want:    "BigQuery",
		},
		{
			// Synthetic values only — real production names get flagged by the
			// internal-leaks gitleaks scanner. Shape is what matters.
			name:    "natural language sentence → routing hint",
			command: "Check Cloud SQL Query Insights and database schema for instance example-db in project demo-project.",
			want:    "natural-language string",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rejectNonGcloudShapes(c.command)
			if c.want == "" {
				assert.Empty(t, got, "expected accept (no hint), got: %s", got)
				return
			}
			assert.Contains(t, got, c.want)
		})
	}
}

// TestIsExecutableLikeToken pins the shape check used to distinguish shell
// commands from natural-language strings. Kept strict on purpose — an NL
// misroute is much more expensive to recover from than a rare false-reject
// on an unusual command name (which the model can retry with a rephrase).
func TestIsExecutableLikeToken(t *testing.T) {
	accept := []string{"gcloud", "kubectl", "aws", "bq", "some-tool", "tool_v2", "tool.sh", "a"}
	reject := []string{"", "Check", "1foo", " gcloud", "hello!", "tool/path", "with spaces"}
	for _, s := range accept {
		assert.True(t, isExecutableLikeToken(s), "expected accept: %q", s)
	}
	for _, s := range reject {
		assert.False(t, isExecutableLikeToken(s), "expected reject: %q", s)
	}
}
