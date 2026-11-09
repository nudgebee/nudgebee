package query

import (
	"fmt"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureGroupsSQL runs an application-group query as ctx against a mock database
// and returns the SQL the engine sent.
func captureGroupsSQL(t *testing.T, ctx *security.RequestContext, request QueryRequest) (string, QueryResponse) {
	t.Helper()
	var captured string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(_ string, actual string) error {
		captured = actual
		return nil
	})))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
	})
	mock.ExpectQuery("").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	resp, err := ExecuteQuery(ctx, request)
	require.NoError(t, err)
	return captured, resp
}

func groupsRequest() QueryRequest {
	return QueryRequest{Table: "applications_list_groups", Columns: fromStringColsToQueryCols([]string{"id"}), Limit: 10}
}

func TestApplicationGroupScope_AccountAdminGetsExistsPredicate(t *testing.T) {
	ctx := scopedCtx(t, fmt.Sprintf(`{"TenantId":"t1","UserId":"u1","Roles":[%[1]q],"AccountIds":["acctA","acctB","acctC"],"ScopedEntityIds":{%[1]q:["acctA","acctB"]}}`, security.AUTH_ACCOUNT_ADMIN_ROLE))

	sql, resp := captureGroupsSQL(t, ctx, groupsRequest())

	assert.Empty(t, resp.Errors, "account_admin must not be rejected with 'account id column not found'")
	assert.Contains(t, sql, "EXISTS (SELECT 1 FROM application_group_mapping agm WHERE agm.group_id = application_group_v2.id::uuid")
	assert.Contains(t, sql, "IN ('acctA', 'acctB')")
	assert.NotContains(t, sql, "acctC", "the caller holds acctC tenant-wide but not via this scoped role, so it must not leak in")
	assert.Contains(t, sql, "t1", "the tenant filter still applies")
}

func TestApplicationGroupScope_AggregateTableIsScopedToo(t *testing.T) {
	ctx := scopedCtx(t, fmt.Sprintf(`{"TenantId":"t1","UserId":"u1","Roles":[%[1]q],"AccountIds":["acctA"],"ScopedEntityIds":{%[1]q:["acctA"]}}`, security.AUTH_ACCOUNT_READ_ADMIN_ROLE))

	sql, resp := captureGroupsSQL(t, ctx, QueryRequest{Table: "applications_aggregate_groups", Limit: 10})

	assert.Empty(t, resp.Errors)
	assert.Contains(t, sql, "EXISTS (SELECT 1 FROM application_group_mapping agm WHERE agm.group_id = application_group.id::uuid")
	assert.Contains(t, sql, "IN ('acctA')")
}

func TestApplicationGroupScope_TenantAdminIsNotRestricted(t *testing.T) {
	ctx := scopedCtx(t, fmt.Sprintf(`{"TenantId":"t1","UserId":"u1","Roles":[%q]}`, security.AUTH_TENANT_ADMIN_ROLE))

	sql, resp := captureGroupsSQL(t, ctx, groupsRequest())

	assert.Empty(t, resp.Errors)
	assert.NotContains(t, sql, "application_group_mapping")
}

func TestApplicationGroupScope_ScopedGrantUsesTheGrantedAccounts(t *testing.T) {
	ctx := scopedCtx(t, `{"TenantId":"t1","UserId":"u1","Roles":[],"AccountIds":["acctA","acctB"],
		"ScopedCustomPermissions":{"acctA":{"applications:Read":true}}}`)

	sql, resp := captureGroupsSQL(t, ctx, groupsRequest())

	assert.Empty(t, resp.Errors)
	assert.Contains(t, sql, "IN ('acctA')")
	assert.NotContains(t, sql, "acctB")
}

func TestApplicationGroupScope_NoAccountsSeesNothing(t *testing.T) {
	assert.Equal(t, "FALSE", tableAccountScope(table_metadata["application_group_v2"], nil))
}

func TestApplicationGroupScope_IdsAreQuoted(t *testing.T) {
	predicate := tableAccountScope(table_metadata["application_group_v2"], []string{"a' OR '1'='1"})
	assert.Contains(t, predicate, "IN ('a'' OR ''1''=''1')")
}

// The production decoder fills a field tagged json:"-" from a request key named
// "-", so the scope must never be reachable through the request. Mirrors the same
// injection guard notification_rule_scope_test.go asserts for notification rules.
func TestApplicationGroupScope_RequestCannotInjectSQL(t *testing.T) {
	raw := map[string]any{
		"table":   "applications_list_groups",
		"limit":   10,
		"columns": []any{map[string]any{"name": "id"}},
		"where": map[string]any{
			"-":       "1=1 OR TRUE",
			"_binary": map[string]any{"name": map[string]any{"_eq": "x"}},
		},
		"-": "1=1 OR TRUE",
	}
	var request QueryRequest
	require.NoError(t, common.UnmarshalMapToStruct(raw, &request))
	ctx := scopedCtx(t, fmt.Sprintf(`{"TenantId":"t1","UserId":"u1","Roles":[%[1]q],"AccountIds":["acctA"],"ScopedEntityIds":{%[1]q:["acctA"]}}`, security.AUTH_ACCOUNT_ADMIN_ROLE))

	sql, _ := captureGroupsSQL(t, ctx, request)

	assert.NotContains(t, sql, "1=1 OR TRUE")
	assert.Contains(t, sql, "IN ('acctA')")
}
