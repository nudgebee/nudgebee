package adapter

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFirstNonEmptyStringField(t *testing.T) {
	resp := map[string]any{
		"message":            "  already present on main  ",
		"pr_creation_reason": "reason text",
		"empty":              "",
		"blank":              "   ",
		"number":             42,
	}

	// Returns the first present, non-blank field (trimmed).
	assert.Equal(t, "already present on main", firstNonEmptyStringField(resp, "message", "pr_creation_reason"))
	// Skips missing, empty, blank, and non-string fields.
	assert.Equal(t, "reason text", firstNonEmptyStringField(resp, "missing", "empty", "blank", "number", "pr_creation_reason"))
	// No match → empty string.
	assert.Equal(t, "", firstNonEmptyStringField(resp, "missing", "empty", "blank"))
}

func TestTruncateForStatus(t *testing.T) {
	short := "no change needed"
	assert.Equal(t, short, truncateForStatus(short), "short strings pass through unchanged")

	long := strings.Repeat("a", 900)
	got := truncateForStatus(long)
	assert.True(t, strings.HasSuffix(got, "…(truncated)"), "long strings get a truncation marker")
	assert.Less(t, len([]rune(got)), 900, "truncated result is shorter than the input")

	// Multi-byte runes must not be split mid-rune.
	multibyte := strings.Repeat("é", 900)
	assert.True(t, strings.HasSuffix(truncateForStatus(multibyte), "…(truncated)"))
}

func TestExtractPRFromAgentResponse(t *testing.T) {
	tests := []struct {
		name    string
		resp    map[string]any
		wantURL string
	}{
		{
			name:    "automated_fix_pr_info",
			resp:    map[string]any{"automated_fix_pr_info": map[string]any{"url": "https://github.com/o/r/pull/1", "number": 1}},
			wantURL: "https://github.com/o/r/pull/1",
		},
		{
			name:    "fix_pr fallback",
			resp:    map[string]any{"fix_pr": map[string]any{"url": "https://github.com/o/r/pull/2"}},
			wantURL: "https://github.com/o/r/pull/2",
		},
		{
			name:    "pr_url fallback",
			resp:    map[string]any{"pr_url": "https://github.com/o/r/pull/3"},
			wantURL: "https://github.com/o/r/pull/3",
		},
		{
			name:    "no pr",
			resp:    map[string]any{"execution_status": "success"},
			wantURL: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, _ := extractPRFromAgentResponse(tt.resp)
			assert.Equal(t, tt.wantURL, url)
		})
	}
}
