package aws

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/samber/lo"
)

type ExtendedSupportInfo struct {
	EOLDate           time.Time
	InExtendedSupport bool
	Year1Cost         float64 // $/vCPU-hour
	Year2Cost         float64
	Year3PlusCost     float64
}

// getRDSExtendedSupportInfo checks if an engine version is in or nearing Extended Support
//
// AWS Native Recommendation Support:
//
//	AWS Cost Optimization Hub: Provides "Upgrade" recommendations for outdated instances
//	 - Already integrated via aws_cost_optimization_hub.go
//	 - Provides general upgrade recommendations but NOT Extended Support cost warnings
//	 - Does NOT provide EOL dates or Extended Support cost estimates
//
// AWS RDS API - DescribeDBEngineVersions:
//
//   - Has "DeprecationDate" field for engine versions
//
//   - Only populated AFTER AWS deprecates the version (reactive, not proactive)
//
//   - Does NOT warn 6+ months in advance
//
//   - Does NOT provide Extended Support pricing information
//
//     AWS Health Dashboard:
//
//   - Sends deprecation event notifications
//
//   - Per-account, reactive notifications only
//
//   - Cannot query future EOL dates programmatically
//
// Why This Implementation is Necessary:
// 1. Proactive Warnings: We need to warn 6 months BEFORE EOL (AWS APIs don't provide this)
// 2. Cost Estimation: We need to calculate Extended Support costs (AWS APIs don't provide pricing)
// 3. Multi-Version Support: We track multiple versions across engines simultaneously
//
// Recommendation: This approach is COMPLEMENTARY to AWS native recommendations:
// - AWS Cost Optimization Hub: General "upgrade" recommendations
// - This implementation: Specific Extended Support cost warnings with pricing
//
// EOL Date Sources (Official AWS Documentation):
// - MySQL: https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/MySQL.Concepts.VersionMgmt.html
// - PostgreSQL: https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/CHAP_PostgreSQL.html#PostgreSQL.Concepts.General.version-support
// - MariaDB: https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/MariaDB.Concepts.VersionMgmt.html
//
// Extended Support Pricing (AWS Official):
// Source: https://aws.amazon.com/rds/faqs/#Amazon_RDS_Extended_Support
// - Year 1 (after community EOL): $0.100 per vCPU-hour
// - Year 2: $0.200 per vCPU-hour
// - Year 3+: $0.400 per vCPU-hour
//
// Maintenance: Check AWS documentation quarterly and update EOL dates below.
func getRDSExtendedSupportInfo(engine, version string) *ExtendedSupportInfo {
	// Map of engine → version → EOL date
	// Source: AWS RDS official documentation (links above)
	// Updated: 2025-01 - Next review: 2025-04
	eolMap := map[string]map[string]string{
		"mysql": {
			"5.7": "2024-02-29", // MySQL 5.7 EOL - Extended Support active
			"8.0": "2026-04-30", // MySQL 8.0 EOL (projected from community schedule)
		},
		"postgres": {
			"11": "2023-11-09", // PostgreSQL 11 EOL - Extended Support active
			"12": "2024-11-14", // PostgreSQL 12 EOL - Extended Support active
			"13": "2025-11-13", // PostgreSQL 13 EOL (upcoming)
			"14": "2026-11-12", // PostgreSQL 14 EOL (projected from community schedule)
			"15": "2027-11-11", // PostgreSQL 15 EOL (projected from community schedule)
		},
		"mariadb": {
			"10.3":  "2023-05-25", // MariaDB 10.3 EOL - Extended Support active
			"10.4":  "2024-06-18", // MariaDB 10.4 EOL - Extended Support active
			"10.5":  "2025-06-24", // MariaDB 10.5 EOL (upcoming)
			"10.6":  "2026-07-06", // MariaDB 10.6 EOL (projected from community schedule)
			"10.11": "2028-02-16", // MariaDB 10.11 EOL (projected from community schedule)
		},
	}

	// Extract major version (e.g., "5.7" from "5.7.44")
	majorVersion := version
	parts := strings.Split(version, ".")
	if len(parts) >= 2 {
		majorVersion = parts[0] + "." + parts[1]
	}

	// Check if this engine/version combination has an EOL date
	if engineVersions, ok := eolMap[strings.ToLower(engine)]; ok {
		// For engines with single-number major versions (PostgreSQL), try first part only
		if _, ok := engineVersions[majorVersion]; !ok {
			// Try first part only (e.g., "11" from "11.22")
			if len(parts) >= 1 {
				majorVersion = parts[0]
			}
		}

		if eolDateStr, ok := engineVersions[majorVersion]; ok {
			eolDate, err := time.Parse("2006-01-02", eolDateStr)
			if err != nil {
				return nil
			}

			now := time.Now()
			inExtendedSupport := now.After(eolDate)

			// Extended Support pricing from AWS official FAQ
			// https://aws.amazon.com/rds/faqs/#Amazon_RDS_Extended_Support
			return &ExtendedSupportInfo{
				EOLDate:           eolDate,
				InExtendedSupport: inExtendedSupport,
				Year1Cost:         0.100, // Year 1: $0.100 per vCPU-hour (AWS official)
				Year2Cost:         0.200, // Year 2: $0.200 per vCPU-hour (AWS official)
				Year3PlusCost:     0.400, // Year 3+: $0.400 per vCPU-hour (AWS official)
			}
		}
	}

	return nil
}

// getRDSStoragePricing gets the pricing for RDS storage type using AWS Pricing API
// Returns price per GB-month for the specified storage type
//
// Pricing is fetched dynamically from AWS Pricing API, which provides:
// - Real-time regional pricing
// - Accurate cost calculations
// - Automatic updates when AWS changes pricing
//
// Fallback pricing (used only if API fails):
// - gp2 (General Purpose SSD): ~$0.115/GB-month (us-east-1 baseline)
// - gp3 (General Purpose SSD v3): ~$0.08/GB-month (us-east-1 baseline)
// Source: https://aws.amazon.com/rds/pricing/ (as of 2025)
func getRDSStoragePricing(cfg aws.Config, region string, storageType string) (float64, error) {
	filtersMap := map[string]string{
		"regionCode":    region,
		"productFamily": "Database Storage",
		"volumeType":    storageType,
	}

	priceList, err := getAvailableInstancesFromPricing(cfg, "AmazonRDS", filtersMap)
	if err != nil {
		return 0, err
	}

	if len(priceList) == 0 {
		return 0, fmt.Errorf("no pricing found for storage type %s in region %s", storageType, region)
	}

	// Get price per GB-month
	price, err := getPricingValue(priceList[0])
	if err != nil {
		return 0, err
	}

	return price, nil
}

// getGravitonInstanceFamily returns the Graviton equivalent instance family
// Returns empty string if no Graviton equivalent exists
//
// Graviton mapping logic:
// - Instance families ending with 'g' are already Graviton (r7g, m7g, t4g, etc.)
// - For non-Graviton instances, we map to the latest Graviton generation:
//   - r-series (memory-optimized): r5, r6i, r6id, r6in → r7g
//   - m-series (general-purpose): m5, m6i, m6id, m6in → m7g
//   - t-series (burstable): t2, t3, t3a → t4g
//   - x-series (memory-intensive): x2iedn, x2iezn → x2g
//
// This approach is more maintainable than hardcoding as it:
// 1. Automatically handles new instance sizes (e.g., r6i.xlarge, r6i.2xlarge)
// 2. Uses AWS's consistent naming pattern (generation number + 'g' suffix)
// 3. Can be updated by modifying just the generation mappings
//
// Source: AWS RDS instance types documentation
// https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/Concepts.DBInstanceClass.html
func getGravitonInstanceFamily(instanceClass string) string {
	return getGravitonInstanceType(instanceClass, "db.")
}

func getAvailableRdsInstances(cfg aws.Config, region string, engine string, memory string, cpu string, instanceType string, deploymentOption string) ([]map[string]interface{}, error) {
	filtersMap := map[string]string{}

	if region != "" {
		filtersMap["regionCode"] = region
	}
	if instanceType != "" {
		filtersMap["instanceType"] = instanceType
	}
	if engine != "" {
		// AWS Pricing API requires the canonical databaseEngine value (e.g. "MySQL"),
		// but DescribeDBInstances returns lowercase forms like "mysql", "aurora-postgresql".
		filtersMap["databaseEngine"] = normalizeRdsEngineForPricing(engine)
	}
	if memory != "" {
		filtersMap["memory"] = memory
	}
	if cpu != "" {
		filtersMap["vcpu"] = cpu
	}
	if deploymentOption != "" {
		filtersMap["deploymentOption"] = deploymentOption
	}
	return getAvailableInstancesFromPricing(cfg, "AmazonRDS", filtersMap)
}

// normalizeRdsEngineForPricing maps AWS RDS engine values returned by DescribeDBInstances
// (lowercase, often hyphenated) to AWS Pricing API canonical databaseEngine values.
// If the value is already canonical or unknown, it is returned unchanged.
func normalizeRdsEngineForPricing(awsEngine string) string {
	e := strings.ToLower(strings.TrimSpace(awsEngine))
	switch {
	case e == "":
		return awsEngine
	case e == "mysql":
		return "MySQL"
	case e == "mariadb":
		return "MariaDB"
	case e == "postgres", e == "postgresql":
		return "PostgreSQL"
	case e == "aurora-mysql":
		return "Aurora MySQL"
	case e == "aurora-postgresql":
		return "Aurora PostgreSQL"
	case strings.HasPrefix(e, "oracle"):
		return "Oracle"
	case strings.HasPrefix(e, "sqlserver"):
		return "SQL Server"
	case strings.HasPrefix(e, "db2"):
		return "Db2"
	default:
		return awsEngine
	}
}

// rdsPricingFailureCache tracks recent failed pricing lookups so we don't repeatedly
// hit AWS Pricing API for instance types that have no static pricing match.
var rdsPricingFailureCache sync.Map // key: "region:dbClass:engine:deploymentOption" → time.Time expiry

const rdsPricingFailureCacheTTL = 1 * time.Hour

// getRdsInstanceTypeDetails returns the pricing-and-attributes map persisted in
// resource.Meta["InstanceTypeDetails"]. It mirrors the AWS Pricing API GetProducts
// JSON shape so existing consumers (getPricingValue, product.attributes lookups)
// continue to work unchanged.
//
// Lookup order:
//  1. Aurora Serverless v2 (db.serverless) is priced per ACU, not per instance class.
//     We skip pricing entirely and return an empty map so downstream code does not
//     synthesize bogus right-size / Graviton recommendations.
//  2. cloud_resource_details DB cache (populated by EC2 path and prior RDS runs).
//  3. AWS Pricing API live, with persist-on-success so subsequent runs hit the cache.
//
// Returns an empty map when nothing is available; callers must treat empty as "no pricing".
func getRdsInstanceTypeDetails(ctx providers.CloudProviderContext, cfg aws.Config, region, awsEngine, dbClass, deploymentOption string) map[string]any {
	if dbClass == "db.serverless" {
		return map[string]any{}
	}
	normalizedEngine := normalizeRdsEngineForPricing(awsEngine)

	if details := lookupRdsInstanceTypeDetailsFromDb(region, dbClass, normalizedEngine, deploymentOption); details != nil {
		return details
	}

	cacheKey := strings.Join([]string{region, dbClass, normalizedEngine, deploymentOption}, ":")
	if expiry, ok := rdsPricingFailureCache.Load(cacheKey); ok {
		if exp, _ := expiry.(time.Time); time.Now().Before(exp) {
			return map[string]any{}
		}
		rdsPricingFailureCache.Delete(cacheKey)
	}

	instanceTypes, err := getAvailableRdsInstances(cfg, region, awsEngine, "", "", dbClass, deploymentOption)
	if err != nil {
		ctx.GetLogger().Warn("rds pricing: AWS Pricing API call failed", "error", err, "instanceClass", dbClass, "engine", awsEngine, "region", region)
		rdsPricingFailureCache.Store(cacheKey, time.Now().Add(rdsPricingFailureCacheTTL))
		return map[string]any{}
	}
	if len(instanceTypes) == 0 {
		ctx.GetLogger().Warn("rds pricing: no AWS Pricing API match", "instanceClass", dbClass, "engine", awsEngine, "region", region)
		rdsPricingFailureCache.Store(cacheKey, time.Now().Add(rdsPricingFailureCacheTTL))
		return map[string]any{}
	}
	details := instanceTypes[0]
	persistRdsInstanceTypeDetailsToDb(region, dbClass, normalizedEngine, deploymentOption, details)
	return details
}

// lookupRdsInstanceTypeDetailsFromDb returns a synthetic InstanceTypeDetails map built
// from cloud_resource_details, or nil when no row is found.
func lookupRdsInstanceTypeDetailsFromDb(region, dbClass, normalizedEngine, deploymentOption string) map[string]any {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil
	}
	var row struct {
		ResourceCost float64 `db:"resource_cost"`
		Attributes   string  `db:"attributes"`
	}
	err = dbms.QueryRowAndScan(&row,
		`SELECT resource_cost, COALESCE(attributes::text, '{}') AS attributes
		 FROM cloud_resource_details
		 WHERE cloud_provider = 'aws' AND service_name = 'AmazonRDS'
		   AND resource_region = $1 AND resource_type = $2
		   AND database_engine = $3 AND deployment_option = $4
		   AND pricing_model = 'on_demand'
		 LIMIT 1`,
		region, dbClass, normalizedEngine, deploymentOption,
	)
	if err != nil {
		return nil
	}
	attrs := map[string]any{}
	if row.Attributes != "" {
		_ = common.UnmarshalJsonString(row.Attributes, &attrs)
	}
	return synthesizeRdsInstanceTypeDetails(row.ResourceCost, attrs)
}

// synthesizeRdsInstanceTypeDetails builds a minimal InstanceTypeDetails map matching
// the AWS Pricing API GetProducts response shape. Downstream consumers read pricing
// via getPricingValue (terms.OnDemand[*].priceDimensions[*].pricePerUnit.USD) and
// attributes via product.attributes — both work against this shape.
func synthesizeRdsInstanceTypeDetails(pricePerHour float64, attributes map[string]any) map[string]any {
	return map[string]any{
		"product": map[string]any{
			"attributes": attributes,
		},
		"terms": map[string]any{
			"OnDemand": map[string]any{
				"synthetic": map[string]any{
					"priceDimensions": map[string]any{
						"synthetic": map[string]any{
							"pricePerUnit": map[string]any{
								"USD": strconv.FormatFloat(pricePerHour, 'f', -1, 64),
							},
						},
					},
				},
			},
		},
	}
}

// persistRdsInstanceTypeDetailsToDb caches a freshly fetched AWS Pricing API entry
// in cloud_resource_details so subsequent runs hit the DB instead of AWS.
func persistRdsInstanceTypeDetailsToDb(region, dbClass, normalizedEngine, deploymentOption string, instanceTypeDetails map[string]any) {
	if len(instanceTypeDetails) == 0 {
		return
	}
	price, err := getPricingValue(instanceTypeDetails)
	if err != nil || price == 0 {
		return
	}
	attrsJson := "{}"
	capacityJson := "{}"
	if product, ok := instanceTypeDetails["product"].(map[string]any); ok {
		if a, ok := product["attributes"].(map[string]any); ok {
			if b, mErr := common.MarshalJson(a); mErr == nil {
				attrsJson = string(b)
			}
			vcpu, _ := a["vcpu"].(string)
			mem, _ := a["memory"].(string)
			memGiB := parseAwsMemoryGiB(mem)
			if c, mErr := common.MarshalJson(map[string]any{"cpu_virtual": vcpu, "memory_gb": memGiB}); mErr == nil {
				capacityJson = string(c)
			}
		}
	}
	dbms, dErr := common.GetDatabaseManager(common.Metastore)
	if dErr != nil {
		return
	}
	_, err = dbms.Exec(
		`INSERT INTO cloud_resource_details
		 (cloud_provider, service_name, service_type, resource_type, resource_region,
		  resource_cost, resource_capacity, attributes, operating_system, tenancy,
		  pricing_model, price_unit, database_engine, deployment_option)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		 ON CONFLICT (cloud_provider, service_name, service_type, resource_type, resource_region,
		              pricing_model, database_engine, deployment_option)
		 DO UPDATE SET resource_cost = EXCLUDED.resource_cost,
		               attributes = EXCLUDED.attributes,
		               resource_capacity = EXCLUDED.resource_capacity`,
		"aws", "AmazonRDS", "RDS", dbClass, region,
		price, capacityJson, attrsJson, "Linux", "Shared",
		"on_demand", "hourly", normalizedEngine, deploymentOption,
	)
	if err != nil {
		slog.Warn("rds pricing: failed to persist to cloud_resource_details", "region", region, "instanceType", dbClass, "engine", normalizedEngine, "error", err)
	}
}

func dbInstanceStatusToNbStatus(status string) providers.ResourceStatus {
	switch strings.ToLower(status) {
	case "available", "backing-up", "configuring-enhanced-monitoring", "configuring-iam-database-auth", "configuring-log-exports", "converting-to-vpc", "creating", "delete-precheck", "modifying", "rebooting", "resetting-master-credentials", "upgrading", "maintenance", "starting":
		return providers.ResourceStatusActive
	case "deleting":
		return providers.ResourceStatusDeleted
	case "failed", "stopped", "stopping":
		return providers.ResourceStatusInactive
	default:
		return providers.ResourceStatusUnknown
	}
}

type amazonRds struct {
	DefaultAwsServiceImpl
}

func (a *amazonRds) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Check if this is an alarm recommendation
	if strings.HasPrefix(recommendation.RuleName, "aws_rds_") && strings.HasSuffix(recommendation.RuleName, "_alarm_missing") {
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

// resolveRdsCluster returns the parent cluster identifier for an Aurora
// cluster-member instance, or "" for a standalone (non-Aurora) RDS instance.
// Used to route start/stop to StartDBCluster/StopDBCluster, which is the only
// way to start/stop Aurora (per-instance start/stop is rejected by the API).
func resolveRdsCluster(ctx context.Context, client *rds.Client, dbInstanceID string) (string, error) {
	out, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(dbInstanceID),
	})
	if err != nil {
		return "", err
	}
	if len(out.DBInstances) == 0 {
		return "", fmt.Errorf("DB instance %s not found", dbInstanceID)
	}
	if out.DBInstances[0].DBClusterIdentifier != nil {
		return *out.DBInstances[0].DBClusterIdentifier, nil
	}
	return "", nil
}

func (a *amazonRds) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	var resultMessage string
	var resultErr error

	// Always audit, even on early returns
	defer func() {
		status := "SUCCESS"
		if resultErr != nil {
			status = "FAILURE"
		}

		auditErr := logResourceActionAudit(ctx, command, account, status, resultMessage)
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

	client := rds.NewFromConfig(cfg)

	// Get DB instance identifier
	dbInstanceID := command.ResourceId
	if dbInstanceID == "" {
		if id, ok := command.Args["db_instance_identifier"].(string); ok {
			dbInstanceID = id
		}
	}

	if dbInstanceID == "" {
		resultErr = fmt.Errorf("db_instance_identifier required")
		resultMessage = resultErr.Error()
		return providers.ApplyCommandResponse{}, resultErr
	}

	switch command.Command {
	case "start":
		// Aurora cluster members can't be started individually — the AWS API
		// requires StartDBCluster on the parent. Look up the instance to see
		// if it's a cluster member; route accordingly. Reboot is unaffected
		// (RebootDBInstance works for both Aurora and standalone instances).
		clusterID, lookupErr := resolveRdsCluster(ctx.GetContext(), client, dbInstanceID)
		if lookupErr != nil {
			resultErr = fmt.Errorf("failed to look up DB instance %s: %w", dbInstanceID, lookupErr)
			resultMessage = resultErr.Error()
			break
		}
		if clusterID != "" {
			_, err := client.StartDBCluster(ctx.GetContext(), &rds.StartDBClusterInput{
				DBClusterIdentifier: aws.String(clusterID),
			})
			if err != nil {
				resultErr = fmt.Errorf("failed to start Aurora cluster %s: %w", clusterID, err)
				resultMessage = resultErr.Error()
			} else {
				resultMessage = fmt.Sprintf("Successfully started Aurora cluster %s (containing instance %s)", clusterID, dbInstanceID)
			}
		} else {
			_, err := client.StartDBInstance(ctx.GetContext(), &rds.StartDBInstanceInput{
				DBInstanceIdentifier: aws.String(dbInstanceID),
			})
			if err != nil {
				resultErr = fmt.Errorf("failed to start RDS instance: %w", err)
				resultMessage = resultErr.Error()
			} else {
				resultMessage = fmt.Sprintf("Successfully started RDS instance %s", dbInstanceID)
			}
		}

	case "stop":
		clusterID, lookupErr := resolveRdsCluster(ctx.GetContext(), client, dbInstanceID)
		if lookupErr != nil {
			resultErr = fmt.Errorf("failed to look up DB instance %s: %w", dbInstanceID, lookupErr)
			resultMessage = resultErr.Error()
			break
		}
		if clusterID != "" {
			_, err := client.StopDBCluster(ctx.GetContext(), &rds.StopDBClusterInput{
				DBClusterIdentifier: aws.String(clusterID),
			})
			if err != nil {
				resultErr = fmt.Errorf("failed to stop Aurora cluster %s: %w", clusterID, err)
				resultMessage = resultErr.Error()
			} else {
				resultMessage = fmt.Sprintf("Successfully stopped Aurora cluster %s (containing instance %s)", clusterID, dbInstanceID)
			}
		} else {
			_, err := client.StopDBInstance(ctx.GetContext(), &rds.StopDBInstanceInput{
				DBInstanceIdentifier: aws.String(dbInstanceID),
			})
			if err != nil {
				resultErr = fmt.Errorf("failed to stop RDS instance: %w", err)
				resultMessage = resultErr.Error()
			} else {
				resultMessage = fmt.Sprintf("Successfully stopped RDS instance %s", dbInstanceID)
			}
		}

	case "reboot":
		_, err := client.RebootDBInstance(ctx.GetContext(), &rds.RebootDBInstanceInput{
			DBInstanceIdentifier: aws.String(dbInstanceID),
		})
		if err != nil {
			resultErr = fmt.Errorf("failed to reboot RDS instance: %w", err)
			resultMessage = resultErr.Error()
		} else {
			resultMessage = fmt.Sprintf("Successfully rebooted RDS instance %s", dbInstanceID)
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

func (a *amazonRds) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Check if this is a Performance Insights request
	if filter.MetricNamespace == "AWS/PI" || isPerformanceInsightsMetric(filter.MetricNames) {
		return getAwsPerformanceInsightsMetrics(ctx, account, filter)
	}

	// Otherwise, use standard CloudWatch metrics
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

// isPerformanceInsightsMetric checks if any metric name is a PI metric
func isPerformanceInsightsMetric(metricNames []string) bool {
	piPrefixes := []string{"db.load", "db.wait"}
	for _, metricName := range metricNames {
		for _, prefix := range piPrefixes {
			if strings.HasPrefix(metricName, prefix) {
				return true
			}
		}
	}
	return false
}

func (a *amazonRds) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", region)
		return []providers.Resource{}, err
	}
	svc := rds.NewFromConfig(cfg)
	resources := []providers.Resource{}
	instanceTypeMap := make(map[string]map[string]any) // Cache for pricing details

	// Fetch all CloudWatch alarms for this region once, instead of per-instance.
	alarmsByResource := fetchAlarmsByResource(ctx, account, region)

	// Loop to handle pagination for DescribeDBInstances
	paginator := rds.NewDescribeDBInstancesPaginator(svc, &rds.DescribeDBInstancesInput{})
	for paginator.HasMorePages() {
		instances, err := paginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch rds resources", "error", err, "accountNumber", account.AccountNumber, "region", region)
			// Return resources collected so far if pagination fails mid-way
			return resources, err
		}

		for _, instance := range instances.DBInstances {
			// --- Nil Checks for Essential Fields ---
			if instance.DBInstanceIdentifier == nil || instance.DBInstanceStatus == nil || instance.DBInstanceArn == nil || instance.InstanceCreateTime == nil || instance.DBInstanceClass == nil || instance.Engine == nil {
				ctx.GetLogger().Warn("Skipping RDS instance due to missing essential fields", "instance", instance)
				continue
			}

			tags := make(map[string][]string)
			for _, tag := range instance.TagList {
				// Added nil checks for tag key/value
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}
			instanceMap := structToMap(instance) // Local map for current instance meta

			// --- Fetch and Cache Instance Type Pricing Details ---
			deploymentOptionForCacheKey := "Single-AZ"
			if instance.MultiAZ != nil && *instance.MultiAZ {
				deploymentOptionForCacheKey = "Multi-AZ"
			}
			instanceTypeKey := fmt.Sprintf("%s:%s:%s", *instance.DBInstanceClass, *instance.Engine, deploymentOptionForCacheKey)

			if _, ok := instanceTypeMap[instanceTypeKey]; !ok {
				deploymentOption := "Single-AZ"
				if instance.MultiAZ != nil && *instance.MultiAZ {
					deploymentOption = "Multi-AZ"
				}
				instanceTypeMap[instanceTypeKey] = getRdsInstanceTypeDetails(ctx, cfg, region, *instance.Engine, *instance.DBInstanceClass, deploymentOption)
			}
			instanceMap["InstanceTypeDetails"] = instanceTypeMap[instanceTypeKey]

			// --- Extract VpcId to top level for cloud_resource action ---
			// cloud_resource expects Meta["VpcId"], but it's nested in DBSubnetGroup
			if instance.DBSubnetGroup != nil && instance.DBSubnetGroup.VpcId != nil {
				instanceMap["VpcId"] = *instance.DBSubnetGroup.VpcId
			}

			// --- Fetch Alarms ---
			instanceMap["AlarmDetails"] = alarmsByResource[*instance.DBInstanceIdentifier]

			resource := providers.Resource{
				Id:          *instance.DBInstanceIdentifier,
				ServiceName: ServiceNameRDS,
				Name:        *instance.DBInstanceIdentifier,
				Status:      dbInstanceStatusToNbStatus(*instance.DBInstanceStatus),
				Region:      region,
				Tags:        tags,
				Meta:        instanceMap,
				Arn:         *instance.DBInstanceArn,
				Type:        getAwsServiceResourceType(ServiceNameRDS, "db"),
				CreatedAt:   *instance.InstanceCreateTime,
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

// https://www.trendmicro.com/cloudoneconformity/knowledge-base/aws/RDS/
func (a *amazonRds) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	startDate := time.Now().Add(-time.Hour * 24 * 7)
	yesterdayDate := time.Now().Add(-time.Hour * 24 * 1)
	endDate := time.Now()

	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)

	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return nil, err
	}

	regions := []string{}
	for _, resource := range existingResources {
		regions = append(regions, resource.Region)
	}
	regions = lo.Uniq(regions)

	// get reserved instances
	regionReservedInstances := map[string][]string{}
	for _, region := range regions {
		regionalCfg := cfg.Copy()
		regionalCfg.Region = region
		svc := rds.NewFromConfig(regionalCfg)
		reservedInstances, err := svc.DescribeReservedDBInstances(context.TODO(), &rds.DescribeReservedDBInstancesInput{})
		if err != nil {
			ctx.GetLogger().Error("failed to fetch rds reserved instances", "error", err, "accountNumber", account.AccountNumber)
		}

		// Unused RDS Reserved Instances
		count := 0
		reservedInsatanceTypes := []string{}
		for _, reservedInstance := range reservedInstances.ReservedDBInstances {
			if *reservedInstance.State == "active" {
				count = int(*reservedInstance.DBInstanceCount)
			}
			reservedInsatanceTypes = append(reservedInsatanceTypes, *reservedInstance.DBInstanceClass)
		}

		regionReservedInstances[region] = reservedInsatanceTypes

		if count == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_rds_reservedinstance_configured",
				Severity:            providers.RecommendationSeverityHigh,
				Savings:             0,
				Data:                map[string]any{},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: ServiceNameRDS,
				ResourceId:          account.AccountNumber,
				ResourceType:        "instance-reservation",
				ResourceRegion:      region,
			})
		}
	}

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}
		existingInsatnceDetail, ok := resource.Meta["InstanceTypeDetails"].(map[string]any)
		if !ok {
			continue
		}
		existingInsatnceDetailAttributes := map[string]any{}
		if product, ok := existingInsatnceDetail["product"].(map[string]any); ok {
			if attributes, ok := product["attributes"].(map[string]any); ok {
				existingInsatnceDetailAttributes = attributes
			}
		}
		// Pricing may be unavailable for this instance type (e.g. db.serverless,
		// or AWS Pricing API had no match at discovery time). Treat that as a
		// soft signal: pricing-dependent recommendations downstream are gated on
		// currentInsatnceCost > 0 or len(existingInsatnceDetail) > 0.
		currentInsatnceCost := 0.0
		if len(existingInsatnceDetail) > 0 {
			price, err := getPricingValue(existingInsatnceDetail)
			if err != nil {
				ctx.GetLogger().Warn("rds: pricing data unavailable for instance, skipping pricing-based recommendations", "error", err, "accountNumber", account.AccountNumber, "region", resource.Region, "resourceId", resource.Id)
			} else {
				currentInsatnceCost = price
			}
		}
		existingInsatnceDetailMemoryBytes := 0.0
		if memoryStr, ok := existingInsatnceDetailAttributes["memory"].(string); ok && memoryStr != "" {
			memoryStr = strings.Split(memoryStr, " ")[0]
			existingInsatnceDetailMemoryBytes1, err := strconv.ParseFloat(memoryStr, 64)
			if err != nil {
				ctx.GetLogger().Warn("failed to get available rds instances", "error", err, "accountNumber", account.AccountNumber, "region", resource.Region)
			}
			existingInsatnceDetailMemoryBytes = existingInsatnceDetailMemoryBytes1 * 1024 * 1024 * 1024
		}

		// check if instance is in reserved instances
		dbInstanceClass, ok := meta["DBInstanceClass"].(string)
		if !ok {
			continue
		}
		if !lo.Contains(regionReservedInstances[resource.Region], dbInstanceClass) {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryRightSizing,
				RuleName:            "aws_rds_instance_reserved",
				Severity:            providers.RecommendationSeverityHigh,
				Savings:             0,
				Data:                map[string]any{},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Cluster Deletion Protection
		if meta["DeletionProtection"] == false {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_rds_delete_protection",
				Severity:            providers.RecommendationSeverityHigh,
				Savings:             0,
				Data:                map[string]any{},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// public access
		if meta["PubliclyAccessible"] == true {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_rds_public_access",
				Severity:            providers.RecommendationSeverityHigh,
				Savings:             0,
				Data:                map[string]any{},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Enable Instance Storage AutoScaling
		if maxStorage, ok := meta["MaxAllocatedStorage"]; !ok || maxStorage == nil {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_rds_storage_autoscaling",
				Severity:            providers.RecommendationSeverityHigh,
				Savings:             0,
				Data:                map[string]any{},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// RDS gp2 → gp3 Storage Migration
		if storageType, ok := meta["StorageType"].(string); ok && storageType == "gp2" {
			allocatedStorage := 0.0
			if allocatedStorageFloat, ok := meta["AllocatedStorage"].(float64); ok {
				allocatedStorage = allocatedStorageFloat
			}

			// Get actual pricing from AWS Pricing API
			gp2Price, err := getRDSStoragePricing(cfg, resource.Region, "General Purpose")
			if err != nil {
				ctx.GetLogger().Warn("failed to get gp2 storage pricing, using default", "error", err, "region", resource.Region)
				// Fallback: us-east-1 baseline pricing from https://aws.amazon.com/rds/pricing/
				// Updated: 2025-01 - Verify quarterly for price changes
				gp2Price = 0.115 // $0.115 per GB-month (gp2 storage)
			}

			gp3Price, err := getRDSStoragePricing(cfg, resource.Region, "General Purpose-GP3")
			if err != nil {
				ctx.GetLogger().Warn("failed to get gp3 storage pricing, using default", "error", err, "region", resource.Region)
				// Fallback: us-east-1 baseline pricing from https://aws.amazon.com/rds/pricing/
				// Updated: 2025-01 - Verify quarterly for price changes
				gp3Price = 0.08 // $0.08 per GB-month (gp3 storage)
			}

			// Calculate monthly savings
			monthlySavings := (gp2Price - gp3Price) * allocatedStorage

			if monthlySavings > 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryInfraUpgrade,
					RuleName:     "aws_rds_gp2_storage",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      monthlySavings,
					Data: map[string]any{
						"current_storage_type": "gp2",
						"recommended_type":     "gp3",
						"allocated_storage_gb": allocatedStorage,
						"current_cost_per_gb":  gp2Price,
						"gp3_cost_per_gb":      gp3Price,
						"monthly_savings":      monthlySavings,
						"reason":               fmt.Sprintf("Instance is using gp2 storage (%v GB). Migrating to gp3 can save $%.2f/month", allocatedStorage, monthlySavings),
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Idle RDS Instance
		isIdle := true
		connectionMetrics, err := a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
			ResourceIds: []string{resource.Id},
			ServiceName: resource.ServiceName,
			StartDate:   &startDate,
			EndDate:     &endDate,
			Region:      resource.Region,
			MetricNames: []string{"DatabaseConnections"},
			Step:        3600 * time.Second,
			Statistics:  []string{"Maximum"},
		})
		if err != nil {
			ctx.GetLogger().Error("Error getting connection metrics", "resourceId", resource.Id, "error", err)
			return nil, err
		}

		// Validate and safely copy the first metric
		var firstConnectionMetric *providers.MetricItem = nil
		if len(connectionMetrics.Items) > 0 {
			metricCopy := connectionMetrics.Items[0]
			firstConnectionMetric = &metricCopy

			for _, metric := range metricCopy.Values {
				if metric > 5 {
					isIdle = false
					break
				}
			}
		}

		// Check additional metrics only if still idle
		if isIdle {
			// read iops
			readMetrics, err := a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
				ResourceIds: []string{resource.Id},
				ServiceName: resource.ServiceName,
				StartDate:   &startDate,
				EndDate:     &endDate,
				Region:      resource.Region,
				MetricNames: []string{"DatabaseConnections"},
				Step:        3600 * time.Second,
				Statistics:  []string{"Maximum"},
			})
			if err != nil {
				ctx.GetLogger().Error("Error getting read metrics", "resourceId", resource.Id, "error", err)
				return nil, err
			}
			if len(readMetrics.Items) > 0 {
				for _, metric := range readMetrics.Items[0].Values {
					if metric > 1 {
						isIdle = false
						break
					}
				}
			}
		}

		if isIdle {
			readIOPSMetrics, err := a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
				ResourceIds: []string{resource.Id},
				ServiceName: resource.ServiceName,
				StartDate:   &startDate,
				EndDate:     &endDate,
				Region:      resource.Region,
				MetricNames: []string{"ReadIOPS"},
				Step:        3600 * time.Second,
				Statistics:  []string{"Sum"},
			})
			if err != nil {
				ctx.GetLogger().Error("Error getting read IOPS metrics", "resourceId", resource.Id, "error", err)
				return nil, err
			}
			if len(readIOPSMetrics.Items) > 0 {
				for _, metric := range readIOPSMetrics.Items[0].Values {
					if metric > 20 {
						isIdle = false
						break
					}
				}
			}
		}

		if isIdle {
			writeMetrics, err := a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
				ResourceIds: []string{resource.Id},
				ServiceName: resource.ServiceName,
				StartDate:   &startDate,
				EndDate:     &endDate,
				Region:      resource.Region,
				MetricNames: []string{"WriteIOPS"},
				Step:        3600 * time.Second,
				Statistics:  []string{"Sum"},
			})
			if err != nil {
				ctx.GetLogger().Error("Error getting write IOPS metrics", "resourceId", resource.Id, "error", err)
				return nil, err
			}
			if len(writeMetrics.Items) > 0 {
				for _, metric := range writeMetrics.Items[0].Values {
					if metric > 20 {
						isIdle = false
						break
					}
				}
			}
		}

		// Only create recommendation if metric is available
		if isIdle && firstConnectionMetric != nil {
			savings := currentInsatnceCost * 24 * 30

			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryRightSizing,
				RuleName:     "aws_rds_idle_instance",
				Severity:     providers.RecommendationSeverityHigh,
				Savings:      savings,
				Data: map[string]any{
					"connections": *firstConnectionMetric, // use safe copy
					"startDate":   startDate.Format(time.RFC3339),
					"endDate":     endDate.Format(time.RFC3339),
				},
				Action:              providers.RecommendationActionDelete,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Aurora Serverless v2 Migration Recommendation
		// Safe type assertions to prevent panics
		engineStr, _ := meta["Engine"].(string)

		// Detect Aurora engines
		isAurora := false
		if engineStr == "aurora" || engineStr == "aurora-mysql" || engineStr == "aurora-postgresql" {
			isAurora = true
		}

		if isAurora && !isIdle {
			// Serverless v2 detection: Must have ServerlessV2ScalingConfiguration
			// Note: Serverless v2 uses EngineMode: "provisioned" (not "serverless")
			// Serverless v1 uses EngineMode: "serverless" without ServerlessV2ScalingConfiguration
			isServerlessV2 := meta["ServerlessV2ScalingConfiguration"] != nil

			if !isServerlessV2 {
				// Provisioned Aurora - check for low utilization
				// Fetch CPU and connection metrics
				cpuMetrics, errCPU := a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
					ResourceIds: []string{resource.Id},
					ServiceName: resource.ServiceName,
					StartDate:   &startDate,
					EndDate:     &endDate,
					Region:      resource.Region,
					MetricNames: []string{"CPUUtilization"},
					Step:        3600 * time.Second,
					Statistics:  []string{"Average"},
				})

				connectionMetricsAurora, errConn := a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
					ResourceIds: []string{resource.Id},
					ServiceName: resource.ServiceName,
					StartDate:   &startDate,
					EndDate:     &endDate,
					Region:      resource.Region,
					MetricNames: []string{"DatabaseConnections"},
					Step:        3600 * time.Second,
					Statistics:  []string{"Average"},
				})

				if errCPU == nil && errConn == nil && len(cpuMetrics.Items) > 0 && len(connectionMetricsAurora.Items) > 0 {
					avgCPU := 0.0
					if len(cpuMetrics.Items[0].Values) > 0 {
						sum := 0.0
						for _, v := range cpuMetrics.Items[0].Values {
							sum += v
						}
						avgCPU = sum / float64(len(cpuMetrics.Items[0].Values))
					}

					avgConnections := 0.0
					if len(connectionMetricsAurora.Items[0].Values) > 0 {
						sum := 0.0
						for _, v := range connectionMetricsAurora.Items[0].Values {
							sum += v
						}
						avgConnections = sum / float64(len(connectionMetricsAurora.Items[0].Values))
					}

					// Recommend Serverless v2 if CPU < 20% and connections < 10
					if avgCPU < 20.0 && avgConnections < 10 {
						// Estimate savings
						// Provisioned cost: currentInsatnceCost * 24 * 30
						// Serverless v2 cost estimation:
						// - Minimum 0.5 ACU = ~$43.80/month for Aurora MySQL, $47.45/month for PostgreSQL
						// - Each ACU = ~$87.60/month (MySQL) or $94.90/month (PostgreSQL)
						provisionedMonthlyCost := currentInsatnceCost * 24 * 30

						// Estimate ACU usage based on CPU utilization
						// Typical: 1 ACU ≈ 2 GB RAM, 1 vCPU at certain performance
						// Rough approximation: avgCPU% / 100 * instance_vcpus = avg ACUs needed
						estimatedACUs := 0.5 // Minimum
						if existingInsatnceDetailAttributes["vcpu"] != nil {
							if vcpuStr, ok := existingInsatnceDetailAttributes["vcpu"].(string); ok {
								if vcpus, errVcpu := strconv.ParseFloat(vcpuStr, 64); errVcpu == nil {
									estimatedACUs = (avgCPU / 100.0) * vcpus
									if estimatedACUs < 0.5 {
										estimatedACUs = 0.5
									}
								}
							}

							// Cost per ACU per hour (us-east-1)
							// NOTE: Aurora pricing varies by region. Other regions may be 25-50% more expensive.
							acuCostPerHour := 0.12 // $0.12 per ACU-hour for Aurora MySQL
							if strings.Contains(engineStr, "postgresql") {
								acuCostPerHour = 0.13 // $0.13 per ACU-hour for Aurora PostgreSQL
							}

							serverlessV2MonthlyCost := estimatedACUs * acuCostPerHour * 730

							savings := provisionedMonthlyCost - serverlessV2MonthlyCost

							if savings > 10 { // Only recommend if savings > $10/month
								recommendations = append(recommendations, providers.Recommendation{
									CategoryName: providers.RecommendationCategoryInfraUpgrade,
									RuleName:     "aws_rds_aurora_serverless_migration",
									Severity:     providers.RecommendationSeverityMedium,
									Savings:      savings,
									Data: map[string]any{
										"db_instance_id":             resource.Id,
										"engine":                     engineStr,
										"current_mode":               "provisioned",
										"recommended_mode":           "serverless-v2",
										"avg_cpu_utilization":        avgCPU,
										"avg_connections":            avgConnections,
										"estimated_acu_usage":        estimatedACUs,
										"provisioned_monthly_cost":   provisionedMonthlyCost,
										"serverless_v2_monthly_cost": serverlessV2MonthlyCost,
										"reason":                     "Aurora instance shows low utilization. Serverless v2 can automatically scale down to 0.5 ACU when idle.",
										"startDate":                  startDate.Format(time.RFC3339),
										"endDate":                    endDate.Format(time.RFC3339),
									},
									Action:              providers.RecommendationActionModify,
									ResourceServiceName: resource.ServiceName,
									ResourceId:          resource.Id,
									ResourceType:        resource.Type,
									ResourceRegion:      resource.Region,
								})
							}
						}
					}
				} else {
					// Already Serverless v2 - check scaling configuration
					if scalingConfig, ok := meta["ServerlessV2ScalingConfiguration"].(map[string]any); ok {
						minCapacity := 0.5
						maxCapacity := 128.0

						if v, ok := scalingConfig["MinCapacity"].(float64); ok {
							minCapacity = v
						}
						if v, ok := scalingConfig["MaxCapacity"].(float64); ok {
							maxCapacity = v
						}

						// Check if min capacity is too high
						if minCapacity > 1.0 {
							// Query actual ACU usage
							acuMetrics, errACU := a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
								ResourceIds: []string{resource.Id},
								ServiceName: resource.ServiceName,
								StartDate:   &startDate,
								EndDate:     &endDate,
								Region:      resource.Region,
								MetricNames: []string{"ServerlessDatabaseCapacity"},
								Step:        3600 * time.Second,
								Statistics:  []string{"Average"},
							})

							if errACU == nil && len(acuMetrics.Items) > 0 && len(acuMetrics.Items[0].Values) > 0 {
								avgACU := 0.0
								sum := 0.0
								for _, v := range acuMetrics.Items[0].Values {
									sum += v
								}
								avgACU = sum / float64(len(acuMetrics.Items[0].Values))

								// If average usage is significantly below min capacity, recommend lowering it
								if avgACU < minCapacity*0.5 {
									// Calculate potential savings
									acuCostPerHour := 0.12
									if strings.Contains(engineStr, "postgresql") {
										acuCostPerHour = 0.13
									}

									currentMinCost := minCapacity * acuCostPerHour * 730
									recommendedMinCapacity := 0.5
									if avgACU > 0.5 {
										recommendedMinCapacity = avgACU
									}
									recommendedMinCost := recommendedMinCapacity * acuCostPerHour * 730

									savings := currentMinCost - recommendedMinCost

									if savings > 5 {
										recommendations = append(recommendations, providers.Recommendation{
											CategoryName: providers.RecommendationCategoryConfiguration,
											RuleName:     "aws_rds_aurora_serverless_scaling_config",
											Severity:     providers.RecommendationSeverityLow,
											Savings:      savings,
											Data: map[string]any{
												"db_instance_id":           resource.Id,
												"engine":                   engineStr,
												"current_min_capacity":     minCapacity,
												"current_max_capacity":     maxCapacity,
												"avg_acu_usage":            avgACU,
												"recommended_min_capacity": recommendedMinCapacity,
												"reason":                   "Average ACU usage is significantly below minimum capacity setting.",
												"startDate":                startDate.Format(time.RFC3339),
												"endDate":                  endDate.Format(time.RFC3339),
											},
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
			}
		}

		// RDS Auto Minor Version Upgrade
		if meta["AutoMinorVersionUpgrade"] != nil && meta["AutoMinorVersionUpgrade"] == false {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_rds_auto_minor_upgrade",
				Severity:            providers.RecommendationSeverityHigh,
				Savings:             0,
				Data:                map[string]any{},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// RDS Extended Support Cost Alerting
		// Note: This is COMPLEMENTARY to AWS Cost Optimization Hub recommendations
		// - AWS COH: Provides general "upgrade" recommendations (already integrated)
		// - This check: Provides SPECIFIC Extended Support cost warnings with pricing
		if engine, ok := meta["Engine"].(string); ok {
			if engineVersion, ok := meta["EngineVersion"].(string); ok {
				if supportInfo := getRDSExtendedSupportInfo(engine, engineVersion); supportInfo != nil {
					// Calculate extended support cost
					vCPUs := 0.0
					if existingInsatnceDetailAttributes != nil {
						if vcpuRaw, ok := existingInsatnceDetailAttributes["vcpu"].(string); ok {
							vcpuStr := strings.Split(vcpuRaw, " ")[0]
							if vcpuFloat, err := strconv.ParseFloat(vcpuStr, 64); err == nil {
								vCPUs = vcpuFloat
							}
						}
					}

					now := time.Now()
					yearsSinceEOL := now.Sub(supportInfo.EOLDate).Hours() / (24 * 365)
					costPerVCPUHour := supportInfo.Year1Cost
					yearLabel := "Year 1"

					if yearsSinceEOL >= 2 {
						costPerVCPUHour = supportInfo.Year3PlusCost
						yearLabel = "Year 3+"
					} else if yearsSinceEOL >= 1 {
						costPerVCPUHour = supportInfo.Year2Cost
						yearLabel = "Year 2"
					}

					// Calculate monthly extended support cost
					monthlyCost := 0.0
					if vCPUs > 0 {
						monthlyCost = vCPUs * costPerVCPUHour * 24 * 30
					}

					var skipExtendedSupport bool
					severity := providers.RecommendationSeverityHigh
					if !supportInfo.InExtendedSupport {
						// Nearing EOL (within 6 months)
						if supportInfo.EOLDate.Sub(now) < 180*24*time.Hour {
							severity = providers.RecommendationSeverityMedium
						} else {
							skipExtendedSupport = true
						}
					}

					if !skipExtendedSupport {
						reasonText := ""
						if supportInfo.InExtendedSupport {
							reasonText = fmt.Sprintf("Instance is running %s %s which is in Extended Support (%s). Incurring $%.2f/month in Extended Support costs (%s at $%.3f per vCPU-hour)",
								engine, engineVersion, yearLabel, monthlyCost, yearLabel, costPerVCPUHour)
						} else {
							daysToEOL := int(supportInfo.EOLDate.Sub(now).Hours() / 24)
							reasonText = fmt.Sprintf("Instance is running %s %s which reaches EOL on %s (%d days). Upgrade before EOL to avoid Extended Support costs",
								engine, engineVersion, supportInfo.EOLDate.Format("2006-01-02"), daysToEOL)
						}

						recommendations = append(recommendations, providers.Recommendation{
							CategoryName: providers.RecommendationCategoryConfiguration,
							RuleName:     "aws_rds_extended_support",
							Severity:     severity,
							Savings:      -monthlyCost, // Negative savings = additional cost
							Data: map[string]any{
								"engine":               engine,
								"engine_version":       engineVersion,
								"eol_date":             supportInfo.EOLDate.Format("2006-01-02"),
								"in_extended_support":  supportInfo.InExtendedSupport,
								"vcpus":                vCPUs,
								"cost_per_vcpu_hour":   costPerVCPUHour,
								"monthly_support_cost": monthlyCost,
								"support_year":         yearLabel,
								"year_1_cost_per_vcpu": supportInfo.Year1Cost,
								"year_2_cost_per_vcpu": supportInfo.Year2Cost,
								"year_3_cost_per_vcpu": supportInfo.Year3PlusCost,
								"reason":               reasonText,
							},
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

		// Performance Insights
		if meta["PerformanceInsightsEnabled"] != nil && meta["PerformanceInsightsEnabled"] == false {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_rds_performance_insights",
				Severity:            providers.RecommendationSeverityHigh,
				Savings:             0,
				Data:                map[string]any{},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// RDS Automated Backups Enabled
		if meta["BackupRetentionPeriod"] != nil && meta["BackupRetentionPeriod"] == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_rds_backup_enabled",
				Severity:            providers.RecommendationSeverityHigh,
				Savings:             0,
				Data:                map[string]any{},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// RDS Encrypted With KMS Customer Master Keys
		if meta["StorageEncrypted"] != nil && meta["StorageEncrypted"] == false {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_rds_storage_encrypted",
				Severity:            providers.RecommendationSeverityHigh,
				Savings:             0,
				Data:                map[string]any{},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Underutilized RDS Instance
		// Overutilized AWS RDS Instances
		isUnderutilized := true
		isOverUtilized := false
		//check CPU metrices
		cpuMetrics, err := a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
			ResourceIds: []string{resource.Id},
			ServiceName: resource.ServiceName,
			StartDate:   &startDate,
			EndDate:     &endDate,
			Region:      resource.Region,
			MetricNames: []string{"CPUUtilization"},
			Step:        3600 * time.Second,
			Statistics:  []string{"Maximum"},
		})
		if err != nil {
			ctx.GetLogger().Error("Error getting metrics", "resourceId", resource.Id, "error", err)
			return nil, err
		}
		if len(cpuMetrics.Items) > 0 {
			for _, metric := range cpuMetrics.Items[0].Values {
				if metric > 20 {
					isUnderutilized = false
					break
				}
			}
			for _, metric := range cpuMetrics.Items[0].Values {
				if metric > 80 {
					isOverUtilized = true
					break
				}
			}
		}
		// check memory metrices
		freeableMemoryMetrices := providers.QueryMetricsResponse{}
		if isUnderutilized || isOverUtilized {
			freeableMemoryMetrices, err = a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
				ResourceIds: []string{resource.Id},
				ServiceName: resource.ServiceName,
				StartDate:   &startDate,
				EndDate:     &endDate,
				Region:      resource.Region,
				MetricNames: []string{"FreeableMemory"},
				Step:        3600 * time.Second,
				Statistics:  []string{"Minimum"},
			})
			if err != nil {
				ctx.GetLogger().Error("Error getting metrics", "resourceId", resource.Id, "error", err)
				return nil, err
			}
			if len(freeableMemoryMetrices.Items) > 0 {
				if isUnderutilized {
					maxFreeMemory := existingInsatnceDetailMemoryBytes * .8
					for _, metric := range freeableMemoryMetrices.Items[0].Values {
						if metric < maxFreeMemory {
							isUnderutilized = false
							break
						}
					}
				}
				if isOverUtilized {
					minFreeMemory := existingInsatnceDetailMemoryBytes * .2
					isOverUtilized = lo.Min(freeableMemoryMetrices.Items[0].Values) < minFreeMemory
				}
			}
		}

		memoryAttr, memoryOk := existingInsatnceDetailAttributes["memory"].(string)
		vcpuAttr, vcpuOk := existingInsatnceDetailAttributes["vcpu"].(string)
		dbEngineAttr, _ := existingInsatnceDetailAttributes["databaseEngine"].(string)
		deployOptAttr, _ := existingInsatnceDetailAttributes["deploymentOption"].(string)

		if isUnderutilized && memoryOk && vcpuOk {
			// reduce memory and cpu by 50pct
			memoryStr := strings.Split(memoryAttr, " ")[0]
			memory, err := strconv.Atoi(memoryStr)
			if err != nil {
				ctx.GetLogger().Warn("failed to get available rds instances", "error", err, "accountNumber", account.AccountNumber, "region", resource.Region)
			}
			memory = memory / 2
			cpuStr := strings.Split(vcpuAttr, " ")[0]
			cpu, err := strconv.Atoi(cpuStr)
			if err != nil {
				ctx.GetLogger().Warn("failed to get available rds instances", "error", err, "accountNumber", account.AccountNumber, "region", resource.Region)
			}
			cpu = cpu / 2

			newInstances, err := getAvailableRdsInstances(cfg, resource.Region, dbEngineAttr, fmt.Sprintf("%d GiB", memory), fmt.Sprint(cpu), "", deployOptAttr)
			if err != nil {
				ctx.GetLogger().Warn("failed to get available rds instances", "error", err, "accountNumber", account.AccountNumber, "region", resource.Region)
			}
			savings := 0.0
			if len(newInstances) > 0 {
				newInstanceCost, err := getPricingValue(newInstances[0])
				if err != nil {
					ctx.GetLogger().Warn("failed to get available rds instances", "error", err, "accountNumber", account.AccountNumber, "region", resource.Region)
				}
				savings = (currentInsatnceCost - newInstanceCost) * 24 * 30
			}

			if len(cpuMetrics.Items) == 0 || len(freeableMemoryMetrices.Items) == 0 {
				ctx.GetLogger().Info("Skipping recommendation for instance: missing CPU or memory metrics", "accountNumber", account.AccountNumber, "region", resource.Region, "instanceId", resource.Id)
				continue
			}
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryRightSizing,
				RuleName:     "aws_rds_underutilized",
				Severity:     providers.RecommendationSeverityHigh,
				Savings:      savings,
				Data: map[string]any{
					"cpu":                  cpuMetrics.Items[0],
					"memory":               freeableMemoryMetrices.Items[0],
					"startDate":            startDate.Format(time.RFC3339),
					"endDate":              endDate.Format(time.RFC3339),
					"recommendedInstances": newInstances,
					"recommendedMemoryGb":  memory,
					"recommendedVCpu":      cpu,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		if isOverUtilized && memoryOk && vcpuOk {
			// increase memory and cpu by 50pct
			memoryStr := strings.Split(memoryAttr, " ")[0]
			memory, err := strconv.Atoi(memoryStr)
			if err != nil {
				ctx.GetLogger().Warn("failed to get available rds instances", "error", err, "accountNumber", account.AccountNumber, "region", resource.Region)
			}
			memory = memory * 2
			cpuStr := strings.Split(vcpuAttr, " ")[0]
			cpu, err := strconv.Atoi(cpuStr)
			if err != nil {
				ctx.GetLogger().Warn("failed to get available rds instances", "error", err, "accountNumber", account.AccountNumber, "region", resource.Region)
			}
			cpu = cpu * 2

			newInstances, err := getAvailableRdsInstances(cfg, resource.Region, dbEngineAttr, fmt.Sprintf("%d GiB", memory), fmt.Sprint(cpu), "", deployOptAttr)
			if err != nil {
				ctx.GetLogger().Warn("failed to get available rds instances", "error", err, "accountNumber", account.AccountNumber, "region", resource.Region)
			}
			savings := -currentInsatnceCost * 24 * 30
			if len(newInstances) > 0 {
				newInstanceCost, err := getPricingValue(newInstances[0])
				if err != nil {
					ctx.GetLogger().Warn("failed to get available rds instances", "error", err, "accountNumber", account.AccountNumber, "region", resource.Region)
				}
				savings = (currentInsatnceCost - newInstanceCost) * 24 * 30
			}
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryRightSizing,
				RuleName:     "aws_rds_overutilized",
				Severity:     providers.RecommendationSeverityHigh,
				Savings:      savings * -1,
				Data: map[string]any{
					"cpu":                  cpuMetrics.Items[0],
					"memory":               freeableMemoryMetrices.Items[0],
					"startDate":            startDate.Format(time.RFC3339),
					"endDate":              endDate.Format(time.RFC3339),
					"recommendedInstances": newInstances,
					"recommendedMemoryGb":  memory,
					"recommendedVCpu":      cpu,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// RDS Free Storage Space
		if allocatedStorageVal, ok := meta["AllocatedStorage"].(float64); ok {
			allcatedStorage := allocatedStorageVal * 1024 * 1024 * 1024
			allcatedStorage10Pct := allcatedStorage * 0.1
			isStorageFull := true
			storageMetrics, err := a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
				ResourceIds: []string{resource.Id},
				ServiceName: resource.ServiceName,
				StartDate:   &yesterdayDate,
				EndDate:     &endDate,
				Region:      resource.Region,
				MetricNames: []string{"FreeStorageSpace"},
				Step:        3600 * time.Second,
				Statistics:  []string{"Maximum"},
			})
			if err != nil {
				ctx.GetLogger().Error("Error getting metrics", "resourceId", resource.Id, "error", err)
				return nil, err
			}
			minValue := float64(math.MaxInt)
			if len(storageMetrics.Items) > 0 {
				for _, metric := range storageMetrics.Items[0].Values {
					if metric > allcatedStorage10Pct {
						isStorageFull = false
						break
					}
					if metric < minValue {
						minValue = metric
					}
				}
			}
			if isStorageFull && len(storageMetrics.Items) > 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryRightSizing,
					RuleName:     "aws_rds_free_storage_space",
					Severity:     providers.RecommendationSeverityHigh,
					// recommend increasing 10pct of storage and calculate cost based on that
					Savings: -1 * allcatedStorage10Pct * 0.08 / (1024 * 1024 * 1024),
					Data: map[string]any{
						"allcatedStorage":      allcatedStorage,
						"allcatedStorage10Pct": allcatedStorage10Pct,
						"increaseStorage":      allcatedStorage10Pct,
						"minValue":             minValue,
						"storageMetrics":       storageMetrics.Items[0],
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// rds instance without tags
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

		// RDS Instance Not In Public Subnet
		rdsAz, _ := resource.Meta["AvailabilityZone"].(string)
		if dbSubnetGroup, ok := resource.Meta["DBSubnetGroup"].(map[string]any); ok && dbSubnetGroup != nil {
			if subnetGroupSubnets, ok := dbSubnetGroup["Subnets"].([]any); ok {
				for _, networkInterfaceAny := range subnetGroupSubnets {
					networkInterface, ok := networkInterfaceAny.(map[string]any)
					if !ok {
						continue
					}
					subnetId, ok := networkInterface["SubnetIdentifier"].(string)
					if !ok {
						continue
					}
					subnetAz, _ := networkInterface["SubnetAvailabilityZone"].(map[string]any)
					azName, _ := subnetAz["Name"].(string)
					if azName == rdsAz && isPublicSubnet(context.TODO(), cfg, subnetId) {
						recommendation := providers.Recommendation{
							CategoryName: providers.RecommendationCategorySecurity,
							RuleName:     "aws_rds_instance_public_subnet",
							Severity:     providers.RecommendationSeverityMedium,
							Savings:      0,
							Data: map[string]any{
								"instance_id":     resource.Id,
								"instance_arn":    resource.Arn,
								"instance_region": resource.Region,
								"subnet_id":       subnetId,
							},
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

		// DB Instance Generation
		if instanceTypeDetails, ok := resource.Meta["InstanceTypeDetails"].(map[string]any); ok && instanceTypeDetails != nil {
			productDetails, ok1 := instanceTypeDetails["product"].(map[string]any)
			if !ok1 {
				continue
			}
			attributes, ok2 := productDetails["attributes"].(map[string]any)
			if ok2 && attributes["currentGeneration"] != "Yes" {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategoryInfraUpgrade,
					RuleName:     "aws_rds_instance_generation",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"instance_id":     resource.Id,
						"instance_arn":    resource.Arn,
						"instance_region": resource.Region,
						"instance_type":   resource.Meta["DBInstanceClass"],
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

		// Graviton Migration Recommendation
		if dbInstanceClass, ok := meta["DBInstanceClass"].(string); ok {
			gravitonClass := getGravitonInstanceFamily(dbInstanceClass)
			if gravitonClass != "" {
				// Try to get pricing for Graviton instance
				engine := ""
				if engineStr, ok := meta["Engine"].(string); ok {
					engine = engineStr
				}

				deploymentOption := "Single-AZ"
				if multiAZ, ok := meta["MultiAZ"].(bool); ok && multiAZ {
					deploymentOption = "Multi-AZ"
				}

				gravitonInstances, err := getAvailableRdsInstances(cfg, resource.Region, engine, "", "", gravitonClass, deploymentOption)
				if err != nil {
					ctx.GetLogger().Warn("failed to get Graviton instance pricing", "error", err, "gravitonClass", gravitonClass, "region", resource.Region)
				} else if len(gravitonInstances) > 0 {
					// Calculate savings (Graviton typically 10-20% cheaper)
					savings := 0.0
					gravitonPrice := 0.0
					if gravitonPriceVal, err := getPricingValue(gravitonInstances[0]); err == nil {
						gravitonPrice = gravitonPriceVal
						if currentInsatnceCost > 0 {
							// Monthly savings
							savings = (currentInsatnceCost - gravitonPrice) * 24 * 30
						}
					}

					if savings > 0 {
						savingsPercent := ((currentInsatnceCost - gravitonPrice) / currentInsatnceCost) * 100
						recommendations = append(recommendations, providers.Recommendation{
							CategoryName: providers.RecommendationCategoryInfraUpgrade,
							RuleName:     "aws_rds_graviton_migration",
							Severity:     providers.RecommendationSeverityMedium,
							Savings:      savings,
							Data: map[string]any{
								"current_instance_class":  dbInstanceClass,
								"graviton_instance_class": gravitonClass,
								"current_hourly_cost":     currentInsatnceCost,
								"graviton_hourly_cost":    gravitonPrice,
								"monthly_savings":         savings,
								"savings_percent":         savingsPercent,
								"reason":                  fmt.Sprintf("Instance can be migrated from %s to Graviton-based %s for %.1f%% cost savings ($%.2f/month)", dbInstanceClass, gravitonClass, savingsPercent, savings),
							},
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

		// Enable RDS Snapshot Encryption
		// Use AWS Backup Service in Use for Amazon RDS
		svc := rds.NewFromConfig(cfg)
		snapshots, err := svc.DescribeDBSnapshots(context.TODO(), &rds.DescribeDBSnapshotsInput{
			DBInstanceIdentifier: aws.String(resource.Id),
		})
		if err != nil {
			ctx.GetLogger().Error("failed to fetch rds snapshots", "error", err, "accountNumber", account.AccountNumber, "region", resource.Region)
		}
		rdsBackUpServiceEnabled := false
		for _, snapshot := range snapshots.DBSnapshots {
			if *snapshot.SnapshotType == "awsbackup" {
				rdsBackUpServiceEnabled = true
			}

			if !*snapshot.Encrypted {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "aws_rds_snapshot_encryption",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"instance_id":     resource.Id,
						"instance_arn":    resource.Arn,
						"instance_region": resource.Region,
						"snapshot_id":     *snapshot.DBSnapshotIdentifier,
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
		if !rdsBackUpServiceEnabled {
			recommendation := providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "aws_rds_backupservice_enabled",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"instance_id":     resource.Id,
					"instance_arn":    resource.Arn,
					"instance_region": resource.Region,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
			recommendations = append(recommendations, recommendation)
		}

		// RDS Copy Tags to Snapshots
		if meta["CopyTagsToSnapshot"] != true {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_rds_copy_tags_to_snapshots",
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

		//RDS alternate instance options
		regionCodeAttr, regionCodeOk := existingInsatnceDetailAttributes["regionCode"].(string)
		if regionCodeOk && dbEngineAttr != "" && memoryOk && vcpuOk {
			availableInsatncesWithSimilarConfigs, err := getAvailableRdsInstances(cfg, regionCodeAttr, dbEngineAttr, memoryAttr, vcpuAttr, "", deployOptAttr)
			if err != nil {
				ctx.GetLogger().Warn("failed to get available rds instances", "error", err, "accountNumber", account.AccountNumber, "region", resource.Region)
			} else {
				alternateInsatnces, err := alternateInstancesBasedOnPricing(availableInsatncesWithSimilarConfigs, existingInsatnceDetail)
				if err != nil {
					ctx.GetLogger().Warn("failed to get available rds instances", "error", err, "accountNumber", account.AccountNumber, "region", resource.Region)
				}
				if len(alternateInsatnces) > 0 {
					//calculate savings betweem lowest and current instance
					alternateInsatnceCost, err := getPricingValue(alternateInsatnces[0])
					if err != nil {
						ctx.GetLogger().Warn("failed to get available rds instances", "error", err, "accountNumber", account.AccountNumber, "region", resource.Region)
					}
					savings := (currentInsatnceCost - alternateInsatnceCost) * 24 * 30
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategoryRightSizing,
						RuleName:     "aws_rds_alternate_instances",
						Severity:     providers.RecommendationSeverityMedium,
						Savings:      savings,
						Data: map[string]any{
							"alternate_instances": alternateInsatnces,
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}
		}

		// RDS Reserved DB Instance Lease Expiration In The Next 30 Days
		// RDS Reserved DB Instance Lease Expiration In The Next 7 Days
		// Enable AWS RDS Transport Encryption

		// Check for missing CloudWatch alarms
		rdsAlarmTemplates, err := LoadAlarmTemplates("rds")
		if err != nil {
			ctx.GetLogger().Warn("Failed to load RDS alarm templates", "error", err, "resourceId", resource.Id)
		} else {
			for _, template := range rdsAlarmTemplates {
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

				// Calculate threshold based on instance class
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
							Name:  "DBInstanceIdentifier",
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
						"db_instance_id":     resource.Id,
						"db_instance_arn":    resource.Arn,
						"db_instance_class":  resource.Meta["DBInstanceClass"],
						"db_instance_region": resource.Region,
						"db_instance_engine": resource.Meta["Engine"],
						"metric_name":        template.Configuration.MetricName,
						"threshold":          threshold,
						"alarm_config":       alarmConfig,
						"alarm_type":         template.AlarmType,
						"reason":             template.Description,
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

func (a *amazonRds) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return "", err
	}
	regionalCfg := cfg.Copy()
	regionalCfg.Region = region
	logsSvc := cloudwatchlogs.NewFromConfig(regionalCfg)
	// Log types for different RDS engines:
	// MySQL/MariaDB: error, general, slowquery, audit
	// PostgreSQL: postgresql, upgrade
	// Oracle: alert, audit, listener, trace
	// SQL Server: agent, error
	logTypes := []string{"error", "general", "slowquery", "audit", "postgresql", "upgrade"}
	for _, logType := range logTypes {
		logGroupName := fmt.Sprintf("/aws/rds/instance/%s/%s", resourceId, logType)
		_, err := logsSvc.DescribeLogStreams(context.TODO(), &cloudwatchlogs.DescribeLogStreamsInput{
			LogGroupName: &logGroupName,
			Limit:        aws.Int32(1),
		})
		if err == nil {
			return logGroupName, nil
		}
	}

	return "", err
}

func (a *amazonRds) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	// Use DescribeResource to get all instance details in one call
	// This avoids duplicating the DescribeDBInstances SDK call
	metadata, err := a.DescribeResource(ctx, account, region, resourceId)
	if err != nil {
		ctx.GetLogger().Error("failed to describe RDS instance", "error", err, "resourceId", resourceId)
		return providers.ServiceMapApplication{}, err
	}

	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      metadata.ResourceARN,
			Kind:      "rds",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      metadata.Status,
	}

	// Add configured relationships from metadata
	// Security Groups
	for _, sg := range metadata.SecurityGroups {
		app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
			Id: providers.ServiceApplicationId{
				Name:      sg,
				Kind:      "ec2",
				Namespace: region,
			},
		}.ToDownstreamLink())
	}

	// Subnets/VPC (use first subnet's VPC for now)
	if len(metadata.Subnets) > 0 && metadata.VpcID != "" {
		// Add VPC as downstream dependency
		app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
			Id: providers.ServiceApplicationId{
				Name:      metadata.VpcID,
				Kind:      "vpc",
				Namespace: region,
			},
		}.ToDownstreamLink())
	}

	// KMS key (from metadata if available)
	if kmsKeyId, ok := metadata.Metadata["kmsKeyId"].(string); ok && kmsKeyId != "" {
		app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
			Id: providers.ServiceApplicationId{
				Name:      kmsKeyId,
				Kind:      "kms",
				Namespace: region,
			},
		}.ToDownstreamLink())
	}

	return app, nil
}

func (a *amazonRds) DescribeResource(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (*ResourceMetadata, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		return nil, fmt.Errorf("failed to create aws session: %w", err)
	}

	rdsSvc := rds.NewFromConfig(cfg)

	// Extract DB identifier from ARN if needed
	dbIdentifier := resourceId
	if strings.HasPrefix(resourceId, "arn:aws:rds:") {
		parts := strings.Split(resourceId, ":")
		if len(parts) >= 7 {
			dbIdentifier = parts[6] // For arn:aws:rds:region:account:db:instance-id
		}
	}

	dbOutput, err := rdsSvc.DescribeDBInstances(context.TODO(), &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(dbIdentifier),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe RDS instance %s: %w", dbIdentifier, err)
	}

	if len(dbOutput.DBInstances) == 0 {
		return nil, fmt.Errorf("RDS instance %s not found", dbIdentifier)
	}

	db := dbOutput.DBInstances[0]

	metadata := &ResourceMetadata{
		ResourceID:     dbIdentifier,
		SecurityGroups: []string{},
		Subnets:        []string{},
		Tags:           make(map[string]string),
		Metadata:       make(map[string]any),
	}

	// ARN
	if db.DBInstanceArn != nil {
		metadata.ResourceARN = *db.DBInstanceArn
	}

	// Status
	if db.DBInstanceStatus != nil {
		metadata.Status = *db.DBInstanceStatus
	}

	// VPC ID and Subnets
	if db.DBSubnetGroup != nil {
		if db.DBSubnetGroup.VpcId != nil {
			metadata.VpcID = *db.DBSubnetGroup.VpcId
		}
		for _, subnet := range db.DBSubnetGroup.Subnets {
			if subnet.SubnetIdentifier != nil {
				metadata.Subnets = append(metadata.Subnets, *subnet.SubnetIdentifier)
			}
		}
	}

	// Endpoint (hostname and port)
	if db.Endpoint != nil {
		if db.Endpoint.Port != nil {
			metadata.Port = int(*db.Endpoint.Port)
		}
		// Store hostname in metadata for DNS resolution
		if db.Endpoint.Address != nil {
			metadata.Metadata["endpoint"] = *db.Endpoint.Address

			// Try to resolve to IP (best effort)
			ip, err := ResolveRDSEndpointToIP(ctx, *db.Endpoint.Address)
			if err == nil {
				metadata.PrivateIP = ip
			} else {
				// If DNS resolution fails, store hostname for later resolution
				ctx.GetLogger().Debug("DNS resolution failed, storing hostname",
					"hostname", *db.Endpoint.Address,
					"error", err)
			}
		}
	}

	// Security Groups
	for _, sg := range db.VpcSecurityGroups {
		if sg.VpcSecurityGroupId != nil {
			metadata.SecurityGroups = append(metadata.SecurityGroups, *sg.VpcSecurityGroupId)
		}
	}

	// Tags
	for _, tag := range db.TagList {
		if tag.Key != nil && tag.Value != nil {
			metadata.Tags[*tag.Key] = *tag.Value
		}
	}

	// Additional RDS-specific metadata
	if db.Engine != nil {
		metadata.Metadata["engine"] = *db.Engine
	}
	if db.EngineVersion != nil {
		metadata.Metadata["engineVersion"] = *db.EngineVersion
	}
	if db.DBInstanceClass != nil {
		metadata.Metadata["instanceClass"] = *db.DBInstanceClass
	}

	ctx.GetLogger().Debug("described RDS resource",
		"resourceId", dbIdentifier,
		"vpcId", metadata.VpcID,
		"privateIP", metadata.PrivateIP,
		"port", metadata.Port)

	return metadata, nil
}
