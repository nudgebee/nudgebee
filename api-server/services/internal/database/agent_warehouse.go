package database

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"slices"
	"strconv"
	"strings"
	"time"

	"net/http"
)

// ChronosphereResponse represents a parsed Chronosphere API response
type ChronosphereResponse struct {
	StatusCode      float64
	Data            any
	ResponseDataMap map[string]any
	RawResponse     []byte
}

// isRetryableError checks if an error message indicates a retryable condition
func isRetryableError(errorMsg string) bool {
	return strings.Contains(errorMsg, "Something went wrong and the request could not complete") ||
		strings.Contains(errorMsg, "In many cases the issue can be resolved by trying again") ||
		strings.Contains(errorMsg, "Service Unavailable")
}

// executeChronosphereRequestWithRetry handles HTTP requests to Chronosphere with retry logic
func (s agentWarehouseStmt) executeChronosphereRequestWithRetry(requestData map[string]any, requestType string) (*ChronosphereResponse, error) {
	const (
		maxRetries = 1
		baseDelay  = 1 * time.Second
	)

	requestBody, err := common.MarshalJson(requestData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	var lastErr error
	httpClient := &http.Client{Transport: common.HttpClient().Transport, Timeout: 30 * time.Second}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := baseDelay * time.Duration(1<<(attempt-1)) // Exponential backoff: 1s, 2s, 4s
			slog.Warn("Retrying Chronosphere request",
				"request_type", requestType,
				"attempt", attempt,
				"delay_seconds", delay.Seconds(),
				"max_retries", maxRetries)
			time.Sleep(delay)
		}

		// Create HTTP request
		stringReader := bytes.NewReader(requestBody)
		httpRequest, err := http.NewRequestWithContext(s.ctx, "POST", fmt.Sprintf("%s/request", config.Config.RelayServerEndpoint), stringReader)
		if err != nil {
			return nil, fmt.Errorf("unable to create HTTP request: %v", err)
		}

		httpRequest.Header.Add("Content-Type", "application/json")
		httpRequest.Header.Add("Accept", "application/json")
		httpRequest.Header.Add("X-SECRET-KEY", config.Config.RelayServerSecretKey)

		// Execute request
		resp, err := httpClient.Do(httpRequest)
		if err != nil {
			lastErr = fmt.Errorf("HTTP request failed: %v", err)
			continue
		}

		// Read response body
		response, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("failed to read response body: %v", err)
			continue
		}

		// Handle HTTP error status codes (retry only on 503)
		if resp.StatusCode == http.StatusServiceUnavailable {
			lastErr = fmt.Errorf("service unavailable (503): %s", string(response))
			slog.Warn("Chronosphere API returned 503 Service Unavailable",
				"request_type", requestType,
				"attempt", attempt+1,
				"response", string(response))
			continue
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP error (status %d): %s", resp.StatusCode, string(response))
		}

		// Parse JSON response
		var jsonResponse map[string]any
		if err := common.UnmarshalJson(response, &jsonResponse); err != nil {
			lastErr = fmt.Errorf("failed to parse JSON response: %v", err)
			continue
		}

		// Check status_code in response
		statusCode, hasStatus := jsonResponse["status_code"].(float64)
		if !hasStatus || statusCode != 200 {
			lastErr = fmt.Errorf("API returned error status: %v", statusCode)
			continue
		}

		// Extract response data
		responseData := jsonResponse["data"]
		if responseData == nil {
			lastErr = fmt.Errorf("no data in response")
			continue
		}

		// Safely perform type assertion for outer data map
		responseDataOuter, ok := responseData.(map[string]any)
		if !ok {
			lastErr = fmt.Errorf("invalid response data structure: outer data is not a map")
			continue
		}

		// Extract inner data map
		innerData, exists := responseDataOuter["data"]
		if !exists {
			lastErr = fmt.Errorf("missing inner data field")
			continue
		}

		responseDataMap, ok := innerData.(map[string]any)
		if !ok {
			lastErr = fmt.Errorf("invalid response data structure: inner data is not a map")
			continue
		}

		// Check for error_message and determine if retryable
		if errorMsg, exists := responseDataMap["error_message"]; exists && errorMsg != nil {
			errorData := errorMsg

			// Include error_details if available
			if errorDetails, exists := responseDataMap["error_details"]; exists && errorDetails != nil {
				errorData = fmt.Sprintf("%v - %v", errorData, errorDetails)
			}

			errorStr := fmt.Sprintf("%v", errorData)

			// Check if this is a retryable error
			if isRetryableError(errorStr) {
				lastErr = fmt.Errorf("retryable API error: %s", errorStr)
				slog.Warn("Chronosphere API returned retryable error",
					"request_type", requestType,
					"attempt", attempt+1,
					"error_message", errorStr)
				continue
			}

			// Non-retryable error - fail immediately
			return nil, fmt.Errorf("API error: %v", errorData)
		}

		// Success case
		return &ChronosphereResponse{
			StatusCode:      statusCode,
			Data:            responseData,
			ResponseDataMap: responseDataMap,
			RawResponse:     response,
		}, nil
	}

	// All retries exhausted
	return nil, fmt.Errorf("request failed after %d retries: %v", maxRetries, lastErr)
}

const driverName = "agent_warehouse"

func init() {
	sql.Register(driverName, newAgentWarehouseDriver())
}

type agentWarehouseDriver struct {
}

func (d *agentWarehouseDriver) Open(name string) (driver.Conn, error) {
	connector, err := d.OpenConnector(name)
	if err != nil {
		return nil, err
	}

	return connector.Connect(context.TODO())
}

func (d *agentWarehouseDriver) OpenConnector(name string) (driver.Connector, error) {
	return newConnector(name, d), nil
}

func newAgentWarehouseDriver() *agentWarehouseDriver {
	return &agentWarehouseDriver{}
}

type agentWarehouseConnector struct {
	name   string
	driver *agentWarehouseDriver
}

func (c *agentWarehouseConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return newConnection(ctx, c.name)
}

func (c *agentWarehouseConnector) Driver() driver.Driver {
	return c.driver
}

func newConnector(name string, driver *agentWarehouseDriver) *agentWarehouseConnector {
	return &agentWarehouseConnector{
		name:   name,
		driver: driver,
	}
}

type agentWarehouseConnection struct {
	ctx context.Context
}

func (c *agentWarehouseConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions not supported")
}

func (c *agentWarehouseConnection) Prepare(query string) (driver.Stmt, error) {
	return agentWarehouseStmt{query: query, ctx: c.ctx}, nil
}

func (c *agentWarehouseConnection) Close() error {
	return nil
}

func newConnection(ctx context.Context, _ string) (*agentWarehouseConnection, error) {
	return &agentWarehouseConnection{
		ctx: ctx,
	}, nil
}

type agentWarehouseStmt struct {
	query string
	ctx   context.Context
}

func (s agentWarehouseStmt) Close() error {
	return nil
}

func (s agentWarehouseStmt) NumInput() int {
	return -1
}

func (s agentWarehouseStmt) Exec(args []driver.Value) (driver.Result, error) {
	return nil, errors.New("not implemented")
}

func (s agentWarehouseStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if len(args) == 0 {
		return nil, errors.New("invalid number of arguments")
	}

	requestData := map[string]any{
		"no_sinks": true,
		"cache":    false,
		"body": map[string]any{
			"account_id":  args[0].Value,
			"action_name": "query_data",
			"action_params": map[string]any{
				"query": s.query,
			},
		},
	}
	switch args[1].Value {
	case "agent_warehouse_bigquery":
		body := requestData["body"].(map[string]any)
		body["action_name"] = "gke_traces"
	case "agent_warehouse_chronosphere":
		body := requestData["body"].(map[string]any)
		body["action_name"] = "chronosphere_query_traces"
		chronosphereParams, err := s.parseChronosphereParams(s.query)
		if err == nil && chronosphereParams != nil {
			// Check if batching is needed for long durations
			result, batchErr := s.handleChronosphereBatching(requestData, chronosphereParams, args[1].Value.(string))
			if batchErr != nil {
				return nil, batchErr
			}
			if result != nil {
				// Batching was performed, return the batched result
				return result, nil
			}
			// No batching needed, continue with single request
			mappedParams := s.mapToChronosphereAPI(chronosphereParams)
			body["action_params"] = mappedParams
		} else {
			chronosphereParams := map[string]any{
				"query_type": "SERVICE_OPERATION",
				"start_time": time.Now().Add(-15 * time.Minute).Format(time.RFC3339),
				"end_time":   time.Now().Format(time.RFC3339),
			}
			body["action_params"] = chronosphereParams
		}
	}

	// Use the new retry helper function
	response, err := s.executeChronosphereRequestWithRetry(requestData, "main_query")
	if err != nil {
		slog.Error("agent: unable to execute query", "error", err)
		return nil, fmt.Errorf("unable to execute query: %v", err)
	}

	return NewAgentWarehouseRows(response.ResponseDataMap, args[0].Value.(string), args[1].Value.(string))
}

func (s agentWarehouseStmt) Query(args []driver.Value) (driver.Rows, error) {
	namedValues := make([]driver.NamedValue, len(args))
	for i, arg := range args {
		namedValues[i] = driver.NamedValue{
			Ordinal: i + 1,
			Value:   arg,
		}
	}
	return s.QueryContext(s.ctx, namedValues)
}

type AgentWarehouseRows struct {
	rows      []any
	cols      []string
	colTypes  []string
	scanIdx   int
	accountId string
}

func (r *AgentWarehouseRows) Columns() []string {
	return r.cols
}

func (r *AgentWarehouseRows) Close() error {
	return nil
}

func (r *AgentWarehouseRows) Next(dest []driver.Value) error {
	if !r.HasNextResultSet() {
		return io.EOF
	}
	for i, val := range r.rows[r.scanIdx].([]any) {
		dest[i] = val
	}
	r.scanIdx++
	return nil
}

func (r *AgentWarehouseRows) HasNextResultSet() bool {
	return r.scanIdx < len(r.rows)
}

func (r *AgentWarehouseRows) NextResultSet() error {
	r.scanIdx++
	return nil
}

// func (r *agentWarehouseRows) ColumnTypeDatabaseTypeName(index int) string {
// 	return r.colTypes[index]
// }

// func (r *agentWarehouseRows) ColumnTypeLength(index int) (length int64, ok bool) {
// 	return 0, false
// }

// func (r *agentWarehouseRows) ColumnTypeNullable(index int) (nullable, ok bool) {
// 	return false, false
// }

// func (r *agentWarehouseRows) ColumnTypePrecisionScale(index int) (precision, scale int64, ok bool) {
// 	return 0, 0, false
// }

// func (r *agentWarehouseRows) ColumnTypeScanType(index int) reflect.Type {
// 	return reflect.TypeOf(any)
// }

func NewAgentWarehouseRows(data map[string]any, account_id string, provider string) (*AgentWarehouseRows, error) {
	// Handle Chronosphere responses differently
	if provider == "agent_warehouse_chronosphere" {
		return newChronosphereRows(data, account_id)
	}

	// Standard SQL provider response handling (ClickHouse, BigQuery)
	dataCols, ok := data["columns"].([]any)
	if !ok {
		return nil, errors.New("unable to read response data")
	}
	cols := make([]string, len(dataCols))
	for i, col := range dataCols {
		cols[i] = col.(string)
	}
	rows := data["data"].([]any)
	if !slices.Contains(cols, "account_id") {
		cols = append(cols, "account_id")
		for i := range rows {
			rows[i] = append(rows[i].([]any), account_id)
		}
	}

	colTypes := make([]string, len(data["column_types"].([]any)))
	for i, colType := range data["column_types"].([]any) {
		colTypes[i] = colType.(string)
	}
	return &AgentWarehouseRows{
		rows:      rows,
		cols:      cols,
		colTypes:  colTypes,
		accountId: account_id,
	}, nil
}

// parseChronosphereParams extracts Chronosphere API parameters from query string
// Expected input format: JSON string with params like:
// {"service": "global-address-service", "start_time": "2025-08-05T21:39:00Z", ...}
func (s agentWarehouseStmt) parseChronosphereParams(query string) (map[string]any, error) {
	// Try to parse as JSON first (preferred format)
	var params map[string]any
	if err := json.Unmarshal([]byte(query), &params); err == nil {
		return params, nil
	}

	// If not JSON, this indicates an issue with the DefGenerator
	// The DefGenerator should always return valid JSON for Chronosphere

	return nil, fmt.Errorf("unable to parse Chronosphere parameters from query: %s", query)
}

// mapToChronosphereAPI maps our internal parameters to what Chronosphere API expects
func (s agentWarehouseStmt) mapToChronosphereAPI(params map[string]any) map[string]any {
	mappedParams := make(map[string]any)
	// Set required parameters with defaults
	queryType := "SERVICE_OPERATION"
	if qt, exists := params["query_type"]; exists {
		queryType = fmt.Sprintf("%v", qt)
	}
	// Determine query_type based on available parameters
	if traceIds, hasTraceIds := params["trace_ids"]; hasTraceIds {
		if traceIdsList, ok := traceIds.([]any); ok && len(traceIdsList) > 0 {
			queryType = "TRACE_IDS"
			mappedParams["trace_ids"] = traceIds
			// Remove time range parameters when trace IDs are specified
			// as Chronosphere API doesn't allow both
		} else if traceIdsStr, ok := traceIds.([]string); ok && len(traceIdsStr) > 0 {
			queryType = "TRACE_IDS"
			mappedParams["trace_ids"] = traceIds
		}
	}

	mappedParams["query_type"] = queryType

	// Always include time range unless trace_ids is specified
	if queryType != "TRACE_IDS" {
		if startTime, exists := params["start_time"]; exists {
			mappedParams["start_time"] = startTime
		}
		if endTime, exists := params["end_time"]; exists {
			mappedParams["end_time"] = endTime
		}
	}
	// Map service parameter for SERVICE_OPERATION queries
	if queryType == "SERVICE_OPERATION" {
		if service, exists := params["service"]; exists {
			mappedParams["service"] = service
		}
	}
	// Map additional filtering parameters based on Chronosphere API specification
	if operation, exists := params["operation"]; exists {
		mappedParams["operation"] = operation
	}
	// Handle tag_filters in the correct Chronosphere API format
	if tagFilters, exists := params["tag_filters"]; exists {
		mappedParams["tag_filters"] = tagFilters
	}

	// Handle label_filters for span attribute filtering
	if labelFilters, exists := params["label_filters"]; exists {
		if filters, ok := labelFilters.(map[string]string); ok && len(filters) > 0 {
			// Convert label filters to Chronosphere tag_filters format
			chronosphereFilters := make([]map[string]string, 0, len(filters))
			for key, value := range filters {
				chronosphereFilters = append(chronosphereFilters, map[string]string{
					"key":   key,
					"value": value,
				})
			}
			mappedParams["tag_filters"] = chronosphereFilters

			slog.Info("Added label filters to Chronosphere query",
				"label_filters", filters,
				"chronosphere_tag_filters", chronosphereFilters)
		}
	}

	slog.Debug("Chronosphere API parameters",
		"query_type", mappedParams["query_type"],
		"has_time_range", mappedParams["start_time"] != nil && mappedParams["end_time"] != nil,
		"all_params", mappedParams)

	// Note: resource_filter, min_duration, max_duration, status_code are not supported
	// in the Chronosphere API based on the documentation provided

	return mappedParams
}

// newChronosphereRows converts Chronosphere trace response to standard row format
func newChronosphereRows(data map[string]any, account_id string) (*AgentWarehouseRows, error) {
	// Chronosphere returns trace data in OpenTelemetry format
	// We need to extract and flatten the spans into a tabular format

	// Expected structure: data contains "traces" array with spans
	traces, ok := data["traces"].([]any)
	if !ok {
		// Fallback: look for "spans" directly
		if spans, hasSpans := data["spans"].([]any); hasSpans {
			// Create the expected nested structure for the fallback case
			traces = []any{map[string]any{
				"resource_spans": []any{
					map[string]any{
						"scope_spans": []any{
							map[string]any{
								"spans": spans,
							},
						},
					},
				},
			}}
		} else {
			return nil, errors.New("unable to find traces or spans in Chronosphere response")
		}
	}

	// Define the columns to match traces_v2 format
	cols := []string{
		"tenant_id",
		"trace_id",
		"span_id",
		"parent_span_id",
		"workload_name",
		"workload_namespace",
		"timestamp",
		"status_code",
		"span_name",
		"resource",
		"duration_ns",
		"destination_workload_name",
		"destination_workload_namespace",
		"destination_name",
		"headers",
		"http_status_code",
		"request_payload",
		"http_response",
		"trace_source",
		"spanattributes",
		"span_attributes",
		"account_id",
		"events_attributes",
	}

	var rows []any

	// Process each trace
	for _, traceData := range traces {
		trace, ok := traceData.(map[string]any)
		if !ok {
			// Skip invalid trace data
			continue
		}
		resourceSpans, hasResourceSpans := trace["resource_spans"].([]any)
		if !hasResourceSpans {
			continue
		}

		// Process each resource span
		for _, resourceSpanData := range resourceSpans {
			resourceSpan, ok := resourceSpanData.(map[string]any)
			if !ok {
				continue
			}

			scopeSpans, hasScopeSpans := resourceSpan["scope_spans"].([]any)
			if !hasScopeSpans {
				continue
			}

			// Process each scope span
			for _, scopeSpanData := range scopeSpans {
				scopeSpan, ok := scopeSpanData.(map[string]any)
				if !ok {
					continue
				}

				spans, hasSpans := scopeSpan["spans"].([]any)
				if !hasSpans {
					continue
				}

				// Process each span
				for _, spanData := range spans {
					span, ok := spanData.(map[string]any)
					if !ok {
						// Skip invalid span data
						continue
					}

					// Extract basic span fields
					traceID := normalizeTraceIDResponse(getStringField(span, "trace_id", ""))
					spanID := normalizeTraceIDResponse(getStringField(span, "span_id", ""))
					parentSpanID := normalizeTraceIDResponse(getStringField(span, "parent_span_id", ""))
					spanName := getStringField(span, "name", "")
					startTimeNano := getStringField(span, "start_time_unix_nano", "")
					endTimeNano := getStringField(span, "end_time_unix_nano", "")

					// Convert nanosecond timestamps to RFC3339 string for TraceSpan compatibility
					startTime := convertNanoToTimeString(startTimeNano)

					// Extract service name and namespace from attributes
					workloadName := extractServiceNameFromAttributes(span)
					workloadNamespace := extractServiceNamespaceFromAttributes(span)

					// Calculate duration as float64 for TraceSpan compatibility
					duration := calculateDurationAsFloat(startTimeNano, endTimeNano)

					// Extract status - OpenTelemetry status has different structure
					statusCode := extractStatusCode(span)

					// Extract additional traces_v2 fields from attributes
					resource := extractAttributeValue(span, []string{"db.statement", "http.url", "http.target"}, "")
					destinationWorkloadName := extractAttributeValue(span, []string{"destination.workload_name", "messaging.destination", "db.host", "net.peer.name"}, "")
					destinationWorkloadNamespace := extractAttributeValue(span, []string{"destination.workload_namespace", "destination.namespace"}, "")
					destinationName := extractAttributeValue(span, []string{"messaging.destination.name", "messaging.destination", "destination.name", "db.host", "net.peer.name"}, "")
					headers := extractAttributeValue(span, []string{"http.headers"}, "")
					httpStatusCode := extractAttributeValue(span, []string{"http.status_code"}, "")
					requestPayload := extractAttributeValue(span, []string{"http.request_payload"}, "")
					httpResponse := extractAttributeValue(span, []string{"http.response"}, "")

					// Determine trace source based on span attributes
					traceSource := "otel" // default
					if scopeName := extractAttributeValue(span, []string{"otel.scope.name"}, ""); scopeName == "nudgebee-node-agent" {
						traceSource = "ebpf"
					}

					// Extract all span attributes as JSON (spanattributes)
					spanAttributes := extractTagsAsJSON(span)
					events := extractEventsAsJson(span)
					// Create row matching traces_v2 format
					row := []any{
						"",                           // tenant_id (empty for Chronosphere)
						traceID,                      // trace_id
						spanID,                       // span_id
						parentSpanID,                 // parent_span_id
						workloadName,                 // workload_name (service_name)
						workloadNamespace,            // workload_namespace (service_namespace)
						startTime,                    // timestamp (start_time_unix_nano)
						statusCode,                   // status_code
						spanName,                     // span_name (operation name)
						resource,                     // resource
						duration,                     // duration_ns
						destinationWorkloadName,      // destination_workload_name
						destinationWorkloadNamespace, // destination_workload_namespace
						destinationName,              // destination_name
						headers,                      // headers
						httpStatusCode,               // http_status_code
						requestPayload,               // request_payload
						httpResponse,                 // http_response
						traceSource,                  // trace_source
						spanAttributes,               // spanattributes
						spanAttributes,               // spanattributes
						account_id,                   // account_id
						events,                       // events
					}

					rows = append(rows, row)
				}
			}
		}
	}

	// Create column types based on traces_v2 schema from metadata.go
	colTypes := []string{
		"string",   // tenant_id - ColumnDefinitionTypeString
		"string",   // trace_id - ColumnDefinitionTypeString
		"string",   // span_id - ColumnDefinitionTypeString
		"string",   // parent_span_id - ColumnDefinitionTypeString
		"string",   // workload_name - ColumnDefinitionTypeString
		"string",   // workload_namespace - ColumnDefinitionTypeString
		"datetime", // timestamp - "datetime"
		"string",   // status_code - ColumnDefinitionTypeString
		"string",   // span_name - ColumnDefinitionTypeString
		"string",   // resource - ColumnDefinitionTypeString
		"float",    // duration_ns - ColumnDefinitionTypeFloat
		"string",   // destination_workload_name - ColumnDefinitionTypeString
		"string",   // destination_workload_namespace - ColumnDefinitionTypeString
		"string",   // destination_name - ColumnDefinitionTypeString
		"string",   // headers - ColumnDefinitionTypeString
		"string",   // http_status_code - ColumnDefinitionTypeString
		"string",   // request_payload - ColumnDefinitionTypeString
		"string",   // http_response - ColumnDefinitionTypeString
		"string",   // trace_source - ColumnDefinitionTypeString
		"json",     // spanattributes - JSON data type
		"json",     // spanattributes - JSON data type
		"string",   // account_id - ColumnDefinitionTypeString
		"json",     // events - JSON data type
	}

	return &AgentWarehouseRows{
		rows:      rows,
		cols:      cols,
		colTypes:  colTypes,
		accountId: account_id,
	}, nil
}

// Helper functions for Chronosphere response parsing

func getStringField(data map[string]any, key, defaultValue string) string {
	if val, exists := data[key]; exists {
		switch v := val.(type) {
		case string:
			return v
		case int:
			return strconv.Itoa(v)
		case int64:
			return strconv.FormatInt(v, 10)
		case float64:
			// For large numbers (like nanosecond timestamps), ensure we don't get scientific notation
			// JSON numbers are decoded as float64, so we need to convert them properly
			if v == float64(int64(v)) {
				// If it's a whole number, convert to int64 first to avoid scientific notation
				return strconv.FormatInt(int64(v), 10)
			}
			// For actual floating point numbers, use standard formatting
			return strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			return strconv.FormatBool(v)
		default:
			// Last resort for other types
			return fmt.Sprintf("%v", v)
		}
	}
	return defaultValue
}

// findAttributeStringValue searches for the first matching attribute key and returns its string value
func findAttributeStringValue(span map[string]any, targetKeys []string, defaultValue string) string {
	// Helper function to search through attributes array
	searchAttributes := func(attributesRaw any) string {
		if attributes, ok := attributesRaw.([]any); ok {
			for _, attrData := range attributes {
				if attr, ok := attrData.(map[string]any); ok {
					if key, hasKey := attr["key"].(string); hasKey {
						// Check if this key matches any of our target keys
						for _, targetKey := range targetKeys {
							if key == targetKey {
								if valueRaw, hasValue := attr["value"]; hasValue {
									if value, ok := valueRaw.(map[string]any); ok {
										// Try different value types
										if stringVal, hasString := value["string_value"].(string); hasString {
											return stringVal
										} else if intVal, hasInt := value["int_value"]; hasInt {
											return fmt.Sprintf("%v", intVal)
										} else if doubleVal, hasDouble := value["double_value"]; hasDouble {
											return fmt.Sprintf("%.0f", doubleVal)
										} else if boolVal, hasBool := value["bool_value"]; hasBool {
											return fmt.Sprintf("%v", boolVal)
										}
									}
								}
							}
						}
					}
				}
			}
		}
		return ""
	}

	// Look in span attributes first
	if attributesRaw, hasAttrs := span["attributes"]; hasAttrs {
		if result := searchAttributes(attributesRaw); result != "" {
			return result
		}
	}

	// Fallback: look for resource attributes
	if resourceRaw, hasResource := span["resource"]; hasResource {
		if resource, ok := resourceRaw.(map[string]any); ok {
			if attributesRaw, hasAttrs := resource["attributes"]; hasAttrs {
				if result := searchAttributes(attributesRaw); result != "" {
					return result
				}
			}
		}
	}

	return defaultValue
}

func extractServiceNameFromAttributes(span map[string]any) string {
	// First try service.name
	serviceName := findAttributeStringValue(span, []string{"service.name"}, "")
	if serviceName != "" {
		return serviceName
	}

	// Fallback to k8s.pod.name
	podName := findAttributeStringValue(span, []string{"k8s.pod.name"}, "")
	if podName != "" {
		return podName
	}

	return "unknown-service"
}

func extractServiceNamespaceFromAttributes(span map[string]any) string {
	return findAttributeStringValue(span, []string{"service.namespace", "k8s.namespace.name"}, "")
}

// convertNanoToTime converts nanosecond timestamp string to time.Time object

// convertNanoToTimeString converts nanosecond timestamp string to RFC3339 string for TraceSpan compatibility
func convertNanoToTimeString(nanoTimestamp string) string {
	if nanoTimestamp == "" {
		return ""
	}

	nanoTime, err := strconv.ParseInt(nanoTimestamp, 10, 64)
	if err != nil {
		return nanoTimestamp // Return original string if parsing fails
	}

	// Convert nanoseconds to time.Time, then to RFC3339 string
	return time.Unix(0, nanoTime).Format(time.RFC3339)
}

// calculateDurationAsFloat calculates duration as float64 for TraceSpan compatibility
func calculateDurationAsFloat(startTime, endTime string) float64 {
	if startTime != "" && endTime != "" {
		startNano, startErr := strconv.ParseInt(startTime, 10, 64)
		endNano, endErr := strconv.ParseInt(endTime, 10, 64)

		if startErr == nil && endErr == nil {
			durationNano := endNano - startNano
			if durationNano >= 0 {
				return float64(durationNano)
			}
		}
	}
	return 0.0
}

// extractStatusCode extracts the status code from OpenTelemetry status field
// Falls back to HTTP status code if OTel status is not available
// According to OTel spec: missing status = UNSET, JSON numbers are float64
func extractStatusCode(span map[string]any) string {
	// First check for OpenTelemetry status
	if statusRaw, hasStatus := span["status"]; hasStatus {
		// Handle status as object (proper OTel format)
		if status, ok := statusRaw.(map[string]any); ok {
			// Check for status code - handle both int and float64 (JSON unmarshaling)
			if codeRaw, hasCode := status["code"]; hasCode {
				// Handle different code formats
				switch code := codeRaw.(type) {
				case string:
					// Handle string status codes (already in final format)
					if strings.HasPrefix(code, "STATUS_CODE_") {
						return code
					}
					// Handle string representations of numeric codes
					switch code {
					case "0":
						return "STATUS_CODE_UNSET"
					case "1":
						return "STATUS_CODE_OK"
					case "2":
						return "STATUS_CODE_ERROR"
					default:
						return "STATUS_CODE_UNKNOWN"
					}
				case float64:
					codeInt := int(code)
					// Map according to OpenTelemetry status codes
					switch codeInt {
					case 0:
						return "STATUS_CODE_UNSET"
					case 1:
						return "STATUS_CODE_OK"
					case 2:
						return "STATUS_CODE_ERROR"
					default:
						// Unknown code, return formatted version
						return fmt.Sprintf("STATUS_CODE_%d", codeInt)
					}
				case int:
					// Map according to OpenTelemetry status codes
					switch code {
					case 0:
						return "STATUS_CODE_UNSET"
					case 1:
						return "STATUS_CODE_OK"
					case 2:
						return "STATUS_CODE_ERROR"
					default:
						// Unknown code, return formatted version
						return fmt.Sprintf("STATUS_CODE_%d", code)
					}
				case int64:
					codeInt := int(code)
					// Map according to OpenTelemetry status codes
					switch codeInt {
					case 0:
						return "STATUS_CODE_UNSET"
					case 1:
						return "STATUS_CODE_OK"
					case 2:
						return "STATUS_CODE_ERROR"
					default:
						// Unknown code, return formatted version
						return fmt.Sprintf("STATUS_CODE_%d", codeInt)
					}
				default:
					// Invalid code type, fall through to HTTP status check
					break
				}
			}
		} else if statusStr, ok := statusRaw.(string); ok {
			// Handle status as string (some APIs might return this format)
			switch statusStr {
			case "OK":
				return "STATUS_CODE_OK"
			case "ERROR":
				return "STATUS_CODE_ERROR"
			case "UNSET":
				return "STATUS_CODE_UNSET"
			}
		}
	}

	// Fallback: check HTTP status code from attributes to derive OTel status
	httpStatusCode := findAttributeStringValue(span, []string{"http.status_code"}, "")
	if httpStatusCode != "" {
		if statusInt, err := strconv.Atoi(httpStatusCode); err == nil {
			if statusInt >= 400 {
				return "STATUS_CODE_ERROR"
			} else if statusInt >= 100 && statusInt < 400 {
				return "STATUS_CODE_OK"
			}
		}
	}

	// Default to UNSET if no status field (per OpenTelemetry specification)
	return "STATUS_CODE_UNSET"
}

// extractAttributeValue searches for the first matching attribute key and returns its value
func extractAttributeValue(span map[string]any, targetKeys []string, defaultValue string) string {
	return findAttributeStringValue(span, targetKeys, defaultValue)
}

func extractTagsAsJSON(span map[string]any) string {
	tags := make(map[string]any)

	// Helper function to extract attribute value from OpenTelemetry format
	extractAttrValue := func(attr map[string]any) any {
		if valueRaw, hasValue := attr["value"]; hasValue {
			if value, ok := valueRaw.(map[string]any); ok {
				// Try different value types in OpenTelemetry format
				if stringVal, ok := value["string_value"].(string); ok {
					return stringVal
				}
				if intVal, ok := value["int_value"]; ok {
					return intVal
				}
				if doubleVal, ok := value["double_value"]; ok {
					return doubleVal
				}
				if boolVal, ok := value["bool_value"]; ok {
					return boolVal
				}
			}
		}
		return nil
	}

	// Extract span attributes
	if attributesRaw, hasAttrs := span["attributes"]; hasAttrs {
		if attributes, ok := attributesRaw.([]any); ok {
			for _, attrData := range attributes {
				if attr, ok := attrData.(map[string]any); ok {
					if key, hasKey := attr["key"].(string); hasKey {
						if value := extractAttrValue(attr); value != nil {
							tags[key] = value
						}
					}
				}
			}
		}
	}

	// Extract event data (exceptions, logs, etc.)
	if eventsRaw, hasEvents := span["events"]; hasEvents {
		if events, ok := eventsRaw.([]any); ok {
			for _, eventData := range events {
				if event, ok := eventData.(map[string]any); ok {
					// Add event name
					if eventName, hasName := event["name"].(string); hasName {
						tags["event.name"] = eventName
					}

					// Extract event attributes (exception.message, exception.stacktrace, etc.)
					if eventAttributesRaw, hasEventAttrs := event["attributes"]; hasEventAttrs {
						if eventAttributes, ok := eventAttributesRaw.([]any); ok {
							for _, eventAttrData := range eventAttributes {
								if eventAttr, ok := eventAttrData.(map[string]any); ok {
									if eventKey, hasEventKey := eventAttr["key"].(string); hasEventKey {
										if value := extractAttrValue(eventAttr); value != nil {
											tags["event."+eventKey] = value
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Convert to JSON string
	if len(tags) > 0 {
		if jsonBytes, err := json.Marshal(tags); err == nil {
			return string(jsonBytes)
		}
	}

	return "{}"
}

func extractEventsAsJson(span map[string]any) string {
	tags := make(map[string]any)

	// Helper function to extract attribute value from OpenTelemetry format
	extractAttrValue := func(attr map[string]any) any {
		if valueRaw, hasValue := attr["value"]; hasValue {
			if value, ok := valueRaw.(map[string]any); ok {
				// Try different value types in OpenTelemetry format
				if stringVal, ok := value["string_value"].(string); ok {
					return stringVal
				}
				if intVal, ok := value["int_value"]; ok {
					return intVal
				}
				if doubleVal, ok := value["double_value"]; ok {
					return doubleVal
				}
				if boolVal, ok := value["bool_value"]; ok {
					return boolVal
				}
			}
		}
		return nil
	}

	// Extract event data (exceptions, logs, etc.)
	if eventsRaw, hasEvents := span["events"]; hasEvents {
		if events, ok := eventsRaw.([]any); ok {
			for _, eventData := range events {
				if event, ok := eventData.(map[string]any); ok {
					// Add event name
					if eventName, hasName := event["name"].(string); hasName {
						tags["event.name"] = eventName
					}

					// Extract event attributes (exception.message, exception.stacktrace, etc.)
					if eventAttributesRaw, hasEventAttrs := event["attributes"]; hasEventAttrs {
						if eventAttributes, ok := eventAttributesRaw.([]any); ok {
							for _, eventAttrData := range eventAttributes {
								if eventAttr, ok := eventAttrData.(map[string]any); ok {
									if eventKey, hasEventKey := eventAttr["key"].(string); hasEventKey {
										if value := extractAttrValue(eventAttr); value != nil {
											tags["event."+eventKey] = value
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Convert to JSON string
	if len(tags) > 0 {
		if jsonBytes, err := json.Marshal(tags); err == nil {
			return string(jsonBytes)
		}
	}

	return "{}"
}

// getMapKeys returns the keys of a map as a slice for logging
func getMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// executeChunksInParallel executes Chronosphere chunks in parallel with controlled concurrency and retry logic
func (s agentWarehouseStmt) executeChunksInParallel(requestData map[string]any, chronosphereParams map[string]any, startTime, endTime time.Time, maxChunkDuration time.Duration, numChunks int) ([]map[string]any, int) {
	const (
		maxConcurrent = 2
		maxRetries    = 1
	)

	type chunkJob struct {
		index      int
		start      time.Time
		end        time.Time
		params     map[string]any
		retryCount int
	}

	type chunkResult struct {
		index int
		data  map[string]any
		err   error
	}

	// Create job channel with buffered capacity
	jobs := make(chan chunkJob, numChunks)
	results := make(chan chunkResult, numChunks)

	// Start worker goroutines
	for w := 0; w < maxConcurrent; w++ {
		go func(workerID int) {
			for job := range jobs {
				slog.Debug("Worker processing chunk",
					"worker_id", workerID,
					"chunk", job.index+1,
					"retry_attempt", job.retryCount)

				data, err := s.executeChronosphereChunk(requestData, job.params)
				results <- chunkResult{
					index: job.index,
					data:  data,
					err:   err,
				}
			}
		}(w)
	}

	// Send initial jobs
	for i := 0; i < numChunks; i++ {
		chunkStart := startTime.Add(time.Duration(i) * maxChunkDuration)
		chunkEnd := chunkStart.Add(maxChunkDuration)
		if chunkEnd.After(endTime) {
			chunkEnd = endTime
		}

		// Create parameters for this chunk
		chunkParams := make(map[string]any)
		for k, v := range chronosphereParams {
			chunkParams[k] = v
		}
		chunkParams["start_time"] = chunkStart.Format(time.RFC3339)
		chunkParams["end_time"] = chunkEnd.Format(time.RFC3339)

		jobs <- chunkJob{
			index:      i,
			start:      chunkStart,
			end:        chunkEnd,
			params:     chunkParams,
			retryCount: 0,
		}
	}

	// Collect results and handle retries
	allResponsesData := make([]map[string]any, 0, numChunks)
	successfulChunks := 0
	completedChunks := 0
	failedJobs := make(map[int]chunkJob)

	for completedChunks < numChunks {
		result := <-results
		completedChunks++

		if result.err != nil {
			originalJob := failedJobs[result.index]
			if originalJob.retryCount == 0 {
				// Find the original job info
				chunkStart := startTime.Add(time.Duration(result.index) * maxChunkDuration)
				chunkEnd := chunkStart.Add(maxChunkDuration)
				if chunkEnd.After(endTime) {
					chunkEnd = endTime
				}

				chunkParams := make(map[string]any)
				for k, v := range chronosphereParams {
					chunkParams[k] = v
				}
				chunkParams["start_time"] = chunkStart.Format(time.RFC3339)
				chunkParams["end_time"] = chunkEnd.Format(time.RFC3339)

				originalJob = chunkJob{
					index:  result.index,
					start:  chunkStart,
					end:    chunkEnd,
					params: chunkParams,
				}
			}

			if originalJob.retryCount < maxRetries {
				// Retry the failed chunk
				slog.Info("Retrying failed chunk",
					"chunk", result.index+1,
					"retry_attempt", originalJob.retryCount+1,
					"error", result.err)

				retryJob := originalJob
				retryJob.retryCount++
				failedJobs[result.index] = retryJob
				jobs <- retryJob
				completedChunks-- // Don't count this as completed yet
			} else {
				slog.Error("Chunk failed after retries",
					"chunk", result.index+1,
					"total_retries", maxRetries,
					"final_error", result.err)
			}
		} else {
			// Successful chunk
			if result.data != nil {
				if traces, ok := result.data["traces"].([]any); ok {
					slog.Debug("Chunk completed successfully",
						"chunk", result.index+1,
						"traces", len(traces))
				}
				allResponsesData = append(allResponsesData, result.data)
				successfulChunks++
			}
		}
	}

	close(jobs)
	close(results)

	slog.Info("Parallel chunk execution completed",
		"total_chunks", numChunks,
		"successful_chunks", successfulChunks,
		"failed_chunks", numChunks-successfulChunks)

	return allResponsesData, successfulChunks
}

// handleChronosphereBatching handles batching for long duration Chronosphere queries and trace ID limitations
// Returns batched result if batching is needed, nil if single query should proceed, or error
func (s agentWarehouseStmt) handleChronosphereBatching(requestData map[string]any, chronosphereParams map[string]any, dataSourceType string) (driver.Rows, error) {
	const (
		maxBatches       = 12               // Maximum number of batches to create
		minBatchDuration = 1 * time.Minute  // Minimum duration per batch
		maxBatchDuration = 15 * time.Minute // Maximum duration per batch
		maxTotalDuration = 2 * time.Hour    // Maximum we support
		maxTraceIDs      = 10               // Chronosphere trace ID limitation
	)

	slog.Debug("Chronosphere batching check initiated")

	// Check for trace ID batching first
	if traceIds, hasTraceIds := chronosphereParams["trace_ids"]; hasTraceIds {
		slog.Info("Starting trace ID batching",
			"trace_ids_type", fmt.Sprintf("%T", traceIds),
			"max_trace_ids", maxTraceIDs)
		return s.handleTraceIdBatching(requestData, chronosphereParams, dataSourceType, traceIds, maxTraceIDs)
	}

	// Extract start and end times for time-based batching
	startTimeStr, hasStart := chronosphereParams["start_time"].(string)
	endTimeStr, hasEnd := chronosphereParams["end_time"].(string)

	slog.Debug("Time-based batching check")

	if !hasStart || !hasEnd {
		slog.Info("No time range specified, skipping batching")
		return nil, nil
	}

	startTime, err := time.Parse(time.RFC3339, startTimeStr)
	if err != nil {
		slog.Error("Failed to parse start_time",
			"start_time_str", startTimeStr,
			"error", err)
		return nil, fmt.Errorf("invalid start_time format: %v", err)
	}

	endTime, err := time.Parse(time.RFC3339, endTimeStr)
	if err != nil {
		slog.Error("Failed to parse end_time",
			"end_time_str", endTimeStr,
			"error", err)
		return nil, fmt.Errorf("invalid end_time format: %v", err)
	}

	totalDuration := endTime.Sub(startTime)

	slog.Debug("Parsed time range", "duration_hours", totalDuration.Hours())

	// Check if duration exceeds maximum supported
	if totalDuration > maxTotalDuration {
		slog.Warn("Requested duration exceeds maximum supported, capping duration",
			"original_duration", totalDuration.Minutes(),
			"capped_duration", maxTotalDuration.Minutes())
		startTime = endTime.Add(-maxTotalDuration)
		totalDuration = maxTotalDuration
	}

	// Calculate dynamic batch size
	// Try to create optimal number of batches, but ensure each batch is at least minBatchDuration
	idealBatchDuration := totalDuration / time.Duration(maxBatches)

	var actualBatchDuration time.Duration
	var numBatches int

	if idealBatchDuration < minBatchDuration {
		// If ideal batch would be too small, use minimum batch duration
		actualBatchDuration = minBatchDuration
		numBatches = int(totalDuration / minBatchDuration)
		if totalDuration%minBatchDuration != 0 {
			numBatches++ // Add one more batch for the remainder
		}
	} else {
		// Use ideal batch duration
		actualBatchDuration = idealBatchDuration
		numBatches = maxBatches
	}

	if numBatches <= 1 {
		return nil, nil
	}

	slog.Info("Batching Chronosphere query",
		"duration_minutes", totalDuration.Minutes(),
		"batches", numBatches,
		"batch_duration_minutes", actualBatchDuration.Minutes())

	allResponsesData, successfulChunks := s.executeChunksInParallel(requestData, chronosphereParams, startTime, endTime, actualBatchDuration, numBatches)

	if successfulChunks == 0 {
		return nil, fmt.Errorf("all %d Chronosphere batches failed", numBatches)
	}

	combinedResponse := combineChronosphereResponses(allResponsesData)

	if combinedTraces, ok := combinedResponse["traces"].([]any); ok {
		slog.Info("Batched query completed", "batches", successfulChunks, "traces", len(combinedTraces))
	}

	return NewAgentWarehouseRows(combinedResponse, "account_id", dataSourceType)
}

// handleTraceIdBatching handles batching when too many trace IDs are specified
func (s agentWarehouseStmt) handleTraceIdBatching(requestData map[string]any, chronosphereParams map[string]any, dataSourceType string, traceIds any, maxTraceIDs int) (driver.Rows, error) {
	var traceIdsList []string

	switch v := traceIds.(type) {
	case []any:
		for _, id := range v {
			if idStr, ok := id.(string); ok {
				traceIdsList = append(traceIdsList, idStr)
			}
		}
	case []string:
		traceIdsList = v
	default:
		return nil, nil
	}

	if len(traceIdsList) <= maxTraceIDs {
		return nil, nil
	}

	// Batching required for trace IDs
	numChunks := (len(traceIdsList) + maxTraceIDs - 1) / maxTraceIDs // Ceiling division

	slog.Info("Starting batched Chronosphere trace ID queries",
		"total_trace_ids", len(traceIdsList),
		"max_per_batch", maxTraceIDs,
		"calculated_chunks", numChunks)

	allResponsesData := make([]map[string]any, 0)
	successfulChunks := 0

	for i := 0; i < numChunks; i++ {
		start := i * maxTraceIDs
		end := start + maxTraceIDs
		if end > len(traceIdsList) {
			end = len(traceIdsList)
		}

		chunkTraceIds := traceIdsList[start:end]

		slog.Info("Processing Chronosphere trace ID chunk",
			"chunk", i+1,
			"total_chunks", numChunks,
			"trace_ids_in_chunk", len(chunkTraceIds),
			"start_index", start,
			"end_index", end)

		// Create parameters for this chunk
		chunkParams := make(map[string]any)
		for k, v := range chronosphereParams {
			chunkParams[k] = v
		}
		chunkParams["trace_ids"] = chunkTraceIds

		slog.Debug("Trace ID chunk parameters",
			"chunk", i+1,
			"chunk_trace_ids", chunkTraceIds)

		// Execute query for this chunk
		chunkResponseData, err := s.executeChronosphereChunk(requestData, chunkParams)
		if err != nil {
			slog.Error("Chronosphere trace ID chunk query failed",
				"chunk", i+1,
				"total_chunks", numChunks,
				"trace_ids_in_chunk", len(chunkTraceIds),
				"error", err)
			continue
		}

		// Log chunk response data stats
		if chunkResponseData != nil {
			if traces, ok := chunkResponseData["traces"].([]any); ok {
				slog.Info("Trace ID chunk query successful",
					"chunk", i+1,
					"traces_count", len(traces),
					"trace_ids_queried", len(chunkTraceIds))
			} else {
				slog.Warn("Trace ID chunk response missing traces data",
					"chunk", i+1,
					"response_keys", getMapKeys(chunkResponseData))
			}
		} else {
			slog.Warn("Trace ID chunk returned nil response data",
				"chunk", i+1)
		}

		allResponsesData = append(allResponsesData, chunkResponseData)
		successfulChunks++
	}

	if successfulChunks == 0 {
		slog.Error("All trace ID chunks failed",
			"total_chunks", numChunks,
			"total_trace_ids", len(traceIdsList))
		return nil, fmt.Errorf("all %d trace ID chunks failed", numChunks)
	}

	// Combine all chunk responses into a single response
	slog.Info("Starting trace ID response combination",
		"response_chunks_to_combine", len(allResponsesData))

	combinedResponse := combineChronosphereResponses(allResponsesData)

	// Log final combined result stats
	if combinedTraces, ok := combinedResponse["traces"].([]any); ok {
		slog.Info("Chronosphere trace ID batched query completed successfully",
			"total_chunks", numChunks,
			"successful_chunks", successfulChunks,
			"total_trace_ids_queried", len(traceIdsList),
			"combined_traces_count", len(combinedTraces))
	} else {
		slog.Warn("Combined trace ID response missing traces data",
			"total_chunks", numChunks,
			"successful_chunks", successfulChunks,
			"combined_response_keys", getMapKeys(combinedResponse))
	}

	return NewAgentWarehouseRows(combinedResponse, "account_id", dataSourceType)
}

// combineChronosphereResponses combines multiple Chronosphere response chunks into one
func combineChronosphereResponses(responses []map[string]any) map[string]any {
	slog.Debug("Combining Chronosphere responses",
		"response_count", len(responses))

	if len(responses) == 0 {
		slog.Warn("No responses to combine, returning empty traces")
		return map[string]any{"traces": []any{}}
	}

	if len(responses) == 1 {
		slog.Debug("Single response, returning as-is")
		if traces, ok := responses[0]["traces"].([]any); ok {
			slog.Debug("Single response traces count", "traces", len(traces))
		}
		return responses[0]
	}

	// Combine all traces from all responses
	allTraces := make([]any, 0)
	totalTracesBeforeCombine := 0

	for i, response := range responses {
		if response == nil {
			slog.Warn("Nil response in combination", "response_index", i)
			continue
		}

		if traces, ok := response["traces"].([]any); ok {
			tracesCount := len(traces)
			allTraces = append(allTraces, traces...)
			totalTracesBeforeCombine += tracesCount
			slog.Debug("Combined traces from response",
				"response_index", i,
				"traces_in_response", tracesCount,
				"total_traces_so_far", len(allTraces))
		} else {
			slog.Warn("Response missing traces data",
				"response_index", i,
				"response_keys", getMapKeys(response))
		}
	}

	slog.Info("Response combination completed",
		"input_responses", len(responses),
		"total_traces_before_combine", totalTracesBeforeCombine,
		"final_traces_count", len(allTraces))

	// Return combined response in the same format as single response
	return map[string]any{
		"traces": allTraces,
	}
}

// executeChronosphereChunk executes a single Chronosphere query chunk with pagination support
func (s agentWarehouseStmt) executeChronosphereChunk(requestData map[string]any, chunkParams map[string]any) (map[string]any, error) {
	// Create request data for this chunk
	chunkRequestData := make(map[string]any)
	for k, v := range requestData {
		chunkRequestData[k] = v
	}

	// Deep copy the body map to avoid race conditions in parallel execution
	originalBody := chunkRequestData["body"].(map[string]any)
	body := make(map[string]any)
	for k, v := range originalBody {
		body[k] = v
	}
	chunkRequestData["body"] = body

	body["action_name"] = "chronosphere_query_traces"

	mappedParams := s.mapToChronosphereAPI(chunkParams)
	body["action_params"] = mappedParams

	// Use the same retry helper function
	response, err := s.executeChronosphereRequestWithRetry(chunkRequestData, "chunk_query")
	if err != nil {
		return nil, fmt.Errorf("chunk query failed: %v", err)
	}

	finalData := response.ResponseDataMap

	var nextToken string
	possibleTokenFields := []string{"next_token", "nextToken", "page_token", "pageToken", "continuation_token"}
	for _, field := range possibleTokenFields {
		if token, exists := finalData[field].(string); exists && token != "" {
			nextToken = token
			break
		}
	}

	if nextToken != "" {
		slog.Warn("Pagination detected - data may be incomplete")
	}

	return finalData, nil
}

// normalizeTraceIDResponse converts Base64 encoded IDs from Chronosphere to hex format
// Per OpenTelemetry spec:
// - trace_id: 16-byte array (32 hex chars)
// - span_id: 8-byte array (16 hex chars)
// - parent_span_id: 8-byte array (16 hex chars)
func normalizeTraceIDResponse(id string) string {
	if id == "" {
		return id
	}

	// Assume all IDs from Chronosphere are Base64 encoded and convert to hex
	if decoded, err := base64.StdEncoding.DecodeString(id); err == nil {
		return hex.EncodeToString(decoded)
	}

	// If Base64 decoding fails, return as-is
	return id
}
