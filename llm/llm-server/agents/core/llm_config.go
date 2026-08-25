package core

// This file handles the configuration and instantiation of Large Language Models (LLMs).
//
// Configuration values are resolved with the following precedence (highest to lowest):
// 1. Context override / conversation override (per-request user-explicit).
// 2. DB Agent-specific.
// 3. DB tier-specific.
// 4. DB Global.
// 5. ENV Agent-specific (e.g., LLM_PROVIDER_MY_AGENT).
// 6. ENV tier-specific (e.g., LLM_TIER_PROVIDER_REASONING).
// 7. ENV Global (e.g., LLM_PROVIDER).
//
// **DB always beats ENV at any specificity.** Rationale: multi-tenant clients
// onboard via UI, which writes to integration_config_values. ENV is the
// operator process-level default. When DB has a value it is the tenant's
// canonical configuration and must not be silently overridden by a stale
// agent-specific ENV. This avoids project-key fragmentation on the Google AI
// CachedContent layer (different cache slot owners across calls → 403).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"nudgebee/llm/config"
	"nudgebee/llm/llms/azure"
	"nudgebee/llm/llms/bedrock"
	"nudgebee/llm/llms/googleai"
	"nudgebee/llm/llms/googleai/vertex"
	vertexendpoint "nudgebee/llm/llms/googleai/vertexai_endpoint"
	"nudgebee/llm/llms/huggingface"
	"nudgebee/llm/llms/sagemaker"
	"nudgebee/llm/security"
	"nudgebee/llm/security/egressfilter"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/anthropic"
	"github.com/tmc/langchaingo/llms/openai"
)

const llmProviderFormat = "llm_provider_%s"
const llmModelFormat = "llm_model_name_%s"
const llmProviderApiKeyFormat = "llm_provider_api_key_%s"
const llmProviderApiEndpointFormat = "llm_provider_api_endpoint_%s"
const llmProviderApiVersionFormat = "llm_provider_api_version_%s"
const llmProviderApiTypeFormat = "llm_provider_api_type_%s"
const llmProviderRegionFormat = "llm_provider_region_%s"
const llmProviderAccessKeyFormat = "llm_provider_access_key_%s"
const llmProviderSecretKeyFormat = "llm_provider_secret_key_%s"
const llmProviderSessionTokenFormat = "llm_provider_session_token_%s"
const llmModelAdapterFormat = "llm_provider_adapter_id_%s"
const llmModelAdapterSupportFormat = "llm_provider_require_adapter_id_%s"
const llmModelFallbackFormat = "llm_model_fallbacks_%s"

// Per-provider TTFT (time-to-first-token) timeout controls. Env-var lookup
// follows the same AutomaticEnv/uppercase convention as the other llm_provider_*
// format keys — e.g. fmt.Sprintf(llmProviderTTFTTimeoutEnabledFormat, "huggingface")
// resolves the viper key `llm_provider_ttft_timeout_enabled_huggingface`, which
// AutomaticEnv reads from the env var LLM_PROVIDER_TTFT_TIMEOUT_ENABLED_HUGGINGFACE.
//
// Enable is required per-provider: the watchdog does NOT fire for any provider
// unless its ENABLED key is explicitly true. When enabled without a per-provider
// SECONDS override, the global config.Config.LlmProviderTTFTTimeoutSeconds is used.
const llmProviderTTFTTimeoutEnabledFormat = "llm_provider_ttft_timeout_enabled_%s"
const llmProviderTTFTTimeoutSecondsFormat = "llm_provider_ttft_timeout_seconds_%s"

// Category-tier config keys. A tier (reasoning / retrieval / summary)
// is configured like an agent but in its own namespace so it cannot collide
// with an agent that happens to share the name.
const llmTierProviderFormat = "llm_tier_provider_%s"
const llmTierModelFormat = "llm_tier_model_%s"
const llmTierModelFallbackFormat = "llm_tier_model_fallbacks_%s"
const llmTierApiKeyFormat = "llm_tier_api_key_%s"
const llmTierApiEndpointFormat = "llm_tier_api_endpoint_%s"
const llmTierApiVersionFormat = "llm_tier_api_version_%s"
const llmTierApiTypeFormat = "llm_tier_api_type_%s"
const llmTierRegionFormat = "llm_tier_region_%s"
const llmTierAccessKeyFormat = "llm_tier_access_key_%s"
const llmTierSecretKeyFormat = "llm_tier_secret_key_%s"
const llmTierSessionTokenFormat = "llm_tier_session_token_%s"

// ModelTier is the optional category an LLM call opts into so ResolveLLMConfig
// can pick a category-specific model. It is read from the request context
// (ContextKeyModelTier). A category is NOT mandatory — an untagged call has an
// empty tier and resolves through the normal flow (global/agent/conversation).
type ModelTier string

const (
	ModelTierReasoning ModelTier = "reasoning"
	ModelTierRetrieval ModelTier = "retrieval"
	ModelTierSummary   ModelTier = "summary"
)

type llmCacheEntry struct {
	model llms.Model
	ts    time.Time
}

var (
	llmClientCache      = make(map[string]llmCacheEntry)
	llmClientCacheMutex sync.RWMutex
	llmClientCacheTTL   = 1 * time.Hour
)

// Bucket-isolates the SDK-client / cached-content caches so per-tier or
// per-agent credential overrides on the same (provider, model, account)
// don't reuse a client built with another tier's creds.
func credsFingerprint(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(h[:4])
}

func resolveCredsFingerprint(accountId, provider, agentName string, appendAgentName bool, resolution ...*LLMConfigResolution) string {
	// OAuth settings and extra headers join the fingerprint so editing them
	// evicts cached clients — otherwise a rotated client secret or changed
	// header keeps serving the old transport until the cache TTL lapses.
	var res *LLMConfigResolution
	if len(resolution) > 0 {
		res = resolution[0]
	}
	auth := resolveLLMAuthSettings(accountId, res)
	extraHeaders := make([]string, 0, len(auth.ExtraHeaders))
	for k, v := range auth.ExtraHeaders {
		extraHeaders = append(extraHeaders, k+"="+v)
	}
	sort.Strings(extraHeaders)
	return credsFingerprint(
		getLLMApiKey(accountId, provider, agentName, appendAgentName, resolution...),
		getLLMApiEndpoint(accountId, provider, agentName, appendAgentName, resolution...),
		getLLMApiVersion(accountId, provider, agentName, appendAgentName, resolution...),
		getLLMRegion(accountId, provider, agentName, appendAgentName, resolution...),
		getLLMAccessKey(accountId, provider, agentName, appendAgentName, resolution...),
		getLLMSecretKey(accountId, provider, agentName, appendAgentName, resolution...),
		getLLMSessionToken(accountId, provider, agentName, appendAgentName, resolution...),
		auth.AuthType,
		auth.OAuth.TokenURL,
		auth.OAuth.ClientID,
		auth.OAuth.ClientSecret,
		auth.OAuth.Scope,
		strings.Join(extraHeaders, ","),
	)
}

// ProviderCustom is the generic provider for anything speaking
// OpenAI's Chat Completions API at a caller-supplied base URL — the de-facto
// standard that gateways (OpenRouter, LiteLLM, Portkey), hosted vendors (Groq,
// Together, DeepSeek) and self-hosted runtimes (vLLM, Ollama, TGI) all
// implement. One provider covers all of them; adding a case per vendor would
// only duplicate this client under different names.
//
// Kept as a constant because the same string has to agree across llm-server,
// the api-server integration config and the app's provider dropdown.
const ProviderCustom = "custom"

func GetLLMModel(provider string, modelName string, agentName string, appendAgentName bool, accountId string, resolution ...*LLMConfigResolution) (llms.Model, error) {
	slog.Debug("GetLLMModel called", "provider", provider, "modelName", modelName, "agentName", agentName, "appendAgentName", appendAgentName, "accountId", accountId)

	var res *LLMConfigResolution
	if len(resolution) > 0 {
		res = resolution[0]
	}

	credsFp := resolveCredsFingerprint(accountId, provider, agentName, appendAgentName, res)
	cacheKey := fmt.Sprintf("%s:%s:%s:%s", provider, modelName, accountId, credsFp)
	llmClientCacheMutex.RLock()
	entry, found := llmClientCache[cacheKey]
	llmClientCacheMutex.RUnlock()
	if found && time.Since(entry.ts) < llmClientCacheTTL {
		slog.Debug("Reusing cached LLM client", "cacheKey", cacheKey)
		return entry.model, nil
	}

	llmClientCacheMutex.Lock()
	defer llmClientCacheMutex.Unlock()
	// Double-check after acquiring lock
	if entry, found := llmClientCache[cacheKey]; found && time.Since(entry.ts) < llmClientCacheTTL {
		return entry.model, nil
	}

	var model llms.Model
	var err error

	switch provider {
	case "openai":
		slog.Debug("Routing to OpenAI LLM provider")
		model, err = getOpenAILLM(provider, modelName, agentName, appendAgentName, accountId, res)
	case ProviderCustom:
		// Anything speaking the OpenAI Chat Completions wire format at a
		// caller-supplied base URL: OpenRouter, vLLM, Ollama, Groq, Together,
		// DeepSeek, LiteLLM and friends. Same client as "openai" — the only
		// difference is that the endpoint is required rather than defaulted,
		// which getCustomLLM enforces.
		slog.Debug("Routing to OpenAI-compatible LLM provider")
		model, err = getCustomLLM(provider, modelName, agentName, appendAgentName, accountId, res)
	case "bedrock":
		slog.Debug("Routing to Bedrock LLM provider")
		model, err = getBedrockLLM(provider, modelName, agentName, appendAgentName, accountId, res)
	case "sagemaker":
		slog.Debug("Routing to SageMaker LLM provider")
		model, err = getSageMakerLLM(provider, agentName, appendAgentName, accountId, res)
	case "huggingface":
		slog.Debug("Routing to Hugging Face LLM provider")
		model, err = getHuggingFaceLLM(provider, modelName, agentName, appendAgentName, accountId, res)
	case "azure":
		slog.Debug("Routing to Azure AI LLM provider")
		model, err = getAzureAILLM(provider, modelName, agentName, appendAgentName, accountId, res)
	case "googleai":
		slog.Debug("Routing to Google AI LLM provider")
		model, err = getGoogleAILLM(provider, modelName, agentName, appendAgentName, accountId, res)
	case "vertexai":
		slog.Debug("Routing to Vertex AI LLM provider")
		model, err = getVertexAILLM(provider, modelName, agentName, appendAgentName, accountId, res)
	case "vertexai_endpoint":
		slog.Debug("Routing to Vertex AI Endpoint LLM provider")
		model, err = getVertexAIEndpointLLM(provider, modelName, agentName, appendAgentName, accountId, res)
	case "anthropic":
		slog.Debug("Routing to Anthropic LLM provider")
		model, err = getAnthropicLLM(provider, modelName, agentName, appendAgentName, accountId, res)
	default:
		slog.Error("Unknown LLM provider", "provider", provider)
		return nil, errors.New("llm model not found - " + provider)
	}

	if err == nil && model != nil {
		// Layer 1 (innermost — runs LAST before the real model): optional EE
		// PII/PHI scrub + rehydrate. ee/scrubbing installs the decorator via
		// init() against the LLMModelDecorator hook — see ee_registry.go. nil
		// = no decoration, OSS behavior, zero cost.
		if LLMModelDecorator != nil {
			model = LLMModelDecorator(model)
		}
		// Layer 2 (outermost — runs FIRST on outbound payload): OSS egress
		// filter. Master switch gates the entire egressfilter subsystem. When
		// off, we skip the WrapModel call entirely — no decorator, no metric
		// emission, no payload serialization, zero overhead. Per-detector
		// flags below only matter once the master is on. See
		// docs/pii-secret-scrubbing.md.
		//
		// Wrapping order is deliberate: egressfilter inspects the raw outbound
		// text BEFORE the scrub decorator rewrites secrets to [REDACTED_*],
		// so its credential gate sees what the caller actually produced.
		if config.Config.LlmServerEgressFilterEnabled {
			model = egressfilter.WrapModel(
				model,
				provider,
				modelName,
				config.Config.LlmServerEgressFilterSecretsEnabled,
				egressfilter.ParseMode(config.Config.LlmServerEgressFilterSecretsMode),
			)
		}
		llmClientCache[cacheKey] = llmCacheEntry{model: model, ts: time.Now()}
	}

	return model, err
}

// ForwardedLLMConfig is the resolved, decrypted LLM configuration that
// llm-server forwards to the stateless code-analysis service per request, so a
// tenant's own LLM integration (DB-backed, encrypted) is honored instead of the
// pod's global secret-env fallback. It deliberately carries only what the
// code-analysis llm.Client consumes.
type ForwardedLLMConfig struct {
	Provider    string `json:"provider,omitempty"`
	Model       string `json:"model,omitempty"`
	ApiKey      string `json:"api_key,omitempty"`
	ApiEndpoint string `json:"endpoint,omitempty"`
	ApiVersion  string `json:"api_version,omitempty"`
	ApiType     string `json:"api_type,omitempty"`
	Region      string `json:"region,omitempty"`
	// AccessKey/SecretKey/SessionToken are the AWS static credentials for
	// Bedrock, whose "API key" is a SigV4 credential triple rather than a single
	// token. Without them a forwarded provider=bedrock reaches the code-analysis
	// pod with nothing to authenticate with, so its AWS SDK falls through to the
	// default credential chain and ends at IMDS — which exists on EKS but not on
	// GKE, where it dead-ends in a 169.254.169.254 dial timeout. They are
	// resolved by the same layered config as every other field here, so a
	// tenant's own Bedrock integration is honored exactly like a keyed provider.
	// Plaintext, like ApiKey: MUST NOT be logged.
	AccessKey    string `json:"access_key,omitempty"`
	SecretKey    string `json:"secret_key,omitempty"`
	SessionToken string `json:"session_token,omitempty"`
	// Tiers carries the ModelTier resolution (reasoning/retrieval/summary) for
	// this account+agent so the workspace can run its internal roles on
	// category-appropriate models (fixer/router on retrieval, review on
	// summary). Only tiers that resolve to a model different from Model, on the
	// same provider, are included — credentials are shared with the run model.
	Tiers map[string]string `json:"tiers,omitempty"`
}

// ResolveLLMConfigForForwarding resolves the full, decrypted LLM config for the
// given account/agent in one call, reusing the canonical resolvers (which apply
// the DB-beats-ENV precedence and decrypt secrets). It returns nil (no error)
// only when no provider resolves at all, in which case the caller omits the
// block and the pod falls back to its global LLM_* secret env. The returned
// ApiKey is plaintext and MUST NOT be logged.
//
// A missing API key is NOT a reason to skip forwarding. Keyless providers are
// legitimate — Bedrock authenticates through the AWS credential chain, not an
// API key — and bailing on an empty key also threw away the provider+model
// llm-server itself resolved and runs on. The pod then fell back to its
// startup env, which on a deployment whose secret sets no LLM_* keys leaves
// code-analysis on its built-in default provider: one nobody selected, with no
// credentials, failing at client construction. Forward what Nubi resolved and
// let the pod fail on the real problem instead.
func ResolveLLMConfigForForwarding(ctx *security.RequestContext, accountId, agentName, conversationId string) (*ForwardedLLMConfig, error) {
	// Fail-safe: without a tenant/account scope there is nothing tenant-specific
	// to forward (and we must never run an unscoped tenant lookup). Skip
	// forwarding so the pod uses its global LLM_* fallback.
	if accountId == "" {
		return nil, nil
	}
	res, err := ResolveLLMConfig(ctx, accountId, agentName, conversationId)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	provider := res.Provider
	if provider == "" {
		return nil, nil
	}
	appendAgentName := agentName != ""
	// Resolve the AWS static credentials as a unit. Both halves must be present
	// for the pair to mean anything; a session token is optional and only
	// meaningful alongside them (it belongs to temporary STS credentials).
	accessKey := getLLMAccessKey(accountId, provider, agentName, appendAgentName, res)
	secretKey := getLLMSecretKey(accountId, provider, agentName, appendAgentName, res)
	sessionToken := getLLMSessionToken(accountId, provider, agentName, appendAgentName, res)
	if accessKey == "" || secretKey == "" {
		accessKey, secretKey, sessionToken = "", "", ""
	}
	// With nothing configured explicitly, hand the sandbox the same identity
	// llm-server itself uses for Bedrock. See ambientBedrockCredentials.
	if accessKey == "" && strings.EqualFold(provider, "bedrock") {
		accessKey, secretKey, sessionToken = ambientBedrockCredentials(
			getLLMRegion(accountId, provider, agentName, appendAgentName, res))
	}
	fwd := &ForwardedLLMConfig{
		Provider:    provider,
		Model:       res.Model,
		ApiKey:      getLLMApiKey(accountId, provider, agentName, appendAgentName, res),
		ApiEndpoint: getLLMApiEndpoint(accountId, provider, agentName, appendAgentName, res),
		ApiVersion:  getLLMApiVersion(accountId, provider, agentName, appendAgentName, res),
		ApiType:     getLLMApiType(accountId, provider, agentName, appendAgentName, res),
		Region:      getLLMRegion(accountId, provider, agentName, appendAgentName, res),
		// The AWS credential triple is the Bedrock equivalent of ApiKey. Only
		// forwarded as a complete pair: the AWS SDK treats a half-set static
		// provider as a hard error rather than falling through to the next
		// source, so a partial forward would break the pod's own credential
		// chain (node role / IRSA on EKS) instead of merely not helping it.
		AccessKey:    accessKey,
		SecretKey:    secretKey,
		SessionToken: sessionToken,
	}

	// Resolve the model tiers through the same layered config so the workspace
	// can tier its internal roles with no new config surface. A tier is only
	// forwarded when it resolves to a different model on the SAME provider —
	// the forwarded credentials belong to the run model's provider. Best-effort:
	// without a usable context there is no safe way to tag the tier, so skip.
	if ctx == nil {
		return fwd, nil
	}
	goCtx := ctx.GetContext()
	if goCtx == nil {
		goCtx = context.Background()
	}
	for _, tier := range []ModelTier{ModelTierReasoning, ModelTierRetrieval, ModelTierSummary} {
		tierCtx := security.NewRequestContext(
			context.WithValue(goCtx, ContextKeyModelTier, tier),
			ctx.GetSecurityContext(),
			ctx.GetLogger(),
			ctx.GetTracer(),
			ctx.GetMeter(),
		)
		tierRes, terr := ResolveLLMConfig(tierCtx, accountId, agentName, conversationId)
		if terr != nil || tierRes == nil {
			continue // tiering is best-effort; the run model always works
		}
		if tierRes.Model == "" || tierRes.Model == fwd.Model || tierRes.Provider != provider {
			continue
		}
		if fwd.Tiers == nil {
			fwd.Tiers = map[string]string{}
		}
		fwd.Tiers[string(tier)] = tierRes.Model
	}
	return fwd, nil
}

func InvalidateLLMClientCache(accountId string) {
	slog.Info("Invalidating LLM client cache for account", "accountId", accountId)
	llmClientCacheMutex.Lock()
	defer llmClientCacheMutex.Unlock()
	suffix := ":" + accountId
	for key := range llmClientCache {
		if strings.HasSuffix(key, suffix) {
			delete(llmClientCache, key)
		}
	}
}

func InvalidateAllLLMClientCache() {
	slog.Info("Invalidating all LLM client cache")
	llmClientCacheMutex.Lock()
	defer llmClientCacheMutex.Unlock()
	llmClientCache = make(map[string]llmCacheEntry)
}

func GetLLMModelName(ctx *security.RequestContext, accountId, provider string, agentName string, appendAgentName bool, conversationId string, resolution ...*LLMConfigResolution) string {
	if len(resolution) > 0 && resolution[0] != nil {
		return resolution[0].Model
	}

	res, err := ResolveLLMConfig(ctx, accountId, agentName, conversationId)
	if err != nil {
		return config.Config.LlmModel
	}
	return res.Model
}

func getLLMModelAdapterName(agentName string) string {
	modelAdapterId := fmt.Sprintf(llmModelAdapterFormat, agentName)
	modelAdapter := config.Config.GetString(modelAdapterId, "")
	return modelAdapter
}

func checkLLMModelAdapterSupport(agentName string) bool {
	modelAdapterSupportCheck := fmt.Sprintf(llmModelAdapterSupportFormat, agentName)
	modelAdapterSupport := config.Config.GetBool(modelAdapterSupportCheck, false)
	return modelAdapterSupport
}

func GetLLMModelIntConfig(accountId, provider, model, configName string, defaultValue int) int {
	provider = strings.ToLower(provider)
	model = normalizeModel(model)
	// Key pattern: configName_provider_model (e.g. llm_concurrency_limit_openai_gpt-4o)
	modelSpecificKey := fmt.Sprintf("%s_%s_%s", configName, provider, model)

	// Layer 1: Check DB Global config for model-specific override
	if accountId != "" {
		if dbConfig, err := getLLMIntegrationConfig(nil, accountId); err == nil && dbConfig != nil {
			if val, ok := dbConfig[modelSpecificKey]; ok && val != "" {
				if intVal, err := strconv.Atoi(val); err == nil {
					return intVal
				}
			}
		}
	}

	// Layer 2: Check ENV for model-specific override
	if envVal := config.Config.GetInt(modelSpecificKey, 0); envVal > 0 {
		return envVal
	}

	// Layer 3: Return global default
	return config.Config.GetInt(configName, defaultValue)
}

func GetLLMProvider(ctx *security.RequestContext, accountId, agentName string, appendAgentName bool, conversationId string, resolution ...*LLMConfigResolution) string {
	if len(resolution) > 0 && resolution[0] != nil {
		return resolution[0].Provider
	}

	res, err := ResolveLLMConfig(ctx, accountId, agentName, conversationId)
	if err != nil {
		return config.Config.LlmProvider
	}
	return res.Provider
}

type conversationOverrideEntry struct {
	provider      string
	model         string
	tierOverrides ConversationTierOverrides
	configSource  string
	ts            time.Time
}

var (
	conversationOverrideCache      = make(map[string]conversationOverrideEntry)
	conversationOverrideCacheMutex sync.RWMutex
	conversationOverrideCacheTTL   = 5 * time.Minute
)

// Returns the conversation's sticky LLM selections: blanket (provider, model),
// per-tier overrides, and the pinned config source. All zero ⇒ no override; fall
// through to lower layers. The config source is independent of the other three —
// a conversation can pin a slot without pinning a provider/model, and vice versa.
func GetConversationOverride(conversationId string) (string, string, ConversationTierOverrides, string, error) {
	conversationOverrideCacheMutex.RLock()
	entry, found := conversationOverrideCache[conversationId]
	conversationOverrideCacheMutex.RUnlock()

	if found && time.Since(entry.ts) < conversationOverrideCacheTTL {
		return entry.provider, entry.model, entry.tierOverrides, entry.configSource, nil
	}

	// Fail closed if the conversation DAO is unavailable (e.g. DB not reachable):
	// return an explicit error rather than silently reporting "no override" (which
	// could mask a conversation-level model/tier restriction) — and never deref a
	// nil DAO, which previously panicked the whole request. Callers already treat
	// a non-nil error as "skip the override and fall through".
	dao := GetConversationDao()
	if dao == nil {
		return "", "", ConversationTierOverrides{}, "", fmt.Errorf("conversation DAO is unavailable")
	}

	conv, err := dao.GetConversation(conversationId)
	if err != nil {
		return "", "", ConversationTierOverrides{}, "", err
	}

	provider := ""
	model := ""
	configSource := ""
	if conv.LlmProvider != nil {
		provider = *conv.LlmProvider
	}
	if conv.LlmModel != nil {
		model = *conv.LlmModel
	}
	if conv.LlmConfigSource != nil {
		configSource = *conv.LlmConfigSource
	}
	var tierOverrides ConversationTierOverrides
	if conv.LlmTierOverrides != nil {
		tierOverrides = *conv.LlmTierOverrides
	}

	conversationOverrideCacheMutex.Lock()
	conversationOverrideCache[conversationId] = conversationOverrideEntry{
		provider:      provider,
		model:         model,
		tierOverrides: tierOverrides,
		configSource:  configSource,
		ts:            time.Now(),
	}
	conversationOverrideCacheMutex.Unlock()

	return provider, model, tierOverrides, configSource, nil
}

func InvalidateConversationOverrideCache(conversationId string) {
	conversationOverrideCacheMutex.Lock()
	delete(conversationOverrideCache, conversationId)
	conversationOverrideCacheMutex.Unlock()
}

func getLLMFallbackModelName(accountId, agentName string, tier ModelTier, appendAgentName bool, resolution ...*LLMConfigResolution) string {
	var dbConfig map[string]string
	if len(resolution) > 0 && resolution[0] != nil {
		dbConfig = resolution[0].dbConfig
	}

	// Layering: ENV layers first (least specific to most specific), then DB
	// layers on top. DB always beats ENV — see package-level docstring.

	// L1 ENV-global
	modelName := config.Config.LlmModelFallbacks

	// L2 ENV-tier
	if tier != "" {
		tierKey := fmt.Sprintf(llmTierModelFallbackFormat, string(tier))
		if v := config.Config.GetString(tierKey, ""); v != "" {
			modelName = v
		}
	}

	// L3 ENV-agent
	if appendAgentName && agentName != "" {
		fallbackKey := fmt.Sprintf(llmModelFallbackFormat, agentName)
		if agentEnvVal := config.Config.GetString(fallbackKey, ""); agentEnvVal != "" {
			modelName = agentEnvVal
		}
	}

	// L4 DB-global
	if accountId != "" {
		if dbConfig, err := getLLMIntegrationConfig(nil, accountId, dbConfig); err == nil && dbConfig != nil {
			if val, ok := dbConfig["llm_model_fallbacks"]; ok && val != "" {
				modelName = val
			}
		}
	}

	// L5 DB-tier
	if tier != "" && accountId != "" {
		if dbConfig, err := getLLMIntegrationConfig(nil, accountId, dbConfig); err == nil && dbConfig != nil {
			tierKey := fmt.Sprintf(llmTierModelFallbackFormat, string(tier))
			if val, ok := dbConfig[tierKey]; ok && val != "" {
				modelName = val
			}
		}
	}

	// L6 DB-agent (highest priority)
	if accountId != "" && appendAgentName && agentName != "" {
		if dbConfig, err := getLLMIntegrationConfig(nil, accountId, dbConfig); err == nil && dbConfig != nil {
			fallbackKey := fmt.Sprintf(llmModelFallbackFormat, agentName)
			if val, ok := dbConfig[fallbackKey]; ok && val != "" {
				modelName = val
			}
		}
	}

	return modelName
}

// readENVTierCredential returns the ENV-tier value for the given credential key
// format (e.g. llmTierApiKeyFormat), but only when the tier's own ENV provider
// matches `provider`. That match guard prevents a Bedrock-tier API key from
// leaking into an Anthropic call when the tier slot is configured but the
// resolved provider came from a different (more-specific) layer.
func readENVTierCredential(tier ModelTier, provider, envKeyFormat string) string {
	if tier == "" {
		return ""
	}
	tierProviderKey := fmt.Sprintf(llmTierProviderFormat, string(tier))
	envTierProvider := config.Config.GetString(tierProviderKey, "")
	if envTierProvider == "" || envTierProvider != provider {
		return ""
	}
	return config.Config.GetString(fmt.Sprintf(envKeyFormat, string(tier)), "")
}

// readDBTierCredential is the DB-tier equivalent of readENVTierCredential.
// Only fires when the tier's DB provider matches `provider`.
func readDBTierCredential(tier ModelTier, provider, dbKeyFormat string, dbConfig map[string]string) string {
	if tier == "" || dbConfig == nil {
		return ""
	}
	tierProviderKey := fmt.Sprintf(llmTierProviderFormat, string(tier))
	dbTierProvider, ok := dbConfig[tierProviderKey]
	if !ok || dbTierProvider == "" || dbTierProvider != provider {
		return ""
	}
	return dbConfig[fmt.Sprintf(dbKeyFormat, string(tier))]
}

func getLLMApiKey(accountId, provider, agentName string, appendAgentName bool, resolution ...*LLMConfigResolution) string {
	slog.Debug("Getting LLM API key", "accountId", accountId, "provider", provider, "agentName", agentName, "appendAgentName", appendAgentName)

	if len(resolution) > 0 && resolution[0] != nil && resolution[0].PinnedConfigSource != "" {
		return resolution[0].PinnedApiKey
	}

	var dbConfig map[string]string
	var tier ModelTier
	if len(resolution) > 0 && resolution[0] != nil {
		dbConfig = resolution[0].dbConfig
		tier = resolution[0].Tier
	}

	// Layering: ENV layers first (least specific to most specific), then the
	// DB block on top. DB always beats ENV — see package-level docstring.

	apiKey := ""
	configSource := "none"

	// L1 ENV-global (only if provider matches)
	if config.Config.LlmProvider == provider {
		apiKey = config.Config.LlmProviderApiKey
		if apiKey != "" {
			configSource = "ENV-global"
		}
		slog.Debug("Using global ENV API key (provider matches)", "provider", provider, "hasKey", apiKey != "")
	}

	// L2 ENV-tier (only fires when the tier's ENV provider matches)
	if v := readENVTierCredential(tier, provider, llmTierApiKeyFormat); v != "" {
		apiKey = v
		configSource = "ENV-tier-specific"
		slog.Debug("Found API key from tier ENV config", "tier", string(tier), "hasKey", true)
	}

	// L3 ENV-agent (check provider match against the agent's own ENV provider)
	if appendAgentName && agentName != "" {
		providerKey := fmt.Sprintf(llmProviderFormat, agentName)
		if envProviderVal := config.Config.GetString(providerKey, ""); envProviderVal != "" && envProviderVal == provider {
			apiKeyKey := fmt.Sprintf(llmProviderApiKeyFormat, agentName)
			if agentEnvVal := config.Config.GetString(apiKeyKey, ""); agentEnvVal != "" {
				apiKey = agentEnvVal
				configSource = "ENV-agent-specific"
				slog.Debug("Found API key from agent ENV config", "apiKeyKey", apiKeyKey, "hasKey", agentEnvVal != "")
			}
		}
	}

	// L4 DB-global (check provider match — overrides any ENV layer above)
	if accountId != "" {
		if dbConfig, err := getLLMIntegrationConfig(nil, accountId, dbConfig); err == nil && dbConfig != nil {
			if val, ok := dbConfig["llm_provider"]; ok && val != "" && val == provider {
				if val, ok := dbConfig["llm_provider_api_key"]; ok && val != "" {
					apiKey = val
					configSource = "DB-global"
					slog.Debug("Found global API key from DB config", "hasKey", val != "")
				}
			}
		} else if err != nil {
			slog.Debug("Failed to get LLM integration config from DB", "error", err)
		}
	}

	// L5 DB-tier (only fires when the tier's DB provider matches)
	if v := readDBTierCredential(tier, provider, llmTierApiKeyFormat, dbConfig); v != "" {
		apiKey = v
		configSource = "DB-tier-specific"
		slog.Debug("Found tier-specific API key from DB config", "tier", string(tier), "hasKey", true)
	}

	// L6 DB-agent (highest priority — check provider match against DB-agent provider)
	if accountId != "" && appendAgentName && agentName != "" {
		if dbConfig, err := getLLMIntegrationConfig(nil, accountId, dbConfig); err == nil && dbConfig != nil {
			providerKey := fmt.Sprintf(llmProviderFormat, agentName)
			if val, ok := dbConfig[providerKey]; ok && val != "" && val == provider {
				apiKeyKey := fmt.Sprintf(llmProviderApiKeyFormat, agentName)
				if val, ok := dbConfig[apiKeyKey]; ok && val != "" {
					apiKey = val
					configSource = "DB-agent-specific"
					slog.Debug("Found agent-specific API key from DB config (highest priority)", "apiKeyKey", apiKeyKey, "hasKey", val != "")
				}
			}
		}
	}

	slog.Debug("LLM API key configuration selected", "source", configSource, "hasKey", apiKey != "", "provider", provider, "agentName", agentName)
	if configSource == "none" {
		// No API key was located in any layer for the resolved provider. Letting this
		// through produces an opaque "401 invalid x-api-key" from the SDK; a Warn here
		// surfaces the misconfig at the source.
		slog.Warn("getLLMApiKey: no API key configured for provider — call will likely 401",
			"accountId", accountId,
			"provider", provider,
			"agentName", agentName)
	}
	return apiKey
}

func getLLMApiEndpoint(accountId, provider, agentName string, appendAgentName bool, resolution ...*LLMConfigResolution) string {
	slog.Debug("Getting LLM API endpoint", "accountId", accountId, "provider", provider, "agentName", agentName, "appendAgentName", appendAgentName)

	// Pinned-source short-circuit: when the request pinned LlmConfigSource,
	// ResolveLLMConfig already read every credential from exactly that slot
	// and populated resolution[0].Pinned*. Skip the layered walk so we can't
	// silently drift to a different endpoint.
	if len(resolution) > 0 && resolution[0] != nil && resolution[0].PinnedConfigSource != "" {
		return resolution[0].PinnedEndpoint
	}

	var dbConfig map[string]string
	var tier ModelTier
	if len(resolution) > 0 && resolution[0] != nil {
		dbConfig = resolution[0].dbConfig
		tier = resolution[0].Tier
	}

	// Layering: ENV first (least specific to most specific), then DB on top.
	// DB always beats ENV — see package-level docstring.

	apiEndpoint := ""
	configSource := "none"

	// L1 ENV-global (only if provider matches)
	if config.Config.LlmProvider == provider {
		apiEndpoint = config.Config.LlmProviderApiEndpoint
		if apiEndpoint != "" {
			configSource = "ENV-global"
		}
		slog.Debug("Using global ENV API endpoint (provider matches)", "provider", provider, "endpoint", apiEndpoint)
	}

	// L2 ENV-tier (only fires when the tier's ENV provider matches)
	if v := readENVTierCredential(tier, provider, llmTierApiEndpointFormat); v != "" {
		apiEndpoint = v
		configSource = "ENV-tier-specific"
		slog.Debug("Found API endpoint from tier ENV config", "tier", string(tier), "endpoint", v)
	}

	// L3 ENV-agent
	if appendAgentName && agentName != "" {
		providerKey := fmt.Sprintf(llmProviderFormat, agentName)
		if envProviderVal := config.Config.GetString(providerKey, ""); envProviderVal != "" && envProviderVal == provider {
			apiEndpointKey := fmt.Sprintf(llmProviderApiEndpointFormat, agentName)
			if agentEnvVal := config.Config.GetString(apiEndpointKey, ""); agentEnvVal != "" {
				apiEndpoint = agentEnvVal
				configSource = "ENV-agent-specific"
				slog.Debug("Found API endpoint from agent ENV config", "apiEndpointKey", apiEndpointKey, "endpoint", agentEnvVal)
			}
		}
	}

	// L4 DB-global
	if accountId != "" {
		if dbConfig, err := getLLMIntegrationConfig(nil, accountId, dbConfig); err == nil && dbConfig != nil {
			if val, ok := dbConfig["llm_provider"]; ok && val != "" && val == provider {
				if val, ok := dbConfig["llm_provider_api_endpoint"]; ok && val != "" {
					apiEndpoint = val
					configSource = "DB-global"
					slog.Debug("Found global API endpoint from DB config", "endpoint", val)
				}
			}
		} else if err != nil {
			slog.Debug("Failed to get LLM integration config from DB", "error", err)
		}
	}

	// L5 DB-tier (only fires when the tier's DB provider matches)
	if v := readDBTierCredential(tier, provider, llmTierApiEndpointFormat, dbConfig); v != "" {
		apiEndpoint = v
		configSource = "DB-tier-specific"
		slog.Debug("Found tier-specific API endpoint from DB config", "tier", string(tier), "endpoint", v)
	}

	// L6 DB-agent (highest priority)
	if accountId != "" && appendAgentName && agentName != "" {
		if dbConfig, err := getLLMIntegrationConfig(nil, accountId, dbConfig); err == nil && dbConfig != nil {
			providerKey := fmt.Sprintf(llmProviderFormat, agentName)
			if val, ok := dbConfig[providerKey]; ok && val != "" && val == provider {
				apiEndpointKey := fmt.Sprintf(llmProviderApiEndpointFormat, agentName)
				if val, ok := dbConfig[apiEndpointKey]; ok && val != "" {
					apiEndpoint = val
					configSource = "DB-agent-specific"
					slog.Debug("Found agent-specific API endpoint from DB config (highest priority)", "apiEndpointKey", apiEndpointKey, "endpoint", val)
				}
			}
		}
	}

	if apiEndpoint != "" {
		slog.Debug("LLM API endpoint configuration selected", "source", configSource, "endpoint", apiEndpoint, "provider", provider, "agentName", agentName)
	}
	return apiEndpoint
}

func getLLMApiVersion(accountId, provider, agentName string, appendAgentName bool, resolution ...*LLMConfigResolution) string {
	slog.Debug("Getting LLM API version", "accountId", accountId, "provider", provider, "agentName", agentName, "appendAgentName", appendAgentName)

	if len(resolution) > 0 && resolution[0] != nil && resolution[0].PinnedConfigSource != "" {
		return resolution[0].PinnedApiVersion
	}

	var dbConfig map[string]string
	var tier ModelTier
	if len(resolution) > 0 && resolution[0] != nil {
		dbConfig = resolution[0].dbConfig
		tier = resolution[0].Tier
	}

	// ENV first, then DB on top. DB always beats ENV — see package-level docstring.

	apiVersion := ""

	// L1 ENV-global
	if config.Config.LlmProvider == provider {
		apiVersion = config.Config.LlmProviderApiVersion
		slog.Debug("Using global ENV API version (provider matches)", "provider", provider, "version", apiVersion)
	}

	// L2 ENV-tier (only fires when the tier's ENV provider matches)
	if v := readENVTierCredential(tier, provider, llmTierApiVersionFormat); v != "" {
		apiVersion = v
		slog.Debug("Found API version from tier ENV config", "tier", string(tier), "version", v)
	}

	// L3 ENV-agent
	if appendAgentName && agentName != "" {
		providerKey := fmt.Sprintf(llmProviderFormat, agentName)
		if envProviderVal := config.Config.GetString(providerKey, ""); envProviderVal != "" && envProviderVal == provider {
			apiVersionKey := fmt.Sprintf(llmProviderApiVersionFormat, agentName)
			if agentEnvVal := config.Config.GetString(apiVersionKey, ""); agentEnvVal != "" {
				apiVersion = agentEnvVal
				slog.Debug("Found API version from agent ENV config", "apiVersionKey", apiVersionKey, "version", agentEnvVal)
			}
		}
	}

	// L4 DB-global
	if accountId != "" {
		if dbConfig, err := getLLMIntegrationConfig(nil, accountId, dbConfig); err == nil && dbConfig != nil {
			if val, ok := dbConfig["llm_provider"]; ok && val != "" && val == provider {
				if val, ok := dbConfig["llm_provider_api_version"]; ok && val != "" {
					apiVersion = val
					slog.Debug("Found global API version from DB config", "version", val)
				}
			}
		} else if err != nil {
			slog.Debug("Failed to get LLM integration config from DB", "error", err)
		}
	}

	// L5 DB-tier (only fires when the tier's DB provider matches)
	if v := readDBTierCredential(tier, provider, llmTierApiVersionFormat, dbConfig); v != "" {
		apiVersion = v
		slog.Debug("Found tier-specific API version from DB config", "tier", string(tier), "version", v)
	}

	// L6 DB-agent (highest priority)
	if accountId != "" && appendAgentName && agentName != "" {
		if dbConfig, err := getLLMIntegrationConfig(nil, accountId, dbConfig); err == nil && dbConfig != nil {
			providerKey := fmt.Sprintf(llmProviderFormat, agentName)
			if val, ok := dbConfig[providerKey]; ok && val != "" && val == provider {
				apiVersionKey := fmt.Sprintf(llmProviderApiVersionFormat, agentName)
				if val, ok := dbConfig[apiVersionKey]; ok && val != "" {
					apiVersion = val
					slog.Debug("Found agent-specific API version from DB config (highest priority)", "apiVersionKey", apiVersionKey, "version", val)
				}
			}
		}
	}

	slog.Debug("Final API version selected", "version", apiVersion)
	return apiVersion
}

func getLLMApiType(accountId, provider, agentName string, appendAgentName bool, resolution ...*LLMConfigResolution) string {
	slog.Debug("Getting LLM API type", "accountId", accountId, "provider", provider, "agentName", agentName, "appendAgentName", appendAgentName)

	if len(resolution) > 0 && resolution[0] != nil && resolution[0].PinnedConfigSource != "" {
		return resolution[0].PinnedApiType
	}

	var dbConfig map[string]string
	var tier ModelTier
	if len(resolution) > 0 && resolution[0] != nil {
		dbConfig = resolution[0].dbConfig
		tier = resolution[0].Tier
	}

	// ENV first, then DB on top. DB always beats ENV — see package-level docstring.

	apiType := ""

	// L1 ENV-global
	if config.Config.LlmProvider == provider {
		apiType = config.Config.LlmProviderApiType
		slog.Debug("Using global ENV API type (provider matches)", "provider", provider, "type", apiType)
	}

	// L2 ENV-tier (only fires when the tier's ENV provider matches)
	if v := readENVTierCredential(tier, provider, llmTierApiTypeFormat); v != "" {
		apiType = v
		slog.Debug("Found API type from tier ENV config", "tier", string(tier), "type", v)
	}

	// L3 ENV-agent
	if appendAgentName && agentName != "" {
		providerKey := fmt.Sprintf(llmProviderFormat, agentName)
		if envProviderVal := config.Config.GetString(providerKey, ""); envProviderVal != "" && envProviderVal == provider {
			apiTypeKey := fmt.Sprintf(llmProviderApiTypeFormat, agentName)
			if agentEnvVal := config.Config.GetString(apiTypeKey, ""); agentEnvVal != "" {
				apiType = agentEnvVal
				slog.Debug("Found API type from agent ENV config", "apiTypeKey", apiTypeKey, "type", agentEnvVal)
			}
		}
	}

	// L4 DB-global
	if accountId != "" {
		if dbConfig, err := getLLMIntegrationConfig(nil, accountId, dbConfig); err == nil && dbConfig != nil {
			if val, ok := dbConfig["llm_provider"]; ok && val != "" && val == provider {
				if val, ok := dbConfig["llm_provider_api_type"]; ok && val != "" {
					apiType = val
					slog.Debug("Found global API type from DB config", "type", val)
				}
			}
		} else if err != nil {
			slog.Debug("Failed to get LLM integration config from DB", "error", err)
		}
	}

	// L5 DB-tier (only fires when the tier's DB provider matches)
	if v := readDBTierCredential(tier, provider, llmTierApiTypeFormat, dbConfig); v != "" {
		apiType = v
		slog.Debug("Found tier-specific API type from DB config", "tier", string(tier), "type", v)
	}

	// L6 DB-agent (highest priority)
	if accountId != "" && appendAgentName && agentName != "" {
		if dbConfig, err := getLLMIntegrationConfig(nil, accountId, dbConfig); err == nil && dbConfig != nil {
			providerKey := fmt.Sprintf(llmProviderFormat, agentName)
			if val, ok := dbConfig[providerKey]; ok && val != "" && val == provider {
				apiTypeKey := fmt.Sprintf(llmProviderApiTypeFormat, agentName)
				if val, ok := dbConfig[apiTypeKey]; ok && val != "" {
					apiType = val
					slog.Debug("Found agent-specific API type from DB config (highest priority)", "apiTypeKey", apiTypeKey, "type", val)
				}
			}
		}
	}

	slog.Debug("Final API type selected", "type", apiType)
	return apiType
}

func getLLMRegion(accountId, provider, agentName string, appendAgentName bool, resolution ...*LLMConfigResolution) string {
	slog.Debug("Getting LLM region", "accountId", accountId, "provider", provider, "agentName", agentName, "appendAgentName", appendAgentName)

	if len(resolution) > 0 && resolution[0] != nil && resolution[0].PinnedConfigSource != "" {
		return resolution[0].PinnedRegion
	}

	var dbConfig map[string]string
	var tier ModelTier
	if len(resolution) > 0 && resolution[0] != nil {
		dbConfig = resolution[0].dbConfig
		tier = resolution[0].Tier
	}

	// ENV first, then DB on top. DB always beats ENV — see package-level docstring.

	region := ""
	configSource := "none"

	// L1 ENV-global
	if config.Config.LlmProvider == provider {
		region = config.Config.LlmProviderRegion
		if region != "" {
			configSource = "ENV-global"
		}
		slog.Debug("Using global ENV region (provider matches)", "provider", provider, "region", region)
	}

	// L2 ENV-tier (only fires when the tier's ENV provider matches)
	if v := readENVTierCredential(tier, provider, llmTierRegionFormat); v != "" {
		region = v
		configSource = "ENV-tier-specific"
		slog.Debug("Found region from tier ENV config", "tier", string(tier), "region", v)
	}

	// L3 ENV-agent
	if appendAgentName && agentName != "" {
		providerKey := fmt.Sprintf(llmProviderFormat, agentName)
		if envProviderVal := config.Config.GetString(providerKey, ""); envProviderVal != "" && envProviderVal == provider {
			regionKey := fmt.Sprintf(llmProviderRegionFormat, agentName)
			if agentEnvVal := config.Config.GetString(regionKey, ""); agentEnvVal != "" {
				region = agentEnvVal
				configSource = "ENV-agent-specific"
				slog.Debug("Found region from agent ENV config", "regionKey", regionKey, "region", agentEnvVal)
			}
		}
	}

	// L4 DB-global
	if accountId != "" {
		if dbConfig, err := getLLMIntegrationConfig(nil, accountId, dbConfig); err == nil && dbConfig != nil {
			if val, ok := dbConfig["llm_provider"]; ok && val != "" && val == provider {
				if val, ok := dbConfig["llm_provider_region"]; ok && val != "" {
					region = val
					configSource = "DB-global"
					slog.Debug("Found global region from DB config", "region", val)
				}
			}
		} else if err != nil {
			slog.Debug("Failed to get LLM integration config from DB", "error", err)
		}
	}

	// L5 DB-tier (only fires when the tier's DB provider matches)
	if v := readDBTierCredential(tier, provider, llmTierRegionFormat, dbConfig); v != "" {
		region = v
		configSource = "DB-tier-specific"
		slog.Debug("Found tier-specific region from DB config", "tier", string(tier), "region", v)
	}

	// L6 DB-agent (highest priority)
	if accountId != "" && appendAgentName && agentName != "" {
		if dbConfig, err := getLLMIntegrationConfig(nil, accountId, dbConfig); err == nil && dbConfig != nil {
			providerKey := fmt.Sprintf(llmProviderFormat, agentName)
			if val, ok := dbConfig[providerKey]; ok && val != "" && val == provider {
				regionKey := fmt.Sprintf(llmProviderRegionFormat, agentName)
				if val, ok := dbConfig[regionKey]; ok && val != "" {
					region = val
					configSource = "DB-agent-specific"
					slog.Debug("Found agent-specific region from DB config (highest priority)", "regionKey", regionKey, "region", val)
				}
			}
		}
	}

	if region != "" {
		slog.Debug("LLM region configuration selected", "source", configSource, "region", region, "provider", provider, "agentName", agentName)
	}
	return region
}

// resolveLLMSecret resolves a provider-scoped secret value (e.g. access key, secret key,
// session token). Layering: ENV first (least specific to most specific), then DB on
// top — DB always beats ENV (see package-level docstring).
//
// `envGlobal` is the value from config.Config (only used when
// config.Config.LlmProvider == provider). `globalKey` is the DB/global key (e.g.
// "llm_provider_access_key"). `agentKeyFormat` is the fmt format for the
// agent-scoped key (e.g. llmProviderAccessKeyFormat).
func resolveLLMSecret(accountId, provider, agentName, envGlobal, globalKey, agentKeyFormat, tierKeyFormat string, appendAgentName bool, resolution ...*LLMConfigResolution) string {
	var dbConfig map[string]string
	var tier ModelTier
	if len(resolution) > 0 && resolution[0] != nil {
		dbConfig = resolution[0].dbConfig
		tier = resolution[0].Tier
	}

	val := ""

	// L1 ENV-global (only if provider matches)
	if config.Config.LlmProvider == provider {
		val = envGlobal
	}

	// L2 ENV-tier (only fires when the tier's ENV provider matches)
	if v := readENVTierCredential(tier, provider, tierKeyFormat); v != "" {
		val = v
	}

	// L3 ENV-agent
	if appendAgentName && agentName != "" {
		providerKey := fmt.Sprintf(llmProviderFormat, agentName)
		if envProviderVal := config.Config.GetString(providerKey, ""); envProviderVal == provider {
			agentKey := fmt.Sprintf(agentKeyFormat, agentName)
			if v := config.Config.GetString(agentKey, ""); v != "" {
				val = v
			}
		}
	}

	// L4 DB-global
	if accountId != "" {
		if dbCfg, err := getLLMIntegrationConfig(nil, accountId, dbConfig); err == nil && dbCfg != nil {
			if providerVal, ok := dbCfg["llm_provider"]; ok && providerVal == provider {
				if v, ok := dbCfg[globalKey]; ok && v != "" {
					val = v
				}
			}
		}
	}

	// L5 DB-tier (only fires when the tier's DB provider matches)
	if v := readDBTierCredential(tier, provider, tierKeyFormat, dbConfig); v != "" {
		val = v
	}

	// L6 DB-agent (highest priority)
	if accountId != "" && appendAgentName && agentName != "" {
		if dbCfg, err := getLLMIntegrationConfig(nil, accountId, dbConfig); err == nil && dbCfg != nil {
			providerKey := fmt.Sprintf(llmProviderFormat, agentName)
			if providerVal, ok := dbCfg[providerKey]; ok && providerVal == provider {
				agentKey := fmt.Sprintf(agentKeyFormat, agentName)
				if v, ok := dbCfg[agentKey]; ok && v != "" {
					val = v
				}
			}
		}
	}

	return val
}

func getLLMAccessKey(accountId, provider, agentName string, appendAgentName bool, resolution ...*LLMConfigResolution) string {
	return resolveLLMSecret(accountId, provider, agentName, config.Config.LlmProviderAccessKey, "llm_provider_access_key", llmProviderAccessKeyFormat, llmTierAccessKeyFormat, appendAgentName, resolution...)
}

func getLLMSecretKey(accountId, provider, agentName string, appendAgentName bool, resolution ...*LLMConfigResolution) string {
	return resolveLLMSecret(accountId, provider, agentName, config.Config.LlmProviderSecretKey, "llm_provider_secret_key", llmProviderSecretKeyFormat, llmTierSecretKeyFormat, appendAgentName, resolution...)
}

func getLLMSessionToken(accountId, provider, agentName string, appendAgentName bool, resolution ...*LLMConfigResolution) string {
	return resolveLLMSecret(accountId, provider, agentName, config.Config.LlmProviderSessionToken, "llm_provider_session_token", llmProviderSessionTokenFormat, llmTierSessionTokenFormat, appendAgentName, resolution...)
}

// getLLMTTFTTimeout returns whether the TTFT timeout should fire for calls to
// the given provider, and the deadline in seconds. Enable is per-provider —
// callers get (false, 0) unless LLM_PROVIDER_TTFT_TIMEOUT_ENABLED_<PROVIDER>=true
// is explicitly set. When enabled, the seconds value is the provider-specific
// override (LLM_PROVIDER_TTFT_TIMEOUT_SECONDS_<PROVIDER>) if set, otherwise the
// global default (config.Config.LlmProviderTTFTTimeoutSeconds).
// ttftTimeoutDefaultEnabled reports whether the TTFT watchdog is armed for a provider
// when no explicit LLM_PROVIDER_TTFT_TIMEOUT_ENABLED_<PROVIDER> is set.
//
// Only googleai defaults on, and only because the failure is measured there: Gemini
// streams were observed accepting a request and then emitting nothing at all, sitting
// out the full 5-minute per-call ceiling before any recovery began — 30 occurrences in
// four days on one agent, averaging 269s of dead time each.
//
// Every other provider stays opt-in. The same hang may well exist elsewhere, but we have
// no TTFT measurements for them, and arming a watchdog against an unmeasured latency
// profile risks abandoning calls that were merely slow. Enable them explicitly once
// their TTFT distribution is known.
func ttftTimeoutDefaultEnabled(provider string) bool {
	return provider == "googleai"
}

func getLLMTTFTTimeout(provider string) (enabled bool, seconds int) {
	if provider == "" {
		return false, 0
	}
	p := strings.ToLower(provider)
	if !config.Config.GetBool(fmt.Sprintf(llmProviderTTFTTimeoutEnabledFormat, p), ttftTimeoutDefaultEnabled(p)) {
		return false, 0
	}
	seconds = config.Config.LlmProviderTTFTTimeoutSeconds
	if v := config.Config.GetInt(fmt.Sprintf(llmProviderTTFTTimeoutSecondsFormat, p), 0); v > 0 {
		seconds = v
	}
	return true, seconds
}

func GetLlmModel(ctx *security.RequestContext, agentName string, accountId string, conversationId string, resolution ...*LLMConfigResolution) (llms.Model, error) {
	if len(resolution) > 0 && resolution[0] != nil {
		res := resolution[0]
		return GetLLMModel(res.Provider, res.Model, agentName, agentName != "", accountId, res)
	}

	slog.Debug("Getting LLM model for agent", "agentName", agentName, "accountId", accountId)
	res, err := ResolveLLMConfig(ctx, accountId, agentName, conversationId)
	if err != nil {
		return nil, err
	}
	return GetLLMModel(res.Provider, res.Model, agentName, agentName != "", accountId, res)
}

func GetLlmModelWithProvider(provider string, agentName string, appendAgentName bool, accountId string, conversationId string) (llms.Model, error) {
	slog.Debug("Getting LLM model with provider", "provider", provider, "agentName", agentName, "appendAgentName", appendAgentName, "accountId", accountId)
	modelName := GetLLMModelName(nil, accountId, provider, agentName, appendAgentName, conversationId)
	slog.Debug("Retrieved model name for provider", "provider", provider, "modelName", modelName)
	return GetLLMModel(provider, modelName, agentName, appendAgentName, accountId)
}

// Anthropic prompt-caching fix (regression from PR #36318).
//
// The multi-breakpoint layout places its primary cache breakpoint on the
// SYSTEM message, but langchaingo v0.1.14's handleSystemMessage unwraps
// llms.CachedContent and serializes `system` as a plain string — silently
// dropping the cache_control block. Anthropic only caches a system prompt
// when `system` is sent as a content-block array. Result: zero cache
// creation/reads on the anthropic provider since 2026-08-16 (worked in March
// when the old single breakpoint landed on a Human message, which the client
// serializes correctly).
//
// Until the client is patched/upgraded, this transport restores the intended
// behavior at the wire: when a /v1/messages body carries `system` as a
// non-empty string (and caching is enabled), it is rewritten to
//
//	"system": [{"type":"text","text":<s>,"cache_control":{"type":"ephemeral"}}]
//
// which is the documented cacheable form. Prompts below Anthropic's minimum
// cacheable size are simply not cached by the API — the form is valid either
// way. Human-message breakpoints are unaffected (they serialize correctly).
// Composes with the temperature sanitizer (http_sanitizer.go) as its base.
type anthropicSystemCacheTransport struct {
	base http.RoundTripper
}

func (t *anthropicSystemCacheTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/messages") || req.Body == nil || !config.Config.LlmEnableCaching {
		return base.RoundTrip(req)
	}
	// Honor the per-request opt-out (custom agents embed dynamic content in
	// their system messages — caching those creates one-off entries).
	if disabled, _ := req.Context().Value(ContextKeyDisableCaching).(bool); disabled {
		return base.RoundTrip(req)
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, err
	}
	// Lazy top-level parse: only "system" is inspected; the (large) messages
	// array stays raw bytes.
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) == nil && payload != nil {
		var sys string
		if raw, ok := payload["system"]; ok && json.Unmarshal(raw, &sys) == nil && strings.TrimSpace(sys) != "" {
			if blocks, mErr := json.Marshal([]map[string]any{{
				"type":          "text",
				"text":          sys,
				"cache_control": map[string]string{"type": "ephemeral"},
			}}); mErr == nil {
				payload["system"] = blocks
				if rewritten, mErr := json.Marshal(payload); mErr == nil {
					body = rewritten
				}
			}
		}
	}
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	clone.Header.Del("Content-Length")
	clone.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return base.RoundTrip(clone)
}

// anthropicCacheHTTPClient carries the system-cache rewrite; passed as the
// base of newAnthropicHTTPClient so the temperature sanitizer and the cache
// rewrite compose on one client.
func anthropicCacheHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   defaultLLMHTTPTimeout,
		Transport: &anthropicSystemCacheTransport{},
	}
}

// anthropicChoiceNormalizer fixes Claude 5-family responses under langchaingo
// v0.1.14: processAnthropicResponse emits one Choice PER CONTENT BLOCK in
// order, and sonnet-5's adaptive reasoning returns [thinking, text] — so
// Choices[0] is the thinking block with Content == "", and every caller that
// reads Choices[0] concludes "llm returned empty content". This decorator
// moves the first choice carrying actual content (text or tool calls) to the
// front; thinking-only choices keep riding behind it with their
// GenerationInfo intact.
type anthropicChoiceNormalizer struct {
	inner llms.Model
}

func wrapAnthropicChoiceNormalizer(inner llms.Model) llms.Model {
	if inner == nil {
		return inner
	}
	return &anthropicChoiceNormalizer{inner: inner}
}

func (a *anthropicChoiceNormalizer) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	resp, err := a.inner.GenerateContent(ctx, messages, options...)
	if err != nil || resp == nil || len(resp.Choices) < 2 {
		return resp, err
	}
	if resp.Choices[0] != nil && (strings.TrimSpace(resp.Choices[0].Content) != "" || len(resp.Choices[0].ToolCalls) > 0) {
		return resp, err
	}
	for i := 1; i < len(resp.Choices); i++ {
		c := resp.Choices[i]
		if c != nil && (strings.TrimSpace(c.Content) != "" || len(c.ToolCalls) > 0) {
			resp.Choices[0], resp.Choices[i] = resp.Choices[i], resp.Choices[0]
			break
		}
	}
	return resp, err
}

func (a *anthropicChoiceNormalizer) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return llms.GenerateFromSinglePrompt(ctx, a, prompt, options...)
}

func getAnthropicLLM(provider, modelName, agentName string, appendAgentName bool, accountId string, resolution ...*LLMConfigResolution) (llms.Model, error) {
	slog.Debug("Initializing Anthropic LLM", "provider", provider, "modelName", modelName, "agentName", agentName, "appendAgentName", appendAgentName, "accountId", accountId)

	var res *LLMConfigResolution
	if len(resolution) > 0 {
		res = resolution[0]
	}

	token := getLLMApiKey(accountId, provider, agentName, appendAgentName, res)
	if token == "" {
		slog.Error("LLM_PROVIDER_API_KEY environment variable is not set for Anthropic LLM provider. Please set this variable to authenticate with the Anthropic LLM service.")
	}
	opts := []anthropic.Option{
		anthropic.WithToken(token),
		anthropic.WithModel(modelName),
		anthropic.WithHTTPClient(newAnthropicHTTPClient(anthropicCacheHTTPClient())),
	}
	baseUrl := getLLMApiEndpoint(accountId, provider, agentName, appendAgentName, res)
	if baseUrl != "" {
		slog.Debug("Using custom base URL for Anthropic", "baseUrl", baseUrl)
		opts = append(opts, anthropic.WithBaseURL(baseUrl))
	}

	llm, err := anthropic.New(opts...)
	if err != nil {
		slog.Error("Failed to create Anthropic LLM", "error", err, "modelName", modelName)
		return nil, err
	}
	slog.Info("Using Anthropic LLM", "model", modelName, "agentName", agentName)
	// Claude 5 responses lead with a thinking choice; promote the text choice.
	return wrapAnthropicChoiceNormalizer(llm), nil
}

func getVertexAILLM(provider, modelName, agentName string, appendAgentName bool, accountId string, resolution ...*LLMConfigResolution) (llms.Model, error) {
	slog.Debug("Initializing Vertex AI LLM", "provider", provider, "modelName", modelName, "agentName", agentName, "appendAgentName", appendAgentName, "accountId", accountId)

	var res *LLMConfigResolution
	if len(resolution) > 0 {
		res = resolution[0]
	}

	token := getLLMApiKey(accountId, provider, agentName, appendAgentName, res)
	if token == "" {
		// Allow empty API key for ADC (Application Default Credentials)
		slog.Info("No LLM_PROVIDER_API_KEY set for Vertex AI, relying on ADC")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	// Resolve project and location from config
	project := config.Config.LlmProviderRegion // Reuse region field for project if needed
	location := config.Config.LlmProviderRegion
	if location == "" {
		location = "us-central1"
	}
	// Try to extract project from GOOGLE_CLOUD_PROJECT or service account
	if p := os.Getenv("GOOGLE_CLOUD_PROJECT"); p != "" {
		project = p
	} else if p := os.Getenv("GCLOUD_PROJECT"); p != "" {
		project = p
	}

	opts := []googleai.Option{googleai.WithDefaultModel(modelName)}
	if project != "" && project != location {
		opts = append(opts, googleai.WithCloudProject(project))
	}
	if location != "" {
		opts = append(opts, googleai.WithCloudLocation(location))
	}

	slog.Info("Vertex AI config", "project", project, "location", location, "model", modelName)
	llm, err := vertex.New(ctx, opts...)
	if err != nil {
		slog.Error("Failed to create Vertex AI LLM", "error", err, "modelName", modelName)
		return nil, err
	}
	slog.Info("Using Vertex AI LLM", "model", modelName, "agentName", agentName)
	return llm, nil
}

func getVertexAIEndpointLLM(provider, modelName, agentName string, appendAgentName bool, accountId string, resolution ...*LLMConfigResolution) (llms.Model, error) {
	slog.Debug("Initializing Vertex AI Endpoint LLM", "provider", provider, "modelName", modelName, "agentName", agentName)

	// Requires:
	// LLM_PROVIDER_API_ENDPOINT = dedicated endpoint domain (e.g., mg-endpoint-xxx.region-xxx.prediction.vertexai.goog)
	// GOOGLE_CLOUD_PROJECT = project ID
	// LLM_PROVIDER_REGION = region
	// LLM_MODEL_NAME = endpoint ID (e.g., mg-endpoint-xxx)
	var res *LLMConfigResolution
	if len(resolution) > 0 {
		res = resolution[0]
	}
	endpointDomain := getLLMApiEndpoint(accountId, provider, agentName, appendAgentName, res)
	project := os.Getenv("GOOGLE_CLOUD_PROJECT")
	location := getLLMRegion(accountId, provider, agentName, appendAgentName, res)
	if location == "" {
		location = "us-central1"
	}

	if endpointDomain == "" {
		return nil, fmt.Errorf("LLM_PROVIDER_API_ENDPOINT is required for vertexai_endpoint provider")
	}
	if project == "" {
		return nil, fmt.Errorf("GOOGLE_CLOUD_PROJECT is required for vertexai_endpoint provider")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	llm, err := vertexendpoint.New(ctx, vertexendpoint.Options{
		EndpointDomain: endpointDomain,
		EndpointID:     modelName, // model name is the endpoint ID
		Project:        project,
		Location:       location,
		Model:          modelName,
	})
	if err != nil {
		slog.Error("Failed to create Vertex AI Endpoint LLM", "error", err)
		return nil, err
	}
	slog.Info("Using Vertex AI Endpoint LLM", "model", modelName, "endpoint", endpointDomain, "agentName", agentName)
	return llm, nil
}

func getGoogleAILLM(provider, modelName, agentName string, appendAgentName bool, accountId string, resolution ...*LLMConfigResolution) (llms.Model, error) {
	slog.Debug("Initializing Google AI LLM", "provider", provider, "modelName", modelName, "agentName", agentName, "appendAgentName", appendAgentName, "accountId", accountId)

	var res *LLMConfigResolution
	if len(resolution) > 0 {
		res = resolution[0]
	}

	token := getLLMApiKey(accountId, provider, agentName, appendAgentName, res)
	if token == "" {
		slog.Error("LLM_PROVIDER_API_KEY environment variable is not set for Google AI")
		return nil, errors.New("LLM_PROVIDER_API_KEY environment variable is not set for Google AI")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	// When an endpoint is configured for the googleai provider, route Gemini
	// traffic through the NB AI Gateway (dogfood) instead of calling Google
	// directly. Empty endpoint = direct Google, the default behavior. The NB
	// token in `token` is sent in the API-key slot; the gateway swaps in the
	// real Google key. The caching helper must resolve the SAME endpoint + token
	// (see llm_common.go / llm_cache.go) or create-vs-reference keys diverge.
	opts := []googleai.Option{googleai.WithAPIKey(token), googleai.WithDefaultModel(modelName)}
	if endpoint := getLLMApiEndpoint(accountId, provider, agentName, appendAgentName, res); endpoint != "" {
		opts = append(opts, googleai.WithBaseURL(endpoint))
		slog.Debug("Routing Google AI traffic through gateway endpoint", "endpoint", endpoint, "agentName", agentName)
	}

	llm, err := googleai.New(ctx, opts...)
	if err != nil {
		slog.Error("Failed to create Google AI LLM", "error", err, "modelName", modelName)
		return nil, err
	}
	slog.Info("Using Google AI LLM", "model", modelName, "agentName", agentName)
	return llm, nil
}

func getAzureAILLM(provider, modelName, agentName string, appendAgentName bool, accountId string, resolution ...*LLMConfigResolution) (llms.Model, error) {
	slog.Debug("Initializing Azure AI LLM", "provider", provider, "modelName", modelName, "agentName", agentName, "appendAgentName", appendAgentName, "accountId", accountId)

	var res *LLMConfigResolution
	if len(resolution) > 0 {
		res = resolution[0]
	}

	adapterName := getLLMModelAdapterName(agentName)
	adapterSupport := checkLLMModelAdapterSupport(agentName)
	slog.Debug("Azure adapter settings", "adapterName", adapterName, "adapterSupport", adapterSupport)

	token := getLLMApiKey(accountId, provider, agentName, appendAgentName, res)
	if token == "" {
		slog.Error("LLM_PROVIDER_API_KEY environment variable is not set for Azure LLM provider. Please set this variable to authenticate with the Azure LLM service.")
	}

	apiVersion := getLLMApiVersion(accountId, provider, agentName, appendAgentName, res)
	baseURL := getLLMApiEndpoint(accountId, provider, agentName, appendAgentName, res)
	slog.Debug("Azure configuration", "apiVersion", apiVersion, "baseURL", baseURL)

	opts := []azure.Option{
		azure.WithToken(token),
		azure.WithAPIVersion(apiVersion),
		azure.WithBaseURL(baseURL),
		azure.WithModel(modelName),
	}

	// Only add WithAdapter if needed
	if adapterName != "" && adapterSupport {
		slog.Debug("Using adapter for Azure model", "adapterName", adapterName, "modelName", modelName)
		opts = append(opts, azure.WithAdapter(adapterName))
	} else if adapterSupport {
		slog.Warn("Adapter is supported but not provided for Azure model", "modelName", modelName)
	} else {
		slog.Debug("Adapter is not supported for Azure model", "modelName", modelName)
	}

	llm, err := azure.New(opts...)
	if err != nil {
		slog.Error("Failed to create Azure AI LLM", "error", err, "modelName", modelName)
		return nil, err
	}
	slog.Info("Using Azure AI LLM", "model", modelName, "agentName", agentName)
	return llm, nil
}

func getHuggingFaceLLM(provider, modelName, agentName string, appendAgentName bool, accountId string, resolution ...*LLMConfigResolution) (llms.Model, error) {
	slog.Debug("Initializing Hugging Face LLM", "provider", provider, "modelName", modelName, "agentName", agentName, "appendAgentName", appendAgentName, "accountId", accountId)

	var res *LLMConfigResolution
	if len(resolution) > 0 {
		res = resolution[0]
	}

	adapterName := getLLMModelAdapterName(agentName)
	adapterSupport := checkLLMModelAdapterSupport(agentName)
	slog.Debug("Hugging Face adapter settings", "adapterName", adapterName, "adapterSupport", adapterSupport)

	apiKey := getLLMApiKey(accountId, provider, agentName, appendAgentName, res)
	apiEndpoint := getLLMApiEndpoint(accountId, provider, agentName, appendAgentName, res)
	apiType := getLLMApiType(accountId, provider, agentName, appendAgentName, res)
	slog.Debug("Hugging Face configuration", "hasApiKey", apiKey != "", "endpoint", apiEndpoint, "apiType", apiType)

	opts := []huggingface.Option{
		huggingface.WithToken(apiKey),
		huggingface.WithURL(apiEndpoint),
		huggingface.WithModel(modelName),
		huggingface.WithAPIType(apiType),
	}

	// Only add WithAdapter if needed
	if adapterName != "" && adapterSupport {
		slog.Debug("Using adapter for Hugging Face model", "adapterName", adapterName, "modelName", modelName)
		opts = append(opts, huggingface.WithAdapter(adapterName))
	} else if adapterSupport {
		slog.Warn("Adapter is supported but not provided for Hugging Face model", "modelName", modelName)
	} else {
		slog.Debug("Adapter is not supported for Hugging Face model", "modelName", modelName)
	}

	llm, err := huggingface.New(opts...)
	if err != nil {
		slog.Error("Failed to create Hugging Face LLM", "error", err, "modelName", modelName)
		return nil, err
	}
	slog.Info("Using Hugging Face LLM", "model", modelName, "agentName", agentName)
	return llm, nil
}

func getSageMakerLLM(provider, agentName string, appendAgentName bool, accountId string, resolution ...*LLMConfigResolution) (llms.Model, error) {
	slog.Debug("Initializing SageMaker LLM", "provider", provider, "agentName", agentName, "appendAgentName", appendAgentName, "accountId", accountId)

	var res *LLMConfigResolution
	if len(resolution) > 0 {
		res = resolution[0]
	}

	endpoint := getLLMApiEndpoint(accountId, provider, agentName, appendAgentName, res)
	region := getLLMRegion(accountId, provider, agentName, appendAgentName, res)
	slog.Debug("SageMaker configuration", "endpoint", endpoint, "region", region)

	llm, err := sagemaker.New(endpoint, region, map[string]any{})
	if err != nil {
		slog.Error("Failed to create SageMaker LLM", "error", err, "endpoint", endpoint, "region", region)
		return nil, err
	}
	slog.Info("Using SageMaker LLM", "endpoint", endpoint, "region", region, "agentName", agentName)
	return llm, nil
}

func getOpenAILLM(provider, modelName, agentName string, appendagentName bool, accountId string, resolution ...*LLMConfigResolution) (llms.Model, error) {
	slog.Debug("Initializing OpenAI LLM", "provider", provider, "modelName", modelName, "agentName", agentName, "appendAgentName", appendagentName, "accountId", accountId)

	var res *LLMConfigResolution
	if len(resolution) > 0 {
		res = resolution[0]
	}

	token := getLLMApiKey(accountId, provider, agentName, appendagentName, res)
	llmApiType := getLLMApiType(accountId, provider, agentName, appendagentName, res)
	apiType := openai.APITypeOpenAI
	// OAuth / extra-header transport (#36556) — custom provider only for now.
	// In OAuth mode the transport owns the Authorization header; the static
	// token is only a non-empty placeholder so the client library doesn't
	// reject construction.
	var authClient *http.Client
	if strings.EqualFold(provider, ProviderCustom) {
		var oauthMode bool
		var err error
		authClient, oauthMode, err = buildLLMAuthHTTPClient(accountId, res)
		if err != nil {
			slog.Error("Failed to resolve LLM auth settings", "error", err, "provider", provider, "agentName", agentName)
			return nil, err
		}
		if oauthMode && token == "" {
			token = "oauth-managed"
		}
	}
	if token == "" {
		slog.Error("LLM_PROVIDER_API_KEY environment variable is not set for OpenAI LLM provider. Please set this variable to authenticate with the OpenAI LLM service.")
	}
	if strings.ToLower(llmApiType) == "azure" {
		apiType = openai.APITypeAzure
		slog.Debug("Using Azure API type for OpenAI", "apiType", apiType)
	} else if strings.ToLower(llmApiType) == "azure_ad" {
		apiType = openai.APITypeAzureAD
		slog.Debug("Using Azure AD API type for OpenAI", "apiType", apiType)
	}

	baseURL := getLLMApiEndpoint(accountId, provider, agentName, appendagentName, res)
	embeddingModel := config.Config.LlmProviderEnbeddingModel
	slog.Debug("OpenAI configuration", "apiType", apiType, "baseURL", baseURL, "embeddingModel", embeddingModel)

	var responseFormatJSON = &openai.ResponseFormat{Type: "text"}
	// OAuth/header client (nil in static mode) rides as the sanitizer's base so
	// both wire concerns compose on one client.
	llm, err := openai.New(openai.WithResponseFormat(responseFormatJSON), openai.WithAPIType(apiType), openai.WithToken(token), openai.WithModel(modelName), openai.WithEmbeddingModel(embeddingModel), openai.WithBaseURL(baseURL), openai.WithHTTPClient(newOpenAIHTTPClient(authClient)))
	if err != nil {
		slog.Error("Failed to create OpenAI LLM", "error", err, "modelName", modelName)
		return nil, err
	}
	slog.Info("Using OpenAI LLM", "model", modelName, "agentName", agentName, "apiType", apiType)
	// Strip llm-server-internal thinking keys (ThinkingBudget/ThinkingLevel) from Metadata so
	// they never serialize into the outbound OpenAI `metadata` field — the langchaingo openai
	// client only filters "openai:"-prefixed keys, and strict OpenAI-compatible providers (Vertex
	// via the NB AI Gateway) reject non-string metadata. Covers `custom` too (getCustomLLM
	// delegates here). See openai_thinking_metadata.go.
	return wrapStripInternalThinkingMetadata(llm), nil
}

// getCustomLLM serves any provider exposing OpenAI's Chat Completions
// API at its own base URL. It reuses the OpenAI client because the wire format
// is identical — the only real difference is that there is no sensible default
// endpoint, so a missing one is an error rather than a silent fall-through to
// api.openai.com with someone else's key.
//
// Endpoint convention: the client appends "/chat/completions" verbatim, so the
// configured value must already include the version segment —
// "https://openrouter.ai/api/v1", not "https://openrouter.ai/api". Note this
// differs from the huggingface provider, whose client appends
// "/v1/chat/completions" itself.
func getCustomLLM(provider, modelName, agentName string, appendAgentName bool, accountId string, resolution ...*LLMConfigResolution) (llms.Model, error) {
	var res *LLMConfigResolution
	if len(resolution) > 0 {
		res = resolution[0]
	}

	if endpoint := getLLMApiEndpoint(accountId, provider, agentName, appendAgentName, res); strings.TrimSpace(endpoint) == "" {
		slog.Error("openai-compatible provider requires an api endpoint",
			"provider", provider, "model", modelName, "agentName", agentName)
		return nil, fmt.Errorf("llm provider %q requires llm_provider_api_endpoint (e.g. https://openrouter.ai/api/v1)", provider)
	}

	return getOpenAILLM(provider, modelName, agentName, appendAgentName, accountId, resolution...)
}

func getBedrockLLM(provider, modelName, agentName string, appendAgentName bool, accountId string, resolution ...*LLMConfigResolution) (llms.Model, error) {
	slog.Debug("Initializing Bedrock LLM", "provider", provider, "modelName", modelName, "agentName", agentName, "appendAgentName", appendAgentName, "accountId", accountId)

	var res *LLMConfigResolution
	if len(resolution) > 0 {
		res = resolution[0]
	}

	region := getLLMRegion(accountId, provider, agentName, appendAgentName, res)
	accessKey := getLLMAccessKey(accountId, provider, agentName, appendAgentName, res)
	secretKey := getLLMSecretKey(accountId, provider, agentName, appendAgentName, res)
	sessionToken := getLLMSessionToken(accountId, provider, agentName, appendAgentName, res)

	// Fail fast on partial static credentials: access_key and secret_key must be set
	// together. Silently falling back to the default chain would hide a misconfiguration.
	if (accessKey == "") != (secretKey == "") {
		slog.Error("Bedrock: partial static credentials configured — access_key and secret_key must be set together",
			"provider", provider, "agentName", agentName,
			"hasAccessKey", accessKey != "", "hasSecretKey", secretKey != "")
		return nil, fmt.Errorf("bedrock: incomplete static credentials (access_key and secret_key must both be set or both be empty)")
	}

	loadOpts := []func(*awsConfig.LoadOptions) error{}
	credSource := "default-chain"
	if accessKey != "" && secretKey != "" {
		loadOpts = append(loadOpts, awsConfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, sessionToken),
		))
		credSource = "static-config"
	}

	cfg, err := awsConfig.LoadDefaultConfig(context.Background(), loadOpts...)
	if err != nil {
		slog.Error("Failed to load AWS config for Bedrock", "error", err)
		return nil, err
	}

	if region != "" {
		cfg.Region = region
		slog.Debug("Using custom region for Bedrock", "region", region)
	} else {
		slog.Debug("Using default AWS region for Bedrock", "region", cfg.Region)
	}

	cfg.RetryMaxAttempts = config.Config.LlmProviderMaxRetries
	slog.Debug("Bedrock retry configuration", "maxRetries", cfg.RetryMaxAttempts)

	client := bedrockruntime.NewFromConfig(cfg)
	llm, err := bedrock.New(bedrock.WithModel(modelName), bedrock.WithClient(client))
	if err != nil {
		slog.Error("Failed to create Bedrock LLM", "error", err, "modelName", modelName, "region", cfg.Region)
		return nil, err
	}
	slog.Info("Using Bedrock LLM", "model", modelName, "agentName", agentName, "region", cfg.Region, "credSource", credSource)
	return llm, nil
}

type ModelConfig struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Source   string `json:"source"` // legacy internal label, e.g. "env-tier-reasoning" (kept for backward compat)

	// ConfigSource is the stable machine-readable slot id — the value clients
	// send back as NBQueryConfig.LlmConfigSource to pin a request to this slot.
	// Format: {layer}:{scope}[:{name}] — see LlmConfigSource docstring.
	ConfigSource string `json:"llm_config_source,omitempty"`

	// ConfigName is the human-readable label for this slot, rendered as the
	// "Config" column in the model picker. Synthesized from ConfigSource + (for
	// db rows) integrations.name. See ConfigNameFor.
	ConfigName string `json:"config_name,omitempty"`

	// IsFallback is true when this row was surfaced via a *_fallbacks list
	// rather than the slot's primary. Display hint only; fallbacks are still
	// selectable as first-class picks.
	IsFallback bool `json:"is_fallback,omitempty"`
}

// MergedSlot names one configured slot that resolves to a credential. Returned
// as credentials[].sources — not for display, but so a client can map a stored
// llm_config_source (which may be any of a credential's slots) back to the
// credential that now represents it.
type MergedSlot struct {
	ConfigSource string `json:"llm_config_source"`
	ConfigName   string `json:"config_name,omitempty"`
}

// LLMConfigResolution provides detailed information about how LLM model configuration
// is resolved, showing the full hierarchy and which layer is active
type LLMConfigResolution struct {
	Provider     string            `json:"provider"`      // Active provider (e.g., "openai", "anthropic")
	Model        string            `json:"model"`         // Active model (e.g., "gpt-4", "claude-3-5-sonnet")
	Source       string            `json:"source"`        // Which layer is active
	IsOverridden bool              `json:"is_overridden"` // True if conversation has explicit override
	AgentName    string            `json:"agent_name,omitempty"`
	Tier         ModelTier         `json:"tier,omitempty"`        // Category the call opted into (empty when no tier was selected)
	MaxContext   int               `json:"max_context,omitempty"` // User-configured context window (tokens); 0 = not set → fall back to the model map
	Hierarchy    []LLMConfigLayer  `json:"hierarchy"`             // Full resolution chain
	dbConfig     map[string]string // unexported cache for optimized downstream lookups

	// Pinned* fields are set only when Source == "pinned:<id>" (i.e., the request
	// carried NBQueryConfig.LlmConfigSource). Downstream credential resolvers
	// (getLLMApiEndpoint / getLLMApiKey / getLLMApiType / getLLMApiVersion /
	// getLLMRegion) short-circuit to these values when set, skipping the normal
	// layered walk. Empty string means "not pinned" — resolver falls through.
	PinnedConfigSource string `json:"pinned_config_source,omitempty"` // original source id, for cache/semaphore keys and audit logs
	PinnedEndpoint     string `json:"-"`
	PinnedApiKey       string `json:"-"`
	PinnedApiType      string `json:"-"`
	PinnedApiVersion   string `json:"-"`
	PinnedRegion       string `json:"-"`
	// AWS-style credentials and the adapter id complete the destination. Without
	// them a pinned bedrock/sagemaker slot would still resolve its identity via
	// the layered walk, so the pin wouldn't actually pin.
	PinnedAccessKey    string `json:"-"`
	PinnedSecretKey    string `json:"-"`
	PinnedSessionToken string `json:"-"`
	PinnedAdapterId    string `json:"-"`
	// OAuth client-credentials settings (#36556). Without these a pinned
	// OAuth config would silently fall back to api-key auth — the same
	// pinned-resolution gap that previously bit MaxContext.
	PinnedAuthType          string `json:"-"`
	PinnedOAuthTokenURL     string `json:"-"`
	PinnedOAuthClientID     string `json:"-"`
	PinnedOAuthClientSecret string `json:"-"`
	PinnedOAuthScope        string `json:"-"`
	PinnedExtraHeaders      string `json:"-"`
}

// credentialIdentity fingerprints every field that decides where a request goes
// and how it authenticates. Deliberately excludes model: the model is a
// parameter of the request, not of the connection, so two slots differing only
// in model are the same credential.
//
// Provider is trimmed and lower-cased — dev data contains values like
// "googleai " with trailing whitespace, which would otherwise read as a
// separate credential.
func credentialIdentity(res *LLMConfigResolution) string {
	return strings.ToLower(strings.TrimSpace(res.Provider)) + "|" +
		credsFingerprint(res.PinnedApiKey, res.PinnedEndpoint, res.PinnedApiVersion, res.PinnedRegion,
			res.PinnedAccessKey, res.PinnedSecretKey, res.PinnedSessionToken,
			res.PinnedAuthType, res.PinnedOAuthTokenURL, res.PinnedOAuthClientID,
			res.PinnedOAuthClientSecret, res.PinnedOAuthScope, res.PinnedExtraHeaders) + "|" +
		strings.TrimSpace(res.PinnedApiType) + "|" + strings.TrimSpace(res.PinnedAdapterId)
}

// LLMConfigLayer represents one layer in the configuration resolution hierarchy
type LLMConfigLayer struct {
	Level    string `json:"level"`    // "env-global", "db-global", "env-agent", "db-agent", "conversation"
	Provider string `json:"provider"` // Provider at this layer
	Model    string `json:"model"`    // Model at this layer
	Active   bool   `json:"active"`   // Whether this layer is being used
}

type ContextKey string

const (
	ContextKeyLLMResolution ContextKey = "llm_resolution_cache"
)

// LLMResolutionCache provides a thread-safe per-request cache for LLM configurations
type LLMResolutionCache struct {
	cache map[string]*LLMConfigResolution
	mu    sync.RWMutex
}

func NewLLMResolutionCache() *LLMResolutionCache {
	return &LLMResolutionCache{
		cache: make(map[string]*LLMConfigResolution),
	}
}

func (c *LLMResolutionCache) Get(key string) (*LLMConfigResolution, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	res, ok := c.cache[key]
	return res, ok
}

func (c *LLMResolutionCache) Set(key string, res *LLMConfigResolution) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[key] = res
}

// contextSizeConfigKey is the base config key for the user-set context window.
// Per-tier uses llm_tier_context_size_<tier>; per-agent uses
// llm_model_context_size_<agent> — mirroring the model-name key convention.
const contextSizeConfigKey = "llm_model_context_size"

// resolveModelContextMap builds a model-name → context-window (tokens) map from
// every configured (model, context-size) pair across scopes — ENV and DB,
// global / per-tier / per-agent. The context window is a property of the MODEL,
// so whichever layer ends up selecting a model (including a conversation or
// per-request override) can look its window up by name. Returns an empty map
// when nothing is configured; callers then fall back to GetLlmMaxTokenLength.
func resolveModelContextMap(dbConfig map[string]string) map[string]int {
	m := map[string]int{}

	dbStr := func(key string) string {
		if dbConfig == nil {
			return ""
		}
		return dbConfig[key]
	}
	dbInt := func(key string) int {
		if v := dbStr(key); v != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				return n
			}
		}
		return 0
	}
	// put records a (model, size) pair. Later calls win for the same model, so
	// DB (invoked after ENV) overrides ENV — mirroring the provider/model layering.
	put := func(model string, size int) {
		model = strings.TrimSpace(model)
		if model != "" && size > 0 {
			m[model] = size
		}
	}

	// Global (ENV then DB). DB global model name lives under "llm_model_name".
	put(config.Config.LlmModel, config.Config.GetInt(contextSizeConfigKey, 0))
	put(dbStr("llm_model_name"), dbInt(contextSizeConfigKey))

	// Per-tier (ENV then DB).
	for _, t := range []string{"reasoning", "retrieval", "summary"} {
		put(config.Config.GetString(fmt.Sprintf(llmTierModelFormat, t), ""), config.Config.GetInt("llm_tier_context_size_"+t, 0))
		put(dbStr(fmt.Sprintf(llmTierModelFormat, t)), dbInt("llm_tier_context_size_"+t))
	}

	// Per-agent (DB only — agent overrides live in the tenant integration config).
	for key := range dbConfig {
		if strings.HasPrefix(key, "llm_model_name_") && key != "llm_model_name" {
			agent := strings.TrimPrefix(key, "llm_model_name_")
			put(dbStr(key), dbInt(contextSizeConfigKey+"_"+agent))
		}
	}

	return m
}

// ResolveModelMaxContext returns the usable context window (tokens) for a model:
// the user-configured value (UI/config) if set, else the hardcoded model map /
// 32k default via GetLlmMaxTokenLength.
func ResolveModelMaxContext(resolution *LLMConfigResolution, model string) int {
	if resolution != nil && resolution.MaxContext > 0 {
		return resolution.MaxContext
	}
	return GetLlmMaxTokenLength(model)
}

// ResolveLLMConfig returns the complete LLM configuration resolution showing all layers
// and which one is active. This is useful for UIs to display current configuration
// and allow users to understand where their model config comes from.
//
// Parameters:
//   - ctx: The RequestContext for per-request caching (MANDATORY)
//   - accountId: The account to resolve config for
//   - agentName: The agent name (e.g., "llm", "k8s_debug_react") - use "" for no agent-specific config
//   - conversationId: Optional conversation ID to check for conversation-level override
//
// Returns the active configuration with full hierarchy showing all fallback layers.
func ResolveLLMConfig(ctx *security.RequestContext, accountId, agentName string, conversationId string) (*LLMConfigResolution, error) {
	t0 := time.Now()

	if ctx == nil {
		slog.Warn("ResolveLLMConfig called without context, per-request caching disabled", "agent", agentName)
	}

	// The category the call opted into, if any (planner / query_generator /
	// summariser). Empty = no category opted → normal resolution flow.
	tier := modelTierFromContext(ctx)

	// Pinned-source short-circuit: highest precedence, above every layer below.
	// When the request carried NBQueryConfig.LlmConfigSource, ContextKeyLlmConfigSourceOverride
	// is populated and every credential (provider, model, endpoint, api-key,
	// api-type, api-version, region) is read from exactly that one slot — no
	// layering, no fallback across sources. Sub-agent calls in the same request
	// inherit the same context key and hit this branch too, so the entire
	// request tree lands on the same endpoint regardless of tier tagging.
	//
	// Cache scope: this cache is owned by the RequestContext (per-request), so
	// cache entries never cross requests or tenants. accountId is intentionally
	// NOT in the key — a single request has one accountId and we want all
	// sub-agents to share the same cached pinned resolution regardless of the
	// agent that first primed it. Key IS agentName + tier so each sub-agent
	// gets its own AgentName / Tier fields on the resolution struct.
	//
	// Hierarchy note: unlike the layered walk, pinned resolutions have a
	// single-entry Hierarchy (the pinned layer). By design — pinning bypasses
	// layer consideration so there's nothing else to report. UIs that render
	// Hierarchy should expect this and fall back to Source / PinnedConfigSource
	// for the "which config was used" answer.
	// A tier-tagged call whose pick names its own credential resolves through
	// that credential, not the conversation-wide pin: in By-task mode each task
	// carries its own (credential, model) pair, so a blanket pin must not
	// override it. Untagged calls still use the conversation pin below.
	pinnedSource, pinnedTierModel := tierPinFor(ctx, conversationId, tier)
	if pinnedSource == "" {
		pinnedSource = pinnedConfigSourceFromContext(ctx)
		// Background work (memory extraction, session extractor, title
		// generation) runs on a fresh context.Background(), so the stamped key
		// is gone and those calls would silently resolve through the layered
		// walk — a different endpoint and api key than the user pinned. The
		// conversation row is the durable record; the tier layer below already
		// relies on the same fallback.
		if pinnedSource == "" && conversationId != "" {
			if _, _, _, convPin, err := GetConversationOverride(conversationId); err == nil {
				pinnedSource = convPin
			}
		}
	}
	if pinnedSource != "" {
		var pinnedCache *LLMResolutionCache
		pinnedCacheKey := fmt.Sprintf("pinned:%s:%s:%s", pinnedSource, agentName, tier)
		if ctx != nil {
			if goCtx := ctx.GetContext(); goCtx != nil {
				if val := goCtx.Value(ContextKeyLLMResolution); val != nil {
					pinnedCache = val.(*LLMResolutionCache)
					if cached, ok := pinnedCache.Get(pinnedCacheKey); ok {
						return cached, nil
					}
				}
			}
		}
		res, err := resolveFromPinnedSource(ctx, pinnedSource, accountId, agentName, tier)
		if err != nil {
			return nil, err
		}
		// A tier pick names its own model alongside its credential. The slot's
		// primary is only the default for calls that didn't ask for one.
		if pinnedTierModel != "" {
			res.Model = pinnedTierModel
			res.Hierarchy[0].Model = pinnedTierModel
		}
		res.AgentName = agentName
		res.Tier = tier
		if pinnedCache != nil {
			pinnedCache.Set(pinnedCacheKey, res)
		}
		// Info level here (once per unique source/agent/tier combo per request,
		// enforced by the cache above) so ops keeps a prod audit trail of which
		// pinned config was actually resolved without spamming the sub-agent
		// callers each time — those go through the cache-hit branch above and
		// don't log.
		slog.Info("llm_config_source pinned resolution",
			"source", pinnedSource,
			"provider", res.Provider,
			"model", res.Model,
			"agentName", agentName,
			"tier", string(tier),
			"accountId", accountId)
		return res, nil
	}

	// Optimization: Check per-request cache if context is provided
	var cache *LLMResolutionCache
	if ctx != nil {
		// Guard against zero-value RequestContext (nil internal context).
		if goCtx := ctx.GetContext(); goCtx != nil {
			if val := goCtx.Value(ContextKeyLLMResolution); val != nil {
				cache = val.(*LLMResolutionCache)
				cacheKey := fmt.Sprintf("%s:%s:%s:%s", accountId, agentName, conversationId, tier)
				if res, ok := cache.Get(cacheKey); ok {
					slog.Debug("Reusing per-request LLM config resolution", "cacheKey", cacheKey)
					return res, nil
				}
			}
		}
	}

	slog.Debug("Resolving LLM config hierarchy",
		"accountId", accountId,
		"agentName", agentName,
		"conversationId", conversationId)

	result := &LLMConfigResolution{
		AgentName:    agentName,
		Tier:         tier,
		IsOverridden: false,
		Hierarchy:    []LLMConfigLayer{},
	}

	// Fetch DB config ONCE for the entire resolution process
	var dbConfig map[string]string
	if accountId != "" {
		dbFetchStart := time.Now()
		result.dbConfig, _ = getLLMIntegrationConfig(ctx, accountId)
		slog.Debug("Fetched LLM integration config from DB", "duration", time.Since(dbFetchStart).String(), "accountId", accountId)
	}
	dbConfig = result.dbConfig

	appendAgentName := agentName != ""

	// Layering rule: lower layers are written first, higher layers overwrite.
	// Within each *source* (ENV, then DB) the layers are ordered by specificity
	// (global → tier → agent). The DB block as a whole sits above the ENV block
	// so a tenant-provided DB value never gets silently overridden by an
	// operator's stale agent-scoped ENV. See package-level docstring.

	// === ENV layers (least specific first) ===

	// L1 ENV-global
	if config.Config.LlmProvider != "" && config.Config.LlmModel != "" {
		result.Hierarchy = append(result.Hierarchy, LLMConfigLayer{
			Level:    "env-global",
			Provider: config.Config.LlmProvider,
			Model:    config.Config.LlmModel,
			Active:   false,
		})
		result.Provider = config.Config.LlmProvider
		result.Model = config.Config.LlmModel
		result.Source = "env-global"
	}

	// L2 ENV-tier (only when the call opted into a category)
	if tier != "" {
		tierProviderKey := fmt.Sprintf(llmTierProviderFormat, string(tier))
		tierModelKey := fmt.Sprintf(llmTierModelFormat, string(tier))
		envTierProvider := config.Config.GetString(tierProviderKey, "")
		envTierModel := config.Config.GetString(tierModelKey, "")
		if envTierProvider != "" && envTierModel != "" {
			result.Hierarchy = append(result.Hierarchy, LLMConfigLayer{
				Level: "env-tier", Provider: envTierProvider, Model: envTierModel, Active: false,
			})
			result.Provider = envTierProvider
			result.Model = envTierModel
			result.Source = "env-tier"
		} else if envTierProvider != "" || envTierModel != "" {
			// Half-set tier config — provider OR model but not both. Layer
			// silently no-ops; surface so an operator who forgot the matching
			// half gets a fast diagnosis instead of a wrong-provider 401 later.
			slog.Warn("ResolveLLMConfig: env-tier is half-set — provider/model must both be present, layer skipped",
				"tier", string(tier),
				"env_provider_set", envTierProvider != "",
				"env_model_set", envTierModel != "",
				"agentName", agentName)
		}
	}

	// L3 ENV-agent
	if appendAgentName {
		providerKey := fmt.Sprintf(llmProviderFormat, agentName)
		modelKey := fmt.Sprintf(llmModelFormat, agentName)
		envAgentProvider := config.Config.GetString(providerKey, "")
		envAgentModel := config.Config.GetString(modelKey, "")
		if envAgentProvider != "" && envAgentModel != "" {
			result.Hierarchy = append(result.Hierarchy, LLMConfigLayer{
				Level:    "env-agent",
				Provider: envAgentProvider,
				Model:    envAgentModel,
				Active:   false,
			})
			result.Provider = envAgentProvider
			result.Model = envAgentModel
			result.Source = "env-agent"
			slog.Debug("Found env-agent config",
				"agentName", agentName,
				"provider", envAgentProvider,
				"model", envAgentModel)
		} else if envAgentProvider != "" || envAgentModel != "" {
			slog.Warn("ResolveLLMConfig: env-agent is half-set — provider/model must both be present, layer skipped",
				"agentName", agentName,
				"env_provider_set", envAgentProvider != "",
				"env_model_set", envAgentModel != "")
		}
	}

	// === DB layers (least specific first, but the whole block beats every ENV layer) ===

	// L4 DB-global
	if dbConfig != nil {
		if provider, ok := dbConfig["llm_provider"]; ok && provider != "" {
			if model, ok := dbConfig["llm_model_name"]; ok && model != "" {
				result.Hierarchy = append(result.Hierarchy, LLMConfigLayer{
					Level:    "db-global",
					Provider: provider,
					Model:    model,
					Active:   false,
				})
				result.Provider = provider
				result.Model = model
				result.Source = "db-global"
			} else {
				// Partial config — provider set but model missing. Layer silently
				// no-ops, and resolution falls through to whatever lower ENV layer
				// took effect, which is rarely what the tenant intended.
				slog.Warn("ResolveLLMConfig: DB-global has llm_provider but llm_model_name is missing — leaving previous ENV-layer resolution in place",
					"accountId", accountId,
					"db_provider", provider,
					"agentName", agentName)
			}
		}
	}

	// L5 DB-tier
	if tier != "" && dbConfig != nil {
		tierProviderKey := fmt.Sprintf(llmTierProviderFormat, string(tier))
		tierModelKey := fmt.Sprintf(llmTierModelFormat, string(tier))
		dbTierProvider, hasProvider := dbConfig[tierProviderKey]
		dbTierModel, hasModel := dbConfig[tierModelKey]
		if hasProvider && dbTierProvider != "" && hasModel && dbTierModel != "" {
			result.Hierarchy = append(result.Hierarchy, LLMConfigLayer{
				Level: "db-tier", Provider: dbTierProvider, Model: dbTierModel, Active: false,
			})
			result.Provider = dbTierProvider
			result.Model = dbTierModel
			result.Source = "db-tier"
		} else if (hasProvider && dbTierProvider != "") || (hasModel && dbTierModel != "") {
			slog.Warn("ResolveLLMConfig: db-tier is half-set — provider/model must both be present, layer skipped",
				"accountId", accountId,
				"tier", string(tier),
				"db_provider_set", hasProvider && dbTierProvider != "",
				"db_model_set", hasModel && dbTierModel != "",
				"agentName", agentName)
		}
	}

	// L6 DB-agent
	if dbConfig != nil && appendAgentName {
		providerKey := fmt.Sprintf(llmProviderFormat, agentName)
		modelKey := fmt.Sprintf(llmModelFormat, agentName)
		dbAgentProvider, hasProvider := dbConfig[providerKey]
		dbAgentModel, hasModel := dbConfig[modelKey]
		if hasProvider && dbAgentProvider != "" && hasModel && dbAgentModel != "" {
			result.Hierarchy = append(result.Hierarchy, LLMConfigLayer{
				Level:    "db-agent",
				Provider: dbAgentProvider,
				Model:    dbAgentModel,
				Active:   false,
			})
			result.Provider = dbAgentProvider
			result.Model = dbAgentModel
			result.Source = "db-agent"
			slog.Debug("Found db-agent config",
				"agentName", agentName,
				"provider", dbAgentProvider,
				"model", dbAgentModel)
		} else if (hasProvider && dbAgentProvider != "") || (hasModel && dbAgentModel != "") {
			slog.Warn("ResolveLLMConfig: db-agent is half-set — provider/model must both be present, layer skipped",
				"accountId", accountId,
				"agentName", agentName,
				"db_provider_set", hasProvider && dbAgentProvider != "",
				"db_model_set", hasModel && dbAgentModel != "")
		}
	}

	// L7: Conversation-specific override (per-request user-explicit). The
	// conversation row can be in one of two mutually-exclusive modes:
	//
	//   - blanket  → llm_provider + llm_model set; applies to every call
	//                regardless of tier-tagging (acts at the conversation
	//                layer here, with `tier` ignored).
	//   - per-tier → llm_tier_overrides set; applies ONLY when the call
	//                opted into a tier whose key matches the override.
	//                Untagged calls in the same conversation fall through
	//                to the lower-precedence layers (no surprise blanket
	//                substitute — that's a UX choice; the UI warns about
	//                it when the user picks per-category mode).
	if conversationId != "" {
		if p, m, tierOverrides, _, err := GetConversationOverride(conversationId); err == nil {
			// Blanket conversation override.
			if p != "" && m != "" {
				result.Hierarchy = append(result.Hierarchy, LLMConfigLayer{
					Level: "conversation", Provider: p, Model: m, Active: false,
				})
				result.Provider = p
				result.Model = m
				result.Source = "conversation"
				result.IsOverridden = true
				slog.Debug("Found conversation blanket override",
					"conversationId", conversationId, "provider", p, "model", m)
			}
			// Per-tier conversation override — only fires for tier-tagged calls.
			if tier != "" {
				if pick, ok := tierOverrides.Get(string(tier)); ok {
					result.Hierarchy = append(result.Hierarchy, LLMConfigLayer{
						Level: "conversation-tier", Provider: pick.Provider, Model: pick.Model, Active: false,
					})
					result.Provider = pick.Provider
					result.Model = pick.Model
					result.Source = "conversation-tier"
					result.IsOverridden = true
					slog.Debug("Found conversation per-tier override",
						"conversationId", conversationId, "tier", string(tier),
						"provider", pick.Provider, "model", pick.Model)
				}
			}
		}
	}

	// Highest precedence: explicit per-request overrides from ctx.
	// (a) Single-model override (blanket): both provider and model must be
	//     present; a half-set override is ignored. (b) Per-tier override: a
	//     map applied only when the call is tier-tagged; the matching key's
	//     pick wins. Both can coexist, but the per-tier override sits above
	//     the single-model override for tagged calls.
	if ctx != nil {
		op, _ := ctx.GetContext().Value(ContextKeyLlmProviderOverride).(string)
		om, _ := ctx.GetContext().Value(ContextKeyLlmModelOverride).(string)
		if op != "" && om != "" {
			result.Hierarchy = append(result.Hierarchy, LLMConfigLayer{
				Level: "context-override", Provider: op, Model: om, Active: false,
			})
			result.Provider = op
			result.Model = om
			result.Source = "context-override"
			result.IsOverridden = true
		}
		if tier != "" {
			if v, ok := ctx.GetContext().Value(ContextKeyLlmTierModelOverrides).(ConversationTierOverrides); ok {
				if pick, ok := v.Get(string(tier)); ok {
					result.Hierarchy = append(result.Hierarchy, LLMConfigLayer{
						Level: "context-override-tier", Provider: pick.Provider, Model: pick.Model, Active: false,
					})
					result.Provider = pick.Provider
					result.Model = pick.Model
					result.Source = "context-override-tier"
					result.IsOverridden = true
				}
			}
		}
	}

	// Mark the active layer in the hierarchy
	for i := range result.Hierarchy {
		if result.Hierarchy[i].Level == result.Source {
			result.Hierarchy[i].Active = true
		}
	}

	// Validation: ensure we found some configuration
	if result.Provider == "" || result.Model == "" {
		return nil, fmt.Errorf("no LLM configuration found for accountId=%s, agentName=%s", accountId, agentName)
	}

	// Context window is a property of the MODEL, resolved AFTER all override
	// layers so it tracks the model the request actually uses — including a
	// conversation / per-request override. Looked up by model name; 0 (no entry)
	// lets callers fall back to the model's hardcoded window via
	// GetLlmMaxTokenLength (see ResolveModelMaxContext).
	result.MaxContext = resolveModelContextMap(dbConfig)[result.Model]

	// contextSource makes it observable whether the window came from a configured
	// (DB/ENV) value or will fall back to the model's hardcoded map.
	contextSource := "model-map"
	if result.MaxContext > 0 {
		contextSource = "db-config"
	}
	// ctx.GetLogger() carries trace_id/span_id; ctx is nil for a few callers.
	resolutionLogger := slog.Default()
	if ctx != nil {
		resolutionLogger = ctx.GetLogger()
	}
	resolutionLogger.Info("LLM config resolution complete",
		"duration", time.Since(t0).String(),
		"provider", result.Provider,
		"model", result.Model,
		"source", result.Source,
		"tier", string(tier),
		"agent", agentName,
		"maxContext", result.MaxContext,
		"contextSource", contextSource)

	// Save to per-request cache if available
	if cache != nil {
		cacheKey := fmt.Sprintf("%s:%s:%s:%s", accountId, agentName, conversationId, tier)
		cache.Set(cacheKey, result)
	}

	return result, nil
}

// modelTierFromContext extracts the category the call opted into from the
// request context. Returns an empty tier when no category was opted.
func modelTierFromContext(ctx *security.RequestContext) ModelTier {
	if ctx == nil {
		return ""
	}
	// A zero-value security.RequestContext has a nil internal context.Context
	// (e.g., tests that build planner stubs with `&security.RequestContext{}`).
	// Guard the Value() call so we don't panic on those paths.
	goCtx := ctx.GetContext()
	if goCtx == nil {
		return ""
	}
	if v, ok := goCtx.Value(ContextKeyModelTier).(ModelTier); ok && v != "" {
		return v
	}
	return ""
}

// GetAllConfiguredModels returns every unique (provider, model) pair that the
// runtime resolver could pick for this account.
//
// Walks both ENV and DB and emits the UNION (DB does not hide ENV — both are
// real config sources, see the [2026-05] LLM config precedence constitution
// entry). Covers six categories per source:
//
//   - global model
//   - global model fallbacks
//   - tier model (reasoning / retrieval / summary)
//   - tier model fallbacks (per tier)
//   - per-agent model overrides
//   - per-agent model fallbacks
//
// Tier and agent rows fall back to the global provider when their own provider
// slot is empty — same rule the resolver uses. ENV agent discovery is
// dynamic (env-var suffix scan) so newly-registered agents don't need to be
// added to a hardcoded list.
func GetAllConfiguredModels(accountId string) ([]ModelConfig, error) {
	var models []ModelConfig
	seen := make(map[string]bool)

	// Dedupe key includes the source id so the same (provider, model) at
	// multiple configured slots (e.g. HF Qwen on env:tier:reasoning AND
	// env:tier:summary with different endpoints) shows up as distinct rows.
	// Both primary and fallback rows get a ConfigSource + ConfigName so
	// clients can pin either — fallback rows carry IsFallback=true so the UI
	// can render them differently and their ConfigName gets a "(fallback)"
	// suffix for extra clarity.
	//
	// Source-id derivation from the internal label (":"-separated, see the
	// ENV emission block below for the shape): strip trailing ":fallback"
	// and validate the remainder is a legal source-id. integrationName is the
	// owning integration's name for db:* rows and "" for env:* rows.
	addModel := func(provider, model, source string, isFallback bool, integrationName string) {
		if provider == "" || model == "" {
			return
		}
		key := fmt.Sprintf("%s:%s:%s", provider, model, source)
		if seen[key] {
			return
		}
		configSource := deriveConfigSource(source, isFallback)
		row := ModelConfig{
			Provider:     provider,
			Model:        model,
			Source:       source,
			ConfigSource: configSource,
			IsFallback:   isFallback,
		}
		if configSource != "" {
			name := ConfigNameFor(configSource, integrationName)
			if isFallback {
				name += " (fallback)"
			}
			row.ConfigName = name
		}
		models = append(models, row)
		seen[key] = true
	}

	addFallbacks := func(provider, fallbackStr, source string, integrationName string) {
		if provider == "" || fallbackStr == "" {
			return
		}
		for _, model := range strings.Split(fallbackStr, ",") {
			addModel(provider, strings.TrimSpace(model), source, true, integrationName)
		}
	}

	tiers := []string{"reasoning", "retrieval", "summary"}

	// ─── ENV ──────────────────────────────────────────────────────────────────
	//
	// Emitted labels use ":" as separator (matches the wire-format source-id
	// schema — see NBQueryConfig.LlmConfigSource) with a trailing ":fallback"
	// suffix on fallback rows. This makes deriveConfigSource a single strip
	// instead of an ordered prefix-match ladder (which was subtle to reason
	// about and one bad rename away from an ambiguity bug). Names — tier
	// (reasoning/retrieval/summary) and agent (snake_case) — can never contain
	// ":", so the split is unambiguous.
	//
	// Fallback-skip guard: only emit fallback rows when the slot's primary
	// (provider + model) is actually populated. A slot with only fallbacks set
	// has no endpoint/api-key to serve them with, and the resolver would error
	// at pin time — so we don't surface those in the picker.

	// ENV global + global fallbacks
	if config.Config.LlmProvider != "" && config.Config.LlmModel != "" {
		addModel(config.Config.LlmProvider, config.Config.LlmModel, "env:global", false, "")
		addFallbacks(config.Config.LlmProvider, config.Config.LlmModelFallbacks, "env:global:fallback", "")
	}

	// ENV tier + tier fallbacks (tier provider falls back to ENV-global)
	for _, tier := range tiers {
		tierProvider := config.Config.GetString(fmt.Sprintf(llmTierProviderFormat, tier), config.Config.LlmProvider)
		tierModel := config.Config.GetString(fmt.Sprintf(llmTierModelFormat, tier), "")
		if tierProvider == "" || tierModel == "" {
			continue
		}
		addModel(tierProvider, tierModel, fmt.Sprintf("env:tier:%s", tier), false, "")
		tierFb := config.Config.GetString(fmt.Sprintf(llmTierModelFallbackFormat, tier), "")
		addFallbacks(tierProvider, tierFb, fmt.Sprintf("env:tier:%s:fallback", tier), "")
	}

	// ENV per-agent — dynamic env-var suffix scan (no hardcoded agent list).
	// Looks for every LLM_PROVIDER_<AGENT> env var, pairs with the matching
	// LLM_MODEL_NAME_<AGENT> and LLM_MODEL_FALLBACKS_<AGENT>. Excludes the
	// LLM_PROVIDER_* credential/config knobs (API_KEY, API_ENDPOINT, REGION,
	// ADAPTER_ID, etc.) so they are not mis-treated as agent names.
	//
	// Exact match OR "<KNOB>_" prefix: the first form rejects
	// "LLM_PROVIDER_API_KEY"; the second rejects per-agent overrides like
	// "LLM_PROVIDER_API_KEY_K8S_DEBUG_REACT". A bare HasPrefix on "API_"
	// would wrongly skip a future agent literally named "api_gateway"
	// (LLM_PROVIDER_API_GATEWAY), so the trailing underscore is required.
	credentialKnobs := []string{
		"API_KEY",
		"API_ENDPOINT",
		"API_VERSION",
		"API_TYPE",
		"REGION",
		"ACCESS_KEY",
		"SECRET_KEY",
		"SESSION_TOKEN",
		"ADAPTER_ID",
		"REQUIRE_ADAPTER_ID",
	}
	const envProviderPrefix = "LLM_PROVIDER_"
	for _, envKV := range os.Environ() {
		eq := strings.IndexByte(envKV, '=')
		if eq < 0 {
			continue
		}
		k, provider := envKV[:eq], envKV[eq+1:]
		if provider == "" || !strings.HasPrefix(k, envProviderPrefix) {
			continue
		}
		upperSuffix := strings.TrimPrefix(k, envProviderPrefix)
		if upperSuffix == "" {
			continue
		}
		isCredentialKnob := false
		for _, knob := range credentialKnobs {
			if upperSuffix == knob || strings.HasPrefix(upperSuffix, knob+"_") {
				isCredentialKnob = true
				break
			}
		}
		if isCredentialKnob {
			continue
		}
		agentName := strings.ToLower(upperSuffix)
		model := config.Config.GetString(fmt.Sprintf(llmModelFormat, agentName), "")
		if model == "" {
			continue
		}
		addModel(provider, model, fmt.Sprintf("env:agent:%s", agentName), false, "")
		fb := config.Config.GetString(fmt.Sprintf(llmModelFallbackFormat, agentName), "")
		addFallbacks(provider, fb, fmt.Sprintf("env:agent:%s:fallback", agentName), "")
	}

	// ─── DB ───────────────────────────────────────────────────────────────────
	//
	// Emitted per integration rather than off the merged config map the layered
	// resolver uses: the merge collapses every visible integration into one map
	// and drops the integration id, leaving nothing for a client to pin. Labels
	// embed the uuid — db:<uuid>[:tier|:agent:<name>][:fallback] — which is what
	// resolveFromPinnedSource looks up.

	integrations, err := getLLMIntegrationsForAccount(nil, accountId)
	if err != nil {
		// The picker is still useful with ENV rows alone, so degrade rather than
		// fail the whole list.
		slog.Warn("GetAllConfiguredModels: unable to load LLM integrations; listing ENV slots only",
			"error", err, "accountId", accountId)
		return models, nil
	}

	for _, integ := range integrations {
		cfg, name := integ.Config, integ.Name
		integrationProvider := cfg["llm_provider"]

		// Integration global + global fallbacks
		if integrationProvider != "" && cfg["llm_model_name"] != "" {
			addModel(integrationProvider, cfg["llm_model_name"], fmt.Sprintf("db:%s", integ.Id), false, name)
			addFallbacks(integrationProvider, cfg["llm_model_fallbacks"], fmt.Sprintf("db:%s:fallback", integ.Id), name)
		}

		// Tier + tier fallbacks (tier provider falls back to the integration's global)
		for _, tier := range tiers {
			tierProvider := cfg[fmt.Sprintf(llmTierProviderFormat, tier)]
			if tierProvider == "" {
				tierProvider = integrationProvider
			}
			tierModel := cfg[fmt.Sprintf(llmTierModelFormat, tier)]
			if tierProvider == "" || tierModel == "" {
				continue
			}
			addModel(tierProvider, tierModel, fmt.Sprintf("db:%s:tier:%s", integ.Id, tier), false, name)
			addFallbacks(tierProvider, cfg[fmt.Sprintf(llmTierModelFallbackFormat, tier)],
				fmt.Sprintf("db:%s:tier:%s:fallback", integ.Id, tier), name)
		}

		// Per-agent — scan llm_provider_<agent> keys, excluding credential
		// suffixes that share the same prefix.
		agentNames := make([]string, 0, len(cfg))
		for key := range cfg {
			if !strings.HasPrefix(key, "llm_provider_") {
				continue
			}
			if strings.HasPrefix(key, "llm_provider_api_") ||
				strings.HasPrefix(key, "llm_provider_region") ||
				strings.HasPrefix(key, "llm_provider_require") ||
				strings.HasPrefix(key, "llm_provider_adapter") {
				continue
			}
			if agentName := strings.TrimPrefix(key, "llm_provider_"); agentName != "" {
				agentNames = append(agentNames, agentName)
			}
		}
		// Sorted so row order doesn't depend on map iteration order.
		sort.Strings(agentNames)
		for _, agentName := range agentNames {
			model := cfg[fmt.Sprintf(llmModelFormat, agentName)]
			if model == "" {
				continue
			}
			provider := cfg[fmt.Sprintf(llmProviderFormat, agentName)]
			addModel(provider, model, fmt.Sprintf("db:%s:agent:%s", integ.Id, agentName), false, name)
			addFallbacks(provider, cfg[fmt.Sprintf(llmModelFallbackFormat, agentName)],
				fmt.Sprintf("db:%s:agent:%s:fallback", integ.Id, agentName), name)
		}
	}

	// Returned un-collapsed: this is the flat per-slot view. Callers wanting
	// unique destinations use GetConfiguredCredentials, which owns that logic.
	return models, nil
}

// IsOpenAIModelWithoutStopSupport checks if the model doesn't support the 'stop' parameter
// OpenAI's reasoning models (o1, o3) and newer GPT-5 series don't support stop words
func IsOpenAIModelWithoutStopSupport(provider, model string) bool {
	if provider != "openai" {
		return false
	}

	modelLower := strings.ToLower(strings.TrimSpace(model))

	// Check for o1 and o3 reasoning model families
	// o1-preview, o1-mini, o1, o3-mini, o3, etc.
	if strings.HasPrefix(modelLower, "o1") ||
		strings.HasPrefix(modelLower, "o3") ||
		strings.Contains(modelLower, "o1-") ||
		strings.Contains(modelLower, "o3-") {
		return true
	}

	// Check for GPT-5 series models
	// gpt-5, gpt-5-mini, gpt-5-turbo, etc.
	if strings.HasPrefix(modelLower, "gpt-5") ||
		strings.Contains(modelLower, "gpt-5-") {
		return true
	}

	return false
}

// ─── Pinned-source resolution ──────────────────────────────────────────────
//
// A pinned source is a request-level override that skips the normal layered
// walk and reads every LLM credential (provider, model, endpoint, api-key,
// api-type, api-version, region) from exactly one configured slot. Consumers
// pick it via NBQueryConfig.LlmConfigSource; the value flows into the
// request context as ContextKeyLlmConfigSourceOverride and every sub-agent
// call in the request tree inherits it via context propagation.
//
// Source-id schema: {layer}:{scope}[:{name}]
//   env:global                    → LLM_PROVIDER / LLM_MODEL_NAME / LLM_PROVIDER_API_*
//   env:tier:<t>                  → LLM_TIER_*_<T>   (t = reasoning|retrieval|summary)
//   env:agent:<agent_name>        → LLM_*_<AGENT>
//   env:all                       → the whole ENV config; each call reads its own tier
//   db:<integration_uuid>[:tier:<t>|:agent:<a>]  → per-tenant integration row
//   db:<integration_uuid>:all     → the whole integration; each call reads its own tier
//
// The ":all" scope is the one pin that is NOT a single slot: it names a config
// and lets the call's tier pick the slot inside it (see slotForWholeConfig).
// Every other scope resolves to exactly one slot regardless of tier.
//
// db ids name one enabled LLM integration visible to the request's account.
// Ids the account can't see are rejected, so the pin can never reach another
// tenant's credentials — see integrationConfigForPin.

// tierPinFor returns the credential and model a tier-tagged call should use
// when the active tier's pick names its own credential, or ("", "") otherwise.
//
// In By-task mode each task is a complete (credential, model) choice, so its
// credential must beat the conversation-wide pin — otherwise a blanket pin made
// on an earlier turn silently serves every tier from one slot's primary model.
// Picks made before per-tier credentials existed carry no ConfigSource; those
// return "" so the caller falls back to the conversation pin, preserving
// behaviour for stored conversations.
//
// Reads the request context first, then the conversation row — the same two
// sources, in the same order, that the layered walk uses for tier overrides, so
// background work that lost its context still resolves identically.
func tierPinFor(ctx *security.RequestContext, conversationId string, tier ModelTier) (string, string) {
	if tier == "" {
		return "", ""
	}
	if ctx != nil && ctx.GetContext() != nil {
		if v, ok := ctx.GetContext().Value(ContextKeyLlmTierModelOverrides).(ConversationTierOverrides); ok {
			if pick, found := v.Get(string(tier)); found && pick.ConfigSource != "" {
				return pick.ConfigSource, pick.Model
			}
		}
	}
	if conversationId != "" {
		if _, _, tierOverrides, _, err := GetConversationOverride(conversationId); err == nil {
			if pick, found := tierOverrides.Get(string(tier)); found && pick.ConfigSource != "" {
				return pick.ConfigSource, pick.Model
			}
		}
	}
	return "", ""
}

// pinnedConfigSourceFromContext returns the pinned source id stamped into ctx,
// or "" if none. Used by ResolveLLMConfig to short-circuit its layered walk.
func pinnedConfigSourceFromContext(ctx *security.RequestContext) string {
	if ctx == nil {
		return ""
	}
	goCtx := ctx.GetContext()
	if goCtx == nil {
		return ""
	}
	v, _ := goCtx.Value(ContextKeyLlmConfigSourceOverride).(string)
	return v
}

// parsedConfigSource is the decoded form of a source-id string.
type parsedConfigSource struct {
	Layer           string // "env" | "db"
	Scope           string // "global" | "tier" | "agent"
	Name            string // tier name or agent name (empty for global)
	IntegrationUuid string // db only
}

// parseConfigSourceId splits a source-id string into (layer, scope, name,
// integration_uuid). Returns an error if malformed. The caller must still
// validate that the parsed slot actually has data — parsing succeeds even for
// slots that reference a tier/agent that isn't configured.
func parseConfigSourceId(sourceId string) (*parsedConfigSource, error) {
	// Trim before splitting: the id is client-supplied, and stray whitespace
	// would otherwise fail as an unknown layer or an unavailable integration —
	// errors that point at the wrong thing. Messages keep the raw value.
	parts := strings.Split(strings.TrimSpace(sourceId), ":")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid llm_config_source %q: expected {layer}:{scope}[:{name}]", sourceId)
	}
	p := &parsedConfigSource{Layer: parts[0]}
	switch p.Layer {
	case "env":
		// env:global | env:all | env:tier:<t> | env:agent:<a>
		p.Scope = parts[1]
		switch p.Scope {
		case "global", "all":
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid llm_config_source %q: env:%s takes no name", sourceId, p.Scope)
			}
		case "tier", "agent":
			if len(parts) != 3 || parts[2] == "" {
				return nil, fmt.Errorf("invalid llm_config_source %q: env:%s requires a name", sourceId, p.Scope)
			}
			p.Name = parts[2]
		default:
			return nil, fmt.Errorf("invalid llm_config_source %q: unknown env scope %q", sourceId, p.Scope)
		}
	case "db":
		// db:<uuid> | db:<uuid>:all | db:<uuid>:tier:<t> | db:<uuid>:agent:<a>
		if len(parts) < 2 || parts[1] == "" {
			return nil, fmt.Errorf("invalid llm_config_source %q: db requires an integration uuid", sourceId)
		}
		p.IntegrationUuid = parts[1]
		if len(parts) == 2 {
			p.Scope = "global"
		} else if len(parts) == 3 && parts[2] == "all" {
			p.Scope = "all"
		} else if len(parts) == 4 && (parts[2] == "tier" || parts[2] == "agent") && parts[3] != "" {
			p.Scope = parts[2]
			p.Name = parts[3]
		} else {
			return nil, fmt.Errorf("invalid llm_config_source %q: db shape is db:<uuid>[:all|:tier:<name>|:agent:<name>]", sourceId)
		}
	default:
		return nil, fmt.Errorf("invalid llm_config_source %q: unknown layer %q (expected env|db)", sourceId, p.Layer)
	}
	return p, nil
}

// slotForWholeConfig turns an ":all" pin — "use this whole config" — into the
// concrete slot the current call should read: the call's own tier when that
// tier defines a model, otherwise the config's global slot.
//
// The global fallback is what makes ":all" safe on a config that defines no
// tiers: it resolves exactly like a plain config pin instead of reading an
// empty tier slot and sending a request with no model. Credentials the chosen
// tier leaves unset are still filled from the global slot by the readers'
// inheritSlotDefaults, so a config with per-tier models but one shared api key
// resolves correctly.
//
// Returns a copy — the caller's parsed source must keep naming ":all" so
// PinnedConfigSource, cache keys and audit logs still record what was pinned
// rather than the slot it happened to land on for this call.
func slotForWholeConfig(p *parsedConfigSource, tier ModelTier, tierDefinesModel func(string) bool) *parsedConfigSource {
	out := *p
	if tier != "" && tierDefinesModel(string(tier)) {
		out.Scope, out.Name = "tier", string(tier)
		return &out
	}
	out.Scope, out.Name = "global", ""
	return &out
}

// resolveFromPinnedSource reads all credentials from the specific slot named by
// sourceId. Returns a fully-populated LLMConfigResolution with Pinned* fields
// set so downstream resolvers short-circuit. Errors when the slot is malformed,
// missing required keys, or (for db) references a different tenant's config.
//
// Provider/model reconciliation with the request:
//   - The slot's PRIMARY (provider, model) is the default effective pair.
//   - When the request also sent LlmProvider / LlmModelName via ctx (stamped as
//     ContextKeyLlmProviderOverride / ContextKeyLlmModelOverride), we validate
//     they're consistent with the slot: provider must match, and model must be
//     either the primary OR in the slot's fallback list. A model in the fallback
//     list is honoured (returned as res.Model) so clients can pin the slot's
//     endpoint / api-key while picking a fallback model. Mismatches return an
//     error — no silent slot-primary substitution, no wrong endpoint drift.
func resolveFromPinnedSource(ctx *security.RequestContext, sourceId, accountId, agentName string, tier ModelTier) (*LLMConfigResolution, error) {
	parsed, err := parseConfigSourceId(sourceId)
	if err != nil {
		return nil, err
	}

	res := &LLMConfigResolution{
		Source:             "pinned:" + sourceId,
		IsOverridden:       true,
		PinnedConfigSource: sourceId,
		Hierarchy: []LLMConfigLayer{{
			Level:  "pinned:" + sourceId,
			Active: true,
		}},
	}

	// Recorded before the switch rewrites Scope to the concrete slot.
	wholeConfig := parsed.Scope == "all"

	var slotFallbacks []string
	var pinConfig map[string]string // db integration config (nil for env pins)
	switch parsed.Layer {
	case "env":
		var err error
		if parsed.Scope == "all" {
			parsed = slotForWholeConfig(parsed, tier, func(t string) bool {
				return config.Config.GetString(fmt.Sprintf(llmTierModelFormat, t), "") != ""
			})
		}
		if slotFallbacks, err = readEnvSlotInto(res, parsed); err != nil {
			return nil, err
		}
	case "db":
		var err error
		pinConfig, err = integrationConfigForPin(ctx, accountId, parsed.IntegrationUuid)
		if err != nil {
			return nil, err
		}
		if parsed.Scope == "all" {
			parsed = slotForWholeConfig(parsed, tier, func(t string) bool {
				return pinConfig[fmt.Sprintf(llmTierModelFormat, t)] != ""
			})
		}
		if slotFallbacks, err = readDbSlotInto(res, pinConfig, parsed); err != nil {
			return nil, err
		}
	}

	// Reconcile with request-level provider/model overrides.
	reqProvider := contextString(ctx, ContextKeyLlmProviderOverride)
	reqModel := contextString(ctx, ContextKeyLlmModelOverride)
	// A whole-config pin means "let this config's tiers choose the model", so a
	// model sent alongside it is a contradiction — and one that would resolve
	// differently per tier, passing validation on some calls and failing on
	// others. Reject it outright rather than honour one of the two intents.
	if wholeConfig && (reqProvider != "" || reqModel != "") {
		return nil, fmt.Errorf("llm_config_source %q selects a whole config; llm_provider/llm_model_name must not be sent with it (got %q/%q)",
			sourceId, reqProvider, reqModel)
	}
	if reqProvider != "" || reqModel != "" {
		// Half-set is ambiguous — either both or neither.
		if reqProvider == "" || reqModel == "" {
			return nil, fmt.Errorf("llm_config_source %q: request has llm_provider=%q and llm_model_name=%q — either both or neither must be set alongside a pinned source",
				sourceId, reqProvider, reqModel)
		}
		// Trim before comparing: dev and prod integration rows carry provider
		// values with trailing whitespace ("googleai "), and credentialIdentity
		// already trims when grouping — so an untrimmed compare here would
		// reject a request for the very credential the picker offered, with an
		// error naming two providers that look identical. Compared via locals
		// so the message keeps the raw values (%q makes the whitespace visible).
		if !strings.EqualFold(strings.TrimSpace(reqProvider), strings.TrimSpace(res.Provider)) {
			return nil, fmt.Errorf("llm_config_source %q pins provider %q; request sent llm_provider %q — refresh model list and re-pick",
				sourceId, res.Provider, reqProvider)
		}
		if reqModel != res.Model {
			if !containsString(slotFallbacks, reqModel) {
				accepted := append([]string{res.Model}, slotFallbacks...)
				return nil, fmt.Errorf("llm_config_source %q cannot serve model %q (accepted: primary=%q, fallbacks=%v)",
					sourceId, reqModel, res.Model, accepted[1:])
			}
			// Fallback picked — use the request's model with the slot's endpoint.
			res.Model = reqModel
		}
	}

	// Context window for the pinned deployment (db pins: llm_model_context_size;
	// env pins: config.Config sizes). 0 → model-default fallback, as in the layered path.
	if mc := resolveModelContextMap(pinConfig)[res.Model]; mc > 0 {
		res.MaxContext = mc
	}

	// Populate active layer's provider/model for display.
	res.Hierarchy[0].Provider = res.Provider
	res.Hierarchy[0].Model = res.Model
	slog.Debug("resolved pinned llm config source",
		"source", sourceId,
		"provider", res.Provider,
		"model", res.Model,
		"agentName", agentName,
		"tier", string(tier),
		"accountId", accountId)
	return res, nil
}

// integrationConfigForPin returns the config map of the integration a db:<uuid>
// source names, or an error if the account can't see it. Tenant isolation comes
// from getLLMIntegrationsForAccount's visibility filter — an integration
// belonging to another tenant is never in the returned list, so an unrecognised
// uuid is rejected here rather than silently resolving to someone else's
// credentials.
func integrationConfigForPin(ctx *security.RequestContext, accountId, integrationUuid string) (map[string]string, error) {
	if accountId == "" {
		return nil, fmt.Errorf("llm_config_source db:%s requires an account context", integrationUuid)
	}
	integrations, err := getLLMIntegrationsForAccount(ctx, accountId)
	if err != nil {
		return nil, fmt.Errorf("llm_config_source db:%s: unable to load LLM integrations: %w", integrationUuid, err)
	}
	for _, integ := range integrations {
		if integ.Id == integrationUuid {
			// Safe to hand out: getLLMIntegrationsForAccount already returns a
			// deep copy, so this map is not the cached one.
			return integ.Config, nil
		}
	}
	return nil, fmt.Errorf("llm_config_source db:%s is not an LLM integration available to this account — refresh model list and re-pick", integrationUuid)
}

// contextString extracts a string-typed value from ctx by key; returns "" for a
// nil ctx or missing/mistyped key. Kept private because it's specific to the
// LLMContextKey space we control here.
func contextString(ctx *security.RequestContext, key LLMContextKey) string {
	if ctx == nil {
		return ""
	}
	goCtx := ctx.GetContext()
	if goCtx == nil {
		return ""
	}
	v, _ := goCtx.Value(key).(string)
	return v
}

// containsString is a small helper avoiding a slices import at this file's use
// site (some legacy files here still target the pre-1.21 slices package).
func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// ModelSupportsTemperature checks if the model supports the 'temperature' parameter.
// Anthropic reasoning models (claude-sonnet-5, claude-opus-5, claude-5 series) and OpenAI
// reasoning model families (o1, o3, gpt-5) reject explicit / non-default temperature on the wire.
func ModelSupportsTemperature(provider, model string) bool {
	modelLower := strings.ToLower(strings.TrimSpace(model))
	pLower := strings.ToLower(strings.TrimSpace(provider))

	// Check Anthropic reasoning models
	if pLower == "anthropic" || strings.Contains(modelLower, "claude") {
		if strings.Contains(modelLower, "claude-sonnet-5") ||
			strings.Contains(modelLower, "claude-opus-5") ||
			strings.Contains(modelLower, "claude-5") {
			return false
		}
	}

	// Check OpenAI reasoning models across any provider, including namespaced proxy/deployment IDs.
	if isOpenAIReasoningModel(modelLower) {
		return false
	}

	return true
}

func isOpenAIReasoningModel(model string) bool {
	for _, family := range []string{"o1", "o3", "gpt-5"} {
		for offset := 0; offset < len(model); {
			index := strings.Index(model[offset:], family)
			if index < 0 {
				break
			}
			index += offset
			beforeFamily := index == 0 || isModelNamespaceSeparator(model[index-1])
			afterIndex := index + len(family)
			afterFamily := afterIndex == len(model) || isModelNamespaceSeparator(model[afterIndex])
			if beforeFamily && afterFamily {
				return true
			}
			offset = index + len(family)
		}
	}
	return false
}

// readEnvSlotInto populates res with credentials read from an env: slot and
// returns the slot's fallback model list (empty if none configured). The caller
// has already parsed sourceId; this function only touches ENV.
func readEnvSlotInto(res *LLMConfigResolution, p *parsedConfigSource) ([]string, error) {
	var fallbackStr string
	switch p.Scope {
	case "global":
		res.Provider = config.Config.LlmProvider
		res.Model = config.Config.LlmModel
		res.PinnedEndpoint = config.Config.LlmProviderApiEndpoint
		res.PinnedApiKey = config.Config.LlmProviderApiKey
		res.PinnedApiType = config.Config.LlmProviderApiType
		res.PinnedApiVersion = config.Config.LlmProviderApiVersion
		res.PinnedRegion = config.Config.LlmProviderRegion
		res.PinnedAccessKey = config.Config.LlmProviderAccessKey
		res.PinnedSecretKey = config.Config.LlmProviderSecretKey
		res.PinnedSessionToken = config.Config.LlmProviderSessionToken
		fallbackStr = config.Config.LlmModelFallbacks
	case "tier":
		res.Provider = config.Config.GetString(fmt.Sprintf(llmTierProviderFormat, p.Name), "")
		res.Model = config.Config.GetString(fmt.Sprintf(llmTierModelFormat, p.Name), "")
		res.PinnedEndpoint = config.Config.GetString(fmt.Sprintf(llmTierApiEndpointFormat, p.Name), "")
		res.PinnedApiKey = config.Config.GetString(fmt.Sprintf(llmTierApiKeyFormat, p.Name), "")
		res.PinnedApiType = config.Config.GetString(fmt.Sprintf(llmTierApiTypeFormat, p.Name), "")
		res.PinnedApiVersion = config.Config.GetString(fmt.Sprintf(llmTierApiVersionFormat, p.Name), "")
		res.PinnedRegion = config.Config.GetString(fmt.Sprintf(llmTierRegionFormat, p.Name), "")
		res.PinnedAccessKey = config.Config.GetString(fmt.Sprintf(llmTierAccessKeyFormat, p.Name), "")
		res.PinnedSecretKey = config.Config.GetString(fmt.Sprintf(llmTierSecretKeyFormat, p.Name), "")
		res.PinnedSessionToken = config.Config.GetString(fmt.Sprintf(llmTierSessionTokenFormat, p.Name), "")
		fallbackStr = config.Config.GetString(fmt.Sprintf(llmTierModelFallbackFormat, p.Name), "")
	case "agent":
		res.Provider = config.Config.GetString(fmt.Sprintf(llmProviderFormat, p.Name), "")
		res.Model = config.Config.GetString(fmt.Sprintf(llmModelFormat, p.Name), "")
		res.PinnedEndpoint = config.Config.GetString(fmt.Sprintf(llmProviderApiEndpointFormat, p.Name), "")
		res.PinnedApiKey = config.Config.GetString(fmt.Sprintf(llmProviderApiKeyFormat, p.Name), "")
		res.PinnedApiType = config.Config.GetString(fmt.Sprintf(llmProviderApiTypeFormat, p.Name), "")
		res.PinnedApiVersion = config.Config.GetString(fmt.Sprintf(llmProviderApiVersionFormat, p.Name), "")
		res.PinnedRegion = config.Config.GetString(fmt.Sprintf(llmProviderRegionFormat, p.Name), "")
		res.PinnedAccessKey = config.Config.GetString(fmt.Sprintf(llmProviderAccessKeyFormat, p.Name), "")
		res.PinnedSecretKey = config.Config.GetString(fmt.Sprintf(llmProviderSecretKeyFormat, p.Name), "")
		res.PinnedSessionToken = config.Config.GetString(fmt.Sprintf(llmProviderSessionTokenFormat, p.Name), "")
		res.PinnedAdapterId = config.Config.GetString(fmt.Sprintf(llmModelAdapterFormat, p.Name), "")
		fallbackStr = config.Config.GetString(fmt.Sprintf(llmModelFallbackFormat, p.Name), "")
	default:
		return nil, fmt.Errorf("readEnvSlotInto: unknown scope %q", p.Scope)
	}

	// Fill anything this slot doesn't define from ENV-global — same rule as the
	// db branch below, and the same rule the layered resolver applies when the
	// request isn't pinned. Values the slot DOES define always win, so the
	// two-HF-endpoints case (each tier naming its own endpoint) is unaffected:
	// inheritance only fills gaps, it never overrides.
	if p.Scope != "global" {
		inheritSlotDefaults(res, slotDefaults{
			provider: config.Config.LlmProvider, endpoint: config.Config.LlmProviderApiEndpoint, apiKey: config.Config.LlmProviderApiKey,
			apiType: config.Config.LlmProviderApiType, apiVersion: config.Config.LlmProviderApiVersion, region: config.Config.LlmProviderRegion,
			accessKey: config.Config.LlmProviderAccessKey, secretKey: config.Config.LlmProviderSecretKey, sessionToken: config.Config.LlmProviderSessionToken,
		})
	}

	return finishSlotRead(res, p, fallbackStr)
}

// readDbSlotInto is readEnvSlotInto's db twin: it populates res from a single
// tenant-configured LLM integration's config map. integration_config_values
// stores the same key names as the ENV formats, lowercased, so the slot shapes
// line up one-for-one with the env branch above.
func readDbSlotInto(res *LLMConfigResolution, cfg map[string]string, p *parsedConfigSource) ([]string, error) {
	var fallbackStr string
	switch p.Scope {
	case "global":
		res.Provider = cfg["llm_provider"]
		res.Model = cfg["llm_model_name"]
		res.PinnedEndpoint = cfg["llm_provider_api_endpoint"]
		res.PinnedApiKey = cfg["llm_provider_api_key"]
		res.PinnedApiType = cfg["llm_provider_api_type"]
		res.PinnedApiVersion = cfg["llm_provider_api_version"]
		res.PinnedRegion = cfg["llm_provider_region"]
		res.PinnedAccessKey = cfg["llm_provider_access_key"]
		res.PinnedSecretKey = cfg["llm_provider_secret_key"]
		res.PinnedSessionToken = cfg["llm_provider_session_token"]
		fallbackStr = cfg["llm_model_fallbacks"]
	case "tier":
		res.Provider = cfg[fmt.Sprintf(llmTierProviderFormat, p.Name)]
		res.Model = cfg[fmt.Sprintf(llmTierModelFormat, p.Name)]
		res.PinnedEndpoint = cfg[fmt.Sprintf(llmTierApiEndpointFormat, p.Name)]
		res.PinnedApiKey = cfg[fmt.Sprintf(llmTierApiKeyFormat, p.Name)]
		res.PinnedApiType = cfg[fmt.Sprintf(llmTierApiTypeFormat, p.Name)]
		res.PinnedApiVersion = cfg[fmt.Sprintf(llmTierApiVersionFormat, p.Name)]
		res.PinnedRegion = cfg[fmt.Sprintf(llmTierRegionFormat, p.Name)]
		res.PinnedAccessKey = cfg[fmt.Sprintf(llmTierAccessKeyFormat, p.Name)]
		res.PinnedSecretKey = cfg[fmt.Sprintf(llmTierSecretKeyFormat, p.Name)]
		res.PinnedSessionToken = cfg[fmt.Sprintf(llmTierSessionTokenFormat, p.Name)]
		fallbackStr = cfg[fmt.Sprintf(llmTierModelFallbackFormat, p.Name)]
	case "agent":
		res.Provider = cfg[fmt.Sprintf(llmProviderFormat, p.Name)]
		res.Model = cfg[fmt.Sprintf(llmModelFormat, p.Name)]
		res.PinnedEndpoint = cfg[fmt.Sprintf(llmProviderApiEndpointFormat, p.Name)]
		res.PinnedApiKey = cfg[fmt.Sprintf(llmProviderApiKeyFormat, p.Name)]
		res.PinnedApiType = cfg[fmt.Sprintf(llmProviderApiTypeFormat, p.Name)]
		res.PinnedApiVersion = cfg[fmt.Sprintf(llmProviderApiVersionFormat, p.Name)]
		res.PinnedRegion = cfg[fmt.Sprintf(llmProviderRegionFormat, p.Name)]
		res.PinnedAccessKey = cfg[fmt.Sprintf(llmProviderAccessKeyFormat, p.Name)]
		res.PinnedSecretKey = cfg[fmt.Sprintf(llmProviderSecretKeyFormat, p.Name)]
		res.PinnedSessionToken = cfg[fmt.Sprintf(llmProviderSessionTokenFormat, p.Name)]
		res.PinnedAdapterId = cfg[fmt.Sprintf(llmModelAdapterFormat, p.Name)]
		fallbackStr = cfg[fmt.Sprintf(llmModelFallbackFormat, p.Name)]
	default:
		return nil, fmt.Errorf("readDbSlotInto: unknown scope %q", p.Scope)
	}

	// OAuth + extra headers (#36556): tier slots may override, everything else
	// inherits the integration's global values. Per-agent OAuth keys do not
	// exist in v1, so the agent scope inherits global too.
	if p.Scope == "tier" {
		res.PinnedAuthType = cfg["llm_tier_auth_type_"+p.Name]
		res.PinnedOAuthTokenURL = cfg["llm_tier_oauth_token_url_"+p.Name]
		res.PinnedOAuthClientID = cfg["llm_tier_oauth_client_id_"+p.Name]
		res.PinnedOAuthClientSecret = cfg["llm_tier_oauth_client_secret_"+p.Name]
		res.PinnedOAuthScope = cfg["llm_tier_oauth_scope_"+p.Name]
		res.PinnedExtraHeaders = cfg["llm_tier_extra_headers_"+p.Name]
	}
	// Per-field global fallback, matching inheritSlotDefaults and the
	// non-pinned resolver: a tier may override just the client id/secret
	// while sharing the integration's token URL (one issuer, one client per
	// tier), so each field falls back independently.
	if res.PinnedAuthType == "" {
		res.PinnedAuthType = cfg[llmAuthTypeKey]
	}
	if res.PinnedOAuthTokenURL == "" {
		res.PinnedOAuthTokenURL = cfg[llmOAuthTokenURLKey]
	}
	if res.PinnedOAuthClientID == "" {
		res.PinnedOAuthClientID = cfg[llmOAuthClientIDKey]
	}
	if res.PinnedOAuthClientSecret == "" {
		res.PinnedOAuthClientSecret = cfg[llmOAuthClientSecretKey]
	}
	if res.PinnedOAuthScope == "" {
		res.PinnedOAuthScope = cfg[llmOAuthScopeKey]
	}
	if res.PinnedExtraHeaders == "" {
		res.PinnedExtraHeaders = cfg[llmExtraHeadersKey]
	}

	// Fill anything this slot doesn't define from the integration's global slot.
	// A tenant integration normally carries one api key and per-tier models, so
	// without this a pinned tier slot resolves with an EMPTY key and the request
	// fails — pinning would behave differently from not pinning, which is the
	// opposite of the point. Values the slot DOES define always win, so a slot
	// with its own endpoint still overrides.
	if p.Scope != "global" {
		inheritSlotDefaults(res, slotDefaults{
			provider: cfg["llm_provider"], endpoint: cfg["llm_provider_api_endpoint"], apiKey: cfg["llm_provider_api_key"],
			apiType: cfg["llm_provider_api_type"], apiVersion: cfg["llm_provider_api_version"], region: cfg["llm_provider_region"],
			accessKey: cfg["llm_provider_access_key"], secretKey: cfg["llm_provider_secret_key"], sessionToken: cfg["llm_provider_session_token"],
		})
	}

	return finishSlotRead(res, p, fallbackStr)
}

// inheritSlotDefaults fills each empty credential on res from the layer's global
// slot. Per-field, so a slot that overrides only its endpoint keeps the shared
// key rather than losing it.
func inheritSlotDefaults(res *LLMConfigResolution, g slotDefaults) {
	if res.Provider == "" {
		res.Provider = g.provider
	}
	// Credentials are only interchangeable within one provider. A slot that
	// names a different provider than the layer's global (openai global with an
	// azure reasoning tier, say) must not gap-fill from it: it would pair an
	// azure model with an openai endpoint, or — worse, when the tier sets no key
	// — send the request with the global provider's api key. That is the leak in
	// #35000 / #34834, on the pinned path. Leaving the field empty makes the
	// misconfiguration fail loudly instead.
	if !strings.EqualFold(strings.TrimSpace(res.Provider), strings.TrimSpace(g.provider)) {
		return
	}
	if res.PinnedEndpoint == "" {
		res.PinnedEndpoint = g.endpoint
	}
	if res.PinnedApiKey == "" {
		res.PinnedApiKey = g.apiKey
	}
	if res.PinnedApiType == "" {
		res.PinnedApiType = g.apiType
	}
	if res.PinnedApiVersion == "" {
		res.PinnedApiVersion = g.apiVersion
	}
	if res.PinnedRegion == "" {
		res.PinnedRegion = g.region
	}
	if res.PinnedAccessKey == "" {
		res.PinnedAccessKey = g.accessKey
	}
	if res.PinnedSecretKey == "" {
		res.PinnedSecretKey = g.secretKey
	}
	if res.PinnedSessionToken == "" {
		res.PinnedSessionToken = g.sessionToken
	}
}

// slotDefaults is a layer's global credentials, used to fill gaps in a
// tier/agent slot. Grouped into a struct because passing nine positional
// strings is how you end up silently swapping the secret and the session token.
type slotDefaults struct {
	provider, endpoint, apiKey, apiType, apiVersion, region string
	accessKey, secretKey, sessionToken                      string
}

// finishSlotRead validates that a slot read produced a servable (provider,
// model) pair and splits its comma-separated fallback list. Shared by the env
// and db readers so both reject empty slots identically.
func finishSlotRead(res *LLMConfigResolution, p *parsedConfigSource, fallbackStr string) ([]string, error) {
	if res.Provider == "" {
		return nil, fmt.Errorf("pinned llm_config_source has no provider set for %s:%s:%s", p.Layer, p.Scope, p.Name)
	}
	if res.Model == "" {
		return nil, fmt.Errorf("pinned llm_config_source has no model set for %s:%s:%s", p.Layer, p.Scope, p.Name)
	}

	var fallbacks []string
	for _, m := range strings.Split(fallbackStr, ",") {
		if m = strings.TrimSpace(m); m != "" {
			fallbacks = append(fallbacks, m)
		}
	}
	return fallbacks, nil
}

// deriveConfigSource maps the internal per-slot source label used inside
// GetAllConfiguredModels to the stable wire-format source id that clients
// send back as NBQueryConfig.LlmConfigSource.
//
// Both primary and fallback rows resolve to the SAME source id — because a
// fallback shares its parent slot's endpoint/api-key. When the user picks a
// fallback row from the picker, the request sends the parent slot as
// LlmConfigSource + the fallback's model as LlmModelName, and
// resolveFromPinnedSource validates the combo (model must be in the slot's
// fallback list) and uses the fallback model with the parent's endpoint.
//
// The internal labels are ":"-separated by design: names ("reasoning",
// "soul_consolidate", etc.) cannot contain ":", so the parser splits
// unambiguously. Fallback rows carry a trailing ":fallback" suffix that we
// strip ONLY when the caller tells us the row is a fallback — trusting the
// label alone breaks for an agent literally named "fallback" (its primary
// label "env:agent:fallback" would then be indistinguishable from the strip
// output of the fallback of some other slot). Since addModel already has
// isFallback in scope, threading it here costs nothing.
func deriveConfigSource(source string, isFallback bool) string {
	id := source
	if isFallback {
		id = strings.TrimSuffix(id, ":fallback")
	}
	if _, err := parseConfigSourceId(id); err != nil {
		return ""
	}
	return id
}

// LLMCredential is one distinct place requests can be sent — a unique
// combination of provider and every field that decides routing and auth. The
// models reachable through it are attached; the slots that configure it are
// listed so the UI can name it in the user's own terms.
type LLMCredential struct {
	// Id is a stable non-reversible fingerprint. Safe to render, but it means
	// nothing to a human — Name is what the UI shows.
	Id string `json:"id"`
	// Name is how the user configured it: "System default", "piyush-llm",
	// "piyush-llm · Summary tier".
	Name     string `json:"name"`
	Provider string `json:"provider"`
	// ConfigSource is the slot a pin should use for this credential.
	ConfigSource string `json:"llm_config_source"`
	// Sources lists every slot that resolves here, including ConfigSource. More
	// than one means several configured slots share these credentials.
	Sources []MergedSlot `json:"sources,omitempty"`
	// Models reachable through this credential, in configuration order.
	Models []CredentialModel `json:"models"`
}

// CredentialModel is one model reachable through a credential.
//
// Deliberately carries no is_fallback flag. Whether a model was configured as a
// slot's primary or as one of its fallbacks changes nothing about picking it —
// resolveFromPinnedSource serves either through the same endpoint — so it is
// configuration provenance, not something the user acts on. It was also
// unreliable after deduping: a model that is one slot's primary and another
// slot's fallback within the same credential took whichever flag the walk
// happened to see first.
type CredentialModel struct {
	Model string `json:"model"`
}

// buildCredentials collapses the flat slot rows into unique credentials.
//
// Identity is credentialIdentity — provider plus every routing/auth field, and
// explicitly NOT the model, so slots that differ only in which model they name
// fold into one credential carrying both models. That is the whole point: a
// tenant integration with one api key is one place to send requests, however
// many tiers name a model on it.
//
// Rows whose slot cannot be resolved are skipped rather than guessed at; they
// still appear in the flat model list.
func buildCredentials(ctx *security.RequestContext, accountId string, models []ModelConfig) []LLMCredential {
	type acc struct {
		cred      LLMCredential
		seenSlot  map[string]bool
		seenModel map[string]bool
	}
	byId := map[string]*acc{}
	var order []string

	for _, m := range models {
		if m.ConfigSource == "" {
			continue
		}
		res, err := resolveFromPinnedSource(ctx, m.ConfigSource, accountId, "", "")
		if err != nil {
			slog.Debug("buildCredentials: slot did not resolve; omitting from credential list",
				"config_source", m.ConfigSource, "error", err)
			continue
		}
		id := credentialIdentity(res)

		// A fallback row's ConfigName carries a " (fallback)" suffix, but it names
		// the same slot as its primary — so strip it rather than letting a
		// credential end up called "piyush-llm (fallback)" depending on which
		// row the walk reached first.
		name := strings.TrimSuffix(m.ConfigName, " (fallback)")

		a, seen := byId[id]
		if !seen {
			a = &acc{
				cred:      LLMCredential{Id: id, Name: name, Provider: res.Provider, ConfigSource: m.ConfigSource},
				seenSlot:  map[string]bool{},
				seenModel: map[string]bool{},
			}
			byId[id] = a
			order = append(order, id)
		}
		// The shortest slot id represents the credential, so a parent slot wins
		// over its own tier variants and the name stays the one the user
		// recognises.
		if len(m.ConfigSource) < len(a.cred.ConfigSource) {
			a.cred.ConfigSource = m.ConfigSource
			a.cred.Name = name
		}
		if !a.seenSlot[m.ConfigSource] {
			a.seenSlot[m.ConfigSource] = true
			a.cred.Sources = append(a.cred.Sources, MergedSlot{ConfigSource: m.ConfigSource, ConfigName: m.ConfigName})
		}
		if !a.seenModel[m.Model] {
			a.seenModel[m.Model] = true
			a.cred.Models = append(a.cred.Models, CredentialModel{Model: m.Model})
		}
	}

	out := make([]LLMCredential, 0, len(order))
	for _, id := range order {
		c := byId[id].cred
		sort.Slice(c.Sources, func(i, j int) bool { return c.Sources[i].ConfigSource < c.Sources[j].ConfigSource })
		out = append(out, c)
	}
	return out
}

// GetConfiguredCredentials returns the unique credentials an account can send
// requests through — system (ENV) and every visible LLM integration, across
// global, per-tier and per-agent slots — each carrying the models reachable
// through it. This is the shape the model picker consumes; it does no grouping
// of its own because only the server can see endpoints and api keys.
func GetConfiguredCredentials(accountId string) ([]LLMCredential, error) {
	models, err := GetAllConfiguredModels(accountId)
	if err != nil {
		return nil, err
	}
	return buildCredentials(nil, accountId, models), nil
}

// BuildCredentialsFrom is GetConfiguredCredentials for callers that already hold
// the flat list, so the slot walk isn't repeated.
func BuildCredentialsFrom(accountId string, models []ModelConfig) []LLMCredential {
	return buildCredentials(nil, accountId, models)
}

// ConfigNameFor returns a human-readable label for a slot-id — used by the
// model picker UI so operators recognize which slot they're picking. env slots
// are owned by the operator and read as "System · <scope>"; db slots belong to
// a tenant-configured integration and read as "<integration name> · <scope>",
// so integrationName must be supplied for db ids (ignored for env ids).
func ConfigNameFor(sourceId, integrationName string) string {
	p, err := parseConfigSourceId(sourceId)
	if err != nil {
		return sourceId // fall back to raw id so the UI still shows something
	}
	owner := "System"
	if p.Layer == "db" {
		if integrationName == "" {
			return sourceId
		}
		owner = integrationName
	}
	switch p.Scope {
	case "all":
		// The whole config, so it is named by the config itself — no slot suffix.
		return owner
	case "global":
		if p.Layer == "db" {
			return owner
		}
		return owner + " · Default"
	case "tier":
		// Title-case the first byte manually; the tier names we emit
		// ("reasoning", "retrieval", "summary") are ASCII, so
		// avoiding cases.Title keeps the dependency list unchanged.
		label := p.Name
		if label != "" && label[0] >= 'a' && label[0] <= 'z' {
			label = string(label[0]-32) + label[1:]
		}
		return owner + " · " + label + " tier"
	case "agent":
		return fmt.Sprintf("%s · Agent: %s", owner, p.Name)
	}
	return sourceId
}

func isModelNamespaceSeparator(char byte) bool {
	return char == ':' || char == '/' || char == '.' || char == '_' || char == '-'
}
