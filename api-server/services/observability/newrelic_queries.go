package observability

import (
	"fmt"
	"strings"
)

// New Relic utilisation query builders. These render abstract metric keys into
// NRQL for node- and workload-scoped utilisation, and are dispatched from
// FetchMetricUtilisation (see service.go). Kept in their own file mirroring the
// per-provider layout (datadog_queries.go, solarwinds_queries.go, dynatrace_queries.go).

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
