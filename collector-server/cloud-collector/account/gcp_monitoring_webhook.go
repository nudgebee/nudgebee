package account

import (
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers/gcloud"
	"nudgebee/collector/cloud/security"
	"time"
)

// CheckGCPMonitoringPermission checks whether the stored GCP credentials for the given account
// have Cloud Monitoring notification channel permissions.
func CheckGCPMonitoringPermission(ctx *security.RequestContext, accountId string) (bool, string, error) {
	account, _, err := getAccount(ctx, accountId)
	if err != nil {
		return false, "", err
	}
	if account.AccessSecret == nil {
		return false, "", fmt.Errorf("no credentials found for account %s", accountId)
	}

	decrypted, err := common.Decrypt(*account.AccessSecret)
	if err != nil {
		return false, "", fmt.Errorf("failed to decrypt credentials for account %s: %w", accountId, err)
	}

	creds := common.GCPCredentials{
		CredentialsJSON: decrypted,
		ProjectID:       account.AccountNumber,
	}
	hasPermission, errorDetail := common.CheckGCPCloudMonitoringPermission(ctx.GetContext(), creds)
	return hasPermission, errorDetail, nil
}

// SetupGCPMonitoringWebhook sets up a webhook notification channel for a GCP account
// and attaches it to all alert policies.
func SetupGCPMonitoringWebhook(ctx *security.RequestContext, accountId, webhookUrl string) (string, error) {
	account, _, err := getAccount(ctx, accountId)
	if err != nil {
		return "", err
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()
	return gcloud.SetupWebhookNotificationChannel(ctx, account, tenantId, webhookUrl)
}

// SyncGCPMonitoringWebhooks syncs webhook notification channels for all GCP accounts
// that have them configured. Called by the daily cron.
func SyncGCPMonitoringWebhooks(ctx *security.RequestContext) {
	t0 := time.Now()
	ctx.GetLogger().Info("webhook-sync: starting sync for all GCP accounts with webhooks")

	accountIds, err := gcloud.GetGCPAccountsWithWebhook()
	if err != nil {
		ctx.GetLogger().Error("webhook-sync: failed to get accounts with webhooks", "error", err)
		return
	}

	if len(accountIds) == 0 {
		ctx.GetLogger().Info("webhook-sync: no accounts with webhooks found")
		return
	}

	ctx.GetLogger().Info("webhook-sync: found accounts with webhooks", "count", len(accountIds))

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("webhook-sync: failed to get database manager", "error", err)
		return
	}

	successCount := 0
	failCount := 0

	for _, accountId := range accountIds {
		// Fetch account with tenant
		var tenantId string
		err := dbms.QueryRowAndScan(&tenantId,
			"SELECT tenant::text FROM cloud_accounts WHERE id = $1 AND status = 'active'",
			accountId)
		if err != nil {
			ctx.GetLogger().Warn("webhook-sync: failed to get tenant for account", "accountId", accountId, "error", err)
			failCount++
			continue
		}

		account, _, err := getAccount(ctx, accountId)
		if err != nil {
			ctx.GetLogger().Warn("webhook-sync: failed to get account", "accountId", accountId, "error", err)
			failCount++
			continue
		}

		if err := gcloud.SyncWebhookNotificationChannel(ctx, account, tenantId); err != nil {
			ctx.GetLogger().Warn("webhook-sync: failed to sync webhook for account", "accountId", accountId, "error", err)
			failCount++
			continue
		}

		successCount++
	}

	ctx.GetLogger().Info("webhook-sync: completed",
		"total", len(accountIds),
		"success", successCount,
		"failed", failCount,
		"duration", time.Since(t0).String())
}
