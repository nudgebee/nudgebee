package tools

import (
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
	return "Executes a SQL query for anomaly data and returns anomaly detection results. The table name is 'anomaly' (singular, NOT 'anomalies'). Available columns: id, created_at, updated_at, name, namespace, reference_value, current_value, anomaly_type, is_anomaly, evaluated_at, pod_name, config_id."
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
	// Validate that the input is actually SQL, not descriptive text
	if isDescriptiveText(input.Command) {
		return core.NBToolResponse{
			Data:   "Error: Please provide a valid SQL query, not a description. Example: 'SELECT * FROM anomaly WHERE name = 'your-workload-name'' or 'SELECT * FROM anomaly ORDER BY evaluated_at DESC LIMIT 5'",
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	if !isValidSQL(input.Command) {
		return core.NBToolResponse{
			Data:   "Error: Invalid SQL query. Please provide a valid SQL SELECT statement. Example: 'SELECT * FROM anomaly WHERE anomaly_type = 'Latency'' or 'DESC anomaly'",
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

	resp, data, err := sqlToolCall(nbRequestContext, input.Command, "anomaly", anomalyView1, 0, nil)
	if err == nil {
		references := []core.NBToolResponseReference{}
		if len(data) > 0 {
			// Add reference to anomaly details page if applicable
			references = append(references, core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"anomaly", "detection"}, "Anomaly Details", nil, ""))
		}
		resp.References = references
	}

	return resp, err
}
