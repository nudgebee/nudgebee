package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

const azureMonitorPayload = `{"schemaId":"azureMonitorCommonAlertSchema","data":{"essentials":{"alertId":"/subscriptions/19e207a9-769d-4afd-b261-10bbed2d43e8/providers/Microsoft.AlertsManagement/alerts/62bd7dd6-0869-4656-8c23-0c5e2866f000","alertRule":"CPU 5 Percent","targetResourceType":"microsoft.compute/virtualmachines","alertRuleID":"/subscriptions/19e207a9-769d-4afd-b261-10bbed2d43e8/resourceGroups/aks-dev-rg/providers/microsoft.insights/metricAlerts/CPU 5 Percent","severity":"Sev0","signalType":"Metric","monitorCondition":"Fired","targetResourceGroup":"aks-dev-rg","monitoringService":"Platform","alertTargetIDs":["/subscriptions/19e207a9-769d-4afd-b261-10bbed2d43e8/resourcegroups/aks-dev-rg/providers/microsoft.compute/virtualmachines/nudgebee-dev"],"configurationItems":["nudgebee-dev"],"originAlertId":"19e207a9-769d-4afd-b261-10bbed2d43e8_aks-dev-rg_microsoft.insights_metricAlerts_CPU 5 Percent_1112063553","firedDateTime":"2025-10-30T07:51:43.209438Z","description":"","essentialsVersion":"1.0","alertContextVersion":"1.0","investigationLink":"https://portal.azure.com/#view/Microsoft_Azure_Monitoring_Alerts/Issue.ReactView/alertId/%2fsubscriptions%2f19e207a9-769d-4afd-b261-10bbed2d43e8%2fresourceGroups%2faks-dev-rg%2fproviders%2fMicrosoft.AlertsManagement%2falerts%2f62bd7dd6-0869-4656-8c23-0c5e2866f000"},"alertContext":{"properties":null,"conditionType":"MultipleResourceMultipleMetricCriteria","condition":{"windowSize":"PT5M","allOf":[{"metricName":"Percentage CPU","metricNamespace":"Microsoft.Compute/virtualMachines","operator":"GreaterThan","threshold":"5","timeAggregation":"Total","dimensions":[],"metricValue":587.24,"webTestName":null}],"staticThresholdFailingPeriods":{"numberOfEvaluationPeriods":0,"minFailingPeriodsToAlert":0},"windowStartTime":"2025-10-30T07:44:32.326Z","windowEndTime":"2025-10-30T07:49:32.326Z"}},"customProperties":null}}`

func TestTools_ParseAzureMonitorWebhookPayload(t *testing.T) {
	azureMonitorIntegration, _ := core.GetIntegration(IntegrationAzureMonitorWebhook)
	assert.NotNil(t, azureMonitorIntegration)
	azureMonitorWebhookIntgeration, _ := azureMonitorIntegration.(AzureMonitorWebhook)
	assert.NotNil(t, azureMonitorWebhookIntgeration)

	userId := os.Getenv("TEST_USER")
	eventData, err := azureMonitorWebhookIntgeration.ProcessEventWebook(security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil), []core.IntegrationConfigValue{}, os.Getenv("TEST_ACCOUNT"), azureMonitorPayload)
	assert.Nil(t, err)
	assert.NotEmpty(t, eventData)
}
