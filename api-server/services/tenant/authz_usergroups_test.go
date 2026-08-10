package tenant

import (
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/security"
)

// contextFor builds a SecurityContext from the wire shape the gateway/session
// produce, so these cases read like a real signed-in user. Mirrors the helper in
// services/user/authz_test.go.
func contextFor(t *testing.T, wire string) *security.SecurityContext {
	t.Helper()
	var sc security.SecurityContext
	if err := common.UnmarshalJson([]byte(wire), &sc); err != nil {
		t.Fatalf("unmarshal security context: %v", err)
	}
	return &sc
}

// The delegation these guards express: a `usergroups:Write` grant may administer
// groups, but must not become a way to mint an administrator. Group membership is
// inheritance — every member picks up the group's tenant role and custom roles —
// so a grant holder joining a privileged group would elevate themselves. The
// split mirrors canAdministerUsers / mayAssignTenantRole in services/user/authz.go.
func TestUserGroupAdministrationUnion(t *testing.T) {
	cases := []struct {
		name             string
		wire             string
		canAdminister    bool
		mayChangePrivGrp bool
	}{
		{
			// The reported session: group read+write, which used to 401 on every
			// create/update because the handlers were IsTenantAdmin-only.
			name:             "usergroups:Write grant with no built-in role",
			wire:             `{"TenantId":"t1","UserId":"u1","Roles":[],"CustomPermissions":{"usergroups:Read":true,"usergroups:Write":true}}`,
			canAdminister:    true,
			mayChangePrivGrp: false,
		},
		{
			name:             "usergroups:Read does not confer write",
			wire:             `{"TenantId":"t1","UserId":"u1","Roles":["tenant_admin_readonly"],"CustomPermissions":{"usergroups:Read":true}}`,
			canAdminister:    false,
			mayChangePrivGrp: false,
		},
		{
			name:             "tenant_admin keeps both, grant or no grant",
			wire:             `{"TenantId":"t1","UserId":"u1","Roles":["tenant_admin"]}`,
			canAdminister:    true,
			mayChangePrivGrp: true,
		},
		{
			name:             "tenant_admin_readonly alone is unchanged by the feature",
			wire:             `{"TenantId":"t1","UserId":"u1","Roles":["tenant_admin_readonly"]}`,
			canAdminister:    false,
			mayChangePrivGrp: false,
		},
		{
			name:             "an unrelated grant confers nothing here",
			wire:             `{"TenantId":"t1","UserId":"u1","Roles":["account_admin"],"CustomPermissions":{"events:Write":true}}`,
			canAdminister:    false,
			mayChangePrivGrp: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := contextFor(t, tc.wire)
			if got := canAdministerUserGroups(sc); got != tc.canAdminister {
				t.Errorf("canAdministerUserGroups = %v, want %v", got, tc.canAdminister)
			}
			// The escalation half: holding usergroups:Write must NOT clear the
			// privileged-membership gate, or the carve-out is decorative.
			if got := mayChangePrivilegedGroupMembership(sc); got != tc.mayChangePrivGrp {
				t.Errorf("mayChangePrivilegedGroupMembership = %v, want %v", got, tc.mayChangePrivGrp)
			}
		})
	}
}
