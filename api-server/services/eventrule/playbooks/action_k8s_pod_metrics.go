package playbooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"nudgebee/services/common"
	"nudgebee/services/relay"
	"strconv"
	"strings"
	"time"
)

type podMetricAction struct {
	autodetectResource string
}

type podMetricData struct {
	Name         string           `json:"name"`
	Data         []podMetricEntry `json:"data"`
	ResourceType string           `json:"resource_type"`
}

type podMetricEntry struct {
	Metric     map[string]any `json:"metric"`
	Timestamps []float64      `json:"timestamps"`
	Values     []string       `json:"values"`
}

func (a *podMetricAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	if a.autodetectResource == "" {
		return false
	}
	event := ctx.GetEvent()
	labels := event.Labels
	namespace := event.SubjectNamespace
	if namespace == "" && labels != nil {
		namespace = labels["namespace"]
	}
	if namespace == "" {
		return false
	}
	// Require at least one K8s workload label (deployment, statefulset, daemonset, pod).
	// SubjectName alone is not sufficient — cloud events (e.g., AWS_EventBridge) can have
	// SubjectName (instance ID) and namespace (AmazonEC2) set, which would falsely match.
	if labels == nil {
		return false
	}
	return labels["deployment"] != "" || labels["statefulset"] != "" ||
		labels["daemonset"] != "" || labels["pod"] != ""
}

func (a *podMetricAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	if a.autodetectResource == "" {
		return nil, errors.New("autodetect_resource is required")
	}

	labels := ctx.GetEvent().Labels
	namespace := ctx.GetEvent().SubjectNamespace
	if namespace == "" && labels != nil {
		namespace = labels["namespace"]
	}
	if namespace == "" {
		return nil, errors.New("namespace is required")
	}

	// Prefer workload-level labels over the raw "pod" label.
	// For deployment-level alerts (e.g. KubeDeploymentRolloutStuck), the "pod" label
	// is the kube-state-metrics exporter pod, not the affected deployment's pods.
	podName := ""
	if labels != nil {
		podName = labels["pod"]
		if labels["deployment"] != "" {
			podName = labels["deployment"]
		} else if labels["statefulset"] != "" {
			podName = labels["statefulset"]
		} else if labels["daemonset"] != "" {
			podName = labels["daemonset"]
		}
	}
	// Fall back to SubjectName for agent events
	if podName == "" {
		podName = ctx.GetEvent().SubjectName
	}

	if podName == "" {
		return nil, errors.New("no pod or workload label found")
	}

	params := map[string]any{
		"pod_name":      podName,
		"namespace":     namespace,
		"resource_type": a.autodetectResource,
	}
	return a.Execute(ctx, params)
}

type podMetricEnricherParams struct {
	PodName      string `json:"pod_name,omitempty"`
	Namespace    string `json:"namespace,omitempty"`
	Duration     int    `json:"duration,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
	Cluster      string `json:"cluster,omitempty"`
	ClusterLabel string `json:"cluster_label,omitempty"`
}

func (a *podMetricAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	var params podMetricEnricherParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	// Set defaults
	if params.Duration == 0 {
		params.Duration = 10
	}
	if params.ResourceType == "" {
		params.ResourceType = "CPU"
	}

	// Build prometheus queries for the pod metrics
	promqlQueries := []NamedQuery{}

	clusterLabel := "cluster"
	if params.ClusterLabel != "" {
		clusterLabel = params.ClusterLabel
	}
	replacement := ""
	if params.Cluster != "" {
		replacement = fmt.Sprintf(`%s="%s",`, clusterLabel, params.Cluster)
	}

	// Helper function to handle cluster label replacement
	replaceClusterLabel := func(query string) string {
		// Replace __CLUSTER__ with the actual cluster label
		return strings.ReplaceAll(query, "__CLUSTER__", replacement)
	}

	// Use regex match for workload names (Deployment, StatefulSet, etc.) since their
	// pod names have a random suffix (e.g., "notifications-7f8b9c-xyz").
	// Check both the "kind" label (set by matchWorkloadAndEnrich) and workload-specific
	// labels (deployment, statefulset, daemonset) which are present on alerts like
	// KubeDeploymentRolloutStuck that don't have a "kind" label.
	podSelector := fmt.Sprintf(`pod="%s"`, params.PodName)
	kind := ""
	if ctx.GetEvent().Labels != nil {
		kind = ctx.GetEvent().Labels["kind"]
	}
	if kind == "" {
		kind = ctx.GetEvent().SubjectType
	}
	useRegex := strings.EqualFold(kind, "deployment") || strings.EqualFold(kind, "daemonset") || strings.EqualFold(kind, "replicaset")
	if !useRegex && ctx.GetEvent().Labels != nil {
		useRegex = ctx.GetEvent().Labels["deployment"] != "" ||
			ctx.GetEvent().Labels["statefulset"] != "" ||
			ctx.GetEvent().Labels["daemonset"] != ""
	}
	if useRegex {
		podSelector = fmt.Sprintf(`pod=~"%s-.*"`, params.PodName)
	}

	// Main metric query (CPU or Memory usage)
	var metricQuery string
	if strings.ToUpper(params.ResourceType) == "CPU" {
		metricQuery = replaceClusterLabel(fmt.Sprintf(`rate(container_cpu_usage_seconds_total{__CLUSTER__ %s, namespace="%s"}[5m])`, podSelector, params.Namespace))
	} else {
		metricQuery = replaceClusterLabel(fmt.Sprintf(`container_memory_rss{__CLUSTER__ %s, namespace="%s"}`, podSelector, params.Namespace))
	}

	promqlQueries = append(promqlQueries, NamedQuery{
		Key:   "metrics",
		Query: metricQuery,
	})

	// Query for pod requests and limits (instant queries) - always fetch both CPU and memory
	requestsQuery := replaceClusterLabel(fmt.Sprintf(`kube_pod_container_resource_requests{__CLUSTER__ %s, namespace="%s"}`, podSelector, params.Namespace))
	promqlQueries = append(promqlQueries, NamedQuery{
		Key:   "requests",
		Query: requestsQuery,
	})

	limitsQuery := replaceClusterLabel(fmt.Sprintf(`kube_pod_container_resource_limits{__CLUSTER__ %s, namespace="%s"}`, podSelector, params.Namespace))
	promqlQueries = append(promqlQueries, NamedQuery{
		Key:   "limits",
		Query: limitsQuery,
	})

	// Execute prometheus queries using the existing prometheus enricher
	prometheusData, err := a.executePrometheusQueries(ctx, promqlQueries, params.Duration)
	if err != nil {
		return nil, err
	}

	// Build the pod metric response structure
	podMetric, insights := a.buildPodMetricResponse(ctx, prometheusData, params)

	additionalInfo := map[string]any{
		"title":              "pod_metric_enricher",
		"action_name":        "pod_metric_enricher",
		"actual_action_name": "pod_metric_enricher",
		"metric_name":        params.ResourceType,
		"pod_name":           params.PodName,
		"namespace":          params.Namespace,
		"cluster":            params.Cluster,
		"cluster_label":      params.ClusterLabel,
	}

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}

	return NewPlaybookActionResponseJson(podMetric, additionalInfo, insights, metadata), nil
}

func (a *podMetricAction) executePrometheusQueries(ctx PlaybookActionContext, promqlQueries []NamedQuery, durationMinutes int) (map[string]any, error) {
	endTime := time.Now()
	startTime := endTime.Add(-time.Duration(durationMinutes) * time.Minute)

	if ctx.GetEvent().StartedAt != nil {
		startTime = *ctx.GetEvent().StartedAt
	}
	if ctx.GetEvent().EndedAt != nil {
		endTime = *ctx.GetEvent().EndedAt
	}

	// Separate queries into range queries (metrics) and instant queries (requests/limits)
	rangeQueries := []NamedQuery{}
	instantQueries := []NamedQuery{}

	for _, query := range promqlQueries {
		if query.Key == "requests" || query.Key == "limits" || query.Key == "requests_alt" || query.Key == "limits_alt" {
			instantQueries = append(instantQueries, query)
		} else {
			rangeQueries = append(rangeQueries, query)
		}
	}

	// Execute queries
	data := map[string]any{}

	if len(rangeQueries) > 0 {
		rangeData, err := a.executePrometheusQueryBatch(ctx, rangeQueries, startTime, endTime, false)
		if err != nil {
			return nil, err
		}
		maps.Copy(data, rangeData)
	}

	if len(instantQueries) > 0 {
		instantData, err := a.executePrometheusQueryBatch(ctx, instantQueries, endTime, endTime, true)
		if err != nil {
			return nil, err
		}
		maps.Copy(data, instantData)
	}

	return data, nil
}

func (a *podMetricAction) executePrometheusQueryBatch(ctx PlaybookActionContext, queries []NamedQuery, startTime, endTime time.Time, instant bool) (map[string]any, error) {
	actionParams := map[string]any{
		"duration": map[string]any{
			"ends_at":   endTime.UTC().Format("2006-01-02 15:04:05 UTC"),
			"starts_at": startTime.UTC().Format("2006-01-02 15:04:05 UTC"),
		},
		"instant":        instant,
		"promql_queries": queries,
	}

	// Add step parameter for range queries
	if !instant {
		actionParams["step"] = "30s"
	}

	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:    ctx.GetAccountId(),
			ActionName:   "prometheus_queries_enricher",
			ActionParams: actionParams,
			Origin:       "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}

	relayResponse, _, err := relay.ExecuteAndExtractResponse(relayRequest)
	if err != nil {
		return nil, err
	}

	result := map[string]any{}
	if relayResponse["data"] != nil {
		switch d := relayResponse["data"].(type) {
		case map[string]any:
			result = d
		case string:
			err := common.UnmarshalJson([]byte(d), &result)
			if err != nil {
				queryType := "range"
				if instant {
					queryType = "instant"
				}
				ctx.GetLogger().Error("prometheus: unable to parse response", "error", err, "response", d, "query_type", queryType)
				return nil, err
			}
		}
	}

	return result, nil
}

func (a *podMetricAction) buildPodMetricResponse(ctx PlaybookActionContext, prometheusData map[string]any, params podMetricEnricherParams) (podMetricData, []PlaybookActionResponseInsight) {
	insights := []PlaybookActionResponseInsight{}
	metricEntries := []podMetricEntry{}

	// Process main metric data (CPU or Memory usage)
	if prometheusData["metrics"] != nil {
		entries := a.extractMetricEntries(prometheusData["metrics"])
		metricEntries = append(metricEntries, entries...)
	}

	// Process requests and limits to populate metric entries and generate insights
	requestsMap := a.extractResourceValues(prometheusData["requests"])
	limitsMap := a.extractResourceValues(prometheusData["limits"])

	// Try alternative queries if the main ones didn't return data
	if len(requestsMap) == 0 && prometheusData["requests_alt"] != nil {
		requestsMap = a.extractResourceValues(prometheusData["requests_alt"])
	}
	if len(limitsMap) == 0 && prometheusData["limits_alt"] != nil {
		limitsMap = a.extractResourceValues(prometheusData["limits_alt"])
	}

	// Debug: check if we got any requests/limits data
	ctx.GetLogger().Debug("pod_metrics_enricher: processing requests and limits",
		"requests_found", len(requestsMap),
		"limits_found", len(limitsMap),
		"pod", params.PodName,
		"namespace", params.Namespace)

	// Update metric entries with requests and limits
	for i := range metricEntries {
		// Filter metric to only include essential labels
		filteredMetric := map[string]any{}

		// Include only essential labels
		essentialLabels := []string{"container", "job", "pod", "namespace"}
		for _, label := range essentialLabels {
			if value, exists := metricEntries[i].Metric[label]; exists {
				filteredMetric[label] = value
			}
		}

		// Add requests and limits
		filteredMetric["requests"] = a.getResourceValues(requestsMap, metricEntries[i].Metric)
		filteredMetric["limits"] = a.getResourceValues(limitsMap, metricEntries[i].Metric)

		metricEntries[i].Metric = filteredMetric
	}

	// Generate insights based on missing requests and limits
	a.generateInsights(requestsMap, limitsMap, params.PodName, &insights)

	podMetric := podMetricData{
		Name:         "pod_metric",
		Data:         metricEntries,
		ResourceType: params.ResourceType,
	}

	return podMetric, insights
}

func (a *podMetricAction) extractMetricEntries(metricsData any) []podMetricEntry {
	entries := []podMetricEntry{}

	// Try to unmarshal to PrometheusQueryResult
	var prometheusResult PrometheusQueryResult
	if dataBytes, err := json.Marshal(metricsData); err == nil {
		if err := json.Unmarshal(dataBytes, &prometheusResult); err == nil {
			// Process SeriesListResult (for range queries)
			for _, series := range prometheusResult.SeriesListResult {
				entry := podMetricEntry(series)
				entries = append(entries, entry)
			}
		}
	}

	// Fallback to old parsing method if PrometheusQueryResult parsing fails
	if len(entries) == 0 {
		entries = a.extractMetricEntriesFromLegacyFormat(metricsData)
	}

	return entries
}

func (a *podMetricAction) extractMetricEntriesFromLegacyFormat(metricsData any) []podMetricEntry {
	entries := []podMetricEntry{}

	seriesData, ok := metricsData.(map[string]any)
	if !ok {
		return entries
	}

	seriesList, ok := seriesData["series_list_result"].([]any)
	if !ok {
		return entries
	}

	for _, series := range seriesList {
		if entry := a.parseSeriesEntry(series); entry != nil {
			entries = append(entries, *entry)
		}
	}

	return entries
}

func (a *podMetricAction) parseSeriesEntry(series any) *podMetricEntry {
	seriesMap, ok := series.(map[string]any)
	if !ok {
		return nil
	}

	entry := &podMetricEntry{
		Metric:     map[string]any{},
		Timestamps: []float64{},
		Values:     []string{},
	}

	// Extract metric labels
	if metric, ok := seriesMap["metric"].(map[string]any); ok {
		entry.Metric = metric
	}

	// Extract timestamps and values using helper functions
	entry.Timestamps = a.extractTimestamps(seriesMap["timestamps"])
	entry.Values = a.extractValues(seriesMap["values"])

	return entry
}

func (a *podMetricAction) extractTimestamps(timestampsData any) []float64 {
	timestamps := []float64{}

	timestampsArray, ok := timestampsData.([]any)
	if !ok {
		return timestamps
	}

	for _, ts := range timestampsArray {
		if timestamp, ok := ts.(float64); ok {
			timestamps = append(timestamps, timestamp)
		}
	}

	return timestamps
}

func (a *podMetricAction) extractValues(valuesData any) []string {
	values := []string{}

	valuesArray, ok := valuesData.([]any)
	if !ok {
		return values
	}

	for _, val := range valuesArray {
		if value, ok := val.(string); ok {
			values = append(values, value)
		}
	}

	return values
}

func (a *podMetricAction) extractResourceValues(resourceData any) map[string]map[string]float64 {
	resourceMap := make(map[string]map[string]float64)

	// Use direct type assertions for instant query results
	dataArray, ok := resourceData.([]any)
	if !ok {
		return resourceMap
	}

	for _, item := range dataArray {
		if resource := a.parseResourceItem(item); resource != nil {
			container := resource.Container
			resourceType := resource.ResourceType
			value := resource.Value

			if resourceMap[container] == nil {
				resourceMap[container] = make(map[string]float64)
			}
			resourceMap[container][resourceType] = value
		}
	}

	return resourceMap
}

type resourceItem struct {
	Container    string
	ResourceType string
	Value        float64
}

func (a *podMetricAction) parseResourceItem(item any) *resourceItem {
	itemMap, ok := item.(map[string]any)
	if !ok {
		return nil
	}

	// Extract metric labels
	metric, ok := itemMap["metric"].(map[string]any)
	if !ok {
		return nil
	}

	container, ok := metric["container"].(string)
	if !ok {
		return nil
	}

	resourceType, ok := metric["resource"].(string)
	if !ok {
		return nil
	}

	// Extract value from the Value array [timestamp, value_string]
	valueArray, ok := itemMap["value"].([]any)
	if !ok || len(valueArray) < 2 {
		return nil
	}

	valueStr, ok := valueArray[1].(string)
	if !ok {
		return nil
	}

	value, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return nil
	}

	return &resourceItem{
		Container:    container,
		ResourceType: resourceType,
		Value:        value,
	}
}

func (a *podMetricAction) getResourceValues(resourceMap map[string]map[string]float64, metric map[string]any) map[string]any {
	result := map[string]any{
		"cpu":    0,
		"memory": 0,
	}

	container := ""
	if c, ok := metric["container"].(string); ok {
		container = c
	}

	if containerResources, ok := resourceMap[container]; ok {
		if cpu, ok := containerResources["cpu"]; ok {
			result["cpu"] = cpu
		}
		if memory, ok := containerResources["memory"]; ok {
			result["memory"] = memory
		}
	}

	return result
}

func (a *podMetricAction) generateInsights(requestsMap, limitsMap map[string]map[string]float64, podName string, insights *[]PlaybookActionResponseInsight) {
	// Track if we found any requests or limits
	hasMemoryRequest := false
	hasMemoryLimit := false
	hasCpuRequest := false

	// Check all containers for requests and limits
	for container, resources := range requestsMap {
		if container == "" {
			continue
		}
		if _, ok := resources["memory"]; ok {
			hasMemoryRequest = true
		}
		if _, ok := resources["cpu"]; ok {
			hasCpuRequest = true
		}
	}

	for container, resources := range limitsMap {
		if container == "" {
			continue
		}
		if _, ok := resources["memory"]; ok {
			hasMemoryLimit = true
		}
	}

	// Generate insights based on missing requests and limits
	if !hasMemoryLimit {
		*insights = append(*insights, PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Pod %s does not have memory limit specified", podName),
			Severity: "High",
		})
	}
	if !hasMemoryRequest {
		*insights = append(*insights, PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Pod %s does not have memory request specified", podName),
			Severity: "Critical",
		})
	}
	if !hasCpuRequest {
		*insights = append(*insights, PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Pod %s does not have CPU request specified", podName),
			Severity: "High",
		})
	}
	// Note: CPU limits are intentionally not checked as they can cause throttling and are often omitted as best practice
}
