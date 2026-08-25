package core

// Equivalence guard for the directional BFS that replaced the visited_path recursive
// CTE in discoverDirectional (kg_traverse / impact analysis). Like the neighbour guard,
// the BFS and the old CTE use different algorithms but MUST return the identical
// discovered-node set, per-node minimum depth, and walked-edge set — across every
// direction (upstream / downstream / both) and the relationship-type + exclude-type
// filters. Reuses the diamond+cycle fixture from discover_neighbor_bfs_equivalence_test.go.
//
// DB-gated: skips cleanly via testenv.RequireMetastore when no Postgres is reachable.

import (
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/lib/pq"

	"nudgebee/services/internal/testenv"
)

// referenceDiscoverDirectionalCTE runs the previous directional visited-path recursive
// CTE as an independent oracle: per-node minimum depth + the walked-edge id set. It has
// no tenant filter, matching the old discoverDirectional.
func referenceDiscoverDirectionalCTE(t *testing.T, s *Service, seeds []string, direction TraverseDirection, levels int, relTypes []string, excludeTypes []NodeType) (map[string]int, map[string]struct{}) {
	t.Helper()
	var edgeJoin, nextNode string
	switch direction {
	case TraverseDirectionDownstream:
		edgeJoin = "e.source_node_id = t.node_id"
		nextNode = "e.destination_node_id"
	case TraverseDirectionUpstream:
		edgeJoin = "e.destination_node_id = t.node_id"
		nextNode = "e.source_node_id"
	default:
		edgeJoin = "(e.source_node_id = t.node_id OR e.destination_node_id = t.node_id)"
		nextNode = "CASE WHEN e.source_node_id = t.node_id THEN e.destination_node_id ELSE e.source_node_id END"
	}
	args := []interface{}{pq.Array(seeds), levels}
	argIdx := 3
	var filters []string
	if len(relTypes) > 0 {
		filters = append(filters, fmt.Sprintf("e.relationship_type = ANY($%d::text[])", argIdx))
		args = append(args, pq.Array(relTypes))
		argIdx++
	}
	if len(excludeTypes) > 0 {
		strs := make([]string, len(excludeTypes))
		for i, nt := range excludeTypes {
			strs[i] = string(nt)
		}
		filters = append(filters, fmt.Sprintf("NOT n.node_type = ANY($%d::text[])", argIdx))
		args = append(args, pq.Array(strs))
		argIdx++
	}
	_ = argIdx // keep the counter advanced for whatever clause is appended next
	extra := ""
	if len(filters) > 0 {
		extra = " AND " + strings.Join(filters, " AND ")
	}
	q := fmt.Sprintf(`
WITH RECURSIVE traversal AS (
  SELECT id AS node_id, NULL::uuid AS edge_id, 0 AS depth, ARRAY[id] AS visited
  FROM knowledge_graph_node
  WHERE id = ANY($1::uuid[]) AND level='Tenant' AND is_active=true
  UNION
  SELECT %s AS node_id, e.id, t.depth+1, t.visited || %s
  FROM traversal t
  JOIN knowledge_graph_edge e ON %s
  JOIN knowledge_graph_node n ON n.id = %s
  WHERE t.depth < $2 AND e.level='Tenant' AND e.is_active=true
    AND n.level='Tenant' AND n.is_active=true
    AND NOT (%s = ANY(t.visited))%s
)
SELECT DISTINCT node_id::text, edge_id::text, depth FROM traversal`, nextNode, nextNode, edgeJoin, nextNode, nextNode, extra)

	rows, err := s.dbManager.Query(q, args...)
	if err != nil {
		t.Fatalf("reference directional CTE query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	minDepth := map[string]int{}
	edgeSet := map[string]struct{}{}
	for rows.Next() {
		var nid string
		var eid sql.NullString
		var d int
		if err := rows.Scan(&nid, &eid, &d); err != nil {
			t.Fatalf("reference directional CTE scan: %v", err)
		}
		if cur, ok := minDepth[nid]; !ok || d < cur {
			minDepth[nid] = d
		}
		if eid.Valid {
			edgeSet[eid.String] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reference directional CTE rows: %v", err)
	}
	return minDepth, edgeSet
}

func TestDiscoverDirectional_BFS_MatchesReferenceCTE(t *testing.T) {
	dbm := testenv.RequireMetastore(t)
	svc := NewService(newTestRequestContext(), slog.New(slog.NewTextHandler(io.Discard, nil)), dbm)

	nodes, edges, byName := bfsEqBuildGraph()
	bfsEqCleanup(t, svc) // clean start in case a prior run was interrupted
	t.Cleanup(func() { bfsEqCleanup(t, svc) })
	if err := svc.SaveNodes(nodes, 1); err != nil {
		t.Fatalf("SaveNodes: %v", err)
	}
	if err := svc.SaveEdges(edges, nodes, 1); err != nil {
		t.Fatalf("SaveEdges: %v", err)
	}
	id := func(name string) string { return byName[name].ID }

	cases := []struct {
		name      string
		seeds     []string
		direction TraverseDirection
		levels    int
		relTypes  []string
		exclude   []NodeType
	}{
		{"downstream_A_l1", []string{id("A")}, TraverseDirectionDownstream, 1, nil, nil},
		{"downstream_A_l3", []string{id("A")}, TraverseDirectionDownstream, 3, nil, nil},
		{"upstream_F_l3", []string{id("F")}, TraverseDirectionUpstream, 3, nil, nil},
		{"upstream_D_l2", []string{id("D")}, TraverseDirectionUpstream, 2, nil, nil},
		{"both_D_l2", []string{id("D")}, TraverseDirectionBoth, 2, nil, nil},
		{"downstream_B_l3_callsOnly", []string{id("B")}, TraverseDirectionDownstream, 3, []string{string(RelationshipCalls)}, nil},
		{"downstream_A_l3_excludeService", []string{id("A")}, TraverseDirectionDownstream, 3, nil, []NodeType{NodeTypeService}},
		{"multiSeed_AF_both_l2", []string{id("A"), id("F")}, TraverseDirectionBoth, 2, nil, nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantDepth, wantEdges := referenceDiscoverDirectionalCTE(t, svc, c.seeds, c.direction, c.levels, c.relTypes, c.exclude)

			gotIDs, gotEdges, gotDepth, err := svc.discoverBFS(c.seeds, traverseOptions{
				Direction:         c.direction,
				Levels:            c.levels,
				RelationshipTypes: c.relTypes,
				ExcludeNodeTypes:  c.exclude,
			})
			if err != nil {
				t.Fatalf("discoverBFS: %v", err)
			}

			if len(gotIDs) != len(gotDepth) {
				t.Errorf("discoveredIDs (%d) != nodeMinDepth keys (%d)", len(gotIDs), len(gotDepth))
			}
			if !reflect.DeepEqual(gotDepth, wantDepth) {
				t.Errorf("nodeMinDepth mismatch:\n got  = %v\n want = %v", gotDepth, wantDepth)
			}
			gotEdgeSet := make(map[string]struct{}, len(gotEdges))
			for _, e := range gotEdges {
				gotEdgeSet[e] = struct{}{}
			}
			if !reflect.DeepEqual(gotEdgeSet, wantEdges) {
				t.Errorf("traversedEdgeIDs mismatch: got %d edges, want %d\n got=%v\nwant=%v",
					len(gotEdgeSet), len(wantEdges), gotEdgeSet, wantEdges)
			}
		})
	}
}

// TestDiscoverBFS_SkipsActiveEdgeToInactiveNode proves the load-bearing endpoint-status
// check: an active edge can point at a tombstoned node (node tombstoning does not
// cascade to edges), and the traversal must surface neither that node nor the edge.
func TestDiscoverBFS_SkipsActiveEdgeToInactiveNode(t *testing.T) {
	dbm := testenv.RequireMetastore(t)
	svc := NewService(newTestRequestContext(), slog.New(slog.NewTextHandler(io.Discard, nil)), dbm)

	mkNode := func(name string, nt NodeType) *DbNode {
		uniqueKey := fmt.Sprintf("k8s:%s::%s::%s", bfsEqAccount, nt, name)
		return NewNode(nt, uniqueKey, map[string]interface{}{"name": name}, bfsEqTenant, bfsEqAccount, "test")
	}
	a := mkNode("TA", NodeTypeWorkload)
	b := mkNode("TB", NodeTypePod)
	g := mkNode("TG", NodeTypeService)
	nodes := []*DbNode{a, b, g}
	mkEdge := func(from, to *DbNode, rel RelationshipType) *DbEdge {
		e := NewEdge(from.ID, to.ID, rel, map[string]interface{}{}, bfsEqTenant, bfsEqAccount, "test")
		e.IsActive = true
		e.LastSyncVersion = 1
		return e
	}
	edges := []*DbEdge{mkEdge(a, b, RelationshipRunsOn), mkEdge(b, g, RelationshipCalls)}

	bfsEqCleanup(t, svc)
	t.Cleanup(func() { bfsEqCleanup(t, svc) })
	if err := svc.SaveNodes(nodes, 1); err != nil {
		t.Fatalf("SaveNodes: %v", err)
	}
	if err := svc.SaveEdges(edges, nodes, 1); err != nil {
		t.Fatalf("SaveEdges: %v", err)
	}
	// Tombstone G but leave the active B->G edge in place.
	if _, err := svc.dbManager.Exec("UPDATE knowledge_graph_node SET is_active=false WHERE id=$1::uuid", g.ID); err != nil {
		t.Fatalf("tombstone G: %v", err)
	}

	gotIDs, gotEdges, _, err := svc.discoverBFS([]string{a.ID}, traverseOptions{
		Direction: TraverseDirectionDownstream,
		Levels:    3,
	})
	if err != nil {
		t.Fatalf("discoverBFS: %v", err)
	}
	got := make(map[string]struct{}, len(gotIDs))
	for _, x := range gotIDs {
		got[x] = struct{}{}
	}
	if _, ok := got[g.ID]; ok {
		t.Errorf("tombstoned node G must not be discovered")
	}
	if _, ok := got[b.ID]; !ok {
		t.Errorf("active node B should be discovered")
	}
	if len(gotIDs) != 2 {
		t.Errorf("expected exactly {A,B} discovered, got %d: %v", len(gotIDs), gotIDs)
	}
	// Only the A->B edge may be walked; the B->G edge points at a tombstoned node.
	if len(gotEdges) != 1 {
		t.Errorf("expected exactly 1 walked edge (A->B), got %d", len(gotEdges))
	}
}
