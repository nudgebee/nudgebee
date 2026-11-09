package security

import "testing"

// A super admin signed into one tenant must not reach another tenant's account
// from there: the handler would store the session tenant next to the foreign
// account. Only synthetic server contexts, which carry no tenant, skip
// the membership check.
func TestHasAccountAccess_SuperAdminStaysInTenant(t *testing.T) {
	const own = "11111111-1111-1111-1111-111111111111"
	const foreign = "22222222-2222-2222-2222-222222222222"

	full := &SecurityContext{tenantId: "t1", accountIds: []string{own}, roles: []string{AUTH_SUPER_ADMIN_FULL_ROLE}}
	if !full.HasAccountAccess(own, SecurityAccessTypeUpdate) {
		t.Error("super admin should write accounts of the tenant they are signed into")
	}
	if full.HasAccountAccess(foreign, SecurityAccessTypeRead) {
		t.Error("super admin must not reach another tenant's account without switching tenant")
	}

	readonly := &SecurityContext{tenantId: "t1", accountIds: []string{own}, roles: []string{AUTH_SUPER_ADMIN_READONLY_ROLE}}
	if !readonly.HasAccountAccess(own, SecurityAccessTypeRead) {
		t.Error("readonly super admin should read accounts of their tenant")
	}
	if readonly.HasAccountAccess(own, SecurityAccessTypeUpdate) {
		t.Error("readonly super admin must not write")
	}
	if readonly.HasAccountAccess(foreign, SecurityAccessTypeRead) {
		t.Error("readonly super admin must not read another tenant's account")
	}

	if !NewSecurityContextForSuperAdmin().HasAccountAccess(foreign, SecurityAccessTypeUpdate) {
		t.Error("server-internal context has no tenant and must keep its bypass")
	}
}
