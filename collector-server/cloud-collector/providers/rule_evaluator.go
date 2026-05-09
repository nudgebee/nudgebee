package providers

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// RuleEvaluator provides generic condition evaluation for alarm templates
type RuleEvaluator struct {
	accessor MetadataAccessor
}

// NewRuleEvaluator creates a new RuleEvaluator with the specified metadata accessor
func NewRuleEvaluator(accessor MetadataAccessor) *RuleEvaluator {
	if accessor == nil {
		accessor = NewMetadataAccessor()
	}
	return &RuleEvaluator{
		accessor: accessor,
	}
}

// EvaluateConditions evaluates a list of conditions against a resource
// Returns true if all conditions pass (AND logic by default)
// Supports OR logic via the Logic field in individual conditions
func (e *RuleEvaluator) EvaluateConditions(resource Resource, conditions []Condition) bool {
	if len(conditions) == 0 {
		// No conditions means always true (NATIVE metrics)
		return true
	}

	// Evaluate each condition
	result := true
	for i, condition := range conditions {
		conditionResult := e.evaluateCondition(resource, condition)

		// First condition sets the initial result
		if i == 0 {
			result = conditionResult
			continue
		}

		// Combine with previous result based on logic operator
		logic := strings.ToUpper(condition.Logic)
		if logic == "" {
			logic = "AND" // Default to AND
		}

		switch logic {
		case "AND":
			result = result && conditionResult
		case "OR":
			result = result || conditionResult
		default:
			// Unknown logic operator, default to AND
			result = result && conditionResult
		}
	}

	return result
}

// evaluateCondition evaluates a single condition against a resource
func (e *RuleEvaluator) evaluateCondition(resource Resource, condition Condition) bool {
	operator := strings.ToLower(condition.Operator)

	switch operator {
	case "exists":
		return e.accessor.Exists(resource, condition.Field)

	case "not_exists":
		return !e.accessor.Exists(resource, condition.Field)

	case "equals":
		return e.evaluateEquals(resource, condition)

	case "not_equals":
		return !e.evaluateEquals(resource, condition)

	case "contains":
		return e.evaluateContains(resource, condition)

	case "gt":
		return e.evaluateNumericComparison(resource, condition, func(a, b float64) bool { return a > b })

	case "gte":
		return e.evaluateNumericComparison(resource, condition, func(a, b float64) bool { return a >= b })

	case "lt":
		return e.evaluateNumericComparison(resource, condition, func(a, b float64) bool { return a < b })

	case "lte":
		return e.evaluateNumericComparison(resource, condition, func(a, b float64) bool { return a <= b })

	case "is_empty":
		return e.evaluateIsEmpty(resource, condition)

	case "not_empty":
		return !e.evaluateIsEmpty(resource, condition)

	default:
		// Unknown operator - return false
		return false
	}
}

// evaluateEquals checks if field value equals the expected value
func (e *RuleEvaluator) evaluateEquals(resource Resource, condition Condition) bool {
	value, err := e.accessor.Get(resource, condition.Field)
	if err != nil {
		return false
	}

	if value == nil {
		return condition.Value == nil
	}

	// Direct comparison
	if reflect.DeepEqual(value, condition.Value) {
		return true
	}

	// Try string comparison
	valueStr := fmt.Sprintf("%v", value)
	expectedStr := fmt.Sprintf("%v", condition.Value)
	return valueStr == expectedStr
}

// evaluateContains checks if string field contains the expected substring
func (e *RuleEvaluator) evaluateContains(resource Resource, condition Condition) bool {
	value, err := e.accessor.GetString(resource, condition.Field)
	if err != nil {
		return false
	}

	expectedStr := fmt.Sprintf("%v", condition.Value)
	return strings.Contains(value, expectedStr)
}

// evaluateNumericComparison performs numeric comparison (gt, gte, lt, lte)
func (e *RuleEvaluator) evaluateNumericComparison(resource Resource, condition Condition, compareFn func(a, b float64) bool) bool {
	value, err := e.accessor.GetFloat(resource, condition.Field)
	if err != nil {
		return false
	}

	// Convert expected value to float64
	var expectedValue float64
	switch v := condition.Value.(type) {
	case float64:
		expectedValue = v
	case float32:
		expectedValue = float64(v)
	case int:
		expectedValue = float64(v)
	case int32:
		expectedValue = float64(v)
	case int64:
		expectedValue = float64(v)
	case string:
		// Try parsing string as float
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return false
		}
		expectedValue = parsed
	default:
		return false
	}

	return compareFn(value, expectedValue)
}

// evaluateIsEmpty checks if string field is empty or whitespace-only
func (e *RuleEvaluator) evaluateIsEmpty(resource Resource, condition Condition) bool {
	value, err := e.accessor.GetString(resource, condition.Field)
	if err != nil {
		// Field doesn't exist or is not a string - consider it empty
		return true
	}

	return strings.TrimSpace(value) == ""
}

// ShouldRecommendAlarm determines if an alarm should be recommended for a resource
// This is a convenience function that wraps EvaluateConditions for use in alarm checking
func (e *RuleEvaluator) ShouldRecommendAlarm(resource Resource, template AlarmTemplate) bool {
	// NATIVE metrics should always be recommended if missing
	if template.MetricType == "NATIVE" {
		return true
	}

	// CONDITIONAL metrics need condition evaluation
	if template.MetricType == "CONDITIONAL" {
		return e.EvaluateConditions(resource, template.Conditions)
	}

	// Unknown metric type - default to not recommending
	return false
}
