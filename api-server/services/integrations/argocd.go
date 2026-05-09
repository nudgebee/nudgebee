package integrations

import (
	"fmt"
	"nudgebee/services/integrations/core"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"regexp"
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

const argoCDServerURLPattern = `^https?://[A-Za-z0-9._-]+(?::\d+)?(?:/[^\s]*)?$`

var (
	argoCDServerURLRegex = regexp.MustCompile(argoCDServerURLPattern)
	argoCDEnvKeyRegex    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
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
				Pattern:     argoCDServerURLPattern,
				Priority:    90,
				IsTestable:  true,
			},
			ArgoCDConfigK8sSecret: {
				Type:        core.ToolSchemaTypeString,
				Description: "ArgoCD Secret in k8s. Required Keys: ARGOCD_SERVER and either ARGOCD_AUTH_TOKEN (token auth) or ARGOCD_USERNAME + ARGOCD_PASSWORD (password auth)",
				Priority:    80,
				IsTestable:  true,
			},
			"account_id": {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Accounts",
				Default:          nil,
				AutoGenerateFunc: "listAccounts",
				Priority:         75,
			},
			ArgoCDConfigAuthMethod: {
				Type:        core.ToolSchemaTypeString,
				Description: "Authentication method",
				Default:     "token",
				Enum:        []any{"token", "password"},
				Priority:    70,
				IsTestable:  true,
			},
			ArgoCDConfigAuthTokenKeyInSecret: {
				Type:         core.ToolSchemaTypeString,
				Description:  "Key name for auth token in the secret",
				Default:      "ARGOCD_AUTH_TOKEN",
				Priority:     60,
				ShowWhen:     map[string]any{ArgoCDConfigAuthMethod: []any{"token"}},
				RequiredWhen: map[string]any{ArgoCDConfigAuthMethod: []any{"token"}},
				IsTestable:   true,
			},
			ArgoCDConfigUsernameKeyInSecret: {
				Type:         core.ToolSchemaTypeString,
				Description:  "Key name for username in the secret",
				Default:      "ARGOCD_USERNAME",
				Priority:     60,
				ShowWhen:     map[string]any{ArgoCDConfigAuthMethod: []any{"password"}},
				RequiredWhen: map[string]any{ArgoCDConfigAuthMethod: []any{"password"}},
				IsTestable:   true,
			},
			ArgoCDConfigPasswordKeyInSecret: {
				Type:         core.ToolSchemaTypeString,
				Description:  "Key name for password in the secret",
				Default:      "ARGOCD_PASSWORD",
				Priority:     55,
				ShowWhen:     map[string]any{ArgoCDConfigAuthMethod: []any{"password"}},
				RequiredWhen: map[string]any{ArgoCDConfigAuthMethod: []any{"password"}},
				IsTestable:   true,
			},
			ArgoCDConfigServerKeyInSecret: {
				Type:        core.ToolSchemaTypeString,
				Description: "Key name for server URL in the secret",
				Default:     "ARGOCD_SERVER",
				Priority:    50,
				IsTestable:  true,
			},
			ArgoCDConfigInsecure: {
				Type:        core.ToolSchemaTypeString,
				Description: "Skip TLS certificate verification (true/false)",
				Default:     "false",
				Enum:        []any{"true", "false"},
				Priority:    10,
				IsTestable:  true,
			},
			ArgoCDConfigTimeout: {
				Type:        core.ToolSchemaTypeString,
				Description: "Command timeout in seconds",
				Default:     "30",
				Priority:    5,
			},
		},
	}
}

func (m ArgoCD) ValidateConfig(securityContext *security.SecurityContext, configs []core.IntegrationConfigValue, accountId string) []error {
	configMap := make(map[string]string, len(configs))
	for _, c := range configs {
		configMap[c.Name] = c.Value
	}

	secretName := configMap[ArgoCDConfigK8sSecret]
	serverURL := configMap[ArgoCDConfigServer]
	authMethod := firstNonEmpty(configMap[ArgoCDConfigAuthMethod], "token")
	insecure := firstNonEmpty(configMap[ArgoCDConfigInsecure], "false")
	serverKey := firstNonEmpty(configMap[ArgoCDConfigServerKeyInSecret], "ARGOCD_SERVER")
	authTokenKey := firstNonEmpty(configMap[ArgoCDConfigAuthTokenKeyInSecret], "ARGOCD_AUTH_TOKEN")
	usernameKey := firstNonEmpty(configMap[ArgoCDConfigUsernameKeyInSecret], "ARGOCD_USERNAME")
	passwordKey := firstNonEmpty(configMap[ArgoCDConfigPasswordKeyInSecret], "ARGOCD_PASSWORD")

	if secretName == "" {
		return []error{fmt.Errorf("k8s_secret is required")}
	}
	if serverURL == "" {
		return []error{fmt.Errorf("server is required")}
	}
	if !argoCDServerURLRegex.MatchString(serverURL) {
		return []error{fmt.Errorf("invalid server URL: must start with http:// or https:// (e.g., https://argocd.example.com)")}
	}
	if !argoCDEnvKeyRegex.MatchString(serverKey) {
		return []error{fmt.Errorf("invalid server_key_in_secret: must match %s", argoCDEnvKeyRegex.String())}
	}

	envFromSecret := map[string]string{serverKey: serverKey}

	switch authMethod {
	case "token":
		if !argoCDEnvKeyRegex.MatchString(authTokenKey) {
			return []error{fmt.Errorf("invalid auth_token_key_in_secret: must match %s", argoCDEnvKeyRegex.String())}
		}
		envFromSecret[authTokenKey] = authTokenKey
	case "password":
		if !argoCDEnvKeyRegex.MatchString(usernameKey) {
			return []error{fmt.Errorf("invalid username_key_in_secret: must match %s", argoCDEnvKeyRegex.String())}
		}
		if !argoCDEnvKeyRegex.MatchString(passwordKey) {
			return []error{fmt.Errorf("invalid password_key_in_secret: must match %s", argoCDEnvKeyRegex.String())}
		}
		envFromSecret[usernameKey] = usernameKey
		envFromSecret[passwordKey] = passwordKey
	default:
		return []error{fmt.Errorf("invalid auth_method %q: must be 'token' or 'password'", authMethod)}
	}

	insecureFlag := ""
	if strings.EqualFold(insecure, "true") {
		insecureFlag = " --insecure"
	}

	var cmd string
	switch authMethod {
	case "token":
		cmd = fmt.Sprintf(
			`argocd account get-user-info --grpc-web%s --server "$%s" --auth-token "$%s" --output json`,
			insecureFlag, serverKey, authTokenKey,
		)
	case "password":
		cmd = fmt.Sprintf(
			`argocd login "$%s" --grpc-web%s --username "$%s" --password "$%s" && `+
				`argocd account get-user-info --grpc-web%s --server "$%s" --output json`,
			serverKey, insecureFlag, usernameKey, passwordKey, insecureFlag, serverKey,
		)
	}

	resp, err := relay.CommandExecutor(accountId, cmd, secretName, envFromSecret)
	if err != nil {
		return core.HandleRelayTimeoutError(err)
	}

	respStr, ok := resp["response"].(string)
	if !ok {
		return []error{fmt.Errorf("unexpected response format from argocd server: %v", resp)}
	}

	if errMsg := detectArgoCDAuthError(respStr); errMsg != "" {
		return []error{fmt.Errorf("argocd validation failed: %s", errMsg)}
	}
	if !strings.Contains(respStr, `"loggedIn":true`) && !strings.Contains(respStr, `"loggedIn": true`) {
		return []error{fmt.Errorf("argocd validation failed: unexpected response: %s", strings.TrimSpace(respStr))}
	}

	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func detectArgoCDAuthError(resp string) string {
	lower := strings.ToLower(resp)
	patterns := []string{
		"unauthenticated",
		"unauthorized",
		"permission denied",
		"invalid username or password",
		"failed to establish connection",
		"x509: certificate",
		"no such host",
		"connection refused",
		"rpc error",
		"fata[",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return strings.TrimSpace(resp)
		}
	}
	return ""
}
