package agents

import (
	"log/slog"
	"nudgebee/llm/agents/aws"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/samber/lo"
)

const AgentK8sDebugName = "k8s_debug"

func init() {
	core.RegisterNBAgentFactory(AgentK8sDebugName, func(accountId string) (core.NBAgent, error) {
		return newK8sDebugAgent(accountId), nil
	})
	core.RegisterAgentCacheInvalidator(func(accountId string, agentName string) {
		if agentName == "" || agentName == AgentK8sDebugName {
			InvalidateAgentSupportedToolsCache(accountId, AgentK8sDebugName)
		}
	})
	toolcore.RegisterToolCacheInvalidator(func(accountId string) {
		InvalidateAgentSupportedToolsCache(accountId, AgentK8sDebugName)
	})

	common.CacheSubscribe("agent_invalidation", func(message string) {
		parts := strings.Split(message, ":")
		if len(parts) == 2 {
			slog.Info("received global agent invalidation", "account_id", parts[0], "agent", parts[1])
			InvalidateAgentSupportedToolsCache(parts[0], parts[1])
		}
	})
}

func newK8sDebugAgent(accountId string) core.NBAgent {
	return &K8sDebugAgent2{
		accountId: accountId,
	}
}

type K8sDebugAgent2 struct {
	accountId string
}

func (l *K8sDebugAgent2) GetName() string {
	return AgentK8sDebugName
}

func (a *K8sDebugAgent2) GetNameAliases() []string {
	return []string{"Debugger"}
}

func (l *K8sDebugAgent2) GetDescription() string {
	return `SRE/DevOps Troubleshooting expert, with expertise on Kubernetes, AWS, GCP, Azure, CloudNative, Helm, Security, Programming, Prometheus, Loki, ELK, Github, Optimization, Jira, SQL, Databases etc`
}

func (l *K8sDebugAgent2) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return getSupportedTools(ctx, l.accountId, l.GetName())
}

func (l *K8sDebugAgent2) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	// Select prompt based on effective planner: react_3 uses a react-optimised prompt
	// that avoids ReWOO plan-oriented language and includes parallel action examples.
	promptKey := prompts.PromptAgentK8sDebug
	promptRepoKey := prompts_repo.PromptAgentK8sDebug
	if config.Config.LlmServerReAct3Enabled || config.Config.LlmServerRewooToReact3Enabled {
		promptKey = prompts.PromptAgentK8sDebugReact
		promptRepoKey = prompts_repo.PromptAgentK8sDebugReact
	}

	// The versioned prompt system (prompts pkg) is tried first; legacy repo is the fallback.
	promptText := prompts.GetPrompt(ctx.GetContext(), promptKey, query.AccountId)
	if promptText == "" {
		promptText = prompts_repo.GetPrompt(promptRepoKey)
	}

	// Resolve all template placeholders in a single pass.
	// Include all known shared-rule partials so account-specific versioned prompts that still
	// reference older keys (e.g. {{.time_handling_rules}}) render correctly instead of causing
	// a template execution failure and falling back to a minimal prompt.
	tmplData := map[string]any{
		"remediation_enabled":   config.Config.RemediationAgentEnabled,
		"shell_tool_enabled":    config.Config.LlmServerShellToolEnabled,
		"data_protection_rules": prompts_repo.GetPrompt(prompts_repo.PromptSharedDataProtectionRules),
		"code_analysis_rules":   prompts_repo.GetPrompt(prompts_repo.PromptSharedCodeAnalysisRules),
		"time_handling_rules":   prompts_repo.GetPrompt(prompts_repo.PromptSharedTimeHandlingRules),
	}
	// Render conditional blocks ({{if .remediation_enabled}}, {{.data_protection_rules}}, etc.).
	// Two-pass strategy:
	//   1. missingkey=error — catches prompts that reference unknown keys so we can log a warning.
	//   2. missingkey=zero  — re-renders with unknown keys as "" so the full prompt is preserved.
	// On any parse error we keep the original promptText so the agent retains its full instructions.
	t, err := template.New("k8s_debug").Option("missingkey=error").Parse(promptText)
	if err != nil {
		slog.ErrorContext(ctx.GetContext(), "failed to parse k8s_debug prompt template, using raw prompt", "error", err)
		// promptText unchanged — preserve full agent instructions
	} else {
		var buf strings.Builder
		if err = t.Execute(&buf, tmplData); err != nil {
			// Log so we know which account's versioned prompt has an unknown key and can fix it.
			slog.WarnContext(ctx.GetContext(), "k8s_debug prompt template has unknown keys, re-rendering with zero fallback", "error", err, "account_id", query.AccountId)
			buf.Reset()
			// Re-parse with missingkey=zero so unknown keys render as "" rather than failing.
			if t2, err2 := template.New("k8s_debug").Option("missingkey=zero").Parse(promptText); err2 == nil {
				if err2 = t2.Execute(&buf, tmplData); err2 == nil {
					promptText = buf.String()
				} else {
					slog.ErrorContext(ctx.GetContext(), "failed to execute k8s_debug prompt template (zero pass), using raw prompt", "error", err2)
					// promptText unchanged — preserve full agent instructions
				}
			}
		} else {
			promptText = buf.String()
		}
	}

	// Parse structured prompt file: extracts Role, Instructions, Constraints, OutputFormat, Examples
	prompt := core.ParsePromptToNBAgentPrompt(promptText)

	// Build tool usage map from registered tools so the prompt includes live tool descriptions
	toolUsage := map[string][]string{}
	for _, t := range l.GetSupportedTools(ctx) {
		toolUsage[t.Name()] = []string{t.Description()}
	}
	prompt.ToolUsage = toolUsage

	return prompt
}

func (l *K8sDebugAgent2) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReWoo
}

func (l *K8sDebugAgent2) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}

type agentSupportedToolsCache struct {
	mutex sync.RWMutex
	data  map[string]struct {
		tools  []toolcore.NBTool
		expiry time.Time
	}
}

var agentSupportedToolsCacheInstance = &agentSupportedToolsCache{
	data: make(map[string]struct {
		tools  []toolcore.NBTool
		expiry time.Time
	}),
}

func (c *agentSupportedToolsCache) get(accountId, agent string) ([]toolcore.NBTool, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	key := accountId + ":" + agent
	item, exists := c.data[key]
	if exists && time.Now().Before(item.expiry) {
		return item.tools, true
	}
	return nil, false
}

func (c *agentSupportedToolsCache) set(accountId, agent string, tools []toolcore.NBTool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	key := accountId + ":" + agent
	c.data[key] = struct {
		tools  []toolcore.NBTool
		expiry time.Time
	}{
		tools:  tools,
		expiry: time.Now().Add(30 * time.Minute),
	}
}

func (c *agentSupportedToolsCache) delete(accountId, agent string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if agent == "" {
		// If agent not specified, clear all for this account
		for k := range c.data {
			if strings.HasPrefix(k, accountId+":") {
				delete(c.data, k)
			}
		}
	} else {
		delete(c.data, accountId+":"+agent)
	}
}

func InvalidateAgentSupportedToolsCache(accountId string, agentName string) {
	agentSupportedToolsCacheInstance.delete(accountId, agentName)
}

func getSupportedTools(ctx *security.RequestContext, accountId string, agentName string) []toolcore.NBTool {
	var staticTools []toolcore.NBTool

	if cached, ok := agentSupportedToolsCacheInstance.get(accountId, agentName); ok {
		staticTools = cached
	} else {
		var toolNames []string
		_, agentToolNames, _ := core.AgentAdditionalInstructionsAndToolsAndConfigs(ctx, accountId, agentName)
		if len(agentToolNames) > 0 {
			slog.Info("Agent has configured tools", "agent", agentName, "tools", agentToolNames)
			toolNames = agentToolNames
		} else {

			baseTools := []string{KubectlAgentName, LogsAgentName, WebSearchAgentName, PostgresAgentName, EventsAgentName, TracesAgentName, MetricsAgentName, RedisAgentName, MySQLAgentName, MSSQLAgentName, OracleAgentName, RabbitMQAgentName, SecurityAgentName, HelmAgentName, GithubAgentName, getTicketAgentName(), WorkflowAgentName, ServiceDependencyGraph, VisualizationAgentName, RecommendationsAgentName, aws.AwsAgentName, aws.AgentAwsObservabilityName, GcpAgentName, AzureAgentName, ResourceSearchAgentName, ServerAgentName, AgentCode2, DelegateAgentToolName}

			// Conditionally add remediation agent based on feature flag
			if config.Config.RemediationAgentEnabled {
				baseTools = append(baseTools, RemediationAgentName)
				slog.Debug("Remediation agent enabled", "accountId", accountId, "agent", agentName)
			}

			// Conditionally add shell tool based on feature flag
			if config.Config.LlmServerShellToolEnabled {
				baseTools = append(baseTools, toolcore.ToolExecuteShellCommand)
				slog.Debug("Shell tool enabled", "accountId", accountId, "agent", agentName)
			}

			// Conditionally add think tool for complex investigations
			if config.Config.LlmServerThinkToolEnabled {
				baseTools = append(baseTools, tools.ThinkToolName)
			}

			toolNames = baseTools
		}

		if core.IsAgentsFollowupEnabled() {
			toolNames = append(toolNames, FollowupAgentName)
		}

		// Add Datadog debug as an optional extra tool
		toolNames = append(toolNames, AgentDatadogDebugName)

		enabledTools := toolcore.GetEnabledNBTools(ctx, accountId)
		enabledMap := make(map[string]toolcore.NBTool)
		for _, t := range enabledTools {
			enabledMap[strings.ToLower(t.Name())] = t
		}

		agentTools := []toolcore.NBTool{}
		for _, name := range toolNames {
			if t, ok := enabledMap[strings.ToLower(name)]; ok {
				agentTools = append(agentTools, t)
			}
		}

		staticTools = lo.UniqBy(agentTools, func(t toolcore.NBTool) string {
			return t.Name()
		})
		agentSupportedToolsCacheInstance.set(accountId, agentName, staticTools)
	}

	// Always merge MCP integration tools fresh — they have their own cache
	// that correctly avoids caching transient failures.
	mcpTools := toolcore.ListMCPIntegrationTools(accountId)
	if len(mcpTools) == 0 {
		return staticTools
	}

	merged := make([]toolcore.NBTool, len(staticTools), len(staticTools)+len(mcpTools))
	copy(merged, staticTools)
	merged = append(merged, mcpTools...)
	return lo.UniqBy(merged, func(t toolcore.NBTool) string {
		return t.Name()
	})
}
