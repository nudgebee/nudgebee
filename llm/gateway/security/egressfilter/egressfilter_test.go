package egressfilter

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMode(t *testing.T) {
	assert.Equal(t, ModeOff, ParseMode(""))
	assert.Equal(t, ModeDetect, ParseMode("detect"))
	assert.Equal(t, ModeEnforce, ParseMode("ENFORCE"))
	assert.Equal(t, ModeRedact, ParseMode(" redact "))
	assert.Equal(t, ModeDetect, ParseMode("bogus")) // fail-safe to detect
}

func TestScan_DetectsBaselineSecrets(t *testing.T) {
	cases := map[string]string{
		"aws-access-key-id":  "creds: AKIAIOSFODNN7EXAMPLE here",
		"anthropic-api-key":  "key sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789 end",
		"private-key-header": "-----BEGIN RSA PRIVATE KEY-----\nMIIE...",
	}
	for wantRule, text := range cases {
		res := Scan(text)
		require.True(t, res.HasHits(), "should detect in %q", text)
		assert.Contains(t, res.RuleIDs(), wantRule)
	}
}

func TestScan_CleanText(t *testing.T) {
	assert.False(t, Scan("The quick brown fox jumps over the lazy dog.").HasHits())
	assert.False(t, Scan("").HasHits())
}

func TestRedact_ReplacesSpanAndHidesSecret(t *testing.T) {
	secret := "AKIAIOSFODNN7EXAMPLE"
	text := "prefix " + secret + " suffix"
	res := Scan(text)
	require.True(t, res.HasHits())
	out := Redact(text, res.Hits)
	assert.NotContains(t, out, secret, "raw secret must be gone")
	assert.Contains(t, out, "[REDACTED:aws-access-key-id]")
	assert.True(t, strings.HasPrefix(out, "prefix ") && strings.HasSuffix(out, " suffix"))
}

func TestRedact_MergesOverlappingAndAdjacent(t *testing.T) {
	// Two rules matching overlapping spans of the same secret must redact once and
	// leak no byte (the bug: replacing right-to-left over overlaps corrupts offsets).
	text := "aXXXXXbYYYYYc" // a=0, X=1..5, b=6, Y=7..11, c=12
	out := Redact(text, []Hit{{RuleID: "r1", Start: 1, End: 8}, {RuleID: "r2", Start: 5, End: 12}})
	assert.Equal(t, "a[REDACTED:r1]c", out)
	assert.NotContains(t, out, "X")
	assert.NotContains(t, out, "Y")

	// Adjacent spans (a.End == b.Start) merge into one.
	assert.Equal(t, "a[REDACTED:r1]c", Redact("aXXXXYYYYc", []Hit{{RuleID: "r1", Start: 1, End: 5}, {RuleID: "r2", Start: 5, End: 9}}))

	// Disjoint spans stay separate.
	out = Redact("aXbYc", []Hit{{RuleID: "r1", Start: 1, End: 2}, {RuleID: "r2", Start: 3, End: 4}})
	assert.Equal(t, "a[REDACTED:r1]b[REDACTED:r2]c", out)
}

func TestEffectiveMode_CapsActiveModesWhenLocked(t *testing.T) {
	prev := EnforcementEnabled()
	t.Cleanup(func() { SetEnforcement(prev) })

	// OSS (enforcement locked): enforce/redact degrade to detect; off/detect unchanged.
	SetEnforcement(false)
	assert.Equal(t, ModeOff, EffectiveMode(ModeOff))
	assert.Equal(t, ModeDetect, EffectiveMode(ModeDetect))
	assert.Equal(t, ModeDetect, EffectiveMode(ModeEnforce))
	assert.Equal(t, ModeDetect, EffectiveMode(ModeRedact))

	// EE (unlocked): every mode passes through.
	SetEnforcement(true)
	assert.Equal(t, ModeEnforce, EffectiveMode(ModeEnforce))
	assert.Equal(t, ModeRedact, EffectiveMode(ModeRedact))
}

func TestAllowlist_DropsExactValue(t *testing.T) {
	// A fake OpenAI-style key that trips the baseline rule.
	key := "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789"
	require.True(t, Scan("token "+key).HasHits())
	RegisterAllowedValues([]string{key})
	assert.False(t, Scan("token "+key).HasHits(), "allowlisted value should be dropped")
	// A different secret still fires (allowlist is exact-match, not global suppression).
	assert.True(t, Scan("creds: AKIAIOSFODNN7EXAMPLE").HasHits())
}
