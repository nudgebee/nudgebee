package security

import (
	"context"
	"strings"
	"testing"
)

func grantCtx(sc *SecurityContext, permission string) *RequestContext {
	rc := NewRequestContext(context.Background(), sc, nil, nil, nil)
	rc.SetActionPermission(permission)
	return rc
}

func TestCanAccessAccount(t *testing.T) {
	own, other, foreign := "acc-own", "acc-other", "acc-foreign"
	tenantGrant := func(keys ...string) *SecurityContext {
		perms := map[string]bool{}
		for _, k := range keys {
			perms[k] = true
		}
		return &SecurityContext{tenantId: "t1", accountIds: []string{own, other}, customPermissions: perms}
	}
	scopedGrant := func(account string, keys ...string) *SecurityContext {
		perms := map[string]bool{}
		for _, k := range keys {
			perms[k] = true
		}
		return &SecurityContext{tenantId: "t1", accountIds: []string{own, other}, scopedCustomPermissions: map[string]map[string]bool{account: perms}}
	}

	cases := []struct {
		name       string
		sc         *SecurityContext
		permission string
		account    string
		access     SecurityAccessType
		want       bool
	}{
		{"built-in account admin, no action permission", &SecurityContext{tenantId: "t1", accountIds: []string{own}, roles: []string{AUTH_TENANT_ADMIN_ROLE}}, "", own, SecurityAccessTypeUpdate, true},
		{"tenant-wide grant matches action", tenantGrant("slo:Write"), "slo:Write", own, SecurityAccessTypeUpdate, true},
		{"tenant-wide grant, other module", tenantGrant("events:Write"), "slo:Write", own, SecurityAccessTypeUpdate, false},
		{"tenant-wide grant never reaches another tenant's account", tenantGrant("slo:Write"), "slo:Write", foreign, SecurityAccessTypeUpdate, false},
		{"grant without action permission (not via gateway)", tenantGrant("slo:Write"), "", own, SecurityAccessTypeUpdate, false},
		{"write grant satisfies read action", tenantGrant("slo:Write"), "slo:Read", own, SecurityAccessTypeRead, true},
		{"read grant does not satisfy write action", tenantGrant("slo:Read"), "slo:Write", own, SecurityAccessTypeUpdate, false},
		{"read-classified action cannot authorize a mutation", tenantGrant("slo:Read"), "slo:Read", own, SecurityAccessTypeDelete, false},
		{"execute grant for execute action", tenantGrant("ml:Execute"), "ml:Execute", own, SecurityAccessTypeUpdate, true},
		{"write grant does not satisfy execute action", tenantGrant("ml:Write"), "ml:Execute", own, SecurityAccessTypeUpdate, false},
		{"account-bound grant on its account", scopedGrant(own, "slo:Write"), "slo:Write", own, SecurityAccessTypeUpdate, true},
		{"account-bound grant on another account", scopedGrant(own, "slo:Write"), "slo:Write", other, SecurityAccessTypeUpdate, false},
		{"account-bound write grant satisfies read", scopedGrant(own, "slo:Write"), "slo:Read", own, SecurityAccessTypeRead, true},
		{"malformed action permission", tenantGrant("slo:Write"), "slo", own, SecurityAccessTypeUpdate, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := grantCtx(tc.sc, tc.permission).GetSecurityContext().CanAccessAccount(tc.account, tc.access); got != tc.want {
				t.Fatalf("CanAccessAccount = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCanAccessTenant(t *testing.T) {
	cases := []struct {
		name       string
		sc         *SecurityContext
		permission string
		access     SecurityAccessType
		want       bool
	}{
		{"tenant admin", &SecurityContext{tenantId: "t1", roles: []string{AUTH_TENANT_ADMIN_ROLE}}, "", SecurityAccessTypeUpdate, true},
		{"tenant read admin reads", &SecurityContext{tenantId: "t1", roles: []string{AUTH_TENANT_READ_ADMIN_ROLE}}, "", SecurityAccessTypeRead, true},
		{"tenant read admin cannot write", &SecurityContext{tenantId: "t1", roles: []string{AUTH_TENANT_READ_ADMIN_ROLE}}, "", SecurityAccessTypeUpdate, false},
		{"tenant-wide grant matches action", &SecurityContext{tenantId: "t1", customPermissions: map[string]bool{"ownership:Write": true}}, "ownership:Write", SecurityAccessTypeUpdate, true},
		{"write grant satisfies read action", &SecurityContext{tenantId: "t1", customPermissions: map[string]bool{"ownership:Write": true}}, "ownership:Read", SecurityAccessTypeRead, true},
		{"read grant cannot write", &SecurityContext{tenantId: "t1", customPermissions: map[string]bool{"ownership:Read": true}}, "ownership:Write", SecurityAccessTypeUpdate, false},
		{"account-bound grant never satisfies a tenant check", &SecurityContext{tenantId: "t1", accountIds: []string{"a1"}, scopedCustomPermissions: map[string]map[string]bool{"a1": {"ownership:Write": true}}}, "ownership:Write", SecurityAccessTypeUpdate, false},
		{"no action permission", &SecurityContext{tenantId: "t1", customPermissions: map[string]bool{"ownership:Write": true}}, "", SecurityAccessTypeUpdate, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := grantCtx(tc.sc, tc.permission).GetSecurityContext().CanAccessTenant(tc.access); got != tc.want {
				t.Fatalf("CanAccessTenant = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGrantChecksFailClosedOnNilRequestContext(t *testing.T) {
	var rc *RequestContext
	if rc.GetSecurityContext().CanAccessAccount("a1", SecurityAccessTypeRead) {
		t.Fatal("CanAccessAccount on nil RequestContext = true, want false")
	}
	if rc.GetSecurityContext().CanAccessTenant(SecurityAccessTypeRead) {
		t.Fatal("CanAccessTenant on nil RequestContext = true, want false")
	}
}

// The checks live on SecurityContext and read the action key it carries; the
// RequestContext only hands the key over.
func TestSecurityContextGrantChecksUseItsActionKey(t *testing.T) {
	sc := &SecurityContext{tenantId: "t1", accountIds: []string{"a1"}, customPermissions: map[string]bool{"cloud:Execute": true}}
	if sc.CanAccessAccount("a1", SecurityAccessTypeUpdate) {
		t.Fatal("no action key means built-in roles only")
	}
	sc.SetActionPermission("cloud:Execute")
	if !sc.CanAccessAccount("a1", SecurityAccessTypeUpdate) {
		t.Fatal("cloud:Execute grant should pass for its own action key")
	}
	sc.SetActionPermission("accounts:Write")
	if sc.CanAccessAccount("a1", SecurityAccessTypeUpdate) {
		t.Fatal("a grant for another module must not pass")
	}
	if !sc.CanAccessTenantForAction(SecurityAccessTypeUpdate, "cloud_execute_command") {
		t.Fatal("another action's key, by name")
	}
	raw, err := sc.MarshalJSON()
	if err != nil || strings.Contains(string(raw), "accounts:Write") {
		t.Fatalf("the action key must not be serialized: %s %v", raw, err)
	}
	var missing *SecurityContext
	missing.SetActionPermission("cloud:Read")
	if missing.CanAccessAccount("a1", SecurityAccessTypeRead) || missing.CanAccessTenant(SecurityAccessTypeRead) || missing.CanAccessTenantForAction(SecurityAccessTypeRead, "cloud_execute_command") {
		t.Fatal("a nil security context must fail closed")
	}
}

// A missing security context fails closed through every read check a handler
// reaches it by (GetSecurityContext on a nil RequestContext is nil).
func TestReadChecksFailClosedWithoutASecurityContext(t *testing.T) {
	var rc *RequestContext
	if rc.GetSecurityContext() != nil {
		t.Fatal("nil RequestContext must yield a nil SecurityContext")
	}
	var sc *SecurityContext
	if sc.CanReadAccountData("a1", "events") || sc.CanReadAccountOrTenantUsage("a1") {
		t.Fatal("a nil security context must fail closed")
	}
}
