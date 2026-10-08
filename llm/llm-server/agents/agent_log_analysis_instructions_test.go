package agents

import (
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"strings"
	"testing"
)

func TestLogAnalysisInstructions_ExecutionAndEvidenceContract(t *testing.T) {
	instructions := strings.Join(logAnalysisExecutionInstructions(), "\n")
	for _, s := range []string{"one Python script", "one shell pipeline", "split on the first two TABs", "earliest/latest", "SAME execution", "malformed/skipped", "complete inline routine listing", "empty-result recovery"} {
		require.Contains(t, instructions, s)
	}
	shared := strings.Join(sharedHeaderAndWorkflow(), "\n")
	require.NotContains(t, shared, "answer from the inline logs, and do NOT call shell_execute")
	enumeration := strings.Join(enumerationInstructions(), "\n")
	require.NotContains(t, enumeration, "does NOT need a JSON-aware extractor")
	require.Contains(t, enumeration, "before applying a top-N display limit")
	for _, mode := range []logModeV3{logModeRoutineV3, logModeEnumerationV3, logModeInvestigationV3} {
		output := strings.Join(outputFormatInstructions(mode), "\n")
		require.Contains(t, output, "Show each log excerpt once")
		require.Contains(t, output, "never present rewritten JSON as the original raw record")
	}
}

func TestLogAnalysisInstructions_SubAgentFindings(t *testing.T) {
	agent := newLogAgent("", services_server.ObservabilityProvider{Provider: "loki"})
	for _, tc := range []struct {
		name, parent string
		delegated    bool
	}{
		{"direct", "", false}, {"top level self", "logs-agent", false},
		{"nil parent", uuid.Nil.String(), false}, {"delegated", "orchestrator", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prompt := agent.GetSystemPrompt(security.NewRequestContextForSuperAdmin(), core.NBAgentRequest{Query: "why is checkout failing", AgentId: "logs-agent", ParentAgentId: tc.parent})
			body := strings.Join(prompt.Instructions, "\n")
			require.Equal(t, tc.delegated, strings.Contains(body, "**Sub-agent response:**"))
			if tc.delegated {
				require.Contains(t, body, "artifact references")
				require.Contains(t, body, "limitations or unresolved questions")
				require.Contains(t, body, "Preserve requested detail and causal reasoning")
			}
		})
	}
}

func TestLogAnalysisInstructions_ModeAndCalculationBoundaries(t *testing.T) {
	shared := strings.Join(logAnalysisExecutionInstructions(), "\n")
	require.Contains(t, shared, "First reuse any requested result already fully supplied")
	require.Contains(t, shared, "Only INVESTIGATION mode requires")
	investigation := strings.Join(investigationInstructions(), "\n")
	require.NotContains(t, investigation, "timestamp on the first matching line")
	require.NotContains(t, investigation, "FIRST and LAST matching error line")
	require.Contains(t, investigation, "earliest observed match as T")
	enumeration := strings.Join(enumerationInstructions(), "\n")
	require.Contains(t, enumeration, `> "grouped-errors.txt"`)
	require.Contains(t, enumeration, "occurrences += $1")
	require.Less(t, strings.Index(enumeration, "occurrences += $1"), strings.Index(enumeration, `head -20 "grouped-errors.txt"`))
}

func TestLogAnalysisInstructions_BundleIsSampledEvidence(t *testing.T) {
	old := config.Config.LogsStandardGrepEnabled
	config.Config.LogsStandardGrepEnabled = true
	t.Cleanup(func() { config.Config.LogsStandardGrepEnabled = old })
	body := strings.Join(investigationInstructions(), "\n")
	require.Contains(t, body, "at most 20 example lines per diagnostic pattern")
	require.NotContains(t, body, "go straight to the WHEN report")
	require.NotContains(t, body, "Mandatory diagnostic sweep (NOT optional")
	require.Contains(t, body, "only AFTER computing counts and time bounds")
	require.Contains(t, body, "A sampled bundle alone does not satisfy")
	require.Contains(t, body, "do not invent bounds")
}
