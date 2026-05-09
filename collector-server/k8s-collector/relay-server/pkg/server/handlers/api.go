// pkg/server/handlers/api.go
package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"nudgebee/relay-server/pkg/config"
	"nudgebee/relay-server/pkg/db"
	"nudgebee/relay-server/pkg/models"
	"nudgebee/relay-server/pkg/mq"
	"nudgebee/relay-server/pkg/signing"
	"nudgebee/relay-server/pkg/utils"
)

// NewAPIProxyHandler creates a generic API proxy handler
// This is a generic implementation similar to NewPrometheusHandler but for any API endpoint.
// It forwards HTTP requests to agents via RPC and returns responses.
func NewAPIProxyHandler(
	store db.AgentStore,
	rpcClient mq.RPCClient,
	topo *mq.Topology,
	cfg *config.Config,
	signer *signing.Signer,
	tracer *trace.Tracer,
	meter *metric.Meter,
	rootLogger *slog.Logger,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		w := c.Writer

		// 1) Extract account ID
		acct := c.GetHeader("X-NB-ACCOUNT-ID")
		if acct == "" {
			rootLogger.Error("missing X-NB-ACCOUNT-ID header")
			c.JSON(http.StatusBadRequest, utils.BuildError(400, "missing account_id"))
			return
		}
		agentType := c.GetHeader("X-NB-Agent-Type")
		if agentType == "" {
			agentType = "k8s"
		}
		c.Set("accountID", acct)

		logger, _, _ := utils.BuildContextFromPayload(c, tracer, meter, rootLogger)

		reqID := c.Request.Header.Get("X-NB-REQUEST-ID")
		if reqID == "" {
			reqID = uuid.NewString()
			c.Request.Header.Set("X-NB-REQUEST-ID", reqID)
		}

		// 2) Extract target API base URL from header (REQUIRED)
		apiBaseURL := c.GetHeader("X-API-Base-URL")
		if apiBaseURL == "" {
			logger.Error("missing X-API-Base-URL header for API proxy")
			c.JSON(http.StatusBadRequest, utils.BuildError(400, "missing X-API-Base-URL header - required for API proxy"))
			return
		}

		// 3) Serialize the incoming HTTP request for generic API proxy
		// Strip the "/api/proxy" prefix from the URL for the forwarded request
		httpReq, err := utils.SerializeAPIProxyRequest(c.Request)
		if err != nil {
			logger.Error("failed to serialize HTTP request", "err", err)
			c.JSON(http.StatusBadRequest, utils.BuildError(400, "failed to read request body"))
			return
		}

		// Add headers to identify this as an API proxy request and pass target URL
		if httpReq.Header == nil {
			httpReq.Header = make(map[string][]string)
		}
		httpReq.Header["X-NB-Request-Type"] = []string{"APIProxy"}
		httpReq.Header["X-API-Base-URL"] = []string{apiBaseURL}

		// 4) WS vs HTTP fallback
		allowed, _, err := store.IsWSEnabled(c.Request.Context(), acct, agentType)
		if err != nil {
			logger.Error("failed to check WS permission", "account_id", acct, "err", err)
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "internal server error"))
			return
		}
		if !allowed {
			logger.Info("WS disabled for API proxy, falling back to direct HTTP", "account", acct)
			// For API proxy, we don't have a fallback mechanism since it's generic
			// Just return an error
			c.JSON(http.StatusServiceUnavailable, utils.BuildError(503, "websocket proxy disabled and no fallback available"))
			return
		}

		// 5) Build and publish RPC payload
		datasourceID := c.GetHeader("X-NB-Datasource-ID")
		var payload []byte
		if agentType == "proxy" && datasourceID != "" {
			unified := map[string]any{
				"request_id":    reqID,
				"datasource_id": datasourceID,
				"action":        "http_request",
				"method":        httpReq.Method,
				"url":           httpReq.URL,
				"header":        httpReq.Header,
				"body":          httpReq.Body,
			}
			payload, err = json.Marshal(unified)
		} else {
			payload, err = json.Marshal(httpReq)
		}
		if err != nil {
			logger.Error("failed to marshal HTTP request", "err", err)
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "failed to serialize request"))
			return
		}

		// Sign payload for proxy agents
		if agentType == "proxy" {
			payload = signPayload(payload, signer, logger)
		}

		routingKey := mq.RelayQueueName(acct, agentType)

		// 5) RPC call with timeout
		ctx, cancel := context.WithTimeout(c.Request.Context(), cfg.HTTP.WriteTimeout)
		defer cancel()

		logger.Info("publishing API proxy RPC", "account", acct, "request_id", reqID, "routing_key", routingKey, "method", httpReq.Method, "url", httpReq.URL, "api_base_url", apiBaseURL)
		respBytes, err := rpcClient.Call(ctx, cfg.RabbitMQ.ExchangeName, routingKey, payload, reqID)
		if err != nil {
			logger.Error("API proxy RPC call failed", "request_id", reqID, "err", err)
			c.JSON(http.StatusGatewayTimeout, utils.BuildError(504, "timeout waiting for agent"))
			return
		}

		// 6) Unwrap AgentResponse → HTTPResponse
		var ar models.AgentResponse
		if err := json.Unmarshal(respBytes, &ar); err != nil {
			logger.Error("failed to unmarshal AgentResponse", "err", err)
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "invalid agent response"))
			return
		}
		var hrResp models.HTTPResponse
		if err := json.Unmarshal([]byte(ar.Data), &hrResp); err != nil {
			logger.Error("failed to unmarshal HTTPResponse", "err", err)
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "invalid HTTP response"))
			return
		}

		// 7) Set response status code
		w.WriteHeader(int(hrResp.StatusCode))

		// 8) Propagate headers with support for multi-value headers
		// HTTPResponse.Header is now map[string][]string to properly support multiple header values
		// (e.g., Set-Cookie, Accept, etc.) which is required for HTTP compliance
		for k, values := range hrResp.Header {
			for _, v := range values {
				c.Header(k, v)
			}
		}

		// 9) Decode and write body
		bodyBytes, err := base64.StdEncoding.DecodeString(hrResp.Body)
		if err != nil {
			logger.Error("failed to decode response body", "err", err)
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "invalid body encoding"))
			return
		}
		logger.Info("writing API proxy response", "status", hrResp.StatusCode, "request_id", ar.RequestID, "content_length", hrResp.ContentLength)
		_, err = w.Write(bodyBytes)
		if err != nil {
			logger.Error("failed to write response", "err", err)
		}
	}
}
