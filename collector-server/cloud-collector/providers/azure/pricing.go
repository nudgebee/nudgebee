package azure

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"nudgebee/collector/cloud/providers"
	"strings"
	"sync"
	"time"
)

// PricingCache stores Azure pricing data with TTL
type PricingCache struct {
	mu          sync.RWMutex
	prices      map[string]PriceEntry
	lastUpdated time.Time
	ttl         time.Duration
	enabled     bool
}

// PriceEntry represents a cached price entry
type PriceEntry struct {
	ServiceName   string
	SKUName       string
	Region        string
	UnitPrice     float64
	CurrencyCode  string
	UnitOfMeasure string
	FetchedAt     time.Time
}

// RetailPricesResponse represents Azure Retail Prices API response
type RetailPricesResponse struct {
	Items        []RetailPriceItem `json:"Items"`
	NextPageLink string            `json:"NextPageLink"`
	Count        int               `json:"Count"`
}

// RetailPriceItem represents a single price item from Azure API
type RetailPriceItem struct {
	CurrencyCode         string  `json:"currencyCode"`
	TierMinimumUnits     float64 `json:"tierMinimumUnits"`
	RetailPrice          float64 `json:"retailPrice"`
	UnitPrice            float64 `json:"unitPrice"`
	ArmRegionName        string  `json:"armRegionName"`
	Location             string  `json:"location"`
	EffectiveStartDate   string  `json:"effectiveStartDate"`
	MeterID              string  `json:"meterId"`
	MeterName            string  `json:"meterName"`
	ProductID            string  `json:"productId"`
	SkuID                string  `json:"skuId"`
	ProductName          string  `json:"productName"`
	SkuName              string  `json:"skuName"`
	ServiceName          string  `json:"serviceName"`
	ServiceID            string  `json:"serviceId"`
	ServiceFamily        string  `json:"serviceFamily"`
	UnitOfMeasure        string  `json:"unitOfMeasure"`
	Type                 string  `json:"type"`
	IsPrimaryMeterRegion bool    `json:"isPrimaryMeterRegion"`
	ArmSkuName           string  `json:"armSkuName"`
}

var (
	globalPricingCache *PricingCache
	once               sync.Once
)

// GetPricingCache returns singleton pricing cache
func GetPricingCache() *PricingCache {
	once.Do(func() {
		globalPricingCache = &PricingCache{
			prices:  make(map[string]PriceEntry),
			ttl:     24 * time.Hour, // Refresh daily
			enabled: true,           // Enabled by default
		}
	})
	return globalPricingCache
}

// SetEnabled enables or disables dynamic pricing
func (pc *PricingCache) SetEnabled(enabled bool) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.enabled = enabled
}

// IsEnabled returns whether dynamic pricing is enabled
func (pc *PricingCache) IsEnabled() bool {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.enabled
}

// SetTTL sets the cache time-to-live duration
func (pc *PricingCache) SetTTL(ttl time.Duration) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.ttl = ttl
}

// GetVMPrice fetches VM pricing from Azure Retail Prices API
func (pc *PricingCache) GetVMPrice(ctx providers.CloudProviderContext, vmSize, region string) (float64, error) {
	if !pc.IsEnabled() {
		return 0, fmt.Errorf("dynamic pricing is disabled")
	}

	cacheKey := fmt.Sprintf("vm:%s:%s", vmSize, normalizeAzureRegion(region))

	// Check cache first
	if price, found := pc.getCachedPrice(cacheKey); found {
		return price, nil
	}

	// Normalize region for Azure API
	normalizedRegion := normalizeAzureRegion(region)

	// Build filter query
	filter := fmt.Sprintf("serviceName eq 'Virtual Machines' and armRegionName eq '%s' and armSkuName eq '%s' and priceType eq 'Consumption'",
		normalizedRegion, vmSize)

	// Fetch from Azure API
	items, err := pc.fetchPrices(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch VM pricing: %w", err)
	}

	if len(items) == 0 {
		return 0, fmt.Errorf("no pricing found for VM %s in region %s", vmSize, normalizedRegion)
	}

	// Get hourly price (filter for Windows or Linux - use lowest price)
	var hourlyPrice float64
	for _, item := range items {
		// Skip if it's a spot/low priority price
		// "Spot" and "Low Priority" appear in MeterName/SkuName, not ProductName
		meterNameLower := strings.ToLower(item.MeterName)
		skuNameLower := strings.ToLower(item.SkuName)
		if strings.Contains(meterNameLower, "spot") || strings.Contains(skuNameLower, "spot") ||
			strings.Contains(meterNameLower, "low priority") || strings.Contains(skuNameLower, "low priority") {
			continue
		}

		// Use retail price if available, otherwise unit price
		price := item.RetailPrice
		if price == 0 {
			price = item.UnitPrice
		}

		if hourlyPrice == 0 || price < hourlyPrice {
			hourlyPrice = price
		}
	}

	if hourlyPrice == 0 {
		return 0, fmt.Errorf("no valid pricing found for VM %s", vmSize)
	}

	// Convert hourly to monthly (730 hours average per month)
	monthlyPrice := hourlyPrice * 730

	// Cache the result
	pc.setCachedPrice(cacheKey, PriceEntry{
		ServiceName:   "Virtual Machines",
		SKUName:       vmSize,
		Region:        normalizedRegion,
		UnitPrice:     monthlyPrice,
		CurrencyCode:  "USD",
		UnitOfMeasure: "1 Hour",
		FetchedAt:     time.Now(),
	})

	return monthlyPrice, nil
}

// GetDiskPrice fetches managed disk pricing
func (pc *PricingCache) GetDiskPrice(ctx providers.CloudProviderContext, diskSKU, region string, sizeGB float64) (float64, error) {
	if !pc.IsEnabled() {
		return 0, fmt.Errorf("dynamic pricing is disabled")
	}

	normalizedRegion := normalizeAzureRegion(region)
	cacheKey := fmt.Sprintf("disk:%s:%s", diskSKU, normalizedRegion)

	// Check cache first
	if pricePerGB, found := pc.getCachedPrice(cacheKey); found {
		return pricePerGB * sizeGB, nil
	}

	// Map disk SKU to product name
	productName := mapDiskSKUToProductName(diskSKU)

	// Build filter query
	filter := fmt.Sprintf("serviceName eq 'Storage' and armRegionName eq '%s' and contains(productName, '%s') and priceType eq 'Consumption'",
		normalizedRegion, productName)

	// Fetch from Azure API
	items, err := pc.fetchPrices(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch disk pricing: %w", err)
	}

	if len(items) == 0 {
		return 0, fmt.Errorf("no pricing found for disk SKU %s in region %s", diskSKU, normalizedRegion)
	}

	// Find the best matching tier based on size
	var pricePerGBMonth float64
	for _, item := range items {
		// Look for provisioned storage pricing
		if strings.Contains(strings.ToLower(item.MeterName), "disk") &&
			!strings.Contains(strings.ToLower(item.MeterName), "transaction") &&
			!strings.Contains(strings.ToLower(item.MeterName), "snapshot") {

			price := item.RetailPrice
			if price == 0 {
				price = item.UnitPrice
			}

			if pricePerGBMonth == 0 || price < pricePerGBMonth {
				pricePerGBMonth = price
			}
		}
	}

	if pricePerGBMonth == 0 {
		return 0, fmt.Errorf("no valid pricing found for disk SKU %s", diskSKU)
	}

	// Cache the result (price per GB/month)
	pc.setCachedPrice(cacheKey, PriceEntry{
		ServiceName:   "Storage",
		SKUName:       diskSKU,
		Region:        normalizedRegion,
		UnitPrice:     pricePerGBMonth,
		CurrencyCode:  "USD",
		UnitOfMeasure: "1 GB/Month",
		FetchedAt:     time.Now(),
	})

	return pricePerGBMonth * sizeGB, nil
}

// fetchPrices fetches prices from Azure Retail Prices API with pagination support
func (pc *PricingCache) fetchPrices(ctx providers.CloudProviderContext, filter string) ([]RetailPriceItem, error) {
	baseURL := "https://prices.azure.com/api/retail/prices"
	params := url.Values{}
	params.Add("$filter", filter)
	params.Add("api-version", "2023-01-01-preview")

	fetchURL := fmt.Sprintf("%s?%s", baseURL, params.Encode())

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	var allItems []RetailPriceItem
	const maxPages = 5 // Safety limit to avoid runaway pagination

	for page := 0; page < maxPages && fetchURL != ""; page++ {
		req, err := http.NewRequestWithContext(ctx.GetContext(), "GET", fetchURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to execute request: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
		}

		var priceResp RetailPricesResponse
		if err := json.NewDecoder(resp.Body).Decode(&priceResp); err != nil {
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}

		allItems = append(allItems, priceResp.Items...)
		fetchURL = priceResp.NextPageLink
	}

	return allItems, nil
}

// getCachedPrice retrieves a price from cache if valid
func (pc *PricingCache) getCachedPrice(key string) (float64, bool) {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	entry, found := pc.prices[key]
	if !found {
		return 0, false
	}

	// Check if cache entry is expired
	if time.Since(entry.FetchedAt) > pc.ttl {
		return 0, false
	}

	return entry.UnitPrice, true
}

// setCachedPrice stores a price in cache
func (pc *PricingCache) setCachedPrice(key string, entry PriceEntry) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	pc.prices[key] = entry
	pc.lastUpdated = time.Now()
}

// ClearCache clears all cached prices
func (pc *PricingCache) ClearCache() {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	pc.prices = make(map[string]PriceEntry)
	pc.lastUpdated = time.Time{}
}

// GetCacheStats returns cache statistics
func (pc *PricingCache) GetCacheStats() map[string]interface{} {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	return map[string]interface{}{
		"total_entries": len(pc.prices),
		"last_updated":  pc.lastUpdated,
		"ttl_hours":     pc.ttl.Hours(),
		"enabled":       pc.enabled,
	}
}

// GetStoragePrice fetches blob storage pricing from Azure Retail Prices API
func (pc *PricingCache) GetStoragePrice(ctx providers.CloudProviderContext, storageTier, redundancy, region string) (float64, error) {
	if !pc.IsEnabled() {
		return 0, fmt.Errorf("dynamic pricing is disabled")
	}

	normalizedRegion := normalizeAzureRegion(region)
	cacheKey := fmt.Sprintf("storage:%s:%s:%s", storageTier, redundancy, normalizedRegion)

	// Check cache first
	if pricePerGB, found := pc.getCachedPrice(cacheKey); found {
		return pricePerGB, nil
	}

	// Map storage tier and redundancy to product name for Azure API
	serviceName := "Storage"
	productName := buildStorageProductName(storageTier, redundancy)

	// Build filter query
	filter := fmt.Sprintf("serviceName eq '%s' and armRegionName eq '%s' and contains(productName, '%s') and priceType eq 'Consumption'",
		serviceName, normalizedRegion, productName)

	// Fetch from Azure API
	items, err := pc.fetchPrices(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch storage pricing: %w", err)
	}

	if len(items) == 0 {
		return 0, fmt.Errorf("no pricing found for storage tier %s with %s in region %s", storageTier, redundancy, normalizedRegion)
	}

	// Find storage pricing (not operations/transactions) with correct tier and redundancy
	storageTierLower := strings.ToLower(storageTier)
	var pricePerGBMonth float64
	for _, item := range items {
		meterNameLower := strings.ToLower(item.MeterName)
		redundancyLower := strings.ToLower(redundancy)

		// Look for data storage pricing (exclude operations, transactions, etc.)
		if (strings.Contains(meterNameLower, "data stored") ||
			strings.Contains(meterNameLower, "capacity")) &&
			!strings.Contains(meterNameLower, "transaction") &&
			!strings.Contains(meterNameLower, "operation") &&
			!strings.Contains(meterNameLower, "bandwidth") {

			// Filter by storage tier in meter name (e.g. "Hot LRS Data Stored", "Cool GRS Data Stored")
			if !strings.HasPrefix(meterNameLower, storageTierLower+" ") {
				continue
			}

			// Filter by redundancy type in meter name
			// Azure API uses patterns like "Hot LRS Data Stored", "Cool GRS Data Stored", "Archive RA-GRS Data Stored"
			matchesRedundancy := false
			switch redundancyLower {
			case "lrs":
				matchesRedundancy = strings.Contains(meterNameLower, "lrs")
			case "grs":
				matchesRedundancy = strings.Contains(meterNameLower, "grs") && !strings.Contains(meterNameLower, "ra-grs") && !strings.Contains(meterNameLower, "ragrs")
			case "zrs":
				matchesRedundancy = strings.Contains(meterNameLower, "zrs") && !strings.Contains(meterNameLower, "gzrs")
			case "ra-grs", "ragrs":
				matchesRedundancy = strings.Contains(meterNameLower, "ra-grs") || strings.Contains(meterNameLower, "ragrs")
			case "gzrs":
				matchesRedundancy = strings.Contains(meterNameLower, "gzrs")
			case "ra-gzrs", "ragzrs":
				matchesRedundancy = strings.Contains(meterNameLower, "ra-gzrs") || strings.Contains(meterNameLower, "ragzrs")
			default:
				// For unknown redundancy types, try exact match
				matchesRedundancy = strings.Contains(meterNameLower, redundancyLower)
			}

			if !matchesRedundancy {
				continue
			}

			price := item.RetailPrice
			if price == 0 {
				price = item.UnitPrice
			}

			if pricePerGBMonth == 0 || price < pricePerGBMonth {
				pricePerGBMonth = price
			}
		}
	}

	if pricePerGBMonth == 0 {
		return 0, fmt.Errorf("no valid storage pricing found for %s %s", storageTier, redundancy)
	}

	// Cache the result (price per GB/month)
	pc.setCachedPrice(cacheKey, PriceEntry{
		ServiceName:   serviceName,
		SKUName:       fmt.Sprintf("%s-%s", storageTier, redundancy),
		Region:        normalizedRegion,
		UnitPrice:     pricePerGBMonth,
		CurrencyCode:  "USD",
		UnitOfMeasure: "1 GB/Month",
		FetchedAt:     time.Now(),
	})

	return pricePerGBMonth, nil
}

// GetSQLStoragePrice fetches Azure SQL database storage pricing
func (pc *PricingCache) GetSQLStoragePrice(ctx providers.CloudProviderContext, tier, region string) (float64, error) {
	if !pc.IsEnabled() {
		return 0, fmt.Errorf("dynamic pricing is disabled")
	}

	normalizedRegion := normalizeAzureRegion(region)
	cacheKey := fmt.Sprintf("sql-storage:%s:%s", tier, normalizedRegion)

	// Check cache first
	if pricePerGB, found := pc.getCachedPrice(cacheKey); found {
		return pricePerGB, nil
	}

	// Build filter for SQL Database storage
	serviceName := "SQL Database"
	filter := fmt.Sprintf("serviceName eq '%s' and armRegionName eq '%s' and priceType eq 'Consumption'",
		serviceName, normalizedRegion)

	// Fetch from Azure API
	items, err := pc.fetchPrices(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch SQL storage pricing: %w", err)
	}

	if len(items) == 0 {
		return 0, fmt.Errorf("no pricing found for SQL storage in region %s", normalizedRegion)
	}

	// Find storage pricing
	var pricePerGBMonth float64
	for _, item := range items {
		meterNameLower := strings.ToLower(item.MeterName)
		productNameLower := strings.ToLower(item.ProductName)

		// Look for data storage pricing for the specific tier
		if strings.Contains(meterNameLower, "data stored") ||
			strings.Contains(meterNameLower, "storage") {

			// Match tier if specified
			if tier != "" && !strings.Contains(productNameLower, strings.ToLower(tier)) {
				continue
			}

			price := item.RetailPrice
			if price == 0 {
				price = item.UnitPrice
			}

			if price > 0 && (pricePerGBMonth == 0 || price < pricePerGBMonth) {
				pricePerGBMonth = price
			}
		}
	}

	if pricePerGBMonth == 0 {
		// Use fallback approximate pricing if API doesn't return results
		pricePerGBMonth = 0.115 // Approximate vCore storage pricing
	}

	// Cache the result
	pc.setCachedPrice(cacheKey, PriceEntry{
		ServiceName:   serviceName,
		SKUName:       tier,
		Region:        normalizedRegion,
		UnitPrice:     pricePerGBMonth,
		CurrencyCode:  "USD",
		UnitOfMeasure: "1 GB/Month",
		FetchedAt:     time.Now(),
	})

	return pricePerGBMonth, nil
}

// GetBackupStoragePrice fetches backup storage pricing
func (pc *PricingCache) GetBackupStoragePrice(ctx providers.CloudProviderContext, redundancy, region string) (float64, error) {
	if !pc.IsEnabled() {
		return 0, fmt.Errorf("dynamic pricing is disabled")
	}

	normalizedRegion := normalizeAzureRegion(region)
	cacheKey := fmt.Sprintf("backup:%s:%s", redundancy, normalizedRegion)

	// Check cache first
	if pricePerGB, found := pc.getCachedPrice(cacheKey); found {
		return pricePerGB, nil
	}

	// Build filter for backup storage
	serviceName := "Storage"

	filter := fmt.Sprintf("serviceName eq '%s' and armRegionName eq '%s' and contains(productName, 'Blob') and priceType eq 'Consumption'",
		serviceName, normalizedRegion)

	// Fetch from Azure API
	items, err := pc.fetchPrices(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch backup storage pricing: %w", err)
	}

	if len(items) == 0 {
		// Use fallback pricing
		switch redundancy {
		case "LRS":
			return 0.10, nil
		case "GRS", "RA-GRS":
			return 0.20, nil
		default:
			return 0.10, nil
		}
	}

	// Find backup storage pricing
	var pricePerGBMonth float64
	for _, item := range items {
		meterNameLower := strings.ToLower(item.MeterName)

		if (strings.Contains(meterNameLower, "data stored") ||
			strings.Contains(meterNameLower, "cool")) &&
			!strings.Contains(meterNameLower, "transaction") {

			price := item.RetailPrice
			if price == 0 {
				price = item.UnitPrice
			}

			if price > 0 && (pricePerGBMonth == 0 || price < pricePerGBMonth) {
				pricePerGBMonth = price
			}
		}
	}

	if pricePerGBMonth == 0 {
		// Fallback pricing
		switch redundancy {
		case "LRS":
			pricePerGBMonth = 0.10
		case "GRS", "RA-GRS":
			pricePerGBMonth = 0.20
		default:
			pricePerGBMonth = 0.10
		}
	}

	// Cache the result
	pc.setCachedPrice(cacheKey, PriceEntry{
		ServiceName:   "Storage",
		SKUName:       fmt.Sprintf("Backup-%s", redundancy),
		Region:        normalizedRegion,
		UnitPrice:     pricePerGBMonth,
		CurrencyCode:  "USD",
		UnitOfMeasure: "1 GB/Month",
		FetchedAt:     time.Now(),
	})

	return pricePerGBMonth, nil
}

// GetSQLComputePrice fetches Azure SQL database compute pricing (vCore or DTU)
func (pc *PricingCache) GetSQLComputePrice(ctx providers.CloudProviderContext, tier, skuName, region string) (float64, error) {
	if !pc.IsEnabled() {
		return 0, fmt.Errorf("dynamic pricing is disabled")
	}

	normalizedRegion := normalizeAzureRegion(region)
	cacheKey := fmt.Sprintf("sql-compute:%s:%s:%s", tier, skuName, normalizedRegion)

	// Check cache first
	if pricePerUnit, found := pc.getCachedPrice(cacheKey); found {
		return pricePerUnit, nil
	}

	// Build filter for SQL Database compute
	serviceName := "SQL Database"
	// Azure API uses armSkuName like "SQLDB_GP_Compute_Gen5_2" for vCore SKUs,
	// while our code passes "GP_Gen5_2". Map to the correct format.
	apiSkuName := mapSQLSkuToArmSkuName(skuName)
	filter := fmt.Sprintf("serviceName eq '%s' and armRegionName eq '%s' and armSkuName eq '%s' and priceType eq 'Consumption'",
		serviceName, normalizedRegion, apiSkuName)

	// Fetch from Azure API
	items, err := pc.fetchPrices(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch SQL compute pricing: %w", err)
	}

	if len(items) == 0 {
		// Fallback: try without exact SKU match, filter by tier
		filter = fmt.Sprintf("serviceName eq '%s' and armRegionName eq '%s' and priceType eq 'Consumption'",
			serviceName, normalizedRegion)
		items, err = pc.fetchPrices(ctx, filter)
		if err != nil || len(items) == 0 {
			// Use fallback approximate pricing based on tier
			return getSQLComputeFallbackPrice(tier, skuName), nil
		}
	}

	// Find compute pricing (vCore or DTU)
	var pricePerUnit float64
	for _, item := range items {
		meterNameLower := strings.ToLower(item.MeterName)
		skuNameLower := strings.ToLower(item.SkuName)

		// Look for vCore or DTU compute pricing
		if (strings.Contains(meterNameLower, "vcore") ||
			strings.Contains(meterNameLower, "dtu") ||
			strings.Contains(skuNameLower, strings.ToLower(skuName))) &&
			!strings.Contains(meterNameLower, "storage") &&
			!strings.Contains(meterNameLower, "backup") {

			price := item.RetailPrice
			if price == 0 {
				price = item.UnitPrice
			}

			if price > 0 && (pricePerUnit == 0 || price < pricePerUnit) {
				pricePerUnit = price
			}
		}
	}

	if pricePerUnit == 0 {
		// Use fallback pricing
		pricePerUnit = getSQLComputeFallbackPrice(tier, skuName)
	}

	// Cache the result
	pc.setCachedPrice(cacheKey, PriceEntry{
		ServiceName:   serviceName,
		SKUName:       skuName,
		Region:        normalizedRegion,
		UnitPrice:     pricePerUnit,
		CurrencyCode:  "USD",
		UnitOfMeasure: "1 Hour",
		FetchedAt:     time.Now(),
	})

	return pricePerUnit, nil
}

// getSQLComputeFallbackPrice provides fallback pricing for SQL compute
func getSQLComputeFallbackPrice(tier, skuName string) float64 {
	skuUpper := strings.ToUpper(skuName)

	// vCore pricing (hourly rates, approximate East US pricing)
	if strings.Contains(skuUpper, "GP_GEN5") {
		// General Purpose Gen5: ~$0.10/vCore/hour
		return 0.10
	}
	if strings.Contains(skuUpper, "BC_GEN5") {
		// Business Critical Gen5: ~$0.27/vCore/hour
		return 0.27
	}

	// DTU pricing (monthly equivalents converted to hourly)
	switch {
	case strings.HasPrefix(skuUpper, "BASIC"):
		return 0.007 // ~$5/month
	case strings.Contains(skuUpper, "S0"):
		return 0.02 // ~$15/month
	case strings.Contains(skuUpper, "S1"):
		return 0.04 // ~$30/month
	case strings.Contains(skuUpper, "S2"):
		return 0.08 // ~$60/month
	case strings.Contains(skuUpper, "S3"):
		return 0.16 // ~$120/month
	case strings.Contains(skuUpper, "P1"):
		return 0.68 // ~$500/month
	case strings.Contains(skuUpper, "P2"):
		return 1.37 // ~$1000/month
	default:
		return 0.10 // Default to GP Gen5 equivalent
	}
}

// buildStorageProductName constructs Azure Storage product name for API queries
func buildStorageProductName(storageTier, redundancy string) string {
	// Azure Retail Prices API uses "Blob Storage" as the product name for all tiers
	// (hot/cool/archive). The tier is encoded in the meter name (e.g. "Hot LRS Data Stored"),
	// not the product name. Premium blob storage uses a different product.
	switch strings.ToLower(storageTier) {
	case "premium":
		return "Premium Block Blob"
	default:
		return "Blob Storage"
	}
}

// mapDiskSKUToProductName maps disk SKU to Azure product name for API queries
func mapDiskSKUToProductName(diskSKU string) string {
	switch {
	case strings.Contains(diskSKU, "Premium_LRS"):
		return "Premium SSD Managed Disks"
	case strings.Contains(diskSKU, "StandardSSD_LRS"):
		return "Standard SSD Managed Disks"
	case strings.Contains(diskSKU, "Standard_LRS"):
		return "Standard HDD Managed Disks"
	case strings.Contains(diskSKU, "PremiumV2_LRS"):
		return "Premium SSD v2 Managed Disks"
	case strings.Contains(diskSKU, "UltraSSD_LRS"):
		return "Ultra Disks"
	default:
		return "Managed Disks"
	}
}

// mapSQLSkuToArmSkuName maps SQL SKU names (e.g. "GP_Gen5_2") to Azure API armSkuName format
// (e.g. "SQLDB_GP_Compute_Gen5_2"). DTU-based SKUs have empty armSkuName in Azure API,
// so we return the original value to let the fallback mechanism handle them.
func mapSQLSkuToArmSkuName(skuName string) string {
	upper := strings.ToUpper(skuName)
	// vCore SKUs: GP_Gen5_2 -> SQLDB_GP_Compute_Gen5_2, BC_Gen5_4 -> SQLDB_BC_Compute_Gen5_4
	if strings.HasPrefix(upper, "GP_GEN") || strings.HasPrefix(upper, "BC_GEN") {
		return "SQLDB_" + strings.Replace(skuName, "_Gen", "_Compute_Gen", 1)
	}
	// HS (Hyperscale) SKUs
	if strings.HasPrefix(upper, "HS_GEN") {
		return "SQLDB_" + strings.Replace(skuName, "_Gen", "_Compute_Gen", 1)
	}
	return skuName
}
