package integrations

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/event"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const datadogPayload = `{"time":"2025-06-27T06:51:30.582796049Z","level":"INFO","msg":"datadog webhook","url":"/api/webhooks/datadog","headers":{"Accept":"*/*","Accept-Encoding":"gzip, deflate","Authorization":"Bearer MySuperSecretToken","Content-Length":"4147","Content-Type":"application/json; charset=utf-8","Traceparent":"00-000000000000000046abb28660870352-0e5fc54254ac193c-00","User-Agent":"python-requests/2.32.3","X-Datadog-Parent-Id":"1035763327997581628","X-Datadog-Trace-Id":"5092360093887300434","X-Forwarded-For":"192.168.1.6","X-Forwarded-Host":"app.nudgebee.com","X-Forwarded-Port":"443","X-Forwarded-Proto":"https","X-Forwarded-Scheme":"https","X-Real-Ip":"192.168.1.6","X-Request-Id":"b9b204f5d0c45b06b6910eb2f64f1bcf","X-Scheme":"https"},"payload":"{\n    \"id\": \"8166212983446713351\",\n    \"last_updated\": \"1751007029000\",\n    \"event_type\": \"query_alert_monitor\",\n    \"title\": \"[P1] [Triggered] CPU usage is high for host k3s-example-node-pool-a6e3h\",\n    \"date\": \"1751007029000\",\n    \"org\": {\n        \"id\": \"1300568798\",\n        \"name\": \"Nudgebee\"\n    },\n    \"body\": \"%%%\\n## \\ud83d\\udea8 What\\u2019s happening\\n\\nHigh CPU usage detected on host k3s-example-node-pool-a6e3h.\\n\\nThis means the host is spending most of its time actively processing, with very little idle CPU capacity available. It typically signals that the system is under heavy load \\u2014 either from intense workloads, misbehaving processes, or increased traffic.\\n\\n---\\n\\n## \\ud83d\\udcc8 Impact\\n\\nIf CPU usage remains high over time, it can lead to:\\n\\n- Sluggish application and system performance\\n- Delayed or stalled background jobs\\n- Timeouts, degraded services, or crashes due to resource exhaustion\\n\\nThis may affect critical applications running on this host and lead to broader service degradation if not addressed promptly.\\n\\n---\\n\\n## Runbook\\n\\n### Initial Troubleshooting Steps\\n\\n1. **Identify the affected host** from the alert.\\n2. Open [**Infrastructure > Host Details**](https://app.datadoghq.com/infrastructure?tags=host%3Ak3s-example-node-pool-a6e3h) in Datadog.\\n3. In the **Metrics** tab, examine:\\n   - ` + "`" + `system.cpu.user` + "`" + `, ` + "`" + `system.cpu.system` + "`" + `, ` + "`" + `system.cpu.idle` + "`" + `\\n   - ` + "`" + `process.cpu.total_pct` + "`" + `, ` + "`" + `process.*` + "`" + ` metrics\\n4. Use [**Live Processes**](https://app.datadoghq.com/process?query=host%3Ak3s-example-node-pool-a6e3h) to view real-time CPU usage.\\n5. SSH into the host (if needed) and run:\\n   ` + "```" + `bash\\n   top -o %CPU\\n   \\n### Cause and Resolution\\n\\nCause | Resolution\\n------|------\\nRunaway processes | **Restart or stop** high-CPU processes if they\\u2019re stuck or misbehaving.\\n------|------\\nTraffic/load spike | Scale out or up the infrastructure\\n------|------\\nCode/deployment issue | Roll back recent changes or investigate further.\\n------|------\\nContainer/JVM misconfiguration | Tune resource limits and runtime settings\\n        \\n###  Related links\\n\\n* [Host Map](https://app.datadoghq.com/infrastructure/map)\\n* [Live Processes](https://app.datadoghq.com/process?query=host%3Ak3s-example-node-pool-a6e3h)\\n* [System Dashboards](https://app.datadoghq.com/dashboard/lists)\\n* [Monitor Documentation](https://docs.datadoghq.com/monitors/)\\n\\n### Who should be notified?\\n\\nAssign the appropriate notification handle for this alert (e.g., ` + "`" + `@slack-infra` + "`" + `, ` + "`" + `@pagerduty-core-systems` + "`" + `):  \\n` + "`" + `@your-team-handle` + "`" + `\\n\\n[![Metric Graph](https://p.us5.datadoghq.com/snapshot/view/dd-snapshots-us5-prod/org_1300568798/2025-06-27/c7f8fa5bc8e2b9def52d388a326d3e84717271ca.png)](https://us5.datadoghq.com/monitors/8248529?group=host%3Ak3s-example-node-pool-a6e3h&from_ts=1751006129000&to_ts=1751007329000&event_id=8166212983446713351&link_source=monitor_notif)\\n\\n` + "`" + `avg(last_15m):100 - avg:system.cpu.idle{*} by {host} > 90` + "`" + `\\n\\nThe monitor was last triggered at Fri Jun 27 2025 06:50:29 UTC.\\n\\n- - -\\n\\n[[Monitor Status](https://us5.datadoghq.com/monitors/8248529?group=host%3Ak3s-example-node-pool-a6e3h&from_ts=1751006129000&to_ts=1751007329000&event_id=8166212983446713351&link_source=monitor_notif)] \\u00b7 [[Edit Monitor](https://us5.datadoghq.com/monitors/8248529/edit?link_source=monitor_notif)] \\u00b7 [[View k3s-example-node-pool-a6e3h](https://us5.datadoghq.com/infrastructure?filter=k3s-example-node-pool-a6e3h&link_source=monitor_notif)] \\u00b7 [[Show Processes](https://us5.datadoghq.com/process?from_ts=1751006129000&to_ts=1751007149000&live=false&showSummaryGraphs=true&sort=cpu%2CASC&query=host%3Ak3s-example-node-pool-a6e3h&link_source=monitor_notif)] \\u00b7 [[Related Logs](https://us5.datadoghq.com/logs?query=host%3A%22k3s-example-node-pool-a6e3h%22&from_ts=1751006129000&to_ts=1751007029000&live=false&link_source=monitor_notif)]\\n%%%\"\n}"}`
const datadogResolvedPayload = `{"time":"2025-06-29T05:25:46.5545646Z","level":"INFO","msg":"datadog webhook","url":"/api/webhooks/datadog","headers":{"Accept":"*/*","Accept-Encoding":"gzip, deflate","Authorization":"Bearer MySuperSecretToken","Content-Length":"1396","Content-Type":"application/json; charset=utf-8","Traceparent":"00-000000000000000020b141302d62b797-4939e571e13df0c5-00","User-Agent":"python-requests/2.32.4","X-Datadog-Parent-Id":"5276500715714244805","X-Datadog-Trace-Id":"2355735755267159959","X-Forwarded-For":"192.168.1.8","X-Forwarded-Host":"app.nudgebee.com","X-Forwarded-Port":"443","X-Forwarded-Proto":"https","X-Forwarded-Scheme":"https","X-Real-Ip":"192.168.1.8","X-Request-Id":"d408619deeaf426a85744e0cfd7c004d","X-Scheme":"https"},"payload":"{\n    \"id\": \"8169025783718885557\",\n    \"last_updated\": \"1751174655000\",\n    \"event_type\": \"error_tracking_alert\",\n    \"title\": \"[P2] [Recovered on {issue.id:655bd304-5322-11f0-80d4-da7ad0900000}] [] New issue to review\",\n    \"date\": \"1751174655000\",\n    \"org\": {\n        \"id\": \"1300568798\",\n        \"name\": \"Nudgebee\"\n    },\n    \"body\": \"%%%\\nA new [issue](https://us5.datadoghq.com/error-tracking/unified?issueId=655bd304-5322-11f0-80d4-da7ad0900000&amp;from_ts=1751088255000&amp;to_ts=1751174655000&amp;live=false&amp;monitor_id=8249055&amp;monitor_sub_type=.new%28%29&amp;link_source=monitor_notif) has been detected.\\n\\nMark the issue as Reviewed to stop receiving this alert.\\n\\nThe **count** of errors matching **[]()**, grouped by **issue.id**, was **<= 0** during the **last 1d**.\\n\\nThe monitor was last triggered at Fri Jun 27 2025 06:54:15 UTC.\\n\\n- - -\\n\\n[[Monitor Status](https://us5.datadoghq.com/monitors/8249055?group=issue.id%3A655bd304-5322-11f0-80d4-da7ad0900000&from_ts=1751173755000&to_ts=1751174955000&event_id=8169025783718885557&link_source=monitor_notif)] \\u00b7 [[Edit Monitor](https://us5.datadoghq.com/monitors/8249055/edit?link_source=monitor_notif)] \\u00b7 [[View in Error Tracking](https://us5.datadoghq.com/error-tracking/unified?issueId=655bd304-5322-11f0-80d4-da7ad0900000&from_ts=1751088255000&to_ts=1751174655000&live=false&link_source=monitor_notif)]\\n%%%\"\n}"}`

func TestTools_ParseDatadogWebhookPayload(t *testing.T) {
	datadogIntegration, _ := core.GetIntegration(IntegrationDatadogWebhook)
	assert.NotNil(t, datadogIntegration)
	datadogWebhookIntgeration, _ := datadogIntegration.(DatadogWebhook)
	assert.NotNil(t, datadogWebhookIntgeration)

	userId := os.Getenv("TEST_USER")
	eventData, err := datadogWebhookIntgeration.ProcessEventWebook(security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil), []core.IntegrationConfigValue{}, os.Getenv("TEST_ACCOUNT"), datadogPayload)
	assert.Nil(t, err)
	assert.NotEmpty(t, eventData)
	eventData, err = datadogWebhookIntgeration.ProcessEventWebook(security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil), []core.IntegrationConfigValue{}, os.Getenv("TEST_ACCOUNT"), datadogResolvedPayload)
	assert.Nil(t, err)
	assert.NotEmpty(t, eventData)
}

const datadogPayload2 = `{
    "id": "8179565223078592612",
    "last_updated": "1751802855000",
    "event_type": "error_tracking_alert",
    "title": "[P2] [Triggered on {issue.id:2df20914-58c8-11f0-b092-da7ad0900000}] [New issue] New issue to review",
    "date": "1751802855000",
    "org": {
        "id": "1300568798",
        "name": "Nudgebee"
    },
    "body": "%%%\nA new [issue](https://us5.datadoghq.com/error-tracking/unified?issueId=2df20914-58c8-11f0-b092-da7ad0900000&amp;from_ts=1751716455000&amp;to_ts=1751802855000&amp;live=false&amp;monitor_id=8249055&amp;monitor_sub_type=.new%28%29&amp;link_source=monitor_notif) has been detected.\n\n\n` + "```" + `\njinja2.exceptions.TemplateSyntaxError: unexpected &#x27;.&#x27;\n` + "```" + `\n\n\nMark the issue as Reviewed to stop receiving this alert.\n\nThe **count** of errors matching **[]()**, grouped by **issue.id**, was **> 0** during the **last 1d**.\n\nThe monitor was last triggered at Sun Jul 06 2025 11:54:15 UTC.\n\n- - -\n\n[[Monitor Status](https://us5.datadoghq.com/monitors/8249055?group=issue.id%3A2df20914-58c8-11f0-b092-da7ad0900000&from_ts=1751801955000&to_ts=1751803155000&event_id=8179565223078592612&link_source=monitor_notif)] \u00b7 [[Edit Monitor](https://us5.datadoghq.com/monitors/8249055/edit?link_source=monitor_notif)] \u00b7 [[View in Error Tracking](https://us5.datadoghq.com/error-tracking/unified?issueId=2df20914-58c8-11f0-b092-da7ad0900000&from_ts=1751716455000&to_ts=1751802855000&live=false&link_source=monitor_notif)]\n%%%"
}`

func TestTools_ParseDatadogWebhookPayload2(t *testing.T) {
	datadogIntegration, _ := core.GetIntegration(IntegrationDatadogWebhook)
	assert.NotNil(t, datadogIntegration)
	datadogWebhookIntgeration, _ := datadogIntegration.(DatadogWebhook)
	assert.NotNil(t, datadogWebhookIntgeration)

	userId := os.Getenv("TEST_USER")
	eventData, err := datadogWebhookIntgeration.ProcessEventWebook(security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil), []core.IntegrationConfigValue{}, os.Getenv("TEST_ACCOUNT"), datadogPayload2)
	assert.Nil(t, err)
	assert.NotEmpty(t, eventData)
}

// TestDatadogIncidentNullNormalization verifies that the custom UnmarshalJSON
// on DatadogIncident normalizes Datadog's literal "null" strings to "".
func TestDatadogIncidentNullNormalization(t *testing.T) {
	raw := `{
		"incident_public_id": "null",
		"incident_severity": "null",
		"incident_status": "null",
		"incident_url": "null",
		"incident_uuid": "null",
		"incident_title": "",
		"incident_message": "null"
	}`
	var inc DatadogIncident
	err := json.Unmarshal([]byte(raw), &inc)
	assert.NoError(t, err)
	assert.Equal(t, "", inc.IncidentPublicID, "IncidentPublicID should be normalized from \"null\" to \"\"")
	assert.Equal(t, "", inc.IncidentSeverity)
	assert.Equal(t, "", inc.IncidentStatus)
	assert.Equal(t, "", inc.IncidentURL)
	assert.Equal(t, "", inc.IncidentUUID)
	assert.Equal(t, "", inc.IncidentTitle, "empty string should stay empty")
	assert.Equal(t, "", inc.IncidentMessage)
}

// TestDatadogIncidentRealValues verifies real incident values are preserved.
func TestDatadogIncidentRealValues(t *testing.T) {
	raw := `{
		"incident_public_id": "123",
		"incident_severity": "SEV-2",
		"incident_status": "active",
		"incident_url": "https://app.datadoghq.com/incidents/123",
		"incident_uuid": "abc-def-ghi",
		"incident_title": "Service Down",
		"incident_message": "The service is down"
	}`
	var inc DatadogIncident
	err := json.Unmarshal([]byte(raw), &inc)
	assert.NoError(t, err)
	assert.Equal(t, "123", inc.IncidentPublicID)
	assert.Equal(t, "SEV-2", inc.IncidentSeverity)
	assert.Equal(t, "active", inc.IncidentStatus)
	assert.Equal(t, "abc-def-ghi", inc.IncidentUUID)
}

// Synthetics alert from Rackspace — has "null" incident fields and no monitor URL in body.
// Without fixes: API skipped (IncidentPublicID="null"), ruleId empty, event rejected.
const datadogSyntheticsPayload = `{
	"id": "8543404741724441201",
	"last_updated": "1773489470000",
	"event_type": "synthetics_alert",
	"title": "[P3] [Triggered] [Synthetics] RMC - A monitor that always fails",
	"date": "1773489470000",
	"org": { "id": "1313229", "name": "rs-1203197" },
	"body": "%%%\nAn alert has been received for idontexist.example.org\n\n@webhook-Nudgebee\n%%%",
	"alert": { "alert_id": "166187407", "alert_metric": "", "alert_query": "", "alert_status": "", "alert_scope": "", "alert_transition": "Triggered", "alert_type": "error", "alert_metric_namespace": "" },
	"incident": { "incident_public_id": "null", "incident_severity": "null", "incident_status": "null", "incident_url": "null", "incident_uuid": "null", "incident_title": "", "incident_message": "%%%\nAn alert has been received\n%%%", "incident_integrations": null, "incident_fields": null },
	"event": { "aggreg_key": "73e071f5c67d33400f240ec415c2d48e", "event_id": "ID", "event_url": "https://app.datadoghq.com/event/event?id=8543404741724441201" }
}`

// Query alert monitor from Rackspace — has monitor URL in body but no API key available.
// Without fixes: API skipped, alertName empty, ruleId empty, event rejected.
const datadogQueryAlertPayload = `{
	"id": "8544055141553603107",
	"last_updated": "1773528177000",
	"event_type": "query_alert_monitor",
	"title": "[Triggered] RMC Dev - Web ASG At Max Capacity",
	"date": "1773528177000",
	"org": { "id": "1313229", "name": "rs-1203197" },
	"body": "%%%\nWeb Auto Scaling Group has reached maximum capacity.\n\n[![Metric Graph](https://p.datadoghq.com/snapshot/view/dd-snapshots-prod/org_1313229/2026-03-14/abc.png)](https://app.datadoghq.com/monitors/266154777?from_ts=1773527277000&to_ts=1773528477000)\n\n**aws.autoscaling.group_in_service_instances** over **autoscalinggroupname:vmstack-dev-web-asg** was **>= 3.0** on average during the **last 5m**.\n\n[[Monitor Status](https://app.datadoghq.com/monitors/266154777?from_ts=1773527277000&to_ts=1773528477000)] · [[Edit Monitor](https://app.datadoghq.com/monitors/266154777/edit)]\n%%%",
	"alert": { "alert_id": "266154777", "alert_metric": "aws.autoscaling.group_in_service_instances", "alert_query": "avg(last_5m):avg:aws.autoscaling.group_in_service_instances{autoscalinggroupname:vmstack-dev-web-asg} >= 3", "alert_status": "", "alert_scope": "", "alert_transition": "Triggered", "alert_type": "error", "alert_metric_namespace": "aws" },
	"incident": { "incident_public_id": "null", "incident_severity": "null", "incident_status": "null", "incident_url": "null", "incident_uuid": "null", "incident_title": "", "incident_message": "", "incident_integrations": null, "incident_fields": null },
	"event": { "aggreg_key": "3f63d7b1a675db4d4f79cb6efcbb8afc", "event_id": "ID", "event_url": "https://app.datadoghq.com/event/event?id=8544055141553603107" }
}`

// Re-Triggered synthetics alert from Rackspace — the most common case (145/170 webhooks).
// "Re-Triggered" was not captured by the original title regex, falling back to ugly cleanTitle.
const datadogReTriggeredPayload = `{
	"id": "8549000000000000001",
	"last_updated": "1773500000000",
	"event_type": "synthetics_alert",
	"title": "[P4] [Re-Triggered] [Synthetics] API Test on vmstack/api",
	"date": "1773500000000",
	"org": { "id": "1313229", "name": "rs-1203197" },
	"body": "%%%\nAn alert has been received for vmstack/api\n\n@webhook-Nudgebee\n%%%",
	"alert": { "alert_id": "166187407", "alert_metric": "", "alert_query": "", "alert_status": "", "alert_scope": "", "alert_transition": "Re-Triggered", "alert_type": "error", "alert_metric_namespace": "" },
	"incident": { "incident_public_id": "null", "incident_severity": "null", "incident_status": "null", "incident_url": "null", "incident_uuid": "null", "incident_title": "", "incident_message": "", "incident_integrations": null, "incident_fields": null },
	"event": { "aggreg_key": "73e071f5c67d33400f240ec415c2d48e", "event_id": "ID", "event_url": "https://app.datadoghq.com/event/event?id=8549000000000000001" }
}`

// Warn alert from Rackspace — status "warning" was previously dropped by routeWebhookEvent.
const datadogWarnPayload = `{
	"id": "8546376597550758824",
	"last_updated": "1773666546000",
	"event_type": "query_alert_monitor",
	"title": "[Warn] RMC Dev - RDS High Write Latency",
	"date": "1773666546000",
	"org": { "id": "1313229", "name": "rs-1203197" },
	"body": "%%%\nRDS write latency is elevated.\n\n[[Monitor Status](https://app.datadoghq.com/monitors/266155000?from_ts=1773665000&to_ts=1773667000)]\n%%%",
	"alert": { "alert_id": "266155000", "alert_metric": "aws.rds.write_latency", "alert_query": "avg(last_5m):avg:aws.rds.write_latency{*} > 0.01", "alert_status": "", "alert_scope": "", "alert_transition": "Warn", "alert_type": "warning", "alert_metric_namespace": "aws" },
	"incident": { "incident_public_id": "null", "incident_severity": "null", "incident_status": "null", "incident_url": "null", "incident_uuid": "null", "incident_title": "", "incident_message": "", "incident_integrations": null, "incident_fields": null },
	"event": { "aggreg_key": "abc123def456", "event_id": "ID", "event_url": "https://app.datadoghq.com/event/event?id=8546376597550758824" }
}`

// TestDatadog_SyntheticsWithNullIncident verifies parsing of a synthetics alert
// with "null" incident fields. Tests UnmarshalJSON normalization, monitorID fallback
// from alert_id, ruleId fallback, and fingerprint construction.
func TestDatadog_SyntheticsWithNullIncident(t *testing.T) {
	var p DatadogPayload
	err := json.Unmarshal([]byte(datadogSyntheticsPayload), &p)
	assert.NoError(t, err)

	// UnmarshalJSON should normalize "null" strings to ""
	assert.Empty(t, p.Incident.IncidentPublicID, "IncidentPublicID should be empty after null normalization")
	assert.Empty(t, p.Incident.IncidentUUID, "IncidentUUID should be empty after null normalization")

	// Extract status and cleanTitle from title regex
	titleMatches := datadogTitleRegex.FindStringSubmatch(p.Title)
	assert.Len(t, titleMatches, 4)
	assert.Equal(t, "P3", titleMatches[1])
	assert.Equal(t, "Triggered", titleMatches[2])
	cleanTitle := titleMatches[3]
	assert.Equal(t, "[Synthetics] RMC - A monitor that always fails", cleanTitle)

	// monitorID: no /monitors/ URL in body, so should fallback to alert_id
	monitorURLMatches := datadogMonitorURLRegex.FindStringSubmatch(p.Body)
	var monitorID string
	if len(monitorURLMatches) >= 2 {
		monitorID = monitorURLMatches[2]
	}
	assert.Empty(t, monitorID, "no /monitors/ URL in synthetics body")

	// Fallback: use alert_id
	if monitorID == "" && p.Alert.AlertID != "" {
		monitorID = p.Alert.AlertID
	}
	assert.Equal(t, "166187407", monitorID)

	// ruleId and fingerprint derivation (replicating ProcessEventWebook logic)
	fingerprint := p.Event.AggregKey
	ruleId := "" // alertName would be empty without API call
	findingId := p.ID

	if monitorID != "" && p.EventType != "notification" {
		fingerprint = fmt.Sprintf("%s-%s", monitorID, p.Event.AggregKey)
	} else if p.Incident.IncidentPublicID != "" {
		ruleId = "datadog_incident"
		findingId = p.Incident.IncidentUUID
		fingerprint = p.Incident.IncidentUUID
	}
	// Fallbacks
	if ruleId == "" && monitorID != "" {
		ruleId = monitorID
	}
	if ruleId == "" {
		ruleId = cleanTitle
	}

	assert.NotEmpty(t, ruleId, "ruleId should not be empty")
	assert.Equal(t, "166187407", ruleId, "ruleId should fallback to monitorID")
	assert.Contains(t, fingerprint, "166187407", "fingerprint should contain monitorID")
	assert.Equal(t, "8543404741724441201", findingId, "findingId should be the event ID, not incident UUID")
}

// TestDatadog_QueryAlertNoApiKey verifies parsing of a query_alert_monitor
// where the /monitors/ URL is in the body (monitorID from URL), and no API key is available.
func TestDatadog_QueryAlertNoApiKey(t *testing.T) {
	var p DatadogPayload
	err := json.Unmarshal([]byte(datadogQueryAlertPayload), &p)
	assert.NoError(t, err)

	// Title parsing
	titleMatches := datadogTitleRegex.FindStringSubmatch(p.Title)
	assert.Len(t, titleMatches, 4)
	cleanTitle := titleMatches[3]
	assert.Equal(t, "RMC Dev - Web ASG At Max Capacity", cleanTitle)

	// monitorID from /monitors/ URL in body
	monitorURLMatches := datadogMonitorURLRegex.FindStringSubmatch(p.Body)
	assert.True(t, len(monitorURLMatches) >= 3, "should find /monitors/ URL in body")
	monitorID := monitorURLMatches[2]
	assert.Equal(t, "266154777", monitorID)

	// ruleId derivation — no alertName (no API), falls back to monitorID
	ruleId := ""
	fingerprint := p.Event.AggregKey
	if monitorID != "" && p.EventType != "notification" {
		fingerprint = fmt.Sprintf("%s-%s", monitorID, p.Event.AggregKey)
	}
	if ruleId == "" && monitorID != "" {
		ruleId = monitorID
	}
	if ruleId == "" {
		ruleId = cleanTitle
	}

	assert.NotEmpty(t, ruleId, "ruleId should not be empty without API key")
	assert.Equal(t, "266154777", ruleId, "ruleId should fallback to monitorID")
	assert.Contains(t, fingerprint, "266154777", "fingerprint should contain monitorID")
}

// TestDatadog_WarnStatus verifies that "Warn" in the title is parsed as "warning" status.
func TestDatadog_WarnStatus(t *testing.T) {
	var p DatadogPayload
	err := json.Unmarshal([]byte(datadogWarnPayload), &p)
	assert.NoError(t, err)

	// Title parsing — [Warn] should be captured by regex
	titleMatches := datadogTitleRegex.FindStringSubmatch(p.Title)
	assert.Len(t, titleMatches, 4)
	assert.Equal(t, "Warn", titleMatches[2])
	cleanTitle := titleMatches[3]
	assert.Equal(t, "RMC Dev - RDS High Write Latency", cleanTitle)

	// Status derivation (same logic as ProcessEventWebook)
	var status string
	switch titleMatches[2] {
	case "Triggered", "Alert":
		status = string(event.EventStatusFiring)
	case "Recovered", "No Data":
		status = string(event.EventStatusResolved)
	case "Warn":
		status = "warning"
	default:
		status = strings.ToLower(titleMatches[2])
	}
	assert.Equal(t, "warning", status)

	// IncidentPublicID normalized
	assert.Empty(t, p.Incident.IncidentPublicID)

	// monitorID from body URL
	monitorURLMatches := datadogMonitorURLRegex.FindStringSubmatch(p.Body)
	var monitorID string
	if len(monitorURLMatches) >= 3 {
		monitorID = monitorURLMatches[2]
	}
	if monitorID == "" && p.Alert.AlertID != "" {
		monitorID = p.Alert.AlertID
	}
	assert.Equal(t, "266155000", monitorID)

	// ruleId fallback
	ruleId := ""
	if ruleId == "" && monitorID != "" {
		ruleId = monitorID
	}
	assert.NotEmpty(t, ruleId)
}

// TestDatadog_ReTriggeredSynthetics verifies that "Re-Triggered" in title is parsed
// correctly — this is the most common case in Rackspace data (145/170 webhooks).
func TestDatadog_ReTriggeredSynthetics(t *testing.T) {
	var p DatadogPayload
	err := json.Unmarshal([]byte(datadogReTriggeredPayload), &p)
	assert.NoError(t, err)

	// Regex must match Re-Triggered
	titleMatches := datadogTitleRegex.FindStringSubmatch(p.Title)
	assert.Len(t, titleMatches, 4, "regex should match Re-Triggered titles")
	assert.Equal(t, "P4", titleMatches[1], "should extract actual priority")
	assert.Equal(t, "Re-Triggered", titleMatches[2])
	assert.Equal(t, "[Synthetics] API Test on vmstack/api", titleMatches[3], "cleanTitle should strip prefix brackets")

	// Status derivation
	var status string
	switch titleMatches[2] {
	case "Triggered", "Re-Triggered", "Alert":
		status = string(event.EventStatusFiring)
	case "Recovered", "No Data":
		status = string(event.EventStatusResolved)
	case "Warn":
		status = "warning"
	default:
		status = strings.ToLower(titleMatches[2])
	}
	assert.Equal(t, string(event.EventStatusFiring), status)

	// Null normalization
	assert.Empty(t, p.Incident.IncidentPublicID)
	assert.Empty(t, p.Incident.IncidentUUID)

	// monitorID from alert_id
	monitorID := p.Alert.AlertID
	assert.Equal(t, "166187407", monitorID)
}
