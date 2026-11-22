package security

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func grantsContext(t *testing.T, wire string) *SecurityContext {
	t.Helper()
	var sc SecurityContext
	require.NoError(t, json.Unmarshal([]byte(wire), &sc))
	return &sc
}

// withKey sets the action key a check reads from the context.
func withKey(sc *SecurityContext, key string) *SecurityContext {
	sc.SetActionPermission(key)
	return sc
}

func TestCanAccessAccountHonoursTheActionsGrant(t *testing.T) {
	const acct, other = "acct-a", "acct-other-tenant"
	tenantWide := grantsContext(t, `{"TenantId":"t","UserId":"u","AccountIds":["acct-a"],"Roles":[],"CustomPermissions":{"workflows:Write":true}}`)
	scoped := grantsContext(t, `{"TenantId":"t","UserId":"u","AccountIds":["acct-a"],"Roles":[],"ScopedCustomPermissions":{"acct-a":{"workflows:Read":true}}}`)
	admin := grantsContext(t, `{"TenantId":"t","UserId":"u","AccountIds":["acct-a"],"Roles":["tenant_admin"]}`)

	assert.True(t, withKey(tenantWide, "workflows:Write").CanAccessAccount(acct, SecurityAccessTypeUpdate))
	assert.True(t, withKey(tenantWide, "workflows:Read").CanAccessAccount(acct, SecurityAccessTypeRead), "Write implies Read")
	assert.False(t, withKey(tenantWide, "workflows:Read").CanAccessAccount(other, SecurityAccessTypeRead), "never another tenant's account")
	assert.False(t, withKey(tenantWide, "").CanAccessAccount(acct, SecurityAccessTypeUpdate), "no key: built-in roles only")
	assert.False(t, withKey(tenantWide, "autooptimize:Write").CanAccessAccount(acct, SecurityAccessTypeUpdate), "another module's grant")

	assert.True(t, withKey(scoped, "workflows:Read").CanAccessAccount(acct, SecurityAccessTypeRead))
	assert.False(t, withKey(scoped, "workflows:Read").CanAccessAccount(acct, SecurityAccessTypeUpdate), "a Read key never authorizes a write")

	assert.True(t, withKey(admin, "").CanAccessAccount(acct, SecurityAccessTypeUpdate), "built-in roles unchanged")
	assert.True(t, withKey(tenantWide, "workflows:Write").CanAccessTenant(SecurityAccessTypeUpdate))
	assert.False(t, withKey(scoped, "workflows:Read").CanAccessTenant(SecurityAccessTypeRead), "an account-bound grant is not tenant-wide")

	var nilSC *SecurityContext
	assert.False(t, withKey(nilSC, "workflows:Read").CanAccessAccount(acct, SecurityAccessTypeRead))
}

func TestCanReadAccountUsesTheModulesReadGrant(t *testing.T) {
	readOnly := grantsContext(t, `{"TenantId":"t","UserId":"u","AccountIds":["acct-a"],"Roles":[],"ScopedCustomPermissions":{"acct-a":{"workflows:Read":true}}}`)
	other := grantsContext(t, `{"TenantId":"t","UserId":"u","AccountIds":["acct-a"],"Roles":[],"CustomPermissions":{"autooptimize:Write":true}}`)
	reader := grantsContext(t, `{"TenantId":"t","UserId":"u","AccountIds":["acct-a"],"Roles":["account_admin_readonly"],"ScopedEntityIds":{"account_admin_readonly":["acct-a"]}}`)
	rc := func(sc *SecurityContext, key string) *RequestContext {
		r := NewRequestContext(context.Background(), sc, slog.Default(), nil, nil)
		r.SetActionPermission(key)
		return r
	}

	assert.True(t, rc(readOnly, "workflows:Write").GetSecurityContext().CanReadAccount("acct-a"), "a Write action reads with the module's Read grant")
	assert.False(t, rc(readOnly, "workflows:Write").GetSecurityContext().CanReadAccount("acct-b"), "only where the grant is bound")
	assert.False(t, rc(other, "workflows:Write").GetSecurityContext().CanReadAccount("acct-a"), "another module's grant")
	assert.False(t, rc(readOnly, "").GetSecurityContext().CanReadAccount("acct-a"), "no key: built-in roles only")
	assert.True(t, rc(reader, "").GetSecurityContext().CanReadAccount("acct-a"), "built-in roles unchanged")
	var nilRC *RequestContext
	assert.False(t, nilRC.GetSecurityContext().CanReadAccount("acct-a"), "a nil RequestContext fails closed")
}
