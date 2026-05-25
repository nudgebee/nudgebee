package observability

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/integrations"
	"nudgebee/services/internal/database"
	"nudgebee/services/ml"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/prometheus/promql/parser"
)

func init() {
	playbooks.RegisterAction("metrics", &observabilityMetricsAction{})
	playbooks.RegisterAction("prometheus_enricher", &prometheusAction{})
	playbooks.RegisterAction("application_performance_metrics", &applicationPerformanceMetricsAction{})
	playbooks.RegisterAction("datadog_metrics", &datadogMetricsAction{})
	playbooks.RegisterAction("metric_anomaly_enricher", &metricAnomalyAction{})
}

type PrometheusInstantResult struct {
	Metric map[string]any `json:"metric"`
	Value  []any          `json:"value"`
}

type prometheusAction struct{}
type prometheusEnricherParams struct {
	Instant       bool                   `json:"instant,omitempty"`
	Duration      map[string]any         `json:"duration,omitempty"`
	Step          string                 `json:"step,omitempty"`
	PromqlQuery   string                 `json:"promql_query,omitempty"`
	PromqlQueries []playbooks.NamedQuery `json:"promql_queries,omitempty"`
}

func (a *prometheusAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	// Skip Prometheus action for Datadog events
	if ctx.GetEvent().Source == "datadog_webhook" {
		return false
	}

	// Skip Prometheus action for AWS CloudWatch alarms
	labels := ctx.GetEvent().Labels
	if labels["aws_region"] != "" || labels["aws_account"] != "" || labels["aws_event_metric_namespace"] != "" {
		return false
	}

	// Skip Prometheus action for Azure alerts — handled by cloud_metrics and cloud_azure_* actions
	if ctx.GetEvent().Source == "Azure_Monitor_Alert" || ctx.GetEvent().Source == "azure_monitor_webhook" {
		return false
	}

	// Skip Prometheus action for GCP alerts
	if labels["gcp_region"] != "" || labels["gcp_account"] != "" {
		return false
	}

	expr, err := a.getValidPrometheusExpressionFromEventRules(ctx)
	return err == nil && expr != ""
}

func (a *prometheusAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	expr, err := a.getValidPrometheusExpressionFromEventRules(ctx)
	if err != nil {
		return nil, err
	}

	params := map[string]any{
		"promql_query": expr,
		"instant":      false,
		"duration": map[string]any{
			"duration_minutes": 10,
		},
	}

	ctx.GetLogger().Info("prometheus auto action: executing with query from event_rules",
		"alert", ctx.GetEvent().Name, "query", expr)

	return a.Execute(ctx, params)
}

// getValidPrometheusExpressionFromEventRules retrieves and validates a Prometheus expression from event_rules table.
// It tries the event name first, then falls back to nb_alert_name and alertname labels.
func (a *prometheusAction) getValidPrometheusExpressionFromEventRules(ctx playbooks.PlaybookActionContext) (string, error) {
	namesToTry := []string{}
	if ctx.GetEvent().Name != "" {
		namesToTry = append(namesToTry, ctx.GetEvent().Name)
	}
	if ctx.GetEvent().Labels != nil {
		if n := ctx.GetEvent().Labels["nb_alert_name"]; n != "" && n != ctx.GetEvent().Name {
			namesToTry = append(namesToTry, n)
		}
		if n := ctx.GetEvent().Labels["alertname"]; n != "" {
			found := false
			for _, existing := range namesToTry {
				if existing == n {
					found = true
					break
				}
			}
			if !found {
				namesToTry = append(namesToTry, n)
			}
		}
	}
	if len(namesToTry) == 0 {
		return "", fmt.Errorf("event name is empty and no alert name labels found")
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("prometheus auto action: failed to get database manager", "error", err)
		return "", fmt.Errorf("failed to get database manager: %w", err)
	}

	for _, name := range namesToTry {
		var expr string
		err = dbms.Db.QueryRowx("SELECT expr FROM event_rules WHERE alert = $1 AND account_id = $2 AND enabled = true AND expr IS NOT NULL AND expr != '' LIMIT 1",
			name, ctx.GetAccountId()).Scan(&expr)
		if err != nil {
			continue
		}
		_, parseErr := parser.ParseExpr(expr)
		if parseErr != nil {
			ctx.GetLogger().Warn("prometheus auto action: invalid PromQL expression in event_rules",
				"alert", name, "expr", expr, "error", parseErr)
			continue
		}
		return expr, nil
	}

	return "", fmt.Errorf("no valid prometheus expression found for alerts %v", namesToTry)
}

func (a *prometheusAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params prometheusEnricherParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	endTime := time.Now()
	durationMinues := 10
	if params.Duration != nil {
		if dm, ok := params.Duration["duration_minutes"]; ok {
			switch dmv := dm.(type) {
			case string:
				durationMinues, err = strconv.Atoi(dmv)
				if err != nil {
					return nil, err
				}
			case int:
				durationMinues = dmv
			case float64:
				durationMinues = int(dmv)
			default:
				return nil, fmt.Errorf("unsupported type for duration_minutes: %T", dmv)
			}
		}
	}
	startTime := endTime.Add(-time.Duration(durationMinues) * time.Minute)
	if ctx.GetEvent().StartedAt != nil {
		startTime = *ctx.GetEvent().StartedAt
	}
	if ctx.GetEvent().EndedAt != nil {
		endTime = *ctx.GetEvent().EndedAt
	}
	if params.PromqlQueries == nil {
		params.PromqlQueries = []playbooks.NamedQuery{}
	}

	extractSeriesResponse := false
	rawQuery := params.PromqlQuery
	if params.PromqlQuery != "" {
		params.PromqlQueries = append(params.PromqlQueries, playbooks.NamedQuery{
			Key:   "A",
			Query: params.PromqlQuery,
		})
		params.PromqlQuery = ""
		extractSeriesResponse = true
	}

	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  ctx.GetAccountId(),
			ActionName: "prometheus_queries_enricher",
			ActionParams: map[string]any{
				"duration": map[string]any{
					"ends_at":   endTime.UTC().Format("2006-01-02 15:04:05 UTC"),
					"starts_at": startTime.UTC().Format("2006-01-02 15:04:05 UTC"),
				},
				"steps":          params.Step,
				"instant":        params.Instant,
				"promql_query":   params.PromqlQuery,
				"promql_queries": params.PromqlQueries,
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}
	relayResponse, additionalInfo, err := relay.ExecuteAndExtractResponse(relayRequest)
	if err != nil {
		return nil, err
	}

	data := map[string]any{}
	if relayResponse["data"] != nil {
		switch d := relayResponse["data"].(type) {
		case map[string]any:
			data = d
		case string:
			err := common.UnmarshalJson([]byte(d), &data)
			if err != nil {
				ctx.GetLogger().Error("prometheus: unable to parse response", "error", err, "response", d)
			}
		}
	}

	if extractSeriesResponse && data["A"] != nil {
		data = data["A"].(map[string]any)
	}

	metadata, ok := relayResponse["metadata"].(map[string]any)
	if !ok {
		metadata = map[string]any{}
	}
	if rawQuery != "" {
		metadata["query"] = rawQuery
	}
	metadata["query-result-version"] = "1.0"

	insight := playbooks.InsightFromRelayResponse(relayResponse)

	// Generate Chronosphere metrics explorer URL
	if len(params.PromqlQueries) > 0 {
		baseURL := getChronosphereBaseURL(ctx.GetAccountId())
		if baseURL != "" {
			metricsExplorerURL, err := buildChronosphereMetricsExplorerURL(baseURL, params.PromqlQueries, durationMinues)
			if err != nil {
				ctx.GetLogger().Warn("prometheusAction: failed to build metrics explorer URL", "error", err, "accountId", ctx.GetAccountId())
			} else {
				additionalInfo["reference_url"] = metricsExplorerURL
				ctx.GetLogger().Debug("prometheusAction: generated metrics explorer URL", "url", metricsExplorerURL)
			}
		}
	}

	return playbooks.PrometheusActionResponse{
		AdditionalInfo: additionalInfo,
		Data:           data,
		Metadata:       metadata,
		Insight:        insight,
	}, err
}

// getChronosphereBaseURL retrieves the Chronosphere base URL from the agent table for the given account
func getChronosphereBaseURL(accountId string) string {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Warn("prometheusAction: failed to get database manager for Chronosphere URL", "error", err)
		return ""
	}

	query := `SELECT connection_status->>'prometheusUrl'
	          FROM agent
	          WHERE cloud_account_id = $1
	          AND connection_status->>'prometheusUrl' LIKE '%chronosphere.io%'
	          LIMIT 1`

	var prometheusUrl string
	err = dbms.Db.QueryRowx(query, accountId).Scan(&prometheusUrl)
	if err != nil {
		slog.Debug("prometheusAction: no Chronosphere URL found for account", "accountId", accountId, "error", err)
		return ""
	}

	// Remove /data/metrics suffix if present for explorer-v2 path
	prometheusUrl = strings.TrimSuffix(prometheusUrl, "/data/metrics")
	prometheusUrl = strings.TrimRight(prometheusUrl, "/")

	return prometheusUrl
}

// formatRelativeTime converts duration in minutes to Chronosphere relative time format
func formatRelativeTime(durationMinutes int) string {
	if durationMinutes < 60 {
		return fmt.Sprintf("%dm", durationMinutes)
	} else if durationMinutes < 1440 {
		hours := durationMinutes / 60
		return fmt.Sprintf("%dh", hours)
	} else {
		days := durationMinutes / 1440
		return fmt.Sprintf("%dd", days)
	}
}

// buildChronosphereMetricsExplorerURL constructs the Chronosphere metrics explorer URL
func buildChronosphereMetricsExplorerURL(baseURL string, promqlQueries []playbooks.NamedQuery, durationMinutes int) (string, error) {
	if len(promqlQueries) == 0 {
		return "", fmt.Errorf("no PromQL queries provided")
	}

	// Build queries array for Chronosphere
	queries := make([]map[string]any, 0, len(promqlQueries))
	for i, namedQuery := range promqlQueries {
		query := map[string]any{
			"kind": "DataQuery",
			"spec": map[string]any{
				"plugin": map[string]any{
					"kind": "PrometheusTimeSeriesQuery",
					"spec": map[string]any{
						"query": namedQuery.Query,
					},
				},
				"id": strconv.Itoa(i),
			},
		}
		queries = append(queries, query)
	}

	// Marshal queries to JSON
	queriesJSON := common.MarshalJsonSafeString(queries)
	if queriesJSON == "" {
		return "", fmt.Errorf("failed to marshal queries")
	}

	// Convert duration to relative time format
	relativeTime := formatRelativeTime(durationMinutes)

	// Build the URL
	metricsExplorerURL := fmt.Sprintf("%s/metrics/explorer-v2?queries=%s&start=%s&formulas=[]",
		baseURL,
		url.QueryEscape(queriesJSON),
		relativeTime,
	)

	return metricsExplorerURL, nil
}

type applicationPerformanceMetricsAction struct{}

type ApplicationPerformanceMetricsResponse struct {
	Metadata       map[string]any                            `json:"metadata"`
	Data           []map[string]any                          `json:"data"`
	AdditionalInfo map[string]any                            `json:"additional_info"`
	Insight        []playbooks.PlaybookActionResponseInsight `json:"insight"`
}

type ApplicationMetricsRequest struct {
	Title string `json:"title"`
	Query string `json:"query"`
}

func (m ApplicationPerformanceMetricsResponse) GetAdditionalInfo() map[string]any {
	return m.AdditionalInfo
}

func (m ApplicationPerformanceMetricsResponse) GetInsights() []playbooks.PlaybookActionResponseInsight {
	return m.Insight
}

func (m ApplicationPerformanceMetricsResponse) GetFormatName() string {
	return "application-performance-metrics"
}

func (m ApplicationPerformanceMetricsResponse) GetData() any {
	return m.Data
}

type ApplicationPerformanceMetricsRequest struct {
	Queries []ApplicationMetricsRequest `json:"queries"`
}

func (a *applicationPerformanceMetricsAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params ApplicationPerformanceMetricsRequest
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	if len(params.Queries) == 0 {
		return nil, fmt.Errorf("no queries provided")
	}
	var data []map[string]any
	for _, query := range params.Queries {
		queryMap, err := common.MarshalStructToMap(query)
		if err != nil {
			return nil, err
		}
		data = append(data, queryMap)
	}
	return ApplicationPerformanceMetricsResponse{
		AdditionalInfo: map[string]any{},
		Data:           data,
		Metadata: map[string]any{
			"query-result-version": "1.0",
		},
		Insight: []playbooks.PlaybookActionResponseInsight{},
	}, nil
}

func (a *applicationPerformanceMetricsAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	// Step 1: Fetch tenant attribute
	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	attrs, err := tenant.GetTenantAttributesByName(requestCtx, "application_performance_metrics")
	if err != nil || len(attrs) == 0 {
		return false
	}

	// Step 2: Parse the tenant attribute value (JSON map of lang -> queries)
	var langQueriesMap map[string][]map[string]any
	err = json.Unmarshal([]byte(attrs[0].Value), &langQueriesMap)
	if err != nil || len(langQueriesMap) == 0 {
		return false
	}

	// Step 3: Check if any language from upstream map is available
	labels := ctx.GetEvent().Labels
	for key := range labels {
		if strings.HasPrefix(key, "language_") {
			lang := labels[key]
			if _, ok := langQueriesMap[lang]; ok {
				return true // Found a matching language
			}
		}
	}

	return false
}

func (a *applicationPerformanceMetricsAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	// Step 1: Fetch tenant attribute
	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	attrs, err := tenant.GetTenantAttributesByName(requestCtx, "application_performance_metrics")
	if err != nil || len(attrs) == 0 {
		return nil, fmt.Errorf("tenant attribute 'application_performance_metrics' not found")
	}

	// Step 2: Parse the tenant attribute value
	var langQueriesMap map[string][]map[string]any
	err = json.Unmarshal([]byte(attrs[0].Value), &langQueriesMap)
	if err != nil {
		return nil, fmt.Errorf("failed to parse tenant attribute: %v", err)
	}

	// Step 3: Find matching language from event labels
	labels := ctx.GetEvent().Labels
	var selectedQueries []map[string]any
	var detectedLang string

	for key := range labels {
		if strings.HasPrefix(key, "language_") {
			lang := labels[key]
			if queries, ok := langQueriesMap[lang]; ok {
				selectedQueries = queries
				detectedLang = lang
				break
			}
		}
	}

	if len(selectedQueries) == 0 {
		return nil, fmt.Errorf("no matching language queries found")
	}

	ctx.GetLogger().Info("application_performance_metrics: auto-executing", "language", detectedLang, "queries_count", len(selectedQueries))

	// Step 4: Return structured response with all selected queries
	return ApplicationPerformanceMetricsResponse{
		AdditionalInfo: map[string]any{
			"language": detectedLang,
		},
		Data: selectedQueries,
		Metadata: map[string]any{
			"query-result-version": "1.0",
			"language":             detectedLang,
			"source":               "tenant_attributes",
		},
		Insight: []playbooks.PlaybookActionResponseInsight{},
	}, nil
}

type observabilityMetricsAction struct{}
type observabilityMetricsActionParams struct {
	Query        string         `json:"query,omitempty"`
	QueryOptions map[string]any `json:"query_options,omitempty"`
	AccountId    string         `json:"account_id,omitempty"`
	Duration     int            `json:"duration,omitempty"`
}

func (a *observabilityMetricsAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	source, err := getMetricsSourceForAccount(requestCtx, ctx.GetAccountId(), "", "")
	if err != nil || source == nil {
		requestCtx.GetLogger().Info("observability: unable to identify metrics source", "account", ctx.GetAccountId())
		return false
	}

	if autoExecutor, ok := source.(PlaybookQueryGenerator); ok {
		return autoExecutor.CanGenerateQuery(ctx)
	}

	// Fallback: auto-execute when event has workload name + namespace
	if ctx.GetEvent().SubjectName != "" && getEventNamespace(ctx.GetEvent()) != "" {
		return true
	}

	return false
}

func (a *observabilityMetricsAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	source, err := getMetricsSourceForAccount(requestCtx, ctx.GetAccountId(), "", "")
	if err != nil || source == nil {
		requestCtx.GetLogger().Info("observability: unable to identify source", "account", ctx.GetAccountId())
		return nil, errors.New("observability: unable to identify source")
	}

	if autoExecutor, ok := source.(PlaybookQueryGenerator); ok {
		query, request, genErr := autoExecutor.GenerateQuery(ctx)
		if genErr == nil {
			params := map[string]any{
				"query":         query,
				"query_options": request,
			}
			result, execErr := a.Execute(ctx, params)
			if execErr == nil && result != nil {
				return result, nil
			}
			ctx.GetLogger().Info("observability: PlaybookQueryGenerator Execute failed, falling back to workload query", "error", execErr)
		} else {
			ctx.GetLogger().Info("observability: PlaybookQueryGenerator failed, trying workload query", "error", genErr)
		}
	}

	// Fallback: workload-based metric query
	return a.autoExecuteByWorkload(ctx)
}

// autoExecuteByWorkload generates metric queries for K8s workloads using standard container metrics.
// It resolves the configured provider to generate provider-appropriate queries (PromQL, Datadog, etc.).
func (a *observabilityMetricsAction) autoExecuteByWorkload(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	workloadName := getEventWorkload(ctx.GetEvent())
	namespace := getEventNamespace(ctx.GetEvent())
	if workloadName == "" || namespace == "" {
		return nil, errors.New("observabilityMetricsAction: workload name and namespace required for fallback")
	}

	// Determine the provider to generate the right query format
	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	provider, _, err := GetLogsMetricsTracesProvider(requestCtx, ctx.GetAccountId(), "", "metrics", "")
	if err != nil {
		return nil, fmt.Errorf("observabilityMetricsAction: unable to determine metrics provider: %w", err)
	}

	queries := generateWorkloadMetricQueries(provider, workloadName, namespace)
	if len(queries) == 0 {
		return nil, fmt.Errorf("observabilityMetricsAction: no workload metric queries available for provider %q", provider)
	}

	ctx.GetLogger().Info("metrics auto action: executing workload-based query",
		"workload", workloadName, "namespace", namespace, "provider", provider)

	params := map[string]any{
		"query": queries["cpu"],
	}
	return a.Execute(ctx, params)
}

// generateWorkloadMetricQueries returns provider-specific metric queries for a K8s workload.
func generateWorkloadMetricQueries(provider, workloadName, namespace string) map[string]string {
	safeWorkload := escapePromQLString(workloadName)
	safeNamespace := escapePromQLString(namespace)

	switch provider {
	case "prometheus", "chronosphere", "signoz":
		return map[string]string{
			"cpu": fmt.Sprintf(
				`sum(rate(container_cpu_usage_seconds_total{namespace="%s",pod=~"%s-.*",container!=""}[5m])) by (pod)`,
				safeNamespace, safeWorkload,
			),
		}
	case "datadog":
		return map[string]string{
			"cpu": fmt.Sprintf(
				`avg:kubernetes.cpu.usage.total{kube_deployment:%s,kube_namespace:%s} by {pod_name}`,
				workloadName, namespace,
			),
		}
	case "newrelic":
		return map[string]string{
			"cpu": fmt.Sprintf(
				"SELECT sum(cpuUsedCores) FROM K8sContainerSample WHERE namespaceName = '%s' AND (deploymentName = '%s' OR podName LIKE '%s-%%') FACET podName TIMESERIES",
				escapeNRQLValue(namespace), escapeNRQLValue(workloadName), escapeNRQLValue(workloadName),
			),
		}
	default:
		return nil
	}
}

func (a *observabilityMetricsAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params observabilityMetricsActionParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	if params.AccountId == "" {
		params.AccountId = ctx.GetAccountId()
	}

	if params.AccountId == "" {
		return nil, errors.New("account_id is required")
	}

	if params.Query == "" {
		return nil, errors.New("query is required")
	}

	startTime := int64(0)
	endTime := int64(0)
	if ctx.GetEvent().StartedAt != nil {
		startTime = ctx.GetEvent().StartedAt.Add(-60 * time.Minute).UnixMilli()
	}
	if ctx.GetEvent().EndedAt != nil {
		endTime = ctx.GetEvent().EndedAt.UnixMilli()
	}

	if endTime == 0 {
		endTime = time.Now().UnixMilli()
	}

	if startTime == 0 {
		if params.Duration < 1 {
			params.Duration = 60
		}
		startTime = endTime - int64(params.Duration*60*1000)
	}

	metricsoutput, err := FetchMetricsQuery(security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil), FetchMetricsRequest{
		AccountId: params.AccountId,
		Queries:   map[string]string{"query": params.Query},
		StartTime: startTime,
		EndTime:   endTime,
		Request:   params.QueryOptions,
	})

	if err != nil {
		return nil, err
	}

	// Skip empty metric results to avoid storing useless evidence
	hasData := false
	for _, r := range metricsoutput.Results {
		if len(r.Payload) > 0 {
			hasData = true
			break
		}
	}
	if !hasData {
		return nil, nil
	}

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}
	resp := playbooks.NewPlaybookActionResponseJson(map[string]any{"data": metricsoutput}, map[string]any{}, []playbooks.PlaybookActionResponseInsight{}, metadata)
	labels := map[string]any{}
	if len(metricsoutput.Results) > 0 && len(metricsoutput.Results[0].Payload) > 0 {
		for k, v := range metricsoutput.Results[0].Payload[0].Metric {
			labels[k] = v
		}
	}
	resp.Labels = labels
	return resp, err
}

// Datadog Metrics Action
type datadogMetricsAction struct{}

type datadogMetricsActionParams struct {
	MetricQuery string `json:"metric_query,omitempty"`
	FromTs      int64  `json:"from_ts,omitempty"`
	ToTs        int64  `json:"to_ts,omitempty"`
	MetricURL   string `json:"metric_url,omitempty"`
}

func (a *datadogMetricsAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	labels := ctx.GetEvent().Labels
	if labels == nil {
		return false
	}

	// Check if we have metric URL or metric query parameters
	if labels["metric_url"] != "" || labels["metric_query"] != "" {
		return true
	}

	return false
}

func (a *datadogMetricsAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	labels := ctx.GetEvent().Labels
	params := map[string]any{}

	if metricURL := labels["metric_url"]; metricURL != "" {
		params["metric_url"] = metricURL
	}

	if metricQuery := labels["metric_query"]; metricQuery != "" {
		params["metric_query"] = metricQuery
	}

	// Set default time range if not provided
	if ctx.GetEvent().StartedAt != nil && ctx.GetEvent().EndedAt != nil {
		params["from_ts"] = ctx.GetEvent().StartedAt.Unix()
		params["to_ts"] = ctx.GetEvent().EndedAt.Unix()
	}

	return a.Execute(ctx, params)
}

func (a *datadogMetricsAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params datadogMetricsActionParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	sc := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)

	// Get Datadog credentials
	apiKey, appKey, site, err := integrations.GetDatadogConfigs(sc, ctx.GetAccountId())
	if err != nil {
		return nil, fmt.Errorf("failed to get datadog configs: %w", err)
	}

	// Parse metric URL if provided
	if params.MetricURL != "" && params.MetricQuery == "" {
		metric, err := url.Parse(params.MetricURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse datadog metric url: %w", err)
		}

		metricQueryParams := metric.Query()
		if query := metricQueryParams.Get("query"); query != "" {
			params.MetricQuery = query
		}

		if fromTs := metricQueryParams.Get("from"); fromTs != "" {
			if fromInt, err := strconv.ParseInt(fromTs, 10, 64); err == nil {
				params.FromTs = fromInt
			}
		}

		if toTs := metricQueryParams.Get("to"); toTs != "" {
			if toInt, err := strconv.ParseInt(toTs, 10, 64); err == nil {
				params.ToTs = toInt
			}
		}
	}

	// Set default time range if not provided
	if params.FromTs == 0 || params.ToTs == 0 {
		now := time.Now()
		params.ToTs = now.Unix()
		params.FromTs = now.Add(-30 * time.Minute).Unix()
	}

	if params.MetricQuery == "" {
		return nil, errors.New("metric_query or metric_url is required")
	}

	// Fetch metrics from Datadog
	eventMetrics, evidence, err := getDatadogMetricsForAction(sc, apiKey, appKey, site, params)
	if err != nil {
		return nil, fmt.Errorf("failed to get datadog metrics: %w", err)
	}

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}

	insights := []playbooks.PlaybookActionResponseInsight{}
	if len(evidence.Insight) > 0 {
		for _, insight := range evidence.Insight {
			insights = append(insights, playbooks.PlaybookActionResponseInsight{
				Message:  insight.Message,
				Severity: insight.Severity,
			})
		}
	}

	return playbooks.NewPlaybookActionResponseJson(eventMetrics, evidence.AdditionalInfo, insights, metadata), nil
}

// Helper function to fetch Datadog metrics
func getDatadogMetricsForAction(sc *security.RequestContext, apiKey, appKey, site string, params datadogMetricsActionParams) (map[string]any, event.EventEvidence, error) {
	// Properly encode the metric query parameter for URL
	encodedQuery := url.QueryEscape(params.MetricQuery)
	requestUrl := fmt.Sprintf(
		"https://%s/api/v1/query?query=%s&from=%d&to=%d",
		site, encodedQuery, params.FromTs, params.ToTs,
	)

	sc.GetLogger().Info("Making Datadog metrics API request",
		"url", requestUrl,
		"original_query", params.MetricQuery,
		"encoded_query", encodedQuery)

	headers := map[string]string{
		"Content-Type":       "application/json",
		"DD-API-KEY":         apiKey,
		"DD-APPLICATION-KEY": appKey,
	}

	// Add timeout and retry logic to handle network issues
	body, err := common.HttpGet(requestUrl, common.HttpWithHeaders(headers), common.HttpWithTimeout(30*time.Second))
	if err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to make request to datadog metrics api: %w", err)
	}
	defer func() {
		if cerr := body.Body.Close(); cerr != nil {
			sc.GetLogger().Error("Error closing response body", "error", cerr)
		}
	}()

	// Check HTTP status code
	if body.StatusCode >= 400 {
		return nil, event.EventEvidence{}, fmt.Errorf("datadog metrics api returned error status: %d", body.StatusCode)
	}

	bodyBytes, err := io.ReadAll(body.Body)
	if err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to read datadog metrics body: %w", err)
	}

	sc.GetLogger().Info("Datadog metrics API response", "status", body.StatusCode, "body_length", len(bodyBytes))

	var eventMetrics map[string]any
	if err := common.UnmarshalJson(bodyBytes, &eventMetrics); err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to unmarshal datadog metrics: %w", err)
	}

	// Debug logging to understand the response structure
	sc.GetLogger().Info("Datadog metrics API response structure", "response", eventMetrics)

	// Check for error in the response
	if errorMsg, hasError := eventMetrics["error"]; hasError {
		errorStr := fmt.Sprintf("%v", errorMsg)
		sc.GetLogger().Error("Datadog API returned error", "error", errorStr)
		return nil, event.EventEvidence{}, fmt.Errorf("datadog metrics query failed: %s", errorStr)
	}

	evidence := event.EventEvidence{}
	if series, ok := eventMetrics["series"]; ok {
		sc.GetLogger().Info("Found series field", "series_type", fmt.Sprintf("%T", series))
		if seriesArray, ok := series.([]any); ok {
			sc.GetLogger().Info("Series array details", "length", len(seriesArray))
			if len(seriesArray) > 0 {
				jsonStr, err := common.MarshalJson(eventMetrics)
				if err != nil {
					return nil, event.EventEvidence{}, fmt.Errorf("marshaling to JSON of metrics: %v", err)
				}
				evidence.Data = string(jsonStr)
				evidence.Type = "json"
				evidence.Insight = []event.EventEvidenceInsight{
					{
						Message:  fmt.Sprintf("Retrieved %d metric series", len(seriesArray)),
						Severity: "info",
					},
				}
				evidence.AdditionalInfo = map[string]any{
					"action_name":            "datadog_metrics",
					"actual_action_name":     "datadog_metrics",
					"action_title":           "Datadog Metrics",
					"conditional_expression": "",
				}
			} else {
				sc.GetLogger().Info("Series array is empty")
			}
		} else {
			sc.GetLogger().Info("Series field is not an array", "actual_type", fmt.Sprintf("%T", series))
		}
	} else {
		sc.GetLogger().Info("No series field found in response", "available_fields", func() []string {
			var fields []string
			for k := range eventMetrics {
				fields = append(fields, k)
			}
			return fields
		}())
	}

	return eventMetrics, evidence, nil
}

// Metric Anomaly Action
type metricAnomalyAction struct{}

type metricAnomalyActionParams struct {
	Namespace             string `json:"namespace"`
	Deployment            string `json:"deployment"`
	Query                 string `json:"query"`
	AnalysisStartTime     string `json:"analysis_start_time"`
	AnalysisEndTime       string `json:"analysis_end_time"`
	HistoricalWindowHours int    `json:"historical_window_hours"`
}

type MetricAnomalyActionResponse struct {
	// Anomaly fields at top level
	Id             string           `json:"id"`
	AccountId      string           `json:"account_id"`
	Tenant         string           `json:"tenant"`
	Name           string           `json:"name"`
	Namespace      string           `json:"namespace"`
	ReferenceValue map[string]any   `json:"reference_value"`
	CurrentValue   float64          `json:"current_value"`
	AnomalyType    string           `json:"anomaly_type"`
	IsAnomaly      bool             `json:"is_anomaly"`
	EvaluatedAt    time.Time        `json:"evaluated_at"`
	PodName        string           `json:"pod_name"`
	HistoricalData []map[string]any `json:"historical_data"`
	Labels         map[string]any   `json:"labels"`
	Query          string           `json:"query"`
	Stats          map[string]any   `json:"stats"`

	// Playbook response fields
	Metadata       map[string]any                            `json:"metadata"`
	AdditionalInfo map[string]any                            `json:"additional_info"`
	Insight        []playbooks.PlaybookActionResponseInsight `json:"insight"`
}

func (m MetricAnomalyActionResponse) GetAdditionalInfo() map[string]any {
	return m.AdditionalInfo
}

func (m MetricAnomalyActionResponse) GetInsights() []playbooks.PlaybookActionResponseInsight {
	return m.Insight
}

func (m MetricAnomalyActionResponse) GetFormatName() string {
	return "metric-anomaly"
}

func (m MetricAnomalyActionResponse) GetData() any {
	// Return the anomaly data as a map for compatibility with playbook interface
	return map[string]any{
		"id":              m.Id,
		"account_id":      m.AccountId,
		"tenant":          m.Tenant,
		"name":            m.Name,
		"namespace":       m.Namespace,
		"reference_value": m.ReferenceValue,
		"current_value":   m.CurrentValue,
		"anomaly_type":    m.AnomalyType,
		"is_anomaly":      m.IsAnomaly,
		"evaluated_at":    m.EvaluatedAt,
		"pod_name":        m.PodName,
	}
}

func (m MetricAnomalyActionResponse) GetIsAnomaly() bool {
	return m.IsAnomaly
}

func (a *metricAnomalyAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params metricAnomalyActionParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	// Validate required parameters
	if params.Query == "" {
		return nil, errors.New("query is required")
	}

	if ctx.GetEvent().StartedAt != nil {
		params.AnalysisStartTime = ctx.GetEvent().StartedAt.Format(time.RFC3339)
	}
	if ctx.GetEvent().EndedAt != nil {
		params.AnalysisEndTime = ctx.GetEvent().EndedAt.Format(time.RFC3339)
	}
	if params.HistoricalWindowHours == 0 {
		params.HistoricalWindowHours = 168 // Default to 7 days
	}

	// Create request context
	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)

	// Build ML request
	mlRequest := ml.MetricAnomalyDetectRequest{
		Namespace:             params.Namespace,
		Deployment:            params.Deployment,
		Account:               ctx.GetAccountId(),
		Tenant:                ctx.GetTenantId(),
		Query:                 params.Query,
		AnalysisStartTime:     params.AnalysisStartTime,
		AnalysisEndTime:       params.AnalysisEndTime,
		HistoricalWindowHours: params.HistoricalWindowHours,
		IncludeHistoricalData: true,
	}

	// Call ML service
	mlAnomalies, err := ml.DetectMetricAnomaly(requestCtx, mlRequest)
	if err != nil {
		ctx.GetLogger().Error("metric_anomaly_enricher: failed to detect anomaly", "error", err)
		if strings.Contains(err.Error(), "500 INTERNAL SERVER ERROR") {
			ctx.GetLogger().Error("metric_anomaly_enricher: ML service returned 500, returning wrapped error", "underlying_error", err.Error())
		}
		return nil, fmt.Errorf("failed to detect metric anomaly: ML service returned 500: %w", err)
	}

	// Format data using Anomaly struct pattern (similar to anomaly evidence collection)
	var anomalyData map[string]any
	hasAnomalyDetected := false

	if len(mlAnomalies) > 0 {
		mlAnomaly := mlAnomalies[0]
		hasAnomalyDetected = mlAnomaly.HasAnomaly

		// Calculate current anomaly value
		anomalyValue := 0.0
		for _, d := range mlAnomaly.Data {
			if d.Anomaly {
				anomalyValue = d.Data
				break
			}
		}

		// Store the entire ML response as reference_value (like OldValue in Anomaly struct)
		referenceValue, err := common.MarshalStructToMap(mlAnomaly)
		if err != nil {
			ctx.GetLogger().Error("metric_anomaly_enricher: unable to convert ML response to map", "error", err)
			referenceValue = map[string]any{}
		}

		// Parse evaluation time
		evalTime := time.Now().UTC()
		if mlAnomaly.EndTime != "" {
			if parsed, err := time.Parse(time.RFC3339, mlAnomaly.EndTime); err == nil {
				evalTime = parsed
			}
		}

		historicalData := []map[string]any{}
		if mlAnomaly.HistoricalData != nil {
			historicalData = mlAnomaly.HistoricalData
		}

		// Build anomaly in the same format as the Anomaly struct
		anomalyData = map[string]any{
			"id":              common.GenerateUUID(),
			"account_id":      mlAnomaly.Account,
			"tenant":          ctx.GetTenantId(),
			"name":            params.Deployment,
			"namespace":       params.Namespace,
			"reference_value": referenceValue,
			"current_value":   anomalyValue,
			"anomaly_type":    "Custom",
			"is_anomaly":      true,
			"evaluated_at":    evalTime,
			"pod_name":        "",
			"historical_data": historicalData,
			"stats":           mlAnomaly.Stats,
		}
	} else {
		anomalyData = map[string]any{
			"is_anomaly": false,
			"message":    "No anomaly data returned",
		}
	}

	// Prepare response metadata
	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                params.Query,
		"namespace":            params.Namespace,
		"deployment":           params.Deployment,
		"analysis_start_time":  params.AnalysisStartTime,
		"analysis_end_time":    params.AnalysisEndTime,
		"historical_window":    params.HistoricalWindowHours,
	}

	// Generate insights
	insights := []playbooks.PlaybookActionResponseInsight{}
	if hasAnomalyDetected {
		insights = append(insights, playbooks.PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Metric anomaly detected for %s in %s", params.Deployment, params.Namespace),
			Severity: "warning",
		})
	} else {
		insights = append(insights, playbooks.PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("No anomalies detected for %s in %s", params.Deployment, params.Namespace),
			Severity: "info",
		})
	}

	// Build response with all fields at top level
	response := MetricAnomalyActionResponse{
		Metadata:       metadata,
		AdditionalInfo: map[string]any{},
		Insight:        insights,
		IsAnomaly:      true,
	}

	// Populate anomaly fields from the map
	if id, ok := anomalyData["id"].(string); ok {
		response.Id = id
	}
	if accountId, ok := anomalyData["account_id"].(string); ok {
		response.AccountId = accountId
	}
	if tenant, ok := anomalyData["tenant"].(string); ok {
		response.Tenant = tenant
	}
	if name, ok := anomalyData["name"].(string); ok {
		response.Name = name
	}
	if namespace, ok := anomalyData["namespace"].(string); ok {
		response.Namespace = namespace
	}
	if referenceValue, ok := anomalyData["reference_value"].(map[string]any); ok {
		response.ReferenceValue = referenceValue
	}
	if currentValue, ok := anomalyData["current_value"].(float64); ok {
		response.CurrentValue = currentValue
	}
	if anomalyType, ok := anomalyData["anomaly_type"].(string); ok {
		response.AnomalyType = anomalyType
	}
	if evaluatedAt, ok := anomalyData["evaluated_at"].(time.Time); ok {
		response.EvaluatedAt = evaluatedAt
	}
	if podName, ok := anomalyData["pod_name"].(string); ok {
		response.PodName = podName
	}

	return response, nil
}
