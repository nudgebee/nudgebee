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

type ElasticSource struct{}

type PPLResponse struct {
	Schema   []PPLSchema     `json:"schema"`
	DataRows [][]interface{} `json:"datarows"`
}

type PPLSchema struct {
	Name string `json:"name"`
	Type string `json:"type"` // e.g., "string", "long"
}

// GetLabelMapping implements [LogSource].
func (e *ElasticSource) GetLabelMapping() map[string]string {
	return map[string]string{}
}

func (e *ElasticSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_contains", "_in", "_not_in", "_like", "_nlike", "_gt", "_lt", "_is_null"}
}

// GetQuery implements [LogSource].
// Generates a query from the where clause. Defaults to DSL (Elasticsearch
// Query DSL JSON) since the Code tab in the UI uses DSL as the fallback
// query language. Callers can still opt into PPL by passing
// query_type: "ppl" explicitly.
func (e *ElasticSource) GetQuery(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) (string, error) {
	var requestedType string
	if fetchLogRequest.Request != nil {
		requestedType, _ = fetchLogRequest.Request["query_type"].(string)
	}
	if requestedType == "ppl" {
		return buildPPLFromWhere(fetchLogRequest.QueryRequest.Where)
	}
	return buildESQueryFromWhere(fetchLogRequest.QueryRequest.Where)
}

func (s *ElasticSource) ExtractIndexFieldValues(resp map[string]any) ([]string, error) {
	outer, ok := resp["data"].(map[string]any)
	if !ok || outer == nil {
		return nil, fmt.Errorf("outer 'data' field not found or not an object")
	}

	rawList, ok := outer["data"].([]any)
	if !ok {
		return nil, fmt.Errorf("inner 'data' field not found or not an array")
	}

	result := make([]string, 0, len(rawList))

	for i, v := range rawList {
		if v == nil {
			continue // skip null entry at index 0
		}
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("element at index %d is not a string", i)
		}
		result = append(result, s)
	}

	return result, nil
}

// resolveAggregatableESField returns a field name that is safe to use in
// aggregations against Elasticsearch. Text fields are not aggregatable by
// default (fielddata is disabled), but they typically have a `.keyword`
// multi-field that is. When the requested field is text and a `.keyword`
// subfield exists in the supplied field list, this returns
// `<fieldName>.keyword`; otherwise it returns the original name unchanged.
//
// The helper accepts both attribute shapes that the two ES integrations
// produce: the agent variant (`_field_caps`) emits per-type sub-objects
// like {"text": {...}}, while the SaaS variant (`_mapping`) emits a flat
// {"type": "text"} attribute.
func resolveAggregatableESField(fields []OutputLogLabelFields, fieldName string) string {
	if fieldName == "" || strings.HasSuffix(fieldName, ".keyword") {
		return fieldName
	}

	var (
		isText        bool
		hasKeywordSub bool
	)
	keywordName := fieldName + ".keyword"
	for _, f := range fields {
		switch f.Field {
		case fieldName:
			if isTextFieldAttributes(f.Attributes) {
				isText = true
			}
		case keywordName:
			hasKeywordSub = true
		}
		if isText && hasKeywordSub {
			break
		}
	}

	if isText && hasKeywordSub {
		return keywordName
	}
	return fieldName
}

// isTextFieldAttributes reports whether the attributes describe a text field
// across both supported shapes (see resolveAggregatableESField).
func isTextFieldAttributes(attrs map[string]any) bool {
	if _, ok := attrs["text"]; ok {
		return true
	}
	if t, ok := attrs["type"].(string); ok && t == "text" {
		return true
	}
	return false
}

// QueryLabelValues implements [LogSource].
func (e *ElasticSource) QueryLabelValues(ctx *security.RequestContext, fetchLogRequest FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	index := common.GetString(fetchLogRequest.Request, "index")
	fieldName := fetchLogRequest.LabelName
	if fields, err := e.QueryIndexFields(ctx, FetchLogLabelRequest{
		AccountId: fetchLogRequest.AccountId,
		Request:   map[string]any{"index": index},
	}); err == nil {
		fieldName = resolveAggregatableESField(fields, fetchLogRequest.LabelName)
	}

	relayRequest := relay.ActionExecuteBody{
		AccountID:  fetchLogRequest.AccountId,
		ActionName: "query_es_field_index_values",
		ActionParams: map[string]any{
			"index":      index,
			"field_name": fieldName,
		},
		NoSinks: true,
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    relayRequest,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute query label: %w", err)
	}

	data3, err := e.ExtractIndexFieldValues(resp)
	if err != nil {
		return nil, err
	}

	var output []OutputLogLabelValue
	for _, v := range data3 {
		if v != "" {
			output = append(output, OutputLogLabelValue{
				Value:      v,
				Attributes: map[string]interface{}{},
			})
		}
	}
	return output, nil
}

func (s *ElasticSource) ExtractIndexNamesAny(resp map[string]any) ([]any, error) {
	outer, ok := resp["data"].(map[string]any)
	if !ok || outer == nil {
		return nil, fmt.Errorf("outer 'data' field not found or not an object")
	}

	raw, ok := outer["data"].(string)
	if !ok || raw == "" {
		return nil, fmt.Errorf("inner 'data' field not found or not a string")
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, fmt.Errorf("failed to unmarshal inner 'data' JSON: %w", err)
	}

	result := make([]any, 0, len(decoded))
	for k := range decoded {
		result = append(result, k) // string stored as any
	}

	return result, nil
}

func extractRelayError(resp map[string]interface{}) error {
	data1, ok := resp["data"].(map[string]interface{})
	if !ok {
		return nil
	}

	data2, ok := data1["data"].(map[string]interface{})
	if !ok {
		return nil
	}

	success, _ := data2["success"].(bool)
	if success {
		return nil
	}

	errStr, ok := data2["error"].(string)
	if !ok {
		return fmt.Errorf("relay request failed but no error payload found")
	}

	return fmt.Errorf("%s", errStr)
}

// QueryLabels implements [LogSource].
func (e *ElasticSource) QueryLabels(ctx *security.RequestContext, fetchLogRequest FetchLogLabelRequest) ([]OutputLogLabel, error) {

	relayRequest := relay.ActionExecuteBody{
		AccountID:    fetchLogRequest.AccountId,
		ActionName:   "query_es_indices",
		ActionParams: map[string]any{},
		NoSinks:      true,
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    relayRequest,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute query label: %w", err)
	}
	if relayErr := extractRelayError(resp); relayErr != nil {
		return nil, relayErr
	}

	data3, err := e.ExtractIndexNamesAny(resp)
	if err != nil {
		return nil, err
	}

	var output []OutputLogLabel
	for _, v := range data3 {
		if str, ok := v.(string); ok {
			output = append(output, OutputLogLabel{
				Label:      str,
				Attributes: map[string]interface{}{},
			})
		}
	}
	return output, nil
}

func parseErrorResponse(m map[string]any) error {
	if dataOuter, ok := m["data"].(map[string]any); ok {
		if dataInner, ok := dataOuter["data"].(map[string]any); ok {
			if errStr, ok := dataInner["error"].(string); ok {
				return fmt.Errorf("log error: %s", errStr)
			}
		}
	}
	return fmt.Errorf("unknown error structure with status code %v", m["status_code"])
}

func (e *ElasticSource) ExtractRawResponseString(resp any) (string, error) {
	m, ok := resp.(map[string]any)
	if !ok {
		return "", fmt.Errorf("resp is not map[string]any")
	}
	statusCode := 200
	if codeVal, exists := m["status_code"]; exists {
		switch v := codeVal.(type) {
		case float64:
			statusCode = int(v)
		case int:
			statusCode = v
		case string:
			parsed, err := strconv.Atoi(v)
			if err != nil {
				return "", fmt.Errorf("invalid status_code string format: %v", v)
			}
			statusCode = parsed
		default:
			return "", fmt.Errorf("status_code has unexpected type: %T", v)
		}
	}

	if statusCode != 200 {
		return "", parseErrorResponse(m)
	}

	data1, ok := m["data"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("resp.data not map")
	}
	switch v := data1["data"].(type) {
	case string:
		return v, nil
	case map[string]any:
		if errStr, exists := v["error"].(string); exists {
			return "", fmt.Errorf("query failed: %s", errStr)
		}
		return "", fmt.Errorf("resp.data.data is a map but unknown structure")
	default:
		return "", fmt.Errorf("resp.data.data has unexpected type: %T", v)
	}
}

func ParseSourceMap(src map[string]any) (OutputLog, bool) {
	ts, ok := src["@timestamp"].(string)
	if !ok || strings.TrimSpace(ts) == "" {
		if tsVal, exists := src["@timestamp"]; exists {
			ts = fmt.Sprintf("%v", tsVal)
		} else {
			return OutputLog{}, false
		}
	}

	msg, ok := src["log"].(string)
	if !ok || strings.TrimSpace(msg) == "" {
		return OutputLog{}, false
	}

	stream, _ := src["stream"].(string)
	severity := "INFO"
	switch strings.ToLower(stream) {
	case "stderr":
		severity = "ERROR"
	case "stdout":
		severity = "INFO"
	}
	log := OutputLog{
		Timestamp: ts,
		Message:   msg,
		Severity:  severity,
		Labels:    make(map[string]any),
	}

	for k, v := range src {
		if k == "@timestamp" || k == "stream" || k == "log" {
			continue
		}
		if v != nil {
			log.Labels[k] = v
		}
	}

	return log, true
}

// QueryLogs implements [LogSource].
func (e *ElasticSource) QueryLogs(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) ([]OutputLog, error) {
	var queryType string
	if fetchLogRequest.Request != nil {
		queryType, _ = fetchLogRequest.Request["query_type"].(string)
	}
	if queryType == "" {
		queryType = "dsl"
	}

	var queryParam any
	var otherDSLFields map[string]interface{}

	switch queryType {
	case "dsl":
		var jsonMap map[string]interface{}
		if err := json.Unmarshal([]byte(fetchLogRequest.Query), &jsonMap); err != nil {
			return nil, fmt.Errorf("failed to unmarshal DSL query: %w", err)
		}
		// Extract query clause if present, otherwise use the whole map as the query
		if q, ok := jsonMap["query"]; ok {
			queryParam = q
			// Remove "query" key and reuse jsonMap for other DSL fields (sort, aggs, highlight, post_filter, etc.)
			delete(jsonMap, "query")
			otherDSLFields = jsonMap
		} else {
			queryParam = jsonMap
		}
	case "ppl":
		queryParam = fetchLogRequest.Query
	default:
		return nil, fmt.Errorf("unsupported query_type: %v", queryType)
	}
	actionParams := map[string]any{
		"query":      queryParam,
		"index":      fetchLogRequest.Request["index"],
		"query_type": queryType,
	}

	// Add other DSL fields (sort, aggs, highlight, etc.) as top-level fields in actionParams
	// Skip restricted keys to prevent parameter injection vulnerability
	restrictedKeys := map[string]bool{
		"query":      true,
		"index":      true,
		"query_type": true,
	}
	for k, v := range otherDSLFields {
		if !restrictedKeys[k] {
			actionParams[k] = v
		}
	}

	if fetchLogRequest.Limit > 0 {
		actionParams["size"] = fetchLogRequest.Limit
	}
	if queryType == "dsl" {
		actionParams["track_total_hits"] = true
	}
	relayRequest := relay.ActionExecuteBody{
		AccountID:    fetchLogRequest.AccountId,
		ActionName:   "query_es",
		ActionParams: actionParams,
		NoSinks:      true,
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    relayRequest,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute query label: %w", err)
	}

	rawJSON, err := e.ExtractRawResponseString(resp)
	if err != nil {
		return nil, err
	}

	var output []OutputLog

	if queryType == "ppl" {
		var pplResp PPLResponse
		if err := json.Unmarshal([]byte(rawJSON), &pplResp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal PPL response: %w", err)
		}

		output = make([]OutputLog, 0, len(pplResp.DataRows))
		colNames := make([]string, len(pplResp.Schema))
		for i, col := range pplResp.Schema {
			colNames[i] = col.Name
		}

		for _, row := range pplResp.DataRows {
			src := make(map[string]any)
			for i, val := range row {
				if i < len(colNames) {
					src[colNames[i]] = val
				}
			}

			if log, ok := ParseSourceMap(src); ok {
				output = append(output, log)
			}
		}

	} else {
		var searchResp SearchResponse
		if err := json.Unmarshal([]byte(rawJSON), &searchResp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal DSL response: %w", err)
		}

		output = make([]OutputLog, 0, len(searchResp.Hits.Hits))

		for _, hit := range searchResp.Hits.Hits {
			if log, ok := ParseSourceMap(hit.Source); ok {
				output = append(output, log)
			}
		}
	}

	return output, nil
}

func (s *ElasticSource) ExtractIndexFieldAndAttributes(resp map[string]any) ([]OutputLogLabelFields, error) {
	outer, ok := resp["data"].(map[string]any)
	if !ok || outer == nil {
		return nil, fmt.Errorf("outer 'data' field not found or not an object")
	}

	raw, ok := outer["data"].(string)
	if !ok || raw == "" {
		return nil, fmt.Errorf("inner 'data' field not found or not a string")
	}

	// Decode the inner JSON string
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, fmt.Errorf("failed to unmarshal inner 'data' JSON: %w", err)
	}

	// Extract "fields"
	fieldsRaw, ok := decoded["fields"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("'fields' not found or not an object in decoded JSON")
	}

	result := make([]OutputLogLabelFields, 0, len(fieldsRaw))

	for fieldName, attrAny := range fieldsRaw {
		attrMap, ok := attrAny.(map[string]any)
		if !ok {
			// skip malformed field entry instead of crashing
			continue
		}

		result = append(result, OutputLogLabelFields{
			Field:      fieldName,
			Attributes: attrMap,
		})
	}

	return result, nil
}

func (e *ElasticSource) QueryIndexFields(ctx *security.RequestContext, fetchLogRequest FetchLogLabelRequest) ([]OutputLogLabelFields, error) {
	relayRequest := relay.ActionExecuteBody{
		AccountID:  fetchLogRequest.AccountId,
		ActionName: "query_es_index_field",
		ActionParams: map[string]any{
			"index": fetchLogRequest.Request["index"],
		},
		NoSinks: true,
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    relayRequest,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute query label: %w", err)
	}

	data3, err := e.ExtractIndexFieldAndAttributes(resp)
	if err != nil {
		return nil, err
	}
	return data3, nil
}

// escapeESWildcard escapes Elasticsearch wildcard special characters in user input
// to prevent wildcard injection attacks that could cause expensive queries (DoS).
func escapeESWildcard(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `*`, `\*`)
	s = strings.ReplaceAll(s, `?`, `\?`)
	return s
}

// binaryToESClause converts a single binary where operation to an ES DSL clause.
// Returns the clause, whether it's a negation (must_not), and any error.
func binaryToESClause(field string, op query.BinaryWhereClauseType, val any) (clause map[string]any, negate bool, err error) {
	switch op {
	case query.Eq:
		return map[string]any{"term": map[string]any{field: val}}, false, nil
	case query.Nq:
		return map[string]any{"term": map[string]any{field: val}}, true, nil
	case query.Contains:
		valStr, ok := val.(string)
		if !ok {
			return nil, false, fmt.Errorf("_contains operator requires string value for field %q, got %T", field, val)
		}
		return map[string]any{
			"wildcard": map[string]any{field: "*" + escapeESWildcard(valStr) + "*"},
		}, false, nil
	case query.In:
		return map[string]any{"terms": map[string]any{field: val}}, false, nil
	case query.NotIn:
		return map[string]any{"terms": map[string]any{field: val}}, true, nil
	case query.Like:
		return map[string]any{"wildcard": map[string]any{field: map[string]any{"value": val}}}, false, nil
	case query.NLike:
		return map[string]any{"wildcard": map[string]any{field: map[string]any{"value": val}}}, true, nil
	case query.Gt:
		return map[string]any{"range": map[string]any{field: map[string]any{"gt": val}}}, false, nil
	case query.Lt:
		return map[string]any{"range": map[string]any{field: map[string]any{"lt": val}}}, false, nil
	case query.IsNull:
		boolVal, ok := val.(bool)
		if !ok {
			return nil, false, fmt.Errorf("_is_null operator requires boolean value for field %q, got %T", field, val)
		}
		if boolVal {
			// does not exist: negate the exists query
			return map[string]any{"exists": map[string]any{"field": field}}, true, nil
		}
		// exists
		return map[string]any{"exists": map[string]any{"field": field}}, false, nil
	default:
		return nil, false, fmt.Errorf("unsupported operator %q for field %q in ES query", op, field)
	}
}

// whereToBool recursively converts a QueryWhereClause into an ES bool query map,
// properly handling Binary, And, Or, and Not clauses.
func whereToBool(where query.QueryWhereClause) (map[string]any, error) {
	var filter, mustNot, should []any

	// Handle binary clauses at this level
	for field, ops := range where.Binary {
		for op, val := range ops {
			clause, negate, err := binaryToESClause(field, op, val)
			if err != nil {
				return nil, err
			}
			if negate {
				mustNot = append(mustNot, clause)
			} else {
				filter = append(filter, clause)
			}
		}
	}

	// Handle AND: each sub-clause becomes a filter (all must match)
	for _, andClause := range where.And {
		sub, err := whereToBool(andClause)
		if err != nil {
			return nil, err
		}
		filter = append(filter, sub)
	}

	// Handle OR: each sub-clause becomes a should (at least one must match)
	for _, orClause := range where.Or {
		sub, err := whereToBool(orClause)
		if err != nil {
			return nil, err
		}
		should = append(should, sub)
	}

	// Handle NOT: the negated sub-clause goes into must_not
	if where.Not != nil {
		sub, err := whereToBool(*where.Not)
		if err != nil {
			return nil, err
		}
		mustNot = append(mustNot, sub)
	}

	// If nothing was added, return match_all
	if len(filter) == 0 && len(mustNot) == 0 && len(should) == 0 {
		return map[string]any{"match_all": map[string]any{}}, nil
	}

	boolQ := map[string]any{}
	if len(filter) > 0 {
		boolQ["filter"] = filter
	}
	if len(mustNot) > 0 {
		boolQ["must_not"] = mustNot
	}
	if len(should) > 0 {
		boolQ["should"] = should
		boolQ["minimum_should_match"] = 1
	}

	return map[string]any{"bool": boolQ}, nil
}

// QueryLogGroup implements LogGroupSource for Elasticsearch.
// Uses ES terms aggregation to group error/critical logs by message, namespace, and workload.
func (e *ElasticSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	selectedNamespace := common.GetString(req.Request, "selectedNamespace")
	selectedWorkload := common.GetString(req.Request, "selectedWorkload")
	index := common.GetString(req.Request, "index")
	if index == "" {
		index = "*"
	}

	// Build filter query for error/critical logs
	filters := []map[string]any{
		{"bool": map[string]any{
			"should": []map[string]any{
				{"terms": map[string]any{"level": []string{"error", "critical", "fatal", "ERROR", "CRITICAL", "FATAL"}}},
				{"terms": map[string]any{"severity": []string{"error", "critical", "fatal", "ERROR", "CRITICAL", "FATAL"}}},
			},
			"minimum_should_match": 1,
		}},
		{"range": map[string]any{
			"@timestamp": map[string]any{
				"gte":    req.StartTime,
				"lte":    req.EndTime,
				"format": "epoch_millis",
			},
		}},
	}
	if selectedNamespace != "" {
		filters = append(filters, map[string]any{
			"term": map[string]any{"kubernetes.namespace_name.keyword": selectedNamespace},
		})
	}
	if selectedWorkload != "" {
		filters = append(filters, map[string]any{
			"wildcard": map[string]any{"kubernetes.pod_name.keyword": escapeESWildcard(selectedWorkload) + "*"},
		})
	}

	esQuery := map[string]any{
		"bool": map[string]any{"filter": filters},
	}

	// Terms aggregation to group by log message, with sub-aggs for namespace/container
	aggs := map[string]any{
		"log_groups": map[string]any{
			"terms": map[string]any{
				"field": "log.keyword",
				"size":  100,
			},
			"aggs": map[string]any{
				"namespaces": map[string]any{
					"terms": map[string]any{"field": "kubernetes.namespace_name.keyword", "size": 10},
				},
				"workloads": map[string]any{
					"terms": map[string]any{"field": "kubernetes.pod_name.keyword", "size": 10},
				},
				"containers": map[string]any{
					"terms": map[string]any{"field": "kubernetes.container_name.keyword", "size": 10},
				},
				"levels": map[string]any{
					"terms": map[string]any{"field": "level", "size": 10},
				},
			},
		},
	}

	actionParams := map[string]any{
		"query":      esQuery,
		"index":      index,
		"query_type": "dsl",
		"aggs":       aggs,
		"size":       0, // We only need aggregations, not documents
	}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:    req.AccountId,
			ActionName:   "query_es",
			ActionParams: actionParams,
			NoSinks:      true,
		},
	})
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("es.QueryLogGroup: failed to execute query: %w", err)
	}

	rawJSON, err := e.ExtractRawResponseString(resp)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("es.QueryLogGroup: failed to extract response: %w", err)
	}

	return parseESLogGroupResponse(rawJSON, req.EndTime)
}

// parseESLogGroupResponse parses ES aggregation response into LogGroupOutput.
func parseESLogGroupResponse(rawJSON string, endTime int64) (LogGroupOutput, error) {
	var esResp map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &esResp); err != nil {
		return LogGroupOutput{}, fmt.Errorf("es.QueryLogGroup: failed to parse response: %w", err)
	}

	aggs, ok := esResp["aggregations"].(map[string]any)
	if !ok {
		return LogGroupOutput{}, nil
	}

	logGroups, ok := aggs["log_groups"].(map[string]any)
	if !ok {
		return LogGroupOutput{}, nil
	}

	buckets, ok := logGroups["buckets"].([]any)
	if !ok {
		return LogGroupOutput{}, nil
	}

	groups := make([]LogGroup, 0, len(buckets))
	for _, b := range buckets {
		bucket, ok := b.(map[string]any)
		if !ok {
			continue
		}

		message, _ := bucket["key"].(string)
		count, _ := bucket["doc_count"].(float64)

		group := LogGroup{
			Sample:     message,
			Timestamps: []int64{endTime},
			Values:     []float64{count},
			Count:      int64(math.Round(count)),
		}

		// Extract first namespace from sub-aggregation
		if nsAgg, ok := bucket["namespaces"].(map[string]any); ok {
			if nsBuckets, ok := nsAgg["buckets"].([]any); ok && len(nsBuckets) > 0 {
				if nsBucket, ok := nsBuckets[0].(map[string]any); ok {
					if ns, ok := nsBucket["key"].(string); ok {
						group.Namespace = ns
					}
				}
			}
		}

		// Extract first workload from sub-aggregation (pod name → workload name)
		if wlAgg, ok := bucket["workloads"].(map[string]any); ok {
			if wlBuckets, ok := wlAgg["buckets"].([]any); ok && len(wlBuckets) > 0 {
				if wlBucket, ok := wlBuckets[0].(map[string]any); ok {
					if wl, ok := wlBucket["key"].(string); ok {
						group.Workload = extractWorkloadFromPodName(wl)
					}
				}
			}
		}

		// Extract first container from sub-aggregation
		if cAgg, ok := bucket["containers"].(map[string]any); ok {
			if cBuckets, ok := cAgg["buckets"].([]any); ok && len(cBuckets) > 0 {
				if cBucket, ok := cBuckets[0].(map[string]any); ok {
					if c, ok := cBucket["key"].(string); ok {
						group.Container = c
					}
				}
			}
		}

		// Extract first level from sub-aggregation
		if lvlAgg, ok := bucket["levels"].(map[string]any); ok {
			if lvlBuckets, ok := lvlAgg["buckets"].([]any); ok && len(lvlBuckets) > 0 {
				if lvlBucket, ok := lvlBuckets[0].(map[string]any); ok {
					if lvl, ok := lvlBucket["key"].(string); ok {
						group.Level = lvl
					}
				}
			}
		}

		if group.Namespace != "" && group.Workload != "" {
			group.ContainerID = fmt.Sprintf("/k8s/%s/%s", group.Namespace, group.Workload)
			if group.Container != "" {
				group.ContainerID += "/" + group.Container
			}
		}

		if message != "" {
			group.PatternHash = generatePatternHash(message)
		}

		groups = append(groups, group)
	}

	return LogGroupOutput{Groups: groups}, nil
}

// buildESQueryFromWhere converts a QueryWhereClause into an Elasticsearch DSL JSON string.
func buildESQueryFromWhere(where query.QueryWhereClause) (string, error) {
	queryClause, err := whereToBool(where)
	if err != nil {
		return "", fmt.Errorf("failed to build ES query from where clause: %w", err)
	}

	result, err := json.Marshal(map[string]any{"query": queryClause})
	if err != nil {
		return "", fmt.Errorf("failed to marshal ES DSL query: %w", err)
	}
	return string(result), nil
}

// buildPPLFromWhere converts a QueryWhereClause into an OpenSearch PPL where clause string.
// Example output: "where app = 'activegate' AND namespace = 'default'"
func buildPPLFromWhere(where query.QueryWhereClause) (string, error) {
	cond, err := whereToPPLCondition(where)
	if err != nil {
		return "", fmt.Errorf("failed to build PPL query from where clause: %w", err)
	}
	if cond == "" {
		return "", nil
	}
	return "where " + cond, nil
}

// whereToPPLCondition recursively converts a QueryWhereClause into a PPL condition string.
func whereToPPLCondition(where query.QueryWhereClause) (string, error) {
	var andParts []string

	// Binary clauses
	for field, ops := range where.Binary {
		for op, val := range ops {
			cond, err := binaryToPPLCondition(field, op, val)
			if err != nil {
				return "", err
			}
			andParts = append(andParts, cond)
		}
	}

	// AND: each sub-clause joined with AND
	for _, andClause := range where.And {
		sub, err := whereToPPLCondition(andClause)
		if err != nil {
			return "", err
		}
		if sub != "" {
			andParts = append(andParts, sub)
		}
	}

	result := strings.Join(andParts, " AND ")

	// OR: sub-clauses joined with OR, grouped in parentheses
	if len(where.Or) > 0 {
		var orParts []string
		for _, orClause := range where.Or {
			sub, err := whereToPPLCondition(orClause)
			if err != nil {
				return "", err
			}
			if sub != "" {
				orParts = append(orParts, sub)
			}
		}
		if len(orParts) > 0 {
			orExpr := strings.Join(orParts, " OR ")
			if len(orParts) > 1 {
				orExpr = "(" + orExpr + ")"
			}
			if result != "" {
				result += " AND " + orExpr
			} else {
				result = orExpr
			}
		}
	}

	// NOT: negate the sub-clause
	if where.Not != nil {
		sub, err := whereToPPLCondition(*where.Not)
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

// pplEscapeString escapes single quotes for PPL string literals.
func pplEscapeString(s string) string {
	return strings.ReplaceAll(s, `'`, `''`)
}

// pplEscapeLikePattern escapes LIKE wildcard characters (% and _) in addition to single quotes.
func pplEscapeLikePattern(s string) string {
	s = strings.ReplaceAll(s, `'`, `''`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// pplFormatValue formats a value for use in a PPL condition.
func pplFormatValue(val any) string {
	switch v := val.(type) {
	case string:
		return "'" + pplEscapeString(v) + "'"
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
		return "'" + pplEscapeString(fmt.Sprintf("%v", val)) + "'"
	}
}

// binaryToPPLCondition converts a single binary where operation to a PPL condition string.
func binaryToPPLCondition(field string, op query.BinaryWhereClauseType, val any) (string, error) {
	switch op {
	case query.Eq:
		return fmt.Sprintf("%s = %s", field, pplFormatValue(val)), nil
	case query.Nq:
		return fmt.Sprintf("%s != %s", field, pplFormatValue(val)), nil
	case query.Lt:
		return fmt.Sprintf("%s < %s", field, pplFormatValue(val)), nil
	case query.Gt:
		return fmt.Sprintf("%s > %s", field, pplFormatValue(val)), nil
	case query.Contains:
		valStr, ok := val.(string)
		if !ok {
			return "", fmt.Errorf("_contains operator requires string value for field %q, got %T", field, val)
		}
		return fmt.Sprintf("%s LIKE '%%%s%%'", field, pplEscapeLikePattern(valStr)), nil
	case query.In:
		vals, ok := val.([]any)
		if !ok {
			return "", fmt.Errorf("_in operator requires array value for field %q, got %T", field, val)
		}
		items := make([]string, len(vals))
		for i, v := range vals {
			items[i] = pplFormatValue(v)
		}
		return fmt.Sprintf("%s IN (%s)", field, strings.Join(items, ", ")), nil
	case query.NotIn:
		vals, ok := val.([]any)
		if !ok {
			return "", fmt.Errorf("_not_in operator requires array value for field %q, got %T", field, val)
		}
		items := make([]string, len(vals))
		for i, v := range vals {
			items[i] = pplFormatValue(v)
		}
		return fmt.Sprintf("NOT %s IN (%s)", field, strings.Join(items, ", ")), nil
	case query.Like:
		return fmt.Sprintf("%s LIKE %s", field, pplFormatValue(val)), nil
	case query.NLike:
		return fmt.Sprintf("NOT %s LIKE %s", field, pplFormatValue(val)), nil
	case query.IsNull:
		boolVal, ok := val.(bool)
		if !ok {
			return "", fmt.Errorf("_is_null operator requires boolean value for field %q, got %T", field, val)
		}
		if boolVal {
			return fmt.Sprintf("isnull(%s)", field), nil
		}
		return fmt.Sprintf("isnotnull(%s)", field), nil
	case query.Regex:
		valStr, ok := val.(string)
		if !ok {
			return "", fmt.Errorf("_regex operator requires string value for field %q, got %T", field, val)
		}
		return fmt.Sprintf("%s = regex('%s')", field, pplEscapeString(valStr)), nil
	default:
		return "", fmt.Errorf("unsupported PPL operator %q for field %q", op, field)
	}
}
