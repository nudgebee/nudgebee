package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

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

// NewProxyConfigPushHandler creates a handler for pushing datasource configs to proxy agents.
// POST /proxy/config/push
// This is called by the API server when integrations are created/updated/deleted.
func NewProxyConfigPushHandler(
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

		var req models.ProxyConfigPushRequest
		if err := c.BindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, utils.BuildError(400, "invalid JSON"))
			return
		}

		if req.AccountID == "" {
			c.JSON(http.StatusBadRequest, utils.BuildError(400, "missing account_id"))
			return
		}

		c.Set("accountID", req.AccountID)
		logger, _, _ := utils.BuildContextFromPayload(c, tracer, meter, rootLogger)

		// Check if proxy agent is connected
		connected, err := store.IsAgentConnected(ctx, req.AccountID, db.AgentTypeProxy)
		if err != nil {
			logger.Error("failed to check proxy agent connection", "account_id", req.AccountID, "err", err)
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "internal server error"))
			return
		}

		if !connected {
			// Agent not connected — config is already in DB and will be pushed on reconnect
			logger.Info("proxy agent not connected, config will be pushed on reconnect", "account_id", req.AccountID)
			c.JSON(http.StatusAccepted, gin.H{"status": "queued", "message": "agent offline, config will sync on reconnect"})
			return
		}

		// Ensure topology exists for proxy agent
		if err := topo.EnsureTenantForAgentType(ctx, req.AccountID, db.AgentTypeProxy); err != nil {
			logger.Error("failed to ensure proxy topology", "account_id", req.AccountID, "err", err)
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "topology error"))
			return
		}

		routingKey := mq.RelayQueueName(req.AccountID, db.AgentTypeProxy)
		reqID := uuid.NewString()

		// Build the config push message
		pushMsg := models.DatasourceConfigPush{
			Action:      "datasource_config_sync",
			RequestID:   reqID,
			AccountID:   req.AccountID,
			Datasources: req.Datasources,
		}

		payload, err := json.Marshal(pushMsg)
		if err != nil {
			logger.Error("failed to marshal config push message", "err", err)
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "serialization error"))
			return
		}

		// Sign the payload before sending to the proxy agent
		payload = signPayload(payload, signer, logger)

		// RPC call to proxy agent with timeout
		ctx, cancel := context.WithTimeout(ctx, cfg.HTTP.WriteTimeout)
		defer cancel()

		logger.Info("pushing datasource config to proxy agent",
			"account_id", req.AccountID,
			"datasource_count", len(req.Datasources),
			"routing_key", routingKey,
			"request_id", reqID,
		)

		resp, err := rpcClient.Call(ctx, cfg.RabbitMQ.ExchangeName, routingKey, payload, reqID)
		if err != nil {
			logger.Error("config push RPC failed", "account_id", req.AccountID, "err", err)
			c.JSON(http.StatusGatewayTimeout, utils.BuildError(504, "timeout pushing config to agent"))
			return
		}

		// Return the agent's response
		c.Data(http.StatusOK, "application/json", resp)
	}
}

// NewProxyConfigTestHandler creates a handler for testing a single datasource config
// against a proxy agent before saving. POST /proxy/config/test
func NewProxyConfigTestHandler(
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

		var req models.TestDatasourceConfigRequest
		if err := c.BindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, utils.BuildError(400, "invalid JSON"))
			return
		}

		if req.AccountID == "" {
			c.JSON(http.StatusBadRequest, utils.BuildError(400, "missing account_id"))
			return
		}

		c.Set("accountID", req.AccountID)
		logger, _, _ := utils.BuildContextFromPayload(c, tracer, meter, rootLogger)

		// Check if proxy agent is connected
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

		// Ensure topology exists for proxy agent
		if err := topo.EnsureTenantForAgentType(ctx, req.AccountID, db.AgentTypeProxy); err != nil {
			logger.Error("failed to ensure proxy topology", "account_id", req.AccountID, "err", err)
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "topology error"))
			return
		}

		routingKey := mq.RelayQueueName(req.AccountID, db.AgentTypeProxy)
		reqID := uuid.NewString()

		// Build the test message
		testMsg := map[string]any{
			"action":     "test_datasource_config",
			"request_id": reqID,
			"datasource": req.Datasource,
		}

		payload, err := json.Marshal(testMsg)
		if err != nil {
			logger.Error("failed to marshal test config message", "err", err)
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "serialization error"))
			return
		}

		payload = signPayload(payload, signer, logger)

		// 20s timeout for connection test
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()

		logger.Info("testing datasource config via proxy agent",
			"account_id", req.AccountID,
			"type", req.Datasource.Type,
			"proxy_type", req.Datasource.ProxyType,
			"request_id", reqID,
		)

		resp, err := rpcClient.Call(ctx, cfg.RabbitMQ.ExchangeName, routingKey, payload, reqID)
		if err != nil {
			logger.Error("test config RPC failed", "account_id", req.AccountID, "err", err)
			c.JSON(http.StatusGatewayTimeout, utils.BuildError(504, "timeout testing datasource config"))
			return
		}

		// Parse agent response
		var agentResp models.TestDatasourceConfigResponse
		if err := json.Unmarshal(resp, &agentResp); err != nil {
			logger.Error("failed to parse agent test response", "err", err)
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "invalid agent response"))
			return
		}

		c.JSON(http.StatusOK, agentResp)
	}
}
