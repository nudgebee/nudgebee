package api

import (
	"context"
	"nudgebee/services/llm"
	"nudgebee/services/observability"
	"nudgebee/services/security"
	"time"
)

// providerStatusRefreshTimeout bounds the post-save provider re-check. It probes at
// most three backends, each capped by its own integration timeout (15s for Loki),
// so this is a backstop against a hung probe leaking the goroutine, not a budget.
const providerStatusRefreshTimeout = 2 * time.Minute

// onIntegrationConfigChanged is the single hook for everything that must happen after
// an integration mutation lands. Handlers call this, not the individual steps below —
// see the invalidateIntegrationCaches comment for what hand-maintained call sites
// cost us the last four times.
//
// Empty accountIds is a no-op, and ctx is only dereferenced past that check: a
// tenant-scoped integration (llm_gateway, ticketing) legitimately has no accounts.
func onIntegrationConfigChanged(ctx *security.RequestContext, accountIds []string) {
	if len(accountIds) == 0 {
		return
	}
	invalidateIntegrationCaches(ctx, accountIds)
	refreshProviderStatusAsync(ctx, accountIds)
}

// refreshProviderStatusAsync re-checks which provider serves each signal for these
// accounts, and probes the non-agent ones, so the Agent Details page reflects a
// just-connected (or just-removed) backend instead of waiting up to a cron interval.
//
// Detached and best-effort: it runs external HTTP probes, so it must not sit in the
// request path, and the caller has already responded. The request context is
// cancelled the moment that response completes, hence WithoutCancel.
func refreshProviderStatusAsync(ctx *security.RequestContext, accountIds []string) {
	if len(accountIds) == 0 {
		return
	}
	detached, cancel := context.WithTimeout(
		context.WithoutCancel(ctx.GetContext()),
		providerStatusRefreshTimeout,
	)
	bgCtx := security.NewRequestContext(detached, ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
	go func() {
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				bgCtx.GetLogger().Error("integrations: provider status refresh panicked", "panic", r)
			}
		}()
		if err := observability.RefreshProviderStatus(bgCtx, accountIds); err != nil {
			bgCtx.GetLogger().Warn("integrations: provider status refresh failed (best-effort)", "error", err)
		}
	}()
}

// invalidateIntegrationCaches drops every integration-derived cache for the given
// accounts. It is the single place that list lives: an integration mutation changes
// what these caches describe, and each cache added at its own call site is a cache
// somebody forgets at the next one.
//
// ADD NEW INTEGRATION-DERIVED CACHES HERE, not in the action handlers. History for
// why this is centralised: 159bb506c9 introduced the llm-server fanout, c654fbd459
// added the default-log-filters cache alongside it at two hand-maintained call sites,
// e5ad9a52d6 added the FinOps account context, the log-label-mappings cache landed at
// those same two sites again, and each round left another mutation path serving stale
// data until its TTL.
//
// Empty accountIds is a no-op — both invalidators already treat it that way, and a
// tenant-scoped integration (llm_gateway, ticketing) legitimately has no accounts.
func invalidateIntegrationCaches(ctx *security.RequestContext, accountIds []string) {
	if len(accountIds) == 0 {
		return
	}
	llm.InvalidateLLMServerCacheForAccounts(ctx, accountIds)
	for _, accId := range accountIds {
		observability.InvalidateDefaultLogFiltersCache(accId)
		observability.InvalidateDefaultTraceFiltersCache(accId)
		observability.InvalidateLogLabelMappingsCache(accId)
	}
}

// mergeAccountIds returns the de-duplicated union of requested and affected,
// preserving first-seen order and dropping empties.
//
// requested is what the mutation was asked to touch; affected is what it touched as
// a side effect — chiefly the accounts an update unlinked, which are by definition
// absent from the request and are exactly the ones whose cached provider resolution
// just became wrong.
func mergeAccountIds(requested, affected []string) []string {
	seen := make(map[string]struct{}, len(requested)+len(affected))
	merged := make([]string, 0, len(requested)+len(affected))
	for _, list := range [][]string{requested, affected} {
		for _, id := range list {
			if id == "" {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			merged = append(merged, id)
		}
	}
	return merged
}
