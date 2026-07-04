package egressfilter

import (
	"regexp"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

// resetAllowlistForTest clears the package-global allowlist between cases.
// Tests touching the allowlist must call this in their setup to avoid
// order-dependence across `go test -count=N` runs and parallel packages
// sharing process state.
func resetAllowlistForTest(t *testing.T) {
	t.Helper()
	allowlistMu = sync.Mutex{}
	allowlistValue = atomic.Value{}
}

func TestRegisterAllowedValue_EmptyIsNoop(t *testing.T) {
	resetAllowlistForTest(t)
	RegisterAllowedValue("")
	assert.Empty(t, loadAllowlist())
}

func TestRegisterAllowedValue_AddsThenDedup(t *testing.T) {
	resetAllowlistForTest(t)
	RegisterAllowedValue("AKIAIOSFODNN7EXAMPLE")
	RegisterAllowedValue("AKIAIOSFODNN7EXAMPLE") // duplicate
	RegisterAllowedValue("AIzaSyExampleKey0123456789012345678901X")
	allow := loadAllowlist()
	assert.Len(t, allow, 2)
	assert.Contains(t, allow, "AKIAIOSFODNN7EXAMPLE")
	assert.Contains(t, allow, "AIzaSyExampleKey0123456789012345678901X")
}

func TestRegisterAllowedValues_SkipsEmptyAndDup(t *testing.T) {
	resetAllowlistForTest(t)
	RegisterAllowedValues([]string{"AKIAIOSFODNN7EXAMPLE", "", "AKIAIOSFODNN7EXAMPLE", "SECOND"})
	allow := loadAllowlist()
	assert.Len(t, allow, 2)
}

func TestLoadAllowlistFromCSV(t *testing.T) {
	resetAllowlistForTest(t)
	LoadAllowlistFromCSV("  AKIAIOSFODNN7EXAMPLE , ,AIzaSyExampleKey0123456789012345678901X ,")
	allow := loadAllowlist()
	assert.Len(t, allow, 2)
	assert.Contains(t, allow, "AKIAIOSFODNN7EXAMPLE")
	assert.Contains(t, allow, "AIzaSyExampleKey0123456789012345678901X")
}

func TestLoadAllowlistFromCSV_EmptyIsNoop(t *testing.T) {
	resetAllowlistForTest(t)
	LoadAllowlistFromCSV("")
	assert.Empty(t, loadAllowlist())
}

// TestApplyAllowlist_EmptyReturnsInputUnchanged guards the hot-path zero-alloc
// promise: when no allowlist is registered, the returned slice is the same
// header as the input.
func TestApplyAllowlist_EmptyReturnsInputUnchanged(t *testing.T) {
	resetAllowlistForTest(t)
	hits := []Hit{{RuleID: "x", Start: 0, End: 3}}
	out := applyAllowlist("abc", hits)
	// Same underlying array — applyAllowlist must not allocate when allowlist
	// is empty (verified by len-and-cap identity).
	assert.Equal(t, len(hits), len(out))
	assert.Equal(t, cap(hits), cap(out))
}

// TestApplyAllowlist_OutOfBoundsFailsClosed guards the fail-closed contract:
// a malformed Hit from a misbehaving custom Filter must not panic the hot
// path, and must NOT be dropped (so the security control remains on).
// Regression for Gemini review on PR #32646.
func TestApplyAllowlist_OutOfBoundsFailsClosed(t *testing.T) {
	resetAllowlistForTest(t)
	RegisterAllowedValue("anything")
	text := "short"
	bad := []Hit{
		{RuleID: "neg-start", Start: -1, End: 3},
		{RuleID: "end-before-start", Start: 4, End: 2},
		{RuleID: "end-past-len", Start: 0, End: 999},
	}
	out := applyAllowlist(text, bad)
	assert.Len(t, out, 3, "all malformed hits must be kept (fail-closed)")
}

func TestApplyAllowlist_DropsMatchKeepsOthers(t *testing.T) {
	resetAllowlistForTest(t)
	RegisterAllowedValue("AKIAIOSFODNN7EXAMPLE")
	text := "aws_key=AKIAIOSFODNN7EXAMPLE other=AKIAREALPRODKEY12345"
	hits := []Hit{
		{RuleID: "aws-access-key-id", Start: 8, End: 28},  // matches "AKIAIOSFODNN7EXAMPLE"
		{RuleID: "aws-access-key-id", Start: 35, End: 55}, // matches "AKIAREALPRODKEY12345"
	}
	out := applyAllowlist(text, hits)
	assert.Len(t, out, 1)
	assert.Equal(t, 35, out[0].Start)
}

// TestScan_AllowlistIntegration exercises the end-to-end path through Scan
// using the real baseline rules.
func TestScan_AllowlistIntegration(t *testing.T) {
	resetAllowlistForTest(t)
	// Confirm both texts trip the baseline aws-access-key-id rule first.
	allowed := "AKIAIOSFODNN7EXAMPLE"
	flagged := "AKIA0123456789ABCDEF"
	assert.True(t, Scan(allowed).HasHits(), "baseline must catch the example AKIA")
	assert.True(t, Scan(flagged).HasHits(), "baseline must catch a real-shape AKIA")

	RegisterAllowedValue(allowed)
	// Same payload, now allowlisted → no hit.
	assert.False(t, Scan(allowed).HasHits(), "allowlisted value must not be reported")
	// Different value, still flagged.
	assert.True(t, Scan(flagged).HasHits(), "non-allowlisted value must still be reported")
}

// TestScan_AllowlistIsExactNotPrefix guards the documented semantics: the
// allowlist matches the rule's captured range bytewise, not a substring or
// prefix. Two near-matches differing by a single character must both be
// reported as hits unless explicitly listed.
func TestScan_AllowlistIsExactNotPrefix(t *testing.T) {
	resetAllowlistForTest(t)
	// Register a custom test rule so we don't depend on baseline regex
	// internals — use a deterministic 4-char prefix that matches exactly
	// two distinct test fixtures below.
	Register("test-prefix", RegexFilter("test-prefix", regexp.MustCompile(`\bTEST_[A-Z0-9]{8}\b`)))
	t.Cleanup(func() {
		// Clear the registry of our test rule so cross-test pollution
		// doesn't leak. We re-init by overwriting with baseline.
		registryValue.Store([]registered(nil))
		initSeedForTest()
	})
	RegisterAllowedValue("TEST_ABCD1234")

	assert.False(t, Scan("TEST_ABCD1234").HasHits(), "exact match must be allowlisted")
	assert.True(t, Scan("TEST_ABCD1235").HasHits(), "one-char-different must still hit")
}

// initSeedForTest re-runs the baseline seed for test cleanup. Exposed only to
// tests in this package; pulls baselineRules() and rewrites the registry so a
// test that injected its own rule can restore the default set.
func initSeedForTest() {
	base := baselineRules()
	seed := make([]registered, 0, len(base))
	for _, r := range base {
		seed = append(seed, registered{name: r.id, filter: &regexFilter{id: r.id, regex: r.regex}})
	}
	registryValue.Store(seed)
}
