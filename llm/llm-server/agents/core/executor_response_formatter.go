package core

import (
	"fmt"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"regexp"
	"strings"

	"github.com/samber/lo"
	"github.com/tmc/langchaingo/llms"
)

var slackIDRegex = regexp.MustCompile(`^[CUW][A-Z0-9]{8,}`)

// fenceLanguageRegex matches a language token on a code fence's opening line
// (e.g. "```markdown\n"), which Slack mrkdwn renders as a literal first line.
var fenceLanguageRegex = regexp.MustCompile("^```[ \\t]*[A-Za-z][A-Za-z0-9_+#.-]*[ \\t]*\\r?\\n")

// Pre-compiled markdown-to-Slack conversion patterns — avoids recompiling on every agent response.
var (
	reCodeBlock     = regexp.MustCompile("(?s)```(.*?)```")
	reInlineCode    = regexp.MustCompile("`([^`]+)`")
	reBoldItalic    = regexp.MustCompile(`\*\*\*(.+?)\*\*\*`)
	reBold          = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reItalic        = regexp.MustCompile(`\*(.+?)\*`)
	reHeader        = regexp.MustCompile(`(?m)^#+\s+(.*)$`)
	reUnderlineBold = regexp.MustCompile(`__(.*?)__`)
	reLink          = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reStrikethrough = regexp.MustCompile(`~~(.*?)~~`)
)

// isSlackRequest checks if the request originated from Slack by checking if the session ID
// matches a common Slack ID pattern (e.g., starting with C, U, or W).
func isSlackRequest(request NBAgentRequest) bool {
	if request.SessionId == "" {
		return false
	}
	return slackIDRegex.MatchString(request.SessionId)
}

func convertMarkdownToSlackMarkdown(response string) string {
	// Map to store code blocks and their placeholders
	codeBlocks := make(map[string]string)
	i := 0

	response = reCodeBlock.ReplaceAllStringFunc(response, func(match string) string {
		match = fenceLanguageRegex.ReplaceAllString(match, "```\n")
		placeholder := fmt.Sprintf("||CODE_BLOCK_%d||", i)
		codeBlocks[placeholder] = match
		i++
		return placeholder
	})

	response = reInlineCode.ReplaceAllStringFunc(response, func(match string) string {
		placeholder := fmt.Sprintf("||CODE_BLOCK_%d||", i)
		codeBlocks[placeholder] = match
		i++
		return placeholder
	})

	response = reBoldItalic.ReplaceAllString(response, "||BI_S||$1||BI_E||")
	response = reBold.ReplaceAllString(response, "||B_S||$1||B_E||")
	response = reItalic.ReplaceAllString(response, "||I_S||$1||I_E||")
	response = reHeader.ReplaceAllString(response, "*$1*")
	response = reUnderlineBold.ReplaceAllString(response, "*$1*")
	response = reLink.ReplaceAllString(response, "<$2|$1>")
	response = reStrikethrough.ReplaceAllString(response, "~$1~")

	// 4. Replace placeholders with final Slack markup
	response = strings.ReplaceAll(response, "||BI_S||", "_*")
	response = strings.ReplaceAll(response, "||BI_E||", "*_")
	response = strings.ReplaceAll(response, "||B_S||", "*")
	response = strings.ReplaceAll(response, "||B_E||", "*")
	response = strings.ReplaceAll(response, "||I_S||", "_")
	response = strings.ReplaceAll(response, "||I_E||", "_")

	// 5. Restore code blocks
	for placeholder, codeBlock := range codeBlocks {
		response = strings.ReplaceAll(response, placeholder, codeBlock)
	}

	return response
}

// FormatAgentResponse reformats the raw agent answer into a polished markdown
// response.  plannerType is used to decide whether to inject a Step Reference
// Guide: for ReAct3 the planner assigns sequential DisplayIDs (E1, E2, …) to
// every action and the guide anchors those IDs for the formatter LLM.  For
// ReWoo the solver already produces correctly-numbered citations so the guide
// is skipped — injecting it would risk the formatter LLM re-numbering
// citations that are already correct.
func FormatAgentResponse(ctx *security.RequestContext, request NBAgentRequest, response NBAgentResponse, plannerType AgentPlannerType) NBAgentResponse {
	systemPrompt := prompts.GetPrompt(ctx.GetContext(), prompts.PromptResponseFormatter, request.AccountId)
	if systemPrompt == "" {
		systemPrompt = prompts_repo.GetPrompt(prompts_repo.PromptExecutor_response_formatter)
	}

	// Build a step reference guide only for React_3 where the planner assigns
	// sequential DisplayIDs. For ReWoo the solver already produces correctly-
	// numbered citations ([Tool - E1](#task-E1)) so we must not inject a guide
	// that could cause the formatter to re-number those correct references.
	buildGuide := plannerType == AgentPlannerTypeReAct3
	var refGuideLines []string
	var supportingParts []string
	for i, t := range response.AgentStepResponse {
		toolName := ""
		if t.Call.FunctionCall != nil {
			toolName = t.Call.FunctionCall.Name
		}
		if buildGuide {
			stepID := fmt.Sprintf("E%d", i+1)
			if toolName != "" {
				refGuideLines = append(refGuideLines, fmt.Sprintf("- %s = %s", stepID, toolName))
			}
			label := stepID
			if toolName != "" {
				label = fmt.Sprintf("%s (%s)", stepID, toolName)
			}
			supportingParts = append(supportingParts, fmt.Sprintf("[%s]\n%s", label, t.Response.Content))
		} else {
			supportingParts = append(supportingParts, t.Response.Content)
		}
	}

	stepReferenceGuide := ""
	if len(refGuideLines) > 0 {
		stepReferenceGuide = "**Step Reference Guide** (use these IDs for citations — do NOT invent new ones):\n" +
			strings.Join(refGuideLines, "\n") + "\n\n"
	}

	supportingDataSteps := strings.Join(supportingParts, "\n\n")

	questionType := lo.Ternary(IsInvestigationRequestTask(request.Query), "investigation", "query")

	userPrompt := fmt.Sprintf(`
	**Question Type** = %s
	**Question** = %s

	**Answer** = %s

	%s**Supporting Data** = %s

	`,
		questionType,
		request.Query,
		response.Response,
		stepReferenceGuide,
		supportingDataSteps,
	)

	messageContent := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
	}
	messageContent = append(messageContent,
		llms.TextParts(llms.ChatMessageTypeHuman, userPrompt),
	)
	completion, err := GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.ParentAgentId, true, messageContent, false, WithThinkingLevel(ThinkingLevelFastTask))
	if err != nil {
		ctx.GetLogger().Error("request: unable to generate content", "error", err)
		return response
	}

	if len(completion.Choices) == 0 || completion.Choices[0].Content == "" {
		return response
	}

	response.Response = []string{completion.Choices[0].Content}
	return response
}
