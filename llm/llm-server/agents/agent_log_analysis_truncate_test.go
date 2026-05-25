package agents

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/security"
)

func TestTruncateText_MultipleLines(t *testing.T) {
	agent := LogAnalysisAgent{}
	ctx := security.NewRequestContextForSuperAdmin()

	// Build input: 100 lines, each ~4-5 tokens
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "error: something went wrong here"
	}
	text := strings.Join(lines, "\n")

	// Use a small token budget that will force truncation
	// Each line is roughly 5-6 tokens; budget for ~10 lines worth
	result := agent.truncateText(ctx, text, 60, "fallback", "fallback")

	resultLines := strings.Split(result, "\n")
	assert.Greater(t, len(resultLines), 0, "should return at least 1 line")
	assert.Less(t, len(resultLines), 100, "should have truncated some lines")

	// Verify each returned line is from the original
	for _, line := range resultLines {
		assert.Equal(t, "error: something went wrong here", line)
	}
}

func TestTruncateText_SingleLineExceedsBudget(t *testing.T) {
	agent := LogAnalysisAgent{}
	ctx := security.NewRequestContextForSuperAdmin()

	// Single very long line
	longLine := strings.Repeat("error token ", 200)
	text := longLine

	// Budget too small for even this one line
	result := agent.truncateText(ctx, text, 5, "fallback", "fallback")

	// Should return empty string since the one line exceeds budget
	// (this is a known limitation — but it's better to verify the behavior)
	assert.Equal(t, "", result)
}

func TestTruncateText_AllLinesFit(t *testing.T) {
	agent := LogAnalysisAgent{}
	ctx := security.NewRequestContextForSuperAdmin()

	text := "line one\nline two\nline three"
	// Very large budget — nothing should be truncated
	result := agent.truncateText(ctx, text, 10000, "fallback", "fallback")

	assert.Equal(t, text, result)
}

func TestTruncateText_EmptyInput(t *testing.T) {
	agent := LogAnalysisAgent{}
	ctx := security.NewRequestContextForSuperAdmin()

	result := agent.truncateText(ctx, "", 100, "fallback", "fallback")
	assert.Equal(t, "", result)
}

// This test verifies the fix: previously errorLines were JSON-marshaled into
// compact form like ["line1","line2"] which has NO newlines. Splitting by "\n"
// gave a single element and truncation returned "". Now we join with "\n" first,
// so truncation works correctly on individual lines.
func TestTruncateText_SimulatesErrorLinesTruncation(t *testing.T) {
	agent := LogAnalysisAgent{}
	ctx := security.NewRequestContextForSuperAdmin()

	// Simulate real errorLines
	errorLines := []string{
		`{"asctime": "2025-04-24 14:46:05", "levelname": "ERROR", "message": "Error executing query"}`,
		`{"asctime": "2025-04-24 14:46:05", "levelname": "ERROR", "message": "invalid input syntax for type uuid"}`,
		`{"asctime": "2025-04-24 14:46:04", "levelname": "ERROR", "message": "connection refused to database"}`,
		`{"asctime": "2025-04-24 14:46:03", "levelname": "ERROR", "message": "timeout waiting for response from upstream"}`,
		`{"asctime": "2025-04-24 14:46:02", "levelname": "ERROR", "message": "failed to authenticate with service account"}`,
	}

	// Join with newlines (the new approach)
	text := strings.Join(errorLines, "\n")

	// Budget enough for ~2-3 lines
	result := agent.truncateText(ctx, text, 80, "fallback", "fallback")

	resultLines := strings.Split(result, "\n")
	assert.Greater(t, len(resultLines), 0, "should keep at least 1 line")
	assert.Less(t, len(resultLines), 5, "should truncate some lines")

	// Each returned line should be a complete original line (not cut mid-JSON)
	for _, line := range resultLines {
		assert.Contains(t, line, `"levelname": "ERROR"`, "each line should be a complete error entry")
	}
}
