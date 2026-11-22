package openaiclient

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChatUsage_OutputTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                  string
		completion, reasoning int
		want                  int
	}{
		{"no reasoning", 500, 0, 500},
		{"reasoning included", 994, 197, 797},
		{"all reasoning", 64, 64, 0},
		{"reasoning above completion is untrusted", 10, 64, 10},
		{"negative reasoning is untrusted", 10, -1, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var u ChatUsage
			u.CompletionTokens = tt.completion
			u.CompletionTokensDetails.ReasoningTokens = tt.reasoning
			assert.Equal(t, tt.want, u.OutputTokens())
		})
	}
}
