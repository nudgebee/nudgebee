package sources

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

func githubUserNode(id, username string) *core.DbNode {
	return &core.DbNode{
		ID:           id,
		SpecificType: "GitHubUser",
		Properties:   map[string]interface{}{"username": username},
	}
}

func TestIndexIdentityNodes(t *testing.T) {
	nodes := []*core.DbNode{
		githubUserNode("gh-1", "priya-dev"),
		{ID: "aws-1", SpecificType: "EC2Instance", Properties: map[string]interface{}{"username": "irrelevant"}},
		{ID: "gh-2", SpecificType: "GitHubUser", Properties: map[string]interface{}{}}, // no username, must be skipped
	}

	index := indexIdentityNodes(nodes)

	if len(index) != 1 {
		t.Fatalf("expected 1 indexed identity node, got %d", len(index))
	}
	got, ok := index[identityKey{"GitHubUser", "priya-dev"}]
	if !ok {
		t.Fatal("expected GitHubUser/priya-dev to be indexed")
	}
	if got.ID != "gh-1" {
		t.Errorf("indexed node ID = %q, want gh-1", got.ID)
	}
}

func TestBuildSameAsEdges(t *testing.T) {
	canonicalByUserID := map[string]*core.DbNode{
		"u-111": {ID: "canonical-priya"},
	}
	identityIndex := map[identityKey]*core.DbNode{
		{"GitHubUser", "priya-dev"}: {ID: "gh-priya"},
	}

	t.Run("links a resolvable mapping", func(t *testing.T) {
		mappings := []mappingRow{
			{integrationType: "github", externalUserID: "priya-dev", mappedUserID: "u-111"},
		}
		edges := buildSameAsEdges(canonicalByUserID, mappings, identityIndex, "tenant-1")
		if len(edges) != 1 {
			t.Fatalf("expected 1 edge, got %d", len(edges))
		}
		e := edges[0]
		if e.SourceNodeID != "gh-priya" || e.DestinationNodeID != "canonical-priya" {
			t.Errorf("edge = %s -> %s, want gh-priya -> canonical-priya", e.SourceNodeID, e.DestinationNodeID)
		}
		if e.RelationshipType != core.RelationshipSameAs {
			t.Errorf("RelationshipType = %v, want SAME_AS", e.RelationshipType)
		}
	})

	t.Run("skips a mapping to a user not active this cycle", func(t *testing.T) {
		mappings := []mappingRow{
			{integrationType: "github", externalUserID: "priya-dev", mappedUserID: "u-999-unknown"},
		}
		edges := buildSameAsEdges(canonicalByUserID, mappings, identityIndex, "tenant-1")
		if len(edges) != 0 {
			t.Errorf("expected 0 edges for an unresolvable user, got %d", len(edges))
		}
	})

	t.Run("skips an integration type not yet wired into the KG", func(t *testing.T) {
		mappings := []mappingRow{
			{integrationType: "gitlab", externalUserID: "priya-dev", mappedUserID: "u-111"},
		}
		edges := buildSameAsEdges(canonicalByUserID, mappings, identityIndex, "tenant-1")
		if len(edges) != 0 {
			t.Errorf("expected 0 edges for an unwired integration type, got %d", len(edges))
		}
	})

	t.Run("skips a login with no matching identity node this cycle", func(t *testing.T) {
		mappings := []mappingRow{
			{integrationType: "github", externalUserID: "someone-else", mappedUserID: "u-111"},
		}
		edges := buildSameAsEdges(canonicalByUserID, mappings, identityIndex, "tenant-1")
		if len(edges) != 0 {
			t.Errorf("expected 0 edges when the identity node wasn't built this cycle, got %d", len(edges))
		}
	})
}

func TestNudgebeeUserSchemaRegistered(t *testing.T) {
	if _, ok := core.LookupSpecificTypeSchema("NudgebeeUser"); !ok {
		t.Error("expected NudgebeeUser specific_type schema to be registered")
	}
}
