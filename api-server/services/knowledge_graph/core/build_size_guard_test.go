package core

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

func TestBuildSizeError(t *testing.T) {
	contribs := []sourceContribution{{AccountName: "prod", Source: "aws", Nodes: 7, Edges: 2}}

	cases := []struct {
		name         string
		nodes, edges int
		wantErr      bool
	}{
		{"under both caps", 5, 5, false},
		{"exactly at both caps", 10, 20, false},
		{"one node over", 11, 0, true},
		{"one edge over", 0, 21, true},
		{"both over", 11, 21, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := buildSizeError("tenant-1", "phase 1 source aws", tc.nodes, tc.edges, 10, 20, contribs)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil {
				return
			}
			if !errors.Is(err, ErrBuildTooLarge) {
				t.Fatalf("error does not wrap ErrBuildTooLarge: %v", err)
			}
			// The message becomes the build request's recorded failure cause, so it
			// must say where to look without anyone reading the logs.
			for _, want := range []string{
				"tenant-1",
				"phase 1 source aws",
				fmt.Sprintf("%d nodes (max 10)", tc.nodes),
				fmt.Sprintf("%d edges (max 20)", tc.edges),
				"prod / aws: 7 nodes, 2 edges",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q is missing %q", err.Error(), want)
				}
			}
		})
	}
}

func TestTopContributors(t *testing.T) {
	in := []sourceContribution{
		{Source: "a", Nodes: 10, Edges: 1},
		{Source: "b", Nodes: 500, Edges: 9},
		{Source: "c", Nodes: 10, Edges: 50}, // ties a on nodes, more edges
		{Source: "d", Nodes: 3},
		{Source: "e", Nodes: 90},
		{Source: "f", Nodes: 1},
		{Source: "g", Nodes: 200},
	}
	original := append([]sourceContribution(nil), in...)

	got := topContributors(in, 5)

	want := []string{"b", "g", "e", "c", "a"}
	if len(got) != len(want) {
		t.Fatalf("got %d contributors, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Source != w {
			t.Errorf("position %d: got %q, want %q (full: %+v)", i, got[i].Source, w, got)
		}
	}
	for i := range in {
		if in[i] != original[i] {
			t.Fatalf("topContributors reordered its input")
		}
	}
	if n := len(topContributors(in[:2], 5)); n != 2 {
		t.Errorf("fewer contributions than the limit: got %d, want 2", n)
	}
}

func TestSourceContributionString(t *testing.T) {
	cases := []struct {
		c    sourceContribution
		want string
	}{
		{sourceContribution{AccountID: "id-1", AccountName: "prod", Source: "aws", Nodes: 3, Edges: 4}, "prod / aws: 3 nodes, 4 edges"},
		{sourceContribution{AccountID: "id-1", Source: "aws", Nodes: 3}, "id-1 / aws: 3 nodes, 0 edges"},
		{sourceContribution{Source: "enricher k8s_ec2", Edges: 2}, "enricher k8s_ec2: 0 nodes, 2 edges"},
	}
	for _, tc := range cases {
		if got := tc.c.String(); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

func TestBuildSizeLimits(t *testing.T) {
	prevNodes, prevEdges := config.Config.KGBuildMaxNodes, config.Config.KGBuildMaxEdges
	t.Cleanup(func() { config.Config.KGBuildMaxNodes, config.Config.KGBuildMaxEdges = prevNodes, prevEdges })

	config.Config.KGBuildMaxNodes, config.Config.KGBuildMaxEdges = 0, -1
	if n, e := buildSizeLimits(); n != defaultBuildMaxNodes || e != defaultBuildMaxEdges {
		t.Errorf("unset/non-positive: got (%d, %d), want defaults (%d, %d)", n, e, defaultBuildMaxNodes, defaultBuildMaxEdges)
	}

	config.Config.KGBuildMaxNodes, config.Config.KGBuildMaxEdges = 1234, 5678
	if n, e := buildSizeLimits(); n != 1234 || e != 5678 {
		t.Errorf("configured: got (%d, %d), want (1234, 5678)", n, e)
	}
}

// fakeFlowSource adds a fixed set of edges in Phase 2.5 — after the last Phase
// 2.1 check — so only the before-save check can see them.
type fakeFlowSource struct{ edges []*DbEdge }

func (f *fakeFlowSource) GetName() string { return "fake_flow" }
func (f *fakeFlowSource) BuildFlowRelationships(*security.RequestContext, *FlowSourceBuildRequest) ([]*DbEdge, []*DbNode, error) {
	return f.edges, nil, nil
}
func (f *fakeFlowSource) IsEnabled() bool                       { return true }
func (f *fakeFlowSource) Validate() error                       { return nil }
func (f *fakeFlowSource) GetSourceCategory() FlowSourceCategory { return FlowSourceCategoryNetworking }

// buildGraphsOnMockDB runs a real BuildGraphs for one AWS account whose source
// returns nodeCount nodes and no edges; flowEdges > 0 also registers a flow
// source adding that many distinct edges between those nodes. sqlmock answers
// only the two queries BuildGraphs makes before Phase 1, so any later query — a
// save above all — fails loudly.
func buildGraphsOnMockDB(t *testing.T, nodeCount, flowEdges int, saveToDB bool) (*BuildResponse, sqlmock.Sqlmock, error) {
	t.Helper()
	const tenantID = "3f0b6a8e-1d2c-4b5a-9e8f-7a6b5c4d3e2f"

	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	dbm := &database.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgresql")}

	// getActiveAccountsForTenant — one active AWS account, 33 columns.
	mock.ExpectQuery(`FROM cloud_accounts`).WillReturnRows(sqlmock.NewRows([]string{
		"id", "cloud_provider", "account_number", "account_name", "created_at", "created_by",
		"updated_at", "updated_by", "billing_source", "start_date", "tenant", "assume_role",
		"region", "status", "account_url", "budget", "synced_at", "sync_status", "account_access",
		"account_purpose", "data", "access_key", "access_secret", "account_type",
		"agent_access_key", "agent_access_secret", "agent_synced_at", "sync_status_message",
		"external_id", "etl_attempt", "parent_account_id", "access_secret_v2", "account_env",
	}).AddRow(
		"acct-1", "AWS", nil, "prod-account", nil, "",
		nil, "", nil, nil, tenantID, nil,
		nil, "active", nil, 0.0, nil, nil, nil,
		nil, nil, nil, nil, "",
		nil, nil, nil, nil,
		nil, 0, nil, nil, "",
	))
	// GetFilterForTenant — the default filter, for the sync version.
	mock.ExpectQuery(`FROM knowledge_graph_tenant_filters`).WillReturnRows(sqlmock.NewRows([]string{
		"id", "tenant_id", "filter_name", "account_ids", "sources", "flow_sources", "filters",
		"is_default", "enabled", "created_at", "last_sync_version", "last_sync_timestamp",
	}).AddRow(
		"9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d", tenantID, "default", "[]", "[]", "[]", "{}",
		true, true, time.Now(), int64(41), nil,
	))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reqCtx := security.NewRequestContextForTenantAdmin(tenantID, logger, nil, nil)
	service := NewService(reqCtx, logger, dbm)

	nodes := make([]*DbNode, nodeCount)
	for i := range nodes {
		nodes[i] = &DbNode{ID: fmt.Sprintf("node-%d", i), NodeType: NodeTypeStorage, UniqueKey: fmt.Sprintf("aws:storage:%d", i)}
	}
	if err := service.RegisterSource(&MockSource{name: "aws", enabled: true, graph: &Graph{Nodes: nodes}}); err != nil {
		t.Fatalf("register source: %v", err)
	}

	req := &BuildRequest{TenantID: tenantID, SaveToDB: saveToDB}
	if flowEdges > 0 {
		edges := make([]*DbEdge, 0, flowEdges)
		for i := 0; i < nodeCount && len(edges) < flowEdges; i++ {
			for j := 0; j < nodeCount && len(edges) < flowEdges; j++ {
				if i != j {
					edges = append(edges, &DbEdge{
						ID: fmt.Sprintf("edge-%d-%d", i, j), SourceNodeID: nodes[i].ID, DestinationNodeID: nodes[j].ID,
						RelationshipType: RelationshipCalls, TenantID: tenantID,
					})
				}
			}
		}
		if len(edges) != flowEdges {
			t.Fatalf("only %d distinct edges fit between %d nodes, want %d", len(edges), nodeCount, flowEdges)
		}
		if err := service.RegisterFlowSource(&fakeFlowSource{edges: edges}); err != nil {
			t.Fatalf("register flow source: %v", err)
		}
		req.FlowSources = []string{"fake_flow"}
	}

	resp, err := service.BuildGraphs(reqCtx, req)
	return resp, mock, err
}

// The fuse fires in Phase 1, before anything is saved or swept. SaveToDB is true,
// so a save would have been attempted had the build continued — and sqlmock would
// have rejected it, surfacing a different error.
func TestBuildGraphs_AbortsWhenGraphExceedsSizeLimit(t *testing.T) {
	prevNodes, prevEdges := config.Config.KGBuildMaxNodes, config.Config.KGBuildMaxEdges
	t.Cleanup(func() { config.Config.KGBuildMaxNodes, config.Config.KGBuildMaxEdges = prevNodes, prevEdges })
	config.Config.KGBuildMaxNodes, config.Config.KGBuildMaxEdges = 3, 100

	resp, mock, err := buildGraphsOnMockDB(t, 5, 0, true)

	if !errors.Is(err, ErrBuildTooLarge) {
		t.Fatalf("BuildGraphs error = %v, want ErrBuildTooLarge", err)
	}
	if resp != nil {
		t.Errorf("an aborted build returned a response: %+v", resp)
	}
	for _, want := range []string{"phase 1 source aws", "5 nodes (max 3)", "prod-account / aws: 5 nodes, 0 edges"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q is missing %q", err.Error(), want)
		}
	}
	// Only the two pre-Phase-1 reads happened: no upsert, no sweep, no sync-version write.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// Under the cap the fuse stays out of the way; what the build does next against
// the mock is not this test's concern.
func TestBuildGraphs_UnderSizeLimitIsNotAborted(t *testing.T) {
	prevNodes, prevEdges := config.Config.KGBuildMaxNodes, config.Config.KGBuildMaxEdges
	t.Cleanup(func() { config.Config.KGBuildMaxNodes, config.Config.KGBuildMaxEdges = prevNodes, prevEdges })
	config.Config.KGBuildMaxNodes, config.Config.KGBuildMaxEdges = 100, 100

	_, _, err := buildGraphsOnMockDB(t, 5, 0, false)
	if errors.Is(err, ErrBuildTooLarge) {
		t.Fatalf("a build under the cap was aborted: %v", err)
	}
}

// What flow sources and the cross-account rules add arrives after the last
// Phase 2.1 check. The before-save check is the only one that sees it: here the
// source adds no edges, so every earlier check passes, and the flow source alone
// pushes the edge count over the cap.
func TestBuildGraphs_AbortsBeforeSaveOnGrowthAfterEnrichment(t *testing.T) {
	prevNodes, prevEdges := config.Config.KGBuildMaxNodes, config.Config.KGBuildMaxEdges
	t.Cleanup(func() { config.Config.KGBuildMaxNodes, config.Config.KGBuildMaxEdges = prevNodes, prevEdges })
	config.Config.KGBuildMaxNodes, config.Config.KGBuildMaxEdges = 100, 6

	resp, _, err := buildGraphsOnMockDB(t, 5, 10, true)

	if !errors.Is(err, ErrBuildTooLarge) {
		t.Fatalf("BuildGraphs error = %v, want ErrBuildTooLarge", err)
	}
	if resp != nil {
		t.Errorf("an aborted build returned a response: %+v", resp)
	}
	for _, want := range []string{"before save", "10 edges (max 6)", "flow sources and cross-account rules: 0 nodes, 10 edges"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q is missing %q", err.Error(), want)
		}
	}
}
