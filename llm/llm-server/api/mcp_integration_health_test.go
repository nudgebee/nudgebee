package api

import "testing"

// #40994 criterion 11: Check now is integrations:Execute and tenant-wide only.
func TestCanCheckMCPIntegration(t *testing.T) {
	cases := []struct {
		name string
		wire string
		want bool
	}{
		{"tenant admin", `{"TenantId":"t1","UserId":"u1","Roles":["tenant_admin"]}`, true},
		{"super admin", `{"TenantId":"t1","UserId":"u1","Roles":["super_admin"]}`, true},
		{"tenant-global Execute grant", `{"TenantId":"t1","UserId":"u1","Roles":[],"CustomPermissions":{"integrations:Execute":true}}`, true},
		{"Read grant only", `{"TenantId":"t1","UserId":"u1","Roles":[],"CustomPermissions":{"integrations:Read":true}}`, false},
		// Execute is matched exactly at the gateway; Write does not reach the action.
		{"Write grant only", `{"TenantId":"t1","UserId":"u1","Roles":[],"CustomPermissions":{"integrations:Write":true}}`, false},
		{"account-scoped Execute grant", `{"TenantId":"t1","UserId":"u1","Roles":[],"AccountIds":["` + authzAcctA + `"],
			"ScopedCustomPermissions":{"` + authzAcctA + `":{"integrations:Execute":true}}}`, false},
		{"account admin", `{"TenantId":"t1","UserId":"u1","Roles":["account_admin"],"AccountIds":["` + authzAcctA + `"]}`, false},
		{"read-only tenant admin", `{"TenantId":"t1","UserId":"u1","Roles":["tenant_admin_readonly"]}`, false},
		{"another module's Execute grant", `{"TenantId":"t1","UserId":"u1","Roles":[],"CustomPermissions":{"ai_tools:Execute":true}}`, false},
	}
	if canCheckMCPIntegration(nil) {
		t.Error("no security context must be refused")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := canCheckMCPIntegration(authzContext(t, c.wire)); got != c.want {
				t.Errorf("canCheckMCPIntegration = %v, want %v", got, c.want)
			}
		})
	}
}
