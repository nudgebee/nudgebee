package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTruncateRecommendationJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		wantTruc bool // expect truncation
	}{
		{
			name:     "short string stays unchanged",
			input:    map[string]any{"recommendation": `{"action":"scale_down"}`},
			wantTruc: false,
		},
		{
			name:     "long string is truncated",
			input:    map[string]any{"recommendation": strings.Repeat("x", 1000)},
			wantTruc: true,
		},
		{
			name:     "byte slice is handled",
			input:    map[string]any{"recommendation": []byte(strings.Repeat("y", 1000))},
			wantTruc: true,
		},
		{
			name:     "missing field is no-op",
			input:    map[string]any{"category": "RightSizing"},
			wantTruc: false,
		},
		{
			name:     "exactly at limit stays unchanged",
			input:    map[string]any{"recommendation": strings.Repeat("z", recommendationMaxJSONChars)},
			wantTruc: false,
		},
		{
			name:     "one over limit is truncated",
			input:    map[string]any{"recommendation": strings.Repeat("z", recommendationMaxJSONChars+1)},
			wantTruc: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateRecommendationJSON(tt.input, 0, 0)
			rec, ok := result["recommendation"]
			if !ok {
				// Field wasn't present — nothing to check for truncation
				assert.False(t, tt.wantTruc)
				return
			}
			s, ok := rec.(string)
			assert.True(t, ok, "recommendation should be string after truncation")
			if tt.wantTruc {
				assert.True(t, strings.HasSuffix(s, "...(truncated)"))
				assert.LessOrEqual(t, len(s), recommendationMaxJSONChars+len("...(truncated)"))
			} else {
				assert.False(t, strings.HasSuffix(s, "...(truncated)"))
			}
		})
	}
}
