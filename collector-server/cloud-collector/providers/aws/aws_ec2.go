package aws

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func ec2StatusToNbStatus(status string) providers.ResourceStatus {
	switch strings.ToLower(status) {
	case "running", "pending":
		return providers.ResourceStatusActive
	case "terminated":
		return providers.ResourceStatusDeleted
	case "stopped", "stopping", "shutting-down":
		return providers.ResourceStatusInactive
	default:
		return providers.ResourceStatusUnknown
	}
}

func getEc2PricesBasedOnPriceList(region string, instanceType string) (float64, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return 0, fmt.Errorf("ec2 pricing: failed to get database manager: %w", err)
	}
	var result struct {
		ResourceCost float64 `db:"resource_cost"`
	}
	err = dbms.QueryRowAndScan(&result,
		`SELECT resource_cost FROM cloud_resource_details
		 WHERE cloud_provider = 'aws' AND service_type = 'Compute'
		   AND resource_region = $1 AND resource_type = $2
		   AND pricing_model = 'on_demand'
		 LIMIT 1`,
		region, instanceType,
	)
	if err == nil {
		return result.ResourceCost, nil
	}

	// Fallback: fetch from AWS Pricing API and persist to DB
	cacheKey := region + ":" + instanceType
	if expiry, ok := pricingFailureCache.Load(cacheKey); ok {
		if time.Now().Before(expiry.(time.Time)) {
			return 0, nil // skip — recently failed
		}
		pricingFailureCache.Delete(cacheKey)
	}

	slog.Info("ec2 pricing: not found in DB, fetching from AWS Pricing API", "region", region, "instanceType", instanceType)
	price, fetchErr := fetchAndStoreEc2Price(dbms, region, instanceType)
	if fetchErr != nil {
		slog.Warn("ec2 pricing: fallback fetch failed", "region", region, "instanceType", instanceType, "error", fetchErr)
		pricingFailureCache.Store(cacheKey, time.Now().Add(pricingFailureCacheTTL))
		return 0, nil
	}
	return price, nil
}

// pricingFailureCache tracks instance types that failed pricing API lookups
// to avoid repeated futile API calls when the pod lacks pricing:GetProducts permission.
var pricingFailureCache sync.Map // key: "region:instanceType", value: time.Time (expiry)

const pricingFailureCacheTTL = 1 * time.Hour

// parseAwsMemoryGiB parses AWS Pricing API memory strings to a GiB float value.
// Handles: "1 GiB", "1,952 GiB" (comma-separated thousands), "512 MiB", "24 TiB".
// Returns 0 on empty input or parse failure.
func parseAwsMemoryGiB(memStr string) float64 {
	memStr = strings.TrimSpace(memStr)
	if memStr == "" {
		return 0
	}
	parts := strings.Fields(memStr) // e.g. ["1,952", "GiB"]
	if len(parts) == 0 {
		return 0
	}
	// Strip thousands-separator commas: "1,952" → "1952"
	numStr := strings.ReplaceAll(parts[0], ",", "")
	value, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0
	}
	if len(parts) >= 2 {
		switch strings.ToUpper(parts[1]) {
		case "MIB":
			return value / 1024
		case "TIB":
			return value * 1024
		}
	}
	return value
}

func fetchAndStoreEc2Price(dbms *common.DatabaseManager, region string, instanceType string) (float64, error) {
	cfg, err := awsconfig.LoadDefaultConfig(context.TODO(), awsconfig.WithRegion("us-east-1"))
	if err != nil {
		return 0, fmt.Errorf("failed to load AWS config: %w", err)
	}

	instances, err := getAvailableEc2Instances(cfg, region, 0, 0, instanceType, "Linux", "")
	if err != nil {
		return 0, fmt.Errorf("failed to fetch from pricing API: %w", err)
	}
	if len(instances) == 0 {
		return 0, fmt.Errorf("no pricing data returned for %s in %s", instanceType, region)
	}

	price, err := getPricingValue(instances[0])
	if err != nil {
		return 0, fmt.Errorf("failed to extract price: %w", err)
	}

	// Persist to DB for future lookups
	attrs := "{}"
	if product, ok := instances[0]["product"].(map[string]any); ok {
		if a, ok := product["attributes"].(map[string]any); ok {
			if b, err := common.MarshalJson(a); err == nil {
				attrs = string(b)
			}
		}
	}

	capacity := "{}"
	if product, ok := instances[0]["product"].(map[string]any); ok {
		if a, ok := product["attributes"].(map[string]any); ok {
			vcpu, _ := a["vcpu"].(string)
			mem, _ := a["memory"].(string)
			memGiB := parseAwsMemoryGiB(mem) // "1 GiB" → 1.0, "1,952 GiB" → 1952.0
			if c, err := common.MarshalJson(map[string]any{"cpu_virtual": vcpu, "memory_gb": memGiB}); err == nil {
				capacity = string(c)
			}
		}
	}

	_, err = dbms.Exec(
		`INSERT INTO cloud_resource_details
		 (cloud_provider, service_name, service_type, resource_type, resource_region,
		  resource_cost, resource_capacity, attributes, operating_system, tenancy,
		  pricing_model, price_unit, database_engine, deployment_option)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		 ON CONFLICT (cloud_provider, service_name, service_type, resource_type, resource_region,
		              pricing_model, database_engine, deployment_option)
		 DO UPDATE SET resource_cost = EXCLUDED.resource_cost, attributes = EXCLUDED.attributes,
		              resource_capacity = EXCLUDED.resource_capacity`,
		"aws", "AmazonEC2", "Compute", instanceType, region,
		price, capacity, attrs, "Linux", "Shared",
		"on_demand", "hourly", "", "",
	)
	if err != nil {
		slog.Warn("ec2 pricing: failed to persist fallback price to DB", "region", region, "instanceType", instanceType, "error", err)
		// Still return the price even if DB persist fails
	}

	return price, nil
}

func getAvailableEc2Instances(cfg aws.Config, region string, memory int, cpu int, instanceType string, operatingSystem string, storageType string) ([]map[string]interface{}, error) {
	filtersMap := map[string]string{}
	if region != "" {
		filtersMap["regionCode"] = region
	}
	if instanceType != "" {
		filtersMap["instanceType"] = instanceType
	}
	if memory > 0 {
		filtersMap["memory"] = fmt.Sprint(memory) + " GiB"
	}
	if cpu > 0 {
		filtersMap["vcpu"] = fmt.Sprint(cpu)
	}

	if operatingSystem != "" {
		filtersMap["operatingSystem"] = operatingSystem
	}

	if storageType != "" {
		filtersMap["storage"] = storageType
	}

	instances, err := getAvailableInstancesFromPricing(cfg, "AmazonEC2", filtersMap)
	if err != nil {
		return nil, err
	}
	// The type of instances is []aws.JSONValue, which is an alias for []map[string]interface{}
	// so we can return it directly.
	return instances, nil
}

type amazonEc2 struct {
	DefaultAwsServiceImpl
}

func (a *amazonEc2) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Check if this is an alarm recommendation
	if strings.HasPrefix(recommendation.RuleName, "aws_ec2_") && strings.HasSuffix(recommendation.RuleName, "_alarm_missing") {
		// This is an alarm recommendation - create the CloudWatch alarm
		err := CreateCloudWatchAlarmFromRecommendation(ctx.GetContext(), account, recommendation)
		if err != nil {
			ctx.GetLogger().Error("Failed to create CloudWatch alarm", "error", err, "ruleName", recommendation.RuleName, "resourceId", recommendation.ResourceId)
			return fmt.Errorf("failed to create CloudWatch alarm: %w", err)
		}
		ctx.GetLogger().Info("Successfully created CloudWatch alarm", "ruleName", recommendation.RuleName, "resourceId", recommendation.ResourceId)
		return nil
	}

	// Other recommendations not yet supported
	return errors.ErrUnsupported
}

func (a *amazonEc2) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	var resultMessage string
	var resultErr error
	var instanceIDs []string

	// Always audit, even on early returns
	defer func() {
		status := "SUCCESS"
		if resultErr != nil {
			status = "FAILURE"
		}

		// Use batch audit logging if we have multiple instance IDs
		var auditErr error
		if len(instanceIDs) > 1 {
			auditErr = logResourceActionAuditBatch(ctx, command, account, status, resultMessage, instanceIDs)
		} else {
			auditErr = logResourceActionAudit(ctx, command, account, status, resultMessage)
		}

		if auditErr != nil {
			ctx.GetLogger().Warn("failed to log audit record", "error", auditErr)
		}
	}()

	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		resultErr = fmt.Errorf("failed to get AWS config: %w", err)
		resultMessage = resultErr.Error()
		return providers.ApplyCommandResponse{}, resultErr
	}

	// Override region if specified in command
	if command.Region != "" {
		cfg.Region = command.Region
	}

	client := ec2.NewFromConfig(cfg)

	// Parse instance IDs - support single or multiple instances
	if command.ResourceId != "" {
		instanceIDs = []string{command.ResourceId}
	} else if ids, ok := command.Args["instance_ids"].([]interface{}); ok {
		for _, id := range ids {
			if idStr, ok := id.(string); ok {
				instanceIDs = append(instanceIDs, idStr)
			}
		}
	} else if ids, ok := command.Args["instance_ids"].([]string); ok {
		instanceIDs = ids
	}

	if len(instanceIDs) == 0 {
		resultErr = fmt.Errorf("instance_id(s) required")
		resultMessage = resultErr.Error()
		return providers.ApplyCommandResponse{}, resultErr
	}

	switch command.Command {
	case "start":
		output, err := client.StartInstances(ctx.GetContext(), &ec2.StartInstancesInput{
			InstanceIds: instanceIDs,
		})
		if err != nil {
			resultErr = fmt.Errorf("failed to start instances: %w", err)
			resultMessage = resultErr.Error()
		} else {
			resultMessage = fmt.Sprintf("Successfully started %d instance(s)", len(output.StartingInstances))
		}

	case "stop":
		output, err := client.StopInstances(ctx.GetContext(), &ec2.StopInstancesInput{
			InstanceIds: instanceIDs,
		})
		if err != nil {
			resultErr = fmt.Errorf("failed to stop instances: %w", err)
			resultMessage = resultErr.Error()
		} else {
			resultMessage = fmt.Sprintf("Successfully stopped %d instance(s)", len(output.StoppingInstances))
		}

	case "reboot":
		_, err := client.RebootInstances(ctx.GetContext(), &ec2.RebootInstancesInput{
			InstanceIds: instanceIDs,
		})
		if err != nil {
			resultErr = fmt.Errorf("failed to reboot instances: %w", err)
			resultMessage = resultErr.Error()
		} else {
			resultMessage = fmt.Sprintf("Successfully rebooted %d instance(s)", len(instanceIDs))
		}

	default:
		resultErr = fmt.Errorf("unsupported command: %s", command.Command)
		resultMessage = resultErr.Error()
	}

	if resultErr != nil {
		return providers.ApplyCommandResponse{Success: false, Message: resultMessage}, resultErr
	}

	return providers.ApplyCommandResponse{Success: true, Message: resultMessage}, nil
}

func (a *amazonEc2) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *amazonEc2) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber, "region", region)
		return []providers.Resource{}, err
	}
	cfg.Region = region
	svc := ec2.NewFromConfig(cfg)
	resources := []providers.Resource{}
	instanceTypeMap := make(map[string]any)

	// Fetch all CloudWatch alarms for this region once, instead of per-instance.
	// This avoids N separate STS AssumeRole + DescribeAlarms calls that can exhaust
	// the request context timeout for accounts with many instances.
	alarmsByResource := fetchAlarmsByResource(ctx, account, region)

	instancesPaginator := ec2.NewDescribeInstancesPaginator(svc, &ec2.DescribeInstancesInput{})
	for instancesPaginator.HasMorePages() {
		instancesOutput, err := instancesPaginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch ec2 instances", "error", err, "accountNumber", account.AccountNumber, "region", region)
			return resources, err
		}

		for _, reservation := range instancesOutput.Reservations {
			for _, instance := range reservation.Instances {
				if instance.InstanceId == nil {
					ctx.GetLogger().Warn("Skipping EC2 instance due to missing InstanceId", "instance", instance, "region", region)
					continue
				}
				if instance.State == nil {
					ctx.GetLogger().Warn("Skipping EC2 instance due to missing State", "instanceId", *instance.InstanceId, "region", region)
					continue
				}
				if instance.State.Name == "" {
					ctx.GetLogger().Warn("Skipping EC2 instance due to missing State.Name", "instanceId", *instance.InstanceId, "region", region)
					continue
				}
				if instance.LaunchTime == nil {
					ctx.GetLogger().Warn("Skipping EC2 instance due to missing LaunchTime", "instanceId", *instance.InstanceId, "region", region)
					continue
				}
				if instance.InstanceType == "" {
					ctx.GetLogger().Warn("Skipping EC2 instance due to missing InstanceType", "instanceId", *instance.InstanceId, "region", region)
					continue
				}

				instanceType := string(instance.InstanceType)
				if _, ok := instanceTypeMap[instanceType]; !ok {
					instanceData := map[string]any{}

					instanceTypeDetailsOutput, err := svc.DescribeInstanceTypes(ctx.GetContext(), &ec2.DescribeInstanceTypesInput{
						InstanceTypes: []types.InstanceType{instance.InstanceType},
					})
					if err != nil {
						ctx.GetLogger().Warn("failed to fetch ec2 instance type details", "error", err, "instanceType", instanceType, "instanceId", *instance.InstanceId, "region", region)
					} else if instanceTypeDetailsOutput == nil || len(instanceTypeDetailsOutput.InstanceTypes) == 0 {
						ctx.GetLogger().Warn("DescribeInstanceTypes returned no details or empty InstanceTypes slice", "instanceType", instanceType, "instanceId", *instance.InstanceId, "region", region)
					} else {
						instanceData = structToMap(instanceTypeDetailsOutput.InstanceTypes[0])
					}

					instancePrice, err := getEc2PricesBasedOnPriceList(region, instanceType)
					if err != nil {
						ctx.GetLogger().Warn("failed to fetch ec2 instance price", "error", err, "instanceType", instanceType, "instanceId", *instance.InstanceId, "region", region)
					} else {
						instanceData["Price"] = instancePrice
					}
					instanceTypeMap[instanceType] = instanceData
				}

				tags := make(map[string][]string)
				for _, tag := range instance.Tags {
					if tag.Key != nil && tag.Value != nil {
						tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
					}
				}

				name := *instance.InstanceId
				if nameTag, ok := tags["Name"]; ok && len(nameTag) > 0 {
					name = nameTag[0]
				}

				meta := structToMap(instance)
				meta["InstanceTypeDetails"] = instanceTypeMap[instanceType]

				meta["AlarmDetails"] = alarmsByResource[*instance.InstanceId]

				resource := providers.Resource{
					Id:          *instance.InstanceId,
					ServiceName: ServiceNameEc2,
					Name:        name,
					Arn:         fmt.Sprintf("arn:aws:ec2:%s:%s:instance/%s", region, account.AccountNumber, *instance.InstanceId),
					Status:      ec2StatusToNbStatus(string(instance.State.Name)),
					Region:      region,
					Tags:        tags,
					Meta:        meta,
					Type:        getAwsServiceResourceType(ServiceNameEc2, "compute-instance"),
					CreatedAt:   *instance.LaunchTime,
				}
				resources = append(resources, resource)
			}
		}
	}

	return a.fetchVolumesAndSnapshots(ctx, account, region, svc, resources)
}

// GetResourcesByIds fetches specific EC2 instances by their IDs using server-side filtering.
// This avoids the full DescribeInstances + per-instance DescribeAlarms scan that GetResources does.
func (a *amazonEc2) GetResourcesByIds(ctx providers.CloudProviderContext, account providers.Account, region string, resourceIds []string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		return nil, err
	}
	cfg.Region = region
	svc := ec2.NewFromConfig(cfg)
	resources := []providers.Resource{}
	instanceTypeMap := make(map[string]any)

	output, err := svc.DescribeInstances(ctx.GetContext(), &ec2.DescribeInstancesInput{
		InstanceIds: resourceIds,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe instances by ids: %w", err)
	}

	for _, reservation := range output.Reservations {
		for _, instance := range reservation.Instances {
			if instance.InstanceId == nil || instance.State == nil || instance.State.Name == "" || instance.LaunchTime == nil || instance.InstanceType == "" {
				continue
			}

			instanceType := string(instance.InstanceType)
			if _, ok := instanceTypeMap[instanceType]; !ok {
				instanceData := map[string]any{}
				instanceTypeDetailsOutput, err := svc.DescribeInstanceTypes(ctx.GetContext(), &ec2.DescribeInstanceTypesInput{
					InstanceTypes: []types.InstanceType{instance.InstanceType},
				})
				if err != nil {
					ctx.GetLogger().Warn("failed to fetch ec2 instance type details", "error", err, "instanceType", instanceType)
				} else if instanceTypeDetailsOutput != nil && len(instanceTypeDetailsOutput.InstanceTypes) > 0 {
					instanceData = structToMap(instanceTypeDetailsOutput.InstanceTypes[0])
				}
				instancePrice, err := getEc2PricesBasedOnPriceList(region, instanceType)
				if err == nil {
					instanceData["Price"] = instancePrice
				}
				instanceTypeMap[instanceType] = instanceData
			}

			tags := make(map[string][]string)
			for _, tag := range instance.Tags {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}

			name := *instance.InstanceId
			if nameTag, ok := tags["Name"]; ok && len(nameTag) > 0 {
				name = nameTag[0]
			}

			meta := structToMap(instance)
			meta["InstanceTypeDetails"] = instanceTypeMap[instanceType]
			// Skip alarm fetching — this is a targeted fetch for event processing (e.g., EventBridge).
			// Alarms are only needed during full periodic resource syncs via GetResources.

			resources = append(resources, providers.Resource{
				Id:          *instance.InstanceId,
				ServiceName: ServiceNameEc2,
				Name:        name,
				Arn:         fmt.Sprintf("arn:aws:ec2:%s:%s:instance/%s", region, account.AccountNumber, *instance.InstanceId),
				Status:      ec2StatusToNbStatus(string(instance.State.Name)),
				Region:      region,
				Tags:        tags,
				Meta:        meta,
				Type:        getAwsServiceResourceType(ServiceNameEc2, "compute-instance"),
				CreatedAt:   *instance.LaunchTime,
			})
		}
	}

	return resources, nil
}

// fetchVolumesAndSnapshots appends EBS volumes and snapshots to the given resources slice.
func (a *amazonEc2) fetchVolumesAndSnapshots(ctx providers.CloudProviderContext, account providers.Account, region string, svc *ec2.Client, resources []providers.Resource) ([]providers.Resource, error) {
	volumesPaginator := ec2.NewDescribeVolumesPaginator(svc, &ec2.DescribeVolumesInput{})
	for volumesPaginator.HasMorePages() {
		volumesOutput, err := volumesPaginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch ec2 volumes", "error", err, "accountNumber", account.AccountNumber, "region", region)
			return resources, err
		}

		for _, volume := range volumesOutput.Volumes {
			if volume.VolumeId == nil {
				ctx.GetLogger().Warn("Skipping EC2 volume due to missing VolumeId", "volume", volume, "region", region)
				continue
			}
			if volume.CreateTime == nil {
				ctx.GetLogger().Warn("Skipping EC2 volume due to missing CreateTime", "volumeId", *volume.VolumeId, "region", region)
				continue
			}

			status := providers.ResourceStatusUnknown
			if volume.State == "" {
				ctx.GetLogger().Warn("EC2 volume State is nil, assuming Unknown status", "volumeId", *volume.VolumeId, "region", region)
			} else {
				switch volume.State {
				case types.VolumeStateCreating:
					status = providers.ResourceStatusUnknown
				case types.VolumeStateAvailable, types.VolumeStateInUse:
					status = providers.ResourceStatusActive
				case types.VolumeStateDeleting, types.VolumeStateDeleted:
					status = providers.ResourceStatusDeleted
				case types.VolumeStateError:
					status = providers.ResourceStatusInactive
				}
			}

			tags := make(map[string][]string)
			for _, tag := range volume.Tags {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}
			arn := fmt.Sprintf("arn:aws:ec2:%s:%s:volume/%s", region, account.AccountNumber, *volume.VolumeId)

			resource := providers.Resource{
				Id:          *volume.VolumeId,
				ServiceName: ServiceNameEc2,
				Name:        *volume.VolumeId,
				Status:      status,
				Region:      region,
				Tags:        tags,
				Meta:        structToMap(volume),
				Arn:         arn,
				Type:        getAwsServiceResourceType(ServiceNameEc2, "storage"),
				CreatedAt:   *volume.CreateTime,
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

// https://www.trendmicro.com/cloudoneconformity-staging/knowledge-base/aws/EC2/
func (a *amazonEc2) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber)
		return nil, err
	}
	startDate := time.Now().Add(-time.Hour * 24 * 7)
	endDate := time.Now()
	for _, resource := range existingResources {
		if resource.Type == "storage" && len(resource.Meta) > 0 {
			size := 0.0
			if s, ok := resource.Meta["Size"].(float64); ok {
				size = s
			} else {
				ctx.GetLogger().Warn("Skipping volume specific recommendation due to missing or incorrect type for Size", "resourceId", resource.Id, "region", resource.Region)
			}

			var state string
			if st, ok := resource.Meta["State"].(string); ok {
				state = st
			} else {
				ctx.GetLogger().Warn("Volume State missing or not a string, skipping orphan check", "resourceId", resource.Id, "region", resource.Region)
				// Skip this specific recommendation if state is not available or not a string
			}

			if state == "available" { // Only proceed if state is valid and "available"
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategoryRightSizing,
					RuleName:     "aws_ec2_orphaned_volume",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      size * 0.1,
					Data: map[string]any{
						"volume_id":     resource.Id,
						"volume_arn":    resource.Arn,
						"volume_type":   resource.Meta["VolumeType"],
						"volume_region": resource.Region,
						"volume_size":   size,
						"volume_state":  state,
					},
					Action:              providers.RecommendationActionDelete,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}

			// check for gp2 volumes and recommend to upgrade to gp3
			if volumeType, ok := resource.Meta["VolumeType"].(string); ok && volumeType == "gp2" {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategoryInfraUpgrade,
					RuleName:     "aws_ec2_ebs_generation_upgrade",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      size * 0.02,
					Data: map[string]any{
						"volume_id":                resource.Id,
						"volume_arn":               resource.Arn,
						"volume_type":              resource.Meta["VolumeType"],
						"volume_region":            resource.Region,
						"volume_size":              size,
						"recommendded_volume_type": "gp3",
						"volume_state":             resource.Meta["State"],
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}

			// check for encryption
			if encryption, ok := resource.Meta["Encrypted"].(bool); ok && !encryption {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "aws_ec2_ebs_encrypt",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"volume_id":     resource.Id,
						"volume_arn":    resource.Arn,
						"volume_type":   resource.Meta["VolumeType"],
						"volume_region": resource.Region,
						"volume_size":   size,
						"volume_state":  resource.Meta["State"],
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

		// stopped compute instances are charged for storage
		if resource.Type == "compute-instance" && len(resource.Meta) > 0 {
			var instanceTypeDetailsMap map[string]any
			currentPrice := 0.0
			cfg.Region = resource.Region
			svc := ec2.NewFromConfig(cfg)

			if itd, ok := resource.Meta["InstanceTypeDetails"].(map[string]any); ok {
				instanceTypeDetailsMap = itd
				if price, ok := instanceTypeDetailsMap["Price"].(float64); ok {
					currentPrice = price
				}
			}

			var resourceState string
			var stateOk bool
			if stateMap, ok := resource.Meta["State"].(map[string]any); ok {
				resourceState, stateOk = stateMap["Name"].(string)
			} else if stateStr, ok := resource.Meta["State"].(string); ok {
				resourceState = stateStr
				stateOk = true
			}

			if !stateOk {
				ctx.GetLogger().Debug("Instance State missing or not a string", "resourceId", resource.Id, "region", resource.Region)
			}

			if stateOk && resourceState == "stopped" {
				totalStorage := 0.0
				if blockDeviceMappingsAny, ok := resource.Meta["BlockDeviceMappings"]; ok {
					if blockDeviceMappings, ok := blockDeviceMappingsAny.([]any); ok {
						volumes := []string{}
						for _, blockDeviceMappingAny := range blockDeviceMappings {
							if blockDeviceMapping, ok := blockDeviceMappingAny.(map[string]any); ok {
								if ebs, ok := blockDeviceMapping["Ebs"].(map[string]any); ok {
									if volumeId, ok := ebs["VolumeId"].(string); ok {
										volumes = append(volumes, volumeId)
									}
								}
							}
						}
						for _, r := range existingResources { // Use different variable name to avoid conflict
							if r.Type == "storage" && len(r.Meta) > 0 {
								for _, volume := range volumes {
									if r.Id == volume {
										if size, ok := r.Meta["Size"].(float64); ok {
											totalStorage += size
										}
									}
								}
							}
						}
					} else {
						ctx.GetLogger().Warn("BlockDeviceMappings is not a slice", "resourceId", resource.Id, "region", resource.Region)
					}
				}

				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategoryRightSizing, // Or InfraUpgrade if the goal is to use it or lose it
					RuleName:     "aws_ec2_stopped_instance_incurring_storage_cost",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0.08 * totalStorage,
					Data: map[string]any{
						"instance_id":     resource.Id,
						"instance_arn":    resource.Arn,
						"instance_type":   resource.Meta["InstanceType"], // Assumes InstanceType exists, consider safe access if needed
						"instance_region": resource.Region,
						"instance_state":  resourceState,
					},
					Action:              providers.RecommendationActionDelete, // Or Review
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}

			//varify public ip address assignment, amazon charges for public ip addresses
			if networkInterfacesAny, ok := resource.Meta["NetworkInterfaces"]; ok {
				if networkInterfaces, ok := networkInterfacesAny.([]any); ok {
					for _, networkInterfaceAny := range networkInterfaces {
						if networkInterface, ok := networkInterfaceAny.(map[string]any); ok {
							if associationAny, ok := networkInterface["Association"]; ok && associationAny != nil {
								if associationMap, ok := associationAny.(map[string]any); ok {
									if publicIp, ok := associationMap["PublicIp"]; ok && publicIp != nil {
										if ipOwnerId, ok := associationMap["IpOwnerId"].(string); ok && ipOwnerId != "amazon" {
											recommendation := providers.Recommendation{
												CategoryName: providers.RecommendationCategorySecurity,
												RuleName:     "aws_ec2_instance_public_ip",
												Severity:     providers.RecommendationSeverityMedium,
												Savings:      0,
												Data: map[string]any{
													"instance_id":     resource.Id,
													"instance_arn":    resource.Arn,
													"instance_type":   resource.Meta["InstanceType"],
													"instance_region": resource.Region,
													"instance_state":  resource.Meta["State"],
													"public_ip":       publicIp,
												},
												Action:              providers.RecommendationActionModify,
												ResourceServiceName: resource.ServiceName,
												ResourceId:          resource.Id,
												ResourceType:        resource.Type,
												ResourceRegion:      resource.Region,
											}
											recommendations = append(recommendations, recommendation)
											break // Found one, no need to check other interfaces for this instance
										}
									}
								} else {
									ctx.GetLogger().Warn("Network interface Association is not a map", "resourceId", resource.Id, "interfaceId", networkInterface["NetworkInterfaceId"])
								}
							}
						} else {
							ctx.GetLogger().Warn("Network interface in NetworkInterfaces slice is not a map", "resourceId", resource.Id)
						}
					}
				} else {
					ctx.GetLogger().Warn("NetworkInterfaces is not a slice", "resourceId", resource.Id, "region", resource.Region)
				}
			}

			// ec2 should be running on private subnets
			if networkInterfacesAny, ok := resource.Meta["NetworkInterfaces"]; ok {
				if networkInterfaces, ok := networkInterfacesAny.([]any); ok {
					for _, networkInterfaceAny := range networkInterfaces {
						if networkInterface, ok := networkInterfaceAny.(map[string]any); ok {
							if subnetId, ok := networkInterface["SubnetId"].(string); ok {
								if isPublicSubnet(ctx.GetContext(), cfg, subnetId) {
									recommendation := providers.Recommendation{
										CategoryName: providers.RecommendationCategorySecurity,
										RuleName:     "aws_ec2_instance_public_subnet",
										Severity:     providers.RecommendationSeverityMedium,
										Savings:      0,
										Data: map[string]any{
											"instance_id":     resource.Id,
											"instance_arn":    resource.Arn,
											"instance_type":   resource.Meta["InstanceType"],
											"instance_region": resource.Region,
											"instance_state":  resource.Meta["State"],
											"subnet_id":       subnetId,
										},
										Action:              providers.RecommendationActionModify,
										ResourceServiceName: resource.ServiceName,
										ResourceId:          resource.Id,
										ResourceType:        resource.Type,
										ResourceRegion:      resource.Region,
									}
									recommendations = append(recommendations, recommendation)
									break // Found one, no need to check other interfaces
								}
							} else {
								ctx.GetLogger().Warn("SubnetId not found or not a string in network interface", "resourceId", resource.Id, "interfaceId", networkInterface["NetworkInterfaceId"])
							}
						} else {
							ctx.GetLogger().Warn("Network interface in NetworkInterfaces slice is not a map", "resourceId", resource.Id)
						}
					}
				} else {
					ctx.GetLogger().Warn("NetworkInterfaces is not a slice", "resourceId", resource.Id, "region", resource.Region)
				}
			}

			// Recommend enabling termination protection if API termination is not disabled.
			apiTerminationDisabled := false
			if disableAPITermination, ok := resource.Meta["DisableApiTermination"].(bool); ok {
				apiTerminationDisabled = disableAPITermination
			}
			if !apiTerminationDisabled {
				recommendation := providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_ec2_instance_termination_protection_disabled",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"instance_id": resource.Id, "instance_arn": resource.Arn, "reason": "API termination protection is not enabled."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}

			// Also, InstanceInitiatedShutdownBehavior == "terminate" is a risk.
			if shutdownBehavior, ok := resource.Meta["InstanceInitiatedShutdownBehavior"].(string); ok {
				if shutdownBehavior == "terminate" {
					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "aws_ec2_instance_terminates_on_os_shutdown",
						Severity:     providers.RecommendationSeverityMedium,
						Savings:      0,
						Data: map[string]any{
							"instance_id":                          resource.Id,
							"instance_arn":                         resource.Arn,
							"instance_initiated_shutdown_behavior": shutdownBehavior,
							"reason":                               "Instance is configured to terminate when shut down from the OS. Consider changing to 'stop'.",
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					}
					recommendations = append(recommendations, recommendation)
				}
			} else {
				ctx.GetLogger().Warn("InstanceInitiatedShutdownBehavior not found or not a string", "resourceId", resource.Id, "region", resource.Region)
			}

			// misisng tags
			if len(resource.Tags) == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_tags",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// idle instances
			// Assume not idle if metrics are missing to prevent incorrect recommendations.
			isIdle := false // Default to not idle
			var cpuMetrics providers.QueryMetricsResponse
			var errCpuMetrics error

			// Only fetch metrics if the instance is not recently launched (e.g., older than 1 day)
			// This is a placeholder for a more sophisticated check, for now, we fetch for all.
			// if time.Since(resource.CreatedAt) > 24*time.Hour {
			cpuMetrics, errCpuMetrics = a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
				ResourceIds: []string{resource.Id},
				ServiceName: resource.ServiceName,
				StartDate:   &startDate,
				EndDate:     &endDate,
				Region:      resource.Region,
				MetricNames: []string{"CPUUtilization"},
				Step:        3600 * time.Second, // 1 hour step
				Statistics:  []string{"Maximum"},
			})
			if errCpuMetrics != nil {
				ctx.GetLogger().Error("Error getting CPU metrics for idle check", "resourceId", resource.Id, "error", errCpuMetrics)
				// Do not proceed with idle check if CPU metrics failed. isIdle remains false.
			} else {
				// Check CPU metrics
				if len(cpuMetrics.Items) > 0 && len(cpuMetrics.Items[0].Values) > 0 {
					isIdle = true // Assume idle until a data point proves otherwise
					for _, v := range cpuMetrics.Items[0].Values {
						if v > 2 { // CPU Utilization > 2%
							isIdle = false
							break
						}
					}
				} else {
					// If no CPU data points, assume not idle (conservative approach)
					isIdle = false
					ctx.GetLogger().Info("No CPU metric data points available for idle check, assuming not idle", "resourceId", resource.Id)
				}
			}

			var ingressMetrics providers.QueryMetricsResponse
			if isIdle { // Only check network if CPU suggests idle
				ingress, err := a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
					ResourceIds: []string{resource.Id},
					ServiceName: resource.ServiceName,
					StartDate:   &startDate,
					EndDate:     &endDate,
					Region:      resource.Region,
					MetricNames: []string{"NetworkIn"},
					Step:        3600 * time.Second,
					Statistics:  []string{"Average"},
				})
				if err != nil {
					ctx.GetLogger().Error("Error getting NetworkIn metrics for idle check", "resourceId", resource.Id, "error", err)
					isIdle = false // If network metrics fail, assume not idle
				} else {
					if len(ingress.Items) > 0 && len(ingress.Items[0].Values) > 0 {
						totalValue := 0.0
						for _, metric := range ingress.Items[0].Values {
							totalValue += metric
						}
						if totalValue > 5*1024*1024 { // More than 5MB total over the period
							isIdle = false
						}
					} else {
						// No network data, could be truly idle or metrics missing. To be conservative, if CPU was low, keep isIdle true.
						// Or, change to isIdle = false if strict "data must be present" is required.
						// Current logic: if CPU is low and no network data, still consider idle.
						ctx.GetLogger().Info("No NetworkIn metric data points available for idle check", "resourceId", resource.Id)
					}
				}
				ingressMetrics = ingress // Store for reporting
			}

			var egressMetrics providers.QueryMetricsResponse
			if isIdle { // Only check network if still considered idle
				egress, err := a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
					ResourceIds: []string{resource.Id},
					ServiceName: resource.ServiceName,
					StartDate:   &startDate,
					EndDate:     &endDate,
					Region:      resource.Region,
					MetricNames: []string{"NetworkOut"},
					Step:        3600 * time.Second,
					Statistics:  []string{"Average"},
				})
				if err != nil {
					ctx.GetLogger().Error("Error getting NetworkOut metrics for idle check", "resourceId", resource.Id, "error", err)
					isIdle = false // If network metrics fail, assume not idle
				} else {
					if len(egress.Items) > 0 && len(egress.Items[0].Values) > 0 {
						totalValue := 0.0
						for _, metric := range egress.Items[0].Values {
							totalValue += metric
						}
						if totalValue > 5*1024*1024 { // More than 5MB total over the period
							isIdle = false
						}
					} else {
						ctx.GetLogger().Info("No NetworkOut metric data points available for idle check", "resourceId", resource.Id)
					}
				}
				egressMetrics = egress // Store for reporting
			}

			if isIdle {
				savings := 0.0
				if currentPrice > 0 { // Make sure price is available
					savings = currentPrice * 24 * 30
				} else {
					ctx.GetLogger().Warn("Cannot calculate savings for idle instance due to missing price", "resourceId", resource.Id)
				}

				idleData := map[string]any{
					"instance_id":     resource.Id,
					"instance_arn":    resource.Arn,
					"instance_type":   resource.Meta["InstanceType"],
					"instance_region": resource.Region,
					"instance_state":  resource.Meta["State"],
					"startDate":       startDate.Format(time.RFC3339),
					"endDate":         endDate.Format(time.RFC3339),
				}
				if len(cpuMetrics.Items) > 0 { // Safe access
					idleData["cpuUsage"] = cpuMetrics.Items[0]
				}
				if len(egressMetrics.Items) > 0 { // Safe access
					idleData["egress"] = egressMetrics.Items[0]
				}
				if len(ingressMetrics.Items) > 0 { // Safe access
					idleData["ingress"] = ingressMetrics.Items[0]
				}
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryRightSizing,
					RuleName:            "aws_ec2_idle_instance",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             savings,
					Data:                idleData,
					Action:              providers.RecommendationActionDelete,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// underutilized instances
			// Assume not underutilized if metrics or instance details are missing.
			isUnderUtilized := false // Default to not underutilized
			if errCpuMetrics == nil && len(cpuMetrics.Items) > 0 && len(cpuMetrics.Items[0].Values) > 0 {
				isUnderUtilized = true // Assume underutilized until a data point proves otherwise
				for _, v := range cpuMetrics.Items[0].Values {
					if v > 60 { // CPU Utilization > 60%
						isUnderUtilized = false
						break
					}
				}
			} else if errCpuMetrics != nil {
				ctx.GetLogger().Info("CPU metrics not available for underutilized check, skipping", "resourceId", resource.Id)
				isUnderUtilized = false
			} else {
				ctx.GetLogger().Info("No CPU metric data points available for underutilized check, assuming not underutilized", "resourceId", resource.Id)
				isUnderUtilized = false
			}

			if isUnderUtilized {
				if instanceTypeDetailsMap == nil {
					ctx.GetLogger().Warn("Skipping underutilized check: InstanceTypeDetailsMap is nil", "resourceId", resource.Id)
				} else {
					vCpuInfo, vCpuOk := instanceTypeDetailsMap["VCpuInfo"].(map[string]any)
					memInfo, memOk := instanceTypeDetailsMap["MemoryInfo"].(map[string]any)

					if !vCpuOk || !memOk {
						ctx.GetLogger().Warn("Skipping underutilized check: VCpuInfo or MemoryInfo missing or not a map", "resourceId", resource.Id)
					} else {
						defaultVCpusFloat, vCpuValOk := vCpuInfo["DefaultVCpus"].(float64)
						sizeInMiBFloat, memValOk := memInfo["SizeInMiB"].(float64)

						if !vCpuValOk || !memValOk {
							ctx.GetLogger().Warn("Skipping underutilized check: DefaultVCpus or SizeInMiB missing or not float64", "resourceId", resource.Id)
						} else {
							recommendedCPU := int64(defaultVCpusFloat / 2)
							if recommendedCPU == 0 {
								recommendedCPU = 1
							}
							recommendedMemory := int64(sizeInMiBFloat / 2) // MiB

							// reduce memory and cpu by 50pct
							// Ensure Architecture and VirtualizationType are present before calling GetInstanceTypesFromInstanceRequirements
							architecture, archOk := resource.Meta["Architecture"].(string)
							virtualizationType, virtOk := resource.Meta["VirtualizationType"].(string)

							if !archOk || !virtOk {
								ctx.GetLogger().Warn("Skipping underutilized instance type search: Architecture or VirtualizationType missing", "resourceId", resource.Id)
							} else {
								recommendedInstanceTypes, err := svc.GetInstanceTypesFromInstanceRequirements(ctx.GetContext(), &ec2.GetInstanceTypesFromInstanceRequirementsInput{
									ArchitectureTypes:   []types.ArchitectureType{types.ArchitectureType(architecture)},
									VirtualizationTypes: []types.VirtualizationType{types.VirtualizationType(virtualizationType)},
									InstanceRequirements: &types.InstanceRequirementsRequest{
										MemoryMiB: &types.MemoryMiBRequest{
											Min: aws.Int32(int32(recommendedMemory)),
											Max: aws.Int32(int32(recommendedMemory)),
										},
										VCpuCount: &types.VCpuCountRangeRequest{
											Min: aws.Int32(int32(recommendedCPU)),
											Max: aws.Int32(int32(recommendedCPU)),
										},
										BurstablePerformance: types.BurstablePerformanceIncluded,
									},
								})
								if err != nil {
									if strings.Contains(err.Error(), "UnauthorizedOperation") {
										ctx.GetLogger().Warn("not authorized to fetch recommended instance types for underutilized check", "resourceId", resource.Id)
									} else {
										ctx.GetLogger().Error("failed to fetch recommended instance types for underutilized check", "error", err, "resourceId", resource.Id)
									}
								} else {
									instanceTypesPriceMap := []map[string]any{}
									for _, instanceType := range recommendedInstanceTypes.InstanceTypes {
										if instanceType.InstanceType == nil {
											continue
										}
										price, err := getEc2PricesBasedOnPriceList(resource.Region, *instanceType.InstanceType)
										if err != nil {
											ctx.GetLogger().Warn("failed to fetch price for recommended instance type", "error", err, "instanceType", *instanceType.InstanceType, "resourceId", resource.Id)
											continue // Skip if price fetch fails
										}
										if currentPrice > 0 && price < currentPrice { // Ensure currentPrice is valid
											instanceTypesPriceMap = append(instanceTypesPriceMap, map[string]any{
												"instanceType": *instanceType.InstanceType,
												"price":        price,
											})
										}
									}

									savings := 0.0
									if currentPrice > 0 { // Ensure currentPrice is valid
										savings = currentPrice * 24 * 30 / 2 // Default if no cheaper option found but still underutilized
										if len(instanceTypesPriceMap) > 0 {
											sort.Slice(instanceTypesPriceMap, func(i, j int) bool {
												priceI, okI := instanceTypesPriceMap[i]["price"].(float64)
												priceJ, okJ := instanceTypesPriceMap[j]["price"].(float64)
												if !okI || !okJ {
													return false // Should not happen if added correctly
												}
												return priceI < priceJ
											})
											if newPrice, ok := instanceTypesPriceMap[0]["price"].(float64); ok {
												savings = (currentPrice - newPrice) * 24 * 30
											}
										}
									}

									underutilizedData := map[string]any{
										"recommendedInstances": instanceTypesPriceMap,
										"recommendedMemoryMiB": recommendedMemory,
										"recommendedVCpu":      recommendedCPU,
									}
									if len(cpuMetrics.Items) > 0 { // Safe access
										underutilizedData["cpu"] = cpuMetrics.Items[0]
									}
									recommendations = append(recommendations, providers.Recommendation{
										CategoryName:        providers.RecommendationCategoryRightSizing,
										RuleName:            "aws_ec2_underutilized",
										Severity:            providers.RecommendationSeverityMedium,
										Savings:             savings,
										Data:                underutilizedData,
										Action:              providers.RecommendationActionModify,
										ResourceServiceName: resource.ServiceName,
										ResourceId:          resource.Id,
										ResourceType:        resource.Type,
										ResourceRegion:      resource.Region,
									})
								}
							}
						}
					}
				}
			}

			// varify ec2 generation
			if instanceTypeDetailsMap != nil {
				if currentGen, ok := instanceTypeDetailsMap["CurrentGeneration"].(bool); ok {
					if !currentGen {
						architecture, archOk := resource.Meta["Architecture"].(string)
						virtualizationType, virtOk := resource.Meta["VirtualizationType"].(string)
						instanceTypeStr, instTypeOk := resource.Meta["InstanceType"].(string)
						memInfo, memOk := instanceTypeDetailsMap["MemoryInfo"].(map[string]any)
						vCpuInfo, vCpuOk := instanceTypeDetailsMap["VCpuInfo"].(map[string]any)

						if !archOk || !virtOk || !instTypeOk || !memOk || !vCpuOk {
							ctx.GetLogger().Warn("Skipping instance generation upgrade check due to missing meta fields or InstanceTypeDetails parts", "resourceId", resource.Id)
						} else {
							sizeInMiBFloat, memValOk := memInfo["SizeInMiB"].(float64)
							defaultVCpusFloat, vCpuValOk := vCpuInfo["DefaultVCpus"].(float64)

							if !memValOk || !vCpuValOk {
								ctx.GetLogger().Warn("Skipping instance generation upgrade: SizeInMiB or DefaultVCpus missing/invalid type", "resourceId", resource.Id)
							} else {
								allowedInstanceTypePattern := string(instanceTypeStr[0]) + "*" // e.g. t*

								recommendedInstanceTypes, err := svc.GetInstanceTypesFromInstanceRequirements(ctx.GetContext(), &ec2.GetInstanceTypesFromInstanceRequirementsInput{
									ArchitectureTypes:   []types.ArchitectureType{types.ArchitectureType(architecture)},
									VirtualizationTypes: []types.VirtualizationType{types.VirtualizationType(virtualizationType)},
									InstanceRequirements: &types.InstanceRequirementsRequest{
										InstanceGenerations: []types.InstanceGeneration{types.InstanceGenerationCurrent},
										MemoryMiB: &types.MemoryMiBRequest{
											Min: aws.Int32(int32(sizeInMiBFloat)),
											Max: aws.Int32(int32(sizeInMiBFloat)),
										},
										VCpuCount: &types.VCpuCountRangeRequest{
											Min: aws.Int32(int32(defaultVCpusFloat)),
											Max: aws.Int32(int32(defaultVCpusFloat)),
										},
										AllowedInstanceTypes: []string{allowedInstanceTypePattern},
									},
								})
								if err != nil {
									ctx.GetLogger().Error("failed to fetch recommended instance types for generation upgrade", "error", err, "resourceId", resource.Id)
								} else {
									instanceTypesPriceMap := []map[string]any{}
									for _, it := range recommendedInstanceTypes.InstanceTypes {
										if it.InstanceType == nil {
											continue
										}
										price, err := getEc2PricesBasedOnPriceList(resource.Region, *it.InstanceType)
										if err != nil {
											ctx.GetLogger().Warn("failed to fetch price for recommended generation upgrade instance type", "error", err, "instanceType", *it.InstanceType, "resourceId", resource.Id)
											continue
										}
										// Only add if cheaper and different type
										if currentPrice > 0 && price < currentPrice && *it.InstanceType != instanceTypeStr {
											instanceTypesPriceMap = append(instanceTypesPriceMap, map[string]any{
												"instanceType": *it.InstanceType,
												"price":        price,
											})
										}
									}

									if len(instanceTypesPriceMap) > 0 {
										sort.Slice(instanceTypesPriceMap, func(i, j int) bool {
											priceI, _ := instanceTypesPriceMap[i]["price"].(float64)
											priceJ, _ := instanceTypesPriceMap[j]["price"].(float64)
											return priceI < priceJ
										})
										savings := 0.0
										if newPrice, ok := instanceTypesPriceMap[0]["price"].(float64); ok && currentPrice > 0 {
											savings = (currentPrice - newPrice) * 24 * 30
										}

										if savings > 0 { // Only recommend if there's a saving
											recommendation := providers.Recommendation{
												CategoryName: providers.RecommendationCategoryRightSizing,
												RuleName:     "aws_ec2_instance_generation_upgrade",
												Severity:     providers.RecommendationSeverityMedium,
												Savings:      savings,
												Data: map[string]any{
													"instance_id":        resource.Id,
													"instance_arn":       resource.Arn,
													"instance_type":      instanceTypeStr,
													"latest_generations": instanceTypesPriceMap,
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
							}
						}
					}
				} else {
					ctx.GetLogger().Warn("CurrentGeneration field missing or not a boolean in InstanceTypeDetails", "resourceId", resource.Id)
				}
			}

			// Alternate instance types (general check, might overlap with underutilized/generation)
			if instanceTypeDetailsMap != nil {
				memInfo, memOk := instanceTypeDetailsMap["MemoryInfo"].(map[string]any)
				vCpuInfo, vCpuOk := instanceTypeDetailsMap["VCpuInfo"].(map[string]any)

				if !memOk {
					ctx.GetLogger().Warn("Skipping alternate instance check: MemoryInfo missing from InstanceTypeDetailsMap", "resourceId", resource.Id)
				} else if !vCpuOk {
					ctx.GetLogger().Warn("Skipping alternate instance check: VCpuInfo missing from InstanceTypeDetailsMap", "resourceId", resource.Id)
				} else {
					sizeInMiBFloat, memValOk := memInfo["SizeInMiB"].(float64)
					defaultVCpusFloat, vCpuValOk := vCpuInfo["DefaultVCpus"].(float64)

					if !memValOk {
						ctx.GetLogger().Warn("Skipping alternate instance check: SizeInMiB missing or not a float64 in MemoryInfo", "resourceId", resource.Id)
					} else if !vCpuValOk {
						ctx.GetLogger().Warn("Skipping alternate instance check: DefaultVCpus missing or not a float64 in VCpuInfo", "resourceId", resource.Id)
					} else {
						// Fetching alternate types with same specs but potentially different family/generation
						altRecommendedInstanceTypes, err := svc.GetInstanceTypesFromInstanceRequirements(ctx.GetContext(), &ec2.GetInstanceTypesFromInstanceRequirementsInput{
							ArchitectureTypes:   []types.ArchitectureType{types.ArchitectureTypeArm64, types.ArchitectureTypeX8664},
							VirtualizationTypes: []types.VirtualizationType{types.VirtualizationTypeHvm},
							InstanceRequirements: &types.InstanceRequirementsRequest{
								MemoryMiB: &types.MemoryMiBRequest{
									Min: aws.Int32(int32(sizeInMiBFloat)),
									Max: aws.Int32(int32(sizeInMiBFloat)),
								},
								VCpuCount: &types.VCpuCountRangeRequest{
									Min: aws.Int32(int32(defaultVCpusFloat)),
									Max: aws.Int32(int32(defaultVCpusFloat)),
								},
							},
						})
						if err != nil {
							if strings.Contains(err.Error(), "UnauthorizedOperation") {
								ctx.GetLogger().Warn("not authorized to fetch alternate ec2 instance types", "resourceId", resource.Id)
							} else {
								ctx.GetLogger().Error("failed to fetch alternate ec2 instance types", "error", err, "resourceId", resource.Id)
							}
						} else {
							altInstanceTypesPriceMap := []map[string]any{}
							currentInstanceTypeStr, _ := resource.Meta["InstanceType"].(string)
							for _, it := range altRecommendedInstanceTypes.InstanceTypes {
								if it.InstanceType == nil || (currentInstanceTypeStr != "" && *it.InstanceType == currentInstanceTypeStr) {
									continue // Skip current type or nil
								}
								price, err := getEc2PricesBasedOnPriceList(resource.Region, *it.InstanceType)
								if err != nil {
									ctx.GetLogger().Warn("failed to fetch price for alternate instance type", "error", err, "instanceType", *it.InstanceType, "resourceId", resource.Id)
									continue
								}
								if currentPrice > 0 && price < currentPrice {
									altInstanceTypesPriceMap = append(altInstanceTypesPriceMap, map[string]any{
										"instanceType": *it.InstanceType,
										"price":        price,
									})
								}
							}

							if len(altInstanceTypesPriceMap) > 0 {
								sort.Slice(altInstanceTypesPriceMap, func(i, j int) bool {
									priceI, _ := altInstanceTypesPriceMap[i]["price"].(float64)
									priceJ, _ := altInstanceTypesPriceMap[j]["price"].(float64)
									return priceI < priceJ
								})
								savings := 0.0
								if newPrice, ok := altInstanceTypesPriceMap[0]["price"].(float64); ok && currentPrice > 0 {
									savings = (currentPrice - newPrice) * 24 * 30
								}

								if savings > 0 { // Only recommend if there's a saving
									recommendation := providers.Recommendation{
										CategoryName: providers.RecommendationCategoryRightSizing,
										RuleName:     "aws_ec2_alternate_instances",
										Severity:     providers.RecommendationSeverityMedium,
										Savings:      savings,
										Data: map[string]any{
											"alternate_instances": altInstanceTypesPriceMap,
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
					}
				}
			}

			// Recommendation: IMDSv2 Enforcement
			if metadataOptionsAny, ok := resource.Meta["MetadataOptions"]; ok {
				if metadataOptions, ok := metadataOptionsAny.(map[string]any); ok {
					if httpTokens, ok := metadataOptions["HttpTokens"].(string); ok && httpTokens != "required" {
						recommendations = append(recommendations, providers.Recommendation{
							CategoryName:        providers.RecommendationCategorySecurity,
							RuleName:            "aws_ec2_instance_imds_token_optional",
							Severity:            providers.RecommendationSeverityMedium,
							Data:                map[string]any{"instance_id": resource.Id, "instance_arn": resource.Arn, "current_http_tokens_state": httpTokens, "reason": "Instance metadata service (IMDS) is not configured to require tokens (IMDSv2). Recommend setting HttpTokens to 'required'."},
							Action:              providers.RecommendationActionModify,
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						})
					} else if !ok {
						ctx.GetLogger().Warn("HttpTokens field in MetadataOptions is not a string or missing", "resourceId", resource.Id)
						// This case implies it might be "required" or some other state, or field is missing. If missing, it defaults to optional.
						// The original code's 'else' block handles the "MetadataOptions is not present" case, which also implies default optional.
						// To be safe, if HttpTokens is present but not a string, we log and don't make a recommendation here.
						// If HttpTokens is explicitly "required", the condition `httpTokens != "required"` handles it.
					}
				} else {
					ctx.GetLogger().Warn("MetadataOptions is not a map", "resourceId", resource.Id)
					// Default behavior: if MetadataOptions is malformed, assume default (optional) and recommend IMDSv2.
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategorySecurity,
						RuleName:            "aws_ec2_instance_imds_token_optional",
						Severity:            providers.RecommendationSeverityMedium,
						Data:                map[string]any{"instance_id": resource.Id, "instance_arn": resource.Arn, "current_http_tokens_state": "unknown (malformed MetadataOptions)", "reason": "Instance metadata service (IMDS) is not configured to require tokens (IMDSv2). Recommend setting HttpTokens to 'required'."},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			} else { // If MetadataOptions is not present, it defaults to optional HttpTokens
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "aws_ec2_instance_imds_token_optional",
					Severity:            providers.RecommendationSeverityMedium,
					Data:                map[string]any{"instance_id": resource.Id, "instance_arn": resource.Arn, "current_http_tokens_state": "optional (default)", "reason": "Instance metadata service (IMDS) is not configured to require tokens (IMDSv2). Recommend setting HttpTokens to 'required'."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Recommendation: Detailed Monitoring Disabled
			if monitoringAny, ok := resource.Meta["Monitoring"]; ok {
				if monitoringMap, ok := monitoringAny.(map[string]any); ok {
					if monState, ok := monitoringMap["State"].(string); ok && monState == "disabled" {
						recommendations = append(recommendations, providers.Recommendation{
							CategoryName:        providers.RecommendationCategoryConfiguration,
							RuleName:            "aws_ec2_detailed_monitoring_disabled",
							Severity:            providers.RecommendationSeverityLow,
							Data:                map[string]any{"instance_id": resource.Id, "instance_arn": resource.Arn, "reason": "Detailed (1-minute interval) CloudWatch monitoring is disabled. Enable for more granular metrics."},
							Action:              providers.RecommendationActionModify,
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						})
					} else if !ok {
						ctx.GetLogger().Warn("Monitoring State field not found or not a string", "resourceId", resource.Id)
					}
				} else {
					ctx.GetLogger().Warn("Monitoring field is not a map", "resourceId", resource.Id)
				}
			} else {
				ctx.GetLogger().Info("Monitoring field not found in resource meta, skipping detailed monitoring check.", "resourceId", resource.Id)
				// If "Monitoring" field itself is missing, assume it's not explicitly disabled, or we cannot determine.
				// Original code would skip this.
			}

			// Check for missing CloudWatch alarms
			ec2AlarmTemplates, err := LoadAlarmTemplates("ec2")
			if err != nil {
				ctx.GetLogger().Warn("Failed to load EC2 alarm templates", "error", err, "resourceId", resource.Id)
			} else {
				for _, template := range ec2AlarmTemplates {
					// Check if we should recommend this alarm based on metric type
					if !ShouldRecommendAlarm(resource, template) {
						continue
					}

					// Check if alarm is missing
					isMissing, err := IsAlarmMissing(resource, template, resource.Id)
					if err != nil {
						ctx.GetLogger().Warn("Error checking alarm", "error", err, "template", template.Name, "resourceId", resource.Id)
						continue
					}

					if !isMissing {
						// Alarm already exists, skip
						continue
					}

					// Calculate threshold based on instance type
					threshold, err := CalculateThreshold(resource, template)
					if err != nil {
						ctx.GetLogger().Warn("Error calculating threshold", "error", err, "template", template.Name, "resourceId", resource.Id)
						continue
					}

					// Build alarm configuration for the recommendation data
					alarmConfig := providers.AlarmCreationConfig{
						AlarmName:          fmt.Sprintf("%s-%s", template.Name, resource.Id),
						MetricName:         template.Configuration.MetricName,
						Namespace:          template.Configuration.Namespace,
						Statistic:          template.Configuration.Statistic,
						Period:             template.Configuration.Period,
						EvaluationPeriods:  template.Configuration.EvaluationPeriods,
						DatapointsToAlarm:  template.Configuration.DatapointsToAlarm,
						Threshold:          threshold,
						ComparisonOperator: template.Configuration.ComparisonOperator,
						TreatMissingData:   template.Configuration.TreatMissingData,
						Dimensions: []providers.AlarmDimension{
							{
								Name:  "InstanceId",
								Value: resource.Id,
							},
						},
					}

					// Create recommendation
					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     template.Name,
						Severity:     providers.RecommendationSeverityFromString(template.Severity),
						Savings:      0,
						Data: map[string]any{
							"instance_id":     resource.Id,
							"instance_arn":    resource.Arn,
							"instance_type":   resource.Meta["InstanceType"],
							"instance_region": resource.Region,
							"metric_name":     template.Configuration.MetricName,
							"threshold":       threshold,
							"alarm_config":    alarmConfig,
							"alarm_type":      template.AlarmType,
							"reason":          template.Description,
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
	}

	return recommendations, nil
}

func (a *amazonEc2) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber)
		return "", err
	}
	cfg.Region = region
	logsSvc := cloudwatchlogs.NewFromConfig(cfg)

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
				return logGroupName, nil
			}
		}
	}
	return "", nil
}

func (a *amazonEc2) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber)
		return providers.ServiceMapApplication{}, err
	}
	cfg.Region = region
	ec2Svc := ec2.NewFromConfig(cfg)
	asgSvc := autoscaling.NewFromConfig(cfg)
	logsSvc := cloudwatchlogs.NewFromConfig(cfg)
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "ec2",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	app.Id.Kind = "ec2"
	var describeInstancesOutput *ec2.DescribeInstancesOutput
	if strings.HasPrefix(resourceId, "i-") {
		describeInstancesOutput, err = ec2Svc.DescribeInstances(ctx.GetContext(), &ec2.DescribeInstancesInput{InstanceIds: []string{resourceId}})
	} else {
		filter := types.Filter{Name: aws.String("tag:Name"), Values: []string{resourceId}}
		describeInstancesOutput, err = ec2Svc.DescribeInstances(ctx.GetContext(), &ec2.DescribeInstancesInput{Filters: []types.Filter{filter}})
	}
	if err != nil {
		return app, err
	}
	if len(describeInstancesOutput.Reservations) > 0 && len(describeInstancesOutput.Reservations[0].Instances) > 0 {
		instance := describeInstancesOutput.Reservations[0].Instances[0]
		app.Id.Name = *instance.InstanceId
		app.Status = string(instance.State.Name)
		if instance.ImageId != nil {
			app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: *instance.ImageId, Kind: "ec2", Namespace: region}}.ToDownstreamLink())
		}
		if instance.VpcId != nil {
			app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: *instance.VpcId, Kind: "ec2", Namespace: region}}.ToDownstreamLink())
		}
		if instance.SubnetId != nil {
			app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: *instance.SubnetId, Kind: "ec2", Namespace: region}}.ToDownstreamLink())
		}
		for _, sg := range instance.SecurityGroups {
			app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: *sg.GroupId, Kind: "ec2", Namespace: region}}.ToDownstreamLink())
		}
		if instance.IamInstanceProfile != nil && instance.IamInstanceProfile.Arn != nil {
			app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: *instance.IamInstanceProfile.Arn, Kind: "iam", Namespace: ""}}.ToDownstreamLink())
		}
		for _, bdm := range instance.BlockDeviceMappings {
			if bdm.Ebs != nil {
				app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: *bdm.Ebs.VolumeId, Kind: "ec2", Namespace: region}}.ToDownstreamLink())
			}
		}
		describeAsgOutput, err := asgSvc.DescribeAutoScalingInstances(ctx.GetContext(), &autoscaling.DescribeAutoScalingInstancesInput{InstanceIds: []string{app.Id.Name}})
		if err == nil && len(describeAsgOutput.AutoScalingInstances) > 0 {
			if name := describeAsgOutput.AutoScalingInstances[0].AutoScalingGroupName; name != nil {
				app.Upstreams = append(app.Upstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: *name, Kind: "autoscaling", Namespace: region}}.ToUpstreamLink())
			}
		}
		ctx.GetLogger().Info("Searching for associated CloudWatch Log Groups", "instanceId", app.Id.Name)

		paginator := cloudwatchlogs.NewDescribeLogGroupsPaginator(logsSvc, &cloudwatchlogs.DescribeLogGroupsInput{})
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx.GetContext())
			if err != nil {
				ctx.GetLogger().Warn("failed to describe log groups for instance", "error", err, "instanceId", app.Id.Name)
				break
			}
			for _, lg := range page.LogGroups {
				logGroupName := *lg.LogGroupName
				describeLogStreamsOutput, err := logsSvc.DescribeLogStreams(ctx.GetContext(), &cloudwatchlogs.DescribeLogStreamsInput{
					LogGroupName:        &logGroupName,
					LogStreamNamePrefix: &app.Id.Name,
					Limit:               aws.Int32(1),
				})
				if err == nil && len(describeLogStreamsOutput.LogStreams) > 0 {
					app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: logGroupName, Kind: "cloudwatchlogs", Namespace: region}}.ToDownstreamLink())
				}
			}
		}
	}
	return app, nil
}
