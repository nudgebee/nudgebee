package query

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Traces on an agent-warehouse source (ClickHouse via the agent, BigQuery,
// Chronosphere) are checked per account in ExecuteQuery, outside the metastore
// branch. They honour the table's PermissionModule grant like that branch does:
// a traces grant reads traces_v2, another module's grant or none does not, and
// built-in account roles read as before.
func TestAgentWarehouseTracesHonourTheTablesModuleGrant(t *testing.T) {
	const account = "11111111-1111-1111-1111-111111111111"
	chronosphere := "https://tenant.chronosphere.io"
	bigquery := "bigquery"
	sources := []struct {
		name     string
		features map[string]any
	}{
		{"agent warehouse", map[string]any{}},
		{"bigquery", map[string]any{"traceProvider": bigquery}},
		{"chronosphere", map[string]any{"prometheusUrl": chronosphere}},
	}
	callers := []struct {
		name    string
		context string
		allowed bool
	}{
		{"traces:Read tenant-wide", `"CustomPermissions":{"traces:Read":true}`, true},
		{"traces:Write implies read", `"CustomPermissions":{"traces:Write":true}`, true},
		{"traces:Read bound to the account", `"ScopedCustomPermissions":{"` + account + `":{"traces:Read":true}}`, true},
		{"traces:Read bound to another account", `"ScopedCustomPermissions":{"99999999-9999-9999-9999-999999999999":{"traces:Read":true}}`, false},
		// The engine is reached from other actions too: a dashboard panel's grant
		// must not open the traces table.
		{"dashboards:Execute", `"CustomPermissions":{"dashboards:Execute":true}`, false},
		{"no grant", `"CustomPermissions":{}`, false},
		{"built-in account_admin_readonly", `"Roles":["account_admin_readonly"],"ScopedEntityIds":{"account_admin_readonly":["` + account + `"]}`, true},
	}

	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	metastore := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) { return metastore, nil })
	unreachable := func() (*database.DatabaseManager, error) { return nil, errors.New("warehouse not reachable in tests") }
	for _, w := range []database.DatabaseManagerType{database.AgentWarehouse, database.AgentWarehouseBigQuery, database.AgentWarehouseChronosphere} {
		database.RegisterDatabaseManagerHook(w, unreachable)
	}
	t.Cleanup(func() {
		for _, w := range []database.DatabaseManagerType{database.Metastore, database.AgentWarehouse, database.AgentWarehouseBigQuery, database.AgentWarehouseChronosphere} {
			database.RegisterDatabaseManagerHook(w, nil)
		}
	})

	for _, src := range sources {
		details, err := json.Marshal(map[string]any{"features": src.features})
		require.NoError(t, err)
		require.NoError(t, common.CacheSet("nb_agents", account, details))
		for _, caller := range callers {
			t.Run(src.name+"/"+caller.name, func(t *testing.T) {
				var sc security.SecurityContext
				require.NoError(t, json.Unmarshal([]byte(`{"TenantId":"tenant-1","UserId":"user-1","AccountIds":["`+account+`"],`+caller.context+`}`), &sc))
				ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)

				resp, _ := ExecuteQuery(ctx, QueryRequest{
					Table:   "traces_v2",
					Columns: []QueryColumn{{Name: "trace_id"}},
					Where:   QueryWhereClause{Binary: map[string]map[BinaryWhereClauseType]any{"account_id": {Eq: account}}},
				})

				denied := len(resp.Errors) > 0 && resp.Errors[0].Message == "Access Denied"
				assert.Equal(t, !caller.allowed, denied, "errors: %v", resp.Errors)
			})
		}
	}
	_ = common.CacheDelete("nb_agents", account)

	// A non-string account id is refused, not a panic.
	t.Run("non-string account id", func(t *testing.T) {
		var sc security.SecurityContext
		require.NoError(t, json.Unmarshal([]byte(`{"TenantId":"tenant-1","UserId":"user-1","AccountIds":["`+account+`"],"Roles":["tenant_admin"]}`), &sc))
		ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
		for _, id := range []any{12345, nil, []string{account}} {
			resp, _ := ExecuteQuery(ctx, QueryRequest{
				Table:   "traces_v2",
				Columns: []QueryColumn{{Name: "trace_id"}},
				Where:   QueryWhereClause{Binary: map[string]map[BinaryWhereClauseType]any{"account_id": {Eq: id}}},
			})
			assert.NotEmpty(t, resp.Errors, "%v", id)
		}
	})
}
