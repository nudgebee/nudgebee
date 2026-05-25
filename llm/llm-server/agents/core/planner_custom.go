package core

import (
	"context"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/tmc/langchaingo/prompts"
)

// NBAgent is an Agent driven by Tools.
type CustomPlanner struct {
	ctx             *security.RequestContext
	agent           NBCustomAgent
	request         NBAgentRequest
	previousHistory []prompts.MessageFormatter
}

// Plan decides what action to take or returns the final result of the input.
func (o *CustomPlanner) Plan(ctx context.Context, intermediateSteps []NBAgentPlannerToolActionStep, inputs string) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error) {
	if inputs != "" {
		o.request.Query = inputs
	}
	response, err := o.agent.Execute(o.ctx, o.request)
	if err != nil {
		return nil, nil, err
	}

	if response.Status == "" {
		response.Status = ConversationStatusCompleted
	}

	if len(response.Response) == 0 {
		response.Response = []string{"No Response Found"}
	}

	return nil, &NBAgentPlannerFinishAction{
		Data:        response.Response[0],
		Status:      response.Status,
		IsTerminal:  response.IsTerminal,
		Followup:    response.FollowupRequest,
		Invocations: response.AgentStepResponse,
	}, nil
}

func (o *CustomPlanner) GetTools() []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (o *CustomPlanner) Marshal() ([]byte, error) {
	return nil, nil
}

func (o *CustomPlanner) Unmarshal(data []byte) error {
	return nil
}

func NewCustomAgent(ctx *security.RequestContext, request NBAgentRequest, agent NBCustomAgent, messageHistoryFomatter []prompts.MessageFormatter) (*CustomPlanner, error) {
	return &CustomPlanner{
		ctx:             ctx,
		request:         request,
		agent:           agent,
		previousHistory: messageHistoryFomatter,
	}, nil
}
