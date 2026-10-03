package observability

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// Separate namespace from nb_log_labels so trace overrides never collide with log
// overrides. tenantCacheKeyPrefix ("t:") is shared with log_labels.go.
const traceLabelsCacheNamespace = "nb_trace_labels"
const traceLabelsCacheTTL = 10 * time.Minute

func init() {
	common.CacheCreateNamespace(
		traceLabelsCacheNamespace,
		common.CacheNamespaceWithExpiration(traceLabelsCacheTTL),
	)
}

// getCustomTraceLabels fetches user-configured trace label overrides from
// cloud_account_attrs (name='trace_labels') for the given account.
// Returns only non-empty label entries; skips 'defaultQuery'.
// Returns empty map on any error (graceful degradation).
func getCustomTraceLabels(ctx *security.RequestContext, accountId string) map[string]string {
	if accountId == "" {
		return map[string]string{}
	}

	// Cache check
	if cached, ok := common.CacheGet(traceLabelsCacheNamespace, accountId); ok {
		var m map[string]string
		if err := json.Unmarshal(cached, &m); err != nil {
			// Drop the corrupt entry so it repopulates correctly next request.
			slog.Warn("getCustomTraceLabels: failed to unmarshal cached trace_labels", "account_id", accountId, "error", err)
			_ = common.CacheDelete(traceLabelsCacheNamespace, accountId)
		} else {
			return m
		}
	}

	// DB fetch
	dbMgr, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Warn("getCustomTraceLabels: failed to get db manager", "error", err)
		return map[string]string{}
	}

	var rawValue string
	err = dbMgr.Db.QueryRowx(
		`SELECT value FROM cloud_account_attrs WHERE cloud_account_id = $1 AND name = 'trace_labels'`,
		accountId,
	).Scan(&rawValue)
	if err != nil {
		// No row is the common case (no override configured) — stay silent; only real
		// DB errors are worth a warning. Either way fall back to the static mapping.
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("getCustomTraceLabels: failed to query trace_labels", "account_id", accountId, "error", err)
		}
		return map[string]string{}
	}

	// Parse JSON: {"workload_name":"...", "span_name":"...", "defaultQuery":"..."}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(rawValue), &parsed); err != nil {
		slog.Warn("getCustomTraceLabels: invalid JSON in trace_labels", "account_id", accountId, "error", err)
		return map[string]string{}
	}

	// Filter: skip defaultQuery (not a label mapping) and any empty values
	result := make(map[string]string, len(parsed))
	for k, v := range parsed {
		if k == "defaultQuery" || v == "" {
			continue
		}
		result[k] = v
	}

	// Cache the filtered result
	if b, err := json.Marshal(result); err == nil {
		if err := common.CacheSet(traceLabelsCacheNamespace, accountId, b); err != nil {
			slog.Warn("getCustomTraceLabels: failed to cache trace_labels", "account_id", accountId, "error", err)
		}
	}

	return result
}

// getTenantTraceLabels fetches tenant-wide trace label overrides from
// tenant_attrs (name='trace_labels') for the given tenant.
// These act as defaults for all accounts under the tenant.
// Returns only non-empty label entries; skips 'defaultQuery'.
// Returns empty map on any error (graceful degradation).
func getTenantTraceLabels(ctx *security.RequestContext, tenantId string) map[string]string {
	if tenantId == "" {
		return map[string]string{}
	}
	cacheKey := tenantCacheKeyPrefix + tenantId

	// Cache check
	if cached, ok := common.CacheGet(traceLabelsCacheNamespace, cacheKey); ok {
		var m map[string]string
		if err := json.Unmarshal(cached, &m); err != nil {
			// Drop the corrupt entry so it repopulates correctly next request.
			slog.Warn("getTenantTraceLabels: failed to unmarshal cached trace_labels", "tenant_id", tenantId, "error", err)
			_ = common.CacheDelete(traceLabelsCacheNamespace, cacheKey)
		} else {
			return m
		}
	}

	// DB fetch
	dbMgr, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Warn("getTenantTraceLabels: failed to get db manager", "error", err)
		return map[string]string{}
	}

	var rawValue string
	err = dbMgr.Db.QueryRowx(
		`SELECT value FROM tenant_attrs WHERE tenant_id = $1 AND name = 'trace_labels'`,
		tenantId,
	).Scan(&rawValue)
	if err != nil {
		// No row is the common case (no override configured) — stay silent; only real
		// DB errors are worth a warning. Either way fall back to the static mapping.
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("getTenantTraceLabels: failed to query trace_labels", "tenant_id", tenantId, "error", err)
		}
		return map[string]string{}
	}

	// Parse JSON: {"workload_name":"...", "span_name":"...", "defaultQuery":"..."}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(rawValue), &parsed); err != nil {
		slog.Warn("getTenantTraceLabels: invalid JSON in trace_labels", "tenant_id", tenantId, "error", err)
		return map[string]string{}
	}

	// Filter: skip defaultQuery (not a label mapping) and any empty values
	result := make(map[string]string, len(parsed))
	for k, v := range parsed {
		if k == "defaultQuery" || v == "" {
			continue
		}
		result[k] = v
	}

	// Cache the filtered result
	if b, err := json.Marshal(result); err == nil {
		if err := common.CacheSet(traceLabelsCacheNamespace, cacheKey, b); err != nil {
			slog.Warn("getTenantTraceLabels: failed to cache trace_labels", "tenant_id", tenantId, "error", err)
		}
	}

	return result
}

// InvalidateTraceLabelsCacheForAccount drops the cached account-scoped trace label overrides so a
// Settings save takes effect on the next query instead of waiting out the 10-minute TTL.
// With the redis cache provider (the Helm chart and .env.example default) the entry is
// shared by every api-server pod, so without this a save is invisible fleet-wide.
//
// Plain CacheDelete rather than CacheDeleteWithTag, which InvalidateDefaultLogFiltersCache
// uses: that cache keys on an (account, provider, source) triple and needs a tag to find
// every entry. These entries are keyed on the id directly and are written by CacheSet with
// no tags at all, so a tag delete would match nothing — or, on the bigcache fallback,
// degenerate into wiping the whole namespace.
func InvalidateTraceLabelsCacheForAccount(accountId string) {
	if accountId == "" {
		return
	}
	if err := common.CacheDelete(traceLabelsCacheNamespace, accountId); err != nil {
		slog.Warn("InvalidateTraceLabelsCacheForAccount: failed to invalidate", "account_id", accountId, "error", err)
	}
	// The integration tier is per-account too, and an account-scoped save is the one
	// moment an operator is watching the panel. Mirrors InvalidateLogLabelsCacheForAccount.
	InvalidateTraceLabelMappingsCache(accountId)
}

// InvalidateTraceLabelsCacheForTenant drops the tenant-wide entry. Keyed with the same
// tenantCacheKeyPrefix getTenantTraceLabels caches under — a bare tenantId would address
// the account key space and delete the wrong entry.
func InvalidateTraceLabelsCacheForTenant(tenantId string) {
	if tenantId == "" {
		return
	}
	if err := common.CacheDelete(traceLabelsCacheNamespace, tenantCacheKeyPrefix+tenantId); err != nil {
		slog.Warn("InvalidateTraceLabelsCacheForTenant: failed to invalidate", "tenant_id", tenantId, "error", err)
	}
}

// resolveTraceLabelMapping is the single implementation of the canonical -> provider
// TRACE field merge. getMergedTraceLabelMapping projects it for query execution and
// GetTraceLabelMapping projects it for the Advanced Settings panel, so the mapping an
// operator is shown is by construction the mapping their queries will use.
//
// The mirror of resolveLogLabelMapping (log_labels.go), reusing label_mapping.go's
// tiers, order and ResolvedLabelMapping verbatim — traces need no tier logs do not
// have, so labelMappingTierOrder is unchanged.
//
// Precedence is declared once, in labelMappingTierOrder. Every tier fails open to an
// empty map, so a missing integration or an unreachable DB degrades the answer rather
// than failing the query.
//
// `ref` is the RESOLVED (provider, source) pair the caller queried with, not one
// derived from the source type. Logs can derive theirs (LogSource.ProviderRef) because
// each log source is dispatched for exactly one pair; traces cannot — resolveTraceSource
// returns ElasticSaasTraceSource *or* ElasticOtelTraceSource for the same ES/user
// integration, and gcp matches on provider alone regardless of source. Every trace call
// site already holds the resolved pair, so it is threaded instead.
func resolveTraceLabelMapping(ctx *security.RequestContext, accountId string,
	ref providerRef, source TraceSource, draft *labelMappingOverride) ResolvedLabelMapping {
	byTier := map[LabelMappingTier]map[string]string{
		LabelTierProviderDefault: source.GetLabelMapping(),
		LabelTierTenant:          getTenantTraceLabels(ctx, ctx.GetSecurityContext().GetTenantId()),
		LabelTierAccount:         getCustomTraceLabels(ctx, accountId),
	}

	// No trace source implements DynamicLabelMappingSource today, and that is the right
	// answer rather than a gap: the tier exists for integrations that carry user-typed
	// FIELD names (Pinot's pinot_pod_col, Hive's hive_message_col), and both of those
	// are log-only providers absent from getTraceSource. Trace integrations carry
	// CONTAINERS — trace_index, openobserve_trace_stream — not field names. The type
	// assertion stays so a future trace source that genuinely does carry field names
	// slots in without touching the resolver.
	if dyn, ok := source.(DynamicLabelMappingSource); ok {
		byTier[LabelTierProviderConfig] = dyn.GetDynamicLabelMapping(ctx, accountId)
	}

	switch {
	case draft != nil && draft.Set:
		byTier[LabelTierIntegration] = sanitizeLabelMapping(draft.Mappings)
	default:
		byTier[LabelTierIntegration] = getIntegrationTraceLabels(ctx, accountId, ref)
	}

	return newResolvedLabelMapping(byTier)
}

// getMergedTraceLabelMapping returns the canonical -> provider field map trace queries
// are rewritten with. Keep this body a single projection of resolveTraceLabelMapping:
// the moment it re-implements any part of the merge, the Advanced Settings panel starts
// lying about what queries actually do.
//
// Note this now always returns a FRESH map, and drops entries with an empty key or
// value (flattenLayers). The previous hand-merge returned the source's static map by
// reference on the no-override path; no caller mutates the result — they pass it to
// convertWhereClauseWithMApping, validateReferencedTraceLabels or buildTraceLabels, or
// place it in a response body — so the copy is safe as well as correct.
func getMergedTraceLabelMapping(ctx *security.RequestContext, accountId string,
	ref providerRef, source TraceSource) map[string]string {
	return resolveTraceLabelMapping(ctx, accountId, ref, source, nil).Effective
}
