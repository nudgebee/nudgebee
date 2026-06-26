package agents

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strconv"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

const EventsAgentName = "events"

func init() {
	// This describes the 'events' agent when it is used as a tool by another agent (e.g., k8s_debug).
	toolDescription := `Retrieves and summarizes Kubernetes, Anomaly, SLO, and configuration event data. This tool is "smart" and handles its own discovery of relevant events, alerts, and incidents based on your natural language query. Use this agent directly for questions about errors, anomalies, SLO violations, deployments, and configuration changes without needing separate reconnaissance. It also explains Nudgebee's auto-triage decisions (why an event is suppressed/duplicate/scored), reports alert noise, surfaces threshold-tuning suggestions, and previews proposed triage rules.
KEYWORDS: configuration, deployment, kubernetes, alert, anomaly, slo, error, event, monitoring, incident, crash, oom, outage, triage, suppress, duplicate, noise, noisy alert, threshold, alert tuning, triage rule, event rule, alert rule, alert definition.
ROUTING_KEYWORDS: configuration_changes, configuration, config, kube-state-metrics, kube state metrics, events, event, alert, triage, alert_noise, threshold_suggestion, triage_rule, event_rule, alert_rule, alert_definition.`

	toolInput := "Provide events question in natural language"
	toolOutput := "The tool will return the events data retrieved your query"

	core.RegisterNBAgentFactoryAndToolAndPrioritizeAgentResponseForTool(EventsAgentName, func(accountId string) (core.NBAgent, error) {
		return newEventsAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

func newEventsAgent(accountId string) core.NBAgent {
	return AgentEvents{
		accountId: accountId,
	}
}

type AgentEvents struct {
	accountId string
}

func (l AgentEvents) GetName() string {
	return EventsAgentName
}

func (a AgentEvents) GetNameAliases() []string {
	return []string{
		"Events", "events", "events_agent",
	}
}

func (l AgentEvents) GetDescription() string {
	return `Events agent: This tool is "smart" and handles its own discovery of relevant monitoring data. Returns Nudgebee Events data based on natural language questions about alerts, configuration changes, deployments, Kubernetes events, anomalies, SLO violations, and incident investigations without needing separate reconnaissance.
Primary keywords (signal words): configuration, deployment, kubernetes, alert, anomaly, slo, error, event, monitoring, incident, oom, crash, outage.`
}

func (l AgentEvents) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**CRITICAL RULE:** When a query is about 'configuration changes', ensure you filter by `finding_type = 'configuration_change'`. If the query is broader (e.g., 'issues and configuration changes'), construct the `WHERE` clause to include all requested types, such as `finding_type IN ('issue', 'configuration_change')`.",
		"**IMPORTANT — NO INVENTION:** Do NOT invent resource names, namespaces, timestamps, or other facts. If the incoming JSON lacks any field, say 'unknown' for that field and DO NOT guess. If you must reference a resource, include a verification tag like '[verified]' only if the tool-returned data contains it.",
		"**Understand the Question:** Carefully analyze the user's question to identify the information they need from the events data.",
		"**Use Events Execute Tool:** Always use the 'events_execute' tool to query the data. Do not attempt to answer questions without using this tool.",
		"**Construct SQL Query:** Generate a valid SQL query against the appropriate view ('events' or 'anomaly').",
		"**Filtering:** Correctly identify and apply filtering criteria:",
		"    - For events: priority, subject_name, subject_type, aggregation_key, finding_type",
		"    - For anomalies: name, namespace, anomaly_type, is_anomaly, pod_name",
		"**Ordering and Limiting:** Order the results by timestamp in descending order to get the most recent data. For events use 'starts_at', for anomalies use 'evaluated_at'. Limit the number of results to a reasonable amount (e.g., 3) unless otherwise specified.",
		"**Answer from Results:** Examine the results returned by the tools. For small result sets (≤5 events), use `event_summary` to format the answer. If the summary lacks specific details (e.g., which resource/instance/drive triggered the alert), use `get_event_evidence` to drill into the event's markdowns or other evidence types. For larger result sets, analyze the evidence manifests and use `get_event_evidence` to drill into specific events.",
		"**Verify Tool Usage:** Before providing the final answer, ensure that you have used the correct tool and your response includes a valid SQL query enclosed within the tool's call.",
		"",
		"## SQL Best Practices",
		"    - For detail queries (investigating specific events): SELECT * to get all columns.",
		"    - For analytical questions (counts, trends, patterns): Use GROUP BY, COUNT, and other aggregation functions. Do NOT select all columns for aggregation queries.",
		"    - Use LIKE clause for string matching with wildcard character '%' and replace spaces from the string with '%'.",
		"    - Do not repeat column names in the query.",
		"    - Always use finding_type as issue to filter the results related to issues, errors.",
		"    - Prefer regex matching for subject_name.",
		"    - Example: subject_name ilike 'services-server%'",
		"    - Strictly Don't use evidence column in where clause for events table.",
		"    - Strictly don't use description column in where clause for events table.",
		"    - TIMESTAMP QUOTING: Always quote timestamp values in single quotes.",
		"      CORRECT: starts_at >= '2024-05-07T09:07:53.710Z'",
		"      WRONG: starts_at >= 2024-05-07T09:07:53.710Z",
		"      WRONG: starts_at >= \"2024-05-07T09:07:53.710Z\"",
		"",
		"## Empty Results",
		"    - If events_execute returns an empty result [], no events match your query.",
		"    - Report this to the user clearly. Do NOT retry the same or similar query.",
		"    - The time range may be too narrow, the filter too specific, or there may genuinely be no matching events.",
		"",
		"## Working with Event Evidence",
		"    - When events_execute returns MULTIPLE events (>5), evidence is shown as a **manifest** listing available evidence types and key insights per event — not the full raw data.",
		"    - To investigate specific events in depth, use get_event_evidence(event_id, evidence_type).",
		"    - Strategy for multi-event responses:",
		"      1. Review manifests to identify critical events (by aggregation_key, insights, severity).",
		"      2. Group similar events (same aggregation_key = same root cause pattern).",
		"      3. Drill into 2-3 representative events using get_event_evidence.",
		"      4. Check logs first (most diagnostic), then metrics/deployment for context.",
		"      5. Synthesize findings across all events in your final summary.",
		"    - Evidence types available via get_event_evidence:",
		"      logs, pod_metrics, node_metrics, container_metrics, traces, deployment,",
		"      pod_events, node_events, pod_data, alert_labels, noisy_neighbours,",
		"      related_events, api_failures, metrics_data, markdowns, all.",
		"    - When events_execute returns ≤5 events, full evidence is included. Use event_summary to format the response.",
		"",
		"## Triage Literacy (how Nudgebee auto-triages events)",
		"    Every event flows through an automatic pipeline: deduplicate → triage rules → correlation → scoring → AI-analysis gate.",
		"    - nb_status is set by the HIGHEST-PRIORITY matching triage rule, INDEPENDENT of the score. A P0 event can still be SUPPRESSED if a suppression rule matched.",
		"    - Triage rule types: 'suppression' (suppress/drop noisy events), 'scoring' (adjust computed_score, e.g. service-tier bonuses), 'classification' (auto-classify duplicates/false-positives).",
		"    - computed_score is fully explained by the `score_factors` column: base_severity × env_multiplier × 4 = raw_score, then duplicate_penalty, correlation_adjustment, finding_type_adjustment and evidence_bonus are applied. Always SELECT score_factors when asked WHY an event has a given priority.",
		"    - fingerprint groups recurring occurrences; the duplicate chain tracks occurrence_number and time-since-first/previous. The 1st occurrence has no penalty; later ones are penalised and often auto-classified DUPLICATE.",
		"    - Correlations classify related events as likely_root_cause (boosts score) vs downstream_impact/upstream_dependency (lowers score) — use them to separate the root cause from its symptoms in an alert storm.",
		"    Use the triage tools to go beyond raw event rows:",
		"    - To EXPLAIN one event's triage decision: SELECT score_factors,nb_status,computed_priority via events_execute, then call get_triage_explanation(event_id) for the dedup chain + correlations.",
		"    - For an ALERT-NOISE / HYGIENE report: aggregate with events_execute (GROUP BY aggregation_key, count(*) vs count(DISTINCT fingerprint), nb_status distribution, COUNT(*) FILTER (WHERE computed_priority IS NULL) for unscored events), then call get_triage_rules to surface coverage gaps.",
		"    - For THRESHOLD tuning: call list_threshold_suggestions; highlight high estimated_reduction + tune_threshold/disable rows, flag low-confidence MAD=0 rows as weak.",
		"    - To PROPOSE a new triage rule: call dryrun_triage_rule with the candidate criteria to get the projected volume reduction, present the number, then direct the user to create the rule in the UI.",
		"    Distinguish two kinds of rules: EVENT RULES (alert definitions that GENERATE events — alert name, expr/threshold, duration, severity, enabled; read with get_event_rules) vs TRIAGE RULES (suppress/score/classify events AFTER they fire; read with get_triage_rules). A threshold suggestion targets the expr/threshold inside an EVENT RULE — use get_event_rules to show the rule's current definition; use get_triage_rules for suppression/scoring coverage.",
		"    READ-ONLY: you explain and recommend. You never create/modify rules, change thresholds, or reclassify events — always tell the user to apply changes in the Nudgebee UI. Never claim to have applied a change.",
	}

	constraints := []string{
		"You MUST use the 'events_execute' tool to interact with the 'events' data and 'anomaly_execute' tool for 'anomaly' data.",
		"You MUST NOT answer questions without first using the appropriate tool ('events_execute' or 'anomaly_execute') to query the database.",
		"You must generate the SQL query.",
		"CRITICAL: When using 'events_execute' or 'anomaly_execute' tool, you MUST provide actual SQL queries, NOT descriptions or explanations.",
		"WRONG: 'The user is asking for event details, I need to query...'",
		"CORRECT for events: 'SELECT * FROM events WHERE id = \"event-id\"'",
		"CORRECT for anomalies: 'SELECT * FROM anomaly WHERE name = \"workload-name\"'",
		"You MUST use synonyms when applicable.",
		"When users ask about 'anomalies' or 'anomaly detection', use the 'anomaly_execute' tool, not 'events_execute'.",
		"For small result sets (≤5 events): use 'event_summary' to format the response. Use 'get_event_evidence' if the summary is missing specific details needed to answer the user's question.",
		"For large result sets (>5 events): analyze the evidence manifests, use 'get_event_evidence' to drill into 2-3 representative events, then synthesize your findings directly.",
	}

	toolUsage := map[string][]string{
		tools.ToolEventExecuteSql: {
			"Use this tool to execute SQL queries against the 'events' view.",
			"Always use this tool to get events data.",
			"Input: MUST be a valid SQL query (e.g., 'SELECT * FROM events WHERE id = \"event-id\"')",
			"Output: the data returned by the sql query.",
			"CRITICAL: Input must be actual SQL, not descriptions or explanations",
			"Examples:",
			"  - Single event: SELECT * FROM events WHERE id = 'your-event-id'",
			"  - Recent events: SELECT * FROM events ORDER BY starts_at DESC LIMIT 5",
			"  - High priority: SELECT * FROM events WHERE priority = 'high'",
			"  - Search by title: SELECT * FROM events WHERE title LIKE '%keyword%'",
		},
		tools.ToolAnomalyExecuteSql: {
			"Use this tool to execute SQL queries against the 'anomaly' view.",
			"Use this tool to get anomaly detection data for workloads.",
			"Input: MUST be a valid SQL query (e.g., 'SELECT * FROM anomaly WHERE name = \"workload-name\"')",
			"Output: the data returned by the sql query.",
			"CRITICAL: Input must be actual SQL, not descriptions or explanations",
			"CRITICAL: ALWAYS include LIMIT clause in queries (default 10, adjust based on user request)",
			"Examples:",
			"  - Recent anomalies: SELECT * FROM anomaly WHERE is_anomaly = true ORDER BY evaluated_at DESC LIMIT 10",
			"  - Workload anomalies: SELECT * FROM anomaly WHERE name = 'llm-server' AND namespace = 'nudgebee' ORDER BY evaluated_at DESC LIMIT 10",
			"  - Latency anomalies: SELECT * FROM anomaly WHERE anomaly_type = 'Latency' AND is_anomaly = true ORDER BY evaluated_at DESC LIMIT 10",
			"  - Memory anomalies: SELECT * FROM anomaly WHERE anomaly_type = 'Memory' AND is_anomaly = true ORDER BY evaluated_at DESC LIMIT 10",
		},
		"event_summary": {
			"Use this tool to format and summarize event data when you have ≤5 events with full evidence.",
			"Input: events data",
			"Output: events in markdown format",
		},
		tools.ToolGetEventEvidence: {
			"Use this tool to fetch detailed evidence for a specific event by ID.",
			"Use this whenever you need to examine raw evidence data for an event — regardless of result set size.",
			"Input: event_id (required), evidence_type (optional: logs, pod_metrics, node_metrics, traces, deployment, pod_events, node_events, pod_data, alert_labels, noisy_neighbours, related_events, api_failures, all)",
			"Output: the requested evidence data for the event.",
			"Strategy: Start with 'logs' (most diagnostic), then 'deployment' or 'pod_metrics' for context.",
		},
		tools.ToolTriageExplanation: {
			"Use this tool to explain HOW a single event was triaged (why it is DUPLICATE/SUPPRESSED or has a given computed_priority).",
			"Input: event_id (required).",
			"Output: duplicate chain (occurrence_number, total_occurrences, time since first/previous), correlated events (root-cause vs downstream), historical firing stats and hourly trend.",
			"Strategy: combine this with the event's `score_factors` column (from events_execute) to give a complete, evidence-backed explanation of the triage decision.",
		},
		tools.ToolTriageRules: {
			"Use this tool to list the configured triage rules (suppression / scoring / classification) for the current account/tenant.",
			"Input: optional rule_type ('suppression'|'scoring'|'classification'), optional enabled (bool).",
			"Output: the rules with their match criteria, actions and priorities.",
			"Use it for rule-coverage analysis and to check whether a rule already exists before proposing a new one.",
		},
		tools.ToolThresholdSuggestions: {
			"Use this tool to list threshold-tuning suggestions for noisy alerts.",
			"Input: optional source, optional confidence ('high'|'medium'|'low'), optional limit.",
			"Output: current vs suggested threshold, recommendation_type, confidence, estimated_reduction (%), and metric stats per alert.",
			"Flag low-confidence flat-metric (MAD=0) rows as weak. These are advisory only — recommend, do not claim to apply.",
		},
		tools.ToolTriageDryRun: {
			"Use this tool to PREVIEW the impact of a candidate triage rule before recommending it — it does NOT create the rule.",
			"Input: rule_type and action (required), plus one or more match criteria (match_alertname, match_source, match_namespace, match_service, match_fingerprint, match_priority, match_finding_type, match_labels).",
			"Output: how many existing events match (projected volume reduction).",
			"Use it to quantify a proposed rule, then direct the user to create it in the UI.",
		},
		tools.ToolGetEventRules: {
			"Use this tool to inspect the alert/event RULE DEFINITIONS that generate events (the alerting rules), NOT triage rules.",
			"Input: optional filters alert (substring), source, severity, alert_type, namespace, enabled, limit.",
			"Output: rules with alert name, expr (condition/threshold), duration, severity, source, metric_provider, namespace and enabled.",
			"Use it to see how an alert is defined, to map a threshold suggestion or noisy alert back to its rule, and to find disabled/misconfigured rules.",
		},
		tools.ToolEventClassification: {
			"Use this tool to get the classification VERDICT for an event (true_positive/false_positive/benign_positive/duplicate) and its reason_code, linked_event_id and rule.",
			"Input: event_id.",
			"Output: the classification record, or a clear note that none was recorded.",
			"Use it to answer how an event was classified and why — complements get_triage_explanation (dedup chain/correlations/score).",
		},
		tools.ToolTriageRuleEvents: {
			"Use this tool to list the events a specific triage rule matched (rule effectiveness).",
			"Input: rule_id (from get_triage_rules), optional limit.",
			"Output: the matched events.",
			"Use it to answer 'what is this rule actually catching / is it still firing?'.",
		},
	}

	schema := []string{
		"**events:** This view contains information about kubernetes/prometheus.",
		"id text = id of event",
		"starts_at timestamp = event was triggered",
		"updated_at timestamp = event data got updated",
		"finding_id text = internal id",
		"title text = event title",
		"description text = event description",
		"source text = event source (e.g. prometheus, kubernetes_api_server, pagerduty_webhook, datadog_webhook, AWS_CloudWatch_Alarm, etc). Use GROUP BY source or SELECT DISTINCT source to discover available values.",
		"aggregation_key text = event type, possible values(AlertmanagerClusterDown,AlertmanagerClusterFailedToSendAlerts,AlertmanagerFailedReload,AlertmanagerFailedToSendAlerts,ApplicationAPIFailures,ConfigurationChange/KubernetesResource/Change,CPUThrottlingHigh,HighErrorCriticalLogs,HighErrorCriticalLogs2,HighErrorCriticalLogs20,HighErrorCriticalLogs22,HighErrorCriticalLogs24,HighErrorCriticalLogs26,HighErrorCriticalLogs3,HighErrorCriticalLogs37,HTTPSlowAPIAlert,image_pull_backoff_reporter,job_failure,KubeAggregatedAPIDown,KubeAggregatedAPIErrors,KubeAPIErrorBudgetBurn,KubeContainerWaiting,KubeCPUOvercommit,KubeDaemonSetMisScheduled,KubeDaemonSetRolloutStuck,KubeDeploymentReplicasMismatch,KubeDeploymentRolloutStuck,KubeHpaMaxedOut,KubeJobFailed,KubeJobNotCompleted,KubeletTooManyPods,KubeMemoryOvercommit,KubeNodeNotReady,KubeNodeReadinessFlapping,KubeNodeUnreachable,KubePersistentVolumeErrors,KubePersistentVolumeFillingUp,KubePodCrashLooping,KubePodNotReady,KubePodStuckTerminating,KubernetesVolumeOutOfDiskSpace,KubernetesWarningEvent,KubeStatefulSetReplicasMismatch,KubeStatefulSetUpdateNotRolledOut,KubeVersionMismatch,LabelsLimitExceededOnIngestion,NGINXTooMany500s,NodeCPUHighUsage,NodeMemoryMajorPagesFaults,NodeSystemSaturation,NudgeBeeKubeHpaMaxedOut,PodMemoryReachingLimit,pod_oom_killer_enricher,PrometheusMissingRuleEvaluations,PrometheusOperatorReconcileErrors,PrometheusOutOfOrderTimestamps,PrometheusRuleFailures,python_profiler,query_failure,report_crash_loop,RequestErrorsToAPI,ServiceDown,TargetDown,TooHighChurnRate24h,TooHighMemoryUsage,TooManyLogs,TooManyScrapeErrors)",
		"finding_type text = issue or config changes, possible values (issue, configuration_change, SLO, Anomaly)",
		"priority text = event priority, possible values (DEBUG, HIGH, INFO, LOW, MEDIUM)",
		"subject_type text = resource type (e.g. pod, deployment, node, daemonset, statefulset, job, virtualmachine, db-instance, etc). Use ILIKE for case-insensitive matching.",
		"subject_name text = event subject name",
		"subject_namespace text = event namespace",
		"subject_node text = event node",
		"ends_at timestamp = event stopped at",
		"fingerprint text = event fingerprint (SHA hash), events with the same fingerprint represent the same recurring pattern (same source + subject + type). Use GROUP BY fingerprint to deduplicate or find noisy/repeated alerts.",
		"evidences text = evidence data for events, can contain logs, metrices, alerts etc",
		"cloud_account_id ",
		"status text = event status (FIRING = currently active, CLOSED = resolved, RESOLVED). Most events are CLOSED. Only filter by status when the user explicitly asks for it.",
		"nb_status text = triage status, possible values (OPEN, DUPLICATE, SUPPRESSED, SNOOZED, ACTION_REQUIRED, RESOLVED). Reflects how the event has been triaged.",
		"urgency text = event urgency, possible values (DEBUG, HIGH, LOW, MEDIUM)",
		"computed_priority text = scored priority (P0=critical ≥80, P1=high 60-79, P2=medium 40-59, P3=low <40). Based on computed_score.",
		"computed_score integer = severity score (0-100) based on priority, environment, service tier, duplicate count, correlations, and evidence factors.",
		"score_factors jsonb = full breakdown of how computed_score was derived. Sub-fields: base_severity (HIGH=25/MEDIUM=15/LOW=10/INFO=5/DEBUG=3), base_severity_source, env_multiplier (prod=1.0/non_prod=0.3/unknown=0.5), raw_score (base_severity×env_multiplier×4), duplicate_penalty (−5 per repeat occurrence, capped −30), correlation_type, correlation_adjustment (likely_root_cause +15/same_service −5/downstream −10/upstream −10), finding_type_adjustment (slo +10/anomaly +5/configuration_change −10), evidence_bonus (OOMKilled +15/restarts≥3 +10), total_adjustments, final_score, confidence. Select this column to EXPLAIN why an event has its computed_priority.",
		"score_confidence numeric = confidence (0-1) in the computed_score, higher when more scoring factors and a known environment were available.",
		"cluster text = kubernetes cluster name",
		"service_key text = service identifier",
		"subject_owner text = owner of the subject resource (e.g. deployment name for a pod)",
		"failure text = failure details",
		"category text = event category",
		"labels jsonb = event labels as key-value pairs",
		"snoozed_until timestamp = if snoozed, when the snooze expires",
		"event_count integer = number of deduplicated events with same fingerprint (available in time-range queries)",
		"**aggregation_key and it's synonyms:**",
		"- 'pod_oom_killer_enricher': 'oom', 'out of memory', 'outofmemory'",
		"- 'report_crash_loop': 'crash loop', 'crash', 'pod crash'",
		"- 'Kubernetes Warning Event': warning event, warning, kubernetes warning",
		"- 'AlertmanagerClusterDown' : 'Alertmanager Cluster Down', 'Alertmanager Down', 'Alertmanager Cluster Failure'",
		"- 'ServiceDown': 'Service Unavailable', 'Service Outage', 'Server Unavailable'",
		"- 'TargetDown': 'Endpoint Failure', 'Target Unreachable', 'Unresponsive Target', 'Endpoint Down', 'Target Failure', 'Unreachable Target', 'Endpoint Downtime'",
		"- For the 'events' table:",
		"  - Strictly Don't use evidence column in where clause.",
		"  - Strictly don't use description column in where clause",
		"",
		"**anomaly:** This view contains anomaly detection data for workloads.",
		"id text = unique identifier for the anomaly record",
		"created_at timestamp = when the anomaly record was created",
		"updated_at timestamp = when the anomaly record was last updated",
		"name text = workload name (deployment/statefulset name)",
		"namespace text = kubernetes namespace of the workload",
		"reference_value text = historical reference metrics used for anomaly detection in JSON format",
		"current_value numeric = current metric value that triggered the anomaly",
		"anomaly_type text = type of anomaly detected, possible values (Latency, Memory, CPU, Network, ErrorRate, Replicas)",
		"is_anomaly boolean = whether an anomaly was detected (true/false)",
		"evaluated_at timestamp = when the anomaly was evaluated",
		"pod_name text = specific pod name if anomaly is pod-level",
		"config_id text = configuration ID used for anomaly detection",
		"- For the 'anomaly' table:",
		"  - Use anomaly_type to filter by specific metric types",
		"  - Use is_anomaly = true to get only detected anomalies",
		"  - Use name and namespace to query specific workloads",
		"  - Order by evaluated_at DESC to get recent anomalies",
		"  - For SELECT queries returning records: ALWAYS include LIMIT clause (default 10, adjust based on user request)",
		"  - For COUNT/aggregation queries: Do NOT include LIMIT",
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "What  are latest errors?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolEventExecuteSql,
					Input: "SELECT * FROM events where priority = 'HIGH' and finding_type='issue' ORDER BY starts_at DESC limit 3",
				},
			},
			Explanation: "We are retrieving only issues, based on the latest 'starts_at' time, limited to 3 and default priority as HIGH.",
		},
		{
			Question: "What  are latest events of services-serves?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolEventExecuteSql,
					Input: "SELECT * FROM events where subject_name ilike 'services-server%' and priority = 'HIGH' and finding_type='issue' ORDER BY starts_at DESC limit 3",
				},
			},
			Explanation: "We are retrieving based on the latest 'starts_at' time, limited to 3, and using like query on `subject_name` also using default priority as HIGH",
		},
		{
			Question: "What are latest oom or out of memory errors?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolEventExecuteSql,
					Input: "SELECT * FROM events WHERE aggregation_key='pod_oom_killer_enricher' and finding_type='issue' and priority = 'HIGH' ORDER BY starts_at DESC limit 3",
				},
			},
			Explanation: "We are filtering using `aggregation_key` and `finding_type`, based on the latest 'starts_at' time, limited to 3",
		},
		{
			Question: "Get all events for nodes within a specific time range?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolEventExecuteSql,
					Input: "SELECT * FROM events WHERE ((starts_at >= '2024-05-07T09:07:53.710Z') AND (starts_at <= '2024-05-14T09:07:53.710Z') AND (subject_type IN ('node')) ORDER BY starts_at DESC LIMIT 3",
				},
			},
			Explanation: "We are filtering using `starts_at` and `subject_type`, limited to 3",
		},
		{
			Question: "Get all events for pods within a specific time range?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolEventExecuteSql,
					Input: "SELECT * FROM events WHERE ((starts_at >= '2024-05-07T09:17:48.191Z') AND (starts_at <= '2024-05-14T09:17:48.191Z') AND (subject_type IN ('pod')) ORDER BY starts_at DESC LIMIT 3",
				},
			},
			Explanation: "We are filtering using `starts_at` and `subject_type`, limited to 3",
		},
		{
			Question: "How many events are there for nodes within a specific time range?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolEventExecuteSql,
					Input: "SELECT count(*) AS event_count FROM events WHERE ((starts_at >= '2024-05-07T09:19:21.743Z') AND (starts_at <= '2024-05-14T09:19:21.743Z') AND (subject_type IN ('node'))",
				},
			},
			Explanation: "We are filtering using `starts_at` and `subject_type`. Limit/Order is not required as this is only count",
		},
		{
			Question: "How many events are thre for each aggregation key within a specific time range?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolEventExecuteSql,
					Input: "SELECT aggregation_key, count(*) AS event_count FROM events WHERE ((starts_at >= '2024-05-07T09:19:21.743Z') AND (starts_at <= '2024-05-14T09:19:21.743Z') group by aggregation_key",
				},
			},
			Explanation: "We are filtering using `starts_at`. grouping by aggregation_key and counting",
		},
		{
			Question:    "How many distinct event patterns are there per namespace in the last 24 hours?",
			Explanation: "Using fingerprint with COUNT(DISTINCT) to count unique event patterns vs total occurrences. No LIMIT needed for aggregation.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolEventExecuteSql,
					Input: "SELECT subject_namespace, count(DISTINCT fingerprint) AS unique_patterns, count(*) AS total_events FROM events WHERE starts_at >= NOW() - INTERVAL '24 hours' GROUP BY subject_namespace ORDER BY total_events DESC",
				},
			},
		},
		{
			Question:    "What fields are retrieved from the events table for a specific subject name, cloud account ID, and finding type within a specific time range?",
			Explanation: "We are filtering using `starts_at`, `finding_type` and `subject_name`, limited to 3",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolEventExecuteSql,
					Input: "SELECT * FROM events WHERE ((starts_at >= '2024-04-30T18:30:00.000Z') AND (starts_at <= '2024-05-31T18:29:59.999Z') AND (finding_type = 'issue') AND (subject_name = 'ip-172-31-93-121.ec2.internal') ORDER BY starts_at DESC LIMIT 3",
				},
			},
		},
		{
			Question: "What are the latest anomalies?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolEventExecuteSql,
					Input: "SELECT * FROM events WHERE finding_type = 'Anomaly' ORDER BY starts_at DESC LIMIT 3",
				},
			},
			Explanation: "We are retrieving the latest anomalies based on the 'starts_at' time, limited to 3.",
		},
		{
			Question: "What are the latest anomalies for nudgebee namespace?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolEventExecuteSql,
					Input: "SELECT * FROM events WHERE finding_type = 'Anomaly' AND subject_namespace = 'nudgebee' ORDER BY starts_at DESC LIMIT 3",
				},
			},
			Explanation: "We are retrieving the latest anomalies for the 'nudgebee' namespace based on the 'starts_at' time, limited to 3.",
		},
		{
			Question: "What are the latest SLO violations?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolEventExecuteSql,
					Input: "SELECT * FROM events WHERE finding_type = 'SLO' ORDER BY starts_at DESC LIMIT 3",
				},
			},
			Explanation: "We are retrieving the latest SLO violations based on the 'starts_at' time, limited to 3.",
		},
		{
			Question: "What are the latest SLO violations for nudgebee namespace?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolEventExecuteSql,
					Input: "SELECT * FROM events WHERE finding_type = 'SLO' AND subject_namespace = 'nudgebee' ORDER BY starts_at DESC LIMIT 3",
				},
			},
			Explanation: "We are retrieving the latest SLO violations for the 'nudgebee' namespace based on the 'starts_at' time, limited to 3.",
		},
		{
			Question: "What anomalies have been detected recently?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolAnomalyExecuteSql,
					Input: "SELECT * FROM anomaly WHERE is_anomaly = true ORDER BY evaluated_at DESC LIMIT 10",
				},
			},
			Explanation: "We are retrieving the latest detected anomalies based on the 'evaluated_at' time, limited to 10.",
		},
		{
			Question: "What are the latency anomalies for llm-server?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolAnomalyExecuteSql,
					Input: "SELECT * FROM anomaly WHERE name ilike 'llm-server%' AND anomaly_type = 'Latency' AND is_anomaly = true ORDER BY evaluated_at DESC LIMIT 10",
				},
			},
			Explanation: "We are filtering for the 'llm-server' workload with 'Latency' anomaly type, showing only detected anomalies, limited to 10.",
		},
		{
			Question: "Show me memory anomalies in the nudgebee namespace?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolAnomalyExecuteSql,
					Input: "SELECT * FROM anomaly WHERE namespace = 'nudgebee' AND anomaly_type = 'Memory' AND is_anomaly = true ORDER BY evaluated_at DESC LIMIT 10",
				},
			},
			Explanation: "We are filtering for the 'nudgebee' namespace with 'Memory' anomaly type, showing only detected anomalies, limited to 10.",
		},
		{
			Question: "What CPU or memory anomalies occurred for services-server?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolAnomalyExecuteSql,
					Input: "SELECT * FROM anomaly WHERE name ilike 'services-server%' AND anomaly_type IN ('CPU', 'Memory') AND is_anomaly = true ORDER BY evaluated_at DESC LIMIT 10",
				},
			},
			Explanation: "We are filtering for 'services-server' workload with either 'CPU' or 'Memory' anomaly types, showing only detected anomalies, limited to 10.",
		},
		{
			Question: "Get me anomalies in the last week",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolAnomalyExecuteSql,
					Input: "SELECT * FROM anomaly WHERE is_anomaly = true AND evaluated_at >= NOW() - INTERVAL '7 days' ORDER BY evaluated_at DESC LIMIT 10",
				},
			},
			Explanation: "We are retrieving anomalies from the last 7 days, ordered by evaluated_at DESC, limited to 10 records.",
		},
		{
			Question: "How many anomalies were detected in the last week?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolAnomalyExecuteSql,
					Input: "SELECT anomaly_type, count(*) AS anomaly_count FROM anomaly WHERE is_anomaly = true AND evaluated_at >= NOW() - INTERVAL '7 days' GROUP BY anomaly_type",
				},
			},
			Explanation: "We are counting anomalies by type for the last 7 days, grouping by anomaly_type. No LIMIT needed for aggregation.",
		},
		{
			Question:    "Why is this event suppressed / why is it only P3?",
			Explanation: "First read the score breakdown and triage status from the events table, then fetch the dedup chain and correlations to explain the decision end-to-end.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolEventExecuteSql,
					Input: "SELECT id, nb_status, computed_priority, computed_score, score_factors, fingerprint FROM events WHERE id = 'your-event-id'",
				},
				{
					Tool:  tools.ToolTriageExplanation,
					Input: "{\"event_id\": \"your-event-id\"}",
				},
			},
		},
		{
			Question:    "Give me an alert-noise report for the last 30 days.",
			Explanation: "Aggregate firings vs distinct fingerprints per alert to find noisy alerts, then surface configured rules to spot coverage gaps.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolEventExecuteSql,
					Input: "SELECT aggregation_key, source, count(*) AS firings, count(DISTINCT fingerprint) AS distinct_patterns FROM events GROUP BY aggregation_key, source ORDER BY firings DESC",
				},
				{
					Tool:  tools.ToolTriageRules,
					Input: "{\"rule_type\": \"suppression\"}",
				},
			},
		},
		{
			Question:    "Which alerts should I tune?",
			Explanation: "List threshold suggestions; recommend the high-reduction tune_threshold/disable ones and flag low-confidence flat-metric rows as weak.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolThresholdSuggestions,
					Input: "{\"confidence\": \"high\"}",
				},
			},
		},
		{
			Question:    "Propose a rule to cut the RabbitmqNoQueueConsumer noise and show the impact.",
			Explanation: "Dry-run the candidate suppression rule to get the projected volume reduction, then direct the user to create it in the UI (read-only — we do not create it).",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolTriageDryRun,
					Input: "{\"rule_type\": \"suppression\", \"action\": \"suppress\", \"match_alertname\": \"RabbitmqNoQueueConsumer\"}",
				},
			},
		},
		{
			Question:    "What's the current threshold/definition for the HighP95Latency alert, and is it enabled?",
			Explanation: "Inspect the alert rule definition (expr holds the threshold) via get_event_rules.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolGetEventRules,
					Input: "{\"alert\": \"HighP95Latency\"}",
				},
			},
		},
	}

	return core.NBAgentPrompt{
		Role:         "a PostgreSQL database expert",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Schema:       schema,
		Examples:     examples,
		Rag: core.NBAgentPromptRag{
			Module: "events",
			Format: core.NBAgentPromptRagFormatJson,
		},
	}
}

func (p AgentEvents) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{
		tools.EventsExecuteTool{}, tools.AnomalyExecuteTool{}, EventSummaryTool{}, tools.GetEventEvidenceTool{},
		tools.TriageExplanationTool{}, tools.TriageRulesTool{}, tools.ThresholdSuggestionsTool{}, tools.TriageDryRunTool{},
		tools.EventRulesTool{}, tools.EventClassificationTool{}, tools.TriageRuleEventsTool{},
	}
}

func (p AgentEvents) GetSummaryToolName() string {
	return ToolEventSummary
}

func (a AgentEvents) ShouldSummarizeNow(toolName string, observation string) bool {
	// We want to summarize after getting data from the database
	if toolName != tools.ToolEventExecuteSql && toolName != tools.ToolAnomalyExecuteSql {
		return false
	}

	// For small result sets (1-3 events) with full evidence, auto-summarize.
	// For larger sets or manifests, let the ReAct loop handle it with
	// get_event_evidence for interactive investigation.
	// For 0 events (empty result or aggregated query), skip summary.
	count := countEventsInResponse(observation)
	if count < 1 || count > 3 {
		return false
	}
	// If evidence is in manifest form (not full data), don't auto-summarize —
	// the LLM should use get_event_evidence to fetch actual evidence first.
	if strings.Contains(observation, `"available_evidence_types"`) {
		return false
	}
	return true
}

// countEventsInResponse counts the number of events in a JSON response
// by counting occurrences of "event_id": — fast string counting, no JSON parsing.
func countEventsInResponse(observation string) int {
	return strings.Count(observation, `"event_id":`)
}

func (l AgentEvents) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func reduceEventData(event events.Event) map[string]any {
	eventData := map[string]any{
		"id":                event.Id,
		"event_id":          event.Id,
		"description":       event.Description,
		"title":             event.Title,
		"created_at":        event.CreatedAt,
		"subject_type":      event.SubjectType,
		"subject_name":      event.SubjectName,
		"subject_namespace": event.SubjectNamespace,
		"subject_node":      event.SubjectNode,
		"aggregation_key":   event.AggregationKey,
	}

	// Carry triage columns through so "explain a triage decision" works even on
	// SELECT * detail queries (this reducer otherwise drops them).
	if event.NbStatus != "" {
		eventData["nb_status"] = event.NbStatus
	}
	if event.ComputedPriority != "" {
		eventData["computed_priority"] = event.ComputedPriority
	}
	if event.ComputedScore != nil {
		eventData["computed_score"] = event.ComputedScore
	}
	if event.ScoreFactors != nil {
		// score_factors arrives as a ::text JSON string; parse it back into a
		// nested object so the LLM sees structured fields rather than an escaped
		// JSON string. Fall back to the raw value if it isn't parseable.
		if s, ok := event.ScoreFactors.(string); ok {
			var parsed any
			if err := json.Unmarshal([]byte(s), &parsed); err == nil {
				eventData["score_factors"] = parsed
			} else {
				eventData["score_factors"] = event.ScoreFactors
			}
		} else {
			eventData["score_factors"] = event.ScoreFactors
		}
	}
	if event.ScoreConfidence != nil {
		eventData["score_confidence"] = event.ScoreConfidence
	}

	logData := ""
	if len(event.Evidences.ErrorLogData) > 0 {
		logData = strings.Join(event.Evidences.ErrorLogData, "\n")
	}
	if logData == "" {
		logData = event.Evidences.LogData
	}
	if logData != "" {
		eventData["logs"] = logData
	}

	if event.Evidences.AlertLabels.Data != nil && len(event.Evidences.AlertLabels.Data.([]any)) > 0 {
		alertLabels := map[string]any{}
		for _, labelAny := range event.Evidences.AlertLabels.Data.([]any) {
			label := labelAny.(map[string]any)
			alertLabels[label["label"].(string)] = label["value"]
		}
		eventData["labels"] = alertLabels
	}

	if event.Evidences.Markdowns != nil {
		markdownEntries := []map[string]any{}
		for _, markdown := range event.Evidences.Markdowns {
			entry := map[string]any{}
			if markdown.Data != nil {
				dataStr := fmt.Sprintf("%v", markdown.Data)
				if len(dataStr) > 3000 {
					dataStr = dataStr[:3000] + "\n... (truncated)"
				}
				entry["data"] = dataStr
			}
			if len(markdown.Insight) > 0 {
				entry["insights"] = markdown.Insight
			}
			if len(entry) > 0 {
				markdownEntries = append(markdownEntries, entry)
			}
		}
		if len(markdownEntries) > 0 {
			eventData["markdowns"] = markdownEntries
		}
	}

	if event.Evidences.AlertData.Data != nil {
		eventData["alert_data"] = event.Evidences.AlertData.Data
	}

	// if event.Evidences.ServiceMap.Data != nil {
	// 	eventData["service_map"] = event.Evidences.ServiceMap.Data
	// }

	if len(event.Evidences.RDBMSQueryData) > 0 {
		eventData["rdbms_query_response"] = event.Evidences.RDBMSQueryData
	}

	if event.Evidences.PodData.Title != "" {
		eventData["pod_data"] = event.Evidences.PodData
	}

	if len(event.Evidences.JobInformation.Title) > 0 {
		eventData["job_information"] = event.Evidences.JobInformation
	}

	if len(event.Evidences.PodEvents) > 0 {
		eventData["pod_events"] = event.Evidences.PodEvents
	}

	if len(event.Evidences.NodeEvents) > 0 {
		eventData["node_events"] = event.Evidences.NodeEvents
	}

	if len(event.Evidences.JobEvents.Title) > 0 {
		eventData["job_events"] = event.Evidences.JobEvents
	}

	if len(event.Evidences.NodeMetrics) > 0 {
		eventData["node_metrics"] = event.Evidences.NodeMetrics
	}

	if len(event.Evidences.PodMetrics) > 0 {
		eventData["pod_metrics"] = event.Evidences.PodMetrics
	}

	if len(event.Evidences.ContainerMetrics.Title) > 0 {
		eventData["container_metrics"] = event.Evidences.ContainerMetrics
	}

	if len(event.Evidences.JobPodEvents.Title) > 0 {
		eventData["job_pod_events"] = event.Evidences.JobPodEvents
	}

	if len(event.Evidences.ApiFailures) > 0 {
		eventData["api_failures"] = event.Evidences.ApiFailures
	}

	if len(event.Evidences.NoisyNeighbours) > 0 {
		eventData["noisy_neighbours"] = event.Evidences.NoisyNeighbours
	}

	if event.Evidences.RelatedEvents.Data != nil {
		eventData["related_events"] = event.Evidences.RelatedEvents
	}

	if len(event.Evidences.Deployment.Title) > 0 {
		eventData["deployment"] = event.Evidences.Deployment
	}

	if len(event.Evidences.NodeData.Insight) > 0 {
		eventData["node_data"] = event.Evidences.NodeData.Insight
	}

	if len(event.Evidences.MetricsData) > 0 {
		eventData["metrics_data"] = event.Evidences.MetricsData
	}

	if event.Evidences.Traces.Data != nil {
		if len(event.Evidences.Traces.Insight) > 0 {
			eventData["traces_insight"] = event.Evidences.Traces.Insight
		} else {
			traces := []map[string]any{}
			traceData := event.Evidences.Traces.Data.(map[string]any)["data"]
			for _, traceAny := range traceData.([]any) {
				trace, ok := traceAny.(map[string]any)
				if !ok {
					continue
				}
				summary := summarizeTraceSpan(trace)
				if summary != nil {
					traces = append(traces, summary)
				}
			}
			if len(traces) > 0 {
				eventData["traces"] = traces
			}
		}
	}

	if len(event.Evidences.Others) > 0 {
		otherEntries := []map[string]any{}
		for _, other := range event.Evidences.Others {
			entry := map[string]any{}
			if other.Data != nil {
				dataStr := fmt.Sprintf("%v", other.Data)
				if len(dataStr) > 3000 {
					dataStr = dataStr[:3000] + "\n... (truncated)"
				}
				entry["data"] = dataStr
			}
			if len(other.Insight) > 0 {
				entry["insights"] = other.Insight
			}
			if len(entry) > 0 {
				otherEntries = append(otherEntries, entry)
			}
		}
		if len(otherEntries) > 0 {
			eventData["other"] = otherEntries
		}
	}

	if len(event.Evidences.UserActions) > 0 {
		eventData["user_actions"] = event.Evidences.UserActions
	}

	return eventData
}

// summarizeTraceSpan builds the span representation that gets fed to the LLM
// as evidence. Previously the projection stripped most attributes — including
// span_attributes, resource_attributes, and exception events — which are
// exactly the fields that carry the RCA signal for gRPC/HTTP failures
// (e.g. rpc.grpc.status_code, rpc.method, exception.message).
//
// The raw evidence JSON is produced from OpenTelemetryTrace with snake_case
// tags (see api-server common/map_datadog_to_opentelemetry_traces.go), so all
// source lookups use snake_case. CamelCase keys (a legacy from an older
// Datadog-shaped payload) are dropped — they never matched against current
// evidence anyway.
//
// The projection is error-biased: for spans with status_code=STATUS_CODE_ERROR
// or a non-OK rpc.grpc.status_code we keep the full attribute maps so the LLM
// can name the exact RPC method / destination / peer / error code. For context
// spans around an error trace we keep a smaller set sufficient to reconstruct
// the call graph without bloating the prompt.
//
// Returns nil when the span carries no useful signal (no status, no identifiers).
func summarizeTraceSpan(trace map[string]any) map[string]any {
	if trace == nil {
		return nil
	}

	statusCode, _ := trace["status_code"].(string)
	spanAttrs := attrMapAsAny(trace, "span_attributes")
	grpcStatus := ""
	httpStatus := ""
	if spanAttrs != nil {
		grpcStatus = attrAsString(spanAttrs["rpc.grpc.status_code"])
		httpStatus = attrAsString(spanAttrs["http.status_code"])
	}
	if httpStatus == "" {
		httpStatus, _ = trace["http_status_code"].(string)
	}

	isError := statusCode == "STATUS_CODE_ERROR" ||
		(grpcStatus != "" && grpcStatus != "0") ||
		isHTTPError(httpStatus)

	// Drop spans that have neither a status nor identifiers — nothing useful for RCA.
	traceID, _ := trace["trace_id"].(string)
	spanID, _ := trace["span_id"].(string)
	if !isError && traceID == "" && spanID == "" {
		return nil
	}

	summary := map[string]any{
		"trace_id":                  traceID,
		"span_id":                   spanID,
		"parent_span_id":            trace["parent_span_id"],
		"service_name":              trace["service_name"],
		"span_name":                 trace["span_name"],
		"span_kind":                 trace["span_kind"],
		"status_code":               statusCode,
		"timestamp":                 trace["timestamp"],
		"duration_ns":               trace["duration_ns"],
		"workload_name":             trace["workload_name"],
		"workload_namespace":        trace["workload_namespace"],
		"destination_workload_name": trace["destination_workload_name"],
		"is_error":                  isError,
	}
	if msg, ok := trace["status_message"].(string); ok && msg != "" {
		summary["status_message"] = msg
	}

	if isError {
		// For error spans keep the full attribute map plus span events (exception
		// info lives under events_attributes / events_name per OTel convention).
		if spanAttrs != nil {
			summary["span_attributes"] = spanAttrs
		}
		if ra := attrMapAsAny(trace, "resource_attributes"); len(ra) > 0 {
			summary["resource_attributes"] = compactResourceAttributes(ra)
		}
		if evAttrs, ok := trace["events_attributes"]; ok && evAttrs != nil {
			summary["events_attributes"] = evAttrs
		}
		if evNames, ok := trace["events_name"]; ok && evNames != nil {
			summary["events_name"] = evNames
		}
		return summary
	}

	// Non-error context span from phase-2 expansion: keep the minimal set that
	// reconstructs the call graph and preserves RPC identity.
	if spanAttrs != nil {
		compactAttrs := map[string]any{}
		for _, key := range []string{
			"rpc.system", "rpc.service", "rpc.method", "rpc.grpc.status_code",
			"http.method", "http.route", "http.url", "http.status_code",
			"server.address", "server.port",
			"db.system", "db.statement",
		} {
			if v, ok := spanAttrs[key]; ok && !isEmptyAttr(v) {
				compactAttrs[key] = v
			}
		}
		if len(compactAttrs) > 0 {
			summary["span_attributes"] = compactAttrs
		}
	}
	return summary
}

// attrMapAsAny returns trace[key] as map[string]any, handling the three shapes
// trace attribute maps arrive in across the codebase:
//  1. map[string]any — decoded from JSONB directly
//  2. map[string]string — the Go struct field type on common.OpenTelemetryTrace;
//     converted in convertOTelTracesToMapRows and stored as-is. Without this
//     branch every attribute lookup silently returns empty.
//  3. string — legacy stringified JSON.
//
// Mirrors api-server/services/observability.traceGetMap — same class of bug
// (gemini review on PR #29235 flagged that RCA-critical fields like
// rpc.grpc.status_code were dropped whenever the upstream producer used the
// struct-typed map).
func attrMapAsAny(trace map[string]any, key string) map[string]any {
	if trace == nil {
		return nil
	}
	if v, ok := trace[key].(map[string]any); ok {
		return v
	}
	if v, ok := trace[key].(map[string]string); ok {
		out := make(map[string]any, len(v))
		for k, vv := range v {
			out[k] = vv
		}
		return out
	}
	if s, ok := trace[key].(string); ok && s != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err == nil {
			return m
		}
	}
	return nil
}

// attrAsString returns the attribute value as a string, accepting strings and
// numerics. OTel attribute values can be any JSON-typed primitive — when the
// producer uses a typed map the value may arrive as int64/float64 (e.g.
// rpc.grpc.status_code = 14) rather than a string. Matches the coercion used
// by api-server/services/observability.traceGetString.
func attrAsString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case int:
		return strconv.Itoa(x)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case bool:
		return strconv.FormatBool(x)
	default:
		return fmt.Sprint(x)
	}
}

// isEmptyAttr reports whether an attribute value carries no signal.
// Treats nil and empty string as empty, but NOT the integer 0 or false —
// those are valid gRPC/HTTP/boolean attribute values.
func isEmptyAttr(v any) bool {
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok {
		return s == ""
	}
	return false
}

// compactResourceAttributes filters the resource attribute map down to the
// keys that identify the pod/deployment and runtime. Full ResourceAttributes
// maps are ~30 keys and most are redundant (OS version, process args, SDK
// versions) — keeping the noisy ones wastes LLM tokens without adding signal.
func compactResourceAttributes(attrs map[string]any) map[string]any {
	keep := []string{
		"service.name", "service.namespace", "service.version",
		"k8s.deployment.name", "k8s.namespace.name", "k8s.pod.name", "k8s.node.name",
		"host.name", "cluster",
	}
	out := make(map[string]any, len(keep))
	for _, k := range keep {
		if v, ok := attrs[k]; ok && !isEmptyAttr(v) {
			out[k] = v
		}
	}
	return out
}

// isHTTPError returns true for 4xx/5xx HTTP status strings.
func isHTTPError(status string) bool {
	if len(status) == 0 {
		return false
	}
	switch status[0] {
	case '4', '5':
		return true
	}
	return false
}

func (l AgentEvents) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	if strings.EqualFold(toolRequest.Tool, tools.ToolEventExecuteSql) {
		eventsData := []events.Event{}
		err := common.UnmarshalJson([]byte(toolResponse), &eventsData)
		if err != nil {
			return toolResponse
		}

		// Check if the unmarshaled data contains actual event informationIf all events have empty Title/Description/AggregationKey, it's likely the response contains count or other non-event data
		hasEventData := false
		for _, event := range eventsData {
			if event.Title != "" || event.Description != "" || event.AggregationKey != "" {
				hasEventData = true
				break
			}
		}

		if !hasEventData {
			return toolResponse
		}

		finalResponse := []map[string]any{}

		for _, event := range eventsData {
			eventData := reduceEventData(event)
			finalResponse = append(finalResponse, eventData)
		}

		toolResponseArr, err := common.MarshalJson(finalResponse)
		if err != nil {
			return toolResponse
		}
		toolResponse = string(toolResponseArr)
	}
	return toolResponse
}

const ToolEventSummary = "event_summary"

type EventSummaryTool struct {
}

func (m EventSummaryTool) Name() string {
	return ToolEventSummary
}

func (m EventSummaryTool) GetType() toolcore.NBToolType {
	return toolcore.NBToolTypeTool
}

func (m EventSummaryTool) Description() string {
	return `summarize Events Data`
}

func (m EventSummaryTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"command": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: "Events Data",
			},
		},
		Required: []string{"command"},
	}
}

func GetCloudProviderForAccount(accountId string) string {
	if accountId == "" {
		return ""
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return ""
	}
	var cloudProvider string
	err = dbms.Db.Get(&cloudProvider, "SELECT cloud_provider FROM cloud_accounts WHERE id = $1", accountId)
	if err != nil {
		return ""
	}
	return cloudProvider
}

func parseEventLabels(labelsStr string) map[string]any {
	result := map[string]any{}
	if labelsStr == "" {
		return result
	}
	if err := common.UnmarshalJson([]byte(labelsStr), &result); err != nil {
		slog.Error("analyzer: failed to parse event labels", "error", err, "labels", labelsStr)
	}

	return result
}

func (m EventSummaryTool) Call(nbRequestContext toolcore.NbToolContext, input toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	dataContext := input.Context
	if len(dataContext) == 0 {
		dataContext = nbRequestContext.QueryContext
	}

	if len(dataContext) == 0 {
		dataContext = "No additional context provided"
	}

	// read User Original Question from context
	nbRequestContext.Ctx.GetLogger().Debug("eventsummary: additional context provided", "context", dataContext)
	// Extract the user question if it follows the "User Original Question:" pattern
	userQuestion := dataContext
	if strings.Contains(dataContext, "User Original Question:") {
		parts := strings.Split(dataContext, "User Original Question:")
		if len(parts) > 1 {
			userQuestion = strings.TrimSpace(parts[1])
			nbRequestContext.Ctx.GetLogger().Debug("eventsummary: extracted user question", "question", userQuestion)
		}
	}

	dataSplits := strings.SplitN(userQuestion, "Answer:", 2)
	if len(dataSplits) == 2 {
		dataContext = strings.TrimSpace(dataSplits[1])
	}
	eventData := []events.Event{}
	err := common.UnmarshalJson([]byte(dataContext), &eventData)
	serviceLabelFound := true
	eventDataToProcess := []string{}
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("eventsummary: unable to unmarshal event data", "error", err)
		eventDataToProcess = append(eventDataToProcess, dataContext)
	} else {
		for _, event := range eventData {
			var parsedLabels map[string]any
			if labelsStr, ok := event.Labels.(string); ok {
				parsedLabels = parseEventLabels(labelsStr)
			}
			if source, ok := parsedLabels["nb_webhook_source"].(string); ok && strings.HasPrefix(source, "datadog") {
				// Check if services or service labels exist and have non-empty values
				if common.HasNonEmptyValue(parsedLabels["services"]) || common.HasNonEmptyValue(parsedLabels["service"]) {
					serviceLabelFound = true
				} else {
					serviceLabelFound = false
				}
			}
			eventMap := reduceEventData(event)
			dataBytes, err := common.MarshalJson(eventMap)
			if err != nil {
				nbRequestContext.Ctx.GetLogger().Error("eventsummary: unable to marshal event item", "error", err)
				continue
			}
			eventDataToProcess = append(eventDataToProcess, string(dataBytes))
		}
	}

	// Batch all events into a single LLM call instead of one-per-event
	var eventPrompt string
	if serviceLabelFound {
		eventPrompt = prompts_repo.GetPrompt(prompts_repo.PromptEventSummary)
	} else {
		eventPrompt = prompts_repo.GetPrompt(prompts_repo.PromptEventGeneralSummary)
	}

	// Add account context (cloud provider) as a hidden system instruction
	if cloudProvider := GetCloudProviderForAccount(nbRequestContext.AccountId); cloudProvider != "" {
		eventPrompt = eventPrompt + "\n\n[INTERNAL INSTRUCTION - DO NOT INCLUDE IN OUTPUT]: The infrastructure runs on " + cloudProvider + ". Use this context to guide your analysis approach, but do NOT mention the cloud provider context, infrastructure type, or deployment controller details in your output. Your response must remain provider-agnostic. Focus only on the evidence and findings."
	}

	batchedEventData := strings.Join(eventDataToProcess, "\n\n---NEXT EVENT---\n\n")
	batchInstruction := input.Command
	if len(eventDataToProcess) > 1 {
		batchInstruction = input.Command + "\n\nIMPORTANT: The data below contains multiple events separated by '---NEXT EVENT---'. Analyze each event and produce a summary section for EACH event."
	}

	messageContent := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, eventPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, batchInstruction+"\n\n --- JSON Data -- \n"+batchedEventData),
	}
	completion, err := core.GenerateAndTrackLLMContent(nbRequestContext.Ctx, nbRequestContext.UserId, nbRequestContext.AccountId, nbRequestContext.ConversationId, nbRequestContext.MessageId, nbRequestContext.ParentAgentId, true, messageContent, true)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("eventsummary: unable to generate content", "error", err)
		return toolcore.NBToolResponse{}, err
	}

	// Clean XML tags from the response since we only want markdown output
	combinedContent := RemoveXMLTags(completion.Choices[0].Content)

	return toolcore.NBToolResponse{
		Data: combinedContent,
		Type: toolcore.NBToolResponseTypeText,
	}, nil
}
