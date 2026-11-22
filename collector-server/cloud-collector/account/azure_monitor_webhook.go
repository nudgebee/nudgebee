package account

import (
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers/azure"
	"nudgebee/collector/cloud/security"
)

// NormalizeAzureMonitorWebhook converts an Azure Monitor webhook body into the event
// row polling would store for the same firing, linked to its cloud resource the
// same way. It stores nothing.
func NormalizeAzureMonitorWebhook(ctx *security.RequestContext, accountId string, body []byte) (WebhookEvent, error) {
	account, provider, err := getAccount(ctx, accountId)
	if err != nil {
		return WebhookEvent{}, err
	}
	if provider != "Azure" {
		return WebhookEvent{}, fmt.Errorf("account %s is not an Azure account", accountId)
	}

	event, err := azure.ConvertMonitorWebhookAlert(ctx, account, body)
	if err != nil {
		return WebhookEvent{}, err
	}
	row, err := webhookEventRow(ctx, event, account, accountId)
	if err != nil {
		return WebhookEvent{}, err
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return WebhookEvent{}, err
	}
	linkCloudResourceIds(ctx, dbms, []map[string]any{row}, accountId)
	return webhookEventFromRow(row)
}
