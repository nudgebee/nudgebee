package core

import (
	"context"
	"fmt"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"time"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/prompts"
)

// NBAgent is an Agent driven by Tools.
type PromptPlanner struct {
	ctx     *security.RequestContext
	prompt  prompts.FormatPrompter
	request NBAgentRequest
	tools   []toolcore.NBTool
	nbAgent NBAgent
}

// Plan decides what action to take or returns the final result of the input.
func (o *PromptPlanner) Plan(
	ctx context.Context,
	intermediateSteps []NBAgentPlannerToolActionStep,
	input string,
) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error) {
	fullInputs := map[string]any{}
	fullInputs["input"] = input
	fullInputs["query_context"] = o.request.ConversationContext
	fullInputs[agentScratchpad] = o.constructScratchPad(intermediateSteps)
	fullInputs["today"] = time.Now().Format("January 02, 2006")

	prompt, err := o.prompt.FormatPrompt(fullInputs)
	if err != nil {
		return nil, nil, err
	}

	mcList := []llms.MessageContent{}
	for _, msg := range prompt.Messages() {
		role := msg.GetType()
		text := msg.GetContent()

		var mc llms.MessageContent

		switch p := msg.(type) {
		case llms.AIChatMessage:
			mc = llms.MessageContent{
				Role: role,
				Parts: []llms.ContentPart{
					llms.ToolCall{
						ID:           p.ToolCalls[0].ID,
						Type:         p.ToolCalls[0].Type,
						FunctionCall: p.ToolCalls[0].FunctionCall,
					},
				},
			}
			mcList = append(mcList, mc)
		default:
			mc = llms.MessageContent{
				Role:  role,
				Parts: []llms.ContentPart{llms.TextContent{Text: text}},
			}
			mcList = append(mcList, mc)
		}
	}

	result, err := GenerateAndTrackLLMContent(o.ctx, o.request.UserId, o.request.AccountId, o.request.ConversationId, o.request.MessageId, o.request.ParentAgentId, true, mcList, true, llms.WithTools(nbToolsToLlmTools(o.tools)), llms.WithTemperature(0.1))
	if err != nil {
		o.ctx.GetLogger().Error("toolagent: unable to save agent thought", "error", err)
		return nil, nil, err
	}

	actions, finish, err := o.parseOutput(result)

	return actions, finish, err
}

func (o *PromptPlanner) GetTools() []toolcore.NBTool {
	return o.tools
}

func (o *PromptPlanner) Marshal() ([]byte, error) {
	return nil, nil
}

func (o *PromptPlanner) Unmarshal([]byte) error {
	return nil
}

func (o *PromptPlanner) constructScratchPad(steps []NBAgentPlannerToolActionStep) []llms.ChatMessage {
	if len(steps) == 0 {
		return nil
	}

	messages := make([]llms.ChatMessage, 0)
	for _, step := range steps {
		messages = append(messages, llms.ToolChatMessage{
			ID:      step.Action.ToolID,
			Content: step.Observation,
		})
	}

	return messages
}

func (o *PromptPlanner) parseOutput(contentResp *llms.ContentResponse) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error) {
	action, finish, err := o.parseOutputInternal(contentResp)
	if _, ok := o.nbAgent.(NBAgentExecutorLlmResponseHandler); ok {
		handler := o.nbAgent.(NBAgentExecutorLlmResponseHandler)
		action1 := []NBAgentPlannerToolAction{}
		for _, a := range action {
			action1 = append(action1, NBAgentPlannerToolAction{
				Tool:      a.Tool,
				ToolInput: a.ToolInput,
				ToolID:    a.ToolID,
				Log:       a.Log,
			})
		}
		action1, finish, err = handler.UpdateExecutorLlmResponse(action1, finish, err)
		if action1 != nil {
			action = []NBAgentPlannerToolAction{}
			for _, a := range action1 {
				action = append(action, NBAgentPlannerToolAction{
					Tool:      a.Tool,
					ToolInput: a.ToolInput,
					ToolID:    a.ToolID,
					Log:       a.Log,
				})
			}
		}
	}
	return action, finish, err
}

func (o *PromptPlanner) parseOutputInternal(contentResp *llms.ContentResponse) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error) {
	choice := contentResp.Choices[0]

	// finish
	if len(choice.ToolCalls) == 0 {
		return nil, &NBAgentPlannerFinishAction{
			Data: choice.Content,
			Log:  choice.Content,
		}, nil
	}

	return []NBAgentPlannerToolAction{
		{
			Tool:      choice.ToolCalls[0].FunctionCall.Name,
			ToolInput: choice.ToolCalls[0].FunctionCall.Arguments,
			Log:       fmt.Sprintf("Invoking: %s with %s", choice.ToolCalls[0].FunctionCall.Name, choice.ToolCalls[0].FunctionCall.Arguments),
			ToolID:    choice.ToolCalls[0].ID,
		},
	}, nil, nil
}

func createPrompt(ctx *security.RequestContext, systemMessage string, tools []toolcore.NBTool, accountId string, agentName string, conversationContext string, extraMessages []prompts.MessageFormatter) prompts.ChatPromptTemplate {
	messageFormatters := []prompts.MessageFormatter{prompts.NewSystemMessagePromptTemplate(systemMessage, nil)}
	if IsSLMEnabled(accountId, agentName) {
		messageFormatters = append(messageFormatters, prompts.NewHumanMessagePromptTemplate(`Question: {{.input}}`, []string{"input"}))
	} else {
		messageFormatters = append(messageFormatters, prompts.NewHumanMessagePromptTemplate(`
	Today is {{.today}}.
	Answer the following questions as best you can using the available tools. 
	You have access to the following tools: {{.tool_names}}

	Begin!

	Question Previous Context: 
	--------------------------------
	{{.conversation_context}}

	Previous Messages:
	--------------------------
	{{.history}}

	Question Current Context:
	--------------------------
	{{ .query_context }}

	Additional Agent Instructions
	--------------------------
	{{ .additional_agent_prompt }}

	Question:
	-------------------
	{{.input}}

	`, []string{"input"}))
	}

	messageFormatters = append(messageFormatters, prompts.MessagesPlaceholder{
		VariableName: agentScratchpad,
	})

	additionalInstructions, _, _ := AgentAdditionalInstructionsAndToolsAndConfigs(ctx, accountId, agentName)

	previousMessageStr := messageFormatterToString(extraMessages)
	tmpl := prompts.NewChatPromptTemplate(messageFormatters)
	tmpl.PartialVariables = map[string]any{
		"tool_names":              reActPromptToolNames(tools),
		"today":                   time.Now().Format("January 02, 2006"),
		"history":                 previousMessageStr,
		"query_context":           "",
		"conversation_context":    conversationContext,
		"additional_agent_prompt": additionalInstructions,
	}
	return tmpl
}

func NewPromptAgent(ctx *security.RequestContext, request NBAgentRequest, nbAgent NBAgent, systemMessage string, extraMessages []prompts.MessageFormatter) (*PromptPlanner, error) {
	if request.ConversationContext == "" {
		request.ConversationContext = "No additional context provided."
	}
	tools := nbAgent.GetSupportedTools(ctx)
	return &PromptPlanner{
		ctx:     ctx,
		prompt:  createPrompt(ctx, systemMessage, tools, request.AccountId, nbAgent.GetName(), request.ConversationContext, extraMessages),
		nbAgent: nbAgent,
		request: request,
		tools:   tools,
	}, nil
}
