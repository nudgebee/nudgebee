package ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseModelParam(t *testing.T) {
	cases := []struct {
		name         string
		input        any
		wantProvider string
		wantModel    string
		wantErr      bool
	}{
		{"nil", nil, "", "", false},
		{"empty string", "", "", "", false},
		{"whitespace", "   ", "", "", false},
		{"valid", "anthropic/claude-3-5-sonnet", "anthropic", "claude-3-5-sonnet", false},
		{"model contains slash", "openai/gpt-4o/preview", "openai", "gpt-4o/preview", false},
		{"label form with spaces", "googleai / gemini-2.5-flash", "googleai", "gemini-2.5-flash", false},
		{"surrounding whitespace", "  anthropic / claude-3-5-sonnet  ", "anthropic", "claude-3-5-sonnet", false},
		{"no slash", "claude-3-5-sonnet", "", "", true},
		{"only slash", "/", "", "", true},
		{"only spaces and slash", "  /  ", "", "", true},
		{"leading slash", "/claude", "", "", true},
		{"trailing slash", "anthropic/", "", "", true},
		{"non-string", 42, "", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider, model, err := parseModelParam(tc.input)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.wantProvider, provider)
			assert.Equal(t, tc.wantModel, model)
		})
	}
}
