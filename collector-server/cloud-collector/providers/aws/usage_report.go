package aws

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"slices"
	"strconv"
	"strings"
	"time"

	"context"

	"github.com/aws/aws-sdk-go-v2/service/costandusagereportservice"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/samber/lo"
)

// known headers
// identity/LineItemId,identity/TimeInterval,
// bill/InvoiceId,bill/BillingEntity,bill/BillType,bill/PayerAccountId,bill/BillingPeriodStartDate,bill/BillingPeriodEndDate,
// lineItem/UsageAccountId,lineItem/LineItemType,lineItem/UsageStartDate,lineItem/UsageEndDate,lineItem/ProductCode,lineItem/UsageType,lineItem/Operation,lineItem/AvailabilityZone,lineItem/ResourceId,lineItem/UsageAmount,lineItem/NormalizationFactor,lineItem/NormalizedUsageAmount,lineItem/CurrencyCode,lineItem/UnblendedRate,lineItem/UnblendedCost,lineItem/BlendedRate,lineItem/BlendedCost,lineItem/LineItemDescription,lineItem/TaxType,lineItem/LegalEntity,
// product/ProductName,product/SizeFlex,product/abdInstanceClass,product/availability,product/availabilityZone,product/capacitystatus,product/category,product/ciType,product/classicnetworkingsupport,product/clockSpeed,product/contentType,product/currentGeneration,product/databaseEngine,product/dedicatedEbsThroughput,product/deploymentOption,product/description,product/durability,product/ecu,product/endpointType,product/engineCode,product/enhancedNetworkingSupported,product/equivalentondemandsku,product/feeCode,product/feeDescription,product/findingGroup,product/findingSource,product/findingStorage,product/fromLocation,product/fromLocationType,product/fromRegionCode,product/gpuMemory,product/group,product/groupDescription,product/instanceFamily,product/instanceType,product/instanceTypeFamily,product/intelAvx2Available,product/intelAvxAvailable,product/intelTurboAvailable,product/licenseModel,product/location,product/locationType,product/logsDestination,product/marketoption,product/maxIopsBurstPerformance,product/maxIopsvolume,product/maxThroughputvolume,product/maxVolumeSize,product/memory,product/messageDeliveryFrequency,product/messageDeliveryOrder,product/minVolumeSize,product/networkPerformance,product/normalizationSizeFactor,product/operatingSystem,product/operation,product/origin,product/physicalProcessor,product/platopricingtype,product/platostoragetype,product/platousagetype,product/platovolumetype,product/preInstalledSw,product/pricingUnit,product/processorArchitecture,product/processorFeatures,product/productFamily,product/queueType,product/recipient,product/region,product/regionCode,product/servicecode,product/servicename,product/sku,product/standardGroup,product/standardStorage,product/storage,product/storageClass,product/storageMedia,product/storageType,product/tenancy,product/tiertype,product/toLocation,product/toLocationType,product/toRegionCode,product/transferType,product/usagetype,product/vcpu,product/version,product/volumeApiName,product/volumeType,product/vpcnetworkingsupport,
// pricing/LeaseContractLength,pricing/OfferingClass,pricing/PurchaseOption,pricing/RateCode,pricing/RateId,pricing/currency,pricing/publicOnDemandCost,pricing/publicOnDemandRate,pricing/term,pricing/unit,
// reservation/AmortizedUpfrontCostForUsage,reservation/AmortizedUpfrontFeeForBillingPeriod,reservation/EffectiveCost,reservation/EndTime,reservation/ModificationStatus,reservation/NormalizedUnitsPerReservation,reservation/NumberOfReservations,reservation/RecurringFeeForUsage,reservation/ReservationARN,reservation/StartTime,reservation/SubscriptionId,reservation/TotalReservedNormalizedUnits,reservation/TotalReservedUnits,reservation/UnitsPerReservation,reservation/UnusedAmortizedUpfrontFeeForBillingPeriod,reservation/UnusedNormalizedUnitQuantity,reservation/UnusedQuantity,reservation/UnusedRecurringFee,reservation/UpfrontValue,
// savingsPlan/TotalCommitmentToDate,savingsPlan/SavingsPlanARN,savingsPlan/SavingsPlanRate,savingsPlan/UsedCommitment,savingsPlan/SavingsPlanEffectiveCost,savingsPlan/AmortizedUpfrontCommitmentForBillingPeriod,savingsPlan/RecurringCommitmentForBillingPeriod,
// resourceTags/aws:autoscaling:groupName,resourceTags/aws:cloudformation:logical-id,resourceTags/aws:cloudformation:stack-id,resourceTags/aws:cloudformation:stack-name,resourceTags/aws:createdBy,resourceTags/aws:ec2:fleet-id,resourceTags/aws:ec2launchtemplate:id,resourceTags/aws:ec2launchtemplate:version,resourceTags/aws:eks:cluster-name,resourceTags/user:AWS.SSM.AppManager.EKS.Cluster.ARN,resourceTags/user:CSIVolumeName,resourceTags/user:KubernetesCluster,resourceTags/user:Name,resourceTags/user:aws-node-termination-handler/managed,resourceTags/user:cluster.k8s.amazonaws.com/name,resourceTags/user:created_by,resourceTags/user:ebs.csi.aws.com/cluster,resourceTags/user:eks:cluster-name,resourceTags/user:eks:nodegroup-name,resourceTags/user:k8s.io/cluster-autoscaler/enabled,resourceTags/user:k8s.io/cluster-autoscaler/nudgebee-dev,resourceTags/user:karpenter.sh/provisioner-name,resourceTags/user:kubernetes.io/cluster/nudgebee-dev,resourceTags/user:kubernetes.io/created-for/pv/name,resourceTags/user:kubernetes.io/created-for/pvc/name,resourceTags/user:kubernetes.io/created-for/pvc/namespace,resourceTags/user:kubernetes.io/service-name,resourceTags/user:node.k8s.amazonaws.com/instance_id,resourceTags/user:test,resourceTags/user:updated_by,resourceTags/user:velero.io/backup,resourceTags/user:velero.io/pv,resourceTags/user:velero.io/schedule-name,resourceTags/user:velero.io/storage-location

func convertToUsageReportItem(header []string, row []string) (providers.UsageReportItem, error) {
	item := providers.UsageReportItem{}
	tags := map[string][]string{}
	for i, value := range row {
		switch strings.ToLower(header[i]) {
		case "lineitem/lineitemtype":
			item.CostCategory = providers.UsageReportCostCategory(value)
		case "lineitem/usagestartdate":
			parsedDate, err := time.Parse("2006-01-02T15:04:05Z", value)
			if err != nil {
				return item, err
			}
			item.StartDate = parsedDate
		case "lineitem/usageenddate":
			parsedDate, err := time.Parse("2006-01-02T15:04:05Z", value)
			if err != nil {
				return item, err
			}
			item.EndDate = parsedDate
		case "lineitem/productcode":
			item.ProductCode = value
		case "lineitem/usagetype":
			item.CostSubCategory = value
		case "lineitem/operation":
			item.ResourceOperation = value
		case "lineitem/resourceid":
			item.ResourceId = value
		case "lineitem/unblendedcost":
			data, err := strconv.ParseFloat(value, 64)
			if err == nil {
				item.Cost = data
			}
		case "lineitem/currencycode":
			item.CostCurrency = value
		case "lineitem/taxtype":
			item.CostSubCategory = value
		case "product/region":
			item.ResourceRegionCode = value
		case "product/servicecode":
			item.ProductServiceCode = value
		case "product/productfamily":
			item.ResourceType = value
		default:
			if value != "" && strings.HasPrefix(header[i], "resourceTags/") {
				tagsSplits := strings.SplitN(header[i], "/", 2)
				tags[tagsSplits[1]] = append(tags[tagsSplits[1]], value)
			}
		}
	}

	// data fixes
	// TODO cleanup between. resourceId, resourceType, arn etc
	if item.ProductCode == "awskms" {
		item.ProductCode = "AWSKMS"
	}

	if item.ProductServiceCode == "" {
		item.ProductServiceCode = item.ProductCode
	}
	if item.ResourceType == "" {
		item.ResourceType = string(item.CostCategory)
	}

	item.ResourceType = strings.ToLower(strings.ReplaceAll(item.ResourceType, " ", "-"))
	if strings.HasPrefix(item.ResourceId, "arn:aws") {
		parts := strings.Split(item.ResourceId, ":")
		// update existing arns to follow nb format
		// default format results in issues like same arn for different types causing issues
		// example - arn:aws:rds:us-east-1:123456789012:db:main used for different types like instance/cost/data-transfer
		if len(parts) == 7 {
			item.ResourceType = parts[5]
			if item.ResourceId == "" || strings.HasPrefix(item.ResourceId, "arn:aws") {
				item.ResourceId = parts[6]
			}
		} else if len(parts) == 6 {
			if strings.Contains(parts[len(parts)-1], "/") {
				resourceSplits := strings.SplitN(parts[len(parts)-1], "/", 2)
				if len(resourceSplits) == 2 {
					if resourceSplits[0] != "" {
						item.ResourceType = resourceSplits[0]
					}
					item.ResourceId = resourceSplits[1]
				}
			} else {
				item.ResourceId = parts[len(parts)-1]
			}
		}
	}

	// Normalize resource type to canonical form (e.g., "instance" → "compute-instance")
	// so usage-report resources match those created by the API-sync path.
	item.ResourceType = getAwsServiceResourceType(item.ProductCode, item.ResourceType)

	if item.CostSubCategory == "" {
		item.CostSubCategory = item.ResourceType
	}

	item.ResourceTags = tags

	// For Credit/Refund items, preserve original resource for traceability
	// then clear ResourceId so cloud_resource_id becomes NULL in DB
	// (avoids unique constraint violation on spends table)
	if item.CostCategory == "Credit" || item.CostCategory == "Refund" {
		if item.ResourceId != "" {
			tags["nb_credit_source_resource"] = []string{item.ResourceId}
		}
		item.ResourceId = ""
	}

	return item, nil
}

func readAwsBillingReport(data io.Reader, timeUnit string) ([]providers.UsageReportItem, error) {
	items := []providers.UsageReportItem{}
	reader := csv.NewReader(data)
	headers, err := reader.Read()
	if err != nil {
		return nil, err
	}
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		item, err := convertToUsageReportItem(headers, row)
		if err != nil {
			return items, err
		}
		items = append(items, item)
	}

	if strings.ToUpper(timeUnit) == "HOURLY" {
		aggregatedItemsMap := make(map[string]providers.UsageReportItem)

		for i, item := range items {
			// Use UTC for consistent date formatting
			dayStartDate := item.StartDate.UTC().Truncate(24 * time.Hour)
			dayKey := dayStartDate.Format("2006-01-02")

			// Build aggregation key (match key used in updateSpendsFromUsageReport)
			// Ensure region code has a default if empty for consistent keying
			regionCode := item.ResourceRegionCode
			if regionCode == "" {
				regionCode = "global" // Or another consistent default
			}
			aggKey := fmt.Sprintf("%s:%s:%s:%s:%s:%s", item.ProductCode, item.CostCategory, regionCode, item.ResourceType, item.ResourceId, dayKey)

			if existingItem, ok := aggregatedItemsMap[aggKey]; ok {
				// Key exists, aggregate cost
				existingItem.Cost += item.Cost
				aggregatedItemsMap[aggKey] = existingItem
				// Free memory on duplicate items — first occurrence's Tags are kept in the map
				items[i].ResourceTags = nil
			} else {
				// Key doesn't exist, create new aggregated item
				newItem := item                  // Copy base item
				newItem.Cost = item.Cost         // Set initial cost
				newItem.StartDate = dayStartDate // Set StartDate to the beginning of the day
				newItem.EndDate = dayStartDate

				aggregatedItemsMap[aggKey] = newItem
			}
		}
		// Convert map back to slice — old items backing array becomes unreachable for GC
		items = slices.Collect(maps.Values(aggregatedItemsMap))
	}

	return items, err
}

func getUsageBucketFromCostReport(ctx providers.CloudProviderContext, account providers.Account, reportName string, bucketName string) (string, string, string, string, string, string, string, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return "", "", "", "GZIP", "", "", "", err
	}
	svc := costandusagereportservice.NewFromConfig(cfg)
	var nextToken *string
	for {
		input := &costandusagereportservice.DescribeReportDefinitionsInput{
			NextToken: nextToken,
		}
		result, err := svc.DescribeReportDefinitions(context.TODO(), input)
		if err != nil {
			ctx.GetLogger().Error("failed to fetch cost report", "error", err, "accountNumber", account.AccountNumber)
			return "", "", "", "GZIP", "", "", "", err
		}
		for _, report := range result.ReportDefinitions {
			if reportName != "" && *report.ReportName != reportName {
				continue
			}
			if bucketName != "" && *report.S3Bucket != bucketName {
				continue
			}
			if report.Format != "textORcsv" {
				continue
			}
			// we only support daily reports for now
			if report.TimeUnit != "DAILY" {
				continue
			}
			return *report.S3Bucket, string(report.S3Region), *report.S3Prefix + "/" + *report.ReportName, string(report.Compression), string(report.ReportVersioning), *report.ReportName, string(report.TimeUnit), nil
		}
		nextToken = result.NextToken
		if nextToken == nil {
			break
		}
	}
	return "", "", "", "GZIP", "", "", "", errors.New("unable to find cost report")
}

func getS3KeysFromUsageReport(
	s3Svc *s3.Client,
	s3Bucket, pathPrefix, reportVersion string,
	reportName string,
	month time.Month,
	year int,
	timeunit string,
) ([]string, error) {

	s3Keys := []string{}

	if !strings.HasSuffix(pathPrefix, "/") {
		pathPrefix += "/"
	}

	startTime := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)

	// End date = first day of next month (handles Dec → Jan automatically)
	endTime := startTime.AddDate(0, 1, 0)

	startKey := startTime.Format("20060102")
	endKey := endTime.Format("20060102")

	path := pathPrefix + startKey + "-" + endKey + "/"
	manifestFile := path + fmt.Sprintf("%s-Manifest.json", reportName)

	input := &s3.GetObjectInput{
		Bucket: &s3Bucket,
		Key:    &manifestFile,
	}

	slog.Debug("aws: fetching cost report manifest",
		"bucket", s3Bucket,
		"key", manifestFile,
		"month", month,
		"year", year,
	)

	result, err := s3Svc.GetObject(context.TODO(), input)
	if err != nil {
		var noSuchKey *types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			slog.Warn("aws: cost report manifest not found",
				"bucket", s3Bucket,
				"key", manifestFile,
				"month", month,
				"year", year,
				"hint", "Report may not be configured, generated yet, or check S3 path configuration",
			)
			return s3Keys, nil
		}

		slog.Error("aws: failed to fetch cost report manifest",
			"bucket", s3Bucket,
			"key", manifestFile,
			"error", err,
		)

		return s3Keys, fmt.Errorf(
			"aws: failed to fetch cost report manifest for bucket %q key %q: %w",
			s3Bucket,
			manifestFile,
			err,
		)
	}

	defer func() {
		if err := result.Body.Close(); err != nil {
			slog.Error("aws: unable to close result body",
				"error", err,
				"bucket", s3Bucket,
				"key", manifestFile,
			)
		}
	}()

	bodyData, err := io.ReadAll(result.Body)
	if err != nil {
		return s3Keys, err
	}

	manifest := map[string]any{}
	if err := common.UnmarshalJson(bodyData, &manifest); err != nil {
		return s3Keys, err
	}

	reportKeysRaw, ok := manifest["reportKeys"]
	if !ok {
		return s3Keys, errors.New("unable to find reportKeys in manifest")
	}

	reportKeys, ok := reportKeysRaw.([]any)
	if !ok {
		return s3Keys, errors.New("reportKeys has unexpected type")
	}

	for _, key := range reportKeys {
		if s, ok := key.(string); ok {
			s3Keys = append(s3Keys, s)
		} else {
			slog.Warn("aws: non-string key found in manifest reportKeys, skipping", "key", key)
		}
	}

	return lo.Uniq(s3Keys), nil
}

func getAwsUsageReport(ctx providers.CloudProviderContext, account providers.Account, month time.Month, year int) (providers.GetUsageReportResponse, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		return providers.GetUsageReportResponse{}, nil
	}
	var s3Bucket, region, pathPrefix, compression, reportVersion, reportName, timeUnit string
	if account.Data != nil {
		accountData := map[string]any{}
		err := common.UnmarshalJson([]byte(*account.Data), &accountData)
		if err != nil {
			ctx.GetLogger().Error("unable to parse accountData", "error", err)
		}
		if name, ok := accountData["cost_report_name"]; ok {
			reportName = name.(string)
		}
		if bucket, ok := accountData["cost_report_s3_bucket"]; ok {
			s3Bucket = bucket.(string)
		}

		// For auto-registered accounts (org or single), all CUR details are pre-populated
		// from the CF callback. Skip DescribeReportDefinitions API call and use stored values.
		if curSource, ok := accountData["cur_source"]; ok && (curSource == "org_callback" || curSource == "auto_callback") {
			if v, ok := accountData["cost_report_s3_region"]; ok {
				region = v.(string)
			}
			if v, ok := accountData["cost_report_s3_prefix"]; ok {
				pathPrefix = v.(string)
			}
			if v, ok := accountData["cost_report_compression"]; ok {
				compression = v.(string)
			} else {
				compression = "GZIP"
			}
			if v, ok := accountData["cost_report_versioning"]; ok {
				reportVersion = v.(string)
			}
			if v, ok := accountData["cost_report_time_unit"]; ok {
				timeUnit = v.(string)
			}
			ctx.GetLogger().Info("aws: using pre-populated CUR config from org callback",
				"bucket", s3Bucket, "region", region, "prefix", pathPrefix,
				"reportName", reportName, "accountNumber", account.AccountNumber)
		}
	}

	// Only call DescribeReportDefinitions if CUR details were not pre-populated
	if region == "" {
		s3Bucket, region, pathPrefix, compression, reportVersion, reportName, timeUnit, err = getUsageBucketFromCostReport(ctx, account, reportName, s3Bucket)
		if err != nil {
			ctx.GetLogger().Error("unable to find cost report", "error", err)
			return providers.GetUsageReportResponse{}, err
		}
	}

	if s3Bucket == "" {
		return providers.GetUsageReportResponse{}, errors.New("unable to find cost report")
	}

	// Get the report from S3
	cfg.Region = region
	s3Svc := s3.NewFromConfig(cfg)

	//search for the report based on path prefix
	s3Keys, err := getS3KeysFromUsageReport(s3Svc, s3Bucket, pathPrefix, reportVersion, reportName, month, year, timeUnit)
	if err != nil {
		ctx.GetLogger().Warn("unable to find cost report", "bucket", s3Bucket, "pathPrefix", pathPrefix)
		return providers.GetUsageReportResponse{}, err
	}

	if len(s3Keys) == 0 {
		ctx.GetLogger().Warn("unable to find cost report", "bucket", s3Bucket, "pathPrefix", pathPrefix)
		return providers.GetUsageReportResponse{}, nil
	}

	items := []providers.UsageReportItem{}
	for _, key := range s3Keys {
		keyWithoutLeadingSlash := strings.TrimPrefix(key, "/")
		input := &s3.GetObjectInput{
			Bucket: &s3Bucket,
			Key:    &keyWithoutLeadingSlash,
		}
		result, err := s3Svc.GetObject(ctx.GetContext(), input)
		if err != nil {
			// Check if error is NoSuchKey - file doesn't exist yet or was removed
			var noSuchKey *types.NoSuchKey
			if errors.As(err, &noSuchKey) {
				slog.Warn("aws: cost report file not found, skipping",
					"bucket", s3Bucket,
					"key", key,
					"hint", "File may not be generated yet or was removed")
				continue // Skip this file and continue with others
			}
			// For other S3 errors (permissions, etc), return the error
			ctx.GetLogger().Error("failed to fetch cost report", "error", err, "bucket", s3Bucket, "key", key)
			return providers.GetUsageReportResponse{}, err
		}
		defer func() {
			if err := result.Body.Close(); err != nil {
				ctx.GetLogger().Error("unable to close result body", "error", err)
			}
		}()
		var stream io.Reader
		if compression == "GZIP" {
			gzr, err := gzip.NewReader(result.Body)
			if err != nil {
				ctx.GetLogger().Error("failed to decompress cost report", "error", err, "bucket", s3Bucket, "key", key)
				return providers.GetUsageReportResponse{}, err
			}
			stream = gzr
		} else if compression == "ZIP" {
			zipBodyBytes, err := io.ReadAll(result.Body)
			if err != nil {
				ctx.GetLogger().Error("failed to read S3 object body for ZIP", "error", err, "bucket", s3Bucket, "key", key)
				continue
			}

			bytesReader := bytes.NewReader(zipBodyBytes)
			zipReader, err := zip.NewReader(bytesReader, int64(len(zipBodyBytes)))
			if err != nil {
				ctx.GetLogger().Error("failed to create zip reader", "error", err, "bucket", s3Bucket, "key", key)
				continue // Skip this file
			}

			// Find the first .csv file in the archive
			foundCsv := false
			var zipFileReadCloser io.ReadCloser
			for _, zf := range zipReader.File {
				if strings.HasSuffix(strings.ToLower(zf.Name), ".csv") {
					ctx.GetLogger().Info("Found CSV file in ZIP archive", "zipFileName", zf.Name, "key", key)
					zipFileReadCloser, err = zf.Open()
					if err != nil {
						ctx.GetLogger().Error("failed to open file within zip archive", "error", err, "zipFileName", zf.Name, "bucket", s3Bucket, "key", key)
						break
					}
					stream = zipFileReadCloser
					foundCsv = true
					break
				}
			}

			if !foundCsv {
				// If opening the file failed in the loop, zipFileReadCloser might be nil
				if zipFileReadCloser == nil {
					ctx.GetLogger().Warn("No .csv file found or failed to open file within ZIP archive", "bucket", s3Bucket, "key", key)
				} else {
					// This case shouldn't happen if break works correctly, but handle defensively
					// This case shouldn't happen if break works correctly, but handle defensively
					if err := zipFileReadCloser.Close(); err != nil {
						ctx.GetLogger().Error("unable to close zipFileReadCloser", "error", err)
					} // Close if opened but loop exited unexpectedly
					zipFileReadCloser = nil
					ctx.GetLogger().Warn("No .csv file processed within ZIP archive", "bucket", s3Bucket, "key", key)
				}
				continue // Skip this S3 object
			}
		}

		if stream == nil {
			return providers.GetUsageReportResponse{}, errors.New("unable to find cost report")
		}

		streamCloser, ok := stream.(io.ReadCloser)
		if !ok {
			streamCloser = io.NopCloser(stream)
		}
		defer func() {
			if err := streamCloser.Close(); err != nil {
				ctx.GetLogger().Error("unable to close streamCloser", "error", err)
			}
		}()

		items2, err := readAwsBillingReport(stream, timeUnit)
		if err != nil {
			ctx.GetLogger().Error("failed to parse cost report", "error", err, "bucket", s3Bucket, "key", key)
			return providers.GetUsageReportResponse{}, err
		}
		items = append(items, items2...)
	}

	return providers.GetUsageReportResponse{Items: items, Dates: []time.Time{}}, nil
}
