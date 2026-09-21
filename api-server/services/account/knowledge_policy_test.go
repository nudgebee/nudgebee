package account

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"nudgebee/services/security"
	"testing"
)

func TestKnowledgePolicyAttribute(t *testing.T) {
	admin := security.NewSecurityContextForSuperAdmin()
	denied := security.NewSecurityContextForAuditRelay("tenant", "user")
	for _, value := range []string{"auto", "always", "llm_only", "disabled"} {
		attr := AccountAttr{CloudAccountId: "account", Name: "knowledge_policy", Value: value}
		require.NoError(t, validateKnowledgePolicyAttribute(admin, attr))
		require.Error(t, validateKnowledgePolicyAttribute(denied, attr))
	}
	for _, value := range []string{"", "unknown", "AUTO", " auto "} {
		require.ErrorContains(t, validateKnowledgePolicyAttribute(admin, AccountAttr{CloudAccountId: "account", Name: "knowledge_policy", Value: value}), "invalid knowledge_policy")
	}
	require.NoError(t, validateKnowledgePolicyAttribute(denied, AccountAttr{Name: "unrelated", Value: "anything"}))
}

func TestKnowledgePolicyAccountAdminScope(t *testing.T) {
	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(`{"TenantId":"tenant", "AccountIds":["a","b","c"], "Roles":["account_admin","k8s_namespace_admin","account_admin_readonly"], "ScopedEntityIds":{"account_admin":["a"],"k8s_namespace_admin":["b"],"account_admin_readonly":["c"]}}`), &sc))
	require.NoError(t, validateKnowledgePolicyAttribute(&sc, AccountAttr{CloudAccountId: "a", Name: "knowledge_policy", Value: "disabled"}))
	for _, id := range []string{"b", "c", "outside"} {
		require.Error(t, validateKnowledgePolicyAttribute(&sc, AccountAttr{CloudAccountId: id, Name: "knowledge_policy", Value: "disabled"}))
	}
}
