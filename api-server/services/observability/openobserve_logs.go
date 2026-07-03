package observability

import (
	"bytes"
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

// OpenObserveLogSource implements LogSource interface for OpenObserve
type OpenObserveLogSource struct{}

// openObserveLogLabelMapping maps standard field names to OpenObserve field names
var openObserveLogLabelMapping = map[string]string{
	"timestamp": "_timestamp",
	"body":      "message",
	"message":   "message",
}

func (s *OpenObserveLogSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_contains", "_ilike"}
}

func (s *OpenObserveLogSource) GetLabelMapping() map[string]string {
	return openObserveLogLabelMapping
}

func (s *OpenObserveLogSource) GetIgnoredQueryRequestKeys() []string {
	return []string{} // No keys ignored by default
}

type openObserveSearchRequest struct {
	Query struct {
		SQL       string `json:"sql"`
		StartTime int64  `json:"start_time"`
		EndTime   int64  `json:"end_time"`
		Size      int    `json:"size,omitempty"`
	} `json:"query"`
}

type openObserveSearchResponse struct {
	Hits []map[string]any `json:"hits"`
}

func escapeOpenObserveString(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func buildOpenObserveBinaryClause(binary query.BinaryWhereClause, mapping map[string]string) (string, error) {
	var parts []string
	for field, ops := range binary {
		col := field
		if mapped, ok := mapping[col]; ok {
			col = mapped
		}
		for op, val := range ops {
			strVal := fmt.Sprintf("%v", val)
			switch op {
			case query.Eq:
				parts = append(parts, fmt.Sprintf("%s = '%s'", col, escapeOpenObserveString(strVal)))
			case query.Nq:
				parts = append(parts, fmt.Sprintf("%s != '%s'", col, escapeOpenObserveString(strVal)))
			case query.Contains:
				parts = append(parts, fmt.Sprintf("str_match(%s, '.*%s.*')", col, escapeOpenObserveString(strVal)))
			case query.ILike:
				parts = append(parts, fmt.Sprintf("str_match_ignore_case(%s, '.*%s.*')", col, escapeOpenObserveString(strVal)))
			default:
				return "", fmt.Errorf("unsupported binary operator for OpenObserve: %s", op)
			}
		}
	}
	return strings.Join(parts, " AND "), nil
}

func buildOpenObserveSQLWhereClause(where query.QueryWhereClause) (string, error) {
	if len(where.Binary) > 0 {
		return buildOpenObserveBinaryClause(where.Binary, openObserveLogLabelMapping)
	}

	if len(where.And) > 0 {
		var parts []string
		for _, c := range where.And {
			part, err := buildOpenObserveSQLWhereClause(c)
			if err != nil {
				return "", err
			}
			if part != "" {
				parts = append(parts, part)
			}
		}
		if len(parts) == 0 {
			return "", nil
		}
		if len(parts) == 1 {
			return parts[0], nil
		}
		return "(" + strings.Join(parts, " AND ") + ")", nil
	}

	if len(where.Or) > 0 {
		var parts []string
		for _, c := range where.Or {
			part, err := buildOpenObserveSQLWhereClause(c)
			if err != nil {
				return "", err
			}
			if part != "" {
				parts = append(parts, part)
			}
		}
		if len(parts) == 0 {
			return "", nil
		}
		if len(parts) == 1 {
			return parts[0], nil
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil
	}

	if where.Not != nil {
		notPart, err := buildOpenObserveSQLWhereClause(*where.Not)
		if err != nil {
			return "", err
		}
		if notPart != "" {
			return fmt.Sprintf("NOT (%s)", notPart), nil
		}
	}

	return "", nil
}

func (s *OpenObserveLogSource) buildSQL(req FetchLogRequest) (string, error) {
	whereClause, err := buildOpenObserveSQLWhereClause(req.QueryRequest.Where)
	if err != nil {
		return "", err
	}
	
	limit := req.Limit
	if limit == 0 {
		limit = 100
	}
	if limit > 10000 {
		limit = 10000
	}
	
	sql := `SELECT * FROM "default"`
	if whereClause != "" {
		sql += " WHERE " + whereClause
	}
	
	sql += fmt.Sprintf(" ORDER BY _timestamp DESC LIMIT %d", limit)
	
	return sql, nil
}

func (s *OpenObserveLogSource) GetQuery(ctx *security.RequestContext, req FetchLogRequest) (string, error) {
	return s.buildSQL(req)
}

func (s *OpenObserveLogSource) QueryLogs(ctx *security.RequestContext, req FetchLogRequest) ([]OutputLog, error) {
	url, orgID, username, password, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	sql, err := s.buildSQL(req)
	if err != nil {
		return nil, err
	}

	// OpenObserve requires microseconds
	startTimeMicros := req.StartTime * 1000
	endTimeMicros := req.EndTime * 1000

	searchReq := openObserveSearchRequest{}
	searchReq.Query.SQL = sql
	searchReq.Query.StartTime = startTimeMicros
	searchReq.Query.EndTime = endTimeMicros

	payloadBytes, err := json.Marshal(searchReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal search request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/%s/_search", url, orgID)
	authHeader := fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(username+":"+password)))

	resp, err := common.HttpPost(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": authHeader,
			"Content-Type":  "application/json",
		}),
		common.HttpWithBody(io.NopCloser(bytes.NewReader(payloadBytes))),
		common.HttpWithTimeout(30*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("OpenObserve API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		var errorBody map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errorBody)
		return nil, fmt.Errorf("OpenObserve query failed with status %d: %v", resp.StatusCode, errorBody)
	}

	var searchResp openObserveSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("failed to decode OpenObserve response: %w", err)
	}

	var outputs []OutputLog
	for _, hit := range searchResp.Hits {
		out := OutputLog{
			Labels: make(map[string]any),
		}
		
		for k, v := range hit {
			if k == "_timestamp" {
				if tVal, ok := v.(float64); ok {
					// convert micros to format
					ts := time.UnixMicro(int64(tVal))
					out.Timestamp = ts.UTC().Format(time.RFC3339Nano)
				} else if tStr, ok := v.(string); ok {
					out.Timestamp = tStr
				}
				out.Labels[k] = v
			} else if k == "message" || k == "log" || k == "body" {
				if str, ok := v.(string); ok {
					out.Message = str
				}
				out.Labels[k] = v
			} else if k == "severity" || k == "level" {
				if str, ok := v.(string); ok {
					out.Severity = str
				}
				out.Labels[k] = v
			} else {
				out.Labels[k] = v
			}
		}
		
		outputs = append(outputs, out)
	}

	return outputs, nil
}

func (s *OpenObserveLogSource) QueryLabels(ctx *security.RequestContext, req FetchLogLabelRequest) ([]OutputLogLabel, error) {
	// Simple implementation: fetch a single recent log to infer labels from the default stream
	fetchReq := FetchLogRequest{
		AccountId:         req.AccountId,
		LogProvider:       req.LogProvider,
		LogProviderSource: req.LogProviderSource,
		StartTime:         req.StartTime,
		EndTime:           req.EndTime,
		Limit:             1,
	}
	
	logs, err := s.QueryLogs(ctx, fetchReq)
	if err != nil {
		return nil, err
	}
	
	if len(logs) == 0 {
		return []OutputLogLabel{}, nil
	}
	
	var labels []OutputLogLabel
	for k := range logs[0].Labels {
		labels = append(labels, OutputLogLabel{
			Label:      k,
			Attributes: make(map[string]any),
		})
	}
	
	return labels, nil
}

func (s *OpenObserveLogSource) QueryLabelValues(ctx *security.RequestContext, req FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	// Query unique values for the requested label using SQL GROUP BY
	url, orgID, username, password, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	col := req.LabelName
	if mapped, ok := openObserveLogLabelMapping[col]; ok {
		col = mapped
	}

	sql := fmt.Sprintf(`SELECT %s FROM "default" GROUP BY %s LIMIT 100`, col, col)

	startTimeMicros := req.StartTime * 1000
	endTimeMicros := req.EndTime * 1000

	searchReq := openObserveSearchRequest{}
	searchReq.Query.SQL = sql
	searchReq.Query.StartTime = startTimeMicros
	searchReq.Query.EndTime = endTimeMicros

	payloadBytes, err := json.Marshal(searchReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal search request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/%s/_search", url, orgID)
	authHeader := fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(username+":"+password)))

	resp, err := common.HttpPost(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": authHeader,
			"Content-Type":  "application/json",
		}),
		common.HttpWithBody(io.NopCloser(bytes.NewReader(payloadBytes))),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("OpenObserve API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenObserve query failed with status %d", resp.StatusCode)
	}

	var searchResp openObserveSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("failed to decode OpenObserve response: %w", err)
	}

	var values []OutputLogLabelValue
	for _, hit := range searchResp.Hits {
		if val, ok := hit[col]; ok {
			strVal := fmt.Sprintf("%v", val)
			if strVal != "" {
				values = append(values, OutputLogLabelValue{
					Value:      strVal,
					Attributes: make(map[string]any),
				})
			}
		}
	}

	return values, nil
}
