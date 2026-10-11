package core

import (
	"context"
	"fmt"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"strings"
)

// multiOptionSeparator joins the options of a multi-select classification answer.
// Both the instruction given to the model and the canonical response use it.
const multiOptionSeparator = ", "

// ClassificationPlanner is a planner that classifies input against a set of
// options. It is a thin adapter over Classify, which owns the prompt, answer
// validation and retries; the planner keeps the agent's tier and cache scope and
// fails the run when no valid option comes back.
type ClassificationPlanner struct {
	ctx           *security.RequestContext
	systemMessage string
	request       NBAgentRequest
	nbAgent       NBAgent
	options       []string
	// multi allows the answer to name several options, comma-separated. Off by
	// default: a single-select agent must keep rejecting a multi-option answer.
	multi bool
}

// NewClassificationPlanner creates a new classification planner. When multi is
// true the model may answer with several options separated by ", "; every one of
// them is still validated against the options list, so a multi-select answer can
// no more invent a name than a single-select one can.
func NewClassificationPlanner(
	ctx *security.RequestContext,
	request NBAgentRequest,
	nbAgent NBAgent,
	systemMessage string,
	options []string,
	multi bool,
) (*ClassificationPlanner, error) {
	if len(options) == 0 {
		return nil, fmt.Errorf("at least one option must be provided for classification")
	}

	return &ClassificationPlanner{
		ctx:           ctx,
		systemMessage: systemMessage,
		request:       request,
		nbAgent:       nbAgent,
		options:       options,
		multi:         multi,
	}, nil
}

// task is the agent's decision for Classify: its system message as the
// instructions and its options as the labels.
func (p *ClassificationPlanner) task() ClassificationTask {
	opts := make([]ClassificationOption, len(p.options))
	for i, o := range p.options {
		opts[i] = ClassificationOption{Label: o}
	}
	return ClassificationTask{
		Name:         "agent:" + p.nbAgent.GetName(),
		Instructions: p.systemMessage,
		Options:      opts,
		Multi:        p.multi,
	}
}

// Plan decides which option to select based on the input.
func (p *ClassificationPlanner) Plan(
	ctx context.Context,
	intermediateSteps []NBAgentPlannerToolActionStep,
	input string,
) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error) {
	logger := p.ctx.GetLogger().With("agent", p.nbAgent.GetName(), "agent_id", p.request.AgentId)
	logger.Debug("classification_planner: planning", "input", input)

	// Honour the agent's declared cache scope, mirroring the ReAct planners.
	// GenerateAndTrackLLMContent reads ContextKeyCacheScope from the context and
	// defaults to Conversation scope when it is absent, so without this an agent's
	// GetCacheScope() would have no effect on the classification path.
	llmCtx := p.ctx
	if cacheProvider, ok := p.nbAgent.(NBAgentCacheScopeProvider); ok {
		llmCtx = security.NewRequestContext(
			context.WithValue(p.ctx.GetContext(), ContextKeyCacheScope, cacheProvider.GetCacheScope()),
			p.ctx.GetSecurityContext(),
			p.ctx.GetLogger(),
			p.ctx.GetTracer(),
			p.ctx.GetMeter(),
		)
	}

	// The agent's own tier (stamped by the executor) is kept, as before.
	decision, err := Classify(llmCtx, ClassificationCall{
		UserId:         p.request.UserId,
		AccountId:      p.request.AccountId,
		ConversationId: p.request.ConversationId,
		MessageId:      p.request.MessageId,
		AgentId:        p.request.ParentAgentId,
		KeepCallerTier: true,
		TrackContent:   true,
	}, p.task(), input)
	if err != nil {
		logger.Error("classification_planner: unable to process llm request", "error", err)
		return nil, nil, err
	}
	if !decision.Valid {
		err := fmt.Errorf("LLM returned an invalid option after %d attempts: '%s'. Valid options are: %v", classificationLLMAttempts, strings.TrimSpace(decision.Raw), p.options)
		logger.Error("classification_planner: failed after retries", "error", err)
		return nil, nil, err
	}
	chosenOption := strings.Join(decision.Labels, multiOptionSeparator)

	logger.Debug("classification_planner: finished", "chosen_option", chosenOption)

	return nil, &NBAgentPlannerFinishAction{
		Data:   chosenOption,
		Status: ConversationStatusCompleted,
	}, nil
}

// GetTools returns the tools for the planner.
func (p *ClassificationPlanner) GetTools() []toolcore.NBTool {
	// This planner does not use tools.
	return []toolcore.NBTool{}
}

func (p *ClassificationPlanner) Marshal() ([]byte, error) {
	return nil, nil
}

func (p *ClassificationPlanner) Unmarshal([]byte) error {
	return nil
}
