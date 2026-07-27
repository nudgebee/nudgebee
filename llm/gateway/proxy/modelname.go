package proxy

import (
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

// genericProviderAliases maps an explicit provider prefix ("provider/model") to a
// Bifrost provider. "google" is accepted as an alias for Gemini's public API. The
// second group are OpenAI-compatible api-key providers (bearer token, default
// endpoint) — drop-in on the generic endpoint; operator/tenant credentials flow
// through the existing api-key plumbing, so the alias is all that's needed to route.
var genericProviderAliases = map[string]schemas.ModelProvider{
	"anthropic":   schemas.Anthropic,
	"openai":      schemas.OpenAI,
	"gemini":      schemas.Gemini,
	"google":      schemas.Gemini,
	"huggingface": schemas.HuggingFace,
	"hf":          schemas.HuggingFace,
	"bedrock":     schemas.Bedrock,
	"groq":        schemas.Groq,
	"mistral":     schemas.Mistral,
	"cohere":      schemas.Cohere,
	"deepseek":    schemas.DeepSeek,
	"xai":         schemas.XAI,
	"perplexity":  schemas.Perplexity,
	"openrouter":  schemas.OpenRouter,
	"fireworks":   schemas.Fireworks,
	"cerebras":    schemas.Cerebras,
	"nebius":      schemas.Nebius,
	"parasail":    schemas.Parasail,
	"ollama":      schemas.Ollama,
	"vllm":        schemas.VLLM,
	"sgl":         schemas.SGL,
}

// resolveModelProvider maps a model name from the generic /v1 endpoint to the
// provider that serves it and the bare model name to send. Two forms are accepted:
//
//   - explicit "provider/model" (e.g. "anthropic/claude-opus-4-8") — unambiguous.
//     The split is on the first "/", so a HuggingFace repo id keeps its own slash
//     (e.g. "huggingface/meta-llama/Llama-3.1-8B-Instruct" → model
//     "meta-llama/Llama-3.1-8B-Instruct"). HuggingFace has no bare-name form: its
//     ids collide with the "provider/model" shape, so it must be addressed explicitly.
//     Bedrock is likewise explicit-only — its model id (e.g.
//     "bedrock/anthropic.claude-3-5-sonnet-20241022-v2:0", or an inference-profile
//     id like "bedrock/us.anthropic.claude-...") is passed through after the prefix.
//     The first-slash split also lets OpenRouter's "vendor/model" ids ride through:
//     "openrouter/anthropic/claude-3.5-sonnet" → model "anthropic/claude-3.5-sonnet".
//   - a bare model name (e.g. "gpt-5", "claude-opus-4-8", "gemini-3.1-flash"),
//     resolved by a well-known provider prefix.
//
// ok is false when the name is empty or matches neither form, so the caller can
// return a clear 400 rather than guessing a provider.
func resolveModelProvider(model string) (provider schemas.ModelProvider, name string, ok bool) {
	name = strings.TrimSpace(model)
	if name == "" {
		return "", "", false
	}
	// Explicit "provider/model" wins when the prefix is a known provider; otherwise
	// the "/" is part of the model name (e.g. Gemini's "models/…") and we fall
	// through to the prefix heuristic on the whole string.
	if prefix, rest, found := strings.Cut(name, "/"); found && rest != "" {
		if p, known := genericProviderAliases[strings.ToLower(prefix)]; known {
			return p, rest, true
		}
	}
	switch {
	case hasAnyPrefix(name, "claude"):
		return schemas.Anthropic, name, true
	case hasAnyPrefix(name, "gpt", "chatgpt", "o1", "o3", "o4"):
		return schemas.OpenAI, name, true
	case hasAnyPrefix(name, "gemini"):
		return schemas.Gemini, name, true
	}
	return "", "", false
}

// hasAnyPrefix reports whether s (case-insensitively) starts with any of prefixes.
func hasAnyPrefix(s string, prefixes ...string) bool {
	l := strings.ToLower(s)
	for _, p := range prefixes {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}
