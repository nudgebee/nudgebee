package integrations

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"regexp"
	"strings"
	"time"
)

// awsRegionPattern is a basic shape check for AWS regions (e.g. us-east-1,
// eu-west-2). It is intentionally not a full AWS region whitelist.
var awsRegionPattern = regexp.MustCompile(`^[a-z]{2}-[a-z]+-\d+$`)

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
		// hits integrations_test_connection_config, which now routes LLM through
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

// TestConnection runs a live connectivity probe against the configured LLM
// provider by delegating to llm-server's /v1/llm-config/test-connection.
// Structural validation has already run by the time this is called (see
// TestableIntegration plumbing in core/integration_config.go). Implements
// core.TestableIntegration.
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
		common.HttpWithTimeout(30*time.Second),
	)
	if err != nil {
		return fmt.Errorf("connectivity test request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("connectivity test failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("connectivity test returned unparseable response: %w", err)
	}
	if !parsed.OK {
		return fmt.Errorf("provider connectivity failed: %s", parsed.Error)
	}
	return nil
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
