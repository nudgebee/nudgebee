package types

import (
	"fmt"
	"math"
	"nudgebee/runbook/common"
	"reflect"
	"strings"
)

// isTemplateString reports whether s contains Gonja/Go template delimiters.
// Such strings pass save-time type validation because ProcessValue resolves
// them to the proper type (map, slice, scalar) at execution time.
func isTemplateString(s string) bool {
	return strings.Contains(s, "{{") || strings.Contains(s, "{%")
}

// PropertyType defines the type of a schema property.
type PropertyType string

const (
	PropertyTypeTimestamp    PropertyType = "timestamp"
	PropertyTypeString       PropertyType = "string"
	PropertyTypeNumber       PropertyType = "number"
	PropertyTypeBoolean      PropertyType = "boolean"
	PropertyTypeArray        PropertyType = "array"
	PropertyTypeObject       PropertyType = "object"
	PropertyTypeAny          PropertyType = "any"
	PropertyTypeAccount      PropertyType = "account"
	PropertyTypeIntegration  PropertyType = "integration"
	PropertyTypeNotification PropertyType = "notification"
	PropertyTypeTicket       PropertyType = "ticket"
	PropertyTypeInteger      PropertyType = "integer"
)

// Schema defines the expected input or output of a task.
type Schema struct {
	Properties map[string]Property `json:"properties"`
}

// OptionsSource defines how to dynamically fetch dropdown options for a field.
type OptionsSource struct {
	Type              string            `json:"type"`
	DependencyMapping map[string]string `json:"dependency_mapping,omitempty"`
}

// DynamicFieldsSource defines how to fetch additional field definitions at runtime.
type DynamicFieldsSource struct {
	Type              string            `json:"type"`
	DependencyMapping map[string]string `json:"dependency_mapping,omitempty"`
}

// VisibleWhen defines a condition for when a field should be visible.
type VisibleWhen struct {
	Field string   `json:"field"`
	Value []string `json:"value"`
}

// RequiredWhen defines a condition for when a field becomes required.
type RequiredWhen struct {
	Field string   `json:"field"`
	Value []string `json:"value"`
}

// Property defines a single field within a schema.
type Property struct {
	Type        PropertyType `json:"type"`
	Description string       `json:"description"`
	// Help is long-form reference content for the field, rendered by the
	// frontend as markdown inside an info-icon tooltip next to the field
	// label. Use this for column lists, operator cheatsheets, or other
	// reference material that would clutter Description.
	Help                string               `json:"help,omitempty"`
	Required            bool                 `json:"required"`
	Default             any                  `json:"default,omitempty"`
	IsEncrypted         bool                 `json:"is_encrypted,omitempty"`
	Options             []string             `json:"options,omitempty"`
	SubType             string               `json:"sub_type,omitempty"`
	Title               string               `json:"title,omitempty"`
	Order               int                  `json:"order,omitempty"`
	Hidden              bool                 `json:"hidden,omitempty"`
	ReadOnly            bool                 `json:"read_only,omitempty"`
	Schema              *Schema              `json:"schema,omitempty"`
	DependsOn           []string             `json:"depends_on,omitempty"`
	VisibleWhen         *VisibleWhen         `json:"visible_when,omitempty"`
	RequiredWhen        *RequiredWhen        `json:"required_when,omitempty"`
	OptionsSource       *OptionsSource       `json:"options_source,omitempty"`
	DynamicFieldsSource *DynamicFieldsSource `json:"dynamic_fields_source,omitempty"`
}

// NewSchema creates a new, empty schema.
func NewSchema() *Schema {
	return &Schema{
		Properties: make(map[string]Property),
	}
}

// Process transforms the input parameters based on the schema types.
// It modifies the params map in-place.
func (s *Schema) Process(params map[string]any) error {
	for name, prop := range s.Properties {
		value, exists := params[name]
		if !exists {
			continue
		}

		// Dereference pointers
		val := reflect.ValueOf(value)
		if val.Kind() == reflect.Ptr && !val.IsNil() {
			value = val.Elem().Interface()
			params[name] = value
		}

		if prop.Type == PropertyTypeTimestamp {
			t, err := common.ParseTime(value)
			if err != nil {
				return fmt.Errorf("invalid timestamp for parameter '%s': %w", name, err)
			}
			params[name] = t
		}
	}
	return nil
}

// Validate checks if the given parameters map conforms to the schema.
func (s *Schema) Validate(params map[string]any) error {
	for name, prop := range s.Properties {
		value, exists := params[name]

		isRequired := prop.Required
		if !isRequired && prop.RequiredWhen != nil {
			if depVal, ok := params[prop.RequiredWhen.Field]; ok {
				// Coerce to string so a boolean form value (e.g. true) compares
				// against RequiredWhen.Value entries like "true". Without this,
				// every non-string controller silently skipped the check.
				depStr := fmt.Sprintf("%v", depVal)
				for _, v := range prop.RequiredWhen.Value {
					if depStr == v {
						isRequired = true
						break
					}
				}
			}
		}

		isMissing := !exists || value == nil
		if s, ok := value.(string); ok && s == "" {
			isMissing = true
		}

		if isRequired && isMissing {
			return fmt.Errorf("missing required parameter: '%s'", name)
		}

		if exists && !isMissing {
			expectedType := prop.Type
			actualType := reflect.TypeOf(value)

			if expectedType == PropertyTypeAny {
				continue
			}

			if expectedType == PropertyTypeTimestamp {
				// Allow strings for timestamps (they will be parsed later)
				if actualType.Kind() == reflect.String {
					continue
				}
				// Also allow time.Time (if already parsed)
				if actualType.String() == "time.Time" {
					continue
				}
			}

			// Template strings (e.g. "{{ Configs['dev-pg'] }}") are legal at save time for
			// any non-string type: ProcessValue resolves them to the correct shape before
			// the task's Execute() runs. Runtime type assertions inside each Execute()
			// handle the resolved value (and silently skip if the resolved shape is wrong,
			// same as the existing core.call-workflow workflow_name check in service.go).
			if s, ok := value.(string); ok && isTemplateString(s) && expectedType != PropertyTypeString {
				continue
			}

			if expectedType == PropertyTypeArray {
				if actualType.Kind() != reflect.Slice && actualType.Kind() != reflect.Array {
					return fmt.Errorf("invalid type for parameter '%s': expected array, got %s", name, actualType.String())
				}
			} else if expectedType == PropertyTypeObject {
				if actualType.Kind() != reflect.Map {
					return fmt.Errorf("invalid type for parameter '%s': expected object (map), got %s", name, actualType.String())
				}
				// Recurse into the nested schema so RequiredWhen / Required
				// rules declared on inner fields (e.g. gitops_config's
				// integration_name) actually run.
				if prop.Schema != nil {
					if nested, ok := value.(map[string]any); ok {
						if err := prop.Schema.Validate(nested); err != nil {
							return fmt.Errorf("%s.%w", name, err)
						}
					}
				}
			} else if expectedType == PropertyTypeAccount || expectedType == PropertyTypeIntegration || expectedType == PropertyTypeNotification || expectedType == PropertyTypeTicket {
				// These types are essentially strings that refer to IDs
				if actualType.Kind() != reflect.String {
					return fmt.Errorf("invalid type for parameter '%s': expected string (ID for %s), got %s", name, expectedType, actualType.String())
				}
			} else if expectedType == PropertyTypeInteger {
				// Accept int types directly, and float64 if it's a whole number.
				// json.Unmarshal into map[string]any always produces float64 for JSON numbers.
				switch v := value.(type) {
				case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
					// valid
				case float64:
					if v != math.Trunc(v) {
						return fmt.Errorf("invalid type for parameter '%s': expected integer, got non-whole number (%v)", name, v)
					}
				default:
					return fmt.Errorf("invalid type for parameter '%s': expected integer, got %s", name, actualType.String())
				}
			} else if expectedType == PropertyTypeNumber {
				// Accept any numeric type (int or float)
				switch actualType.Kind() {
				case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
					reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
					reflect.Float32, reflect.Float64:
					// valid
				default:
					return fmt.Errorf("invalid type for parameter '%s': expected number, got %s", name, actualType.String())
				}
			} else if expectedType == PropertyTypeBoolean {
				if actualType.Kind() != reflect.Bool {
					return fmt.Errorf("invalid type for parameter '%s': expected boolean, got %s", name, actualType.String())
				}
			} else if actualType.String() != string(expectedType) {
				return fmt.Errorf("invalid type for parameter '%s': expected %s, got %s", name, expectedType, actualType.String())
			}
		}
	}
	return nil
}
