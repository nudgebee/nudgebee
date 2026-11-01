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
		switch UtilisationKey(metricKey) {
		case KeyCPUUsage:
			queries[metricKey] = fmt.Sprintf(
				"SELECT average(cpuUsedCores) FROM K8sNodeSample WHERE %s FACET nodeName",
				nodeFilter)
		case KeyMemoryUsage:
			queries[metricKey] = fmt.Sprintf(
				"SELECT average(memoryUsedBytes) FROM K8sNodeSample WHERE %s FACET nodeName",
				nodeFilter)
		case KeyCPURequest:
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(cpuRequestedCores) FROM K8sContainerSample WHERE %s FACET nodeName",
				nodeFilter)
		case KeyCPULimit:
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(cpuLimitCores) FROM K8sContainerSample WHERE %s FACET nodeName",
				nodeFilter)
		case KeyMemoryRequest:
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(memoryRequestedBytes) FROM K8sContainerSample WHERE %s FACET nodeName",
				nodeFilter)
		case KeyMemoryLimit:
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(memoryLimitBytes) FROM K8sContainerSample WHERE %s FACET nodeName",
				nodeFilter)
		case KeyDiskTotal:
			queries[metricKey] = fmt.Sprintf(
				"SELECT latest(fsCapacityBytes) FROM K8sNodeSample WHERE %s FACET nodeName",
				nodeFilter)
		case KeyDiskUsed:
			queries[metricKey] = fmt.Sprintf(
				"SELECT latest(fsUsedBytes) FROM K8sNodeSample WHERE %s FACET nodeName",
				nodeFilter)
		case KeyCPUAllocatable:
			queries[metricKey] = fmt.Sprintf(
				"SELECT latest(allocatableCpuCores) FROM K8sNodeSample WHERE %s FACET nodeName",
				nodeFilter)
		case KeyMemoryAllocatable:
			queries[metricKey] = fmt.Sprintf(
				"SELECT latest(allocatableMemoryBytes) FROM K8sNodeSample WHERE %s FACET nodeName",
				nodeFilter)
		case KeyCPUUsageLine:
			queries[metricKey] = fmt.Sprintf(
				"SELECT average(cpuUsedCores) FROM K8sNodeSample WHERE %s FACET nodeName",
				nodeFilter)
		case KeyMemoryUsageLine:
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
		switch UtilisationKey(metricKey) {
		case KeyCPUReal:
			queries[metricKey] = "SELECT sum(cpuUsedCores) FROM K8sNodeSample"
		case KeyCPUTotal:
			queries[metricKey] = "SELECT sum(capacityCpuCores) FROM K8sNodeSample"
		case KeyMemReal:
			queries[metricKey] = "SELECT sum(memoryUsedBytes) FROM K8sNodeSample"
		case KeyMemTotal:
			queries[metricKey] = "SELECT sum(capacityMemoryBytes) FROM K8sNodeSample"
		case KeyP90CPU:
			queries[metricKey] = "SELECT percentile(cpuUsedCores, 90) FROM K8sNodeSample"
		case KeyP50CPU:
			queries[metricKey] = "SELECT percentile(cpuUsedCores, 50) FROM K8sNodeSample"
		case KeyP90Mem:
			queries[metricKey] = "SELECT percentile(memoryUsedBytes, 90) FROM K8sNodeSample"
		case KeyP50Mem:
			queries[metricKey] = "SELECT percentile(memoryUsedBytes, 50) FROM K8sNodeSample"
		case KeyMaxUsageCPU:
			queries[metricKey] = "SELECT max(cpuUsedCores) FROM K8sNodeSample"
		case KeyMaxUsageMem:
			queries[metricKey] = "SELECT max(memoryUsedBytes) FROM K8sNodeSample"
		// --- Cluster-wide Container Resource Aggregations (no workload filter) ---
		case KeyCPURequest:
			queries[metricKey] = "SELECT sum(cpuRequestedCores) FROM K8sContainerSample"
		case KeyCPULimit:
			queries[metricKey] = "SELECT sum(cpuLimitCores) FROM K8sContainerSample"
		case KeyMemoryRequest:
			queries[metricKey] = "SELECT sum(memoryRequestedBytes) FROM K8sContainerSample"
		case KeyMemoryLimit:
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
		switch UtilisationKey(metricKey) {
		// --- Resource Metrics from K8sContainerSample ---
		case KeyCPUUsage:
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(cpuUsedCores) FROM K8sContainerSample WHERE %s %s",
				whereClause, facetClause)
		case KeyCPURequest:
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(cpuRequestedCores) FROM K8sContainerSample WHERE %s %s",
				whereClause, facetClause)
		case KeyCPULimit:
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(cpuLimitCores) FROM K8sContainerSample WHERE %s %s",
				whereClause, facetClause)
		case KeyMemoryUsage:
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(memoryWorkingSetBytes) FROM K8sContainerSample WHERE %s %s",
				whereClause, facetClause)
		case KeyMemoryRequest:
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(memoryRequestedBytes) FROM K8sContainerSample WHERE %s %s",
				whereClause, facetClause)
		case KeyMemoryLimit:
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(memoryLimitBytes) FROM K8sContainerSample WHERE %s %s",
				whereClause, facetClause)

		// --- HTTP/APM Metrics from Transaction events ---
		case KeyHTTPStatus:
			queries[metricKey] = fmt.Sprintf(
				"SELECT count(*) FROM Transaction WHERE %s FACET httpResponseCode",
				whereClause)
		case KeyHTTPThroughput:
			queries[metricKey] = fmt.Sprintf(
				"SELECT rate(count(*), 1 minute) FROM Transaction WHERE %s %s",
				whereClause, facetClause)
		case KeyHTTPLatencyP95:
			queries[metricKey] = fmt.Sprintf(
				"SELECT percentile(duration, 95) FROM Transaction WHERE %s %s",
				whereClause, facetClause)
		case KeyHTTPLatencyP99, KeyWorkloadHTTPLatencyP99:
			queries[metricKey] = fmt.Sprintf(
				"SELECT percentile(duration, 99) FROM Transaction WHERE %s %s",
				whereClause, facetClause)
		case KeyHTTPLatencySum:
			queries[metricKey] = fmt.Sprintf(
				"SELECT sum(duration) FROM Transaction WHERE %s %s",
				whereClause, facetClause)
		case KeyHTTPMaxResponseTime:
			queries[metricKey] = fmt.Sprintf(
				"SELECT max(duration) FROM Transaction WHERE %s %s",
				whereClause, facetClause)
		case KeyHTTPErrorRate:
			queries[metricKey] = fmt.Sprintf(
				"SELECT percentage(count(*), WHERE error IS true) FROM Transaction WHERE %s %s",
				whereClause, facetClause)

		// --- PVC/Volume Metrics from K8sVolumeSample ---
		case KeyPVCUsage:
			if pvcWhereClause != "" {
				queries[metricKey] = fmt.Sprintf(
					"SELECT sum(fsUsedBytes) FROM K8sVolumeSample WHERE %s FACET pvcName",
					pvcWhereClause)
			}
		case KeyPVCRequests:
			if pvcWhereClause != "" {
				queries[metricKey] = fmt.Sprintf(
					"SELECT sum(fsCapacityBytes) FROM K8sVolumeSample WHERE %s FACET pvcName",
					pvcWhereClause)
			}
		}
	}
	return queries
}
