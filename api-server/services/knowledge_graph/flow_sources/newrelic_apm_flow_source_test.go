package flow_sources

import (
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"testing"
)

func TestNewNewRelicAPMFlowSource(t *testing.T) {
	src := NewNewRelicAPMFlowSource(slog.Default())
	if src.GetName() != "newrelic-apm" {
		t.Errorf("expected name newrelic-apm, got %s", src.GetName())
	}
	if src.GetSourceCategory() != core.FlowSourceCategoryTracing {
		t.Errorf("expected category tracing, got %s", src.GetSourceCategory())
	}
	if !src.IsEnabled() {
		t.Errorf("expected enabled=true")
	}
}

func TestNewRelicAPMFlowSource_Validate(t *testing.T) {
	src := NewNewRelicAPMFlowSource(slog.Default())
	if err := src.Validate(); err != nil {
		t.Errorf("Validate returned error: %v", err)
	}
}

func TestParseSpanFacetRow(t *testing.T) {
	tests := []struct {
		name       string
		row        map[string]any
		wantCaller string
		wantTarget string
		wantKind   string
		wantDB     string
		wantCount  int64
	}{
		{
			name: "peer.service wins over everything",
			row: map[string]any{
				"facet": []any{"cart", "10.0.0.1", "postgresql", "cart_db", "checkout-api"},
				"count": float64(42),
			},
			wantCaller: "cart", wantTarget: "checkout-api", wantKind: "peer_service", wantDB: "postgresql", wantCount: 42,
		},
		{
			name: "db.system + db.name → database when peer.service empty",
			row: map[string]any{
				"facet": []any{"cart", nil, "postgresql", "cart_db", nil},
				"count": float64(100),
			},
			wantCaller: "cart", wantTarget: "cart_db", wantKind: "database", wantDB: "postgresql", wantCount: 100,
		},
		{
			name: "db.system without db.name → row rejected",
			row: map[string]any{
				"facet": []any{"cart", nil, "postgresql", nil, nil},
				"count": float64(7),
			},
			wantCaller: "cart", wantTarget: "", wantKind: "", wantDB: "", wantCount: 0,
		},
		{
			name: "server.address as IP → cluster_ip",
			row: map[string]any{
				"facet": []any{"loki-gateway", "34.118.225.23", nil, nil, nil},
				"count": float64(35000),
			},
			wantCaller: "loki-gateway", wantTarget: "34.118.225.23", wantKind: "cluster_ip", wantDB: "", wantCount: 35000,
		},
		{
			name: "server.address as hostname → hostname",
			row: map[string]any{
				"facet": []any{"frontend", "checkout.svc.cluster.local", nil, nil, nil},
				"count": float64(1),
			},
			wantCaller: "frontend", wantTarget: "checkout.svc.cluster.local", wantKind: "hostname", wantCount: 1,
		},
		{
			name: "all targets null → skipped",
			row: map[string]any{
				"facet": []any{"cart", nil, nil, nil, nil},
				"count": float64(0),
			},
			wantCaller: "cart", wantTarget: "", wantKind: "", wantCount: 0,
		},
		{
			name: "malformed facet (too short) → skipped",
			row: map[string]any{
				"facet": []any{"cart", "10.0.0.1"},
				"count": float64(5),
			},
			wantCaller: "", wantTarget: "", wantCount: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caller, target, kind, db, count := parseSpanFacetRow(tt.row)
			if caller != tt.wantCaller {
				t.Errorf("caller: want %q, got %q", tt.wantCaller, caller)
			}
			if target != tt.wantTarget {
				t.Errorf("target: want %q, got %q", tt.wantTarget, target)
			}
			if kind != tt.wantKind {
				t.Errorf("kind: want %q, got %q", tt.wantKind, kind)
			}
			if db != tt.wantDB {
				t.Errorf("dbSystem: want %q, got %q", tt.wantDB, db)
			}
			if count != tt.wantCount {
				t.Errorf("count: want %d, got %d", tt.wantCount, count)
			}
		})
	}
}

func TestInferNewRelicProtocol(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", "http"},
		{"postgresql", "postgres"},
		{"Postgres", "postgres"},
		{"mysql", "mysql"},
		{"redis", "redis"},
		{"mongodb", "mongodb"},
		{"Mongo", "mongodb"},
		{"cassandra", "cassandra"},
		{"elasticsearch", "elasticsearch"},
		{"clickhouse", "clickhouse"}, // unknown DB falls through to lowercased value
	}
	for _, tt := range tests {
		if got := inferNewRelicProtocol(tt.in); got != tt.want {
			t.Errorf("inferNewRelicProtocol(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNewRelicAPMFlowSource_BuildEdgeProperties(t *testing.T) {
	src := NewNewRelicAPMFlowSource(slog.Default())

	t.Run("database edge includes nr_db_system", func(t *testing.T) {
		props := src.buildNRQLEdgeProperties("cart", "cart_db", "database", "postgresql", "8031798", 100, nrStrategyNRQL)
		assertProp(t, props, "nr_source_service", "cart")
		assertProp(t, props, "nr_target_identifier", "cart_db")
		assertProp(t, props, "nr_target_kind", "database")
		assertProp(t, props, "nr_db_system", "postgresql")
		assertProp(t, props, "nr_protocol", "postgres")
		assertProp(t, props, "nr_account_id", "8031798")
		assertProp(t, props, "nr_strategy", nrStrategyNRQL)
		if v, ok := props["nr_request_count"].(int64); !ok || v != 100 {
			t.Errorf("expected nr_request_count=100, got %v", props["nr_request_count"])
		}
	})

	t.Run("non-database edge omits nr_db_system", func(t *testing.T) {
		props := src.buildNRQLEdgeProperties("cart", "checkout-api", "peer_service", "", "8031798", 7, nrStrategyNRQL)
		if _, ok := props["nr_db_system"]; ok {
			t.Errorf("nr_db_system should not be set on non-database edges")
		}
		assertProp(t, props, "nr_protocol", "http")
	})

	t.Run("fallback strategy stamps nerdgraph_fallback_nrql", func(t *testing.T) {
		props := src.buildNRQLEdgeProperties("cart", "checkout-api", "peer_service", "", "8031798", 7, nrStrategyNerdGraphFallbackNRQL)
		assertProp(t, props, "nr_strategy", nrStrategyNerdGraphFallbackNRQL)
	})
}

func TestNewRelicAPMFlowSource_FindDatabaseNode_RequiresBothEngineAndName(t *testing.T) {
	src := NewNewRelicAPMFlowSource(slog.Default())
	src.InitializeNodeMatcher([]*core.DbNode{
		{
			ID: "db-1", NodeType: core.NodeTypeDatabase,
			CloudAccountID: "acct-1",
			Properties:     map[string]interface{}{"name": "cart_db", "engine": "postgresql"},
		},
		{
			ID: "db-2", NodeType: core.NodeTypeDatabase,
			CloudAccountID: "acct-1",
			Properties:     map[string]interface{}{"name": "orders_db", "engine": "postgresql"},
		},
	})

	if got := src.findDatabaseNode("cart_db", "postgresql", "acct-1"); got == nil || got.ID != "db-1" {
		t.Errorf("expected db-1, got %v", got)
	}
	if got := src.findDatabaseNode("orders_db", "postgresql", "acct-1"); got == nil || got.ID != "db-2" {
		t.Errorf("expected db-2, got %v", got)
	}
	if got := src.findDatabaseNode("", "postgresql", "acct-1"); got != nil {
		t.Errorf("empty name should reject; got %v", got)
	}
	if got := src.findDatabaseNode("cart_db", "", "acct-1"); got != nil {
		t.Errorf("empty engine should reject; got %v", got)
	}
	if got := src.findDatabaseNode("nonexistent", "postgresql", "acct-1"); got != nil {
		t.Errorf("missing name should not match; got %v", got)
	}
}

func TestNewRelicAPMFlowSource_FindDatabaseNode_CrossAccountFallback(t *testing.T) {
	src := NewNewRelicAPMFlowSource(slog.Default())
	// DB lives in acct-2, caller is in acct-1 — same-account match misses,
	// cross-account fallback should hit.
	src.InitializeNodeMatcher([]*core.DbNode{
		{
			ID: "db-1", NodeType: core.NodeTypeDatabase,
			CloudAccountID: "acct-2",
			Properties:     map[string]interface{}{"name": "shared_db", "engine": "postgresql"},
		},
	})
	got := src.findDatabaseNode("shared_db", "postgresql", "acct-1")
	if got == nil || got.ID != "db-1" {
		t.Errorf("expected cross-account fallback to find db-1, got %v", got)
	}
}

func TestNewRelicAPMFlowSource_ResolveTarget_ClusterIP_MultiCluster(t *testing.T) {
	src := NewNewRelicAPMFlowSource(slog.Default())
	nodes := []*core.DbNode{
		{
			ID: "svc-a", NodeType: core.NodeTypeK8sService,
			Properties: map[string]interface{}{"name": "loki", "cluster": "cluster-a", "cluster_ip": "10.0.0.1"},
		},
		{
			ID: "svc-b", NodeType: core.NodeTypeK8sService,
			Properties: map[string]interface{}{"name": "loki", "cluster": "cluster-b", "cluster_ip": "10.0.0.1"},
		},
	}
	src.InitializeNodeMatcher(nodes)
	resolver := NewK8sServiceIPResolver(nodes)

	gotA := src.resolveTarget("cluster_ip", "10.0.0.1", "", "cluster-a", "acct", resolver)
	if gotA == nil || gotA.ID != "svc-a" {
		t.Errorf("caller in cluster-a should match svc-a, got %v", gotA)
	}
	gotB := src.resolveTarget("cluster_ip", "10.0.0.1", "", "cluster-b", "acct", resolver)
	if gotB == nil || gotB.ID != "svc-b" {
		t.Errorf("caller in cluster-b should match svc-b, got %v", gotB)
	}
	// Caller without cluster context + ambiguous IP → no match.
	if got := src.resolveTarget("cluster_ip", "10.0.0.1", "", "", "acct", resolver); got != nil {
		t.Errorf("ambiguous IP without caller cluster should return nil, got %v", got)
	}
}

func TestNewRelicAPMFlowSource_ResolveTarget_PeerServiceAndHostname(t *testing.T) {
	src := NewNewRelicAPMFlowSource(slog.Default())
	src.InitializeNodeMatcher([]*core.DbNode{
		{
			ID: "svc-checkout", NodeType: core.NodeTypeService,
			CloudAccountID: "acct-1",
			Properties:     map[string]interface{}{"name": "checkout-api"},
		},
	})
	resolver := NewK8sServiceIPResolver(nil)

	if got := src.resolveTarget("peer_service", "checkout-api", "", "", "acct-1", resolver); got == nil || got.ID != "svc-checkout" {
		t.Errorf("peer_service should match by name, got %v", got)
	}
	if got := src.resolveTarget("hostname", "checkout-api", "", "", "acct-1", resolver); got == nil || got.ID != "svc-checkout" {
		t.Errorf("hostname should match by name, got %v", got)
	}
	if got := src.resolveTarget("peer_service", "ghost-svc", "", "", "acct-1", resolver); got != nil {
		t.Errorf("missing peer_service should return nil, got %v", got)
	}
	if got := src.resolveTarget("unknown_kind", "anything", "", "", "acct-1", resolver); got != nil {
		t.Errorf("unknown target kind should return nil, got %v", got)
	}
}

func TestResolveCloudAccountID(t *testing.T) {
	t.Run("uses request value when set", func(t *testing.T) {
		req := &core.FlowSourceBuildRequest{CloudAccountID: "acct-x"}
		if got := resolveCloudAccountID(req); got != "acct-x" {
			t.Errorf("want acct-x, got %s", got)
		}
	})
	t.Run("falls back to first existing node", func(t *testing.T) {
		req := &core.FlowSourceBuildRequest{
			ExistingNodes: []*core.DbNode{
				{CloudAccountID: ""},
				{CloudAccountID: "acct-y"},
				{CloudAccountID: "acct-z"},
			},
		}
		if got := resolveCloudAccountID(req); got != "acct-y" {
			t.Errorf("want acct-y, got %s", got)
		}
	})
	t.Run("empty when no source", func(t *testing.T) {
		req := &core.FlowSourceBuildRequest{}
		if got := resolveCloudAccountID(req); got != "" {
			t.Errorf("want empty, got %s", got)
		}
	})
}

// ---- helpers ----

func assertProp(t *testing.T, props map[string]interface{}, key, want string) {
	t.Helper()
	got, ok := props[key].(string)
	if !ok || got != want {
		t.Errorf("prop %s: want %q, got %v", key, want, props[key])
	}
}
