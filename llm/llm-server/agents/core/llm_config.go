package core

// This file handles the configuration and instantiation of Large Language Models (LLMs).
//
// Configuration values are resolved with the following precedence (highest to lowest):
// 1. Agent-specific configuration from the database (e.g., a setting for 'my_agent').
// 2. Agent-specific configuration from environment variables (e.g., LLM_PROVIDER_MY_AGENT).
// 3. Global LLM configuration from the database.
// 4. Global LLM configuration from environment variables (e.g., LLM_PROVIDER).

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

// ResolveFastLLMConfig resolves the fastest available model for a given context.
// It prioritizes 'lite' or 'flash' models to minimize latency for classification/routing tasks.
func ResolveFastLLMConfig(ctx *security.RequestContext, accountId string) (*LLMConfigResolution, error) {
	// For now, we hardcode the preference for Flash-Lite if available in the env or config.
	// In a real multi-provider setup, this would look for specific 'lite' tiers.
	res, err := ResolveLLMConfig(ctx, accountId, "router", "")
	if err != nil {
		return nil, err
	}

	// Optimization: If the resolved model is a 'Pro' or 'Ultra' model, try to force a Lite version.
	// This is a simple implementation that can be expanded with a proper capability map.
	if config.Config.LlmModelLite != "" {
		res.Model = config.Config.LlmModelLite
		return res, nil
	}

	switch res.Provider {
	case "googleai", "vertexai", "vertexai_endpoint":
		lowerModel := strings.ToLower(res.Model)
		// Downgrade pro/ultra to flash-lite; also downgrade non-lite flash models
		// (e.g. gemini-3-flash-preview) to avoid sharing the same quota pool.
		if strings.Contains(lowerModel, "pro") || strings.Contains(lowerModel, "ultra") ||
			(strings.Contains(lowerModel, "flash") && !strings.Contains(lowerModel, "lite")) {
			res.Model = "gemini-2.5-flash-lite"
		}
	case "openai":
		if strings.Contains(strings.ToLower(res.Model), "pro") || strings.Contains(strings.ToLower(res.Model), "ultra") {
			res.Model = "gpt-4o-mini"
		}
	case "anthropic":
		if strings.Contains(strings.ToLower(res.Model), "pro") || strings.Contains(strings.ToLower(res.Model), "ultra") {
			res.Model = "claude-3-haiku-20240307"
		}
	}

	return res, nil
}

func GetLlmModelLite(ctx *security.RequestContext, agentName string, accountId string, conversationId string) (llms.Model, error) {
	res, err := ResolveFastLLMConfig(ctx, accountId)
	if err != nil {
		return nil, err
	}
	return GetLLMModel(res.Provider, res.Model, agentName, true, accountId, res)
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

func getLLMFallbackModelName(accountId, agentName string, appendAgentName bool, resolution ...*LLMConfigResolution) string {
	var dbConfig map[string]string
	if len(resolution) > 0 && resolution[0] != nil {
		dbConfig = resolution[0].dbConfig
	}

	// Layer 1: Start with Global ENV default
	modelName := config.Config.LlmModelFallbacks

	// Layer 2: Override if DB Global exists
	if accountId != "" {
		if dbConfig, err := getLLMIntegrationConfig(nil, accountId, dbConfig); err == nil && dbConfig != nil {
			if val, ok := dbConfig["llm_model_fallbacks"]; ok && val != "" {
				modelName = val
			}
		}
	}

	// Layer 3: Override if Agent ENV exists
	if appendAgentName && agentName != "" {
		fallbackKey := fmt.Sprintf(llmModelFallbackFormat, agentName)
		if agentEnvVal := config.Config.GetString(fallbackKey, ""); agentEnvVal != "" {
			modelName = agentEnvVal
		}
	}

	// Layer 4: Override if DB Agent exists (highest priority)
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

	// Layer 1: Start with Global ENV default (only if provider matches)
	apiKey := ""
	configSource := "none"
	if config.Config.LlmProvider == provider {
		apiKey = config.Config.LlmProviderApiKey
		if apiKey != "" {
			configSource = "ENV-global"
		}
		slog.Debug("Using global ENV API key (provider matches)", "provider", provider, "hasKey", apiKey != "")
	}

	// Layer 2: Override if DB Global exists (check provider match)
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

	// Layer 3: Override if Agent ENV exists (check provider match)
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

	// Layer 4: Override if DB Agent exists (highest priority, check provider match)
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
	return apiKey
}

func getLLMApiEndpoint(accountId, provider, agentName string, appendAgentName bool, resolution ...*LLMConfigResolution) string {
	slog.Debug("Getting LLM API endpoint", "accountId", accountId, "provider", provider, "agentName", agentName, "appendAgentName", appendAgentName)

	var dbConfig map[string]string
	if len(resolution) > 0 && resolution[0] != nil {
		dbConfig = resolution[0].dbConfig
	}

	// Layer 1: Start with Global ENV default (only if provider matches)
	apiEndpoint := ""
	configSource := "none"
	if config.Config.LlmProvider == provider {
		apiEndpoint = config.Config.LlmProviderApiEndpoint
		if apiEndpoint != "" {
			configSource = "ENV-global"
		}
		slog.Debug("Using global ENV API endpoint (provider matches)", "provider", provider, "endpoint", apiEndpoint)
	}

	// Layer 2: Override if DB Global exists (check provider match)
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

	// Layer 3: Override if Agent ENV exists (check provider match)
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

	// Layer 4: Override if DB Agent exists (highest priority, check provider match)
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

	// Layer 1: Start with Global ENV default (only if provider matches)
	apiVersion := ""
	if config.Config.LlmProvider == provider {
		apiVersion = config.Config.LlmProviderApiVersion
		slog.Debug("Using global ENV API version (provider matches)", "provider", provider, "version", apiVersion)
	}

	// Layer 2: Override if DB Global exists (check provider match)
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

	// Layer 3: Override if Agent ENV exists (check provider match)
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

	// Layer 4: Override if DB Agent exists (highest priority, check provider match)
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

	// Layer 1: Start with Global ENV default (only if provider matches)
	apiType := ""
	if config.Config.LlmProvider == provider {
		apiType = config.Config.LlmProviderApiType
		slog.Debug("Using global ENV API type (provider matches)", "provider", provider, "type", apiType)
	}

	// Layer 2: Override if DB Global exists (check provider match)
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

	// Layer 3: Override if Agent ENV exists (check provider match)
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

	// Layer 4: Override if DB Agent exists (highest priority, check provider match)
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

	// Layer 1: Start with Global ENV default (only if provider matches)
	region := ""
	configSource := "none"
	if config.Config.LlmProvider == provider {
		region = config.Config.LlmProviderRegion
		if region != "" {
			configSource = "ENV-global"
		}
		slog.Debug("Using global ENV region (provider matches)", "provider", provider, "region", region)
	}

	// Layer 2: Override if DB Global exists (check provider match)
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

	// Layer 3: Override if Agent ENV exists (check provider match)
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

	// Layer 4: Override if DB Agent exists (highest priority, check provider match)
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
// session token) using the same 4-layer precedence as getLLMApiKey/getLLMRegion:
// ENV-global < DB-global < ENV-agent < DB-agent. `envGlobal` is the value from
// config.Config (only used when config.Config.LlmProvider == provider). `globalKey`
// is the DB/global key (e.g. "llm_provider_access_key"). `agentKeyFormat` is the
// fmt format for the agent-scoped key (e.g. llmProviderAccessKeyFormat).
func resolveLLMSecret(accountId, provider, agentName, envGlobal, globalKey, agentKeyFormat string, appendAgentName bool, resolution ...*LLMConfigResolution) string {
	var dbConfig map[string]string
	if len(resolution) > 0 && resolution[0] != nil {
		dbConfig = resolution[0].dbConfig
	}

	// Layer 1: ENV global (only if provider matches)
	val := ""
	if config.Config.LlmProvider == provider {
		val = envGlobal
	}

	// Layer 2: DB global (check provider match)
	if accountId != "" {
		if dbCfg, err := getLLMIntegrationConfig(nil, accountId, dbConfig); err == nil && dbCfg != nil {
			if providerVal, ok := dbCfg["llm_provider"]; ok && providerVal == provider {
				if v, ok := dbCfg[globalKey]; ok && v != "" {
					val = v
				}
			}
		}
	}

	// Layer 3: ENV agent (check provider match)
	if appendAgentName && agentName != "" {
		providerKey := fmt.Sprintf(llmProviderFormat, agentName)
		if envProviderVal := config.Config.GetString(providerKey, ""); envProviderVal == provider {
			agentKey := fmt.Sprintf(agentKeyFormat, agentName)
			if v := config.Config.GetString(agentKey, ""); v != "" {
				val = v
			}
		}
	}

	// Layer 4: DB agent (highest priority, check provider match)
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

	// Optimization: Check per-request cache if context is provided
	var cache *LLMResolutionCache
	if ctx != nil {
		if val := ctx.GetContext().Value(ContextKeyLLMResolution); val != nil {
			cache = val.(*LLMResolutionCache)
			cacheKey := fmt.Sprintf("%s:%s:%s", accountId, agentName, conversationId)
			if res, ok := cache.Get(cacheKey); ok {
				slog.Debug("Reusing per-request LLM config resolution", "cacheKey", cacheKey)
				return res, nil
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

	// Layer 1: Global ENV default (lowest priority)
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

	// Layer 2: Global DB config
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
			}
		}
	}

	// Layer 3: Agent-specific ENV config
	if appendAgentName {
		providerKey := fmt.Sprintf(llmProviderFormat, agentName)
		modelKey := fmt.Sprintf(llmModelFormat, agentName)

		if provider := config.Config.GetString(providerKey, ""); provider != "" {
			if model := config.Config.GetString(modelKey, ""); model != "" {
				result.Hierarchy = append(result.Hierarchy, LLMConfigLayer{
					Level:    "env-agent",
					Provider: provider,
					Model:    model,
					Active:   false,
				})
				result.Provider = provider
				result.Model = model
				result.Source = "env-agent"
				slog.Debug("Found env-agent config",
					"agentName", agentName,
					"provider", provider,
					"model", model)
			}
		}
	}

	// Layer 4: Agent-specific DB config
	if dbConfig != nil && appendAgentName {
		providerKey := fmt.Sprintf(llmProviderFormat, agentName)
		modelKey := fmt.Sprintf(llmModelFormat, agentName)

		if provider, ok := dbConfig[providerKey]; ok && provider != "" {
			if model, ok := dbConfig[modelKey]; ok && model != "" {
				result.Hierarchy = append(result.Hierarchy, LLMConfigLayer{
					Level:    "db-agent",
					Provider: provider,
					Model:    model,
					Active:   false,
				})
				result.Provider = provider
				result.Model = model
				result.Source = "db-agent"
				slog.Debug("Found db-agent config",
					"agentName", agentName,
					"provider", provider,
					"model", model)
			}
		}
	}

	// Layer 5: Conversation-specific override (highest priority)
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
		"agent", agentName)

	// Save to per-request cache if available
	if cache != nil {
		cacheKey := fmt.Sprintf("%s:%s:%s", accountId, agentName, conversationId)
		cache.Set(cacheKey, result)
	}

	return result, nil
}

// GetAllConfiguredModels returns all unique models from ENV and DB configurations.
// Includes global and agent-specific models for user selection.
func GetAllConfiguredModels(accountId string) ([]ModelConfig, error) {
	var models []ModelConfig
	seen := make(map[string]bool)

	// Helper to add model if unique
	addModel := func(provider, model, source string) {
		if provider == "" || model == "" {
			return
		}
		key := fmt.Sprintf("%s:%s", provider, model)
		if !seen[key] {
			models = append(models, ModelConfig{
				Provider: provider,
				Model:    model,
				Source:   source,
			})
			seen[key] = true
		}
	}

	// 1. ENV Global
	addModel(config.Config.LlmProvider, config.Config.LlmModel, "env-global")

	// 2. ENV Agent-specific (check known agent names)
	knownAgents := []string{"llm", "k8s_debug_react", "prometheus", "loki", "github", "security", "summary_agent"}
	for _, agentName := range knownAgents {
		providerKey := fmt.Sprintf(llmProviderFormat, agentName)
		modelKey := fmt.Sprintf(llmModelFormat, agentName)
		provider := config.Config.GetString(providerKey, "")
		model := config.Config.GetString(modelKey, "")
		if provider != "" && model != "" {
			addModel(provider, model, fmt.Sprintf("env-agent-%s", agentName))
		}
	}

	// 3. DB Global and Agent-specific
	if accountId != "" {
		dbConfig, err := getLLMIntegrationConfig(nil, accountId)
		if err == nil && dbConfig != nil {
			// DB Global
			if p, ok := dbConfig["llm_provider"]; ok {
				if m, ok := dbConfig["llm_model_name"]; ok {
					addModel(p, m, "db-global")
				}
			}

			// DB Agent-specific (scan all keys for llm_provider_*)
			for key, provider := range dbConfig {
				if !strings.HasPrefix(key, "llm_provider_") {
					continue
				}
				// Skip non-agent keys
				if strings.HasPrefix(key, "llm_provider_api_") ||
					strings.HasPrefix(key, "llm_provider_region") ||
					strings.HasPrefix(key, "llm_provider_require") ||
					strings.HasPrefix(key, "llm_provider_adapter") {
					continue
				}

				agentName := strings.TrimPrefix(key, "llm_provider_")
				modelKey := fmt.Sprintf("llm_model_name_%s", agentName)
				if model, ok := dbConfig[modelKey]; ok && provider != "" && model != "" {
					addModel(provider, model, fmt.Sprintf("db-agent-%s", agentName))
				}
			}
		}
	}

	// 4. Fallback models (comma-separated model names)
	// Fallback models use the global provider since they're alternatives for quota/rate limit scenarios
	globalProvider := config.Config.LlmProvider
	if accountId != "" {
		if dbConfig, err := getLLMIntegrationConfig(nil, accountId); err == nil && dbConfig != nil {
			if p, ok := dbConfig["llm_provider"]; ok && p != "" {
				globalProvider = p
			}
		}
	}

	// Helper to add fallback models from comma-separated string
	addFallbackModels := func(fallbackStr, source string) {
		if fallbackStr == "" || globalProvider == "" {
			return
		}
		for _, model := range strings.Split(fallbackStr, ",") {
			model = strings.TrimSpace(model)
			if model != "" {
				addModel(globalProvider, model, source)
			}
		}
	}

	// 4a. ENV Global fallbacks
	addFallbackModels(config.Config.LlmModelFallbacks, "env-fallback")

	// 4b. ENV Agent-specific fallbacks
	for _, agentName := range knownAgents {
		fallbackKey := fmt.Sprintf(llmModelFallbackFormat, agentName)
		fallbackStr := config.Config.GetString(fallbackKey, "")
		addFallbackModels(fallbackStr, fmt.Sprintf("env-fallback-%s", agentName))
	}

	// 4c. DB Global and Agent-specific fallbacks
	if accountId != "" {
		if dbConfig, err := getLLMIntegrationConfig(nil, accountId); err == nil && dbConfig != nil {
			// DB Global fallbacks
			if fallbackStr, ok := dbConfig["llm_model_fallbacks"]; ok {
				addFallbackModels(fallbackStr, "db-fallback")
			}

			// DB Agent-specific fallbacks
			for key, fallbackStr := range dbConfig {
				if strings.HasPrefix(key, "llm_model_fallbacks_") {
					agentName := strings.TrimPrefix(key, "llm_model_fallbacks_")
					addFallbackModels(fallbackStr, fmt.Sprintf("db-fallback-%s", agentName))
				}
			}
		}
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
