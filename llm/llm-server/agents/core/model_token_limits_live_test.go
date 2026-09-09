package core

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestModelTokenLimits_Live exercises the REAL catalog fetch (fetchModelTokenLimits's
// SQL) against a live database — the unit tests stub it, so this is the only
// coverage of the query itself. Gated like the other live tests:
//
//	TEST_MODEL_LIMITS_LIVE=1 LLM_SERVER_DB_URL=... go test -run TestModelTokenLimits_Live ./agents/core/
func TestModelTokenLimits_Live(t *testing.T) {
	if os.Getenv("TEST_MODEL_LIMITS_LIVE") == "" {
		t.Skip("set TEST_MODEL_LIMITS_LIVE=1 (and LLM_SERVER_DB_URL) to run against a live database")
	}

	catalog, err := fetchModelTokenLimits("")
	require.NoError(t, err, "built-in catalog fetch must succeed")
	require.NotEmpty(t, catalog, "expected seeded built-in rows (V878)")

	// Spot-check the V878 seed through the full resolution path.
	assert.Equal(t, 16384, ResolveMaxOutputTokens("", "openai", "gpt-4o"))
	assert.Equal(t, 65536, ResolveMaxOutputTokens("", "anthropic", "claude-opus-4.6"))
	assert.Equal(t, 64000, ResolveMaxOutputTokens("", "anthropic", "claude-sonnet-4.5"))
	assert.Equal(t, 32000, ResolveMaxOutputTokens("", "anthropic", "claude-opus-4"))
	// The #36449 customer id: Bedrock cross-region, hyphenated version — must
	// resolve against the bare dotted catalog row via canonicalization.
	assert.Equal(t, 65536, ResolveMaxOutputTokens("", "bedrock", "us.anthropic.claude-sonnet-4-6"))
	// Seeded by V880 after live traffic on it was found unpriced and floored.
	assert.Equal(t, 65536, ResolveMaxOutputTokens("", "googleai", "gemini-3.7-flash"))
	assert.Equal(t, 4096, ResolveMaxOutputTokens("", "anthropic", "claude-opus-3"))
	// Unseeded (embedding) rows stay on the caller's floor.
	assert.Equal(t, 0, ResolveMaxOutputTokens("", "googleai", "text-embedding-004"))
}
