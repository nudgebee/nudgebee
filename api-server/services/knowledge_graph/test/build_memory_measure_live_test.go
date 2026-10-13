package test

import (
	"encoding/json"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/flow_sources"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

// TestLiveKGBuild_MeasureMemory measures how much heap one tenant's build takes,
// per node, so the build-size fuse (kg_build_max_nodes / kg_build_max_edges) is
// calibrated against the build path rather than a read-path benchmark
// (KG_SCALING_BASELINE.md measures GetCompleteGraphFromDatabase, which is not
// what OOMKilled services-server in #40937).
//
// What it reports:
//   - peak heap during BuildGraphs, sampled every 25ms (garbage included — the
//     container is killed on RSS, and with GOGC=100 the heap grows to roughly
//     twice the live set between collections, so the sampled peak is the
//     relevant number, not the post-GC live size);
//   - the save path's cost, added analytically: SaveNodes / SaveEdges marshal
//     every row's JSON columns into one values slice before inserting, so their
//     peak is at least the JSON size of the graph. The build runs with
//     SaveToDB=false so nothing is persisted to the shared graph.
//
// Not read-only: BuildGraphs refreshes the tenant's filter-options cache
// (knowledge_graph_filter_options) after every build, SaveToDB or not. That row
// is recomputed from the unchanged persisted graph, so it is the same write the
// hourly build makes.
//
// Skipped unless KG_MEASURE_TENANT is set:
//
//	KG_MEASURE_TENANT=<tenant-uuid> \
//	bash run-with-env.sh .env go test ./knowledge_graph/test/ -run TestLiveKGBuild_MeasureMemory -v -timeout 30m
func TestLiveKGBuild_MeasureMemory(t *testing.T) {
	tenantID := os.Getenv("KG_MEASURE_TENANT")
	if tenantID == "" {
		t.Skip("set KG_MEASURE_TENANT to run the build memory measurement")
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

	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	baseline := ms.HeapAlloc

	var peak atomic.Uint64
	peak.Store(baseline)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(25 * time.Millisecond)
		defer tick.Stop()
		var s runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				runtime.ReadMemStats(&s)
				for {
					cur := peak.Load()
					if s.HeapAlloc <= cur || peak.CompareAndSwap(cur, s.HeapAlloc) {
						break
					}
				}
			}
		}
	}()

	now := time.Now().UTC()
	started := time.Now()
	resp, err := kgService.BuildGraphs(reqCtx, &core.BuildRequest{
		TenantID:  tenantID,
		Filters:   map[string]string{},
		SaveToDB:  false,
		TimeRange: &core.TimeRange{StartTime: now.Add(-24 * time.Hour), EndTime: now},
	})
	close(stop)
	wg.Wait()
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("BuildGraphs: %v", err)
	}

	g := resp.KnowledgeGraph
	nodes, edges := len(g.Nodes), len(g.Edges)
	if nodes == 0 {
		t.Fatalf("tenant %s built 0 nodes — nothing to measure", tenantID)
	}

	// The save path marshals each node's and edge's JSON columns before inserting.
	var nodeJSON, edgeJSON int
	for i := range g.Nodes {
		if b, err := json.Marshal(g.Nodes[i].Properties); err == nil {
			nodeJSON += len(b)
		}
		if b, err := json.Marshal(g.Nodes[i].Labels); err == nil {
			nodeJSON += len(b)
		}
	}
	for i := range g.Edges {
		if b, err := json.Marshal(g.Edges[i].Properties); err == nil {
			edgeJSON += len(b)
		}
	}

	buildPeak := peak.Load() - baseline
	withSave := buildPeak + uint64(nodeJSON+edgeJSON)
	mb := func(b uint64) float64 { return float64(b) / (1 << 20) }

	t.Logf("tenant %s: %d nodes, %d edges (%.2f edges/node), build took %s",
		tenantID, nodes, edges, float64(edges)/float64(nodes), elapsed.Round(time.Second))
	t.Logf("peak heap during build:           %8.1f MB  (%6.0f bytes/node all-in)", mb(buildPeak), float64(buildPeak)/float64(nodes))
	t.Logf("save-path JSON (nodes + edges):   %8.1f MB  (%.1f + %.1f)", mb(uint64(nodeJSON+edgeJSON)), mb(uint64(nodeJSON)), mb(uint64(edgeJSON)))
	t.Logf("estimated peak including save:    %8.1f MB  (%6.0f bytes/node all-in)", mb(withSave), float64(withSave)/float64(nodes))
}
