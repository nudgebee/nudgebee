package gcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/collector/cloud/security"
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/gcloud/models"

	"cloud.google.com/go/bigquery"
	"github.com/stretchr/testify/assert"
	"google.golang.org/api/option"
)

func TestGetBillingConfigFromAccount(t *testing.T) {
	tests := []struct {
		name        string
		accountData *string
		expected    models.BillingConfig
		expectError bool
	}{
		{
			name: "valid billing config",
			accountData: stringPtr(`{
				"billing_data": {
					"billing_project_id": "test-project",
					"dataset_name": "billing_dataset",
					"table_name": "gcp_billing_export_v1_1234567890"
				}
			}`),
			expected: models.BillingConfig{
				ProjectID: "test-project",
				DatasetID: "billing_dataset",
				TableID:   "gcp_billing_export_v1_1234567890",
			},
			expectError: false,
		},
		{
			name:        "nil account data",
			accountData: nil,
			expected:    models.BillingConfig{},
			expectError: true,
		},
		{
			name:        "empty account data",
			accountData: stringPtr(""),
			expected:    models.BillingConfig{},
			expectError: true,
		},
		{
			name:        "invalid JSON",
			accountData: stringPtr("invalid json"),
			expected:    models.BillingConfig{},
			expectError: true,
		},
		{
			name: "missing project_id - should fallback to account number",
			accountData: stringPtr(`{
				"billing_data": {
					"dataset_name": "billing_dataset",
					"table_name": "gcp_billing_export_v1_1234567890"
				}
			}`),
			expected: models.BillingConfig{
				ProjectID: "test-account", // Should use AccountNumber as fallback
				DatasetID: "billing_dataset",
				TableID:   "gcp_billing_export_v1_1234567890",
			},
			expectError: false,
		},
		{
			name: "missing dataset_id",
			accountData: stringPtr(`{
				"billing_data": {
					"billing_project_id": "test-project",
					"table_name": "gcp_billing_export_v1_1234567890"
				}
			}`),
			expected:    models.BillingConfig{},
			expectError: true,
		},
		{
			name: "missing table_id",
			accountData: stringPtr(`{
				"billing_data": {
					"billing_project_id": "test-project",
					"dataset_name": "billing_dataset"
				}
			}`),
			expected:    models.BillingConfig{},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := providers.Account{
				AccountNumber: "test-account",
				AccountName:   "Test Account",
				Data:          tt.accountData,
			}

			config, err := getBillingConfigFromAccount(account)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if config.ProjectID != tt.expected.ProjectID {
				t.Errorf("expected ProjectID %s, got %s", tt.expected.ProjectID, config.ProjectID)
			}

			if config.DatasetID != tt.expected.DatasetID {
				t.Errorf("expected DatasetID %s, got %s", tt.expected.DatasetID, config.DatasetID)
			}

			if config.TableID != tt.expected.TableID {
				t.Errorf("expected TableID %s, got %s", tt.expected.TableID, config.TableID)
			}
		})
	}
}

func TestConvertToGcpUsageReportItem(t *testing.T) {
	startTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	endTime := time.Date(2024, 1, 15, 11, 30, 0, 0, time.UTC)

	tests := []struct {
		name     string
		row      models.BigQueryBillingRow
		expected providers.UsageReportItem
	}{
		{
			name: "complete billing row",
			row: models.BigQueryBillingRow{
				ServiceName:    "Compute Engine",
				SKUDescription: "N1 Predefined Instance Core running in Americas",
				UsageStartTime: startTime,
				UsageEndTime:   endTime,
				ProjectID:      bigquery.NullString{StringVal: "test-project", Valid: true},
				Region:         bigquery.NullString{StringVal: "us-central1", Valid: true},
				ResourceName:   bigquery.NullString{StringVal: "test-instance", Valid: true},
				Cost:           0.123456,
				Currency:       "USD",
				CostType:       "REGULAR",
				Labels: []models.LabelEntry{
					{Key: bigquery.NullString{StringVal: "environment", Valid: true}, Value: bigquery.NullString{StringVal: "production", Valid: true}},
					{Key: bigquery.NullString{StringVal: "team", Valid: true}, Value: bigquery.NullString{StringVal: "backend", Valid: true}},
				},
				SystemLabels: []models.LabelEntry{
					{Key: bigquery.NullString{StringVal: "goog-resource-type", Valid: true}, Value: bigquery.NullString{StringVal: "compute.googleapis.com/Instance", Valid: true}},
				},
				UsageAmount: 1.0,
				UsageUnit:   "hour",
			},
			expected: providers.UsageReportItem{
				ProductCode:        "Compute Engine",
				ProductServiceCode: "N1 Predefined Instance Core running in Americas",
				ResourceRegionCode: "us-central1",
				ResourceId:         "test-project/test-instance",
				StartDate:          startTime,
				EndDate:            endTime,
				Cost:               0.123456,
				CostCurrency:       "USD",
				CostCategory:       providers.UsageReportItemTypeUsage,
				ResourceType:       "compute-engine",
				CostSubCategory:    "N1 Predefined Instance Core running in Americas",
				ResourceTags: map[string][]string{
					"environment":               {"production"},
					"team":                      {"backend"},
					"system:goog-resource-type": {"compute.googleapis.com/Instance"},
				},
			},
		},
		{
			name: "tax cost type",
			row: models.BigQueryBillingRow{
				ServiceName:    "Cloud Storage",
				SKUDescription: "Standard Storage US",
				UsageStartTime: startTime,
				UsageEndTime:   endTime,
				ProjectID:      bigquery.NullString{StringVal: "test-project", Valid: true},
				Region:         bigquery.NullString{StringVal: "us", Valid: true},
				ResourceName:   bigquery.NullString{StringVal: "", Valid: true},
				Cost:           0.05,
				Currency:       "USD",
				CostType:       "TAX",
			},
			expected: providers.UsageReportItem{
				ProductCode:        "Cloud Storage",
				ProductServiceCode: "Standard Storage US",
				ResourceRegionCode: "us",
				ResourceId:         "test-project",
				StartDate:          startTime,
				EndDate:            endTime,
				Cost:               0.05,
				CostCurrency:       "USD",
				CostCategory:       providers.UsageReportItemTypeTax,
				ResourceType:       "cloud-storage",
				CostSubCategory:    "Standard Storage US",
				ResourceTags:       map[string][]string{},
			},
		},
		{
			name: "adjustment cost type",
			row: models.BigQueryBillingRow{
				ServiceName:    "BigQuery",
				SKUDescription: "Analysis",
				UsageStartTime: startTime,
				UsageEndTime:   endTime,
				ProjectID:      bigquery.NullString{StringVal: "test-project", Valid: true},
				Region:         bigquery.NullString{StringVal: "us-central1", Valid: true},
				ResourceName:   bigquery.NullString{StringVal: "adjustment", Valid: true},
				Cost:           -0.01,
				Currency:       "USD",
				CostType:       "ADJUSTMENT",
			},
			expected: providers.UsageReportItem{
				ProductCode:        "BigQuery",
				ProductServiceCode: "Analysis",
				ResourceRegionCode: "us-central1",
				ResourceId:         "test-project/adjustment",
				StartDate:          startTime,
				EndDate:            endTime,
				Cost:               -0.01,
				CostCurrency:       "USD",
				CostCategory:       providers.UsageReportItemTypeUnknown,
				ResourceType:       "bigquery",
				CostSubCategory:    "Analysis",
				ResourceTags:       map[string][]string{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := convertToGcpUsageReportItem(tt.row)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if len(results) == 0 {
				t.Fatalf("expected at least one item")
			}
			result := results[0]

			if result.ProductCode != tt.expected.ProductCode {
				t.Errorf("expected ProductCode %s, got %s", tt.expected.ProductCode, result.ProductCode)
			}
			if result.Cost != tt.expected.Cost {
				t.Errorf("expected Cost %f, got %f", tt.expected.Cost, result.Cost)
			}
			if result.CostCategory != tt.expected.CostCategory {
				t.Errorf("expected CostCategory %v, got %v", tt.expected.CostCategory, result.CostCategory)
			}
			if result.ResourceId != tt.expected.ResourceId {
				t.Errorf("expected ResourceId %s, got %s", tt.expected.ResourceId, result.ResourceId)
			}
		})
	}

	// Dedicated test: credit rows emit a separate credit item
	t.Run("row with credit emits separate credit item", func(t *testing.T) {
		row := models.BigQueryBillingRow{
			ServiceName:    "Compute Engine",
			SKUDescription: "N1 Core",
			UsageStartTime: startTime,
			UsageEndTime:   endTime,
			ProjectID:      bigquery.NullString{StringVal: "test-project", Valid: true},
			Region:         bigquery.NullString{StringVal: "us-central1", Valid: true},
			ResourceName:   bigquery.NullString{StringVal: "test-instance", Valid: true},
			Cost:           100.0,
			CreditAmount:   bigquery.NullFloat64{Float64: -100.0, Valid: true},
			Currency:       "USD",
			CostType:       "REGULAR",
		}
		results, err := convertToGcpUsageReportItem(row)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 items (gross + credit), got %d", len(results))
		}
		// First item: gross
		if results[0].Cost != 100.0 {
			t.Errorf("expected gross cost 100.0, got %f", results[0].Cost)
		}
		// Second item: credit
		if results[1].Cost != -100.0 {
			t.Errorf("expected credit cost -100.0, got %f", results[1].Cost)
		}
		if results[1].CostCategory != "Credit" {
			t.Errorf("expected credit CostCategory 'Credit', got %v", results[1].CostCategory)
		}
	})
}

func TestAggregateDailyBilling(t *testing.T) {
	baseDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		items    []providers.UsageReportItem
		expected int // expected number of aggregated items
	}{
		{
			name:     "empty items",
			items:    []providers.UsageReportItem{},
			expected: 0,
		},
		{
			name: "single item",
			items: []providers.UsageReportItem{
				{
					ProductCode:        "Compute Engine",
					ResourceRegionCode: "us-central1",
					ResourceType:       "compute-engine",
					ResourceId:         "test-instance",
					StartDate:          baseDate.Add(2 * time.Hour),
					Cost:               0.1,
				},
			},
			expected: 1,
		},
		{
			name: "multiple items same day same resource",
			items: []providers.UsageReportItem{
				{
					ProductCode:        "Compute Engine",
					ResourceRegionCode: "us-central1",
					ResourceType:       "compute-engine",
					ResourceId:         "test-instance",
					StartDate:          baseDate.Add(2 * time.Hour),
					Cost:               0.1,
				},
				{
					ProductCode:        "Compute Engine",
					ResourceRegionCode: "us-central1",
					ResourceType:       "compute-engine",
					ResourceId:         "test-instance",
					StartDate:          baseDate.Add(4 * time.Hour),
					Cost:               0.2,
				},
			},
			expected: 1,
		},
		{
			name: "multiple items different days",
			items: []providers.UsageReportItem{
				{
					ProductCode:        "Compute Engine",
					ResourceRegionCode: "us-central1",
					ResourceType:       "compute-engine",
					ResourceId:         "test-instance",
					StartDate:          baseDate.Add(2 * time.Hour),
					Cost:               0.1,
				},
				{
					ProductCode:        "Compute Engine",
					ResourceRegionCode: "us-central1",
					ResourceType:       "compute-engine",
					ResourceId:         "test-instance",
					StartDate:          baseDate.Add(24 * time.Hour).Add(2 * time.Hour),
					Cost:               0.2,
				},
			},
			expected: 2,
		},
		{
			name: "multiple items different resources",
			items: []providers.UsageReportItem{
				{
					ProductCode:        "Compute Engine",
					ResourceRegionCode: "us-central1",
					ResourceType:       "compute-engine",
					ResourceId:         "test-instance-1",
					StartDate:          baseDate.Add(2 * time.Hour),
					Cost:               0.1,
				},
				{
					ProductCode:        "Compute Engine",
					ResourceRegionCode: "us-central1",
					ResourceType:       "compute-engine",
					ResourceId:         "test-instance-2",
					StartDate:          baseDate.Add(2 * time.Hour),
					Cost:               0.2,
				},
			},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _ := aggregateDailyBilling(tt.items)

			if len(result) != tt.expected {
				t.Errorf("expected %d aggregated items, got %d", tt.expected, len(result))
			}

			// If we expect aggregation, check that costs are summed correctly
			if tt.name == "multiple items same day same resource" && len(result) == 1 {
				expectedCost := 0.3
				if result[0].Cost-expectedCost > 0.000001 || expectedCost-result[0].Cost > 0.000001 {
					t.Errorf("expected aggregated cost %f, got %f", expectedCost, result[0].Cost)
				}
			}
		})
	}
}

// Helper function to create string pointer
func stringPtr(s string) *string {
	return &s
}

func TestGetGcloudUsageReports(t *testing.T) {
	// Create a "no-op" slog logger that discards all output for clean test runs.

	startTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		account       providers.Account
		setupMock     func(account providers.Account)
		expectError   bool
		expectedItems int
		expectedCost  float64
	}{
		{
			name: "successful report generation",
			account: providers.Account{
				AccountNumber: "431c4781-937c-4f02-bb90-c47bc0201c79",
				Data: stringPtr(`{
					"billing_data": {
						"billing_project_id": "nudgebee-dev",
						"dataset_name": "billing_export",
						"table_name": "gcp_billing_export_resource_v1_01766B_B907EB_02180F"
					}
				}`),
			},

			setupMock: func(account providers.Account) {
				mockRows := []models.BigQueryBillingRow{
					{ServiceName: "Compute Engine", SKUDescription: "N1 Core", UsageStartTime: startTime, UsageEndTime: startTime.Add(time.Hour), ProjectID: bigquery.NullString{StringVal: "test-project", Valid: true}, Region: bigquery.NullString{StringVal: "us-central1", Valid: true}, Cost: 1.23, Currency: "USD", CostType: "REGULAR"},
					{ServiceName: "Compute Engine", SKUDescription: "N1 Core", UsageStartTime: startTime.Add(2 * time.Hour), UsageEndTime: startTime.Add(3 * time.Hour), ProjectID: bigquery.NullString{StringVal: "test-project", Valid: true}, Region: bigquery.NullString{StringVal: "us-central1", Valid: true}, Cost: 1.50, Currency: "USD", CostType: "REGULAR"},
				}
				streamBigQueryBilling = mockStreamFromRows(mockRows, nil)
			},
			expectError:   false,
			expectedItems: 1,
			expectedCost:  2.73,
		},
		{
			name: "error from invalid billing config",
			account: providers.Account{
				AccountNumber: "gcp-test-account-invalid-config",
				Data:          stringPtr(`{}`), // Missing required fields
			},
			setupMock:   nil, // No mock needed as it should fail before querying
			expectError: true,
		},
		{
			name: "error from BigQuery query",
			account: providers.Account{
				AccountNumber: "gcp-test-account-bq-error",
				Data: stringPtr(`{
					"billing_data": {
						"billing_project_id": "test-project",
						"dataset_name": "test_dataset",
						"table_name": "test_table"
					}
				}`),
			},
			setupMock: func(account providers.Account) {
				streamBigQueryBilling = mockStreamFromRows(nil, errors.New("bigquery query failed"))
			},
			expectError: true,
		},
		{
			//"billing_dataset_id": "abhay_test_1760341304538",
			name: "live integration test with real credentials",
			account: providers.Account{
				AccountNumber: "431c4781-937c-4f02-bb90-c47bc0201c79",
				Data: stringPtr(`{
					"billing_data": {
						"billing_project_id": "nudgebee-dev",
						"dataset_name": "billing_export",
						"table_name": "gcp_billing_export_v1_01766B_B907EB_02180F"
					}
				}`),
			},
			setupMock: func(account providers.Account) {
				// This is a live test, so we use the real implementation.
				// All setup is now handled in the main test body.
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Store original function and restore it after the test
			originalQueryFunc := streamBigQueryBilling
			originalNewClientFunc := newBigQueryClient
			defer func() {
				streamBigQueryBilling = originalQueryFunc
				newBigQueryClient = originalNewClientFunc
			}()

			// Skip live integration tests unless explicitly enabled
			if tt.name == "live integration test with real credentials" && os.Getenv("RUN_GCP_INTEGRATION_TESTS") != "true" {
				t.Skip("Skipping live integration test. Set RUN_GCP_INTEGRATION_TESTS=true to run.")
			}

			// Setup the mock for this specific test case
			if tt.setupMock != nil {
				tt.setupMock(tt.account)
			}

			// Default context for non-live tests
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			var providerCtx providers.CloudProviderContext
			if tt.name != "live integration test with real credentials" {
				providerCtx = security.NewRequestContext(context.Background(), nil, logger, nil, nil)
			} else {
				logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
				securityCtx := security.NewSecurityContextForSuperAdmin()
				providerCtx = security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

				originalNewClient := newBigQueryClient
				newBigQueryClient = func(ctx context.Context, projectID string, opts ...option.ClientOption) (bigQueryClient, error) {
					// Prefer explicit credentials if provided on the account
					if tt.account.AccessSecret != nil && *tt.account.AccessSecret != "" {
						session, err := getGcloudSessionFromAccount(providerCtx, tt.account)
						if err != nil {
							return nil, fmt.Errorf("live test failed to get session: %w", err)
						}
						if session.AccountCred == "" {
							return nil, fmt.Errorf("missing AccountCred")
						}
						opts = append(opts, option.WithAuthCredentialsJSON(
							option.CredentialsType("service_account"),
							[]byte(session.AccountCred),
						))
					} else if credPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); credPath != "" {
						content, err := os.ReadFile(credPath)
						if err != nil {
							return nil, fmt.Errorf("failed to read credentials file: %w", err)
						}
						// Next, use GOOGLE_APPLICATION_CREDENTIALS if set
						opts = append(opts, option.WithAuthCredentialsJSON(
							option.CredentialsType("service_account"),
							content,
						))
					} // else rely on ADC with no extra options
					return originalNewClient(ctx, projectID, opts...)
				}
			}

			// Run the function
			resp, err := getGcloudUsageReport(providerCtx, tt.account, time.January, 2024)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected an error but got none")
				}
				return // Test is done for error cases
			}

			if err != nil {
				t.Fatalf("getGcloudUsageReport() unexpected error: %v", err)
			}

			// For live integration tests, print detailed billing summary
			if tt.name == "live integration test with real credentials" {
				fmt.Printf("\n=== GCP BILLING REPORT (January 2024) ===\n")
				fmt.Printf("Account: %s\n", tt.account.AccountNumber)
				fmt.Printf("Total line items: %d\n\n", len(resp.Items))

				// Calculate totals by service
				serviceTotals := make(map[string]float64)
				var totalCost float64

				for _, item := range resp.Items {
					serviceTotals[item.ProductCode] += item.Cost
					totalCost += item.Cost
				}

				fmt.Printf("=== COST SUMMARY BY SERVICE ===\n")
				for service, cost := range serviceTotals {
					fmt.Printf("  %-30s: $ %.2f\n", service, cost)
				}
				fmt.Printf("  %s\n", strings.Repeat("-", 50))
				fmt.Printf("  %-30s: $ %.2f\n\n", "Total Cost", totalCost)

				fmt.Printf("=== DETAILED BILLING ITEMS ===\n")
				for i, item := range resp.Items {
					fmt.Printf("\nItem #%d:\n", i+1)
					fmt.Printf("  Service: %s\n", item.ProductCode)
					fmt.Printf("  SKU: %s\n", item.ProductServiceCode)
					fmt.Printf("  Resource ID: %s\n", item.ResourceId)
					fmt.Printf("  Region: %s\n", item.ResourceRegionCode)
					fmt.Printf("  Cost: $ %.2f %s\n", item.Cost, item.CostCurrency)
					fmt.Printf("  Category: %s\n", item.CostCategory)
					fmt.Printf("  Date: %s\n", item.StartDate.Format("2006-01-02"))
					if len(item.ResourceTags) > 0 {
						fmt.Printf("  Tags: %v\n", item.ResourceTags)
					}
				}

				t.Logf("Successfully fetched %d billing items with total cost $ %.2f", len(resp.Items), totalCost)
			} else {
				// For mocked tests, we can be more specific
				if len(resp.Items) != tt.expectedItems {
					t.Fatalf("expected %d aggregated item(s), got %d", tt.expectedItems, len(resp.Items))
				}
				if tt.expectedItems > 0 {
					// Use a tolerance for float comparison
					if (resp.Items[0].Cost - tt.expectedCost) > 0.000001 {
						t.Errorf("expected aggregated cost to be %f, got %f", tt.expectedCost, resp.Items[0].Cost)
					}
				}
			}
		})
	}
}

// mockStreamFromRows returns a streamBigQueryBilling stand-in that hands the
// supplied rows to the processor one at a time, mirroring production semantics
// without holding the test fixture in memory.
func mockStreamFromRows(rows []models.BigQueryBillingRow, queryErr error) func(providers.CloudProviderContext, models.BillingConfig, time.Time, time.Time, providers.Account, func(models.BigQueryBillingRow) error) (bigQueryQueryStats, error) {
	return func(_ providers.CloudProviderContext, _ models.BillingConfig, _, _ time.Time, _ providers.Account, process func(models.BigQueryBillingRow) error) (bigQueryQueryStats, error) {
		stats := bigQueryQueryStats{
			ServiceMap:  make(map[string]float64),
			CurrencyMap: make(map[string]int),
		}
		if queryErr != nil {
			return stats, queryErr
		}
		for i, row := range rows {
			stats.RowCount++
			stats.ServiceMap[row.ServiceName] += row.Cost
			stats.CurrencyMap[row.Currency]++
			if row.CreditAmount.Valid {
				stats.CreditCount++
				stats.TotalCreditAmount += row.CreditAmount.Float64
			}
			if i == 0 {
				stats.FirstDate = row.UsageStartTime
			}
			stats.LastDate = row.UsageStartTime
			if err := process(row); err != nil {
				return stats, err
			}
		}
		return stats, nil
	}
}

// mockBigQueryClient implements the bigQueryClient interface for testing.
type mockBigQueryClient struct {
	QueryFunc    func(q string) *bigquery.Query
	QueryReadErr error // To simulate errors during the Read call
}

func (m *mockBigQueryClient) Query(q string) *bigquery.Query {
	if m.QueryFunc != nil {
		return m.QueryFunc(q)
	}
	return nil
}

func (m *mockBigQueryClient) Close() error {
	return nil // No-op for mock
}

func TestGetGcloudUsageReport(t *testing.T) {
	// 1. Setup Mock Account Data
	accountData := map[string]any{
		"billing_data": map[string]string{
			"dataset_name": "test_dataset",
			"table_name":   "test_table",
		},
	}
	accountDataJSON, _ := json.Marshal(accountData)
	accountDataStr := string(accountDataJSON)

	account := providers.Account{
		AccountNumber: "test-gcp-project",
		Data:          &accountDataStr,
	}

	// 2. Setup Mock BigQuery Response
	mockRows := []models.BigQueryBillingRow{
		{
			ServiceName:    "Compute Engine",
			SKUDescription: "N1 Standard 1",
			UsageStartTime: time.Date(2023, 1, 1, 10, 0, 0, 0, time.UTC),
			UsageEndTime:   time.Date(2023, 1, 1, 11, 0, 0, 0, time.UTC),
			ProjectID:      bigquery.NullString{StringVal: "test-gcp-project", Valid: true},
			Region:         bigquery.NullString{StringVal: "us-central1", Valid: true},
			Cost:           15.50,
			Currency:       "USD",
		},
	}

	// 3. Override the newBigQueryClient factory to return our mock client
	originalNewBigQueryClient := newBigQueryClient
	defer func() { newBigQueryClient = originalNewBigQueryClient }()

	newBigQueryClient = func(ctx context.Context, projectID string, opts ...option.ClientOption) (bigQueryClient, error) {
		return &mockBigQueryClient{
			QueryFunc: func(q string) *bigquery.Query {
				// The bigquery.Query struct is unexported, so we can't create a mock that satisfies it directly.
				// Instead, we can use a real Query object and override its Read method for testing.
				// This is a bit of a workaround due to the library's design.
				// A more robust solution would be to define interfaces for Query and RowIterator.
				// For now, we will mock the entire queryBigQueryBilling function.
				return nil // This part is tricky to mock directly.
			},
		}, nil
	}

	// Since mocking the chain of client -> query -> iterator is complex,
	// we'll mock the higher-level `streamBigQueryBilling` function directly.
	originalStream := streamBigQueryBilling
	defer func() { streamBigQueryBilling = originalStream }()
	streamBigQueryBilling = mockStreamFromRows(mockRows, nil)

	// 4. Setup Test Context
	ctx := providers.NewCloudProviderContext(context.Background())

	// 5. Call the function we want to test
	resp, err := getGcloudUsageReport(ctx, account, time.January, 2023)

	// 6. Assert the results
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp.Items, 1) // Expect 1 aggregated item
	assert.Equal(t, "Compute Engine", resp.Items[0].ProductCode)
	assert.Equal(t, 15.50, resp.Items[0].Cost)
	assert.Equal(t, "us-central1", resp.Items[0].ResourceRegionCode)
}

// Optional: Add a test for when query fails
func TestGetGcloudUsageReport_QueryFailure(t *testing.T) {
	// 1. Setup Mock Account Data
	accountData := map[string]any{
		"billing_data": map[string]string{
			"dataset_name": "test_dataset",
			"table_name":   "test_table",
		},
	}
	accountDataJSON, _ := json.Marshal(accountData)
	accountDataStr := string(accountDataJSON)

	account := providers.Account{
		AccountNumber: "test-gcp-project",
		Data:          &accountDataStr,
	}

	// 2. Override the client factory to simulate an error
	// Mock the higher-level function to return an error.
	originalStream := streamBigQueryBilling
	defer func() { streamBigQueryBilling = originalStream }()
	streamBigQueryBilling = mockStreamFromRows(nil, errors.New("mock bigquery error: table not found"))

	// 3. Setup Test Context
	ctx := providers.NewCloudProviderContext(context.Background())

	// 4. Call the function and assert an error is returned
	_, err := getGcloudUsageReport(ctx, account, time.January, 2023)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mock bigquery error")
}

// TestGetGcloudUsageReport_CurrentMonth tests fetching billing data for the current month (or recent months)
func TestGetGcloudUsageReport_CurrentMonth(t *testing.T) {
	if os.Getenv("RUN_GCP_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping live integration test. Set RUN_GCP_INTEGRATION_TESTS=true to run.")
	}

	// Get account from integration test helper
	account := getTestAccountForBilling(t)

	// Setup Test Context
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	securityCtx := security.NewSecurityContextForSuperAdmin()
	ctx := security.NewRequestContext(context.Background(), securityCtx, logger, nil, nil)

	// Test for multiple recent months - uses current year from system clock
	currentYear := time.Now().Year()
	testMonths := []struct {
		month time.Month
		year  int
		name  string
	}{
		{time.November, currentYear, fmt.Sprintf("November %d", currentYear)},
		{time.October, currentYear, fmt.Sprintf("October %d", currentYear)},
		{time.September, currentYear, fmt.Sprintf("September %d", currentYear)},
	}

	for _, tm := range testMonths {
		t.Run(tm.name, func(t *testing.T) {
			resp, err := getGcloudUsageReport(ctx, account, tm.month, tm.year)
			if err != nil {
				t.Logf("Warning: Failed to fetch billing data for %s: %v", tm.name, err)
				return
			}

			fmt.Printf("\n=== GCP BILLING REPORT (%s) ===\n", tm.name)
			fmt.Printf("Account: %s\n", account.AccountNumber)
			fmt.Printf("Total line items: %d\n\n", len(resp.Items))

			if len(resp.Items) == 0 {
				fmt.Printf("No billing data found for %s\n", tm.name)
				return
			}

			// Calculate totals by service
			serviceTotals := make(map[string]float64)
			regionTotals := make(map[string]float64)
			var totalCost float64

			for _, item := range resp.Items {
				serviceTotals[item.ProductCode] += item.Cost
				regionTotals[item.ResourceRegionCode] += item.Cost
				totalCost += item.Cost
			}

			fmt.Printf("=== COST SUMMARY BY SERVICE ===\n")
			for service, cost := range serviceTotals {
				fmt.Printf("  %-30s: $ %.2f\n", service, cost)
			}
			fmt.Printf("  %s\n", strings.Repeat("-", 50))
			fmt.Printf("  %-30s: $ %.2f\n\n", "Total Cost", totalCost)

			fmt.Printf("=== COST SUMMARY BY REGION ===\n")
			for region, cost := range regionTotals {
				fmt.Printf("  %-30s: $ %.2f\n", region, cost)
			}
			fmt.Printf("\n")

			fmt.Printf("=== TOP 10 BILLING ITEMS ===\n")
			// Sort items by cost (descending) and show top 10
			itemsCount := len(resp.Items)
			if itemsCount > 10 {
				itemsCount = 10
			}

			for i := 0; i < itemsCount; i++ {
				item := resp.Items[i]
				fmt.Printf("\nItem #%d:\n", i+1)
				fmt.Printf("  Service: %s\n", item.ProductCode)
				fmt.Printf("  SKU: %s\n", item.ProductServiceCode)
				fmt.Printf("  Resource ID: %s\n", item.ResourceId)
				fmt.Printf("  Region: %s\n", item.ResourceRegionCode)
				fmt.Printf("  Cost: $ %.2f %s\n", item.Cost, item.CostCurrency)
				fmt.Printf("  Date: %s\n", item.StartDate.Format("2006-01-02"))
			}

			t.Logf("Successfully fetched %d billing items with total cost $ %.2f for %s", len(resp.Items), totalCost, tm.name)
		})
	}
}

// Helper function to get test account for billing tests
func getTestAccountForBilling(t *testing.T) providers.Account {
	accountData := `{
		"billing_data": {
			"billing_project_id": "nudgebee-dev",
			"dataset_name": "billing_export",
			"table_name": "gcp_billing_export_resource_v1_01766B_B907EB_02180F"
		}
	}`

	return providers.Account{
		AccountNumber: "431c4781-937c-4f02-bb90-c47bc0201c79",
		AccountName:   "nudgebee-dev",
		Data:          &accountData,
	}
}
