package observability

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/integrations/hiveclient"
	"nudgebee/services/query"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---- Shared types ----

// hiveTsMode carries the timestamp handling strategy for a Hive column.
// Hive timestamps are typically BIGINT (epoch ms or seconds) or STRING/TIMESTAMP.
type hiveTsMode struct {
	IsString    bool   // true → formatted string; false → numeric epoch
	ScaleFactor int64  // numeric: divisor converting epoch-ms → column unit (1=ms, 1000=s)
	GoFormat    string // string: Go time layout (e.g. "2006-01-02T15:04:05Z07:00")
}

// hiveColumnSpec describes a single column from a DESCRIBE response.
// IsPartition is true for columns Hive uses as partition keys — surfaced to
// the UI so we can warn the user if their WHERE clause skips all of them
// (resulting in a full-table scan).
type hiveColumnSpec struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	IsPartition bool   `json:"is_partition,omitempty"`
}

// hiveSchemaResponse is the agent's normalized response from `hive_schema`.
type hiveSchemaResponse struct {
	Columns []hiveColumnSpec `json:"columns"`
}

// hiveQueryResponse is the agent's normalized response from `hive_query`.
// Mirrors Pinot's resultTable shape but flattened: columns + rows + errors.
type hiveQueryResponse struct {
	Columns []string         `json:"columns"`
	Rows    [][]any          `json:"rows"`
	Errors  []map[string]any `json:"errors,omitempty"`
}

// ---- Schema cache ----

type hiveSchemaCacheEntry struct {
	schema            *hiveSchemaResponse
	detectedTsFormats map[string]string
	fetchedAt         time.Time
}

var hiveSchemaCache = struct {
	sync.Mutex
	m map[string]hiveSchemaCacheEntry
}{m: make(map[string]hiveSchemaCacheEntry)}

const hiveSchemaCacheTTL = 5 * time.Minute

func getCachedHiveSchema(key string) (*hiveSchemaResponse, bool) {
	hiveSchemaCache.Lock()
	defer hiveSchemaCache.Unlock()
	e, ok := hiveSchemaCache.m[key]
	if !ok || time.Since(e.fetchedAt) > hiveSchemaCacheTTL {
		return nil, false
	}
	return e.schema, true
}

func setHiveSchemaCache(key string, schema *hiveSchemaResponse) {
	hiveSchemaCache.Lock()
	defer hiveSchemaCache.Unlock()
	prev := hiveSchemaCache.m[key].detectedTsFormats
	hiveSchemaCache.m[key] = hiveSchemaCacheEntry{
		schema:            schema,
		detectedTsFormats: prev,
		fetchedAt:         time.Now(),
	}
}

func getCachedHiveTsFormat(key, col string) (string, bool) {
	hiveSchemaCache.Lock()
	defer hiveSchemaCache.Unlock()
	e, ok := hiveSchemaCache.m[key]
	if !ok || e.detectedTsFormats == nil {
		return "", false
	}
	v, ok := e.detectedTsFormats[col]
	return v, ok && v != ""
}

func setCachedHiveTsFormat(key, col, format string) {
	hiveSchemaCache.Lock()
	defer hiveSchemaCache.Unlock()
	e := hiveSchemaCache.m[key]
	if e.detectedTsFormats == nil {
		e.detectedTsFormats = map[string]string{}
	}
	e.detectedTsFormats[col] = format
	if e.fetchedAt.IsZero() {
		e.fetchedAt = time.Now()
	}
	hiveSchemaCache.m[key] = e
}

// ---- Timestamp auto-detection (sample-based) ----
//
// The known-layouts list and detector live in the hiveclient package so the
// strict integrations.Hive.ValidateConfig path can reuse them. This local
// alias keeps the call sites here unchanged.

func hiveDetectTimestampFormatObs(sample string) string {
	return hiveclient.DetectTimestampFormat(sample)
}

// sampleHiveTimestampValueRelay fetches one non-null timestamp via hive_query.
func sampleHiveTimestampValueRelay(accountId, db, table, tsCol string) (string, error) {
	if !hiveSafeColumnRef(tsCol) {
		return "", fmt.Errorf("invalid timestamp column %q", tsCol)
	}
	tsColQ := hiveQuoteIdent(tsCol)
	sqlQuery := fmt.Sprintf("SELECT %s FROM %s WHERE %s IS NOT NULL LIMIT 1",
		tsColQ, hiveQualifiedTable(db, table), tsColQ)
	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:    accountId,
			ActionName:   "hive_query",
			ActionParams: map[string]any{"sql": sqlQuery},
			NoSinks:      true,
		},
	})
	if err != nil {
		return "", fmt.Errorf("relay sample query failed: %w", err)
	}
	rawBytes, err := extractHiveRelayData(resp)
	if err != nil {
		return "", err
	}
	var r hiveQueryResponse
	if err := json.Unmarshal(rawBytes, &r); err != nil {
		return "", fmt.Errorf("parse sample response: %w", err)
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

// ---- HiveSource — relay/agent mode ----

// HiveSource implements LogSource by routing all queries through the relay agent.
// The agent must register actions hive_query and hive_schema.
type HiveSource struct{}

func (h *HiveSource) GetLabelMapping() map[string]string {
	return map[string]string{}
}

func (h *HiveSource) GetSupportedOperators() []string {
	return []string{
		"_eq", "_neq", "_contains", "_in", "_not_in",
		"_regex", "_nregex", "_is_null",
		"_gt", "_lt", "_gte", "_lte", "_like", "_nlike",
	}
}

func (h *HiveSource) GetQuery(ctx *security.RequestContext, req FetchLogRequest) (string, error) {
	db := hiveStringParam(req.Request, "hive_database", "default")
	table := hiveStringParam(req.Request, "hive_table", "")
	tsCol := hiveStringParam(req.Request, "hive_timestamp_col", "time_ms")
	where, err := buildHiveWhereClause(req.QueryRequest.Where)
	if err != nil {
		return "", fmt.Errorf("hive.GetQuery: %w", err)
	}
	limit := 1000
	if req.Limit > 0 {
		limit = req.Limit
	}
	return buildHiveSQL(db, table, tsCol, where, req.StartTime, req.EndTime, hiveTsMode{ScaleFactor: 1}, limit, req.SortFields), nil
}

func (h *HiveSource) QueryLogs(ctx *security.RequestContext, req FetchLogRequest) ([]OutputLog, error) {
	db := hiveStringParam(req.Request, "hive_database", "default")
	table := hiveStringParam(req.Request, "hive_table", "")
	tsCol := hiveStringParam(req.Request, "hive_timestamp_col", "time_ms")
	msgCol := hiveStringParam(req.Request, "hive_message_col", "log")
	sevCol := hiveStringParam(req.Request, "hive_severity_col", "")

	schema, _ := fetchHiveSchema(ctx, req.AccountId, db, table)
	cacheKey := "agent:" + req.AccountId + ":" + db + "." + table
	detectedFmt, _ := getCachedHiveTsFormat(cacheKey, tsCol)
	tsMode := resolveHiveTsMode(schema, tsCol, detectedFmt)

	if tsMode.IsString && tsMode.GoFormat == "" {
		if sample, sampleErr := sampleHiveTimestampValueRelay(req.AccountId, db, table, tsCol); sampleErr == nil {
			if d := hiveDetectTimestampFormatObs(sample); d != "" {
				setCachedHiveTsFormat(cacheKey, tsCol, d)
				tsMode.GoFormat = d
				detectedFmt = d
			}
		}
	}
	tsConv := getHiveTimestampConverter(schema, tsCol, detectedFmt)

	var sqlQuery string
	if req.Query != "" {
		sqlQuery = req.Query
	} else {
		where, err := buildHiveWhereClause(req.QueryRequest.Where)
		if err != nil {
			return nil, fmt.Errorf("hive.QueryLogs: %w", err)
		}
		limit := 1000
		if req.Limit > 0 {
			limit = req.Limit
		}
		sqlQuery = buildHiveSQL(db, table, tsCol, where, req.StartTime, req.EndTime, tsMode, limit, req.SortFields)
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:    req.AccountId,
			ActionName:   "hive_query",
			ActionParams: map[string]any{"sql": sqlQuery},
			NoSinks:      true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("hive.QueryLogs: relay execute failed: %w", err)
	}
	rawBytes, err := extractHiveRelayData(resp)
	if err != nil {
		return nil, fmt.Errorf("hive.QueryLogs: %w", err)
	}
	limit := 1000
	if req.Limit > 0 {
		limit = req.Limit
	}
	return parseHiveResultBytes(rawBytes, tsCol, msgCol, sevCol, tsConv, limit)
}

func (h *HiveSource) QueryLabels(ctx *security.RequestContext, req FetchLogLabelRequest) ([]OutputLogLabel, error) {
	db := hiveStringParam(req.Request, "hive_database", "default")
	table := hiveStringParam(req.Request, "hive_table", "")

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:    req.AccountId,
			ActionName:   "hive_schema",
			ActionParams: map[string]any{"database": db, "table": table},
			NoSinks:      true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("hive.QueryLabels: relay execute failed: %w", err)
	}
	rawBytes, err := extractHiveRelayData(resp)
	if err != nil {
		return nil, fmt.Errorf("hive.QueryLabels: %w", err)
	}
	schema, labels, err := parseHiveSchemaBytes(rawBytes)
	if err != nil {
		return nil, err
	}
	if schema != nil {
		setHiveSchemaCache("agent:"+req.AccountId+":"+db+"."+table, schema)
	}
	return labels, nil
}

func (h *HiveSource) QueryLabelValues(ctx *security.RequestContext, req FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	col := req.LabelName
	if !hiveSafeColumnRef(col) {
		return nil, fmt.Errorf("hive.QueryLabelValues: invalid column name %q", col)
	}
	db := hiveStringParam(req.Request, "hive_database", "default")
	table := hiveStringParam(req.Request, "hive_table", "")
	colQ := hiveQuoteIdent(col)
	sqlQuery := fmt.Sprintf("SELECT DISTINCT %s FROM %s WHERE %s IS NOT NULL LIMIT 100",
		colQ, hiveQualifiedTable(db, table), colQ)

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:    req.AccountId,
			ActionName:   "hive_query",
			ActionParams: map[string]any{"sql": sqlQuery},
			NoSinks:      true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("hive.QueryLabelValues: relay execute failed: %w", err)
	}
	rawBytes, err := extractHiveRelayData(resp)
	if err != nil {
		return nil, fmt.Errorf("hive.QueryLabelValues: %w", err)
	}
	return parseHiveLabelValuesBytes(rawBytes)
}

// ---- Log groups (shared helpers + relay-mode implementation) ----

type hiveLogGroupCols struct {
	Database, Table, TsCol, MsgCol, SevCol, NsCol, PodCol, ContainerCol string
}

func resolveHiveLogGroupColsFromRequest(request map[string]any) hiveLogGroupCols {
	return hiveLogGroupCols{
		Database:     hiveStringParam(request, "hive_database", "default"),
		Table:        hiveStringParam(request, "hive_table", ""),
		TsCol:        hiveStringParam(request, "hive_timestamp_col", "time_ms"),
		MsgCol:       hiveStringParam(request, "hive_message_col", "log"),
		SevCol:       hiveStringParam(request, "hive_severity_col", ""),
		NsCol:        hiveStringParam(request, "hive_namespace_col", "kubernetes.namespace_name"),
		PodCol:       hiveStringParam(request, "hive_pod_col", "kubernetes.pod_name"),
		ContainerCol: hiveStringParam(request, "hive_container_col", "kubernetes.container_name"),
	}
}

var hiveLogGroupSeverityValues = []string{"error", "critical", "fatal", "err", "crit"}

// buildHiveLogGroupSQL emits a GROUP BY query pushing aggregation to Hive.
func buildHiveLogGroupSQL(cols hiveLogGroupCols, tsMode hiveTsMode, startMs, endMs int64, selectedNs, selectedWorkload string, limit int) string {
	if limit <= 0 {
		limit = 100
	}
	tsColQ := hiveQuoteIdent(cols.TsCol)
	msgColQ := hiveQuoteIdent(cols.MsgCol)
	nsColQ := hiveQuoteIdent(cols.NsCol)
	podColQ := hiveQuoteIdent(cols.PodCol)
	containerColQ := hiveQuoteIdent(cols.ContainerCol)

	var timeFilter string
	if tsMode.IsString {
		startStr := time.UnixMilli(startMs).UTC().Format(tsMode.GoFormat)
		endStr := time.UnixMilli(endMs).UTC().Format(tsMode.GoFormat)
		timeFilter = fmt.Sprintf("%s BETWEEN '%s' AND '%s'", tsColQ, startStr, endStr)
	} else {
		timeFilter = hiveNumericTimeFilter(tsColQ, startMs, endMs, tsMode.ScaleFactor)
	}

	conditions := []string{timeFilter}

	groupCols := []string{msgColQ, nsColQ, podColQ, containerColQ}
	selectCols := []string{msgColQ, nsColQ, podColQ, containerColQ}

	if cols.SevCol != "" {
		sevColQ := hiveQuoteIdent(cols.SevCol)
		quoted := make([]string, len(hiveLogGroupSeverityValues))
		for i, v := range hiveLogGroupSeverityValues {
			quoted[i] = "'" + hiveEscapeString(v) + "'"
		}
		conditions = append(conditions, fmt.Sprintf("LOWER(%s) IN (%s)", sevColQ, strings.Join(quoted, ", ")))
		groupCols = append(groupCols, sevColQ)
		selectCols = append(selectCols, sevColQ)
	}

	if selectedNs != "" && cols.NsCol != "" {
		conditions = append(conditions, fmt.Sprintf("%s = '%s'", nsColQ, hiveEscapeString(selectedNs)))
	}
	if selectedWorkload != "" && cols.PodCol != "" {
		conditions = append(conditions, fmt.Sprintf("%s LIKE '%s-%%'", podColQ, hiveEscapeString(selectedWorkload)))
	}

	return fmt.Sprintf(
		"SELECT %s, COUNT(*) AS cnt FROM %s WHERE %s GROUP BY %s ORDER BY cnt DESC LIMIT %d",
		strings.Join(selectCols, ", "),
		hiveQualifiedTable(cols.Database, cols.Table),
		strings.Join(conditions, " AND "),
		strings.Join(groupCols, ", "),
		limit,
	)
}

// parseHiveLogGroupBytes parses a Hive resultset JSON into LogGroupOutput.
// Delegates to parseHiveLogGroupFromStruct after unmarshalling.
func parseHiveLogGroupBytes(data []byte, cols hiveLogGroupCols, endTime int64) (LogGroupOutput, error) {
	var r hiveQueryResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return LogGroupOutput{}, fmt.Errorf("hive: failed to unmarshal log-group response: %w", err)
	}
	if len(r.Errors) > 0 {
		return LogGroupOutput{}, fmt.Errorf("hive log-group query error: %v", r.Errors[0])
	}
	return parseHiveLogGroupFromStruct(&r, cols, endTime), nil
}

// QueryLogGroup implements LogGroupSource for the relay-mode Hive integration.
func (h *HiveSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	cols := resolveHiveLogGroupColsFromRequest(req.Request)
	if cols.Table == "" {
		return LogGroupOutput{}, fmt.Errorf("hive.QueryLogGroup: hive_table is required")
	}

	schema, _ := fetchHiveSchema(ctx, req.AccountId, cols.Database, cols.Table)
	cacheKey := "agent:" + req.AccountId + ":" + cols.Database + "." + cols.Table
	detectedFmt, _ := getCachedHiveTsFormat(cacheKey, cols.TsCol)
	tsMode := resolveHiveTsMode(schema, cols.TsCol, detectedFmt)

	if tsMode.IsString && tsMode.GoFormat == "" {
		if sample, sampleErr := sampleHiveTimestampValueRelay(req.AccountId, cols.Database, cols.Table, cols.TsCol); sampleErr == nil {
			if d := hiveDetectTimestampFormatObs(sample); d != "" {
				setCachedHiveTsFormat(cacheKey, cols.TsCol, d)
				tsMode.GoFormat = d
			}
		}
	}

	selectedNs := common.GetString(req.Request, "selectedNamespace")
	selectedWorkload := common.GetString(req.Request, "selectedWorkload")
	sqlQuery := buildHiveLogGroupSQL(cols, tsMode, req.StartTime, req.EndTime, selectedNs, selectedWorkload, 100)

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:    req.AccountId,
			ActionName:   "hive_query",
			ActionParams: map[string]any{"sql": sqlQuery},
			NoSinks:      true,
		},
	})
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("hive.QueryLogGroup: relay execute failed: %w", err)
	}
	rawBytes, err := extractHiveRelayData(resp)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("hive.QueryLogGroup: %w", err)
	}
	return parseHiveLogGroupBytes(rawBytes, cols, req.EndTime)
}

func fetchHiveSchema(ctx *security.RequestContext, accountId, db, table string) (*hiveSchemaResponse, error) {
	cacheKey := "agent:" + accountId + ":" + db + "." + table
	if cached, ok := getCachedHiveSchema(cacheKey); ok {
		return cached, nil
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:    accountId,
			ActionName:   "hive_schema",
			ActionParams: map[string]any{"database": db, "table": table},
			NoSinks:      true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("fetchHiveSchema: relay failed: %w", err)
	}

	rawBytes, err := extractHiveRelayData(resp)
	if err != nil {
		return nil, fmt.Errorf("fetchHiveSchema: %w", err)
	}
	schema, _, err := parseHiveSchemaBytes(rawBytes)
	if err != nil {
		return nil, err
	}
	if schema != nil {
		setHiveSchemaCache(cacheKey, schema)
	}
	return schema, nil
}

// ---- Relay envelope extraction ----

// extractHiveRelayData unpacks raw agent JSON bytes from the relay NoSinks response.
// Mirrors extractPinotRelayData.
func extractHiveRelayData(resp map[string]any) ([]byte, error) {
	data1, ok := resp["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("hive relay: missing outer data envelope")
	}
	switch v := data1["data"].(type) {
	case string:
		return []byte(v), nil
	case map[string]any:
		return json.Marshal(v)
	default:
		return nil, fmt.Errorf("hive relay: unexpected payload type %T", data1["data"])
	}
}

// ---- SQL building ----

func buildHiveWhereClause(where query.QueryWhereClause) (string, error) {
	var parts []string

	for col, ops := range where.Binary {
		for op, val := range ops {
			cond, err := binaryToHiveCondition(col, op, val)
			if err != nil {
				return "", err
			}
			parts = append(parts, cond)
		}
	}

	for _, andClause := range where.And {
		sub, err := buildHiveWhereClause(andClause)
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
			sub, err := buildHiveWhereClause(orClause)
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
		sub, err := buildHiveWhereClause(*where.Not)
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

func binaryToHiveCondition(col string, op query.BinaryWhereClauseType, val any) (string, error) {
	colQ := hiveQuoteIdent(col)
	switch op {
	case query.Eq, query.EqF:
		return fmt.Sprintf("%s = %s", colQ, hiveFormatValue(val)), nil
	case query.Nq, query.NqF:
		return fmt.Sprintf("%s <> %s", colQ, hiveFormatValue(val)), nil
	case query.Lt, query.LtF:
		return fmt.Sprintf("%s < %s", colQ, hiveFormatValue(val)), nil
	case query.Gt, query.GtF:
		return fmt.Sprintf("%s > %s", colQ, hiveFormatValue(val)), nil
	case query.Lte, query.LteF:
		return fmt.Sprintf("%s <= %s", colQ, hiveFormatValue(val)), nil
	case query.Gte, query.GteF:
		return fmt.Sprintf("%s >= %s", colQ, hiveFormatValue(val)), nil
	case query.Contains, query.IContains:
		return fmt.Sprintf("%s LIKE '%%%s%%'", colQ, hiveEscapeString(fmt.Sprintf("%v", val))), nil
	case query.NIContains:
		return fmt.Sprintf("%s NOT LIKE '%%%s%%'", colQ, hiveEscapeString(fmt.Sprintf("%v", val))), nil
	case query.Like, query.LikeF:
		return fmt.Sprintf("%s LIKE '%s'", colQ, hiveEscapeString(fmt.Sprintf("%v", val))), nil
	case query.NLike:
		return fmt.Sprintf("%s NOT LIKE '%s'", colQ, hiveEscapeString(fmt.Sprintf("%v", val))), nil
	case query.In:
		items, err := hiveFormatInList(val)
		if err != nil {
			return "", fmt.Errorf("_in for %s: %w", col, err)
		}
		return fmt.Sprintf("%s IN (%s)", colQ, items), nil
	case query.NotIn:
		items, err := hiveFormatInList(val)
		if err != nil {
			return "", fmt.Errorf("_not_in for %s: %w", col, err)
		}
		return fmt.Sprintf("%s NOT IN (%s)", colQ, items), nil
	case query.Regex:
		// Hive uses RLIKE for regex matching.
		return fmt.Sprintf("%s RLIKE %s", colQ, hiveFormatValue(val)), nil
	case query.NRegex:
		return fmt.Sprintf("NOT (%s RLIKE %s)", colQ, hiveFormatValue(val)), nil
	case query.IsNull:
		if b, ok := val.(bool); ok && !b {
			return fmt.Sprintf("%s IS NOT NULL", colQ), nil
		}
		return fmt.Sprintf("%s IS NULL", colQ), nil
	default:
		return "", fmt.Errorf("unsupported Hive operator %q for column %q", op, col)
	}
}

// hiveLogOverfetchFactor multiplies the user's LIMIT before pushing it to
// Hive so the client-side sort keeps the actual newest rows in the window,
// not just "first N rows Hive happened to scan first." Hive returns rows in
// HDFS file scan order which doesn't always correlate with timestamp; over-
// fetching trades a small amount of bandwidth for a meaningful UX win.
const hiveLogOverfetchFactor = 10

// hiveLogOverfetchCap is the absolute upper bound on the SQL-side LIMIT.
// Caps the cost of a runaway overfetch on a wide time range or huge LIMIT
// request — never read more than 10k raw rows just to take the top N.
const hiveLogOverfetchCap = 10_000

// buildHiveSQL constructs the full Hive SELECT statement.
//
// Deliberately omits ORDER BY: ordering forces Hive into a MapReduce job
// (sorting needs a reducer), and many HiveServer2 deployments — especially
// containerised ones running MR in local mode — fail or are extremely slow
// on MR tasks. A simple SELECT … WHERE … LIMIT goes through the fetch task
// instead and returns in <1s. The result is sorted client-side by the
// parsers (sortLogsByTimestampDesc) so the user-facing newest-first
// invariant is preserved. sortFields is accepted but currently honoured
// only as a no-op — wire client-side custom sorting when the UI surfaces it.
//
// Hive also lacks OFFSET, so callers paginate via WHERE clause filters.
//
// We overfetch by hiveLogOverfetchFactor and truncate client-side after the
// sort (see parseHiveResultBytes). This guards against the fetch-task path
// returning rows in file-scan order: without overfetch, a user asking for
// "last 100 rows" could get the first 100 of an arbitrary partition file
// rather than the actual newest 100 in the window.
func buildHiveSQL(db, table, tsCol, whereClause string, startMs, endMs int64, tsMode hiveTsMode, limit int, _ []SortField) string {
	if limit <= 0 {
		limit = 1000
	}
	scanLimit := limit * hiveLogOverfetchFactor
	if scanLimit > hiveLogOverfetchCap {
		scanLimit = hiveLogOverfetchCap
	}
	if scanLimit < limit {
		scanLimit = limit
	}

	tsColQ := hiveQuoteIdent(tsCol)
	var timeFilter string
	if tsMode.IsString {
		startStr := time.UnixMilli(startMs).UTC().Format(tsMode.GoFormat)
		endStr := time.UnixMilli(endMs).UTC().Format(tsMode.GoFormat)
		timeFilter = fmt.Sprintf("%s BETWEEN '%s' AND '%s'", tsColQ, startStr, endStr)
	} else {
		timeFilter = hiveNumericTimeFilter(tsColQ, startMs, endMs, tsMode.ScaleFactor)
	}

	where := timeFilter
	if whereClause != "" {
		where += " AND (" + whereClause + ")"
	}

	return fmt.Sprintf("SELECT * FROM %s WHERE %s LIMIT %d",
		hiveQualifiedTable(db, table), where, scanLimit)
}

// sortLogsByTimestampDesc sorts a result set newest-first by the OutputLog
// timestamp string. Timestamps are RFC3339Nano (produced by
// getHiveTimestampConverter) so lexicographic compare matches chronological
// order. Logs with an empty timestamp sort to the end.
func sortLogsByTimestampDesc(logs []OutputLog) {
	sort.SliceStable(logs, func(i, j int) bool {
		a, b := logs[i].Timestamp, logs[j].Timestamp
		if a == b {
			return false
		}
		if a == "" {
			return false
		}
		if b == "" {
			return true
		}
		return a > b
	})
}

// ---- Timestamp mode resolution ----

// hiveNumericTimeFilter emits a `tsCol BETWEEN start AND end` predicate where
// start/end are converted from milliseconds to the column's native unit per
// the ScaleFactor convention (see hiveTypeScaleFactor). Microseconds use the
// negative sentinel and require multiplication instead of division.
func hiveNumericTimeFilter(tsColQ string, startMs, endMs int64, sf int64) string {
	var start, end int64
	switch {
	case sf == hiveScaleMicros:
		start, end = startMs*1_000, endMs*1_000
	case sf > 0:
		start, end = startMs/sf, endMs/sf
	default:
		// Unknown sentinel — assume ms.
		start, end = startMs, endMs
	}
	return fmt.Sprintf("%s BETWEEN %d AND %d", tsColQ, start, end)
}

// hiveScaleMicros is the sentinel ScaleFactor value meaning "column unit is
// microseconds". Microseconds can't be expressed as a positive "ms per unit"
// divisor (it'd be 0.001), so we use a distinct negative value and the
// converter + time-filter builders branch on it explicitly.
const hiveScaleMicros int64 = -1

// hiveTypeScaleFactor infers a divisor from a Hive numeric timestamp column type.
// Hive doesn't carry unit metadata like Pinot does; we infer from name + type
// (e.g. *_ms → milliseconds, *_us → microseconds, *_s/*_sec → seconds,
// otherwise assume milliseconds — the modern norm for container log shippers
// (fluent-bit / fluentd / OTel) and aligns with the most common k8s_logs
// schema).
//
// Returns:
//   - 1                : column unit is milliseconds (default)
//   - hiveScaleMicros  : column unit is microseconds (sentinel; multiply ms by 1000)
//   - 1000             : column unit is seconds (only when the name suffix says so)
func hiveTypeScaleFactor(colName, dataType string) int64 {
	lower := strings.ToLower(colName)
	switch {
	case strings.HasSuffix(lower, "_us") || strings.HasSuffix(lower, "_micros"):
		return hiveScaleMicros
	case strings.HasSuffix(lower, "_s") || strings.HasSuffix(lower, "_sec") || strings.HasSuffix(lower, "_seconds"):
		return 1_000
	}
	// Default: milliseconds. Covers *_ms / *_millis and the unsuffixed common
	// case. Customers with second-precision columns must name them
	// explicitly (or pre-cast to ms) to opt out.
	return 1
}

func resolveHiveTsMode(schema *hiveSchemaResponse, tsCol, tsFormat string) hiveTsMode {
	if schema != nil {
		for _, c := range schema.Columns {
			if c.Name != tsCol {
				continue
			}
			t := strings.ToLower(strings.TrimSpace(c.Type))
			switch {
			case strings.HasPrefix(t, "string") || strings.HasPrefix(t, "varchar") || strings.HasPrefix(t, "char") || strings.HasPrefix(t, "timestamp"):
				return hiveTsMode{IsString: true, GoFormat: tsFormat}
			case strings.HasPrefix(t, "bigint") || strings.HasPrefix(t, "int") || strings.HasPrefix(t, "double") || strings.HasPrefix(t, "float") || strings.HasPrefix(t, "decimal"):
				return hiveTsMode{ScaleFactor: hiveTypeScaleFactor(tsCol, t)}
			}
		}
	}
	if tsFormat != "" {
		return hiveTsMode{IsString: true, GoFormat: tsFormat}
	}
	// Best-effort guess from column name only (no schema).
	return hiveTsMode{ScaleFactor: hiveTypeScaleFactor(tsCol, "")}
}

func getHiveTimestampConverter(schema *hiveSchemaResponse, tsCol, tsFormat string) func(any) string {
	mode := resolveHiveTsMode(schema, tsCol, tsFormat)
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
	sf := mode.ScaleFactor
	return func(v any) string {
		var n int64
		switch val := v.(type) {
		case float64:
			n = int64(val)
		case int64:
			n = val
		case int:
			n = int64(val)
		case string:
			parsed, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return val
			}
			n = parsed
		default:
			return fmt.Sprintf("%v", v)
		}
		var t time.Time
		switch {
		case sf == hiveScaleMicros:
			t = time.UnixMicro(n)
		case sf == 1:
			t = time.UnixMilli(n)
		case sf == 1_000:
			t = time.Unix(n, 0)
		case sf > 0:
			t = time.Unix(n*sf/1_000, 0)
		default:
			// Unrecognised non-positive sentinel — fall back to milliseconds.
			t = time.UnixMilli(n)
		}
		return t.UTC().Format(time.RFC3339Nano)
	}
}

// ---- Response parsers ----

// parseHiveResultBytes converts a Hive query response JSON to []OutputLog.
// `limit` is the user-facing limit — after sort the slice is truncated to
// this many newest rows. Pass <= 0 to keep all rows (used when the caller
// already capped the SQL-side LIMIT).
func parseHiveResultBytes(data []byte, tsCol, msgCol, sevCol string, tsConv func(any) string, limit int) ([]OutputLog, error) {
	var r hiveQueryResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("hive: failed to unmarshal result: %w", err)
	}
	if len(r.Errors) > 0 {
		return nil, fmt.Errorf("hive query error: %v", r.Errors[0])
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

	tsIdx, hasTsCol := lookup(tsCol)
	msgIdx, hasMsgCol := lookup(msgCol)
	sevIdx := -1
	if sevCol != "" {
		if i, ok := lookup(sevCol); ok {
			sevIdx = i
		}
	}

	logs := make([]OutputLog, 0, len(r.Rows))
	for _, row := range r.Rows {
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

		labels := make(map[string]any, len(r.Columns))
		for i, c := range r.Columns {
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
	sortLogsByTimestampDesc(logs)
	if limit > 0 && len(logs) > limit {
		logs = logs[:limit]
	}
	return logs, nil
}

// parseHiveSchemaBytes parses an agent hive_schema JSON payload.
func parseHiveSchemaBytes(data []byte) (*hiveSchemaResponse, []OutputLogLabel, error) {
	var schema hiveSchemaResponse
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, nil, fmt.Errorf("hive: failed to unmarshal schema: %w", err)
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
	return &schema, labels, nil
}

// parseHiveLabelValuesBytes extracts distinct values from a single-column result.
func parseHiveLabelValuesBytes(data []byte) ([]OutputLogLabelValue, error) {
	var r hiveQueryResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("hive: failed to unmarshal label values: %w", err)
	}
	if len(r.Errors) > 0 {
		return nil, fmt.Errorf("hive label values error: %v", r.Errors[0])
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

// ---- Small helpers ----

func hiveStringParam(request map[string]any, key, defaultVal string) string {
	if request == nil {
		return defaultVal
	}
	if v, ok := request[key].(string); ok && v != "" {
		return v
	}
	return defaultVal
}

func hiveFormatValue(val any) string {
	switch v := val.(type) {
	case string:
		return "'" + hiveEscapeString(v) + "'"
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
		return "'" + hiveEscapeString(fmt.Sprintf("%v", val)) + "'"
	}
}

func hiveEscapeString(s string) string {
	// HiveQL: escape backslash and single quote.
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return s
}

func hiveFormatInList(val any) (string, error) {
	items, ok := val.([]any)
	if !ok {
		return "", fmt.Errorf("expected []any, got %T", val)
	}
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = hiveFormatValue(item)
	}
	return strings.Join(parts, ", "), nil
}

// hiveColumnRefRe accepts a column reference, optionally dot-qualified for
// nested struct access (e.g. "kubernetes.namespace_name").
var hiveColumnRefRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)*$`)

func hiveSafeColumnRef(col string) bool {
	return col != "" && hiveColumnRefRe.MatchString(col)
}

// hiveQuoteIdent wraps each segment of a Hive column reference in backticks.
// "kubernetes.namespace_name" → "`kubernetes`.`namespace_name`".
// Embedded backticks are doubled per HiveQL escaping rules.
func hiveQuoteIdent(s string) string {
	parts := strings.Split(s, ".")
	for i, p := range parts {
		parts[i] = "`" + strings.ReplaceAll(p, "`", "``") + "`"
	}
	return strings.Join(parts, ".")
}

// hiveQualifiedTable returns `db`.`table` (or just `table` if db is empty).
func hiveQualifiedTable(db, table string) string {
	if db == "" {
		return hiveQuoteIdent(table)
	}
	return hiveQuoteIdent(db) + "." + hiveQuoteIdent(table)
}
