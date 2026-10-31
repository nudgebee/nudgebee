package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"
)

const (
	openObserveDefaultLogStream   = "default"
	openObserveDefaultTraceStream = "default"
	openObserveDefaultResultSize  = 100
	openObserveMaxResponseBytes   = 4 << 20
)

type openObserveConfig struct {
	URL         string
	OrgID       string
	Username    string
	Password    string
	LogStream   string
	TraceStream string
}

func getOpenObserveConfig(ctx core.NbToolContext) (openObserveConfig, error) {
	cfg := openObserveConfig{
		LogStream:   openObserveDefaultLogStream,
		TraceStream: openObserveDefaultTraceStream,
	}
	for _, value := range ctx.ToolConfig.Values {
		switch strings.ToLower(strings.TrimSpace(value.Name)) {
		case "openobserve_url":
			cfg.URL = strings.TrimSpace(value.Value)
		case "openobserve_org_id":
			cfg.OrgID = strings.TrimSpace(value.Value)
		case "openobserve_username":
			cfg.Username = value.Value
		case "openobserve_password":
			cfg.Password = value.Value
			if value.IsEncrypted {
				password, err := common.Decrypt(value.Value)
				if err != nil {
					return cfg, fmt.Errorf("openobserve: unable to decrypt password: %w", err)
				}
				cfg.Password = password
			}
		case "openobserve_log_stream":
			if strings.TrimSpace(value.Value) != "" {
				cfg.LogStream = strings.TrimSpace(value.Value)
			}
		case "openobserve_trace_stream":
			if strings.TrimSpace(value.Value) != "" {
				cfg.TraceStream = strings.TrimSpace(value.Value)
			}
		}
	}
	if cfg.URL == "" || cfg.OrgID == "" || cfg.Username == "" || cfg.Password == "" {
		return cfg, fmt.Errorf("openobserve: openobserve_url, openobserve_org_id, openobserve_username, and openobserve_password are required in the integration configuration")
	}
	parsed, err := url.ParseRequestURI(cfg.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return cfg, fmt.Errorf("openobserve: openobserve_url must be an http or https URL without embedded credentials")
	}
	// Match the integration's API-side normalization: its path is not part of the API base URL.
	cfg.URL = parsed.Scheme + "://" + parsed.Host
	if !isSafeOpenObserveStream(cfg.LogStream) || !isSafeOpenObserveStream(cfg.TraceStream) {
		return cfg, fmt.Errorf("openobserve: stream names may contain only letters, digits, and underscores")
	}
	return cfg, nil
}

func getOpenObserveConfigSchema() core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"openobserve_url", "openobserve_org_id", "openobserve_username", "openobserve_password"},
		ConfigType:   "openobserve",
		ConfigSource: core.ToolConfigSourceIntegration,
		Properties: map[string]core.ToolSchemaProperty{
			"openobserve_url":          {Type: core.ToolSchemaTypeString, Description: "OpenObserve base URL"},
			"openobserve_org_id":       {Type: core.ToolSchemaTypeString, Description: "OpenObserve organization ID"},
			"openobserve_username":     {Type: core.ToolSchemaTypeString, Description: "OpenObserve user name"},
			"openobserve_password":     {Type: core.ToolSchemaTypeString, Description: "OpenObserve password or API token", IsEncrypted: true},
			"openobserve_log_stream":   {Type: core.ToolSchemaTypeString, Description: "Default log stream", Default: openObserveDefaultLogStream},
			"openobserve_trace_stream": {Type: core.ToolSchemaTypeString, Description: "Default trace stream", Default: openObserveDefaultTraceStream},
		},
	}
}

func openObserveTimeRange(ctx core.NbToolContext, args map[string]any, defaultWindow time.Duration) (time.Time, time.Time, error) {
	start, end, err := ExtractStartEndtimeFromLabels(ctx, args)
	if err == nil {
		if !start.Before(end) {
			return time.Time{}, time.Time{}, fmt.Errorf("openobserve: start_time must be earlier than end_time")
		}
		return start.UTC(), end.UTC(), nil
	}
	for _, key := range []string{"start_time", "end_time", "start", "end", "range", "duration"} {
		if value, ok := args[key]; ok && value != nil && value != "" {
			return time.Time{}, time.Time{}, fmt.Errorf("openobserve: provide a valid start_time/end_time pair or range: %w", err)
		}
	}
	if defaultWindow <= 0 {
		defaultWindow = time.Hour
	}
	end = time.Now().UTC()
	return end.Add(-defaultWindow), end, nil
}

func isSafeOpenObserveStream(stream string) bool {
	if stream == "" {
		return false
	}
	for _, r := range stream {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

// shortcut: reject SQL outside the single-stream subset; use a dialect parser if joins, subqueries, or dollar strings are needed.
func prepareOpenObserveSQL(query, stream string) (string, error) {
	query = strings.TrimSpace(query)
	if len(query) >= 2 && query[0] == '`' && query[len(query)-1] == '`' {
		query = strings.TrimSpace(query[1 : len(query)-1])
	}
	if query == "" {
		return "", fmt.Errorf("openobserve: query is required")
	}
	if !isSafeOpenObserveStream(stream) {
		return "", fmt.Errorf("openobserve: invalid configured stream name")
	}
	tokens, err := tokenizeOpenObserveSQL(query)
	if err != nil {
		return "", err
	}
	if len(tokens) == 0 {
		return "", fmt.Errorf("openobserve: query is required")
	}

	first := strings.ToLower(tokens[0].value)
	fullSelect := first == "select"
	if fullSelect {
		fromCount := 0
		inFromClause := false
		for i, token := range tokens {
			if token.kind != 'w' {
				if inFromClause && token.value == "," && token.depth == 0 {
					return "", fmt.Errorf("openobserve: SELECT queries may use only one stream")
				}
				continue
			}
			word := strings.ToLower(token.value)
			switch word {
			case "select":
				if i != 0 {
					return "", fmt.Errorf("openobserve: nested SELECT queries are not supported")
				}
			case "with", "union", "join", "into", "insert", "update", "delete", "drop", "alter", "create", "truncate", "copy":
				return "", fmt.Errorf("openobserve: only a single read-only SELECT is supported")
			case "from":
				fromCount++
				if token.depth != 0 || i+1 >= len(tokens) {
					return "", fmt.Errorf("openobserve: SELECT must read from the configured stream")
				}
				source := tokens[i+1]
				if (source.kind != 'w' && source.kind != 'i') || !strings.EqualFold(source.value, stream) {
					return "", fmt.Errorf("openobserve: SELECT may read only from the configured stream %q", stream)
				}
				if i+2 < len(tokens) && (tokens[i+2].value == "." || tokens[i+2].value == ",") {
					return "", fmt.Errorf("openobserve: SELECT queries may use only one stream")
				}
				inFromClause = true
			case "where", "group", "order", "having", "limit", "offset":
				inFromClause = false
			}
		}
		if fromCount != 1 {
			return "", fmt.Errorf("openobserve: SELECT must read from the configured stream %q", stream)
		}
		return query, nil
	}

	if first == "where" {
		query = strings.TrimSpace(query[tokens[0].end:])
	}
	if query == "" {
		return "", fmt.Errorf("openobserve: WHERE predicate is required")
	}
	for _, token := range tokens {
		if token.kind != 'w' {
			continue
		}
		switch strings.ToLower(token.value) {
		case "select", "with", "union", "join", "from", "into", "insert", "update", "delete", "drop", "alter", "create", "truncate", "copy":
			return "", fmt.Errorf("openobserve: predicates cannot contain query or statement clauses")
		}
	}
	return fmt.Sprintf("SELECT * FROM %s WHERE %s", stream, query), nil
}

type openObserveSQLToken struct {
	value string
	kind  byte
	depth int
	end   int
}

func tokenizeOpenObserveSQL(query string) ([]openObserveSQLToken, error) {
	tokens := make([]openObserveSQLToken, 0, len(query)/4)
	depth := 0
	for i := 0; i < len(query); {
		c := query[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			i++
			continue
		}
		if c == '-' && i+1 < len(query) && query[i+1] == '-' || c == '/' && i+1 < len(query) && query[i+1] == '*' {
			return nil, fmt.Errorf("openobserve: SQL comments are not supported")
		}
		if c == ';' {
			return nil, fmt.Errorf("openobserve: only one SQL statement is allowed")
		}
		if c == '$' && strings.IndexByte(query[i+1:], '$') >= 0 {
			return nil, fmt.Errorf("openobserve: dollar-quoted strings are not supported")
		}
		if c == '\'' || c == '"' || c == '`' {
			quote := c
			start := i + 1
			closed := false
			escapedString := quote == '\'' && len(tokens) > 0 && tokens[len(tokens)-1].kind == 'w' && strings.EqualFold(tokens[len(tokens)-1].value, "E") && tokens[len(tokens)-1].end == i
			i++
			for i < len(query) {
				if escapedString && query[i] == '\\' {
					if i+1 >= len(query) {
						return nil, fmt.Errorf("openobserve: unterminated escaped string")
					}
					i += 2
					continue
				}
				if query[i] == quote {
					if i+1 < len(query) && query[i+1] == quote {
						i += 2
						continue
					}
					kind := byte('i')
					if quote == '\'' {
						kind = 's'
					}
					tokens = append(tokens, openObserveSQLToken{value: query[start:i], kind: kind, depth: depth, end: i + 1})
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("openobserve: unterminated quoted value")
			}
			continue
		}
		if c == '(' {
			depth++
			tokens = append(tokens, openObserveSQLToken{value: "(", kind: 'p', depth: depth, end: i + 1})
			i++
			continue
		}
		if c == ')' {
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("openobserve: unbalanced SQL parentheses")
			}
			tokens = append(tokens, openObserveSQLToken{value: ")", kind: 'p', depth: depth, end: i + 1})
			i++
			continue
		}
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			start := i
			for i < len(query) {
				c = query[i]
				if c != '_' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
					break
				}
				i++
			}
			tokens = append(tokens, openObserveSQLToken{value: query[start:i], kind: 'w', depth: depth, end: i})
			continue
		}
		tokens = append(tokens, openObserveSQLToken{value: string(c), kind: 'p', depth: depth, end: i + 1})
		i++
	}
	if depth != 0 {
		return nil, fmt.Errorf("openobserve: unbalanced SQL parentheses")
	}
	return tokens, nil
}

type openObserveClient struct {
	config     openObserveConfig
	httpClient *http.Client
}

func newOpenObserveClient(config openObserveConfig) openObserveClient {
	return openObserveClient{config: config, httpClient: &http.Client{Timeout: 30 * time.Second}}
}

func (c openObserveClient) search(ctx context.Context, sql string, start, end time.Time, size int, signalType string) (map[string]any, error) {
	if !start.Before(end) {
		return nil, fmt.Errorf("openobserve: start_time must be earlier than end_time")
	}
	if size <= 0 {
		size = openObserveDefaultResultSize
	}
	request := struct {
		Query struct {
			SQL       string `json:"sql"`
			StartTime int64  `json:"start_time"`
			EndTime   int64  `json:"end_time"`
			From      int    `json:"from"`
			Size      int    `json:"size"`
		} `json:"query"`
	}{}
	request.Query.SQL = sql
	request.Query.StartTime = start.UnixMicro()
	request.Query.EndTime = end.UnixMicro()
	request.Query.Size = size
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("openobserve: unable to encode search request: %w", err)
	}
	endpoint := c.apiURL("_search")
	if signalType != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return nil, fmt.Errorf("openobserve: unable to build search URL: %w", err)
		}
		query := parsed.Query()
		query.Set("type", signalType)
		parsed.RawQuery = query.Encode()
		endpoint = parsed.String()
	}
	return c.do(ctx, endpoint, "application/json", body)
}

func (c openObserveClient) apiURL(resource string) string {
	endpoint := strings.TrimRight(c.config.URL, "/") + "/api/" + url.PathEscape(c.config.OrgID) + "/" + strings.TrimLeft(resource, "/")
	return endpoint
}

func (c openObserveClient) do(ctx context.Context, endpoint, contentType string, body []byte) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openobserve: unable to create request: %w", err)
	}
	req.SetBasicAuth(c.config.Username, c.config.Password)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	client := c.httpClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openobserve: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, openObserveMaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("openobserve: unable to read response: %w", err)
	}
	if len(responseBody) > openObserveMaxResponseBytes {
		return nil, fmt.Errorf("openobserve: response exceeds %d bytes", openObserveMaxResponseBytes)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("openobserve: API returned HTTP %d: %s", resp.StatusCode, openObserveErrorMessage(responseBody))
	}
	var result map[string]any
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return nil, fmt.Errorf("openobserve: unable to decode JSON response: %w", err)
	}
	if status, ok := result["status"].(string); ok && strings.EqualFold(status, "error") {
		message, _ := result["error"].(string)
		return nil, fmt.Errorf("openobserve: query failed: %s", message)
	}
	return result, nil
}

func openObserveErrorMessage(body []byte) string {
	var detail struct {
		Message     string   `json:"message"`
		Error       string   `json:"error"`
		ErrorDetail string   `json:"error_detail"`
		Hint        string   `json:"hint"`
		Suggestions []string `json:"suggestions"`
	}
	if json.Unmarshal(body, &detail) == nil {
		parts := make([]string, 0, 3)
		for _, value := range []string{detail.Message, detail.Error, detail.ErrorDetail, detail.Hint} {
			if value = strings.TrimSpace(value); value != "" {
				parts = append(parts, value)
			}
		}
		if len(detail.Suggestions) > 0 {
			parts = append(parts, "suggestions: "+strings.Join(detail.Suggestions, ", "))
		}
		if len(parts) > 0 {
			message := strings.Join(parts, "; ")
			if len(message) > 1000 {
				message = message[:1000] + "…"
			}
			return message
		}
	}
	message := strings.TrimSpace(string(body))
	if len(message) > 1000 {
		message = message[:1000] + "…"
	}
	if message == "" {
		return "no error details returned"
	}
	return message
}
