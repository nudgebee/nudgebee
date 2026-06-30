package azure

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"nudgebee/collector/cloud/providers"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
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

	appendRows := func(props *armcostmanagement.QueryProperties) {
		if props == nil {
			return
		}
		for _, row := range props.Rows {
			item, convErr := convertToUsageReportItem(props.Columns, row)
			if convErr != nil {
				ctx.GetLogger().Warn("failed to convert row to usage report item", "error", convErr, "subscription", session.SubscriptionID)
				continue
			}
			allItems = append(allItems, item)
		}
	}

	appendRows(result.Properties)

	// The Cost Management Query API caps each response at 5000 rows and returns a
	// NextLink (with an embedded $skiptoken) when more rows exist. QueryClient.Usage
	// does not auto-paginate, so follow NextLink manually until it is empty —
	// otherwise large subscriptions are silently truncated at 5000 daily line items.
	nextLink := ""
	if result.Properties != nil && result.Properties.NextLink != nil {
		nextLink = *result.Properties.NextLink
	}
	if nextLink != "" {
		body, marshalErr := json.Marshal(queryDef)
		if marshalErr != nil {
			return providers.GetUsageReportResponse{}, fmt.Errorf("failed to marshal usage query for pagination: %w", marshalErr)
		}
		const maxUsagePages = 1000
		for page := 1; nextLink != ""; page++ {
			if page >= maxUsagePages {
				ctx.GetLogger().Error("azure: usage report exceeded max pages, results may be truncated",
					"subscription", session.SubscriptionID, "max_pages", maxUsagePages)
				break
			}
			props, pageErr := fetchAzureUsagePage(ctx, cred, nextLink, body, session.SubscriptionID)
			if pageErr != nil {
				// Return the rows gathered so far rather than failing the whole
				// report — partial (but already past the 5000 first-page cap) beats
				// none, and never regresses the pre-pagination behaviour.
				ctx.GetLogger().Error("azure: failed to fetch usage report page, returning partial results",
					"error", pageErr, "page", page+1, "subscription", session.SubscriptionID, "rows_so_far", len(allItems))
				break
			}
			appendRows(props)
			nextLink = ""
			if props != nil && props.NextLink != nil {
				nextLink = *props.NextLink
			}
		}
	}

	return providers.GetUsageReportResponse{Items: allItems}, nil
}

// fetchAzureUsagePage follows a Cost Management Query API NextLink to retrieve a
// further page of usage rows. The Query API is a POST endpoint and its NextLink
// carries the pagination cursor ($skiptoken) in the URL, so the original query
// body is re-sent to that URL, authenticated with the same service-principal
// credential. 429s are retried with the same backoff schedule as the first page
// so a throttled page does not silently drop rows.
func fetchAzureUsagePage(ctx providers.CloudProviderContext, cred azcore.TokenCredential, nextLink string, body []byte, subscriptionID string) (*armcostmanagement.QueryProperties, error) {
	maxRetries := 3
	for attempt := 0; attempt <= maxRetries; attempt++ {
		token, err := cred.GetToken(ctx.GetContext(), policy.TokenRequestOptions{
			Scopes: []string{"https://management.azure.com/.default"},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to acquire token: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx.GetContext(), http.MethodPost, nextLink, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token.Token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRetries {
			backoff := time.Duration(30<<uint(attempt)) * time.Second // 30s, 60s, 120s
			ctx.GetLogger().Warn("azure: cost management API rate limited on pagination, retrying",
				"attempt", attempt+1, "backoff", backoff, "subscription", subscriptionID)
			select {
			case <-time.After(backoff):
				continue
			case <-ctx.GetContext().Done():
				return nil, ctx.GetContext().Err()
			}
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
		}

		var result armcostmanagement.QueryResult
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal usage page: %w", err)
		}
		return result.Properties, nil
	}
	return nil, fmt.Errorf("cost management API rate limited after retries")
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
