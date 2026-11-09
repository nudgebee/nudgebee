package core

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/lib/pq"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// capability_catalog.go — the merged, classified list of real platform
// capabilities the SOP reviewer (EPIC #38459) binds SOP steps to. See the
// design spec, "sop runbook agentification overview.md", Part II §3.3/§4.
//
// Both underlying catalogs already exist and are NOT built here:
//   - agent tools: nbSystemTools (tool_factory.go, this package) — the same
//     registry the ai_list_tools action and the ReAct planner use.
//   - workflow tasks: runbook-server's GET /tasks — already called
//     internally by the workflow_task_list NBTool (tools/tool_workflow.go)
//     via DoRunbookRequest. Fetched directly here (not through that
//     function) because tools/core cannot import package tools without
//     creating an import cycle (tools already imports tools/core); rag-server
//     calls elsewhere in this file's package (see rag_service.go) already
//     establish the same "tools/core makes its own outbound HTTP calls"
//     pattern this follows.
//
// What's new is scope classification: is a capability typed/bounded
// ("narrow"), or does it take a single free-text command/script/query field
// covering an entire service surface ("pass_through")? That distinction is
// what lets sop_reviewer cap a step at approval-gated regardless of how safe
// the specific command looks (TOOLMAP-005 in the design spec).
//
// capabilityScopeTable is hand-authored and checked in, not computed from
// schema shape at runtime. Two adversarial reviews during design rejected a
// live heuristic: runbook-server's scripting.run_script has one required
// field ("script", arbitrary bash/JS/Python/PowerShell across five execution
// surfaces) alongside thirteen OPTIONAL fields — a naive "count all fields"
// implementation would classify arbitrary remote code execution as narrow.
// Every entry below was added after reading its actual InputSchema in
// source, not inferred from its name or description.

// CapabilitySurface distinguishes which registry a capability comes from.
type CapabilitySurface string

const (
	CapabilitySurfaceAgentTool    CapabilitySurface = "agent_tool"
	CapabilitySurfaceWorkflowTask CapabilitySurface = "workflow_task"
)

// CapabilityScope is the platform's classification of a capability's blast
// radius.
type CapabilityScope string

const (
	// CapabilityScopeNarrow: typed, bounded parameters.
	CapabilityScopeNarrow CapabilityScope = "narrow"
	// CapabilityScopePassThrough: a single free-text command/script/query
	// field covering an entire service surface. Capped at approval-gated
	// regardless of what the specific command does.
	CapabilityScopePassThrough CapabilityScope = "pass_through"
	// CapabilityScopeUnclassified is returned for any capability not present
	// in capabilityScopeTable. Callers MUST treat this identically to "no
	// capability exists" (a gap) — never as narrow. Misclassifying a
	// pass-through capability as narrow is the dangerous direction; treating
	// an unreviewed capability as a gap is always the safe default.
	CapabilityScopeUnclassified CapabilityScope = "unclassified"
)

// CapabilityEntry is one real, callable platform capability.
type CapabilityEntry struct {
	Surface     CapabilitySurface `json:"surface"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Scope       CapabilityScope   `json:"scope"`
}

// capabilityScopeTable is the source of truth for scope classification.
// Add an entry only after reading the capability's actual InputSchema in
// source — do not classify from its name or description alone. Anything
// not listed here resolves to CapabilityScopeUnclassified.
var capabilityScopeTable = map[string]CapabilityScope{
	// --- Agent tools (llm/llm-server/tools/) ---
	// Single required, untyped "command" string, no other fields.
	"kubectl_execute": CapabilityScopePassThrough, // tool_kubectl.go
	"aws_execute":     CapabilityScopePassThrough, // tool_cloud_aws.go
	"gcloud_execute":  CapabilityScopePassThrough, // tool_cloud_gcp.go
	"azure_execute":   CapabilityScopePassThrough, // tool_cloud_azure.go
	"github_execute":  CapabilityScopePassThrough, // tool_github.go
	"gitlab_execute":  CapabilityScopePassThrough, // tool_gitlab.go
	"shell_execute":   CapabilityScopePassThrough, // tool_shell.go
	"helm_execute":    CapabilityScopePassThrough, // tool_helm.go: Required: []string{"command"}
	"rabbit_execute":  CapabilityScopePassThrough, // tool_rabbit.go: Required: []string{"args"}
	// A sub-agent registered as a tool (agents/agent_remediation.go): input is
	// free-text investigation context or a request to execute a plan, and it
	// runs approved commands. Its built-in approval flow does not cap what it
	// can reach, so it is classified by its ceiling.
	"remediation": CapabilityScopePassThrough,

	// Sub-agents registered as tools take a natural-language question, so their
	// input shape says nothing about blast radius. They are classified by the
	// ceiling of the tools in their GetSupportedTools (verified in source, not
	// inferred from names). A read-only exemption is deliberately not modelled
	// yet: it needs a reviewer to consume it and a code-level enforcement point.
	//   agents/agent_log.go        -> includes shell_execute (pass_through above)
	//   agents/agent_events.go     -> includes EventsExecuteTool (free-text SQL)
	//   agents/agent_prometheus.go -> includes PrometheusExecuteTool (free-text PromQL)
	"logs":       CapabilityScopePassThrough,
	"events":     CapabilityScopePassThrough,
	"prometheus": CapabilityScopePassThrough,
	// tool_prometheus.go: Required: []string{"command"}, an untyped PromQL string.
	"prometheus_execute": CapabilityScopePassThrough,
	// agents/agent_promql.go: generate-only (prompt forbids execution) and its
	// own tools are bounded (MetricsListTool, ListMetricsLabelsTool), but it is
	// a ReAct agent that does not opt out of default tools, so
	// FilterAndInjectDefaultTools (agents/core/utils.go) gives it shell_execute.
	"promql_query": CapabilityScopePassThrough,

	// --- Workflow tasks (runbook-server/internal/tasks/) ---
	// k8s/cli.go: only "command" (string) is required.
	"k8s.cli": CapabilityScopePassThrough,
	// scripting/run_script_task.go: ONE required field ("script") alongside
	// 13 OPTIONAL fields (executor_type, language, env, target_id, region,
	// ...). This is the entry that broke a naive "count all fields"
	// heuristic during design review (see package doc comment above).
	"scripting.run_script": CapabilityScopePassThrough,

	// k8s/workload_restart.go: three REQUIRED, typed/enum-constrained fields
	// (namespace, kind [Options: Pod/Deployment/DaemonSet/StatefulSet/
	// ReplicaSet], name) — a purpose-built, bounded action.
	"k8s.workload_restart": CapabilityScopeNarrow,
	// k8s/horizontal_rightsize.go: multiple required, typed fields.
	"k8s.horizontal_rightsize": CapabilityScopeNarrow,

	// Database tools: one free-text query/command field over a whole database
	// (or Redis instance). The SQL tools reject writes in code
	// (sqlValidateReadOnly), but a read-only exemption is not modelled yet (see
	// "logs"/"events" above), so they are classified by schema shape like every
	// other free-text tool. Schemas read in source:
	//   tool_postgres.go, tool_mysql.go, tool_mssql.go, tool_oracle.go,
	//   tool_clickhouse.go: command|query (RequiredOneOf), plus database/instance.
	//   tool_redis.go: command Required, plus instance.
	"postgres_query_execute":   CapabilityScopePassThrough,
	"mysql_query_execute":      CapabilityScopePassThrough,
	"mssql_query_execute":      CapabilityScopePassThrough,
	"oracle_query_execute":     CapabilityScopePassThrough,
	"clickhouse_query_execute": CapabilityScopePassThrough,
	"redis_command_executer":   CapabilityScopePassThrough,

	// Free-text SQL / CLI / query tools (agent tools). Each takes one free-text field that is run
	// against a whole service surface; schemas and Call() read in source.
	"anomaly_execute":                   CapabilityScopePassThrough,
	"argocd_execute":                    CapabilityScopePassThrough,
	"crawl_execute":                     CapabilityScopePassThrough,
	"search_execute":                    CapabilityScopePassThrough,
	"datadog_log_execute":               CapabilityScopePassThrough,
	"datadog_metrics_execute":           CapabilityScopePassThrough,
	"datadog_traces_execute":            CapabilityScopePassThrough,
	"events_execute":                    CapabilityScopePassThrough,
	"logs_execute_v2":                   CapabilityScopePassThrough,
	"recommendation_execute":            CapabilityScopePassThrough,
	"recommendation_resolution_execute": CapabilityScopePassThrough,
	"remediation_execute":               CapabilityScopePassThrough,
	"security_execute":                  CapabilityScopePassThrough,
	"server_command_executor":           CapabilityScopePassThrough,
	"signoz_log_execute":                CapabilityScopePassThrough,
	"splunk_metrics_query":              CapabilityScopePassThrough,
	"ticket_master":                     CapabilityScopePassThrough,
	"traces_execute":                    CapabilityScopePassThrough,
	"traces_execute_chronosphere":       CapabilityScopePassThrough,
	"traces_execute_default":            CapabilityScopePassThrough,
	"traces_execute_jaeger":             CapabilityScopePassThrough,
	"traces_execute_v2":                 CapabilityScopePassThrough,
	"vm_execute":                        CapabilityScopePassThrough,
	"watch_resource":                    CapabilityScopePassThrough,
	"es_metrics_query":                  CapabilityScopePassThrough,
	"recommendation_execute_cli":        CapabilityScopePassThrough,
	"datadog_hosts_execute":             CapabilityScopeNarrow,
	"kg_search_nodes":                   CapabilityScopeNarrow,
	"kg_traverse":                       CapabilityScopeNarrow,
	"mermaid_validation":                CapabilityScopeNarrow,
	"metrics_labels_list":               CapabilityScopeNarrow,
	"metrics_list":                      CapabilityScopeNarrow,
	"nudgebee_docs_search":              CapabilityScopeNarrow,
	"remediation_generate":              CapabilityScopeNarrow,
	"search_docs":                       CapabilityScopeNarrow,
	"search_metrics":                    CapabilityScopeNarrow,
	"search_skills":                     CapabilityScopeNarrow,
	"service_dependency_graph_execute":  CapabilityScopeNarrow,
	"trigger_cis_scan":                  CapabilityScopeNarrow,
	"trigger_image_scan":                CapabilityScopeNarrow,
	"workflow_search":                   CapabilityScopeNarrow,

	// Datadog / New Relic *_execute tools: the free-text field only filters one fixed read
	// endpoint, so narrow is arguable, but the input shape is a single free-text field and
	// prometheus_execute (also read-only) is pass_through, so they follow the shape. Flip to
	// narrow only after the table's owner decides read-only bounded filters qualify.
	"datadog_apm_entities_execute":               CapabilityScopePassThrough,
	"datadog_containers_execute":                 CapabilityScopePassThrough,
	"datadog_events_execute":                     CapabilityScopePassThrough,
	"datadog_incident_execute":                   CapabilityScopePassThrough,
	"datadog_services_execute":                   CapabilityScopePassThrough,
	"datadog_software_catalog_execute":           CapabilityScopePassThrough,
	"datadog_resource_search_execute":            CapabilityScopePassThrough,
	"newrelic_apm_services_execute":              CapabilityScopePassThrough,
	"newrelic_infrastructure_containers_execute": CapabilityScopePassThrough,
	"newrelic_infrastructure_hosts_execute":      CapabilityScopePassThrough,
	"newrelic_resource_search_execute":           CapabilityScopePassThrough,

	// Workflow executors: each can create, change or run arbitrary workflows and
	// tasks, so they are pass_through despite their typed ids and objects.
	"workflow_create":              CapabilityScopePassThrough,
	"workflow_update":              CapabilityScopePassThrough,
	"workflow_dry_run":             CapabilityScopePassThrough,
	"workflow_execution_retrigger": CapabilityScopePassThrough,
	"workflow_task_execute":        CapabilityScopePassThrough,
	"workflow_trigger":             CapabilityScopePassThrough,

	// Typed agent tools: ids, enums and filters only, fixed actions or fixed read queries.
	"aggregate_events":                        CapabilityScopeNarrow,
	"cloud_resource_search_execute":           CapabilityScopeNarrow,
	"dryrun_triage_rule":                      CapabilityScopeNarrow,
	"get_event_by_id":                         CapabilityScopeNarrow,
	"get_event_classification":                CapabilityScopeNarrow,
	"get_event_evidence":                      CapabilityScopeNarrow,
	"get_event_incident_assembly":             CapabilityScopeNarrow,
	"get_event_rules":                         CapabilityScopeNarrow,
	"get_event_triage_explanation":            CapabilityScopeNarrow,
	"get_triage_rule_events":                  CapabilityScopeNarrow,
	"get_triage_rules":                        CapabilityScopeNarrow,
	"kg_get_node":                             CapabilityScopeNarrow,
	"list_events":                             CapabilityScopeNarrow,
	"list_threshold_suggestions":              CapabilityScopeNarrow,
	"load_skills":                             CapabilityScopeNarrow,
	"metrics_label_values":                    CapabilityScopeNarrow,
	"metrics_series_match":                    CapabilityScopeNarrow,
	"mongodb_current_op_get":                  CapabilityScopeNarrow,
	"mongodb_replset_status_get":              CapabilityScopeNarrow,
	"mongodb_server_status_get":               CapabilityScopeNarrow,
	"nudgebee_account_get":                    CapabilityScopeNarrow,
	"nudgebee_accounts_count":                 CapabilityScopeNarrow,
	"nudgebee_accounts_list":                  CapabilityScopeNarrow,
	"nudgebee_agent_health_get":               CapabilityScopeNarrow,
	"nudgebee_integration_diagnose":           CapabilityScopeNarrow,
	"nudgebee_integration_get_status":         CapabilityScopeNarrow,
	"nudgebee_integrations_count":             CapabilityScopeNarrow,
	"nudgebee_integrations_list":              CapabilityScopeNarrow,
	"propose_recommendation_apply":            CapabilityScopeNarrow,
	"recommendation_record_ticket_resolution": CapabilityScopeNarrow,
	"recommendation_set_dismissal":            CapabilityScopeNarrow,
	"resource_search_execute":                 CapabilityScopeNarrow,
	"spend_allocation":                        CapabilityScopeNarrow,
	"spend_forecast":                          CapabilityScopeNarrow,
	"spend_summary":                           CapabilityScopeNarrow,
	"think":                                   CapabilityScopeNarrow,
	"update_notebook":                         CapabilityScopeNarrow,
	"vm_list_execute":                         CapabilityScopeNarrow,
	"watch_cancel":                            CapabilityScopeNarrow,
	"watch_list":                              CapabilityScopeNarrow,
	"watch_status":                            CapabilityScopeNarrow,
	"workflow_config_delete":                  CapabilityScopeNarrow,
	"workflow_config_get":                     CapabilityScopeNarrow,
	"workflow_config_list":                    CapabilityScopeNarrow,
	"workflow_delete":                         CapabilityScopeNarrow,
	"workflow_delete_version":                 CapabilityScopeNarrow,
	"workflow_examples":                       CapabilityScopeNarrow,
	"workflow_execution_cancel":               CapabilityScopeNarrow,
	"workflow_execution_get":                  CapabilityScopeNarrow,
	"workflow_executions":                     CapabilityScopeNarrow,
	"workflow_get":                            CapabilityScopeNarrow,
	"workflow_get_version":                    CapabilityScopeNarrow,
	"workflow_list":                           CapabilityScopeNarrow,
	"workflow_list_versions":                  CapabilityScopeNarrow,
	"workflow_pause":                          CapabilityScopeNarrow,
	"workflow_resume":                         CapabilityScopeNarrow,
	"workflow_state":                          CapabilityScopeNarrow,
	"workflow_task_list":                      CapabilityScopeNarrow,
	"workflow_task_schema":                    CapabilityScopeNarrow,
	"workflow_update_version_metadata":        CapabilityScopeNarrow,

	// Agents and tools exposed as tools, classified by the ceiling of what they can reach. A ReAct
	// or Orchestrating agent that does not opt out of default tools also gets shell_execute
	// injected (agents/core/utils.go FilterAndInjectDefaultTools), so it inherits pass_through
	// even when its own tools are bounded. The narrow ones below are not caught by that: llm,
	// loganalysis, visualizer and clarification use the custom planner, datadog_containers_query
	// and datadog_log_query the tools planner (neither gets default tools injected), and
	// nudgebee and service_dependency_graph are ReAct agents that opt out
	// (OptOutDefaultTools). Checked against the registered agents, not read off names.
	"automation":                 CapabilityScopePassThrough,
	"aws_observability":          CapabilityScopePassThrough,
	"clickhouse":                 CapabilityScopePassThrough,
	"code_analyzer":              CapabilityScopePassThrough,
	"datadog_containers":         CapabilityScopePassThrough,
	"datadog_events":             CapabilityScopePassThrough,
	"datadog_hosts":              CapabilityScopePassThrough,
	"datadog_incident":           CapabilityScopePassThrough,
	"datadog_logs":               CapabilityScopePassThrough,
	"datadog_metrics":            CapabilityScopePassThrough,
	"datadog_metrics_query":      CapabilityScopePassThrough,
	"datadog_orchestrator":       CapabilityScopePassThrough,
	"datadog_service":            CapabilityScopePassThrough,
	"datadog_software_catalog":   CapabilityScopePassThrough,
	"datadog_traces":             CapabilityScopePassThrough,
	"delegate_agent":             CapabilityScopePassThrough,
	"elastic_search_metrics":     CapabilityScopePassThrough,
	"fetch_logs":                 CapabilityScopePassThrough,
	"fetch_logs_direct":          CapabilityScopePassThrough,
	"finops":                     CapabilityScopePassThrough,
	"github":                     CapabilityScopePassThrough,
	"gitlab":                     CapabilityScopePassThrough,
	"metrics":                    CapabilityScopePassThrough,
	"mssql":                      CapabilityScopePassThrough,
	"mysql":                      CapabilityScopePassThrough,
	"oracle":                     CapabilityScopePassThrough,
	"postgres":                   CapabilityScopePassThrough,
	"prompt_refinement":          CapabilityScopePassThrough,
	"security":                   CapabilityScopePassThrough,
	"server":                     CapabilityScopePassThrough,
	"signoz_logs":                CapabilityScopePassThrough,
	"splunk_enterprise_metrics":  CapabilityScopePassThrough,
	"tickets":                    CapabilityScopePassThrough,
	"tickets_v2":                 CapabilityScopePassThrough,
	"traces":                     CapabilityScopePassThrough,
	"ask_clarification":          CapabilityScopeNarrow,
	"clarification":              CapabilityScopeNarrow,
	"datadog_containers_query":   CapabilityScopeNarrow,
	"datadog_log_query":          CapabilityScopeNarrow,
	"evidenceinsightstool":       CapabilityScopeNarrow,
	"llm":                        CapabilityScopeNarrow,
	"loganalysis":                CapabilityScopeNarrow,
	"memory_read":                CapabilityScopeNarrow,
	"memory_write":               CapabilityScopeNarrow,
	"nudgebee":                   CapabilityScopeNarrow,
	"service_dependency_graph":   CapabilityScopeNarrow,
	"signoz_log_query_generator": CapabilityScopeNarrow,
	"visualizer":                 CapabilityScopeNarrow,

	// Workflow tasks (runbook-server/internal/tasks): CLI/SSH/HTTP/SQL/query tasks, tasks that run
	// arbitrary nested tasks or workflows, and tasks that run arbitrary code or tool-using LLMs
	// are pass_through.
	"aws.cli":                           CapabilityScopePassThrough,
	"azure.cli":                         CapabilityScopePassThrough,
	"cicd.argocd.cli":                   CapabilityScopePassThrough,
	"cloud.aws.cli":                     CapabilityScopePassThrough,
	"cloud.azure.cli":                   CapabilityScopePassThrough,
	"cloud.gcp.cli":                     CapabilityScopePassThrough,
	"cloud.k8s.cli":                     CapabilityScopePassThrough,
	"core.call-workflow":                CapabilityScopePassThrough,
	"core.foreach":                      CapabilityScopePassThrough,
	"core.group":                        CapabilityScopePassThrough,
	"data.filter":                       CapabilityScopePassThrough,
	"data.transform":                    CapabilityScopePassThrough,
	"dbms.query":                        CapabilityScopePassThrough,
	"dbms.redis.cli":                    CapabilityScopePassThrough,
	"gcp.cli":                           CapabilityScopePassThrough,
	"integrations.http":                 CapabilityScopePassThrough,
	"integrations.ssh":                  CapabilityScopePassThrough,
	"llm.a2a_call":                      CapabilityScopePassThrough,
	"llm.investigate":                   CapabilityScopePassThrough,
	"llm.mcp_call":                      CapabilityScopePassThrough,
	"llm.nubi":                          CapabilityScopePassThrough,
	"llm.router":                        CapabilityScopePassThrough,
	"mq.rabbitmqadmin.cli":              CapabilityScopePassThrough,
	"network.tcp":                       CapabilityScopePassThrough,
	"observability.logs":                CapabilityScopePassThrough,
	"observability.metrics":             CapabilityScopePassThrough,
	"observability.traces":              CapabilityScopePassThrough,
	"scm.github.cli":                    CapabilityScopePassThrough,
	"scm.gitlab.cli":                    CapabilityScopePassThrough,
	"core.approval":                     CapabilityScopeNarrow,
	"core.print":                        CapabilityScopeNarrow,
	"core.switch":                       CapabilityScopeNarrow,
	"core.wait":                         CapabilityScopeNarrow,
	"crypto.decode":                     CapabilityScopeNarrow,
	"crypto.decrypt":                    CapabilityScopeNarrow,
	"crypto.encode":                     CapabilityScopeNarrow,
	"crypto.encrypt":                    CapabilityScopeNarrow,
	"crypto.hash":                       CapabilityScopeNarrow,
	"events.add_evidence":               CapabilityScopeNarrow,
	"events.store":                      CapabilityScopeNarrow,
	"google_chat.join_space":            CapabilityScopeNarrow,
	"k8s.continuous_rightsize":          CapabilityScopeNarrow,
	"k8s.node_graceful_shutdown":        CapabilityScopeNarrow,
	"k8s.pod_delete":                    CapabilityScopeNarrow,
	"k8s.profile":                       CapabilityScopeNarrow,
	"k8s.pv_rightsize":                  CapabilityScopeNarrow,
	"k8s.vertical_rightsize":            CapabilityScopeNarrow,
	"network.dns":                       CapabilityScopeNarrow,
	"network.ntp":                       CapabilityScopeNarrow,
	"network.ping":                      CapabilityScopeNarrow,
	"network.ssl":                       CapabilityScopeNarrow,
	"network.traceroute":                CapabilityScopeNarrow,
	"network.whois":                     CapabilityScopeNarrow,
	"notifications.add_channel_members": CapabilityScopeNarrow,
	"notifications.add_reaction":        CapabilityScopeNarrow,
	"notifications.create_channel":      CapabilityScopeNarrow,
	"notifications.dm":                  CapabilityScopeNarrow,
	"notifications.email":               CapabilityScopeNarrow,
	"notifications.im":                  CapabilityScopeNarrow,
	"notifications.read_thread":         CapabilityScopeNarrow,
	"observability.log_groups":          CapabilityScopeNarrow,
	"slack.join_channel":                CapabilityScopeNarrow,
	"tickets.acknowledge":               CapabilityScopeNarrow,
	"tickets.add_comment":               CapabilityScopeNarrow,
	"tickets.assign":                    CapabilityScopeNarrow,
	"tickets.create":                    CapabilityScopeNarrow,
	"tickets.escalate":                  CapabilityScopeNarrow,
	"tickets.get":                       CapabilityScopeNarrow,
	"tickets.get_comments":              CapabilityScopeNarrow,
	"tickets.resolve":                   CapabilityScopeNarrow,
	"tickets.transition":                CapabilityScopeNarrow,
	"tickets.update":                    CapabilityScopeNarrow,
	"vertical_rightsize_generate":       CapabilityScopeNarrow,
}

// ClassifyCapability returns the checked-in scope for a capability name.
// found is false for anything not yet in capabilityScopeTable; callers must
// treat that as CapabilityScopeUnclassified, never assume narrow.
func ClassifyCapability(name string) (scope CapabilityScope, found bool) {
	if scope, found = capabilityScopeTable[name]; found {
		return scope, true
	}
	return CapabilityScopeUnclassified, false
}

// GetCapabilityCatalog returns every real, classified platform capability
// sop_reviewer can bind an SOP step to: every registered agent tool merged
// with every registered runbook-server workflow task.
//
// Scoped deliberately to the fixed, global platform capability set —
// tenant-configured custom/MCP tools (see ListCustomTools,
// ListMCPIntegrationToolDtos) are out of scope for this first pass, since
// classification here is global, not tenant-aware (a known, deliberate
// limitation; tenant_automation_policy is the intended future integration
// point if that's ever needed).
//
// Fails fast, before any classification or LLM work, if either source is
// unreachable — an incomplete catalog produces false "gap" findings (PRE-003
// in the design spec), which is worse than not reviewing at all.
func GetCapabilityCatalog(ctx *security.RequestContext, accountId string) ([]CapabilityEntry, error) {
	tools := listAgentToolCapabilities(ctx, accountId)

	tasks, err := listWorkflowTaskCapabilities(ctx, accountId)
	if err != nil {
		return nil, fmt.Errorf("capability_catalog: failed to list workflow tasks: %w", err)
	}

	entries := make([]CapabilityEntry, 0, len(tools)+len(tasks))
	entries = append(entries, tools...)
	entries = append(entries, tasks...)
	return entries, nil
}

// listAgentToolCapabilities enumerates nbSystemTools directly (not
// ListTools/ListToolsForTenant, which also mix in tenant-configured
// custom/MCP tools — out of scope here, see GetCapabilityCatalog doc
// comment). A single tool failing to construct is logged and skipped rather
// than failing the whole catalog fetch.
func listAgentToolCapabilities(ctx *security.RequestContext, accountId string) []CapabilityEntry {
	entries := make([]CapabilityEntry, 0, len(nbSystemTools))
	for name, factory := range nbSystemTools {
		// Isolated behind its own recover so one misbehaving factory (panic,
		// not just an error return) can't abort the whole catalog sync —
		// same "skip and keep going" contract as the err != nil branch below.
		func() {
			defer func() {
				if r := recover(); r != nil {
					ctx.GetLogger().Error("capability_catalog: tool factory panicked, skipping",
						"tool", name, "panic", r, "stack", string(debug.Stack()))
				}
			}()
			tool, err := factory(accountId)
			if err != nil {
				ctx.GetLogger().Warn("capability_catalog: failed to construct tool, skipping", "tool", name, "error", err)
				return
			}
			scope, _ := ClassifyCapability(name)
			entries = append(entries, CapabilityEntry{
				Surface:     CapabilitySurfaceAgentTool,
				Name:        name,
				Description: tool.Description(),
				Scope:       scope,
			})
		}()
	}
	return entries
}

// runbookTaskDefinition mirrors the fields we need from runbook-server's
// TaskDefinition (runbook-server/internal/model/workflow.go) — the wire
// shape of GET /tasks.
type runbookTaskDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type runbookTaskListResponse struct {
	Tasks []runbookTaskDefinition `json:"tasks"`
}

func listWorkflowTaskCapabilities(ctx *security.RequestContext, accountId string) ([]CapabilityEntry, error) {
	var tenantId, userId string
	if sc := ctx.GetSecurityContext(); sc != nil {
		tenantId, userId = sc.GetTenantId(), sc.GetUserId()
	}
	respBytes, err := doRunbookTasksRequest(ctx, accountId, tenantId, userId)
	if err != nil {
		return nil, err
	}

	var parsed runbookTaskListResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse runbook-server task list: %w", err)
	}

	if len(parsed.Tasks) == 0 {
		// A live runbook-server always has a baseline of registered task
		// types; a 200 with zero tasks is far more likely a pod still
		// starting up mid-rollout than a genuine "no workflow tasks exist"
		// state. Treated as a fetch failure, same as a non-200 above,
		// because SyncCapabilityCatalog's knownNames-membership deletion
		// would otherwise read this as "every workflow_task capability was
		// removed" and delete all of them — len(catalog) > 0 alone doesn't
		// catch this, since the agent-tool half keeps the catalog non-empty.
		return nil, fmt.Errorf("runbook-server returned no tasks; refusing to treat as an empty catalog")
	}

	entries := make([]CapabilityEntry, 0, len(parsed.Tasks))
	for _, task := range parsed.Tasks {
		scope, _ := ClassifyCapability(task.Name)
		entries = append(entries, CapabilityEntry{
			Surface:     CapabilitySurfaceWorkflowTask,
			Name:        task.Name,
			Description: task.Description,
			Scope:       scope,
		})
	}
	return entries, nil
}

// doRunbookTasksRequest is a minimal GET against runbook-server's task
// registry. Mirrors tools/tool_workflow.go's DoRunbookRequest (same target
// service, same header contract) rather than calling it directly, since
// package tools already imports tools/core and importing back would create
// a cycle.
func doRunbookTasksRequest(ctx *security.RequestContext, accountId, tenantId, userId string) ([]byte, error) {
	url := strings.TrimSpace(config.Config.WorkflowServerEndpoint)
	if url != "" && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "http://" + url
	}
	url = strings.TrimSuffix(url, "/") + "/tasks"

	req, err := http.NewRequestWithContext(ctx.GetContext(), http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("X-Account-ID", accountId)
	req.Header.Set("X-Tenant-ID", tenantId)
	if userId != "" {
		req.Header.Set("X-User-ID", userId)
	}

	resp, err := common.HttpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to reach runbook server: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read runbook server response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("runbook server returned error (status %d): %s", resp.StatusCode, string(respBytes))
	}
	return respBytes, nil
}

// UnclassifiedCapabilities returns the names of every capability the live
// catalog reports that capabilityScopeTable does not yet cover. This is the
// visible signal for classification drift between this checked-in table and
// what's actually registered in either service — surface it (a startup log
// line, a metric, a periodic check) rather than letting new capabilities
// sit invisibly unclassified. Deliberately does not attempt to guess a
// classification for them; see the package doc comment for why.
func UnclassifiedCapabilities(ctx *security.RequestContext, accountId string) ([]string, error) {
	catalog, err := GetCapabilityCatalog(ctx, accountId)
	if err != nil {
		return nil, err
	}
	var unclassified []string
	for _, entry := range catalog {
		if entry.Scope == CapabilityScopeUnclassified {
			unclassified = append(unclassified, entry.Name)
		}
	}
	return unclassified, nil
}

// SyncCapabilityCatalog writes the live, classified capability catalog into
// llm_kb_capability_entries (migration V977) as entry_type = 'platform_capability'
// rows — the shared, polymorphic table rag-server reads directly over its own
// metastore connection (the same table also holds rag-server's own
// entry_type = 'kb_collection' rows — see the migration comment for why these
// live together), mirroring how rag-server already reads
// `integrations`/`integration_config_values` directly rather than calling
// back into llm-server (no reverse-direction HTTP dependency exists
// anywhere in this codebase; this follows that same pattern instead of
// introducing one).
//
// Meant to run once at startup, not on a timer: the underlying catalog only
// changes when a tool/task ships, i.e. on deploy, not per-request.
//
// Uses an empty accountId — Name()/Description()/InputSchema() are static
// per tool implementation and shouldn't depend on account context, but this
// assumption hasn't been verified against every one of the ~130 registered
// tool constructors; worth a spot-check if a factory ever panics here.
func SyncCapabilityCatalog(ctx *security.RequestContext) error {
	catalog, err := GetCapabilityCatalog(ctx, "")
	if err != nil {
		return fmt.Errorf("capability_catalog: failed to build catalog: %w", err)
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("capability_catalog: failed to get database manager: %w", err)
	}

	for _, entry := range catalog {
		if err := upsertCapabilityEntry(ctx, dbms, entry); err != nil {
			return fmt.Errorf("capability_catalog: failed to upsert %q: %w", entry.Name, err)
		}
	}

	// A tool/task removed from the live registries must stop being a valid
	// grounding target — otherwise a deleted capability's row lingers forever
	// and the hallucination guard keeps accepting a name that no longer
	// exists. Skipped when catalog is empty as an extra guard against ever
	// truncating the table on a degenerate run — GetCapabilityCatalog already
	// fails fast rather than returning empty.
	//
	// Deletion is by name-set membership, not by a syncStarted timestamp
	// comparison — that earlier approach broke two ways: (1) comparing a
	// client-side time.Now() against updated_at columns Postgres stamped
	// with its own NOW() is a cross-clock comparison, and on a host whose
	// local timezone isn't UTC (confirmed live: IST, UTC+5:30) the client
	// timestamp came out far enough ahead that every row this sync had just
	// upserted immediately read as "older than syncStarted" — reproduced
	// live against a real DB, 219/219 rows upserted then 219/219 deleted one
	// statement later; (2) even with a correct clock, "not touched this
	// sync" also matches a tool that's still registered but failed to
	// construct (listAgentToolCapabilities skips it) — its previously-good
	// row would be deleted for a transient failure, not a real removal.
	// knownNames sidesteps both: every currently-registered tool name
	// counts as known regardless of whether it constructed successfully
	// this run, so a construction failure only leaves that tool's
	// description stale until the next successful sync, never deletes it.
	if len(catalog) > 0 {
		knownNames := make([]string, 0, len(nbSystemTools)+len(catalog))
		for name := range nbSystemTools {
			knownNames = append(knownNames, name)
		}
		for _, entry := range catalog {
			if entry.Surface == CapabilitySurfaceWorkflowTask {
				knownNames = append(knownNames, entry.Name)
			}
		}
		if err := deleteObsoleteCapabilityEntries(ctx, dbms, knownNames); err != nil {
			return fmt.Errorf("capability_catalog: failed to remove obsolete entries: %w", err)
		}
	}

	ctx.GetLogger().Info("capability_catalog: synced", "count", len(catalog))
	return nil
}

// deleteObsoleteCapabilityEntries removes every platform_capability row
// whose name isn't in knownNames — the full currently-registered set (see
// SyncCapabilityCatalog for why that's nbSystemTools' keys plus the fetched
// workflow tasks, not just the entries that made it into this run's
// catalog). Scoped to entry_type = 'platform_capability' — this table also
// holds rag-server's entry_type = 'kb_collection' rows, which must never be
// touched by this sync. Split out from SyncCapabilityCatalog so the SQL
// shape is testable on its own — same rationale as upsertCapabilityEntry
// below.
func deleteObsoleteCapabilityEntries(ctx *security.RequestContext, dbms *common.DatabaseManager, knownNames []string) error {
	_, err := dbms.Db.ExecContext(ctx.GetContext(),
		`DELETE FROM llm_kb_capability_entries WHERE entry_type = 'platform_capability' AND NOT (name = ANY($1))`,
		pq.Array(knownNames))
	return err
}

// upsertCapabilityEntry writes one entry_type = 'platform_capability' row of
// llm_kb_capability_entries. The ON CONFLICT target is the partial unique index
// on (name) WHERE entry_type = 'platform_capability' — the table's other
// entry type (kb_collection) has its own, disjoint partial unique index on
// (collection_name), so this can never collide with a kb_collection row.
// Split out from SyncCapabilityCatalog so the SQL shape is testable against
// a single row instead of the full, dynamically-sized live registry.
func upsertCapabilityEntry(ctx *security.RequestContext, dbms *common.DatabaseManager, entry CapabilityEntry) error {
	_, err := dbms.Db.ExecContext(ctx.GetContext(), `
		INSERT INTO llm_kb_capability_entries (entry_type, name, surface, scope, description, updated_at)
		VALUES ('platform_capability', $1, $2, $3, $4, NOW())
		ON CONFLICT (name) WHERE entry_type = 'platform_capability' DO UPDATE
		SET surface = EXCLUDED.surface, scope = EXCLUDED.scope,
		    description = EXCLUDED.description, updated_at = NOW()`,
		entry.Name, entry.Surface, entry.Scope, entry.Description)
	return err
}
