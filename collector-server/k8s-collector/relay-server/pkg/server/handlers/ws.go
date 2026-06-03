// pkg/server/handlers/ws.go
package handlers

import (
	"context"
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
	rabbitmq "nudgebee/relay-server/pkg/mq"
	"nudgebee/relay-server/pkg/server/middleware"
	"nudgebee/relay-server/pkg/utils"
)

func WSHandler(
	store db.AgentStore,
	rpcClient rabbitmq.RPCClient,
	cfg *config.Config,
	roottracer *trace.Tracer,
	rootmeter *metric.Meter,
	rootLogger *slog.Logger,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		// bind & validate
		var req models.InteractiveShellRequest
		if err := c.BindJSON(&req); err != nil {
			c.JSON(400, utils.BuildError(400, "invalid JSON"))
			return
		}
		acct := req.AccountID
		if acct == "" {
			c.JSON(400, utils.BuildError(400, "missing account_id"))
			return
		}
		c.Set(middleware.CtxAccountID, acct)

		logger, _, _ := utils.BuildContextFromPayload(c, roottracer, rootmeter, rootLogger)

		// Interactive shell is always k8s agent
		agentType := "k8s"

		status, err := store.IsAgentConnected(ctx, acct, agentType)
		if err != nil {
			logger.Error("failed to check agent connection", "account_id", acct, "err", err)
			c.JSON(500, utils.BuildError(500, "internal server error"))
			return
		}
		if !status {
			logger.Info("agent not connected", "account", acct)
			c.JSON(400, utils.BuildError(400, "agent not connected"))
			return
		}

		allowed, fallback, err := store.IsWSEnabled(ctx, acct, agentType)
		if err != nil {
			c.JSON(500, utils.BuildError(500, "account check failed"))
			return
		}

		if !allowed {
			utils.FallbackWS(c, *logger, fallback, req)
			return
		}

		// 5) Build and publish RPC payload
		reqID := req.RequestID
		if reqID == "" {
			reqID = uuid.NewString()
			req.RequestID = reqID
		}

		payload, _ := json.Marshal(req)
		routingKey := rabbitmq.RelayQueueName(acct, agentType)

		// 6) RPC call with timeout
		ctx, cancel := context.WithTimeout(c.Request.Context(), cfg.HTTP.WriteTimeout)
		defer cancel()
		respBytes, err := rpcClient.Call(ctx, cfg.RabbitMQ.ExchangeName, routingKey, payload, reqID)
		if err != nil {
			logger.Error("failed to call agent HTTP for interactive shell", "request_id", reqID, "error", err)
			c.JSON(http.StatusGatewayTimeout, utils.BuildError(504, "timeout waiting for agent"))
			return
		}

		var ar models.AgentResponse
		if err := json.Unmarshal(respBytes, &ar); err != nil {
			logger.Error("failed to unmarshal AgentResponse", "request_id", reqID, "error", err)
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "invalid agent response"))
			return
		}
		_, err = c.Writer.Write([]byte(ar.Data))
		if err != nil {
			logger.Error("failed to write response body", "request_id", reqID, "error", err)
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "failed to send response"))
			return
		}
	}
}
