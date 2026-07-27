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
		// HuggingFace repo ids keep their own slash (split is on the first "/").
		{"huggingface/meta-llama/Llama-3.1-8B-Instruct", schemas.HuggingFace, "meta-llama/Llama-3.1-8B-Instruct", true},
		{"hf/deepseek-ai/DeepSeek-V3", schemas.HuggingFace, "deepseek-ai/DeepSeek-V3", true},
		// Bedrock: explicit prefix, model id (incl. inference-profile ids) passed through.
		{"bedrock/anthropic.claude-3-5-sonnet-20241022-v2:0", schemas.Bedrock, "anthropic.claude-3-5-sonnet-20241022-v2:0", true},
		{"bedrock/us.anthropic.claude-3-5-haiku-20241022-v1:0", schemas.Bedrock, "us.anthropic.claude-3-5-haiku-20241022-v1:0", true},
		// OpenAI-compatible api-key providers — explicit prefix routes to the provider.
		{"groq/llama-3.3-70b-versatile", schemas.Groq, "llama-3.3-70b-versatile", true},
		{"mistral/mistral-large-latest", schemas.Mistral, "mistral-large-latest", true},
		{"deepseek/deepseek-chat", schemas.DeepSeek, "deepseek-chat", true},
		{"xai/grok-4", schemas.XAI, "grok-4", true},
		{"perplexity/sonar", schemas.Perplexity, "sonar", true},
		{"cohere/command-r-plus", schemas.Cohere, "command-r-plus", true},
		{"fireworks/llama-v3p1-70b", schemas.Fireworks, "llama-v3p1-70b", true},
		{"cerebras/llama-3.3-70b", schemas.Cerebras, "llama-3.3-70b", true},
		{"nebius/deepseek-v3", schemas.Nebius, "deepseek-v3", true},
		{"parasail/some-model", schemas.Parasail, "some-model", true},
		// OpenRouter model ids are themselves "vendor/model"; the first-slash split
		// keeps the rest intact.
		{"openrouter/anthropic/claude-3.5-sonnet", schemas.OpenRouter, "anthropic/claude-3.5-sonnet", true},
		// Self-hosted OpenAI-compatible servers.
		{"ollama/llama3.3", schemas.Ollama, "llama3.3", true},
		{"vllm/meta-llama/Llama-3.1-8B-Instruct", schemas.VLLM, "meta-llama/Llama-3.1-8B-Instruct", true},
		{"sgl/qwen2.5-72b", schemas.SGL, "qwen2.5-72b", true},
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
