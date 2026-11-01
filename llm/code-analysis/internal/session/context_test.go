package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatQuerySection(t *testing.T) {
	tests := []struct {
		name     string
		ctx      *SessionContext
		expected string
	}{
		{
			name:     "nil context",
			ctx:      nil,
			expected: "",
		},
		{
			name: "identical task and original query",
			ctx: &SessionContext{
				OriginalQuery: "fix the bug",
				Task:          "fix the bug",
			},
			expected: "=== USER QUERY ===\nfix the bug\n\n",
		},
		{
			name: "case-insensitive identical task and original query",
			ctx: &SessionContext{
				OriginalQuery: "Fix The Bug",
				Task:          "fix the bug",
			},
			expected: "=== USER QUERY ===\nFix The Bug\n\n",
		},
		{
			name: "empty task falls back to user query",
			ctx: &SessionContext{
				OriginalQuery: "investigate latency",
				Task:          "",
			},
			expected: "=== USER QUERY ===\ninvestigate latency\n\n",
		},
		{
			name: "distinct task and original query",
			ctx: &SessionContext{
				OriginalQuery: "Read runbook X and fix issue",
				Task:          "Inspect auth handler",
			},
			expected: "=== USER REQUEST ===\nRead runbook X and fix issue\n\n=== DELEGATED TASK ===\nInspect auth handler\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.ctx.FormatQuerySection())
		})
	}
}
