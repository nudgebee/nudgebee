package integrations

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
	"time"
)

const (
	// IntegrationSplunkO11y is the internal name for the Splunk Observability Cloud integration.
	IntegrationSplunkO11y = "splunk_observability_platform"

	// Config key names stored in the database
	SplunkO11yConfigRealm       = "realm"        // e.g. "us1", "eu0", "us0"
	SplunkO11yConfigAccessToken = "access_token" // Organization/Realm access token

	// API base URL templates — realm is substituted at runtime
	SplunkO11yAPIBase    = "https://api.%s.signalfx.com"
	SplunkO11yStreamBase = "https://stream.%s.signalfx.com"

	// Authentication header for all Splunk Observability Cloud API calls
	SplunkO11yTokenHeader = "X-SF-TOKEN"

	// API path constants
	SplunkO11yLogSearchPath  = "/v1/log/search"         // Log Observer search
	SplunkO11ySignalFlowPath = "/v2/signalflow/execute" // SignalFlow metrics streaming
	SplunkO11yTracePath      = "/v2/trace"              // APM trace search
	SplunkO11yValidatePath   = "/v2/organization"       // Connection validation
	SplunkO11yMetricListPath = "/v2/metric"             // Metric catalog
	SplunkO11yDimensionPath  = "/v2/dimension"          // Dimension (label) catalog

	SplunkO11yDefaultLimit = 1000
	SplunkO11yTimeout      = 60 * time.Second
)

func init() {
	core.RegisterIntegration(SplunkO11y{})
}

// SplunkO11yConnConfig holds the resolved credentials for Splunk Observability Cloud API calls.
type SplunkO11yConnConfig struct {
	Realm       string
	AccessToken string
}

// apiBase returns the API base URL for the given realm.
func (c SplunkO11yConnConfig) apiBase() string {
	return fmt.Sprintf(SplunkO11yAPIBase, c.Realm)
}

// streamBase returns the streaming API base URL for the given realm.
func (c SplunkO11yConnConfig) streamBase() string {
	return fmt.Sprintf(SplunkO11yStreamBase, c.Realm)
}

// SplunkO11y implements core.Integration for Splunk Observability Cloud (formerly SignalFx).
type SplunkO11y struct{}

func (m SplunkO11y) Name() string {
	return IntegrationSplunkO11y
}

func (m SplunkO11y) Category() core.IntegrationCategory {
	return core.IntegrationCategoryObservabilityPlatform
}

func (m SplunkO11y) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Testable: true,
		Required: []string{SplunkO11yConfigRealm, SplunkO11yConfigAccessToken},
		Properties: map[string]core.IntegrationSchemaProperty{
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Name for this Splunk Observability Cloud integration",
				Default:     "",
				Priority:    100,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Accounts",
				Default:          nil,
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			SplunkO11yConfigRealm: {
				Type:        core.ToolSchemaTypeString,
				Description: "Splunk Observability Cloud realm. Find it in Settings → Organizations.",
				Enum:        []any{"us0", "us1", "us2", "eu0", "ap0", "jp0", "au0"},
				Priority:    88,
				IsTestable:  true,
			},
			SplunkO11yConfigAccessToken: {
				Type:        core.ToolSchemaTypeString,
				Description: "Organization access token. Find or create it in Settings → Access Tokens.",
				IsEncrypted: true,
				Priority:    72,
				IsTestable:  true,
			},
			// core.DefaultLogProvider: {
			// 	Type:        core.ToolSchemaTypeBoolean,
			// 	Description: "Make Splunk Observability Cloud the default Log Provider",
			// 	Default:     false,
			// 	Priority:    30,
			// },
			core.DefaultMetricsProvider: {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Make Splunk Observability Cloud the default Metrics Provider",
				Default:     false,
				Priority:    20,
			},
		},
	}
}

func (m SplunkO11y) ValidateConfig(securityContext *security.SecurityContext, integrationConfig []core.IntegrationConfigValue, accountId string) []error {
	realm := ""
	accessToken := ""

	for _, config := range integrationConfig {
		switch config.Name {
		case SplunkO11yConfigRealm:
			realm = config.Value
		case SplunkO11yConfigAccessToken:
			accessToken = config.Value
			if config.IsEncrypted {
				var err error
				accessToken, err = common.Decrypt(config.Value)
				if err != nil {
					return []error{fmt.Errorf("splunk observability: failed to decrypt access token: %w", err)}
				}
			}
		}
	}

	if realm == "" {
		return []error{fmt.Errorf("splunk observability: realm must not be empty")}
	}
	if accessToken == "" {
		return []error{fmt.Errorf("splunk observability: access_token must not be empty")}
	}

	cfg := SplunkO11yConnConfig{Realm: realm, AccessToken: accessToken}
	if err := validateSplunkO11yConnection(cfg); err != nil {
		return []error{fmt.Errorf("splunk observability config validation failed: %w", err)}
	}
	return nil
}

// validateSplunkO11yConnection tests the connection to Splunk Observability Cloud.
func validateSplunkO11yConnection(cfg SplunkO11yConnConfig) error {
	client := newSplunkO11yHTTPClient()
	reqURL := cfg.apiBase() + SplunkO11yValidatePath

	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create validation request: %w", err)
	}
	req.Header.Set(SplunkO11yTokenHeader, cfg.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to Splunk Observability Cloud API at %s: %w", cfg.apiBase(), err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		body = []byte{}
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return fmt.Errorf("invalid or expired access token (HTTP 401)")
	case http.StatusForbidden:
		return fmt.Errorf("access token does not have sufficient permissions (HTTP 403)")
	default:
		return fmt.Errorf("splunk Observability Cloud API returned status %d: %s", resp.StatusCode, string(body))
	}
}

// GetSplunkO11yConfigs retrieves realm and access_token from the database for the given account.
func GetSplunkO11yConfigs(sc *security.RequestContext, accountId string) (SplunkO11yConnConfig, error) {
	splunkIntegrations, listErr := core.ListIntegrationConfigs(sc, accountId, IntegrationSplunkO11y)
	if listErr != nil {
		return SplunkO11yConnConfig{}, fmt.Errorf("failed to list Splunk O11y integration configs: %w", listErr)
	}
	if len(splunkIntegrations) == 0 {
		return SplunkO11yConnConfig{}, fmt.Errorf("splunk Observability Cloud integration not found for account: %s", accountId)
	}

	var realm, accessToken string
	for _, config := range splunkIntegrations[0].Configs {
		switch config.Name {
		case SplunkO11yConfigRealm:
			realm = config.Value
		case SplunkO11yConfigAccessToken:
			accessToken = config.Value
			if config.IsEncrypted {
				var decryptErr error
				accessToken, decryptErr = common.Decrypt(config.Value)
				if decryptErr != nil {
					return SplunkO11yConnConfig{}, fmt.Errorf("failed to decrypt Splunk O11y access token: %w", decryptErr)
				}
			}
		}
	}

	if realm == "" {
		return SplunkO11yConnConfig{}, fmt.Errorf("splunk O11y realm not configured for account: %s", accountId)
	}
	if accessToken == "" {
		return SplunkO11yConnConfig{}, fmt.Errorf("splunk O11y access_token not configured for account: %s", accountId)
	}

	return SplunkO11yConnConfig{Realm: realm, AccessToken: accessToken}, nil
}

// --- Log Observer API ---

// O11yLogSearchRequest is the request body for the Log Observer search API.
type O11yLogSearchRequest struct {
	Query   string `json:"query"`
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
	Limit   int    `json:"limit"`
}

// O11yLogEntry represents a single log entry from the Log Observer API.
type O11yLogEntry struct {
	Timestamp  int64          `json:"timestamp"`
	Attributes map[string]any `json:"attributes"`
}

// O11yLogSearchResponse is the response from the Log Observer search API.
type O11yLogSearchResponse struct {
	Entries []O11yLogEntry `json:"entries"`
}

// ExecuteO11yLogSearch queries the Splunk Observability Cloud Log Observer API.
// query is a Lucene-style filter expression (e.g. "kubernetes.namespace.name:default severity:ERROR").
// startMs and endMs are Unix milliseconds.
func ExecuteO11yLogSearch(cfg SplunkO11yConnConfig, query string, startMs, endMs int64, limit int) ([]O11yLogEntry, error) {
	if limit <= 0 || limit > 5000 {
		limit = SplunkO11yDefaultLimit
	}

	reqBody := O11yLogSearchRequest{
		Query:   query,
		StartMs: startMs,
		EndMs:   endMs,
		Limit:   limit,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal log search request: %w", err)
	}

	client := newSplunkO11yHTTPClient()
	reqURL := cfg.apiBase() + SplunkO11yLogSearchPath

	req, err := http.NewRequest(http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create log search request: %w", err)
	}
	req.Header.Set(SplunkO11yTokenHeader, cfg.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("splunk O11y log search request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read log search response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("invalid or expired Splunk O11y access token (HTTP 401)")
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("splunk Log Observer API not found (HTTP 404): the Log Observer feature may not be enabled for your organization. Verify your realm and that Log Observer is included in your Splunk Observability Cloud subscription")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("splunk O11y log search returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var searchResp O11yLogSearchResponse
	if err := json.Unmarshal(respBody, &searchResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal log search response: %w", err)
	}
	return searchResp.Entries, nil
}

// --- SignalFlow API ---

// SignalFlowRequest is the JSON body for the SignalFlow execute endpoint.
// Time range and immediate flag are passed as URL query parameters, not in the body.
type SignalFlowRequest struct {
	ProgramText string `json:"programText"`
}

// SignalFlowMetadataLine represents a metadata SSE line from SignalFlow.
type SignalFlowMetadataLine struct {
	Type       string         `json:"type"`
	TsID       string         `json:"tsId"`
	Properties map[string]any `json:"properties"`
}

// SignalFlowDataEntry is one element in the "data" array of a SignalFlow data event.
type SignalFlowDataEntry struct {
	TsID  string  `json:"tsId"`
	Value float64 `json:"value"`
}

// SignalFlowDataLine represents a data SSE event from SignalFlow.
type SignalFlowDataLine struct {
	LogicalTimestampMs int64                 `json:"logicalTimestampMs"`
	Data               []SignalFlowDataEntry `json:"data"`
}

// SignalFlowDataPoint holds a single resolved time-series data point with labels.
type SignalFlowDataPoint struct {
	TimestampMs int64
	Value       float64
	Labels      map[string]string
	MetricName  string
}

// ExecuteSignalFlow runs a SignalFlow program and returns all data points.
// program is a SignalFlow expression, e.g. `data('cpu.utilization').mean().publish()`.
// startMs/stopMs are Unix milliseconds passed as URL query params (not in the JSON body).
func ExecuteSignalFlow(cfg SplunkO11yConnConfig, program string, startMs, endMs, resolutionMs int64) ([]SignalFlowDataPoint, error) {
	bodyBytes, err := json.Marshal(SignalFlowRequest{ProgramText: program})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal SignalFlow request: %w", err)
	}

	// Use a context timeout so the SSE stream read is cancelled if the server
	// never sends a terminal END_OF_CHANNEL event.
	ctx, cancel := context.WithTimeout(context.Background(), SplunkO11yTimeout)
	defer cancel()

	// Use a streaming client with no Timeout — http.Client.Timeout cancels the body
	// read after the deadline even if data is still arriving, which breaks SSE parsing.
	// The context (set above) provides the actual deadline for the entire operation.
	client := newSplunkO11yStreamHTTPClient()
	reqURL := cfg.streamBase() + SplunkO11ySignalFlowPath

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create SignalFlow request: %w", err)
	}

	// Time range and resolution go as URL query params — not in the JSON body.
	q := req.URL.Query()
	if startMs > 0 {
		q.Set("startMs", fmt.Sprintf("%d", startMs))
	}
	if endMs > 0 {
		q.Set("stopMs", fmt.Sprintf("%d", endMs))
	}
	if resolutionMs > 0 {
		q.Set("resolution", fmt.Sprintf("%d", resolutionMs))
	}
	req.URL.RawQuery = q.Encode()

	req.Header.Set(SplunkO11yTokenHeader, cfg.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	// Do NOT set Accept: application/json — SignalFlow always returns SSE (text/event-stream).

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("signalFlow request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("invalid or expired Splunk O11y access token (HTTP 401)")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body) //nolint:errcheck // best-effort error body for context
		return nil, fmt.Errorf("signalFlow returned status %d: %s", resp.StatusCode, string(body))
	}

	points, err := parseSignalFlowResponse(resp.Body)
	// Context deadline/cancel just means we stopped reading after the timeout.
	// Return whatever data points were collected rather than failing entirely.
	if err != nil && ctx.Err() != nil {
		return points, nil
	}
	return points, err
}

// parseSignalFlowResponse parses the SSE stream from SignalFlow.
// SSE format: each event is delimited by a blank line.
// Each event has an `event: <type>` line followed by one or more `data: <json-fragment>` lines.
// We correlate metadata events (tsId → metric name + labels) with data events.
func parseSignalFlowResponse(body io.Reader) ([]SignalFlowDataPoint, error) {
	metaByTsID := make(map[string]SignalFlowMetadataLine)
	var dataLines []SignalFlowDataLine

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var eventType string
	var dataBuf strings.Builder

	done := false
	flush := func() {
		raw := strings.TrimSpace(dataBuf.String())
		dataBuf.Reset()
		if raw == "" {
			return
		}
		switch eventType {
		case "metadata":
			var meta SignalFlowMetadataLine
			if err := json.Unmarshal([]byte(raw), &meta); err == nil && meta.TsID != "" {
				metaByTsID[meta.TsID] = meta
			}
		case "data":
			var dl SignalFlowDataLine
			if err := json.Unmarshal([]byte(raw), &dl); err == nil {
				dataLines = append(dataLines, dl)
			}
		case "control-message":
			// END_OF_CHANNEL signals all historical data has been delivered.
			var ctrl struct {
				Event string `json:"event"`
			}
			if err := json.Unmarshal([]byte(raw), &ctrl); err == nil &&
				(ctrl.Event == "END_OF_CHANNEL" || ctrl.Event == "CHANNEL_ABORT") {
				done = true
			}
		}
		eventType = ""
	}

	for !done && scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataBuf.WriteString(strings.TrimPrefix(line, "data:"))
		}
	}
	flush() // handle final event without trailing blank line

	// Correlate data entries with their metadata to produce labeled data points.
	// This runs before the scanner error check so partial results from a timed-out
	// stream are still returned (context deadline = we read all available data).
	var points []SignalFlowDataPoint
	for _, dl := range dataLines {
		for _, entry := range dl.Data {
			point := SignalFlowDataPoint{
				TimestampMs: dl.LogicalTimestampMs,
				Value:       entry.Value,
				Labels:      make(map[string]string),
			}
			if meta, ok := metaByTsID[entry.TsID]; ok {
				for k, v := range meta.Properties {
					switch k {
					case "sf_originatingMetric":
						// Prefer the original metric name over the computed alias.
						point.MetricName = fmt.Sprintf("%v", v)
					case "sf_metric":
						// Only use sf_metric as fallback if sf_originatingMetric not set.
						if point.MetricName == "" {
							point.MetricName = fmt.Sprintf("%v", v)
						}
					default:
						if !strings.HasPrefix(k, "sf_") {
							point.Labels[k] = fmt.Sprintf("%v", v)
						}
					}
				}
			}
			points = append(points, point)
		}
	}

	// Return any scanner error after correlation — context deadline just means we
	// read all available historical data; the caller will suppress it via ctx.Err().
	if err := scanner.Err(); err != nil {
		return points, fmt.Errorf("failed to read SignalFlow response: %w", err)
	}
	return points, nil
}

// --- APM Trace API ---

// ExecuteO11yTraceQuery queries the Splunk APM trace search API.
// query is a filter expression (e.g. "service.name:my-service").
// startMs and endMs are Unix milliseconds.
func ExecuteO11yTraceQuery(cfg SplunkO11yConnConfig, query string, startMs, endMs int64, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 100
	}

	client := newSplunkO11yHTTPClient()
	params := url.Values{}
	params.Set("startMs", fmt.Sprintf("%d", startMs))
	params.Set("endMs", fmt.Sprintf("%d", endMs))
	params.Set("limit", fmt.Sprintf("%d", limit))
	if query != "" {
		params.Set("query", query)
	}

	reqURL := cfg.apiBase() + SplunkO11yTracePath + "?" + params.Encode()
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace query request: %w", err)
	}
	req.Header.Set(SplunkO11yTokenHeader, cfg.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("splunk O11y trace query failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read trace query response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("invalid or expired Splunk O11y access token (HTTP 401)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("splunk O11y trace query returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal trace query response: %w", err)
	}

	// The response has a "traces" array
	if tracesRaw, ok := result["traces"]; ok {
		var traces []map[string]any
		if err := json.Unmarshal(tracesRaw, &traces); err != nil {
			return nil, fmt.Errorf("failed to unmarshal traces array: %w", err)
		}
		return traces, nil
	}
	return nil, nil
}

// --- Metric and Dimension Catalog APIs ---

// FetchO11yMetricList returns available metric names from the Splunk O11y catalog.
func FetchO11yMetricList(cfg SplunkO11yConnConfig, query string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 200
	}
	client := newSplunkO11yHTTPClient()
	params := url.Values{}
	if query != "" {
		params.Set("query", query)
	}
	params.Set("limit", fmt.Sprintf("%d", limit))

	reqURL := cfg.apiBase() + SplunkO11yMetricListPath + "?" + params.Encode()
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric list request: %w", err)
	}
	req.Header.Set(SplunkO11yTokenHeader, cfg.AccessToken)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("splunk O11y metric list request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		body = []byte{}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("splunk O11y metric list returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metric list response: %w", err)
	}

	names := make([]string, 0, len(result.Results))
	for _, r := range result.Results {
		if r.Name != "" {
			names = append(names, r.Name)
		}
	}
	return names, nil
}

// FetchO11yDimensions returns dimension names or values from the Splunk O11y catalog.
func FetchO11yDimensions(cfg SplunkO11yConnConfig, query string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 100
	}
	client := newSplunkO11yHTTPClient()
	params := url.Values{}
	if query != "" {
		params.Set("query", query)
	}
	params.Set("limit", fmt.Sprintf("%d", limit))

	reqURL := cfg.apiBase() + SplunkO11yDimensionPath + "?" + params.Encode()
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create dimension request: %w", err)
	}
	req.Header.Set(SplunkO11yTokenHeader, cfg.AccessToken)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("splunk O11y dimension request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		body = []byte{}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("splunk O11y dimension request returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal dimension response: %w", err)
	}
	return result.Results, nil
}

// --- Log enrichment for webhooks ---

// SplunkLog represents a parsed log entry (used by the webhook for evidence).
type SplunkLog struct {
	Timestamp  int64             `json:"timestamp"`
	Message    string            `json:"message"`
	Severity   string            `json:"severity"`
	Namespace  string            `json:"namespace,omitempty"`
	Pod        string            `json:"pod,omitempty"`
	Container  string            `json:"container,omitempty"`
	Attributes map[string]string `json:"attributes"`
}

// getSplunkO11yLogs fetches related logs from Splunk O11y Log Observer for webhook evidence.
func getSplunkO11yLogs(cfg SplunkO11yConnConfig, filter string, fromMs, toMs int64) ([]SplunkLog, event.EventEvidence, error) {
	entries, err := ExecuteO11yLogSearch(cfg, filter, fromMs, toMs, SplunkO11yDefaultLimit)
	if err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to fetch Splunk O11y logs: %w", err)
	}

	logs := make([]SplunkLog, 0, len(entries))
	for _, e := range entries {
		log := SplunkLog{
			Timestamp:  e.Timestamp,
			Attributes: make(map[string]string),
		}
		attrs := e.Attributes

		if msg, ok := attrs["message"].(string); ok {
			log.Message = msg
		} else if msg, ok := attrs["body"].(string); ok {
			log.Message = msg
		}
		if sev, ok := attrs["severity"].(string); ok {
			log.Severity = sev
		}
		if ns, ok := attrs["kubernetes.namespace.name"].(string); ok {
			log.Namespace = ns
		}
		if pod, ok := attrs["kubernetes.pod.name"].(string); ok {
			log.Pod = pod
		}
		if ctr, ok := attrs["kubernetes.container.name"].(string); ok {
			log.Container = ctr
		}
		for k, v := range attrs {
			if str, ok := v.(string); ok {
				log.Attributes[k] = str
			}
		}
		logs = append(logs, log)
	}

	evidence := event.EventEvidence{
		Type: "json",
		Data: map[string]any{
			"name": "Splunk Observability Cloud Logs",
			"data": logs,
		},
		Insight: []event.EventEvidenceInsight{
			{
				Message:  fmt.Sprintf("Fetched %d logs from Splunk Observability Cloud", len(logs)),
				Severity: "info",
			},
		},
		AdditionalInfo: map[string]any{
			"action_name":            "splunk_o11y_logs",
			"actual_action_name":     "splunk_o11y_logs",
			"action_title":           "Splunk Observability Cloud Logs",
			"conditional_expression": "",
		},
	}
	return logs, evidence, nil
}

// --- Query escaping ---

// EscapeO11yQueryString escapes special Lucene characters for safe use in Log Observer queries.
// Lucene special chars: + - && || ! ( ) { } [ ] ^ " ~ * ? : \ /
func EscapeO11yQueryString(s string) string {
	var sb strings.Builder
	for _, ch := range s {
		switch ch {
		case '+', '-', '!', '(', ')', '{', '}', '[', ']', '^', '"', '~', '*', '?', ':', '\\', '/':
			sb.WriteRune('\\')
			sb.WriteRune(ch)
		case '\n', '\r', '\t':
			sb.WriteRune(' ')
		default:
			sb.WriteRune(ch)
		}
	}
	return sb.String()
}

// EscapeO11yFieldValue escapes a field value for Lucene field:value syntax.
// For exact matches with spaces, wraps in quotes.
func EscapeO11yFieldValue(s string) string {
	if strings.ContainsAny(s, " \t\n") {
		// Wrap in quotes and escape internal double quotes
		escaped := strings.ReplaceAll(s, `"`, `\"`)
		return `"` + escaped + `"`
	}
	return EscapeO11yQueryString(s)
}

// --- HTTP client ---

// newSplunkO11yHTTPClient creates an HTTP client for standard O11y API calls (non-streaming).
func newSplunkO11yHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, //nolint:gosec
		},
		Timeout: SplunkO11yTimeout,
	}
}

// newSplunkO11yStreamHTTPClient creates an HTTP client for SSE streaming (SignalFlow).
// No Timeout is set — http.Client.Timeout cancels body reads after the deadline even
// while data is still arriving, which breaks streaming. The caller uses a context instead.
func newSplunkO11yStreamHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, //nolint:gosec
		},
	}
}
