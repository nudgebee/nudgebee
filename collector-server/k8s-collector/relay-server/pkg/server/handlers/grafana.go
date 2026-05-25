// pkg/server/handlers/grafana.go
package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
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

func NewGrafanaHandler(
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

		// 2) Serialize the incoming HTTP request
		httpReq, err := utils.SerializeHTTPRequest(c.Request)
		if err != nil {
			logger.Error("failed to serialize HTTP request", "err", err)
			c.JSON(http.StatusBadRequest, utils.BuildError(400, "failed to read request body"))
			return
		}

		// Add header to identify this as a Grafana request
		if httpReq.Header == nil {
			httpReq.Header = make(map[string][]string)
		}
		httpReq.Header["X-NB-Request-Type"] = []string{"Grafana"}

		// 3) WS vs HTTP fallback
		allowed, fallbackURL, err := store.IsWSEnabled(c.Request.Context(), acct, agentType)
		if err != nil {
			logger.Error("failed to check WS permission", "account_id", acct, "err", err)
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "internal server error"))
			return
		}
		if !allowed {
			logger.Info("WS disabled for grafana proxy, falling back", "account", acct, "url", fallbackURL)
			utils.FallbackGrafana(c, logger, fallbackURL, *httpReq)
			return
		}

		// topology should already exist from register call

		// 5) Build and publish RPC payload
		reqID := c.GetHeader("X-NB-REQUEST-ID")
		if reqID == "" {
			reqID = uuid.NewString()
		}

		// Wrap in unified format for proxy agents
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
			payload, _ = json.Marshal(unified)
		} else {
			payload, _ = json.Marshal(httpReq)
		}

		// Sign payload for proxy agents
		if agentType == "proxy" {
			payload = signPayload(payload, signer, logger)
		}

		routingKey := mq.RelayQueueName(acct, agentType)

		// 6) RPC call with timeout
		ctx, cancel := context.WithTimeout(c.Request.Context(), cfg.HTTP.WriteTimeout)
		defer cancel()

		logger.Info("publishing Grafana RPC", "account", acct, "request_id", reqID, "routing_key", routingKey)
		respBytes, err := rpcClient.Call(ctx, cfg.RabbitMQ.ExchangeName, routingKey, payload, reqID)
		if err != nil {
			logger.Error("Grafana RPC call failed", "request_id", reqID, "err", err)
			c.JSON(http.StatusGatewayTimeout, utils.BuildError(504, "timeout waiting for agent"))
			return
		}

		// 7) Unwrap AgentResponse → HTTPResponse
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
		logger.Info("writing Grafana proxy response", "status", ar.StatusCode, "request_id", ar.RequestID)
		_, err = w.Write(bodyBytes)
		if err != nil {
			log.Printf("Failed to write response %f", err)
		}
	}
}
