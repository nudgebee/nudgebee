package core

import (
	"context"
	"encoding/xml"
	"fmt"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	nbprompts "nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"regexp"
	"strings"
	"time"

	"github.com/samber/lo"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/prompts"
)

var (
	rewooSolverContentRegex      = regexp.MustCompile("(?s)<content>(.*?)</content>")
	rewooSolverRequiredInfoRegex = regexp.MustCompile("(?s)<required_information>(.*?)</required_information>")
)

type rewooSolverFinalAnswer struct {
	XMLName xml.Name `xml:"final_answer"`
	Thought string   `xml:"thought"`
	Content string   `xml:"content"`
}

type rewooSolverMissingInfo struct {
	XMLName             xml.Name `xml:"missing_information"`
	Thought             string   `xml:"thought"`
	RequiredInformation string   `xml:"required_information"`
}

type ReWooSolver struct {
	ctx                    *security.RequestContext
	prompt                 prompts.FormatPrompter
	request                NBAgentRequest
	nbAgent                NBAgent
	isInvestigationRequest bool
}

func NewReWooSolver(ctx *security.RequestContext, request NBAgentRequest, nbAgent NBAgent, agentPrompt string) (*ReWooSolver, error) {
	return &ReWooSolver{
		ctx:                    ctx,
		prompt:                 createSolverPrompt(request, nbAgent, request.AccountId, agentPrompt),
		request:                request,
		nbAgent:                nbAgent,
		isInvestigationRequest: IsInvestigationRequestTask(request.Query),
	}, nil
}

func (s *ReWooSolver) Solve(
	ctx context.Context,
	input string,
	intermediateSteps []NBAgentPlannerToolActionStep,
	notebook string,
	taskContext ...string,
) (*NBAgentPlannerFinishAction, string, string, error) {
	updatedNotebook := ""
	var finish *NBAgentPlannerFinishAction
	var missingInfo string
	scratchpad := ConstructScratchPad(intermediateSteps, ScratchpadContext{Ctx: s.ctx, Request: s.request})

	// If no tool steps were executed and this is an investigation (not a simple fast-path query),
	// the solver has nothing to synthesize. Return missing_information to force replanning.
	// NOTE: The fast path intentionally calls Solve with empty steps for simple/greeting queries;
	// guard only applies to investigation requests where tool data is required.
	// EXCEPTION: Queries that explicitly opt out of tool-based investigation (e.g. "do not investigate",
	// "analyze the following data") provide inline artifacts and do not need tool calls — the solver
	// can synthesize directly from the input.
	inputLower := strings.ToLower(strings.TrimSpace(input))
	hasInlineArtifact := strings.Contains(inputLower, "do not investigate") ||
		strings.Contains(inputLower, "analyze the following") ||
		strings.Contains(inputLower, "analyze below")
	if len(intermediateSteps) == 0 && s.isInvestigationRequest && !hasInlineArtifact {
		s.ctx.GetLogger().Warn("rewoosolver: called with no tool observations for investigation, returning missing_information to force replanning")
		return nil, "No tool steps were executed. Please generate a complete investigation plan with specific tool calls to gather the required data.", updatedNotebook, nil
	}

	// Detect if data quality is clearly insufficient — used only to relax the meta-talk
	// rejection so the solver can suggest alternative commands when tools have failed.
	// We check if failures outnumber successes from the data_quality tag counts.
	hasInsufficientData := hasToolFailureMajority(scratchpad)

	taskContextStr := ""
	if len(taskContext) > 0 {
		taskContextStr = taskContext[0]
	}

	fullInputs := map[string]any{
		"input":         input,
		"scratchpad":    scratchpad,
		"notebook":      notebook,
		"question_type": lo.Ternary(s.isInvestigationRequest, "investigation", "query"),
		"task_context":  taskContextStr,
	}
	fullInputs["today"] = time.Now().Format("January 02, 2006")
	fullInputs["conversation_context"] = s.request.ConversationContext
	fullInputs["history"] = "" // Assuming no history for now
	fullInputs["tool_names"] = reActPromptToolNames(s.nbAgent.GetSupportedTools(s.ctx))

	prompt, err := s.prompt.FormatPrompt(fullInputs)
	if err != nil {
		return nil, "", "", err
	}

	mcList := []llms.MessageContent{}
	for _, msg := range prompt.Messages() {
		mcList = append(mcList, llms.MessageContent{
			Role:  msg.GetType(),
			Parts: []llms.ContentPart{llms.TextContent{Text: msg.GetContent()}},
		})
	}

	// Attach images from the current request to the last human message
	mcList = AppendImagesToLastHumanMessage(mcList, s.request.Images)

	var result *llms.ContentResponse
	// improve loop, if system is not generating correct response or formatting issue then retry with additional error information
	for i := range 3 {
		s.ctx.GetLogger().Debug("rewoosolver: sending llm request", "attempt", i+1, "messages", mcList)
		// Use a role-qualified agent name so the cache key is distinct from the planner and critiquer.
		// Passing a non-UUID string causes GenerateAndTrackLLMContent to use it directly as
		// agentName, producing key "accountId:conversationId:k8s_debug_solver:model" — stable
		// across requests and separate from the planner's and critiquer's cache entries.
		solverAgentId := s.nbAgent.GetName() + "_solver"
		result, err = GenerateAndTrackLLMContent(s.ctx, s.request.UserId, s.request.AccountId, s.request.ConversationId, s.request.MessageId, solverAgentId, false, mcList, true,
			llms.WithTemperature(0.0))

		if err != nil {
			s.ctx.GetLogger().Error("rewoosolver: unable to process llm request", "error", err)
			continue
		}
		if len(result.Choices) == 0 {
			s.ctx.GetLogger().Error("rewoosolver: no data found, retrying", "error", err)
			continue
		}
		s.ctx.GetLogger().Debug("rewoosolver: llm response", "response", result.Choices[0])
		finish, missingInfo, updatedNotebook, err = s.parseOutput(result.Choices[0].Content, hasInsufficientData)
		if err != nil {
			s.ctx.GetLogger().Error("rewoosolver: failed to parse output, retrying", "error", err, "data", result.Choices[0].Content)

			// add error message to llm context and retry
			// NOTE: Do NOT include the error text in the message — the model interprets
			// "could not parse solver output" as a question to explain, and goes into tutorial mode.
			// Instead, give a direct command with the exact required root tag.
			errorMsg := "Your previous response was not valid XML. You MUST output ONLY a single XML block. The root element MUST be either <final_answer> or <missing_information>. Do not explain, do not output JSON, do not output markdown. Output the XML now."
			humanMessage := llms.MessageContent{
				Role:  llms.ChatMessageTypeHuman,
				Parts: []llms.ContentPart{llms.TextContent{Text: errorMsg}},
			}
			mcList = append(mcList, humanMessage)
			continue
		}
		if missingInfo != "" {
			return nil, missingInfo, updatedNotebook, nil
		}
		return finish, "", updatedNotebook, nil
	}

	return nil, "", updatedNotebook, fmt.Errorf("solver failed after multiple retries")
}

func (s *ReWooSolver) parseOutput(output string, insufficientData bool) (*NBAgentPlannerFinishAction, string, string, error) {
	updatedNotebook := ""
	if strings.Contains(output, "<update_notebook>") {
		updatedNotebook = common.XmlExtractTagContent(output, "update_notebook")
	}

	// Robust XML extraction using regex
	xmlContent := common.XmlRegexExtract(output, regexp.MustCompile("(?s)```xml\n(.*?)\n```"))
	if xmlContent == "" {
		if strings.Contains(output, "<final_answer>") || strings.Contains(output, "<missing_information>") || strings.Contains(output, "<thought>") {
			xmlContent = output
		}
	}

	if xmlContent == "" {
		// If no XML tags found, check if it looks like a plain text final answer
		if !strings.Contains(output, "<") {
			return &NBAgentPlannerFinishAction{
				Log:  output,
				Data: output,
			}, "", updatedNotebook, nil
		}
		return nil, "", updatedNotebook, fmt.Errorf("rewoosolver: unable to find XML block in output")
	}

	// Surgically extract the root element to strip any leading <thought> tags
	// that Gemini's native thinking mode injects before the actual root element.
	if missingMatch := regexp.MustCompile(`(?s)(<missing_information>.*</missing_information>)`).FindString(xmlContent); missingMatch != "" {
		xmlContent = missingMatch
	} else if finalMatch := regexp.MustCompile(`(?s)(<final_answer>.*</final_answer>)`).FindString(xmlContent); finalMatch != "" {
		xmlContent = finalMatch
	}

	if strings.Contains(xmlContent, "<missing_information>") {
		xmlContent = common.XmlSanitize(xmlContent, "missing_information")
		var missingInfo rewooSolverMissingInfo
		err := xml.Unmarshal([]byte(xmlContent), &missingInfo)
		if err != nil {
			s.ctx.GetLogger().Error("rewoosolver: unable to unmarshal missing information XML, attempting regex fallback", "error", err.Error(), "data", xmlContent)
			extractedRequiredInfo := common.XmlExtractCDATA(common.XmlRegexExtract(xmlContent, rewooSolverRequiredInfoRegex))

			if extractedRequiredInfo != "" {
				s.ctx.GetLogger().Info("rewoosolver: successfully extracted missing information using regex fallback")
				thought := common.XmlExtractTagContent(output, "thought")
				if thought == "" {
					thought = extractedRequiredInfo
				}
				return &NBAgentPlannerFinishAction{
					Log:  thought,
					Data: extractedRequiredInfo,
				}, extractedRequiredInfo, updatedNotebook, nil
			}
			return nil, "", updatedNotebook, fmt.Errorf("could not parse missing information output even with regex fallback: %w", err)
		}

		thought := missingInfo.Thought
		if thought == "" {
			thought = missingInfo.RequiredInformation
		}
		return &NBAgentPlannerFinishAction{
			Log:  thought,
			Data: missingInfo.RequiredInformation,
		}, missingInfo.RequiredInformation, updatedNotebook, nil
	}

	if strings.Contains(xmlContent, "<final_answer>") {
		xmlContent = common.XmlSanitize(xmlContent, "final_answer")
		var finalAnswer rewooSolverFinalAnswer
		err := xml.Unmarshal([]byte(xmlContent), &finalAnswer)
		if err != nil {
			s.ctx.GetLogger().Warn("rewoosolver: unable to unmarshal final answer XML, attempting regex fallback", "error", err.Error(), "data", xmlContent)

			// Try common tag content extraction first (it handles CDATA and sanitization internally)
			extractedContent := common.XmlExtractTagContent(output, "content")
			extractedThought := common.XmlExtractTagContent(output, "thought")

			// If specific tag extraction fails, try the raw content regex
			if extractedContent == "" {
				extractedContent = common.XmlExtractCDATA(common.XmlRegexExtract(xmlContent, rewooSolverContentRegex))
			}

			if extractedContent != "" {
				s.ctx.GetLogger().Info("rewoosolver: successfully extracted final answer content using regex fallback")
				thought := extractedThought
				if thought == "" {
					thought = extractedContent
				}
				return &NBAgentPlannerFinishAction{
						Log:  thought,
						Data: extractedContent,
					},
					"", updatedNotebook, nil
			}
			return nil, "", updatedNotebook, fmt.Errorf("could not parse final answer output even with regex fallback: %w", err)
		}

		if finalAnswer.Content == "" {
			if finalAnswer.Thought != "" {
				s.ctx.GetLogger().Warn("rewoosolver: content empty, using thought as answer")
				finalAnswer.Content = finalAnswer.Thought
			} else {
				// Fallback: Regex extraction as a safety net
				extractedContent := common.XmlExtractCDATA(common.XmlRegexExtract(xmlContent, rewooSolverContentRegex))
				if extractedContent != "" {
					finalAnswer.Content = extractedContent
				} else {
					s.ctx.GetLogger().Warn("rewoosolver: content completely empty, returning raw output")
					finalAnswer.Content = output
				}
			}
		}

		// CRITICAL: Reject "meta-talk" (instructions to user) as a final answer
		// Skip this check when data quality is insufficient — the LLM is allowed to suggest
		// alternative commands since the system couldn't gather the data itself.
		if s.isInvestigationRequest && !insufficientData {
			contentLower := strings.ToLower(finalAnswer.Content)
			forbiddenPatterns := []string{
				"use the `kubectl` tool",
				"use the kubectl tool",
				"please run",
				"you should run",
				"execute the following command",
			}
			for _, pattern := range forbiddenPatterns {
				if strings.Contains(contentLower, pattern) {
					s.ctx.GetLogger().Warn("rewoosolver: rejected meta-talk in final answer", "pattern", pattern)
					return nil, "", updatedNotebook, fmt.Errorf("your answer consists of instructions for the user. You MUST perform these actions yourself using tools and provide the RESULTS instead of telling the user what to do")
				}
			}
		}

		thought := finalAnswer.Thought
		if thought == "" {
			thought = finalAnswer.Content
		}

		return &NBAgentPlannerFinishAction{
				Log:  thought,
				Data: finalAnswer.Content,
			},
			"", updatedNotebook, nil
	}

	return nil, "", updatedNotebook, fmt.Errorf("could not parse solver output: no <final_answer> or <missing_information> tag found")
}

func createSolverPrompt(request NBAgentRequest, nbAgent NBAgent, accountId string, agentPrompt string) prompts.ChatPromptTemplate {
	fullPrompt := nbprompts.GetPrompt(context.Background(), nbprompts.PromptRewooSolver, accountId)
	if fullPrompt == "" {
		fullPrompt = prompts_repo.GetPrompt(prompts_repo.PromptPlannerRewooSolver)
	}
	parts := strings.Split(fullPrompt, "## Input")

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
				"notebook",
				"tool_names",
				"tool_descriptions",
				"question_type",
				"workspace_enabled",
				"shell_tool_enabled",
			},
		),
	}

	tmpl := prompts.NewChatPromptTemplate(messageFormatters)

	// Filter and inject tools for the solver as well
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

// hasToolFailureMajority checks the data_quality tag in the scratchpad to determine
// if tool failures clearly dominate successes. This is a conservative heuristic used
// only for code-level decisions (relaxing meta-talk ban, skipping critique).
// The actual data sufficiency judgment for the answer is left to the LLM.
func hasToolFailureMajority(scratchpad string) bool {
	// Extract counts from <data_quality failed="N" empty="N" success="N" total="N">
	idx := strings.Index(scratchpad, "<data_quality ")
	if idx == -1 {
		return false
	}
	tag := scratchpad[idx:]
	endIdx := strings.Index(tag, ">")
	if endIdx == -1 {
		return false
	}
	tag = tag[:endIdx]

	extractAttr := func(attr string) int {
		prefix := attr + `="`
		start := strings.Index(tag, prefix)
		if start == -1 {
			return 0
		}
		start += len(prefix)
		end := strings.Index(tag[start:], `"`)
		if end == -1 {
			return 0
		}
		val := 0
		for _, c := range tag[start : start+end] {
			if c >= '0' && c <= '9' {
				val = val*10 + int(c-'0')
			}
		}
		return val
	}

	failed := extractAttr("failed")
	empty := extractAttr("empty")
	success := extractAttr("success")

	// Conservative: only true when there is no usable data, or no-data results outnumber successes.
	noData := failed + empty
	return (noData+success) > 0 && (success == 0 || noData > success)
}
