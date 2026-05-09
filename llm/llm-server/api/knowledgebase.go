package api

import (
	"errors"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Request DTOs for Knowledgebase operations
type kbCreateRequest struct {
	AccountId     string `json:"account_id"`
	Knowledgebase struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Data        string `json:"data"`
		Format      string `json:"format"`
		FileName    string `json:"file_name"`
	} `json:"knowledgebase"`
}

type kbGetRequest struct {
	AccountId string `json:"account_id"`
	Id        string `json:"id"`
}

type kbListRequest struct {
	AccountId string `json:"account_id"`
}

type kbUpdateRequest struct {
	AccountId     string `json:"account_id"`
	Knowledgebase struct {
		Id          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Data        string `json:"data"`
		Format      string `json:"format"`
		FileName    string `json:"file_name"`
	} `json:"knowledgebase"`
}

type kbDeleteRequest struct {
	AccountId string `json:"account_id"`
	Id        string `json:"id"`
}

type kbListAgentKBsRequest struct {
	AccountId string `json:"account_id"`
	AgentId   string `json:"agent_id"`
}

type kbMapToAgentRequest struct {
	AccountId string `json:"account_id"`
	KbId      string `json:"kb_id"`
	AgentId   string `json:"agent_id"`
}

type kbUnmapFromAgentRequest struct {
	AccountId string `json:"account_id"`
	KbId      string `json:"kb_id"`
	AgentId   string `json:"agent_id"`
}

type kbListAgentsWithCountsRequest struct {
	AccountId string `json:"account_id"`
}

type kbListMappingsRequest struct {
	AccountId string `json:"account_id"`
}

type kbListKBAgentsRequest struct {
	AccountId string `json:"account_id"`
	KbId      string `json:"kb_id"`
}

type kbLoadHistoryRequest struct {
	AccountId string `json:"account_id"`
	KbId      string `json:"kb_id"`
}

type kbRetriggerRequest struct {
	AccountId string `json:"account_id"`
	KbId      string `json:"kb_id"`
}

const errorKBUserAccessMessage = "kb: user doesn't have access to this account"

// Handler functions

func kbCreate(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request kbCreateRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error("kb: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: account_id is required")}))
		return
	}

	// Check if user has access to account
	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeCreate) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{Message: errorKBUserAccessMessage},
		}))
		return
	}

	kb := core.Knowledgebase{
		Name:         request.Knowledgebase.Name,
		Description:  request.Knowledgebase.Description,
		Data:         request.Knowledgebase.Data,
		DataFormat:   request.Knowledgebase.Format,
		DataFilename: request.Knowledgebase.FileName,
	}

	resp, err := core.CreateKnowledgebase(context, request.AccountId, kb)
	if err != nil {
		slog.Error("kb: failed to create", "error", err, "account_id", request.AccountId)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	c.JSON(200, buildApiResponse(resp, nil))
}

func kbGet(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request kbGetRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error("kb: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: account_id is required")}))
		return
	}

	if request.Id == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: id is required")}))
		return
	}

	// Check if user has access to account
	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{Message: errorKBUserAccessMessage},
		}))
		return
	}

	resp, err := core.GetKnowledgebase(context, request.AccountId, request.Id)
	if err != nil {
		slog.Error("kb: failed to get", "error", err, "kb_id", request.Id)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	c.JSON(200, buildApiResponse(resp, nil))
}

func kbList(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request kbListRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error("kb: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: account_id is required")}))
		return
	}

	// Check if user has access to account
	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{Message: errorKBUserAccessMessage},
		}))
		return
	}

	resp, err := core.ListKnowledgebases(context, request.AccountId)
	if err != nil {
		slog.Error("kb: failed to list", "error", err, "account_id", request.AccountId)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	c.JSON(200, buildApiResponse(resp, nil))
}

func kbUpdate(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request kbUpdateRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error("kb: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: account_id is required")}))
		return
	}

	if request.Knowledgebase.Id == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: id is required")}))
		return
	}

	// Check if user has access to account
	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeUpdate) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{Message: errorKBUserAccessMessage},
		}))
		return
	}

	updates := core.Knowledgebase{
		Name:         request.Knowledgebase.Name,
		Description:  request.Knowledgebase.Description,
		Data:         request.Knowledgebase.Data,
		DataFormat:   request.Knowledgebase.Format,
		DataFilename: request.Knowledgebase.FileName,
	}

	err = core.UpdateKnowledgebase(context, request.AccountId, request.Knowledgebase.Id, updates)
	if err != nil {
		slog.Error("kb: failed to update", "error", err, "kb_id", request.Knowledgebase.Id)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	c.JSON(200, buildApiResponse(map[string]string{"status": "ok", "id": request.Knowledgebase.Id}, nil))
}

func kbDelete(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request kbDeleteRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error("kb: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: account_id is required")}))
		return
	}

	if request.Id == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: id is required")}))
		return
	}

	// Check if user has access to account
	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeDelete) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{Message: errorKBUserAccessMessage},
		}))
		return
	}

	err = core.DeleteKnowledgebase(context, request.AccountId, request.Id)
	if err != nil {
		slog.Error("kb: failed to delete", "error", err, "kb_id", request.Id)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	c.JSON(200, buildApiResponse(map[string]string{"status": "ok"}, nil))
}

func kbListAgentKBs(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request kbListAgentKBsRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error("kb: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: account_id is required")}))
		return
	}

	if request.AgentId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: agent_id is required")}))
		return
	}

	// Check if user has access to account
	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{Message: errorKBUserAccessMessage},
		}))
		return
	}

	resp, err := core.ListAgentKBs(context, request.AccountId, request.AgentId)
	if err != nil {
		slog.Error("kb: failed to list agent KBs", "error", err, "agent_id", request.AgentId)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	c.JSON(200, buildApiResponse(resp, nil))
}

func kbMapToAgent(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request kbMapToAgentRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error("kb: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: account_id is required")}))
		return
	}

	if request.KbId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: kb_id is required")}))
		return
	}

	if request.AgentId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: agent_id is required")}))
		return
	}

	// Check if user has access to account
	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeCreate) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{Message: errorKBUserAccessMessage},
		}))
		return
	}

	resp, err := core.MapKBToAgent(context, request.AccountId, request.KbId, request.AgentId)
	if err != nil {
		slog.Error("kb: failed to map KB to agent", "error", err, "kb_id", request.KbId, "agent_id", request.AgentId)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	c.JSON(200, buildApiResponse(resp, nil))
}

func kbUnmapFromAgent(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request kbUnmapFromAgentRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error("kb: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: account_id is required")}))
		return
	}

	if request.KbId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: kb_id is required")}))
		return
	}

	if request.AgentId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: agent_id is required")}))
		return
	}

	// Check if user has access to account
	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeCreate) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{Message: errorKBUserAccessMessage},
		}))
		return
	}

	err = core.UnmapKBFromAgent(context, request.AccountId, request.KbId, request.AgentId)
	if err != nil {
		slog.Error("kb: failed to unmap KB from agent", "error", err, "kb_id", request.KbId, "agent_id", request.AgentId)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	c.JSON(200, buildApiResponse(map[string]string{"status": "ok"}, nil))
}

func kbListAgentsWithCounts(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request kbListAgentsWithCountsRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error("kb: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: account_id is required")}))
		return
	}

	// Check if user has access to account
	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{Message: errorKBUserAccessMessage},
		}))
		return
	}

	resp, err := core.ListAgentsWithKBCounts(context, request.AccountId)
	if err != nil {
		slog.Error("kb: failed to list agents with KB counts", "error", err)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	c.JSON(200, buildApiResponse(resp, nil))
}

func kbListMappings(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request kbListMappingsRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error("kb: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: account_id is required")}))
		return
	}

	// Check if user has access to account
	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{Message: errorKBUserAccessMessage},
		}))
		return
	}

	resp, err := core.ListKBAgentMappings(context, request.AccountId)
	if err != nil {
		slog.Error("kb: failed to list KB-agent mappings", "error", err)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	c.JSON(200, buildApiResponse(resp, nil))
}

func kbListKBAgents(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request kbListKBAgentsRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error("kb: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: account_id is required")}))
		return
	}

	if request.KbId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: kb_id is required")}))
		return
	}

	// Check if user has access to account
	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{Message: errorKBUserAccessMessage},
		}))
		return
	}

	resp, err := core.ListKBAgents(context, request.AccountId, request.KbId)
	if err != nil {
		slog.Error("kb: failed to list KB agents", "error", err, "kb_id", request.KbId)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	c.JSON(200, buildApiResponse(resp, nil))
}

// Route handler registration
func kbGetLoadHistory(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request kbLoadHistoryRequest
	if err := common.DecodeMapToStruct(payload, &request); err != nil {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	if request.AccountId == "" || request.KbId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: account_id and kb_id are required")}))
		return
	}
	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
		c.JSON(403, buildApiResponse(nil, []error{common.Error{Message: errorKBUserAccessMessage}}))
		return
	}
	resp, err := core.GetKBLoadHistory(context, request.AccountId, request.KbId)
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	c.JSON(200, buildApiResponse(resp, nil))
}

func kbRetrigger(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request kbRetriggerRequest
	if err := common.DecodeMapToStruct(payload, &request); err != nil {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	if request.AccountId == "" || request.KbId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: account_id and kb_id are required")}))
		return
	}
	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeUpdate) {
		c.JSON(403, buildApiResponse(nil, []error{common.Error{Message: errorKBUserAccessMessage}}))
		return
	}
	err := core.RetriggerKnowledgebase(context, request.AccountId, request.KbId)
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	c.JSON(200, buildApiResponse(map[string]string{"status": "ok", "id": request.KbId}, nil))
}

func handleKnowledgebaseApis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	groupV2 := r.Group("/v1/knowledgebases")

	groupV2.POST("", func(c *gin.Context) {
		var hasuraRequest HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraRequest)
		if err != nil {
			slog.Error("kb: error binding hasura request", "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{Message: "kb: " + err.Error()},
			}))
			return
		}

		if hasuraRequest.Action.Name == "" {
			c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: invalid payload, action.name is required")}))
			return
		}

		context, err := buildContextFromHasuraPayload(c.Request.Context(), c, &hasuraRequest, tracer, meter, slog.With())
		if err != nil {
			c.JSON(401, buildApiResponse(nil, []error{err}))
			return
		}

		payload := hasuraRequest.Input
		if rawRequest, ok := payload["request"]; ok {
			if castedRequest, castOk := rawRequest.(map[string]any); castOk {
				payload = castedRequest
			}
		}

		switch hasuraRequest.Action.Name {
		case "ai_create_kb":
			common.MetricsApiRequestsTotal("kb_create")
			kbCreate(c, context, payload)
		case "ai_get_kb":
			common.MetricsApiRequestsTotal("kb_get")
			kbGet(c, context, payload)
		case "ai_list_kb":
			common.MetricsApiRequestsTotal("kb_list")
			kbList(c, context, payload)
		case "ai_update_kb":
			common.MetricsApiRequestsTotal("kb_update")
			kbUpdate(c, context, payload)
		case "ai_delete_kb":
			common.MetricsApiRequestsTotal("kb_delete")
			kbDelete(c, context, payload)
		case "ai_list_agent_kbs":
			common.MetricsApiRequestsTotal("kb_list_agent_kbs")
			kbListAgentKBs(c, context, payload)
		case "ai_map_kb_to_agent":
			common.MetricsApiRequestsTotal("kb_map_to_agent")
			kbMapToAgent(c, context, payload)
		case "ai_unmap_kb_from_agent":
			common.MetricsApiRequestsTotal("kb_unmap_from_agent")
			kbUnmapFromAgent(c, context, payload)
		case "ai_list_agents_with_kb_counts":
			common.MetricsApiRequestsTotal("kb_list_agents_with_counts")
			kbListAgentsWithCounts(c, context, payload)
		case "ai_list_kb_agent_mappings":
			common.MetricsApiRequestsTotal("kb_list_mappings")
			kbListMappings(c, context, payload)
		case "ai_list_kb_agents":
			common.MetricsApiRequestsTotal("kb_list_kb_agents")
			kbListKBAgents(c, context, payload)
		case "ai_get_kb_load_history":
			common.MetricsApiRequestsTotal("kb_get_load_history")
			kbGetLoadHistory(c, context, payload)
		case "ai_retrigger_kb":
			common.MetricsApiRequestsTotal("kb_retrigger")
			kbRetrigger(c, context, payload)
		default:
			c.JSON(400, buildApiResponse(nil, []error{errors.New("kb: invalid payload, unsupported action")}))
		}
	})
}
