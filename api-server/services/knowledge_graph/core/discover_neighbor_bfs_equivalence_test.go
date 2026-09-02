package core

// Equivalence guard for the Go BFS that replaced the visited_path recursive CTE in
// discoverNeighborNodesRecursive. The BFS and the old CTE use different algorithms
// (global-visited vertex expansion vs per-path walk enumeration) but MUST return the
// identical result: same discovered node set, same per-node minimum depth, same
// walked-edge set. This test seeds a small graph containing a diamond and a cycle
// (the shapes where the two algorithms diverge in work) and asserts they agree.
//
// DB-gated: skips cleanly via testenv.RequireMetastore when no Postgres is reachable.

import (
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/lib/pq"

	"nudgebee/services/internal/testenv"
)

const (
	bfsEqTenant  = "b1a5e000-0000-4000-8000-000000000001"
	bfsEqAccount = "b1a5e000-0000-4000-8000-0000000000a1"
)

// bfsEqBuildGraph builds a deterministic topology exercising multi-path reachability:
//
//	A ── B ── D ── E ── F
//	│   ╱│   ╱
//	└─ C ┘   (B-C sibling edge → cycle A-B-C-A; D reachable via both B and C)
//
// Returns the nodes, edges, and a name→node lookup.
func bfsEqBuildGraph() ([]*DbNode, []*DbEdge, map[string]*DbNode) {
	types := map[string]NodeType{
		"A": NodeTypeWorkload, "B": NodeTypePod, "C": NodeTypePod,
		"D": NodeTypeService, "E": NodeTypeWorkload, "F": NodeTypePod,
	}
	byName := make(map[string]*DbNode, len(types))
	nodes := make([]*DbNode, 0, len(types))
	for _, name := range []string{"A", "B", "C", "D", "E", "F"} {
		nt := types[name]
		uniqueKey := fmt.Sprintf("k8s:%s::%s::%s", bfsEqAccount, nt, name)
		n := NewNode(nt, uniqueKey, map[string]interface{}{"name": name}, bfsEqTenant, bfsEqAccount, "test")
		byName[name] = n
		nodes = append(nodes, n)
	}
	mk := func(from, to string, rel RelationshipType) *DbEdge {
		e := NewEdge(byName[from].ID, byName[to].ID, rel, map[string]interface{}{}, bfsEqTenant, bfsEqAccount, "test")
		e.IsActive = true
		e.LastSyncVersion = 1
		return e
	}
	edges := []*DbEdge{
		mk("A", "B", RelationshipRunsOn),
		mk("A", "C", RelationshipRunsOn),
		mk("B", "D", RelationshipCalls),
		mk("C", "D", RelationshipCalls),
		mk("D", "E", RelationshipCalls),
		mk("B", "C", RelationshipBelongsTo),
		mk("E", "F", RelationshipManages),
	}
	return nodes, edges, byName
}

func bfsEqCleanup(t *testing.T, s *Service) {
	t.Helper()
	for _, tbl := range []string{"knowledge_graph_edge", "knowledge_graph_node"} {
		if _, err := s.dbManager.Exec("DELETE FROM "+tbl+" WHERE tenant_id = $1::uuid", bfsEqTenant); err != nil {
			t.Logf("bfs-eq cleanup: delete from %s: %v", tbl, err)
		}
	}
}

// referenceDiscoverCTE runs the previous visited_path recursive-CTE algorithm as an
// independent oracle and returns per-node minimum depth + the walked-edge id set.
func referenceDiscoverCTE(t *testing.T, s *Service, tenantID string, nodeIDs []string, levels int, nodeTypes []NodeType) (map[string]int, map[string]struct{}) {
	t.Helper()
	typeFilter := ""
	args := []interface{}{pq.Array(nodeIDs), tenantID, levels}
	if len(nodeTypes) > 0 {
		strs := make([]string, len(nodeTypes))
		for i, nt := range nodeTypes {
			strs[i] = string(nt)
		}
		typeFilter = " AND n.node_type = ANY($4::text[])"
		args = append(args, pq.Array(strs))
	}
	q := fmt.Sprintf(`
WITH RECURSIVE nt AS (
  SELECT id AS node_id, NULL::uuid AS edge_id, 0 AS depth, ARRAY[id] AS visited_path
  FROM knowledge_graph_node
  WHERE id = ANY($1::uuid[]) AND tenant_id=$2 AND level='Tenant' AND is_active=true
  UNION
  SELECT CASE WHEN e.source_node_id=nt.node_id THEN e.destination_node_id ELSE e.source_node_id END,
         e.id, nt.depth+1,
         nt.visited_path || CASE WHEN e.source_node_id=nt.node_id THEN e.destination_node_id ELSE e.source_node_id END
  FROM nt
  JOIN knowledge_graph_edge e ON (e.source_node_id=nt.node_id OR e.destination_node_id=nt.node_id)
  JOIN knowledge_graph_node n ON n.id = CASE WHEN e.source_node_id=nt.node_id THEN e.destination_node_id ELSE e.source_node_id END
  WHERE nt.depth < $3 AND e.tenant_id=$2 AND e.level='Tenant' AND e.is_active=true
    AND n.tenant_id=$2 AND n.level='Tenant' AND n.is_active=true%s
    AND NOT (CASE WHEN e.source_node_id=nt.node_id THEN e.destination_node_id ELSE e.source_node_id END = ANY(nt.visited_path))
)
SELECT DISTINCT node_id::text, edge_id::text, depth FROM nt`, typeFilter)

	rows, err := s.dbManager.Query(q, args...)
	if err != nil {
		t.Fatalf("reference CTE query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	minDepth := map[string]int{}
	edgeSet := map[string]struct{}{}
	for rows.Next() {
		var nid string
		var eid sql.NullString
		var d int
		if err := rows.Scan(&nid, &eid, &d); err != nil {
			t.Fatalf("reference CTE scan: %v", err)
		}
		if cur, ok := minDepth[nid]; !ok || d < cur {
			minDepth[nid] = d
		}
		if eid.Valid {
			edgeSet[eid.String] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reference CTE rows: %v", err)
	}
	return minDepth, edgeSet
}

func TestDiscoverNeighbors_BFS_MatchesReferenceCTE(t *testing.T) {
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
		name   string
		seeds  []string
		levels int
		types  []NodeType
	}{
		{"seedA_level1", []string{id("A")}, 1, nil},
		{"seedA_level2", []string{id("A")}, 2, nil},
		{"seedA_level3", []string{id("A")}, 3, nil},
		{"seedA_level3_podFilter", []string{id("A")}, 3, []NodeType{NodeTypePod}},
		{"multiSeed_AE_level2", []string{id("A"), id("E")}, 2, nil},
		{"isolatedSeed_F_level3", []string{id("F")}, 3, nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantDepth, wantEdges := referenceDiscoverCTE(t, svc, bfsEqTenant, c.seeds, c.levels, c.types)

			gotIDs, gotEdges, gotDepth, err := svc.discoverBFS(c.seeds, traverseOptions{
				Direction:        TraverseDirectionBoth,
				Levels:           c.levels,
				IncludeNodeTypes: c.types,
				TenantID:         bfsEqTenant,
			})
			if err != nil {
				t.Fatalf("discoverBFS: %v", err)
			}

			// discoveredIDs must be exactly the keys of the min-depth map.
			if len(gotIDs) != len(gotDepth) {
				t.Errorf("discoveredIDs (%d) != nodeMinDepth keys (%d)", len(gotIDs), len(gotDepth))
			}
			// Per-node minimum depth must match the CTE oracle.
			if !reflect.DeepEqual(gotDepth, wantDepth) {
				t.Errorf("nodeMinDepth mismatch:\n got  = %v\n want = %v", gotDepth, wantDepth)
			}
			// Walked-edge set must match the CTE oracle.
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
