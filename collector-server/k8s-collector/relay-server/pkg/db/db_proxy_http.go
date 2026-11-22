package db

import (
	"encoding/json"
	"log/slog"
	"strings"
)

// The normalizers below rewrite a prometheus, loki or signoz integration's config into the
// shape the forager's http proxy reads. They are ports of the API server's
// (api-server/services/integrations/core/proxy_config_push.go) and must stay in step with
// them: both build the datasource list a forager is given.

// defaultLogProviderKey is the account-level flag stored beside a Loki or SigNoz config;
// it is not part of the datasource.
const defaultLogProviderKey = "default_log_provider"

// httpProxyAuthTypes maps the integration's auth_type values to the forager http proxy's.
// Everything else (SigV4, Coralogix, Azure AD) needs the server to sign requests and is
// rejected by the integration's validation.
var httpProxyAuthTypes = map[string]string{
	"none":         "",
	"basic":        "basic",
	"bearer_token": "bearer",
}

type prometheusIntegrationConfig struct {
	URL         string `json:"prometheus_url"`
	Username    string `json:"prometheus_username"`
	Password    string `json:"prometheus_password"`
	BearerToken string `json:"prometheus_bearer_token"`
	AuthType    string `json:"auth_type"`
}

type prometheusProxyDatasource struct {
	BaseURL  string `json:"base_url,omitempty"`
	AuthType string `json:"auth_type,omitempty"`
}

type prometheusProxyCredentials struct {
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	BearerToken string `json:"bearer_token,omitempty"`
}

// prometheusServerOnlyKeys are server-side or unsupported through the forager, so they are
// removed from the config map and never sent.
var prometheusServerOnlyKeys = []string{
	"prometheus_extra_headers", "prometheus_additional_labels",
	"prometheus_ruler_type", "prometheus_ruler_url", "default_metrics_provider",
	"aws_access_key_id", "aws_secret_access_key", "aws_region", "aws_service_name",
	"coralogix_token", "azure_client_id", "azure_client_secret", "azure_tenant_id", "azure_resource",
}

// normalizePrometheusProxyDatasource rewrites a prometheus integration's config into the
// shape the forager's http proxy reads: prometheus_url -> base_url,
// prometheus_username/password/bearer_token -> credentials, auth_type translated. The
// integration's own keys are removed so secrets never travel in the plain config map and
// server-only settings are not sent at all. Keys it does not know about are left in place.
func normalizePrometheusProxyDatasource(configMap map[string]any, credentials map[string]string) {
	var in prometheusIntegrationConfig
	if raw, err := json.Marshal(configMap); err == nil {
		// A mistyped value is skipped and the rest still decode; the keys are deleted
		// below either way so nothing secret stays in the plain map.
		if err := json.Unmarshal(raw, &in); err != nil {
			slog.Warn("prometheus proxy config has a non-string value", "error", err)
		}
	}

	for _, key := range []string{"prometheus_url", "prometheus_username", "prometheus_password", "prometheus_bearer_token", "auth_type"} {
		delete(configMap, key)
	}
	for _, key := range prometheusServerOnlyKeys {
		delete(configMap, key)
	}

	mergeJSON(&configMap, prometheusProxyDatasource{
		BaseURL:  strings.TrimRight(strings.TrimSpace(in.URL), "/"),
		AuthType: httpProxyAuthTypes[in.AuthType],
	})
	mergeJSON(&credentials, prometheusProxyCredentials{
		Username:    in.Username,
		Password:    in.Password,
		BearerToken: in.BearerToken,
	})
}

// mergeJSON marshals v and decodes it into *dst (a pointer to a non-nil map), adding or
// overwriting only the keys v emits.
func mergeJSON(dst, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		slog.Error("proxy config marshal failed", "error", err)
		return
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		slog.Error("proxy config merge failed", "error", err)
	}
}

// normalizeLokiProxyDatasource is the Loki counterpart. The tenant id is dropped: the
// forager applies a custom header only for auth_type=custom_header, so the server sends
// X-Scope-OrgID per request. loki_headers and the ruler URL stay server-side.
func normalizeLokiProxyDatasource(configMap map[string]any, credentials map[string]string) {
	take := func(key string) string {
		v, _ := configMap[key].(string)
		delete(configMap, key)
		return v
	}

	if u := strings.TrimRight(strings.TrimSpace(take("loki_url")), "/"); u != "" {
		configMap["base_url"] = u
	}
	if v := take("loki_username"); v != "" {
		credentials["username"] = v
	}
	if v := take("loki_password"); v != "" {
		credentials["password"] = v
	}
	if v := take("loki_bearer_token"); v != "" {
		credentials["bearer_token"] = v
	}
	take("loki_tenant_id")

	authType := take("loki_auth_type")
	if mapped := httpProxyAuthTypes[authType]; mapped != "" {
		configMap["auth_type"] = mapped
	}

	for _, key := range []string{"loki_headers", "loki_rules_url", defaultLogProviderKey} {
		delete(configMap, key)
	}
}

// normalizeSignozProxyDatasource maps signoz_url to base_url and drops everything else. The
// forager's datasource is unauthenticated on purpose: SigNoz authenticates with a login JWT
// or an API key, which the forager's static auth cannot do, so those stay on the server and
// are sent per request.
func normalizeSignozProxyDatasource(configMap map[string]any) {
	if u, _ := configMap["signoz_url"].(string); strings.TrimSpace(u) != "" {
		configMap["base_url"] = strings.TrimRight(strings.TrimSpace(u), "/")
	}
	for _, key := range []string{"signoz_url", "signoz_username", "signoz_password", "signoz_api_key", "signoz_auth_type", "signoz_mode", defaultLogProviderKey} {
		delete(configMap, key)
	}
}
