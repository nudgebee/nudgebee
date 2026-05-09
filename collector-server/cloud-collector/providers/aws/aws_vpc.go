package aws

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type amazonVpc struct {
	DefaultAwsServiceImpl
}

func (a *amazonVpc) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *amazonVpc) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *amazonVpc) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}
func (a *amazonVpc) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := ec2.NewFromConfig(cfg)
	resources := []providers.Resource{}

	// --- Get VPCs ---
	vpcPaginator := ec2.NewDescribeVpcsPaginator(svc, &ec2.DescribeVpcsInput{})
	for vpcPaginator.HasMorePages() {
		vpcsOutput, err := vpcPaginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch vpcs", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}
		for _, vpc := range vpcsOutput.Vpcs {
			tags := make(map[string][]string)
			for _, tag := range vpc.Tags {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}
			meta := structToMap(vpc)
			resources = append(resources, providers.Resource{
				Id:          *vpc.VpcId,
				ServiceName: ServiceNameVPC,
				Name:        *vpc.VpcId, // Often VPCs have a Name tag, but ID is unique
				Status:      providers.ResourceStatusActive,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         fmt.Sprintf("arn:aws:ec2:%s:%s:vpc/%s", regionName, account.AccountNumber, *vpc.VpcId),
				CreatedAt:   time.Now(), // Creation time not directly in DescribeVpcs
				Type:        getAwsServiceResourceType(ServiceNameVPC, "vpc"),
			})
		}
	}

	// --- Get Subnets ---
	subnetPaginator := ec2.NewDescribeSubnetsPaginator(svc, &ec2.DescribeSubnetsInput{})
	for subnetPaginator.HasMorePages() {
		subnetsOutput, err := subnetPaginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch subnets", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}
		for _, subnet := range subnetsOutput.Subnets {
			tags := make(map[string][]string)
			for _, tag := range subnet.Tags {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}
			meta := structToMap(subnet)
			resources = append(resources, providers.Resource{
				Id:          *subnet.SubnetId,
				ServiceName: ServiceNameVPC,
				Name:        *subnet.SubnetId,
				Status:      providers.ResourceStatusActive,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         fmt.Sprintf("arn:aws:ec2:%s:%s:subnet/%s", regionName, account.AccountNumber, *subnet.SubnetId),
				CreatedAt:   time.Now(), // Creation time not directly in DescribeSubnets
				Type:        getAwsServiceResourceType(ServiceNameVPC, "subnet"),
			})
		}
	}

	// --- Get Security Groups ---
	sgPaginator := ec2.NewDescribeSecurityGroupsPaginator(svc, &ec2.DescribeSecurityGroupsInput{})
	for sgPaginator.HasMorePages() {
		securityGroupsOutput, err := sgPaginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch security groups", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}
		for _, sg := range securityGroupsOutput.SecurityGroups {
			tags := make(map[string][]string)
			for _, tag := range sg.Tags {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}
			meta := structToMap(sg)
			resources = append(resources, providers.Resource{
				Id:          *sg.GroupId,
				ServiceName: ServiceNameVPC,
				Name:        *sg.GroupName,
				Status:      providers.ResourceStatusActive,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         fmt.Sprintf("arn:aws:ec2:%s:%s:security-group/%s", regionName, account.AccountNumber, *sg.GroupId),
				CreatedAt:   time.Now(), // Creation time not directly in DescribeSecurityGroups
				Type:        getAwsServiceResourceType(ServiceNameVPC, "security_group"),
			})
		}
	}

	// --- Get Network Interfaces (ENIs) ---
	eniPaginator := ec2.NewDescribeNetworkInterfacesPaginator(svc, &ec2.DescribeNetworkInterfacesInput{})
	for eniPaginator.HasMorePages() {
		eniOutput, err := eniPaginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch network interfaces", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}
		for _, eni := range eniOutput.NetworkInterfaces {
			tags := make(map[string][]string)
			for _, tag := range eni.TagSet {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}

			meta := structToMap(eni)
			// Ensure PrivateIpAddress is stored in meta for easy querying
			if eni.PrivateIpAddress != nil {
				meta["PrivateIpAddress"] = *eni.PrivateIpAddress
			}

			// Map ENI status to resource status
			var status providers.ResourceStatus
			switch eni.Status {
			case ec2types.NetworkInterfaceStatusInUse:
				status = providers.ResourceStatusActive
			case ec2types.NetworkInterfaceStatusAvailable:
				status = providers.ResourceStatusActive
			default:
				status = providers.ResourceStatus(eni.Status)
			}

			resources = append(resources, providers.Resource{
				Id:          *eni.NetworkInterfaceId,
				ServiceName: ServiceNameVPC,
				Name:        getEniName(eni),
				Status:      status,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         fmt.Sprintf("arn:aws:ec2:%s:%s:network-interface/%s", regionName, account.AccountNumber, *eni.NetworkInterfaceId),
				CreatedAt:   time.Now(), // Creation time not directly available
				Type:        getAwsServiceResourceType(ServiceNameVPC, "network-interface"),
			})
		}
	}

	// --- Get Elastic IPs ---
	// Note: DescribeAddresses does not support pagination
	addressesOutput, err := svc.DescribeAddresses(context.TODO(), &ec2.DescribeAddressesInput{})
	if err != nil {
		ctx.GetLogger().Error("failed to fetch elastic IPs", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
		return resources, err
	}
	if addressesOutput != nil {
		for _, address := range addressesOutput.Addresses {
			if address.PublicIp == nil {
				ctx.GetLogger().Warn("Skipping Elastic IP due to missing PublicIp", "allocationId", address.AllocationId, "region", regionName)
				continue
			}

			tags := make(map[string][]string)
			for _, tag := range address.Tags {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}

			meta := structToMap(address)

			// Determine status based on association
			status := providers.ResourceStatusActive
			if address.AssociationId == nil {
				status = providers.ResourceStatusInactive // Unallocated
			}

			// Use AllocationId as unique identifier (EIP in VPC), fallback to PublicIp (EC2-Classic)
			resourceId := *address.PublicIp
			if address.AllocationId != nil {
				resourceId = *address.AllocationId
			}

			name := resourceId
			if nameTag, ok := tags["Name"]; ok && len(nameTag) > 0 {
				name = nameTag[0]
			}

			resources = append(resources, providers.Resource{
				Id:          resourceId,
				ServiceName: ServiceNameVPC,
				Name:        name,
				Status:      status,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         fmt.Sprintf("arn:aws:ec2:%s:%s:elastic-ip/%s", regionName, account.AccountNumber, resourceId),
				CreatedAt:   time.Now(), // Creation time not available
				Type:        getAwsServiceResourceType(ServiceNameVPC, "elastic-ip"),
			})
		}
	}

	// --- Get Route Tables ---
	routeTablesPaginator := ec2.NewDescribeRouteTablesPaginator(svc, &ec2.DescribeRouteTablesInput{})
	for routeTablesPaginator.HasMorePages() {
		routeTablesOutput, err := routeTablesPaginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch route tables", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}
		for _, routeTable := range routeTablesOutput.RouteTables {
			if routeTable.RouteTableId == nil {
				ctx.GetLogger().Warn("Skipping route table due to missing RouteTableId", "region", regionName)
				continue
			}

			tags := make(map[string][]string)
			for _, tag := range routeTable.Tags {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}

			meta := structToMap(routeTable)

			name := *routeTable.RouteTableId
			if nameTag, ok := tags["Name"]; ok && len(nameTag) > 0 {
				name = nameTag[0]
			}

			resources = append(resources, providers.Resource{
				Id:          *routeTable.RouteTableId,
				ServiceName: ServiceNameVPC,
				Name:        name,
				Status:      providers.ResourceStatusActive,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         fmt.Sprintf("arn:aws:ec2:%s:%s:route-table/%s", regionName, account.AccountNumber, *routeTable.RouteTableId),
				CreatedAt:   time.Now(), // Creation time not available
				Type:        getAwsServiceResourceType(ServiceNameVPC, "route-table"),
			})
		}
	}

	// --- Get VPC Endpoints ---
	vpcEndpointsPaginator := ec2.NewDescribeVpcEndpointsPaginator(svc, &ec2.DescribeVpcEndpointsInput{})
	for vpcEndpointsPaginator.HasMorePages() {
		vpcEndpointsOutput, err := vpcEndpointsPaginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch VPC endpoints", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}
		for _, endpoint := range vpcEndpointsOutput.VpcEndpoints {
			if endpoint.VpcEndpointId == nil {
				ctx.GetLogger().Warn("Skipping VPC endpoint due to missing VpcEndpointId", "region", regionName)
				continue
			}

			tags := make(map[string][]string)
			for _, tag := range endpoint.Tags {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}

			meta := structToMap(endpoint)

			// Map endpoint state to resource status
			status := providers.ResourceStatusUnknown
			switch endpoint.State {
			case ec2types.StateAvailable, ec2types.StatePendingAcceptance, ec2types.StatePending:
				status = providers.ResourceStatusActive
			case ec2types.StateDeleting, ec2types.StateDeleted, ec2types.StateRejected, ec2types.StateFailed, ec2types.StateExpired:
				status = providers.ResourceStatusDeleted
			}

			name := *endpoint.VpcEndpointId
			if nameTag, ok := tags["Name"]; ok && len(nameTag) > 0 {
				name = nameTag[0]
			}

			resources = append(resources, providers.Resource{
				Id:          *endpoint.VpcEndpointId,
				ServiceName: ServiceNameVPC,
				Name:        name,
				Status:      status,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         fmt.Sprintf("arn:aws:ec2:%s:%s:vpc-endpoint/%s", regionName, account.AccountNumber, *endpoint.VpcEndpointId),
				CreatedAt:   aws.ToTime(endpoint.CreationTimestamp),
				Type:        getAwsServiceResourceType(ServiceNameVPC, "vpc-endpoint"),
			})
		}
	}

	// --- Get NAT Gateways ---
	natGatewaysPaginator := ec2.NewDescribeNatGatewaysPaginator(svc, &ec2.DescribeNatGatewaysInput{})
	for natGatewaysPaginator.HasMorePages() {
		natGatewaysOutput, err := natGatewaysPaginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch NAT gateways", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}
		for _, natGateway := range natGatewaysOutput.NatGateways {
			if natGateway.NatGatewayId == nil {
				ctx.GetLogger().Warn("Skipping NAT gateway due to missing NatGatewayId", "region", regionName)
				continue
			}

			tags := make(map[string][]string)
			for _, tag := range natGateway.Tags {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}

			meta := structToMap(natGateway)

			// Map NAT gateway state to resource status
			status := providers.ResourceStatusUnknown
			switch natGateway.State {
			case ec2types.NatGatewayStateAvailable, ec2types.NatGatewayStatePending:
				status = providers.ResourceStatusActive
			case ec2types.NatGatewayStateDeleting, ec2types.NatGatewayStateDeleted, ec2types.NatGatewayStateFailed:
				status = providers.ResourceStatusDeleted
			}

			name := *natGateway.NatGatewayId
			if nameTag, ok := tags["Name"]; ok && len(nameTag) > 0 {
				name = nameTag[0]
			}

			resources = append(resources, providers.Resource{
				Id:          *natGateway.NatGatewayId,
				ServiceName: ServiceNameVPC,
				Name:        name,
				Status:      status,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         fmt.Sprintf("arn:aws:ec2:%s:%s:natgateway/%s", regionName, account.AccountNumber, *natGateway.NatGatewayId),
				CreatedAt:   aws.ToTime(natGateway.CreateTime),
				Type:        getAwsServiceResourceType(ServiceNameVPC, "natgateway"),
			})
		}
	}

	// --- Get VPC Flow Logs ---
	flowLogsPaginator := ec2.NewDescribeFlowLogsPaginator(svc, &ec2.DescribeFlowLogsInput{})
	for flowLogsPaginator.HasMorePages() {
		flowLogsOutput, err := flowLogsPaginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch VPC flow logs", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}
		for _, flowLog := range flowLogsOutput.FlowLogs {
			if flowLog.FlowLogId == nil {
				ctx.GetLogger().Warn("Skipping VPC flow log due to missing FlowLogId", "region", regionName)
				continue
			}

			tags := make(map[string][]string)
			for _, tag := range flowLog.Tags {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}

			meta := structToMap(flowLog)

			name := *flowLog.FlowLogId
			if nameTag, ok := tags["Name"]; ok && len(nameTag) > 0 {
				name = nameTag[0]
			}

			// Map flow log status to resource status
			status := providers.ResourceStatusActive
			if flowLog.FlowLogStatus != nil && *flowLog.FlowLogStatus == "INACTIVE" {
				status = providers.ResourceStatusInactive
			}

			resources = append(resources, providers.Resource{
				Id:          *flowLog.FlowLogId,
				ServiceName: ServiceNameVPC,
				Name:        name,
				Status:      status,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         fmt.Sprintf("arn:aws:ec2:%s:%s:vpc-flow-log/%s", regionName, account.AccountNumber, *flowLog.FlowLogId),
				CreatedAt:   aws.ToTime(flowLog.CreationTime),
				Type:        getAwsServiceResourceType(ServiceNameVPC, "vpc-flow-log"),
			})
		}
	}

	return resources, nil
}

// getEniName extracts a meaningful name from ENI description or uses ID
func getEniName(eni ec2types.NetworkInterface) string {
	if eni.Description != nil && *eni.Description != "" {
		return *eni.Description
	}
	if eni.NetworkInterfaceId != nil {
		return *eni.NetworkInterfaceId
	}
	return "unknown-eni"
}
func (a *amazonVpc) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return recommendations, err
	}
	for _, resource := range existingResources {
		svc := ec2.NewFromConfig(cfg)

		// unallocated elastic ips
		addresses, err := svc.DescribeAddresses(context.TODO(), &ec2.DescribeAddressesInput{})
		if err != nil {
			ctx.GetLogger().Error("Error getting elastic ips", "error", err)
			continue
		}
		for _, address := range addresses.Addresses {
			if address.AssociationId == nil {
				cost := 0.0
				usageType, err := getUsageType(cfg.Region)
				if err != nil {
					ctx.GetLogger().Error("Error getting elastic ip pricing", "error", err)
					continue
				}
				prices, err := getAvailableInstancesFromPricing(cfg, "AmazonVPC", map[string]string{
					"productFamily": "IP Address",
					"usagetype":     usageType,
				})
				if err != nil {
					ctx.GetLogger().Error("Error getting elastic ip pricing", "error", err)
				} else {
					if len(prices) > 0 {
						price, err := getPricingValue(prices[0])
						if err != nil {
							ctx.GetLogger().Error("Error getting elastic ip pricing", "error", err)
						} else {
							cost = price * 24 * 30
						}
					}
				}
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategoryRightSizing,
					RuleName:     "aws_vpc_unallocated_elastic_ip",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      cost,
					Data: map[string]any{
						"elastic_ip": *address.PublicIp,
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
	return recommendations, nil
}

func (a *amazonVpc) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return "", err
	}
	cfg.Region = region

	// First, try to find VPC Flow Logs using DescribeFlowLogs
	ec2Svc := ec2.NewFromConfig(cfg)
	flowLogsOutput, err := ec2Svc.DescribeFlowLogs(context.TODO(), &ec2.DescribeFlowLogsInput{
		Filter: []ec2types.Filter{
			{
				Name:   aws.String("resource-id"),
				Values: []string{resourceId},
			},
		},
	})

	if err == nil && flowLogsOutput != nil && len(flowLogsOutput.FlowLogs) > 0 {
		// Find flow logs that go to CloudWatch Logs
		for _, flowLog := range flowLogsOutput.FlowLogs {
			if flowLog.LogDestinationType == ec2types.LogDestinationTypeCloudWatchLogs {
				if flowLog.LogGroupName != nil && *flowLog.LogGroupName != "" {
					ctx.GetLogger().Info("found VPC Flow Logs in CloudWatch",
						"resourceId", resourceId,
						"logGroup", *flowLog.LogGroupName,
						"flowLogId", *flowLog.FlowLogId)
					return *flowLog.LogGroupName, nil
				}
			}
		}
	}

	// Fallback: generic log group search by log stream prefix
	ctx.GetLogger().Debug("VPC Flow Logs not found via DescribeFlowLogs, falling back to generic search",
		"resourceId", resourceId)

	logsSvc := cloudwatchlogs.NewFromConfig(cfg)
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
				LogGroupName:        aws.String(logGroupName),
				LogStreamNamePrefix: aws.String(resourceId),
				Limit:               aws.Int32(1),
			})
			if err == nil && len(describeLogStreamsOutput.LogStreams) > 0 {
				foundLogGroup = logGroupName
				return foundLogGroup, nil
			}
		}
	}
	return foundLogGroup, err
}

func (a *amazonVpc) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "vpc",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	// session, err := getAwsSessionFromAccount(account)
	// if err != nil {
	// 	ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
	// 	return providers.ServiceMapApplication{}, err
	// }

	return app, nil
}

func getUsageType(region string) (string, error) {
	regionMap := map[string]string{
		"us-east-1":      "USE1",
		"us-east-2":      "USE2",
		"us-west-1":      "USW1",
		"us-west-2":      "USW2",
		"af-south-1":     "AFS1",
		"ap-east-1":      "APE1",
		"ap-south-1":     "APS1",
		"ap-northeast-1": "APN1",
		"ap-northeast-2": "APN2",
		"ap-northeast-3": "APN3",
		"ap-southeast-1": "APS1",
		"ap-southeast-2": "APS2",
		"ca-central-1":   "CAN1",
		"eu-central-1":   "EUC1",
		"eu-west-1":      "EU",
		"eu-west-2":      "EUW2",
		"eu-south-1":     "EUS1",
		"eu-west-3":      "EUW3",
		"eu-north-1":     "EUN1",
		"me-south-1":     "MES1",
		"sa-east-1":      "SAE1",
	}
	if val, ok := regionMap[region]; ok {
		return fmt.Sprintf("%s-ElasticIP:IdleAddress", val), nil
	}
	return "", fmt.Errorf("region not found")
}
