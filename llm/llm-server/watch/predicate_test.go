package watch

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// ---------------------------------------------------------------------------
// CompilePredicate dispatch + create-time validation
// ---------------------------------------------------------------------------

func TestCompilePredicate_RejectsEmptyExpression(t *testing.T) {
	for _, kind := range []PredicateKind{PredicateKindRegex, PredicateKindSubstring, PredicateKindLLMJudge} {
		t.Run(string(kind), func(t *testing.T) {
			_, err := CompilePredicate(kind, "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "expression must not be empty")
			_, err = CompilePredicate(kind, "   \t\n")
			require.Error(t, err, "whitespace-only expression must also fail")
		})
	}
}

func TestCompilePredicate_RejectsUnknownKind(t *testing.T) {
	_, err := CompilePredicate(PredicateKind("badkind"), "anything")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported kind")
}

func TestCompilePredicate_RegexCompileErrorsAtCreateTime(t *testing.T) {
	// Catching this at create time is the whole point of CompilePredicate —
	// users find out their regex is broken in watch_resource, not 30s later
	// from the dispatcher.
	_, err := CompilePredicate(PredicateKindRegex, "(unclosed")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid regex")
}

func TestCompilePredicate_DispatchReturnsRightImpl(t *testing.T) {
	cases := []struct {
		kind PredicateKind
		expr string
		want string // unexported type name fragment we expect in fmt.Sprintf("%T")
	}{
		{PredicateKindRegex, "ok", "regexPredicate"},
		{PredicateKindSubstring, "ok", "substringPredicate"},
		{PredicateKindLLMJudge, "is it done?", "llmJudgePredicate"},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			p, err := CompilePredicate(tc.kind, tc.expr)
			require.NoError(t, err)
			require.NotNil(t, p)
			gotType := typeNameOf(p)
			assert.Contains(t, gotType, tc.want)
		})
	}
}

// ---------------------------------------------------------------------------
// regex predicate
// ---------------------------------------------------------------------------

func TestRegexPredicate_Match(t *testing.T) {
	p, err := CompilePredicate(PredicateKindRegex, "successfully rolled out")
	require.NoError(t, err)

	r, err := p.Eval(nil, Watch{}, Observation{Data: "deployment x successfully rolled out"})
	require.NoError(t, err)
	assert.True(t, r.Met)
	assert.Contains(t, r.Summary, "matched")
	assert.Contains(t, r.Summary, "successfully rolled out")
}

func TestRegexPredicate_NoMatch(t *testing.T) {
	p, _ := CompilePredicate(PredicateKindRegex, "completed")
	r, err := p.Eval(nil, Watch{}, Observation{Data: "still in_progress"})
	require.NoError(t, err)
	assert.False(t, r.Met)
	assert.Contains(t, r.Summary, "not matched")
}

func TestRegexPredicate_EmptyObservation(t *testing.T) {
	// Common in real life: kubectl rollout status --timeout=0 sometimes
	// returns empty stdout. Predicate must NOT match (avoid ghost successes)
	// and must NOT crash.
	p, _ := CompilePredicate(PredicateKindRegex, "completed")
	r, err := p.Eval(nil, Watch{}, Observation{Data: ""})
	require.NoError(t, err)
	assert.False(t, r.Met)
}

func TestRegexPredicate_MultilineAndAnchors(t *testing.T) {
	// Confirms we don't accidentally enable multiline mode under the user.
	p, _ := CompilePredicate(PredicateKindRegex, "^done$")
	r, _ := p.Eval(nil, Watch{}, Observation{Data: "first line\ndone\nthird line"})
	// Default Go regex: ^/$ are start/end of input — should NOT match.
	assert.False(t, r.Met)

	// Explicit (?m) flag still works for callers that want it.
	p2, err := CompilePredicate(PredicateKindRegex, "(?m)^done$")
	require.NoError(t, err)
	r2, _ := p2.Eval(nil, Watch{}, Observation{Data: "first line\ndone\nthird line"})
	assert.True(t, r2.Met)
}

// ---------------------------------------------------------------------------
// substring predicate
// ---------------------------------------------------------------------------

func TestSubstringPredicate_Match(t *testing.T) {
	p, _ := CompilePredicate(PredicateKindSubstring, "rolled out")
	r, err := p.Eval(nil, Watch{}, Observation{Data: "deployment x successfully rolled out"})
	require.NoError(t, err)
	assert.True(t, r.Met)
	assert.Contains(t, r.Summary, "found substring")
}

func TestSubstringPredicate_NoMatch(t *testing.T) {
	p, _ := CompilePredicate(PredicateKindSubstring, "rolled out")
	r, err := p.Eval(nil, Watch{}, Observation{Data: "still in_progress"})
	require.NoError(t, err)
	assert.False(t, r.Met)
	assert.Contains(t, r.Summary, "not found")
}

func TestSubstringPredicate_CaseSensitive(t *testing.T) {
	// strings.Contains is case-sensitive; document & lock that here so we
	// don't accidentally change the contract later.
	p, _ := CompilePredicate(PredicateKindSubstring, "ROLLED OUT")
	r, _ := p.Eval(nil, Watch{}, Observation{Data: "deployment x successfully rolled out"})
	assert.False(t, r.Met)
}

// ---------------------------------------------------------------------------
// llm_judge predicate
// ---------------------------------------------------------------------------

// fakeLLMJudgeClient lets each test inject the model's reply (or an error).
type fakeLLMJudgeClient struct {
	body string
	err  error
}

func (f *fakeLLMJudgeClient) GenerateContent(_ context.Context, _ []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: f.body}}}, nil
}

func withJudgeClient(t *testing.T, client LLMJudgeClient) {
	t.Helper()
	prev := getLLMJudgeClientFactory()
	SetLLMJudgeClientFactory(func(_ *security.RequestContext, _ Watch) (LLMJudgeClient, error) {
		return client, nil
	})
	t.Cleanup(func() { SetLLMJudgeClientFactory(prev) })
}

func TestLLMJudge_NoFactoryRegistered(t *testing.T) {
	prev := getLLMJudgeClientFactory()
	SetLLMJudgeClientFactory(nil)
	t.Cleanup(func() { SetLLMJudgeClientFactory(prev) })

	p, _ := CompilePredicate(PredicateKindLLMJudge, "is it done?")
	_, err := p.Eval(testCtx(t), Watch{}, Observation{Data: "anything"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no LLM client factory")
}

func TestLLMJudge_FactoryReturnsError(t *testing.T) {
	prev := getLLMJudgeClientFactory()
	SetLLMJudgeClientFactory(func(*security.RequestContext, Watch) (LLMJudgeClient, error) {
		return nil, errors.New("boom")
	})
	t.Cleanup(func() { SetLLMJudgeClientFactory(prev) })

	p, _ := CompilePredicate(PredicateKindLLMJudge, "is it done?")
	_, err := p.Eval(testCtx(t), Watch{}, Observation{Data: "anything"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to build client")
}

func TestLLMJudge_StrictJSONMet(t *testing.T) {
	withJudgeClient(t, &fakeLLMJudgeClient{
		body: `{"met": true, "summary": "all 13 runs done"}`,
	})
	p, _ := CompilePredicate(PredicateKindLLMJudge, "are all runs done?")
	r, err := p.Eval(testCtx(t), Watch{}, Observation{Data: "raw observation"})
	require.NoError(t, err)
	assert.True(t, r.Met)
	assert.Equal(t, "all 13 runs done", r.Summary)
}

func TestLLMJudge_StrictJSONNotMet(t *testing.T) {
	withJudgeClient(t, &fakeLLMJudgeClient{
		body: `{"met": false, "summary": "still 4 of 13 running"}`,
	})
	p, _ := CompilePredicate(PredicateKindLLMJudge, "are all runs done?")
	r, err := p.Eval(testCtx(t), Watch{}, Observation{Data: "raw observation"})
	require.NoError(t, err)
	assert.False(t, r.Met)
}

func TestLLMJudge_TolerantToProseAroundJSON(t *testing.T) {
	// The implementation deliberately strips leading prose / trailing prose
	// around the first {...}, because models drift. Lock that in.
	withJudgeClient(t, &fakeLLMJudgeClient{
		body: "Sure, here is my answer:\n{\"met\": true, \"summary\": \"done\"}\nLet me know!",
	})
	p, _ := CompilePredicate(PredicateKindLLMJudge, "done yet?")
	r, err := p.Eval(testCtx(t), Watch{}, Observation{Data: "raw"})
	require.NoError(t, err)
	assert.True(t, r.Met)
	assert.Equal(t, "done", r.Summary)
}

func TestLLMJudge_NonJSONIsAnError(t *testing.T) {
	withJudgeClient(t, &fakeLLMJudgeClient{
		body: "I think it's done!",
	})
	p, _ := CompilePredicate(PredicateKindLLMJudge, "done?")
	_, err := p.Eval(testCtx(t), Watch{}, Observation{Data: "raw"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not valid JSON")
}

func TestLLMJudge_EmptyChoicesIsAnError(t *testing.T) {
	// fakeLLMJudgeClient returning a response with no choices.
	prev := getLLMJudgeClientFactory()
	SetLLMJudgeClientFactory(func(*security.RequestContext, Watch) (LLMJudgeClient, error) {
		return llmJudgeClientFn(func(context.Context, []llms.MessageContent, ...llms.CallOption) (*llms.ContentResponse, error) {
			return &llms.ContentResponse{Choices: nil}, nil
		}), nil
	})
	t.Cleanup(func() { SetLLMJudgeClientFactory(prev) })

	p, _ := CompilePredicate(PredicateKindLLMJudge, "done?")
	_, err := p.Eval(testCtx(t), Watch{}, Observation{Data: "raw"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty model response")
}

func TestLLMJudge_ModelInvocationErrorIsTransient(t *testing.T) {
	withJudgeClient(t, &fakeLLMJudgeClient{err: errors.New("provider 5xx")})
	p, _ := CompilePredicate(PredicateKindLLMJudge, "done?")
	_, err := p.Eval(testCtx(t), Watch{}, Observation{Data: "raw"})
	require.Error(t, err)
	// Caller treats predicate-eval errors as transient and retries.
	// The error message should preserve enough context for that.
	assert.Contains(t, err.Error(), "model invocation failed")
}

func TestLLMJudge_PromptIncludesQuestionAndObservation(t *testing.T) {
	// The judge predicate is only useful if both the user's question and the
	// poll observation actually reach the model. Verify both via a capturing
	// fake.
	captured := &capturingClient{}
	prev := getLLMJudgeClientFactory()
	SetLLMJudgeClientFactory(func(*security.RequestContext, Watch) (LLMJudgeClient, error) {
		return captured, nil
	})
	t.Cleanup(func() { SetLLMJudgeClientFactory(prev) })

	p, _ := CompilePredicate(PredicateKindLLMJudge, "are all 13 workflow runs done?")
	_, _ = p.Eval(testCtx(t), Watch{}, Observation{Data: "{\"in_progress\":4,\"done\":9}"})

	require.NotEmpty(t, captured.lastPrompt)
	assert.Contains(t, captured.lastPrompt, "are all 13 workflow runs done?")
	assert.Contains(t, captured.lastPrompt, "in_progress")
}

func TestLLMJudge_LargeObservationIsTruncated(t *testing.T) {
	// 16k cap from the implementation; we don't want a 1MB blob to balloon
	// the prompt and burn tokens / break model context limits.
	captured := &capturingClient{}
	prev := getLLMJudgeClientFactory()
	SetLLMJudgeClientFactory(func(*security.RequestContext, Watch) (LLMJudgeClient, error) {
		return captured, nil
	})
	t.Cleanup(func() { SetLLMJudgeClientFactory(prev) })

	huge := strings.Repeat("a", 50_000)
	p, _ := CompilePredicate(PredicateKindLLMJudge, "anything?")
	_, _ = p.Eval(testCtx(t), Watch{}, Observation{Data: huge})

	// Prompt itself is bounded by 16000 + boilerplate, so well under 50k.
	assert.Less(t, len(captured.lastPrompt), 20_000)
	// But the observation portion (which is what gets truncated) ends with
	// the "…" boundary marker.
	assert.Contains(t, captured.lastPrompt, "…")
}

// TestLLMJudge_JSONWithBraceInStringValue documents the limit of the
// trailing-prose strip: when the model's reply contains a `}` inside a
// JSON string value, the LastIndex(raw, "}") logic still finds the
// OUTERMOST closing brace (because the string-internal `}` appears before
// the structural one in `}\n}` orderings). Pin that on so we know
// downstream what the contract is — if this ever flips, the test fails
// loudly and a more sophisticated JSON-aware tokenizer is needed.
func TestLLMJudge_JSONWithBraceInStringValue(t *testing.T) {
	// summary field embeds a literal `}` character; the closing `}` of the
	// object comes after it, so LastIndex finds the right one.
	withJudgeClient(t, &fakeLLMJudgeClient{
		body: `{"met": true, "summary": "matched on pattern ending with }"}`,
	})
	p, _ := CompilePredicate(PredicateKindLLMJudge, "done?")
	r, err := p.Eval(testCtx(t), Watch{}, Observation{Data: "raw"})
	require.NoError(t, err)
	assert.True(t, r.Met)
	assert.Contains(t, r.Summary, "ending with }")
}

// TestLLMJudge_JSONWrappedInCodeFence guards the case the model emits
// markdown-wrapped JSON despite "no code fences" in the prompt. The
// implementation's leading-`{` strip skips the ```json prefix; the
// trailing-`}` strip leaves the JSON intact. If a future refactor breaks
// either side, watches with llm_judge predicates start failing.
func TestLLMJudge_JSONWrappedInCodeFence(t *testing.T) {
	withJudgeClient(t, &fakeLLMJudgeClient{
		body: "```json\n{\"met\": false, \"summary\": \"not yet\"}\n```",
	})
	p, _ := CompilePredicate(PredicateKindLLMJudge, "done?")
	r, err := p.Eval(testCtx(t), Watch{}, Observation{Data: "raw"})
	require.NoError(t, err)
	assert.False(t, r.Met)
	assert.Equal(t, "not yet", r.Summary)
}

// TestLLMJudge_EmptyMetField_DefaultsToFalse documents what happens when
// the model emits a partial JSON object. `met` defaults to its zero value
// (false) and Eval returns Met=false, which means the watch keeps polling
// — the safe default. Pin that on; an alternative (treat missing field
// as an error) would terminate watches whenever the model misbehaves
// transiently.
func TestLLMJudge_EmptyMetField_DefaultsToFalse(t *testing.T) {
	withJudgeClient(t, &fakeLLMJudgeClient{
		body: `{"summary": "model forgot the met field"}`,
	})
	p, _ := CompilePredicate(PredicateKindLLMJudge, "done?")
	r, err := p.Eval(testCtx(t), Watch{}, Observation{Data: "raw"})
	require.NoError(t, err)
	assert.False(t, r.Met, "missing met field must default to false (safe — keep polling)")
}

// ---------------------------------------------------------------------------
// truncate (UTF-8 boundary safety)
// ---------------------------------------------------------------------------

func TestTruncate_NoCutWhenUnderLimit(t *testing.T) {
	got := truncate("hello", 100)
	assert.Equal(t, "hello", got)
}

func TestTruncate_AddsEllipsisOverLimit(t *testing.T) {
	got := truncate("hello world", 5)
	// Walks back to a valid rune boundary before appending.
	assert.True(t, strings.HasSuffix(got, "…"))
	assert.LessOrEqual(t, len(got)-len("…"), 5)
}

func TestTruncate_NeverProducesInvalidUTF8(t *testing.T) {
	// 4-byte rune (U+1F600 grinning face). Naïve byte-slicing at offsets 1/2/3
	// would split the rune and produce invalid UTF-8 — Postgres `text` would
	// reject the INSERT. truncate() must walk back to the last rune boundary.
	const grin = "\xF0\x9F\x98\x80" // 1 rune, 4 bytes
	for limit := 1; limit <= 4; limit++ {
		got := truncate(grin+grin+grin, limit)
		// "…" is appended for any non-empty output; trim it before validating.
		stripped := strings.TrimSuffix(got, "…")
		// What remains MUST be valid UTF-8 — Postgres text columns require it.
		assert.Truef(t, isValidUTF8(stripped),
			"limit=%d produced invalid UTF-8 prefix %q", limit, stripped)
	}
}

func TestTruncate_NonPositiveLimitIsNoop(t *testing.T) {
	assert.Equal(t, "abc", truncate("abc", 0))
	assert.Equal(t, "abc", truncate("abc", -1))
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

type llmJudgeClientFn func(ctx context.Context, messages []llms.MessageContent, opts ...llms.CallOption) (*llms.ContentResponse, error)

func (f llmJudgeClientFn) GenerateContent(ctx context.Context, messages []llms.MessageContent, opts ...llms.CallOption) (*llms.ContentResponse, error) {
	return f(ctx, messages, opts...)
}

type capturingClient struct {
	lastPrompt string
}

func (c *capturingClient) GenerateContent(_ context.Context, messages []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	if len(messages) > 0 && len(messages[0].Parts) > 0 {
		if tc, ok := messages[0].Parts[0].(llms.TextContent); ok {
			c.lastPrompt = tc.Text
		}
	}
	return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: `{"met": false, "summary": "n/a"}`}}}, nil
}

// testCtx returns a RequestContext suitable for predicate evaluation. The
// llm_judge path only touches GetContext() so a minimal context is enough.
func testCtx(t *testing.T) *security.RequestContext {
	t.Helper()
	return security.NewRequestContextForSuperAdmin()
}

// typeNameOf returns the dynamic type name of v with leading '*' stripped.
// Used to assert CompilePredicate's dispatch returns the right impl.
func typeNameOf(v any) string {
	if v == nil {
		return "<nil>"
	}
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.String()
}

// isValidUTF8 wraps utf8.ValidString so the truncation tests read clearly.
func isValidUTF8(s string) bool {
	return utf8.ValidString(s)
}
