package workflow

import (
	"fmt"
	"nudgebee/runbook/internal/model"
	"os"

	"gopkg.in/yaml.v3"
)

// ParseFile reads a YAML workflow file from the given path and returns a Workflow object.
func ParseFile(filepath string) (*model.Workflow, error) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return Parse(data)
}

// Parse reads YAML workflow data from a byte slice and returns a Workflow object.
func Parse(data []byte) (*model.Workflow, error) {
	var workflow model.Workflow
	err := yaml.Unmarshal(data, &workflow)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal yaml: %w", err)
	}

	return &workflow, nil
}
