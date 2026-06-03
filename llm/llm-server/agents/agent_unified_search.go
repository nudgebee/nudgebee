package agents

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/tmc/langchaingo/llms"
)

const WebSearchAgentName = "websearch"

// minCrawlWordCount is the minimum number of words (each ≥3 chars) required for
// crawled content to be considered usable. Pages with fewer words are typically
// bot-block challenges, login walls, or empty error pages.
const minCrawlWordCount = 40

// blockedPagePhrases covers three categories of low-quality crawl responses:
//   - Bot-detection / CAPTCHA challenges (Cloudflare, DDoS-Guard, etc.)
//   - Login/paywall gates that return HTTP 200 with a sign-in prompt
//   - Generic error pages (404/410/500 soft-pages served as 200)
var blockedPagePhrases = []string{
	"enable javascript", "checking your browser", "just a moment",
	"ddos protection", "verify you are human", "captcha",
	"bot detection", "please wait while", "attention required",
	"sign in to continue", "log in to continue", "sign in to read",
	"subscribe to read", "subscribe to continue", "members only",
	"create an account to view",
	"page not found", "this page doesn't exist", "this page does not exist",
	"no longer available", "something went wrong",
}

// isCrawlContentUsable returns false when the crawled page is a bot-block
// challenge, a login/paywall gate, a generic error page, or simply too short
// to be useful. It tries to unwrap a JSON {"content":"..."} envelope first
// (the format returned by CrawlExecuteTool), then falls back to the raw string.
func isCrawlContentUsable(data string) bool {
	var parsed struct {
		Content string `json:"content"`
	}
	content := data
	if err := json.NewDecoder(strings.NewReader(data)).Decode(&parsed); err == nil && parsed.Content != "" {
		content = parsed.Content
	}

	wordCount := 0
	for _, w := range strings.Fields(content) {
		if len(w) >= 3 {
			wordCount++
		}
	}
	if wordCount < minCrawlWordCount {
		return false
	}

	trimmed := strings.TrimSpace(content)
	scanContent := trimmed
	count := 0
	for i := range trimmed {
		if count == 500 {
			scanContent = trimmed[:i]
			break
		}
		count++
	}
	lower := strings.ToLower(scanContent)
	for _, phrase := range blockedPagePhrases {
		if strings.Contains(lower, phrase) {
			return false
		}
	}
	return true
}

func init() {
	toolDescription := `A unified search agent that searches across internal docs, skills, and the web in parallel.`
	toolInput := "A natural language query."
	toolOutput := "A comprehensive answer based on search results."

	core.RegisterNBAgentFactoryAndTool(WebSearchAgentName, func(accountId string) (core.NBAgent, error) {
		return newUnifiedSearchAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

func newUnifiedSearchAgent(accountId string) UnifiedSearchAgent {
	return UnifiedSearchAgent{
		accountId: accountId,
	}
}

type UnifiedSearchAgent struct {
	accountId string
}

func (a UnifiedSearchAgent) GetName() string {
	return WebSearchAgentName
}

func (a UnifiedSearchAgent) GetNameAliases() []string {
	return []string{"Unified Search", "Search Agent"}
}

func (a UnifiedSearchAgent) GetDescription() string {
	return `Searches across internal documentation, skills, and the web to answer user queries.`
}

func (a UnifiedSearchAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	// Use registry to ensure we get configured versions of these tools
	toolsList := []toolcore.NBTool{}
	if t, ok := toolcore.GetNBTool(a.accountId, tools.ToolExecuteSearchCommand); ok {
		toolsList = append(toolsList, t)
	}
	if t, ok := toolcore.GetNBTool(a.accountId, tools.SearchDocsToolName); ok {
		toolsList = append(toolsList, t)
	}
	if t, ok := toolcore.GetNBTool(a.accountId, tools.ToolExecuteCrawlCommand); ok {
		toolsList = append(toolsList, t)
	}
	return toolsList
}

func (a UnifiedSearchAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	// Not used directly as we implement Execute manually, but good to have
	return core.NBAgentPrompt{
		Role: "Unified Search Assistant",
	}
}

func (a UnifiedSearchAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

func (a UnifiedSearchAgent) GetModelCategory() core.ModelTier {
	return core.ModelTierRetrieval
}

func (a UnifiedSearchAgent) GetSummaryToolName() string {
	return core.ToolLlm
}

type SearchAnalysis struct {
	WebQuery    string `json:"web_query"`
	DocsQuery   string `json:"docs_query"`
	SkillsQuery string `json:"skills_query"`
	UseWeb      bool   `json:"use_web"`
	UseDocs     bool   `json:"use_docs"`
	UseSkills   bool   `json:"use_skills"`
	IsURL       bool   `json:"is_url"`
	TargetURL   string `json:"target_url"`
}

func (a UnifiedSearchAgent) Execute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	core.GetConversationDao().UpdateConversationMessageAsync(request.MessageId, "Analyzing unified search strategy...", core.ConversationStatusInProgress)

	// 1. Analyze the query to determine search strategy
	analysis, err := a.analyzeQuery(ctx, request)
	if err != nil {
		ctx.GetLogger().Warn("unified_search: analysis failed, falling back to full search", "error", err)
		analysis = SearchAnalysis{
			WebQuery:    request.Query,
			DocsQuery:   request.Query,
			SkillsQuery: request.Query,
			UseWeb:      true,
			UseDocs:     true,
			UseSkills:   true,
		}
	}

	// 2. Execute searches in parallel
	var wg sync.WaitGroup
	results := make([]string, 3)
	errors := make([]error, 0)
	invocations := make([]core.ToolInvocation, 0)
	var mu sync.Mutex

	if analysis.UseDocs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			core.GetConversationDao().UpdateConversationMessageAsync(request.MessageId, "Searching internal documentation...", core.ConversationStatusInProgress)
			tool, ok := toolcore.GetNBTool(request.AccountId, tools.SearchDocsToolName)
			if !ok {
				mu.Lock()
				errors = append(errors, fmt.Errorf("docs search tool not found"))
				mu.Unlock()
				return
			}
			toolCtx := toolcore.NewNbToolContext(ctx, tool, request.AccountId, request.UserId, request.ConversationId, request.MessageId, request.AgentId, analysis.DocsQuery, nil, request.QueryContext, request.QueryConfig, "")
			resp, err := tool.Call(toolCtx, toolcore.NBToolCallRequest{Command: analysis.DocsQuery})

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errors = append(errors, fmt.Errorf("docs search failed: %w", err))
			} else if resp.Data != "" {
				invocations = append(invocations, core.ToolInvocation{
					Call:     llms.ToolCall{FunctionCall: &llms.FunctionCall{Name: "docs", Arguments: fmt.Sprintf(`{"command": %q}`, analysis.DocsQuery)}},
					Response: llms.ToolCallResponse{Content: resp.Data},
				})
				results[0] = fmt.Sprintf("## Internal Documentation:\n%s", resp.Data)
			}
		}()
	}

	if analysis.UseSkills {
		wg.Add(1)
		go func() {
			defer wg.Done()
			core.GetConversationDao().UpdateConversationMessageAsync(request.MessageId, "Retrieving agent skills...", core.ConversationStatusInProgress)
			res, err := a.searchSkills(ctx, request, analysis.SkillsQuery)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errors = append(errors, fmt.Errorf("skills search failed: %w", err))
			} else if res != "" {
				results[1] = fmt.Sprintf("## Agent Skills:\n%s", res)
			}
		}()
	}

	if analysis.UseWeb || analysis.IsURL {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var content string
			var successUrl string
			var searchErr error
			llm, llmErr := core.GetLlmModel(ctx, a.GetName(), request.AccountId, request.ConversationId)
			if llmErr != nil {
				mu.Lock()
				errors = append(errors, fmt.Errorf("web branch failed: unable to get LLM model: %w", llmErr))
				mu.Unlock()
				return
			}

			if analysis.IsURL {
				// Direct URL — try crawling it directly
				core.GetConversationDao().UpdateConversationMessageAsync(request.MessageId, fmt.Sprintf("Crawling %s...", analysis.TargetURL), core.ConversationStatusInProgress)
				crawlTool, ok := toolcore.GetNBTool(request.AccountId, tools.ToolExecuteCrawlCommand)
				if ok {
					toolCtx := toolcore.NewNbToolContext(ctx, crawlTool, request.AccountId, request.UserId, request.ConversationId, request.MessageId, request.AgentId, analysis.TargetURL, nil, request.QueryContext, request.QueryConfig, "")
					resp, err := crawlTool.Call(toolCtx, toolcore.NBToolCallRequest{Command: analysis.TargetURL})
					if err == nil {
						mu.Lock()
						invocations = append(invocations, core.ToolInvocation{
							Call:     llms.ToolCall{FunctionCall: &llms.FunctionCall{Name: "crawl", Arguments: fmt.Sprintf(`{"url": %q}`, analysis.TargetURL)}},
							Response: llms.ToolCallResponse{Content: core.TruncateHead(resp.Data, 500)},
						})
						mu.Unlock()
						content = core.SummarizeContent(ctx, llm, resp.Data, request.AccountId, a.GetName(), request.ConversationId, request.MessageId, request.UserId)
						successUrl = analysis.TargetURL
					} else {
						ctx.GetLogger().Warn("unified_search: crawl failed for direct URL", "url", analysis.TargetURL, "error", err)
						searchErr = fmt.Errorf("crawl failed for %s: %w", analysis.TargetURL, err)
					}
				}
			} else {
				// Web search — get ranked links and try crawling them in order
				core.GetConversationDao().UpdateConversationMessageAsync(request.MessageId, "Searching the web...", core.ConversationStatusInProgress)
				tool, ok := toolcore.GetNBTool(request.AccountId, tools.ToolExecuteSearchCommand)
				if !ok {
					searchErr = fmt.Errorf("web search tool not found")
				} else {
					toolCtx := toolcore.NewNbToolContext(ctx, tool, request.AccountId, request.UserId, request.ConversationId, request.MessageId, request.AgentId, analysis.WebQuery, nil, request.QueryContext, request.QueryConfig, "")
					resp, err := tool.Call(toolCtx, toolcore.NBToolCallRequest{Command: analysis.WebQuery})
					if err != nil {
						searchErr = err
					} else {
						mu.Lock()
						invocations = append(invocations, core.ToolInvocation{
							Call:     llms.ToolCall{FunctionCall: &llms.FunctionCall{Name: "web_search", Arguments: fmt.Sprintf(`{"command": %q}`, analysis.WebQuery)}},
							Response: llms.ToolCallResponse{Content: resp.Data},
						})
						mu.Unlock()

						// Get ranked URLs and try crawling them in order until one succeeds
						const maxCrawlAttempts = 3
						rankedUrls := a.selectRankedLinks(ctx, request, resp.Data, maxCrawlAttempts)

						crawlTool, ok := toolcore.GetNBTool(request.AccountId, tools.ToolExecuteCrawlCommand)
						if !ok {
							searchErr = fmt.Errorf("crawl tool not found")
						} else {
							for _, candidateUrl := range rankedUrls {
								core.GetConversationDao().UpdateConversationMessageAsync(request.MessageId, fmt.Sprintf("Crawling %s...", candidateUrl), core.ConversationStatusInProgress)
								crawlCtx := toolcore.NewNbToolContext(ctx, crawlTool, request.AccountId, request.UserId, request.ConversationId, request.MessageId, request.AgentId, candidateUrl, nil, request.QueryContext, request.QueryConfig, "")
								crawlResp, crawlErr := crawlTool.Call(crawlCtx, toolcore.NBToolCallRequest{Command: candidateUrl})
								if crawlErr != nil {
									ctx.GetLogger().Warn("unified_search: crawl failed, trying next URL", "url", candidateUrl, "error", crawlErr)
									continue
								}
								if !isCrawlContentUsable(crawlResp.Data) {
									ctx.GetLogger().Warn("unified_search: crawl returned unusable content (bot block, login wall, error page, or too short), trying next URL", "url", candidateUrl)
									continue
								}

								mu.Lock()
								invocations = append(invocations, core.ToolInvocation{
									Call:     llms.ToolCall{FunctionCall: &llms.FunctionCall{Name: "crawl", Arguments: fmt.Sprintf(`{"url": %q}`, candidateUrl)}},
									Response: llms.ToolCallResponse{Content: core.TruncateHead(crawlResp.Data, 500)},
								})
								mu.Unlock()

								content = core.SummarizeContent(ctx, llm, crawlResp.Data, request.AccountId, a.GetName(), request.ConversationId, request.MessageId, request.UserId)
								successUrl = candidateUrl
								break
							}
							if content == "" && len(rankedUrls) > 0 {
								searchErr = fmt.Errorf("failed to crawl any of the %d candidate URLs", len(rankedUrls))
							}
						}
					}
				}
			}

			mu.Lock()
			defer mu.Unlock()
			if searchErr != nil {
				errors = append(errors, fmt.Errorf("web branch failed: %w", searchErr))
			} else if content != "" {
				results[2] = fmt.Sprintf("## Web Content (Source: %s):\n%s", successUrl, content)
			}
		}()
	}

	wg.Wait()
	if len(errors) > 0 {
		// Log partial failures for better observability.
		ctx.GetLogger().Warn("unified_search: one or more search sources failed", "errors", errors)
	}

	core.GetConversationDao().UpdateConversationMessageAsync(request.MessageId, "Synthesizing final answer...", core.ConversationStatusInProgress)

	// Filter and combine
	finalResults := make([]string, 0)
	for _, r := range results {
		if r != "" {
			finalResults = append(finalResults, r)
		}
	}

	if len(finalResults) == 0 {
		// Whether the branches failed or simply found nothing, return a clean
		// completed response instead of an error. Propagating an error here
		// caused parent agents (e.g. k8s_debug) to treat websearch as a
		// recoverable failure and start invoking unrelated tools
		// (deepwiki_mcp_ask_question, resource_search, …) — see the
		// "election results" trace where every fallback was a topic mismatch.
		if len(errors) > 0 {
			ctx.GetLogger().Warn("unified_search: all search sources failed", "errors", errors)
		}
		return core.NBAgentResponse{
			Response:          []string{"I could not find any relevant information from the configured sources. Web search may be temporarily unavailable; please try again later or rephrase the question."},
			AgentName:         a.GetName(),
			Status:            core.ConversationStatusCompleted,
			AgentStepResponse: invocations,
		}, nil
	}

	combinedContext := strings.Join(finalResults, "\n\n")
	if len(errors) > 0 {
		combinedContext = fmt.Sprintf("%s\n\n**System Note**: Some sources failed to return results: %v", combinedContext, errors)
	}

	finalAnswer, err := a.synthesizeAnswer(ctx, request, combinedContext)
	if err != nil {
		return core.NBAgentResponse{AgentStepResponse: invocations}, fmt.Errorf("failed to synthesize answer: %w", err)
	}

	// Ensure References section exists if we have a target URL (from direct URL or search)
	effectiveUrl := analysis.TargetURL
	if effectiveUrl == "" {
		// Look for URLs in the invocations if not provided directly
		for _, inv := range invocations {
			if inv.Call.FunctionCall != nil && inv.Call.FunctionCall.Name == "crawl" {
				var args map[string]interface{}
				if err := common.ExtractAndUnmarshalJSON([]byte(inv.Call.FunctionCall.Arguments), &args); err == nil {
					if url, ok := args["url"].(string); ok {
						effectiveUrl = url
						break
					}
				}
			}
		}
	}

	if effectiveUrl != "" && !strings.Contains(strings.ToLower(finalAnswer), "references") {
		finalAnswer = fmt.Sprintf("%s\n\n#### References\n- [%s](%s)", finalAnswer, effectiveUrl, effectiveUrl)
	}

	return core.NBAgentResponse{
		Response:          []string{finalAnswer},
		AgentName:         a.GetName(),
		Status:            core.ConversationStatusCompleted,
		AgentStepResponse: invocations,
	}, nil
}

// selectRankedLinks asks the LLM to rank the top URLs from search results by relevance.
// Returns up to maxLinks URLs ordered by relevance.
func (a UnifiedSearchAgent) selectRankedLinks(ctx *security.RequestContext, request core.NBAgentRequest, searchResults string, maxLinks int) []string {
	selectionPrompt := fmt.Sprintf(`Based on the user's request: "%s", pick the top %d most relevant URLs from these search results.
Return ONLY the URLs, one per line, ordered by relevance (most relevant first). No numbering, no extra text.

Search Results:
%s`, request.Query, maxLinks, searchResults)

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, selectionPrompt),
	}

	resp, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, false, messages, true)
	if err != nil || len(resp.Choices) == 0 {
		return nil
	}

	var urls []string
	for _, line := range strings.Split(resp.Choices[0].Content, "\n") {
		line = strings.TrimSpace(line)
		// Extract URL from each line (may contain numbering or extra text)
		for _, part := range strings.Fields(line) {
			cleaned := strings.Trim(part, "`\"'()[]<>")
			if strings.HasPrefix(cleaned, "http") {
				urls = append(urls, cleaned)
				break
			}
		}
	}

	if len(urls) > maxLinks {
		urls = urls[:maxLinks]
	}
	return urls
}

func (a UnifiedSearchAgent) analyzeQuery(ctx *security.RequestContext, request core.NBAgentRequest) (SearchAnalysis, error) {
	prompt := `You are an intelligent search assistant. Analyze the user's query to determine which sources to search.

Sources:
- **web**: External information, latest news, general knowledge, public docs.
- **docs**: Internal company documentation, runbooks, configuration, specific internal knowledge.
- **skills**: Capabilities of available agents, what tasks can be performed.

Guidelines:
- If the query is a direct URL, set "is_url": true, "target_url": the URL, and others to false.
- If the query is about "latest news", "weather", or clearly external, set "use_web": true.
- If the query is about "how to configure X internally", "company policy", set "use_docs": true.
- If the query is about "can you do X", "what agents are available", set "use_skills": true.
- You can enable multiple sources if the query is ambiguous.

Output JSON format:
{
  "web_query": "string",
  "docs_query": "string",
  "skills_query": "string",
  "use_web": boolean,
  "use_docs": boolean,
  "use_skills": boolean,
  "is_url": boolean,
  "target_url": "string"
}`

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, prompt),
	}

	if request.ConversationContext != "" {
		messages = append(messages, llms.TextParts(llms.ChatMessageTypeSystem, "Conversation History for Context:\n"+request.ConversationContext))
	}

	messages = append(messages, llms.TextParts(llms.ChatMessageTypeHuman, request.Query))

	resp, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, true, messages, true)
	if err != nil {
		return SearchAnalysis{}, err
	}

	if len(resp.Choices) == 0 {
		return SearchAnalysis{}, fmt.Errorf("LLM returned no choices for analysis")
	}

	var analysis SearchAnalysis
	if err := common.ExtractAndUnmarshalJSON([]byte(resp.Choices[0].Content), &analysis); err != nil {
		return SearchAnalysis{}, err
	}

	return analysis, nil
}

func (a UnifiedSearchAgent) searchSkills(ctx *security.RequestContext, request core.NBAgentRequest, query string) (string, error) {
	// Query RAG with module="skills" (assuming this module exists or will be populated)
	// If "skills" module is empty, this might return nothing, which is fine.
	// We use QueryRAGCollection to target specific module/collection if needed.
	// Here we use QueryRAG which takes module.

	results := toolcore.QueryRAG(request.UserId, request.AccountId, query, "skills", 3, request.ConversationId, request.MessageId, request.AgentId, true)

	if len(results) == 0 {
		return "", nil
	}

	var sb strings.Builder
	for _, res := range results {
		name := "Skill"
		if val, ok := res.Metadata["name"]; ok {
			name = fmt.Sprintf("%v", val)
		}
		fmt.Fprintf(&sb, "- %s: %s (Relevance: %.2f)\n", name, res.Document, res.SimilarityScore)
	}
	return sb.String(), nil
}

func (a UnifiedSearchAgent) synthesizeAnswer(ctx *security.RequestContext, request core.NBAgentRequest, context string) (string, error) {
	prompt := `You are a helpful assistant. Synthesize the provided search results to answer the user's question.

- Use the provided context from Web, Docs, and Skills.
- Note: Some context blocks (Docs and Web) are provided in JSON format containing lists of documents or links.
- If information is conflicting, prioritize Internal Documentation.
- If no relevant information is found in the context, state that clearly.
- MANDATORY: Include a "#### References" section at the end listing all sources/URLs used.
- Cite sources/URLs if available in the context (e.g., [Source](url)).
- Be concise and direct.`

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, prompt),
		llms.TextParts(llms.ChatMessageTypeSystem, fmt.Sprintf("Context:\n%s", context)),
	}

	// websearch is AgentPlannerTypeCustom and bypasses the executor's basePrompt →
	// systemMessage path, so the lazy <skill-lists> + load_skills flow that
	// ReAct/ReWoo planners use cannot reach this synthesis call. The executor
	// eagerly loads the active mapped skills (own ∪ inherited, narrowed to the
	// question-aware selection when LlmServerSkillSelectionTopK is enabled) into
	// request.SkillsContext — surface it as a system message so any expert
	// guidance the user mapped to "websearch" actually shapes the final answer.
	if strings.TrimSpace(request.SkillsContext) != "" {
		messages = append(messages, llms.TextParts(llms.ChatMessageTypeSystem, request.SkillsContext))
	}

	if request.ConversationContext != "" {
		messages = append(messages, llms.TextParts(llms.ChatMessageTypeSystem, "Conversation History for Context:\n"+request.ConversationContext))
	}

	messages = append(messages, llms.TextParts(llms.ChatMessageTypeHuman, request.Query))

	resp, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, true, messages, true)
	if err != nil {
		return "", err
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("LLM returned no choices for synthesis")
	}

	return resp.Choices[0].Content, nil
}
