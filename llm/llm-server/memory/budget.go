package memory

import "strings"

// estimateTokens provides a rough token count for a string.
// Uses the ~4 chars per token heuristic which is conservative for English.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

// trimToTokenBudget truncates a rendered block to fit within a token budget.
// It trims at the last newline boundary before the limit to avoid cutting mid-line.
func trimToTokenBudget(block string, maxTokens int) string {
	if maxTokens <= 0 || block == "" {
		return block
	}
	if estimateTokens(block) <= maxTokens {
		return block
	}

	maxChars := maxTokens * 4
	if maxChars >= len(block) {
		return block
	}

	truncated := block[:maxChars]
	// Find last newline to avoid cutting mid-line.
	if idx := strings.LastIndex(truncated, "\n"); idx > 0 {
		truncated = truncated[:idx]
	}
	return truncated
}
