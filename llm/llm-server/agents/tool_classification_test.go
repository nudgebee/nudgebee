package agents

import (
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// unclassifiedLeafTools are the leaf tools that do not yet say whether a
// call reads or writes. A governed run's policy may refuse them
// (AgentRunPolicy.CheckTool); every other run treats them as reads. A new leaf tool
// should declare its request type (embed toolcore.ReadOnlyTool when no input
// can make it change anything) rather than join this list. EE-only tools are
// checked in their own package (ee/memory: TestMemoryToolsRequestType).
var unclassifiedLeafTools = []string{
	"ask_clarification", "crawl_execute", "evidenceinsightstool", "mermaid_validation",
	"mongodb_current_op_get", "mongodb_replset_status_get", "mongodb_server_status_get",
	"remediation_generate", "search_execute", "service_dependency_graph_execute",
	"standard_diagnostic_grep", "think", "vm_list_execute",
}

// Every registered leaf tool declares whether a call reads or writes, except
// the ones listed above.
func TestLeafToolsDeclareTheirRequestType(t *testing.T) {
	account := "00000000-0000-0000-0000-0000000000aa"
	var got []string
	for _, name := range toolcore.ListRegisteredSystemToolNames() {
		tool, ok := toolcore.GetNBTool(account, name)
		if !ok || tool == nil || tool.GetType() != toolcore.NBToolTypeTool {
			continue // needs an account's integration to build, or is an agent
		}
		if _, isAgent := core.GetNBAgent(nil, name, account, core.AgentStatusEnabled); isAgent {
			continue // an agent's wrapper: its own tools are each checked
		}
		if strings.HasPrefix(name, "mock_") {
			continue // registered by other tests in this package
		}
		_, inferred := tool.(toolcore.ToolRequestInference)
		_, prompted := tool.(toolcore.ToolRequestInferencePrompt)
		if !inferred && !prompted {
			got = append(got, name)
		}
	}
	sort.Strings(got)
	want := append([]string(nil), unclassifiedLeafTools...)
	sort.Strings(want)
	assert.Equal(t, want, got, "a leaf tool without a request type: classify it, or (only if it truly cannot be classified) list it")
}

// The log, metric and trace query tools only read.
func TestObservabilityQueryToolsAreReads(t *testing.T) {
	for _, tool := range []toolcore.NBTool{
		&tools.NBLogToolV2{},
		tools.DatadogAPMEntitiesExecuteTool{}, tools.DatadogContainersExecuteTool{}, tools.DatadogEventsExecuteTool{},
		tools.DatadogHostsTool{}, tools.DatadogIncidentExecuteTool{}, tools.DatadogLogExecuteTool{},
		tools.DatadogMetricsExecuteTool{}, tools.DatadogResourceSearchTool{}, tools.DatadogServicesExecuteTool{},
		tools.DatadogSoftwareCatalogTool{}, tools.DatadogTracesExecuteTool{},
		tools.NewRelicAPMServicesTool{}, tools.NewRelicInfrastructureContainersTool{},
		tools.NewRelicInfrastructureHostsTool{}, tools.NewRelicResourceSearchTool{},
		tools.ESMetricsQueryTool{}, tools.SplunkMetricsQueryTool{},
	} {
		inference, ok := tool.(toolcore.ToolRequestInference)
		if !assert.True(t, ok, "%s declares its request type", tool.Name()) {
			continue
		}
		requestType, err := inference.InferToolRequestType(nil, tool.Name(), `{"query":"anything"}`)
		assert.NoError(t, err)
		assert.Equal(t, toolcore.ToolRequestTypeRead, requestType, tool.Name())
	}
}
