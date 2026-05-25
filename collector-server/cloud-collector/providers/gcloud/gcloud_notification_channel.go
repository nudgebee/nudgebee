package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/api/cloudresourcemanager/v3"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	nudgebeeChannelDisplayName = "Nudgebee Monitoring Alerts"
	nudgebeeChannelType        = "webhook_tokenauth"
)

// SetupWebhookNotificationChannel creates a webhook notification channel in GCP
// and attaches it to all enabled alert policies. If the channel already exists,
// it reuses it and ensures all policies have it attached.
// tenantId is passed separately because Account struct doesn't carry it.
func SetupWebhookNotificationChannel(ctx providers.CloudProviderContext, account providers.Account, tenantId, webhookUrl string) (string, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return "", fmt.Errorf("failed to get gcloud session: %w", err)
	}

	channelClient, err := monitoring.NewNotificationChannelClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return "", fmt.Errorf("failed to create notification channel client: %w", err)
	}
	defer func() { _ = channelClient.Close() }()

	// Check if channel already exists
	channelName, err := findExistingNudgebeeChannel(ctx, channelClient, session.ProjectId)
	if err != nil {
		ctx.GetLogger().Warn("failed to check existing channels, will attempt create", "error", err)
	}

	if channelName == "" {
		// Create new notification channel
		channel := &monitoringpb.NotificationChannel{
			Type:        nudgebeeChannelType,
			DisplayName: nudgebeeChannelDisplayName,
			Labels: map[string]string{
				"url": webhookUrl,
			},
			Enabled: wrapperspb.Bool(true),
		}

		created, err := channelClient.CreateNotificationChannel(ctx.GetContext(), &monitoringpb.CreateNotificationChannelRequest{
			Name:                fmt.Sprintf("projects/%s", session.ProjectId),
			NotificationChannel: channel,
		})
		if err != nil {
			return "", fmt.Errorf("failed to create notification channel: %w", err)
		}
		channelName = created.GetName()
		ctx.GetLogger().Info("created notification channel", "channelName", channelName, "projectId", session.ProjectId)
	} else {
		// Existing channel found — ensure its URL label matches the current webhook URL
		existing, err := channelClient.GetNotificationChannel(ctx.GetContext(), &monitoringpb.GetNotificationChannelRequest{
			Name: channelName,
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to get existing channel for URL check", "channelName", channelName, "error", err)
		} else if existing.GetLabels()["url"] != webhookUrl {
			existing.Labels["url"] = webhookUrl
			_, err = channelClient.UpdateNotificationChannel(ctx.GetContext(), &monitoringpb.UpdateNotificationChannelRequest{
				NotificationChannel: existing,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to update channel URL", "channelName", channelName, "error", err)
			} else {
				ctx.GetLogger().Info("updated existing notification channel URL", "channelName", channelName, "projectId", session.ProjectId)
			}
		}
		ctx.GetLogger().Info("found existing notification channel", "channelName", channelName, "projectId", session.ProjectId)
	}

	// Attach channel to all alert policies
	attached, err := attachChannelToAllPolicies(ctx, session, channelName)
	if err != nil {
		ctx.GetLogger().Warn("failed to attach channel to some policies", "error", err)
		// Don't fail — channel was created successfully
	}
	ctx.GetLogger().Info("attached channel to alert policies", "attached", attached, "projectId", session.ProjectId)

	// Store channel reference in agent table
	if err := storeWebhookChannelRef(account.ID, tenantId, channelName); err != nil {
		ctx.GetLogger().Warn("failed to store webhook channel ref", "error", err)
	}

	return channelName, nil
}

// SyncWebhookNotificationChannel verifies the channel still exists and attaches
// it to any new alert policies that were created since setup.
func SyncWebhookNotificationChannel(ctx providers.CloudProviderContext, account providers.Account, tenantId string) error {
	channelName, err := getStoredWebhookChannelRef(account.ID, tenantId)
	if err != nil || channelName == "" {
		// No webhook configured for this account — skip
		return nil
	}

	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return fmt.Errorf("failed to get gcloud session: %w", err)
	}

	channelClient, err := monitoring.NewNotificationChannelClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return fmt.Errorf("failed to create notification channel client: %w", err)
	}
	defer func() { _ = channelClient.Close() }()

	// Verify channel still exists
	_, err = channelClient.GetNotificationChannel(ctx.GetContext(), &monitoringpb.GetNotificationChannelRequest{
		Name: channelName,
	})
	if err != nil {
		ctx.GetLogger().Warn("notification channel no longer exists, clearing ref", "channelName", channelName, "error", err)
		_ = clearWebhookChannelRef(account.ID, tenantId)
		return fmt.Errorf("notification channel %s no longer exists: %w", channelName, err)
	}

	// Attach to any new policies
	attached, err := attachChannelToAllPolicies(ctx, session, channelName)
	if err != nil {
		return fmt.Errorf("failed to sync channel to policies: %w", err)
	}

	// Update agent status with last sync time
	if err := updateWebhookAgentStatus(account.ID, tenantId); err != nil {
		ctx.GetLogger().Warn("failed to update webhook agent status", "error", err)
	}

	ctx.GetLogger().Info("synced webhook notification channel", "channelName", channelName, "newPoliciesAttached", attached, "projectId", session.ProjectId)
	return nil
}

// findExistingNudgebeeChannel looks for an existing Nudgebee webhook channel in the project.
func findExistingNudgebeeChannel(ctx providers.CloudProviderContext, client *monitoring.NotificationChannelClient, projectId string) (string, error) {
	req := &monitoringpb.ListNotificationChannelsRequest{
		Name: fmt.Sprintf("projects/%s", projectId),
	}

	it := client.ListNotificationChannels(ctx.GetContext(), req)
	for {
		channel, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return "", fmt.Errorf("failed to list notification channels: %w", err)
		}

		if channel.GetDisplayName() == nudgebeeChannelDisplayName && channel.GetType() == nudgebeeChannelType {
			return channel.GetName(), nil
		}
	}
	return "", nil
}

// attachChannelToAllPolicies attaches the notification channel to all enabled
// alert policies that don't already have it. Returns the number of policies
// updated and the number that failed.
func attachChannelToAllPolicies(ctx providers.CloudProviderContext, session gcloudAuthSession, channelName string) (int, error) {
	policyClient, err := monitoring.NewAlertPolicyClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return 0, fmt.Errorf("failed to create alert policy client: %w", err)
	}
	defer func() { _ = policyClient.Close() }()

	req := &monitoringpb.ListAlertPoliciesRequest{
		Name: fmt.Sprintf("projects/%s", session.ProjectId),
	}

	attached := 0
	failed := 0
	it := policyClient.ListAlertPolicies(ctx.GetContext(), req)
	for {
		policy, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return attached, fmt.Errorf("failed to list alert policies: %w", err)
		}

		// Skip disabled policies
		if policy.GetEnabled() != nil && !policy.GetEnabled().GetValue() {
			continue
		}

		// Check if channel is already attached
		alreadyAttached := false
		for _, ch := range policy.GetNotificationChannels() {
			if ch == channelName {
				alreadyAttached = true
				break
			}
		}
		if alreadyAttached {
			continue
		}

		// Append channel and update policy
		policy.NotificationChannels = append(policy.NotificationChannels, channelName)
		_, err = policyClient.UpdateAlertPolicy(ctx.GetContext(), &monitoringpb.UpdateAlertPolicyRequest{
			AlertPolicy: policy,
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to attach channel to policy", "policyName", policy.GetDisplayName(), "error", err)
			failed++
			continue
		}
		attached++
	}

	if failed > 0 {
		return attached, fmt.Errorf("failed to attach channel to %d policy(ies)", failed)
	}
	return attached, nil
}

// storeWebhookChannelRef persists the channel resource name in the main GCP
// agent row's connection_status JSONB under the "gcp_monitoring_webhook" key.
func storeWebhookChannelRef(accountId, tenantId, channelName string) error {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("webhook agent: failed to get dbms: %w", err)
	}

	now := time.Now()
	timestamp := now.Format(time.RFC3339)

	var tenantIdParam interface{}
	if tenantId == "" {
		tenantIdParam = nil
	} else {
		tenantIdParam = tenantId
	}

	query := `UPDATE agent
		SET updated_at = $1,
			connection_status = jsonb_set(
				COALESCE(connection_status, '{}'::jsonb),
				'{gcp_monitoring_webhook}',
				jsonb_build_object(
					'channel_name', to_jsonb($4::text),
					'setup_at', to_jsonb($1::text),
					'last_sync_at', to_jsonb($1::text)
				)
			)
		WHERE cloud_account_id = $2 AND tenant IS NOT DISTINCT FROM $3 AND type = 'GCP'`

	result, err := dbms.Exec(query, timestamp, accountId, tenantIdParam, channelName)
	if err != nil {
		return fmt.Errorf("webhook agent: failed to update main GCP agent: %w", err)
	}
	if rowsAffected, _ := result.RowsAffected(); rowsAffected == 0 {
		return fmt.Errorf("webhook agent: no main GCP agent row found for cloud_account_id=%s", accountId)
	}
	return nil
}

// updateWebhookAgentStatus updates the last sync timestamp in the main GCP agent row.
func updateWebhookAgentStatus(accountId, tenantId string) error {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("webhook agent status: failed to get dbms: %w", err)
	}

	now := time.Now()
	timestamp := now.Format(time.RFC3339)

	var tenantIdParam interface{}
	if tenantId == "" {
		tenantIdParam = nil
	} else {
		tenantIdParam = tenantId
	}

	query := `UPDATE agent
		SET updated_at = $1,
			last_synced_at = $1,
			connection_status = jsonb_set(
				COALESCE(connection_status, '{}'::jsonb),
				'{gcp_monitoring_webhook,last_sync_at}',
				to_jsonb($1::text)
			)
		WHERE cloud_account_id = $2 AND tenant IS NOT DISTINCT FROM $3 AND type = 'GCP'`

	_, err = dbms.Exec(query, timestamp, accountId, tenantIdParam)
	return err
}

// getStoredWebhookChannelRef retrieves the stored channel name from the main GCP agent row.
func getStoredWebhookChannelRef(accountId, tenantId string) (string, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", fmt.Errorf("webhook agent: failed to get dbms: %w", err)
	}

	var tenantIdParam interface{}
	if tenantId == "" {
		tenantIdParam = nil
	} else {
		tenantIdParam = tenantId
	}

	var connectionStatusStr string
	err = dbms.QueryRowAndScan(&connectionStatusStr,
		`SELECT connection_status::text FROM agent WHERE cloud_account_id = $1 AND tenant IS NOT DISTINCT FROM $2 AND type = 'GCP'`,
		accountId, tenantIdParam)
	if err != nil {
		return "", err
	}

	var connectionStatus map[string]any
	if err := common.UnmarshalJson([]byte(connectionStatusStr), &connectionStatus); err != nil {
		return "", err
	}

	webhookStatus, ok := connectionStatus["gcp_monitoring_webhook"].(map[string]any)
	if !ok {
		return "", nil
	}

	channelName, _ := webhookStatus["channel_name"].(string)
	return channelName, nil
}

// clearWebhookChannelRef removes the gcp_monitoring_webhook key from the main GCP agent's connection_status.
func clearWebhookChannelRef(accountId, tenantId string) error {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return err
	}

	var tenantIdParam interface{}
	if tenantId == "" {
		tenantIdParam = nil
	} else {
		tenantIdParam = tenantId
	}

	_, err = dbms.Exec(
		`UPDATE agent SET connection_status = connection_status - 'gcp_monitoring_webhook'
		 WHERE cloud_account_id = $1 AND tenant IS NOT DISTINCT FROM $2 AND type = 'GCP'`,
		accountId, tenantIdParam)
	return err
}

// GetGCPAccountsWithWebhook returns account IDs that have a webhook notification channel configured.
func GetGCPAccountsWithWebhook() ([]string, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, err
	}

	rows, err := dbms.Query(
		`SELECT cloud_account_id FROM agent
		 WHERE type = 'GCP' AND status = 'CONNECTED'
		   AND jsonb_exists(connection_status, 'gcp_monitoring_webhook')`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var accountIds []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		accountIds = append(accountIds, id)
	}
	return accountIds, rows.Err()
}

// CheckMonitoringEditorPermission checks if the SA has the permissions needed
// to create notification channels and attach them to alert policies.
func CheckMonitoringEditorPermission(ctx providers.CloudProviderContext, account providers.Account) bool {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return false
	}

	crmService, err := cloudresourcemanager.NewService(ctx.GetContext(), session.Opts...)
	if err != nil {
		return false
	}

	requiredPermissions := []string{
		"monitoring.notificationChannels.create",
		"monitoring.notificationChannels.update",
		"monitoring.alertPolicies.update",
	}

	resp, err := crmService.Projects.TestIamPermissions(
		fmt.Sprintf("projects/%s", session.ProjectId),
		&cloudresourcemanager.TestIamPermissionsRequest{
			Permissions: requiredPermissions,
		},
	).Context(ctx.GetContext()).Do()
	if err != nil {
		ctx.GetLogger().Warn("failed to check monitoring permissions via TestIamPermissions", "error", err)
		return false
	}

	granted := make(map[string]bool, len(resp.Permissions))
	for _, p := range resp.Permissions {
		granted[p] = true
	}
	for _, p := range requiredPermissions {
		if !granted[p] {
			return false
		}
	}
	return true
}

// RemoveWebhookNotificationChannel removes the Nudgebee webhook notification channel
// from the GCP project and detaches it from all alert policies.
func RemoveWebhookNotificationChannel(ctx providers.CloudProviderContext, account providers.Account, tenantId string) error {
	channelName, err := getStoredWebhookChannelRef(account.ID, tenantId)
	if err != nil || channelName == "" {
		return nil // Nothing to remove
	}

	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return fmt.Errorf("failed to get gcloud session: %w", err)
	}

	// Detach channel from all policies first
	policyClient, err := monitoring.NewAlertPolicyClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return fmt.Errorf("failed to create alert policy client: %w", err)
	}
	defer func() { _ = policyClient.Close() }()

	req := &monitoringpb.ListAlertPoliciesRequest{
		Name: fmt.Sprintf("projects/%s", session.ProjectId),
	}
	it := policyClient.ListAlertPolicies(ctx.GetContext(), req)
	for {
		policy, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			break
		}

		// Remove channel from policy's notification channels
		var filtered []string
		for _, ch := range policy.GetNotificationChannels() {
			if ch != channelName {
				filtered = append(filtered, ch)
			}
		}
		if len(filtered) != len(policy.GetNotificationChannels()) {
			policy.NotificationChannels = filtered
			_, _ = policyClient.UpdateAlertPolicy(ctx.GetContext(), &monitoringpb.UpdateAlertPolicyRequest{
				AlertPolicy: policy,
			})
		}
	}

	// Delete the channel
	channelClient, err := monitoring.NewNotificationChannelClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return fmt.Errorf("failed to create notification channel client: %w", err)
	}
	defer func() { _ = channelClient.Close() }()

	err = channelClient.DeleteNotificationChannel(ctx.GetContext(), &monitoringpb.DeleteNotificationChannelRequest{
		Name: channelName,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			// Channel already deleted — safe to clear ref
			ctx.GetLogger().Info("notification channel already deleted", "channelName", channelName)
		} else {
			// Real failure — keep the ref so a retry can pick up where it left off
			return fmt.Errorf("failed to delete notification channel: %w", err)
		}
	}

	// Clear the agent entry only after successful deletion
	return clearWebhookChannelRef(account.ID, tenantId)
}

// buildWebhookUrl constructs the webhook URL for a given integration token.
func BuildWebhookUrl(baseUrl, token string) string {
	baseUrl = strings.TrimRight(baseUrl, "/")
	return fmt.Sprintf("%s/api/webhooks/gcp-monitoring?token=%s", baseUrl, token)
}
