package observability

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strconv"
	"strings"
)

// SignozSaasSource is a LogSource implementation for Signoz SaaS.
type SignozSaasSource struct{}

type SignozJwtResponse struct {
	AccessJwt string `json:"accessJwt"`
}

const (
	SignozUrl      = "signoz_url"
	SignozUserName = "signoz_username"
	SignozPassword = "signoz_password"
)

func (s *SignozSaasSource) GetJwtToken(ctx *security.RequestContext, signozUrl, signozUsername, signozPassword string) (string, error) {
	jwtTokenObject := map[string]string{
		"email":    signozUsername,
		"password": signozPassword,
	}

	res, err := common.HttpPost(fmt.Sprintf("%s/api/v1/login", signozUrl), common.HttpWithJsonBody(jwtTokenObject))
	if err != nil {
		return "", fmt.Errorf("failed to post login request: %w", err)
	}
	defer func() {
		err := res.Body.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing response body", "error", err)
		}
	}()

	scanner := bufio.NewScanner(res.Body)
	var obj SignozJwtResponse
	for scanner.Scan() {
		if err := json.Unmarshal(scanner.Bytes(), &obj); err != nil {
			slog.Warn("signoz: JWT response parse error", "error", err)
			continue
		}
	}
	if obj.AccessJwt != "" {
		return obj.AccessJwt, nil
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to scan response body: %w", err)
	}

	return "", fmt.Errorf("empty JWT token received")
}
func (s *SignozSaasSource) GetLabelMapping() map[string]string {
	return map[string]string{}
}

func (s *SignozSaasSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_in", "_not_in", "_contains", "_like"}
}

func (s *SignozSaasSource) GetSignozAuth(ctx *security.RequestContext, accountId string) (string, string, error) {
	integrationDto, err := core.ListIntegrationConfigs(ctx, accountId, "signoz")
	if err != nil {
		return "", "", fmt.Errorf("failed to get signoz integration: %w", err)
	}
	if len(integrationDto) == 0 {
		return "", "", errors.New("no signoz integrations found")
	}

	integration := integrationDto[0]

	var signozUrl, signozUsername, signozPassword string
	for _, config := range integration.Configs {
		switch config.Name {
		case SignozUrl:
			signozUrl = config.Value
		case SignozUserName:
			signozUsername = config.Value
		case SignozPassword:
			signozPassword = config.Value
		}
	}

	if signozUrl == "" || signozUsername == "" || signozPassword == "" {
		return "", "", fmt.Errorf("missing required signoz configuration values")
	}

	jwtToken, err := s.GetJwtToken(ctx, signozUrl, signozUsername, signozPassword)
	if err != nil {
		return "", "", fmt.Errorf("failed to get jwt token: %w", err)
	}

	return signozUrl, jwtToken, nil
}

func (s *SignozSaasSource) QueryLogs(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) ([]OutputLog, error) {
	if fetchLogRequest.Query == "" {
		flattenedClauses, err := FlattenSignozQuery(fetchLogRequest.QueryRequest.Where)
		if err != nil {
			return nil, fmt.Errorf("failed to flatten query: %w", err)
		}
		queryJSON, err := json.Marshal(flattenedClauses)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal flattened query: %w", err)
		}
		fetchLogRequest.Query = string(queryJSON)
	}
	signozUrl, jwtToken, err := s.GetSignozAuth(ctx, fetchLogRequest.AccountId)
	if err != nil {
		return nil, err
	}

	var parsedQuery any
	if err := common.UnmarshalJson([]byte(fetchLogRequest.Query), &parsedQuery); err != nil {
		return nil, fmt.Errorf("failed to parse Query JSON for signoz: %w", err)
	}
	orderBy := []map[string]any{
		{"columnName": "timestamp", "order": "desc"},
	}
	if len(fetchLogRequest.SortFields) > 0 {
		orderBy = make([]map[string]any, len(fetchLogRequest.SortFields))
		for i, sf := range fetchLogRequest.SortFields {
			orderBy[i] = map[string]any{
				"columnName": sf.ColumnName,
				"order":      sf.Order,
			}
		}
	}
	limit := 1000
	if fetchLogRequest.Limit > 0 {
		limit = fetchLogRequest.Limit
	}
	steps := 60
	if fetchLogRequest.StepInterval > 0 {
		steps = fetchLogRequest.StepInterval
	}
	offset := 0
	if fetchLogRequest.Offset > 0 {
		offset = fetchLogRequest.Offset
	}
	var op = "AND"
	if fetchLogRequest.Request != nil {
		if val, ok := fetchLogRequest.Request["op"]; ok {
			if opStr, ok := val.(string); ok && opStr != "" {
				op = opStr
			}
		}
	}
	signozLogRequest := map[string]any{
		"start":     fetchLogRequest.StartTime,
		"end":       fetchLogRequest.EndTime,
		"step":      60,
		"variables": map[string]any{},
		"compositeQuery": map[string]any{
			"queryType": "builder",
			"panelType": "list",
			"fillGaps":  false,
			"builderQueries": map[string]any{
				"A": map[string]any{
					"dataSource":         "logs",
					"queryName":          "A",
					"aggregateOperator":  "noop",
					"aggregateAttribute": map[string]any{},
					"timeAggregation":    "rate",
					"spaceAggregation":   "sum",
					"functions":          []any{},
					"filters": map[string]any{
						"op":    op,
						"items": parsedQuery,
					},
					"expression":   "A",
					"disabled":     false,
					"stepInterval": steps,
					"having":       []any{},
					"limit":        nil,
					"orderBy":      orderBy,
					"groupBy":      []any{},
					"legend":       "",
					"reduceTo":     "avg",
					"offset":       offset,
					"pageSize":     limit,
				},
			},
		},
	}

	resp, err := common.HttpPost(fmt.Sprintf("%s/api/v4/query_range", signozUrl), common.HttpWithHeaders(map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", jwtToken),
		"Content-Type":  "application/json",
	}), common.HttpWithJsonBody(signozLogRequest))
	if err != nil {
		return nil, fmt.Errorf("failed to execute signoz log labels: %w", err)
	}
	jsonResponseBody := resp.Body
	defer func() {
		err := jsonResponseBody.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing response body", "error", err)
		}
	}()

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("Failed to get signoz labels", "resp", resp)
		return nil, fmt.Errorf("failed to get signoz labels, status code: %d", resp.StatusCode)
	}
	bodyBytes, err := io.ReadAll(jsonResponseBody)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	var data3 map[string]any
	if err := common.UnmarshalJson(bodyBytes, &data3); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response body: %w", err)
	}

	dataSection, ok := data3["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing or invalid 'data' field in response")
	}

	result, ok := dataSection["result"].([]any)
	if !ok || result == nil {
		return nil, fmt.Errorf("'result' field is not an array or is nil from response")
	}
	if len(result) == 0 {
		return []OutputLog{}, nil
	}
	firstMap, ok := result[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("first element is not an object")
	}
	list, ok := firstMap["list"].([]any)
	if !ok || list == nil {
		return []OutputLog{}, nil
	}

	data, err := common.MarshalJson(list)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal json: %w", err)
	}
	var signozLogResp []SigNozSaaSLog
	if err := json.Unmarshal([]byte(data), &signozLogResp); err != nil {
		ctx.GetLogger().Error("Error unmarshaling Signoz logs response:", "err", err)
		return nil, fmt.Errorf("failed to marshal json: %w", err)
	}
	outputLog := s.convertSigNozLogs(signozLogResp)
	return outputLog, nil
}

func (s *SignozSaasSource) QueryLabels(ctx *security.RequestContext, fetchLogLabelRequest FetchLogLabelRequest) ([]OutputLogLabel, error) {
	signozUrl, jwtToken, err := s.GetSignozAuth(ctx, fetchLogLabelRequest.AccountId)
	if err != nil {
		return nil, err
	}

	params := fmt.Sprintf("searchText=%s&dataSource=%s&existingFilter=%s&aggregateOperator=noop", "", "logs", "")
	resp, err := common.HttpGet(fmt.Sprintf("%s/api/v3/autocomplete/attribute_keys?%s", signozUrl, params), common.HttpWithHeaders(map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", jwtToken),
		"Content-Type":  "application/json",
	}))
	if err != nil {
		return nil, fmt.Errorf("failed to execute signoz log labels: %w", err)
	}
	jsonResponseBody := resp.Body
	defer func() {
		err := jsonResponseBody.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing response body", "error", err)
		}
	}()

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("Failed to get signoz labels", "resp", resp)
		return nil, fmt.Errorf("failed to get signoz labels, status code: %d", resp.StatusCode)
	}
	bodyBytes, err := io.ReadAll(jsonResponseBody)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	var data3 map[string]any
	if err := common.UnmarshalJson(bodyBytes, &data3); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response body: %w", err)
	}
	dataMap, ok := data3["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("logs.FetchLogLabels: data3[\"data\"] is not a map")
	}

	attrKeysAny, ok := dataMap["attributeKeys"]
	if !ok {
		return nil, fmt.Errorf("logs.FetchLogLabels: attributeKeys field missing")
	}

	result, ok := attrKeysAny.([]any)
	if !ok || result == nil {
		return nil, fmt.Errorf("logs.FetchLogLabels: attributeKeys field is not an array or is nil")
	}

	if len(result) == 0 {
		return []OutputLogLabel{}, nil
	}

	data, err := common.MarshalJson(result)
	if err != nil {
		return nil, fmt.Errorf("logs.FetchLogLabels failed to marshal json: %w", err)
	}
	logs, err := s.convertSigNozLogLabels(string(data))
	if err != nil {
		return nil, fmt.Errorf("logs.FetchLogLabels failed to convert Signoz log label to nudgebee json: %w", err)
	}

	return logs, nil
}

func (s *SignozSaasSource) QueryLabelValues(ctx *security.RequestContext, fetchLogLabelValueRequest FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	signozUrl, jwtToken, err := s.GetSignozAuth(ctx, fetchLogLabelValueRequest.AccountId)
	if err != nil {
		return nil, err
	}
	filterAttributeKeyDataType := "string"
	if v, ok := fetchLogLabelValueRequest.Request["filterAttributeKeyDataType"].(string); ok && v != "" {
		filterAttributeKeyDataType = v
	}

	tagType := "resource"
	if v, ok := fetchLogLabelValueRequest.Request["tagType"].(string); ok && v != "" {
		tagType = v
	}

	params := fmt.Sprintf(
		"searchText=%s&dataSource=%s&filterAttributeKeyDataType=%s&aggregateOperator=noop&aggregateAttribute=%s&tagType=%s&attributeKey=%s",
		"",
		"logs",
		filterAttributeKeyDataType,
		"",
		tagType,
		fetchLogLabelValueRequest.LabelName,
	)
	resp, err := common.HttpGet(fmt.Sprintf("%s/api/v3/autocomplete/attribute_values?%s", signozUrl, params), common.HttpWithHeaders(map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", jwtToken),
		"Content-Type":  "application/json",
	}))
	if err != nil {
		return nil, fmt.Errorf("failed to execute signoz log labels: %w", err)
	}
	jsonResponseBody := resp.Body
	defer func() {
		err := jsonResponseBody.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing response body", "error", err)
		}
	}()

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("Failed to get signoz labels", "resp", resp)
		return nil, fmt.Errorf("failed to get signoz labels, status code: %d", resp.StatusCode)
	}
	bodyBytes, err := io.ReadAll(jsonResponseBody)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	var data3 map[string]any
	if err := common.UnmarshalJson(bodyBytes, &data3); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response body: %w", err)
	}
	var result []any
	var ok1 bool

	dataSection, ok := data3["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("logs.FetchLogLabelValues: missing 'data' field in response")
	}

	switch fetchLogLabelValueRequest.Request["filterAttributeKeyDataType"] {
	case "string":
		if v, ok := dataSection["stringAttributeValues"].(map[string]any); ok {
			result, ok1 = v["data"].([]any)
		} else if v, ok := dataSection["stringAttributeValues"].([]any); ok {
			result, ok1 = v, true
		}
	case "number":
		if v, ok := dataSection["numberAttributeValues"].(map[string]any); ok {
			result, ok1 = v["data"].([]any)
		} else if v, ok := dataSection["numberAttributeValues"].([]any); ok {
			result, ok1 = v, true
		}
	case "bool":
		if v, ok := dataSection["boolAttributeValues"].(map[string]any); ok {
			result, ok1 = v["data"].([]any)
		} else if v, ok := dataSection["boolAttributeValues"].([]any); ok {
			result, ok1 = v, true
		}
	default:
		return nil, fmt.Errorf("logs.FetchLogLabelValues unknown data type: %v",
			fetchLogLabelValueRequest.Request["filterAttributeKeyDataType"])
	}

	if !ok1 || result == nil {
		return []OutputLogLabelValue{}, nil
	}
	if len(result) == 0 {
		return []OutputLogLabelValue{}, nil
	}

	data, err := common.MarshalJson(result)
	if err != nil {
		return nil, fmt.Errorf("logs.FetchLogLabelValues failed to marshal json: %w", err)
	}
	logs, err := ConvertSigNozLogLabelValues(data)
	if err != nil {
		return nil, fmt.Errorf("logs.FetchLogLabelValues failed to convert Signoz log label value to nudgebee json: %w", err)
	}

	return logs, nil
}

// SigNoz log structure
type SigNozSaaSLog struct {
	Timestamp string `json:"timestamp"`
	Data      struct {
		AttributesBool    map[string]bool    `json:"attributes_bool"`
		AttributesFloat64 map[string]float64 `json:"attributes_float64"`
		AttributesInt64   map[string]int64   `json:"attributes_int64"`
		AttributesString  map[string]string  `json:"attributes_string"`
		Body              string             `json:"body"`
		ID                string             `json:"id"`
		ResourcesString   map[string]string  `json:"resources_string"`
		SeverityNumber    int                `json:"severity_number"`
		SeverityText      string             `json:"severity_text"`
		SpanID            string             `json:"span_id"`
		TraceFlags        int                `json:"trace_flags"`
		TraceID           string             `json:"trace_id"`
	} `json:"data"`
}

// Map SigNoz severity to standard severity levels
func (s *SignozSaasSource) mapSeverity(severityText string, severityNumber int) string {
	switch strings.ToLower(severityText) {
	case "trace", "debug":
		return "debug"
	case "information", "info":
		return "info"
	case "warning", "warn":
		return "warn"
	case "error":
		return "error"
	case "fatal", "critical":
		return "fatal"
	default:
		// Fallback to severity number mapping
		switch {
		case severityNumber <= 8: // TRACE and DEBUG
			return "debug"
		case severityNumber <= 12: // INFO
			return "info"
		case severityNumber <= 16: // WARN
			return "warn"
		case severityNumber <= 20: // ERROR
			return "error"
		default: // FATAL and above
			return "fatal"
		}
	}
}

// Convert SigNoz logs to output format
func (s *SignozSaasSource) convertSigNozLogs(sigNozLogs []SigNozSaaSLog) []OutputLog {
	var outputLogs []OutputLog

	for _, sigNozLog := range sigNozLogs {
		// Create streams_tags from various attributes
		streamsTags := make(map[string]string)

		// Add string attributes to streams_tags
		for key, value := range sigNozLog.Data.AttributesString {
			streamsTags[key] = value
		}
		for key, value := range sigNozLog.Data.AttributesBool {
			streamsTags[key] = strconv.FormatBool(value)
		}
		for key, value := range sigNozLog.Data.AttributesFloat64 {
			streamsTags[key] = strconv.FormatFloat(value, 'f', -1, 64)
		}
		for key, value := range sigNozLog.Data.AttributesInt64 {
			streamsTags[key] = strconv.FormatInt(value, 10)
		}

		// Add resource strings to streams_tags (optional, you can customize this)
		for key, value := range sigNozLog.Data.ResourcesString {
			streamsTags[key] = value
		}

		// Add trace information to streams_tags
		if sigNozLog.Data.TraceID != "" {
			streamsTags["trace_id"] = sigNozLog.Data.TraceID
		}
		if sigNozLog.Data.SpanID != "" {
			streamsTags["span_id"] = sigNozLog.Data.SpanID
		}

		labels := make(map[string]interface{})
		for k, v := range streamsTags {
			labels[k] = v
		}

		outputLog := OutputLog{
			Timestamp: sigNozLog.Timestamp,
			Message:   sigNozLog.Data.Body,
			Labels:    labels,
			Severity:  s.mapSeverity(sigNozLog.Data.SeverityText, sigNozLog.Data.SeverityNumber),
		}

		outputLogs = append(outputLogs, outputLog)
	}

	return outputLogs
}

// Transform function
func (s *SignozSaasSource) convertSigNozLogLabels(inputJSON string) ([]OutputLogLabel, error) {
	var inputs []Input
	if err := json.Unmarshal([]byte(inputJSON), &inputs); err != nil {
		return nil, err
	}
	var outputs []OutputLogLabel
	for _, in := range inputs {
		out := OutputLogLabel{
			Label: in.Key,
			Attributes: map[string]interface{}{
				"dataType": in.DataType,
				"type":     in.Type,
				"isColumn": in.IsColumn,
				"isJSON":   in.IsJSON,
			},
		}
		outputs = append(outputs, out)
	}
	return outputs, nil
}

func (s *SignozSaasSource) GetQuery(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) (string, error) {
	return "", nil
}

// QueryLogGroup implements LogGroupSource for Signoz SaaS.
// Uses the Signoz /api/v4/query_range endpoint with count aggregation and groupBy.
func (s *SignozSaasSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	signozUrl, jwtToken, err := s.GetSignozAuth(ctx, req.AccountId)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("signoz_saas.QueryLogGroup: auth failed: %w", err)
	}

	filters := buildSignozSaasLogGroupFilters(req)

	queryBody := map[string]any{
		"start":     req.StartTime,
		"end":       req.EndTime,
		"step":      3600,
		"variables": map[string]any{},
		"compositeQuery": map[string]any{
			"queryType": "builder",
			"panelType": "graph",
			"fillGaps":  false,
			"builderQueries": map[string]any{
				"A": map[string]any{
					"dataSource":         "logs",
					"queryName":          "A",
					"aggregateOperator":  "count",
					"aggregateAttribute": map[string]any{},
					"timeAggregation":    "rate",
					"spaceAggregation":   "sum",
					"functions":          []any{},
					"filters": map[string]any{
						"op":    "AND",
						"items": filters,
					},
					"expression":   "A",
					"disabled":     false,
					"stepInterval": 3600,
					"having":       []any{},
					"limit":        100,
					"orderBy": []map[string]any{
						{"columnName": "#SIGNOZ_VALUE", "order": "desc"},
					},
					"groupBy": []map[string]any{
						{"key": "severity_text", "dataType": "string", "type": "tag", "isColumn": false},
						{"key": "k8s_namespace_name", "dataType": "string", "type": "resource", "isColumn": false},
						{"key": "k8s_pod_name", "dataType": "string", "type": "resource", "isColumn": false},
					},
					"legend":   "",
					"reduceTo": "avg",
					"offset":   0,
					"pageSize": 100,
				},
			},
		},
	}

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": fmt.Sprintf("Bearer %s", jwtToken),
	}

	apiURL := fmt.Sprintf("%s/api/v4/query_range", signozUrl)
	resp, err := common.HttpPost(apiURL, common.HttpWithJsonBody(queryBody), common.HttpWithHeaders(headers))
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("signoz_saas.QueryLogGroup: HTTP request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return LogGroupOutput{}, fmt.Errorf("signoz_saas.QueryLogGroup: request failed with status %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("signoz_saas.QueryLogGroup: failed to read response: %w", err)
	}

	var respData map[string]any
	if err := json.Unmarshal(bodyBytes, &respData); err != nil {
		return LogGroupOutput{}, fmt.Errorf("signoz_saas.QueryLogGroup: failed to parse response: %w", err)
	}

	return parseSignozSaasLogGroupResponse(respData, req.EndTime)
}

func buildSignozSaasLogGroupFilters(req FetchLogGroupRequest) []map[string]any {
	filters := []map[string]any{
		{
			"key":   map[string]string{"key": "severity_text"},
			"value": []string{"ERROR", "CRITICAL", "FATAL", "error", "critical", "fatal"},
			"op":    "in",
		},
	}

	selectedNamespace := common.GetString(req.Request, "selectedNamespace")
	selectedWorkload := common.GetString(req.Request, "selectedWorkload")

	if selectedNamespace != "" {
		filters = append(filters, map[string]any{
			"key":   map[string]string{"key": "k8s_namespace_name"},
			"value": selectedNamespace,
			"op":    "=",
		})
	}
	if selectedWorkload != "" {
		filters = append(filters, map[string]any{
			"key":   map[string]string{"key": "k8s_pod_name"},
			"value": selectedWorkload,
			"op":    "contains",
		})
	}

	return filters
}

func parseSignozSaasLogGroupResponse(respData map[string]any, endTime int64) (LogGroupOutput, error) {
	data, ok := respData["data"].(map[string]any)
	if !ok {
		return LogGroupOutput{}, nil
	}
	result, ok := data["result"].([]any)
	if !ok || len(result) == 0 {
		return LogGroupOutput{}, nil
	}

	firstMap, ok := result[0].(map[string]any)
	if !ok {
		return LogGroupOutput{}, nil
	}

	series, ok := firstMap["series"].([]any)
	if !ok || series == nil {
		return LogGroupOutput{}, nil
	}

	groups := make([]LogGroup, 0, len(series))
	for _, sr := range series {
		seriesMap, ok := sr.(map[string]any)
		if !ok {
			continue
		}

		labels, _ := seriesMap["labels"].(map[string]any)
		labelMap := make(map[string]string)
		for k, v := range labels {
			if str, ok := v.(string); ok {
				labelMap[k] = str
			}
		}

		group := LogGroup{}

		if ns := labelMap["k8s_namespace_name"]; ns != "" {
			group.Namespace = ns
		}
		if pod := labelMap["k8s_pod_name"]; pod != "" {
			group.Workload = extractWorkloadFromPodName(pod)
			group.Sample = pod // Use pod name as sample since Signoz groups by pod, not message
		}
		if level := labelMap["severity_text"]; level != "" {
			group.Level = level
		}
		if group.Namespace != "" && group.Workload != "" {
			group.ContainerID = fmt.Sprintf("/k8s/%s/%s", group.Namespace, group.Workload)
		}

		// Generate pattern_hash from workload name
		if group.Workload != "" {
			group.PatternHash = generatePatternHash(group.Workload)
		}

		points, _ := seriesMap["points"].([]any)
		timestamps := make([]int64, 0, len(points))
		values := make([]float64, 0, len(points))
		for _, p := range points {
			point, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if ts, ok := point["timestamp"].(float64); ok {
				timestamps = append(timestamps, int64(ts))
			}
			if val, ok := point["value"].(float64); ok {
				values = append(values, val)
			}
		}

		group.Timestamps = timestamps
		group.Values = values
		var total int64
		for _, v := range values {
			total += int64(math.Round(v))
		}
		group.Count = total

		groups = append(groups, group)
	}

	return LogGroupOutput{Groups: groups}, nil
}
