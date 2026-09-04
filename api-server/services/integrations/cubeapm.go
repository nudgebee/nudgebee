package integrations

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
	"time"
)

func init() {
	core.RegisterIntegration(CubeAPM{})
}

const IntegrationCubeAPM = "cubeapm"

// CubeAPM splits its HTTP surface across two ports on the same host: the query
// APIs (logs, metrics, traces) answer on 3140, while alert-rule management lives
// behind the admin server on 3199. Users configure the query URL; the admin URL
// is derived from it unless they override it, because a same-host deployment is
// overwhelmingly the common case and asking for both URLs up front is friction
// for the majority to serve the minority.
const (
	CubeAPMDefaultQueryPort = "3140"
	CubeAPMDefaultAdminPort = "3199"
)

type CubeAPM struct{}

func (m CubeAPM) Name() string {
	return IntegrationCubeAPM
}

func (m CubeAPM) Category() core.IntegrationCategory {
	return core.IntegrationCategoryObservabilityPlatform
}

func (m CubeAPM) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{"cubeapm_url"},
		Testable: true,
		Description: "CubeAPM serves queries on port 3140 and alert-rule management on the admin " +
			"port 3199. Point the URL at the query port — the admin URL is derived from it unless " +
			"you override it below.",
		Properties: map[string]core.IntegrationSchemaProperty{
			"cubeapm_url": {
				Type: core.ToolSchemaTypeString,
				Description: "Base URL of the CubeAPM query API, including its port " +
					"(e.g. http://cubeapm.observability.svc:3140).",
				Priority:   85,
				IsTestable: true,
			},
			"cubeapm_token": {
				Type: core.ToolSchemaTypeString,
				Description: "Bearer token for the query API. Leave empty when CubeAPM's query " +
					"port is unauthenticated (the default for in-cluster deployments).",
				IsEncrypted: true,
				Priority:    80,
				IsTestable:  true,
			},
			"cubeapm_admin_url": {
				Type: core.ToolSchemaTypeString,
				Description: "Base URL of the CubeAPM admin API, used to create and manage alert " +
					"rules (e.g. http://cubeapm.observability.svc:3199). Leave empty to derive it " +
					"from the query URL by swapping port " + CubeAPMDefaultQueryPort + " for " +
					CubeAPMDefaultAdminPort + ".",
				Priority: 75,
			},
			"cubeapm_admin_token": {
				Type: core.ToolSchemaTypeString,
				Description: "Bearer token for the admin API, matching CubeAPM's http-token-admin " +
					"setting. Required only when that setting is configured.",
				IsEncrypted: true,
				Priority:    70,
			},
			"cubeapm_env": {
				Type: core.ToolSchemaTypeString,
				Description: "Restrict log and trace queries to one CubeAPM environment tag (env). " +
					"Leave empty to search all environments; a single-environment install reports " +
					"everything under 'UNSET'. Metrics are not scoped by this — filter them with an " +
					"env label chip instead, since injecting the matcher would blank out any metric " +
					"family that does not carry the label.",
				Default:  "",
				Priority: 65,
			},
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Custom name for this CubeAPM integration.",
				Default:     "",
				Priority:    100,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Associated account(s) for this integration.",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			core.DefaultLogProvider: {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Make CubeAPM default Log Provider",
				Default:     false,
				Priority:    15,
			},
			core.DefaultTraceProvider: {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Make CubeAPM default Trace Provider",
				Default:     false,
				Priority:    14,
			},
			core.DefaultMetricsProvider: {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Make CubeAPM default Metrics Provider",
				Default:     false,
				Priority:    13,
			},
		},
	}
}

func (m CubeAPM) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	var rawURL, rawAdminURL string
	for _, c := range config {
		switch c.Name {
		case "cubeapm_url":
			rawURL = c.Value
		case "cubeapm_admin_url":
			rawAdminURL = c.Value
		}
	}

	var errs []error
	if err := validateCubeAPMBaseURL("cubeapm_url", rawURL, true); err != nil {
		errs = append(errs, err)
	}
	if err := validateCubeAPMBaseURL("cubeapm_admin_url", rawAdminURL, false); err != nil {
		errs = append(errs, err)
	}
	return errs
}

// validateCubeAPMBaseURL rejects the URL shapes that produce a silently broken
// integration: a missing scheme, and a pasted browser URL that carries a path.
// The path check matters more here than it looks — every CubeAPM endpoint is
// mounted under its own /api/<signal> prefix, so a stored base of
// ".../logs/explorer" builds requests that 404 rather than failing loudly.
func validateCubeAPMBaseURL(field, raw string, required bool) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if required {
			return fmt.Errorf("%s is required", field)
		}
		return nil
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return fmt.Errorf("%s must start with http:// or https:// (got %q)", field, raw)
	}
	parsed, err := neturl.Parse(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%s is not a valid URL: %q", field, raw)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return fmt.Errorf("%s must be the base URL only — remove the path after the host (use %q, not %q)",
			field, normalizeCubeAPMURL(raw), raw)
	}
	return nil
}

// TestConnection implements core.TestableIntegration. It runs an instant PromQL
// query for the scalar `1`: the metrics query API is documented and always
// present, and a scalar needs no metric to exist, so the probe reports on
// reachability and auth rather than on whether the install has data yet.
func (m CubeAPM) TestConnection(sc *security.RequestContext, config []core.IntegrationConfigValue, accountId string) error {
	var url, token string
	for _, c := range config {
		switch c.Name {
		case "cubeapm_url":
			url = c.Value
		case "cubeapm_token":
			token = c.Value
		}
	}

	url = normalizeCubeAPMURL(url)
	if url == "" {
		return fmt.Errorf("cubeapm_url is required")
	}

	form := neturl.Values{}
	form.Set("query", "1")

	resp, err := common.HttpPost(
		url+"/api/metrics/api/v1/query",
		common.HttpWithHeaders(CubeAPMRequestHeaders(token, "application/x-www-form-urlencoded")),
		common.HttpWithBody(io.NopCloser(bytes.NewReader([]byte(form.Encode())))),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "connection refused") {
			return fmt.Errorf("failed to connect to CubeAPM at %s — the server may be down, or the "+
				"port may not be the query port (%s): %w", url, CubeAPMDefaultQueryPort, err)
		}
		return fmt.Errorf("failed to connect to CubeAPM at %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return fmt.Errorf("CubeAPM rejected the credentials (HTTP 401) — check cubeapm_token")
	case http.StatusForbidden:
		return fmt.Errorf("insufficient permissions for CubeAPM (HTTP 403)")
	case http.StatusNotFound:
		return fmt.Errorf("CubeAPM /api/metrics/api/v1/query not found at %s — check that cubeapm_url "+
			"points at the query port (%s), not the UI port", url, CubeAPMDefaultQueryPort)
	default:
		return fmt.Errorf("CubeAPM API returned unexpected status: HTTP %d", resp.StatusCode)
	}
}

// normalizeCubeAPMURL trims whitespace and strips any path/query/fragment so a URL
// copied out of the browser still resolves. The port is deliberately preserved —
// unlike most integrations here, CubeAPM's port is load-bearing.
func normalizeCubeAPMURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := neturl.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(raw, "/")
	}
	return parsed.Scheme + "://" + parsed.Host
}

// CubeAPMRequestHeaders builds the header set for a CubeAPM call. The Authorization
// header is omitted entirely when no token is configured: CubeAPM's query port
// is unauthenticated by default, and sending `Bearer ` with an empty value is a
// malformed credential rather than an absent one.
func CubeAPMRequestHeaders(token, contentType string) map[string]string {
	headers := map[string]string{}
	if contentType != "" {
		headers["Content-Type"] = contentType
	}
	if strings.TrimSpace(token) != "" {
		headers["Authorization"] = "Bearer " + strings.TrimSpace(token)
	}
	return headers
}

// deriveCubeAPMAdminURL turns a query URL into the admin URL by swapping the port.
// Returns "" when the query URL carries a port other than the standard query port,
// because guessing at that point would silently target the wrong service — the
// caller then reports that cubeapm_admin_url must be set explicitly.
func deriveCubeAPMAdminURL(queryURL string) string {
	parsed, err := neturl.Parse(queryURL)
	if err != nil || parsed.Host == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil || port != CubeAPMDefaultQueryPort {
		return ""
	}
	return parsed.Scheme + "://" + net.JoinHostPort(host, CubeAPMDefaultAdminPort)
}

// CubeAPMConfig is the resolved per-account CubeAPM connection.
type CubeAPMConfig struct {
	// URL is the query API base (port 3140), without trailing slash.
	URL string
	// Token authenticates against URL. Empty when the query port is open.
	Token string
	// AdminURL is the admin API base (port 3199) used for alert-rule management.
	// Empty when it was neither configured nor derivable.
	AdminURL string
	// AdminToken authenticates against AdminURL.
	AdminToken string
	// Env scopes log and trace queries to one CubeAPM environment tag. Empty means
	// all environments — those queries omit the parameter rather than sending a
	// blank. Metrics deliberately ignore it (see the schema description).
	Env string
}

// GetCubeAPMConfigs retrieves and decrypts CubeAPM configuration for an account.
func GetCubeAPMConfigs(sc *security.RequestContext, accountId string) (CubeAPMConfig, error) {
	var cfg CubeAPMConfig

	// ListIntegrationConfigs always scopes by tenant (and rejects an empty tenant
	// outright), so this is not a cross-tenant guard. It guards the ACCOUNT: that
	// query applies the cloud_account_id filter only when accountId is non-empty,
	// so an empty one silently returns whichever CubeAPM integration happens to
	// come first in the tenant — connecting to another account's CubeAPM with
	// another account's token, and reporting success.
	if accountId == "" {
		return cfg, fmt.Errorf("account_id is required to resolve a CubeAPM integration")
	}

	cubeIntegrations, err := core.ListIntegrationConfigs(sc, accountId, IntegrationCubeAPM)
	if err != nil {
		return cfg, fmt.Errorf("failed to list CubeAPM integration configs: %w", err)
	}
	if len(cubeIntegrations) == 0 {
		return cfg, fmt.Errorf("cubeapm integration not found for account: %s", accountId)
	}

	for _, config := range cubeIntegrations[0].Configs {
		switch config.Name {
		case "cubeapm_url":
			cfg.URL = config.Value
		case "cubeapm_admin_url":
			cfg.AdminURL = config.Value
		case "cubeapm_env":
			cfg.Env = strings.TrimSpace(config.Value)
		case "cubeapm_token":
			cfg.Token, err = decryptCubeAPMValue(config)
			if err != nil {
				return cfg, fmt.Errorf("failed to decrypt CubeAPM token: %w", err)
			}
		case "cubeapm_admin_token":
			cfg.AdminToken, err = decryptCubeAPMValue(config)
			if err != nil {
				return cfg, fmt.Errorf("failed to decrypt CubeAPM admin token: %w", err)
			}
		}
	}

	cfg.URL = normalizeCubeAPMURL(cfg.URL)
	if cfg.URL == "" {
		return cfg, fmt.Errorf("cubeapm integration for account %s has no cubeapm_url", accountId)
	}

	cfg.AdminURL = normalizeCubeAPMURL(cfg.AdminURL)
	if cfg.AdminURL == "" {
		cfg.AdminURL = deriveCubeAPMAdminURL(cfg.URL)
	}

	return cfg, nil
}

func decryptCubeAPMValue(config core.IntegrationConfigValue) (string, error) {
	if !config.IsEncrypted || config.Value == "" {
		return config.Value, nil
	}
	return common.Decrypt(config.Value)
}
