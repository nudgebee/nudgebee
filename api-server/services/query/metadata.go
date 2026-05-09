package query

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/services/account"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"
)

const traceIntegrationCacheTTL = 10 * time.Minute

// traceIntegrationCache caches GetDefaultTraceIntegrationType results per accountId
// with a TTL to avoid repeated DB lookups on every trace query.
var traceIntegrationCache = struct {
	sync.RWMutex
	entries map[string]traceIntCacheEntry
}{entries: make(map[string]traceIntCacheEntry)}

// InvalidateTraceIntegrationCache clears the trace integration lookup cache.
// Call this after any integration create/update/delete operation.
func InvalidateTraceIntegrationCache() {
	traceIntegrationCache.Lock()
	traceIntegrationCache.entries = make(map[string]traceIntCacheEntry)
	traceIntegrationCache.Unlock()
}

type traceIntCacheEntry struct {
	value     string
	expiresAt time.Time
}

type TableType string

const (
	Aggregate TableType = "aggregate"
	Normal    TableType = "normal"
	Derived   TableType = "derived"
)

type ColumnDefinitionType string

const (
	ColumnDefinitionTypeString   ColumnDefinitionType = "string"
	ColumnDefinitionTypeInt      ColumnDefinitionType = "integer"
	ColumnDefinitionTypeFloat    ColumnDefinitionType = "float"
	ColumnDefinitionTypeDatetime ColumnDefinitionType = "datetime"
	ColumnDefinitionTypeList     ColumnDefinitionType = "list"
	ColumnDefinitionTypeMap      ColumnDefinitionType = "map"
	ColumnDefinitionTypeJson     ColumnDefinitionType = "json"
	ColumnDefinitionTypeBoolean  ColumnDefinitionType = "boolean"
)

type ColumnDefinition struct {
	Type         ColumnDefinitionType
	Def          string
	IsAggregated bool
	DefGenerator func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error)
	WhereDef     string
}

type TableDefinition struct {
	Columns             map[string]ColumnDefinition
	Type                TableType
	Source              database.DatabaseManagerType
	SourceGenerator     func(ctx *security.RequestContext, accountId string, request QueryRequest) database.DatabaseManagerType
	Def                 string
	DefGenerator        func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error)
	Name                string
	TenantIdColumnName  string
	AccountIdColumnName string
	NamespaceColumnName string
	UpdateFilters       func(ctx *security.RequestContext, request QueryRequest) (QueryRequest, error)
}

var AzureTraceTableDefinition = map[string]ColumnDefinition{
	"trace_id": {
		Type: ColumnDefinitionTypeString,
		Def:  "operation_Id",
	},
	"timestamp": {
		Type: ColumnDefinitionTypeDatetime,
		Def:  "timestamp",
	},
	"http_status_code": {
		Type: ColumnDefinitionTypeString,
		Def:  "tostring(customMeasurements[\"http.status_code\"])",
	},
	"span_id": {
		Type: ColumnDefinitionTypeString,
		Def:  "id",
	},
	"name": {
		Type: ColumnDefinitionTypeString,
		Def:  "name",
	},
	"parent_span_id": {
		Type: ColumnDefinitionTypeString,
		Def:  "operation_ParentId",
	},
	"duration_ns": {
		Type: ColumnDefinitionTypeString,
		Def:  "duration",
	},
	"status_code": {
		Type: ColumnDefinitionTypeString,
		Def:  "resultCode",
	},
	"span_name": {
		Type: ColumnDefinitionTypeString,
		Def:  "name",
	},
	"resource": {
		Type: ColumnDefinitionTypeString,
		Def:  "tostring(customDimensions[\"service.name\"])",
	},
	"destination_workload_name": {
		Type: ColumnDefinitionTypeString,
		Def:  "tostring(customDimensions[\"destination.workload_name\"])",
	},
	"destination_workload_namespace": {
		Type: ColumnDefinitionTypeString,
		Def:  "tostring(customDimensions[\"destination.workload_namespace\"])",
	},
	"workload_name": {
		Type: ColumnDefinitionTypeString,
		Def:  "tostring(customDimensions[\"source.workload_name\"])",
	},
	"workload_namespace": {
		Type: ColumnDefinitionTypeString,
		Def:  "tostring(customDimensions[\"source.workload_namespace\"])",
	},
	"headers": {
		Type: ColumnDefinitionTypeString,
		Def:  "tostring(customDimensions[\"headers\"])",
	},
	"request_payload": {
		Type: ColumnDefinitionTypeString,
		Def:  "tostring(customDimensions[\"http.request_payload\"])",
	},
	"http_response": {
		Type: ColumnDefinitionTypeString,
		Def:  "tostring(customDimensions[\"http.response\"])",
	},
	"service_name": {
		Type: ColumnDefinitionTypeString,
		Def:  "tostring(customDimensions[\"service.name\"])",
	},
}

func GetDefaultTraceIntegrationType(context *security.RequestContext, accountId string) (string, error) {
	// Check cache first
	traceIntegrationCache.RLock()
	if entry, ok := traceIntegrationCache.entries[accountId]; ok {
		if time.Now().Before(entry.expiresAt) {
			traceIntegrationCache.RUnlock()
			return entry.value, nil
		}
		// Expired — evict the stale entry
		traceIntegrationCache.RUnlock()
		traceIntegrationCache.Lock()
		if e, exists := traceIntegrationCache.entries[accountId]; exists && time.Now().After(e.expiresAt) {
			delete(traceIntegrationCache.entries, accountId)
		}
		traceIntegrationCache.Unlock()
	} else {
		traceIntegrationCache.RUnlock()
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		context.GetLogger().Error("integrations: failed to get database manager", "error", err)
		return "", err
	}
	rows, err := dbms.Db.Queryx(`SELECT i.type
			FROM integrations i
			JOIN integrations_cloud_accounts ica
			ON i.id = ica.integration_id
			WHERE ica.cloud_account_id = $1
			AND ica.default_traces_provider = true
			LIMIT 1;`, accountId)
	if err != nil {
		context.GetLogger().Error("integrations: failed to get integration by config values", "error", err)
		return "", err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			slog.Error("integrations: failed to close integration by config values result", "error", cerr)
		}
	}()
	integrationType := ""

	if rows.Next() {
		if err := rows.Scan(&integrationType); err != nil {
			return "", err
		}
	}

	// Cache the result (including empty string)
	traceIntegrationCache.Lock()
	traceIntegrationCache.entries[accountId] = traceIntCacheEntry{
		value:     integrationType,
		expiresAt: time.Now().Add(traceIntegrationCacheTTL),
	}
	traceIntegrationCache.Unlock()

	return integrationType, nil
}

func GetTracesTableNames(ctx *security.RequestContext, accountId string) []string {
	agentDetails, err := account.GetAgentConnectionDetails(accountId)
	defaultTables := []string{account.AgentTraceTableConfigKey}
	if err != nil {
		ctx.GetLogger().Error("query: unable to identify traces table, returning default", "error", err)
		return defaultTables
	}
	traceTable := agentDetails.Features.TraceProviderConfig[account.AgentTraceTableConfigKey]
	if traceTable != nil {
		defaultTables[0] = traceTable.(string)
	}
	return defaultTables
}

func getTracesProviderAndUrl(ctx *security.RequestContext, accountId string) (string, string, bool) {
	agentDetails, err := account.GetAgentConnectionDetails(accountId)
	traceProvider := "otel_clickhouse"
	traceProviderConfig := "otel_clickhouse"
	hasMaterializedColumn := false
	if err != nil {
		ctx.GetLogger().Error("query: unable to identify traces provider, returning default 'otel_clickhouse'", "error", err)
		return traceProvider, traceProviderConfig, hasMaterializedColumn
	}
	if agentDetails.Features.TraceProvider != nil {
		traceProvider = *agentDetails.Features.TraceProvider
	}
	if agentDetails.Features.TracesUrl != nil {
		traceProviderConfig = *agentDetails.Features.TracesUrl
	}
	if config := agentDetails.Features.TraceProviderConfig; config != nil {
		if val, ok := config["hasMaterializedColumn"].(bool); ok {
			hasMaterializedColumn = val
		} else {
			hasMaterializedColumn = false
		}
	}
	return traceProvider, traceProviderConfig, hasMaterializedColumn
}

func getSource(tableName string) database.DatabaseManagerType {
	if tableName == "ticket_groupings_v2" || tableName == "spend_groupings_v2" || tableName == "event_groupings_v2" || tableName == "events_v2" || tableName == "k8s_metrics_groupings_v2" || tableName == "metric_groupings_v2" || tableName == "dw_query_groupings_v2" || tableName == "event_rules_groupings_v2" || tableName == "slo_report_groupings_v2" || tableName == "auto_pilot_groupings_v2" {
		return database.Metastore
	}

	if config.Config.ClickhouseEnabled {
		return database.Warehouse
	}
	return database.Metastore
}

func getSourceByAccountId(ctx *security.RequestContext, accountId string) database.DatabaseManagerType {
	agentDetails, err := account.GetAgentConnectionDetails(accountId)
	if err != nil {
		ctx.GetLogger().Error("query: unable to identify traces table, returning default", "error", err)
		return database.AgentWarehouse
	}
	if agentDetails.Features.TraceProvider != nil && *agentDetails.Features.TraceProvider == "bigquery" {
		return database.AgentWarehouseBigQuery
	}
	// if prometheus url contains "chronosphere", use chronosphere
	if agentDetails.Features.PrometheusUrl != nil && strings.Contains(*agentDetails.Features.PrometheusUrl, "chronosphere") {
		return database.AgentWarehouseChronosphere
	}
	integrationType, err := GetDefaultTraceIntegrationType(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("query: unable to identify traces table, returning default", "error", err)
		return database.AgentWarehouse
	}
	if integrationType == "azure_app_insights" {
		return database.AzureMonitoring
	}

	return database.AgentWarehouse
}

func getColumnDefinitionValue(traceProvider string, colName string) string {
	if traceProvider == "otel_clickhouse" && colName == "p99_latency" {
		return "quantile(0.99)(duration_ns)"
	} else if traceProvider == "otel_clickhouse" && colName == "p95_latency" {
		return "quantile(0.95)(duration_ns)"
	} else if traceProvider == "otel_clickhouse" && colName == "p50_latency" {
		return "quantile(0.50)(duration_ns)"
	} else if traceProvider == "bigquery" && colName == "p50_latency" {
		return "APPROX_QUANTILES(duration_ns, 100)[OFFSET(50)]"
	} else if traceProvider == "bigquery" && colName == "p95_latency" {
		return "APPROX_QUANTILES(duration_ns, 100)[OFFSET(95)]"
	} else if traceProvider == "bigquery" && colName == "p99_latency" {
		return "APPROX_QUANTILES(duration_ns, 100)[OFFSET(99)]"
	}
	return ""
}

// chronoStringMatchMap maps internal operators to Chronosphere's string match types.
var chronoStringMatchMap = map[BinaryWhereClauseType]string{
	Eq:    "EXACT",
	Nq:    "EXACT_NEGATION",
	In:    "IN",
	NotIn: "NOT_IN",
	Like:  "REGEX",
	NLike: "REGEX_NEGATION",
}

// chronoNumericComparisonMap maps internal operators to Chronosphere's numeric comparison types.
var chronoNumericComparisonMap = map[BinaryWhereClauseType]string{
	Eq:  "EQUAL",
	Nq:  "NOT_EQUAL",
	Gt:  "GREATER_THAN",
	Gte: "GREATER_THAN_OR_EQUAL",
	Lt:  "LESS_THAN",
	Lte: "LESS_THAN_OR_EQUAL",
}

// appendTagFilter is a helper function that creates and appends a correctly formatted
// tag filter to the params map based on the key, operator, and value.
func appendTagFilter(params map[string]any, key string, op BinaryWhereClauseType, value any) {
	// Ensure tag_filters slice exists and is of the correct type.
	if _, ok := params["tag_filters"]; !ok {
		params["tag_filters"] = []map[string]any{}
	}
	tagFilters, ok := params["tag_filters"].([]map[string]any)
	if !ok {
		// If it exists but is the wrong type, we cannot proceed.
		// In a real application, you should log this error.
		return
	}

	tagFilter := map[string]any{"key": key}

	// Case 1: Handle numeric values
	if numValue, isNumeric := convertToNumeric(value); isNumeric {
		if comparison, supported := chronoNumericComparisonMap[op]; supported {
			tagFilter["numeric_value"] = map[string]any{
				"comparison": comparison,
				"value":      numValue,
			}
		} else {
			return // Operator not supported for numeric types
		}
		// Case 2: Handle string values
	} else {
		if matchType, supported := chronoStringMatchMap[op]; supported {
			// Special handling for IN and NOT_IN, which require an array of strings
			if op == In || op == NotIn {
				if values, isSlice := value.([]any); isSlice {
					strValues := make([]string, len(values))
					for i, v := range values {
						strValues[i] = fmt.Sprintf("%v", v)
					}
					tagFilter["value"] = map[string]any{
						"match":     matchType,
						"in_values": strValues,
					}
				} else {
					return // IN/NOT_IN requires a slice value
				}
			} else {
				// Handle standard string comparisons (EXACT, REGEX, etc.)
				tagFilter["value"] = map[string]any{
					"match": matchType,
					"value": fmt.Sprintf("%v", value),
				}
			}
		} else {
			return // Operator not supported for string types
		}
	}

	params["tag_filters"] = append(tagFilters, tagFilter)
}

func ExtractChronosphereParams(request QueryRequest) map[string]any {
	params := map[string]any{
		"query_type": "SERVICE_OPERATION",
	}

	// Extract time range from filters or use defaults (expanded range for better data availability)
	now := time.Now()
	params["start_time"] = now.Add(-15 * time.Minute).Format(time.RFC3339) // 15 mins ago
	params["end_time"] = now.Format(time.RFC3339)

	// Extract service and other parameters from WHERE clause
	extractFromBinaryClause := func(binary BinaryWhereClause) {
		for column, conditions := range binary {
			for condType, value := range conditions {
				switch column {
				case "service", "workload_name":
					if condType == Eq {
						params["service"] = value
					}
				case "start_time", "timestamp":
					switch condType {
					case Gte:
						params["start_time"] = value
					case Lte:
						params["end_time"] = value
					case Between:
						// Handle _between format: {"_gte": "start", "_lte": "end"}
						if betweenMap, ok := value.(map[string]any); ok {
							if startTime, hasStart := betweenMap["_gte"]; hasStart {
								params["start_time"] = startTime
							}
							if endTime, hasEnd := betweenMap["_lte"]; hasEnd {
								params["end_time"] = endTime
							}
						}
					}
				case "end_time":
					if condType == Lte {
						params["end_time"] = value
					}
				case "query_type":
					if condType == Eq {
						params["query_type"] = value
					}
				case "operation":
					if condType == Eq {
						params["operation"] = value
					}
				case "resource":
					appendTagFilter(params, "http.url", condType, value)
				case "span_name":
					appendTagFilter(params, "cumulative_tag", condType, value)
				case "trace_id", "trace_ids":
					if condType == In || condType == Eq {
						if traceIdsList, ok := value.([]any); ok {
							strTraceIds := make([]string, len(traceIdsList))
							for i, id := range traceIdsList {
								strTraceIds[i] = fmt.Sprintf("%v", id)
							}
							params["trace_ids"] = strTraceIds
						} else if traceIdsSlice, ok := value.([]string); ok {
							params["trace_ids"] = traceIdsSlice
						} else {
							params["trace_ids"] = []string{fmt.Sprintf("%v", value)}
						}
					}
				case "spanattributes", "tags":
					var tagMap map[string]any
					if m, ok := value.(map[string]any); ok {
						tagMap = m
					} else if s, ok := value.(string); ok {
						// Attempt to unmarshal if the value is a JSON string
						_ = json.Unmarshal([]byte(s), &tagMap)
					}

					for k, v := range tagMap {
						appendTagFilter(params, k, condType, v)
					}
				}
			}
		}
	}

	// Process WHERE clause
	if len(request.Where.Binary) > 0 {
		extractFromBinaryClause(request.Where.Binary)
	}

	// Process AND clauses
	for _, andClause := range request.Where.And {
		if len(andClause.Binary) > 0 {
			extractFromBinaryClause(andClause.Binary)
		}
	}

	// Handle service parameter - Chronosphere API might not support "*" wildcard
	if service, exists := params["service"]; exists && service == "*" {
		// Remove wildcard service parameter - let Chronosphere query all services
		delete(params, "service")
	}

	// Set query_type to TRACE_IDS if trace_ids are provided
	if traceIds, exists := params["trace_ids"]; exists {
		if traceIdsList, ok := traceIds.([]string); ok && len(traceIdsList) > 0 {
			params["query_type"] = "TRACE_IDS"
		}
	}

	return params
}

// convertToNumeric tries to convert a value to a numeric type for Chronosphere tag filters
// Returns the numeric value and true if successful, otherwise false
func convertToNumeric(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	case string:
		// Try to parse string as number
		if numVal, err := strconv.ParseFloat(v, 64); err == nil {
			return numVal, true
		}
		return 0, false
	default:
		return 0, false
	}
}

func GetNamespaceColumnDefForTraceProvider(ctx *security.RequestContext, accountId string) string {
	source := getSourceByAccountId(ctx, accountId)
	switch source {
	case database.AgentWarehouseChronosphere:
		// Return the column name that newChronosphereRows produces
		return "workload_namespace"
	case database.AgentWarehouseBigQuery:
		return "span.attributes.attributeMap.source_workload_namespace"
	default:
		return "CASE WHEN mapContains(SpanAttributes, 'source.workload_namespace') THEN SpanAttributes['source.workload_namespace'] WHEN mapContains(ResourceAttributes, 'k8s.namespace.name') THEN ResourceAttributes['k8s.namespace.name'] ELSE ResourceAttributes['service.namespace'] END"
	}
}

func GetServiceColumnDefForTraceProvider(ctx *security.RequestContext, accountId string) string {
	source := getSourceByAccountId(ctx, accountId)
	switch source {
	case database.AgentWarehouseChronosphere:
		// Return the column name that newChronosphereRows produces
		return "workload_name"
	case database.AgentWarehouseBigQuery:
		return "span.attributes.attributeMap.source_workload_name"
	default:
		return "CASE WHEN mapContains(SpanAttributes, 'source.workload_name') THEN SpanAttributes['source.workload_name'] WHEN mapContains(ResourceAttributes, 'k8s.deployment.name') THEN ResourceAttributes['k8s.deployment.name'] ELSE ResourceAttributes['service.name'] END"
	}
}

func GetSpanNameColumnDefForTraceProvider(ctx *security.RequestContext, accountId string) string {
	source := getSourceByAccountId(ctx, accountId)
	switch source {
	case database.AgentWarehouseChronosphere:
		// Return the column name that newChronosphereRows produces
		return "span_name"
	case database.AgentWarehouseBigQuery:
		return "span.displayName.value"
	default:
		return "SpanName"
	}
}

func GetDurationColumnDefForTraceProvider(ctx *security.RequestContext, accountId string) string {
	source := getSourceByAccountId(ctx, accountId)
	switch source {
	case database.AgentWarehouseChronosphere:
		// Return the column name that newChronosphereRows produces
		return "duration_ns"
	case database.AgentWarehouseBigQuery:
		return "TIMESTAMP_DIFF(span.endTime, span.startTime, MICROSECOND) * 1000"
	default:
		return "Duration"
	}
}

// Columns that depend on the event_duplicates JOIN for fingerprint_first_seen_at / fingerprint_event_count
var fingerprintDependentColumns = map[string]bool{
	"fingerprint_first_seen_at": true,
	"fingerprint_event_count":   true,
	"is_new_issue":              true,
	"count_new_issues":          true,
	"count_recurring_issues":    true,
}

// Columns that depend on the event_log_analysis JOIN
var prDependentColumns = map[string]bool{
	"pr_url":   true,
	"pr_title": true,
}

func whereReferencesColumns(where QueryWhereClause, cols map[string]bool) bool {
	for col := range where.Binary {
		if cols[col] {
			return true
		}
	}
	for _, c := range where.And {
		if whereReferencesColumns(c, cols) {
			return true
		}
	}
	for _, c := range where.Or {
		if whereReferencesColumns(c, cols) {
			return true
		}
	}
	if where.Not != nil {
		if whereReferencesColumns(*where.Not, cols) {
			return true
		}
	}
	return false
}

func requestReferencesColumns(request QueryRequest, cols map[string]bool) bool {
	if len(request.Columns) == 0 {
		return true
	}
	for _, col := range request.Columns {
		if cols[col.Name] {
			return true
		}
	}
	for _, ob := range request.OrderBy {
		if cols[ob.Column] {
			return true
		}
	}
	for _, gb := range request.GroupBy {
		if cols[gb] {
			return true
		}
	}
	if whereReferencesColumns(request.Where, cols) {
		return true
	}
	if whereReferencesColumns(request.Having, cols) {
		return true
	}
	return false
}

// extractFilterSQL extracts a filter for the given column from request.Where.Binary,
// generates the SQL fragment (e.g. " AND col = 'val'" or " AND col IN ('a','b')"),
// and removes the filter from the request to avoid redundant outer WHERE.
// The sqlColumn parameter is the actual SQL column name to use in the generated fragment.
func extractFilterSQL(request *QueryRequest, filterName string, sqlColumn string) string {
	dialect := &postgresDialect{}
	filter, ok := request.Where.Binary[filterName]
	if !ok {
		return ""
	}
	var sql string
	if eqVal, ok := filter[Eq]; ok {
		sql = " AND " + sqlColumn + " = " + dialect.QuoteLiteral(eqVal)
	} else if inVal, ok := filter[In]; ok {
		if vals, ok := inVal.([]any); ok && len(vals) > 0 {
			quoted := make([]string, len(vals))
			for i, v := range vals {
				quoted[i] = dialect.QuoteLiteral(v)
			}
			sql = " AND " + sqlColumn + " IN (" + strings.Join(quoted, ",") + ")"
		}
	}
	if sql != "" {
		delete(request.Where.Binary, filterName)
	}
	return sql
}

var table_metadata = map[string]TableDefinition{
	"k8s_cluster_groupings_v2": {
		Type:   Normal,
		Source: database.Metastore,
		Name:   "k8s_cluster_groupings_v2",
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			accountFilter := extractFilterSQL(&request, "account_id", "ksn.cloud_account_id")
			// Build matching filters for workload and pod subqueries using same account
			workloadAccountFilter := strings.Replace(accountFilter, "ksn.cloud_account_id", "ksw.cloud_account_id", 1)
			podAccountFilter := strings.Replace(accountFilter, "ksn.cloud_account_id", "ksp.cloud_account_id", 1)

			def := `
		(
			SELECT
				ksn.cloud_account_id AS account_id,
				ksn.tenant_id,
				node_count,
				node_spot_count,
				node_cpu_capacity,
				node_cpu_allocatable,
				node_memory_capacity,
				node_memory_allocatable,
				workload_type_counts,
				pod_status_counts
			FROM (
				SELECT
					ksn.cloud_account_id,
					ksn.tenant_id,
					COUNT(*) AS node_count,
					SUM(CASE WHEN ksn.node_type = 'spot' THEN 1 ELSE 0 END) AS node_spot_count,
					SUM(ksn.cpu_capacity) AS node_cpu_capacity,
					SUM(ksn.cpu_allocatable) AS node_cpu_allocatable,
					SUM(ksn.memory_capacity) AS node_memory_capacity,
					SUM(ksn.memory_allocatable) AS node_memory_allocatable
				FROM
					k8s_nodes ksn
				WHERE
					ksn.is_active = TRUE` + accountFilter + `
				GROUP BY
					ksn.cloud_account_id,
					ksn.tenant_id
			) AS ksn
			JOIN (
				SELECT
					ksw.cloud_account_id,
					ksw.tenant_id,
					jsonb_object_agg(ksw.kind, workload_count) AS workload_type_counts
				FROM (
					SELECT
						ksw.cloud_account_id,
						ksw.tenant_id,
						ksw.kind,
						COUNT(*) AS workload_count
					FROM
						k8s_workloads ksw
					WHERE
						ksw.is_active` + workloadAccountFilter + `
					GROUP BY
						ksw.cloud_account_id,
						ksw.tenant_id,
						ksw.kind
				) AS ksw
				GROUP BY
					ksw.cloud_account_id,
					ksw.tenant_id
			) AS ksw
			USING (cloud_account_id, tenant_id)
			JOIN (
				SELECT
					ksp.cloud_account_id,
					ksp.tenant_id,
					jsonb_object_agg(ksp.status, status_count) AS pod_status_counts
				FROM (
					SELECT
						ksp.cloud_account_id,
						ksp.tenant_id,
						ksp.status,
						COUNT(*) AS status_count
					FROM
						k8s_pods ksp
					WHERE
						ksp.is_active` + podAccountFilter + `
					GROUP BY
						ksp.cloud_account_id,
						ksp.tenant_id,
						ksp.status
				) AS ksp
				GROUP BY
					ksp.cloud_account_id,
					ksp.tenant_id
			) AS ksp
			USING (cloud_account_id, tenant_id)
		) as kcg2`
			return def, request, nil
		},
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
			},
			"node_count": {
				Type: ColumnDefinitionTypeInt,
			},
			"node_spot_count": {
				Type: ColumnDefinitionTypeInt,
			},
			"node_cpu_capacity": {
				Type: ColumnDefinitionTypeFloat,
			},
			"node_cpu_allocatable": {
				Type: ColumnDefinitionTypeFloat,
			},
			"node_memory_capacity": {
				Type: ColumnDefinitionTypeFloat,
			},
			"node_memory_allocatable": {
				Type: ColumnDefinitionTypeFloat,
			},
			"workload_type_counts": {
				Type: ColumnDefinitionTypeJson,
			},
			"pod_status_counts": {
				Type: ColumnDefinitionTypeJson,
			},
		},
	},
	"k8s_metrics_groupings_v2": {
		Type:                Aggregate,
		Source:              getSource("k8s_metrics_groupings_v2"),
		Def:                 "cloud_resource_metrics",
		Name:                "k8s_metrics_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		NamespaceColumnName: "namespace_name",
		UpdateFilters: func(ctx *security.RequestContext, request QueryRequest) (QueryRequest, error) {
			metricsType := []string{}
			for _, c := range request.Columns {
				switch c.Name {
				case "cost":
					metricsType = append(metricsType, "totalCost")
				case "avg_efficiency", "max_efficiency":
					metricsType = append(metricsType, "totalEfficiency")
				case "avg_cpu_used", "max_cpu_used":
					metricsType = append(metricsType, "cpuCoreUsageAverage")
				case "avg_memory_used", "max_memory_used":
					metricsType = append(metricsType, "ramByteUsageAverage")
				case "avg_cpu_request", "max_cpu_request":
					metricsType = append(metricsType, "cpuCoreRequestAverage")
				case "avg_memory_request", "max_memory_request":
					metricsType = append(metricsType, "ramByteRequestAverage")
				case "avg_cpu_efficiency", "max_cpu_efficiency":
					metricsType = append(metricsType, "cpuEfficiency")
				case "avg_ram_efficiency", "max_ram_efficiency":
					metricsType = append(metricsType, "ramEfficiency")
				case "sum_ingress":
					metricsType = append(metricsType, "networkReceiveBytes")
				case "sum_egress":
					metricsType = append(metricsType, "networkTransferBytes")
				}
			}
			if len(metricsType) > 0 {
				request.Where.And = append(request.Where.And, QueryWhereClause{
					Binary: BinaryWhereClause{
						"metric": {
							In: metricsType,
						},
					},
				})
			}
			return request, nil
		},
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"workload_type": {
				Type: ColumnDefinitionTypeString,
				Def:  "tags ->> 'controllerKind'",
			},
			"workload_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "tags ->> 'controller'",
			},
			"namespace_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "tags ->> 'namespace'",
			},
			"pod_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "tags ->> 'name'",
			},
			"node_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "tags ->> 'node'",
			},
			"workload_fqdn": {
				Type: ColumnDefinitionTypeString,
				Def:  "tags ->> 'workload_fqdn'",
			},
			"pod_fqdn": {
				Type: ColumnDefinitionTypeString,
				Def:  "tags ->> 'pod_fqdn'",
			},
			"timestamp": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"metric": {
				Type: ColumnDefinitionTypeString,
			},
			"cost": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(case when metric = 'totalCost' then value end)",
				IsAggregated: true,
			},
			"avg_efficiency": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(case when metric = 'totalEfficiency' then value end)",
				IsAggregated: true,
			},
			"max_efficiency": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(case when metric = 'totalEfficiency' then value end)",
				IsAggregated: true,
			},
			"avg_cpu_used": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(case when metric = 'cpuCoreUsageAverage' then value end)",
				IsAggregated: true,
			},
			"max_cpu_used": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(case when metric = 'cpuCoreUsageAverage' then value end)",
				IsAggregated: true,
			},
			"avg_memory_used": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(case when metric = 'ramByteUsageAverage' then value end)",
				IsAggregated: true,
			},
			"max_memory_used": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(case when metric = 'ramByteUsageAverage' then value end)",
				IsAggregated: true,
			},
			"avg_cpu_request": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(case when metric = 'cpuCoreRequestAverage' then value end)",
				IsAggregated: true,
			},
			"max_cpu_request": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(case when metric = 'cpuCoreRequestAverage' then value end)",
				IsAggregated: true,
			},
			"avg_memory_request": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(case when metric = 'ramByteRequestAverage' then value end)",
				IsAggregated: true,
			},
			"max_memory_request": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(case when metric = 'ramByteRequestAverage' then value end)",
				IsAggregated: true,
			},
			"avg_cpu_efficiency": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(case when metric = 'cpuEfficiency' then value end)",
				IsAggregated: true,
			},
			"max_cpu_efficiency": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(case when metric = 'cpuEfficiency' then value end)",
				IsAggregated: true,
			},
			"avg_ram_efficiency": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(case when metric = 'ramEfficiency' then value end)",
				IsAggregated: true,
			},
			"max_ram_efficiency": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(case when metric = 'ramEfficiency' then value end)",
				IsAggregated: true,
			},
			"sum_ingress": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(case when metric = 'networkReceiveBytes' then value end)",
				IsAggregated: true,
			},
			"sum_egress": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(case when metric = 'networkTransferBytes' then value end)",
				IsAggregated: true,
			},
		},
	},
	"metric_groupings_v2": {
		Type:                Aggregate,
		Source:              getSource("metric_groupings_v2"),
		Def:                 "cloud_resource_metrics",
		Name:                "metric_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"resource_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_resource_id",
			},
			"metric": {
				Type: ColumnDefinitionTypeString,
				Def:  "metric",
			},
			"timestamp": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "timestamp",
			},
			"count_value": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
			"sum_value": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(value)",
				IsAggregated: true,
			},
			"avg_value": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(value)",
				IsAggregated: true,
			},
			"min_value": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "min(value)",
				IsAggregated: true,
			},
			"max_value": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(value)",
				IsAggregated: true,
			},
		},
	},
	"metrics_v2": {
		Type:                Normal,
		Def:                 "cloud_resource_metrics",
		Name:                "metrics_v2",
		Source:              getSource("metrics_v2"),
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "cloud_account_id",
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
			},
			"cloud_account_id": {
				Type: ColumnDefinitionTypeString,
			},
			"cloud_resource_id": {
				Type: ColumnDefinitionTypeString,
			},
			"metric": {
				Type: ColumnDefinitionTypeString,
			},
			"metric_type": {
				Type: ColumnDefinitionTypeString,
			},
			"timestamp": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"value": {
				Type: ColumnDefinitionTypeFloat,
			},
			"tags": {
				Type: ColumnDefinitionTypeJson,
			},
		},
	},
	"event_groupings_v2": {
		Type:   Aggregate,
		Source: getSource("event_groupings_v2"),
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			if requestReferencesColumns(request, fingerprintDependentColumns) {
				return "events LEFT JOIN event_duplicates ed ON ed.event_id = events.id AND ed.cloud_account_id = events.cloud_account_id", request, nil
			}
			return "events", request, nil
		},
		Name:                "event_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		NamespaceColumnName: "subject_namespace",
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "events.id",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "events.tenant",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "events.cloud_account_id",
			},
			"cluster": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_resource_id",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
			},
			"service_key": {
				Type: ColumnDefinitionTypeString,
			},
			"subject_node": {
				Type: ColumnDefinitionTypeString,
			},
			"subject_namespace": {
				Type: ColumnDefinitionTypeString,
			},
			"subject_name": {
				Type: ColumnDefinitionTypeString,
			},
			"subject_type": {
				Type: ColumnDefinitionTypeString,
			},
			"subject_owner": {
				Type: ColumnDefinitionTypeString,
				Def:  "COALESCE(subject_owner, subject_name, '')",
			},
			"priority": {
				Type: ColumnDefinitionTypeString,
			},
			"category": {
				Type: ColumnDefinitionTypeString,
			},
			"finding_type": {
				Type: ColumnDefinitionTypeString,
			},
			"aggregation_key": {
				Type: ColumnDefinitionTypeString,
			},
			"source": {
				Type: ColumnDefinitionTypeString,
			},
			"fingerprint": {
				Type: ColumnDefinitionTypeString,
				Def:  "events.fingerprint",
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "events.created_at",
			},
			"starts_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "events.starts_at",
			},
			"ends_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"principal": {
				Type: ColumnDefinitionTypeString,
			},
			"title": {
				Type: ColumnDefinitionTypeString,
			},
			"finding_id": {
				Type: ColumnDefinitionTypeString,
			},
			"latest_event_id": {
				Type:         ColumnDefinitionTypeString,
				Def:          "(array_agg(events.id ORDER BY events.created_at DESC))[1]",
				IsAggregated: true,
			},
			"latest_computed_score": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "(array_agg(computed_score ORDER BY events.created_at DESC))[1]",
				IsAggregated: true,
			},
			"latest_score_factors": {
				Type:         ColumnDefinitionTypeJson,
				Def:          "(array_agg(score_factors ORDER BY events.created_at DESC))[1]",
				IsAggregated: true,
			},
			"latest_score_confidence": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "cast((array_agg(score_confidence ORDER BY events.created_at DESC))[1] as double precision)",
				IsAggregated: true,
			},
			"latest_computed_priority": {
				Type:         ColumnDefinitionTypeString,
				Def:          "(array_agg(computed_priority ORDER BY events.created_at DESC))[1]",
				IsAggregated: true,
			},
			"latest_nb_status": {
				Type:         ColumnDefinitionTypeString,
				Def:          "(array_agg(nb_status ORDER BY CASE nb_status WHEN 'ACTION_REQUIRED' THEN 1 WHEN 'OPEN' THEN 2 WHEN 'SNOOZED' THEN 3 WHEN 'ACKNOWLEDGED' THEN 4 WHEN 'INVESTIGATING' THEN 5 WHEN 'DUPLICATE' THEN 6 WHEN 'SUPPRESSED' THEN 7 WHEN 'DROPPED' THEN 8 WHEN 'RESOLVED' THEN 9 ELSE 10 END, events.created_at DESC))[1]",
				IsAggregated: true,
			},
			"latest_title": {
				Type:         ColumnDefinitionTypeString,
				Def:          "(array_agg(title ORDER BY events.created_at DESC))[1]",
				IsAggregated: true,
			},
			"max_created_at": {
				Type:         ColumnDefinitionTypeDatetime,
				Def:          "max(events.created_at)",
				IsAggregated: true,
			},
			"min_created_at": {
				Type:         ColumnDefinitionTypeDatetime,
				Def:          "min(events.created_at)",
				IsAggregated: true,
			},
			"event_count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
			"count_subject_name": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(distinct subject_name)",
				IsAggregated: true,
			},
			"count_aggregation_key": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(distinct aggregation_key)",
				IsAggregated: true,
			},
			"distinct_status": {
				Type:         "json",
				Def:          "jsonb_agg(distinct status)::text",
				IsAggregated: true,
			},
			"distinct_priority": {
				Type:         "json",
				Def:          "jsonb_agg(distinct priority)::text",
				IsAggregated: true,
			},
			"distinct_aggregation_key": {
				Type:         "json",
				Def:          "jsonb_agg(distinct aggregation_key)::text",
				IsAggregated: true,
			},
			"distinct_subject_name": {
				Type:         "json",
				Def:          "jsonb_agg(distinct subject_name)::text",
				IsAggregated: true,
			},
			"distinct_subject_namespace": {
				Type:         "json",
				Def:          "jsonb_agg(distinct subject_namespace)::text",
				IsAggregated: true,
			},
			"count_priority_high": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when priority = 'HIGH' then 1 end)",
				IsAggregated: true,
			},
			"count_priority_medium": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when priority = 'MEDIUM' then 1 end)",
				IsAggregated: true,
			},
			"count_priority_low": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when priority = 'LOW' then 1 end)",
				IsAggregated: true,
			},
			"count_priority_debug": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when priority = 'DEBUG' then 1 end)",
				IsAggregated: true,
			},
			"count_priority_info": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when priority = 'INFO' then 1 end)",
				IsAggregated: true,
			},
			"count_application_issues": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when aggregation_key in ('HighErrorCriticalLogs', 'ApplicationAPIFailures') and finding_type = 'issue' then 1 end)",
				IsAggregated: true,
			},
			"count_node_issues": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when subject_type = 'node'  and finding_type = 'issue' then 1 end)",
				IsAggregated: true,
			},
			"count_pod_issues": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when subject_type = 'pod' and aggregation_key not in ('HighErrorCriticalLogs', 'ApplicationAPIFailures')  and finding_type = 'issue' then 1 end)",
				IsAggregated: true,
			},
			"labels": {
				Type: ColumnDefinitionTypeJson,
			},
			"computed_score": {
				Type: ColumnDefinitionTypeInt,
			},
			"computed_priority": {
				Type: ColumnDefinitionTypeString,
			},
			"score_factors": {
				Type: ColumnDefinitionTypeJson,
			},
			"score_confidence": {
				Type: ColumnDefinitionTypeFloat,
			},
			"nb_status": {
				Type: ColumnDefinitionTypeString,
			},
			"snoozed_until": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"nb_status_changed_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"nb_status_changed_by": {
				Type: ColumnDefinitionTypeString,
			},
			"max_computed_score": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "max(computed_score)",
				IsAggregated: true,
			},
			"count_priority_p0": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when computed_priority = 'P0' then 1 end)",
				IsAggregated: true,
			},
			"count_priority_p1": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when computed_priority = 'P1' then 1 end)",
				IsAggregated: true,
			},
			"count_priority_p2": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when computed_priority = 'P2' then 1 end)",
				IsAggregated: true,
			},
			"count_priority_p3": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when computed_priority = 'P3' then 1 end)",
				IsAggregated: true,
			},
			"count_new_issues": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(DISTINCT CASE WHEN ed.absolute_first_seen_at > NOW() - INTERVAL '7 days' THEN events.fingerprint END)",
				IsAggregated: true,
			},
			"count_recurring_issues": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(DISTINCT CASE WHEN ed.absolute_first_seen_at <= NOW() - INTERVAL '7 days' THEN events.fingerprint END)",
				IsAggregated: true,
			},
			"fingerprint_first_seen_at": {
				Type:         ColumnDefinitionTypeDatetime,
				Def:          "min(ed.absolute_first_seen_at)",
				IsAggregated: true,
			},
			"fingerprint_event_count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "max(ed.occurrence_number)",
				IsAggregated: true,
			},
			"is_new_issue": {
				Type: ColumnDefinitionTypeBoolean,
				Def:  "CASE WHEN ed.absolute_first_seen_at > NOW() - INTERVAL '7 days' THEN true ELSE false END",
			},
		},
	},
	"events_v2": {
		Type:   Normal,
		Source: getSource("events_v2"),
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			needsPr := requestReferencesColumns(request, prDependentColumns)
			needsFingerprint := requestReferencesColumns(request, fingerprintDependentColumns)

			switch {
			case needsPr && needsFingerprint:
				return `(SELECT e.*, ela.analysis::jsonb->'automated_fix_pr'->>'url' as pr_url,
					ela.analysis::jsonb->'automated_fix_pr'->>'title' as pr_title,
					ed.absolute_first_seen_at as fingerprint_first_seen_at
					FROM events e LEFT JOIN event_log_analysis ela
					ON ela.event_id = e.id
					AND ela.analysis_type = 'log_analysis'
					AND ela.status = 'COMPLETED'
					AND ela.analysis LIKE '{%'
					AND ela.analysis::jsonb->'automated_fix_pr'->>'url' != ''
					LEFT JOIN event_duplicates ed ON ed.event_id = e.id AND ed.cloud_account_id = e.cloud_account_id) as events`, request, nil
			case needsPr:
				return `(SELECT e.*, ela.analysis::jsonb->'automated_fix_pr'->>'url' as pr_url,
					ela.analysis::jsonb->'automated_fix_pr'->>'title' as pr_title
					FROM events e LEFT JOIN event_log_analysis ela
					ON ela.event_id = e.id
					AND ela.analysis_type = 'log_analysis'
					AND ela.status = 'COMPLETED'
					AND ela.analysis LIKE '{%'
					AND ela.analysis::jsonb->'automated_fix_pr'->>'url' != '') as events`, request, nil
			case needsFingerprint:
				return `(SELECT e.*, ed.absolute_first_seen_at as fingerprint_first_seen_at
					FROM events e
					LEFT JOIN event_duplicates ed ON ed.event_id = e.id AND ed.cloud_account_id = e.cloud_account_id) as events`, request, nil
			default:
				return "events", request, nil
			}
		},
		Name:                "events_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		NamespaceColumnName: "subject_namespace",
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"updated_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"finding_id": {
				Type: ColumnDefinitionTypeString,
			},
			"title": {
				Type: ColumnDefinitionTypeString,
			},
			"description": {
				Type: ColumnDefinitionTypeString,
			},
			"source": {
				Type: ColumnDefinitionTypeString,
			},
			"aggregation_key": {
				Type: ColumnDefinitionTypeString,
			},
			"failure": {
				Type: ColumnDefinitionTypeString,
			},
			"finding_type": {
				Type: ColumnDefinitionTypeString,
			},
			"category": {
				Type: ColumnDefinitionTypeString,
			},
			"priority": {
				Type: ColumnDefinitionTypeString,
			},
			"subject_type": {
				Type: ColumnDefinitionTypeString,
			},
			"subject_name": {
				Type: ColumnDefinitionTypeString,
			},
			"subject_namespace": {
				Type: ColumnDefinitionTypeString,
			},
			"subject_node": {
				Type: ColumnDefinitionTypeString,
			},
			"subject_owner": {
				Type: ColumnDefinitionTypeString,
			},
			"service_key": {
				Type: ColumnDefinitionTypeString,
			},
			"cluster": {
				Type: ColumnDefinitionTypeString,
			},
			"ends_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"starts_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"fingerprint": {
				Type: ColumnDefinitionTypeString,
			},
			"evidences": {
				Type: ColumnDefinitionTypeString,
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"resource_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_resource_id",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
			},
			"principal": {
				Type: ColumnDefinitionTypeString,
			},
			"labels": {
				Type: ColumnDefinitionTypeJson,
			},
			"urgency": {
				Type: ColumnDefinitionTypeString,
			},
			"computed_score": {
				Type: ColumnDefinitionTypeInt,
			},
			"computed_priority": {
				Type: ColumnDefinitionTypeString,
			},
			"score_factors": {
				Type: ColumnDefinitionTypeJson,
			},
			"score_confidence": {
				Type: ColumnDefinitionTypeFloat,
			},
			"nb_status": {
				Type: ColumnDefinitionTypeString,
			},
			"snoozed_until": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"nb_status_changed_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"nb_status_changed_by": {
				Type: ColumnDefinitionTypeString,
			},
			"pr_url": {
				Type: ColumnDefinitionTypeString,
			},
			"pr_title": {
				Type: ColumnDefinitionTypeString,
			},
			"fingerprint_first_seen_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"is_new_issue": {
				Type: ColumnDefinitionTypeBoolean,
				Def:  "CASE WHEN fingerprint_first_seen_at > NOW() - INTERVAL '7 days' THEN true ELSE false END",
			},
		},
	},
	"dw_queries_v2": {
		Type:                Normal,
		Def:                 "dw_queries",
		Name:                "dw_queries_v2",
		Source:              getSource("dw_queries_v2"),
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_id": {
				Type: ColumnDefinitionTypeString,
			},
			"tags": {
				Type: ColumnDefinitionTypeJson,
			},
			"database_name": {
				Type: ColumnDefinitionTypeString,
			},
			"db_username": {
				Type: ColumnDefinitionTypeString,
			},
			"query_type": {
				Type: ColumnDefinitionTypeString,
			},
			"query_exec_duration_micro": {
				Type: ColumnDefinitionTypeInt,
			},
			"bill_total_duration_micro": {
				Type: ColumnDefinitionTypeInt,
			},
			"bill_interval_from": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"bill_interval_to": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"bill": {
				Type: ColumnDefinitionTypeFloat,
			},
			"rpu": {
				Type: ColumnDefinitionTypeFloat,
			},
			"query_text": {
				Type: ColumnDefinitionTypeString,
			},
			"query_planning_duration_micro": {
				Type: ColumnDefinitionTypeInt,
			},
			"query_error_message": {
				Type: ColumnDefinitionTypeString,
			},
			"query_returned_rows": {
				Type: ColumnDefinitionTypeInt,
			},
			"query_returned_bytes": {
				Type: ColumnDefinitionTypeInt,
			},
			"query_usage_limit": {
				Type: ColumnDefinitionTypeString,
			},
			"query_transaction_id": {
				Type: ColumnDefinitionTypeString,
			},
			"query_session_id": {
				Type: ColumnDefinitionTypeString,
			},
			"query_status": {
				Type: ColumnDefinitionTypeString,
			},
			"query_result_cache_hit": {
				Type: ColumnDefinitionTypeBoolean,
			},
			"query_started_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"query_ended_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"query_id": {
				Type: ColumnDefinitionTypeString,
			},
			"query_queue_duration_micro": {
				Type: ColumnDefinitionTypeInt,
			},
			"query_normalized": {
				Type: ColumnDefinitionTypeString,
			},
			"query_normalized_md5": {
				Type: ColumnDefinitionTypeString,
			},
			"queue_provision_time": {
				Type: ColumnDefinitionTypeFloat,
			},
			"queue_repair_time": {
				Type: ColumnDefinitionTypeFloat,
			},
			"queue_overload_time": {
				Type: ColumnDefinitionTypeFloat,
			},
			"partitions_scanned": {
				Type: ColumnDefinitionTypeFloat,
			},
			"bytes_scanned": {
				Type: ColumnDefinitionTypeFloat,
			},
			"bytes_spilled_locally": {
				Type: ColumnDefinitionTypeFloat,
			},
			"bytes_spilled_remotely": {
				Type: ColumnDefinitionTypeFloat,
			},
			"transaction_block_time": {
				Type: ColumnDefinitionTypeFloat,
			},
			"table_names": {
				Type: "array",
			},
			"query_remote_ip": {
				Type: ColumnDefinitionTypeString,
			},
			"query_md5": {
				Type: ColumnDefinitionTypeString,
			},
			"warehouse_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "tags ->> 'warehouse_name'",
			},
		},
	},
	"dw_query_groupings_v2": {
		Type:                Aggregate,
		Source:              getSource("dw_query_groupings_v2"),
		Def:                 "dw_queries",
		Name:                "dw_query_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_id": {
				Type: ColumnDefinitionTypeString,
			},
			"database_name": {
				Type: ColumnDefinitionTypeString,
			},
			"db_username": {
				Type: ColumnDefinitionTypeString,
			},
			"query_remote_ip": {
				Type: ColumnDefinitionTypeString,
			},
			"query_type": {
				Type: ColumnDefinitionTypeString,
			},
			"query_status": {
				Type: ColumnDefinitionTypeString,
			},
			"warehouse_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "tags ->> 'warehouse_name'",
			},
			"query_normalized": {
				Type: ColumnDefinitionTypeString,
			},
			"query_normalized_md5": {
				Type: ColumnDefinitionTypeString,
			},
			"query_text": {
				Type: ColumnDefinitionTypeString,
			},
			"query_started_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"query_exec_duration_micro": {
				Type: ColumnDefinitionTypeFloat,
			},
			"avg_query_exec_duration_micro": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(query_exec_duration_micro)",
				IsAggregated: true,
			},
			"max_query_exec_duration_micro": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(query_exec_duration_micro)",
				IsAggregated: true,
			},
			"sum_query_exec_duration_micro": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(query_exec_duration_micro)",
				IsAggregated: true,
			},
			"bill": {
				Type: ColumnDefinitionTypeFloat,
			},
			"avg_bill": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(bill)",
				IsAggregated: true,
			},
			"max_bill": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(bill)",
				IsAggregated: true,
			},
			"sum_bill": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(bill)",
				IsAggregated: true,
			},
			"bytes_spilled_locally": {
				Type: ColumnDefinitionTypeFloat,
			},
			"avg_bytes_spilled_locally": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(bytes_spilled_locally)",
				IsAggregated: true,
			},
			"max_bytes_spilled_locally": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(bytes_spilled_locally)",
				IsAggregated: true,
			},
			"sum_bytes_spilled_locally": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(bytes_spilled_locally)",
				IsAggregated: true,
			},
			"bytes_spilled_remotely": {
				Type: ColumnDefinitionTypeFloat,
			},
			"avg_bytes_spilled_remotely": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(bytes_spilled_remotely)",
				IsAggregated: true,
			},
			"max_bytes_spilled_remotely": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(bytes_spilled_remotely)",
				IsAggregated: true,
			},
			"sum_bytes_spilled_remotely": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(bytes_spilled_remotely)",
				IsAggregated: true,
			},
			"bytes_scanned": {
				Type: ColumnDefinitionTypeFloat,
			},
			"avg_bytes_scanned": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(bytes_scanned)",
				IsAggregated: true,
			},
			"max_bytes_scanned": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(bytes_scanned)",
				IsAggregated: true,
			},
			"sum_bytes_scanned": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(bytes_scanned)",
				IsAggregated: true,
			},
			"partitions_scanned": {
				Type: ColumnDefinitionTypeFloat,
			},
			"avg_partitions_scanned": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(partitions_scanned)",
				IsAggregated: true,
			},
			"max_partitions_scanned": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(partitions_scanned)",
				IsAggregated: true,
			},
			"sum_partitions_scanned": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(partitions_scanned)",
				IsAggregated: true,
			},
			"query_planning_duration_micro": {
				Type: ColumnDefinitionTypeFloat,
			},
			"avg_query_planning_duration_micro": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(query_planning_duration_micro)",
				IsAggregated: true,
			},
			"max_query_planning_duration_micro": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(query_planning_duration_micro)",
				IsAggregated: true,
			},
			"sum_query_planning_duration_micro": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(query_planning_duration_micro)",
				IsAggregated: true,
			},
			"query_queue_duration_micro": {
				Type: ColumnDefinitionTypeFloat,
			},
			"avg_query_queue_duration_micro": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(query_queue_duration_micro)",
				IsAggregated: true,
			},
			"max_query_queue_duration_micro": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(query_queue_duration_micro)",
				IsAggregated: true,
			},
			"sum_query_queue_duration_micro": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(query_queue_duration_micro)",
				IsAggregated: true,
			},
			"query_returned_bytes": {
				Type: ColumnDefinitionTypeFloat,
			},
			"avg_query_returned_bytes": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(query_returned_bytes)",
				IsAggregated: true,
			},
			"max_query_returned_bytes": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(query_returned_bytes)",
				IsAggregated: true,
			},
			"sum_query_returned_bytes": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(query_returned_bytes)",
				IsAggregated: true,
			},
			"query_returned_rows": {
				Type: ColumnDefinitionTypeFloat,
			},
			"avg_query_returned_rows": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(query_returned_rows)",
				IsAggregated: true,
			},
			"max_query_returned_rows": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(query_returned_rows)",
				IsAggregated: true,
			},
			"sum_query_returned_rows": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(query_returned_rows)",
				IsAggregated: true,
			},
			"rpu": {
				Type: ColumnDefinitionTypeFloat,
			},
			"avg_rpu": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(rpu)",
				IsAggregated: true,
			},
			"max_rpu": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(rpu)",
				IsAggregated: true,
			},
			"sum_rpu": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(rpu)",
				IsAggregated: true,
			},
			"max_query_started_at": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(query_started_at)",
				IsAggregated: true,
			},
			"min_query_started_at": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "min(query_started_at)",
				IsAggregated: true,
			},
			"query_count": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "count(*)",
				IsAggregated: true,
			},
			"db_username_list": {
				Type:         "list",
				Def:          "groupArray(distinct db_username)",
				IsAggregated: true,
			},
			"query_remote_ip_list": {
				Type:         "list",
				Def:          "groupArray(distinct query_remote_ip)",
				IsAggregated: true,
			},
			"database_name_list": {
				Type:         "list",
				Def:          "groupArray(distinct database_name)",
				IsAggregated: true,
			},
		},
	},
	"spends_v2": {
		Type:                Normal,
		Def:                 "spends",
		Name:                "spends_v2",
		Source:              getSource("spends_v2"),
		TenantIdColumnName:  "tenant",
		AccountIdColumnName: "cloud_account",
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
			},
			"date": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"amount": {
				Type: ColumnDefinitionTypeFloat,
			},
			"unit": {
				Type: ColumnDefinitionTypeString,
			},
			"business_unit": {
				Type: ColumnDefinitionTypeString,
			},
			"tenant": {
				Type: ColumnDefinitionTypeString,
			},
			"cloud_account": {
				Type: ColumnDefinitionTypeString,
			},
			"cloud_resource_id": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_service_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "tags -> 'nb_service_name' ->> 0",
			},
			"resource_region": {
				Type: ColumnDefinitionTypeString,
				Def:  "tags -> 'nb_resource_region_code' ->> 0",
			},
			"resource_type": {
				Type: ColumnDefinitionTypeString,
				Def:  "tags -> 'nb_resource_type' ->> 0",
			},
			"exclude_aggregate": {
				Type: ColumnDefinitionTypeBoolean,
			},
			"tags": {
				Type: ColumnDefinitionTypeJson,
			},
		},
	},
	"spend_groupings_v2": {
		Type:                Aggregate,
		Source:              getSource("spend_groupings_v2"),
		Def:                 "spends",
		Name:                "spend_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account",
			},
			"resource_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_resource_id",
			},
			"resource_service_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "tags -> 'nb_service_name' ->> 0",
			},
			"resource_region": {
				Type: ColumnDefinitionTypeString,
				Def:  "tags -> 'nb_resource_region_code' ->> 0",
			},
			"resource_type": {
				Type: ColumnDefinitionTypeString,
				Def:  "tags -> 'nb_resource_type' ->> 0",
			},
			"spend_date": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "date",
			},
			"exclude_aggregate": {
				Type: ColumnDefinitionTypeBoolean,
			},
			"amount": {
				Type: ColumnDefinitionTypeFloat,
			},
			"spend_amount": {
				Type:         ColumnDefinitionTypeFloat,
				IsAggregated: true,
				Def:          "sum(amount)",
			},
			"spend_count": {
				Type:         ColumnDefinitionTypeFloat,
				IsAggregated: true,
				Def:          "count(*)",
			},
			"resource_count": {
				Type:         ColumnDefinitionTypeFloat,
				IsAggregated: true,
				Def:          "count(distinct cloud_resource_id)",
			},
			"account_count": {
				Type:         ColumnDefinitionTypeFloat,
				IsAggregated: true,
				Def:          "count(distinct cloud_account)",
			},
			"currency_type": {
				Type: ColumnDefinitionTypeString,
				Def:  "unit",
			},
		},
	},
	"audits_v2": {
		Type:                Normal,
		Source:              getSource("audits_v2"),
		Def:                 "audit",
		Name:                "audits_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		UpdateFilters: func(ctx *security.RequestContext, request QueryRequest) (QueryRequest, error) {
			if binaryClause, ok := request.Where.Binary["username"]; ok {
				if usernameAny, ok := binaryClause[Eq]; ok {
					username := usernameAny.(string)
					var userId string
					if username == "SYSTEM" {
						userId = "00000000-0000-0000-0000-000000000000"
					} else {
						manager, err := database.GetDatabaseManager(database.Metastore)
						if err != nil {
							return request, err
						}
						err = manager.Db.Get(&userId, "SELECT id FROM users WHERE username = $1", username)
						if err != nil {
							return request, fmt.Errorf("user not found: %s", username)
						}
					}
					delete(request.Where.Binary, "username")
					request.Where.Binary["user_id"] = map[BinaryWhereClauseType]any{
						Eq: userId,
					}
				}
			}
			return request, nil
		},
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"user_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "user_id",
			},
			"username": {
				Type: ColumnDefinitionTypeString,
				Def:  "user_id",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_id",
			},
			"event_time": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"event_category": {
				Type: ColumnDefinitionTypeString,
			},
			"event_type": {
				Type: ColumnDefinitionTypeString,
			},
			"event_prev_state": {
				Type: ColumnDefinitionTypeString,
			},
			"event_state": {
				Type: ColumnDefinitionTypeString,
			},
			"event_actor": {
				Type: ColumnDefinitionTypeString,
			},
			"event_target": {
				Type: ColumnDefinitionTypeString,
			},
			"event_action": {
				Type: ColumnDefinitionTypeString,
			},
			"event_status": {
				Type: ColumnDefinitionTypeString,
			},
			"transaction_id": {
				Type: ColumnDefinitionTypeString,
			},
			"event_attr": {
				Type: ColumnDefinitionTypeJson,
			},
		},
	},
	"audit_groupings_v2": {
		Type:                Aggregate,
		Source:              getSource("audit_groupings_v2"),
		Def:                 "audit",
		Name:                "audits_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		UpdateFilters: func(ctx *security.RequestContext, request QueryRequest) (QueryRequest, error) {
			if binaryClause, ok := request.Where.Binary["username"]; ok {
				if usernameAny, ok := binaryClause[Eq]; ok {
					username := usernameAny.(string)
					var userId string
					if username == "SYSTEM" {
						userId = "00000000-0000-0000-0000-000000000000"
					} else {
						manager, err := database.GetDatabaseManager(database.Metastore)
						if err != nil {
							return request, err
						}
						err = manager.Db.Get(&userId, "SELECT id FROM users WHERE username = $1", username)
						if err != nil {
							return request, fmt.Errorf("user not found: %s", username)
						}
					}
					delete(request.Where.Binary, "username")
					request.Where.Binary["user_id"] = map[BinaryWhereClauseType]any{
						Eq: userId,
					}
				}
			}
			return request, nil
		},
		Columns: map[string]ColumnDefinition{
			"user_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "user_id",
			},
			"username": {
				Type: ColumnDefinitionTypeString,
				Def:  "user_id",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_id",
			},
			"event_time": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"event_category": {
				Type: ColumnDefinitionTypeString,
			},
			"event_type": {
				Type: ColumnDefinitionTypeString,
			},
			"event_actor": {
				Type: ColumnDefinitionTypeString,
			},
			"event_target": {
				Type: ColumnDefinitionTypeString,
			},
			"event_action": {
				Type: ColumnDefinitionTypeString,
			},
			"event_status": {
				Type: ColumnDefinitionTypeString,
			},
			"transaction_id": {
				Type: ColumnDefinitionTypeString,
			},
			"count": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"traces_v2": {
		Type: Normal,
		SourceGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) database.DatabaseManagerType {
			return getSourceByAccountId(ctx, accountId)
		},
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			source := getSourceByAccountId(ctx, accountId)
			if source == database.AgentWarehouseChronosphere {
				// For Chronosphere, this DefGenerator won't be called since we handle it in ExecuteQuery
				// But we need to return something to avoid errors during table validation
				return "SELECT 1", request, nil
			}

			tableName := GetTracesTableNames(ctx, accountId)[0]
			traceProvider, traceProviderUrl, hasMaterializedColumn := getTracesProviderAndUrl(ctx, accountId)
			baseQuery := fmt.Sprintf(`(SELECT TraceId AS trace_id, SpanId AS span_id, ParentSpanId AS parent_span_id, workload_namespace, workload_name, Timestamp AS timestamp, StatusCode AS status_code, SpanName AS span_name, resource, Duration AS duration_ns, destination_workload_name, destination_workload_namespace, destination_name, headers, http_status_code, request_payload, http_response, trace_source, SpanAttributes as spanattributes FROM %s) AS traces_v2`, tableName)
			if !hasMaterializedColumn {
				baseQuery = fmt.Sprintf(`(SELECT TraceId AS trace_id, SpanId AS span_id, ParentSpanId AS parent_span_id, CASE WHEN mapContains(SpanAttributes, 'source.workload_namespace') THEN SpanAttributes['source.workload_namespace'] WHEN mapContains(ResourceAttributes, 'k8s.namespace.name') THEN ResourceAttributes['k8s.namespace.name'] ELSE ResourceAttributes['service.namespace'] END AS workload_namespace, CASE WHEN mapContains(SpanAttributes, 'source.workload_name') THEN SpanAttributes['source.workload_name'] WHEN mapContains(ResourceAttributes, 'k8s.deployment.name') THEN ResourceAttributes['k8s.deployment.name'] ELSE ResourceAttributes['service.name'] END AS workload_name, Timestamp AS timestamp, StatusCode AS status_code, SpanName AS span_name, CASE WHEN mapContains(SpanAttributes, 'db.statement') THEN SpanAttributes['db.statement'] ELSE SpanAttributes['http.url'] END AS resource, Duration AS duration_ns, CASE WHEN mapContains(SpanAttributes, 'destination.workload_name') THEN SpanAttributes['destination.workload_name'] WHEN mapContains(ResourceAttributes, 'k8s.deployment.name') THEN ResourceAttributes['k8s.deployment.name'] WHEN mapContains(ResourceAttributes, 'service.name') THEN ResourceAttributes['service.name'] ELSE ResourceAttributes['net.peer.name'] END AS destination_workload_name, CASE WHEN mapContains(SpanAttributes, 'destination.workload_namespace') THEN SpanAttributes['destination.workload_namespace'] WHEN mapContains(ResourceAttributes, 'k8s.namespace.name') THEN ResourceAttributes['k8s.namespace.name'] ELSE ResourceAttributes['service.namespace'] END AS destination_workload_namespace, CASE WHEN mapContains(SpanAttributes, 'destination.name') THEN SpanAttributes['destination.name'] WHEN mapContains(ResourceAttributes, 'service.name') THEN ResourceAttributes['service.name'] ELSE ResourceAttributes['net.peer.name'] END AS destination_name, SpanAttributes['http.headers'] AS headers, SpanAttributes['http.status_code'] AS http_status_code, SpanAttributes['http.request_payload'] AS request_payload, SpanAttributes['http.response'] AS http_response, CASE WHEN SpanAttributes['otel.scope.name'] = 'nudgebee-node-agent' THEN 'ebpf' ELSE 'otel' END AS trace_source, SpanAttributes as spanattributes FROM %s) AS traces_v2`, tableName)
			}
			if traceProvider == "bigquery" {
				baseQuery = fmt.Sprintf(`(SELECT extendedFields.traceId AS trace_id, span.spanId AS span_id, span.parentSpanId AS parent_span_id, span.attributes.attributeMap.source_workload_namespace AS workload_namespace, span.attributes.attributeMap.source_workload_name AS workload_name, span.startTime AS timestamp, TIMESTAMP_DIFF(span.endTime, span.startTime, MICROSECOND) * 1000 AS duration_ns, CASE WHEN SAFE_CAST(span.attributes.attributeMap._http_status_code AS INT64) >= 400 OR SAFE_CAST(span.attributes.attributeMap._http_status_code AS INT64) < 200 OR SAFE_CAST(span.attributes.attributeMap._http_status_code AS INT64) IS NULL THEN 'STATUS_CODE_ERROR' ELSE 'STATUS_CODE_UNSET' END AS status_code, span.displayName.value AS span_name, CASE WHEN span.attributes.attributeMap.http_url IS NOT NULL THEN span.attributes.attributeMap.http_url ELSE span.attributes.attributeMap._http_path END AS resource, CASE WHEN span.attributes.attributeMap.destination_workload_name IS NOT NULL THEN span.attributes.attributeMap.destination_workload_name ELSE span.attributes.attributeMap.net_peer_name END AS destination_workload_name, CASE WHEN span.attributes.attributeMap.destination_workload_namespace IS NOT NULL THEN span.attributes.attributeMap.destination_workload_namespace ELSE span.attributes.attributeMap.destination_namespace END AS destination_workload_namespace, CASE WHEN span.attributes.attributeMap.destination_name IS NOT NULL THEN span.attributes.attributeMap.destination_name ELSE span.attributes.attributeMap.net_peer_name END AS destination_name, span.attributes.attributeMap.http_headers AS headers, span.attributes.attributeMap._http_status_code AS http_status_code, span.attributes.attributeMap.http_request_payload AS request_payload, span.attributes.attributeMap.http_response AS http_response, CASE WHEN span.attributes.attributeMap.otel_scope_name = 'nudgebee-node-agent' THEN 'ebpf' ELSE 'otel' END AS trace_source FROM %s) AS traces_v2`, traceProviderUrl)
			}
			return baseQuery, request, nil
		},
		Name:                "traces_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		NamespaceColumnName: "workload_namespace",
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "''",
			},
			"trace_id": {
				Type: ColumnDefinitionTypeString,
			},
			"span_id": {
				Type: ColumnDefinitionTypeString,
			},
			"parent_span_id": {
				Type: ColumnDefinitionTypeString,
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "''",
			},
			"timestamp": {
				Type: "datetime",
			},
			"workload_name": {
				Type: ColumnDefinitionTypeString,
				DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
					return GetServiceColumnDefForTraceProvider(ctx, accountId), request, nil
				},
			},
			"workload_namespace": {
				Type: ColumnDefinitionTypeString,
				DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
					return GetNamespaceColumnDefForTraceProvider(ctx, accountId), request, nil
				},
			},
			"duration_ns": {
				Type: ColumnDefinitionTypeFloat,
				DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
					return GetDurationColumnDefForTraceProvider(ctx, accountId), request, nil
				},
			},
			"status_code": {
				Type: ColumnDefinitionTypeString,
			},
			"span_name": {
				Type: ColumnDefinitionTypeString,
				DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
					return GetSpanNameColumnDefForTraceProvider(ctx, accountId), request, nil
				},
			},
			"resource": {
				Type: ColumnDefinitionTypeString,
			},
			"destination_workload_namespace": {
				Type: ColumnDefinitionTypeString,
			},
			"destination_name": {
				Type: ColumnDefinitionTypeString,
			},
			"destination_workload_name": {
				Type: ColumnDefinitionTypeString,
			},
			"headers": {
				Type: ColumnDefinitionTypeString,
				DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
					_, _, hasMaterializedColumn := getTracesProviderAndUrl(ctx, accountId)
					if hasMaterializedColumn {
						return "headers", request, nil
					}
					return "base64Decode(headers)", request, nil
				},
			},
			"http_status_code": {
				Type: ColumnDefinitionTypeString,
			},
			"request_payload": {
				Type: ColumnDefinitionTypeString,
			},
			"http_response": {
				Type: ColumnDefinitionTypeString,
			},
			"service_name": {
				Type: ColumnDefinitionTypeString,
			},
			"trace_source": {
				Type: ColumnDefinitionTypeString,
			},
			"traces": {
				Type: ColumnDefinitionTypeJson,
			},
			"resource_spans": {
				Type: ColumnDefinitionTypeJson,
			},
			"scope_spans": {
				Type: ColumnDefinitionTypeJson,
			},
			"spans": {
				Type: ColumnDefinitionTypeJson,
			},
			"name": {
				Type: ColumnDefinitionTypeString,
			},
			"start_time_unix_nano": {
				Type: ColumnDefinitionTypeString,
			},
			"end_time_unix_nano": {
				Type: ColumnDefinitionTypeString,
			},
			"attributes": {
				Type: ColumnDefinitionTypeJson,
			},
			"events": {
				Type: ColumnDefinitionTypeJson,
			},
			"links": {
				Type: ColumnDefinitionTypeJson,
			},
			"status": {
				Type: ColumnDefinitionTypeJson,
			},
			"service": {
				Type: ColumnDefinitionTypeString,
			},
			"operation": {
				Type: ColumnDefinitionTypeString,
			},
			"query_type": {
				Type: ColumnDefinitionTypeString,
			},
			"start_time": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"end_time": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"tag_filters": {
				Type: ColumnDefinitionTypeJson,
			},
			"trace_ids": {
				Type: ColumnDefinitionTypeList,
			},
			"spanattributes": {
				Type: ColumnDefinitionTypeJson,
			},
			"events_attributes": {
				Type: ColumnDefinitionTypeJson,
			},
		},
	},
	"ticket_groupings_v2": {
		Type:                Aggregate,
		Source:              getSource("ticket_groupings_v2"),
		Def:                 "tickets",
		Name:                "ticket_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_id",
			},
			"ticket_type": {
				Type: ColumnDefinitionTypeString,
				Def:  "ticket_type",
			},
			"reference_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "reference_id",
			},
			"title": {
				Type: ColumnDefinitionTypeString,
			},
			"platform": {
				Type: ColumnDefinitionTypeString,
			},
			"status": {
				Type: ColumnDefinitionTypeString,
			},
			"assignee": {
				Type: ColumnDefinitionTypeString,
			},
			"created_by": {
				Type: ColumnDefinitionTypeString,
			},
			"severity": {
				Type: ColumnDefinitionTypeString,
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"traces_groupings_v2": {
		Type: Aggregate,
		SourceGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) database.DatabaseManagerType {
			return getSourceByAccountId(ctx, accountId)
		},
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			tableName := GetTracesTableNames(ctx, accountId)[0]
			traceProvider, traceProviderUrl, hasMaterializedColumn := getTracesProviderAndUrl(ctx, accountId)
			baseQuery := fmt.Sprintf(`(SELECT workload_zone, destination_workload_zone, TraceId AS trace_id, SpanId AS span_id, ParentSpanId AS parent_span_id, cloud_availability_zone, workload_namespace,workload_name, Timestamp AS timestamp, StatusCode AS status_code, SpanName AS span_name, resource, Duration AS duration_ns, destination_workload_name, destination_workload_namespace, destination_name, headers, http_status_code, request_payload, http_response, trace_source FROM %s) AS traces_grouping_v2`, tableName)
			if !hasMaterializedColumn {
				baseQuery = fmt.Sprintf(`(SELECT ResourceAttributes['cloud.availability_zone'] AS workload_zone, SpanAttributes['destination.cloud.availablity_zone'] AS destination_workload_zone, TraceId AS trace_id, SpanId AS span_id, ParentSpanId AS parent_span_id, ResourceAttributes['cloud.availability_zone'] AS cloud_availability_zone, CASE WHEN mapContains(SpanAttributes, 'source.workload_namespace') THEN SpanAttributes['source.workload_namespace'] WHEN mapContains(ResourceAttributes, 'k8s.namespace.name') THEN ResourceAttributes['k8s.namespace.name'] ELSE ResourceAttributes['service.namespace'] END AS workload_namespace, CASE WHEN mapContains(SpanAttributes, 'source.workload_name') THEN SpanAttributes['source.workload_name'] WHEN mapContains(ResourceAttributes, 'k8s.deployment.name') THEN ResourceAttributes['k8s.deployment.name'] ELSE ResourceAttributes['service.name'] END AS workload_name, Timestamp AS timestamp, StatusCode AS status_code, SpanName AS span_name, CASE WHEN mapContains(SpanAttributes, 'db.statement') THEN SpanAttributes['db.statement'] ELSE SpanAttributes['http.url'] END AS resource, Duration AS duration_ns, CASE WHEN mapContains(SpanAttributes, 'destination.workload_name') THEN SpanAttributes['destination.workload_name'] WHEN mapContains(ResourceAttributes, 'k8s.deployment.name') THEN ResourceAttributes['k8s.deployment.name'] WHEN mapContains(ResourceAttributes, 'service.name') THEN ResourceAttributes['service.name'] ELSE ResourceAttributes['net.peer.name'] END AS destination_workload_name, CASE WHEN mapContains(SpanAttributes, 'destination.workload_namespace') THEN SpanAttributes['destination.workload_namespace'] WHEN mapContains(ResourceAttributes, 'k8s.namespace.name') THEN ResourceAttributes['k8s.namespace.name'] ELSE ResourceAttributes['service.namespace'] END AS destination_workload_namespace, CASE WHEN mapContains(SpanAttributes, 'destination.name') THEN SpanAttributes['destination.name'] WHEN mapContains(ResourceAttributes, 'service.name') THEN ResourceAttributes['service.name'] ELSE ResourceAttributes['net.peer.name'] END AS destination_name, SpanAttributes['http.headers'] AS headers, SpanAttributes['http.status_code'] AS http_status_code, SpanAttributes['http.request_payload'] AS request_payload, SpanAttributes['http.response'] AS http_response, CASE WHEN SpanAttributes['otel.scope.name'] = 'nudgebee-node-agent' THEN 'ebpf' ELSE 'otel' END AS trace_source FROM %s) AS traces_grouping_v2`, tableName)
			}
			if traceProvider == "bigquery" {
				baseQuery = fmt.Sprintf(`(SELECT span.attributes.attributeMap.source_workload_namespace AS workload_namespace, span.attributes.attributeMap.source_workload_name AS workload_name, span.startTime AS timestamp, span.displayName.value AS span_name, CASE WHEN SAFE_CAST(span.attributes.attributeMap._http_status_code AS INT64) >= 400 OR SAFE_CAST(span.attributes.attributeMap._http_status_code AS INT64) < 200 OR SAFE_CAST(span.attributes.attributeMap._http_status_code AS INT64) IS NULL THEN 'STATUS_CODE_ERROR' ELSE 'STATUS_CODE_UNSET' END AS status_code, span.attributes.attributeMap._http_status_code as http_status_code, TIMESTAMP_DIFF(span.endTime, span.startTime, MICROSECOND) * 1000 AS duration_ns, CASE WHEN span.attributes.attributeMap.http_url IS NOT NULL THEN span.attributes.attributeMap.http_url ELSE span.attributes.attributeMap._http_path END AS resource, CASE WHEN span.attributes.attributeMap.destination_workload_name IS NOT NULL THEN span.attributes.attributeMap.destination_workload_name ELSE span.attributes.attributeMap.net_peer_name END AS destination_workload_name, CASE WHEN span.attributes.attributeMap.destination_workload_namespace IS NOT NULL THEN span.attributes.attributeMap.destination_workload_namespace ELSE span.attributes.attributeMap.destination_namespace END AS destination_workload_namespace FROM %s) AS traces_grouping_v2`, traceProviderUrl)
			}
			return baseQuery, request, nil
		},
		Name:                "traces_groupings_v2",
		AccountIdColumnName: "account_id",
		NamespaceColumnName: "workload_namespace",
		Columns: map[string]ColumnDefinition{
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "''",
			},
			"timestamp": {
				Type: "datetime",
			},
			"workload_name": {
				Type: ColumnDefinitionTypeString,
			},
			"workload_namespace": {
				Type: ColumnDefinitionTypeString,
			},
			"workload_zone": {
				Type: ColumnDefinitionTypeString,
			},
			"duration_ns": {
				Type: ColumnDefinitionTypeFloat,
			},
			"status_code": {
				Type: ColumnDefinitionTypeString,
			},
			"span_name": {
				Type: ColumnDefinitionTypeString,
			},
			"resource": {
				Type: ColumnDefinitionTypeString,
			},
			"headers": {
				Type: ColumnDefinitionTypeString,
				DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
					_, _, hasMaterializedColumn := getTracesProviderAndUrl(ctx, accountId)
					if hasMaterializedColumn {
						return "headers", request, nil
					}
					return "base64Decode(headers)", request, nil
				},
			},
			"destination_workload_namespace": {
				Type: ColumnDefinitionTypeString,
			},
			"destination_name": {
				Type: ColumnDefinitionTypeString,
			},
			"destination_workload_name": {
				Type: ColumnDefinitionTypeString,
			},
			"destination_workload_zone": {
				Type: ColumnDefinitionTypeString,
			},
			"http_status_code": {
				Type: ColumnDefinitionTypeString,
			},
			"trace_id": {
				Type: ColumnDefinitionTypeString,
			},
			"span_id": {
				Type: ColumnDefinitionTypeString,
			},
			"parent_span_id": {
				Type: ColumnDefinitionTypeString,
			},
			"trace_source": {
				Type: ColumnDefinitionTypeString,
			},
			"count": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "count(*)",
				IsAggregated: true,
			},
			"error_count": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "SUM(CASE WHEN http_status_code LIKE '4%' OR http_status_code LIKE '5%' THEN 1 ELSE 0 END)",
				IsAggregated: true,
			},
			"p99_latency": {
				Type: ColumnDefinitionTypeFloat,
				DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
					traceProvider, _, _ := getTracesProviderAndUrl(ctx, accountId)
					return getColumnDefinitionValue(traceProvider, "p99_latency"), request, nil
				},
				IsAggregated: true,
			},
			"p50_latency": {
				Type: ColumnDefinitionTypeFloat,
				DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
					traceProvider, _, _ := getTracesProviderAndUrl(ctx, accountId)
					return getColumnDefinitionValue(traceProvider, "p50_latency"), request, nil
				},
				IsAggregated: true,
			},
			"p95_latency": {
				Type: ColumnDefinitionTypeFloat,
				DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
					traceProvider, _, _ := getTracesProviderAndUrl(ctx, accountId)
					return getColumnDefinitionValue(traceProvider, "p95_latency"), request, nil
				},
				IsAggregated: true,
			},
			"max_latency": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "MAX(duration_ns)",
				IsAggregated: true,
			},
			"service_name": {
				Type: ColumnDefinitionTypeString,
			},
		},
	},
	"traces_heatmap_v2": {
		Type: Normal,
		SourceGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) database.DatabaseManagerType {
			return getSourceByAccountId(ctx, accountId)
		},
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			source := getSourceByAccountId(ctx, accountId)
			if source == database.AgentWarehouseChronosphere {
				// For Chronosphere, this DefGenerator won't be called since we handle it in ExecuteQuery
				// But we need to return something to avoid errors during table validation
				return "SELECT 1", request, nil
			}

			tableNames := GetTracesTableNames(ctx, accountId)
			baseQuery := fmt.Sprintf(`SELECT TraceId AS trace_id, Timestamp AS timestamp, SpanAttributes AS span_attributes, Duration AS duration_ns, StatusCode AS status_code, ParentSpanId AS parent_span_id, SpanId AS span_id, SpanName AS span_name, ServiceName AS service_name, ResourceAttributes AS resource_attributes, Events.Attributes AS events_attributes, Events.Name AS events_name FROM %s WHERE TraceId = @traceId ORDER BY Timestamp ASC`, tableNames[0])
			traceProvider, traceProviderUrl, _ := getTracesProviderAndUrl(ctx, accountId)
			if traceProvider == "bigquery" {
				baseQuery = fmt.Sprintf("SELECT extendedFields.traceId AS trace_id, span.startTime AS timestamp, TO_JSON_STRING(STRUCT(span.attributes.attributeMap.http_url AS `http.url`, span.attributes.attributeMap._http_status_code AS `http.status_code`)) AS span_attributes, TIMESTAMP_DIFF(span.endTime, span.startTime, MICROSECOND) * 1000 AS duration_ns, span.attributes.attributeMap._http_status_code AS status_code, span.parentSpanId AS parent_span_id, span.spanId AS span_id, span.displayName.value AS span_name, span.attributes.attributeMap.container_id AS service_name, TO_JSON_STRING(span) AS resource_attributes, '' AS events_attributes, span.spanKind AS events_name FROM %s WHERE extendedFields.traceId = @traceId ORDER BY Timestamp ASC", traceProviderUrl)

			}
			return baseQuery, request, nil
		},
		Name:                "traces_heatmap_v2",
		AccountIdColumnName: "account_id",
		NamespaceColumnName: "workload_namespace",
		Columns: map[string]ColumnDefinition{
			"trace_id": {
				Type: ColumnDefinitionTypeString,
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "''",
			},
			"timestamp": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"span_attributes": {
				Type: ColumnDefinitionTypeString,
			},
			"duration_ns": {
				Type: ColumnDefinitionTypeFloat,
			},
			"status_code": {
				Type: ColumnDefinitionTypeString,
			},
			"parent_span_id": {
				Type: ColumnDefinitionTypeString,
			},
			"span_id": {
				Type: ColumnDefinitionTypeString,
			},
			"span_name": {
				Type: ColumnDefinitionTypeString,
			},
			"service_name": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_attributes": {
				Type: ColumnDefinitionTypeString,
			},
			"events_attributes": {
				Type: ColumnDefinitionTypeString,
			},
			"events_name": {
				Type: ColumnDefinitionTypeString,
			},
		},
	},
	"recommendation_groupings_v2": {
		Type:   Aggregate,
		Source: database.Metastore,
		Name:   "recommendation_groupings_v2",
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			// Push down these filters into the subquery so the planner can use
			// idx_recommendation_tenant_account_status(tenant_id, cloud_account_id, status, category, rule_name)
			pushdownFilters := extractFilterSQL(&request, "account_id", "r.cloud_account_id")
			pushdownFilters += extractFilterSQL(&request, "status", "r.status")
			pushdownFilters += extractFilterSQL(&request, "category", "r.category")
			pushdownFilters += extractFilterSQL(&request, "rule_name", "r.rule_name")

			// Fast path: when no column requiring a JOIN or the dedup window function is
			// needed (e.g. pure count queries like count_recommendations), skip the CTE
			// entirely and scan recommendation directly. This avoids the LEFT JOINs to
			// cloud_resourses / cloud_accounts and the ROW_NUMBER() window function.
			joinRequiringCols := map[string]bool{
				"resource_name":             true,
				"account_name":              true,
				"account_cloud_provider":    true,
				"resource_cloud_service":    true,
				"resource_region":           true,
				"resource_k8s_namespace":    true,
				"resource_meta":             true,
				"resource_type":             true,
				"sum_estimated_savings":     true, // uses is_primary_recommendation — needs window fn
				"is_primary_recommendation": true, // fast path hardcodes TRUE; slow path uses window fn
			}
			needsJoin := false
			for _, col := range request.Columns {
				if joinRequiringCols[col.Name] {
					needsJoin = true
					break
				}
			}
			if !needsJoin {
				for _, col := range request.GroupBy {
					if joinRequiringCols[col] {
						needsJoin = true
						break
					}
				}
			}
			if !needsJoin {
				needsJoin = whereReferencesColumns(request.Where, joinRequiringCols)
			}
			if !needsJoin {
				for _, ob := range request.OrderBy {
					if joinRequiringCols[ob.Column] {
						needsJoin = true
						break
					}
				}
			}

			if !needsJoin {
				def := `(
		SELECT
			r.*,
			TRUE AS is_primary_recommendation
		FROM recommendation r
		WHERE r.cloud_account_id IN (SELECT id FROM cloud_accounts WHERE status = 'active')` + pushdownFilters + `
	) as r1`
				return def, request, nil
			}

			def := `(
		WITH all_recommendations AS (
			SELECT
				r.*,
				cr.name as resource_name,
				COALESCE(cr.service_name, r.recommendation ->> 'service_name') as resource_cloud_service,
				cr.region as resource_region,
				ca.cloud_provider as account_cloud_provider,
				ca.account_name as account_name,
				CASE WHEN cr.meta ->> 'namespace' IS NOT NULL THEN cr.meta ->> 'namespace'
					 WHEN cr.meta -> 'config' ->> 'namespace' IS NOT NULL THEN cr.meta -> 'config' ->> 'namespace'
					 WHEN r.recommendation -> 'spec' -> 'claimRef' ->> 'namespace' IS NOT NULL THEN r.recommendation -> 'spec' -> 'claimRef' ->> 'namespace'
					 WHEN r.recommendation -> 'metadata' ->> 'namespace' IS NOT NULL THEN r.recommendation -> 'metadata' ->> 'namespace'
					 ELSE r.recommendation ->> 'namespace'
				END as resource_k8s_namespace,
				cr.meta as resource_meta,
				CASE
					WHEN cr.meta ->> 'controllerKind' IS NOT NULL THEN cr.meta ->> 'controllerKind'
					WHEN cr.meta -> 'config' ->> 'controllerKind' IS NOT NULL THEN cr.meta -> 'config' ->> 'controllerKind'
					WHEN r.recommendation -> 'spec' -> 'claimRef' ->> 'controllerKind' IS NOT NULL THEN r.recommendation -> 'spec' -> 'claimRef' ->> 'controllerKind'
					WHEN r.recommendation -> 'metadata' ->> 'controllerKind' IS NOT NULL THEN r.recommendation -> 'metadata' ->> 'controllerKind'
					ELSE r.recommendation ->> 'controllerKind'
				END as resource_type,
				ROW_NUMBER() OVER (
					PARTITION BY
						CASE
							-- Producer-set group: collapses alternative recommendations for the same opportunity
							-- (e.g. 8 Savings Plan variants written by Cost Explorer for one workload, or
							-- a Savings Plan / Reserved Instance pair from Cost Optimization Hub on the same commit).
							WHEN r.dedupe_group IS NOT NULL AND r.dedupe_group <> '' THEN r.dedupe_group
							-- Per-resource recommendations: one primary per (resource, category) wins.
							WHEN r.resource_id IS NOT NULL THEN r.resource_id::text
							-- Azure-shaped fallback (kept while Azure ingestion still relies on it).
							WHEN r.recommendation->>'recommendation_type_id' IS NOT NULL
								THEN r.cloud_account_id::text || ':'
									|| COALESCE(r.recommendation->>'recommendation_type_id', '') || ':'
									|| COALESCE(r.recommendation->>'ext_subid', r.recommendation->>'subscription_id', '') || ':'
									|| COALESCE(r.recommendation->>'ext_sku', '')
							ELSE r.id::text
						END,
						r.category
					ORDER BY r.estimated_savings DESC, r.updated_at DESC, r.id
				) AS resource_rank
			FROM recommendation r
			LEFT JOIN cloud_resourses cr ON cr.id = r.resource_id
			LEFT JOIN cloud_accounts ca ON ca.id = r.cloud_account_id
			WHERE ca.status = 'active'` + pushdownFilters + `
		)
		SELECT
			a.*,
			CASE
				WHEN a.resource_rank = 1 THEN TRUE
				ELSE FALSE
			END AS is_primary_recommendation
		FROM all_recommendations a
	) as r1`
			return def, request, nil
		},
		NamespaceColumnName: "resource_k8s_namespace",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "cloud_account_id",
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"cloud_account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"account_cloud_provider": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_cloud_provider",
			},
			"account_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_name",
			},
			"resource_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "resource_id",
			},
			"resource_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "resource_name",
			},
			"resource_cloud_service": {
				Type: ColumnDefinitionTypeString,
				Def:  "resource_cloud_service",
			},
			"resource_region": {
				Type: ColumnDefinitionTypeString,
				Def:  "resource_region",
			},
			"resource_k8s_namespace": {
				Type: ColumnDefinitionTypeString,
				Def:  "resource_k8s_namespace",
			},
			"resource_meta": {
				Type: ColumnDefinitionTypeJson,
				Def:  "resource_meta",
			},
			"resource_type": {
				Type: ColumnDefinitionTypeString,
				Def:  "resource_type",
			},
			"category": {
				Type: ColumnDefinitionTypeString,
				Def:  "category",
			},
			"rule_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "rule_name",
			},
			"estimated_savings": {
				Type: ColumnDefinitionTypeFloat,
				Def:  "estimated_savings",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"severity": {
				Type: ColumnDefinitionTypeString,
				Def:  "severity",
			},
			"recommendation": {
				Type: ColumnDefinitionTypeJson,
				Def:  "recommendation",
			},
			"recommendation_action": {
				Type: ColumnDefinitionTypeString,
				Def:  "recommendation_action",
			},
			"account_object_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_object_id",
			},
			"timestamp": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "updated_at",
			},
			"is_primary_recommendation": {
				Type: ColumnDefinitionTypeBoolean,
				Def:  "is_primary_recommendation",
			},
			"count": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "count(*)",
				IsAggregated: true,
			},
			"sum_estimated_savings": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(CASE WHEN is_primary_recommendation THEN estimated_savings ELSE 0 END)",
				IsAggregated: true,
			},
			"deleted_version": {
				Type: ColumnDefinitionTypeFloat,
				Def:  "COALESCE(NULLIF(r1.recommendation ->> 'deleted_version', '')::FLOAT, 0)",
			},
			"deprecated_version": {
				Type: ColumnDefinitionTypeFloat,
				Def:  "COALESCE(NULLIF(r1.recommendation ->> 'deprecated_version', '')::FLOAT, 0)",
			},
		},
	},
	"integrations_get_all_accounts": {
		Type:               Derived,
		Source:             database.Metastore,
		Name:               "integrations_all_accounts",
		TenantIdColumnName: "tenant_id",
		Def: `(
			WITH accounts AS (
				SELECT
					tenant,
					json_agg(json_build_object('id', id, 'account_name', account_name, 'cloud_provider', cloud_provider, 'status', status) ORDER BY account_name) as all_accounts
				FROM cloud_accounts
				WHERE cloud_provider IN ('K8s', 'AWS', 'Azure', 'GCP')
				GROUP BY tenant
			),
			messaging AS (
				SELECT
					tenant_id,
					json_agg(json_build_object('id', id, 'username', username, 'team_name', team_name, 'created_at', created_at, 'team_id', team_id, 'channels', channels, 'platform', platform) ORDER BY team_name) as messaging_platforms
				FROM messaging_platforms
				WHERE platform IN ('slack', 'ms_teams', 'google_chat')
				GROUP BY tenant_id
			),
			integrations_agg AS (
				SELECT
					tenant_id,
					json_agg(json_build_object('type', type, 'status', status, 'name', name) ORDER BY name) as integrations
				FROM integrations
				WHERE type IN (
					'github', 'gitlab', 'jira', 'servicenow', 'pagerduty', 'zenduty',
					'pagerduty_webhook', 'zenduty_webhook', 'prometheus_alertmanager_webhook',
					'datadog_webhook', 'azure_monitor_webhook', 'servicenow_webhook',
					'postgresql', 'rabbitmq', 'mysql', 'redis', 'confluence', 'clickhouse',
					'datadog', 'argocd', 'llm', 'loggly', 'loki', 'signoz',
					'azure_app_insights', 'prometheus', 'otel_clickhouse', 'chronosphere',
					'ssh', 'observe', 'jaeger', 'ES', 'newrelic', 'newrelic_webhook'
				)
				GROUP BY tenant_id
			)
			SELECT
				t.id as tenant_id,
				COALESCE(a.all_accounts, '[]'::json) as all_accounts,
				COALESCE(m.messaging_platforms, '[]'::json) as messaging_platforms,
				COALESCE(i.integrations, '[]'::json) as integrations
			FROM tenant t
			LEFT JOIN accounts a ON a.tenant = t.id
			LEFT JOIN messaging m ON m.tenant_id = t.id
			LEFT JOIN integrations_agg i ON i.tenant_id = t.id
		) as accounts_data`,
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"all_accounts": {
				Type: ColumnDefinitionTypeJson,
				Def:  "all_accounts",
			},
			"messaging_platforms": {
				Type: ColumnDefinitionTypeJson,
				Def:  "messaging_platforms",
			},
			"integrations": {
				Type: ColumnDefinitionTypeJson,
				Def:  "integrations",
			},
		},
	},
	"recommendation_security_v2": {
		Type:   Normal,
		Source: database.Metastore,
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			// Push down account_id filter into CTE and JOIN to avoid full scan of k8s_pods + recommendation
			podAccountFilter := extractFilterSQL(&request, "account_id", "cr.cloud_account_id")
			recAccountFilter := strings.Replace(podAccountFilter, "cr.cloud_account_id", "r.cloud_account_id", 1)
			def := `
		(
			with pod_container as (
				select
					cr.workload_name as workload_name ,
					cr.workload_type as workload_type ,
					cr."namespace" as "namespace" ,
					cr.cloud_account_id as cloud_account_id ,
					container ->>'image'::text as image,
					cr.tenant_id as tenant_id
				from
								k8s_pods cr ,
					lateral jsonb_array_elements(cr.meta->'config'->'containers') as container
				where cr.is_active is not false` + podAccountFilter + `
				group by
					cr.workload_name,
					cr.workload_type,
					cr."namespace",
					cr.cloud_account_id ,
					cr.tenant_id ,
					container ->>'image'::text
			) select
					r.tenant_id as tenant_id,
					COALESCE(cr.cloud_account_id, r.cloud_account_id) as account_id,
					r.id as id,
					r.severity,
					case
						when r.severity = 'Critical' then 10
						when r.severity = 'High' then 8
						when r.severity = 'Medium' then 5
						when r.severity = 'Low' then 2
						when r.severity = 'Info' then 1
						else 0
					end as severity_weight,
					r.status,
					r.recommendation->>'image_name'::text as image,
					r.recommendation ->>'VulnerabilityID'::text as vulnerability_id,
					r.recommendation ->>'PkgID'::text as package_id,
					r.created_at as created_at,
					r.updated_at as updated_at,
					cr.workload_name,
					cr.workload_type,
					cr.namespace,
					r.recommendation::varchar as recommendation
			from
					pod_container cr
			right outer join recommendation r on
					r.category = 'Security'
					and r.rule_name = 'image_scan'
					and r.account_object_id is not null
					and r.cloud_account_id = cr.cloud_account_id
					and r.tenant_id = cr.tenant_id
					and r.recommendation->>'image_name'::text = cr.image
		where 1=1` + recAccountFilter + `
		) as t
		`
			return def, request, nil
		},
		Name:                "recommendation_security_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		NamespaceColumnName: "namespace",
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
			},
			"id": {
				Type: ColumnDefinitionTypeString,
			},
			"severity": {
				Type: ColumnDefinitionTypeString,
			},
			"severity_weight": {
				Type: ColumnDefinitionTypeInt,
			},
			"status": {
				Type: ColumnDefinitionTypeString,
			},
			"image": {
				Type: ColumnDefinitionTypeString,
			},
			"vulnerability_id": {
				Type: ColumnDefinitionTypeString,
			},
			"package_id": {
				Type: ColumnDefinitionTypeString,
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"updated_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"is_active": {
				Type: ColumnDefinitionTypeBoolean,
			},
			"workload_name": {
				Type: ColumnDefinitionTypeString,
			},
			"workload_type": {
				Type: ColumnDefinitionTypeString,
			},
			"namespace": {
				Type: ColumnDefinitionTypeString,
			},
			"recommendation": {
				Type: ColumnDefinitionTypeJson,
			},
		},
	},
	"recommendation_security_groupings_v2": {
		Type:   Aggregate,
		Source: database.Metastore,
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			// Push down account_id filter into CTE and JOIN to avoid full scan of k8s_pods + recommendation
			podAccountFilter := extractFilterSQL(&request, "account_id", "cr.cloud_account_id")
			recAccountFilter := strings.Replace(podAccountFilter, "cr.cloud_account_id", "r.cloud_account_id", 1)
			def := `
		(
			with pod_container as (
				select
					cr.workload_name as workload_name ,
					cr.workload_type as workload_type ,
					cr."namespace" as "namespace" ,
					cr.cloud_account_id as cloud_account_id ,
					container ->>'image'::text as image,
					cr.tenant_id as tenant_id
				from
								k8s_pods cr ,
					lateral jsonb_array_elements(cr.meta->'config'->'containers') as container
				where cr.is_active is not false` + podAccountFilter + `
				group by
					cr.workload_name,
					cr.workload_type,
					cr."namespace",
					cr.cloud_account_id ,
					cr.tenant_id ,
					container ->>'image'::text
				) select
					r.tenant_id as tenant_id,
					COALESCE(cr.cloud_account_id, r.cloud_account_id) as account_id,
					r.id as id,
					r.severity,
					case
						when r.severity = 'Critical' then 10
						when r.severity = 'High' then 8
						when r.severity = 'Medium' then 5
						when r.severity = 'Low' then 2
						when r.severity = 'Info' then 1
						else 0
					end as severity_weight,
					r.status,
					r.recommendation->>'image_name'::text as image,
					r.recommendation ->>'VulnerabilityID'::text as vulnerability_id,
					r.recommendation ->>'PkgID'::text as package_id,
					r.created_at as created_at,
					r.updated_at as updated_at,
					cr.workload_name,
					cr.workload_type,
					cr.namespace,
					r.recommendation::varchar as recommendation
				from
					pod_container cr
				inner join recommendation r on
					r.category = 'Security'
					and r.rule_name = 'image_scan'
					and r.account_object_id is not null
					and r.cloud_account_id = cr.cloud_account_id
					and r.tenant_id = cr.tenant_id
					and r.recommendation->>'image_name'::text = cr.image
		where 1=1` + recAccountFilter + `
		) as t
		`
			return def, request, nil
		},
		Name:                "recommendation_security_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		NamespaceColumnName: "namespace",
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
			},
			"id": {
				Type: ColumnDefinitionTypeString,
			},
			"severity": {
				Type: ColumnDefinitionTypeString,
			},
			"severity_weight": {
				Type: ColumnDefinitionTypeInt,
			},
			"status": {
				Type: ColumnDefinitionTypeString,
			},
			"image": {
				Type: ColumnDefinitionTypeString,
			},
			"vulnerability_id": {
				Type: ColumnDefinitionTypeString,
			},
			"package_id": {
				Type: ColumnDefinitionTypeString,
			},
			"updated_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"is_active": {
				Type: ColumnDefinitionTypeBoolean,
			},
			"workload_name": {
				Type: ColumnDefinitionTypeString,
			},
			"workload_type": {
				Type: ColumnDefinitionTypeString,
			},
			"namespace": {
				Type: ColumnDefinitionTypeString,
			},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
			"count_severity_critical": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when severity = 'Critical' then 1 end)",
				IsAggregated: true,
			},
			"count_severity_high": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when severity = 'High' then 1 end)",
				IsAggregated: true,
			},
			"count_severity_medium": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when severity = 'Medium' then 1 end)",
				IsAggregated: true,
			},
			"count_severity_low": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when severity = 'Low' then 1 end)",
				IsAggregated: true,
			},
			"count_severity_info": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when severity = 'Info' then 1 end)",
				IsAggregated: true,
			},
			"count_workload_name": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(distinct workload_name)",
				IsAggregated: true,
			},
			"count_image": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(distinct image)",
				IsAggregated: true,
			},
			"count_vulnerability_id": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(distinct vulnerability_id)",
				IsAggregated: true,
			},
			"count_package_id": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(distinct package_id)",
				IsAggregated: true,
			},
			"max_updated_at": {
				Type:         ColumnDefinitionTypeDatetime,
				Def:          "max(updated_at)",
				IsAggregated: true,
			},
			"max_created_at": {
				Type:         ColumnDefinitionTypeDatetime,
				Def:          "max(created_at)",
				IsAggregated: true,
			},
		},
	},
	"auto_playbook_v2": {
		Type:   Normal,
		Source: database.Metastore,
		Def: `(SELECT ap.id::varchar as id, ap.tenant_id,
			ap.account_id,
			ap.trigger,
			ap.resource_filter,
			ap.name,
			ap.created_at,
			ap.created_by,
			ap.updated_by,
			ap.status,
			ap.tasks,
			ap.last_executed_time,
			u.username,
			u.display_name,
			uu.id as updated_user_id,
			uu.username as updated_username,
			uu.display_name as updated_user_displayname,
			ap.attributes,
			ap.notification
	 FROM   auto_playbook ap
			left join users u
				   ON ap.created_by = u.id left join users uu ON ap.updated_by = uu.id) as r`,
		Name:                "auto_playbook_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_id",
			},
			"name": {
				Type: ColumnDefinitionTypeString,
				Def:  "name",
			},
			"created_at": {
				Type: ColumnDefinitionTypeString,
				Def:  "created_at",
			},
			"created_by": {
				Type: ColumnDefinitionTypeString,
				Def:  "created_by",
			},
			"updated_by": {
				Type: ColumnDefinitionTypeString,
				Def:  "updated_by",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"last_executed_time": {
				Type: ColumnDefinitionTypeString,
				Def:  "last_executed_time",
			},
			"tasks": {
				Type: ColumnDefinitionTypeString,
				Def:  "tasks",
			},
			"trigger": {
				Type: ColumnDefinitionTypeString,
				Def:  "trigger",
			},
			"resource_filter": {
				Type: ColumnDefinitionTypeString,
				Def:  "resource_filter",
			},
			"username": {
				Type: ColumnDefinitionTypeString,
			},
			"display_name": {
				Type: ColumnDefinitionTypeString,
			},
			"updated_username": {
				Type: ColumnDefinitionTypeString,
			},
			"updated_user_displayname": {
				Type: ColumnDefinitionTypeString,
			},
			"attributes": {
				Type: ColumnDefinitionTypeString,
				Def:  "attributes",
			},
			"notification": {
				Type: ColumnDefinitionTypeJson,
				Def:  "notification",
			},
		},
	},
	"auto_playbook_grouping_v2": {
		Type:                Aggregate,
		Source:              database.Metastore,
		Def:                 "auto_playbook",
		Name:                "auto_playbook_grouping_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id::varchar",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_id",
			},
			"name": {
				Type: ColumnDefinitionTypeString,
				Def:  "name",
			},
			"created_at": {
				Type: ColumnDefinitionTypeString,
				Def:  "created_at",
			},
			"created_by": {
				Type: ColumnDefinitionTypeString,
				Def:  "created_by",
			},
			"updated_by": {
				Type: ColumnDefinitionTypeString,
				Def:  "updated_by",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"last_executed_time": {
				Type: ColumnDefinitionTypeString,
				Def:  "last_executed_time",
			},
			"trigger": {
				Type: ColumnDefinitionTypeString,
				Def:  "trigger",
			},
			"resource_filter": {
				Type: ColumnDefinitionTypeString,
				Def:  "resource_filter",
			},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"llm_conversation_feedback_v2": {
		Type:                Aggregate,
		Source:              database.Metastore,
		Def:                 "llm_conversation_feedback",
		Name:                "llm_conversation_feedback_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "cloud_account_id",
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id::varchar",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"cloud_account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"module": {
				Type: ColumnDefinitionTypeString,
				Def:  "module",
			},
			"question": {
				Type: ColumnDefinitionTypeString,
				Def:  "question",
			},
			"useful": {
				Type: ColumnDefinitionTypeBoolean,
				Def:  "useful",
			},
			"additional_notes": {
				Type: ColumnDefinitionTypeString,
				Def:  "additional_notes",
			},
			"session_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "session_id",
			},
			"updated_at": {
				Type: ColumnDefinitionTypeString,
				Def:  "updated_at",
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at",
			},
			"llm_response": {
				Type: ColumnDefinitionTypeString,
				Def:  "llm_response",
			},
			"user_corrected_response": {
				Type: ColumnDefinitionTypeString,
				Def:  "user_corrected_response",
			},
			"conversation_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "conversation_id",
			},
			"user_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "user_id",
			},
		},
	},
	"k8s_workloads_v2": {
		Type:                Normal,
		Source:              database.Metastore,
		Def:                 "k8s_workloads",
		Name:                "k8s_workloads_v2",
		AccountIdColumnName: "account_id",
		TenantIdColumnName:  "tenant_id",
		NamespaceColumnName: "namespace",
		UpdateFilters: func(ctx *security.RequestContext, request QueryRequest) (QueryRequest, error) {
			if !tenant.IsFeatureEnabled(ctx, ctx.GetSecurityContext().GetTenantId(), tenant.FEATURE_RBACK_K8S_ACCESS) {
				return request, nil
			}
			accountId := ""
			if binaryClause, ok := request.Where.Binary["account_id"]; ok {
				if accountIdAny, ok := binaryClause[Eq]; ok {
					accountId = accountIdAny.(string)
				}
			}

			if accountId == "" {
				return QueryRequest{}, errors.New("accountId is required")
			}
			workloads, err := ctx.GetSecurityContext().ListK8sObjectNames(accountId, "workloads", security.K8sRbacPermissionTypeList)
			if err != nil {
				return request, err
			}

			request.Where.And = append(request.Where.And, QueryWhereClause{
				Binary: BinaryWhereClause{
					"workload_fqdn": {
						In: workloads,
					},
				},
			})

			return request, err
		},
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"resource_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_resource_id",
			},
			"external_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "external_id",
			},
			"namespace": {
				Type: ColumnDefinitionTypeString,
				Def:  "namespace",
			},
			"is_active": {
				Type: ColumnDefinitionTypeBoolean,
				Def:  "is_active",
			},
			"total_pods": {
				Type: ColumnDefinitionTypeInt,
				Def:  "total_pods",
			},
			"ready_pods": {
				Type: ColumnDefinitionTypeInt,
				Def:  "ready_pods",
			},
			"name": {
				Type: ColumnDefinitionTypeString,
				Def:  "name",
			},
			"kind": {
				Type: ColumnDefinitionTypeString,
				Def:  "kind",
			},
			"creation_time": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "creation_time",
			},
			"last_seen": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "last_seen",
			},
			"labels": {
				Type: ColumnDefinitionTypeJson,
				Def:  "labels",
			},
			"meta": {
				Type: ColumnDefinitionTypeJson,
				Def:  "meta",
			},
			"updated_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "updated_at",
			},
			"workload_fqdn": {
				Type: ColumnDefinitionTypeString,
				Def:  "concat(namespace, '/', name)",
			},
		},
	},
	"k8s_pods_v2": {
		Type:                Normal,
		Source:              database.Metastore,
		Def:                 "k8s_pods",
		Name:                "k8s_pods_v2",
		AccountIdColumnName: "account_id",
		TenantIdColumnName:  "tenant_id",
		NamespaceColumnName: "namespace",
		UpdateFilters: func(ctx *security.RequestContext, request QueryRequest) (QueryRequest, error) {
			if !tenant.IsFeatureEnabled(ctx, ctx.GetSecurityContext().GetTenantId(), tenant.FEATURE_RBACK_K8S_ACCESS) {
				return request, nil
			}

			accountId := ""
			if binaryClause, ok := request.Where.Binary["account_id"]; ok {
				if accountIdAny, ok := binaryClause[Eq]; ok {
					accountId = accountIdAny.(string)
				}
			}
			if accountId == "" {
				return QueryRequest{}, errors.New("accountId is required")
			}

			workloads, err := ctx.GetSecurityContext().ListK8sObjectNames(accountId, "pods", security.K8sRbacPermissionTypeList)
			if err != nil {
				return request, err
			}

			request.Where.And = append(request.Where.And, QueryWhereClause{
				Binary: BinaryWhereClause{
					"pod_fqdn": {
						In: workloads,
					},
				},
			})
			return request, nil
		},
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"resource_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_resource_id",
			},
			"external_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "external_id",
			},
			"name": {
				Type: ColumnDefinitionTypeString,
				Def:  "name",
			},
			"workload_type": {
				Type: ColumnDefinitionTypeString,
				Def:  "workload_type",
			},
			"workload_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "workload_name",
			},
			"namespace": {
				Type: ColumnDefinitionTypeString,
				Def:  "namespace",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"node_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "node_name",
			},
			"is_active": {
				Type: ColumnDefinitionTypeBoolean,
				Def:  "is_active",
			},
			"restart_count": {
				Type: ColumnDefinitionTypeJson,
				Def:  "restart_count",
			},
			"creation_time": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "creation_time",
			},
			"last_seen": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "creation_time",
			},
			"labels": {
				Type: ColumnDefinitionTypeJson,
				Def:  "labels",
			},
			"meta": {
				Type: ColumnDefinitionTypeJson,
				Def:  "meta",
			},
			"updated_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "updated_at",
			},
			"pod_fqdn": {
				Type: ColumnDefinitionTypeString,
				Def:  "concat(namespace, '/', name)",
			},
		},
	},
	"k8s_namespaces_v2": {
		Type:                Normal,
		Source:              database.Metastore,
		Def:                 "k8s_namespaces",
		Name:                "k8s_namespaces_v2",
		AccountIdColumnName: "account_id",
		TenantIdColumnName:  "tenant_id",
		NamespaceColumnName: "name",
		UpdateFilters: func(ctx *security.RequestContext, request QueryRequest) (QueryRequest, error) {
			if !tenant.IsFeatureEnabled(ctx, ctx.GetSecurityContext().GetTenantId(), tenant.FEATURE_RBACK_K8S_ACCESS) {
				return request, nil
			}

			accountId := ""
			if binaryClause, ok := request.Where.Binary["account_id"]; ok {
				if accountIdAny, ok := binaryClause[Eq]; ok {
					accountId = accountIdAny.(string)
				}
			}
			if accountId == "" {
				return QueryRequest{}, errors.New("accountId is required")
			}

			namespaces, err := ctx.GetSecurityContext().ListK8sObjectNames(accountId, "namespaces", security.K8sRbacPermissionTypeList)
			if err != nil {
				return request, err
			}

			request.Where.And = append(request.Where.And, QueryWhereClause{
				Binary: BinaryWhereClause{
					"name": {
						In: namespaces,
					},
				},
			})
			return request, nil
		},
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"name": {
				Type: ColumnDefinitionTypeString,
				Def:  "name",
			},
			"is_active": {
				Type: ColumnDefinitionTypeBoolean,
				Def:  "is_active",
			},
			"workload_count": {
				Type: ColumnDefinitionTypeInt,
				Def:  "workload_count",
			},
			"pod_count": {
				Type: ColumnDefinitionTypeInt,
				Def:  "pod_count",
			},
			"creation_time": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "creation_time",
			},
			"updated_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "updated_at",
			},
		},
	},
	"k8s_workload_groupings_v2": {
		Type:                Aggregate,
		Source:              database.Metastore,
		Def:                 "k8s_workloads",
		Name:                "k8s_workload_groupings_v2",
		AccountIdColumnName: "account_id",
		TenantIdColumnName:  "tenant_id",
		NamespaceColumnName: "namespace",
		UpdateFilters: func(ctx *security.RequestContext, request QueryRequest) (QueryRequest, error) {
			if !tenant.IsFeatureEnabled(ctx, ctx.GetSecurityContext().GetTenantId(), tenant.FEATURE_RBACK_K8S_ACCESS) {
				return request, nil
			}
			accountId := ""
			if binaryClause, ok := request.Where.Binary["cloud_account_id"]; ok {
				if accountIdAny, ok := binaryClause[Eq]; ok {
					accountId = accountIdAny.(string)
				}
			}

			if accountId == "" {
				return QueryRequest{}, errors.New("accountId is required")
			}
			workloads, err := ctx.GetSecurityContext().ListK8sObjectNames(accountId, "workloads", security.K8sRbacPermissionTypeList)
			if err != nil {
				return request, err
			}

			request.Where.And = append(request.Where.And, QueryWhereClause{
				Binary: BinaryWhereClause{
					"workload_fqdn": {
						In: workloads,
					},
				},
			})

			return request, err
		},
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"resource_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_resource_id",
			},
			"external_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "external_id",
			},
			"namespace": {
				Type: ColumnDefinitionTypeString,
				Def:  "namespace",
			},
			"is_active": {
				Type: ColumnDefinitionTypeBoolean,
				Def:  "is_active",
			},
			"name": {
				Type: ColumnDefinitionTypeString,
				Def:  "name",
			},
			"kind": {
				Type: ColumnDefinitionTypeString,
				Def:  "kind",
			},
			"workload_fqdn": {
				Type: ColumnDefinitionTypeString,
				Def:  "concat(namespace, '/', name)",
			},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
			"deployment_count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when kind='Deployment' then 1 end)",
				IsAggregated: true,
			},
			"statefulset_count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when kind='StatefulSet' then 1 end)",
				IsAggregated: true,
			},
			"daemonset_count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when kind='DaemonSet' then 1 end)",
				IsAggregated: true,
			},
			"replicaset_count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when kind='ReplicaSet' then 1 end)",
				IsAggregated: true,
			},
			"job_count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when kind='Job' then 1 end)",
				IsAggregated: true,
			},
			"cronjob_count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when kind='CronJob' then 1 end)",
				IsAggregated: true,
			},
			"rollout_count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(case when kind='Rollout' then 1 end)",
				IsAggregated: true,
			},
		},
	},
	"k8s_pod_groupings_v2": {
		Type:                Aggregate,
		Source:              database.Metastore,
		Def:                 "k8s_pods",
		Name:                "k8s_pod_groupings_v2",
		AccountIdColumnName: "account_id",
		TenantIdColumnName:  "tenant_id",
		NamespaceColumnName: "namespace",
		UpdateFilters: func(ctx *security.RequestContext, request QueryRequest) (QueryRequest, error) {
			if !tenant.IsFeatureEnabled(ctx, ctx.GetSecurityContext().GetTenantId(), tenant.FEATURE_RBACK_K8S_ACCESS) {
				return request, nil
			}

			accountId := ""
			if binaryClause, ok := request.Where.Binary["account_id"]; ok {
				if accountIdAny, ok := binaryClause[Eq]; ok {
					accountId = accountIdAny.(string)
				}
			}
			if accountId == "" {
				return QueryRequest{}, errors.New("accountId is required")
			}

			workloads, err := ctx.GetSecurityContext().ListK8sObjectNames(accountId, "pods", security.K8sRbacPermissionTypeList)
			if err != nil {
				return request, err
			}

			request.Where.And = append(request.Where.And, QueryWhereClause{
				Binary: BinaryWhereClause{
					"pod_fqdn": {
						In: workloads,
					},
				},
			})
			return request, nil
		},
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"resource_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_resource_id",
			},
			"external_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "external_id",
			},
			"name": {
				Type: ColumnDefinitionTypeString,
				Def:  "name",
			},
			"workload_type": {
				Type: ColumnDefinitionTypeString,
				Def:  "workload_type",
			},
			"workload_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "workload_name",
			},
			"namespace": {
				Type: ColumnDefinitionTypeString,
				Def:  "namespace",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"node_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "node_name",
			},
			"is_active": {
				Type: ColumnDefinitionTypeBoolean,
				Def:  "is_active",
			},
			"pod_fqdn": {
				Type: ColumnDefinitionTypeString,
				Def:  "concat(namespace, '/', name)",
			},
			"creation_time": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "creation_time",
			},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"k8s_namespace_groupings_v2": {
		Type:                Aggregate,
		Source:              database.Metastore,
		Def:                 "k8s_namespaces",
		Name:                "k8s_namespaces_v2",
		AccountIdColumnName: "account_id",
		TenantIdColumnName:  "tenant_id",
		NamespaceColumnName: "name",
		UpdateFilters: func(ctx *security.RequestContext, request QueryRequest) (QueryRequest, error) {
			if !tenant.IsFeatureEnabled(ctx, ctx.GetSecurityContext().GetTenantId(), tenant.FEATURE_RBACK_K8S_ACCESS) {
				return request, nil
			}

			accountId := ""
			if binaryClause, ok := request.Where.Binary["cloud_account_id"]; ok {
				if accountIdAny, ok := binaryClause[Eq]; ok {
					accountId = accountIdAny.(string)
				}
			}
			if accountId == "" {
				return QueryRequest{}, errors.New("accountId is required")
			}

			namespaces, err := ctx.GetSecurityContext().ListK8sObjectNames(accountId, "namespaces", security.K8sRbacPermissionTypeList)
			if err != nil {
				return request, err
			}

			request.Where.And = append(request.Where.And, QueryWhereClause{
				Binary: BinaryWhereClause{
					"name": {
						In: namespaces,
					},
				},
			})
			return request, nil
		},
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"name": {
				Type: ColumnDefinitionTypeString,
				Def:  "name",
			},
			"is_active": {
				Type: ColumnDefinitionTypeBoolean,
				Def:  "is_active",
			},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"k8s_workloads_cloud_account_monitoring_v2": {
		Type:   Normal,
		Source: database.Metastore,
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			// Push down account_id filter into the event_count CTE to avoid full scan of events table (61GB)
			eventAccountFilter := extractFilterSQL(&request, "account_id", "e2.cloud_account_id")
			def := `(WITH event_count AS (
			SELECT
				subject_name,
				subject_namespace,
				count(*) AS event_count,
				e2.aggregation_key,
				e2.cloud_account_id,
				e2.cloud_resource_id,
				DATE_TRUNC('HOUR', e2.created_at) AS event_date,
				SUM(case when aggregation_key in ('KubePodCrashLooping', 'ReportCrashLoop', 'CPUThrottlingHigh', 'image_pull_backoff_reporter', 'pod_oom_killer_enricher') then 1 else 0 end) pod_error_count,
				SUM(case when aggregation_key in ('HighErrorCriticalLogs', 'ApplicationAPIFailures') then 1 else 0 end) application_error_count
			FROM
				events e2
			WHERE
				e2.finding_type = 'issue'
				AND e2.priority != 'DEBUG'` + eventAccountFilter + `
			GROUP BY
				aggregation_key,
				cloud_account_id,
				cloud_resource_id,
				subject_name,
				subject_namespace,
				DATE_TRUNC('HOUR', e2.created_at)
		), workload_slo_report AS (
			SELECT
				status,
				config_id,
				bad_events_count,
				good_events_count,
				RANK() OVER (
					PARTITION BY config_id
					ORDER BY sr.created_at
				) rn
			FROM
				slo_report sr
		), workload_slo AS (
			SELECT
				SUM(CASE WHEN sr.status = 'FIRING' THEN 1 ELSE 0 END) AS failed_slo_count,
				COUNT(*) AS total_slo_count,
				sc.workload_name,
				sc.workload_namespace,
				sc.tenant_id,
				sc.cloud_account_id
			FROM
				slo_config sc
				inner JOIN workload_slo_report sr ON sr.config_id = sc.id
					AND sr.rn = 1
					AND sc.enabled = true
			GROUP BY
				sc.tenant_id,
				sc.cloud_account_id,
				sc.workload_name,
				sc.workload_namespace
		) , workload_list as (
		select
			ksw.tenant_id AS tenant_id,
			ksw.cloud_account_id as cloud_account_id ,
			ksw.name,
			ksw.cloud_resource_id AS cloud_resource_id,
			ksw.namespace,
			ksw.is_active ,
			ksw.kind ,
			ksw.total_pods,
			ksw.creation_time,
			ksw.ready_pods,
			slo.failed_slo_count,
			slo.total_slo_count
		from
		k8s_workloads ksw
		left join workload_slo slo on
		slo.tenant_id = ksw.tenant_id
		and slo.cloud_account_id = ksw.cloud_account_id
		and slo.workload_name = ksw."name"
		and slo.workload_namespace = ksw."namespace"
		)
		SELECT
			kw.tenant_id AS tenant_id,
			kw.name,
			kw.cloud_resource_id AS workload_id,
			kw.namespace,
			kw.total_pods,
			kw.creation_time,
			kw.ready_pods,
			ca.id AS account_id,
			ca.account_name,
			SUM(event_count) AS event_count,
			SUM(pod_error_count) AS pod_error_count,
			SUM(application_error_count) AS application_error_count,
			kw.failed_slo_count AS failed_slo_count,
			kw.total_slo_count AS total_slo_count
		FROM
			workload_list kw
		INNER JOIN cloud_accounts ca ON
			kw.cloud_account_id = ca.id
		LEFT JOIN event_count ON
			event_count.subject_name LIKE kw.name || '%'
			AND event_count.subject_namespace = kw."namespace"
			AND event_count.cloud_account_id = kw.cloud_account_id
			AND event_count.event_date > (CURRENT_TIMESTAMP - INTERVAL '24 hours')
		WHERE
			kw.is_active = true
			AND kw.kind IN ('Deployment', 'StatefulSet', 'DaemonSet', 'Rollout')
			AND kw."namespace" NOT IN ('kube-system')
		GROUP BY
			kw.tenant_id,
			kw.name,
			kw.cloud_resource_id,
			kw."namespace",
			kw.total_pods,
			kw.ready_pods,
			ca.id,
			ca.account_name,
			kw.creation_time,
			kw.failed_slo_count,
			kw.total_slo_count
		) as kwca`
			return def, request, nil
		},
		Name:                "k8s_workloads_cloud_account_monitoring",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		NamespaceColumnName: "namespace",
		Columns: map[string]ColumnDefinition{
			"name": {
				Type: ColumnDefinitionTypeString,
				Def:  "name",
			},
			"account_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_name",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_id",
			},
			"namespace": {
				Type: ColumnDefinitionTypeString,
				Def:  "namespace",
			},
			"workload_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "workload_id",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"ready_pods": {
				Type: ColumnDefinitionTypeInt,
				Def:  "ready_pods",
			},
			"total_pods": {
				Type: ColumnDefinitionTypeInt,
				Def:  "total_pods",
			},
			"event_count": {
				Type: ColumnDefinitionTypeInt,
				Def:  "event_count",
			},
			"creation_time": {
				Type: ColumnDefinitionTypeString,
				Def:  "creation_time",
			},
			"pod_error_count": {
				Type: ColumnDefinitionTypeInt,
				Def:  "pod_error_count",
			},
			"application_error_count": {
				Type: ColumnDefinitionTypeInt,
				Def:  "application_error_count",
			},
			"failed_slo_count": {
				Type: ColumnDefinitionTypeInt,
				Def:  "failed_slo_count",
			},
			"total_slo_count": {
				Type: ColumnDefinitionTypeInt,
				Def:  "total_slo_count",
			},
		},
	},
	"k8s_workloads_cloud_account_monitoring_recommendations_v2": {
		Type:   Normal,
		Source: database.Metastore,
		Def: `(SELECT
			kw.name as workload_name,
			kw.namespace,
			kw.kind as workload_type,
			kw.cloud_account_id,
			kw.tenant_id,
			count(*) as recommendation_count
		FROM
			k8s_workloads kw
		INNER JOIN recommendation r
			ON
			kw.cloud_account_id = r.cloud_account_id
			AND kw.tenant_id = r.tenant_id
			AND kw.cloud_resource_id = r.resource_id
		WHERE
			r.status = 'Open'
			AND category = 'RightSizing'
			AND rule_name = 'pod_right_sizing'
		GROUP BY
			kw.name,
			kw.namespace,
			kw.kind,
			kw.cloud_account_id,
			kw.tenant_id) as kwcamr`,
		Name:                "k8s_workloads_cloud_account_monitoring_recommendations_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "cloud_account_id",
		NamespaceColumnName: "namespace",
		Columns: map[string]ColumnDefinition{
			"workload_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "workload_name",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"namespace": {
				Type: ColumnDefinitionTypeString,
				Def:  "namespace",
			},
			"recommendation_count": {
				Type: ColumnDefinitionTypeInt,
				Def:  "recommendation_count",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
		},
	},
	"cloud_metric_groupings_v2": {
		Type:                Aggregate,
		Source:              database.AgentMetrices,
		Def:                 "cloud_resource_metric",
		Name:                "cloud_metric_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"region_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "region_name",
			},
			"service_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "service_name",
			},
			"resource_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_resource_id",
			},
			"resource_type": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_resource_type",
			},
			"metric": {
				Type: ColumnDefinitionTypeString,
				Def:  "metric",
			},
			"timestamp": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "timestamp",
			},
			"count_value": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
			"sum_value": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(value)",
				IsAggregated: true,
			},
			"avg_value": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "avg(value)",
				IsAggregated: true,
			},
			"min_value": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "min(value)",
				IsAggregated: true,
			},
			"max_value": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "max(value)",
				IsAggregated: true,
			},
		},
	},
	"recommendation_security_cis_groupings_v2": {
		Type:   Aggregate,
		Source: database.Metastore,
		Def: `(select *
								, (case when severity = 'Critical' then 10 
										when severity = 'High' then 8 
										when severity = 'Medium' then 5 
										when severity = 'Low' then 2 
										when severity = 'Info' then 1 
										else 0 end) as severity_weight
								from recommendation where rule_name = 'k8s-cis-1.23'
							) as r`,
		Name:                "recommendation_security_cis_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"severity": {
				Type: ColumnDefinitionTypeString,
			},
			"severity_weight": {
				Type: ColumnDefinitionTypeInt,
			},
			"rule_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "recommendation ->> 'Id'",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"rule_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "recommendation ->> 'Name'",
			},
			"rule_description": {
				Type: ColumnDefinitionTypeString,
				Def:  "recommendation ->> 'Description'",
			},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				IsAggregated: true,
				Def:          "count(*)",
			},
			"updated_at": {
				Type:         ColumnDefinitionTypeDatetime,
				IsAggregated: true,
				Def:          "max(updated_at)",
			},
		},
	},
	"recommendations_v2": {
		Type:   Normal,
		Source: database.Metastore,
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			pushdownFilters := extractFilterSQL(&request, "account_id", "r.cloud_account_id")
			pushdownFilters += extractFilterSQL(&request, "status", "r.status")
			def := `(
					WITH all_recommendations AS (
						SELECT
							r.*,
							COALESCE(
								cr.meta ->> 'namespace',
								cr.meta -> 'config' ->> 'namespace',
								r.recommendation -> 'spec' -> 'claimRef' ->> 'namespace',
								r.recommendation -> 'metadata' ->> 'namespace',
								r.recommendation ->> 'namespace'
							) as resource_k8s_namespace,
							cr.meta as resource_meta,
							cr."name" as resource_name,
							COALESCE(
								cr.meta ->> 'controllerKind',
								cr.meta -> 'config' ->> 'controllerKind',
								r.recommendation -> 'spec' -> 'claimRef' ->> 'controllerKind',
								r.recommendation -> 'metadata' ->> 'controllerKind',
								r.recommendation ->> 'controllerKind'
							) as resource_type,
							COALESCE(cr.service_name, r.recommendation ->> 'service_name') as resource_cloud_service,
							cr.is_active as resource_is_active,
							cr.cloud_provider as resource_cloud_provider,
							cr.arn as resource_arn,
							cr.region as resource_region,
							cr.status as resource_status,
							ROW_NUMBER() OVER (
								PARTITION BY
									CASE
										WHEN r.resource_id IS NOT NULL THEN r.resource_id::text
										WHEN r.recommendation->>'recommendation_type_id' IS NOT NULL
											THEN r.cloud_account_id::text || ':'
												|| COALESCE(r.recommendation->>'recommendation_type_id', '') || ':'
												|| COALESCE(r.recommendation->>'ext_subid', r.recommendation->>'subscription_id', '') || ':'
												|| COALESCE(r.recommendation->>'ext_sku', '')
										ELSE r.id::text
									END,
									r.category
								ORDER BY r.estimated_savings DESC, r.updated_at DESC, r.id
							) AS resource_rank
						FROM recommendation r
						LEFT JOIN cloud_resourses cr ON cr.id = r.resource_id
						LEFT JOIN cloud_accounts ca ON ca.id = r.cloud_account_id
						WHERE ca.status = 'active'` + pushdownFilters + `
					)
					SELECT
						a.*,
						CASE
							WHEN a.resource_rank = 1 THEN TRUE
							ELSE FALSE
						END AS is_primary_recommendation
					FROM all_recommendations a
				) as r1`
			return def, request, nil
		},
		Name:                "recommendations_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		NamespaceColumnName: "resource_k8s_namespace",
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"updated_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"resource_id": {
				Type: ColumnDefinitionTypeString,
			},
			"recommendation": {
				Type: ColumnDefinitionTypeJson,
			},
			"recommendation_action": {
				Type: ColumnDefinitionTypeString,
			},
			"note": {
				Type: ColumnDefinitionTypeString,
			},
			"severity": {
				Type: ColumnDefinitionTypeString,
			},
			"estimated_savings": {
				Type: ColumnDefinitionTypeFloat,
			},
			"status": {
				Type: ColumnDefinitionTypeString,
			},
			"category": {
				Type: ColumnDefinitionTypeString,
			},
			"rule_name": {
				Type: ColumnDefinitionTypeString,
			},
			"dismissed_reason": {
				Type: ColumnDefinitionTypeString,
			},
			"is_dismissed": {
				Type: ColumnDefinitionTypeBoolean,
			},
			"account_object_id": {
				Type: ColumnDefinitionTypeString,
			},
			"updated_by": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_k8s_namespace": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_meta": {
				Type: ColumnDefinitionTypeJson,
			},
			"resource_name": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_type": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_cloud_service": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_is_active": {
				Type: ColumnDefinitionTypeBoolean,
			},
			"resource_cloud_provider": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_arn": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_region": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_status": {
				Type: ColumnDefinitionTypeString,
			},
			"is_primary_recommendation": {
				Type: ColumnDefinitionTypeBoolean,
			},
			"finops_score": {
				Type: ColumnDefinitionTypeInt,
			},
			"finops_band": {
				Type: ColumnDefinitionTypeString,
			},
			"finops_score_breakdown": {
				Type: ColumnDefinitionTypeJson,
			},
			"deleted_version": {
				Type: ColumnDefinitionTypeFloat,
				Def:  "COALESCE(NULLIF(r1.recommendation ->> 'deleted_version', '')::FLOAT, 0)",
			},
			"deprecated_version": {
				Type: ColumnDefinitionTypeFloat,
				Def:  "COALESCE(NULLIF(r1.recommendation ->> 'deprecated_version', '')::FLOAT, 0)",
			},
		},
	},
	"recommendation_misconfig_groupings_v2": {
		Type:   Aggregate,
		Source: database.Metastore,
		Def: `(select r.tenant_id
				, r.cloud_account_id
				, r.resource_id
				, r.category
				, r.rule_name
				, r.status
				, r.created_at
				, r.updated_at
				, r.estimated_savings
				, r.account_object_id
				,(case when severity = 'Critical' then 10 
						when severity = 'High' then 8 
						when severity = 'Medium' then 5 
						when severity = 'Low' then 2 
						when severity = 'Info' then 1 
						else 0 end
				) as severity_weight
				,rr ->> 'category' as missconfig_category
				,rr ->> 'message' as missconfig_message
				,rr ->> 'kind' as resource_type
				,rr ->> 'name' as resource_name
				,rr ->> 'container' as resource_k8s_container
				,rr ->> 'namespace' as resource_k8s_namespace
			from recommendation r,
				lateral jsonb_array_elements(r.recommendation) as rr
			where r.category = 'Configuration' and r.rule_name = 'misconfigurations'
			) as rrr`,
		Name:                "recommendation_misconfig_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		NamespaceColumnName: "resource_k8s_namespace",
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"resource_id": {
				Type: ColumnDefinitionTypeString,
			},
			"category": {
				Type: ColumnDefinitionTypeString,
			},
			"rule_name": {
				Type: ColumnDefinitionTypeString,
			},
			"status": {
				Type: ColumnDefinitionTypeString,
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"updated_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"missconfig_category": {
				Type: ColumnDefinitionTypeString,
			},
			"missconfig_message": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_type": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_k8s_namespace": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_k8s_container": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_name": {
				Type: ColumnDefinitionTypeString,
			},
			"estimated_savings": {
				Type: ColumnDefinitionTypeFloat,
			},
			"account_object_id": {
				Type: ColumnDefinitionTypeString,
			},
			"count": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "count(*)",
				IsAggregated: true,
			},
			"sum_estimated_savings": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(estimated_savings)",
				IsAggregated: true,
			},
		},
	},
	"recommendation_misconfigs_v2": {
		Type:   Normal,
		Source: database.Metastore,
		Def: `(select r.tenant_id
				, r.cloud_account_id
				, r.resource_id
				, r.category
				, r.rule_name
				, r.status
				, r.estimated_savings
				, r.account_object_id
				, r.created_at
				, r.updated_at
				,(case when severity = 'Critical' then 10 
						when severity = 'High' then 8 
						when severity = 'Medium' then 5 
						when severity = 'Low' then 2 
						when severity = 'Info' then 1 
						else 0 end
				) as severity_weight
				,rr ->> 'category' as missconfig_category
				,rr ->> 'message' as missconfig_message
				,rr ->> 'kind' as resource_type
				,rr ->> 'name' as resource_name
				,rr ->> 'container' as resource_k8s_container
				,rr ->> 'namespace' as resource_k8s_namespace
			from recommendation r,
				lateral jsonb_array_elements(r.recommendation) as rr
			where r.category = 'Configuration' and r.rule_name = 'misconfigurations'
			) as rrr`,
		Name:                "recommendation_misconfigs_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		NamespaceColumnName: "resource_k8s_namespace",
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"resource_id": {
				Type: ColumnDefinitionTypeString,
			},
			"category": {
				Type: ColumnDefinitionTypeString,
			},
			"rule_name": {
				Type: ColumnDefinitionTypeString,
			},
			"status": {
				Type: ColumnDefinitionTypeString,
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"updated_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"missconfig_category": {
				Type: ColumnDefinitionTypeString,
			},
			"missconfig_message": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_type": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_k8s_namespace": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_k8s_container": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_name": {
				Type: ColumnDefinitionTypeString,
			},
			"estimated_savings": {
				Type: ColumnDefinitionTypeFloat,
			},
			"account_object_id": {
				Type: ColumnDefinitionTypeString,
			},
		},
	},
	"slo_report_observation_v2": {
		Type:   Normal,
		Source: database.Metastore,
		Def: `(SELECT sc.name AS config_name,
		sr.workload_namespace,
		sr.workload_name,
		sr.cloud_account_id,
		sr.tenant_id,
		MAX(sr.timestamp) AS timestamp,
		SUM(sr.good_events_count) AS total_good_events,
		SUM(sr.bad_events_count) AS total_bad_events,
		SUM(sr.events_count) AS total_events,
		sr.status AS status
		FROM slo_report sr
			JOIN slo_config sc
				ON sr.config_id = sc.id
		GROUP BY sc.name,
		  sr.workload_namespace,
		  sr.workload_name,
		  sr.cloud_account_id,
		  sr.tenant_id,
		  sr.timestamp, sr.status) as sro`,
		Name:                "slo_report_observation_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "cloud_account_id",
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"timestamp": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "timestamp",
			},
			"workload_namespace": {
				Type: ColumnDefinitionTypeString,
			},
			"workload_name": {
				Type: ColumnDefinitionTypeString,
			},
			"config_name": {
				Type: ColumnDefinitionTypeString,
			},
			"total_good_events": {
				Type:         ColumnDefinitionTypeString,
				IsAggregated: true,
				Def:          `SUM(total_good_events)`,
			},
			"total_bad_events": {
				Type:         ColumnDefinitionTypeString,
				IsAggregated: true,
				Def:          `SUM(total_bad_events)`,
			},
			"total_events": {
				Type:         ColumnDefinitionTypeString,
				IsAggregated: true,
				Def:          `SUM(total_events)`,
			},
			"status": {
				Type: ColumnDefinitionTypeString,
			},
		},
	},
	"anomaly_v2": {
		Type:                Normal,
		Source:              database.Metastore,
		Def:                 `anomaly`,
		Name:                "anomaly_v2",
		TenantIdColumnName:  "tenant",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"tenant": {
				Type: ColumnDefinitionTypeString,
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
			},
			"id": {
				Type: ColumnDefinitionTypeString,
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"updated_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"namespace": {
				Type: ColumnDefinitionTypeString,
			},
			"name": {
				Type: ColumnDefinitionTypeString,
			},
			"reference_value": {
				Type: ColumnDefinitionTypeString,
			},
			"current_value": {
				Type: ColumnDefinitionTypeString,
			},
			"anomaly_type": {
				Type: ColumnDefinitionTypeString,
			},
			"is_anomaly": {
				Type: ColumnDefinitionTypeBoolean,
			},
			"evaluated_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"config_id": {
				Type: ColumnDefinitionTypeString,
			},
			"insights": {
				Type: ColumnDefinitionTypeJson,
			},
		},
	},
	"anomaly_grouping_v2": {
		Type:                Normal,
		Source:              database.Metastore,
		Def:                 `anomaly`,
		Name:                "anomaly_grouping_v2",
		TenantIdColumnName:  "tenant",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"tenant": {
				Type: ColumnDefinitionTypeString,
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"updated_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"namespace": {
				Type: ColumnDefinitionTypeString,
			},
			"name": {
				Type: ColumnDefinitionTypeString,
			},
			"anomaly_type": {
				Type: ColumnDefinitionTypeString,
			},
			"is_anomaly": {
				Type: ColumnDefinitionTypeBoolean,
			},
			"evaluated_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"config_id": {
				Type: ColumnDefinitionTypeString,
			},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"anomaly_v3": {
		Type:   Normal,
		Source: database.Metastore,
		Def: `(
			SELECT Count (*) as anomaly_count, namespace, name, anomaly_type, tenant, account_id, Max(evaluated_at) as evaluated_at
			FROM anomaly 
			WHERE evaluated_at >= NOW() - INTERVAL '1 month'
			GROUP BY namespace, name, anomaly_type, account_id, tenant ORDER BY MAX(evaluated_at) DESC) as anomaly_group`,
		Name:                "anomaly_v3",
		TenantIdColumnName:  "tenant",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"tenant": {
				Type: ColumnDefinitionTypeString,
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
			},
			"namespace": {
				Type: ColumnDefinitionTypeString,
			},
			"name": {
				Type: ColumnDefinitionTypeString,
			},
			"anomaly_type": {
				Type: ColumnDefinitionTypeString,
			},
			"anomaly_count": {
				Type: ColumnDefinitionTypeInt,
				Def:  "anomaly_count",
			},
			"evaluated_at": {
				Type: ColumnDefinitionTypeString,
			},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"resource_groupings_v2": {
		Type:   Aggregate,
		Source: database.Metastore,
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			var baseQuery string

			spendColumns := []string{"spend_amount", "spend_date"}
			recommendationColumns := []string{
				"recommendation_rule_name", "recommendation_category", "recommendation_status",
				"recommendation_severity", "recommendation_estimated_savings",
			}

			resourcesColumn := []string{
				"resource_id", "resource_name",
				"resource_status", "resource_type", "resource_region", "resource_arn", "resource_service_name", "resource_tags",
			}

			// allColumnsMatch returns true if every Binary column in the clause tree
			// belongs to matchColumns. Used to decide if _or/_not can be safely split.
			var allColumnsMatch func(where QueryWhereClause, matchColumns []string) bool
			allColumnsMatch = func(where QueryWhereClause, matchColumns []string) bool {
				for col := range where.Binary {
					if !lo.Contains(matchColumns, col) {
						return false
					}
				}
				for _, andClause := range where.And {
					if !allColumnsMatch(andClause, matchColumns) {
						return false
					}
				}
				for _, orClause := range where.Or {
					if !allColumnsMatch(orClause, matchColumns) {
						return false
					}
				}
				if where.Not != nil {
					if !allColumnsMatch(*where.Not, matchColumns) {
						return false
					}
				}
				return true
			}

			// splitWhereClause recursively separates matched columns from the rest,
			// handling nested _and/_or/_not clauses.
			// For _and, each child is split independently (safe because AND distributes).
			// For _or/_not, the whole subtree is kept intact unless ALL descendants
			// belong to matchColumns, to avoid changing OR/NOT semantics.
			var splitWhereClause func(where QueryWhereClause, matchColumns []string) (matched QueryWhereClause, remaining QueryWhereClause)
			splitWhereClause = func(where QueryWhereClause, matchColumns []string) (QueryWhereClause, QueryWhereClause) {
				matched := QueryWhereClause{Binary: make(BinaryWhereClause)}
				remaining := QueryWhereClause{Binary: make(BinaryWhereClause)}

				for col, clauses := range where.Binary {
					if lo.Contains(matchColumns, col) {
						matched.Binary[col] = clauses
					} else {
						remaining.Binary[col] = clauses
					}
				}

				for _, andClause := range where.And {
					matchChild, restChild := splitWhereClause(andClause, matchColumns)
					if hasFilters(matchChild) {
						matched.And = append(matched.And, matchChild)
					}
					if hasFilters(restChild) {
						remaining.And = append(remaining.And, restChild)
					}
				}

				// For OR: only split if ALL branches belong to the same family,
				// otherwise keep the entire OR in remaining to preserve semantics
				if len(where.Or) > 0 {
					allMatch := true
					for _, orClause := range where.Or {
						if !allColumnsMatch(orClause, matchColumns) {
							allMatch = false
							break
						}
					}
					if allMatch {
						matched.Or = append(matched.Or, where.Or...)
					} else {
						remaining.Or = append(remaining.Or, where.Or...)
					}
				}

				// For NOT: only split if all descendants belong to the same family
				if where.Not != nil {
					if allColumnsMatch(*where.Not, matchColumns) {
						matched.Not = where.Not
					} else {
						remaining.Not = where.Not
					}
				}

				return matched, remaining
			}

			// renameWhereColumns recursively renames column keys by stripping a prefix
			var renameWhereColumns func(where QueryWhereClause, prefix string) QueryWhereClause
			renameWhereColumns = func(where QueryWhereClause, prefix string) QueryWhereClause {
				renamed := QueryWhereClause{Binary: make(BinaryWhereClause)}
				for col, clauses := range where.Binary {
					newCol := strings.ReplaceAll(col, prefix, "")
					renamed.Binary[newCol] = clauses
				}
				for _, andClause := range where.And {
					renamed.And = append(renamed.And, renameWhereColumns(andClause, prefix))
				}
				for _, orClause := range where.Or {
					renamed.Or = append(renamed.Or, renameWhereColumns(orClause, prefix))
				}
				if where.Not != nil {
					child := renameWhereColumns(*where.Not, prefix)
					renamed.Not = &child
				}
				return renamed
			}

			// whereReferencesColumns returns true if any Binary column in the clause
			// tree is present in the given column set. Used to detect unsupported
			// mixed-family filters that survived pushdown splitting.
			// spend/recommendation columns are always pushdown-only (never on outer query).
			// resource columns are only pushdown-only for the service-rollup shape
			// (which drops individual resource fields from its projection).
			isServiceRollup := !slices.Contains(request.GroupBy, "resource_id") &&
				!slices.Contains(request.GroupBy, "resource_name") &&
				slices.Contains(request.GroupBy, "resource_service_name")
			pushdownOnlyColumns := append(append([]string{}, spendColumns...), recommendationColumns...)
			if isServiceRollup {
				pushdownOnlyColumns = append(pushdownOnlyColumns,
					"resource_id", "resource_name",
					"resource_status", "resource_type", "resource_region", "resource_arn", "resource_tags",
				)
			}
			var whereReferencesColumns func(where QueryWhereClause, cols []string) bool
			whereReferencesColumns = func(where QueryWhereClause, cols []string) bool {
				for col := range where.Binary {
					if lo.Contains(cols, col) {
						return true
					}
				}
				for _, andClause := range where.And {
					if whereReferencesColumns(andClause, cols) {
						return true
					}
				}
				for _, orClause := range where.Or {
					if whereReferencesColumns(orClause, cols) {
						return true
					}
				}
				if where.Not != nil {
					if whereReferencesColumns(*where.Not, cols) {
						return true
					}
				}
				return false
			}

			// Split spend filters
			spendWhereClause, afterSpend := splitWhereClause(request.Where, spendColumns)

			// Split recommendation filters from the remaining
			recommendationWhereClause, afterRec := splitWhereClause(afterSpend, recommendationColumns)

			// Split resource filters from the remaining
			resourcesWhereClause, remaining := splitWhereClause(afterRec, resourcesColumn)
			// Strip "resource_" prefix from column names to match actual DB columns
			resourcesWhereClause = renameWhereColumns(resourcesWhereClause, "resource_")

			// Reject if remaining contains mixed-family _or/_not filters that reference
			// pushdown-only columns — these can't be evaluated on the outer query
			if whereReferencesColumns(remaining, pushdownOnlyColumns) {
				return "", request, fmt.Errorf("unsupported mixed _or/_not filter across resource/spend/recommendation columns in resource_groupings_v2")
			}

			request.Where = remaining
			var spendsWhereStr, recommendationWhereStr, resourceWhereStr string
			var err error
			if hasFilters(spendWhereClause) {
				tempTableDef := TableDefinition{
					Columns: map[string]ColumnDefinition{
						"spend_amount": {
							Type: ColumnDefinitionTypeFloat,
						},
						"spend_date": {
							Type: ColumnDefinitionTypeDatetime,
						},
					},
					Type:   Normal,
					Def:    "spends",
					Name:   "spends",
					Source: database.Metastore,
				}
				spendsWhereStr, err = generateWhereClause(spendWhereClause, tempTableDef)
				if err != nil {
					return "", request, fmt.Errorf("failed to generate spends where clause: %w", err)
				}
			} else {
				spendsWhereStr = "1 = 1"
			}

			if hasFilters(recommendationWhereClause) {
				tempTableDef := TableDefinition{
					Columns: map[string]ColumnDefinition{
						"recommendation_rule_name": {
							Type: ColumnDefinitionTypeString,
						},
						"recommendation_category": {
							Type: ColumnDefinitionTypeString,
						},
						"recommendation_status": {
							Type: ColumnDefinitionTypeString,
						},
						"recommendation_severity": {
							Type: ColumnDefinitionTypeString,
						},
						"recommendation_estimated_savings": {
							Type: ColumnDefinitionTypeString,
						},
					},
					Type:   Normal,
					Def:    "recommendation",
					Name:   "recommendation",
					Source: database.Metastore,
				}
				recommendationWhereStr, err = generateWhereClause(recommendationWhereClause, tempTableDef)
				if err != nil {
					return "", request, fmt.Errorf("failed to generate recommendations where clause: %w", err)
				}
			} else {
				recommendationWhereStr = "1 = 1"
			}

			if hasFilters(resourcesWhereClause) {
				tempTableDef := TableDefinition{
					Columns: map[string]ColumnDefinition{
						"id": {
							Type: ColumnDefinitionTypeString,
						},
						"name": {
							Type: ColumnDefinitionTypeString,
						},
						"status": {
							Type: ColumnDefinitionTypeString,
						},
						"type": {
							Type: ColumnDefinitionTypeString,
						},
						"arn": {
							Type: ColumnDefinitionTypeString,
						},
						"region": {
							Type: ColumnDefinitionTypeString,
						},
						"service_name": {
							Type: ColumnDefinitionTypeString,
						},
						"tags": {
							Type: ColumnDefinitionTypeJson,
						},
					},
					Type:   Normal,
					Def:    "cloud_resourses",
					Name:   "cloud_resourses",
					Source: database.Metastore,
				}
				resourceWhereStr, err = generateWhereClause(resourcesWhereClause, tempTableDef)
				if err != nil {
					return "", request, fmt.Errorf("failed to generate resources where clause: %w", err)
				}
			} else {
				resourceWhereStr = "1 = 1"
			}

			// Push account_id and tenant_id into the CTE so the planner can use
			// the account/tenant index before the full status+type scan.
			// extractFilterSQL also removes them from request.Where to avoid
			// redundant re-application on the outer query.
			resourceWhereStr += extractFilterSQL(&request, "account_id", "account")
			resourceWhereStr += extractFilterSQL(&request, "tenant_id", "tenant")

			// Skip joins that aren't needed by this request — avoids scanning
			// spends/recommendation when the caller only wants resource counts.
			needsSpend := hasFilters(spendWhereClause) || requestReferencesColumns(request, map[string]bool{
				"spend_date": true, "spend_amount": true, "sum_spend_amount": true,
			})
			needsRec := hasFilters(recommendationWhereClause) || requestReferencesColumns(request, map[string]bool{
				"recommendation_rule_name": true, "recommendation_category": true,
				"recommendation_status": true, "recommendation_severity": true,
				"recommendation_count": true, "recommendation_estimated_savings": true,
				"count_recommendation": true, "sum_recommendation_estimated_savings": true,
			})

			spendSelect := "NULL::float as spend_amount"
			recSelect := "NULL::int as recommendation_count, NULL::float as recommendation_estimated_savings"
			var spendJoin, recJoin string
			if needsSpend {
				spendSelect = "s.spend_amount::float"
			}
			if needsRec {
				recSelect = "r.recommendation_count::int, r.recommendation_estimated_savings::float"
			}

			if isServiceRollup {
				if needsSpend {
					spendJoin = `left join (select sum(spends1.spend_amount) as spend_amount, cr2.tenant, cr2.service_name, spends1.cloud_account from (select amount as spend_amount, "date" as spend_date, cloud_resource_id, cloud_account from spends) spends1 join resource_base cr2 on cr2.id = spends1.cloud_resource_id and cr2.account = spends1.cloud_account where __spends__where__ group by cr2.tenant, cr2.service_name, spends1.cloud_account) s on s.tenant = cr.tenant and s.service_name = cr.service_name and s.cloud_account = cr.account`
				}
				if needsRec {
					recJoin = `left join (select count(*) as recommendation_count, sum(r1.recommendation_estimated_savings) as recommendation_estimated_savings, r1.cloud_account_id, cr3.tenant, cr3.service_name from (select id as recommendation_id, rule_name as recommendation_rule_name, category as recommendation_category, status as recommendation_status, severity as recommendation_severity, estimated_savings as recommendation_estimated_savings, resource_id, cloud_account_id from recommendation) r1 join resource_base cr3 on cr3.id = r1.resource_id and cr3.account = r1.cloud_account_id where __recommendations__where__ group by cr3.tenant, cr3.service_name, r1.cloud_account_id) r on r.tenant = cr.tenant and r.service_name = cr.service_name and r.cloud_account_id = cr.account`
				}
				baseQuery = fmt.Sprintf(`(
					with resource_base as (
						select tenant, account, id, service_name from cloud_resourses where __resources__where__
					)
					select cr.tenant as tenant_id, cr.account as account_id, cr.service_name
						, resource_count::int
						, %s
						, %s
					from (select tenant, account, service_name, count(*) as resource_count from resource_base group by tenant, account, service_name) cr
					%s
					%s
				) as resource_group`, spendSelect, recSelect, spendJoin, recJoin)
			} else {
				if needsSpend {
					spendJoin = `left join (select sum(spend_amount) as spend_amount, cloud_resource_id, cloud_account from (select amount as spend_amount, "date" as spend_date, cloud_resource_id, cloud_account from spends) spends1 where __spends__where__ group by cloud_resource_id, cloud_account) s on s.cloud_resource_id = cr.id and s.cloud_account = cr.account`
				}
				if needsRec {
					recJoin = `left join (select count(*) as recommendation_count, sum(recommendation_estimated_savings) as recommendation_estimated_savings, resource_id, cloud_account_id from (select id as recommendation_id, rule_name as recommendation_rule_name, category as recommendation_category, status as recommendation_status, severity as recommendation_severity, estimated_savings as recommendation_estimated_savings, resource_id, cloud_account_id from recommendation ) r1 where __recommendations__where__ group by resource_id, cloud_account_id ) r on r.resource_id = cr.id and r.cloud_account_id = cr.account`
				}
				baseQuery = fmt.Sprintf(`(
					select cr.tenant as tenant_id, cr.account as account_id, cr.id, cr.name, cr.service_name, cr.status, cr."type", cr.region, cr.arn, cr.tags
						, cr.resource_count::int
						, %s
						, %s
					from (select tenant, account, id, name, service_name, status, "type", region, arn, tags, count(*) as resource_count from cloud_resourses cr1 where __resources__where__ group by tenant, account, id, name, service_name, status, "type", region, arn, tags) cr
					%s
					%s
				) as resource_group`, spendSelect, recSelect, spendJoin, recJoin)
			}

			baseQuery = strings.ReplaceAll(baseQuery, "__resources__where__", resourceWhereStr)
			baseQuery = strings.ReplaceAll(baseQuery, "__spends__where__", spendsWhereStr)
			baseQuery = strings.ReplaceAll(baseQuery, "__recommendations__where__", recommendationWhereStr)

			return baseQuery, request, nil
		},
		Name:                "resource_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
			},
			"resource_service_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "service_name",
			},
			"resource_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"resource_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "name",
			},
			"resource_status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"resource_type": {
				Type: ColumnDefinitionTypeString,
				Def:  "type",
			},
			"resource_region": {
				Type: ColumnDefinitionTypeString,
				Def:  "region",
			},
			"resource_arn": {
				Type: ColumnDefinitionTypeString,
				Def:  "arn",
			},
			"resource_tags": {
				Type: ColumnDefinitionTypeJson,
				Def:  "tags",
			},
			"spend_date": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"recommendation_rule_name": {
				Type: ColumnDefinitionTypeString,
			},
			"recommendation_category": {
				Type: ColumnDefinitionTypeString,
			},
			"recommendation_status": {
				Type: ColumnDefinitionTypeString,
			},
			"recommendation_severity": {
				Type: ColumnDefinitionTypeString,
			},
			"count_resource": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "sum(resource_count)",
				IsAggregated: true,
			},
			"sum_spend_amount": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(spend_amount)",
				IsAggregated: true,
			},
			"count_recommendation": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "sum(recommendation_count)",
				IsAggregated: true,
			},
			"sum_recommendation_estimated_savings": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(recommendation_estimated_savings)",
				IsAggregated: true,
			},
		},
	},
	"application_profile_v2": {
		Type:                Normal,
		Source:              database.Metastore,
		Def:                 `application_profile`,
		Name:                "application_profile_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "cloud_account_id",
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
			},
			"cloud_account_id": {
				Type: ColumnDefinitionTypeString,
			},
			"pod_name": {
				Type: ColumnDefinitionTypeString,
			},
			"workload_name": {
				Type: ColumnDefinitionTypeString,
			},
			"namespace": {
				Type: ColumnDefinitionTypeString,
			},
			"created_by": {
				Type: ColumnDefinitionTypeString,
			},
			"profile": {
				Type: ColumnDefinitionTypeJson,
			},
			"source": {
				Type: ColumnDefinitionTypeString,
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"updated_at": {
				Type: ColumnDefinitionTypeDatetime,
			},
			"profile_type": {
				Type: ColumnDefinitionTypeString,
			},
			"profile_duration": {
				Type: ColumnDefinitionTypeInt,
			},
			"profile_language": {
				Type: ColumnDefinitionTypeString,
			},
			"profile_tool": {
				Type: ColumnDefinitionTypeString,
			},
			"output_type": {
				Type: ColumnDefinitionTypeString,
			},
		},
	},
	"llm_rag_grouping_v2": {
		Type:                Aggregate,
		Source:              database.Metastore,
		Def:                 `llm_rags`,
		Name:                "llm_rag_grouping_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id", // uuid
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString, // uuid
				Def:  "tenant_id",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_id", // uuid
			},
			"agent_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "agent_id", // varchar
			},
			"data": {
				Type: ColumnDefinitionTypeString,
				Def:  "data", // text
			},
			"created_by": {
				Type: ColumnDefinitionTypeString,
				Def:  "created_by", // uuid
			},
			"updated_by": {
				Type: ColumnDefinitionTypeString,
				Def:  "updated_by", // uuid
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at", // timestamp
			},
			"updated_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "updated_at", // timestamp
			},
			"data_format": {
				Type: ColumnDefinitionTypeString,
				Def:  "data_format",
			},
			"data_filename": {
				Type: ColumnDefinitionTypeString,
				Def:  "data_filename",
			},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(agent_id)",
				IsAggregated: true,
			},
		},
	},
	"admin_get_user_tenant_roles_v2": {
		Type:               Derived,
		Source:             database.Metastore,
		Name:               "admin_get_user_tenant_roles_v2",
		TenantIdColumnName: "tenant_id",
		Def: `(
			SELECT
				ur.entity_id::text as entity_id,
				ur.entity_type,
				ur.role,
				ur.tenant_id::text as tenant_id,
				u.username as username
			FROM user_roles ur
			INNER JOIN users u ON u.id = ur.user_id
			UNION ALL
			SELECT
				gr.entity_id,
				gr.entity_type,
				gr.role,
				ug.tenant::text AS tenant_id,
				u.username as username
			FROM group_roles gr
			INNER JOIN user_groups ug ON ug.id = gr.group_id
			INNER JOIN usergroup_users ugu ON ugu."group" = ug.id
			INNER JOIN users u ON u.id = ugu."user"
		) as user_tenant_roles`,
		Columns: map[string]ColumnDefinition{
			"entity_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "entity_id",
			},
			"entity_type": {
				Type: ColumnDefinitionTypeString,
				Def:  "entity_type",
			},
			"role": {
				Type: ColumnDefinitionTypeString,
				Def:  "role",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
		},
	},
	"admin_get_users_by_tenant_v2": {
		Type:               Derived,
		Source:             database.Metastore,
		TenantIdColumnName: "tenant_id",
		Name:               "admin_get_users_by_tenant_v2",
		Def: `(
			SELECT
				u.id::text as id,
				u.display_name,
				u.status::text as status,
				u.username as username,
				u.created_at,
				tu.tenant::text as tenant_id,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'id', ur.id,
						'role', ur.role,
						'entity_type', ur.entity_type,
						'entity_id', ur.entity_id,
						'role_display_name', COALESCE(r.display_name, ur.role)
					))
					FROM user_roles ur
					LEFT JOIN roles r ON r.value = ur.role
					WHERE ur.user_id = u.id AND ur.entity_id = tu.tenant::text),
					'[]'::json
				) as user_roles,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'name', ug.name,
						'id', ug.id,
						'group_roles', COALESCE(
							(SELECT json_agg(json_build_object('role', gr.role))
							FROM group_roles gr WHERE gr.group_id = ug.id),
							'[]'::json
						)
					))
					FROM usergroup_users ugu
					INNER JOIN user_groups ug ON ug.id = ugu."group"
					WHERE ugu."user" = u.id AND ug.tenant = tu.tenant),
					'[]'::json
				) as user_groups,
				(SELECT ua.accessed_at
				 FROM user_auths ua
				 WHERE ua."user" = u.id AND ua.tenant_id = tu.tenant
				 ORDER BY ua.accessed_at DESC
				 LIMIT 1
				) as last_accessed_at
			FROM users u
			INNER JOIN tenant_users tu ON tu."user" = u.id
		) as users_by_tenant`,
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"display_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "display_name",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"username": {
				Type: ColumnDefinitionTypeString,
				Def:  "username",
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"user_roles": {
				Type: ColumnDefinitionTypeJson,
				Def:  "user_roles",
			},
			"user_groups": {
				Type: ColumnDefinitionTypeJson,
				Def:  "user_groups",
			},
			"last_accessed_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "last_accessed_at",
			},
		},
	},
	"admin_get_users_grouping_by_tenant_v2": {
		Type:               Aggregate,
		Source:             database.Metastore,
		TenantIdColumnName: "tenant_id",
		Name:               "admin_get_users_grouping_by_tenant_v2",
		Def: `(
			SELECT
				u.id::text as id,
				u.display_name,
				u.status::text as status,
				u.username as username,
				u.created_at,
				tu.tenant::text as tenant_id
			FROM users u
			INNER JOIN tenant_users tu ON tu."user" = u.id
		) as users_grouping`,
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"display_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "display_name",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"username": {
				Type: ColumnDefinitionTypeString,
				Def:  "username",
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"admin_get_user_groups_v2": {
		Type:               Derived,
		Source:             database.Metastore,
		TenantIdColumnName: "tenant_id",
		Name:               "admin_get_user_groups_v2",
		Def: `(
			SELECT
				ug.id::text as id,
				ug.name,
				ug.description,
				ug.owner::text as owner,
				ug.created_at,
				ug.tenant::text as tenant_id,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'role', gr.role,
						'entity_type', gr.entity_type,
						'entity_id', gr.entity_id
					))
					FROM group_roles gr
					WHERE gr.group_id = ug.id),
					'[]'::json
				) as group_roles,
				u.display_name as owner_display_name,
				(SELECT COUNT(*)
				 FROM usergroup_users ugu
				 WHERE ugu."group" = ug.id
				) as member_count
			FROM user_groups ug
			LEFT JOIN users u ON u.id = ug.owner
		) as user_groups_v2`,
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"name": {
				Type: ColumnDefinitionTypeString,
				Def:  "name",
			},
			"description": {
				Type: ColumnDefinitionTypeString,
				Def:  "description",
			},
			"owner": {
				Type: ColumnDefinitionTypeString,
				Def:  "owner",
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"group_roles": {
				Type: ColumnDefinitionTypeJson,
				Def:  "group_roles",
			},
			"owner_display_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "owner_display_name",
			},
			"member_count": {
				Type: ColumnDefinitionTypeInt,
				Def:  "member_count",
			},
		},
	},
	"admin_get_user_groups_grouping_v2": {
		Type:               Aggregate,
		Source:             database.Metastore,
		TenantIdColumnName: "tenant_id",
		Name:               "admin_get_user_groups_grouping_v2",
		Def: `(
			SELECT
				ug.id::text as id,
				ug.name,
				ug.description,
				ug.owner::text as owner,
				ug.created_at,
				ug.tenant::text as tenant_id
			FROM user_groups ug
		) as user_groups_grouping`,
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"name": {
				Type: ColumnDefinitionTypeString,
				Def:  "name",
			},
			"description": {
				Type: ColumnDefinitionTypeString,
				Def:  "description",
			},
			"owner": {
				Type: ColumnDefinitionTypeString,
				Def:  "owner",
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"admin_get_notification_rules_v2": {
		Type:               Derived,
		Source:             database.Metastore,
		TenantIdColumnName: "tenant_id",
		Name:               "admin_get_notification_rules_v2",
		Def: `(
			SELECT
					nr.id::text as id,
					nr.account_id::text as account_id,
					nr.source,
					nr.created_at,
					nr.cluster,
					nr.description,
					nr.aggregation_key,
					nr.expires_at,
					nr.workload,
					nr.name,
					nr.namespace,
					nr.is_suppressed,
					nr.created_by::text as created_by,
					nr.severity,
					nr.delivery_mode,
					nr.frequency,
					nr.tenant_id::text as tenant_id,
					u.display_name as created_by_display_name,
					COALESCE(nrm.mappings, '[]'::json) as notification_rule_mappings
			FROM notification_rules nr
			LEFT JOIN users u ON u.id = nr.created_by
			LEFT JOIN (
					SELECT
							rule_id,
							json_agg(json_build_object(
									'id', id,
									'channels', channels,
									'platform', platform
							)) as mappings
					FROM notification_rule_mappings
					GROUP BY rule_id
			) nrm ON nrm.rule_id = nr.id
	) as notification_rules_v2`,
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_id",
			},
			"source": {
				Type: ColumnDefinitionTypeString,
				Def:  "source",
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at",
			},
			"cluster": {
				Type: ColumnDefinitionTypeString,
				Def:  "cluster",
			},
			"description": {
				Type: ColumnDefinitionTypeString,
				Def:  "description",
			},
			"aggregation_key": {
				Type: ColumnDefinitionTypeString,
				Def:  "aggregation_key",
			},
			"expires_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "expires_at",
			},
			"workload": {
				Type: ColumnDefinitionTypeString,
				Def:  "workload",
			},
			"name": {
				Type: ColumnDefinitionTypeString,
				Def:  "name",
			},
			"namespace": {
				Type: ColumnDefinitionTypeString,
				Def:  "namespace",
			},
			"is_suppressed": {
				Type: ColumnDefinitionTypeBoolean,
				Def:  "is_suppressed",
			},
			"created_by": {
				Type: ColumnDefinitionTypeString,
				Def:  "created_by",
			},
			"severity": {
				Type: ColumnDefinitionTypeString,
				Def:  "severity",
			},
			"delivery_mode": {
				Type: ColumnDefinitionTypeString,
				Def:  "delivery_mode",
			},
			"frequency": {
				Type: ColumnDefinitionTypeString,
				Def:  "frequency",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"created_by_display_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "created_by_display_name",
			},
			"notification_rule_mappings": {
				Type: ColumnDefinitionTypeJson,
				Def:  "notification_rule_mappings",
			},
		},
	},
	"admin_get_notification_rules_grouping_v2": {
		Type:               Aggregate,
		Source:             database.Metastore,
		TenantIdColumnName: "tenant_id",
		Name:               "admin_notification_rules_grouping_v2",
		Def: `(
			SELECT
				nr.id::text as id,
				nr.account_id::text as account_id,
				nr.source,
				nr.created_at,
				nr.cluster,
				nr.name,
				nr.namespace,
				nr.is_suppressed,
				nr.severity,
				nr.delivery_mode,
				nr.tenant_id::text as tenant_id
			FROM notification_rules nr
		) as notification_rules_grouping`,
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_id",
			},
			"source": {
				Type: ColumnDefinitionTypeString,
				Def:  "source",
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at",
			},
			"cluster": {
				Type: ColumnDefinitionTypeString,
				Def:  "cluster",
			},
			"name": {
				Type: ColumnDefinitionTypeString,
				Def:  "name",
			},
			"namespace": {
				Type: ColumnDefinitionTypeString,
				Def:  "namespace",
			},
			"is_suppressed": {
				Type: ColumnDefinitionTypeBoolean,
				Def:  "is_suppressed",
			},
			"severity": {
				Type: ColumnDefinitionTypeString,
				Def:  "severity",
			},
			"delivery_mode": {
				Type: ColumnDefinitionTypeString,
				Def:  "delivery_mode",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"admin_get_integrations_v2": {
		Type:               Derived,
		Source:             database.Metastore,
		TenantIdColumnName: "tenant_id",
		Name:               "admin_get_integrations_v2",
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			// Extract optional config_value_name and config_value_value filters
			// These allow filtering integrations by their config values at the DB level
			configNameFilter := extractFilterSQL(&request, "config_value_name", "icv_filter.name")
			configValueFilter := extractFilterSQL(&request, "config_value_value", "icv_filter.value")

			existsClause := ""
			if configNameFilter != "" || configValueFilter != "" {
				existsClause = " AND EXISTS (SELECT 1 FROM integration_config_values icv_filter WHERE icv_filter.integration_id = i.id" + configNameFilter + configValueFilter + ")"
			}

			def := `(
			SELECT
				i.id::text as id,
				i.name,
				i.type,
				i.source,
				i.status,
				i.labels,
				i.created_at,
				i.updated_at,
				i.created_by::text as created_by,
				i.updated_by::text as updated_by,
				i.tenant_id::text as tenant_id,
				uc.display_name as created_by_display_name,
				uu.display_name as updated_by_display_name,
				COALESCE(ica_agg.cloud_accounts, '[]'::json) as integrations_cloud_accounts,
				COALESCE(icv_agg.config_values, '[]'::json) as integration_config_values
			FROM integrations i
			LEFT JOIN users uc ON uc.id = i.created_by
			LEFT JOIN users uu ON uu.id = i.updated_by
			LEFT JOIN (
				SELECT
					ica.integration_id,
					json_agg(json_build_object(
						'cloud_account_id', ica.cloud_account_id,
						'cloud_account_name', ca.account_name,
						'default_log_provider', ica.default_log_provider,
						'default_traces_provider', ica.default_traces_provider,
						'default_metrics_provider', ica.default_metrics_provider
					)) as cloud_accounts
				FROM integrations_cloud_accounts ica
				LEFT JOIN cloud_accounts ca ON ca.id = ica.cloud_account_id
				GROUP BY ica.integration_id
			) ica_agg ON ica_agg.integration_id = i.id
			LEFT JOIN (
				SELECT
					icv.integration_id,
					json_agg(json_build_object(
						'id', icv.id,
						'name', icv.name,
						'value', icv.value,
						'type', icv.type
					)) as config_values
				FROM integration_config_values icv
				GROUP BY icv.integration_id
			) icv_agg ON icv_agg.integration_id = i.id
			WHERE 1=1` + existsClause + `
		) as integrations_v2`
			return def, request, nil
		},
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"name": {
				Type: ColumnDefinitionTypeString,
				Def:  "name",
			},
			"type": {
				Type: ColumnDefinitionTypeString,
				Def:  "type",
			},
			"source": {
				Type: ColumnDefinitionTypeString,
				Def:  "source",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"labels": {
				Type: ColumnDefinitionTypeJson,
				Def:  "labels",
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at",
			},
			"updated_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "updated_at",
			},
			"created_by": {
				Type: ColumnDefinitionTypeString,
				Def:  "created_by",
			},
			"updated_by": {
				Type: ColumnDefinitionTypeString,
				Def:  "updated_by",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"created_by_display_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "created_by_display_name",
			},
			"updated_by_display_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "updated_by_display_name",
			},
			"integrations_cloud_accounts": {
				Type: ColumnDefinitionTypeJson,
				Def:  "integrations_cloud_accounts",
			},
			"integration_config_values": {
				Type: ColumnDefinitionTypeJson,
				Def:  "integration_config_values",
			},
		},
	},
	"admin_get_integrations_grouping_v2": {
		Type:               Aggregate,
		Source:             database.Metastore,
		TenantIdColumnName: "tenant_id",
		Name:               "admin_get_integrations_grouping_v2",
		Def: `(
			SELECT
				i.id::text as id,
				i.name,
				i.type,
				i.source,
				i.status,
				i.created_at,
				i.tenant_id::text as tenant_id
			FROM integrations i
		) as integrations_grouping`,
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"name": {
				Type: ColumnDefinitionTypeString,
				Def:  "name",
			},
			"type": {
				Type: ColumnDefinitionTypeString,
				Def:  "type",
			},
			"source": {
				Type: ColumnDefinitionTypeString,
				Def:  "source",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"get_cloud_accounts_v2": {
		Type:               Normal,
		Source:             database.Metastore,
		TenantIdColumnName: "tenant_id",
		Name:               "get_cloud_accounts_v2",
		Def: `(
			SELECT
				ca.account_name,
				ca.account_number,
				ca.id::text AS id,
				ca.created_at,
				ca.created_by,
				u.display_name AS created_by_name,
				ca.status,
				ca.cloud_provider,
				ca.tenant::text AS tenant_id,
				ca.account_type,
				ca.account_access,
				ca.synced_at,
				ca.agent_synced_at,
				ca.sync_status,
				ca.data,
				COALESCE(caa_agg.cloud_account_attrs, '[]'::json) AS cloud_account_attrs,
				COALESCE(ag.agents, '[]'::json) AS agents
			FROM cloud_accounts ca
			LEFT JOIN users u
				ON u.id = ca.created_by
			LEFT JOIN (
				SELECT cloud_account_id,
					json_agg(json_build_object(
						'id', caa.id,
						'name', caa.name,
						'value', caa.value
					)) AS cloud_account_attrs
				FROM cloud_account_attrs caa
				GROUP BY cloud_account_id
			) caa_agg ON caa_agg.cloud_account_id = ca.id
			LEFT JOIN (
				SELECT cloud_account_id,
					json_agg(json_build_object(
						'last_connected_at', last_connected_at,
						'status', status,
						'version', version,
						'connection_status', connection_status,
						'k8s_provider', k8s_provider,
						'k8s_version', k8s_version,
						'status_message', status_message,
						'last_synced_at', last_synced_at
					)) as agents
				FROM agent
				WHERE type NOT IN ('proxy', 'eventbridge', 'gcp_monitoring_webhook')
				GROUP BY cloud_account_id
			) ag ON ag.cloud_account_id = ca.id
		) as cloud_accounts_v2`,
		Columns: map[string]ColumnDefinition{
			"account_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_name",
			},
			"account_number": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_number",
			},
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at",
			},
			"created_by": {
				Type: ColumnDefinitionTypeString,
				Def:  "created_by",
			},
			"created_by_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "created_by_name",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"cloud_provider": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_provider",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"cloud_account_attrs": {
				Type: ColumnDefinitionTypeJson,
				Def:  "cloud_account_attrs",
			},
			"account_type": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_type",
			},
			"account_access": {
				Type: ColumnDefinitionTypeJson,
				Def:  "account_access",
			},
			"synced_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "synced_at",
			},
			"agent_synced_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "agent_synced_at",
			},
			"sync_status": {
				Type: ColumnDefinitionTypeString,
				Def:  "sync_status",
			},
			"agents": {
				Type: ColumnDefinitionTypeJson,
				Def:  "agents",
			},
			"data": {
				Type: ColumnDefinitionTypeJson,
				Def:  "data",
			},
		},
	},
	"get_cloud_accounts_grouping_v2": {
		Type:               Aggregate,
		Source:             database.Metastore,
		TenantIdColumnName: "tenant_id",
		Name:               "get_cloud_accounts_grouping_v2",
		Def: `(
			SELECT
				ca.id::text AS id,
				ca.account_name,
				ca.account_number,
				ca.status,
				ca.cloud_provider,
				ca.created_at,
				ca.created_by,
				ca.tenant::text AS tenant_id
			FROM cloud_accounts ca
		) as cloud_accounts_grouping_v2`,
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"account_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_name",
			},
			"account_number": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_number",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"cloud_provider": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_provider",
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at",
			},
			"created_by": {
				Type: ColumnDefinitionTypeString,
				Def:  "created_by",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"user_super_admin_role_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "user_super_admin_role_v2",
		Def: `(
			SELECT user_id::text as user_id, role::text as role
			FROM user_roles
			WHERE role IN ('super_admin', 'super_admin_readonly') AND tenant_id IS NULL
		) as super_admin_roles`,
		Columns: map[string]ColumnDefinition{
			"user_id": {Type: ColumnDefinitionTypeString, Def: "user_id"},
			"role":    {Type: ColumnDefinitionTypeString, Def: "role"},
		},
	},
	"user_account_ids_by_tenant_v2": {
		Type:               Normal,
		Source:             database.Metastore,
		Name:               "user_account_ids_by_tenant_v2",
		Def:                "cloud_accounts",
		TenantIdColumnName: "tenant",
		Columns: map[string]ColumnDefinition{
			"id":     {Type: ColumnDefinitionTypeString, Def: "id"},
			"tenant": {Type: ColumnDefinitionTypeString, Def: "tenant"},
		},
	},
	"tenant_attributes_v2": {
		Type:               Normal,
		Source:             database.Metastore,
		Name:               "tenant_attributes_v2",
		Def:                "tenant_attrs",
		TenantIdColumnName: "tenant_id",
		Columns: map[string]ColumnDefinition{
			"id":        {Type: ColumnDefinitionTypeString, Def: "id"},
			"name":      {Type: ColumnDefinitionTypeString, Def: "name"},
			"value":     {Type: ColumnDefinitionTypeString, Def: "value"},
			"tenant_id": {Type: ColumnDefinitionTypeString, Def: "tenant_id"},
		},
	},
	"user_details_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "user_details_v2",
		Def: `(
			SELECT
				u.id::text as id,
				u.display_name,
				u.username as username,
				u.status::text as status,
				u.created_at,
				u.updated_at,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'id', ur.id,
						'role', ur.role,
						'entity_type', ur.entity_type,
						'entity_id', ur.entity_id,
						'tenant_id', ur.tenant_id
					))
					FROM user_roles ur
					WHERE ur.user_id = u.id),
					'[]'::json
				) as user_roles,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'id', ua.id,
						'avatar', ua.avatar,
						'name', ua.name,
						'status', ua.status,
						'provider', ua.provider,
						'provider_type', ua.provider_type
					))
					FROM user_auths ua
					WHERE ua."user" = u.id),
					'[]'::json
				) as user_auths,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'id', tu.tenant,
						'is_default', tu.is_default,
						'name', t.name
					))
					FROM tenant_users tu
					JOIN tenant t ON t.id = tu.tenant
					WHERE tu."user" = u.id),
					'[]'::json
				) as tenants,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'name', ug.name,
						'id', ug.id,
						'description', ug.description,
						'group_roles', COALESCE(
							(SELECT json_agg(json_build_object(
								'role', gr.role,
								'entity_type', gr.entity_type,
								'entity_id', gr.entity_id
							))
							FROM group_roles gr WHERE gr.group_id = ug.id),
							'[]'::json
						)
					))
					FROM usergroup_users ugu
					INNER JOIN user_groups ug ON ug.id = ugu."group"
					WHERE ugu."user" = u.id),
					'[]'::json
				) as groups,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'id', uattr.id,
						'name', uattr.name,
						'value', uattr.value
					))
					FROM user_attrs uattr
					WHERE uattr."user" = u.id),
					'[]'::json
				) as user_attrs
			FROM users u
		) as user_details`,
		Columns: map[string]ColumnDefinition{
			"id":           {Type: ColumnDefinitionTypeString, Def: "id"},
			"display_name": {Type: ColumnDefinitionTypeString, Def: "display_name"},
			"username":     {Type: ColumnDefinitionTypeString, Def: "username"},
			"status":       {Type: ColumnDefinitionTypeString, Def: "status"},
			"created_at":   {Type: ColumnDefinitionTypeDatetime, Def: "created_at"},
			"updated_at":   {Type: ColumnDefinitionTypeDatetime, Def: "updated_at"},
			"user_roles":   {Type: ColumnDefinitionTypeJson, Def: "user_roles"},
			"user_auths":   {Type: ColumnDefinitionTypeJson, Def: "user_auths"},
			"tenants":      {Type: ColumnDefinitionTypeJson, Def: "tenants"},
			"groups":       {Type: ColumnDefinitionTypeJson, Def: "groups"},
			"user_attrs":   {Type: ColumnDefinitionTypeJson, Def: "user_attrs"},
		},
	},
	"user_by_provider_account_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "user_by_provider_account_v2",
		Def: `(
			SELECT
				ua_match.id::text as auth_id,
				u.id::text as id,
				u.display_name,
				u.username as username,
				u.status::text as status,
				u.created_at,
				u.updated_at,
				ua_match.account_id::text as account_id,
				ua_match.provider::text as provider,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'id', ur.id,
						'role', ur.role,
						'entity_type', ur.entity_type,
						'entity_id', ur.entity_id,
						'tenant_id', ur.tenant_id
					))
					FROM user_roles ur
					WHERE ur.user_id = u.id),
					'[]'::json
				) as user_roles,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'id', ua.id,
						'avatar', ua.avatar,
						'name', ua.name,
						'status', ua.status
					))
					FROM user_auths ua
					WHERE ua."user" = u.id),
					'[]'::json
				) as user_auths,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'id', tu.tenant,
						'is_default', tu.is_default,
						'name', t.name
					))
					FROM tenant_users tu
					JOIN tenant t ON t.id = tu.tenant
					WHERE tu."user" = u.id),
					'[]'::json
				) as tenants,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'name', ug.name,
						'id', ug.id,
						'group_roles', COALESCE(
							(SELECT json_agg(json_build_object(
								'role', gr.role,
								'entity_type', gr.entity_type,
								'entity_id', gr.entity_id
							))
							FROM group_roles gr WHERE gr.group_id = ug.id),
							'[]'::json
						)
					))
					FROM usergroup_users ugu
					INNER JOIN user_groups ug ON ug.id = ugu."group"
					WHERE ugu."user" = u.id),
					'[]'::json
				) as groups
			FROM user_auths ua_match
			JOIN users u ON u.id = ua_match."user"
		) as user_by_provider`,
		Columns: map[string]ColumnDefinition{
			"auth_id":      {Type: ColumnDefinitionTypeString, Def: "auth_id"},
			"id":           {Type: ColumnDefinitionTypeString, Def: "id"},
			"display_name": {Type: ColumnDefinitionTypeString, Def: "display_name"},
			"username":     {Type: ColumnDefinitionTypeString, Def: "username"},
			"status":       {Type: ColumnDefinitionTypeString, Def: "status"},
			"created_at":   {Type: ColumnDefinitionTypeDatetime, Def: "created_at"},
			"updated_at":   {Type: ColumnDefinitionTypeDatetime, Def: "updated_at"},
			"account_id":   {Type: ColumnDefinitionTypeString, Def: "account_id"},
			"provider":     {Type: ColumnDefinitionTypeString, Def: "provider"},
			"user_roles":   {Type: ColumnDefinitionTypeJson, Def: "user_roles"},
			"user_auths":   {Type: ColumnDefinitionTypeJson, Def: "user_auths"},
			"tenants":      {Type: ColumnDefinitionTypeJson, Def: "tenants"},
			"groups":       {Type: ColumnDefinitionTypeJson, Def: "groups"},
		},
	},
	"get_agent_health_v2": {
		Type:               Normal,
		Source:             database.Metastore,
		Name:               "get_agent_health_v2",
		TenantIdColumnName: "tenant_id",
		Def: `(
			SELECT
				id::text as id,
				cloud_account_id,
				tenant::text as tenant_id,
				type,
				version,
				status_message,
				status,
				last_connected_at,
				created_at,
				k8s_version,
				k8s_provider,
				connection_status
			FROM agent
			) as agent_health_v2`,
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"cloud_account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_account_id",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"type": {
				Type: ColumnDefinitionTypeString,
				Def:  "type",
			},
			"version": {
				Type: ColumnDefinitionTypeString,
				Def:  "version",
			},
			"status_message": {
				Type: ColumnDefinitionTypeString,
				Def:  "status_message",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"last_connected_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "last_connected_at",
			},
			"k8s_version": {
				Type: ColumnDefinitionTypeString,
				Def:  "k8s_version",
			},
			"k8s_provider": {
				Type: ColumnDefinitionTypeString,
				Def:  "k8s_provider",
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at",
			},
			"connection_status": {
				Type: ColumnDefinitionTypeJson,
				Def:  "connection_status",
			},
		},
	},
	"event_rules_v2": {
		Type:                Normal,
		Source:              database.Metastore,
		Def:                 "event_rules",
		Name:                "event_rules_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id":                     {Type: ColumnDefinitionTypeString},
			"created_at":             {Type: ColumnDefinitionTypeDatetime},
			"updated_at":             {Type: ColumnDefinitionTypeDatetime},
			"account_id":             {Type: ColumnDefinitionTypeString},
			"tenant_id":              {Type: ColumnDefinitionTypeString},
			"alert":                  {Type: ColumnDefinitionTypeString},
			"annotations":            {Type: ColumnDefinitionTypeJson},
			"expr":                   {Type: ColumnDefinitionTypeString},
			"duration":               {Type: ColumnDefinitionTypeString},
			"labels":                 {Type: ColumnDefinitionTypeJson},
			"source":                 {Type: ColumnDefinitionTypeString},
			"category":               {Type: ColumnDefinitionTypeString},
			"severity":               {Type: ColumnDefinitionTypeString},
			"enabled":                {Type: ColumnDefinitionTypeBoolean},
			"created_by":             {Type: ColumnDefinitionTypeString},
			"updated_by":             {Type: ColumnDefinitionTypeString},
			"is_editable":            {Type: ColumnDefinitionTypeBoolean},
			"group":                  {Type: ColumnDefinitionTypeString, Def: "\"group\""},
			"name":                   {Type: ColumnDefinitionTypeString},
			"namespace":              {Type: ColumnDefinitionTypeString},
			"alert_type":             {Type: ColumnDefinitionTypeString},
			"metric_provider":        {Type: ColumnDefinitionTypeString},
			"metric_provider_source": {Type: ColumnDefinitionTypeString},
			"provider_config":        {Type: ColumnDefinitionTypeJson},
			"external_rule_id":       {Type: ColumnDefinitionTypeString},
		},
	},
	"event_rules_groupings_v2": {
		Type:                Aggregate,
		Source:              database.Metastore,
		Def:                 "event_rules",
		Name:                "event_rules_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"tenant_id":  {Type: ColumnDefinitionTypeString},
			"account_id": {Type: ColumnDefinitionTypeString},
			"category":   {Type: ColumnDefinitionTypeString},
			"source":     {Type: ColumnDefinitionTypeString},
			"severity":   {Type: ColumnDefinitionTypeString},
			"enabled":    {Type: ColumnDefinitionTypeBoolean},
			"alert":      {Type: ColumnDefinitionTypeString},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"slo_config_v2": {
		Type:                Normal,
		Source:              database.Metastore,
		Def:                 "slo_config",
		Name:                "slo_config_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "cloud_account_id",
		Columns: map[string]ColumnDefinition{
			"id":                 {Type: ColumnDefinitionTypeString},
			"name":               {Type: ColumnDefinitionTypeString},
			"description":        {Type: ColumnDefinitionTypeString},
			"schedule":           {Type: ColumnDefinitionTypeString},
			"created_by":         {Type: ColumnDefinitionTypeString},
			"updated_by":         {Type: ColumnDefinitionTypeString},
			"filter_good_query":  {Type: ColumnDefinitionTypeString},
			"filter_bad_query":   {Type: ColumnDefinitionTypeString},
			"threshold":          {Type: ColumnDefinitionTypeString},
			"created_at":         {Type: ColumnDefinitionTypeDatetime},
			"updated_at":         {Type: ColumnDefinitionTypeDatetime},
			"method":             {Type: ColumnDefinitionTypeString},
			"histogram_query":    {Type: ColumnDefinitionTypeString},
			"cloud_account_id":   {Type: ColumnDefinitionTypeString},
			"tenant_id":          {Type: ColumnDefinitionTypeString},
			"window":             {Type: ColumnDefinitionTypeString, Def: "\"window\""},
			"workload_name":      {Type: ColumnDefinitionTypeString},
			"workload_namespace": {Type: ColumnDefinitionTypeString},
			"goal":               {Type: ColumnDefinitionTypeString},
			"enabled":            {Type: ColumnDefinitionTypeBoolean},
		},
	},
	"slo_report_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "slo_report_v2",
		Def: `(SELECT sr.*,
			json_build_object(
				'name', sc.name,
				'threshold', sc.threshold,
				'window', sc."window",
				'goal', sc.goal
			) as slo_config
			FROM slo_report sr
			LEFT JOIN slo_config sc ON sc.id = sr.config_id
		) as slo_report_v2`,
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "cloud_account_id",
		Columns: map[string]ColumnDefinition{
			"id":                     {Type: ColumnDefinitionTypeString},
			"created_at":             {Type: ColumnDefinitionTypeDatetime},
			"updated_at":             {Type: ColumnDefinitionTypeDatetime},
			"config_id":              {Type: ColumnDefinitionTypeString},
			"status":                 {Type: ColumnDefinitionTypeString},
			"cloud_account_id":       {Type: ColumnDefinitionTypeString},
			"tenant_id":              {Type: ColumnDefinitionTypeString},
			"workload_name":          {Type: ColumnDefinitionTypeString},
			"workload_namespace":     {Type: ColumnDefinitionTypeString},
			"error_budget_burn_rate": {Type: ColumnDefinitionTypeString},
			"events_count":           {Type: ColumnDefinitionTypeString},
			"good_events_count":      {Type: ColumnDefinitionTypeString},
			"bad_events_count":       {Type: ColumnDefinitionTypeString},
			"sli_measurement":        {Type: ColumnDefinitionTypeString},
			"slo_config":             {Type: ColumnDefinitionTypeJson},
			"timestamp":              {Type: ColumnDefinitionTypeDatetime},
		},
	},
	"slo_report_groupings_v2": {
		Type:                Aggregate,
		Source:              database.Metastore,
		Def:                 "slo_report",
		Name:                "slo_report_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "cloud_account_id",
		Columns: map[string]ColumnDefinition{
			"tenant_id":          {Type: ColumnDefinitionTypeString},
			"cloud_account_id":   {Type: ColumnDefinitionTypeString},
			"workload_name":      {Type: ColumnDefinitionTypeString},
			"workload_namespace": {Type: ColumnDefinitionTypeString},
			"status":             {Type: ColumnDefinitionTypeString},
			"config_id":          {Type: ColumnDefinitionTypeString},
			"timestamp": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at",
			},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"insight_v2": {
		Type:                Normal,
		Source:              database.Metastore,
		Def:                 "insight",
		Name:                "insight_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id":           {Type: ColumnDefinitionTypeString},
			"created_at":   {Type: ColumnDefinitionTypeDatetime},
			"updated_at":   {Type: ColumnDefinitionTypeDatetime},
			"title":        {Type: ColumnDefinitionTypeString},
			"type":         {Type: ColumnDefinitionTypeString},
			"source":       {Type: ColumnDefinitionTypeString},
			"account_id":   {Type: ColumnDefinitionTypeString},
			"tenant_id":    {Type: ColumnDefinitionTypeString, Def: "tenant"},
			"unique_id":    {Type: ColumnDefinitionTypeString},
			"resource_id":  {Type: ColumnDefinitionTypeString},
			"status":       {Type: ColumnDefinitionTypeString},
			"rule":         {Type: ColumnDefinitionTypeJson},
			"severity":     {Type: ColumnDefinitionTypeString},
			"applications": {Type: ColumnDefinitionTypeJson},
		},
	},
	"agent_playbook_v2": {
		Type:                Normal,
		Source:              database.Metastore,
		Def:                 "agent_playbook",
		Name:                "agent_playbook_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "cloud_account_id",
		Columns: map[string]ColumnDefinition{
			"id":               {Type: ColumnDefinitionTypeString},
			"tenant_id":        {Type: ColumnDefinitionTypeString},
			"cloud_account_id": {Type: ColumnDefinitionTypeString},
			"trigger_params":   {Type: ColumnDefinitionTypeJson},
			"action_params":    {Type: ColumnDefinitionTypeJson},
			"created_at":       {Type: ColumnDefinitionTypeDatetime},
			"updated_at":       {Type: ColumnDefinitionTypeDatetime},
			"source":           {Type: ColumnDefinitionTypeString},
			"processor":        {Type: ColumnDefinitionTypeString},
			"alert_name":       {Type: ColumnDefinitionTypeString},
		},
	},
	"tickets_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "tickets_v2",
		Def: `(SELECT t.*,
			u.display_name AS created_by_display_name
			FROM tickets t
			LEFT JOIN users u ON u.id = t.created_by
		) as tickets_v2`,
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id":                      {Type: ColumnDefinitionTypeString},
			"created_at":              {Type: ColumnDefinitionTypeDatetime},
			"updated_at":              {Type: ColumnDefinitionTypeDatetime},
			"created_by":              {Type: ColumnDefinitionTypeString},
			"tenant_id":               {Type: ColumnDefinitionTypeString, Def: "tenant"},
			"reference_id":            {Type: ColumnDefinitionTypeString},
			"ticket_type":             {Type: ColumnDefinitionTypeString},
			"status":                  {Type: ColumnDefinitionTypeString},
			"ticket_id":               {Type: ColumnDefinitionTypeString},
			"assignee":                {Type: ColumnDefinitionTypeString},
			"integration_id":          {Type: ColumnDefinitionTypeString},
			"url":                     {Type: ColumnDefinitionTypeString},
			"severity":                {Type: ColumnDefinitionTypeString},
			"description":             {Type: ColumnDefinitionTypeString},
			"title":                   {Type: ColumnDefinitionTypeString},
			"source":                  {Type: ColumnDefinitionTypeString},
			"platform":                {Type: ColumnDefinitionTypeString},
			"account_id":              {Type: ColumnDefinitionTypeString},
			"project_key":             {Type: ColumnDefinitionTypeString},
			"created_by_display_name": {Type: ColumnDefinitionTypeString},
		},
	},
	"auto_pilot_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "auto_pilot_v2",
		Def: `(SELECT ap.id, ap.name, ap.account_id, ap.tenant_id, ap.rule, ap.notification,
			ap.schedule_time, ap.next_schedule_time, ap.last_executed_time, ap.created_by,
			ap.creation_date, ap.start_at, ap.end_at, ap.attributes,
			ap.status::text as status,
			ap.category::text as category,
			u.username as username,
			u.display_name as display_name,
			u2.display_name as updated_by_display_name,
			ca.account_name as account_name,
			COALESCE(aorm_agg.auto_optimize_resource_maps, '[]'::json) as auto_optimize_resource_maps
			FROM auto_pilot ap
			LEFT JOIN users u ON u.id = ap.created_by
			LEFT JOIN users u2 ON u2.id = ap.update_by
			LEFT JOIN cloud_accounts ca ON ca.id = ap.account_id
			LEFT JOIN (
				SELECT
					auto_optimize_id,
					json_agg(json_build_object('resource_identifier', resource_identifier)) as auto_optimize_resource_maps
				FROM auto_optimize_resource_map
				GROUP BY auto_optimize_id
			) aorm_agg ON aorm_agg.auto_optimize_id = ap.id
		) as auto_pilot_v2`,
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id":                          {Type: ColumnDefinitionTypeString},
			"name":                        {Type: ColumnDefinitionTypeString},
			"account_id":                  {Type: ColumnDefinitionTypeString},
			"tenant_id":                   {Type: ColumnDefinitionTypeString},
			"rule":                        {Type: ColumnDefinitionTypeJson},
			"status":                      {Type: ColumnDefinitionTypeString},
			"category":                    {Type: ColumnDefinitionTypeString},
			"notification":                {Type: ColumnDefinitionTypeJson},
			"auto_optimize_resource_maps": {Type: ColumnDefinitionTypeJson},
			"schedule_time":               {Type: ColumnDefinitionTypeString},
			"next_schedule_time":          {Type: ColumnDefinitionTypeString},
			"last_executed_time":          {Type: ColumnDefinitionTypeDatetime},
			"created_by":                  {Type: ColumnDefinitionTypeString},
			"creation_date":               {Type: ColumnDefinitionTypeDatetime},
			"start_at":                    {Type: ColumnDefinitionTypeDatetime},
			"end_at":                      {Type: ColumnDefinitionTypeDatetime},
			"attributes":                  {Type: ColumnDefinitionTypeJson},
			"username":                    {Type: ColumnDefinitionTypeString},
			"display_name":                {Type: ColumnDefinitionTypeString},
			"updated_by_display_name":     {Type: ColumnDefinitionTypeString},
			"account_name":                {Type: ColumnDefinitionTypeString},
		},
	},
	"auto_pilot_groupings_v2": {
		Type:                Aggregate,
		Source:              database.Metastore,
		Def:                 "(SELECT id, tenant_id, account_id, status::text as status, category::text as category, name FROM auto_pilot) as auto_pilot_agg",
		Name:                "auto_pilot_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id":         {Type: ColumnDefinitionTypeString},
			"tenant_id":  {Type: ColumnDefinitionTypeString},
			"account_id": {Type: ColumnDefinitionTypeString},
			"status":     {Type: ColumnDefinitionTypeString},
			"category":   {Type: ColumnDefinitionTypeString},
			"name":       {Type: ColumnDefinitionTypeString},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"upgrade_plan_audit_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "upgrade_plan_audit_v2",
		Def: `(SELECT upa.*,
			json_build_object('display_name', u.display_name) as user_actioned_by
			FROM upgrade_plan_audit upa
			LEFT JOIN users u ON u.id = upa.actioned_by
		) as upgrade_plan_audit_v2`,
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id":               {Type: ColumnDefinitionTypeString},
			"created_at":       {Type: ColumnDefinitionTypeDatetime},
			"tenant_id":        {Type: ColumnDefinitionTypeString},
			"plan_id":          {Type: ColumnDefinitionTypeString},
			"step_id":          {Type: ColumnDefinitionTypeString},
			"task_id":          {Type: ColumnDefinitionTypeString},
			"field":            {Type: ColumnDefinitionTypeString},
			"action":           {Type: ColumnDefinitionTypeString},
			"old_value":        {Type: ColumnDefinitionTypeString},
			"new_value":        {Type: ColumnDefinitionTypeString},
			"actioned_by":      {Type: ColumnDefinitionTypeString},
			"account_id":       {Type: ColumnDefinitionTypeString},
			"user_actioned_by": {Type: ColumnDefinitionTypeJson},
		},
	},
	"k8s_nodes_v2": {
		Type:                Normal,
		Source:              database.Metastore,
		Def:                 "k8s_nodes",
		Name:                "k8s_nodes_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "cloud_account_id",
		Columns: map[string]ColumnDefinition{
			"name":               {Type: ColumnDefinitionTypeString},
			"is_active":          {Type: ColumnDefinitionTypeBoolean},
			"node_creation_time": {Type: ColumnDefinitionTypeDatetime},
			"updated_at":         {Type: ColumnDefinitionTypeDatetime},
			"conditions":         {Type: ColumnDefinitionTypeJson},
			"node_type":          {Type: ColumnDefinitionTypeString},
			"node_flavor":        {Type: ColumnDefinitionTypeString},
			"node_region":        {Type: ColumnDefinitionTypeString},
			"node_zone":          {Type: ColumnDefinitionTypeString},
			"memory_capacity":    {Type: ColumnDefinitionTypeFloat},
			"cpu_capacity":       {Type: ColumnDefinitionTypeFloat},
			"memory_allocatable": {Type: ColumnDefinitionTypeFloat},
			"cpu_allocatable":    {Type: ColumnDefinitionTypeFloat},
			"memory_limits":      {Type: ColumnDefinitionTypeFloat},
			"cpu_limits":         {Type: ColumnDefinitionTypeFloat},
			"cloud_resource_id":  {Type: ColumnDefinitionTypeString},
			"cloud_account_id":   {Type: ColumnDefinitionTypeString},
			"external_ip":        {Type: ColumnDefinitionTypeString},
			"internal_ip":        {Type: ColumnDefinitionTypeString},
			"labels":             {Type: ColumnDefinitionTypeJson},
			"taints":             {Type: ColumnDefinitionTypeJson},
			"cost":               {Type: ColumnDefinitionTypeFloat},
			"meta":               {Type: ColumnDefinitionTypeJson},
			"tenant_id":          {Type: ColumnDefinitionTypeString},
		},
	},
	"k8s_nodes_groupings_v2": {
		Type:                Aggregate,
		Source:              database.Metastore,
		Def:                 "k8s_nodes",
		Name:                "k8s_nodes_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "cloud_account_id",
		Columns: map[string]ColumnDefinition{
			"tenant_id":        {Type: ColumnDefinitionTypeString},
			"cloud_account_id": {Type: ColumnDefinitionTypeString},
			"is_active":        {Type: ColumnDefinitionTypeBoolean},
			"node_type":        {Type: ColumnDefinitionTypeString},
			"node_flavor":      {Type: ColumnDefinitionTypeString},
			"name":             {Type: ColumnDefinitionTypeString},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"cloud_resource_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "cloud_resource_v2",
		Def: `(SELECT cr.*,
			ca.account_name as account_name,
			cr.resourse_id as resource_id,
			cr.resourse_created_on as resource_created_on,
			cr.meta ->> 'namespace' as namespace
			FROM cloud_resourses cr
			LEFT JOIN cloud_accounts ca ON ca.id = cr.account
		) as cloud_resource_v2`,
		TenantIdColumnName:  "tenant",
		AccountIdColumnName: "account",
		NamespaceColumnName: "namespace",
		Columns: map[string]ColumnDefinition{
			"id":                   {Type: ColumnDefinitionTypeString},
			"name":                 {Type: ColumnDefinitionTypeString},
			"arn":                  {Type: ColumnDefinitionTypeString},
			"type":                 {Type: ColumnDefinitionTypeString},
			"status":               {Type: ColumnDefinitionTypeString},
			"is_active":            {Type: ColumnDefinitionTypeBoolean},
			"meta":                 {Type: ColumnDefinitionTypeJson},
			"tags":                 {Type: ColumnDefinitionTypeJson},
			"account":              {Type: ColumnDefinitionTypeString},
			"tenant":               {Type: ColumnDefinitionTypeString},
			"namespace":            {Type: ColumnDefinitionTypeString},
			"service_name":         {Type: ColumnDefinitionTypeString},
			"region":               {Type: ColumnDefinitionTypeString},
			"cloud_provider":       {Type: ColumnDefinitionTypeString},
			"external_resource_id": {Type: ColumnDefinitionTypeString},
			"resource_id":          {Type: ColumnDefinitionTypeString},
			"first_seen":           {Type: ColumnDefinitionTypeDatetime},
			"last_seen":            {Type: ColumnDefinitionTypeDatetime},
			"created_at":           {Type: ColumnDefinitionTypeDatetime},
			"updated_at":           {Type: ColumnDefinitionTypeDatetime},
			"resource_created_on":  {Type: ColumnDefinitionTypeDatetime},
			"created_by":           {Type: ColumnDefinitionTypeString},
			"updated_by":           {Type: ColumnDefinitionTypeString},
			"account_name":         {Type: ColumnDefinitionTypeString},
		},
	},
	"cloud_resource_details_v2": {
		Type:                Derived,
		Source:              database.Metastore,
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "",
		Name:                "cloud_resource_details_v2",
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			tenantId := ctx.GetSecurityContext().GetTenantId()
			def := fmt.Sprintf(`(
				SELECT
					id,
					cloud_provider,
					service_name,
					service_type,
					resource_type,
					resource_region,
					resource_cost,
					resource_capacity,
					database_engine,
					deployment_option,
					'%s'::text as tenant_id
				FROM cloud_resource_details
			) as cloud_resource_details_v2`, tenantId)
			return def, request, nil
		},
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeInt,
				Def:  "id",
			},
			"cloud_provider": {
				Type: ColumnDefinitionTypeString,
				Def:  "cloud_provider",
			},
			"service_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "service_name",
			},
			"service_type": {
				Type: ColumnDefinitionTypeString,
				Def:  "service_type",
			},
			"resource_type": {
				Type: ColumnDefinitionTypeString,
				Def:  "resource_type",
			},
			"resource_region": {
				Type: ColumnDefinitionTypeString,
				Def:  "resource_region",
			},
			"resource_cost": {
				Type: ColumnDefinitionTypeFloat,
				Def:  "resource_cost",
			},
			"resource_capacity": {
				Type: ColumnDefinitionTypeFloat,
				Def:  "resource_capacity",
			},
			"database_engine": {
				Type: ColumnDefinitionTypeString,
				Def:  "database_engine",
			},
			"deployment_option": {
				Type: ColumnDefinitionTypeString,
				Def:  "deployment_option",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
		},
	},
	"resource_details_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "resource_details_v2",
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			baseQuery := `(
				SELECT
					cr.id,
					cr.name,
					cr.arn,
					cr.type,
					cr.status,
					cr.is_active,
					cr.meta,
					cr.tags,
					cr.account,
					cr.tenant,
					cr.service_name,
					cr.region,
					cr.external_resource_id,
					cr.first_seen,
					cr.last_seen,
					cr.created_at,
					cr.updated_at,
					ca.account_name as account_name,
					ca.cloud_provider as cloud_provider,
					ca.synced_at as account_synced_at,
					ca.account_type as account_type,
					ca.sync_status as sync_status,
					cr.resourse_id as resource_id,
					cr.resourse_created_on as resource_created_on,
					cr.meta ->> 'namespace' as namespace,
					'1970-01-01'::timestamp as spend_date,
					COALESCE(s.spend_amount, 0)::float as spend_amount,
					COALESCE(r.recommendation_count, 0)::int as recommendation_count,
					COALESCE(r.recommendation_estimated_savings, 0)::float as recommendation_estimated_savings,
					COALESCE(r.critical_recommendation_count, 0)::int as critical_recommendation_count
				FROM cloud_resourses cr
				LEFT JOIN cloud_accounts ca ON ca.id = cr.account
				LEFT JOIN (
					SELECT cloud_resource_id, cloud_account, SUM(amount) as spend_amount
					FROM (SELECT cloud_resource_id, cloud_account, "date" as spend_date, amount FROM spends) spends_inner
					WHERE __spends__where__
					GROUP BY cloud_resource_id, cloud_account
				) s ON s.cloud_resource_id = cr.id AND s.cloud_account = cr.account
				LEFT JOIN (
					SELECT resource_id,
						COUNT(*) FILTER (WHERE status IN ('Open', 'Assigned')) as recommendation_count,
						SUM(estimated_savings) FILTER (WHERE status IN ('Open', 'Assigned')) as recommendation_estimated_savings,
						COUNT(*) FILTER (WHERE severity = 'Critical' AND status IN ('Open', 'Assigned')) as critical_recommendation_count
					FROM recommendation
					GROUP BY resource_id
				) r ON r.resource_id = cr.id
			) as resource_details_v2`

			// spend_date is intercepted by splitWhereClause and pushed into __spends__where__
			// inside the spend subquery, so a _gte/_lte filter on spend_date controls which
			// rows are summed and never reaches the outer WHERE. The outer SELECT exposes a
			// constant placeholder only to satisfy the Columns definition.
			spendColumns := []string{"spend_date"}

			// splitWhereClause recursively separates spend-related filters from the rest
			var splitWhereClause func(where QueryWhereClause) (spendWhere QueryWhereClause, remaining QueryWhereClause)
			splitWhereClause = func(where QueryWhereClause) (QueryWhereClause, QueryWhereClause) {
				spend := QueryWhereClause{Binary: make(BinaryWhereClause)}
				rest := QueryWhereClause{Binary: make(BinaryWhereClause)}

				// Split top-level binary clauses
				for col, clauses := range where.Binary {
					if lo.Contains(spendColumns, col) {
						spend.Binary[col] = clauses
					} else {
						rest.Binary[col] = clauses
					}
				}

				// Recursively split _and clauses
				for _, andClause := range where.And {
					spendChild, restChild := splitWhereClause(andClause)
					if hasFilters(spendChild) {
						spend.And = append(spend.And, spendChild)
					}
					if hasFilters(restChild) {
						rest.And = append(rest.And, restChild)
					}
				}

				// Recursively split _or clauses
				for _, orClause := range where.Or {
					spendChild, restChild := splitWhereClause(orClause)
					if hasFilters(spendChild) {
						spend.Or = append(spend.Or, spendChild)
					}
					if hasFilters(restChild) {
						rest.Or = append(rest.Or, restChild)
					}
				}

				// Recursively split _not clause
				if where.Not != nil {
					spendChild, restChild := splitWhereClause(*where.Not)
					if hasFilters(spendChild) {
						spend.Not = &spendChild
					}
					if hasFilters(restChild) {
						rest.Not = &restChild
					}
				}

				return spend, rest
			}

			spendWhereClause, remainingWhere := splitWhereClause(request.Where)
			request.Where = remainingWhere

			var spendsWhereStr string
			var err error
			if hasFilters(spendWhereClause) {
				tempTableDef := TableDefinition{
					Columns: map[string]ColumnDefinition{
						"spend_date": {
							Type: ColumnDefinitionTypeDatetime,
						},
					},
					Type:   Normal,
					Def:    "spends",
					Name:   "spends",
					Source: database.Metastore,
				}
				spendsWhereStr, err = generateWhereClause(spendWhereClause, tempTableDef)
				if err != nil {
					return "", request, fmt.Errorf("failed to generate spends where clause: %w", err)
				}
			} else {
				spendsWhereStr = "1 = 1"
			}

			baseQuery = strings.ReplaceAll(baseQuery, "__spends__where__", spendsWhereStr)
			return baseQuery, request, nil
		},
		TenantIdColumnName:  "tenant",
		AccountIdColumnName: "account",
		NamespaceColumnName: "namespace",
		Columns: map[string]ColumnDefinition{
			"id":                               {Type: ColumnDefinitionTypeString},
			"name":                             {Type: ColumnDefinitionTypeString},
			"arn":                              {Type: ColumnDefinitionTypeString},
			"type":                             {Type: ColumnDefinitionTypeString},
			"status":                           {Type: ColumnDefinitionTypeString},
			"is_active":                        {Type: ColumnDefinitionTypeBoolean},
			"meta":                             {Type: ColumnDefinitionTypeJson},
			"tags":                             {Type: ColumnDefinitionTypeJson},
			"account":                          {Type: ColumnDefinitionTypeString},
			"tenant":                           {Type: ColumnDefinitionTypeString},
			"namespace":                        {Type: ColumnDefinitionTypeString},
			"service_name":                     {Type: ColumnDefinitionTypeString},
			"region":                           {Type: ColumnDefinitionTypeString},
			"cloud_provider":                   {Type: ColumnDefinitionTypeString},
			"external_resource_id":             {Type: ColumnDefinitionTypeString},
			"resource_id":                      {Type: ColumnDefinitionTypeString},
			"first_seen":                       {Type: ColumnDefinitionTypeDatetime},
			"last_seen":                        {Type: ColumnDefinitionTypeDatetime},
			"created_at":                       {Type: ColumnDefinitionTypeDatetime},
			"updated_at":                       {Type: ColumnDefinitionTypeDatetime},
			"resource_created_on":              {Type: ColumnDefinitionTypeDatetime},
			"account_name":                     {Type: ColumnDefinitionTypeString},
			"account_synced_at":                {Type: ColumnDefinitionTypeDatetime},
			"account_type":                     {Type: ColumnDefinitionTypeString},
			"sync_status":                      {Type: ColumnDefinitionTypeString},
			"spend_amount":                     {Type: ColumnDefinitionTypeFloat},
			"recommendation_count":             {Type: ColumnDefinitionTypeInt},
			"recommendation_estimated_savings": {Type: ColumnDefinitionTypeFloat},
			"critical_recommendation_count":    {Type: ColumnDefinitionTypeInt},
			"spend_date":                       {Type: ColumnDefinitionTypeDatetime},
		},
	},
	"cloud_resource_groupings_v2": {
		Type:                Aggregate,
		Source:              database.Metastore,
		Def:                 "cloud_resourses",
		Name:                "cloud_resource_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "account",
			},
			"service_name": {
				Type: ColumnDefinitionTypeString,
			},
			"type": {
				Type: ColumnDefinitionTypeString,
			},
			"status": {
				Type: ColumnDefinitionTypeString,
			},
			"region": {
				Type: ColumnDefinitionTypeString,
			},
			"meta": {
				Type: ColumnDefinitionTypeJson,
			},
			"tags": {
				Type: ColumnDefinitionTypeJson,
			},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"cloud_resources_list_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "cloud_resources_list_v2",
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			baseQuery := `(
				SELECT cr.region, cr.resourse_id, cr.service_name, cr.status, cr.name, cr.id,
					cr.meta, cr.account, cr.tags, cr.created_at, cr.resourse_created_on, cr.type, cr.tenant,
					cr.meta ->> 'namespace' as namespace,
					COALESCE(s.spend_amount, 0)::float as spend_amount,
					m.metric as latest_metric,
					m.value::float as latest_metric_value,
					m.timestamp as latest_metric_timestamp,
					COUNT(*) OVER() as total_count
				FROM cloud_resourses cr
				LEFT JOIN (
					SELECT cloud_resource_id, cloud_account, SUM(amount) as spend_amount
					FROM spends
					__spends__where__
					GROUP BY cloud_resource_id, cloud_account
				) s ON s.cloud_resource_id = cr.id AND s.cloud_account = cr.account
				LEFT JOIN LATERAL (
					SELECT metric, value, timestamp
					FROM cloud_resource_metrics crm
					WHERE crm.cloud_resource_id = cr.id
						AND __metrics__where__
					ORDER BY timestamp DESC
					LIMIT 1
				) m ON true
				__cr__where__
			) as cloud_resources_list_v2`

			metricColumns := []string{"metric"}
			// Columns that exist directly on cloud_resourses table and can be pushed down
			crColumns := []string{"region", "resourse_id", "service_name", "status", "name", "id", "type", "account", "tenant", "created_at", "meta", "tags"}

			// splitWhereClause recursively separates filters into matched and remaining
			var splitWhereClause func(where QueryWhereClause, matchColumns []string) (matched QueryWhereClause, remaining QueryWhereClause)
			splitWhereClause = func(where QueryWhereClause, matchColumns []string) (matched QueryWhereClause, remaining QueryWhereClause) {
				matched = QueryWhereClause{Binary: make(BinaryWhereClause)}
				remaining = QueryWhereClause{Binary: make(BinaryWhereClause)}

				for col, clauses := range where.Binary {
					if lo.Contains(matchColumns, col) {
						matched.Binary[col] = clauses
					} else {
						remaining.Binary[col] = clauses
					}
				}

				for _, andClause := range where.And {
					matchChild, restChild := splitWhereClause(andClause, matchColumns)
					if hasFilters(matchChild) {
						matched.And = append(matched.And, matchChild)
					}
					if hasFilters(restChild) {
						remaining.And = append(remaining.And, restChild)
					}
				}

				for _, orClause := range where.Or {
					matchChild, restChild := splitWhereClause(orClause, matchColumns)
					if hasFilters(matchChild) {
						matched.Or = append(matched.Or, matchChild)
					}
					if hasFilters(restChild) {
						remaining.Or = append(remaining.Or, restChild)
					}
				}

				if where.Not != nil {
					matchChild, restChild := splitWhereClause(*where.Not, matchColumns)
					if hasFilters(matchChild) {
						matched.Not = &matchChild
					}
					if hasFilters(restChild) {
						remaining.Not = &restChild
					}
				}

				return matched, remaining
			}

			// Split metric filters first
			metricWhereClause, afterMetric := splitWhereClause(request.Where, metricColumns)

			// Split cloud_resourses filters from the remaining
			crWhereClause, remaining := splitWhereClause(afterMetric, crColumns)
			request.Where = remaining

			// Generate metrics WHERE
			var metricsWhereStr string
			var err error
			if hasFilters(metricWhereClause) {
				tempTableDef := TableDefinition{
					Columns: map[string]ColumnDefinition{
						"metric": {Type: ColumnDefinitionTypeString},
					},
					Type:   Normal,
					Def:    "cloud_resource_metrics",
					Name:   "cloud_resource_metrics",
					Source: database.Metastore,
				}
				metricsWhereStr, err = generateWhereClause(metricWhereClause, tempTableDef)
				if err != nil {
					return "", request, fmt.Errorf("failed to generate metrics where clause: %w", err)
				}
			} else {
				metricsWhereStr = "1 = 1"
			}

			// Generate cloud_resourses WHERE (pushed into subquery for performance)
			var crWhereStr string
			if hasFilters(crWhereClause) {
				crTableDef := TableDefinition{
					Columns: map[string]ColumnDefinition{
						"region":       {Type: ColumnDefinitionTypeString},
						"resourse_id":  {Type: ColumnDefinitionTypeString},
						"service_name": {Type: ColumnDefinitionTypeString},
						"status":       {Type: ColumnDefinitionTypeString},
						"name":         {Type: ColumnDefinitionTypeString},
						"id":           {Type: ColumnDefinitionTypeString},
						"type":         {Type: ColumnDefinitionTypeString},
						"account":      {Type: ColumnDefinitionTypeString},
						"tenant":       {Type: ColumnDefinitionTypeString},
						"created_at":   {Type: ColumnDefinitionTypeDatetime},
						"meta":         {Type: ColumnDefinitionTypeJson},
						"tags":         {Type: ColumnDefinitionTypeJson},
					},
					Type:   Normal,
					Def:    "cloud_resourses",
					Name:   "cloud_resourses",
					Source: database.Metastore,
				}
				crWhereStr, err = generateWhereClause(crWhereClause, crTableDef)
				if err != nil {
					return "", request, fmt.Errorf("failed to generate cloud_resourses where clause: %w", err)
				}
			}

			// Build spends WHERE to scope aggregation to filtered accounts
			spendsWhereStr := ""
			if accountBinary, ok := crWhereClause.Binary["account"]; ok {
				spendsTableDef := TableDefinition{
					Columns: map[string]ColumnDefinition{
						"cloud_account": {Type: ColumnDefinitionTypeString},
					},
					Type:   Normal,
					Def:    "spends",
					Name:   "spends",
					Source: database.Metastore,
				}
				spendsWhere := QueryWhereClause{
					Binary: BinaryWhereClause{"cloud_account": accountBinary},
				}
				spendsWhereStr, err = generateWhereClause(spendsWhere, spendsTableDef)
				if err != nil {
					spendsWhereStr = "" // non-critical, fall back to no filter
				}
			}

			if crWhereStr != "" {
				baseQuery = strings.ReplaceAll(baseQuery, "__cr__where__", "WHERE "+crWhereStr)
			} else {
				baseQuery = strings.ReplaceAll(baseQuery, "__cr__where__", "")
			}
			if spendsWhereStr != "" {
				baseQuery = strings.ReplaceAll(baseQuery, "__spends__where__", "WHERE "+spendsWhereStr)
			} else {
				baseQuery = strings.ReplaceAll(baseQuery, "__spends__where__", "")
			}
			baseQuery = strings.ReplaceAll(baseQuery, "__metrics__where__", metricsWhereStr)
			return baseQuery, request, nil
		},
		TenantIdColumnName:  "tenant",
		AccountIdColumnName: "account",
		NamespaceColumnName: "namespace",
		Columns: map[string]ColumnDefinition{
			"id":                      {Type: ColumnDefinitionTypeString},
			"name":                    {Type: ColumnDefinitionTypeString},
			"resourse_id":             {Type: ColumnDefinitionTypeString},
			"type":                    {Type: ColumnDefinitionTypeString},
			"status":                  {Type: ColumnDefinitionTypeString},
			"meta":                    {Type: ColumnDefinitionTypeJson},
			"tags":                    {Type: ColumnDefinitionTypeJson},
			"account":                 {Type: ColumnDefinitionTypeString},
			"tenant":                  {Type: ColumnDefinitionTypeString},
			"namespace":               {Type: ColumnDefinitionTypeString},
			"service_name":            {Type: ColumnDefinitionTypeString},
			"region":                  {Type: ColumnDefinitionTypeString},
			"created_at":              {Type: ColumnDefinitionTypeDatetime},
			"resourse_created_on":     {Type: ColumnDefinitionTypeDatetime},
			"spend_amount":            {Type: ColumnDefinitionTypeFloat},
			"latest_metric":           {Type: ColumnDefinitionTypeString},
			"latest_metric_value":     {Type: ColumnDefinitionTypeFloat},
			"latest_metric_timestamp": {Type: ColumnDefinitionTypeDatetime},
			"total_count":             {Type: ColumnDefinitionTypeInt},
			"metric":                  {Type: ColumnDefinitionTypeString},
		},
	},
	"resource_spend_trend_v2": {
		Type:   Aggregate,
		Source: database.Metastore,
		Name:   "resource_spend_trend_v2",
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			return "spends s LEFT JOIN cloud_resourses cr ON s.cloud_resource_id = cr.id AND s.cloud_account = cr.account", request, nil
		},
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "s.tenant",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "s.cloud_account",
			},
			"resource_external_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "cr.resourse_id",
			},
			"spend_date": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  `s."date"`,
			},
			"exclude_aggregate": {
				Type: ColumnDefinitionTypeBoolean,
				Def:  "s.exclude_aggregate",
			},
			"spend_amount": {
				Type:         ColumnDefinitionTypeFloat,
				Def:          "sum(s.amount)",
				IsAggregated: true,
			},
			"spend_count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
			"currency_type": {
				Type: ColumnDefinitionTypeString,
				Def:  "s.unit",
			},
		},
	},
	"cloud_resource_attributes_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "cloud_resource_attributes_v2",
		Def: `(SELECT cra.*,
			cr.id as resource_uuid,
			cr.arn as resource_arn,
			cr.name as resource_name,
			cr.type as resource_type,
			cr.meta as resource_meta,
			cr.status as resource_status,
			cr.created_at as resource_created_at,
			cr.updated_at as resource_updated_at,
			cr.meta ->> 'namespace' as namespace
			FROM cloud_resource_attributes cra
			LEFT JOIN cloud_resourses cr ON cr.id = cra.resource_id
		) as cloud_resource_attributes_v2`,
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		NamespaceColumnName: "namespace",
		Columns: map[string]ColumnDefinition{
			"id":                  {Type: ColumnDefinitionTypeString},
			"name":                {Type: ColumnDefinitionTypeString},
			"value":               {Type: ColumnDefinitionTypeString},
			"labels":              {Type: ColumnDefinitionTypeJson},
			"resource_id":         {Type: ColumnDefinitionTypeString},
			"account_id":          {Type: ColumnDefinitionTypeString},
			"tenant_id":           {Type: ColumnDefinitionTypeString},
			"namespace":           {Type: ColumnDefinitionTypeString},
			"created_at":          {Type: ColumnDefinitionTypeDatetime},
			"last_seen_at":        {Type: ColumnDefinitionTypeDatetime},
			"resource_uuid":       {Type: ColumnDefinitionTypeString},
			"resource_arn":        {Type: ColumnDefinitionTypeString},
			"resource_name":       {Type: ColumnDefinitionTypeString},
			"resource_type":       {Type: ColumnDefinitionTypeString},
			"resource_meta":       {Type: ColumnDefinitionTypeJson},
			"resource_status":     {Type: ColumnDefinitionTypeString},
			"resource_created_at": {Type: ColumnDefinitionTypeDatetime},
			"resource_updated_at": {Type: ColumnDefinitionTypeDatetime},
		},
	},
	"notification_channel_account_mapping_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "notification_channel_account_mapping_v2",
		Def: `(SELECT ncam.*,
			ca.account_name as account_name,
			ca.cloud_provider as cloud_provider,
			u.display_name as created_by_display_name
			FROM notification_channel_account_mappings ncam
			LEFT JOIN cloud_accounts ca ON ca.id = ncam.account_id
			LEFT JOIN users u ON u.id = ncam.created_by
		) as notification_channel_account_mapping_v2`,
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id":                      {Type: ColumnDefinitionTypeString},
			"account_id":              {Type: ColumnDefinitionTypeString},
			"platform":                {Type: ColumnDefinitionTypeString},
			"team_id":                 {Type: ColumnDefinitionTypeString},
			"channel_id":              {Type: ColumnDefinitionTypeString},
			"channel_metadata":        {Type: ColumnDefinitionTypeJson},
			"created_at":              {Type: ColumnDefinitionTypeDatetime},
			"updated_at":              {Type: ColumnDefinitionTypeDatetime},
			"created_by":              {Type: ColumnDefinitionTypeString},
			"updated_by":              {Type: ColumnDefinitionTypeString},
			"tenant_id":               {Type: ColumnDefinitionTypeString},
			"account_name":            {Type: ColumnDefinitionTypeString},
			"cloud_provider":          {Type: ColumnDefinitionTypeString},
			"created_by_display_name": {Type: ColumnDefinitionTypeString},
		},
	},
	"application_group_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "application_group_v2",
		Def: `(SELECT ag.*,
			uc.display_name as created_by_display_name,
			uu.display_name as updated_by_display_name
			FROM application_group ag
			LEFT JOIN users uc ON uc.id = ag.created_by
			LEFT JOIN users uu ON uu.id = ag.updated_by
		) as application_group_v2`,
		TenantIdColumnName: "tenant_id",
		Columns: map[string]ColumnDefinition{
			"id":                      {Type: ColumnDefinitionTypeString},
			"name":                    {Type: ColumnDefinitionTypeString},
			"description":             {Type: ColumnDefinitionTypeString},
			"created_at":              {Type: ColumnDefinitionTypeDatetime},
			"updated_at":              {Type: ColumnDefinitionTypeDatetime},
			"created_by":              {Type: ColumnDefinitionTypeString},
			"updated_by":              {Type: ColumnDefinitionTypeString},
			"tenant_id":               {Type: ColumnDefinitionTypeString},
			"created_by_display_name": {Type: ColumnDefinitionTypeString},
			"updated_by_display_name": {Type: ColumnDefinitionTypeString},
		},
	},
	"application_group_groupings_v2": {
		Type:               Aggregate,
		Source:             database.Metastore,
		Def:                "application_group",
		Name:               "application_group_groupings_v2",
		TenantIdColumnName: "tenant_id",
		Columns: map[string]ColumnDefinition{
			"tenant_id": {Type: ColumnDefinitionTypeString},
			"name":      {Type: ColumnDefinitionTypeString},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"application_group_mapping_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "application_group_mapping_v2",
		Def: `(SELECT agm.*,
			ca.account_name as account_name,
			kw.is_active as workload_is_active,
			kw.name as workload_display_name,
			kw.namespace as workload_namespace
			FROM application_group_mapping agm
			LEFT JOIN cloud_accounts ca ON ca.id = agm.account_id
			LEFT JOIN k8s_workloads kw ON kw.cloud_account_id = agm.account_id
				AND kw.kind = agm.workload_kind
				AND kw.namespace = agm.namespace_name
				AND kw.name = agm.workload_name
		) as application_group_mapping_v2`,
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id":                    {Type: ColumnDefinitionTypeString},
			"group_id":              {Type: ColumnDefinitionTypeString},
			"account_id":            {Type: ColumnDefinitionTypeString},
			"workload_name":         {Type: ColumnDefinitionTypeString},
			"workload_kind":         {Type: ColumnDefinitionTypeString},
			"cloud_resource_id":     {Type: ColumnDefinitionTypeString},
			"tenant_id":             {Type: ColumnDefinitionTypeString},
			"account_name":          {Type: ColumnDefinitionTypeString},
			"workload_is_active":    {Type: ColumnDefinitionTypeBoolean},
			"workload_display_name": {Type: ColumnDefinitionTypeString},
			"workload_namespace":    {Type: ColumnDefinitionTypeString},
		},
	},
	"application_group_mapping_groupings_v2": {
		Type:                Aggregate,
		Source:              database.Metastore,
		Def:                 "application_group_mapping",
		Name:                "application_group_mapping_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"tenant_id":  {Type: ColumnDefinitionTypeString},
			"account_id": {Type: ColumnDefinitionTypeString},
			"group_id":   {Type: ColumnDefinitionTypeString},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"auto_pilot_task_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "auto_pilot_task_v2",
		Def: `(SELECT apt.*,
			ap.category as auto_pilot_category,
			ap.account_id as auto_pilot_account_id,
			COALESCE(aorm_agg.auto_optimize_resource_maps, '[]'::json) as auto_pilot_resource_maps
			FROM auto_pilot_task apt
			LEFT JOIN auto_pilot ap ON ap.id = apt.auto_pilot_id
			LEFT JOIN (
				SELECT
					auto_optimize_id,
					json_agg(json_build_object('resource_identifier', resource_identifier)) as auto_optimize_resource_maps
				FROM auto_optimize_resource_map
				GROUP BY auto_optimize_id
			) aorm_agg ON aorm_agg.auto_optimize_id = ap.id
		) as auto_pilot_task_v2`,
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id":                       {Type: ColumnDefinitionTypeString},
			"tenant_id":                {Type: ColumnDefinitionTypeString},
			"account_id":               {Type: ColumnDefinitionTypeString},
			"auto_pilot_id":            {Type: ColumnDefinitionTypeString},
			"task_id":                  {Type: ColumnDefinitionTypeString},
			"name":                     {Type: ColumnDefinitionTypeString},
			"command":                  {Type: ColumnDefinitionTypeString},
			"reason":                   {Type: ColumnDefinitionTypeString},
			"status":                   {Type: ColumnDefinitionTypeString},
			"meta":                     {Type: ColumnDefinitionTypeJson},
			"resource_filter":          {Type: ColumnDefinitionTypeJson},
			"recommendation_id":        {Type: ColumnDefinitionTypeString},
			"scheduled_time":           {Type: ColumnDefinitionTypeDatetime},
			"created_at":               {Type: ColumnDefinitionTypeDatetime},
			"updated_at":               {Type: ColumnDefinitionTypeDatetime},
			"auto_pilot_category":      {Type: ColumnDefinitionTypeString},
			"auto_pilot_account_id":    {Type: ColumnDefinitionTypeString},
			"auto_pilot_resource_maps": {Type: ColumnDefinitionTypeJson},
			"attributes":               {Type: ColumnDefinitionTypeJson},
		},
	},
	"auto_pilot_task_groupings_v2": {
		Type:   Aggregate,
		Source: database.Metastore,
		Def: `(SELECT apt.*, ap.category as auto_pilot_category, ap.account_id as auto_pilot_account_id
			FROM auto_pilot_task apt
			LEFT JOIN auto_pilot ap ON ap.id = apt.auto_pilot_id
		) as apt_agg`,
		Name:                "auto_pilot_task_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
			"tenant_id":             {Type: ColumnDefinitionTypeString},
			"account_id":            {Type: ColumnDefinitionTypeString},
			"status":                {Type: ColumnDefinitionTypeString},
			"scheduled_time":        {Type: ColumnDefinitionTypeDatetime},
			"auto_pilot_id":         {Type: ColumnDefinitionTypeString},
			"auto_pilot_category":   {Type: ColumnDefinitionTypeString},
			"auto_pilot_account_id": {Type: ColumnDefinitionTypeString},
		},
	},
	"auto_pilot_approvals_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "auto_pilot_approvals_v2",
		Def: `(SELECT apa.*,
			apas.description as approval_status_description,
			u.display_name as reviewer_display_name
			FROM auto_pilot_approvals apa
			LEFT JOIN auto_pilot_approval_status apas ON apas.status = apa.status
			LEFT JOIN users u ON u.id = apa.reviewer_id
		) as auto_pilot_approvals_v2`,
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id":                          {Type: ColumnDefinitionTypeString},
			"tenant_id":                   {Type: ColumnDefinitionTypeString},
			"account_id":                  {Type: ColumnDefinitionTypeString},
			"autopilot_id":                {Type: ColumnDefinitionTypeString},
			"policy_id":                   {Type: ColumnDefinitionTypeString},
			"reviewer_id":                 {Type: ColumnDefinitionTypeString},
			"status":                      {Type: ColumnDefinitionTypeString},
			"auto_pilot_type":             {Type: ColumnDefinitionTypeString},
			"reviewer_comments":           {Type: ColumnDefinitionTypeString},
			"attributes":                  {Type: ColumnDefinitionTypeJson},
			"created_at":                  {Type: ColumnDefinitionTypeDatetime},
			"updated_at":                  {Type: ColumnDefinitionTypeDatetime},
			"approval_status_description": {Type: ColumnDefinitionTypeString},
			"reviewer_display_name":       {Type: ColumnDefinitionTypeString},
		},
	},
	"auto_pilot_approvals_groupings_v2": {
		Type:                Aggregate,
		Source:              database.Metastore,
		Def:                 "auto_pilot_approvals",
		Name:                "auto_pilot_approvals_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
			"autopilot_id": {Type: ColumnDefinitionTypeString},
		},
	},
	"auto_pilot_approval_policy_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "auto_pilot_approval_policy_v2",
		Def: `(SELECT apap.*,
			ca.account_name as account_name,
			uc.display_name as created_by_display_name,
			uu.display_name as updated_by_display_name
			FROM auto_pilot_approval_policy apap
			LEFT JOIN cloud_accounts ca ON ca.id = apap.account_id
			LEFT JOIN users uc ON uc.id = apap.created_by
			LEFT JOIN users uu ON uu.id = apap.updated_by
		) as auto_pilot_approval_policy_v2`,
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id":                      {Type: ColumnDefinitionTypeString},
			"tenant_id":               {Type: ColumnDefinitionTypeString},
			"account_id":              {Type: ColumnDefinitionTypeString},
			"created_by":              {Type: ColumnDefinitionTypeString},
			"updated_by":              {Type: ColumnDefinitionTypeString},
			"policy_attributes":       {Type: ColumnDefinitionTypeJson},
			"created_at":              {Type: ColumnDefinitionTypeDatetime},
			"updated_at":              {Type: ColumnDefinitionTypeDatetime},
			"account_name":            {Type: ColumnDefinitionTypeString},
			"created_by_display_name": {Type: ColumnDefinitionTypeString},
			"updated_by_display_name": {Type: ColumnDefinitionTypeString},
		},
	},
	"recommendation_resolution_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "recommendation_resolution_v2",
		Def: `(SELECT
			rr.id,
			rr.recommendation_id,
			rr.type,
			rr.status,
			rr.type_reference_id,
			rr.resolver_type,
			rr.resolver_id,
			rr.created_at,
			rr.updated_at,
			rr.status_message,
			CASE WHEN rr.data IS NOT NULL THEN jsonb_build_object('data', rr.data->'data', 'provider_config', rr.data->'provider_config') END as data,
			r.tenant_id as tenant_id,
			r.cloud_account_id as account_id,
			CASE WHEN r.recommendation IS NOT NULL THEN jsonb_build_object('spec', r.recommendation->'spec', 'metadata', r.recommendation->'metadata', 'namespace', r.recommendation->'namespace') END as rec_recommendation,
			r.rule_name as rec_rule_name,
			r.severity as rec_severity,
			r.status as rec_status,
			r.recommendation_action as rec_recommendation_action,
			r.category as rec_category,
			r.estimated_savings as rec_estimated_savings,
			cr.name as rec_resource_name,
			CASE WHEN cr.meta IS NOT NULL THEN jsonb_build_object('namespace', cr.meta->'namespace', 'config', cr.meta->'config', 'controller', cr.meta->'controller') END as rec_resource_meta,
			u.display_name as resolver_display_name
			FROM recommendation_resolution rr
			LEFT JOIN recommendation r ON r.id = rr.recommendation_id
			LEFT JOIN cloud_resourses cr ON cr.id = r.resource_id
			LEFT JOIN users u ON rr.resolver_type = 'User' AND u.id::text = rr.resolver_id
		) as recommendation_resolution_v2`,
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id":                        {Type: ColumnDefinitionTypeString},
			"recommendation_id":         {Type: ColumnDefinitionTypeString},
			"type":                      {Type: ColumnDefinitionTypeString},
			"type_reference_id":         {Type: ColumnDefinitionTypeString},
			"status":                    {Type: ColumnDefinitionTypeString},
			"status_message":            {Type: ColumnDefinitionTypeString},
			"resolver_id":               {Type: ColumnDefinitionTypeString},
			"resolver_type":             {Type: ColumnDefinitionTypeString},
			"data":                      {Type: ColumnDefinitionTypeJson},
			"created_at":                {Type: ColumnDefinitionTypeDatetime},
			"updated_at":                {Type: ColumnDefinitionTypeDatetime},
			"tenant_id":                 {Type: ColumnDefinitionTypeString},
			"account_id":                {Type: ColumnDefinitionTypeString},
			"rec_recommendation":        {Type: ColumnDefinitionTypeString},
			"rec_rule_name":             {Type: ColumnDefinitionTypeString},
			"rec_severity":              {Type: ColumnDefinitionTypeString},
			"rec_status":                {Type: ColumnDefinitionTypeString},
			"rec_recommendation_action": {Type: ColumnDefinitionTypeString},
			"rec_category":              {Type: ColumnDefinitionTypeString},
			"rec_estimated_savings":     {Type: ColumnDefinitionTypeFloat},
			"rec_resource_name":         {Type: ColumnDefinitionTypeString},
			"rec_resource_meta":         {Type: ColumnDefinitionTypeJson},
			"resolver_display_name":     {Type: ColumnDefinitionTypeString},
		},
	},
	"recommendation_resolution_groupings_v2": {
		Type:                Aggregate,
		Source:              database.Metastore,
		Def:                 "(SELECT rr.*, r.tenant_id, r.cloud_account_id as account_id FROM recommendation_resolution rr LEFT JOIN recommendation r ON r.id = rr.recommendation_id) as rr_agg",
		Name:                "recommendation_resolution_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
			"resolver_type":     {Type: ColumnDefinitionTypeString},
			"type":              {Type: ColumnDefinitionTypeString},
			"status":            {Type: ColumnDefinitionTypeString},
			"recommendation_id": {Type: ColumnDefinitionTypeString},
			"tenant_id":         {Type: ColumnDefinitionTypeString},
			"account_id":        {Type: ColumnDefinitionTypeString},
		},
	},
	"event_resolution_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "event_resolution_v2",
		Def: `(SELECT er.*,
			e.tenant as tenant_id,
			e.cloud_account_id as account_id,
			u.display_name as resolver_display_name,
			ev.subject_name as event_subject_name,
			ev.subject_namespace as event_subject_namespace,
			ev.cloud_account_id as event_cloud_account_id,
			ev.priority as event_priority,
			ev.category as event_category
			FROM event_resolution er
			LEFT JOIN events e ON e.id = er.event_id
			LEFT JOIN events ev ON ev.id = er.event_id
			LEFT JOIN users u ON er.resolver_type = 'User' AND u.id::text = er.resolver_id
		) as event_resolution_v2`,
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id":                      {Type: ColumnDefinitionTypeString},
			"event_id":                {Type: ColumnDefinitionTypeString},
			"type":                    {Type: ColumnDefinitionTypeString},
			"type_reference_id":       {Type: ColumnDefinitionTypeString},
			"status":                  {Type: ColumnDefinitionTypeString},
			"status_message":          {Type: ColumnDefinitionTypeString},
			"resolver_id":             {Type: ColumnDefinitionTypeString},
			"resolver_type":           {Type: ColumnDefinitionTypeString},
			"data":                    {Type: ColumnDefinitionTypeJson},
			"created_at":              {Type: ColumnDefinitionTypeDatetime},
			"updated_at":              {Type: ColumnDefinitionTypeDatetime},
			"tenant_id":               {Type: ColumnDefinitionTypeString},
			"account_id":              {Type: ColumnDefinitionTypeString},
			"resolver_display_name":   {Type: ColumnDefinitionTypeString},
			"event_subject_name":      {Type: ColumnDefinitionTypeString},
			"event_subject_namespace": {Type: ColumnDefinitionTypeString},
			"event_cloud_account_id":  {Type: ColumnDefinitionTypeString},
			"event_priority":          {Type: ColumnDefinitionTypeString},
			"event_category":          {Type: ColumnDefinitionTypeString},
		},
	},
	"event_resolution_groupings_v2": {
		Type:                Aggregate,
		Source:              database.Metastore,
		Def:                 "(SELECT er.*, e.tenant as tenant_id, e.cloud_account_id as account_id, e.cloud_account_id as event_cloud_account_id FROM event_resolution er LEFT JOIN events e ON e.id = er.event_id) as er_agg",
		Name:                "event_resolution_groupings_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
			"tenant_id":              {Type: ColumnDefinitionTypeString},
			"account_id":             {Type: ColumnDefinitionTypeString},
			"event_cloud_account_id": {Type: ColumnDefinitionTypeString},
			"resolver_type":          {Type: ColumnDefinitionTypeString},
		},
	},
	"llm_agents_installation_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "llm_agents_installation_v2",
		Def: `(SELECT lai.*,
			ca.tenant as tenant_id
			FROM llm_agents_installation lai
			LEFT JOIN cloud_accounts ca ON ca.id = lai.account_id
		) as llm_agents_installation_v2`,
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id":                      {Type: ColumnDefinitionTypeString},
			"agent_id":                {Type: ColumnDefinitionTypeString},
			"account_id":              {Type: ColumnDefinitionTypeString},
			"tenant_id":               {Type: ColumnDefinitionTypeString},
			"config":                  {Type: ColumnDefinitionTypeJson},
			"tools":                   {Type: ColumnDefinitionTypeJson},
			"additional_instructions": {Type: ColumnDefinitionTypeString},
			"created_at":              {Type: ColumnDefinitionTypeDatetime},
			"created_by":              {Type: ColumnDefinitionTypeString},
			"updated_at":              {Type: ColumnDefinitionTypeDatetime},
			"updated_by":              {Type: ColumnDefinitionTypeString},
		},
	},
	"llm_functions_v2": {
		Type:                Normal,
		Def:                 "llm_functions",
		Name:                "llm_functions_v2",
		Source:              database.Metastore,
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id":                {Type: ColumnDefinitionTypeString},
			"tenant_id":         {Type: ColumnDefinitionTypeString},
			"account_id":        {Type: ColumnDefinitionTypeString},
			"name":              {Type: ColumnDefinitionTypeString},
			"description":       {Type: ColumnDefinitionTypeString},
			"prompt":            {Type: ColumnDefinitionTypeString},
			"variables":         {Type: ColumnDefinitionTypeJson},
			"variable_defaults": {Type: ColumnDefinitionTypeJson},
			"status":            {Type: ColumnDefinitionTypeString},
			"version":           {Type: ColumnDefinitionTypeInt},
			"created_by":        {Type: ColumnDefinitionTypeString},
			"updated_by":        {Type: ColumnDefinitionTypeString},
			"created_at":        {Type: ColumnDefinitionTypeDatetime},
			"updated_at":        {Type: ColumnDefinitionTypeDatetime},
		},
	},
	"llm_conversation_list_v2": {
		Type:                Derived,
		Source:              database.Metastore,
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Name:                "llm_conversation_list_v2",
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			userId := ctx.GetSecurityContext().GetUserId()
			dialect := &postgresDialect{}

			// When userId is empty (e.g. admin role), use NULL to avoid UUID parse error
			userIdSQL := "NULL::uuid"
			if userId != "" {
				userIdSQL = dialect.QuoteLiteral(userId) + "::uuid"
			}

			// splitWhereClause recursively separates filters into matched and remaining
			var splitWhereClause func(where QueryWhereClause, matchColumns []string) (matched QueryWhereClause, remaining QueryWhereClause)
			splitWhereClause = func(where QueryWhereClause, matchColumns []string) (matched QueryWhereClause, remaining QueryWhereClause) {
				matched = QueryWhereClause{Binary: make(BinaryWhereClause)}
				remaining = QueryWhereClause{Binary: make(BinaryWhereClause)}

				for col, clauses := range where.Binary {
					if lo.Contains(matchColumns, col) {
						matched.Binary[col] = clauses
					} else {
						remaining.Binary[col] = clauses
					}
				}

				for _, andClause := range where.And {
					matchChild, restChild := splitWhereClause(andClause, matchColumns)
					if hasFilters(matchChild) {
						matched.And = append(matched.And, matchChild)
					}
					if hasFilters(restChild) {
						remaining.And = append(remaining.And, restChild)
					}
				}

				for _, orClause := range where.Or {
					matchChild, restChild := splitWhereClause(orClause, matchColumns)
					if hasFilters(matchChild) {
						matched.Or = append(matched.Or, matchChild)
					}
					if hasFilters(restChild) {
						remaining.Or = append(remaining.Or, restChild)
					}
				}

				if where.Not != nil {
					matchChild, restChild := splitWhereClause(*where.Not, matchColumns)
					if hasFilters(matchChild) {
						matched.Not = &matchChild
					}
					if hasFilters(restChild) {
						remaining.Not = &restChild
					}
				}

				return matched, remaining
			}

			// Extract message_search filters from WHERE and convert to EXISTS subquery
			messageSearchColumns := []string{"message_search"}
			messageSearchWhere, remaining := splitWhereClause(request.Where, messageSearchColumns)
			request.Where = remaining

			// Extract extract_event_ids_from_title flag from WHERE
			extractEventIdsColumns := []string{"extract_event_ids_from_title"}
			extractEventIdsWhere, remaining2 := splitWhereClause(remaining, extractEventIdsColumns)

			// Extract event_status filter from WHERE (filters by event status of IDs in title)
			eventStatusColumns := []string{"event_status"}
			eventStatusWhere, remaining3 := splitWhereClause(remaining2, eventStatusColumns)
			request.Where = remaining3

			messageSearchSQL := ""
			if hasFilters(messageSearchWhere) {
				tempTableDef := TableDefinition{
					Columns: map[string]ColumnDefinition{
						"message_search": {
							Type: ColumnDefinitionTypeString,
							Def:  "ms.message",
						},
					},
					Type:   Normal,
					Def:    "llm_conversation_messages",
					Name:   "llm_conversation_messages",
					Source: database.Metastore,
				}
				searchWhereStr, err := generateWhereClause(messageSearchWhere, tempTableDef)
				if err != nil {
					return "", request, fmt.Errorf("failed to generate message search where clause: %w", err)
				}
				messageSearchSQL = fmt.Sprintf(" AND EXISTS (SELECT 1 FROM llm_conversation_messages ms WHERE ms.conversation_id = c.id AND (%s))", searchWhereStr)
			}

			extractEventIdSQL := ""
			if clauses, ok := extractEventIdsWhere.Binary["extract_event_ids_from_title"]; ok {
				for op, val := range clauses {
					if op == "_eq" {
						if b, ok := val.(bool); ok && b {
							extractEventIdSQL = ` AND c.title ~ '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}'`
						}
					}
				}
			}

			eventStatusSQL := ""
			if clauses, ok := eventStatusWhere.Binary["event_status"]; ok {
				for op, val := range clauses {
					if op == "_eq" {
						if s, ok := val.(string); ok && s != "" {
							quotedStatus := dialect.QuoteLiteral(s)
							eventStatusSQL = fmt.Sprintf(
								` AND EXISTS (SELECT 1 FROM events e WHERE e.id = ANY(ARRAY(SELECT (regexp_matches(c.title, '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}', 'g'))[1]::uuid)) AND e.status = %s AND e.tenant = c.tenant_id)`,
								quotedStatus,
							)
						}
					}
				}
			}

			def := fmt.Sprintf(`(
				SELECT
					c.id::text as id,
					c.updated_at,
					c.status,
					c.user_id::text as user_id,
					c.session_id,
					c.created_at,
					c.source,
					c.title,
					c.account_id::text as account_id,
					c.tenant_id::text as tenant_id,
					u.display_name as user_display_name,
					u.username as user_username,
					(SELECT json_build_object('id', m.id::text, 'status', m.status)
					 FROM llm_conversation_messages m
					 WHERE m.conversation_id = c.id AND m.role = 'human' AND m.message_type = 'generation'
					 ORDER BY m.updated_at DESC LIMIT 1)::text as for_status,
					CASE WHEN EXISTS(
						SELECT 1 FROM llm_conversation_saved s
						WHERE s.conversation_id = c.id AND s.user_id = %s
					) THEN true ELSE false END as is_saved
				FROM llm_conversations c
				LEFT JOIN users u ON u.id = c.user_id
				WHERE EXISTS (
					SELECT 1 FROM llm_conversation_messages m
					WHERE m.conversation_id = c.id
					AND m.message_type = 'generation'
					AND m.role = 'human'
				)%s%s%s
			) as llm_conversation_list_v2`, userIdSQL, messageSearchSQL, extractEventIdSQL, eventStatusSQL)

			return def, request, nil
		},
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"updated_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "updated_at",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"user_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "user_id",
			},
			"session_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "session_id",
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at",
			},
			"source": {
				Type: ColumnDefinitionTypeString,
				Def:  "source",
			},
			"title": {
				Type: ColumnDefinitionTypeString,
				Def:  "title",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_id",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"user_display_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "user_display_name",
			},
			"user_username": {
				Type: ColumnDefinitionTypeString,
				Def:  "user_username",
			},
			"for_status": {
				Type: ColumnDefinitionTypeJson,
				Def:  "for_status",
			},
			"is_saved": {
				Type: ColumnDefinitionTypeBoolean,
				Def:  "is_saved",
			},
			"total_count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "COUNT(*) OVER()",
				IsAggregated: true,
			},
		},
	},
	"llm_conversation_detail_v2": {
		Type:                Derived,
		Source:              database.Metastore,
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Name:                "llm_conversation_detail_v2",
		Def: `(
			SELECT
				c.id::text as id,
				c.session_id,
				c.account_id::text as account_id,
				c.created_at,
				c.updated_at,
				c.source,
				c.context,
				c.status,
				c.user_id::text as user_id,
				c.title,
				c.tenant_id::text as tenant_id,
				u.display_name as user_display_name,
				COALESCE((
					SELECT json_agg(msg_row ORDER BY msg_row.created_at ASC)
					FROM (
						SELECT
							m.id::text as id,
							mu.display_name as user_display_name,
							m.created_at,
							m.updated_at,
							m.message,
							m.message_type,
							m.response,
							m.role,
							m.status,
							m.parent_agent_id::text as parent_agent_id,
							m.message_config,
							m.ack_message,
							COALESCE((
								SELECT json_agg(agent_row ORDER BY agent_row.created_at ASC)
								FROM (
									SELECT
										a.id::text as id,
										a.agent_name,
										a.response,
										a.created_at,
										a.updated_at,
										a.query,
										a.thought,
										a.parent_agent_id::text as parent_agent_id,
										a.status,
										a.response_summary,
										a.references,
										COALESCE((
											SELECT json_agg(tool_row ORDER BY tool_row.created_at ASC)
											FROM (
												SELECT
													t.tool_name,
													t.parameters,
													t.response,
													t.created_at,
													t.updated_at,
													t.thought,
													t.tool_type,
													t.child_agent_id,
													t.agent_id::text as agent_id,
													t.references,
													t.tool_id,
													t.status
												FROM llm_conversation_tool_calls t
												WHERE t.agent_id = a.id
											) tool_row
										), '[]'::json) as llm_conversation_tool_calls
									FROM llm_conversation_agent a
									WHERE a.message_id = m.id
								) agent_row
							), '[]'::json) as llm_conversation_agents
						FROM llm_conversation_messages m
						LEFT JOIN users mu ON mu.id = m.user_id
						WHERE m.conversation_id = c.id AND m.message_type IN ('generation', 'followup')
					) msg_row
				), '[]'::json)::text as messages
			FROM llm_conversations c
			LEFT JOIN users u ON u.id = c.user_id
		) as llm_conversation_detail_v2`,
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"session_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "session_id",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_id",
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at",
			},
			"updated_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "updated_at",
			},
			"source": {
				Type: ColumnDefinitionTypeString,
				Def:  "source",
			},
			"context": {
				Type: ColumnDefinitionTypeString,
				Def:  "context",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"user_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "user_id",
			},
			"title": {
				Type: ColumnDefinitionTypeString,
				Def:  "title",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"user_display_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "user_display_name",
			},
			"messages": {
				Type: ColumnDefinitionTypeJson,
				Def:  "messages",
			},
		},
	},
	"llm_conversation_detail_polling_v2": {
		Type:                Derived,
		Source:              database.Metastore,
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Name:                "llm_conversation_detail_polling_v2",
		Def: `(
			SELECT
				c.id::text as id,
				c.session_id,
				c.account_id::text as account_id,
				c.created_at,
				c.updated_at,
				c.source,
				c.context,
				c.status,
				c.user_id::text as user_id,
				c.title,
				c.tenant_id::text as tenant_id,
				u.display_name as user_display_name,
				COALESCE((
					SELECT json_agg(msg_row ORDER BY msg_row.created_at ASC)
					FROM (
						SELECT
							m.id::text as id,
							mu.display_name as user_display_name,
							m.created_at,
							m.updated_at,
							m.message,
							m.message_type,
							CASE
								WHEN m.message_type = 'followup' THEN COALESCE(m.response, '')
								ELSE COALESCE(m.compact_response, '') 
							END as response,
							m.role,
							m.status,
							m.parent_agent_id::text as parent_agent_id,
							m.message_config,
							m.ack_message,
							COALESCE((
								SELECT json_agg(agent_row ORDER BY agent_row.created_at ASC)
								FROM (
									SELECT
										a.id::text as id,
										a.agent_name,
										COALESCE(a.response_summary, '') as response,
										a.created_at,
										a.updated_at,
										a.query,
										a.thought,
										a.parent_agent_id::text as parent_agent_id,
										a.status,
										a.references,
										COALESCE((
											SELECT json_agg(tool_row ORDER BY tool_row.created_at ASC)
											FROM (
												SELECT
													t.tool_name,
													''::text as parameters,
													''::text as response,
													t.created_at,
													t.updated_at,
													t.thought,
													t.tool_type,
													t.child_agent_id,
													t.agent_id::text as agent_id,
													t.references,
													t.tool_id,
													t.status
												FROM llm_conversation_tool_calls t
												WHERE t.agent_id = a.id
											) tool_row
										), '[]'::json) as llm_conversation_tool_calls
									FROM llm_conversation_agent a
									WHERE a.message_id = m.id
								) agent_row
							), '[]'::json) as llm_conversation_agents
						FROM llm_conversation_messages m
						LEFT JOIN users mu ON mu.id = m.user_id
						WHERE m.conversation_id = c.id AND m.message_type IN ('generation', 'followup')
					) msg_row
				), '[]'::json)::text as messages
			FROM llm_conversations c
			LEFT JOIN users u ON u.id = c.user_id
		) as llm_conversation_detail_polling_v2`,
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"session_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "session_id",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_id",
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at",
			},
			"updated_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "updated_at",
			},
			"source": {
				Type: ColumnDefinitionTypeString,
				Def:  "source",
			},
			"context": {
				Type: ColumnDefinitionTypeString,
				Def:  "context",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"user_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "user_id",
			},
			"title": {
				Type: ColumnDefinitionTypeString,
				Def:  "title",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"user_display_name": {
				Type: ColumnDefinitionTypeString,
				Def:  "user_display_name",
			},
			"messages": {
				Type: ColumnDefinitionTypeJson,
				Def:  "messages",
			},
		},
	},
	"llm_conversation_groupings_v2": {
		Type:                Aggregate,
		Source:              database.Metastore,
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Name:                "llm_conversation_groupings_v2",
		DefGenerator: func(ctx *security.RequestContext, accountId string, request QueryRequest) (string, QueryRequest, error) {
			// splitWhereClause recursively separates filters into matched and remaining
			var splitWhereClause func(where QueryWhereClause, matchColumns []string) (matched QueryWhereClause, remaining QueryWhereClause)
			splitWhereClause = func(where QueryWhereClause, matchColumns []string) (matched QueryWhereClause, remaining QueryWhereClause) {
				matched = QueryWhereClause{Binary: make(BinaryWhereClause)}
				remaining = QueryWhereClause{Binary: make(BinaryWhereClause)}

				for col, clauses := range where.Binary {
					if lo.Contains(matchColumns, col) {
						matched.Binary[col] = clauses
					} else {
						remaining.Binary[col] = clauses
					}
				}

				for _, andClause := range where.And {
					matchChild, restChild := splitWhereClause(andClause, matchColumns)
					if hasFilters(matchChild) {
						matched.And = append(matched.And, matchChild)
					}
					if hasFilters(restChild) {
						remaining.And = append(remaining.And, restChild)
					}
				}

				for _, orClause := range where.Or {
					matchChild, restChild := splitWhereClause(orClause, matchColumns)
					if hasFilters(matchChild) {
						matched.Or = append(matched.Or, matchChild)
					}
					if hasFilters(restChild) {
						remaining.Or = append(remaining.Or, restChild)
					}
				}

				if where.Not != nil {
					matchChild, restChild := splitWhereClause(*where.Not, matchColumns)
					if hasFilters(matchChild) {
						matched.Not = &matchChild
					}
					if hasFilters(restChild) {
						remaining.Not = &restChild
					}
				}

				return matched, remaining
			}

			// Extract message_created_at filters from WHERE and convert to EXISTS subquery
			messageCreatedAtColumns := []string{"message_created_at"}
			messageCreatedAtWhere, remaining := splitWhereClause(request.Where, messageCreatedAtColumns)
			request.Where = remaining

			// Extract extract_event_ids_from_title flag from WHERE
			extractEventIdsColumns := []string{"extract_event_ids_from_title"}
			extractEventIdsWhere, remaining2 := splitWhereClause(remaining, extractEventIdsColumns)

			// Extract event_status filter from WHERE (filters by event status of IDs in title)
			eventStatusColumns := []string{"event_status"}
			eventStatusWhere, remaining3 := splitWhereClause(remaining2, eventStatusColumns)
			request.Where = remaining3

			existsCondition := ""
			if hasFilters(messageCreatedAtWhere) {
				tempTableDef := TableDefinition{
					Columns: map[string]ColumnDefinition{
						"message_created_at": {
							Type: ColumnDefinitionTypeDatetime,
							Def:  "m.created_at",
						},
					},
					Type:   Normal,
					Def:    "llm_conversation_messages",
					Name:   "llm_conversation_messages",
					Source: database.Metastore,
				}
				whereStr, err := generateWhereClause(messageCreatedAtWhere, tempTableDef)
				if err != nil {
					return "", request, fmt.Errorf("failed to generate message_created_at where clause: %w", err)
				}
				existsCondition = fmt.Sprintf(" AND EXISTS (SELECT 1 FROM llm_conversation_messages m WHERE m.conversation_id = c.id AND (%s))", whereStr)
			}

			extractEventIdSQL := ""
			if clauses, ok := extractEventIdsWhere.Binary["extract_event_ids_from_title"]; ok {
				for op, val := range clauses {
					if op == "_eq" {
						if b, ok := val.(bool); ok && b {
							extractEventIdSQL = ` AND c.title ~ '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}'`
						}
					}
				}
			}

			eventStatusSQL := ""
			if clauses, ok := eventStatusWhere.Binary["event_status"]; ok {
				dialect := &postgresDialect{}
				for op, val := range clauses {
					if op == "_eq" {
						if s, ok := val.(string); ok && s != "" {
							quotedStatus := dialect.QuoteLiteral(s)
							eventStatusSQL = fmt.Sprintf(
								` AND EXISTS (SELECT 1 FROM events e WHERE e.id = ANY(ARRAY(SELECT (regexp_matches(c.title, '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}', 'g'))[1]::uuid)) AND e.status = %s AND e.tenant = c.tenant_id)`,
								quotedStatus,
							)
						}
					}
				}
			}

			def := fmt.Sprintf(`(
				SELECT
					c.id::text as id,
					c.tenant_id::text as tenant_id,
					c.account_id::text as account_id,
					c.source,
					c.status,
					c.created_at,
					c.updated_at,
					c.title
				FROM llm_conversations c
				WHERE 1=1%s%s%s
			) as llm_conversation_groupings_v2`, existsCondition, extractEventIdSQL, eventStatusSQL)

			return def, request, nil
		},
		Columns: map[string]ColumnDefinition{
			"id": {
				Type: ColumnDefinitionTypeString,
				Def:  "id",
			},
			"tenant_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "tenant_id",
			},
			"account_id": {
				Type: ColumnDefinitionTypeString,
				Def:  "account_id",
			},
			"source": {
				Type: ColumnDefinitionTypeString,
				Def:  "source",
			},
			"status": {
				Type: ColumnDefinitionTypeString,
				Def:  "status",
			},
			"created_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "created_at",
			},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
			"updated_at": {
				Type: ColumnDefinitionTypeDatetime,
				Def:  "updated_at",
			},
			"title": {
				Type: ColumnDefinitionTypeString,
				Def:  "title",
			},
		},
	},
	"knowledge_base_v2": {
		Type:   Normal,
		Source: database.Metastore,
		Name:   "knowledge_base_v2",
		Def:    "knowledge_base",
		Columns: map[string]ColumnDefinition{
			"id":          {Type: ColumnDefinitionTypeString, Def: "id"},
			"rule_name":   {Type: ColumnDefinitionTypeString, Def: "rule_name"},
			"description": {Type: ColumnDefinitionTypeString, Def: "description"},
			"impact":      {Type: ColumnDefinitionTypeString, Def: "impact"},
			"diagnosis":   {Type: ColumnDefinitionTypeString, Def: "diagnosis"},
			"mitigation":  {Type: ColumnDefinitionTypeString, Def: "mitigation"},
			"created_at":  {Type: ColumnDefinitionTypeDatetime, Def: "created_at"},
			"updated_at":  {Type: ColumnDefinitionTypeDatetime, Def: "updated_at"},
		},
	},
	"feature_flag_v2": {
		Type:                Normal,
		Source:              database.Metastore,
		Name:                "feature_flag_v2",
		Def:                 "feature_flag",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id":                {Type: ColumnDefinitionTypeString, Def: "id"},
			"feature_id":        {Type: ColumnDefinitionTypeString, Def: "feature_id"},
			"feature_module_id": {Type: ColumnDefinitionTypeString, Def: "feature_module_id"},
			"status":            {Type: ColumnDefinitionTypeString, Def: "status"},
			"account_id":        {Type: ColumnDefinitionTypeString, Def: "account_id"},
			"tenant_id":         {Type: ColumnDefinitionTypeString, Def: "tenant_id"},
			"created_at":        {Type: ColumnDefinitionTypeDatetime, Def: "created_at"},
		},
	},
	"user_history_v2": {
		Type:                Normal,
		Source:              database.Metastore,
		Name:                "user_history_v2",
		Def:                 "user_history",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "account_id",
		Columns: map[string]ColumnDefinition{
			"id":         {Type: ColumnDefinitionTypeString, Def: "id"},
			"user_id":    {Type: ColumnDefinitionTypeString, Def: "user_id"},
			"tenant_id":  {Type: ColumnDefinitionTypeString, Def: "tenant_id"},
			"account_id": {Type: ColumnDefinitionTypeString, Def: "account_id"},
			"module":     {Type: ColumnDefinitionTypeString, Def: "module"},
			"data":       {Type: ColumnDefinitionTypeString, Def: "data"},
			"meta":       {Type: ColumnDefinitionTypeJson, Def: "meta"},
			"duration":   {Type: ColumnDefinitionTypeFloat, Def: "duration"},
			"status":     {Type: ColumnDefinitionTypeString, Def: "status"},
			"created_at": {Type: ColumnDefinitionTypeDatetime, Def: "created_at"},
			"updated_at": {Type: ColumnDefinitionTypeDatetime, Def: "updated_at"},
		},
	},
	"tenant_v2": {
		Type:               Normal,
		Source:             database.Metastore,
		Name:               "tenant_v2",
		Def:                "tenant",
		TenantIdColumnName: "id",
		Columns: map[string]ColumnDefinition{
			"id":         {Type: ColumnDefinitionTypeString, Def: "id"},
			"name":       {Type: ColumnDefinitionTypeString, Def: "name"},
			"type":       {Type: ColumnDefinitionTypeString, Def: "type"},
			"created_at": {Type: ColumnDefinitionTypeDatetime, Def: "created_at"},
			"updated_at": {Type: ColumnDefinitionTypeDatetime, Def: "updated_at"},
		},
	},
	"feature_v2": {
		Type:   Normal,
		Source: database.Metastore,
		Name:   "feature_v2",
		Def:    "feature",
		Columns: map[string]ColumnDefinition{
			"value":       {Type: ColumnDefinitionTypeString, Def: "value"},
			"description": {Type: ColumnDefinitionTypeString, Def: "description"},
		},
	},
	"tenant_by_user_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "tenant_by_user_v2",
		Def: `(
			SELECT
				t.id::text as id,
				t.name as name,
				u.username as username
			FROM tenant t
			INNER JOIN tenant_users tu ON tu.tenant = t.id
			INNER JOIN users u ON u.id = tu."user"
		) as tenant_by_user_v2`,
		Columns: map[string]ColumnDefinition{
			"id":       {Type: ColumnDefinitionTypeString, Def: "id"},
			"name":     {Type: ColumnDefinitionTypeString, Def: "name"},
			"username": {Type: ColumnDefinitionTypeString, Def: "username"},
		},
	},
	"cloud_account_attrs_v2": {
		Type:                Derived,
		Source:              database.Metastore,
		Name:                "cloud_account_attrs_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "cloud_account_id",
		Def: `(
			SELECT
				caa.id::text as id,
				caa.name as name,
				caa.value as value,
				caa.cloud_account_id::text as cloud_account_id,
				caa.created_at,
				caa.updated_at,
				ca.tenant::text as tenant_id
			FROM cloud_account_attrs caa
			INNER JOIN cloud_accounts ca ON ca.id = caa.cloud_account_id
		) as cloud_account_attrs_v2`,
		Columns: map[string]ColumnDefinition{
			"id":               {Type: ColumnDefinitionTypeString, Def: "id"},
			"name":             {Type: ColumnDefinitionTypeString, Def: "name"},
			"value":            {Type: ColumnDefinitionTypeString, Def: "value"},
			"cloud_account_id": {Type: ColumnDefinitionTypeString, Def: "cloud_account_id"},
			"created_at":       {Type: ColumnDefinitionTypeDatetime, Def: "created_at"},
			"updated_at":       {Type: ColumnDefinitionTypeDatetime, Def: "updated_at"},
			"tenant_id":        {Type: ColumnDefinitionTypeString, Def: "tenant_id"},
		},
	},
	"usergroup_users_v2": {
		Type:               Derived,
		Source:             database.Metastore,
		Name:               "usergroup_users_v2",
		TenantIdColumnName: "tenant_id",
		Def: `(
			SELECT
				ugu.id::text as id,
				ugu."group"::text as group_id,
				u.id::text as user_id,
				u.display_name,
				u.username,
				u.status::text as status,
				ug.tenant::text as tenant_id,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'id', ur.id,
						'role', ur.role,
						'entity_type', ur.entity_type,
						'entity_id', ur.entity_id,
						'roleByRole', json_build_object('display_name', r.display_name)
					))
					FROM user_roles ur
					LEFT JOIN roles r ON r.value = ur.role
					WHERE ur.user_id = u.id),
					'[]'::json
				) as user_roles,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'user_group', json_build_object('name', ug2.name, 'id', ug2.id)
					))
					FROM usergroup_users ugu2
					INNER JOIN user_groups ug2 ON ug2.id = ugu2."group"
					WHERE ugu2."user" = u.id),
					'[]'::json
				) as user_groups
			FROM usergroup_users ugu
			INNER JOIN users u ON u.id = ugu."user"
			INNER JOIN user_groups ug ON ug.id = ugu."group"
		) as usergroup_users_v2`,
		Columns: map[string]ColumnDefinition{
			"id":           {Type: ColumnDefinitionTypeString, Def: "id"},
			"group_id":     {Type: ColumnDefinitionTypeString, Def: "group_id"},
			"user_id":      {Type: ColumnDefinitionTypeString, Def: "user_id"},
			"display_name": {Type: ColumnDefinitionTypeString, Def: "display_name"},
			"username":     {Type: ColumnDefinitionTypeString, Def: "username"},
			"status":       {Type: ColumnDefinitionTypeString, Def: "status"},
			"tenant_id":    {Type: ColumnDefinitionTypeString, Def: "tenant_id"},
			"user_roles":   {Type: ColumnDefinitionTypeJson, Def: "user_roles"},
			"user_groups":  {Type: ColumnDefinitionTypeJson, Def: "user_groups"},
		},
	},
	"usergroup_users_grouping_v2": {
		Type:               Aggregate,
		Source:             database.Metastore,
		Name:               "usergroup_users_grouping_v2",
		TenantIdColumnName: "tenant_id",
		Def: `(
			SELECT
				ugu.id::text as id,
				ugu."group"::text as group_id,
				ug.tenant::text as tenant_id
			FROM usergroup_users ugu
			INNER JOIN user_groups ug ON ug.id = ugu."group"
		) as usergroup_users_grouping`,
		Columns: map[string]ColumnDefinition{
			"id":        {Type: ColumnDefinitionTypeString, Def: "id"},
			"group_id":  {Type: ColumnDefinitionTypeString, Def: "group_id"},
			"tenant_id": {Type: ColumnDefinitionTypeString, Def: "tenant_id"},
			"count": {
				Type:         ColumnDefinitionTypeInt,
				Def:          "count(*)",
				IsAggregated: true,
			},
		},
	},
	"user_auth_by_username_v2": {
		Type:   Derived,
		Source: database.Metastore,
		Name:   "user_auth_by_username_v2",
		Def: `(
			SELECT
				ua.id::text as auth_id,
				ua.credential as credential,
				ua.tenant_id::text as auth_tenant_id,
				ua.expires_at as expires_at,
				ua.status::text as auth_status,
				ua.provider::text as provider,
				ua."user"::text as user_id,
				u.id::text as id,
				u.display_name,
				u.username as username,
				u.status::text as user_status,
				u.created_at,
				u.updated_at,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'id', ur.id,
						'role', ur.role,
						'entity_type', ur.entity_type,
						'entity_id', ur.entity_id
					))
					FROM user_roles ur
					WHERE ur.user_id = u.id),
					'[]'::json
				) as user_roles,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'id', uattr.id,
						'name', uattr.name,
						'value', uattr.value
					))
					FROM user_attrs uattr
					WHERE uattr."user" = u.id),
					'[]'::json
				) as user_attrs,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'id', ua2.id,
						'avatar', ua2.avatar,
						'name', ua2.name,
						'status', ua2.status
					))
					FROM user_auths ua2
					WHERE ua2."user" = u.id),
					'[]'::json
				) as auth_accounts,
				COALESCE(
					(SELECT json_agg(json_build_object(
						'id', tu.tenant,
						'is_default', tu.is_default,
						'name', t.name
					))
					FROM tenant_users tu
					JOIN tenant t ON t.id = tu.tenant
					WHERE tu."user" = u.id),
					'[]'::json
				) as tenants
			FROM user_auths ua
			JOIN users u ON u.id = ua."user"
		) as user_auth_by_username_v2`,
		Columns: map[string]ColumnDefinition{
			"auth_id":        {Type: ColumnDefinitionTypeString, Def: "auth_id"},
			"credential":     {Type: ColumnDefinitionTypeString, Def: "credential"},
			"auth_tenant_id": {Type: ColumnDefinitionTypeString, Def: "auth_tenant_id"},
			"expires_at":     {Type: ColumnDefinitionTypeDatetime, Def: "expires_at"},
			"auth_status":    {Type: ColumnDefinitionTypeString, Def: "auth_status"},
			"provider":       {Type: ColumnDefinitionTypeString, Def: "provider"},
			"user_id":        {Type: ColumnDefinitionTypeString, Def: "user_id"},
			"id":             {Type: ColumnDefinitionTypeString, Def: "id"},
			"display_name":   {Type: ColumnDefinitionTypeString, Def: "display_name"},
			"username":       {Type: ColumnDefinitionTypeString, Def: "username"},
			"user_status":    {Type: ColumnDefinitionTypeString, Def: "user_status"},
			"created_at":     {Type: ColumnDefinitionTypeDatetime, Def: "created_at"},
			"updated_at":     {Type: ColumnDefinitionTypeDatetime, Def: "updated_at"},
			"user_roles":     {Type: ColumnDefinitionTypeJson, Def: "user_roles"},
			"user_attrs":     {Type: ColumnDefinitionTypeJson, Def: "user_attrs"},
			"auth_accounts":  {Type: ColumnDefinitionTypeJson, Def: "auth_accounts"},
			"tenants":        {Type: ColumnDefinitionTypeJson, Def: "tenants"},
		},
	},
	"anomaly_type_v2": {
		Type:   Derived,
		Source: getSource("anomaly_type_v2"),
		Name:   "anomaly_type_v2",
		Def: `(
			SELECT
				value,
				comment
			FROM anomaly_type
		) as anomaly_type_v2`,
		Columns: map[string]ColumnDefinition{
			"value":   {Type: ColumnDefinitionTypeString, Def: "value"},
			"comment": {Type: ColumnDefinitionTypeString, Def: "comment"},
		},
	},
	"cloud_resource_metrics_v2": {
		Type:                Derived,
		Source:              getSource("cloud_resource_metrics_v2"),
		Name:                "cloud_resource_metrics_v2",
		TenantIdColumnName:  "tenant_id",
		AccountIdColumnName: "cloud_account_id",
		Def: `(
			SELECT
				crm.id::text as id,
				crm.metric,
				crm.value,
				crm.timestamp,
				crm.cloud_resource_id::text as cloud_resource_id,
				crm.cloud_account_id::text as cloud_account_id,
				crm.tenant_id::text as tenant_id,
				cr.name as resource_name,
				cr.resourse_id as resource_id,
				cr.service_name as service_name
			FROM cloud_resource_metrics crm
			LEFT JOIN cloud_resourses cr ON cr.id = crm.cloud_resource_id
		) as cloud_resource_metrics_v2`,
		Columns: map[string]ColumnDefinition{
			"id":                {Type: ColumnDefinitionTypeString, Def: "id"},
			"metric":            {Type: ColumnDefinitionTypeString, Def: "metric"},
			"value":             {Type: ColumnDefinitionTypeFloat, Def: "value"},
			"timestamp":         {Type: ColumnDefinitionTypeDatetime, Def: "timestamp"},
			"cloud_resource_id": {Type: ColumnDefinitionTypeString, Def: "cloud_resource_id"},
			"cloud_account_id":  {Type: ColumnDefinitionTypeString, Def: "cloud_account_id"},
			"tenant_id":         {Type: ColumnDefinitionTypeString, Def: "tenant_id"},
			"resource_name":     {Type: ColumnDefinitionTypeString, Def: "resource_name"},
			"resource_id":       {Type: ColumnDefinitionTypeString, Def: "resource_id"},
			"service_name":      {Type: ColumnDefinitionTypeString, Def: "service_name"},
		},
	},
}

func GetTableMetadata(name string) (TableDefinition, bool) {
	def, ok := table_metadata[strings.ToLower(name)]
	return def, ok
}

func getColumnTypeFromClickhouseType(chType string) ColumnDefinitionType {
	switch strings.ToLower(chType) {
	case "string":
		return ColumnDefinitionTypeString
	case "int32", "int64", "uint32", "uint64":
		return ColumnDefinitionTypeInt
	case "float32", "float64":
		return ColumnDefinitionTypeFloat
	case "datetime":
		return ColumnDefinitionTypeDatetime
	case "array(string)":
		return ColumnDefinitionTypeList
	case "map(string, string)":
		return ColumnDefinitionTypeMap
	case "object('json')":
		return ColumnDefinitionTypeJson
	default:
		return ColumnDefinitionTypeString
	}
}

func init() {

	if config.Config.Env == "" {
		return
	}

	// temp, so that we dont have to connect to warehouse on startup
	if len(table_metadata) > 0 {
		return
	}

	// load tables from DB and initialize tableMeta
	warehouse, err := database.GetDatabaseManager(database.Warehouse)
	if err != nil {
		slog.Error("unable to load warehouse connector", "error", err)
		return
	}

	for k, v := range table_metadata {
		if v.Type == Aggregate || v.Type == Derived {
			continue
		}
		tableDef := v

		rows, err := warehouse.Db.Queryx("select name, type from system.columns where table = ? and database = ? ", v.Def, config.Config.ClickhouseDatabase)
		if err != nil {
			slog.Error("unable to query warehouse connector", "error", err, "table", k)
			continue
		}
		defer func() {
			err := rows.Close()
			if err != nil {
				slog.Error("query: unable to close rows", "error", err)
			}
		}()

		columnDefs := tableDef.Columns
		for rows.Next() {
			var row = make(map[string]any)
			err = rows.MapScan(row)
			if err != nil {
				slog.Error("unable to scan rows", "error", err, "table", k)
				break
			}
			columnName := row["name"].(string)
			columnType := getColumnTypeFromClickhouseType(row["type"].(string))
			if _, ok := columnDefs[columnName]; !ok {
				columnDefs[columnName] = ColumnDefinition{
					IsAggregated: false,
					Type:         ColumnDefinitionType(columnType),
					Def:          columnName,
				}
			}
		}
		slog.Info("updating table definition", "table", k, "columns", columnDefs)
		tableDef.Columns = columnDefs
		table_metadata[k] = tableDef
	}
}
