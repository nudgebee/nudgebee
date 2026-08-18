package integrations

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
	"time"
)

func init() {
	core.RegisterIntegration(OpenObserve{})
}

const IntegrationOpenObserve = "openobserve"

// OpenObserveDefaultLogStream is the stream OpenObserve ingests logs into unless the
// collector is configured otherwise.
const OpenObserveDefaultLogStream = "default"

// OpenObserveDefaultTraceStream is the equivalent default for spans. Kept separate from
// the log stream: OpenObserve stores each signal in its own stream, so a deployment that
// renames one has no reason to have renamed the other.
const OpenObserveDefaultTraceStream = "default"

type OpenObserve struct{}

func (m OpenObserve) Name() string {
	return IntegrationOpenObserve
}

func (m OpenObserve) Category() core.IntegrationCategory {
	return core.IntegrationCategoryObservabilityPlatform
}

func (m OpenObserve) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{"openobserve_url", "openobserve_org_id", "openobserve_username", "openobserve_password"},
		Testable: true,
		Properties: map[string]core.IntegrationSchemaProperty{
			"openobserve_url": {
				Type:        core.ToolSchemaTypeString,
				Description: "Base URL of the OpenObserve instance (e.g., https://cloud.openobserve.ai or http://localhost:5080).",
				Priority:    85,
				IsTestable:  true,
			},
			"openobserve_org_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "OpenObserve Organization ID. Use 'default' for single-org deployments.",
				Default:     "default",
				Priority:    80,
				IsTestable:  true,
			},
			"openobserve_username": {
				Type:        core.ToolSchemaTypeString,
				Description: "Email or username for OpenObserve Basic Auth.",
				Priority:    75,
				IsTestable:  true,
			},
			"openobserve_password": {
				Type:        core.ToolSchemaTypeString,
				Description: "Password or API token for OpenObserve Basic Auth.",
				IsEncrypted: true,
				Priority:    70,
				IsTestable:  true,
			},
			"openobserve_log_stream": {
				Type: core.ToolSchemaTypeString,
				Description: "Name of the OpenObserve stream holding logs. Leave as 'default' unless your " +
					"collector writes to a differently-named stream (e.g. 'k8s_logs').",
				Default:  OpenObserveDefaultLogStream,
				Priority: 65,
			},
			"openobserve_trace_stream": {
				Type: core.ToolSchemaTypeString,
				Description: "Name of the OpenObserve stream holding traces. Leave as 'default' unless your " +
					"collector writes spans to a differently-named stream.",
				Default:  OpenObserveDefaultTraceStream,
				Priority: 64,
			},
			core.IntegrationConfigName: {
				Type:             core.ToolSchemaTypeString,
				Description:      "Custom name for this OpenObserve integration.",
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         100,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Associated account(s) for this integration.",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			core.DefaultLogProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make OpenObserve default Log Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         15,
			},
			core.DefaultTraceProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make OpenObserve default Trace Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         14,
			},
			core.DefaultMetricsProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make OpenObserve default Metrics Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         13,
			},
		},
	}
}

func (m OpenObserve) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	var openobserveURL, orgID, username, password string
	for _, c := range config {
		switch c.Name {
		case "openobserve_url":
			openobserveURL = c.Value
		case "openobserve_org_id":
			orgID = c.Value
		case "openobserve_username":
			username = c.Value
		case "openobserve_password":
			password = c.Value
		}
	}

	rawURL := strings.TrimSpace(openobserveURL)
	openobserveURL = normalizeOpenObserveURL(rawURL)
	orgID = strings.TrimSpace(orgID)
	username = strings.TrimSpace(username)

	var errs []error
	if openobserveURL == "" {
		errs = append(errs, fmt.Errorf("openobserve_url is required"))
	} else if !strings.HasPrefix(openobserveURL, "http://") && !strings.HasPrefix(openobserveURL, "https://") {
		errs = append(errs, fmt.Errorf("openobserve_url must start with http:// or https:// (got %q)", openobserveURL))
	} else if parsed, err := neturl.Parse(rawURL); err == nil && parsed.Path != "" && parsed.Path != "/" {
		errs = append(errs, fmt.Errorf("openobserve_url must be the base URL only — remove the path after the host (use %q, not %q)", openobserveURL, rawURL))
	}
	if orgID == "" {
		errs = append(errs, fmt.Errorf("openobserve_org_id is required"))
	}
	if username == "" {
		errs = append(errs, fmt.Errorf("openobserve_username is required"))
	}
	if password == "" {
		errs = append(errs, fmt.Errorf("openobserve_password is required"))
	}
	return errs
}

// TestConnection implements core.TestableIntegration to perform live connectivity checks.
func (m OpenObserve) TestConnection(sc *security.RequestContext, config []core.IntegrationConfigValue, accountId string) error {
	var openobserveURL, orgID, username, password string
	for _, c := range config {
		switch c.Name {
		case "openobserve_url":
			openobserveURL = c.Value
		case "openobserve_org_id":
			orgID = c.Value
		case "openobserve_username":
			username = c.Value
		case "openobserve_password":
			password = c.Value
		}
	}

	openobserveURL = normalizeOpenObserveURL(strings.TrimSpace(openobserveURL))
	orgID = strings.TrimSpace(orgID)
	username = strings.TrimSpace(username)

	resp, err := common.HttpGet(
		fmt.Sprintf("%s/api/%s/streams", openobserveURL, neturl.PathEscape(orgID)),
		common.HttpWithHeaders(map[string]string{
			"Authorization": fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(username+":"+password))),
			"Content-Type":  "application/json",
		}),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "connection refused") {
			return fmt.Errorf("failed to connect to OpenObserve at %s the server may be down, or a tunnel/port-forward may have died: %w", openobserveURL, err)
		}
		return fmt.Errorf("failed to connect to OpenObserve at %s: %w", openobserveURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return fmt.Errorf("invalid OpenObserve credentials (HTTP 401) — check username and password")
	case http.StatusForbidden:
		return fmt.Errorf("insufficient permissions for OpenObserve (HTTP 403)")
	case http.StatusNotFound:
		return fmt.Errorf("OpenObserve /api/%s/streams not found at %s — check openobserve_url and openobserve_org_id", orgID, openobserveURL)
	default:
		return fmt.Errorf("OpenObserve API returned unexpected status: HTTP %d", resp.StatusCode)
	}
}

// normalizeOpenObserveURL trims whitespace and strips any path/query/fragment
// so users can paste a full URL from the browser.
func normalizeOpenObserveURL(raw string) string {
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

// OpenObserveConfig is the resolved per-account OpenObserve connection.
type OpenObserveConfig struct {
	URL      string
	OrgID    string
	Username string
	Password string
	// LogStream is the stream logs are queried from. Always populated
	// it falls back to OpenObserveDefaultLogStream when unconfigured.
	LogStream string
	// TraceStream is the stream spans are queried from. Always populated
	// it falls back to OpenObserveDefaultTraceStream when unconfigured.
	TraceStream string
}

// GetOpenObserveConfigs retrieves and decrypts OpenObserve configuration for an account.
func GetOpenObserveConfigs(sc *security.RequestContext, accountId string) (OpenObserveConfig, error) {
	cfg := OpenObserveConfig{
		LogStream:   OpenObserveDefaultLogStream,
		TraceStream: OpenObserveDefaultTraceStream,
	}

	openobserveIntegrations, err := core.ListIntegrationConfigs(sc, accountId, IntegrationOpenObserve)
	if err != nil {
		return cfg, fmt.Errorf("failed to list OpenObserve integration configs: %w", err)
	}
	if len(openobserveIntegrations) == 0 {
		return cfg, fmt.Errorf("openobserve integration not found for account: %s", accountId)
	}
	openobserveIntegration := openobserveIntegrations[0]
	for _, config := range openobserveIntegration.Configs {
		switch config.Name {
		case "openobserve_url":
			cfg.URL = config.Value
		case "openobserve_org_id":
			cfg.OrgID = config.Value
		case "openobserve_username":
			cfg.Username = config.Value
		case "openobserve_log_stream":
			// An integration saved before this field existed has no value; keep the default.
			if stream := strings.TrimSpace(config.Value); stream != "" {
				cfg.LogStream = stream
			}
		case "openobserve_trace_stream":
			if stream := strings.TrimSpace(config.Value); stream != "" {
				cfg.TraceStream = stream
			}
		case "openobserve_password":
			cfg.Password = config.Value
			if config.IsEncrypted {
				decrypted, err := common.Decrypt(config.Value)
				if err != nil {
					return cfg, fmt.Errorf("failed to decrypt OpenObserve password: %w", err)
				}
				cfg.Password = decrypted
			}
		}
	}

	cfg.URL = normalizeOpenObserveURL(cfg.URL)

	// Stream names are interpolated into SQL as bare identifiers, which cannot be
	// parameterized. Validating here rather than at each query site is deliberate: the
	// stream reaches SQL from seven different places across the log and trace sources,
	// and a per-site guard is one refactor away from being forgotten at the eighth.
	if !IsSafeOpenObserveStreamName(cfg.LogStream) {
		return cfg, fmt.Errorf("invalid openobserve_log_stream %q: stream names may only contain letters, digits and underscores", cfg.LogStream)
	}
	if !IsSafeOpenObserveStreamName(cfg.TraceStream) {
		return cfg, fmt.Errorf("invalid openobserve_trace_stream %q: stream names may only contain letters, digits and underscores", cfg.TraceStream)
	}

	return cfg, nil
}

// IsSafeOpenObserveStreamName reports whether a stream name is safe to interpolate into a
// SQL identifier position. OpenObserve itself normalizes stream names to letters, digits
// and underscores, so this rejects nothing a real stream could be called — it exists to
// stop a hostile or malformed integration config from breaking out of the quoted
// identifier in `FROM "<stream>"`.
func IsSafeOpenObserveStreamName(stream string) bool {
	if stream == "" || len(stream) > 255 {
		return false
	}
	for _, r := range stream {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}
