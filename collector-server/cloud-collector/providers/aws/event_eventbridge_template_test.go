package aws

import (
	"context"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderTemplateValue(t *testing.T) {
	dummyRuleSet := EventRuleSet{}

	processor := NewTemplatedEventBridgeProcessor(dummyRuleSet, nil) // Pass nil for the dummy API as it's not used in this test
	pCtx := providers.NewCloudProviderContext(context.Background())

	sampleEvent := EventBridgeEvent{
		ID:         "event-123",
		Source:     "aws.ecs",
		Account:    "864186153326",
		Region:     "us-east-1",
		DetailType: "ECS Task State Change",
		Time:       time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
	}
	sampleDetailMap := map[string]any{
		"taskArn":    "arn:aws:ecs:us-east-1:864186153326:task/my-cluster/abcdef1234567890",
		"status":     "STOPPED",
		"exitCode":   137,
		"nestedData": map[string]any{"key": "nestedValue"},
	}

	templateData := struct {
		Event  EventBridgeEvent
		Detail map[string]any
	}{
		Event:  sampleEvent,
		Detail: sampleDetailMap,
	}

	tests := []struct {
		name          string
		fieldName     string
		templateStr   string
		data          any
		expected      string
		expectError   bool
		errorContains string
	}{
		{
			name:        "Simple Event ID access",
			fieldName:   "EventID",
			templateStr: `{{ .Event.ID }}`,
			data:        templateData,
			expected:    "event-123",
			expectError: false,
		},
		{
			name:        "Detail field access",
			fieldName:   "TaskStatus",
			templateStr: `{{ .Detail.status }}`,
			data:        templateData,
			expected:    "STOPPED",
			expectError: false,
		},
		{
			name:        "Nested Detail field access",
			fieldName:   "NestedDetail",
			templateStr: `{{ .Detail.nestedData.key }}`,
			data:        templateData,
			expected:    "nestedValue",
			expectError: false,
		},
		{
			name:        "Custom function splitList and last",
			fieldName:   "TaskID",
			templateStr: `{{ .Detail.taskArn | splitList "/" | last }}`,
			data:        templateData,
			expected:    "abcdef1234567890",
			expectError: false,
		},
		{
			name:        "Custom function toJson",
			fieldName:   "DetailJSON",
			templateStr: `{{ toJson .Detail.nestedData }}`,
			data:        templateData,
			expected:    `{"key":"nestedValue"}`, // This is the expected *semantic* content
			expectError: false,
		},
		{
			name:        "Custom function default - value exists",
			fieldName:   "DefaultValueExists",
			templateStr: `{{ .Detail.status | default "fallback" }}`,
			data:        templateData,
			expected:    "STOPPED",
			expectError: false,
		},
		{
			name:        "Custom function default - value missing",
			fieldName:   "DefaultValueMissing",
			templateStr: `{{ .Detail.nonExistentKey | default "fallbackValue" }}`,
			data:        templateData,
			expected:    "fallbackValue",
			expectError: false,
		},
		{
			name:        "Custom function printf",
			fieldName:   "PrintfTest",
			templateStr: `{{ printf "Task %s is %s" (.Detail.taskArn | splitList "/" | last) .Detail.status }}`,
			data:        templateData,
			expected:    "Task abcdef1234567890 is STOPPED",
			expectError: false,
		},
		{
			name:          "Invalid template syntax",
			fieldName:     "InvalidTemplate",
			templateStr:   `{{ .Event.ID `, // Missing closing braces
			data:          templateData,
			expectError:   true,
			errorContains: "parsing template",
		},
		{
			name:          "Empty template string",
			fieldName:     "EmptyTemplate",
			templateStr:   ``,
			data:          templateData,
			expectError:   true,
			errorContains: "template string is empty",
		},
	}

	// RDS-specific test data simulating an RDS DB Instance Event
	rdsTemplateData := struct {
		Event  EventBridgeEvent
		Detail map[string]any
	}{
		Event: EventBridgeEvent{
			ID:         "rds-event-1",
			Source:     "aws.rds",
			Account:    "864186153326",
			Region:     "us-east-1",
			DetailType: "RDS DB Instance Event",
		},
		Detail: map[string]any{
			"SourceIdentifier": "database-1",
			"EventCategories":  []any{"deletion"},
			"Message":          "DB instance deleted",
		},
	}
	rdsNonDeletionData := struct {
		Event  EventBridgeEvent
		Detail map[string]any
	}{
		Event: EventBridgeEvent{
			ID:         "rds-event-2",
			Source:     "aws.rds",
			Account:    "864186153326",
			Region:     "us-east-1",
			DetailType: "RDS DB Instance Event",
		},
		Detail: map[string]any{
			"SourceIdentifier": "database-1",
			"EventCategories":  []any{"configuration change"},
			"Message":          "DB instance modified",
		},
	}

	rdsTests := []struct {
		name        string
		templateStr string
		data        any
		expected    string
	}{
		{
			name:        "RDS deletion event - should return Deleted",
			templateStr: `{{ if containsStr (index .Detail "EventCategories" | toJson | str) "deletion" }}Deleted{{ else }}Active{{ end }}`,
			data:        rdsTemplateData,
			expected:    "Deleted",
		},
		{
			name:        "RDS non-deletion event - should return Active",
			templateStr: `{{ if containsStr (index .Detail "EventCategories" | toJson | str) "deletion" }}Deleted{{ else }}Active{{ end }}`,
			data:        rdsNonDeletionData,
			expected:    "Active",
		},
	}

	for _, tt := range rdsTests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := processor.renderTemplateValue(pCtx, "new_status", tt.templateStr, tt.data)
			require.NoError(t, err, "Did not expect an error for test: %s", tt.name)
			assert.Equal(t, tt.expected, result, "Output mismatch for test: %s", tt.name)
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := processor.renderTemplateValue(pCtx, tt.fieldName, tt.templateStr, tt.data)

			if tt.expectError {
				require.Error(t, err, "Expected an error for test: %s", tt.name)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains, "Error message mismatch for test: %s", tt.name)
				}
			} else {
				require.NoError(t, err, "Did not expect an error for test: %s", tt.name)
				// For toJson, compare unmarshalled maps to be robust against formatting differences
				if tt.name == "Custom function toJson" {
					var expectedMap, resultMap map[string]any
					errExpected := common.UnmarshalJson([]byte(tt.expected), &expectedMap)
					require.NoError(t, errExpected, "Failed to unmarshal expected JSON for test: %s", tt.name)
					errResult := common.UnmarshalJson([]byte(result), &resultMap)
					require.NoError(t, errResult, "Failed to unmarshal result JSON for test: %s", tt.name)
					assert.Equal(t, expectedMap, resultMap, "Unmarshalled JSON content mismatch for test: %s", tt.name)
				} else {
					assert.Equal(t, tt.expected, result, "Output mismatch for test: %s", tt.name)
				}
			}
		})
	}
}
