package tools

import (
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"strings"
	"time"
)

func init() {
	core.RegisterNBToolFactory(ToolAnomalyExecuteSql, func(accountId string) (core.NBTool, error) {
		return AnomalyExecuteTool{}, nil
	})
}

const ToolAnomalyExecuteSql = "anomaly_execute"

const anomalyView = `
SELECT
    id::text,
    created_at,
    updated_at,
    name,
    namespace,
    reference_value::text,
    current_value,
    anomaly_type,
    is_anomaly,
    evaluated_at,
    pod_name,
    config_id::text
FROM anomaly
WHERE account_id = '__ACCOUNT_ID__'::uuid AND evaluated_at >= '__DATE_FILTER__'
ORDER by evaluated_at desc
`

type AnomalyExecuteTool struct {
}

func (m AnomalyExecuteTool) Name() string {
	return ToolAnomalyExecuteSql
}

func (m AnomalyExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m AnomalyExecuteTool) Description() string {
	return "Executes a SQL query for anomaly data and returns anomaly detection results. The table name is 'anomaly' (singular, NOT 'anomalies'). Provide a SQL SELECT statement in the 'command' field. Available columns: id, created_at, updated_at, name, namespace, reference_value, current_value, anomaly_type, is_anomaly, evaluated_at, pod_name, config_id. For cost anomalies filter anomaly_type IN ('CloudSpendService','CloudSpendAccount'). Date macros like '[[Time:-30d]]' are supported in WHERE clauses."
}

// costAnomalyTemplate is a ready-to-run query returned in error messages so the
// LLM can self-correct in a single step instead of burning retries.
const costAnomalyTemplate = "SELECT name, namespace, anomaly_type, reference_value, evaluated_at FROM anomaly WHERE anomaly_type IN ('CloudSpendService', 'CloudSpendAccount') AND evaluated_at >= '[[Time:-30d]]' ORDER BY evaluated_at DESC LIMIT 20"

// extractAnomalySQL recovers a SQL statement when the model wrapped it in a JSON
// object (e.g. {"query":"SELECT ..."} or {"sql":"..."}) or routed it through the
// structured arguments map. The executor leaves request.Command as the raw JSON
// string when the payload has no "command" key, so without this the tool would
// treat the whole JSON blob as the query and reject it as invalid SQL.
func extractAnomalySQL(command string, arguments map[string]any) string {
	if trimmed := strings.TrimSpace(command); strings.HasPrefix(trimmed, "{") {
		var parsed map[string]any
		if err := common.UnmarshalJson([]byte(trimmed), &parsed); err == nil {
			for _, key := range []string{"command", "query", "sql"} {
				if q, ok := parsed[key].(string); ok && strings.TrimSpace(q) != "" {
					return strings.TrimSpace(q)
				}
			}
		}
	}
	// The executor routes unknown top-level keys (e.g. {"query":"..."}) into
	// Arguments, so check there too.
	for _, key := range []string{"query", "sql", "command"} {
		if q, ok := arguments[key].(string); ok && strings.TrimSpace(q) != "" {
			return strings.TrimSpace(q)
		}
	}
	return strings.TrimSpace(command)
}

func (m AnomalyExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "anomaly SQL Query to execute",
			},
		},
		Required: []string{"command"},
	}
}

// InferToolRequestType classifies this tool as read-only so it skips LLM-based auth classification.
func (m AnomalyExecuteTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func (m AnomalyExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	// Recover SQL the model may have wrapped in JSON ({"query":...}) or routed
	// through structured arguments before validating.
	input.Command = extractAnomalySQL(input.Command, input.Arguments)

	// Validate that the input is actually SQL, not descriptive text
	if isDescriptiveText(input.Command) {
		return core.NBToolResponse{
			Data:   "Error: Provide a SQL SELECT statement in the 'command' field, not a description. For cost anomalies use: " + costAnomalyTemplate,
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	if !isValidSQL(input.Command) {
		return core.NBToolResponse{
			Data:   "Error: Invalid SQL query. Provide a SQL SELECT statement in the 'command' field. For cost anomalies use: " + costAnomalyTemplate,
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	// check only for last 30 days data
	anomalyView1 := anomalyView

	if strings.Contains(anomalyView1, "__DATE_FILTER__") {
		last30days := time.Now().AddDate(0, 0, -30)
		daysStr := last30days.Format("2006-01-02")
		anomalyView1 = strings.ReplaceAll(anomalyView1, "__DATE_FILTER__", daysStr)
	}

	if nbRequestContext.AccountId != "" {
		anomalyView1 = strings.ReplaceAll(anomalyView1, "__ACCOUNT_ID__", nbRequestContext.AccountId)
	}

	input.Command = strings.TrimSuffix(input.Command, ";")
	input.Command = common.SubstituteDateMacros(input.Command)

	resp, data, err := sqlToolCall(nbRequestContext, input.Command, "anomaly", anomalyView1, 0, nil)
	if err == nil {
		references := []core.NBToolResponseReference{}
		if len(data) > 0 {
			// Add reference to anomaly details page if applicable
			references = append(references, core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"events", "anomaly"}, "Anomaly Details", nil, ""))
		}
		resp.References = references
	}

	return resp, err
}
