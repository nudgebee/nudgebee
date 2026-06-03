package utils

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// PrometheusDateRange represents a date range with start and end times
type PrometheusDateRange struct {
	StartsAt string `json:"starts_at"`
	EndsAt   string `json:"ends_at"`
}

// PrometheusDuration represents a duration in minutes
type PrometheusDuration struct {
	DurationMinutes int `json:"duration_minutes"`
}

// Default resolution when no rule matches
const DefaultResolution = 3000

// ResolutionFunc represents a function that calculates resolution based on duration
type ResolutionFunc func(time.Duration) int

// ResolutionRule represents either a fixed resolution or a function to calculate it
type ResolutionRule struct {
	MaxDuration time.Duration
	Resolution  any // can be int or ResolutionFunc
}

// resolutionData mimics the original Python _RESOLUTION_DATA exactly
var resolutionData = []ResolutionRule{
	{
		MaxDuration: time.Hour,
		Resolution:  250, // timedelta(hours=1): 250
	},
	{
		MaxDuration: 24 * time.Hour,
		// NOTE: 1 minute resolution, max 1440 points
		// lambda duration: math.ceil(duration.total_seconds() / 60)
		Resolution: ResolutionFunc(func(duration time.Duration) int {
			return int(math.Ceil(duration.Seconds() / 60))
		}),
	},
	{
		MaxDuration: 7 * 24 * time.Hour,
		// NOTE: 5 minute resolution, max 2016 points
		// lambda duration: math.ceil(duration.total_seconds() / (60 * 5))
		Resolution: ResolutionFunc(func(duration time.Duration) int {
			return int(math.Ceil(duration.Seconds() / (60 * 5)))
		}),
	},
}

// GetResolutionFromDuration calculates the appropriate resolution for a given duration
// This exactly matches the original Python logic with _RESOLUTION_DATA
func GetResolutionFromDuration(duration time.Duration) int {
	// Sort by duration and find the first entry where duration <= maxDuration
	for _, rule := range resolutionData {
		if duration <= rule.MaxDuration {
			switch res := rule.Resolution.(type) {
			case int:
				return res
			case ResolutionFunc:
				return res(duration)
			}
		}
	}
	// If no rule matches, return default resolution
	return DefaultResolution
}

// CalculateStep calculates the step parameter for Prometheus queries
// It returns an integer step value instead of decimal as was done in the agent
func CalculateStep(startsAt, endsAt time.Time, existingStep string, resolution int) string {
	if existingStep != "" {
		// If step is already provided, use it as is
		return existingStep
	}

	queryDuration := endsAt.Sub(startsAt)

	// Calculate step using the original formula: max(duration_seconds / resolution, 1.0)
	stepFloat := math.Max(queryDuration.Seconds()/float64(resolution), 1.0)

	// Round to nearest integer as specified in the requirements
	step := int(math.Round(stepFloat))

	// Return as string representation of integer seconds (without 's' suffix)
	return fmt.Sprintf("%d", step)
}

// ParseDuration parses either PrometheusDateRange or PrometheusDuration into start and end times
func ParseDuration(duration any) (time.Time, time.Time, error) {
	switch d := duration.(type) {
	case PrometheusDateRange:
		startTime, err := parseTimestamp(d.StartsAt)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		endTime, err := parseTimestamp(d.EndsAt)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		return startTime, endTime, nil

	case PrometheusDuration:
		now := time.Now().UTC()
		startTime := now.Add(-time.Duration(d.DurationMinutes) * time.Minute)
		endTime := now
		return startTime, endTime, nil

	case map[string]any:
		// Handle generic map structure from JSON
		if startsAtStr, ok := d["starts_at"].(string); ok {
			if endsAtStr, ok := d["ends_at"].(string); ok {
				// This is a PrometheusDateRange
				startTime, err := parseTimestamp(startsAtStr)
				if err != nil {
					return time.Time{}, time.Time{}, err
				}
				endTime, err := parseTimestamp(endsAtStr)
				if err != nil {
					return time.Time{}, time.Time{}, err
				}
				return startTime, endTime, nil
			}
		}
		if durationMinutes, ok := d["duration_minutes"].(float64); ok {
			// This is a PrometheusDuration
			now := time.Now().UTC()
			startTime := now.Add(-time.Duration(int(durationMinutes)) * time.Minute)
			endTime := now
			return startTime, endTime, nil
		}

	default:
		return time.Time{}, time.Time{}, fmt.Errorf("unsupported duration type: %T", duration)
	}

	return time.Time{}, time.Time{}, fmt.Errorf("failed to parse duration")
}

// parseTimestamp parses a timestamp string in the format "2006-01-02 15:04:05 UTC"
func parseTimestamp(timestampStr string) (time.Time, error) {
	const backendFormat = "2006-01-02 15:04:05 UTC"
	return time.Parse(backendFormat, timestampStr)
}

// EnhancePrometheusStepCalculation enhances the Prometheus request with calculated step
// This function processes the JSON request body and calculates step when it's missing or empty
func EnhancePrometheusStepCalculation(logger *slog.Logger, requestBodyStr string) string {
	// Use provided logger or default
	l := logger
	if l == nil {
		l = slog.Default()
	}

	// Extract action_params to check for step and duration
	// Support both legacy format (body.action_params) and new format (action_params)
	pathPrefix := "body."
	actionParamsJSON := gjson.Get(requestBodyStr, "body.action_params")
	if !actionParamsJSON.Exists() {
		actionParamsJSON = gjson.Get(requestBodyStr, "action_params")
		if !actionParamsJSON.Exists() {
			return requestBodyStr
		}
		pathPrefix = ""
	}

	// Check if this is a non-instant query (query_range type)
	instant := gjson.Get(requestBodyStr, pathPrefix+"action_params.instant").Bool()
	if instant {
		// For instant queries, we don't need to calculate step
		return requestBodyStr
	}

	// Get current step value
	currentStep := gjson.Get(requestBodyStr, pathPrefix+"action_params.step").String()

	// If step is already provided and not empty, return as is
	if currentStep != "" {
		return requestBodyStr
	}

	// Extract duration information
	durationJSON := gjson.Get(requestBodyStr, pathPrefix+"action_params.duration")
	if !durationJSON.Exists() {
		return requestBodyStr
	}

	// Parse duration to get start and end times
	var duration any
	if err := json.Unmarshal([]byte(durationJSON.Raw), &duration); err != nil {
		l.Error("failed to parse duration", "err", err)
		return requestBodyStr
	}

	startTime, endTime, err := ParseDuration(duration)
	if err != nil {
		l.Error("failed to parse duration times", "err", err)
		return requestBodyStr
	}

	// Calculate step
	queryDuration := endTime.Sub(startTime)
	resolution := GetResolutionFromDuration(queryDuration)
	calculatedStep := CalculateStep(startTime, endTime, "", resolution)

	// Update the request body with calculated step
	modifiedBody, err := sjson.Set(requestBodyStr, pathPrefix+"action_params.step", calculatedStep)
	if err != nil {
		l.Error("failed to set calculated step", "err", err)
		return requestBodyStr
	}

	l.Debug("calculated step for prometheus query", "step", calculatedStep, "duration", queryDuration)
	return modifiedBody
}
