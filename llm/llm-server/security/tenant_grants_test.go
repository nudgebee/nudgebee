package security

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tenantGrantContext(t *testing.T, wire map[string]any) *SecurityContext {
	t.Helper()
	wire["TenantId"] = "t1"
	wire["AccountIds"] = []string{"acct-a", "acct-b"}
	raw, err := json.Marshal(wire)
	require.NoError(t, err)
	sc := &SecurityContext{}
	require.NoError(t, sc.UnmarshalJSON(raw))
	return sc
}

// HasTenantWideGrant counts tenant-wide grants only; CanReadTenantModule adds
// the built-in tenant readers and lets Write imply Read.
func TestTenantWideGrants(t *testing.T) {
	tests := []struct {
		name      string
		wire      map[string]any
		wantWrite bool // HasTenantWideGrant("ai_budget", "Write")
		wantRead  bool // CanReadTenantModule("ai_budget")
	}{
		{"tenant admin", map[string]any{"Roles": []string{AUTH_TENANT_ADMIN_ROLE}}, false, true},
		{"read-only tenant admin", map[string]any{"Roles": []string{AUTH_TENANT_READ_ADMIN_ROLE}}, false, true},
		{"read-only super admin", map[string]any{"Roles": []string{AUTH_SUPER_ADMIN_READONLY_ROLE}}, false, true},
		{"account admin", map[string]any{"Roles": []string{AUTH_ACCOUNT_ADMIN_ROLE}, "ScopedEntityIds": map[string][]string{AUTH_ACCOUNT_ADMIN_ROLE: {"acct-a"}}}, false, false},
		{"tenant-wide Read", map[string]any{"CustomPermissions": map[string]bool{"ai_budget:Read": true}}, false, true},
		{"tenant-wide Write", map[string]any{"CustomPermissions": map[string]bool{"ai_budget:Write": true}}, true, true},
		{"tenant-wide Execute", map[string]any{"CustomPermissions": map[string]bool{"ai_budget:Execute": true}}, false, false},
		{"account-scoped Write", map[string]any{"ScopedCustomPermissions": map[string]map[string]bool{"acct-a": {"ai_budget:Write": true}}}, false, false},
		{"another module's Write", map[string]any{"CustomPermissions": map[string]bool{"ai_models:Write": true}}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := tenantGrantContext(t, tt.wire)
			assert.Equal(t, tt.wantWrite, sc.HasTenantWideGrant("ai_budget", "Write"))
			assert.Equal(t, tt.wantRead, sc.CanReadTenantModule("ai_budget"))
		})
	}
	var nilSC *SecurityContext
	assert.False(t, nilSC.HasTenantWideGrant("ai_budget", "Write"))
	assert.False(t, nilSC.CanReadTenantModule("ai_budget"))
}
