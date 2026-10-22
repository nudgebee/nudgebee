package integrations

import (
	"encoding/json"
	"testing"

	"nudgebee/services/event"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Values below are from a real trigger and its reset of the "Node is down" alert on
// an Orion 2026.4.0 test bed (firing 18, 2026-10-21/22): the node was polled at
// 10.128.0.250 when it went down and at 10.128.0.107 when it came back, so the
// reset reports a different ip than its trigger; alert_active_id and the
// NetObject were the same on both.
const orionTestIntegrationId = "15c3dfc9-926b-4c53-85fa-52ca8a75c5ed"

func orionBody(overrides map[string]string) string {
	body := map[string]string{
		"status":          "firing",
		"alert_def_id":    "97808111-9879-4961-b8a9-5face6a39b06",
		"alert_active_id": "18",
		"alert_name":      "Node is down",
		"object":          "10.128.0.250",
		"object_type":     "Node",
		"object_url":      "https://orion-testbed:443/Orion/NetPerfMon/NodeDetails.aspx?NetObject=N:4",
		"ip":              "10.128.0.250",
		"severity":        "Critical",
		"message":         "Node is down",
		"url":             "https://orion-testbed:443/Orion/View.aspx?NetObject=AAT:14",
	}
	for k, v := range overrides {
		if v == "<omit>" {
			delete(body, k)
			continue
		}
		body[k] = v
	}
	b, _ := json.Marshal(body)
	return string(b)
}

func mustParseOrion(t *testing.T, body string) orionPayload {
	t.Helper()
	p, verr := parseOrionPayload(body)
	require.Nil(t, verr)
	return p
}

func orionSettings() []core.IntegrationConfigValue {
	return []core.IntegrationConfigValue{{Name: core.SyntheticIntegrationIdKey, Value: orionTestIntegrationId}}
}

func parseOrion(t *testing.T, body string) core.EventIncomingWebhook {
	t.Helper()
	sc := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	events, err := OrionWebhook{}.ProcessEventWebook(sc, orionSettings(), "acct-1", body)
	require.NoError(t, err)
	require.Len(t, events, 1)
	return events[0]
}

func TestOrionWebhook_TriggerAndResetShareIdentity(t *testing.T) {
	trigger := parseOrion(t, orionBody(nil))
	reset := parseOrion(t, orionBody(map[string]string{"status": "resolved", "ip": "10.128.0.107"}))

	wantFingerprint := "ow:" + orionTestIntegrationId + ":97808111-9879-4961-b8a9-5face6a39b06|N:4"
	assert.Equal(t, wantFingerprint, trigger.Investigation.Fingerprint)
	assert.Equal(t, wantFingerprint, trigger.EventId, "the reset closes the alert's open row by this id")
	assert.Equal(t, trigger.Investigation.Fingerprint, reset.Investigation.Fingerprint, "a changed ip must not change identity")
	assert.Equal(t, trigger.EventId, reset.EventId)
	assert.Equal(t, "18", trigger.FiringKey)
	assert.Equal(t, trigger.FiringKey, reset.FiringKey)

	assert.Equal(t, string(event.EventStatusFiring), trigger.EventStatus)
	assert.Equal(t, string(event.EventStatusResolved), reset.EventStatus)
	assert.Equal(t, string(event.EventPriorityHigh), trigger.EventPriority)
	assert.Equal(t, "Node is down on 10.128.0.250", trigger.EventTitle)
	assert.Equal(t, "10.128.0.250", trigger.EventSubjectName)
	assert.Equal(t, "host", trigger.EventSubjectKind)
	assert.Equal(t, "https://orion-testbed:443/Orion/View.aspx?NetObject=AAT:14", trigger.EventUrl)
	assert.True(t, trigger.EventCreatedAt.IsZero(), "Orion's trigger-time macro has no zone; the delivery time is used")

	assert.Equal(t, "Node is down", trigger.Investigation.RuleName)
	assert.Equal(t, "Node is down", trigger.Investigation.RuleId, "aggregation_key is the alert's name, as event_rules.alert")
	labels := trigger.Investigation.Labels
	assert.Equal(t, "10.128.0.250", labels["ip"])
	assert.Equal(t, "10.128.0.107", reset.Investigation.Labels["ip"])
	assert.Equal(t, "N:4", labels["orion_netobject"])
	assert.Equal(t, "97808111-9879-4961-b8a9-5face6a39b06", labels["orion_alert_def_id"])
	assert.Equal(t, "18", labels["orion_alert_active_id"])
	assert.Equal(t, "Node", labels["object_type"])
	assert.Equal(t, "SolarWinds Platform", labels["source_tool"])
	assert.Equal(t, "true", labels["nb_skip_workload_match"])
	assert.NotContains(t, labels, "orion_identity")
}

func TestOrionWebhook_SameCaptionOnTwoObjectsIsTwoAlerts(t *testing.T) {
	diskA := parseOrion(t, orionBody(map[string]string{
		"alert_name": "Disk space low", "object": `C:\`, "object_type": "Volume",
		"object_url": "https://orion/Orion/NetPerfMon/VolumeDetails.aspx?NetObject=V:12",
	}))
	diskB := parseOrion(t, orionBody(map[string]string{
		"alert_name": "Disk space low", "object": `C:\`, "object_type": "Volume",
		"object_url": "https://orion/Orion/NetPerfMon/VolumeDetails.aspx?NetObject=V:31",
	}))
	assert.NotEqual(t, diskA.Investigation.Fingerprint, diskB.Investigation.Fingerprint)
	assert.Equal(t, "other", diskA.EventSubjectKind)
}

// Orion JSON-escapes macro values when it renders the body: a node named
// TEST\ "quoted" arrived as "TEST\\ \"quoted\"" (verified on Orion 2026.4.0), so a
// volume caption like C:\ or a quoted message stays valid JSON.
func TestOrionWebhook_EscapedValuesAsOrionSendsThem(t *testing.T) {
	body := `{"status":"firing","alert_def_id":"97808111-9879-4961-b8a9-5face6a39b06","alert_active_id":"",` +
		`"alert_name":"Node is down","object":"TEST\\ \"quoted\"","object_type":"Node",` +
		`"object_url":"https://orion-testbed:443/Orion/NetPerfMon/NodeDetails.aspx?NetObject=N:4"}`
	e := parseOrion(t, body)
	assert.Equal(t, `TEST\ "quoted"`, e.EventSubjectName)
	assert.Equal(t, `Node is down on TEST\ "quoted"`, e.EventTitle)
	assert.Equal(t, "ow:"+orionTestIntegrationId+":97808111-9879-4961-b8a9-5face6a39b06|N:4", e.Investigation.Fingerprint,
		"a renamed node keeps its identity")
}

func TestOrionWebhook_TwoOrionServersNeverShareAnAlert(t *testing.T) {
	body := orionBody(nil)
	p, verr := parseOrionPayload(body)
	require.Nil(t, verr)
	a, _ := orionFingerprint("integration-a", p)
	b, _ := orionFingerprint("integration-b", p)
	assert.NotEqual(t, a, b, "two Orion servers both number their nodes from N:1")
}

func TestOrionWebhook_TestButtonDelivery(t *testing.T) {
	// Orion's Test leaves the per-firing macros unresolved or empty.
	e := parseOrion(t, orionBody(map[string]string{
		"alert_active_id": "${N=Alerting;M=AlertActiveID}",
		"url":             "",
	}))
	assert.Empty(t, e.FiringKey, "every Test delivery stays on the alert's one row")
	assert.NotContains(t, e.Investigation.Labels, "orion_alert_active_id")
	assert.Empty(t, e.EventUrl)
}

func TestOrionWebhook_CaptionFallbackIsVisible(t *testing.T) {
	e := parseOrion(t, orionBody(map[string]string{"object_url": "${N=SwisEntity;M=DetailsUrl}"}))
	assert.Equal(t, "ow:"+orionTestIntegrationId+":97808111-9879-4961-b8a9-5face6a39b06|caption:10.128.0.250", e.Investigation.Fingerprint)
	assert.Equal(t, "caption_fallback", e.Investigation.Labels["orion_identity"])
}

func TestOrionWebhook_RejectsWithFieldErrors(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
	}{
		{"missing alert_def_id", orionBody(map[string]string{"alert_def_id": "<omit>"}), "alert_def_id"},
		{"unresolved alert_def_id macro", orionBody(map[string]string{"alert_def_id": "${N=Alerting;M=AlertDefID}"}), "alert_def_id"},
		{"missing alert_name", orionBody(map[string]string{"alert_name": ""}), "alert_name"},
		{"unknown status", orionBody(map[string]string{"status": "triggered"}), "status"},
		{"no object at all", orionBody(map[string]string{"object": "<omit>", "object_url": "<omit>"}), "object_url"},
		{"javascript link", orionBody(map[string]string{"url": "javascript:alert(1)"}), "url"},
		{"not JSON", `{"status": "firing" "alert_name": "x"}`, "payload"},
		{"not an object", `[]`, "payload"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := OrionWebhook{}.ValidateWebhookPayload(c.body)
			require.Error(t, err)
			var verr *core.WebhookValidationError
			require.ErrorAs(t, err, &verr)
			fields := []string{}
			for _, it := range verr.Items {
				fields = append(fields, it.Field)
			}
			assert.Contains(t, fields, c.field)
		})
	}
	assert.NoError(t, OrionWebhook{}.ValidateWebhookPayload(orionBody(nil)))
}

func TestOrionWebhook_SeverityWords(t *testing.T) {
	for word, want := range map[string]event.EventPriority{
		"Critical":      event.EventPriorityHigh,
		"Serious":       event.EventPriorityHigh,
		"Warning":       event.EventPriorityMedium,
		"Notice":        event.EventPriorityLow,
		"Informational": event.EventPriorityInfo,
		"":              event.EventPriorityMedium,
		"Something new": event.EventPriorityMedium,
	} {
		assert.Equal(t, want, mapOrionSeverity(word), word)
	}
}

func TestOrionWebhook_EventRuleIsOnePerAlert(t *testing.T) {
	p, verr := parseOrionPayload(orionBody(nil))
	require.Nil(t, verr)
	rule := orionEventRule("acct-1", p, mapOrionSeverity(p.Severity))
	assert.Equal(t, "Node is down", rule.Alert, "the alert's name, not the per-object title")
	assert.Equal(t, "97808111-9879-4961-b8a9-5face6a39b06", rule.ExternalRuleID)
	assert.Equal(t, IntegrationOrionWebhook, rule.Source)
	assert.Equal(t, "critical", rule.Severity)
}

func TestOrionWebhook_NeedsIntegrationId(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	_, err := OrionWebhook{}.ProcessEventWebook(sc, nil, "acct-1", orionBody(nil))
	assert.Error(t, err)
}

func TestOrionNetObject(t *testing.T) {
	assert.Equal(t, "N:4", orionNetObject("https://orion-testbed:443/Orion/NetPerfMon/NodeDetails.aspx?NetObject=N:4"))
	assert.Equal(t, "I:7", orionNetObject("/Orion/Interfaces/InterfaceDetails.aspx?netobject=I:7"))
	assert.Equal(t, "", orionNetObject("https://orion/Orion/View.aspx"))
	assert.Equal(t, "", orionNetObject(""))
}
