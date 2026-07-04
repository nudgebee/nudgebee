package egressfilter

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// fakeModel records the last GenerateContent / Call arguments and returns a
// canned response. Lets us assert that the wrapper actually calls (or skips)
// the inner model. Counters use atomics and the last-args capture is mutexed
// so the same instance is safe to share across the concurrency tests.
type fakeModel struct {
	calls         atomic.Int64
	mu            sync.Mutex
	lastMessages  []llms.MessageContent
	lastPrompt    string
	respText      string
	returnOnError error
}

func (f *fakeModel) GenerateContent(_ context.Context, messages []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.lastMessages = messages
	f.mu.Unlock()
	if f.returnOnError != nil {
		return nil, f.returnOnError
	}
	return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: f.respText}}}, nil
}

func (f *fakeModel) Call(_ context.Context, prompt string, _ ...llms.CallOption) (string, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.lastPrompt = prompt
	f.mu.Unlock()
	return f.respText, f.returnOnError
}

// callCount is a small helper so tests can assert the counter without leaking
// the atomic type into every assertion. Returns int (not int64) so plain
// integer literals in assertions match the deep-equal type check.
func (f *fakeModel) callCount() int { return int(f.calls.Load()) }

func TestScan_Clean(t *testing.T) {
	r := Scan("Investigate pod restarts in namespace prod.")
	assert.False(t, r.HasHits())
	assert.Empty(t, r.RuleIDs())
}

func TestScan_DetectsKnownSecrets(t *testing.T) {
	cases := map[string]string{
		"aws-access-key-id":  "key=AKIAIOSFODNN7EXAMPLE",
		"github-pat":         "auth ghp_Abcdefghijklmnopqrstuvwxyz0123456789",
		"private-key-header": "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBA...",
		"openai-api-key":     "OPENAI_API_KEY=sk-proj-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789",
		"anthropic-api-key":  "ANTHROPIC=sk-ant-api01-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789",
	}
	for name, sample := range cases {
		t.Run(name, func(t *testing.T) {
			r := Scan(sample)
			assert.True(t, r.HasHits(), "expected hit for %q", name)
			assert.Contains(t, r.RuleIDs(), name)
		})
	}
}

// TestRegister_AppendsRegexFilterToActiveSet locks the public extension API
// for regex rules: Register(name, RegexFilter(id, regex)) — after the call
// Scan should match the registered pattern with the supplied rule id.
func TestRegister_AppendsRegexFilterToActiveSet(t *testing.T) {
	const id = "test-only-rule-do-not-ship"
	Register(id, RegexFilter(id, regexp.MustCompile(`\bTEST_TOKEN_[A-Z0-9]{8}\b`)))
	// We do not expose a deregister; the registry is monotonic for the
	// lifetime of the process. Subsequent tests must tolerate the addition.
	r := Scan("payload TEST_TOKEN_ABCD1234 trailing")
	assert.True(t, r.HasHits())
	assert.Contains(t, r.RuleIDs(), id)
}

// TestRegister_AcceptsFilterFunc verifies the FilterFunc adapter — anything
// with the right shape (func(string) []Hit) works as a Filter. This is the
// extension path entropy-style detectors use.
//
// The FilterFunc fires only on a sentinel substring so it does not pollute
// later tests in this binary (the registry is monotonic — once registered
// always there).
func TestRegister_AcceptsFilterFunc(t *testing.T) {
	const id = "test-only-filterfunc"
	const sentinel = "VISIBILITY_TEST_ONLY_SENTINEL_DO_NOT_REUSE"
	Register(id, FilterFunc(func(text string) []Hit {
		if idx := strings.Index(text, sentinel); idx >= 0 {
			return []Hit{{RuleID: id, Start: idx, End: idx + len(sentinel)}}
		}
		return nil
	}))
	r := Scan("payload " + sentinel + " trailing")
	assert.True(t, r.HasHits())
	assert.Contains(t, r.RuleIDs(), id)
}

func TestScan_NegativeCorpus(t *testing.T) {
	clean := []string{
		"",
		"hello world",
		"kubectl get pods -n production",
		"the quick brown fox jumps over the lazy dog",
		"git commit -m 'fix(api): handle null pointer in router'",
		"namespace=prod, deployment=checkout, replicas=3",
		// short alphanumeric — under entropy minimum
		"variable-name-camelCase",
	}
	for _, s := range clean {
		t.Run(s, func(t *testing.T) {
			r := Scan(s)
			assert.False(t, r.HasHits(), "unexpected hit for %q rules=%v", s, r.RuleIDs())
		})
	}
}

// (No TestScan_NoEntropyByDefault — under the unified Register API there is
// no separate "entropy slot" to assert absent. The baseline is "the filters
// you see in baselineRules()"; absence of entropy in the baseline is encoded
// directly by not listing it in regex_filters.go. The corresponding presence test
// for the EE bundle lives in ee/egressfilter/visibility_test.go.)

func TestParseMode(t *testing.T) {
	assert.Equal(t, ModeEnforce, ParseMode("enforce"))
	assert.Equal(t, ModeEnforce, ParseMode("ENFORCE"))
	assert.Equal(t, ModeEnforce, ParseMode("  enforce  "))
	assert.Equal(t, ModeAudit, ParseMode("audit"))
	assert.Equal(t, ModeAudit, ParseMode(""))
	assert.Equal(t, ModeAudit, ParseMode("garbage"))
}

func TestWrapModel_DisabledIsNoOp(t *testing.T) {
	inner := &fakeModel{respText: "ok"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", false, ModeEnforce)
	// Same pointer — wrapping disabled.
	assert.Same(t, inner, wrapped)
}

func TestWrapModel_EnforcePassesCleanThrough(t *testing.T) {
	inner := &fakeModel{respText: "ok"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeEnforce)

	resp, err := wrapped.GenerateContent(context.Background(),
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeHuman, "What's the status of pod foo in prod?"),
		})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 1, inner.callCount())
}

func TestWrapModel_EnforceBlocksSecret(t *testing.T) {
	inner := &fakeModel{respText: "should not be reached"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeEnforce)

	_, err := wrapped.GenerateContent(context.Background(),
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeHuman, "use OPENAI_API_KEY=sk-proj-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789"),
		})
	require.Error(t, err)
	assert.Equal(t, 0, inner.callCount(), "inner model must not be called when blocked")

	// errors.Is matches the sentinel and AsError unwraps the typed error.
	assert.True(t, errors.Is(err, ErrSecretsBlocked))
	se, ok := AsError(err)
	require.True(t, ok)
	assert.NotEmpty(t, se.AuditID)
	assert.NotEmpty(t, se.RuleIDs)

	// User-facing message must be safe: no rule names, no secret values, no
	// internal jargon — just a short remediation hint and the audit ID.
	msg := se.Error()
	assert.Contains(t, msg, "blocked")
	assert.Contains(t, msg, se.AuditID)
	assert.NotContains(t, msg, "sk-proj-")
	for _, rid := range se.RuleIDs {
		assert.NotContains(t, msg, rid)
	}
}

func TestWrapModel_AuditDoesNotBlock(t *testing.T) {
	inner := &fakeModel{respText: "passed through"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeAudit)

	_, err := wrapped.GenerateContent(context.Background(),
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeHuman, "leaked: ghp_Abcdefghijklmnopqrstuvwxyz0123456789"),
		})
	require.NoError(t, err)
	assert.Equal(t, 1, inner.callCount(), "audit mode must let the call through")
}

func TestWrapModel_CallPathAlsoScrubbed(t *testing.T) {
	inner := &fakeModel{respText: "ok"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeEnforce)

	// Call() routes through GenerateFromSinglePrompt → GenerateContent → scan.
	_, err := llms.GenerateFromSinglePrompt(context.Background(), wrapped, "key=AKIAIOSFODNN7EXAMPLE")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSecretsBlocked))
	assert.Equal(t, 0, inner.callCount())
}

func TestSerializeMessages_FlattensTextAndToolParts(t *testing.T) {
	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "system rules"}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "user query"}}},
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{llms.ToolCallResponse{Content: "tool output"}}},
	}
	got := serializeMessages(msgs)
	for _, want := range []string{"system rules", "user query", "tool output"} {
		assert.True(t, strings.Contains(got, want), "missing %q in flattened payload", want)
	}
}

// TestSerializeMessages_HandlesPointerToolParts ensures secrets in *llms.ToolCall
// and *llms.ToolCallResponse (the pointer forms langchaingo sometimes uses)
// are still scanned. Without these cases, secrets would silently bypass the
// egressfilter. Regression test for the Gemini high-priority finding.
func TestSerializeMessages_HandlesPointerToolParts(t *testing.T) {
	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{
			&llms.ToolCall{
				ID: "call_1", Type: "function",
				FunctionCall: &llms.FunctionCall{
					Name:      "create_secret",
					Arguments: `{"value":"AKIAIOSFODNN7EXAMPLE"}`,
				},
			},
		}},
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
			&llms.ToolCallResponse{ToolCallID: "call_1", Name: "create_secret", Content: "stored ghp_Abcdefghijklmnopqrstuvwxyz0123456789"},
		}},
	}
	got := serializeMessages(msgs)
	assert.Contains(t, got, "AKIAIOSFODNN7EXAMPLE")
	assert.Contains(t, got, "ghp_Abcdefghijklmnopqrstuvwxyz0123456789")

	// And the wrapper actually catches them end-to-end.
	inner := &fakeModel{respText: "ok"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeEnforce)
	_, err := wrapped.GenerateContent(context.Background(), msgs)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSecretsBlocked))
	assert.Equal(t, 0, inner.callCount())
}

// TestSerializeMessages_ScansImageURLContent ensures the URL field of an
// ImageURLContent part is included in the scan payload — pre-signed S3 URLs
// and Azure Blob SAS tokens routinely embed credential material in query
// params and would otherwise slip past the egressfilter. Regression test for the
// Gemini high-priority finding on the second review round.
func TestSerializeMessages_ScansImageURLContent(t *testing.T) {
	// A fabricated S3-signed-style URL carrying an AWS access key id in a
	// query param. The aws-access-key-id rule should fire on it.
	sasURL := "https://example-bucket.s3.amazonaws.com/img.png?X-Amz-Credential=AKIAIOSFODNN7EXAMPLE/20260101/us-east-1/s3/aws4_request"
	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.ImageURLContent{URL: sasURL, Detail: "auto"},
		}},
	}
	got := serializeMessages(msgs)
	assert.Contains(t, got, "AKIAIOSFODNN7EXAMPLE", "ImageURLContent.URL must be in flattened payload")

	// End-to-end: wrapper must block when the URL carries a secret.
	inner := &fakeModel{respText: "ok"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeEnforce)
	_, err := wrapped.GenerateContent(context.Background(), msgs)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSecretsBlocked))
	assert.Equal(t, 0, inner.callCount())

	// Pointer form must also be handled.
	msgsPtr := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			&llms.ImageURLContent{URL: sasURL, Detail: "auto"},
		}},
	}
	inner2 := &fakeModel{respText: "ok"}
	wrapped2 := WrapModel(inner2, "openai", "gpt-4o", true, ModeEnforce)
	_, err = wrapped2.GenerateContent(context.Background(), msgsPtr)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSecretsBlocked))
	assert.Equal(t, 0, inner2.callCount())

	// A nil *llms.ImageURLContent must not panic.
	msgsNil := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{(*llms.ImageURLContent)(nil)}},
	}
	assert.NotPanics(t, func() { _ = serializeMessages(msgsNil) })
}

// TestNewAuditID_Format asserts the audit ID has the documented prefix and
// length (egress- + 12 hex chars). Locks the format so we don't accidentally
// shrink it back into birthday-collision territory.
func TestNewAuditID_Format(t *testing.T) {
	for range 5 {
		id := newAuditID()
		assert.True(t, strings.HasPrefix(id, "egress-"), "expected egress- prefix, got %q", id)
		assert.Len(t, id, len("egress-")+12, "expected 12 hex chars after prefix, got %q", id)
		// The 12 chars after the prefix must all be lowercase hex.
		for _, r := range id[len("egress-"):] {
			assert.True(t, (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f'), "non-hex char %q in id %q", r, id)
		}
	}
}

func TestSerializeMessages_NilPointerPartsDoNotPanic(t *testing.T) {
	// A nil *llms.ToolCall or *llms.ToolCallResponse must not panic — Parts is
	// constructed by callers we don't fully control.
	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{
			(*llms.ToolCall)(nil),
			(*llms.ToolCallResponse)(nil),
			(*llms.TextContent)(nil),
		}},
	}
	assert.NotPanics(t, func() {
		_ = serializeMessages(msgs)
	})
}

func TestRuleIDs_AreSorted(t *testing.T) {
	// Construct a result with hits in non-sorted insertion order; RuleIDs must
	// return them sorted (the docstring promises it for deterministic logs/metrics).
	r := Result{Hits: []Hit{
		{RuleID: "zeta"},
		{RuleID: "alpha"},
		{RuleID: "mu"},
		{RuleID: "alpha"}, // dup must collapse
	}}
	assert.Equal(t, []string{"alpha", "mu", "zeta"}, r.RuleIDs())
}

func TestError_LogStringIncludesRuleIDs(t *testing.T) {
	e := &Error{AuditID: "egress-abc12345", RuleIDs: []string{"github-pat", "aws-access-key-id"}}
	log := e.LogString()
	// Sorted for determinism.
	assert.Contains(t, log, "aws-access-key-id,github-pat")
	assert.Contains(t, log, "egress-abc12345")
}
