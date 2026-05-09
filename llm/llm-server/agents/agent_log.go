package agents

import (
	"context"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"
)

const LogsAgentName = "logs"

func init() {
	core.RegisterNBAgentFactory(LogsAgentName, func(accountId string) (core.NBAgent, error) {
		return getLogAgent(security.NewRequestContextForSuperAdmin(), accountId)
	})
	toolcore.RegisterNBToolFactory(LogsAgentName, func(accountId string) (toolcore.NBTool, error) {
		return LogAgentTool{}, nil
	})
}

func LogCalculateTimeWindow(minutes string) (int64, int64) {
	windowInMinutes, err := strconv.Atoi(minutes)
	if err != nil {
		windowInMinutes = 60 * 24
	}
	oneMinuteInSecs := int64(time.Minute)
	windowInSeconds := int64(windowInMinutes) * oneMinuteInSecs
	endTime := time.Now().UTC().UnixNano()
	startTime := endTime - windowInSeconds
	return startTime, endTime
}

const PROMPT_TIME_MINUTES_SYSTEM = `Extract the time range in minutes from the following user question. Convert common time expressions like "one hour" or "30 minutes" into the total number of minutes. If no time range is specified, return 1440.
	Question: "Get me last one hour logs"
	Output: 60
	Question: "Fetch logs from the last 2 hours"
	Output: 120
	Question: "I need the last 30 minutes of data"
	Output: 30
	Question: "I need recent data"
	Output: 1440`

var (
	timeRegexes = []struct {
		re         *regexp.Regexp
		multiplier int
	}{
		{regexp.MustCompile(`(?i)last\s+(\d+)\s*(?:m|min|mins|minute|minutes)\b`), 1},
		{regexp.MustCompile(`(?i)last\s+(\d+)\s*(?:h|hr|hrs|hour|hours)\b`), 60},
		{regexp.MustCompile(`(?i)last\s+(\d+)\s*(?:d|day|days)\b`), 1440},
	}

	// logFailurePatterns indicate actual execution failures that should trigger fallback
	// to the next agent. "No data" results are handled structurally: agent_log_default
	// returns ConversationStatusFailed after exhausting retries, triggering the fallback.
	logFailurePatterns = []string{
		"unable to retrieve",
		"failed to fetch logs",
		"could not retrieve logs",
		"failed to retrieve",
		"failed to execute",
		"error executing",
		"no log data found",
	}

	// nonRetryableLogPatterns are infrastructure error patterns that cannot be fixed
	// by refining the log query. LLM refinement is skipped for these.
	nonRetryableLogPatterns = []string{
		"jwt", "unauthorized", "forbidden", "auth",
		"connection refused", "connection reset",
		"timed out", "timeout", "deadline exceeded",
		"workspace readiness", "workspace not ready",
		"certificate", "tls handshake",
	}
)

// maxShortResponseLen is the max response length for fallback failure-pattern matching.
// Longer responses are trusted as valid LLM summaries.
const maxShortResponseLen = 500

func LogGetTimeRangeFilters(ctx *security.RequestContext, query core.NBAgentRequest) (int64, int64) {
	// Optimization: Try to extract time using regex first
	for _, tr := range timeRegexes {
		matches := tr.re.FindStringSubmatch(query.Query)
		if len(matches) > 1 {
			val, err := strconv.Atoi(matches[1])
			if err == nil {
				return LogCalculateTimeWindow(fmt.Sprintf("%d", val*tr.multiplier))
			}
		}
	}

	timeCtx := security.NewRequestContext(
		context.WithValue(ctx.GetContext(), core.ContextKeyCacheScope, core.CacheScopeGlobal),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	res, err := core.GenerateAndTrackLLMContent(timeCtx, query.UserId, query.AccountId, query.ConversationId, query.MessageId, "logs", false, []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, PROMPT_TIME_MINUTES_SYSTEM),
		llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf("Current Question: %q\nOutput:", query.Query)),
	}, false, llms.WithTemperature(0.0))
	if err != nil {
		ctx.GetLogger().Error("log: unable to generate content", "error", err)
		start, end := LogCalculateTimeWindow("1440")
		return start, end
	}
	if len(res.Choices) == 0 {
		start, end := LogCalculateTimeWindow("1440")
		return start, end
	}
	start, end := LogCalculateTimeWindow(res.Choices[0].Content)
	return start, end
}

func newLogAgent(ctx *security.RequestContext, accountId string, primaryAgent services_server.ObservabilityProvider) core.NBAgent {
	var agentsToTry []core.NBAgent

	// Add primary agent based on configuration
	switch strings.ToLower(primaryAgent.Provider) {
	case "datadog":
		if logAgent, ok := core.GetNBAgent(ctx, "datadog_log", accountId, ""); ok {
			agentsToTry = append(agentsToTry, logAgent)
		}
	default:
		defaultLogsAgent := LogDefaultAgent{accountId: accountId, provider: primaryAgent}
		agentsToTry = append(agentsToTry, &defaultLogsAgent)
	}

	// Always add Kubectl as a fallback
	agentsToTry = append(agentsToTry, newKubectlLogAgent(accountId))

	return &logAgent{
		accountId: accountId,
		agents:    agentsToTry,
	}
}

type logAgent struct {
	accountId string
	agents    []core.NBAgent
}

func (f *logAgent) GetName() string {
	return LogsAgentName
}

func (f *logAgent) GetNameAliases() []string {
	return []string{"Logs"}
}

func (f *logAgent) GetDescription() string {
	return `Retrieves and analyzes logs from various sources (Kubernetes, Loki, Elasticsearch, Datadog) by translating natural language questions into log queries. This agent handles its own resource discovery (e.g., finding the correct pod name or namespace). Use this for: fetching application or container logs, searching log entries by keyword or time range, troubleshooting pod/container errors via log output, and correlating logs across services. Do NOT use for: querying performance metrics (use ` + "`" + `metrics` + "`" + ` agent instead), running kubectl commands (use ` + "`" + `kubectl` + "`" + ` or ` + "`" + `kubectl_execute` + "`" + `), or querying Kubernetes events (use ` + "`" + `events` + "`" + ` agent).`
}

func (f *logAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{
		Role:         "an expert in retrieving logs from various sources with fallback mechanisms.",
		Instructions: []string{"Try to retrieve logs using the primary configured log source. If that fails, attempt to use fallback log sources like kubectl."},
		Constraints:  []string{"Always try the agents in the specified order."},
	}
}

func (f *logAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	supportedTools := []toolcore.NBTool{}
	for _, agent := range f.agents {
		supportedTools = append(supportedTools, agent.GetSupportedTools(ctx)...)
	}
	return supportedTools
}

func (f *logAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

// Execute method for the fallbackLogAgent
func (f *logAgent) Execute(ctx *security.RequestContext, query core.NBAgentRequest) (core.NBAgentResponse, error) {
	var allEfforts []string
	var lastAgentStepResponses []core.ToolInvocation
	var allReferences []toolcore.NBToolResponseReference

	for _, agent := range f.agents {
		nbRequestContext := toolcore.NbToolContext{
			Ctx:            ctx,
			AccountId:      f.accountId,
			ConversationId: query.ConversationId,
			ParentAgentId:  query.ParentAgentId,
			MessageId:      query.MessageId,
			QueryContext:   query.QueryContext,
			QueryConfig:    query.QueryConfig,
			UserId:         query.UserId,
			// Tell downstream fallback agents (LogDefaultAgent, KubectlLogAgent, ...)
			// to also surface KBs the user mapped to "logs" — accumulated alongside
			// any skills inherited from a custom-planner ancestor of "logs" itself.
			InheritSkillsFromAgents: append(query.InheritSkillsFromAgents, f.GetName()),
			OriginalQuery:           query.OriginalQuery,
			SelectedSkillIds:        query.SelectedSkillIds,
		}

		nbToolCallRequest := toolcore.NBToolCallRequest{}
		nbToolCallRequest.Command = query.Query
		nbToolCallRequest.Context = query.QueryContext
		nbToolCallRequest.Arguments = map[string]any{}

		resp, err := core.ExecuteAgentToolCall(nbRequestContext, agent, nbToolCallRequest)
		lastAgentStepResponses = append(lastAgentStepResponses, resp.AgentStepResponse...)
		allReferences = append(allReferences, resp.References...)

		isSuccess := false
		if resp.Status == core.ConversationStatusFailed {
			// Agent explicitly reported failure — always fall through to next agent
			isSuccess = false
		} else if resp.Status == core.ConversationStatusCompleted || resp.Status == core.ConversationStatusWaiting {
			if len(resp.Response) > 0 {
				// Default to success; only override if response contains an execution failure pattern
				isSuccess = true
				if len(resp.Response[0]) < maxShortResponseLen {
					responseLower := strings.ToLower(resp.Response[0])
					for _, pattern := range logFailurePatterns {
						if strings.Contains(responseLower, pattern) {
							isSuccess = false
							break
						}
					}
				}
			}
		}

		if isSuccess {
			return resp, err
		}

		// Capture effort log for failure reporting
		if len(resp.Response) > 0 {
			allEfforts = append(allEfforts, fmt.Sprintf("--- %s Agent Effort ---\n%s", agent.GetName(), resp.Response[0]))
		}

		ctx.GetLogger().Warn("log: agent execution failed, trying next agent", "agent", agent.GetName(), "error", err)
	}

	combinedFailMsg := "All log agents failed to retrieve logs.\n\n" + strings.Join(allEfforts, "\n\n")
	return core.NBAgentResponse{
		Response:          []string{combinedFailMsg},
		Status:            core.ConversationStatusFailed,
		AgentStepResponse: lastAgentStepResponses,
		References:        allReferences,
	}, nil
}

func getLogAgent(ctx *security.RequestContext, accountId string) (core.NBAgent, error) {
	logConnectionProvider, err := tools.GetLogProvider(accountId)
	if err != nil || logConnectionProvider.Provider == "" || logConnectionProvider.Provider == "k8s" {
		ctx.GetLogger().Warn("log: unable to resolve log provider or provider is k8s, falling back directly to kubectl", "error", err, "provider", logConnectionProvider.Provider)
		return newKubectlLogAgent(accountId), nil
	}
	return newLogAgent(ctx, accountId, logConnectionProvider), nil
}

// isRetryableLogQueryError returns true if the error is likely fixable by refining the query.
// Infrastructure errors (auth, timeout, connection) are not retryable via query refinement.
func isRetryableLogQueryError(errMsg string) bool {
	lower := strings.ToLower(errMsg)
	for _, pattern := range nonRetryableLogPatterns {
		if strings.Contains(lower, pattern) {
			return false
		}
	}
	return true
}

type LogAgentTool struct {
}

func (m LogAgentTool) Name() string {
	return LogsAgentName
}

func (m LogAgentTool) GetType() toolcore.NBToolType {
	return toolcore.NBToolTypeAgent
}

func (m LogAgentTool) Description() string {
	return `Retrieves and analyzes logs from various sources (Kubernetes, Loki, Elasticsearch, Datadog) by translating natural language questions into log queries. This tool handles its own resource discovery (e.g., finding the correct pod name or namespace). Use this for: fetching application or container logs, searching log entries by keyword or time range, troubleshooting pod/container errors via log output, and correlating logs across services. Do NOT use for: querying performance metrics (use ` + "`" + `metrics` + "`" + ` agent instead), running kubectl commands (use ` + "`" + `kubectl` + "`" + ` or ` + "`" + `kubectl_execute` + "`" + `), or querying Kubernetes events (use ` + "`" + `events` + "`" + ` agent). Returns log data and summaries based on your query.

	Usage:

	* Input: Provide a question in natural language to search, filter, or troubleshoot logs.
	* Output: Returns log data and summaries based on your query.
	`
}

func (m LogAgentTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"command": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: "Log Query Question",
			},
			"output_file": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: "Optional: Path in the workspace to save the raw log data (e.g., 'logs.json'). Relative paths are saved in the conversation directory.",
			},
		},
		Required: []string{"command"},
	}
}

func (m LogAgentTool) Call(nbRequestContext toolcore.NbToolContext, input toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	agent, err := getLogAgent(nbRequestContext.Ctx, nbRequestContext.AccountId)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Info("log: unable to get logsAgent", "error", err.Error())
		return toolcore.NBToolResponse{}, err
	}

	resp, err := core.ExecuteAgentToolCall(nbRequestContext, agent, input)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("log: unable to process events request", "error", err, "input", input)
		return toolcore.NBToolResponse{}, err
	}

	if len(resp.Response) > 0 {
		logData := resp.Response[0]
		var references []toolcore.NBToolResponseReference

		// Determine if we should save to file (Explicit request OR Automatic overflow protection)
		outputFile, _ := input.Arguments["output_file"].(string)
		if outputFile != "" {
			outputFile = common.SanitizePath(outputFile)
		}
		shouldSave := false

		if config.Config.LlmServerShellToolEnabled {
			if outputFile != "" {
				shouldSave = true // User explicitly asked for file
			} else if len(logData) > 2000 {
				shouldSave = true // Data too large, auto-save
				outputFile = fmt.Sprintf("logs_%d.txt", time.Now().UnixNano())
			}
		}

		if shouldSave {
			wm := workspace.NewWorkspaceManager()
			err := wm.SaveFile(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, outputFile, logData)
			if err != nil {
				nbRequestContext.Ctx.GetLogger().Error("log: failed to save logs to workspace", "error", err, "path", outputFile)
			} else {
				references = append(references, toolcore.NBToolResponseReference{
					Text:        "Raw log data saved to workspace",
					Url:         outputFile,
					Type:        "file",
					Description: "Raw log data collected by system",
				})

				// Pass the full analyzed response as the preview — the sub-agent already
				// returns a synthesized analysis, not raw logs, so truncating it would
				// discard all useful content from the parent planner's scratchpad.
				savedLen := len(logData)
				logData = fmt.Sprintf("Output large (%d bytes). Saved to %s.\nPreview: %s", savedLen, outputFile, logData)
			}
		}

		if _, ok := agent.(core.NBAgentReActPlannerSummaryToolProvider); ok {
			return toolcore.NBToolResponse{
				Data:       logData,
				Type:       toolcore.NBToolResponseTypeText,
				Status:     toolcore.NBToolResponseStatusSuccess,
				References: references,
			}, nil
		}

		slices.Reverse(resp.AgentStepResponse)
		for _, invocation := range resp.AgentStepResponse {
			if invocation.Response.Content != "" {
				// If we saved to a file, we return the summary text instead of the full JSON content
				respData := invocation.Response.Content
				respType := toolcore.NBToolResponseTypeJson
				if len(references) > 0 {
					respData = logData
					respType = toolcore.NBToolResponseTypeText
				}

				return toolcore.NBToolResponse{
					Data:       respData,
					Type:       respType,
					Status:     toolcore.NBToolResponseStatusSuccess,
					References: references,
				}, nil
			}
		}
		return toolcore.NBToolResponse{
			Data:       logData,
			Type:       toolcore.NBToolResponseTypeText,
			Status:     toolcore.NBToolResponseStatusSuccess,
			References: references,
		}, nil
	}

	return toolcore.NBToolResponse{}, toolcore.ErrUnableToFetchData
}
