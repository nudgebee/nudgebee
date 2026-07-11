package egressfilter

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// The tests in this file exist to make the egressfilter's behavior visible at a
// glance. They assert correctness AND log a structured summary so anyone
// running `go test -v ./security/egressfilter/...` can see exactly which rules
// fired, where in the payload, what the user-facing error looks like, and
// what the public extension API actually does on a real payload.
//
// They are intentionally separate from the regression tests so failures here
// signal "the runtime story changed," not "an internal helper drifted."

// describeHits returns a human-readable, deterministic summary of a Result —
// one line per hit with rule id, byte offsets, and a clipped snippet of the
// matched text. Snippet length is bounded so leaked values cannot dominate
// test output.
func describeHits(text string, r Result) string {
	if !r.HasHits() {
		return "(no hits)"
	}
	type entry struct {
		ruleID string
		start  int
		end    int
		snip   string
	}
	entries := make([]entry, 0, len(r.Hits))
	for _, h := range r.Hits {
		s, e := h.Start, h.End
		if s < 0 {
			s = 0
		}
		if e > len(text) {
			e = len(text)
		}
		snip := text[s:e]
		if len(snip) > 24 {
			snip = snip[:21] + "..."
		}
		// Replace newlines so each entry stays on one line.
		snip = strings.ReplaceAll(snip, "\n", `\n`)
		entries = append(entries, entry{h.RuleID, h.Start, h.End, snip})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].start != entries[j].start {
			return entries[i].start < entries[j].start
		}
		return entries[i].ruleID < entries[j].ruleID
	})
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "    [%s] bytes %d..%d match=%q", e.ruleID, e.start, e.end, e.snip)
	}
	return b.String()
}

// envDumpPayload is a realistic kubectl env-dump shape. It carries one secret
// the OSS baseline catches and several that an extension bundle (or a user-
// registered rule) would catch. Constants are AWS / GitHub canonical public
// examples — not real credentials.
const envDumpPayload = `Pod env vars:
  AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE
  AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
  GITHUB_TOKEN=ghp_Abcdefghijklmnopqrstuvwxyz0123456789
  SLACK_BOT_TOKEN=xoxb-1234567890-1234567890-AbCdEfGhIjKl
  JWT_BEARER=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTYifQ.AbCdEfGhIjKlMnOpQrSt
  STRIPE_KEY=sk_live_AbCdEfGhIjKlMnOpQrStUvWx`

// TestVisibility_BaselineCatalogue_OnRealisticPayload runs Scan on a multi-
// secret env-dump shape and asserts the exact set of rule ids the OSS
// baseline catches. Logs a structured summary so the runtime story is visible
// without firing up the binary.
//
// In the default build (no extension bundle): expect exactly 2 hits, both
// from rules that ship in the baseline corpus.
func TestVisibility_BaselineCatalogue_OnRealisticPayload(t *testing.T) {
	t.Logf("payload (%d bytes):\n%s", len(envDumpPayload), envDumpPayload)

	r := Scan(envDumpPayload)
	t.Logf("hits (%d):\n%s", len(r.Hits), describeHits(envDumpPayload, r))
	t.Logf("rule_ids that fired: %v", r.RuleIDs())

	// Assertions: only baseline rules. Anyone reading the test output knows
	// without ambiguity which rules belong to the baseline.
	assert.ElementsMatch(t,
		[]string{"aws-access-key-id", "github-pat"},
		r.RuleIDs(),
		"baseline must catch exactly aws-access-key-id and github-pat from the env dump",
	)

	// And a paranoia check: nothing fired that the baseline does not own.
	knownBaseline := map[string]struct{}{
		"aws-access-key-id":  {},
		"private-key-header": {},
		"github-pat":         {},
		"openai-api-key":     {},
		"anthropic-api-key":  {},
	}
	for _, id := range r.RuleIDs() {
		_, ok := knownBaseline[id]
		assert.True(t, ok, "unexpected rule_id %q outside the documented baseline", id)
	}
}

// TestVisibility_ExtensionAPI_AddsCoverage demonstrates the public
// RegisterRule extension API on a payload the baseline does NOT cover.
// Before registration: 0 hits. After: 1 hit with the registered rule id.
// This is what a self-hosted operator (or an extension bundle) does on
// init().
func TestVisibility_ExtensionAPI_AddsCoverage(t *testing.T) {
	const (
		customRuleID  = "visibility-demo-internal-token"
		customPattern = `\bINT_TOKEN_[A-Z0-9]{12}\b`
	)
	payload := "Service config: server=foo.internal INT_TOKEN_ABC123XYZ456 endpoint=https://api.foo.internal"

	t.Logf("payload: %q", payload)

	// Baseline (extension not yet registered): expect clean.
	before := Scan(payload)
	t.Logf("before RegisterRule: hits=%d rule_ids=%v", len(before.Hits), before.RuleIDs())
	assert.False(t, before.HasHits(), "baseline must not match the synthetic INT_TOKEN_ pattern")

	// Register an extension rule — this is the same API the EE bundle and
	// self-hosters use.
	Register(customRuleID, RegexFilter(customRuleID, regexp.MustCompile(customPattern)))

	// After registration: one hit, matching the new id, at the right offset.
	after := Scan(payload)
	t.Logf("after  RegisterRule: hits=%d\n%s", len(after.Hits), describeHits(payload, after))
	require.True(t, after.HasHits(), "registered rule must match the payload")
	assert.Contains(t, after.RuleIDs(), customRuleID)
	assert.Equal(t, 1, len(after.Hits), "exactly one hit expected")
	assert.Equal(t, strings.Index(payload, "INT_TOKEN_"), after.Hits[0].Start)
}

// TestVisibility_WrapperBlock_OperatorView shows what the operator and the
// end user see when the wrapper blocks an outbound call in enforce mode.
// Pretty-prints the full block shape — audit id, rule ids that fired, hit
// count, payload size, the typed error, and the user-facing error message —
// so the operational story is obvious without reading wrapper code.
//
// This is the test to run when you want to know "what does a real block
// look like from the outside".
func TestVisibility_WrapperBlock_OperatorView(t *testing.T) {
	// A realistic-shape outbound payload: system prompt + a tool response
	// that leaked an AWS access key.
	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{
			llms.TextContent{Text: "You are an SRE assistant. Investigate."},
		}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.TextContent{Text: "Why is foo-api restarting in prod?"},
		}},
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
			llms.ToolCallResponse{
				ToolCallID: "call_1",
				Name:       "kubectl_get_env",
				Content:    "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE\nAWS_REGION=us-east-1",
			},
		}},
	}

	inner := &fakeModel{respText: "should never reach the provider"}
	wrapped := WrapModel(inner, "bedrock", "claude-sonnet-4", true, ModeEnforce)

	_, err := wrapped.GenerateContent(context.Background(), msgs)
	require.Error(t, err)
	require.Equal(t, 0, inner.callCount(), "inner provider must not be called when blocked")
	require.True(t, errors.Is(err, ErrSecretsBlocked))

	se, ok := AsError(err)
	require.True(t, ok)

	payload := serializeMessages(msgs)
	scanResult := Scan(payload) // recomputed only so we can pretty-print offsets

	t.Logf(`egressfilter block — operator-view summary:

  audit_id          : %s
  mode              : %s
  rule_ids fired    : %v
  hit count         : %d
  payload bytes     : %d
  user-facing error : %q
  inner.call_count  : %d

  hits:
%s
`,
		se.AuditID,
		ModeEnforce,
		se.RuleIDs,
		len(scanResult.Hits),
		len(payload),
		err.Error(),
		inner.callCount(),
		describeHits(payload, scanResult),
	)

	// Hard contracts the operator-view depends on:
	assert.Regexp(t, `^egress-[0-9a-f]{12}$`, se.AuditID,
		"audit id format must stay egress-<12 hex chars>")
	assert.Contains(t, se.RuleIDs, "aws-access-key-id")
	// User-facing message must not echo the secret value or any rule ID.
	assert.NotContains(t, err.Error(), "AKIA")
	for _, rid := range se.RuleIDs {
		assert.NotContains(t, err.Error(), rid)
	}
	// User-facing message MUST carry the audit id so operators can correlate.
	assert.Contains(t, err.Error(), se.AuditID)
}

// TestVisibility_EnvDumpDelta_BaselineVsExtension demonstrates the
// OSS/extension split on a single shared payload — without depending on the
// extension bundle existing in the source tree. We simulate "an extension is
// registered" via RegisterRule and assert the delta is exactly the additional
// rule fires.
//
// Run with `-v` to see the side-by-side delta in test output.
func TestVisibility_EnvDumpDelta_BaselineVsExtension(t *testing.T) {
	t.Logf("payload (env dump, %d bytes):\n%s", len(envDumpPayload), envDumpPayload)

	baseline := Scan(envDumpPayload)
	t.Logf("\nbaseline-only catches:\n  rule_ids=%v\n%s",
		baseline.RuleIDs(), describeHits(envDumpPayload, baseline))

	// Simulate an extension registering one extra rule (slack-bot-token shape).
	// We choose a fixed-id rule so the test output is deterministic. This is
	// NOT the EE rule; it's a stand-in to show what registration looks like.
	const demoID = "visibility-demo-slack-shape"
	Register(demoID, RegexFilter(demoID, regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,48}\b`)))

	extended := Scan(envDumpPayload)
	t.Logf("\nwith one extension rule registered (%s):\n  rule_ids=%v\n%s",
		demoID, extended.RuleIDs(), describeHits(envDumpPayload, extended))

	// Baseline catches the two it always catches.
	assert.ElementsMatch(t,
		[]string{"aws-access-key-id", "github-pat"},
		baseline.RuleIDs())

	// Extension adds exactly one rule id (the one we registered) and does not
	// drop any of the baseline ones.
	assert.ElementsMatch(t,
		[]string{"aws-access-key-id", "github-pat", demoID},
		extended.RuleIDs(),
		"extension must add only the registered rule_id and preserve baseline matches",
	)
}
