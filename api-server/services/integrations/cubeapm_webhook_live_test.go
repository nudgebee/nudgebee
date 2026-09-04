package integrations

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"nudgebee/services/security"
)

// liveCubeAPMWebhookPayload is CubeAPM's default (Alertmanager-compatible)
// notification body, shaped exactly as its webhook template renders it —
// including the four fields Alertmanager has no equivalent for.
const liveCubeAPMWebhookPayload = `{
  "receiver": "nudgebee",
  "status": "firing",
  "alerts": [
    {
      "status": "firing",
      "labels": {
        "alertname": "PaymentServiceHighErrorRate",
        "severity": "critical",
        "namespace": "payments",
        "deployment": "payment-service",
        "env": "UNSET"
      },
      "annotations": {
        "summary": "payment-service error rate above 5%",
        "description": "cube_apm_errors_total exceeded the threshold for 5 minutes",
        "runbook_url": "https://runbooks.example.com/payment"
      },
      "startsAt": "2026-09-04T10:15:00Z",
      "endsAt": "0001-01-01T00:00:00Z",
      "generatorURL": "http://cubeapm:3125/alerts/2",
      "fingerprint": "live-e2e-8c1f2a9b",
      "cubeImageURL": "http://cubeapm:3125/render/alert/2.png",
      "cubeSampleLog": "2026-09-04T10:14:59Z ERROR Failed connecting to database"
    }
  ],
  "groupLabels": {"alertname": "PaymentServiceHighErrorRate"},
  "commonLabels": {"cluster": "demo-cluster"},
  "commonAnnotations": {"team": "payments"},
  "externalURL": "http://cubeapm:3125",
  "groupKeyHash": "live-e2e-group",
  "incidentTime": "2026-09-04T10:15:00Z",
  "notifyTime": "2026-09-04T10:15:30Z"
}`

// TestLiveCubeAPMWebhook drives the real ProcessEventWebook against a live
// database, exercising payload parsing, subject extraction and evidence building
// the same way an inbound CubeAPM alert would.
//
// Skipped unless explicitly enabled, so it never runs in CI:
//
//	LIVE_CUBEAPM_TENANT_ID=<tenant-uuid> \
//	LIVE_CUBEAPM_ACCOUNT_ID=<cloud-account-uuid> \
//	go test ./integrations/ -run TestLiveCubeAPMWebhook -v
//
// NOTE: unlike the observability live tests, this one WRITES — it upserts the
// event rule the alert belongs to, which is what a real delivery does.
func TestLiveCubeAPMWebhook(t *testing.T) {
	tenantId := os.Getenv("LIVE_CUBEAPM_TENANT_ID")
	accountId := os.Getenv("LIVE_CUBEAPM_ACCOUNT_ID")
	if tenantId == "" || accountId == "" {
		t.Skip("set LIVE_CUBEAPM_TENANT_ID and LIVE_CUBEAPM_ACCOUNT_ID to run this against a real database")
	}

	sc := security.NewRequestContextForTenantAdmin(tenantId, slog.Default(), nil, nil)
	if sc == nil {
		t.Fatal("could not build a tenant-admin context; check the database connection")
	}

	events, err := CubeAPMWebhook{}.ProcessEventWebook(sc, nil, accountId, liveCubeAPMWebhookPayload)
	if err != nil {
		t.Fatalf("ProcessEventWebook failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	e := events[0]

	t.Logf("WEBHOOK EVENT: type=%q title=%q status=%q priority=%q",
		e.EventType, e.EventTitle, e.EventStatus, e.EventPriority)
	t.Logf("  subject: kind=%q name=%q namespace=%q", e.EventSubjectKind, e.EventSubjectName, e.EventSubjectNamespace)
	t.Logf("  url=%q fingerprint=%q tags=%v", e.EventUrl, e.EventId, e.EventTags)
	t.Logf("  evidences: %d", len(e.Investigation.Evidences))
	for _, ev := range e.Investigation.Evidences {
		t.Logf("    type=%s action=%v", ev.Type, ev.AdditionalInfo["action_name"])
	}

	if e.EventType != "PaymentServiceHighErrorRate" {
		t.Errorf("EventType = %q, want the alertname", e.EventType)
	}
	// deployment= is the strongest subject signal; a pod name would be ephemeral.
	if e.EventSubjectKind != "deployment" || e.EventSubjectName != "payment-service" {
		t.Errorf("subject = %s/%s, want deployment/payment-service", e.EventSubjectKind, e.EventSubjectName)
	}
	if e.EventSubjectNamespace != "payments" {
		t.Errorf("namespace = %q, want payments", e.EventSubjectNamespace)
	}
	if e.EventCreatedAt.IsZero() {
		t.Error("startsAt did not parse")
	}
	// endsAt is CubeAPM's year-1 zero time for a still-firing alert; treating it
	// as a real end would resolve the event immediately.
	if !e.EventEndsAt.IsZero() {
		t.Errorf("EventEndsAt = %v, want zero for a firing alert", e.EventEndsAt)
	}
	// commonLabels must be merged in, or grouping context is lost.
	if e.Investigation.Labels["cluster"] != "demo-cluster" {
		t.Errorf("commonLabels were not merged: %v", e.Investigation.Labels)
	}
	// runbook_url is an annotation upstream but the evidence card reads Labels.
	if e.Investigation.Labels["runbook_url"] == "" {
		t.Error("runbook_url was not surfaced onto labels; the runbook link would not render")
	}

	// The alert table, the sample log and the chart image should all be present.
	if len(e.Investigation.Evidences) != 3 {
		t.Errorf("got %d evidences, want 3 (alert table + sample log + chart)", len(e.Investigation.Evidences))
	}

	// Rule registration is deliberately fire-and-forget on a detached context, so
	// a real delivery completes it after the HTTP response has been written. A
	// test process would exit first and the write would never be observed, which
	// looks identical to the write silently failing — so wait for it explicitly.
	time.Sleep(3 * time.Second)
	t.Log("waited for the detached CreateEventRule goroutine; " +
		"query event_rules where source = cubeapm_webhook to confirm")
}
