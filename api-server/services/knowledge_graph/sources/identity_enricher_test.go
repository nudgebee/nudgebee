package sources

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

func githubUserNode(id, username, integrationID string) *core.DbNode {
	return &core.DbNode{
		ID:             id,
		SpecificType:   "GitHubUser",
		Properties:     map[string]interface{}{"username": username},
		CloudAccountID: integrationID,
	}
}

func TestIndexIdentityNodes(t *testing.T) {
	nodes := []*core.DbNode{
		githubUserNode("gh-1", "priya-dev", "integration-a"),
		{ID: "aws-1", SpecificType: "EC2Instance", Properties: map[string]interface{}{"username": "irrelevant"}},
		{ID: "gh-2", SpecificType: "GitHubUser", Properties: map[string]interface{}{}}, // no username, must be skipped
	}

	index := indexIdentityNodes(nodes)

	if len(index) != 1 {
		t.Fatalf("expected 1 indexed identity node, got %d", len(index))
	}
	got, ok := index[identityKey{"GitHubUser", "priya-dev", "integration-a"}]
	if !ok {
		t.Fatal("expected GitHubUser/priya-dev/integration-a to be indexed")
	}
	if got.ID != "gh-1" {
		t.Errorf("indexed node ID = %q, want gh-1", got.ID)
	}
}

// TestIndexIdentityNodes_SameUserAcrossTwoIntegrations is a regression test for a bug
// found in production (nb-34851 investigation): a tenant with two integrations of the
// same type (e.g. two PagerDuty accounts) where the same external user shows up in
// both used to collide on (specific_type, username) alone, silently dropping one of
// the two identity nodes from the index — so one of the two never got a SAME_AS edge
// even though its integration_user_accounts row was correctly mapped.
func TestIndexIdentityNodes_SameUserAcrossTwoIntegrations(t *testing.T) {
	nodes := []*core.DbNode{
		githubUserNode("gh-instance-a", "shared-login", "integration-a"),
		githubUserNode("gh-instance-b", "shared-login", "integration-b"),
	}

	index := indexIdentityNodes(nodes)

	if len(index) != 2 {
		t.Fatalf("expected both same-username nodes from different integrations to be indexed separately, got %d", len(index))
	}
	if got, ok := index[identityKey{"GitHubUser", "shared-login", "integration-a"}]; !ok || got.ID != "gh-instance-a" {
		t.Errorf("expected integration-a's node to be indexed as gh-instance-a, got %+v (ok=%v)", got, ok)
	}
	if got, ok := index[identityKey{"GitHubUser", "shared-login", "integration-b"}]; !ok || got.ID != "gh-instance-b" {
		t.Errorf("expected integration-b's node to be indexed as gh-instance-b, got %+v (ok=%v)", got, ok)
	}
}

func TestBuildSameAsEdges(t *testing.T) {
	canonicalByUserID := map[string]*core.DbNode{
		"u-111": {ID: "canonical-priya"},
	}
	identityIndex := map[identityKey]*core.DbNode{
		{"GitHubUser", "priya-dev", "integration-a"}: {ID: "gh-priya"},
	}

	t.Run("links a resolvable mapping", func(t *testing.T) {
		mappings := []mappingRow{
			{integrationType: "github", externalUserID: "priya-dev", mappedUserID: "u-111", integrationID: "integration-a"},
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
			{integrationType: "github", externalUserID: "priya-dev", mappedUserID: "u-999-unknown", integrationID: "integration-a"},
		}
		edges := buildSameAsEdges(canonicalByUserID, mappings, identityIndex, "tenant-1")
		if len(edges) != 0 {
			t.Errorf("expected 0 edges for an unresolvable user, got %d", len(edges))
		}
	})

	t.Run("skips an integration type not yet wired into the KG", func(t *testing.T) {
		mappings := []mappingRow{
			{integrationType: "gitlab", externalUserID: "priya-dev", mappedUserID: "u-111", integrationID: "integration-a"},
		}
		edges := buildSameAsEdges(canonicalByUserID, mappings, identityIndex, "tenant-1")
		if len(edges) != 0 {
			t.Errorf("expected 0 edges for an unwired integration type, got %d", len(edges))
		}
	})

	t.Run("skips a login with no matching identity node this cycle", func(t *testing.T) {
		mappings := []mappingRow{
			{integrationType: "github", externalUserID: "someone-else", mappedUserID: "u-111", integrationID: "integration-a"},
		}
		edges := buildSameAsEdges(canonicalByUserID, mappings, identityIndex, "tenant-1")
		if len(edges) != 0 {
			t.Errorf("expected 0 edges when the identity node wasn't built this cycle, got %d", len(edges))
		}
	})

}

// TestBuildSameAsEdges_IntegrationScoping covers the per-integration-instance scoping
// added for nb-34851: a tenant with two integrations of the same type (e.g. two
// PagerDuty accounts) where the same external user shows up in both, each correctly
// mapped to the same Nudgebee user. Both identity nodes must get their own SAME_AS
// edge — previously only one of the two ever did, because the identity index collided
// on (specific_type, username) alone.
func TestBuildSameAsEdges_IntegrationScoping(t *testing.T) {
	canonicalByUserID := map[string]*core.DbNode{
		"u-111": {ID: "canonical-priya"},
	}
	identityIndex := map[identityKey]*core.DbNode{
		{"GitHubUser", "priya-dev", "integration-a"}: {ID: "gh-priya"},
	}

	t.Run("skips a mapping whose integration_id doesn't match this identity node's instance", func(t *testing.T) {
		mappings := []mappingRow{
			{integrationType: "github", externalUserID: "priya-dev", mappedUserID: "u-111", integrationID: "integration-b"},
		}
		edges := buildSameAsEdges(canonicalByUserID, mappings, identityIndex, "tenant-1")
		if len(edges) != 0 {
			t.Errorf("expected 0 edges when the mapping is for a different integration instance, got %d", len(edges))
		}
	})

	t.Run("links both instances when the same user appears in two integrations of the same type", func(t *testing.T) {
		twoInstanceIndex := map[identityKey]*core.DbNode{
			{"PagerDutyUser", "PD1234X", "integration-a"}: {ID: "pd-instance-a"},
			{"PagerDutyUser", "PD1234X", "integration-b"}: {ID: "pd-instance-b"},
		}
		mappings := []mappingRow{
			{integrationType: "pagerduty", externalUserID: "PD1234X", mappedUserID: "u-111", integrationID: "integration-a"},
			{integrationType: "pagerduty", externalUserID: "PD1234X", mappedUserID: "u-111", integrationID: "integration-b"},
		}
		edges := buildSameAsEdges(canonicalByUserID, mappings, twoInstanceIndex, "tenant-1")
		if len(edges) != 2 {
			t.Fatalf("expected 2 edges (one per integration instance), got %d", len(edges))
		}
		sources := map[string]bool{edges[0].SourceNodeID: true, edges[1].SourceNodeID: true}
		if !sources["pd-instance-a"] || !sources["pd-instance-b"] {
			t.Errorf("expected edges from both pd-instance-a and pd-instance-b, got sources %v", sources)
		}
	})
}

func TestNudgebeeUserSchemaRegistered(t *testing.T) {
	if _, ok := core.LookupSpecificTypeSchema("NudgebeeUser"); !ok {
		t.Error("expected NudgebeeUser specific_type schema to be registered")
	}
}
