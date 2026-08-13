package core

import "testing"

func TestParseImageRef(t *testing.T) {
	cases := []struct {
		ref                                 string
		wantReg, wantRepo, wantTag, wantDig string
	}{
		{"nginx", "docker.io", "nginx", "latest", ""},
		{"nginx:1.21", "docker.io", "nginx", "1.21", ""},
		{"library/nginx:1.21", "docker.io", "library/nginx", "1.21", ""},
		{"123456789012.dkr.ecr.us-east-1.amazonaws.com/my-app:v2", "123456789012.dkr.ecr.us-east-1.amazonaws.com", "my-app", "v2", ""},
		{"123456789012.dkr.ecr.us-east-1.amazonaws.com/my-app@sha256:abc", "123456789012.dkr.ecr.us-east-1.amazonaws.com", "my-app", "", "sha256:abc"},
		{"registry:5000/team/app:v1", "registry:5000", "team/app", "v1", ""},
	}
	for _, c := range cases {
		reg, repo, tag, dig := ParseImageRef(c.ref)
		if reg != c.wantReg || repo != c.wantRepo || tag != c.wantTag || dig != c.wantDig {
			t.Errorf("ParseImageRef(%q) = (%q,%q,%q,%q), want (%q,%q,%q,%q)",
				c.ref, reg, repo, tag, dig, c.wantReg, c.wantRepo, c.wantTag, c.wantDig)
		}
	}
}

func TestSynthesizeContainerImages(t *testing.T) {
	// Two users share nginx:1.21 (dedup to one node); one also runs my-app:v2.
	u1 := &DbNode{ID: "u1", NodeType: NodeTypeWorkload, Properties: map[string]interface{}{"refs": []string{"nginx:1.21", "sidecar:latest"}}}
	u2 := &DbNode{ID: "u2", NodeType: NodeTypeServerlessFunction, Properties: map[string]interface{}{"refs": []string{"nginx:1.21", "123.dkr.ecr.us-east-1.amazonaws.com/my-app:v2"}}}
	refsOf := func(n *DbNode) []string {
		v, _ := n.Properties["refs"].([]string)
		return v
	}

	nodes, edges := SynthesizeContainerImages([]*DbNode{u1, u2}, refsOf, CloudProviderAWS, "t1", "a1", "aws")

	if len(nodes) != 3 { // nginx, sidecar, my-app
		t.Fatalf("expected 3 deduped ContainerImage nodes, got %d", len(nodes))
	}
	for _, n := range nodes {
		if n.NodeType != NodeTypeContainerImage || n.SpecificType != "ContainerImage" {
			t.Errorf("node %v: type=%s specific=%s, want ContainerImage", n.Properties["name"], n.NodeType, n.SpecificType)
		}
	}
	if len(edges) != 4 { // u1→nginx, u1→sidecar, u2→nginx, u2→my-app
		t.Fatalf("expected 4 USES_IMAGE edges, got %d", len(edges))
	}

	var nginxID string
	for _, n := range nodes {
		if n.Properties["name"] == "nginx" {
			nginxID = n.ID
		}
	}
	var u1Nginx, u2Nginx bool
	for _, e := range edges {
		if e.RelationshipType != RelationshipUsesImage {
			t.Errorf("edge rel = %s, want USES_IMAGE", e.RelationshipType)
		}
		if e.DestinationNodeID == nginxID && e.SourceNodeID == "u1" {
			u1Nginx = true
		}
		if e.DestinationNodeID == nginxID && e.SourceNodeID == "u2" {
			u2Nginx = true
		}
	}
	if !u1Nginx || !u2Nginx {
		t.Error("both users should USES_IMAGE the single deduped nginx node")
	}
}
