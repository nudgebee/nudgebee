package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/utils"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		// The stock patterns stay OTel — they are our own collector's convention.
		{"metrics-*", schemaOTel},
		{"metrix-*", schemaOTel},
		// Any OTHER unmatched name is UNKNOWN, not OTel. Asserting OTel here is what
		// taught Elastic Agent accounts (`metrics-*-prod`) the wrong fields.
		{"custom-metrics-index", ""},
		{"metrics-*-prod", ""},
		{"telemetry-prod-2026", ""},
	}

	for _, tc := range cases {
		got, ok := classifyIndex(tc.pattern)
		assert.Equal(t, tc.want != "", ok, "classifyIndex(%q) ok", tc.pattern)
		assert.Equal(t, tc.want, got, "classifyIndex(%q)", tc.pattern)
	}
}

func TestDetectMetricSchemas_Default(t *testing.T) {
	// No explicit config → should fall back to OTel.
	cfg := utils.ESIndexConfig{DefaultIndex: "*"}
	schemas, indices, _, _ := detectMetricSchemas(cfg, nil)

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
	schemas, indices, _, _ := detectMetricSchemas(cfg, nil)

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
	schemas, indices, _, _ := detectMetricSchemas(cfg, nil)

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
	schemas, _, _, _ := detectMetricSchemas(cfg, nil)

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
	schemas, indices, _, _ := detectMetricSchemas(cfg, nil)

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

	assert.Contains(t, buildTimestampRule(otelOnly, ""), "`time`")
	assert.NotContains(t, buildTimestampRule(otelOnly, ""), "all other indices")

	assert.Contains(t, buildTimestampRule(k8sOnly, ""), "`@timestamp`")
	// The k8s-only rule says "Do NOT use `time`" — check the positive instruction is absent.
	assert.NotContains(t, buildTimestampRule(k8sOnly, ""), "filter on `time`")

	assert.Contains(t, buildTimestampRule(cwOnly, ""), "`@timestamp`")

	// Mixed must mention both.
	rule := buildTimestampRule(mixed, "")
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
	rule := buildTimestampRule(schemas, "")
	assert.Contains(t, rule, "`time`", "mixed timestamp rule should mention `time`")
	assert.Contains(t, rule, "`@timestamp`", "mixed timestamp rule should mention `@timestamp`")

	// Verify index patterns for the new schemas are correctly classified.
	for pattern, expectedSchema := range map[string]esMetricSchema{
		"metrics-kubernetes.pod-*":          schemaElasticK8s,
		"metrics-kubernetes.container-*":    schemaElasticK8s,
		"metrics-kubernetes.state_deploy-*": schemaElasticK8s,
		"metrics-cloudwatch_metrics-*":      schemaCloudWatch,
	} {
		got, ok := classifyIndex(pattern)
		assert.True(t, ok, "classifyIndex(%q) should be conclusive", pattern)
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

// TestDetectMetricSchemas_ConfiguredIndexIsNotDilutedByWildcards pins the #36233
// regression: a configured index that classifies as OTel used to have `metrics-*`
// and `metrix-*` appended beside it, and the model then preferred the wildcard.
// On a shared ES endpoint that wildcard also spans other environments' data streams.
func TestDetectMetricSchemas_ConfiguredIndexIsNotDilutedByWildcards(t *testing.T) {
	cfg := utils.ESIndexConfig{DefaultIndex: "metrics-*-prod"}
	_, indices, _, _ := detectMetricSchemas(cfg, nil)

	assert.Equal(t, []string{"metrics-*-prod"}, indices,
		"a configured index must be the only offered target")
	assert.NotContains(t, indices, "metrics-*", "stock wildcard must not be offered beside a configured index")
	assert.NotContains(t, indices, "metrix-*", "stock wildcard must not be offered beside a configured index")
}

// TestDetectMetricSchemas_StockWildcardsOnlyWhenUnconfigured keeps the fallback for
// accounts that configured nothing — the wildcards are the only guess available there.
func TestDetectMetricSchemas_StockWildcardsOnlyWhenUnconfigured(t *testing.T) {
	_, indices, _, _ := detectMetricSchemas(utils.ESIndexConfig{DefaultIndex: ""}, nil)
	assert.ElementsMatch(t, []string{"metrics-*", "metrix-*"}, indices)
}

// TestDetectSchemasFromFields covers mapping-derived detection, including the two
// real-account shapes where the index NAME disagrees with the document body.
func TestDetectSchemasFromFields(t *testing.T) {
	tests := []struct {
		name     string
		fields   []string
		expected []esMetricSchema
		wantTS   string
	}{
		{
			// dev cluster `metricbeat-otelshape-*`: OTel body, ECS timestamp.
			// classifyIndex calls this metricbeat purely because of the index name.
			name: "otel body under an ECS timestamp",
			fields: []string{
				"@timestamp", "name", "name.keyword", "value",
				"attributes.resource.attributes.k8s@namespace@name",
				"attributes.metric.attributes.pod", "agent.id", "ecs.version",
			},
			expected: []esMetricSchema{schemaOTel},
			wantTS:   esTimestampECS,
		},
		{
			name: "true OTel collector index",
			fields: []string{
				"time", "name", "value", "attributes.resource.attributes.k8s@pod@name",
			},
			expected: []esMetricSchema{schemaOTel},
			wantTS:   esTimestampOTel,
		},
		{
			// the customer's `metrics-*-prod`: Elastic Agent body, name implies nothing.
			name: "elastic agent k8s body",
			fields: []string{
				"@timestamp", "kubernetes.pod.name", "kubernetes.container.memory.usage.bytes",
				"metricset.name", "data_stream.dataset", "agent.version",
			},
			expected: []esMetricSchema{schemaElasticK8s},
			wantTS:   esTimestampECS,
		},
		{
			name:     "plain metricbeat without kubernetes fields",
			fields:   []string{"@timestamp", "metricset.name", "event.dataset", "host.name"},
			expected: []esMetricSchema{schemaMetricbeat},
			wantTS:   esTimestampECS,
		},
		{
			name:     "cloudwatch",
			fields:   []string{"@timestamp", "aws.cloudwatch.metric_name", "aws.ec2.metrics.CPUUtilization.avg", "aws.dimensions.InstanceId"},
			expected: []esMetricSchema{schemaCloudWatch},
			wantTS:   esTimestampECS,
		},
		{
			// Metricbeat's index template maps ~170 `aws.<svc>.metrics.*` fields on every
			// index it creates, whether or not any AWS data was written. Dev's
			// `metricbeat-8.19.11` carries all of them plus the dynamic-template
			// placeholder `aws.*.metrics.*.*`, and no CloudWatch data. Claiming
			// CloudWatch here gave a pure Kubernetes account a CloudWatch routing table
			// and CloudWatch examples.
			name: "metricbeat template aws fields are not cloudwatch evidence",
			fields: []string{
				"@timestamp", "aws.*.metrics.*.*", "aws.applicationelb.metrics.ConsumedLCUs.avg",
				"aws.ec2.metrics.CPUUtilization.avg", "kubernetes.pod.name",
				"kubernetes.container.memory.usage.bytes", "metricset.name",
			},
			expected: []esMetricSchema{schemaElasticK8s},
			wantTS:   esTimestampECS,
		},
		{
			name:     "inconclusive mapping yields no schema so the caller can fall back",
			fields:   []string{"@timestamp", "host.name"},
			expected: nil,
			wantTS:   esTimestampECS,
		},
		{
			name:     "empty mapping",
			fields:   nil,
			expected: nil,
			wantTS:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schemas, ts := detectSchemasFromFields(tt.fields)
			assert.Len(t, schemas, len(tt.expected))
			for _, want := range tt.expected {
				assert.True(t, schemas[want], "expected schema %s", want)
			}
			assert.Equal(t, tt.wantTS, ts)
		})
	}
}

// TestDetectMetricSchemas_ProbeOverridesNameClassification proves the mapping wins
// over the index name, using the dev cluster's real shape.
func TestDetectMetricSchemas_ProbeOverridesNameClassification(t *testing.T) {
	cfg := utils.ESIndexConfig{DefaultIndex: "metricbeat*"}

	// Name-only classification calls this Metricbeat/ECS.
	nameOnly, _, _, _ := detectMetricSchemas(cfg, nil)
	assert.True(t, nameOnly[schemaMetricbeat])
	assert.False(t, nameOnly[schemaOTel])

	// The mapping says the documents are OTel-shaped on an ECS timestamp.
	probe := func(string) (map[esMetricSchema]bool, string) {
		return map[esMetricSchema]bool{schemaOTel: true}, esTimestampECS
	}
	probed, _, ts, _ := detectMetricSchemas(cfg, probe)
	assert.True(t, probed[schemaOTel], "mapping-derived schema must win")
	assert.False(t, probed[schemaMetricbeat], "name-derived schema must not survive a conclusive probe")
	assert.Equal(t, esTimestampECS, ts)
}

// TestBuildTimestampRule_DetectedFieldOverridesSchemaGuess pins the failure measured
// against the dev cluster: OTel-shaped docs stored under `@timestamp`, where the
// schema-derived rule ("range on `time`") returns 0 hits.
func TestBuildTimestampRule_DetectedFieldOverridesSchemaGuess(t *testing.T) {
	otelOnly := map[esMetricSchema]bool{schemaOTel: true}

	assert.Contains(t, buildTimestampRule(otelOnly, ""), "`time`",
		"with no detection the schema guess still applies")

	rule := buildTimestampRule(otelOnly, esTimestampECS)
	assert.Contains(t, rule, "`@timestamp`")
	assert.Contains(t, rule, "Using `time` returns 0 results")
}

// TestSystemPrompt_ExamplesUseResolvedIndexAndTimestamp pins the second half of the
// #36233 regression: the OTel fast path used to hardcode `metrics-*` and `time` in its
// routing row, header and worked example. That contradicted the Target Indices and
// Time Range lines built from the account's real config, and the model followed the
// example — reaching a wildcard that on a shared endpoint spans other environments.
func TestSystemPrompt_ExamplesUseResolvedIndexAndTimestamp(t *testing.T) {
	agent := ElasticSearchMetricsAgent{accountId: "test-account"}

	// The dev cluster's real shape for `metricbeat*`: OTel-shaped documents stored
	// under an ECS `@timestamp`.
	prompt := agent.buildSystemPrompt(
		map[esMetricSchema]bool{schemaOTel: true},
		[]string{"metricbeat*"},
		esTimestampECS,
		map[esMetricSchema]schemaTarget{schemaOTel: {index: "metricbeat*", timestamp: esTimestampECS}},
	)

	body := strings.Join(prompt.Instructions, "\n") + "\n" + strings.Join(prompt.Constraints, "\n")

	assert.NotContains(t, body, "metrics-*", "stock wildcard must not appear once a real index resolved")
	assert.NotContains(t, body, "metrix-*", "stock wildcard must not appear once a real index resolved")
	assert.Contains(t, body, "metricbeat*", "examples must use the account's resolved index")
	assert.Contains(t, body, "**OTel Schema Fast Path (for `metricbeat*`):**")
	assert.Contains(t, body, `{"range":{"@timestamp":{"gte":"now-1h"}}}`,
		"worked example must range on the detected timestamp field")
	assert.NotContains(t, body, `{"range":{"time":`, "worked example must not contradict the detected timestamp field")
}

// TestUnknownSchema_DiscoversInsteadOfGuessing pins the behaviour for a setup this
// agent has never seen: when neither the mapping nor the index name identifies a
// schema, the prompt must tell the agent to sample a document rather than hand it a
// canned field catalogue. Guessing OTel here is the original #36233 failure — the
// agent queries fields that do not exist, gets nothing, and reports "no data".
func TestUnknownSchema_DiscoversInsteadOfGuessing(t *testing.T) {
	// A mapping from some pipeline we do not model, and a name carrying no hint.
	probe := func(string) (map[esMetricSchema]bool, string) { return nil, "" }
	schemas, indices, ts, _ := detectMetricSchemas(
		utils.ESIndexConfig{DefaultIndex: "telemetry-prod-2026"}, probe)

	assert.Empty(t, schemas, "an unrecognised schema must not be guessed")
	assert.Equal(t, []string{"telemetry-prod-2026"}, indices)
	assert.Empty(t, ts)

	prompt := ElasticSearchMetricsAgent{accountId: "acct"}.buildSystemPrompt(schemas, indices, ts, nil)
	body := strings.Join(prompt.Instructions, "\n") + "\n" + strings.Join(prompt.Constraints, "\n")

	assert.Contains(t, body, "Schema Unknown — DISCOVER BEFORE QUERYING")
	assert.Contains(t, body, "telemetry-prod-2026", "discovery must target the configured index")
	assert.NotContains(t, body, "OTel Schema Fast Path", "must not assert a schema it has no evidence for")
	assert.NotContains(t, body, "Elastic Agent K8s Fast Path")
	assert.NotContains(t, body, "AWS CloudWatch Fast Path")
	assert.NotContains(t, body, "Schema Routing (CRITICAL", "no routing table when no schema is known")

	// The timestamp rule must not assert `time` either.
	rule := buildTimestampRule(schemas, "")
	assert.Contains(t, rule, "determine that field from a sample document")
	assert.NotContains(t, rule, "NOT `@timestamp`")
}

// TestSystemPrompt_MultiSchemaAttribution pins that each schema's section names its OWN
// index and timestamp. A single global exemplar reintroduced the #36233 contradiction on
// multi-index accounts: the Elastic Agent rows and examples pointed at the OTel index and
// ranged on `time`, contradicting the "Use @timestamp" text in the same block.
func TestSystemPrompt_MultiSchemaAttribution(t *testing.T) {
	schemas := map[esMetricSchema]bool{schemaOTel: true, schemaElasticK8s: true}
	indices := []string{"otel-idx", "k8s-idx"}
	targets := map[esMetricSchema]schemaTarget{
		schemaOTel:       {index: "otel-idx", timestamp: esTimestampOTel},
		schemaElasticK8s: {index: "k8s-idx", timestamp: esTimestampECS},
	}

	prompt := ElasticSearchMetricsAgent{accountId: "acct"}.buildSystemPrompt(schemas, indices, "", targets)
	body := strings.Join(prompt.Instructions, "\n")

	assert.Contains(t, body, "| `otel-idx`", "OTel row must name the OTel index")
	assert.Contains(t, body, "| `k8s-idx`", "Elastic Agent row must name its own index")
	assert.Contains(t, body, "**OTel Schema Fast Path (for `otel-idx`):**")
	assert.Contains(t, body, "**Elastic Agent K8s Fast Path (query these fields on `k8s-idx`):**")

	// Each fast path's worked example must range on its own index's timestamp.
	assert.Contains(t, body, `{"index":"otel-idx"`)
	assert.Contains(t, body, `{"index":"k8s-idx"`)
	for _, line := range prompt.Instructions {
		if strings.Contains(line, `{"index":"k8s-idx"`) {
			assert.Contains(t, line, `"@timestamp"`, "Elastic Agent example must use its own timestamp")
			assert.NotContains(t, line, `{"range":{"time"`, "must not range on the OTel timestamp")
		}
		if strings.Contains(line, `{"index":"otel-idx"`) {
			assert.Contains(t, line, `"time"`, "OTel example must use its own timestamp")
		}
	}
}

// TestSystemPrompt_ExampleExplanationsMatchTheQuery pins that the few-shot Explanation
// text agrees with the query beside it. Explanations render into the ReAct prompt as
// <thought> immediately before the tool input, so a hardcoded field name there tells the
// model to use one timestamp while the example shows another.
func TestSystemPrompt_ExampleExplanationsMatchTheQuery(t *testing.T) {
	// Dev's real shape: OTel documents stored under an ECS @timestamp.
	prompt := ElasticSearchMetricsAgent{accountId: "acct"}.buildSystemPrompt(
		map[esMetricSchema]bool{schemaOTel: true},
		[]string{"metricbeat*"},
		esTimestampECS,
		map[esMetricSchema]schemaTarget{schemaOTel: {index: "metricbeat*", timestamp: esTimestampECS}},
	)

	require.NotEmpty(t, prompt.Examples)
	for _, ex := range prompt.Examples {
		assert.NotContains(t, ex.Explanation, "time for timestamp",
			"explanation must not name a timestamp the query does not use")
		for _, step := range ex.AnswerSteps {
			assert.NotContains(t, step.Explanation, "Use `time` (NOT `@timestamp`)",
				"explanation contradicts the parameterised query")
			if strings.Contains(step.Input, `"range":{"@timestamp"`) {
				assert.NotContains(t, step.Explanation, "`time`",
					"explanation names `time` while the query ranges on @timestamp")
			}
		}
	}
}

// TestSystemPrompt_MetricbeatRowUsesResolvedIndex pins the branch that was missed when the
// other three routing rows were parameterised.
func TestSystemPrompt_MetricbeatRowUsesResolvedIndex(t *testing.T) {
	prompt := ElasticSearchMetricsAgent{accountId: "acct"}.buildSystemPrompt(
		map[esMetricSchema]bool{schemaMetricbeat: true},
		[]string{"metricbeat-8.19.11-prod"},
		esTimestampECS,
		map[esMetricSchema]schemaTarget{schemaMetricbeat: {index: "metricbeat-8.19.11-prod", timestamp: esTimestampECS}},
	)
	body := strings.Join(prompt.Instructions, "\n")

	assert.Contains(t, body, "| `metricbeat-8.19.11-prod`")
	assert.NotContains(t, body, "`metricbeat-*`", "stock pattern must not outrank the configured index")
}

// TestESSchemaProbeCache_SweepsExpiredEntries pins the opportunistic eviction: expiry
// alone is passive and only evicts on re-access, so a key probed once and never again
// would be retained for the process lifetime.
func TestESSchemaProbeCache_SweepsExpiredEntries(t *testing.T) {
	esSchemaProbeCache.Range(func(k, _ any) bool { esSchemaProbeCache.Delete(k); return true })

	expired := esSchemaProbeEntry{expiry: time.Now().Add(-time.Hour)}
	live := esSchemaProbeEntry{schemas: map[esMetricSchema]bool{schemaOTel: true}, expiry: time.Now().Add(time.Hour)}

	// One-off keys that are never read again, plus one live entry.
	for i := 0; i < esSchemaProbeSweepEvery-1; i++ {
		esSchemaProbeStore(esSchemaProbeKey{accountId: "acct", indexPattern: fmt.Sprintf("stale-%d", i)}, expired)
	}
	esSchemaProbeStore(esSchemaProbeKey{accountId: "acct", indexPattern: "live"}, live)

	remaining := 0
	esSchemaProbeCache.Range(func(_, _ any) bool { remaining++; return true })
	assert.Equal(t, 1, remaining, "expired one-off entries must be swept")

	_, ok := esSchemaProbeCache.Load(esSchemaProbeKey{accountId: "acct", indexPattern: "live"})
	assert.True(t, ok, "unexpired entry must survive the sweep")
}
