package security

import (
	"testing"

	"nudgebee/llm/common"
)

// scFromWire builds a context the way loadSecurityContextFromServicesServer
// does: api-server resolves roles with `user_roles WHERE tenant_id = $1`, so a
// global super-admin grant (tenant_id IS NULL) is never in the payload. The
// caller then adds it from the session's allowed_roles, which is what AddRole
// stands in for here.
func scFromWire(t *testing.T, wire string) *SecurityContext {
	t.Helper()
	var sc SecurityContext
	if err := common.UnmarshalJson([]byte(wire), &sc); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	return &sc
}

func TestHasExplicitTenantRole(t *testing.T) {
	const acct = "11111111-1111-1111-1111-111111111111"

	tests := []struct {
		name  string
		build func(t *testing.T) *SecurityContext
		want  bool
	}{
		{
			name: "super admin with no grant in this tenant",
			build: func(t *testing.T) *SecurityContext {
				sc := scFromWire(t, `{"TenantId":"t1","UserId":"u1","Roles":[]}`)
				sc.AddRole(AUTH_SUPER_ADMIN_FULL_ROLE)
				return sc
			},
			want: false,
		},
		{
			name: "readonly super admin with no grant in this tenant",
			build: func(t *testing.T) *SecurityContext {
				sc := scFromWire(t, `{"TenantId":"t1","UserId":"u1","Roles":[]}`)
				sc.AddRole(AUTH_SUPER_ADMIN_READONLY_ROLE)
				return sc
			},
			want: false,
		},
		{
			name: "super admin who is also a tenant admin here",
			build: func(t *testing.T) *SecurityContext {
				sc := scFromWire(t, `{"TenantId":"t1","UserId":"u1","Roles":["tenant_admin"]}`)
				sc.AddRole(AUTH_SUPER_ADMIN_FULL_ROLE)
				return sc
			},
			want: true,
		},
		{
			name: "super admin with an account-scoped role here",
			build: func(t *testing.T) *SecurityContext {
				sc := scFromWire(t, `{"TenantId":"t1","UserId":"u1","Roles":["account_admin"],
					"ScopedEntityIds":{"account_admin":["`+acct+`"]}}`)
				sc.AddRole(AUTH_SUPER_ADMIN_FULL_ROLE)
				return sc
			},
			want: true,
		},
		{
			name: "super admin holding only a custom-role grant here",
			build: func(t *testing.T) *SecurityContext {
				sc := scFromWire(t, `{"TenantId":"t1","UserId":"u1","Roles":[],
					"CustomPermissions":{"notifications:Write":true}}`)
				sc.AddRole(AUTH_SUPER_ADMIN_FULL_ROLE)
				return sc
			},
			want: true,
		},
		{
			name: "super admin holding only an account-scoped custom grant here",
			build: func(t *testing.T) *SecurityContext {
				sc := scFromWire(t, `{"TenantId":"t1","UserId":"u1","Roles":[],
					"ScopedCustomPermissions":{"`+acct+`":{"ai_conversations:Read":true}}}`)
				sc.AddRole(AUTH_SUPER_ADMIN_FULL_ROLE)
				return sc
			},
			want: true,
		},
		{
			name: "synthetic server-internal admin",
			build: func(t *testing.T) *SecurityContext {
				return NewSecurityContextForSuperAdmin()
			},
			want: false,
		},
		{
			name: "ordinary user with a role here",
			build: func(t *testing.T) *SecurityContext {
				return scFromWire(t, `{"TenantId":"t1","UserId":"u1","Roles":["tenant_admin_readonly"]}`)
			},
			want: true,
		},
		{
			name: "user with no grant anywhere",
			build: func(t *testing.T) *SecurityContext {
				return scFromWire(t, `{"TenantId":"t1","UserId":"u1","Roles":[]}`)
			},
			want: false,
		},
		{
			// Fails closed rather than panicking, for a caller that forgets to guard.
			name:  "nil context",
			build: func(t *testing.T) *SecurityContext { return nil },
			want:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.build(t).HasExplicitTenantRole(); got != tc.want {
				t.Errorf("HasExplicitTenantRole() = %v, want %v", got, tc.want)
			}
		})
	}
}
