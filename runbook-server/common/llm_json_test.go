package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractJSONFromLLMResponse(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      any
		expectErr bool
	}{
		{
			name:  "clean json object",
			input: `{"summary":"ok","severity":"high"}`,
			want:  map[string]any{"summary": "ok", "severity": "high"},
		},
		{
			name:  "object with surrounding whitespace",
			input: "  \n {\"summary\":\"ok\"} \n ",
			want:  map[string]any{"summary": "ok"},
		},
		{
			name:  "json array",
			input: `["a","b"]`,
			want:  []any{"a", "b"},
		},
		{
			name:  "fenced json with prose around it",
			input: "Based on my investigation:\n```json\n{\"summary\":\"ok\"}\n```\nLet me know if you need more.",
			want:  map[string]any{"summary": "ok"},
		},
		{
			name:  "bare fence without json tag",
			input: "Result:\n```\n{\"summary\":\"ok\"}\n```",
			want:  map[string]any{"summary": "ok"},
		},
		{
			name:  "multiple fences with non-json first",
			input: "First, run this:\n```bash\necho 'hello'\n```\nThen you get:\n```json\n{\"summary\":\"ok\"}\n```",
			want:  map[string]any{"summary": "ok"},
		},
		{
			name:  "non-json fence with braces before json fence",
			input: "Config:\n```yaml\nmap: {nested: true}\n```\nResult:\n```json\n{\"summary\":\"ok\"}\n```",
			want:  map[string]any{"summary": "ok"},
		},
		{
			name:  "unfenced prose-wrapped object falls back to delimiters",
			input: `Here is the answer: {"summary":"ok"} hope that helps.`,
			want:  map[string]any{"summary": "ok"},
		},
		{
			name:  "unfenced prose-wrapped array falls back to delimiters",
			input: `The items are: ["a","b"] done.`,
			want:  []any{"a", "b"},
		},
		{
			name:  "nested object via delimiter fallback",
			input: `prefix {"a":{"b":1}} suffix`,
			want:  map[string]any{"a": map[string]any{"b": float64(1)}},
		},
		{
			name:      "no json present",
			input:     "I could not complete the investigation, sorry.",
			expectErr: true,
		},
		{
			name:      "malformed json in fence and body",
			input:     "```json\n{\"summary\": }\n```",
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractJSONFromLLMResponse(tc.input)
			if tc.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "could not extract valid JSON from response")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
