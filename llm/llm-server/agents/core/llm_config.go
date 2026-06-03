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
	"context"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/llm/config"
	"nudgebee/llm/llms/azure"
	"nudgebee/llm/llms/bedrock"
	"nudgebee/llm/llms/googleai"
	"nudgebee/llm/llms/googleai/vertex"
	vertexendpoint "nudgebee/llm/llms/googleai/vertexai_endpoint"
	"nudgebee/llm/llms/huggingface"
	"nudgebee/llm/llms/sagemaker"
	"nudgebee/llm/security"
	"os"
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

// Category-tier config keys. A tier (reasoning / retrieval / summary)
// is configured like an agent but in its own namespace so it cannot collide
// with an agent that happens to share the name.
const llmTierProviderFormat = "llm_tier_provider_%s"
const llmTierModelFormat = "llm_tier_model_%s"
const llmTierModelFallbackFormat = "llm_tier_model_fallbacks_%s"

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

func GetLLMModel(provider string, modelName string, agentName string, appendAgentName bool, accountId string, resolution ...*LLMConfigResolution) (llms.Model, error) {
	slog.Debug("GetLLMModel called", "provider", provider, "modelName", modelName, "agentName", agentName, "appendAgentName", appendAgentName, "accountId", accountId)

	var res *LLMConfigResolution
	if len(resolution) > 0 {
		res = resolution[0]
	}

	// Optimization: Reuse LLM clients based on provider, model, and accountId
	cacheKey := fmt.Sprintf("%s:%s:%s", provider, modelName, accountId)
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
		llmClientCache[cacheKey] = llmCacheEntry{model: model, ts: time.Now()}
	}

	return model, err
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

var (
	conversationOverrideCache = make(map[string]struct {
		provider string
		model    string
		ts       time.Time
	})
	conversationOverrideCacheMutex sync.RWMutex
	conversationOverrideCacheTTL   = 5 * time.Minute
)

// GetConversationOverride returns the model override for a conversation (if set)
func GetConversationOverride(conversationId string) (string, string, error) {
	conversationOverrideCacheMutex.RLock()
	entry, found := conversationOverrideCache[conversationId]
	conversationOverrideCacheMutex.RUnlock()

	if found && time.Since(entry.ts) < conversationOverrideCacheTTL {
		return entry.provider, entry.model, nil
	}

	conv, err := GetConversationDao().GetConversation(conversationId)
	if err != nil {
		return "", "", err
	}

	provider := ""
	model := ""
	if conv.LlmProvider != nil {
		provider = *conv.LlmProvider
	}
	if conv.LlmModel != nil {
		model = *conv.LlmModel
	}

	conversationOverrideCacheMutex.Lock()
	conversationOverrideCache[conversationId] = struct {
		provider string
		model    string
		ts       time.Time
	}{provider: provider, model: model, ts: time.Now()}
	conversationOverrideCacheMutex.Unlock()

	return provider, model, nil
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

func getLLMApiKey(accountId, provider, agentName string, appendAgentName bool, resolution ...*LLMConfigResolution) string {
	slog.Debug("Getting LLM API key", "accountId", accountId, "provider", provider, "agentName", agentName, "appendAgentName", appendAgentName)

	var dbConfig map[string]string
	if len(resolution) > 0 && resolution[0] != nil {
		dbConfig = resolution[0].dbConfig
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

	// L2 ENV-agent (check provider match against the agent's own ENV provider)
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

	// L3 DB-global (check provider match — overrides any ENV layer above)
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

	// L4 DB-agent (highest priority — check provider match against DB-agent provider)
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

	var dbConfig map[string]string
	if len(resolution) > 0 && resolution[0] != nil {
		dbConfig = resolution[0].dbConfig
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

	// L2 ENV-agent
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

	// L3 DB-global
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

	// L4 DB-agent (highest priority)
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

	var dbConfig map[string]string
	if len(resolution) > 0 && resolution[0] != nil {
		dbConfig = resolution[0].dbConfig
	}

	// ENV first, then DB on top. DB always beats ENV — see package-level docstring.

	apiVersion := ""

	// L1 ENV-global
	if config.Config.LlmProvider == provider {
		apiVersion = config.Config.LlmProviderApiVersion
		slog.Debug("Using global ENV API version (provider matches)", "provider", provider, "version", apiVersion)
	}

	// L2 ENV-agent
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

	// L3 DB-global
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

	// L4 DB-agent (highest priority)
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

	var dbConfig map[string]string
	if len(resolution) > 0 && resolution[0] != nil {
		dbConfig = resolution[0].dbConfig
	}

	// ENV first, then DB on top. DB always beats ENV — see package-level docstring.

	apiType := ""

	// L1 ENV-global
	if config.Config.LlmProvider == provider {
		apiType = config.Config.LlmProviderApiType
		slog.Debug("Using global ENV API type (provider matches)", "provider", provider, "type", apiType)
	}

	// L2 ENV-agent
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

	// L3 DB-global
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

	// L4 DB-agent (highest priority)
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

	var dbConfig map[string]string
	if len(resolution) > 0 && resolution[0] != nil {
		dbConfig = resolution[0].dbConfig
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

	// L2 ENV-agent
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

	// L3 DB-global
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

	// L4 DB-agent (highest priority)
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
func resolveLLMSecret(accountId, provider, agentName, envGlobal, globalKey, agentKeyFormat string, appendAgentName bool, resolution ...*LLMConfigResolution) string {
	var dbConfig map[string]string
	if len(resolution) > 0 && resolution[0] != nil {
		dbConfig = resolution[0].dbConfig
	}

	val := ""

	// L1 ENV-global (only if provider matches)
	if config.Config.LlmProvider == provider {
		val = envGlobal
	}

	// L2 ENV-agent
	if appendAgentName && agentName != "" {
		providerKey := fmt.Sprintf(llmProviderFormat, agentName)
		if envProviderVal := config.Config.GetString(providerKey, ""); envProviderVal == provider {
			agentKey := fmt.Sprintf(agentKeyFormat, agentName)
			if v := config.Config.GetString(agentKey, ""); v != "" {
				val = v
			}
		}
	}

	// L3 DB-global
	if accountId != "" {
		if dbCfg, err := getLLMIntegrationConfig(nil, accountId, dbConfig); err == nil && dbCfg != nil {
			if providerVal, ok := dbCfg["llm_provider"]; ok && providerVal == provider {
				if v, ok := dbCfg[globalKey]; ok && v != "" {
					val = v
				}
			}
		}
	}

	// L4 DB-agent (highest priority)
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
	return resolveLLMSecret(accountId, provider, agentName, config.Config.LlmProviderAccessKey, "llm_provider_access_key", llmProviderAccessKeyFormat, appendAgentName, resolution...)
}

func getLLMSecretKey(accountId, provider, agentName string, appendAgentName bool, resolution ...*LLMConfigResolution) string {
	return resolveLLMSecret(accountId, provider, agentName, config.Config.LlmProviderSecretKey, "llm_provider_secret_key", llmProviderSecretKeyFormat, appendAgentName, resolution...)
}

func getLLMSessionToken(accountId, provider, agentName string, appendAgentName bool, resolution ...*LLMConfigResolution) string {
	return resolveLLMSecret(accountId, provider, agentName, config.Config.LlmProviderSessionToken, "llm_provider_session_token", llmProviderSessionTokenFormat, appendAgentName, resolution...)
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
	return llm, nil
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
	llm, err := googleai.New(ctx, googleai.WithAPIKey(token), googleai.WithDefaultModel(modelName))
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
	slog.Debug("Hugging Face configuration", "hasApiKey", apiKey != "", "endpoint", apiEndpoint)

	opts := []huggingface.Option{
		huggingface.WithToken(apiKey),
		huggingface.WithURL(apiEndpoint),
		huggingface.WithModel(modelName),
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
	llm, err := openai.New(openai.WithResponseFormat(responseFormatJSON), openai.WithAPIType(apiType), openai.WithToken(token), openai.WithModel(modelName), openai.WithEmbeddingModel(embeddingModel), openai.WithBaseURL(baseURL))
	if err != nil {
		slog.Error("Failed to create OpenAI LLM", "error", err, "modelName", modelName)
		return nil, err
	}
	slog.Info("Using OpenAI LLM", "model", modelName, "agentName", agentName, "apiType", apiType)
	return llm, nil
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
	Source   string `json:"source"` // "global", "agent", "db", "env"
}

// LLMConfigResolution provides detailed information about how LLM model configuration
// is resolved, showing the full hierarchy and which layer is active
type LLMConfigResolution struct {
	Provider     string            `json:"provider"`      // Active provider (e.g., "openai", "anthropic")
	Model        string            `json:"model"`         // Active model (e.g., "gpt-4", "claude-3-5-sonnet")
	Source       string            `json:"source"`        // Which layer is active
	IsOverridden bool              `json:"is_overridden"` // True if conversation has explicit override
	AgentName    string            `json:"agent_name,omitempty"`
	Hierarchy    []LLMConfigLayer  `json:"hierarchy"` // Full resolution chain
	dbConfig     map[string]string // unexported cache for optimized downstream lookups
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

	// L7: Conversation-specific override (per-request user-explicit)
	if conversationId != "" {
		if p, m, err := GetConversationOverride(conversationId); err == nil && p != "" && m != "" {
			result.Hierarchy = append(result.Hierarchy, LLMConfigLayer{
				Level:    "conversation",
				Provider: p,
				Model:    m,
				Active:   false,
			})
			result.Provider = p
			result.Model = m
			result.Source = "conversation"
			result.IsOverridden = true
			slog.Debug("Found conversation override",
				"conversationId", conversationId,
				"provider", p,
				"model", m)
		}
	}

	// Highest precedence: explicit per-request override. Both provider and
	// model must be present; a half-set override is ignored.
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

	slog.Info("LLM config resolution complete",
		"duration", time.Since(t0).String(),
		"provider", result.Provider,
		"model", result.Model,
		"source", result.Source,
		"tier", string(tier),
		"agent", agentName)

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

	addModel := func(provider, model, source string) {
		if provider == "" || model == "" {
			return
		}
		key := fmt.Sprintf("%s:%s", provider, model)
		if seen[key] {
			return
		}
		models = append(models, ModelConfig{
			Provider: provider,
			Model:    model,
			Source:   source,
		})
		seen[key] = true
	}

	addFallbacks := func(provider, fallbackStr, source string) {
		if provider == "" || fallbackStr == "" {
			return
		}
		for _, model := range strings.Split(fallbackStr, ",") {
			addModel(provider, strings.TrimSpace(model), source)
		}
	}

	// Load the account's DB config once. Subsequent DB walks reuse this.
	var dbConfig map[string]string
	if accountId != "" {
		if cfg, err := getLLMIntegrationConfig(nil, accountId); err == nil && cfg != nil {
			dbConfig = cfg
		}
	}

	// Resolve the effective global provider — DB wins over ENV when both set.
	globalProvider := config.Config.LlmProvider
	if dbConfig != nil {
		if p, ok := dbConfig["llm_provider"]; ok && p != "" {
			globalProvider = p
		}
	}

	tiers := []string{"reasoning", "retrieval", "summary"}

	// ─── ENV ──────────────────────────────────────────────────────────────────

	// ENV global + global fallbacks
	addModel(config.Config.LlmProvider, config.Config.LlmModel, "env-global")
	addFallbacks(config.Config.LlmProvider, config.Config.LlmModelFallbacks, "env-fallback")

	// ENV tier + tier fallbacks (tier provider falls back to ENV-global)
	for _, tier := range tiers {
		tierProvider := config.Config.GetString(fmt.Sprintf(llmTierProviderFormat, tier), config.Config.LlmProvider)
		tierModel := config.Config.GetString(fmt.Sprintf(llmTierModelFormat, tier), "")
		addModel(tierProvider, tierModel, fmt.Sprintf("env-tier-%s", tier))
		tierFb := config.Config.GetString(fmt.Sprintf(llmTierModelFallbackFormat, tier), "")
		addFallbacks(tierProvider, tierFb, fmt.Sprintf("env-tier-fallback-%s", tier))
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
		addModel(provider, model, fmt.Sprintf("env-agent-%s", agentName))
		fb := config.Config.GetString(fmt.Sprintf(llmModelFallbackFormat, agentName), "")
		addFallbacks(provider, fb, fmt.Sprintf("env-fallback-%s", agentName))
	}

	// ─── DB ───────────────────────────────────────────────────────────────────

	if dbConfig == nil {
		return models, nil
	}

	// DB global + global fallbacks
	addModel(dbConfig["llm_provider"], dbConfig["llm_model_name"], "db-global")
	addFallbacks(globalProvider, dbConfig["llm_model_fallbacks"], "db-fallback")

	// DB tier + tier fallbacks (tier provider falls back to DB-global, then ENV-global)
	for _, tier := range tiers {
		tierProvider := dbConfig[fmt.Sprintf(llmTierProviderFormat, tier)]
		if tierProvider == "" {
			tierProvider = globalProvider
		}
		addModel(tierProvider, dbConfig[fmt.Sprintf(llmTierModelFormat, tier)], fmt.Sprintf("db-tier-%s", tier))
		addFallbacks(tierProvider, dbConfig[fmt.Sprintf(llmTierModelFallbackFormat, tier)], fmt.Sprintf("db-tier-fallback-%s", tier))
	}

	// DB per-agent — scan llm_provider_<agent> keys, excluding credential
	// suffixes that share the same prefix.
	for key, provider := range dbConfig {
		if !strings.HasPrefix(key, "llm_provider_") {
			continue
		}
		if strings.HasPrefix(key, "llm_provider_api_") ||
			strings.HasPrefix(key, "llm_provider_region") ||
			strings.HasPrefix(key, "llm_provider_require") ||
			strings.HasPrefix(key, "llm_provider_adapter") {
			continue
		}
		agentName := strings.TrimPrefix(key, "llm_provider_")
		if agentName == "" {
			continue
		}
		model := dbConfig[fmt.Sprintf(llmModelFormat, agentName)]
		addModel(provider, model, fmt.Sprintf("db-agent-%s", agentName))
		addFallbacks(provider, dbConfig[fmt.Sprintf(llmModelFallbackFormat, agentName)], fmt.Sprintf("db-fallback-%s", agentName))
	}

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
