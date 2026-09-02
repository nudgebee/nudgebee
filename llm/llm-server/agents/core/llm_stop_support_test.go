package core

import "testing"

func TestIsOpenAIModelWithoutStopSupport(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		model    string
		want     bool
	}{
		{"openai gpt-5", "openai", "gpt-5.6-terra", true},
		{"openai o1", "openai", "o1-preview", true},
		{"openai o3", "openai", "o3-mini", true},
		{"openai gpt-4o keeps stop", "openai", "gpt-4o-mini", false},
		// An OpenAI-compatible gateway on the custom provider serves the same
		// models and rejects `stop` the same way.
		{"custom gateway gpt-5", "custom", "gpt-5.6-terra", true},
		{"custom gateway o3", "custom", "o3-mini", true},
		{"custom gateway non-openai model", "custom", "gemma-4-26b", false},
		{"custom provider casing", "Custom", "gpt-5.6-terra", true},
		{"unrelated provider", "googleai", "gemini-3-flash-preview", false},
		{"unrelated provider gpt-shaped model", "anthropic", "claude-sonnet-5", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsOpenAIModelWithoutStopSupport(tc.provider, tc.model); got != tc.want {
				t.Fatalf("IsOpenAIModelWithoutStopSupport(%q, %q) = %v, want %v",
					tc.provider, tc.model, got, tc.want)
			}
		})
	}
}
