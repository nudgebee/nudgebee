package core

// DB-gated test for filter-options cache schema versioning: a legacy bare payload
// (or a version mismatch) must read as a miss so stale-shape rows are never served.

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"nudgebee/services/internal/testenv"
)

const filterCacheVersionTenant = "b2ca6e00-0000-4000-8000-000000000002"

func TestFilterOptionsCache_SchemaVersionGuardsStalePayloads(t *testing.T) {
	dbm := testenv.RequireMetastore(t)
	svc := NewService(newTestRequestContext(), slog.New(slog.NewTextHandler(io.Discard, nil)), dbm)

	if _, err := dbm.Exec(`CREATE TABLE IF NOT EXISTS public.knowledge_graph_filter_options (
		tenant_id UUID PRIMARY KEY, payload JSONB NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		t.Fatalf("create cache table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = dbm.Exec(`DELETE FROM knowledge_graph_filter_options WHERE tenant_id = $1::uuid`, filterCacheVersionTenant)
	})

	upsert := func(raw []byte) {
		if _, err := dbm.Exec(`INSERT INTO knowledge_graph_filter_options (tenant_id, payload, updated_at)
			VALUES ($1, $2, now()) ON CONFLICT (tenant_id) DO UPDATE SET payload = EXCLUDED.payload`,
			filterCacheVersionTenant, raw); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	// 1. A legacy bare FilterOptions payload (pre-envelope) must read as a MISS.
	bare, _ := json.Marshal(&FilterOptions{NodeTypes: []string{"LEGACY"}, NodeCount: 1})
	upsert(bare)
	if _, ok := svc.readFilterOptionsCache(filterCacheVersionTenant); ok {
		t.Error("legacy bare payload should be a cache miss, got a hit")
	}

	// 2. A payload written by writeFilterOptionsCache (current version) must HIT.
	svc.writeFilterOptionsCache(filterCacheVersionTenant, &FilterOptions{NodeTypes: []string{"CURRENT"}, NodeCount: 2})
	got, ok := svc.readFilterOptionsCache(filterCacheVersionTenant)
	if !ok {
		t.Fatal("current-version payload should be a cache hit")
	}
	if got.NodeCount != 2 || len(got.NodeTypes) != 1 || got.NodeTypes[0] != "CURRENT" {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// 3. A future/mismatched schema version must read as a MISS.
	wrong, _ := json.Marshal(filterOptionsCacheEnvelope{
		SchemaVersion: filterOptionsCacheSchemaVersion + 1,
		Options:       &FilterOptions{NodeTypes: []string{"FUTURE"}, NodeCount: 3},
	})
	upsert(wrong)
	if _, ok := svc.readFilterOptionsCache(filterCacheVersionTenant); ok {
		t.Error("mismatched schema version should be a cache miss, got a hit")
	}
}
