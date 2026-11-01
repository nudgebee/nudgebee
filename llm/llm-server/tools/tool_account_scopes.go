package tools

import toolcore "nudgebee/llm/tools/core"

// Account scope of every tool registered in this package (see
// toolcore.AccountScope). A new tool must be declared here or the build's
// TestEveryToolDeclaresAnAccountScope fails.
func init() {
	toolcore.DeclareAccountScope(toolcore.AccountScopeFollowsTarget,
		"es_metrics_query", "splunk_metrics_query",
		"cloud_resource_search_execute", "kg_search_nodes",
		"aggregate_events", "anomaly_execute", "dryrun_triage_rule", "events_execute", "get_event_by_id",
		"get_event_classification", "get_event_evidence", "get_event_incident_assembly",
		"get_event_rules", "get_event_triage_explanation", "get_triage_rule_events", "get_triage_rules",
		"list_events", "list_threshold_suggestions", "logs_execute_v2", "metrics_label_values",
		"metrics_labels_list", "metrics_list", "metrics_series_match", "nudgebee_agent_health_get",
		"prometheus_execute", "propose_recommendation_apply", "recommendation_execute",
		"recommendation_resolution_execute", "search_metrics", "security_execute",
		"service_dependency_graph_execute", "signoz_log_execute", "spend_allocation", "spend_forecast",
		"spend_summary", "traces_execute", "traces_execute_chronosphere", "traces_execute_default",
		"traces_execute_jaeger", "traces_execute_v2",
	)
	toolcore.DeclareAccountScope(toolcore.AccountScopeOwnSelector,
		"aws_execute", "azure_execute", "gcloud_execute", "helm_execute", "kubectl_execute",
		"resource_search_execute", "shell_execute",
	)
	toolcore.DeclareAccountScope(toolcore.AccountScopeIndependent,
		"crawl_execute", "github_execute", "gitlab_execute",
		"kg_get_node", "kg_traverse", "load_skills", "mermaid_validation",
		"nudgebee_account_get", "nudgebee_accounts_count", "nudgebee_accounts_list",
		"nudgebee_docs_search", "nudgebee_integration_diagnose", "nudgebee_integration_get_status",
		"nudgebee_integrations_count", "nudgebee_integrations_list", "search_docs", "search_execute",
		"search_skills", "standard_diagnostic_grep", "think", "ticket_master", "ticket_master_v2",
		"update_notebook",
	)
	toolcore.DeclareOriginBound(toolcore.OriginBoundWrite,
		"recommendation_apply", "recommendation_execute_cli", "recommendation_record_ticket_resolution",
		"recommendation_set_dismissal", "remediation_execute", "remediation_generate", "trigger_cis_scan",
		"trigger_image_scan", "watch_cancel", "watch_resource",
	)
	// Run against the target account's own integration, picked by
	// ResolveToolCallTarget; a call names one with `instance`.
	toolcore.DeclareAccountScope(toolcore.AccountScopeFollowsTarget,
		"argocd_execute", "clickhouse_query_execute", "datadog_apm_entities_execute",
		"datadog_containers_execute", "datadog_events_execute", "datadog_hosts_execute",
		"datadog_incident_execute", "datadog_log_execute", "datadog_metrics_execute",
		"datadog_resource_search_execute", "datadog_services_execute", "datadog_software_catalog_execute",
		"datadog_traces_execute", "mongodb_current_op_get", "mongodb_replset_status_get",
		"mongodb_server_status_get", "mssql_query_execute", "mysql_query_execute",
		"newrelic_apm_services_execute", "newrelic_infrastructure_containers_execute",
		"newrelic_infrastructure_hosts_execute", "newrelic_resource_search_execute",
		"oracle_query_execute", "postgres_query_execute", "rabbit_execute", "redis_command_executer",
	)
	toolcore.DeclareOriginBound(toolcore.OriginBoundConversationState,
		"watch_list", "watch_status", "workflow_config_delete", "workflow_config_get",
		"workflow_config_list", "workflow_config_save", "workflow_create", "workflow_delete",
		"workflow_delete_version", "workflow_dry_run", "workflow_examples", "workflow_execution_cancel",
		"workflow_execution_get", "workflow_execution_retrigger", "workflow_executions", "workflow_get",
		"workflow_get_version", "workflow_list", "workflow_list_versions", "workflow_make_version_live",
		"workflow_pause", "workflow_publish", "workflow_restore_version", "workflow_resume",
		"workflow_search", "workflow_state", "workflow_task_execute", "workflow_task_list",
		"workflow_task_schema", "workflow_trigger", "workflow_update", "workflow_update_version_metadata",
		"workflow_validate",
	)
	toolcore.DeclareOriginBound(toolcore.OriginBoundNotYetSupported,
		"server_command_executor",
	)
	// VM tools read and run on the target's VM account (provider "vm").
	toolcore.DeclareAccountScope(toolcore.AccountScopeFollowsTarget, "vm_execute", "vm_list_execute")
}
