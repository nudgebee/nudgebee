package tools

import (
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
	"strings"
)

const ToolLogsExecute = "logs_execute"

// logsUIRef builds the "#monitoring/logs" source reference for a
// logs_execute_v2 response (see tool_logs_v2.go). It carries the structured
// where-clause as the `filter` query
// param (URL-encoded JSON) — the exact shape the Logs tab's initializeQuery
// consumes — instead of a raw provider DSL (LogQL / ES JSON / Signoz filters)
// the UI cannot parse. When there is no where-clause, the bare tab link is
// emitted so the source still navigates correctly.
func logsUIRef(nbRequestContext core.NbToolContext, qb core.QueryBuilder) core.NBToolResponseReference {
	var params map[string]string
	if w := qb.Where; w.Binary != nil || len(w.And) > 0 || len(w.Or) > 0 || w.Not != nil {
		if b, err := common.MarshalJson(w); err == nil {
			params = map[string]string{"filter": string(b)}
		}
	}
	return core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"monitoring", "logs"}, "Query Logs", params, "")
}

// NewNBLogToolWithProvider builds against an already-resolved provider, so labels
// come from the same backend the caller is querying (may be a request override).
func NewNBLogToolWithProvider(accountId string, logProvider services_server.ObservabilityProvider) *NBLogTool {
	return &NBLogTool{
		accountId:   accountId,
		logProvider: logProvider,
	}
}

type NBLogTool struct {
	accountId   string
	logProvider services_server.ObservabilityProvider
	labels      []string
}

func (t *NBLogTool) Name() string {
	return ToolLogsExecute
}

func (t *NBLogTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (t *NBLogTool) Description() string {
	return `Executes a structured log query and returns matching log lines.
		Input: a QueryBuilder JSON object whose "where" clause is built from field/operator conditions (use the special field "_body" to match message text); plus optional "index" (log index/pattern), "limit" (max lines), and a time window via start_time/end_time (RFC3339 or Unix) or range (e.g. '1h', '2d').
		Example: {"where": {"_and": [{"kubernetes.namespace_name": {"_eq": "prod"}}, {"_body": {"_ilike": "error"}}]}, "limit": 200, "range": "1h"}`
}

func (t *NBLogTool) QueryLabels() []string {
	if len(t.labels) > 0 {
		return t.labels
	}
	logLabels, err := executeFetchLogLabels(t.accountId, t.logProvider)
	if err != nil {
		// Callers degrade to backend defaults on an empty list — without this warn
		// a bad override (e.g. a backend the account has no integration for) is invisible.
		slog.Warn("logs: unable to fetch provider labels", "error", err,
			"provider", t.logProvider.Provider, "account_id", t.accountId)
		return []string{}
	}
	labels := make([]string, 0, len(logLabels.Labels))
	for _, label := range logLabels.Labels {
		if IsESLogProvider(t.logProvider.Provider) {
			if skipESLabel(label.Label) {
				continue
			}
		}
		labels = append(labels, label.Label)
	}
	if IsESLogProvider(t.logProvider.Provider) {
		labels = deduplicateESLabels(labels)
		labels = capESLabels(labels)
	}
	if len(labels) > 0 {
		t.labels = append(labels, "_body")
	}
	slog.Info("logs: QueryLabels complete", "account_id", t.accountId, "provider", t.logProvider.Provider, "raw_labels_count", len(logLabels.Labels), "final_labels_count", len(t.labels), "final_labels", t.labels)
	return t.labels
}

// maxESLabelsInPrompt is the cap on how many ES field names are injected into the
// LLM prompt. Real ES indices can return thousands of fields; dumping all of them
// wastes tokens and confuses the model. Priority fields are always included first;
// remaining slots are filled from the filtered/deduped list.
const maxESLabelsInPrompt = 50

// esLabelPriority lists the most useful ES fields for log querying in priority order.
// These are always included first (up to maxESLabelsInPrompt), regardless of what the
// index returns, as long as they exist in the actual label set.
//
// Every k8s concept is listed in BOTH shipper conventions, and each with and without
// the `.keyword` suffix. Fluent-Bit flattens to `kubernetes.namespace_name` while
// ECS (Beats / Elastic Agent) writes `kubernetes.namespace`, and the same cluster can
// carry both. This list previously named only the Fluent-Bit + `.keyword` spellings,
// so on an ECS estate the genuinely queryable fields were the ones NOT promoted — and
// with a 50-field cap the model was steered toward names its data does not use.
//
// Entries absent from the account's real label set are skipped by capESLabels, so
// listing every spelling costs nothing and lets one list serve both conventions.
var esLabelPriority = []string{
	// namespace
	"kubernetes.namespace",
	"kubernetes.namespace.keyword",
	"kubernetes.namespace_name",
	"kubernetes.namespace_name.keyword",
	// pod
	"kubernetes.pod.name",
	"kubernetes.pod.name.keyword",
	"kubernetes.pod_name",
	"kubernetes.pod_name.keyword",
	// container
	"kubernetes.container.name",
	"kubernetes.container.name.keyword",
	"kubernetes.container_name",
	"kubernetes.container_name.keyword",
	// workload / app labels
	"kubernetes.deployment.name",
	"kubernetes.deployment_name",
	"kubernetes.labels.app",
	"kubernetes.labels.app.keyword",
	"kubernetes.labels.app_kubernetes_io/name",
	"kubernetes.labels.app_kubernetes_io/name.keyword",
	"kubernetes.labels.app_kubernetes_io/component",
	"kubernetes.labels.app_kubernetes_io/component.keyword",
	"kubernetes.labels.app_kubernetes_io/instance",
	"kubernetes.labels.app_kubernetes_io/instance.keyword",
	// host / stream / body
	"kubernetes.host",
	"kubernetes.host.keyword",
	"stream",
	"stream.keyword",
	"log",
	"log.keyword",
	"message",
}

// IsESLogProvider returns true if the provider string identifies an Elasticsearch log provider.
func IsESLogProvider(provider string) bool {
	p := strings.ToLower(provider)
	return p == "es" || p == "elasticsearch"
}

// skipESLabel returns true for ES fields that are not useful for log querying:
// internal ES metadata fields and un-queryable parent container objects.
func skipESLabel(label string) bool {
	// Internal ES metadata: _id, _source, _version, _seq_no, _routing, etc.
	if strings.HasPrefix(label, "_") {
		return true
	}
	// Parent container objects — not directly filterable leaf fields.
	switch label {
	case "kubernetes", "kubernetes.labels", "kubernetes.annotations", "@timestamp", "time":
		return true
	}
	return false
}

// deduplicateESLabels removes text-field variants when the corresponding .keyword
// field is already present (e.g. keeps "kubernetes.pod_name.keyword", drops "kubernetes.pod_name").
func deduplicateESLabels(labels []string) []string {
	labelSet := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		labelSet[l] = struct{}{}
	}
	result := make([]string, 0, len(labels))
	for _, l := range labels {
		if !strings.HasSuffix(l, ".keyword") {
			if _, hasKeyword := labelSet[l+".keyword"]; hasKeyword {
				continue // prefer the .keyword variant
			}
		}
		result = append(result, l)
	}
	return result
}

// capESLabels limits the label list to maxESLabelsInPrompt entries, always
// placing priority fields first so the most useful ones are never crowded out.
func capESLabels(labels []string) []string {
	if len(labels) <= maxESLabelsInPrompt {
		return labels
	}
	labelSet := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		labelSet[l] = struct{}{}
	}

	result := make([]string, 0, maxESLabelsInPrompt)
	added := make(map[string]struct{}, maxESLabelsInPrompt)

	// Phase 1: priority fields that exist in the actual label set.
	for _, p := range esLabelPriority {
		if _, exists := labelSet[p]; exists {
			result = append(result, p)
			added[p] = struct{}{}
		}
	}

	// Phase 2: fill remaining slots from the filtered list (preserving original order).
	for _, l := range labels {
		if len(result) >= maxESLabelsInPrompt {
			break
		}
		if _, seen := added[l]; !seen {
			result = append(result, l)
		}
	}

	slog.Info("es labels capped for prompt", "total", len(labels), "capped", len(result))
	return result
}

func (t *NBLogTool) QueryLabelValues() []string {
	return []string{}
}

func (t *NBLogTool) GetOperators() []string {
	return []string{}
}

func (t *NBLogTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "JSON query to Execute",
			},
			"start_time": {
				Type:        core.ToolSchemaTypeString,
				Description: "Start Time for the query. Format: RFC3339 or Unix timestamp",
			},
			"end_time": {
				Type:        core.ToolSchemaTypeString,
				Description: "End Time for the query. Format: RFC3339 or Unix timestamp",
			},
			"range": {
				Type:        core.ToolSchemaTypeString,
				Description: "Time range for the query (e.g., '2d', '1w', '1h'). If provided, start_time is calculated relative to end_time.",
			},
			"index": {
				Type:        core.ToolSchemaTypeString,
				Description: "Elasticsearch index name or pattern to query (e.g., 'app-logs-*', 'nginx-access-*'). If not specified, the account's default index is used.",
			},
			"limit": {
				Type:        core.ToolSchemaTypeInteger,
				Description: "Optional maximum number of log lines to return.",
			},
		},
		Required: []string{"command"},
	}
}
