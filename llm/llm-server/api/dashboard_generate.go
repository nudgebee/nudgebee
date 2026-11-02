package api

import (
	"errors"
	"log/slog"
	"net/http"

	"nudgebee/llm/agents"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/budget"
	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// dashboardGenerateHandler backs the Dashboards page's "Generate with AI" option
// (app action ai_generate_dashboard). It is a single-turn generator in the shape of
// the metrics-query handler: one request, one answer, no follow-up questions. The
// answer is a dashboard draft that the page opens in its editor unsaved — this
// endpoint never writes a dashboard.
func dashboardGenerateHandler(tracer trace.Tracer, meter metric.Meter) gin.HandlerFunc {
	return func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_dashboard_generate")
		requestMap := make(map[string]any)
		if err := c.ShouldBindJSON(&requestMap); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{common.Error{Message: "api: " + err.Error()}}))
			return
		}
		var request ConversationApiRequest
		var actionRequest ActionRequest
		if err := common.DecodeMapToStruct(requestMap, &actionRequest); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{common.Error{Message: "api: " + err.Error()}}))
			return
		}
		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		if err := common.DecodeMapToStruct(actionRequestPayload, &request); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{common.Error{Message: "api: " + err.Error()}}))
			return
		}

		if request.Query == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{common.Error{Message: "api: query is required"}}))
			return
		}
		if request.AccountId == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{common.Error{Message: "api: account_id is required"}}))
			return
		}

		// Every request is a fresh build; a conversation id from the caller would
		// let it append to someone else's conversation, so it is never taken.
		request.ConversationId = uuid.New().String()
		request.SessionId = request.ConversationId

		logger := slog.With("account_id", request.AccountId, "conversation_id", request.ConversationId, "user_id", request.UserId)
		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}

		// The conversation belongs to whoever is signed in. A caller-supplied
		// user_id would file it in another user's Nubi history.
		request.UserId = agentContext.GetSecurityContext().GetUserId()

		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) &&
			!grantedRun(agentContext.GetSecurityContext(), request.AccountId, moduleAiGeneration) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		logger.Info("api: processing request", "request", slog.AnyValue(request))

		// Resolved before the budget check so an account whose metrics backend
		// cannot be generated for costs the tenant nothing.
		builder, err := agents.NewDashboardBuilderAgent(agentContext, request.AccountId)
		if err != nil {
			logger.Warn("api: unable to build dashboard builder agent", "error", err.Error())
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}

		if budget.CheckBudgetAndRespond(c, agentContext.GetSecurityContext().GetTenantId(), request.AccountId, budget.ModuleUserInvestigation, logger) {
			return
		}

		handleRequestExecution(c, agentContext, request.Async, request.UserId, "chains_dashboard_generate", logger, func(ctx *security.RequestContext) (core.NBAgentResponse, error) {
			return core.HandleConversationSessionRequest(ctx, builder, request.UserId, request.AccountId, request.SessionId, request.Query, core.ConversationSessionRequestWithSource(core.ConversationSourceDashboardBuilder), core.ConversationSessionRequestWithEnableQueryRefinement(false), core.ConversationSessionRequestWithConfig(request.Config))
		}, core.NBAgentResponse{
			Response:       []string{},
			Query:          request.Query,
			AgentName:      builder.GetName(),
			ConversationId: request.ConversationId,
			SessionId:      request.SessionId,
			Status:         core.ConversationStatusInProgress,
		})
	}
}
