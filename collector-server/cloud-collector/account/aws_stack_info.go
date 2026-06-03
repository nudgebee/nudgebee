package account

import (
	"fmt"
	awsprovider "nudgebee/collector/cloud/providers/aws"
	"nudgebee/collector/cloud/security"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

type AwsStackInfoResponse struct {
	StackName       string `json:"stack_name"`
	StackRegion     string `json:"stack_region"`
	StackStatus     string `json:"stack_status"`
	TemplateVersion string `json:"template_version"`
}

func GetAwsStackInfo(ctx *security.RequestContext, accountId string) (AwsStackInfoResponse, error) {
	acnt, providerName, err := getAccount(ctx, accountId)
	if err != nil {
		return AwsStackInfoResponse{}, err
	}
	if strings.ToLower(providerName) != "aws" {
		return AwsStackInfoResponse{}, fmt.Errorf("account is not an AWS account")
	}
	if acnt.AssumeRole == nil || *acnt.AssumeRole == "" {
		return AwsStackInfoResponse{}, fmt.Errorf("account has no assume role configured")
	}

	cfg, err := awsprovider.GetAwsConfigFromAccount(ctx.GetContext(), acnt)
	if err != nil {
		return AwsStackInfoResponse{}, fmt.Errorf("failed to get AWS config: %w", err)
	}

	targetRoleArn := *acnt.AssumeRole

	// Try us-east-1 first — the default onboarding region.
	// If not found there, search all enabled regions.
	result, err := findStackInRegion(ctx, cfg, "us-east-1", targetRoleArn)
	if err == nil {
		return result, nil
	}

	regions, regErr := getEnabledRegions(ctx, cfg)
	if regErr != nil {
		return AwsStackInfoResponse{}, fmt.Errorf("stack not found in us-east-1 and failed to list regions: %w", regErr)
	}

	for _, region := range regions {
		if region == "us-east-1" {
			continue // already checked
		}
		result, err = findStackInRegion(ctx, cfg, region, targetRoleArn)
		if err == nil {
			return result, nil
		}
	}

	return AwsStackInfoResponse{}, fmt.Errorf("no CloudFormation stack found with RoleArn matching %s in any region", targetRoleArn)
}

func findStackInRegion(ctx *security.RequestContext, cfg aws.Config, region string, targetRoleArn string) (AwsStackInfoResponse, error) {
	cfg.Region = region
	svc := cloudformation.NewFromConfig(cfg)

	paginator := cloudformation.NewDescribeStacksPaginator(svc, &cloudformation.DescribeStacksInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			return AwsStackInfoResponse{}, fmt.Errorf("failed to describe stacks in %s: %w", region, err)
		}

		for _, stack := range page.Stacks {
			if !isActiveStack(stack.StackStatus) {
				continue
			}

			for _, output := range stack.Outputs {
				if output.OutputKey != nil && *output.OutputKey == "RoleArn" &&
					output.OutputValue != nil && *output.OutputValue == targetRoleArn {

					templateVersion := ""
					for _, o := range stack.Outputs {
						if o.OutputKey != nil && *o.OutputKey == "NudgebeeTemplateVersion" && o.OutputValue != nil {
							templateVersion = *o.OutputValue
							break
						}
					}

					return AwsStackInfoResponse{
						StackName:       *stack.StackName,
						StackRegion:     region,
						StackStatus:     string(stack.StackStatus),
						TemplateVersion: templateVersion,
					}, nil
				}
			}
		}
	}

	return AwsStackInfoResponse{}, fmt.Errorf("stack not found in %s", region)
}

func getEnabledRegions(ctx *security.RequestContext, cfg aws.Config) ([]string, error) {
	cfg.Region = "us-east-1"
	ec2Svc := ec2.NewFromConfig(cfg)
	resp, err := ec2Svc.DescribeRegions(ctx.GetContext(), &ec2.DescribeRegionsInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to describe regions: %w", err)
	}
	regions := make([]string, 0, len(resp.Regions))
	for _, r := range resp.Regions {
		if r.RegionName != nil {
			regions = append(regions, *r.RegionName)
		}
	}
	return regions, nil
}

func isActiveStack(status cftypes.StackStatus) bool {
	switch status {
	case cftypes.StackStatusCreateComplete,
		cftypes.StackStatusUpdateComplete,
		cftypes.StackStatusUpdateRollbackComplete:
		return true
	default:
		return false
	}
}
