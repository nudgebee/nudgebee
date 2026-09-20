package agents

// resourceSearchToolUsage is the shared ToolUsage description for the direct
// resource_search_execute tool (tools.ToolResourceSearch), reused by every sub-agent
// that resolves resource names (kubectl, prometheus, traces, service_dependency).
//
// It documents the STRUCTURED JSON input the tool expects — NOT natural language,
// which was the interface of the removed resource_search agent (#32503 Phase 2).
// Kept in one place so the input contract can't drift per-agent.
var resourceSearchToolUsage = []string{
	"Resolve a resource name to its real identity/namespace from the inventory DB.",
	"Input: JSON with search_type ('fuzzy', 'suggestions', 'namespace', 'label'), plus resource_name, resource_type, namespace, label_selector (required for 'label'), and optional cluster (connected Kubernetes cluster name) as relevant.",
	"Output: JSON with resource suggestions, search strategies, and the actual target cluster provenance.",
	"Examples: {\"resource_type\": \"podss\", \"search_type\": \"fuzzy\"} or {\"resource_name\": \"llm-server\", \"namespace\": \"default\", \"search_type\": \"suggestions\"}",
}
