package agents

import (
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	"strings"
	"sync"
	"time"

	toolcore "nudgebee/llm/tools/core"
)

// Lean-only (plus opt-in native): delegating/direct dropped 2026-08-11 (#32503 Phase 1).
// The K8s orchestrator now runs the lean-loop implementation under the primary
// handle by default, with an opt-in K8s-native mode for kubectl-first accounts.
// The always-direct / always-delegating eval handles were removed with the collapse.
const AgentK8sOrchestratorName = "k8s_orchestrator"

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
	K8sOrchestratorModeLean   = "lean"   // reduced tool core + minimal prompt — the DEFAULT
	K8sOrchestratorModeNative = "native" // K8s-native: kubectl-first, no cloud/NL-wrapper sub-agents (see agent_k8s_orchestrator_native.go)
)

// newK8sOrchestratorAgent is the primary, router-selected agent. Its behavior is
// chosen by config.K8sOrchestratorMode (boot-time; rollback = change + redeploy).
// Only lean and native remain after the delegating/direct collapse in #32503 Phase 1.
// Unknown/empty falls back to lean (the production default).
func newK8sOrchestratorAgent(accountId string) core.NBAgent {
	if strings.ToLower(strings.TrimSpace(config.Config.K8sOrchestratorMode)) == K8sOrchestratorModeNative {
		return newK8sNativeAgentNamed(accountId, AgentK8sOrchestratorName)
	}
	return newK8sLeanAgentNamed(accountId, AgentK8sOrchestratorName)
}

// K8sLeanAgent is the K8s lean-loop orchestrator: the reduced k8s core
// (kubectl_execute + logs/events/metrics/traces + resource_search + SDG +
// recommendations + websearch + finops + delegate_agent + search_tools +
// search_skills) plus a short principle-level prompt (agent_k8s_lean). Every
// specialist (databases, helm, cloud CLIs, …) is dropped from context and
// reached on-demand via search_tools + delegate_agent. Everything else —
// including the answer critique — runs through the runtime-selected ReAct
// planner under the standard gates.
type K8sLeanAgent struct {
	accountId string
	// name is the handle this instance runs under. Currently always
	// AgentK8sOrchestratorName; retained to keep the distinct-name → distinct
	// cache key contract intact for any future eval handles.
	name string
}

func newK8sLeanAgentNamed(accountId, name string) *K8sLeanAgent {
	return &K8sLeanAgent{accountId: accountId, name: name}
}

func (l *K8sLeanAgent) GetName() string { return l.name }

func (l *K8sLeanAgent) AcceptsExecutionTarget() {}

func (l *K8sLeanAgent) GetNameAliases() []string {
	return []string{"Kubernetes Troubleshooter", "Debugger", "k8s_debug"}
}

func (l *K8sLeanAgent) GetDescription() string {
	return `Lean-loop SRE/DevOps troubleshooting orchestrator: workspace-based Kubernetes reads and local analysis, approval-aware direct kubectl mutations, and specialists such as Helm reached on-demand via search_tools + delegate_agent.`
}

func (l *K8sLeanAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeOrchestrating
}

func (l *K8sLeanAgent) GetModelCategory() core.ModelTier { return core.ModelTierReasoning }
func (l *K8sLeanAgent) GetCacheScope() core.CacheScope   { return core.CacheScopeAccount }

// IsWatchCapable: drives action sub-agents (restart/scale/rollout) whose outcome
// completes later, so it may register a background watch.
func (l *K8sLeanAgent) IsWatchCapable() bool { return true }

// NB: no CritiqueEnabled() method on purpose — the orchestrator does not
// implement NBAgentReActPlannerCritiqueSupport, so critique is governed by the
// standard gate (top-level && investigation).

func (l *K8sLeanAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	// Reduced k8s core (kubectl_execute + logs/events/metrics/traces + SDG +
	// resource_search + recommendations + delegate_agent + search_tools);
	// specialists reached on-demand via search_tools + delegate_agent.
	return getTrimmedK8sSupportedTools(ctx, l.accountId, l.GetName())
}

func (l *K8sLeanAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	promptText, promptErr := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptK8sLean, query.AccountId)
	if promptErr != nil {
		// Return nothing rather than continue: everything appended below is
		// decoration, so carrying on yields a "system prompt" that is just a memory
		// nudge — worse than empty, because it looks like a prompt. MustResolveAll
		// covers default/v1 at startup; this catches a malformed provider- or
		// version-specific override added later.
		ctx.GetLogger().Error("k8s orchestrator: system prompt failed to load", "error", promptErr)
		return core.NBAgentPrompt{}
	}
	if nudge := memoryNudgeIfEnabled(); nudge != "" {
		promptText += "\n\n" + nudge
	}
	if grounding := k8sGroundingIfEnabled(query); grounding != "" {
		promptText += "\n\n" + grounding
	}
	return core.ParsePromptToNBAgentPrompt(promptText)
}

// UpdateToolResponseForPlanner reuses the shared kubectl log condenser (the lean
// agent runs kubectl directly, so raw log output lands in its own scratchpad).
func (l *K8sLeanAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	return filterKubectlLogResponse(toolRequest, toolResponse)
}

// ---- shared kubectl log condenser (moved from agent_kubectl.go in Phase 3d) ----

// filterKubectlLogResponse condenses a kubectl_execute response to error-context log
// lines when the command was a `kubectl logs` invocation. For any other command, or an
// unparseable payload, the response is returned unchanged. Shared by the k8s
// orchestrator (lean + native) via UpdateToolResponseForPlanner so log output is
// condensed identically wherever kubectl_execute lands.
//
// Kept here (not in the k8s reduced-core file) so the two orchestrator variants
// keep a single import site for the helper they both use, and so removing the
// retired kubectl wrapper agent in Phase 3d didn't need a new "helpers" file.
func filterKubectlLogResponse(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	if !strings.EqualFold(toolRequest.Tool, tools.ToolExecuteKubectlCommand) {
		return toolResponse
	}
	// Only kubectl logs output is condensed; everything else passes through untouched.
	if !strings.Contains(toolRequest.ToolInput, "kubectl logs") {
		return toolResponse
	}

	resultsMap := map[string]any{}
	if err := common.UnmarshalJson([]byte(toolResponse), &resultsMap); err != nil {
		return toolResponse
	}

	stdout := ""
	stderr := ""
	if v, isOk := resultsMap["stdout"].(string); isOk {
		stdout = v
	}
	if v, isOk := resultsMap["stderr"].(string); isOk {
		stderr = v
	}

	logs := tools.GetErrorLinesFromLogStringOrDefault(stdout+stderr, true)
	return strings.Join(logs, "\n")
}

// ---- shared cache + memory-nudge helpers (used by lean + native) ----

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

// appendMemoryToolName adds the on-demand memory tools to a tool-name list
// when LLM_MEMORY_TOOLS_ENABLED is on.
//
// Automation / event / cron runs can SEE memory_write in their tool list
// (we don't hide it per-source), but memoryWriteTool.Call refuses the write
// at the top when ctx.Source is a known automation source. This closes the
// alert-text-becomes-tenant-fact path — in those runs the "human" turn
// carries external payload text, so without this gate an agent-authored
// type=fact write would land in tenant-wide Collective memory sourced
// from alert content. The per-request MemoryWriteAllowed switch (PR #39737)
// is the intended control plane but no caller sets it today, and
// collectUserTurnsIncludingCurrent's isAutomated check only covers the
// user-attribution half.
func appendMemoryToolName(names []string) []string {
	if config.Config.MemoryToolsEnabled {
		// Tool names kept in sync with the Name() method of memoryReadTool /
		// memoryWriteTool in ee/memory/tool_memory.go. The literals here are
		// the public contract the model is trained to call.
		names = append(names, "memory_read", "memory_write")
	}
	return names
}

// memoryNudgeIfEnabled returns the memory-tools prompt nudge when the tools
// are enabled.
func memoryNudgeIfEnabled() string {
	if config.Config.MemoryToolsEnabled {
		return memoryReadWriteNudge
	}
	return ""
}

// memoryReadWriteNudge teaches the model when to call the new memory tools —
// the concrete triggers that a tool description alone can't convey reliably
// enough to survive across LLM providers. Deliberately terse and example-
// heavy: models converge on the shape from patterns better than from
// abstract rules. Ordered "read first, write when appropriate" because the
// model is more likely to over-write than over-read.
const memoryReadWriteNudge = "**memory_read before running live tools** when a resource id, subject, or preference key could plausibly be in memory. Pass `filters` with a specific field (resource_id, subject, or key) — at least one field is required. Only add `query` when you don't know the exact key and want a semantic broaden.\n\n" +
	"**Call memory_write when the user's turn is clearly telling you something to remember going forward**, in one of these four shapes:\n" +
	"- Imperative: 'remember', 'note', 'save this', 'don't forget', 'keep this in mind', 'store'\n" +
	"- Declarative preference: 'I prefer X', 'I use X', 'my default X is Y', 'always do X', 'never do Y'\n" +
	"- Declarative fact: 'X is at Y', 'X lives in Y', 'X runs on Y'\n" +
	"- Declarative playbook / flow: 'for X, my flow is: A → B → C', 'when X happens I do Y', 'here's how I investigate X'\n\n" +
	"**DO NOT save pass-through context during an unrelated investigation.** If the user drops a fact while asking about something else ('the OOM started at 2pm — by the way, checkout runs on node pool B — can you check limits?'), answer the question; do not save 'checkout runs on node pool B'. Save only when the durable statement is the point of the turn, or when the user explicitly asked you to remember.\n\n" +
	"Pick the type from the shape:\n" +
	"- **type=preference** — a rule the user wants you to follow going forward (log source, default namespace, output format). `key` is a short snake_case identifier; `value` is what they said.\n" +
	"- **type=fact** — a truth about their infra (where a service runs, what it depends on, a config invariant). `subject` names the resource; `body` is the assertion.\n" +
	"- **type=playbook** — an ordered sequence for handling a specific problem. `problem` names the trigger; `steps` is the ordered list.\n\n" +
	"Pass the user's own words verbatim as `evidence` (min 15 chars). For type=playbook the evidence must appear verbatim in a recent user turn — the runtime refuses agent-fabricated playbooks.\n\n" +
	"**When both apply in one user turn** — read first (recall what's known), then write (persist what's new). Call memory_write on the same turn the user stated the durable content; do not defer to a later turn.\n\n" +
	"**Do NOT call memory_write for** your own methodology or interim findings during an investigation. That path is auto-consolidated when investigations resolve. And do NOT call it for questions the user asks (\"what are the pods?\" is a query, not a save-intent)."

// k8sGroundingNudge is the "ground before you fan out" discipline. k8s_lean is the
// only lean orchestrator missing the "start where the symptom shows" paragraph its
// aws/gcp/azure siblings carry, and in practice the planner opens live-symptom
// investigations by delegating a heavy metrics/logs sub-agent and blocking on it —
// before running the cheap authoritative kubectl tools it already holds. This adds
// two disciplines: (1) probe cheap-and-local first, then delegate scoped; (2) for a
// hostname/URL symptom, resolve WHAT SERVES the host before assuming a workload, and
// say so honestly when nothing in-cluster serves it (rather than diagnosing a
// similarly-named workload — the observed "external marketing host → similarly-named
// in-cluster dev host" subject swap). It scopes the investigation, never replaces it. Appended (not baked into k8s_lean.yaml)
// so it stays behind OrchestratorGroundingEnabled for a clean A/B and cannot regress the shared
// prompt when the flag is off.
const k8sGroundingNudge = "**Ground before scoped investigation when the target is unknown.** For a live symptom — a CPU/memory surge, restarts, pending pods, a workload erroring right now — use the cheap authoritative tools you already hold: `kubectl top`/`get`/`describe` on the named workload and its recent `events`, issued together in one parallel batch. If the workload, namespace, symptom, and time window are already known, issue independent historical evidence calls (`metrics`, `logs`, or `traces`) alongside that live batch instead of waiting. If their inputs depend on what the live snapshot reveals, observe it first and then delegate with the resolved scope. When the symptom is a hostname or URL (e.g. an uptime/downtime alert), first resolve WHAT SERVES IT — `kubectl get ingress -A` (or the Service) for that host — before assuming a workload; if no in-cluster ingress serves that host, say so plainly (\"not served by this cluster\") rather than diagnosing a similarly-named workload. Grounding SCOPES the investigation; it never replaces it — a healthy live snapshot doesn't close a \"why did it happen\" question, so carry it through to the mechanism."

// k8sGroundingIfEnabled returns the grounding discipline when the flag is on, else "".
func k8sGroundingIfEnabled(query core.NBAgentRequest) string {
	isTopLevel := query.ParentAgentId == "" || query.ParentAgentId == query.AgentId
	if config.Config.OrchestratorGroundingEnabled && isTopLevel {
		return k8sGroundingNudge
	}
	return ""
}
