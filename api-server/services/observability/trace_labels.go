package observability

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
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

// getMergedTraceLabelMapping returns the trace provider's static label mapping merged
// with tenant-wide, account-specific, and (optionally) integration-dynamic overrides.
// Precedence (highest → lowest): dynamic (integration config) > account > tenant > static.
// Mirrors getMergedLabelMapping (log_labels.go) and reuses the source-agnostic
// DynamicLabelMappingSource interface defined there.
func getMergedTraceLabelMapping(ctx *security.RequestContext, accountId string, source TraceSource) map[string]string {
	staticMap := source.GetLabelMapping()
	tenantId := ctx.GetSecurityContext().GetTenantId()
	tenantMap := getTenantTraceLabels(ctx, tenantId)
	accountMap := getCustomTraceLabels(ctx, accountId)

	var dynamicMap map[string]string
	if dyn, ok := source.(DynamicLabelMappingSource); ok {
		dynamicMap = dyn.GetDynamicLabelMapping(ctx, accountId)
	}

	if len(tenantMap) == 0 && len(accountMap) == 0 && len(dynamicMap) == 0 {
		return staticMap
	}

	merged := make(map[string]string, len(staticMap)+len(tenantMap)+len(accountMap)+len(dynamicMap))
	maps.Copy(merged, staticMap)
	maps.Copy(merged, tenantMap)
	maps.Copy(merged, accountMap)
	maps.Copy(merged, dynamicMap)
	return merged
}
