package observability

import (
	"fmt"
	"log/slog"
	"math"
	"nudgebee/services/account"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/database"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"sort"
	"strings"
	"time"
)

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
	QueryGroupedTraces(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) ([]TraceGroupingValues, error)
	QueryGroupedTracesCount(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) (common.OpenTelemetryTraceGroupCount, error)
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

// injectPromQLLabels appends label selectors to a PromQL expression.
// If the expression already contains {}, the new labels are appended inside.
// If it contains a range selector [Xm], labels are inserted before it.
// Otherwise labels are appended at the end.
func injectPromQLLabels(expr string, labels map[string]string) string {
	if len(labels) == 0 {
		return expr
	}
	keys := sortedKeys(labels)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, k, escapePromQLString(labels[k])))
	}
	newSelector := strings.Join(parts, ",")

	if idx := strings.Index(expr, "{"); idx != -1 {
		closeOffset := strings.Index(expr[idx:], "}")
		if closeOffset == -1 {
			return expr + "{" + newSelector + "}"
		}
		closeIdx := idx + closeOffset
		existing := expr[idx+1 : closeIdx]
		if existing == "" {
			return expr[:idx+1] + newSelector + expr[closeIdx:]
		}
		return expr[:idx+1] + existing + "," + newSelector + expr[closeIdx:]
	}
	if idx := strings.Index(expr, "["); idx != -1 {
		return expr[:idx] + "{" + newSelector + "}" + expr[idx:]
	}
	return expr + "{" + newSelector + "}"
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
			} else if providerType == "metrics" {
				// Prefer a configured SaaS metrics integration (e.g. New Relic) over the
				// agent's PrometheusUrl when no integration is explicitly flagged with
				// default_metrics_provider=true. Without this, accounts with New Relic
				// configured but no default flag silently fall back to "prometheus".
				nrIntegration, err := core.GetIntegrationByType(ctx, accountId, "newrelic")
				if err != nil {
					ctx.GetLogger().Warn("metrics fallback: failed to probe newrelic integration",
						"account_id", accountId, "error", err)
				}
				if nrIntegration != nil {
					defaultProvider = nrIntegration.Type
					defaultSource = nrIntegration.Source
					matchedIntegration = nrIntegration
				} else if agentDetails.Features.PrometheusUrl != nil {
					if strings.Contains(*agentDetails.Features.PrometheusUrl, "chronosphere") {
						defaultProvider = "chronosphere"
					} else {
						defaultProvider = "prometheus"
					}
					defaultSource = "agent"
				}
			} else {
				defaultSource = "agent"
			}
			if defaultSource == "" && (providerType == "logs" || providerType == "traces") {
				defaultSource = "agent"
			}
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
	return defaultProvider, defaultSource, matchedIntegration, nil
}

func FetchLogs(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) ([]OutputLog, error) {
	source, err := getLogSourceForAccount(ctx, fetchLogRequest.AccountId, fetchLogRequest.LogProvider, fetchLogRequest.LogProviderSource)
	if err != nil {
		return nil, err
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

	logs, err := source.QueryLogs(ctx, fetchLogRequest)
	if err != nil {
		return nil, err
	}
	normalizeOutputLogLabels(logs, filteringMap)
	return logs, nil
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
}

func getProviderCapabilities(provider, integrationSource, providerType string) ProviderCapabilities {
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

	// Interface-derived capabilities: operator list and optional interfaces.
	switch providerType {
	case "logs":
		source, err := getLogSource(provider, integrationSource)
		if err == nil {
			caps.SupportedOperators = source.GetSupportedOperators()
			_, caps.SupportsAutoQuery = source.(PlaybookQueryGenerator)
		} else {
			slog.Warn("getProviderCapabilities: failed to get log source", "provider", provider, "error", err)
		}
	case "traces":
		source, err := getTraceSource(provider, integrationSource)
		if err == nil {
			caps.SupportedOperators = source.GetSupportedOperators()
		} else {
			slog.Warn("getProviderCapabilities: failed to get trace source", "provider", provider, "error", err)
		}
	case "metrics":
		source, err := getMetricsSource(provider, integrationSource)
		if err == nil {
			caps.SupportedOperators = source.GetSupportedOperators()
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
	caps := getProviderCapabilities(defaultProvider, integrationSource, providerType)
	return &DefaultProviderResponse{
		Provider:          defaultProvider,
		IntegrationSource: integrationSource,
		DefaultIndex:      readIndexFromIntegration(context, integrationDto, providerType),
		Capabilities:      caps,
	}, nil
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
		caps := getProviderCapabilities(provider, source, providerType)
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
	filteringMap := source.GetLabelMapping()
	labelValuesRequest.QueryRequest.Where = convertWhereClauseWithMApping(labelValuesRequest.QueryRequest.Where, filteringMap)

	return source.GetLabelValues(context, labelValuesRequest)
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

	return source.QueryTraces(context, fetchTracesRequest)
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
	// Apply label mapping before building query
	filteringMap := getMergedLabelMapping(ctx, fetchLogRequest.AccountId, source)
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

func GetMetricsQuery(ctx *security.RequestContext, req FetchMetricsRequest) (FetchMetricQueryOutput, error) {
	source, err := getMetricsSourceForAccount(ctx, req.AccountId, req.MetricProvider, req.MetricProviderSource)
	if err != nil {
		return FetchMetricQueryOutput{}, err
	}
	query, err := source.GetQuery(ctx, req)
	if err != nil {
		return FetchMetricQueryOutput{}, err
	}
	return FetchMetricQueryOutput{Query: query}, nil
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
			queries[metricKey] = fmt.Sprintf(`sum by (instance) (rate(node_cpu_seconds_total{mode!="idle", instance=~"%s|%s"}[5m])) or (sum by (node) (rate(node_cpu_seconds_total{mode!="idle", node=~"%s"}[5m]))) or (sum by (node) (rate(node_resources_cpu_usage_seconds_total{mode!="idle", node=~"%s"}[5m])))`, meta.InternalIP, meta.NodeName, meta.NodeName, meta.NodeName)
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
		case "network_receive_packet":
			queries[metricKey] = fmt.Sprintf(`(sum(rate(container_network_receive_bytes_total{__CLUSTER__ %sjob!=""}[5m]))) or (sum(rate(container_net_tcp_bytes_received_total{__CLUSTER__ %sjob!=""}[5m])))`, containerFilter, containerIDFilter)
		case "network_transmit_packets":
			queries[metricKey] = fmt.Sprintf(`-((sum(rate(container_network_transmit_bytes_total{__CLUSTER__ %sjob!=""}[5m]))) or (sum(rate(container_net_tcp_bytes_sent_total{__CLUSTER__ %sjob!=""}[5m]))))`, containerFilter, containerIDFilter)
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
		case "cpu_real":
			queries[metricKey] = `sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[24h])) or sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[24h]))`
		case "cpu_total":
			queries[metricKey] = `sum(machine_cpu_cores{__CLUSTER__}) or sum(node_resources_cpu_logical_cores{__CLUSTER__})`
		case "mem_real":
			queries[metricKey] = `sum(node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemAvailable_bytes{__CLUSTER__}) or sum(node_resources_memory_total_bytes{__CLUSTER__} - node_resources_memory_available_bytes{__CLUSTER__})`
		case "mem_total":
			queries[metricKey] = `sum(node_memory_MemTotal_bytes{__CLUSTER__}) or sum(node_resources_memory_total_bytes{__CLUSTER__})`
		case "p90_mem":
			queries[metricKey] = `quantile(0.9, node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemAvailable_bytes{__CLUSTER__}) or quantile(0.9, node_resources_memory_total_bytes{__CLUSTER__} - node_resources_memory_available_bytes{__CLUSTER__})`
		case "p90_cpu":
			queries[metricKey] = `sum(quantile_over_time(0.90, rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[24h])[24h:])) or sum(quantile_over_time(0.90, rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[24h])[24h:]))`
		case "p50_mem":
			queries[metricKey] = `quantile(0.5, node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemAvailable_bytes{__CLUSTER__}) or quantile(0.5, node_resources_memory_total_bytes{__CLUSTER__} - node_resources_memory_available_bytes{__CLUSTER__})`
		case "p50_cpu":
			queries[metricKey] = `sum(quantile_over_time(0.50, rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[24h])[24h:])) or sum(quantile_over_time(0.50, rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[24h])[24h:]))`
		case "max_usage_mem":
			queries[metricKey] = `max_over_time(sum(node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemFree_bytes{__CLUSTER__} - node_memory_Buffers_bytes{__CLUSTER__} - node_memory_Cached_bytes{__CLUSTER__})[24h:])`
		case "max_usage_cpu":
			queries[metricKey] = `sum(max_over_time(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[24h])[24h:])) or sum(max_over_time(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[24h])[24h:]))`
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
	_, err = dbms.Exec(query, ctx.GetSecurityContext().GetUserId(), ctx.GetSecurityContext().GetTenantId(), userHistoryRequest.AccountId, userHistoryRequest.Module, userHistoryRequest.Data, time.Now(), time.Now(), userHistoryRequest.Duration, userHistoryRequest.Status)

	if err != nil {
		return nil, fmt.Errorf("failed to insert record in user_history: %w", err)
	}

	return map[string]string{
		"status": "done",
	}, nil
}
