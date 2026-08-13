package aws

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/knowledge_graph/sources/model"
)

// ec2InstanceSchema is the fixed concrete per-specific_type property schema for the
// EC2Instance specific_type. Names are the node.Properties keys written by
// extractComputeMetadata + extractCommonMetadataFields; universal base keys
// (name/arn/region/labels/...) are implicit (see core.universalBaseProperties).
// Indexed fields are hoisted into query_attributes for filtering.
var ec2InstanceSchema = core.SpecificTypeSchema{
	SpecificType: "EC2Instance",
	NodeType:     core.NodeTypeComputeInstance,
	Properties: []core.PropertyDef{
		{Name: "instance_type", Indexed: true},
		{Name: "instance_state", Indexed: true},
		{Name: "availability_zone", Indexed: true},
		{Name: "platform", Indexed: true},
		{Name: "architecture", Indexed: true},
		{Name: "image_id", Indexed: true},
		{Name: "vpc_id", Indexed: true},
		{Name: "subnet_id", Indexed: true},
		{Name: "private_ip", Indexed: true},
		{Name: "public_ip", Indexed: true},
		{Name: "private_dns_name"},
		{Name: "public_dns_name"},
		{Name: "iam_instance_profile_arn"},
		{Name: "launch_time"},
		{Name: "tenancy"},
		{Name: "ebs_optimized"},
		{Name: "monitoring_state"},
		{Name: "boot_mode"},
		{Name: "instance_lifecycle"},
		{Name: "security_groups"},
		{Name: "dns_name"},
		{Name: "kms_key_id"},
		{Name: "encrypted"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "private_ip_address"},
		{Name: "public_ip_address"},
		{Name: "launch_time_unix"},
		{Name: "iam_instance_profile"},
		{Name: "host_resource_group_arn"},
		{Name: "hibernation_option"},
		{Name: "metadata_http_tokens"},
		{Name: "metadata_http_put_response_hop_limit"},
		{Name: "metadata_http_endpoint"},
		{Name: "metadata_http_protocol_ipv6"},
		{Name: "metadata_instance_tags"},
		{Name: "imds_access_mode"},
		{Name: "imds_v1_enabled"},
		{Name: "imds_v2_required"},
		{Name: "exposed_internet"},
		{Name: "eks_cluster_name"},
		{Name: "ipv6_address"},
	},
}

func init() { core.RegisterSpecificTypeSchema(ec2InstanceSchema) }

// ssmManagedInstanceSchema is the concrete schema for SSM-managed instances
// (Systems Manager). They flow through the same NodeTypeComputeInstance path and
// extractComputeMetadata as EC2 instances, so the property keys match ec2InstanceSchema.
var ssmManagedInstanceSchema = core.SpecificTypeSchema{
	SpecificType: "SSMManagedInstance",
	NodeType:     core.NodeTypeComputeInstance,
	Properties: []core.PropertyDef{
		{Name: "instance_type", Indexed: true},
		{Name: "instance_state", Indexed: true},
		{Name: "availability_zone", Indexed: true},
		{Name: "platform", Indexed: true},
		{Name: "architecture", Indexed: true},
		{Name: "image_id", Indexed: true},
		{Name: "vpc_id", Indexed: true},
		{Name: "subnet_id", Indexed: true},
		{Name: "private_ip", Indexed: true},
		{Name: "public_ip", Indexed: true},
		{Name: "private_dns_name"},
		{Name: "public_dns_name"},
		{Name: "iam_instance_profile_arn"},
		{Name: "launch_time"},
		{Name: "tenancy"},
		{Name: "ebs_optimized"},
		{Name: "monitoring_state"},
		{Name: "boot_mode"},
		{Name: "instance_lifecycle"},
		{Name: "security_groups"},
		{Name: "dns_name"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "ping_status"},
		{Name: "last_ping_date_time"},
		{Name: "agent_version"},
		{Name: "is_latest_version"},
		{Name: "platform_type"},
		{Name: "platform_name"},
		{Name: "platform_version"},
		{Name: "activation_id"},
		{Name: "iam_role"},
		{Name: "registration_date"},
		{Name: "resource_type"},
		{Name: "ip_address"},
		{Name: "computer_name"},
		{Name: "association_status"},
		{Name: "last_association_execution_date"},
		{Name: "last_successful_association_execution_date"},
		{Name: "source_id"},
		{Name: "source_type"},
	},
}

func init() { core.RegisterSpecificTypeSchema(ssmManagedInstanceSchema) }

// extractComputeMetadata extracts essential fields for EC2 instances
func (s *AWSSource) extractComputeMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Instance type (important for capacity)
	if instanceType, ok := metaMap["InstanceType"].(string); ok && instanceType != "" {
		properties["instance_type"] = instanceType
	}

	// Private/Public IPs (important for connectivity)
	if privateIP, ok := metaMap["PrivateIpAddress"].(string); ok && privateIP != "" {
		properties["private_ip"] = privateIP
	}
	if publicIP, ok := metaMap["PublicIpAddress"].(string); ok && publicIP != "" {
		properties["public_ip"] = publicIP
	}

	// Private DNS Name (important for internal hostname resolution)
	if privateDnsName, ok := metaMap["PrivateDnsName"].(string); ok && privateDnsName != "" {
		properties["private_dns_name"] = privateDnsName
	}

	// State (important for operational status)
	if state, ok := metaMap["State"].(map[string]interface{}); ok {
		if stateName, ok := state["Name"].(string); ok && stateName != "" {
			properties["instance_state"] = stateName
		}
	}

	// Subnet ID (important for network topology)
	if subnetID, ok := metaMap["SubnetId"].(string); ok && subnetID != "" {
		properties["subnet_id"] = subnetID
	}

	// Availability Zone + Tenancy (from Placement - HA / dedicated-host analysis)
	if placement, ok := metaMap["Placement"].(map[string]interface{}); ok {
		if az, ok := placement["AvailabilityZone"].(string); ok && az != "" {
			properties["availability_zone"] = az
		}
		if tenancy, ok := placement["Tenancy"].(string); ok && tenancy != "" {
			properties["tenancy"] = tenancy
		}
	}

	// IAM instance profile ARN (needed for ServiceIdentity relationship)
	if instanceProfile, ok := metaMap["IamInstanceProfile"].(map[string]interface{}); ok {
		if profileARN, ok := instanceProfile["Arn"].(string); ok && profileARN != "" {
			properties["iam_instance_profile_arn"] = profileARN
		}
	}

	// Additional describe-instances fields for the EC2Instance schema
	// (all present in the raw meta; each guarded so absent fields are skipped).
	if imageID, ok := metaMap["ImageId"].(string); ok && imageID != "" {
		properties["image_id"] = imageID
	}
	if publicDNSName, ok := metaMap["PublicDnsName"].(string); ok && publicDNSName != "" {
		properties["public_dns_name"] = publicDNSName
	}
	if launchTime, ok := metaMap["LaunchTime"].(string); ok && launchTime != "" {
		properties["launch_time"] = launchTime
	}
	if architecture, ok := metaMap["Architecture"].(string); ok && architecture != "" {
		properties["architecture"] = architecture
	}
	if platform, ok := metaMap["Platform"].(string); ok && platform != "" {
		properties["platform"] = platform
	}
	if bootMode, ok := metaMap["BootMode"].(string); ok && bootMode != "" {
		properties["boot_mode"] = bootMode
	}
	if lifecycle, ok := metaMap["InstanceLifecycle"].(string); ok && lifecycle != "" {
		properties["instance_lifecycle"] = lifecycle
	}
	if ebsOptimized, ok := metaMap["EbsOptimized"].(bool); ok {
		properties["ebs_optimized"] = ebsOptimized
	}
	if monitoring, ok := metaMap["Monitoring"].(map[string]interface{}); ok {
		if mState, ok := monitoring["State"].(string); ok && mState != "" {
			properties["monitoring_state"] = mState
		}
	}
}

// createEC2Edges creates edges for EC2 instances
func (s *AWSSource) createEC2Edges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Use pre-extracted properties (set by extractCommonMetadataFields and extractComputeMetadata).
		// Raw meta is not stored in node.Properties, so we read the flattened fields.

		// 1. EC2 → VPC relationship
		if vpcID, ok := node.Properties["vpc_id"].(string); ok && vpcID != "" {
			if vpcNode, exists := lookup.ByResourceID[vpcID]; exists {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc"}, req))
			}
		}

		// 2. EC2 → Subnet relationship
		if subnetID, ok := node.Properties["subnet_id"].(string); ok && subnetID != "" {
			if subnetNode, exists := lookup.ByResourceID[subnetID]; exists {
				edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "subnet"}, req))
			}
		}

		// 3. EC2 → Security Group relationships
		// security_groups is stored as []interface{} with each element being {"GroupId": ..., "GroupName": ...}
		if securityGroups, ok := node.Properties["security_groups"].([]interface{}); ok {
			for _, sg := range securityGroups {
				if sgMap, ok := sg.(map[string]interface{}); ok {
					if groupID, ok := sgMap["GroupId"].(string); ok && groupID != "" {
						if sgNode, exists := lookup.ByResourceID[groupID]; exists {
							edges = append(edges, s.createEdge(node, sgNode, core.RelationshipHostedOn,
								map[string]interface{}{
									"connection_type": "security_group",
									"group_name":      sgMap["GroupName"],
								}, req))
						}
					}
				}
			}
		}

		// 4. EC2 → EKS Cluster relationship (if part of EKS node group)
		// Tags are stored in properties["labels"] as {"key": ["value"]} (values are arrays from the tags column).
		// EKS cluster tag key is "aws:eks:cluster-name" (or legacy "eks:cluster-name").
		if labels, ok := node.Properties["labels"].(map[string]interface{}); ok {
			eksClusterName := extractLabelValue(labels, "aws:eks:cluster-name")
			if eksClusterName == "" {
				eksClusterName = extractLabelValue(labels, "eks:cluster-name")
			}

			if eksClusterName != "" {
				for _, eksNode := range lookup.GetNodesByTypeAndName(core.NodeTypeManagedCluster, eksClusterName) {
					edges = append(edges, s.createEdge(node, eksNode, core.RelationshipRunsOn,
						map[string]interface{}{
							"connection_type": "eks_node",
							"cluster_name":    eksClusterName,
						}, req))
					break
				}
			}
		}
	}

	return edges
}

// awsEC2Rels is the declarative form of the mechanical EC2 attachment edges:
// instance HOSTED_ON its VPC, subnet, and each security group. The security-group
// case is a MapSliceSubKey — security_groups is []{"GroupId","GroupName"} — and
// carries the group_name onto the edge, matching createEC2Edges exactly.
var awsEC2Rels = []model.RelSpec{
	{
		Key:     "vpc_id",
		Shape:   model.Scalar,
		Lookup:  model.ByResourceID,
		RelType: core.RelationshipHostedOn,
		Props:   map[string]interface{}{"connection_type": "vpc"},
	},
	{
		Key:     "subnet_id",
		Shape:   model.Scalar,
		Lookup:  model.ByResourceID,
		RelType: core.RelationshipHostedOn,
		Props:   map[string]interface{}{"connection_type": "subnet"},
	},
	{
		Key:        "security_groups",
		Shape:      model.MapSliceSubKey,
		SubKey:     "GroupId",
		Lookup:     model.ByResourceID,
		RelType:    core.RelationshipHostedOn,
		Props:      map[string]interface{}{"connection_type": "security_group"},
		ExtraProps: map[string]string{"group_name": "GroupName"},
	},
}

// createEC2EKSEdges is the ESCAPE HATCH for the one EC2 edge that isn't a plain
// property→lookup rule: an instance tagged with an EKS cluster name RUNS_ON that
// managed cluster. It reads a label with a legacy-key fallback and matches the
// cluster by (type, name), so it stays hand-written (mirrors createEC2Edges tail).
func (s *AWSSource) createEC2EKSEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)
	for _, node := range nodes {
		labels, ok := node.Properties["labels"].(map[string]interface{})
		if !ok {
			continue
		}
		eksClusterName := extractLabelValue(labels, "aws:eks:cluster-name")
		if eksClusterName == "" {
			eksClusterName = extractLabelValue(labels, "eks:cluster-name")
		}
		if eksClusterName == "" {
			continue
		}
		for _, eksNode := range lookup.GetNodesByTypeAndName(core.NodeTypeManagedCluster, eksClusterName) {
			edges = append(edges, s.createEdge(node, eksNode, core.RelationshipRunsOn,
				map[string]interface{}{
					"connection_type": "eks_node",
					"cluster_name":    eksClusterName,
				}, req))
			break
		}
	}
	return edges
}

// createEC2EdgesViaEngine is the declarative-engine equivalent of createEC2Edges:
// the three mechanical attachment edges via the engine + the EKS escape hatch.
func (s *AWSSource) createEC2EdgesViaEngine(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := model.BuildEdges(nodes, awsEC2Rels, lookup, awsEdgeFactory(req))
	edges = append(edges, s.createEC2EKSEdges(nodes, lookup, req)...)
	return edges
}
