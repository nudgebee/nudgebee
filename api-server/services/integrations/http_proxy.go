package integrations

import (
	"fmt"
	"net/url"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(HTTPProxy{})
}

const IntegrationHTTPProxy = "http_proxy"

type HTTPProxy struct{}

func (m HTTPProxy) Name() string {
	return IntegrationHTTPProxy
}

func (m HTTPProxy) Category() core.IntegrationCategory {
	return core.IntegrationCategoryProxy
}

func (m HTTPProxy) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{"base_url"},
		Properties: map[string]core.IntegrationSchemaProperty{
			"proxy_type": {
				Type:     core.ToolSchemaTypeString,
				Default:  "http-proxy",
				Hidden:   true,
				Priority: 4,
			},
			"base_url": {
				Type:        core.ToolSchemaTypeString,
				Description: "Base URL of the HTTP endpoint (e.g., http://prometheus.internal:9090)",
				Priority:    85,
			},
			"auth_type": {
				Type:        core.ToolSchemaTypeString,
				Description: "Authentication method",
				Default:     "none",
				Enum:        []any{"none", "basic", "bearer", "custom_header"},
				Priority:    92,
			},
			"username": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Username for basic auth",
				ShowWhen:     map[string]any{"auth_type": "basic"},
				RequiredWhen: map[string]any{"auth_type": "basic"},
				Priority:     72,
			},
			"password": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Password for basic auth",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"auth_type": "basic"},
				RequiredWhen: map[string]any{"auth_type": "basic"},
				Priority:     70,
			},
			"bearer_token": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Bearer token",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"auth_type": "bearer"},
				RequiredWhen: map[string]any{"auth_type": "bearer"},
				Priority:     68,
			},
			"custom_header_name": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Custom header name (e.g., X-Api-Key)",
				ShowWhen:     map[string]any{"auth_type": "custom_header"},
				RequiredWhen: map[string]any{"auth_type": "custom_header"},
				Priority:     66,
			},
			"custom_header_value": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Custom header value",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"auth_type": "custom_header"},
				RequiredWhen: map[string]any{"auth_type": "custom_header"},
				Priority:     64,
			},
			"tls_skip_verify": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Skip TLS certificate verification (not recommended for production)",
				Default:     false,
				Priority:    30,
			},
			"credential_source": {
				Type:        core.ToolSchemaTypeString,
				Description: "Where credentials are stored",
				Default:     "cloud_push",
				Enum:        []any{"cloud_push", "aws_sm", "gcp_sm", "azure_kv", "local"},
				Priority:    90,
			},
			"secret_ref": {
				Type:        core.ToolSchemaTypeString,
				Description: "Secret reference in the secret manager",
				ShowWhen:    map[string]any{"credential_source": []any{"aws_sm", "gcp_sm", "azure_kv"}},
				Priority:    62,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of HTTP Proxy integration",
				Priority:    100,
			},
		},
	}
}

func (m HTTPProxy) ValidateConfig(_ *security.SecurityContext, config []core.IntegrationConfigValue, _ string) []error {
	configMap := make(map[string]string)
	for _, c := range config {
		configMap[c.Name] = c.Value
	}

	var errs []error

	baseURL := configMap["base_url"]
	if baseURL == "" {
		errs = append(errs, fmt.Errorf("base_url is required"))
	} else {
		if _, err := url.ParseRequestURI(baseURL); err != nil {
			errs = append(errs, fmt.Errorf("base_url is not a valid URL"))
		}
	}

	authType := configMap["auth_type"]
	if authType == "" {
		authType = "none"
	}

	credSource := configMap["credential_source"]
	if credSource == "" {
		credSource = "cloud_push"
	}

	// Only validate auth fields when using cloud_push credentials
	if credSource == "cloud_push" {
		switch authType {
		case "basic":
			if configMap["username"] == "" {
				errs = append(errs, fmt.Errorf("username is required for basic auth"))
			}
			if configMap["password"] == "" {
				errs = append(errs, fmt.Errorf("password is required for basic auth"))
			}
		case "bearer":
			if configMap["bearer_token"] == "" {
				errs = append(errs, fmt.Errorf("bearer_token is required for bearer auth"))
			}
		case "custom_header":
			if configMap["custom_header_name"] == "" {
				errs = append(errs, fmt.Errorf("custom_header_name is required for custom header auth"))
			}
			if configMap["custom_header_value"] == "" {
				errs = append(errs, fmt.Errorf("custom_header_value is required for custom header auth"))
			}
		}
	}

	if credSource == "aws_sm" || credSource == "gcp_sm" || credSource == "azure_kv" {
		if configMap["secret_ref"] == "" {
			errs = append(errs, fmt.Errorf("secret_ref is required for %s credential source", credSource))
		}
	}

	return errs
}
