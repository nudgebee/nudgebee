package core

import (
	"reflect"
	"testing"
)

// The declarable types are real node types, and the Kubernetes ones are only
// offered in Kubernetes accounts: outside a cluster a Workload has no meaning.
// (The sweep never retires a declared node of any type; see
// TestMarkInactiveNodes_NeverSweepsDeclaredNodes.)
func TestManualNodeTypes(t *testing.T) {
	for _, nt := range append(append([]NodeType{}, ManualNodeTypes...), ManualKubernetesNodeTypes...) {
		if !nt.IsKnown() {
			t.Errorf("%s is not a known node type", nt)
		}
		if !IsManualNodeType(nt) {
			t.Errorf("%s should be declarable", nt)
		}
	}
	for _, nt := range ManualKubernetesNodeTypes {
		if IsManualTenantScopedNodeType(nt) {
			t.Errorf("%s must not be declarable outside a Kubernetes account", nt)
		}
	}
	if IsManualNodeType(NodeTypePod) {
		t.Error("pods must not be declarable")
	}
}

func TestMaterializeManualNode(t *testing.T) {
	base := map[string]interface{}{"name": "orders", "region": "us-east-1", "subtype": "queue"}
	key := BuildUniqueKey(CloudProviderAWS, "acct", "us-east-1", NodeTypeMessageQueue, "", "orders")
	details := ManualNodeDetails{
		ManualNodeID: 7,
		Description:  "order events",
		Properties:   map[string]interface{}{"queue_type": "fifo"},
		Labels:       map[string]string{"team": "payments"},
	}

	a, err := MaterializeManualNode(NodeTypeMessageQueue, "SQSQueue", key, base, details, "tenant", "acct")
	if err != nil {
		t.Fatalf("MaterializeManualNode: %v", err)
	}
	b, _ := MaterializeManualNode(NodeTypeMessageQueue, "SQSQueue", key, base, details, "tenant", "acct")

	if a.ID != NodeIDFor(key, "tenant", "acct") || a.ID != b.ID {
		t.Errorf("id must be NodeIDFor(key) on every call: %s / %s", a.ID, b.ID)
	}
	if a.Source != ManualNodeSource || a.Properties["source"] != ManualNodeSource {
		t.Errorf("source = %q / %v, want %q", a.Source, a.Properties["source"], ManualNodeSource)
	}
	if a.SpecificType != "SQSQueue" {
		t.Errorf("specific_type = %q", a.SpecificType)
	}
	if a.Labels["team"] != "payments" {
		t.Errorf("labels not lifted: %v", a.Labels)
	}
	if a.Properties["description"] != "order events" || a.Properties["queue_type"] != "fifo" {
		t.Errorf("details not layered on: %v", a.Properties)
	}
	if a.Properties["manual_declared"] != true || a.Properties["manual_node_id"] != int64(7) {
		t.Errorf("declaration markers missing: %v", a.Properties)
	}
	if _, ok := base["source"]; ok {
		t.Error("MaterializeManualNode mutated the base properties")
	}

	for _, reserved := range []string{"name", "region", "vpc_id", "vpc_name_hierarchy", "namespace", "source"} {
		_, err := MaterializeManualNode(NodeTypeMessageQueue, "SQSQueue", key, base,
			ManualNodeDetails{Properties: map[string]interface{}{reserved: "x"}}, "tenant", "acct")
		if err == nil {
			t.Errorf("setting %q as an optional property must be refused: it is a key input or stamped", reserved)
		}
	}
}

func TestSchemaProperties_OffersOnlyWhatAUserCanKnow(t *testing.T) {
	RegisterSpecificTypeSchema(SpecificTypeSchema{
		SpecificType: "TestManualSchemaType",
		NodeType:     NodeTypeDatabase,
		Properties: []PropertyDef{
			{Name: "engine"},
			{Name: "annotations", Shape: PropertyShapeMap},
			{Name: "subnet_ids", Shape: PropertyShapeList},
			// Key inputs are left out.
			{Name: "vpc_id"}, {Name: "region"},
			// A form cannot express a list of objects.
			{Name: "ports", Shape: PropertyShapeRecords},
			// Only the collector knows these.
			{Name: "instance_state"}, {Name: "launch_time"}, {Name: "uid"}, {Name: "kind"},
		},
	})
	got := SchemaProperties("TestManualSchemaType")
	want := []ManualNodeProperty{{Name: "annotations", Shape: PropertyShapeMap}, {Name: "engine"}, {Name: "subnet_ids", Shape: PropertyShapeList}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SchemaProperties = %v, want %v", got, want)
	}
	if SchemaProperties("NoSuchType") != nil {
		t.Error("unknown type should have no properties")
	}
}

func TestIsManualObservedProperty(t *testing.T) {
	for _, k := range []string{"created_at", "launch_time", "creation_timestamp", "instance_state", "table_status", "last_modified", "phase", "k8s_workload_id", "cluster_ips", "earliest_restorable_time", "latest_ready_revision"} {
		if !IsManualObservedProperty(k) {
			t.Errorf("%q is only known to the collector and must not be declarable", k)
		}
	}
	for _, k := range []string{"engine", "engine_version", "instance_type", "replicas", "primary_image", "service_account_name", "external_ips", "dns_name", "preferred_backup_window", "creation_token"} {
		if IsManualObservedProperty(k) {
			t.Errorf("%q is something a user can know and must stay declarable", k)
		}
	}
}

func TestBaseProperties_DropsStampedKeys(t *testing.T) {
	n := &DbNode{Properties: map[string]interface{}{
		"name": "orders", "source": "aws", "labels": map[string]interface{}{"a": "b"}, "description": "x",
	}}
	got := BaseProperties(n)
	if len(got) != 1 || got["name"] != "orders" {
		t.Errorf("BaseProperties = %v, want only name", got)
	}
}

// A declared ExternalService is never folded into a cloud resource, even
// when a bridge edge points it at one.
func TestCollapse_DeclaredExternalServiceSurvives(t *testing.T) {
	wl := mkNode("wl1", NodeTypeWorkload, "payment-service")
	es := mkNode("es1", NodeTypeExternalService, "db.example.com")
	es.Source = ManualNodeSource
	db := mkNode("db1", NodeTypeDatabase, "prod-db")

	gotNodes, gotEdges, redirectCount := CollapseEnrichedExternalServices(
		[]*DbNode{wl, es, db},
		[]*DbEdge{
			mkEdge("e_calls", wl.ID, es.ID, RelationshipCalls),
			mkEdge("e_bridge", es.ID, db.ID, RelationshipRoutesThrough),
		},
		quietLogger(),
	)
	if redirectCount != 0 || len(gotNodes) != 3 {
		t.Fatalf("declared ES was collapsed: redirects=%d nodes=%v", redirectCount, nodeIDs(gotNodes))
	}
	edgeFor(t, gotEdges, wl.ID, es.ID, RelationshipCalls)
}

// The same fixture TestResolveGCPManagedServiceCalls_FirestoreSameAccount
// rewrites, except the stub was declared by a user.
func TestResolveGCPManagedServiceCalls_SkipsDeclaredStub(t *testing.T) {
	const acct = "acct-A"
	fs := gcpDataNode("fs-A", acct, NodeTypeDatabase, "(default)", "firestore")
	caller := &DbNode{ID: "svc-api", NodeType: NodeTypeServerlessFunction, CloudAccountID: acct, Source: "gcp"}
	stub := esStub("es-datastore", "GCP Datastore", acct)
	stub.Source = ManualNodeSource
	edge := callsEdge("e1", caller.ID, stub.ID, acct)

	outNodes, _, rewritten := ResolveGCPManagedServiceCalls([]*DbNode{fs, caller, stub}, []*DbEdge{edge}, nil)
	if rewritten != 0 || edge.DestinationNodeID != stub.ID {
		t.Fatalf("declared stub was repointed (%d rewrites, dest %s)", rewritten, edge.DestinationNodeID)
	}
	kept := false
	for _, n := range outNodes {
		kept = kept || n.ID == stub.ID
	}
	if !kept {
		t.Error("declared stub was dropped")
	}
}
