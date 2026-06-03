package integrations

import (
	"nudgebee/services/event"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Grafana-via-Zenduty fixture. Shape matches a real captured payload but all
// identifiers are synthetic: ZDFAKE* / GRAFAKE* / all-zero UUID variant.
// The summary is the Alertmanager firing-text format Grafana emits when
// posting to Zenduty's outgoing webhook.
const zendutyGrafanaPayload = `{
  "payload": {
    "event_type": "triggered",
    "incident": {
      "summary": "**Firing**\n\nValue: [no value]\nLabels:\n - alertname = DatasourceNoData\n - datasource_uid = GRAFAKEDS00000001\n - grafana_folder = Alarms\n - ref_id = A\n - rulename = Sample Service Response Time > 2s\n - severity = warning\nAnnotations:\nSource: http://localhost:3000/alerting/grafana/grafakerule0001/view?orgId=1\n",
      "incident_number": 4,
      "creation_date": "2026-05-25T06:06:23Z",
      "status": 1,
      "unique_id": "ZDFAKEINCIDENT00000001",
      "title": "[Grafana] - [FIRING:1] DatasourceNoData Alarms (GRAFAKEDS00000001 A Sample Service Response Time > 2s warning)",
      "incident_key": "ZDFAKEKEY00000000001",
      "service": {
        "name": "Sample Service",
        "unique_id": "00000000-0000-0000-0000-00000000fake"
      },
      "urgency": 1,
      "assigned_to": {
        "username": "test",
        "first_name": "Test",
        "last_name": "User",
        "email": "test@example.com"
      }
    }
  }
}`

// Prometheus alert routed through Zenduty. Title prefix [Prometheus][...] is the
// convention we see in dev — the summary still uses Alertmanager firing format.
const zendutyPrometheusPayload = `{
  "payload": {
    "event_type": "triggered",
    "incident": {
      "summary": "**Firing**\n\nLabels:\n - alertname = KubePodCrashLooping\n - namespace = payments-prod\n - pod = checkout-api-7f8d9-bbvls\n - deployment = checkout-api\n - service_name = checkout-api\n - severity = critical\nAnnotations:\n - summary = Pod is crash looping\n - description = checkout-api pod has restarted 5 times in the last 10 minutes\nSource: http://prom.example.com/alerts\n",
      "incident_number": 99,
      "creation_date": "2026-05-25T07:00:00Z",
      "status": 1,
      "unique_id": "ZD-PROM-99",
      "title": "[Prometheus][KubePodCrashLooping] - Pod is crash looping.",
      "incident_key": "prom-KubePodCrashLooping-checkout-api",
      "service": {
        "name": "Payments Team",
        "unique_id": "svc-payments"
      },
      "urgency": 2
    }
  }
}`

// Custom integration that doesn't emit Alertmanager-style labels — exercises
// the fallback path. The leak-prevention assertion is the critical one here:
// subject_name must NOT become "Database Team" (Zenduty's service grouping).
const zendutyCustomPayload = `{
  "payload": {
    "event_type": "triggered",
    "incident": {
      "summary": "Free-form text from a custom integration. No labels.",
      "incident_number": 1,
      "creation_date": "2026-05-25T08:00:00Z",
      "status": 1,
      "unique_id": "ZD-999",
      "title": "Disk full on prod-db-1",
      "service": {
        "name": "Database Team",
        "unique_id": "svc-db"
      },
      "urgency": 1
    }
  }
}`

func newZendutyIntegration(t *testing.T) ZenDutyWebhook {
	t.Helper()
	integ, _ := core.GetIntegration(IntegrationZendutyWebhook)
	assert.NotNil(t, integ, "ZenDuty integration should be registered")
	zd, ok := integ.(ZenDutyWebhook)
	assert.True(t, ok, "integration should be a ZenDutyWebhook")
	return zd
}

func newTestCtx() *security.RequestContext {
	return security.NewRequestContextForUserTenant(os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT"), nil, nil, nil)
}

// Test 1: Grafana-via-Zenduty (matches the bug-report payload shape).
// Locks in the fix for all four reported bugs:
//   - subject_name no longer leaks "Sample Service"
//   - fingerprint populated from IncidentKey
//   - RuleId derived from alertname (not the per-incident UniqueID)
//   - inner Alertmanager labels reach Investigation.Labels
func TestZenduty_GrafanaRouted_FixesAllFourBugs(t *testing.T) {
	zd := newZendutyIntegration(t)

	out, err := zd.ProcessEventWebook(newTestCtx(), []core.IntegrationConfigValue{}, os.Getenv("TEST_ACCOUNT"), zendutyGrafanaPayload)
	assert.NoError(t, err)
	assert.Len(t, out, 1)

	got := out[0]
	inv := got.Investigation

	// Bug 3: RuleId/RuleName derived from extracted alertname, not incident.UniqueID
	assert.Equal(t, "DatasourceNoData", inv.RuleId, "RuleId should be alertname, not per-incident UniqueID")
	assert.Equal(t, "DatasourceNoData", inv.RuleName)

	// Bug 2: Fingerprint populated from Zenduty's stable IncidentKey
	assert.Equal(t, "ZDFAKEKEY00000000001", inv.Fingerprint)

	// Bug 4: Inner Alertmanager labels reach Investigation.Labels
	assert.Equal(t, "DatasourceNoData", inv.Labels["alertname"])
	assert.Equal(t, "Sample Service Response Time > 2s", inv.Labels["rulename"])
	assert.Equal(t, "warning", inv.Labels["severity"])
	assert.Equal(t, "Alarms", inv.Labels["grafana_folder"])
	assert.Equal(t, "GRAFAKEDS00000001", inv.Labels["datasource_uid"])
	assert.Contains(t, inv.Labels["source_url"], "grafana/grafakerule0001")

	// Bug 1: Zenduty grouping never leaks via service_name label
	_, hasServiceName := inv.Labels["service_name"]
	assert.False(t, hasServiceName, "service_name label must be absent — leak path into subjectKeys")
	assert.Equal(t, "Sample Service", inv.Labels["nb_zenduty_service_name"], "Zenduty grouping preserved under nb_ prefix")
	assert.Equal(t, "00000000-0000-0000-0000-00000000fake", inv.Labels["nb_zenduty_service_id"])

	// Severity refinement: warning → Medium
	assert.Equal(t, event.EventPriortiyMedium, inv.Severity)

	// nb_alert_* normalization wired so buildAlertRuleEvidence has data to render
	assert.Equal(t, "grafana", inv.Labels["nb_alert_source"])
	assert.Equal(t, "DatasourceNoData", inv.Labels["nb_alert_name"])
	assert.Equal(t, "DatasourceNoData", inv.Labels["nb_alert_rule_id"])
	assert.Equal(t, "warning", inv.Labels["nb_alert_severity"])

	// Title rewritten to readable form (rulename wins when annotation_summary absent)
	assert.Equal(t, "Sample Service Response Time > 2s", got.EventTitle)

	// Subject correctly unresolved (no workload-shaped label in this payload).
	// Empty is the right answer per bug report: better than wrong.
	assert.Equal(t, "", got.EventSubjectName)
	assert.Equal(t, "unresolved", inv.Labels["nb_subject_resolution"])

	// Alert-rule evidence card rendered
	foundRuleEvidence := false
	for _, ev := range inv.Evidences {
		if ev.Type != "markdown" {
			continue
		}
		dataMap, ok := ev.Data.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := dataMap["name"].(string); name == "Alert Rule Details" {
			foundRuleEvidence = true
			break
		}
	}
	assert.True(t, foundRuleEvidence, "buildAlertRuleEvidence should append an Alert Rule Details card")
}

// Test 2: Prometheus-via-Zenduty with workload-shaped labels exercises the
// shared resolveSubjectFromLabels prioritized walk (deployment wins).
func TestZenduty_PrometheusRouted_ResolvesSubjectFromLabels(t *testing.T) {
	zd := newZendutyIntegration(t)

	out, err := zd.ProcessEventWebook(newTestCtx(), []core.IntegrationConfigValue{}, os.Getenv("TEST_ACCOUNT"), zendutyPrometheusPayload)
	assert.NoError(t, err)
	assert.Len(t, out, 1)

	got := out[0]
	inv := got.Investigation

	// Subject resolved from deployment label (high-priority key in resolveSubjectFromLabels)
	assert.Equal(t, "checkout-api", got.EventSubjectName)
	assert.Equal(t, "payments-prod", got.EventSubjectNamespace)

	// Real upstream service_name from firing labels — NOT Zenduty grouping
	assert.Equal(t, "checkout-api", inv.Labels["service_name"], "upstream service_name from firing labels reaches labels")
	assert.Equal(t, "Payments Team", inv.Labels["nb_zenduty_service_name"], "Zenduty grouping under nb_ prefix")

	// Rule from alertname
	assert.Equal(t, "KubePodCrashLooping", inv.RuleId)
	assert.Equal(t, "KubePodCrashLooping", inv.RuleName)

	// Fingerprint from IncidentKey
	assert.Equal(t, "prom-KubePodCrashLooping-checkout-api", inv.Fingerprint)

	// Severity refinement: critical → High
	assert.Equal(t, event.EventPriortiyHigh, inv.Severity)

	// Source classified as prometheus (no grafana_folder/datasource_uid)
	assert.Equal(t, "prometheus", inv.Labels["nb_alert_source"])

	// Title rewritten to annotation_summary (which parseFiringLabels keys as "summary")
	assert.Equal(t, "Pod is crash looping", got.EventTitle)
}

// Test 3: Custom integration without Alertmanager labels — the critical
// regression test proving the service_name leak is plugged in all paths.
func TestZenduty_CustomPayload_PreventsServiceNameLeak(t *testing.T) {
	zd := newZendutyIntegration(t)

	out, err := zd.ProcessEventWebook(newTestCtx(), []core.IntegrationConfigValue{}, os.Getenv("TEST_ACCOUNT"), zendutyCustomPayload)
	assert.NoError(t, err)
	assert.Len(t, out, 1)

	got := out[0]
	inv := got.Investigation

	// CRITICAL leak-prevention assertion: service_name label MUST be absent
	// so event/service.go:1683's subjectKeys walk cannot pick it as subject.
	_, hasServiceName := inv.Labels["service_name"]
	assert.False(t, hasServiceName, "service_name MUST be absent — this is the leak the fix prevents")

	// Zenduty grouping safely preserved under nb_ prefix
	assert.Equal(t, "Database Team", inv.Labels["nb_zenduty_service_name"])

	// Fallback values when summary has no alertname/IncidentKey
	assert.Equal(t, "ZD-999", inv.RuleId, "falls back to UniqueID when alertname/rulename absent")
	assert.Equal(t, "ZD-999", inv.Fingerprint, "falls back to UniqueID when IncidentKey empty")

	// Title preserved (no annotation_summary to override with)
	assert.Equal(t, "Disk full on prod-db-1", got.EventTitle)

	// Subject unresolved is correct — better than the wrong-but-stable
	// "Database Team" that the bug originally produced.
	assert.Equal(t, "", got.EventSubjectName)
}

// Test 4: Dedup stability across status transitions — same IncidentKey,
// different event_type, must produce identical fingerprint + RuleId.
// Locks in the fix for "every webhook delivery becomes a new event".
func TestZenduty_DedupStableAcrossStatusTransitions(t *testing.T) {
	zd := newZendutyIntegration(t)
	ctx := newTestCtx()
	acct := os.Getenv("TEST_ACCOUNT")

	triggered, err := zd.ProcessEventWebook(ctx, []core.IntegrationConfigValue{}, acct, zendutyPrometheusPayload)
	assert.NoError(t, err)
	assert.Len(t, triggered, 1)

	// Same payload but flipped to resolved (status=3 = ZenDutyStatusResolved, event_type="resolved")
	resolvedPayload := strings.NewReplacer(
		`"event_type": "triggered"`, `"event_type": "resolved"`,
		`"status": 1`, `"status": 3`,
	).Replace(zendutyPrometheusPayload)

	resolved, err := zd.ProcessEventWebook(ctx, []core.IntegrationConfigValue{}, acct, resolvedPayload)
	assert.NoError(t, err)
	assert.Len(t, resolved, 1)

	// Critical: same fingerprint + RuleId across status transitions.
	// Today (before fix) every webhook delivery would dedup-miss because
	// Fingerprint was empty and RuleId was the per-incident UniqueID.
	assert.Equal(t, triggered[0].Investigation.Fingerprint, resolved[0].Investigation.Fingerprint)
	assert.Equal(t, triggered[0].Investigation.RuleId, resolved[0].Investigation.RuleId)

	// Status should still differ correctly
	assert.Equal(t, event.EventStatusFiring, triggered[0].Investigation.Status)
	assert.Equal(t, event.EventStatusResolved, resolved[0].Investigation.Status)
}
