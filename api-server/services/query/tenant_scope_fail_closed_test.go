package query

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unscopedTables lists the registered tables that have no tenant column, are not
// global catalogs and are not authorized per account by their source.
func unscopedTables() []string {
	out := []string{}
	for name, def := range table_metadata {
		if def.TenantIdColumnName == "" && !def.GlobalCatalog && def.SourceGenerator == nil && !checksAccountAccess(def.Source) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// stubMetastore points the Metastore at a sqlmock that answers every query with
// one empty result and counts how many statements reached it.
func stubMetastore(t *testing.T) *int {
	t.Helper()
	executed := 0
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(_ string, _ string) error {
		executed++
		return nil
	})))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 50; i++ {
		mock.ExpectQuery("").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
	})
	return &executed
}

func TestUnscopedTablesAreTheKnownUserLookups(t *testing.T) {
	// The tables NextAuth reads as the server identity. A new table without a
	// tenant column fails closed at runtime; this pins that it was meant to.
	assert.Equal(t, []string{
		"tenant_by_user_v2",
		"user_auth_by_username_v2",
		"user_by_provider_account_v2",
		"user_details_v2",
		"user_super_admin_role_v2",
	}, unscopedTables())
}

func TestExecuteQuery_TableWithoutTenantColumnFailsClosed(t *testing.T) {
	const tenant = "11111111-1111-1111-1111-111111111111"
	const acct = "22222222-2222-2222-2222-222222222222"
	tenantUsers := map[string]*security.RequestContext{
		"tenant_admin":  scopedCtx(t, fmt.Sprintf(`{"TenantId":%q,"UserId":"u1","Roles":[%q]}`, tenant, security.AUTH_TENANT_ADMIN_ROLE)),
		"account_admin": scopedCtx(t, fmt.Sprintf(`{"TenantId":%q,"UserId":"u1","Roles":[%q],"AccountIds":[%q]}`, tenant, security.AUTH_ACCOUNT_ADMIN_ROLE, acct)),
		"users_grant":   scopedCtx(t, fmt.Sprintf(`{"TenantId":%q,"UserId":"u1","CustomPermissions":{"users:Read":true,"tenants:Read":true}}`, tenant)),
		// A session with no tenant used to skip every tenant restriction.
		"no_tenant": scopedCtx(t, `{"UserId":"u1","Roles":["tenant_admin"]}`),
		// super_admin_readonly is not super admin: it does not read credentials.
		"super_admin_readonly": scopedCtx(t, fmt.Sprintf(`{"TenantId":%q,"UserId":"u1","Roles":[%q]}`, tenant, security.AUTH_SUPER_ADMIN_READONLY_ROLE)),
	}

	for _, table := range unscopedTables() {
		for who, ctx := range tenantUsers {
			t.Run(table+"/"+who, func(t *testing.T) {
				executed := stubMetastore(t)
				resp, err := ExecuteQuery(ctx, QueryRequest{Table: table, Columns: []QueryColumn{{Name: firstColumn(table)}}})
				require.NoError(t, err)
				require.Len(t, resp.Errors, 1)
				assert.Equal(t, "Access Denied", resp.Errors[0].Message)
				assert.Zero(t, *executed, "no SQL may run")
			})
		}
	}
}

func TestExecuteQuery_TableWithoutTenantColumnOpenToServerAndSuperAdmin(t *testing.T) {
	callers := map[string]*security.RequestContext{
		"server_internal": security.NewRequestContext(context.Background(), security.NewSecurityContextForSuperAdmin(), slog.Default(), nil, nil),
		"super_admin":     scopedCtx(t, `{"TenantId":"11111111-1111-1111-1111-111111111111","UserId":"u1","Roles":["super_admin"]}`),
	}
	for _, table := range unscopedTables() {
		for who, ctx := range callers {
			t.Run(table+"/"+who, func(t *testing.T) {
				executed := stubMetastore(t)
				resp, err := ExecuteQuery(ctx, QueryRequest{Table: table, Columns: []QueryColumn{{Name: firstColumn(table)}}})
				require.NoError(t, err)
				assert.Empty(t, resp.Errors)
				assert.Positive(t, *executed, "the query must reach the database")
			})
		}
	}
}

func TestExecuteQuery_GlobalCatalogStaysReadableByTenantUsers(t *testing.T) {
	ctx := scopedCtx(t, `{"TenantId":"11111111-1111-1111-1111-111111111111","UserId":"u1","Roles":["account_admin"],"AccountIds":["22222222-2222-2222-2222-222222222222"]}`)
	for _, table := range []string{"knowledge_base_v2", "feature_v2", "cloud_resource_details_v2"} {
		t.Run(table, func(t *testing.T) {
			executed := stubMetastore(t)
			resp, err := ExecuteQuery(ctx, QueryRequest{Table: table, Columns: []QueryColumn{{Name: firstColumn(table)}}})
			require.NoError(t, err)
			assert.Empty(t, resp.Errors)
			assert.Positive(t, *executed, "the query must reach the database")
		})
	}
}

func TestGlobalCatalogTablesArePinned(t *testing.T) {
	got := []string{}
	for name, def := range table_metadata {
		if def.GlobalCatalog {
			got = append(got, name)
		}
	}
	sort.Strings(got)
	// Every row here is readable by every tenant. Add a table only when it holds
	// no tenant data at all.
	assert.Equal(t, []string{"anomaly_type_v2", "cloud_resource_details_v2", "feature_v2", "knowledge_base_v2"}, got)
}

func firstColumn(table string) string {
	names := []string{}
	for name, col := range table_metadata[table].Columns {
		if !col.IsAggregated {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names[0]
}
