package core

import (
	"context"
	"encoding/xml"
	"fmt"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"regexp"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/prompts"
)

const (
	maxCritiquerRetries     = 3
	critiquerDecisionRefine = "refine"
	critiquerDecisionAccept = "accept"
)

var (
	rewooCritiquerDecisionRegex = regexp.MustCompile(`(?s)<decision>(.*?)</decision>`)
	rewooCritiquerFeedbackRegex = regexp.MustCompile(`(?s)<feedback>(.*?)</feedback>`)
)

type rewooCritiquerResponse struct {
	XMLName  xml.Name `xml:"critique_response"`
	Thought  string   `xml:"thought"`
	Decision string   `xml:"decision"` // "accept" or "refine"
	Feedback string   `xml:"feedback"`
}

type ReWooCritiquer struct {
	ctx     *security.RequestContext
	prompt  prompts.FormatPrompter
	request NBAgentRequest
	nbAgent NBAgent
}

func NewReWooCritiquer(ctx *security.RequestContext, request NBAgentRequest, nbAgent NBAgent, agentPrompt string) (*ReWooCritiquer, error) {
	return &ReWooCritiquer{
		ctx:     ctx,
		prompt:  createCritiquerPrompt(request, nbAgent, agentPrompt),
		request: request,
		nbAgent: nbAgent,
	}, nil
}

// Critique evaluates the final answer from the solver and decides if it needs refinement.
// It returns:
// - bool: true if refinement is needed, false otherwise.
// - string: the feedback for refinement.
// - error: if an error occurs.
func (c *ReWooCritiquer) Critique(
	ctx context.Context,
	input string,
	intermediateSteps []NBAgentPlannerToolActionStep,
	finalAnswer *NBAgentPlannerFinishAction,
	notebook string,
	questionType string,
	toolDescriptions string,
) (bool, string, error) {
	scratchpad := ConstructScratchPad(intermediateSteps, ScratchpadContext{Ctx: c.ctx, Request: c.request})

	fullInputs := map[string]any{
		"input":             input,
		"scratchpad":        scratchpad,
		"final_answer":      finalAnswer.Data,
		"notebook":          notebook,
		"question_type":     questionType,
		"tool_descriptions": toolDescriptions,
	}
	fullInputs["today"] = time.Now().Format("January 02, 2006")
	fullInputs["task_context"] = c.request.QueryContext
	fullInputs["conversation_context"] = c.request.ConversationContext
	fullInputs["history"] = ""
	fullInputs["tool_names"] = reActPromptToolNames(c.nbAgent.GetSupportedTools(c.ctx))

	prompt, err := c.prompt.FormatPrompt(fullInputs)
	if err != nil {
		return false, "", err
	}

	mcList := []llms.MessageContent{}
	for _, msg := range prompt.Messages() {
		mcList = append(mcList, llms.MessageContent{
			Role:  msg.GetType(),
			Parts: []llms.ContentPart{llms.TextContent{Text: msg.GetContent()}},
		})
	}

	var result *llms.ContentResponse
	// improve loop, if system is not generating correct response or formatting issue then retry with additional error information
	for i := range maxCritiquerRetries {
		c.ctx.GetLogger().Debug("rewoocritiquer: sending llm request", "attempt", i+1)
		// Use a role-qualified agent name so the cache key is distinct from the planner and solver.
		// Passing a non-UUID string causes GenerateAndTrackLLMContent to use it directly as
		// agentName, producing key "accountId:conversationId:k8s_debug_critiquer:model" — stable
		// across requests and separate from the planner's and solver's cache entries.
		critiquerAgentId := c.nbAgent.GetName() + "_critiquer"
		result, err = GenerateAndTrackLLMContent(c.ctx, c.request.UserId, c.request.AccountId, c.request.ConversationId, c.request.MessageId, critiquerAgentId, false, mcList, true,
			llms.WithTemperature(0.0))

		if err != nil {
			c.ctx.GetLogger().Error("rewoocritiquer: unable to process llm request", "error", err)
			continue
		}
		if len(result.Choices) == 0 {
			c.ctx.GetLogger().Error("rewoocritiquer: no data found, retrying")
			continue
		}
		c.ctx.GetLogger().Debug("rewoocritiquer: llm response", "response", result.Choices[0])

		refine, feedback, err := c.parseOutput(result.Choices[0].Content)
		if err != nil {
			c.ctx.GetLogger().Error("rewoocritiquer: failed to parse output, retrying", "error", err, "data", result.Choices[0].Content)
			// NOTE: Do NOT include the error text — the model interprets parse error descriptions
			// as questions to explain, causing it to generate tutorials instead of XML critique.
			errorMsg := "Your previous response was not valid XML. You MUST output ONLY a single XML block with root element <critique_response>. Do not explain, do not output JSON, do not output markdown. Output the XML critique now."
			humanMessage := llms.MessageContent{
				Role:  llms.ChatMessageTypeHuman,
				Parts: []llms.ContentPart{llms.TextContent{Text: errorMsg}},
			}
			mcList = append(mcList, humanMessage)
			continue
		}
		return refine, feedback, nil
	}

	return false, "", fmt.Errorf("critiquer failed after multiple retries")
}

func (c *ReWooCritiquer) parseOutput(output string) (bool, string, error) {
	// Robust XML extraction using regex
	critiqueXml := common.XmlRegexExtract(output, regexp.MustCompile("(?s)```xml\n(.*?)\n```"))
	if critiqueXml == "" {
		if strings.Contains(output, "<critique_response>") || strings.Contains(output, "<thought>") || strings.Contains(output, "<decision>") {
			critiqueXml = output
		}
	}

	if critiqueXml == "" {
		return false, "", fmt.Errorf("rewoocritiquer: unable to find XML block in output")
	}

	// Surgically extract the <critique_response> element to strip any leading <thought> tags
	// that Gemini's native thinking mode injects before the actual root element.
	if critiqueMatch := regexp.MustCompile(`(?s)(<critique_response>.*</critique_response>)`).FindString(critiqueXml); critiqueMatch != "" {
		critiqueXml = critiqueMatch
	}

	critiqueXml = common.XmlSanitize(critiqueXml, "critique_response")

	var critiqueResp rewooCritiquerResponse
	err := xml.Unmarshal([]byte(critiqueXml), &critiqueResp)
	if err != nil {
		c.ctx.GetLogger().Warn("rewoocritiquer: unable to unmarshal critique response XML, attempting regex fallback", "error", err.Error(), "data", critiqueXml)

		// Fallback: Try to extract decision and feedback using regex if XML unmarshalling fails
		extractedDecision := common.XmlExtractCDATA(common.XmlRegexExtract(critiqueXml, rewooCritiquerDecisionRegex))
		extractedFeedback := common.XmlExtractCDATA(common.XmlRegexExtract(critiqueXml, rewooCritiquerFeedbackRegex))

		if strings.ToLower(extractedDecision) == critiquerDecisionRefine {
			c.ctx.GetLogger().Info("rewoocritiquer: successfully extracted critique details using regex fallback (refine)")
			return true, extractedFeedback, nil
		} else if strings.ToLower(extractedDecision) == critiquerDecisionAccept {
			c.ctx.GetLogger().Info("rewoocritiquer: successfully extracted critique details using regex fallback (accept)")
			return false, "", nil
		}

		// If regex fallback also fails to find a clear decision, then return the original error
		return false, "", fmt.Errorf("could not parse critiquer output even with regex fallback: %w", err)
	}

	if strings.ToLower(critiqueResp.Decision) == critiquerDecisionRefine {
		return true, critiqueResp.Feedback, nil
	}

	return false, critiqueResp.Feedback, nil
}

func createCritiquerPrompt(request NBAgentRequest, nbAgent NBAgent, agentPrompt string) prompts.ChatPromptTemplate {
	critiquerPrompt := prompts_repo.GetPrompt(prompts_repo.PromptPlannerRewooCritiquer)

	parts := strings.Split(critiquerPrompt, "## Input")
	systemPart := parts[0]
	humanPart := "## Input\n" + parts[1]

	messageFormatters := []prompts.MessageFormatter{
		prompts.NewSystemMessagePromptTemplate(
			systemPart,
			[]string{},
		),
		prompts.NewHumanMessagePromptTemplate(
			humanPart,
			[]string{
				"input",
				"today",
				"conversation_context",
				"history",
				"task_context",
				"scratchpad",
				"final_answer",
				"notebook",
				"tool_names",
				"tool_descriptions",
				"workspace_enabled",
				"shell_tool_enabled",
				"question_type",
			},
		),
	}

	tmpl := prompts.NewChatPromptTemplate(messageFormatters)

	// Filter and inject tools for the critiquer as well
	availableTools := FilterAndInjectDefaultTools(request.AccountId, nbAgent, agentPrompt, nbAgent.GetSupportedTools(nil), request.Capabilities)

	if len(request.ClientTools) > 0 {
		messageFormatters = append([]prompts.MessageFormatter{
			prompts.NewSystemMessagePromptTemplate(
				`IMPORTANT: The user has provided "Local Tools" (prefixed with 'local_'). 
				If the user's request involves their local machine, files, or shell, you MUST prioritize these Local Tools over server-side counterparts.`,
				[]string{}),
		}, messageFormatters...)
		tmpl = prompts.NewChatPromptTemplate(messageFormatters)
	}

	tmpl.PartialVariables = map[string]any{
		"today":                time.Now().Format("January 02, 2006"),
		"conversation_context": request.ConversationContext,
		"history":              "", // Placeholder for now
		"task_context":         "", // Placeholder for now
		"notebook":             "",
		"tool_names":           reActPromptToolNames(availableTools),
		"tool_descriptions":    reActPromptToolDescriptions(availableTools),
		"workspace_enabled":    config.Config.LlmServerWorkspaceEnabled,
		"shell_tool_enabled":   config.Config.LlmServerShellToolEnabled && HasShellTool(availableTools),
	}
	return tmpl
}
