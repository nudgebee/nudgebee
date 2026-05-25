package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"strings"
)

const ToolNewRelicResourceSearchExecute = "newrelic_resource_search_execute"

func init() {
	core.RegisterNBToolFactory(ToolNewRelicResourceSearchExecute, func(accountId string) (core.NBTool, error) {
		return NewRelicResourceSearchTool{}, nil
	})
}

type NewRelicResourceSearchTool struct{}

type NewRelicResourceSearchRequest struct {
	ResourceType string `json:"resource_type"`
	Query        string `json:"query"`
}

func (t NewRelicResourceSearchTool) Name() string { return ToolNewRelicResourceSearchExecute }

func (t NewRelicResourceSearchTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t NewRelicResourceSearchTool) Description() string {
	return `Searches for resources in New Relic. Supports searching for APM services, infrastructure hosts, and containers.`
}

func (t NewRelicResourceSearchTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"resource_type": {
				Type:        core.ToolSchemaTypeString,
				Description: "The type of resource to search for. Supported values: 'apm_services', 'services', 'hosts', 'infrastructure_hosts', 'containers'",
			},
			"query": {
				Type:        core.ToolSchemaTypeString,
				Description: "The search query (e.g., 'name:api-server', 'tags.environment:production')",
			},
		},
		Required: []string{"resource_type", "query"},
	}
}

func (t NewRelicResourceSearchTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	// Parse the request
	var request NewRelicResourceSearchRequest
	if err := common.UnmarshalJson([]byte(input.Command), &request); err != nil {
		return core.NBToolResponse{}, fmt.Errorf("invalid JSON input: %v", err)
	}

	// Validate required fields
	if request.ResourceType == "" {
		return core.NBToolResponse{}, fmt.Errorf("resource_type is required")
	}
	if request.Query == "" {
		return core.NBToolResponse{}, fmt.Errorf("query is required and cannot be empty")
	}

	// Determine which tool to use based on resource type (case-insensitive)
	var toolName string
	resourceTypeLower := strings.ToLower(request.ResourceType)
	switch resourceTypeLower {
	case "apm_services", "services":
		toolName = ToolNewRelicAPMServices
	case "hosts", "infrastructure_hosts":
		toolName = ToolNewRelicInfrastructureHosts
	case "containers":
		toolName = ToolNewRelicInfrastructureContainers
	default:
		return core.NBToolResponse{}, fmt.Errorf("unsupported resource type: %s. Supported types: 'apm_services', 'services', 'hosts', 'infrastructure_hosts', 'containers'", request.ResourceType)
	}

	// Get the target tool
	tool, ok := core.GetNBTool(nbRequestContext.AccountId, toolName)
	if !ok {
		return core.NBToolResponse{}, fmt.Errorf("tool not found: %s", toolName)
	}

	// Delegate to the target tool
	return tool.Call(nbRequestContext, core.NBToolCallRequest{
		Command: request.Query,
	})
}

func (t NewRelicResourceSearchTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return getNewRelicConfigSchema()
}
