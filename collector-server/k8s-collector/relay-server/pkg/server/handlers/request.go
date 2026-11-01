package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"nudgebee/relay-server/pkg/config"
	"nudgebee/relay-server/pkg/db"
	"nudgebee/relay-server/pkg/models"
	"nudgebee/relay-server/pkg/mq"
	"nudgebee/relay-server/pkg/server/metrics"
	"nudgebee/relay-server/pkg/server/middleware"
	"nudgebee/relay-server/pkg/signing"
	"nudgebee/relay-server/pkg/utils"
)

func NewRequestHandler(
	store db.AgentStore,
	rpcClient mq.RPCClient,
	topo mq.TenantEnsurer,
	prober mq.ConsumerProber,
	cfg *config.Config,
	signer *signing.Signer,
	roottracer *trace.Tracer,
	rootmeter *metric.Meter,
	rootLogger *slog.Logger,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		ctx := c.Request.Context()

		// Zero-copy: read raw body
		rawBody, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(400, utils.BuildError(400, "failed to read request body"))
			return
		}

		// Basic JSON validation
		if !gjson.ValidBytes(rawBody) {
			c.JSON(400, utils.BuildError(400, "invalid JSON"))
			return
		}

		// Fast JSON parsing - extract only needed fields
		// Support both legacy format (body.account_id) and new unified format (account_id)
		accountID := gjson.GetBytes(rawBody, "body.account_id").String()
		if accountID == "" {
			accountID = gjson.GetBytes(rawBody, "account_id").String()
		}
		if accountID == "" {
			c.JSON(400, utils.BuildError(400, "missing account_id"))
			return
		}

		requestID := gjson.GetBytes(rawBody, "request_id").String()
		// Support both legacy format (body.action_name) and new unified format (action)
		actionName := gjson.GetBytes(rawBody, "body.action_name").String()
		if actionName == "" {
			actionName = gjson.GetBytes(rawBody, "action").String()
		}

		// Determine agent type from header (default: k8s for backward compat)
		agentType := c.GetHeader("X-NB-Agent-Type")
		if agentType == "" {
			agentType = "k8s"
		}

		c.Set(middleware.CtxAccountID, accountID)

		logger, _, _ := utils.BuildContextFromPayload(c, roottracer, rootmeter, rootLogger)

		// Direct MCP: handle locally without agent routing
		if handleDirectMCP(c, rawBody, logger, start, accountID) {
			return
		}

		// Single DB call to get all agent status info
		connected, wsEnabled, fallbackURL, prometheusAdditionalLabel, err := store.GetAgentStatus(ctx, accountID, agentType)
		if err != nil {
			logger.Error("failed to check agent status", "account_id", accountID, "err", err)
			c.JSON(500, utils.BuildError(500, "internal server error"))
			if metrics.AsyncMetricsInstance != nil {
				metrics.AsyncMetricsInstance.IncWSRequestErrors(metrics.AttrAccount(accountID))
			}
			return
		}
		if !connected {
			logger.Info("agent not connected", "account", accountID)
			c.JSON(http.StatusServiceUnavailable, utils.BuildError(http.StatusServiceUnavailable, "agent not connected"))
			return
		}

		defer func() {
			// Async metrics - non-blocking
			if metrics.AsyncMetricsInstance != nil {
				metrics.AsyncMetricsInstance.RecordAgentRTT(time.Since(start).Seconds(), metrics.AttrAccount(accountID))
				metrics.AsyncMetricsInstance.RecordRequestLatency(time.Since(start).Seconds(),
					metrics.AttrAccount(accountID), attribute.String("handler", "request"), attribute.String("action", actionName))
			}
		}()

		// Add timestamp and request_id if missing (modify raw JSON in-place as []byte)
		modifiedBody := rawBody
		if requestID == "" {
			requestID = strconv.FormatInt(time.Now().UnixNano(), 10)
			modifiedBody, _ = sjson.SetBytes(modifiedBody, "request_id", requestID)
		}

		timestamp := time.Now().Unix()
		modifiedBody, _ = sjson.SetBytes(modifiedBody, "body.timestamp", timestamp)

		if actionName == "prometheus_queries_enricher" || actionName == "prometheus_enricher" || actionName == "application_stats" || actionName == "slo_generator" {
			if prometheusAdditionalLabel != "" {
				prometheusAdditionalLabel = strings.ReplaceAll(prometheusAdditionalLabel, "\"", "\\\"")
				prometheusAdditionalLabel = prometheusAdditionalLabel + " , "
			}
			modifiedBody = bytes.ReplaceAll(modifiedBody, []byte("__CLUSTER__"), []byte(prometheusAdditionalLabel))

			if actionName == "prometheus_queries_enricher" || actionName == "prometheus_enricher" {
				modifiedBody = []byte(utils.EnhancePrometheusStepCalculation(logger, string(modifiedBody)))
			}
		}
		if !wsEnabled {
			// For fallback, we need the full request object
			var req models.ExternalActionRequest
			if err := json.Unmarshal(modifiedBody, &req); err != nil {
				c.JSON(400, utils.BuildError(400, "invalid JSON for fallback"))
				return
			}
			if metrics.AsyncMetricsInstance != nil {
				metrics.AsyncMetricsInstance.IncFallbacks(metrics.AttrAccount(accountID))
			}
			utils.FallbackPost(c, *logger, fallbackURL, req)
			return
		}

		// Use modified raw body as payload (zero-copy!)
		// NOTE: all `body` mutations (body.timestamp, __CLUSTER__/prometheus
		// rewrites) are already applied above, so signing here binds the exact
		// bytes that get published.
		payload := modifiedBody

		// Sign payload for proxy agents (legacy field-extraction scheme); for
		// native k8s agents, sign the raw `body` bytes under additive relay_*
		// fields so the agent can authorize mutations. Signing is additive (old
		// agents ignore relay_*; the agent falls back to lightActions when a
		// signature is absent/unverifiable), so it's on by default — SignK8sEnabled
		// is a kill-switch, not an opt-in.
		switch {
		case agentType == "proxy":
			payload = signPayload(payload, signer, logger)
		case agentType == "k8s" && cfg.Signing.SignK8sEnabled:
			payload = signK8sPayload(payload, signer, logger)
		}

		rk := mq.RelayQueueName(accountID, agentType)
		if agentType == db.AgentTypeProxy {
			rk = proxyRoutingKey(ctx, store, prober, accountID, rawBody, logger)
		}

		// Log action execution for tracking
		logger.Info("executing action",
			"action", actionName,
			"corr_id", requestID)

		// topology should already exist from register call

		if metrics.AsyncMetricsInstance != nil {
			metrics.AsyncMetricsInstance.IncInFlight(metrics.AttrAccount(accountID))
			defer metrics.AsyncMetricsInstance.DecInFlight(metrics.AttrAccount(accountID))
		}

		ctx, cancel := context.WithTimeout(ctx, cfg.HTTP.WriteTimeout)
		defer cancel()

		// Extract once for error logging
		actionParams := gjson.GetBytes(rawBody, "body.action_params")

		logger.Info("publishing RPC", "corr", requestID, "rk", rk)
		resp, err := rpcClient.Call(ctx, cfg.RabbitMQ.ExchangeName, rk, payload, requestID)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || ctx.Err() != nil {
				logger.Error("RPC timeout", "corr", requestID, "action_name", actionName, "action_params", loggableActionParams(actionName, actionParams), "err", err)
				if metrics.AsyncMetricsInstance != nil {
					metrics.AsyncMetricsInstance.IncWSRequestTimeouts(metrics.AttrAccount(accountID))
				}
				c.JSON(504, utils.BuildError(504, "timeout waiting for agent"))
			} else {
				logger.Error("RPC error", "corr", requestID, "action_name", actionName, "action_params", loggableActionParams(actionName, actionParams), "err", err)
				if metrics.AsyncMetricsInstance != nil {
					metrics.AsyncMetricsInstance.IncWSRequestErrors(metrics.AttrAccount(accountID))
				}
				c.JSON(500, utils.BuildError(500, "RPC error"))
			}
			return
		}

		if _, err := c.Writer.Write(resp); err != nil {
			logger.Error("error writing response", "corr", requestID, "action_name", actionName, "action_params", loggableActionParams(actionName, actionParams), "err", err)
			c.JSON(500, utils.BuildError(500, "error writing response"))
			if metrics.AsyncMetricsInstance != nil {
				metrics.AsyncMetricsInstance.IncWSRequestErrors(metrics.AttrAccount(accountID))
			}
			return
		}
	}
}

// requestDatasourceIDPaths are where a proxy request may carry the addressed
// datasource: the legacy envelope (llm-server / runbook-server / api-server
// put it in body.action_params) first, then the unified top-level format.
var requestDatasourceIDPaths = []string{
	"body.action_params.datasource_id",
	"datasource_id",
	"params.datasource_id",
	"body.datasource_id",
}

func requestDatasourceID(raw []byte) string {
	for _, path := range requestDatasourceIDPaths {
		if v := gjson.GetBytes(raw, path).String(); v != "" {
			return v
		}
	}
	return ""
}

// proxyRoutingKey picks the queue for a request to a proxy agent.
//
// Several proxy agents may serve one account, each with its own datasources,
// and all of them consume the shared relay_requests_{acct}_proxy queue. A
// request published there is taken by whichever agent's session gets it
// first, which need not be the agent that has the datasource. So when the
// datasource's owner is known (the agent_id recorded at registration), the
// request goes to that agent's own queue instead.
//
// It falls back to the shared queue — the pre-existing behaviour, still
// consumed by every proxy session — whenever routing to the owner cannot be
// shown to work: no datasource in the request, no or ambiguous recorded owner
// (rows written before agent_id existed), a lookup error, or an owner queue
// with no consumer. The last case covers a rollout, where the owner is still
// connected to a relay replica that only consumes the shared queue, and an
// owner that is offline: its queue would hold the request until the TTL, while
// the shared queue lets the caller fail fast. The consumer check asks the
// broker, so it holds whichever relay replica hosts the owner's session.
func proxyRoutingKey(ctx context.Context, store db.AgentStore, prober mq.ConsumerProber, accountID string, raw []byte, logger *slog.Logger) string {
	shared := mq.RelayQueueName(accountID, db.AgentTypeProxy)

	dsID := requestDatasourceID(raw)
	if dsID == "" {
		return shared
	}
	owner, err := store.GetDatasourceAgentID(ctx, accountID, dsID)
	if err != nil {
		logger.Warn("failed to resolve datasource owner agent, using shared proxy queue",
			"account_id", accountID, "datasource_id", dsID, "err", err)
		return shared
	}
	if owner == "" {
		return shared
	}

	own := mq.RelayAgentQueueName(accountID, db.AgentTypeProxy, owner)
	if prober != nil {
		has, err := prober.HasConsumers(ctx, own)
		if err != nil {
			logger.Warn("failed to probe owner agent queue, using shared proxy queue",
				"account_id", accountID, "datasource_id", dsID, "agent_id", owner, "queue", own, "err", err)
			return shared
		}
		if !has {
			logger.Info("owner agent queue has no consumer, using shared proxy queue",
				"account_id", accountID, "datasource_id", dsID, "agent_id", owner, "queue", own)
			return shared
		}
	}
	return own
}
