package aws

import (
	"errors"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/securityhub"
	"github.com/aws/smithy-go"
)

type awsSecurityHub struct {
	DefaultAwsServiceImpl
}

func (a *awsSecurityHub) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *awsSecurityHub) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *awsSecurityHub) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}
func (a *awsSecurityHub) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := securityhub.NewFromConfig(cfg)
	resources := []providers.Resource{}

	// DescribeHub is the primary way to check if Security Hub is enabled in a region.
	// It doesn't return a list, but a single hub description.
	describeHubOutput, err := svc.DescribeHub(ctx.GetContext(), &securityhub.DescribeHubInput{})
	if err != nil {
		var apiErr *smithy.GenericAPIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "InvalidAccessException" || (apiErr.ErrorCode() == "InvalidInputException" && strings.Contains(apiErr.ErrorMessage(), "Security Hub is not enabled in this region"))) {
			ctx.GetLogger().Info("Security Hub is not enabled in this region", "region", regionName, "accountNumber", account.AccountNumber)
			return resources, nil // Not an error, just no hub resource
		}
		ctx.GetLogger().Error("failed to describe securityhub hub", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
		return nil, err
	}

	// If DescribeHub succeeds, Security Hub is enabled.
	// Create a resource representing the Security Hub instance in this region.
	meta := structToMap(describeHubOutput) // Contains HubArn, SubscribedAt

	resource := providers.Resource{
		Id:          account.AccountNumber, // Hub is account-level per region
		ServiceName: ServiceNameSecurityHub,
		Name:        "SecurityHub",
		Status:      providers.ResourceStatusActive, // If described, it's active
		Region:      regionName,
		Tags:        map[string][]string{}, // Security Hub itself doesn't have tags directly
		Meta:        meta,
		Arn:         *describeHubOutput.HubArn,
		CreatedAt:   time.Now(), // SubscribedAt is in HubArn, but not directly in describeHubOutput
		Type:        getAwsServiceResourceType(ServiceNameSecurityHub, "hub"),
	}
	resources = append(resources, resource)

	// Optionally, list enabled standards
	listStandardsOutput, err := svc.DescribeStandards(ctx.GetContext(), &securityhub.DescribeStandardsInput{})
	if err != nil {
		ctx.GetLogger().Warn("failed to list securityhub standards", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
	} else {
		for _, standard := range listStandardsOutput.Standards {
			// StandardsSubscriptionArn is not directly available on the Standard struct.
			// We assume if a standard is listed by DescribeStandards, it's "available" to be subscribed to.
			// If we need to check if it's *actually* subscribed, we'd need DescribeStandardsControls or GetEnabledStandards.
			// For now, we'll just list the standard itself.
			standardMeta := structToMap(standard)
			resources = append(resources, providers.Resource{
				Id:          *standard.StandardsArn,
				ServiceName: ServiceNameSecurityHub,
				Name:        *standard.Name,
				Status:      providers.ResourceStatusActive, // Represents the standard's availability
				Region:      regionName,
				Tags:        map[string][]string{},
				Meta:        standardMeta,
				Arn:         *standard.StandardsArn,
				CreatedAt:   time.Now(), // Not directly available
				Type:        getAwsServiceResourceType(ServiceNameSecurityHub, "standard"),
			})
		}
	}

	return resources, nil
}

func (a *awsSecurityHub) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", "")
		return recommendations, err
	}

	svc := securityhub.NewFromConfig(cfg)

	// Check if Security Hub is enabled before calling GetFindings.
	// When Security Hub is not enabled, GetFindings returns AccessDeniedException
	// instead of a descriptive error, triggering false-positive permission alerts.
	_, err = svc.DescribeHub(ctx.GetContext(), &securityhub.DescribeHubInput{})
	if err != nil {
		var apiErr *smithy.GenericAPIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "InvalidInputException" || apiErr.ErrorCode() == "InvalidAccessException" || apiErr.ErrorCode() == "AccessDeniedException") {
			ctx.GetLogger().Info("Security Hub is not enabled, skipping recommendations", "accountNumber", account.AccountNumber)
			return recommendations, nil
		}
		ctx.GetLogger().Error("failed to describe securityhub hub", "error", err, "accountNumber", account.AccountNumber)
		return recommendations, err
	}

	paginator := securityhub.NewGetFindingsPaginator(svc, &securityhub.GetFindingsInput{})
	for paginator.HasMorePages() {
		findings, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch securityhub findings", "error", err, "accountNumber", account.AccountNumber)
			return recommendations, err
		}

		for _, finding := range findings.Findings {
			region := "global"
			resourceType := "account"
			resourceId := account.AccountNumber
			if len(finding.Resources) > 0 {
				if finding.Resources[0].Region != nil {
					region = *finding.Resources[0].Region
				}
				if finding.Resources[0].Type != nil && *finding.Resources[0].Type != "AwsAccount" {
					resourceType = strings.ToLower(*finding.Resources[0].Type)
				}
				if finding.Resources[0].Id != nil && *finding.Resources[0].Type != "AwsAccount" {
					resourceId = strings.ToLower(*finding.Resources[0].Id)
				}
			}

			ruleName := "aws_securityhub"
			if finding.Compliance != nil {
				if finding.Compliance.SecurityControlId != nil {
					ruleName = "aws_securityhub_" + strings.ToLower(*finding.Compliance.SecurityControlId)
				} else {
					ctx.GetLogger().Error("failed to fetch securityhub finding", "finding", finding.Id)
					continue
				}
			}

			recommendation := providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            ruleName,
				Severity:            providers.RecommendationSeverityFromString(string(finding.Severity.Label)),
				Savings:             0,
				Data:                structToMap(finding),
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: "securityhub",
				ResourceId:          resourceId,
				ResourceType:        resourceType,
				ResourceRegion:      region,
			}
			recommendations = append(recommendations, recommendation)
		}
	}

	return recommendations, nil
}

func (a *awsSecurityHub) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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
		page, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			return "", err
		}
		for _, lg := range page.LogGroups {
			logGroupName := *lg.LogGroupName
			describeLogStreamsOutput, err := logsSvc.DescribeLogStreams(ctx.GetContext(), &cloudwatchlogs.DescribeLogStreamsInput{
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

func (a *awsSecurityHub) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "securityhub",
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
