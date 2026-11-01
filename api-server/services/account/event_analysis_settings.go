package account

import (
	"bytes"
	"encoding/json"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/security"
	"regexp"
)

// eventAnalysisSettingsAttr is the cloud_account_attrs key for per-account AI
// event analysis settings. llm-server reads it (events.EventAnalysisSettings)
// and llm.ProcessEvent reads excluded_namespaces.
const eventAnalysisSettingsAttr = "event_analysis"

const (
	maxEventAnalysisReuseWindowHours = 720
	maxEventAnalysisNamespaces       = 100
)

var k8sNamespacePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

type eventAnalysisSettings struct {
	ReuseRelated          *bool    `json:"reuse_related"`
	ReuseWindowHours      *int     `json:"reuse_window_hours"`
	AlwaysFreshNamespaces []string `json:"always_fresh_namespaces"`
	ExcludedNamespaces    []string `json:"excluded_namespaces"`
}

// validateEventAnalysisAttribute rejects a malformed event_analysis value at
// write time; llm-server fails open on a bad row, so an unvalidated typo would
// silently fall back to defaults. Other attributes pass through untouched.
func validateEventAnalysisAttribute(sc *security.SecurityContext, attr AccountAttr) error {
	if attr.Name != eventAnalysisSettingsAttr {
		return nil
	}
	if sc == nil || !sc.HasAccountAccess(attr.CloudAccountId, security.SecurityAccessTypeUpdate) ||
		(!sc.IsSuperAdmin() && !sc.IsTenantAdmin() && !sc.HasScopedRole(security.AUTH_ACCOUNT_ADMIN_ROLE, attr.CloudAccountId)) {
		return common.ErrorUnauthorized("Not Allowed")
	}
	var settings eventAnalysisSettings
	decoder := json.NewDecoder(bytes.NewReader([]byte(attr.Value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		return fmt.Errorf("invalid event_analysis: %w", err)
	}
	if h := settings.ReuseWindowHours; h != nil && (*h < 1 || *h > maxEventAnalysisReuseWindowHours) {
		return fmt.Errorf("invalid event_analysis: reuse_window_hours must be between 1 and %d", maxEventAnalysisReuseWindowHours)
	}
	for field, namespaces := range map[string][]string{
		"always_fresh_namespaces": settings.AlwaysFreshNamespaces,
		"excluded_namespaces":     settings.ExcludedNamespaces,
	} {
		if len(namespaces) > maxEventAnalysisNamespaces {
			return fmt.Errorf("invalid event_analysis: %s allows at most %d namespaces", field, maxEventAnalysisNamespaces)
		}
		for _, ns := range namespaces {
			if len(ns) > 63 || !k8sNamespacePattern.MatchString(ns) {
				return fmt.Errorf("invalid event_analysis: %q in %s is not a valid Kubernetes namespace", ns, field)
			}
		}
	}
	return nil
}
