package egressfilter

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"

	"github.com/google/uuid"
)

// withReveal flips the testing-only reveal flag for one test and restores it.
// withReveal forces the per-tenant reveal decision for the duration of a test
// and returns a ctx carrying a tenant, since RevealValues resolves from both.
func withReveal(t *testing.T, on bool) context.Context {
	t.Helper()
	prev := revealLookup
	revealLookup = func(string) (bool, error) { return on, nil }
	t.Cleanup(func() { revealLookup = prev })
	return WithTenantID(context.Background(), uuid.New())
}

func TestValueShape(t *testing.T) {
	cases := map[string]string{
		"zaphod.b@galaxy.test": "aaaaaa.a@aaaaaa.aaaa",
		"+1 415-555-0142":      "+9 999-999-9999",
		"AKIAIOSFODNN7EXAMPLE": "AAAAAAAAAAAA9AAAAAAA",
		"ghp_qfq4zK":           "aaa_aaa9aA",
		"":                     "",
	}
	for in, want := range cases {
		assert.Equal(t, want, ValueShape(in), "shape of %q", in)
	}
	// Two different values of the same shape must be indistinguishable —
	// that property is what makes the shape safe to persist.
	assert.Equal(t, ValueShape("alice@acme.co"), ValueShape("bruno@zzzz.io"))
}

func TestValueShape_LongValueCapped(t *testing.T) {
	long := make([]byte, 500)
	for i := range long {
		long[i] = 'a'
	}
	got := ValueShape(string(long))
	assert.LessOrEqual(t, len([]rune(got)), maxShapeLen+1, "shape must stay bounded")
	assert.Contains(t, got, "…")
}

func TestAnnotateHits_RevealOff_NoRawValue(t *testing.T) {
	ctx := withReveal(t, false)
	payload := "token=SUPERSECRETVALUE123 end"
	hits := []Hit{{RuleID: "generic-kv-secret", Start: 6, End: 25}}

	got := annotateHits(ctx, hits, payload)

	assert.Equal(t, 19, got[0].Length)
	assert.Equal(t, "AAAAAAAAAAAAAAAA999", got[0].Shape)
	assert.Empty(t, got[0].Value, "raw value must not be recorded when reveal is off")
	// Input must not be mutated — the security decision still reads it.
	assert.Empty(t, hits[0].Shape)
}

func TestAnnotateHits_RevealOn_CarriesRawValue(t *testing.T) {
	ctx := withReveal(t, true)
	payload := "token=SUPERSECRETVALUE123 end"
	got := annotateHits(ctx, []Hit{{RuleID: "generic-kv-secret", Start: 6, End: 25}}, payload)
	assert.Equal(t, "SUPERSECRETVALUE123", got[0].Value)
	assert.Equal(t, 19, got[0].Length, "length and shape stay populated when revealing")
}

func TestAnnotateHits_OutOfRangeOffsetsAreSkipped(t *testing.T) {
	ctx := withReveal(t, true)
	payload := "short"
	hits := []Hit{
		{RuleID: "a", Start: -1, End: 3},
		{RuleID: "b", Start: 0, End: 999},
		{RuleID: "c", Start: 4, End: 2},
	}
	got := annotateHits(ctx, hits, payload)
	for _, h := range got {
		assert.Zero(t, h.Length, "rule %s: bad offsets must not panic or record", h.RuleID)
		assert.Empty(t, h.Value)
	}
}

func TestAnnotateHits_KnowledgeBaseAttribution(t *testing.T) {
	ctx := withReveal(t, false)
	payload := "chatter\n<retrieved_knowledge>\nRunbook body with alice@acme.co inside\nSource: https://wiki/pages/123\n</retrieved_knowledge>\ntail"
	start := strings.Index(payload, "alice@acme.co")
	got := annotateHits(ctx, []Hit{{RuleID: "x", Start: start, End: start + len("alice@acme.co")}}, payload)
	assert.Equal(t, OriginKnowledgeBase, got[0].Origin)
	assert.Equal(t, "https://wiki/pages/123", got[0].DocURL)
}

func TestAnnotateHits_OutsideKnowledgeBaseHasNoOrigin(t *testing.T) {
	ctx := withReveal(t, false)
	payload := "<retrieved_knowledge>\ndoc\nSource: https://wiki/1\n</retrieved_knowledge>\nlater alice@acme.co"
	start := len(payload) - len("alice@acme.co")
	got := annotateHits(ctx, []Hit{{RuleID: "x", Start: start, End: len(payload)}}, payload)
	assert.Empty(t, got[0].Origin, "value after the KB block must not be attributed to it")
	assert.Empty(t, got[0].DocURL)
}

// A multi-MB match must not reserve its own length for a 64-rune result.
// (Gemini review on PR #35859.)
func TestValueShape_HugeInputDoesNotOverAllocate(t *testing.T) {
	huge := strings.Repeat("A", 2<<20) // 2 MiB
	got := ValueShape(huge)
	assert.LessOrEqual(t, len(got), maxShapeLen*4+len("…"))
	assert.True(t, strings.HasSuffix(got, "…"))
}

// The load-bearing invariant: no letter or digit of the ORIGINAL may survive
// into the shape, in any script. The first implementation only masked ASCII,
// so "Владимир Петров" masked to itself — raw PII persisted with reveal OFF.
// (Gemini review on PR #35859.)
func TestValueShape_NoLetterOrDigitSurvives(t *testing.T) {
	for _, v := range []string{
		"josé.garcía@acme.es",
		"Владимир Петров",
		"山田太郎@example.jp",
		"müller@königsberg.de",
		"Ελένη Παπαδοπούλου",
		"محمد عبد الله",
		"राहुल शर्मा",
		"alice@acme.co",
		"+1 415-555-0142",
	} {
		got := ValueShape(v)
		for _, r := range got {
			assert.False(t, unicode.IsLetter(r) && r != 'a' && r != 'A',
				"letter %q survived into shape %q of %q", r, got, v)
			assert.False(t, unicode.IsDigit(r) && r != '9',
				"digit %q survived into shape %q of %q", r, got, v)
		}
	}
}

// Same script, same length -> identical shape. This is what makes the shape
// safe to persist, and it must hold for non-Latin scripts too.
func TestValueShape_IndistinguishableAcrossScripts(t *testing.T) {
	assert.Equal(t, ValueShape("Владимир"), ValueShape("Екатерина"[:len("Владимир")]))
	assert.Equal(t, ValueShape("山田太郎"), ValueShape("鈴木一郎"))
	assert.Equal(t, ValueShape("josé"), ValueShape("renê"))
}

// The pass-through must be a CLOSED set. An open default branch reproduced
// punctuation-dense credentials verbatim — ValueShape("!@#$%^&*()_+") returned
// itself and ValueShape("P@$$!") returned "A@$$!".
func TestValueShape_OnlyStructuralRunesSurvive(t *testing.T) {
	for _, v := range []string{"!@#$%^&*()_+", "P@$$!", "Tr0ub4dor&3!", "a\"b'c<d>e", "x\ty\nz"} {
		got := ValueShape(v)
		for _, r := range got {
			ok := r == 'a' || r == 'A' || r == '9' || r == '#' || strings.ContainsRune(structuralRunes, r)
			assert.True(t, ok, "rune %q survived into shape %q of %q", r, got, v)
		}
	}
	// Structure that makes a shape readable is still preserved.
	assert.Equal(t, "aaaaaa.a@aaaaaa.aaaa", ValueShape("zaphod.b@galaxy.test"))
	assert.Equal(t, "+9 999-999-9999", ValueShape("+1 415-555-0142"))
}

func TestValueShape_LengthIsRunesNotBytes(t *testing.T) {
	ctx := withReveal(t, false)
	// 山田太郎@example.jp is 15 runes / 27 bytes.
	payload := "山田太郎@example.jp"
	got := annotateHits(ctx, []Hit{{RuleID: "x", Start: 0, End: len(payload)}}, payload)
	assert.Equal(t, 15, got[0].Length, "length must be runes so the UI's \"chars\" label is honest")
}

// RevealValues is a security gate: every path that is not an explicit
// "enabled" for THIS tenant must return false, so a lookup problem can never
// be the reason raw secrets start being persisted.
func TestRevealValues_FailsClosed(t *testing.T) {
	errBoom := errors.New("db unreachable")
	cases := []struct {
		name   string
		ctx    context.Context
		lookup func(string) (bool, error)
		want   bool
	}{
		{"enabled for tenant", WithTenantID(context.Background(), uuid.New()),
			func(string) (bool, error) { return true, nil }, true},
		{"not enrolled", WithTenantID(context.Background(), uuid.New()),
			func(string) (bool, error) { return false, nil }, false},
		{"lookup error", WithTenantID(context.Background(), uuid.New()),
			func(string) (bool, error) { return true, errBoom }, false},
		{"no tenant on ctx", context.Background(),
			func(string) (bool, error) { return true, nil }, false},
		{"nil ctx", nil,
			func(string) (bool, error) { return true, nil }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prev := revealLookup
			revealLookup = tc.lookup
			t.Cleanup(func() { revealLookup = prev })
			assert.Equal(t, tc.want, RevealValues(tc.ctx))
		})
	}
}

// The flag is per-tenant: an enrolled tenant must not reveal values for a
// different tenant sharing the same process.
func TestRevealValues_ScopedPerTenant(t *testing.T) {
	enrolled, other := uuid.New(), uuid.New()
	prev := revealLookup
	revealLookup = func(tenantID string) (bool, error) { return tenantID == enrolled.String(), nil }
	t.Cleanup(func() { revealLookup = prev })

	assert.True(t, RevealValues(WithTenantID(context.Background(), enrolled)))
	assert.False(t, RevealValues(WithTenantID(context.Background(), other)),
		"a second tenant on the same instance must not inherit the reveal")
}
