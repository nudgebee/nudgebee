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
	core.RegisterIntegration(SplunkEnterprise{})
}

// IntegrationSplunkEnterprise is the internal name for the Splunk Enterprise /
// Splunk Cloud Platform integration.
//
// Deliberately distinct from IntegrationSplunkO11y ("splunk_observability_platform"):
// these are different products with different APIs. Observability Cloud is SignalFx
// (realm + X-SF-TOKEN + SignalFlow); this is the core platform (management port 8089 +
// SPL over the search REST API). eventrule.providerToIntegration already aliases the
// bare string "splunk" onto the Observability Cloud name, so reusing either string here
// would silently reroute provider resolution for alert rules.
const IntegrationSplunkEnterprise = "splunk_enterprise"

// Config key names stored in the database.
const (
	SplunkEnterpriseConfigURL         = "splunk_url"
	SplunkEnterpriseConfigAuthType    = "splunk_auth_type"
	SplunkEnterpriseConfigToken       = "splunk_token"
	SplunkEnterpriseConfigUsername    = "splunk_username"
	SplunkEnterpriseConfigPassword    = "splunk_password"
	SplunkEnterpriseConfigLogIndex    = "splunk_log_index"
	SplunkEnterpriseConfigMetricIndex = "splunk_metric_index"
	SplunkEnterpriseConfigTraceIndex  = "splunk_trace_index"
	SplunkEnterpriseConfigApp         = "splunk_app"
	SplunkEnterpriseConfigInsecure    = "splunk_insecure_skip_verify"
)

// Auth type values for SplunkEnterpriseConfigAuthType.
const (
	SplunkEnterpriseAuthToken = "token"
	SplunkEnterpriseAuthBasic = "basic"
)

// SplunkEnterpriseDefaultLogIndex is the index logs are read from unless configured
// otherwise. "main" is Splunk's out-of-the-box catch-all; a cluster fed by the Splunk
// OTel collector chart normally writes somewhere explicit instead.
const SplunkEnterpriseDefaultLogIndex = "main"

// SplunkEnterpriseDefaultMetricIndex is empty on purpose: unlike logs, there is no
// stock metrics index in Splunk. A metrics index has to be created explicitly with
// datatype=metric, so guessing a name would produce searches against an index that
// does not exist. Empty means "metrics not configured" and the metric source stays off.
const SplunkEnterpriseDefaultMetricIndex = ""

// SplunkEnterpriseDefaultTraceIndex is empty for the same reason as the metric index,
// and one more: Splunk Enterprise has no trace store at all. Spans only exist in an
// index if an OpenTelemetry Collector was pointed at it with the splunk_hec exporter,
// which is an explicit deployment choice. Empty means "traces not configured", and the
// trace source stays off rather than searching an index that holds no spans.
const SplunkEnterpriseDefaultTraceIndex = ""

// SplunkEnterpriseDefaultApp is the app namespace searches run in. Every stock install
// has "search"; the namespace only affects which knowledge objects (field extractions,
// macros, lookups) are in scope for the search.
const SplunkEnterpriseDefaultApp = "search"

// SplunkEnterpriseServerInfoPath is the cheapest authenticated endpoint that proves both
// reachability and credentials. It exists on every Splunk version and needs no
// capability beyond a valid session.
const SplunkEnterpriseServerInfoPath = "/services/server/info"

type SplunkEnterprise struct{}

func (m SplunkEnterprise) Name() string {
	return IntegrationSplunkEnterprise
}

func (m SplunkEnterprise) Category() core.IntegrationCategory {
	return core.IntegrationCategoryObservabilityPlatform
}

func (m SplunkEnterprise) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{SplunkEnterpriseConfigURL, SplunkEnterpriseConfigAuthType},
		Testable: true,
		Description: "Connects to Splunk Enterprise or Splunk Cloud Platform over the search REST API. " +
			"For Splunk Observability Cloud (SignalFx) use the separate Splunk Observability Cloud integration instead.",
		Properties: map[string]core.IntegrationSchemaProperty{
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Custom name for this Splunk Enterprise integration.",
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
			SplunkEnterpriseConfigURL: {
				Type: core.ToolSchemaTypeString,
				Description: "Base URL of the Splunk management endpoint, including the port " +
					"(e.g. https://splunk.example.com:8089). This is the management port (8089), " +
					"not the web UI port (8000).",
				Priority:   88,
				IsTestable: true,
			},
			SplunkEnterpriseConfigAuthType: {
				Type:        core.ToolSchemaTypeString,
				Description: "Authentication method. Prefer a bearer token; Basic auth is available for installs without token authentication enabled.",
				Enum:        []any{SplunkEnterpriseAuthToken, SplunkEnterpriseAuthBasic},
				Default:     SplunkEnterpriseAuthToken,
				Priority:    86,
				IsTestable:  true,
			},
			SplunkEnterpriseConfigToken: {
				Type:         core.ToolSchemaTypeString,
				Description:  "Splunk authentication token (Settings -> Tokens). Sent as an Authorization: Bearer header.",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{SplunkEnterpriseConfigAuthType: SplunkEnterpriseAuthToken},
				RequiredWhen: map[string]any{SplunkEnterpriseConfigAuthType: SplunkEnterpriseAuthToken},
				Priority:     84,
				IsTestable:   true,
			},
			SplunkEnterpriseConfigUsername: {
				Type:         core.ToolSchemaTypeString,
				Description:  "Splunk username for Basic auth.",
				ShowWhen:     map[string]any{SplunkEnterpriseConfigAuthType: SplunkEnterpriseAuthBasic},
				RequiredWhen: map[string]any{SplunkEnterpriseConfigAuthType: SplunkEnterpriseAuthBasic},
				Priority:     82,
				IsTestable:   true,
			},
			SplunkEnterpriseConfigPassword: {
				Type:         core.ToolSchemaTypeString,
				Description:  "Splunk password for Basic auth.",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{SplunkEnterpriseConfigAuthType: SplunkEnterpriseAuthBasic},
				RequiredWhen: map[string]any{SplunkEnterpriseConfigAuthType: SplunkEnterpriseAuthBasic},
				Priority:     80,
				IsTestable:   true,
			},
			SplunkEnterpriseConfigLogIndex: {
				Type: core.ToolSchemaTypeString,
				Description: "Index holding Kubernetes logs (e.g. otel_logs). Every generated search is " +
					"pinned to this index.",
				Default:  SplunkEnterpriseDefaultLogIndex,
				Priority: 78,
			},
			SplunkEnterpriseConfigMetricIndex: {
				Type: core.ToolSchemaTypeString,
				Description: "Metrics index holding Kubernetes metrics (e.g. otel_metrics). Must be an " +
					"index created with datatype=metric. Leave empty if this Splunk holds no metrics — " +
					"metric queries stay disabled rather than searching an index that does not exist.",
				Default:  SplunkEnterpriseDefaultMetricIndex,
				Priority: 77,
			},
			SplunkEnterpriseConfigTraceIndex: {
				Type: core.ToolSchemaTypeString,
				Description: "Index holding OpenTelemetry spans (e.g. otel_traces), as written by the " +
					"OpenTelemetry Collector's splunk_hec exporter. Leave empty if this Splunk holds no " +
					"traces — trace queries stay disabled rather than searching an index with no spans.",
				Default:  SplunkEnterpriseDefaultTraceIndex,
				Priority: 76,
			},
			SplunkEnterpriseConfigApp: {
				Type: core.ToolSchemaTypeString,
				Description: "App namespace searches run in. Controls which field extractions and macros " +
					"are in scope. Leave as 'search' unless your field extractions live in another app.",
				Default:  SplunkEnterpriseDefaultApp,
				Priority: 40,
			},
			SplunkEnterpriseConfigInsecure: {
				Type: core.ToolSchemaTypeBoolean,
				Description: "Skip TLS certificate verification. Needed for the self-signed certificate the " +
					"Splunk Operator for Kubernetes issues by default. Leave off when Splunk presents a trusted certificate.",
				Default:  false,
				Priority: 30,
			},
			core.DefaultLogProvider: {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Make Splunk Enterprise the default Log Provider",
				Default:     false,
				Priority:    15,
			},
			core.DefaultMetricsProvider: {
				Type: core.ToolSchemaTypeBoolean,
				Description: "Make Splunk Enterprise the default Metrics Provider. Requires " +
					"a metrics index above — without one there are no metrics to query.",
				Default:  false,
				Priority: 14,
			},
			core.DefaultTraceProvider: {
				Type: core.ToolSchemaTypeBoolean,
				Description: "Make Splunk Enterprise the default Trace Provider. Requires " +
					"a trace index above — without one there are no spans to query.",
				Default:  false,
				Priority: 13,
			},
		},
	}
}

// splunkEnterpriseReadConfig indexes a config list by name. Shared by ValidateConfig and
// TestConnection, which are handed not-yet-persisted form values, and by
// GetSplunkEnterpriseConfig, which is handed the stored row.
func splunkEnterpriseReadConfig(config []core.IntegrationConfigValue) map[string]core.IntegrationConfigValue {
	out := make(map[string]core.IntegrationConfigValue, len(config))
	for _, c := range config {
		out[c.Name] = c
	}
	return out
}

func (m SplunkEnterprise) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	values := splunkEnterpriseReadConfig(config)

	rawURL := strings.TrimSpace(values[SplunkEnterpriseConfigURL].Value)
	normalized := NormalizeSplunkEnterpriseURL(rawURL)
	authType := splunkEnterpriseAuthType(values[SplunkEnterpriseConfigAuthType].Value)

	var errs []error
	if normalized == "" {
		errs = append(errs, fmt.Errorf("%s is required", SplunkEnterpriseConfigURL))
	} else if !strings.HasPrefix(normalized, "http://") && !strings.HasPrefix(normalized, "https://") {
		errs = append(errs, fmt.Errorf("%s must start with http:// or https:// (got %q)", SplunkEnterpriseConfigURL, normalized))
	} else if parsed, err := neturl.Parse(rawURL); err == nil && parsed.Path != "" && parsed.Path != "/" {
		errs = append(errs, fmt.Errorf("%s must be the base URL only - remove the path after the host (use %q, not %q)",
			SplunkEnterpriseConfigURL, normalized, rawURL))
	}

	switch authType {
	case SplunkEnterpriseAuthToken:
		if strings.TrimSpace(values[SplunkEnterpriseConfigToken].Value) == "" {
			errs = append(errs, fmt.Errorf("%s is required when %s is %q",
				SplunkEnterpriseConfigToken, SplunkEnterpriseConfigAuthType, SplunkEnterpriseAuthToken))
		}
	case SplunkEnterpriseAuthBasic:
		if strings.TrimSpace(values[SplunkEnterpriseConfigUsername].Value) == "" {
			errs = append(errs, fmt.Errorf("%s is required when %s is %q",
				SplunkEnterpriseConfigUsername, SplunkEnterpriseConfigAuthType, SplunkEnterpriseAuthBasic))
		}
		if values[SplunkEnterpriseConfigPassword].Value == "" {
			errs = append(errs, fmt.Errorf("%s is required when %s is %q",
				SplunkEnterpriseConfigPassword, SplunkEnterpriseConfigAuthType, SplunkEnterpriseAuthBasic))
		}
	default:
		errs = append(errs, fmt.Errorf("%s must be %q or %q (got %q)",
			SplunkEnterpriseConfigAuthType, SplunkEnterpriseAuthToken, SplunkEnterpriseAuthBasic, authType))
	}

	// Index and app names are interpolated into SPL as bare tokens and cannot be
	// parameterized, so they are validated at the boundary rather than at each query
	// site - the same reasoning as GetOpenObserveConfigs, and more load-bearing here
	// because the SPL pipeline reaches commands like `delete` and `outputlookup`.
	//
	// All three index fields are checked, not just the log one: each is interpolated into
	// SPL the same way, and rejecting a bad name only at query time turns a typo in the
	// form into an opaque failure on the Logs/Metrics/Traces tab instead of an error next
	// to the field that caused it.
	for _, key := range []string{
		SplunkEnterpriseConfigLogIndex,
		SplunkEnterpriseConfigMetricIndex,
		SplunkEnterpriseConfigTraceIndex,
	} {
		if index := strings.TrimSpace(values[key].Value); index != "" && !IsSafeSplunkIndexName(index) {
			errs = append(errs, fmt.Errorf("invalid %s %q: index names may only contain letters, digits, underscores and hyphens",
				key, index))
		}
	}
	if app := strings.TrimSpace(values[SplunkEnterpriseConfigApp].Value); app != "" && !IsSafeSplunkIndexName(app) {
		errs = append(errs, fmt.Errorf("invalid %s %q: app names may only contain letters, digits, underscores and hyphens",
			SplunkEnterpriseConfigApp, app))
	}

	return errs
}

// TestConnection implements core.TestableIntegration.
func (m SplunkEnterprise) TestConnection(sc *security.RequestContext, config []core.IntegrationConfigValue, accountId string) error {
	cfg, err := splunkEnterpriseConfigFromValues(config)
	if err != nil {
		return err
	}

	opts := []common.HttpOption{
		common.HttpWithHeaders(cfg.AuthHeaders()),
		common.HttpWithQueryParams(map[string]string{"output_mode": "json"}),
		common.HttpWithTimeout(15 * time.Second),
	}
	if cfg.InsecureSkipVerify {
		opts = append(opts, common.HttpWithInsecureSkipVerify())
	}

	resp, err := common.HttpGet(cfg.URL+SplunkEnterpriseServerInfoPath, opts...)
	if err != nil {
		if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "connection refused") {
			return fmt.Errorf("failed to connect to Splunk at %s - the server may be down, or a tunnel/port-forward may have died: %w", cfg.URL, err)
		}
		if strings.Contains(err.Error(), "x509") || strings.Contains(err.Error(), "certificate") {
			return fmt.Errorf("TLS verification failed for Splunk at %s - the Splunk Operator issues a self-signed certificate by default; enable %s if that is expected: %w",
				cfg.URL, SplunkEnterpriseConfigInsecure, err)
		}
		return fmt.Errorf("failed to connect to Splunk at %s: %w", cfg.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		if cfg.AuthType == SplunkEnterpriseAuthToken {
			return fmt.Errorf("splunk rejected the token (HTTP 401) - check %s, and that token authentication is enabled on this instance", SplunkEnterpriseConfigToken)
		}
		return fmt.Errorf("invalid Splunk credentials (HTTP 401) - check %s and %s", SplunkEnterpriseConfigUsername, SplunkEnterpriseConfigPassword)
	case http.StatusForbidden:
		return fmt.Errorf("insufficient Splunk permissions (HTTP 403) - the account needs at least the 'search' capability")
	case http.StatusNotFound:
		return fmt.Errorf("splunk %s not found at %s - check that %s points at the management port (usually 8089), not the web UI port (8000)",
			SplunkEnterpriseServerInfoPath, cfg.URL, SplunkEnterpriseConfigURL)
	default:
		return fmt.Errorf("splunk API returned unexpected status: HTTP %d", resp.StatusCode)
	}
}

// NormalizeSplunkEnterpriseURL trims whitespace and strips any path/query/fragment so a
// URL pasted from the browser still works. The port is preserved - for Splunk it is
// load-bearing (8089 management vs 8000 web UI), so unlike a hostname-only service we
// must never drop it.
func NormalizeSplunkEnterpriseURL(raw string) string {
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

// IsSafeSplunkIndexName reports whether a name is safe to interpolate into SPL as a bare
// token. Splunk index names allow letters, digits, underscores and hyphens; internal
// indexes such as _internal and _audit legitimately lead with an underscore, so a leading
// underscore is permitted. The check exists to stop a hostile or malformed integration
// config from closing the quoted literal in index="<name>" and appending a pipeline
// command of its own.
func IsSafeSplunkIndexName(name string) bool {
	if name == "" || len(name) > 255 {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func splunkEnterpriseAuthType(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "", SplunkEnterpriseAuthToken:
		return SplunkEnterpriseAuthToken
	case SplunkEnterpriseAuthBasic:
		return SplunkEnterpriseAuthBasic
	default:
		return normalized
	}
}

// SplunkEnterpriseConfig is the resolved per-account Splunk connection.
type SplunkEnterpriseConfig struct {
	URL      string
	AuthType string
	Token    string
	Username string
	Password string
	// LogIndex is the index logs are read from. Always populated - it falls back to
	// SplunkEnterpriseDefaultLogIndex when unconfigured.
	LogIndex string
	// MetricIndex is the metrics index. Empty means metrics are not configured for this
	// Splunk, which is the normal case: a metrics index must be created explicitly with
	// datatype=metric, so there is no safe default to fall back to.
	MetricIndex string
	// TraceIndex is the span index. Empty means traces are not configured for this
	// account; see SplunkEnterpriseDefaultTraceIndex.
	TraceIndex string
	// App is the search app namespace. Always populated, defaulting to
	// SplunkEnterpriseDefaultApp.
	App                string
	InsecureSkipVerify bool
}

// AuthHeaders returns the headers every Splunk REST call needs.
func (c SplunkEnterpriseConfig) AuthHeaders() map[string]string {
	if c.AuthType == SplunkEnterpriseAuthBasic {
		return map[string]string{
			"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(c.Username+":"+c.Password)),
		}
	}
	return map[string]string{"Authorization": "Bearer " + c.Token}
}

// SearchEndpoint returns the search-jobs URL for this connection, scoped to the
// configured app namespace so that app's field extractions and macros are in scope.
func (c SplunkEnterpriseConfig) SearchEndpoint() string {
	return fmt.Sprintf("%s/servicesNS/-/%s/search/v2/jobs", c.URL, neturl.PathEscape(c.App))
}

// splunkEnterpriseConfigFromValues resolves a config list into a connection, decrypting
// secrets and applying defaults. Shared by TestConnection (unsaved form values) and
// GetSplunkEnterpriseConfig (stored row) so both paths normalize identically.
func splunkEnterpriseConfigFromValues(config []core.IntegrationConfigValue) (SplunkEnterpriseConfig, error) {
	values := splunkEnterpriseReadConfig(config)

	cfg := SplunkEnterpriseConfig{
		LogIndex: SplunkEnterpriseDefaultLogIndex,
		App:      SplunkEnterpriseDefaultApp,
	}

	cfg.URL = NormalizeSplunkEnterpriseURL(values[SplunkEnterpriseConfigURL].Value)
	cfg.AuthType = splunkEnterpriseAuthType(values[SplunkEnterpriseConfigAuthType].Value)
	cfg.Username = strings.TrimSpace(values[SplunkEnterpriseConfigUsername].Value)

	// An integration saved before a field existed has no value; keep the default.
	if index := strings.TrimSpace(values[SplunkEnterpriseConfigLogIndex].Value); index != "" {
		cfg.LogIndex = index
	}
	cfg.MetricIndex = strings.TrimSpace(values[SplunkEnterpriseConfigMetricIndex].Value)
	cfg.TraceIndex = strings.TrimSpace(values[SplunkEnterpriseConfigTraceIndex].Value)
	if app := strings.TrimSpace(values[SplunkEnterpriseConfigApp].Value); app != "" {
		cfg.App = app
	}
	cfg.InsecureSkipVerify = strings.EqualFold(strings.TrimSpace(values[SplunkEnterpriseConfigInsecure].Value), "true")

	token, err := splunkEnterpriseSecret(values[SplunkEnterpriseConfigToken])
	if err != nil {
		return cfg, fmt.Errorf("failed to decrypt %s: %w", SplunkEnterpriseConfigToken, err)
	}
	cfg.Token = token

	password, err := splunkEnterpriseSecret(values[SplunkEnterpriseConfigPassword])
	if err != nil {
		return cfg, fmt.Errorf("failed to decrypt %s: %w", SplunkEnterpriseConfigPassword, err)
	}
	cfg.Password = password

	if !IsSafeSplunkIndexName(cfg.LogIndex) {
		return cfg, fmt.Errorf("invalid %s %q: index names may only contain letters, digits, underscores and hyphens",
			SplunkEnterpriseConfigLogIndex, cfg.LogIndex)
	}
	if cfg.TraceIndex != "" && !IsSafeSplunkIndexName(cfg.TraceIndex) {
		return cfg, fmt.Errorf("invalid %s %q: index names may only contain letters, digits, underscores and hyphens",
			SplunkEnterpriseConfigTraceIndex, cfg.TraceIndex)
	}
	if cfg.MetricIndex != "" && !IsSafeSplunkIndexName(cfg.MetricIndex) {
		return cfg, fmt.Errorf("invalid %s %q: index names may only contain letters, digits, underscores and hyphens",
			SplunkEnterpriseConfigMetricIndex, cfg.MetricIndex)
	}
	if !IsSafeSplunkIndexName(cfg.App) {
		return cfg, fmt.Errorf("invalid %s %q: app names may only contain letters, digits, underscores and hyphens",
			SplunkEnterpriseConfigApp, cfg.App)
	}

	return cfg, nil
}

func splunkEnterpriseSecret(value core.IntegrationConfigValue) (string, error) {
	if value.Value == "" {
		return "", nil
	}
	if !value.IsEncrypted {
		return strings.TrimSpace(value.Value), nil
	}
	decrypted, err := common.Decrypt(value.Value)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(decrypted), nil
}

// GetSplunkEnterpriseConfig retrieves and decrypts the Splunk Enterprise configuration
// for an account.
func GetSplunkEnterpriseConfig(sc *security.RequestContext, accountId string) (SplunkEnterpriseConfig, error) {
	integrationConfigs, err := core.ListIntegrationConfigs(sc, accountId, IntegrationSplunkEnterprise)
	if err != nil {
		return SplunkEnterpriseConfig{}, fmt.Errorf("failed to list Splunk Enterprise integration configs: %w", err)
	}
	if len(integrationConfigs) == 0 {
		return SplunkEnterpriseConfig{}, fmt.Errorf("splunk enterprise integration not found for account: %s", accountId)
	}
	return splunkEnterpriseConfigFromValues(integrationConfigs[0].Configs)
}
