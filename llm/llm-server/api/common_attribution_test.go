package api

import (
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
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
