package playbooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"nudgebee/services/common"
	"nudgebee/services/relay"
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
	// Accept either a workload label on the event (Robusta-style Findings
	// carry labels.deployment/statefulset/daemonset/pod) OR the K8s subject
	// metadata the Go agent populates (subject_type=pod, with the owner
	// workload in subject_owner). SubjectName alone is not enough — cloud
	// events (e.g. AWS_EventBridge) carry an instance ID there.
	if labels != nil && (labels["deployment"] != "" || labels["statefulset"] != "" ||
		labels["daemonset"] != "" || labels["pod"] != "") {
		return true
	}
	return strings.EqualFold(event.SubjectType, "pod") && (event.SubjectOwner != "" || event.SubjectName != "")
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
	// Go-agent findings don't set workload labels — fall back to SubjectOwner
	// (the resolved owner of the Pod, e.g. the StatefulSet name) so the
	// regex query catches every Pod backed by the owner. Then to SubjectName.
	if podName == "" {
		podName = ctx.GetEvent().SubjectOwner
	}
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

	// Set defaults. Capture the user's original ResourceType BEFORE the
	// CPU fallback — generateInsights uses the user's intent (empty means
	// "no scope, emit all three"), while the prometheus query path
	// downstream needs a concrete resource type to build the query, so
	// it consumes the post-default value via params.ResourceType.
	userResourceType := params.ResourceType
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

	// Use regex match for workload names (Deployment, StatefulSet, DaemonSet,
	// ReplicaSet, Job, ReplicationController) since their pod names have a
	// random suffix appended by the workload controller. StatefulSet was
	// previously missing here and broke OOMKilled investigations for any
	// StatefulSet-owned pod (e.g. vmsingle-*): the workload-name exact match
	// `pod="vmsingle-victoria-..."` finds zero series and the action returns
	// false-negative "Pod X does not have memory limit specified" insights.
	//
	// We check both the "kind" label (set by matchWorkloadAndEnrich) and the
	// per-workload labels (deployment / statefulset / daemonset / job) which
	// are present on alerts like KubeDeploymentRolloutStuck where the "kind"
	// label is unset.
	podSelector := fmt.Sprintf(`pod="%s"`, params.PodName)
	kind := ""
	if ctx.GetEvent().Labels != nil {
		kind = ctx.GetEvent().Labels["kind"]
	}
	if kind == "" {
		kind = ctx.GetEvent().SubjectType
	}
	useRegex := strings.EqualFold(kind, "deployment") ||
		strings.EqualFold(kind, "daemonset") ||
		strings.EqualFold(kind, "replicaset") ||
		strings.EqualFold(kind, "statefulset") ||
		strings.EqualFold(kind, "job") ||
		strings.EqualFold(kind, "replicationcontroller")
	if !useRegex && ctx.GetEvent().Labels != nil {
		useRegex = ctx.GetEvent().Labels["deployment"] != "" ||
			ctx.GetEvent().Labels["statefulset"] != "" ||
			ctx.GetEvent().Labels["daemonset"] != "" ||
			ctx.GetEvent().Labels["job"] != "" ||
			ctx.GetEvent().Labels["replicaset"] != ""
	}
	// Implicit workload-name signal: AutoExecute resolves `pod_name` to
	// `SubjectOwner` (the owner workload, e.g. the StatefulSet name) when
	// no per-workload label is present on the event. Go-agent findings for
	// `pod_oom_killer_enricher` follow this path. SubjectName then holds
	// the actual pod (`<workload>-<hash>-<suffix>`), so a divergence
	// between params.PodName and SubjectName is a reliable signal that we
	// need regex matching, regardless of how SubjectOwnerKind is labelled.
	if !useRegex && params.PodName != "" && ctx.GetEvent().SubjectName != "" &&
		params.PodName != ctx.GetEvent().SubjectName &&
		strings.HasPrefix(ctx.GetEvent().SubjectName, params.PodName+"-") {
		useRegex = true
	}
	if useRegex {
		podSelector = fmt.Sprintf(`pod=~"%s-.*"`, params.PodName)
	}

	// Main metric query (CPU or Memory usage).
	//
	// cAdvisor emits three flavours of `container_*` series per pod:
	//   (a) per-container: container=<name>  — the actual app series
	//   (b) the sandbox / pause container: container="POD"
	//   (c) the pod-level aggregate (sum of containers): container=""
	// (b) and (c) end up as extra noise rows that the UI's
	// MemoryAllocationCard renders as separate panels — producing the
	// "two Pod memory charts where one is empty" symptom on the
	// investigate page. Match Robusta's enricher and the noisy_neighbours
	// fix by requiring a non-empty, non-POD container label.
	containerFilter := `container!="", container!="POD"`
	var metricQuery string
	if strings.ToUpper(params.ResourceType) == "CPU" {
		metricQuery = replaceClusterLabel(fmt.Sprintf(`rate(container_cpu_usage_seconds_total{__CLUSTER__ %s, %s, namespace="%s"}[5m])`, podSelector, containerFilter, params.Namespace))
	} else {
		metricQuery = replaceClusterLabel(fmt.Sprintf(`container_memory_rss{__CLUSTER__ %s, %s, namespace="%s"}`, podSelector, containerFilter, params.Namespace))
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
	podMetric, insights := a.buildPodMetricResponse(ctx, prometheusData, params, userResourceType)

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

// buildPodMetricResponse builds the response payload and generates
// resource-allocation insights. insightResourceType controls insight scoping
// — see generateInsights for the semantics. The caller passes the user's
// ORIGINAL resource_type (pre-default), which preserves "emit all three"
// for callers that didn't ask for per-resource scoping.
func (a *podMetricAction) buildPodMetricResponse(ctx PlaybookActionContext, prometheusData map[string]any, params podMetricEnricherParams, insightResourceType string) (podMetricData, []PlaybookActionResponseInsight) {
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

	// When the main metric query returned no series — typical for an
	// OOMKilled pod whose container is no longer running — but
	// kube_pod_container_resource_{requests,limits} did return data,
	// seed an entry per container so the UI can still render the
	// "Resource allocation" card and so the requests/limits-driven
	// insights actually surface. Legacy Robusta read requests/limits
	// straight off the K8s pod spec (oom_killer.py via
	// pod.spec.containers[].resources) for exactly this case.
	if len(metricEntries) == 0 && (len(requestsMap) > 0 || len(limitsMap) > 0) {
		containers := map[string]bool{}
		for c := range requestsMap {
			containers[c] = true
		}
		for c := range limitsMap {
			containers[c] = true
		}
		for container := range containers {
			metric := map[string]any{
				"container": container,
				"pod":       params.PodName,
				"namespace": params.Namespace,
			}
			metric["requests"] = a.getResourceValues(requestsMap, metric)
			metric["limits"] = a.getResourceValues(limitsMap, metric)
			metricEntries = append(metricEntries, podMetricEntry{
				Metric:     metric,
				Timestamps: []float64{},
				Values:     []string{},
			})
		}
	}

	// Scope insight emission to the resource_type being collected. Both
	// the cpu and memory enricher invocations call generateInsights — if
	// each emits all three (memory_limit / memory_request / cpu_request),
	// the user sees 6 insights instead of 3. Scoping each call to its own
	// resource_type keeps the union at exactly three unique insights.
	//
	// Pass the user's ORIGINAL resource_type (pre-default) so callers that
	// drive Execute manually without specifying it keep the legacy
	// "emit all three" behaviour. The defaulted value above is only used
	// by the prometheus query builder, which requires a concrete type.
	a.generateInsights(requestsMap, limitsMap, params.PodName, insightResourceType, &insights)

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

	// Relay's prometheus_queries_enricher wraps instant results as
	// `{result_type, vector_result: [{metric, value}, ...]}`. The
	// older code path here asserted `resourceData.([]any)` and silently
	// returned an empty map for every Stage-2.2 invocation, so
	// requestsMap / limitsMap were always empty and the
	// "X pods does not have a memory limit" insights never fired.
	// vectorResultEntries already handles the wrapping + both value
	// shapes (Robusta-coerced object and standard Prometheus tuple).
	for _, entry := range vectorResultEntries(resourceData) {
		container, _ := entry.metric["container"].(string)
		resourceType, _ := entry.metric["resource"].(string)
		if container == "" || resourceType == "" {
			continue
		}
		if resourceMap[container] == nil {
			resourceMap[container] = make(map[string]float64)
		}
		resourceMap[container][resourceType] = entry.value
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

	// The relay's prometheus_queries_enricher emits the Robusta-coerced
	// shape `{"timestamp": <float>, "value": "<str>"}`; older code paths
	// may still emit the standard `[ts, "v"]` tuple. parseInstantValue
	// accepts both — see action_noisy_neighbours.go.
	value, ok := parseInstantValue(itemMap["value"])
	if !ok {
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

// generateInsights emits resource-allocation insights scoped by resourceType
// so that the cpu and memory enricher invocations together produce the union
// of three unique insights (memory_limit, memory_request, cpu_request) — never
// six. Per-call emission:
//   - resourceType="memory" → up to 2 insights (memory_limit, memory_request)
//   - resourceType="cpu"    → up to 1 insight  (cpu_request)
//   - resourceType=""       → up to 3 insights (legacy / direct Execute callers
//     that haven't opted into per-resource scoping)
//
// "Up to" because each insight only fires when the corresponding resource is
// unset on the pod; a fully-specified pod emits zero insights regardless of
// resourceType.
func (a *podMetricAction) generateInsights(requestsMap, limitsMap map[string]map[string]float64, podName, resourceType string, insights *[]PlaybookActionResponseInsight) {
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

	rt := strings.ToLower(resourceType)
	emitMemory := rt == "" || rt == "memory"
	emitCPU := rt == "" || rt == "cpu"

	// Generate insights based on missing requests and limits
	if emitMemory && !hasMemoryLimit {
		*insights = append(*insights, PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Pod %s does not have memory limit specified", podName),
			Severity: "High",
		})
	}
	if emitMemory && !hasMemoryRequest {
		*insights = append(*insights, PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Pod %s does not have memory request specified", podName),
			Severity: "Critical",
		})
	}
	if emitCPU && !hasCpuRequest {
		*insights = append(*insights, PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Pod %s does not have CPU request specified", podName),
			Severity: "High",
		})
	}
	// Note: CPU limits are intentionally not checked as they can cause throttling and are often omitted as best practice
}
