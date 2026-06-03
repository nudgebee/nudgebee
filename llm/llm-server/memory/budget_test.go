package memory

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEstimateTokens(t *testing.T) {
	assert.Equal(t, 0, estimateTokens(""))
	// ~4 chars/token heuristic
	assert.Equal(t, 1, estimateTokens("abcd"))
	assert.Equal(t, 2, estimateTokens("abcdefgh"))
}

func TestTrimToTokenBudget_Passthrough(t *testing.T) {
	got := trimToTokenBudget("short", 1000)
	assert.Equal(t, "short", got)
}

func TestTrimToTokenBudget_ZeroOrNegative(t *testing.T) {
	assert.Equal(t, "hello", trimToTokenBudget("hello", 0))
	assert.Equal(t, "hello", trimToTokenBudget("hello", -1))
}

func TestTrimToTokenBudget_TrimsAtNewlineBoundary(t *testing.T) {
	body := strings.Repeat("line1\n", 10) // ~60 chars, ~15 tokens
	// Budget 5 tokens = ~20 chars; trim should land on a newline
	got := trimToTokenBudget(body, 5)
	assert.True(t, len(got) <= 20+6, "should be close to budget, got %d chars", len(got))
	// Should end at a line boundary (or be shorter than full block)
	assert.True(t, strings.HasSuffix(got, "line1") || !strings.Contains(got, "\n"),
		"trimmed output should end cleanly, got %q", got)
}
