package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

const ToolDatadogResourceSearchExecute = "datadog_resource_search_execute"

func init() {
	core.RegisterNBToolFactory(ToolDatadogResourceSearchExecute, func(accountId string) (core.NBTool, error) {
		return DatadogResourceSearchTool{}, nil
	})
}

type DatadogResourceSearchTool struct{}

type DatadogResourceSearchRequest struct {
	ResourceType string `json:"resource_type"`
	Query        string `json:"query"`
}

func (t DatadogResourceSearchTool) Name() string {
	return ToolDatadogResourceSearchExecute
}

func (t DatadogResourceSearchTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (t DatadogResourceSearchTool) Description() string {
	return `Searches for resources in Datadog.`
}

func (t DatadogResourceSearchTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"resource_type": {
				Type:        core.ToolSchemaTypeString,
				Description: "The type of resource to search for. Supported values: 'services', 'containers', 'apm_entities'.",
			},
			"query": {
				Type:        core.ToolSchemaTypeString,
				Description: "The search query.",
			},
		},
		Required: []string{"resource_type", "query"},
	}
}

func (t DatadogResourceSearchTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	var request DatadogResourceSearchRequest
	if err := common.UnmarshalJson([]byte(input.Command), &request); err != nil {
		return core.NBToolResponse{}, fmt.Errorf("invalid JSON input: %v", err)
	}

	var toolName string
	switch request.ResourceType {
	case "services", "catalog":
		toolName = ToolDatadogSoftwareCatalog
	case "containers":
		toolName = ToolDatadogContainersExecute
	case "apm_entities":
		toolName = ToolDatadogAPMEntities
	default:
		return core.NBToolResponse{}, fmt.Errorf("unsupported resource type: %s", request.ResourceType)
	}

	tool, ok := core.GetNBTool(nbRequestContext.AccountId, toolName)
	if !ok {
		return core.NBToolResponse{}, fmt.Errorf("tool not found: %s", toolName)
	}

	return tool.Call(nbRequestContext, core.NBToolCallRequest{
		Command: request.Query,
	})
}

func (t DatadogResourceSearchTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return getDatadogConfigSchema()
}
