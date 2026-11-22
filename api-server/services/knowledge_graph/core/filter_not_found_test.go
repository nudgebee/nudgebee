package core

import (
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// The default-filter endpoint answers "no filter yet" only for a missing row;
// a failed read must stay distinguishable so it is not shown as defaults.
func TestGetFilterForTenantTellsMissingFromFailed(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := security.NewRequestContextForTenantAdmin("tenant-1", logger, nil, nil)
	for name, tc := range map[string]struct {
		dbErr       error
		wantMissing bool
	}{
		"missing row": {sql.ErrNoRows, true},
		"db failure":  {errors.New("connection reset by peer"), false},
	} {
		t.Run(name, func(t *testing.T) {
			rawDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			t.Cleanup(func() { _ = rawDB.Close() })
			q := mock.ExpectQuery(`FROM knowledge_graph_tenant_filters`)
			if tc.dbErr == sql.ErrNoRows {
				// As in production: the query runs and Scan finds no row.
				q.WillReturnRows(sqlmock.NewRows([]string{"id"}))
			} else {
				q.WillReturnError(tc.dbErr)
			}
			repo := NewFilterRepository(&database.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgresql")}, logger)

			_, err = repo.GetFilterForTenant(ctx, "tenant-1", "")
			if err == nil {
				t.Fatal("want an error")
			}
			if got := errors.Is(err, sql.ErrNoRows); got != tc.wantMissing {
				t.Fatalf("errors.Is(err, sql.ErrNoRows) = %v, want %v (err: %v)", got, tc.wantMissing, err)
			}
			if got := isNoRowsError(err); got != tc.wantMissing {
				t.Fatalf("isNoRowsError = %v, want %v", got, tc.wantMissing)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("filter query did not run: %v", err)
			}
		})
	}
}
