package agents

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/samber/lo"
	"github.com/tmc/langchaingo/llms"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// ==========================================
// PROMPT TEMPLATES
// ==========================================

// PROMPT_CHAIN_LOG_ANALYSIS_SYS_MSG is the main prompt for log analysis
const PROMPT_CHAIN_LOG_ANALYSIS_SYS_MSG = `
Context:
	You are an SRE (Site Reliability Engineer) responsible for maintaining the reliability and performance of a large-scale distributed system. You need to analyze system logs to identify potential issues, understand root causes, and propose solutions. The logs may include error messages, warnings, system metrics, and other diagnostic information from various components of the system.
Task:
	Analyze the provided logs to determine the underlying issue, explain the root cause, and suggest possible actions to resolve the problem. Focus on identifying patterns, anomalies, or repeated errors that could indicate the source of the issue.
Instructions:
	Strictly provide a direct and concise response without adding conversational elements or introductory phrases.
	Include specific log entries that support your analysis and recommendations.
Response Format:
	**Issue Identification:**
		Briefly describe the main issue observed in the logs.
	**Root Cause Analysis:**
		Explain the likely cause(s) of the issue, referencing specific log entries or patterns.
	**Suggested Actions:**
		Provide actionable recommendations to address the identified issue, including any immediate steps to mitigate the problem and long-term solutions to prevent recurrence.
	**Additional Observations:**
		Note any other anomalies or potential issues found in the logs that may need further investigation.
Logs: %v`

// PROMPT_CHAIN_LOKI_SUMMARY_USER_MSG summarizes log data with actionable insights
const PROMPT_CHAIN_LOKI_SUMMARY_USER_MSG = `Briefly summarize the logs based on the question that was asked with bullet points and aggregate similar records. Do not respond with any additional explanation beyond the summary. Strictly don't use keyword like 'loki' in the summary. Summarize the logs as follows: 1. Aggregate similar records to avoid duplicate entries and highlight the most important insights for the logs with Insights heading.2. Must Include the log as markdown text in summary from logs results and for each log error, provide detailed recommendations to resolve the issue with fomatted as *Log:*<log from results>\n\t*Issue:*<>\n *Resolution:*<>\n.4.Don't add count to Log:.5.when no logs to analyze, respond with 'No logs found'.\n\n`

// PROMPT_CHAIN_LOG_EXTRACT_K8S_INFO extracts Kubernetes resource information from logs
const PROMPT_CHAIN_LOG_EXTRACT_K8S_INFO = `Extract all Kubernetes resource information from the provided log data. If multiple resources are mentioned, include them all.

Return the result as a valid JSON array with the following format:
[
  {
    "namespace": "<namespace>",
    "pod_name": "<pod_name>",
    "workload_name": "<workload_name>"
  }
]

- Include at least the "namespace" and "pod_name" fields if they can be confidently determined.
- Only include "workload_name" if it is clearly identifiable.
- Do not confuse the workload name with the workload type (e.g., Deployment, StatefulSet).
- If no valid Kubernetes resources are found, return an empty array.
- If resource identification is uncertain, omit the entry entirely.

DO NOT ASSUME THE K8S INFO IF NOT MENTIONED IN THE LOG.

Log data: %v`

func init() {
	toolDescription := `Analyzes log data to identify issues and root causes, and extracts the source file path, file name, and line number related to the root cause. Use this agent to troubleshoot, investigate, or summarize log data for automation, monitoring, or debugging. Returns a summary with actionable insights and relevant file context.`
	toolInput := "Provide log data to analyze."
	toolOutput := "log analysis summary"

	core.RegisterNBAgentFactoryAndTool(AgentLogAnalysisName, func(accountId string) (core.NBAgent, error) {
		return LogAnalysisAgent{}, nil
	}, toolDescription, toolInput, toolOutput)
}

type LogAnalysisAgent struct {
}

const AgentLogAnalysisName = "loganalysis"

func (l LogAnalysisAgent) GetName() string {
	return AgentLogAnalysisName
}

func (l LogAnalysisAgent) GetNameAliases() []string {
	return []string{"Log Analysis"}
}

func (l LogAnalysisAgent) GetDescription() string {
	return `Analyzes log data to identify issues and root causes, and extracts the source file path, file name, and line number related to the root cause. Use this agent to troubleshoot, investigate, or summarize log data for automation, monitoring, or debugging. Returns a summary with actionable insights and relevant file context.`
}

func (l LogAnalysisAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (l LogAnalysisAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{
		Variables: []string{"data"},
		Instructions: []string{
			"Log Data : {{ .data }}",
		},
	}
}

func (l LogAnalysisAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

func (l LogAnalysisAgent) extractK8sInfo(ctx *security.RequestContext, accountId string, conversationId string, messageId string, agentId string, logData string, userId string) ([]map[string]string, error) {
	logger := ctx.GetLogger()
	logger.Debug("Extracting K8s info from log data", "data_length", len(logData))

	// Prepare the prompt for the LLM
	messageHistory := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf(PROMPT_CHAIN_LOG_EXTRACT_K8S_INFO, logData)),
	}

	// Generate completion with temperature 0 for more deterministic results
	completion, err := core.GenerateAndTrackLLMContent(ctx, userId, accountId, conversationId, messageId, agentId, false, messageHistory, true, llms.WithTemperature(0.0))
	if err != nil {
		return nil, fmt.Errorf("failed to extract k8s info from log data: %w", err)
	}

	if completion == nil || len(completion.Choices) == 0 {
		logger.Warn("LLM returned empty response for K8s info extraction")
		return []map[string]string{}, nil
	}

	llmResponse := completion.Choices[0].Content
	logger.Debug("Received LLM response for K8s info extraction", "response_length", len(llmResponse))

	// First try to extract JSON using a more robust approach
	jsonString := extractJSONFromText(llmResponse)
	if jsonString == "" {
		logger.Warn("No JSON array found in LLM response, returning empty result")
		return []map[string]string{}, nil
	}

	// Parse the JSON array
	var k8sInfoList []map[string]string
	err = common.UnmarshalJson([]byte(jsonString), &k8sInfoList)
	if err != nil {
		logger.Error("Failed to parse JSON from LLM response", "error", err, "json_string", jsonString)
		return nil, fmt.Errorf("failed to parse k8s info: %w", err)
	}

	return k8sInfoList, nil
}

// extractJSONFromText attempts to extract a JSON array from text using multiple methods
func extractJSONFromText(text string) string {
	// First try to find JSON array using brackets
	startIdx := strings.Index(text, "[")
	endIdx := strings.LastIndex(text, "]")

	if startIdx != -1 && endIdx != -1 && startIdx < endIdx {
		return strings.TrimSpace(text[startIdx : endIdx+1])
	}

	// If that fails, try with regex for more complex cases
	re := regexp.MustCompile(`\[\s*(?:\{[^{}]*\}(?:\s*,\s*\{[^{}]*\})*)\s*\]`)
	jsonString := strings.TrimSpace(re.FindString(text))

	return jsonString
}

func (l LogAnalysisAgent) Execute(ctx *security.RequestContext, query core.NBAgentRequest) (core.NBAgentResponse, error) {
	logger := ctx.GetLogger()

	if len(query.Query) == 0 {
		return core.NBAgentResponse{}, errors.New("loganalysis: not enough data")
	}

	logger.Debug("loganalysis: starting execution", "query_length", len(query.Query), "account_id", query.AccountId)

	agentId := query.ParentAgentId

	eventId := query.QueryConfig.EventId

	var err error
	errorLines := tools.GetErrorLinesFromLogStringOrDefault(query.Query, true)

	logger.Debug("loganalysis: error line extraction complete", "error_lines_count", len(errorLines))

	if len(errorLines) == 0 {
		return core.NBAgentResponse{}, errors.New("loganalysis: unable to identify errors")
	}
	slices.Reverse(errorLines)
	// Join with newlines instead of JSON marshaling — the data is injected into a prompt
	// template, so the LLM reads it as plain text. This also ensures truncateText can
	// split by "\n" and get individual lines (compact JSON has no newlines).
	logData := strings.Join(errorLines, "\n")

	// Extract k8s info before proceeding

	var k8sInfoList []map[string]string
	k8sInfoList, err = l.extractK8sInfo(ctx, query.AccountId, query.ConversationId, query.MessageId, agentId, query.Query, query.UserId)
	if err != nil {
		ctx.GetLogger().Error("loganalysis: failed to extract k8s info", "error", err.Error(), "account_id", query.AccountId)
	}

	if query.QueryConfig.Namespace != "" && query.QueryConfig.Workload != "" {
		k8sInfoList = append(k8sInfoList, map[string]string{
			"pod_name":      "",
			"namespace":     query.QueryConfig.Namespace,
			"workload_name": query.QueryConfig.Workload,
		})
	}

	meta, err := l.GetSourceCodeAnnotations(ctx, query, k8sInfoList, eventId)
	if err != nil {
		ctx.GetLogger().Info("loganalysis: unable to get source code annotations", "error", err)
	}

	eventLogFileDetails := []map[string]any{}

	logger.Debug("loganalysis: k8s info extraction done", "k8s_info_count", len(k8sInfoList), "has_meta", meta != nil)

	logger.Debug("loganalysis: reducing log data for max tokens")
	systemMessage, err := core.GetPromptTemplate(l.GetSystemPrompt(ctx, query), query, l.GetPlannerType()).Format(map[string]any{
		"data": logData,
	})
	if err != nil {
		ctx.GetLogger().Error("loganalysis: unable to generate system prompt", "error", err.Error())
		return core.NBAgentResponse{}, err
	}
	systemPrompt := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemMessage),
	}
	summaryMsg := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, PROMPT_CHAIN_LOKI_SUMMARY_USER_MSG),
	}
	provider := core.GetLLMProvider(ctx, query.AccountId, l.GetName(), false, query.ConversationId)
	model := core.GetLLMModelName(ctx, query.AccountId, provider, l.GetName(), false, query.ConversationId)
	maxTokens := core.GetLlmMaxTokenLength(model)
	logTokenCount := core.GetLLMNumTokensFromMessages(ctx, systemPrompt, provider, model)
	promptTokenCount := core.GetLLMNumTokensFromMessages(ctx, summaryMsg, provider, model)
	maxTokens = maxTokens - promptTokenCount

	// SkillsContext is prepended to the final messageContent below, so its tokens
	// must be subtracted from the budget before we decide how much log data we can
	// keep — otherwise large skills push the final request past the model limit
	// and the safety check at the end of this function rejects the call.
	if strings.TrimSpace(query.SkillsContext) != "" {
		skillsMsg := []llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeHuman, query.SkillsContext),
		}
		skillsTokens := core.GetLLMNumTokensFromMessages(ctx, skillsMsg, provider, model)
		maxTokens = maxTokens - skillsTokens
		logger.Debug("loganalysis: budgeted skills context", "skills_tokens", skillsTokens, "remaining_max_tokens", maxTokens)
	}

	data := logData
	if logTokenCount > maxTokens {
		logger.Debug("loganalysis: truncating log data", "log_tokens", logTokenCount, "max_tokens", maxTokens)
		data = l.truncateText(ctx, logData, maxTokens-30, provider, model)
	}
	logger.Debug("loganalysis: log data prepared", "data_length", len(data), "truncated", logTokenCount > maxTokens)

	logger.Debug("loganalysis: generating log file details")
	logFileDetailMessage := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, prompts_repo.GetPrompt(prompts_repo.PromptLogAnalysisFileExtractor, query.Query)),
	}
	res, err := core.GenerateAndTrackLLMContent(ctx, query.UserId, query.AccountId, query.ConversationId, query.MessageId, query.AgentId, false, logFileDetailMessage, true, llms.WithTemperature(0.0))
	if err != nil {
		ctx.GetLogger().Error("loganalysis: file extraction LLM call failed, continuing without file details", "error", err)
	}

	jsonString := ""
	if res == nil || len(res.Choices) == 0 || strings.TrimSpace(res.Choices[0].Content) == "" {
		if err == nil {
			logger.Warn("loganalysis: file extraction LLM returned empty response, continuing without file details")
		}
	} else {
		jsonString = res.Choices[0].Content
	}
	if jsonString != "" {
		ctx.GetLogger().Debug("Received JSON string", "content", jsonString)

		// Clean the JSON string if it contains any markdown formatting or extra characters
		cleanedJSON := jsonString
		// Remove markdown code block markers if present
		cleanedJSON = strings.TrimPrefix(cleanedJSON, "```json\n")
		cleanedJSON = strings.TrimSuffix(cleanedJSON, "\n```")
		cleanedJSON = strings.TrimSpace(cleanedJSON)

		// Try to unmarshal as array first
		err = common.UnmarshalJson([]byte(cleanedJSON), &eventLogFileDetails)
		if err != nil {
			ctx.GetLogger().Error("Failed to unmarshal JSON array", "error", err, "data", cleanedJSON)

			// Try to unmarshal as single object
			var eventLogFileDetail map[string]any
			err = common.UnmarshalJson([]byte(cleanedJSON), &eventLogFileDetail)
			if err != nil {
				ctx.GetLogger().Error("Failed to unmarshal JSON object", "error", err, "data", cleanedJSON)
			} else {
				eventLogFileDetails = append(eventLogFileDetails, eventLogFileDetail)
			}
		}

		ctx.GetLogger().Debug("Parsed file details", "count", len(eventLogFileDetails))

		// Verifying the file path and filename exist in the log data
		eventLogFileDetails = lo.Filter(eventLogFileDetails, func(fd map[string]any, i int) bool {
			if fd == nil {
				return false
			}

			// Check if file_name and file_path exist and are not empty
			fileName, fileNameOk := fd["file_name"].(string)
			filePath, filePathOk := fd["file_path"].(string)

			if !fileNameOk || fileName == "" || !filePathOk || filePath == "" {
				return false
			}

			// Filter out system paths
			if strings.HasPrefix(filePath, "/go/pkg") {
				return false
			}

			if strings.HasPrefix(filePath, "/node_modules/") {
				return false
			}

			// Include the file if its path is mentioned in the query or accept all valid files
			// Changed from requiring the path to be in the query to accepting all valid files
			return true
		})
	}

	sourceUpdates := map[string]any{}
	sourceDetails := map[string]string{}

	// Modified to perform GitHub analysis even when filename information is not available
	if len(meta) > 0 {
		// Create a safe copy of the data to pass to analyzeGithubCode
		githubData := map[string]any{
			"summary": data,
			"errors":  errorLines,
		}

		// Add file details if available
		if len(eventLogFileDetails) > 0 {
			githubData["files"] = eventLogFileDetails
		}

		// Only add GitHub repo and commit if they exist in meta
		if repo, exists := meta["workloads.nudgebee.com/git.repo"]; exists && repo != "" {
			githubData["git_repo"] = repo
		}

		if commit, exists := meta["workloads.nudgebee.com/git.hash"]; exists {
			githubData["git_commit"] = commit
		}

		githubResp, err := l.analyzeGithubCode(ctx, query, query.ConversationId, meta, githubData)
		if err != nil {
			ctx.GetLogger().Error("loganalysis: unable to do github analysis", "error", err)
		}
		sourceUpdates = githubResp
		sourceDetails = meta

	} else {
		ctx.GetLogger().Warn("loganalysis: skipping code analysis", "meta", meta, "files", eventLogFileDetails)
	}

	messageContent := ""

	if eventId == "" {
		// Include sourceUpdates in the message to show diff in code suggestions section
		sourceUpdatesJson, err := common.MarshalJson(sourceUpdates)
		if err != nil {
			ctx.GetLogger().Error("loganalysis: unable to marshal sourceUpdates", "error", err)
			sourceUpdatesJson = []byte("{}")
		}
		// Add sourceUpdates as a structured part of the prompt
		// First format the log analysis prompt with the data
		baseContent := fmt.Sprintf(PROMPT_CHAIN_LOG_ANALYSIS_SYS_MSG, string(data))
		// Then add the source updates information
		messageContent = fmt.Sprintf(`
	%s
	**Code Suggestions**
		Display code suggestions in a format similar to a git diff, with "+" and "-" symbols indicating added and removed lines of code.
		Do not generate this section if there are no githubDiff and return "No code suggestions available".
		Below is the githubDiff information:
		%s\n\n`, baseContent, string(sourceUpdatesJson))
	} else {
		// Format the log analysis prompt with the data - don't include code suggestions here
		// as they will be passed directly in the response
		messageContent = fmt.Sprintf(PROMPT_CHAIN_LOG_ANALYSIS_SYS_MSG, string(data))
	}

	// Custom-planner agents bypass the executor's systemMessage path so the
	// `<skill-lists>` + load_skills mechanism never reaches us. The executor
	// eagerly loads the bodies of the selected mapped KBs into SkillsContext —
	// prepend it so the LLM has the expert guidance ahead of the log data.
	if strings.TrimSpace(query.SkillsContext) != "" {
		messageContent = query.SkillsContext + "\n\n" + messageContent
	}
	messageHistory := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, messageContent),
	}
	token := core.GetLLMNumTokensFromMessages(ctx, messageHistory, provider, model)
	if token > maxTokens {
		return core.NBAgentResponse{}, errors.New("loganalysis: the log data is too large to analyze. Please provide a smaller log data")
	}

	ctx.GetLogger().Debug("Generating log summary")
	completion, err := core.GenerateAndTrackLLMContent(ctx, query.UserId, query.AccountId, query.ConversationId, query.MessageId, query.AgentId, false, messageHistory, true, llms.WithTemperature(0.0))
	if err != nil {
		ctx.GetLogger().Error("loganalysis: unable to generate log summary", "error", err)
		return core.NBAgentResponse{}, err
	}

	if completion == nil || len(completion.Choices) == 0 || strings.TrimSpace(completion.Choices[0].Content) == "" {
		ctx.GetLogger().Error("loganalysis: LLM returned empty response for log summary")
		return core.NBAgentResponse{}, errors.New("loganalysis: LLM returned empty response for log summary")
	}

	logSummary := completion.Choices[0].Content

	response := map[string]any{
		"response":       logSummary,
		"files":          eventLogFileDetails,
		"errors":         errorLines,
		"source_updates": sourceUpdates,
		"source_details": sourceDetails,
	}
	responseBytes, err := common.MarshalJson(response)
	if err != nil {
		return core.NBAgentResponse{}, err
	}

	return core.NBAgentResponse{Response: []string{string(responseBytes)}, Status: core.ConversationStatusCompleted}, nil
}

func (l LogAnalysisAgent) GetSourceCodeAnnotations(ctx *security.RequestContext, request core.NBAgentRequest, k8sInfo []map[string]string, eventId string) (map[string]string, error) {
	if len(k8sInfo) == 0 && eventId == "" {
		return nil, nil
	}

	ctx.GetLogger().Info("Getting source code annotations", "pod_count", len(k8sInfo), "eventId", eventId)

	// Get the database connection
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, err
	}

	// First try to get annotations by eventId if available
	if eventId != "" {
		ctx.GetLogger().Info("Attempting to get annotations using eventId", "eventId", eventId)

		workloadName := request.QueryConfig.Workload
		namespace := request.QueryConfig.Namespace

		annotations, err := services_server.GetSourceCodeAnnotations(ctx, dbManager, request.AccountId, services_server.SourceCodeAnnotationOptions{
			EventId:      eventId,
			WorkloadName: workloadName,
			Namespace:    namespace,
		})
		if err == nil && len(annotations) > 0 {
			ctx.GetLogger().Info("Successfully retrieved annotations using eventId", "count", len(annotations))
			return annotations, nil
		}
		ctx.GetLogger().Info("No annotations found using eventId, falling back to pod/workload names")
	}

	var k8sInfoObjects []map[string]string
	for _, info := range k8sInfo {
		obj := map[string]string{
			"pod_name":      info["pod_name"],
			"namespace":     info["namespace"],
			"workload_name": info["workload_name"],
		}
		k8sInfoObjects = append(k8sInfoObjects, obj)
	}
	for _, i := range k8sInfoObjects {
		annotations, err := services_server.GetSourceCodeAnnotations(ctx, dbManager, request.AccountId, services_server.SourceCodeAnnotationOptions{
			PodName:      i["pod_name"],
			WorkloadName: i["workload_name"],
			Namespace:    i["namespace"],
		})
		if err != nil {
			ctx.GetLogger().Info("Failed to get source code annotations", "error", err, "pod_name", i["pod_name"], "workload_name", i["workload_name"])
		}
		if len(annotations) > 0 && (annotations["workloads.nudgebee.com/git.repo"] != "" || annotations["workloads.nudgebee.com/git.hash"] != "") {
			return annotations, nil
		}
	}
	return nil, nil
}

func (l LogAnalysisAgent) analyzeGithubCode(ctx *security.RequestContext, request core.NBAgentRequest, parentConversationId string, githubInfo map[string]string, queryResponse map[string]any) (map[string]any, error) {
	ctx.GetLogger().Info("analyzer: doing git analysis")
	logAnalysisGithub := LogGithubAgent{}

	if githubInfo == nil {
		ctx.GetLogger().Info("analyzer: githubInfo is nil, skip github analysis")
		return map[string]any{}, nil
	}

	repo, repoExists := githubInfo["workloads.nudgebee.com/git.repo"]
	if !repoExists || repo == "" {
		ctx.GetLogger().Info("analyzer: no github config, skip github analysis")
		return map[string]any{}, nil
	}

	commit, commitExists := githubInfo["workloads.nudgebee.com/git.hash"]
	if !commitExists {
		commit = ""
	}

	if queryResponse["errors"] == nil {
		ctx.GetLogger().Info("analyzer: no errors on log response, skip github analysis")
		return map[string]any{}, nil
	}

	errors := []string{}
	if errors1, ok := queryResponse["errors"].([]string); ok {
		if len(errors1) == 0 {
			ctx.GetLogger().Info("analyzer: no errors on log response, skip github analysis")
			return map[string]any{}, nil
		}
		errors = errors1
	} else if errors1, ok := queryResponse["errors"].([]any); ok {
		if len(errors1) == 0 {
			ctx.GetLogger().Info("analyzer: no errors on log response, skip github analysis")
			return map[string]any{}, nil
		}
		for _, e := range errors1 {
			if eStr, ok := e.(string); ok {
				errors = append(errors, eStr)
			}
		}
	}

	filesMap := []map[string]any{}
	if filesMap1, ok := queryResponse["files"].([]map[string]any); ok {
		filesMap = filesMap1
	} else if filesMap1, ok := queryResponse["files"].([]any); ok {
		for _, f := range filesMap1 {
			if fMap, ok := f.(map[string]any); ok {
				filesMap = append(filesMap, fMap)
			}
		}
	}

	// Log if no files were found, but continue with analysis anyway
	if len(filesMap) == 0 {
		ctx.GetLogger().Info("analyzer: no files found in log response, continuing with github analysis anyway")
	}

	logGithubRequest := LogGithubAgentRequest{
		Query:     prompts_repo.GetPrompt(prompts_repo.PromptLogAnalysisCodeDiffGenerateRequest),
		Errors:    errors,
		Files:     filesMap,
		GitRepo:   repo,
		GitCommit: commit,
	}

	logGithubRequestJson, err := common.MarshalJson(logGithubRequest)
	if err != nil {
		ctx.GetLogger().Error("analyzer: unable to serialize json", "error", err)
		return map[string]any{}, err
	}

	resp, err := core.HandleConversationSessionRequest(
		ctx,
		logAnalysisGithub,
		request.UserId,
		request.AccountId,
		parentConversationId,
		string(logGithubRequestJson),
		core.ConversationSessionRequestWithSource(core.ConversationSourceInvestigation),
	)

	if err != nil {
		ctx.GetLogger().Error("analyzer: unable to get github analysis", "error", err, "accountId", request.AccountId, "userId", request.UserId)
		return map[string]any{}, err
	}

	if len(resp.Response) == 0 {
		ctx.GetLogger().Info("analyzer: github analysis resulted in empty response")
		return map[string]any{}, nil
	}

	// tryParseJSON attempts multiple strategies to parse potentially malformed JSON

	githubDiffResp, err := tryParseJSON(resp.Response[0])
	if err != nil {
		ctx.GetLogger().Info("analyzer: failed to parse GitHub analysis response",
			"error", err,
			"response_length", len(resp.Response[0]))

		return map[string]any{}, nil
	}

	return githubDiffResp, nil
}

// tryParseJSON attempts multiple strategies to parse potentially malformed JSON
func tryParseJSON(data string) (map[string]any, error) {
	var result map[string]any

	// Try direct unmarshal first
	if err := common.UnmarshalJson([]byte(data), &result); err == nil {
		return result, nil
	}

	// Try to handle the case where the string might be double-encoded
	var str string
	if err := common.UnmarshalJson([]byte(data), &str); err == nil {
		// If we successfully unmarshaled a string, try to unmarshal that string
		if err := common.UnmarshalJson([]byte(str), &result); err == nil {
			return result, nil
		}
	}

	return nil, fmt.Errorf("all parsing attempts failed")
}

// truncateText splits text by newlines and truncates to fit within maxTokens.
func (l LogAnalysisAgent) truncateText(context *security.RequestContext, text string, maxTokens int, provider string, model string) string {
	lines := strings.Split(text, "\n")
	ctx := &TruncateContext{
		lines:     lines,
		model:     model,
		provider:  provider,
		maxTokens: maxTokens,
	}
	l.truncateToMaxTokens(context, ctx)
	return strings.Join(ctx.lines[:ctx.truncateIndex], "\n")
}

// truncateToMaxTokens truncates lines sequentially to fit within maxTokens.
func (l LogAnalysisAgent) truncateToMaxTokens(context *security.RequestContext, ctx *TruncateContext) {
	batchSize := 50
	totalTokens := 0

	for i := 0; i < len(ctx.lines); i += batchSize {
		end := i + batchSize
		if end > len(ctx.lines) {
			end = len(ctx.lines)
		}

		batchTokens := core.GetLLMNumTokensFromStringMessages(context, ctx.lines[i:end], ctx.provider, ctx.model)

		if totalTokens+batchTokens <= ctx.maxTokens {
			totalTokens += batchTokens
			ctx.truncateIndex = end
			continue
		}

		// This batch exceeds the limit — process lines individually
		for j := i; j < end; j++ {
			lineTokens := core.GetLLMNumTokensFromStringMessages(context, []string{ctx.lines[j]}, ctx.provider, ctx.model)
			if totalTokens+lineTokens > ctx.maxTokens {
				ctx.truncateIndex = j
				context.GetLogger().Debug("Truncated log data", "lines_kept", j, "total_lines", len(ctx.lines), "tokens_used", totalTokens)
				return
			}
			totalTokens += lineTokens
		}
		ctx.truncateIndex = end
	}

	context.GetLogger().Debug("No truncation needed", "lines", len(ctx.lines), "tokens", totalTokens)
}

// TruncateContext holds the context needed for the truncation process.
type TruncateContext struct {
	lines         []string
	model         string
	provider      string
	maxTokens     int
	truncateIndex int
}
