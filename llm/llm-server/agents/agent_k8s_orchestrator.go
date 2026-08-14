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

const AgentK8sOrchestratorName = "k8s_orchestrator"

// AgentK8sOrchestrator2Name is an explicit, @-invocable handle that always runs the
// direct-kubectl (v2) behavior regardless of the configured K8sOrchestratorMode. The
// router never selects it; it exists so v1 (the flag-gated default) and v2 can be
// invoked side by side — `@k8s_orchestrator` vs `@k8s_orchestrator_2` — for live eval
// on the same deployment without a config flip. Both names share ONE implementation;
// only (name, mode) differ, and the distinct name gives it a distinct cache key.
const AgentK8sOrchestrator2Name = "k8s_orchestrator_2"

func init() {
	core.RegisterNBAgentFactoryWithAliases(AgentK8sOrchestratorName, func(accountId string) (core.NBAgent, error) {
		return newK8sOrchestratorAgent(accountId), nil
	}, "k8s_debug")
	core.RegisterAgentCacheInvalidator(func(accountId string, agentName string) {
		if agentName == "" || agentName == AgentK8sOrchestratorName {
			InvalidateAgentSupportedToolsCache(accountId, AgentK8sOrchestratorName)
		}
	})
	toolcore.RegisterToolCacheInvalidator(func(accountId string) {
		InvalidateAgentSupportedToolsCache(accountId, AgentK8sOrchestratorName)
	})

	// Explicit always-direct eval handle (see AgentK8sOrchestrator2Name). Same
	// implementation, distinct name → distinct cache key, invocable by @name only.
	core.RegisterNBAgentFactoryWithAliases(AgentK8sOrchestrator2Name, func(accountId string) (core.NBAgent, error) {
		return newK8sOrchestrator2Agent(accountId), nil
	}, "k8s_debug_2")
	core.RegisterAgentCacheInvalidator(func(accountId string, agentName string) {
		if agentName == "" || agentName == AgentK8sOrchestrator2Name {
			InvalidateAgentSupportedToolsCache(accountId, AgentK8sOrchestrator2Name)
		}
	})
	toolcore.RegisterToolCacheInvalidator(func(accountId string) {
		InvalidateAgentSupportedToolsCache(accountId, AgentK8sOrchestrator2Name)
	})

	common.CacheSubscribe("agent_invalidation", func(message string) {
		parts := strings.Split(message, ":")
		if len(parts) == 2 {
			slog.Info("received global agent invalidation", "account_id", parts[0], "agent", parts[1])
			InvalidateAgentSupportedToolsCache(parts[0], parts[1])
		}
	})
}

// K8sOrchestratorMode* are the values of config llm_server_k8s_orchestrator_mode,
// the single boot-time knob for what the router-selected k8s_orchestrator runs.
const (
	K8sOrchestratorModeDelegating = "delegating" // v1: delegate kubectl to the sub-agent (fallback for unknown mode)
	K8sOrchestratorModeDirect     = "direct"     // v2: hold kubectl_execute, run kubectl directly
	K8sOrchestratorModeLean       = "lean"       // reduced tool core + minimal prompt — the DEFAULT (critique unchanged)
	K8sOrchestratorModeNative     = "native"     // K8s-native: kubectl-first, no cloud/NL-wrapper sub-agents (see agent_k8s_orchestrator_native.go)
)

// newK8sOrchestratorAgent is the primary, router-selected agent. Its behavior is
// chosen by config.K8sOrchestratorMode (boot-time; rollback = change + redeploy).
// Running lean/direct under the primary name keeps routing, stored history, and
// the @k8s_debug aliases unchanged — only prompt/critique/kubectl behavior differ.
// The configured default is "lean" (llm_server_k8s_orchestrator_mode); an
// unknown/typo value falls back to delegating (v1).
func newK8sOrchestratorAgent(accountId string) core.NBAgent {
	switch strings.ToLower(strings.TrimSpace(config.Config.K8sOrchestratorMode)) {
	case K8sOrchestratorModeLean:
		return newK8sLeanAgentNamed(accountId, AgentK8sOrchestratorName)
	case K8sOrchestratorModeDirect:
		return newK8sOrchestratorAgentNamed(accountId, AgentK8sOrchestratorName, true)
	default:
		return newK8sOrchestratorAgentNamed(accountId, AgentK8sOrchestratorName, false)
	}
}

// newK8sOrchestrator2Agent is the always-direct eval handle (see AgentK8sOrchestrator2Name):
// v2 behavior regardless of the flag, for live side-by-side comparison against the primary.
func newK8sOrchestrator2Agent(accountId string) core.NBAgent {
	return newK8sOrchestratorAgentNamed(accountId, AgentK8sOrchestrator2Name, true)
}

// newK8sOrchestratorAgentMode builds the primary-named agent in an explicit kubectl
// mode. Tests use it to exercise delegating (false) and direct (true) behavior.
func newK8sOrchestratorAgentMode(accountId string, useKubectlDirect bool) core.NBAgent {
	return newK8sOrchestratorAgentNamed(accountId, AgentK8sOrchestratorName, useKubectlDirect)
}

// newK8sOrchestratorAgentNamed is the shared constructor. name drives identity and the
// cache key (so the primary and the eval handle stay isolated); useKubectlDirect drives
// the kubectl tool, the prompt's {{if .use_kubectl_direct}} branch, and the log filter.
func newK8sOrchestratorAgentNamed(accountId, name string, useKubectlDirect bool) core.NBAgent {
	return &K8sOrchestratorAgent{
		accountId:        accountId,
		name:             name,
		useKubectlDirect: useKubectlDirect,
	}
}

type K8sOrchestratorAgent struct {
	accountId        string
	name             string
	useKubectlDirect bool
}

func (l *K8sOrchestratorAgent) GetName() string {
	return l.name
}

func (a *K8sOrchestratorAgent) GetNameAliases() []string {
	if a.name == AgentK8sOrchestrator2Name {
		return []string{"Debugger2", "k8s_debug_2"}
	}
	return []string{"Debugger", "k8s_debug"}
}

func (l *K8sOrchestratorAgent) GetDescription() string {
	return `SRE/DevOps Troubleshooting expert, with expertise on Kubernetes, AWS, GCP, Azure, CloudNative, Helm, Security, Programming, Prometheus, Loki, ELK, Github, Optimization, Jira, SQL, Databases etc`
}

func (l *K8sOrchestratorAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	// v2 (direct) holds kubectl_execute and runs kubectl itself; v1 (delegating)
	// holds the kubectl sub-agent. Cache keys off GetName(); since the mode is fixed
	// per deploy by the global flag, only one tool set is ever cached in production.
	kubectlTool := KubectlAgentName
	if l.useKubectlDirect {
		kubectlTool = tools.ToolExecuteKubectlCommand
	}
	return getSupportedTools(ctx, l.accountId, l.GetName(), kubectlTool)
}

func (l *K8sOrchestratorAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	// useKubectlDirect drives the prompt's {{if .use_kubectl_direct}} blocks: run
	// kubectl_execute directly (v2) vs route kubectl work through the `kubectl` sub-agent (v1).
	return renderK8sDebugReactPrompt(ctx, query, l.useKubectlDirect)
}

// UpdateToolResponseForPlanner condenses kubectl_execute log output for the planner
// when running kubectl directly (v2). In delegating mode (v1) the kubectl sub-agent
// does its own filtering, so this is a passthrough.
func (l *K8sOrchestratorAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	if l.useKubectlDirect {
		return filterKubectlLogResponse(toolRequest, toolResponse)
	}
	return toolResponse
}

// renderK8sDebugReactPrompt renders the shared agent_k8s_debug_react prompt for both
// k8s orchestrators. The useKubectlDirect flag drives the {{if .use_kubectl_direct}}
// blocks in the prompt file: false = delegate kubectl work to the `kubectl` sub-agent,
// true = hold and run `kubectl_execute` directly. All kubectl guidance lives in the
// prompt file (gated by that flag), not appended here in Go, so the two orchestrators
// cannot drift apart.
func renderK8sDebugReactPrompt(ctx *security.RequestContext, query core.NBAgentRequest, useKubectlDirect bool) core.NBAgentPrompt {
	promptKey := prompts.PromptAgentK8sDebugReact
	promptRepoKey := prompts_repo.PromptAgentK8sDebugReact

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
		"watch_enabled":         config.Config.WatchEnabled,
		"data_protection_rules": prompts_repo.GetPrompt(prompts_repo.PromptSharedDataProtectionRules),
		"code_analysis_rules":   prompts_repo.GetPrompt(prompts_repo.PromptSharedCodeAnalysisRules),
		"time_handling_rules":   prompts_repo.GetPrompt(prompts_repo.PromptSharedTimeHandlingRules),
		"use_kubectl_direct":    useKubectlDirect,
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

	// ToolUsage intentionally left unset: the planner already renders each tool's
	// Description() once via {{.tool_descriptions}}. Copying the same Description()
	// into ToolUsage produced a redundant second render in the
	// <tool_usage_instructions> block of this orchestrator's (account-cached) prompt.

	return prompt
}

func (l *K8sOrchestratorAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeOrchestrating
}

// IsWatchCapable: drives action sub-agents (restart/scale/rollout) whose outcome
// completes later, so it may register a background watch.
func (l *K8sOrchestratorAgent) IsWatchCapable() bool { return true }

func (l *K8sOrchestratorAgent) GetModelCategory() core.ModelTier {
	return core.ModelTierReasoning
}

func (l *K8sOrchestratorAgent) GetCacheScope() core.CacheScope {
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

// getSupportedTools builds the k8s orchestrator tool list. kubectlTool is the first
// base tool: KubectlAgentName for the delegating orchestrator, or
// tools.ToolExecuteKubectlCommand for the direct orchestrator that runs kubectl itself.
func getSupportedTools(ctx *security.RequestContext, accountId string, agentName string, kubectlTool string) []toolcore.NBTool {
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

			baseTools := []string{kubectlTool, LogsAgentName, WebSearchAgentName, PostgresAgentName, EventsAgentName, TracesAgentName, MetricsAgentName, RedisAgentName, MySQLAgentName, MSSQLAgentName, OracleAgentName, RabbitMQAgentName, SecurityAgentName, HelmAgentName, GithubAgentName, getTicketAgentName(), WorkflowAgentName, ServiceDependencyGraph, VisualizationAgentName, RecommendationsAgentName, aws.AwsAgentName, aws.AgentAwsObservabilityName, GcpAgentName, AzureAgentName, ResourceSearchAgentName, ServerAgentName, AgentCode2, DelegateAgentToolName}

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
		toolNames = append(toolNames, AgentDatadogOrchestratorName)

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
