package integrations

import (
	"fmt"
	"nudgebee/services/integrations/core"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"strings"
)

const (
	ArgoCDConfigServer               = "server"
	ArgoCDConfigInsecure             = "insecure"
	ArgoCDConfigTimeout              = "timeout"
	ArgoCDConfigK8sSecret            = "k8s_secret"
	ArgoCDConfigServerKeyInSecret    = "server_key_in_secret"
	ArgoCDConfigAuthTokenKeyInSecret = "auth_token_key_in_secret"
	ArgoCDConfigUsernameKeyInSecret  = "username_key_in_secret"
	ArgoCDConfigPasswordKeyInSecret  = "password_key_in_secret"
	ArgoCDConfigAuthMethod           = "auth_method"
)

func init() {
	core.RegisterIntegration(ArgoCD{})
}

const IntegrationArgoCD = "argocd"

type ArgoCD struct {
}

func (m ArgoCD) Name() string {
	return IntegrationArgoCD
}

func (m ArgoCD) Category() core.IntegrationCategory {
	return core.IntegrationCategoryCICD
}

func (m ArgoCD) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Testable: true,
		Required: []string{ArgoCDConfigK8sSecret, ArgoCDConfigServer},
		Properties: map[string]core.IntegrationSchemaProperty{
			"integration_config_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "Name for ArgoCD Integration",
				Default:     "",
				Priority:    100,
			},
			ArgoCDConfigServer: {
				Type:        core.ToolSchemaTypeString,
				Description: "ArgoCD Server URL (e.g., https://argocd.example.com)",
				Priority:    90,
				IsTestable:  true,
			},
			ArgoCDConfigK8sSecret: {
				Type:        core.ToolSchemaTypeString,
				Description: "ArgoCD Secret in k8s. Required Keys: ARGOCD_SERVER and either ARGOCD_AUTH_TOKEN (token auth) or ARGOCD_USERNAME + ARGOCD_PASSWORD (password auth)",
				Priority:    85,
				IsTestable:  true,
			},
			"account_id": {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Accounts",
				Default:          nil,
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			ArgoCDConfigAuthMethod: {
				Type:        core.ToolSchemaTypeString,
				Description: "Authentication method",
				Default:     "token",
				Enum:        []any{"token", "password"},
				Priority:    80,
				IsTestable:  true,
			},
			ArgoCDConfigAuthTokenKeyInSecret: {
				Type:         core.ToolSchemaTypeString,
				Description:  "Key name for auth token in the secret",
				Default:      "ARGOCD_AUTH_TOKEN",
				Priority:     75,
				ShowWhen:     map[string]any{ArgoCDConfigAuthMethod: []any{"token"}},
				RequiredWhen: map[string]any{ArgoCDConfigAuthMethod: []any{"token"}},
				IsTestable:   true,
			},
			ArgoCDConfigUsernameKeyInSecret: {
				Type:         core.ToolSchemaTypeString,
				Description:  "Key name for username in the secret",
				Default:      "ARGOCD_USERNAME",
				Priority:     75,
				ShowWhen:     map[string]any{ArgoCDConfigAuthMethod: []any{"password"}},
				RequiredWhen: map[string]any{ArgoCDConfigAuthMethod: []any{"password"}},
				IsTestable:   true,
			},
			ArgoCDConfigPasswordKeyInSecret: {
				Type:         core.ToolSchemaTypeString,
				Description:  "Key name for password in the secret",
				Default:      "ARGOCD_PASSWORD",
				Priority:     70,
				ShowWhen:     map[string]any{ArgoCDConfigAuthMethod: []any{"password"}},
				RequiredWhen: map[string]any{ArgoCDConfigAuthMethod: []any{"password"}},
				IsTestable:   true,
			},
			ArgoCDConfigServerKeyInSecret: {
				Type:        core.ToolSchemaTypeString,
				Description: "Key name for server URL in the secret",
				Default:     "ARGOCD_SERVER",
				Priority:    65,
				IsTestable:  true,
			},
			ArgoCDConfigInsecure: {
				Type:        core.ToolSchemaTypeString,
				Description: "Skip TLS certificate verification (true/false)",
				Default:     "false",
				Enum:        []any{"true", "false"},
				Priority:    20,
				IsTestable:  true,
			},
			ArgoCDConfigTimeout: {
				Type:        core.ToolSchemaTypeString,
				Description: "Command timeout in seconds",
				Default:     "30",
				Priority:    10,
			},
		},
	}
}

func (m ArgoCD) ValidateConfig(securityContext *security.SecurityContext, configs []core.IntegrationConfigValue, accountId string) []error {

	secretName := ""
	serverKey := "ARGOCD_SERVER"
	authTokenKey := "ARGOCD_AUTH_TOKEN"
	usernameKey := "ARGOCD_USERNAME"
	passwordKey := "ARGOCD_PASSWORD"
	authMethod := "password"
	insecure := "false"

	// Extract configuration values
	for _, config := range configs {
		switch config.Name {
		case ArgoCDConfigK8sSecret:
			secretName = config.Value
		case ArgoCDConfigServerKeyInSecret:
			if config.Value != "" {
				serverKey = config.Value
			}
		case ArgoCDConfigAuthTokenKeyInSecret:
			if config.Value != "" {
				authTokenKey = config.Value
			}
		case ArgoCDConfigUsernameKeyInSecret:
			if config.Value != "" {
				usernameKey = config.Value
			}
		case ArgoCDConfigPasswordKeyInSecret:
			if config.Value != "" {
				passwordKey = config.Value
			}
		case ArgoCDConfigAuthMethod:
			if config.Value != "" {
				authMethod = config.Value
			}
		case ArgoCDConfigInsecure:
			if config.Value != "" {
				insecure = config.Value
			}
		}
	}

	if secretName == "" {
		return []error{fmt.Errorf("k8s_secret is required")}
	}

	// Build environment variables map
	envFromSecret := make(map[string]string)
	envFromSecret[serverKey] = serverKey

	if authMethod == "password" {
		envFromSecret[usernameKey] = usernameKey
		envFromSecret[passwordKey] = passwordKey
	} else {
		envFromSecret[authTokenKey] = authTokenKey
	}

	// Build argocd command with appropriate flags
	var argoCDCmd strings.Builder
	argoCDCmd.WriteString("argocd version --client")

	if insecure == "true" {
		argoCDCmd.WriteString(" --insecure")
	}

	// Execute the command to validate connection
	resp, err := relay.CommandExecutor(accountId, argoCDCmd.String(), secretName, envFromSecret)

	if err != nil {
		return core.HandleRelayTimeoutError(err)
	}

	respStr, ok := resp["response"].(string)
	if !ok {
		return []error{fmt.Errorf("unexpected response format from argocd server: %v", resp)}
	}

	// Check if the response contains the expected ArgoCD version format
	if !strings.Contains(respStr, "argocd: v") || !strings.Contains(respStr, "BuildDate:") {
		return []error{fmt.Errorf("validation failed: expected ArgoCD version information not found in response")}
	}

	return []error{}
}
