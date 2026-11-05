package integrations

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

// maxWebhookLabelHelpRows keeps the list short enough to read in the form's
// tooltip. It names the common labels, not every label a parser can set.
const maxWebhookLabelHelpRows = 8

func incidentWebhookIntegrations() []core.Integration {
	var out []core.Integration
	for _, integration := range core.RegisteredIntegrations() {
		if integration.Category() == core.IntegrationCategoryIncidentWebhook {
			out = append(out, integration)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Every incident webhook has to say which label names it produces: the form has no
// other way to tell a user what to type into account mapping.
func TestIncidentWebhooksDeclareLabelHelp(t *testing.T) {
	webhooks := incidentWebhookIntegrations()
	require.NotEmpty(t, webhooks)

	for _, integration := range webhooks {
		name := integration.Name()
		t.Run(name, func(t *testing.T) {
			describer, ok := integration.(core.WebhookLabelDescriber)
			require.True(t, ok, "%s is an incident webhook and must implement WebhookLabelHelp() next to its parser", name)

			help := describer.WebhookLabelHelp()
			assert.NotEmpty(t, strings.TrimSpace(help.Summary), "summary")
			assert.LessOrEqual(t, len(help.Labels), maxWebhookLabelHelpRows, "list the common labels only")
			assert.LessOrEqual(t, len(help.Notes), 4, "notes")
			// workflow_webhook forwards the body to the workflow server and builds no event.
			if name != IntegrationGenericWebhook {
				assert.NotEmpty(t, help.Labels, "a webhook that produces labels must list them")
			}

			seen := map[string]bool{}
			for _, row := range help.Labels {
				assert.NotEmpty(t, strings.TrimSpace(row.Label), "label")
				assert.NotEmpty(t, strings.TrimSpace(row.Source), "source of %q", row.Label)
				assert.False(t, seen[row.Label], "label %q is listed twice", row.Label)
				seen[row.Label] = true
			}

			// The form is also served on partner-branded hosts, so the text must not
			// name the product.
			text := strings.ToLower(help.Summary + " " + strings.Join(help.Notes, " "))
			for _, row := range help.Labels {
				text += " " + strings.ToLower(row.Source)
			}
			assert.NotContains(t, text, "nudgebee")
		})
	}
}

func TestIntegrationConfigsCarriesWebhookLabelHelp(t *testing.T) {
	schema, err := core.IntegrationConfigs(nil, IntegrationElasticsearchWebhook, "")
	require.NoError(t, err)
	require.NotNil(t, schema.WebhookLabelHelp)
	assert.NotEmpty(t, schema.WebhookLabelHelp.Labels)

	// Wire names the form reads.
	raw, err := json.Marshal(schema)
	require.NoError(t, err)
	var decoded struct {
		Help struct {
			Summary string `json:"summary"`
			Labels  []struct {
				Label  string `json:"label"`
				Source string `json:"source"`
			} `json:"labels"`
			Notes []string `json:"notes"`
		} `json:"webhook_label_help"`
	}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, schema.WebhookLabelHelp.Summary, decoded.Help.Summary)
	require.NotEmpty(t, decoded.Help.Labels)
	assert.Equal(t, schema.WebhookLabelHelp.Labels[0].Label, decoded.Help.Labels[0].Label)

	// A non-webhook integration carries none.
	other, err := core.IntegrationConfigs(nil, IntegrationPagerDuty, "")
	require.NoError(t, err)
	assert.Nil(t, other.WebhookLabelHelp)
}

// processedLabels runs a webhook's ProcessEventWebook on one payload and returns the
// labels of the single event it yields. The event-rule write the parsers start in
// the background needs a database and only logs without one.
func processedLabels(t *testing.T, webhook core.EventIncomingTroubleshootWebhookIntegration, payload string) map[string]string {
	t.Helper()
	sc := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	got, err := singleEvent(webhook.ProcessEventWebook(sc, nil, "acct-1", payload))
	require.NoError(t, err)
	return got.Investigation.Labels
}

const webhookLabelHelpAlertmanagerBody = `{
	"status": "firing", "externalURL": "https://alertmanager.example.com",
	"alerts": [{
		"status": "firing",
		"labels": {"alertname": "HighErrorRate", "severity": "critical", "namespace": "payments", "deployment": "checkout-api"},
		"annotations": {"summary": "Checkout error rate above 5%", "runbook_url": "https://runbooks.example.com/high-error-rate"},
		"startsAt": "2026-06-01T10:00:00Z", "endsAt": "0001-01-01T00:00:00Z",
		"generatorURL": "https://alerts.example.com/graph", "fingerprint": "3f2c9a1b7d4e6a80"
	}]
}`

// webhookLabelHelpProducers runs a webhook's real label-building code on a sample
// payload. Only webhooks whose labels depend on the payload alone, and whose
// parser can run without a database or the provider's API, are here.
var webhookLabelHelpProducers = map[string]func(t *testing.T) map[string]string{
	IntegrationPrometheusAlertManagerWebhook: func(t *testing.T) map[string]string {
		return processedLabels(t, PrometheusAlertManagerWebhook{}, webhookLabelHelpAlertmanagerBody)
	},
	IntegrationCubeAPMWebhook: func(t *testing.T) map[string]string {
		return processedLabels(t, CubeAPMWebhook{}, webhookLabelHelpAlertmanagerBody)
	},
	IntegrationSigNozWebhook: func(t *testing.T) map[string]string {
		stubSigNozRuleRegistration(t)
		return processedLabels(t, SigNozWebhook{}, `{
			"status": "firing", "externalURL": "https://signoz.example.com",
			"alerts": [{
				"status": "firing",
				"labels": {"alertname": "HighMemory", "severity": "critical", "ruleId": "r-1",
					"ruleSource": "https://signoz.example.com/alerts/overview?ruleId=r-1",
					"k8s.namespace.name": "shop", "k8s.deployment.name": "checkout", "k8s.pod.name": "checkout-1"},
				"annotations": {"summary": "memory high", "runbook_url": "https://runbooks.example.com/memory"},
				"startsAt": "2026-10-15T09:50:30Z", "endsAt": "0001-01-01T00:00:00Z",
				"generatorURL": "https://signoz.example.com/alerts/overview?ruleId=r-1", "fingerprint": "fp-1"
			}]
		}`)
	},
	IntegrationSplunkWebhook: func(t *testing.T) map[string]string {
		return processedLabels(t, SplunkWebhook{}, `{
			"search_name": "Checkout errors", "app": "search", "owner": "admin", "sid": "scheduler__admin__1",
			"results_link": "https://splunk.example.com/app/search/@go?sid=scheduler__admin__1",
			"result": {"host": "web-1", "source": "/var/log/checkout.log", "index": "main", "sourcetype": "checkout"}
		}`)
	},
	IntegrationElasticsearchWebhook: func(t *testing.T) map[string]string {
		payload := canonicalKibanaPayload()
		payload["alert"].(map[string]any)["uuid"] = "6f1c2a9e-0b7d-4c1a-9e55-3d2b8f4a7c10" // Kibana 8.8+
		got, err := mapElasticsearchAlertToEvent("acct", payload)
		require.NoError(t, err)
		return got.Investigation.Labels
	},
	IntegrationEventWebhook: func(t *testing.T) map[string]string {
		items, verr := parseEventWebhookPayload(`{
			"id": "alert-1", "fingerprint": "fp-1", "status": "firing", "title": "Checkout errors",
			"rule_name": "checkout-errors", "source_tool": "uptime-kuma", "labels": {"team": "payments"}
		}`)
		require.Nil(t, verr)
		require.Len(t, items, 1)
		return buildEventWebhookEvent("integration-1", "acct", items[0]).Investigation.Labels
	},
	IntegrationGrafanaWebhook: func(t *testing.T) map[string]string {
		got, err := mapGrafanaAlertToEvent("acct", map[string]any{
			"status":       "firing",
			"fingerprint":  "abc123",
			"labels":       map[string]any{"alertname": "HighCPU", "pod": "api-0"},
			"generatorURL": "https://grafana.example/alerting/abc",
			"dashboardURL": "https://grafana.example/d/abc",
			"panelURL":     "https://grafana.example/d/abc?viewPanel=1",
			"silenceURL":   "https://grafana.example/alerting/silence/new",
		})
		require.NoError(t, err)
		return got.Investigation.Labels
	},
	IntegrationOpenObserveWebhook: func(t *testing.T) map[string]string {
		got, _, err := mapOpenObserveAlertToEvent("acct", decodeOpenObservePayload(t, canonicalOpenObserveBody))
		require.NoError(t, err)
		return got.Investigation.Labels
	},
	IntegrationSolarWindsWebhook: func(t *testing.T) map[string]string {
		var entityAlert, logAlert SolarWindsWebhookPayload
		require.NoError(t, json.Unmarshal([]byte(`{
			"type": "entity-metric-raw", "name": "High CPU", "severity": "CRITICAL", "priority": "HIGH",
			"condition": "cpu > 90",
			"entities": [{"type": "KubernetesPod", "entityId": "e-1", "displayName": "api-0"}]
		}`), &entityAlert))
		require.NoError(t, json.Unmarshal([]byte(`{
			"type": "generic-event-raw", "name": "Error logs", "severity": "WARNING", "priority": "LOW",
			"events": [{"hostName": "node-1", "applicationName": "api"}]
		}`), &logAlert))

		_, _, _, labels := extractSolarWindsEntityInfo(entityAlert)
		for k, v := range extractSolarWindsGenericEventInfo(logAlert) {
			if _, exists := labels[k]; !exists {
				labels[k] = v
			}
		}
		return labels
	},
}

// webhookLabelHelpUnchecked names the webhooks whose help text is NOT checked
// against the parser, and why. Text for these is only as good as its last review.
var webhookLabelHelpUnchecked = map[string]string{
	IntegrationGenericWebhook:       "produces no labels",
	IntegrationGCPMonitoringWebhook: "labels are built by cloud-collector, which ProcessEventWebook calls",
	IntegrationAzureMonitorWebhook:  "labels are built inside ProcessEventWebook, which resolves the account from the database",
	IntegrationServicenowWebhook:    "labels are built inside ProcessEventWebook and extended from the ServiceNow API",
	IntegrationDynatraceWebhook:     "labels depend on the Dynatrace API",
	IntegrationNewRelicWebhook:      "labels depend on the New Relic API",
	IntegrationDatadogWebhook:       "labels depend on the Datadog API",
	IntegrationPagerdutyWebhook:     "labels depend on the PagerDuty API",
	IntegrationZendutyWebhook:       "labels depend on the Zenduty API",
}

// A label the help text promises must be one the parser really sets: a rule built
// on a name that does not exist never matches, and the alert lands on the default
// account with no warning.
func TestWebhookLabelHelpMatchesParser(t *testing.T) {
	for _, integration := range incidentWebhookIntegrations() {
		name := integration.Name()
		produce, checked := webhookLabelHelpProducers[name]
		_, unchecked := webhookLabelHelpUnchecked[name]
		require.True(t, checked != unchecked,
			"%s must be in exactly one of webhookLabelHelpProducers or webhookLabelHelpUnchecked", name)
		if !checked {
			continue
		}

		t.Run(name, func(t *testing.T) {
			labels := produce(t)
			help := integration.(core.WebhookLabelDescriber).WebhookLabelHelp()
			for _, row := range help.Labels {
				// `<alert label>`, `tag_<name>`: a family of names, not one literal.
				if strings.Contains(row.Label, "<") {
					continue
				}
				assert.Contains(t, labels, row.Label, "help lists %q but the parser did not set it", row.Label)
			}
		})
	}
}
