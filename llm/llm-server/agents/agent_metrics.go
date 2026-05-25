package agents

import (
	"fmt"
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"slices"
	"strings"
	"time"
)

const MetricsAgentName = "metrics"

func init() {
	core.RegisterNBAgentFactory(MetricsAgentName, func(accountId string) (core.NBAgent, error) {
		return getMetricsAgent(security.NewRequestContextForSuperAdmin(), accountId)
	})
	toolcore.RegisterNBToolFactory(MetricsAgentName, func(accountId string) (toolcore.NBTool, error) {
		return MetricsAgentTool{}, nil
	})
}

func newMetricsAgent(ctx *security.RequestContext, accountId string, provider services_server.ObservabilityProvider) core.NBAgent {
	var primaryAgent core.NBAgent

	switch strings.ToLower(provider.Provider) {
	case "datadog":
		if metricsAgent, ok := core.GetNBAgent(ctx, "datadog_metrics", accountId, ""); ok {
			primaryAgent = metricsAgent
		} else {
			slog.Warn("metrics: datadog metrics agent not found, falling back to prometheus", "accountId", accountId)
		}
	case "elasticsearch", "opensearch", "es":
		if esMetricsAgent, ok := core.GetNBAgent(ctx, ElasticSearchMetricsAgentName, accountId, ""); ok {
			primaryAgent = esMetricsAgent
		} else {
			slog.Warn("metrics: elasticsearch metrics agent not found, falling back to prometheus", "accountId", accountId)
		}
	}

	if primaryAgent == nil {
		primaryAgent = &PrometheusAgent{accountId: accountId}
	}

	return &metricsAgent{
		accountId: accountId,
		agent:     primaryAgent,
	}
}

type metricsAgent struct {
	accountId string
	agent     core.NBAgent
}

func (f *metricsAgent) GetName() string {
	return MetricsAgentName
}

func (f *metricsAgent) GetNameAliases() []string {
	return []string{"Metrics"}
}

func (f *metricsAgent) GetDescription() string {
	return `Retrieves, analyzes, and visualizes metrics from various sources (Kubernetes, Prometheus, Datadog) by translating natural language questions into metrics queries. Use this for: CPU/memory/network utilization, performance trends, SLO/SLA data, threshold breaches, custom metric queries, and generating charts or graphs. Do NOT use for: log analysis (use ` + "`" + `logs` + "`" + ` agent instead), Kubernetes resource state inspection (use ` + "`" + `kubectl` + "`" + ` or ` + "`" + `kubectl_execute` + "`" + `), or querying Kubernetes events (use ` + "`" + `events` + "`" + ` agent).`
}

func (f *metricsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{
		Role:         "an expert in retrieving metrics from various sources.",
		Instructions: []string{"Retrieve metrics using the configured metrics source."},
	}
}

func (f *metricsAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return f.agent.GetSupportedTools(ctx)
}

func (f *metricsAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

func (f *metricsAgent) Execute(ctx *security.RequestContext, query core.NBAgentRequest) (core.NBAgentResponse, error) {
	nbRequestContext := toolcore.NbToolContext{
		Ctx:            ctx,
		AccountId:      f.accountId,
		ConversationId: query.ConversationId,
		ParentAgentId:  query.ParentAgentId,
		MessageId:      query.MessageId,
		QueryContext:   query.QueryContext,
		QueryConfig:    query.QueryConfig,
		UserId:         query.UserId,
		// Tell the underlying provider agent (Prometheus, Datadog, ...) — which
		// is a normal ReAct agent and is the one actually running an LLM — to
		// also surface KBs the user mapped to "metrics" via its lazy
		// <skill-lists> + load_skills flow.
		InheritSkillsFromAgents: append(query.InheritSkillsFromAgents, f.GetName()),
		OriginalQuery:           query.OriginalQuery,
		SelectedSkillIds:        query.SelectedSkillIds,
	}

	nbToolCallRequest := toolcore.NBToolCallRequest{}
	nbToolCallRequest.Command = query.Query
	nbToolCallRequest.Context = query.QueryContext
	nbToolCallRequest.Arguments = map[string]any{}

	return core.ExecuteAgentToolCall(nbRequestContext, f.agent, nbToolCallRequest)
}

func getMetricsAgent(ctx *security.RequestContext, accountId string) (core.NBAgent, error) {
	metricsConnectionProvider, err := tools.GetMetricsProvider(accountId)
	if err != nil {
		return nil, err
	}
	return newMetricsAgent(ctx, accountId, metricsConnectionProvider), nil
}

type MetricsAgentTool struct {
}

func (m MetricsAgentTool) Name() string {
	return MetricsAgentName
}

func (m MetricsAgentTool) GetType() toolcore.NBToolType {
	return toolcore.NBToolTypeAgent
}

func (m MetricsAgentTool) Description() string {
	return `Retrieves, analyzes, and visualizes metrics from various sources (Kubernetes, Prometheus, Datadog) by translating natural language questions into metrics queries. Use this for: CPU/memory/network utilization, performance trends, SLO/SLA data, threshold breaches, custom metric queries, and generating charts or graphs. Do NOT use for: log analysis (use ` + "`" + `logs` + "`" + ` agent instead), Kubernetes resource state inspection (use ` + "`" + `kubectl` + "`" + ` or ` + "`" + `kubectl_execute` + "`" + `), or querying Kubernetes events (use ` + "`" + `events` + "`" + ` agent). Returns metrics data, summaries, and visualizations based on your query.

	Usage:

	* Input: Provide a question in natural language to search, filter, troubleshoot, or visualize metrics.
	* Output: Returns metrics data, summaries, and visualizations based on your query.
	`
}

func (m MetricsAgentTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"command": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: "Metrics Query Question",
			},
			"output_file": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: "Optional: Path in the workspace to save the raw metrics data (e.g., 'metrics.json'). Relative paths are saved in the conversation directory.",
			},
		},
		Required: []string{"command"},
	}
}

func (m MetricsAgentTool) Call(nbRequestContext toolcore.NbToolContext, input toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	agent, err := getMetricsAgent(nbRequestContext.Ctx, nbRequestContext.AccountId)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Info("metrics: unable to get metricsAgent", "error", err.Error())
		return toolcore.NBToolResponse{}, err
	}

	resp, err := core.ExecuteAgentToolCall(nbRequestContext, agent, input)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("metrics: unable to process events request", "error", err, "input", input)
		return toolcore.NBToolResponse{}, err
	}

	if len(resp.Response) > 0 {
		metricData := resp.Response[0]
		var references []toolcore.NBToolResponseReference

		// Determine if we should save to file (Explicit request OR Automatic overflow protection)
		outputFile, _ := input.Arguments["output_file"].(string)
		if outputFile != "" {
			outputFile = common.SanitizePath(outputFile)
		}
		shouldSave := false

		if config.Config.LlmServerShellToolEnabled {
			if outputFile != "" {
				shouldSave = true
			} else if len(metricData) > 2000 {
				shouldSave = true
				outputFile = fmt.Sprintf("metrics_%d.txt", time.Now().UnixNano()) // Metrics often JSON/Text
			}
		}

		if shouldSave {
			wm := workspace.NewWorkspaceManager()
			err := wm.SaveFile(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, outputFile, metricData)
			if err != nil {
				nbRequestContext.Ctx.GetLogger().Error("metrics: failed to save metrics to workspace", "error", err, "path", outputFile)
			} else {
				references = append(references, toolcore.NBToolResponseReference{
					Text:        "Raw metrics data saved to workspace",
					Url:         outputFile,
					Type:        "file",
					Description: "Raw metrics data collected by system",
				})

				savedLen := len(metricData)
				metricData = fmt.Sprintf("Output large (%d bytes). Saved to %s.\nPreview: %s", savedLen, outputFile, metricData)
			}
		}

		if _, ok := agent.(core.NBAgentReActPlannerSummaryToolProvider); ok {
			return toolcore.NBToolResponse{
				Data:       metricData,
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
					respData = metricData
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
			Data:       metricData,
			Type:       toolcore.NBToolResponseTypeText,
			Status:     toolcore.NBToolResponseStatusSuccess,
			References: references,
		}, nil
	}

	return toolcore.NBToolResponse{}, toolcore.ErrUnableToFetchData
}
