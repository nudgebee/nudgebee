package api

import (
	"database/sql"
	"errors"
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Request DTOs
type memoryCreateRequest struct {
	AccountId string `json:"account_id"`
	Memory    struct {
		ConversationId string `json:"conversation_id"`
		MessageId      string `json:"message_id"`
		Content        string `json:"content"`
		MemoryType     string `json:"memory_type"`
	} `json:"memory"`
}

type memoryListRequest struct {
	AccountId      string `json:"account_id"`
	Limit          int    `json:"limit"`
	Offset         int    `json:"offset"`
	ConversationId string `json:"conversation_id"`
	MessageId      string `json:"message_id"`
	MemoryType     string `json:"memory_type"`
	Query          string `json:"query"`
}

type memoryDeleteRequest struct {
	AccountId string `json:"account_id"`
	Id        string `json:"id"`
}

const errorMemoryUserAccessMessage = "memory: user doesn't have access to this account"

// Handlers

func memoryCreate(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request memoryCreateRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error("memory: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("memory: account_id is required")}))
		return
	}

	if request.Memory.Content == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("memory: content is required")}))
		return
	}

	// Validate and default MemoryType
	mType := core.MemoryType(request.Memory.MemoryType).Validate()
	if mType == "" {
		mType = core.MemoryTypeUserPreference
	}

	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeCreate) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{Message: errorMemoryUserAccessMessage},
		}))
		return
	}

	id, err := core.GetConversationDao().SaveLongTermMemory(request.AccountId, request.Memory.ConversationId, request.Memory.MessageId, request.Memory.Content, mType)
	if err != nil {
		slog.Error("memory: failed to create", "error", err, "account_id", request.AccountId)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	c.JSON(200, buildApiResponse(map[string]string{"id": id, "status": "ok"}, nil))
}

func memoryList(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request memoryListRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error("memory: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("memory: account_id is required")}))
		return
	}

	if request.Limit == 0 {
		request.Limit = 20
	}

	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{Message: errorMemoryUserAccessMessage},
		}))
		return
	}

	memories, err := core.GetConversationDao().ListLongTermMemories(request.AccountId, request.ConversationId, request.MessageId, request.MemoryType, request.Query, request.Limit, request.Offset)
	if err != nil {
		slog.Error("memory: failed to list", "error", err, "account_id", request.AccountId)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	c.JSON(200, buildApiResponse(memories, nil))
}

func memoryDelete(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request memoryDeleteRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error("memory: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("memory: account_id is required")}))
		return
	}

	if request.Id == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("memory: id is required")}))
		return
	}

	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeDelete) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{Message: errorMemoryUserAccessMessage},
		}))
		return
	}

	err = core.GetConversationDao().DeleteLongTermMemory(request.Id, request.AccountId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(404, buildApiResponse(nil, []error{
				common.Error{Message: "memory: memory not found"},
			}))
			return
		}
		slog.Error("memory: failed to delete", "error", err, "id", request.Id)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	c.JSON(200, buildApiResponse(map[string]string{"status": "ok"}, nil))
}

func handleMemoryApis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	groupV2 := r.Group("/v1/memory")

	groupV2.POST("", func(c *gin.Context) {
		var hasuraRequest HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraRequest)
		if err != nil {
			slog.Error("memory: error binding hasura request", "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{Message: "memory: " + err.Error()},
			}))
			return
		}

		if hasuraRequest.Action.Name == "" {
			c.JSON(400, buildApiResponse(nil, []error{errors.New("memory: invalid payload, action.name is required")}))
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
		case "ai_create_memory":
			common.MetricsApiRequestsTotal("memory_create")
			memoryCreate(c, context, payload)
		case "ai_list_memory":
			common.MetricsApiRequestsTotal("memory_list")
			memoryList(c, context, payload)
		case "ai_delete_memory":
			common.MetricsApiRequestsTotal("memory_delete")
			memoryDelete(c, context, payload)
		default:
			c.JSON(400, buildApiResponse(nil, []error{errors.New("memory: invalid payload, unsupported action")}))
		}
	})
}
