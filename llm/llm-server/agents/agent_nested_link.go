package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

var loadActiveGlobalContextFn = toolcore.LoadActiveGlobalContext

// delegatedAccountContext is the Account Context a delegator (metrics, traces,
// metrics_query) hands the provider agent it runs for accountId. A call that
// targets another account gets that account's context, never the caller's.
func delegatedAccountContext(ctx *security.RequestContext, query core.NBAgentRequest, accountId string) string {
	if accountId == "" || accountId == query.AccountId {
		return query.AccountContext
	}
	return loadActiveGlobalContextFn(ctx, accountId)
}

func nestedAgentParentID(query core.NBAgentRequest) string {
	if query.AgentId != "" {
		return query.AgentId
	}
	return query.ParentAgentId
}

func nestedAgentAdditionalDetails(response core.NBAgentResponse) map[string]any {
	return map[string]any{
		core.NBToolCallAdditionalDetailsAgentId:   response.AgentId,
		core.NBToolCallAdditionalDetailsMessageId: response.MessageId,
	}
}

func addNestedAgentWaitingDetails(details map[string]any, response core.NBAgentResponse) {
	details[core.NBToolCallAdditionalDetailsQuery] = response.Query
	details[core.NBToolCallAdditionalDetailsFollowupRequest] = response.FollowupRequest
}

func handleNestedAgentCallPreamble(
	nbRequestContext toolcore.NbToolContext,
	response core.NBAgentResponse,
	err error,
	agentName string,
) (toolcore.NBToolResponse, error, bool) {
	additionalDetails := nestedAgentAdditionalDetails(response)
	if err != nil {
		return toolcore.NBToolResponse{
			AdditionalDetails: additionalDetails,
			References:        response.References,
		}, err, true
	}
	if response.Status != core.ConversationStatusWaiting {
		return toolcore.NBToolResponse{AdditionalDetails: additionalDetails}, nil, false
	}

	addNestedAgentWaitingDetails(additionalDetails, response)
	responseData := "Waiting for user input."
	if len(response.Response) > 0 {
		responseData = response.Response[0]
	}
	return toolcore.NBToolResponse{
		Data:              responseData,
		Type:              toolcore.NBToolResponseTypeText,
		Status:            toolcore.NBToolResponseStatusWaiting,
		IsTerminal:        response.IsTerminal,
		AdditionalDetails: additionalDetails,
		References:        response.References,
		SubAgentEvidence:  core.BuildSubAgentEvidenceForTool(nbRequestContext.Ctx, agentName, response.AgentStepResponse),
	}, nil, true
}
