package proxy

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
)

func TestResolveModelProvider(t *testing.T) {
	cases := []struct {
		in           string
		wantProvider schemas.ModelProvider
		wantModel    string
		wantOK       bool
	}{
		// Explicit provider/model.
		{"anthropic/claude-opus-4-8", schemas.Anthropic, "claude-opus-4-8", true},
		{"openai/gpt-5", schemas.OpenAI, "gpt-5", true},
		{"gemini/gemini-3.1-flash", schemas.Gemini, "gemini-3.1-flash", true},
		{"google/gemini-3.1-flash", schemas.Gemini, "gemini-3.1-flash", true},
		{"OpenAI/gpt-5", schemas.OpenAI, "gpt-5", true}, // provider prefix is case-insensitive
		// Bare well-known names via prefix heuristic.
		{"claude-opus-4-8", schemas.Anthropic, "claude-opus-4-8", true},
		{"gpt-5", schemas.OpenAI, "gpt-5", true},
		{"o3-mini", schemas.OpenAI, "o3-mini", true},
		{"gemini-3.1-flash", schemas.Gemini, "gemini-3.1-flash", true},
		// An unknown prefix before "/" is not a provider, and the whole string
		// ("models/…") has no known model prefix, so it doesn't resolve — the caller
		// returns a clear 400 asking for "provider/model".
		{"models/gemini-3.1-flash", "", "", false},
		// Unresolvable.
		{"", "", "", false},
		{"   ", "", "", false},
		{"mystery-model-9000", "", "", false},
		{"llama-3", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			p, m, ok := resolveModelProvider(tc.in)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantProvider, p)
				assert.Equal(t, tc.wantModel, m)
			}
		})
	}
}
