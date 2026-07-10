package aws

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"strings"
)

// eksClusterSchema — concrete schema for EKS clusters.
// Fields written by extractClusterMetadata (shared with ECS, see ecs.go).
var eksClusterSchema = core.SpecificTypeSchema{
	SpecificType: "EKSCluster",
	NodeType:     core.NodeTypeManagedCluster,
	Properties: []core.PropertyDef{
		{Name: "cluster_version", Indexed: true},
		{Name: "cluster_endpoint", Indexed: true},
		{Name: "cluster_status", Indexed: true},
		{Name: "vpc_id", Indexed: true},
		{Name: "dns_name", Indexed: true},
		{Name: "role_arn"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "created_at"},
		{Name: "endpoint"},
		{Name: "cluster_endpoint_public"},
		{Name: "exposed_internet"},
		{Name: "version"},
		{Name: "platform_version"},
		{Name: "cluster_logging"},
		{Name: "certificate_authority_data_present"},
		{Name: "certificate_authority_parse_status"},
		{Name: "certificate_authority_parse_error"},
		{Name: "certificate_authority_sha256_fingerprint"},
		{Name: "certificate_authority_subject"},
		{Name: "certificate_authority_issuer"},
		{Name: "certificate_authority_not_before"},
		{Name: "certificate_authority_not_after"},
		{Name: "certificate_authority_subject_key_identifier"},
		{Name: "certificate_authority_authority_key_identifier"},
	},
}

// eksNodeGroupSchema — EKS managed node groups (NodeTypeComputeInstancePool). Node
// group attributes are read from cached meta during edge building rather than lifted
// into properties at creation, so no resource-specific fields are declared.
var eksNodeGroupSchema = core.SpecificTypeSchema{
	SpecificType: "EKSNodeGroup",
	NodeType:     core.NodeTypeComputeInstancePool,
	Properties:   []core.PropertyDef{},
}

// eksPodSchema — EKS-reported pods (NodeTypePod). namespace/cluster are set by later
// enrichment, not at AWS-source creation time.
var eksPodSchema = core.SpecificTypeSchema{
	SpecificType: "EKSPod",
	NodeType:     core.NodeTypePod,
	Properties:   []core.PropertyDef{},
}

func init() {
	core.RegisterSpecificTypeSchema(eksClusterSchema)
	core.RegisterSpecificTypeSchema(eksNodeGroupSchema)
	core.RegisterSpecificTypeSchema(eksPodSchema)
}

// extractClusterMetadata extracts essential fields for EKS/ECS clusters
func (s *AWSSource) extractClusterMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Cluster endpoint (important for API access)
	if endpoint, ok := metaMap["Endpoint"].(string); ok && endpoint != "" {
		properties["cluster_endpoint"] = endpoint
		// Mirror onto dns_name (host portion only) so the ExternalService
		// enricher's dns_name index hits when eBPF observes traffic to the
		// cluster API server.
		if host := sources.RepoURIHost(endpoint); host != "" {
			if existing, _ := properties["dns_name"].(string); existing == "" {
				properties["dns_name"] = strings.ToLower(host)
			}
		}
	}

	// Version (important for compatibility)
	if version, ok := metaMap["Version"].(string); ok && version != "" {
		properties["cluster_version"] = version
	}

	// Status (important for operational state)
	if status, ok := metaMap["Status"].(string); ok && status != "" {
		properties["cluster_status"] = status
	}

	// IAM cluster role ARN (used by createEKSEdges to wire RUNS_AS → ServiceIdentity)
	if roleArn, ok := metaMap["RoleArn"].(string); ok && roleArn != "" {
		properties["role_arn"] = roleArn
	}
}

// getEKSMetaFromCache reads EKS-cluster meta from the per-build cache.
func (s *AWSSource) getEKSMetaFromCache(node *core.DbNode) (map[string]interface{}, bool) {
	return s.metaFromCache(node, "cluster")
}

// createEKSEdges creates edges for EKS clusters
func (s *AWSSource) createEKSEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		meta, hasMeta := s.getEKSMetaFromCache(node)
		if !hasMeta {
			continue
		}

		// RUNS_AS → ServiceIdentity (cluster IAM role)
		if roleArn, ok := node.Properties["role_arn"].(string); ok && roleArn != "" {
			if roleNode, exists := lookup.ByARN[roleArn]; exists {
				edges = append(edges, s.createEdge(node, roleNode, core.RelationshipRunsAs,
					map[string]interface{}{"connection_type": "cluster_role"}, req))
			}
		}

		// Parse ResourcesVpcConfig which contains VPC, subnet, and security group information
		if resourcesVpcConfig, ok := meta["ResourcesVpcConfig"].(map[string]interface{}); ok {
			// 1. EKS → VPC relationship
			if vpcID, ok := resourcesVpcConfig["VpcId"].(string); ok && vpcID != "" {
				if vpcNode, exists := lookup.ByResourceID[vpcID]; exists {
					// Extract additional VPC config details for edge properties
					endpointPublicAccess, _ := resourcesVpcConfig["EndpointPublicAccess"].(bool)
					endpointPrivateAccess, _ := resourcesVpcConfig["EndpointPrivateAccess"].(bool)

					edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
						map[string]interface{}{
							"connection_type":         "vpc",
							"endpoint_public_access":  endpointPublicAccess,
							"endpoint_private_access": endpointPrivateAccess,
						}, req))
				}
			}

			// 2. EKS → Subnet relationships (EKS clusters span multiple subnets for HA)
			if subnetIds, ok := resourcesVpcConfig["SubnetIds"].([]interface{}); ok {
				for _, subnetID := range subnetIds {
					if subnetIDStr, ok := subnetID.(string); ok && subnetIDStr != "" {
						if subnetNode, exists := lookup.ByResourceID[subnetIDStr]; exists {
							edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
								map[string]interface{}{
									"connection_type": "subnet",
								}, req))
						}
					}
				}
			}

			// 3. EKS → Security Group relationships
			// Collect all security group IDs (both SecurityGroupIds array and ClusterSecurityGroupId)
			securityGroupIDs := make([]string, 0)

			// Add SecurityGroupIds array
			if sgIds, ok := resourcesVpcConfig["SecurityGroupIds"].([]interface{}); ok {
				for _, sgID := range sgIds {
					if sgIDStr, ok := sgID.(string); ok && sgIDStr != "" {
						securityGroupIDs = append(securityGroupIDs, sgIDStr)
					}
				}
			}

			// Add ClusterSecurityGroupId
			if clusterSGID, ok := resourcesVpcConfig["ClusterSecurityGroupId"].(string); ok && clusterSGID != "" {
				securityGroupIDs = append(securityGroupIDs, clusterSGID)
			}

			// Create edges for all security groups
			for _, sgID := range securityGroupIDs {
				if sgNode, exists := lookup.ByResourceID[sgID]; exists {
					// Determine if this is the cluster security group
					isClusterSG := false
					if clusterSGID, ok := resourcesVpcConfig["ClusterSecurityGroupId"].(string); ok {
						isClusterSG = (sgID == clusterSGID)
					}

					edges = append(edges, s.createEdge(node, sgNode, core.RelationshipHostedOn,
						map[string]interface{}{
							"connection_type": "security_group",
							"cluster_sg":      isClusterSG,
						}, req))
				}
			}
		}
	}

	return edges
}

// getEKSNodeGroupMetaFromCache reads EKS NodeGroup meta from the per-build cache.
func (s *AWSSource) getEKSNodeGroupMetaFromCache(node *core.DbNode) (map[string]interface{}, bool) {
	return s.metaFromCache(node, "nodegroup")
}

// createEKSNodeGroupEdges wires EKS managed NodeGroups to the rest of the
// graph: BELONGS_TO the parent ManagedCluster and HOSTED_ON each Subnet the
// node group spans. The MANAGES → ComputeInstance edges are left as a
// follow-up because resolving group → EC2 requires a separate
// DescribeAutoScalingGroup call which isn't yet plumbed.
func (s *AWSSource) createEKSNodeGroupEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Only handle AWS EKS NodeGroups; ComputeInstancePool is also used by GCP/AKS.
		if svc, _ := node.Properties["service_name"].(string); svc != "AmazonEKS" {
			continue
		}

		meta, hasMeta := s.getEKSNodeGroupMetaFromCache(node)
		if !hasMeta {
			continue
		}

		// BELONGS_TO → parent EKS ManagedCluster (looked up by resource_id which
		// equals the cluster name for AmazonEKS rows).
		if clusterName, ok := meta["ClusterName"].(string); ok && clusterName != "" {
			if clusterNode, exists := lookup.ByResourceID[clusterName]; exists {
				edges = append(edges, s.createEdge(node, clusterNode, core.RelationshipBelongsTo,
					map[string]interface{}{"connection_type": "eks_nodegroup"}, req))
			}
		}

		// HOSTED_ON → each subnet the node group spans (EKS NodeGroups can be
		// multi-AZ for HA).
		if subnets, ok := meta["Subnets"].([]interface{}); ok {
			for _, subnetID := range subnets {
				subnetStr, ok := subnetID.(string)
				if !ok || subnetStr == "" {
					continue
				}
				if subnetNode, exists := lookup.ByResourceID[subnetStr]; exists {
					edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
						map[string]interface{}{"connection_type": "subnet"}, req))
				}
			}
		}
	}

	return edges
}
