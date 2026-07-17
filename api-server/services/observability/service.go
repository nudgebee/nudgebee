package observability

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"nudgebee/services/account"
	"nudgebee/services/cloud"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/database"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"regexp"
	"sort"
	"strings"
	"time"
)

// promqlLabelNameRe matches valid PromQL label names. Enforced on every
// LabelMatcher.Label so user-supplied strings cannot smuggle additional
// selectors via concatenation (e.g. `foo,bar=evil`).
var promqlLabelNameRe = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)

type LogSource interface {
	QueryLogs(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) ([]OutputLog, error)
	QueryLabels(ctx *security.RequestContext, fetchLogRequest FetchLogLabelRequest) ([]OutputLogLabel, error)
	QueryLabelValues(ctx *security.RequestContext, fetchLogRequest FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error)
	GetQuery(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) (string, error)
	GetLabelMapping() map[string]string
	GetSupportedOperators() []string
}

type LogGroupSource interface {
	QueryLogGroup(ctx *security.RequestContext, fetchLogGroupRequest FetchLogGroupRequest) (LogGroupOutput, error)
}

// QueryRequestKeyFilter is an optional interface that LogSource implementations can
// implement to declare keys that should be stripped from QueryRequest.Where before
// query execution. If an integration does not implement this interface, no keys are
// removed (full pass-through).
//
// Keys must be declared in provider space (i.e. the names after label-mapping has
// been applied), because key removal runs after convertWhereClauseWithMApping.
// Keys that have no mapping entry keep their original names, so those can be
// declared as-is.
//
// When all conditions in a sub-clause are removed, the sub-clause is pruned rather
// than left empty. If the entire top-level Where becomes empty, QueryLogs is called
// without a WHERE clause and returns all logs within the requested time window.
type QueryRequestKeyFilter interface {
	GetIgnoredQueryRequestKeys() []string
}

type TraceSource interface {
	QueryTraces(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) ([]common.OpenTelemetryTrace, error)
	GetQuery(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) (string, error)
	CountTraces(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) (common.OpenTelemetryTraceCount, error)
	GetLabelValues(ctx *security.RequestContext, fetchTraceRequest TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error)
	// QueryLabels enumerates the label KEYS actually present in the backend (e.g. span/
	// resource attribute names), analogous to LogSource.QueryLabels. Sources without a
	// backend label-key discovery API return an empty slice; FetchTraceLabels then falls
	// back to the derived canonical + mapping label set.
	QueryLabels(ctx *security.RequestContext, request FetchTraceLabelRequest) ([]OutputTraceLabel, error)
	QueryGroupedTraces(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) ([]TraceGroupingValues, error)
	QueryGroupedTracesCount(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) (common.OpenTelemetryTraceGroupCount, error)
	// QueryRootSpansByTrace backs the "By Traces" listing view: it returns one representative
	// row per trace (the root span, or the earliest span when no root is present in the
	// window). Filters apply to the chosen root span. Every TraceSource must implement it so
	// the toggle works for all providers; non-ClickHouse providers reduce their span result
	// to roots via the shared pickRootSpans helper.
	QueryRootSpansByTrace(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) ([]common.OpenTelemetryTrace, error)
	// CountTracesByTrace returns the number of distinct traces matching the filters for the
	// "By Traces" view. Providers that cannot count distinct traces cheaply return Count = -1,
	// which the frontend already treats as an estimate for pagination.
	CountTracesByTrace(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) (common.OpenTelemetryTraceCount, error)
	QueryTracesHeatmap(ctx *security.RequestContext, fetchHeatMapRequest TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error)
	GetLabelMapping() map[string]string
	GetSupportedOperators() []string
}

type MetricSource interface {
	FetchMetricsQuery(ctx *security.RequestContext, fetchMetricsRequest FetchMetricsRequest) (OutputMetricQuery, error)
	FetchMetricList(ctx *security.RequestContext, fetchMetricsListRequest FetchMetricsListRequest) ([]OutputMetrics, error)
	FetchMetricLabelValues(ctx *security.RequestContext, fetchMetricsLabelRequest FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error)
	FetchMetricsLabels(ctx *security.RequestContext, fetchMetricsRequest FetchMetricLabelsRequest) ([]OutputMetricLabels, error)
	GetSupportedOperators() []string
	GetQuery(ctx *security.RequestContext, fetchMetricsRequest FetchMetricsRequest) (string, error)
}

// MetricSeriesSource is an OPTIONAL capability a MetricSource may implement to answer
// "which metric families have series for workload W in namespace N" via a label-selector
// series lookup. It is intentionally separate from MetricSource so providers that have
// no series-match equivalent (datadog, cloudwatch, newrelic, …) are not forced to stub
// it; the orchestrator type-asserts and returns a clear "not supported" error otherwise.
// Implemented by Prometheus/VictoriaMetrics (and, in a follow-up, Elasticsearch).
type MetricSeriesSource interface {
	FetchMetricSeries(ctx *security.RequestContext, fetchMetricSeriesRequest FetchMetricSeriesRequest) (MetricSeriesResult, error)
}

func escapePromQLString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

// sortedKeys returns the keys of m in sorted order for deterministic output.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// wrapPromQLAggregator wraps a rendered PromQL expression in a non-parametric
// aggregator (sum/avg/min/max/count/stddev/stdvar/group). Empty op = pass
// through unchanged. Parametric aggregators (topk/bottomk/quantile) are
// rejected — they need a scalar parameter that the QueryItem shape doesn't
// carry today.
func wrapPromQLAggregator(expr string, op string) (string, error) {
	switch op {
	case "":
		return expr, nil
	case "sum", "avg", "min", "max", "count", "stddev", "stdvar", "group":
		return op + "(" + expr + ")", nil
	case "topk", "bottomk", "quantile":
		return "", fmt.Errorf("aggregate_operator %q requires a scalar parameter and is not yet supported", op)
	default:
		return "", fmt.Errorf("unsupported aggregate_operator %q", op)
	}
}

// promqlMatcherOp translates a wire-token operator (as advertised by
// Prometheus/Chronosphere GetSupportedOperators) into the PromQL matcher
// operator string. _in / _not_in are advertised but not yet supported here —
// they require a list-value editor and an end-to-end value-shape contract.
func promqlMatcherOp(token string) (string, error) {
	switch token {
	case "_eq":
		return "=", nil
	case "_neq":
		return "!=", nil
	case "_regex":
		return "=~", nil
	case "_in", "_not_in":
		return "", fmt.Errorf("operator %q not yet supported in PromQL builder; use _regex with an alternation pattern instead", token)
	default:
		return "", fmt.Errorf("unsupported operator %q", token)
	}
}

// injectPromQLMatchers renders LabelMatchers (with operators) and the legacy
// Labels map (eq-only, used by internal callers) into the selector portion of
// a PromQL expression. Output is deterministic: matchers are sorted by
// (label, operator, value); legacy labels are appended after, sorted by name.
//
// If the expression already contains {}, the new selectors are appended
// inside. If it contains a range selector [Xm], a new {} is inserted before
// it. Otherwise selectors are appended at the end.
func injectPromQLMatchers(expr string, matchers []LabelMatcher, legacyLabels map[string]string) (string, error) {
	if len(matchers) == 0 && len(legacyLabels) == 0 {
		return expr, nil
	}

	sorted := make([]LabelMatcher, len(matchers))
	copy(sorted, matchers)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Label != sorted[j].Label {
			return sorted[i].Label < sorted[j].Label
		}
		if sorted[i].Operator != sorted[j].Operator {
			return sorted[i].Operator < sorted[j].Operator
		}
		return sorted[i].Value < sorted[j].Value
	})

	parts := make([]string, 0, len(sorted)+len(legacyLabels))
	for _, m := range sorted {
		if !promqlLabelNameRe.MatchString(m.Label) {
			return "", fmt.Errorf("invalid label name %q", m.Label)
		}
		op, err := promqlMatcherOp(m.Operator)
		if err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf(`%s%s"%s"`, m.Label, op, escapePromQLString(m.Value)))
	}
	for _, k := range sortedKeys(legacyLabels) {
		if !promqlLabelNameRe.MatchString(k) {
			return "", fmt.Errorf("invalid label name %q", k)
		}
		parts = append(parts, fmt.Sprintf(`%s="%s"`, k, escapePromQLString(legacyLabels[k])))
	}
	newSelector := strings.Join(parts, ",")

	if idx := strings.Index(expr, "{"); idx != -1 {
		closeOffset := strings.Index(expr[idx:], "}")
		if closeOffset == -1 {
			return expr + "{" + newSelector + "}", nil
		}
		closeIdx := idx + closeOffset
		existing := expr[idx+1 : closeIdx]
		if existing == "" {
			return expr[:idx+1] + newSelector + expr[closeIdx:], nil
		}
		return expr[:idx+1] + existing + "," + newSelector + expr[closeIdx:], nil
	}
	if idx := strings.Index(expr, "["); idx != -1 {
		return expr[:idx] + "{" + newSelector + "}" + expr[idx:], nil
	}
	return expr + "{" + newSelector + "}", nil
}

func getLogSource(provider, integrationSource string) (LogSource, error) {
	switch {
	case provider == "loki" && integrationSource == "agent":
		return &LokiSource{}, nil
	case provider == "signoz" && integrationSource == "agent":
		return &SignozSource{}, nil
	case provider == "signoz" && integrationSource == "user":
		return &SignozSaasSource{}, nil
	case provider == "datadog" && integrationSource == "user":
		return &DatadogSource{}, nil
	case provider == "observe" && integrationSource == "user":
		return &ObserveSource{}, nil
	case provider == "loggly" && integrationSource == "user":
		return &LogglySource{}, nil
	case provider == "azure_app_insights" && integrationSource == "user":
		return &AzureAppInsightsSource{}, nil
	case provider == "aws_cloudwatch" && integrationSource == "user":
		return &cloudLogs{}, nil
	case provider == "ES" && integrationSource == "agent":
		return &ElasticSource{}, nil
	case provider == "ES" && integrationSource == "user":
		return &ElasticSaasSource{}, nil
	case provider == "newrelic" && integrationSource == "user":
		return &NewRelicLogSource{}, nil
	case provider == "splunk_observability_platform" && integrationSource == "user":
		return &SplunkLogSource{}, nil
	case provider == "dynatrace" && integrationSource == "user":
		return &DynatraceLogSource{}, nil
	case provider == "solarwinds" && integrationSource == "user":
		return &SolarWindsLogSource{}, nil
	case provider == "pinot" && integrationSource == "agent":
		return &PinotSource{}, nil
	case provider == "pinot" && integrationSource == "user":
		return &PinotSaasSource{}, nil
	case provider == "hive" && integrationSource == "user":
		return &HiveSaasSource{}, nil
	case provider == "openobserve" && integrationSource == "user":
		return &OpenObserveLogSource{}, nil
		// hive:agent is intentionally NOT wired here yet — the relay-mode
		// `HiveSource` is implemented but the matching `hive_query` /
		// `hive_schema` actions don't exist in nudgebee-agent yet. Returning
		// the unsupported-combination error from the default case is a clearer
		// signal at config time than routing to a source that errors on every
		// call. Re-add this case once the agent PR ships.
	default:
		return nil, fmt.Errorf(
			"unsupported log provider/source combination: provider=%s, integrationSource=%s",
			provider, integrationSource,
		)
	}
}

func getLogGroupSource(provider, integrationSource string) (LogGroupSource, error) {
	// First try to get the log source and check if it supports log grouping.
	logSource, err := getLogSource(provider, integrationSource)
	if err == nil {
		if grouper, ok := logSource.(LogGroupSource); ok {
			return grouper, nil
		}
	}

	// Fallback to dedicated log group sources (e.g. Prometheus metrics-based grouping).
	switch {
	case provider == "prometheus" && integrationSource == "agent":
		return &PrometheusLogGroupSource{}, nil
	case provider == "dynatrace" && integrationSource == "user":
		return &DynatraceLogGroupSource{}, nil
	default:
		return nil, fmt.Errorf(
			"unsupported log group provider/source combination: provider=%s, integrationSource=%s",
			provider, integrationSource,
		)
	}
}

func getLogSourceForAccount(ctx *security.RequestContext, accountId string, logProvider string, logProviderSource string) (LogSource, error) {
	if accountId == "" {
		return nil, fmt.Errorf("account_id is required")
	}

	logProvider, integrationSource, err := GetLogsMetricsTracesProvider(ctx, accountId, logProvider, "logs", logProviderSource)
	if err != nil {
		return nil, err
	}

	if logProvider == "" {
		return nil, fmt.Errorf("FetchLogs log provider (log_provider) is required")
	}

	source, err := getLogSource(logProvider, integrationSource)
	return source, err
}

func getLogGroupSourceForAccount(ctx *security.RequestContext, accountId string, logProvider string, logProviderSource string) (LogGroupSource, error) {
	if accountId == "" {
		return nil, fmt.Errorf("account_id is required")
	}

	// Try the logs provider first — each log source can implement its own grouping.
	logsProvider, logsIntegrationSource, err := GetLogsMetricsTracesProvider(ctx, accountId, logProvider, "logs", logProviderSource)
	if err != nil {
		return nil, err
	}
	if logsProvider != "" {
		source, err := getLogGroupSource(logsProvider, logsIntegrationSource)
		if err == nil {
			return source, nil
		}
		ctx.GetLogger().Debug("log provider does not support log grouping, falling back to metrics provider",
			"logs_provider", logsProvider, "error", err)
	}

	// Fallback: try the metrics provider (e.g. Prometheus-based log grouping).
	metricsProvider, metricsIntegrationSource, err := GetLogsMetricsTracesProvider(ctx, accountId, "", "metrics", "")
	if err != nil {
		return nil, err
	}
	if metricsProvider == "" {
		return nil, fmt.Errorf("no log or metrics provider configured for log grouping")
	}

	source, err := getLogGroupSource(metricsProvider, metricsIntegrationSource)
	return source, err
}

func getTraceSource(provider, integrationSource string) (TraceSource, error) {
	if integrationSource == "" {
		integrationSource = "agent"
	}
	switch {
	case provider == "datadog" && integrationSource == "user":
		return &DatadogTraceSource{}, nil
	case provider == "azure_app_insights" && integrationSource == "user":
		return &AzureAppInsightsTraceSource{}, nil
	case provider == "chronosphere" && integrationSource == "user":
		return &ChronosphereTraceSaasSource{}, nil
	case provider == "chronosphere" && integrationSource == "agent":
		return &ChronosphereTraceSource{}, nil
	case provider == "otel_clickhouse" && integrationSource == "agent":
		return &OtelClickhouseTraceSource{}, nil
	case provider == "jaeger" && integrationSource == "agent":
		return &JaegerTraceSource{}, nil
	case provider == "jaeger" && integrationSource == "user":
		return &JaegerSaasTraceSource{}, nil
	case provider == "newrelic" && integrationSource == "user":
		return &NewRelicTraceSource{}, nil
	case provider == "splunk_observability_platform" && integrationSource == "user":
		return &SplunkTraceSource{}, nil
	case provider == "ES" && integrationSource == "user":
		return &ElasticSaasTraceSource{}, nil
	case provider == "dynatrace" && integrationSource == "user":
		return &DynatraceTraceSource{}, nil
	case provider == "solarwinds" && integrationSource == "user":
		return &SolarWindsTraceSource{}, nil
	case provider == "gcp":
		// GCP cloud accounts have no agent/integration row; the provider is synthesized
		// by the resolver, so match on provider alone regardless of source.
		return &GcpTraceSource{}, nil
	case provider == "openobserve" && integrationSource == "user":
		return &OpenObserveTraceSource{}, nil
	default:
		return nil, fmt.Errorf(
			"unsupported traces provider/source combination: provider=%s, integrationSource=%s",
			provider, integrationSource,
		)
	}
}

func getMetricsSource(provider, integrationSource string) (MetricSource, error) {
	switch {
	case provider == "datadog" && integrationSource == "user":
		return &DatadogMetricSource{}, nil
	case provider == "prometheus" && integrationSource == "agent":
		return &PrometheusMetricSource{}, nil
	case provider == "chronosphere" && integrationSource == "user":
		return &ChronosphereMetricSaasSource{}, nil
	case provider == "chronosphere" && integrationSource == "agent":
		return &ChronosphereMetricSource{}, nil
	case provider == "aws_cloudwatch" && integrationSource == "user":
		return &cloudMetrics{}, nil
	case provider == "azure_app_insights" && integrationSource == "user":
		return &AzureAppInsightsMetricSource{}, nil
	case provider == "newrelic" && integrationSource == "user":
		return &NewRelicMetricSource{}, nil
	case provider == "splunk_observability_platform" && integrationSource == "user":
		return &SplunkMetricSource{}, nil
	case provider == "ES" && integrationSource == "user":
		return &ElasticSaasMetricSource{}, nil
	case provider == "dynatrace" && integrationSource == "user":
		return &DynatraceMetricSource{}, nil
	case provider == "solarwinds" && integrationSource == "user":
		return &SolarWindsMetricSource{}, nil
	case provider == "openobserve" && integrationSource == "user":
		return &OpenObserveMetricSource{}, nil
	default:
		return nil, fmt.Errorf(
			"unsupported metric provider/source combination: provider=%s, integrationSource=%s",
			provider, integrationSource,
		)
	}
}

func getMetricsSourceForAccount(ctx *security.RequestContext, accountId string, metricsProvider string, metricsProviderSource string) (MetricSource, error) {
	if accountId == "" {
		return nil, fmt.Errorf("account_id is required")
	}

	metricsProvider, integrationSource, err := GetLogsMetricsTracesProvider(ctx, accountId, metricsProvider, "metrics", metricsProviderSource)
	if err != nil {
		return nil, err
	}

	if metricsProvider == "" {
		return nil, fmt.Errorf("observability: metrics provider (metrics_provider) is required")
	}

	if integrationSource == "" {
		return nil, fmt.Errorf("observability: source provider (integration source) is required")
	}

	source, err := getMetricsSource(metricsProvider, integrationSource)
	return source, err
}

func GetLogsMetricsTracesProvider(ctx *security.RequestContext, accountId, logProviderFromRequest, providerType string, logSourceFromRequest string) (string, string, error) {
	provider, source, _, err := getLogsMetricsTracesProviderWithIntegration(ctx, accountId, logProviderFromRequest, providerType, logSourceFromRequest)
	return provider, source, err
}

// getLogsMetricsTracesProviderWithIntegration resolves the provider/source
// pair for an account and also returns the integration DTO that produced the
// match (when one exists), so callers that need additional config from the
// same integration can avoid a second lookup.
func getLogsMetricsTracesProviderWithIntegration(ctx *security.RequestContext, accountId, logProviderFromRequest, providerType string, logSourceFromRequest string) (string, string, *core.IntegrationDto, error) {
	defaultProvider := logProviderFromRequest
	defaultSource := logSourceFromRequest
	var matchedIntegration *core.IntegrationDto
	valueProvider := ""
	switch providerType {
	case "logs":
		valueProvider = "default_log_provider"
	case "metrics":
		valueProvider = "default_metrics_provider"
	case "traces":
		valueProvider = "default_traces_provider"
	default:
		return "", "", nil, fmt.Errorf("unknown providerType: %q", providerType)
	}

	if defaultProvider == "" {
		integrationDto, err := core.GetIntegrationByConfigNameValues(ctx, accountId, valueProvider, "true")
		if err != nil {
			return "", "", nil, err
		}

		if integrationDto != nil {
			defaultProvider = integrationDto.Type
			defaultSource = integrationDto.Source
			matchedIntegration = integrationDto
		} else {
			agentDetails, err := account.GetAgentConnectionDetails(accountId)
			if err != nil {
				// GCP cloud accounts have no agent; their traces come from Cloud Trace.
				// Resolve them to the gcp trace source before treating the missing agent
				// as an error — this is the only trace path for a GCP cloud account.
				if providerType == "traces" {
					if cp, cErr := cloud.GetCloudAccountProvider(accountId, ctx.GetSecurityContext().GetTenantId()); cErr == nil && strings.EqualFold(cp, "gcp") {
						return "gcp", "user", nil, nil
					}
				}
				ctx.GetLogger().Error(fmt.Sprintf("unable to get agent details, for account %s", accountId), "error", err)
				return "", "", nil, err
			}
			if providerType == "logs" && agentDetails.Features.LogsConnectionProvider != nil {
				defaultProvider = *agentDetails.Features.LogsConnectionProvider
			} else if providerType == "traces" && agentDetails.Features.TraceProvider != nil {
				if agentDetails.Features.PrometheusUrl != nil && strings.Contains(*agentDetails.Features.PrometheusUrl, "chronosphere") {
					defaultProvider = "chronosphere"
				} else {
					defaultProvider = *agentDetails.Features.TraceProvider
				}
			} else if providerType == "metrics" && agentDetails.Features.PrometheusUrl != nil {
				if agentDetails.Features.PrometheusUrl != nil && strings.Contains(*agentDetails.Features.PrometheusUrl, "chronosphere") {
					defaultProvider = "chronosphere"
				} else {
					defaultProvider = "prometheus"
				}
			}
			defaultSource = "agent"
		}
	} else if defaultSource == "" {
		integrationDto, err := core.GetIntegrationByType(ctx, accountId, defaultProvider)
		if err != nil {
			ctx.GetLogger().Error("failed to look up source for provider", "provider", defaultProvider, "error", err)
			return "", "", nil, err
		}

		if integrationDto != nil {
			defaultSource = integrationDto.Source
			matchedIntegration = integrationDto
		} else {
			defaultSource = "agent"
		}
	}
	// GCP cloud accounts register a type=GCP agent row (the cloud-collector
	// connection), so GetAgentConnectionDetails succeeds but carries no trace
	// provider — the err-path synthesis above is never reached. Fall back to the
	// Cloud Trace source for any GCP cloud account still left without a traces
	// provider, so the account's Traces tab resolves instead of returning empty.
	if providerType == "traces" && defaultProvider == "" {
		if cp, cErr := cloud.GetCloudAccountProvider(accountId, ctx.GetSecurityContext().GetTenantId()); cErr == nil && strings.EqualFold(cp, "gcp") {
			return "gcp", "user", nil, nil
		}
	}
	return defaultProvider, defaultSource, matchedIntegration, nil
}

func FetchLogs(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) (FetchLogsResult, error) {
	source, err := getLogSourceForAccount(ctx, fetchLogRequest.AccountId, fetchLogRequest.LogProvider, fetchLogRequest.LogProviderSource)
	if err != nil {
		return FetchLogsResult{}, err
	}
	filteringMap := getMergedLabelMapping(ctx, fetchLogRequest.AccountId, source)
	fetchLogRequest.SortFields = convertOrderByWithMapping(fetchLogRequest.SortFields, filteringMap)
	fetchLogRequest.QueryRequest.Where = convertWhereClauseWithMApping(fetchLogRequest.QueryRequest.Where, filteringMap)

	// Let the integration strip keys it does not support from the where clause.
	// If the integration does not implement QueryRequestKeyFilter, nothing is removed.
	if filter, ok := source.(QueryRequestKeyFilter); ok {
		if ignoredKeys := filter.GetIgnoredQueryRequestKeys(); len(ignoredKeys) > 0 {
			fetchLogRequest.QueryRequest.Where = removeKeysFromWhereClause(fetchLogRequest.QueryRequest.Where, ignoredKeys)
		}
	}

	// Always-apply per-account default filters (e.g. a central Pinot scoped to
	// cluster_id) configured on the log integration. AND them into the where clause
	// here — after label-mapping and key-strip — so the operator-entered
	// provider-native columns are used verbatim (not renamed, not stripped).
	if defaults := getDefaultLogFilters(ctx, fetchLogRequest.AccountId); hasWhereData(defaults) {
		if hasWhereData(fetchLogRequest.QueryRequest.Where) {
			fetchLogRequest.QueryRequest.Where = query.QueryWhereClause{
				And: []query.QueryWhereClause{fetchLogRequest.QueryRequest.Where, defaults},
			}
		} else {
			fetchLogRequest.QueryRequest.Where = defaults
		}
	}

	// Auto-convert: if no raw query but where clause exists, generate query from where clause.
	// Some providers (e.g. Signoz, Datadog) handle where clauses natively in QueryLogs
	// and don't implement GetQuery, so a GetQuery error is logged but not fatal.
	if fetchLogRequest.Query == "" && hasWhereData(fetchLogRequest.QueryRequest.Where) {
		generatedQuery, err := source.GetQuery(ctx, fetchLogRequest)
		if err != nil {
			slog.Warn("FetchLogs: GetQuery failed, falling back to QueryLogs with where clause",
				"error", err, "account_id", fetchLogRequest.AccountId)
		} else if generatedQuery != "" {
			fetchLogRequest.Query = generatedQuery
		}
	}

	// Snapshot the label names the caller referenced BEFORE the query runs, so a
	// time range or other filter the backend injects during execution is not
	// mistaken for a user-supplied label during validation below.
	var referencedLabels map[string]struct{}
	if fetchLogRequest.ValidateRequest {
		referencedLabels = map[string]struct{}{}
		collectWhereFieldNames(fetchLogRequest.QueryRequest.Where, referencedLabels)
	}

	logs, err := source.QueryLogs(ctx, fetchLogRequest)

	// A query that matched nothing — or that failed with a backend error — is
	// frequently caused by a mistyped label NAME in the where-clause (e.g.
	// "service_nam" instead of "service_name"). Some backends silently return
	// zero rows for an unknown label (Loki); others reject the query (ClickHouse).
	// Either way the caller — notably an LLM agent — can't tell it should fix the
	// label name. When the caller opts in via ValidateRequest, cross-check the
	// referenced label names against the labels the provider exposes and, if any
	// are unknown, replace the result with an actionable message. Best-effort: if
	// every referenced label is recognized (or the label set can't be
	// determined), fall through to the original error / empty result.
	if fetchLogRequest.ValidateRequest && (err != nil || len(logs) == 0) {
		if verr := validateReferencedLabels(ctx, source, fetchLogRequest, referencedLabels, filteringMap); verr != nil {
			return FetchLogsResult{}, verr
		}
	}
	if err != nil {
		return FetchLogsResult{}, err
	}
	normalizeOutputLogLabels(logs, filteringMap)

	// Resolve the query that was actually used so callers (UI, LLM, runbooks)
	// can show it. fetchLogRequest.Query holds the raw query or the GetQuery
	// result set above. Providers that consume the where-clause natively emit
	// no query string, so fall back to the canonical where JSON.
	usedQuery := fetchLogRequest.Query
	if usedQuery == "" && hasWhereData(fetchLogRequest.QueryRequest.Where) {
		if b, mErr := json.Marshal(fetchLogRequest.QueryRequest.Where); mErr == nil {
			usedQuery = string(b)
		}
	}

	// Resolve the provider for the result. The canonical (v2) path sends an empty
	// LogProvider and lets us resolve the account default, so fall back to the
	// resolved default provider when the request didn't name one.
	provider := fetchLogRequest.LogProvider
	if provider == "" {
		if resolved, _, _, perr := getLogsMetricsTracesProviderWithIntegration(ctx, fetchLogRequest.AccountId, "", "logs", fetchLogRequest.LogProviderSource); perr == nil {
			provider = resolved
		}
	}
	return FetchLogsResult{Logs: logs, Query: usedQuery, Provider: provider}, nil
}

// lineContentFields are where-clause fields that filter the log message body
// rather than a label. They are never returned by the backends' label-key APIs,
// so they must be excluded from label-name validation to avoid false positives.
var lineContentFields = []string{"content", "log", "message", "body", "line", "_line"}

// collectWhereFieldNames walks a canonical where-clause and records every field
// name referenced by a binary condition, across nested _and / _or / _not.
func collectWhereFieldNames(where query.QueryWhereClause, out map[string]struct{}) {
	for field := range where.Binary {
		out[field] = struct{}{}
	}
	for _, c := range where.And {
		collectWhereFieldNames(c, out)
	}
	for _, c := range where.Or {
		collectWhereFieldNames(c, out)
	}
	if where.Not != nil {
		collectWhereFieldNames(*where.Not, out)
	}
}

// suggestLabels returns up to a few available labels that look related to an unknown
// one (case-insensitive substring, either direction), so a typo can be corrected
// without dumping the provider's full label list — which for trace backends can run to
// hundreds of attributes and waste caller (LLM) tokens.
func suggestLabels(unknown, available []string) []string {
	const maxSuggestions = 5
	seen := map[string]struct{}{}
	out := []string{}
	for _, u := range unknown {
		lu := strings.ToLower(u)
		for _, a := range available {
			la := strings.ToLower(a)
			if !strings.Contains(la, lu) && !strings.Contains(lu, la) {
				continue
			}
			if _, ok := seen[a]; ok {
				continue
			}
			seen[a] = struct{}{}
			out = append(out, a)
			if len(out) >= maxSuggestions {
				return out
			}
		}
	}
	return out
}

// unknownLabelError builds the actionable, token-conscious error returned when a query
// references label names the provider doesn't expose. It names the unknown label(s) and
// either the closest valid matches or a pointer to the label-listing action — never the
// full label list. noun is "logs"/"traces", providerNoun is "log"/"trace".
func unknownLabelError(noun, providerNoun, listAction string, unknown, available []string) error {
	if suggestions := suggestLabels(unknown, available); len(suggestions) > 0 {
		return fmt.Errorf("no %s matched: unknown label name(s) %v for this %s provider; closest valid label(s): %v", noun, unknown, providerNoun, suggestions)
	}
	return fmt.Errorf("no %s matched: unknown label name(s) %v for this %s provider; call %s for valid names", noun, unknown, providerNoun, listAction)
}

// unknownReferencedLabels partitions the referenced field names against the set the
// provider recognizes. A field is "known" if the backend exposes it as a label
// (availableLabels), it is an alias in the label mapping (either canonical or provider
// side), or it is a line-content filter. It returns the sorted unknown fields and the
// sorted available labels. Shared by the log and trace validators.
func unknownReferencedLabels(referenced map[string]struct{}, availableLabels []string, filteringMap map[string]string) (unknown, available []string) {
	known := make(map[string]struct{}, len(availableLabels)+len(filteringMap)*2+len(lineContentFields))
	available = make([]string, 0, len(availableLabels))
	for _, l := range availableLabels {
		known[l] = struct{}{}
		available = append(available, l)
	}
	for canonical, providerKey := range filteringMap {
		known[canonical] = struct{}{}
		known[providerKey] = struct{}{}
	}
	for _, f := range lineContentFields {
		known[f] = struct{}{}
	}
	for f := range referenced {
		if _, ok := known[f]; !ok {
			unknown = append(unknown, f)
		}
	}
	sort.Strings(unknown)
	sort.Strings(available)
	return unknown, available
}

// validateReferencedLabels checks, for a query that returned no logs (or failed),
// whether the caller's where-clause references label names the log provider does not
// expose. `referenced` is the set of field names snapshotted BEFORE the query ran, so
// filters the backend injects during execution (e.g. a time range) are excluded. It
// returns an actionable error naming the unknown label(s) and listing the available
// ones, or nil when every referenced field is recognized. Best-effort: if the
// available label set cannot be determined, it returns nil so the original error /
// empty result is preserved. The referenced fields are in provider label space
// (mapping was applied upstream in FetchLogs), matching the space QueryLabels returns.
func validateReferencedLabels(ctx *security.RequestContext, source LogSource, fetchLogRequest FetchLogRequest, referenced map[string]struct{}, filteringMap map[string]string) error {
	if len(referenced) == 0 {
		return nil
	}
	labels, err := source.QueryLabels(ctx, FetchLogLabelRequest{
		AccountId:         fetchLogRequest.AccountId,
		LogProvider:       fetchLogRequest.LogProvider,
		LogProviderSource: fetchLogRequest.LogProviderSource,
		StartTime:         fetchLogRequest.StartTime,
		EndTime:           fetchLogRequest.EndTime,
	})
	if err != nil || len(labels) == 0 {
		// Cannot determine the available label set — don't block the query.
		return nil
	}
	names := make([]string, len(labels))
	for i, l := range labels {
		names[i] = l.Label
	}
	unknown, available := unknownReferencedLabels(referenced, names, filteringMap)
	if len(unknown) == 0 {
		return nil
	}
	return unknownLabelError("logs", "log", "logs_list_labels", unknown, available)
}

// validateReferencedTraceLabels is the trace counterpart of validateReferencedLabels.
// It validates against the same authoritative label set FetchTraceLabels exposes — the
// canonical trace fields unioned with the merged label mapping and any backend-discovered
// keys — because raw QueryLabels omits the canonical columns (service_name, span_name, …)
// and would false-positive on them. mergedMapping is the caller's merged trace label
// mapping. Fails open when live label discovery errors.
func validateReferencedTraceLabels(ctx *security.RequestContext, source TraceSource, fetchTracesRequest TracesV3Request, referenced map[string]struct{}, mergedMapping map[string]string) error {
	if len(referenced) == 0 {
		return nil
	}
	discovered, err := source.QueryLabels(ctx, FetchTraceLabelRequest{
		AccountId:      fetchTracesRequest.AccountId,
		ProviderType:   fetchTracesRequest.ProviderType,
		ProviderSource: fetchTracesRequest.ProviderSource,
		StartTime:      fetchTracesRequest.StartTime,
		EndTime:        fetchTracesRequest.EndTime,
	})
	if err != nil {
		// Live discovery failed — can't confirm the backend label set; don't block.
		return nil
	}
	authoritative := buildTraceLabels(mergedMapping, discovered)
	names := make([]string, len(authoritative))
	for i, l := range authoritative {
		names[i] = l.Label
	}
	unknown, available := unknownReferencedLabels(referenced, names, mergedMapping)
	if len(unknown) == 0 {
		return nil
	}
	return unknownLabelError("traces", "trace", "traces_list_labels", unknown, available)
}

// normalizeOutputLogLabels adds canonical label names as aliases for provider-specific
// names in each log's Labels map. For example, Splunk stores the k8s namespace as
// "namespace_name" but the frontend expects "namespace". This ensures consistent label
// keys across all providers so features like +/- Logs can build drill-down queries.
func normalizeOutputLogLabels(logs []OutputLog, labelMapping map[string]string) {
	if len(labelMapping) == 0 {
		return
	}
	// Build reverse map: providerKey → canonicalKey
	reverseMap := make(map[string]string, len(labelMapping))
	for canonical, providerKey := range labelMapping {
		reverseMap[providerKey] = canonical
	}
	for i := range logs {
		if logs[i].Labels == nil {
			continue
		}
		for providerKey, canonical := range reverseMap {
			if val, ok := logs[i].Labels[providerKey]; ok {
				if _, exists := logs[i].Labels[canonical]; !exists {
					logs[i].Labels[canonical] = val
				}
			}
		}
	}
}

func hasWhereData(where query.QueryWhereClause) bool {
	return len(where.Binary) > 0 || len(where.And) > 0 || len(where.Or) > 0 || where.Not != nil
}

func FetchLogLabels(ctx *security.RequestContext, fetchLogRequest FetchLogLabelRequest) ([]OutputLogLabel, error) {
	source, err := getLogSourceForAccount(ctx, fetchLogRequest.AccountId, fetchLogRequest.LogProvider, fetchLogRequest.LogProviderSource)
	if err != nil {
		return nil, err
	}
	return source.QueryLabels(ctx, fetchLogRequest)
}

func FetchLogLabelValues(ctx *security.RequestContext, fetchLogRequest FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	source, err := getLogSourceForAccount(ctx, fetchLogRequest.AccountId, fetchLogRequest.LogProvider, fetchLogRequest.LogProviderSource)
	if err != nil {
		return nil, err
	}
	return source.QueryLabelValues(ctx, fetchLogRequest)
}

func FetchLogIndexFields(ctx *security.RequestContext, fetchLogRequest FetchLogLabelRequest) ([]OutputLogLabelFields, error) {
	resp, err := GetDefaultProvider(ctx, fetchLogRequest.AccountId, "logs", fetchLogRequest.LogProviderSource)
	if err != nil {
		return nil, err
	}

	if resp.Provider != "ES" {
		return nil, fmt.Errorf("FetchLogIndexFields is only supported for ES provider")
	}

	logSource, err := getLogSource(resp.Provider, resp.IntegrationSource)
	if err != nil {
		return nil, err
	}

	switch s := logSource.(type) {
	case *ElasticSource:
		return s.QueryIndexFields(ctx, fetchLogRequest)
	case *ElasticSaasSource:
		return s.QueryIndexFields(ctx, fetchLogRequest)
	default:
		return nil, fmt.Errorf("log source does not support QueryIndexFields")
	}
}

func FetchLogGroup(ctx *security.RequestContext, fetchLogGroupRequest FetchLogGroupRequest) (LogGroupOutput, error) {
	source, err := getLogGroupSourceForAccount(ctx, fetchLogGroupRequest.AccountId, fetchLogGroupRequest.LogProvider, fetchLogGroupRequest.LogProviderSource)
	if err != nil {
		return LogGroupOutput{}, err
	}
	return source.QueryLogGroup(ctx, fetchLogGroupRequest)
}

// providerStaticCaps holds all statically-declared boolean capabilities for a provider.
// All known providers are listed explicitly in allProviderCaps so omissions are intentional.
// SupportedOperators, SupportsAutoQuery, and SupportsLogGroups are runtime-detected
// via interface assertion on the resolved source (see getProviderCapabilities).
type providerStaticCaps struct {
	SupportsServiceMap             bool
	SupportsTraceGrouping          bool
	SupportsHeatmap                bool
	SupportsCrossZoneCommunication bool
	SupportsRawQuery               bool
}

var allProviderCaps = map[string]providerStaticCaps{
	"datadog": {
		SupportsServiceMap:    true,
		SupportsTraceGrouping: true,
		SupportsHeatmap:       true,
		SupportsRawQuery:      true,
	},
	"newrelic": {
		SupportsServiceMap:    true,
		SupportsTraceGrouping: true,
		SupportsHeatmap:       true,
		SupportsRawQuery:      true,
	},
	"dynatrace": {
		SupportsServiceMap:    false,
		SupportsTraceGrouping: true,
		SupportsHeatmap:       true,
		SupportsRawQuery:      true,
	},
	"splunk_observability_platform": {
		SupportsServiceMap:    true,
		SupportsTraceGrouping: true,
		SupportsHeatmap:       true,
		SupportsRawQuery:      true,
	},
	"solarwinds": {
		SupportsServiceMap:    false,
		SupportsRawQuery:      false,
		SupportsTraceGrouping: true,
		SupportsHeatmap:       true,
	},
	"azure_app_insights": {
		SupportsServiceMap: true,
		// QueryTracesHeatmap and QueryGroupedTraces both return "not implemented"
		SupportsHeatmap:       false,
		SupportsTraceGrouping: false,
		SupportsRawQuery:      true,
	},
	"jaeger": {
		SupportsServiceMap:    false,
		SupportsTraceGrouping: true,
		SupportsHeatmap:       true,
		SupportsRawQuery:      false,
	},
	"signoz": {
		SupportsServiceMap:    true,
		SupportsTraceGrouping: true,
		SupportsHeatmap:       true,
		SupportsRawQuery:      true,
	},
	"loki": {
		SupportsServiceMap: true,
		SupportsRawQuery:   true,
	},
	"ES": {
		SupportsServiceMap: true,
		SupportsRawQuery:   true,
	},
	"otel_clickhouse": {
		SupportsServiceMap:    true,
		SupportsTraceGrouping: true,
		SupportsHeatmap:       true,
		SupportsRawQuery:      true,
	},
	"observe": {
		SupportsServiceMap:    true,
		SupportsTraceGrouping: false,
		SupportsHeatmap:       true,
		SupportsRawQuery:      true,
	},
	"loggly": {
		SupportsServiceMap: true,
		SupportsRawQuery:   true,
	},
	"aws_cloudwatch": {
		SupportsServiceMap: true,
		// GetQuery returns "", nil — cannot produce a usable query
		SupportsRawQuery: false,
	},
	"chronosphere": {
		SupportsServiceMap: true,
		// QueryGroupedTraces returns "grouped traces not supported"
		SupportsTraceGrouping: false,
		SupportsHeatmap:       true,
		SupportsRawQuery:      false,
	},
	"prometheus": {
		SupportsServiceMap: true,
		SupportsRawQuery:   true,
	},
	"openobserve": {
		SupportsServiceMap:    true,
		SupportsRawQuery:      true,
		SupportsHeatmap:       false,
		SupportsTraceGrouping: false,
	},
}

func getProviderCapabilities(ctx *security.RequestContext, accountId, provider, integrationSource, providerType string) ProviderCapabilities {
	if provider == "" {
		return ProviderCapabilities{}
	}
	var caps ProviderCapabilities

	// Apply static per-provider capabilities.
	if s, ok := allProviderCaps[provider]; ok {
		caps.SupportsServiceMap = s.SupportsServiceMap
		caps.SupportsTraceGrouping = s.SupportsTraceGrouping
		caps.SupportsHeatmap = s.SupportsHeatmap
		caps.SupportsCrossZoneCommunication = s.SupportsCrossZoneCommunication
		caps.SupportsRawQuery = s.SupportsRawQuery
	}

	// Dynamic: log grouping is advertised iff the resolved source implements
	// LogGroupSource (covers LogSource impls and dedicated fallback sources
	// like PrometheusLogGroupSource / DynatraceLogGroupSource).
	if _, err := getLogGroupSource(provider, integrationSource); err == nil {
		caps.SupportsLogGroups = true
	}

	// Interface-derived capabilities: operator list, label mapping, optional interfaces.
	switch providerType {
	case "logs":
		source, err := getLogSource(provider, integrationSource)
		if err == nil {
			caps.SupportedOperators = source.GetSupportedOperators()
			_, caps.SupportsAutoQuery = source.(PlaybookQueryGenerator)
			// Full canonical→provider merge (static ∪ tenant ∪ account ∪ dynamic).
			// Skip the merge when accountId is empty: with no account the lookup
			// can only return the static defaults, so there's nothing to merge.
			if accountId != "" {
				caps.LabelMappings = getMergedLabelMapping(ctx, accountId, source)
			}
		} else {
			slog.Warn("getProviderCapabilities: failed to get log source", "provider", provider, "error", err)
		}
	case "traces":
		source, err := getTraceSource(provider, integrationSource)
		if err == nil {
			caps.SupportedOperators = source.GetSupportedOperators()
			// Full canonical→provider merge (static ∪ tenant ∪ account ∪ dynamic).
			// Skip the merge when accountId is empty: with no account the lookup
			// can only return the static defaults, so there's nothing to merge.
			if accountId != "" {
				caps.LabelMappings = getMergedTraceLabelMapping(ctx, accountId, source)
			} else {
				caps.LabelMappings = source.GetLabelMapping()
			}
		} else {
			slog.Warn("getProviderCapabilities: failed to get trace source", "provider", provider, "error", err)
		}
	case "metrics":
		source, err := getMetricsSource(provider, integrationSource)
		if err == nil {
			caps.SupportedOperators = source.GetSupportedOperators()
			// No metric label mapping today — metric sources don't implement
			// GetLabelMapping; LabelMappings stays empty for metrics.
		} else {
			slog.Warn("getProviderCapabilities: failed to get metrics source", "provider", provider, "error", err)
		}
	}

	caps.SupportedOperatorDescriptors = query.DescribeOperators(caps.SupportedOperators)
	return caps
}

func GetDefaultProvider(context *security.RequestContext, accountId, providerType string, providerSource string) (*DefaultProviderResponse, error) {
	defaultProvider, integrationSource, integrationDto, err := getLogsMetricsTracesProviderWithIntegration(context, accountId, "", providerType, providerSource)
	if err != nil {
		return nil, err
	}
	caps := getProviderCapabilities(context, accountId, defaultProvider, integrationSource, providerType)
	return &DefaultProviderResponse{
		Provider:           defaultProvider,
		IntegrationSource:  integrationSource,
		DefaultIndex:       readIndexFromIntegration(context, integrationDto, providerType),
		Capabilities:       caps,
		AvailableProviders: listAvailableProviders(context, accountId, providerType),
	}, nil
}

// listAvailableProviders returns the active providers that can serve the
// requested provider_type for the account — the account's active (non-disabled)
// user-configured integrations plus the agent-detected provider (loki / ES /
// prometheus / signoz / chronosphere, source "agent") — each with its supported
// query operators for that type. Capability is derived from the same source
// factories used by getProviderCapabilities (pure switch statements, no side
// effects), so non-observability integrations (slack, jira, …) resolve no
// source and are naturally excluded. Returns an empty slice on lookup failure —
// the available list is auxiliary and must never fail default-provider resolution.
func listAvailableProviders(context *security.RequestContext, accountId, providerType string) []AvailableProvider {
	servesType := func(provider, source string) bool {
		switch providerType {
		case "logs":
			_, err := getLogSource(provider, source)
			return err == nil
		case "traces":
			_, err := getTraceSource(provider, source)
			return err == nil
		case "metrics":
			_, err := getMetricsSource(provider, source)
			return err == nil
		default:
			return false
		}
	}

	seen := map[string]bool{}
	available := []AvailableProvider{}
	add := func(provider, source string) {
		if provider == "" || seen[provider] || !servesType(provider, source) {
			return
		}
		seen[provider] = true
		caps := getProviderCapabilities(context, accountId, provider, source, providerType)
		available = append(available, AvailableProvider{
			Provider:                     provider,
			SupportedOperators:           caps.SupportedOperators,
			SupportedOperatorDescriptors: caps.SupportedOperatorDescriptors,
		})
	}

	// User-configured integrations.
	integrations, err := core.ListActiveIntegrationsForAccount(context, accountId)
	if err != nil {
		context.GetLogger().Warn("listAvailableProviders: failed to list active integrations",
			"account_id", accountId, "error", err)
	}
	for _, integration := range integrations {
		add(integration.Type, integration.Source)
	}

	// Agent-detected provider (source "agent"). Mirrors the per-type resolution
	// in getLogsMetricsTracesProviderWithIntegration so the two paths can't drift.
	add(agentProviderForType(context, accountId, providerType), "agent")

	return available
}

// agentProviderForType resolves the provider name the connected agent exposes
// for the given provider_type, or "" when none is configured. Kept in sync with
// the agent branch of getLogsMetricsTracesProviderWithIntegration.
func agentProviderForType(context *security.RequestContext, accountId, providerType string) string {
	agentDetails, err := account.GetAgentConnectionDetails(accountId)
	if err != nil {
		// No connected agent (or lookup failure) — agent providers simply absent.
		context.GetLogger().Debug("agentProviderForType: no agent connection details",
			"account_id", accountId, "error", err)
		return ""
	}
	isChronosphere := agentDetails.Features.PrometheusUrl != nil &&
		strings.Contains(*agentDetails.Features.PrometheusUrl, "chronosphere")
	switch providerType {
	case "logs":
		if agentDetails.Features.LogsConnectionProvider != nil {
			return *agentDetails.Features.LogsConnectionProvider
		}
	case "traces":
		if agentDetails.Features.TraceProvider != nil {
			if isChronosphere {
				return "chronosphere"
			}
			return *agentDetails.Features.TraceProvider
		}
	case "metrics":
		if agentDetails.Features.PrometheusUrl != nil {
			if isChronosphere {
				return "chronosphere"
			}
			return "prometheus"
		}
	}
	return ""
}

// readIndexFromIntegration reads the log_index / metrics_index / trace_index
// config value from the supplied integration. Returns an empty string when
// no integration was matched or the entry is unset.
func readIndexFromIntegration(ctx *security.RequestContext, integrationDto *core.IntegrationDto, providerType string) string {
	if integrationDto == nil {
		return ""
	}
	var configName string
	switch providerType {
	case "logs":
		configName = "log_index"
	case "metrics":
		configName = "metrics_index"
	case "traces":
		configName = "trace_index"
	default:
		return ""
	}
	value, err := core.GetIntegrationConfigValueByName(ctx, integrationDto.Id, configName)
	if err != nil {
		ctx.GetLogger().Warn("readIndexFromIntegration: failed to read config value",
			"integration_id", integrationDto.Id, "name", configName, "error", err)
		return ""
	}
	return value
}

// ListProviderCapabilities returns the default provider capabilities for logs, traces, and metrics.
// It reuses GetLogsMetricsTracesProvider (the same logic as get_default_provider) for each type,
// so only the account's configured default integration per type is returned.
func ListProviderCapabilities(ctx *security.RequestContext, accountId string) ([]ProviderCapabilityEntry, error) {
	result := []ProviderCapabilityEntry{}
	for _, providerType := range []string{"logs", "traces", "metrics"} {
		provider, source, err := GetLogsMetricsTracesProvider(ctx, accountId, "", providerType, "")
		if err != nil || provider == "" {
			continue
		}
		caps := getProviderCapabilities(ctx, accountId, provider, source, providerType)
		result = append(result, ProviderCapabilityEntry{
			Provider:     provider,
			ProviderType: providerType,
			Capabilities: caps,
		})
	}
	return result, nil
}

func getTraceSourceForAccount(ctx *security.RequestContext, accountId string, traceProviderStr string, traceProviderSource string) (TraceSource, error) {
	if accountId == "" {
		return nil, fmt.Errorf("account_id is required")
	}

	traceProvider, integrationSource, err := GetLogsMetricsTracesProvider(ctx, accountId, traceProviderStr, "traces", traceProviderSource)
	if err != nil {
		return nil, err
	}

	if traceProvider == "" {
		return nil, fmt.Errorf("observability: trace provider (trace_provider) is required")
	}

	source, err := getTraceSource(traceProvider, integrationSource)
	return source, err
}

func GetTracesLabelValues(context *security.RequestContext, labelValuesRequest TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	if labelValuesRequest.AccountId == "" {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("account_id is required")
	}

	traceProvider, integrationSource, err := GetLogsMetricsTracesProvider(context, labelValuesRequest.AccountId, labelValuesRequest.ProviderType, "traces", labelValuesRequest.ProviderSource)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, err
	}

	if traceProvider == "" {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("GetTracesLabelValues: trace provider (trace_provider) is required")
	}
	source, err := getTraceSource(traceProvider, integrationSource)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, err
	}
	filteringMap := getMergedTraceLabelMapping(context, labelValuesRequest.AccountId, source)
	labelValuesRequest.QueryRequest.Where = convertWhereClauseWithMApping(labelValuesRequest.QueryRequest.Where, filteringMap)

	return source.GetLabelValues(context, labelValuesRequest)
}

// canonicalTraceField is a provider-independent trace field with its value type.
type canonicalTraceField struct {
	name string
	typ  string
}

// canonicalTraceFields is the provider-independent trace field vocabulary the
// query builder / trace agents can always filter on, regardless of the resolved
// backend. It is surfaced by FetchTraceLabels alongside any account/tenant
// trace_labels overrides.
var canonicalTraceFields = []canonicalTraceField{
	{"service_name", "string"},
	{"workload_name", "string"},
	{"span_name", "string"},
	{"trace_id", "string"},
	{"duration_ns", "integer"},
	{"http_status_code", "integer"},
	{"status_code", "string"},
	{"resource", "string"},
	{"destination_workload_name", "string"},
	{"destination_workload_namespace", "string"},
}

// FetchTraceLabels returns the trace labels usable for the account's resolved trace
// provider: the always-available canonical trace field set (typed) unioned with the
// merged label mapping keys (static ∪ tenant ∪ account ∪ dynamic) and, when the source
// supports it (TraceLabelKeysSource, e.g. otel_clickhouse), the label keys actually
// present in the backend for the time window. Deduped, canonical-first. Live discovery
// failures degrade gracefully to the derived set.
func FetchTraceLabels(context *security.RequestContext, request FetchTraceLabelRequest) (TraceLabelsResponse, error) {
	if request.AccountId == "" {
		return TraceLabelsResponse{}, fmt.Errorf("account_id is required")
	}

	traceProvider, integrationSource, err := GetLogsMetricsTracesProvider(context, request.AccountId, request.ProviderType, "traces", request.ProviderSource)
	if err != nil {
		return TraceLabelsResponse{}, err
	}
	if traceProvider == "" {
		return TraceLabelsResponse{}, fmt.Errorf("FetchTraceLabels: trace provider (trace_provider) is required")
	}
	source, err := getTraceSource(traceProvider, integrationSource)
	if err != nil {
		return TraceLabelsResponse{}, err
	}

	// Live per-provider label-key discovery; sources without a discovery API return an
	// empty slice. On error keep the derived set so the action never hard-fails on a
	// backend hiccup.
	discovered, keyErr := source.QueryLabels(context, request)
	if keyErr != nil {
		context.GetLogger().Warn("FetchTraceLabels: live label discovery failed, using derived labels", "provider", traceProvider, "error", keyErr)
		discovered = nil
	}

	merged := getMergedTraceLabelMapping(context, request.AccountId, source)
	return TraceLabelsResponse{Labels: buildTraceLabels(merged, discovered)}, nil
}

// buildTraceLabels returns the canonical trace field set unioned with the keys of the
// merged label mapping and any backend-discovered labels, deduped and canonical-first.
// Canonical fields carry their value type in attributes; mapping/discovered keys carry
// an empty (non-null) attributes object. Pure helper (no I/O) so the union/dedup
// behaviour is unit-testable.
func buildTraceLabels(mergedMapping map[string]string, discovered []OutputTraceLabel) []OutputTraceLabel {
	seen := make(map[string]struct{})
	labels := make([]OutputTraceLabel, 0, len(canonicalTraceFields)+len(mergedMapping)+len(discovered))
	appendLabel := func(key, typ string) {
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		// Attributes always defaults to an empty object (never null); canonical
		// fields carry their value type, mapping/discovered keys stay {}.
		attrs := map[string]any{}
		if typ != "" {
			attrs["type"] = typ
		}
		labels = append(labels, OutputTraceLabel{Label: key, Attributes: attrs})
	}

	for _, field := range canonicalTraceFields {
		appendLabel(field.name, field.typ)
	}
	// Mapping + discovered keys have no known type — attributes stay {}.
	for key := range mergedMapping {
		appendLabel(key, "")
	}
	for _, d := range discovered {
		appendLabel(d.Label, "")
	}

	return labels
}

func GetGroupedTraces(context *security.RequestContext, TraceQuery TracesV3Request) ([]TraceGroupingValues, error) {
	if TraceQuery.AccountId == "" {
		return []TraceGroupingValues{}, fmt.Errorf("account_id is required")
	}

	traceProvider, integrationSource, err := GetLogsMetricsTracesProvider(context, TraceQuery.AccountId, TraceQuery.ProviderType, "traces", TraceQuery.ProviderSource)
	if err != nil {
		return []TraceGroupingValues{}, err
	}

	if traceProvider == "" {
		return []TraceGroupingValues{}, fmt.Errorf("GetTracesLabelValues: trace provider (trace_provider) is required")
	}
	source, err := getTraceSource(traceProvider, integrationSource)
	if err != nil {
		return []TraceGroupingValues{}, err
	}
	filteringMap := source.GetLabelMapping()
	TraceQuery.QueryRequest.Where = convertWhereClauseWithMApping(TraceQuery.QueryRequest.Where, filteringMap)

	return source.QueryGroupedTraces(context, TraceQuery)
}

func GetGroupedTracesCount(context *security.RequestContext, TraceQuery TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	if TraceQuery.AccountId == "" {
		return common.OpenTelemetryTraceGroupCount{}, fmt.Errorf("account_id is required")
	}

	traceProvider, integrationSource, err := GetLogsMetricsTracesProvider(context, TraceQuery.AccountId, TraceQuery.ProviderType, "traces", TraceQuery.ProviderSource)
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, err
	}

	if traceProvider == "" {
		return common.OpenTelemetryTraceGroupCount{}, fmt.Errorf("GetGroupedTracesCount: trace provider (trace_provider) is required")
	}
	source, err := getTraceSource(traceProvider, integrationSource)
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, err
	}
	filteringMap := source.GetLabelMapping()
	TraceQuery.QueryRequest.Where = convertWhereClauseWithMApping(TraceQuery.QueryRequest.Where, filteringMap)

	return source.QueryGroupedTracesCount(context, TraceQuery)
}

func GetTraceHeatMap(context *security.RequestContext, TracesHeatMapRequest TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	if TracesHeatMapRequest.AccountId == "" {
		return []common.OpenTelemetryTraceHeatMap{}, fmt.Errorf("account_id is required")
	}

	traceProvider, integrationSource, err := GetLogsMetricsTracesProvider(context, TracesHeatMapRequest.AccountId, TracesHeatMapRequest.ProviderType, "traces", TracesHeatMapRequest.ProviderSource)
	if err != nil {
		return []common.OpenTelemetryTraceHeatMap{}, err
	}

	if traceProvider == "" {
		return []common.OpenTelemetryTraceHeatMap{}, fmt.Errorf("GetGroupedTracesCount: trace provider (trace_provider) is required")
	}
	source, err := getTraceSource(traceProvider, integrationSource)
	if err != nil {
		return []common.OpenTelemetryTraceHeatMap{}, err
	}

	return source.QueryTracesHeatmap(context, TracesHeatMapRequest)
}

func CountTraces(context *security.RequestContext, fetchTracesRequest TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	if fetchTracesRequest.AccountId == "" {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("account_id is required")
	}

	traceProvider, integrationSource, err := GetLogsMetricsTracesProvider(context, fetchTracesRequest.AccountId, fetchTracesRequest.ProviderType, "traces", fetchTracesRequest.ProviderSource)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}

	if traceProvider == "" {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("CountTraces trace provider (trace_provider) is required")
	}

	source, err := getTraceSource(traceProvider, integrationSource)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}
	filteringMap := source.GetLabelMapping()
	fetchTracesRequest.QueryRequest.Where = convertWhereClauseWithMApping(fetchTracesRequest.QueryRequest.Where, filteringMap)

	return source.CountTraces(context, fetchTracesRequest)
}

func GetTraces(context *security.RequestContext, fetchTracesRequest TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	if fetchTracesRequest.AccountId == "" {
		return nil, fmt.Errorf("account_id is required")
	}

	traceProvider, integrationSource, err := GetLogsMetricsTracesProvider(context, fetchTracesRequest.AccountId, fetchTracesRequest.ProviderType, "traces", fetchTracesRequest.ProviderSource)
	if err != nil {
		return nil, err
	}

	if traceProvider == "" {
		return nil, fmt.Errorf("GetTraces trace provider (trace_provider) is required")
	}
	source, err := getTraceSource(traceProvider, integrationSource)
	if err != nil {
		return nil, err
	}
	filteringMap := source.GetLabelMapping()
	fetchTracesRequest.QueryRequest.Where = convertWhereClauseWithMApping(fetchTracesRequest.QueryRequest.Where, filteringMap)

	var referencedLabels map[string]struct{}
	if fetchTracesRequest.ValidateRequest {
		referencedLabels = map[string]struct{}{}
		collectWhereFieldNames(fetchTracesRequest.QueryRequest.Where, referencedLabels)
	}

	traces, err := source.QueryTraces(context, fetchTracesRequest)
	if fetchTracesRequest.ValidateRequest && (err != nil || len(traces) == 0) {
		mergedMap := getMergedTraceLabelMapping(context, fetchTracesRequest.AccountId, source)
		if verr := validateReferencedTraceLabels(context, source, fetchTracesRequest, referencedLabels, mergedMap); verr != nil {
			return nil, verr
		}
	}
	if err != nil {
		return nil, err
	}
	return traces, nil
}

// GetRootSpansByTrace resolves the trace source and returns one root span per trace for the
// "By Traces" listing view. It mirrors GetTraces (provider resolution + label mapping) but
// dispatches to QueryRootSpansByTrace.
func GetRootSpansByTrace(context *security.RequestContext, fetchTracesRequest TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	if fetchTracesRequest.AccountId == "" {
		return nil, fmt.Errorf("account_id is required")
	}

	traceProvider, integrationSource, err := GetLogsMetricsTracesProvider(context, fetchTracesRequest.AccountId, fetchTracesRequest.ProviderType, "traces", fetchTracesRequest.ProviderSource)
	if err != nil {
		return nil, err
	}

	if traceProvider == "" {
		return nil, fmt.Errorf("GetRootSpansByTrace trace provider (trace_provider) is required")
	}
	source, err := getTraceSource(traceProvider, integrationSource)
	if err != nil {
		return nil, err
	}
	filteringMap := source.GetLabelMapping()
	fetchTracesRequest.QueryRequest.Where = convertWhereClauseWithMApping(fetchTracesRequest.QueryRequest.Where, filteringMap)

	return source.QueryRootSpansByTrace(context, fetchTracesRequest)
}

// CountTracesByTrace resolves the trace source and returns the distinct-trace count for the
// "By Traces" listing view. It mirrors CountTraces but dispatches to CountTracesByTrace.
func CountTracesByTrace(context *security.RequestContext, fetchTracesRequest TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	if fetchTracesRequest.AccountId == "" {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("account_id is required")
	}

	traceProvider, integrationSource, err := GetLogsMetricsTracesProvider(context, fetchTracesRequest.AccountId, fetchTracesRequest.ProviderType, "traces", fetchTracesRequest.ProviderSource)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}

	if traceProvider == "" {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("CountTracesByTrace trace provider (trace_provider) is required")
	}

	source, err := getTraceSource(traceProvider, integrationSource)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}
	filteringMap := source.GetLabelMapping()
	fetchTracesRequest.QueryRequest.Where = convertWhereClauseWithMApping(fetchTracesRequest.QueryRequest.Where, filteringMap)

	return source.CountTracesByTrace(context, fetchTracesRequest)
}

// GetTracesWithRawResult resolves the trace source and, when it is the otel ClickHouse source,
// returns the raw {columns, column_types, rows} result set instead of the typed span array. This
// backs the free-form agent trace path (IncludeRawResult) so aggregation / custom-projection
// queries return their real values rather than being zeroed by MapRowToOpenTelemetryTrace's
// fixed-schema coercion. A nil Result tells the caller to fall back to the typed GetTraces array
// (e.g. for a non-clickhouse provider). The underlying query executes exactly once.
func GetTracesWithRawResult(context *security.RequestContext, fetchTracesRequest TracesV3Request) (TracesQueryResult, error) {
	if fetchTracesRequest.AccountId == "" {
		return TracesQueryResult{}, fmt.Errorf("account_id is required")
	}

	traceProvider, integrationSource, err := GetLogsMetricsTracesProvider(context, fetchTracesRequest.AccountId, fetchTracesRequest.ProviderType, "traces", fetchTracesRequest.ProviderSource)
	if err != nil {
		return TracesQueryResult{}, err
	}
	if traceProvider == "" {
		return TracesQueryResult{}, fmt.Errorf("GetTracesWithRawResult trace provider (trace_provider) is required")
	}
	source, err := getTraceSource(traceProvider, integrationSource)
	if err != nil {
		return TracesQueryResult{}, err
	}

	// Only the otel ClickHouse source supports raw passthrough. Any other provider → nil Result,
	// caller falls back to the typed array.
	clickhouseSource, ok := source.(*OtelClickhouseTraceSource)
	if !ok {
		return TracesQueryResult{}, nil
	}

	filteringMap := source.GetLabelMapping()
	fetchTracesRequest.QueryRequest.Where = convertWhereClauseWithMApping(fetchTracesRequest.QueryRequest.Where, filteringMap)

	var referencedLabels map[string]struct{}
	if fetchTracesRequest.ValidateRequest {
		referencedLabels = map[string]struct{}{}
		collectWhereFieldNames(fetchTracesRequest.QueryRequest.Where, referencedLabels)
	}

	raw, err := clickhouseSource.QueryTracesRaw(context, fetchTracesRequest)
	if fetchTracesRequest.ValidateRequest && (err != nil || len(raw.Rows) == 0) {
		mergedMap := getMergedTraceLabelMapping(context, fetchTracesRequest.AccountId, source)
		if verr := validateReferencedTraceLabels(context, source, fetchTracesRequest, referencedLabels, mergedMap); verr != nil {
			return TracesQueryResult{}, verr
		}
	}
	if err != nil {
		return TracesQueryResult{}, err
	}
	return TracesQueryResult{Result: &raw}, nil
}

// Sorting option
type SortBy string

const (
	SortByErrorCount SortBy = "error_count"
	SortByCount      SortBy = "count"
	SortByP95Latency SortBy = "p95_latency"
	SortByP99Latency SortBy = "p99_latency"
	SortByMaxLatency SortBy = "max_latency"
)

func GetTracesQuery(context *security.RequestContext, fetchTracesRequest TracesV3Request) (string, error) {
	if fetchTracesRequest.AccountId == "" {
		return "", fmt.Errorf("account_id is required")
	}

	traceProvider, integrationSource, err := GetLogsMetricsTracesProvider(context, fetchTracesRequest.AccountId, fetchTracesRequest.ProviderType, "traces", fetchTracesRequest.ProviderSource)
	if err != nil {
		return "", err
	}

	if traceProvider == "" {
		return "", fmt.Errorf("GetTraces trace provider (trace_provider) is required")
	}
	source, err := getTraceSource(traceProvider, integrationSource)
	if err != nil {
		return "", err
	}

	return source.GetQuery(context, fetchTracesRequest)
}

func GetLogsQuery(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) (OutputLogQuery, error) {
	if fetchLogRequest.AccountId == "" {
		return OutputLogQuery{}, fmt.Errorf("account_id is required")
	}

	logProvider, integrationSource, err := GetLogsMetricsTracesProvider(ctx, fetchLogRequest.AccountId, fetchLogRequest.LogProvider, "logs", fetchLogRequest.LogProviderSource)
	if err != nil {
		return OutputLogQuery{}, err
	}

	if logProvider == "" {
		return OutputLogQuery{}, fmt.Errorf("GetLogsQuery log provider (log_provider) is required")
	}
	source, err := getLogSource(logProvider, integrationSource)
	if err != nil {
		return OutputLogQuery{}, err
	}
	// Apply label mapping before building query — mirror FetchLogs so the SQL-preview
	// endpoint translates both WHERE and ORDER BY identifiers consistently with execution.
	filteringMap := getMergedLabelMapping(ctx, fetchLogRequest.AccountId, source)
	fetchLogRequest.SortFields = convertOrderByWithMapping(fetchLogRequest.SortFields, filteringMap)
	fetchLogRequest.QueryRequest.Where = convertWhereClauseWithMApping(fetchLogRequest.QueryRequest.Where, filteringMap)
	query, err := source.GetQuery(ctx, fetchLogRequest)
	if err != nil {
		return OutputLogQuery{}, err
	}
	return OutputLogQuery{
		Query: query,
	}, nil
}

func FetchMetricsQuery(ctx *security.RequestContext, fetchMetricsRequest FetchMetricsRequest) (OutputMetricQuery, error) {
	source, err := getMetricsSourceForAccount(ctx, fetchMetricsRequest.AccountId, fetchMetricsRequest.MetricProvider, fetchMetricsRequest.MetricProviderSource)
	if err != nil {
		return OutputMetricQuery{}, err
	}
	return source.FetchMetricsQuery(ctx, fetchMetricsRequest)
}

// GetMetricsQuery renders BUILDER chips into PromQL strings. Input carries a
// QueryItems map (key → {metric, label_matchers}); output is a Results map
// (same keys → rendered PromQL). Each item is rendered independently so
// matchers from one block never leak into another. Returns the first
// per-item error to surface the offending key clearly.
func GetMetricsQuery(ctx *security.RequestContext, req FetchMetricsRequest) (FetchMetricQueryOutput, error) {
	source, err := getMetricsSourceForAccount(ctx, req.AccountId, req.MetricProvider, req.MetricProviderSource)
	if err != nil {
		return FetchMetricQueryOutput{}, err
	}
	results := make(map[string]string, len(req.QueryItems))
	for key, item := range req.QueryItems {
		perItem := req
		perItem.Queries = map[string]string{key: item.Metric}
		perItem.LabelMatchers = item.LabelMatchers
		perItem.QueryItems = nil
		perItem.Labels = nil
		query, qerr := source.GetQuery(ctx, perItem)
		if qerr != nil {
			return FetchMetricQueryOutput{}, fmt.Errorf("query %q: %w", key, qerr)
		}
		wrapped, werr := wrapPromQLAggregator(query, item.AggregateOperator)
		if werr != nil {
			return FetchMetricQueryOutput{}, fmt.Errorf("query %q: %w", key, werr)
		}
		results[key] = wrapped
	}
	return FetchMetricQueryOutput{Results: results}, nil
}

func FetchMetricsList(ctx *security.RequestContext, fetchMetricsListRequest FetchMetricsListRequest) ([]OutputMetrics, error) {
	source, err := getMetricsSourceForAccount(ctx, fetchMetricsListRequest.AccountId, fetchMetricsListRequest.MetricProvider, fetchMetricsListRequest.MetricProviderSource)
	if err != nil {
		return []OutputMetrics{}, err
	}
	output, err1 := source.FetchMetricList(ctx, fetchMetricsListRequest)
	if err1 != nil {
		return nil, err1
	}

	if fetchMetricsListRequest.Metric != "" {
		var filtered []OutputMetrics
		for _, m := range output {
			if strings.Contains(strings.ToLower(m.Metric), strings.ToLower(fetchMetricsListRequest.Metric)) {
				filtered = append(filtered, m)
			}
		}
		output = filtered
	}

	sort.Slice(output, func(i, j int) bool {
		return output[i].Metric < output[j].Metric
	})
	return output, nil
}

// FetchMetricSeries resolves which metric families have series for (namespace, workload)
// on the account's metrics provider. It requires the provider to implement the optional
// MetricSeriesSource capability; providers without a series-match equivalent return a
// clear "not supported" error rather than a silent empty result.
func FetchMetricSeries(ctx *security.RequestContext, fetchMetricSeriesRequest FetchMetricSeriesRequest) (MetricSeriesResult, error) {
	if fetchMetricSeriesRequest.AccountId == "" {
		return MetricSeriesResult{}, fmt.Errorf("observability: account_id is required for series-match")
	}
	if fetchMetricSeriesRequest.Workload == "" {
		return MetricSeriesResult{}, fmt.Errorf("observability: workload is required for series-match")
	}
	source, err := getMetricsSourceForAccount(ctx, fetchMetricSeriesRequest.AccountId, fetchMetricSeriesRequest.MetricProvider, fetchMetricSeriesRequest.MetricProviderSource)
	if err != nil {
		return MetricSeriesResult{}, err
	}
	seriesSource, ok := source.(MetricSeriesSource)
	if !ok {
		return MetricSeriesResult{}, fmt.Errorf("observability: series-match is not supported for metrics provider %q", fetchMetricSeriesRequest.MetricProvider)
	}
	return seriesSource.FetchMetricSeries(ctx, fetchMetricSeriesRequest)
}

func FetchMetricLabelValues(ctx *security.RequestContext, fetchMetricsLabelValueRequest FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	source, err := getMetricsSourceForAccount(ctx, fetchMetricsLabelValueRequest.AccountId, fetchMetricsLabelValueRequest.MetricProvider, fetchMetricsLabelValueRequest.MetricProviderSource)
	if err != nil {
		return []OutputMetricsLabelValues{}, err
	}
	output, err1 := source.FetchMetricLabelValues(ctx, fetchMetricsLabelValueRequest)
	if err1 != nil {
		return []OutputMetricsLabelValues{}, err1
	}

	if output == nil {
		return []OutputMetricsLabelValues{}, nil
	}

	sort.Slice(output, func(i, j int) bool {
		return output[i].Value < output[j].Value
	})
	return output, nil
}

func FetchMetricLabelsList(ctx *security.RequestContext, fetchMetricLabelListRequest FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	source, err := getMetricsSourceForAccount(ctx, fetchMetricLabelListRequest.AccountId, fetchMetricLabelListRequest.MetricProvider, fetchMetricLabelListRequest.MetricProviderSource)
	if err != nil {
		return []OutputMetricLabels{}, err
	}
	output, err1 := source.FetchMetricsLabels(ctx, fetchMetricLabelListRequest)
	if err1 != nil {
		return nil, err1
	}

	sort.Slice(output, func(i, j int) bool {
		return output[i].Label < output[j].Label
	})
	return output, nil
}

func FetchMetricUtilisation(ctx *security.RequestContext, req GetUtilisationTrendRequest) (OutputMetricQuery, error) {
	metricsProvider, integrationSource, err := GetLogsMetricsTracesProvider(ctx, req.AccountId, req.MetricProvider, "metrics", req.MetricProviderSource)
	if err != nil {
		return OutputMetricQuery{}, err
	}

	// --- 1. Extract Metadata ---
	meta, err := parseRequestMetadata(req.Request)
	if err != nil {
		return OutputMetricQuery{}, err
	}

	instant := false
	if v, ok := req.Request["instant"].(bool); ok {
		instant = v
	}

	// Cluster utilisation aggregations follow the picker range instead of a hardcoded
	// 24h window. Harmless for other providers/kinds, which ignore these fields.
	meta.RangeWindow, meta.Step, meta.RateWindow = promAggWindow(req.StartTime, req.EndTime)

	// The helper functions return a fully initialized map, so we don't need to allocate one here.
	var queries map[string]string
	// swRequest carries SolarWinds-specific filter/groupBy params passed as FetchMetricsRequest.Request.
	// Nil for all other providers (safe — no other MetricSource reads req.Request).
	var swRequest map[string]any

	// --- 2. Build Queries based on Provider and Kind ---
	switch metricsProvider {
	case "datadog":
		if meta.Kind == "node" {
			queries = buildDatadogNodeQueries(meta, meta.RequestedMetrics)
		} else {
			queries = buildDatadogWorkloadQueries(meta, meta.RequestedMetrics)
		}

	case "prometheus", "victoria_metrics", "chronosphere":
		if meta.Kind == "node" {
			queries = buildPrometheusNodeQueries(meta, meta.RequestedMetrics)
		} else {
			queries = buildPrometheusWorkloadQueries(meta, meta.RequestedMetrics)
		}

	case "newrelic":
		if meta.Kind == "node" {
			queries = buildNewRelicNodeQueries(meta, meta.RequestedMetrics)
		} else {
			queries = buildNewRelicWorkloadQueries(meta, meta.RequestedMetrics)
		}

	case "dynatrace":
		if meta.Kind == "node" {
			queries = buildDynatraceNodeQueries(meta, meta.RequestedMetrics)
		} else if meta.Namespace == "" && meta.Name == "" && meta.NodeName == "" {
			queries = buildDynatraceClusterQueries(meta.RequestedMetrics)
		} else {
			queries = buildDynatraceWorkloadQueries(meta, meta.RequestedMetrics)
		}

	case "solarwinds":
		clusterLevel := meta.Namespace == "" && meta.Name == "" && meta.NodeName == ""
		if meta.Kind == "node" {
			// Node queries use AVG: per-node utilization values are already singular series.
			queries = buildSolarWindsNodeQueries(meta.NodeName, meta.RequestedMetrics)
			swRequest = buildSolarWindsRequestParams(meta, buildSolarWindsGroupBy(meta), "AVG")
		} else if clusterLevel {
			// Cluster-level needs two separate API calls (different groupBy per metric group).
			// The helper handles both calls, merges results, and post-processes percentiles.
			return fetchSolarWindsClusterUtilisation(ctx, req, metricsProvider, integrationSource, meta, instant)
		} else {
			// Workload queries use SUM: pod-level metrics must be summed across all pods
			// belonging to the workload to yield total workload resource consumption.
			queries = buildSolarWindsWorkloadQueries(meta, meta.RequestedMetrics)
			swRequest = buildSolarWindsRequestParams(meta, buildSolarWindsGroupBy(meta), "SUM")
		}

	case "ES":
		return fetchESMetricUtilisation(ctx, req, meta)

	default:
		return OutputMetricQuery{}, fmt.Errorf("not supporting this metrics provider: %v", metricsProvider)
	}

	// Ensure queries is not nil before passing it (though helper functions should return empty map, not nil)
	if queries == nil {
		queries = make(map[string]string)
	}

	output, err := FetchMetricsQuery(ctx, FetchMetricsRequest{
		AccountId:            req.AccountId,
		MetricProvider:       metricsProvider,
		MetricProviderSource: integrationSource,
		Queries:              queries,
		StartTime:            req.StartTime,
		EndTime:              req.EndTime,
		Instant:              instant,
		Request:              swRequest,
	})
	if err != nil {
		return output, err
	}

	// Post-process Datadog percentile queries: collapse per-host series into a single percentile series
	if metricsProvider == "datadog" {
		percentileKeys := make(map[string]float64)
		for _, m := range meta.RequestedMetrics {
			switch m {
			case "p90_mem", "p90_cpu":
				percentileKeys[m] = 0.90
			case "p50_mem", "p50_cpu":
				percentileKeys[m] = 0.50
			}
		}
		if len(percentileKeys) > 0 {
			for i, qr := range output.Results {
				if pct, ok := percentileKeys[qr.QueryKey]; ok && len(qr.Payload) > 1 {
					output.Results[i].Payload = []Result{computePercentileFromSeries(qr.Payload, pct)}
				}
			}
		}
	}

	// Post-process Dynatrace cluster-wide metrics: collapse per-node series into a single series.
	// DQL queries for p90/p50/max use `by: {node}` to get entity-scoped data,
	// then we reduce across nodes here — same pattern as Datadog above.
	if metricsProvider == "dynatrace" {
		for i, qr := range output.Results {
			if len(qr.Payload) == 0 {
				continue
			}
			switch qr.QueryKey {
			case "p90_cpu", "p90_mem":
				output.Results[i].Payload = []Result{computePercentileFromSeries(qr.Payload, 0.90)}
			case "p50_cpu", "p50_mem":
				output.Results[i].Payload = []Result{computePercentileFromSeries(qr.Payload, 0.50)}
			case "max_usage_cpu", "max_usage_mem":
				output.Results[i].Payload = []Result{computePercentileFromSeries(qr.Payload, 1.0)}
			}
		}
	}

	return output, nil
}

// computePercentileFromSeries computes a percentile across multiple host series at each timestamp.
// It collects all values at each timestamp, sorts them, and picks the value at the given percentile.
func computePercentileFromSeries(series []Result, percentile float64) Result {
	// Collect all values grouped by timestamp
	tsValues := make(map[int64][]float64)
	var allTimestamps []int64

	for _, s := range series {
		for i, ts := range s.Timestamps {
			if i < len(s.Values) {
				if _, exists := tsValues[ts]; !exists {
					allTimestamps = append(allTimestamps, ts)
				}
				tsValues[ts] = append(tsValues[ts], s.Values[i])
			}
		}
	}

	sort.Slice(allTimestamps, func(i, j int) bool { return allTimestamps[i] < allTimestamps[j] })

	result := Result{
		Metric:     map[string]string{},
		Timestamps: make([]int64, 0, len(allTimestamps)),
		Values:     make([]float64, 0, len(allTimestamps)),
	}

	for _, ts := range allTimestamps {
		vals := tsValues[ts]
		if len(vals) == 0 {
			continue
		}
		sort.Float64s(vals)
		idx := percentile * float64(len(vals)-1)
		lower := int(math.Floor(idx))
		upper := int(math.Ceil(idx))
		if lower == upper || upper >= len(vals) {
			result.Values = append(result.Values, vals[lower])
		} else {
			// Linear interpolation between the two nearest values
			frac := idx - float64(lower)
			result.Values = append(result.Values, vals[lower]*(1-frac)+vals[upper]*frac)
		}
		result.Timestamps = append(result.Timestamps, ts)
	}

	return result
}

// --- Helper Structs & Parsing ---

type RequestMetadata struct {
	Namespace        string
	Name             string
	PVCName          string
	ContainerName    string
	Kind             string
	NodeName         string
	NodeIP           string
	InternalIP       string
	RequestedMetrics []string
	Regex            bool
	// Aggregation windows (Prometheus/MetricsQL duration literals) for cluster-level
	// utilisation queries, derived from the picker range so the time filter actually
	// adjusts the numbers. Empty for direct-constructed metadata (unit tests), where
	// the query builder falls back to a 24h window.
	RangeWindow string
	Step        string
	RateWindow  string
}

func parseRequestMetadata(reqMap map[string]any) (RequestMetadata, error) {
	m := RequestMetadata{}

	// Standard Workload fields
	if v, ok := reqMap["workload_namespace"]; ok {
		m.Namespace, _ = v.(string)
	}
	if v, ok := reqMap["workload_name"]; ok {
		m.Name, _ = v.(string)
	}
	if v, ok := reqMap["container_name"]; ok {
		m.ContainerName, _ = v.(string)
	} // Capture Container Name

	// Node fields
	if v, ok := reqMap["node_name"]; ok {
		m.NodeName, _ = v.(string)
	}
	if v, ok := reqMap["node_ip"]; ok {
		m.NodeIP, _ = v.(string)
	}
	if v, ok := reqMap["internal_ip"]; ok {
		m.InternalIP, _ = v.(string)
	}

	if kindRaw, ok := reqMap["kind"]; ok {
		m.Kind, _ = kindRaw.(string)
	}
	if v, ok := reqMap["pvc_name"]; ok {
		m.PVCName, _ = v.(string)
	}

	if v, ok := reqMap["regex"]; ok {
		m.Regex, _ = v.(bool)
	}

	if metricsRaw, ok := reqMap["metrics"]; ok {
		if slice, ok := metricsRaw.([]interface{}); ok {
			for _, v := range slice {
				if s, ok := v.(string); ok {
					m.RequestedMetrics = append(m.RequestedMetrics, s)
				}
			}
		} else if slice, ok := metricsRaw.([]string); ok {
			m.RequestedMetrics = slice
		}
	} else {
		return m, fmt.Errorf("request field 'metrics' is missing")
	}

	return m, nil
}

// --- DATADOG HELPERS ---

func buildDatadogNodeQueries(meta RequestMetadata, metrics []string) map[string]string {
	queries := make(map[string]string)
	if meta.NodeName == "" {
		return queries // Or return error if strict validation is needed
	}

	filterStr := fmt.Sprintf("host:%s", meta.NodeName)
	groupBy := " by {host}"

	for _, metricKey := range metrics {
		switch metricKey {
		case "cpu_usage":
			queries[metricKey] = fmt.Sprintf("avg:kubernetes.cpu.usage.total{%s}%s", filterStr, groupBy)
		case "memory_usage":
			queries[metricKey] = fmt.Sprintf("avg:system.mem.used{%s}%s", filterStr, groupBy)
		case "cpu_request":
			queries[metricKey] = fmt.Sprintf("avg:kubernetes.cpu.requests{%s}%s", filterStr, groupBy)
		case "cpu_limit":
			queries[metricKey] = fmt.Sprintf("avg:kubernetes.cpu.limits{%s}%s", filterStr, groupBy)
		case "memory_request":
			queries[metricKey] = fmt.Sprintf("avg:kubernetes.memory.requests{%s}%s", filterStr, groupBy)
		case "memory_limit":
			queries[metricKey] = fmt.Sprintf("avg:kubernetes.memory.limits{%s}%s", filterStr, groupBy)
		case "disk_total":
			queries[metricKey] = fmt.Sprintf("avg:system.disk.total{%s}%s", filterStr, groupBy)
		case "disk_used":
			queries[metricKey] = fmt.Sprintf("avg:system.disk.used{%s}%s", filterStr, groupBy)
		case "cpu_usage_line":
			queries[metricKey] = fmt.Sprintf("avg:kubernetes.cpu.usage.total{%s}%s", filterStr, groupBy)
		case "memory_usage_line":
			queries[metricKey] = fmt.Sprintf("avg:system.mem.used{%s}%s", filterStr, groupBy)
		case "pvc_usage":
			queries[metricKey] = fmt.Sprintf("(avg:system.disk.used{%s}%s / avg:system.disk.total{%s}%s) * 100", filterStr, groupBy, filterStr, groupBy)
		}
	}
	return queries
}

func buildDatadogWorkloadQueries(meta RequestMetadata, metrics []string) map[string]string {
	queries := make(map[string]string)

	var tagKey string
	switch meta.Kind {
	case "deployment":
		tagKey = "kube_deployment"
	case "statefulset":
		tagKey = "kube_stateful_set"
	case "daemonset":
		tagKey = "kube_daemon_set"
	case "pod":
		tagKey = "pod_name"
	default:
		tagKey = "kube_deployment"
	}

	var filterStr string
	if meta.Name != "" && meta.Namespace != "" {
		filterStr = fmt.Sprintf("kube_namespace:%s, %s:%s", meta.Namespace, tagKey, meta.Name)
	} else if meta.Namespace != "" {
		filterStr = fmt.Sprintf("kube_namespace:%s", meta.Namespace)
	}

	// --- NEW: Append Container Filter if present ---
	if filterStr != "" && meta.ContainerName != "" {
		filterStr = fmt.Sprintf("%s, kube_container_name:%s", filterStr, meta.ContainerName)
	}

	if filterStr == "" {
		return queries
	}

	groupBy := fmt.Sprintf(" by {%s}", tagKey)

	// PVC filter for Datadog
	var pvcFilterStr string
	if meta.Namespace != "" {
		if meta.PVCName != "" {
			pvcFilterStr = fmt.Sprintf("kube_namespace:%s, persistentvolumeclaim:%s", meta.Namespace, meta.PVCName)
		} else if meta.Name != "" {
			pvcFilterStr = fmt.Sprintf("kube_namespace:%s, persistentvolumeclaim:%s-*", meta.Namespace, meta.Name)
		} else {
			pvcFilterStr = fmt.Sprintf("kube_namespace:%s", meta.Namespace)
		}
	}

	for _, metricKey := range metrics {
		switch metricKey {
		// (Cases remain identical, they just use the updated filterStr)
		case "http_status":
			queries[metricKey] = fmt.Sprintf("sum:trace.servlet.request.hits{%s} by {http.status_code}.as_rate()", filterStr)
		case "http_max_response_time":
			queries[metricKey] = fmt.Sprintf("max:trace.servlet.request.duration{%s}%s", filterStr, groupBy)
		case "network_receive_packet":
			queries[metricKey] = fmt.Sprintf("avg:kubernetes.network.rx_bytes{%s}%s", filterStr, groupBy)
		case "network_transmit_packets":
			queries[metricKey] = fmt.Sprintf("avg:kubernetes.network.tx_bytes{%s}%s * -1", filterStr, groupBy)
		case "http_throughput":
			queries[metricKey] = fmt.Sprintf("sum:trace.servlet.request.hits{%s}%s.as_rate()", filterStr, groupBy)
		case "http_latency_p95":
			queries[metricKey] = fmt.Sprintf("p95:trace.servlet.request.duration{%s}%s", filterStr, groupBy)
		case "http_latency_p99":
			queries[metricKey] = fmt.Sprintf("p99:trace.servlet.request.duration{%s}%s", filterStr, groupBy)
		case "http_latency_sum":
			queries[metricKey] = fmt.Sprintf("sum:trace.servlet.request.duration{%s}%s", filterStr, groupBy)
		case "http_error_rate":
			queries[metricKey] = fmt.Sprintf("(sum:trace.servlet.request.errors{%s}%s / sum:trace.servlet.request.hits{%s}%s) * 100", filterStr, groupBy, filterStr, groupBy)
		case "network_usage":
			queries[metricKey] = fmt.Sprintf("default(avg:container.net.tcp.connection.time.seconds.total{%s}%s, avg:kubernetes.network.rx_bytes{%s}%s)", filterStr, groupBy, filterStr, groupBy)
		case "cpu_usage":
			queries[metricKey] = fmt.Sprintf("avg:kubernetes.cpu.usage.total{%s}%s", filterStr, groupBy)
		case "cpu_request":
			queries[metricKey] = fmt.Sprintf("avg:kubernetes.cpu.requests{%s}%s", filterStr, groupBy)
		case "cpu_limit":
			queries[metricKey] = fmt.Sprintf("avg:kubernetes.cpu.limits{%s}%s", filterStr, groupBy)
		case "memory_usage":
			queries[metricKey] = fmt.Sprintf("avg:kubernetes.memory.working_set{%s}%s", filterStr, groupBy)
		case "memory_request":
			queries[metricKey] = fmt.Sprintf("avg:kubernetes.memory.requests{%s}%s", filterStr, groupBy)
		case "memory_limit":
			queries[metricKey] = fmt.Sprintf("avg:kubernetes.memory.limits{%s}%s", filterStr, groupBy)

		// --- PVC Metrics ---
		case "pvc_usage":
			if pvcFilterStr != "" {
				queries[metricKey] = fmt.Sprintf("sum:kubernetes.kubelet.volume.stats.used_bytes{%s}", pvcFilterStr)
			}
		case "pvc_requests":
			if pvcFilterStr != "" {
				queries[metricKey] = fmt.Sprintf("sum:kubernetes_state.persistentvolumeclaim.request_storage{%s}", pvcFilterStr)
			}

		// --- Node/Cluster Aggregations ---
		case "cpu_real":
			queries[metricKey] = "sum:kubernetes.cpu.usage.total{*}"
		case "cpu_total":
			queries[metricKey] = "sum:kubernetes_state.node.cpu_capacity{*}"
		case "mem_real":
			queries[metricKey] = "sum:kubernetes.memory.usage{*}"
		case "mem_total":
			queries[metricKey] = "sum:kubernetes_state.node.memory_capacity{*}"
		case "p90_mem":
			queries[metricKey] = "avg:system.mem.used{*} by {host}"
		case "p90_cpu":
			queries[metricKey] = "avg:kubernetes.cpu.usage.total{*} by {host}"
		case "p50_mem":
			queries[metricKey] = "avg:system.mem.used{*} by {host}"
		case "p50_cpu":
			queries[metricKey] = "avg:kubernetes.cpu.usage.total{*} by {host}"
		case "max_usage_mem":
			queries[metricKey] = "max:system.mem.used{*}"
		case "max_usage_cpu":
			queries[metricKey] = "max:kubernetes.cpu.usage.total{*}"
		case "replica_defined":
			queries[metricKey] = fmt.Sprintf("sum:kubernetes_state.replicaset.replicas_desired{kube_namespace:%s, kube_replica_set:%s-*}", meta.Namespace, meta.Name)
		case "replica_ready":
			queries[metricKey] = fmt.Sprintf("sum:kubernetes_state.replicaset.replicas_ready{kube_namespace:%s, kube_replica_set:%s-*}", meta.Namespace, meta.Name)
		}
	}
	return queries
}

// --- PROMETHEUS HELPERS ---

func buildPrometheusNodeQueries(meta RequestMetadata, metrics []string) map[string]string {
	queries := make(map[string]string)

	// Escape the node identity fields before they are interpolated into PromQL,
	// mirroring the safeMeta sanitisation in buildPrometheusWorkloadQueries. These
	// originate from request input, so an unescaped quote could otherwise break out
	// of the query string. meta is a value copy, so reassigning it is local.
	meta.InternalIP = escapePromQLString(meta.InternalIP)
	meta.NodeName = escapePromQLString(meta.NodeName)
	meta.NodeIP = escapePromQLString(meta.NodeIP)

	for _, metricKey := range metrics {
		switch metricKey {
		case "cpu_usage":
			queries[metricKey] = fmt.Sprintf(`sum(irate(node_cpu_seconds_total{mode!="idle", instance=~"%s.*"}[5m])) OR sum(irate(node_resources_cpu_usage_seconds_total{mode!="idle", instance=~"%s.*"}[5m]))`, meta.InternalIP, meta.NodeName)
		case "memory_usage":
			queries[metricKey] = fmt.Sprintf(`sum(node_memory_Active_bytes{instance=~"%s.*"}) or sum(node_resources_memory_total_bytes{instance=~"%s.*"} - node_resources_memory_available_bytes{instance=~"%s.*"})`, meta.InternalIP, meta.NodeName, meta.NodeName)
		case "cpu_request":
			queries[metricKey] = fmt.Sprintf(`sum(kube_pod_container_resource_requests{resource="cpu", node=~"%s.*"})`, meta.NodeName)
		case "memory_request":
			queries[metricKey] = fmt.Sprintf(`sum(kube_pod_container_resource_requests{resource="memory", node=~"%s.*"})`, meta.NodeName)
		case "cpu_limit":
			queries[metricKey] = fmt.Sprintf(`sum(kube_pod_container_resource_limits{resource="cpu", node=~"%s.*"})`, meta.NodeName)
		case "memory_limit":
			queries[metricKey] = fmt.Sprintf(`sum(kube_pod_container_resource_limits{resource="memory", node=~"%s.*"})`, meta.NodeName)
		case "disk_total":
			queries[metricKey] = fmt.Sprintf(`sum(node_filesystem_size_bytes{mountpoint="/", instance=~"%s.*"}) or sum(kubelet_volume_stats_capacity_bytes{instance=~"%s.*"}) or sum(kubelet_volume_stats_capacity_bytes{instance=~"%s.*"})`, meta.InternalIP, meta.NodeName, meta.NodeIP)
		case "disk_used":
			queries[metricKey] = fmt.Sprintf(`(sum(node_filesystem_size_bytes{mountpoint="/", instance=~"%s.*"}) - sum(node_filesystem_free_bytes{mountpoint="/", instance=~"%s.*"})) or (sum(kubelet_volume_stats_capacity_bytes{instance=~"%s.*"}) - sum(kubelet_volume_stats_available_bytes{instance=~"%s.*"})) or (sum(kubelet_volume_stats_capacity_bytes{instance=~"%s.*"}) - sum(kubelet_volume_stats_available_bytes{instance=~"%s.*"}))`, meta.InternalIP, meta.InternalIP, meta.NodeName, meta.NodeName, meta.NodeIP, meta.NodeIP)
		case "cpu_usage_line":
			// node-agent labels node_resources_cpu_usage_seconds_total with `instance` (= node name),
			// not `node`; the last fallback must match on instance or it returns empty when
			// node-exporter (node_cpu_seconds_total) is absent and CPU renders as 0%.
			queries[metricKey] = fmt.Sprintf(`sum by (instance) (rate(node_cpu_seconds_total{mode!="idle", instance=~"%s|%s"}[5m])) or (sum by (node) (rate(node_cpu_seconds_total{mode!="idle", node=~"%s"}[5m]))) or (sum by (instance) (rate(node_resources_cpu_usage_seconds_total{mode!="idle", instance=~"%s"}[5m])))`, meta.InternalIP, meta.NodeName, meta.NodeName, meta.NodeName)
		case "memory_usage_line":
			queries[metricKey] = fmt.Sprintf(`(avg(node_memory_MemTotal_bytes{instance=~"%s|%s"} - node_memory_MemAvailable_bytes{instance=~"%s|%s"}) by (instance)) or (avg(node_resources_memory_total_bytes{instance=~"%s"} - node_resources_memory_available_bytes{instance=~"%s"}) by (instance)) or (avg(node_memory_MemTotal_bytes{node=~"%s"} - node_memory_MemAvailable_bytes{node=~"%s"}) by (node)) or (avg(node_resources_memory_total_bytes{node=~"%s"} - node_resources_memory_available_bytes{node=~"%s"}) by (node))`, meta.InternalIP, meta.NodeName, meta.InternalIP, meta.NodeName, meta.NodeName, meta.NodeName, meta.NodeName, meta.NodeName, meta.NodeName, meta.NodeName)
		case "pvc_usage":
			queries[metricKey] = fmt.Sprintf(`((1 - node_filesystem_free_bytes{ __CLUSTER__ instance=~"%s.*", fstype !~"tmpfs"} / node_filesystem_size_bytes{ __CLUSTER__ instance=~"%s.*", fstype !~"tmpfs"}) * 100) or (kubelet_volume_stats_used_bytes{ __CLUSTER__ instance=~"%s.*"}  * 100/ kubelet_volume_stats_capacity_bytes{ __CLUSTER__ instance=~"%s.*"})`, meta.NodeIP, meta.NodeIP, meta.NodeName, meta.NodeName)
		case "node_az":
			queries[metricKey] = `count(karpenter_nodes_total_pod_requests{ __CLUSTER__ provisioner_name="",resource_type="pods"}) by (zone)`
		case "pod_az":
			queries[metricKey] = `sum(karpenter_pods_state{ __CLUSTER__ provisioner=""}) by (zone)`
		case "no_of_pods":
			queries[metricKey] = `sum(karpenter_pods_state{ __CLUSTER__ provisioner="", name=~".*-[0-9]+.*"})`
		case "node_pool_pod_trend":
			queries[metricKey] = `sum by (nodepool)(karpenter_pods_state{__CLUSTER__})`
		case "nodeclaims_disrupted":
			queries[metricKey] = `round(sum(increase(karpenter_nodeclaims_disrupted_total{__CLUSTER__}[1h])) by (nodepool, capacity_type, reason))`
		case "node_created_node_pool":
			queries[metricKey] = `round(sum(increase(karpenter_nodes_created_total{__CLUSTER__}[1h])) by (nodepool))`
		case "nodes_terminated_node_pool":
			queries[metricKey] = `round(sum(increase(karpenter_nodes_terminated_total{__CLUSTER__}[1h])) by (nodepool))`
		case "node_disruption_decisions_reason_decision":
			queries[metricKey] = `round(sum(increase(karpenter_voluntary_disruption_decisions_total{__CLUSTER__}[1h])) by (decision, reason))`
		case "nodes_eligible_disruption_reason":
			queries[metricKey] = `round(sum(increase(karpenter_voluntary_disruption_eligible_nodes{__CLUSTER__}[1h])) by (reason))`
		case "network_receive_packet":
			queries[metricKey] = fmt.Sprintf(`sum(irate(node_network_receive_packets_total{__CLUSTER__ instance=~"%s.*", device!~"lo|veth.*|docker.*|flannel.*|cali.*|cbr.*"}[5m])) or sum(irate(node_network_receive_packets_total{__CLUSTER__ instance=~"%s.*", device!~"lo|veth.*|docker.*|flannel.*|cali.*|cbr.*"}[5m])) or sum(irate(node_network_receive_packets_total{__CLUSTER__ instance=~"%s.*", device!~"lo|veth.*|docker.*|flannel.*|cali.*|cbr.*"}[5m]))`, meta.InternalIP, meta.NodeName, meta.NodeIP)
		case "network_transmit_packets":
			queries[metricKey] = fmt.Sprintf(`sum(irate(node_network_transmit_packets_total{__CLUSTER__ instance=~"%s.*", device!~"lo|veth.*|docker.*|flannel.*|cali.*|cbr.*"}[5m])) or sum(irate(node_network_transmit_packets_total{__CLUSTER__ instance=~"%s.*", device!~"lo|veth.*|docker.*|flannel.*|cali.*|cbr.*"}[5m])) or sum(irate(node_network_transmit_packets_total{__CLUSTER__ instance=~"%s.*", device!~"lo|veth.*|docker.*|flannel.*|cali.*|cbr.*"}[5m]))`, meta.InternalIP, meta.NodeName, meta.NodeIP)
		}
	}
	return queries
}

// promAggWindow derives the subquery range, step and inner-rate windows (as
// Prometheus/MetricsQL duration literals) for cluster utilisation aggregations from
// the picker's start/end (unix millis). The window follows the picker so the usage,
// P50/P90/Max and the usage-trend sparkline reflect the selected range instead of a
// hardcoded 24h. Falls back to 24h when the range is missing or invalid.
func promAggWindow(startMs, endMs int64) (rangeStr, stepStr, rateStr string) {
	rangeSec := (endMs - startMs) / 1000
	if rangeSec <= 0 {
		rangeSec = 24 * 3600
	}
	// ~300 sample points across the range, clamped so short ranges keep a 1m
	// resolution and long ranges don't explode the subquery point count.
	stepSec := rangeSec / 300
	if stepSec < 60 {
		stepSec = 60
	}
	if stepSec > 1800 {
		stepSec = 1800
	}
	// Inner rate window: at least 5m (a few scrape intervals) and never finer than
	// the step, so each sampled point covers its whole interval.
	rateSec := stepSec
	if rateSec < 300 {
		rateSec = 300
	}
	return fmt.Sprintf("%ds", rangeSec), fmt.Sprintf("%ds", stepSec), fmt.Sprintf("%ds", rateSec)
}

func buildPrometheusWorkloadQueries(meta RequestMetadata, metrics []string) map[string]string {
	queries := make(map[string]string)

	safeMeta := meta
	safeMeta.InternalIP = escapePromQLString(meta.InternalIP)
	safeMeta.Namespace = escapePromQLString(meta.Namespace)
	safeMeta.Name = escapePromQLString(meta.Name)
	safeMeta.ContainerName = escapePromQLString(meta.ContainerName)
	safeMeta.PVCName = escapePromQLString(meta.PVCName)

	// --- 1. Construct Filters ---
	var basePodFilter, containerFilter, containerIDFilter string

	if safeMeta.Namespace != "" {
		// Define the Pod Matcher based on Kind
		var podMatcher string
		if safeMeta.Name != "" {
			if safeMeta.Kind == "pod" {
				podMatcher = fmt.Sprintf(`pod="%s"`, safeMeta.Name)
			} else {
				// Regex match for deployments/statefulsets
				podMatcher = fmt.Sprintf(`pod=~"%s-.*"`, safeMeta.Name)
			}
			basePodFilter = fmt.Sprintf(` namespace="%s", %s`, safeMeta.Namespace, podMatcher)

			// Handle Container Filter Logic
			if safeMeta.ContainerName != "" {
				containerFilter = fmt.Sprintf(`%s, container="%s"`, basePodFilter, safeMeta.ContainerName)
				containerIDFilter = fmt.Sprintf(` container_id=~"/k8s/%s/%s/.*"`, safeMeta.Namespace, safeMeta.Name)
			} else {
				// --- FIX: This ELSE block was missing ---
				// If no container_name is provided, we still need a filter (usually excluding empty containers)
				containerFilter = fmt.Sprintf(`%s, container!=""`, basePodFilter)
				containerIDFilter = fmt.Sprintf(` container_id=~"/k8s/%s/%s/.*"`, safeMeta.Namespace, safeMeta.Name)
			}

		} else {
			// Namespace only case
			basePodFilter = fmt.Sprintf(` namespace="%s"`, safeMeta.Namespace)
			if safeMeta.ContainerName != "" {
				containerFilter = fmt.Sprintf(`%s, container="%s"`, basePodFilter, safeMeta.ContainerName)
			} else {
				containerFilter = basePodFilter // Direct assignment since basePodFilter is string
			}
			containerIDFilter = fmt.Sprintf(` container_id=~"/k8s/%s/.*"`, safeMeta.Namespace)
		}
	}

	// --- 2. Destination Filters ---
	var destFilter, actualDestFilter string
	if safeMeta.Namespace != "" && safeMeta.Name != "" {
		if safeMeta.Regex {
			destFilter = fmt.Sprintf(` destination_workload_namespace=~"%s", destination_workload_name=~"%s"`, safeMeta.Namespace, safeMeta.Name)
		} else {
			destFilter = fmt.Sprintf(` destination_workload_namespace="%s", destination_workload_name="%s"`, safeMeta.Namespace, safeMeta.Name)
		}
		actualDestFilter = fmt.Sprintf(` actual_destination_workload_namespace="%s", actual_destination_workload_name=~"%s.*"`, safeMeta.Namespace, safeMeta.Name)
	} else if safeMeta.Namespace != "" {
		if safeMeta.Regex {
			destFilter = fmt.Sprintf(` destination_workload_namespace=~"%s"`, safeMeta.Namespace)
		} else {
			destFilter = fmt.Sprintf(` destination_workload_namespace="%s"`, safeMeta.Namespace)
		}
		actualDestFilter = fmt.Sprintf(` actual_destination_workload_namespace="%s"`, safeMeta.Namespace)
	}

	// --- 3. PVC Filters ---
	var pvcFilter string
	if safeMeta.Namespace != "" {
		if safeMeta.PVCName != "" {
			pvcFilter = fmt.Sprintf(` namespace="%s", persistentvolumeclaim="%s"`, safeMeta.Namespace, safeMeta.PVCName)
		} else if safeMeta.Name != "" {
			pvcFilter = fmt.Sprintf(` namespace="%s", persistentvolumeclaim=~"%s.*"`, safeMeta.Namespace, safeMeta.Name)
		} else {
			pvcFilter = fmt.Sprintf(` namespace="%s"`, safeMeta.Namespace)
		}
	}

	// --- 4. Append Trailing Commas ---
	if basePodFilter != "" {
		basePodFilter += ","
	}
	if containerFilter != "" {
		containerFilter += ","
	}
	if containerIDFilter != "" {
		containerIDFilter += ","
	}
	if destFilter != "" {
		destFilter += ","
	}
	if actualDestFilter != "" {
		actualDestFilter += ","
	}
	if pvcFilter != "" {
		pvcFilter += ","
	}

	// Cluster-level aggregation windows, derived from the picker range (empty for
	// unit-tested metadata -> fall back to a 24h window / 5m resolution).
	rangeW := safeMeta.RangeWindow
	if rangeW == "" {
		rangeW = "24h"
	}
	stepW := safeMeta.Step
	if stepW == "" {
		stepW = "5m"
	}
	rateW := safeMeta.RateWindow
	if rateW == "" {
		rateW = "5m"
	}

	// --- 5. Build Queries ---
	for _, metricKey := range metrics {
		switch metricKey {
		// --- PVC Metrics ---
		case "pvc_usage":
			queries[metricKey] = fmt.Sprintf(`sum(kubelet_volume_stats_used_bytes{__CLUSTER__ %s})`, pvcFilter)
		case "pvc_requests":
			queries[metricKey] = fmt.Sprintf(`sum(kube_persistentvolumeclaim_resource_requests_storage_bytes{__CLUSTER__ %s})`, pvcFilter)

		// --- HTTP / Network ---
		case "http_status":
			queries[metricKey] = fmt.Sprintf(`sum by (actual_destination_workload_namespace, status) (rate(container_http_requests_total{__CLUSTER__ %sjob!=""}[5m]))`, actualDestFilter)
		case "http_max_response_time":
			queries[metricKey] = fmt.Sprintf(`max by (actual_destination_workload_namespace) (max_over_time(container_net_tcp_connection_time_seconds_total{__CLUSTER__ %sjob!=""}[5m]))`, actualDestFilter)
		case "http_throughput":
			queries[metricKey] = fmt.Sprintf(`sort_desc(sum by(method, path, destination_workload_name, destination_workload_namespace)(increase(container_http_requests_total{__CLUSTER__ %sjob!=""}[1h])))`, destFilter)
		case "http_latency_p95":
			queries[metricKey] = fmt.Sprintf(`histogram_quantile(0.95, sum by(le, path, method, destination_workload_name, destination_workload_namespace) (increase(container_http_requests_duration_seconds_total_bucket{__CLUSTER__ %sjob!=""}[1h])))`, destFilter)
		case "http_latency_p99":
			queries[metricKey] = fmt.Sprintf(`histogram_quantile(0.99, sum by(le, path, method, destination_workload_name, destination_workload_namespace) (increase(container_http_requests_duration_seconds_total_bucket{__CLUSTER__ %sjob!=""}[1h])))`, destFilter)
		case "http_latency_sum":
			queries[metricKey] = fmt.Sprintf(`sum by (path, method, destination_workload_name, destination_workload_namespace) (increase(container_http_requests_duration_seconds_total_sum{__CLUSTER__ %sjob!=""}[1h]))`, destFilter)
		case "http_error_rate":
			queries[metricKey] = fmt.Sprintf(`(sum by(method, path, destination_workload_name, destination_workload_namespace)(increase(container_http_requests_total{__CLUSTER__ %sstatus=~"^[45]..$"}[1h])) / sum(increase(container_http_requests_total{__CLUSTER__ %sjob!=""}[1h]))) * 100`, destFilter, destFilter)

		// --- Network Packet Logic ---
		// Network metrics are pod-scoped: cAdvisor's container_network_*_bytes_total series carry an
		// empty `container` label, so the container!="" constraint in containerFilter filters them all
		// out. Use basePodFilter (namespace + pod matcher only) instead.
		case "network_receive_packet":
			queries[metricKey] = fmt.Sprintf(`(sum(rate(container_network_receive_bytes_total{__CLUSTER__ %sjob!=""}[5m]))) or (sum(rate(container_net_tcp_bytes_received_total{__CLUSTER__ %sjob!=""}[5m])))`, basePodFilter, containerIDFilter)
		case "network_transmit_packets":
			queries[metricKey] = fmt.Sprintf(`-((sum(rate(container_network_transmit_bytes_total{__CLUSTER__ %sjob!=""}[5m]))) or (sum(rate(container_net_tcp_bytes_sent_total{__CLUSTER__ %sjob!=""}[5m]))))`, basePodFilter, containerIDFilter)
		case "network_usage":
			queries[metricKey] = fmt.Sprintf(`sum(container_net_tcp_connection_time_seconds_total{__CLUSTER__ %scontainer!=""}) or sum(kube_network_rx_bytes{__CLUSTER__ %scontainer!=""})`, containerFilter, basePodFilter)

		// --- Resources ---
		case "cpu_usage":
			queries[metricKey] = fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{__CLUSTER__ %s}[5m]))`, containerFilter)
		case "cpu_request":
			queries[metricKey] = fmt.Sprintf(`sum(kube_pod_container_resource_requests{__CLUSTER__ %sresource="cpu"})`, containerFilter)
		case "cpu_limit":
			queries[metricKey] = fmt.Sprintf(`sum(kube_pod_container_resource_limits{__CLUSTER__ %sresource="cpu"})`, containerFilter)
		case "memory_usage":
			queries[metricKey] = fmt.Sprintf(`sum(container_memory_working_set_bytes{__CLUSTER__ %s})`, containerFilter)
		case "memory_request":
			queries[metricKey] = fmt.Sprintf(`sum(kube_pod_container_resource_requests{__CLUSTER__ %sresource="memory"})`, containerFilter)
		case "memory_limit":
			queries[metricKey] = fmt.Sprintf(`sum(kube_pod_container_resource_limits{__CLUSTER__ %sresource="memory"})`, containerFilter)
		case "disk_total":
			queries[metricKey] = fmt.Sprintf(`sum(node_filesystem_size_bytes{ __CLUSTER__ mountpoint="/", instance=~"%s.*"}) or sum(kubelet_volume_stats_capacity_bytes{ __CLUSTER__ instance=~"%s.*"}) or sum(kubelet_volume_stats_capacity_bytes{ __CLUSTER__ instance=~"%s.*"})`, safeMeta.InternalIP, safeMeta.NodeName, safeMeta.NodeIP)
		case "disk_used":
			queries[metricKey] = fmt.Sprintf(`(sum(node_filesystem_size_bytes{ __CLUSTER__ mountpoint="/", instance=~"%s.*"}) - sum(node_filesystem_free_bytes{ __CLUSTER__ mountpoint="/", instance=~"%s.*"})) or (sum(kubelet_volume_stats_capacity_bytes{ __CLUSTER__ instance=~"%s.*"}) - sum(kubelet_volume_stats_available_bytes{ __CLUSTER__ instance=~"%s.*"})) or (sum(kubelet_volume_stats_capacity_bytes{ __CLUSTER__ instance=~"%s.*"}) - sum(kubelet_volume_stats_available_bytes{ __CLUSTER__ instance=~"%s.*"}))`, safeMeta.InternalIP, safeMeta.InternalIP, safeMeta.NodeName, safeMeta.NodeName, safeMeta.NodeIP, safeMeta.NodeIP)

		// --- Node/Cluster Aggregations ---
		// Usage / percentiles / peak are windowed by the picker range (rangeW) instead of a
		// hardcoded 24h, so the time filter actually adjusts the numbers. The percentile/peak
		// queries sum across the per-(node,core,mode) series FIRST, then aggregate over time.
		// Summing each series' own time-percentile instead (the old form) added peaks that occur
		// at different instants and produced values above physical capacity (P50/P90/Max >100%).
		// Valid in both PromQL (prod) and VictoriaMetrics MetricsQL (dev).
		case "cpu_real":
			queries[metricKey] = fmt.Sprintf(`sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[%s])) or sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[%s]))`, rangeW, rangeW)
		case "cpu_total":
			queries[metricKey] = `sum(machine_cpu_cores{__CLUSTER__}) or sum(node_resources_cpu_logical_cores{__CLUSTER__})`
		case "mem_real":
			queries[metricKey] = `sum(node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemAvailable_bytes{__CLUSTER__}) or sum(node_resources_memory_total_bytes{__CLUSTER__} - node_resources_memory_available_bytes{__CLUSTER__})`
		case "mem_total":
			queries[metricKey] = `sum(node_memory_MemTotal_bytes{__CLUSTER__}) or sum(node_resources_memory_total_bytes{__CLUSTER__})`
		// cpu_usage_trend / mem_usage_trend feed the utilisation sparkline: fetched as a RANGE
		// query so the relay evaluates them at each step across the picker window. CPU uses a
		// short rate window (rateW) so spikes register instead of being averaged away.
		case "cpu_usage_trend":
			queries[metricKey] = fmt.Sprintf(`sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[%s])) or sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[%s]))`, rateW, rateW)
		case "mem_usage_trend":
			queries[metricKey] = `sum(node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemAvailable_bytes{__CLUSTER__}) or sum(node_resources_memory_total_bytes{__CLUSTER__} - node_resources_memory_available_bytes{__CLUSTER__})`
		case "p90_mem":
			queries[metricKey] = `quantile(0.9, node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemAvailable_bytes{__CLUSTER__}) or quantile(0.9, node_resources_memory_total_bytes{__CLUSTER__} - node_resources_memory_available_bytes{__CLUSTER__})`
		case "p90_cpu":
			queries[metricKey] = fmt.Sprintf(`quantile_over_time(0.90, sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[%s]))[%s:%s]) or quantile_over_time(0.90, sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[%s]))[%s:%s])`, rateW, rangeW, stepW, rateW, rangeW, stepW)
		case "p50_mem":
			queries[metricKey] = `quantile(0.5, node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemAvailable_bytes{__CLUSTER__}) or quantile(0.5, node_resources_memory_total_bytes{__CLUSTER__} - node_resources_memory_available_bytes{__CLUSTER__})`
		case "p50_cpu":
			queries[metricKey] = fmt.Sprintf(`quantile_over_time(0.50, sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[%s]))[%s:%s]) or quantile_over_time(0.50, sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[%s]))[%s:%s])`, rateW, rangeW, stepW, rateW, rangeW, stepW)
		case "max_usage_mem":
			queries[metricKey] = fmt.Sprintf(`max_over_time(sum(node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemFree_bytes{__CLUSTER__} - node_memory_Buffers_bytes{__CLUSTER__} - node_memory_Cached_bytes{__CLUSTER__})[%s:%s])`, rangeW, stepW)
		case "max_usage_cpu":
			queries[metricKey] = fmt.Sprintf(`max_over_time(sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[%s]))[%s:%s]) or max_over_time(sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[%s]))[%s:%s])`, rateW, rangeW, stepW, rateW, rangeW, stepW)
		case "replica_defined":
			queries[metricKey] = fmt.Sprintf(`sum(kube_replicaset_spec_replicas{ __CLUSTER__ namespace="%s", replicaset=~"%s.*"})`, safeMeta.Namespace, safeMeta.Name)
		case "replica_ready":
			queries[metricKey] = fmt.Sprintf(`sum(kube_replicaset_status_ready_replicas{ __CLUSTER__ namespace="%s", replicaset=~"%s.*"})`, safeMeta.Namespace, safeMeta.Name)

		// others
		case "container_application_type_with_pod":
			queries[metricKey] = fmt.Sprintf(`container_application_type{ __CLUSTER__ container_id=~"/k8s/%s/%s.*"}`, safeMeta.Namespace, safeMeta.Name)
		case "container_application_type_with_workload":
			queries[metricKey] = fmt.Sprintf(`container_application_type{ __CLUSTER__ container_id=~"/k8s/%s/%s-.*"}`, safeMeta.Namespace, safeMeta.Name)
		case "jvm_memory_metric_count":
			queries[metricKey] = fmt.Sprintf(`count by (namespace, pod) ({ __CLUSTER__ __name__=~"process.runtime.jvm.memory.usage|process_runtime_jvm_memory_usage_bytes", namespace=~"%s"})`, safeMeta.Namespace)
		case "cpython_memory_metric_count":
			queries[metricKey] = fmt.Sprintf(`count by (pod, namespace) ({ __CLUSTER__ __name__=~"process.runtime.cpython.memory|process_runtime_cpython_memory_bytes", namespace=~"%s"})`, safeMeta.Namespace)
		case "go_heap_memory_metric_count":
			queries[metricKey] = fmt.Sprintf(`count by (pod, namespace) ({ __CLUSTER__ __name__=~"process.runtime.go.mem.heap_sys|process_runtime_go_mem_heap_sys_bytes|go.memory.used|go_memory_used_bytes", namespace=~"%s"})`, safeMeta.Namespace)
		case "service_info_by_cluster_ip":
			queries[metricKey] = fmt.Sprintf(`kube_service_info{ __CLUSTER__ cluster_ip="%s"}`, safeMeta.InternalIP)
		case "sensitive_log_messages":
			queries[metricKey] = "sum(increase(container_sensitive_log_messages_total{__CLUSTER__}[5m])) by (pattern, container_id, regex, name, pattern_hash)"
		case "container_error_log_count_with_pod":
			queries[metricKey] = fmt.Sprintf(`sum(increase(container_log_messages_total{ __CLUSTER__ container_id=~"%s", level=~"critical|error|exception"}[5m])) by (container_id)`, safeMeta.Name)
		case "container_error_log_count_with_workload":
			queries[metricKey] = fmt.Sprintf(`sum(increase(container_log_messages_total{ __CLUSTER__ container_id=~"%s", level=~"critical|error"}[5m])) by (container_id)`, safeMeta.Name)
		case "workload_http_error_rate":
			queries[metricKey] = fmt.Sprintf(`sum by(destination_workload_name, destination_workload_namespace)(rate(container_http_requests_total{ __CLUSTER__ status=~"5..|4..", destination_workload_name=~"%s", destination_workload_namespace=~"%s"}[1h])) / sum by(destination_workload_name, destination_workload_namespace)(rate(container_http_requests_total{ __CLUSTER__ destination_workload_name=~"%s", destination_workload_namespace=~"%s"}[1h]))`, safeMeta.Name, safeMeta.Namespace, safeMeta.Name, safeMeta.Namespace)
		case "container_http_latency_p90":
			queries[metricKey] = fmt.Sprintf(`histogram_quantile(0.90, sum(rate(container_http_requests_duration_seconds_total_bucket{ __CLUSTER__ container_id=~"%s", destination_workload_namespace!="external", destination_workload_namespace!=""}[1h])) by (le))`, safeMeta.ContainerName)
		case "container_http_latency_p99":
			queries[metricKey] = fmt.Sprintf(`histogram_quantile(0.99, sum(rate(container_http_requests_duration_seconds_total_bucket{ __CLUSTER__ container_id=~"%s", destination_workload_namespace!="external", destination_workload_namespace!=""}[1h])) by (le))`, safeMeta.ContainerName)
		case "container_http_latency_p95":
			queries[metricKey] = fmt.Sprintf(`histogram_quantile(0.95, sum(rate(container_http_requests_duration_seconds_total_bucket{ __CLUSTER__ container_id=~"%s", destination_workload_namespace!="external", destination_workload_namespace!=""}[1h])) by (le))`, safeMeta.ContainerName)
		case "container_http_latency_p50":
			queries[metricKey] = fmt.Sprintf(`histogram_quantile(0.50, sum(rate(container_http_requests_duration_seconds_total_bucket{ __CLUSTER__ container_id=~"%s", destination_workload_namespace!="external", destination_workload_namespace!=""}[1h])) by (le))`, safeMeta.ContainerName)
		case "container_http_latency_mean":
			queries[metricKey] = fmt.Sprintf(`sum(rate(container_http_requests_duration_seconds_total_sum{ __CLUSTER__ container_id=~"%s", destination_workload_namespace!="external", destination_workload_namespace!=""}[1h])) / sum(rate(container_http_requests_duration_seconds_total_count{ __CLUSTER__ container_id=~"%s", destination_workload_namespace!="external", destination_workload_namespace!=""}[1h]))`, safeMeta.ContainerName, safeMeta.ContainerName)
		case "container_http_request_count":
			queries[metricKey] = fmt.Sprintf(`sum(increase(container_http_requests_total{ __CLUSTER__ container_id=~"%s"}[1h]))`, safeMeta.ContainerName)
		case "container_http_error_status_count":
			queries[metricKey] = fmt.Sprintf(`sum by(status) (increase(container_http_requests_total{ __CLUSTER__ status=~"4..|5..",container_id=~"%s"}[1h]))`, safeMeta.ContainerName)
		case "container_top_destination_services":
			queries[metricKey] = fmt.Sprintf(`topk(5, sum by (destination_workload_name, destination_workload_namespace) (rate(container_http_requests_total{ __CLUSTER__ container_id=~"%s"}[1h])))`, safeMeta.ContainerName)
		case "cpu_usage_pod":
			queries[metricKey] = fmt.Sprintf(`sum(irate(container_cpu_usage_seconds_total{namespace="%s", pod=~"%s"}[1m]))`, safeMeta.Namespace, safeMeta.Name)
		case "cpu_request_pod":
			queries[metricKey] = fmt.Sprintf(`kube_pod_container_resource_requests{resource = "cpu", namespace="%s", pod=~"%s"}`, safeMeta.Namespace, safeMeta.Name)
		case "cpu_limit_pod":
			queries[metricKey] = fmt.Sprintf(`kube_pod_container_resource_limits{resource = "cpu", namespace="%s", pod=~"%s"}`, safeMeta.Namespace, safeMeta.Name)
		case "container_top_http_requests":
			queries[metricKey] = fmt.Sprintf(`topk(5, sum by (destination_workload_name, destination_workload_namespace) (rate(container_http_requests_total{ __CLUSTER__ container_id=~"%s"}[1h])))`, safeMeta.ContainerName)
		case "container_top_cpu_usage":
			queries[metricKey] = fmt.Sprintf(`topk(5, sum by (pod, namespace) (rate(container_cpu_usage_seconds_total{ __CLUSTER__ pod=~"%s", namespace=~"%s"}[1h])))`, safeMeta.Name, safeMeta.Namespace)
		case "container_top_memory_usage":
			queries[metricKey] = fmt.Sprintf(`topk(5, sum by (pod, namespace) (rate(container_memory_working_set_bytes{ __CLUSTER__ pod=~"%s", namespace=~"%s"}[1h])))`, safeMeta.Name, safeMeta.Namespace)
		case "container_top_http_error_calls":
			queries[metricKey] = fmt.Sprintf(`topk(5, sum by (destination_workload_name, destination_workload_namespace) (increase(container_http_requests_total{ __CLUSTER__ status=~"4..|5..",container_id=~"%s"}[1h])))`, safeMeta.ContainerName)
		}
	}
	return queries
}

// --- NEW RELIC HELPERS ---

// buildNRQLNodeNameFilter builds the appropriate NRQL WHERE condition for node name filtering.
// When nodeName contains pipe-separated values (|) or regex wildcards (.*), uses RLIKE.
// Otherwise uses exact equality (=).
// NewRelic RLIKE has a 256-character limit; long patterns are split into multiple RLIKE OR conditions.
func buildNRQLNodeNameFilter(nodeName string) string {
	if nodeName == "" {
		return ""
	}
	// Use exact equality for simple node names without regex patterns
	if !strings.Contains(nodeName, "|") && !strings.Contains(nodeName, ".*") {
		return fmt.Sprintf("nodeName = '%s'", escapeNRQLValue(nodeName))
	}

	const (
		maxRLIKELen    = 256
		nrlikeTemplate = "nodeName RLIKE '%s'"
	)
	escaped := escapeNRQLValue(nodeName)
	if len(escaped) <= maxRLIKELen {
		return fmt.Sprintf(nrlikeTemplate, escaped)
	}

	// Pattern exceeds RLIKE 256-char limit: chunk pipe-separated parts into groups that fit,
	// then join multiple RLIKE expressions with OR.
	parts := strings.Split(nodeName, "|")
	var rlikeExprs []string
	current := ""
	for _, part := range parts {
		escapedPart := escapeNRQLValue(part)
		if current == "" {
			current = escapedPart
		} else if len(current)+1+len(escapedPart) <= maxRLIKELen {
			current += "|" + escapedPart
		} else {
			rlikeExprs = append(rlikeExprs, fmt.Sprintf(nrlikeTemplate, current))
			current = escapedPart
		}
	}
	if current != "" {
		rlikeExprs = append(rlikeExprs, fmt.Sprintf(nrlikeTemplate, current))
	}
	if len(rlikeExprs) == 1 {
		return rlikeExprs[0]
	}
	return "(" + strings.Join(rlikeExprs, " OR ") + ")"
}

func buildNewRelicNodeQueries(meta RequestMetadata, metrics []string) map[string]string {
	queries := make(map[string]string)
	if meta.NodeName == "" {
		return queries
	}

	nodeFilter := buildNRQLNodeNameFilter(meta.NodeName)

	for _, metricKey := range metrics {
		switch metricKey {
		case "cpu_usage":
			queries[metricKey] = fmt.Sprintf(
				"SELECT average(cpuUsedCores) FROM K8sNodeSample WHERE %s FACET nodeName",
				nodeFilter)
		case "memory_usage":
			queries[metricKey] = fmt.Sprintf(
				"SELECT average(memoryUsedBytes) FROM K8sNodeSample WHERE %s FACET nodeName",
				nodeFilter)
		case "cpu_request":
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(cpuRequestedCores) FROM K8sContainerSample WHERE %s FACET nodeName",
				nodeFilter)
		case "cpu_limit":
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(cpuLimitCores) FROM K8sContainerSample WHERE %s FACET nodeName",
				nodeFilter)
		case "memory_request":
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(memoryRequestedBytes) FROM K8sContainerSample WHERE %s FACET nodeName",
				nodeFilter)
		case "memory_limit":
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(memoryLimitBytes) FROM K8sContainerSample WHERE %s FACET nodeName",
				nodeFilter)
		case "disk_total":
			queries[metricKey] = fmt.Sprintf(
				"SELECT latest(fsCapacityBytes) FROM K8sNodeSample WHERE %s FACET nodeName",
				nodeFilter)
		case "disk_used":
			queries[metricKey] = fmt.Sprintf(
				"SELECT latest(fsUsedBytes) FROM K8sNodeSample WHERE %s FACET nodeName",
				nodeFilter)
		case "cpu_allocatable":
			queries[metricKey] = fmt.Sprintf(
				"SELECT latest(allocatableCpuCores) FROM K8sNodeSample WHERE %s FACET nodeName",
				nodeFilter)
		case "memory_allocatable":
			queries[metricKey] = fmt.Sprintf(
				"SELECT latest(allocatableMemoryBytes) FROM K8sNodeSample WHERE %s FACET nodeName",
				nodeFilter)
		case "cpu_usage_line":
			queries[metricKey] = fmt.Sprintf(
				"SELECT average(cpuUsedCores) FROM K8sNodeSample WHERE %s FACET nodeName",
				nodeFilter)
		case "memory_usage_line":
			queries[metricKey] = fmt.Sprintf(
				"SELECT average(memoryUsedBytes) FROM K8sNodeSample WHERE %s FACET nodeName",
				nodeFilter)
		}
	}
	return queries
}

func buildNewRelicWorkloadQueries(meta RequestMetadata, metrics []string) map[string]string {
	queries := make(map[string]string)

	// Build WHERE clause based on kind and metadata
	var whereClause string
	namespace := escapeNRQLValue(meta.Namespace)
	name := escapeNRQLValue(meta.Name)

	switch meta.Kind {
	case "pod":
		if meta.Namespace != "" && meta.Name != "" {
			whereClause = fmt.Sprintf("namespaceName = '%s' AND podName = '%s'", namespace, name)
		} else if meta.Namespace != "" {
			whereClause = fmt.Sprintf("namespaceName = '%s'", namespace)
		}
	case "deployment":
		if meta.Namespace != "" && meta.Name != "" {
			whereClause = fmt.Sprintf("namespaceName = '%s' AND deploymentName = '%s'", namespace, name)
		} else if meta.Namespace != "" {
			whereClause = fmt.Sprintf("namespaceName = '%s'", namespace)
		}
	case "statefulset":
		if meta.Namespace != "" && meta.Name != "" {
			// StatefulSet pods match pattern: statefulsetname-0, statefulsetname-1, etc.
			whereClause = fmt.Sprintf("namespaceName = '%s' AND podName LIKE '%s-%%'", namespace, name)
		} else if meta.Namespace != "" {
			whereClause = fmt.Sprintf("namespaceName = '%s'", namespace)
		}
	case "daemonset":
		if meta.Namespace != "" && meta.Name != "" {
			whereClause = fmt.Sprintf("namespaceName = '%s' AND daemonsetName = '%s'", namespace, name)
		} else if meta.Namespace != "" {
			whereClause = fmt.Sprintf("namespaceName = '%s'", namespace)
		}
	default:
		// Default to deployment pattern
		if meta.Namespace != "" && meta.Name != "" {
			whereClause = fmt.Sprintf("namespaceName = '%s' AND deploymentName = '%s'", namespace, name)
		} else if meta.Namespace != "" {
			whereClause = fmt.Sprintf("namespaceName = '%s'", namespace)
		}
	}

	// Add container filter if specified
	if meta.ContainerName != "" && whereClause != "" {
		whereClause = fmt.Sprintf("%s AND containerName = '%s'", whereClause, escapeNRQLValue(meta.ContainerName))
	}

	// --- Pass 1: Cluster/Node Aggregation Metrics (no workload filter needed) ---
	for _, metricKey := range metrics {
		switch metricKey {
		case "cpu_real":
			queries[metricKey] = "SELECT sum(cpuUsedCores) FROM K8sNodeSample"
		case "cpu_total":
			queries[metricKey] = "SELECT sum(capacityCpuCores) FROM K8sNodeSample"
		case "mem_real":
			queries[metricKey] = "SELECT sum(memoryUsedBytes) FROM K8sNodeSample"
		case "mem_total":
			queries[metricKey] = "SELECT sum(capacityMemoryBytes) FROM K8sNodeSample"
		case "p90_cpu":
			queries[metricKey] = "SELECT percentile(cpuUsedCores, 90) FROM K8sNodeSample"
		case "p50_cpu":
			queries[metricKey] = "SELECT percentile(cpuUsedCores, 50) FROM K8sNodeSample"
		case "p90_mem":
			queries[metricKey] = "SELECT percentile(memoryUsedBytes, 90) FROM K8sNodeSample"
		case "p50_mem":
			queries[metricKey] = "SELECT percentile(memoryUsedBytes, 50) FROM K8sNodeSample"
		case "max_usage_cpu":
			queries[metricKey] = "SELECT max(cpuUsedCores) FROM K8sNodeSample"
		case "max_usage_mem":
			queries[metricKey] = "SELECT max(memoryUsedBytes) FROM K8sNodeSample"
		// --- Cluster-wide Container Resource Aggregations (no workload filter) ---
		case "cpu_request":
			queries[metricKey] = "SELECT sum(cpuRequestedCores) FROM K8sContainerSample"
		case "cpu_limit":
			queries[metricKey] = "SELECT sum(cpuLimitCores) FROM K8sContainerSample"
		case "memory_request":
			queries[metricKey] = "SELECT sum(memoryRequestedBytes) FROM K8sContainerSample"
		case "memory_limit":
			queries[metricKey] = "SELECT sum(memoryLimitBytes) FROM K8sContainerSample"
		}
	}

	// If no workload context, return cluster-level metrics only
	if whereClause == "" {
		return queries
	}

	// Determine FACET clause based on kind
	var facetClause string
	switch meta.Kind {
	case "pod":
		facetClause = "FACET podName"
	case "deployment":
		facetClause = "FACET deploymentName"
	case "statefulset":
		facetClause = "FACET podName"
	case "daemonset":
		facetClause = "FACET daemonsetName"
	default:
		facetClause = "FACET deploymentName"
	}

	// Build PVC WHERE clause for volume metrics
	var pvcWhereClause string
	if meta.Namespace != "" {
		if meta.PVCName != "" {
			pvcWhereClause = fmt.Sprintf("namespaceName = '%s' AND pvcName = '%s'", namespace, escapeNRQLValue(meta.PVCName))
		} else if meta.Name != "" {
			pvcWhereClause = fmt.Sprintf("namespaceName = '%s' AND pvcName LIKE '%s%%'", namespace, name)
		} else {
			pvcWhereClause = fmt.Sprintf("namespaceName = '%s'", namespace)
		}
	}

	// --- Pass 2: Workload-specific Metrics (require whereClause) ---
	for _, metricKey := range metrics {
		switch metricKey {
		// --- Resource Metrics from K8sContainerSample ---
		case "cpu_usage":
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(cpuUsedCores) FROM K8sContainerSample WHERE %s %s",
				whereClause, facetClause)
		case "cpu_request":
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(cpuRequestedCores) FROM K8sContainerSample WHERE %s %s",
				whereClause, facetClause)
		case "cpu_limit":
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(cpuLimitCores) FROM K8sContainerSample WHERE %s %s",
				whereClause, facetClause)
		case "memory_usage":
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(memoryWorkingSetBytes) FROM K8sContainerSample WHERE %s %s",
				whereClause, facetClause)
		case "memory_request":
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(memoryRequestedBytes) FROM K8sContainerSample WHERE %s %s",
				whereClause, facetClause)
		case "memory_limit":
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(memoryLimitBytes) FROM K8sContainerSample WHERE %s %s",
				whereClause, facetClause)

		// --- HTTP/APM Metrics from Transaction events ---
		case "http_status":
			queries[metricKey] = fmt.Sprintf(
				"SELECT count(*) FROM Transaction WHERE %s FACET httpResponseCode",
				whereClause)
		case "http_throughput":
			queries[metricKey] = fmt.Sprintf(
				"SELECT rate(count(*), 1 minute) FROM Transaction WHERE %s %s",
				whereClause, facetClause)
		case "http_latency_p95":
			queries[metricKey] = fmt.Sprintf(
				"SELECT percentile(duration, 95) FROM Transaction WHERE %s %s",
				whereClause, facetClause)
		case "http_latency_p99":
			queries[metricKey] = fmt.Sprintf(
				"SELECT percentile(duration, 99) FROM Transaction WHERE %s %s",
				whereClause, facetClause)
		case "http_latency_sum":
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(duration) FROM Transaction WHERE %s %s",
				whereClause, facetClause)
		case "http_max_response_time":
			queries[metricKey] = fmt.Sprintf(
				"SELECT max(duration) FROM Transaction WHERE %s %s",
				whereClause, facetClause)
		case "http_error_rate":
			queries[metricKey] = fmt.Sprintf(
				"SELECT percentage(count(*), WHERE error IS true) FROM Transaction WHERE %s %s",
				whereClause, facetClause)

		// --- PVC/Volume Metrics from K8sVolumeSample ---
		case "pvc_usage":
			if pvcWhereClause != "" {
				queries[metricKey] = fmt.Sprintf(
					"SELECT sum(fsUsedBytes) FROM K8sVolumeSample WHERE %s FACET pvcName",
					pvcWhereClause)
			}
		case "pvc_requests":
			if pvcWhereClause != "" {
				queries[metricKey] = fmt.Sprintf(
					"SELECT sum(fsCapacityBytes) FROM K8sVolumeSample WHERE %s FACET pvcName",
					pvcWhereClause)
			}
		}
	}
	return queries
}

func SaveUserHistory(ctx *security.RequestContext, userHistoryRequest UserHistoryRequest) (map[string]string, error) {
	if userHistoryRequest.AccountId == "" {
		return nil, fmt.Errorf("account id is required")
	}
	if userHistoryRequest.Data == "" {
		return nil, fmt.Errorf("data is required")
	}
	if userHistoryRequest.Module == "" {
		return nil, fmt.Errorf("module is required")
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("observability.SaveUserHistory: failed to get database manager", "error", err)
		return nil, err
	}
	query := `INSERT INTO user_history (user_id, tenant_id, account_id, module, data, created_at, updated_at, meta, duration, status) VALUES ($1, $2, $3, $4, $5, $6, $7, '{}'::jsonb, $8, $9);`
	// user_history.created_at/updated_at are typed `timestamp` (no tz), so persist UTC
	// explicitly — time.Now() would store the process-local wall clock and read back
	// labeled as UTC (see issue #31312).
	now := time.Now().UTC()
	_, err = dbms.Exec(query, ctx.GetSecurityContext().GetUserId(), ctx.GetSecurityContext().GetTenantId(), userHistoryRequest.AccountId, userHistoryRequest.Module, userHistoryRequest.Data, now, now, userHistoryRequest.Duration, userHistoryRequest.Status)

	if err != nil {
		return nil, fmt.Errorf("failed to insert record in user_history: %w", err)
	}

	return map[string]string{
		"status": "done",
	}, nil
}
