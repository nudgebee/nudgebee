package observability

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/query"
	"nudgebee/services/security"
)

const traceDefaultFiltersCacheNamespace = "nb_trace_default_filters"
const traceDefaultFiltersCacheTTL = 10 * time.Minute

// defaultTraceFiltersConfigName is the integration_config_values entry that holds
// the per-account always-apply TRACE filters, configured on the trace integration
// form.
//
// Deliberately distinct from the logs-side `default_filters`: signoz, datadog,
// dynatrace, chronosphere and ES are each a single integration record serving BOTH
// logs and traces, so reusing one key would apply an operator's log filters to
// their trace queries. The two lists are also expressed in different vocabularies —
// log filters are provider-native column names, trace filters are canonical trace
// fields (see ApplyDefaultTraceFilters).
//
// Aliased from the integrations package rather than re-spelled: that package must
// also auto-allow the key on save (CreateIntegrationConfig rejects any config value
// whose name is absent from the schema), and two independent string literals would
// let the reader and the writer drift apart silently.
const defaultTraceFiltersConfigName = core.DefaultTraceFiltersConfigName

func init() {
	common.CacheCreateNamespace(
		traceDefaultFiltersCacheNamespace,
		common.CacheNamespaceWithExpiration(traceDefaultFiltersCacheTTL),
	)
}

// defaultTraceFiltersCacheKey must mirror the same (accountId, provider, source)
// triple the trace entry points resolve their actual query source with — otherwise
// an explicit provider override (e.g. querying a non-default trace integration)
// would read/cache another integration's filters instead.
func defaultTraceFiltersCacheKey(accountId, traceProvider, traceProviderSource string) string {
	return accountId + "|" + traceProvider + "|" + traceProviderSource
}

// defaultTraceFiltersAccountTag lets InvalidateDefaultTraceFiltersCache drop every
// cached entry for an account in one call, regardless of which provider/source each
// entry was cached under.
//
// The tag is prefixed rather than the bare "account:<id>" the two log caches use,
// because gocache matches invalidation tags across the whole store: a bare tag here
// would make every trace-filter invalidation also drop the log-filter and
// log-label-mapping entries for that account. Those two already collide with each
// other; a third participant is not the fix, so this one stays out of it.
func defaultTraceFiltersAccountTag(accountId string) string {
	return "trace_default_filters_account:" + accountId
}

// InvalidateDefaultTraceFiltersCache drops all cached default-trace-filter clauses
// for this account. Called from invalidateIntegrationCaches after an integration
// save/delete so a new filter applies immediately instead of waiting out the TTL.
func InvalidateDefaultTraceFiltersCache(accountId string) {
	if accountId == "" {
		return
	}
	if err := common.CacheDeleteWithTag(traceDefaultFiltersCacheNamespace, defaultTraceFiltersAccountTag(accountId)); err != nil {
		slog.Warn("InvalidateDefaultTraceFiltersCache: failed to invalidate", "account_id", accountId, "error", err)
	}
}

// ApplyDefaultTraceFilters scopes a trace request to the account's standing trace
// filters, in place. Every trace SPAN-QUERY entry point calls it; label/value
// discovery does not, matching the logs side.
//
// Two things differ from ApplyDefaultLogFilters, both deliberate:
//
//  1. Call it BEFORE convertWhereClauseWithMApping, not after. Trace filters are
//     stored as CANONICAL field names so they survive a provider change, so they
//     have to go through the same mapping every other clause does. (Log filters are
//     provider-native and are therefore applied after mapping — do not "align" the
//     two.) Injecting before the mapping also means collectWhereFieldNames sees the
//     filter, so a canonical field the provider cannot resolve is named by the
//     existing empty-result diagnosis instead of silently matching nothing, and
//     resolveExecutedTraceQuery reports the filter in the recorded query.
//
//  2. It can return an error. A request carrying a provider-native query string
//     (raw ClickHouse SQL, a Datadog query, Chronosphere JSON) has no where clause
//     to AND into — the string is executed verbatim — so a standing filter cannot
//     be honoured. Since the filter exists precisely to keep one account off
//     another environment's spans, running unscoped is the wrong failure: refuse
//     instead, and say why. Resolution itself still fails open, so an account with
//     no filter configured is never affected.
func ApplyDefaultTraceFilters(ctx *security.RequestContext, req *TracesV3Request) error {
	if req == nil || ctx == nil {
		return nil
	}
	defaults := getDefaultTraceFilters(ctx, req.AccountId, req.ProviderType, req.ProviderSource)
	return applyDefaultTraceFilters(req, defaults)
}

// applyDefaultTraceFilters is the pure half of ApplyDefaultTraceFilters, split out
// so the no-op and fail-closed contracts are testable without a database.
func applyDefaultTraceFilters(req *TracesV3Request, defaults query.QueryWhereClause) error {
	if req == nil || !hasWhereData(defaults) {
		return nil
	}
	if req.Query != "" {
		return fmt.Errorf(
			"this account has a standing trace filter (%s) configured on its trace integration, "+
				"and a raw provider query cannot be scoped to it; use the canonical trace query path "+
				"(a where clause, e.g. the traces_execute_v2 tool) so the filter can be applied",
			describeTraceFilterClause(defaults))
	}
	req.QueryRequest.Where = andWhereClause(req.QueryRequest.Where, defaults)
	return nil
}

// describeTraceFilterClause renders a standing filter clause as `key=value` pairs
// for the fail-closed error. The clause is always equality-only and built by
// buildDefaultFilterClause, so a flat sorted list is a faithful description; the
// sort keeps the message stable across Go's randomised map iteration.
func describeTraceFilterClause(clause query.QueryWhereClause) string {
	pairs := []string{}
	var walk func(c query.QueryWhereClause)
	walk = func(c query.QueryWhereClause) {
		for field, ops := range c.Binary {
			if val, ok := ops[query.Eq]; ok {
				pairs = append(pairs, fmt.Sprintf("%s=%v", field, val))
			}
		}
		for _, sub := range c.And {
			walk(sub)
		}
	}
	walk(clause)
	sort.Strings(pairs)
	return strings.Join(pairs, ", ")
}

// getDefaultTraceFilters returns the always-apply where-clause configured for this
// account on the trace integration actually used for this query (same
// provider/source the caller resolves its trace source with). Fails open (empty
// clause) on any error so a bad or absent config never blocks a trace query.
// Cached per (account, provider, source) for 10 min, tagged by account.
func getDefaultTraceFilters(ctx *security.RequestContext, accountId, traceProvider, traceProviderSource string) query.QueryWhereClause {
	if accountId == "" {
		return query.QueryWhereClause{}
	}
	cacheKey := defaultTraceFiltersCacheKey(accountId, traceProvider, traceProviderSource)

	if cached, ok := common.CacheGet(traceDefaultFiltersCacheNamespace, cacheKey); ok {
		var clause query.QueryWhereClause
		if err := json.Unmarshal(cached, &clause); err == nil {
			return clause
		}
		_ = common.CacheDelete(traceDefaultFiltersCacheNamespace, cacheKey)
	}

	clause := loadDefaultTraceFilters(ctx, accountId, traceProvider, traceProviderSource)

	if b, err := json.Marshal(clause); err == nil {
		tag := defaultTraceFiltersAccountTag(accountId)
		if err := common.CacheSet(traceDefaultFiltersCacheNamespace, cacheKey, b, common.CacheSetWithTags(tag)); err != nil {
			ctx.GetLogger().Warn("getDefaultTraceFilters: failed to cache default filters", "account_id", accountId, "error", err)
		}
	}
	return clause
}

// loadDefaultTraceFilters resolves the account's trace integration for this
// provider/source, reads its `default_trace_filters` config, and builds the clause
// for this account. Mirrors loadDefaultLogFilters.
func loadDefaultTraceFilters(ctx *security.RequestContext, accountId, traceProvider, traceProviderSource string) query.QueryWhereClause {
	provider, _, dto, err := getLogsMetricsTracesProviderWithIntegration(ctx, accountId, traceProvider, "traces", traceProviderSource)
	if err != nil || provider == "" {
		return query.QueryWhereClause{}
	}

	// The resolver DTO carries no Configs; re-list to get the populated config
	// values (same path GetPinotConfig uses).
	dtos, err := core.ListIntegrationConfigs(ctx, accountId, provider)
	if err != nil || len(dtos) == 0 {
		return query.QueryWhereClause{}
	}

	// Prefer the exact integration the resolver picked; else the first user-source one.
	var configs []core.IntegrationConfigValue
	if dto != nil {
		for _, d := range dtos {
			if d.Id == dto.Id {
				configs = d.Configs
				break
			}
		}
	}
	if configs == nil {
		for _, d := range dtos {
			if d.Source == "user" {
				configs = d.Configs
				break
			}
		}
	}

	var raw string
	for _, c := range configs {
		if c.Name == defaultTraceFiltersConfigName {
			raw = c.Value
			break
		}
	}
	if raw == "" {
		return query.QueryWhereClause{}
	}

	var entries []accountDefaultFilters
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		ctx.GetLogger().Warn("loadDefaultTraceFilters: invalid default_trace_filters JSON", "account_id", accountId, "error", err)
		return query.QueryWhereClause{}
	}

	for _, e := range entries {
		if e.AccountId == accountId {
			return buildDefaultFilterClause(e.Filters)
		}
	}
	return query.QueryWhereClause{}
}
