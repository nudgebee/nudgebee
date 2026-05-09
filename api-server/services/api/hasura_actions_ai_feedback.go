package api

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/feedback"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleHasuraAiFeedbackAction(hasuraPayload *HasuraActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	hasuraRequest := hasuraPayload.Input
	switch hasuraPayload.Action.Name {
	case "ai_feedback_create", "ai_create_feedback":
		var aiFeedbackRequest feedback.ConversationFeedbackRequest
		if hasuraRequest["request"] != nil {
			hasuraRequest = hasuraRequest["request"].(map[string]interface{})
		}
		err := common.UnmarshalMapToStruct(hasuraRequest, &aiFeedbackRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := feedback.CreateConversationAiFeedback(ctx, aiFeedbackRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, map[string]any{"data": resp})
		return
	case "ai_save_conversation":
		var saveConversationRequest feedback.SaveOrDeleteConversationRequest
		if hasuraRequest["request"] != nil {
			hasuraRequest = hasuraRequest["request"].(map[string]interface{})
		}
		err := common.UnmarshalMapToStruct(hasuraRequest, &saveConversationRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := feedback.SaveConversation(ctx, saveConversationRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, map[string]any{"data": resp})
		return
	case "ai_delete_saved_conversation":
		var deleteConversationRequest feedback.SaveOrDeleteConversationRequest
		if hasuraRequest["request"] != nil {
			hasuraRequest = hasuraRequest["request"].(map[string]interface{})
		}
		err := common.UnmarshalMapToStruct(hasuraRequest, &deleteConversationRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := feedback.DeleteSavedConversation(ctx, deleteConversationRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, map[string]any{"data": resp})
		return
	case "ai_delete_llm_conversation_by_id":
		var deleteConversationRequest feedback.DeleteConversationRequest
		if hasuraRequest["request"] != nil {
			hasuraRequest = hasuraRequest["request"].(map[string]interface{})
		}
		err := common.UnmarshalMapToStruct(hasuraRequest, &deleteConversationRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := feedback.DeleteConversationByConversationId(ctx, deleteConversationRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		// print resp
		logger.Info("delete conversation response", "resp", resp)
		c.JSON(200, map[string]any{"data": resp})
		return
	}
}
