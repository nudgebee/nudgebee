package core

import (
	"encoding/json"
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
)

// PrintTask implements a task that simply prints the input message.
// Useful for troubleshooting and debugging template rendering.
type PrintTask struct{}

// GetName returns the unique name of the task.
func (t *PrintTask) GetName() string {
	return "core.print"
}

// GetDescription returns a brief description of the task.
func (t *PrintTask) GetDescription() string {
	return "Log a message to the output. Useful for debugging templates."
}

// GetDisplayName returns a human-readable name for the task.
func (t *PrintTask) GetDisplayName() string {
	return "Print"
}

// Execute runs the core logic of the task.
func (t *PrintTask) Execute(taskCtx types.TaskContext, params map[string]any) (result any, err error) {
	messageParam, ok := params["message"]
	if !ok {
		return nil, fmt.Errorf("missing or invalid 'message' parameter")
	}

	var message string
	if s, ok := messageParam.(string); ok {
		message = s
	} else {
		// Try pretty printing JSON for complex objects
		b, err := json.MarshalIndent(messageParam, "", "  ")
		if err == nil {
			message = string(b)
		} else {
			message = fmt.Sprintf("%v", messageParam)
		}
	}

	taskCtx.GetLogger().Info("PrintTask Executed", "message", message)

	// Return the message directly as 'data'
	return map[string]string{
		"data": message,
	}, nil
}

// InputSchema returns the schema for the task's expected parameters.
func (t *PrintTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"message": {
				Type:        "string",
				Description: "The message to print.",
				Required:    true,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *PrintTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        "string",
				Description: "The printed message.",
				Required:    true,
			},
		},
	}
}
