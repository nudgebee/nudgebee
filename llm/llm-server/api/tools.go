package api

import (
	"errors"
	"log/slog"
	agentcore "nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"strings"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type toolCreateRequest struct {
	AccountId string `json:"account_id"`
	Tool      struct {
		Name         string                `json:"name"`
		Description  string                `json:"description"`
		ExecutorType core.ToolExecutorType `json:"executor_type"`
		Config       map[string]any        `json:"config"`
		Status       core.ToolStatus       `json:"status"`
		InputSchema  core.ToolSchema       `json:"schema" db:"input_schema"`
	} `json:"tool"`
}

type toolUpdateRequest struct {
	AccountId string `json:"account_id"`
	Tool      struct {
		Id           string                `json:"id"`
		Name         string                `json:"name"`
		Description  string                `json:"description"`
		ExecutorType core.ToolExecutorType `json:"executor_type"`
		Config       map[string]any        `json:"config"`
		Status       core.ToolStatus       `json:"status"`
		InputSchema  core.ToolSchema       `json:"schema" db:"input_schema"`
	} `json:"tool"`
}

func toolListTool(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	if payload["account_id"] == nil || payload["account_id"] == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("tools: invalid payload, account_id is required")}))
		return
	}
	accountIdPayload, ok := payload["account_id"].(string)
	if !ok || accountIdPayload == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("tools: invalid payload, account_id must be a non-empty string")}))
		return
	}

	// Check if user has access to account
	if !context.GetSecurityContext().HasAccountAccess(accountIdPayload, security.SecurityAccessTypeRead) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{
				Message: errorUserAccessMessage,
			},
		}))
		return
	}

	resp := core.ListTools(context, accountIdPayload)
	customAgents := agentcore.ListCustomAgents(context, accountIdPayload, true)
	for _, agent := range customAgents {
		// Add custom agents as tools
		resp = append(resp, core.ToolDto{
			Id:           agent.Id,
			Name:         agent.Name,
			Description:  agent.Description,
			Type:         core.ToolTypeCustom,
			Status:       core.ToolStatusEnabled,
			NBToolType:   core.NBToolTypeAgent,
			IsConfigured: true,
			NeedsConfig:  false,
			Config: map[string]any{
				"agent_id": agent.Id,
			},
		})
	}
	c.JSON(200, buildApiResponse(resp, nil))
}

func toolCreateTool(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request toolCreateRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error(errorBindingMessage, "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{
				Message: err.Error(),
			},
		}))
		return
	}

	err = common.ValidateStruct(request)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{
				Message: err.Error(),
			},
		}))
		return
	}

	// Check if user has access to account
	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeCreate) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{
				Message: errorUserAccessMessage,
			},
		}))
		return
	}

	var config map[string]any
	switch request.Tool.ExecutorType {
	case core.ToolExecutorTypeMCP:
		c.JSON(400, buildApiResponse(nil, []error{errors.New("MCP tools are now managed via integrations, create an MCP integration instead of an MCP tool")}))
		return
	case core.ToolExecutorTypeContainer:
		config = request.Tool.Config
	default:
		c.JSON(400, buildApiResponse(nil, []error{errors.New("tools: invalid executor_type, must be 'container'")}))
		return
	}

	resp, err := core.CreateCustomTool(context, request.AccountId, core.ToolDto{
		Name:         request.Tool.Name,
		Description:  request.Tool.Description,
		Config:       config,
		Type:         core.ToolTypeCustom,
		Status:       request.Tool.Status,
		InputSchema:  request.Tool.InputSchema,
		NBToolType:   core.NBToolTypeTool,
		ExecutorType: request.Tool.ExecutorType,
		CreatedBy:    context.GetSecurityContext().GetUserId(),
	})
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{
				Message: err.Error(),
			},
		}))
		return
	}

	c.JSON(200, buildApiResponse(resp, nil))
}

func toolDeleteTool(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	if payload["account_id"] == nil || payload["account_id"] == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("agents: invalid payload, account_id is required")}))
		return
	}

	if payload["name"] == nil || payload["name"] == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("agents: invalid payload, name is required")}))
		return
	}
	accountIdPayload, okAccount := payload["account_id"].(string)
	if !okAccount || accountIdPayload == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("tools: invalid payload, account_id must be a non-empty string")}))
		return
	}
	namePayload, okName := payload["name"].(string)
	if !okName || namePayload == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("tools: invalid payload, name must be a non-empty string")}))
		return
	}

	// Check if user has access to account
	if !context.GetSecurityContext().HasAccountAccess(accountIdPayload, security.SecurityAccessTypeDelete) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{
				Message: errorUserAccessMessage,
			},
		}))
		return
	}

	err := core.DeleteCustomTool(context, accountIdPayload, namePayload)
	if err != nil {
		// Consider if different error types from core.DeleteCustomTool
		// might warrant different HTTP status codes (e.g., 404 if not found vs 500 for other errors)
		// For now, a generic 500 is better than 200 on error.
		slog.Error("tools: failed to delete custom tool", "error", err, "account_id", accountIdPayload, "name", namePayload)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{
				Message: "tools: failed to delete tool: " + err.Error(),
			},
		}))
	} else {
		c.JSON(200, buildApiResponse(map[string]any{
			"status": "ok",
		}, nil)) // Pass nil for errors on success
	}

}

func toolUpdateTool(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	request := toolUpdateRequest{}
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error(errorBindingMessage, "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{
				Message: err.Error(),
			},
		}))
		return
	}

	err = common.ValidateStruct(request)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{
				Message: err.Error(),
			},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("tools: invalid payload, account_id is required")}))
		return
	}

	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeUpdate) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{
				Message: errorUserAccessMessage,
			},
		}))
		return
	}

	// Compose ToolDto for update
	tool := core.ToolDto{
		Id:           request.Tool.Id,
		Name:         request.Tool.Name,
		Description:  request.Tool.Description,
		Config:       request.Tool.Config,
		Status:       request.Tool.Status,
		InputSchema:  request.Tool.InputSchema,
		ExecutorType: request.Tool.ExecutorType,
		NBToolType:   core.NBToolTypeTool,
	}

	err = core.UpdateCustomTool(context, request.AccountId, request.Tool.Name, tool)
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{
				Message: err.Error(),
			},
		}))
		return
	}

	c.JSON(200, buildApiResponse(tool, nil))
}

func handleToolsApis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	groupV2 := r.Group("/v1/tools")

	groupV2.POST("", func(c *gin.Context) {
		var actionRequest ActionRequest
		err := c.ShouldBindJSON(&actionRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		if actionRequest.Action.Name == "" {
			c.JSON(400, buildApiResponse(nil, []error{errors.New("tools: invalid payload, action.name is required")}))
			return
		}

		context, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.With())
		if err != nil {
			c.JSON(401, buildApiResponse(nil, []error{err}))
			return
		}

		payload := actionRequest.Input
		if rawRequest, ok := payload["request"]; ok {
			if castedRequest, castOk := rawRequest.(map[string]any); castOk {
				payload = castedRequest
			} else {
				c.JSON(400, buildApiResponse(nil, []error{errors.New("tools: invalid payload, request field is not a valid object")}))
				return
			}
		}

		switch actionRequest.Action.Name {
		case "ai_list_tools":
			common.MetricsApiRequestsTotal("tools_list")
			toolListTool(c, context, payload)
			return
		case "ai_create_tool":
			common.MetricsApiRequestsTotal("tools_create")
			toolCreateTool(c, context, payload)
			return
		case "ai_update_tool":
			common.MetricsApiRequestsTotal("tools_update")
			toolUpdateTool(c, context, payload)
			return
		case "ai_delete_tool":
			common.MetricsApiRequestsTotal("tools_delete")
			toolDeleteTool(c, context, payload)
			return
		default:
			c.JSON(400, buildApiResponse(nil, []error{errors.New("tools: invalid payload, unsupported action")}))
			return
		}
	})

	// POST endpoint to list all configs for an account (account-level only)
	groupV2.POST("/configs", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("tools_list_all_configs")

		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error("tools: failed to bind request", "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{
					Message: "tools: " + err.Error(),
				},
			}))
			return
		}

		var actionRequest ActionRequest
		err = common.DecodeMapToStruct(requestMap, &actionRequest)
		if err != nil {
			slog.Error("tools: failed to decode rpc request", "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{
					Message: "tools: " + err.Error(),
				},
			}))
			return
		}

		actionRequestPayload := actionRequest.Input
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}

		context, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.With())
		if err != nil {
			c.JSON(401, buildApiResponse(nil, []error{err}))
			return
		}

		accountId, ok := actionRequestPayload["account_id"].(string)
		if !ok || accountId == "" {
			c.JSON(400, buildApiResponse(nil, []error{errors.New("tools: account_id is required in request body")}))
			return
		}

		// Check if user has access to account
		if !context.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
			c.JSON(403, buildApiResponse(nil, []error{
				common.Error{
					Message: errorUserAccessMessage,
				},
			}))
			return
		}

		// List all configs for this account (both account-level and tenant-level)
		configs, err := core.ListAllToolConfigs(context, accountId)
		if err != nil {
			slog.Error("tools: failed to list all configs", "error", err, "account_id", accountId)
			c.JSON(500, buildApiResponse(nil, []error{
				common.Error{
					Message: "tools: failed to list configs",
				},
			}))
			return
		}

		simplifiedConfigs := make([]map[string]any, 0, len(configs))
		for _, config := range configs {
			// Convert Values array to a simple map, excluding sensitive fields
			valuesMap := make(map[string]any)
			for _, v := range config.Values {
				// Filter out sensitive configuration values
				lowerName := strings.ToLower(v.Name)
				if !strings.Contains(lowerName, "password") &&
					!strings.Contains(lowerName, "secret") &&
					!strings.Contains(lowerName, "token") &&
					!strings.Contains(lowerName, "key") &&
					!strings.Contains(lowerName, "credential") &&
					!strings.Contains(lowerName, "auth") &&
					!strings.Contains(lowerName, "api_key") &&
					!strings.Contains(lowerName, "access") {
					valuesMap[v.Name] = v.Value
				}
			}

			simplifiedConfig := map[string]any{
				"name":   config.Name,
				"type":   config.Schema.ConfigType,
				"values": valuesMap,
			}

			// Only include tags if they exist
			if len(config.Tags) > 0 {
				simplifiedConfig["tags"] = config.Tags
			}

			simplifiedConfigs = append(simplifiedConfigs, simplifiedConfig)
		}

		response := map[string]any{
			"configs": simplifiedConfigs,
		}

		c.JSON(200, buildApiResponse(response, nil))
	})

}
