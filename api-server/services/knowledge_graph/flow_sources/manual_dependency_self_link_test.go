package flow_sources

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"nudgebee/services/knowledge_graph/core"
)

func TestSameManualEndpoint(t *testing.T) {
	base := ManualEndpoint{NodeType: "Workload", Name: "app-dev", Namespace: "nudgebee", Cluster: "iteration-prod"}
	same := base
	same.Name = "  app-dev  " // surrounding whitespace is not a different node
	if !sameManualEndpoint(base, same) {
		t.Errorf("endpoints differing only in whitespace treated as different nodes")
	}
	for name, other := range map[string]ManualEndpoint{
		"name":       {NodeType: "Workload", Name: "app-test", Namespace: "nudgebee", Cluster: "iteration-prod"},
		"node type":  {NodeType: "Pod", Name: "app-dev", Namespace: "nudgebee", Cluster: "iteration-prod"},
		"namespace":  {NodeType: "Workload", Name: "app-dev", Namespace: "default", Cluster: "iteration-prod"},
		"cluster":    {NodeType: "Workload", Name: "app-dev", Namespace: "nudgebee", Cluster: "iteration-dev"},
		"arn":        {NodeType: "Workload", Name: "app-dev", Namespace: "nudgebee", Cluster: "iteration-prod", ARN: "arn:x"},
		"account":    {NodeType: "Workload", Name: "app-dev", Namespace: "nudgebee", Cluster: "iteration-prod", AccountID: "123456789012"},
		"region":     {NodeType: "Workload", Name: "app-dev", Namespace: "nudgebee", Cluster: "iteration-prod", Region: "us-east-1"},
		"everything": {NodeType: "Database", Name: "ledger-db"},
	} {
		if sameManualEndpoint(base, other) {
			t.Errorf("endpoints differing in %s treated as the same node", name)
		}
	}
}

// A stored row whose two sides resolve to one node emits no edge and is
// marked invalid_payload, so a row written before the write-time guard (and
// the self-loop an earlier build drew from it) is cleaned up by a build.
func TestProcessRow_SelfLoopEmitsNoEdge(t *testing.T) {
	tenantID := uuid.NewString()
	src := &core.DbNode{ID: uuid.NewString(), TenantID: tenantID, CloudAccountID: uuid.NewString(), NodeType: core.NodeTypeWorkload}
	dst := &core.DbNode{ID: uuid.NewString(), TenantID: tenantID, CloudAccountID: src.CloudAccountID, NodeType: core.NodeTypeWorkload}
	nodeIndex := map[string]*core.DbNode{src.ID: src, dst.ID: dst}
	source := NewManualFlowSource(slog.Default())

	row := manualDependencyRow{
		ID: 1, TenantID: tenantID, RelationshipType: "CALLS",
		SourceNodeType: "Workload", SourceName: "app-dev",
		DestNodeType: "Workload", DestName: "app-dev",
		ResolvedSourceNodeID: src.ID, ResolvedDestNodeID: src.ID,
		ResolutionStatus: ManualResolutionResolved,
	}
	// resolver is nil: a row already resolved against nodes in this build's
	// index is rechecked without one.
	update, edge, err := source.processRow(context.Background(), nil, nodeIndex, row, tenantID)
	if err != nil {
		t.Fatalf("processRow: %v", err)
	}
	if edge != nil {
		t.Errorf("a self-loop edge was emitted: %s -> %s", edge.SourceNodeID, edge.DestinationNodeID)
	}
	if update.ResolutionStatus != ManualResolutionInvalidPayload {
		t.Errorf("resolution_status = %s, want %s", update.ResolutionStatus, ManualResolutionInvalidPayload)
	}
	if update.ResolutionError != ErrManualDependencySelfLink.Error() {
		t.Errorf("resolution_error = %q, want %q", update.ResolutionError, ErrManualDependencySelfLink.Error())
	}

	row.ResolvedDestNodeID = dst.ID
	update, edge, err = source.processRow(context.Background(), nil, nodeIndex, row, tenantID)
	if err != nil {
		t.Fatalf("processRow between two nodes: %v", err)
	}
	if edge == nil || update.ResolutionStatus != ManualResolutionResolved {
		t.Errorf("a declaration between two different nodes must still emit its edge (status %s)", update.ResolutionStatus)
	}
}
