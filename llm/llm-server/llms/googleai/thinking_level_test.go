package googleai

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSupportsThinkingLevelOption(t *testing.T) {
	t.Parallel()

	cases := []struct {
		model string
		want  bool
	}{
		// Gemini 3.x — supports the string ThinkingLevel API.
		{"gemini-3.1-pro-preview", true},
		{"gemini-3-flash-preview", true},
		{"Gemini-3.5-Pro", true},
		// Gemini 2.5 thinking models — use the older ThinkingBudget field;
		// rejecting ThinkingLevel here is the bug we are guarding against.
		{"gemini-2.5-flash", false},
		{"gemini-2.5-pro", false},
		{"gemini-2.5-flash-lite", false},
		// Other families — never accept ThinkingLevel.
		{"gemini-2.0-flash", false},
		{"gpt-4o", false},
		{"claude-3-haiku", false},
		{"", false},
	}

	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			assert.Equal(t, tc.want, supportsThinkingLevelOption(tc.model))
		})
	}
}

func TestIsThinkingLevelUnsupportedError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "exact Gemini API rejection (raw)",
			err:  errors.New("Error 400, Message: Thinking level is not supported for this model., Status: INVALID_ARGUMENT, Details: []"),
			want: true,
		},
		{
			name: "wrapped through streaming path",
			err:  errors.New("error in stream mode: Error 400, Message: Thinking level is not supported for this model., Status: INVALID_ARGUMENT, Details: []"),
			want: true,
		},
		{
			name: "case-insensitive match",
			err:  errors.New("THINKING LEVEL IS NOT SUPPORTED for this model"),
			want: true,
		},
		{
			name: "unrelated 400 (cached content)",
			err:  errors.New("Error 403, Message: CachedContent not found (or permission denied), Status: PERMISSION_DENIED"),
			want: false,
		},
		{
			name: "unrelated thinking-tokens error",
			err:  errors.New("thinking tokens exceeded budget"),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isThinkingLevelUnsupportedError(tc.err))
		})
	}
}
