package tenant

import "testing"

// The escalation these guards close: UpsertTenantAttributes / UpsertFeatureFlags
// are CanManage-gated, so a `tenants:Write` / `featureflags:Write` custom grant
// reaches them. Without the denylist that grant also carried "switch off
// entitlement metering" and "switch off K8s RBAC / dynamic RBAC itself".
func TestPrivilegedTenantAttributes(t *testing.T) {
	for _, name := range []string{"entitlement_bypass", "Entitlement_Bypass", "  entitlement_bypass  "} {
		if !isPrivilegedTenantAttribute(name) {
			t.Errorf("%q must require a tenant admin (case/whitespace must not bypass the check)", name)
		}
	}
	// Ordinary tenant config stays delegable — the denylist must not creep, or
	// the `tenants:Write` persona the feature exists for stops working.
	for _, name := range []string{"log_labels", "trace_labels", "connection_mode", "pod_right_sizing", ""} {
		if isPrivilegedTenantAttribute(name) {
			t.Errorf("%q is ordinary tenant config and must stay writable by a tenants:Write grant", name)
		}
	}
}

func TestPrivilegedFeatureFlags(t *testing.T) {
	// RBAC_K8S gates K8s RBAC enforcement; CUSTOM_ROLES is the switch for the
	// grant mechanism doing the checking, so a grant holder must not reach it.
	for _, id := range []string{FEATURE_RBACK_K8S_ACCESS, "CUSTOM_ROLES", "custom_roles", " rbac_k8s "} {
		if !isPrivilegedFeatureFlag(id) {
			t.Errorf("%q must require a tenant admin", id)
		}
	}
	for _, id := range []string{FEATURE_ANOMALY_DETECTION, FEATURE_VERTICAL_RIGHTSIZING, ""} {
		if isPrivilegedFeatureFlag(id) {
			t.Errorf("%q is a product flag and must stay toggleable by a featureflags:Write grant", id)
		}
	}
}
