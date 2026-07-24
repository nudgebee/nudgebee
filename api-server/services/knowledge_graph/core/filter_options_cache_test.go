package core

// DB-gated test for the per-tenant filter-options cache: the unfiltered path is
// served from knowledge_graph_filter_options; filtered calls bypass it. Skips
// cleanly when no Postgres is reachable.

import (
	"io"
	"log/slog"
	"slices"
	"testing"

	"nudgebee/services/internal/testenv"
)

const filterCacheTenant = "b2ca6e00-0000-4000-8000-000000000001"

func TestFilterOptionsCache_UnfilteredServedFromCacheFilteredBypasses(t *testing.T) {
	dbm := testenv.RequireMetastore(t)
	svc := NewService(newTestRequestContext(), slog.New(slog.NewTextHandler(io.Discard, nil)), dbm)

	// Provision the cache table (no-op if the migration already applied it).
	if _, err := dbm.Exec(`CREATE TABLE IF NOT EXISTS public.knowledge_graph_filter_options (
		tenant_id UUID PRIMARY KEY, payload JSONB NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		t.Fatalf("create cache table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = dbm.Exec(`DELETE FROM knowledge_graph_filter_options WHERE tenant_id = $1::uuid`, filterCacheTenant)
	})

	// Seed a cache row whose node_types carry a sentinel that live computation on
	// this empty tenant could never produce.
	const sentinel = "SENTINEL-CACHE-TYPE"
	svc.writeFilterOptionsCache(filterCacheTenant, &FilterOptions{
		NodeTypes: []string{sentinel},
		NodeIDMap: map[string]string{"k8s:acct::Pod::x": "id-1"},
		NodeCount: 1,
	})

	// Unfiltered → must be served from the cache (contains the sentinel).
	unfiltered, err := svc.GetFilterOptions(filterCacheTenant, nil, nil)
	if err != nil {
		t.Fatalf("GetFilterOptions unfiltered: %v", err)
	}
	if !slices.Contains(unfiltered.NodeTypes, sentinel) {
		t.Errorf("unfiltered call did not read the cache: NodeTypes=%v", unfiltered.NodeTypes)
	}

	// Filtered → must bypass the cache and compute live (no sentinel; empty tenant).
	filtered, err := svc.GetFilterOptions(filterCacheTenant, &GraphFilters{NodeTypes: []NodeType{NodeTypePod}}, nil)
	if err != nil {
		t.Fatalf("GetFilterOptions filtered: %v", err)
	}
	if slices.Contains(filtered.NodeTypes, sentinel) {
		t.Errorf("filtered call incorrectly served from cache: NodeTypes=%v", filtered.NodeTypes)
	}

	// readFilterOptionsCache round-trips the payload.
	got, ok := svc.readFilterOptionsCache(filterCacheTenant)
	if !ok {
		t.Fatal("readFilterOptionsCache: expected a hit")
	}
	if got.NodeCount != 1 || len(got.NodeIDMap) != 1 || !slices.Contains(got.NodeTypes, sentinel) {
		t.Errorf("cache round-trip mismatch: %+v", got)
	}
}
