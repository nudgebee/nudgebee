package resourcepatch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	_ "nudgebee/services/knowledge_graph/sources/aws" // registers the AWS patch policy
	"nudgebee/services/knowledge_graph/sources/k8s"
)

const (
	testTenant  = "tenant-1"
	testAccount = "account-1"
)

// ---------------------------------------------------------------------------
// Fakes

type fakeStore struct {
	locked           bool  // true while ExcludeBuilds runs its callback
	lockTaken        int   // how many times ExcludeBuilds ran its callback
	lockErr          error // makes ExcludeBuilds fail before running anything
	savedOutsideLock bool
	nodes            []*core.StoredNode // active graph nodes
	rows             []sources.CloudResourceRow
	version          int64
	running          bool
	lookupErr        error

	savedNodes     []*core.DbNode
	savedVersion   int64
	savedEdges     []*core.DbEdge
	tombstoned     []string
	edgeTombstones []core.EdgeSignature
	lookups        int
	ops            []string // write order, for ordering assertions
}

func (f *fakeStore) FindActiveNodesByProviderID(_, _, id string) ([]*core.StoredNode, error) {
	f.lookups++
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	out := make([]*core.StoredNode, 0)
	for _, n := range f.nodes {
		rid, _ := n.Properties["resource_id"].(string)
		nb, _ := n.Properties["nb_resource_id"].(string)
		arn, _ := n.Properties["arn"].(string)
		if rid == id || nb == id || (arn != "" && strings.EqualFold(arn, id)) {
			out = append(out, n)
		}
	}
	return out, nil
}

func (f *fakeStore) FindActiveNodeByID(_, id string) (*core.StoredNode, error) {
	for _, n := range f.nodes {
		if n.ID == id {
			return n, nil
		}
	}
	return nil, nil
}

func (f *fakeStore) FindActiveNodesByName(_, _, source, specificType, name string) ([]*core.StoredNode, error) {
	out := make([]*core.StoredNode, 0)
	for _, n := range f.nodes {
		if n.Source == source && n.SpecificType == specificType && n.Properties["name"] == name {
			out = append(out, n)
		}
	}
	return out, nil
}

func (f *fakeStore) TombstoneNodeWithIncidentEdges(_, nodeID string) (int64, error) {
	f.tombstoned = append(f.tombstoned, nodeID)
	f.ops = append(f.ops, "tombstone:"+nodeID)
	return 2, nil
}

func (f *fakeStore) TombstoneOutgoingEdges(_, _ string, sig core.EdgeSignature, _ int64) (int64, error) {
	f.edgeTombstones = append(f.edgeTombstones, sig)
	return 0, nil
}

func (f *fakeStore) LoadActiveNodesByTypes(_, _ string, types []core.NodeType) ([]*core.DbNode, error) {
	want := make(map[core.NodeType]bool)
	for _, t := range types {
		want[t] = true
	}
	out := make([]*core.DbNode, 0)
	for _, n := range f.nodes {
		if want[n.NodeType] {
			out = append(out, n.DbNode)
		}
	}
	return out, nil
}

func (f *fakeStore) CurrentSyncVersion(string) (int64, error) { return f.version, nil }

// ExcludeBuilds records that patches ran under the lock, and fails like the
// real lock would when lockErr is set.
func (f *fakeStore) ExcludeBuilds(_ string, fn func(func(string) (bool, error)) error) error {
	if f.lockErr != nil {
		return f.lockErr
	}
	f.locked = true
	f.lockTaken++
	defer func() { f.locked = false }()
	return fn(func(string) (bool, error) { return f.running, nil })
}

func (f *fakeStore) SaveNodes(nodes []*core.DbNode, v int64) error {
	f.savedOutsideLock = f.savedOutsideLock || !f.locked
	f.savedNodes = append(f.savedNodes, nodes...)
	f.savedVersion = v
	for _, n := range nodes {
		f.ops = append(f.ops, "save:"+n.ID)
	}
	return nil
}

func (f *fakeStore) SaveEdges(edges []*core.DbEdge, _ []*core.DbNode, _ int64) error {
	f.savedEdges = append(f.savedEdges, edges...)
	return nil
}

func (f *fakeStore) LoadCloudResourceRows(_, resourceID, rowID string) ([]sources.CloudResourceRow, error) {
	out := make([]sources.CloudResourceRow, 0)
	for _, r := range f.rows {
		if r.IsActive && ((resourceID != "" && r.ResourceID == resourceID) || r.ID == rowID) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeStore) writes() int {
	return len(f.savedNodes) + len(f.savedEdges) + len(f.tombstoned) + len(f.edgeTombstones)
}

// fakeK8sPolicy serves workload patches built by the real k8s builder from rows
// the test supplies, and node presence from a set.
type fakeK8sPolicy struct {
	helper      *k8s.K8sSourceTestHelper
	workloads   map[string]*k8s.K8sWorkloadRow
	activeNodes map[string]bool
	buildErr    error
}

func (p *fakeK8sPolicy) Source() string                  { return "k8s" }
func (p *fakeK8sPolicy) DetectedWorkloadKinds() []string { return p.helper.DetectedWorkloadKinds() }
func (p *fakeK8sPolicy) NodeSpecificType() string        { return "KubernetesNode" }
func (p *fakeK8sPolicy) WorkloadSpecificTypes(kinds []string) []string {
	return kinds
}

func (p *fakeK8sPolicy) BuildWorkload(tenantID, accountID, workloadID string) (*sources.WorkloadPatch, error) {
	if p.buildErr != nil {
		return nil, p.buildErr
	}
	row, ok := p.workloads[workloadID]
	if !ok {
		return nil, sources.ErrNotInInventory
	}
	req := &core.SourceBuildRequest{TenantID: tenantID, CloudAccountID: accountID}
	patch := p.helper.BuildWorkloadPatch(row, map[string]string{}, req)
	if row.Kind == "Service" {
		return &sources.WorkloadPatch{Kind: row.Kind}, nil
	}
	return patch, nil
}

func (p *fakeK8sPolicy) HasActiveNode(_, _, name string) (bool, error) {
	return p.activeNodes[name], nil
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newTestApplier(t *testing.T, st store, k8sPolicy sources.K8sPatchPolicy) *Applier {
	t.Helper()
	return newApplier(st, sources.NewCloudPatchPolicies(discard()), k8sPolicy, discard())
}

func awsPolicy() sources.CloudPatchPolicy {
	return sources.NewCloudPatchPolicies(discard())["aws"]
}

// ---------------------------------------------------------------------------
// AWS fixture: a VPC, a subnet, an EC2 instance and a dead-letter queue, in both
// the inventory and the graph, built by the real AWS policy.

func awsRow(id, resourceID, name, typ, service string, meta, tags map[string]interface{}) sources.CloudResourceRow {
	metaJSON, _ := json.Marshal(meta)
	tagsJSON, _ := json.Marshal(tags)
	return sources.CloudResourceRow{
		ID: id, ResourceID: resourceID, Name: name, Type: typ, ServiceName: service,
		Status: "Active", IsActive: true, CloudProvider: "AWS", Region: "us-east-1",
		Account: testAccount, Tenant: testTenant, Meta: metaJSON, Tags: tagsJSON,
	}
}

const dlqARN = "arn:aws:sqs:us-east-1:111111111111:orders-dlq"

func awsFixture(t *testing.T) *fakeStore {
	t.Helper()
	vpc := awsRow("row-vpc", "vpc-1", "main-vpc", "vpc", "AmazonVPC", map[string]interface{}{"VpcId": "vpc-1"}, nil)
	subnet := awsRow("row-subnet", "subnet-1", "main-subnet", "subnet", "AmazonVPC", map[string]interface{}{"SubnetId": "subnet-1", "VpcId": "vpc-1"}, nil)
	ec2 := awsRow("row-ec2", "i-0aaa", "app-ec2", "compute-instance", "AmazonEC2",
		map[string]interface{}{"InstanceId": "i-0aaa", "VpcId": "vpc-1", "SubnetId": "subnet-1"},
		map[string]interface{}{"team": "a"})
	dlq := awsRow("row-dlq", "orders-dlq", "orders-dlq", "queue", "AWSQueueService",
		map[string]interface{}{"QueueName": "orders-dlq", "QueueArn": dlqARN}, nil)
	dlq.ARN = dlqARN

	st := &fakeStore{rows: []sources.CloudResourceRow{vpc, subnet, ec2, dlq}, version: 10}
	policy := awsPolicy()
	req := &core.SourceBuildRequest{TenantID: testTenant, CloudAccountID: testAccount}
	infra, _ := policy.BuildNodes([]sources.CloudResourceRow{vpc, subnet}, nil, req)
	instance, missing := policy.BuildNodes([]sources.CloudResourceRow{ec2}, infra, req)
	queue, _ := policy.BuildNodes([]sources.CloudResourceRow{dlq}, infra, req)
	if len(missing) != 0 || len(instance) != 1 || len(queue) != 1 {
		t.Fatalf("fixture: built %d instance / %d queue nodes, missing %v", len(instance), len(queue), missing)
	}
	for _, n := range append(append(infra, instance...), queue...) {
		st.nodes = append(st.nodes, &core.StoredNode{DbNode: n, LastSyncVersion: 10})
	}
	// An enricher-written key the single-resource build cannot recompute.
	instance[0].Properties["exposed_internet"] = true
	return st
}

func (f *fakeStore) node(t *testing.T, resourceID string) *core.StoredNode {
	t.Helper()
	for _, n := range f.nodes {
		if n.Properties["resource_id"] == resourceID {
			return n
		}
	}
	t.Fatalf("fixture has no node for %s", resourceID)
	return nil
}

func upsert(id string) Ref { return Ref{AccountID: testAccount, ProviderResourceID: id, Op: OpUpsert} }
func del(id string) Ref    { return Ref{AccountID: testAccount, ProviderResourceID: id, Op: OpDelete} }

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ---------------------------------------------------------------------------
// Updates

func TestApply_EC2PropertyAndEdgeUpdate(t *testing.T) {
	st := awsFixture(t)
	for i := range st.rows {
		if st.rows[i].ResourceID == "i-0aaa" {
			st.rows[i].Tags = json.RawMessage(`{"team":"b"}`)
		}
	}
	stored := st.node(t, "i-0aaa")
	stored.LastSyncVersion = 12 // patched once already since the last build

	res := newTestApplier(t, st, nil).Apply(testTenant, upsert("i-0aaa"))
	if res.Outcome != Applied {
		t.Fatalf("outcome %s (%s), want applied", res.Outcome, res.Reason)
	}
	if len(st.savedNodes) != 1 || st.savedNodes[0].ID != stored.ID {
		t.Fatalf("saved %d nodes, want the instance", len(st.savedNodes))
	}
	saved := st.savedNodes[0]
	if st.savedVersion != 13 {
		t.Errorf("stamped version %d, want 13 (one above the node's own version)", st.savedVersion)
	}
	if saved.Properties["exposed_internet"] != true {
		t.Error("stored-only property was dropped by the merge")
	}
	if !strings.Contains(mustJSON(saved.Properties["labels"]), `"b"`) {
		t.Errorf("new tag not written: labels=%s", mustJSON(saved.Properties["labels"]))
	}
	if len(st.savedEdges) != 2 {
		t.Errorf("saved %d edges, want HOSTED_ON vpc + subnet", len(st.savedEdges))
	}
	if len(st.edgeTombstones) != 1 || !hasKind(st.edgeTombstones[0], core.RelationshipRunsOn, "eks_node") {
		t.Errorf("EC2 signature not used to retire old edges: %v", st.edgeTombstones)
	}
}

func hasKind(sig core.EdgeSignature, rel core.RelationshipType, connectionType string) bool {
	for _, k := range sig.Kinds {
		if k.Relationship == rel && k.ConnectionType == connectionType {
			return true
		}
	}
	return false
}

func TestApply_UpdateDeclines(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(st *fakeStore)
		ref    string
		want   Outcome
		writes bool
	}{
		{"renamed resource changes the unique key", func(st *fakeStore) { st.rows[2].Name = "renamed-ec2" }, "i-0aaa", NeedsRebuild, false},
		{"account-level kind", nil, "vpc-1", NeedsRebuild, false},
		{"references a subnet the graph does not have", func(st *fakeStore) {
			st.rows[2].Meta = json.RawMessage(`{"InstanceId":"i-0aaa","VpcId":"vpc-1","SubnetId":"subnet-9"}`)
		}, "i-0aaa", NeedsRebuild, false},
		{"provider id matches two nodes", func(st *fakeStore) {
			dup := *st.nodes[2].DbNode
			dup.ID = "other"
			st.nodes = append(st.nodes, &core.StoredNode{DbNode: &dup})
		}, "i-0aaa", NeedsRebuild, false},
		{"node from a source without a policy", func(st *fakeStore) { st.nodes[2].Source = "traces" }, "i-0aaa", NeedsRebuild, false},
		{"lookup error", func(st *fakeStore) { st.lookupErr = errors.New("boom") }, "i-0aaa", NeedsRebuild, false},
		{"no active row left: tombstone even for an upsert", func(st *fakeStore) { st.rows[2].IsActive = false }, "i-0aaa", Applied, true},
		{"row excluded by the source's status filter", func(st *fakeStore) { st.rows[2].Status = "Deleted" }, "i-0aaa", Applied, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := awsFixture(t)
			if tc.mutate != nil {
				tc.mutate(st)
			}
			res := newTestApplier(t, st, nil).Apply(testTenant, upsert(tc.ref))
			if res.Outcome != tc.want {
				t.Fatalf("outcome %s (%s), want %s", res.Outcome, res.Reason, tc.want)
			}
			if tc.writes {
				if len(st.tombstoned) != 1 || len(st.savedNodes) != 0 {
					t.Errorf("want exactly one tombstone and no saves, got %d tombstones, %d saves", len(st.tombstoned), len(st.savedNodes))
				}
			} else if st.writes() != 0 {
				t.Errorf("declined ref still wrote to the graph (%d writes)", st.writes())
			}
		})
	}
}

func TestApply_EC2AndSSMRowsCollapse(t *testing.T) {
	st := awsFixture(t)
	st.rows = append(st.rows, awsRow("row-ssm", "i-0aaa", "i-0aaa", "managedinstance", "AWSSystemsManager",
		map[string]interface{}{"InstanceId": "i-0aaa", "PingStatus": "Online"}, nil))
	if !awsPolicy().IncludeForGraph(&st.rows[len(st.rows)-1]) {
		t.Fatal("fixture SSM row is filtered out, so the collapse would not be exercised")
	}

	res := newTestApplier(t, st, nil).Apply(testTenant, upsert("i-0aaa"))
	if res.Outcome != Applied {
		t.Fatalf("outcome %s (%s), want applied: the SSM row folds into the EC2 node", res.Outcome, res.Reason)
	}
	if len(st.savedNodes) != 1 || st.savedNodes[0].SpecificType != "EC2Instance" {
		t.Fatalf("saved %d nodes, want only the EC2 survivor", len(st.savedNodes))
	}
	if st.savedNodes[0].Properties["managed"] != true {
		t.Error("SSM-only property was not folded into the survivor")
	}
}

// ---------------------------------------------------------------------------
// Creates

func newQueueRow() sources.CloudResourceRow {
	row := awsRow("3f1c2d4e-0000-4000-8000-00000000a001", "orders", "orders", "queue", "AWSQueueService", map[string]interface{}{
		"QueueName":     "orders",
		"QueueArn":      "arn:aws:sqs:us-east-1:111111111111:orders",
		"RedrivePolicy": `{"deadLetterTargetArn":"` + dlqARN + `","maxReceiveCount":5}`,
	}, nil)
	row.ARN = "arn:aws:sqs:us-east-1:111111111111:orders"
	return row
}

func TestApply_CreateQueueWithDeadLetterEdge(t *testing.T) {
	st := awsFixture(t)
	queue := newQueueRow()
	st.rows = append(st.rows, queue)

	res := newTestApplier(t, st, nil).Apply(testTenant, upsert(queue.ID))
	if res.Outcome != Applied {
		t.Fatalf("outcome %s (%s), want applied", res.Outcome, res.Reason)
	}
	if len(st.savedNodes) != 1 || st.savedNodes[0].SpecificType != "SQSQueue" {
		t.Fatalf("saved %v, want the new queue", st.savedNodes)
	}
	if st.savedVersion != 11 {
		t.Errorf("stamped version %d, want current+1 = 11", st.savedVersion)
	}
	dlq := st.node(t, "orders-dlq")
	if len(st.savedEdges) != 1 || st.savedEdges[0].DestinationNodeID != dlq.ID || st.savedEdges[0].RelationshipType != core.RelationshipRoutesTo {
		t.Errorf("want one ROUTES_TO edge to the dead-letter queue, got %s", mustJSON(st.savedEdges))
	}
}

func TestApply_CreateNodeOnlyType(t *testing.T) {
	st := awsFixture(t)
	topic := awsRow("3f1c2d4e-0000-4000-8000-00000000a002", "arn:aws:sns:us-east-1:111111111111:events", "events", "topic", "AmazonSNS", nil, nil)
	st.rows = append(st.rows, topic)

	res := newTestApplier(t, st, nil).Apply(testTenant, upsert(topic.ID))
	if res.Outcome != Applied {
		t.Fatalf("outcome %s (%s), want applied", res.Outcome, res.Reason)
	}
	if len(st.savedNodes) != 1 || st.savedNodes[0].SpecificType != "SNSTopic" {
		t.Fatalf("saved %v, want the new topic", st.savedNodes)
	}
	if len(st.savedEdges) != 0 || len(st.edgeTombstones) != 0 {
		t.Error("a node-only type must not touch edges")
	}
}

func TestApply_CreateDeclines(t *testing.T) {
	cases := []struct {
		name string
		row  sources.CloudResourceRow
		want Outcome
	}{
		{"EC2 instance is not creatable", awsRow("3f1c2d4e-0000-4000-8000-00000000b001", "i-0bbb", "new-ec2", "compute-instance", "AmazonEC2",
			map[string]interface{}{"InstanceId": "i-0bbb", "VpcId": "vpc-1", "SubnetId": "subnet-1"}, nil), NeedsRebuild},
		{"IAM role rows build no node of their own", awsRow("3f1c2d4e-0000-4000-8000-00000000b002", "app-role", "app-role", "Role", "AWSIAM", nil, nil), NeedsRebuild},
		{"row outside the graph's scope", awsRow("3f1c2d4e-0000-4000-8000-00000000b003", "Default", "Default", "sampling-rule", "AWSXRay", nil, nil), NothingToDo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := awsFixture(t)
			st.rows = append(st.rows, tc.row)
			res := newTestApplier(t, st, nil).Apply(testTenant, upsert(tc.row.ID))
			if res.Outcome != tc.want {
				t.Fatalf("outcome %s (%s), want %s", res.Outcome, res.Reason, tc.want)
			}
			if st.writes() != 0 {
				t.Errorf("declined create wrote to the graph (%d writes)", st.writes())
			}
		})
	}

	// An EventBridge upsert carries the provider's own id, not the row id.
	t.Run("event ref for a brand-new instance", func(t *testing.T) {
		st := awsFixture(t)
		st.rows = append(st.rows, awsRow("3f1c2d4e-0000-4000-8000-00000000c001", "i-0new", "new-ec2", "compute-instance", "AmazonEC2",
			map[string]interface{}{"InstanceId": "i-0new", "VpcId": "vpc-1", "SubnetId": "subnet-1"}, nil))
		res := newTestApplier(t, st, nil).Apply(testTenant, upsert("i-0new"))
		if res.Outcome != NeedsRebuild || st.writes() != 0 {
			t.Fatalf("outcome %s (%s) with %d writes, want needs_rebuild", res.Outcome, res.Reason, st.writes())
		}
	})

	t.Run("no inventory row", func(t *testing.T) {
		st := awsFixture(t)
		if res := newTestApplier(t, st, nil).Apply(testTenant, upsert("unknown-id")); res.Outcome != NeedsRebuild {
			t.Fatalf("outcome %s, want needs_rebuild", res.Outcome)
		}
	})
}

func TestApply_CreateOverExistingKeyMerges(t *testing.T) {
	st := awsFixture(t)
	queue := newQueueRow()
	st.rows = append(st.rows, queue)
	// A node for the same queue (same unique key) exists under an older row id.
	policy := awsPolicy()
	built, _ := policy.BuildNodes([]sources.CloudResourceRow{queue}, nil, &core.SourceBuildRequest{TenantID: testTenant, CloudAccountID: testAccount})
	old := built[0]
	old.Properties["nb_resource_id"] = "3f1c2d4e-0000-4000-8000-00000000dead"
	old.Properties["resource_id"] = "orders-previous"
	old.Properties["enriched_by_someone"] = "keep me"
	st.nodes = append(st.nodes, &core.StoredNode{DbNode: old, LastSyncVersion: 30})

	res := newTestApplier(t, st, nil).Apply(testTenant, upsert(queue.ID))
	if res.Outcome != Applied {
		t.Fatalf("outcome %s (%s), want applied", res.Outcome, res.Reason)
	}
	saved := st.savedNodes[0]
	if saved.Properties["enriched_by_someone"] != "keep me" || saved.Properties["nb_resource_id"] != queue.ID {
		t.Errorf("existing node not merged: %s", mustJSON(saved.Properties))
	}
	if st.savedVersion != 31 {
		t.Errorf("version %d, want one above the existing node (31)", st.savedVersion)
	}
}

// ---------------------------------------------------------------------------
// ApplyAll

func TestApplyAll_Rules(t *testing.T) {
	t.Run("running build folds refs into a rebuild", func(t *testing.T) {
		st := awsFixture(t)
		st.running = true
		if got := newTestApplier(t, st, nil).ApplyAll(context.Background(), testTenant, []Ref{upsert("i-0aaa")}); len(got) != 1 {
			t.Errorf("rebuild %v, want the account", got)
		}
		if st.lookups != 0 || st.writes() != 0 {
			t.Error("refs were applied while a build covering the account was running")
		}
	})

	t.Run("over the cap rebuilds without applying", func(t *testing.T) {
		st := awsFixture(t)
		refs := make([]Ref, maxRefsPerAccount+1)
		for i := range refs {
			refs[i] = upsert("i-0aaa")
		}
		if got := newTestApplier(t, st, nil).ApplyAll(context.Background(), testTenant, refs); len(got) != 1 || st.lookups != 0 {
			t.Errorf("rebuild %v after %d lookups, want the account and no lookups", got, st.lookups)
		}
	})

	t.Run("ref without an account is skipped", func(t *testing.T) {
		st := awsFixture(t)
		ref := upsert("i-0aaa")
		ref.AccountID = ""
		if got := newTestApplier(t, st, nil).ApplyAll(context.Background(), testTenant, []Ref{ref}); len(got) != 0 || st.writes() != 0 {
			t.Errorf("rebuild %v, %d writes; want the ref skipped", got, st.writes())
		}
	})

	t.Run("a creatable new resource needs no rebuild, an unknown delete neither", func(t *testing.T) {
		st := awsFixture(t)
		queue := newQueueRow()
		st.rows = append(st.rows, queue)
		got := newTestApplier(t, st, nil).ApplyAll(context.Background(), testTenant, []Ref{upsert(queue.ID), del("gone")})
		if len(got) != 0 {
			t.Errorf("rebuild %v, want none", got)
		}
	})

	t.Run("every write happens under the tenant build lock", func(t *testing.T) {
		st := awsFixture(t)
		queue := newQueueRow()
		st.rows = append(st.rows, queue)
		newTestApplier(t, st, nil).ApplyAll(context.Background(), testTenant, []Ref{upsert(queue.ID)})
		if st.writes() == 0 {
			t.Fatal("nothing was written; the case does not exercise the lock")
		}
		if st.savedOutsideLock {
			t.Error("a patch wrote outside ExcludeBuilds, where a build could be claimed underneath it")
		}
	})

	// The lock is a transaction: held around every ref, it sat idle in transaction
	// while they were applied, and a migration queued behind it (#41195).
	t.Run("the build lock is taken per ref, not held across them", func(t *testing.T) {
		st := awsFixture(t)
		st.rows[2].IsActive = false // the instance is gone
		queue := newQueueRow()
		st.rows = append(st.rows, queue)
		newTestApplier(t, st, nil).ApplyAll(context.Background(), testTenant, []Ref{upsert(queue.ID), del("i-0aaa")})
		if st.lockTaken != 2 {
			t.Errorf("lock taken %d times for 2 refs, want once per ref", st.lockTaken)
		}
	})

	t.Run("no lock means no patch and a rebuild of every account", func(t *testing.T) {
		st := awsFixture(t)
		st.lockErr = errors.New("lock unavailable")
		got := newTestApplier(t, st, nil).ApplyAll(context.Background(), testTenant, []Ref{upsert("i-0aaa")})
		if len(got) != 1 || st.lookups != 0 || st.writes() != 0 {
			t.Errorf("rebuild %v after %d lookups and %d writes; want the account, nothing applied", got, st.lookups, st.writes())
		}
	})

	t.Run("deletes run before upserts", func(t *testing.T) {
		st := awsFixture(t)
		st.rows[2].IsActive = false // the instance is gone
		queue := newQueueRow()
		st.rows = append(st.rows, queue)
		newTestApplier(t, st, nil).ApplyAll(context.Background(), testTenant, []Ref{upsert(queue.ID), del("i-0aaa")})
		if len(st.ops) < 2 || !strings.HasPrefix(st.ops[0], "tombstone:") {
			t.Errorf("write order %v, want the tombstone first", st.ops)
		}
	})
}

// ---------------------------------------------------------------------------
// Kubernetes

func k8sFixture(t *testing.T, kind string) (*fakeStore, *fakeK8sPolicy, *k8s.K8sWorkloadRow) {
	t.Helper()
	row := &k8s.K8sWorkloadRow{
		ID: "wl-1", TenantID: testTenant, CloudAccountID: testAccount, Kind: kind,
		Namespace: "shop", Name: "api", ClusterName: "dev", IsActive: true,
		Meta: json.RawMessage(`{"config":{"containers":[{"name":"api","image":"registry.example.com/api:1.0"}]}}`),
	}
	source, err := k8s.NewK8sSource(k8s.K8sSourceConfig{}, discard())
	if err != nil {
		t.Fatal(err)
	}
	policy := &fakeK8sPolicy{
		helper:      k8s.NewK8sSourceTestHelper(source),
		workloads:   map[string]*k8s.K8sWorkloadRow{"wl-1": row},
		activeNodes: map[string]bool{},
	}
	req := &core.SourceBuildRequest{TenantID: testTenant, CloudAccountID: testAccount}
	patch := policy.helper.BuildWorkloadPatch(row, map[string]string{}, req)
	st := &fakeStore{version: 3, nodes: []*core.StoredNode{
		{DbNode: patch.Node, LastSyncVersion: 3},
		{DbNode: patch.Namespace, LastSyncVersion: 3},
	}}
	return st, policy, row
}

func TestApply_K8sWorkload(t *testing.T) {
	t.Run("rollout applies node and own edges", func(t *testing.T) {
		st, policy, _ := k8sFixture(t, "Deployment")
		res := newTestApplier(t, st, policy).Apply(testTenant, upsert("wl-1"))
		if res.Outcome != Applied {
			t.Fatalf("outcome %s (%s), want applied", res.Outcome, res.Reason)
		}
		if st.savedVersion != 4 {
			t.Errorf("version %d, want 4", st.savedVersion)
		}
		found := false
		for _, e := range st.savedEdges {
			if e.RelationshipType == core.RelationshipRunsOn && e.DestinationNodeID == st.nodes[1].ID {
				found = true
			}
		}
		if !found {
			t.Error("RUNS_ON namespace edge not re-emitted")
		}
		if len(st.edgeTombstones) != 1 || st.edgeTombstones[0].Source != "k8s" {
			t.Errorf("workload signature not used: %v", st.edgeTombstones)
		}
	})

	t.Run("workload gone tombstones", func(t *testing.T) {
		st, policy, _ := k8sFixture(t, "Deployment")
		policy.workloads = map[string]*k8s.K8sWorkloadRow{}
		if res := newTestApplier(t, st, policy).Apply(testTenant, del("wl-1")); res.Outcome != Applied || len(st.tombstoned) != 1 {
			t.Fatalf("outcome %s, %d tombstones; want applied with one tombstone", res.Outcome, len(st.tombstoned))
		}
	})

	declines := []struct {
		name   string
		kind   string
		mutate func(st *fakeStore, policy *fakeK8sPolicy, row *k8s.K8sWorkloadRow)
	}{
		{"relay-shaped kind", "Service", nil},
		{"namespace not in graph", "Deployment", func(st *fakeStore, _ *fakeK8sPolicy, _ *k8s.K8sWorkloadRow) { st.nodes = st.nodes[:1] }},
		{"moved namespace changes the key", "Deployment", func(_ *fakeStore, _ *fakeK8sPolicy, row *k8s.K8sWorkloadRow) { row.Namespace = "other" }},
		{"inventory error", "Deployment", func(_ *fakeStore, policy *fakeK8sPolicy, _ *k8s.K8sWorkloadRow) {
			policy.buildErr = errors.New("db down")
		}},
	}
	for _, tc := range declines {
		t.Run(tc.name, func(t *testing.T) {
			st, policy, row := k8sFixture(t, tc.kind)
			if tc.mutate != nil {
				tc.mutate(st, policy, row)
			}
			if res := newTestApplier(t, st, policy).Apply(testTenant, upsert("wl-1")); res.Outcome != NeedsRebuild || st.writes() != 0 {
				t.Fatalf("outcome %s (%s) with %d writes, want needs_rebuild and no writes", res.Outcome, res.Reason, st.writes())
			}
		})
	}
}

func TestApply_K8sNodeDelete(t *testing.T) {
	nodeRef := Ref{AccountID: testAccount, ProviderResourceID: "ip-10-0-0-1", NodeType: string(core.NodeTypeNode), Op: OpDelete}
	fixture := func(t *testing.T) (*fakeStore, *fakeK8sPolicy) {
		st, policy, _ := k8sFixture(t, "Deployment")
		node := core.NewNode(core.NodeTypeNode, "k8s:"+testAccount+"::Node::ip-10-0-0-1",
			map[string]interface{}{"name": "ip-10-0-0-1", "specific_type": "KubernetesNode"}, testTenant, testAccount, "k8s")
		st.nodes = append(st.nodes, &core.StoredNode{DbNode: node})
		return st, policy
	}

	t.Run("gone from inventory tombstones", func(t *testing.T) {
		st, policy := fixture(t)
		if res := newTestApplier(t, st, policy).Apply(testTenant, nodeRef); res.Outcome != Applied || len(st.tombstoned) != 1 {
			t.Fatalf("outcome %s (%s), %d tombstones; want applied", res.Outcome, res.Reason, len(st.tombstoned))
		}
	})
	t.Run("still in inventory does nothing", func(t *testing.T) {
		st, policy := fixture(t)
		policy.activeNodes["ip-10-0-0-1"] = true
		if res := newTestApplier(t, st, policy).Apply(testTenant, nodeRef); res.Outcome != NothingToDo || st.writes() != 0 {
			t.Fatalf("outcome %s with %d writes, want nothing_to_do", res.Outcome, st.writes())
		}
	})
	t.Run("no node with the name does nothing", func(t *testing.T) {
		st, policy := fixture(t)
		ref := nodeRef
		ref.ProviderResourceID = "ip-unknown"
		if res := newTestApplier(t, st, policy).Apply(testTenant, ref); res.Outcome != NothingToDo {
			t.Fatalf("outcome %s, want nothing_to_do", res.Outcome)
		}
	})
	t.Run("without a k8s policy the account rebuilds", func(t *testing.T) {
		st, _ := fixture(t)
		if res := newTestApplier(t, st, nil).Apply(testTenant, nodeRef); res.Outcome != NeedsRebuild {
			t.Fatalf("outcome %s, want needs_rebuild", res.Outcome)
		}
	})
}
