package core

import (
	"strings"
	"sync"
)

// Central registry mapping agent-name patterns to modules. Used by
// ResolveAgentModule() when an agent does not implement NBAgentModuleProvider
// directly. This keeps Phase 0 classification in one place instead of
// sprinkling a one-line method across 150+ agent files.
//
// Rules:
//   - Exact name match wins over prefix match.
//   - Prefix matches are evaluated in order of registration (longer prefixes
//     should be registered before shorter ones).
//   - An agent can be tagged with multiple modules; the first is primary.
//
// External callers should NOT mutate the registry at runtime; it is populated
// once during init() with known agent groupings.

var (
	agentModuleRegistryMu sync.RWMutex
	agentModuleExact      = map[string][]AgentModule{}
	agentModulePrefix     []agentModulePrefixEntry
)

type agentModulePrefixEntry struct {
	prefix  string
	modules []AgentModule
}

// RegisterAgentModule tags an agent name exactly.
func RegisterAgentModule(agentName string, modules ...AgentModule) {
	if agentName == "" || len(modules) == 0 {
		return
	}
	agentModuleRegistryMu.Lock()
	defer agentModuleRegistryMu.Unlock()
	agentModuleExact[agentName] = modules
}

// RegisterAgentModulePrefix tags all agents whose name starts with the given
// prefix. Useful for broad groupings (e.g., "datadog_" → SRE).
func RegisterAgentModulePrefix(prefix string, modules ...AgentModule) {
	if prefix == "" || len(modules) == 0 {
		return
	}
	agentModuleRegistryMu.Lock()
	defer agentModuleRegistryMu.Unlock()
	agentModulePrefix = append(agentModulePrefix, agentModulePrefixEntry{
		prefix: prefix, modules: modules,
	})
}

// lookupAgentModules consults the registry. Returns nil if no match.
func lookupAgentModules(agentName string) []AgentModule {
	agentModuleRegistryMu.RLock()
	defer agentModuleRegistryMu.RUnlock()

	if mods, ok := agentModuleExact[agentName]; ok {
		return mods
	}
	for _, entry := range agentModulePrefix {
		if strings.HasPrefix(agentName, entry.prefix) {
			return entry.modules
		}
	}
	return nil
}

// init seeds the registry with known groupings. Add entries as new agents
// join specific modules. Agents absent from this list default to Generic.
//
// Registration order matters: lookup is first-match wins, so longer/more
// specific prefixes MUST be registered before shorter ones (e.g.,
// "aws_debug" before "aws_", otherwise the broad "aws_" entry shadows it).
func init() {
	// ── Multi-module entries — register first (longest prefixes) ─────────
	// Cloud debug agents span infrastructure investigation, cost analysis,
	// and observability — primary = CloudOps because they orchestrate
	// across the cloud surface; FinOps + Observability stay on the tag
	// list so memory queries scoped to those modules can still find rows.
	RegisterAgentModulePrefix("aws_debug", AgentModuleCloudOps, AgentModuleFinOps, AgentModuleObservability)
	RegisterAgentModulePrefix("gcp_debug", AgentModuleCloudOps, AgentModuleFinOps, AgentModuleObservability)
	RegisterAgentModulePrefix("azure_debug", AgentModuleCloudOps, AgentModuleFinOps, AgentModuleObservability)

	// Cloud-managed K8s straddles two domains: a kubectl call into EKS is
	// both K8sOps and CloudOps. Primary = K8sOps because the agent's verbs
	// (pod/service/deployment) are platform-shaped, not cloud-shaped.
	RegisterAgentModulePrefix("eks_", AgentModuleK8sOps, AgentModuleCloudOps)
	RegisterAgentModulePrefix("gke_", AgentModuleK8sOps, AgentModuleCloudOps)
	RegisterAgentModulePrefix("aks_", AgentModuleK8sOps, AgentModuleCloudOps)

	// ── Observability ────────────────────────────────────────────────────
	// Logs, metrics, traces, alerting, RCA — vendor-agnostic monitoring.
	RegisterAgentModulePrefix("prometheus_", AgentModuleObservability)
	RegisterAgentModulePrefix("promql_", AgentModuleObservability)
	RegisterAgentModulePrefix("datadog_", AgentModuleObservability)
	RegisterAgentModulePrefix("newrelic_", AgentModuleObservability)
	RegisterAgentModulePrefix("loki_", AgentModuleObservability)
	RegisterAgentModulePrefix("elastic", AgentModuleObservability)
	RegisterAgentModulePrefix("cloudwatch_", AgentModuleObservability)
	RegisterAgentModulePrefix("logs_", AgentModuleObservability)
	RegisterAgentModulePrefix("log_analysis", AgentModuleObservability)
	RegisterAgentModulePrefix("traces_", AgentModuleObservability)
	RegisterAgentModulePrefix("trace_", AgentModuleObservability)
	RegisterAgentModulePrefix("metrics_", AgentModuleObservability)
	RegisterAgentModulePrefix("metric_", AgentModuleObservability)
	RegisterAgentModulePrefix("alert", AgentModuleObservability)
	RegisterAgentModulePrefix("incident", AgentModuleObservability)
	RegisterAgentModulePrefix("rca", AgentModuleObservability)
	RegisterAgentModulePrefix("sre_", AgentModuleObservability)

	// ── Kubernetes / K8s Ops ─────────────────────────────────────────────
	// Pure platform tooling. Cloud-managed K8s flavors (eks_/gke_/aks_)
	// already multi-tagged above.
	RegisterAgentModulePrefix("k8s_", AgentModuleK8sOps)
	RegisterAgentModulePrefix("kube", AgentModuleK8sOps)
	RegisterAgentModulePrefix("helm_", AgentModuleK8sOps)
	RegisterAgentModulePrefix("argocd_", AgentModuleK8sOps)

	// ── Cloud Ops ────────────────────────────────────────────────────────
	// Provider-specific infrastructure: VPC/IAM/S3/compute/etc. The
	// "*_debug" variants are multi-tagged above; everything else under
	// these prefixes (e.g. aws_metric_recommendation) lands here.
	RegisterAgentModulePrefix("aws_", AgentModuleCloudOps)
	RegisterAgentModulePrefix("gcp_", AgentModuleCloudOps)
	RegisterAgentModulePrefix("azure_", AgentModuleCloudOps)

	// ── FinOps ───────────────────────────────────────────────────────────
	RegisterAgentModulePrefix("finops_", AgentModuleFinOps)
	RegisterAgentModulePrefix("cost_", AgentModuleFinOps)
	RegisterAgentModulePrefix("billing_", AgentModuleFinOps)
	RegisterAgentModulePrefix("budget_", AgentModuleFinOps)

	// ── Automation / workflow ────────────────────────────────────────────
	RegisterAgentModulePrefix("automation_", AgentModuleAutomation)
	RegisterAgentModulePrefix("workflow_", AgentModuleAutomation)
	RegisterAgentModulePrefix("runbook_", AgentModuleAutomation)
	RegisterAgentModulePrefix("playbook_", AgentModuleAutomation)
}
