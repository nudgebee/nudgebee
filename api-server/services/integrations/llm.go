package integrations

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"regexp"
	"sort"
	"strings"
	"time"
)

// awsRegionPattern is a basic shape check for AWS regions (e.g. us-east-1,
// eu-west-2). It is intentionally not a full AWS region whitelist.
var awsRegionPattern = regexp.MustCompile(`^[a-z]{2}-[a-z]+-\d+$`)

// IsLLMSecretFieldName is exported here as a convenience re-export of the
// canonical definition in package core (which integrations/core/integration_config.go
// also references). Callers outside core can use either name.
func IsLLMSecretFieldName(name string) bool {
	return core.IsLLMSecretFieldName(name)
}

// llmProviders is the set of accepted llm_provider / llm_provider_summary_agent
// values. It MUST stay in sync with the Enum declared in ConfigSchema; the
// drift-guard test in llm_test.go enforces that.
var llmProviders = []string{"anthropic", "azure", "bedrock", "googleai", "huggingface", "openai", "sagemaker", "vertexai"}

// providerRequiredFields returns the base field names that must be non-empty
// for a given provider. These mirror the RequiredWhen contracts declared in
// ConfigSchema; the drift-guard test cross-checks this table against the
// schema so the two cannot silently diverge.
func providerRequiredFields(provider string) []string {
	switch provider {
	case "bedrock":
		return []string{"llm_provider_access_key", "llm_provider_secret_key", "llm_provider_region"}
	case "sagemaker":
		return []string{"llm_provider_region", "llm_provider_api_endpoint"}
	case "azure":
		return []string{"llm_provider_api_key", "llm_provider_api_endpoint", "llm_provider_api_version"}
	case "anthropic", "googleai", "huggingface", "openai", "vertexai":
		return []string{"llm_provider_api_key"}
	default:
		return nil
	}
}

// providerRequiredSummaryFields returns the *_summary_agent field names that
// must be non-empty for a given summarization provider. The summary block has
// no access/secret key fields (unlike the primary block), so this mirrors only
// the summary fields that actually exist in ConfigSchema.
func providerRequiredSummaryFields(provider string) []string {
	switch provider {
	case "bedrock":
		return []string{"llm_provider_region_summary_agent"}
	case "sagemaker":
		return []string{"llm_provider_region_summary_agent", "llm_provider_api_endpoint_summary_agent"}
	case "azure":
		return []string{"llm_provider_api_key_summary_agent", "llm_provider_api_endpoint_summary_agent", "llm_provider_api_version_summary_agent"}
	case "anthropic", "googleai", "huggingface", "openai", "vertexai":
		return []string{"llm_provider_api_key_summary_agent"}
	default:
		return nil
	}
}

func init() {
	core.RegisterIntegration(LLM{})
}

const IntegrationLLM = "llm"

type LLM struct {
}

func (m LLM) Name() string {
	return IntegrationLLM
}

func (m LLM) Category() core.IntegrationCategory {
	return core.IntegrationLLM
}

func (m LLM) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{"llm_provider", "llm_model_name"},
		// Testable opts in to the form's "Test Connection" button. The button
		// hits integrations_check_connection_config, which now routes LLM through
		// the live provider probe in llm-server (TestableIntegration interface).
		Testable: true,
		Properties: map[string]core.IntegrationSchemaProperty{
			"account_id": {
				Type:             core.ToolSchemaTypeArray,
				Description:      "List of account identifiers the configuration should apply to. Optional. Auto-populated using listAccounts.",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         22,
			},
			"integration_config_name": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Unique name to identify this integration configuration.",
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         21,
			},
			"llm_provider": {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of the LLM provider (e.g., openai, bedrock, sagemaker, huggingface, azure, googleai, vertexai, anthropic).",
				Enum:        []any{"anthropic", "azure", "bedrock", "googleai", "huggingface", "openai", "sagemaker", "vertexai"},
				Priority:    20,
			},
			"llm_model_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of the primary model to be used from the LLM provider (e.g., gpt-4, llama2, etc.).",
				Priority:    19,
			},
			"llm_provider_api_key": {
				Type:        core.ToolSchemaTypeString,
				Description: "API key for authenticating with the LLM provider.",
				Priority:    18,
				// IsEncrypted=true so the save path encrypts the value via
				// common.Encrypt before storing in integration_config_values
				// (with per-row is_encrypted=true). Combined with the
				// admin_get_integrations_v2 redaction in query/metadata.go,
				// this ensures the API key is never returned to the UI in
				// any form (plaintext or ciphertext). Existing rows with
				// per-row is_encrypted=false stay as plaintext — llm-server's
				// resolver checks the per-row flag, so legacy rows keep
				// working; new edits auto-upgrade to encrypted.
				IsEncrypted: true,
				RequiredWhen: map[string]any{
					"llm_provider": []string{"anthropic", "azure", "googleai", "huggingface", "openai", "vertexai"},
				},
			},
			"llm_provider_api_endpoint": {
				Type:        core.ToolSchemaTypeString,
				Description: "Custom API endpoint for the LLM provider.",
				Priority:    17,
				RequiredWhen: map[string]any{
					"llm_provider": []string{"azure", "sagemaker"},
				},
				ShowWhen: map[string]any{
					"llm_provider": []string{"azure", "openai", "sagemaker"},
				},
			},
			"llm_provider_api_version": {
				Type:        core.ToolSchemaTypeString,
				Description: "API version of the LLM provider to be used. Optional, used for version-specific compatibility.",
				Priority:    16,
				RequiredWhen: map[string]any{
					"llm_provider": []string{"azure"},
				},
			},
			"llm_provider_region": {
				Type:        core.ToolSchemaTypeString,
				Description: "Geographic region for the LLM provider's deployment (e.g., eastus, eu-west). Optional.",
				Priority:    15,
				RequiredWhen: map[string]any{
					"llm_provider": []string{"bedrock", "sagemaker"},
				},
			},
			"llm_provider_access_key": {
				Type:        core.ToolSchemaTypeString,
				Description: "AWS Access Key ID for the Bedrock service.",
				Priority:    15,
				IsEncrypted: true,
				ShowWhen: map[string]any{
					"llm_provider": []string{"bedrock"},
				},
				RequiredWhen: map[string]any{
					"llm_provider": []string{"bedrock"},
				},
			},
			"llm_provider_secret_key": {
				Type:        core.ToolSchemaTypeString,
				Description: "AWS Secret Access Key for the Bedrock service.",
				Priority:    15,
				IsEncrypted: true,
				ShowWhen: map[string]any{
					"llm_provider": []string{"bedrock"},
				},
				RequiredWhen: map[string]any{
					"llm_provider": []string{"bedrock"},
				},
			},
			"llm_provider_api_type": {
				Type:        core.ToolSchemaTypeString,
				Description: "Type of the API. Optional.",
				Priority:    14,
				ShowWhen: map[string]any{
					"llm_provider": []string{"openai"},
				},
			},
			"llm_provider_adapter_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "The adapter ID for a fine-tuned model. Optional.",
				Priority:    13,
				ShowWhen: map[string]any{
					"llm_provider": []string{"azure", "huggingface"},
				},
			},
			"llm_provider_require_adapter_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "Specifies whether an adapter ID is required for the selected provider. Optional.",
				Priority:    12,
				ShowWhen: map[string]any{
					"llm_provider": []string{"azure", "huggingface"},
				},
			},
			"llm_model_fallbacks": {
				Type:        core.ToolSchemaTypeString,
				Description: "Comma-separated list of fallback model names to use in case the primary model fails. Optional.",
				Priority:    11,
			},
			"add_model_for_summarization": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Add a different model for text summarization tasks.",
				Default:     false,
				Priority:    10,
			},
			"llm_provider_summary_agent": {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of the LLM provider to be used specifically for summarization tasks.",
				Enum:        []any{"anthropic", "azure", "bedrock", "googleai", "huggingface", "openai", "sagemaker", "vertexai"},
				Priority:    9,
				ShowWhen: map[string]any{
					"add_model_for_summarization": true,
				},
				RequiredWhen: map[string]any{
					"add_model_for_summarization": true,
				},
			},
			"llm_model_name_summary_agent": {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of the model to be used specifically for summarization tasks.",
				Priority:    8,
				ShowWhen: map[string]any{
					"add_model_for_summarization": true,
				},
				RequiredWhen: map[string]any{
					"add_model_for_summarization": true,
				},
			},
			"llm_provider_api_key_summary_agent": {
				Type:        core.ToolSchemaTypeString,
				Description: "API key for authenticating with the LLM provider for summarization tasks.",
				Priority:    7,
				// IsEncrypted=true — see llm_provider_api_key above for rationale.
				IsEncrypted: true,
				ShowWhen: map[string]any{
					"add_model_for_summarization": true,
				},
				RequiredWhen: map[string]any{
					"add_model_for_summarization": true,
				},
			},
			"llm_provider_api_endpoint_summary_agent": {
				Type:        core.ToolSchemaTypeString,
				Description: "Custom API endpoint for the LLM provider.",
				Priority:    6,
				RequiredWhen: map[string]any{
					"add_model_for_summarization": true,
					"llm_provider_summary_agent":  []string{"azure", "sagemaker"},
				},
				ShowWhen: map[string]any{
					"add_model_for_summarization": true,
					"llm_provider_summary_agent":  []string{"azure", "openai", "sagemaker"},
				},
			},
			"llm_provider_api_version_summary_agent": {
				Type:        core.ToolSchemaTypeString,
				Description: "API version of the LLM provider to be used. Optional, used for version-specific compatibility.",
				Priority:    5,
				RequiredWhen: map[string]any{
					"add_model_for_summarization": true,
					"llm_provider_summary_agent":  []string{"azure"},
				},
			},
			"llm_provider_region_summary_agent": {
				Type:        core.ToolSchemaTypeString,
				Description: "Geographic region for the LLM provider's deployment (e.g., eastus, eu-west). Optional.",
				Priority:    4,
				RequiredWhen: map[string]any{
					"add_model_for_summarization": true,
					"llm_provider_summary_agent":  []string{"bedrock", "sagemaker"},
				},
			},
			"llm_provider_api_type_summary_agent": {
				Type:        core.ToolSchemaTypeString,
				Description: "Type of the API. Optional.",
				Priority:    3,
				ShowWhen: map[string]any{
					"add_model_for_summarization": true,
					"llm_provider_summary_agent":  []string{"openai"},
				},
			},
			"llm_provider_adapter_id_summary_agent": {
				Type:        core.ToolSchemaTypeString,
				Description: "The adapter ID for a fine-tuned model. Optional.",
				Priority:    2,
				ShowWhen: map[string]any{
					"add_model_for_summarization": true,
					"llm_provider_summary_agent":  []string{"azure", "huggingface"},
				},
			},
			"llm_provider_require_adapter_id_summary_agent": {
				Type:        core.ToolSchemaTypeString,
				Description: "Specifies whether an adapter ID is required for the selected provider. Optional.",
				Priority:    1,
				ShowWhen: map[string]any{
					"add_model_for_summarization": true,
					"llm_provider_summary_agent":  []string{"azure", "huggingface"},
				},
			},
			"llm_model_fallbacks_summary_agent": {
				Type:        core.ToolSchemaTypeString,
				Description: "Comma-separated list of fallback model names to use in case the primary model fails. Optional.",
				Priority:    1,
				ShowWhen: map[string]any{
					"add_model_for_summarization": true,
				},
			},
		},
	}
}

func (m LLM) ValidateConfig(securityContext *security.SecurityContext, integrationConfig []core.IntegrationConfigValue, accountId string) []error {
	cfg := make(map[string]string, len(integrationConfig))
	for _, c := range integrationConfig {
		cfg[c.Name] = strings.TrimSpace(c.Value)
	}

	var errs []error

	// Always-required fields.
	provider := cfg["llm_provider"]
	if provider == "" {
		errs = append(errs, fmt.Errorf("llm_provider is required"))
	}
	if cfg["llm_model_name"] == "" {
		errs = append(errs, fmt.Errorf("llm_model_name is required"))
	}

	// Provider enum + provider-specific contracts.
	if provider != "" {
		if !isLLMProvider(provider) {
			errs = append(errs, fmt.Errorf("llm_provider %q is invalid; must be one of %s", provider, strings.Join(llmProviders, ", ")))
		} else {
			for _, f := range providerRequiredFields(provider) {
				if cfg[f] == "" {
					errs = append(errs, fmt.Errorf("%s is required when llm_provider is %q", f, provider))
				}
			}
		}

		// Bedrock credentials must be supplied as a pair.
		if provider == "bedrock" {
			ak, sk := cfg["llm_provider_access_key"], cfg["llm_provider_secret_key"]
			if (ak == "") != (sk == "") {
				errs = append(errs, fmt.Errorf("llm_provider_access_key and llm_provider_secret_key must be provided together for bedrock"))
			}
		}

		// Region shape check for AWS-backed providers.
		if err := validateAWSRegion("llm_provider_region", provider, cfg["llm_provider_region"]); err != nil {
			errs = append(errs, err)
		}
	}

	// Endpoint URL format (when set).
	if ep := cfg["llm_provider_api_endpoint"]; ep != "" && !isValidHTTPURL(ep) {
		errs = append(errs, fmt.Errorf("llm_provider_api_endpoint %q must be a valid http(s) URL", ep))
	}

	// Comma-separated fallback model list: every entry non-empty after trim.
	if fb := cfg["llm_model_fallbacks"]; fb != "" {
		for _, part := range strings.Split(fb, ",") {
			if strings.TrimSpace(part) == "" {
				errs = append(errs, fmt.Errorf("llm_model_fallbacks must be a comma-separated list with no empty entries"))
				break
			}
		}
	}

	// Summarization model contract.
	if cfg["add_model_for_summarization"] == "true" {
		sProvider := cfg["llm_provider_summary_agent"]
		if sProvider == "" {
			errs = append(errs, fmt.Errorf("llm_provider_summary_agent is required when add_model_for_summarization is true"))
		} else if !isLLMProvider(sProvider) {
			errs = append(errs, fmt.Errorf("llm_provider_summary_agent %q is invalid; must be one of %s", sProvider, strings.Join(llmProviders, ", ")))
		} else {
			for _, f := range providerRequiredSummaryFields(sProvider) {
				if cfg[f] == "" {
					errs = append(errs, fmt.Errorf("%s is required when llm_provider_summary_agent is %q", f, sProvider))
				}
			}
		}
		if cfg["llm_model_name_summary_agent"] == "" {
			errs = append(errs, fmt.Errorf("llm_model_name_summary_agent is required when add_model_for_summarization is true"))
		}
		if ep := cfg["llm_provider_api_endpoint_summary_agent"]; ep != "" && !isValidHTTPURL(ep) {
			errs = append(errs, fmt.Errorf("llm_provider_api_endpoint_summary_agent %q must be a valid http(s) URL", ep))
		}
		if err := validateAWSRegion("llm_provider_region_summary_agent", sProvider, cfg["llm_provider_region_summary_agent"]); err != nil {
			errs = append(errs, err)
		}
	}

	return errs
}

// validateAWSRegion checks the AWS region shape for AWS-backed providers
// (bedrock/sagemaker). It is a no-op for other providers or when region is
// unset. Shared by the primary and summary-agent code paths.
func validateAWSRegion(field, provider, region string) error {
	if provider != "bedrock" && provider != "sagemaker" {
		return nil
	}
	if region == "" || awsRegionPattern.MatchString(region) {
		return nil
	}
	return fmt.Errorf("%s %q is not a valid AWS region (expected e.g. us-east-1)", field, region)
}

// llmProbeResultJSON mirrors the wire shape llm-server returns in
// /v1/llm-config/test-connection. Declared as a named type (rather than the
// anonymous struct it used to be inlined as) so the same shape can be reused
// across the JSON unmarshal, the aggregate-builder param, and the test
// helpers without 3-way drift on field renames.
type llmProbeResultJSON struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Source     string `json:"source"`
	OK         bool   `json:"ok"`
	Error      string `json:"error"`
	Untestable bool   `json:"untestable"`
}

// TestConnection runs a live connectivity probe against every (provider, model)
// pair in the configuration by delegating to llm-server's
// /v1/llm-config/test-connection. The UI's "Test Connection" button must verify
// not just the primary model but also every tier and agent override and each
// chain's fallbacks — a typo in any fallback would otherwise only manifest
// during a real failover in production.
//
// Per-target SDK errors are classified via humanizeProviderError so the UI
// gets actionable text instead of raw SDK strings. We additionally detect the
// case where our own multi-probe burst tripped the provider's rate limit
// (≥2 rate-limit failures against the same provider) and surface a dedicated
// "burst-rate-limited" message so the user retries rather than hunting for a
// nonexistent config bug.
//
// Untestable targets (vertexai) count as success — the resolver still picks
// them at runtime; only structural validation has run for them here.
func (m LLM) TestConnection(_ *security.SecurityContext, values []core.IntegrationConfigValue, _ string) error {
	if config.Config.LLMServerEndpoint == "" {
		return fmt.Errorf("llm_server_endpoint not configured; cannot run connectivity test")
	}

	cfg := make(map[string]string, len(values))
	for _, v := range values {
		cfg[v.Name] = v.Value
	}

	url := strings.TrimRight(config.Config.LLMServerEndpoint, "/") + "/v1/llm-config/test-connection"
	headers := map[string]string{"Content-Type": "application/json"}
	if config.Config.LLMServerTokenHeader != "" && config.Config.LLMServerToken != "" {
		headers[config.Config.LLMServerTokenHeader] = config.Config.LLMServerToken
	}

	resp, err := common.HttpPost(url,
		common.HttpWithJsonBody(map[string]any{"config": cfg}),
		common.HttpWithHeaders(headers),
		// 90s: 5-way parallel probe across ~25 models, 15s per-probe budget on
		// llm-server side, with slack for queueing and the response trip.
		common.HttpWithTimeout(90*time.Second),
	)
	if err != nil {
		return fmt.Errorf("could not reach llm-server: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("connectivity test failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		OK      bool                 `json:"ok"`
		Error   string               `json:"error"`
		Results []llmProbeResultJSON `json:"results"`
		Summary string               `json:"summary"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("connectivity test returned unparseable response: %w", err)
	}
	if parsed.OK {
		return nil
	}

	// Backwards-compat: if llm-server returned only the legacy top-level Error
	// (no per-target results), humanize it directly. New code paths always
	// populate Results, but legacy tests and any pre-upgrade llm-server
	// instances rely on this shape.
	if len(parsed.Results) == 0 {
		return errors.New(humanizeProviderError(parsed.Error))
	}

	// Detect burst rate-limit. The classification is per-failure based on the
	// raw error string. When the same provider racks up ≥2 rate-limit hits in
	// one probe burst, we assume it's our own concurrency-induced throttling
	// rather than a config bug, and tell the user to retry. Save stays blocked
	// (the test result is still "not OK") so they don't ship an unverified
	// config — they just need to wait and try again.
	rateLimitsByProvider := make(map[string]int)
	for _, r := range parsed.Results {
		if r.OK || r.Untestable {
			continue
		}
		if isRateLimitError(r.Error) {
			rateLimitsByProvider[r.Provider]++
		}
	}
	for prov, count := range rateLimitsByProvider {
		if count >= 2 {
			return fmt.Errorf(
				"%s rate-limited %d times during the connectivity test burst — wait a minute and retry. The models themselves may be valid; this is most likely caused by our parallel probes hitting the provider's rate limit, not your configuration",
				prov, count,
			)
		}
	}

	// No burst — build a per-target message. We surface the first failure with
	// its humanized error and source label, then append a count of additional
	// failures so the message stays terse but the user knows the scope.
	return errors.New(buildAggregateProbeError(parsed.Results, parsed.Summary))
}

// isRateLimitError matches the patterns humanizeProviderError uses to detect
// rate-limit shapes. Duplicated narrowly here so the burst detection runs
// against the raw error strings before humanization.
func isRateLimitError(raw string) bool {
	lower := strings.ToLower(raw)
	return containsHTTPStatus(lower, "429") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "rate_limit") ||
		strings.Contains(lower, "quota") ||
		strings.Contains(lower, "too many requests")
}

// containsHTTPStatus reports whether the given (lowercased) error message
// contains an HTTP status code reference of the form that provider SDKs
// actually emit: `Error NNN`, `code NNN`, `code: NNN`, `status NNN`,
// `status: NNN`, `http NNN`, leading `NNN ` token, or `NNN:` token.
//
// Bare-substring matching like strings.Contains(lower, "401") is too leaky —
// it triggers on model names (`claude-401-experimental`), resource IDs, or
// request IDs that happen to contain the same three digits. The patterns
// below cover what we've seen in real provider errors without paying false
// positives for in-band occurrences of the digit sequence.
func containsHTTPStatus(lowerMsg, code string) bool {
	if lowerMsg == "" || code == "" {
		return false
	}
	for _, p := range []string{
		"error " + code,    // googleapi: Error 401: ...
		"code " + code,     // openai shapes: code 429
		"code: " + code,    // googleapi structured: code: 401
		"status " + code,   // some SDKs: status 403
		"status: " + code,  // structured shapes
		"http " + code,     // generic: http 429
		"(" + code + ")",   // "(401)" wrapper
		code + " unauthor", // "401 Unauthorized"
		code + " forbid",   // "403 Forbidden"
		code + " too many", // "429 Too Many Requests"
		code + " bad",      // "400 Bad Request" not used but consistent
	} {
		if strings.Contains(lowerMsg, p) {
			return true
		}
	}
	// Leading-token match: "401 Incorrect API key provided" — the digit
	// sequence is the first token of the message.
	if strings.HasPrefix(lowerMsg, code+" ") {
		return true
	}
	return false
}

// buildAggregateProbeError condenses N per-target failures into a single
// user-actionable message: every failure (up to maxLines, default 5) with its
// source label so the user knows which field to fix. Failures are sorted by
// source rank (primary global → tier-primary → agent-primary → fallbacks of
// each) so the most-important error shows up first regardless of how the
// llm-server happened to order the results. Untestable targets (vertexai)
// are excluded — they don't represent real failures.
//
// The previous version showed only the first failure and a count of "and N
// other(s) failed", which made it easy for the user to miss a primary-model
// typo behind a fallback failure. We now surface every failure so the user
// fixes them all in one cycle.
func buildAggregateProbeError(results []llmProbeResultJSON, summary string) string {
	type failure struct {
		Provider, Model, Source, Error string
	}
	failures := make([]failure, 0)
	for _, r := range results {
		if r.OK || r.Untestable {
			continue
		}
		failures = append(failures, failure{r.Provider, r.Model, r.Source, r.Error})
	}
	if len(failures) == 0 {
		return "Connection test failed (no per-model details from llm-server)"
	}

	// Source-rank ordering: primary models first so a primary-model typo is
	// always the first line the user reads. Within the same rank, keep the
	// llm-server's deterministic ordering (alpha by provider+model).
	sourceRank := func(src string) int {
		// The source can be a comma-joined list when the same (provider, model)
		// is referenced from multiple places (e.g. "global, tier-summary").
		// Rank by the highest-priority (lowest rank) component.
		best := 99
		for _, s := range strings.Split(src, ",") {
			s = strings.TrimSpace(s)
			r := 99
			switch {
			case s == "global":
				r = 0
			case strings.HasPrefix(s, "tier-") && !strings.HasSuffix(s, "-fallback"):
				r = 1
			case strings.HasPrefix(s, "agent-") && !strings.HasSuffix(s, "-fallback"):
				r = 2
			case s == "global-fallback":
				r = 3
			case strings.HasPrefix(s, "tier-") && strings.HasSuffix(s, "-fallback"):
				r = 4
			case strings.HasPrefix(s, "agent-") && strings.HasSuffix(s, "-fallback"):
				r = 5
			}
			if r < best {
				best = r
			}
		}
		return best
	}
	sort.SliceStable(failures, func(i, j int) bool {
		return sourceRank(failures[i].Source) < sourceRank(failures[j].Source)
	})

	// Hard cap on visible lines — we want the user to see all their typos but
	// don't want a 50-line toast if they've got an unusually large config or
	// the provider had a transient outage.
	const maxLines = 5
	shown := failures
	hidden := 0
	if len(shown) > maxLines {
		hidden = len(shown) - maxLines
		shown = shown[:maxLines]
	}

	var b strings.Builder
	if len(failures) == 1 {
		f := shown[0]
		fmt.Fprintf(&b, "%s (%s · %s): %s", f.Provider, f.Model, f.Source, humanizeProviderError(f.Error))
	} else {
		fmt.Fprintf(&b, "%d model(s) failed connectivity test:", len(failures))
		for _, f := range shown {
			fmt.Fprintf(&b, "\n  - %s (%s · %s): %s", f.Provider, f.Model, f.Source, humanizeProviderError(f.Error))
		}
		if hidden > 0 {
			fmt.Fprintf(&b, "\n  ... and %d more failed", hidden)
		}
		if summary != "" {
			fmt.Fprintf(&b, "\n(%s)", summary)
		}
	}
	return b.String()
}

// humanizeProviderError maps the raw provider-SDK error strings that llm-server
// surfaces in its test-connection response to user-actionable text. Pattern
// matching only — we never invent details that aren't in the underlying error.
//
// Unknown shapes fall through to the original message (with obvious SDK noise
// prefixes stripped) so genuinely novel failures aren't hidden behind a
// generic toast.
//
// Patterns are roughly ordered most-specific → least-specific so an error that
// matches both "model not found" and "404" picks the more useful message.
func humanizeProviderError(raw string) string {
	msg := strings.TrimSpace(raw)
	if msg == "" {
		return "Connection test failed (no details from provider)"
	}
	lower := strings.ToLower(msg)

	// Model not found / unsupported — Google AI, OpenAI, Azure, Anthropic, Bedrock
	// all surface this shape with different wording.
	if strings.Contains(lower, "model") && (strings.Contains(lower, "not found") ||
		strings.Contains(lower, "does not exist") ||
		strings.Contains(lower, "is not supported") ||
		strings.Contains(lower, "unknown model")) {
		return "Model name not recognized by the provider — check the spelling and that the model is available for this account/region"
	}

	// Auth failures — 401 / "unauthorized" / "invalid api key".
	// HTTP-status patterns are matched in their `Error NNN` / `code NNN` /
	// `status NNN` / `NNN <message>` shapes so that a model name like
	// `claude-401-experimental` or a request-ID containing `401` doesn't
	// trigger a spurious "authentication failed" classification.
	if containsHTTPStatus(lower, "401") ||
		strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "invalid api key") ||
		strings.Contains(lower, "invalid_api_key") ||
		strings.Contains(lower, "incorrect api key") ||
		strings.Contains(lower, "api key not valid") ||
		strings.Contains(lower, "authentication failed") ||
		strings.Contains(lower, "invalid credentials") {
		return "Authentication failed — check that the API key / access key is correct and active"
	}

	// Permission denied — 403 / "access denied" / "forbidden".
	if containsHTTPStatus(lower, "403") ||
		strings.Contains(lower, "permission denied") ||
		strings.Contains(lower, "accessdenied") ||
		strings.Contains(lower, "access denied") ||
		strings.Contains(lower, "forbidden") {
		return "Permission denied — the API key is valid but lacks access to this model/region. Check IAM policies (Bedrock) or project access (Google AI / Vertex AI)"
	}

	// Rate limit / quota.
	if containsHTTPStatus(lower, "429") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "rate_limit") ||
		strings.Contains(lower, "quota") ||
		strings.Contains(lower, "too many requests") {
		return "Rate limit or quota exceeded — wait a minute and retry, or check your provider quota"
	}

	// Region / endpoint issues — Bedrock, Vertex AI.
	if strings.Contains(lower, "region") && (strings.Contains(lower, "not enabled") ||
		strings.Contains(lower, "not available") ||
		strings.Contains(lower, "invalid region")) {
		return "Region not enabled for this model — verify the region in the provider console and that the model is available there"
	}

	// Network — DNS, timeout, connection refused.
	if strings.Contains(lower, "no such host") ||
		strings.Contains(lower, "dial tcp") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "deadline exceeded") {
		return "Could not reach the provider endpoint — check the endpoint URL and network connectivity"
	}

	// Invalid endpoint URL (Azure / Vertex AI custom endpoint).
	if strings.Contains(lower, "endpoint") && (strings.Contains(lower, "invalid") ||
		strings.Contains(lower, "malformed") ||
		strings.Contains(lower, "not a valid")) {
		return "Endpoint URL is invalid — must be a full https:// URL of the provider's API endpoint"
	}

	// Strip obvious SDK noise prefixes as a safety net so the raw error stays
	// readable for unknown shapes. `stripTypeName=true` peels the
	// `TypeName: ` suffix that namespaced shapes (e.g.
	// `google.api_core.exceptions.NotFound: 404 ...`) leave behind. For
	// non-namespaced wrappers like `googleapi: Error 500: foo` the rest of
	// the message is already user-visible information ("Error 500: foo")
	// and must not be peeled further.
	noisePrefixes := []struct {
		prefix        string
		stripTypeName bool
	}{
		{"googleapi: ", false},
		{"google.api_core.exceptions.", true},
		{"operation error ", false},
		{"botocore.exceptions.", true},
		{"openai.error.", true},
		{"openai.AuthenticationError: ", false},
		{"anthropic.AuthenticationError: ", false},
	}
	for _, p := range noisePrefixes {
		if strings.HasPrefix(msg, p.prefix) {
			msg = strings.TrimPrefix(msg, p.prefix)
			if p.stripTypeName {
				if idx := strings.Index(msg, ": "); idx > 0 && idx < 40 {
					msg = msg[idx+2:]
				}
			}
			break
		}
	}
	return "Provider connectivity failed: " + msg
}

func isLLMProvider(p string) bool {
	for _, v := range llmProviders {
		if v == p {
			return true
		}
	}
	return false
}

func isValidHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// AllowsDynamicKey lets the LLM integration accept per-tier and per-agent
// override keys that aren't enumerated in the static schema. The agent list
// lives in llm-server (registered via RegisterNBAgentFactory) and is surfaced
// to the UI via the ai_list_agents Hasura action; we don't duplicate it here.
// Validation is purely pattern-based — any key matching one of these prefixes
// is accepted as an override.
//
// Accepted prefixes (each matches the literal prefix plus a non-empty suffix):
//   - llm_tier_provider_<tier>                (provider for a category tier)
//   - llm_tier_model_<tier>                   (model for a tier; also matches
//     llm_tier_model_fallbacks_<tier>
//     via prefix overlap)
//   - llm_provider_<...>                      (broad: covers llm_provider_<agent>
//     AND every per-agent secret/region
//     key the resolver reads:
//     llm_provider_api_key_<agent>,
//     llm_provider_api_endpoint_<agent>,
//     llm_provider_api_version_<agent>,
//     llm_provider_api_type_<agent>,
//     llm_provider_region_<agent>,
//     llm_provider_access_key_<agent>,
//     llm_provider_secret_key_<agent>,
//     llm_provider_session_token_<agent>,
//     llm_provider_adapter_id_<agent>)
//   - llm_model_name_<agent>                  (model override for one agent)
//   - llm_model_fallbacks_<agent>             (fallbacks for one agent)
//
// The prefix-overlap pattern is intentional: it lets the UI write any new
// agent-scoped override without requiring a Go-side schema update each time
// the resolver learns a new field.
func (m LLM) AllowsDynamicKey(name string) bool {
	overridePrefixes := []string{
		"llm_tier_provider_",
		"llm_tier_model_",
		"llm_provider_",
		"llm_model_name_",
		"llm_model_fallbacks_",
	}
	for _, p := range overridePrefixes {
		if strings.HasPrefix(name, p) && len(name) > len(p) {
			return true
		}
	}
	return false
}
