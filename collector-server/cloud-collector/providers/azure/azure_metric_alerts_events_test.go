package azure

import (
	"context"
	"encoding/json"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetAzureAlerts(t *testing.T) {
	// Skip if not running integration tests
	if os.Getenv("RUN_AZURE_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_AZURE_INTEGRATION_TESTS=true to run.")
	}

	// Get test Azure account with credentials
	account := getMetricAlertsTestAccount()

	ctx := providers.NewCloudProviderContext(context.Background())

	t.Run("Get all alerts", func(t *testing.T) {
		filter := AlertsFilter{}

		response, err := getAzureAlerts(ctx, account, filter)
		assert.NoError(t, err)
		assert.NotNil(t, response)

		t.Logf("Found %d fired alert events", len(response.Items))

		// Log first event details if any
		if len(response.Items) > 0 {
			event := response.Items[0]
			t.Logf("Sample event: %s (Source: %s, Severity: %s, Status: %s)",
				event.EventName, event.EventSource, event.EventSeverity, event.EventStatus)
		}
	})

	t.Run("Filter by service name", func(t *testing.T) {
		filter := AlertsFilter{
			ServiceName: "microsoft.compute/virtualmachines",
		}

		response, err := getAzureAlerts(ctx, account, filter)
		assert.NoError(t, err)
		assert.NotNil(t, response)

		t.Logf("Found %d VM-related alerts", len(response.Items))
	})

	t.Run("Filter by region", func(t *testing.T) {
		filter := AlertsFilter{
			Region: "eastus",
		}

		response, err := getAzureAlerts(ctx, account, filter)
		assert.NoError(t, err)
		assert.NotNil(t, response)

		t.Logf("Found %d alerts in East US", len(response.Items))

		for _, event := range response.Items {
			assert.Equal(t, "eastus", event.ResourceRegion)
		}
	})
}

func TestAzureProviderListEvents(t *testing.T) {
	// Skip if not running integration tests
	if os.Getenv("RUN_AZURE_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_AZURE_INTEGRATION_TESTS=true to run.")
	}

	account := getMetricAlertsTestAccount()

	provider := &azureProvider{}
	ctx := providers.NewCloudProviderContext(context.Background())

	t.Run("List all events", func(t *testing.T) {
		query := providers.ListEventRequest{}

		response, err := provider.ListEvents(ctx, account, query)
		assert.NoError(t, err)
		assert.NotNil(t, response)

		t.Logf("Found %d events", len(response.Items))
		t.Logf("Summary has %d entries", len(response.Summary))

		// Verify summary structure
		for _, summary := range response.Summary {
			t.Logf("Service: %s, Region: %s, Updates: %d",
				summary.ServiceName, summary.Region, summary.ResourceUpdated)
		}
	})

	t.Run("List events by service", func(t *testing.T) {
		query := providers.ListEventRequest{
			ServiceNames: []string{"microsoft.compute/virtualmachines"},
		}

		response, err := provider.ListEvents(ctx, account, query)
		assert.NoError(t, err)
		assert.NotNil(t, response)

		t.Logf("Found %d VM events", len(response.Items))

		for _, event := range response.Items {
			assert.Equal(t, "microsoft.compute/virtualmachines", event.ResourceServiceName)
		}
	})
}

func TestAzureProviderListEventRules(t *testing.T) {
	// Skip if not running integration tests
	if os.Getenv("RUN_AZURE_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_AZURE_INTEGRATION_TESTS=true to run.")
	}

	account := getMetricAlertsTestAccount()

	provider := &azureProvider{}
	ctx := providers.NewCloudProviderContext(context.Background())

	response, err := provider.ListEventRules(ctx, account)
	assert.NoError(t, err)
	assert.NotNil(t, response)

	t.Logf("Found %d metric alert rules", len(response.Items))

	// Verify rule structure
	for i, rule := range response.Items {
		if i < 5 { // Log first 5 rules
			t.Logf("Rule: %s (Severity: %s, Source: %s)",
				rule.Name, rule.Severity, rule.Source)
		}
		assert.NotEmpty(t, rule.Name)
		assert.NotEmpty(t, rule.Summary)
		assert.NotEmpty(t, rule.Source)
		assert.NotNil(t, rule.Labels)
	}
}

func TestBuildAlertRawMap(t *testing.T) {
	// Skip if not running integration tests
	if os.Getenv("RUN_AZURE_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration test. Set RUN_AZURE_INTEGRATION_TESTS=true to run.")
	}

	account := getMetricAlertsTestAccount()
	ctx := providers.NewCloudProviderContext(context.Background())

	response, err := getAzureAlerts(ctx, account, AlertsFilter{})
	assert.NoError(t, err)

	t.Logf("Checking Raw map structure for %d events", len(response.Items))

	for i, event := range response.Items {
		assert.NotNil(t, event.Raw, "event %d Raw should not be nil", i)

		// Verify top-level keys exist
		assert.Contains(t, event.Raw, "id", "event %d Raw missing 'id'", i)
		assert.Contains(t, event.Raw, "name", "event %d Raw missing 'name'", i)
		assert.Contains(t, event.Raw, "essentials", "event %d Raw missing 'essentials'", i)

		// Verify essentials sub-map
		ess, ok := event.Raw["essentials"].(map[string]any)
		assert.True(t, ok, "event %d essentials should be a map", i)
		if ok {
			// These should be present from GetByID enrichment
			if _, hasSeverity := ess["severity"]; hasSeverity {
				assert.IsType(t, "", ess["severity"])
			}
			if _, hasCond := ess["monitorCondition"]; hasCond {
				assert.IsType(t, "", ess["monitorCondition"])
			}
		}

		// Verify Raw is JSON-serializable
		rawJSON, err := json.Marshal(event.Raw)
		assert.NoError(t, err, "event %d Raw should be JSON-serializable", i)
		t.Logf("Event %d Raw size: %d bytes", i, len(rawJSON))

		if i >= 4 {
			break // sample first 5 events
		}
	}
}

// Helper function to get test Azure account for metric alerts
func getMetricAlertsTestAccount() providers.Account {
	// Azure account structure mapping:
	// AccountNumber -> TenantID
	// AssumeRole    -> SubscriptionID
	// AccessKey     -> ClientID
	// AccessSecret  -> ClientSecret (must be encrypted)

	// Read from environment variables
	subscriptionID := os.Getenv("AZURE_SUBSCRIPTION_ID")
	tenantID := os.Getenv("AZURE_TENANT_ID")
	clientID := os.Getenv("AZURE_CLIENT_ID")
	clientSecretPlain := os.Getenv("AZURE_CLIENT_SECRET")

	if subscriptionID == "" || tenantID == "" || clientID == "" || clientSecretPlain == "" {
		return providers.Account{}
	}

	// Encrypt the client secret
	encryptedSecret, err := common.Encrypt(clientSecretPlain)
	if err != nil {
		encryptedSecret = clientSecretPlain
	}

	return providers.Account{
		AccountNumber: tenantID,
		AssumeRole:    &subscriptionID,
		AccessKey:     &clientID,
		AccessSecret:  &encryptedSecret,
	}
}
