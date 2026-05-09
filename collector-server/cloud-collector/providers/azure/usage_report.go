package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/costmanagement/armcostmanagement"
)

func getAzureUsageReport(ctx providers.CloudProviderContext, account providers.Account, month time.Month, year int) (providers.GetUsageReportResponse, error) {
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.GetUsageReportResponse{}, fmt.Errorf("failed to create credential: %w", err)
	}

	client, err := armcostmanagement.NewQueryClient(cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.GetUsageReportResponse{}, fmt.Errorf("failed to create costmanagement client: %w", err)
	}

	timeframe := armcostmanagement.QueryTimePeriod{
		From: to.Ptr(time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)),
		To:   to.Ptr(time.Date(year, month+1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Second)),
	}

	queryDef := armcostmanagement.QueryDefinition{
		Type:       to.Ptr(armcostmanagement.ExportTypeActualCost),
		Timeframe:  to.Ptr(armcostmanagement.TimeframeTypeCustom),
		TimePeriod: &timeframe,
		Dataset: &armcostmanagement.QueryDataset{
			Granularity: to.Ptr(armcostmanagement.GranularityTypeDaily),
			Aggregation: map[string]*armcostmanagement.QueryAggregation{
				"PreTaxCost": {
					Name:     to.Ptr("PreTaxCost"),
					Function: to.Ptr(armcostmanagement.FunctionTypeSum),
				},
			},
			Grouping: []*armcostmanagement.QueryGrouping{
				{
					Name: to.Ptr("ResourceId"),
					Type: to.Ptr(armcostmanagement.QueryColumnTypeDimension),
				},
				{
					Name: to.Ptr("ResourceType"),
					Type: to.Ptr(armcostmanagement.QueryColumnTypeDimension),
				},
				{
					Name: to.Ptr("ResourceLocation"),
					Type: to.Ptr(armcostmanagement.QueryColumnTypeDimension),
				},
				{
					Name: to.Ptr("ServiceName"),
					Type: to.Ptr(armcostmanagement.QueryColumnTypeDimension),
				},
				{
					Name: to.Ptr("MeterCategory"),
					Type: to.Ptr(armcostmanagement.QueryColumnTypeDimension),
				},
				{
					Name: to.Ptr("MeterSubcategory"),
					Type: to.Ptr(armcostmanagement.QueryColumnTypeDimension),
				},
				{
					Name: to.Ptr("ChargeType"),
					Type: to.Ptr(armcostmanagement.QueryColumnTypeDimension),
				},
				{
					Name: to.Ptr("PublisherType"),
					Type: to.Ptr(armcostmanagement.QueryColumnTypeDimension),
				},
				{
					Name: to.Ptr("PricingModel"),
					Type: to.Ptr(armcostmanagement.QueryColumnTypeDimension),
				},
			},
		},
	}

	var allItems []providers.UsageReportItem

	scope := fmt.Sprintf("/subscriptions/%s", session.SubscriptionID)

	var result armcostmanagement.QueryClientUsageResponse
	maxRetries := 3
	for attempt := 0; attempt <= maxRetries; attempt++ {
		result, err = client.Usage(ctx.GetContext(), scope, queryDef, nil)
		if err == nil {
			break
		}
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == 429 && attempt < maxRetries {
			backoff := time.Duration(30<<uint(attempt)) * time.Second // 30s, 60s, 120s
			ctx.GetLogger().Warn("azure: cost management API rate limited, retrying",
				"attempt", attempt+1, "backoff", backoff, "subscription", session.SubscriptionID)
			time.Sleep(backoff)
			continue
		}
		break
	}
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == 429 {
			return providers.GetUsageReportResponse{}, fmt.Errorf("failed to get usage report from Azure Cost Management API: %w (rate limited after retries)", err)
		}
		return providers.GetUsageReportResponse{}, fmt.Errorf("failed to get usage report from Azure Cost Management API: %w. This may be due to missing permissions. Please ensure the service principal has the 'Cost Management Reader' role assigned at the subscription scope", err)
	}

	if result.Properties != nil && result.Properties.Rows != nil {
		for _, row := range result.Properties.Rows {
			item, err := convertToUsageReportItem(result.Properties.Columns, row)
			if err != nil {
				ctx.GetLogger().Warn("failed to convert row to usage report item", "error", err, "subscription", session.SubscriptionID)
				continue
			}
			allItems = append(allItems, item)
		}
	}

	return providers.GetUsageReportResponse{Items: allItems}, nil
}

func convertToUsageReportItem(header []*armcostmanagement.QueryColumn, row []any) (providers.UsageReportItem, error) {
	item := providers.UsageReportItem{}
	tags := map[string][]string{}

	for i, value := range row {
		valStr := fmt.Sprintf("%v", value)
		colName := *header[i].Name

		switch strings.ToLower(colName) {
		case "pretaxcost":
			cost, err := strconv.ParseFloat(valStr, 64)
			if err == nil {
				item.Cost = cost
			}
		case "currency":
			item.CostCurrency = valStr
		case "usagedate":
			if dateFloat, ok := value.(float64); ok {
				dateStr := strconv.FormatFloat(dateFloat, 'f', 0, 64)
				t, err := time.Parse("20060102", dateStr)
				if err == nil {
					item.StartDate = t
					item.EndDate = t
				}
			}
		case "resourceid":
			item.ResourceId = valStr
			item.ResourceArn = valStr
			parts := strings.Split(valStr, "/")
			if len(parts) > 0 {
				item.ResourceName = parts[len(parts)-1]
			}
		case "resourcetype":
			item.ProductCode = strings.ToLower(valStr)
			parts := strings.Split(valStr, "/")
			if len(parts) > 1 {
				item.ResourceType = parts[len(parts)-1]
			} else {
				item.ResourceType = strings.ToLower(valStr)
			}
		case "resourcelocation":
			item.ResourceRegionCode = normalizeAzureRegion(valStr)
		case "consumedservice", "servicename":
			item.ProductServiceCode = strings.ToLower(valStr)
		case "metercategory":
			item.CostCategory = providers.UsageReportCostCategory(valStr)
		case "metersubcategory":
			item.CostSubCategory = strings.ToLower(valStr)
		case "chargetype":
			item.ChargeType = strings.ToLower(valStr)
		case "publishertype":
			item.PublisherType = strings.ToLower(valStr)
		case "pricingmodel":
			item.PricingModel = strings.ToLower(valStr)
		default:
			if strings.HasPrefix(strings.ToLower(colName), "tags.") {
				tagName := strings.TrimPrefix(colName, "tags.")
				tags[tagName] = append(tags[tagName], valStr)
			}
		}
	}

	// Fallbacks for rows without a ResourceType (RI purchases, support plans, marketplace, etc.)
	if item.ProductCode == "" {
		item.ProductCode = item.ProductServiceCode
	}
	if item.ProductCode == "" {
		item.ProductCode = string(item.CostCategory)
	}
	if item.ResourceType == "" {
		item.ResourceType = string(item.CostCategory)
	}

	// Azure Cost Management API reports ResourceType at the parent service level
	// (e.g., "microsoft.sql/servers") even for child resources like databases or elastic pools.
	// The ResourceId ARM path contains the full hierarchy, so derive the correct leaf type
	// from it to match what resource discovery produces (see ListResources normalization).
	if item.ResourceId != "" {
		if leafType := extractLeafTypeFromArmResourceId(item.ResourceId); leafType != "" {
			item.ResourceType = leafType
		}
	}

	item.ResourceTags = tags
	return item, nil
}

// extractLeafTypeFromArmResourceId extracts the leaf resource type segment from an Azure ARM resource ID.
// ARM format: /subscriptions/{sub}/resourceGroups/{rg}/providers/{namespace}/{type}/{name}[/{type}/{name}...]
// For ".../providers/Microsoft.Sql/servers/myserver/databases/mydb", returns "databases".
// For ".../providers/Microsoft.Compute/virtualMachines/myvm", returns "virtualmachines".
func extractLeafTypeFromArmResourceId(armId string) string {
	lowerArmId := strings.ToLower(armId)
	providerIdx := strings.Index(lowerArmId, "/providers/")
	if providerIdx == -1 {
		return ""
	}
	afterProvider := lowerArmId[providerIdx+len("/providers/"):]
	parts := strings.Split(afterProvider, "/")
	// Minimum valid: namespace/type/name (3 parts), always odd count
	if len(parts) < 3 || len(parts)%2 == 0 {
		return ""
	}
	// Leaf type is at second-to-last position (last element is the resource name)
	return parts[len(parts)-2]
}
