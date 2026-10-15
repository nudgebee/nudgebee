package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/integrations"
)

func TestAzureTraceTableDefinition_WorkspaceNames(t *testing.T) {
	legacy := azureTraceTableDefinition(integrations.AzureAppInsightsConfig{AzureAppInsightsAppID: "app"})
	assert.Equal(t, "operation_Id", legacy["trace_id"].Def)

	ws := azureTraceTableDefinition(integrations.AzureAppInsightsConfig{LogsWorkbookID: "ws"})
	assert.Equal(t, "OperationId", ws["trace_id"].Def)
	assert.Equal(t, "TimeGenerated", ws["timestamp"].Def)
	assert.Equal(t, "ParentId", ws["parent_span_id"].Def)
	assert.Equal(t, "DurationMs * 1000000", ws["duration_ns"].Def)
	assert.Equal(t, `column_ifexists("Data", column_ifexists("Url", ""))`, ws["resource"].Def)
	assert.Equal(t, `tostring(Properties["service.name"])`, ws["service_name"].Def)
	assert.Equal(t, "ResultCode", ws["http_status_code"].Def)
	assert.Len(t, ws, len(AzureTraceTableDefinition))
	for label, def := range ws {
		assert.NotContains(t, def.Def, "customDimensions", label)
	}
}

func TestConvertAzureTrace_WorkspaceColumnsMatchLegacy(t *testing.T) {
	s := &AzureAppInsightsTraceSource{}
	legacyCols := []Column{{Name: "operation_Id"}, {Name: "id"}, {Name: "operation_ParentId"}, {Name: "name"}, {Name: "resultCode"}, {Name: "duration"}, {Name: "timestamp"}, {Name: "customDimensions"}}
	wsCols := []Column{{Name: "OperationId"}, {Name: "Id"}, {Name: "ParentId"}, {Name: "Name"}, {Name: "ResultCode"}, {Name: "DurationMs"}, {Name: "TimeGenerated"}, {Name: "Properties"}}
	row := []any{"trace1", "span1", "parent1", "POST /payments", "200", 42.0, "2026-10-14T18:00:00.123Z", `{"service.name":"orders-api","destination.workload_name":"payments-api"}`}

	toMap := func(cols []Column) map[string]any {
		m := map[string]any{}
		for i, c := range cols {
			m[c.Name] = row[i]
		}
		return m
	}
	norm := normalizeAzureTraceColumns(wsCols)
	got := s.convertAzureTraceToOpenteleMetry(toMap(norm), norm)
	want := s.convertAzureTraceToOpenteleMetry(toMap(legacyCols), legacyCols)
	require.Equal(t, want, got)
	assert.Equal(t, "trace1", got.TraceID)
	assert.Equal(t, "orders-api", got.ServiceName)
}

func TestBuildAzureMetricKql_Workspace(t *testing.T) {
	kql, err := buildAzureMetricKql(azureWorkspaceMetricSchema, "checkout_latency_ms", 1700000000000, 1700003600000, 5, map[string]string{"service.name": "orders-api"}, nil)
	require.NoError(t, err)
	assert.Equal(t, "AppMetrics\n"+
		"| where TimeGenerated between (datetime('2023-11-14T22:13:20Z') .. datetime('2023-11-14T23:13:20Z'))\n"+
		"| where Name == 'checkout_latency_ms'\n"+
		"| where tostring(Properties['service.name']) == 'orders-api'\n"+
		"| summarize value = sum(Sum) / sum(ItemCount) by timestamp = bin(TimeGenerated, 5m)\n"+
		"| order by timestamp asc", kql)
}

func TestAzureTraceLabelExpr(t *testing.T) {
	legacy := integrations.AzureAppInsightsConfig{AzureAppInsightsAppID: "app"}
	ws := integrations.AzureAppInsightsConfig{LogsWorkbookID: "ws"}
	assert.Equal(t, "name", azureTraceLabelExpr(legacy, "span_name"))
	assert.Equal(t, "Name", azureTraceLabelExpr(ws, "span_name"))
	assert.Equal(t, "Name", azureTraceLabelExpr(ws, "name"))
	assert.Equal(t, `tostring(customDimensions["k8s.pod"])`, azureTraceLabelExpr(legacy, `tostring(customDimensions["k8s.pod"])`))
	assert.Equal(t, `tostring(Properties["k8s.pod"])`, azureTraceLabelExpr(ws, `tostring(customDimensions["k8s.pod"])`))
	assert.Equal(t, "Target", azureTraceLabelExpr(ws, "Target"))
}

func TestConvertAzureTrace_FillsTableColumns(t *testing.T) {
	s := &AzureAppInsightsTraceSource{}
	cols := normalizeAzureTraceColumns([]Column{{Name: "OperationId"}, {Name: "Id"}, {Name: "Name"}, {Name: "ResultCode"}, {Name: "DurationMs"}, {Name: "Target"}, {Name: "Data"}, {Name: "AppRoleName"}, {Name: "Properties"}})
	row := []any{"trace1", "span1", "POST /payments", "200", 41.0, "payments-api", "http://payments-api/pay", "orders-role",
		`{"service.name":"orders-api","source.workload_name":"orders","source.workload_namespace":"poc-orders","destination.workload_name":"payments","destination.workload_namespace":"poc-payments"}`}
	m := map[string]any{}
	for i, c := range cols {
		m[c.Name] = row[i]
	}
	got := s.convertAzureTraceToOpenteleMetry(m, cols)
	assert.Equal(t, int64(41_000_000), got.DurationNs, "milliseconds converted to nanoseconds")
	assert.Equal(t, "200", got.HTTPStatusCode)
	assert.Equal(t, "orders", got.WorkloadName)
	assert.Equal(t, "poc-orders", got.WorkloadNamespace)
	assert.Equal(t, "payments", got.DestinationWorkload)
	assert.Equal(t, "poc-payments", got.DestinationNamespace)
	assert.Equal(t, "payments", got.DestinationName)
	assert.Equal(t, "http://payments-api/pay", got.Resource)

	// Without Kubernetes attributes: service name, then the dependency target.
	bare := map[string]any{"name": "GET /x", "target": "db-host", "cloud_RoleName": "role-a", "duration": 2.5}
	bareCols := []Column{{Name: "name"}, {Name: "target"}, {Name: "cloud_RoleName"}, {Name: "duration"}}
	got = s.convertAzureTraceToOpenteleMetry(bare, bareCols)
	assert.Equal(t, "role-a", got.WorkloadName)
	assert.Equal(t, "db-host", got.DestinationName)
	assert.Equal(t, int64(2_500_000), got.DurationNs)
}
