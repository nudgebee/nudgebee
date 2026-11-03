package api

import (
	"context"
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const attributionRealUserId = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func attributionContext(t *testing.T, wire string, superAdmin bool) *security.SecurityContext {
	t.Helper()
	var sc security.SecurityContext
	if err := common.UnmarshalJson([]byte(wire), &sc); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if superAdmin {
		sc.AddRole(security.AUTH_SUPER_ADMIN_FULL_ROLE)
	}
	return &sc
}

// A super admin visiting a tenant they hold no grant in must be recorded as the
// system user (#23879, traceless cross-tenant access); one who also holds a role
// in the tenant keeps their own id, or they lose their chat history and audit
// attribution (#32495).
func TestAttributionUserId(t *testing.T) {
	tests := []struct {
		name string
		sc   func(t *testing.T) *security.SecurityContext
		want string
	}{
		{
			name: "super admin on a foreign tenant is anonymised",
			sc: func(t *testing.T) *security.SecurityContext {
				return attributionContext(t, `{"TenantId":"t1","UserId":"`+attributionRealUserId+`","Roles":[]}`, true)
			},
			want: security.GetSystemUserId(),
		},
		{
			name: "super admin who is a member of this tenant keeps their id",
			sc: func(t *testing.T) *security.SecurityContext {
				return attributionContext(t, `{"TenantId":"t1","UserId":"`+attributionRealUserId+`","Roles":["tenant_admin"]}`, true)
			},
			want: attributionRealUserId,
		},
		{
			name: "ordinary user keeps their id",
			sc: func(t *testing.T) *security.SecurityContext {
				return attributionContext(t, `{"TenantId":"t1","UserId":"`+attributionRealUserId+`","Roles":["account_admin"]}`, false)
			},
			want: attributionRealUserId,
		},
		{
			// buildContextFromPayload builds this when the request carries no
			// user id: the tenant_admin is hard-coded, not granted.
			name: "super admin on a context built without a user id is anonymised",
			sc: func(t *testing.T) *security.SecurityContext {
				sc := security.NewSecurityContextForTenantAccountAdmin("t1", security.GetSystemUserId(), []string{"a1"})
				sc.AddRole(security.AUTH_SUPER_ADMIN_FULL_ROLE)
				return sc
			},
			want: security.GetSystemUserId(),
		},
		{
			name: "synthetic server-internal admin is anonymised",
			sc: func(t *testing.T) *security.SecurityContext {
				return security.NewSecurityContextForSuperAdmin()
			},
			want: security.GetSystemUserId(),
		},
		{
			name: "missing security context leaves the request untouched",
			sc:   func(t *testing.T) *security.SecurityContext { return nil },
			want: attributionRealUserId,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := attributionUserId(tc.sc(t), attributionRealUserId); got != tc.want {
				t.Errorf("attributionUserId() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A super admin's event analysis in a tenant they are not a member of must run
// as the system user: api-server refuses the operator's own id there, so every
// metrics, logs and traces lookup would fail with 403.
func TestAsSystemUserForVisitingSuperAdmin(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgres")}, nil
	})
	t.Cleanup(func() { common.RegisterDatabaseManagerHook(common.Metastore, nil) })
	mock.ExpectQuery("SELECT id::text FROM cloud_accounts WHERE tenant =").WithArgs("t1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("a1"))

	visitor := attributionContext(t, `{"TenantId":"t1","UserId":"`+attributionRealUserId+`","Roles":[]}`, true)
	got, err := asSystemUserForVisitingSuperAdmin(security.NewRequestContext(context.Background(), visitor, nil, nil, nil))
	require.NoError(t, err)
	sc := got.GetSecurityContext()
	assert.Equal(t, security.GetSystemUserId(), sc.GetUserId())
	assert.Empty(t, sc.EffectiveUserIdForRPC(), "api-server must take its tenant-admin branch")
	assert.Equal(t, "t1", sc.GetTenantId())
	assert.True(t, sc.HasAccountAccess("a1", security.SecurityAccessTypeRead))
	assert.False(t, sc.IsSuperAdmin(), "the swapped context must not keep the operator's super admin role")
	require.NoError(t, mock.ExpectationsWereMet())

	for name, sc := range map[string]*security.SecurityContext{
		"super admin who is a member keeps their own context": attributionContext(t, `{"TenantId":"t1","UserId":"`+attributionRealUserId+`","Roles":["tenant_admin"]}`, true),
		"ordinary user keeps their own context":               attributionContext(t, `{"TenantId":"t1","UserId":"`+attributionRealUserId+`","Roles":["account_admin"]}`, false),
	} {
		t.Run(name, func(t *testing.T) {
			ctx := security.NewRequestContext(context.Background(), sc, nil, nil, nil)
			got, err := asSystemUserForVisitingSuperAdmin(ctx)
			require.NoError(t, err)
			assert.Same(t, ctx, got)
		})
	}

	t.Run("missing security context fails closed", func(t *testing.T) {
		got, err := asSystemUserForVisitingSuperAdmin(nil)
		assert.Error(t, err)
		assert.Nil(t, got)
	})
}
