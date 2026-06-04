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
	ArgoCDConfigGrpcWeb              = "grpc_web"
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
		Required: []string{ArgoCDConfigK8sSecret},
		Properties: map[string]core.IntegrationSchemaProperty{
			"integration_config_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "Name for ArgoCD Integration",
				Default:     "",
				Priority:    100,
			},
			ArgoCDConfigK8sSecret: {
				Type:        core.ToolSchemaTypeString,
				Description: "ArgoCD Secret in k8s. Required Keys: ARGOCD_SERVER (server URL) and either ARGOCD_AUTH_TOKEN (token auth) or ARGOCD_USERNAME + ARGOCD_PASSWORD (password auth)",
				Priority:    90,
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
			ArgoCDConfigGrpcWeb: {
				Type:        core.ToolSchemaTypeString,
				Description: "Use gRPC-Web protocol — required when the ArgoCD server is reached through an HTTP/1.1 ingress/proxy (the usual case). Set false only for a direct gRPC endpoint.",
				Default:     "true",
				Enum:        []any{"true", "false"},
				Priority:    15,
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
	configMap := make(map[string]string, len(configs))
	for _, c := range configs {
		configMap[c.Name] = c.Value
	}

	secretName := configMap[ArgoCDConfigK8sSecret]
	authMethod := firstNonEmpty(configMap[ArgoCDConfigAuthMethod], "token")
	insecure := firstNonEmpty(configMap[ArgoCDConfigInsecure], "false")
	grpcWeb := firstNonEmpty(configMap[ArgoCDConfigGrpcWeb], "true")
	serverKey := firstNonEmpty(configMap[ArgoCDConfigServerKeyInSecret], "ARGOCD_SERVER")
	authTokenKey := firstNonEmpty(configMap[ArgoCDConfigAuthTokenKeyInSecret], "ARGOCD_AUTH_TOKEN")
	usernameKey := firstNonEmpty(configMap[ArgoCDConfigUsernameKeyInSecret], "ARGOCD_USERNAME")
	passwordKey := firstNonEmpty(configMap[ArgoCDConfigPasswordKeyInSecret], "ARGOCD_PASSWORD")

	if secretName == "" {
		return []error{fmt.Errorf("k8s_secret is required")}
	}

	// The ArgoCD server URL is sourced from the k8s secret (ARGOCD_SERVER key),
	// not a separate config field — the probe and the runtime playbook both read
	// it from the secret so there is a single source of truth.
	envFromSecret := map[string]string{serverKey: serverKey}

	switch authMethod {
	case "token":
		envFromSecret[authTokenKey] = authTokenKey
	case "password":
		envFromSecret[usernameKey] = usernameKey
		envFromSecret[passwordKey] = passwordKey
	default:
		return []error{fmt.Errorf("invalid auth_method %q: must be 'token' or 'password'", authMethod)}
	}

	// gRPC-Web is required when the server is fronted by an HTTP/1.1 ingress/proxy
	// (the common deployment). It is opt-out via the grpc_web field for the rare
	// direct-gRPC endpoint, where forcing --grpc-web would break the handshake.
	flags := ""
	if !strings.EqualFold(grpcWeb, "false") {
		flags += " --grpc-web"
	}
	if strings.EqualFold(insecure, "true") {
		flags += " --insecure"
	}

	var cmd string
	switch authMethod {
	case "token":
		cmd = fmt.Sprintf(
			`argocd account get-user-info%s --server "$%s" --auth-token "$%s" --output json`,
			flags, serverKey, authTokenKey,
		)
	case "password":
		cmd = fmt.Sprintf(
			`argocd login "$%s"%s --username "$%s" --password "$%s" && `+
				`argocd account get-user-info%s --server "$%s" --output json`,
			serverKey, flags, usernameKey, passwordKey, flags, serverKey,
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

	if strings.Contains(respStr, `"loggedIn":true`) || strings.Contains(respStr, `"loggedIn": true`) {
		return nil
	}

	if errMsg := detectArgoCDAuthError(respStr); errMsg != "" {
		return []error{fmt.Errorf("argocd validation failed: %s", errMsg)}
	}

	return []error{fmt.Errorf("argocd validation failed: unexpected response: %s", strings.TrimSpace(respStr))}
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
