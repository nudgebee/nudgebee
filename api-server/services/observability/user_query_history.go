package observability

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Status values recorded in user_history.status. The History modal renders these
// verbatim (UserHistory.jsx), so they must not change without a data migration.
const (
	userHistoryStatusSuccess = "SUCCESS"
	userHistoryStatusFailed  = "FAILED"
)

const (
	logQueryModulePrefix     = "log_query_"
	metricsQueryModulePrefix = "metrics_query_"
)

// userHistoryModules mirrors the module_check CHECK constraint (migration V875).
//
// The constraint is an allowlist and the history write is fire-and-forget, so a
// provider that ships without a migration would fail its INSERT silently.
// Checking here first turns that into a logged skip. Keep in sync with V875 —
// TestUserHistoryModulesCoverAllProviders walks the provider switches and fails
// if one has no entry here.
var userHistoryModules = map[string]struct{}{
	// getLogSource — 14 providers, lowercased.
	"log_query_loki":                          {},
	"log_query_signoz":                        {},
	"log_query_datadog":                       {},
	"log_query_observe":                       {},
	"log_query_loggly":                        {},
	"log_query_azure_app_insights":            {},
	"log_query_aws_cloudwatch":                {},
	"log_query_es":                            {},
	"log_query_newrelic":                      {},
	"log_query_splunk_observability_platform": {},
	"log_query_dynatrace":                     {},
	"log_query_solarwinds":                    {},
	"log_query_pinot":                         {},
	"log_query_hive":                          {},
	// getMetricsSource — 10 providers, lowercased.
	"metrics_query_prometheus":                    {},
	"metrics_query_datadog":                       {},
	"metrics_query_chronosphere":                  {},
	"metrics_query_aws_cloudwatch":                {},
	"metrics_query_azure_app_insights":            {},
	"metrics_query_newrelic":                      {},
	"metrics_query_splunk_observability_platform": {},
	"metrics_query_es":                            {},
	"metrics_query_dynatrace":                     {},
	"metrics_query_solarwinds":                    {},
	// Frontend-only labels sniffed from the Prometheus URL (QueryMetrics.tsx),
	// listed in both spellings by KubernetesCreateAlert.tsx. Neither resolves in
	// getMetricsSource, so these rows are FAILED-only by construction.
	"metrics_query_victoria-metrics": {},
	"metrics_query_victoria_metrics": {},
}

// IsKnownUserHistoryModule reports whether module is permitted by module_check.
func IsKnownUserHistoryModule(module string) bool {
	_, ok := userHistoryModules[module]
	return ok
}

// BuildLogQueryHistory renders one user_history row for a log query, or reports
// false when there is nothing worth recording.
//
// The recorded query is the one the backend actually ran (FetchLogsResult.Query,
// i.e. usedQuery in FetchLogs), not what the caller typed: Builder mode sends a
// where clause and no query string, so the caller-side value is either empty or
// an unreadable filter array.
func BuildLogQueryHistory(req FetchLogRequest, res FetchLogsResult, execErr error, elapsed time.Duration) (UserHistoryRequest, bool) {
	provider := res.Provider
	if provider == "" {
		// FetchLogs failed before resolving a provider (no integration, or label
		// data-type validation rejected the request). Fall back to whatever the
		// caller named; if it named nothing, there is no module to file under.
		provider = req.LogProvider
	}
	if provider == "" {
		return UserHistoryRequest{}, false
	}

	data := res.Query
	if data == "" {
		// Errors raised before usedQuery is computed leave Query empty.
		data = req.Query
	}
	if data == "" && hasWhereData(req.QueryRequest.Where) {
		// The where clause is then the only remaining description of the ask.
		if encoded, err := json.Marshal(req.QueryRequest.Where); err == nil {
			data = string(encoded)
		}
	}

	return newUserHistoryRow(logQueryModulePrefix, provider, req.AccountId, data, execErr, elapsed)
}

// BuildMetricsQueryHistory renders one user_history row for a metrics query.
//
// A single submit can carry several queries (the UI splits the editor contents on
// ';'), so the executed PromQL is joined newline-separated. Results arrive from a
// Go map, so they are sorted by QueryKey to keep the recorded text deterministic
// rather than dependent on map iteration order.
func BuildMetricsQueryHistory(req FetchMetricsRequest, res OutputMetricQuery, execErr error, elapsed time.Duration) (UserHistoryRequest, bool) {
	if req.MetricProvider == "" {
		return UserHistoryRequest{}, false
	}

	results := make([]QueryResult, len(res.Results))
	copy(results, res.Results)
	sort.SliceStable(results, func(i, j int) bool { return results[i].QueryKey < results[j].QueryKey })

	queries := make([]string, 0, len(results))
	perQueryFailed := false
	for _, r := range results {
		if r.Error != nil && *r.Error != "" {
			// A per-result error is a query failure even though the call returned
			// 200 — this is the case the frontend recorded as FAILED.
			perQueryFailed = true
		}
		if r.Query != "" {
			queries = append(queries, r.Query)
		}
	}

	data := strings.Join(queries, "\n")
	if data == "" {
		// Nothing executed (resolution failed, or no results came back at all):
		// fall back to the submitted queries, sorted for the same determinism.
		keys := make([]string, 0, len(req.Queries))
		for k := range req.Queries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		submitted := make([]string, 0, len(keys))
		for _, k := range keys {
			if q := req.Queries[k]; q != "" {
				submitted = append(submitted, q)
			}
		}
		data = strings.Join(submitted, "\n")
	}

	row, ok := newUserHistoryRow(metricsQueryModulePrefix, req.MetricProvider, req.AccountId, data, execErr, elapsed)
	if ok && perQueryFailed {
		row.Status = userHistoryStatusFailed
	}
	return row, ok
}

// newUserHistoryRow applies the rules shared by both builders: the module is
// always <prefix><lowercased provider> so it matches what the read side asks
// for, and a row with no query text is not worth writing.
func newUserHistoryRow(prefix, provider, accountId, data string, execErr error, elapsed time.Duration) (UserHistoryRequest, bool) {
	if data == "" {
		return UserHistoryRequest{}, false
	}

	status := userHistoryStatusSuccess
	if execErr != nil {
		status = userHistoryStatusFailed
	}

	return UserHistoryRequest{
		AccountId: accountId,
		Module:    prefix + strings.ToLower(provider),
		Data:      data,
		Status:    status,
		Duration:  elapsed.Milliseconds(),
	}, true
}
