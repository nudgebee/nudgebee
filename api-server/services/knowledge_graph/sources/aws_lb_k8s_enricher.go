package sources

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"strings"
	"time"
)

func init() {
	// Register AWS LoadBalancer to K8s enricher factory with the global registry
	RegisterCrossSourceEnricherFactory("aws_lb_k8s", func(logger *slog.Logger) core.CrossSourceEnricherInterface {
		return NewLoadBalancerK8sEnricher(logger)
	}, "Enriches AWS LoadBalancer nodes with edges to K8s Service/Workload nodes")
}

// keyFormat is the format string for creating lookup keys: "account:namespace:name"
const keyFormat = "%s:%s:%s"

// portKeyFormat is the format string for creating NodePort lookup keys: "account:port"
const portKeyFormat = "%s:%d"

// LoadBalancerK8sEnricher enriches AWS LoadBalancer nodes with edges to K8s nodes
// This runs after both aws_source and k8s_source complete, creating cross-source relationships
type LoadBalancerK8sEnricher struct {
	logger *slog.Logger
}

// NewLoadBalancerK8sEnricher creates a new LoadBalancerK8sEnricher
func NewLoadBalancerK8sEnricher(logger *slog.Logger) *LoadBalancerK8sEnricher {
	if logger == nil {
		logger = slog.Default()
	}
	return &LoadBalancerK8sEnricher{logger: logger}
}

// GetName returns the name of this enricher
func (e *LoadBalancerK8sEnricher) GetName() string {
	return "aws_lb_k8s"
}

// EnrichCrossSources enriches LoadBalancer nodes with edges to K8s Service/Workload nodes
func (e *LoadBalancerK8sEnricher) EnrichCrossSources(
	reqCtx *security.RequestContext,
	allNodes []*core.DbNode,
	allEdges []*core.DbEdge,
	tenantID string,
) ([]*core.DbNode, []*core.DbEdge, error) {

	// 1. Filter LoadBalancer nodes from aws_source
	lbNodes := make([]*core.DbNode, 0)
	for _, node := range allNodes {
		if node.Source == "aws" && node.NodeType == core.NodeTypeLoadBalancer {
			lbNodes = append(lbNodes, node)
		}
	}

	if len(lbNodes) == 0 {
		e.logger.Debug("No LoadBalancer nodes found from aws_source")
		return allNodes, allEdges, nil
	}

	e.logger.Info("Starting LoadBalancer-K8s enrichment",
		"lb_count", len(lbNodes),
		"total_nodes", len(allNodes))

	// 2. Filter K8s nodes for matching
	k8sNodes := make([]*core.DbNode, 0)
	for _, node := range allNodes {
		if node.Source == "k8s" {
			k8sNodes = append(k8sNodes, node)
		}
	}

	if len(k8sNodes) == 0 {
		e.logger.Debug("No K8s nodes found from k8s_source")
		return allNodes, allEdges, nil
	}

	// Collect all unique K8s account IDs from nodes (for fallback when no mapping exists)
	allK8sAccountIDs := make(map[string]bool)
	for _, node := range k8sNodes {
		if node.CloudAccountID != "" {
			allK8sAccountIDs[node.CloudAccountID] = true
		}
	}
	allK8sAccountIDsList := make([]string, 0, len(allK8sAccountIDs))
	for accountID := range allK8sAccountIDs {
		allK8sAccountIDsList = append(allK8sAccountIDsList, accountID)
	}

	// 3. Get AWS -> K8s account mapping
	awsToK8sMap := e.getAWSToK8sAccountMapping(reqCtx, tenantID)

	if len(awsToK8sMap) == 0 {
		e.logger.Info("No AWS-K8s account mapping found, will search all K8s accounts",
			"k8s_accounts_count", len(allK8sAccountIDsList))
	} else {
		e.logger.Info("AWS-K8s account mapping loaded",
			"mappings", len(awsToK8sMap))
	}

	// 4. Build K8s node lookup maps
	k8sServicesByName := make(map[string]*core.DbNode)      // "account:namespace:name" -> K8sService
	k8sServicesByNodePort := make(map[string]*core.DbNode)  // "account:port" -> K8sService
	k8sServicesByClusterIP := make(map[string]*core.DbNode) // "account:cluster_ip" -> K8sService
	k8sWorkloadsByName := make(map[string]*core.DbNode)     // "account:namespace:name" -> Workload

	for _, node := range k8sNodes {
		accountID := node.CloudAccountID
		namespace := getStringProp(node, "namespace")
		name := getStringProp(node, "name")

		switch node.NodeType {
		case core.NodeTypeK8sService:
			if namespace != "" && name != "" {
				key := fmt.Sprintf(keyFormat, accountID, namespace, name)
				k8sServicesByName[key] = node

				// Index by node_ports
				nodePorts := e.extractNodePorts(node)
				for _, port := range nodePorts {
					portKey := fmt.Sprintf(portKeyFormat, accountID, port)
					k8sServicesByNodePort[portKey] = node
				}

				// Index by cluster_ip
				clusterIP := getStringProp(node, "cluster_ip")
				if clusterIP != "" && clusterIP != "None" {
					ipKey := fmt.Sprintf("%s:%s", accountID, clusterIP)
					k8sServicesByClusterIP[ipKey] = node
				}

				// Index by external_ips
				externalIPs := e.extractStringSlice(node, "external_ips")
				for _, extIP := range externalIPs {
					ipKey := fmt.Sprintf("%s:%s", accountID, extIP)
					k8sServicesByClusterIP[ipKey] = node
				}
			}

		case core.NodeTypeWorkload:
			if namespace != "" && name != "" {
				key := fmt.Sprintf(keyFormat, accountID, namespace, name)
				k8sWorkloadsByName[key] = node
			}
		}
	}

	e.logger.Debug("K8s lookup maps built",
		"services_by_name", len(k8sServicesByName),
		"services_by_nodeport", len(k8sServicesByNodePort),
		"services_by_cluster_ip", len(k8sServicesByClusterIP),
		"workloads_by_name", len(k8sWorkloadsByName))

	// 5. Enrich each LoadBalancer
	newNodes := make([]*core.DbNode, 0)
	newEdges := make([]*core.DbEdge, 0)
	matchedCount := 0

	for _, lbNode := range lbNodes {
		awsAccountID := lbNode.CloudAccountID
		k8sAccountIDs := awsToK8sMap[awsAccountID]

		// Fallback: if no specific mapping, try all K8s accounts
		if len(k8sAccountIDs) == 0 {
			k8sAccountIDs = allK8sAccountIDsList
			if len(k8sAccountIDs) > 0 {
				e.logger.Debug("No K8s account mapping for AWS account, trying all K8s accounts",
					"aws_account", awsAccountID,
					"lb_name", getStringProp(lbNode, "name"),
					"k8s_accounts_count", len(k8sAccountIDs))
			}
		}

		if len(k8sAccountIDs) == 0 {
			e.logger.Debug("No K8s accounts available for matching",
				"aws_account", awsAccountID,
				"lb_name", getStringProp(lbNode, "name"))
			continue
		}

		// Try each K8s account (mapped or all available)
		for _, k8sAccountID := range k8sAccountIDs {
			nodes, edges := e.enrichLoadBalancer(reqCtx, lbNode, awsAccountID, k8sAccountID, k8sServicesByName, k8sServicesByNodePort, k8sServicesByClusterIP, k8sWorkloadsByName, tenantID)
			if len(edges) > 0 {
				newNodes = append(newNodes, nodes...)
				newEdges = append(newEdges, edges...)
				matchedCount++
				break // Only match once per LB
			}
		}
	}

	// 6. Combine nodes and edges
	allNodes = append(allNodes, newNodes...)
	allEdges = append(allEdges, newEdges...)

	e.logger.Info("LoadBalancer-K8s enrichment completed",
		"lb_nodes", len(lbNodes),
		"matched_lbs", matchedCount,
		"new_nodes", len(newNodes),
		"new_edges", len(newEdges))

	// 7. Gap 3 — link AWS ManagedCluster (EKS) → K8s Cluster nodes by name
	managedClusterEdges := e.linkManagedClusterToK8sCluster(allNodes, tenantID)
	allEdges = append(allEdges, managedClusterEdges...)

	// 8. Gap 5 — link BackendPool → K8s Workload via EC2→K8sNode resolution
	// Runs after k8s_ec2_enricher has created K8sNode→ComputeInstance edges so
	// we can walk BackendPool→ComputeInstance→K8sNode→Workload in-memory.
	backendPoolEdges := e.linkBackendPoolToWorkload(allNodes, allEdges, tenantID)
	allEdges = append(allEdges, backendPoolEdges...)

	return allNodes, allEdges, nil
}

// linkManagedClusterToK8sCluster creates MANAGES edges from AWS ManagedCluster (EKS)
// to K8s Cluster nodes by matching on cluster name.
func (e *LoadBalancerK8sEnricher) linkManagedClusterToK8sCluster(
	allNodes []*core.DbNode,
	tenantID string,
) []*core.DbEdge {
	// Index K8s Cluster nodes by name
	k8sClusterByName := make(map[string]*core.DbNode)
	for _, n := range allNodes {
		if n.NodeType == core.NodeTypeCluster && n.Source == "k8s" {
			if name, _ := n.Properties["name"].(string); name != "" {
				k8sClusterByName[name] = n
			}
		}
	}
	if len(k8sClusterByName) == 0 {
		return nil
	}

	newEdges := make([]*core.DbEdge, 0)
	for _, n := range allNodes {
		if n.NodeType != core.NodeTypeManagedCluster {
			continue
		}
		if svc, _ := n.Properties["service_name"].(string); svc != "AmazonEKS" {
			continue
		}
		clusterName, _ := n.Properties["name"].(string)
		if clusterName == "" {
			continue
		}
		k8sCluster, found := k8sClusterByName[clusterName]
		if !found {
			continue
		}
		edge := core.NewEdge(
			n.ID,
			k8sCluster.ID,
			core.RelationshipManages,
			map[string]interface{}{
				"connection_type": "eks_cluster_name",
				"cluster_name":    clusterName,
			},
			tenantID,
			n.CloudAccountID,
			"aws_lb_k8s_enricher",
		)
		newEdges = append(newEdges, edge)
		e.logger.Debug("linked ManagedCluster → K8s Cluster",
			"cluster_name", clusterName)
	}
	e.logger.Info("ManagedCluster-K8sCluster linking completed", "edges_created", len(newEdges))
	return newEdges
}

// linkBackendPoolToWorkload creates ROUTES_TO edges from BackendPool → Workload
// by walking the chain: BackendPool→ComputeInstance (existing ROUTES_TO edge) →
// K8s Node (RUNS_ON edge from k8s_ec2_enricher) → Workload (RUNS_ON edge from k8s_source).
func (e *LoadBalancerK8sEnricher) linkBackendPoolToWorkload(
	allNodes []*core.DbNode,
	allEdges []*core.DbEdge,
	tenantID string,
) []*core.DbEdge {
	// Build quick node index
	nodeByID := make(map[string]*core.DbNode, len(allNodes))
	for _, n := range allNodes {
		nodeByID[n.ID] = n
	}

	// Build adjacency: sourceID → []destinationID, filtered by relationship
	routesToEC2 := make(map[string][]string)        // BackendPool → ComputeInstance
	nodeRunsOnEC2 := make(map[string][]string)      // K8s Node → ComputeInstance (or reverse)
	workloadRunsOnNode := make(map[string][]string) // Workload → K8s Node (or reverse)

	for _, edge := range allEdges {
		src, hasSrc := nodeByID[edge.SourceNodeID]
		dst, hasDst := nodeByID[edge.DestinationNodeID]
		if !hasSrc || !hasDst {
			continue
		}

		switch edge.RelationshipType {
		case core.RelationshipRoutesTo:
			if src.NodeType == core.NodeTypeBackendPool && dst.NodeType == core.NodeTypeComputeInstance {
				routesToEC2[src.ID] = append(routesToEC2[src.ID], dst.ID)
			}
		case core.RelationshipRunsOn:
			// K8s Node → EC2 (created by k8s_ec2_enricher)
			if src.NodeType == core.NodeTypeNode && dst.NodeType == core.NodeTypeComputeInstance {
				nodeRunsOnEC2[dst.ID] = append(nodeRunsOnEC2[dst.ID], src.ID) // EC2 → k8sNode
			}
			// Workload → K8s Node (created by k8s_source)
			if src.NodeType == core.NodeTypeWorkload && dst.NodeType == core.NodeTypeNode {
				workloadRunsOnNode[dst.ID] = append(workloadRunsOnNode[dst.ID], src.ID) // node → workload
			}
		}
	}

	if len(routesToEC2) == 0 {
		return nil
	}

	newEdges := make([]*core.DbEdge, 0)
	seen := make(map[string]bool) // deduplicate backendPool+workload pairs

	for bpID, ec2IDs := range routesToEC2 {
		bpNode := nodeByID[bpID]
		for _, ec2ID := range ec2IDs {
			// Walk EC2 → K8s Node → Workload
			for _, k8sNodeID := range nodeRunsOnEC2[ec2ID] {
				for _, workloadID := range workloadRunsOnNode[k8sNodeID] {
					key := bpID + ":" + workloadID
					if seen[key] {
						continue
					}
					seen[key] = true

					workloadNode := nodeByID[workloadID]
					edge := core.NewEdge(
						bpID,
						workloadID,
						core.RelationshipRoutesTo,
						map[string]interface{}{
							"connection_type": "ec2_node_resolution",
							"ec2_instance_id": ec2ID,
							"k8s_node_id":     k8sNodeID,
							"workload_name":   getStringProp(workloadNode, "name"),
							"workload_ns":     getStringProp(workloadNode, "namespace"),
						},
						tenantID,
						bpNode.CloudAccountID,
						"aws_lb_k8s_enricher",
					)
					newEdges = append(newEdges, edge)
					e.logger.Debug("linked BackendPool → Workload via EC2 node resolution",
						"backend_pool", getStringProp(bpNode, "name"),
						"workload", getStringProp(workloadNode, "name"))
				}
			}
		}
	}
	e.logger.Info("BackendPool-Workload linking completed", "edges_created", len(newEdges))
	return newEdges
}

// enrichLoadBalancer tries to match a single LoadBalancer to K8s nodes
// Returns both new nodes (if created) and edges
func (e *LoadBalancerK8sEnricher) enrichLoadBalancer(
	reqCtx *security.RequestContext,
	lbNode *core.DbNode,
	awsAccountID string,
	k8sAccountID string,
	k8sServicesByName map[string]*core.DbNode,
	k8sServicesByNodePort map[string]*core.DbNode,
	k8sServicesByClusterIP map[string]*core.DbNode,
	k8sWorkloadsByName map[string]*core.DbNode,
	tenantID string,
) ([]*core.DbNode, []*core.DbEdge) {

	lbName := getStringProp(lbNode, "name")

	// Strategy 1: Match by K8s tags (most reliable)
	if targetNode := e.matchByK8sTags(lbNode, k8sAccountID, k8sServicesByName, k8sWorkloadsByName); targetNode != nil {
		e.logger.Info("Matched LoadBalancer to K8s node via K8s tags",
			"lb_name", lbName,
			"target_node", getStringProp(targetNode, "name"),
			"target_type", targetNode.NodeType)
		edge := e.createEdge(lbNode, targetNode, "k8s_tags", tenantID)
		return nil, []*core.DbEdge{edge}
	}

	// Strategy 2: Match by NodePort
	if targetNode := e.matchByNodePort(lbNode, k8sAccountID, k8sServicesByNodePort); targetNode != nil {
		e.logger.Info("Matched LoadBalancer to K8s node via NodePort",
			"lb_name", lbName,
			"target_node", getStringProp(targetNode, "name"),
			"target_type", targetNode.NodeType)
		edge := e.createEdge(lbNode, targetNode, "node_port", tenantID)
		return nil, []*core.DbEdge{edge}
	}

	// Strategy 3: Match by target IPs to cluster_ip (quick check)
	if targetNode := e.matchByTargetIPs(reqCtx, lbNode, awsAccountID, k8sAccountID, k8sServicesByClusterIP); targetNode != nil {
		e.logger.Info("Matched LoadBalancer to K8s node via target IPs",
			"lb_name", lbName,
			"target_node", getStringProp(targetNode, "name"),
			"target_type", targetNode.NodeType)
		edge := e.createEdge(lbNode, targetNode, "target_ip", tenantID)
		return nil, []*core.DbEdge{edge}
	}

	// Strategy 4: Match via Prometheus pod IP mapping (most comprehensive)
	// This queries kube_pod_info to resolve target IPs to actual pods and their owners
	// Searches existing nodes first, only creates new nodes if not found
	nodes, edges := e.matchByPrometheus(reqCtx, lbNode, awsAccountID, k8sAccountID,
		k8sServicesByName, k8sWorkloadsByName, tenantID)
	if len(edges) > 0 {
		return nodes, edges
	}

	e.logger.Debug("No K8s match found for LoadBalancer",
		"lb_name", lbName,
		"k8s_account", k8sAccountID)

	return nil, nil
}

// matchByK8sTags matches LoadBalancer to K8s nodes using kubernetes.io/service-name tag
func (e *LoadBalancerK8sEnricher) matchByK8sTags(
	lbNode *core.DbNode,
	k8sAccountID string,
	k8sServicesByName map[string]*core.DbNode,
	k8sWorkloadsByName map[string]*core.DbNode,
) *core.DbNode {

	// Check for kubernetes.io/service-name tag (already extracted by aws_source.go)
	k8sServiceName := getStringProp(lbNode, "k8s_service_name")
	k8sNamespace := getStringProp(lbNode, "k8s_service_namespace")

	if k8sServiceName == "" || k8sNamespace == "" {
		return nil
	}

	key := fmt.Sprintf(keyFormat, k8sAccountID, k8sNamespace, k8sServiceName)

	// Try K8sService first
	if svc, found := k8sServicesByName[key]; found {
		return svc
	}

	// Try Workload
	if wl, found := k8sWorkloadsByName[key]; found {
		return wl
	}

	return nil
}

// matchByNodePort matches LoadBalancer to K8s Service using target_node_ports
func (e *LoadBalancerK8sEnricher) matchByNodePort(
	lbNode *core.DbNode,
	k8sAccountID string,
	k8sServicesByNodePort map[string]*core.DbNode,
) *core.DbNode {

	// Get target_node_ports from LB (already extracted by aws_source.go)
	targetNodePorts := e.extractTargetNodePorts(lbNode)
	if len(targetNodePorts) == 0 {
		return nil
	}

	for _, port := range targetNodePorts {
		portKey := fmt.Sprintf(portKeyFormat, k8sAccountID, port)
		if svc, found := k8sServicesByNodePort[portKey]; found {
			return svc
		}
	}

	return nil
}

// matchByTargetIPs matches LoadBalancer to K8s Service using target IPs from AWS API
func (e *LoadBalancerK8sEnricher) matchByTargetIPs(
	reqCtx *security.RequestContext,
	lbNode *core.DbNode,
	awsAccountID string,
	k8sAccountID string,
	k8sServicesByClusterIP map[string]*core.DbNode,
) *core.DbNode {

	// Get LB ARN and region
	arn := getStringProp(lbNode, "arn")
	region := getStringProp(lbNode, "region")

	if arn == "" || region == "" || !isValidARN(arn) {
		e.logger.Debug("Skipping target IP matching: missing or invalid ARN/region",
			"lb_name", getStringProp(lbNode, "name"))
		return nil
	}

	// Step 1: Query AWS for target groups
	targetGroups, err := e.getTargetGroups(reqCtx, awsAccountID, arn, region)
	if err != nil || len(targetGroups) == 0 {
		return nil
	}

	// Step 2: Collect target IPs from all target groups
	targetIPs := e.collectTargetIPs(reqCtx, awsAccountID, region, targetGroups, lbNode)
	if len(targetIPs) == 0 {
		return nil
	}

	// Step 3: Match IPs to K8s Service cluster_ip
	for ip := range targetIPs {
		ipKey := fmt.Sprintf("%s:%s", k8sAccountID, ip)
		if svc, found := k8sServicesByClusterIP[ipKey]; found {
			e.logger.Debug("Matched target IP to K8s Service",
				"ip", ip,
				"service", getStringProp(svc, "name"))
			return svc
		}
	}

	return nil
}

// getTargetGroups queries AWS for target groups of a LoadBalancer
func (e *LoadBalancerK8sEnricher) getTargetGroups(
	reqCtx *security.RequestContext,
	awsAccountID, arn, region string,
) ([]map[string]interface{}, error) {

	tgCommand := fmt.Sprintf(
		"aws elbv2 describe-target-groups --region %s --load-balancer-arn %s --output json",
		region, arn,
	)

	tgResp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   tgCommand,
	})
	if err != nil {
		e.logger.Debug("Failed to query target groups",
			"arn", arn,
			"error", err)
		return nil, err
	}

	// Parse target groups
	data, ok := tgResp["data"].(string)
	if !ok || data == "" || !strings.HasPrefix(strings.TrimSpace(data), "{") {
		return nil, fmt.Errorf("unexpected or non-JSON response format from cloud collector")
	}

	var tgData struct {
		TargetGroups []map[string]interface{} `json:"TargetGroups"`
	}
	if err := json.Unmarshal([]byte(data), &tgData); err != nil {
		e.logger.Debug("Failed to parse target groups response", "error", err)
		return nil, err
	}

	return tgData.TargetGroups, nil
}

// collectTargetIPs collects all target IPs from target groups, resolving EC2 instances to IPs
func (e *LoadBalancerK8sEnricher) collectTargetIPs(
	reqCtx *security.RequestContext,
	awsAccountID, region string,
	targetGroups []map[string]interface{},
	lbNode *core.DbNode,
) map[string]bool {

	uniqueIPs := make(map[string]bool)
	instanceIDs := make(map[string]bool)

	// Collect IPs and instance IDs from target health
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
			e.logger.Debug("Failed to query target health", "target_group", tgArn, "error", err)
			continue
		}

		// Parse target health
		data, ok := healthResp["data"].(string)
		if !ok || data == "" || !strings.HasPrefix(strings.TrimSpace(data), "{") {
			e.logger.Debug("Skipping target group: unexpected response format", "tg_arn", tgArn)
			continue
		}

		var healthData struct {
			TargetHealthDescriptions []map[string]interface{} `json:"TargetHealthDescriptions"`
		}
		if err := json.Unmarshal([]byte(data), &healthData); err != nil {
			e.logger.Debug("Failed to parse target health response", "error", err)
			continue
		}

		for _, target := range healthData.TargetHealthDescriptions {
			targetInfo, ok := target["Target"].(map[string]interface{})
			if !ok {
				continue
			}

			targetID, ok := targetInfo["Id"].(string)
			if !ok {
				continue
			}

			// Check if this is an instance ID (starts with "i-") or an IP address
			if strings.HasPrefix(targetID, "i-") {
				instanceIDs[targetID] = true
			} else {
				uniqueIPs[targetID] = true
			}
		}
	}

	// Resolve EC2 instance IDs to private IPs
	if len(instanceIDs) > 0 {
		e.resolveInstanceIPs(reqCtx, awsAccountID, region, instanceIDs, uniqueIPs, lbNode)
	}

	return uniqueIPs
}

// resolveInstanceIPs resolves EC2 instance IDs to their private IPs
func (e *LoadBalancerK8sEnricher) resolveInstanceIPs(
	reqCtx *security.RequestContext,
	awsAccountID, region string,
	instanceIDs map[string]bool,
	uniqueIPs map[string]bool,
	lbNode *core.DbNode,
) {
	// Build space-separated list of instance IDs
	instanceIDList := make([]string, 0, len(instanceIDs))
	for instanceID := range instanceIDs {
		instanceIDList = append(instanceIDList, instanceID)
	}
	instanceIDStr := strings.Join(instanceIDList, " ")

	// Query EC2 to get private IPs
	ec2Command := fmt.Sprintf(
		"aws ec2 describe-instances --region %s --instance-ids %s --query 'Reservations[].Instances[].[InstanceId,PrivateIpAddress]' --output json",
		region, instanceIDStr,
	)

	ec2Resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   ec2Command,
	})
	if err != nil {
		e.logger.Debug("Failed to query EC2 instances",
			"lb_name", getStringProp(lbNode, "name"),
			"error", err)
		return
	}

	// Parse EC2 response: [[instanceID, privateIP], ...]
	data, ok := ec2Resp["data"].(string)
	if !ok || data == "" || (!strings.HasPrefix(strings.TrimSpace(data), "{") && !strings.HasPrefix(strings.TrimSpace(data), "[")) {
		e.logger.Debug("Failed to resolve instance IPs: unexpected response format")
		return
	}

	var instances [][]string
	if err := json.Unmarshal([]byte(data), &instances); err != nil {
		e.logger.Debug("Failed to parse EC2 response", "error", err)
		return
	}

	for _, inst := range instances {
		if len(inst) == 2 && inst[1] != "" {
			uniqueIPs[inst[1]] = true
			e.logger.Debug("Resolved EC2 instance to private IP",
				"instance_id", inst[0],
				"private_ip", inst[1])
		}
	}
}

// createEdge creates an edge from LoadBalancer to K8s node
// Uses core.NewEdge() helper following aws_source pattern
func (e *LoadBalancerK8sEnricher) createEdge(
	lbNode, targetNode *core.DbNode,
	matchStrategy, tenantID string,
) *core.DbEdge {
	properties := map[string]interface{}{
		"discovered_from":       "cross_source_enrichment",
		"match_strategy":        matchStrategy,
		"k8s_service_name":      getStringProp(targetNode, "name"),
		"k8s_service_namespace": getStringProp(targetNode, "namespace"),
	}

	return core.NewEdge(
		lbNode.ID,
		targetNode.ID,
		core.RelationshipRoutesTo,
		properties,
		tenantID,
		lbNode.CloudAccountID,
		"aws_lb_k8s_enricher",
	)
}

// getAWSToK8sAccountMapping returns a map of AWS account IDs to their linked K8s account IDs
func (e *LoadBalancerK8sEnricher) getAWSToK8sAccountMapping(
	reqCtx *security.RequestContext,
	tenantID string,
) map[string][]string {
	// Use existing helper function (returns k8s_account -> []aws_accounts)
	// We need the reverse: aws_account -> []k8s_accounts

	k8sToAws, err := core.GetK8sCloudAccountMapping(reqCtx, tenantID, "AWS")
	if err != nil {
		e.logger.Warn("Failed to get K8s-AWS account mapping", "error", err)
		return make(map[string][]string)
	}

	// Reverse the mapping
	awsToK8s := make(map[string][]string)
	for k8sAcct, awsAccts := range k8sToAws {
		for _, awsAcct := range awsAccts {
			awsToK8s[awsAcct] = append(awsToK8s[awsAcct], k8sAcct)
		}
	}

	return awsToK8s
}

// extractNodePorts extracts node_ports from a K8s Service node
func (e *LoadBalancerK8sEnricher) extractNodePorts(node *core.DbNode) []int {
	ports := make([]int, 0)

	nodePorts, ok := node.Properties["node_ports"]
	if !ok {
		return ports
	}

	switch v := nodePorts.(type) {
	case []interface{}:
		for _, p := range v {
			switch pv := p.(type) {
			case float64:
				ports = append(ports, int(pv))
			case int:
				ports = append(ports, pv)
			case int64:
				ports = append(ports, int(pv))
			}
		}
	case []int:
		ports = v
	case []float64:
		for _, p := range v {
			ports = append(ports, int(p))
		}
	}

	return ports
}

// extractTargetNodePorts extracts target_node_ports from a LoadBalancer node
func (e *LoadBalancerK8sEnricher) extractTargetNodePorts(node *core.DbNode) []int {
	ports := make([]int, 0)

	targetNodePorts, ok := node.Properties["target_node_ports"]
	if !ok {
		return ports
	}

	switch v := targetNodePorts.(type) {
	case []interface{}:
		for _, p := range v {
			switch pv := p.(type) {
			case float64:
				ports = append(ports, int(pv))
			case int:
				ports = append(ports, pv)
			case int64:
				ports = append(ports, int(pv))
			}
		}
	case []int:
		ports = v
	case []float64:
		for _, p := range v {
			ports = append(ports, int(p))
		}
	}

	return ports
}

// extractStringSlice extracts a string slice property from a node
func (e *LoadBalancerK8sEnricher) extractStringSlice(node *core.DbNode, key string) []string {
	result := make([]string, 0)
	if node == nil || node.Properties == nil {
		return result
	}

	val, ok := node.Properties[key]
	if !ok {
		return result
	}

	switch v := val.(type) {
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
	case []string:
		result = v
	}

	return result
}

// getStringProp is a helper to safely get a string property from a node
func getStringProp(node *core.DbNode, key string) string {
	if node == nil || node.Properties == nil {
		return ""
	}
	if v, ok := node.Properties[key].(string); ok {
		return v
	}
	return ""
}

// isValidARN performs basic validation of AWS ARN format
func isValidARN(arn string) bool {
	if arn == "" {
		return false
	}
	parts := strings.Split(arn, ":")
	// ARN format: arn:partition:service:region:account-id:resource-id
	// or arn:partition:service:region:account-id:resource-type/resource-id
	return len(parts) >= 6 && parts[0] == "arn" && strings.HasPrefix(parts[1], "aws")
}

// ============================================================================
// Prometheus Integration Types and Methods
// ============================================================================

// podMetricInfo holds parsed kube_pod_info metric labels
type podMetricInfo struct {
	PodIP         string
	PodName       string
	Namespace     string
	K8sCluster    string
	CreatedByKind string
	CreatedByName string
	Node          string
	HostIP        string
	Labels        map[string]string // All raw labels for full context
}

// replicaSetOwner holds parsed kube_replicaset_owner metric info
type replicaSetOwner struct {
	Kind string
	Name string
}

// extractDeploymentFromReplicaSet extracts the Deployment name from a ReplicaSet name
// ReplicaSet naming pattern: {deployment-name}-{hash}
// Example: manoj-shipper-759b8c597f -> manoj-shipper
func extractDeploymentFromReplicaSet(replicaSetName string) string {
	parts := strings.Split(replicaSetName, "-")
	if len(parts) < 2 {
		return replicaSetName
	}

	// Remove last part if it looks like a ReplicaSet hash (typically 9-10 alphanumeric chars)
	lastPart := parts[len(parts)-1]
	if len(lastPart) >= 8 && len(lastPart) <= 10 && isAlphanumeric(lastPart) {
		return strings.Join(parts[:len(parts)-1], "-")
	}

	return replicaSetName
}

// isAlphanumeric checks if a string contains only lowercase alphanumeric characters
func isAlphanumeric(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// parsePrometheusResponse extracts the result array from various Prometheus response formats
// Handles three formats:
// 1. {"query_name": [...]} - query name as direct key
// 2. {"data": [...]} - data array wrapper
// 3. {"data": {"query_name": {"result": [...]}}} - nested structure
func (e *LoadBalancerK8sEnricher) parsePrometheusResponse(resp map[string]any, queryName string) []interface{} {
	// Format 1: Query name as direct key
	if data, ok := resp[queryName].([]interface{}); ok {
		return data
	}

	// Format 2: Data array wrapper
	if data, ok := resp["data"].([]interface{}); ok {
		return data
	}

	// Format 3: Nested structure
	if data, ok := resp["data"].(map[string]interface{}); ok {
		if queryData, ok := data[queryName].(map[string]interface{}); ok {
			if result, ok := queryData["result"].([]interface{}); ok {
				return result
			}
		}
	}

	return nil
}

// queryPodInfoByIPs queries kube_pod_info from Prometheus to map IPs to pods
// Returns a map of IP -> podMetricInfo
func (e *LoadBalancerK8sEnricher) queryPodInfoByIPs(
	k8sAccountID string,
	targetIPs map[string]bool,
) (map[string]*podMetricInfo, error) {

	if len(targetIPs) == 0 {
		return make(map[string]*podMetricInfo), nil
	}

	// Build IP filter for Prometheus query
	ipList := make([]string, 0, len(targetIPs))
	for ip := range targetIPs {
		ipList = append(ipList, ip)
	}
	ipFilter := strings.Join(ipList, "|")
	if ipFilter == "" {
		return make(map[string]*podMetricInfo), nil
	}

	queries := map[string]string{
		"pod_info": fmt.Sprintf(`kube_pod_info{pod_ip=~"%s"}`, ipFilter),
	}

	// Use UTC: relay.ExecutePrometheus formats the timestamp with a "UTC"
	// suffix; the value must already be UTC. Defense in depth with the
	// relay-side fix.
	endTime := time.Now().UTC()
	startTime := endTime.Add(-5 * time.Minute)

	resp, err := relay.ExecutePrometheus(k8sAccountID, startTime, endTime, queries, true)
	if err != nil {
		e.logger.Warn("Failed to query kube_pod_info",
			"k8s_account", k8sAccountID,
			"error", err)
		return nil, err
	}

	resultArray := e.parsePrometheusResponse(resp, "pod_info")
	if len(resultArray) == 0 {
		e.logger.Debug("No pod info results from Prometheus",
			"k8s_account", k8sAccountID,
			"target_ips", len(targetIPs))
		return make(map[string]*podMetricInfo), nil
	}

	// Parse results into podMetricInfo structs
	result := make(map[string]*podMetricInfo)
	for _, item := range resultArray {
		pod, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		metric, ok := pod["metric"].(map[string]interface{})
		if !ok {
			continue
		}

		podIP, _ := metric["pod_ip"].(string)
		if podIP == "" {
			continue
		}

		// Extract all labels
		labels := make(map[string]string)
		for k, v := range metric {
			if strVal, ok := v.(string); ok {
				labels[k] = strVal
			}
		}

		result[podIP] = &podMetricInfo{
			PodIP:         podIP,
			PodName:       labels["pod"],
			Namespace:     labels["namespace"],
			K8sCluster:    labels["k8s_cluster"],
			CreatedByKind: labels["created_by_kind"],
			CreatedByName: labels["created_by_name"],
			Node:          labels["node"],
			HostIP:        labels["host_ip"],
			Labels:        labels,
		}
	}

	e.logger.Debug("Queried pod info from Prometheus",
		"k8s_account", k8sAccountID,
		"pods_found", len(result))

	return result, nil
}

// queryReplicaSetOwners queries kube_replicaset_owner to resolve RS -> Deployment chain
// Groups queries by namespace for efficiency
func (e *LoadBalancerK8sEnricher) queryReplicaSetOwners(
	k8sAccountID string,
	replicaSets map[string]bool, // key: "namespace/rsName"
) (map[string]*replicaSetOwner, error) {

	if len(replicaSets) == 0 {
		return make(map[string]*replicaSetOwner), nil
	}

	// Group by namespace for efficient queries
	namespaceReplicaSets := make(map[string][]string)
	for nsRS := range replicaSets {
		parts := strings.SplitN(nsRS, "/", 2)
		if len(parts) == 2 {
			namespace := parts[0]
			rsName := parts[1]
			namespaceReplicaSets[namespace] = append(namespaceReplicaSets[namespace], rsName)
		}
	}

	// Build focused query with specific ReplicaSets
	var queryParts []string
	for namespace, rsNames := range namespaceReplicaSets {
		if len(rsNames) == 1 {
			queryParts = append(queryParts,
				fmt.Sprintf(`kube_replicaset_owner{namespace="%s",replicaset="%s"}`, namespace, rsNames[0]))
		} else {
			// Use regex for multiple ReplicaSets in same namespace
			rsRegex := strings.Join(rsNames, "|")
			queryParts = append(queryParts,
				fmt.Sprintf(`kube_replicaset_owner{namespace="%s",replicaset=~"%s"}`, namespace, rsRegex))
		}
	}

	rsQuery := strings.Join(queryParts, " or ")
	queries := map[string]string{
		"rs_owner": rsQuery,
	}

	// Use UTC: relay.ExecutePrometheus formats the timestamp with a "UTC"
	// suffix; the value must already be UTC. Defense in depth with the
	// relay-side fix.
	endTime := time.Now().UTC()
	startTime := endTime.Add(-5 * time.Minute)

	resp, err := relay.ExecutePrometheus(k8sAccountID, startTime, endTime, queries, true)
	if err != nil {
		e.logger.Warn("Failed to query kube_replicaset_owner",
			"k8s_account", k8sAccountID,
			"error", err)
		return nil, err
	}

	resultArray := e.parsePrometheusResponse(resp, "rs_owner")
	result := make(map[string]*replicaSetOwner)

	for _, item := range resultArray {
		rs, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		metric, ok := rs["metric"].(map[string]interface{})
		if !ok {
			continue
		}

		rsNamespace, _ := metric["namespace"].(string)
		rsName, _ := metric["replicaset"].(string)
		ownerKind, _ := metric["owner_kind"].(string)
		ownerName, _ := metric["owner_name"].(string)

		if rsNamespace != "" && rsName != "" {
			key := fmt.Sprintf("%s/%s", rsNamespace, rsName)
			result[key] = &replicaSetOwner{
				Kind: ownerKind,
				Name: ownerName,
			}
		}
	}

	e.logger.Debug("Queried ReplicaSet owners",
		"k8s_account", k8sAccountID,
		"replicasets_resolved", len(result))

	return result, nil
}

// createWorkloadNode creates a new DbNode for a pod owner (Deployment/StatefulSet/DaemonSet)
// Only called when no existing node is found
// Follows k8s_source pattern: temp node -> NewUniqueKeyComponents -> NewNode
func (e *LoadBalancerK8sEnricher) createWorkloadNode(
	podInfo *podMetricInfo,
	ownerKind string,
	ownerName string,
	k8sAccountID string,
	tenantID string,
) *core.DbNode {

	properties := map[string]interface{}{
		"name":       ownerName,
		"namespace":  podInfo.Namespace,
		"owner_kind": ownerKind,
	}

	// Add K8s cluster if available (use "cluster" key like k8s_source)
	if podInfo.K8sCluster != "" {
		properties["cluster"] = podInfo.K8sCluster
	}

	// Add node and host_ip if available
	if podInfo.Node != "" {
		properties["node"] = podInfo.Node
	}
	if podInfo.HostIP != "" {
		properties["host_ip"] = podInfo.HostIP
	}

	// Store all labels for full context
	if len(podInfo.Labels) > 0 {
		properties["labels"] = podInfo.Labels
	}

	// Generate unique key using core.NewUniqueKeyComponents (like k8s_source pattern)
	// Format: k8s:{account}:{location}:{NodeType}:{namespace}:{name}
	keyComponents := core.NewUniqueKeyComponents("k8s", core.NodeTypeWorkload)
	keyComponents.Account = k8sAccountID
	keyComponents.Hierarchy = podInfo.Namespace
	keyComponents.Name = ownerName
	// Note: Location is empty for workloads (not region-specific)

	uniqueKey := keyComponents.Build()

	// Step 3: Create node using core.NewNode (like k8s_source)
	return core.NewNode(
		core.NodeTypeWorkload,
		uniqueKey,
		properties,
		tenantID,
		k8sAccountID,
		"k8s", // Use "k8s" as source to match existing K8s nodes
	)
}

// matchByPrometheus matches LoadBalancer to K8s pods/workloads using Prometheus kube_pod_info
// This is Strategy 4 - resolves target IPs to actual pods and their owners
// Key principle: Search existing nodes first, only create new nodes if not found
// Returns both matching edges AND new nodes (for workloads not in existing node set)
func (e *LoadBalancerK8sEnricher) matchByPrometheus(
	reqCtx *security.RequestContext,
	lbNode *core.DbNode,
	awsAccountID string,
	k8sAccountID string,
	k8sServicesByName map[string]*core.DbNode,
	k8sWorkloadsByName map[string]*core.DbNode,
	tenantID string,
) ([]*core.DbNode, []*core.DbEdge) {

	newNodes := make([]*core.DbNode, 0)
	newEdges := make([]*core.DbEdge, 0)

	// Get LB ARN and region
	arn := getStringProp(lbNode, "arn")
	region := getStringProp(lbNode, "region")

	if arn == "" || region == "" {
		return newNodes, newEdges
	}

	// Step 1: Get target groups from AWS
	targetGroups, err := e.getTargetGroups(reqCtx, awsAccountID, arn, region)
	if err != nil || len(targetGroups) == 0 {
		return newNodes, newEdges
	}

	// Step 2: Collect target IPs (reuse existing logic)
	targetIPs := e.collectTargetIPs(reqCtx, awsAccountID, region, targetGroups, lbNode)
	if len(targetIPs) == 0 {
		return newNodes, newEdges
	}

	// Step 3: Query Prometheus for pod info
	podInfoMap, err := e.queryPodInfoByIPs(k8sAccountID, targetIPs)
	if err != nil || len(podInfoMap) == 0 {
		return newNodes, newEdges
	}

	// Step 4: Collect ReplicaSets that need owner resolution
	replicaSetsToQuery := make(map[string]bool)
	for _, podInfo := range podInfoMap {
		if podInfo.CreatedByKind == "ReplicaSet" && podInfo.CreatedByName != "" && podInfo.Namespace != "" {
			replicaSetsToQuery[fmt.Sprintf("%s/%s", podInfo.Namespace, podInfo.CreatedByName)] = true
		}
	}

	// Step 5: Query ReplicaSet owners
	rsOwners, _ := e.queryReplicaSetOwners(k8sAccountID, replicaSetsToQuery)

	// Step 6: Process each pod and create edges/nodes
	processedOwners := make(map[string]bool)      // Track processed owner keys to avoid duplicates
	createdNodes := make(map[string]*core.DbNode) // Track nodes we've created in this run

	for _, podInfo := range podInfoMap {
		// Resolve owner (might be ReplicaSet -> Deployment)
		ownerKind := podInfo.CreatedByKind
		ownerName := podInfo.CreatedByName

		if ownerKind == "ReplicaSet" && ownerName != "" {
			rsKey := fmt.Sprintf("%s/%s", podInfo.Namespace, ownerName)
			if owner, found := rsOwners[rsKey]; found && owner.Kind != "" {
				ownerKind = owner.Kind
				ownerName = owner.Name
			} else {
				// Fallback: extract deployment name from RS pattern
				ownerName = extractDeploymentFromReplicaSet(ownerName)
				ownerKind = "Deployment"
			}
		}

		if ownerKind == "" || ownerName == "" {
			continue
		}

		// Create unique owner key to avoid duplicates
		ownerKey := fmt.Sprintf("%s:%s:%s", k8sAccountID, podInfo.Namespace, ownerName)
		if processedOwners[ownerKey] {
			continue // Already processed this owner
		}
		processedOwners[ownerKey] = true

		// IMPORTANT: Search existing nodes first
		var targetNode *core.DbNode
		serviceKey := fmt.Sprintf(keyFormat, k8sAccountID, podInfo.Namespace, ownerName)

		// 1. Try to find existing K8s Service
		if svc, found := k8sServicesByName[serviceKey]; found {
			targetNode = svc
			e.logger.Debug("Found existing K8s Service for LB target",
				"lb_name", getStringProp(lbNode, "name"),
				"service_name", ownerName,
				"namespace", podInfo.Namespace)
		}

		// 2. Try to find existing Workload
		if targetNode == nil {
			if wl, found := k8sWorkloadsByName[serviceKey]; found {
				targetNode = wl
				e.logger.Debug("Found existing Workload for LB target",
					"lb_name", getStringProp(lbNode, "name"),
					"workload_name", ownerName,
					"namespace", podInfo.Namespace)
			}
		}

		// 3. Check if we already created this node in this run
		if targetNode == nil {
			if created, found := createdNodes[ownerKey]; found {
				targetNode = created
			}
		}

		// 4. ONLY if no existing node found, create new Workload node
		if targetNode == nil {
			targetNode = e.createWorkloadNode(podInfo, ownerKind, ownerName, k8sAccountID, tenantID)
			newNodes = append(newNodes, targetNode)
			createdNodes[ownerKey] = targetNode
			e.logger.Info("Created new Workload node for LB target",
				"lb_name", getStringProp(lbNode, "name"),
				"owner_kind", ownerKind,
				"owner_name", ownerName,
				"namespace", podInfo.Namespace)
		}

		// Create edge from LB to target
		edge := e.createEdge(lbNode, targetNode, "prometheus_pod_mapping", tenantID)
		edge.Properties["pod_ip"] = podInfo.PodIP
		edge.Properties["pod_name"] = podInfo.PodName
		edge.Properties["owner_kind"] = ownerKind
		edge.Properties["owner_name"] = ownerName
		newEdges = append(newEdges, edge)

		e.logger.Info("Matched LoadBalancer to workload via Prometheus",
			"lb_name", getStringProp(lbNode, "name"),
			"owner_kind", ownerKind,
			"owner_name", ownerName,
			"namespace", podInfo.Namespace,
			"pod_ip", podInfo.PodIP)
	}

	return newNodes, newEdges
}
