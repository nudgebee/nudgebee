package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

const SearchAgentNameOld = "websearch_old"

func init() {
	// This describes the 'websearch' agent when it is used as a tool by another agent.
	toolDescription := `Searches the internet and crawls website content. Use this to find information, verify if a URL is accessible, or fetch the content of a specific page (e.g. error messages). Provide a question or URL.`
	toolInput := "Provide search question in natural language."
	toolOutput := "A markdown summary of the search response with references."

	core.RegisterNBAgentFactoryAndTool(SearchAgentNameOld, func(accountId string) (core.NBAgent, error) {
		return newSearchAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

func newSearchAgent(accountId string) SearchAgent {
	return SearchAgent{
		accountId: accountId,
	}
}

type SearchAgent struct {
	accountId string
}

func (p SearchAgent) GetName() string {
	return SearchAgentNameOld
}

func (l SearchAgent) GetNameAliases() []string {
	return []string{"Web Search", "Google Search", "Crawl"}
}

func (p SearchAgent) GetDescription() string {
	return `Searches internet or get a content of website and returns the response based on users questions`
}

func (l SearchAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{
		Role: "an expert in searching the internet and extracting information",
		Instructions: []string{
			"Identify if the user provided a URL or a search query.",
			"If a URL is given, crawl it directly.",
			"If a search query is given, search the web and pick the most relevant link.",
			"Summarize the content concisely in markdown.",
		},
	}
}

func (p SearchAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{tools.SearchExecuteTool{}, tools.CrawlExecuteTool{}}
}

func (l SearchAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

func (l SearchAgent) GetModelCategory() core.ModelTier {
	return core.ModelTierRetrieval
}

func (a SearchAgent) Execute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	// 1. Analyze Input: URL or Search Query?
	core.GetConversationDao().UpdateConversationMessageAsync(request.MessageId, "Analyzing search request...", core.ConversationStatusInProgress)

	analysisPrompt := `Analyze the user input and determine if it contains a direct URL to crawl or if it requires a web search.
Return ONLY a JSON object with:
- "type": "url" or "search"
- "value": the extracted URL or the optimized search query

User Input: {{.input}}`

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, strings.ReplaceAll(analysisPrompt, "{{.input}}", request.Query)),
	}

	llmResp, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, false, messages, true)
	if err != nil {
		return core.NBAgentResponse{}, fmt.Errorf("search: failed to analyze input: %w", err)
	}

	var analysis struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	}
	err = common.ExtractAndUnmarshalJSON([]byte(llmResp.Choices[0].Content), &analysis)
	if err != nil {
		// Fallback: assume search if parsing fails
		analysis.Type = "search"
		analysis.Value = request.Query
	}

	var targetUrl string
	var agentStepResponses []core.ToolInvocation

	if analysis.Type == "url" {
		targetUrl = analysis.Value
	} else {
		core.GetConversationDao().UpdateConversationMessageAsync(request.MessageId, "Searching for relevant links...", core.ConversationStatusInProgress)
		// 2. Search for Links
		searchTool := tools.SearchExecuteTool{}
		toolCtx := toolcore.NewNbToolContext(ctx, searchTool, request.AccountId, request.UserId, request.ConversationId, request.MessageId, request.AgentId, analysis.Value, nil, request.QueryContext, request.QueryConfig, "")

		searchResp, err := searchTool.Call(toolCtx, toolcore.NBToolCallRequest{Command: analysis.Value})
		if err != nil {
			return core.NBAgentResponse{}, fmt.Errorf("search: search failed: %w", err)
		}

		agentStepResponses = append(agentStepResponses, core.ToolInvocation{
			Call:     llms.ToolCall{Type: "function", FunctionCall: &llms.FunctionCall{Name: searchTool.Name(), Arguments: analysis.Value}},
			Response: llms.ToolCallResponse{Name: searchTool.Name(), Content: searchResp.Data},
		})

		// 3. Select Best Link
		selectionPrompt := fmt.Sprintf(`Based on the user's request: "%s", pick the SINGLE most relevant URL from these search results.
Return ONLY the URL as a plain string.

Search Results:
<search_results>
%s
</search_results>`, request.Query, searchResp.Data)

		selectResp, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, false, []llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeHuman, selectionPrompt),
		}, true)
		if err != nil || len(selectResp.Choices) == 0 {
			return core.NBAgentResponse{}, fmt.Errorf("search: failed to select link")
		}

		// Robust extraction to handle preamble or markdown
		content := selectResp.Choices[0].Content
		parts := strings.Fields(content)
		for _, p := range parts {
			if strings.HasPrefix(p, "http") {
				targetUrl = strings.Trim(p, "`\"'()")
				break
			}
		}
		if targetUrl == "" {
			targetUrl = strings.TrimSpace(selectResp.Choices[0].Content)
		}
	}

	// 4. Crawl the URL
	core.GetConversationDao().UpdateConversationMessageAsync(request.MessageId, "Crawling website content...", core.ConversationStatusInProgress)
	crawlTool := tools.CrawlExecuteTool{}
	crawlToolCtx := toolcore.NewNbToolContext(ctx, crawlTool, request.AccountId, request.UserId, request.ConversationId, request.MessageId, request.AgentId, targetUrl, nil, request.QueryContext, request.QueryConfig, "")

	crawlResp, err := crawlTool.Call(crawlToolCtx, toolcore.NBToolCallRequest{Command: targetUrl})
	if err != nil {
		return core.NBAgentResponse{}, fmt.Errorf("search: crawl failed for %s: %w", targetUrl, err)
	}

	agentStepResponses = append(agentStepResponses, core.ToolInvocation{
		Call:     llms.ToolCall{Type: "function", FunctionCall: &llms.FunctionCall{Name: crawlTool.Name(), Arguments: targetUrl}},
		Response: llms.ToolCallResponse{Name: crawlTool.Name(), Content: crawlResp.Data},
	})

	if crawlResp.Data == "" {
		return core.NBAgentResponse{
			Response:          []string{"I was able to reach the website, but it returned no readable content. The page might be protected or requires JavaScript that failed to render."},
			AgentName:         a.GetName(),
			Status:            core.ConversationStatusCompleted,
			AgentStepResponse: agentStepResponses,
		}, nil
	}

	// 5. Intelligent Summarization (Parallel if large)
	core.GetConversationDao().UpdateConversationMessageAsync(request.MessageId, "Summarizing content...", core.ConversationStatusInProgress)
	fullLlm, err := core.GetLlmModel(ctx, a.GetName(), request.AccountId, request.ConversationId)
	if err != nil {
		fullLlm, _ = core.GetLlmModel(ctx, "llm", request.AccountId, request.ConversationId)
	}

	// We use the exported SummarizeContent which handles chunking and parallelization internally
	summary := core.SummarizeContent(ctx, fullLlm, crawlResp.Data, request.AccountId, a.GetName(), request.ConversationId, request.MessageId, request.UserId)

	if summary == "" {
		summary = "I successfully retrieved the content but was unable to generate a summary. Here is a snippet of the raw data:\n\n" + core.TruncateHead(crawlResp.Data, 1000)
	}

	finalMarkdown := fmt.Sprintf("%s\n\n#### References\n- [%s](%s)", summary, targetUrl, targetUrl)

	if finalMarkdown == "" {
		finalMarkdown = "Search completed, but no summary could be generated."
	}

	return core.NBAgentResponse{
		Response:          []string{finalMarkdown},
		AgentName:         a.GetName(),
		Status:            core.ConversationStatusCompleted,
		AgentStepResponse: agentStepResponses,
	}, nil
}
