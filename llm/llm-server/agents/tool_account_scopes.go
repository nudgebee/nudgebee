package agents

import toolcore "nudgebee/llm/tools/core"

// Account scope of every tool registered in this package (see
// toolcore.AccountScope). A new tool must be declared here or the build's
// TestEveryToolDeclaresAnAccountScope fails.
//
// Categories: FollowsTarget reads data for NbToolContext.DataAccountID();
// AccountIndependent uses no account-level data; OwnSelector takes its own
// cluster/account argument; OriginBound(reason) acts only on the
// conversation's account and is refused for a call about another account.
func init() {
	toolcore.DeclareAccountScope(toolcore.AccountScopeFollowsTarget,
		"prometheus", "promql_query", "elastic_search_metrics", "splunk_enterprise_metrics",
		"aws_observability", "delegate_agent", "event_summary", "events", "fetch_logs_direct", "finops",
		"clickhouse", "logs", "metrics", "mssql", "mysql", "nudgebee", "oracle", "postgres", "security",
		"service_dependency_graph", "traces",
	)
	toolcore.DeclareAccountScope(toolcore.AccountScopeIndependent,
		"loganalysis",
		"ask_clarification", "github", "gitlab", "prompt_refinement", "search_tools", "tickets",
		"tickets_v2", "visualizer", "websearch",
	)
	toolcore.DeclareOriginBound(toolcore.OriginBoundWrite,
		"automation", "automation_builder", "remediation",
	)
	toolcore.DeclareOriginBound(toolcore.OriginBoundIntegration,
		"datadog_containers", "datadog_containers_query", "datadog_events", "datadog_hosts",
		"datadog_incident", "datadog_log_query", "datadog_logs", "datadog_metrics",
		"datadog_metrics_query", "datadog_orchestrator", "datadog_service", "datadog_software_catalog",
		"datadog_traces",
	)
	toolcore.DeclareOriginBound(toolcore.OriginBoundNotYetSupported,
		"code_analyzer", "evidenceinsightstool", "fetch_logs", "server")
}
