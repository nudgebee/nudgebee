package core

import (
	"bytes"
	"fmt"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"regexp"
	"strings"
	"text/template"

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

// buildStepReferenceParts renders the per-step supporting-data block and, for
// ReAct3-executed responses, a Step Reference Guide that maps each step's
// sequential ID (E1, E2, …) to its tool so the formatter LLM cites real IDs.
// plannerType must be the effective (runtime) type: orchestrating and react
// agents run as react_3 and assign DisplayIDs, so they get the guide too.
// Returns the guide block (empty when not applicable) and the joined supporting data.
func buildStepReferenceParts(response NBAgentResponse, plannerType AgentPlannerType) (guide string, supportingData string) {
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

	if len(refGuideLines) > 0 {
		guide = "**Step Reference Guide** (use these IDs for citations — do NOT invent new ones):\n" +
			strings.Join(refGuideLines, "\n") + "\n\n"
	}
	return guide, strings.Join(supportingParts, "\n\n")
}

// FormatAgentResponse reformats the raw agent answer into a polished markdown
// response.  plannerType must be the *effective* (runtime) planner type — see
// resolveEffectivePlannerType. When it is ReAct3 the planner assigned sequential
// DisplayIDs (E1, E2, …) to every action, and the guide anchors those IDs for the
// formatter LLM so it cites real step IDs instead of inventing new ones.
func FormatAgentResponse(ctx *security.RequestContext, request NBAgentRequest, response NBAgentResponse, plannerType AgentPlannerType) NBAgentResponse {
	questionType := lo.Ternary(IsInvestigationRequestTask(request.Query), "investigation", "query")

	// Render the formatter prompt as a Go template so the causality-chain instructions
	// are gated on `is_investigation`. On a query turn the LLM never sees the
	// "how to build a Causality Chain" spec — removing the instruction is far more
	// reliable than the prose "if query, do NOT add" it was observed ignoring (a
	// "generate dashboard" turn came back with a `### Causality Chain (5-Whys)`).
	tmplData := map[string]interface{}{"is_investigation": questionType == "investigation"}
	systemPrompt := prompts.GetPrompt(ctx.GetContext(), prompts.PromptResponseFormatter, request.AccountId)
	if systemPrompt == "" {
		systemPrompt = prompts_repo.GetPrompt(prompts_repo.PromptExecutor_response_formatter)
	}
	systemPrompt = renderFormatterTemplate(systemPrompt, tmplData)

	stepReferenceGuide, supportingDataSteps := buildStepReferenceParts(response, plannerType)

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

// renderFormatterTemplate renders the formatter system prompt as a Go template so its
// `{{if .is_investigation}}` gates resolve. It returns the input unchanged if the text
// isn't a valid template or rendering fails (e.g. a DB-overridden prompt with no
// template actions), so a malformed override never breaks formatting.
func renderFormatterTemplate(text string, data map[string]interface{}) string {
	tmpl, err := template.New("formatter").Parse(text)
	if err != nil {
		return text
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return text
	}
	return buf.String()
}
