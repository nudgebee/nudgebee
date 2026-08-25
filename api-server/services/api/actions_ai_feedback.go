package api

import (
	"log/slog"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/conversation"
	"nudgebee/services/feedback"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// chatAuditSourceWeb mirrors llm-server's web chat conversation source
// (core.ConversationSourceUserInvestigation) so both CHAT_ACTIONS writers agree
// on the event_attr "source" value. The conversation-delete action is only
// reachable from the web UI.
const chatAuditSourceWeb = "UserInvestigation"

func handleAiFeedbackAction(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	actionRequest := actionPayload.Input
	switch actionPayload.Action.Name {
	case "ai_feedback_create", "ai_create_feedback":
		var aiFeedbackRequest feedback.ConversationFeedbackRequest
		if actionRequest["request"] != nil {
			actionRequest = actionRequest["request"].(map[string]interface{})
		}
		err := common.UnmarshalMapToStruct(actionRequest, &aiFeedbackRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := feedback.CreateConversationAiFeedback(ctx, aiFeedbackRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, map[string]any{"data": resp})
		return
	case "ai_save_conversation", "ai_create_saved_conversation":
		var saveConversationRequest feedback.SaveOrDeleteConversationRequest
		if actionRequest["request"] != nil {
			actionRequest = actionRequest["request"].(map[string]interface{})
		}
		err := common.UnmarshalMapToStruct(actionRequest, &saveConversationRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := feedback.SaveConversation(ctx, saveConversationRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, map[string]any{"data": resp})
		return
	case "ai_delete_saved_conversation":
		var deleteConversationRequest feedback.SaveOrDeleteConversationRequest
		if actionRequest["request"] != nil {
			actionRequest = actionRequest["request"].(map[string]interface{})
		}
		err := common.UnmarshalMapToStruct(actionRequest, &deleteConversationRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := feedback.DeleteSavedConversation(ctx, deleteConversationRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, map[string]any{"data": resp})
		return
	case "ai_delete_llm_conversation_by_id":
		var deleteConversationRequest feedback.DeleteConversationRequest
		if actionRequest["request"] != nil {
			actionRequest = actionRequest["request"].(map[string]interface{})
		}
		err := common.UnmarshalMapToStruct(actionRequest, &deleteConversationRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		// Resolve the owning account before deletion (the row is gone afterwards) so the audit is account-tagged; tolerate lookup failure (best-effort audit).
		deleteAccountId, deleteSessionId, accErr := feedback.GetConversationAccountAndSession(ctx, deleteConversationRequest.ConversationId)
		if accErr != nil {
			logger.Warn("chat audit: failed to resolve account for deleted conversation", "error", accErr, "conversation_id", deleteConversationRequest.ConversationId)
		}
		resp, err := feedback.DeleteConversationByConversationId(ctx, deleteConversationRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		// Record the delete under CHAT_ACTIONS via the standard audit path (same
		// as every other action handler). Best-effort: log on failure, never block
		// the response.
		if auditErr := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{{
			UserId:        ctx.GetSecurityContext().GetUserId(),
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			AccountId:     deleteAccountId,
			EventTime:     time.Now().UTC(),
			EventCategory: audit.EventCategoryChatActions,
			EventType:     audit.EventTypeChatConversationDelete,
			EventAction:   audit.EventActionDelete,
			EventActor:    audit.EventActorUiService,
			EventTarget:   deleteConversationRequest.ConversationId,
			EventStatus:   audit.EventStatusSuccess,
			EventState:    map[string]any{"deleted": true},
			EventAttr: map[string]any{
				"conversation_id": deleteConversationRequest.ConversationId,
				"session_id":      deleteSessionId,
				"source":          chatAuditSourceWeb,
			},
		}}}); auditErr != nil {
			logger.Error("chat audit: failed to create delete audit", "error", auditErr)
		}
		c.JSON(200, map[string]any{"data": resp})
		return
	case "ai_get_conversation_v3":
		var deltaRequest conversation.GetConversationDeltaRequest
		if reqObj, ok := actionRequest["request"].(map[string]interface{}); ok && reqObj != nil {
			actionRequest = reqObj
		}
		err := common.UnmarshalMapToStruct(actionRequest, &deltaRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := conversation.GetConversationDelta(ctx, deltaRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		// AiGetConversationV3Response's SDL has the conversation/messages/agents/
		// tool_calls/cursor fields directly at the top level — no `data` wrapper —
		// so we return the struct verbatim. Wrapping it in `{"data": resp}` would
		// hide the fields from selection-set pruning under the RPC bypass and
		// produce empty `{}` GraphQL responses.
		c.JSON(200, resp)
		return
	}
}
