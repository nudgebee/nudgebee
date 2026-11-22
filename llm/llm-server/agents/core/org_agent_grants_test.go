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
	toolcore "nudgebee/llm/tools/core"
)

const (
	agentGrantTenant   = "66666666-6666-6666-6666-666666666666"
	agentGrantForeign  = "77777777-7777-7777-7777-777777777777"
	agentGrantAccountA = "66666666-0000-0000-0000-00000000000a"
	agentGrantAccountB = "66666666-0000-0000-0000-00000000000b"
	agentGrantItem     = "66666666-0000-0000-0000-0000000000ff"
	agentUnauthorized  = "auth: unauthorized"
)

// agentGrantCaller is a person of agentGrantTenant (accounts A and B) with the
// given built-in roles and custom grants.
func agentGrantCaller(t *testing.T, roles []string, scoped map[string][]string, perms map[string]bool, scopedPerms map[string]map[string]bool) *security.RequestContext {
	t.Helper()
	sc := &security.SecurityContext{}
	raw, err := json.Marshal(map[string]any{
		"TenantId": agentGrantTenant, "UserId": "u1", "AccountIds": []string{agentGrantAccountA, agentGrantAccountB},
		"Roles": roles, "ScopedEntityIds": scoped,
		"CustomPermissions": perms, "ScopedCustomPermissions": scopedPerms,
	})
	require.NoError(t, err)
	require.NoError(t, sc.UnmarshalJSON(raw))
	return security.NewRequestContext(context.Background(), sc, nil, nil, nil)
}

func agentGrantCallers(t *testing.T) map[string]*security.RequestContext {
	return map[string]*security.RequestContext{
		"tenant_admin":          agentGrantCaller(t, []string{security.AUTH_TENANT_ADMIN_ROLE}, nil, nil, nil),
		"tenant_admin_readonly": agentGrantCaller(t, []string{security.AUTH_TENANT_READ_ADMIN_ROLE}, nil, nil, nil),
		"account_admin_a": agentGrantCaller(t, []string{security.AUTH_ACCOUNT_ADMIN_ROLE},
			map[string][]string{security.AUTH_ACCOUNT_ADMIN_ROLE: {agentGrantAccountA}}, nil, nil),
		"no_role":        agentGrantCaller(t, nil, nil, nil, nil),
		"agents_read":    agentGrantCaller(t, nil, nil, map[string]bool{"ai_agents:Read": true}, nil),
		"agents_write":   agentGrantCaller(t, nil, nil, map[string]bool{"ai_agents:Write": true}, nil),
		"agents_execute": agentGrantCaller(t, nil, nil, map[string]bool{"ai_agents:Execute": true}, nil),
		"agents_read_scoped_a": agentGrantCaller(t, nil, nil, nil,
			map[string]map[string]bool{agentGrantAccountA: {"ai_agents:Read": true}}),
		"agents_write_scoped_a": agentGrantCaller(t, nil, nil, nil,
			map[string]map[string]bool{agentGrantAccountA: {"ai_agents:Write": true}}),
		"tools_read":  agentGrantCaller(t, nil, nil, map[string]bool{"ai_tools:Read": true}, nil),
		"tools_write": agentGrantCaller(t, nil, nil, map[string]bool{"ai_tools:Write": true}, nil),
	}
}

func agentGrantMockDB(t *testing.T) sqlmock.Sqlmock {
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

func agentOwnerRow(tenant, visibility string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "name", "tenant_id", "visibility", "account_id"}).
		AddRow(agentGrantItem, "triage", tenant, visibility, "")
}

// expectLockedAgentOwner is LockOrgItemOwner inside DoInTransaction; whatever
// runs after the authorization check is unexpected and fails the call with a
// non-authorization error.
func expectLockedAgentOwner(mock sqlmock.Sqlmock, tenant, visibility string) {
	mock.ExpectBegin()
	mock.ExpectExec(`FOR UPDATE`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT t.id::text AS id`).WillReturnRows(agentOwnerRow(tenant, visibility))
	mock.ExpectRollback()
}

func assertAgentGrant(t *testing.T, err error, allow bool) {
	t.Helper()
	refused := err != nil && err.Error() == agentUnauthorized
	if allow {
		assert.False(t, refused, "want allowed, got %v", err)
	} else {
		assert.True(t, refused, "want refused, got %v", err)
	}
}

type agentGrantCase struct {
	caller string
	allow  bool
}

// Organisation agents: sharing them (visibility, per-account switches) is open
// to a tenant-wide ai_agents:Write grant as well as tenant admins; listing the
// switches to a tenant-wide ai_agents Read or Write grant as well as tenant
// readers. Account-scoped grants, Read-for-a-write and the ai_tools grant (a
// different item's module) are refused.
func TestOrgAgentSharingGrants(t *testing.T) {
	callers := agentGrantCallers(t)

	t.Run("ai_update_agent_visibility", func(t *testing.T) {
		for _, tc := range []agentGrantCase{
			{"tenant_admin", true},
			{"agents_write", true},
			{"tenant_admin_readonly", false},
			{"account_admin_a", false},
			{"agents_read", false},
			{"agents_execute", false},
			{"agents_write_scoped_a", false},
			{"tools_write", false},
		} {
			t.Run(tc.caller, func(t *testing.T) {
				mock := agentGrantMockDB(t)
				expectLockedAgentOwner(mock, agentGrantTenant, toolcore.VisibilityOrganisation)
				err := SetCustomAgentVisibility(callers[tc.caller], agentGrantItem, toolcore.VisibilityAccount, nil, agentGrantAccountA)
				assertAgentGrant(t, err, tc.allow)
			})
		}
		t.Run("agents_write on another tenant's agent", func(t *testing.T) {
			mock := agentGrantMockDB(t)
			expectLockedAgentOwner(mock, agentGrantForeign, toolcore.VisibilityOrganisation)
			err := SetCustomAgentVisibility(callers["agents_write"], agentGrantItem, toolcore.VisibilityAccount, nil, agentGrantAccountA)
			assertAgentGrant(t, err, false)
		})
	})

	t.Run("ai_update_agent_account_override", func(t *testing.T) {
		for _, tc := range []agentGrantCase{
			{"tenant_admin", true},
			{"agents_write", true},
			{"tenant_admin_readonly", false},
			{"account_admin_a", false},
			{"agents_read", false},
			{"agents_write_scoped_a", false},
			{"tools_write", false},
		} {
			t.Run(tc.caller, func(t *testing.T) {
				mock := agentGrantMockDB(t)
				expectLockedAgentOwner(mock, agentGrantTenant, toolcore.VisibilitySelectedAccounts)
				err := SetCustomAgentAccountSwitch(callers[tc.caller], agentGrantItem, agentGrantAccountA, true)
				assertAgentGrant(t, err, tc.allow)
			})
		}
		t.Run("account_admin_a on an organisation-wide agent", func(t *testing.T) {
			mock := agentGrantMockDB(t)
			expectLockedAgentOwner(mock, agentGrantTenant, toolcore.VisibilityOrganisation)
			err := SetCustomAgentAccountSwitch(callers["account_admin_a"], agentGrantItem, agentGrantAccountA, false)
			assertAgentGrant(t, err, true)
		})
	})

	t.Run("ai_list_agent_account_overrides", func(t *testing.T) {
		for _, tc := range []agentGrantCase{
			{"tenant_admin", true},
			{"tenant_admin_readonly", true},
			{"agents_read", true},
			{"agents_write", true},
			{"account_admin_a", false},
			{"no_role", false},
			{"agents_execute", false},
			{"agents_read_scoped_a", false},
			{"tools_read", false},
		} {
			t.Run(tc.caller, func(t *testing.T) {
				mock := agentGrantMockDB(t)
				mock.ExpectQuery(`SELECT t.id::text AS id`).WillReturnRows(agentOwnerRow(agentGrantTenant, toolcore.VisibilityOrganisation))
				mock.ExpectQuery(`FROM llm_agent_account_overrides`).WillReturnError(errors.New("listed"))
				_, err := ListCustomAgentAccountSwitches(callers[tc.caller], agentGrantItem)
				assertAgentGrant(t, err, tc.allow)
			})
		}
	})
}

// agentPqArrayArg matches a pq.Array(ids) argument.
type agentPqArrayArg []string

func (a agentPqArrayArg) Match(v driver.Value) bool {
	want, err := pq.Array([]string(a)).Value()
	return err == nil && v == want
}

// The tenant-wide agent list (ai_list_agents without an account): tenant-wide
// readers keep the unfiltered query; everyone else gets account agents of the
// accounts they may read ($3) and the organisation agents as before ($4: every
// account of the tenant). An account admin of A sees A's custom agents only.
func TestListCustomAgentsForTenantGrants(t *testing.T) {
	callers := agentGrantCallers(t)
	all := agentPqArrayArg{agentGrantAccountA, agentGrantAccountB}
	for _, tc := range []struct {
		caller   string
		readable agentPqArrayArg // nil: sees every account (no account filter)
	}{
		{"tenant_admin", nil},
		{"tenant_admin_readonly", nil},
		{"agents_read", nil},
		{"agents_write", nil},
		{"account_admin_a", agentPqArrayArg{agentGrantAccountA}},
		{"agents_read_scoped_a", agentPqArrayArg{agentGrantAccountA}},
		{"agents_write_scoped_a", agentPqArrayArg{agentGrantAccountA}},
		{"agents_execute", agentPqArrayArg{}},
		{"tools_read", agentPqArrayArg{}},
		{"no_role", agentPqArrayArg{}},
	} {
		t.Run(tc.caller, func(t *testing.T) {
			mock := agentGrantMockDB(t)
			q := mock.ExpectQuery(`FROM llm_agents lg`)
			if tc.readable == nil {
				q.WithArgs(AgentTypeCustom, agentGrantTenant)
			} else {
				q.WithArgs(AgentTypeCustom, agentGrantTenant, tc.readable, all)
			}
			q.WillReturnRows(sqlmock.NewRows([]string{"id"}))
			ListCustomAgentsForTenant(callers[tc.caller], false)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
