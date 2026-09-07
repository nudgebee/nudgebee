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
	"nudgebee/services/security"
)

const traceLabelMappingsCacheNamespace = "nb_trace_label_mappings"
const traceLabelMappingsCacheTTL = 10 * time.Minute

// traceLabelMappingsConfigName is the integration_config_values entry holding the
// per-account canonical -> provider TRACE field mapping.
//
// Aliased from the integrations package rather than re-spelled, for the reason
// trace_default_filters.go records: that package must also auto-allow the key on save
// (CreateIntegrationConfig rejects any config value whose name is absent from the
// schema), and two independent string literals would let the reader and the writer
// drift apart silently.
const traceLabelMappingsConfigName = core.TraceLabelMappingsConfigName

func init() {
	common.CacheCreateNamespace(
		traceLabelMappingsCacheNamespace,
		common.CacheNamespaceWithExpiration(traceLabelMappingsCacheTTL),
	)
}

// traceLabelMappingsCacheKey mirrors logLabelMappingsCacheKey and
// defaultTraceFiltersCacheKey: the same (account, provider, source) triple the trace
// entry points RESOLVE their query source with, so an explicit provider override reads
// its own integration's mapping rather than the account default's.
func traceLabelMappingsCacheKey(accountId string, ref providerRef) string {
	return accountId + "|" + ref.Provider + "|" + ref.Source
}

// traceLabelMappingsAccountTag lets InvalidateTraceLabelMappingsCache drop every cached
// entry for an account in one call, whichever provider/source it was cached under.
//
// The tag is PREFIXED rather than the bare "account:<id>" the two log caches use, for
// the reason trace_default_filters.go documents: gocache matches invalidation tags
// across the whole store, so a bare tag here would make every trace-mapping
// invalidation also drop the log-filter and log-label-mapping entries for that account.
func traceLabelMappingsAccountTag(accountId string) string {
	return "trace_label_mappings_account:" + accountId
}

// InvalidateTraceLabelMappingsCache drops all cached integration-level trace mappings
// for this account. Called from invalidateIntegrationCaches after an integration
// save/delete, and chained from InvalidateTraceLabelsCacheForAccount, so a new mapping
// applies immediately instead of waiting out the TTL.
// The two request-scoped readers below log through ctx.GetLogger() so tenant/trace
// context rides along; this one takes no context by design — it is called from cache
// invalidation paths that have none — so the global logger is correct here, not an
// oversight. Same split as trace_default_filters.go.
func InvalidateTraceLabelMappingsCache(accountId string) {
	if accountId == "" {
		return
	}
	if err := common.CacheDeleteWithTag(traceLabelMappingsCacheNamespace, traceLabelMappingsAccountTag(accountId)); err != nil {
		slog.Warn("InvalidateTraceLabelMappingsCache: failed to invalidate", "account_id", accountId, "error", err)
	}
}

// readTraceIntegrationConfigValue returns one named config value from the TRACE
// integration serving this (account, provider, source) triple. The trace twin of
// readLogIntegrationConfigValue, and fails open the same way: "" for every failure,
// because an absent value means "nothing configured".
func readTraceIntegrationConfigValue(ctx *security.RequestContext, accountId, traceProvider, traceProviderSource, name string) string {
	configs, _ := lookupTraceIntegrationConfigs(ctx, accountId, traceProvider, traceProviderSource)
	for _, c := range configs {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// lookupTraceIntegrationConfigs is lookupLogIntegrationConfigs for traces — same
// matching rules, resolved against the "traces" provider type so an account whose log
// and trace providers differ reads the right integration's mapping.
func lookupTraceIntegrationConfigs(ctx *security.RequestContext, accountId, traceProvider, traceProviderSource string) ([]core.IntegrationConfigValue, bool) {
	return lookupIntegrationConfigs(ctx, accountId, traceProvider, traceProviderSource, "traces")
}

// getIntegrationTraceLabels returns the canonical -> provider trace field mapping
// configured for this account on the trace integration serving this query. Cached per
// (account, provider, source) for 10 min; fails open (empty map) on any error so a
// malformed blob can never break a trace query.
func getIntegrationTraceLabels(ctx *security.RequestContext, accountId string, ref providerRef) map[string]string {
	if accountId == "" {
		return map[string]string{}
	}
	cacheKey := traceLabelMappingsCacheKey(accountId, ref)

	if cached, ok := common.CacheGet(traceLabelMappingsCacheNamespace, cacheKey); ok {
		var m map[string]string
		if err := json.Unmarshal(cached, &m); err == nil {
			return m
		}
		_ = common.CacheDelete(traceLabelMappingsCacheNamespace, cacheKey)
	}

	mapping := loadIntegrationTraceLabels(ctx, accountId, ref)

	if b, err := json.Marshal(mapping); err == nil {
		tag := traceLabelMappingsAccountTag(accountId)
		if err := common.CacheSet(traceLabelMappingsCacheNamespace, cacheKey, b, common.CacheSetWithTags(tag)); err != nil {
			ctx.GetLogger().Warn("getIntegrationTraceLabels: failed to cache mapping", "account_id", accountId, "error", err)
		}
	}
	return mapping
}

// loadIntegrationTraceLabels reads and parses the integration's trace_label_mappings
// blob, returning the entry for this account.
func loadIntegrationTraceLabels(ctx *security.RequestContext, accountId string, ref providerRef) map[string]string {
	raw := readTraceIntegrationConfigValue(ctx, accountId, ref.Provider, ref.Source, traceLabelMappingsConfigName)
	if strings.TrimSpace(raw) == "" {
		return map[string]string{}
	}

	var entries []core.AccountLogLabelMappings
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		ctx.GetLogger().Warn("loadIntegrationTraceLabels: invalid trace_label_mappings JSON", "account_id", accountId, "error", err)
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

// SupportsTraceSource reports whether this (provider, integration source) pair resolves
// to a trace source at all. The trace twin of SupportsLogSource, derived from the same
// switch getTraceSource uses so the two cannot drift.
func SupportsTraceSource(provider, integrationSource string) bool {
	_, err := getTraceSource(provider, integrationSource)
	return err == nil
}

// GetTraceLabelMapping answers "which provider field does each canonical TRACE field
// resolve to for this account, and which layer decided that?" — the read behind the
// integration form's effective-mapping panel.
//
// It projects the same resolveTraceLabelMapping every trace query goes through, so the
// panel cannot report a mapping that queries do not use. Mirrors GetLogLabelMapping.
func GetTraceLabelMapping(ctx *security.RequestContext, request GetLabelMappingRequest) (LabelMappingResponse, error) {
	provider, integrationSource, _, err := getLogsMetricsTracesProviderWithIntegration(
		ctx, request.AccountId, request.Provider, "traces", request.ProviderSource)
	if err != nil {
		return LabelMappingResponse{}, err
	}
	// An unsaved integration resolves to nothing, but the form still names the
	// provider it is creating — fall back to it so the lower tiers can be read from
	// the static source registry, which needs no DB row. "What am I inheriting?" is
	// the useful answer at that point.
	if provider == "" {
		provider = strings.TrimSpace(request.Provider)
		integrationSource = strings.TrimSpace(request.ProviderSource)
	}
	if provider == "" {
		return LabelMappingResponse{}, fmt.Errorf("provider is required when the account has no default trace provider")
	}
	if integrationSource == "" {
		integrationSource = "user"
	}

	// resolveTraceSource rather than getTraceSource: for ES it upgrades to the
	// OTel-native reader based on the effective trace index, and those two sources
	// publish different static mappings. Reading the wrong one would make the
	// provider-default tier disagree with what queries actually resolve.
	source, err := resolveTraceSource(ctx, request.AccountId, provider, integrationSource, "")
	if err != nil {
		return LabelMappingResponse{}, fmt.Errorf("provider %q (%s) is not a trace provider: %w", provider, integrationSource, err)
	}

	var draft *labelMappingOverride
	if request.DraftSet {
		draft = &labelMappingOverride{Set: true, Mappings: request.DraftMappings}
	}

	// Derived from the integration listing rather than the resolver's DTO, for the
	// reason GetLogLabelMapping records: the resolver returns a nil DTO whenever the
	// caller pins both provider and source — which the integration form always does.
	_, integrationSaved := lookupTraceIntegrationConfigs(ctx, request.AccountId, provider, integrationSource)

	ref := providerRef{Provider: provider, Source: integrationSource}
	resolved := resolveTraceLabelMapping(ctx, request.AccountId, ref, source, draft)
	return LabelMappingResponse{
		AccountId:        request.AccountId,
		Provider:         provider,
		ProviderSource:   integrationSource,
		ProviderType:     labelMappingProviderTypeTraces,
		IntegrationSaved: integrationSaved,
		DraftApplied:     request.DraftSet,
		TierOrder:        labelMappingTierOrder,
		Fields:           canonicalTraceFieldsOnly(resolved.Fields(allCanonicalTraceFieldNames())),
		// NOT filtered, deliberately. Effective is the map queries are actually rewritten
		// with, so it keeps every entry — including the provider aliases the field list
		// above drops. The two differing is the point: Fields answers "what can I map?",
		// Effective answers "what is in force?". Trimming Effective here would break the
		// projection invariant and change query behaviour.
		Effective: resolved.Effective,
	}, nil
}

// canonicalTraceFieldsOnly limits the advertised field list to the canonical trace
// vocabulary.
//
// Fields() lists every key any tier contributes, which for a trace provider means its
// internal aliases too: Datadog publishes 14 OTel-semconv entries (@k8s.pod.name,
// @telemetry.sdk.language, ...) beside the 7 canonical ones, and because '@' sorts below
// every letter in Fields()' sort.Strings they landed at the TOP of the panel — an
// operator opened "Trace Label Mapping" and saw fourteen rows of Datadog plumbing before
// a single field they could map. New Relic (10) and Chronosphere (12) are the same shape.
//
// Those aliases are not configuration. They exist so a query written in OTel terms is
// rewritten correctly, they keep doing that job through Effective, and no operator ever
// sets one. So they are dropped from the ADVERTISED list only.
//
// Known consequence, accepted: a non-canonical key an operator sets themselves is no
// longer listed here either, though it still rewrites queries. A key stored on this
// integration is still visible as an editable row in the mapping cards above the panel
// (they render the saved blob verbatim), and tenant/account keys remain visible on those
// Settings screens — what is lost is seeing it resolved, with its winning tier.
//
// Logs get no equivalent filter: every log provider's keys are already plain concepts
// (pod, namespace, container, level), and filtering would drop real extras that the
// openobserve / splunk / newrelic log sources contribute (body, node, cluster, severity).
func canonicalTraceFieldsOnly(fields []LabelMappingField) []LabelMappingField {
	canonical := make(map[string]struct{}, len(canonicalTraceFields))
	for _, f := range canonicalTraceFields {
		canonical[f.name] = struct{}{}
	}

	out := make([]LabelMappingField, 0, len(canonical))
	for _, f := range fields {
		if _, ok := canonical[f.Canonical]; ok {
			out = append(out, f)
		}
	}
	return out
}

// allCanonicalTraceFieldNames is the full canonical vocabulary, sorted: what the panel
// advertises as mappable, so an operator sees what they COULD map and not only what
// some tier already sets, and what an undeclared (passthrough) provider still
// advertises. Derived from canonicalTraceFields so the panel and the query builder can
// never list different fields.
func allCanonicalTraceFieldNames() []string {
	out := make([]string, 0, len(canonicalTraceFields))
	for _, f := range canonicalTraceFields {
		out = append(out, f.name)
	}
	sort.Strings(out)
	return out
}
