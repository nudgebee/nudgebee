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
	"nudgebee/relay-server/pkg/mq"
	"nudgebee/relay-server/pkg/signing"
	"nudgebee/relay-server/pkg/utils"
)

// NewResyncInventoryHandler creates a handler that triggers a connected agent
// to re-send its datasource inventory. POST /proxy/resync-inventory
func NewResyncInventoryHandler(
	store db.AgentStore,
	rpcClient mq.RPCClient,
	topo mq.TenantEnsurer,
	cfg *config.Config,
	signer *signing.Signer,
	tracer *trace.Tracer,
	meter *metric.Meter,
	rootLogger *slog.Logger,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		var req struct {
			AccountID string `json:"account_id" binding:"required"`
		}
		if err := c.BindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, utils.BuildError(400, "invalid JSON: account_id required"))
			return
		}

		c.Set("accountID", req.AccountID)
		logger, _, _ := utils.BuildContextFromPayload(c, tracer, meter, rootLogger)

		connected, err := store.IsAgentConnected(ctx, req.AccountID, db.AgentTypeProxy)
		if err != nil {
			logger.Error("failed to check proxy agent connection", "account_id", req.AccountID, "err", err)
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "internal server error"))
			return
		}
		if !connected {
			c.JSON(http.StatusBadRequest, utils.BuildError(400, "proxy agent not connected"))
			return
		}

		if err := topo.EnsureTenantForAgentType(ctx, req.AccountID, db.AgentTypeProxy); err != nil {
			logger.Error("failed to ensure proxy topology", "account_id", req.AccountID, "err", err)
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "topology error"))
			return
		}

		routingKey := mq.RelayQueueName(req.AccountID, db.AgentTypeProxy)
		reqID := uuid.NewString()

		resyncMsg := map[string]any{
			"action":     "resync_inventory",
			"request_id": reqID,
		}

		payload, err := json.Marshal(resyncMsg)
		if err != nil {
			logger.Error("failed to marshal resync message", "err", err)
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "serialization error"))
			return
		}

		payload = signPayload(payload, signer, logger)

		ctx, cancel := context.WithTimeout(ctx, cfg.HTTP.WriteTimeout)
		defer cancel()

		logger.Info("sending resync_inventory to proxy agent",
			"account_id", req.AccountID,
			"request_id", reqID,
		)

		resp, err := rpcClient.Call(ctx, cfg.RabbitMQ.ExchangeName, routingKey, payload, reqID)
		if err != nil {
			logger.Error("resync inventory RPC failed", "account_id", req.AccountID, "err", err)
			c.JSON(http.StatusGatewayTimeout, utils.BuildError(504, "timeout contacting agent"))
			return
		}

		c.Data(http.StatusOK, "application/json", resp)
	}
}
