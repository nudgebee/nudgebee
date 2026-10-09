// Package promforager carries Prometheus API calls to a customer-network
// Prometheus through the account's forager (connection_mode=vm_agent). It is its
// own package because both integrations and observability/alertrule need it and
// alertrule cannot import integrations (integrations → eventrule → alertrule).
package promforager

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"

	"nudgebee/services/integrations/promclient"
	"nudgebee/services/relay"
)

// foragerTimeoutSeconds bounds one proxied query. It is above the
// forager's own 120s upstream timeout so the forager's error wins the race.
const foragerTimeoutSeconds = 125

// executeProxy is the relay call, indirected so tests need no relay server.
var executeProxy = relay.ExecuteProxyWithAgentTypeContext

// NewTransport sends a Prometheus API call through the account's
// forager as an http_request action against the datasource the forager holds.
func NewTransport(accountID, datasourceKey, agentType string) promclient.ProxyTransport {
	return func(ctx context.Context, method, relPath string, header http.Header, body []byte) (int, http.Header, []byte, error) {
		params := map[string]any{"method": method, "url": relPath}
		if len(header) > 0 {
			params["header"] = map[string][]string(header)
		}
		if len(body) > 0 {
			// Always base64: the forager falls back to raw bytes only when decoding
			// fails, so a raw body that happens to be valid base64 would be mangled.
			params["body"] = base64.StdEncoding.EncodeToString(body)
		}

		result, err := executeProxy(ctx, accountID, "http_request", datasourceKey, params, foragerTimeoutSeconds, agentType)
		if err != nil {
			return 0, nil, nil, err
		}

		statusF, ok := result["status_code"].(float64)
		if !ok {
			return 0, nil, nil, fmt.Errorf("forager response has no status_code")
		}
		respBody, err := base64.StdEncoding.DecodeString(fmt.Sprint(result["body"]))
		if err != nil {
			return 0, nil, nil, fmt.Errorf("decoding forager response body: %w", err)
		}
		respHeader := make(http.Header)
		if raw, ok := result["header"].(map[string]any); ok {
			for k, vals := range raw {
				if list, ok := vals.([]any); ok {
					for _, v := range list {
						respHeader.Add(k, fmt.Sprint(v))
					}
				}
			}
		}
		return int(statusF), respHeader, respBody, nil
	}
}
