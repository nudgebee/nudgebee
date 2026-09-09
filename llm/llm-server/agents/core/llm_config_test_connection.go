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
	"net/http"
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

	"nudgebee/llm/llms/openai"
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
	cfgKeyAPIType      = "llm_provider_api_type"
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
	// Cheap ping to verify auth + reachability; >1 token so thinking models still return a content block.
	if _, err := llm.GenerateContent(pingCtx, []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "ping"),
	}, llms.WithMaxTokens(16)); err != nil {
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
//   - Per-tier credentials: each credential key has a per-tier variant
//     (llm_tier_api_key_<tier>, llm_tier_access_key_<tier>, etc.) that
//     overrides the global one when present. Falls back to global otherwise —
//     same shape as per-agent credentials.
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

	// Dedupe on (provider, model, credentials). Credentials are part of the key
	// so the same model reached via different per-tier/agent keys is probed
	// separately — collapsing on (provider, model) alone would silently skip a
	// tier whose credentials differ. Same-cred duplicates still collapse.
	type dedupKey struct{ provider, model, creds string }
	deduped := map[dedupKey]probeTarget{}
	addTarget := func(prov, mod, source string, effectiveCfg map[string]string) {
		if prov == "" || mod == "" {
			return
		}
		key := dedupKey{prov, mod, credFingerprint(effectiveCfg)}
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

	// 2) Per-tier (reasoning / retrieval / summary). Mirror per-agent semantics
	// for credentials: a tier-scoped api_key / endpoint / region / version / aws
	// key overrides the global value for probes against that tier's provider.
	for _, tier := range []string{"reasoning", "retrieval", "summary"} {
		tierProvider := cfg["llm_tier_provider_"+tier]
		if tierProvider == "" {
			tierProvider = provider
		}
		tierModel := cfg["llm_tier_model_"+tier]
		tierCfg := buildScopedCfg(cfg, tierProvider, tierModel, func(generic string) string {
			return cfg[tierScopedKey(generic, tier)]
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
		agentCfg := buildScopedCfg(cfg, agentProvider, val, func(generic string) string {
			return cfg[generic+"_"+agent]
		})
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

// providerScopedKeys are config fields whose meaning is tied to a specific
// provider (endpoint, wire type, API version, region, and each credential).
// A tier/agent that overrides the provider must NOT inherit these from the
// global config — a googleai tier has no business using the global
// huggingface endpoint. They come only from that tier/agent's own scoped keys.
var providerScopedKeys = []string{
	cfgKeyAPIKey, cfgKeyAPIEndpoint, cfgKeyAPIVersion, cfgKeyAPIType,
	cfgKeyRegion, cfgKeyAccessKey, cfgKeySecretKey, cfgKeySessionToken,
	llmAuthTypeKey, llmOAuthTokenURLKey, llmOAuthClientIDKey,
	llmOAuthClientSecretKey, llmOAuthScopeKey, llmExtraHeadersKey,
	llmDeploymentNameKey,
}

// buildScopedCfg returns the effective config for a tier/agent probe target.
// It copies the global cfg, sets the target's provider+model, then resolves
// each provider-scoped field: the target's own scoped value wins; if it has
// none and the target's provider matches the global provider, the global value
// is inherited; otherwise (a different provider) the field is cleared so a
// foreign provider's endpoint/region/keys can't leak in. scoped maps a generic
// cfgKey to the target's per-scope value.
func buildScopedCfg(global map[string]string, provider, model string, scoped func(genericKey string) string) map[string]string {
	out := make(map[string]string, len(global))
	for k, v := range global {
		out[k] = v
	}
	out[cfgKeyProvider] = provider
	out[cfgKeyModel] = model
	sameProvider := provider == global[cfgKeyProvider]
	for _, k := range providerScopedKeys {
		if v := scoped(k); v != "" {
			out[k] = v
		} else if !sameProvider {
			// Different provider and no scoped value — drop the global's
			// provider-scoped field rather than inheriting it.
			delete(out, k)
		}
	}
	return out
}

// tierScopedKey maps a generic provider-scoped key (llm_provider_<x>) to its
// per-tier variant (llm_tier_<x>_<tier>), matching the per-tier resolvers in
// llm_config.go. Derived from the shared llm_provider_/llm_tier_ naming so new
// provider-scoped keys are picked up without editing this. Auth keys
// (llm_auth_type, llm_oauth_*, llm_extra_headers) follow the resolver's
// llm_ → llm_tier_ substitution instead (llm_tier_auth_type_<tier> etc.).
func tierScopedKey(generic, tier string) string {
	if strings.HasPrefix(generic, "llm_provider_") {
		return "llm_tier_" + strings.TrimPrefix(generic, "llm_provider_") + "_" + tier
	}
	return strings.Replace(generic, "llm_", "llm_tier_", 1) + "_" + tier
}

// credFingerprint is a stable string over a target's provider-scoped fields,
// used as part of the dedupe key so two targets sharing (provider, model) but
// differing in credentials are probed separately.
func credFingerprint(cfg map[string]string) string {
	var b strings.Builder
	for _, k := range providerScopedKeys {
		b.WriteString(cfg[k])
		b.WriteByte(0)
	}
	return b.String()
}

func buildLLMFromConfig(provider, model string, cfg map[string]string) (llms.Model, error) {
	switch provider {
	case "openai":
		return newOpenAIFromConfig(model, cfg)
	case ProviderCustom:
		return newCustomFromConfig(model, cfg)
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
	// OAuth / extra headers are custom-provider-only for now; the plain
	// openai provider probes with its static key.
	var authClient *http.Client
	token := cfg[cfgKeyAPIKey]
	if strings.EqualFold(cfg[cfgKeyProvider], ProviderCustom) {
		var oauthMode bool
		var err error
		authClient, oauthMode, err = probeAuthHTTPClient(cfg)
		if err != nil {
			return nil, err
		}
		if oauthMode && token == "" {
			// Placeholder: llmAuthTransport replaces the auth header with the
			// fresh bearer, but the client constructor requires a non-empty token.
			token = "oauth-managed"
		}
	}
	opts := []openai.Option{
		openai.WithToken(token),
		openai.WithModel(model),
		openai.WithResponseFormat(&openai.ResponseFormat{Type: "text"}),
	}
	if ep := cfg[cfgKeyAPIEndpoint]; ep != "" {
		opts = append(opts, openai.WithBaseURL(ep))
	}
	// Probe/runtime parity (mirrors getOpenAILLM): Azure-shaped gateways need
	// the api-type + api-version URL grammar, and a deployment segment that can
	// differ from the body's model name — otherwise Test Connection probes a
	// different URL than conversations use.
	apiType := openai.APITypeOpenAI
	switch strings.ToLower(cfg[cfgKeyAPIType]) {
	case "azure":
		apiType = openai.APITypeAzure
	case "azure_ad":
		apiType = openai.APITypeAzureAD
	}
	opts = append(opts, openai.WithAPIType(apiType))
	if apiType == openai.APITypeAzure || apiType == openai.APITypeAzureAD {
		// The client constructor requires an embeddings model on Azure api
		// types (the runtime always passes one from env). The probe never
		// embeds, so a placeholder keeps construction failures from masking
		// the real gateway response.
		opts = append(opts, openai.WithEmbeddingModel("probe-unused"))
		if v := cfg[cfgKeyAPIVersion]; v != "" {
			opts = append(opts, openai.WithAPIVersion(v))
		}
		if deployment := strings.TrimSpace(cfg[llmDeploymentNameKey]); deployment != "" && deployment != model {
			opts = append(opts, openai.WithModel(deployment))
			authClient = wrapDeploymentBodyModel(authClient, model)
		}
	}
	// OAuth/header client (nil in static mode) rides as the sanitizer's base,
	// mirroring getOpenAILLM.
	opts = append(opts, openai.WithHTTPClient(newOpenAIHTTPClient(wrapProbeURLLogging(authClient))))
	return openai.New(opts...)
}

// probeURLLoggingTransport logs the final wire URL each probe request hits, so
// a failing Test Connection can be checked against the exact endpoint —
// including the deployment segment and api-version on azure-shaped gateways.
type probeURLLoggingTransport struct{ base http.RoundTripper }

func (t *probeURLLoggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	slog.Info("llm connectivity test: probing endpoint", "method", req.Method, "url", req.URL.String())
	return base.RoundTrip(req)
}

func wrapProbeURLLogging(inner *http.Client) *http.Client {
	out := &http.Client{Timeout: defaultLLMHTTPTimeout}
	if inner != nil {
		clone := *inner
		out = &clone
	}
	out.Transport = &probeURLLoggingTransport{base: out.Transport}
	return out
}

// newCustomFromConfig mirrors the runtime's getCustomLLM: the OpenAI client
// pointed at a caller-supplied base URL. The endpoint is required rather than
// defaulted — without this guard a config missing it would probe
// api.openai.com, so a user holding a valid OpenAI key would see the test pass
// and the runtime then fail.
func newCustomFromConfig(model string, cfg map[string]string) (llms.Model, error) {
	if strings.TrimSpace(cfg[cfgKeyAPIEndpoint]) == "" {
		return nil, fmt.Errorf("llm provider %q requires llm_provider_api_endpoint (e.g. https://openrouter.ai/api/v1)", ProviderCustom)
	}
	return newOpenAIFromConfig(model, cfg)
}

func newAzureFromConfig(model string, cfg map[string]string) (llms.Model, error) {
	opts := []azure.Option{
		azure.WithToken(cfg[cfgKeyAPIKey]),
		azure.WithAPIVersion(cfg[cfgKeyAPIVersion]),
		azure.WithBaseURL(cfg[cfgKeyAPIEndpoint]),
		azure.WithModel(model),
	}
	return azure.New(opts...)
}

func newAnthropicFromConfig(model string, cfg map[string]string) (llms.Model, error) {
	opts := []anthropic.Option{
		anthropic.WithToken(cfg[cfgKeyAPIKey]),
		anthropic.WithModel(model),
		// Compose the cache rewrite under the temperature sanitizer, same as runtime.
		anthropic.WithHTTPClient(newAnthropicHTTPClient(anthropicCacheHTTPClient())),
	}
	if ep := cfg[cfgKeyAPIEndpoint]; ep != "" {
		opts = append(opts, anthropic.WithBaseURL(ep))
	}
	llm, err := anthropic.New(opts...)
	if err != nil {
		return nil, err
	}
	// Claude 5 responses lead with a thinking choice; promote the text choice.
	return wrapAnthropicChoiceNormalizer(llm), nil
}

func newHuggingFaceFromConfig(model string, cfg map[string]string) (llms.Model, error) {
	// api_type selects the wire protocol ("openai" → /v1/chat/completions for
	// HF Dedicated Endpoints). Mirror the runtime resolver (llm_config.go's
	// huggingface.WithAPIType) so the probe exercises the same protocol; without
	// it an OpenAI-compatible endpoint is probed with the native HF API and fails.
	return huggingface.New(
		huggingface.WithToken(cfg[cfgKeyAPIKey]),
		huggingface.WithURL(cfg[cfgKeyAPIEndpoint]),
		huggingface.WithModel(model),
		huggingface.WithAPIType(cfg[cfgKeyAPIType]),
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
	opts := []googleai.Option{
		googleai.WithAPIKey(cfg[cfgKeyAPIKey]),
		googleai.WithDefaultModel(model),
	}
	// Validate the same path production uses: if a gateway endpoint is configured,
	// test connectivity through it rather than directly to Google.
	if endpoint := cfg[cfgKeyAPIEndpoint]; endpoint != "" {
		opts = append(opts, googleai.WithBaseURL(endpoint))
	}
	return googleai.New(context.Background(), opts...)
}
