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

// getDefaultLogFilters returns the always-apply where-clause configured for this
// account on its default log integration. Fails open (empty clause) on any error
// so a bad/absent config never blocks a log query. Cached per account (10 min).
func getDefaultLogFilters(ctx *security.RequestContext, accountId string) query.QueryWhereClause {
	if accountId == "" {
		return query.QueryWhereClause{}
	}

	if cached, ok := common.CacheGet(logDefaultFiltersCacheNamespace, accountId); ok {
		var clause query.QueryWhereClause
		if err := json.Unmarshal(cached, &clause); err == nil {
			return clause
		}
		_ = common.CacheDelete(logDefaultFiltersCacheNamespace, accountId)
	}

	clause := loadDefaultLogFilters(ctx, accountId)

	if b, err := json.Marshal(clause); err == nil {
		if err := common.CacheSet(logDefaultFiltersCacheNamespace, accountId, b); err != nil {
			slog.Warn("getDefaultLogFilters: failed to cache default filters", "account_id", accountId, "error", err)
		}
	}
	return clause
}

// loadDefaultLogFilters resolves the account's default log integration, reads its
// `default_filters` config, and builds the clause for this account.
func loadDefaultLogFilters(ctx *security.RequestContext, accountId string) query.QueryWhereClause {
	provider, _, dto, err := getLogsMetricsTracesProviderWithIntegration(ctx, accountId, "", "logs", "")
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
		if c.Name == defaultFiltersConfigName {
			raw = c.Value
			break
		}
	}
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
