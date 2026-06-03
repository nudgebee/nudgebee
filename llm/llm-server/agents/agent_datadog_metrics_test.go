package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDatadogMetricsAgent_GetMaxIterations(t *testing.T) {
	agent := DatadogMetricsAgent{}
	assert.Equal(t, 6, agent.GetMaxIterations())
}

func TestDatadogMetricsAgent_CritiqueEnabled(t *testing.T) {
	agent := DatadogMetricsAgent{}
	assert.False(t, agent.CritiqueEnabled())
}

func TestSuggestMetricsSearchTerms(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		contains []string // all must be present
		empty    bool     // expect empty result
	}{
		{name: "CPU query", query: "Show me CPU usage for my-pod", contains: []string{"cpu"}},
		{name: "AWS EC2 query", query: "What is the EC2 CPU utilization?", contains: []string{"ec2", "cpu"}},
		{name: "ALB query", query: "Show me ALB error rate", contains: []string{"applicationelb", "error"}},
		{name: "network query", query: "Show network traffic for host", contains: []string{"network"}},
		{name: "no keywords match", query: "What is the kafka consumer lag?", empty: true},
		{name: "multiple keywords", query: "Show CPU and memory for my pod", contains: []string{"cpu", "memory"}},
		{name: "Lambda + latency", query: "What is the Lambda latency?", contains: []string{"lambda", "latency"}},
		{name: "disk IO", query: "Show disk IO utilization", contains: []string{"disk", "io"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := suggestMetricsSearchTerms(tt.query)
			if tt.empty {
				assert.Empty(t, hint)
				return
			}
			assert.Contains(t, hint, "metrics_list")
			for _, kw := range tt.contains {
				assert.Contains(t, hint, kw)
			}
		})
	}
}

func TestSuggestMetricsSearchTerms_NoFalseSubstringMatch(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		notContains []string
		contains    []string
	}{
		{
			name:        "information should not match io",
			query:       "Get information about the system",
			notContains: []string{"read", "write"},
		},
		{
			name:        "destination should not match nat",
			query:       "Show destination IP metrics",
			notContains: []string{"natgateway"},
		},
		{
			name:        "utilization should not trigger io hint",
			query:       "Show CPU utilization for host",
			notContains: []string{"read", "write"},
		},
		{
			name:        "nodes matches node prefix but not es",
			query:       "Show nodes metrics",
			contains:    []string{"node", "system"},
			notContains: []string{"es,"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := suggestMetricsSearchTerms(tt.query)
			for _, kw := range tt.notContains {
				assert.NotContains(t, hint, kw)
			}
			for _, kw := range tt.contains {
				assert.Contains(t, hint, kw)
			}
		})
	}
}

func TestMatchFastPath(t *testing.T) {
	tests := []struct {
		name          string
		query         string
		wantMetric    string
		wantQueryPart string // substring that must appear in the returned query
		wantEmpty     bool
	}{
		{name: "EC2 CPU", query: "Show me EC2 CPU utilization", wantMetric: "aws.ec2.cpuutilization", wantQueryPart: "avg:aws.ec2.cpuutilization{*} by {host}"},
		{name: "ALB requests", query: "How many requests are hitting my ALB?", wantMetric: "aws.applicationelb.request_count", wantQueryPart: "by {loadbalancer}"},
		{name: "pod memory", query: "Show pod memory usage", wantMetric: "kubernetes.memory.usage", wantQueryPart: "by {pod_name}"},
		{name: "NAT gateway", query: "NAT gateway connections", wantMetric: "aws.natgateway.active_connection_count"},
		{name: "no match", query: "Show me redis throughput", wantEmpty: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, query := matchFastPath(tt.query)
			if tt.wantEmpty {
				assert.Empty(t, query, "should not match unknown patterns")
				return
			}
			assert.NotEmpty(t, query)
			assert.Equal(t, tt.wantMetric, tmpl.Metric)
			if tt.wantQueryPart != "" {
				assert.Contains(t, query, tt.wantQueryPart)
			}
		})
	}
}

func TestMatchFastPath_NoFalseSubstringMatch(t *testing.T) {
	// "instances" should NOT match keyword "in" (ec2+network+in template)
	_, query := matchFastPath("EC2 instances network status")
	// Should match ec2+network (2-keyword template), NOT ec2+network+in
	assert.NotEmpty(t, query, "should match ec2+network template")
	assert.Contains(t, query, "aws.ec2.network_in", "should fall through to ec2+network, not ec2+network+in")

	// "nodes" should NOT cause "es" keyword to match
	_, query = matchFastPath("Show nodes CPU usage")
	assert.NotContains(t, query, "aws.es.cpuutilization", "nodes should not match 'es' keyword")
}

func TestMatchFastPath_PluralKeywordsStillWork(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantMetric string
	}{
		{name: "requests matches request", query: "Show ALB requests count", wantMetric: "aws.applicationelb.request_count"},
		{name: "connections matches connection", query: "NAT connections count", wantMetric: "aws.natgateway.active_connection_count"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, query := matchFastPath(tt.query)
			assert.NotEmpty(t, query)
			assert.Equal(t, tt.wantMetric, tmpl.Metric)
		})
	}
}

func TestDatadogMetricsAgent_UpdateToolResponseForPlanner_MetricsListTruncation(t *testing.T) {
	agent := DatadogMetricsAgent{}

	// Short response should pass through unchanged
	shortResponse := "metric1\nmetric2\nmetric3"
	result := agent.UpdateToolResponseForPlanner(
		core.NBAgentPlannerToolAction{Tool: tools.ToolMetricsList},
		shortResponse,
	)
	assert.Equal(t, shortResponse, result)

	// Long response should be truncated at a newline boundary
	longResponse := strings.Repeat("a.very.long.metric.name.with.labels{label1=value1,label2=value2}\n", 200)
	result = agent.UpdateToolResponseForPlanner(
		core.NBAgentPlannerToolAction{Tool: tools.ToolMetricsList},
		longResponse,
	)
	assert.LessOrEqual(t, len(result), getMaxDatadogMetricsToolResponseChars()+100) // +100 for the truncation message
	assert.Contains(t, result, "truncated")
	// Verify line-aware: the truncation point should be at a complete line.
	// The result format is "<complete lines>\n... (truncated ...)", so the last
	// real content line should end with "}" (end of metric entry), not mid-word.
	truncIdx := strings.Index(result, "\n... (truncated")
	if truncIdx > 0 {
		lastContentChar := result[truncIdx-1]
		assert.Equal(t, byte('}'), lastContentChar, "should truncate at end of a complete metric line, not mid-line")
	}
}

func TestDatadogMetricsAgent_UpdateToolResponseForPlanner_MetricsLabelsListTruncation(t *testing.T) {
	agent := DatadogMetricsAgent{}

	longResponse := strings.Repeat("label_name:label_value_with_lots_of_data\n", 200)
	result := agent.UpdateToolResponseForPlanner(
		core.NBAgentPlannerToolAction{Tool: tools.ToolMetricsLabelsList},
		longResponse,
	)
	assert.LessOrEqual(t, len(result), getMaxDatadogMetricsToolResponseChars()+100)
	assert.Contains(t, result, "truncated")
}

func TestDatadogMetricsAgent_UpdateToolResponseForPlanner_ExecuteResponse(t *testing.T) {
	agent := DatadogMetricsAgent{}

	// Valid JSON response with series data
	response := `{"query":"avg:aws.ec2.cpuutilization{*}","series":[{"display_name":"cpuutilization","scope":"host:i-1234","stats":{"min":10.1,"max":85.5,"avg":42.3,"p99":80.2}}]}`
	result := agent.UpdateToolResponseForPlanner(
		core.NBAgentPlannerToolAction{Tool: tools.ToolExecuteDatadogMetrics},
		response,
	)
	assert.Contains(t, result, "**Query**")
	assert.Contains(t, result, "Display Name: cpuutilization")
	assert.Contains(t, result, "host:i-1234")
}

func TestDatadogMetricsAgent_GetSystemPrompt_PlatformAgnostic(t *testing.T) {
	agent := DatadogMetricsAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	// Prompt should NOT contain hardcoded K8s-only metrics
	prompt := agent.GetSystemPrompt(sc, core.NBAgentRequest{Query: "Show CPU usage"})
	allConstraints := strings.Join(prompt.Constraints, " ")
	assert.NotContains(t, allConstraints, "kubernetes.cpu.usage.total")
	assert.NotContains(t, allConstraints, "container.cpu.limit")

	// Should instruct to use metrics_list for discovery
	assert.Contains(t, allConstraints, "metrics_list")

	// Role should mention multi-platform
	assert.Contains(t, prompt.Role, "AWS")
	assert.Contains(t, prompt.Role, "GCP")
	assert.Contains(t, prompt.Role, "Kubernetes")
}

func TestDatadogMetricsAgent_GetSystemPrompt_SearchHints(t *testing.T) {
	agent := DatadogMetricsAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	// EC2 query should get ec2 search hint in instructions
	prompt := agent.GetSystemPrompt(sc, core.NBAgentRequest{Query: "Show me EC2 CPU utilization"})
	allInstructions := strings.Join(prompt.Instructions, " ")
	assert.Contains(t, allInstructions, "ec2")
	assert.Contains(t, allInstructions, "cpu")

	// Unknown query should NOT have search hints
	prompt = agent.GetSystemPrompt(sc, core.NBAgentRequest{Query: "Show me the kafka lag"})
	allInstructions = strings.Join(prompt.Instructions, " ")
	assert.NotContains(t, allInstructions, "Suggested search keywords")
}

func TestDatadogMetricsAgent_GetSystemPrompt_HasMultiPlatformExamples(t *testing.T) {
	agent := DatadogMetricsAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	prompt := agent.GetSystemPrompt(sc, core.NBAgentRequest{Query: "anything"})

	// Should have both AWS and K8s examples
	hasAWS := false
	hasK8s := false
	for _, ex := range prompt.Examples {
		if strings.Contains(ex.Question, "EC2") || strings.Contains(ex.Question, "Load Balancer") || strings.Contains(ex.Question, "ALB") {
			hasAWS = true
		}
		if strings.Contains(ex.Question, "pod") || strings.Contains(ex.Question, "deployment") || strings.Contains(ex.Question, "namespace") {
			hasK8s = true
		}
	}
	assert.True(t, hasAWS, "should have AWS examples")
	assert.True(t, hasK8s, "should have Kubernetes examples")
}

func TestComputeTrend(t *testing.T) {
	tests := []struct {
		name     string
		points   []any
		contains string // substring that must appear, or empty for exact match
		exact    string // exact expected result (used when contains is empty)
	}{
		{
			name: "rising",
			points: func() []any {
				pts := make([]any, 12)
				for i := 0; i < 12; i++ {
					pts[i] = []any{float64(i * 1000), 10.0 + float64(i)}
				}
				return pts
			}(),
			contains: "Rising",
		},
		{
			name: "falling",
			points: func() []any {
				pts := make([]any, 12)
				for i := 0; i < 12; i++ {
					pts[i] = []any{float64(i * 1000), 20.0 - float64(i)}
				}
				return pts
			}(),
			contains: "Falling",
		},
		{
			name: "stable",
			points: func() []any {
				pts := make([]any, 12)
				for i := 0; i < 12; i++ {
					pts[i] = []any{float64(i * 1000), 50.0 + float64(i%3-1)}
				}
				return pts
			}(),
			exact: "Stable",
		},
		{
			name: "too few points",
			points: []any{
				[]any{1000.0, 10.0},
				[]any{2000.0, 20.0},
			},
			exact: "",
		},
		{
			name: "string values rising",
			points: func() []any {
				pts := make([]any, 12)
				for i := 0; i < 12; i++ {
					pts[i] = []any{float64(i * 1000), fmt.Sprintf("%.1f", 10.0+float64(i))}
				}
				return pts
			}(),
			contains: "Rising",
		},
		{
			name: "mixed float and string values",
			points: func() []any {
				pts := make([]any, 12)
				for i := 0; i < 12; i++ {
					v := 10.0 + float64(i)
					if i%2 == 0 {
						pts[i] = []any{float64(i * 1000), v}
					} else {
						pts[i] = []any{float64(i * 1000), fmt.Sprintf("%.1f", v)}
					}
				}
				return pts
			}(),
			contains: "Rising",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeTrend(tt.points)
			if tt.contains != "" {
				assert.Contains(t, result, tt.contains)
			} else {
				assert.Equal(t, tt.exact, result)
			}
		})
	}
}

func TestIsMultiMetricQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{name: "single metric", query: "Show EC2 CPU utilization", want: false},
		{name: "CPU and memory", query: "Show me CPU and memory for EC2", want: true},
		{name: "CPU and network", query: "EC2 CPU and network traffic", want: true},
		{name: "no and conjunction", query: "Show pod CPU usage", want: false},
		{name: "and without two metric terms", query: "Show CPU and host details", want: false},
		{name: "multi template match", query: "Show EC2 CPU and pod memory", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isMultiMetricQuery(tt.query))
		})
	}
}

func TestGetSystemPrompt_FastPathSkippedForMultiMetric(t *testing.T) {
	agent := DatadogMetricsAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	// Single-metric query should include FAST PATH
	prompt := agent.GetSystemPrompt(sc, core.NBAgentRequest{Query: "Show EC2 CPU utilization"})
	allInstructions := strings.Join(prompt.Instructions, " ")
	assert.Contains(t, allInstructions, "FAST PATH")

	// Multi-metric query should NOT include FAST PATH
	prompt = agent.GetSystemPrompt(sc, core.NBAgentRequest{Query: "Show EC2 CPU and network traffic"})
	allInstructions = strings.Join(prompt.Instructions, " ")
	assert.NotContains(t, allInstructions, "FAST PATH")
}
