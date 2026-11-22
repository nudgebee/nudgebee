package agents

import (
	"testing"

	"nudgebee/llm/tools"

	"github.com/stretchr/testify/assert"
)

// Alert investigations run on these orchestrators, and the investigation prompt
// asks them to compute "getting worse" with analyze_metric_series, so every one
// of them must mount it.
func TestInvestigationOrchestratorsMountAnalyzeMetricSeries(t *testing.T) {
	assert.Contains(t, trimmedK8sCoreToolNames(), tools.ToolAnalyzeMetricSeries)
	for _, cliTool := range []string{
		tools.ToolExecuteAwsCliCommand,
		tools.ToolExecuteGcpCliCommand,
		tools.ToolExecuteAzureCliCommand,
	} {
		assert.Contains(t, cloudLeanCoreToolNames(cliTool), tools.ToolAnalyzeMetricSeries, cliTool)
	}
}
