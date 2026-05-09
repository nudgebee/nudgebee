package agents

import (
	"encoding/json"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"sort"
	"strings"
	"sync"

	"github.com/tmc/langchaingo/llms"
)

const ResourceSearchAgentName = "resource_search"

func init() {
	// This describes the 'resource_search' agent when it is used as a tool by another agent.
	toolDescription := `Searches for resources across multiple platforms (Kubernetes, AWS, GCP, Azure, Datadog, GitHub, ArgoCD) using fuzzy matching and provides suggestions for resource discovery.`
	toolInput := "Provide a natural language query to search for resources across infrastructure, monitoring, and code repositories."
	toolOutput := "A JSON object containing found resources across platforms, labeled by source and account."

	core.RegisterNBAgentFactoryAndTool(ResourceSearchAgentName, func(accountId string) (core.NBAgent, error) {
		return newResourceSearchAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

func newResourceSearchAgent(accountId string) ResourceSearchAgent {
	return ResourceSearchAgent{
		accountId: accountId,
	}
}

type ResourceSearchAgent struct {
	accountId string
}

func (a ResourceSearchAgent) GetName() string {
	return ResourceSearchAgentName
}

func (a ResourceSearchAgent) GetNameAliases() []string {
	return []string{"Resource Search", "Global Resource Search", "Cross-Platform Search"}
}

func (a ResourceSearchAgent) GetDescription() string {
	return `Searches for resources across Kubernetes, Cloud (AWS/GCP/Azure), Monitoring (Datadog), Code (GitHub), and CI/CD (ArgoCD).`
}

func (a ResourceSearchAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	toolsList := []toolcore.NBTool{}

	// K8s Search
	if tool, ok := toolcore.GetNBTool(a.accountId, tools.ToolResourceSearch); ok {
		toolsList = append(toolsList, tool)
	}

	// Cloud Search (DB-backed)
	if tool, ok := toolcore.GetNBTool(a.accountId, tools.ToolCloudResourceSearch); ok {
		toolsList = append(toolsList, tool)
	}

	// Datadog
	if tools.HasDatadogIntegration(a.accountId) {
		if tool, ok := toolcore.GetNBTool(a.accountId, tools.ToolDatadogResourceSearchExecute); ok {
			toolsList = append(toolsList, tool)
		}
	}

	// GitHub
	if tool, ok := toolcore.GetNBTool(a.accountId, tools.ToolExecuteGithubCliCommand); ok {
		toolsList = append(toolsList, tool)
	}

	// ArgoCD
	if tool, ok := toolcore.GetNBTool(a.accountId, tools.ToolExecuteArgoCDCommand); ok {
		toolsList = append(toolsList, tool)
	}

	return toolsList
}

func (a ResourceSearchAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	// Instructions for Global Discovery
	instructions := []string{
		"**Primary Role:** You are a Global Resource Discovery assistant.",
		"**Goal:** Translate user queries into precise tool calls to search for resources across Kubernetes, Cloud, Datadog, GitHub, and ArgoCD.",
		"**Strategy:** Always search across all relevant platforms unless specified otherwise.",
		"**Tools Usage:**",
		"   - `resource_search_execute`: For Kubernetes resources (pods, services, deployments).",
		"   - `cloud_resource_search_execute`: For AWS/GCP/Azure resources (instances, buckets, databases) indexed in the DB.",
		"   - `datadog_resource_search_execute`: For monitoring resources (services, containers).",
		"   - `github_execute`: To find source code repositories.",
		"   - `argocd_execute`: To identify CI/CD applications and sync status.",
	}

	constraints := []string{
		"You MUST output tool calls in a single JSON array.",
		"Label each search clearly in your internal planning.",
		"If searching for a service name, try to find it in K8s, Cloud, and Datadog in parallel.",
		"If you find a K8s workload, also try to find its corresponding GitHub repo or ArgoCD app.",
	}

	toolUsage := map[string][]string{
		tools.ToolResourceSearch: {
			"Search Kubernetes resources.",
		},
		tools.ToolCloudResourceSearch: {
			"Search AWS/GCP/Azure resources in DB.",
		},
		tools.ToolDatadogResourceSearchExecute: {
			"Search Datadog services/containers.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "Global Resource Search Assistant",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
	}
}

func (a ResourceSearchAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

func (a ResourceSearchAgent) Execute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	// 1. Construct System Prompt for Tool Call Generation
	systemPrompt := `You are an intelligent assistant that generates tool calls to search for resources across multiple platforms.

Available Tools:
1. resource_search_execute: Search for Kubernetes pods, deployments, etc.
   - Input: {"resource_name": "...", "search_type": "suggestions"}
2. cloud_resource_search_execute: Search for AWS/GCP/Azure instances, buckets, etc.
   - Input: {"resource_name": "..."}
3. datadog_resource_search_execute: Search for Datadog monitoring entities.
   - Input: {"resource_type": "services", "query": "...", "search_type": "datadog"}

Strategy:
- Generate calls for K8s, Cloud, and Datadog (if relevant).
- RETURN ONLY A JSON ARRAY OF TOOL CALLS.
`

	// 2. Call LLM to generate base tool calls
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
	}
	// resource_search is an AgentPlannerTypeCustom agent and bypasses the
	// executor's basePrompt → systemMessage path, so the lazy <skill-lists> +
	// load_skills flow used by ReAct/ReWoo planners cannot reach this LLM call.
	// The executor eagerly loads the active mapped skills (own ∪ inherited,
	// narrowed to the question-aware selection when LlmServerSkillSelectionTopK
	// is enabled) into request.SkillsContext for us — prepend it as a system
	// message so any expert guidance the user mapped to resource_search (or to a
	// custom-planner ancestor like logs_default) shapes the resource lookup.
	if strings.TrimSpace(request.SkillsContext) != "" {
		messages = append(messages, llms.TextParts(llms.ChatMessageTypeSystem, request.SkillsContext))
	}
	if request.ConversationContext != "" {
		messages = append(messages, llms.TextParts(llms.ChatMessageTypeSystem, fmt.Sprintf("Conversation Context:\n%s", request.ConversationContext)))
	}
	if request.QueryContext != "" {
		messages = append(messages, llms.TextParts(llms.ChatMessageTypeSystem, fmt.Sprintf("Query Context:\n%s", request.QueryContext)))
	}
	messages = append(messages, llms.TextParts(llms.ChatMessageTypeHuman, request.Query))

	llmResp, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, true, messages, true)
	if err != nil {
		return core.NBAgentResponse{}, err
	}

	type ToolCall struct {
		Tool       string          `json:"tool"`
		ToolCode   string          `json:"tool_code"`
		ToolName   string          `json:"tool_name"`
		Input      json.RawMessage `json:"input"`
		Args       json.RawMessage `json:"args"`
		Parameters json.RawMessage `json:"parameters"`
		Arguments  json.RawMessage `json:"arguments"`
	}
	var toolCalls []ToolCall

	// Robust Extraction: Handle Native Tool Calls AND JSON Content
	respContent := llmResp.Choices[0].Content
	ctx.GetLogger().Info("resource_search: llm output received", "content", respContent)

	// 1. First check for native tool calls
	for _, tc := range llmResp.Choices[0].ToolCalls {
		argBytes, _ := json.Marshal(tc.FunctionCall.Arguments)
		toolCalls = append(toolCalls, ToolCall{
			Tool:  tc.FunctionCall.Name,
			Input: json.RawMessage(argBytes),
		})
	}

	// 2. If no native calls, extract from JSON content
	if len(toolCalls) == 0 {
		var rawToolCalls []ToolCall
		if err := common.ExtractAndUnmarshalJSON([]byte(respContent), &rawToolCalls); err != nil || len(rawToolCalls) == 0 {
			_ = json.Unmarshal([]byte(respContent), &rawToolCalls)
		}

		for _, rtc := range rawToolCalls {
			tc := rtc
			if tc.Tool == "" && tc.ToolCode != "" {
				tc.Tool = tc.ToolCode
			}
			if tc.Tool == "" && tc.ToolName != "" {
				tc.Tool = tc.ToolName
			}

			// Priority order for input payload
			if len(tc.Input) == 0 || string(tc.Input) == "null" {
				if len(tc.Parameters) > 0 && string(tc.Parameters) != "null" {
					tc.Input = tc.Parameters
				} else if len(tc.Args) > 0 && string(tc.Args) != "null" {
					tc.Input = tc.Args
				} else if len(tc.Arguments) > 0 && string(tc.Arguments) != "null" {
					tc.Input = tc.Arguments
				}
			}

			if tc.Tool != "" {
				toolCalls = append(toolCalls, tc)
			}
		}
	}

	if len(toolCalls) == 0 {
		ctx.GetLogger().Info("resource_search: falling back to manual tool construction", "query", request.Query)

		// 1. Better Heuristic Extraction
		searchTerms := strings.ToLower(request.Query)
		fillers := []string{"find", "all", "search", "for", "instances", "across", "my", "cluster", "and", "cloud", "accounts"}
		for _, f := range fillers {
			searchTerms = strings.ReplaceAll(searchTerms, f, "")
		}
		words := strings.Fields(searchTerms)
		if len(words) > 0 {
			resourceName := words[0] // Grab first meaningful word

			// 2. Alias Expansion (e.g., postgres -> postgresql)
			variations := []string{resourceName}
			if resourceName == "postgres" {
				variations = append(variations, "postgresql")
			}

			for _, v := range variations {
				toolCalls = append(toolCalls, ToolCall{Tool: tools.ToolResourceSearch, Input: json.RawMessage(fmt.Sprintf(`{"resource_name": "%s", "search_type": "suggestions"}`, v))})
				toolCalls = append(toolCalls, ToolCall{Tool: tools.ToolCloudResourceSearch, Input: json.RawMessage(fmt.Sprintf(`{"resource_name": "%s"}`, v))})
			}
		}
	}

	// 3. Execute Tools in Parallel
	type ToolResult struct {
		ToolName  string
		AccountID string
		Input     string
		Response  string
		Err       error
	}
	// Use a large enough buffer to prevent deadlocks during parallel bursts
	resultsChan := make(chan ToolResult, 100)
	var wg sync.WaitGroup

	supportedTools := a.GetSupportedTools(ctx)
	isSupported := func(tName string) bool {
		for _, st := range supportedTools {
			if st.Name() == tName {
				return true
			}
		}
		return false
	}

	for _, call := range toolCalls {
		if call.Tool == "" {
			continue
		}

		wg.Add(1)
		go func(tc ToolCall) {
			defer wg.Done()

			// Normalize tool name
			tName := tc.Tool
			if !strings.HasSuffix(tName, "_execute") && tName != "github_execute" && tName != "argocd_execute" {
				tName = tName + "_execute"
			}

			// Security Validation: Only execute supported tools
			if !isSupported(tName) {
				ctx.GetLogger().Warn("resource_search: rejecting unsupported tool call", "tool", tName)
				return
			}

			ctx.GetLogger().Info("resource_search: attempting tool call", "tool", tName, "input", string(tc.Input))

			toolInstance, found := toolcore.GetNBTool(a.accountId, tName)
			if !found {
				ctx.GetLogger().Warn("resource_search: tool not found in registry", "tool", tName)
				return
			}

			inputStr := string(tc.Input)
			toolCtx := toolcore.NewNbToolContext(ctx, toolInstance, a.accountId, request.UserId, request.ConversationId, request.MessageId, request.AgentId, inputStr, nil, request.QueryContext, request.QueryConfig, "")
			resp, err := toolInstance.Call(toolCtx, toolcore.NBToolCallRequest{Command: inputStr})

			if err != nil {
				ctx.GetLogger().Error("resource_search: tool call failed", "tool", tName, "error", err)
			} else {
				ctx.GetLogger().Info("resource_search: tool call successful", "tool", tName, "response_len", len(resp.Data))
			}

			resultsChan <- ToolResult{ToolName: tName, AccountID: a.accountId, Input: inputStr, Response: resp.Data, Err: err}
		}(call)
	}

	// Wait for all tools to complete
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// 4. Aggregate & Prioritize Results
	var agentStepResponses []core.ToolInvocation

	type DiscoveryResult struct {
		Priority int
		Label    string
		Block    string
		Content  string
	}
	var allResults []DiscoveryResult
	seenKeys := make(map[string]bool)

	// Build a set of meaningful search terms from the original query so that we can
	// validate tool results before presenting them. This is the last line of defence
	// against tools returning unrelated resources (e.g. when a grep pipe fails on the
	// relay and all resources of a type are returned).
	queryTerms := extractResourceQueryTerms(request.Query)

	// 4a. Collect K8s resources for batch enrichment
	var k8sResults []DiscoveryResult
	var k8sEnrichmentOptions []services_server.SourceCodeAnnotationOptions

	for res := range resultsChan {
		// Always record the invocation (even on error) so callers can inspect what was tried.
		agentStepResponses = append(agentStepResponses, core.ToolInvocation{
			Call: llms.ToolCall{
				Type:         "function",
				FunctionCall: &llms.FunctionCall{Name: res.ToolName, Arguments: res.Input},
			},
			Response: llms.ToolCallResponse{Name: res.ToolName, Content: res.Response},
		})

		if res.Err != nil || res.Response == "" {
			continue
		}

		if res.ToolName == tools.ToolResourceSearch {
			var k8sResp tools.K8sResourceSearchResponse
			if err := common.UnmarshalJson([]byte(res.Response), &k8sResp); err == nil && len(k8sResp.Resources) > 0 {
				for _, r := range k8sResp.Resources {
					// Skip resources that don't relate to any meaningful query term.
					if len(queryTerms) > 0 && !resourceNameMatchesTerms(r.Name, queryTerms) {
						ctx.GetLogger().Debug("resource_search: skipping irrelevant k8s result", "name", r.Name, "query_terms", queryTerms)
						continue
					}

					key := fmt.Sprintf("k8s:%s:%s", strings.ToLower(r.Type), strings.ToLower(r.Name))
					if seenKeys[key] {
						continue
					}
					seenKeys[key] = true

					// Priority Logic: Workloads > Services > Storage > Metadata
					p := 50
					switch strings.ToLower(r.Type) {
					case "pod", "deployment", "statefulset", "daemonset", "workload":
						p = 100
					case "service", "ingress":
						p = 80
					case "persistentvolume", "pvc":
						p = 70
					}

					k8sResults = append(k8sResults, DiscoveryResult{
						Priority: p,
						Label:    fmt.Sprintf("- Found %s **%s** in Kubernetes (ns: %s).", r.Type, r.Name, r.Namespace),
						Block:    fmt.Sprintf("[VERIFIED_DISCOVERY]\nSource: Kubernetes\nType: %s\nName: %s\nNamespace: %s\nStatus: %s\n", r.Type, r.Name, r.Namespace, r.Status),
					})
					k8sEnrichmentOptions = append(k8sEnrichmentOptions, services_server.SourceCodeAnnotationOptions{
						WorkloadName: r.Name,
						Namespace:    r.Namespace,
					})
				}
			}
		} else if res.ToolName == tools.ToolCloudResourceSearch {
			// 4b. Parse Cloud Results
			var cloudResp tools.CloudResourceSearchResponse
			if err := common.UnmarshalJson([]byte(res.Response), &cloudResp); err == nil && len(cloudResp.Resources) > 0 {
				for _, r := range cloudResp.Resources {
					if strings.ToLower(r.Service) == "kubernetes" {
						continue
					}
					// Skip cloud resources that don't match any meaningful query term.
					if len(queryTerms) > 0 && !resourceNameMatchesTerms(r.Name, queryTerms) {
						ctx.GetLogger().Debug("resource_search: skipping irrelevant cloud result", "name", r.Name, "query_terms", queryTerms)
						continue
					}

					key := fmt.Sprintf("cloud:%s:%s", strings.ToLower(r.Type), strings.ToLower(r.Name))
					if seenKeys[key] {
						continue
					}
					seenKeys[key] = true

					// Priority Logic: DB > Compute > Logs/Artifacts
					p := 40
					lowerSvc := strings.ToLower(r.Service)
					if strings.Contains(lowerSvc, "rds") || strings.Contains(lowerSvc, "sql") || strings.Contains(lowerSvc, "db") {
						p = 95
					} else if strings.Contains(lowerSvc, "ec2") || strings.Contains(lowerSvc, "instance") {
						p = 90
					}

					block := fmt.Sprintf("- Found %s **%s** in Cloud (%s).\n", r.Type, r.Name, r.Service)
					block += "[VERIFIED_DISCOVERY]\n"
					block += fmt.Sprintf("Source: Cloud\nAccount: %s\nType: %s\nName: %s\nService: %s\nRegion: %s\n", r.AccountID, r.Type, r.Name, r.Service, r.Region)

					allResults = append(allResults, DiscoveryResult{
						Priority: p,
						Content:  block,
					})
				}
			}
		} else {
			// 4c. Other tools
			allResults = append(allResults, DiscoveryResult{
				Priority: 30,
				Content:  fmt.Sprintf("[%s Result]\n%s\n", res.ToolName, res.Response),
			})
		}
	}

	// 4d. Batch Enrich K8s Results
	if len(k8sEnrichmentOptions) > 0 {
		enrichments, _ := services_server.BatchGetSourceCodeRepo(ctx, a.accountId, k8sEnrichmentOptions)
		for i, res := range k8sResults {
			opt := k8sEnrichmentOptions[i]
			key := fmt.Sprintf("%s/%s", opt.Namespace, opt.WorkloadName)
			if enrichment, ok := enrichments[key]; ok {
				if enrichment.CodeRepo != "" {
					res.Block += fmt.Sprintf("CodeRepo: %s\n", enrichment.CodeRepo)
				}
				if enrichment.ArgoCDApp != "" {
					res.Block += fmt.Sprintf("ArgoCDApp: %s (Status: %s)\n", enrichment.ArgoCDApp, enrichment.SyncStatus)
				}
			}
			allResults = append(allResults, DiscoveryResult{
				Priority: res.Priority,
				Content:  res.Label + "\n" + res.Block,
			})
		}
	}

	// 5. Final Sort & Joined Response
	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].Priority > allResults[j].Priority
	})

	var finalResponseContent string
	if len(allResults) > 0 {
		finalResponseContent = "### 🔍 Discovery Results (Prioritized)\n"
		for _, res := range allResults {
			finalResponseContent += res.Content + "\n"
		}
	} else {
		finalResponseContent = "No resources found matching your query across Kubernetes, Cloud, and Monitoring platforms."
	}

	return core.NBAgentResponse{
		Response:          []string{finalResponseContent},
		AgentName:         a.GetName(),
		Status:            core.ConversationStatusCompleted,
		AgentStepResponse: agentStepResponses,
	}, nil
}

// genericQueryWords are common words that should not be used alone as search terms.
var genericQueryWords = map[string]bool{
	"find": true, "search": true, "show": true, "list": true, "get": true,
	"all": true, "the": true, "for": true, "and": true, "my": true,
	"pod": true, "pods": true, "server": true, "service": true, "app": true,
	"api": true, "web": true,
	"cluster": true, "cloud": true, "instances": true, "across": true,
}

// extractResourceQueryTerms derives meaningful search terms from a natural-language query.
// Terms shorter than 3 chars or in genericQueryWords are excluded.
func extractResourceQueryTerms(query string) []string {
	seen := map[string]bool{}
	var terms []string
	for _, word := range strings.Fields(strings.ToLower(query)) {
		// Strip trailing punctuation
		word = strings.TrimRight(word, ".,!?;:")
		if len(word) < 3 || genericQueryWords[word] || seen[word] {
			continue
		}
		seen[word] = true
		terms = append(terms, word)
		// Also add hyphen/underscore/dot-split components (e.g. "llm-server" → "llm", "app.kubernetes.io" → "kubernetes")
		for _, part := range strings.FieldsFunc(word, func(c rune) bool { return c == '-' || c == '_' || c == '.' }) {
			if len(part) >= 3 && !genericQueryWords[part] && !seen[part] {
				seen[part] = true
				terms = append(terms, part)
			}
		}
	}
	return terms
}

// resourceNameMatchesTerms delegates to the shared implementation in the tools package.
func resourceNameMatchesTerms(name string, terms []string) bool {
	return tools.ResourceNameMatchesTerms(name, terms)
}
