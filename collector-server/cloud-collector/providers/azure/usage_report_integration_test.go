package azure

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"os"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/costmanagement/armcostmanagement"
)

// TestAzureUsageReportIntegration tests the Azure usage report functionality
func TestAzureUsageReportIntegration(t *testing.T) {
	if os.Getenv("RUN_AZURE_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_AZURE_INTEGRATION_TESTS=true to run.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	securityCtx := security.NewSecurityContextForSuperAdmin()
	ctx := security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

	account := getTestAzureAccount(t)

	// Test current month
	t.Run("GetUsageReport-CurrentMonth", func(t *testing.T) {
		now := time.Now()
		month := now.Month()
		year := now.Year()

		response, err := getAzureUsageReport(ctx, account, month, year)
		if err != nil {
			t.Fatalf("GetUsageReport failed: %v", err)
		}

		fmt.Printf("\n=== AZURE USAGE REPORT - CURRENT MONTH ===\n")
		fmt.Printf("Month: %s %d\n", month, year)
		fmt.Printf("Total items: %d\n\n", len(response.Items))

		if len(response.Items) > 0 {
			// Group by service
			serviceMap := make(map[string]float64)
			serviceCounts := make(map[string]int)
			totalCost := 0.0

			for _, item := range response.Items {
				serviceMap[item.ProductServiceCode] += item.Cost
				serviceCounts[item.ProductServiceCode]++
				totalCost += item.Cost
			}

			fmt.Printf("Summary:\n")
			fmt.Printf("  Total Cost: %.2f %s\n", totalCost, response.Items[0].CostCurrency)
			fmt.Printf("  Unique Services: %d\n\n", len(serviceMap))

			fmt.Printf("Cost by Service:\n")
			for service, cost := range serviceMap {
				fmt.Printf("  %-40s: $%.2f (%d items)\n", service, cost, serviceCounts[service])
			}
			fmt.Printf("\n")

			// Print sample items (first 5)
			fmt.Printf("Sample Usage Items:\n")
			for i := 0; i < len(response.Items) && i < 5; i++ {
				item := response.Items[i]
				fmt.Printf("\nItem #%d:\n", i+1)
				fmt.Printf("  Resource ID: %s\n", item.ResourceId)
				fmt.Printf("  Resource Name: %s\n", item.ResourceName)
				fmt.Printf("  Resource Type: %s\n", item.ResourceType)
				fmt.Printf("  Product Code: %s\n", item.ProductCode)
				fmt.Printf("  Service Code: %s\n", item.ProductServiceCode)
				fmt.Printf("  Region: %s\n", item.ResourceRegionCode)
				fmt.Printf("  Cost: %.4f %s\n", item.Cost, item.CostCurrency)
				fmt.Printf("  Cost Category: %s\n", item.CostCategory)
				fmt.Printf("  Cost Sub-Category: %s\n", item.CostSubCategory)
				fmt.Printf("  Charge Type: %s\n", item.ChargeType)
				fmt.Printf("  Publisher Type: %s\n", item.PublisherType)
				fmt.Printf("  Pricing Model: %s\n", item.PricingModel)
				fmt.Printf("  Start Date: %s\n", item.StartDate.Format("2006-01-02"))
				fmt.Printf("  End Date: %s\n", item.EndDate.Format("2006-01-02"))
				if len(item.ResourceTags) > 0 {
					fmt.Printf("  Tags: %v\n", item.ResourceTags)
				}
			}
		}

		t.Logf("Found %d usage report items for %s %d", len(response.Items), month, year)
	})

	// Test previous month
	t.Run("GetUsageReport-PreviousMonth", func(t *testing.T) {
		now := time.Now()
		// Get previous month
		firstDayOfCurrentMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		lastMonth := firstDayOfCurrentMonth.AddDate(0, -1, 0)
		month := lastMonth.Month()
		year := lastMonth.Year()

		response, err := getAzureUsageReport(ctx, account, month, year)
		if err != nil {
			t.Fatalf("GetUsageReport failed: %v", err)
		}

		fmt.Printf("\n=== AZURE USAGE REPORT - PREVIOUS MONTH ===\n")
		fmt.Printf("Month: %s %d\n", month, year)
		fmt.Printf("Total items: %d\n\n", len(response.Items))

		if len(response.Items) > 0 {
			// Verify date ranges are correct
			for _, item := range response.Items {
				itemMonth := item.StartDate.Month()
				itemYear := item.StartDate.Year()
				if itemMonth != month || itemYear != year {
					t.Errorf("Item date mismatch: expected %s %d, got %s %d",
						month, year, itemMonth, itemYear)
				}
			}

			totalCost := 0.0
			for _, item := range response.Items {
				totalCost += item.Cost
			}

			fmt.Printf("Summary:\n")
			fmt.Printf("  Total Cost: %.2f %s\n", totalCost, response.Items[0].CostCurrency)
		}

		t.Logf("Found %d usage report items for %s %d", len(response.Items), month, year)
	})
}

// TestAzureUsageReportDataValidation tests data validation
func TestAzureUsageReportDataValidation(t *testing.T) {
	if os.Getenv("RUN_AZURE_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_AZURE_INTEGRATION_TESTS=true to run.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	securityCtx := security.NewSecurityContextForSuperAdmin()
	ctx := security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

	account := getTestAzureAccount(t)

	now := time.Now()
	month := now.Month()
	year := now.Year()

	response, err := getAzureUsageReport(ctx, account, month, year)
	if err != nil {
		t.Fatalf("GetUsageReport failed: %v", err)
	}

	if len(response.Items) == 0 {
		t.Skip("No usage data available for validation")
	}

	t.Run("ValidateRequiredFields", func(t *testing.T) {
		fmt.Printf("\n=== VALIDATING REQUIRED FIELDS ===\n")
		invalidItems := 0

		for i, item := range response.Items {
			issues := []string{}

			// Validate required fields
			if item.Cost < 0 {
				issues = append(issues, "negative cost")
			}
			if item.CostCurrency == "" {
				issues = append(issues, "missing currency")
			}
			if item.ResourceId == "" {
				issues = append(issues, "missing resource ID")
			}
			if item.ResourceArn == "" {
				issues = append(issues, "missing resource ARN")
			}
			if item.ProductCode == "" {
				issues = append(issues, "missing product code")
			}
			if item.StartDate.IsZero() {
				issues = append(issues, "missing start date")
			}
			if item.EndDate.IsZero() {
				issues = append(issues, "missing end date")
			}

			if len(issues) > 0 {
				invalidItems++
				fmt.Printf("Item #%d has issues: %v\n", i+1, issues)
			}
		}

		if invalidItems > 0 {
			t.Errorf("Found %d items with validation issues out of %d total items", invalidItems, len(response.Items))
		} else {
			fmt.Printf("All %d items passed validation\n", len(response.Items))
		}
	})

	t.Run("ValidateResourceTypes", func(t *testing.T) {
		fmt.Printf("\n=== VALIDATING RESOURCE TYPES ===\n")

		resourceTypes := make(map[string]int)
		for _, item := range response.Items {
			resourceTypes[item.ResourceType]++
		}

		fmt.Printf("Found %d unique resource types:\n", len(resourceTypes))
		for resType, count := range resourceTypes {
			fmt.Printf("  %-50s: %d\n", resType, count)
		}
	})

	t.Run("ValidateRegions", func(t *testing.T) {
		fmt.Printf("\n=== VALIDATING REGIONS ===\n")

		regions := make(map[string]int)
		for _, item := range response.Items {
			if item.ResourceRegionCode != "" {
				regions[item.ResourceRegionCode]++
			}
		}

		fmt.Printf("Found %d unique regions:\n", len(regions))
		for region, count := range regions {
			fmt.Printf("  %-30s: %d\n", region, count)
		}
	})

	t.Run("ValidateCostCategories", func(t *testing.T) {
		fmt.Printf("\n=== VALIDATING COST CATEGORIES ===\n")

		categories := make(map[string]int)
		subCategories := make(map[string]int)

		for _, item := range response.Items {
			if string(item.CostCategory) != "" {
				categories[string(item.CostCategory)]++
			}
			if item.CostSubCategory != "" {
				subCategories[item.CostSubCategory]++
			}
		}

		fmt.Printf("Found %d unique cost categories:\n", len(categories))
		for cat, count := range categories {
			fmt.Printf("  %-40s: %d\n", cat, count)
		}

		fmt.Printf("\nFound %d unique cost sub-categories\n", len(subCategories))
	})

	t.Run("ValidateChargeTypes", func(t *testing.T) {
		fmt.Printf("\n=== VALIDATING CHARGE TYPES ===\n")

		chargeTypes := make(map[string]int)
		publisherTypes := make(map[string]int)
		pricingModels := make(map[string]int)

		for _, item := range response.Items {
			if item.ChargeType != "" {
				chargeTypes[item.ChargeType]++
			}
			if item.PublisherType != "" {
				publisherTypes[item.PublisherType]++
			}
			if item.PricingModel != "" {
				pricingModels[item.PricingModel]++
			}
		}

		fmt.Printf("Charge Types:\n")
		for ct, count := range chargeTypes {
			fmt.Printf("  %-30s: %d\n", ct, count)
		}

		fmt.Printf("\nPublisher Types:\n")
		for pt, count := range publisherTypes {
			fmt.Printf("  %-30s: %d\n", pt, count)
		}

		fmt.Printf("\nPricing Models:\n")
		for pm, count := range pricingModels {
			fmt.Printf("  %-30s: %d\n", pm, count)
		}
	})
}

// TestAzureProviderIntegration tests the main Azure provider GetUsageReport method
func TestAzureProviderIntegration(t *testing.T) {
	if os.Getenv("RUN_AZURE_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_AZURE_INTEGRATION_TESTS=true to run.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	securityCtx := security.NewSecurityContextForSuperAdmin()
	ctx := security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

	account := getTestAzureAccount(t)

	provider := &azureProvider{}

	t.Run("GetUsageReport-ViaProvider", func(t *testing.T) {
		now := time.Now()
		month := now.Month()
		year := now.Year()

		response, err := provider.GetUsageReport(ctx, account, month, year)
		if err != nil {
			t.Fatalf("Provider GetUsageReport failed: %v", err)
		}

		fmt.Printf("\n=== AZURE PROVIDER USAGE REPORT ===\n")
		fmt.Printf("Month: %s %d\n", month, year)
		fmt.Printf("Total items: %d\n", len(response.Items))

		t.Logf("Provider returned %d usage report items", len(response.Items))
	})
}

// TestConvertToUsageReportItem tests the conversion function
func TestConvertToUsageReportItem(t *testing.T) {
	// This is a unit test that doesn't require real Azure credentials
	t.Run("ConvertValidRow", func(t *testing.T) {
		// Mock header and row data simulating Azure Cost Management API response
		header := []*armcostmanagement.QueryColumn{
			{Name: to.Ptr("PreTaxCost")},
			{Name: to.Ptr("Currency")},
			{Name: to.Ptr("UsageDate")},
			{Name: to.Ptr("ResourceId")},
			{Name: to.Ptr("ResourceType")},
			{Name: to.Ptr("ResourceLocation")},
			{Name: to.Ptr("ConsumedService")},
			{Name: to.Ptr("MeterCategory")},
			{Name: to.Ptr("MeterSubcategory")},
			{Name: to.Ptr("ChargeType")},
			{Name: to.Ptr("PublisherType")},
			{Name: to.Ptr("PricingModel")},
		}

		row := []any{
			10.5,       // PreTaxCost
			"USD",      // Currency
			20250101.0, // UsageDate as float
			"/subscriptions/sub-123/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/vm-test", // ResourceId
			"Microsoft.Compute/virtualMachines", // ResourceType
			"eastus",                            // ResourceLocation
			"Microsoft.Compute",                 // ConsumedService
			"Virtual Machines",                  // MeterCategory
			"D2s v3",                            // MeterSubcategory
			"Usage",                             // ChargeType
			"Azure",                             // PublisherType
			"OnDemand",                          // PricingModel
		}

		item, err := convertToUsageReportItem(header, row)
		if err != nil {
			t.Fatalf("convertToUsageReportItem failed: %v", err)
		}

		// Validate conversions
		if item.Cost != 10.5 {
			t.Errorf("Expected cost 10.5, got %.2f", item.Cost)
		}
		if item.CostCurrency != "USD" {
			t.Errorf("Expected currency USD, got %s", item.CostCurrency)
		}
		if item.ResourceId == "" {
			t.Error("ResourceId should not be empty")
		}
		if item.ResourceName != "vm-test" {
			t.Errorf("Expected resource name 'vm-test', got %s", item.ResourceName)
		}
		if item.ResourceType != "virtualmachines" {
			t.Errorf("Expected resource type 'virtualmachines', got %s", item.ResourceType)
		}
		if item.ProductCode != "microsoft.compute/virtualmachines" {
			t.Errorf("Expected product code 'microsoft.compute/virtualmachines', got %s", item.ProductCode)
		}
		if item.ResourceRegionCode != "eastus" {
			t.Errorf("Expected region 'eastus', got %s", item.ResourceRegionCode)
		}

		fmt.Printf("\n=== CONVERSION TEST RESULTS ===\n")
		fmt.Printf("Cost: %.2f %s\n", item.Cost, item.CostCurrency)
		fmt.Printf("Resource: %s (%s)\n", item.ResourceName, item.ResourceType)
		fmt.Printf("Region: %s\n", item.ResourceRegionCode)
		fmt.Printf("Service: %s\n", item.ProductServiceCode)
		fmt.Printf("Date: %s\n", item.StartDate.Format("2006-01-02"))
	})
}

func TestConvertToUsageReportItem_SubResource(t *testing.T) {
	// Azure Cost Management API reports ResourceType as the parent service type
	// (e.g., "microsoft.sql/servers") even for child resources like databases.
	// The fix derives the correct leaf type from the ARM ResourceId path.
	t.Run("SQLDatabase", func(t *testing.T) {
		header := []*armcostmanagement.QueryColumn{
			{Name: to.Ptr("PreTaxCost")},
			{Name: to.Ptr("Currency")},
			{Name: to.Ptr("UsageDate")},
			{Name: to.Ptr("ResourceId")},
			{Name: to.Ptr("ResourceType")},
			{Name: to.Ptr("ResourceLocation")},
			{Name: to.Ptr("ConsumedService")},
			{Name: to.Ptr("MeterCategory")},
			{Name: to.Ptr("MeterSubcategory")},
			{Name: to.Ptr("ChargeType")},
			{Name: to.Ptr("PublisherType")},
			{Name: to.Ptr("PricingModel")},
		}

		row := []any{
			15.75,
			"USD",
			20250315.0,
			"/subscriptions/sub-123/resourcegroups/rg-1/providers/microsoft.sql/servers/myserver/databases/mydb",
			"microsoft.sql/servers", // API reports parent type, not microsoft.sql/servers/databases
			"southindia",
			"microsoft.sql",
			"SQL Database",
			"vcore",
			"usage",
			"azure",
			"ondemand",
		}

		item, err := convertToUsageReportItem(header, row)
		if err != nil {
			t.Fatalf("convertToUsageReportItem failed: %v", err)
		}

		if item.ResourceType != "databases" {
			t.Errorf("Expected resource type 'databases', got %q", item.ResourceType)
		}
		if item.ProductCode != "microsoft.sql/servers" {
			t.Errorf("Expected product code 'microsoft.sql/servers', got %q", item.ProductCode)
		}
		if item.ResourceName != "mydb" {
			t.Errorf("Expected resource name 'mydb', got %q", item.ResourceName)
		}
	})

	t.Run("ElasticPool", func(t *testing.T) {
		header := []*armcostmanagement.QueryColumn{
			{Name: to.Ptr("PreTaxCost")},
			{Name: to.Ptr("ResourceId")},
			{Name: to.Ptr("ResourceType")},
		}

		row := []any{
			5.0,
			"/subscriptions/sub-123/resourcegroups/rg-1/providers/microsoft.sql/servers/myserver/elasticpools/mypool",
			"microsoft.sql/servers",
		}

		item, err := convertToUsageReportItem(header, row)
		if err != nil {
			t.Fatalf("convertToUsageReportItem failed: %v", err)
		}

		if item.ResourceType != "elasticpools" {
			t.Errorf("Expected resource type 'elasticpools', got %q", item.ResourceType)
		}
	})
}

func TestExtractLeafTypeFromArmResourceId(t *testing.T) {
	tests := []struct {
		name     string
		armId    string
		expected string
	}{
		{
			name:     "SQL database (sub-resource)",
			armId:    "/subscriptions/sub-123/resourceGroups/rg-1/providers/Microsoft.Sql/servers/myserver/databases/mydb",
			expected: "databases",
		},
		{
			name:     "VM (top-level resource)",
			armId:    "/subscriptions/sub-123/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/vm-test",
			expected: "virtualmachines",
		},
		{
			name:     "Elastic pool (sub-resource)",
			armId:    "/subscriptions/sub-123/resourceGroups/rg-1/providers/Microsoft.Sql/servers/myserver/elasticPools/mypool",
			expected: "elasticpools",
		},
		{
			name:     "VNet subnet (sub-resource)",
			armId:    "/subscriptions/sub-123/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/myvnet/subnets/mysubnet",
			expected: "subnets",
		},
		{
			name:     "Storage account (top-level)",
			armId:    "/subscriptions/sub-123/resourceGroups/rg-1/providers/Microsoft.Storage/storageAccounts/mystorage",
			expected: "storageaccounts",
		},
		{
			name:     "No providers segment",
			armId:    "/subscriptions/sub-123/resourceGroups/rg-1",
			expected: "",
		},
		{
			name:     "Empty string",
			armId:    "",
			expected: "",
		},
		{
			name:     "Lowercase ARM ID",
			armId:    "/subscriptions/sub-123/resourcegroups/rg-1/providers/microsoft.sql/servers/myserver/databases/mydb",
			expected: "databases",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLeafTypeFromArmResourceId(tt.armId)
			if got != tt.expected {
				t.Errorf("extractLeafTypeFromArmResourceId(%q) = %q, want %q", tt.armId, got, tt.expected)
			}
		})
	}
}

// Helper function to create test account from environment
func getTestAzureAccount(t *testing.T) providers.Account {
	tenantID := os.Getenv("AZURE_TENANT_ID")
	if tenantID == "" {
		t.Fatal("AZURE_TENANT_ID environment variable is required")
	}

	clientID := os.Getenv("AZURE_CLIENT_ID")
	if clientID == "" {
		t.Fatal("AZURE_CLIENT_ID environment variable is required")
	}

	clientSecret := os.Getenv("AZURE_CLIENT_SECRET")
	if clientSecret == "" {
		t.Fatal("AZURE_CLIENT_SECRET environment variable is required")
	}

	subscriptionID := os.Getenv("AZURE_SUBSCRIPTION_ID")
	if subscriptionID == "" {
		t.Fatal("AZURE_SUBSCRIPTION_ID environment variable is required")
	}

	// Encrypt the client secret
	encryptedSecret, err := common.Encrypt(clientSecret)
	if err != nil {
		t.Fatalf("Failed to encrypt client secret: %v", err)
	}

	return providers.Account{
		AccountNumber: tenantID,
		AccountName:   "Test Azure Subscription",
		AccessKey:     &clientID,
		AccessSecret:  &encryptedSecret,
		AssumeRole:    &subscriptionID,
	}
}
