package integrations

import (
	"fmt"
	"nudgebee/services/integrations/core"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"regexp"
	"strings"
)

// argoCDSecretNameRegex bounds the k8s secret name that gets handed to the relay
// agent (optionally namespaced as "namespace/name"). The auth/server env-var keys
// are no longer user-controlled — they are fixed constants — so the only
// user-supplied value reaching the relay is the secret name; keep it to k8s-safe
// characters as defense-in-depth.
var argoCDSecretNameRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+(/[a-zA-Z0-9._-]+)?$`)

func isValidSecretName(name string) bool {
	return argoCDSecretNameRegex.MatchString(name)
}

const (
	ArgoCDConfigInsecure   = "insecure"
	ArgoCDConfigGrpcWeb    = "grpc_web"
	ArgoCDConfigTimeout    = "timeout"
	ArgoCDConfigK8sSecret  = "k8s_secret"
	ArgoCDConfigAuthMethod = "auth_method"
)

// Standard ArgoCD CLI env-var keys expected inside the k8s secret. They are not
// configurable: the argocd CLI reads exactly these names, so a "key name" config
// field would only let a user point at a key the CLI can't consume.
const (
	argoCDSecretKeyServer    = "ARGOCD_SERVER"
	argoCDSecretKeyAuthToken = "ARGOCD_AUTH_TOKEN"
	argoCDSecretKeyUsername  = "ARGOCD_USERNAME"
	argoCDSecretKeyPassword  = "ARGOCD_PASSWORD"
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

	if secretName == "" {
		return []error{fmt.Errorf("k8s_secret is required")}
	}
	if !isValidSecretName(secretName) {
		return []error{fmt.Errorf("invalid k8s_secret name %q: must contain only alphanumeric characters, dashes, dots, underscores, or a single slash", secretName)}
	}

	// Server URL and credentials are all read from the k8s secret under the
	// standard ArgoCD CLI env-var keys — a single source of truth. The CLI reads
	// $ARGOCD_SERVER for --server.
	envFromSecret := map[string]string{argoCDSecretKeyServer: argoCDSecretKeyServer}

	switch authMethod {
	case "token":
		envFromSecret[argoCDSecretKeyAuthToken] = argoCDSecretKeyAuthToken
	case "password":
		envFromSecret[argoCDSecretKeyUsername] = argoCDSecretKeyUsername
		envFromSecret[argoCDSecretKeyPassword] = argoCDSecretKeyPassword
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
			`argocd account get-user-info%s --server "$ARGOCD_SERVER" --auth-token "$ARGOCD_AUTH_TOKEN" --output json`,
			flags,
		)
	case "password":
		cmd = fmt.Sprintf(
			`argocd login "$ARGOCD_SERVER"%s --username "$ARGOCD_USERNAME" --password "$ARGOCD_PASSWORD" && `+
				`argocd account get-user-info%s --server "$ARGOCD_SERVER" --output json`,
			flags, flags,
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
