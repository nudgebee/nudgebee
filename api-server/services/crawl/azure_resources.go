package crawl

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"strings"
	"time"
)

// AzureRetailPricing struct for unmarshalling Azure Retail pricing api JSON response
type AzureRetailPricing struct {
	BillingCurrency    string                         `json:"BillingCurrency"`
	CustomerEntityId   string                         `json:"CustomerEntityId"`
	CustomerEntityType string                         `json:"CustomerEntityType"`
	Items              []AzureRetailPricingAttributes `json:"Items"`
	NextPageLink       string                         `json:"NextPageLink"`
	Count              int                            `json:"Count"`
}

// AzureRetailPricingAttributes struct for unmarshalling Azure Retail pricing api JSON response
type AzureRetailPricingAttributes struct {
	CurrencyCode         string     `json:"currencyCode"`
	TierMinimumUnits     float32    `json:"tierMinimumUnits"`
	RetailPrice          float32    `json:"retailPrice"`
	UnitPrice            float32    `json:"unitPrice"`
	ArmRegionName        string     `json:"armRegionName"`
	Location             string     `json:"location"`
	EffectiveStartDate   *time.Time `json:"effectiveStartDate"`
	EffectiveEndDate     *time.Time `json:"effectiveEndDate"`
	MeterId              string     `json:"meterId"`
	MeterName            string     `json:"meterName"`
	ProductId            string     `json:"productId"`
	SkuId                string     `json:"skuId"`
	ProductName          string     `json:"productName"`
	SkuName              string     `json:"skuName"`
	ServiceName          string     `json:"serviceName"`
	ServiceId            string     `json:"serviceId"`
	ServiceFamily        string     `json:"serviceFamily"`
	UnitOfMeasure        string     `json:"unitOfMeasure"`
	Type                 string     `json:"type"`
	IsPrimaryMeterRegion bool       `json:"isPrimaryMeterRegion"`
	ArmSkuName           string     `json:"armSkuName"`
}

type Item struct {
	CurrencyCode         string  `json:"currencyCode"`
	TierMinimumUnits     float32 `json:"tierMinimumUnits"`
	RetailPrice          float64 `json:"retailPrice"`
	UnitPrice            float64 `json:"unitPrice"`
	ArmRegionName        string  `json:"armRegionName"`
	Location             string  `json:"location"`
	EffectiveStartDate   string  `json:"effectiveStartDate"`
	EffectiveEndDate     string  `json:"effectiveEndDate"`
	MeterId              string  `json:"meterId"`
	MeterName            string  `json:"meterName"`
	ProductId            string  `json:"productId"`
	SkuId                string  `json:"skuId"`
	ProductName          string  `json:"productName"`
	SkuName              string  `json:"skuName"`
	ServiceName          string  `json:"serviceName"`
	ServiceId            string  `json:"serviceId"`
	ServiceFamily        string  `json:"serviceFamily"`
	UnitOfMeasure        string  `json:"unitOfMeasure"`
	Type                 string  `json:"type"`
	IsPrimaryMeterRegion bool    `json:"isPrimaryMeterRegion"`
	ArmSkuName           string  `json:"armSkuName"`
}

type Response struct {
	BillingCurrency    string `json:"BillingCurrency"`
	CustomerEntityId   string `json:"CustomerEntityId"`
	CustomerEntityType string `json:"CustomerEntityType"`
	Items              []Item `json:"Items"`
	NextPageLink       string `json:"NextPageLink"`
}

func extractResource(azurePricingData AzureRetailPricing, spot bool) ([]*InstanceInfo, error) {
	infoList := make([]*InstanceInfo, 0)
	for _, item := range azurePricingData.Items {
		if item.Type == "DevTestConsumption" {
			continue
		}
		if strings.Contains(item.MeterName, "Low Priority") {
			continue
		}
		if strings.Contains(item.ProductName, "Cloud Services") {
			continue
		}
		if item.Type == "Consumption" && !strings.Contains(item.ProductName, "Windows") && spot == strings.Contains(strings.ToLower(item.SkuName), " spot") {
			pricingModel := "on_demand"
			if strings.Contains(strings.ToLower(item.SkuName), " spot") {
				pricingModel = "spot"
			}
			infoList = append(infoList, &InstanceInfo{
				CloudProvider:     "azure",
				ServiceName:       item.ServiceName,
				ServiceType:       item.ServiceFamily,
				ResourceType:      item.ArmSkuName,
				ResourceRegion:    item.ArmRegionName,
				ResourceCost:      float64(item.RetailPrice),
				ResourceCapacity:  safeJsonDump(map[string]any{"memory_gb": 0, "cpu_virtual": 0}),
				Attributes:        safeJsonDump(map[string]any{"meterId": item.MeterId, "meterName": item.MeterName, "productId": item.ProductId, "skuId": item.SkuId, "productName": item.ProductName, "unitOfMeasure": item.UnitOfMeasure, "type": item.Type, "isPrimaryMeterRegion": item.IsPrimaryMeterRegion, "skuName": item.SkuName}),
				OperatingSystem:   "Linux",
				Architecture:      "x86_64",
				CurrentGeneration: true,
				PriceUnit:         "hourly",
				PricingModel:      pricingModel,
			})
		}
	}
	return infoList, nil
}

func getRetailPrice(region string, serviceName string, currencyCode string, skuNames []string) (AzureRetailPricing, error) {
	pricingURL := "https://prices.azure.com/api/retail/prices?$skip=0"

	if currencyCode != "" {
		pricingURL += fmt.Sprintf("&currencyCode='%s'", currencyCode)
	}

	var filterParams []string

	if serviceName != "" {
		serviceNameParam := fmt.Sprintf("serviceName eq '%s'", serviceName)
		filterParams = append(filterParams, serviceNameParam)
	}

	if region != "" {
		regionParam := fmt.Sprintf("armRegionName eq '%s'", region)
		filterParams = append(filterParams, regionParam)
	}

	if len(skuNames) > 0 {
		if len(skuNames) == 1 {
			skuNameParam := fmt.Sprintf("armSkuName eq '%s'", skuNames[0])
			filterParams = append(filterParams, skuNameParam)
		} else {
			skuNameParam := fmt.Sprintf("armSkuName in ('%s')", strings.Join(skuNames, "','"))
			filterParams = append(filterParams, skuNameParam)
		}
	}

	if len(filterParams) > 0 {
		filterParamsEscaped := url.QueryEscape(strings.Join(filterParams[:], " and "))
		pricingURL += fmt.Sprintf("&$filter=%s", filterParamsEscaped)
	}

	return fetchAllPages(pricingURL)
}

// fetchAllPages fetches the initial URL and follows NextPageLink until all pages are consumed.
func fetchAllPages(initialURL string) (AzureRetailPricing, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	var allItems []AzureRetailPricingAttributes
	var result AzureRetailPricing
	currentURL := initialURL

	for currentURL != "" {
		log.Printf("starting download retail price payload from \"%s\"", currentURL)
		resp, err := client.Get(currentURL)
		if err != nil {
			return AzureRetailPricing{}, fmt.Errorf("bogus fetch of \"%s\": %v", currentURL, err)
		}

		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			_ = resp.Body.Close()
			return AzureRetailPricing{}, fmt.Errorf("retail price responded with error status code %d", resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return AzureRetailPricing{}, fmt.Errorf("crawl: error getting response: %v", err)
		}

		page := AzureRetailPricing{}
		jsonErr := common.UnmarshalJson(body, &page)
		if jsonErr != nil {
			return AzureRetailPricing{}, fmt.Errorf("crawl: error unmarshalling data: %v", jsonErr)
		}

		allItems = append(allItems, page.Items...)

		// Preserve metadata from first page
		if result.BillingCurrency == "" {
			result.BillingCurrency = page.BillingCurrency
			result.CustomerEntityId = page.CustomerEntityId
			result.CustomerEntityType = page.CustomerEntityType
		}

		currentURL = page.NextPageLink
	}

	result.Items = allItems
	result.Count = len(allItems)
	return result, nil
}

func get_azure_pricing_data(region string, serviceName string, instanceFlavors []string) ([]*InstanceInfo, error) {
	pricingData, err := getRetailPrice(region, serviceName, "USD", instanceFlavors)
	if err != nil {
		return nil, fmt.Errorf("failed to get retail price: %w", err)
	}
	infoList := make([]*InstanceInfo, 0)

	// Extract on-demand pricing
	list, err := extractResource(pricingData, false)
	if err != nil {
		return nil, fmt.Errorf("failed to extract resource: %w", err)
	}
	infoList = append(infoList, list...)

	// Extract spot pricing
	spotList, err := extractResource(pricingData, true)
	if err != nil {
		return nil, fmt.Errorf("failed to extract spot resource: %w", err)
	}
	infoList = append(infoList, spotList...)

	return infoList, nil
}

func AzureResourceMeta(ctx *security.RequestContext) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	// Broaden detection: match AKS provider OR Standard_* node_flavor pattern
	regionRows, err := dbms.Db.Queryx(`
		SELECT ksn.node_flavor, ksn.node_region, ksn.memory_capacity, ksn.cpu_capacity
		FROM k8s_nodes ksn
		WHERE ksn.is_active IS NOT FALSE
		  AND (
			ksn.node_flavor LIKE 'Standard_%'
			OR EXISTS (SELECT 1 FROM agent a WHERE a.cloud_account_id = ksn.cloud_account_id AND a.k8s_provider = 'AKS')
		  )
		GROUP BY ksn.node_region, ksn.node_flavor, ksn.memory_capacity, ksn.cpu_capacity
	`)
	if err != nil {
		return err
	}

	type nodeInfo struct {
		flavor    string
		region    string
		memoryCap int64
		cpuCap    int64
	}

	nodes := make([]nodeInfo, 0)
	regionSet := make(map[string]bool)
	for regionRows.Next() {
		region := map[string]any{}
		err = regionRows.MapScan(region)
		if err != nil {
			return err
		}
		ni := nodeInfo{
			region: region["node_region"].(string),
		}
		if f, ok := region["node_flavor"].(string); ok {
			ni.flavor = f
		}
		if m, ok := region["memory_capacity"].(int64); ok {
			ni.memoryCap = m
		}
		if c, ok := region["cpu_capacity"].(int64); ok {
			ni.cpuCap = c
		}
		nodes = append(nodes, ni)
		regionSet[ni.region] = true
	}

	if len(regionSet) == 0 {
		return nil
	}

	// Build a map of known node flavor -> capacity from k8s_nodes
	type capacityInfo struct {
		memoryGB int64
		cpu      int64
	}
	flavorCapacity := make(map[string]capacityInfo)
	for _, n := range nodes {
		if n.flavor != "" {
			if _, exists := flavorCapacity[n.flavor]; !exists {
				flavorCapacity[n.flavor] = capacityInfo{
					memoryGB: n.memoryCap / 1024,
					cpu:      n.cpuCap,
				}
			}
		}
	}

	// Fetch pricing only for known VM flavors (not all SKUs in the region)
	skuNames := make([]string, 0, len(flavorCapacity))
	for flavor := range flavorCapacity {
		skuNames = append(skuNames, flavor)
	}
	instanceList := make([]*InstanceInfo, 0)
	for region := range regionSet {
		instances, err := get_azure_pricing_data(region, "Virtual Machines", skuNames)
		if err != nil {
			ctx.GetLogger().Error("Failed to get azure pricing data, skipping region", "region", region, "error", err)
			continue
		}

		// Populate capacity for known flavors
		for _, instance := range instances {
			if cap, ok := flavorCapacity[instance.ResourceType]; ok {
				instance.ResourceCapacity = safeJsonDump(map[string]any{"memory_gb": cap.memoryGB, "cpu_virtual": cap.cpu})
			}
		}

		instanceList = append(instanceList, instances...)
	}

	ctx.GetLogger().Info("Inserting Azure resource details into database", "instanceInfo", len(instanceList))
	for i := 0; i < len(instanceList); i += 50 {
		end := i + 50
		if end > len(instanceList) {
			end = len(instanceList)
		}
		err = insertInstanceInfo(dbms, instanceList[i:end])
		if err != nil {
			return err
		}
	}
	return nil
}
