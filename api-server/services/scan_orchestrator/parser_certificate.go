package scan_orchestrator

import (
	"encoding/json"
	"fmt"
	"time"
)

// CertificateExpiryRuleName matches the collector's RuleName.CERTIFICATE_EXPIRY
// (event_handler.py:329) — the UI keys on this exact value (see
// app/src/components1/recommendations/KubernetesSSLCertificateRecommendation.jsx).
const CertificateExpiryRuleName = "certificate_expiry"

// DefaultCertExpiryWarningDays mirrors the collector's default in
// handle_certificate_scanner_report (event_handler.py:2205) — certificates
// expiring within this many days are flagged High severity. Future PR may
// thread the cloud_account_attrs setting `certificate_expiry_recommendation`
// through here.
const DefaultCertExpiryWarningDays = 30

// ParseCertificates turns the agent's snake_cased cert-manager certificate
// list (returned by get_resource for cert-manager.io/v1/certificates) into
// Recommendation rows. Mirrors the collector's
// handle_certificate_scanner_report (event_handler.py:2198-2253):
//
//   - severity = High when status.not_after is within DefaultCertExpiryWarningDays
//   - certificates without status.not_after are skipped (not yet issued)
//   - account_object_id matches collector's get_certificate_id —
//     "<namespace>/<name>"
//
// Note the snake_case input: agent's get_resource passes K8s output through
// SnakeKeysDeep (kube/snake.go), so notAfter → not_after etc. The collector
// reads camelCase because it ran in Robusta which used Hikaru.
func ParseCertificates(certs []map[string]any, account ScanAccount, now time.Time) ([]Recommendation, error) {
	out := make([]Recommendation, 0, len(certs))
	for _, cert := range certs {
		md, _ := cert["metadata"].(map[string]any)
		status, _ := cert["status"].(map[string]any)
		name, _ := md["name"].(string)
		namespace, _ := md["namespace"].(string)
		notAfterStr, _ := status["not_after"].(string)
		if name == "" || notAfterStr == "" {
			continue
		}
		notAfter, err := time.Parse(time.RFC3339, notAfterStr)
		if err != nil {
			continue
		}
		daysUntilExpiry := int(notAfter.Sub(now).Hours() / 24)
		severity := "Info"
		if daysUntilExpiry <= DefaultCertExpiryWarningDays {
			severity = "High"
		}

		recommendationContent := map[string]any{
			"name":              name,
			"namespace":         namespace,
			"expiry_date":       notAfter.Format("2006-01-02 15:04:05"),
			"days_until_expiry": daysUntilExpiry,
			// metadata sub-shape mirrors what the collector's
			// get_workload_details extracted (annotations + labels filtered).
			// Passing through the snake_cased annotations/labels is enough for
			// the UI's CertificateExpiryEvidence panel.
			"metadata": map[string]any{
				"annotations": md["annotations"],
				"labels":      md["labels"],
			},
		}
		recommendationJSON, err := json.Marshal(recommendationContent)
		if err != nil {
			return nil, fmt.Errorf("certificate: encode recommendation: %w", err)
		}

		out = append(out, Recommendation{
			CloudAccountID:       account.AccountID,
			TenantID:             account.TenantID,
			Category:             "Configuration",
			RuleName:             CertificateExpiryRuleName,
			RecommendationAction: "Modify",
			Recommendation:       string(recommendationJSON),
			Severity:             severity,
			Status:               "Open",
			AccountObjectID:      formatCertObjectID(namespace, name),
		})
	}
	return out, nil
}

// formatCertObjectID matches the collector's get_certificate_id —
// "<namespace>/<name>" — so existing rows UPSERT in place after the migration.
func formatCertObjectID(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return fmt.Sprintf("%s/%s", namespace, name)
}
