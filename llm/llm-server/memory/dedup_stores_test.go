package memory_test

import (
	"context"
	"strings"
	"testing"

	"nudgebee/llm/ee/memory"
	memprefs "nudgebee/llm/ee/memory/stores/preferences"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Both dedup layers used to read only the collective store, so a preference,
// pattern or decision fact was compared against rows it could never match.
// The extractor therefore never saw what it had already recorded and coined a
// fresh key each time — `tone`, `assistant_tone`, `user_style_tone` all holding
// "direct". These lock in that each store is now consulted for its own facts.

func seedPref(t *testing.T, tenantID, userID, key string, value any) {
	t.Helper()
	_, err := memprefs.Upsert(&memprefs.Preference{
		TenantID:   tenantID,
		Scope:      memprefs.ScopeUser,
		UserID:     userID,
		Key:        key,
		Value:      value,
		Source:     memprefs.SourceInferred,
		Confidence: 0.8,
	})
	require.NoError(t, err)
}

func TestDedupContextSurfacesPreferenceKeys(t *testing.T) {
	tenantID, userID, _, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	seedPref(t, tenantID, userID, "tone", "direct")
	seedPref(t, tenantID, userID, "verbosity", "terse")

	lines := memory.DedupContext(context.Background(), tenantID, userID, "how should you answer me", 5)
	joined := strings.Join(lines, "\n")

	// The key list is what stops naming drift — without it the extractor has no
	// way to know "tone" is already taken and invents "assistant_tone".
	assert.Contains(t, joined, "tone", "existing preference keys must reach the extractor")
	assert.Contains(t, joined, "verbosity")
	assert.Contains(t, joined, "[preferences]", "the block must say which layer a line came from")
}

// Without a user there is nothing user-scoped to read; collective-only is the
// honest answer rather than an error or a panic.
func TestDedupContextWithoutUserIsCollectiveOnly(t *testing.T) {
	tenantID, _, _, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	lines := memory.DedupContext(context.Background(), tenantID, "", "anything", 5)
	for _, l := range lines {
		assert.NotContains(t, l, "[preferences]")
		assert.NotContains(t, l, "[patterns]")
		assert.NotContains(t, l, "[decisions]")
	}
}

// Layer 2: a preference re-learned under a different key must collapse onto the
// existing key rather than inserting a second row for the same value.
func TestCanonicalSubjectMatchesWithinPreferences(t *testing.T) {
	tenantID, userID, _, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	seedPref(t, tenantID, userID, "preferred_log_source", "loki is the log source to use")

	canon := memory.CanonicalSubject(context.Background(), tenantID, userID,
		memory.TargetPreferences, "log_source_preference", "loki is the log source to use", 0.8)

	assert.Equal(t, "preferred_log_source", canon,
		"a preference must be compared against preferences, not collective")
}

// The same call routed at collective must not see the preference row — a
// cross-store match would corrupt the other layer's keyspace.
func TestCanonicalSubjectDoesNotLeakAcrossStores(t *testing.T) {
	tenantID, userID, _, cleanup := requireEdgeIntegration(t)
	defer cleanup()

	seedPref(t, tenantID, userID, "preferred_log_source", "loki is the log source to use")

	canon := memory.CanonicalSubject(context.Background(), tenantID, userID,
		memory.TargetCollective, "log_source_preference", "loki is the log source to use", 0.8)

	assert.Empty(t, canon, "a preference row must never canonicalise a collective fact")
}
