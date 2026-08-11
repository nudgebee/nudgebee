package agents

import (
	"errors"
	"fmt"
	"nudgebee/llm/agents/aws"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"strings"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

func init() {
}

type RouterAgent struct {
}

func (l RouterAgent) GetName() string {
	return core.RouterAgentName
}

func (l RouterAgent) GetNameAliases() []string {
	return []string{"Router"}
}

func (l RouterAgent) GetDescription() string {
	return `Routes user queries to the most appropriate agent based on the nature of their query.`
}

func (l RouterAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (l RouterAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**Purpose:** You are an intelligent assistant responsible for routing user questions to the most appropriate agent based on the nature of their query.",
		"**Direct Agent Mentions:** If a question directly mentions an agent using @<agent_name>, return <agent_name> directly.",
		"**Category Matching:** Analyze the user's query and match it to one of the defined agent categories based on keywords and descriptions.",
		"**Ambiguity Handling:** If a query is ambiguous and does not clearly match a specific category, return 'InvestigateAgent'.",
		"**Common Agents and Categories:**",
		"	- **InvestigateAgent:** Handles questions related to SRE/DevOps/Promgramming/Software Development.",
		"		- Keywords: Kubernetes, Helm, Prometheus, Postgres, Security, Logs, Errors, Investigate, Docker, Mysql, Code.",
		"	- **UnifiedSearchAgent:** Handles questions about Nudgebee product features, installation, integrations, usage, and general documentation queries across all indexed sources (Confluence, ServiceNow, uploaded KBs, Nudgebee product docs, and the web).",
		"		- Keywords: nudgebee, nubi, how to use, setup, configure, feature, integration, nudgebee docs, product help.",
		"	- **FinOpsAgent:** Handles questions about cloud cost, spend, savings, budgets, optimization opportunities, rightsizing financial impact, idle/unattached resources, cost anomalies, and commitment coverage.",
		"		- Keywords: cost, spend, spending, bill, budget, savings, save, expensive, cheaper, optimize cost, idle, unattached, orphaned, rightsizing, waste, wasted, finops, commitment, reserved instance, savings plan, CUD, SUD, monthly cost, run-rate.",
		"	- **automation:** Handles requests to create, list, trigger, or manage automations and workflows. Only route here when the user explicitly wants to build, edit, or manage an automation/workflow — NOT when the query merely mentions a CI/CD workflow or GitHub Actions run in passing.",
		"		- Keywords: create automation, build workflow, list automations, trigger workflow, manage automation.",
		"	- **GeneralAgent:** Handles general questions that do not fit into SRE/DevOps/Programming.",
		"**Response Format:**",
		"	- Return only the name of the agent without any other trailing or leading characters.",
		"**Context Awareness:**",
		"	- Consider the context of the user query and historical data to make the best routing decision.",
		"**Previous Conversation Context:**",
		"{{ .previousConversation }}",
		"**Current Question:**",
		"{{ .currentQuestion }}",
	}

	examples := []core.NBAgentPromptExample{
		{
			Question:    "@logs can you give me recent logs",
			Answer:      "logsagent",
			Explanation: `Matches "logs" agent as its explicitly mentioned using @logs.`,
		},
		{
			Question:    "@prometheus can you give me recent memory usage",
			Answer:      "prometheus",
			Explanation: `Matches "prometheus" agent as its explicitly mentioned using @prometheus.`,
		},
		{
			Question:    "Can you tell me how to deploy a Helm chart?",
			Answer:      "InvestigateAgent",
			Explanation: `Matches InvestigateAgent as fallback.`,
		},
		{
			Question:    "Can you tell me memory usage by Pod.",
			Answer:      "InvestigateAgent",
			Explanation: `Matches InvestigateAgent based on the keywords "memory" and "usage".`,
		},
		{
			Question:    "Can you tell me cpu usage by Pod.",
			Answer:      "InvestigateAgent",
			Explanation: `Matches InvestigateAgent based on the keywords "cpu" and "usage".`,
		},
		{
			Question:    "What are the pods with OOM errors?",
			Answer:      "InvestigateAgent",
			Explanation: `Matches InvestigateAgent based on the keyword "errors" and not having the keyword "log".`,
		},
		{
			Question:    "Show me recent errors on services-server in namespace nudgebee",
			Answer:      "InvestigateAgent",
			Explanation: `Matches InvestigateAgent based on the keyword "errors" and not having the keyword "log".`,
		},
		{
			Question:    "Logs.",
			Answer:      "GeneralAgent",
			Explanation: `Does not clearly match any category even though it has the keyword "logs" without any context.`,
		},
		{
			Question:    "How does the internet work?",
			Answer:      "GeneralAgent",
			Explanation: `Does not clearly match any category.`,
		},
		{
			Question:    "Show me all open tickets assigned to me",
			Answer:      "tickets",
			Explanation: `Matches TicketAgent based on the keyword "tickets" and context about ticket assignment.`,
		},
		{
			Question:    "How does Nudgebee work?",
			Answer:      "UnifiedSearchAgent",
			Explanation: `Matches UnifiedSearchAgent for product-related questions about Nudgebee.`,
		},
		{
			Question:    "How do I set up integrations in Nudgebee?",
			Answer:      "UnifiedSearchAgent",
			Explanation: `Matches UnifiedSearchAgent for questions about Nudgebee setup and configuration.`,
		},
		{
			Question:    "aklasfkldjkfds",
			Answer:      "GeneralAgent",
			Explanation: `Does not clearly match any category and random gibberish input.`,
		},
		{
			Question:    "What's driving our AWS bill this month?",
			Answer:      "finops",
			Explanation: `Matches FinOpsAgent based on the keyword "bill" and cloud cost context.`,
		},
		{
			Question:    "Show me the top savings opportunities",
			Answer:      "finops",
			Explanation: `Matches FinOpsAgent based on the keyword "savings" and optimization context.`,
		},
		{
			Question:    "How much are we spending on EC2?",
			Answer:      "finops",
			Explanation: `Matches FinOpsAgent based on the keyword "spending" and cloud cost context.`,
		},
		{
			Question:    "Why did our cloud bill go up?",
			Answer:      "finops",
			Explanation: `Matches FinOpsAgent — cost and billing questions route to finops even when phrased as an investigation.`,
		},
		{
			Question:    "Create an automation to restart pods when OOM occurs",
			Answer:      "automation",
			Explanation: `Matches automation agent because user explicitly wants to create an automation.`,
		},
		{
			Question:    "Can you resolve the merge conflict and raise a PR? Context: [View Workflow](https://github.com/...actions/runs/123)",
			Answer:      "InvestigateAgent",
			Explanation: `Matches InvestigateAgent because user wants to resolve a conflict, not build an automation. The word "Workflow" appears only in a GitHub Actions link context.`,
		},
	}

	plannerAgentName := GetDebugAgentName(query.AccountId)

	constrains := []string{
		fmt.Sprintf("- If user is asking for investigation, troubleshooting, optimization etc, then always prefer '%s' agent — unless the question is about cloud cost, spend, billing, budgets, or savings", plannerAgentName),
		fmt.Sprintf("- Questions about cloud cost, spend, billing, budgets, or savings always route to '%s', even when phrased as an investigation (e.g. \"why did our cloud bill go up?\")", FinOpsAgentName),
		"- if user question is not related to sre/devops/software development domains, then use generalagent",
	}

	return core.NBAgentPrompt{
		Role:         "an intelligent assistant capable of routing user questions to the most appropriate agent based on the nature of their query.",
		Instructions: instructions,
		Examples:     examples,
		Constraints:  constrains,
		OutputFormat: "you MUST Return only the name of the agent without any other reasoning, explanation, trailing or leading characters.. For example : SecurityAgent",
		Variables:    []string{"previousConversation", "currentQuestion", "agents"},
	}
}

func (l RouterAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

func (l RouterAgent) Execute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {

	plannerAgent := GetDebugAgentName(request.AccountId)
	// check for @<agent> in the start and return agent
	// observed during testing, that pure llm solution failes sometimes because of history && other data
	// Explicit @-invocation is honoured verbatim — the K8s-native upgrade below
	// deliberately does not fire here so a user who typed @k8s_orchestrator gets
	// the standard variant even when the mode signal is on.
	// Only the FIRST mention routes ("@a @b q" -> a); trailing punctuation isn't
	// part of the name ("@a, q" -> a). See common.ParseAgentMention.
	if name, _ := common.ParseAgentMention(request.Query); name != "" {
		return core.NBAgentResponse{Response: []string{name}}, nil
	}

	// try to route to previous agent if its not router agent
	chatHistory, err := core.GetConversationDao().LoadConversationMessages(request.AccountId, request.ConversationId, request.UserId, core.MessageTypeRoute, 1)
	if err != nil {
		ctx.GetLogger().Error("router: unable to load chat history", "error", err)
		return core.NBAgentResponse{Response: nil}, err
	}
	if len(chatHistory) > 0 && chatHistory[0]["response"] != "" {
		previousRouterAgent := strings.TrimSpace(chatHistory[0]["response"])
		if previousRouterAgent != "" && previousRouterAgent != core.RouterAgentName {
			plannerAgent = previousRouterAgent
		}
	}
	// Normalize into a SEPARATE local for the K8s variant check + override
	// comparison — do NOT mutate plannerAgent itself, since custom or
	// user-defined agent names may legitimately carry mixed case that
	// downstream lookups depend on. Stored history occasionally has case
	// drift on the built-in K8s names ("K8s_Orchestrator"); the switch
	// below matches lowercase, so a case-drifted entry would silently skip
	// the mode upgrade without this normalized-only comparison.
	plannerAgentNormalized := strings.ToLower(strings.TrimSpace(plannerAgent))

	// K8s orchestrator mode upgrade: when the router landed on a K8s
	// variant and a per-request or env mode signal names a specific variant
	// ("native" / "direct" / "lean"), swap the selection to the corresponding
	// always-that-variant handle. Non-K8s orchestrators (aws/gcp/azure) are
	// left alone — the signal is scoped to K8s routing.
	if isK8sOrchestratorVariant(plannerAgentNormalized) {
		if override := ResolveK8sOrchestratorOverride(request); override != "" && override != plannerAgentNormalized {
			ctx.GetLogger().Info("router: K8s orchestrator mode signal, upgrading agent",
				"original", plannerAgent, "override", override)
			plannerAgent = override
		}
	}

	return core.NBAgentResponse{Response: []string{plannerAgent}}, nil
}

// isK8sOrchestratorVariant reports whether the router-picked agent name is
// any of the surviving K8s orchestrator variants (primary + native + legacy
// aliases that used to point at delegating/direct/lean handles). Only these
// get upgraded by the K8s orchestrator mode signal. Legacy aliases stay
// listed so stored history that mentions the old names is still recognised as
// a K8s variant and eligible for override (they all now resolve to the primary).
func isK8sOrchestratorVariant(name string) bool {
	// Normalize case + whitespace so a stored-history entry with drifted
	// casing ("K8s_Orchestrator", "K8S_DEBUG") still matches. The
	// registration side uses lowercase names consistently.
	switch strings.ToLower(strings.TrimSpace(name)) {
	case AgentK8sOrchestratorName,
		AgentK8sOrchestratorNativeName,
		"k8s_debug",
		"k8s_debug_native",
		// Legacy names from the pre-collapse era (#32503 Phase 1) — kept so
		// stored conversation history still routes through the K8s mode
		// override path instead of silently missing it.
		"k8s_orchestrator_delegating", "k8s_orchestrator_direct", "k8s_orchestrator_lean",
		"k8s_debug_delegating", "k8s_debug_direct", "k8s_debug_lean",
		"k8s_orchestrator_2", "k8s_debug_2":
		return true
	}
	return false
}

// getTicketAgentName returns the appropriate ticket agent name based on the feature flag.
func getTicketAgentName() string {
	if config.Config.TicketV2Enabled {
		return TicketsV2AgentName
	}
	return TicketsAgentName
}

// getEventsAgentName returns the appropriate events agent name based on the feature flag.
func getEventsAgentName() string {
	if config.Config.EventsV2Enabled {
		return EventsV2AgentName
	}
	return EventsAgentName
}

func getAgent(ctx *security.RequestContext, agent string, accountId string) (core.NBAgent, bool) {
	var agentName string
	switch strings.ToLower(agent) {
	case "investigatechain", "investigate", "troubleshoot", "investigateagent", AgentK8sOrchestratorName, aws.AgentAwsOrchestratorName, "k8s_debug", "aws_debug":
		agentName = GetDebugAgentName(accountId)
	case "postgreschain", "postgres_debug", "postgresagent", PostgresAgentName:
		agentName = PostgresAgentName
	case "mysqlchain", "mysql_debug", "mysqlagent", MySQLAgentName:
		agentName = MySQLAgentName
	case "mssqlchain", "mssql_debug", "mssqlagent", "sqlserver", MSSQLAgentName:
		agentName = MSSQLAgentName
	case "oraclechain", "oracle_debug", "oracleagent", "oracledb", OracleAgentName:
		agentName = OracleAgentName
	case "prometheuschain", "prometheusagent", PrometheusAgentName:
		agentName = PrometheusAgentName
	case "logchain", "logsagent", LogsAgentName:
		agentName = LogsAgentName
	case "recommendationschain", "recommendation", "recommendationsagent", RecommendationsAgentName:
		agentName = RecommendationsAgentName
	case "eventschain", "event", "eventchain", EventsAgentName:
		agentName = getEventsAgentName()
	case FinOpsAgentName, "finopschain", "finopsagent", "cost", "spend":
		agentName = FinOpsAgentName
	case EventsV2AgentName:
		agentName = EventsV2AgentName
	case "ticket", "jira", "bugs", "issues", "ticketagent", "ticketmaster", "create_ticket", "ticket_create", TicketsAgentName:
		agentName = getTicketAgentName()
	case TicketsV2AgentName:
		agentName = TicketsV2AgentName
	case core.RouterAgentName:
		agentName = core.RouterAgentName
	case AgentLogAnalysisName:
		agentName = AgentLogAnalysisName
	case "automation", "automationmanager", "workflow", "workflowmanager", "automation_builder":
		agentName = WorkflowAgentName
	case "code", "code_debugger", "code_rca_agent", agentCodeAnalyzerLegacyName, AgentCodeAnalyzer:
		agentName = AgentCodeAnalyzer
	case "nudgebee_docs", "nudgebee", "nubidocs", "product_docs", "nudgebeedocsagent",
		"knowledge_base", "kb", "knowledgebase",
		"unifiedsearchagent", "unified_search", "search", WebSearchAgentName:
		// NudgebeeDocsAgent and KBAgent were consolidated into UnifiedSearchAgent;
		// route all legacy docs/KB agent aliases AND the unified search name (as
		// surfaced by the router prompt) to the unified search agent.
		agentName = WebSearchAgentName
	case "general", "generalchain", "generalagent", "help":
		return HelpAgent{}, false
	default:
		agentName = agent
	}

	return core.GetNBAgent(ctx, agentName, accountId, core.AgentStatusEnabled)
}

func InferAgent(ctx *security.RequestContext, userId string, accountId string, conversationId string, query string, configs ...core.ConversationSessionRequestConfig) (core.NBAgent, error) {
	isNewConversation := core.IsNewConversationRequest(configs...)

	// Optimization: If conversation has a last agent in history, try to use it directly to skip routing overhead
	// But ONLY if this isn't explicitly flagged as a new conversation
	if conversationId != "" && !isNewConversation {
		chatHistory, err := core.GetConversationDao().LoadConversationMessages(accountId, conversationId, userId, core.MessageTypeRoute, 1)
		if err == nil && len(chatHistory) > 0 && chatHistory[0]["response"] != "" {
			lastAgentName := strings.TrimSpace(chatHistory[0]["response"])
			if lastAgentName != "" && lastAgentName != core.RouterAgentName {
				if agent, found := getAgent(ctx, lastAgentName, accountId); found {
					ctx.GetLogger().Info("router: reusing last active agent from history", "agent_name", lastAgentName)
					return agent, nil
				}
			}
		}
	}

	routerChain := RouterAgent{}
	if conversationId == "" {
		conversationId = uuid.NewString()
	}
	// Carry the caller's QueryConfig so router-time checks (K8s orchestrator
	// mode signal, tool_configs, capabilities…) see the per-request values
	// instead of falling back to env defaults. Without this, a UI-selected
	// k8s_orchestrator_mode never reached ResolveK8sOrchestratorOverride and
	// the router always resolved to whatever LLM_SERVER_K8S_ORCHESTRATOR_MODE
	// was booted with.
	chainRes, err := routerChain.Execute(ctx, core.NBAgentRequest{
		Query:          query,
		AccountId:      accountId,
		ConversationId: conversationId,
		UserId:         userId,
		QueryConfig:    core.QueryConfigFromRequests(configs...),
	})

	if err != nil {
		ctx.GetLogger().Error("Unable to execute router", "error", err)
		return nil, errors.New("llm: unable to complete request, Please try again later")
	}

	if len(chainRes.Response) == 0 || chainRes.Response[0] == "" {
		return nil, errors.New("llm: unable to identify intent. My expertise is focused on Kubernetes, Helm, Events, Logs, Metrics, Recommendations, Software Development. Could you please provide more details or rephrase your question so I can better assist you?")
	}

	// Trim the chain name to remove any leading or trailing spaces
	chainName := chainRes.Response[0]
	chain, ok := getAgent(ctx, chainName, accountId)
	if !ok {
		ctx.GetLogger().Error("Unable to identify agent", "chain", chainName)
		return nil, errors.New("llm: unable to identify intent. My expertise is focused on Kubernetes, Helm, Events, Logs, Metrics, Recommendations, Software Development. Could you please provide more details or rephrase your question so I can better assist you?")
	}

	return chain, err
}

func InferAgentOrHelp(ctx *security.RequestContext, userId string, accountId string, conversationId string, query string, configs ...core.ConversationSessionRequestConfig) (core.NBAgent, error) {
	agent, err := InferAgent(ctx, userId, accountId, conversationId, query, configs...)
	if err != nil {
		return HelpAgent{}, nil
	}

	return agent, err
}

type HelpAgent struct {
}

func (l HelpAgent) GetName() string {
	return "help"
}

func (l HelpAgent) GetNameAliases() []string {
	return []string{"Help"}
}

func (l HelpAgent) GetDescription() string {
	return `Returns help message based on user question`
}

func (l HelpAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (l HelpAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		fmt.Sprintf("**Purpose:** You are the %s AI Assistant created by %s, designed to assist users in formulating their questions correctly.", config.Config.AIAssistantName, config.Config.AIAssistantCompany),
		"**Expertise:** Your expertise is focused on Kubernetes, Helm, Events, Logs, Metrics, Recommendations, Software Development and Security.",
		"**Greetings:** If a user greets you, respond with a greeting and introduce what they can do within the system.",
		"**Agent Awareness:** Let user know of all available agents. Available agents are: \n{{ .agents }}",
		"**Clear Instructions**: Provide user with a clear instructions on how to use the system.",
	}

	constraints := []string{
		"You can only use the information provided to answer the question.",
		"Do not ask for any clarification from the user, provide a clear and accurate response.",
		"Do not make up information, if you are not sure of the answer, say that you don't know.",
	}

	examples := []core.NBAgentPromptExample{}

	return core.NBAgentPrompt{
		Instructions: instructions,
		Constraints:  constraints,
		Examples:     examples,
		Variables:    []string{"agents"},
	}
}

func (l HelpAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

func (l HelpAgent) Execute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	agents := []string{}
	allowOnlyEnabled := true
	for _, a := range core.ListAgents(ctx, request.AccountId, allowOnlyEnabled) {
		agents = append(agents, fmt.Sprintf("* %s - %s", a.Name, a.Description))
	}

	systemtMessages, err := core.GetPromptTemplate(l.GetSystemPrompt(ctx, request), request, l.GetPlannerType()).Format(map[string]any{
		"agents": strings.Join(agents, "\n"),
	})
	if err != nil {
		ctx.GetLogger().Error("helper: unable to evaluate prompt", "error", err)
		return core.NBAgentResponse{Response: []string{"unable to process request at the momemt, please try again later"}}, nil
	}

	routerPromptMessage := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemtMessages),
		llms.TextParts(llms.ChatMessageTypeHuman, request.Query),
	}
	completion, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, false, routerPromptMessage, true)
	if err != nil {
		ctx.GetLogger().Error("helper: unable to generate content", "error", err)
		return core.NBAgentResponse{}, err
	}
	return core.NBAgentResponse{Response: []string{completion.Choices[0].Content}}, nil
}
