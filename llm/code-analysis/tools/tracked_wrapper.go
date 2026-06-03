package tools

import (
	"context"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/tools/core"
)

// TrackedToolWrapper wraps any tool to automatically track its invocations
type TrackedToolWrapper struct {
	tool    core.NBTool
	tracker *common.ToolInvocationTracker
	logger  *common.Logger
}

// NewTrackedToolWrapper creates a wrapper that tracks tool invocations
func NewTrackedToolWrapper(tool core.NBTool, tracker *common.ToolInvocationTracker, logger *common.Logger) *TrackedToolWrapper {
	return &TrackedToolWrapper{
		tool:    tool,
		tracker: tracker,
		logger:  logger,
	}
}

// Name returns the underlying tool's name
func (w *TrackedToolWrapper) Name() string {
	return w.tool.Name()
}

// Description returns the underlying tool's description
func (w *TrackedToolWrapper) Description() string {
	return w.tool.Description()
}

// InputSchema returns the underlying tool's input schema
func (w *TrackedToolWrapper) InputSchema() core.ToolSchema {
	return w.tool.InputSchema()
}

// GetType returns the underlying tool's type
func (w *TrackedToolWrapper) GetType() core.NBToolType {
	return w.tool.GetType()
}

// IsReadOnly delegates to the wrapped tool if it implements ReadOnlyTool.
func (w *TrackedToolWrapper) IsReadOnly() bool {
	if ro, ok := w.tool.(core.ReadOnlyTool); ok {
		return ro.IsReadOnly()
	}
	return false
}

// Execute wraps the tool execution with comprehensive tracking
func (w *TrackedToolWrapper) Execute(ctx context.Context, input map[string]any) core.NBToolResponse {
	toolName := w.tool.Name()

	// Convert input to map[string]any for tracking and sanitize sensitive data
	inputForTracking := w.sanitizeInput(input)

	// Check if intention is provided in the input
	var invocationID string
	if intention, ok := input["__intention"].(string); ok && intention != "" {
		// Remove intention from actual tool input
		sanitizedInput := make(map[string]any)
		for k, v := range input {
			if k != "__intention" {
				sanitizedInput[k] = v
			}
		}
		input = sanitizedInput

		// Start tracking with intention
		invocationID = w.tracker.StartInvocationWithIntention(toolName, inputForTracking, intention)

		// Log intention if logger is available
		if w.logger != nil {
			w.logger.Log(common.EventToolStart, "Tool execution started with intention", map[string]any{
				"tool_name":     toolName,
				"invocation_id": invocationID,
				"intention":     intention,
			})
		}
	} else {
		// Start tracking normally
		invocationID = w.tracker.StartInvocation(toolName, inputForTracking)

		// Log tool start if logger is available
		if w.logger != nil {
			w.logger.Log(common.EventToolStart, "Tool execution started", map[string]any{
				"tool_name":     toolName,
				"invocation_id": invocationID,
			})
		}
	}

	// Execute the actual tool
	response := w.tool.Execute(ctx, input)

	// Complete tracking with results
	w.tracker.CompleteInvocation(invocationID, w.sanitizeOutput(response), response.Status, nil)

	// Add metadata about the execution
	w.tracker.AddMetadata(invocationID, "execution_context", "agent_managed")
	if response.Observation != "" {
		w.tracker.AddMetadata(invocationID, "observation", response.Observation)
	}

	// Log tool completion if logger is available
	if w.logger != nil {
		w.logger.Log(common.EventToolComplete, "Tool execution completed", map[string]any{
			"tool_name":     toolName,
			"invocation_id": invocationID,
			"status":        response.Status,
			"duration":      w.getLastInvocationDuration(invocationID),
		})
	}

	return response
}

// sanitizeInput removes sensitive information from tool input
func (w *TrackedToolWrapper) sanitizeInput(input map[string]any) map[string]any {
	inputForTracking := make(map[string]any)
	for k, v := range input {
		// Sanitize sensitive fields
		if k == "credentials" || k == "password" || k == "token" || k == "github_token" {
			inputForTracking[k] = "***REDACTED***"
		} else if str, ok := v.(string); ok && w.looksLikeToken(str) {
			inputForTracking[k] = "***REDACTED***"
		} else {
			inputForTracking[k] = v
		}
	}
	return inputForTracking
}

// looksLikeToken checks if a string looks like a sensitive token
func (w *TrackedToolWrapper) looksLikeToken(s string) bool {
	if len(s) < 10 {
		return false
	}

	// GitHub token patterns
	if len(s) >= 36 && (s[:4] == "ghp_" || s[:4] == "ghs_" || s[:11] == "github_pat_") {
		return true
	}

	// Other long alphanumeric strings that might be tokens
	if len(s) > 30 {
		// Check if it's mostly alphanumeric/base64-like
		alphanumCount := 0
		for _, char := range s {
			if (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') ||
				(char >= '0' && char <= '9') || char == '_' || char == '-' || char == '+' || char == '/' || char == '=' {
				alphanumCount++
			}
		}
		if float64(alphanumCount)/float64(len(s)) > 0.8 {
			return true
		}
	}

	return false
}

// sanitizeOutput removes sensitive information from tool output
func (w *TrackedToolWrapper) sanitizeOutput(response core.NBToolResponse) any {
	// Convert response to a map for sanitization
	responseMap := map[string]any{
		"status":      response.Status,
		"result":      response.Result,
		"observation": response.Observation,
	}

	if response.Error != "" {
		responseMap["error"] = response.Error
	}

	// Add data if present, but sanitize it
	if response.Data != nil {
		if dataMap, ok := response.Data.(map[string]any); ok {
			sanitizedData := make(map[string]any)
			for k, v := range dataMap {
				if k == "credentials" || k == "password" || k == "token" {
					sanitizedData[k] = "***REDACTED***"
				} else {
					sanitizedData[k] = v
				}
			}
			responseMap["data"] = sanitizedData
		} else {
			responseMap["data"] = response.Data
		}
	}

	return responseMap
}

// getLastInvocationDuration gets the duration of the most recent invocation
func (w *TrackedToolWrapper) getLastInvocationDuration(invocationID string) string {
	invocations := w.tracker.GetInvocations()
	for i := len(invocations) - 1; i >= 0; i-- {
		if invocations[i].ID == invocationID {
			return invocations[i].Duration
		}
	}
	return "unknown"
}

// GetTracker returns the underlying tracker (for access to invocation data)
func (w *TrackedToolWrapper) GetTracker() *common.ToolInvocationTracker {
	return w.tracker
}

// GetWrappedTool returns the underlying tool
func (w *TrackedToolWrapper) GetWrappedTool() core.NBTool {
	return w.tool
}

// WrapToolsWithTracking wraps a map of tools with tracking
func WrapToolsWithTracking(tools map[string]core.NBTool, tracker *common.ToolInvocationTracker, logger *common.Logger) map[string]core.NBTool {
	wrappedTools := make(map[string]core.NBTool)

	for name, tool := range tools {
		wrappedTools[name] = NewTrackedToolWrapper(tool, tracker, logger)
	}

	return wrappedTools
}

// ExtractTrackersFromTools extracts all trackers from wrapped tools
func ExtractTrackersFromTools(tools map[string]core.NBTool) []*common.ToolInvocationTracker {
	var trackers []*common.ToolInvocationTracker

	for _, tool := range tools {
		if wrappedTool, ok := tool.(*TrackedToolWrapper); ok {
			trackers = append(trackers, wrappedTool.GetTracker())
		}
	}

	return trackers
}
