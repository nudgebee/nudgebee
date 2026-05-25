package core

import (
	"context"
	"fmt"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"strings"

	"github.com/samber/lo"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/prompts"
)

// ClassificationPlanner is a planner that classifies input against a set of options.
type ClassificationPlanner struct {
	ctx     *security.RequestContext
	prompt  prompts.FormatPrompter
	request NBAgentRequest
	nbAgent NBAgent
	options []string
}

// NewClassificationPlanner creates a new classification planner.
func NewClassificationPlanner(
	ctx *security.RequestContext,
	request NBAgentRequest,
	nbAgent NBAgent,
	systemMessage string,
	options []string,
) (*ClassificationPlanner, error) {
	if len(options) == 0 {
		return nil, fmt.Errorf("at least one option must be provided for classification")
	}

	return &ClassificationPlanner{
		ctx:     ctx,
		prompt:  createClassificationPrompt(systemMessage, options),
		request: request,
		nbAgent: nbAgent,
		options: options,
	}, nil
}

func createClassificationPrompt(systemMessage string, options []string) prompts.ChatPromptTemplate {
	optionsStr := "- " + strings.Join(options, "\n- ")

	strictInstructions := "You MUST choose one of the options from the list provided. Your response MUST be ONLY the name of the chosen option. Do not add any other text, explanation, or punctuation. Do not make up new options."
	fullSystemMessage := fmt.Sprintf("%s\n\n%s\n\nYour options are:\n%s", systemMessage, strictInstructions, optionsStr)

	messageFormatters := []prompts.MessageFormatter{
		prompts.NewSystemMessagePromptTemplate(fullSystemMessage, nil),
		prompts.NewHumanMessagePromptTemplate("{{.input}}", []string{"input"}),
	}

	tmpl := prompts.NewChatPromptTemplate(messageFormatters)
	tmpl.PartialVariables = map[string]any{}
	return tmpl
}

// Plan decides which option to select based on the input.
func (p *ClassificationPlanner) Plan(
	ctx context.Context,
	intermediateSteps []NBAgentPlannerToolActionStep,
	input string,
) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error) {
	logger := p.ctx.GetLogger().With("agent", p.nbAgent.GetName(), "agent_id", p.request.AgentId)
	logger.Debug("classification_planner: planning", "input", input)

	fullInputs := map[string]any{"input": input}

	prompt, err := p.prompt.FormatPrompt(fullInputs)
	if err != nil {
		return nil, nil, err
	}

	mcList := lo.Map(prompt.Messages(), func(msg llms.ChatMessage, _ int) llms.MessageContent {
		return llms.MessageContent{
			Role:  msg.GetType(),
			Parts: []llms.ContentPart{llms.TextContent{Text: msg.GetContent()}},
		}
	})

	var chosenOption string
	var isValid bool

	for i := range 3 {
		result, err := GenerateAndTrackLLMContent(p.ctx, p.request.UserId, p.request.AccountId, p.request.ConversationId, p.request.MessageId, p.request.ParentAgentId, true, mcList, true, llms.WithTemperature(0.0), WithThinkingLevel(ThinkingLevelFastTask))
		if err != nil {
			logger.Error("classification_planner: unable to process llm request", "error", err, "attempt", i+1)
			return nil, nil, err
		}

		if len(result.Choices) == 0 || result.Choices[0].Content == "" {
			return nil, nil, fmt.Errorf("no content in response from LLM")
		}

		chosenOption = strings.TrimSpace(result.Choices[0].Content)
		isValid = false
		for _, opt := range p.options {
			if strings.EqualFold(opt, chosenOption) {
				chosenOption = opt // Use the canonical casing
				isValid = true
				break
			}
		}

		if isValid {
			break // Success
		}

		// If not valid and not the last attempt, prepare for retry
		if i < 2 {
			logger.Warn("classification_planner: LLM returned an invalid option, retrying...", "returned_option", chosenOption, "attempt", i+1)

			// Add AI's invalid response to history
			mcList = append(mcList, llms.MessageContent{
				Role:  llms.ChatMessageTypeAI,
				Parts: []llms.ContentPart{llms.TextContent{Text: result.Choices[0].Content}},
			})

			// Add a new instruction for the LLM
			retryPrompt := fmt.Sprintf(
				"Your response '%s' is not a valid option. You MUST choose one of the options from the list provided. Do not add any other text to your response.",
				chosenOption,
			)
			mcList = append(mcList, llms.MessageContent{
				Role:  llms.ChatMessageTypeHuman,
				Parts: []llms.ContentPart{llms.TextContent{Text: retryPrompt}},
			})
		}
	}

	if !isValid {
		err := fmt.Errorf("LLM returned an invalid option after 3 attempts: '%s'. Valid options are: %v", chosenOption, p.options)
		logger.Error("classification_planner: failed after retries", "error", err)
		return nil, nil, err
	}

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
