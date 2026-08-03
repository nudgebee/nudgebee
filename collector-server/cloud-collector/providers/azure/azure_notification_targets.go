package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

// ListNotificationTargets returns the enabled action groups across the
// account's subscriptions so a metric alert can be created with them attached.
func (a *azureProvider) ListNotificationTargets(ctx providers.CloudProviderContext, account providers.Account, request providers.ListNotificationTargetsRequest) (providers.ListNotificationTargetsResponse, error) {
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ListNotificationTargetsResponse{}, fmt.Errorf("failed to create azure credential: %w", err)
	}

	targets := []providers.NotificationTarget{}
	for _, subID := range strings.Split(session.SubscriptionID, ",") {
		subID = strings.TrimSpace(subID)
		if subID == "" {
			continue
		}
		client, err := armmonitor.NewActionGroupsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return providers.ListNotificationTargetsResponse{}, fmt.Errorf("failed to create action groups client: %w", err)
		}
		pager := client.NewListBySubscriptionIDPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return providers.ListNotificationTargetsResponse{}, fmt.Errorf("failed to list action groups: %w", err)
			}
			for _, group := range page.Value {
				if group.ID == nil || group.Name == nil {
					continue
				}
				if group.Properties != nil && group.Properties.Enabled != nil && !*group.Properties.Enabled {
					continue
				}
				targets = append(targets, providers.NotificationTarget{Id: *group.ID, Name: *group.Name, Type: "action_group"})
			}
		}
	}

	return providers.ListNotificationTargetsResponse{Targets: targets}, nil
}
