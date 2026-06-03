package event

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestParseSeveritySet(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]bool
	}{
		{"empty", "", map[string]bool{}},
		{"single", "HIGH", map[string]bool{"HIGH": true}},
		{"csv with spaces", " high , Medium ", map[string]bool{"HIGH": true, "MEDIUM": true}},
		{"trailing comma + blanks", "HIGH,,", map[string]bool{"HIGH": true}},
		{"lowercased normalized", "low", map[string]bool{"LOW": true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, parseSeveritySet(c.in))
		})
	}
}

func TestBuildRCAComment(t *testing.T) {
	t.Run("includes header rca body and deep link", func(t *testing.T) {
		out := buildRCAComment("root cause: OOMKilled", "evt-123")
		assert.Contains(t, out, "NudgeBee RCA Analysis")
		assert.Contains(t, out, "root cause: OOMKilled")
		assert.Contains(t, out, "/investigate?id=evt-123")
	})

	t.Run("truncates long bodies rune-safely without panicking", func(t *testing.T) {
		// Multi-byte runes ensure naive byte slicing would split a rune.
		long := strings.Repeat("é", rcaWritebackMaxCommentChars+500)
		out := buildRCAComment(long, "evt-1")
		assert.Contains(t, out, "…(truncated)")
		assert.True(t, strings.Contains(out, "/investigate?id=evt-1"))
		// Output must remain valid UTF-8 (no broken rune from truncation).
		assert.True(t, utf8.ValidString(out))
	})

	t.Run("short body is not truncated", func(t *testing.T) {
		out := buildRCAComment("brief", "e")
		assert.NotContains(t, out, "…(truncated)")
	})
}

func TestRCAContentHashStableAndDistinct(t *testing.T) {
	a := rcaContentHash("same text")
	b := rcaContentHash("same text")
	c := rcaContentHash("different text")
	assert.Equal(t, a, b, "same input must hash identically (idempotency depends on this)")
	assert.NotEqual(t, a, c, "different RCA versions must hash differently (revision -> new note)")
}

// TestRCAWritebackSourceSeam pins the source seam: each registered event source
// maps to the right add-comment token + integration-config namespace.
func TestRCAWritebackSourceSeam(t *testing.T) {
	zd, ok := rcaWritebackSources["zenduty_webhook"]
	assert.True(t, ok, "zenduty_webhook must be a registered writeback source")
	assert.Equal(t, "zenduty", zd.commentSource)
	assert.Equal(t, "zenduty", zd.configNamespace)

	pd, ok := rcaWritebackSources["pagerduty_webhook"]
	assert.True(t, ok, "pagerduty_webhook must be a registered writeback source")
	assert.Equal(t, "pagerduty", pd.commentSource)
	assert.Equal(t, "pagerduty", pd.configNamespace)
}
