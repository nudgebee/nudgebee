package core

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// podSelectColumns is podSelect's projection, in scan order.
var podSelectColumns = []string{
	"cloud_resource_id", "tenant_id", "cloud_account_id", "name", "namespace",
	"status", "node_name", "workload_type", "workload_name", "creation_time",
	"last_seen", "restart_count", "labels", "pod_ip", "host_ip", "is_helm_release",
}

var kgNodeColumns = []string{
	"id", "created_at", "updated_at", "properties", "labels", "query_attributes",
	"cloud_account_id", "tenant_id", "unique_key", "node_type", "level", "source",
}

const (
	synthTenant  = "9b7c2a1e-0000-4000-8000-000000000001"
	synthAccount = "9b7c2a1e-0000-4000-8000-000000000002"
	synthPodID   = "9b7c2a1e-0000-4000-8000-000000000003"
	synthWlID    = "9b7c2a1e-0000-4000-8000-000000000004"
)

func newSynthService(t *testing.T) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dbm := &database.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgresql")}
	// The synth fallback scopes itself to ctx's tenant, so the context has to
	// carry one. NewSecurityContextForTenantAdmin resolves the tenant's accounts
	// against the database and returns nil without one, so it can't be used here;
	// NewSecurityContextForAuditRelay carries the tenant with no DB lookup.
	secCtx := security.NewSecurityContextForAuditRelay(synthTenant, "")
	reqCtx := security.NewRequestContext(context.Background(), secCtx, logger, nil, nil)
	return NewService(reqCtx, logger, dbm), mock
}

// expectEdgeMiss stands in for the stored-edge lookup finding nothing — which is
// the case for every Pod edge, since Pods are synthesized at read time and
// knowledge_graph_edge holds no row for them.
func expectEdgeMiss(mock sqlmock.Sqlmock, edgeID string) {
	mock.ExpectQuery(`FROM knowledge_graph_edge`).
		WithArgs(edgeID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
}

// #41504: a Workload→Pod MANAGES edge is built at read time, so kg_get_edge used
// to 404 on it and Edge Details stayed blank. Given the endpoints the caller
// already holds, it must now resolve — with the pod's last_seen as the edge's
// updated_at, which is the "Last seen" the dialog shows.
func TestGetEdgeByIDWithEndpoints_ResolvesSynthManagesEdge(t *testing.T) {
	service, mock := newSynthService(t)
	lastSeen := time.Date(2026, 10, 16, 9, 30, 0, 0, time.UTC)
	edgeID := GenerateEdgeID(synthWlID, synthPodID, RelationshipManages)

	expectEdgeMiss(mock, edgeID)
	// node_name is empty so only the MANAGES branch of NeighborsForPod runs.
	mock.ExpectQuery(`FROM public.k8s_pods`).
		WithArgs(synthPodID, synthTenant).
		WillReturnRows(sqlmock.NewRows(podSelectColumns).AddRow(
			synthPodID, synthTenant, synthAccount, "benchmark-server-55bb479c9f-snmpl", "nudgebee",
			"Running", "", "Deployment", "benchmark-server", lastSeen,
			lastSeen, []byte(`{}`), []byte(`{}`), "10.0.0.1", "10.0.0.2", nil,
		))
	mock.ExpectQuery(`node_type = 'Cluster'`).
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("iteration-test"))
	mock.ExpectQuery(`FROM public.knowledge_graph_node`).
		WillReturnRows(sqlmock.NewRows(kgNodeColumns).AddRow(
			synthWlID, lastSeen, lastSeen, []byte(`{"name":"benchmark-server","kind":"Deployment"}`),
			[]byte(`{}`), []byte(`{}`), synthAccount, synthTenant,
			"k8s:iteration-test::Workload:nudgebee:benchmark-server", "Workload", "Tenant", "k8s",
		))

	got, err := service.GetEdgeByIDWithEndpoints(edgeID, synthWlID, synthPodID)
	if err != nil {
		t.Fatalf("GetEdgeByIDWithEndpoints: %v", err)
	}
	if got == nil {
		t.Fatal("synth MANAGES edge not resolved, want the edge the synthesizer builds")
	}
	if got.ID != edgeID {
		t.Errorf("id = %q, want %q", got.ID, edgeID)
	}
	if got.RelationshipType != RelationshipManages {
		t.Errorf("relationship_type = %q, want MANAGES", got.RelationshipType)
	}
	if got.SourceNodeID != synthWlID || got.DestinationNodeID != synthPodID {
		t.Errorf("endpoints = %q -> %q, want %q -> %q", got.SourceNodeID, got.DestinationNodeID, synthWlID, synthPodID)
	}
	if !got.UpdatedAt.Equal(lastSeen) {
		t.Errorf("updated_at = %v, want the pod's last_seen %v", got.UpdatedAt, lastSeen)
	}
	if got.Source != "k8s" {
		t.Errorf("source = %q, want k8s", got.Source)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// The endpoints are hints, not data: the edge returned must be one the
// synthesizer itself produced. An id that matches no synthesized edge stays
// not-found rather than being fabricated from whatever the caller passed.
func TestGetEdgeByIDWithEndpoints_DoesNotFabricateFromHints(t *testing.T) {
	service, mock := newSynthService(t)
	lastSeen := time.Date(2026, 10, 16, 9, 30, 0, 0, time.UTC)

	expectEdgeMiss(mock, "not-a-synth-edge")
	mock.ExpectQuery(`FROM public.k8s_pods`).
		WithArgs(synthPodID, synthTenant).
		WillReturnRows(sqlmock.NewRows(podSelectColumns).AddRow(
			synthPodID, synthTenant, synthAccount, "pod-a", "nudgebee",
			"Running", "", "Deployment", "benchmark-server", lastSeen,
			lastSeen, []byte(`{}`), []byte(`{}`), "", "", nil,
		))
	mock.ExpectQuery(`node_type = 'Cluster'`).
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("iteration-test"))
	mock.ExpectQuery(`FROM public.knowledge_graph_node`).
		WillReturnRows(sqlmock.NewRows(kgNodeColumns).AddRow(
			synthWlID, lastSeen, lastSeen, []byte(`{"name":"benchmark-server","kind":"Deployment"}`),
			[]byte(`{}`), []byte(`{}`), synthAccount, synthTenant,
			"k8s:iteration-test::Workload:nudgebee:benchmark-server", "Workload", "Tenant", "k8s",
		))
	// Second hint: not a pod, so it resolves to nothing.
	mock.ExpectQuery(`FROM public.k8s_pods`).
		WithArgs(synthWlID, synthTenant).
		WillReturnRows(sqlmock.NewRows(podSelectColumns))

	got, err := service.GetEdgeByIDWithEndpoints("not-a-synth-edge", synthWlID, synthPodID)
	if err != nil {
		t.Fatalf("GetEdgeByIDWithEndpoints: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil — the id matches no synthesized edge", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// Without endpoints there is nothing to resolve a synth edge from (its uuid5 id
// can't be reversed), so the old single-query behaviour must be unchanged — no
// pod scan, and still not-found.
func TestGetEdgeByID_WithoutEndpointsStaysDatabaseOnly(t *testing.T) {
	service, mock := newSynthService(t)

	expectEdgeMiss(mock, "edge-1")

	got, err := service.GetEdgeByID("edge-1")
	if err != nil {
		t.Fatalf("GetEdgeByID: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}
