package aws

import (
	"fmt"

	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

// autoScalingGroupSchema — AutoScalingGroup rolls up to NodeTypeComputeInstancePool
// (alongside EKS/GKE/AKS node pools + Karpenter). Nodes + membership edges are
// synthesized from the autoscaling CLI (ASGs aren't collected into cloud_resourses).
var autoScalingGroupSchema = core.SpecificTypeSchema{
	SpecificType: "AutoScalingGroup",
	NodeType:     core.NodeTypeComputeInstancePool,
	Properties: []core.PropertyDef{
		{Name: "min_size", Indexed: true},
		{Name: "max_size", Indexed: true},
		{Name: "desired_capacity", Indexed: true},
		{Name: "provisioned_by", Indexed: true},
	},
}

func init() { core.RegisterSpecificTypeSchema(autoScalingGroupSchema) }

// asgData is the shaped `aws autoscaling describe-auto-scaling-groups --query` row.
type asgData struct {
	Name      string   `json:"name"`
	Min       int      `json:"min"`
	Max       int      `json:"max"`
	Desired   int      `json:"desired"`
	Instances []string `json:"instances"`
}

// createASGNodesAndEdges synthesizes AutoScalingGroup nodes + EC2 -BELONGS_TO-> ASG
// membership edges, fetched live per region via the autoscaling CLI (ASGs aren't in
// cloud_resourses).
func (s *AWSSource) createASGNodesAndEdges(reqCtx *security.RequestContext, ec2Nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	regions := make(map[string]struct{})
	for _, n := range ec2Nodes {
		if r, ok := n.Properties["region"].(string); ok && r != "" {
			regions[r] = struct{}{}
		}
	}

	nodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)
	for region := range regions {
		asgs := s.fetchAutoScalingGroups(reqCtx, req.CloudAccountID, region)
		n, e := s.buildASGNodesAndEdges(asgs, region, lookup, req)
		nodes = append(nodes, n...)
		edges = append(edges, e...)
	}
	return nodes, edges
}

// fetchAutoScalingGroups returns the region's ASGs (name, capacity, member instance IDs)
// via `aws autoscaling describe-auto-scaling-groups`. Best-effort.
func (s *AWSSource) fetchAutoScalingGroups(reqCtx *security.RequestContext, accountID, region string) []asgData {
	cmd := fmt.Sprintf(
		"aws autoscaling describe-auto-scaling-groups --region %s --query 'AutoScalingGroups[].{name:AutoScalingGroupName,min:MinSize,max:MaxSize,desired:DesiredCapacity,instances:Instances[].InstanceId}' --output json",
		region,
	)
	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{AccountID: accountID, Command: cmd}, 2)
	if err != nil {
		s.logger.Debug("autoscaling describe-auto-scaling-groups failed", "region", region, "error", err)
		return nil
	}
	var asgs []asgData
	if !parseAWSCliData(resp, &asgs) {
		return nil
	}
	return asgs
}

// buildASGNodesAndEdges is the pure (network-free) builder: one ComputeInstancePool
// node per ASG + each member EC2 instance (that resolves to a node) -BELONGS_TO-> it.
func (s *AWSSource) buildASGNodesAndEdges(asgs []asgData, region string, lookup *sources.NodeLookup, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	nodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)

	for _, asg := range asgs {
		if asg.Name == "" {
			continue
		}
		props := map[string]interface{}{
			"name":             asg.Name,
			"region":           region,
			"min_size":         asg.Min,
			"max_size":         asg.Max,
			"desired_capacity": asg.Desired,
			"provisioned_by":   "aws_autoscaling",
			"specific_type":    "AutoScalingGroup",
		}
		uniqueKey := core.BuildUniqueKey(core.CloudProviderAWS, req.CloudAccountID, region, core.NodeTypeComputeInstancePool, "", asg.Name)
		asgNode := core.NewNode(core.NodeTypeComputeInstancePool, uniqueKey, props, req.TenantID, req.CloudAccountID, "aws")
		nodes = append(nodes, asgNode)

		for _, instanceID := range asg.Instances {
			if instNode, ok := lookup.ByResourceID[instanceID]; ok {
				edges = append(edges, s.createEdge(instNode, asgNode, core.RelationshipBelongsTo,
					map[string]interface{}{"connection_type": "auto_scaling_group"}, req))
			}
		}
	}

	return nodes, edges
}
