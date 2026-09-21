package account

import (
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/security"
)

// Reuse the existing account attribute action while validating the runtime enum.
// Other attributes retain their existing contract.
func validateKnowledgePolicyAttribute(sc *security.SecurityContext, attr AccountAttr) error {
	if attr.Name != "knowledge_policy" {
		return nil
	}
	if !sc.HasAccountAccess(attr.CloudAccountId, security.SecurityAccessTypeUpdate) ||
		(!sc.IsSuperAdmin() && !sc.IsTenantAdmin() && !sc.HasScopedRole(security.AUTH_ACCOUNT_ADMIN_ROLE, attr.CloudAccountId)) {
		return common.ErrorUnauthorized("Not Allowed")
	}
	switch attr.Value {
	case "auto", "always", "llm_only", "disabled":
		return nil
	default:
		return fmt.Errorf("invalid knowledge_policy: expected auto, always, llm_only, or disabled")
	}
}
