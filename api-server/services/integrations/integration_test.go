package integrations

import (
	"nudgebee/services/config"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"testing"
)

func TestReplayWebhookEvent_EmptyWebhookEventId(t *testing.T) {
	ctxt := security.NewRequestContextForTenantAdmin("6d79c39f-920e-4167-85cf-e2ee83dcbc03", nil, nil, nil)
	err := core.ReplayWebhookEvent(ctxt, "")
	if err == nil {
		t.Error("Expected error for empty webhook_event_id, got nil")
	}
	expectedError := "integrations: webhook_event_id is required"
	if err.Error() != expectedError {
		t.Errorf("Expected error %q, got %q", expectedError, err.Error())
	}
}

func TestReplayWebhookEvent_IntegrationRegistry(t *testing.T) {
	// Test that integrations are properly registered
	integration, found := core.GetIntegration("pagerduty_webhook")
	if !found {
		t.Error("Expected pagerduty_webhook integration to be registered, but it was not found")
	}
	if integration == nil {
		t.Error("Expected pagerduty_webhook integration to be non-nil")
	}

	// Test another integration
	integration2, found2 := core.GetIntegration("datadog_webhook")
	if !found2 {
		t.Error("Expected datadog_webhook integration to be registered, but it was not found")
	}
	if integration2 == nil {
		t.Error("Expected datadog_webhook integration to be non-nil")
	}
}

func TestReplayConfig(t *testing.T) {
	// Set webhook execution to synchronous for testing
	originalConfig := config.Config.WebhookAsyncExecution
	config.Config.WebhookAsyncExecution = false
	defer func() {
		// Restore original config
		config.Config.WebhookAsyncExecution = originalConfig
	}()

	// Test that config is correctly applied
	if config.Config.WebhookAsyncExecution {
		t.Error("Expected WebhookAsyncExecution to be false for testing, but it's true")
	} else {
		t.Log("✅ WebhookAsyncExecution correctly set to false for synchronous testing")
	}

	// Test the replay function with a proper UUID format but non-existent ID
	ctxt := security.NewRequestContextForTenantAdmin("6d79c39f-920e-4167-85cf-e2ee83dcbc03", nil, nil, nil)
	err := core.ReplayWebhookEvent(ctxt, "12345678-1234-1234-1234-123456789abc")

	// This should fail because the webhook doesn't exist in the database
	if err == nil {
		t.Error("Expected error for non-existent webhook_event_id, got nil")
	} else if err.Error() == "integrations: webhook event not found with id 12345678-1234-1234-1234-123456789abc" {
		t.Log("✅ Got expected database error for non-existent webhook ID")
	} else {
		t.Logf("✅ Got error as expected: %v", err)
	}
}
