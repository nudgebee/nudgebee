package observability

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"nudgebee/services/common"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
	"time"
)

const (
	LokiUrl         = "loki_url"
	LokiRulesUrl    = "loki_rules_url"
	LokiAuthType    = "loki_auth_type"
	LokiUsername    = "loki_username"
	LokiPassword    = "loki_password"
	LokiBearerToken = "loki_bearer_token"
	LokiTenantId    = "loki_tenant_id"
	LokiHeaders     = "loki_headers"
)

// Authentication methods, mirroring the integration form's loki_auth_type values.
const (
	LokiAuthNone   = "none"
	LokiAuthBasic  = "basic"
	LokiAuthBearer = "bearer_token"
)

// LokiConfig is the resolved connection for a user-configured (non-agent) Loki.
type LokiConfig struct {
	Url string
	// RulesUrl is the ruler base URL when the ruler is a separate service from the
	// query endpoint. Empty means "same as Url" — resolved by RulerBaseURL.
	RulesUrl string
	// AuthType selects which credential below is sent. Only the selected method is
	// applied, so leftovers from a previously-chosen method are never used.
	AuthType    string
	Username    string
	Password    string
	BearerToken string
	// TenantID is sent as X-Scope-OrgID on every request (multi-tenant Loki).
	TenantID string
	// Headers carries any additional headers the deployment needs. Authentication is
	// not expressed here — AuthType owns it.
	Headers map[string]string
}

// RulerBaseURL returns the base URL for ruler API calls, falling back to the query
// URL when the deployment serves both from one endpoint (single-binary Loki).
func (c *LokiConfig) RulerBaseURL() string {
	if c.RulesUrl != "" {
		return c.RulesUrl
	}
	return c.Url
}

// GetLokiConfig reads and decrypts the user-sourced Loki integration configuration.
// Agent-sourced rows are filtered out: they carry no URL, and LokiSource (relay) is the
// source that serves them.
func GetLokiConfig(ctx *security.RequestContext, accountId string) (*LokiConfig, error) {
	dtos, err := core.ListIntegrationConfigs(ctx, accountId, "loki")
	if err != nil {
		return nil, fmt.Errorf("failed to get loki integration: %w", err)
	}

	var userDtos []core.IntegrationDto
	for _, dto := range dtos {
		if dto.Source == "user" {
			userDtos = append(userDtos, dto)
		}
	}
	if len(userDtos) == 0 {
		return nil, fmt.Errorf("no loki integration configured for account %s", accountId)
	}

	cfg := &LokiConfig{Headers: map[string]string{}}
	for _, c := range userDtos[0].Configs {
		value := c.Value
		if c.IsEncrypted && value != "" {
			decrypted, decErr := common.Decrypt(value)
			if decErr != nil {
				return nil, fmt.Errorf("failed to decrypt loki config %s: %w", c.Name, decErr)
			}
			value = decrypted
		}
		switch c.Name {
		case LokiUrl:
			cfg.Url = strings.TrimRight(strings.TrimSpace(value), "/")
		case LokiRulesUrl:
			cfg.RulesUrl = strings.TrimRight(strings.TrimSpace(value), "/")
		case LokiAuthType:
			cfg.AuthType = strings.TrimSpace(value)
		case LokiUsername:
			cfg.Username = strings.TrimSpace(value)
		case LokiPassword:
			cfg.Password = value
		case LokiBearerToken:
			cfg.BearerToken = strings.TrimSpace(value)
		case LokiTenantId:
			cfg.TenantID = strings.TrimSpace(value)
		case LokiHeaders:
			headers, hErr := core.ParseExtraHeaders(value)
			if hErr != nil {
				return nil, fmt.Errorf("loki integration has invalid %s: %w", LokiHeaders, hErr)
			}
			cfg.Headers = headers
		}
	}

	// Integrations saved before the auth selector existed carry no loki_auth_type;
	// infer it from the credentials present so they keep authenticating as they did.
	if cfg.AuthType == "" {
		switch {
		case cfg.Username != "" && cfg.Password != "":
			cfg.AuthType = LokiAuthBasic
		case cfg.BearerToken != "":
			cfg.AuthType = LokiAuthBearer
		default:
			cfg.AuthType = LokiAuthNone
		}
	}

	if cfg.Url == "" {
		return nil, fmt.Errorf("loki integration is missing %s", LokiUrl)
	}
	return cfg, nil
}

// lokiHTTPClient mirrors the agent's Loki client timeout (60s): Loki range queries over
// a wide window are routinely slower than the 30s other integrations use.
var lokiHTTPClient = &http.Client{Timeout: 60 * time.Second}

// lokiRequest executes an authenticated HTTP request against a user-configured Loki.
// Extra headers are applied first so the explicit credential fields win on conflict.
func lokiRequest(method, rawURL, body, contentType string, cfg *LokiConfig) (*http.Response, error) {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, rawURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create loki request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}
	// Only the selected method is applied. Sending a leftover credential from a method
	// the operator switched away from would turn an anonymous-read Loki into a 401.
	switch cfg.AuthType {
	case LokiAuthBasic:
		if cfg.Username != "" && cfg.Password != "" {
			req.SetBasicAuth(cfg.Username, cfg.Password)
		}
	case LokiAuthBearer:
		if cfg.BearerToken != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.BearerToken)
		}
	}
	if cfg.TenantID != "" {
		req.Header.Set("X-Scope-OrgID", cfg.TenantID)
	}
	return lokiHTTPClient.Do(req)
}

// readLokiResponse reads the body and turns any non-2xx into an error carrying the
// status and body. Loki puts the parse/validation reason in the body, and losing it
// is what makes a bad LogQL query look like "no logs found".
func readLokiResponse(resp *http.Response, operation string) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read loki %s response: %w", operation, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("loki %s: HTTP %d: %s", operation, resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}
	return bodyBytes, nil
}

// lokiEnvelope is the standard Loki API response wrapper. The relay path unwraps to
// the inner data object before parsing; the direct path has to do the same, because
// convertLokiResponse expects {resultType, result}, not the whole body.
type lokiEnvelope struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
}

// LokiSaasSource implements LogSource via direct HTTP calls to a user-configured Loki,
// with no agent in the path.
//
// It composes LokiSource by name rather than embedding it: LokiSource.QueryLogGroup
// calls QueryLogs on its own receiver, and Go embedding does not dispatch virtually,
// so an embedded QueryLogGroup would silently keep using the relay. Every method here
// is explicit, and the shared query building/parsing is reused verbatim.
type LokiSaasSource struct {
	shared LokiSource
}

func (s *LokiSaasSource) QueryLogs(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) ([]OutputLog, error) {
	cfg, err := GetLokiConfig(ctx, fetchLogRequest.AccountId)
	if err != nil {
		return nil, err
	}

	params, err := s.shared.prepareQueryParams(&fetchLogRequest)
	if err != nil {
		return nil, err
	}

	resp, err := lokiRequest(http.MethodGet, cfg.Url+"/loki/api/v1/query_range?"+params, "", "", cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to execute loki query: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := readLokiResponse(resp, "query_range")
	if err != nil {
		return nil, err
	}

	var envelope lokiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("loki.QueryLogs failed to unmarshal response: %w", err)
	}
	// A valid query over a window with no data (e.g. past retention) returns success
	// with a null data object. That is an empty result, not a failure.
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return []OutputLog{}, nil
	}

	outputLog, err := s.shared.convertLokiResponse(envelope.Data)
	if err != nil {
		return nil, fmt.Errorf("loki.QueryLogs failed to convert loki response in nudgebee log: %w", err)
	}
	return outputLog, nil
}

// lokiTimeRangeParams reproduces the window the relay path sends for label lookups.
// StartTime/EndTime are epoch milliseconds throughout this package; Loki wants nanoseconds.
func lokiTimeRangeParams(startTime, endTime int64) string {
	return fmt.Sprintf("start=%d&end=%d", startTime*1_000_000, endTime*1_000_000)
}

// sanitizeLokiQueryParams re-encodes a caller-supplied query string so it is safe to
// append to a URL. The frontend sends Loki-native "start=<ns>&end=<ns>" strings here,
// which are already valid API parameters.
func sanitizeLokiQueryParams(raw string) string {
	values, err := neturl.ParseQuery(raw)
	if err != nil {
		return ""
	}
	return values.Encode()
}

func (s *LokiSaasSource) QueryLabels(ctx *security.RequestContext, fetchLogRequest FetchLogLabelRequest) ([]OutputLogLabel, error) {
	cfg, err := GetLokiConfig(ctx, fetchLogRequest.AccountId)
	if err != nil {
		return nil, err
	}

	query := ""
	if fetchLogRequest.Request != nil {
		if queryStr, ok := fetchLogRequest.Request["query"].(string); ok {
			query = queryStr
		}
	}
	if query == "" {
		query = lokiTimeRangeParams(fetchLogRequest.StartTime, fetchLogRequest.EndTime)
	}

	values, err := s.fetchLokiStrings(cfg, "/loki/api/v1/labels?"+sanitizeLokiQueryParams(query), "labels")
	if err != nil {
		return nil, err
	}

	output := []OutputLogLabel{}
	for _, v := range values {
		output = append(output, OutputLogLabel{
			Label:      v,
			Attributes: map[string]interface{}{},
		})
	}
	return output, nil
}

func (s *LokiSaasSource) QueryLabelValues(ctx *security.RequestContext, fetchLogRequest FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	cfg, err := GetLokiConfig(ctx, fetchLogRequest.AccountId)
	if err != nil {
		return nil, err
	}
	if fetchLogRequest.LabelName == "" {
		return nil, fmt.Errorf("loki: label name is required")
	}

	query := ""
	if fetchLogRequest.Request != nil {
		if queryStr, ok := fetchLogRequest.Request["query"].(string); ok {
			query = queryStr
		}
	}
	// Mirrors LokiSource: fall back to an explicit window when the caller supplied one,
	// so probes that widen the range actually scope the value lookup.
	if query == "" && fetchLogRequest.StartTime > 0 && fetchLogRequest.EndTime > 0 {
		query = lokiTimeRangeParams(fetchLogRequest.StartTime, fetchLogRequest.EndTime)
	}

	path := "/loki/api/v1/label/" + neturl.PathEscape(fetchLogRequest.LabelName) + "/values"
	if params := sanitizeLokiQueryParams(query); params != "" {
		path += "?" + params
	}

	values, err := s.fetchLokiStrings(cfg, path, "label values")
	if err != nil {
		return nil, err
	}

	var output []OutputLogLabelValue
	for _, v := range values {
		output = append(output, OutputLogLabelValue{
			Value:      v,
			Attributes: map[string]interface{}{},
		})
	}
	return output, nil
}

// fetchLokiStrings GETs an endpoint whose success payload is a string array
// (labels, label values) and returns it, treating a null payload as empty.
func (s *LokiSaasSource) fetchLokiStrings(cfg *LokiConfig, path, operation string) ([]string, error) {
	resp, err := lokiRequest(http.MethodGet, cfg.Url+path, "", "", cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to execute loki %s query: %w", operation, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := readLokiResponse(resp, operation)
	if err != nil {
		return nil, err
	}

	var envelope struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("loki %s: failed to unmarshal response: %w", operation, err)
	}
	// Loki answers a valid query with a null data array when nothing exists in the
	// range (e.g. past retention). Empty result, not an error.
	if envelope.Data == nil {
		return []string{}, nil
	}
	return envelope.Data, nil
}

func (s *LokiSaasSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	logql := s.shared.buildLogGroupStreamQuery(req)

	logs, err := s.QueryLogs(ctx, FetchLogRequest{
		AccountId: req.AccountId,
		Query:     logql,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Limit:     1000,
	})
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("loki.QueryLogGroup: failed to fetch logs: %w", err)
	}

	return s.shared.groupLogsByPattern(logs, req.EndTime), nil
}

// The remaining LogSource surface is transport-independent and delegates to the
// shared implementation so the two Loki sources cannot drift.

func (s *LokiSaasSource) GetQuery(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) (string, error) {
	return s.shared.GetQuery(ctx, fetchLogRequest)
}

func (s *LokiSaasSource) GetLabelMapping() map[string]string {
	return s.shared.GetLabelMapping()
}

func (s *LokiSaasSource) GetSupportedOperators() []string {
	return s.shared.GetSupportedOperators()
}

func (s *LokiSaasSource) CanGenerateQuery(ctx playbooks.PlaybookActionContext) bool {
	return s.shared.CanGenerateQuery(ctx)
}

func (s *LokiSaasSource) GenerateQuery(ctx playbooks.PlaybookActionContext) (string, map[string]any, error) {
	return s.shared.GenerateQuery(ctx)
}
