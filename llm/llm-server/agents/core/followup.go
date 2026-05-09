package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/samber/lo"
	"github.com/tmc/langchaingo/llms"

	toolcore "nudgebee/llm/tools/core"
)

// [Added for TicketV2] scratchpadTagRegex matches XML tags used in the agent scratchpad format
// (e.g. <observation>, </thought_action>, <final_answer>) to prevent injection.
// TicketV2 followups collect free-form user input (ticket descriptions, etc.) which could
// contain XML-like tags that break the scratchpad parsing when the agent resumes.
var scratchpadTagRegex = regexp.MustCompile(`</?(?:observation|thought_action|thought|action|tool_name|tool_input|final_answer)\b[^>]*>`)

type FollowupType string

const (
	FollowupTypeText             FollowupType = "text"
	FollowupTypeSingleSelect     FollowupType = "single_select"
	FollowupTypeMultiSelect      FollowupType = "multi_select"
	FollowupTypeToolConfig       FollowupType = "tool_config"
	FollowupTypeToolConfirmation FollowupType = "tool_confirmation"
	FollowupTypeUserInput        FollowupType = "user_input"
	FollowupTypeAccountSelect    FollowupType = "account_select"
)

type FollowupRequest struct {
	Question        string         `json:"question"`
	FollowupType    FollowupType   `json:"followupType"`
	FollowupOptions []string       `json:"followupOptions"`
	FollowupData    map[string]any `json:"followupData,omitempty"`
	AgentName       string         `json:"agentName"`
	AgentId         uuid.UUID      `json:"agentId"`
	ToolName        string         `json:"toolName"`
	ToolId          string         `json:"toolId"`
}

const PROMPT_IDENTIFY_MISSING_INFORMATION = `
Context:
--------------------------------
	You are an assistant specialized in Kubernetes, helping users troubleshoot, analyze, and fetch data related to Kubernetes resources. 
	Users may ask questions related to fetching logs, events, recommendations, metrics, or performing other investigative tasks on Kubernetes clusters. 
	Their initial questions might sometimes lack necessary details, such as the specific resource name or namespace. 

Task:
--------------------------------
Given a user question related to Kubernetes:
	- Identify missing information like Namespace, Workload etc
	- Use json format as output, DO not include anything extra, stick to the format provided
	
Response Format (JSON):
--------------------------------
	{
		"followup_questions": Array of Questions Required To Answer,
		"reason": Reason to ask above question
	}

Examples:
--------------------------------
	History: Can you fetch the logs?
    Question: xyz server,
	Response: {
		"followup_questions":": ["provide namespace name"],
		"reason": "user wants to get the logs from server but namespace is missing"
    }


    Question: can you fetch me logs of pods xyz in namespace abc?,
	Response: {
		"followup_questions": [],
		"reason": "question has all the information like pod, namespace to pull logs"
    }


History:
--------------------------------
 %v

Current User Question:
--------------------------------
%v

`

func FollowupRequestForMissingInformation(ctx *security.RequestContext, query NBAgentRequest, agent NBAgent) (FollowupRequest, error) {
	refineUpPrompt := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf(PROMPT_IDENTIFY_MISSING_INFORMATION, "", query.Query)),
	}
	res, err := GenerateAndTrackLLMContent(ctx, query.UserId, query.AccountId, query.ConversationId, query.MessageId, "refine", true, refineUpPrompt, true, llms.WithTemperature(0.0), llms.WithJSONMode())
	if err != nil {
		ctx.GetLogger().Error("followup: unable to generate content", "error", err)
		return FollowupRequest{}, nil
	}
	if len(res.Choices) == 0 {
		return FollowupRequest{}, nil
	}

	// Regular expression to extract the JSON object
	if res.Choices[0].Content == "" {
		return FollowupRequest{}, nil
	}

	followupQuestions := map[string]any{}
	err = common.UnmarshalJson([]byte(strings.Trim(res.Choices[0].Content, "`")), &followupQuestions)
	if err != nil {
		ctx.GetLogger().Error("followup: unable to unmarshal refine response", "error", err)
		return FollowupRequest{}, err
	}

	if followupQuestions["followup_questions"] == nil {
		return FollowupRequest{}, nil
	}

	followupQuestionsArray, ok := followupQuestions["followup_questions"].([]any)
	if !ok {
		return FollowupRequest{}, nil
	}

	for _, fq := range followupQuestionsArray {
		return FollowupRequest{
			Question:     fq.(string),
			FollowupType: FollowupTypeText,
			AgentName:    agent.GetName(),
			AgentId: func() uuid.UUID {
				if query.AgentId != "" {
					return uuid.MustParse(query.AgentId)
				}
				return uuid.Nil
			}(),
		}, nil
	}

	return FollowupRequest{}, nil
}

func FollowupRequestForToolOperationConfirmation(ctx *security.RequestContext, query NBAgentRequest, agent NBAgent, action NBAgentPlannerToolAction, toolRequestType toolcore.ToolRequestType) (FollowupRequest, error) {
	input := action.ToolInput
	if strings.Contains(input, `"query"`) {
		commandMap := map[string]any{}
		err := common.UnmarshalJson([]byte(input), &commandMap)
		if err == nil {
			input = commandMap["query"].(string)
		}
	}
	followUpRequest := FollowupRequest{
		Question:     fmt.Sprintf("Tool(%s) is trying to %s cluster resources. Do you want to continue?\nCommand - %s", action.Tool, toolRequestType, input),
		FollowupType: FollowupTypeToolConfirmation,
		FollowupOptions: []string{
			"yes",
			"no",
		},
		AgentName: agent.GetName(),
		AgentId:   uuid.MustParse(query.AgentId),
		ToolName:  action.Tool,
		ToolId:    action.ToolID,
	}
	return followUpRequest, nil
}

func FollowupRequestForMultipleToolConfigs(ctx *security.RequestContext, query NBAgentRequest, agent NBAgent, action NBAgentPlannerToolAction) (FollowupRequest, error) {
	// tool config refinement
	// look for all the tools in the flow 1 level, quick fix for planner agents, will rrequire some rethinking for better fix
	toolsInCompleteFlow := map[string]toolcore.NBTool{}
	for _, tool := range agent.GetSupportedTools(ctx) {
		if _, ok := tool.(toolcore.NBToolConfig); ok {
			toolsInCompleteFlow[tool.Name()] = tool
		}
		if tool.GetType() == toolcore.NBToolTypeAgent {
			if agent, ok := GetNBAgent(ctx, tool.Name(), query.AccountId, AgentStatusEnabled); ok {
				for _, t := range agent.GetSupportedTools(ctx) {
					if _, ok := t.(toolcore.NBToolConfig); ok {
						toolsInCompleteFlow[t.Name()] = t
					}
				}
			}
		}
	}

	existingToolConfigs := map[string]string{}
	if query.QueryConfig.ToolConfigs != nil {
		for k, v := range query.QueryConfig.ToolConfigs {
			existingToolConfigs[k] = v
		}
	}

	for _, tool := range toolsInCompleteFlow {
		if tool.Name() != action.Tool {
			continue
		}
		// there is config already available
		if existingToolConfigs[tool.Name()] != "" {
			continue
		}
		configs, err := toolcore.ListToolConfigs(ctx, query.AccountId, tool)
		if err != nil {
			ctx.GetLogger().Error("followup: unable to list tool configs", "error", err, "tool", tool.Name())
			return FollowupRequest{}, err
		}

		if len(configs) > 1 {
			return FollowupRequest{
				Question:        fmt.Sprintf("I have found multiple configurations for the tool %s, please select the one you are looking for:", tool.Name()),
				FollowupType:    FollowupTypeToolConfig,
				FollowupOptions: lo.Map(configs, func(c toolcore.ToolConfig, i int) string { return c.Name }),
				AgentName:       agent.GetName(),
				AgentId:         uuid.MustParse(query.AgentId),
				ToolName:        tool.Name(),
				ToolId:          action.ToolID,
			}, nil
		}
	}
	return FollowupRequest{}, nil
}

func GenerateFollowup(ctx *security.RequestContext, query NBAgentRequest, followupRequest FollowupRequest) (uuid.UUID, error) {
	// store followup question in context

	if followupRequest.AgentId == uuid.Nil {
		return uuid.Nil, errors.New("followup: agentid is required")
	}
	if followupRequest.AgentName == "" {
		return uuid.Nil, errors.New("followup: agentName is required")
	}
	if followupRequest.Question == "" {
		return uuid.Nil, errors.New("followup: question is required")
	}
	if followupRequest.FollowupType == "" {
		return uuid.Nil, errors.New("followup: followupType is required")
	}

	dao := GetConversationDao()

	// Check if the agent already has an active followup message
	agents, err := dao.ListConversationAgents("", followupRequest.AgentId.String())
	if err == nil && len(agents) > 0 {
		existingAgent := agents[0]
		if existingAgent.FollowupMessageID != uuid.Nil {
			fmsg, fErr := dao.GetConversationMessage(existingAgent.FollowupMessageID.String(), query.AccountId, query.ConversationId)
			if fErr == nil && !IsTerminalConversationStatus(fmsg.Status) {
				ctx.GetLogger().Info("followup: agent already has an active followup message, updating config",
					"agentId", followupRequest.AgentId.String(),
					"followupMessageId", existingAgent.FollowupMessageID)
				newConfig := map[string]any{
					"question":        followupRequest.Question,
					"followupType":    followupRequest.FollowupType,
					"followupOptions": followupRequest.FollowupOptions,
					"toolName":        followupRequest.ToolName,
					"toolId":          followupRequest.ToolId,
				}
				if followupRequest.FollowupData != nil {
					newConfig["followupData"] = followupRequest.FollowupData
				}
				if updateErr := dao.UpdateConversationMessageFollowupConfig(existingAgent.FollowupMessageID.String(), newConfig); updateErr != nil {
					ctx.GetLogger().Error("followup: failed to update followup config", "error", updateErr)
				}
				return existingAgent.FollowupMessageID, nil
			}
		}
	}

	followupRequestConfig := map[string]any{
		"question":        followupRequest.Question,
		"followupType":    followupRequest.FollowupType,
		"followupOptions": followupRequest.FollowupOptions,
		"toolName":        followupRequest.ToolName,
		"toolId":          followupRequest.ToolId,
	}
	if followupRequest.FollowupData != nil {
		followupRequestConfig["followupData"] = followupRequest.FollowupData
	}

	followUpContextJson, err := common.MarshalJson(query)
	if err != nil {
		return uuid.Nil, err
	}

	followupMessage, err := dao.SaveConversationMessage(uuid.NewString(), query.ConversationId, query.AccountId, query.UserId, MessageRoleAI, MessageTypeFollowup, followupRequest.Question, "", followupRequest.AgentName, followupRequest.AgentId, followupRequestConfig, string(followUpContextJson), "", "")
	if err != nil {
		return uuid.Nil, err
	}
	err = dao.UpdateConversationMessage(query.MessageId, "", ConversationStatusWaiting)
	if err != nil {
		return uuid.Nil, err
	}

	// Ensure message ID fits within database column constraints
	err = dao.UpdateConversationAgentWithFollowup(followupRequest.AgentId.String(), followupMessage.String())
	if err != nil {
		return uuid.Nil, err
	}

	// Set followup on ancestor agents — but only if they don't already
	// have an active (non-completed) followup from another concurrent sub-agent.
	// Prevents overwriting when multiple parallel sub-agents each need independent
	// followups (#28141). Without this, redis's followup would clobber postgres's
	// on the common parent, and the parent's resume would only see one of them.
	//
	// Returns true if the agent's slot was set (now points at this followup), or
	// was already pointing at an active followup we shouldn't overwrite. Either
	// way the caller can skip generating a separate followup for that agent.
	setFollowupIfIdle := func(agentId string) bool {
		agents, lookupErr := dao.ListConversationAgents("", agentId)
		if lookupErr != nil || len(agents) == 0 {
			return false
		}
		existing := agents[0]
		if existing.FollowupMessageID != uuid.Nil {
			fmsg, fErr := dao.GetConversationMessage(existing.FollowupMessageID.String(), query.AccountId, query.ConversationId)
			if fErr == nil && !IsTerminalConversationStatus(fmsg.Status) {
				// Parent already has an active followup — don't overwrite.
				// A Failed/Killed/Terminated followup is considered inactive so
				// that a fresh followup from another sub-agent can reclaim the
				// parent's followup_message_id slot.
				return true
			}
		}
		if err := dao.UpdateConversationAgentWithFollowup(agentId, followupMessage.String()); err != nil {
			return false
		}
		return true
	}

	if followupRequest.AgentId.String() != query.AgentId && query.AgentId != "" && query.AgentId != uuid.Nil.String() {
		setFollowupIfIdle(query.AgentId)
	}

	// Walk the full ancestor chain so every agent above the one that raised the
	// followup points at the same followup_message_id. Without this, the parent's
	// executor runs, sees its own agent record has no followup, and calls
	// GenerateFollowup again — producing a duplicate followup card in the UI.
	// Bounded by maxAncestors to avoid infinite loops on malformed parent links.
	const maxAncestors = 16
	ancestorId := query.ParentAgentId
	visited := map[string]bool{
		followupRequest.AgentId.String(): true,
		query.AgentId:                    true,
	}
	for i := 0; i < maxAncestors; i++ {
		if ancestorId == "" || ancestorId == uuid.Nil.String() || visited[ancestorId] {
			break
		}
		visited[ancestorId] = true
		setFollowupIfIdle(ancestorId)
		parentId, _ := dao.GetConversationAgentParentAgentIdAndPreviousState(ancestorId)
		if parentId == ancestorId {
			break
		}
		ancestorId = parentId
	}

	return followupMessage, err
}

func HandleFollowupResponse(ctx *security.RequestContext, query NBAgentRequest) (ConversationMessage, error) {

	if query.AgentId == "" {
		return ConversationMessage{}, nil
	}

	dao := GetConversationDao()
	agents, err := dao.ListConversationAgents("", query.AgentId)
	if err != nil || len(agents) == 0 {
		return ConversationMessage{}, errors.New("followup: agentid is not found required")
	}
	agent := agents[0]

	if !strings.EqualFold(string(agent.Status), string(AgentExecutionStatusWaiting)) {
		return ConversationMessage{}, nil
	}

	// store follwup question in context
	followupMessage, err := dao.GetConversationMessage(agent.FollowupMessageID.String(), query.AccountId, query.ConversationId)
	if err != nil {
		return ConversationMessage{}, err
	}
	if followupMessage.MessageConfig != nil {
		var followupReq FollowupRequest
		err := common.UnmarshalJson([]byte(*followupMessage.MessageConfig), &followupReq)
		if err == nil {
			if followupReq.ToolId != "" && followupReq.ToolName != "" && (followupReq.FollowupType == FollowupTypeUserInput || followupReq.FollowupType == FollowupTypeText || followupReq.FollowupType == FollowupTypeSingleSelect || followupReq.FollowupType == FollowupTypeMultiSelect) {
				// [Changed for TicketV2] Three fixes to followup response handling:
				// 1. Use original agent's message ID — the tool call was saved under the original request's
				//    message ID, not the followup response message ID. Previously caused DB lookup misses.
				// 2. Sanitize user input — TicketV2 collects free-form text (descriptions) that could contain
				//    XML-like tags breaking scratchpad parsing on agent resume.
				// 3. Format response with context — includes the original question so the LLM knows which
				//    field was answered and doesn't re-ask. Prevents duplicate followup loops.
				// Backward-compatible: FollowupTypeUserInput (old type) is still handled in the condition above.
				originalMessageId := agent.MessageID.String()

				var toolResponse string
				if followupReq.FollowupType == FollowupTypeMultiSelect {
					// For multi_select, query.Query is a JSON array string: '["opt1","opt2"]'
					var selectedOptions []string
					if err := json.Unmarshal([]byte(query.Query), &selectedOptions); err == nil && len(selectedOptions) > 0 {
						quoted := make([]string, len(selectedOptions))
						for i, opt := range selectedOptions {
							quoted[i] = fmt.Sprintf("%q", scratchpadTagRegex.ReplaceAllString(opt, ""))
						}
						toolResponse = fmt.Sprintf("User responded to \"%s\" by selecting: %s\nProceed with the user's selections. Do NOT ask this question again.", followupReq.Question, strings.Join(quoted, ", "))
					} else {
						// Fallback: treat as plain text if JSON parse fails
						sanitizedResponse := scratchpadTagRegex.ReplaceAllString(query.Query, "")
						toolResponse = fmt.Sprintf("User responded to \"%s\" with: %s\nProceed with the user's answer. Do NOT ask this question again.", followupReq.Question, sanitizedResponse)
					}
				} else {
					sanitizedResponse := scratchpadTagRegex.ReplaceAllString(query.Query, "")
					toolResponse = fmt.Sprintf("User responded to \"%s\" with: %s\nProceed with the user's answer. Do NOT ask this question again.", followupReq.Question, sanitizedResponse)
				}

				err = dao.UpdateConversationToolResponse(followupReq.ToolId, originalMessageId, query.ConversationId, query.AccountId, toolResponse, toolcore.NBToolResponseStatusSuccess)
				if err != nil {
					ctx.GetLogger().Warn("followup: unable to update tool response", "error", err, "toolId", followupReq.ToolId, "messageId", originalMessageId)
				}
			}
		} else {
			ctx.GetLogger().Error("followup: unable to unmarshal followup request", "error", err, "messageConfig", followupMessage.MessageConfig)
		}
	}
	// if followup message is of type toolConfig then, update messageContext

	if followupMessage.Status != ConversationStatusCompleted {
		// For multi_select, store the selected options as readable text in the DB
		responseText := query.Query
		var selectedOptions []string
		if json.Unmarshal([]byte(query.Query), &selectedOptions) == nil && len(selectedOptions) > 0 {
			responseText = strings.Join(selectedOptions, ", ")
		}

		err = dao.UpdateConversationMessage(agent.FollowupMessageID.String(), responseText, ConversationStatusCompleted)
		if err != nil {
			return ConversationMessage{}, err
		}
		err = dao.UpdateConversationMessage(query.MessageId, "", ConversationStatusInProgress)
		if err != nil {
			return ConversationMessage{}, err
		}
		err = dao.UpdateConversationAgentResponse(agent.ID.String(), "", AgentExecutionStatusInProgress, "", "", "", "")
		if err != nil {
			return ConversationMessage{}, err
		}

		followupMessage.Response = responseText

		return followupMessage, nil
	} else {
		err = dao.UpdateConversationAgentResponse(agent.ID.String(), "", AgentExecutionStatusInProgress, "", "", "", "")
		if err != nil {
			return ConversationMessage{}, err
		}
		return ConversationMessage{}, nil
	}
}
