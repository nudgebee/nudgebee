package integrations

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
	"time"
)

// Loki is the user-configured (direct HTTP) Loki integration: Nudgebee connects to the
// customer's Loki endpoint itself, with no agent in the path.
//
// It registers under a source-qualified key because LokiAgent already claims the plain
// "loki" name for the agent-managed variant. Note that a bare core.GetIntegration("loki")
// therefore still resolves the agent descriptor — user-mode callers (schema fetch, create,
// connection test, edit) all go through GetIntegrationBySource.
func init() {
	core.RegisterIntegrationWithSource(IntegrationLoki, "user", Loki{})
}

const IntegrationLoki = "loki"

// Authentication methods offered by the integration form. The chosen one decides which
// credential fields are shown and required; anything not selected is ignored, so a
// half-filled method left over from a previous choice cannot silently take effect.
const (
	LokiAuthNone   = "none"
	LokiAuthBasic  = "basic"
	LokiAuthBearer = "bearer_token"
)

type Loki struct{}

func (m Loki) Name() string {
	return IntegrationLoki
}

func (m Loki) Category() core.IntegrationCategory {
	return core.IntegrationCategoryLog
}

func (m Loki) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{"loki_url"},
		Testable: true,
		Properties: map[string]core.IntegrationSchemaProperty{
			"loki_url": {
				Type: core.ToolSchemaTypeString,
				Description: "Base URL of the Loki query endpoint (e.g. http://loki-gateway.loki.svc.cluster.local " +
					"or https://logs-prod-012.grafana.net). Base URL only — /loki/api/v1/... is appended.",
				Priority:   90,
				IsTestable: true,
			},
			"loki_auth_type": {
				Type:        core.ToolSchemaTypeString,
				Description: "How Nudgebee authenticates to Loki.",
				Default:     LokiAuthNone,
				Enum:        []any{LokiAuthNone, LokiAuthBasic, LokiAuthBearer},
				Priority:    85,
				IsTestable:  true,
			},
			"loki_username": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Basic-auth username. On Grafana Cloud this is the numeric instance ID.",
				ShowWhen:     map[string]any{"loki_auth_type": LokiAuthBasic},
				RequiredWhen: map[string]any{"loki_auth_type": LokiAuthBasic},
				Priority:     80,
				IsTestable:   true,
			},
			"loki_password": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Basic-auth password, or the Grafana Cloud API token.",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"loki_auth_type": LokiAuthBasic},
				RequiredWhen: map[string]any{"loki_auth_type": LokiAuthBasic},
				Priority:     79,
				IsTestable:   true,
			},
			"loki_bearer_token": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Token sent as the Authorization: Bearer header.",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"loki_auth_type": LokiAuthBearer},
				RequiredWhen: map[string]any{"loki_auth_type": LokiAuthBearer},
				Priority:     78,
				IsTestable:   true,
			},
			"loki_tenant_id": {
				Type: core.ToolSchemaTypeString,
				Description: "Tenant ID for multi-tenant Loki, sent as the X-Scope-OrgID header. " +
					"Leave empty when Loki runs with auth_enabled: false.",
				Priority:   75,
				IsTestable: true,
			},
			"loki_headers": {
				Type: core.ToolSchemaTypeString,
				Description: "Any additional HTTP headers, as a JSON object — for a proxy or gateway that " +
					`needs them, e.g. {"X-Team": "platform"}. Authentication is set above.`,
				IsEncrypted: true,
				Priority:    70,
				IsTestable:  true,
			},
			"loki_rules_url": {
				Type: core.ToolSchemaTypeString,
				Description: "Ruler base URL, only when the Loki ruler is a separate service from the query " +
					"endpoint. Defaults to the query URL. Used for alert rules.",
				Priority:   60,
				IsTestable: true,
			},
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Custom name for this Loki integration.",
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
				Description: "Make Loki default Log Provider",
				Default:     false,
				Priority:    15,
			},
		},
	}
}

// lokiFormConfig is the subset of the form this integration validates and probes.
type lokiFormConfig struct {
	URL         string
	RulesURL    string
	AuthType    string
	Username    string
	Password    string
	BearerToken string
	TenantID    string
	Headers     string
}

func readLokiFormConfig(config []core.IntegrationConfigValue) lokiFormConfig {
	var cfg lokiFormConfig
	for _, c := range config {
		switch c.Name {
		case "loki_url":
			cfg.URL = c.Value
		case "loki_rules_url":
			cfg.RulesURL = c.Value
		case "loki_auth_type":
			cfg.AuthType = c.Value
		case "loki_username":
			cfg.Username = c.Value
		case "loki_password":
			cfg.Password = c.Value
		case "loki_bearer_token":
			cfg.BearerToken = c.Value
		case "loki_tenant_id":
			cfg.TenantID = c.Value
		case "loki_headers":
			cfg.Headers = c.Value
		}
	}
	cfg.URL = normalizeLokiURL(cfg.URL)
	cfg.RulesURL = normalizeLokiURL(cfg.RulesURL)
	cfg.AuthType = strings.TrimSpace(cfg.AuthType)
	cfg.Username = strings.TrimSpace(cfg.Username)
	cfg.TenantID = strings.TrimSpace(cfg.TenantID)
	// An integration saved before the auth selector existed has no loki_auth_type, but
	// its credentials still have to keep working: infer the method from what is set.
	if cfg.AuthType == "" {
		if cfg.Username != "" || cfg.Password != "" {
			cfg.AuthType = LokiAuthBasic
		} else {
			cfg.AuthType = LokiAuthNone
		}
	}
	return cfg
}

// ValidateConfig checks the shape of the submitted form. The live probe is in
// TestConnection so a save is not silently coupled to endpoint reachability.
func (m Loki) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	cfg := readLokiFormConfig(config)

	var errs []error
	if cfg.URL == "" {
		errs = append(errs, fmt.Errorf("loki_url is required"))
	} else {
		if err := validateEgressURL(cfg.URL); err != nil {
			errs = append(errs, fmt.Errorf("loki_url %w", err))
		}
		if hasURLPath(rawLokiValue(config, "loki_url")) {
			errs = append(errs, fmt.Errorf("loki_url must be the base URL only — remove the path after the host (use %q)", cfg.URL))
		}
	}

	if cfg.RulesURL != "" {
		if err := validateEgressURL(cfg.RulesURL); err != nil {
			errs = append(errs, fmt.Errorf("loki_rules_url %w", err))
		}
		if hasURLPath(rawLokiValue(config, "loki_rules_url")) {
			errs = append(errs, fmt.Errorf("loki_rules_url must be the base URL only — remove the path after the host (use %q)", cfg.RulesURL))
		}
	}

	switch cfg.AuthType {
	case LokiAuthNone:
	case LokiAuthBasic:
		// A lone username is not a credential: Loki would reject the request, or worse,
		// accept it anonymously and look like it worked.
		if cfg.Username == "" || cfg.Password == "" {
			errs = append(errs, fmt.Errorf("loki_username and loki_password are both required for basic authentication"))
		}
	case LokiAuthBearer:
		if cfg.BearerToken == "" {
			errs = append(errs, fmt.Errorf("loki_bearer_token is required for bearer-token authentication"))
		}
	default:
		errs = append(errs, fmt.Errorf("loki_auth_type must be one of %q, %q or %q (got %q)",
			LokiAuthNone, LokiAuthBasic, LokiAuthBearer, cfg.AuthType))
	}

	headers, err := core.ParseExtraHeaders(cfg.Headers)
	if err != nil {
		errs = append(errs, fmt.Errorf("loki_headers %w", err))
	} else if cfg.AuthType != LokiAuthNone {
		// The selected method owns the Authorization header; a second one here would
		// silently win or lose depending on header ordering.
		for name := range headers {
			if strings.EqualFold(name, "Authorization") {
				errs = append(errs, fmt.Errorf("loki_headers sets Authorization while an authentication method is also selected — use one or the other"))
				break
			}
		}
	}

	return errs
}

// TestConnection implements core.TestableIntegration.
func (m Loki) TestConnection(sc *security.RequestContext, config []core.IntegrationConfigValue, accountId string) error {
	cfg := readLokiFormConfig(config)
	headers, err := core.ParseExtraHeaders(cfg.Headers)
	if err != nil {
		return fmt.Errorf("loki_headers %w", err)
	}

	// buildinfo, not /ready: the loki-gateway nginx in front of a distributed Loki only
	// proxies /loki/... paths and answers 404 for everything else, so /ready would fail
	// against a perfectly healthy deployment.
	if err := lokiProbe(cfg.URL+"/loki/api/v1/status/buildinfo", cfg, headers, "query endpoint"); err != nil {
		return err
	}

	// Only meaningful when the ruler is a separate service; the query-endpoint probe
	// already covers the single-binary case.
	if cfg.RulesURL != "" {
		if err := lokiProbe(cfg.RulesURL+"/loki/api/v1/rules", cfg, headers, "ruler"); err != nil {
			return err
		}
	}
	return nil
}

// lokiProbe performs one authenticated GET and maps the outcome to an operator-facing error.
func lokiProbe(url string, cfg lokiFormConfig, headers map[string]string, target string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to build Loki %s request: %w", target, err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// The probe must authenticate exactly the way queries will, or a passing test
	// connection would tell the operator nothing about whether logs can be read.
	switch cfg.AuthType {
	case LokiAuthBasic:
		req.SetBasicAuth(cfg.Username, cfg.Password)
	case LokiAuthBearer:
		req.Header.Set("Authorization", "Bearer "+cfg.BearerToken)
	}
	if cfg.TenantID != "" {
		req.Header.Set("X-Scope-OrgID", cfg.TenantID)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "connection refused") {
			return fmt.Errorf("failed to connect to the Loki %s at %s — the server may be down, or a tunnel/port-forward may have died: %w", target, url, err)
		}
		return fmt.Errorf("failed to connect to the Loki %s at %s: %w", target, url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode <= 299:
		return nil
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("invalid Loki credentials (HTTP 401) — check loki_username/loki_password, or the Authorization header in loki_headers")
	case resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("insufficient permissions for Loki (HTTP 403) — check loki_tenant_id")
	case resp.StatusCode == http.StatusNotFound && target == "ruler":
		// A ruler with no rule groups yet answers 404 "no rule groups found" (observed
		// on Loki 3.4.1), which is the normal state before the first alert rule is
		// created. Failing the connection test for it would block the very setup step
		// the operator is trying to complete, so a reachable ruler is good enough here.
		return nil
	case resp.StatusCode == http.StatusMethodNotAllowed && target == "ruler":
		return fmt.Errorf("Loki ruler at %s is read-only (HTTP 405) — it needs a writable rule store; alert rules cannot be created against local rule storage", url)
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("Loki API not found at %s (HTTP 404) — check that loki_url points at the Loki query endpoint or gateway", url)
	default:
		return fmt.Errorf("Loki %s returned unexpected status: HTTP %d", target, resp.StatusCode)
	}
}

// rawLokiValue returns the untrimmed submitted value, for path checks that must see
// exactly what the user pasted.
func rawLokiValue(config []core.IntegrationConfigValue, name string) string {
	for _, c := range config {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// normalizeLokiURL trims whitespace and strips any path/query/fragment so users can
// paste a URL straight from the browser.
func normalizeLokiURL(raw string) string {
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
