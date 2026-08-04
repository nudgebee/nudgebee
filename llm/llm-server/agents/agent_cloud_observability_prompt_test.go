package agents

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

// flattenAgentPrompt renders every text-bearing field of an NBAgentPrompt into a
// single searchable string so prompt-content assertions can pin load-bearing
// guidance regardless of which section (instruction / constraint / tool-usage /
// example) it lives in.
func flattenAgentPrompt(p core.NBAgentPrompt) string {
	var b strings.Builder
	b.WriteString(p.Role + "\n")
	b.WriteString(p.OutputFormat + "\n")
	for _, s := range p.Instructions {
		b.WriteString(s + "\n")
	}
	for _, s := range p.Constraints {
		b.WriteString(s + "\n")
	}
	for _, vals := range p.ToolUsage {
		for _, s := range vals {
			b.WriteString(s + "\n")
		}
	}
	for _, ex := range p.Examples {
		b.WriteString(ex.Question + "\n" + ex.Answer + "\n" + ex.Explanation + "\n")
		for _, st := range ex.AnswerSteps {
			b.WriteString(st.Tool + " " + st.Input + " " + st.Explanation + "\n")
		}
	}
	return b.String()
}

// TestCloudObservabilityAgentPrompts pins the two things every cloud CLI
// observability agent's prompt MUST carry: (1) the correct primary read command
// for its signal, and (2) the query-bounding + resource-discovery guidance that
// keeps large-data reads safe (the log-group/workspace/descriptor discovery and
// the --limit/take/pageSize bounds). A future edit that drops any of these fails
// here rather than silently regressing behaviour in production. Also enforces the
// repo-wide "no literal TODO in prompts" rule for these inline Go prompts (the
// prompts_repo TODO test does not scan Go-defined prompts).
func TestCloudObservabilityAgentPrompts(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	req := core.NBAgentRequest{}

	cases := []struct {
		name        string
		agent       core.NBAgent
		mustContain []string
	}{
		{
			name:  "gcp_metrics",
			agent: GcpMetricsAgent{accountId: "acct-1"},
			mustContain: []string{
				"gcloud monitoring time-series list",   // primary read
				"gcloud monitoring metric-descriptors", // discovery
				"monitoring.googleapis.com/v3",         // v3 REST path for complex reads
				"--limit",                              // bounding
				"workspace file",                       // large-output offload
			},
		},
		{
			name:  "azure_metrics",
			agent: AzureMetricsAgent{accountId: "acct-1"},
			mustContain: []string{
				"az monitor metrics list",             // primary read
				"az monitor metrics list-definitions", // discovery
				"--interval",                          // bounding
				"--aggregation",
				"workspace file", // large-output offload
			},
		},
		{
			name:  "gcp_logs",
			agent: GcpLogsAgent{accountId: "acct-1"},
			mustContain: []string{
				"gcloud logging read",      // primary read
				"gcloud logging logs list", // discovery
				"resource.type",            // the log-group analog: scope by resource
				"pod_name=~",               // workload->pod resolution (resource_search analog)
				"--freshness",              // bounding (window)
				"--limit",                  // bounding (rows)
				"workspace file",           // large-output offload
			},
		},
		{
			name:  "azure_logs",
			agent: AzureLogsAgent{accountId: "acct-1"},
			mustContain: []string{
				"az monitor log-analytics query",          // primary read
				"az monitor log-analytics workspace list", // discovery (the workspace analog of a log group)
				"startswith",     // workload->pod resolution (resource_search analog)
				"ago(",           // bounding (KQL window)
				"take ",          // bounding (KQL rows)
				"workspace file", // large-output offload
			},
		},
		{
			name:  "gcp_traces",
			agent: GcpTracesAgent{accountId: "acct-1"},
			mustContain: []string{
				"cloudtrace.googleapis.com",      // Cloud Trace v1 REST
				"gcloud auth print-access-token", // token path via shell_execute
				"pageSize",                       // bounding
				"gcloud logging read",            // log-correlation fallback
				"workspace file",                 // large-output offload
			},
		},
		{
			name:  "azure_traces",
			agent: AzureTracesAgent{accountId: "acct-1"},
			mustContain: []string{
				"az monitor app-insights component list", // discovery
				"az monitor app-insights query",          // primary read
				"ago(",                                   // bounding (KQL window)
				"take ",                                  // bounding (KQL rows)
				"workspace file",                         // large-output offload
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			flat := flattenAgentPrompt(c.agent.GetSystemPrompt(ctx, req))
			assert.NotContains(t, flat, "TODO", "inline prompt must not contain a literal TODO marker")
			assert.Greater(t, len(flat), 500, "prompt looks empty/too short")
			for _, want := range c.mustContain {
				assert.Contains(t, flat, want, "%s prompt missing required guidance %q", c.name, want)
			}
		})
	}
}
