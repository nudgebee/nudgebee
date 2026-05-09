package core

import (
	"context"
	"encoding/json"
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

// Helper function to parse input parameters
func ParseInput(input map[string]any, target any) error {
	jsonData, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return json.Unmarshal(jsonData, target)
}
