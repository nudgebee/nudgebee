package crawl

import (
	"context"
	"fmt"
	"log"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	"github.com/aws/aws-sdk-go-v2/service/pricing/types"
	"github.com/samber/lo"
)

// Compiled once at package init instead of on every extractMemory/parseGpuCount call.
var digitsRegex = regexp.MustCompile(`(\d+)`)

type InstanceInfo struct {
	CloudProvider      string        `json:"cloud_provider"`
	ServiceName        string        `json:"service_name"`
	ServiceType        string        `json:"service_type"`
	ResourceType       string        `json:"resource_type"`
	ResourceRegion     string        `json:"resource_region"`
	ResourceCapacity   string        `json:"resource_capacity"`
	ResourceCost       float64       `json:"resource_cost"`
	Attributes         string        `json:"attributes"`
	SpotPricing        []AZPriceInfo `json:"spot_pricing"`
	Architecture       string        `json:"architecture"`
	GpuCount           int           `json:"gpu_count"`
	NetworkPerformance string        `json:"network_performance"`
	OperatingSystem    string        `json:"operating_system"`
	Tenancy            string        `json:"tenancy"`
	CurrentGeneration  bool          `json:"current_generation"`
	DatabaseEngine     string        `json:"database_engine"`
	DeploymentOption   string        `json:"deployment_option"`
	PriceUnit          string        `json:"price_unit"`
	PricingModel       string        `json:"pricing_model"`
}

type SpotPriceInfo struct {
	Region       string        `json:"region"`
	InstanceType string        `json:"instance_type"`
	AZPrices     []AZPriceInfo `json:"az_prices"`
}

type AZPriceInfo struct {
	AZ    string  `json:"az"`
	Price float64 `json:"price"`
}

func safeJsonDump(data any) string {
	bytes, _ := common.MarshalJson(data)
	return string(bytes)
}

func extractMemory(memoryStr string) int {
	memoryMatch := digitsRegex.FindStringSubmatch(memoryStr)
	if len(memoryMatch) > 1 {
		memory, err := strconv.Atoi(memoryMatch[1])
		if err == nil {
			return memory
		}
	}
	return 0
}

func parseGpuCount(gpuStr string) int {
	if gpuStr == "" {
		return 0
	}
	match := digitsRegex.FindStringSubmatch(gpuStr)
	if len(match) > 1 {
		count, err := strconv.Atoi(match[1])
		if err == nil {
			return count
		}
	}
	return 0
}

func currentPriceByService(ctx *security.RequestContext, serviceType string, region string, engine string, deploymentOption string) ([]*InstanceInfo, error) {
	var filters []types.Filter
	var serviceCode *string
	var instanceTypes []string

	dbEngine := ""
	deplOption := ""

	switch serviceType {
	case "Compute":
		serviceCode = aws.String("AmazonEC2")
		os := aws.String("Linux")
		preinstalledSoftware := aws.String("NA")
		tenancy := "Shared"
		byol := false
		filters = []types.Filter{
			{Type: types.FilterTypeTermMatch, Field: aws.String("termType"), Value: aws.String("OnDemand")},
			{Type: types.FilterTypeTermMatch, Field: aws.String("capacitystatus"), Value: aws.String(lo.Ternary(tenancy == "Host", "AllocatedHost", "Used"))},
			{Type: types.FilterTypeTermMatch, Field: aws.String("regionCode"), Value: aws.String(region)},
			{Type: types.FilterTypeTermMatch, Field: aws.String("tenancy"), Value: aws.String(tenancy)},
			{Type: types.FilterTypeTermMatch, Field: aws.String("operatingSystem"), Value: os},
			{Type: types.FilterTypeTermMatch, Field: aws.String("preInstalledSw"), Value: preinstalledSoftware},
			{Type: types.FilterTypeTermMatch, Field: aws.String("licenseModel"), Value: aws.String(lo.Ternary(byol, "Bring your own license", "No License required"))},
		}
	case "RDS":
		dbEngine = engine
		deplOption = deploymentOption
		filters = []types.Filter{
			{Type: types.FilterTypeTermMatch, Field: aws.String("termType"), Value: aws.String("OnDemand")},
			{Type: types.FilterTypeTermMatch, Field: aws.String("regionCode"), Value: aws.String(region)},
			{Type: types.FilterTypeTermMatch, Field: aws.String("databaseEngine"), Value: aws.String(engine)},
			{Type: types.FilterTypeTermMatch, Field: aws.String("deploymentOption"), Value: aws.String(deploymentOption)},
			{Type: types.FilterTypeTermMatch, Field: aws.String("licenseModel"), Value: aws.String("No license required")},
		}
		serviceCode = aws.String("AmazonRDS")
	default:
		return nil, fmt.Errorf("crawl: invalid service type")
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx.GetContext(), awsconfig.WithRegion("us-east-1"))
	if err != nil {
		return nil, err
	}
	pricingClient := pricing.NewFromConfig(cfg)

	var nextToken *string
	infoList := make([]*InstanceInfo, 0)

	for {
		filters2 := make([]types.Filter, 0)
		filters2 = append(filters2, filters...)
		response, err := pricingClient.GetProducts(ctx.GetContext(), &pricing.GetProductsInput{ServiceCode: serviceCode, Filters: filters2, NextToken: nextToken})
		if err != nil {
			ctx.GetLogger().Error("Error getting products from pricing api", "error", err)
			return nil, err
		}
		data, err := extractInfoList(ctx, response, serviceType, dbEngine, deplOption)
		if err != nil {
			ctx.GetLogger().Info("Error extracting info list", "error", err)
			return nil, err
		}
		infoList = append(infoList, data...)

		nextToken = response.NextToken
		if nextToken == nil {
			break
		}
	}
	if serviceType == "Compute" {
		instanceTypes = make([]string, 0)
		for _, info := range infoList {
			instanceTypes = append(instanceTypes, info.ResourceType)
		}
		spotPricing, err := getSpotInstancePricing(ctx.GetContext(), region, instanceTypes)
		if err != nil {
			ctx.GetLogger().Info("Error getting spot pricing", "error", err)
		} else {
			for _, info := range infoList {
				for _, spotPrice := range spotPricing {
					if spotPrice.InstanceType == info.ResourceType {
						info.SpotPricing = spotPrice.AZPrices
					}
				}
			}
		}
	}

	return infoList, nil
}

func extractInfoList(ctx *security.RequestContext, response *pricing.GetProductsOutput, serviceType string, dbEngine string, deplOption string) ([]*InstanceInfo, error) {
	infoList := make([]*InstanceInfo, 0)

	for _, price := range response.PriceList {
		priceData := map[string]any{}
		err := common.UnmarshalJson([]byte(price), &priceData)
		if err != nil {
			return nil, err
		}

		for _, onDemand := range priceData["terms"].(map[string]any)["OnDemand"].(map[string]any) {
			for _, priceDimensions := range onDemand.(map[string]any)["priceDimensions"].(map[string]any) {
				attrs := priceData["product"].(map[string]any)["attributes"].(map[string]any)
				if _, ok := attrs["vcpu"]; !ok {
					continue
				}
				if _, ok := attrs["memory"]; !ok {
					continue
				}

				vcpus := attrs["vcpu"].(string)
				memory := extractMemory(attrs["memory"].(string))
				resourceCostString := priceDimensions.(map[string]any)["pricePerUnit"].(map[string]any)["USD"]
				if resourceCostString == nil {
					ctx.GetLogger().Info("Price not found", "price", safeJsonDump(priceDimensions))
					continue
				}
				resourceCost, err := strconv.ParseFloat(resourceCostString.(string), 64)
				if err != nil {
					log.Printf("Error parsing price: %v", err)
					return nil, err
				}

				// Extract new fields from attributes
				architecture := ""
				if v, ok := attrs["processorArchitecture"].(string); ok {
					architecture = v
				}
				networkPerf := ""
				if v, ok := attrs["networkPerformance"].(string); ok {
					networkPerf = v
				}
				gpuCount := 0
				if v, ok := attrs["gpu"].(string); ok {
					gpuCount = parseGpuCount(v)
				}
				currentGen := true
				if v, ok := attrs["currentGeneration"].(string); ok {
					currentGen = v == "Yes"
				}
				osName := "Linux"
				if v, ok := attrs["operatingSystem"].(string); ok {
					osName = v
				}
				tenancy := "Shared"
				if v, ok := attrs["tenancy"].(string); ok {
					tenancy = v
				}

				instanceInfo := &InstanceInfo{
					CloudProvider:      "aws",
					ServiceName:        priceData["serviceCode"].(string),
					ServiceType:        serviceType,
					ResourceType:       attrs["instanceType"].(string),
					ResourceRegion:     attrs["regionCode"].(string),
					ResourceCapacity:   safeJsonDump(map[string]any{"memory_gb": memory, "cpu_virtual": vcpus}),
					ResourceCost:       resourceCost,
					Attributes:         safeJsonDump(attrs),
					Architecture:       architecture,
					GpuCount:           gpuCount,
					NetworkPerformance: networkPerf,
					OperatingSystem:    osName,
					Tenancy:            tenancy,
					CurrentGeneration:  currentGen,
					DatabaseEngine:     dbEngine,
					DeploymentOption:   deplOption,
					PriceUnit:          "hourly",
					PricingModel:       "on_demand",
				}

				infoList = append(infoList, instanceInfo)
			}
		}
	}

	return infoList, nil
}

func AWsResourceMeta(ctx *security.RequestContext) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	// Fetch regions from both EKS nodes and active cloud resources
	regionRows, err := dbms.Db.Query(`
		SELECT DISTINCT region FROM (
			SELECT DISTINCT ksn.meta -> 'node_info' -> 'labels' ->> 'topology.kubernetes.io/region' AS region
			FROM k8s_nodes ksn
			INNER JOIN agent a ON ksn.cloud_account_id = a.cloud_account_id AND a.k8s_provider = 'EKS'
			WHERE ksn.is_active IS NOT false
			UNION
			SELECT DISTINCT region
			FROM cloud_resourses
			WHERE service_name IN ('AmazonEC2', 'AmazonRDS') AND is_active = true AND region != ''
		) regions WHERE region IS NOT NULL AND region != ''
	`)

	if err != nil {
		return err
	}
	regions := make([]string, 0)
	for regionRows.Next() {
		var region string
		err = regionRows.Scan(&region)
		if err != nil {
			ctx.GetLogger().Error("Error scanning region", "error", err)
			return err
		}
		regions = append(regions, region)
	}

	instanceInfo := make([]*InstanceInfo, 0)
	for _, region := range regions {
		ctx.GetLogger().Info("scanning resources for region", "region", region)

		// EC2 Compute pricing
		ec2PricingData, err := currentPriceByService(ctx, "Compute", region, "", "")
		if err != nil {
			ctx.GetLogger().Error("Error scanning Ec2 Pricing", "region", region, "error", err)
			return err
		}
		if ec2PricingData != nil {
			instanceInfo = append(instanceInfo, ec2PricingData...)
		}

		// RDS pricing — multiple engines and deployment options
		rdsEngines := []string{"PostgreSQL", "MySQL", "MariaDB"}
		rdsDeployments := []string{"Single-AZ", "Multi-AZ"}
		for _, engine := range rdsEngines {
			for _, deployment := range rdsDeployments {
				rdsPricingData, err := currentPriceByService(ctx, "RDS", region, engine, deployment)
				if err != nil {
					ctx.GetLogger().Error("Error scanning RDS Pricing", "region", region, "engine", engine, "deployment", deployment, "error", err)
					continue // Don't fail entire crawl for one engine/deployment combo
				}
				if rdsPricingData != nil {
					instanceInfo = append(instanceInfo, rdsPricingData...)
				}
			}
		}
	}
	// Deduplicate by unique key to avoid "ON CONFLICT DO UPDATE cannot affect row a second time"
	seen := make(map[string]int, len(instanceInfo))
	deduped := make([]*InstanceInfo, 0, len(instanceInfo))
	for _, info := range instanceInfo {
		key := info.CloudProvider + "|" + info.ServiceName + "|" + info.ServiceType + "|" +
			info.ResourceType + "|" + info.ResourceRegion + "|" +
			info.PricingModel + "|" + info.DatabaseEngine + "|" + info.DeploymentOption
		if idx, exists := seen[key]; exists {
			deduped[idx] = info // replace with latest
		} else {
			seen[key] = len(deduped)
			deduped = append(deduped, info)
		}
	}
	if len(instanceInfo) != len(deduped) {
		ctx.GetLogger().Info("Deduplicated AWS pricing entries", "before", len(instanceInfo), "after", len(deduped))
	}
	instanceInfo = deduped

	ctx.GetLogger().Info("Inserting AWS resource details into database", "instanceInfo", len(instanceInfo))
	for i := 0; i < len(instanceInfo); i += 50 {
		end := i + 50
		if end > len(instanceInfo) {
			end = len(instanceInfo)
		}
		err = insertInstanceInfo(dbms, instanceInfo[i:end])
		if err != nil {
			ctx.GetLogger().Error("Error inserting instance info batch", "error", err, "batchStart", i, "batchEnd", end)
			continue
		}
	}
	return nil
}

func insertInstanceInfo(dbms *database.DatabaseManager, instanceInfo []*InstanceInfo) error {
	cols := []string{
		"cloud_provider", "service_name", "service_type", "resource_type", "resource_region",
		"resource_capacity", "resource_cost", "attributes", "spot_pricing",
		"architecture", "gpu_count", "network_performance", "operating_system", "tenancy",
		"current_generation", "database_engine", "deployment_option", "price_unit", "pricing_model",
	}
	onConflict := []string{
		"cloud_provider", "service_name", "service_type", "resource_type", "resource_region",
		"pricing_model", "database_engine", "deployment_option",
	}
	onConflictUpdate := []string{
		"resource_cost", "attributes", "spot_pricing", "resource_capacity",
		"architecture", "gpu_count", "network_performance", "operating_system", "tenancy",
		"current_generation", "price_unit",
	}

	values := make([][]any, 0, len(instanceInfo))
	for _, info := range instanceInfo {
		values = append(values, []any{
			info.CloudProvider, info.ServiceName, info.ServiceType, info.ResourceType, info.ResourceRegion,
			info.ResourceCapacity, info.ResourceCost, info.Attributes, safeJsonDump(info.SpotPricing),
			info.Architecture, info.GpuCount, info.NetworkPerformance, info.OperatingSystem, info.Tenancy,
			info.CurrentGeneration, info.DatabaseEngine, info.DeploymentOption, info.PriceUnit, info.PricingModel,
		})
	}

	_, err := dbms.Insert(nil, "cloud_resource_details", onConflict, onConflictUpdate, nil, cols, values...)
	return err
}

func JoinStrings(values []string, sep string) string {
	var b strings.Builder
	for i, v := range values {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(v)
	}
	return b.String()
}

func getSpotInstancePricing(ctx context.Context, resourceRegion string, instanceTypesStr []string) ([]SpotPriceInfo, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(resourceRegion))
	if err != nil {
		return nil, err
	}
	svc := ec2.NewFromConfig(cfg)
	region := cfg.Region

	instanceTypes := make([]ec2types.InstanceType, len(instanceTypesStr))
	for i, v := range instanceTypesStr {
		instanceTypes[i] = ec2types.InstanceType(v)
	}

	// Set the input parameters for the DescribeSpotPriceHistory API call
	input := &ec2.DescribeSpotPriceHistoryInput{
		InstanceTypes: instanceTypes,
		ProductDescriptions: []string{
			"Linux/UNIX",
		},
		StartTime: aws.Time(time.Now().Add(-24 * time.Hour)), // Last 24 hours
		EndTime:   aws.Time(time.Now()),
	}

	var allSpotPriceHistory []ec2types.SpotPrice
	var nextToken *string

	// Handle pagination
	for {
		// Call the DescribeSpotPriceHistory API
		result, err := svc.DescribeSpotPriceHistory(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to get spot price history: %w", err)
		}

		// Append the current page of spot prices to the total results
		allSpotPriceHistory = append(allSpotPriceHistory, result.SpotPriceHistory...)

		// Check if there is another page of results
		if result.NextToken == nil || *result.NextToken == "" {
			break
		}

		// Set the next token for the next iteration
		nextToken = result.NextToken
		input.NextToken = nextToken
	}

	// Map to hold the data organized by region and instance type
	spotPricesMap := make(map[string]map[string][]AZPriceInfo)

	// Process the spot price history into the desired format
	for _, history := range allSpotPriceHistory {
		price, err := strconv.ParseFloat(aws.ToString(history.SpotPrice), 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse spot price: %w", err)
		}

		instanceType := string(history.InstanceType)
		az := aws.ToString(history.AvailabilityZone)

		if _, ok := spotPricesMap[region]; !ok {
			spotPricesMap[region] = make(map[string][]AZPriceInfo)
		}
		spotPricesMap[region][instanceType] = append(spotPricesMap[region][instanceType], AZPriceInfo{
			AZ:    az,
			Price: price,
		})
	}

	// Convert map to slice
	var spotPrices []SpotPriceInfo
	for region, instanceTypes := range spotPricesMap {
		for instanceType, azPrices := range instanceTypes {
			sort.Slice(azPrices, func(i, j int) bool {
				return azPrices[i].Price < azPrices[j].Price
			})
			spotPrices = append(spotPrices, SpotPriceInfo{
				Region:       region,
				InstanceType: instanceType,
				AZPrices:     azPrices,
			})
		}
	}

	return spotPrices, nil
}
