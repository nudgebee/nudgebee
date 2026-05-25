package providers

import (
	"fmt"
	"strconv"
	"strings"
)

// MetadataAccessor provides a generic interface for accessing resource metadata
// across different cloud providers (AWS, Azure, GCP)
type MetadataAccessor interface {
	// Get retrieves a value at the specified field path
	Get(resource Resource, path string) (interface{}, error)

	// GetString retrieves a string value at the specified field path
	GetString(resource Resource, path string) (string, error)

	// GetBool retrieves a boolean value at the specified field path
	GetBool(resource Resource, path string) (bool, error)

	// GetFloat retrieves a float64 value at the specified field path
	GetFloat(resource Resource, path string) (float64, error)

	// Exists checks if a field exists at the specified path
	Exists(resource Resource, path string) bool
}

// DefaultMetadataAccessor implements MetadataAccessor with support for:
// - Direct field access: "Id", "Name", "Type", "Region"
// - Meta map access: "Meta.DBInstanceClass", "Meta.properties.sku.name"
// - Tags map access: "Tags.Environment"
type DefaultMetadataAccessor struct{}

// NewMetadataAccessor creates a new DefaultMetadataAccessor
func NewMetadataAccessor() MetadataAccessor {
	return &DefaultMetadataAccessor{}
}

// Get retrieves a value at the specified field path
// Supported paths:
//   - "Id", "Name", "Type", "Arn", "ServiceName", "Region" - direct resource fields
//   - "Meta.field" - access to Meta map (AWS: Meta.DBInstanceClass)
//   - "Meta.properties.field" - nested access (Azure: Meta.properties.sku.name)
//   - "Tags.key" - access to Tags map
func (a *DefaultMetadataAccessor) Get(resource Resource, path string) (interface{}, error) {
	parts := strings.Split(path, ".")

	if len(parts) == 0 {
		return nil, fmt.Errorf("empty field path")
	}

	// Handle direct resource fields
	if len(parts) == 1 {
		return a.getDirectField(resource, parts[0])
	}

	// Handle nested paths (Meta.field, Tags.key)
	return a.getNestedField(resource, parts)
}

// GetString retrieves a string value at the specified field path
func (a *DefaultMetadataAccessor) GetString(resource Resource, path string) (string, error) {
	value, err := a.Get(resource, path)
	if err != nil {
		return "", err
	}

	if value == nil {
		return "", fmt.Errorf("field %s is nil", path)
	}

	// Type assertion with various string representations
	switch v := value.(type) {
	case string:
		return v, nil
	case fmt.Stringer:
		return v.String(), nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

// GetBool retrieves a boolean value at the specified field path
func (a *DefaultMetadataAccessor) GetBool(resource Resource, path string) (bool, error) {
	value, err := a.Get(resource, path)
	if err != nil {
		return false, err
	}

	if value == nil {
		return false, fmt.Errorf("field %s is nil", path)
	}

	// Type assertion
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		// Parse string as boolean
		return strconv.ParseBool(v)
	case int, int32, int64:
		// Non-zero is true
		return v != 0, nil
	case float64:
		return v != 0, nil
	default:
		return false, fmt.Errorf("field %s is not a boolean (type: %T)", path, value)
	}
}

// GetFloat retrieves a float64 value at the specified field path
func (a *DefaultMetadataAccessor) GetFloat(resource Resource, path string) (float64, error) {
	value, err := a.Get(resource, path)
	if err != nil {
		return 0, err
	}

	if value == nil {
		return 0, fmt.Errorf("field %s is nil", path)
	}

	// Type conversion
	switch v := value.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(v, 64)
	default:
		return 0, fmt.Errorf("field %s is not numeric (type: %T)", path, value)
	}
}

// Exists checks if a field exists at the specified path
func (a *DefaultMetadataAccessor) Exists(resource Resource, path string) bool {
	value, err := a.Get(resource, path)
	return err == nil && value != nil
}

// getDirectField retrieves a direct field from the resource struct
func (a *DefaultMetadataAccessor) getDirectField(resource Resource, field string) (interface{}, error) {
	switch field {
	case "Id":
		return resource.Id, nil
	case "Name":
		return resource.Name, nil
	case "Type":
		return resource.Type, nil
	case "Arn":
		return resource.Arn, nil
	case "ServiceName":
		return resource.ServiceName, nil
	case "Status":
		return resource.Status, nil
	case "Region":
		return resource.Region, nil
	case "CreatedAt":
		return resource.CreatedAt, nil
	default:
		return nil, fmt.Errorf("unknown direct field: %s", field)
	}
}

// getNestedField retrieves a nested field from Meta or Tags maps
func (a *DefaultMetadataAccessor) getNestedField(resource Resource, parts []string) (interface{}, error) {
	root := parts[0]
	remaining := parts[1:]

	switch root {
	case "Meta":
		return a.getFromMap(resource.Meta, remaining)
	case "Tags":
		return a.getFromTagsMap(resource.Tags, remaining)
	default:
		return nil, fmt.Errorf("unknown root field: %s (expected Meta or Tags)", root)
	}
}

// getFromMap recursively retrieves a value from a nested map structure
func (a *DefaultMetadataAccessor) getFromMap(data map[string]interface{}, parts []string) (interface{}, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty field path")
	}

	key := parts[0]

	// Get value from current level
	value, ok := data[key]
	if !ok {
		return nil, fmt.Errorf("field not found: %s", key)
	}

	// If this is the last part, return the value
	if len(parts) == 1 {
		return value, nil
	}

	// Otherwise, continue traversing
	// Check if value is a map[string]interface{}
	if nestedMap, ok := value.(map[string]interface{}); ok {
		return a.getFromMap(nestedMap, parts[1:])
	}

	return nil, fmt.Errorf("field %s is not a map (cannot traverse further)", key)
}

// getFromTagsMap retrieves a value from the Tags map
// Tags structure: map[string][]string
func (a *DefaultMetadataAccessor) getFromTagsMap(tags map[string][]string, parts []string) (interface{}, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty tag key")
	}

	if len(parts) > 1 {
		return nil, fmt.Errorf("tags only support single-level access (e.g., Tags.Environment)")
	}

	key := parts[0]
	values, ok := tags[key]
	if !ok {
		return nil, fmt.Errorf("tag not found: %s", key)
	}

	// Return the first value if multiple exist
	if len(values) > 0 {
		return values[0], nil
	}

	return nil, fmt.Errorf("tag %s has no values", key)
}
