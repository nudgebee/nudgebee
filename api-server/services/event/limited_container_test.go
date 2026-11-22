package event

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/internal/database"
)

func setupLimitedContainerTest(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	manager := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return manager, nil
	})
	return mock
}

func TestSingleLimitedContainer(t *testing.T) {
	tests := []struct {
		name  string
		names []string
		err   error
		want  string
	}{
		{name: "one limited container is named", names: []string{"app"}, want: "app"},
		{name: "no limited container", names: nil, want: ""},
		{name: "several limited containers are ambiguous", names: []string{"app", "istio-proxy"}, want: ""},
		{name: "a failed read names none", err: errors.New("connection refused"), want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := setupLimitedContainerTest(t)
			// The workload inventory, not the pod inventory: only its container specs
			// carry resources. The kind is matched in lower case.
			q := mock.ExpectQuery(`FROM k8s_workloads w`).
				WithArgs("acc-1", "shop", "orders", "deployment", "cpu")
			if tt.err != nil {
				q.WillReturnError(tt.err)
			} else {
				rows := sqlmock.NewRows([]string{"name"})
				for _, n := range tt.names {
					rows.AddRow(n)
				}
				q.WillReturnRows(rows)
			}

			got := SingleLimitedContainer("acc-1", "shop", "Deployment", "orders", "cpu")

			assert.Equal(t, tt.want, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
