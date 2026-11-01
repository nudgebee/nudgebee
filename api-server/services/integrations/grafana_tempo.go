package integrations

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"
)

func init() {
	core.RegisterIntegration(GrafanaTempo{})
}

// IntegrationGrafanaTempo is the Grafana Tempo trace backend. Traces only: Tempo stores
// no logs, and its span metrics are deliberately not exposed as a metrics source (see
// docs/architecture-decisions.md, "Grafana Tempo is a trace source only").
const IntegrationGrafanaTempo = "grafana_tempo"

const (
	GrafanaTempoConfigURL      = "tempo_url"
	GrafanaTempoConfigAuthType = "tempo_auth_type"
	GrafanaTempoConfigUsername = "tempo_username"
	GrafanaTempoConfigPassword = "tempo_password"
	GrafanaTempoConfigToken    = "tempo_token"
	GrafanaTempoConfigTenantID = "tempo_tenant_id"
	GrafanaTempoConfigInsecure = "tempo_insecure_skip_verify"
)

// Tempo has no authentication of its own; "none" is the common case, with basic or
// bearer when it sits behind a gateway (Grafana Cloud, an auth proxy, an ingress).
const (
	GrafanaTempoAuthNone   = "none"
	GrafanaTempoAuthBasic  = "basic"
	GrafanaTempoAuthBearer = "bearer"
)

type GrafanaTempo struct{}

func (GrafanaTempo) Name() string { return IntegrationGrafanaTempo }

func (GrafanaTempo) Category() core.IntegrationCategory {
	return core.IntegrationCategoryObservabilityPlatform
}

func (GrafanaTempo) ConfigSchema() core.IntegrationSchema {
	basic := map[string]any{GrafanaTempoConfigAuthType: GrafanaTempoAuthBasic}
	bearer := map[string]any{GrafanaTempoConfigAuthType: GrafanaTempoAuthBearer}
	return core.IntegrationSchema{
		Type:        core.ToolSchemaTypeObject,
		Required:    []string{GrafanaTempoConfigURL, GrafanaTempoConfigAuthType},
		Testable:    true,
		Description: "Connects to Grafana Tempo over its HTTP query API to show distributed traces.",
		Properties: map[string]core.IntegrationSchemaProperty{
			core.IntegrationConfigName: {
				Type: core.ToolSchemaTypeString, Description: "Custom name for this Grafana Tempo integration.",
				Default: "", Priority: 100,
			},
			core.AccountId: {
				Type: core.ToolSchemaTypeArray, Description: "Associated account(s) for this integration.",
				Default: "", AutoGenerateFunc: "listAccounts", Priority: 95,
			},
			GrafanaTempoConfigURL: {
				Type: core.ToolSchemaTypeString,
				Description: "Base URL of the Tempo query API (e.g. http://tempo.monitoring:3200). Include any " +
					"path prefix a gateway serves it under (e.g. https://gw.example.com/tempo).",
				Priority: 88, IsTestable: true,
			},
			GrafanaTempoConfigAuthType: {
				Type:        core.ToolSchemaTypeString,
				Description: "Tempo has no built-in auth; use basic or bearer only when a gateway in front of it requires them.",
				Enum:        []any{GrafanaTempoAuthNone, GrafanaTempoAuthBasic, GrafanaTempoAuthBearer},
				Default:     GrafanaTempoAuthNone, Priority: 86, IsTestable: true,
			},
			GrafanaTempoConfigUsername: {
				Type: core.ToolSchemaTypeString, Description: "Username for Basic auth.",
				ShowWhen: basic, RequiredWhen: basic, Priority: 84, IsTestable: true,
			},
			GrafanaTempoConfigPassword: {
				Type: core.ToolSchemaTypeString, Description: "Password for Basic auth.", IsEncrypted: true,
				ShowWhen: basic, RequiredWhen: basic, Priority: 82, IsTestable: true,
			},
			GrafanaTempoConfigToken: {
				Type: core.ToolSchemaTypeString, Description: "Token sent as Authorization: Bearer.", IsEncrypted: true,
				ShowWhen: bearer, RequiredWhen: bearer, Priority: 80, IsTestable: true,
			},
			GrafanaTempoConfigTenantID: {
				Type:        core.ToolSchemaTypeString,
				Description: "Tenant for multi-tenant Tempo, sent as the X-Scope-OrgID header. Leave empty for single-tenant.",
				Default:     "", Priority: 70, IsTestable: true,
			},
			GrafanaTempoConfigInsecure: {
				Type: core.ToolSchemaTypeBoolean, Description: "Skip TLS certificate verification (self-signed certificates).",
				Default: false, Priority: 30, IsTestable: true,
			},
			core.DefaultTraceProvider: {
				Type: core.ToolSchemaTypeBoolean, Description: "Make Grafana Tempo the default Trace Provider",
				Default: false, Priority: 13,
			},
		},
	}
}

func grafanaTempoReadConfig(config []core.IntegrationConfigValue) map[string]core.IntegrationConfigValue {
	out := make(map[string]core.IntegrationConfigValue, len(config))
	for _, c := range config {
		out[c.Name] = c
	}
	return out
}

func grafanaTempoAuthType(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return GrafanaTempoAuthNone
	}
	return v
}

// normalizeGrafanaTempoURL keeps any path prefix (unlike Splunk, a gateway commonly
// serves Tempo under one) and only trims whitespace and trailing slashes.
func normalizeGrafanaTempoURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func (GrafanaTempo) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	values := grafanaTempoReadConfig(config)
	url := normalizeGrafanaTempoURL(values[GrafanaTempoConfigURL].Value)

	var errs []error
	switch {
	case url == "":
		errs = append(errs, fmt.Errorf("%s is required", GrafanaTempoConfigURL))
	case !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://"):
		errs = append(errs, fmt.Errorf("%s must start with http:// or https:// (got %q)", GrafanaTempoConfigURL, url))
	}

	switch auth := grafanaTempoAuthType(values[GrafanaTempoConfigAuthType].Value); auth {
	case GrafanaTempoAuthNone:
	case GrafanaTempoAuthBasic:
		if strings.TrimSpace(values[GrafanaTempoConfigUsername].Value) == "" {
			errs = append(errs, fmt.Errorf("%s is required when %s is %q", GrafanaTempoConfigUsername, GrafanaTempoConfigAuthType, auth))
		}
		if values[GrafanaTempoConfigPassword].Value == "" {
			errs = append(errs, fmt.Errorf("%s is required when %s is %q", GrafanaTempoConfigPassword, GrafanaTempoConfigAuthType, auth))
		}
	case GrafanaTempoAuthBearer:
		if strings.TrimSpace(values[GrafanaTempoConfigToken].Value) == "" {
			errs = append(errs, fmt.Errorf("%s is required when %s is %q", GrafanaTempoConfigToken, GrafanaTempoConfigAuthType, auth))
		}
	default:
		errs = append(errs, fmt.Errorf("%s must be %q, %q or %q (got %q)", GrafanaTempoConfigAuthType,
			GrafanaTempoAuthNone, GrafanaTempoAuthBasic, GrafanaTempoAuthBearer, auth))
	}
	return errs
}

// GrafanaTempoConfig is the resolved per-account Tempo connection.
type GrafanaTempoConfig struct {
	URL                string
	AuthType           string
	Username           string
	Password           string
	Token              string
	TenantID           string
	InsecureSkipVerify bool
}

// Headers returns the auth and tenant headers every Tempo request needs.
func (c GrafanaTempoConfig) Headers() map[string]string {
	h := map[string]string{"Accept": "application/json"}
	switch c.AuthType {
	case GrafanaTempoAuthBasic:
		h["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(c.Username+":"+c.Password))
	case GrafanaTempoAuthBearer:
		h["Authorization"] = "Bearer " + c.Token
	}
	if c.TenantID != "" {
		h["X-Scope-OrgID"] = c.TenantID
	}
	return h
}

func grafanaTempoSecret(v core.IntegrationConfigValue) (string, error) {
	if v.Value == "" || !v.IsEncrypted {
		return strings.TrimSpace(v.Value), nil
	}
	d, err := common.Decrypt(v.Value)
	return strings.TrimSpace(d), err
}

// grafanaTempoConfigFromValues is shared by TestConnection (unsaved form values) and
// GetGrafanaTempoConfig (stored row) so both normalise identically.
func grafanaTempoConfigFromValues(config []core.IntegrationConfigValue) (GrafanaTempoConfig, error) {
	values := grafanaTempoReadConfig(config)
	cfg := GrafanaTempoConfig{
		URL:                normalizeGrafanaTempoURL(values[GrafanaTempoConfigURL].Value),
		AuthType:           grafanaTempoAuthType(values[GrafanaTempoConfigAuthType].Value),
		Username:           strings.TrimSpace(values[GrafanaTempoConfigUsername].Value),
		TenantID:           strings.TrimSpace(values[GrafanaTempoConfigTenantID].Value),
		InsecureSkipVerify: strings.EqualFold(strings.TrimSpace(values[GrafanaTempoConfigInsecure].Value), "true"),
	}
	var err error
	if cfg.Password, err = grafanaTempoSecret(values[GrafanaTempoConfigPassword]); err != nil {
		return cfg, fmt.Errorf("failed to decrypt %s: %w", GrafanaTempoConfigPassword, err)
	}
	if cfg.Token, err = grafanaTempoSecret(values[GrafanaTempoConfigToken]); err != nil {
		return cfg, fmt.Errorf("failed to decrypt %s: %w", GrafanaTempoConfigToken, err)
	}
	return cfg, nil
}

func grafanaTempoGet(cfg GrafanaTempoConfig, path string, params map[string]string) (int, string, error) {
	opts := []common.HttpOption{
		common.HttpWithHeaders(cfg.Headers()),
		common.HttpWithQueryParams(params),
		common.HttpWithTimeout(15 * time.Second),
	}
	if cfg.InsecureSkipVerify {
		opts = append(opts, common.HttpWithInsecureSkipVerify())
	}
	resp, err := common.HttpGet(cfg.URL+path, opts...)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, strings.TrimSpace(string(body)), nil
}

// TestConnection implements core.TestableIntegration. /api/echo proves the URL is a
// Tempo query endpoint; the tags call proves the credentials and tenant are accepted by
// the query path itself (a gateway can let /api/echo through unauthenticated).
func (GrafanaTempo) TestConnection(sc *security.RequestContext, config []core.IntegrationConfigValue, accountId string) error {
	cfg, err := grafanaTempoConfigFromValues(config)
	if err != nil {
		return err
	}
	status, _, err := grafanaTempoGet(cfg, "/api/echo", nil)
	if err != nil {
		if strings.Contains(err.Error(), "x509") || strings.Contains(err.Error(), "certificate") {
			return fmt.Errorf("TLS verification failed for Tempo at %s - enable %s for a self-signed certificate: %w",
				cfg.URL, GrafanaTempoConfigInsecure, err)
		}
		return fmt.Errorf("failed to connect to Tempo at %s - the server may be down, or a tunnel/port-forward may have died: %w", cfg.URL, err)
	}
	switch status {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("tempo rejected the credentials (HTTP %d) - check %s and the matching secret", status, GrafanaTempoConfigAuthType)
	case http.StatusNotFound:
		return fmt.Errorf("%s/api/echo not found - %s is not a Tempo query endpoint (check the path prefix)", cfg.URL, GrafanaTempoConfigURL)
	default:
		return fmt.Errorf("tempo /api/echo returned unexpected status: HTTP %d", status)
	}

	now := time.Now()
	status, body, err := grafanaTempoGet(cfg, "/api/v2/search/tags", map[string]string{
		"start": strconv.FormatInt(now.Add(-time.Hour).Unix(), 10),
		"end":   strconv.FormatInt(now.Unix(), 10),
	})
	if err != nil {
		return fmt.Errorf("tempo tag search failed: %w", err)
	}
	switch {
	case status == http.StatusOK:
		return nil
	case strings.Contains(strings.ToLower(body), "org id"):
		return fmt.Errorf("tempo is multi-tenant and needs a tenant - set %s (sent as X-Scope-OrgID)", GrafanaTempoConfigTenantID)
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("tempo rejected the credentials on the query API (HTTP %d) - check %s and the matching secret", status, GrafanaTempoConfigAuthType)
	case status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout:
		return fmt.Errorf("tempo tag search returned HTTP %d - Tempo or a gateway in front of it is unavailable", status)
	default:
		return fmt.Errorf("tempo tag search returned HTTP %d: %s", status, body)
	}
}

// GetGrafanaTempoConfig retrieves and decrypts the Tempo configuration for an account.
// An empty accountId is refused: ListIntegrationConfigs only filters by account when one
// is given, so it would otherwise return another account's Tempo.
func GetGrafanaTempoConfig(sc *security.RequestContext, accountId string) (GrafanaTempoConfig, error) {
	if accountId == "" {
		return GrafanaTempoConfig{}, fmt.Errorf("account_id is required to resolve a Grafana Tempo integration")
	}
	configs, err := core.ListIntegrationConfigs(sc, accountId, IntegrationGrafanaTempo)
	if err != nil {
		return GrafanaTempoConfig{}, fmt.Errorf("failed to list Grafana Tempo integration configs: %w", err)
	}
	if len(configs) == 0 {
		return GrafanaTempoConfig{}, fmt.Errorf("grafana tempo integration not found for account: %s", accountId)
	}
	return grafanaTempoConfigFromValues(configs[0].Configs)
}
