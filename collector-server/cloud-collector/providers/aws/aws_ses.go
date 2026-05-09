package aws

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/ses/types"
)

type awsSes struct {
	DefaultAwsServiceImpl
}

func (a *awsSes) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *awsSes) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *awsSes) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsSes) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameSES)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := ses.NewFromConfig(cfg)
	resources := []providers.Resource{}

	// --- Get Identities (Domains and Email Addresses) ---
	identityNames := []string{} // Collect names to fetch attributes later

	paginator := ses.NewListIdentitiesPaginator(svc, &ses.ListIdentitiesInput{})
	for paginator.HasMorePages() {
		identitiesOutput, err := paginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to list ses identities", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err // Return partial or fail completely
		}

		identityNames = append(identityNames, identitiesOutput.Identities...)
	}

	// Batch get attributes for identities
	if len(identityNames) > 0 {
		// Get Verification Attributes
		verificationAttrsOutput, err := svc.GetIdentityVerificationAttributes(context.TODO(), &ses.GetIdentityVerificationAttributesInput{
			Identities: identityNames,
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to get ses identity verification attributes", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			// Continue without verification status if this fails
		}

		// Get DKIM Attributes
		dkimAttrsOutput, err := svc.GetIdentityDkimAttributes(context.TODO(), &ses.GetIdentityDkimAttributesInput{
			Identities: identityNames,
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to get ses identity dkim attributes", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			// Continue without DKIM status if this fails
		}

		// Get Mail From Domain Attributes
		mailFromAttrsOutput, err := svc.GetIdentityMailFromDomainAttributes(context.TODO(), &ses.GetIdentityMailFromDomainAttributesInput{
			Identities: identityNames,
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to get ses identity mail from domain attributes", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			// Continue without Mail From status if this fails
		}

		// Get Notification Attributes
		notificationAttrsOutput, err := svc.GetIdentityNotificationAttributes(context.TODO(), &ses.GetIdentityNotificationAttributesInput{
			Identities: identityNames,
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to get ses identity notification attributes", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			// Continue without notification status if this fails
		}

		// Process each identity
		for _, identityName := range identityNames {
			tags := make(map[string][]string) // SES identities don't have standard tags via API
			meta := make(map[string]any)

			// Add attributes to meta if available
			if verificationAttrsOutput != nil {
				if attrs, ok := verificationAttrsOutput.VerificationAttributes[identityName]; ok {
					meta["VerificationAttributes"] = structToMap(attrs)
				}
			}
			if dkimAttrsOutput != nil {
				if attrs, ok := dkimAttrsOutput.DkimAttributes[identityName]; ok {
					meta["DkimAttributes"] = structToMap(attrs)
				}
			}
			if mailFromAttrsOutput != nil {
				if attrs, ok := mailFromAttrsOutput.MailFromDomainAttributes[identityName]; ok {
					meta["MailFromDomainAttributes"] = structToMap(attrs)
				}
			}
			if notificationAttrsOutput != nil {
				if attrs, ok := notificationAttrsOutput.NotificationAttributes[identityName]; ok {
					meta["NotificationAttributes"] = structToMap(attrs)
				}
			}

			// Determine status based on verification
			status := providers.ResourceStatusUnknown
			if verificationAttrsOutput != nil {
				if attrs, ok := verificationAttrsOutput.VerificationAttributes[identityName]; ok {
					if attrs.VerificationStatus != "" {
						switch attrs.VerificationStatus {
						case types.VerificationStatusSuccess:
							status = providers.ResourceStatusActive
						case types.VerificationStatusPending, types.VerificationStatusTemporaryFailure:
							status = providers.ResourceStatusActive
						case types.VerificationStatusFailed, types.VerificationStatusNotStarted:
							status = providers.ResourceStatusInactive
						}
					}
				}
			}

			resource := providers.Resource{
				Id:          identityName,
				ServiceName: ServiceNameSES,
				Name:        identityName,
				Status:      status,
				Region:      regionName,
				Tags:        tags, // Empty for now
				Meta:        meta,
				CreatedAt:   time.Now(), // Not available from List/Get APIs
				Type:        getAwsServiceResourceType(ServiceNameSES, "identity"),
			}
			resources = append(resources, resource)
		}
	}

	// --- Get Configuration Sets ---
	var nextTokenConfigSets *string
	for {
		configurationsOutput, err := svc.ListConfigurationSets(context.TODO(), &ses.ListConfigurationSetsInput{
			NextToken: nextTokenConfigSets,
		})
		if err != nil {
			ctx.GetLogger().Error("failed to list ses configuration sets", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err // Return partial or fail completely
		}

		for _, configuration := range configurationsOutput.ConfigurationSets {
			tags := make(map[string][]string) // Config sets don't have standard tags via API
			meta := make(map[string]any)

			// Describe Configuration Set to get details (e.g., event destinations)
			details, err := svc.DescribeConfigurationSet(context.TODO(), &ses.DescribeConfigurationSetInput{
				ConfigurationSetName: configuration.Name,
				// ConfigurationSetAttributeNames: []*string{aws.String(ses.ConfigurationSetAttributeEventDestinations)}, // Specify attributes if needed
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to describe ses configuration set", "error", err, "configSetName", *configuration.Name, "accountNumber", account.AccountNumber, "region", regionName)
				// Continue with basic info if describe fails
				meta["Name"] = *configuration.Name // Add name at least
			} else {
				meta = structToMap(details)
			}

			resource := providers.Resource{
				Id:          *configuration.Name,
				ServiceName: ServiceNameSES,
				Name:        *configuration.Name,
				Status:      providers.ResourceStatusActive, // Assume active if listed
				Region:      regionName,
				Tags:        tags, // Empty for now
				Meta:        meta,
				CreatedAt:   time.Now(), // Not available from List/Describe APIs
				Type:        getAwsServiceResourceType(ServiceNameSES, "configuration-set"),
			}
			resources = append(resources, resource)
		}
		nextTokenConfigSets = configurationsOutput.NextToken
		if nextTokenConfigSets == nil {
			break
		}
	}

	return resources, nil
}

func (a *awsSes) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	// Session might be needed if additional API calls are required beyond what's in Meta
	_, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session for recommendations", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameSES)
		return recommendations, err
	}

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue // Skip if no metadata available
		}

		// --- Recommendations for Identities ---
		if resource.Type == getAwsServiceResourceType(ServiceNameSES, "identity") {
			// Check 1: DKIM Enabled and Verified
			dkimEnabled := false
			dkimVerified := false
			if dkimAttrs, ok := meta["DkimAttributes"].(map[string]any); ok {
				if enabled, ok := dkimAttrs["DkimEnabled"].(bool); ok {
					dkimEnabled = enabled
				}
				if status, ok := dkimAttrs["DkimVerificationStatus"].(string); ok && status == string(types.VerificationStatusSuccess) {
					dkimVerified = true
				}
			}
			if !dkimEnabled || !dkimVerified {
				reason := "DKIM is not enabled."
				if dkimEnabled && !dkimVerified {
					reason = "DKIM is enabled but not verified."
				}
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "aws_ses_identity_dkim",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"identity_name": resource.Name, "identity_arn": resource.Arn, "reason": reason},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check 2: Mail From Domain Configured
			mailFromConfigured := false
			mailFromStatusOk := false
			if mailFromAttrs, ok := meta["MailFromDomainAttributes"].(map[string]any); ok {
				if domain, ok := mailFromAttrs["MailFromDomain"].(string); ok && domain != "" {
					mailFromConfigured = true
				}
				if status, ok := mailFromAttrs["MailFromDomainStatus"].(string); ok && status == "Success" {
					mailFromStatusOk = true
				}
			}
			if !mailFromConfigured || !mailFromStatusOk {
				reason := "Custom MAIL FROM domain is not configured."
				if mailFromConfigured && !mailFromStatusOk {
					reason = "Custom MAIL FROM domain is configured but status is not Success."
				}
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration, // Or Deliverability
					RuleName:            "aws_ses_identity_mail_from_domain",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"identity_name": resource.Name, "identity_arn": resource.Arn, "reason": reason},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check 3: Bounce/Complaint Notifications Configured
			bounceNotificationsConfigured := false
			complaintNotificationsConfigured := false
			if notifyAttrs, ok := meta["NotificationAttributes"].(map[string]any); ok {
				if topic, ok := notifyAttrs["BounceTopic"].(string); ok && topic != "" {
					bounceNotificationsConfigured = true
				}
				if topic, ok := notifyAttrs["ComplaintTopic"].(string); ok && topic != "" {
					complaintNotificationsConfigured = true
				}
			}
			if !bounceNotificationsConfigured || !complaintNotificationsConfigured {
				reason := ""
				if !bounceNotificationsConfigured && !complaintNotificationsConfigured {
					reason = "Bounce and Complaint notifications are not configured."
				} else if !bounceNotificationsConfigured {
					reason = "Bounce notifications are not configured."
				} else {
					reason = "Complaint notifications are not configured."
				}
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration, // Or Deliverability
					RuleName:            "aws_ses_identity_notifications",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"identity_name": resource.Name, "identity_arn": resource.Arn, "reason": reason},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check 4: Missing Tags (SES identities don't support tags directly)
			// if len(resource.Tags) == 0 { ... } // Skip tag check for identities
		}

		// --- Recommendations for Configuration Sets ---
		if resource.Type == getAwsServiceResourceType(ServiceNameSES, "configuration-set") {
			// Check 1: Event Destinations Configured
			hasEventDestinations := false
			if destinations, ok := meta["EventDestinations"].([]any); ok && len(destinations) > 0 {
				hasEventDestinations = true
			}
			if !hasEventDestinations {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration, // Or Monitoring
					RuleName:            "aws_ses_configset_event_destinations",
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{"configset_name": resource.Name, "configset_arn": resource.Arn, "reason": "Configuration set has no event destinations configured."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check 2: Missing Tags (SES config sets don't support tags directly)
			// if len(resource.Tags) == 0 { ... } // Skip tag check for config sets
		}
	}

	return recommendations, nil
}

func (a *awsSes) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return "", err
	}
	regionalCfg := cfg.Copy()
	regionalCfg.Region = region
	logsSvc := cloudwatchlogs.NewFromConfig(regionalCfg)

	var foundLogGroup string
	paginator := cloudwatchlogs.NewDescribeLogGroupsPaginator(logsSvc, &cloudwatchlogs.DescribeLogGroupsInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.TODO())
		if err != nil {
			return "", err
		}
		for _, lg := range page.LogGroups {
			logGroupName := *lg.LogGroupName
			describeLogStreamsOutput, err := logsSvc.DescribeLogStreams(context.TODO(), &cloudwatchlogs.DescribeLogStreamsInput{
				LogGroupName:        &logGroupName,
				LogStreamNamePrefix: &resourceId,
				Limit:               aws.Int32(1),
			})
			if err == nil && len(describeLogStreamsOutput.LogStreams) > 0 {
				foundLogGroup = logGroupName
				return foundLogGroup, nil
			}
		}
	}
	return foundLogGroup, nil
}

func (a *awsSes) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "ses",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	_, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return providers.ServiceMapApplication{}, err
	}

	return app, nil
}
