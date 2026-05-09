package observability

import (
	"encoding/json"
	"log/slog"
	"maps"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

const logLabelsCacheNamespace = "nb_log_labels"
const logLabelsCacheTTL = 10 * time.Minute
const tenantCacheKeyPrefix = "t:"

func init() {
	common.CacheCreateNamespace(
		logLabelsCacheNamespace,
		common.CacheNamespaceWithExpiration(logLabelsCacheTTL),
	)
}

// getCustomLogLabels fetches user-configured log label overrides from
// cloud_account_attrs (name='log_labels') for the given account.
// Returns only non-empty label entries; skips 'defaultQuery'.
// Returns empty map on any error (graceful degradation).
func getCustomLogLabels(ctx *security.RequestContext, accountId string) map[string]string {
	if accountId == "" {
		return map[string]string{}
	}

	// Cache check
	if cached, ok := common.CacheGet(logLabelsCacheNamespace, accountId); ok {
		var m map[string]string
		if err := json.Unmarshal(cached, &m); err != nil {
			slog.Warn("getCustomLogLabels: failed to unmarshal cached log_labels", "account_id", accountId, "error", err)
		} else {
			return m
		}
	}

	// DB fetch
	dbMgr, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Warn("getCustomLogLabels: failed to get db manager", "error", err)
		return map[string]string{}
	}

	var rawValue string
	err = dbMgr.Db.QueryRowx(
		`SELECT value FROM cloud_account_attrs WHERE cloud_account_id = $1 AND name = 'log_labels'`,
		accountId,
	).Scan(&rawValue)
	if err != nil {
		// no row or DB error — not a fatal condition, fall back to static mapping
		slog.Debug("getCustomLogLabels: no log_labels found", "account_id", accountId, "error", err)
		return map[string]string{}
	}

	// Parse JSON: {"pod":"...", "namespace":"...", "app":"...", "defaultQuery":"..."}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(rawValue), &parsed); err != nil {
		slog.Warn("getCustomLogLabels: invalid JSON in log_labels", "account_id", accountId, "error", err)
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
		if err := common.CacheSet(logLabelsCacheNamespace, accountId, b); err != nil {
			slog.Warn("getCustomLogLabels: failed to cache log_labels", "account_id", accountId, "error", err)
		}
	}

	return result
}

// getTenantLogLabels fetches tenant-wide log label overrides from
// tenant_attrs (name='log_labels') for the given tenant.
// These act as defaults for all accounts under the tenant.
// Returns only non-empty label entries; skips 'defaultQuery'.
// Returns empty map on any error (graceful degradation).
func getTenantLogLabels(ctx *security.RequestContext, tenantId string) map[string]string {
	if tenantId == "" {
		return map[string]string{}
	}
	cacheKey := tenantCacheKeyPrefix + tenantId

	// Cache check
	if cached, ok := common.CacheGet(logLabelsCacheNamespace, cacheKey); ok {
		var m map[string]string
		if err := json.Unmarshal(cached, &m); err != nil {
			slog.Warn("getTenantLogLabels: failed to unmarshal cached log_labels", "tenant_id", tenantId, "error", err)
		} else {
			return m
		}
	}

	// DB fetch
	dbMgr, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Warn("getTenantLogLabels: failed to get db manager", "error", err)
		return map[string]string{}
	}

	var rawValue string
	err = dbMgr.Db.QueryRowx(
		`SELECT value FROM tenant_attrs WHERE tenant_id = $1 AND name = 'log_labels'`,
		tenantId,
	).Scan(&rawValue)
	if err != nil {
		// no row or DB error — not a fatal condition
		slog.Debug("getTenantLogLabels: no log_labels found", "tenant_id", tenantId, "error", err)
		return map[string]string{}
	}

	// Parse JSON: {"pod":"...", "namespace":"...", "app":"...", "defaultQuery":"..."}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(rawValue), &parsed); err != nil {
		slog.Warn("getTenantLogLabels: invalid JSON in log_labels", "tenant_id", tenantId, "error", err)
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
		if err := common.CacheSet(logLabelsCacheNamespace, cacheKey, b); err != nil {
			slog.Warn("getTenantLogLabels: failed to cache log_labels", "tenant_id", tenantId, "error", err)
		}
	}

	return result
}

// getMergedLabelMapping returns the provider's static label mapping merged with
// tenant-wide and account-specific overrides from DB.
// Precedence (highest → lowest): account > tenant > static provider map.
func getMergedLabelMapping(ctx *security.RequestContext, accountId string, source LogSource) map[string]string {
	staticMap := source.GetLabelMapping()
	tenantId := ctx.GetSecurityContext().GetTenantId()
	tenantMap := getTenantLogLabels(ctx, tenantId)
	accountMap := getCustomLogLabels(ctx, accountId)

	if len(tenantMap) == 0 && len(accountMap) == 0 {
		return staticMap
	}

	// Merge: static → tenant → account (account has highest precedence)
	merged := make(map[string]string, len(staticMap)+len(tenantMap)+len(accountMap))
	maps.Copy(merged, staticMap)
	maps.Copy(merged, tenantMap)
	maps.Copy(merged, accountMap)
	return merged
}
