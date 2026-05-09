package api

import (
	"errors"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type ragQueryRequest struct {
	AccountId string `json:"account_id" mapstructure:"account_id" validate:"required"`
	Module    string `json:"module" mapstructure:"module" validate:"required"`
	Query     string `json:"query" mapstructure:"query" validate:"required"`
	Limit     int    `json:"limit" mapstructure:"limit"`
}

type createAgentRagRequest struct {
	AccountId string `json:"account_id" mapstructure:"account_id" validate:"required"`
	Agent     string `json:"agent" mapstructure:"agent" validate:"required"`
	Data      string `json:"data" mapstructure:"data" validate:"required"`
	Format    string `json:"format" mapstructure:"format"`
	FileName  string `json:"file_name" mapstructure:"file_name"`
}

func ragQuery(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	if payload["account_id"] == nil || payload["account_id"] == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("agents: invalid payload, account_id is required")}))
		return
	}

	// Check if user has access to account
	if !context.GetSecurityContext().HasAccountAccess(payload["account_id"].(string), security.SecurityAccessTypeRead) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{
				Message: errorUserAccessMessage,
			},
		}))
		return
	}

	queryRequest := ragQueryRequest{}
	err := common.DecodeMapToStruct(payload, &queryRequest)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}

	err = common.ValidateStruct(queryRequest)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}

	if queryRequest.Limit == 0 {
		queryRequest.Limit = 3
	}

	resp := toolcore.QueryRAG(context.GetSecurityContext().GetUserId(), queryRequest.AccountId, queryRequest.Query, queryRequest.Module, queryRequest.Limit, uuid.NewString(), uuid.NewString(), uuid.NewString(), false)

	// Check if response is empty
	if len(resp) == 0 {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("rag: unable to query rag")}))
		return
	}

	// Extract documents from search results
	documents := []string{}
	for _, result := range resp {
		documents = append(documents, result.Document)
	}

	c.JSON(200, buildApiResponse(map[string]any{
		"data":  documents,
		"limit": queryRequest.Limit,
	}, nil))
}

func createAgentRagData(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	if payload["account_id"] == nil || payload["account_id"] == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("agents: invalid payload, account_id is required")}))
		return
	}

	// Check if user has create access to account
	if !context.GetSecurityContext().HasAccountAccess(payload["account_id"].(string), security.SecurityAccessTypeCreate) {
		c.JSON(403, buildApiResponse(nil, []error{
			common.Error{
				Message: errorUserAccessMessage,
			},
		}))
		return
	}

	createRequest := createAgentRagRequest{
		Format: "text", // Default format
	}

	err := common.DecodeMapToStruct(payload, &createRequest)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}

	// Validate required fields
	err = common.ValidateStruct(createRequest)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}

	// Call the core function
	agentRag, err := toolcore.CreateAgentRag(context, createRequest.AccountId, createRequest.Agent,
		createRequest.Data, createRequest.Format, createRequest.FileName)

	if err != nil {
		slog.Error("rag: failed to create agent rag", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}

	c.JSON(200, buildApiResponse(agentRag, nil))
}

func handleRagApis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	groupV2 := r.Group("/v1/rags")

	groupV2.POST("", func(c *gin.Context) {
		var hasuraRequest HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		if hasuraRequest.Action.Name == "" {
			c.JSON(400, buildApiResponse(nil, []error{errors.New("tools: invalid payload, action.name is required")}))
			return
		}

		context, err := buildContextFromHasuraPayload(c.Request.Context(), c, &hasuraRequest, tracer, meter, slog.With())
		if err != nil {
			c.JSON(401, buildApiResponse(nil, []error{err}))
			return
		}

		payload := hasuraRequest.Input
		if payload["request"] != nil {
			payload = payload["request"].(map[string]any)
		}

		switch hasuraRequest.Action.Name {
		case "ai_query_rag":
			common.MetricsApiRequestsTotal("rag_query")
			ragQuery(c, context, payload)
		case "ai_create_rag":
			common.MetricsApiRequestsTotal("rag_create")
			createAgentRagData(c, context, payload)
		default:
			c.JSON(400, buildApiResponse(nil, []error{errors.New("rags: invalid payload, unsupported action")}))
		}
	})

}
