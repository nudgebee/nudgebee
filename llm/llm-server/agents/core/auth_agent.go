package core

import (
	"fmt"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"strings"

	toolcore "nudgebee/llm/tools/core"

	"github.com/tmc/langchaingo/llms"
)

func IsAgentToolAuthorizedToProcessRequest(ctx *security.RequestContext, agent NBAgent, request NBAgentRequest, action NBAgentPlannerToolAction) (*NBAgentPlannerFinishAction, *toolcore.ToolRequestType, error) {
	toolName := action.Tool
	var tool toolcore.NBTool
	found := false
	for _, tool1 := range agent.GetSupportedTools(ctx) {
		if strings.EqualFold(tool1.Name(), toolName) {
			found = true
			tool = tool1
			break
		}
	}

	if !found {
		// check if it's a builtin tool like load_skills or shell_execute
		if strings.EqualFold(toolName, "load_skills") || (strings.EqualFold(toolName, toolcore.ToolExecuteShellCommand) && config.Config.LlmServerShellToolEnabled) {
			if t, ok := toolcore.GetNBTool(request.AccountId, toolName); ok {
				found = true
				tool = t
			}
		}
	}

	if !found {
		// check if it's a client tool
		for _, ct := range request.ClientTools {
			if strings.EqualFold(ct.Name, toolName) {
				found = true
				tool = toolcore.NewClientToolWrapper(ct)
				break
			}
		}
	}

	if !found {
		return nil, nil, fmt.Errorf("auth: tool not found - %s, agent - %s", toolName, agent.GetName())
	}

	requestType := toolcore.ToolRequestType("")
	if tool.GetType() == toolcore.NBToolTypeTool {
		// Try static heuristic first, then fall back to LLM-based classification
		if toolValidator, ok := tool.(toolcore.ToolRequestInference); ok {
			requestType1, err := toolValidator.InferToolRequestType(ctx, toolName, action.ToolInput)
			if err != nil {
				ctx.GetLogger().Error("auth: unable to infer tool request type", "error", err, "tool", toolName, "input", action.ToolInput, "agent", agent.GetName())
				return nil, nil, err
			}
			requestType = requestType1
			if requestType != "" && requestType != toolcore.ToolRequestTypeRead {
				if !ctx.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeCreate) {
					ctx.GetLogger().Error("auth: agent is trying to execute command, but user doesnt have access to update", "tool", toolName, "input", action.ToolInput, "agent", agent.GetName(), "user", ctx.GetSecurityContext().GetUserId(), "account", request.AccountId)
					return &NBAgentPlannerFinishAction{
						Data: fmt.Sprintf("auth: agent is trying to execute - %s, but user doesnt have access to update", action.ToolInput),
					}, &requestType, nil
				}
			}
		}
		// Fall through to LLM-based classification if heuristic was not available or returned empty (unrecognized verb)
		if requestType == "" {
			if toolValidator, ok := tool.(toolcore.ToolRequestInferencePrompt); ok {
				prompt, err := toolValidator.InferToolRequestTypePrompt(ctx, toolName, action.ToolInput)
				if err != nil {
					ctx.GetLogger().Error("auth: unable to infer tool request type", "error", err, "tool", toolName, "input", action.ToolInput, "agent", agent.GetName())
					return nil, nil, err
				}
				if prompt != "" {
					response, err := GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, false, []llms.MessageContent{
						{
							Role:  llms.ChatMessageTypeSystem,
							Parts: []llms.ContentPart{llms.TextContent{Text: prompt}},
						},
						{
							Role:  llms.ChatMessageTypeHuman,
							Parts: []llms.ContentPart{llms.TextContent{Text: action.ToolInput}},
						},
					}, true)
					if err != nil {
						ctx.GetLogger().Error("auth: unable to execute llm model for infering tool request type", "error", err, "agent", agent.GetName())
						return nil, nil, err
					}
					requestTypeStr := strings.ToLower(response.Choices[0].Content)
					if strings.Contains(requestTypeStr, "\n") {
						requestTypeStr = strings.Split(requestTypeStr, "\n")[0]
					}
					requestTypeStr = strings.TrimSpace(requestTypeStr)
					if requestTypeStr == "write" || requestTypeStr == "execute" {
						requestTypeStr = string(toolcore.ToolRequestTypeCreate)
					}

					if requestTypeStr != string(toolcore.ToolRequestTypeRead) && requestTypeStr != string(toolcore.ToolRequestTypeCreate) && requestTypeStr != string(toolcore.ToolRequestTypeUpdate) && requestTypeStr != string(toolcore.ToolRequestTypeDelete) {
						ctx.GetLogger().Warn("auth: classification returned unrecognized type, routing to user approval", "request_type", requestTypeStr, "tool", toolName, "input", action.ToolInput, "agent", agent.GetName())
						requestTypeStr = string(toolcore.ToolRequestTypeUpdate)
					}
					requestType = toolcore.ToolRequestType(requestTypeStr)
					if requestType != "" && requestType != toolcore.ToolRequestTypeRead {
						if !ctx.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeCreate) {
							ctx.GetLogger().Error("auth: agent is trying to execute command, but user doesnt have access to update", "tool", toolName, "input", action.ToolInput, "agent", agent.GetName(), "user", ctx.GetSecurityContext().GetUserId(), "account", request.AccountId)
							return &NBAgentPlannerFinishAction{
								Data: fmt.Sprintf("auth: agent is trying to execute - %s, but user doesnt have access to update", action.ToolInput),
							}, &requestType, nil
						}
					}
				}
			}
		}
	}

	// Default to read if no classification was performed (tool implements neither interface)
	if requestType == "" {
		requestType = toolcore.ToolRequestTypeRead
	}
	return nil, &requestType, nil
}
