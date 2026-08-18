package core

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
)

type NBToolType string

const (
	NBToolTypeTool         NBToolType = "tool"
	NBToolTypeMemory       NBToolType = "memory"
	NBToolTypeExternal     NBToolType = "external"
	NBToolTypeSystem       NBToolType = "system"
	NBToolTypeCodeAnalysis NBToolType = "code_analysis"
)

type ToolSchema struct {
	Type        string         `json:"type,omitempty"`
	Description string         `json:"description,omitempty"`
	Properties  map[string]any `json:"properties,omitempty"`
	Required    []string       `json:"required,omitempty"`
}

type NBToolResponse struct {
	Status      string `json:"status"`
	Result      string `json:"result"`
	Error       string `json:"error,omitempty"`
	Observation string `json:"observation,omitempty"`
	Data        any    `json:"data,omitempty"`
}

type NBTool interface {
	Name() string
	Description() string
	InputSchema() ToolSchema
	Execute(ctx context.Context, input map[string]any) NBToolResponse
	GetType() NBToolType
}

// ReadOnlyTool is an optional interface that tools can implement to indicate
// they are safe to run concurrently (no side effects).
type ReadOnlyTool interface {
	IsReadOnly() bool
}

// Summarizer is an optional interface that tools can implement to provide
// custom output summarization when observations are too long.
type Summarizer interface {
	Summarize(output string, maxLen int) string
}

// Helper function to create a tool schema
func CreateToolSchema(schemaType, description string, properties map[string]any, required []string) ToolSchema {
	return ToolSchema{
		Type:        schemaType,
		Description: description,
		Properties:  properties,
		Required:    required,
	}
}

// Helper function to create a successful response
func CreateSuccessResponse(result, observation string, data any) NBToolResponse {
	return NBToolResponse{
		Status:      "success",
		Result:      result,
		Observation: observation,
		Data:        data,
	}
}

// Helper function to create an error response
func CreateErrorResponse(errorMsg, observation string) NBToolResponse {
	return NBToolResponse{
		Status:      "error",
		Error:       errorMsg,
		Observation: observation,
	}
}

// CreateErrorResponseWithData is CreateErrorResponse plus structured data.
// Failure responses that drop their data lose real diagnostics for programmatic
// consumers — e.g. the fix verifier reads exit codes from the CLI result, which
// used to be absent exactly on the failed commands it cares about most.
func CreateErrorResponseWithData(errorMsg, observation string, data any) NBToolResponse {
	return NBToolResponse{
		Status:      "error",
		Error:       errorMsg,
		Observation: observation,
		Data:        data,
	}
}

// Helper function to parse input parameters
func ParseInput(input map[string]any, target any) error {
	jsonData, err := json.Marshal(coerceScalarStrings(input, target))
	if err != nil {
		return err
	}
	return json.Unmarshal(jsonData, target)
}

// coerceScalarStrings converts string-quoted scalars in the LLM's tool arguments
// to the type the target field actually declares — `"shallow": "true"` against a
// `bool` field, `"limit": "10"` against an `int`. Models emit these routinely and
// encoding/json rejects them outright, which failed the whole call with
// "Failed to parse tool input parameters"; the planner's retry then dropped other
// arguments it had gotten right (a repo_clone lost its `branch` that way and
// cloned the wrong ref). Only top-level fields are coerced, and only when the
// string parses cleanly — anything else is passed through untouched so a genuine
// type error still surfaces as one.
func coerceScalarStrings(input map[string]any, target any) map[string]any {
	t := reflect.TypeOf(target)
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return input
	}

	out := input
	cloned := false
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		// Mirror encoding/json's own rules: it never populates unexported
		// fields or ones tagged `json:"-"`, so coercing an input key that
		// happens to share such a field's Go name would rewrite a value json
		// is about to ignore.
		if field.PkgPath != "" {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		raw, ok := input[name].(string)
		if !ok {
			continue
		}
		coerced, ok := coerceString(raw, field.Type)
		if !ok {
			continue
		}
		if !cloned {
			out = make(map[string]any, len(input))
			for k, v := range input {
				out[k] = v
			}
			cloned = true
		}
		out[name] = coerced
	}
	return out
}

// coerceString parses raw into the kind ft declares, reporting whether it applies.
func coerceString(raw string, ft reflect.Type) (any, bool) {
	for ft.Kind() == reflect.Ptr {
		ft = ft.Elem()
	}
	switch ft.Kind() {
	case reflect.Bool:
		v, err := strconv.ParseBool(raw)
		return v, err == nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, err := strconv.ParseInt(raw, 10, 64)
		return v, err == nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := strconv.ParseUint(raw, 10, 64)
		return v, err == nil
	case reflect.Float32, reflect.Float64:
		v, err := strconv.ParseFloat(raw, 64)
		return v, err == nil
	default:
		return nil, false
	}
}
