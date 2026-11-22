package aws

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

// Declaring AWS resources by hand: see sources/manual_node.go.

// awsManualNames is what the collector stores as name for the types where
// that is not obvious (read off cloud_resourses: EC2 and EBS rows carry the
// Name tag when there is one, EFS rows the file system id).
var awsManualNames = map[string][2]string{
	"EC2Instance":        {"Name", "The instance's Name tag, or its instance id (i-…) if it has none"},
	"EBSVolume":          {"Name", "The volume's Name tag, or its volume id (vol-…) if it has none"},
	"EFSFileSystem":      {"File system id", "The file system id (fs-…)"},
	"RDSInstance":        {"DB instance identifier", ""},
	"RDSCluster":         {"DB cluster identifier", ""},
	"RedshiftCluster":    {"Cluster identifier", ""},
	"ALBLoadBalancer":    {"Load balancer name", "The short name, not the ARN"},
	"NLBLoadBalancer":    {"Load balancer name", "The short name, not the ARN"},
	"ELBLoadBalancer":    {"Load balancer name", ""},
	"S3Bucket":           {"Bucket name", ""},
	"SQSQueue":           {"Queue name", ""},
	"SNSTopic":           {"Topic name", ""},
	"LambdaFunction":     {"Function name", ""},
	"DynamoDBTable":      {"Table name", ""},
	"ElastiCacheCluster": {"Cluster id", ""},
	"MSKCluster":         {"Cluster name", ""},
	"OpenSearchDomain":   {"Domain name", ""},
}

// awsManualNetworks are the types whose unique key carries their VPC: the
// default branch of GenerateUniqueKey keys a resource by the VPC its metadata
// names, and these are the types whose metadata names one. Load balancers and
// instances always have one; RDS and Redshift do when they sit in a subnet
// group, which is every modern deployment. TestManualNode_AWSKeyMatchesCollector
// holds each of them to the collector's key.
var awsManualNetworks = map[string]bool{
	"EC2Instance":     true,
	"ALBLoadBalancer": true,
	"NLBLoadBalancer": true,
	"ELBLoadBalancer": true,
	"RDSInstance":     false,
	"RDSCluster":      false,
	"RedshiftCluster": false,
}

func awsManualCandidates() []sources.ManualNodeEntry {
	var exact, fallback []sources.ManualNodeEntry
	for _, k := range kinds.Kinds() {
		if k.Suppress || k.Node != nil {
			continue
		}
		st := k.specificType()
		e := sources.ManualNodeEntry{SpecificType: st, NodeType: k.NodeType, ServiceName: k.Service}
		if names, ok := awsManualNames[st]; ok {
			e.NameLabel, e.NameHint = names[0], names[1]
		}
		if required, ok := awsManualNetworks[st]; ok {
			e.Network = core.NodeTypeVPC
			e.NetworkRequired = required
			e.NetworkHint = "The VPC the resource is in"
			if !required {
				e.NetworkHint = "The VPC the resource is in; leave empty only if it is not in one"
			}
		}
		if len(k.Types) > 0 {
			e.RowType = k.Types[0]
			exact = append(exact, e)
		} else {
			fallback = append(fallback, e)
		}
	}
	return append(exact, fallback...)
}

// applyAWSManualNetwork mirrors propagateVPCNamesToResources: the resource is
// keyed by the VPC node's name, falling back to its id.
func applyAWSManualNetwork(src *AWSSource) func(*core.DbNode, *core.ManualNodeNetwork) {
	return func(node *core.DbNode, network *core.ManualNodeNetwork) {
		node.Properties["vpc_id"] = network.ResourceID
		name := network.Name
		if name == "" {
			name = network.ResourceID
		}
		node.Properties["vpc_name_hierarchy"] = name
		node.UniqueKey = src.GenerateUniqueKey(node)
		node.ID = core.NodeIDFor(node.UniqueKey, node.TenantID, node.CloudAccountID)
	}
}

func init() {
	src, _ := NewAWSSource(AWSSourceConfig{}, nil)
	core.RegisterManualNodeBuilder("AWS", &sources.CloudManualNodeBuilder{
		CloudProvider: "AWS",
		Candidates:    awsManualCandidates,
		BuildRow:      src.createNodeFromResource,
		ApplyNetwork:  applyAWSManualNetwork(src),
	})
}
