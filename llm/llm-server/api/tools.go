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
		// Visibility is account (default), organisation or selected_accounts (#41073).
		Visibility string   `json:"visibility"`
		AccountIds []string `json:"account_ids"`
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
	// Empty account_id means the caller is on a surface with no current
	// account (the global b-Cortex sidebar). Fall back to a tenant-wide
	// read; the service layer shows a tenant-wide reader every account's custom
	// tools and anyone else only those of accounts they can read
	// (CanReadAccountData: a built-in role or an ai_tools grant). System
	// tools are always returned. Custom-agents-as-tools are intentionally
	// omitted from the tenant-wide view — they're already shown on the
	// Agents tab and duplicating them here would be confusing.
	if payload["account_id"] == nil || payload["account_id"] == "" {
		resp := append(core.ListToolsForTenant(context), tenantMCPWriteToolDtos(context)...)
		c.JSON(200, BuildApiResponse(filterToolsByType(resp, payload), nil))
		return
	}
	accountIdPayload, ok := payload["account_id"].(string)
	if !ok || accountIdPayload == "" {
		c.JSON(400, BuildApiResponse(nil, []error{errors.New("tools: invalid payload, account_id must be a non-empty string")}))
		return
	}

	// Check if user has access to account
	if !context.GetSecurityContext().HasAccountAccess(accountIdPayload, security.SecurityAccessTypeRead) &&
		!granted(context.GetSecurityContext(), accountIdPayload, moduleAiTools, "Read", "Write") {
		c.JSON(403, BuildApiResponse(nil, []error{
			common.Error{
				Message: errorUserAccessMessage,
			},
		}))
		return
	}

	resp := core.ListTools(context, accountIdPayload)
	// Organisation tools this account switched off, so the tool list can
	// switch them back on. Pickers don't ask for them.
	if includeSwitchedOff, _ := payload["include_switched_off"].(bool); includeSwitchedOff {
		resp = append(resp, core.ListSwitchedOffOrgTools(accountIdPayload)...)
	}
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
			Visibility: agent.Visibility,
		})
	}
	c.JSON(200, BuildApiResponse(filterToolsByType(resp, payload), nil))
}

// tenantMCPWriteToolDtos lists, for the tenant-wide view, the MCP tools their
// integrations mark as needing approval, from saved settings: listing every MCP
// tool would mean contacting every server of every account (#40399).
func tenantMCPWriteToolDtos(context *security.RequestContext) []core.ToolDto {
	// Integrations are tenant-wide, so only callers who see every account get
	// them, as with custom tools in this view.
	sc := context.GetSecurityContext()
	if !sc.IsTenantAdmin() && !sc.IsTenantReadAdmin() && !sc.IsSuperAdmin() && !sc.IsSuperAdminReadonly() {
		return nil
	}
	tenantID, err := toolApprovalTenantID(context)
	if err != nil {
		return nil
	}
	tools, _, err := toolApprovalMCPWriteTools(context, tenantID)
	if err != nil {
		context.GetLogger().Warn("tools: failed to list MCP tools that need approval", "error", err)
		return nil
	}
	dtos := make([]core.ToolDto, 0, len(tools))
	for _, tool := range tools {
		dtos = append(dtos, core.ToolDto{
			Id:           tool.Integration.ID + ":" + tool.Name,
			Name:         tool.Name,
			Description:  "MCP tool of " + tool.Integration.Name,
			Type:         core.ToolTypeCustom,
			Status:       core.ToolStatusEnabled,
			ExecutorType: core.ToolExecutorTypeMCP,
			NBToolType:   core.NBToolTypeTool,
			Config:       map[string]any{"integration_id": tool.Integration.ID},
			Approval:     core.ToolApprovalAlways,
		})
	}
	return dtos
}

// filterToolsByType keeps the entries of the requested nb_tool_type ("tool" or
// "agent"), so the Tools list can leave out agents the Agents tab already
// shows; without it every entry is returned, as pickers that offer agents need.
func filterToolsByType(tools []core.ToolDto, payload map[string]any) []core.ToolDto {
	wanted, _ := payload["nb_tool_type"].(string)
	if wanted = strings.TrimSpace(wanted); wanted == "" {
		return tools
	}
	// A new slice: the list can share its array with the tool cache.
	kept := make([]core.ToolDto, 0, len(tools))
	for _, tool := range tools {
		if string(tool.NBToolType) == wanted {
			kept = append(kept, tool)
		}
	}
	return kept
}

func toolCreateTool(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request toolCreateRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error(errorBindingMessage, "error", err)
		c.JSON(400, BuildApiResponse(nil, []error{
			common.Error{
				Message: err.Error(),
			},
		}))
		return
	}

	err = common.ValidateStruct(request)
	if err != nil {
		c.JSON(400, BuildApiResponse(nil, []error{
			common.Error{
				Message: err.Error(),
			},
		}))
		return
	}

	// Check if user has access to account. An organisation tool needs a
	// tenant admin, which the service checks.
	orgTool := request.Tool.Visibility != "" && request.Tool.Visibility != core.VisibilityAccount
	if !orgTool && !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeCreate) &&
		!grantedWrite(context.GetSecurityContext(), request.AccountId, moduleAiTools) {
		c.JSON(403, BuildApiResponse(nil, []error{
			common.Error{
				Message: errorUserAccessMessage,
			},
		}))
		return
	}

	var config map[string]any
	switch request.Tool.ExecutorType {
	case core.ToolExecutorTypeMCP:
		c.JSON(400, BuildApiResponse(nil, []error{errors.New("MCP tools are now managed via integrations, create an MCP integration instead of an MCP tool")}))
		return
	case core.ToolExecutorTypeContainer:
		config = request.Tool.Config
	default:
		c.JSON(400, BuildApiResponse(nil, []error{errors.New("tools: invalid executor_type, must be 'container'")}))
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
		Visibility:   request.Tool.Visibility,
		AccountIds:   request.Tool.AccountIds,
	})
	if err != nil {
		c.JSON(serviceErrorStatus(err), BuildApiResponse(nil, []error{toApiError(err)}))
		return
	}

	c.JSON(200, BuildApiResponse(resp, nil))
}

func toolDeleteTool(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	if payload["name"] == nil || payload["name"] == "" {
		c.JSON(400, BuildApiResponse(nil, []error{errors.New("agents: invalid payload, name is required")}))
		return
	}
	// An empty account_id deletes an organisation tool (tenant admin, checked by the service).
	accountIdPayload, okAccount := payload["account_id"].(string)
	if payload["account_id"] != nil && !okAccount {
		c.JSON(400, BuildApiResponse(nil, []error{errors.New("tools: invalid payload, account_id must be a string")}))
		return
	}
	namePayload, okName := payload["name"].(string)
	if !okName || namePayload == "" {
		c.JSON(400, BuildApiResponse(nil, []error{errors.New("tools: invalid payload, name must be a non-empty string")}))
		return
	}

	// Check if user has access to account
	if accountIdPayload != "" && !context.GetSecurityContext().HasAccountAccess(accountIdPayload, security.SecurityAccessTypeDelete) &&
		!grantedWrite(context.GetSecurityContext(), accountIdPayload, moduleAiTools) {
		c.JSON(403, BuildApiResponse(nil, []error{
			common.Error{
				Message: errorUserAccessMessage,
			},
		}))
		return
	}

	err := core.DeleteCustomTool(context, accountIdPayload, namePayload)
	if err != nil {
		slog.Error("tools: failed to delete custom tool", "error", err, "account_id", accountIdPayload, "name", namePayload)
		c.JSON(serviceErrorStatus(err), BuildApiResponse(nil, []error{toApiError(err)}))
	} else {
		c.JSON(200, BuildApiResponse(map[string]any{
			"status": "ok",
		}, nil)) // Pass nil for errors on success
	}

}

func toolUpdateTool(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	request := toolUpdateRequest{}
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error(errorBindingMessage, "error", err)
		c.JSON(400, BuildApiResponse(nil, []error{
			common.Error{
				Message: err.Error(),
			},
		}))
		return
	}

	err = common.ValidateStruct(request)
	if err != nil {
		c.JSON(400, BuildApiResponse(nil, []error{
			common.Error{
				Message: err.Error(),
			},
		}))
		return
	}

	// An empty account_id updates an organisation tool (tenant admin, checked by the service).
	if request.AccountId != "" && !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeUpdate) &&
		!grantedWrite(context.GetSecurityContext(), request.AccountId, moduleAiTools) {
		c.JSON(403, BuildApiResponse(nil, []error{
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
		c.JSON(serviceErrorStatus(err), BuildApiResponse(nil, []error{toApiError(err)}))
		return
	}

	c.JSON(200, BuildApiResponse(tool, nil))
}

func handleToolsApis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	groupV2 := r.Group("/v1/tools")

	groupV2.POST("", func(c *gin.Context) {
		var actionRequest ActionRequest
		err := c.ShouldBindJSON(&actionRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(400, BuildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		if actionRequest.Action.Name == "" {
			c.JSON(400, BuildApiResponse(nil, []error{errors.New("tools: invalid payload, action.name is required")}))
			return
		}

		context, err := BuildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.With())
		if err != nil {
			c.JSON(401, BuildApiResponse(nil, []error{err}))
			return
		}

		payload := actionRequest.Input
		if rawRequest, ok := payload["request"]; ok {
			if castedRequest, castOk := rawRequest.(map[string]any); castOk {
				payload = castedRequest
			} else {
				c.JSON(400, BuildApiResponse(nil, []error{errors.New("tools: invalid payload, request field is not a valid object")}))
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
		case "ai_update_tool_visibility":
			common.MetricsApiRequestsTotal("tools_visibility_update")
			toolUpdateVisibility(c, context, payload)
			return
		case "ai_update_tool_account_override":
			common.MetricsApiRequestsTotal("tools_account_override_update")
			toolUpdateAccountOverride(c, context, payload)
			return
		case "ai_list_tool_account_overrides":
			common.MetricsApiRequestsTotal("tools_account_overrides_list")
			toolListAccountOverrides(c, context, payload)
			return
		default:
			c.JSON(400, BuildApiResponse(nil, []error{errors.New("tools: invalid payload, unsupported action")}))
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
			c.JSON(400, BuildApiResponse(nil, []error{
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
			c.JSON(400, BuildApiResponse(nil, []error{
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

		context, err := BuildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.With())
		if err != nil {
			c.JSON(401, BuildApiResponse(nil, []error{err}))
			return
		}

		accountId, ok := actionRequestPayload["account_id"].(string)
		if !ok || accountId == "" {
			c.JSON(400, BuildApiResponse(nil, []error{errors.New("tools: account_id is required in request body")}))
			return
		}

		// Check if user has access to account
		if !context.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) &&
			!granted(context.GetSecurityContext(), accountId, moduleAiTools, "Read", "Write") {
			c.JSON(403, BuildApiResponse(nil, []error{
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
			c.JSON(500, BuildApiResponse(nil, []error{
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

		c.JSON(200, BuildApiResponse(response, nil))
	})

}

func toolUpdateVisibility(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	request := orgItemVisibilityRequest{}
	if err := common.DecodeMapToStruct(payload, &request); err != nil || request.Id == "" || request.Visibility == "" {
		c.JSON(400, BuildApiResponse(nil, []error{errors.New("tools: invalid payload, id and visibility are required")}))
		return
	}
	if err := core.SetCustomToolVisibility(context, request.Id, request.Visibility, request.AccountIds, request.TargetAccountId); err != nil {
		c.JSON(400, BuildApiResponse(nil, []error{err}))
		return
	}
	c.JSON(200, BuildApiResponse(map[string]any{"status": "ok"}, nil))
}

func toolUpdateAccountOverride(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	request := orgItemAccountOverrideRequest{}
	if err := common.DecodeMapToStruct(payload, &request); err != nil || request.Id == "" || request.AccountId == "" {
		c.JSON(400, BuildApiResponse(nil, []error{errors.New("tools: invalid payload, id and account_id are required")}))
		return
	}
	if err := core.SetCustomToolAccountSwitch(context, request.Id, request.AccountId, request.Enabled); err != nil {
		c.JSON(400, BuildApiResponse(nil, []error{err}))
		return
	}
	c.JSON(200, BuildApiResponse(map[string]any{"status": "ok"}, nil))
}

func toolListAccountOverrides(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	id, _ := payload["id"].(string)
	if id == "" {
		c.JSON(400, BuildApiResponse(nil, []error{errors.New("tools: invalid payload, id is required")}))
		return
	}
	resp, err := core.ListCustomToolAccountSwitches(context, id)
	if err != nil {
		c.JSON(400, BuildApiResponse(nil, []error{err}))
		return
	}
	c.JSON(200, BuildApiResponse(resp, nil))
}
