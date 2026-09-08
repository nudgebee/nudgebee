package flow_sources

import (
	"fmt"
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/observability"
	"nudgebee/services/security"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MapIPsToPods maps a list of IP addresses to Kubernetes pods
// This is extracted from EnrichLoadBalancerWithTargets for reuse
func MapIPsToPods(
	reqCtx *security.RequestContext,
	ips []string,
	k8sAccountID string,
	tenantID string,
	sourceName string,
	existingNodes []*core.DbNode,
) ([]*core.DbNode, []*core.DbEdge, error) {

	podNodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)

	if len(ips) == 0 {
		return podNodes, edges, nil
	}

	// Build a map of existing Service nodes for quick lookup
	serviceNodeMap := make(map[string]*core.DbNode)
	for _, node := range existingNodes {
		if node.NodeType == core.NodeTypeService {
			name, _ := node.Properties["name"].(string)
			namespace, _ := node.Properties["namespace"].(string)
			cluster, _ := node.Properties["cluster"].(string)
			if name != "" {
				key := fmt.Sprintf("%s:%s:%s", name, namespace, cluster)
				serviceNodeMap[key] = node
			}
		}
	}

	// Query kube_pod_info to map IPs to pod names
	ipFilter := strings.Join(ips, "|")
	queries := map[string]string{
		"pod_info": fmt.Sprintf(`kube_pod_info{pod_ip=~"%s"}`, ipFilter),
	}

	endTime := time.Now().UTC()
	startTime := endTime.Add(-5 * time.Minute)

	podInfoResp, err := observability.FetchMetricsQuery(reqCtx, observability.FetchMetricsRequest{
		AccountId:      k8sAccountID,
		MetricProvider: "prometheus",
		Queries:        queries,
		StartTime:      startTime.UnixMilli(),
		EndTime:        endTime.UnixMilli(),
		Instant:        true,
	})
	if err != nil {
		slog.Warn("Failed to query kube_pod_info for IPs",
			"source", sourceName,
			"error", err)
		return podNodes, edges, nil
	}

	// Parse pod info response
	podMetrics := observability.PromQLLabels(podInfoResp, "pod_info")

	if len(podMetrics) == 0 {
		slog.Debug("No pod info results found for IPs",
			"source", sourceName,
			"ips", len(ips))
		return podNodes, edges, nil
	}

	// Collect ReplicaSets to query for owners
	replicaSetsToQuery := make(map[string]bool)
	for _, metric := range podMetrics {
		createdByKind := metric["created_by_kind"]
		createdByName := metric["created_by_name"]
		namespace := metric["namespace"]

		if createdByKind == "ReplicaSet" && createdByName != "" && namespace != "" {
			replicaSetsToQuery[fmt.Sprintf("%s/%s", namespace, createdByName)] = true
		}
	}

	// Query kube_replicaset_owner to get Deployment owners
	replicaSetOwners := make(map[string]map[string]string)
	if len(replicaSetsToQuery) > 0 {
		// Build focused query with specific ReplicaSets
		// Group by namespace to create efficient regex filters
		namespaceReplicaSets := make(map[string][]string)
		for nsRS := range replicaSetsToQuery {
			parts := strings.SplitN(nsRS, "/", 2)
			if len(parts) == 2 {
				namespace := parts[0]
				rsName := parts[1]
				namespaceReplicaSets[namespace] = append(namespaceReplicaSets[namespace], rsName)
			}
		}

		// Build regex for ReplicaSet names per namespace
		var queryParts []string
		for namespace, rsNames := range namespaceReplicaSets {
			if len(rsNames) == 1 {
				queryParts = append(queryParts, fmt.Sprintf(`kube_replicaset_owner{namespace="%s",replicaset="%s"}`, namespace, rsNames[0]))
			} else {
				// Use regex for multiple ReplicaSets in same namespace
				rsRegex := strings.Join(rsNames, "|")
				queryParts = append(queryParts, fmt.Sprintf(`kube_replicaset_owner{namespace="%s",replicaset=~"%s"}`, namespace, rsRegex))
			}
		}

		// Combine all namespace queries with OR
		rsQuery := strings.Join(queryParts, " or ")
		rsQueries := map[string]string{
			"rs_owner": rsQuery,
		}

		slog.Info("Querying ReplicaSet owners",
			"replicasets_count", len(replicaSetsToQuery),
			"query", rsQuery)

		rsResp, err := observability.FetchMetricsQuery(reqCtx, observability.FetchMetricsRequest{
			AccountId:      k8sAccountID,
			MetricProvider: "prometheus",
			Queries:        rsQueries,
			StartTime:      startTime.UnixMilli(),
			EndTime:        endTime.UnixMilli(),
			Instant:        true,
		})
		if err == nil {
			for _, metric := range observability.PromQLLabels(rsResp, "rs_owner") {
				rsNamespace := metric["namespace"]
				rsName := metric["replicaset"]
				ownerKind := metric["owner_kind"]
				ownerName := metric["owner_name"]

				if rsNamespace != "" && rsName != "" {
					key := fmt.Sprintf("%s/%s", rsNamespace, rsName)
					replicaSetOwners[key] = map[string]string{
						"kind": ownerKind,
						"name": ownerName,
					}
				}
			}
		}
	}

	// Create pod nodes and edges
	for _, metric := range podMetrics {
		podName := metric["pod"]
		namespace := metric["namespace"]
		podIP := metric["pod_ip"]
		createdByKind := metric["created_by_kind"]
		createdByName := metric["created_by_name"]

		if podName == "" || podIP == "" {
			continue
		}

		// Determine the actual owner (might be Deployment via ReplicaSet)
		ownerKind := createdByKind
		ownerName := createdByName

		if createdByKind == "ReplicaSet" {
			rsKey := fmt.Sprintf("%s/%s", namespace, createdByName)
			if owner, exists := replicaSetOwners[rsKey]; exists {
				if owner["kind"] != "" {
					ownerKind = owner["kind"]
					ownerName = owner["name"]
				}
			} else {
				ownerName = core.ExtractDeploymentFromReplicaSet(createdByName)
				ownerKind = "Deployment"
			}
		}

		// Create node for the pod's owner (Deployment, StatefulSet, etc.)
		// Use NodeTypePod and store owner kind in properties
		// Preserve all metric labels from Prometheus first
		labels := make(map[string]string)
		for k, v := range metric {
			labels[k] = v
		}

		// Build properties with standard fields extracted from labels
		properties := map[string]interface{}{
			"name":       ownerName,
			"namespace":  namespace,
			"owner_kind": ownerKind,
			"pod_name":   podName,
			"pod_ip":     podIP,
		}

		// Extract commonly-used K8s fields from labels to top-level for easy access
		// This standardizes access pattern: check top-level first, fall back to labels
		if k8sCluster, ok := labels["k8s_cluster"]; ok && k8sCluster != "" {
			properties["k8s_cluster"] = k8sCluster
		}

		if node, ok := labels["node"]; ok && node != "" {
			properties["node"] = node
		}

		if hostIP, ok := labels["host_ip"]; ok && hostIP != "" {
			properties["host_ip"] = hostIP
		}

		// Store all labels for full context
		if len(labels) > 0 {
			properties["labels"] = labels
		}

		uniqueKey := fmt.Sprintf("%s:%s:%s", ownerKind, ownerName, namespace)
		targetNode := &core.DbNode{
			ID:             core.NodeIDFor(uniqueKey, tenantID, k8sAccountID),
			UniqueKey:      uniqueKey,
			NodeType:       core.NodeTypePod,
			CloudAccountID: k8sAccountID,
			TenantID:       tenantID,
			Properties:     properties,
			Labels:         labels,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		podNodes = append(podNodes, targetNode)

		// Try to find matching Service node to create edge to
		var targetServiceNode *core.DbNode
		for key, svcNode := range serviceNodeMap {
			svcNamespace, _ := svcNode.Properties["namespace"].(string)
			if svcNamespace == namespace {
				targetServiceNode = svcNode
				slog.Debug("Matched pod to service",
					"pod", podName,
					"service", key)
				break
			}
		}

		// Create edge from source (DNS hostname) to target (Service or Pod)
		if targetServiceNode != nil {
			edge := &core.DbEdge{
				ID:                uuid.New().String(),
				SourceNodeID:      sourceName, // This should be a node ID, not a name
				DestinationNodeID: targetServiceNode.ID,
				RelationshipType:  core.RelationshipResolvesTo,
				TenantID:          tenantID,
				CloudAccountID:    k8sAccountID,
				Properties: map[string]interface{}{
					"discovered_from": "route53_dns",
					"pod_ip":          podIP,
					"pod_name":        podName,
				},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			edges = append(edges, edge)
		}
	}

	return podNodes, edges, nil
}
