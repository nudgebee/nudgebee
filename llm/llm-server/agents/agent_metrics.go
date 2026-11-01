package agents

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/llm/agents/aws"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
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

	switch strings.ToLower(strings.TrimSpace(provider.Provider)) {
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
	case "splunk_enterprise":
		if splunkMetricsAgent, ok := core.GetNBAgent(ctx, SplunkEnterpriseMetricsAgentName, accountId, ""); ok {
			primaryAgent = splunkMetricsAgent
		} else {
			slog.Warn("metrics: splunk enterprise metrics agent not found, falling back to prometheus", "accountId", accountId)
		}
	case "aws":
		// Cloud-only AWS account with no first-class metrics provider — read CloudWatch
		// metrics via the aws CLI.
		primaryAgent = aws.NewAwsMetricsAgent(accountId)
	case "gcp":
		// Cloud-only GCP account with no first-class metrics provider — read Cloud
		// Monitoring via the gcloud CLI (GetMetricsProvider resolves this from the
		// account's cloud type; see cloudFallbackProvider).
		primaryAgent = newGcpMetricsAgent(accountId)
	case "azure":
		// Cloud-only Azure account with no first-class metrics provider — read Azure
		// Monitor via the az CLI.
		primaryAgent = newAzureMetricsAgent(accountId)
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
	executor  agentExecutorFunc
}

func (f *metricsAgent) GetName() string {
	return MetricsAgentName
}

func (f *metricsAgent) GetNameAliases() []string {
	return []string{"Metrics Analyst", "Metrics"}
}

func (f *metricsAgent) AcceptsExecutionTarget() {}

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
		ParentAgentId:  nestedAgentParentID(query),
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
		OriginalQueryFromUser:   query.OriginalQueryFromUser,
		SelectedSkillIds:        query.SelectedSkillIds,
		KnowledgePolicy:         string(query.KnowledgePolicy),
		KnowledgePolicyResolved: query.KnowledgePolicyResolved,
	}

	nbToolCallRequest := toolcore.NBToolCallRequest{}
	nbToolCallRequest.Command = query.Query
	nbToolCallRequest.Context = query.QueryContext
	nbToolCallRequest.Arguments = map[string]any{}

	executor := f.executor
	if executor == nil {
		executor = core.ExecuteAgentToolCall
	}
	return executor(nbRequestContext, f.agent, nbToolCallRequest)
}

func getMetricsAgent(ctx *security.RequestContext, accountId string) (core.NBAgent, error) {
	metricsConnectionProvider, err := tools.GetMetricsProvider(accountId)
	if err != nil {
		return nil, err
	}
	return newMetricsAgent(ctx, accountId, metricsConnectionProvider), nil
}

const MetricsQueryAgentName = "metrics_query"

// newMetricsQueryAgent is the generation-only twin of newMetricsAgent: same provider
// switch, but every arm returns an agent that WRITES a query instead of running one.
//
// It backs the metrics tab's "AI" box, which needs a query to put in the editor. The
// executing agents cannot serve that: they return an SRE prose report (the ES one is
// explicitly told never to reveal its DSL) and reaching it costs a full ReAct loop —
// one traced ES run spent 50s over six queries. Before this existed the endpoint
// hardcoded PrometheusAgent, so every non-Prometheus account was handed PromQL for an
// editor that speaks something else.
//
// Provider strings are matched exactly as newMetricsAgent matches them, so the two
// routers cannot disagree about which backend an account is on.
func newMetricsQueryAgent(ctx *security.RequestContext, accountId, requestedIndex string, provider services_server.ObservabilityProvider) (core.NBAgent, error) {
	providerName := strings.ToLower(strings.TrimSpace(provider.Provider))

	var writer core.NBAgent
	switch providerName {
	case "elasticsearch", "opensearch", "es":
		writer = NewESMetricsQueryAgent(accountId, requestedIndex)
	case "datadog":
		if ddAgent, ok := core.GetNBAgent(ctx, DatadogMetricsQueryAgentName, accountId, ""); ok {
			writer = ddAgent
		} else {
			slog.Warn("metrics_query: datadog metrics query agent not found", "accountId", accountId)
		}
	case "aws", "gcp", "azure":
		// Cloud-only accounts read metrics through a CLI agent (aws/gcloud/az). There
		// is no query language behind the metrics editor for them, so generating one
		// would produce a string the Code tab can never run. Say so instead.
		return nil, fmt.Errorf("AI query generation is not supported for the %q metrics provider — it has no query editor language", providerName)
	}

	if writer == nil {
		// Prometheus, VictoriaMetrics and Chronosphere all speak PromQL, and
		// promql_query is already a generation-only agent with a strict
		// {"promql": ...} contract. It is also the right default for an account
		// whose provider we could not resolve — matching newMetricsAgent's default.
		promqlAgent, ok := core.GetNBAgent(ctx, PromqlAgentName, accountId, "")
		if !ok {
			return nil, fmt.Errorf("metrics_query: promql query agent not registered")
		}
		writer = promqlAgent
	}

	return &metricsQueryAgent{
		accountId: accountId,
		provider:  provider.Provider,
		agent:     writer,
	}, nil
}

// NewMetricsQueryAgent resolves the account's metrics provider and returns the
// generation-only agent for it. requestedIndex is the caller's Elasticsearch index
// selection and is ignored by providers with no index concept.
func NewMetricsQueryAgent(ctx *security.RequestContext, accountId, requestedIndex string) (core.NBAgent, error) {
	metricsConnectionProvider, err := tools.GetMetricsProvider(accountId)
	if err != nil {
		return nil, fmt.Errorf("metrics_query: resolve metrics provider: %w", err)
	}
	return newMetricsQueryAgent(ctx, accountId, requestedIndex, metricsConnectionProvider)
}

// metricsQueryAgent runs the selected query writer and normalises its answer into one
// envelope. The writers predate this wrapper and each answer in their own shape —
// promql_query returns {"promql": ...}, datadog_metrics_query returns a bare query
// string, ESMetricsQueryAgent returns {query, provider, index}. Normalising here means
// the caller has a single contract regardless of provider.
type metricsQueryAgent struct {
	accountId string
	provider  string
	agent     core.NBAgent
	executor  agentExecutorFunc
}

func (m *metricsQueryAgent) GetName() string { return MetricsQueryAgentName }

func (m *metricsQueryAgent) GetNameAliases() []string { return []string{"Metrics Query"} }

func (m *metricsQueryAgent) GetDescription() string {
	return `Generates a single provider-native metrics query from a natural-language question, for whichever metrics backend the account is on. Generation only — it never executes the query.`
}

func (m *metricsQueryAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{
		Role: "an SRE expert that writes one provider-native metrics query from natural language",
	}
}

func (m *metricsQueryAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return m.agent.GetSupportedTools(ctx)
}

func (m *metricsQueryAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

// metricsQueryResult is the response envelope. It matches logQueryResult
// (agent_log_query.go) field-for-field so a caller handling generated queries has one
// shape across logs and metrics rather than two.
type metricsQueryResult struct {
	Query    string `json:"query"`
	Provider string `json:"provider"`
	// Index is the Elasticsearch index the query is meant to run against. Empty for
	// backends with no index concept.
	Index string `json:"index,omitempty"`
}

func (m *metricsQueryAgent) Execute(ctx *security.RequestContext, query core.NBAgentRequest) (core.NBAgentResponse, error) {
	nbRequestContext := toolcore.NbToolContext{
		Ctx:                     ctx,
		AccountId:               m.accountId,
		ConversationId:          query.ConversationId,
		ParentAgentId:           nestedAgentParentID(query),
		MessageId:               query.MessageId,
		QueryContext:            query.QueryContext,
		QueryConfig:             query.QueryConfig,
		UserId:                  query.UserId,
		InheritSkillsFromAgents: append(query.InheritSkillsFromAgents, m.GetName()),
		OriginalQuery:           query.OriginalQuery,
		OriginalQueryFromUser:   query.OriginalQueryFromUser,
		SelectedSkillIds:        query.SelectedSkillIds,
		KnowledgePolicy:         string(query.KnowledgePolicy),
		KnowledgePolicyResolved: query.KnowledgePolicyResolved,
	}

	nbToolCallRequest := toolcore.NBToolCallRequest{}
	nbToolCallRequest.Command = query.Query
	nbToolCallRequest.Context = query.QueryContext
	nbToolCallRequest.Arguments = map[string]any{}

	executor := m.executor
	if executor == nil {
		executor = core.ExecuteAgentToolCall
	}
	resp, err := executor(nbRequestContext, m.agent, nbToolCallRequest)
	if err != nil {
		return errorResponse(m.GetName(), fmt.Errorf("metrics query generation: %w", err)), nil
	}
	if len(resp.Response) == 0 {
		return errorResponse(m.GetName(), fmt.Errorf("metrics query generation: %s returned no answer", m.agent.GetName())), nil
	}

	result, err := normaliseGeneratedMetricsQuery(m.provider, resp.Response[0])
	if err != nil {
		return errorResponse(m.GetName(), err), nil
	}

	data, err := common.MarshalJson(result)
	if err != nil {
		return errorResponse(m.GetName(), fmt.Errorf("metrics query generation: marshal result: %w", err)), nil
	}

	return core.NBAgentResponse{
		AgentName: m.GetName(),
		Response:  []string{string(data)},
		Status:    core.ConversationStatusCompleted,
	}, nil
}

// normaliseGeneratedMetricsQuery folds each writer's own answer shape into
// metricsQueryResult.
//
// It never returns an empty query with a nil error: an empty editor box after a
// spinner reads to the user as "the AI is broken" with nothing to act on, so an
// unusable answer must surface as an error the caller can show.
func normaliseGeneratedMetricsQuery(provider, raw string) (metricsQueryResult, error) {
	trimmed := stripQueryCodeFence(strings.TrimSpace(raw))
	if trimmed == "" {
		return metricsQueryResult{}, fmt.Errorf("metrics query generation: empty response")
	}

	result := metricsQueryResult{Provider: provider}

	// ESMetricsQueryAgent already answers in the target shape; promql_query answers
	// {"promql": "..."} or {"promql": ["q1","q2"]}. Both are JSON objects, so one
	// decode covers them and anything else falls through to the bare-string case.
	var envelope struct {
		Query    string          `json:"query"`
		Index    string          `json:"index"`
		Provider string          `json:"provider"`
		Promql   json.RawMessage `json:"promql"`
	}
	if err := json.Unmarshal([]byte(trimmed), &envelope); err == nil {
		if envelope.Provider != "" {
			result.Provider = envelope.Provider
		}
		result.Index = envelope.Index

		if strings.TrimSpace(envelope.Query) != "" {
			result.Query = envelope.Query
			return result, nil
		}
		if len(envelope.Promql) > 0 {
			promql, perr := firstPromqlQuery(envelope.Promql)
			if perr != nil {
				return metricsQueryResult{}, perr
			}
			result.Query = promql
			return result, nil
		}
		// A JSON object carrying neither key is not a query. Falling through here
		// would put a raw JSON blob in the user's PromQL box.
		return metricsQueryResult{}, fmt.Errorf("metrics query generation: response had no query (%s)", truncateString(trimmed, 200))
	}

	// Bare query text — datadog_metrics_query's contract, and the shape any future
	// writer is most likely to use.
	result.Query = trimmed
	return result, nil
}

// firstPromqlQuery reads promql_query's value, which is a string for the single-query
// case and an array when the agent was asked for several. The editor holds one query,
// so the first is the one that matters.
func firstPromqlQuery(rawPromql json.RawMessage) (string, error) {
	var single string
	if err := json.Unmarshal(rawPromql, &single); err == nil {
		if strings.TrimSpace(single) == "" {
			return "", fmt.Errorf("metrics query generation: promql_query returned an empty query")
		}
		return single, nil
	}

	var many []string
	if err := json.Unmarshal(rawPromql, &many); err == nil {
		for _, q := range many {
			if strings.TrimSpace(q) != "" {
				return q, nil
			}
		}
	}
	return "", fmt.Errorf("metrics query generation: could not read a query out of promql_query's response")
}

// buildLargeMetricsMessage builds the truncation notice returned to whichever
// orchestrator invoked the metrics tool. The sub-agent's own ReAct loop has
// already finished by this point (MetricsAgentTool.Call reaches this after
// ExecuteAgentToolCall returns), so this message — not a sub-agent prompt — is
// what the calling agent actually observes.
//
// The saved file holds the sub-agent's own answer text, not raw per-datapoint
// data: ExecuteAgentToolCall's nested-call path never invokes the top-level
// conversation flow that runs PostProcessResponse (see PrometheusAgent's
// override in agent_prometheus.go — it's wired up but only fires when
// Prometheus is the top-level agent for a whole conversation, e.g. a direct
// `@prometheus` invocation, never through this wrapper). So the guidance here
// is plain-text search, not a specific data shape, and applies the same way
// regardless of which provider (Prometheus, Datadog, ...) is behind the call.
func buildLargeMetricsMessage(savedLen int, outputFile, preview string) string {
	return fmt.Sprintf("Output large (%d bytes). Saved to %s. Use shell_execute to search it (e.g. grep -n \"<keyword or timestamp>\" %s | head -20) instead of re-querying with a narrower time range.\nPreview: %s", savedLen, outputFile, outputFile, preview)
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
			"target": toolcore.AgentTargetSchemaProperty(),
		},
		Required: []string{"command"},
	}
}

func (m MetricsAgentTool) PrepareDelegatedTargetInput(target *toolcore.ExecutionTarget, input string) (string, error) {
	return tools.AddDelegatedTargetObject(input, target)
}

func (m MetricsAgentTool) Call(nbRequestContext toolcore.NbToolContext, input toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	target, queryAccountId, err := tools.ResolveObservabilityTarget(nbRequestContext, input)
	if err != nil {
		return toolcore.NBToolResponse{
			Status: toolcore.NBToolResponseStatusError,
			Data:   err.Error(),
		}, nil
	}
	if target != nil {
		nbRequestContext.QueryConfig.DelegatedTarget = target
		// The sub-agent writes its own query from the command text, which may
		// still name the target; tell it the target is already the scope.
		input.Context = strings.TrimSpace(input.Context + "\n" + target.ScopeNote())
	}

	provider, err := tools.GetMetricsProvider(queryAccountId)
	if target != nil {
		if err != nil || provider.Provider == "" {
			return toolcore.NBToolResponse{
				Status: toolcore.NBToolResponseStatusError,
				Data:   fmt.Sprintf("no metrics provider configured for target %s %q (account %s); will not fall back to conversation origin account", target.Provider, target.Name, queryAccountId),
			}, nil
		}
		if tools.IsCloudObservabilityProvider(target.Provider) {
			if !strings.EqualFold(provider.Provider, target.Provider) && !tools.HasActiveCloudProvider(queryAccountId, target.Provider) {
				return toolcore.NBToolResponse{
					Status: toolcore.NBToolResponseStatusError,
					Data:   fmt.Sprintf("target %s %q requires a %s metrics provider, but found %q; will not fall back to conversation origin account", target.Provider, target.Name, target.Provider, provider.Provider),
				}, nil
			}
		}
	}

	agent, err := getMetricsAgent(nbRequestContext.Ctx, queryAccountId)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Info("metrics: unable to get metricsAgent", "error", err.Error())
		return toolcore.NBToolResponse{}, err
	}

	resp, err := core.ExecuteAgentToolCall(nbRequestContext, agent, input)

	preambleResponse, preambleErr, handled := handleNestedAgentCallPreamble(nbRequestContext, resp, err, MetricsAgentName)
	if preambleErr != nil {
		nbRequestContext.Ctx.GetLogger().Error("metrics: unable to process events request", "error", err, "input", input)
		return preambleResponse, preambleErr
	}
	if handled {
		return preambleResponse, nil
	}
	additionalDetails := preambleResponse.AdditionalDetails

	if len(resp.Response) > 0 {
		metricData := resp.Response[0]
		var references []toolcore.NBToolResponseReference

		// Determine if we should save to file (Explicit request OR Automatic overflow protection)
		outputFile, _ := input.Arguments["output_file"].(string)
		if outputFile != "" {
			outputFile = common.SanitizePath(outputFile)
		}
		shouldSave := false

		if outputFile != "" {
			shouldSave = true
		} else if len(metricData) > 2000 {
			shouldSave = true
			outputFile = fmt.Sprintf("metrics_%d.txt", time.Now().UnixNano()) // Metrics often JSON/Text
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

				metricData = buildLargeMetricsMessage(len(metricData), outputFile, metricData)
			}
		}

		// Build the bounded evidence manifest from the chronological step order —
		// this bespoke wrapper bypasses factory_agent's generic path, so without
		// this the `metrics` sub-agent drops its evidence at the tool boundary.
		subAgentEvidence := core.BuildSubAgentEvidenceForTool(nbRequestContext.Ctx, MetricsAgentName, resp.AgentStepResponse)

		if _, ok := agent.(core.NBAgentReActPlannerSummaryToolProvider); ok {
			return toolcore.NBToolResponse{
				Data:              metricData,
				Type:              toolcore.NBToolResponseTypeText,
				Status:            toolcore.NBToolResponseStatusSuccess,
				References:        references,
				SubAgentEvidence:  subAgentEvidence,
				AdditionalDetails: additionalDetails,
			}, nil
		}

		if content, status, ok := newestMetricsStep(resp.AgentStepResponse); ok {
			// If we saved to a file, we return the summary text instead of the full JSON content
			respType := toolcore.NBToolResponseTypeJson
			if len(references) > 0 {
				content = metricData
				respType = toolcore.NBToolResponseTypeText
			}
			return toolcore.NBToolResponse{
				Data:              content,
				Type:              respType,
				Status:            status,
				References:        references,
				SubAgentEvidence:  subAgentEvidence,
				AdditionalDetails: additionalDetails,
			}, nil
		}
		return toolcore.NBToolResponse{
			Data:              metricData,
			Type:              toolcore.NBToolResponseTypeText,
			Status:            toolcore.NBToolResponseStatusSuccess,
			References:        references,
			SubAgentEvidence:  subAgentEvidence,
			AdditionalDetails: additionalDetails,
		}, nil
	}

	return toolcore.NBToolResponse{AdditionalDetails: additionalDetails, References: resp.References}, toolcore.ErrUnableToFetchData
}

// newestMetricsStep returns the sub-agent's newest non-empty step, which the metrics
// tool forwards as its data; a failed step's content is error text -> reported as Error.
func newestMetricsStep(steps []core.ToolInvocation) (string, toolcore.NBToolResponseStatus, bool) {
	for i := len(steps) - 1; i >= 0; i-- {
		if steps[i].Response.Content == "" {
			continue
		}
		if steps[i].Failed {
			return steps[i].Response.Content, toolcore.NBToolResponseStatusError, true
		}
		return steps[i].Response.Content, toolcore.NBToolResponseStatusSuccess, true
	}
	return "", "", false
}
