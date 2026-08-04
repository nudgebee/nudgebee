package engine

import (
	"log/slog"
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
	ProjectID    string // llm_provider_project_id (vertex GCP project)
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

// buildCred converts OPERATOR config into a Bifrost Key. For cloud providers it
// populates the structured cloud config (Bedrock here); for api-key providers it
// sets Value. Operator Bedrock may be KEYLESS: with no static access/secret keys,
// Bifrost falls back to the AWS default credential chain (IRSA / instance role) —
// the standard keyless posture on EKS. Returns ok=false when no credential material
// is present (and the provider is not one that can run keyless).
func buildCred(cfg ProviderCredsConfig) (schemas.ModelProvider, providerCred, bool) {
	provider := NormalizeProvider(cfg.Provider)
	key, ok := buildKey(provider, cfg, true /* allowKeyless: operator Bedrock may use IRSA */)
	if !ok {
		return provider, providerCred{}, false
	}
	return provider, providerCred{key: key, endpoint: cfg.Endpoint}, true
}

// BuildTenantKey builds a per-tenant BYO key from resolved integration config, for
// the EE credential resolver. Unlike operator creds, tenant Bedrock MUST carry static
// credentials: a keyless Bedrock key would fall through to the gateway pod's own IAM
// role (the OPERATOR's identity), so an empty-key tenant Bedrock config is rejected
// (ok=false) and the request falls back to the operator default instead of silently
// borrowing the operator's role. The returned key is tagged with a "-tenant" ID.
func BuildTenantKey(cfg ProviderCredsConfig) (schemas.ModelProvider, schemas.Key, bool) {
	provider := NormalizeProvider(cfg.Provider)
	key, ok := buildKey(provider, cfg, false /* allowKeyless: never for tenant BYO */)
	if !ok {
		return provider, schemas.Key{}, false
	}
	key.ID = string(provider) + "-tenant"
	return provider, key, true
}

// buildKey assembles the Bifrost Key for a provider from config. For Bedrock it
// builds the structured cloud config; for api-key providers it sets Value. When
// allowKeyless is true, an empty-credential Bedrock key is still returned (Bifrost
// resolves creds via the AWS default chain / IRSA); when false, static access+secret
// are required. ok=false means no usable credential for this config.
func buildKey(provider schemas.ModelProvider, cfg ProviderCredsConfig, allowKeyless bool) (schemas.Key, bool) {
	// No provider named (e.g. an integration row missing llm_provider) — nothing to build.
	if provider == "" {
		return schemas.Key{}, false
	}
	// Models=["*"] is required: core's key pool denies keys with an empty model
	// list by default, so a wildcard is what lets this key serve any model.
	key := schemas.Key{ID: string(provider) + "-nb", Models: schemas.WhiteList{"*"}}

	switch provider {
	case schemas.Bedrock:
		// Partial static credentials are a misconfiguration — access and secret must be
		// set together (matches llm-server). Reject rather than sign with a half key.
		if (cfg.AccessKey == "") != (cfg.SecretKey == "") {
			slog.Warn("engine: bedrock has partial static credentials; ignoring (access_key and secret_key must both be set)")
			return schemas.Key{}, false
		}
		keyless := cfg.AccessKey == "" && cfg.SecretKey == ""
		if keyless && !allowKeyless {
			// Tenant BYO with no static creds: NOT a usable tenant credential. Falling
			// back to the AWS default chain here would sign with the pod's IAM role — the
			// operator's identity — for a tenant. Reject so the operator default is used.
			return schemas.Key{}, false
		}
		bk := &schemas.BedrockKeyConfig{}
		if !keyless {
			bk.AccessKey = schemas.SecretVar{Val: cfg.AccessKey}
			bk.SecretKey = schemas.SecretVar{Val: cfg.SecretKey}
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
	case schemas.Azure:
		// Azure OpenAI (v1 API): the resource endpoint is REQUIRED and travels ON the
		// key (AzureKeyConfig.Endpoint), so this works for both operator config and
		// per-tenant BYO (a DirectKey carries the endpoint). Auth is an api-key (common)
		// or, keyless, the Azure default credential chain (managed identity). Keyless is
		// the pod's identity, so it is OPERATOR-ONLY (allowKeyless) — a tenant must supply
		// a static api-key, never borrow the pod's managed identity. Azure AD client-secret
		// auth (client/tenant id) is a possible follow-up; not wired here.
		// Sanitize: a pasted endpoint often has a trailing slash or stray whitespace, and
		// Bifrost's Azure chat path builds "{endpoint}/openai/v1/chat/completions" without
		// trimming — a trailing slash would yield a double-slash path and 404.
		endpoint := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
		if endpoint == "" {
			return schemas.Key{}, false
		}
		keyless := cfg.APIKey == ""
		if keyless && !allowKeyless {
			return schemas.Key{}, false
		}
		key.AzureKeyConfig = &schemas.AzureKeyConfig{Endpoint: schemas.SecretVar{Val: endpoint}}
		if !keyless {
			key.Value = schemas.SecretVar{Val: cfg.APIKey}
		}
	case schemas.Vertex:
		// Vertex AI: project + region are REQUIRED and travel ON the key (VertexKeyConfig),
		// so this works for operator config and per-tenant BYO alike. Auth is a service-
		// account JSON (llm_provider_api_key) or, keyless, GCP Application Default
		// Credentials (Workload Identity on GKE). Keyless = the pod's identity, so it is
		// OPERATOR-ONLY (allowKeyless) — a tenant must supply a static service-account key,
		// never borrow the pod's identity. Region must be a GCP region (e.g. us-central1);
		// the llm_provider_region default (us-west-2) is an AWS region and won't work here.
		//
		// Trim stray whitespace: project/region go straight into the request URL (a space
		// silently breaks routing), and trimming the credential first means a whitespace-only
		// value is treated as keyless (clean fallback) rather than a downstream JSON error.
		projectID := strings.TrimSpace(cfg.ProjectID)
		region := strings.TrimSpace(cfg.Region)
		creds := strings.TrimSpace(cfg.APIKey)
		if projectID == "" || region == "" {
			return schemas.Key{}, false
		}
		if region == "us-west-2" {
			// The shared llm_provider_region default is us-west-2 (an AWS region). It is
			// non-empty, so it passes the check above but fails at request time with an
			// opaque GCP error — warn so the misconfig is visible at config time.
			slog.Warn("engine: vertex region is \"us-west-2\", an AWS region — set a GCP region (e.g. us-central1)")
		}
		keyless := creds == ""
		if keyless && !allowKeyless {
			return schemas.Key{}, false
		}
		vk := &schemas.VertexKeyConfig{
			ProjectID: schemas.SecretVar{Val: projectID},
			Region:    schemas.SecretVar{Val: region},
		}
		if !keyless {
			vk.AuthCredentials = schemas.SecretVar{Val: creds} // service-account JSON
		}
		key.VertexKeyConfig = vk
	case schemas.Ollama, schemas.VLLM, schemas.SGL:
		// Self-hosted OpenAI-compatible servers: reached by base URL (carried via the
		// provider config's BaseURL from cfg.Endpoint) with an OPTIONAL bearer token.
		//
		// OPERATOR-ONLY, enforced here as a hard boundary. allowKeyless is true only on
		// the operator path (buildCred) and false on the tenant path (BuildTenantKey),
		// so `!allowKeyless` rejects every tenant self-hosted config outright. This is
		// defense-in-depth: a per-tenant DirectKey cannot carry a base URL, so even if a
		// tenant config somehow supplied an endpoint, BuildTenantKey would drop it and
		// the request would silently route to the OPERATOR's server — a cross-tenant
		// leak. Rejecting on the tenant path regardless of endpoint prevents that.
		// The endpoint is required on the operator path (nowhere to send otherwise).
		if !allowKeyless || cfg.Endpoint == "" {
			return schemas.Key{}, false
		}
		if cfg.APIKey != "" {
			key.Value = schemas.SecretVar{Val: cfg.APIKey}
		}
	default:
		if cfg.APIKey == "" {
			return schemas.Key{}, false
		}
		key.Value = schemas.SecretVar{Val: cfg.APIKey}
	}

	return key, true
}
