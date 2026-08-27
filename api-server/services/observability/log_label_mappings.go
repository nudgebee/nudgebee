package observability

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

const logLabelMappingsCacheNamespace = "nb_log_label_mappings"
const logLabelMappingsCacheTTL = 10 * time.Minute

func init() {
	common.CacheCreateNamespace(
		logLabelMappingsCacheNamespace,
		common.CacheNamespaceWithExpiration(logLabelMappingsCacheTTL),
	)
}

// logLabelMappingsCacheKey mirrors defaultLogFiltersCacheKey: the same
// (account, provider, source) triple FetchLogs resolves its query source with, so an
// explicit provider override reads its own integration's mapping rather than the
// account default's.
func logLabelMappingsCacheKey(accountId string, ref providerRef) string {
	return accountId + "|" + ref.Provider + "|" + ref.Source
}

// logLabelMappingsAccountTag lets InvalidateLogLabelMappingsCache drop every cached
// entry for an account in one call, whichever provider/source it was cached under.
func logLabelMappingsAccountTag(accountId string) string {
	return "account:" + accountId
}

// InvalidateLogLabelMappingsCache drops all cached integration-level mappings for
// this account. Call after saving or deleting a log integration so a new mapping
// applies immediately instead of waiting out the TTL.
func InvalidateLogLabelMappingsCache(accountId string) {
	if accountId == "" {
		return
	}
	if err := common.CacheDeleteWithTag(logLabelMappingsCacheNamespace, logLabelMappingsAccountTag(accountId)); err != nil {
		slog.Warn("InvalidateLogLabelMappingsCache: failed to invalidate", "account_id", accountId, "error", err)
	}
}

// getIntegrationLogLabels returns the canonical -> provider field mapping configured
// for this account on the log integration serving this query. Cached per
// (account, provider, source) for 10 min; fails open (empty map) on any error so a
// malformed blob can never break a log query.
func getIntegrationLogLabels(ctx *security.RequestContext, accountId string, ref providerRef) map[string]string {
	if accountId == "" {
		return map[string]string{}
	}
	cacheKey := logLabelMappingsCacheKey(accountId, ref)

	if cached, ok := common.CacheGet(logLabelMappingsCacheNamespace, cacheKey); ok {
		var m map[string]string
		if err := json.Unmarshal(cached, &m); err == nil {
			return m
		}
		_ = common.CacheDelete(logLabelMappingsCacheNamespace, cacheKey)
	}

	mapping := loadIntegrationLogLabels(ctx, accountId, ref)

	if b, err := json.Marshal(mapping); err == nil {
		tag := logLabelMappingsAccountTag(accountId)
		if err := common.CacheSet(logLabelMappingsCacheNamespace, cacheKey, b, common.CacheSetWithTags(tag)); err != nil {
			slog.Warn("getIntegrationLogLabels: failed to cache mapping", "account_id", accountId, "error", err)
		}
	}
	return mapping
}

// loadIntegrationLogLabels reads and parses the integration's log_label_mappings
// blob, returning the entry for this account.
func loadIntegrationLogLabels(ctx *security.RequestContext, accountId string, ref providerRef) map[string]string {
	raw := readLogIntegrationConfigValue(ctx, accountId, ref.Provider, ref.Source, core.LogLabelMappingsConfigName)
	if strings.TrimSpace(raw) == "" {
		return map[string]string{}
	}

	var entries []core.AccountLogLabelMappings
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		slog.Warn("getIntegrationLogLabels: invalid log_label_mappings JSON", "account_id", accountId, "error", err)
		return map[string]string{}
	}

	for _, e := range entries {
		if e.AccountId != accountId {
			continue
		}
		return sanitizeLabelMapping(e.Mappings)
	}
	return map[string]string{}
}

// sanitizeLabelMapping trims and drops half-filled rows. A row with a canonical name
// but no provider field is a mapping the operator started and abandoned; keeping it
// would either rename a column to "" or mask a working lower-tier mapping.
func sanitizeLabelMapping(raw map[string]string) map[string]string {
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if key == "" || val == "" {
			continue
		}
		out[key] = val
	}
	return out
}

// SupportsLogSource reports whether this (provider, integration source) pair resolves
// to a log source at all.
//
// Exposed so the integration form can offer the Log Label Mapping editor only where a
// log query actually exists. The integration CATEGORY is too coarse for that: jaeger
// and otel_clickhouse are observability_platform / log category but serve traces only,
// and rendering a log field editor on them would be dead config. Deriving it from the
// same switch getLogSource uses is what keeps the two from drifting.
func SupportsLogSource(provider, integrationSource string) bool {
	_, err := getLogSource(provider, integrationSource)
	return err == nil
}

// GetLogLabelMapping answers "which provider field does each canonical field resolve
// to for this account, and which layer decided that?" — the read behind the log
// integration form's effective-mapping panel.
//
// It projects the same resolveLogLabelMapping every log query goes through, so the
// panel cannot report a mapping that queries do not use.
func GetLogLabelMapping(ctx *security.RequestContext, request GetLabelMappingRequest) (LabelMappingResponse, error) {
	providerType := strings.TrimSpace(request.ProviderType)
	if providerType == "" {
		providerType = "logs"
	}
	if providerType != "logs" {
		return LabelMappingResponse{}, fmt.Errorf("provider_type %q is not supported (only \"logs\")", providerType)
	}

	provider, integrationSource, _, err := getLogsMetricsTracesProviderWithIntegration(
		ctx, request.AccountId, request.Provider, "logs", request.ProviderSource)
	if err != nil {
		return LabelMappingResponse{}, err
	}
	// An unsaved integration resolves to nothing, but the form still names the
	// provider it is creating — fall back to it so the provider-default tier can be
	// read from the static source registry, which needs no DB row.
	if provider == "" {
		provider = strings.TrimSpace(request.Provider)
		integrationSource = strings.TrimSpace(request.ProviderSource)
	}
	if provider == "" {
		return LabelMappingResponse{}, fmt.Errorf("provider is required when the account has no default log provider")
	}
	if integrationSource == "" {
		integrationSource = "user"
	}

	source, err := getLogSource(provider, integrationSource)
	if err != nil {
		return LabelMappingResponse{}, fmt.Errorf("provider %q (%s) is not a log provider: %w", provider, integrationSource, err)
	}

	var draft *labelMappingOverride
	if request.DraftSet {
		draft = &labelMappingOverride{Set: true, Mappings: request.DraftMappings}
	}

	// Derived from the integration listing rather than the resolver's DTO: the resolver
	// returns a nil DTO whenever the caller pins both provider and source — which the
	// integration form always does — so trusting it would report every saved integration
	// as unsaved and make the panel label live mappings "(unsaved)".
	_, integrationSaved := lookupLogIntegrationConfigs(ctx, request.AccountId, provider, integrationSource)

	resolved := resolveLogLabelMapping(ctx, request.AccountId, source, draft)
	return LabelMappingResponse{
		AccountId:        request.AccountId,
		Provider:         provider,
		ProviderSource:   integrationSource,
		ProviderType:     providerType,
		IntegrationSaved: integrationSaved,
		DraftApplied:     request.DraftSet,
		TierOrder:        labelMappingTierOrder,
		Fields:           resolved.Fields(knownCanonicalLogFields),
		Effective:        resolved.Effective,
	}, nil
}
