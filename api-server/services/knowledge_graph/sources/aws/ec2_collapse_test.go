package aws

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

func computeNode(id, specificType string, props map[string]interface{}) *core.DbNode {
	n := rdsTestNode(id, core.NodeTypeComputeInstance, props)
	n.SpecificType = specificType
	return n
}

// The defect this guards: EC2 describe and Systems Manager inventory both report
// the same instance, both map to NodeTypeComputeInstance, and their unique keys
// differ (Name tag + VPC vs instance id + no VPC). That split the instance's
// edges — the SSM node received the load balancer's ROUTES_TO while the EC2 node
// received the VPC-flow-log CALLS chain — so a blast radius seeded on the load
// balancer dead-ended on a node with no outgoing traffic.
func TestCollapseComputeInstanceDuplicates_SSMFoldsIntoEC2(t *testing.T) {
	ssm := computeNode("ssm-node", "SSMManagedInstance", map[string]interface{}{
		"name":          "i-0f568ef22d52139bb",
		"resource_id":   "i-0f568ef22d52139bb",
		"arn":           "arn:aws:ssm:us-east-1:864186153326:managed-instance/i-0f568ef22d52139bb",
		"ping_status":   "Online",
		"agent_version": "3.2.1",
	})
	ec2 := computeNode("ec2-node", "EC2Instance", map[string]interface{}{
		"name":        "nb-demo-web",
		"resource_id": "i-0f568ef22d52139bb",
		"arn":         "arn:aws:ec2:us-east-1:864186153326:instance/i-0f568ef22d52139bb",
		"vpc_id":      "vpc-06f8c85f523a3e52c",
		"private_ip":  "172.31.0.145",
	})
	other := computeNode("other-node", "EC2Instance", map[string]interface{}{
		"name":        "nb-demo-api",
		"resource_id": "i-0fdb1cda6f2773c24",
		"private_ip":  "172.31.1.149",
	})

	// SSM first, so the survivor is chosen by specific_type and not by input order.
	got := collapseComputeInstanceDuplicates([]*core.DbNode{ssm, ec2, other})

	if len(got) != 2 {
		t.Fatalf("expected 2 nodes after collapse, got %d", len(got))
	}
	var survivor *core.DbNode
	for _, n := range got {
		if id, _ := core.GetNodePropertyString(n, "resource_id"); id == "i-0f568ef22d52139bb" {
			survivor = n
		}
	}
	if survivor == nil {
		t.Fatal("collapsed instance disappeared entirely")
	}
	if survivor != ec2 {
		t.Fatalf("expected the EC2-described node to survive, got %q", survivor.ID)
	}

	// Identity must be the EC2 one: the unique key is rebuilt from name later, so
	// letting the SSM row's name or ARN win would re-key the node.
	if name, _ := core.GetNodePropertyString(survivor, "name"); name != "nb-demo-web" {
		t.Errorf("survivor name = %q, want the EC2 Name tag nb-demo-web", name)
	}
	if arn, _ := core.GetNodePropertyString(survivor, "arn"); arn != "arn:aws:ec2:us-east-1:864186153326:instance/i-0f568ef22d52139bb" {
		t.Errorf("survivor arn = %q, want the EC2 ARN", arn)
	}
	// SSM-only facts are still worth keeping.
	if ping, _ := core.GetNodePropertyString(survivor, "ping_status"); ping != "Online" {
		t.Errorf("ping_status = %q, want the SSM value folded in", ping)
	}
	if ip, _ := core.GetNodePropertyString(survivor, "private_ip"); ip != "172.31.0.145" {
		t.Errorf("private_ip = %q, want the EC2 value preserved", ip)
	}
}

// An instance Systems Manager knows about but EC2 describe does not report
// (hybrid / on-prem activation) must keep its node.
func TestCollapseComputeInstanceDuplicates_KeepsSSMOnlyInstance(t *testing.T) {
	ssmOnly := computeNode("ssm-only", "SSMManagedInstance", map[string]interface{}{
		"name":        "mi-0abc123",
		"resource_id": "mi-0abc123",
	})
	noResourceID := computeNode("no-rid", "EC2Instance", map[string]interface{}{"name": "unknown"})

	got := collapseComputeInstanceDuplicates([]*core.DbNode{ssmOnly, noResourceID})
	if len(got) != 2 {
		t.Fatalf("expected both nodes kept, got %d", len(got))
	}
}

// Non-compute nodes sharing a resource_id with an instance must be untouched —
// the collapse is scoped to ComputeInstance.
func TestCollapseComputeInstanceDuplicates_LeavesOtherNodeTypes(t *testing.T) {
	ec2 := computeNode("ec2", "EC2Instance", map[string]interface{}{"name": "web", "resource_id": "i-1"})
	volume := rdsTestNode("vol", core.NodeTypeStorage, map[string]interface{}{"name": "vol", "resource_id": "i-1"})

	got := collapseComputeInstanceDuplicates([]*core.DbNode{ec2, volume})
	if len(got) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(got))
	}
}

// Node properties are decoded JSON, so they routinely hold slices and maps —
// security groups, tag maps, IP lists. fillMissingProperties compares those
// interface values against "" and nil, which is safe precisely because the other
// operand is an untyped string constant: an interface comparison only panics
// when BOTH sides carry the same non-comparable dynamic type, and a slice or map
// can never match string. This pins that, since "interface == string panics" is
// a plausible-sounding review finding that would otherwise get re-litigated.
func TestFillMissingProperties_NonComparablePropertyValues(t *testing.T) {
	ssm := computeNode("ssm-node", "SSMManagedInstance", map[string]interface{}{
		"name":            "i-1",
		"resource_id":     "i-1",
		"security_groups": []interface{}{map[string]interface{}{"GroupId": "sg-1"}},
		"tags":            map[string]interface{}{"Name": "web"},
		"private_ips":     []string{"172.31.0.145"},
		"ping_status":     "Online",
	})
	ec2 := computeNode("ec2-node", "EC2Instance", map[string]interface{}{
		"name":            "nb-demo-web",
		"resource_id":     "i-1",
		"security_groups": []interface{}{map[string]interface{}{"GroupId": "sg-2"}},
		"labels":          map[string]interface{}{"env": "demo"},
	})

	got := collapseComputeInstanceDuplicates([]*core.DbNode{ssm, ec2})

	if len(got) != 1 || got[0] != ec2 {
		t.Fatalf("expected the EC2 node to survive alone, got %d node(s)", len(got))
	}
	// A non-comparable value already on the survivor is kept, not replaced.
	sgs, ok := ec2.Properties["security_groups"].([]interface{})
	if !ok || len(sgs) != 1 {
		t.Fatalf("security_groups = %#v, want the survivor's own slice", ec2.Properties["security_groups"])
	}
	if grp, _ := sgs[0].(map[string]interface{}); grp["GroupId"] != "sg-2" {
		t.Errorf("security_groups = %#v, want the EC2 node's sg-2 preserved", sgs)
	}
	// A non-comparable value the survivor lacks is folded in.
	if tags, ok := ec2.Properties["tags"].(map[string]interface{}); !ok || tags["Name"] != "web" {
		t.Errorf("tags = %#v, want the SSM map folded in", ec2.Properties["tags"])
	}
	if ips, ok := ec2.Properties["private_ips"].([]string); !ok || len(ips) != 1 {
		t.Errorf("private_ips = %#v, want the SSM slice folded in", ec2.Properties["private_ips"])
	}
	if ping, _ := core.GetNodePropertyString(ec2, "ping_status"); ping != "Online" {
		t.Errorf("ping_status = %q, want Online", ping)
	}
}

// The point of collapsing before NewNodeLookup: an instance id must resolve to
// the node that carries the traffic edges, which is what every load-balancer /
// autoscaling / nodegroup edge builder looks up.
func TestCollapseComputeInstanceDuplicates_LookupResolvesToEC2Node(t *testing.T) {
	ssm := computeNode("ssm-node", "SSMManagedInstance", map[string]interface{}{
		"name": "i-1", "resource_id": "i-1",
	})
	ec2 := computeNode("ec2-node", "EC2Instance", map[string]interface{}{
		"name": "nb-demo-web", "resource_id": "i-1", "private_ip": "172.31.0.145",
	})

	lookup := sources.NewNodeLookup(collapseComputeInstanceDuplicates([]*core.DbNode{ssm, ec2}))
	if resolved := lookup.ByResourceID["i-1"]; resolved != ec2 {
		t.Fatalf("ByResourceID resolved to %v, want the EC2 node", resolved)
	}
}
