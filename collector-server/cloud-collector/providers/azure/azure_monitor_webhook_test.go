package azure

import (
	"context"
	"encoding/json"
	"nudgebee/collector/cloud/providers"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertsmanagement/armalertsmanagement"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One firing of a Cosmos DB metric alert, as both paths see it. The shape follows
// the production pair that was stored twice: the webhook names the firing under
// its subscription, the Alerts Management list under its target resource.
const (
	parityRule   = "/subscriptions/28ebc387-0000-4000-8000-000000000001/resourceGroups/rg-cosmos-aiops-demo/providers/microsoft.insights/metricAlerts/alert-normalized-ru-high"
	parityTarget = "/subscriptions/28ebc387-0000-4000-8000-000000000001/resourcegroups/rg-cosmos-aiops-demo/providers/microsoft.documentdb/databaseaccounts/cosmos-aiops-7k8ubf"
	parityGUID   = "8ed832be-0d19-4977-a14d-8983a2edf000"
)

func parityPolledAlert(condition armalertsmanagement.MonitorCondition) *armalertsmanagement.Alert {
	return &armalertsmanagement.Alert{
		ID:   to.Ptr(parityTarget + "/providers/Microsoft.AlertsManagement/alerts/" + parityGUID),
		Name: to.Ptr("alert-normalized-ru-high"),
		Properties: &armalertsmanagement.AlertProperties{Essentials: &armalertsmanagement.Essentials{
			AlertRule:           to.Ptr(parityRule),
			TargetResource:      to.Ptr(parityTarget),
			TargetResourceGroup: to.Ptr("rg-cosmos-aiops-demo"),
			TargetResourceType:  to.Ptr("microsoft.documentdb/databaseaccounts"),
			Severity:            to.Ptr(armalertsmanagement.SeveritySev2),
			MonitorCondition:    to.Ptr(condition),
			SignalType:          to.Ptr(armalertsmanagement.SignalTypeMetric),
			MonitorService:      to.Ptr(armalertsmanagement.MonitorServicePlatform),
			StartDateTime:       to.Ptr(time.Date(2026, 10, 23, 10, 59, 18, 0, time.UTC)),
		}},
	}
}

func parityWebhookBody(t *testing.T, condition, alertRuleID string) []byte {
	t.Helper()
	essentials := map[string]any{
		"alertId":           "/subscriptions/28ebc387-0000-4000-8000-000000000001/providers/Microsoft.AlertsManagement/alerts/" + parityGUID,
		"alertRule":         "alert-normalized-ru-high",
		"severity":          "Sev2",
		"signalType":        "Metric",
		"monitorCondition":  condition,
		"monitoringService": "Platform",
		"alertTargetIDs":    []string{parityTarget},
		"firedDateTime":     "2026-10-23T10:59:18.000Z",
		"essentialsVersion": "1.0",
	}
	if alertRuleID != "" {
		essentials["alertRuleID"] = alertRuleID
	}
	body, err := json.Marshal(map[string]any{
		"schemaId": "azureMonitorCommonAlertSchema",
		"data":     map[string]any{"essentials": essentials, "alertContext": map[string]any{}},
	})
	require.NoError(t, err)
	return body
}

// An account without stored credentials (a webhook-only one) cannot read the
// firing back, so these exercise the payload mapping.
func noCredsAccount(t *testing.T) providers.Account {
	t.Helper()
	for _, key := range []string{"AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET", "AZURE_TENANT_ID", "AZURE_SUBSCRIPTION_ID"} {
		t.Setenv(key, "")
	}
	return providers.Account{}
}

func TestConvertMonitorWebhookAlertMatchesPolledFiring(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	polledAlert := parityPolledAlert(armalertsmanagement.MonitorConditionFired)
	polled := convertAzureAlertToEvent("28ebc387-0000-4000-8000-000000000001", polledAlert, polledAlert.Properties.Essentials)

	pushed, err := ConvertMonitorWebhookAlert(ctx, noCredsAccount(t), parityWebhookBody(t, "Fired", parityRule))
	require.NoError(t, err)

	assert.Equal(t, polled.FiringFindingID(), pushed.FiringFindingID(), "finding_id: the events unique index collapses the two arrivals on it")
	assert.Equal(t, polled.EventId, pushed.EventId, "fingerprint")
	assert.Equal(t, polled.ResourceId, pushed.ResourceId, "subject")
	assert.Equal(t, polled.Title, pushed.Title)
	assert.Equal(t, polled.EventName, pushed.EventName)
	assert.Equal(t, polled.ResourceServiceName, pushed.ResourceServiceName)
	assert.Equal(t, polled.ResourceType, pushed.ResourceType)
	assert.Equal(t, polled.ResourceRegion, pushed.ResourceRegion)
	assert.Equal(t, polled.EventSeverity, pushed.EventSeverity)
	assert.True(t, polled.Date.Equal(pushed.Date), "starts_at")
	assert.Equal(t, polled.EventSource, pushed.EventSource)
	assert.Equal(t, providers.EventStatusFiring, pushed.EventStatus)
}

// The resolution of a firing is the same firing: it must carry the finding_id
// and fingerprint its open row was stored under.
func TestConvertMonitorWebhookAlertResolvedKeepsIdentity(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	account := noCredsAccount(t)

	fired, err := ConvertMonitorWebhookAlert(ctx, account, parityWebhookBody(t, "Fired", parityRule))
	require.NoError(t, err)
	resolved, err := ConvertMonitorWebhookAlert(ctx, account, parityWebhookBody(t, "Resolved", parityRule))
	require.NoError(t, err)

	assert.Equal(t, providers.EventStatusResolved, resolved.EventStatus)
	assert.Equal(t, fired.FiringFindingID(), resolved.FiringFindingID())
	assert.Equal(t, fired.EventId, resolved.EventId)
}

// Without alertRuleID the payload cannot rebuild polling's fingerprint, but the
// finding_id still matches, which is what keeps the firing to one row.
func TestConvertMonitorWebhookAlertWithoutRuleIDStillMatchesFinding(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	polledAlert := parityPolledAlert(armalertsmanagement.MonitorConditionFired)
	polled := convertAzureAlertToEvent("28ebc387-0000-4000-8000-000000000001", polledAlert, polledAlert.Properties.Essentials)

	pushed, err := ConvertMonitorWebhookAlert(ctx, noCredsAccount(t), parityWebhookBody(t, "Fired", ""))
	require.NoError(t, err)

	assert.Equal(t, polled.FiringFindingID(), pushed.FiringFindingID())
	assert.Equal(t, polled.ResourceId, pushed.ResourceId)
}

func TestConvertMonitorWebhookAlertRejectsBadPayloads(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	account := noCredsAccount(t)

	_, err := ConvertMonitorWebhookAlert(ctx, account, []byte(`{"schemaId":"other","data":{"essentials":{"alertId":"x"}}}`))
	assert.Error(t, err)
	_, err = ConvertMonitorWebhookAlert(ctx, account, []byte(`{"schemaId":"azureMonitorCommonAlertSchema","data":{"essentials":{}}}`))
	assert.Error(t, err)
	_, err = ConvertMonitorWebhookAlert(ctx, account, []byte(`not json`))
	assert.Error(t, err)
}

func TestCanonicalAzureAlertFindingID(t *testing.T) {
	want := "/subscriptions/28ebc387-0000-4000-8000-000000000001/providers/microsoft.alertsmanagement/alerts/" + parityGUID

	// Polled: listed under its target resource.
	assert.Equal(t, want, canonicalAzureAlertFindingID("28ebc387-0000-4000-8000-000000000001",
		parityTarget+"/providers/Microsoft.AlertsManagement/alerts/"+parityGUID))
	// Pushed: subscription-scoped, subscription taken from the id itself.
	assert.Equal(t, want, canonicalAzureAlertFindingID("",
		"/subscriptions/28ebc387-0000-4000-8000-000000000001/providers/Microsoft.AlertsManagement/alerts/"+parityGUID))
	// Casing does not split a firing.
	assert.Equal(t, want, canonicalAzureAlertFindingID("28EBC387-0000-4000-8000-000000000001",
		"/SUBSCRIPTIONS/28EBC387-0000-4000-8000-000000000001/PROVIDERS/MICROSOFT.ALERTSMANAGEMENT/ALERTS/"+"8ED832BE-0D19-4977-A14D-8983A2EDF000"))
}

// Polling keeps the old resource-scoped id as LegacyFindingId, so an alert that is
// open when this ships is matched under it and is neither closed nor stored again.
func TestConvertAzureAlertToEventKeepsLegacyFindingID(t *testing.T) {
	alert := parityPolledAlert(armalertsmanagement.MonitorConditionFired)
	event := convertAzureAlertToEvent("28ebc387-0000-4000-8000-000000000001", alert, alert.Properties.Essentials)

	assert.Equal(t, []string{
		"/subscriptions/28ebc387-0000-4000-8000-000000000001/providers/microsoft.alertsmanagement/alerts/" + parityGUID,
		*alert.ID,
	}, event.StoredFindingIDs())
}

// A delivery captured from a real action group (api-server's azureMonitorPayload
// fixture), against the firing as the Alerts Management list returns it: the
// alertRule and targetResource casing polling sees, the id under its target.
const capturedWebhookDelivery = `{"schemaId":"azureMonitorCommonAlertSchema","data":{"essentials":{"alertId":"/subscriptions/19e207a9-769d-4afd-b261-10bbed2d43e8/providers/Microsoft.AlertsManagement/alerts/62bd7dd6-0869-4656-8c23-0c5e2866f000","alertRule":"CPU 5 Percent","targetResourceType":"microsoft.compute/virtualmachines","alertRuleID":"/subscriptions/19e207a9-769d-4afd-b261-10bbed2d43e8/resourceGroups/aks-dev-rg/providers/microsoft.insights/metricAlerts/CPU 5 Percent","severity":"Sev0","signalType":"Metric","monitorCondition":"Fired","targetResourceGroup":"aks-dev-rg","monitoringService":"Platform","alertTargetIDs":["/subscriptions/19e207a9-769d-4afd-b261-10bbed2d43e8/resourcegroups/aks-dev-rg/providers/microsoft.compute/virtualmachines/test-cluster-dev"],"configurationItems":["test-cluster-dev"],"originAlertId":"19e207a9-769d-4afd-b261-10bbed2d43e8_aks-dev-rg_microsoft.insights_metricAlerts_CPU 5 Percent_1112063553","firedDateTime":"2025-10-30T07:51:43.209438Z","description":"","essentialsVersion":"1.0","alertContextVersion":"1.0"},"alertContext":{},"customProperties":null}}`

func TestConvertMonitorWebhookAlertMatchesPolledFiringForCapturedDelivery(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	sub := "19e207a9-769d-4afd-b261-10bbed2d43e8"
	target := "/subscriptions/" + sub + "/resourcegroups/aks-dev-rg/providers/microsoft.compute/virtualmachines/test-cluster-dev"
	polledAlert := &armalertsmanagement.Alert{
		ID:   to.Ptr(target + "/providers/Microsoft.AlertsManagement/alerts/62bd7dd6-0869-4656-8c23-0c5e2866f000"),
		Name: to.Ptr("CPU 5 Percent"),
		Properties: &armalertsmanagement.AlertProperties{Essentials: &armalertsmanagement.Essentials{
			AlertRule:           to.Ptr("/subscriptions/" + sub + "/resourceGroups/aks-dev-rg/providers/microsoft.insights/metricAlerts/CPU 5 Percent"),
			TargetResource:      to.Ptr(target),
			TargetResourceGroup: to.Ptr("aks-dev-rg"),
			TargetResourceType:  to.Ptr("microsoft.compute/virtualmachines"),
			Severity:            to.Ptr(armalertsmanagement.SeveritySev0),
			MonitorCondition:    to.Ptr(armalertsmanagement.MonitorConditionFired),
			StartDateTime:       to.Ptr(time.Date(2025, 10, 30, 7, 51, 43, 209438000, time.UTC)),
		}},
	}
	polled := convertAzureAlertToEvent(sub, polledAlert, polledAlert.Properties.Essentials)

	pushed, err := ConvertMonitorWebhookAlert(ctx, noCredsAccount(t), []byte(capturedWebhookDelivery))
	require.NoError(t, err)

	assert.Equal(t, polled.FiringFindingID(), pushed.FiringFindingID())
	assert.Equal(t, polled.EventId, pushed.EventId)
	assert.Equal(t, polled.ResourceId, pushed.ResourceId)
	assert.Equal(t, polled.Title, pushed.Title)
	assert.Equal(t, polled.EventSeverity, pushed.EventSeverity)
	assert.True(t, polled.Date.Equal(pushed.Date))
}

// A webhook-only account has no credentials, so its lookup reports
// errNoAlertReadAccess and is logged quietly; the delivery still converts.
func TestFetchAzureAlertWithoutCredentialsReportsNoReadAccess(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	_, err := fetchAzureAlert(ctx, noCredsAccount(t), "28ebc387-0000-4000-8000-000000000001",
		"/subscriptions/28ebc387-0000-4000-8000-000000000001/providers/Microsoft.AlertsManagement/alerts/"+parityGUID)
	assert.ErrorIs(t, err, errNoAlertReadAccess)

	// A malformed id is a real failure, not a missing-credentials one.
	_, err = fetchAzureAlert(ctx, noCredsAccount(t), "", "/subscriptions/x/providers/Microsoft.AlertsManagement/alerts/not-a-guid")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, errNoAlertReadAccess)
}
