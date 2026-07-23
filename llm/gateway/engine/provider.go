package engine

import (
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

// ProviderCredsConfig is one operator-configured provider credential, populated
// from the LLM_PROVIDER_* env family (mirroring llm-server's naming). It is the
// operator/account credential source.
type ProviderCredsConfig struct {
	Provider     string // llm_provider (e.g. "anthropic", "gemini", "bedrock")
	APIKey       string // llm_provider_api_key
	Endpoint     string // llm_provider_api_endpoint (base URL override)
	Region       string // llm_provider_region (cloud)
	AccessKey    string // llm_provider_access_key (bedrock)
	SecretKey    string // llm_provider_secret_key (bedrock)
	SessionToken string // llm_provider_session_token (bedrock)
}

// providerCred is a resolved credential the nbAccount hands to core via
// GetKeysForProvider (plus an optional base-URL override on the endpoint field).
type providerCred struct {
	key      schemas.Key
	endpoint string
}

// NormalizeProvider maps llm-server provider names to Bifrost provider constants.
func NormalizeProvider(name string) schemas.ModelProvider {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "anthropic":
		return schemas.Anthropic
	case "openai":
		return schemas.OpenAI
	case "bedrock":
		return schemas.Bedrock
	case "gemini", "googleai", "google":
		return schemas.Gemini
	case "vertex", "vertexai", "vertexai_endpoint":
		return schemas.Vertex
	case "azure":
		return schemas.Azure
	case "huggingface", "hf":
		return schemas.HuggingFace
	default:
		return schemas.ModelProvider(strings.ToLower(strings.TrimSpace(name)))
	}
}

// buildCred converts operator config into a Bifrost Key. For cloud providers it
// populates the structured cloud config (Bedrock here); for api-key providers it
// sets Value. Returns ok=false when no credential material is present.
func buildCred(cfg ProviderCredsConfig) (schemas.ModelProvider, providerCred, bool) {
	provider := NormalizeProvider(cfg.Provider)
	// Models=["*"] is required: core's key pool denies keys with an empty model
	// list by default, so a wildcard is what lets this key serve any model.
	key := schemas.Key{ID: string(provider) + "-nb", Models: schemas.WhiteList{"*"}}

	switch provider {
	case schemas.Bedrock:
		if cfg.AccessKey == "" && cfg.SecretKey == "" {
			return provider, providerCred{}, false
		}
		bk := &schemas.BedrockKeyConfig{
			AccessKey: schemas.SecretVar{Val: cfg.AccessKey},
			SecretKey: schemas.SecretVar{Val: cfg.SecretKey},
		}
		if cfg.SessionToken != "" {
			st := schemas.SecretVar{Val: cfg.SessionToken}
			bk.SessionToken = &st
		}
		if cfg.Region != "" {
			r := schemas.SecretVar{Val: cfg.Region}
			bk.Region = &r
		}
		key.BedrockKeyConfig = bk
	default:
		if cfg.APIKey == "" {
			return provider, providerCred{}, false
		}
		key.Value = schemas.SecretVar{Val: cfg.APIKey}
	}

	return provider, providerCred{key: key, endpoint: cfg.Endpoint}, true
}
