package agents

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/utils"
	"nudgebee/llm/workspace"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/tmc/langchaingo/llms"
)

const LogDefaultAgentName = "logs_default"

// LogDefaultAgent is the default implementation for log querying agents.
// It translates natural language queries into structured log queries using
// a two-step process: query generation and query execution.
//
// The agent supports multiple observability providers (Datadog, Signoz, Loki)
// and provides provider-specific examples to improve query generation accuracy.
// It includes error recovery logic to automatically retry failed queries with corrections.
type LogDefaultAgent struct {
	accountId string
	tools     []toolcore.NBTool
	toolsOnce sync.Once
	provider  services_server.ObservabilityProvider
}

func (l *LogDefaultAgent) GetName() string {
	return LogDefaultAgentName
}

func (l *LogDefaultAgent) GetNameAliases() []string {
	return []string{"Logs"}
}

func (l *LogDefaultAgent) GetDescription() string {
	return `Collects and analyzes logs by translating natural language questions into structured queries. Use this agent to search, filter, or troubleshoot logs for automation, monitoring, or debugging. Returns log data and summaries based on your query.`
}

func (l *LogDefaultAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**Your Task:** Retrieve and analyze logs by translating natural language questions into structured queries.",
		"**Two-Step Workflow:**",
		"  1. Use `query_generator` to convert the user's question into a valid JSON query.",
		"  2. Use `logs_execute` to execute the generated JSON query and retrieve logs.",
		"  3. If execution succeeds → Present results.",
		"  4. If execution fails → Analyze error and retry with corrected query.",
		"**Error Recovery Process:**",
		"  When `logs_execute` returns an error:",
		"  - **Analyze:** Identify the root cause (invalid field, wrong operator, syntax error, missing filter).",
		"  - **Diagnose Common Errors:**",
		"    • 'Unknown field X' → Field doesn't exist, use available fields only",
		"    • 'Invalid operator Y' → Use supported operators only",
		"    • 'Syntax error' → Check JSON structure",
		"    • 'No data found' → Query is valid but returned empty results (not an error)",
		"  - **Regenerate:** Call `query_generator` again with error context to create a CORRECTED query.",
		"  - **Retry:** Execute the corrected query with `logs_execute`.",
		"  - **Limit:** Maximum 2 retry attempts. After 2 failures, report the issue clearly to the user.",
		"**Context Awareness:** Extract key details from user queries:",
		"  - Time ranges (e.g., 'last hour', '2024-01-01 to 2024-01-02')",
		"  - Workload identifiers (pods, deployments, services)",
		"  - Namespaces and clusters",
		"  - Keywords and error patterns (e.g., 'timeout', 'error', '500')",
		"**Present Results:** Provide a concise, markdown-formatted summary of the logs with key insights.",
		"**Always Execute:** Never skip the execution step. Always validate the query by running it with `logs_execute`.",
	}

	constraints := []string{
		"MUST use `query_generator` tool to generate the JSON query.",
		"MUST use `logs_execute` tool to execute the JSON query.",
		"MUST NOT answer the question without using both tools.",
		"MUST NOT skip query execution - always validate by running the query.",
		"If query execution fails, MUST analyze the error and regenerate a corrected query.",
		"MUST retry failed queries with corrections up to 2 times.",
		"After 2 failed attempts, MUST explain the issue clearly to the user.",
		"MUST differentiate between execution errors (fix and retry) and empty results (valid query, no data).",
		"Avoid asking for clarification when context clues can reasonably infer intent.",
		"If genuinely ambiguous after retry attempts, make reasonable assumptions and state them clearly in your response.",
	}

	toolUsage := map[string][]string{
		QueryGeneratorAgentName: {
			"Purpose: Generate a valid JSON query from natural language",
			"Input: User's log query in natural language",
			"Context Aware: Can accept error feedback to generate corrected queries",
			"Example: 'show error logs' → {\"where\": {\"_body\": {\"_ilike\": \"%error%\"}}}",
			"Output: Structured JSON query compatible with the log provider",
		},
		tools.ToolLogsExecute: {
			"Purpose: Execute a JSON query and retrieve logs",
			"Input: Valid JSON query from query_generator",
			"Optional Parameters:",
			"  - start_time: ISO timestamp (e.g., '2024-01-01T10:00:00Z')",
			"  - end_time: ISO timestamp",
			"  - range: Relative time (e.g., '1h', '2d', '1w')",
			"  - limit: Maximum number of logs to return",
			"  - index: Elasticsearch index name or pattern (e.g., 'app-logs-*'). Omit to use account default.",
			"Output: Log entries matching the query OR error message with details for correction",
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in log analysis using structured query generation and execution with error recovery",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples: []core.NBAgentPromptExample{
			{
				Question: "Show me logs for the application 'my-app' in namespace 'production'.",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:  QueryGeneratorAgentName,
						Input: "Show me logs for the application 'my-app' in namespace 'production'",
					},
					{
						Tool:  tools.ToolLogsExecute,
						Input: `{"command": {"where": {"app":{"_eq":"my-app"},"namespace":{"_eq":"production"}}}}`,
					},
				},
				Explanation: "Two-step process: generate query using query_generator, then execute with logs_execute.",
			},
			{
				Question: "Get error logs from the last hour.",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:  QueryGeneratorAgentName,
						Input: "Get error logs from the last hour",
					},
					{
						Tool:  tools.ToolLogsExecute,
						Input: `{"command": {"where": {"_body":{"_ilike":"%error%"}}}, "range": "1h"}`,
					},
				},
				Explanation: "Use time range parameter in logs_execute for temporal filtering.",
			},
			{
				Question: "Show logs for pod 'api-server-xyz' between 10:00 and 11:00 today.",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:  QueryGeneratorAgentName,
						Input: "Show logs for pod 'api-server-xyz'",
					},
					{
						Tool:  tools.ToolLogsExecute,
						Input: `{"command": {"where": {"pod":{"_eq":"api-server-xyz"}}}, "start_time": "2024-01-01T10:00:00Z", "end_time": "2024-01-01T11:00:00Z"}`,
					},
				},
				Explanation: "Use start_time and end_time for precise time windows.",
			},
			{
				Question: "Show logs for service 'api-server'.",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:  QueryGeneratorAgentName,
						Input: "Show logs for service 'api-server'",
					},
					{
						Tool:  tools.ToolLogsExecute,
						Input: `{"command": {"where": {"service":{"_eq":"api-server"}}}}`,
					},
					{
						Tool:  QueryGeneratorAgentName,
						Input: "Show logs for app 'api-server' (previous attempt used invalid field 'service', use 'app' instead)",
					},
					{
						Tool:  tools.ToolLogsExecute,
						Input: `{"command": {"where": {"app":{"_eq":"api-server"}}}}`,
					},
				},
				Explanation: "When execution fails, analyze the error, regenerate with corrections, and retry.",
			},
			{
				Question: "Get logs containing 'timeout' errors.",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:  QueryGeneratorAgentName,
						Input: "Get logs containing 'timeout' errors",
					},
					{
						Tool:  tools.ToolLogsExecute,
						Input: `{"command": {"where": {"_body":{"_eq":"timeout"}}}}`,
					},
					{
						Tool:  QueryGeneratorAgentName,
						Input: "Get logs containing 'timeout' using pattern matching (previous attempt used exact match which returned 0 results)",
					},
					{
						Tool:  tools.ToolLogsExecute,
						Input: `{"command": {"where": {"_body":{"_ilike":"%timeout%"}}}}`,
					},
				},
				Explanation: "Empty results may indicate query needs refinement (e.g., _eq → _ilike for text search).",
			},
		},
	}
}

func (p *LogDefaultAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	p.toolsOnce.Do(func() {
		executeTool, err := tools.NewNBLogTool(p.accountId)
		if err != nil {
			slog.Error("logagent: failed to create NBLogTool for default agent", "error", err, "account_id", p.accountId)
			return
		}
		p.tools = []toolcore.NBTool{executeTool}

		// Fetch available ES indices for the account (if ES provider)
		var availableIndices map[string]string
		if tools.IsESLogProvider(p.provider.Provider) {
			indexCfg := utils.GetESAccountIndexConfig(p.accountId)
			availableIndices = indexCfg.Indices
		}

		queryAgent := newQueryGeneratorAgent(p.accountId, executeTool.QueryLabels(), executeTool.GetOperators(), p.getProviderSpecificExamples(), availableIndices)
		queryTool := core.NewToolFromAgent(queryAgent)
		p.tools = append(p.tools, queryTool)

		// Add resource search tool for resolving ambiguous resource names
		if resourceSearchTool, ok := toolcore.GetNBTool(p.accountId, ResourceSearchAgentName); ok {
			p.tools = append(p.tools, resourceSearchTool)
		}
	})
	return p.tools
}

func (l *LogDefaultAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

func (l *LogDefaultAgent) Execute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	var agentStepResponses []core.ToolInvocation
	var finalLogs string
	var query string
	var effortLog []string
	var references []toolcore.NBToolResponseReference

	// 1. Initialize/Retrieve Tools
	supportedTools := l.GetSupportedTools(ctx)
	if len(supportedTools) < 2 {
		return core.NBAgentResponse{}, fmt.Errorf("log_default: tools not properly initialized")
	}
	// Tools: [0] logs_execute, [1] query_generator, [2] resource_search (optional)
	executeTool := supportedTools[0]
	queryGen := supportedTools[1]

	// 2. Resource Resolution (Optional but Proactive)
	resolvedContext := ""
	if len(supportedTools) >= 3 {
		resolved, resolvedRefs, err := l.resolveResource(ctx, request)
		if err == nil && resolved != "" && !strings.Contains(strings.ToLower(resolved), "no resources found") {
			resolvedContext = fmt.Sprintf("Found matching resource details: %s", resolved)
			references = append(references, resolvedRefs...)
			effortLog = append(effortLog, fmt.Sprintf("Step 0: Discovered resource context via search: %s", resolved))
			ctx.GetLogger().Info("log_default: proactive resource resolution", "context", resolvedContext)
		}
	}

	// 3. Query Generation & Execution Loop (max 2 attempts)
	currentError := ""
	for i := 0; i < 2; i++ {
		// 3a. Generate or Refine JSON Query
		if i == 0 {
			// First attempt: standard generation
			nbRequestContext := toolcore.NbToolContext{
				Ctx:            ctx,
				AccountId:      l.accountId,
				ConversationId: request.ConversationId,
				ParentAgentId:  request.AgentId,
				MessageId:      request.MessageId,
				QueryContext:   request.QueryContext + "\n" + resolvedContext,
				QueryConfig:    request.QueryConfig,
				UserId:         request.UserId,
				// Tell the query_generator sub-agent (ReAct planner) to also surface
				// KBs mapped to "logs_default" plus anything inherited from a custom
				// ancestor of logs_default itself, via its lazy <skill-lists> + load_skills flow.
				InheritSkillsFromAgents: append(request.InheritSkillsFromAgents, l.GetName()),
				OriginalQuery:           request.OriginalQuery,
				SelectedSkillIds:        request.SelectedSkillIds,
			}

			agentTool, ok := queryGen.(core.AgentTool)
			if !ok {
				return core.NBAgentResponse{}, fmt.Errorf("log_default: query_generator does not implement AgentTool")
			}

			genResp, err := core.ExecuteAgentToolCall(nbRequestContext, agentTool.GetAgent(ctx), toolcore.NBToolCallRequest{Command: request.Query})
			if err != nil || len(genResp.Response) == 0 {
				return core.NBAgentResponse{}, fmt.Errorf("query generation failed: %w", err)
			}
			query = genResp.Response[0]

			// Programmatic Sanitization: Fix namespace with spaces (LLM often combines cluster + namespace)
			if strings.Contains(query, "\"namespace\"") {
				tempData := map[string]any{}
				if err := common.UnmarshalJson([]byte(query), &tempData); err == nil {
					if where, ok := tempData["where"].(map[string]any); ok {
						if ns, ok := where["namespace"].(map[string]any); ok {
							for op, val := range ns {
								if s, ok := val.(string); ok && strings.Contains(s, " ") {
									parts := strings.Fields(s)
									where["namespace"].(map[string]any)[op] = parts[len(parts)-1]
									newQuery, _ := common.MarshalJson(tempData)
									query = string(newQuery)
									effortLog = append(effortLog, fmt.Sprintf("Sanitization: Cleaned namespace '%s' to '%s'", s, parts[len(parts)-1]))
								}
							}
						}
					}
				}
			}
		} else {
			// Second attempt: LLM-driven refinement based on previous error
			ctx.GetLogger().Info("log_default: attempting query refinement after error", "error", currentError)
			refined, err := l.refineLogQueryWithError(ctx, request, query, currentError)
			if err != nil {
				effortLog = append(effortLog, fmt.Sprintf("Attempt %d: Refinement failed: %v", i+1, err))
				break
			}
			query = refined
		}

		effortLog = append(effortLog, fmt.Sprintf("Attempt %d: Executing query: %s", i+1, query))

		// 3b. Execute Logs Retrieval
		toolCtx := toolcore.NewNbToolContext(ctx, executeTool, l.accountId, request.UserId, request.ConversationId, request.MessageId, request.AgentId, query, nil, request.QueryContext, request.QueryConfig, "")
		toolResp, toolErr := executeTool.Call(toolCtx, toolcore.NBToolCallRequest{Command: query})

		// Record step
		agentStepResponses = append(agentStepResponses, core.ToolInvocation{
			Call:       llms.ToolCall{Type: "function", FunctionCall: &llms.FunctionCall{Name: tools.ToolLogsExecute, Arguments: query}},
			Response:   llms.ToolCallResponse{Name: tools.ToolLogsExecute, Content: toolResp.Data},
			References: toolResp.References,
		})

		if toolErr != nil || toolResp.Status == toolcore.NBToolResponseStatusError {
			currentError = toolResp.Data
			if currentError == "" && toolErr != nil {
				currentError = toolErr.Error()
			}
			effortLog = append(effortLog, fmt.Sprintf("Attempt %d Result: Failed with error: %s", i+1, currentError))

			// Don't waste an LLM refinement call on infrastructure errors that no query change can fix
			if !isRetryableLogQueryError(currentError) {
				effortLog = append(effortLog, fmt.Sprintf("Attempt %d: Non-retryable error, skipping refinement", i+1))
				break
			}
			continue // Trigger retry with refinement
		}

		// Success — but check if it's an empty result (valid query, no data)
		finalLogs = toolResp.Data
		references = append(references, toolResp.References...)

		isEmptyResult := strings.Contains(strings.ToLower(finalLogs), "no logs found") || strings.Contains(strings.ToLower(finalLogs), "no results")
		if isEmptyResult && i == 0 {
			// First attempt returned no data — the query might have used wrong
			// labels. Trigger refinement on the next iteration instead of
			// returning immediately.
			currentError = finalLogs
			effortLog = append(effortLog, fmt.Sprintf("Attempt %d Result: No logs found, will attempt refinement", i+1))
			continue
		}
		if isEmptyResult {
			// Second attempt also returned no data — report as failed so the
			// parent logAgent falls through to the kubectl fallback.
			return core.NBAgentResponse{
				Response:          []string{finalLogs},
				AgentName:         l.GetName(),
				Status:            core.ConversationStatusFailed,
				AgentStepResponse: agentStepResponses,
				References:        references,
			}, nil
		}

		effortLog = append(effortLog, fmt.Sprintf("Attempt %d Result: Success! Found logs.", i+1))
		break
	}

	if finalLogs == "" {
		failMsg := fmt.Sprintf("Failed to retrieve logs from %s after %d attempts.\n\n### Effort Summary:\n* %s",
			l.provider.Provider, len(effortLog)/2+1, strings.Join(effortLog, "\n* "))

		return core.NBAgentResponse{
			Response:          []string{failMsg},
			Status:            core.ConversationStatusFailed,
			AgentStepResponse: agentStepResponses,
		}, nil
	}

	// 4. Save raw logs to workspace
	logFileName := fmt.Sprintf("logs_%s_%d.txt", strings.ToLower(l.provider.Provider), time.Now().UnixNano())
	wm := workspace.NewWorkspaceManager()
	var finalReferences []toolcore.NBToolResponseReference

	if err := wm.SaveFile(ctx, l.accountId, request.ConversationId, logFileName, finalLogs); err == nil {
		// Priority 1: File Reference
		finalReferences = append(finalReferences, toolcore.NBToolResponseReference{
			Text:        logFileName,
			Url:         logFileName,
			Type:        "file",
			Description: fmt.Sprintf("Raw log data from %s", l.provider.Provider),
		})
		effortLog = append(effortLog, fmt.Sprintf("System: Raw logs saved to workspace as %s", logFileName))
		ctx.GetLogger().Info("log_default: logs saved", "file", logFileName, "total_steps", len(effortLog))
	} else {
		ctx.GetLogger().Warn("log_default: failed to save logs to workspace", "error", err)
	}

	// Add other collected references (e.g. Loki query link)
	finalReferences = append(finalReferences, references...)

	// 5. Post-process (Smart Tail: Raw Tail + Errors Spotlight)
	processedLogs := l.processLogData(request, finalLogs)

	// 6. Final Response Synthesis
	finalAnswer, err := l.generateFinalResponse(ctx, request, query, processedLogs)
	if err != nil {
		finalAnswer = fmt.Sprintf("Retrieved logs from %s provider using query: `%s`.\n\n%s", l.provider.Provider, query, processedLogs)
	}

	// If the LLM determined no relevant data exists, propagate as failure
	// so the parent logs agent falls through to kubectl fallback.
	if isNoLogDataResponse(finalAnswer) {
		ctx.GetLogger().Info("log_default: LLM synthesis indicates no relevant log data, returning failed for fallback", "provider", l.provider.Provider)
		return core.NBAgentResponse{
			Response:          []string{finalAnswer},
			AgentName:         l.GetName(),
			Status:            core.ConversationStatusFailed,
			AgentStepResponse: agentStepResponses,
			References:        finalReferences,
		}, nil
	}

	return core.NBAgentResponse{
		Response:          []string{finalAnswer},
		AgentName:         l.GetName(),
		Status:            core.ConversationStatusCompleted,
		AgentStepResponse: agentStepResponses,
		References:        finalReferences,
	}, nil
}

func (l *LogDefaultAgent) resolveResource(ctx *security.RequestContext, request core.NBAgentRequest) (string, []toolcore.NBToolResponseReference, error) {
	tool, ok := toolcore.GetNBTool(l.accountId, ResourceSearchAgentName)
	if !ok {
		return "", nil, fmt.Errorf("search tool not found")
	}

	// Simple heuristic extraction: find words after 'app', 'service', 'pod', 'deployment'
	searchQuery := ""
	lowQuery := strings.ToLower(request.Query)
	patterns := []string{"app\\s+([^\\s]+)", "service\\s+([^\\s]+)", "pod\\s+([^\\s]+)", "deployment\\s+([^\\s]+)", "workload\\s+([^\\s]+)"}
	for _, p := range patterns {
		re := regexp.MustCompile(p)
		matches := re.FindStringSubmatch(lowQuery)
		if len(matches) > 1 {
			searchQuery = matches[1]
			break
		}
	}

	// If no specific keyword found, only search if query seems to be about a specific thing
	if searchQuery == "" {
		if !strings.Contains(lowQuery, "app") && !strings.Contains(lowQuery, "service") && !strings.Contains(lowQuery, "pod") && !strings.Contains(lowQuery, "workload") {
			return "", nil, nil
		}
		searchQuery = request.Query // Fallback to full query if keywords present but extraction failed
	}

	toolCtx := toolcore.NewNbToolContext(ctx, tool, l.accountId, request.UserId, request.ConversationId, request.MessageId, request.AgentId, searchQuery, nil, request.QueryContext, request.QueryConfig, "")
	// resource_search is registered via RegisterNBAgentFactoryAndTool, so calling
	// it as a tool re-enters executeAgent through nbAgentTool.Call → ExecuteAgentToolCall.
	// Propagate the inherited-skills chain (plus log_default's own name) so the
	// re-entrant executor can union them with resource_search's own KB mappings.
	toolCtx.InheritSkillsFromAgents = append(request.InheritSkillsFromAgents, l.GetName())
	toolCtx.OriginalQuery = request.OriginalQuery
	toolCtx.SelectedSkillIds = request.SelectedSkillIds
	resp, err := tool.Call(toolCtx, toolcore.NBToolCallRequest{Command: searchQuery})
	if err != nil {
		ctx.GetLogger().Warn("log_default: resource search tool call failed, continuing without resource context", "error", err)
		return "", nil, nil
	}

	if resp.Status == toolcore.NBToolResponseStatusError {
		ctx.GetLogger().Warn("log_default: resource search returned error status, continuing without resource context", "data_preview", resp.Data[:min(len(resp.Data), 200)])
		return "", nil, nil
	}

	return resp.Data, resp.References, nil
}

func (l *LogDefaultAgent) processLogData(request core.NBAgentRequest, rawLogs string) string {
	isErrorQuery := strings.Contains(strings.ToLower(request.Query), "error") || strings.Contains(strings.ToLower(request.Query), "fail")
	lines := strings.Split(rawLogs, "\n")

	// Part A: Error Spotlight
	errorLines := tools.GetErrorLinesFromLogStringOrDefault(rawLogs, false)

	// Part B: Raw Tail (Take last 50 lines for context, or 20 lines if it's strictly an error query)
	tailCount := 50
	if isErrorQuery && len(errorLines) > 0 {
		tailCount = 20
	}

	tailStart := 0
	if len(lines) > tailCount {
		tailStart = len(lines) - tailCount
	}
	rawTail := strings.Join(lines[tailStart:], "\n")

	processed := ""
	if rawTail != "" {
		processed += fmt.Sprintf("--- RECENT LOG TAIL ---\n%s\n\n", rawTail)
	}
	if len(errorLines) > 0 {
		processed += fmt.Sprintf("--- DETECTED ERROR HIGHLIGHTS ---\n%s", strings.Join(errorLines, "\n"))
	}

	// Hard cap at 10k chars
	if len(processed) > 10000 {
		processed = processed[:5000] + "\n\n... [TRUNCATED] ...\n\n" + processed[len(processed)-5000:]
	}
	return processed
}

func (l *LogDefaultAgent) generateFinalResponse(ctx *security.RequestContext, request core.NBAgentRequest, query, logs string) (string, error) {
	systemPrompt := fmt.Sprintf(`You are an expert SRE analyzing logs from %s (query: %s).

HARD RULES:
- If the logs contain NO relevant data (empty results, only metadata, no matching entries), respond with ONLY: "No log data found for the given query."
- NEVER speculate beyond what the log data shows. State facts from the data, not hypotheses about what might be wrong.
- Limit response to 400 words. Prefer tables and bullet points over prose.

ANALYSIS FRAMEWORK:
1. **Answer First:** Open with a 1-2 sentence direct answer to the user's question based on what the logs show.
2. **Quantitative Summary:** Count errors vs total log lines. Report error rate or frequency if determinable. Group duplicate/similar errors — report unique error signatures with occurrence counts, not every instance.
3. **Timeline:** Note the first and last occurrence timestamps. Identify if errors are continuous, intermittent, or a single burst.
4. **Error Signatures:** Extract specific error messages, HTTP status codes, exception types, and stack trace entry points. Include the exact log line as evidence (truncated if long).
5. **Correlation:** If trace IDs, request IDs, or correlation IDs are present, mention them for cross-service debugging.
6. **Log Health Check:** If logs exist but contain no errors/warnings, explicitly state the application appears healthy from a logging perspective.

OUTPUT FORMAT (Markdown):
### Log Analysis Summary
[1-2 sentence direct answer]

**Query Period:** [start] to [end]
**Log Volume:** [N total lines, M errors, K warnings]

### Findings
[Grouped error signatures with counts, timestamps, and evidence]

### Assessment
[Conclude: healthy / degraded / failing, with confidence level based on data quality]

User Question: %s`, l.provider.Provider, query, request.Query)

	// Custom-planner agents bypass the executor's systemMessage path so the
	// lazy <skill-lists> + load_skills mechanism does not reach this direct LLM
	// call. Prepend SkillsContext (eagerly loaded by the executor) so mapped
	// expert guidance informs the final log summary.
	if strings.TrimSpace(request.SkillsContext) != "" {
		systemPrompt = request.SkillsContext + "\n\n" + systemPrompt
	}

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf("Logs:\n%s", logs)),
	}

	res, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, false, messages, true)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Choices[0].Content), nil
}

func (l *LogDefaultAgent) refinementStrategies() string {
	switch strings.ToLower(l.provider.Provider) {
	case "es", "elasticsearch":
		return `REFINEMENT STRATEGIES (Elasticsearch):
1. If "No logs found", try broadening: replace exact field matches with a wildcard on "message" (e.g. {"message": {"_ilike": "%keyword%"}}).
2. If a field is not found, try alternative names: "kubernetes.pod_name.keyword" instead of "pod", "kubernetes.namespace_name.keyword" instead of "namespace".
3. LABEL FIELDS: "kubernetes.labels.*" fields are text type — do NOT use ".keyword" suffix. Use "kubernetes.labels.app_kubernetes_io/name", NOT "kubernetes.labels.app_kubernetes_io/name.keyword".
4. KEYWORD SUFFIX RULE: Only use ".keyword" for standard metadata fields (pod_name, namespace_name, container_name, host). Never append ".keyword" to "kubernetes.labels.*" fields.
5. Ensure the query follows the QueryBuilder schema: {"where": {field: {operator: value}}, "range": "Xh"}.`
	case "signoz":
		return `REFINEMENT STRATEGIES (Signoz):
1. If "No logs found", try broadening: use "body" field with "_ilike" for full-text search (e.g. {"body": {"_ilike": "%keyword%"}}).
2. Common Signoz field names: "service.name", "severity_text", "service.namespace", "pod_name", "container_name", "source", "body".
3. Prefer "_ilike" (contains) over "_eq" (exact match) for text searches — Signoz maps _ilike to "contains" operator.
4. If a specific field is not found, try the "body" field as a fallback for keyword matching.
5. Ensure the query follows the QueryBuilder schema: {"where": {field: {operator: value}}, "range": "Xh"}.
6. FIELD MAPPING RULES: Use "pod_name" for pods (NOT host.name), "container_name" for containers (NOT service.name), "service.namespace" for namespaces (NOT deployment.environment).`
	case "loki":
		return `REFINEMENT STRATEGIES (Loki):
1. If "No logs found", labels like "app" or "service" might be incorrect for this cluster.
2. TRY BROADENING: Drop specific labels and use a line filter on the body instead.
3. USE SINGLE-WORD KEYWORDS: For line filters, use specific words like "coredns" or "error" rather than full phrases.
4. EXAMPLE: Instead of {"app": "coredns"}, try {"_body": {"_ilike": "%coredns%"}} combined with a reliable label like "namespace".
5. Ensure the query follows the provider's valid schema.`
	default:
		return `REFINEMENT STRATEGIES:
1. If "No logs found", try broadening the query by removing overly specific filters.
2. Check field names are valid for the provider and simplify the where clause.
3. Ensure the query follows the QueryBuilder schema: {"where": {field: {operator: value}}}.`
	}
}

func (l *LogDefaultAgent) refineLogQueryWithError(ctx *security.RequestContext, request core.NBAgentRequest, currentQuery, errorMsg string) (string, error) {
	systemPrompt := fmt.Sprintf(`The previous JSON query for %s logs failed or returned empty results.
Analyze the error and the user's original request to provide a CORRECTED JSON query.

PROVIDER: %s
ERROR/RESULT:
%s

CURRENT QUERY:
%s

%s

Return ONLY the corrected JSON query object within triple backticks.`,
		l.provider.Provider, l.provider.Provider, errorMsg, currentQuery,
		l.refinementStrategies())

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, request.Query),
	}

	// Use Lite model for correction turn
	liteCtx := security.NewRequestContext(
		context.WithValue(ctx.GetContext(), core.ContextKeyUseLiteModel, true),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	res, err := core.GenerateAndTrackLLMContent(liteCtx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, false, messages, true)
	if err != nil {
		return "", err
	}

	return core.SanatizeMarkdownCodeBlock(res.Choices[0].Content), nil
}

// isNoLogDataResponse returns true when the LLM synthesis indicates no
// meaningful log data was found. The length guard avoids false positives
// on detailed analyses that incidentally contain a trigger phrase; the
// prompt constrains the LLM to a short deterministic reply for no-data.
func isNoLogDataResponse(response string) bool {
	if len(response) > maxShortResponseLen {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(response))
	noDataPatterns := []string{
		"no log data found",
		"no logs found",
		"no relevant log data",
		"no matching log entries",
	}
	for _, p := range noDataPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func (l *LogDefaultAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	// Not used in Custom mode but kept for interface compatibility
	return toolResponse
}

func (p *LogDefaultAgent) getProviderSpecificExamples() []core.NBAgentPromptExample {
	switch strings.ToLower(p.provider.Provider) {
	case "datadog":
		return []core.NBAgentPromptExample{
			{
				Question:    "Show me logs for service 'web-api'.",
				Answer:      `{"where": {"service":{"_eq":"web-api"}}}`,
				Explanation: "Available Labels - service",
			},
			{
				Question:    "Get error logs for service 'web-api'.",
				Answer:      `{"where": {"service":{"_eq":"web-api"}, "status":{"_eq":"error"}}}`,
				Explanation: "Available Labels - service, status",
			},
			{
				Question:    "Find logs for pod 'my-pod' in namespace 'prod'.",
				Answer:      `{"where": {"pod_name":{"_eq":"my-pod"}, "kube_namespace":{"_eq":"prod"}}}`,
				Explanation: "Available Labels - pod_name, kube_namespace",
			},
			{
				Question:    "Show logs from container 'nginx' with warning level.",
				Answer:      `{"where": {"container_name":{"_eq":"nginx"}, "@level":{"_eq":"warn"}}}`,
				Explanation: "Available Labels - container_name, @level",
			},
			{
				Question:    "Get logs from image 'alpine:3.15' in the last hour.",
				Answer:      `{"command": {"where": {"image_name":{"_eq":"alpine:3.15"}}}, "range": "1h"}`,
				Explanation: "Available Labels - image_name, range",
			},
			{
				Question:    "Find logs containing 'connection timeout' from host 'web-server-01'.",
				Answer:      `{"where": {"host":{"_eq":"web-server-01"}, "_body":{"_ilike":"%connection timeout%"}}}`,
				Explanation: "Available Labels - host, _body",
			},
			{
				Question:    "Show last 50 logs for deployment 'api-deployment'.",
				Answer:      `{"where": {"kube_ownerref_name":{"_eq":"api-deployment"}}, "limit": 50}`,
				Explanation: "Available Labels - kube_ownerref_name, limit",
			},
		}
	case "signoz":
		return []core.NBAgentPromptExample{
			{
				Question:    "Show me logs for service 'web-api'.",
				Answer:      `{"where": {"service.name":{"_ilike":"%web-api%"}}}`,
				Explanation: "Available Labels - service.name. Prefer _ilike (contains) over _eq for text matching.",
			},
			{
				Question:    "Get error logs for service 'web-api'.",
				Answer:      `{"where": {"service.name":{"_ilike":"%web-api%"}, "severity_text":{"_eq":"ERROR"}}}`,
				Explanation: "Available Labels - service.name, severity_text. severity_text values: TRACE, DEBUG, INFO, WARN, ERROR, FATAL.",
			},
			{
				Question:    "Find logs for namespace 'prod'.",
				Answer:      `{"where": {"service.namespace":{"_eq":"prod"}}}`,
				Explanation: "Available Labels - service.namespace. Use service.namespace for Kubernetes namespace filtering, NOT deployment.environment.",
			},
			{
				Question:    "Get logs from pod api-server-abc123.",
				Answer:      `{"where": {"pod_name":{"_ilike":"%api-server-abc123%"}}}`,
				Explanation: "Available Labels - pod_name. IMPORTANT: Use pod_name for pod filtering, NOT host.name or service.name.",
			},
			{
				Question:    "Show debug logs from pod 'app-pod-123'.",
				Answer:      `{"where": {"pod_name":{"_ilike":"%app-pod-123%"}, "severity_text":{"_eq":"DEBUG"}}}`,
				Explanation: "Available Labels - pod_name, severity_text. Always use pod_name for pod-based queries.",
			},
			{
				Question:    "Get logs from the worker container.",
				Answer:      `{"where": {"container_name":{"_ilike":"%worker%"}}}`,
				Explanation: "Available Labels - container_name. IMPORTANT: Use container_name for container filtering, NOT service.name.",
			},
			{
				Question:    "Get logs from container 'nginx' in staging namespace.",
				Answer:      `{"where": {"container_name":{"_ilike":"%nginx%"}, "service.namespace":{"_ilike":"%staging%"}}}`,
				Explanation: "Available Labels - container_name, service.namespace. Use container_name for containers and service.namespace for namespaces.",
			},
			{
				Question:    "Find logs containing 'database error' from service 'user-service'.",
				Answer:      `{"where": {"service.name":{"_ilike":"%user-service%"}, "body":{"_ilike":"%database error%"}}}`,
				Explanation: "Available Labels - service.name, body. Use body field with _ilike for full-text log search.",
			},
			{
				Question:    "Show last 100 logs for deployment in namespace 'staging'.",
				Answer:      `{"where": {"service.namespace":{"_ilike":"%staging%"}}, "limit": 100}`,
				Explanation: "Available Labels - service.namespace, limit. Use service.namespace for namespace queries.",
			},
			{
				Question:    "Get critical logs from source 'kubernetes' after yesterday.",
				Answer:      `{"where": {"source":{"_eq":"kubernetes"}, "severity_text":{"_eq":"FATAL"}}, "start_time": "2024-01-01T00:00:00Z"}`,
				Explanation: "Available Labels - source, severity_text, start_time",
			},
			{
				Question:    "What services are logging? / List all services.",
				Answer:      `{"where": {}, "limit": 100, "range": "24h"}`,
				Explanation: "For broad queries like 'list services' or 'what services exist', use an empty where clause with a wide time range. The log output will contain service.name labels that can be summarized. NEVER use _is_null operator — it is not supported.",
			},
		}
	case "loki":
		return []core.NBAgentPromptExample{
			{
				Question:    "Show me logs for app 'web-api'.",
				Answer:      `{"where": {"app":{"_eq":"web-api"}}}`,
				Explanation: "Available Labels - app",
			},
			{
				Question:    "Get error logs for app 'web-api'.",
				Answer:      `{"where": {"app":{"_eq":"web-api"}, "_body":{"_ilike":"%error%"}}}`,
				Explanation: "Available Labels - app, _body. PRIORTIZE `app` over `k8s_deployment_name` if both are present",
			},
			{
				Question:    "Find logs for app 'web-api' in namespace 'prod'.",
				Answer:      `{"where": {"app":{"_eq":"web-api"}, "namespace":{"_eq":"prod"}}}`,
				Explanation: "Available Labels - app, namespace. PRIORTIZE `namespace` over `k8s_namespace_name` if both are present",
			},
			{
				Question:    "Show logs from container 'redis' on job 'cache-job'.",
				Answer:      `{"where": {"container":{"_eq":"redis"}, "job":{"_eq":"cache-job"}}}`,
				Explanation: "Available Labels - container, job",
			},
			{
				Question:    "Get logs for the api-server pod containing 'timeout'.",
				Answer:      `{"where": {"app":{"_eq":"api-server"}, "_body":{"_ilike":"%timeout%"}}}`,
				Explanation: "Use `app` to identify a service/pod by name — pod names have random suffixes (e.g. api-server-7f8b9c-x2k). Only use `pod` with `_like` for prefix patterns.",
			},
			{
				Question:    "Find logs from stream 'stderr' for instance 'web-01' in last 2 hours.",
				Answer:      `{"command": {"where": {"stream":{"_eq":"stderr"}, "instance":{"_eq":"web-01"}}}, "range": "2h"}`,
				Explanation: "Available Labels - stream, instance, range",
			},
			{
				Question:    "Show last 25 logs from filename '/var/log/app.log'.",
				Answer:      `{"where": {"filename":{"_eq":"/var/log/app.log"}}, "limit": 25}`,
				Explanation: "Available Labels - filename, limit",
			},
			{
				Question:    "Get logs from level 'warn' or 'error' for service 'auth-service'.",
				Answer:      `{"where": {"app":{"_eq":"auth-service"}, "_or": [{"level":{"_eq":"warn"}}, {"level":{"_eq":"error"}}]}}`,
				Explanation: "When the user says 'service X', map it to the Loki label that identifies workloads — typically `app`, NOT `service_name`. `service_name` is a Datadog/OTel convention and is rarely a Loki label. Always pick from the injected Fields list.",
			},
			{
				Question:    "Show me errors from the checkout-api service in the last 15 minutes.",
				Answer:      `{"where": {"app":{"_eq":"checkout-api"}, "_body":{"_ilike":"%error%"}}, "range": "15m"}`,
				Explanation: "English phrasing 'the X service' / 'X service' means workload=X. In Loki this is the `app` label (or `job` / `container` if `app` is not in Fields). Never emit `service_name`, `service.name`, or `kubernetes.labels.app` unless they appear in the injected Fields list.",
			},
			{
				Question:    "Find logs from node 'k8s-worker-1' after specific timestamp.",
				Answer:      `{"command": {"where": {"node_name":{"_eq":"k8s-worker-1"}}}, "start_time": "2024-01-01T10:00:00Z"}`,
				Explanation: "Available Labels - node_name, start_time",
			},
			{
				Question:    "Get logs around 2025-01-01 10:00:00.",
				Answer:      `{"command": {"where": {"app":{"_eq":"my-app"}}}, "start_time": "2025-01-01T09:30:00Z", "end_time": "2025-01-01T10:30:00Z"}`,
				Explanation: "Available Labels - start_time, end_time. For 'around' queries, calculate start and end times (e.g. +/- 30 mins).",
			},
		}
	case "es", "elasticsearch":
		return []core.NBAgentPromptExample{
			{
				Question:    "Show me logs for pod 'my-pod' in namespace 'production'.",
				Answer:      `{"where": {"kubernetes.pod_name.keyword": {"_eq": "my-pod"}, "kubernetes.namespace_name.keyword": {"_eq": "production"}}}`,
				Explanation: "Available Fields - kubernetes.pod_name.keyword, kubernetes.namespace_name.keyword",
			},
			{
				Question:    "Get error logs containing 'connection refused'.",
				Answer:      `{"where": {"message": {"_ilike": "%connection refused%"}}}`,
				Explanation: "Use 'message' field (not '_body') for full-text log body search. _ilike performs case-insensitive contains.",
			},
			{
				Question:    "Find logs for namespace 'staging' from the last 2 hours.",
				Answer:      `{"where": {"kubernetes.namespace_name.keyword": {"_eq": "staging"}}, "range": "2h"}`,
				Explanation: "Available Fields - kubernetes.namespace_name.keyword, range",
			},
			{
				Question:    "Show logs for container 'nginx' with error messages.",
				Answer:      `{"where": {"kubernetes.container_name.keyword": {"_eq": "nginx"}, "message": {"_ilike": "%error%"}}}`,
				Explanation: "Available Fields - kubernetes.container_name.keyword, message",
			},
			{
				Question:    "Get logs for pods whose name starts with 'api-server'.",
				Answer:      `{"where": {"kubernetes.pod_name.keyword": {"_ilike": "api-server%"}}}`,
				Explanation: "Use _ilike with SQL wildcards: % for any characters. kubernetes.pod_name.keyword for pod name prefix filter.",
			},
			{
				Question:    "Show logs between 2024-01-01 10:00 and 11:00.",
				Answer:      `{"where": {}, "start_time": "2024-01-01T10:00:00Z", "end_time": "2024-01-01T11:00:00Z"}`,
				Explanation: "Use start_time and end_time (RFC3339 format) for precise time windows.",
			},
			{
				Question:    "Show last 50 error logs for namespace 'prod'.",
				Answer:      `{"where": {"kubernetes.namespace_name.keyword": {"_eq": "prod"}, "message": {"_ilike": "%error%"}}, "limit": 50}`,
				Explanation: "Available Fields - kubernetes.namespace_name.keyword, message, limit",
			},
			{
				Question:    "Get warn or error logs for namespace 'default'.",
				Answer:      `{"where": {"kubernetes.namespace_name.keyword": {"_eq": "default"}, "_or": [{"message": {"_ilike": "%warn%"}}, {"message": {"_ilike": "%error%"}}]}}`,
				Explanation: "Use _or for multi-value conditions on the same field.",
			},
			{
				Question:    "Show me nginx access logs with 5xx errors.",
				Answer:      `{"where": {"message": {"_ilike": "%5___%"}}, "index": "nginx-access-*"}`,
				Explanation: "Use the 'index' field to target a specific Elasticsearch index pattern when the user's query implies a particular log source. Omit 'index' to use the account default.",
			},
		}
	default:
		return []core.NBAgentPromptExample{}
	}
}
