package azure

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Template helper functions for Azure Event Grid processor
// These functions are registered in the template.FuncMap and can be used in YAML templates

// String manipulation functions
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func hasPrefix(s, prefix string) bool {
	return strings.HasPrefix(s, prefix)
}

func hasSuffix(s, suffix string) bool {
	return strings.HasSuffix(s, suffix)
}

func toLower(s string) string {
	return strings.ToLower(s)
}

func toUpper(s string) string {
	return strings.ToUpper(s)
}

func trim(s string) string {
	return strings.TrimSpace(s)
}

func split(s, sep string) []string {
	return strings.Split(s, sep)
}

func join(elems []string, sep string) string {
	return strings.Join(elems, sep)
}

func replace(s, old, new string) string {
	return strings.ReplaceAll(s, old, new)
}

// Comparison and logical functions
func eq(a, b interface{}) bool {
	return reflect.DeepEqual(a, b)
}

func ne(a, b interface{}) bool {
	return !reflect.DeepEqual(a, b)
}

func and(args ...bool) bool {
	for _, arg := range args {
		if !arg {
			return false
		}
	}
	return true
}

func or(args ...bool) bool {
	for _, arg := range args {
		if arg {
			return true
		}
	}
	return false
}

func not(b bool) bool {
	return !b
}

// Type conversion helpers
func toString(v interface{}) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func toInt(v interface{}) (int, error) {
	switch val := v.(type) {
	case int:
		return val, nil
	case int64:
		return int(val), nil
	case float64:
		return int(val), nil
	case string:
		return strconv.Atoi(val)
	default:
		return 0, fmt.Errorf("cannot convert %T to int", v)
	}
}

func toFloat64(v interface{}) (float64, error) {
	switch val := v.(type) {
	case float64:
		return val, nil
	case float32:
		return float64(val), nil
	case int:
		return float64(val), nil
	case int64:
		return float64(val), nil
	case string:
		return strconv.ParseFloat(val, 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", v)
	}
}

// extractResourceName extracts the last segment of a resource ID (resource name)
func extractResourceName(resourceId string) string {
	if resourceId == "" {
		return ""
	}
	parts := strings.Split(resourceId, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			return parts[i]
		}
	}
	return ""
}
