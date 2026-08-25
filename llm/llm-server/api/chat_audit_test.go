package api

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToolConfirmationDecision(t *testing.T) {
	cases := map[string]string{
		"yes":       "approved",
		"Yes":       "approved",
		" YES ":     "approved",
		"approve":   "approved",
		"proceed":   "approved",
		"no":        "rejected",
		"No":        "rejected",
		"reject":    "rejected",
		"cancelled": "rejected",
		"maybe":     "other",
		"":          "other",
		"do it now": "other",
	}
	for response, want := range cases {
		t.Run(response, func(t *testing.T) {
			assert.Equal(t, want, toolConfirmationDecision(response))
		})
	}
}

func TestTruncateQueryForAudit(t *testing.T) {
	t.Run("short query is returned unchanged", func(t *testing.T) {
		q := "why is my pod crashlooping?"
		assert.Equal(t, q, truncateQueryForAudit(q))
	})

	t.Run("empty query is returned unchanged", func(t *testing.T) {
		assert.Equal(t, "", truncateQueryForAudit(""))
	})

	t.Run("query at the cap is not truncated", func(t *testing.T) {
		q := strings.Repeat("a", maxAuditQueryPreviewRunes)
		assert.Equal(t, q, truncateQueryForAudit(q))
	})

	t.Run("long query is capped with marker", func(t *testing.T) {
		q := strings.Repeat("a", maxAuditQueryPreviewRunes+50)
		got := truncateQueryForAudit(q)
		assert.Equal(t, strings.Repeat("a", maxAuditQueryPreviewRunes)+"…[truncated]", got)
		assert.Equal(t, maxAuditQueryPreviewRunes, len([]rune(strings.TrimSuffix(got, "…[truncated]"))))
	})

	t.Run("multi-byte runes are not split", func(t *testing.T) {
		// Each emoji is multi-byte but a single rune; truncation must cut on rune boundaries so the preview stays valid UTF-8.
		q := strings.Repeat("🚀", maxAuditQueryPreviewRunes+10)
		got := truncateQueryForAudit(q)
		assert.True(t, strings.HasSuffix(got, "…[truncated]"))
		assert.Equal(t, strings.Repeat("🚀", maxAuditQueryPreviewRunes), strings.TrimSuffix(got, "…[truncated]"))
	})
}
