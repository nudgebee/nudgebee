package account

import (
	"bytes"
	"encoding/json"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/security"
)

// nudgebeeWorkloadsAttr is the cloud_account_attrs key for the account's
// "NudgeBee's own workloads" setting; notification.NudgebeeWorkloadsAttr reads
// it and must change with it.
const nudgebeeWorkloadsAttr = "nudgebee_workloads"

type nudgebeeWorkloadsSettings struct {
	HideInNotifications *bool    `json:"hide_in_notifications"`
	Namespaces          []string `json:"namespaces"`
}

// validateNudgebeeWorkloadsAttribute rejects a malformed nudgebee_workloads
// value at write time. The digest queries cast it to jsonb, so a value that is
// not a JSON object of the expected shape would fail the tenant's digests.
func validateNudgebeeWorkloadsAttribute(sc *security.SecurityContext, attr AccountAttr) error {
	if attr.Name != nudgebeeWorkloadsAttr {
		return nil
	}
	if sc == nil || !sc.HasAccountAccess(attr.CloudAccountId, security.SecurityAccessTypeUpdate) ||
		(!sc.IsSuperAdmin() && !sc.IsTenantAdmin() && !sc.HasScopedRole(security.AUTH_ACCOUNT_ADMIN_ROLE, attr.CloudAccountId)) {
		return common.ErrorUnauthorized("Not Allowed")
	}
	// json.Valid rejects anything after the first value, which Decode would
	// silently accept and the jsonb cast would not.
	if !json.Valid([]byte(attr.Value)) {
		return fmt.Errorf("invalid nudgebee_workloads: not a single JSON value")
	}
	var settings nudgebeeWorkloadsSettings
	decoder := json.NewDecoder(bytes.NewReader([]byte(attr.Value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		return fmt.Errorf("invalid nudgebee_workloads: %w", err)
	}
	if len(settings.Namespaces) > maxEventAnalysisNamespaces {
		return fmt.Errorf("invalid nudgebee_workloads: namespaces allows at most %d namespaces", maxEventAnalysisNamespaces)
	}
	for _, ns := range settings.Namespaces {
		if len(ns) > 63 || !k8sNamespacePattern.MatchString(ns) {
			return fmt.Errorf("invalid nudgebee_workloads: %q is not a valid Kubernetes namespace", ns)
		}
	}
	return nil
}
