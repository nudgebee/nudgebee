package playbooks

import (
	"encoding/base64"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/relay"
	"unicode/utf8"
)

type proxyHTTPRequestAction struct{}

type proxyHTTPRequestParams struct {
	DatasourceID string            `json:"datasource_id"`
	Method       string            `json:"method,omitempty"`
	URL          string            `json:"url"`
	Headers      map[string]string `json:"headers,omitempty"`
	Body         string            `json:"body,omitempty"`
	TimeoutMs    int               `json:"timeout_ms,omitempty"`
	AccountID    string            `json:"account_id,omitempty"`
}

func (a *proxyHTTPRequestAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	var params proxyHTTPRequestParams
	if err := common.UnmarshalMapToStruct(rawParams, &params); err != nil {
		return nil, fmt.Errorf("failed to unmarshal params: %w", err)
	}

	if params.DatasourceID == "" {
		return nil, fmt.Errorf("datasource_id is required")
	}
	if params.URL == "" {
		return nil, fmt.Errorf("url is required")
	}

	accountID := params.AccountID
	if accountID == "" {
		accountID = resolveAccountIDForIntegration(params.DatasourceID)
	}
	if accountID == "" {
		accountID = ctx.GetAccountId()
	}
	if accountID == "" {
		return nil, fmt.Errorf("account_id is required")
	}

	method := params.Method
	if method == "" {
		method = "GET"
	}

	actionParams := map[string]any{
		"method": method,
		"url":    params.URL,
	}
	if params.Headers != nil {
		headerMap := make(map[string][]string, len(params.Headers))
		for k, v := range params.Headers {
			headerMap[k] = []string{v}
		}
		actionParams["header"] = headerMap
	}
	if params.Body != "" {
		actionParams["body"] = params.Body
	}

	datasourceKey, err := resolveDatasourceKey(params.DatasourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve datasource: %w", err)
	}

	timeoutSec := 120
	if params.TimeoutMs > 0 {
		timeoutSec = (params.TimeoutMs / 1000) + 5
	}

	result, err := relay.ExecuteProxy(accountID, "http_request", datasourceKey, actionParams, timeoutSec)
	if err != nil {
		return nil, fmt.Errorf("proxy http_request failed: %w", err)
	}

	// Decode base64-encoded response body for readable evidence
	if bodyB64, ok := result["body"].(string); ok {
		decoded, decErr := base64.StdEncoding.DecodeString(bodyB64)
		if decErr == nil {
			if utf8.Valid(decoded) {
				result["body"] = string(decoded)
			} else {
				result["body"] = "[binary data]"
			}
		}
	}

	title, _ := rawParams["title"].(string)
	if title == "" {
		title = "HTTP Request"
	}

	return NewPlaybookActionResponseJson(
		result,
		map[string]any{"title": title},
		nil,
		map[string]any{"query-result-version": "1.0", "method": method, "url": params.URL},
	), nil
}
