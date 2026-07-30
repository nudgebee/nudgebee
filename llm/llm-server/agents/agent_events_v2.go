package agents

import (
	"strings"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// EventsV2AgentName registers a second, independent events agent alongside
// the original "events" agent (agent_events.go), per the permanent-parallel
// decision recorded for this change — see docs/architecture-decisions.md.
// It is intentionally NOT registered as a discoverable sub-agent tool
// (RegisterNBAgentFactory, not …AndTool): events_v2 is reachable only via
// top-level agent selection — an explicit "@events_v2" mention, the LLM
// router, or the "events"-redirect below.
//
// events_v2 prefers three deterministic, parameter-typed tools
// (get_event_by_id, list_events, aggregate_events — tools/tool_event_v2.go)
// over LLM-authored raw SQL for the query shapes real events_execute traffic
// showed are dominant. events_execute/anomaly_execute remain available as a
// fallback for the long tail those tools don't cover (free-text search,
// anomaly-table queries, unusual filter combinations).
//
// Gated by config.Config.EventsV2Enabled (default false) — a kill switch
// while the agent is new/unproven, checked inside the factory closure (which
// runs per-request, well after config is loaded, unlike this init()).
// Matching the TicketV2Enabled precedent, chain_router.go's getEventsAgentName()
// redirects top-level "events" resolution (explicit @events mention, LLM
// router, or InferAgent's history-reuse) to events_v2 when the flag is on.
// This does NOT cover "events" invoked as a sub-agent tool by other
// orchestrators (e.g. k8s_orchestrator) — that path resolves the tool
// registered under the literal name "events" (agent_events.go's
// RegisterNBAgentFactoryAndToolAndPrioritizeAgentResponseForTool) directly via
// core.GetNBAgent, bypassing chain_router.go's switch entirely. Sub-agent-tool
// callers keep hitting v1 regardless of the flag until/unless that's revisited.
const EventsV2AgentName = "events_v2"

func init() {
	core.RegisterNBAgentFactory(EventsV2AgentName, func(accountId string) (core.NBAgent, error) {
		return newEventsV2Agent(accountId), nil
	})
}

func newEventsV2Agent(accountId string) core.NBAgent {
	return AgentEventsV2{accountId: accountId}
}

type AgentEventsV2 struct {
	accountId string
}

func (a AgentEventsV2) GetName() string {
	return EventsV2AgentName
}

func (a AgentEventsV2) GetNameAliases() []string {
	return []string{"events_v2", "events2"}
}

func (a AgentEventsV2) GetDescription() string {
	return `Events agent (v2, tool-first): answers questions about alerts, configuration changes, deployments, Kubernetes events, anomalies, SLO violations, and incident investigations using deterministic structured tools (get_event_by_id, list_events, aggregate_events) rather than LLM-generated SQL, falling back to raw SQL only for filters those tools can't express. Same domain coverage as the "events" agent.
Primary keywords (signal words): configuration, deployment, kubernetes, alert, anomaly, slo, error, event, monitoring, incident, oom, crash, outage.`
}

func (a AgentEventsV2) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (a AgentEventsV2) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{
		tools.GetEventByIdTool{}, tools.ListEventsTool{}, tools.AggregateEventsTool{},
		tools.EventsExecuteTool{}, tools.AnomalyExecuteTool{}, EventSummaryTool{}, tools.GetEventEvidenceTool{},
		tools.TriageExplanationTool{}, tools.TriageRulesTool{}, tools.ThresholdSuggestionsTool{}, tools.TriageDryRunTool{},
		tools.EventRulesTool{}, tools.EventClassificationTool{}, tools.TriageRuleEventsTool{},
	}
}

func (a AgentEventsV2) GetSummaryToolName() string {
	return ToolEventSummary
}

// UpdateToolResponseForPlanner reduces raw events.Event rows down to
// reduceEventData's curated shape before they enter the scratchpad — the same
// cleanup agent_events.go applies to events_execute. Only tools that return
// full-evidence (InvestigateData-shaped) rows qualify: events_execute
// (raw-SQL fallback) and get_event_by_id. list_events is deliberately
// excluded — it always returns the compact evidence *manifest*
// (buildEvidenceManifest's available_evidence_types/insights/has_logs shape),
// which shares no field names with InvestigateData. Unmarshaling a manifest
// through reduceEventData would silently zero out every Evidences field and
// drop the manifest entirely rather than clean it.
func (a AgentEventsV2) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	if !strings.EqualFold(toolRequest.Tool, tools.ToolEventExecuteSql) && !strings.EqualFold(toolRequest.Tool, tools.ToolGetEventById) {
		return toolResponse
	}

	eventsData := []events.Event{}
	if err := common.UnmarshalJson([]byte(toolResponse), &eventsData); err != nil {
		return toolResponse
	}

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
		finalResponse = append(finalResponse, reduceEventData(event))
	}

	toolResponseArr, err := common.MarshalJson(finalResponse)
	if err != nil {
		return toolResponse
	}
	return string(toolResponseArr)
}

func (a AgentEventsV2) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**TOOL-FIRST RULE:** Prefer the structured tools — get_event_by_id, list_events, aggregate_events — over events_execute/anomaly_execute raw SQL. Only write raw SQL when a structured tool genuinely cannot express the request: free-text search over title/description, filter combinations the structured tools don't support, or anomaly-table (workload metric anomaly) queries.",
		"**get_event_by_id:** single-event UUID lookups and 'why was this event triaged this way' questions. Returns full evidence — combine with get_triage_explanation for the dedup chain and correlations.",
		"**list_events:** any 'show me / list / recent events matching X' question. Takes typed filters (subject_name, subject_namespace, subject_type, finding_type, aggregation_key, priority, time range). Returns a compact evidence *manifest* per event, not full evidence — call get_event_by_id or get_event_evidence afterward to drill into a specific one.",
		"**aggregate_events:** 'how many X' / 'top N by Y' / alert-noise questions. group_by is one of: aggregation_key, subject_namespace, subject_type, finding_type, priority, source, nb_status.",
		"**IMPORTANT — NO INVENTION:** Do NOT invent resource names, namespaces, timestamps, or other facts. If the incoming JSON lacks any field, say 'unknown' for that field and DO NOT guess.",
		"**Anomalies (workload metric anomaly detection) vs finding_type='Anomaly' (event table):** when the user asks about 'anomalies' / 'anomaly detection' on a *workload* (CPU/Memory/Latency/Network/ErrorRate/Replicas), use anomaly_execute against the `anomaly` table, not list_events/aggregate_events — those only query the `events` table.",
		"",
		"## Raw SQL fallback (events_execute / anomaly_execute)",
		"    - Only reach for this when the structured tools can't express the request.",
		"    - Prefer a targeted column list over SELECT * for list-style questions — SELECT * is appropriate only for a true single-event detail dump (and get_event_by_id already covers that case, so raw SQL rarely needs SELECT * at all).",
		"    - ALWAYS include LIMIT for row-returning queries; omit LIMIT only for COUNT/aggregation queries.",
		"    - Quote timestamp literals: `starts_at >= '2024-05-07T09:07:53.710Z'` — never bare or double-quoted.",
		"",
		"## Triage Literacy (how Nudgebee auto-triages events)",
		"    Every event flows through an automatic pipeline: deduplicate → triage rules → correlation → scoring → AI-analysis gate.",
		"    - nb_status is set by the HIGHEST-PRIORITY matching triage rule, INDEPENDENT of the score. A P0 event can still be SUPPRESSED if a suppression rule matched.",
		"    - computed_score is fully explained by the `score_factors` column (returned by get_event_by_id): base_severity × env_multiplier × 4 = raw_score, then duplicate_penalty, correlation_adjustment, finding_type_adjustment and evidence_bonus are applied.",
		"    - fingerprint groups recurring occurrences. Use aggregate_events with count_distinct_fingerprint=true to separate unique patterns from raw occurrence counts.",
		"    To EXPLAIN one event's triage decision: get_event_by_id(event_id), then get_triage_explanation(event_id) for the dedup chain + correlations.",
		"    For an ALERT-NOISE / HYGIENE report: aggregate_events(group_by='aggregation_key') and aggregate_events(group_by='aggregation_key', count_distinct_fingerprint=true) to compare firings vs distinct patterns, then get_triage_rules to surface coverage gaps.",
		"    For THRESHOLD tuning: call list_threshold_suggestions; highlight high estimated_reduction + tune_threshold/disable rows, flag low-confidence MAD=0 rows as weak.",
		"    To PROPOSE a new triage rule: call dryrun_triage_rule with the candidate criteria to get the projected volume reduction, present the number, then direct the user to create the rule in the UI.",
		"    Distinguish EVENT RULES (alert definitions that GENERATE events; read with get_event_rules) from TRIAGE RULES (suppress/score/classify events AFTER they fire; read with get_triage_rules).",
		"    READ-ONLY: you explain and recommend. You never create/modify rules, change thresholds, or reclassify events — always tell the user to apply changes in the Nudgebee UI. Never claim to have applied a change.",
	}

	constraints := []string{
		"You MUST use get_event_by_id, list_events, or aggregate_events for events-table questions unless none of them can express the request.",
		"You MUST use anomaly_execute (not the events-table tools) for workload metric anomaly-detection questions.",
		"You MUST NOT answer questions without first calling a tool to query the data.",
		"When falling back to raw SQL, you MUST provide actual SQL queries, NOT descriptions or explanations.",
	}

	toolUsage := map[string][]string{
		tools.ToolGetEventById: {
			"Single-event detail lookup by UUID. Returns all columns + full evidence.",
			"Input: event_id (required).",
		},
		tools.ToolListEvents: {
			"Filtered event list/search. Returns compact evidence manifests, not full evidence.",
			"Input: all filters optional — subject_name, subject_namespace[], subject_type, finding_type[], aggregation_key[], priority[], start_time/end_time or relative_range, limit (default 10).",
		},
		tools.ToolAggregateEvents: {
			"Count/group events by one column.",
			"Input: group_by (required, one of aggregation_key/subject_namespace/subject_type/finding_type/priority/source/nb_status), optional count_distinct_fingerprint, subject_namespace[], finding_type[], time range.",
		},
		tools.ToolEventExecuteSql: {
			"Raw-SQL fallback for events-table questions the structured tools can't express (free-text title/description search, unusual filter combinations).",
			"Input: MUST be a valid SQL query, e.g. \"SELECT id, title, starts_at FROM events WHERE title ILIKE '%connection refused%' ORDER BY starts_at DESC LIMIT 5\"",
		},
		tools.ToolAnomalyExecuteSql: {
			"Raw-SQL for the anomaly (workload metric anomaly detection) table — always used for this table, there is no structured-tool equivalent yet.",
			"Input: MUST be a valid SQL query. CRITICAL: ALWAYS include LIMIT (default 10).",
			"Example: SELECT * FROM anomaly WHERE name ilike 'llm-server%' AND anomaly_type = 'Latency' AND is_anomaly = true ORDER BY evaluated_at DESC LIMIT 10",
		},
		"event_summary": {
			"Use this tool to format and summarize event data when you have a small result set with full evidence.",
			"Input: events data. Output: events in markdown format.",
		},
		tools.ToolGetEventEvidence: {
			"Fetch detailed evidence for a specific event by ID — use after list_events to drill into one event's manifest.",
			"Input: event_id (required), evidence_type (optional: logs, pod_metrics, node_metrics, traces, deployment, pod_events, node_events, pod_data, alert_labels, noisy_neighbours, related_events, api_failures, all).",
			"Strategy: for a broad or multi-hypothesis investigation (e.g. root-cause analysis), call this ONCE with evidence_type='all' rather than fetching types one at a time — cheaper and avoids redundant round-trips. Only request a single specific evidence_type when you already know exactly which one you need.",
		},
		tools.ToolTriageExplanation: {
			"Explains HOW a single event was triaged (why DUPLICATE/SUPPRESSED or its computed_priority).",
			"Input: event_id (required). Combine with get_event_by_id's score_factors for a complete explanation.",
		},
		tools.ToolTriageRules: {
			"Lists configured triage rules (suppression/scoring/classification).",
			"Input: optional rule_type, optional enabled.",
		},
		tools.ToolThresholdSuggestions: {
			"Lists threshold-tuning suggestions for noisy alerts.",
			"Input: optional source, confidence, limit.",
		},
		tools.ToolTriageDryRun: {
			"PREVIEWS the impact of a candidate triage rule — does NOT create it.",
			"Input: rule_type and action (required), plus match criteria.",
		},
		tools.ToolGetEventRules: {
			"Inspects alert/event RULE DEFINITIONS (the alerting rules), NOT triage rules.",
			"Input: optional filters alert, source, severity, alert_type, namespace, enabled, limit.",
		},
		tools.ToolEventClassification: {
			"Gets the classification VERDICT for an event (true_positive/false_positive/benign_positive/duplicate).",
			"Input: event_id.",
		},
		tools.ToolTriageRuleEvents: {
			"Lists events a specific triage rule matched.",
			"Input: rule_id (from get_triage_rules), optional limit.",
		},
	}

	// Trimmed: only columns the structured tools don't already expose as
	// typed parameters, for the raw-SQL fallback path. See tools/tool_event_v2.go
	// for the get_event_by_id/list_events/aggregate_events column sets.
	schema := []string{
		"**events:** kubernetes/prometheus/config-change event data — same table the structured tools query.",
		"Columns beyond what get_event_by_id/list_events/aggregate_events already expose as parameters:",
		"description text, title text — free text, ILIKE searchable (structured tools don't support free-text search).",
		"source text = event source (prometheus, kubernetes_api_server, pagerduty_webhook, datadog_webhook, AWS_CloudWatch_Alarm, etc). GROUP BY/SELECT DISTINCT to discover values.",
		"status text = FIRING/CLOSED/RESOLVED. Most events are CLOSED — only filter when explicitly asked.",
		"fingerprint text = recurrence grouping key (SHA hash). Prefer aggregate_events(count_distinct_fingerprint=true) over hand-written COUNT(DISTINCT fingerprint).",
		"labels jsonb = event labels as key-value pairs.",
		"snoozed_until timestamp, service_key text, subject_owner text, category text, urgency text, score_confidence numeric, cluster text.",
		"Strictly don't use evidence or description columns in a WHERE clause filter beyond a single ILIKE term — they're large free-text/JSON fields.",
		"",
		"**anomaly:** workload metric anomaly detection data — always queried via anomaly_execute, no structured-tool equivalent yet.",
		"id, created_at, updated_at, name (workload), namespace, reference_value (json), current_value numeric,",
		"anomaly_type (Latency, Memory, CPU, Network, ErrorRate, Replicas), is_anomaly boolean, evaluated_at, pod_name, config_id.",
		"Order by evaluated_at DESC. ALWAYS include LIMIT for row queries (default 10); omit for COUNT/GROUP BY.",
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "What are latest errors?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolListEvents,
					Input: `{"finding_type": ["issue"], "priority": ["HIGH"], "limit": 3}`,
				},
			},
			Explanation: "List-style question — use list_events with typed filters instead of hand-writing SQL.",
		},
		{
			Question: "Get the details of event <id> and explain how it was triaged.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetEventById, Input: `{"event_id": "your-event-id"}`},
				{Tool: tools.ToolTriageExplanation, Input: `{"event_id": "your-event-id"}`},
			},
			Explanation: "Single-event detail + triage question — the dominant real query shape for this agent. get_event_by_id returns full evidence and score_factors; get_triage_explanation adds the dedup chain and correlations.",
		},
		{
			Question: "Show recent OOM or pod restart events in the nudgebee, redis, and rabbit namespaces.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolListEvents,
					Input: `{"subject_namespace": ["nudgebee", "redis", "rabbit"], "aggregation_key": ["pod_oom_killer_enricher", "report_crash_loop"], "limit": 10}`,
				},
			},
			Explanation: "subject_namespace and aggregation_key both take LISTS — an 'OOM or restart across several namespaces' question needs every value passed together in one call, not separate calls per namespace/key or a raw-SQL fallback.",
		},
		{
			Question: "How many events are there per aggregation key in the last 7 days?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolAggregateEvents,
					Input: `{"group_by": "aggregation_key", "relative_range": "7d"}`,
				},
			},
			Explanation: "Grouping/counting question — use aggregate_events instead of hand-written GROUP BY/COUNT SQL.",
		},
		{
			Question: "Give me an alert-noise report for the last 30 days.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolAggregateEvents, Input: `{"group_by": "aggregation_key", "relative_range": "30d"}`},
				{Tool: tools.ToolAggregateEvents, Input: `{"group_by": "aggregation_key", "count_distinct_fingerprint": true, "relative_range": "30d"}`},
				{Tool: tools.ToolTriageRules, Input: `{"rule_type": "suppression"}`},
			},
			Explanation: "Compare raw firings vs distinct fingerprints per alert to find noisy ones, then surface configured suppression rules to spot coverage gaps.",
		},
		{
			Question: "Find events mentioning 'connection refused' in the title.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolEventExecuteSql,
					Input: "SELECT id, title, starts_at, priority FROM events WHERE title ILIKE '%connection refused%' ORDER BY starts_at DESC LIMIT 5",
				},
			},
			Explanation: "Free-text search isn't a list_events parameter — this is the raw-SQL fallback case.",
		},
		{
			Question: "What are the latency anomalies for llm-server?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolAnomalyExecuteSql,
					Input: "SELECT * FROM anomaly WHERE name ilike 'llm-server%' AND anomaly_type = 'Latency' AND is_anomaly = true ORDER BY evaluated_at DESC LIMIT 10",
				},
			},
			Explanation: "Workload metric anomaly question — always the anomaly table via anomaly_execute, never list_events/aggregate_events.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "an events and anomaly investigation expert",
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
