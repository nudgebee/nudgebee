package api

import (
	"errors"
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type agentCreateRequest struct {
	AccountId     string             `json:"account_id"`
	Agent         core.AgentDto      `json:"agent"`
	Rags          []core.AgentRagDto `json:"rags"`
	OverrideAgent *bool              `json:"override_agent"`
}

type agentUpdateRequest struct {
	AccountId string              `json:"account_id"`
	Agent     core.AgentUpdateDto `json:"agent"`
	Rags      []core.AgentRagDto  `json:"rags"`
}

type CreateAgentExtensionRequest struct {
	AccountId string              `json:"account_id"`
	Agent     core.AgentExtension `json:"agent"`
}

type UpdateAgentExtensionRequest struct {
	AccountId string              `json:"account_id"`
	Agent     core.AgentExtension `json:"agent"`
}

type DeleteAgentExtensionRequest struct {
	AccountId string `json:"account_id"`
	AgentName string `json:"agent_name"`
}

func agentListAgent(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	if payload["account_id"] == nil || payload["account_id"] == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("agents: invalid payload, account_id is required")}))
		return
	}
	accountIdPayload, ok := payload["account_id"].(string)
	if !ok || accountIdPayload == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("agents: invalid payload, account_id must be a non-empty string")}))
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
	allowOnlyEnabled := false
	resp := core.ListAgents(context, accountIdPayload, allowOnlyEnabled)
	c.JSON(200, buildApiResponse(resp, nil))
}

func agentCreateAgent(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	request := agentCreateRequest{}
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

	// allows customization of system agents
	overrideAgent := false
	if request.OverrideAgent != nil {
		overrideAgent = *request.OverrideAgent
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
	request.Agent.CreatedBy = context.GetSecurityContext().GetUserId()
	request.Agent.Type = core.AgentTypeCustom

	resp, err := core.CreateCustomAgent(context, request.AccountId, request.Agent, request.Rags, overrideAgent)
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

func agentDeleteAgent(c *gin.Context, context *security.RequestContext, payload map[string]any) {
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
		c.JSON(400, buildApiResponse(nil, []error{errors.New("agents: invalid payload, account_id must be a non-empty string")}))
		return
	}
	namePayload, okName := payload["name"].(string)
	if !okName || namePayload == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("agents: invalid payload, name must be a non-empty string")}))
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

	err := core.DeleteCustomAgent(context, accountIdPayload, namePayload)
	if err != nil {
		// Consider if different error types from core.DeleteCustomAgent
		// might warrant different HTTP status codes (e.g., 404 if not found vs 500 for other errors)
		// For now, a generic 500 is better than 200 on error.
		slog.Error("agentDeleteAgent: failed to delete custom agent", "error", err, "account_id", accountIdPayload, "name", namePayload)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{
				Message: "agents: failed to delete agent: " + err.Error(),
			},
		}))
	} else {
		c.JSON(200, buildApiResponse(map[string]any{
			"status": "ok",
		}, nil)) // Pass nil for errors on success
	}

}

func agentUpdateAgent(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	request := agentUpdateRequest{}
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
		c.JSON(400, buildApiResponse(nil, []error{errors.New("agents: invalid payload, account_id is required")}))
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
	request.Agent.UpdatedBy = context.GetSecurityContext().GetUserId()
	request.Agent.UpdatedAt = time.Now()

	resp, err := core.UpdateCustomAgent(context, request.AccountId, request.Agent)
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

func createAgentExtension(c *gin.Context, context *security.RequestContext, payload map[string]any) {

	request := CreateAgentExtensionRequest{}
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
		c.JSON(400, buildApiResponse(nil, []error{errors.New("agents: invalid payload, account_id is required")}))
		return
	}

	if request.Agent.AgentName == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("agents: invalid payload, agent name is required")}))
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

	createdBy := context.GetSecurityContext().GetUserId()

	resp, err := core.CreateAgentExtension(context, request.AccountId, request.Agent, createdBy)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}

	c.JSON(200, buildApiResponse(resp, nil))
}

func updateAgentExtension(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	request := UpdateAgentExtensionRequest{}
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
		c.JSON(400, buildApiResponse(nil, []error{errors.New("agents: invalid payload, account_id is required")}))
		return
	}

	if request.Agent.AgentName == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("agents: invalid payload, agent name is required")}))
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

	updatedBy := context.GetSecurityContext().GetUserId()

	resp, err := core.UpdateAgentExtension(context, request.AccountId, request.Agent, updatedBy)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}

	c.JSON(200, buildApiResponse(resp, nil))
}

func deleteAgentExtension(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	request := DeleteAgentExtensionRequest{}
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
		c.JSON(400, buildApiResponse(nil, []error{errors.New("agents: invalid payload, account_id is required")}))
		return
	}

	if request.AgentName == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("agents: invalid payload, agent_name is required")}))
		return
	}

	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeDelete) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{
				Message: errorUserAccessMessage,
			},
		}))
		return
	}

	err = core.DeleteAgentExtension(context, request.AccountId, request.AgentName)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}

	c.JSON(200, buildApiResponse(map[string]any{
		"status": "ok",
	}, nil))
}

func handleAgentsApis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	groupV2 := r.Group("/v1/agents")

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
			c.JSON(400, buildApiResponse(nil, []error{errors.New("agents: invalid payload, action.name is required")}))
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
				c.JSON(400, buildApiResponse(nil, []error{errors.New("agents: invalid payload, request field is not a valid object")}))
				return
			}
		}

		switch actionRequest.Action.Name {
		case "ai_list_agents":
			common.MetricsApiRequestsTotal("agents_list")
			agentListAgent(c, context, payload)
			return
		case "ai_create_agent":
			common.MetricsApiRequestsTotal("agents_create")
			agentCreateAgent(c, context, payload)
			return
		case "ai_update_agent":
			common.MetricsApiRequestsTotal("agents_update")
			agentUpdateAgent(c, context, payload)
			return
		case "ai_delete_agent":
			common.MetricsApiRequestsTotal("agents_delete")
			agentDeleteAgent(c, context, payload)
			return
		case "ai_create_agent_extension":
			common.MetricsApiRequestsTotal("agents_extension_create")
			createAgentExtension(c, context, payload)
			return
		case "ai_update_agent_extension":
			common.MetricsApiRequestsTotal("agents_extension_update")
			updateAgentExtension(c, context, payload)
			return
		case "ai_delete_agent_extension":
			common.MetricsApiRequestsTotal("agents_extension_delete")
			deleteAgentExtension(c, context, payload)
			return
		default:
			c.JSON(400, buildApiResponse(nil, []error{errors.New("agents: invalid payload, unsupported action")}))
			return
		}
	})

}
