package observability

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
	"time"
)

const (
	PinotUrl          = "pinot_url"
	PinotAuthType     = "auth_type"
	PinotUsername     = "username"
	PinotPassword     = "password"
	PinotBearerToken  = "bearer_token"
	PinotTable        = "pinot_table"
	PinotTimestampCol = "pinot_timestamp_col"
	PinotMessageCol   = "pinot_message_col"
	PinotSeverityCol  = "pinot_severity_col"
	PinotNamespaceCol = "pinot_namespace_col"
	PinotPodCol       = "pinot_pod_col"
	PinotContainerCol = "pinot_container_col"
)

// PinotConfig holds the resolved configuration for a direct Pinot integration.
type PinotConfig struct {
	Url          string
	AuthType     string // "none", "basic", "bearer_token"
	Username     string
	Password     string
	BearerToken  string
	Table        string
	TimestampCol string
	MessageCol   string
	SeverityCol  string
	NamespaceCol string // log-group grouping column (default: namespace)
	PodCol       string // log-group grouping column (default: pod)
	ContainerCol string // log-group grouping column (default: container)
}

// GetPinotConfig reads and decrypts the Pinot integration configuration.
func GetPinotConfig(ctx *security.RequestContext, accountId string) (*PinotConfig, error) {
	dtos, err := core.ListIntegrationConfigs(ctx, accountId, "pinot")
	if err != nil {
		return nil, fmt.Errorf("failed to get pinot integration: %w", err)
	}

	var userDtos []core.IntegrationDto
	for _, dto := range dtos {
		if dto.Source == "user" {
			userDtos = append(userDtos, dto)
		}
	}
	if len(userDtos) == 0 {
		return nil, fmt.Errorf("no pinot integration configured for account %s", accountId)
	}

	cfg := &PinotConfig{
		AuthType:     "none",
		TimestampCol: "ingest_hour",
		MessageCol:   "log",
		NamespaceCol: "namespace",
		PodCol:       "pod",
		ContainerCol: "container",
	}

	for _, c := range userDtos[0].Configs {
		value := c.Value
		if c.IsEncrypted && value != "" {
			decrypted, decErr := common.Decrypt(value)
			if decErr != nil {
				return nil, fmt.Errorf("failed to decrypt pinot config %s: %w", c.Name, decErr)
			}
			value = decrypted
		}
		switch c.Name {
		case PinotUrl:
			cfg.Url = value
		case PinotAuthType:
			cfg.AuthType = value
		case PinotUsername:
			cfg.Username = value
		case PinotPassword:
			cfg.Password = value
		case PinotBearerToken:
			cfg.BearerToken = value
		case PinotTable:
			cfg.Table = value
		case PinotTimestampCol:
			cfg.TimestampCol = value
		case PinotMessageCol:
			cfg.MessageCol = value
		case PinotSeverityCol:
			cfg.SeverityCol = value
		case PinotNamespaceCol:
			cfg.NamespaceCol = value
		case PinotPodCol:
			cfg.PodCol = value
		case PinotContainerCol:
			cfg.ContainerCol = value
		}
	}

	if cfg.Url == "" {
		return nil, fmt.Errorf("pinot integration is missing pinot_url")
	}
	if cfg.Table == "" {
		return nil, fmt.Errorf("pinot integration is missing pinot_table")
	}
	if cfg.TimestampCol == "" {
		return nil, fmt.Errorf("pinot integration is missing pinot_timestamp_col")
	}
	if cfg.MessageCol == "" {
		return nil, fmt.Errorf("pinot integration is missing pinot_message_col")
	}
	cfg.Url = strings.TrimRight(cfg.Url, "/")
	if cfg.AuthType == "" {
		cfg.AuthType = "none"
	}
	return cfg, nil
}

// pinotHTTPClient skips TLS verification for user-managed Pinot installations
// that may use self-signed certificates.
var pinotHTTPClient = func() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // User-configured Pinot with self-signed certs
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}
}()

// pinotRequest executes an authenticated HTTP request against the Pinot controller.
func pinotRequest(method, rawURL, bodyJSON string, cfg *PinotConfig) (*http.Response, error) {
	var bodyReader io.Reader
	if bodyJSON != "" {
		bodyReader = strings.NewReader(bodyJSON)
	}
	req, err := http.NewRequest(method, rawURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create pinot request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	switch cfg.AuthType {
	case "basic":
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(cfg.Username+":"+cfg.Password)))
	case "bearer_token":
		req.Header.Set("Authorization", "Bearer "+cfg.BearerToken)
	}

	return pinotHTTPClient.Do(req)
}

// fetchPinotSchemaDirect fetches the Pinot table schema via direct HTTP and caches it.
// Cache key: "user:{url}:{table}"
func fetchPinotSchemaDirect(accountId string, cfg *PinotConfig) (*pinotSchemaResponse, error) {
	cacheKey := "user:" + cfg.Url + ":" + cfg.Table
	if cached, ok := getCachedPinotSchema(cacheKey); ok {
		return cached, nil
	}

	resp, err := pinotRequest("GET", fmt.Sprintf("%s/schemas/%s", cfg.Url, cfg.Table), "", cfg)
	if err != nil {
		return nil, fmt.Errorf("fetchPinotSchemaDirect: %w", err)
	}
	bodyBytes, err := readPinotResponse(resp, "schema fetch")
	if err != nil {
		return nil, err
	}

	schema, _, err := parsePinotSchemaBytes(bodyBytes)
	if err != nil {
		return nil, err
	}
	if schema != nil {
		setPinotSchemaCache(cacheKey, schema)
	}
	return schema, nil
}

// readPinotResponse reads the HTTP response body and returns an error for non-200 responses.
func readPinotResponse(resp *http.Response, operation string) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read pinot %s response: %w", operation, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pinot %s returned HTTP %d: %s", operation, resp.StatusCode, string(bodyBytes))
	}
	return bodyBytes, nil
}

// PinotSaasSource implements LogSource via direct HTTP calls to the Pinot controller REST API.
type PinotSaasSource struct{}

func (p *PinotSaasSource) GetLabelMapping() map[string]string {
	return map[string]string{}
}

// GetDynamicLabelMapping exposes the integration-configured column names as a
// canonical → Pinot-column mapping so getMergedLabelMapping can layer it on top
// of tenant_attrs.log_labels / cloud_account_attrs.log_labels. The integration
// form has the highest precedence — see DynamicLabelMappingSource doc.
func (p *PinotSaasSource) GetDynamicLabelMapping(ctx *security.RequestContext, accountId string) map[string]string {
	cfg, err := GetPinotConfig(ctx, accountId)
	if err != nil {
		return map[string]string{}
	}
	m := map[string]string{}
	if cfg.NamespaceCol != "" {
		m["namespace"] = cfg.NamespaceCol
	}
	if cfg.PodCol != "" {
		m["pod"] = cfg.PodCol
	}
	if cfg.ContainerCol != "" {
		m["container"] = cfg.ContainerCol
	}
	if cfg.SeverityCol != "" {
		m["level"] = cfg.SeverityCol
	}
	if cfg.MessageCol != "" {
		m["message"] = cfg.MessageCol
	}
	if cfg.TimestampCol != "" {
		m["timestamp"] = cfg.TimestampCol
	}
	return m
}

// applyMergedLabelOverrides layers the merged mapping (tenant/account/dynamic)
// on top of cfg's column fields. With dynamic-on-top precedence this is a no-op
// for keys the integration form sets, but it lets tenant/account fill in keys
// the form left empty (notably cfg.SeverityCol).
func (p *PinotSaasSource) applyMergedLabelOverrides(ctx *security.RequestContext, accountId string, cfg *PinotConfig) {
	merged := getMergedLabelMapping(ctx, accountId, p)
	if len(merged) == 0 {
		return
	}
	if v := merged["namespace"]; v != "" {
		cfg.NamespaceCol = v
	}
	if v := merged["pod"]; v != "" {
		cfg.PodCol = v
	}
	if v := merged["container"]; v != "" {
		cfg.ContainerCol = v
	}
	if v := merged["level"]; v != "" {
		cfg.SeverityCol = v
	}
	if v := merged["message"]; v != "" {
		cfg.MessageCol = v
	}
	if v := merged["timestamp"]; v != "" {
		cfg.TimestampCol = v
	}
}

func (p *PinotSaasSource) GetSupportedOperators() []string {
	return []string{
		"_eq", "_neq", "_contains",
		"_regex", "_nregex", "_is_null",
		"_gt", "_lt", "_gte", "_lte", "_like", "_nlike",
	}
}

func (p *PinotSaasSource) GetQuery(ctx *security.RequestContext, req FetchLogRequest) (string, error) {
	cfg, err := GetPinotConfig(ctx, req.AccountId)
	if err != nil {
		return "", fmt.Errorf("pinot.GetQuery: %w", err)
	}
	p.applyMergedLabelOverrides(ctx, req.AccountId, cfg)
	where, err := buildPinotWhereClause(req.QueryRequest.Where)
	if err != nil {
		return "", fmt.Errorf("pinot.GetQuery: %w", err)
	}
	limit := 1000
	if req.Limit > 0 {
		limit = req.Limit
	}
	// No schema fetch in GetQuery — display-only, use ms default scale
	return buildPinotSQL(cfg.Table, cfg.TimestampCol, where, req.StartTime, req.EndTime, pinotTsMode{ScaleFactor: 1}, limit, req.Offset, req.SortFields), nil
}

func (p *PinotSaasSource) QueryLogs(ctx *security.RequestContext, req FetchLogRequest) ([]OutputLog, error) {
	cfg, err := GetPinotConfig(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("pinot.QueryLogs: %w", err)
	}
	p.applyMergedLabelOverrides(ctx, req.AccountId, cfg)

	schema, _ := fetchPinotSchemaDirect(req.AccountId, cfg)
	cacheKey := "user:" + cfg.Url + ":" + cfg.Table
	detectedFmt, _ := getCachedPinotTsFormat(cacheKey, cfg.TimestampCol)
	tsMode := resolveTsMode(schema, cfg.TimestampCol, detectedFmt)

	if tsMode.IsString && tsMode.GoFormat == "" {
		if sample, sampleErr := samplePinotTimestampValueDirect(cfg, cfg.TimestampCol); sampleErr == nil {
			if d := pinotDetectTimestampFormatObs(sample); d != "" {
				setCachedPinotTsFormat(cacheKey, cfg.TimestampCol, d)
				tsMode.GoFormat = d
				detectedFmt = d
			}
		}
	}
	tsConv := getTimestampConverter(schema, cfg.TimestampCol, detectedFmt)

	var sqlQuery string
	if req.Query != "" {
		sqlQuery = req.Query
	} else {
		where, whereErr := buildPinotWhereClause(req.QueryRequest.Where)
		if whereErr != nil {
			return nil, fmt.Errorf("pinot.QueryLogs: %w", whereErr)
		}
		limit := 1000
		if req.Limit > 0 {
			limit = req.Limit
		}
		sqlQuery = buildPinotSQL(cfg.Table, cfg.TimestampCol, where, req.StartTime, req.EndTime, tsMode, limit, req.Offset, req.SortFields)
	}

	sqlBody, marshalErr := json.Marshal(map[string]string{"sql": sqlQuery})
	if marshalErr != nil {
		return nil, fmt.Errorf("pinot.QueryLogs: failed to marshal SQL: %w", marshalErr)
	}
	resp, err := pinotRequest("POST", fmt.Sprintf("%s/sql", cfg.Url), string(sqlBody), cfg)
	if err != nil {
		return nil, fmt.Errorf("pinot.QueryLogs: request failed: %w", err)
	}
	bodyBytes, err := readPinotResponse(resp, "query")
	if err != nil {
		return nil, err
	}
	return parsePinotResultTableBytes(bodyBytes, cfg.TimestampCol, cfg.MessageCol, cfg.SeverityCol, tsConv)
}

func (p *PinotSaasSource) QueryLabels(ctx *security.RequestContext, req FetchLogLabelRequest) ([]OutputLogLabel, error) {
	cfg, err := GetPinotConfig(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("pinot.QueryLabels: %w", err)
	}

	resp, err := pinotRequest("GET", fmt.Sprintf("%s/schemas/%s", cfg.Url, cfg.Table), "", cfg)
	if err != nil {
		return nil, fmt.Errorf("pinot.QueryLabels: request failed: %w", err)
	}
	bodyBytes, err := readPinotResponse(resp, "schema")
	if err != nil {
		return nil, err
	}
	schema, labels, err := parsePinotSchemaBytes(bodyBytes)
	if err != nil {
		return nil, err
	}
	if schema != nil {
		setPinotSchemaCache("user:"+cfg.Url+":"+cfg.Table, schema)
	}
	return labels, nil
}

func (p *PinotSaasSource) QueryLabelValues(ctx *security.RequestContext, req FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	col := req.LabelName
	if !pinotSafeColumnName(col) {
		return nil, fmt.Errorf("pinot.QueryLabelValues: invalid column name %q", col)
	}

	cfg, err := GetPinotConfig(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("pinot.QueryLabelValues: %w", err)
	}

	colQ := pinotQuoteIdent(col)
	sqlQuery := fmt.Sprintf("SELECT DISTINCT %s FROM %s WHERE %s IS NOT NULL LIMIT 100", colQ, pinotQuoteIdent(cfg.Table), colQ)
	sqlBody, marshalErr := json.Marshal(map[string]string{"sql": sqlQuery})
	if marshalErr != nil {
		return nil, fmt.Errorf("pinot.QueryLabelValues: failed to marshal SQL: %w", marshalErr)
	}
	resp, err := pinotRequest("POST", fmt.Sprintf("%s/sql", cfg.Url), string(sqlBody), cfg)
	if err != nil {
		return nil, fmt.Errorf("pinot.QueryLabelValues: request failed: %w", err)
	}
	bodyBytes, err := readPinotResponse(resp, "label values query")
	if err != nil {
		return nil, err
	}
	return parsePinotLabelValuesBytes(bodyBytes)
}

// QueryLogGroup implements LogGroupSource for the direct-mode Pinot integration.
// Push aggregation down to Pinot via GROUP BY — see PinotSource.QueryLogGroup for rationale.
func (p *PinotSaasSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	cfg, err := GetPinotConfig(ctx, req.AccountId)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("pinot.QueryLogGroup: %w", err)
	}
	p.applyMergedLabelOverrides(ctx, req.AccountId, cfg)

	cols := pinotLogGroupCols{
		Table:        cfg.Table,
		TsCol:        cfg.TimestampCol,
		MsgCol:       cfg.MessageCol,
		SevCol:       cfg.SeverityCol,
		NsCol:        cfg.NamespaceCol,
		PodCol:       cfg.PodCol,
		ContainerCol: cfg.ContainerCol,
	}

	schema, _ := fetchPinotSchemaDirect(req.AccountId, cfg)
	cacheKey := "user:" + cfg.Url + ":" + cfg.Table
	detectedFmt, _ := getCachedPinotTsFormat(cacheKey, cols.TsCol)
	tsMode := resolveTsMode(schema, cols.TsCol, detectedFmt)

	// Sample-on-miss for STRING timestamp columns — mirrors QueryLogs.
	if tsMode.IsString && tsMode.GoFormat == "" {
		if sample, sampleErr := samplePinotTimestampValueDirect(cfg, cols.TsCol); sampleErr == nil {
			if d := pinotDetectTimestampFormatObs(sample); d != "" {
				setCachedPinotTsFormat(cacheKey, cols.TsCol, d)
				tsMode.GoFormat = d
			}
		}
	}

	selectedNs := common.GetString(req.Request, "selectedNamespace")
	selectedWorkload := common.GetString(req.Request, "selectedWorkload")
	sqlQuery := buildPinotLogGroupSQL(cols, tsMode, req.StartTime, req.EndTime, selectedNs, selectedWorkload, 100)

	sqlBody, marshalErr := json.Marshal(map[string]string{"sql": sqlQuery})
	if marshalErr != nil {
		return LogGroupOutput{}, fmt.Errorf("pinot.QueryLogGroup: failed to marshal SQL: %w", marshalErr)
	}
	resp, err := pinotRequest("POST", fmt.Sprintf("%s/sql", cfg.Url), string(sqlBody), cfg)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("pinot.QueryLogGroup: request failed: %w", err)
	}
	bodyBytes, err := readPinotResponse(resp, "log-group query")
	if err != nil {
		return LogGroupOutput{}, err
	}
	return parsePinotLogGroupBytes(bodyBytes, cols, tsMode, req.EndTime)
}

// samplePinotTimestampValueDirect fetches one non-null timestamp value via direct
// HTTP, used to auto-detect a STRING-typed column's Go time layout.
func samplePinotTimestampValueDirect(cfg *PinotConfig, tsCol string) (string, error) {
	if !pinotSafeColumnName(tsCol) {
		return "", fmt.Errorf("invalid timestamp column %q", tsCol)
	}
	tsColQ := pinotQuoteIdent(tsCol)
	sqlQuery := fmt.Sprintf("SELECT %s FROM %s WHERE %s IS NOT NULL LIMIT 1", tsColQ, pinotQuoteIdent(cfg.Table), tsColQ)
	body, err := json.Marshal(map[string]string{"sql": sqlQuery})
	if err != nil {
		return "", fmt.Errorf("marshal sample sql: %w", err)
	}
	resp, err := pinotRequest("POST", fmt.Sprintf("%s/sql", cfg.Url), string(body), cfg)
	if err != nil {
		return "", fmt.Errorf("sample request failed: %w", err)
	}
	bodyBytes, err := readPinotResponse(resp, "sample query")
	if err != nil {
		return "", err
	}
	var r pinotQueryResponse
	if err := json.Unmarshal(bodyBytes, &r); err != nil {
		return "", fmt.Errorf("parse sample response: %w", err)
	}
	if len(r.ResultTable.Rows) == 0 || len(r.ResultTable.Rows[0]) == 0 {
		return "", fmt.Errorf("no sample rows found")
	}
	s, ok := r.ResultTable.Rows[0][0].(string)
	if !ok {
		return "", fmt.Errorf("sample value is not a string (got %T)", r.ResultTable.Rows[0][0])
	}
	return s, nil
}
