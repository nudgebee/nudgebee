package core

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

const (
	grantTenant   = "44444444-4444-4444-4444-444444444444"
	grantForeign  = "55555555-5555-5555-5555-555555555555"
	grantAccountA = "44444444-0000-0000-0000-00000000000a"
	grantAccountB = "44444444-0000-0000-0000-00000000000b"
	grantItem     = "44444444-0000-0000-0000-0000000000ff"
)

// grantCaller is a person of grantTenant (accounts A and B) with the given
// built-in roles and custom grants.
func grantCaller(t *testing.T, roles []string, scoped map[string][]string, perms map[string]bool, scopedPerms map[string]map[string]bool) *security.RequestContext {
	t.Helper()
	sc := &security.SecurityContext{}
	raw, err := json.Marshal(map[string]any{
		"TenantId": grantTenant, "UserId": "u1", "AccountIds": []string{grantAccountA, grantAccountB},
		"Roles": roles, "ScopedEntityIds": scoped,
		"CustomPermissions": perms, "ScopedCustomPermissions": scopedPerms,
	})
	require.NoError(t, err)
	require.NoError(t, sc.UnmarshalJSON(raw))
	return security.NewRequestContext(context.Background(), sc, nil, nil, nil)
}

func grantCallers(t *testing.T) map[string]*security.RequestContext {
	return map[string]*security.RequestContext{
		"tenant_admin":          grantCaller(t, []string{security.AUTH_TENANT_ADMIN_ROLE}, nil, nil, nil),
		"tenant_admin_readonly": grantCaller(t, []string{security.AUTH_TENANT_READ_ADMIN_ROLE}, nil, nil, nil),
		"account_admin_a": grantCaller(t, []string{security.AUTH_ACCOUNT_ADMIN_ROLE},
			map[string][]string{security.AUTH_ACCOUNT_ADMIN_ROLE: {grantAccountA}}, nil, nil),
		"no_role":              grantCaller(t, nil, nil, nil, nil),
		"tools_read":           grantCaller(t, nil, nil, map[string]bool{"ai_tools:Read": true}, nil),
		"tools_write":          grantCaller(t, nil, nil, map[string]bool{"ai_tools:Write": true}, nil),
		"tools_execute":        grantCaller(t, nil, nil, map[string]bool{"ai_tools:Execute": true}, nil),
		"tools_read_scoped_a":  grantCaller(t, nil, nil, nil, map[string]map[string]bool{grantAccountA: {"ai_tools:Read": true}}),
		"tools_write_scoped_a": grantCaller(t, nil, nil, nil, map[string]map[string]bool{grantAccountA: {"ai_tools:Write": true}}),
		"agents_read":          grantCaller(t, nil, nil, map[string]bool{"ai_agents:Read": true}, nil),
		"agents_write":         grantCaller(t, nil, nil, map[string]bool{"ai_agents:Write": true}, nil),
		"agents_write_scoped_a": grantCaller(t, nil, nil, nil,
			map[string]map[string]bool{grantAccountA: {"ai_agents:Write": true}}),
		"kbs_write": grantCaller(t, nil, nil, map[string]bool{"ai_kbs:Write": true}, nil),
	}
}

// grantMockDB installs a fresh mocked metastore for one call.
func grantMockDB(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	sdb := sqlx.NewDb(db, "postgres")
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sdb}, nil
	})
	t.Cleanup(func() {
		common.RegisterDatabaseManagerHook(common.Metastore, nil)
		_ = db.Close()
	})
	return mock
}

func grantOwnerRow(tenant, visibility string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "name", "tenant_id", "visibility", "account_id"}).
		AddRow(grantItem, "deploy-check", tenant, visibility, "")
}

// expectLockedOwner is LockOrgItemOwner inside DoInTransaction. Anything the
// call runs after the authorization check is unexpected and fails the call with
// a non-authorization error.
func expectLockedOwner(mock sqlmock.Sqlmock, tenant, visibility string) {
	mock.ExpectBegin()
	mock.ExpectExec(`FOR UPDATE`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT t.id::text AS id`).WillReturnRows(grantOwnerRow(tenant, visibility))
	mock.ExpectRollback()
}

type orgGrantCase struct {
	caller string
	allow  bool
}

func assertOrgGrant(t *testing.T, err error, allow bool, unauthorized string) {
	t.Helper()
	refused := err != nil && err.Error() == unauthorized
	if allow {
		assert.False(t, refused, "want allowed, got %v", err)
	} else {
		assert.True(t, refused, "want refused, got %v", err)
	}
}

// Organisation-wide tools: changing who a tool reaches and its per-account
// switches is open to a tenant-wide ai_tools:Write grant as well as tenant
// admins; listing the switches to a tenant-wide ai_tools Read or Write grant as
// well as tenant readers. Account-scoped grants, Read-for-a-write and another
// module's grant are refused.
func TestOrgToolSharingGrants(t *testing.T) {
	callers := grantCallers(t)

	t.Run("ai_update_tool_visibility", func(t *testing.T) {
		for _, tc := range []orgGrantCase{
			{"tenant_admin", true},
			{"tools_write", true},
			{"tenant_admin_readonly", false},
			{"account_admin_a", false},
			{"tools_read", false},
			{"tools_execute", false},
			{"tools_write_scoped_a", false},
			{"agents_write", false},
		} {
			t.Run(tc.caller, func(t *testing.T) {
				mock := grantMockDB(t)
				expectLockedOwner(mock, grantTenant, VisibilityOrganisation)
				err := SetCustomToolVisibility(callers[tc.caller], grantItem, VisibilityAccount, nil, grantAccountA)
				assertOrgGrant(t, err, tc.allow, errUnauthorized)
			})
		}
		t.Run("tools_write on another tenant's tool", func(t *testing.T) {
			mock := grantMockDB(t)
			expectLockedOwner(mock, grantForeign, VisibilityOrganisation)
			err := SetCustomToolVisibility(callers["tools_write"], grantItem, VisibilityAccount, nil, grantAccountA)
			assertOrgGrant(t, err, false, errUnauthorized)
		})
	})

	t.Run("ai_update_tool_account_override", func(t *testing.T) {
		// A selected-accounts tool: only those who may share it choose its accounts.
		for _, tc := range []orgGrantCase{
			{"tenant_admin", true},
			{"tools_write", true},
			{"tenant_admin_readonly", false},
			{"account_admin_a", false},
			{"tools_read", false},
			{"tools_write_scoped_a", false},
			{"agents_write", false},
		} {
			t.Run(tc.caller, func(t *testing.T) {
				mock := grantMockDB(t)
				expectLockedOwner(mock, grantTenant, VisibilitySelectedAccounts)
				err := SetCustomToolAccountSwitch(callers[tc.caller], grantItem, grantAccountA, true)
				assertOrgGrant(t, err, tc.allow, errUnauthorized)
			})
		}
		// An organisation-wide tool: an account admin still switches it for their account.
		for _, tc := range []orgGrantCase{
			{"account_admin_a", true},
			{"tools_write_scoped_a", false},
		} {
			t.Run(tc.caller+" on an organisation-wide tool", func(t *testing.T) {
				mock := grantMockDB(t)
				expectLockedOwner(mock, grantTenant, VisibilityOrganisation)
				err := SetCustomToolAccountSwitch(callers[tc.caller], grantItem, grantAccountA, false)
				assertOrgGrant(t, err, tc.allow, errUnauthorized)
			})
		}
	})

	t.Run("ai_list_tool_account_overrides", func(t *testing.T) {
		for _, tc := range []orgGrantCase{
			{"tenant_admin", true},
			{"tenant_admin_readonly", true},
			{"tools_read", true},
			{"tools_write", true},
			{"account_admin_a", false},
			{"no_role", false},
			{"tools_execute", false},
			{"tools_read_scoped_a", false},
			{"agents_read", false},
		} {
			t.Run(tc.caller, func(t *testing.T) {
				mock := grantMockDB(t)
				mock.ExpectQuery(`SELECT t.id::text AS id`).WillReturnRows(grantOwnerRow(grantTenant, VisibilityOrganisation))
				mock.ExpectQuery(`FROM llm_tool_account_overrides`).WillReturnError(errors.New("listed"))
				_, err := ListCustomToolAccountSwitches(callers[tc.caller], grantItem)
				assertOrgGrant(t, err, tc.allow, errUnauthorized)
			})
		}
	})
}

// pqArrayArg matches a pq.Array(ids) argument.
type pqArrayArg []string

func (a pqArrayArg) Match(v driver.Value) bool {
	want, err := pq.Array([]string(a)).Value()
	return err == nil && v == want
}

// The tenant-wide tool list (ai_list_tools without an account): tenant-wide
// readers keep the unfiltered query; everyone else gets account tools of the
// accounts they may read ($3) and the organisation tools as before ($4: every
// account of the tenant).
func TestListCustomToolsForTenantGrants(t *testing.T) {
	callers := grantCallers(t)
	all := pqArrayArg{grantAccountA, grantAccountB}
	for _, tc := range []struct {
		caller   string
		readable pqArrayArg // nil: sees every account (no account filter)
	}{
		{"tenant_admin", nil},
		{"tenant_admin_readonly", nil},
		{"tools_read", nil},
		{"tools_write", nil},
		{"account_admin_a", pqArrayArg{grantAccountA}},
		{"tools_read_scoped_a", pqArrayArg{grantAccountA}},
		{"tools_write_scoped_a", pqArrayArg{grantAccountA}},
		{"tools_execute", pqArrayArg{}},
		{"agents_read", pqArrayArg{}},
		{"no_role", pqArrayArg{}},
	} {
		t.Run(tc.caller, func(t *testing.T) {
			mock := grantMockDB(t)
			q := mock.ExpectQuery(`FROM llm_tools lt`)
			if tc.readable == nil {
				q.WithArgs(ToolTypeCustom, grantTenant)
			} else {
				q.WithArgs(ToolTypeCustom, grantTenant, tc.readable, all)
			}
			q.WillReturnRows(sqlmock.NewRows([]string{"id"}))
			ListCustomToolsForTenant(callers[tc.caller])
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
