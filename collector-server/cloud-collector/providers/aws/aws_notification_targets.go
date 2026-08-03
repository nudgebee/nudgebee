package aws

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/sns"
)

// ListNotificationTargets returns the SNS topics in the requested region so a
// CloudWatch alarm can be created with them as alarm actions.
func (a *awsProvider) ListNotificationTargets(ctx providers.CloudProviderContext, account providers.Account, request providers.ListNotificationTargetsRequest) (providers.ListNotificationTargetsResponse, error) {
	if request.Region == "" {
		return providers.ListNotificationTargetsResponse{}, fmt.Errorf("region is required")
	}

	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		return providers.ListNotificationTargetsResponse{}, fmt.Errorf("failed to create AWS config: %w", err)
	}
	cfg.Region = request.Region
	svc := sns.NewFromConfig(cfg)

	targets := []providers.NotificationTarget{}
	paginator := sns.NewListTopicsPaginator(svc, &sns.ListTopicsInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			return providers.ListNotificationTargetsResponse{}, fmt.Errorf("failed to list sns topics: %w", err)
		}
		for _, topic := range page.Topics {
			if topic.TopicArn == nil {
				continue
			}
			arn := *topic.TopicArn
			name := arn
			if idx := strings.LastIndex(arn, ":"); idx >= 0 {
				name = arn[idx+1:]
			}
			targets = append(targets, providers.NotificationTarget{Id: arn, Name: name, Type: "sns_topic"})
		}
	}

	return providers.ListNotificationTargetsResponse{Targets: targets}, nil
}
