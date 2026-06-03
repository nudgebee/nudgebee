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
	"sort"
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

// Field-name constants mirror api-server/services/integrations/llm.go's
// ConfigSchema. The probe payload is the exact same name/value pairs the
// integration form stores, so any rename must happen on both sides.
const (
	cfgKeyProvider     = "llm_provider"
	cfgKeyModel        = "llm_model_name"
	cfgKeyFallbacks    = "llm_model_fallbacks"
	cfgKeyAPIKey       = "llm_provider_api_key"
	cfgKeyAPIEndpoint  = "llm_provider_api_endpoint"
	cfgKeyAPIVersion   = "llm_provider_api_version"
	cfgKeyRegion       = "llm_provider_region"
	cfgKeyAccessKey    = "llm_provider_access_key"
	cfgKeySecretKey    = "llm_provider_secret_key"
	cfgKeySessionToken = "llm_provider_session_token"
)

// Concurrency limit for the multi-model probe burst. A typical config has
// ~10-25 (provider, model) pairs across global + tiers + agents + fallbacks.
// 5 concurrent probes keeps wall time bounded (~5-10s for a 25-model config)
// without hammering providers hard enough to trigger genuine rate limits.
const probeConcurrency = 5

// Per-probe wall-clock budget. Same 15s used by the original single-probe.
const probeTimeout = 15 * time.Second

// ProbeResult describes the outcome of probing one (provider, model) pair.
// Surfaced via the test-connection HTTP response so the api-server can
// humanize per-model errors and build a user-actionable aggregate.
type ProbeResult struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Source     string `json:"source"` // human label: "global", "global-fallback", "tier-summary", "agent-k8s_debug", etc.
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`      // raw SDK error string; api-server runs it through humanizeProviderError
	Untestable bool   `json:"untestable,omitempty"` // true for vertexai — structural validation only, treat as pass
}

// probeTarget is a single (provider, model, effective-config) tuple to probe.
// Source carries the human label used in the result so the UI can tell the
// user *which* tier/agent the failing model came from.
type probeTarget struct {
	provider string
	model    string
	source   string
	// cfg is the effective per-target config to feed the per-provider client
	// builders. Inherits global creds; overlays agent-specific creds when the
	// source is an agent that has its own llm_provider_api_key_<agent> etc.
	cfg map[string]string
}

// TestLLMProviderConnection probes the primary (provider, model) pair from cfg.
// Retained as a thin shim around the multi-target probe for callers that only
// care about the primary; the HTTP handler uses TestLLMProviderConnectionAll
// to enumerate global + tiers + agents + fallbacks. New code should call
// TestLLMProviderConnectionAll directly.
func TestLLMProviderConnection(ctx context.Context, cfg map[string]string) error {
	provider := cfg[cfgKeyProvider]
	model := cfg[cfgKeyModel]
	if provider == "" || model == "" {
		return errors.New("llm_provider and llm_model_name are required")
	}
	res := probeOne(ctx, probeTarget{provider: provider, model: model, source: "global", cfg: cfg})
	if !res.OK && !res.Untestable {
		return fmt.Errorf("connectivity probe to %s failed: %s", provider, res.Error)
	}
	return nil
}

// TestLLMProviderConnectionAll enumerates every (provider, model) pair in cfg
// (global, tier overrides, agent overrides, and each chain's fallbacks),
// probes them in parallel with bounded concurrency, and returns the per-target
// results. The HTTP handler returns these verbatim so the api-server can
// classify per-model errors and build a single user-facing message.
//
// Vertex AI is a special case: it uses ADC / GOOGLE_CLOUD_PROJECT, so a
// request-time probe is not meaningful. Vertex AI targets are flagged
// Untestable=true with OK=true so the aggregate count doesn't dock them as
// failures.
func TestLLMProviderConnectionAll(ctx context.Context, cfg map[string]string) ([]ProbeResult, error) {
	targets := enumerateProbeTargets(cfg)
	if len(targets) == 0 {
		return nil, errors.New("no probe targets — llm_provider and llm_model_name are required")
	}

	results := make([]ProbeResult, len(targets))
	sem := make(chan struct{}, probeConcurrency)
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, t probeTarget) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = probeOne(ctx, t)
		}(i, t)
	}
	wg.Wait()
	return results, nil
}

// probeOne runs the existing single-probe logic against one target's effective
// config. Vertex AI returns Untestable=true OK=true; everything else returns
// the raw SDK error (api-server humanizes downstream).
func probeOne(ctx context.Context, t probeTarget) ProbeResult {
	if t.provider == "vertexai" {
		return ProbeResult{
			Provider: t.provider, Model: t.model, Source: t.source,
			OK: true, Untestable: true,
		}
	}
	llm, err := buildLLMFromConfig(t.provider, t.model, t.cfg)
	if err != nil {
		return ProbeResult{
			Provider: t.provider, Model: t.model, Source: t.source,
			OK: false, Error: fmt.Sprintf("failed to instantiate %s client: %s", t.provider, err.Error()),
		}
	}
	pingCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	// One-token ping: cheapest call that exercises auth + reachability without
	// burning meaningful budget. Response body intentionally ignored.
	if _, err := llm.GenerateContent(pingCtx, []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "ping"),
	}, llms.WithMaxTokens(1)); err != nil {
		return ProbeResult{
			Provider: t.provider, Model: t.model, Source: t.source,
			OK: false, Error: err.Error(),
		}
	}
	return ProbeResult{
		Provider: t.provider, Model: t.model, Source: t.source,
		OK: true,
	}
}

// enumerateProbeTargets walks the config and returns one probeTarget per
// (provider, model) pair to probe. Dedupes on (provider, model) — the same
// model declared in multiple places only gets probed once, but the source
// label lists every place it's referenced so the user can fix the right field.
//
// Inheritance rules:
//   - Per-tier provider: llm_tier_provider_<tier>, falls back to global
//   - Per-tier credentials: tier rows do NOT have their own credential schema
//     keys, so they inherit global credentials wholesale.
//   - Per-agent provider: llm_provider_<agent>, falls back to global
//   - Per-agent credentials: each credential key has a per-agent variant
//     (llm_provider_api_key_<agent>, etc.) that overrides the global one when
//     present. Falls back to the global value otherwise.
//   - Fallbacks within a tier/agent inherit that tier/agent's provider and
//     credentials. Fallbacks in the global chain inherit global.
func enumerateProbeTargets(cfg map[string]string) []probeTarget {
	provider := cfg[cfgKeyProvider]
	model := cfg[cfgKeyModel]
	if provider == "" || model == "" {
		return nil
	}

	// dedupeKey -> []source labels.
	type pair struct{ provider, model string }
	deduped := map[pair]probeTarget{}
	addTarget := func(prov, mod, source string, effectiveCfg map[string]string) {
		if prov == "" || mod == "" {
			return
		}
		key := pair{prov, mod}
		if existing, ok := deduped[key]; ok {
			// Append the source label so the user knows every place the
			// duplicate-model is referenced.
			existing.source = existing.source + ", " + source
			deduped[key] = existing
			return
		}
		deduped[key] = probeTarget{provider: prov, model: mod, source: source, cfg: effectiveCfg}
	}

	// 1) Global primary + fallbacks (all using global creds + global provider).
	addTarget(provider, model, "global", cfg)
	for _, fb := range splitFallbacks(cfg[cfgKeyFallbacks]) {
		addTarget(provider, fb, "global-fallback", cfg)
	}

	// 2) Per-tier (reasoning / retrieval / summary).
	for _, tier := range []string{"reasoning", "retrieval", "summary"} {
		tierProvider := cfg["llm_tier_provider_"+tier]
		if tierProvider == "" {
			tierProvider = provider
		}
		tierModel := cfg["llm_tier_model_"+tier]
		tierCfg := overlayCfg(cfg, map[string]string{
			cfgKeyProvider: tierProvider,
			cfgKeyModel:    tierModel,
		})
		if tierModel != "" {
			addTarget(tierProvider, tierModel, "tier-"+tier, tierCfg)
		}
		for _, fb := range splitFallbacks(cfg["llm_tier_model_fallbacks_"+tier]) {
			addTarget(tierProvider, fb, "tier-"+tier+"-fallback", tierCfg)
		}
	}

	// 3) Per-agent — discover agents by scanning for llm_model_name_<agent>.
	for key, val := range cfg {
		if !strings.HasPrefix(key, "llm_model_name_") || key == cfgKeyModel || val == "" {
			continue
		}
		agent := strings.TrimPrefix(key, "llm_model_name_")
		agentProvider := cfg["llm_provider_"+agent]
		if agentProvider == "" {
			agentProvider = provider
		}
		agentCfg := overlayCfg(cfg, agentCredsOverlay(cfg, agent, agentProvider, val))
		addTarget(agentProvider, val, "agent-"+agent, agentCfg)
		for _, fb := range splitFallbacks(cfg["llm_model_fallbacks_"+agent]) {
			addTarget(agentProvider, fb, "agent-"+agent+"-fallback", agentCfg)
		}
	}

	// Stable ordering for deterministic responses + test output.
	out := make([]probeTarget, 0, len(deduped))
	for _, t := range deduped {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].provider != out[j].provider {
			return out[i].provider < out[j].provider
		}
		return out[i].model < out[j].model
	})
	return out
}

// splitFallbacks turns a comma-separated list into a slice, trimming whitespace
// and skipping empties. Mirrors the api-server-side parsing.
func splitFallbacks(raw string) []string {
	if raw == "" {
		return nil
	}
	out := make([]string, 0)
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// overlayCfg returns a shallow merge of base with overlay's non-empty values
// on top. Used to build per-target effective configs without mutating the
// shared cfg map.
func overlayCfg(base, overlay map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

// agentCredsOverlay collects the per-agent credential keys that override the
// global ones (e.g. llm_provider_api_key_<agent>). Only keys with non-empty
// values are included so global creds remain the default fallthrough.
func agentCredsOverlay(cfg map[string]string, agent, agentProvider, agentModel string) map[string]string {
	overlay := map[string]string{
		cfgKeyProvider: agentProvider,
		cfgKeyModel:    agentModel,
	}
	for _, k := range []string{
		cfgKeyAPIKey, cfgKeyAPIEndpoint, cfgKeyAPIVersion,
		cfgKeyRegion, cfgKeyAccessKey, cfgKeySecretKey, cfgKeySessionToken,
	} {
		if v := cfg[k+"_"+agent]; v != "" {
			overlay[k] = v
		}
	}
	return overlay
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
		// Handled in probeOne — should never reach here.
		return nil, fmt.Errorf("vertexai connectivity probe is structural-only and should be handled upstream")
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
