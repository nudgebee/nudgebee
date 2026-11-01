package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"nudgebee/llm/tools/core"
)

// addDelegatedTargetArgument preserves the model's tool input and adds only
// the selector that the specific execution tool has chosen to inherit.
func addDelegatedTargetArgument(input, field, name string) (string, error) {
	var fields map[string]json.RawMessage
	if strings.HasPrefix(strings.TrimSpace(input), "{") {
		if err := json.Unmarshal([]byte(input), &fields); err != nil {
			return "", fmt.Errorf("decode tool input for delegated target: %w", err)
		}
	} else {
		command, err := json.Marshal(input)
		if err != nil {
			return "", fmt.Errorf("encode tool command for delegated target: %w", err)
		}
		fields = map[string]json.RawMessage{"command": command}
	}
	value, err := json.Marshal(name)
	if err != nil {
		return "", fmt.Errorf("encode delegated target: %w", err)
	}
	fields[field] = value
	encoded, err := json.Marshal(fields)
	if err != nil {
		return "", fmt.Errorf("encode tool input with delegated target: %w", err)
	}
	return string(encoded), nil
}

// AddDelegatedTargetObject preserves the model's tool input and injects or updates
// the target object argument ({provider, name}).
func AddDelegatedTargetObject(input string, target *core.ExecutionTarget) (string, error) {
	if target == nil {
		return input, nil
	}
	var fields map[string]json.RawMessage
	if strings.HasPrefix(strings.TrimSpace(input), "{") {
		if err := json.Unmarshal([]byte(input), &fields); err != nil {
			return "", fmt.Errorf("decode tool input for delegated target: %w", err)
		}
	} else {
		command, err := json.Marshal(input)
		if err != nil {
			return "", fmt.Errorf("encode tool command for delegated target: %w", err)
		}
		fields = map[string]json.RawMessage{"command": command}
	}
	// Do not override an already explicit target object.
	if _, exists := fields["target"]; exists {
		return input, nil
	}
	targetBytes, err := json.Marshal(struct {
		Provider string `json:"provider"`
		Name     string `json:"name"`
	}{
		Provider: target.Provider,
		Name:     target.Name,
	})
	if err != nil {
		return "", fmt.Errorf("encode delegated target: %w", err)
	}
	fields["target"] = targetBytes
	encoded, err := json.Marshal(fields)
	if err != nil {
		return "", fmt.Errorf("encode tool input with delegated target: %w", err)
	}
	return string(encoded), nil
}
