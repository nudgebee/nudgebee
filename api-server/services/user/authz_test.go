package user

import (
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/security"
)

// contextFor builds a SecurityContext from the wire shape the gateway/session
// produce, so these cases read like a real signed-in user.
func contextFor(t *testing.T, wire string) *security.SecurityContext {
	t.Helper()
	var sc security.SecurityContext
	if err := common.UnmarshalJson([]byte(wire), &sc); err != nil {
		t.Fatalf("unmarshal security context: %v", err)
	}
	return &sc
}

// The union a user expects from "built-in role + custom role": the built-in role
// keeps everything it had, and the grant adds user administration on top —
// without adding the privilege administration that would let it escalate.
func TestUserAdministrationUnion(t *testing.T) {
	cases := []struct {
		name             string
		wire             string
		canAdminister    bool
		mayAssignTenants bool
	}{
		{
			// The reported session: read-only tenant admin holding users:Read+Write.
			name:             "tenant_admin_readonly plus users:Write grant",
			wire:             `{"TenantId":"t1","UserId":"u1","Roles":["tenant_admin_readonly"],"CustomPermissions":{"users:Read":true,"users:Write":true}}`,
			canAdminister:    true,
			mayAssignTenants: false,
		},
		{
			name:             "tenant_admin_readonly alone is unchanged by the feature",
			wire:             `{"TenantId":"t1","UserId":"u1","Roles":["tenant_admin_readonly"]}`,
			canAdminister:    false,
			mayAssignTenants: false,
		},
		{
			name:             "tenant_admin keeps both, grant or no grant",
			wire:             `{"TenantId":"t1","UserId":"u1","Roles":["tenant_admin"]}`,
			canAdminister:    true,
			mayAssignTenants: true,
		},
		{
			// A pure custom-role user: user administration, never privilege.
			name:             "users:Write with no built-in role",
			wire:             `{"TenantId":"t1","UserId":"u1","Roles":[],"CustomPermissions":{"users:Write":true}}`,
			canAdminister:    true,
			mayAssignTenants: false,
		},
		{
			name:             "users:Read does not confer write",
			wire:             `{"TenantId":"t1","UserId":"u1","Roles":["tenant_admin_readonly"],"CustomPermissions":{"users:Read":true}}`,
			canAdminister:    false,
			mayAssignTenants: false,
		},
		{
			name:             "an unrelated grant confers nothing here",
			wire:             `{"TenantId":"t1","UserId":"u1","Roles":["account_admin"],"CustomPermissions":{"events:Write":true}}`,
			canAdminister:    false,
			mayAssignTenants: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := contextFor(t, tc.wire)
			if got := canAdministerUsers(sc); got != tc.canAdminister {
				t.Errorf("canAdministerUsers = %v, want %v", got, tc.canAdminister)
			}
			if got := mayAssignTenantRole(sc); got != tc.mayAssignTenants {
				t.Errorf("mayAssignTenantRole = %v, want %v", got, tc.mayAssignTenants)
			}
		})
	}
}

// The role guard compares against the user's CURRENT role. Guarding on whether
// the field was sent instead would refuse every profile edit, because the user
// modal posts `role` on every save.
func TestChangesTenantRole(t *testing.T) {
	cases := []struct {
		name      string
		current   []string
		requested string
		want      bool
	}{
		{"same role resubmitted is not a change", []string{"tenant_admin_readonly"}, "tenant_admin_readonly", false},
		{"promotion is a change", []string{"tenant_admin_readonly"}, "tenant_admin", true},
		{"granting a role to a role-less user is a change", nil, "tenant_admin", true},
		{"empty against no role is not a change", nil, "", false},
		{"empty against an existing role is a removal", []string{"tenant_admin"}, "", true},
		{"collapsing two roles into one is a change", []string{"tenant_admin", "tenant_admin_readonly"}, "tenant_admin", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tu := tenantUser{id: "u1", roles: tc.current}
			if got := tu.changesTenantRole(tc.requested); got != tc.want {
				t.Errorf("changesTenantRole(%q) with current %v = %v, want %v", tc.requested, tc.current, got, tc.want)
			}
		})
	}
}
