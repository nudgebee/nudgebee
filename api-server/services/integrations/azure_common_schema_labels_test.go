package integrations

import (
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Shape of the body Zenduty archives for a Log Alerts V2 rule split by
// Namespace and ControllerName (Container Insights). Identifiers are synthetic;
// field set and nesting match a captured payload.
const azureLogAlertStoredPayloadTmpl = `{
  "payload": {
    "schemaId": "azureMonitorCommonAlertSchema",
    "data": {
      "essentials": {
        "alertId": "/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.AlertsManagement/alerts/{{ALERT_ID}}",
        "alertRule": "pod-restarts",
        "targetResourceType": "microsoft.operationalinsights/workspaces",
        "alertRuleID": "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/example-rg/providers/microsoft.insights/scheduledqueryrules/pod-restarts",
        "severity": "Sev2",
        "signalType": "Log",
        "monitorCondition": "Fired",
        "targetResourceGroup": "example-rg",
        "monitoringService": "Log Alerts V2",
        "alertTargetIDs": ["/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/example-rg/providers/microsoft.operationalinsights/workspaces/example-law"],
        "configurationItems": ["/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/example-rg/providers/Microsoft.OperationalInsights/workspaces/example-law"],
        "originAlertId": "5bc994c6-c6cc-144a-8da5-f95109a55fac",
        "firedDateTime": "2026-10-14T10:44:54.0242683Z",
        "description": "Pod restarts, split per workload",
        "essentialsVersion": "1.0",
        "alertContextVersion": "1.0",
        "investigationLink": "https://portal.azure.com/example",
        "region": "centralindia"
      },
      "alertContext": {
        "properties": null,
        "conditionType": "LogQueryCriteria",
        "condition": {
          "windowSize": "PT15M",
          "allOf": [{
            "searchQuery": "KubePodInventory | summarize Restarts=max(ContainerRestartCount) by Namespace, ControllerName | where Restarts > 0",
            "metricMeasureColumn": "Restarts",
            "targetResourceTypes": null,
            "operator": "GreaterThan",
            "threshold": "0",
            "timeAggregation": "Maximum",
            "dimensions": [
              {"name": "ControllerName", "value": "{{CONTROLLER}}"},
              {"name": "Namespace", "value": "{{NAMESPACE}}"}
            ],
            "metricValue": 1,
            "failingPeriods": {"numberOfEvaluationPeriods": 1, "minFailingPeriodsToAlert": 1},
            "linkToSearchResultsUI": "https://portal.azure.com/example",
            "event": null
          }],
          "windowStartTime": "2026-10-14T10:29:20Z",
          "windowEndTime": "2026-10-14T10:44:20Z"
        }
      },
      "customProperties": null
    }
  },
  "metadata": {"application": "Microsoft Azure Monitoring"}
}`

// A platform metric alert on an AKS cluster: no dimensions, so no subject.
const azureMetricAlertStoredPayload = `{
  "payload": {
    "schemaId": "azureMonitorCommonAlertSchema",
    "data": {
      "essentials": {
        "alertId": "/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.AlertsManagement/alerts/2e3c908f-a50f-433c-a911-6dd13e2af000",
        "alertRule": "node-cpu-high",
        "severity": "Sev2",
        "signalType": "Metric",
        "monitorCondition": "Fired",
        "monitoringService": "Platform",
        "alertTargetIDs": ["/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/example-rg/providers/microsoft.containerservice/managedclusters/example-aks"]
      },
      "alertContext": {
        "conditionType": "SingleResourceMultipleMetricCriteria",
        "condition": {
          "windowSize": "PT5M",
          "allOf": [{
            "metricName": "node_cpu_usage_percentage",
            "metricNamespace": "microsoft.containerservice/managedclusters",
            "operator": "GreaterThan",
            "threshold": "40",
            "timeAggregation": "Average",
            "dimensions": null,
            "metricValue": 49.4,
            "webTestName": null
          }],
          "staticThresholdFailingPeriods": {"numberOfEvaluationPeriods": 1, "minFailingPeriodsToAlert": 1}
        }
      },
      "customProperties": null
    }
  }
}`

func azureLogAlertPayload(alertID, controller, namespace string) []byte {
	r := strings.NewReplacer("{{ALERT_ID}}", alertID, "{{CONTROLLER}}", controller, "{{NAMESPACE}}", namespace)
	return []byte(r.Replace(azureLogAlertStoredPayloadTmpl))
}

func TestDecodeZendutyStoredPayload_AzureLogAlert(t *testing.T) {
	labels, schema, err := decodeZendutyStoredPayload(
		azureLogAlertPayload("f9f62e76-968c-b1c0-36d3-ab9389930024", "checkout-6f665f49dd", "shop"))
	require.NoError(t, err)

	assert.Equal(t, azureCommonAlertSchemaID, schema)
	assert.Equal(t, "pod-restarts", labels["alertname"], "the rule, not Zenduty's md5 entity_id")
	assert.Equal(t, "azure", labels["nb_alert_source"])
	assert.Equal(t, "Sev2", labels["nb_alert_severity"])
	assert.Empty(t, labels["severity"], "Sev2 must not remap the incident urgency")
	assert.Equal(t, "Fired", labels["monitor_condition"])
	assert.Equal(t, "checkout", labels["subject_name"], "ReplicaSet resolved to its Deployment")
	assert.Equal(t, "shop", labels["subject_namespace"])
	assert.Equal(t, "shop", labels["namespace"])
	assert.Equal(t, "checkout-6f665f49dd", labels[core.WorkloadCandidatesLabel], "raw ReplicaSet kept as a fallback candidate")
	assert.Equal(t, "checkout-6f665f49dd", labels["controllername"], "dimension passes through as a label")
	assert.True(t, strings.HasPrefix(labels["nb_alert_fingerprint"], "nbfp:"))
}

// The point of the fingerprint: Azure mints a new alertId for every alert
// instance, and a rollout mints a new ReplicaSet — neither is a new alert.
func TestAzureFingerprint_StableAcrossInstancesAndRollouts(t *testing.T) {
	fp := func(alertID, controller, namespace string) string {
		labels, _, err := decodeZendutyStoredPayload(azureLogAlertPayload(alertID, controller, namespace))
		require.NoError(t, err)
		return labels["nb_alert_fingerprint"]
	}

	first := fp("f9f62e76-968c-b1c0-36d3-ab9389930024", "checkout-6f665f49dd", "shop")
	assert.Equal(t, first, fp("d87649c9-c6e4-48c1-3b45-9846f15c0024", "checkout-6f665f49dd", "shop"), "new alert instance")
	assert.Equal(t, first, fp("d87649c9-c6e4-48c1-3b45-9846f15c0024", "checkout-7c75686877", "shop"), "after a rollout")

	assert.NotEqual(t, first, fp("f9f62e76-968c-b1c0-36d3-ab9389930024", "payments-f5678b6dd", "shop"), "different workload")
	assert.NotEqual(t, first, fp("f9f62e76-968c-b1c0-36d3-ab9389930024", "checkout-6f665f49dd", "billing"), "same name, other namespace")
}

func TestDecodeZendutyStoredPayload_AzureMetricAlertWithoutDimensions(t *testing.T) {
	labels, schema, err := decodeZendutyStoredPayload([]byte(azureMetricAlertStoredPayload))
	require.NoError(t, err)

	assert.Equal(t, azureCommonAlertSchemaID, schema)
	assert.Equal(t, "node-cpu-high", labels["alertname"])
	assert.Contains(t, labels["azure_alert_target_resource"], "managedclusters/example-aks")
	assert.Empty(t, labels["subject_name"], "no dimension names a workload")
	assert.NotEmpty(t, labels["nb_alert_fingerprint"])
}

func TestDecodeZendutyStoredPayload_AzureWithoutRuleIsLabelless(t *testing.T) {
	_, _, err := decodeZendutyStoredPayload([]byte(`{"payload": {"schemaId": "azureMonitorCommonAlertSchema", "data": {"essentials": {}}}}`))
	assert.Error(t, err)
}

// Regression guard for the Prometheus path: an Alertmanager body must decode to
// exactly what alertmanagerPayloadToLabels produced before the schema branch.
func TestDecodeZendutyStoredPayload_AlertmanagerUnchanged(t *testing.T) {
	body := `{
		"alerts": [{
			"labels": {"alertname": "HighP95Latency", "destination_workload_name": "relay-server-64945c7bc6", "destination_workload_namespace": "platform", "severity": "warning"},
			"annotations": {"summary": "High P95 latency for relay-server"},
			"generatorURL": "http://vmalert:8080/vmalert/alert?group_id=1&alert_id=2",
			"fingerprint": "4cde56d671cfaf25"
		}],
		"commonLabels": {"alertname": "HighP95Latency", "destination_workload_name": "relay-server-64945c7bc6", "destination_workload_namespace": "platform", "severity": "warning"},
		"commonAnnotations": {"summary": "High P95 latency for relay-server"}
	}`
	var p alertmanagerPayload
	require.NoError(t, json.Unmarshal([]byte(body), &p))

	labels, schema, err := decodeZendutyStoredPayload([]byte(`{"payload": ` + body + `}`))
	require.NoError(t, err)

	assert.Empty(t, schema)
	assert.Equal(t, alertmanagerPayloadToLabels(p), labels)
	assert.Equal(t, "4cde56d671cfaf25", labels["nb_alert_fingerprint"])
}

func TestDecodeZendutyStoredPayload_EmptyPayloadIsLabelless(t *testing.T) {
	_, _, err := decodeZendutyStoredPayload([]byte(`{"payload": null}`))
	assert.Error(t, err)
	_, _, err = decodeZendutyStoredPayload([]byte(`{}`))
	assert.Error(t, err)
}

// Azure payload labels must beat the alerts API's md5 entity_id and the
// user-chosen integration name, while a webhook-summary value still wins.
func TestZendutyEnrich_Apply_AzurePayloadBeatsEntityID(t *testing.T) {
	payload, _, err := decodeZendutyStoredPayload(
		azureLogAlertPayload("f9f62e76-968c-b1c0-36d3-ab9389930024", "checkout-6f665f49dd", "shop"))
	require.NoError(t, err)

	alert := &core.EventIncomingWebhookInvestigation{Labels: map[string]string{"team": "from-webhook"}}
	payload["team"] = "from-payload"
	applyZendutyEnrichResult(alert, zendutyEnrichResult{
		EntityID:        "783c8bc5d79e91a5222b059d1b881787",
		IntegrationName: "Azure Monitor (poc)",
		PayloadLabels:   payload,
		PayloadSchema:   azureCommonAlertSchemaID,
	})

	assert.Equal(t, "pod-restarts", alert.Labels["alertname"])
	assert.Equal(t, "azure", alert.Labels["nb_alert_source"])
	assert.Equal(t, "783c8bc5d79e91a5222b059d1b881787", alert.Labels["nb_alert_entity_id"], "entity id still recorded")
	assert.Equal(t, "Azure Monitor (poc)", alert.Labels["nb_zenduty_integration_name"])
	assert.Equal(t, "from-webhook", alert.Labels["team"], "webhook summary values stay authoritative")
	assert.Equal(t, "true", alert.Labels["nb_zenduty_payload_recovered"])
}

// The Alertmanager order is unchanged: entity_id (the clean alertname for
// Prometheus-family integrations) still wins over the payload.
func TestZendutyEnrich_Apply_AlertmanagerPrecedenceUnchanged(t *testing.T) {
	alert := &core.EventIncomingWebhookInvestigation{Labels: map[string]string{}}
	applyZendutyEnrichResult(alert, zendutyEnrichResult{
		EntityID:        "KubePodNotReady",
		IntegrationName: "vmalert",
		PayloadLabels:   map[string]string{"alertname": "SomethingElse", "nb_alert_source": "other", "namespace": "platform"},
	})

	assert.Equal(t, "KubePodNotReady", alert.Labels["alertname"])
	assert.Equal(t, "prometheus", alert.Labels["nb_alert_source"])
	assert.Equal(t, "platform", alert.Labels["namespace"])
}

func TestZendutyMD5EntityID(t *testing.T) {
	assert.True(t, zendutyMD5EntityID.MatchString("783c8bc5d79e91a5222b059d1b881787"))
	assert.True(t, zendutyMD5EntityID.MatchString("783C8BC5D79E91A5222B059D1B881787"))
	for _, name := range []string{"KubePodNotReady", "pod-restarts", "783c8bc5d79e91a5222b059d1b88178"} {
		assert.False(t, zendutyMD5EntityID.MatchString(name), name)
	}
}
