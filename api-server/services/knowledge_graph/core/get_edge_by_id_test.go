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

// #40706: kg_get_edge must return the edge's `source` column. Edge Details
// shows it as the edge's origin, and an edge rewritten by a resource patch has
// no properties.contributing_sources to fall back on.
func TestGetEdgeByIDReturnsSource(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	dbm := &database.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgresql")}

	now := time.Now()
	mock.ExpectQuery(`COALESCE\(source, ''\)\s+FROM knowledge_graph_edge`).
		WithArgs("edge-1", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "created_at", "updated_at", "relationship_type", "properties",
			"cloud_account_id", "tenant_id", "source_node_id", "destination_node_id", "level", "source",
		}).AddRow(
			"edge-1", now, now, "RUNS_AS", []byte(`{"source":"iam_role"}`),
			"acct-1", "tenant-1", "n1", "n2", "Tenant", "aws",
		))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(security.NewRequestContext(context.Background(), security.NewSecurityContextForAuditRelay("tenant-1", ""), logger, nil, nil), logger, dbm)

	got, err := service.GetEdgeByID("edge-1")
	if err != nil {
		t.Fatalf("GetEdgeByID: %v", err)
	}
	if got == nil || got.Source != "aws" {
		t.Fatalf("GetEdgeByID source = %+v, want aws", got)
	}
	if got.Properties["source"] != "iam_role" {
		t.Errorf("properties.source = %v, want iam_role untouched", got.Properties["source"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}
