package account

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
)

// ListNotificationTargets returns the notification destinations (SNS topics,
// notification channels, action groups) an alarm can be wired to for the account.
func ListNotificationTargets(ctx *security.RequestContext, accountId string, request providers.ListNotificationTargetsRequest) (providers.ListNotificationTargetsResponse, error) {
	account, provider, err := getAccount(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch account", "error", err)
		return providers.ListNotificationTargetsResponse{}, err
	}
	cloudProvider, ok := providers.GetProvider(provider)
	if !ok {
		return providers.ListNotificationTargetsResponse{}, fmt.Errorf("provider not found")
	}
	lister, ok := cloudProvider.(providers.NotificationTargetLister)
	if !ok {
		return providers.ListNotificationTargetsResponse{}, fmt.Errorf("notification targets not supported for provider %s", provider)
	}
	return lister.ListNotificationTargets(ctx, account, request)
}
