package aws

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

type amazonCloudwatch struct {
	DefaultAwsServiceImpl
}

func (a *amazonCloudwatch) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *amazonCloudwatch) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *amazonCloudwatch) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *amazonCloudwatch) ListMetrics(_ providers.CloudProviderContext, _ providers.Account, request providers.ListMetricsRequest) (providers.ListMetricsResponse, error) {
	return listAwsCloudwatchMetrics(request)
}

func (a *amazonCloudwatch) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameCloudWatch)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := cloudwatchlogs.NewFromConfig(cfg)
	resources := []providers.Resource{}

	paginator := cloudwatchlogs.NewDescribeLogGroupsPaginator(svc, &cloudwatchlogs.DescribeLogGroupsInput{})
	for paginator.HasMorePages() {
		logGroupsOutput, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch cloudwatch log groups", "error", err, "region", regionName)
			return resources, err
		}

		for _, logGroup := range logGroupsOutput.LogGroups {
			if logGroup.LogGroupName == nil || *logGroup.LogGroupName == "" || logGroup.Arn == nil {
				continue
			}

			tags := make(map[string][]string)
			tagsOutput, err := svc.ListTagsForResource(ctx.GetContext(), &cloudwatchlogs.ListTagsForResourceInput{
				ResourceArn: logGroup.Arn,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch tags for log group", "error", err, "logGroupArn", *logGroup.Arn)
			} else {
				for k, v := range tagsOutput.Tags {
					tags[k] = append(tags[k], v)
				}
			}

			resource := providers.Resource{
				Id:          *logGroup.LogGroupName,
				ServiceName: ServiceNameCloudWatch,
				Name:        *logGroup.LogGroupName,
				Status:      providers.ResourceStatusActive,
				Region:      regionName,
				Tags:        tags,
				Meta:        structToMap(logGroup),
				Arn:         *logGroup.Arn,
				CreatedAt:   time.UnixMilli(*logGroup.CreationTime),
				Type:        getAwsServiceResourceType(ServiceNameCloudWatch, "log-group"),
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

const (
	// cloudWatchLogsProposedRetentionDays is the retention this recommendation
	// proposes for a log group that has none. The saving is the storage that
	// would age out beyond it.
	cloudWatchLogsProposedRetentionDays = 30

	// cloudWatchLogsStandardStorageUSDPerGBMonth is the us-east-1 Standard-class
	// rate, used only when the Pricing API returns nothing for the region.
	// AWS only: Azure and GCP price log storage on their own schedules and must
	// never read this value.
	cloudWatchLogsStandardStorageUSDPerGBMonth = 0.03

	logStorageSourcePricingAPI = "pricing_api"
	logStorageSourceFlatRate   = "aws_flat_rate"

	bytesPerGB = 1024 * 1024 * 1024
)

// logStoragePrice is a resolved per-GB-month rate together with where it came
// from. The two travel as one value because the flat-rate fallback happens to
// equal the real us-east-1 Standard rate: comparing the number back against the
// constant cannot tell a quote from a fallback, and would report every
// correctly-quoted us-east-1 group as an estimate.
type logStoragePrice struct {
	USDPerGBMonth float64
	Source        string
}

// cloudWatchStorageUsageTypeSuffix maps a log group's class onto the tail of the
// Pricing API usage type that bills its stored bytes. The region prefix varies
// (USE1-, APS3-, EU-) and us-east-1 also publishes a legacy unprefixed entry, so
// callers match on the suffix rather than reconstructing the full usage type.
func cloudWatchStorageUsageTypeSuffix(logGroupClass string) string {
	if strings.EqualFold(strings.TrimSpace(logGroupClass), "INFREQUENT_ACCESS") {
		return "TimedStorage-IA-ByteHrs"
	}
	// STANDARD, DELIVERY and an absent class all bill at the standard rate.
	return "TimedStorage-ByteHrs"
}

// selectCloudWatchStorageRate picks the per-GB-month rate for one storage class
// out of a region's CloudWatch price list, taking the cheapest priceable match so
// a duplicate entry cannot make the choice arbitrary.
func selectCloudWatchStorageRate(products []map[string]interface{}, suffix string) (float64, bool) {
	best, found := 0.0, false
	for _, product := range products {
		p, _ := product["product"].(map[string]any)
		attrs, _ := p["attributes"].(map[string]any)
		usageType, _ := attrs["usagetype"].(string)
		if !strings.HasSuffix(usageType, suffix) {
			continue
		}
		price, err := getPricingValue(product)
		if err != nil || price <= 0 {
			continue
		}
		if !found || price < best {
			best, found = price, true
		}
	}
	return best, found
}

// resolveLogStoragePrice turns a region's price list into the rate for one
// storage class, falling back to the published AWS rate when the lookup failed
// or matched nothing usable. The source travels with the rate rather than being
// recovered by comparing it to the fallback constant afterwards — the fallback
// equals the real us-east-1 Standard rate, so that comparison labels every
// correctly-quoted us-east-1 group an estimate.
func resolveLogStoragePrice(products []map[string]interface{}, suffix string, lookupErr error) logStoragePrice {
	if lookupErr == nil {
		if quoted, ok := selectCloudWatchStorageRate(products, suffix); ok {
			return logStoragePrice{USDPerGBMonth: quoted, Source: logStorageSourcePricingAPI}
		}
	}
	return logStoragePrice{USDPerGBMonth: cloudWatchLogsStandardStorageUSDPerGBMonth, Source: logStorageSourceFlatRate}
}

// agedOutFraction estimates the share of a log group's stored bytes older than
// the proposed retention, assuming ingestion has been roughly even over the
// group's life: with a uniform age spread, data older than N days is
// (age-N)/age of the total. It is an estimate, not a measurement — AWS reports
// only a total byte count, never an age histogram — so the assumption is
// recorded on the recommendation alongside the figure.
func agedOutFraction(createdAt time.Time, retentionDays int, now time.Time) float64 {
	if createdAt.IsZero() || retentionDays <= 0 {
		return 0
	}
	ageDays := now.Sub(createdAt).Hours() / 24
	if ageDays <= float64(retentionDays) {
		return 0
	}
	return (ageDays - float64(retentionDays)) / ageDays
}

// logRetentionMonthlySaving converts stored bytes and an aged-out share into the
// monthly storage cost that setting a retention period would stop.
func logRetentionMonthlySaving(storedBytes, agedOut, usdPerGBMonth float64) float64 {
	if storedBytes <= 0 || agedOut <= 0 || usdPerGBMonth <= 0 {
		return 0
	}
	return (storedBytes / bytesPerGB) * agedOut * usdPerGBMonth
}

// metaFloat reads a numeric meta value, tolerating the pointer forms the AWS SDK
// produces and the float64 a JSON round-trip leaves behind.
func metaFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case *int64:
		if n != nil {
			return float64(*n), true
		}
	case *int32:
		if n != nil {
			return float64(*n), true
		}
	}
	return 0, false
}

func (a *amazonCloudwatch) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	cfg, cfgErr := getAwsConfigFromAccount(ctx.GetContext(), account)
	if cfgErr != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", cfgErr, "accountNumber", account.AccountNumber)
	}
	// One price list per region, not per log group: an account can hold hundreds.
	storageRates := map[string]logStoragePrice{}
	now := time.Now()
	for _, resource := range existingResources {
		if resource.Type != getAwsServiceResourceType(ServiceNameCloudWatch, "log-group") {
			continue
		}

		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		retentionSet := false
		if retentionDaysAny, rdOk := meta["RetentionInDays"]; rdOk && retentionDaysAny != nil {
			// Handle both *int32 (from AWS SDK via structToMap) and float64 (from JSON unmarshal)
			switch v := retentionDaysAny.(type) {
			case *int32:
				retentionSet = *v > 0
			case float64:
				retentionSet = v > 0
			case int32:
				retentionSet = v > 0
			case int:
				retentionSet = v > 0
			}
		}

		if !retentionSet {
			logGroupClass, _ := meta["LogGroupClass"].(string)
			storedBytes, _ := metaFloat(meta["StoredBytes"])
			suffix := cloudWatchStorageUsageTypeSuffix(logGroupClass)

			rateKey := resource.Region + "|" + suffix
			price, cached := storageRates[rateKey]
			if !cached {
				var products []map[string]interface{}
				lookupErr := cfgErr
				if cfgErr == nil {
					products, lookupErr = getAvailableInstancesFromPricing(cfg, "AmazonCloudWatch", map[string]string{
						"regionCode":      resource.Region,
						"operatingSystem": "",
					})
					if lookupErr != nil {
						ctx.GetLogger().Warn("cloudwatch logs pricing lookup failed", "error", lookupErr, "region", resource.Region)
					}
				}
				price = resolveLogStoragePrice(products, suffix, lookupErr)
				if price.Source == logStorageSourceFlatRate {
					// Once per region and class, not per log group: the memo below
					// covers every remaining group in that region.
					reason := "no matching product in the region's price list"
					switch {
					case cfgErr != nil:
						reason = "no AWS session for this account"
					case lookupErr != nil:
						reason = "pricing lookup failed"
					}
					ctx.GetLogger().Warn("cloudwatch logs storage priced at the AWS flat rate", "reason", reason, "region", resource.Region, "logGroupClass", logGroupClass)
				}
				storageRates[rateKey] = price
			}

			agedOut := agedOutFraction(resource.CreatedAt, cloudWatchLogsProposedRetentionDays, now)
			savings := logRetentionMonthlySaving(storedBytes, agedOut, price.USDPerGBMonth)

			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryRightSizing,
				RuleName:     "aws_cloudwatch_log_group_retention",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      savings,
				Data: map[string]any{
					"log_group_name":          resource.Name,
					"reason":                  "Retention period not set.",
					"log_group_class":         logGroupClass,
					"stored_bytes":            storedBytes,
					"proposed_retention_days": cloudWatchLogsProposedRetentionDays,
					"aged_out_fraction":       agedOut,
					"usd_per_gb_month":        price.USDPerGBMonth,
					"pricing_source":          price.Source,
					"savings_basis":           fmt.Sprintf("stored bytes older than %d days, estimated from the group's age assuming even ingestion", cloudWatchLogsProposedRetentionDays),
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		if kmsKeyId, ok := meta["KmsKeyId"].(string); !ok || kmsKeyId == "" {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_cloudwatch_log_group_encryption_cmk",
				Severity:            providers.RecommendationSeverityMedium,
				Data:                map[string]any{"log_group_name": resource.Name, "reason": "Not encrypted with CMK."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_tags",
				Severity:            providers.RecommendationSeverityLow,
				Data:                map[string]any{"log_group_name": resource.Name},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}
	}

	return recommendations, nil
}

func (a *amazonCloudwatch) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (a *amazonCloudwatch) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber)
		return providers.ServiceMapApplication{}, err
	}
	cfg.Region = region
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "cloudwatch",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	cwSvc := cloudwatch.NewFromConfig(cfg)

	alarmName := resourceId
	if strings.HasPrefix(resourceId, "arn:aws:cloudwatch:") {
		parts := strings.Split(resourceId, ":")
		if len(parts) > 6 && parts[5] == "alarm" {
			alarmName = parts[6]
		}
	}
	describeAlarmsOutput, err := cwSvc.DescribeAlarms(ctx.GetContext(), &cloudwatch.DescribeAlarmsInput{AlarmNames: []string{alarmName}})
	if err != nil {
		return app, err
	}
	if len(describeAlarmsOutput.MetricAlarms) > 0 {
		alarm := describeAlarmsOutput.MetricAlarms[0]
		app.Id.Name = *alarm.AlarmArn
		app.Status = string(alarm.StateValue)
		if alarm.Namespace != nil && len(alarm.Dimensions) > 0 {
			nsParts := strings.Split(*alarm.Namespace, "/")
			svcName := ""
			if len(nsParts) > 1 {
				svcName = strings.ToLower(nsParts[1])
			}
			for _, dim := range alarm.Dimensions {
				app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: *dim.Value, Kind: svcName, Namespace: region}}.ToDownstreamLink())
			}
		}
		for _, actionArn := range alarm.AlarmActions {
			app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: actionArn, Kind: "sns", Namespace: region}}.ToDownstreamLink())
		}
	}

	return app, nil
}
