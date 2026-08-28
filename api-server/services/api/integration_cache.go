package api

import (
	"nudgebee/services/llm"
	"nudgebee/services/observability"
	"nudgebee/services/security"
)

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
