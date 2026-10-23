package core

import (
	"bytes"
	"fmt"
	llmruntime "nudgebee/llm/llms/runtime"
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
	reNestedItalic  = regexp.MustCompile(`\*(_[^*]+?_)\*`)
	reItalic        = regexp.MustCompile(`\*(.+?)\*`)
	reHeader        = regexp.MustCompile(`(?m)^#+\s+(.*)$`)
	reUnderlineBold = regexp.MustCompile(`__(.*?)__`)
	reLink          = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reStrikethrough = regexp.MustCompile(`~~(.*?)~~`)
)

// citationLabelCleaner drops the characters that would break a Slack link label.
var citationLabelCleaner = strings.NewReplacer("<", "", ">", "", "|", "")

// isSlackRequest checks if the request originated from Slack by checking if the session ID
// matches a common Slack ID pattern (e.g., starting with C, U, or W).
func isSlackRequest(request NBAgentRequest) bool {
	if request.SessionId == "" {
		return false
	}
	return slackIDRegex.MatchString(request.SessionId)
}

// convertMarkdownToSlackMarkdown converts an answer to Slack mrkdwn. A step
// citation links to that step on citationPageURL (the app page showing the
// answer); without one it is reduced to its label.
func convertMarkdownToSlackMarkdown(response string, citationPageURL string) string {
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

	// A step citation's #task-<id> target only resolves inside the app; Slack
	// would show <#task-<id>|Tool - E3> as raw text. Link it to the step on the
	// app page instead, or keep just the label. The result is held out like
	// code so the passes below leave it alone.
	response = anyStepLink.ReplaceAllStringFunc(response, func(link string) string {
		m := anyStepLink.FindStringSubmatch(link)
		label := strings.TrimSpace(citationLabelCleaner.Replace(m[1]))
		if label == "" {
			return ""
		}
		cited := "[" + label + "]"
		if citationPageURL != "" && rowIDTarget.MatchString(m[2]) {
			cited = "<" + citationPageURL + "#task-" + m[2] + "|" + label + ">"
		}
		placeholder := fmt.Sprintf("||CODE_BLOCK_%d||", i)
		codeBlocks[placeholder] = cited
		i++
		return placeholder
	})

	response = reBoldItalic.ReplaceAllString(response, "||BI_S||$1||BI_E||")
	response = reBold.ReplaceAllString(response, "||B_S||$1||B_E||")
	response = reNestedItalic.ReplaceAllString(response, "$1")
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
			// The planner's own id, which the answer already cites. Counting by
			// position drifts from it whenever a step was dropped, replaced or
			// reused from the cache, and the guide then relabels citations.
			stepID := t.DisplayID
			if stepID == "" {
				stepID = fmt.Sprintf("E%d", i+1)
			}
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
	systemPrompt, promptErr := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptResponseFormatter, request.AccountId)
	if promptErr != nil {
		// Formatting with an empty system prompt would silently mangle the answer.
		// Returning the response unformatted is the safe outcome: the content is
		// intact and merely un-prettified, where proceeding would ship the user a
		// garbled answer produced under no instruction.
		ctx.GetLogger().Error("formatter: system prompt failed to load, returning response unformatted", "error", promptErr)
		return response
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
	completion, err := llmruntime.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.ParentAgentId, true, messageContent, false, llmruntime.WithThinkingLevel(llmruntime.ThinkingLevelFastTask))
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
