package observability

import (
	"encoding/json"
	"log/slog"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/query"
	"nudgebee/services/security"
)

const logDefaultFiltersCacheNamespace = "nb_log_default_filters"
const logDefaultFiltersCacheTTL = 10 * time.Minute

// defaultFiltersConfigName is the integration_config_values entry that holds the
// per-account always-apply log filters, configured on the log integration form.
const defaultFiltersConfigName = "default_filters"

func init() {
	common.CacheCreateNamespace(
		logDefaultFiltersCacheNamespace,
		common.CacheNamespaceWithExpiration(logDefaultFiltersCacheTTL),
	)
}

// defaultFilterRow is one always-apply filter (key = value). Equality only for
// now; `op` is reserved for future operators and normalized to equality here.
type defaultFilterRow struct {
	Key   string `json:"key"`
	Op    string `json:"op,omitempty"`
	Value any    `json:"value"`
}

// accountDefaultFilters is one per-account entry in the log integration's
// `default_filters` config value.
type accountDefaultFilters struct {
	AccountId string             `json:"accountId"`
	Filters   []defaultFilterRow `json:"filters"`
}

// buildDefaultFilterClause turns always-apply filter rows into a canonical
// where-clause (equality only). Rows with an empty key or empty value are skipped
// (fail-open per row); an empty clause is returned when nothing usable remains.
func buildDefaultFilterClause(rows []defaultFilterRow) query.QueryWhereClause {
	clauses := make([]query.QueryWhereClause, 0, len(rows))
	for _, r := range rows {
		// Equality only, string values only (the UI emits trimmed strings). The
		// string type-assert also avoids a panic from comparing a non-comparable
		// JSON value (slice/map) against "".
		val, ok := r.Value.(string)
		if r.Key == "" || !ok || val == "" {
			continue
		}
		clauses = append(clauses, query.QueryWhereClause{
			Binary: query.BinaryWhereClause{r.Key: {query.Eq: val}},
		})
	}
	switch len(clauses) {
	case 0:
		return query.QueryWhereClause{}
	case 1:
		return clauses[0]
	default:
		return query.QueryWhereClause{And: clauses}
	}
}

// defaultLogFiltersCacheKey must mirror the same (accountId, logProvider,
// logProviderSource) triple FetchLogs resolves the actual query source with —
// otherwise an explicit provider override (e.g. querying a non-default Pinot
// integration) would read/cache another integration's filters instead.
func defaultLogFiltersCacheKey(accountId, logProvider, logProviderSource string) string {
	return accountId + "|" + logProvider + "|" + logProviderSource
}

// defaultLogFiltersAccountTag lets InvalidateDefaultLogFiltersCache drop every
// cached entry for an account in one call, regardless of which provider/source
// each entry was cached under.
func defaultLogFiltersAccountTag(accountId string) string {
	return "account:" + accountId
}

// InvalidateDefaultLogFiltersCache drops all cached default-filter clauses for
// this account. Call after saving/deleting a log integration's `default_filters`
// config so the new filters apply immediately instead of waiting out the TTL.
func InvalidateDefaultLogFiltersCache(accountId string) {
	if accountId == "" {
		return
	}
	if err := common.CacheDeleteWithTag(logDefaultFiltersCacheNamespace, defaultLogFiltersAccountTag(accountId)); err != nil {
		slog.Warn("InvalidateDefaultLogFiltersCache: failed to invalidate", "account_id", accountId, "error", err)
	}
}

// getDefaultLogFilters returns the always-apply where-clause configured for this
// account on the log integration actually used for this query (same
// logProvider/logProviderSource FetchLogs resolves the query source with).
// Fails open (empty clause) on any error so a bad/absent config never blocks a
// log query. Cached per (account, provider, source) for 10 min.
func getDefaultLogFilters(ctx *security.RequestContext, accountId, logProvider, logProviderSource string) query.QueryWhereClause {
	if accountId == "" {
		return query.QueryWhereClause{}
	}
	cacheKey := defaultLogFiltersCacheKey(accountId, logProvider, logProviderSource)

	if cached, ok := common.CacheGet(logDefaultFiltersCacheNamespace, cacheKey); ok {
		var clause query.QueryWhereClause
		if err := json.Unmarshal(cached, &clause); err == nil {
			return clause
		}
		_ = common.CacheDelete(logDefaultFiltersCacheNamespace, cacheKey)
	}

	clause := loadDefaultLogFilters(ctx, accountId, logProvider, logProviderSource)

	if b, err := json.Marshal(clause); err == nil {
		tag := defaultLogFiltersAccountTag(accountId)
		if err := common.CacheSet(logDefaultFiltersCacheNamespace, cacheKey, b, common.CacheSetWithTags(tag)); err != nil {
			slog.Warn("getDefaultLogFilters: failed to cache default filters", "account_id", accountId, "error", err)
		}
	}
	return clause
}

// ApplyDefaultLogFilters ANDs the account's always-apply log filters into
// fetchLogRequest's where clause in place. Shared by FetchLogs (query
// execution) and GetLogsQuery (the SQL-preview endpoint that populates the log
// builder's raw-query editor) so a saved filter reaches Pinot's SQL either way
// — the raw-query editor is Pinot's default mode, and once a raw query string
// is submitted, FetchLogs has no where clause left to inject into (see
// pinot.go/pinot_saas.go QueryLogs: a non-empty Query is executed verbatim).
func ApplyDefaultLogFilters(ctx *security.RequestContext, fetchLogRequest *FetchLogRequest) {
	if fetchLogRequest == nil || ctx == nil {
		return
	}
	defaults := getDefaultLogFilters(ctx, fetchLogRequest.AccountId, fetchLogRequest.LogProvider, fetchLogRequest.LogProviderSource)
	fetchLogRequest.QueryRequest.Where = andWhereClause(fetchLogRequest.QueryRequest.Where, defaults)
}

// andWhereClause ANDs defaults into existing, skipping either side that carries
// no data rather than emitting a degenerate And{} branch.
func andWhereClause(existing, defaults query.QueryWhereClause) query.QueryWhereClause {
	if !hasWhereData(defaults) {
		return existing
	}
	if !hasWhereData(existing) {
		return defaults
	}
	return query.QueryWhereClause{And: []query.QueryWhereClause{existing, defaults}}
}

// readLogIntegrationConfigValue returns one named config value from the log
// integration that actually serves this (account, provider, source) triple.
//
// Resolving the integration — rather than taking the account's default — is what
// keeps a per-integration setting attached to the integration the query will really
// run against: an account can carry two log integrations, and the logs tab can pin
// the non-default one. Empty logProvider/logProviderSource still mean "the default",
// which is what the callers with no override in hand pass.
//
// Returns "" for every failure (no integration, no config row, lookup error). Both
// callers treat an absent value as "nothing configured", so failing open here keeps
// a misconfigured integration from breaking log queries outright.
func readLogIntegrationConfigValue(ctx *security.RequestContext, accountId, logProvider, logProviderSource, name string) string {
	configs, _ := lookupLogIntegrationConfigs(ctx, accountId, logProvider, logProviderSource)
	for _, c := range configs {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// lookupLogIntegrationConfigs returns the config values of the log integration serving
// this (account, provider, source) triple, and whether such an integration exists.
//
// The `found` flag is separate from `len(configs) > 0` because a saved integration can
// legitimately hold no config values yet, and callers need to tell "no integration" from
// "an integration with nothing set" — the Advanced Settings panel labels its answer
// differently in each case.
//
// Note it does NOT rely on the resolver's DTO to decide `found`: when the caller pins
// both provider and source (as the integration form does), the resolver short-circuits
// and returns a nil DTO even though the integration exists. The listing below is the
// authority.
func lookupLogIntegrationConfigs(ctx *security.RequestContext, accountId, logProvider, logProviderSource string) ([]core.IntegrationConfigValue, bool) {
	provider, source, dto, err := getLogsMetricsTracesProviderWithIntegration(ctx, accountId, logProvider, "logs", logProviderSource)
	if err != nil || provider == "" {
		return nil, false
	}

	// The resolver DTO carries no Configs; re-list to get the populated config
	// values (same path GetPinotConfig uses).
	dtos, err := core.ListIntegrationConfigs(ctx, accountId, provider)
	if err != nil || len(dtos) == 0 {
		return nil, false
	}

	// Prefer the exact integration the resolver picked.
	if dto != nil {
		for _, d := range dtos {
			if d.Id == dto.Id {
				return d.Configs, true
			}
		}
	}
	// Then one matching the RESOLVED source. Matching only "user" (as this did before
	// it was generalised) silently skipped every agent-source provider — loki, ES-agent,
	// pinot-agent — so a per-account setting saved on one of those was stored and never
	// read back.
	if source != "" {
		for _, d := range dtos {
			if d.Source == source {
				return d.Configs, true
			}
		}
	}
	for _, d := range dtos {
		if d.Source == "user" {
			return d.Configs, true
		}
	}
	return nil, false
}

// loadDefaultLogFilters resolves the account's log integration for this
// logProvider/logProviderSource, reads its `default_filters` config, and builds
// the clause for this account.
func loadDefaultLogFilters(ctx *security.RequestContext, accountId, logProvider, logProviderSource string) query.QueryWhereClause {
	raw := readLogIntegrationConfigValue(ctx, accountId, logProvider, logProviderSource, defaultFiltersConfigName)
	if raw == "" {
		return query.QueryWhereClause{}
	}

	var entries []accountDefaultFilters
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		slog.Warn("getDefaultLogFilters: invalid default_filters JSON", "account_id", accountId, "error", err)
		return query.QueryWhereClause{}
	}

	for _, e := range entries {
		if e.AccountId == accountId {
			return buildDefaultFilterClause(e.Filters)
		}
	}
	return query.QueryWhereClause{}
}
