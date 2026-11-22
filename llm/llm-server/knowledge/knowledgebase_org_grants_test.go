package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

const (
	kbGrantAccountB = "aaaaaaaa-0000-0000-0000-00000000000b"
	kbGrantItem     = "kb-org-1"
)

// kbGrantCaller is a person of orgTestTenant (accounts orgTestAccount and
// kbGrantAccountB) with the given built-in roles and custom grants.
func kbGrantCaller(t *testing.T, roles []string, scoped map[string][]string, perms map[string]bool, scopedPerms map[string]map[string]bool) *security.RequestContext {
	t.Helper()
	sc := &security.SecurityContext{}
	raw, err := json.Marshal(map[string]any{
		"TenantId": orgTestTenant, "UserId": "u1", "AccountIds": []string{orgTestAccount, kbGrantAccountB},
		"Roles": roles, "ScopedEntityIds": scoped,
		"CustomPermissions": perms, "ScopedCustomPermissions": scopedPerms,
	})
	require.NoError(t, err)
	require.NoError(t, sc.UnmarshalJSON(raw))
	return security.NewRequestContext(context.Background(), sc, nil, nil, nil)
}

func kbGrantCallers(t *testing.T) map[string]*security.RequestContext {
	return map[string]*security.RequestContext{
		"tenant_admin":          kbGrantCaller(t, []string{security.AUTH_TENANT_ADMIN_ROLE}, nil, nil, nil),
		"tenant_admin_readonly": kbGrantCaller(t, []string{security.AUTH_TENANT_READ_ADMIN_ROLE}, nil, nil, nil),
		"account_admin_a": kbGrantCaller(t, []string{security.AUTH_ACCOUNT_ADMIN_ROLE},
			map[string][]string{security.AUTH_ACCOUNT_ADMIN_ROLE: {orgTestAccount}}, nil, nil),
		"no_role":      kbGrantCaller(t, nil, nil, nil, nil),
		"kbs_read":     kbGrantCaller(t, nil, nil, map[string]bool{"ai_kbs:Read": true}, nil),
		"kbs_write":    kbGrantCaller(t, nil, nil, map[string]bool{"ai_kbs:Write": true}, nil),
		"kbs_execute":  kbGrantCaller(t, nil, nil, map[string]bool{"ai_kbs:Execute": true}, nil),
		"agents_read":  kbGrantCaller(t, nil, nil, map[string]bool{"ai_agents:Read": true}, nil),
		"agents_write": kbGrantCaller(t, nil, nil, map[string]bool{"ai_agents:Write": true}, nil),
		"kbs_read_scoped_a": kbGrantCaller(t, nil, nil, nil,
			map[string]map[string]bool{orgTestAccount: {"ai_kbs:Read": true}}),
		"kbs_write_scoped_a": kbGrantCaller(t, nil, nil, nil,
			map[string]map[string]bool{orgTestAccount: {"ai_kbs:Write": true}}),
	}
}

func kbGrantMockDB(t *testing.T) sqlmock.Sqlmock {
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

type kbGrantCase struct {
	caller string
	allow  bool
}

func assertKBGrant(t *testing.T, err error, allow bool) {
	t.Helper()
	refused := err != nil && (err.Error() == errKBOrgAdminOnly || err.Error() == errKBUnauthorized)
	if allow {
		assert.False(t, refused, "want allowed, got %v", err)
	} else {
		assert.True(t, refused, "want refused, got %v", err)
	}
}

// Organisation knowledge bases: sharing them (visibility, per-account switches)
// is open to a tenant-wide ai_kbs:Write grant as well as tenant admins; listing
// the switches to a tenant-wide ai_kbs Read or Write grant as well as tenant
// readers. Account-scoped grants, Read-for-a-write and the ai_agents grant are
// refused.
func TestOrgKBSharingGrants(t *testing.T) {
	callers := kbGrantCallers(t)

	kbRow := func(tenant string) *sqlmock.Rows {
		return sqlmock.NewRows([]string{"name", "account_id", "tenant_id", "visibility", "kb_type", "status", "synced"}).
			AddRow("runbooks", "", tenant, KBVisibilityOrganisation, "manual", "ready", false)
	}

	t.Run("ai_update_kb_visibility", func(t *testing.T) {
		for _, tc := range []kbGrantCase{
			{"tenant_admin", true},
			{"kbs_write", true},
			{"tenant_admin_readonly", false},
			{"account_admin_a", false},
			{"kbs_read", false},
			{"kbs_execute", false},
			{"kbs_write_scoped_a", false},
			{"agents_write", false},
		} {
			t.Run(tc.caller, func(t *testing.T) {
				mock := kbGrantMockDB(t)
				mock.ExpectQuery(`FROM llm_knowledgebases WHERE id = \$1`).WillReturnRows(kbRow(orgTestTenant))
				_, err := SetKnowledgebaseVisibility(callers[tc.caller], kbGrantItem, KBVisibilitySelectedAccounts, []string{orgTestAccount}, "")
				assertKBGrant(t, err, tc.allow)
			})
		}
		t.Run("kbs_write on another tenant's KB", func(t *testing.T) {
			mock := kbGrantMockDB(t)
			mock.ExpectQuery(`FROM llm_knowledgebases WHERE id = \$1`).WillReturnRows(kbRow(orgTestOther))
			_, err := SetKnowledgebaseVisibility(callers["kbs_write"], kbGrantItem, KBVisibilitySelectedAccounts, []string{orgTestAccount}, "")
			assertKBGrant(t, err, false)
		})
	})

	t.Run("ai_update_kb_account_override", func(t *testing.T) {
		switchRow := func(visibility string) *sqlmock.Rows {
			return sqlmock.NewRows([]string{"name", "tenant_id", "visibility"}).AddRow("runbooks", orgTestTenant, visibility)
		}
		for _, tc := range []kbGrantCase{
			{"tenant_admin", true},
			{"kbs_write", true},
			{"tenant_admin_readonly", false},
			{"account_admin_a", false},
			{"kbs_read", false},
			{"kbs_write_scoped_a", false},
			{"agents_write", false},
		} {
			t.Run(tc.caller+" on a selected-accounts KB", func(t *testing.T) {
				mock := kbGrantMockDB(t)
				mock.ExpectQuery(`FROM llm_knowledgebases kb JOIN cloud_accounts`).WillReturnRows(switchRow(KBVisibilitySelectedAccounts))
				mock.ExpectExec(`INSERT INTO llm_kb_account_overrides`).WillReturnError(errors.New("saved"))
				err := SetKBAccountOverride(callers[tc.caller], orgTestAccount, kbGrantItem, true)
				assertKBGrant(t, err, tc.allow)
			})
		}
		for _, tc := range []kbGrantCase{
			{"account_admin_a", true},
			{"kbs_write", true},
			{"kbs_write_scoped_a", false},
		} {
			t.Run(tc.caller+" on an organisation-wide KB", func(t *testing.T) {
				mock := kbGrantMockDB(t)
				mock.ExpectQuery(`FROM llm_knowledgebases kb JOIN cloud_accounts`).WillReturnRows(switchRow(KBVisibilityOrganisation))
				mock.ExpectExec(`INSERT INTO llm_kb_account_overrides`).WillReturnError(errors.New("saved"))
				err := SetKBAccountOverride(callers[tc.caller], orgTestAccount, kbGrantItem, false)
				assertKBGrant(t, err, tc.allow)
			})
		}
		t.Run("kbs_write for an account outside the tenant", func(t *testing.T) {
			kbGrantMockDB(t)
			err := SetKBAccountOverride(callers["kbs_write"], orgTestOther, kbGrantItem, false)
			assertKBGrant(t, err, false)
		})
	})

	t.Run("ai_list_kb_account_overrides", func(t *testing.T) {
		for _, tc := range []kbGrantCase{
			{"tenant_admin", true},
			{"tenant_admin_readonly", true},
			{"kbs_read", true},
			{"kbs_write", true},
			{"account_admin_a", false},
			{"no_role", false},
			{"kbs_execute", false},
			{"kbs_read_scoped_a", false},
			{"agents_read", false},
		} {
			t.Run(tc.caller, func(t *testing.T) {
				mock := kbGrantMockDB(t)
				mock.ExpectQuery(`SELECT tenant_id::text FROM llm_knowledgebases`).
					WillReturnRows(sqlmock.NewRows([]string{"tenant_id"}).AddRow(orgTestTenant))
				mock.ExpectQuery(`FROM llm_kb_account_overrides`).WillReturnError(errors.New("listed"))
				_, err := ListKBAccountOverrides(callers[tc.caller], kbGrantItem)
				assertKBGrant(t, err, tc.allow)
			})
		}
	})
}
