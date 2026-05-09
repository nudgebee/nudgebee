package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

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
					"llm_provider": []string{"azure", "sagemaker", "huggingface", "anthropic"},
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
					"llm_provider":                []string{"azure", "sagemaker", "huggingface", "anthropic"},
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
	return []error{}
}
