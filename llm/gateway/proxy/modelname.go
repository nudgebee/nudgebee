package proxy

import (
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

// genericProviderAliases maps an explicit provider prefix ("provider/model") to a
// Bifrost provider. "google" is accepted as an alias for Gemini's public API.
var genericProviderAliases = map[string]schemas.ModelProvider{
	"anthropic": schemas.Anthropic,
	"openai":    schemas.OpenAI,
	"gemini":    schemas.Gemini,
	"google":    schemas.Gemini,
}

// resolveModelProvider maps a model name from the generic /v1 endpoint to the
// provider that serves it and the bare model name to send. Two forms are accepted:
//
//   - explicit "provider/model" (e.g. "anthropic/claude-opus-4-8") — unambiguous;
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
