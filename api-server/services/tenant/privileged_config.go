package tenant

import "strings"

// Tenant config is delegable, privilege is not.
//
// UpsertTenantAttributes and UpsertFeatureFlags are gated by CanManage, so a
// dynamic-RBAC `tenants:Write` grant reaches both of them — that
// is the point: a tenant admin can hand tenant configuration to someone who is
// not an admin. But a handful of the keys those two handlers write are not
// configuration at all; they govern billing or authorization. Delegating "set
// tenant attributes" must not implicitly delegate "turn off access control" or
// "stop being billed".
//
// These are DENYLISTS, not allowlists, and deliberately so: the writable key
// space is open-ended (integration config, observability label maps, per-tenant
// product toggles), so an allowlist of safe keys would be a guess that silently
// breaks the delegated persona the feature exists for. The privileged keys, by
// contrast, are enumerable and each entry below is a key some code path reads to
// make a security or billing decision. Fail-open for unlisted keys therefore
// preserves today's behavior exactly; the escalation paths are what close.
//
// Adding a key: if a new attribute or flag is read anywhere that decides
// authorization, entitlement, or authentication, add it here in the same change.

// privilegedTenantAttributes are tenant_attrs keys that only a tenant admin (or
// super admin) may write, whatever custom grants the caller holds.
//
//	entitlement_bypass — read by entitlement/service.go:375, where value "true"
//	  switches off entitlement metering for the whole tenant. A billing control.
var privilegedTenantAttributes = map[string]bool{
	"entitlement_bypass": true,
}

// privilegedFeatureFlags are feature ids that only a tenant admin (or super
// admin) may toggle.
//
//	RBAC_K8S      — gates K8s RBAC enforcement (tenant/authz.go, and the
//	                namespace filters in query/metadata.go). Disabling it removes
//	                per-namespace access control tenant-wide.
//	CUSTOM_ROLES  — the on/off switch for dynamic RBAC itself
//	                (security.CustomRolesEnabled). A grant holder able to toggle
//	                this could disable the mechanism that is checking them.
var privilegedFeatureFlags = map[string]bool{
	FEATURE_RBACK_K8S_ACCESS: true,
	"CUSTOM_ROLES":           true,
}

// isPrivilegedTenantAttribute reports whether writing `name` requires a real
// tenant admin. Matching is case-insensitive because tenant_attrs.name is not a
// citext column, so `Entitlement_Bypass` would otherwise slip past the check and
// still be found by the reader's exact-match lookup on some collations.
func isPrivilegedTenantAttribute(name string) bool {
	return privilegedTenantAttributes[strings.ToLower(strings.TrimSpace(name))]
}

// isPrivilegedFeatureFlag reports whether toggling `featureID` requires a real
// tenant admin. Feature ids are upper-case by convention; normalized for the
// same reason as above.
func isPrivilegedFeatureFlag(featureID string) bool {
	return privilegedFeatureFlags[strings.ToUpper(strings.TrimSpace(featureID))]
}
