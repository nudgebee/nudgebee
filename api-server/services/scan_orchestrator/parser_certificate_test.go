package scan_orchestrator

import (
	"encoding/json"
	"testing"
	"time"
)

// Real cert-manager.io/v1/certificates shape after the agent's SnakeKeysDeep
// (status.notAfter → status.not_after). One expired cert (severity High via
// the within-warning-window branch), one healthy cert (severity Info), one
// without status.not_after (skipped — not yet issued).
const certFixture = `[
  {
    "api_version": "cert-manager.io/v1",
    "kind": "Certificate",
    "metadata": {"name": "expiring-soon", "namespace": "monitoring"},
    "status":   {"not_after": "2026-05-15T00:00:00Z"}
  },
  {
    "api_version": "cert-manager.io/v1",
    "kind": "Certificate",
    "metadata": {"name": "healthy", "namespace": "ingress-nginx"},
    "status":   {"not_after": "2027-08-01T00:00:00Z"}
  },
  {
    "api_version": "cert-manager.io/v1",
    "kind": "Certificate",
    "metadata": {"name": "not-yet-issued", "namespace": "default"},
    "status":   {}
  }
]`

func TestParseCertificates_SeverityWindow(t *testing.T) {
	var certs []map[string]any
	if err := json.Unmarshal([]byte(certFixture), &certs); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	recs, err := ParseCertificates(certs, ScanAccount{AccountID: "acc-1", TenantID: "tenant-1"}, now)
	if err != nil {
		t.Fatalf("ParseCertificates: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("recs = %d; want 2 (skipped: not-yet-issued)", len(recs))
	}

	byID := map[string]Recommendation{}
	for _, r := range recs {
		byID[r.AccountObjectID] = r
	}

	// expiring-soon (5 days out → ≤ 30 day warning window)
	expiring, ok := byID["monitoring/expiring-soon"]
	if !ok {
		t.Fatalf("missing monitoring/expiring-soon; have %v", byID)
	}
	if expiring.Severity != "High" {
		t.Errorf("expiring.Severity = %q; want High (within %d-day window)", expiring.Severity, DefaultCertExpiryWarningDays)
	}
	if expiring.RuleName != CertificateExpiryRuleName {
		t.Errorf("expiring.RuleName = %q", expiring.RuleName)
	}
	if expiring.Category != "Configuration" {
		t.Errorf("expiring.Category = %q", expiring.Category)
	}

	// healthy (>1 year out → Info)
	healthy, ok := byID["ingress-nginx/healthy"]
	if !ok {
		t.Fatalf("missing ingress-nginx/healthy")
	}
	if healthy.Severity != "Info" {
		t.Errorf("healthy.Severity = %q; want Info", healthy.Severity)
	}

	// recommendation JSON shape — collector's UI reads name/namespace/expiry_date/days_until_expiry
	var content map[string]any
	if err := json.Unmarshal([]byte(expiring.Recommendation), &content); err != nil {
		t.Fatalf("recommendation not valid JSON: %v", err)
	}
	if content["name"] != "expiring-soon" {
		t.Errorf("content.name = %v", content["name"])
	}
	if content["namespace"] != "monitoring" {
		t.Errorf("content.namespace = %v", content["namespace"])
	}
	days, _ := content["days_until_expiry"].(float64)
	if int(days) != 5 {
		t.Errorf("content.days_until_expiry = %v; want 5", content["days_until_expiry"])
	}
}

func TestParseCertificates_SkipsInvalidNotAfter(t *testing.T) {
	certs := []map[string]any{
		{
			"metadata": map[string]any{"name": "bad", "namespace": "x"},
			"status":   map[string]any{"not_after": "not-a-date"},
		},
	}
	recs, err := ParseCertificates(certs, ScanAccount{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Errorf("recs = %d; want 0 (invalid date skipped)", len(recs))
	}
}

func TestFormatCertObjectID(t *testing.T) {
	if got := formatCertObjectID("monitoring", "tls"); got != "monitoring/tls" {
		t.Errorf("got %q", got)
	}
	if got := formatCertObjectID("", "cluster-cert"); got != "cluster-cert" {
		t.Errorf("got %q (cluster-scoped should drop namespace)", got)
	}
}
