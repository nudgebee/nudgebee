package openai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The prefix checks these replaced did not match the id format our own gateway
// serves, so a gpt-5 model reached through it was not reported as reasoning-capable.
func TestSupportsReasoning_MatchesNamespacedFamilies(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  bool
	}{
		{"gpt-5", true},
		{"gpt-5-mini", true},
		{"openai/gpt-5.6-terra", true},
		{"openai/gpt-5.6-sol", true},
		{"azure:gpt-5", true},
		{"o4-mini", true},
		{"openai/o4-mini", true},
		{"openai/o5", true},

		{"gpt-4o", false},
		{"gpt-50", false},
		{"o45-experimental", false},
		{"claude-sonnet-5", false},
	} {
		t.Run(tc.model, func(t *testing.T) {
			o := &LLM{client: nil}
			o.model = tc.model
			assert.Equal(t, tc.want, o.SupportsReasoning())
		})
	}
}
