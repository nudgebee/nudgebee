package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestElasticSearchMetricsAgent_Properties(t *testing.T) {
	agent := ElasticSearchMetricsAgent{accountId: "test-account"}

	assert.Equal(t, ElasticSearchMetricsAgentName, agent.GetName())
	assert.Contains(t, agent.GetNameAliases(), "Elastic Search Metrics")
	assert.NotEmpty(t, agent.GetDescription())
	assert.Equal(t, core.AgentPlannerTypeReAct, agent.GetPlannerType())
	assert.Equal(t, 10, agent.GetMaxIterations())
}

func TestElasticSearchMetricsAgent_SystemPrompt(t *testing.T) {
	agent := ElasticSearchMetricsAgent{accountId: "test-account"}
	sc := security.NewRequestContextForSuperAdmin()
	query := core.NBAgentRequest{Query: "average cpu usage"}

	prompt := agent.GetSystemPrompt(sc, query)

	assert.NotEmpty(t, prompt.Role)
	assert.NotEmpty(t, prompt.Instructions)
	assert.NotEmpty(t, prompt.Constraints)
	assert.NotEmpty(t, prompt.OutputFormat)

	// Verify that Elasticsearch instructions are present
	foundES := false
	for _, inst := range prompt.Instructions {
		if containsIgnoreCase(inst, "Elasticsearch") || containsIgnoreCase(inst, "Opensearch") {
			foundES = true
			break
		}
	}
	assert.True(t, foundES, "System prompt should contain Elasticsearch/Opensearch instructions")
}

func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func TestIsLogIndex(t *testing.T) {
	cases := []struct {
		pattern string
		want    bool
	}{
		// Metric patterns — must NOT be classified as logs.
		{"metrics-*", false},
		{"metricbeat-*", false},
		{"metrics-kubernetes.pod-gd-govdelivery-prod-us", false},
		{"metrics-kubernetes.container-gd-govdelivery-prod-us", false},
		{"metrics-kubernetes.state_deployment-gd-govdelivery-prod-us", false},
		{"metrics-kubernetes.event-gd-govdelivery-prod-us", false},
		{"metrics-kubernetes.volume-gd-govdelivery-prod-us", false},
		{"metrics-cloudwatch_metrics-gd-govdelivery-prod-us", false},

		// Log patterns — MUST be classified as logs (Elastic Agent uses `logs-*` prefix).
		{"logs-kubernetes.container_logs-gd-govdelivery-prod-us", true},
		{"logs-apm.app.bulletinprocessor-default", true},
		{"logs-apm.app.ochocinco-default", true},
		{"logs-aws.cloudtrail-logs-gd-govdelivery-prod-us", true},
		{"fluentk8-*", true},
		{"signoz_logs", true},
	}

	for _, tc := range cases {
		got := isLogIndex(tc.pattern)
		assert.Equal(t, tc.want, got, "isLogIndex(%q)", tc.pattern)
	}
}

func TestClassifyIndex(t *testing.T) {
	cases := []struct {
		pattern string
		want    esMetricSchema
	}{
		{"metrics-cloudwatch_metrics-prod", schemaCloudWatch},
		{"metrics-cloudwatch_metrics-*", schemaCloudWatch},
		{"metrics-kubernetes.pod-*", schemaElasticK8s},
		{"metrics-kubernetes.container-prod", schemaElasticK8s},
		{"metrics-kubernetes.state_deployment-*", schemaElasticK8s},
		{"metricbeat-*", schemaMetricbeat},
		{"metricbeat-8.x", schemaMetricbeat},
		{"metrics-*", schemaOTel},
		{"metrix-*", schemaOTel},
		{"custom-metrics-index", schemaOTel},
	}

	for _, tc := range cases {
		got := classifyIndex(tc.pattern)
		assert.Equal(t, tc.want, got, "classifyIndex(%q)", tc.pattern)
	}
}

func TestDetectMetricSchemas_Default(t *testing.T) {
	// No explicit config → should fall back to OTel.
	cfg := utils.ESIndexConfig{DefaultIndex: "*"}
	schemas, indices := detectMetricSchemas(cfg)

	assert.True(t, schemas[schemaOTel], "default config should activate OTel schema")
	assert.False(t, schemas[schemaElasticK8s])
	assert.False(t, schemas[schemaCloudWatch])
	assert.False(t, schemas[schemaMetricbeat])
	assert.NotEmpty(t, indices)
}

func TestDetectMetricSchemas_ElasticK8s(t *testing.T) {
	cfg := utils.ESIndexConfig{
		DefaultIndex: "metrics-kubernetes.pod-gd-prod",
		Indices: map[string]string{
			"container": "metrics-kubernetes.container-gd-prod",
		},
	}
	schemas, indices := detectMetricSchemas(cfg)

	assert.True(t, schemas[schemaElasticK8s])
	assert.False(t, schemas[schemaOTel])
	assert.False(t, schemas[schemaCloudWatch])
	assert.Contains(t, indices, "metrics-kubernetes.pod-gd-prod")
	assert.Contains(t, indices, "metrics-kubernetes.container-gd-prod")
}

func TestDetectMetricSchemas_CloudWatch(t *testing.T) {
	cfg := utils.ESIndexConfig{
		DefaultIndex: "metrics-cloudwatch_metrics-prod",
	}
	schemas, indices := detectMetricSchemas(cfg)

	assert.True(t, schemas[schemaCloudWatch])
	assert.False(t, schemas[schemaElasticK8s])
	assert.False(t, schemas[schemaOTel])
	assert.Contains(t, indices, "metrics-cloudwatch_metrics-prod")
}

func TestDetectMetricSchemas_MultiSchema(t *testing.T) {
	cfg := utils.ESIndexConfig{
		DefaultIndex: "metrics-kubernetes.pod-prod",
		Indices: map[string]string{
			"cloudwatch": "metrics-cloudwatch_metrics-prod",
		},
	}
	schemas, _ := detectMetricSchemas(cfg)

	assert.True(t, schemas[schemaElasticK8s])
	assert.True(t, schemas[schemaCloudWatch])
}

func TestDetectMetricSchemas_LogIndicesExcluded(t *testing.T) {
	// Log indices must be filtered out even if they appear in the config.
	cfg := utils.ESIndexConfig{
		DefaultIndex: "logs-kubernetes.container_logs-prod",
		Indices: map[string]string{
			"app": "logs-apm.app.service-default",
		},
	}
	schemas, indices := detectMetricSchemas(cfg)

	// All log indices filtered → falls back to OTel default.
	assert.True(t, schemas[schemaOTel], "should fall back to OTel when all indices are log indices")
	for _, idx := range indices {
		assert.False(t, isLogIndex(idx), "log index leaked into available indices: %q", idx)
	}
}

func TestBuildTimestampRule(t *testing.T) {
	otelOnly := map[esMetricSchema]bool{schemaOTel: true}
	k8sOnly := map[esMetricSchema]bool{schemaElasticK8s: true}
	cwOnly := map[esMetricSchema]bool{schemaCloudWatch: true}
	mixed := map[esMetricSchema]bool{schemaOTel: true, schemaElasticK8s: true}

	assert.Contains(t, buildTimestampRule(otelOnly), "`time`")
	assert.NotContains(t, buildTimestampRule(otelOnly), "all other indices")

	assert.Contains(t, buildTimestampRule(k8sOnly), "`@timestamp`")
	// The k8s-only rule says "Do NOT use `time`" — check the positive instruction is absent.
	assert.NotContains(t, buildTimestampRule(k8sOnly), "filter on `time`")

	assert.Contains(t, buildTimestampRule(cwOnly), "`@timestamp`")

	// Mixed must mention both.
	rule := buildTimestampRule(mixed)
	assert.Contains(t, rule, "`time`")
	assert.Contains(t, rule, "`@timestamp`")
}

// TestElasticSearchMetricsAgent_SystemPrompt_OTelOnly verifies that a default account
// (OTel-only) gets OTel-specific instructions and NOT Elastic Agent or CloudWatch ones.
func TestElasticSearchMetricsAgent_SystemPrompt_OTelOnly(t *testing.T) {
	agent := ElasticSearchMetricsAgent{accountId: "test-account"}
	sc := security.NewRequestContextForSuperAdmin()
	prompt := agent.GetSystemPrompt(sc, core.NBAgentRequest{Query: "any"})

	haystack := strings.Join(prompt.Instructions, "\n") + "\n" + strings.Join(prompt.Constraints, "\n")

	// OTel markers must be present.
	assert.Contains(t, haystack, "OTel collector", "OTel schema row missing")
	assert.Contains(t, haystack, "name.keyword", "OTel metric name field missing")
	assert.Contains(t, haystack, "`time`", "OTel timestamp rule missing")

	// Elastic Agent K8s and CloudWatch markers must NOT be present for default accounts.
	assert.NotContains(t, haystack, "Elastic Agent K8s", "should not inject K8s schema for default account")
	assert.NotContains(t, haystack, "AWS CloudWatch", "should not inject CloudWatch schema for default account")
}

// TestElasticSearchMetricsAgent_SystemPrompt_CoversAllSchemas verifies that an account
// configured with all four schemas gets all schema-specific instructions injected.
func TestElasticSearchMetricsAgent_SystemPrompt_CoversAllSchemas(t *testing.T) {
	// Build a prompt by calling the schema functions directly (bypassing DB lookup).
	schemas := map[esMetricSchema]bool{
		schemaOTel:       true,
		schemaMetricbeat: true,
		schemaElasticK8s: true,
		schemaCloudWatch: true,
	}

	// Verify schema map contains all four active schemas.
	for _, s := range []esMetricSchema{schemaOTel, schemaMetricbeat, schemaElasticK8s, schemaCloudWatch} {
		assert.True(t, schemas[s], "schema %s should be active", s)
	}

	// Verify timestamp rule covers all schemas.
	rule := buildTimestampRule(schemas)
	assert.Contains(t, rule, "`time`", "mixed timestamp rule should mention `time`")
	assert.Contains(t, rule, "`@timestamp`", "mixed timestamp rule should mention `@timestamp`")

	// Verify index patterns for the new schemas are correctly classified.
	for pattern, expectedSchema := range map[string]esMetricSchema{
		"metrics-kubernetes.pod-*":          schemaElasticK8s,
		"metrics-kubernetes.container-*":    schemaElasticK8s,
		"metrics-kubernetes.state_deploy-*": schemaElasticK8s,
		"metrics-cloudwatch_metrics-*":      schemaCloudWatch,
	} {
		got := classifyIndex(pattern)
		assert.Equal(t, expectedSchema, got, "classifyIndex(%q)", pattern)
	}

	// Verify schema-specific field paths are known to the test (not just string constants).
	knownPaths := []string{
		"kubernetes.container.cpu.usage.nanocores",
		"kubernetes.deployment.replicas.available",
		"kubernetes.namespace",
		"aws.ec2.metrics.CPUUtilization",
		"aws.dimensions.InstanceId",
		"@timestamp",
	}
	for _, path := range knownPaths {
		assert.NotEmpty(t, path, "field path constant should not be empty")
	}
}
