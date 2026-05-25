package core

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

func init() {

	llmToolDescription := `A general-purpose LLM tool for answering conceptual questions, summarizing text, or performing general reasoning and analysis on provided data. Input should be a clear question or instruction.`
	llmToolInput := "User Question"
	llmToolOutput := "The tool will return the output of the LLM query"

	RegisterNBAgentFactoryAndTool(ToolLlm, func(accountId string) (NBAgent, error) {
		return LLMAgent{}, nil
	}, llmToolDescription, llmToolInput, llmToolOutput)

	clarificationToolDescription := `Get confirmation or clarification from the user.`
	clarificationToolInput := "Clarification Question"
	clarificationToolOutput := "User's response"

	RegisterNBAgentFactoryAndTool(ToolClarification, func(accountId string) (NBAgent, error) {
		return ClarificationAgent{}, nil
	}, clarificationToolDescription, clarificationToolInput, clarificationToolOutput)
}

type LLMAgent struct {
}

func (p LLMAgent) GetName() string {
	return ToolLlm
}

func (a LLMAgent) GetNameAliases() []string {
	return []string{"LLM"}
}

func (p LLMAgent) GetDescription() string {
	return `Uses LLM to provide comprehensive output that includes ALL data from the given question and context. Always provide full information in the question to get the complete output without omitting any details.`
}

func (l LLMAgent) GetSystemPrompt(ctx *security.RequestContext, query NBAgentRequest) NBAgentPrompt {
	return NBAgentPrompt{}
}

func (p LLMAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (l LLMAgent) GetPlannerType() AgentPlannerType {
	return AgentPlannerTypeCustom
}

func (l LLMAgent) Execute(ctx *security.RequestContext, request NBAgentRequest) (NBAgentResponse, error) {
	queryContext := request.QueryContext

	var args struct {
		Query   string `json:"query"`
		Context string `json:"context"`
	}
	if err := common.UnmarshalJson([]byte(request.Query), &args); err != nil {
		args.Query = request.Query
	}
	command := strings.TrimSpace(args.Query)
	if args.Context != "" {
		queryContext = args.Context + "\n\n" + queryContext
	}

	accountInstructions := ""
	if request.AccountPrompt != "" {
		accountInstructions = fmt.Sprintf("\nAccount Instructions:\n%s\n", common.SanitizePromptInput(request.AccountPrompt))
	}

	sourceInfo := ""
	if request.ConversationSource != "" {
		sourceInfo = fmt.Sprintf("Source: %s", common.SanitizePromptInput(string(request.ConversationSource)))
	}

	now := time.Now().UTC().Format(time.RFC3339)

	systemPrompt := prompts.GetPrompt(ctx.GetContext(), prompts.PromptAgentLlm, request.AccountId,
		now, sourceInfo, accountInstructions)
	if systemPrompt == "" {
		systemPrompt = fmt.Sprintf(`
		<instructions>
			Current Date & Time (UTC): %s
			%s
			%s
			You are a seasoned technology expert with deep experience in software engineering and infrastructure operations.
			IMPORTANT: You MUST include ALL relevant data from the provided context in your response. Provide complete and comprehensive answers.
			CRITICAL: Check context for remediation state. If tool="remediation_generate" or text contains "Proposed"/"Can Do"/"AWAITING": use future tense ("I can...", "Would you like me to..."). If tool="remediation_execute" or stdout/stderr present: use past tense ("I executed...", "Applied..."). Default to future tense if unclear.
			Process and include ALL data passed to you in the context - do not truncate or skip relevant information.
			If the context contains tables, statistics, or structured data, include it in your response when relevant.
			AVOID making assumptions or fabricating information not present in the context.
			AVOID unnecessary repetition or explanation of self-explanatory information.

			Primary Instruction:
			- If context is provided (in the <context> tag), prioritize it. Include ALL relevant data from the context.
			- If context is empty, use your expert knowledge within the SRE/DevOps/Development domain.
		</instructions>
		<output_format>
			Respond using professional Markdown:
			- Tables: Use for metrics, statistics, and structured data.
			- Code Blocks: Use triple backticks for logs, JSON, and source code.
			- Lists: Use for recommendations or itemized findings.
			- Data Integrity: Preserve the original structure, headers, and rows of all provided data.
		</output_format>
		`, now, sourceInfo, accountInstructions)
	}

	if val, ok := ctx.GetContext().Value(ContextKeyUseLiteModel).(bool); ok && val {
		systemPrompt = "You are a technical systems expert. Provide a concise and accurate summary of the provided context to answer the user's question. Preserve all technical details and data. Use Markdown format."
	}

	messageContent := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf(`<context>%s</context>`, queryContext)),
		llms.TextParts(llms.ChatMessageTypeHuman, command),
	}
	completion, err := GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, true, messageContent, true)
	if err != nil {
		ctx.GetLogger().Error("llm: unable to generate content", "error", err)
		return NBAgentResponse{Response: nil}, err
	}

	content := strings.TrimSpace(completion.Choices[0].Content)
	if len(content) == 0 {
		return NBAgentResponse{Response: []string{request.Query}}, nil
	}

	return NBAgentResponse{
		Response: []string{content},
		Status:   ConversationStatusCompleted,
	}, nil
}

const ToolClarification = "clarification"

type ClarificationAgent struct {
}

func (p ClarificationAgent) GetName() string {
	return ToolClarification
}

func (a ClarificationAgent) GetNameAliases() []string {
	return []string{"Followup", "Clarification"}
}

func (p ClarificationAgent) GetDescription() string {
	return `Get confirmation or clarification from the user.`
}

func (l ClarificationAgent) GetSystemPrompt(ctx *security.RequestContext, query NBAgentRequest) NBAgentPrompt {
	return NBAgentPrompt{}
}

func (p ClarificationAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (l ClarificationAgent) GetPlannerType() AgentPlannerType {
	return AgentPlannerTypeCustom
}

func (l ClarificationAgent) Execute(ctx *security.RequestContext, request NBAgentRequest) (NBAgentResponse, error) {

	queryContext := request.QueryContext

	var args struct {
		Query string `json:"query"`
	}
	if err := common.UnmarshalJson([]byte(request.Query), &args); err != nil {
		args.Query = request.Query
	}
	command := strings.TrimSpace(args.Query)

	if queryContext != "" {
		command = fmt.Sprintf("Provide answers for the question asked. Stick to the provided context and question. Do not generate random data or facts. \n\n Context: %s\n\nSummarize the data strictly into Markdown format.", queryContext)
	}

	messageContent := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are a seasoned technology expert with deep experience in software engineering and infrastructure operations."),
		llms.TextParts(llms.ChatMessageTypeHuman, command),
	}
	completion, err := GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, true, messageContent, true)
	if err != nil {
		ctx.GetLogger().Error("llm: unable to generate content", "error", err)
		return NBAgentResponse{}, err
	}

	content := strings.TrimSpace(completion.Choices[0].Content)
	if len(content) == 0 {
		return NBAgentResponse{Response: []string{request.Query}}, nil
	}

	agentId := uuid.Nil
	if request.AgentId != "" {
		parsedId, err := uuid.Parse(request.AgentId)
		if err != nil {
			ctx.GetLogger().Warn("llm: malformed agentId in request, defaulting to Nil", "agentId", request.AgentId, "error", err)
		} else {
			agentId = parsedId
		}
	}

	return NBAgentResponse{
		Response: []string{content},
		Status:   ConversationStatusWaiting,
		FollowupRequest: FollowupRequest{
			Question:     content,
			FollowupType: FollowupTypeText,
			AgentName:    l.GetName(),
			AgentId:      agentId,
		},
	}, nil
}
