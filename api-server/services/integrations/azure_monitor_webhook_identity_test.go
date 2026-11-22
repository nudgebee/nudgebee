package integrations

import (
	"net/http"
	"nudgebee/services/integrations/core"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const azureAlertId = "/subscriptions/19e207a9-769d-4afd-b261-10bbed2d43e8/providers/Microsoft.AlertsManagement/alerts/62bd7dd6-0869-4656-8c23-0c5e2866f000"

// What the collector names the azureMonitorPayload firing, the way polling stores it.
var sampleNormalizedAzureVM = collectorNormalizedEvent{
	Title:            "CPU 5 Percent",
	AggregationKey:   "CPU 5 Percent",
	SubjectName:      "/subscriptions/19e207a9-769d-4afd-b261-10bbed2d43e8/resourcegroups/aks-dev-rg/providers/microsoft.compute/virtualmachines/test-cluster-dev",
	SubjectType:      "virtualmachines",
	SubjectNamespace: "microsoft.compute/virtualmachines",
	ServiceKey:       "azure:19e207a9:aks-dev-rg:microsoft.compute/virtualmachines:virtualmachines:test-cluster-dev",
	Fingerprint:      "/subscriptions/19e207a9-769d-4afd-b261-10bbed2d43e8/resourceGroups/aks-dev-rg/providers/microsoft.insights/metricAlerts/CPU 5 Percent:/subscriptions/19e207a9-769d-4afd-b261-10bbed2d43e8/resourcegroups/aks-dev-rg/providers/microsoft.compute/virtualmachines/test-cluster-dev",
	FindingId:        "/subscriptions/19e207a9-769d-4afd-b261-10bbed2d43e8/providers/microsoft.alertsmanagement/alerts/62bd7dd6-0869-4656-8c23-0c5e2866f000",
	CloudResourceId:  "5b0c2f0e-0000-4000-8000-000000000042",
	Priority:         "HIGH",
	Status:           "FIRING",
}

func TestNormalizeAzureMonitorAlertAsksCollector(t *testing.T) {
	var got http.Request
	var gotBody string
	fakeCollector(t, http.StatusOK, map[string]any{"data": sampleNormalizedAzureVM}, &got, &gotBody)

	n, err := normalizeByCollector(testTenantRequestContext(), "normalize_azure_monitor_webhook", "acc-1", azureMonitorPayload)
	require.NoError(t, err)

	assert.Equal(t, "/v1/cloud/normalize_azure_monitor_webhook", got.URL.Path)
	assert.Equal(t, "acc-1", got.URL.Query().Get("account_id"))
	assert.Equal(t, "secret", got.Header.Get("x-collector-token"))
	assert.Equal(t, "tenant-1", got.Header.Get("x-tenant-id"))
	assert.JSONEq(t, azureMonitorPayload, gotBody, "the delivery is forwarded unchanged")
	assert.Equal(t, sampleNormalizedAzureVM, n)
}

func TestApplyCollectorIdentityNamesTheAlertAsPollingDoes(t *testing.T) {
	webhook := core.EventIncomingWebhook{
		WebhookId:             "azure-monitor-" + azureAlertId,
		EventId:               azureAlertId,
		EventTitle:            "Azure Monitor Alert: CPU 5 Percent",
		EventDescription:      "## Alert Description\n...",
		EventSubjectNamespace: "microsoft.compute/virtualmachines",
		EventSubjectName:      "test-cluster-dev",
		Investigation: core.EventIncomingWebhookInvestigation{
			Fingerprint: azureAlertId,
			Labels:      map[string]string{"alert_id": azureAlertId},
		},
	}

	setAzureFiringKey(&webhook, azureAlertId)
	applyCollectorIdentity(&webhook, sampleNormalizedAzureVM)

	assert.Equal(t, sampleNormalizedAzureVM.FindingId, webhook.EventId, "finding_id")
	assert.Equal(t, sampleNormalizedAzureVM.Fingerprint, webhook.Investigation.Fingerprint)
	assert.Equal(t, sampleNormalizedAzureVM.SubjectName, webhook.EventSubjectName)
	assert.Equal(t, sampleNormalizedAzureVM.SubjectNamespace, webhook.EventSubjectNamespace)
	assert.Equal(t, sampleNormalizedAzureVM.SubjectType, webhook.EventSubjectKind)
	assert.Equal(t, sampleNormalizedAzureVM.Title, webhook.EventTitle)
	assert.Equal(t, sampleNormalizedAzureVM.ServiceKey, webhook.ServiceKey)
	assert.Equal(t, sampleNormalizedAzureVM.CloudResourceId, webhook.CloudResourceId)
	assert.Equal(t, azureAlertId, webhook.LegacyFingerprint, "events opened under the old alertId fingerprint still close")

	// Webhook-only context is kept.
	assert.Equal(t, "## Alert Description\n...", webhook.EventDescription)
	assert.Equal(t, azureAlertId, webhook.Investigation.Labels["alert_id"])
	assert.Equal(t, "azure-monitor-"+azureAlertId, webhook.WebhookId)
}

// With the collector down, the delivery is still keyed by the finding_id polling
// stores, so it collapses with its polled twin and a resolve still closes it;
// only fingerprint and subject keep the webhook's own naming.
func TestSetAzureFiringKeyWithoutCollector(t *testing.T) {
	webhook := core.EventIncomingWebhook{
		EventId:       azureAlertId,
		Investigation: core.EventIncomingWebhookInvestigation{Fingerprint: azureAlertId},
	}

	setAzureFiringKey(&webhook, azureAlertId)

	assert.Equal(t, sampleNormalizedAzureVM.FindingId, webhook.EventId, "the lowercased subscription-scoped alertId is the canonical finding_id")
	assert.Equal(t, azureAlertId, webhook.LegacyFingerprint, "events stored under alertId in Azure's casing still close")
	assert.Equal(t, azureAlertId, webhook.Investigation.Fingerprint, "fingerprint is left to the collector")
}
