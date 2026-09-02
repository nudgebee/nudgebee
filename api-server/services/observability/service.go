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
	case provider == "splunk_enterprise" && integrationSource == "user":
		return &SplunkEnterpriseLogSource{}, nil
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
	case provider == "splunk_enterprise" && integrationSource == "user":
		return &SplunkEnterpriseTraceSource{}, nil
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
	default:
		return nil, fmt.Errorf(
			"unsupported traces provider/source combination: provider=%s, integrationSource=%s",
			provider, integrationSource,
		)
	}
}

// isOtelNativeTraceIndex reports whether a resolved ES trace index targets the
// OTel-native traces-*.otel data streams (mapping.mode: otel) rather than the
// Data Prepper otel-v1-apm-span-* schema.
func isOtelNativeTraceIndex(index string) bool {
	index = strings.TrimSpace(index)
	if index == "" || strings.Contains(index, "otel-v1-apm-span") {
		return false
	}
	return strings.Contains(index, ".otel") || strings.HasPrefix(index, "traces-")
}

// resolveTraceSource resolves the trace source for an account and, for ES, upgrades
// to the OTel-native reader when the effective trace index targets the traces-*.otel
// data streams. The effective index mirrors esTraceIndexFor's precedence: the
// per-request index override (Traces-tab picker) wins over the account's stored
// config — so pointing the picker at an OTel-native data stream selects the OTel
// reader even when the saved trace index is still Data Prepper's. Non-ES providers
// and non-OTel ES indices keep the source returned by getTraceSource. A config lookup
// failure is non-fatal — it just leaves the base (Data Prepper) ES source in place.
func resolveTraceSource(ctx *security.RequestContext, accountId, provider, integrationSource, indexOverride string) (TraceSource, error) {
	src, err := getTraceSource(provider, integrationSource)
	if err != nil {
		return nil, err
	}
	if provider == "ES" {
		effectiveIndex := indexOverride
		if effectiveIndex == "" {
			if cfg, cerr := GetElasticsearchConfig(ctx, accountId); cerr == nil {
				effectiveIndex = cfg.TraceIndex
			}
		}
		if isOtelNativeTraceIndex(effectiveIndex) {
			return &ElasticOtelTraceSource{}, nil
		}
	}
	return src, nil
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
	case provider == "splunk_enterprise" && integrationSource == "user":
		return &SplunkEnterpriseMetricSource{}, nil
	case provider == "ES" && integrationSource == "user":
		return &ElasticSaasMetricSource{}, nil
	case provider == "dynatrace" && integrationSource == "user":
		return &DynatraceMetricSource{}, nil
	case provider == "solarwinds" && integrationSource == "user":
		return &SolarWindsMetricSource{}, nil
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

	// Reconcile the clause with the labels' data types: reject an operator the type
	// cannot support (e.g. a regex on an INT column, which Pinot answers with a raw
	// backend error), and render each value as the column's native type — the UI sends
	// chip values as strings, so a numeric column would otherwise be compared against a
	// quoted string. Runs here because field
	// names are now in provider space — the space QueryLabels reports types in — and
	// because keys the integration strips above should not be validated. Placed before
	// GetQuery so the unsupported query is never built. The builder already hides these
	// operators per label; this guards the callers that bypass it (code mode, saved
	// URLs, the LLM agent, direct API). Fails open when the type cannot be established.
	if err := applyLabelDataTypes(ctx, source, &fetchLogRequest); err != nil {
		return FetchLogsResult{}, err
	}

	// Always-apply per-account default filters (e.g. a central Pinot scoped to
	// cluster_id) configured on the log integration. AND them into the where clause
	// here — after label-mapping and key-strip — so the operator-entered
	// provider-native columns are used verbatim (not renamed, not stripped).
	ApplyDefaultLogFilters(ctx, &fetchLogRequest)

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

	// Snapshot the label names AND equality values the caller referenced BEFORE the query
	// runs, so a time range or other filter the backend injects during execution is not
	// mistaken for a user-supplied label/value during validation below.
	var referencedLabels map[string]struct{}
	var referencedValues map[string][]string
	if fetchLogRequest.ValidateRequest {
		referencedLabels = map[string]struct{}{}
		collectWhereFieldNames(fetchLogRequest.QueryRequest.Where, referencedLabels)
		referencedValues = map[string][]string{}
		collectWhereFieldValues(fetchLogRequest.QueryRequest.Where, referencedValues)
	}

	logs, err := source.QueryLogs(ctx, fetchLogRequest)

	// Resolve the query that was actually used so callers (UI, LLM, runbooks) can show
	// it — including on the empty-result diagnosis early-returns below, not just the
	// success path. fetchLogRequest.Query holds the raw query or the GetQuery result set
	// above. Providers that consume the where-clause natively emit no query string, so
	// fall back to the canonical where JSON.
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

	// A query that matched nothing — or that failed with a backend error — is frequently
	// caused by a mistyped label NAME (e.g. "service_nam") or a mistyped label VALUE (e.g.
	// namespace="prodd"). Some backends silently return zero rows for an unknown label/value
	// (Loki); others reject the query (ClickHouse). Either way the caller — notably an LLM
	// agent — can't tell what to fix. When the caller opts in via ValidateRequest, run an
	// ordered diagnosis and, on a hit, return a 200 with empty Logs and the actionable message
	// in Suggestion — (1) unknown label names, then (2) unknown label values with closest-match
	// suggestions — rather than an error: the diagnosis successfully determined what to fix, it
	// didn't fail. Best-effort: each check fails open (returns nil) when it can't establish the
	// provider's label/value set, so a legitimately-empty query falls through unchanged.
	if fetchLogRequest.ValidateRequest && (err != nil || len(logs) == 0) {
		// One structured line per diagnosis run, so the trigger rate and hit rate are
		// queryable in Loki (filter on msg="log_query_empty_result_diagnosis"). "outcome"
		// distinguishes which check produced the hint the agent then rewrites against
		// (unknown_label_name / unknown_label_value) from a genuinely-empty result (none).
		logger := ctx.GetLogger()
		outcome := "none"
		defer func() {
			logger.Info("log_query_empty_result_diagnosis",
				"account_id", fetchLogRequest.AccountId,
				"provider", fetchLogRequest.LogProvider,
				"had_backend_error", err != nil,
				"referenced_labels", len(referencedLabels),
				"referenced_value_fields", len(referencedValues),
				"outcome", outcome)
		}()

		if verr := validateReferencedLabels(ctx, source, fetchLogRequest, referencedLabels, filteringMap); verr != nil {
			outcome = "unknown_label_name"
			return FetchLogsResult{Logs: []OutputLog{}, Query: usedQuery, Provider: provider, Suggestion: verr.Error()}, nil
		}
		// Value suggestions only apply to a query that ran cleanly but matched nothing.
		// On a backend error the empty result isn't a "wrong value" signal — the value-set
		// fetch would be unreliable and the real error is the more useful thing to surface —
		// so gate this specifically on an empty (non-errored) result.
		if err == nil && len(logs) == 0 {
			if verr := validateReferencedLabelValues(ctx, source, fetchLogRequest, referencedValues); verr != nil {
				outcome = "unknown_label_value"
				return FetchLogsResult{Logs: []OutputLog{}, Query: usedQuery, Provider: provider, Suggestion: verr.Error()}, nil
			}
		}
	}
	if err != nil {
		return FetchLogsResult{}, err
	}
	normalizeOutputLogLabels(logs, filteringMap)

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

// collectWhereFieldValues walks a canonical where-clause and records, per field, the
// discrete equality values the caller filtered on. Only the equality operators _eq and
// _in carry a concrete value worth cross-checking against a label's real value set;
// pattern operators (_regex / _contains / _like / …) match substrings or expressions, so
// a "value not in the label's value list" check would false-positive on them and they are
// skipped. Values are coerced to strings, mirroring how the backends render them.
func collectWhereFieldValues(where query.QueryWhereClause, out map[string][]string) {
	for field, ops := range where.Binary {
		for op, val := range ops {
			switch op {
			case query.Eq:
				out[field] = append(out[field], fmt.Sprintf("%v", val))
			case query.In:
				if arr, err := toStringArray(val); err == nil {
					out[field] = append(out[field], arr...)
				}
			}
		}
	}
	for _, c := range where.And {
		collectWhereFieldValues(c, out)
	}
	for _, c := range where.Or {
		collectWhereFieldValues(c, out)
	}
	if where.Not != nil {
		collectWhereFieldValues(*where.Not, out)
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
		// Same matcher the value suggestions use (shared tokens OR typo distance OR
		// substring). Plain substring containment only caught truncations — a mid-word
		// deletion or transposition ("k8s_deploymnt_name") scored nothing and the caller
		// got an unknown-label error with no correction to act on.
		for _, a := range closestValues(u, available) {
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

// levenshtein returns the edit distance (insertions, deletions, substitutions) between a
// and b. Standard two-row dynamic-programming implementation; O(len(a)*len(b)) time,
// O(len(b)) space. Used to rank "did-you-mean" value suggestions for typos.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, min(curr[j-1]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}

// tokenize splits a value on the delimiters common to observability identifiers
// (ml-k8s-server, k8s_namespace, foo.bar) into its lowercase parts, as a set. Used by
// closestValues so hyphenated names match on shared segments, not just whole-string distance.
func tokenize(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, tok := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return strings.ContainsRune("-_./: ", r)
	}) {
		out[tok] = struct{}{}
	}
	return out
}

// sharedTokenCount returns how many tokens a and b have in common.
func sharedTokenCount(a, b map[string]struct{}) int {
	n := 0
	for t := range a {
		if _, ok := b[t]; ok {
			n++
		}
	}
	return n
}

// closestValues ranks candidates by closeness to target and returns up to maxValueSuggestions
// of them. A candidate is a plausible suggestion if it shares a delimiter-separated token with
// the target (e.g. "ml-server" ~ "ml-k8s-server" share "ml"+"server"), OR is within a typo
// edit-distance threshold (scaled to length, so "prodd"→"prod" matches but unrelated strings
// don't), OR is a bidirectional substring ("prod" ~ "production"). Ranking is shared-token
// count DESC, then edit distance ASC — so for a hyphenated-name typo the semantically closest
// value (most shared segments) wins over strings that merely share the common "-server" suffix.
// Comparison is case-insensitive. Empty slice when nothing is plausibly close — callers then
// point at the label-values listing action instead of dumping the full set.
func closestValues(target string, candidates []string) []string {
	const maxValueSuggestions = 5
	lt := strings.ToLower(target)
	threshold := len(lt) / 3
	if threshold < 2 {
		threshold = 2
	}
	targetTokens := tokenize(target)

	type scored struct {
		value  string
		shared int
		dist   int
	}
	var ranked []scored
	seen := map[string]struct{}{}
	for _, c := range candidates {
		lc := strings.ToLower(c)
		if _, ok := seen[lc]; ok {
			continue
		}
		seen[lc] = struct{}{}
		shared := sharedTokenCount(targetTokens, tokenize(lc))
		dist := levenshtein(lt, lc)
		substr := strings.Contains(lc, lt) || strings.Contains(lt, lc)
		if shared == 0 && dist > threshold && !substr {
			continue // not plausibly close by any signal
		}
		ranked = append(ranked, scored{value: c, shared: shared, dist: dist})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].shared != ranked[j].shared {
			return ranked[i].shared > ranked[j].shared
		}
		return ranked[i].dist < ranked[j].dist
	})

	out := make([]string, 0, maxValueSuggestions)
	for _, s := range ranked {
		out = append(out, s.value)
		if len(out) >= maxValueSuggestions {
			break
		}
	}
	return out
}

// unknownLabelError builds the actionable, token-conscious error returned when a query
// references label names the provider doesn't expose. It names the unknown label(s) and
// either the closest valid matches or action-agnostic guidance (verify or drop the
// filter) — never the full label list, and never a listing tool the caller may not
// have (mirrors unknownValueError's fallback). noun is "logs"/"traces", providerNoun is
// "log"/"trace".
func unknownLabelError(noun, providerNoun string, unknown, available []string) error {
	if suggestions := suggestLabels(unknown, available); len(suggestions) > 0 {
		return fmt.Errorf("no %s matched: unknown label name(s) %v for this %s provider; closest valid label(s): %v", noun, unknown, providerNoun, suggestions)
	}
	return fmt.Errorf("no %s matched: unknown label name(s) %v for this %s provider; verify the name is correct or remove this filter", noun, unknown, providerNoun)
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
	return unknownLabelError("logs", "log", unknown, available)
}

// valueValidationLookback bounds how far back the value validator looks when establishing a
// label's real value set. Label values are time-bounded, so checking them against the
// request's (possibly narrow) window would mislabel a value that merely has no data in that
// window as "wrong". A wider window establishes the true value universe; a value absent from
// it is genuinely unknown, while a value present in it (but not in the narrow request window)
// is a time-range problem handled separately.
const valueValidationLookback = 7 * 24 * 60 * 60 // seconds

// maxLabelValuesToScan caps how many values we pull per label before giving up on value
// suggestion. High-cardinality labels (pod names, request ids) can have thousands of values;
// scanning them all wastes latency and tokens, and an equality filter on such a label is
// rarely a typo. Above the cap we fail open (no diagnosis).
const maxLabelValuesToScan = 2000

// unknownValueError builds the actionable message returned when a query filters a label to a
// value the provider has never seen. It names the label and offending value plus the closest
// valid value(s); when nothing is close it gives action-agnostic guidance (verify or drop the
// filter) rather than naming a listing tool the caller may not have. Never dumps the full value
// list (token-conscious, mirroring unknownLabelError).
func unknownValueError(label, value string, candidates []string) error {
	if suggestions := closestValues(value, candidates); len(suggestions) > 0 {
		return fmt.Errorf("no logs matched: value %q for label %q not found; closest valid value(s): %v", value, label, suggestions)
	}
	return fmt.Errorf("no logs matched: value %q for label %q was not found for this log provider; verify the value is correct or remove this filter", value, label)
}

// validateReferencedLabelValues checks, for a query that returned no logs, whether an equality
// filter targets a value the log provider has never emitted for that label — the classic
// "namespace=prodd" typo. For each referenced field it fetches the label's real values over a
// widened window (see valueValidationLookback) and, if the filtered value is absent, returns an
// actionable error naming the closest valid value(s). Best-effort throughout: unknown/high-
// cardinality labels, discovery errors, and empty value sets all fail open (return nil) so a
// legitimately-empty query is never blocked. `referencedValues` is in provider label space
// (mapping was applied upstream in FetchLogs), matching the space QueryLabelValues expects.
func validateReferencedLabelValues(ctx *security.RequestContext, source LogSource, fetchLogRequest FetchLogRequest, referencedValues map[string][]string) error {
	if len(referencedValues) == 0 {
		return nil
	}

	lineContent := make(map[string]struct{}, len(lineContentFields))
	for _, f := range lineContentFields {
		lineContent[f] = struct{}{}
	}

	// Widen the window so we probe the label's full value universe, not just the request slice.
	// StartTime/EndTime are millisecond epoch; valueValidationLookback is in seconds, so scale it.
	startTime, endTime := fetchLogRequest.StartTime, fetchLogRequest.EndTime
	if endTime > 0 {
		if wideStart := endTime - valueValidationLookback*1000; wideStart < startTime {
			startTime = wideStart
		}
	}

	labels := make([]string, 0, len(referencedValues))
	for label := range referencedValues {
		labels = append(labels, label)
	}
	sort.Strings(labels)

	for _, label := range labels {
		// Line-body filters (content/message/…) aren't labels — never value-check them.
		if _, ok := lineContent[label]; ok {
			continue
		}
		labelValues, err := source.QueryLabelValues(ctx, FetchLogLabelValuesRequest{
			LabelName:         label,
			AccountId:         fetchLogRequest.AccountId,
			LogProvider:       fetchLogRequest.LogProvider,
			LogProviderSource: fetchLogRequest.LogProviderSource,
			StartTime:         startTime,
			EndTime:           endTime,
		})
		// Unknown label, discovery failure, empty or high-cardinality value set → fail open.
		if err != nil || len(labelValues) == 0 || len(labelValues) > maxLabelValuesToScan {
			continue
		}
		valueSet := make(map[string]struct{}, len(labelValues))
		candidates := make([]string, len(labelValues))
		for i, v := range labelValues {
			valueSet[v.Value] = struct{}{}
			candidates[i] = v.Value
		}
		for _, want := range referencedValues[label] {
			if _, ok := valueSet[want]; !ok {
				return unknownValueError(label, want, candidates)
			}
		}
	}
	return nil
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
	return unknownLabelError("traces", "trace", unknown, available)
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
	labels, err := source.QueryLabels(ctx, fetchLogRequest)
	if err != nil {
		return nil, err
	}
	// Normalize each provider's own type vocabulary (Pinot "INT", Signoz "int64",
	// Hive "bigint", ES "long", …) into DataType here, at the one place every
	// provider's labels pass through, so no provider has to implement anything.
	for i := range labels {
		labels[i].DataType = normalizeLabelDataType(labels[i].Attributes)
	}
	return labels, nil
}

func FetchLogLabelValues(ctx *security.RequestContext, fetchLogRequest FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	source, err := getLogSourceForAccount(ctx, fetchLogRequest.AccountId, fetchLogRequest.LogProvider, fetchLogRequest.LogProviderSource)
	if err != nil {
		return nil, err
	}
	return source.QueryLabelValues(ctx, fetchLogRequest)
}

// esAllIndicesWildcard is the last-resort field-listing target, matching the
// fallback QueryLogGroup already uses when no log index is configured.
const esAllIndicesWildcard = "*"

// resolveLogFieldsIndex picks the index a field listing runs against:
// request.index, else the account default. Returns "" when neither resolves —
// deliberately NOT the wildcard, because the ES sources hold a second default
// (cfg.LogIndex, read straight off the integration record) that only they can
// see, and injecting "*" here would mask it. They own the last resort.
func resolveLogFieldsIndex(requestIndex, defaultIndex string) string {
	if idx := strings.TrimSpace(requestIndex); idx != "" {
		return idx
	}
	return strings.TrimSpace(defaultIndex)
}

// FetchLogLabelsOrIndexFields serves the logs_list_labels action.
//
//	fetch_index=true  index targets (ES only — rejected elsewhere, since a
//	                  provider with no index concept can only mean a confused caller)
//	absent/false      queryable fields: ES resolves an index and reads _mapping,
//	                  every other provider's QueryLabels already returns fields
//
// The flag used to mean the opposite, so the no-flag answer for ES was a list of
// index NAMES where callers — LLM agents especially — expected field names, and
// nothing errored to say so.
func FetchLogLabelsOrIndexFields(ctx *security.RequestContext, fetchLogRequest FetchLogLabelRequest) ([]OutputLogLabel, error) {
	// LogProvider is forwarded so a pinned provider resolves against itself; the
	// old path passed "" and answered from the account default. WithIntegration
	// rather than GetDefaultProvider: it returns the integration the default
	// index is read from, without the capabilities work a listing never uses.
	provider, integrationSource, integrationDto, err := getLogsMetricsTracesProviderWithIntegration(ctx, fetchLogRequest.AccountId, fetchLogRequest.LogProvider, "logs", fetchLogRequest.LogProviderSource)
	if err != nil {
		return nil, err
	}

	// An unresolved provider falls through, so a missing integration reports
	// itself rather than being blamed on the flag.
	if provider != "ES" {
		if fetchLogRequest.FetchIndex && provider != "" {
			return nil, fmt.Errorf("fetch_index is only supported for the Elasticsearch log provider: %q has no index concept — omit fetch_index to list its queryable labels", provider)
		}
		return FetchLogLabels(ctx, fetchLogRequest)
	}

	if fetchLogRequest.FetchIndex {
		return FetchLogLabels(ctx, fetchLogRequest)
	}

	logSource, err := getLogSource(provider, integrationSource)
	if err != nil {
		return nil, err
	}

	// Same default get_default_provider reports: per-account override → log_index.
	// Note this is nil whenever the caller supplied BOTH log_provider and
	// log_provider_source (llm-server does), so an unresolved index here is
	// normal, not exceptional — the source resolves it from its own config.
	defaultIndex := readIndexFromIntegration(ctx, integrationDto, "logs", fetchLogRequest.AccountId)
	if index := resolveLogFieldsIndex(common.GetString(fetchLogRequest.Request, "index"), defaultIndex); index != "" {
		// Both ES variants read Request["index"], so pass it there rather than
		// teaching each about default resolution. Copied, not mutated: Request is
		// the caller's and a resolved default must not leak back into it.
		request := make(map[string]any, len(fetchLogRequest.Request)+1)
		for k, v := range fetchLogRequest.Request {
			request[k] = v
		}
		request["index"] = index
		fetchLogRequest.Request = request
	}

	ctx.GetLogger().Info("logs_list_labels: listing index fields",
		"account_id", fetchLogRequest.AccountId, "provider", provider,
		"integration_source", integrationSource,
		"index", common.GetString(fetchLogRequest.Request, "index"))

	var fields []OutputLogLabelFields
	switch s := logSource.(type) {
	case *ElasticSource:
		fields, err = s.QueryIndexFields(ctx, fetchLogRequest)
	case *ElasticSaasSource:
		fields, err = s.QueryIndexFields(ctx, fetchLogRequest)
	default:
		return nil, fmt.Errorf("log source does not support QueryIndexFields")
	}
	if err != nil {
		return nil, err
	}
	return LabelsFromIndexFields(fields), nil
}

func FetchLogGroup(ctx *security.RequestContext, fetchLogGroupRequest FetchLogGroupRequest) (LogGroupOutput, error) {
	source, err := getLogGroupSourceForAccount(ctx, fetchLogGroupRequest.AccountId, fetchLogGroupRequest.LogProvider, fetchLogGroupRequest.LogProviderSource)
	if err != nil {
		return LogGroupOutput{}, err
	}
	out, err := source.QueryLogGroup(ctx, fetchLogGroupRequest)
	if err != nil {
		return LogGroupOutput{}, err
	}
	return mergeLogGroupsByPattern(out), nil
}

// mergeLogGroupsByPattern collapses groups describing the same pattern in the same
// place, so that pattern_hash identifies exactly one row.
//
// Sources split into two camps. Some group in-process by pattern hash (Loki,
// SolarWinds, Loggly) and arrive already merged — for them this is a no-op. The
// rest push aggregation down to the provider and group by the *raw* message
// (Elasticsearch terms, Dynatrace summarize, Pinot GROUP BY, New Relic FACET), so
// one pattern arrives split across a row per message variant, every row carrying
// the same pattern_hash. The UI treats pattern_hash as a ticket reference id and
// resolves it with a findIndex, so duplicate hashes silently attach a ticket to
// whichever row happens to come first.
//
// Merging centrally lets each source keep the strategy that suits it while the
// invariant holds for all of them.
func mergeLogGroupsByPattern(out LogGroupOutput) LogGroupOutput {
	type mergeKey struct{ hash, namespace, workload, level string }

	type entry struct {
		group *LogGroup
		tsIdx map[int64]int // timestamp -> index into Timestamps/Values
	}

	merged := make(map[mergeKey]*entry, len(out.Groups))
	order := make([]mergeKey, 0, len(out.Groups))

	for i := range out.Groups {
		g := out.Groups[i]
		key := mergeKey{hash: g.PatternHash, namespace: g.Namespace, workload: g.Workload, level: g.Level}

		existing, ok := merged[key]
		if !ok {
			cp := g
			cp.Timestamps = append([]int64(nil), g.Timestamps...)
			cp.Values = append([]float64(nil), g.Values...)

			tsIdx := make(map[int64]int, len(cp.Timestamps))
			for j, ts := range cp.Timestamps {
				tsIdx[ts] = j
			}

			merged[key] = &entry{group: &cp, tsIdx: tsIdx}
			order = append(order, key)
			continue
		}

		existing.group.Count += g.Count

		// Series are summed per timestamp rather than concatenated: the Prometheus
		// source returns a real multi-point series, and the rest a single point.
		for j, ts := range g.Timestamps {
			var v float64
			if j < len(g.Values) {
				v = g.Values[j]
			}
			if idx, found := existing.tsIdx[ts]; found {
				existing.group.Values[idx] += v
				continue
			}
			existing.tsIdx[ts] = len(existing.group.Timestamps)
			existing.group.Timestamps = append(existing.group.Timestamps, ts)
			existing.group.Values = append(existing.group.Values, v)
		}
	}

	groups := make([]LogGroup, 0, len(order))
	for _, key := range order {
		g := merged[key].group
		sort.Sort(&timestampedValues{timestamps: g.Timestamps, values: g.Values})
		groups = append(groups, *g)
	}

	// Merging changes counts, so the by-count ordering the sources applied no
	// longer holds.
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Count > groups[j].Count })

	return LogGroupOutput{Groups: groups}
}

// timestampedValues keeps a group's Values aligned with its Timestamps while
// sorting chronologically.
type timestampedValues struct {
	timestamps []int64
	values     []float64
}

func (t *timestampedValues) Len() int           { return len(t.timestamps) }
func (t *timestampedValues) Less(i, j int) bool { return t.timestamps[i] < t.timestamps[j] }
func (t *timestampedValues) Swap(i, j int) {
	t.timestamps[i], t.timestamps[j] = t.timestamps[j], t.timestamps[i]
	if i < len(t.values) && j < len(t.values) {
		t.values[i], t.values[j] = t.values[j], t.values[i]
	}
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
		SupportsServiceMap:    true,
		SupportsTraceGrouping: true,
		SupportsRawQuery:      true,
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
	// Logs, metrics and traces are all implemented.
	//
	// SupportsRawQuery is true because QueryLogs, FetchMetricsQuery and QueryTraces all
	// honour a caller-supplied query directly. Grouping and the heatmap are true because
	// SplunkEnterpriseTraceSource implements QueryGroupedTraces and QueryTracesHeatmap
	// with real SPL aggregations rather than the "not implemented" stubs some providers
	// return.
	//
	// SupportsServiceMap stays false: the map needs caller-to-callee edges, and a Splunk
	// span carries only a bare peer NAME for its callee with no peer namespace anywhere
	// in the schema — the same reason destination_workload_namespace is unfilterable. An
	// edge list built from names alone would silently merge same-named services in
	// different namespaces, so the view is not advertised rather than drawn wrong.
	"splunk_enterprise": {
		SupportsServiceMap:    false,
		SupportsRawQuery:      true,
		SupportsHeatmap:       true,
		SupportsTraceGrouping: true,
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
	// resolvedSource carries the source out of the switch so the operator descriptors
	// below can pick up its operator↔data-type override, if it declares one.
	var resolvedSource any
	switch providerType {
	case "logs":
		source, err := getLogSource(provider, integrationSource)
		if err == nil {
			resolvedSource = source
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
		source, err := resolveTraceSource(ctx, accountId, provider, integrationSource, "")
		if err == nil {
			resolvedSource = source
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
			resolvedSource = source
			caps.SupportedOperators = source.GetSupportedOperators()
			// No metric label mapping today — metric sources don't implement
			// GetLabelMapping; LabelMappings stays empty for metrics.
		} else {
			slog.Warn("getProviderCapabilities: failed to get metrics source", "provider", provider, "error", err)
		}
	}

	// Descriptors carry the operator↔data-type matrix the UI filters its per-label
	// operator menu with. Apply the source's override here — the single point where
	// descriptors are built — so the menu the UI offers and the rule the log validator
	// enforces resolve identically and can never drift apart.
	caps.SupportedOperatorDescriptors = applyOperatorDataTypeOverrides(
		query.DescribeOperators(caps.SupportedOperators), resolvedSource)
	return caps
}

func GetDefaultProvider(context *security.RequestContext, accountId, providerType, providerSource, requestedProvider string) (*DefaultProviderResponse, error) {
	// requestedProvider (empty for the default-provider case) pins resolution to a
	// specific provider so the logs tab can seed an overridden provider's index.
	defaultProvider, integrationSource, integrationDto, err := getLogsMetricsTracesProviderWithIntegration(context, accountId, requestedProvider, providerType, providerSource)
	if err != nil {
		return nil, err
	}
	caps := getProviderCapabilities(context, accountId, defaultProvider, integrationSource, providerType)
	return &DefaultProviderResponse{
		Provider:           defaultProvider,
		IntegrationSource:  integrationSource,
		DefaultIndex:       readIndexFromIntegration(context, integrationDto, providerType, accountId),
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
// config value from the supplied integration. When the integration carries a
// per-account index_account_mapping (ES "Advanced Settings"), a non-empty
// override for accountId wins over the top-level value. Returns an empty string
// when no integration was matched or the entry is unset.
func readIndexFromIntegration(ctx *security.RequestContext, integrationDto *core.IntegrationDto, providerType string, accountId string) string {
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
	// Per-account override (ES Advanced Settings) takes precedence. The value is
	// absent on non-ES integrations, so this is a no-op there.
	if mapping, err := core.GetIntegrationConfigValueByName(ctx, integrationDto.Id, ElasticsearchIndexAccountMapping); err == nil {
		if override := resolveESIndexOverride(mapping, accountId, providerType); override != "" {
			return override
		}
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
		provider, source, integrationDto, err := getLogsMetricsTracesProviderWithIntegration(ctx, accountId, "", providerType, "")
		if err != nil || provider == "" {
			continue
		}
		caps := getProviderCapabilities(ctx, accountId, provider, source, providerType)
		// Surface the account's resolved default index (per-account ES Advanced Settings
		// mapping → top-level {trace,log,metrics}_index), matching get_default_provider.
		// Lets consumers that read the shared capabilities list (e.g. the Cross-Zone /
		// Group trace subtabs) scope queries to the same index without a second call.
		caps.DefaultIndex = readIndexFromIntegration(ctx, integrationDto, providerType, accountId)
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

	source, err := resolveTraceSource(ctx, accountId, traceProvider, integrationSource, "")
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
	source, err := resolveTraceSource(context, labelValuesRequest.AccountId, traceProvider, integrationSource, traceIndexOverride(labelValuesRequest.Request))
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
	source, err := resolveTraceSource(context, request.AccountId, traceProvider, integrationSource, "")
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
	source, err := resolveTraceSource(context, TraceQuery.AccountId, traceProvider, integrationSource, traceIndexOverride(TraceQuery.Request))
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
	source, err := resolveTraceSource(context, TraceQuery.AccountId, traceProvider, integrationSource, traceIndexOverride(TraceQuery.Request))
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
	source, err := resolveTraceSource(context, TracesHeatMapRequest.AccountId, traceProvider, integrationSource, traceIndexOverride(TracesHeatMapRequest.Request))
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

	source, err := resolveTraceSource(context, fetchTracesRequest.AccountId, traceProvider, integrationSource, traceIndexOverride(fetchTracesRequest.Request))
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
	source, err := resolveTraceSource(context, fetchTracesRequest.AccountId, traceProvider, integrationSource, traceIndexOverride(fetchTracesRequest.Request))
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
	source, err := resolveTraceSource(context, fetchTracesRequest.AccountId, traceProvider, integrationSource, traceIndexOverride(fetchTracesRequest.Request))
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

	source, err := resolveTraceSource(context, fetchTracesRequest.AccountId, traceProvider, integrationSource, traceIndexOverride(fetchTracesRequest.Request))
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
	source, err := resolveTraceSource(context, fetchTracesRequest.AccountId, traceProvider, integrationSource, traceIndexOverride(fetchTracesRequest.Request))
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
	// Mirror FetchLogs' data-type reconciliation: this endpoint is the preview that
	// seeds the builder's raw-query editor, so it must both fail on the same input and
	// emit the same literals, rather than handing back SQL that dies when submitted.
	if err := applyLabelDataTypes(ctx, source, &fetchLogRequest); err != nil {
		return OutputLogQuery{}, err
	}
	// Also mirror FetchLogs' always-apply default filters: this endpoint generates
	// the SQL text that seeds the log builder's raw-query editor (Pinot's default
	// mode), and once that text is submitted as a raw query, FetchLogs has no where
	// clause left to inject into — so the filter must already be baked in here.
	ApplyDefaultLogFilters(ctx, &fetchLogRequest)
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
	// NodePods is the pod list resolved for NodeName, used to answer node-scoped
	// questions from metricsets that carry no node field. Filled in by the
	// Elasticsearch path just before querying; never parsed from the request.
	NodePods []string
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
