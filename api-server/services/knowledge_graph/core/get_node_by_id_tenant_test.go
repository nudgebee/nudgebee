package core

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// Node ids are global, and kg_get_node takes one straight from the request:
// the lookup must only find nodes of the caller's tenant.
func TestGetNodeByIDIsScopedToTheCallersTenant(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	dbm := &database.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgresql")}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(`{"TenantId":"tenant-1","Roles":["tenant_admin"]}`), &sc))
	service := NewService(security.NewRequestContext(context.Background(), &sc, logger, nil, nil), logger, dbm)

	mock.ExpectQuery(`FROM knowledge_graph_node\s+WHERE id = ANY\(\$1::uuid\[\]\)\s+AND tenant_id = \$2::uuid`).
		WithArgs(sqlmock.AnyArg(), "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	// Not a stored node: the pod synthesizer is asked next, also within the tenant.
	mock.ExpectQuery(`cloud_resource_id::text`).
		WithArgs("node-of-another-tenant", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"cloud_resource_id"}))

	node, err := service.GetNodeByID("node-of-another-tenant")
	require.NoError(t, err)
	assert.Nil(t, node)

	// No caller, or no tenant: nothing is looked up.
	node, err = NewService(nil, logger, dbm).GetNodeByID("node-of-another-tenant")
	require.NoError(t, err)
	assert.Nil(t, node)
	nodes, err := service.fetchNodesByIDs("", []string{"node-of-another-tenant"})
	require.NoError(t, err)
	assert.Empty(t, nodes)

	require.NoError(t, mock.ExpectationsWereMet())
}
