package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEvaluateExpression(t *testing.T) {
	testCases := []struct {
		name                   string
		contextData            map[string]any
		conditionToEvaluate    string
		expectedResult         any
		expectError            bool
		expectedErrorSubstring string
	}{
		{
			name:                "Basic arithmetic comparison",
			contextData:         map[string]any{"value": 10},
			conditionToEvaluate: "value > 5",
			expectedResult:      true,
			expectError:         false,
		},
		{
			name:                "Basic boolean logic AND",
			contextData:         map[string]any{"status": "SUCCESS", "value": 25},
			conditionToEvaluate: "status == 'SUCCESS' && value > 20",
			expectedResult:      true,
			expectError:         false,
		},
		{
			name:                "Basic boolean logic OR (true case)",
			contextData:         map[string]any{"status": "FAILED", "value": 15},
			conditionToEvaluate: "status == 'SUCCESS' || value > 10",
			expectedResult:      true,
			expectError:         false,
		},
		{
			name:                "Basic boolean logic OR (false case)",
			contextData:         map[string]any{"status": "FAILED", "value": 5},
			conditionToEvaluate: "status == 'SUCCESS' || value > 10",
			expectedResult:      false,
			expectError:         false,
		},
		{
			name:                "Accessing nested data (map)",
			contextData:         map[string]any{"plan1": map[string]any{"data": map[string]any{"value": 30, "status": "SUCCESS"}}},
			conditionToEvaluate: "plan1.data.value > 20 && plan1.data.status == 'SUCCESS'",
			expectedResult:      true,
			expectError:         false,
		},
		{
			name:                "Accessing nested data (map) - condition false",
			contextData:         map[string]any{"plan1": map[string]any{"data": map[string]any{"value": 15, "status": "SUCCESS"}}},
			conditionToEvaluate: "plan1.data.value > 20 && plan1.data.status == 'SUCCESS'",
			expectedResult:      false,
			expectError:         false,
		},
		{
			name:                   "Missing variable in expression",
			contextData:            map[string]any{"value": 10},
			conditionToEvaluate:    "value > 5 && missing_var == true",
			expectedResult:         nil,
			expectError:            true,
			expectedErrorSubstring: "unknown name missing_var",
		},
		{
			name:                   "Invalid expression syntax",
			contextData:            map[string]any{"value": 10},
			conditionToEvaluate:    "value > 5 &&",
			expectedResult:         nil,
			expectError:            true,
			expectedErrorSubstring: "error compiling expression",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := EvaluateExpression(tc.contextData, tc.conditionToEvaluate)

			if tc.expectError {
				assert.Error(t, err)
				if tc.expectedErrorSubstring != "" {
					assert.Contains(t, err.Error(), tc.expectedErrorSubstring)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedResult, result)
			}
		})
	}
}
