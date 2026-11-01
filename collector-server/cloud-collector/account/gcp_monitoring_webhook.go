package account

import (
	"encoding/json"
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
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

// GCPWebhookEvent is the event row polling would store for a GCP Monitoring incident.
// api-server ingests webhook deliveries itself but takes their identity from here, so a
// pushed incident and a polled one agree on every field enrichment, dedup and resolve
// key on.
type GCPWebhookEvent struct {
	Title            string            `json:"title"`
	Description      string            `json:"description"`
	AggregationKey   string            `json:"aggregation_key"`
	SubjectName      string            `json:"subject_name"`
	SubjectType      string            `json:"subject_type"`
	SubjectNamespace string            `json:"subject_namespace"`
	ServiceKey       string            `json:"service_key"`
	Fingerprint      string            `json:"fingerprint"`
	FindingId        string            `json:"finding_id"`
	CloudResourceId  string            `json:"cloud_resource_id"`
	Priority         string            `json:"priority"`
	Status           string            `json:"status"`
	Labels           map[string]string `json:"labels"`
}

// NormalizeGCPMonitoringWebhook converts a GCP Monitoring webhook body into the event row
// polling would store for the same incident, linked to its cloud resource the same way.
// It stores nothing.
func NormalizeGCPMonitoringWebhook(ctx *security.RequestContext, accountId string, body []byte) (GCPWebhookEvent, error) {
	account, provider, err := getAccount(ctx, accountId)
	if err != nil {
		return GCPWebhookEvent{}, err
	}
	if provider != "GCP" {
		return GCPWebhookEvent{}, fmt.Errorf("account %s is not a GCP account", accountId)
	}

	event, err := gcloud.ConvertWebhookIncident(ctx, account, body)
	if err != nil {
		return GCPWebhookEvent{}, err
	}
	row, err := gcpWebhookEventRow(ctx, event, account, accountId)
	if err != nil {
		return GCPWebhookEvent{}, err
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return GCPWebhookEvent{}, err
	}
	linkCloudResourceIds(ctx, dbms, []map[string]any{row}, accountId)
	return gcpWebhookEventFromRow(row)
}

// gcpWebhookEventRow runs the polling path's own row mapping rather than restating it,
// so the two cannot drift apart again.
func gcpWebhookEventRow(ctx *security.RequestContext, event providers.Event, account providers.Account, accountId string) (map[string]any, error) {
	row, err := prepareEventForDB(ctx, event, account, accountId)
	if err != nil {
		return nil, err
	}
	// Set by insertNewEvents, not prepareEventForDB.
	row["finding_id"] = event.FiringFindingID()
	return row, nil
}

func gcpWebhookEventFromRow(row map[string]any) (GCPWebhookEvent, error) {
	// Round-trip through JSON: the row holds pointers (description, cloud_resource_id)
	// and typed enums.
	raw, err := json.Marshal(row)
	if err != nil {
		return GCPWebhookEvent{}, err
	}
	var out GCPWebhookEvent
	if err := json.Unmarshal(raw, &out); err != nil {
		return GCPWebhookEvent{}, err
	}
	return out, nil
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
