package tools

import (
	"errors"
	"strings"
	"testing"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsCloudMetricRead_Azure(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{"az monitor metrics list --resource /subscriptions/s/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm --metric 'Percentage CPU' --interval PT5M", true},
		{"az monitor metrics list --resource r --metric \"Available Memory Bytes\" --start-time 2026-10-13T00:00:00Z --end-time 2026-10-20T00:00:00Z -o json", true},
		{"az --subscription s1 monitor metrics list --resource r", true},
		{"az monitor metrics list-definitions --resource r", false},
		{"az monitor metrics alert list", false},
		{"az monitor activity-log list --offset 1d", false},
		{"az vm list", false},
		{"az monitor metrics list --resource r | jq .", false},
		{"aws cloudwatch get-metric-data --metric-data-queries file://q.json", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, isCloudMetricRead(azureMetricRead, tt.command), tt.command)
	}
}

const azureMetricJSON = `{"cost":0,"interval":"PT5M","value":[{"name":{"value":"Percentage CPU"},"timeseries":[{"data":[{"average":12.5,"timeStamp":"2026-10-20T00:00:00Z"}]}],"unit":"Percent"}]}`

func azureMetricCall(t *testing.T, ws *metricSaveWorkspace, command string) core.NBToolResponse {
	t.Helper()
	tool := AzureCliTool{workspaceManager: ws}
	resp, err := tool.Call(cloudCliToolContext("workspace", "azure-id", "azure-prod", tool), core.NBToolCallRequest{Command: command})
	require.NoError(t, err)
	require.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	return resp
}

func TestAzureCall_MetricReadSavedWithRefAndNote(t *testing.T) {
	ws := &metricSaveWorkspace{stdout: azureMetricJSON}
	resp := azureMetricCall(t, ws, "az monitor metrics list --resource r --metric 'Percentage CPU'")

	require.Equal(t, 1, ws.saves)
	assert.True(t, strings.HasPrefix(ws.savedPath, "metrics_azure_monitor_"), ws.savedPath)
	assert.Equal(t, azureMetricJSON, ws.body)
	require.Len(t, resp.References, 1)
	assert.Equal(t, core.NBToolResponseReference{Type: "file", Url: ws.savedPath, Text: ws.savedPath, Description: azureMetricRead.description}, resp.References[0])
	require.True(t, strings.HasPrefix(resp.Data, "[account: azure-prod]\n"+azureMetricJSON+"\n"), resp.Data)
	assert.Contains(t, resp.Data, `"`+ws.savedPath+`"`)
	assert.Contains(t, resp.Data, "analyze_metric_series")
}

func TestAzureCall_MetricSaveFailureKeepsNormalOutput(t *testing.T) {
	ws := &metricSaveWorkspace{stdout: azureMetricJSON, saveErr: errors.New("workspace unavailable")}
	resp := azureMetricCall(t, ws, "az monitor metrics list --resource r")
	assert.Equal(t, 1, ws.saves)
	assert.Empty(t, resp.References)
	assert.Equal(t, "[account: azure-prod]\n"+azureMetricJSON, resp.Data)
}

func TestAzureCall_NonMetricNotSaved(t *testing.T) {
	ws := &metricSaveWorkspace{stdout: `[{"name":"vm"}]`}
	resp := azureMetricCall(t, ws, "az vm list")
	assert.Equal(t, 0, ws.saves)
	assert.Empty(t, resp.References)
	assert.Equal(t, "[account: azure-prod]\n"+`[{"name":"vm"}]`, resp.Data)
}
