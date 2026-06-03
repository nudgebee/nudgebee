package observability

import (
	"context"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/integrations/hiveclient"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"

	"github.com/beltran/gohive"
)

const (
	HiveUrl          = "hive_url"
	HiveDatabase     = "hive_database"
	HiveAuthType     = "auth_type"
	HiveUsername     = "username"
	HivePassword     = "password"
	HiveTable        = "hive_table"
	HiveTimestampCol = "hive_timestamp_col"
	HiveMessageCol   = "hive_message_col"
	HiveSeverityCol  = "hive_severity_col"
	HiveNamespaceCol = "hive_namespace_col"
	HivePodCol       = "hive_pod_col"
	HiveContainerCol = "hive_container_col"
	HiveTLSSkipVer   = "hive_tls_skip_verify"
)

// HiveConfig holds the resolved configuration for a direct Hive integration.
type HiveConfig struct {
	Host          string
	Port          int
	UseTLS        bool
	TLSSkipVerify bool
	Database      string
	AuthType      string // "none" | "ldap"
	Username      string
	Password      string
	Table         string
	TimestampCol  string
	MessageCol    string
	SeverityCol   string
	NamespaceCol  string
	PodCol        string
	ContainerCol  string
}

// GetHiveConfig reads and decrypts the Hive integration configuration.
func GetHiveConfig(ctx *security.RequestContext, accountId string) (*HiveConfig, error) {
	dtos, err := core.ListIntegrationConfigs(ctx, accountId, "hive")
	if err != nil {
		return nil, fmt.Errorf("failed to get hive integration: %w", err)
	}

	var userDtos []core.IntegrationDto
	for _, dto := range dtos {
		if dto.Source == "user" {
			userDtos = append(userDtos, dto)
		}
	}
	if len(userDtos) == 0 {
		return nil, fmt.Errorf("no hive integration configured for account %s", accountId)
	}

	cfg := &HiveConfig{
		AuthType:     "none",
		Database:     "default",
		TimestampCol: "time_ms",
		MessageCol:   "log",
		NamespaceCol: "kubernetes.namespace_name",
		PodCol:       "kubernetes.pod_name",
		ContainerCol: "kubernetes.container_name",
	}

	var rawURL string
	for _, c := range userDtos[0].Configs {
		value := c.Value
		if c.IsEncrypted && value != "" {
			decrypted, decErr := common.Decrypt(value)
			if decErr != nil {
				return nil, fmt.Errorf("failed to decrypt hive config %s: %w", c.Name, decErr)
			}
			value = decrypted
		}
		switch c.Name {
		case HiveUrl:
			rawURL = value
		case HiveDatabase:
			cfg.Database = value
		case HiveAuthType:
			cfg.AuthType = value
		case HiveUsername:
			cfg.Username = value
		case HivePassword:
			cfg.Password = value
		case HiveTable:
			cfg.Table = value
		case HiveTimestampCol:
			cfg.TimestampCol = value
		case HiveMessageCol:
			cfg.MessageCol = value
		case HiveSeverityCol:
			cfg.SeverityCol = value
		case HiveNamespaceCol:
			cfg.NamespaceCol = value
		case HivePodCol:
			cfg.PodCol = value
		case HiveContainerCol:
			cfg.ContainerCol = value
		case HiveTLSSkipVer:
			cfg.TLSSkipVerify = strings.EqualFold(strings.TrimSpace(value), "true")
		}
	}

	host, port, useTLS, err := parseHiveHostPort(rawURL)
	if err != nil {
		return nil, err
	}
	cfg.Host = host
	cfg.Port = port
	cfg.UseTLS = useTLS

	if cfg.Table == "" {
		return nil, fmt.Errorf("hive integration is missing hive_table")
	}
	if cfg.TimestampCol == "" {
		return nil, fmt.Errorf("hive integration is missing hive_timestamp_col")
	}
	if cfg.MessageCol == "" {
		return nil, fmt.Errorf("hive integration is missing hive_message_col")
	}
	if cfg.AuthType == "" {
		cfg.AuthType = "none"
	}
	if cfg.Database == "" {
		cfg.Database = "default"
	}
	return cfg, nil
}

// parseHiveHostPort delegates to hiveclient. Kept as a package-local symbol
// because tests reference it; remove the wrapper once tests migrate.
func parseHiveHostPort(raw string) (string, int, bool, error) {
	return hiveclient.ParseHostPort(raw)
}

// hiveConnect opens a HiveServer2 Thrift connection per the supplied config.
// Caller is responsible for closing the returned Connection.
func hiveConnect(cfg *HiveConfig) (*gohive.Connection, error) {
	return hiveclient.Connect(hiveclient.ConnectConfig{
		Host:          cfg.Host,
		Port:          cfg.Port,
		Auth:          cfg.AuthType,
		Username:      cfg.Username,
		Password:      cfg.Password,
		Database:      cfg.Database,
		UseTLS:        cfg.UseTLS,
		TLSSkipVerify: cfg.TLSSkipVerify,
	})
}

// runHiveQuery executes a HiveQL statement and returns the result set as a
// hiveQueryResponse so the existing parsers consume it unchanged.
func runHiveQuery(cfg *HiveConfig, sqlQuery string) (*hiveQueryResponse, error) {
	conn, err := hiveConnect(cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	r, err := hiveclient.RunQuery(ctx, conn, sqlQuery)
	if err != nil {
		return nil, err
	}
	return &hiveQueryResponse{Columns: r.Columns, Rows: r.Rows}, nil
}

// fetchHiveSchemaDirect runs DESCRIBE via hiveclient and caches the result
// keyed by host+db+table. The partition flag is set by hiveclient.DescribeColumns.
func fetchHiveSchemaDirect(cfg *HiveConfig) (*hiveSchemaResponse, error) {
	cacheKey := fmt.Sprintf("user:%s:%d:%s.%s", cfg.Host, cfg.Port, cfg.Database, cfg.Table)
	if cached, ok := getCachedHiveSchema(cacheKey); ok {
		return cached, nil
	}

	conn, err := hiveConnect(cfg)
	if err != nil {
		return nil, fmt.Errorf("fetchHiveSchemaDirect: %w", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cols, err := hiveclient.DescribeColumns(ctx, conn, cfg.Database, cfg.Table)
	if err != nil {
		return nil, fmt.Errorf("fetchHiveSchemaDirect: %w", err)
	}

	schema := &hiveSchemaResponse{Columns: make([]hiveColumnSpec, len(cols))}
	for i, c := range cols {
		schema.Columns[i] = hiveColumnSpec{Name: c.Name, Type: c.Type, IsPartition: c.IsPartition}
	}

	setHiveSchemaCache(cacheKey, schema)
	return schema, nil
}

// sampleHiveTimestampValueDirect fetches one non-null timestamp via direct
// Thrift, used to auto-detect a STRING-typed column's Go time layout.
func sampleHiveTimestampValueDirect(cfg *HiveConfig, tsCol string) (string, error) {
	if !hiveSafeColumnRef(tsCol) {
		return "", fmt.Errorf("invalid timestamp column %q", tsCol)
	}
	tsColQ := hiveQuoteIdent(tsCol)
	sqlQuery := fmt.Sprintf("SELECT %s FROM %s WHERE %s IS NOT NULL LIMIT 1",
		tsColQ, hiveQualifiedTable(cfg.Database, cfg.Table), tsColQ)
	r, err := runHiveQuery(cfg, sqlQuery)
	if err != nil {
		return "", err
	}
	if len(r.Rows) == 0 || len(r.Rows[0]) == 0 {
		return "", fmt.Errorf("no sample rows found")
	}
	s, ok := r.Rows[0][0].(string)
	if !ok {
		return "", fmt.Errorf("sample value is not a string (got %T)", r.Rows[0][0])
	}
	return s, nil
}

// HiveSaasSource implements LogSource via direct HiveServer2 Thrift connections.
type HiveSaasSource struct{}

func (h *HiveSaasSource) GetLabelMapping() map[string]string {
	return map[string]string{}
}

// GetDynamicLabelMapping exposes the integration-configured column names so
// getMergedLabelMapping can layer them on top of tenant/account overrides.
func (h *HiveSaasSource) GetDynamicLabelMapping(ctx *security.RequestContext, accountId string) map[string]string {
	cfg, err := GetHiveConfig(ctx, accountId)
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

func (h *HiveSaasSource) applyMergedLabelOverrides(ctx *security.RequestContext, accountId string, cfg *HiveConfig) {
	merged := getMergedLabelMapping(ctx, accountId, h)
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

func (h *HiveSaasSource) GetSupportedOperators() []string {
	return []string{
		"_eq", "_neq", "_contains",
		"_regex", "_nregex", "_is_null",
		"_gt", "_lt", "_gte", "_lte", "_like", "_nlike",
	}
}

func (h *HiveSaasSource) GetQuery(ctx *security.RequestContext, req FetchLogRequest) (string, error) {
	cfg, err := GetHiveConfig(ctx, req.AccountId)
	if err != nil {
		return "", fmt.Errorf("hive.GetQuery: %w", err)
	}
	h.applyMergedLabelOverrides(ctx, req.AccountId, cfg)
	where, err := buildHiveWhereClause(req.QueryRequest.Where)
	if err != nil {
		return "", fmt.Errorf("hive.GetQuery: %w", err)
	}
	limit := 1000
	if req.Limit > 0 {
		limit = req.Limit
	}
	return buildHiveSQL(cfg.Database, cfg.Table, cfg.TimestampCol, where, req.StartTime, req.EndTime, hiveTsMode{ScaleFactor: 1}, limit, req.SortFields), nil
}

func (h *HiveSaasSource) QueryLogs(ctx *security.RequestContext, req FetchLogRequest) ([]OutputLog, error) {
	cfg, err := GetHiveConfig(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("hive.QueryLogs: %w", err)
	}
	h.applyMergedLabelOverrides(ctx, req.AccountId, cfg)

	schema, _ := fetchHiveSchemaDirect(cfg)
	cacheKey := fmt.Sprintf("user:%s:%d:%s.%s", cfg.Host, cfg.Port, cfg.Database, cfg.Table)
	detectedFmt, _ := getCachedHiveTsFormat(cacheKey, cfg.TimestampCol)
	tsMode := resolveHiveTsMode(schema, cfg.TimestampCol, detectedFmt)

	if tsMode.IsString && tsMode.GoFormat == "" {
		if sample, sampleErr := sampleHiveTimestampValueDirect(cfg, cfg.TimestampCol); sampleErr == nil {
			if d := hiveDetectTimestampFormatObs(sample); d != "" {
				setCachedHiveTsFormat(cacheKey, cfg.TimestampCol, d)
				tsMode.GoFormat = d
				detectedFmt = d
			}
		}
	}
	tsConv := getHiveTimestampConverter(schema, cfg.TimestampCol, detectedFmt)

	limit := 1000
	if req.Limit > 0 {
		limit = req.Limit
	}
	var sqlQuery string
	if req.Query != "" {
		sqlQuery = req.Query
	} else {
		where, whereErr := buildHiveWhereClause(req.QueryRequest.Where)
		if whereErr != nil {
			return nil, fmt.Errorf("hive.QueryLogs: %w", whereErr)
		}
		sqlQuery = buildHiveSQL(cfg.Database, cfg.Table, cfg.TimestampCol, where, req.StartTime, req.EndTime, tsMode, limit, req.SortFields)
	}

	r, err := runHiveQuery(cfg, sqlQuery)
	if err != nil {
		return nil, fmt.Errorf("hive.QueryLogs: %w", err)
	}
	return parseHiveResult(r, cfg.TimestampCol, cfg.MessageCol, cfg.SeverityCol, tsConv, limit), nil
}

func (h *HiveSaasSource) QueryLabels(ctx *security.RequestContext, req FetchLogLabelRequest) ([]OutputLogLabel, error) {
	cfg, err := GetHiveConfig(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("hive.QueryLabels: %w", err)
	}
	schema, err := fetchHiveSchemaDirect(cfg)
	if err != nil {
		return nil, err
	}
	labels := make([]OutputLogLabel, 0, len(schema.Columns))
	for _, c := range schema.Columns {
		attrs := map[string]any{"dataType": c.Type}
		if c.IsPartition {
			attrs["isPartition"] = true
		}
		labels = append(labels, OutputLogLabel{
			Label:      c.Name,
			Attributes: attrs,
		})
	}
	return labels, nil
}

func (h *HiveSaasSource) QueryLabelValues(ctx *security.RequestContext, req FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	col := req.LabelName
	if !hiveSafeColumnRef(col) {
		return nil, fmt.Errorf("hive.QueryLabelValues: invalid column name %q", col)
	}
	cfg, err := GetHiveConfig(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("hive.QueryLabelValues: %w", err)
	}
	colQ := hiveQuoteIdent(col)
	sqlQuery := fmt.Sprintf("SELECT DISTINCT %s FROM %s WHERE %s IS NOT NULL LIMIT 100",
		colQ, hiveQualifiedTable(cfg.Database, cfg.Table), colQ)
	r, err := runHiveQuery(cfg, sqlQuery)
	if err != nil {
		return nil, fmt.Errorf("hive.QueryLabelValues: %w", err)
	}
	vals := make([]OutputLogLabelValue, 0, len(r.Rows))
	for _, row := range r.Rows {
		if len(row) == 0 {
			continue
		}
		v := fmt.Sprintf("%v", row[0])
		if v == "" || v == "<nil>" {
			continue
		}
		vals = append(vals, OutputLogLabelValue{Value: v, Attributes: map[string]any{}})
	}
	return vals, nil
}

// QueryLogGroup implements LogGroupSource for the direct-mode Hive integration.
func (h *HiveSaasSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	cfg, err := GetHiveConfig(ctx, req.AccountId)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("hive.QueryLogGroup: %w", err)
	}
	h.applyMergedLabelOverrides(ctx, req.AccountId, cfg)

	cols := hiveLogGroupCols{
		Database:     cfg.Database,
		Table:        cfg.Table,
		TsCol:        cfg.TimestampCol,
		MsgCol:       cfg.MessageCol,
		SevCol:       cfg.SeverityCol,
		NsCol:        cfg.NamespaceCol,
		PodCol:       cfg.PodCol,
		ContainerCol: cfg.ContainerCol,
	}

	schema, _ := fetchHiveSchemaDirect(cfg)
	cacheKey := fmt.Sprintf("user:%s:%d:%s.%s", cfg.Host, cfg.Port, cfg.Database, cfg.Table)
	detectedFmt, _ := getCachedHiveTsFormat(cacheKey, cols.TsCol)
	tsMode := resolveHiveTsMode(schema, cols.TsCol, detectedFmt)

	if tsMode.IsString && tsMode.GoFormat == "" {
		if sample, sampleErr := sampleHiveTimestampValueDirect(cfg, cols.TsCol); sampleErr == nil {
			if d := hiveDetectTimestampFormatObs(sample); d != "" {
				setCachedHiveTsFormat(cacheKey, cols.TsCol, d)
				tsMode.GoFormat = d
			}
		}
	}

	selectedNs := common.GetString(req.Request, "selectedNamespace")
	selectedWorkload := common.GetString(req.Request, "selectedWorkload")
	sqlQuery := buildHiveLogGroupSQL(cols, tsMode, req.StartTime, req.EndTime, selectedNs, selectedWorkload, 100)

	r, err := runHiveQuery(cfg, sqlQuery)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("hive.QueryLogGroup: %w", err)
	}
	// Reuse the relay-mode parser by re-marshalling the response.
	// parseHiveLogGroupBytes expects the same JSON shape we already have.
	return parseHiveLogGroupFromStruct(r, cols, req.EndTime), nil
}

// parseHiveResult is a thin wrapper around parseHiveResultBytes that takes the
// in-memory struct, avoiding a JSON marshal/unmarshal round-trip for direct mode.
func parseHiveResult(r *hiveQueryResponse, tsCol, msgCol, sevCol string, tsConv func(any) string, limit int) []OutputLog {
	if r == nil {
		return nil
	}
	colIdx := make(map[string]int, len(r.Columns))
	for i, c := range r.Columns {
		colIdx[c] = i
		if dot := strings.LastIndex(c, "."); dot >= 0 && dot+1 < len(c) {
			if _, exists := colIdx[c[dot+1:]]; !exists {
				colIdx[c[dot+1:]] = i
			}
		}
	}
	lookup := func(name string) (int, bool) {
		if i, ok := colIdx[name]; ok {
			return i, true
		}
		if dot := strings.LastIndex(name, "."); dot >= 0 && dot+1 < len(name) {
			if i, ok := colIdx[name[dot+1:]]; ok {
				return i, true
			}
		}
		return -1, false
	}
	tsIdx, hasTs := lookup(tsCol)
	msgIdx, hasMsg := lookup(msgCol)
	sevIdx := -1
	if sevCol != "" {
		if i, ok := lookup(sevCol); ok {
			sevIdx = i
		}
	}

	logs := make([]OutputLog, 0, len(r.Rows))
	for _, row := range r.Rows {
		ts := ""
		if hasTs && tsIdx < len(row) && tsConv != nil {
			ts = tsConv(row[tsIdx])
		}
		msg := ""
		if hasMsg && msgIdx < len(row) {
			msg = fmt.Sprintf("%v", row[msgIdx])
		}
		sev := ""
		if sevIdx >= 0 && sevIdx < len(row) {
			sev = fmt.Sprintf("%v", row[sevIdx])
		}
		if sev == "" {
			sev = GetSeverityLevels(msg)
		}
		labels := make(map[string]any, len(r.Columns))
		for i, c := range r.Columns {
			if c == tsCol || c == msgCol || (sevCol != "" && c == sevCol) {
				continue
			}
			if i < len(row) {
				labels[c] = row[i]
			}
		}
		logs = append(logs, OutputLog{Timestamp: ts, Message: msg, Severity: sev, Labels: labels})
	}
	sortLogsByTimestampDesc(logs)
	if limit > 0 && len(logs) > limit {
		logs = logs[:limit]
	}
	return logs
}

// parseHiveLogGroupFromStruct mirrors parseHiveLogGroupBytes but takes the
// in-memory hiveQueryResponse — avoids a round-trip through JSON for direct mode.
func parseHiveLogGroupFromStruct(r *hiveQueryResponse, cols hiveLogGroupCols, endTime int64) LogGroupOutput {
	if r == nil {
		return LogGroupOutput{}
	}
	colIdx := make(map[string]int, len(r.Columns))
	for i, c := range r.Columns {
		colIdx[c] = i
		if dot := strings.LastIndex(c, "."); dot >= 0 && dot+1 < len(c) {
			if _, exists := colIdx[c[dot+1:]]; !exists {
				colIdx[c[dot+1:]] = i
			}
		}
	}
	lookup := func(name string) (int, bool) {
		if i, ok := colIdx[name]; ok {
			return i, true
		}
		if dot := strings.LastIndex(name, "."); dot >= 0 && dot+1 < len(name) {
			if i, ok := colIdx[name[dot+1:]]; ok {
				return i, true
			}
		}
		return -1, false
	}
	idxMsg, hasMsg := lookup(cols.MsgCol)
	idxNs, hasNs := lookup(cols.NsCol)
	idxPod, hasPod := lookup(cols.PodCol)
	idxContainer, hasContainer := lookup(cols.ContainerCol)
	idxSev := -1
	if cols.SevCol != "" {
		if i, ok := lookup(cols.SevCol); ok {
			idxSev = i
		}
	}
	idxCnt, hasCnt := lookup("cnt")

	var endTimeSec int64
	switch {
	case endTime <= 0:
		endTimeSec = time.Now().Unix()
	case endTime >= 1e12:
		endTimeSec = endTime / 1000
	default:
		endTimeSec = endTime
	}

	groups := make([]LogGroup, 0, len(r.Rows))
	for _, row := range r.Rows {
		var message, namespace, pod, container, level string
		if hasMsg && idxMsg < len(row) {
			message = fmt.Sprintf("%v", row[idxMsg])
		}
		if hasNs && idxNs < len(row) {
			namespace = fmt.Sprintf("%v", row[idxNs])
		}
		if hasPod && idxPod < len(row) {
			pod = fmt.Sprintf("%v", row[idxPod])
		}
		if hasContainer && idxContainer < len(row) {
			container = fmt.Sprintf("%v", row[idxContainer])
		}
		if idxSev >= 0 && idxSev < len(row) {
			level = strings.ToLower(fmt.Sprintf("%v", row[idxSev]))
		}
		if level == "" {
			level = "error"
		}
		var count int64
		if hasCnt && idxCnt < len(row) {
			switch v := row[idxCnt].(type) {
			case float64:
				count = int64(v)
			case int64:
				count = v
			case int:
				count = int64(v)
			case string:
				if n, err := strconv.ParseInt(v, 10, 64); err == nil {
					count = n
				}
			}
		}
		sample := message
		if runes := []rune(sample); len(runes) > 500 {
			sample = string(runes[:500])
		}
		workload := extractWorkloadFromPodName(pod)
		containerID := ""
		if namespace != "" && workload != "" {
			containerID = fmt.Sprintf("/k8s/%s/%s", namespace, workload)
		}
		groups = append(groups, LogGroup{
			Sample:      sample,
			Namespace:   namespace,
			Workload:    workload,
			Container:   container,
			ContainerID: containerID,
			PatternHash: generatePatternHash(message),
			Level:       level,
			Count:       count,
			Timestamps:  []int64{endTimeSec},
			Values:      []float64{float64(count)},
		})
	}
	return LogGroupOutput{Groups: groups}
}
