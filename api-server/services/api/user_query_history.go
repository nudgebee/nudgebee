package api

import (
	"context"
	"log/slog"
	"time"

	"nudgebee/services/observability"
	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Bounded so a wedged metastore cannot accumulate goroutines. Generous relative
// to a single INSERT.
const userQueryHistoryWriteTimeout = 30 * time.Second

// shouldRecordUserQueryHistory decides whether a query call earns a history row.
//
// Two independent gates, because neither alone is sufficient:
//
//   - recordHistory is INTENT. /rpc/logs and /rpc/metrics are also reached by
//     polling, dashboard panels, drilldowns and log-context expansion, none of
//     which the user thinks of as "running a query".
//   - session_variables.user_id is PROVENANCE. The gateway always populates it;
//     llm-server and runbook-server post {action, input} with no
//     session_variables at all. That matters because every caller — browser
//     included — authenticates with the SAME shared X-ACTION-TOKEN, so there is
//     no per-caller identity to check. llm-server forwards the real requesting
//     user's id in x-user-id, so it would otherwise satisfy the FK and file agent
//     queries under a human. History is read account-wide rather than per-user,
//     so a leaked row is visible to everyone on the account.
func shouldRecordUserQueryHistory(recordHistory bool, sessionVariables map[string]any) bool {
	return recordHistory && sessionString(sessionVariables, "user_id") != ""
}

// recordUserQueryHistoryAsync writes one user_history row off the request path.
//
// Fire-and-forget, matching observability's other audit writers: errors are
// logged, never returned, and never surfaced to the user. That contract is why
// the pre-flight guards below exist — a row rejected inside the goroutine is
// invisible, so the predictable rejection causes are checked up front and logged
// as warnings instead.
func recordUserQueryHistoryAsync(
	c *gin.Context,
	ctx *security.RequestContext,
	row observability.UserHistoryRequest,
	tracer *trace.Tracer,
	meter *metric.Meter,
	logger *slog.Logger,
) {
	// Both guarded because this function spawns a goroutine: a panic here would be
	// raised outside Gin's recovery middleware, which only covers the request
	// goroutine. Callers today always pass a non-nil ctx (buildContextFromPayload
	// returns an error rather than a nil context), but the deref is free to make safe.
	if ctx == nil {
		return
	}
	secCtx := ctx.GetSecurityContext()
	if secCtx == nil {
		return
	}

	// Snapshot identity to plain strings BEFORE spawning, so the INSERT cannot
	// depend on context state read after the handler returned.
	userId, tenantId := secCtx.GetUserId(), secCtx.GetTenantId()

	// user_history.user_id/tenant_id/account_id are NOT NULL with FKs to
	// users/tenant/cloud_accounts, so any of these empty is a doomed INSERT
	// rather than a row. Empty userId is the normal shape for tenant-admin and
	// super-admin contexts, which is a second reason internal callers can't
	// pollute history.
	if userId == "" || tenantId == "" || row.AccountId == "" || row.Data == "" || row.Module == "" {
		logger.Warn("user_query_history: skipping unrecordable row",
			"module", row.Module,
			"has_user", userId != "",
			"has_tenant", tenantId != "",
			"has_account", row.AccountId != "",
			"has_data", row.Data != "")
		return
	}

	// module_check is an allowlist. Mirroring it in Go means a provider that
	// shipped without a migration degrades to this warning rather than a
	// swallowed constraint violation.
	if !observability.IsKnownUserHistoryModule(row.Module) {
		logger.Warn("user_query_history: module not in allowlist; add it to the module_check migration",
			"module", row.Module)
		return
	}

	// A client that disconnected mid-query did not get a query outcome.
	if reqCtx := ctx.GetContext(); reqCtx != nil && reqCtx.Err() != nil {
		return
	}

	base := context.Background()
	if c != nil && c.Request != nil {
		base = c.Request.Context()
	}
	// WithoutCancel so the write survives the handler returning — buildContextFromPayload
	// wires the request context to c.Request.Context(), which net/http cancels at
	// that point. WithTimeout so the goroutine is bounded.
	detachedCtx, cancel := context.WithTimeout(context.WithoutCancel(base), userQueryHistoryWriteTimeout)
	go func() {
		defer cancel()
		// An unrecovered panic in a spawned goroutine takes down the process;
		// Gin's recovery middleware only guards the request goroutine.
		defer func() {
			if r := recover(); r != nil {
				logger.Error("user_query_history: panic writing row", "panic", r, "module", row.Module)
			}
		}()
		taskCtx := security.NewRequestContext(detachedCtx, secCtx, logger, tracer, meter)
		if _, err := observability.SaveUserHistoryForUser(taskCtx, userId, tenantId, row); err != nil {
			logger.Error("user_query_history: insert failed",
				"error", err, "module", row.Module, "account_id", row.AccountId, "status", row.Status)
		}
	}()
}
