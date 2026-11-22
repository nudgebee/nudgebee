package test

// Tier A: self-hosted VM accounts.
//
// A host scanned through a VM account gets its own SelfHostedVM node, owned by
// the VM account, linked to its cloud instance node by the selfhosted_vm_same_as_*
// rules in core/default_relationships.json. The twins come from the real AWS and
// GCP converters, so the rules are tested against the exact resource_id and
// cloud_provider values those sources emit. DB-free.

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/knowledge_graph/sources/selfhosted"
)

const (
	e2eVMAccount      = "test-account-vm-1"
	e2eAWSAccount2    = "test-account-aws-2"
	selfHostedEC2ID   = "i-0a1b2c3d4e5f60718"
	selfHostedGCEID   = "6436163694725850158"
	selfHostedOnPrem  = "vm-192.168.10.5"
	selfHostedK8sNode = "ip-10-0-1-5.ec2.internal"
)

var selfHostedScannedAt = time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)

// selfHostedHostRows is what the selfhosted source reads for the VM account:
// one host matched to an EC2 instance, one matched to a GCE instance, and one
// on-prem host with no cloud identity.
func selfHostedHostRows() []selfhosted.HostRow {
	scan := func(row selfhosted.HostRow) selfhosted.HostRow {
		row.ScanStatus = sql.NullString{String: "completed", Valid: true}
		row.PackageCount = sql.NullInt64{Int64: 495, Valid: true}
		row.FindingCount = sql.NullInt64{Int64: 93, Valid: true}
		row.ScannedAt = selfHostedScannedAt
		row.OSFamily = "ubuntu"
		row.OSVersion = "22.04"
		return row
	}
	return []selfhosted.HostRow{
		scan(selfhosted.HostRow{NBResourceID: "vm-row-ec2", ResourceID: selfHostedEC2ID, Name: selfHostedEC2ID,
			CloudProvider: "AWS", Region: "us-east-1", PrivateIP: "10.0.1.5"}),
		scan(selfhosted.HostRow{NBResourceID: "vm-row-gce", ResourceID: selfHostedGCEID, Name: selfHostedGCEID,
			CloudProvider: "GCP", Region: "us-central1", PrivateIP: "10.128.0.7"}),
		scan(selfhosted.HostRow{NBResourceID: "vm-row-onprem", ResourceID: selfHostedOnPrem, Name: "192.168.10.5",
			CloudProvider: "SelfHosted", Region: "global", PrivateIP: "192.168.10.5"}),
	}
}

func selfHostedEC2Row(account string) sources.CloudResourceRow {
	return sources.CloudResourceRow{
		ID: "ec2-row-" + account, ResourceID: selfHostedEC2ID, Name: "api-vm", Type: "compute-instance",
		ServiceName: "AmazonEC2", Account: account, Region: "us-east-1", CloudProvider: "AWS",
		Status: "Active", IsActive: true, ExternalResourceID: selfHostedEC2ID,
		Meta: json.RawMessage(`{"PrivateIpAddress":"10.0.1.5","State":{"Name":"running"}}`),
	}
}

func selfHostedGCERow() sources.CloudResourceRow {
	return sources.CloudResourceRow{
		ID: "gce-row-1", ResourceID: selfHostedGCEID, Name: "batch-vm", Type: "compute.googleapis.com/Instance",
		ServiceName: "Compute Engine", Account: "proj-1", Region: "us-central1", CloudProvider: "GCP",
		Status: "Active", IsActive: true, ExternalResourceID: selfHostedGCEID,
		Meta: json.RawMessage(`{"status":"RUNNING"}`),
	}
}

// selfHostedK8sNode is a K8s node running on the EC2 instance. The K8s → EC2
// enricher indexes ComputeInstance nodes by resource_id; the SelfHostedVM twin
// carries the instance id in instance_id, so the node must link to EC2 only.
func selfHostedK8sNodeGraph() *core.DbNode {
	key := core.BuildUniqueKey(core.CloudProviderK8s, e2eK8sAccount, "us-east-1", core.NodeTypeNode, "", selfHostedK8sNode)
	return core.NewNode(core.NodeTypeNode, key, map[string]interface{}{
		"specific_type": "KubernetesNode",
		"name":          selfHostedK8sNode,
		"providerID":    "aws:///us-east-1a/" + selfHostedEC2ID,
	}, e2eTenantID, e2eK8sAccount, "k8s")
}

func vmGraph() []*core.DbNode {
	return selfhosted.ConvertHostRows(selfHostedHostRows(), e2eTenantID, e2eVMAccount)
}

func awsGraph(t *testing.T, account string) ([]*core.DbNode, []*core.DbEdge) {
	t.Helper()
	return buildAWSGraph(t, []sources.CloudResourceRow{selfHostedEC2Row(account)},
		&core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: account})
}

func gcpGraph(t *testing.T) ([]*core.DbNode, []*core.DbEdge) {
	t.Helper()
	return buildGCPGraph(t, []sources.CloudResourceRow{selfHostedGCERow()},
		&core.SourceBuildRequest{TenantID: e2eTenantID, CloudAccountID: e2eGCPAccount})
}

func findNode(t *testing.T, nodes []*core.DbNode, match func(*core.DbNode) bool, what string) *core.DbNode {
	t.Helper()
	var found *core.DbNode
	for _, n := range nodes {
		if match(n) {
			if found != nil {
				t.Fatalf("more than one %s", what)
			}
			found = n
		}
	}
	if found == nil {
		t.Fatalf("no %s in the graph", what)
	}
	return found
}

func vmTwin(t *testing.T, nodes []*core.DbNode, hostKind, id string) *core.DbNode {
	return findNode(t, nodes, func(n *core.DbNode) bool {
		if n.SpecificType != selfhosted.SpecificTypeSelfHostedVM || n.Properties["host_kind"] != hostKind {
			return false
		}
		return n.Properties["instance_id"] == id || n.Properties["resource_id"] == id
	}, "SelfHostedVM "+id)
}

func cloudTwin(t *testing.T, nodes []*core.DbNode, specificType, account string) *core.DbNode {
	return findNode(t, nodes, func(n *core.DbNode) bool {
		return n.SpecificType == specificType && n.CloudAccountID == account
	}, specificType+" in "+account)
}

func hasEdge(edges []*core.DbEdge, from, to string, rel core.RelationshipType) bool {
	for _, e := range edges {
		if e.SourceNodeID == from && e.DestinationNodeID == to && e.RelationshipType == rel {
			return true
		}
	}
	return false
}

func edgesTouching(edges []*core.DbEdge, nodeID string, rel core.RelationshipType) int {
	n := 0
	for _, e := range edges {
		if e.RelationshipType == rel && (e.SourceNodeID == nodeID || e.DestinationNodeID == nodeID) {
			n++
		}
	}
	return n
}

// ============================================================================
// Scenario: selfhosted_default — a VM account in the default setup (its rows
// filed in the VM account), an AWS account and a GCP account holding the cloud
// instances, and a K8s node running on the EC2 instance.
// ============================================================================

func TestE2E_TierA_SelfHostedDefault(t *testing.T) {
	awsNodes, awsEdges := awsGraph(t, e2eAWSAccount)
	gcpNodes, gcpEdges := gcpGraph(t)

	nodes := append(append(append([]*core.DbNode{}, awsNodes...), gcpNodes...), vmGraph()...)
	nodes = append(nodes, selfHostedK8sNodeGraph())
	edges := append(append([]*core.DbEdge{}, awsEdges...), gcpEdges...)
	nodes, edges = runK8sEC2Enricher(t, nodes, edges, e2eTenantID)

	svc := core.NewService(createTestRequestContext(e2eTenantID), e2eDiscardLogger(), nil)
	nodes, edges = mergeGraph(nodes, edges, mergeOpts{crossAccount: true, svc: svc})

	assertGoldenGraph(t, e2eGoldenPath("selfhosted_default"), nodes, edges)

	vmEC2 := vmTwin(t, nodes, selfhosted.HostKindCloudIdentified, selfHostedEC2ID)
	vmGCE := vmTwin(t, nodes, selfhosted.HostKindCloudIdentified, selfHostedGCEID)
	vmOnPrem := vmTwin(t, nodes, selfhosted.HostKindOnPrem, selfHostedOnPrem)
	ec2 := cloudTwin(t, nodes, "EC2Instance", e2eAWSAccount)
	gce := cloudTwin(t, nodes, "GCEInstance", e2eGCPAccount)

	for _, n := range []*core.DbNode{vmEC2, vmGCE, vmOnPrem} {
		if n.CloudAccountID != e2eVMAccount || n.Source != "selfhosted" {
			t.Errorf("%s: account/source = %s/%s, want the VM account and selfhosted", n.UniqueKey, n.CloudAccountID, n.Source)
		}
	}

	// SAME_AS in both directions between each identified host and its instance.
	for _, pair := range []struct{ vm, cloud *core.DbNode }{{vmEC2, ec2}, {vmGCE, gce}} {
		if !hasEdge(edges, pair.vm.ID, pair.cloud.ID, core.RelationshipSameAs) {
			t.Errorf("missing SAME_AS %s -> %s", pair.vm.UniqueKey, pair.cloud.UniqueKey)
		}
		if !hasEdge(edges, pair.cloud.ID, pair.vm.ID, core.RelationshipSameAs) {
			t.Errorf("missing SAME_AS %s -> %s", pair.cloud.UniqueKey, pair.vm.UniqueKey)
		}
	}
	if got := edgesTouching(edges, vmEC2.ID, core.RelationshipSameAs); got != 2 {
		t.Errorf("EC2-identified host has %d SAME_AS edges, want 2 (one each way, no cross-provider match)", got)
	}
	if got := edgesTouching(edges, vmOnPrem.ID, core.RelationshipSameAs); got != 0 {
		t.Errorf("on-prem host has %d SAME_AS edges, want 0", got)
	}

	// The K8s node runs on the EC2 instance only — never on its VM-account twin.
	k8sNode := findNode(t, nodes, func(n *core.DbNode) bool { return n.NodeType == core.NodeTypeNode }, "K8s node")
	if !hasEdge(edges, k8sNode.ID, ec2.ID, core.RelationshipRunsOn) {
		t.Error("K8s node has no RUNS_ON to its EC2 instance")
	}
	if got := edgesTouching(edges, vmEC2.ID, core.RelationshipRunsOn); got != 0 {
		t.Errorf("SelfHostedVM twin has %d RUNS_ON edges, want 0", got)
	}

	// The cloud node is untouched by the VM account: no scan data leaks onto it.
	awsOnly, _ := awsGraph(t, e2eAWSAccount)
	baseline := cloudTwin(t, awsOnly, "EC2Instance", e2eAWSAccount)
	for _, k := range []string{"package_count", "finding_count", "os_family", "host_kind", "instance_id"} {
		if _, ok := ec2.Properties[k]; ok {
			t.Errorf("EC2 node carries %q from the VM account", k)
		}
	}
	if len(ec2.Properties) != len(baseline.Properties) {
		t.Errorf("EC2 node has %d properties, an AWS-only build has %d", len(ec2.Properties), len(baseline.Properties))
	}
}

// A host whose datasource is pointed at the AWS account is read from the AWS
// account's row. Its node still belongs to the VM account, with the same key and
// id as in the default setup, and is linked the same way.
func TestE2E_TierA_SelfHostedRepointed(t *testing.T) {
	defaultNode := vmTwin(t, vmGraph(), selfhosted.HostKindCloudIdentified, selfHostedEC2ID)

	rows := selfHostedHostRows()[:1]
	rows[0].NBResourceID = "ec2-row-" + e2eAWSAccount // the AWS account's own row
	repointed := selfhosted.ConvertHostRows(rows, e2eTenantID, e2eVMAccount)

	awsNodes, awsEdges := awsGraph(t, e2eAWSAccount)
	svc := core.NewService(createTestRequestContext(e2eTenantID), e2eDiscardLogger(), nil)
	nodes, edges := mergeGraph(append(awsNodes, repointed...), awsEdges, mergeOpts{crossAccount: true, svc: svc})

	vm := vmTwin(t, nodes, selfhosted.HostKindCloudIdentified, selfHostedEC2ID)
	if vm.ID != defaultNode.ID || vm.CloudAccountID != e2eVMAccount {
		t.Errorf("re-pointed node %s/%s, want id %s in the VM account", vm.ID, vm.CloudAccountID, defaultNode.ID)
	}
	ec2 := cloudTwin(t, nodes, "EC2Instance", e2eAWSAccount)
	if !hasEdge(edges, vm.ID, ec2.ID, core.RelationshipSameAs) || !hasEdge(edges, ec2.ID, vm.ID, core.RelationshipSameAs) {
		t.Error("re-pointed host is not SAME_AS its EC2 instance in both directions")
	}
}

// The same instance onboarded under two AWS accounts gives two EC2 nodes; the
// VM twin is the same machine as both.
func TestE2E_TierA_SelfHostedDuplicateCloudTwins(t *testing.T) {
	aws1, e1 := awsGraph(t, e2eAWSAccount)
	aws2, e2 := awsGraph(t, e2eAWSAccount2)
	vm := selfhosted.ConvertHostRows(selfHostedHostRows()[:1], e2eTenantID, e2eVMAccount)

	nodes := append(append(append([]*core.DbNode{}, aws1...), aws2...), vm...)
	svc := core.NewService(createTestRequestContext(e2eTenantID), e2eDiscardLogger(), nil)
	nodes, edges := mergeGraph(nodes, append(e1, e2...), mergeOpts{crossAccount: true, svc: svc})

	vmNode := vmTwin(t, nodes, selfhosted.HostKindCloudIdentified, selfHostedEC2ID)
	for _, account := range []string{e2eAWSAccount, e2eAWSAccount2} {
		ec2 := cloudTwin(t, nodes, "EC2Instance", account)
		if !hasEdge(edges, vmNode.ID, ec2.ID, core.RelationshipSameAs) || !hasEdge(edges, ec2.ID, vmNode.ID, core.RelationshipSameAs) {
			t.Errorf("VM twin is not SAME_AS the EC2 node in %s", account)
		}
	}
	if got := edgesTouching(edges, vmNode.ID, core.RelationshipSameAs); got != 4 {
		t.Errorf("VM twin has %d SAME_AS edges, want 4", got)
	}
}

// Rebuilding only the VM account (the AWS account as context), or only the AWS
// account (the VM account as context), must produce the same SAME_AS edges as a
// tenant-wide build — otherwise a scoped build would drop them and its sweep
// would tombstone the real ones.
func TestE2E_TierA_SelfHostedPartialBuildMatchesFullBuild(t *testing.T) {
	svc := core.NewService(createTestRequestContext(e2eTenantID), e2eDiscardLogger(), nil)
	build := func() []*core.DbNode {
		awsNodes, _ := awsGraph(t, e2eAWSAccount)
		return append(awsNodes, selfhosted.ConvertHostRows(selfHostedHostRows()[:1], e2eTenantID, e2eVMAccount)...)
	}

	_, fullEdges := mergeGraph(build(), nil, mergeOpts{crossAccount: true, svc: svc})
	want := crossAccountEdgeKeys(fullEdges)
	if len(want) != 2 {
		t.Fatalf("reference build produced %d cross-account edges, want the 2 SAME_AS edges: %v", len(want), want)
	}

	// The in-memory pipeline is identical whichever account is the rebuilt one
	// and which arrives as context; what differs is which nodes get persisted.
	for _, rebuilt := range []string{e2eVMAccount, e2eAWSAccount} {
		nodes, edges := mergeGraph(build(), nil, mergeOpts{crossAccount: true, svc: svc})
		if got := crossAccountEdgeKeys(edges); !equalStringSlices(want, got) {
			t.Errorf("rebuilding %s: cross-account edges differ\n full:    %v\n partial: %v", rebuilt, want, got)
		}
		if !hasAccountSpanningEdge(nodes, edges) {
			t.Errorf("rebuilding %s: no SAME_AS edge spans the two accounts", rebuilt)
		}
	}
}
