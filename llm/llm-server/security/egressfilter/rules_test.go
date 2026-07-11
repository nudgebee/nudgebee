package egressfilter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRules_PositiveCorpusByID covers each baseline rule with a realistic-
// shaped sample and asserts the corresponding rule id fires.
func TestRules_PositiveCorpusByID(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		expectRule string
	}{
		{"AKIA prefix", "id=AKIAIOSFODNN7EXAMPLE here", "aws-access-key-id"},
		{"ASIA prefix (STS)", "id=ASIAIOSFODNN7EXAMPLE here", "aws-access-key-id"},
		{"GitHub PAT classic", "ghp_Abcdefghijklmnopqrstuvwxyz0123456789", "github-pat"},
		{"GitHub OAuth token", "gho_Abcdefghijklmnopqrstuvwxyz0123456789", "github-pat"},
		{"OpenAI sk-proj key", "key=sk-proj-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789", "openai-api-key"},
		{"OpenAI sk- key", "key=sk-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789", "openai-api-key"},
		{"Anthropic sk-ant", "key=sk-ant-api01-AbCdEfGhIjKlMnOpQrStUvWxYz012345_-6789", "anthropic-api-key"},
		{"RSA private key", "-----BEGIN RSA PRIVATE KEY-----\ndata", "private-key-header"},
		{"OpenSSH private key", "-----BEGIN OPENSSH PRIVATE KEY-----\ndata", "private-key-header"},
		{"Encrypted private key", "-----BEGIN ENCRYPTED PRIVATE KEY-----\ndata", "private-key-header"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Scan(tc.input)
			assert.True(t, r.HasHits(), "expected at least one hit for %q", tc.name)
			assert.Contains(t, r.RuleIDs(), tc.expectRule, "expected rule %q to fire on %q (saw %v)", tc.expectRule, tc.name, r.RuleIDs())
		})
	}
}

// TestRules_NegativeCorpus_DomainSpecific exercises strings that look secret-
// shaped but appear in our domain (k8s manifests, AWS error messages, CLI
// output). These must NOT trip the egressfilter, otherwise audit-mode rollout
// will be unusable.
func TestRules_NegativeCorpus_DomainSpecific(t *testing.T) {
	clean := []struct {
		name  string
		input string
	}{
		{"k8s pod name", "pod foo-api-7b9c-xyz12 entered CrashLoopBackOff"},
		{"deployment with hash", "deployment.apps/checkout-7d8b9f5c4d-h2k4j 3/3 Running"},
		{"sha256 image digest line (no key context)", "image: nginx@sha256:1234567890abcdef1234567890abcdef1234"},
		{"AWS ARN", "arn:aws:iam::123456789012:role/MyRole"},
		{"AWS region/account in error", "User: arn:aws:sts::123456789012:assumed-role/foo is not authorized"},
		{"ISO timestamp", "2026-01-12T10:14:00Z"},
		{"git short sha", "fix: handle null pointer (commit a1b2c3d)"},
		{"version string", "kubectl v1.30.4 (commit abcdef1234)"},
		{"prose with sk word", "Please ask the on-call engineer for help with this stack."},
		{"hex color", "background: #1a2b3c4d;"},
		{"JSON without secrets", `{"status":"ok","items":[1,2,3]}`},
		{"yaml namespace", "metadata:\n  namespace: production\n  name: checkout-deployment"},
	}
	for _, tc := range clean {
		t.Run(tc.name, func(t *testing.T) {
			r := Scan(tc.input)
			assert.False(t, r.HasHits(), "false positive on %q (rules=%v)", tc.name, r.RuleIDs())
		})
	}
}
