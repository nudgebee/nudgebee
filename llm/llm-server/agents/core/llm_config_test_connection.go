package core

// Config-driven LLM client builders used exclusively by the LLM config
// connectivity probe (POST /v1/llm-config/test-connection in
// api/llm_config.go). These deliberately bypass the env / account
// resolution path (see llm_config.go: GetLLMModel et al.) — connectivity
// testing must instantiate a client from a config payload supplied at
// request time, without touching the global client cache or per-account DB
// overrides. Output is intentionally short-lived (a single GenerateContent
// ping) and not cached.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/llm/config"
	"nudgebee/llm/llms/azure"
	"nudgebee/llm/llms/bedrock"
	"nudgebee/llm/llms/googleai"
	"nudgebee/llm/llms/huggingface"
	"nudgebee/llm/llms/sagemaker"
	"time"

	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/anthropic"
	"github.com/tmc/langchaingo/llms/openai"
)

// Field-name constants mirror api-server/services/integrations/llm.go's
// ConfigSchema. The probe payload is the exact same name/value pairs the
// integration form stores, so any rename must happen on both sides.
const (
	cfgKeyProvider      = "llm_provider"
	cfgKeyModel         = "llm_model_name"
	cfgKeyAPIKey        = "llm_provider_api_key"
	cfgKeyAPIEndpoint   = "llm_provider_api_endpoint"
	cfgKeyAPIVersion    = "llm_provider_api_version"
	cfgKeyRegion        = "llm_provider_region"
	cfgKeyAccessKey     = "llm_provider_access_key"
	cfgKeySecretKey     = "llm_provider_secret_key"
	cfgKeySessionToken  = "llm_provider_session_token"
)

// TestLLMProviderConnection instantiates the provider client from cfg and
// issues a minimal GenerateContent ping. Returns nil on success; the wrapped
// error otherwise. The caller is expected to have already performed
// structural validation (api-server ValidateConfig) so we treat missing
// fields here as internal errors, not user-facing ones.
func TestLLMProviderConnection(ctx context.Context, cfg map[string]string) error {
	provider := cfg[cfgKeyProvider]
	model := cfg[cfgKeyModel]
	if provider == "" || model == "" {
		return errors.New("llm_provider and llm_model_name are required")
	}

	llm, err := buildLLMFromConfig(provider, model, cfg)
	if err != nil {
		return fmt.Errorf("failed to instantiate %s client: %w", provider, err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// One-token ping: cheapest call that exercises auth + reachability without
	// burning meaningful budget. We deliberately ignore the response body —
	// success means the provider answered without auth/network errors.
	_, err = llm.GenerateContent(pingCtx, []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "ping"),
	}, llms.WithMaxTokens(1))
	if err != nil {
		return fmt.Errorf("connectivity probe to %s failed: %w", provider, err)
	}
	return nil
}

func buildLLMFromConfig(provider, model string, cfg map[string]string) (llms.Model, error) {
	switch provider {
	case "openai":
		return newOpenAIFromConfig(model, cfg)
	case "azure":
		return newAzureFromConfig(model, cfg)
	case "anthropic":
		return newAnthropicFromConfig(model, cfg)
	case "huggingface":
		return newHuggingFaceFromConfig(model, cfg)
	case "sagemaker":
		return newSageMakerFromConfig(cfg)
	case "bedrock":
		return newBedrockFromConfig(model, cfg)
	case "googleai":
		return newGoogleAIFromConfig(model, cfg)
	case "vertexai":
		// vertexai uses ADC / GOOGLE_CLOUD_PROJECT rather than a payload API
		// key, so a request-time probe is not meaningful. Structural validation
		// only; the live probe is deferred to first agent invocation.
		return nil, fmt.Errorf("live connectivity test not supported for vertexai; structural validation only")
	default:
		return nil, fmt.Errorf("unknown llm_provider %q", provider)
	}
}

func newOpenAIFromConfig(model string, cfg map[string]string) (llms.Model, error) {
	opts := []openai.Option{
		openai.WithToken(cfg[cfgKeyAPIKey]),
		openai.WithModel(model),
		openai.WithResponseFormat(&openai.ResponseFormat{Type: "text"}),
	}
	if ep := cfg[cfgKeyAPIEndpoint]; ep != "" {
		opts = append(opts, openai.WithBaseURL(ep))
	}
	return openai.New(opts...)
}

func newAzureFromConfig(model string, cfg map[string]string) (llms.Model, error) {
	return azure.New(
		azure.WithToken(cfg[cfgKeyAPIKey]),
		azure.WithAPIVersion(cfg[cfgKeyAPIVersion]),
		azure.WithBaseURL(cfg[cfgKeyAPIEndpoint]),
		azure.WithModel(model),
	)
}

func newAnthropicFromConfig(model string, cfg map[string]string) (llms.Model, error) {
	opts := []anthropic.Option{
		anthropic.WithToken(cfg[cfgKeyAPIKey]),
		anthropic.WithModel(model),
	}
	if ep := cfg[cfgKeyAPIEndpoint]; ep != "" {
		opts = append(opts, anthropic.WithBaseURL(ep))
	}
	return anthropic.New(opts...)
}

func newHuggingFaceFromConfig(model string, cfg map[string]string) (llms.Model, error) {
	return huggingface.New(
		huggingface.WithToken(cfg[cfgKeyAPIKey]),
		huggingface.WithURL(cfg[cfgKeyAPIEndpoint]),
		huggingface.WithModel(model),
	)
}

func newSageMakerFromConfig(cfg map[string]string) (llms.Model, error) {
	return sagemaker.New(cfg[cfgKeyAPIEndpoint], cfg[cfgKeyRegion], map[string]any{})
}

func newBedrockFromConfig(model string, cfg map[string]string) (llms.Model, error) {
	region := cfg[cfgKeyRegion]
	accessKey := cfg[cfgKeyAccessKey]
	secretKey := cfg[cfgKeySecretKey]
	sessionToken := cfg[cfgKeySessionToken]

	if (accessKey == "") != (secretKey == "") {
		return nil, errors.New("bedrock: access_key and secret_key must both be set or both be empty")
	}

	loadOpts := []func(*awsConfig.LoadOptions) error{}
	if accessKey != "" && secretKey != "" {
		loadOpts = append(loadOpts, awsConfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, sessionToken),
		))
	}
	awsCfg, err := awsConfig.LoadDefaultConfig(context.Background(), loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}
	if region != "" {
		awsCfg.Region = region
	}
	awsCfg.RetryMaxAttempts = config.Config.LlmProviderMaxRetries
	slog.Debug("bedrock connectivity test: built client", "region", awsCfg.Region)

	client := bedrockruntime.NewFromConfig(awsCfg)
	return bedrock.New(bedrock.WithModel(model), bedrock.WithClient(client))
}

func newGoogleAIFromConfig(model string, cfg map[string]string) (llms.Model, error) {
	return googleai.New(
		context.Background(),
		googleai.WithAPIKey(cfg[cfgKeyAPIKey]),
		googleai.WithDefaultModel(model),
	)
}
