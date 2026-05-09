package sources

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/flow_sources"
	"nudgebee/services/relay"
	"nudgebee/services/security"
)

// Provenance values stamped on every edge this enricher emits. The source
// name matches the priority-map key in flow_sources/edge_priority.go so log
// and metric correlation pivot on a single canonical name.
const (
	awsLBPodTargetSourceName = "aws_lb_pod_target"
	awsLBPodTargetCategory   = "cross_source"
)

func init() {
	RegisterCrossSourceEnricherFactory(
		awsLBPodTargetSourceName,
		func(logger *slog.Logger) core.CrossSourceEnricherInterface {
			return NewLoadBalancerPodTargetEnricher(logger)
		},
		"Enriches AWS LoadBalancer nodes with backend pod targets via target-group + Prometheus lookup",
	)
}

// LoadBalancerPodTargetEnricher resolves a LoadBalancer's AWS target-group
// targets to K8s pods (or Service / Deployment owners) and emits ROUTES_TO
// edges. Lifted as-is from the now-deleted CloudEnricher path; lives here as
// a Phase-2.1 cross-source enricher so it has a registered home.
type LoadBalancerPodTargetEnricher struct {
	logger *slog.Logger
}

// NewLoadBalancerPodTargetEnricher constructs the enricher.
func NewLoadBalancerPodTargetEnricher(logger *slog.Logger) *LoadBalancerPodTargetEnricher {
	if logger == nil {
		logger = slog.Default()
	}
	return &LoadBalancerPodTargetEnricher{logger: logger}
}

// GetName implements core.CrossSourceEnricherInterface.
func (e *LoadBalancerPodTargetEnricher) GetName() string {
	return awsLBPodTargetSourceName
}

// EnrichCrossSources walks the unified graph for LoadBalancer nodes emitted
// by aws_source, derives the K8s account that maps to each LB's AWS account,
// and dispatches per-LB to enrichLoadBalancerWithTargets. Iteration is serial
// — the lifted code makes blocking AWS-CLI and Prometheus calls per LB; the
// perf concern (holding the per-tenant build lock through these calls) is
// tracked as a follow-up rather than addressed here.
func (e *LoadBalancerPodTargetEnricher) EnrichCrossSources(
	reqCtx *security.RequestContext,
	allNodes []*core.DbNode,
	allEdges []*core.DbEdge,
	tenantID string,
) ([]*core.DbNode, []*core.DbEdge, error) {

	lbNodes := make([]*core.DbNode, 0)
	for _, n := range allNodes {
		if n != nil && n.Source == "aws" && n.NodeType == core.NodeTypeLoadBalancer {
			lbNodes = append(lbNodes, n)
		}
	}
	if len(lbNodes) == 0 {
		e.logger.Debug("no LoadBalancer nodes; skipping aws_lb_pod_target enrichment")
		return allNodes, allEdges, nil
	}

	// Derive AWS→K8s account mapping once per tenant. First-K8s-account-wins
	// on collision matches the original behaviour where the caller passed a
	// single k8sAccountID.
	k8sToAws, err := core.GetK8sCloudAccountMapping(reqCtx, tenantID, "AWS")
	if err != nil {
		e.logger.Warn("Failed to get K8s↔AWS account mapping; falling back to empty map",
			"tenant_id", tenantID, "error", err)
		k8sToAws = make(map[string][]string)
	}
	awsToK8s := make(map[string]string, len(k8sToAws))
	for k8sAcct, awsAccts := range k8sToAws {
		for _, awsAcct := range awsAccts {
			if _, exists := awsToK8s[awsAcct]; !exists {
				awsToK8s[awsAcct] = k8sAcct
			}
		}
	}

	e.logger.Info("Starting LoadBalancer pod-target enrichment",
		"lb_count", len(lbNodes),
		"aws_to_k8s_mappings", len(awsToK8s))

	newNodes := make([]*core.DbNode, 0)
	newEdges := make([]*core.DbEdge, 0)
	for _, lbNode := range lbNodes {
		awsAccountID, _ := lbNode.Properties["aws_account_id"].(string)
		if awsAccountID == "" {
			awsAccountID = lbNode.CloudAccountID
		}
		if awsAccountID == "" {
			e.logger.Debug("LoadBalancer missing aws_account_id; skipping",
				"lb_name", lbNode.Properties["name"])
			continue
		}
		k8sAccountID, hasK8s := awsToK8s[awsAccountID]
		if !hasK8s {
			e.logger.Debug("No K8s account mapped to LB's AWS account; skipping",
				"lb_name", lbNode.Properties["name"],
				"aws_account", awsAccountID)
			continue
		}

		podNodes, edges, err := e.enrichLoadBalancerWithTargets(reqCtx, lbNode, allNodes, awsAccountID, k8sAccountID, tenantID)
		if err != nil {
			e.logger.Warn("Failed to enrich LoadBalancer with pod targets",
				"lb_name", lbNode.Properties["name"],
				"error", err)
			continue
		}
		newNodes = append(newNodes, podNodes...)
		newEdges = append(newEdges, edges...)
	}

	e.logger.Info("LoadBalancer pod-target enrichment completed",
		"lb_count", len(lbNodes),
		"new_pod_nodes", len(newNodes),
		"new_edges", len(newEdges))

	return append(allNodes, newNodes...), append(allEdges, newEdges...), nil
}

// enrichLoadBalancerWithTargets enriches a single LoadBalancer node with its
// backend pod/service targets. Lifted verbatim from CloudEnricher.enrichLoadBalancerWithTargets
// with three localised changes:
//  1. Receiver renamed *CloudEnricher -> *LoadBalancerPodTargetEnricher.
//  2. ResolveIngressBackendServices call qualified to flow_sources.* (cross-package).
//  3. Both edge construction sites stamp created_by_flow_source / flow_source_category /
//     source_priority so log + metric correlation has a canonical source name.
func (e *LoadBalancerPodTargetEnricher) enrichLoadBalancerWithTargets(
	reqCtx *security.RequestContext,
	lbNode *core.DbNode,
	existingNodes []*core.DbNode,
	awsAccountID, k8sAccountID, tenantID string,
) ([]*core.DbNode, []*core.DbEdge, error) {

	podNodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)

	// Build a map of existing Service nodes for quick lookup by name+namespace+cluster
	serviceNodeMap := make(map[string]*core.DbNode)
	// Build a map to track pod owner nodes (Deployment/StatefulSet/DaemonSet)
	ownerNodeMap := make(map[string]*core.DbNode)
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

	// Extract LoadBalancer ARN and region from properties
	arn, arnOk := lbNode.Properties["arn"].(string)
	region, regionOk := lbNode.Properties["region"].(string)

	if !arnOk || !regionOk || arn == "" || region == "" {
		e.logger.Debug("Skipping LoadBalancer enrichment: missing ARN or region",
			"lb_name", lbNode.Properties["name"])
		return podNodes, edges, nil
	}

	// Step 1: Query AWS for target groups
	tgCommand := fmt.Sprintf(
		"aws elbv2 describe-target-groups --region %s --load-balancer-arn %s --output json",
		region, arn,
	)

	tgResp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   tgCommand,
	})
	if err != nil {
		e.logger.Warn("Failed to query LoadBalancer target groups",
			"lb_name", lbNode.Properties["name"],
			"error", err)
		return podNodes, edges, nil
	}

	// Parse target groups
	var targetGroups []map[string]interface{}
	if data, ok := tgResp["data"].(string); ok {
		var tgData struct {
			TargetGroups []map[string]interface{} `json:"TargetGroups"`
		}
		if err := json.Unmarshal([]byte(data), &tgData); err != nil {
			e.logger.Warn("Failed to parse target groups", "error", err)
			return podNodes, edges, nil
		}
		targetGroups = tgData.TargetGroups
	}

	if len(targetGroups) == 0 {
		e.logger.Debug("No target groups found for LoadBalancer",
			"lb_name", lbNode.Properties["name"])
		return podNodes, edges, nil
	}

	// Step 1.5: Query LoadBalancer tags to check for Kubernetes service mapping
	tagsCommand := fmt.Sprintf(
		"aws elbv2 describe-tags --resource-arns %s --output json",
		arn,
	)

	tagsResp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   tagsCommand,
	})

	var k8sServiceName, k8sNamespace string
	if err == nil && tagsResp != nil {
		if data, ok := tagsResp["data"].(string); ok {
			var tagsData struct {
				TagDescriptions []struct {
					Tags []struct {
						Key   string `json:"Key"`
						Value string `json:"Value"`
					} `json:"Tags"`
				} `json:"TagDescriptions"`
			}
			if json.Unmarshal([]byte(data), &tagsData) == nil && len(tagsData.TagDescriptions) > 0 {
				for _, tag := range tagsData.TagDescriptions[0].Tags {
					if tag.Key == "kubernetes.io/service-name" {
						parts := strings.Split(tag.Value, "/")
						if len(parts) == 2 {
							k8sNamespace = parts[0]
							k8sServiceName = parts[1]
							e.logger.Info("Found Kubernetes service for LoadBalancer",
								"lb_name", lbNode.Properties["name"],
								"k8s_service", tag.Value)
						}
						break
					}
				}
			}
		}
	}

	// If this LB is for an ingress controller, create ingress node and skip pod mapping
	if k8sNamespace != "" && k8sServiceName != "" && strings.Contains(k8sServiceName, "ingress") {
		environment := "inferred"
		if lbEnv, ok := lbNode.Properties["environment"].(string); ok && lbEnv != "" {
			environment = lbEnv
		}

		ingressNode := &core.DbNode{
			ID:             uuid.New().String(),
			NodeType:       core.NodeTypeService,
			UniqueKey:      core.BuildUniqueKey(core.CloudProviderK8s, k8sAccountID, "", core.NodeTypeService, k8sNamespace, k8sServiceName),
			CloudAccountID: k8sAccountID,
			TenantID:       tenantID,
			Level:          "Tenant",
			Source:         "cloud",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
			Properties: map[string]interface{}{
				"name":         k8sServiceName,
				"namespace":    k8sNamespace,
				"environment":  environment,
				"type":         "nginx",
				"service.name": k8sServiceName,
			},
			Labels:          map[string]string{},
			QueryAttributes: map[string]interface{}{},
		}

		edge := &core.DbEdge{
			ID:                uuid.New().String(),
			SourceNodeID:      lbNode.ID,
			DestinationNodeID: ingressNode.ID,
			RelationshipType:  core.RelationshipRoutesTo,
			Properties: map[string]interface{}{
				"discovered_from":        "aws_lb_tags",
				"service_name":           fmt.Sprintf("%s/%s", k8sNamespace, k8sServiceName),
				"created_by_flow_source": awsLBPodTargetSourceName,
				"flow_source_category":   awsLBPodTargetCategory,
				"source_priority":        int(flow_sources.GetEdgeSourcePriority(awsLBPodTargetSourceName, core.RelationshipRoutesTo)),
			},
			CloudAccountID: k8sAccountID,
			TenantID:       tenantID,
			Level:          "Tenant",
			Source:         "cloud",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}

		e.logger.Info("Created ingress controller node for LoadBalancer",
			"lb_name", lbNode.Properties["name"],
			"ingress_service", k8sServiceName,
			"namespace", k8sNamespace)

		nodes := []*core.DbNode{ingressNode}
		resultEdges := []*core.DbEdge{edge}

		ingressBackendNodes, ingressBackendEdges, err := flow_sources.ResolveIngressBackendServices(reqCtx, k8sAccountID, tenantID, environment, ingressNode)
		if err != nil {
			e.logger.Warn("Failed to resolve Ingress backend services",
				"error", err,
				"ingress_service", k8sServiceName)
		} else if ingressBackendNodes != nil {
			nodes = append(nodes, ingressBackendNodes...)
			resultEdges = append(resultEdges, ingressBackendEdges...)
			e.logger.Info("Resolved Ingress backend services",
				"ingress_service", k8sServiceName,
				"backend_services_count", len(ingressBackendNodes))
		}

		return nodes, resultEdges, nil
	}

	// Step 2: Collect all target IPs and instance IDs from all target groups
	uniqueIPs := make(map[string]bool)
	instanceIDs := make(map[string]bool)

	for _, tg := range targetGroups {
		tgArn, ok := tg["TargetGroupArn"].(string)
		if !ok {
			continue
		}

		healthCommand := fmt.Sprintf(
			"aws elbv2 describe-target-health --region %s --target-group-arn %s --output json",
			region, tgArn,
		)

		healthResp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
			AccountID: awsAccountID,
			Command:   healthCommand,
		})
		if err != nil {
			e.logger.Warn("Failed to query target health", "target_group", tgArn, "error", err)
			continue
		}

		if data, ok := healthResp["data"].(string); ok {
			var healthData struct {
				TargetHealthDescriptions []map[string]interface{} `json:"TargetHealthDescriptions"`
			}
			if err := json.Unmarshal([]byte(data), &healthData); err != nil {
				continue
			}

			for _, target := range healthData.TargetHealthDescriptions {
				if targetInfo, ok := target["Target"].(map[string]interface{}); ok {
					if targetID, ok := targetInfo["Id"].(string); ok {
						if strings.HasPrefix(targetID, "i-") {
							instanceIDs[targetID] = true
						} else {
							uniqueIPs[targetID] = true
						}
					}
				}
			}
		}
	}

	// Step 2b: Resolve EC2 instance IDs to private IPs
	if len(instanceIDs) > 0 {
		e.logger.Info("Resolving EC2 instance IDs to private IPs",
			"lb_name", lbNode.Properties["name"],
			"instance_count", len(instanceIDs))

		instanceIDList := make([]string, 0, len(instanceIDs))
		for instanceID := range instanceIDs {
			instanceIDList = append(instanceIDList, instanceID)
		}
		instanceIDStr := strings.Join(instanceIDList, " ")

		ec2Command := fmt.Sprintf(
			"aws ec2 describe-instances --region %s --instance-ids %s --query 'Reservations[].Instances[].[InstanceId,PrivateIpAddress]' --output json",
			region, instanceIDStr,
		)

		ec2Resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
			AccountID: awsAccountID,
			Command:   ec2Command,
		})
		if err != nil {
			e.logger.Warn("Failed to query EC2 instances",
				"lb_name", lbNode.Properties["name"],
				"error", err)
		} else {
			if data, ok := ec2Resp["data"].(string); ok {
				var instances [][]string
				if err := json.Unmarshal([]byte(data), &instances); err == nil {
					for _, inst := range instances {
						if len(inst) == 2 {
							instanceID := inst[0]
							privateIP := inst[1]
							if privateIP != "" {
								uniqueIPs[privateIP] = true
								e.logger.Debug("Resolved EC2 instance to private IP",
									"instance_id", instanceID,
									"private_ip", privateIP,
									"lb_name", lbNode.Properties["name"])
							}
						}
					}
					e.logger.Info("EC2 instance resolution completed",
						"lb_name", lbNode.Properties["name"],
						"instances_resolved", len(instances),
						"total_ips", len(uniqueIPs))
				} else {
					e.logger.Warn("Failed to parse EC2 response", "error", err)
				}
			}
		}
	}

	if len(uniqueIPs) == 0 {
		e.logger.Info("No target IPs found for LoadBalancer",
			"lb_name", lbNode.Properties["name"],
			"instance_targets_attempted", len(instanceIDs))
		return podNodes, edges, nil
	}

	// Step 3: Query kube_pod_info to map IPs to pod names (using K8s account ID)
	ipList := make([]string, 0, len(uniqueIPs))
	for ip := range uniqueIPs {
		ipList = append(ipList, ip)
	}
	ipFilter := strings.Join(ipList, "|")

	queries := map[string]string{
		"pod_info": fmt.Sprintf(`kube_pod_info{pod_ip=~"%s"}`, ipFilter),
	}

	endTime := time.Now()
	startTime := endTime.Add(-5 * time.Minute)

	podInfoResp, err := relay.ExecutePrometheus(k8sAccountID, startTime, endTime, queries, true)
	if err != nil {
		e.logger.Warn("Failed to query kube_pod_info",
			"lb_name", lbNode.Properties["name"],
			"error", err)
		return podNodes, edges, nil
	}

	// Step 4: Create Pod nodes and edges from query results
	var resultArray []interface{}

	if podInfoData, ok := podInfoResp["pod_info"].([]interface{}); ok {
		resultArray = podInfoData
		e.logger.Debug("Found pod info with query name key", "count", len(podInfoData))
	} else if data, ok := podInfoResp["data"].([]interface{}); ok {
		resultArray = data
		e.logger.Debug("Found pod info in data array", "count", len(data))
	} else if data, ok := podInfoResp["data"].(map[string]interface{}); ok {
		if podInfoData, ok := data["pod_info"].(map[string]interface{}); ok {
			if result, ok := podInfoData["result"].([]interface{}); ok {
				resultArray = result
				e.logger.Debug("Found pod info in nested structure", "count", len(result))
			}
		}
	}

	if len(resultArray) == 0 {
		e.logger.Warn("No pod info results found in Prometheus response",
			"lb_name", lbNode.Properties["name"],
			"target_ips", len(uniqueIPs))
	}

	replicaSetsToQuery := make(map[string]bool)
	podMetrics := make([]map[string]interface{}, 0)

	for _, item := range resultArray {
		if pod, ok := item.(map[string]interface{}); ok {
			if metric, ok := pod["metric"].(map[string]interface{}); ok {
				podMetrics = append(podMetrics, metric)

				createdByKind, _ := metric["created_by_kind"].(string)
				createdByName, _ := metric["created_by_name"].(string)
				namespace, _ := metric["namespace"].(string)

				if createdByKind == "ReplicaSet" && createdByName != "" && namespace != "" {
					replicaSetsToQuery[fmt.Sprintf("%s/%s", namespace, createdByName)] = true
				}
			}
		}
	}

	// Step 5: Query kube_replicaset_owner to get Deployment owners for ReplicaSets
	replicaSetOwners := make(map[string]map[string]string)
	if len(replicaSetsToQuery) > 0 {
		rsQueries := map[string]string{
			"rs_owner": "kube_replicaset_owner",
		}

		rsResp, err := relay.ExecutePrometheus(k8sAccountID, startTime, endTime, rsQueries, true)
		if err == nil {
			var rsResultArray []interface{}
			if rsData, ok := rsResp["rs_owner"].([]interface{}); ok {
				rsResultArray = rsData
			} else if data, ok := rsResp["data"].([]interface{}); ok {
				rsResultArray = data
			} else if data, ok := rsResp["data"].(map[string]interface{}); ok {
				if rsData, ok := data["rs_owner"].(map[string]interface{}); ok {
					if result, ok := rsData["result"].([]interface{}); ok {
						rsResultArray = result
					}
				}
			}

			for _, item := range rsResultArray {
				if rs, ok := item.(map[string]interface{}); ok {
					if metric, ok := rs["metric"].(map[string]interface{}); ok {
						rsNamespace, _ := metric["namespace"].(string)
						rsName, _ := metric["replicaset"].(string)
						ownerKind, _ := metric["owner_kind"].(string)
						ownerName, _ := metric["owner_name"].(string)

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
		}
	}

	// Step 6: Process all pod metrics and create owner nodes
	for _, metric := range podMetrics {
		podIP, _ := metric["pod_ip"].(string)
		podName, _ := metric["pod"].(string)
		namespace, _ := metric["namespace"].(string)
		k8sCluster, _ := metric["k8s_cluster"].(string)
		createdByKind, _ := metric["created_by_kind"].(string)
		createdByName, _ := metric["created_by_name"].(string)

		if podName == "" || namespace == "" {
			continue
		}

		ownerKind := createdByKind
		ownerName := createdByName

		if createdByKind == "ReplicaSet" && createdByName != "" {
			rsKey := fmt.Sprintf("%s/%s", namespace, createdByName)
			if owner, found := replicaSetOwners[rsKey]; found && owner["kind"] != "" {
				ownerKind = owner["kind"]
				ownerName = owner["name"]
			} else {
				ownerName = extractDeploymentFromReplicaSet(createdByName)
				ownerKind = "Deployment"
			}
		}

		if ownerKind == "" || ownerName == "" {
			e.logger.Debug("Skipping pod without owner info",
				"pod_name", podName,
				"namespace", namespace)
			continue
		}

		var targetNode *core.DbNode
		var targetNodeType = core.NodeTypePod

		serviceKeys := []string{
			fmt.Sprintf("%s:%s:%s", ownerName, namespace, k8sCluster),
			fmt.Sprintf("%s:%s:", ownerName, namespace),
			fmt.Sprintf("%s::%s", ownerName, k8sCluster),
			fmt.Sprintf("%s::", ownerName),
		}

		for _, key := range serviceKeys {
			if svcNode, found := serviceNodeMap[key]; found {
				targetNode = svcNode
				targetNodeType = core.NodeTypeService
				e.logger.Info("Found matching Service for LoadBalancer target",
					"lb_name", lbNode.Properties["name"],
					"pod_name", podName,
					"owner_name", ownerName,
					"service_key", key)
				break
			}
		}

		if targetNode == nil {
			ownerKey := fmt.Sprintf("%s:%s:%s", namespace, ownerKind, ownerName)

			if existingOwner, found := ownerNodeMap[ownerKey]; found {
				if pods, ok := existingOwner.Properties["pods"].([]string); ok {
					existingOwner.Properties["pods"] = append(pods, podName)
				}
				targetNode = existingOwner
			} else {
				labels := make(map[string]string)
				for k, v := range metric {
					if strVal, ok := v.(string); ok {
						labels[k] = strVal
					}
				}

				properties := map[string]interface{}{
					"name":       ownerName,
					"namespace":  namespace,
					"owner_kind": ownerKind,
					"pods":       []string{podName},
				}

				if k8sCluster != "" {
					properties["k8s_cluster"] = k8sCluster
				} else if cluster, ok := labels["k8s_cluster"]; ok && cluster != "" {
					properties["k8s_cluster"] = cluster
				}

				if node, ok := labels["node"]; ok && node != "" {
					properties["node"] = node
				}

				if hostIP, ok := labels["host_ip"]; ok && hostIP != "" {
					properties["host_ip"] = hostIP
				}

				if len(labels) > 0 {
					properties["labels"] = labels
				}

				targetNode = &core.DbNode{
					ID:              uuid.New().String(),
					UniqueKey:       core.BuildUniqueKey(core.CloudProviderK8s, k8sAccountID, k8sCluster, core.NodeTypePod, namespace, ownerName),
					NodeType:        core.NodeTypePod,
					CloudAccountID:  k8sAccountID,
					TenantID:        tenantID,
					Level:           "Tenant",
					Source:          "cloud",
					Properties:      properties,
					Labels:          labels,
					QueryAttributes: map[string]interface{}{},
					CreatedAt:       time.Now(),
					UpdatedAt:       time.Now(),
				}
				ownerNodeMap[ownerKey] = targetNode
				podNodes = append(podNodes, targetNode)
			}
		}

		// Create edge: LoadBalancer -> Service/Owner
		edge := &core.DbEdge{
			ID:                uuid.New().String(),
			SourceNodeID:      lbNode.ID,
			DestinationNodeID: targetNode.ID,
			RelationshipType:  core.RelationshipRoutesTo,
			Properties: map[string]interface{}{
				"discovered_from":        "aws_target_health",
				"target_ip":              podIP,
				"pod_name":               podName,
				"owner_kind":             ownerKind,
				"owner_name":             ownerName,
				"created_by_flow_source": awsLBPodTargetSourceName,
				"flow_source_category":   awsLBPodTargetCategory,
				"source_priority":        int(flow_sources.GetEdgeSourcePriority(awsLBPodTargetSourceName, core.RelationshipRoutesTo)),
			},
			CloudAccountID: awsAccountID,
			TenantID:       tenantID,
			Level:          "Tenant",
			Source:         "cloud",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		edges = append(edges, edge)

		e.logger.Info("Linked LoadBalancer to target",
			"lb_name", lbNode.Properties["name"],
			"target_type", targetNodeType,
			"target_name", targetNode.Properties["name"],
			"pod_name", podName,
			"owner_kind", ownerKind,
			"owner_name", ownerName,
			"namespace", namespace,
			"pod_ip", podIP)
	}

	e.logger.Info("LoadBalancer target enrichment completed",
		"lb_name", lbNode.Properties["name"],
		"target_ips", len(uniqueIPs),
		"pods_discovered", len(podNodes))

	return podNodes, edges, nil
}
