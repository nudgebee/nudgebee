package observability

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/query"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"sort"
	"strconv"
	"strings"
	"time"
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
	return []string{"_eq", "_neq", "_contains", "_like", "_ilike", "_nlike", "_gt", "_lt", "_is_null"}
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

	// The agent returns its payload as a JSON-stringified string (the dispatch
	// contract — see ExtractIndexNamesAny, which reads the same shape). For the
	// query_es_field_index_values action that payload is the raw Elasticsearch
	// _search response, with the distinct values under the `unique_values`
	// terms-aggregation buckets. (The legacy receiver pre-flattened this into a
	// string array; the Go agent does not, so we extract the buckets here.)
	raw, ok := outer["data"].(string)
	if !ok || raw == "" {
		return nil, fmt.Errorf("inner 'data' field not found or not a string")
	}

	var decoded struct {
		Aggregations struct {
			UniqueValues struct {
				Buckets []struct {
					Key any `json:"key"`
				} `json:"buckets"`
			} `json:"unique_values"`
		} `json:"aggregations"`
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, fmt.Errorf("failed to unmarshal inner 'data' JSON: %w", err)
	}

	buckets := decoded.Aggregations.UniqueValues.Buckets
	result := make([]string, 0, len(buckets))
	for _, b := range buckets {
		switch k := b.Key.(type) {
		case nil:
			continue
		case string:
			if k != "" {
				result = append(result, k)
			}
		default:
			// Numeric / boolean keys (non-keyword fields) — render as-is.
			result = append(result, fmt.Sprintf("%v", k))
		}
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

func extractTimestamp(src map[string]any) string {
	for _, key := range []string{"@timestamp", "time", "timestamp", "datetime"} {
		if val, exists := src[key]; exists && val != nil {
			str := strings.TrimSpace(fmt.Sprintf("%v", val))
			if str != "" && str != "<nil>" {
				return str
			}
		}
	}
	return ""
}

func ParseSourceMap(src map[string]any) (OutputLog, bool) {
	ts := extractTimestamp(src)
	if ts == "" {
		return OutputLog{}, false
	}

	// Message: ECS/Filebeat/Elastic Agent carry it at top-level "message";
	// Fluent-Bit carries it at top-level "log"; OTel-native docs carry it at
	// body.text — or a bare string "body".
	msg, ok := src["message"].(string)
	if !ok || strings.TrimSpace(msg) == "" {
		msg, ok = src["log"].(string)
	}
	if !ok || strings.TrimSpace(msg) == "" {
		msg = otelBodyText(src)
	}
	if strings.TrimSpace(msg) == "" {
		msg, _ = src["message"].(string)
	}
	// Still nothing recognisable as a line: the document is a real event that
	// simply has no message field (Packetbeat network_traffic.*, and other Elastic
	// integrations that match a logs-* pattern while carrying only structured
	// fields). Render the whole _source as the line rather than dropping the hit,
	// so those datasets are visible instead of silently returning nothing.
	if strings.TrimSpace(msg) == "" {
		msg = sourceAsMessage(src)
	}
	if strings.TrimSpace(msg) == "" {
		return OutputLog{}, false
	}

	// Stream: Fluent-Bit "stream"; OTel attributes.log.iostream.
	stream, _ := src["stream"].(string)
	if stream == "" {
		stream = otelIOStream(src)
	}
	severity := "INFO"
	switch strings.ToLower(stream) {
	case "stderr":
		severity = "ERROR"
	case "stdout":
		severity = "INFO"
	}
	log := OutputLog{
		Timestamp: normalizeESTimestamp(ts),
		Message:   msg,
		Severity:  severity,
		Labels:    make(map[string]any),
	}

	// Labels: OTel docs nest the useful identifiers under resource.attributes
	// (k8s.namespace.name, k8s.pod.name, …) and attributes; flatten those so
	// callers get flat label keys instead of nested maps. resource.attributes and
	// attributes are checked independently — a doc may carry attributes without
	// resource.attributes (non-k8s OTel, or resource detection disabled). Only
	// fall back to the legacy top-level copy (Fluent-Bit/ECS) when neither exists.
	ra, hasResourceAttrs := nestedMap(src, "resource", "attributes")
	a, hasAttrs := src["attributes"].(map[string]any)
	switch {
	case hasResourceAttrs || hasAttrs:
		for k, v := range ra {
			if v != nil {
				log.Labels[k] = v
			}
		}
		for k, v := range a {
			if v != nil {
				log.Labels[k] = v
			}
		}
	default:
		for k, v := range src {
			if k == "@timestamp" || k == "time" || k == "timestamp" || k == "datetime" || k == "stream" || k == "log" || k == "message" {
				continue
			}
			if v != nil {
				log.Labels[k] = v
			}
		}
	}

	return log, true
}

// otelBodyText extracts the log message from an OTel-native ES doc, where the
// body is either a bare string or an object with a "text" field.
func otelBodyText(src map[string]any) string {
	switch b := src["body"].(type) {
	case string:
		return b
	case map[string]any:
		if t, ok := b["text"].(string); ok {
			return t
		}
	}
	return ""
}

// sourceAsMessage renders a document's _source as a compact JSON string, for use
// as the log line when the document carries no message field of its own.
// @timestamp is omitted — OutputLog already carries it in its own field, so
// repeating it in the line is noise. Returns "" when nothing else remains, so a
// document that is only a timestamp (or is empty) is still dropped rather than
// rendered as "{}". Keys are emitted sorted: encoding/json orders map keys, so
// the same document always renders identically.
func sourceAsMessage(src map[string]any) string {
	rest := make(map[string]any, len(src))
	for k, v := range src {
		if k == "@timestamp" {
			continue
		}
		rest[k] = v
	}
	if len(rest) == 0 {
		return ""
	}
	encoded, err := json.Marshal(rest)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// otelIOStream returns attributes.log.iostream ("stdout"/"stderr") from an OTel doc.
func otelIOStream(src map[string]any) string {
	if a, ok := src["attributes"].(map[string]any); ok {
		if s, ok := a["log.iostream"].(string); ok {
			return s
		}
	}
	return ""
}

// nestedMap returns src[k1][k2] when both levels are maps.
func nestedMap(src map[string]any, k1, k2 string) (map[string]any, bool) {
	if m1, ok := src[k1].(map[string]any); ok {
		if m2, ok := m1[k2].(map[string]any); ok {
			return m2, true
		}
	}
	return nil, false
}

// normalizeESTimestamp renders an ES @timestamp as an RFC3339 timestamp with
// nanosecond precision (e.g. "2026-06-18T05:35:23.471912906Z"). OTel-native docs
// store @timestamp as epoch milliseconds, optionally with a fractional
// sub-millisecond part ("1781559826466.265767"); this converts it to RFC3339Nano,
// preserving the fraction down to nanoseconds. Non-numeric values (already
// ISO-8601, e.g. Fluent-Bit/ECS) pass through unchanged.
func normalizeESTimestamp(ts string) string {
	ts = strings.TrimSpace(ts)
	intPart := ts
	fracPart := ""
	if dot := strings.IndexByte(ts, '.'); dot >= 0 {
		intPart, fracPart = ts[:dot], ts[dot+1:]
	}
	ms, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return ts // already an ISO-8601 string (or some other non-epoch value)
	}
	// fracPart is the fractional millisecond. Six digits resolve to nanoseconds
	// (1e-6 ms = 1 ns); pad/truncate to six, then it is the ns within the ms.
	var fracNs int64
	if fracPart != "" {
		const fracDigits = 6
		if len(fracPart) > fracDigits {
			fracPart = fracPart[:fracDigits]
		}
		if f, perr := strconv.ParseInt(fracPart, 10, 64); perr == nil {
			for i := len(fracPart); i < fracDigits; i++ {
				f *= 10
			}
			fracNs = f
		}
	}
	totalNs := ms*int64(time.Millisecond) + fracNs
	return time.Unix(0, totalNs).UTC().Format(time.RFC3339Nano)
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
	index, _ := fetchLogRequest.Request["index"].(string)
	if index == "" {
		// The relay has no account config to fall back on, so an unresolved index
		// widens here rather than reaching the agent empty. Mirrors the SaaS source.
		index = esAllIndicesWildcard
		ctx.GetLogger().Warn("ES index fields: no index resolved for agent source, listing across every index",
			"account_id", fetchLogRequest.AccountId)
	}
	relayRequest := relay.ActionExecuteBody{
		AccountID:  fetchLogRequest.AccountId,
		ActionName: "query_es_index_field",
		ActionParams: map[string]any{
			"index": index,
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

// sqlLikeToESWildcard translates a canonical SQL LIKE pattern into an Elasticsearch
// wildcard pattern: `%` → `*`, `_` → `?`, honouring `\%` / `\_` / `\\` escapes and
// escaping any literal `*` / `?` the value already contained.
//
// The canonical where-clause contract is SQL LIKE — Loki converts the same input with
// convertSQLLikeToRegex (`%`→`.*`, `_`→`.`), and the query generator is prompted with
// patterns like "%error%". Elasticsearch was the only backend that passed the pattern
// through verbatim, so `%error%` reached ES as a literal wildcard containing percent
// signs and matched NOTHING — silently, with HTTP 200.
//
// Measured against the dev cluster on logs-kubernetes.container_logs-*:
// wildcard "%nudgebee%" → 0 hits; wildcard "*nudgebee*" → 10000 hits.
func sqlLikeToESWildcard(pattern string) string {
	// Scanned rune by rune rather than by successive ReplaceAll passes: the passes
	// could not tell a lone backslash from an escape introducer. `a\b` kept its single
	// backslash, which ES reads as an escape and drops (matching "ab"), and `a\*b`
	// (literal backslash, literal star) became `a\\*b`, where the star was left as a
	// wildcard instead of a literal.
	var sb strings.Builder
	sb.Grow(len(pattern) + 8)
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		switch r := runes[i]; r {
		case '\\':
			// SQL escape: `\%` and `\_` yield the literal character; anything else is a
			// literal backslash, which ES needs doubled.
			if i+1 < len(runes) && (runes[i+1] == '%' || runes[i+1] == '_') {
				sb.WriteRune(runes[i+1])
				i++
				continue
			}
			sb.WriteString(`\\`)
			if i+1 < len(runes) && runes[i+1] == '\\' {
				i++
			}
		case '%':
			sb.WriteByte('*')
		case '_':
			sb.WriteByte('?')
		case '*', '?':
			// Already an ES wildcard in the source value — keep it literal.
			sb.WriteByte('\\')
			sb.WriteRune(r)
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// esWildcardValue renders a filter value as the wildcard pattern string. The value is
// already a string on every path the query generator produces; the Sprintf fallback
// exists only for the numeric/bool literals a hand-written where clause can carry.
func esWildcardValue(val any) string {
	if s, ok := val.(string); ok {
		return sqlLikeToESWildcard(s)
	}
	return sqlLikeToESWildcard(fmt.Sprintf("%v", val))
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
		return map[string]any{
			"wildcard": map[string]any{field: map[string]any{"value": esWildcardValue(val)}},
		}, false, nil
	case query.NLike:
		return map[string]any{
			"wildcard": map[string]any{field: map[string]any{"value": esWildcardValue(val)}},
		}, true, nil
	case query.ILike:
		// Elasticsearch does case-insensitive wildcard matching natively, so there is
		// no reason to reject `_ilike` — and rejecting it was expensive. The generator
		// emits `_ilike` for text matching whatever the advertised operator list says
		// (few-shots outweigh the list), the whole query was then refused with
		// `unsupported operator "_ilike"`, and the agent burned a full iteration —
		// minutes of model time — rediscovering that by trial and error.
		return map[string]any{
			"wildcard": map[string]any{field: map[string]any{
				"value": esWildcardValue(val), "case_insensitive": true,
			}},
		}, false, nil
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
			clause, negate, err := binaryClauseForField(field, op, val)
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

// QueryLogGroup implements LogGroupSource for Elasticsearch (Loki-style).
//
// It fetches raw error/critical/fatal log documents from the selected index via
// QueryLogs — which parses every supported ES doc shape (Fluent-Bit/ECS and
// OTel-native) through ParseSourceMap — then groups them by message pattern
// in-memory (see groupESLogsByPattern). This replaces the previous terms
// aggregation, which grouped on hardcoded Fluent-Bit keyword fields
// (log.keyword, kubernetes.*_name.keyword) and so returned nothing for
// OTel-native indices, where the message lives at body.text and the k8s
// identifiers under resource.attributes.
func (e *ElasticSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	index := common.GetString(req.Request, "index")
	if index == "" {
		index = "*"
	}
	selectedNamespace := common.GetString(req.Request, "selectedNamespace")
	selectedWorkload := common.GetString(req.Request, "selectedWorkload")

	// The agent's query_es action does not inject the time range, so embed it in
	// the DSL alongside the error match.
	q := esLogGroupErrorQuery()
	if req.StartTime > 0 || req.EndTime > 0 {
		bq := q["bool"].(map[string]any)
		bq["filter"] = append(bq["filter"].([]any), esLogTimeRangeClause(req.StartTime, req.EndTime))
	}
	body, err := json.Marshal(map[string]any{
		"query": q,
		"sort":  esLogGroupSort(),
	})
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("es.QueryLogGroup: failed to marshal query: %w", err)
	}

	logs, err := e.QueryLogs(ctx, FetchLogRequest{
		AccountId: req.AccountId,
		Query:     string(body),
		Request:   map[string]any{"index": index, "query_type": "dsl"},
		Limit:     esLogGroupFetchLimit,
	})
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("es.QueryLogGroup: failed to fetch logs: %w", err)
	}

	return groupESLogsByPattern(logs, selectedNamespace, selectedWorkload, req.EndTime), nil
}

// esLogGroupFetchLimit bounds how many raw error documents are pulled back for
// in-memory grouping (matches the Loki log-group budget).
const esLogGroupFetchLimit = 1000

// esLogGroupErrorQuery builds the shared bool query matching error/critical/fatal
// logs. A doc qualifies (minimum_should_match 1) when EITHER its message text
// mentions an error keyword OR a structured level/severity field carries an error
// value:
//
//   - simple_query_string is scoped to the known message fields across doc shapes
//     (Fluent-Bit "log", OTel "body.text"/"body", generic "message"). Scoping to
//     the message — rather than every field — keeps a stray "error" in an
//     unrelated field, URL, or index name from pulling in non-error logs, and
//     keeps the esInferLogLevel label honest. It stays lenient by design, so a
//     message field the index does not have is silently ignored.
//   - terms on level/severity/severity_text catch structured errors whose message
//     text carries no keyword (e.g. {"level":"ERROR","msg":"connection refused"}),
//     including the OTel severity_text field. A term only matches a keyword-mapped
//     field, so it is additive: the message match remains the primary signal.
//
// Namespace/workload scoping is applied in Go (see groupESLogsByPattern), not
// here, because the k8s identifier field names differ across doc shapes and a
// hardcoded field filter silently excludes the shapes it does not name.
func esLogGroupErrorQuery() map[string]any {
	errorValues := []string{"error", "critical", "fatal", "ERROR", "CRITICAL", "FATAL"}
	return map[string]any{
		"bool": map[string]any{
			"filter": []any{
				map[string]any{
					"bool": map[string]any{
						"minimum_should_match": 1,
						"should": []any{
							map[string]any{
								"simple_query_string": map[string]any{
									"query":            "error critical fatal",
									"default_operator": "or",
									"fields":           []string{"log", "body.text", "body", "message"},
								},
							},
							map[string]any{"terms": map[string]any{"level": errorValues}},
							map[string]any{"terms": map[string]any{"severity": errorValues}},
							map[string]any{"terms": map[string]any{"severity_text": errorValues}},
						},
					},
				},
			},
		},
	}
}

// esLogGroupSort orders results newest-first; unmapped_type avoids a sort error
// when a "*" search spans an index without an @timestamp mapping.
func esLogGroupSort() []any {
	return []any{
		map[string]any{"@timestamp": map[string]any{"order": "desc", "unmapped_type": "date"}},
	}
}

// groupESLogsByPattern groups raw ES log entries by message pattern hash
// in-memory and returns the top error groups, mirroring the Loki/SolarWinds
// in-memory grouping. Namespace/workload are read via extractESK8sMeta, which
// understands both the Fluent-Bit ("kubernetes" nested object) and OTel-native
// (resource.attributes flattened, e.g. k8s.namespace.name) label shapes. The
// selectedNamespace/selectedWorkload scope filters drop a log only when a
// mismatching value is positively read, so docs whose shape omits the field are
// kept rather than silently discarded. Each group's Timestamps carry the max
// (last-seen) log timestamp, falling back to the query-window end.
func groupESLogsByPattern(logs []OutputLog, selectedNamespace, selectedWorkload string, endTime int64) LogGroupOutput {
	type groupEntry struct {
		sample    string
		hash      string
		namespace string
		workload  string
		container string
		level     string
		count     int64
		lastSeen  int64 // max log timestamp (unix seconds); 0 until one is parsed
	}

	grouped := make(map[string]*groupEntry)

	for _, log := range logs {
		if log.Message == "" {
			continue
		}

		namespace, pod, container := extractESK8sMeta(log.Labels)
		workload := extractWorkloadFromPodName(pod)

		if selectedNamespace != "" && namespace != "" && namespace != selectedNamespace {
			continue
		}
		if selectedWorkload != "" && workload != "" && workload != selectedWorkload {
			continue
		}

		hash := generatePatternHash(log.Message)
		level := esInferLogLevel(log.Message)
		seen := logLastSeenUnix(log.Timestamp)

		compositeKey := hash + "|" + namespace + "|" + workload + "|" + level
		entry, exists := grouped[compositeKey]
		if !exists {
			sample := log.Message
			if runes := []rune(sample); len(runes) > 500 {
				sample = string(runes[:500])
			}
			entry = &groupEntry{
				sample:    sample,
				hash:      hash,
				namespace: namespace,
				workload:  workload,
				container: container,
				level:     level,
			}
			grouped[compositeKey] = entry
		}
		entry.count++
		if seen > entry.lastSeen {
			entry.lastSeen = seen
		}
	}

	windowEnd := logGroupWindowEndUnix(endTime)

	groups := make([]LogGroup, 0, len(grouped))
	for _, entry := range grouped {
		containerID := ""
		if entry.namespace != "" && entry.workload != "" {
			containerID = fmt.Sprintf("/k8s/%s/%s", entry.namespace, entry.workload)
			if entry.container != "" {
				containerID += "/" + entry.container
			}
		}

		level := entry.level
		if level == "" {
			level = "error"
		}

		lastSeen := entry.lastSeen
		if lastSeen <= 0 {
			lastSeen = windowEnd
		}

		groups = append(groups, LogGroup{
			Sample:      entry.sample,
			Namespace:   entry.namespace,
			Workload:    entry.workload,
			Container:   entry.container,
			ContainerID: containerID,
			PatternHash: entry.hash,
			Level:       level,
			Count:       entry.count,
			Timestamps:  []int64{lastSeen},
			Values:      []float64{float64(entry.count)},
		})
	}

	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Count > groups[j].Count
	})

	if len(groups) > 100 {
		groups = groups[:100]
	}

	return LogGroupOutput{Groups: groups}
}

// extractESK8sMeta reads the Kubernetes namespace, pod, and container from a
// parsed ES log's labels across the supported doc shapes:
//   - OTel-native: resource.attributes are flattened by ParseSourceMap, e.g.
//     "k8s.namespace.name", "k8s.pod.name", "k8s.container.name".
//   - Fluent-Bit/ECS: the top-level "kubernetes" object is preserved as a nested
//     map with "namespace_name", "pod_name", "container_name".
func extractESK8sMeta(labels map[string]any) (namespace, pod, container string) {
	namespace = esLabelString(labels, "k8s.namespace.name", "kubernetes.namespace_name", "namespace_name", "namespace")
	pod = esLabelString(labels, "k8s.pod.name", "kubernetes.pod_name", "pod_name", "pod")
	container = esLabelString(labels, "k8s.container.name", "kubernetes.container_name", "container_name", "container")

	if kube, ok := labels["kubernetes"].(map[string]any); ok {
		if namespace == "" {
			namespace, _ = kube["namespace_name"].(string)
		}
		if pod == "" {
			pod, _ = kube["pod_name"].(string)
		}
		if container == "" {
			container, _ = kube["container_name"].(string)
		}
	}
	return namespace, pod, container
}

// esLabelString returns the first non-empty string value among the given label keys.
func esLabelString(labels map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := labels[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// esInferLogLevel derives a severity level from the message text. Documents
// reaching the grouper are already filtered to error/critical/fatal, so the
// default is "error"; the finer levels are surfaced when the keyword is present.
func esInferLogLevel(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "fatal"):
		return "fatal"
	case strings.Contains(lower, "critical"):
		return "critical"
	case strings.Contains(lower, "error"):
		return "error"
	case strings.Contains(lower, "warn"):
		return "warning"
	default:
		return "error"
	}
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
