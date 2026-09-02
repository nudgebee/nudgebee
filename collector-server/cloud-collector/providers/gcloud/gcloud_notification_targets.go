package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/providers"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/api/iterator"
)

// ListNotificationTargets returns the project's notification channels so an
// alert policy can be created with them attached. Explicitly disabled channels
// are skipped — attaching them would not notify anyone.
func (g *gcloudProvider) ListNotificationTargets(ctx providers.CloudProviderContext, account providers.Account, request providers.ListNotificationTargetsRequest) (providers.ListNotificationTargetsResponse, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return providers.ListNotificationTargetsResponse{}, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := monitoring.NewNotificationChannelClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return providers.ListNotificationTargetsResponse{}, fmt.Errorf("failed to create notification channel client: %w", err)
	}
	defer func() { _ = client.Close() }()

	targets := []providers.NotificationTarget{}
	it := client.ListNotificationChannels(ctx.GetContext(), &monitoringpb.ListNotificationChannelsRequest{
		Name: fmt.Sprintf("projects/%s", session.ProjectId),
	})
	for {
		channel, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return providers.ListNotificationTargetsResponse{}, fmt.Errorf("failed to list notification channels: %w", err)
		}
		if channel.GetEnabled() != nil && !channel.GetEnabled().GetValue() {
			continue
		}
		name := channel.GetDisplayName()
		if name == "" {
			name = channel.GetName()
		}
		targets = append(targets, providers.NotificationTarget{
			Id:   channel.GetName(),
			Name: name,
			Type: channel.GetType(),
		})
	}

	return providers.ListNotificationTargetsResponse{Targets: targets}, nil
}
