package integrations

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"slices"
	"sort"
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
				// Testable so that editing only this field re-runs validation on save and
				// asks for a fresh connection test, which is where a missing stream is named.
				IsTestable: true,
			},
			"openobserve_trace_stream": {
				Type: core.ToolSchemaTypeString,
				Description: "Name of the OpenObserve stream holding traces. Leave as 'default' unless your " +
					"collector writes spans to a differently-named stream.",
				Default:  OpenObserveDefaultTraceStream,
				Priority: 64,
				// Testable so that editing only this field re-runs validation on save and
				// asks for a fresh connection test, which is where a missing stream is named.
				IsTestable: true,
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
	var openobserveURL, orgID, username, password, logStream, traceStream string
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
		case "openobserve_log_stream":
			logStream = c.Value
		case "openobserve_trace_stream":
			traceStream = c.Value
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
	// Blank means the default stream. Anything else must pass the same check
	// GetOpenObserveConfigs applies at query time — which fails every OpenObserve query
	// for the account, metrics included, so the name has to be caught here, on save.
	for _, stream := range []struct{ field, name string }{
		{"openobserve_log_stream", logStream},
		{"openobserve_trace_stream", traceStream},
	} {
		if name := strings.TrimSpace(stream.name); name != "" && !IsSafeOpenObserveStreamName(name) {
			errs = append(errs, fmt.Errorf("%s %q is not a valid OpenObserve stream name: use only letters, digits and underscores", stream.field, name))
		}
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

// openObserveStreamNoteLimit caps how many stream names a connection note lists.
const openObserveStreamNoteLimit = 10

// ConnectionNotes reports a configured log or trace stream the organization does not
// hold. A wrong stream name fails nothing: OpenObserve answers a search on it with
// "stream not found", which the query path reads as an idle window, so the log and trace
// screens are just empty. It is a note, not a TestConnection failure, because that probe
// also decides provider status, where failing would mark the working signals — metrics
// share this integration — disconnected too.
//
// A signal the organization holds no stream of at all is not reported: that is an org not
// sending it (or not yet), and a note on every test would be noise.
func (m OpenObserve) ConnectionNotes(sc *security.RequestContext, config []core.IntegrationConfigValue, accountId string) []string {
	var openobserveURL, orgID, username, password string
	logStream, traceStream := OpenObserveDefaultLogStream, OpenObserveDefaultTraceStream
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
		case "openobserve_log_stream":
			if name := strings.TrimSpace(c.Value); name != "" {
				logStream = name
			}
		case "openobserve_trace_stream":
			if name := strings.TrimSpace(c.Value); name != "" {
				traceStream = name
			}
		}
	}
	openobserveURL = normalizeOpenObserveURL(openobserveURL)
	orgID = strings.TrimSpace(orgID)
	username = strings.TrimSpace(username)

	var notes []string
	for _, stream := range []struct{ signal, streamType, field, name string }{
		{"log", "logs", "openobserve_log_stream", logStream},
		{"trace", "traces", "openobserve_trace_stream", traceStream},
	} {
		available, err := listOpenObserveStreams(openobserveURL, orgID, username, password, stream.streamType)
		if err != nil || len(available) == 0 || slices.Contains(available, stream.name) {
			continue
		}
		notes = append(notes, fmt.Sprintf("%s stream %q was not found in organization %q, so %s searches will "+
			"come back empty. Set %s to one of this organization's %s streams: %s.",
			strings.ToUpper(stream.signal[:1])+stream.signal[1:], stream.name, orgID, stream.signal,
			stream.field, stream.signal, summarizeOpenObserveStreams(available)))
	}
	return notes
}

// listOpenObserveStreams returns the names of the organization's streams of one type.
func listOpenObserveStreams(openobserveURL, orgID, username, password, streamType string) ([]string, error) {
	resp, err := common.HttpGet(
		fmt.Sprintf("%s/api/%s/streams?type=%s", openobserveURL, neturl.PathEscape(orgID), neturl.QueryEscape(streamType)),
		common.HttpWithHeaders(map[string]string{
			"Authorization": fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(username+":"+password))),
			"Content-Type":  "application/json",
		}),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenObserve streams list returned HTTP %d", resp.StatusCode)
	}

	var body struct {
		List []struct {
			Name       string `json:"name"`
			StreamType string `json:"stream_type"`
		} `json:"list"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(body.List))
	for _, s := range body.List {
		// The type filter is re-checked rather than trusted: a server that ignores it
		// would otherwise offer a metrics stream as a place to find logs.
		if s.Name != "" && (s.StreamType == "" || s.StreamType == streamType) {
			names = append(names, s.Name)
		}
	}
	return names, nil
}

// summarizeOpenObserveStreams lists stream names in order, capped so an organization
// with thousands of streams does not produce an unreadable note.
func summarizeOpenObserveStreams(names []string) string {
	sorted := slices.Clone(names)
	sort.Strings(sorted)
	if len(sorted) <= openObserveStreamNoteLimit {
		return strings.Join(sorted, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(sorted[:openObserveStreamNoteLimit], ", "), len(sorted)-openObserveStreamNoteLimit)
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

	openobserveIntegrations, err := core.ListActiveIntegrationConfigs(sc, accountId, IntegrationOpenObserve)
	if err != nil {
		return cfg, fmt.Errorf("failed to list OpenObserve integration configs: %w", err)
	}
	if len(openobserveIntegrations) == 0 {
		return cfg, fmt.Errorf("openobserve integration not found for account: %s", accountId)
	}
	if len(openobserveIntegrations) > 1 {
		return cfg, fmt.Errorf("multiple active openobserve integrations for account: %s", accountId)
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
