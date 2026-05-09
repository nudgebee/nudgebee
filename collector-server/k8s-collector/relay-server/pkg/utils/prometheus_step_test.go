package utils

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestGetResolutionFromDuration(t *testing.T) {
	tests := []struct {
		name        string
		duration    time.Duration
		expectedRes int
	}{
		// Based on Python _RESOLUTION_DATA:
		// timedelta(hours=1): 250
		{"30 minutes", 30 * time.Minute, 250},
		{"1 hour", 1 * time.Hour, 250},
		// timedelta(days=1): lambda duration: math.ceil(duration.total_seconds() / 60)
		{"3 hours", 3 * time.Hour, int(math.Ceil((3 * time.Hour).Seconds() / 60))},  // 180
		{"12 hours", 12 * time.Hour, int(math.Ceil((12 * time.Hour).Seconds() / 60))}, // 720
		{"1 day", 24 * time.Hour, int(math.Ceil((24 * time.Hour).Seconds() / 60))},   // 1440
		// timedelta(weeks=1): lambda duration: math.ceil(duration.total_seconds() / (60 * 5))
		{"3 days", 72 * time.Hour, int(math.Ceil((72 * time.Hour).Seconds() / (60 * 5)))}, // 864
		{"1 week", 168 * time.Hour, int(math.Ceil((168 * time.Hour).Seconds() / (60 * 5)))}, // 2016
		// > 1 week: default resolution of 3000
		{"1 month", 720 * time.Hour, 3000},
		{"2 months", 1440 * time.Hour, 3000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := GetResolutionFromDuration(tt.duration)
			if res != tt.expectedRes {
				t.Errorf("GetResolutionFromDuration(%v) = %d, want %d", tt.duration, res, tt.expectedRes)
			}
		})
	}
}

func TestCalculateStep(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(1 * time.Hour)
	
	tests := []struct {
		name         string
		existingStep string
		resolution   int
		expectedStep string
	}{
		{"existing step provided", "30s", 60, "30s"},
		{"no existing step - 1 hour duration", "", 60, "60"},
		{"no existing step - minimum 1 second", "", 7200, "1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := CalculateStep(start, end, tt.existingStep, tt.resolution)
			if step != tt.expectedStep {
				t.Errorf("CalculateStep() = %s, want %s", step, tt.expectedStep)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		wantErr  bool
		checkDur bool
		duration time.Duration
	}{
		{
			name: "PrometheusDateRange",
			input: PrometheusDateRange{
				StartsAt: "2024-01-01 00:00:00 UTC",
				EndsAt:   "2024-01-01 01:00:00 UTC",
			},
			wantErr:  false,
			checkDur: true,
			duration: 1 * time.Hour,
		},
		{
			name: "PrometheusDuration",
			input: PrometheusDuration{
				DurationMinutes: 60,
			},
			wantErr:  false,
			checkDur: true,
			duration: 60 * time.Minute,
		},
		{
			name: "Map interface - DateRange",
			input: map[string]interface{}{
				"starts_at": "2024-01-01 00:00:00 UTC",
				"ends_at":   "2024-01-01 02:00:00 UTC",
			},
			wantErr:  false,
			checkDur: true,
			duration: 2 * time.Hour,
		},
		{
			name: "Map interface - Duration",
			input: map[string]interface{}{
				"duration_minutes": float64(120),
			},
			wantErr:  false,
			checkDur: true,
			duration: 120 * time.Minute,
		},
		{
			name:    "Invalid input",
			input:   "invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, err := ParseDuration(tt.input)
			
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseDuration() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			
			if tt.checkDur && !tt.wantErr {
				actualDur := end.Sub(start)
				// Allow for small time differences due to "now" calculations
				if abs(actualDur-tt.duration) > time.Second {
					t.Errorf("ParseDuration() duration = %v, want %v", actualDur, tt.duration)
				}
			}
		})
	}
}

func TestEnhancePrometheusStepCalculation(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool // whether step should be calculated
	}{
		{
			name: "instant query - no step calculation",
			input: `{
				"body": {
					"action_params": {
						"instant": true,
						"step": "",
						"duration": {
							"starts_at": "2024-01-01 00:00:00 UTC",
							"ends_at": "2024-01-01 01:00:00 UTC"
						}
					}
				}
			}`,
			expected: false,
		},
		{
			name: "range query with empty step - should calculate",
			input: `{
				"body": {
					"action_params": {
						"instant": false,
						"step": "",
						"duration": {
							"starts_at": "2024-01-01 00:00:00 UTC",
							"ends_at": "2024-01-01 01:00:00 UTC"
						}
					}
				}
			}`,
			expected: true,
		},
		{
			name: "range query with existing step - should not calculate",
			input: `{
				"body": {
					"action_params": {
						"instant": false,
						"step": "30s",
						"duration": {
							"starts_at": "2024-01-01 00:00:00 UTC",
							"ends_at": "2024-01-01 01:00:00 UTC"
						}
					}
				}
			}`,
			expected: false,
		},
		{
			name: "new format - range query with empty step - should calculate",
			input: `{
				"action_params": {
					"instant": false,
					"step": "",
					"duration": {
						"starts_at": "2024-01-01 00:00:00 UTC",
						"ends_at": "2024-01-01 01:00:00 UTC"
					}
				}
			}`,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EnhancePrometheusStepCalculation(nil, tt.input)
			
			// Parse both input and result to compare step values
			var inputJSON, resultJSON map[string]interface{}
			_ = json.Unmarshal([]byte(tt.input), &inputJSON)
			_ = json.Unmarshal([]byte(result), &resultJSON)
			
			// Try legacy format first, then new format
			inputStep := getNestedString(inputJSON, "body", "action_params", "step")
			if inputStep == "" {
				inputStep = getNestedString(inputJSON, "action_params", "step")
			}
			
			resultStep := getNestedString(resultJSON, "body", "action_params", "step")
			if resultStep == "" {
				resultStep = getNestedString(resultJSON, "action_params", "step")
			}
			
			if tt.expected {
				// Step should have been calculated (changed from empty)
				if inputStep == resultStep {
					t.Errorf("Expected step to be calculated, but it remained: %s", resultStep)
				}
				if resultStep == "" {
					t.Errorf("Expected step to be calculated, but got empty string")
				}
			} else {
				// Step should remain unchanged
				if inputStep != resultStep {
					t.Errorf("Expected step to remain unchanged, but changed from %s to %s", inputStep, resultStep)
				}
			}
		})
	}
}

// Helper functions
func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func getNestedString(m map[string]interface{}, keys ...string) string {
	current := m
	for i, key := range keys {
		if i == len(keys)-1 {
			if val, ok := current[key].(string); ok {
				return val
			}
			return ""
		}
		if next, ok := current[key].(map[string]interface{}); ok {
			current = next
		} else {
			return ""
		}
	}
	return ""
}