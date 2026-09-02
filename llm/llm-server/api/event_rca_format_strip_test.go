package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStripRCAFormatScaffolding(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "header with dashed separator",
			input:    "<<Event RCA Report Format>>\n---------------------------------\n# 📝 Root Cause Analysis (RCA) Report\n\n## Summary",
			expected: "# 📝 Root Cause Analysis (RCA) Report\n\n## Summary",
		},
		{
			name:     "header without separator",
			input:    "<<Event RCA Report Format>>\n# Report body",
			expected: "# Report body",
		},
		{
			name:     "leading whitespace before header",
			input:    "\n\n  <<Custom Format>>\n----\n# Report",
			expected: "# Report",
		},
		{
			name:     "clean report untouched",
			input:    "# 📝 Root Cause Analysis (RCA) Report\n\n## Summary",
			expected: "# 📝 Root Cause Analysis (RCA) Report\n\n## Summary",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "marker not on its own line is untouched",
			input:    "<<inline>> marker mid-text\nbody",
			expected: "<<inline>> marker mid-text\nbody",
		},
		{
			name:     "dashed line kept when not preceded by marker",
			input:    "# Title\n-----\nbody",
			expected: "# Title\n-----\nbody",
		},
		{
			name:     "separator containing non-dashes is kept",
			input:    "<<Format>>\n--- notes ---x\nbody",
			expected: "--- notes ---x\nbody",
		},
		{
			name:     "marker only, no newline",
			input:    "<<Format>>",
			expected: "<<Format>>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, stripRCAFormatScaffolding(tt.input))
		})
	}
}
