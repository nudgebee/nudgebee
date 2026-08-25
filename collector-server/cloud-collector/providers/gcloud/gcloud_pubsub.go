package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"cloud.google.com/go/pubsub/v2"
	pubsubpb "cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"google.golang.org/api/iterator"
)

const ServiceNamePubSub = "Cloud Pub/Sub"

type pubSubService struct{}

func (s *pubSubService) GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Query Cloud Monitoring metrics for Pub/Sub
	return getGcloudMonitoringMetrics(ctx, account, filter)
}

func (s *pubSubService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := pubsub.NewClient(ctx.GetContext(), session.ProjectId, session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create Pub/Sub client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close Pub/Sub client", "error", cerr)
		}
	}()

	var resources []providers.Resource

	// List all topics using v2 API
	topicIt := client.TopicAdminClient.ListTopics(ctx.GetContext(), &pubsubpb.ListTopicsRequest{
		Project: fmt.Sprintf("projects/%s", session.ProjectId),
	})
	for {
		topic, err := topicIt.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			RecordGCPPermissionError(ctx, err)
			if isGCPPermissionOrNotFoundError(err) {
				ctx.GetLogger().Warn("skipping Pub/Sub topics — API disabled or permission denied", "error", err)
				break
			}
			ctx.GetLogger().Error("failed to list Pub/Sub topics", "error", err)
			break
		}

		resource := topicToResourceV2(ctx, topic, session.ProjectId, client)
		resources = append(resources, resource)
	}

	// List all subscriptions
	subIt := client.SubscriptionAdminClient.ListSubscriptions(ctx.GetContext(), &pubsubpb.ListSubscriptionsRequest{
		Project: fmt.Sprintf("projects/%s", session.ProjectId),
	})
	for {
		sub, err := subIt.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			RecordGCPPermissionError(ctx, err)
			if isGCPPermissionOrNotFoundError(err) {
				ctx.GetLogger().Warn("skipping Pub/Sub subscriptions — API disabled or permission denied", "error", err)
				break
			}
			ctx.GetLogger().Error("failed to list Pub/Sub subscriptions", "error", err)
			break
		}

		subResource := subscriptionToResourceV2(ctx, sub, session.ProjectId)
		resources = append(resources, subResource)
	}

	ctx.GetLogger().Info("retrieved Pub/Sub resources", "count", len(resources), "projectId", session.ProjectId)
	return resources, nil
}

func topicToResourceV2(ctx providers.CloudProviderContext, topic *pubsubpb.Topic, projectId string, client *pubsub.Client) providers.Resource {
	// Extract topic ID from full name (projects/{project}/topics/{topic})
	parts := strings.Split(topic.Name, "/")
	topicId := parts[len(parts)-1]

	// Use topic ID only as resource ID (matches GCP Monitoring topic_id label)
	resourceId := topicId
	selfLink := topic.Name

	labels := make(map[string][]string)
	for key, value := range topic.Labels {
		labels[key] = []string{value}
	}

	kmsKeyName := topic.KmsKeyName
	retentionStr := "0s"
	if topic.MessageRetentionDuration != nil {
		retentionStr = topic.MessageRetentionDuration.AsDuration().String()
	}

	meta := map[string]interface{}{
		"retention_duration": retentionStr,
		"kms_key":            kmsKeyName,
		"message_storage":    topic.MessageStoragePolicy,
		"schema_settings":    topic.SchemaSettings,
		"selfLink":           selfLink,
	}

	return providers.Resource{
		Id:          resourceId, // Topic ID only (matches GCP Monitoring topic_id)
		Name:        topicId,
		Type:        "pubsub.googleapis.com/Topic",
		Arn:         selfLink, // Full path for ARN
		ServiceName: ServiceNamePubSub,
		Status:      providers.ResourceStatusActive,
		Region:      "global", // Topics are global in Pub/Sub
		Tags:        labels,
		Meta:        meta,
		CreatedAt:   time.Time{}, // Pub/Sub API doesn't expose creation time - using zero value
	}
}

func subscriptionToResourceV2(ctx providers.CloudProviderContext, sub *pubsubpb.Subscription, projectId string) providers.Resource {
	// Extract subscription ID from full name (projects/{project}/subscriptions/{subscription})
	parts := strings.Split(sub.Name, "/")
	subId := parts[len(parts)-1]
	resourceId := sub.Name

	labels := make(map[string][]string)
	for key, value := range sub.Labels {
		labels[key] = []string{value}
	}

	ackDeadline := time.Duration(sub.AckDeadlineSeconds) * time.Second
	retainAckedMessages := sub.RetainAckedMessages
	retentionDuration := sub.MessageRetentionDuration.AsDuration()
	pushEndpoint := ""
	if sub.PushConfig != nil {
		pushEndpoint = sub.PushConfig.PushEndpoint
	}

	deadLetterTopic := ""
	if sub.DeadLetterPolicy != nil {
		deadLetterTopic = sub.DeadLetterPolicy.DeadLetterTopic
	}

	deliveryType := "pull"
	if pushEndpoint != "" {
		deliveryType = "push"
	}

	// Extract topic ID from full topic name
	topicParts := strings.Split(sub.Topic, "/")
	topicId := topicParts[len(topicParts)-1]

	meta := map[string]interface{}{
		"topic":                 topicId,
		"ack_deadline":          ackDeadline.String(),
		"retain_acked_messages": retainAckedMessages,
		"retention_duration":    retentionDuration.String(),
		"delivery_type":         deliveryType,
		"push_endpoint":         pushEndpoint,
		"dead_letter_topic":     deadLetterTopic,
	}

	return providers.Resource{
		Id:          resourceId,
		Name:        subId,
		Type:        "pubsub.googleapis.com/Subscription",
		ServiceName: ServiceNamePubSub,
		Status:      providers.ResourceStatusActive,
		Region:      "global",
		Tags:        labels,
		Meta:        meta,
		CreatedAt:   time.Time{}, // Pub/Sub API doesn't expose creation time - using zero value
	}
}

func (s *pubSubService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	// Load GCP alarm templates for Pub/Sub (all subscription-level metrics)
	pubsubAlarmTemplates, err := LoadGCPAlarmTemplates(ServiceNamePubSub)
	if err != nil {
		ctx.GetLogger().Warn("Failed to load GCP Pub/Sub alarm templates", "error", err)
		pubsubAlarmTemplates = []providers.AlarmTemplate{} // Continue with other recommendations
	}
	// Evaluates CONDITIONAL templates' conditions (e.g. the dead-letter template
	// only applies to subscriptions that have a dead-letter topic configured)
	evaluator := providers.NewRuleEvaluator(nil)

	for _, resource := range existingResources {
		if resource.ServiceName != ServiceNamePubSub {
			continue
		}

		// Recommendation 1: Check if resource has no labels
		if len(resource.Tags) == 0 {
			ruleName := "gcp_pubsub_topic_no_labels"
			if resource.Type == "pubsub.googleapis.com/Subscription" {
				ruleName = "gcp_pubsub_subscription_no_labels"
			}

			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            ruleName,
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Action:              providers.RecommendationActionModify,
				Data:                map[string]any{"resource_id": resource.Id},
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Subscription-specific recommendations
		if resource.Type == "pubsub.googleapis.com/Subscription" {
			// Check for missing Cloud Monitoring alert policies. All Pub/Sub alarm
			// templates target subscription-level metrics, so topics are skipped.
			for _, template := range pubsubAlarmTemplates {
				// CONDITIONAL templates apply only when the resource matches their
				// conditions; NATIVE templates always apply
				if !evaluator.ShouldRecommendAlarm(resource, template) {
					continue
				}

				// GCP Monitoring's subscription_id label carries the short name;
				// resource.Id is the full projects/... path (subscriptionToResourceV2)
				resourceFilter := GetResourceFilterForService(ServiceNamePubSub, resource.Name)
				isMissing, err := IsAlarmMissing(resource, template, resourceFilter)
				if err != nil {
					ctx.GetLogger().Warn("Failed to check if alarm is missing", "error", err, "template", template.Name)
					continue
				}

				if !isMissing {
					// Alarm already exists, skip
					continue
				}

				// All Pub/Sub thresholds are static — no per-resource capacity to scale by
				threshold := template.ThresholdRules.Default

				alarmConfig := buildGCPAlarmConfig(resource, template, threshold, []providers.AlarmDimension{
					{Name: "subscription_id", Value: resource.Name},
				})

				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     template.Name,
					Severity:     providers.RecommendationSeverityFromString(template.Severity),
					Savings:      0,
					Data: map[string]any{
						"subscription_id":   resource.Name,
						"subscription_name": resource.Name,
						"topic":             resource.Meta["topic"],
						"metric_name":       template.Configuration.MetricName,
						"threshold":         threshold,
						"alarm_config":      alarmConfig,
						"alarm_type":        template.AlarmType,
						"reason":            template.Description,
						"metric_type":       template.MetricType,
						"project_id":        account.AccountNumber,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Recommendation 2: Check for subscriptions without dead letter topics
			if deadLetterTopic, ok := resource.Meta["dead_letter_topic"].(string); ok && deadLetterTopic == "" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "gcp_pubsub_no_dead_letter",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Action:              providers.RecommendationActionModify,
					Data:                map[string]any{"subscription_id": resource.Id},
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Recommendation 3: Check for long retention duration
			if retentionStr, ok := resource.Meta["retention_duration"].(string); ok && retentionStr != "" {
				// Parse retention duration and compare with 7 days
				duration, err := time.ParseDuration(retentionStr)
				sevenDays := 7 * 24 * time.Hour
				if err == nil && duration > sevenDays {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategoryRightSizing,
						RuleName:            "gcp_pubsub_long_retention",
						Severity:            providers.RecommendationSeverityLow,
						Savings:             5.0,
						Action:              providers.RecommendationActionModify,
						Data:                map[string]any{"subscription_id": resource.Id, "current_retention": retentionStr, "current_retention_hours": duration.Hours(), "recommended_duration": "168h (7 days)"},
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Recommendation 4: Check for retaining acked messages
			if retainAcked, ok := resource.Meta["retain_acked_messages"].(bool); ok && retainAcked {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryRightSizing,
					RuleName:            "gcp_pubsub_retain_acked",
					Severity:            providers.RecommendationSeverityLow,
					Savings:             3.0,
					Action:              providers.RecommendationActionModify,
					Data:                map[string]any{"subscription_id": resource.Id},
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}
	}

	return recommendations, nil
}

func (s *pubSubService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := pubsub.NewClient(ctx.GetContext(), session.ProjectId, session.Opts...)
	if err != nil {
		return fmt.Errorf("failed to create Pub/Sub client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close Pub/Sub client", "error", cerr)
		}
	}()

	switch recommendation.RuleName {
	case "gcp_pubsub_topic_no_labels", "gcp_pubsub_subscription_no_labels":
		return fmt.Errorf("automatic label addition not yet implemented - please add labels manually via GCP console or gcloud CLI")

	case "gcp_pubsub_no_dead_letter":
		return fmt.Errorf("automatic dead letter topic configuration not yet implemented - please configure manually via GCP console")

	case "gcp_pubsub_long_retention":
		return fmt.Errorf("automatic retention adjustment not yet implemented - please adjust retention duration manually via GCP console")

	case "gcp_pubsub_retain_acked":
		return fmt.Errorf("automatic configuration change not yet implemented - please disable retain_acked_messages via GCP console")

	default:
		return fmt.Errorf("unknown recommendation rule: %s", recommendation.RuleName)
	}
}

func (s *pubSubService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{}, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := pubsub.NewClient(ctx.GetContext(), session.ProjectId, session.Opts...)
	if err != nil {
		return providers.ApplyCommandResponse{}, fmt.Errorf("failed to create Pub/Sub client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close Pub/Sub client", "error", cerr)
		}
	}()

	switch command.Command {
	case "create_topic":
		topicId, ok := command.Args["topic_id"].(string)
		if !ok || topicId == "" {
			return providers.ApplyCommandResponse{}, fmt.Errorf("topic_id arg required")
		}

		topicName := fmt.Sprintf("projects/%s/topics/%s", session.ProjectId, topicId)

		// Check if topic exists using v2 API
		_, err := client.TopicAdminClient.GetTopic(ctx.GetContext(), &pubsubpb.GetTopicRequest{
			Topic: topicName,
		})
		if err == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("Topic %s already exists", topicId),
			}, nil
		}

		// Create topic using v2 API
		_, err = client.TopicAdminClient.CreateTopic(ctx.GetContext(), &pubsubpb.Topic{
			Name: topicName,
		})
		if err != nil {
			return providers.ApplyCommandResponse{}, fmt.Errorf("failed to create topic: %w", err)
		}

		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("Successfully created topic %s", topicId),
		}, nil

	case "delete_topic":
		topicId, ok := command.Args["topic_id"].(string)
		if !ok || topicId == "" {
			return providers.ApplyCommandResponse{}, fmt.Errorf("topic_id arg required")
		}

		topicName := fmt.Sprintf("projects/%s/topics/%s", session.ProjectId, topicId)
		err := client.TopicAdminClient.DeleteTopic(ctx.GetContext(), &pubsubpb.DeleteTopicRequest{
			Topic: topicName,
		})
		if err != nil {
			return providers.ApplyCommandResponse{}, fmt.Errorf("failed to delete topic: %w", err)
		}

		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("Successfully deleted topic %s", topicId),
		}, nil

	case "delete_subscription":
		subId, ok := command.Args["subscription_id"].(string)
		if !ok || subId == "" {
			return providers.ApplyCommandResponse{}, fmt.Errorf("subscription_id arg required")
		}

		subName := fmt.Sprintf("projects/%s/subscriptions/%s", session.ProjectId, subId)
		err := client.SubscriptionAdminClient.DeleteSubscription(ctx.GetContext(), &pubsubpb.DeleteSubscriptionRequest{
			Subscription: subName,
		})
		if err != nil {
			return providers.ApplyCommandResponse{}, fmt.Errorf("failed to delete subscription: %w", err)
		}

		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("Successfully deleted subscription %s", subId),
		}, nil

	default:
		return providers.ApplyCommandResponse{}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *pubSubService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, _ string) string {
	return ""
}
