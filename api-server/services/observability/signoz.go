package observability

import (
	"encoding/json"
	"fmt"
	"math"
	"nudgebee/services/common"
	"nudgebee/services/query"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"strconv"
	"strings"
)

// SignozSource is a LogSource implementation for Signoz.
type SignozSource struct{}

func (s *SignozSource) GetLabelMapping() map[string]string {
	return map[string]string{}
}

func (s *SignozSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_in", "_not_in", "_contains", "_like"}
}

const (
	Nq          query.BinaryWhereClauseType = "_neq"
	Eq          query.BinaryWhereClauseType = "_eq"
	Lt          query.BinaryWhereClauseType = "_lt"
	Gt          query.BinaryWhereClauseType = "_gt"
	Lte         query.BinaryWhereClauseType = "_lte"
	Gte         query.BinaryWhereClauseType = "_gte"
	In          query.BinaryWhereClauseType = "_in"
	NotIn       query.BinaryWhereClauseType = "_not_in"
	Like        query.BinaryWhereClauseType = "_like"
	Between     query.BinaryWhereClauseType = "_between"
	Contains    query.BinaryWhereClauseType = "_contains"
	NotContains query.BinaryWhereClauseType = "_not_contains"
	Exist       query.BinaryWhereClauseType = "_exist"
	NotExist    query.BinaryWhereClauseType = "_not_exist"
	ILike       query.BinaryWhereClauseType = "_ilike"
	HasKey      query.BinaryWhereClauseType = "_has_key"
	IsNull      query.BinaryWhereClauseType = "_is_null"
	RegEx       query.BinaryWhereClauseType = "_regex"
	NotRegEx    query.BinaryWhereClauseType = "_not_regex"
	NqF         query.BinaryWhereClauseType = "_neq_f"
	EqF         query.BinaryWhereClauseType = "_eq_f"
	LikeF       query.BinaryWhereClauseType = "_like_f"
	ILikeF      query.BinaryWhereClauseType = "_ilike_f"
	NLike       query.BinaryWhereClauseType = "_nlike"
)

type FlattenedClause struct {
	Key   map[string]string `json:"key"`
	Value any               `json:"value"`
	Op    string            `json:"op"`
}

func FlattenSignozQuery(q query.QueryWhereClause) ([]FlattenedClause, error) {
	var result []FlattenedClause

	// Handle binary clause
	for field, conds := range q.Binary {
		for op, value := range conds {
			var signozOp string
			switch op {
			case Eq, EqF:
				signozOp = "="
			case Nq, NqF:
				signozOp = "!="
			case In:
				signozOp = "in"
				if _, ok := value.([]any); !ok {
					return nil, fmt.Errorf("value for 'in' operator must be an array")
				}
			case NotIn:
				signozOp = "nin"
				if _, ok := value.([]any); !ok {
					return nil, fmt.Errorf("value for 'nin' operator must be an array")
				}
			case Like:
				signozOp = "like"
			case NLike:
				signozOp = "nlike"
			case Contains:
				signozOp = "contains"
			case NotContains:
				signozOp = "ncontains"
			case Exist:
				signozOp = "exists"
			case NotExist:
				signozOp = "nexists"
			case RegEx:
				signozOp = "regex"
			case NotRegEx:
				signozOp = "nregex"

			default:
				return nil, fmt.Errorf("unsupported operator: %s", op)
			}
			result = append(result, FlattenedClause{
				Key:   map[string]string{"key": field},
				Value: value,
				Op:    signozOp,
			})
		}
	}
	// Handle AND
	for _, sub := range q.And {
		subResult, err := FlattenSignozQuery(sub)
		if err != nil {
			return nil, err
		}
		result = append(result, subResult...)
	}

	// Handle OR
	for range q.Or {
		return nil, fmt.Errorf("OR operator is not supported in Signoz queries")
		// subResult, err := FlattenSignozQuery(sub)
		// if err != nil {
		// 	return nil, err
		// }
		// result = append(result, subResult...)
	}

	// Handle NOT
	if q.Not != nil {
		return nil, fmt.Errorf("NOT operator is not supported in Signoz queries")
		// subResult, err := FlattenSignozQuery(*q.Not)
		// if err != nil {
		// 	return nil, err
		// }
		// result = append(result, subResult...)
	}

	return result, nil
}

func (s *SignozSource) QueryLogs(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) ([]OutputLog, error) {
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
	signozLogRequest := relay.ActionExecuteBody{
		AccountID:  fetchLogRequest.AccountId,
		ActionName: "signoz_query_range",
		ActionParams: map[string]any{
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
		},
		NoSinks: true,
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    signozLogRequest,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute signoz query: %w", err)
	}

	data1, ok := resp["data"].(map[string]any)
	if !ok || data1 == nil {
		return nil, fmt.Errorf("data1 field not found or is nil from response")
	}
	data2, ok := data1["data"].(map[string]any)
	if !ok || data2 == nil {
		return nil, fmt.Errorf("data2 field not found or is nil from response")
	}
	if errMsg, exists := data2["error"]; exists {
		return nil, fmt.Errorf("API returned error: %v", errMsg)
	}
	data3, ok := data2["data"].(map[string]any)
	if !ok || data3 == nil {
		return nil, fmt.Errorf("data3 field not found or is nil from response")
	}
	result, ok := data3["result"].([]any)
	if !ok || result == nil {
		return nil, fmt.Errorf("result field is not an array or is nil from response")
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
	var signozLogResp []SigNozLog
	if err := json.Unmarshal([]byte(data), &signozLogResp); err != nil {
		ctx.GetLogger().Error("Error unmarshaling Signoz logs response:", "err", err)
		return nil, fmt.Errorf("failed to marshal json: %w", err)
	}
	outputLog := s.convertSigNozLogs(signozLogResp)
	return outputLog, nil
}

func (s *SignozSource) QueryLabels(ctx *security.RequestContext, fetchLogRequest FetchLogLabelRequest) ([]OutputLogLabel, error) {
	signozLogRequest := relay.ActionExecuteBody{
		AccountID:  fetchLogRequest.AccountId,
		ActionName: "signoz_label_suggest",
		ActionParams: map[string]any{
			"dataSource": "logs",
		},
		NoSinks: true,
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    signozLogRequest,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute signoz log labels: %w", err)
	}

	data1, ok := resp["data"].(map[string]any)
	if !ok || data1 == nil {
		ctx.GetLogger().Error("logs.FetchLogLabels data1 field not found or is nil from response", "data", data1)
		return []OutputLogLabel{}, nil
	}
	data2, ok := data1["data"].(map[string]any)
	if !ok || data2 == nil {
		ctx.GetLogger().Error("logs.FetchLogLabels data2 field not found or is nil from response", "data", data1)
		return []OutputLogLabel{}, nil
	}
	data3, ok := data2["data"].(map[string]any)
	if !ok || data3 == nil {
		ctx.GetLogger().Error("logs.FetchLogLabels data3 field not found or is nil from response", "data", data1)
		return []OutputLogLabel{}, nil
	}
	result, ok := data3["attributes"].([]any)
	if !ok || result == nil {
		ctx.GetLogger().Error("logs.FetchLogLabels attributes field is not an array or is nil from response", "data", data1)
		return []OutputLogLabel{}, nil
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

func (s *SignozSource) QueryLabelValues(ctx *security.RequestContext, fetchLogRequest FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	signozLogRequest := relay.ActionExecuteBody{
		AccountID:  fetchLogRequest.AccountId,
		ActionName: "signoz_value_suggest",
		ActionParams: map[string]any{
			"attributeKey":               fetchLogRequest.LabelName,
			"filterAttributeKeyDataType": fetchLogRequest.Request["filterAttributeKeyDataType"],
			"searchText":                 fetchLogRequest.Request["searchText"],
			"tagType":                    fetchLogRequest.Request["tagType"],
		},
		NoSinks: true,
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    signozLogRequest,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute signoz log labels value: %w", err)
	}

	data1, ok := resp["data"].(map[string]any)
	if !ok || data1 == nil {
		return nil, fmt.Errorf("logs.FetchLogLabelValues data1 field not found or is nil from response")
	}
	data2, ok := data1["data"].(map[string]any)
	if !ok || data2 == nil {
		return nil, fmt.Errorf("logs.FetchLogLabelValues data2 field not found or is nil from response")
	}
	data3, ok := data2["data"].(map[string]any)
	if !ok || data3 == nil {
		return nil, fmt.Errorf("logs.FetchLogLabelValues data3 field not found or is nil from response")
	}
	var result []any
	var ok1 bool

	switch fetchLogRequest.Request["filterAttributeKeyDataType"] {
	case "string":
		result, ok1 = data3["stringAttributeValues"].([]any)
	case "number":
		result, ok1 = data3["numberAttributeValues"].([]any)
	case "bool":
		result, ok1 = data3["boolAttributeValues"].([]any)
	default:
		return nil, fmt.Errorf("logs.FetchLogLabelValues unknown data type: %v", fetchLogRequest.Request["filterAttributeKeyDataType"])
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
type SigNozLog struct {
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
func (s *SignozSource) mapSeverity(severityText string, severityNumber int) string {
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
func (s *SignozSource) convertSigNozLogs(sigNozLogs []SigNozLog) []OutputLog {
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

// Input structure
type Input struct {
	Key      string `json:"key"`
	DataType string `json:"dataType"`
	Type     string `json:"type"`
	IsColumn bool   `json:"isColumn"`
	IsJSON   bool   `json:"isJSON"`
}

// Transform function
func (s *SignozSource) convertSigNozLogLabels(inputJSON string) ([]OutputLogLabel, error) {
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

func ConvertSigNozLogLabelValues(inputJSON []byte) ([]OutputLogLabelValue, error) {
	var values []string
	if err := json.Unmarshal(inputJSON, &values); err != nil {
		return nil, fmt.Errorf("failed to unmarshal input: %w", err)
	}

	var output []OutputLogLabelValue
	for _, l := range values {
		output = append(output, OutputLogLabelValue{
			Value:      l,
			Attributes: make(map[string]interface{}), // empty map for now
		})
	}

	return output, nil
}

func (s *SignozSource) GetQuery(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) (string, error) {
	return "", nil
}

// QueryLogGroup implements LogGroupSource for Signoz (agent).
// Uses the Signoz builder query with count aggregation and groupBy to group error logs.
func (s *SignozSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	filters := s.buildLogGroupFilters(req)

	signozRequest := relay.ActionExecuteBody{
		AccountID:  req.AccountId,
		ActionName: "signoz_query_range",
		ActionParams: map[string]any{
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
		},
		NoSinks: true,
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    signozRequest,
	})
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("signoz.QueryLogGroup: failed to execute query: %w", err)
	}

	return s.parseSignozLogGroupResponse(resp, req.EndTime)
}

// buildLogGroupFilters builds Signoz filter items for error/critical log grouping.
func (s *SignozSource) buildLogGroupFilters(req FetchLogGroupRequest) []map[string]any {
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

// parseSignozLogGroupResponse parses the Signoz query_range response for aggregation queries.
func (s *SignozSource) parseSignozLogGroupResponse(resp map[string]any, endTime int64) (LogGroupOutput, error) {
	data1, ok := resp["data"].(map[string]any)
	if !ok || data1 == nil {
		return LogGroupOutput{}, nil
	}
	data2, ok := data1["data"].(map[string]any)
	if !ok || data2 == nil {
		return LogGroupOutput{}, nil
	}
	if errMsg, exists := data2["error"]; exists {
		return LogGroupOutput{}, fmt.Errorf("signoz.QueryLogGroup: API error: %v", errMsg)
	}
	data3, ok := data2["data"].(map[string]any)
	if !ok || data3 == nil {
		return LogGroupOutput{}, nil
	}
	result, ok := data3["result"].([]any)
	if !ok || len(result) == 0 {
		return LogGroupOutput{}, nil
	}

	// For aggregation queries, result contains series with queryName, series data
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

		// Extract labels from the series
		labels, _ := seriesMap["labels"].(map[string]any)
		labelMap := make(map[string]string)
		for k, v := range labels {
			if str, ok := v.(string); ok {
				labelMap[k] = str
			}
		}

		group := LogGroup{}

		// Map Signoz label names to standard names
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

		// Extract values: Signoz returns {points: [{timestamp, value}, ...]}
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
