package observability

import (
	"encoding/json"
	"log/slog"
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

// DynamicLabelMappingSource is an optional capability for log sources whose
// canonical → provider field mapping depends on the integration configuration
// rather than hard-coded constants. Implementations must accept being called
// without a configured integration and return an empty map in that case
// (never panic).
type DynamicLabelMappingSource interface {
	GetDynamicLabelMapping(ctx *security.RequestContext, accountId string) map[string]string
}

// knownCanonicalLogFields is the vocabulary the Advanced Settings panel advertises
// as mappable, so an operator can see what they COULD map and not only what some
// tier already sets.
//
// It is display metadata, nothing more: it does not gate resolution (any key an
// operator types is honoured) and it is deliberately NOT a canonical-field
// dictionary of the kind traces carry — logs have no types, descriptions or
// per-field operator derivation, and inventing half of that here would be worse than
// leaving it to the dedicated change. Sourced from the names the log pipeline
// actually builds filters from: buildWorkloadLogWhereClause ("app", "namespace"),
// autoExecuteByTraceID ("trace_id"), LogsFilterMap ("content"), and the Pinot/Hive
// column mappings.
var knownCanonicalLogFields = []string{
	"app",
	"container",
	"content",
	"level",
	"message",
	"namespace",
	"pod",
	"timestamp",
	"trace_id",
}

// labelMappingOverride replaces the integration tier with caller-supplied rows, so
// the Advanced Settings panel can preview a mapping the operator has typed but not
// yet saved.
//
// Set is load-bearing and separate from a nil/empty Mappings: Set=true with no rows
// means "the operator deleted every row, so the integration tier contributes
// nothing", which is a different answer from a nil override ("read what is saved").
// Same distinction the frontend's previouslySet rule solves on the save side.
type labelMappingOverride struct {
	Set      bool
	Mappings map[string]string
}

// resolveLogLabelMapping is the single implementation of the canonical -> provider
// field merge. getMergedLabelMapping projects it for query execution and
// GetLogLabelMapping projects it for the Advanced Settings panel, so the mapping an
// operator is shown is by construction the mapping their queries will use.
//
// Precedence is declared once, in labelMappingTierOrder. Every tier fails open to an
// empty map, so a missing integration or an unreachable DB degrades the answer
// rather than failing the query.
func resolveLogLabelMapping(ctx *security.RequestContext, accountId string,
	source LogSource, draft *labelMappingOverride) ResolvedLabelMapping {
	// The source names the integration it reads from, so nothing upstream has to
	// thread it: every log source is dispatched for exactly one (provider, source)
	// pair, and knows which.
	ref := source.ProviderRef()
	byTier := map[LabelMappingTier]map[string]string{
		LabelTierProviderDefault: source.GetLabelMapping(),
		LabelTierTenant:          getTenantLogLabels(ctx, ctx.GetSecurityContext().GetTenantId()),
		LabelTierAccount:         getCustomLogLabels(ctx, accountId),
	}

	if dyn, ok := source.(DynamicLabelMappingSource); ok {
		byTier[LabelTierProviderConfig] = dyn.GetDynamicLabelMapping(ctx, accountId)
	}

	switch {
	case draft != nil && draft.Set:
		byTier[LabelTierIntegration] = sanitizeLabelMapping(draft.Mappings)
	default:
		byTier[LabelTierIntegration] = getIntegrationLogLabels(ctx, accountId, ref)
	}

	return newResolvedLabelMapping(byTier)
}

// getMergedLabelMapping returns the canonical -> provider field map queries are
// rewritten with. Keep this body a single projection of resolveLogLabelMapping: the
// moment it re-implements any part of the merge, the Advanced Settings panel starts
// lying about what queries actually do.
func getMergedLabelMapping(ctx *security.RequestContext, accountId string, source LogSource) map[string]string {
	return resolveLogLabelMapping(ctx, accountId, source, nil).Effective
}

// InvalidateLogLabelsCacheForAccount drops the cached account-level log labels and
// every cached integration-level mapping for this account.
//
// Without this, an edit to cloud_account_attrs.log_labels took up to the 10 min TTL
// to apply — and the Advanced Settings panel would keep reporting the old answer
// right after a save, which is precisely when someone is looking at it.
func InvalidateLogLabelsCacheForAccount(accountId string) {
	if accountId == "" {
		return
	}
	if err := common.CacheDelete(logLabelsCacheNamespace, accountId); err != nil {
		slog.Warn("InvalidateLogLabelsCacheForAccount: failed to invalidate", "account_id", accountId, "error", err)
	}
	InvalidateLogLabelMappingsCache(accountId)
}

// InvalidateLogLabelsCacheForTenant drops the cached tenant-level log labels.
func InvalidateLogLabelsCacheForTenant(tenantId string) {
	if tenantId == "" {
		return
	}
	if err := common.CacheDelete(logLabelsCacheNamespace, tenantCacheKeyPrefix+tenantId); err != nil {
		slog.Warn("InvalidateLogLabelsCacheForTenant: failed to invalidate", "tenant_id", tenantId, "error", err)
	}
}
