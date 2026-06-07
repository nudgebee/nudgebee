package playbooks

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
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

// validateProxyURL prevents SSRF from the api-server's perspective.
// The actual HTTP request is made by the relay inside the customer's cluster,
// so private RFC1918 IPs are legitimate targets. We only block addresses that
// would be dangerous from the api-server's own network: loopback (api-server
// services), link-local (cloud metadata like 169.254.169.254), and unspecified.
// DNS failures are allowed through because the relay may resolve cluster-internal
// names that the api-server cannot.
func validateProxyURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q: only http and https are allowed", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("missing host in URL")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil // relay may resolve cluster-internal DNS that api-server cannot
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return fmt.Errorf("URL resolves to restricted IP %s", ip)
		}
	}
	return nil
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
	if err := validateProxyURL(params.URL); err != nil {
		return nil, fmt.Errorf("URL validation failed: %w", err)
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
