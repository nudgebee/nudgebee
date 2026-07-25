package sources

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/ownership"
)

func TestBuildMemberOfEdges(t *testing.T) {
	userNodeIDs := map[string]string{"u-111": "canonical-priya"}
	groupNodes := map[string]*core.DbNode{"g-1": {ID: "group-node-1"}}

	t.Run("links a resolvable membership", func(t *testing.T) {
		memberships := []membershipRow{{userID: "u-111", groupID: "g-1"}}
		edges := buildMemberOfEdges(userNodeIDs, groupNodes, memberships, "tenant-1")
		if len(edges) != 1 {
			t.Fatalf("expected 1 edge, got %d", len(edges))
		}
		e := edges[0]
		if e.SourceNodeID != "canonical-priya" || e.DestinationNodeID != "group-node-1" {
			t.Errorf("edge = %s -> %s, want canonical-priya -> group-node-1", e.SourceNodeID, e.DestinationNodeID)
		}
		if e.RelationshipType != core.RelationshipMemberOf {
			t.Errorf("RelationshipType = %v, want MEMBER_OF", e.RelationshipType)
		}
	})

	t.Run("skips a membership for a user not active this cycle", func(t *testing.T) {
		memberships := []membershipRow{{userID: "u-999-unknown", groupID: "g-1"}}
		edges := buildMemberOfEdges(userNodeIDs, groupNodes, memberships, "tenant-1")
		if len(edges) != 0 {
			t.Errorf("expected 0 edges for an unresolvable user, got %d", len(edges))
		}
	})

	t.Run("skips a membership for a group not found this cycle", func(t *testing.T) {
		memberships := []membershipRow{{userID: "u-111", groupID: "g-999-unknown"}}
		edges := buildMemberOfEdges(userNodeIDs, groupNodes, memberships, "tenant-1")
		if len(edges) != 0 {
			t.Errorf("expected 0 edges for an unresolvable group, got %d", len(edges))
		}
	})
}

func TestCollectOwnableResources(t *testing.T) {
	nodes := []*core.DbNode{
		{
			ID: "workload-1", NodeType: core.NodeTypeWorkload, Source: "k8s",
			Properties: map[string]interface{}{"k8s_workload_id": "wl-1"},
		},
		{
			ID: "namespace-1", NodeType: core.NodeTypeNamespace, Source: "k8s",
			CloudAccountID: "acct-1",
			Properties:     map[string]interface{}{"name": "prod"},
		},
		{
			ID: "cluster-1", NodeType: core.NodeTypeCluster, Source: "k8s",
			CloudAccountID: "acct-1",
			Properties:     map[string]interface{}{},
		},
		{
			ID: "ec2-1", NodeType: core.NodeTypeComputeInstance, Source: "aws",
			Properties: map[string]interface{}{"nb_resource_id": "cr-1"},
		},
		{
			// Same source/property shape as a real cloud resource, but no
			// nb_resource_id — must be skipped (defensive, not expected in practice).
			ID: "ec2-2", NodeType: core.NodeTypeComputeInstance, Source: "aws",
			Properties: map[string]interface{}{},
		},
		{
			// k8s-sourced node that happens to carry an irrelevant property —
			// not aws/gcp/azure sourced, so must not be treated as a cloud resource.
			ID: "pod-1", NodeType: core.NodeTypePod, Source: "k8s",
			Properties: map[string]interface{}{"k8s_workload_id": "wl-1"},
		},
	}

	idx := collectOwnableResources(nodes)

	if len(idx.refs) != 4 {
		t.Fatalf("expected 4 resource refs, got %d: %+v", len(idx.refs), idx.refs)
	}

	cases := []struct {
		resourceType string
		resourceKey  string
		wantNodeID   string
	}{
		{ownership.ResourceTypeWorkload, "wl-1", "workload-1"},
		{ownership.ResourceTypeNamespace, "acct-1/prod", "namespace-1"},
		{ownership.ResourceTypeCloudAccount, "acct-1", "cluster-1"},
		{ownership.ResourceTypeCloudResource, "cr-1", "ec2-1"},
	}
	for _, c := range cases {
		node, ok := idx.nodes[c.resourceType+"\x00"+c.resourceKey]
		if !ok {
			t.Errorf("expected an indexed node for %s/%s", c.resourceType, c.resourceKey)
			continue
		}
		if node.ID != c.wantNodeID {
			t.Errorf("%s/%s -> node %q, want %q", c.resourceType, c.resourceKey, node.ID, c.wantNodeID)
		}
	}
}

func TestBuildOwnsEdges(t *testing.T) {
	idx := ownableResourceIndex{
		nodes: map[string]*core.DbNode{
			ownership.ResourceTypeWorkload + "\x00wl-1": {ID: "workload-node-1"},
		},
	}
	userNodeIDs := map[string]string{"u-1": "user-node-1"}
	groupNodes := map[string]*core.DbNode{"g-1": {ID: "group-node-1"}}

	t.Run("emits an edge for a found user owner", func(t *testing.T) {
		results := []ownership.OwnerResult{
			{
				ResourceType: ownership.ResourceTypeWorkload, ResourceKey: "wl-1", Found: true,
				OwnerType: ownership.OwnerTypeUser, OwnerId: "u-1",
				Source: ownership.SourceManual, Via: ownership.ViaSelf,
			},
		}
		edges := buildOwnsEdges(results, idx, userNodeIDs, groupNodes, "tenant-1")
		if len(edges) != 1 {
			t.Fatalf("expected 1 edge, got %d", len(edges))
		}
		e := edges[0]
		if e.SourceNodeID != "user-node-1" || e.DestinationNodeID != "workload-node-1" {
			t.Errorf("edge = %s -> %s, want user-node-1 -> workload-node-1", e.SourceNodeID, e.DestinationNodeID)
		}
		if e.RelationshipType != core.RelationshipOwns {
			t.Errorf("RelationshipType = %v, want OWNS", e.RelationshipType)
		}
		if e.Properties["source"] != ownership.SourceManual || e.Properties["via"] != ownership.ViaSelf {
			t.Errorf("edge properties = %+v, want source/via provenance", e.Properties)
		}
	})

	t.Run("emits an edge for a found group owner", func(t *testing.T) {
		results := []ownership.OwnerResult{
			{
				ResourceType: ownership.ResourceTypeWorkload, ResourceKey: "wl-1", Found: true,
				OwnerType: ownership.OwnerTypeGroup, OwnerId: "g-1",
				Source: ownership.SourceRule, Via: ownership.ViaNamespace,
			},
		}
		edges := buildOwnsEdges(results, idx, userNodeIDs, groupNodes, "tenant-1")
		if len(edges) != 1 {
			t.Fatalf("expected 1 edge, got %d", len(edges))
		}
		if edges[0].SourceNodeID != "group-node-1" {
			t.Errorf("SourceNodeID = %q, want group-node-1", edges[0].SourceNodeID)
		}
	})

	t.Run("skips an unresolved result", func(t *testing.T) {
		results := []ownership.OwnerResult{
			{ResourceType: ownership.ResourceTypeWorkload, ResourceKey: "wl-1", Found: false},
		}
		edges := buildOwnsEdges(results, idx, userNodeIDs, groupNodes, "tenant-1")
		if len(edges) != 0 {
			t.Errorf("expected 0 edges for an unresolved result, got %d", len(edges))
		}
	})

	t.Run("skips a resource not indexed this cycle", func(t *testing.T) {
		results := []ownership.OwnerResult{
			{
				ResourceType: ownership.ResourceTypeWorkload, ResourceKey: "wl-unknown", Found: true,
				OwnerType: ownership.OwnerTypeUser, OwnerId: "u-1",
			},
		}
		edges := buildOwnsEdges(results, idx, userNodeIDs, groupNodes, "tenant-1")
		if len(edges) != 0 {
			t.Errorf("expected 0 edges for a resource missing from the index, got %d", len(edges))
		}
	})

	t.Run("skips an owner not resolvable this cycle", func(t *testing.T) {
		results := []ownership.OwnerResult{
			{
				ResourceType: ownership.ResourceTypeWorkload, ResourceKey: "wl-1", Found: true,
				OwnerType: ownership.OwnerTypeUser, OwnerId: "u-unknown",
			},
		}
		edges := buildOwnsEdges(results, idx, userNodeIDs, groupNodes, "tenant-1")
		if len(edges) != 0 {
			t.Errorf("expected 0 edges for an unresolvable owner, got %d", len(edges))
		}
	})
}

func TestNudgebeeGroupSchemaRegistered(t *testing.T) {
	if _, ok := core.LookupSpecificTypeSchema("NudgebeeGroup"); !ok {
		t.Error("expected NudgebeeGroup specific_type schema to be registered")
	}
}
