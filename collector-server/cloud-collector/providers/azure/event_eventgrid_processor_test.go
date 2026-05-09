package azure

import (
	"context"
	"encoding/json"
	"log/slog"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"os"
	"testing"
	"time"
)

// MockCloudProviderContext implements providers.CloudProviderContext for testing
type MockCloudProviderContext struct{}

func (m *MockCloudProviderContext) GetContext() context.Context {
	return context.Background()
}

func (m *MockCloudProviderContext) GetLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, nil))
}

func (m *MockCloudProviderContext) GetSecurityContext() *security.SecurityContext {
	// Return nil for tests - tests don't need security context
	return nil
}

// MockAzureProvider implements azureProviderAPI for testing
type MockAzureProvider struct {
	queryLogsResponse     providers.QueryLogsResponse
	queryMetricsResponse  providers.QueryMetricsResponse
	listResourcesResponse providers.ListResourcesResponse
}

func (m *MockAzureProvider) QueryLogs(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryLogsRequest) (providers.QueryLogsResponse, error) {
	return m.queryLogsResponse, nil
}

func (m *MockAzureProvider) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return m.queryMetricsResponse, nil
}

func (m *MockAzureProvider) ListResources(ctx providers.CloudProviderContext, account providers.Account, query providers.ListResourceRequest) (providers.ListResourcesResponse, error) {
	return m.listResourcesResponse, nil
}

// TestRuleMatching tests rule trigger matching logic
func TestRuleMatching(t *testing.T) {
	mockProvider := &MockAzureProvider{}
	processor := NewTemplatedEventGridProcessor([]AzureEventRule{}, mockProvider)

	tests := []struct {
		name        string
		rule        AzureEventRule
		event       EventGridEvent
		shouldMatch bool
		description string
	}{
		{
			name: "VM write operation matches",
			rule: AzureEventRule{
				Name: "vm_state_change",
				Triggers: AzureEventRuleTrigger{
					SourceSystem: "Azure_EventGrid",
					EventFilters: []AzureEventFilter{
						{Template: `{{ eq .EventType "Microsoft.Resources.ResourceWriteSuccess" }}`},
						{Template: `{{ contains .Data.operationName "Microsoft.Compute/virtualMachines" }}`},
					},
				},
			},
			event: EventGridEvent{
				ID:        "test-1",
				EventType: "Microsoft.Resources.ResourceWriteSuccess",
				Subject:   "/subscriptions/12345/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1",
				EventTime: time.Now(),
				Data:      json.RawMessage(`{"operationName": "Microsoft.Compute/virtualMachines/start/action"}`),
			},
			shouldMatch: true,
			description: "Event type matches and operationName contains VM operations",
		},
		{
			name: "Wrong event type does not match",
			rule: AzureEventRule{
				Name: "vm_state_change",
				Triggers: AzureEventRuleTrigger{
					SourceSystem: "Azure_EventGrid",
					EventFilters: []AzureEventFilter{
						{Template: `{{ eq .EventType "Microsoft.Resources.ResourceWriteSuccess" }}`},
					},
				},
			},
			event: EventGridEvent{
				ID:        "test-2",
				EventType: "Microsoft.Resources.ResourceDeleteSuccess",
				Subject:   "/subscriptions/12345/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1",
				EventTime: time.Now(),
				Data:      json.RawMessage(`{"operationName": "Microsoft.Compute/virtualMachines/delete"}`),
			},
			shouldMatch: false,
			description: "Event type is Delete, not Write",
		},
		{
			name: "Storage operation matches",
			rule: AzureEventRule{
				Name: "storage_operation",
				Triggers: AzureEventRuleTrigger{
					SourceSystem: "Azure_EventGrid",
					EventFilters: []AzureEventFilter{
						{Template: `{{ contains .Data.operationName "Microsoft.Storage/storageAccounts" }}`},
					},
				},
			},
			event: EventGridEvent{
				ID:        "test-3",
				EventType: "Microsoft.Resources.ResourceWriteSuccess",
				Subject:   "/subscriptions/12345/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/storage1",
				EventTime: time.Now(),
				Data:      json.RawMessage(`{"operationName": "Microsoft.Storage/storageAccounts/write"}`),
			},
			shouldMatch: true,
			description: "Storage account operation matches filter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var eventData map[string]interface{}
			_ = json.Unmarshal(tt.event.Data, &eventData)

			templateContext := map[string]interface{}{
				"ID":              tt.event.ID,
				"EventType":       tt.event.EventType,
				"Subject":         tt.event.Subject,
				"EventTime":       tt.event.EventTime,
				"Data":            eventData,
				"DataVersion":     tt.event.DataVersion,
				"MetadataVersion": tt.event.MetadataVersion,
				"Topic":           tt.event.Topic,
			}

			ctx := &MockCloudProviderContext{}
			matched, err := processor.ruleMatches(ctx, tt.rule, templateContext)
			if err != nil {
				t.Errorf("ruleMatches returned error: %v", err)
			}

			if matched != tt.shouldMatch {
				t.Errorf("%s: expected match=%v, got match=%v", tt.description, tt.shouldMatch, matched)
			}
		})
	}
}

// TestTemplateEvaluation tests template field evaluation
func TestTemplateEvaluation(t *testing.T) {
	mockProvider := &MockAzureProvider{}
	processor := NewTemplatedEventGridProcessor([]AzureEventRule{}, mockProvider)

	tests := []struct {
		name          string
		template      AzureEventFieldTemplate
		context       map[string]interface{}
		expectedValue string
		description   string
	}{
		{
			name: "Static value",
			template: AzureEventFieldTemplate{
				Value: "microsoft.compute/virtualmachines",
			},
			context:       map[string]interface{}{},
			expectedValue: "microsoft.compute/virtualmachines",
			description:   "Should return static value as-is",
		},
		{
			name: "Template with variable substitution",
			template: AzureEventFieldTemplate{
				Template: `{{ .Data.resourceUri }}`,
			},
			context: map[string]interface{}{
				"Data": map[string]interface{}{
					"resourceUri": "/subscriptions/12345/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1",
				},
			},
			expectedValue: "/subscriptions/12345/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1",
			description:   "Should substitute variable from context",
		},
		{
			name: "Template with function call",
			template: AzureEventFieldTemplate{
				Template: `{{ .Data.resourceUri | basename }}`,
			},
			context: map[string]interface{}{
				"Data": map[string]interface{}{
					"resourceUri": "/subscriptions/12345/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1",
				},
			},
			expectedValue: "vm1",
			description:   "Should apply basename function to extract resource name",
		},
		{
			name: "Template with toLower function",
			template: AzureEventFieldTemplate{
				Template: `{{ .Data.region | toLower }}`,
			},
			context: map[string]interface{}{
				"Data": map[string]interface{}{
					"region": "EastUS",
				},
			},
			expectedValue: "eastus",
			description:   "Should convert region to lowercase",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &MockCloudProviderContext{}
			result, err := processor.evaluateFieldTemplate(ctx, tt.template, tt.context)
			if err != nil {
				t.Errorf("evaluateFieldTemplate returned error: %v", err)
			}

			if result != tt.expectedValue {
				t.Errorf("%s: expected '%s', got '%s'", tt.description, tt.expectedValue, result)
			}
		})
	}
}

// TestEvaluateTemplateValueRecursion verifies that template evaluation walks into
// nested maps and slices. Without this, action params like
//
//	meta_updates:
//	  last_operation: "{{ .Data.operationName }}"
//
// were stored verbatim in cloud_resourses.meta as the literal string
// "{{ .Data.operationName }}" because the previous executeAction loop only
// looked at top-level string values.
func TestEvaluateTemplateValueRecursion(t *testing.T) {
	processor := NewTemplatedEventGridProcessor([]AzureEventRule{}, &MockAzureProvider{})
	ctx := &MockCloudProviderContext{}

	templateContext := map[string]interface{}{
		"EventTime": "2026-04-30T05:10:16Z",
		"Data": map[string]interface{}{
			"operationName": "Microsoft.Sql/managedInstances/write",
			"status":        "Succeeded",
		},
	}

	params := map[string]interface{}{
		"resource_id": "{{ .Data.operationName }}",
		"new_status":  "Active",
		"update_meta": true,
		"meta_updates": map[string]interface{}{
			"last_operation":      "{{ .Data.operationName }}",
			"last_operation_time": "{{ .EventTime }}",
			"operation_status":    "{{ .Data.status }}",
		},
		"status_mapping": []interface{}{
			"{{ .Data.status }}",
			"static_value",
		},
	}

	out := processor.evaluateTemplateValue(ctx, params, templateContext)
	got, ok := out.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", out)
	}

	if v := got["resource_id"].(string); v != "Microsoft.Sql/managedInstances/write" {
		t.Errorf("top-level template not rendered: got %q", v)
	}
	if v := got["new_status"].(string); v != "Active" {
		t.Errorf("plain string mutated: got %q", v)
	}
	if v := got["update_meta"].(bool); v != true {
		t.Errorf("non-string scalar lost: got %v", v)
	}

	meta, ok := got["meta_updates"].(map[string]interface{})
	if !ok {
		t.Fatalf("nested map collapsed to %T", got["meta_updates"])
	}
	if v := meta["last_operation"].(string); v != "Microsoft.Sql/managedInstances/write" {
		t.Errorf("nested meta_updates.last_operation not rendered: got %q", v)
	}
	if v := meta["last_operation_time"].(string); v != "2026-04-30T05:10:16Z" {
		t.Errorf("nested meta_updates.last_operation_time not rendered: got %q", v)
	}
	if v := meta["operation_status"].(string); v != "Succeeded" {
		t.Errorf("nested meta_updates.operation_status not rendered: got %q", v)
	}

	slice, ok := got["status_mapping"].([]interface{})
	if !ok {
		t.Fatalf("nested slice collapsed to %T", got["status_mapping"])
	}
	if v := slice[0].(string); v != "Succeeded" {
		t.Errorf("nested slice[0] template not rendered: got %q", v)
	}
	if v := slice[1].(string); v != "static_value" {
		t.Errorf("nested slice[1] static value mutated: got %q", v)
	}
}

// TestOperationStatusMapping tests getOperationStatus function
func TestOperationStatusMapping(t *testing.T) {
	tests := []struct {
		operationName  string
		expectedStatus string
	}{
		// VM operations
		{"Microsoft.Compute/virtualMachines/write", "Active"},
		{"Microsoft.Compute/virtualMachines/start/action", "Running"},
		{"Microsoft.Compute/virtualMachines/powerOff/action", "Stopped"},
		{"Microsoft.Compute/virtualMachines/deallocate/action", "Deallocated"},
		{"Microsoft.Compute/virtualMachines/restart/action", "Restarting"},
		{"Microsoft.Compute/virtualMachines/delete", "Deleted"},

		// SQL operations
		{"Microsoft.Sql/servers/databases/write", "Active"},
		{"Microsoft.Sql/servers/databases/delete", "Deleted"},
		{"Microsoft.Sql/servers/databases/pause/action", "Paused"},
		{"Microsoft.Sql/servers/databases/resume/action", "Running"},

		// Storage operations
		{"Microsoft.Storage/storageAccounts/write", "Active"},
		{"Microsoft.Storage/storageAccounts/delete", "Deleted"},

		// Web App operations
		{"Microsoft.Web/sites/write", "Active"},
		{"Microsoft.Web/sites/start/action", "Running"},
		{"Microsoft.Web/sites/stop/action", "Stopped"},
		{"Microsoft.Web/sites/delete", "Deleted"},

		// Generic patterns
		{"SomeProvider/resources/write", "Active"},
		{"SomeProvider/resources/delete", "Deleted"},
		{"SomeProvider/resources/start/action", "Running"},
		{"SomeProvider/resources/stop/action", "Stopped"},

		// Unknown operation
		{"SomeProvider/resources/unknownAction", "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.operationName, func(t *testing.T) {
			status := getOperationStatus(tt.operationName)
			if status != tt.expectedStatus {
				t.Errorf("getOperationStatus(%s) = '%s', want '%s'", tt.operationName, status, tt.expectedStatus)
			}
		})
	}
}

// TestEventEvidenceGeneration tests that action results are properly converted to EventEvidence
func TestEventEvidenceGeneration(t *testing.T) {
	mockProvider := &MockAzureProvider{
		listResourcesResponse: providers.ListResourcesResponse{
			Items: []providers.Resource{
				{
					Id:          "/subscriptions/12345/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1",
					Name:        "vm1",
					ServiceName: "microsoft.compute/virtualmachines",
					Type:        "virtualmachine",
					Region:      "eastus",
					Status:      providers.ResourceStatusActive,
					CreatedAt:   time.Now(),
				},
			},
		},
	}

	rule := AzureEventRule{
		Name:        "test_rule",
		Description: "Test rule for evidence generation",
		Triggers: AzureEventRuleTrigger{
			SourceSystem: "Azure_EventGrid",
		},
		EventTemplate: AzureEventOutputTemplate{
			Title:               AzureEventFieldTemplate{Value: "Test Event"},
			Severity:            AzureEventFieldTemplate{Value: "Info"},
			Description:         AzureEventFieldTemplate{Value: "Test Description"},
			EventStatus:         AzureEventFieldTemplate{Value: "Open"},
			ResourceId:          AzureEventFieldTemplate{Value: "/subscriptions/12345/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1"},
			ResourceType:        AzureEventFieldTemplate{Value: "virtualmachine"},
			ResourceServiceName: AzureEventFieldTemplate{Value: "microsoft.compute/virtualmachines"},
			ResourceRegion:      AzureEventFieldTemplate{Value: "eastus"},
		},
		Actions: []AzureActionDefinition{
			{
				Name:        "get_resource",
				Type:        "azure_get_resource",
				Description: "Retrieve resource details",
				Params: map[string]any{
					"resource_id": "/subscriptions/12345/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1",
				},
			},
		},
	}

	processor := NewTemplatedEventGridProcessor([]AzureEventRule{rule}, mockProvider)

	templateContext := map[string]interface{}{
		"ID":        "test-event-123",
		"EventType": "Microsoft.Resources.ResourceWriteSuccess",
		"EventTime": time.Now(),
		"Data": map[string]interface{}{
			"resourceUri": "/subscriptions/12345/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1",
		},
	}

	account := providers.Account{
		ID:            "test-account-id",
		AccountNumber: "12345",
		CloudProvider: "azure",
	}

	ctx := &MockCloudProviderContext{}
	providerEvent, err := processor.executeRule(ctx, rule, templateContext, account)
	if err != nil {
		t.Fatalf("executeRule returned error: %v", err)
	}

	// Verify event fields
	if providerEvent.EventName != "Test Event" {
		t.Errorf("Expected EventName 'Test Event', got '%s'", providerEvent.EventName)
	}

	if providerEvent.EventSeverity != providers.EventSeverity("Info") {
		t.Errorf("Expected EventSeverity 'Info', got '%s'", providerEvent.EventSeverity)
	}

	// Verify EventEvidence was generated
	if len(providerEvent.AdditionalContext) != 1 {
		t.Errorf("Expected 1 evidence item, got %d", len(providerEvent.AdditionalContext))
	}

	if len(providerEvent.AdditionalContext) > 0 {
		evidence := providerEvent.AdditionalContext[0]

		if evidence.Type != providers.EventEvidenceTypeJson {
			t.Errorf("Expected evidence type EventEvidenceTypeJson, got %v", evidence.Type)
		}

		if len(evidence.Insight) != 1 || evidence.Insight[0] != "Retrieve resource details" {
			t.Errorf("Expected Insight 'Retrieve resource details', got %v", evidence.Insight)
		}

		if evidence.AdditionalInfo["action_name"] != "get_resource" {
			t.Errorf("Expected action_name 'get_resource', got '%v'", evidence.AdditionalInfo["action_name"])
		}
	}
}

// TestEndToEndEventProcessing tests complete event processing flow
func TestEndToEndEventProcessing(t *testing.T) {
	mockProvider := &MockAzureProvider{}

	// Define a rule for VM state changes
	rule := AzureEventRule{
		Name: "vm_state_change",
		Triggers: AzureEventRuleTrigger{
			SourceSystem: "Azure_EventGrid",
			EventFilters: []AzureEventFilter{
				{Template: `{{ eq .EventType "Microsoft.Resources.ResourceWriteSuccess" }}`},
				{Template: `{{ contains .Data.operationName "Microsoft.Compute/virtualMachines" }}`},
			},
		},
		EventTemplate: AzureEventOutputTemplate{
			Title: AzureEventFieldTemplate{
				Template: `VM {{ .Data.resourceUri | basename }} state changed to {{ .Data.operationName | getOperationStatus }}`,
			},
			Severity: AzureEventFieldTemplate{Value: "Info"},
			Description: AzureEventFieldTemplate{
				Template: `Azure Virtual Machine {{ .Data.resourceUri | basename }} underwent operation: {{ .Data.operationName }}`,
			},
			EventStatus: AzureEventFieldTemplate{Value: "Open"},
			ResourceId: AzureEventFieldTemplate{
				Template: `{{ .Data.resourceUri }}`,
			},
			ResourceType: AzureEventFieldTemplate{
				Template: `{{ .Data.resourceUri | extractResourceType }}`,
			},
			ResourceServiceName: AzureEventFieldTemplate{
				Value: "microsoft.compute/virtualmachines",
			},
			ResourceRegion: AzureEventFieldTemplate{
				Template: `{{ extractRegion .Data }}`,
			},
		},
		Actions: []AzureActionDefinition{
			{
				Name:        "update_resource_state",
				Type:        "update_cloud_resource",
				Description: "Update cloud_resourses table with new VM state",
				Params: map[string]any{
					"resource_id": `{{ .Data.resourceUri }}`,
					"new_status":  `{{ .Data.operationName | getOperationStatus }}`,
				},
			},
		},
	}

	processor := NewTemplatedEventGridProcessor([]AzureEventRule{rule}, mockProvider)

	// Create a test event
	event := EventGridEvent{
		ID:        "test-event-456",
		EventType: "Microsoft.Resources.ResourceWriteSuccess",
		Subject:   "/subscriptions/12345/resourceGroups/test-rg/providers/Microsoft.Compute/virtualMachines/test-vm",
		EventTime: time.Date(2025, 1, 9, 12, 0, 0, 0, time.UTC),
		Data: json.RawMessage(`{
			"operationName": "Microsoft.Compute/virtualMachines/start/action",
			"resourceUri": "/subscriptions/12345/resourceGroups/test-rg/providers/Microsoft.Compute/virtualMachines/test-vm",
			"status": "Succeeded",
			"location": "eastus"
		}`),
		DataVersion:     "1.0",
		MetadataVersion: "1",
	}

	account := providers.Account{
		ID:            "test-account-id",
		AccountNumber: "12345",
		CloudProvider: "azure",
	}

	// Process the event
	ctx := &MockCloudProviderContext{}
	providerEvent, err := processor.Process(ctx, event, account)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}

	// Verify the resulting providers.Event
	if providerEvent.EventId != "test-event-456" {
		t.Errorf("Expected EventId 'test-event-456', got '%s'", providerEvent.EventId)
	}

	expectedTitle := "VM test-vm state changed to Running"
	if providerEvent.Title != expectedTitle {
		t.Errorf("Expected Title '%s', got '%s'", expectedTitle, providerEvent.Title)
	}

	expectedDescription := "Azure Virtual Machine test-vm underwent operation: Microsoft.Compute/virtualMachines/start/action"
	if providerEvent.Description != expectedDescription {
		t.Errorf("Expected Description '%s', got '%s'", expectedDescription, providerEvent.Description)
	}

	if providerEvent.EventSeverity != providers.EventSeverity("Info") {
		t.Errorf("Expected EventSeverity 'Info', got '%v'", providerEvent.EventSeverity)
	}

	if providerEvent.EventStatus != providers.EventStatusFiring {
		t.Errorf("Expected EventStatus 'FIRING', got '%v'", providerEvent.EventStatus)
	}

	if providerEvent.ResourceId != "/subscriptions/12345/resourceGroups/test-rg/providers/Microsoft.Compute/virtualMachines/test-vm" {
		t.Errorf("Unexpected ResourceId: %s", providerEvent.ResourceId)
	}

	if providerEvent.ResourceServiceName != "microsoft.compute/virtualmachines" {
		t.Errorf("Expected ResourceServiceName 'microsoft.compute/virtualmachines', got '%s'", providerEvent.ResourceServiceName)
	}

	if providerEvent.ResourceRegion != "eastus" {
		t.Errorf("Expected ResourceRegion 'eastus', got '%s'", providerEvent.ResourceRegion)
	}

	// Verify action evidence
	if len(providerEvent.AdditionalContext) != 1 {
		t.Errorf("Expected 1 evidence item, got %d", len(providerEvent.AdditionalContext))
	}
}

// TestRawEventPopulated verifies that the Raw event data is preserved (not nil)
// so it appears as a real "Raw Event" evidence in the DB instead of "null".
func TestRawEventPopulated(t *testing.T) {
	mockProvider := &MockAzureProvider{}

	rule := AzureEventRule{
		Name: "vm_delete",
		Triggers: AzureEventRuleTrigger{
			SourceSystem: "Azure_EventGrid",
			EventFilters: []AzureEventFilter{
				{Template: `{{ eq .EventType "Microsoft.Resources.ResourceDeleteSuccess" }}`},
			},
		},
		EventTemplate: AzureEventOutputTemplate{
			Title:               AzureEventFieldTemplate{Value: "VM Deleted"},
			Severity:            AzureEventFieldTemplate{Value: "Medium"},
			EventStatus:         AzureEventFieldTemplate{Value: "Open"},
			ResourceId:          AzureEventFieldTemplate{Template: `{{ .Subject }}`},
			ResourceServiceName: AzureEventFieldTemplate{Value: "microsoft.compute/virtualmachines"},
			Labels: map[string]AzureEventFieldTemplate{
				"azure_subscription_id": {Template: `{{ .Subject | extractSubscriptionId }}`},
				"azure_resource_group":  {Template: `{{ .Subject | extractResourceGroup }}`},
			},
		},
	}

	processor := NewTemplatedEventGridProcessor([]AzureEventRule{rule}, mockProvider)

	event := EventGridEvent{
		ID:        "delete-event-1",
		EventType: "Microsoft.Resources.ResourceDeleteSuccess",
		Subject:   "/subscriptions/sub-123/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/my-vm",
		EventTime: time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC),
		Data: json.RawMessage(`{
			"operationName": "Microsoft.Compute/virtualMachines/delete",
			"resourceUri": "/subscriptions/sub-123/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/my-vm",
			"caller": "user@example.com",
			"correlationId": "corr-abc-123"
		}`),
		Topic: "/subscriptions/sub-123",
	}

	account := providers.Account{ID: "acc-1", AccountNumber: "sub-123", CloudProvider: "azure"}
	ctx := &MockCloudProviderContext{}

	providerEvent, err := processor.Process(ctx, event, account)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}

	// Raw must not be nil — this was the bug causing "null" evidence in the DB
	if providerEvent.Raw == nil {
		t.Fatal("Raw event data is nil — evidence will appear as 'null' in the DB")
	}

	// Should contain the EventGrid envelope fields
	if providerEvent.Raw["eventType"] != "Microsoft.Resources.ResourceDeleteSuccess" {
		t.Errorf("Raw missing eventType, got: %v", providerEvent.Raw["eventType"])
	}
	if providerEvent.Raw["subject"] != event.Subject {
		t.Errorf("Raw missing subject, got: %v", providerEvent.Raw["subject"])
	}

	// Should contain the Activity Log data (caller, correlationId) from Data
	if providerEvent.Raw["caller"] != "user@example.com" {
		t.Errorf("Raw missing caller from Activity Log data, got: %v", providerEvent.Raw["caller"])
	}
	if providerEvent.Raw["correlationId"] != "corr-abc-123" {
		t.Errorf("Raw missing correlationId, got: %v", providerEvent.Raw["correlationId"])
	}
}

// TestAutoPopulateEnrichmentLabels verifies that azure_alert_target_resource and
// azure_service_name are auto-populated from event fields, enabling auto-execute
// enrichment in the api-server.
func TestAutoPopulateEnrichmentLabels(t *testing.T) {
	mockProvider := &MockAzureProvider{}

	rule := AzureEventRule{
		Name: "resource_delete",
		Triggers: AzureEventRuleTrigger{
			SourceSystem: "Azure_EventGrid",
			EventFilters: []AzureEventFilter{
				{Template: `{{ eq .EventType "Microsoft.Resources.ResourceDeleteSuccess" }}`},
			},
		},
		EventTemplate: AzureEventOutputTemplate{
			Title:               AzureEventFieldTemplate{Value: "Resource Deleted"},
			Severity:            AzureEventFieldTemplate{Value: "Medium"},
			EventStatus:         AzureEventFieldTemplate{Value: "Open"},
			ResourceId:          AzureEventFieldTemplate{Template: `{{ .Subject }}`},
			ResourceServiceName: AzureEventFieldTemplate{Value: "microsoft.sql/servers/databases"},
			Labels: map[string]AzureEventFieldTemplate{
				"azure_subscription_id": {Template: `{{ .Subject | extractSubscriptionId }}`},
				"azure_resource_group":  {Template: `{{ .Subject | extractResourceGroup }}`},
			},
		},
	}

	processor := NewTemplatedEventGridProcessor([]AzureEventRule{rule}, mockProvider)

	event := EventGridEvent{
		ID:        "del-sql-1",
		EventType: "Microsoft.Resources.ResourceDeleteSuccess",
		Subject:   "/subscriptions/sub-123/resourceGroups/rg-1/providers/Microsoft.Sql/servers/mydb/databases/testdb",
		EventTime: time.Now(),
		Data:      json.RawMessage(`{"operationName": "Microsoft.Sql/servers/databases/delete"}`),
	}

	account := providers.Account{ID: "acc-1", AccountNumber: "sub-123", CloudProvider: "azure"}
	ctx := &MockCloudProviderContext{}

	providerEvent, err := processor.Process(ctx, event, account)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}

	// azure_alert_target_resource should be auto-populated from ResourceId
	if providerEvent.Labels["azure_alert_target_resource"] != event.Subject {
		t.Errorf("Expected azure_alert_target_resource = %q, got %q",
			event.Subject, providerEvent.Labels["azure_alert_target_resource"])
	}

	// azure_service_name should be auto-populated from ResourceServiceName
	if providerEvent.Labels["azure_service_name"] != "microsoft.sql/servers/databases" {
		t.Errorf("Expected azure_service_name = 'microsoft.sql/servers/databases', got %q",
			providerEvent.Labels["azure_service_name"])
	}

	// Should NOT overwrite if already set in YAML
	if providerEvent.Labels["azure_subscription_id"] != "sub-123" {
		t.Errorf("Expected azure_subscription_id = 'sub-123', got %q",
			providerEvent.Labels["azure_subscription_id"])
	}
}

// TestAutoPopulateSkipsExistingLabels verifies that auto-populate does not
// overwrite labels already defined in the YAML rule.
func TestAutoPopulateSkipsExistingLabels(t *testing.T) {
	mockProvider := &MockAzureProvider{}

	rule := AzureEventRule{
		Name: "rule_with_existing_labels",
		Triggers: AzureEventRuleTrigger{
			SourceSystem: "Azure_EventGrid",
			EventFilters: []AzureEventFilter{
				{Template: `{{ eq .EventType "Microsoft.Resources.ResourceWriteSuccess" }}`},
			},
		},
		EventTemplate: AzureEventOutputTemplate{
			Title:               AzureEventFieldTemplate{Value: "Test"},
			Severity:            AzureEventFieldTemplate{Value: "Info"},
			EventStatus:         AzureEventFieldTemplate{Value: "Open"},
			ResourceId:          AzureEventFieldTemplate{Value: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1"},
			ResourceServiceName: AzureEventFieldTemplate{Value: "microsoft.compute/virtualmachines"},
			Labels: map[string]AzureEventFieldTemplate{
				"azure_alert_target_resource": {Value: "/custom/target/resource"},
				"azure_service_name":          {Value: "custom-service-name"},
			},
		},
	}

	processor := NewTemplatedEventGridProcessor([]AzureEventRule{rule}, mockProvider)

	event := EventGridEvent{
		ID:        "test-skip",
		EventType: "Microsoft.Resources.ResourceWriteSuccess",
		EventTime: time.Now(),
		Data:      json.RawMessage(`{}`),
	}

	account := providers.Account{ID: "acc-1", AccountNumber: "sub-123", CloudProvider: "azure"}
	ctx := &MockCloudProviderContext{}

	providerEvent, err := processor.Process(ctx, event, account)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}

	// Should keep the YAML-defined values, not overwrite with event fields
	if providerEvent.Labels["azure_alert_target_resource"] != "/custom/target/resource" {
		t.Errorf("Should not overwrite existing label, got %q", providerEvent.Labels["azure_alert_target_resource"])
	}
	if providerEvent.Labels["azure_service_name"] != "custom-service-name" {
		t.Errorf("Should not overwrite existing label, got %q", providerEvent.Labels["azure_service_name"])
	}
}

// TestNoMatchingRule tests behavior when no rule matches an event
func TestNoMatchingRule(t *testing.T) {
	mockProvider := &MockAzureProvider{}

	// Define a rule that won't match
	rule := AzureEventRule{
		Name: "sql_operation",
		Triggers: AzureEventRuleTrigger{
			SourceSystem: "Azure_EventGrid",
			EventFilters: []AzureEventFilter{
				{Template: `{{ contains .Data.operationName "Microsoft.Sql" }}`},
			},
		},
		EventTemplate: AzureEventOutputTemplate{
			Title: AzureEventFieldTemplate{Value: "SQL Operation"},
		},
	}

	processor := NewTemplatedEventGridProcessor([]AzureEventRule{rule}, mockProvider)

	// Create an event that doesn't match (VM operation, not SQL)
	event := EventGridEvent{
		ID:        "test-event-789",
		EventType: "Microsoft.Resources.ResourceWriteSuccess",
		Subject:   "/subscriptions/12345/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1",
		EventTime: time.Now(),
		Data:      json.RawMessage(`{"operationName": "Microsoft.Compute/virtualMachines/write"}`),
	}

	account := providers.Account{
		ID:            "test-account-id",
		AccountNumber: "12345",
		CloudProvider: "azure",
	}

	// Process the event
	ctx := &MockCloudProviderContext{}
	providerEvent, err := processor.Process(ctx, event, account)
	if err != nil {
		t.Errorf("Process should not return error for non-matching event: %v", err)
	}

	// Should return empty event (EventId will be empty)
	if providerEvent.EventId != "" {
		t.Errorf("Expected empty event for non-matching rule, got EventId '%s'", providerEvent.EventId)
	}
}
