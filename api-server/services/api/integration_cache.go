package api

import (
	"context"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/observability"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"strings"
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
	requestIntegrationKBSync(ctx)
	refreshProviderStatusAsync(ctx, accountIds)
}

// llmServerTask is a message on llm-server's task queue: work one llm-server
// replica does once. It MUST stay in sync with llm-server's consumer
// (llm/llm-server/api/llm_server_tasks_mq.go).
type llmServerTask struct {
	Type   string   `json:"type"`
	Keys   []string `json:"keys,omitempty"`
	Sender string   `json:"sender,omitempty"`
}

// llmTaskSyncIntegrationKBs asks llm-server to kick the knowledge-base sync
// for these tenants. Keys: tenant ids.
const llmTaskSyncIntegrationKBs = "sync_integration_kbs"

// requestIntegrationKBSync asks llm-server, once (one replica takes the task),
// to kick the knowledge-base sync for the tenant, so a freshly saved
// integration with sync_knowledge_base=true starts indexing within seconds
// instead of at llm-server's next periodic sync. The sync is per tenant, so
// one task covers every account the change touched. The cache drops for the
// same change go to every replica as the integration_changed replica broadcast.
//
// Best-effort and off the request path: a lost task only means waiting for
// the periodic sync. WithoutCancel keeps the trace (context values) but not
// the request's cancellation, which fires once the response completes.
func requestIntegrationKBSync(ctx *security.RequestContext) {
	secCtx := ctx.GetSecurityContext()
	if secCtx == nil || secCtx.GetTenantId() == "" {
		return
	}
	task := llmServerTask{
		Type:   llmTaskSyncIntegrationKBs,
		Keys:   []string{secCtx.GetTenantId()},
		Sender: "services-server/" + config.Config.ServerName,
	}
	detached := context.WithoutCancel(ctx.GetContext())
	logger := ctx.GetLogger()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("integrations: llm-server task publish panicked", "type", task.Type, "panic", r)
			}
		}()
		if err := common.MqPublish(config.Config.RabbitMqLlmServerTasksExchange, config.Config.RabbitMqLlmServerTasksQueue, task,
			common.MqPublishWithExpiration(time.Hour), common.MqPublishWithContext(detached)); err != nil {
			logger.Warn("integrations: failed to publish llm-server task (best-effort)", "type", task.Type, "error", err)
		}
	}()
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
// why this is centralised: 159bb506c9 introduced the llm-server fanout (since replaced
// by the integration_changed replica broadcast, #40807), c654fbd459
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
	// Reaches every replica of every service that caches integration state,
	// llm-server included (#40807).
	common.ReplicaBroadcast(ctx.GetContext(), common.ChangeIntegration, accountIds...)
	for _, accId := range accountIds {
		observability.InvalidateDefaultLogFiltersCache(accId)
		observability.InvalidateDefaultTraceFiltersCache(accId)
		observability.InvalidateLogLabelMappingsCache(accId)
		observability.InvalidateTraceLabelMappingsCache(accId)
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

// llmTenantWideAccounts widens an LLM change to every account in the tenant: a
// tenant-wide integration or the tenant default reaches accounts that are not
// linked to it, so clearing only the linked ones would leave the rest stale.
func llmTenantWideAccounts(ctx *security.RequestContext, integrationName string, accountIds []string) []string {
	if !strings.EqualFold(integrationName, "llm") {
		return accountIds
	}
	ids, err := tenant.GetAccountIdsForTenant(ctx, ctx.GetSecurityContext().GetTenantId())
	if err != nil {
		ctx.GetLogger().Error("integrations: failed to list tenant accounts for LLM cache clear", "error", err)
		return accountIds
	}
	return mergeAccountIds(accountIds, ids)
}
