package aws

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

func lambdaStatusToNbStatus(status *string) providers.ResourceStatus {
	if status == nil {
		return providers.ResourceStatusUnknown
	}
	switch strings.ToLower(*status) {
	case "active", "pending", "inprogress", "successful":
		return providers.ResourceStatusActive
	case "terminated":
		return providers.ResourceStatusDeleted
	case "inactive", "failed":
		return providers.ResourceStatusInactive
	default:
		return providers.ResourceStatusUnknown
	}
}

var deprecatedLambdaRuntimes = []string{"dotnet7", "java8", "go1.x", "provided", "ruby2.7", "nodejs14.x", "python3.7", "dotnetcore3.1", "nodejs12.x", "python3.6", "dotnet5.0", "dotnetcore2.1", "nodejs10.x", "ruby2.5", "python2.7", "nodejs8.10", "nodejs4.3", "nodejs4.3-edge", "nodejs6.10", "dotnetcore1.0", "dotnetcore2.0", "nodejs"}

type awsLambda struct {
	DefaultAwsServiceImpl
}

func (a *awsLambda) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *awsLambda) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *awsLambda) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsLambda) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", region)
		return []providers.Resource{}, err
	}
	svc := lambda.NewFromConfig(cfg)
	ec2Svc := ec2.NewFromConfig(cfg) // Create EC2 client once for ENI fetching
	resources := []providers.Resource{}

	// Loop for Pagination
	paginator := lambda.NewListFunctionsPaginator(svc, &lambda.ListFunctionsInput{})
	for paginator.HasMorePages() {
		instances, err := paginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch lambda resources", "error", err, "accountNumber", account.AccountNumber, "region", region)
			// Return resources collected so far if pagination fails mid-way
			return resources, err
		}

		if len(instances.Functions) == 0 {
			break // No more functions on this page or in the region
		}

		for _, instance := range instances.Functions {
			// Basic nil checks for essential fields from ListFunctions
			if instance.FunctionName == nil || instance.FunctionArn == nil {
				ctx.GetLogger().Warn("Skipping Lambda function due to missing Name or ARN in list response", "instance", instance)
				continue
			}

			tags := make(map[string][]string)
			result, err := svc.ListTags(context.TODO(), &lambda.ListTagsInput{
				Resource: instance.FunctionArn,
			})
			if err != nil {
				// Log warning for non-critical tag fetch failure
				ctx.GetLogger().Warn("failed to fetch lambda tags", "error", err, "functionArn", *instance.FunctionArn, "accountNumber", account.AccountNumber, "region", region)
			} else {
				for k, v := range result.Tags {
					tags[k] = append(tags[k], v)
				}
			}

			// Use LastModified as CreatedAt proxy, handle parsing error gracefully
			createdAt := time.Time{} // Zero time as fallback
			if instance.LastModified != nil {
				parsedTime, err := time.Parse("2006-01-02T15:04:05.999+0000", *instance.LastModified)
				if err != nil {
					// Log warning on parse failure, use zero time
					ctx.GetLogger().Warn("failed to parse LastModified time", "error", err, "functionArn", *instance.FunctionArn, "lastModified", *instance.LastModified)
				} else {
					createdAt = parsedTime
				}
			}

			metaMap := structToMap(instance)

			// --- Fetch Additional Details ---

			// Function URL Configs
			functionUrls, err := svc.ListFunctionUrlConfigs(context.TODO(), &lambda.ListFunctionUrlConfigsInput{
				FunctionName: instance.FunctionName,
			})
			if err != nil {
				// Log warning for non-critical failure
				ctx.GetLogger().Warn("failed to fetch lambda function urls", "error", err, "functionName", *instance.FunctionName, "accountNumber", account.AccountNumber, "region", region)
			} else {
				metaMap["FunctionUrls"] = functionUrls.FunctionUrlConfigs
			}

			// Function Concurrency
			concurrency, err := svc.GetFunctionConcurrency(context.TODO(), &lambda.GetFunctionConcurrencyInput{
				FunctionName: instance.FunctionName,
			})
			if err != nil {
				// Log warning for non-critical failure (might error if not set)
				ctx.GetLogger().Warn("failed to fetch lambda concurrency", "error", err, "functionName", *instance.FunctionName, "accountNumber", account.AccountNumber, "region", region)
			} else {
				metaMap["Concurrency"] = concurrency.ReservedConcurrentExecutions
			}

			// Provisioned Concurrency
			// Note: ListProvisionedConcurrencyConfigs might require specific permissions
			provisionedConcurrency, err := svc.ListProvisionedConcurrencyConfigs(context.TODO(), &lambda.ListProvisionedConcurrencyConfigsInput{
				FunctionName: instance.FunctionName,
			})
			if err != nil {
				// Log warning for non-critical failure (might error if none exist)
				ctx.GetLogger().Warn("failed to fetch lambda provisioned concurrency", "error", err, "functionName", *instance.FunctionName, "accountNumber", account.AccountNumber, "region", region)
			} else {
				metaMap["ProvisionedConcurrency"] = provisionedConcurrency.ProvisionedConcurrencyConfigs
			}

			// Fetch Network Interfaces (ENIs) for VPC Lambda functions
			if instance.VpcConfig != nil && instance.VpcConfig.VpcId != nil {
				enis, privateIPs := fetchLambdaENIs(ctx, ec2Svc, *instance.FunctionName, account.AccountNumber, region)
				if len(enis) > 0 {
					metaMap["NetworkInterfaceIds"] = enis
					ctx.GetLogger().Info("fetched Lambda ENIs", "functionName", *instance.FunctionName, "enis", enis, "privateIPs", privateIPs)
				}
				if len(privateIPs) > 0 {
					metaMap["PrivateIpAddresses"] = privateIPs
				}
			}

			resource := providers.Resource{
				Id:          *instance.FunctionName,
				ServiceName: ServiceNameLambda,
				Name:        *instance.FunctionName,
				Status:      lambdaStatusToNbStatus((*string)(&instance.State)),
				Region:      region,
				Tags:        tags,
				Meta:        metaMap,
				Arn:         *instance.FunctionArn,
				CreatedAt:   createdAt, // Using parsed LastModified or zero time
				Type:        getAwsServiceResourceType(ServiceNameLambda, "function"),
			}
			resources = append(resources, resource)
		}
	}
	return resources, nil
}

// fetchLambdaENIs fetches network interfaces for a Lambda function by searching for ENIs
// with description matching "AWS Lambda VPC ENI-{FunctionName}-*"
func fetchLambdaENIs(ctx providers.CloudProviderContext, ec2Svc *ec2.Client, functionName, accountNumber, region string) ([]string, []string) {
	// Lambda ENIs have description format: "AWS Lambda VPC ENI-{FunctionName}-{UUID}"
	descriptionPattern := fmt.Sprintf("AWS Lambda VPC ENI-%s-*", functionName)

	eniOutput, err := ec2Svc.DescribeNetworkInterfaces(context.TODO(), &ec2.DescribeNetworkInterfacesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("description"),
				Values: []string{descriptionPattern},
			},
			{
				Name:   aws.String("status"),
				Values: []string{"in-use", "available"},
			},
		},
	})

	if err != nil {
		ctx.GetLogger().Warn("failed to fetch Lambda ENIs", "error", err, "functionName", functionName, "accountNumber", accountNumber, "region", region)
		return []string{}, []string{}
	}

	eniIds := []string{}
	privateIPs := []string{}
	for _, eni := range eniOutput.NetworkInterfaces {
		if eni.NetworkInterfaceId != nil {
			eniIds = append(eniIds, *eni.NetworkInterfaceId)
		}
		if eni.PrivateIpAddress != nil {
			privateIPs = append(privateIPs, *eni.PrivateIpAddress)
		}
	}

	return eniIds, privateIPs
}

// https://www.trendmicro.com/cloudoneconformity-staging/knowledge-base/aws/Lambda/

func (a *awsLambda) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	// sess, err := getAwsSessionFromAccount(account)
	// if err != nil {
	// 	ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
	// 	return nil, err
	// }

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// Check Lambda Function URL Not in Use
		if urls, ok := meta["FunctionUrls"]; ok {
			urlSlice, _ := urls.([]any)
			for _, urlAny := range urlSlice {
				urlMap, _ := urlAny.(map[string]interface{})
				if urlMap != nil && urlMap["Url"] != nil && urlMap["UrlStatus"] != nil && urlMap["UrlStatus"] == "ACTIVE" {
					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategorySecurity,
						RuleName:     "aws_lambda_function_url",
						Severity:     providers.RecommendationSeverityHigh,
						Savings:      0,
						Data: map[string]any{
							"url":    urlMap["Url"],
							"status": urlMap["UrlStatus"],
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					}
					recommendations = append(recommendations, recommendation)
				}
			}
		}

		// Enable Dead Letter Queue for Lambda Functions
		if resource.Meta["DeadLetterConfig"] == nil {
			recommendation := providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_lambda_dead_letter_queue",
				Severity:            providers.RecommendationSeverityHigh,
				Savings:             0,
				Data:                nil,
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
			recommendations = append(recommendations, recommendation)
		}

		// Enable Encryption at Rest for Environment Variables using Customer Master Keys
		if resource.Meta["KMSKeyArn"] == nil {
			recommendation := providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_lambda_environment_variable_encryption",
				Severity:            providers.RecommendationSeverityHigh,
				Savings:             0,
				Data:                nil,
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
			recommendations = append(recommendations, recommendation)
		}

		//Enable and Configure Provisioned Concurrency
		if resource.Meta["ProvisionedConcurrency"] == nil {
			recommendation := providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_lambda_provisioned_concurrency",
				Severity:            providers.RecommendationSeverityHigh,
				Savings:             0,
				Data:                nil,
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
			recommendations = append(recommendations, recommendation)
		}

		// Enable and Configure Reserved Concurrency
		if resource.Meta["Concurrency"] == nil {
			recommendation := providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_lambda_reserved_concurrency",
				Severity:            providers.RecommendationSeverityHigh,
				Savings:             0,
				Data:                nil,
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
			recommendations = append(recommendations, recommendation)
		}

		// Lambda should not use deprecated runtime
		if runtime, ok := meta["Runtime"]; ok {
			if runtimeStr, ok := runtime.(string); ok {
				for _, deprecatedRuntime := range deprecatedLambdaRuntimes {
					if runtimeStr == deprecatedRuntime {
						recommendation := providers.Recommendation{
							CategoryName:        providers.RecommendationCategoryInfraUpgrade,
							RuleName:            "aws_lambda_deprecated_runtime",
							Severity:            providers.RecommendationSeverityHigh,
							Savings:             0,
							Data:                nil,
							Action:              providers.RecommendationActionModify,
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						}
						recommendations = append(recommendations, recommendation)
						break
					}
				}
			}
		}

		// Tracing Enabled for AWS Lambda Functions
		tracingConfig, _ := resource.Meta["TracingConfig"].(map[string]interface{})
		if tracingConfig == nil || tracingConfig["Mode"] == "PassThrough" {
			recommendation := providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_lambda_tracing",
				Severity:            providers.RecommendationSeverityHigh,
				Savings:             0,
				Data:                nil,
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
			recommendations = append(recommendations, recommendation)
		}

	}

	return recommendations, nil
}

func (a *awsLambda) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return "", err
	}
	regionalCfg := cfg.Copy()
	regionalCfg.Region = region
	logsSvc := cloudwatchlogs.NewFromConfig(regionalCfg)

	logGroupName := fmt.Sprintf("/aws/lambda/%s", resourceId)
	// Verify the log group exists before returning it.
	_, err = logsSvc.DescribeLogGroups(context.TODO(), &cloudwatchlogs.DescribeLogGroupsInput{
		LogGroupNamePrefix: &logGroupName,
		Limit:              aws.Int32(1),
	})
	if err == nil {
		return logGroupName, nil
	}
	return "", err
}

func (a *awsLambda) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "lambda",
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
