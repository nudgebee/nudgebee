package tools

import (
	"errors"
	"fmt"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

func init() {
	core.RegisterNBToolFactory(ToolRecommendationRecordTicketResolution, func(accountId string) (core.NBTool, error) {
		return RecommendationTicketResolutionTool{}, nil
	})
}

const ToolRecommendationRecordTicketResolution = "recommendation_record_ticket_resolution"

// RecommendationTicketResolutionTool links an already-created ticket to a
// recommendation as its resolution attempt, via api-server's
// recommendations_create_ticket_resolution RPC under the requesting user's
// role. The handler claims an Open recommendation into InProgress and writes
// the Ticket resolution row; settlement then belongs to the ticket-status
// sync when the ticket closes. Idempotent for the same ticket id. Ticket
// creation itself goes through the ticket tool — this records the linkage.
type RecommendationTicketResolutionTool struct{}

func (m RecommendationTicketResolutionTool) Name() string {
	return ToolRecommendationRecordTicketResolution
}
func (m RecommendationTicketResolutionTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (m RecommendationTicketResolutionTool) Description() string {
	return "Records a ticket as a recommendation's resolution attempt after the user confirms: the recommendation moves Open → InProgress and settles automatically when the ticket later closes (resolved → Success, rejected/cancelled → back to Open). Use AFTER a ticket was created for the recommendation (via the ticket tool). Idempotent for the same ticket. Inputs: recommendation_id (required), ticket_id (required — the ticket's id/key from creation), ticket_key (optional display key)."
}

func (m RecommendationTicketResolutionTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"recommendation_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "UUID of the recommendation the ticket tracks (recommendation.id).",
			},
			"ticket_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "The created ticket's id (e.g. 'OPS-123') — becomes the resolution's reference.",
			},
			"ticket_key": {
				Type:        core.ToolSchemaTypeString,
				Description: "Optional human-readable ticket key for the status message.",
			},
		},
		Required: []string{"recommendation_id", "ticket_id"},
	}
}

// InferToolRequestType is static on purpose (see RecommendationApplyTool).
func (m RecommendationTicketResolutionTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeCreate, nil
}

// ConfirmationKey makes the write confirmation per-action.
func (m RecommendationTicketResolutionTool) ConfirmationKey(toolInput string) string {
	return perActionConfirmationKey(ToolRecommendationRecordTicketResolution, toolInput)
}

// ConfirmationQuestion states the linkage and its status effect plainly.
func (m RecommendationTicketResolutionTool) ConfirmationQuestion(toolInput string) string {
	args := confirmationArgs(toolInput)
	ticketId, _ := args["ticket_id"].(string)
	recommendationId, _ := args["recommendation_id"].(string)
	if ticketId == "" || recommendationId == "" {
		return ""
	}
	return fmt.Sprintf("Record ticket %s as the resolution attempt for recommendation %s?\nThe recommendation moves to In Progress and settles automatically when the ticket closes. Do you want to continue?",
		ticketId, recommendationId)
}

func (m RecommendationTicketResolutionTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	recommendationId := stringArg(input, "recommendation_id")
	if recommendationId == "" {
		return errNBLLMToolResponse(errRecommendationIdRequired), nil
	}
	ticketId := stringArg(input, "ticket_id")
	if ticketId == "" {
		return errNBLLMToolResponse(errors.New("ticket_id is required")), nil
	}

	object := map[string]any{
		"account_id":        nbCtx.AccountId,
		"recommendation_id": recommendationId,
		"ticket_id":         ticketId,
		"ticket_key":        stringArg(input, "ticket_key"),
		"resolver_type":     "NBLLM",
	}

	resp, err := doApiServerActionRequest(nbCtx, "/rpc/recommendation", "recommendations_create_ticket_resolution", map[string]any{"object": object}, "recommendation_record_ticket_resolution")
	if err != nil {
		nbCtx.Ctx.GetLogger().Error("recommendation_record_ticket_resolution: failed", "error", err, "recommendation_id", recommendationId, "ticket_id", ticketId)
		return errNBLLMToolResponse(err), nil
	}
	return core.NBToolResponse{
		Data:   resp,
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}
