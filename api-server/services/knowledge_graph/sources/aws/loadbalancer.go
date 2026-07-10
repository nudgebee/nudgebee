package aws

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
	"strings"
)

// loadBalancerSchemaProps — concrete properties written by extractLoadBalancerMetadata
// (+ common vpc_id / security_groups / availability_zone). Shared by the ELB/ALB/NLB
// schemas.
var loadBalancerSchemaProps = []core.PropertyDef{
	{Name: "load_balancer_type", Indexed: true},
	{Name: "scheme", Indexed: true},
	{Name: "dns_name", Indexed: true},
	{Name: "vpc_id", Indexed: true},
	{Name: "availability_zone", Indexed: true},
	{Name: "is_public_entry"},
	{Name: "subnets"},
	{Name: "instances"},
	{Name: "security_groups"},
}

// elbClassicParityProps — Additional provider fields (not yet emitted by our extractor)
// for the classic ELB model.
var elbClassicParityProps = []core.PropertyDef{
	{Name: "canonical_hosted_zone_name"},
	{Name: "canonical_hosted_zone_name_id"},
	{Name: "created_time"},
}

// elbV2ParityProps — Additional provider fields (not yet emitted by our extractor) for
// the ALB/NLB v2 model.
var elbV2ParityProps = []core.PropertyDef{
	{Name: "load_balancer_name"},
	{Name: "canonical_hosted_zone_id"},
	{Name: "exposed_internet"},
	{Name: "created_time"},
}

var elbLoadBalancerSchema = core.SpecificTypeSchema{
	SpecificType: "ELBLoadBalancer",
	NodeType:     core.NodeTypeLoadBalancer,
	Properties:   append(append([]core.PropertyDef{}, loadBalancerSchemaProps...), elbClassicParityProps...),
}

var albLoadBalancerSchema = core.SpecificTypeSchema{
	SpecificType: "ALBLoadBalancer",
	NodeType:     core.NodeTypeLoadBalancer,
	Properties:   append(append([]core.PropertyDef{}, loadBalancerSchemaProps...), elbV2ParityProps...),
}

var nlbLoadBalancerSchema = core.SpecificTypeSchema{
	SpecificType: "NLBLoadBalancer",
	NodeType:     core.NodeTypeLoadBalancer,
	Properties:   append(append([]core.PropertyDef{}, loadBalancerSchemaProps...), elbV2ParityProps...),
}

// elbTargetGroupSchema — target groups (NodeTypeBackendPool). Real rows carry only
// base + common (vpc_id); protocol/port/target_type/health-check fields require a
// live describe-target-groups call and are declared here for parity/filtering.
var elbTargetGroupSchema = core.SpecificTypeSchema{
	SpecificType: "ELBTargetGroup",
	NodeType:     core.NodeTypeBackendPool,
	Properties: []core.PropertyDef{
		{Name: "vpc_id", Indexed: true},
		{Name: "protocol", Indexed: true},
		{Name: "port", Indexed: true},
		{Name: "target_type", Indexed: true},
		{Name: "health_check_enabled", Indexed: true},
		{Name: "health_check_protocol", Indexed: true},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "target_group_name"},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(elbLoadBalancerSchema)
	core.RegisterSpecificTypeSchema(albLoadBalancerSchema)
	core.RegisterSpecificTypeSchema(nlbLoadBalancerSchema)
	core.RegisterSpecificTypeSchema(elbTargetGroupSchema)
}

// extractLoadBalancerMetadata extracts essential fields for load balancers
func (s *AWSSource) extractLoadBalancerMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// DNS name (critical for routing)
	if dnsName, ok := metaMap["DNSName"].(string); ok && dnsName != "" {
		properties["dns_name"] = dnsName
	}

	// Scheme (important for accessibility)
	if scheme, ok := metaMap["Scheme"].(string); ok && scheme != "" {
		properties["scheme"] = scheme
		// scheme="internet-facing" means the LB has a public DNS name reachable from the internet;
		// scheme="internal" means it's only reachable inside the VPC.
		properties["is_public_entry"] = scheme == "internet-facing"
	}

	// Type (important for capabilities) - ELBv2 only
	if lbType, ok := metaMap["Type"].(string); ok && lbType != "" {
		properties["load_balancer_type"] = lbType
	}

	// State code (active, provisioning, failed)
	if state, ok := metaMap["State"].(map[string]interface{}); ok {
		if code, ok := state["Code"].(string); ok && code != "" {
			properties["state"] = code
		}
	}

	// Subnets for edge creation:
	// Classic ELB: direct "Subnets" list of IDs
	// NLB/ALB: "AvailabilityZones" array of objects with "SubnetId"
	if subnets, ok := metaMap["Subnets"].([]interface{}); ok && len(subnets) > 0 {
		properties["subnets"] = subnets
	} else if azs, ok := metaMap["AvailabilityZones"].([]interface{}); ok && len(azs) > 0 {
		subnetList := make([]interface{}, 0, len(azs))
		for _, az := range azs {
			if azMap, ok := az.(map[string]interface{}); ok {
				if subnetID, ok := azMap["SubnetId"].(string); ok && subnetID != "" {
					subnetList = append(subnetList, subnetID)
				}
			}
		}
		if len(subnetList) > 0 {
			properties["subnets"] = subnetList
		}
	}

	// Instances (Classic ELB) — stored for edge creation without CLI
	if instances, ok := metaMap["Instances"].([]interface{}); ok && len(instances) > 0 {
		properties["instances"] = instances
	}
}

// createLoadBalancerEdges creates edges for Load Balancers (ALB/NLB/CLB)
// Uses metadata stored in cloud_resourses table (meta + tags columns) for edge creation.
// All input nodes are returned unchanged — no filtering occurs.
// Returns all LB nodes (identical to input), BackendPool nodes (for Target Groups), and edges.
func (s *AWSSource) createLoadBalancerEdges(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbNode, []*core.DbEdge) {
	edges := make([]*core.DbEdge, 0)
	backendPoolNodes := make([]*core.DbNode, 0)

	for _, node := range nodes {
		// 1. LB → VPC edge
		if vpcID, ok := node.Properties["vpc_id"].(string); ok && vpcID != "" {
			if vpcNode, exists := lookup.ByResourceID[vpcID]; exists {
				scheme, _ := node.Properties["scheme"].(string)
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc", "scheme": scheme}, req))
			}
		}

		// 2. LB → Subnet edges
		if subnets, ok := node.Properties["subnets"].([]interface{}); ok {
			for _, subnet := range subnets {
				if subnetID, ok := subnet.(string); ok && subnetID != "" {
					if subnetNode, exists := lookup.ByResourceID[subnetID]; exists {
						edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
							map[string]interface{}{"connection_type": "subnet"}, req))
					}
				}
			}
		}

		// 3. LB → Security Group edges
		if secGroups, ok := node.Properties["security_groups"].([]interface{}); ok {
			for _, sg := range secGroups {
				if sgID, ok := sg.(string); ok && sgID != "" {
					if sgNode, exists := lookup.ByResourceID[sgID]; exists {
						edges = append(edges, s.createEdge(node, sgNode, core.RelationshipHostedOn,
							map[string]interface{}{"connection_type": "security_group"}, req))
					}
				}
			}
		}

		// 4. Classic ELB: Instance edges (from meta.Instances stored in node properties)
		// Note: NLB/ALB target group edges (BackendPool nodes) require live AWS CLI access
		// because target groups are NOT embedded in the LB resource in DB meta.
		// They are kept on CLI via fetchTargetGroupsForLoadBalancer / fetchTargetHealthForTargetGroup.
		if instances, ok := node.Properties["instances"].([]interface{}); ok {
			for _, instRaw := range instances {
				instMap, ok := instRaw.(map[string]interface{})
				if !ok {
					continue
				}
				instanceID, _ := instMap["InstanceId"].(string)
				if instanceID == "" {
					continue
				}
				if instanceNode, exists := lookup.ByResourceID[instanceID]; exists {
					edges = append(edges, s.createEdge(node, instanceNode, core.RelationshipRoutesTo,
						map[string]interface{}{"connection_type": "instance_target"}, req))
				}
			}
		}

		// 4b. ALB/NLB (v2): registered instance targets are NOT in DB meta — fetch the
		// target groups + health live via the elbv2 CLI (through the collector).
		if st := node.SpecificType; st == "ALBLoadBalancer" || st == "NLBLoadBalancer" {
			edges = append(edges, s.createLoadBalancerV2TargetEdges(reqCtx, node, lookup, req)...)
		}

		// 5. LB → EKS Cluster (via kubernetes tags stored in node.Properties["labels"])
		s.linkLoadBalancerToEKSCluster(node, map[string]interface{}{}, lookup, req, &edges)

		// 6. Extract K8s service info from tags for cross-source matching
		s.extractK8sServiceFromLBTags(node, map[string]interface{}{})
	}

	s.logger.Info("Created Load Balancer edges from metadata",
		"lb_count", len(nodes),
		"backend_pool_count", len(backendPoolNodes),
		"edges_created", len(edges))

	return nodes, backendPoolNodes, edges
}

// createLoadBalancerV2TargetEdges builds ALB/NLB → EC2 instance (ROUTES_TO,
// connection_type "instance_target") edges for the instances registered in the load
// balancer's target groups — the cloud-native ingress→backend path that classic ELBs
// already get from meta.instances. The target-group membership isn't in the LB's DB
// meta, so it's fetched live via the elbv2 CLI (through the collector). IP-type targets
// (ECS/Fargate/k8s) are out of scope here — those are resolved by the LB↔k8s enricher.
func (s *AWSSource) createLoadBalancerV2TargetEdges(reqCtx *security.RequestContext, lbNode *core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	arn, _ := lbNode.Properties["arn"].(string)
	region, _ := lbNode.Properties["region"].(string)
	if arn == "" || region == "" {
		return edges
	}

	instanceIDs := s.fetchLBTargetInstanceIDs(reqCtx, lbNode.CloudAccountID, region, arn)
	return s.buildLBTargetEdges(lbNode, instanceIDs, lookup, req)
}

// buildLBTargetEdges is the pure (network-free) edge builder: LB → ComputeInstance
// (ROUTES_TO, instance_target) for each registered instance ID that resolves to a node.
func (s *AWSSource) buildLBTargetEdges(lbNode *core.DbNode, instanceIDs []string, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)
	for _, instanceID := range instanceIDs {
		if instanceNode, exists := lookup.ByResourceID[instanceID]; exists {
			edges = append(edges, s.createEdge(lbNode, instanceNode, core.RelationshipRoutesTo,
				map[string]interface{}{"connection_type": "instance_target"}, req))
		}
	}
	return edges
}

// fetchLBTargetInstanceIDs returns the distinct EC2 instance IDs registered as targets
// of a v2 load balancer, via two elbv2 CLI calls: describe-target-groups (to list the
// LB's instance-type target groups) then describe-target-health per group. Best-effort:
// CLI errors are logged and skipped.
func (s *AWSSource) fetchLBTargetInstanceIDs(reqCtx *security.RequestContext, accountID, region, lbARN string) []string {
	tgCmd := fmt.Sprintf(
		"aws elbv2 describe-target-groups --load-balancer-arn %s --region %s --query 'TargetGroups[].[TargetGroupArn,TargetType]' --output json",
		lbARN, region,
	)
	tgResp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{AccountID: accountID, Command: tgCmd}, 2)
	if err != nil {
		s.logger.Debug("elbv2 describe-target-groups failed", "lb_arn", lbARN, "error", err)
		return nil
	}
	var targetGroups [][]string
	if !parseAWSCliData(tgResp, &targetGroups) {
		return nil
	}

	instanceSet := make(map[string]struct{})
	for _, tg := range targetGroups {
		if len(tg) != 2 || tg[1] != "instance" { // only instance-type target groups
			continue
		}
		thCmd := fmt.Sprintf(
			"aws elbv2 describe-target-health --target-group-arn %s --region %s --query 'TargetHealthDescriptions[].Target.Id' --output json",
			tg[0], region,
		)
		thResp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{AccountID: accountID, Command: thCmd}, 2)
		if err != nil {
			s.logger.Debug("elbv2 describe-target-health failed", "tg_arn", tg[0], "error", err)
			continue
		}
		var ids []string
		if !parseAWSCliData(thResp, &ids) {
			continue
		}
		for _, id := range ids {
			if strings.HasPrefix(id, "i-") { // instance IDs (not IP targets)
				instanceSet[id] = struct{}{}
			}
		}
	}

	out := make([]string, 0, len(instanceSet))
	for id := range instanceSet {
		out = append(out, id)
	}
	return out
}

// parseAWSCliData extracts the CLI stdout (resp["data"]) from a collector execute_cli
// response and unmarshals its JSON into target. Returns false if absent/non-JSON.
func parseAWSCliData(resp map[string]any, target interface{}) bool {
	data, ok := resp["data"].(string)
	if !ok {
		return false
	}
	data = strings.TrimSpace(data)
	if data == "" || (!strings.HasPrefix(data, "{") && !strings.HasPrefix(data, "[")) {
		return false
	}
	return json.Unmarshal([]byte(data), target) == nil
}

// linkLoadBalancerToEKSCluster creates an edge from LoadBalancer to EKS cluster based on Kubernetes tags
func (s *AWSSource) linkLoadBalancerToEKSCluster(node *core.DbNode, meta map[string]interface{}, lookup *sources.NodeLookup, req *core.SourceBuildRequest, edges *[]*core.DbEdge) {
	var clusterName string
	var ownership string // "owned" or "shared"

	// Try to get tags from node properties first (may have been stored from cloud_resources)
	tags, hasTags := node.Properties["labels"].(map[string]interface{})
	if !hasTags {
		// Try tags field
		tags, hasTags = node.Properties["tags"].(map[string]interface{})
	}

	if hasTags {
		// Check for elbv2.k8s.aws/cluster tag (set by AWS Load Balancer Controller)
		if cluster := extractTagStringValue(tags["elbv2.k8s.aws/cluster"]); cluster != "" {
			clusterName = cluster
			ownership = "owned"
		}

		// Check for eks:eks-cluster-name tag (set by EKS for NLBs)
		if clusterName == "" {
			if cluster := extractTagStringValue(tags["eks:eks-cluster-name"]); cluster != "" {
				clusterName = cluster
				ownership = "owned"
			}
		}

		// Check for kubernetes.io/cluster/{name} tags
		if clusterName == "" {
			for key, value := range tags {
				if strings.HasPrefix(key, "kubernetes.io/cluster/") {
					clusterName = strings.TrimPrefix(key, "kubernetes.io/cluster/")
					ownership = extractTagStringValue(value)
					break
				}
			}
		}
	}

	// Also check metadata Tags array (from CLI fetch)
	if clusterName == "" {
		if metaTags, ok := meta["Tags"].([]interface{}); ok {
			for _, t := range metaTags {
				if tagMap, ok := t.(map[string]interface{}); ok {
					key, _ := tagMap["Key"].(string)
					value, _ := tagMap["Value"].(string)

					if key == "elbv2.k8s.aws/cluster" && value != "" {
						clusterName = value
						ownership = "owned"
						break
					}

					if strings.HasPrefix(key, "kubernetes.io/cluster/") {
						clusterName = strings.TrimPrefix(key, "kubernetes.io/cluster/")
						ownership = value
						break
					}
				}
			}
		}
	}

	eksNodes, hasEKS := lookup.ByNodeType[core.NodeTypeManagedCluster]
	if !hasEKS {
		return
	}

	if clusterName != "" {
		// Tag-based match (highest confidence)
		for _, eksNode := range eksNodes {
			eksName, _ := eksNode.Properties["name"].(string)
			if eksName == clusterName {
				*edges = append(*edges, s.createEdge(node, eksNode, core.RelationshipBelongsTo,
					map[string]interface{}{
						"connection_type": "kubernetes_cluster",
						"cluster_name":    clusterName,
						"ownership":       ownership,
					}, req))
				s.logger.Debug("created LoadBalancer -> EKS cluster edge via tags",
					"lb_name", node.Properties["name"],
					"cluster_name", clusterName,
					"ownership", ownership)
				return
			}
		}
		s.logger.Debug("LoadBalancer has Kubernetes tags but no matching EKS cluster found",
			"lb_name", node.Properties["name"],
			"cluster_name", clusterName)
		return
	}

	// No k8s tags — fallback: match by VPC co-location (low confidence).
	// If exactly one EKS cluster shares the same VPC as the LB, infer the relationship.
	lbVPCID, _ := node.Properties["vpc_id"].(string)
	if lbVPCID == "" {
		return
	}

	var matchedEKS *core.DbNode
	for _, eksNode := range eksNodes {
		// Only consider AWS EKS clusters
		if svc, _ := eksNode.Properties["service_name"].(string); svc != "AmazonEKS" {
			continue
		}
		eksVPCID, _ := eksNode.Properties["vpc_id"].(string)
		if eksVPCID != "" && eksVPCID == lbVPCID {
			if matchedEKS != nil {
				// Multiple EKS clusters in the same VPC — ambiguous, skip
				s.logger.Debug("Multiple EKS clusters in same VPC, skipping VPC-based LB → EKS inference",
					"lb_name", node.Properties["name"],
					"vpc_id", lbVPCID)
				return
			}
			matchedEKS = eksNode
		}
	}

	if matchedEKS != nil {
		eksName, _ := matchedEKS.Properties["name"].(string)
		*edges = append(*edges, s.createEdge(node, matchedEKS, core.RelationshipBelongsTo,
			map[string]interface{}{
				"connection_type": "vpc_inferred",
				"cluster_name":    eksName,
				"confidence":      "low",
			}, req))
		s.logger.Debug("created LoadBalancer -> EKS cluster edge via VPC co-location",
			"lb_name", node.Properties["name"],
			"cluster_name", eksName,
			"vpc_id", lbVPCID)
	}
}

// extractTagStringValue extracts a string value from a tag entry.
// Handles both plain string values and the array format used by the cloud_resourses.tags column
// (e.g. {"key": ["value"]} → "value", {"key": "value"} → "value").
func extractTagStringValue(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	if arr, ok := v.([]interface{}); ok && len(arr) > 0 {
		if s, ok := arr[0].(string); ok {
			return s
		}
	}
	return ""
}

// extractK8sServiceFromLBTags extracts Kubernetes service info from LoadBalancer tags
// and stores k8s_service_name, k8s_service_namespace, and k8s_cluster_name properties for cross-source matching.
// AWS Load Balancer Controller sets:
// - kubernetes.io/service-name tag in format "namespace/service-name"
// - elbv2.k8s.aws/cluster or kubernetes.io/cluster/{cluster-name} tags for cluster identification
// EKS also sets:
// - eks:eks-cluster-name tag (cluster name)
// - service.eks.amazonaws.com/stack tag in format "namespace/service-name"
func (s *AWSSource) extractK8sServiceFromLBTags(node *core.DbNode, meta map[string]interface{}) {
	var k8sServiceName, k8sNamespace, k8sClusterName string

	// Try to get tags from node properties first (may have been stored from cloud_resources)
	tags, hasTags := node.Properties["labels"].(map[string]interface{})
	if !hasTags {
		tags, hasTags = node.Properties["tags"].(map[string]interface{})
	}

	if hasTags {
		// Check for kubernetes.io/service-name tag (format: "namespace/service-name")
		if serviceName := extractTagStringValue(tags["kubernetes.io/service-name"]); serviceName != "" {
			parts := strings.Split(serviceName, "/")
			if len(parts) == 2 {
				k8sNamespace = parts[0]
				k8sServiceName = parts[1]
			}
		}

		// Check for service.eks.amazonaws.com/stack tag (EKS NLB format: "namespace/service-name")
		if k8sServiceName == "" {
			if stack := extractTagStringValue(tags["service.eks.amazonaws.com/stack"]); stack != "" {
				parts := strings.Split(stack, "/")
				if len(parts) == 2 {
					k8sNamespace = parts[0]
					k8sServiceName = parts[1]
				}
			}
		}

		// Check for cluster name from elbv2.k8s.aws/cluster tag
		if cluster := extractTagStringValue(tags["elbv2.k8s.aws/cluster"]); cluster != "" {
			k8sClusterName = cluster
		}

		// Check for cluster name from eks:eks-cluster-name tag (EKS NLB format)
		if k8sClusterName == "" {
			if cluster := extractTagStringValue(tags["eks:eks-cluster-name"]); cluster != "" {
				k8sClusterName = cluster
			}
		}

		// Check for cluster name from kubernetes.io/cluster/{name} tags
		if k8sClusterName == "" {
			for key := range tags {
				if strings.HasPrefix(key, "kubernetes.io/cluster/") {
					k8sClusterName = strings.TrimPrefix(key, "kubernetes.io/cluster/")
					break
				}
			}
		}
	}

	// Also check metadata Tags array (from CLI fetch)
	if metaTags, ok := meta["Tags"].([]interface{}); ok {
		for _, t := range metaTags {
			if tagMap, ok := t.(map[string]interface{}); ok {
				key, _ := tagMap["Key"].(string)
				value, _ := tagMap["Value"].(string)

				// Extract service name if not already found
				if k8sServiceName == "" && key == "kubernetes.io/service-name" && value != "" {
					parts := strings.Split(value, "/")
					if len(parts) == 2 {
						k8sNamespace = parts[0]
						k8sServiceName = parts[1]
					}
				}

				// Extract cluster name if not already found
				if k8sClusterName == "" {
					if key == "elbv2.k8s.aws/cluster" && value != "" {
						k8sClusterName = value
					} else if strings.HasPrefix(key, "kubernetes.io/cluster/") {
						k8sClusterName = strings.TrimPrefix(key, "kubernetes.io/cluster/")
					}
				}
			}
		}
	}

	if k8sServiceName != "" {
		node.Properties["k8s_service_name"] = k8sServiceName
		node.Properties["k8s_service_namespace"] = k8sNamespace
		if k8sClusterName != "" {
			node.Properties["k8s_cluster_name"] = k8sClusterName
		}
		s.logger.Debug("extracted K8s service info from LoadBalancer tags",
			"lb_name", node.Properties["name"],
			"k8s_service_name", k8sServiceName,
			"k8s_namespace", k8sNamespace,
			"k8s_cluster_name", k8sClusterName)
	}
}
