package observability

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"strings"
	"time"
)

var AzureTraceTableDefinition = map[string]query.ColumnDefinition{
	"trace_id": {
		Type: query.ColumnDefinitionTypeString,
		Def:  "operation_Id",
	},
	"timestamp": {
		Type: query.ColumnDefinitionTypeDatetime,
		Def:  "timestamp",
	},
	"http_status_code": {
		Type: query.ColumnDefinitionTypeString,
		Def:  "resultCode",
	},
	"span_id": {
		Type: query.ColumnDefinitionTypeString,
		Def:  "id",
	},
	"name": {
		Type: query.ColumnDefinitionTypeString,
		Def:  "name",
	},
	"parent_span_id": {
		Type: query.ColumnDefinitionTypeString,
		Def:  "operation_ParentId",
	},
	// App Insights stores milliseconds; filters and sorting compare nanoseconds,
	// matching what the converter returns.
	"duration_ns": {
		Type: query.ColumnDefinitionTypeFloat,
		Def:  "duration * 1000000",
	},
	"status_code": {
		Type: query.ColumnDefinitionTypeString,
		Def:  "resultCode",
	},
	"span_name": {
		Type: query.ColumnDefinitionTypeString,
		Def:  "name",
	},
	// The endpoint or statement called, as for other trace sources. Requests
	// tables have a url instead of data.
	"resource": {
		Type: query.ColumnDefinitionTypeString,
		Def:  `column_ifexists("data", column_ifexists("url", ""))`,
	},
	"destination_workload_name": {
		Type: query.ColumnDefinitionTypeString,
		Def:  "tostring(customDimensions[\"destination.workload_name\"])",
	},
	"destination_workload_namespace": {
		Type: query.ColumnDefinitionTypeString,
		Def:  "tostring(customDimensions[\"destination.workload_namespace\"])",
	},
	"workload_name": {
		Type: query.ColumnDefinitionTypeString,
		Def:  "tostring(customDimensions[\"source.workload_name\"])",
	},
	"workload_namespace": {
		Type: query.ColumnDefinitionTypeString,
		Def:  "tostring(customDimensions[\"source.workload_namespace\"])",
	},
	"headers": {
		Type: query.ColumnDefinitionTypeString,
		Def:  "tostring(customDimensions[\"headers\"])",
	},
	"request_payload": {
		Type: query.ColumnDefinitionTypeString,
		Def:  "tostring(customDimensions[\"http.request_payload\"])",
	},
	"http_response": {
		Type: query.ColumnDefinitionTypeString,
		Def:  "tostring(customDimensions[\"http.response\"])",
	},
	"service_name": {
		Type: query.ColumnDefinitionTypeString,
		Def:  "tostring(customDimensions[\"service.name\"])",
	},
}

// appInsightsWorkspaceColumns maps App Insights API column names to the names
// the same columns have in a Log Analytics workspace (AppDependencies etc.).
var appInsightsWorkspaceColumns = map[string]string{
	"operation_Id":       "OperationId",
	"operation_ParentId": "ParentId",
	"id":                 "Id",
	"name":               "Name",
	"timestamp":          "TimeGenerated",
	"duration":           "DurationMs",
	"resultCode":         "ResultCode",
	"customDimensions":   "Properties",
	"customMeasurements": "Measurements",
	"data":               "Data",
	"url":                "Url",
	"target":             "Target",
	"cloud_RoleName":     "AppRoleName",
}

// workspaceToAppInsightsColumns is the reverse map, used to read workspace
// responses with the App Insights column names the row converters expect.
var workspaceToAppInsightsColumns = func() map[string]string {
	m := make(map[string]string, len(appInsightsWorkspaceColumns))
	for k, v := range appInsightsWorkspaceColumns {
		m[v] = k
	}
	return m
}()

// workspaceBagReplacer renames the App Insights property bags to their
// workspace names inside a column expression.
var workspaceBagReplacer = strings.NewReplacer("customDimensions", "Properties", "customMeasurements", "Measurements")

// azureWorkspaceTraceExpressions holds the workspace form of definitions that
// are expressions rather than a single column or property bag.
var azureWorkspaceTraceExpressions = map[string]string{
	"duration_ns": "DurationMs * 1000000",
	"resource":    `column_ifexists("Data", column_ifexists("Url", ""))`,
}

// azureWorkspaceTraceTableDefinition is AzureTraceTableDefinition with every
// column expression rewritten to its workspace name.
var azureWorkspaceTraceTableDefinition = func() map[string]query.ColumnDefinition {
	defs := make(map[string]query.ColumnDefinition, len(AzureTraceTableDefinition))
	for label, def := range AzureTraceTableDefinition {
		if expr, ok := azureWorkspaceTraceExpressions[label]; ok {
			def.Def = expr
		} else if ws, ok := appInsightsWorkspaceColumns[def.Def]; ok {
			def.Def = ws
		} else {
			def.Def = workspaceBagReplacer.Replace(def.Def)
		}
		defs[label] = def
	}
	return defs
}()

// azureTraceTableDefinition returns the column definitions for the endpoint
// this integration's traces are read from.
func azureTraceTableDefinition(cfg integrations.AzureAppInsightsConfig) map[string]query.ColumnDefinition {
	if cfg.UsesAppInsightsAPI() {
		return AzureTraceTableDefinition
	}
	return azureWorkspaceTraceTableDefinition
}

// azureTraceLabelExpr resolves a label to the KQL expression behind it: a
// Nudgebee label (span_name, resource, ...) through the table definition, and a
// raw App Insights column name to its workspace name when reading a workspace.
// Anything else is used as given.
func azureTraceLabelExpr(cfg integrations.AzureAppInsightsConfig, label string) string {
	if def, ok := azureTraceTableDefinition(cfg)[label]; ok {
		return def.Def
	}
	if !cfg.UsesAppInsightsAPI() {
		if ws, ok := appInsightsWorkspaceColumns[label]; ok {
			return ws
		}
		return workspaceBagReplacer.Replace(label)
	}
	return label
}

// normalizeAzureTraceColumns renames workspace columns (OperationId, Properties,
// ...) to their App Insights names so one row converter handles both endpoints.
func normalizeAzureTraceColumns(columns []Column) []Column {
	out := make([]Column, len(columns))
	for i, c := range columns {
		if name, ok := workspaceToAppInsightsColumns[c.Name]; ok {
			c.Name = name
		}
		out[i] = c
	}
	return out
}

type AzureResponse struct {
	Tables []Table `json:"tables"`
}

type Table struct {
	Name    string   `json:"name"`
	Columns []Column `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type AzureAppInsightsSource struct{}

type AzureAppInsightsTraceSource struct{}

func decodeBase64(encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
func (s *AzureAppInsightsTraceSource) GetLabelMapping() map[string]string {
	return map[string]string{}
}

func (s *AzureAppInsightsTraceSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_contains", "_like"}
}

func (s *AzureAppInsightsTraceSource) QueryGroupedTracesCount(sc *security.RequestContext, tracesRequest TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	return common.OpenTelemetryTraceGroupCount{Count: -1}, nil
}

func (s *AzureAppInsightsTraceSource) CountTraces(sc *security.RequestContext, tracesRequest TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	azureInsightsObj := integrations.AzureAppInsights{}
	azureConf, err := integrations.GetAzureAppInsightConfigs(sc, tracesRequest.AccountId)
	if err != nil {
		sc.GetLogger().Error("CountTraces: failed to get azure configs", "error", err)
		return common.OpenTelemetryTraceCount{}, err
	}
	tracesAnalyticsURL, err := integrations.AppInsightsQueryURL(azureConf)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}
	headers, err := azureInsightsObj.GetHeader(azureConf, map[string]string{"Content-Type": "application/json"}, integrations.AppInsightsTokenResource(azureConf))
	if err != nil {
		sc.GetLogger().Error("CountTraces: failed to get azure headers", "error", err)
		return common.OpenTelemetryTraceCount{}, err
	}
	query := fmt.Sprintf("%s | count", integrations.AppInsightsTable(azureConf, azureConf.TracesTableName))
	traceApiRequest := map[string]string{
		"query": query,
	}

	body, err := common.HttpPost(tracesAnalyticsURL, common.HttpWithHeaders(headers), common.HttpWithJsonBody(traceApiRequest))
	if err != nil {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("failed to make request to azure traces api: %w", err)
	}
	defer func() { _ = body.Body.Close() }()

	if body.StatusCode != http.StatusOK {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("azure trace query failed: status=%d body=%s", body.StatusCode, body.Body)
	}

	bodyBytes, err := io.ReadAll(body.Body)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("failed to read azure traces body: %w", err)
	}

	var azureAppInsightsLogs AzureResponse
	if err := common.UnmarshalJson(bodyBytes, &azureAppInsightsLogs); err != nil {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("failed to unmarshal azure traces: %w", err)
	}
	finalCount, err := azureCountFromResponse(azureAppInsightsLogs)
	if err != nil {
		sc.GetLogger().Error("CountTraces: failed to extract count", "error", err)
		return common.OpenTelemetryTraceCount{}, err
	}
	otelTraces := common.OpenTelemetryTraceCount{
		Count: finalCount,
	}
	return otelTraces, nil
}

// azureCountFromResponse extracts the scalar count from a `| count` KQL response.
// An empty result set (no tables/rows) is a valid "zero traces" answer rather
// than a panic — the previous direct `Tables[0].Rows[0][0]` indexing crashed on
// any empty Azure response.
func azureCountFromResponse(resp AzureResponse) (int, error) {
	if len(resp.Tables) == 0 || len(resp.Tables[0].Rows) == 0 || len(resp.Tables[0].Rows[0]) == 0 {
		return 0, nil
	}
	switch v := resp.Tables[0].Rows[0][0].(type) {
	case int:
		return v, nil
	case float64:
		return int(v), nil
	default:
		return 0, fmt.Errorf("unknown type for count: %T", v)
	}
}

func (s *AzureAppInsightsTraceSource) QueryTracesHeatmap(ctx *security.RequestContext, fetchHeatMapRequest TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	return nil, fmt.Errorf("not implemented")
}

// TraceLabelValuesAreComplete marks this source as safe for trace value validation.
// KQL `| distinct <field>` returns every distinct value for the window.
func (s *AzureAppInsightsTraceSource) TraceLabelValuesAreComplete() {}

func (s *AzureAppInsightsTraceSource) GetLabelValues(sc *security.RequestContext, tracesRequest TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	azureInsightsObj := integrations.AzureAppInsights{}
	azureConf, err := integrations.GetAzureAppInsightConfigs(sc, tracesRequest.AccountId)
	if err != nil {
		sc.GetLogger().Error("GetLabelValues: failed to get azure configs", "error", err)
		return common.OpenTelemetryTraceLabelValues{}, err
	}
	tracesAnalyticsURL, err := integrations.AppInsightsQueryURL(azureConf)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, err
	}
	headers, err := azureInsightsObj.GetHeader(azureConf, map[string]string{"Content-Type": "application/json"}, integrations.AppInsightsTokenResource(azureConf))
	if err != nil {
		sc.GetLogger().Error("GetLabelValues: failed to get azure headers", "error", err)
		return common.OpenTelemetryTraceLabelValues{}, err
	}
	// Inject time range into where clause for KQL generator (mirrors injectTimeFilter for QueryTraces)
	if tracesRequest.StartTime != 0 && tracesRequest.EndTime != 0 {
		if tracesRequest.QueryRequest.Where.Binary == nil {
			tracesRequest.QueryRequest.Where.Binary = make(query.BinaryWhereClause)
		}
		tracesRequest.QueryRequest.Where.Binary["timestamp"] = map[query.BinaryWhereClauseType]any{
			query.Between: map[string]any{
				"_gte": time.UnixMilli(tracesRequest.StartTime).UTC().Format(time.RFC3339Nano),
				"_lte": time.UnixMilli(tracesRequest.EndTime).UTC().Format(time.RFC3339Nano),
			},
		}
	}
	partialQuery, err := s.generateKql(sc, azureConf, tracesRequest.QueryRequest)
	if err != nil {
		sc.GetLogger().Error("GetLabelValues: failed to get kql query", "error", err)
		return common.OpenTelemetryTraceLabelValues{}, err
	}
	traceApiRequest := map[string]string{
		"query": fmt.Sprintf("%s | project nb_label = %s | distinct nb_label", partialQuery, azureTraceLabelExpr(azureConf, tracesRequest.Label)),
	}

	body, err := common.HttpPost(tracesAnalyticsURL, common.HttpWithHeaders(headers), common.HttpWithJsonBody(traceApiRequest))
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("failed to make request to azure traces api: %w", err)
	}
	defer func() { _ = body.Body.Close() }()

	if body.StatusCode != http.StatusOK {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("azure trace query failed: status=%d body=%s", body.StatusCode, body.Body)
	}

	bodyBytes, err := io.ReadAll(body.Body)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("failed to read azure traces body: %w", err)
	}

	var azureAppInsightsLogs AzureResponse
	if err := common.UnmarshalJson(bodyBytes, &azureAppInsightsLogs); err != nil {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("failed to unmarshal azure traces: %w", err)
	}
	otelTraces := common.OpenTelemetryTraceLabelValues{
		Values: azureLabelValuesFromResponse(azureAppInsightsLogs),
	}
	return otelTraces, nil
}

// azureLabelValuesFromResponse pulls the first column of each row from a
// `| distinct <label>` KQL response, tolerating an empty result set and empty
// rows instead of panicking on `Tables[0]` / `row[0]` like the prior code did.
func azureLabelValuesFromResponse(resp AzureResponse) []string {
	labelValues := []string{}
	if len(resp.Tables) == 0 {
		return labelValues
	}
	for _, row := range resp.Tables[0].Rows {
		if len(row) == 0 || row[0] == nil {
			continue
		}
		labelValues = append(labelValues, fmt.Sprintf("%v", row[0]))
	}
	return labelValues
}

// injectTimeFilter adds a timestamp _between filter from StartTime/EndTime into the where clause
// so that the KQL generator produces a proper time-range predicate.
func (s *AzureAppInsightsTraceSource) injectTimeFilter(req *TracesV3Request) {
	if req.StartTime == 0 && req.EndTime == 0 {
		return
	}
	if req.QueryRequest.Where.Binary == nil {
		req.QueryRequest.Where.Binary = make(map[string]map[query.BinaryWhereClauseType]any)
	}
	between := map[string]any{}
	if req.StartTime != 0 {
		between["_gte"] = time.UnixMilli(req.StartTime).UTC().Format(time.RFC3339Nano)
	}
	if req.EndTime != 0 {
		between["_lte"] = time.UnixMilli(req.EndTime).UTC().Format(time.RFC3339Nano)
	}
	req.QueryRequest.Where.Binary["timestamp"] = map[query.BinaryWhereClauseType]any{
		Between: between,
	}
}

func (s *AzureAppInsightsTraceSource) generateKql(sc *security.RequestContext, azureConf integrations.AzureAppInsightsConfig, traceQueryBuilderRequest TracesQueryBuilderRequest) (string, error) {
	var tableDef query.TableDefinition
	tableDef.Columns = azureTraceTableDefinition(azureConf)
	tableDef.Def = integrations.AppInsightsTable(azureConf, azureConf.TracesTableName)
	generateKqlQuery := query.KqlGenerator{}
	queryRequest := getQueryRequest(sc, traceQueryBuilderRequest, tableDef, "trace_v2")
	kqlQuery, err := generateKqlQuery.GenerateKqlQuery(queryRequest, tableDef)
	if err != nil {
		sc.GetLogger().Error("generateKql: failed to generate kql", "error", err)
		return "", err
	}
	sc.GetLogger().Info("Generated KQL Query", "query", kqlQuery)
	if traceQueryBuilderRequest.Limit > 0 {
		kqlQuery = fmt.Sprintf("%s | limit %v", kqlQuery, traceQueryBuilderRequest.Limit)
	}
	return kqlQuery, nil
}

func (s *AzureAppInsightsTraceSource) GetQuery(sc *security.RequestContext, tracesRequest TracesV3Request) (string, error) {
	azureConf, err := integrations.GetAzureAppInsightConfigs(sc, tracesRequest.AccountId)
	if err != nil {
		sc.GetLogger().Error("GetAzureAppInsightConfigs: failed to get azure config", "error", err)
		return "", err
	}
	query := ""
	query, err = s.generateKql(sc, azureConf, tracesRequest.QueryRequest)
	if err != nil {
		sc.GetLogger().Error("GetQuery: failed to generate kql", "error", err)
		return "", err
	}
	return query, nil
}

func (c *AzureAppInsightsTraceSource) QueryGroupedTraces(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) ([]TraceGroupingValues, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *AzureAppInsightsTraceSource) QueryTraces(sc *security.RequestContext, tracesRequest TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	azureInsightsObj := integrations.AzureAppInsights{}
	azureConf, err := integrations.GetAzureAppInsightConfigs(sc, tracesRequest.AccountId)
	if err != nil {
		sc.GetLogger().Error("QueryTraces: failed to get azure configs", "error", err)
		return nil, err
	}
	tracesAnalyticsURL, err := integrations.AppInsightsQueryURL(azureConf)
	if err != nil {
		return nil, err
	}
	headers, err := azureInsightsObj.GetHeader(azureConf, map[string]string{"Content-Type": "application/json"}, integrations.AppInsightsTokenResource(azureConf))
	if err != nil {
		sc.GetLogger().Error("QueryTraces: failed to get azure headers", "error", err)
		return nil, err
	}
	s.injectTimeFilter(&tracesRequest)
	query := ""
	if tracesRequest.Query == "" {
		query, err = s.generateKql(sc, azureConf, tracesRequest.QueryRequest)
	} else {
		query = tracesRequest.Query
		if tracesRequest.QueryRequest.Limit > 0 {
			query = fmt.Sprintf("%s | limit %v", query, tracesRequest.QueryRequest.Limit)
		}
	}

	if err != nil {
		sc.GetLogger().Error("QueryTraces: failed to generate kql", "error", err)
		return []common.OpenTelemetryTrace{}, err
	}

	traceApiRequest := map[string]string{
		"query": query,
	}

	body, err := common.HttpPost(tracesAnalyticsURL, common.HttpWithHeaders(headers), common.HttpWithJsonBody(traceApiRequest))
	if err != nil {
		return nil, fmt.Errorf("failed to make request to azure traces api: %w", err)
	}
	defer func() { _ = body.Body.Close() }()

	if body.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("azure trace query failed: status=%d body=%s", body.StatusCode, body.Body)
	}

	bodyBytes, err := io.ReadAll(body.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read azure traces body: %w", err)
	}

	var azureAppInsightsLogs AzureResponse
	if err := common.UnmarshalJson(bodyBytes, &azureAppInsightsLogs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal azure traces: %w", err)
	}
	otelTraces := []common.OpenTelemetryTrace{}
	for _, table := range azureAppInsightsLogs.Tables {
		columns := normalizeAzureTraceColumns(table.Columns)
		for _, row := range table.Rows {
			rowMap := make(map[string]any)
			for i, col := range columns {
				if i < len(row) {
					rowMap[col.Name] = row[i]
				}
			}
			trace := s.convertAzureTraceToOpenteleMetry(rowMap, columns)
			otelTraces = append(otelTraces, trace)
		}
	}
	return otelTraces, nil
}

func (s *AzureAppInsightsTraceSource) convertAzureTraceToOpenteleMetry(azureTraceLog map[string]any, metadata []Column) common.OpenTelemetryTrace {
	otelTrace := common.OpenTelemetryTrace{
		DurationNs:         0,
		SpanAttributes:     make(map[string]string),
		ResourceAttributes: make(map[string]string),
	}
	for _, col := range metadata {
		value, exists := azureTraceLog[col.Name]
		if !exists {
			continue
		}
		switch col.Name {
		case "id":
			if strVal, ok := value.(string); ok {
				otelTrace.SpanID = strVal
			}
		case "operation_Id":
			if strVal, ok := value.(string); ok {
				otelTrace.TraceID = strVal
			}
		case "operation_ParentId":
			if strVal, ok := value.(string); ok {
				otelTrace.ParentSpanID = strVal
			}
		case "customDimensions":
			if strVal, ok := value.(string); ok {
				customDimensions := make(map[string]any)
				err := json.Unmarshal([]byte(strVal), &customDimensions)
				if err != nil {
					continue
				}
				for k, v := range customDimensions {
					switch k {
					case "cloud.account.id":
						otelTrace.ResourceAttributes["cloud.account.id"] = fmt.Sprintf("%v", v)
					// node-agent emits this key misspelled; accept both (see query.DestinationZoneExpr).
					case "destination.cloud.availablity_zone", "destination.cloud.availability_zone":
						otelTrace.ResourceAttributes["cloud.availability_zone"] = fmt.Sprintf("%v", v)
					case "cloud.region":
						otelTrace.ResourceAttributes["cloud.region"] = fmt.Sprintf("%v", v)
					case "container.id":
						otelTrace.ResourceAttributes["container.id"] = fmt.Sprintf("%v", v)
					case "host.id":
						otelTrace.ResourceAttributes["host.id"] = fmt.Sprintf("%v", v)
					case "host.name":
						otelTrace.ResourceAttributes["host.name"] = fmt.Sprintf("%v", v)
					case "service.name":
						otelTrace.ResourceAttributes["service.name"] = fmt.Sprintf("%v", v)
						otelTrace.ServiceName = fmt.Sprintf("%v", v)
					case "http.headers":
						decodedHeader, err := decodeBase64(fmt.Sprintf("%v", v))
						if err != nil {
							otelTrace.SpanAttributes["headers"] = fmt.Sprintf("%v", v)
						} else {
							otelTrace.SpanAttributes["headers"] = fmt.Sprintf("%v", decodedHeader)
						}
					default:
						// Add to attributes
						otelTrace.SpanAttributes[k] = fmt.Sprintf("%v", v)
					}
				}
			}
		case "customMeasurements":
			if strVal, ok := value.(string); ok {
				customDimensions := make(map[string]any)
				err := json.Unmarshal([]byte(strVal), &customDimensions)
				if err != nil {
					continue
				}
				for k, v := range customDimensions {
					otelTrace.SpanAttributes[k] = fmt.Sprintf("%v", v)
				}
			}
		case "name":
			if strVal, ok := value.(string); ok {
				otelTrace.SpanName = strVal
			}
		case "resultCode":
			if strVal, ok := value.(string); ok {
				otelTrace.StatusCode = strVal
				otelTrace.HTTPStatusCode = strVal
			}
		case "duration":
			// App Insights reports duration in milliseconds.
			if floatVal, ok := value.(float64); ok {
				otelTrace.DurationNs = int64(floatVal * float64(time.Millisecond))
			}
		case "timestamp":
			if strVal, ok := value.(string); ok {
				parsedTime, err := time.Parse(time.RFC3339Nano, strVal)
				if err != nil {
					continue
				}
				otelTrace.Timestamp = parsedTime.UTC().Format(time.RFC3339Nano)
			}
		default:
			// Add to attributes
			otelTrace.SpanAttributes[col.Name] = fmt.Sprintf("%v", value)
		}
	}
	// Fill the columns the traces table reads (source, destination, resource)
	// from what App Insights recorded on the span.
	attrs := otelTrace.SpanAttributes
	otelTrace.WorkloadName = firstNonEmpty(attrs["source.workload_name"], otelTrace.ServiceName, attrs["cloud_RoleName"])
	otelTrace.WorkloadNamespace = attrs["source.workload_namespace"]
	otelTrace.DestinationWorkload = attrs["destination.workload_name"]
	otelTrace.DestinationNamespace = attrs["destination.workload_namespace"]
	otelTrace.DestinationName = firstNonEmpty(otelTrace.DestinationWorkload, attrs["target"])
	otelTrace.Resource = firstNonEmpty(attrs["data"], attrs["url"])
	return otelTrace

}

func (s *AzureAppInsightsSource) convertAzureLogToLog(azureTraceLog map[string]any, metadata []Column) OutputLog {
	OutputLog := OutputLog{
		Labels:    map[string]interface{}{},
		Timestamp: "",
		Message:   "",
		Severity:  "",
	}

	for _, col := range metadata {
		value, exists := azureTraceLog[col.Name]
		if !exists {
			continue
		}
		switch col.Name {
		case "LogLevel":
			if strVal, ok := value.(string); ok {
				OutputLog.Severity = strVal
			}
		case "LogMessage":
			if strVal, ok := value.(string); ok {
				OutputLog.Message = strVal
			}
		case "TimeGenerated":
			if strVal, ok := value.(string); ok {
				parsedTime, err := time.Parse(time.RFC3339Nano, strVal)
				if err != nil {
					continue
				}
				OutputLog.Timestamp = parsedTime.UTC().Format(time.RFC3339Nano)
			}
		default:
			// Add to attributes
			OutputLog.Labels[col.Name] = fmt.Sprintf("%v", value)
		}
	}
	return OutputLog

}

func (s *AzureAppInsightsSource) ExecuteQuery(ctx *security.RequestContext, azureConf integrations.AzureAppInsightsConfig, query string) (AzureResponse, error) {
	azureInsightsObj := integrations.AzureAppInsights{}
	headers, err := azureInsightsObj.GetHeader(azureConf, map[string]string{"Content-Type": "application/json"}, integrations.AzureAnalyticsURL)
	if err != nil {
		ctx.GetLogger().Error("ExecuteQuery: failed to get azure configs", "error", err)
		return AzureResponse{}, err
	}
	logApiRequest := map[string]string{
		"query": query,
	}

	logAnalyticsURL := "https://api.loganalytics.io/v1/workspaces/" + azureConf.LogsWorkbookID + "/query"
	res, err := common.HttpPost(logAnalyticsURL, common.HttpWithHeaders(headers), common.HttpWithJsonBody(logApiRequest))
	if err != nil {
		return AzureResponse{}, fmt.Errorf("failed to make request to azure log api: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(res.Body)
		return AzureResponse{}, fmt.Errorf("azure log query failed: status=%d body=%s", res.StatusCode, string(bodyBytes))
	}

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return AzureResponse{}, fmt.Errorf("failed to read azure log body: %w", err)
	}

	var azureAppInsightsLogs AzureResponse
	if err := common.UnmarshalJson(bodyBytes, &azureAppInsightsLogs); err != nil {
		return AzureResponse{}, fmt.Errorf("failed to unmarshal azure logs: %w", err)
	}
	return azureAppInsightsLogs, nil

}

func (s *AzureAppInsightsSource) QueryLogs(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) ([]OutputLog, error) {
	azureConf, err := integrations.GetAzureAppInsightConfigs(ctx, fetchLogRequest.AccountId)
	if err != nil {
		ctx.GetLogger().Error("QueryLogs: failed to get azure configs", "error", err)
		return nil, err
	}
	if fetchLogRequest.Limit == 0 {
		fetchLogRequest.Limit = 1000
	} else if fetchLogRequest.Limit > 1000 {
		return nil, fmt.Errorf("azure app insights: limit exceeds maximum of 1000")
	}
	if fetchLogRequest.Query == "" {
		fetchLogRequest.Query = fmt.Sprintf("%s | limit %d", azureConf.LogsTableName, fetchLogRequest.Limit)
	} else if fetchLogRequest.Limit > 0 {
		fetchLogRequest.Query = fmt.Sprintf("%s | limit %v", fetchLogRequest.Query, fetchLogRequest.Limit)
	}
	azureAppInsightsLogs, err := s.ExecuteQuery(ctx, azureConf, fetchLogRequest.Query)
	if err != nil {
		ctx.GetLogger().Error("QueryLogs: failed to execute query", "error", err)
		return nil, err
	}
	outputLogs := []OutputLog{}
	for _, table := range azureAppInsightsLogs.Tables {
		for _, row := range table.Rows {
			rowMap := make(map[string]any)
			for i, col := range table.Columns {
				if i < len(row) {
					rowMap[col.Name] = row[i]
				}
			}
			trace := s.convertAzureLogToLog(rowMap, table.Columns)
			outputLogs = append(outputLogs, trace)
		}
	}
	return outputLogs, nil
}

func (s *AzureAppInsightsSource) QueryLabels(ctx *security.RequestContext, fetchLogRequest FetchLogLabelRequest) ([]OutputLogLabel, error) {
	azureConf, err := integrations.GetAzureAppInsightConfigs(ctx, fetchLogRequest.AccountId)
	if err != nil {
		ctx.GetLogger().Error("QueryLogs: failed to get azure configs", "error", err)
		return nil, err
	}
	query := fmt.Sprintf("%s | limit 1", azureConf.LogsTableName)
	azureAppInsightsLogs, err := s.ExecuteQuery(ctx, azureConf, query)
	if err != nil {
		ctx.GetLogger().Error("QueryLabels: failed to execute query", "error", err)
		return nil, err
	}
	outputLogs := []OutputLogLabel{}
	for _, table := range azureAppInsightsLogs.Tables {
		for _, col := range table.Columns {
			outputLogs = append(outputLogs, OutputLogLabel{
				Label:      col.Name,
				Attributes: map[string]interface{}{"type": col.Type},
			})
		}
	}
	return outputLogs, nil

}

func (s *AzureAppInsightsSource) QueryLabelValues(ctx *security.RequestContext, fetchLogRequest FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	azureConf, err := integrations.GetAzureAppInsightConfigs(ctx, fetchLogRequest.AccountId)
	if err != nil {
		ctx.GetLogger().Error("QueryLogs: failed to get azure configs", "error", err)
		return nil, err
	}
	query := fmt.Sprintf("%s | distinct %s", azureConf.LogsTableName, fetchLogRequest.LabelName)
	azureAppInsightsLogs, err := s.ExecuteQuery(ctx, azureConf, query)
	if err != nil {
		ctx.GetLogger().Error("QueryLabels: failed to execute query", "error", err)
		return nil, err
	}
	outputLogs := []OutputLogLabelValue{}
	for _, table := range azureAppInsightsLogs.Tables {
		for _, row := range table.Rows {
			if len(row) > 0 {
				outputLogs = append(outputLogs, OutputLogLabelValue{
					Value:      fmt.Sprintf("%v", row[0]),
					Attributes: map[string]interface{}{},
				})
			}
		}
	}
	return outputLogs, nil
}

func (s *AzureAppInsightsSource) GetQuery(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) (string, error) {
	return "", nil
}

func (s *AzureAppInsightsSource) GetLabelMapping() map[string]string {
	return map[string]string{}
}

func (s *AzureAppInsightsSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_contains", "_like"}
}

// QueryRootSpansByTrace returns one root span per trace for the "By Traces" view (see
// queryRootSpansViaSpans): spans are fetched via QueryTraces and reduced to one root per trace.
func (s *AzureAppInsightsTraceSource) QueryRootSpansByTrace(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	return queryRootSpansViaSpans(ctx, s, req)
}

// CountTracesByTrace returns -1 (estimate) for the "By Traces" view; distinct-trace counting is
// not available cheaply for this provider, so the frontend estimates pagination.
func (s *AzureAppInsightsTraceSource) CountTracesByTrace(_ *security.RequestContext, _ TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return countTracesByTraceEstimate()
}

// QueryLabels returns backend-discovered trace label keys. AzureAppInsightsTraceSource has no generic
// label-key discovery API, so it returns an empty list; FetchTraceLabels falls back to
// the derived canonical + mapping label set. Implements TraceSource.QueryLabels.
func (s *AzureAppInsightsTraceSource) QueryLabels(_ *security.RequestContext, _ FetchTraceLabelRequest) ([]OutputTraceLabel, error) {
	return []OutputTraceLabel{}, nil
}
