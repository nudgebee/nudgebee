package api

import (
	"context"
	"errors"
	"log/slog"
	"nudgebee/services/agentconnect"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/recommendation"
	"nudgebee/services/security"
	"nudgebee/services/spend"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// buildContextForAuditActor builds the RequestContext used to persist a single
// relayed audit. The /v1/audit endpoint is a service-to-service ingest pipe whose
// payload carries each audit's own actor, so the context is derived from that
// actor (NOT a blanket super-admin). This matters: CreateAudit skips super-admin
// contexts, and the prior super-admin context here silently dropped every relayed
// audit (notification rules, messaging-platform config, llm/runbook).
func buildContextForAuditActor(c *gin.Context, tenantId string, userId string, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) *security.RequestContext {
	// trace_id/span_id are stamped by NewRequestContext from the request context.
	childLogger := logger.With("service", "audit")
	return security.NewRequestContext(c.Request.Context(), security.NewSecurityContextForAuditRelay(tenantId, userId), childLogger, tracer, meter)
}

func handleAuditApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	groupV2 := r.Group("/v1/audit")
	groupV2.POST("", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "audit")
		var actionPayload audit.AuditRequest
		err := c.ShouldBindJSON(&actionPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "audit", "invalid_json")
			logger.Error("audit: error binding request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if len(actionPayload.Audits) == 0 {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "audit", "invalid_json")
			c.JSON(400, common.ErrorActionBadRequest("audit: audits is required"))
			return
		}

		// Persist each audit under a context derived from its own actor so the
		// super-admin skip is evaluated against the real actor, not the relay.
		var errs []error
		for i := range actionPayload.Audits {
			a := actionPayload.Audits[i]
			// An audit with no tenant can't be attributed or surfaced in any
			// tenant-scoped view; skip it rather than persist an orphan row.
			if a.TenantId == "" {
				logger.Warn("audit: skipping relayed audit with empty tenant_id", "event_type", a.EventType)
				continue
			}
			actorCtx := buildContextForAuditActor(c, a.TenantId, a.UserId, tracer, meter, logger)
			if err := audit.CreateAudit(actorCtx, &audit.AuditRequest{Audits: []audit.Audit{a}}); err != nil {
				errs = append(errs, err)
				continue
			}
			// On a K8s agent's first connect, kick off the server-side
			// recommendation scans so a freshly onboarded cluster populates
			// without waiting for the daily refresh crons. Relay emits this
			// event only on the NOT_CONNECTED->CONNECTED transition, and
			// TriggerInitialScans is idempotent, so reconnects don't re-run it.
			if a.EventType == audit.EventTypeK8sRelayAgentConnected && a.AccountId != "" {
				onAgentConnected(c, a.AccountId, a.TenantId, tracer, meter, logger)
			}
		}
		if len(errs) > 0 {
			joined := errors.Join(errs...)
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "audit", "create_audit_failed")
			logger.Error("audit: error creating audit", "error", joined)
			c.JSON(400, common.ErrorActionBadRequest(joined.Error()))
			return
		}

		c.JSON(200, gin.H{"status": "ok"})
	})
}

// agentConnectPublishTimeout bounds the wait for the broker's confirm, which
// would otherwise run to MqPublish's own timeout on every retry. It does not cut
// short the sleeps between retries, so a broker that is down still takes a few
// seconds longer than this before the work falls back to this pod.
const agentConnectPublishTimeout = 15 * time.Second

// onAgentConnected hands an agent's first-connect work — the initial scans and a
// one-off server-side OpenCost sync, so a freshly onboarded cluster stamps
// opencostServerSide instead of showing "disconnected" until the 6-hourly cron —
// to the worker deployment through the agent_connect queue. If the publish fails,
// or the queue is switched off, the work runs on this pod as it did before the
// queue existed, rather than being dropped.
//
// The publish runs off the request path, so a slow or unreachable broker never
// holds up the /v1/audit response, and on a context detached from the request,
// so relay hanging up can't fail a publish the broker has already taken.
func onAgentConnected(c *gin.Context, accountId, tenantId string, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	ctx := context.WithoutCancel(c.Request.Context())
	if !config.Config.FeatureAgentConnectQueueEnabled {
		triggerInitialScansAsync(ctx, accountId, tenantId, tracer, meter, logger)
		triggerServerSideOpenCostAsync(ctx, accountId, tenantId, tracer, meter, logger)
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("audit: panic queueing first-connect work", "panic", r, "account_id", accountId)
			}
		}()
		publishCtx, cancel := context.WithTimeout(ctx, agentConnectPublishTimeout)
		err := agentconnect.Publish(publishCtx, accountId, tenantId)
		cancel()
		if err == nil {
			return
		}
		logger.Warn("audit: could not queue first-connect work, running it on this pod",
			"error", err, "account_id", accountId, "tenant_id", tenantId)
		triggerInitialScansAsync(ctx, accountId, tenantId, tracer, meter, logger)
		triggerServerSideOpenCostAsync(ctx, accountId, tenantId, tracer, meter, logger)
	}()
}

// triggerInitialScansAsync fires the first-connect recommendation scans for an
// account in a detached goroutine so the /v1/audit ingest response stays fast
// and is unaffected if scan dispatch errors. Work is bounded by an absolute
// timeout and recovers from panics.
func triggerInitialScansAsync(ctx context.Context, accountId, tenantId string, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	secCtx := security.NewSecurityContextForTenantAdmin(tenantId)
	if secCtx == nil {
		logger.Error("audit: nil tenant-admin context for initial scans", "account_id", accountId, "tenant_id", tenantId)
		return
	}
	// Detach from the request context (cancelled when the handler returns) but
	// bound the work so a hung run can't leak the goroutine.
	detachedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Minute)
	go func() {
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				logger.Error("audit: panic triggering initial scans", "panic", r, "account_id", accountId)
			}
		}()
		taskCtx := security.NewRequestContext(detachedCtx, secCtx, logger, tracer, meter)
		if err := recommendation.TriggerInitialScans(taskCtx, accountId, tenantId); err != nil {
			logger.Error("audit: error triggering initial scans", "error", err, "account_id", accountId)
		}
	}()
}

// triggerServerSideOpenCostAsync kicks a one-off server-side OpenCost sync for a
// just-connected K8s cluster. It delegates to spend.TriggerServerSideOnConnect,
// which guards on the opencostServerSide marker being unset (so reconnects of an
// already-stamped cluster no-op) and runs the sync on its own detached,
// timeout-bounded context — keeping the /v1/audit ingest response fast.
func triggerServerSideOpenCostAsync(ctx context.Context, accountId, tenantId string, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	secCtx := security.NewSecurityContextForTenantAdmin(tenantId)
	if secCtx == nil {
		logger.Error("audit: nil tenant-admin context for server-side opencost", "account_id", accountId, "tenant_id", tenantId)
		return
	}
	spend.TriggerServerSideOnConnect(security.NewRequestContext(ctx, secCtx, logger, tracer, meter), accountId, tenantId)
}
