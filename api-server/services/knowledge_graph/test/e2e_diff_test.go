package test

// End-to-end KG test module — shared graph-diff engine.
//
// This file provides the golden-diff machinery used by the Tier-A (JSON
// in-memory) E2E scenarios. It normalizes a built graph down to its SEMANTIC
// content — dropping volatile fields (id, created_at, updated_at,
// last_sync_version, is_active, contributing-source timestamps) — and diffs it
// against a committed golden file keyed by stable identity (node unique_key,
// edge src/dst unique_key + relationship_type).
//
// Node/edge IDs are deterministic SHA1s of the stable keys, so edges are joined
// back to their endpoints' unique_keys to make goldens independent of the
// generated UUIDs.
//
// Run `go test -run E2E -update` to (re)generate golden files; the first golden
// for a scenario MUST be human-reviewed against expectation before commit.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// updateGolden regenerates golden files instead of asserting against them.
var updateGolden = flag.Bool("update", false, "update E2E golden files instead of asserting")

// normNode is the semantic (volatile-free) projection of a core.DbNode.
type normNode struct {
	UniqueKey          string                 `json:"unique_key"`
	NodeType           string                 `json:"node_type"`
	SpecificType       string                 `json:"specific_type"`
	CloudAccountID     string                 `json:"cloud_account_id"`
	TenantID           string                 `json:"tenant_id"`
	Level              string                 `json:"level"`
	Source             string                 `json:"source"`
	Properties         map[string]interface{} `json:"properties"`
	Labels             map[string]string      `json:"labels"`
	QueryAttributes    map[string]interface{} `json:"query_attributes"`
	OntologyAttributes map[string]interface{} `json:"ontology_attributes"`
}

// normEdge is the semantic projection of a core.DbEdge, with endpoints
// expressed as node unique_keys rather than generated IDs.
type normEdge struct {
	SrcKey              string                 `json:"src_key"`
	DstKey              string                 `json:"dst_key"`
	RelationshipType    string                 `json:"relationship_type"`
	CloudAccountID      string                 `json:"cloud_account_id"`
	TenantID            string                 `json:"tenant_id"`
	Level               string                 `json:"level"`
	Source              string                 `json:"source"`
	Properties          map[string]interface{} `json:"properties"`
	ContributingSources []string               `json:"contributing_sources"` // source names only (timestamps dropped), sorted
}

// normGraph is the full normalized, deterministically-ordered graph.
type normGraph struct {
	Nodes []normNode `json:"nodes"`
	Edges []normEdge `json:"edges"`
}

// normalizeGraph projects built nodes/edges to their semantic form, resolves
// edge endpoints to unique_keys, sorts deterministically, and round-trips
// through JSON so numeric types match a golden loaded from disk.
func normalizeGraph(nodes []*core.DbNode, edges []*core.DbEdge) normGraph {
	idToKey := make(map[string]string, len(nodes))
	for _, n := range nodes {
		idToKey[n.ID] = n.UniqueKey
	}

	nn := make([]normNode, 0, len(nodes))
	for _, n := range nodes {
		nn = append(nn, normNode{
			UniqueKey:          n.UniqueKey,
			NodeType:           string(n.NodeType),
			SpecificType:       n.SpecificType,
			CloudAccountID:     n.CloudAccountID,
			TenantID:           n.TenantID,
			Level:              n.Level,
			Source:             n.Source,
			Properties:         n.Properties,
			Labels:             n.Labels,
			QueryAttributes:    n.QueryAttributes,
			OntologyAttributes: n.OntologyAttributes,
		})
	}

	ne := make([]normEdge, 0, len(edges))
	for _, e := range edges {
		cs := make([]string, 0, len(e.ContributingSources))
		for _, c := range e.ContributingSources {
			cs = append(cs, c.Source)
		}
		sort.Strings(cs)
		ne = append(ne, normEdge{
			SrcKey:              resolveEndpoint(idToKey, e.SourceNodeID),
			DstKey:              resolveEndpoint(idToKey, e.DestinationNodeID),
			RelationshipType:    string(e.RelationshipType),
			CloudAccountID:      e.CloudAccountID,
			TenantID:            e.TenantID,
			Level:               e.Level,
			Source:              e.Source,
			Properties:          e.Properties,
			ContributingSources: cs,
		})
	}

	sort.Slice(nn, func(i, j int) bool { return nodeKey(nn[i]) < nodeKey(nn[j]) })
	sort.Slice(ne, func(i, j int) bool { return edgeKey(ne[i]) < edgeKey(ne[j]) })

	g := normGraph{Nodes: nn, Edges: ne}
	// Round-trip through JSON so built values (e.g. int vs float64) match a
	// golden that was loaded from JSON — makes reflect.DeepEqual reliable.
	raw, _ := json.Marshal(g)
	var out normGraph
	_ = json.Unmarshal(raw, &out)
	return out
}

// resolveEndpoint maps a generated node ID back to its unique_key; a dangling
// endpoint (edge to a node not in the set) is surfaced explicitly so it can't
// hide silently.
func resolveEndpoint(idToKey map[string]string, id string) string {
	if k, ok := idToKey[id]; ok {
		return k
	}
	return "DANGLING_ID:" + id
}

func nodeKey(n normNode) string {
	return n.UniqueKey + "|" + n.CloudAccountID + "|" + n.TenantID
}

func edgeKey(e normEdge) string {
	return e.SrcKey + "->" + e.DstKey + "|" + e.RelationshipType + "|" + e.CloudAccountID + "|" + e.TenantID
}

// assertGoldenGraph normalizes (nodes, edges) and either regenerates the golden
// (with -update) or diffs against it, failing with a structured added/removed/
// changed report on mismatch.
func assertGoldenGraph(t *testing.T, goldenPath string, nodes []*core.DbNode, edges []*core.DbEdge) {
	t.Helper()
	got := normalizeGraph(nodes, edges)

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("failed to create golden dir: %v", err)
		}
		raw, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatalf("failed to marshal golden: %v", err)
		}
		if err := os.WriteFile(goldenPath, append(raw, '\n'), 0o644); err != nil {
			t.Fatalf("failed to write golden: %v", err)
		}
		t.Logf("updated golden %s (%d nodes, %d edges)", goldenPath, len(got.Nodes), len(got.Edges))
		return
	}

	rawWant, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("missing golden %s (run `go test -run E2E -update` to generate): %v", goldenPath, err)
	}
	var want normGraph
	if err := json.Unmarshal(rawWant, &want); err != nil {
		t.Fatalf("failed to parse golden %s: %v", goldenPath, err)
	}

	if diffs := diffGraphs(want, got); len(diffs) > 0 {
		t.Errorf("golden mismatch for %s (%d differences); run with -update to regenerate after review:\n%s",
			goldenPath, len(diffs), strings.Join(diffs, "\n"))
	}
}

// diffGraphs returns a human-readable list of semantic differences between two
// normalized graphs, keyed by stable identity.
func diffGraphs(want, got normGraph) []string {
	diffs := diffKeyed("NODE", indexBy(want.Nodes, nodeKey), indexBy(got.Nodes, nodeKey))
	diffs = append(diffs, diffKeyed("EDGE", indexBy(want.Edges, edgeKey), indexBy(got.Edges, edgeKey))...)
	sort.Strings(diffs)
	return diffs
}

// indexBy builds a stable-key → item map from a slice.
func indexBy[T any](items []T, keyOf func(T) string) map[string]T {
	m := make(map[string]T, len(items))
	for _, it := range items {
		m[keyOf(it)] = it
	}
	return m
}

// diffKeyed reports added/removed/changed items between two keyed maps.
func diffKeyed[T any](kind string, want, got map[string]T) []string {
	var diffs []string
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("- %s removed: %s", kind, k))
			continue
		}
		if !reflect.DeepEqual(w, g) {
			diffs = append(diffs, fmt.Sprintf("~ %s changed: %s\n    want: %s\n    got:  %s", kind, k, mustJSON(w), mustJSON(g)))
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			diffs = append(diffs, fmt.Sprintf("+ %s added: %s", kind, k))
		}
	}
	return diffs
}

func mustJSON(v interface{}) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
