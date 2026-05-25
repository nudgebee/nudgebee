package integrations

import (
	"fmt"
	"net/url"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(MCP{})
}

const IntegrationMCP = "mcp"

type MCP struct{}

func (m MCP) Name() string {
	return IntegrationMCP
}

func (m MCP) Category() core.IntegrationCategory {
	return core.IntegrationCategoryProxy
}

func (m MCP) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.IntegrationSchemaProperty{
			"connection_mode": {
				Type:        core.ToolSchemaTypeString,
				Description: "Connection mode",
				Default:     "direct",
				Enum:        []any{"direct", "vm_agent"},
				Priority:    100,
			},
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of MCP integration",
				Priority:    95,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         90,
			},
			// Transport selection
			"transport": {
				Type:        core.ToolSchemaTypeString,
				Description: "MCP server transport type",
				Default:     "http",
				Enum:        []any{"http"},
				Priority:    85,
			},
			// HTTP transport fields (both direct and vm_agent)
			"url": {
				Type:         core.ToolSchemaTypeString,
				Description:  "URL of the MCP server",
				ShowWhen:     map[string]any{"transport": "http"},
				RequiredWhen: map[string]any{"transport": "http"},
				Priority:     80,
			},
			// Auth fields (direct mode)
			"auth_type": {
				Type:        core.ToolSchemaTypeString,
				Description: "Authentication type",
				Default:     "none",
				Enum:        []any{"none", "bearer", "basic", "api_key", "custom_header", "oauth2"},
				ShowWhen:    map[string]any{"connection_mode": "direct", "transport": "http"},
				Priority:    70,
			},
			"bearer_token": {
				Type:        core.ToolSchemaTypeString,
				Description: "Bearer token",
				IsEncrypted: true,
				ShowWhen:    map[string]any{"auth_type": "bearer", "connection_mode": "direct"},
				Priority:    65,
			},
			"username": {
				Type:     core.ToolSchemaTypeString,
				ShowWhen: map[string]any{"auth_type": "basic", "connection_mode": "direct"},
				Priority: 65,
			},
			"password": {
				Type:        core.ToolSchemaTypeString,
				IsEncrypted: true,
				ShowWhen:    map[string]any{"auth_type": "basic", "connection_mode": "direct"},
				Priority:    64,
			},
			"custom_header_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "Custom header name",
				ShowWhen:    map[string]any{"auth_type": []any{"api_key", "custom_header"}, "connection_mode": "direct"},
				Priority:    65,
			},
			"custom_header_value": {
				Type:        core.ToolSchemaTypeString,
				Description: "Custom header value",
				IsEncrypted: true,
				ShowWhen:    map[string]any{"auth_type": []any{"api_key", "custom_header"}, "connection_mode": "direct"},
				Priority:    64,
			},
			// OAuth 2.0 client_credentials fields
			"oauth_token_url": {
				Type:         core.ToolSchemaTypeString,
				Description:  "OAuth 2.0 token endpoint URL",
				ShowWhen:     map[string]any{"auth_type": "oauth2", "connection_mode": "direct"},
				RequiredWhen: map[string]any{"auth_type": "oauth2"},
				Priority:     65,
			},
			"oauth_client_id": {
				Type:         core.ToolSchemaTypeString,
				Description:  "OAuth 2.0 client ID",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"auth_type": "oauth2", "connection_mode": "direct"},
				RequiredWhen: map[string]any{"auth_type": "oauth2"},
				Priority:     64,
			},
			"oauth_client_secret": {
				Type:         core.ToolSchemaTypeString,
				Description:  "OAuth 2.0 client secret",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"auth_type": "oauth2", "connection_mode": "direct"},
				RequiredWhen: map[string]any{"auth_type": "oauth2"},
				Priority:     63,
			},
			"oauth_scope": {
				Type:        core.ToolSchemaTypeString,
				Description: "OAuth 2.0 scope (space-separated)",
				ShowWhen:    map[string]any{"auth_type": "oauth2", "connection_mode": "direct"},
				Priority:    62,
			},
			"oauth_audience": {
				Type:        core.ToolSchemaTypeString,
				Description: "OAuth 2.0 audience / resource",
				ShowWhen:    map[string]any{"auth_type": "oauth2", "connection_mode": "direct"},
				Priority:    61,
			},
			// VM agent credential fields
			"credential_source": {
				Type:        core.ToolSchemaTypeString,
				Description: "Where MCP server credentials are stored",
				Default:     "cloud_push",
				Enum:        []any{"cloud_push", "local"},
				ShowWhen:    map[string]any{"connection_mode": "vm_agent", "transport": "http"},
				Priority:    70,
			},
			// LLM instructions
			"llm_instructions": {
				Type:        core.ToolSchemaTypeString,
				Description: "Instructions for the LLM agent on when and how to use this MCP server's tools",
				Multiline:   true,
				Priority:    30,
			},
			// Hidden proxy_type for vm_agent mode config push
			"proxy_type": {
				Type:    core.ToolSchemaTypeString,
				Default: "mcp-proxy",
				Hidden:  true,
			},
		},
	}
}

func (m MCP) ValidateConfig(_ *security.SecurityContext, config []core.IntegrationConfigValue, _ string) []error {
	configMap := make(map[string]string)
	for _, c := range config {
		configMap[c.Name] = c.Value
	}

	transport := configMap["transport"]
	if transport == "" {
		transport = "http"
	}

	var errs []error

	switch transport {
	case "http":
		if configMap["url"] == "" {
			errs = append(errs, fmt.Errorf("url is required for HTTP transport"))
		} else {
			if _, err := url.ParseRequestURI(configMap["url"]); err != nil {
				errs = append(errs, fmt.Errorf("url is not a valid URL"))
			}
		}
	default:
		errs = append(errs, fmt.Errorf("unsupported transport: %s", transport))
	}

	return errs
}
