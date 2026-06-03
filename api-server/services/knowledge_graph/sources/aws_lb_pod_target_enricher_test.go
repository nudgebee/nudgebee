package sources

import (
	"io"
	"log/slog"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// TestAWSLBPodTargetEnricherRegistered asserts the init() block in
// aws_lb_pod_target_enricher.go registered the factory with the global
// cross-source enricher registry under the expected name. RegisterAllEnrichersToService
// drives off this registry, so without this assertion a typo in init() would
// silently disappear the enricher from the build pipeline.
func TestAWSLBPodTargetEnricherRegistered(t *testing.T) {
	enricher, err := CreateCrossSourceEnricher("aws_lb_pod_target", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("CreateCrossSourceEnricher(aws_lb_pod_target) error: %v", err)
	}
	if enricher == nil {
		t.Fatal("expected non-nil enricher")
	}
	if got := enricher.GetName(); got != "aws_lb_pod_target" {
		t.Errorf("GetName() = %q, want %q", got, "aws_lb_pod_target")
	}
}

// TestAWSLBPodTargetEnricher_NoLBs covers the early-exit when the unified
// graph contains no LoadBalancer nodes from aws_source. Must return the input
// slices unchanged and no error — otherwise every tenant without AWS LBs
// pays a cost they shouldn't.
func TestAWSLBPodTargetEnricherNoLBs(t *testing.T) {
	e := NewLoadBalancerPodTargetEnricher(slog.New(slog.NewTextHandler(io.Discard, nil)))

	nodes := []*core.DbNode{
		{ID: "k8s-svc-1", NodeType: core.NodeTypeK8sService, Source: "k8s"},
		{ID: "rds-1", NodeType: core.NodeTypeDatabase, Source: "aws"},
	}
	edges := []*core.DbEdge{
		{ID: "e1", SourceNodeID: "k8s-svc-1", DestinationNodeID: "rds-1"},
	}

	gotNodes, gotEdges, err := e.EnrichCrossSources(nil, nodes, edges, "tenant-1")
	if err != nil {
		t.Fatalf("EnrichCrossSources error: %v", err)
	}
	if len(gotNodes) != len(nodes) {
		t.Errorf("nodes count changed: got %d, want %d", len(gotNodes), len(nodes))
	}
	if len(gotEdges) != len(edges) {
		t.Errorf("edges count changed: got %d, want %d", len(gotEdges), len(edges))
	}
}

// TestAWSLBPodTargetEnricher_FilterCorrectness asserts that only LB nodes
// from the aws source are picked up — k8s-source LB nodes (synthesized
// elsewhere) and aws-source non-LB nodes must not be processed.
func TestAWSLBPodTargetEnricherFilterCorrectness(t *testing.T) {
	e := NewLoadBalancerPodTargetEnricher(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Three nodes that should all be IGNORED by the LB-target filter:
	//  - k8s-source LB (wrong source)
	//  - aws-source RDS (wrong type)
	//  - aws-source LB without aws_account_id and without CloudAccountID (skipped)
	nodes := []*core.DbNode{
		{ID: "lb-from-k8s", NodeType: core.NodeTypeLoadBalancer, Source: "k8s",
			Properties: map[string]interface{}{"aws_account_id": "111", "arn": "arn:x", "region": "us-east-1"}},
		{ID: "rds", NodeType: core.NodeTypeDatabase, Source: "aws"},
		{ID: "lb-no-acct", NodeType: core.NodeTypeLoadBalancer, Source: "aws",
			Properties: map[string]interface{}{"name": "orphan"}},
	}

	gotNodes, gotEdges, err := e.EnrichCrossSources(nil, nodes, nil, "tenant-1")
	if err != nil {
		t.Fatalf("EnrichCrossSources error: %v", err)
	}
	// No new nodes/edges should be emitted — the third node would have qualified
	// by source+type but is skipped because it has no aws_account_id / no
	// CloudAccountID. Net: input slices returned unchanged in length.
	if len(gotNodes) != len(nodes) {
		t.Errorf("nodes count changed: got %d, want %d", len(gotNodes), len(nodes))
	}
	if len(gotEdges) != 0 {
		t.Errorf("edges count: got %d, want 0", len(gotEdges))
	}
}

// TestExtractDeploymentFromReplicaSet sanity-checks the helper PR-2 lifted
// implicitly by reusing the existing aws_lb_k8s_enricher.go copy.
func TestExtractDeploymentFromReplicaSetReusedHelper(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"my-deploy-7d8f9c4b6", "my-deploy"},
		{"single", "single"},
		{"name-with-no-hash", "name-with-no-hash"},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := extractDeploymentFromReplicaSet(tc.in); got != tc.want {
				t.Errorf("extractDeploymentFromReplicaSet(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
