package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractAnomalySQL(t *testing.T) {
	const sql = "SELECT * FROM anomaly ORDER BY evaluated_at DESC LIMIT 5"

	tests := []struct {
		name      string
		command   string
		arguments map[string]any
		expected  string
	}{
		{
			name:     "raw SQL passes through",
			command:  sql,
			expected: sql,
		},
		{
			name:     "JSON-wrapped under command key",
			command:  `{"command":"` + sql + `"}`,
			expected: sql,
		},
		{
			name:     "JSON-wrapped under query key (the failure from the benchmark)",
			command:  `{"query":"DESC anomaly"}`,
			expected: "DESC anomaly",
		},
		{
			name:     "JSON-wrapped under sql key",
			command:  `{"sql":"` + sql + `"}`,
			expected: sql,
		},
		{
			name:      "query routed via arguments map",
			command:   `{"query":"DESC anomaly"}`,
			arguments: map[string]any{"query": "DESC anomaly"},
			expected:  "DESC anomaly",
		},
		{
			name:      "arguments-only query when command empty",
			command:   "",
			arguments: map[string]any{"query": sql},
			expected:  sql,
		},
		{
			name:     "non-JSON descriptive text returned unchanged",
			command:  "show me cost anomalies",
			expected: "show me cost anomalies",
		},
		{
			name:     "surrounding whitespace is trimmed",
			command:  "  " + sql + "  ",
			expected: sql,
		},
		{
			name:     "whitespace trimmed from JSON-extracted SQL",
			command:  `{"query":"  DESC anomaly  "}`,
			expected: "DESC anomaly",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, extractAnomalySQL(tc.command, tc.arguments))
		})
	}
}
