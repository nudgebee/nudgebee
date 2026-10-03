package tasks

import (
	"encoding/json"
	"fmt"
	"nudgebee/runbook/internal/model"
	"reflect"
	"strings"

	"go.temporal.io/sdk/temporal"
)

// Standard error codes for task validation failures.
const (
	ErrCodeEmptyOutput          = "ERR_EMPTY_OUTPUT"
	ErrCodeMalformedOutput      = "ERR_MALFORMED_OUTPUT"
	ErrCodeTypeMismatch         = "ERR_TYPE_MISMATCH"
	ErrCodeMissingRequiredField = "ERR_MISSING_REQUIRED_FIELD"
)

// TaskValidationError represents a structured failure during deterministic task result validation.
type TaskValidationError struct {
	Code     string         `json:"code"`
	Reason   string         `json:"reason"`
	TaskType string         `json:"task_type,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

func (e *TaskValidationError) Error() string {
	if e.TaskType != "" {
		return fmt.Sprintf("task (%s) output validation failed [%s]: %s", e.TaskType, e.Code, e.Reason)
	}
	return fmt.Sprintf("task output validation failed [%s]: %s", e.Code, e.Reason)
}

// IsRetryable reports whether the validation failure is potentially transient (e.g. empty output
// during cold starts or asynchronous background processes) or deterministic (e.g. malformed syntax,
// schema type mismatch, or missing required fields).
func (e *TaskValidationError) IsRetryable() bool {
	if e == nil {
		return false
	}
	// Only ERR_EMPTY_OUTPUT is potentially transient (process cold starts, unwritten buffers).
	// Deterministic schema, type, and malformed syntax errors must NOT retry.
	return e.Code == ErrCodeEmptyOutput
}

// ToTemporalError maps the validation failure into a Temporal ApplicationError,
// explicitly flagging deterministic contract violations as NonRetryable to avoid
// burning Temporal retry budgets on unrecoverable contract failures.
func (e *TaskValidationError) ToTemporalError() error {
	if e == nil {
		return nil
	}
	if !e.IsRetryable() {
		return temporal.NewNonRetryableApplicationError(e.Error(), e.Code, e)
	}
	return temporal.NewApplicationError(e.Error(), e.Code)
}

// extractPayload inspects a task's raw result to locate the primary content payload.
// For tasks wrapping content in {"data": ...} or {"body": ...}, it returns that inner
// value, true for isWrapped, and the wrapping key name.
func extractPayload(result any) (any, bool, string) {
	if resMap, ok := result.(map[string]any); ok {
		if data, hasData := resMap["data"]; hasData {
			return data, true, "data"
		}
		if body, hasBody := resMap["body"]; hasBody {
			return body, true, "body"
		}
	}
	return result, false, ""
}

// isPayloadEmpty returns true if v represents an empty value (nil, empty string, empty map, or empty slice).
func isPayloadEmpty(v any) bool {
	if v == nil {
		return true
	}
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val) == ""
	case map[string]any:
		return len(val) == 0
	case []any:
		return len(val) == 0
	case []byte:
		return len(val) == 0
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len() == 0
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return true
		}
		return isPayloadEmpty(rv.Elem().Interface())
	}
	return false
}

// normalizeJSONValue marshals an arbitrary Go value (struct, custom map, slice, primitive)
// and unmarshals it back into standard Go JSON types (map[string]any, []any, float64, string, bool, nil).
func normalizeJSONValue(v any) (any, error) {
	bytes, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(bytes, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ValidateTaskOutput validates a task execution result against the declared expected output contract.
// It normalizes string-encoded JSON payloads and custom Go types into native structures when JSON output is expected,
// verifies non-emptiness when required, and checks required fields.
// Returns the normalized result or a structured *TaskValidationError.
func ValidateTaskOutput(result any, expected *model.TaskExpectedOutput, taskType string) (any, *TaskValidationError) {
	if expected == nil {
		return result, nil
	}

	payload, isWrapped, wrapKey := extractPayload(result)

	// Emptiness check
	if isPayloadEmpty(payload) {
		if !expected.IsAllowEmpty() {
			return nil, &TaskValidationError{
				Code:     ErrCodeEmptyOutput,
				Reason:   "empty output when non-empty result is required",
				TaskType: taskType,
			}
		}
		// If empty is allowed and no specific fields are required, legitimate empty result passes
		if len(expected.Required) == 0 {
			return result, nil
		}
		return nil, &TaskValidationError{
			Code:     ErrCodeMissingRequiredField,
			Reason:   fmt.Sprintf("required field '%s' missing from empty output", expected.Required[0]),
			TaskType: taskType,
		}
	}

	// Type checking and structured decoding
	expType := strings.ToLower(strings.TrimSpace(expected.Type))
	switch expType {
	case "string":
		if _, ok := payload.(string); !ok {
			return nil, &TaskValidationError{
				Code:     ErrCodeTypeMismatch,
				Reason:   fmt.Sprintf("expected string output, got %T", payload),
				TaskType: taskType,
			}
		}
	case "json":
		if strVal, ok := payload.(string); ok {
			var parsed any
			if err := json.Unmarshal([]byte(strVal), &parsed); err != nil {
				return nil, &TaskValidationError{
					Code:     ErrCodeMalformedOutput,
					Reason:   fmt.Sprintf("expected valid JSON output: %v", err),
					TaskType: taskType,
				}
			}
			payload = parsed
			if isWrapped {
				if resMap, ok := result.(map[string]any); ok {
					resMap[wrapKey] = parsed
				}
			} else {
				result = parsed
			}
		} else if _, isMap := payload.(map[string]any); !isMap {
			if _, isSlice := payload.([]any); !isSlice {
				norm, err := normalizeJSONValue(payload)
				if err != nil {
					return nil, &TaskValidationError{
						Code:     ErrCodeMalformedOutput,
						Reason:   fmt.Sprintf("output cannot be represented as JSON: %v", err),
						TaskType: taskType,
					}
				}
				payload = norm
				if isWrapped {
					if resMap, ok := result.(map[string]any); ok {
						resMap[wrapKey] = norm
					}
				} else {
					result = norm
				}
			}
		}
	case "object":
		if strVal, ok := payload.(string); ok {
			var parsed any
			if err := json.Unmarshal([]byte(strVal), &parsed); err != nil {
				return nil, &TaskValidationError{
					Code:     ErrCodeMalformedOutput,
					Reason:   fmt.Sprintf("expected JSON object: %v", err),
					TaskType: taskType,
				}
			}
			parsedMap, isMap := parsed.(map[string]any)
			if !isMap {
				return nil, &TaskValidationError{
					Code:     ErrCodeTypeMismatch,
					Reason:   fmt.Sprintf("expected JSON object, got %T", parsed),
					TaskType: taskType,
				}
			}
			payload = parsedMap
			if isWrapped {
				if resMap, ok := result.(map[string]any); ok {
					resMap[wrapKey] = parsedMap
				}
			} else {
				result = parsedMap
			}
		} else if _, isMap := payload.(map[string]any); !isMap {
			norm, err := normalizeJSONValue(payload)
			if err != nil {
				return nil, &TaskValidationError{
					Code:     ErrCodeMalformedOutput,
					Reason:   fmt.Sprintf("output cannot be represented as JSON object: %v", err),
					TaskType: taskType,
				}
			}
			parsedMap, isMap := norm.(map[string]any)
			if !isMap {
				return nil, &TaskValidationError{
					Code:     ErrCodeTypeMismatch,
					Reason:   fmt.Sprintf("expected object output, got %T", payload),
					TaskType: taskType,
				}
			}
			payload = parsedMap
			if isWrapped {
				if resMap, ok := result.(map[string]any); ok {
					resMap[wrapKey] = parsedMap
				}
			} else {
				result = parsedMap
			}
		}
	case "array":
		if strVal, ok := payload.(string); ok {
			var parsed any
			if err := json.Unmarshal([]byte(strVal), &parsed); err != nil {
				return nil, &TaskValidationError{
					Code:     ErrCodeMalformedOutput,
					Reason:   fmt.Sprintf("expected JSON array: %v", err),
					TaskType: taskType,
				}
			}
			parsedSlice, isSlice := parsed.([]any)
			if !isSlice {
				return nil, &TaskValidationError{
					Code:     ErrCodeTypeMismatch,
					Reason:   fmt.Sprintf("expected JSON array, got %T", parsed),
					TaskType: taskType,
				}
			}
			payload = parsedSlice
			if isWrapped {
				if resMap, ok := result.(map[string]any); ok {
					resMap[wrapKey] = parsedSlice
				}
			} else {
				result = parsedSlice
			}
		} else if _, isSlice := payload.([]any); !isSlice {
			norm, err := normalizeJSONValue(payload)
			if err != nil {
				return nil, &TaskValidationError{
					Code:     ErrCodeMalformedOutput,
					Reason:   fmt.Sprintf("output cannot be represented as JSON array: %v", err),
					TaskType: taskType,
				}
			}
			parsedSlice, isSlice := norm.([]any)
			if !isSlice {
				return nil, &TaskValidationError{
					Code:     ErrCodeTypeMismatch,
					Reason:   fmt.Sprintf("expected array output, got %T", payload),
					TaskType: taskType,
				}
			}
			payload = parsedSlice
			if isWrapped {
				if resMap, ok := result.(map[string]any); ok {
					resMap[wrapKey] = parsedSlice
				}
			} else {
				result = parsedSlice
			}
		}
	}

	// Required fields assertion
	if len(expected.Required) > 0 {
		payloadMap, isMap := payload.(map[string]any)
		rootMap, hasRootMap := result.(map[string]any)

		if !isMap && !hasRootMap {
			// If payload is a struct or custom map that wasn't normalized yet (e.g. if type wasn't specified as object/json)
			if norm, err := normalizeJSONValue(payload); err == nil {
				if nm, ok := norm.(map[string]any); ok {
					payloadMap = nm
					isMap = true
					payload = nm
					if isWrapped {
						if resMap, ok := result.(map[string]any); ok {
							resMap[wrapKey] = nm
						}
					} else {
						result = nm
					}
				}
			}
		}

		if !isMap && !hasRootMap {
			return nil, &TaskValidationError{
				Code:     ErrCodeTypeMismatch,
				Reason:   fmt.Sprintf("cannot validate required fields on non-object payload (%T)", payload),
				TaskType: taskType,
			}
		}

		for _, reqKey := range expected.Required {
			found := false
			if isMap {
				if val, exists := payloadMap[reqKey]; exists && val != nil {
					found = true
				}
			}
			if !found && hasRootMap {
				if val, exists := rootMap[reqKey]; exists && val != nil {
					found = true
				}
			}
			if !found {
				return nil, &TaskValidationError{
					Code:     ErrCodeMissingRequiredField,
					Reason:   fmt.Sprintf("required field '%s' is missing or null", reqKey),
					TaskType: taskType,
				}
			}
		}
	}

	return result, nil
}
