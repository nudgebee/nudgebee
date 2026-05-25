package observability

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/query"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---- Shared types ----

// pinotTsMode carries the timestamp handling strategy for a Pinot column.
type pinotTsMode struct {
	IsString    bool   // true → formatted string; false → numeric epoch
	ScaleFactor int64  // for numeric: divisor converting epoch-ms → column unit (1=ms, 1000=s, 3600000=h)
	GoFormat    string // for string: Go time layout (e.g. "2006-01-02T15:04:05Z07:00")
}

type pinotFieldSpec struct {
	Name     string `json:"name"`
	DataType string `json:"dataType"`
}

type pinotDateTimeFieldSpec struct {
	Name     string `json:"name"`
	DataType string `json:"dataType"`
	Format   string `json:"format"` // e.g. "1:MILLISECONDS:EPOCH" or "1:DAYS:SIMPLE_DATE_FORMAT:yyyy-MM-dd"
}

// pinotSchemaResponse matches the Pinot GET /schemas/{table} response body.
type pinotSchemaResponse struct {
	SchemaName          string                   `json:"schemaName"`
	DimensionFieldSpecs []pinotFieldSpec         `json:"dimensionFieldSpecs"`
	MetricFieldSpecs    []pinotFieldSpec         `json:"metricFieldSpecs"`
	DateTimeFieldSpecs  []pinotDateTimeFieldSpec `json:"dateTimeFieldSpecs"`
}

// pinotQueryResponse matches the Pinot POST /query/sql response body.
type pinotQueryResponse struct {
	ResultTable struct {
		DataSchema struct {
			ColumnNames     []string `json:"columnNames"`
			ColumnDataTypes []string `json:"columnDataTypes"`
		} `json:"dataSchema"`
		Rows [][]any `json:"rows"`
	} `json:"resultTable"`
	Exceptions []map[string]any `json:"exceptions"`
}

// ---- Schema cache ----

type pinotSchemaCacheEntry struct {
	schema            *pinotSchemaResponse
	detectedTsFormats map[string]string // col → Go time layout, populated lazily by QueryLogs
	fetchedAt         time.Time
}

var pinotSchemaCache = struct {
	sync.Mutex
	m map[string]pinotSchemaCacheEntry
}{m: make(map[string]pinotSchemaCacheEntry)}

const pinotSchemaCacheTTL = 5 * time.Minute

func getCachedPinotSchema(key string) (*pinotSchemaResponse, bool) {
	pinotSchemaCache.Lock()
	defer pinotSchemaCache.Unlock()
	e, ok := pinotSchemaCache.m[key]
	if !ok || time.Since(e.fetchedAt) > pinotSchemaCacheTTL {
		return nil, false
	}
	return e.schema, true
}

// setPinotSchemaCache refreshes the schema but preserves any previously-detected
// timestamp formats so the next QueryLogs doesn't have to re-sample.
func setPinotSchemaCache(key string, schema *pinotSchemaResponse) {
	pinotSchemaCache.Lock()
	defer pinotSchemaCache.Unlock()
	prev := pinotSchemaCache.m[key].detectedTsFormats
	pinotSchemaCache.m[key] = pinotSchemaCacheEntry{
		schema:            schema,
		detectedTsFormats: prev,
		fetchedAt:         time.Now(),
	}
}

func getCachedPinotTsFormat(key, col string) (string, bool) {
	pinotSchemaCache.Lock()
	defer pinotSchemaCache.Unlock()
	e, ok := pinotSchemaCache.m[key]
	if !ok || e.detectedTsFormats == nil {
		return "", false
	}
	v, ok := e.detectedTsFormats[col]
	return v, ok && v != ""
}

func setCachedPinotTsFormat(key, col, format string) {
	pinotSchemaCache.Lock()
	defer pinotSchemaCache.Unlock()
	e := pinotSchemaCache.m[key]
	if e.detectedTsFormats == nil {
		e.detectedTsFormats = map[string]string{}
	}
	e.detectedTsFormats[col] = format
	if e.fetchedAt.IsZero() {
		e.fetchedAt = time.Now()
	}
	pinotSchemaCache.m[key] = e
}

// ---- Timestamp auto-detection (sample-based) ----

// pinotObsKnownTimestampLayouts is the list of Go time layouts the observability
// package tries against a sample value to detect a STRING column's format.
// Mirrors integrations.pinotValidateKnownLayouts.
var pinotObsKnownTimestampLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.000000Z",
	"2006-01-02T15:04:05.000Z",
	time.RFC3339,
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.999999",
	"2006-01-02 15:04:05.000",
	"2006-01-02 15:04:05",
	"2006-01-02",
	"01/02/2006 15:04:05",
	"02/01/2006 15:04:05",
	"02-Jan-2006 15:04:05",
	"02-Jan-2006",
	time.RFC1123,
	time.RFC1123Z,
	time.RFC822,
	time.RFC822Z,
}

// pinotDetectTimestampFormatObs returns the first known layout that parses sample,
// or "" if no layout matches.
func pinotDetectTimestampFormatObs(sample string) string {
	s := strings.TrimSpace(sample)
	if s == "" {
		return ""
	}
	for _, layout := range pinotObsKnownTimestampLayouts {
		if _, err := time.Parse(layout, s); err == nil {
			return layout
		}
	}
	return ""
}

// samplePinotTimestampValueRelay fetches one non-null timestamp value via the
// relay's pinot_query action. Returns the string value or an error.
func samplePinotTimestampValueRelay(accountId, table, tsCol string) (string, error) {
	if !pinotSafeColumnName(tsCol) {
		return "", fmt.Errorf("invalid timestamp column %q", tsCol)
	}
	tsColQ := pinotQuoteIdent(tsCol)
	sqlQuery := fmt.Sprintf("SELECT %s FROM %s WHERE %s IS NOT NULL LIMIT 1", tsColQ, pinotQuoteIdent(table), tsColQ)
	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:    accountId,
			ActionName:   "pinot_query",
			ActionParams: map[string]any{"sql": sqlQuery},
			NoSinks:      true,
		},
	})
	if err != nil {
		return "", fmt.Errorf("relay sample query failed: %w", err)
	}
	rawBytes, err := extractPinotRelayData(resp)
	if err != nil {
		return "", err
	}
	var r pinotQueryResponse
	if err := json.Unmarshal(rawBytes, &r); err != nil {
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

// ---- PinotSource — relay/agent mode ----

// PinotSource implements LogSource by routing all queries through the relay agent.
// The agent must have the actions pinot_query, pinot_schema registered (nudgebee-agent PR #47).
type PinotSource struct{}

func (p *PinotSource) GetLabelMapping() map[string]string {
	return map[string]string{}
}

func (p *PinotSource) GetSupportedOperators() []string {
	return []string{
		"_eq", "_neq", "_contains", "_in", "_not_in",
		"_regex", "_nregex", "_is_null",
		"_gt", "_lt", "_gte", "_lte", "_like", "_nlike",
	}
}

func (p *PinotSource) GetQuery(ctx *security.RequestContext, req FetchLogRequest) (string, error) {
	table := pinotStringParam(req.Request, "pinot_table", "")
	tsCol := pinotStringParam(req.Request, "pinot_timestamp_col", "ts")
	where, err := buildPinotWhereClause(req.QueryRequest.Where)
	if err != nil {
		return "", fmt.Errorf("pinot.GetQuery: %w", err)
	}
	limit := 1000
	if req.Limit > 0 {
		limit = req.Limit
	}
	// No schema fetch in GetQuery — display-only, use ms default scale
	return buildPinotSQL(table, tsCol, where, req.StartTime, req.EndTime, pinotTsMode{ScaleFactor: 1}, limit, req.Offset, req.SortFields), nil
}

func (p *PinotSource) QueryLogs(ctx *security.RequestContext, req FetchLogRequest) ([]OutputLog, error) {
	table := pinotStringParam(req.Request, "pinot_table", "")
	tsCol := pinotStringParam(req.Request, "pinot_timestamp_col", "ts")
	msgCol := pinotStringParam(req.Request, "pinot_message_col", "log")
	sevCol := pinotStringParam(req.Request, "pinot_severity_col", "")

	schema, _ := fetchPinotSchema(ctx, req.AccountId, table)
	cacheKey := "agent:" + req.AccountId + ":" + table
	detectedFmt, _ := getCachedPinotTsFormat(cacheKey, tsCol)
	tsMode := resolveTsMode(schema, tsCol, detectedFmt)

	if tsMode.IsString && tsMode.GoFormat == "" {
		if sample, sampleErr := samplePinotTimestampValueRelay(req.AccountId, table, tsCol); sampleErr == nil {
			if d := pinotDetectTimestampFormatObs(sample); d != "" {
				setCachedPinotTsFormat(cacheKey, tsCol, d)
				tsMode.GoFormat = d
				detectedFmt = d
			}
		}
	}
	tsConv := getTimestampConverter(schema, tsCol, detectedFmt)

	var sqlQuery string
	if req.Query != "" {
		sqlQuery = req.Query
	} else {
		where, err := buildPinotWhereClause(req.QueryRequest.Where)
		if err != nil {
			return nil, fmt.Errorf("pinot.QueryLogs: %w", err)
		}
		limit := 1000
		if req.Limit > 0 {
			limit = req.Limit
		}
		sqlQuery = buildPinotSQL(table, tsCol, where, req.StartTime, req.EndTime, tsMode, limit, req.Offset, req.SortFields)
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:    req.AccountId,
			ActionName:   "pinot_query",
			ActionParams: map[string]any{"sql": sqlQuery},
			NoSinks:      true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("pinot.QueryLogs: relay execute failed: %w", err)
	}

	rawBytes, err := extractPinotRelayData(resp)
	if err != nil {
		return nil, fmt.Errorf("pinot.QueryLogs: %w", err)
	}
	return parsePinotResultTableBytes(rawBytes, tsCol, msgCol, sevCol, tsConv)
}

func (p *PinotSource) QueryLabels(ctx *security.RequestContext, req FetchLogLabelRequest) ([]OutputLogLabel, error) {
	table := pinotStringParam(req.Request, "pinot_table", "")

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:    req.AccountId,
			ActionName:   "pinot_schema",
			ActionParams: map[string]any{"table": table},
			NoSinks:      true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("pinot.QueryLabels: relay execute failed: %w", err)
	}

	rawBytes, err := extractPinotRelayData(resp)
	if err != nil {
		return nil, fmt.Errorf("pinot.QueryLabels: %w", err)
	}
	schema, labels, err := parsePinotSchemaBytes(rawBytes)
	if err != nil {
		return nil, err
	}
	if schema != nil {
		setPinotSchemaCache("agent:"+req.AccountId+":"+table, schema)
	}
	return labels, nil
}

func (p *PinotSource) QueryLabelValues(ctx *security.RequestContext, req FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	col := req.LabelName
	if !pinotSafeColumnName(col) {
		return nil, fmt.Errorf("pinot.QueryLabelValues: invalid column name %q", col)
	}
	table := pinotStringParam(req.Request, "pinot_table", "")
	colQ := pinotQuoteIdent(col)
	sqlQuery := fmt.Sprintf("SELECT DISTINCT %s FROM %s WHERE %s IS NOT NULL LIMIT 100", colQ, pinotQuoteIdent(table), colQ)

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:    req.AccountId,
			ActionName:   "pinot_query",
			ActionParams: map[string]any{"sql": sqlQuery},
			NoSinks:      true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("pinot.QueryLabelValues: relay execute failed: %w", err)
	}

	rawBytes, err := extractPinotRelayData(resp)
	if err != nil {
		return nil, fmt.Errorf("pinot.QueryLabelValues: %w", err)
	}
	return parsePinotLabelValuesBytes(rawBytes)
}

// ---- Log groups (shared helpers + relay-mode implementation) ----

// pinotLogGroupCols carries the column names needed to build & parse a log-group query.
type pinotLogGroupCols struct {
	Table, TsCol, MsgCol, SevCol, NsCol, PodCol, ContainerCol string
}

// resolvePinotLogGroupColsFromRequest pulls column names from the relay request map,
// falling back to the standard k8s_logs conventions (per nudgebee-agent PR #47).
func resolvePinotLogGroupColsFromRequest(request map[string]any) pinotLogGroupCols {
	return pinotLogGroupCols{
		Table:        pinotStringParam(request, "pinot_table", ""),
		TsCol:        pinotStringParam(request, "pinot_timestamp_col", "ts"),
		MsgCol:       pinotStringParam(request, "pinot_message_col", "log"),
		SevCol:       pinotStringParam(request, "pinot_severity_col", ""),
		NsCol:        pinotStringParam(request, "pinot_namespace_col", "namespace"),
		PodCol:       pinotStringParam(request, "pinot_pod_col", "pod"),
		ContainerCol: pinotStringParam(request, "pinot_container_col", "container"),
	}
}

// pinotLogGroupSeverityValues are the lower-cased severity strings filtered for log groups.
var pinotLogGroupSeverityValues = []string{"error", "critical", "fatal", "err", "crit"}

// buildPinotLogGroupSQL emits a GROUP BY query that pushes the aggregation down to Pinot.
// Returns SELECT log, namespace, pod, container, level, COUNT(*) FROM ... GROUP BY ... ORDER BY count DESC LIMIT N.
// Pinot's columnar aggregation is far faster than pulling 10K raw rows and hashing in Go;
// it also removes the sampling cap so we see true top-N across the whole window.
func buildPinotLogGroupSQL(cols pinotLogGroupCols, tsMode pinotTsMode, startMs, endMs int64, selectedNs, selectedWorkload string, limit int) string {
	if limit <= 0 {
		limit = 100
	}
	tsColQ := pinotQuoteIdent(cols.TsCol)
	msgColQ := pinotQuoteIdent(cols.MsgCol)
	nsColQ := pinotQuoteIdent(cols.NsCol)
	podColQ := pinotQuoteIdent(cols.PodCol)
	containerColQ := pinotQuoteIdent(cols.ContainerCol)

	var timeFilter string
	if tsMode.IsString {
		startStr := time.UnixMilli(startMs).UTC().Format(tsMode.GoFormat)
		endStr := time.UnixMilli(endMs).UTC().Format(tsMode.GoFormat)
		timeFilter = fmt.Sprintf("%s BETWEEN '%s' AND '%s'", tsColQ, startStr, endStr)
	} else {
		sf := tsMode.ScaleFactor
		if sf <= 0 {
			sf = 1
		}
		timeFilter = fmt.Sprintf("%s BETWEEN %d AND %d", tsColQ, startMs/sf, endMs/sf)
	}

	conditions := []string{timeFilter}

	groupCols := []string{msgColQ, nsColQ, podColQ, containerColQ}
	selectCols := []string{msgColQ, nsColQ, podColQ, containerColQ}

	if cols.SevCol != "" {
		sevColQ := pinotQuoteIdent(cols.SevCol)
		quoted := make([]string, len(pinotLogGroupSeverityValues))
		for i, v := range pinotLogGroupSeverityValues {
			quoted[i] = "'" + pinotEscapeString(v) + "'"
		}
		conditions = append(conditions, fmt.Sprintf("LOWER(%s) IN (%s)", sevColQ, strings.Join(quoted, ", ")))
		groupCols = append(groupCols, sevColQ)
		selectCols = append(selectCols, sevColQ)
	}

	if selectedNs != "" && cols.NsCol != "" {
		conditions = append(conditions, fmt.Sprintf("%s = '%s'", nsColQ, pinotEscapeString(selectedNs)))
	}
	if selectedWorkload != "" && cols.PodCol != "" {
		conditions = append(conditions, fmt.Sprintf("%s LIKE '%s-%%'", podColQ, pinotEscapeString(selectedWorkload)))
	}

	return fmt.Sprintf(
		"SELECT %s, COUNT(*) AS cnt FROM %s WHERE %s GROUP BY %s ORDER BY cnt DESC LIMIT %d",
		strings.Join(selectCols, ", "),
		pinotQuoteIdent(cols.Table),
		strings.Join(conditions, " AND "),
		strings.Join(groupCols, ", "),
		limit,
	)
}

// parsePinotLogGroupBytes parses a Pinot resultTable from the GROUP BY query into LogGroupOutput.
// Expected columns (in SELECT order): msg, namespace, pod, container, [level], cnt.
func parsePinotLogGroupBytes(data []byte, cols pinotLogGroupCols, endTime int64) (LogGroupOutput, error) {
	var r pinotQueryResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return LogGroupOutput{}, fmt.Errorf("pinot: failed to unmarshal log-group response: %w", err)
	}
	if len(r.Exceptions) > 0 {
		return LogGroupOutput{}, fmt.Errorf("pinot log-group query exception: %v", r.Exceptions[0])
	}

	colIdx := make(map[string]int, len(r.ResultTable.DataSchema.ColumnNames))
	for i, c := range r.ResultTable.DataSchema.ColumnNames {
		colIdx[c] = i
	}
	idxMsg, hasMsg := colIdx[cols.MsgCol]
	idxNs, hasNs := colIdx[cols.NsCol]
	idxPod, hasPod := colIdx[cols.PodCol]
	idxContainer, hasContainer := colIdx[cols.ContainerCol]
	idxSev := -1
	if cols.SevCol != "" {
		if i, ok := colIdx[cols.SevCol]; ok {
			idxSev = i
		}
	}
	idxCnt, hasCnt := colIdx["cnt"]

	var endTimeSec int64
	switch {
	case endTime <= 0:
		endTimeSec = time.Now().Unix()
	case endTime >= 1e12:
		endTimeSec = endTime / 1000
	default:
		endTimeSec = endTime
	}

	groups := make([]LogGroup, 0, len(r.ResultTable.Rows))
	for _, row := range r.ResultTable.Rows {
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
	return LogGroupOutput{Groups: groups}, nil
}

// QueryLogGroup implements LogGroupSource for the relay-mode Pinot integration.
// Strategy: push aggregation down to Pinot via GROUP BY — far more efficient than
// fetching raw rows and hashing client-side, since generatePatternHash is just SHA1
// of the exact message bytes (so client-side grouping has zero fidelity advantage).
func (p *PinotSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	cols := resolvePinotLogGroupColsFromRequest(req.Request)
	if cols.Table == "" {
		return LogGroupOutput{}, fmt.Errorf("pinot.QueryLogGroup: pinot_table is required")
	}

	schema, _ := fetchPinotSchema(ctx, req.AccountId, cols.Table)
	cacheKey := "agent:" + req.AccountId + ":" + cols.Table
	detectedFmt, _ := getCachedPinotTsFormat(cacheKey, cols.TsCol)
	tsMode := resolveTsMode(schema, cols.TsCol, detectedFmt)

	// Sample-on-miss for STRING timestamp columns — mirrors the path in QueryLogs.
	// Without this, a cold cache on a STRING tsCol renders an empty GoFormat and the
	// BETWEEN literal becomes Go's reference time, matching nothing.
	if tsMode.IsString && tsMode.GoFormat == "" {
		if sample, sampleErr := samplePinotTimestampValueRelay(req.AccountId, cols.Table, cols.TsCol); sampleErr == nil {
			if d := pinotDetectTimestampFormatObs(sample); d != "" {
				setCachedPinotTsFormat(cacheKey, cols.TsCol, d)
				tsMode.GoFormat = d
			}
		}
	}

	selectedNs := common.GetString(req.Request, "selectedNamespace")
	selectedWorkload := common.GetString(req.Request, "selectedWorkload")
	sqlQuery := buildPinotLogGroupSQL(cols, tsMode, req.StartTime, req.EndTime, selectedNs, selectedWorkload, 100)

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:    req.AccountId,
			ActionName:   "pinot_query",
			ActionParams: map[string]any{"sql": sqlQuery},
			NoSinks:      true,
		},
	})
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("pinot.QueryLogGroup: relay execute failed: %w", err)
	}
	rawBytes, err := extractPinotRelayData(resp)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("pinot.QueryLogGroup: %w", err)
	}
	return parsePinotLogGroupBytes(rawBytes, cols, req.EndTime)
}

// fetchPinotSchema fetches and caches the Pinot table schema via relay.
// Cache key: "agent:{accountId}:{table}"
func fetchPinotSchema(ctx *security.RequestContext, accountId, table string) (*pinotSchemaResponse, error) {
	cacheKey := "agent:" + accountId + ":" + table
	if cached, ok := getCachedPinotSchema(cacheKey); ok {
		return cached, nil
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:    accountId,
			ActionName:   "pinot_schema",
			ActionParams: map[string]any{"table": table},
			NoSinks:      true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("fetchPinotSchema: relay failed: %w", err)
	}

	rawBytes, err := extractPinotRelayData(resp)
	if err != nil {
		return nil, fmt.Errorf("fetchPinotSchema: %w", err)
	}
	schema, _, err := parsePinotSchemaBytes(rawBytes)
	if err != nil {
		return nil, err
	}
	if schema != nil {
		setPinotSchemaCache(cacheKey, schema)
	}
	return schema, nil
}

// ---- Relay envelope extraction ----

// extractPinotRelayData unpacks the raw agent JSON bytes from the relay NoSinks response.
// For NoSinks calls relay.Execute returns: resp["data"]["data"] = <JSON string or map>.
// Mirrors ElasticSource.ExtractRawResponseString pattern in elasticsearch.go.
func extractPinotRelayData(resp map[string]any) ([]byte, error) {
	data1, ok := resp["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("pinot relay: missing outer data envelope")
	}
	switch v := data1["data"].(type) {
	case string:
		return []byte(v), nil
	case map[string]any:
		return json.Marshal(v)
	default:
		return nil, fmt.Errorf("pinot relay: unexpected payload type %T", data1["data"])
	}
}

// ---- SQL building ----

// buildPinotWhereClause converts a QueryWhereClause to a Pinot SQL WHERE fragment.
func buildPinotWhereClause(where query.QueryWhereClause) (string, error) {
	var parts []string

	for col, ops := range where.Binary {
		for op, val := range ops {
			cond, err := binaryToPinotCondition(col, op, val)
			if err != nil {
				return "", err
			}
			parts = append(parts, cond)
		}
	}

	for _, andClause := range where.And {
		sub, err := buildPinotWhereClause(andClause)
		if err != nil {
			return "", err
		}
		if sub != "" {
			parts = append(parts, sub)
		}
	}

	result := strings.Join(parts, " AND ")

	if len(where.Or) > 0 {
		var orParts []string
		for _, orClause := range where.Or {
			sub, err := buildPinotWhereClause(orClause)
			if err != nil {
				return "", err
			}
			if sub != "" {
				orParts = append(orParts, sub)
			}
		}
		if len(orParts) > 0 {
			orExpr := "(" + strings.Join(orParts, " OR ") + ")"
			if result != "" {
				result += " AND " + orExpr
			} else {
				result = orExpr
			}
		}
	}

	if where.Not != nil {
		sub, err := buildPinotWhereClause(*where.Not)
		if err != nil {
			return "", err
		}
		if sub != "" {
			notExpr := "NOT (" + sub + ")"
			if result != "" {
				result += " AND " + notExpr
			} else {
				result = notExpr
			}
		}
	}

	return result, nil
}

func binaryToPinotCondition(col string, op query.BinaryWhereClauseType, val any) (string, error) {
	colQ := pinotQuoteIdent(col)
	switch op {
	case query.Eq, query.EqF:
		return fmt.Sprintf("%s = %s", colQ, pinotFormatValue(val)), nil
	case query.Nq, query.NqF:
		return fmt.Sprintf("%s <> %s", colQ, pinotFormatValue(val)), nil
	case query.Lt, query.LtF:
		return fmt.Sprintf("%s < %s", colQ, pinotFormatValue(val)), nil
	case query.Gt, query.GtF:
		return fmt.Sprintf("%s > %s", colQ, pinotFormatValue(val)), nil
	case query.Lte, query.LteF:
		return fmt.Sprintf("%s <= %s", colQ, pinotFormatValue(val)), nil
	case query.Gte, query.GteF:
		return fmt.Sprintf("%s >= %s", colQ, pinotFormatValue(val)), nil
	case query.Contains, query.IContains:
		return fmt.Sprintf("%s LIKE '%%%s%%'", colQ, pinotEscapeString(fmt.Sprintf("%v", val))), nil
	case query.NIContains:
		return fmt.Sprintf("%s NOT LIKE '%%%s%%'", colQ, pinotEscapeString(fmt.Sprintf("%v", val))), nil
	case query.Like, query.LikeF:
		return fmt.Sprintf("%s LIKE '%s'", colQ, pinotEscapeString(fmt.Sprintf("%v", val))), nil
	case query.NLike:
		return fmt.Sprintf("%s NOT LIKE '%s'", colQ, pinotEscapeString(fmt.Sprintf("%v", val))), nil
	case query.In:
		items, err := pinotFormatInList(val)
		if err != nil {
			return "", fmt.Errorf("_in for %s: %w", col, err)
		}
		return fmt.Sprintf("%s IN (%s)", colQ, items), nil
	case query.NotIn:
		items, err := pinotFormatInList(val)
		if err != nil {
			return "", fmt.Errorf("_not_in for %s: %w", col, err)
		}
		return fmt.Sprintf("%s NOT IN (%s)", colQ, items), nil
	case query.Regex:
		return fmt.Sprintf("REGEXP_LIKE(%s, %s)", colQ, pinotFormatValue(val)), nil
	case query.NRegex:
		return fmt.Sprintf("NOT REGEXP_LIKE(%s, %s)", colQ, pinotFormatValue(val)), nil
	case query.IsNull:
		if b, ok := val.(bool); ok && !b {
			return fmt.Sprintf("%s IS NOT NULL", colQ), nil
		}
		return fmt.Sprintf("%s IS NULL", colQ), nil
	default:
		return "", fmt.Errorf("unsupported Pinot operator %q for column %q", op, col)
	}
}

// buildPinotSQL constructs the full Pinot SELECT statement.
// req.StartTime and req.EndTime are always epoch milliseconds; tsMode converts them to the column unit.
func buildPinotSQL(table, tsCol, whereClause string, startMs, endMs int64, tsMode pinotTsMode, limit, offset int, sortFields []SortField) string {
	if limit <= 0 {
		limit = 1000
	}

	tsColQ := pinotQuoteIdent(tsCol)
	var timeFilter string
	if tsMode.IsString {
		startStr := time.UnixMilli(startMs).UTC().Format(tsMode.GoFormat)
		endStr := time.UnixMilli(endMs).UTC().Format(tsMode.GoFormat)
		timeFilter = fmt.Sprintf("%s BETWEEN '%s' AND '%s'", tsColQ, startStr, endStr)
	} else {
		sf := tsMode.ScaleFactor
		if sf <= 0 {
			sf = 1
		}
		timeFilter = fmt.Sprintf("%s BETWEEN %d AND %d", tsColQ, startMs/sf, endMs/sf)
	}

	where := timeFilter
	if whereClause != "" {
		where += " AND (" + whereClause + ")"
	}

	orderBy := tsColQ + " DESC"
	if len(sortFields) > 0 {
		var parts []string
		for _, sf := range sortFields {
			ord := "ASC"
			if strings.EqualFold(sf.Order, "desc") {
				ord = "DESC"
			}
			parts = append(parts, pinotQuoteIdent(sf.ColumnName)+" "+ord)
		}
		orderBy = strings.Join(parts, ", ")
	}

	sql := fmt.Sprintf("SELECT * FROM %s WHERE %s ORDER BY %s LIMIT %d", pinotQuoteIdent(table), where, orderBy, limit)
	if offset > 0 {
		sql += fmt.Sprintf(" OFFSET %d", offset)
	}
	return sql
}

// ---- Timestamp mode resolution ----

// getTimestampScaleFactor returns the divisor to convert epoch-ms to the column's native unit
// from a Pinot EPOCH format string like "1:HOURS:EPOCH".
func getTimestampScaleFactor(pinotFmt string) int64 {
	parts := strings.SplitN(pinotFmt, ":", 3)
	if len(parts) < 2 {
		return 1
	}
	switch strings.ToUpper(parts[1]) {
	case "SECONDS":
		return 1_000
	case "MINUTES":
		return 60_000
	case "HOURS":
		return 3_600_000
	case "DAYS":
		return 86_400_000
	default:
		return 1
	}
}

// resolveTsMode determines the pinotTsMode for query building from schema metadata and user config.
func resolveTsMode(schema *pinotSchemaResponse, tsCol, tsFormat string) pinotTsMode {
	if schema != nil {
		for _, f := range schema.DateTimeFieldSpecs {
			if f.Name != tsCol {
				continue
			}
			parts := strings.SplitN(f.Format, ":", 4)
			if len(parts) < 3 {
				return pinotTsMode{ScaleFactor: 1}
			}
			switch strings.ToUpper(parts[2]) {
			case "EPOCH":
				return pinotTsMode{ScaleFactor: getTimestampScaleFactor(f.Format)}
			case "SIMPLE_DATE_FORMAT":
				javaFmt := ""
				if len(parts) == 4 {
					javaFmt = parts[3]
				}
				goFmt := tsFormat
				if goFmt == "" {
					goFmt = pinotJavaToGoFormatObs(javaFmt)
				}
				return pinotTsMode{IsString: true, GoFormat: goFmt}
			}
			return pinotTsMode{ScaleFactor: 1}
		}
		// Check dimension/metric columns for STRING types
		for _, f := range schema.DimensionFieldSpecs {
			if f.Name == tsCol && (f.DataType == "STRING" || f.DataType == "TEXT") {
				return pinotTsMode{IsString: true, GoFormat: tsFormat}
			}
		}
		for _, f := range schema.MetricFieldSpecs {
			if f.Name == tsCol && (f.DataType == "STRING" || f.DataType == "TEXT") {
				return pinotTsMode{IsString: true, GoFormat: tsFormat}
			}
		}
	}
	if tsFormat != "" {
		return pinotTsMode{IsString: true, GoFormat: tsFormat}
	}
	return pinotTsMode{ScaleFactor: 1}
}

// getTimestampConverter returns a func(any) string that converts a Pinot row timestamp value
// to RFC3339Nano, handling both numeric epoch and formatted string columns.
func getTimestampConverter(schema *pinotSchemaResponse, tsCol, tsFormat string) func(any) string {
	mode := resolveTsMode(schema, tsCol, tsFormat)
	if mode.IsString {
		goFmt := mode.GoFormat
		return func(v any) string {
			s, ok := v.(string)
			if !ok {
				return fmt.Sprintf("%v", v)
			}
			if goFmt == "" {
				return s
			}
			t, err := time.Parse(goFmt, s)
			if err != nil {
				return s
			}
			return t.UTC().Format(time.RFC3339Nano)
		}
	}
	// Numeric epoch: convert using scale factor
	sf := mode.ScaleFactor
	if sf <= 0 {
		sf = 1
	}
	return func(v any) string {
		f, ok := v.(float64)
		if !ok {
			return fmt.Sprintf("%v", v)
		}
		var t time.Time
		switch sf {
		case 1:
			t = time.UnixMilli(int64(f))
		case 1_000:
			t = time.Unix(int64(f), 0)
		default:
			// column unit → seconds: sf ms/unit ÷ 1000 ms/s = sf/1000 s/unit
			t = time.Unix(int64(f)*sf/1_000, 0)
		}
		return t.UTC().Format(time.RFC3339Nano)
	}
}

// pinotJavaToGoFormatObs converts a Pinot/Java SimpleDateFormat pattern to a Go time layout.
// Returns empty string when the pattern cannot be reliably converted; callers must fail validation.
func pinotJavaToGoFormatObs(javaFmt string) string {
	if javaFmt == "" {
		return ""
	}
	s := strings.ReplaceAll(javaFmt, "'T'", "T")
	if strings.Contains(s, "'") {
		return "" // other quoted literals not supported
	}
	type sub struct{ java, goFmt string }
	subs := []sub{
		{"yyyy", "2006"}, {"yy", "06"},
		{"SSSSSS", "000000"}, {"SSS", "000"},
		{"MM", "01"}, {"dd", "02"},
		{"HH", "15"}, {"mm", "04"}, {"ss", "05"},
		{"XXX", "-07:00"}, {"XX", "-0700"}, {"Z", "-07:00"}, {"z", "MST"},
	}
	for _, sub := range subs {
		s = strings.ReplaceAll(s, sub.java, sub.goFmt)
	}
	ref := time.Date(2006, 1, 2, 15, 4, 5, 0, time.UTC)
	if _, err := time.Parse(s, ref.Format(s)); err != nil {
		return ""
	}
	return s
}

// ---- Response parsers ----

// parsePinotResultTableBytes converts a Pinot resultTable JSON payload to []OutputLog.
func parsePinotResultTableBytes(data []byte, tsCol, msgCol, sevCol string, tsConv func(any) string) ([]OutputLog, error) {
	var r pinotQueryResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("pinot: failed to unmarshal resultTable: %w", err)
	}
	if len(r.Exceptions) > 0 {
		return nil, fmt.Errorf("pinot query exception: %v", r.Exceptions[0])
	}

	cols := r.ResultTable.DataSchema.ColumnNames
	colIdx := make(map[string]int, len(cols))
	for i, c := range cols {
		colIdx[c] = i
	}

	tsIdx, hasTsCol := colIdx[tsCol]
	msgIdx, hasMsgCol := colIdx[msgCol]
	sevIdx := -1
	if sevCol != "" {
		if i, ok := colIdx[sevCol]; ok {
			sevIdx = i
		}
	}

	logs := make([]OutputLog, 0, len(r.ResultTable.Rows))
	for _, row := range r.ResultTable.Rows {
		ts := ""
		if hasTsCol && tsIdx < len(row) && tsConv != nil {
			ts = tsConv(row[tsIdx])
		}

		msg := ""
		if hasMsgCol && msgIdx < len(row) {
			msg = fmt.Sprintf("%v", row[msgIdx])
		}

		sev := ""
		if sevIdx >= 0 && sevIdx < len(row) {
			sev = fmt.Sprintf("%v", row[sevIdx])
		}
		if sev == "" {
			sev = GetSeverityLevels(msg)
		}

		labels := make(map[string]any, len(cols))
		for i, c := range cols {
			if c == tsCol || c == msgCol || (sevCol != "" && c == sevCol) {
				continue
			}
			if i < len(row) {
				labels[c] = row[i]
			}
		}

		logs = append(logs, OutputLog{
			Timestamp: ts,
			Message:   msg,
			Severity:  sev,
			Labels:    labels,
		})
	}
	return logs, nil
}

// parsePinotSchemaBytes parses a Pinot schema JSON payload.
// Returns both the schema struct (for caching) and the []OutputLogLabel slice.
func parsePinotSchemaBytes(data []byte) (*pinotSchemaResponse, []OutputLogLabel, error) {
	var schema pinotSchemaResponse
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, nil, fmt.Errorf("pinot: failed to unmarshal schema: %w", err)
	}

	labels := make([]OutputLogLabel, 0, len(schema.DimensionFieldSpecs)+len(schema.MetricFieldSpecs)+len(schema.DateTimeFieldSpecs))
	for _, f := range schema.DimensionFieldSpecs {
		labels = append(labels, OutputLogLabel{
			Label:      f.Name,
			Attributes: map[string]any{"dataType": f.DataType, "fieldType": "dimension"},
		})
	}
	for _, f := range schema.MetricFieldSpecs {
		labels = append(labels, OutputLogLabel{
			Label:      f.Name,
			Attributes: map[string]any{"dataType": f.DataType, "fieldType": "metric"},
		})
	}
	for _, f := range schema.DateTimeFieldSpecs {
		labels = append(labels, OutputLogLabel{
			Label:      f.Name,
			Attributes: map[string]any{"dataType": f.DataType, "fieldType": "dateTime", "format": f.Format},
		})
	}
	return &schema, labels, nil
}

// parsePinotLabelValuesBytes extracts distinct values from a single-column resultTable.
func parsePinotLabelValuesBytes(data []byte) ([]OutputLogLabelValue, error) {
	var r pinotQueryResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("pinot: failed to unmarshal label values: %w", err)
	}
	if len(r.Exceptions) > 0 {
		return nil, fmt.Errorf("pinot label values exception: %v", r.Exceptions[0])
	}
	vals := make([]OutputLogLabelValue, 0, len(r.ResultTable.Rows))
	for _, row := range r.ResultTable.Rows {
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

// ---- Small helpers ----

func pinotStringParam(request map[string]any, key, defaultVal string) string {
	if request == nil {
		return defaultVal
	}
	if v, ok := request[key].(string); ok && v != "" {
		return v
	}
	return defaultVal
}

func pinotFormatValue(val any) string {
	switch v := val.(type) {
	case string:
		return "'" + pinotEscapeString(v) + "'"
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%g", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return "'" + pinotEscapeString(fmt.Sprintf("%v", val)) + "'"
	}
}

func pinotEscapeString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func pinotFormatInList(val any) (string, error) {
	items, ok := val.([]any)
	if !ok {
		return "", fmt.Errorf("expected []any, got %T", val)
	}
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = pinotFormatValue(item)
	}
	return strings.Join(parts, ", "), nil
}

var pinotColumnNameRe = regexp.MustCompile(`^[a-zA-Z0-9_.]+$`)

func pinotSafeColumnName(col string) bool {
	return col != "" && pinotColumnNameRe.MatchString(col)
}

// pinotQuoteIdent wraps a Pinot SQL identifier in double quotes, escaping any
// embedded double quote by doubling. Required because Pinot's Calcite parser
// reserves words like "timestamp", "date", "level", "value", "user", etc.
func pinotQuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
