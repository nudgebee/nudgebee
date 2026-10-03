package test

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"testing"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/flow_sources"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

// TestLiveKGBuild_NoCrossNamespaceCalls runs a REAL knowledge-graph build against
// whatever environment .env points at and reports trace-derived CALLS edges that
// cross a namespace boundary.
//
// SaveToDB is false: this exercises the whole pipeline but writes nothing, so it
// is safe to run against a shared environment.
//
// Opt-in only — it needs live backends and is never part of CI:
//
//	KG_LIVE_TENANT=<tenant-uuid> go test ./knowledge_graph/test/ \
//	    -run TestLiveKGBuild_NoCrossNamespaceCalls -v -timeout 30m
func TestLiveKGBuild_NoCrossNamespaceCalls(t *testing.T) {
	tenantID := os.Getenv("KG_LIVE_TENANT")
	if tenantID == "" {
		t.Skip("set KG_LIVE_TENANT to run the live build check")
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatalf("database manager: %v", err)
	}

	reqCtx := security.NewRequestContextForTenantAdmin(tenantID, logger, nil, nil)
	kgService := core.NewService(reqCtx, logger, dbms)

	// Same registration the queue consumer performs.
	if err := sources.RegisterAllSourcesToService(kgService, "", "", reqCtx); err != nil {
		t.Logf("register sources: %v", err)
	}
	if err := flow_sources.RegisterAllFlowSourcesToService(kgService, reqCtx); err != nil {
		t.Logf("register flow sources: %v", err)
	}
	if err := sources.RegisterAllEnrichersToService(kgService, reqCtx); err != nil {
		t.Logf("register enrichers: %v", err)
	}

	// The traces flow source queries a hardcoded 2h window ending at
	// TimeRange.EndTime (it ignores StartTime), so aiming the check at a past
	// period means overriding the END time, not widening the range.
	now := time.Now().UTC()
	if v := os.Getenv("KG_LIVE_END_TIME"); v != "" {
		parsed, perr := time.Parse(time.RFC3339, v)
		if perr != nil {
			t.Fatalf("KG_LIVE_END_TIME %q is not RFC3339: %v", v, perr)
		}
		now = parsed.UTC()
		t.Logf("querying traces in the 2h window ending %s", now.Format(time.RFC3339))
	}
	resp, err := kgService.BuildGraphs(reqCtx, &core.BuildRequest{
		TenantID:  tenantID,
		Filters:   map[string]string{},
		SaveToDB:  false, // read-only: never writes to the shared graph
		TimeRange: &core.TimeRange{StartTime: now.Add(-24 * time.Hour), EndTime: now},
	})
	if err != nil {
		t.Fatalf("BuildGraphs: %v", err)
	}
	if !resp.Success {
		t.Fatalf("build reported failure: %s", resp.Error)
	}

	g := resp.KnowledgeGraph
	byID := make(map[string]*core.KgNode, len(g.Nodes))
	for i := range g.Nodes {
		byID[g.Nodes[i].ID] = &g.Nodes[i]
	}
	str := func(n *core.KgNode, k string) string {
		if n == nil {
			return ""
		}
		v, _ := n.Properties[k].(string)
		return v
	}

	// KgEdge.Source is not populated on the in-memory build response — the
	// producing flow source is carried on the edge properties.
	edgeSourceOf := func(e core.KgEdge) string {
		if v, ok := e.Properties["created_by_flow_source"].(string); ok && v != "" {
			return v
		}
		if v, ok := e.Properties["source_name"].(string); ok && v != "" {
			return v
		}
		if e.Source != "" {
			return e.Source
		}
		return "(unattributed)"
	}

	var crossing, inNamespace []string
	for _, e := range g.Edges {
		if e.RelationshipType != core.RelationshipCalls || edgeSourceOf(e) != "traces" {
			continue
		}
		src, dst := byID[e.SourceNodeID], byID[e.DestinationNodeID]
		sNS, dNS := str(src, "namespace"), str(dst, "namespace")
		if sNS == "" || dNS == "" {
			continue // external / non-namespaced target
		}
		line := fmt.Sprintf("%s/%s -> %s/%s", sNS, str(src, "name"), dNS, str(dst, "name"))
		if sNS != dNS {
			crossing = append(crossing, line)
		} else {
			inNamespace = append(inNamespace, line)
		}
	}
	sort.Strings(crossing)
	sort.Strings(inNamespace)

	fmt.Printf("\n=== LIVE BUILD (save_to_db=false) tenant=%s ===\n", tenantID)
	fmt.Printf("nodes=%d edges=%d accounts=%d\n", len(g.Nodes), len(g.Edges), resp.AccountsProcessed)

	// Which sources actually contributed? A silent flow source makes the
	// namespace assertion below vacuous, so surface it rather than pass on zero.
	edgeBySource := map[string]int{}
	callsBySource := map[string]int{}
	for _, e := range g.Edges {
		src := edgeSourceOf(e)
		edgeBySource[src]++
		if e.RelationshipType == core.RelationshipCalls {
			callsBySource[src]++
		}
	}
	nodeBySource := map[string]int{}
	for _, n := range g.Nodes {
		nodeBySource[n.Source]++
	}
	seen := map[string]bool{}
	srcs := []string{}
	for s := range edgeBySource {
		if !seen[s] {
			seen[s] = true
			srcs = append(srcs, s)
		}
	}
	for s := range nodeBySource {
		if !seen[s] {
			seen[s] = true
			srcs = append(srcs, s)
		}
	}
	sort.Strings(srcs)
	fmt.Println("edges by source (CALLS in parens):")
	for _, s := range srcs {
		fmt.Printf("   %-16s %6d  (CALLS %d)  nodes=%d\n", s, edgeBySource[s], callsBySource[s], nodeBySource[s])
	}
	fmt.Printf("trace CALLS edges between namespaced nodes: %d in-namespace, %d crossing\n",
		len(inNamespace), len(crossing))
	for _, l := range inNamespace {
		fmt.Println("   ok    ", l)
	}
	for _, l := range crossing {
		fmt.Println("   CROSS ", l)
	}
	fmt.Println()

	if callsBySource["traces"] == 0 {
		t.Fatalf("traces flow source produced no CALLS edges — the namespace assertion would be vacuous; " +
			"check that the trace backend is reachable and the flow source is enabled for this tenant")
	}
	if len(crossing) > 0 {
		t.Errorf("%d trace-derived CALLS edges cross a namespace boundary", len(crossing))
	}
}
