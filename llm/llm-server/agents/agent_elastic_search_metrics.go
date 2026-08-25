package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/utils"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const ElasticSearchMetricsAgentName = "elastic_search_metrics"

func init() {
	toolDescription := `Elasticsearch/Opensearch Metrics Expert. Translates natural language questions into Elasticsearch aggregation queries and executes them against metric indices (e.g. metrics-*, metricbeat-*) to retrieve, analyze, and summarize CPU, memory, network, and custom metrics. Use when the configured metrics provider is Elasticsearch or Opensearch.`
	toolInput := "Provide a question in natural language to retrieve, analyze, or summarize metrics from Elasticsearch."
	toolOutput := "Returns metric data and summaries derived from Elasticsearch aggregation responses."

	core.RegisterNBAgentFactoryAndToolAndPrioritizeAgentResponseForTool(ElasticSearchMetricsAgentName, func(accountId string) (core.NBAgent, error) {
		return ElasticSearchMetricsAgent{accountId: accountId}, nil
	}, toolDescription, toolInput, toolOutput)
}

type ElasticSearchMetricsAgent struct {
	accountId string
}

func (e ElasticSearchMetricsAgent) GetName() string {
	return ElasticSearchMetricsAgentName
}

func (e ElasticSearchMetricsAgent) GetNameAliases() []string {
	return []string{"Elastic Search Metrics", "Opensearch Metrics"}
}

func (e ElasticSearchMetricsAgent) GetDescription() string {
	return `Retrieves and analyzes metrics from Elasticsearch/Opensearch using aggregation DSL queries.`
}

// esMetricSchema identifies the ingestion pipeline / field schema for a metric index.
type esMetricSchema string

const (
	schemaOTel       esMetricSchema = "otel"
	schemaMetricbeat esMetricSchema = "metricbeat"
	schemaElasticK8s esMetricSchema = "elastic_k8s"
	schemaCloudWatch esMetricSchema = "cloudwatch"
)

// esFieldProbe reports the schemas an index pattern actually carries, and the
// timestamp field it uses, by reading its mapping. Returns an empty set when the
// mapping is unavailable or inconclusive.
type esFieldProbe func(indexPattern string) (map[esMetricSchema]bool, string)

// esSchemaProbeCache memoises mapping probes by (accountId, indexPattern). The probe
// costs an api-server round trip and an ES `_mapping` call, and GetSystemPrompt runs
// on every agent invocation — an index's mapping does not change on that timescale.
var esSchemaProbeCache sync.Map

const (
	esSchemaProbeTTL = time.Hour
	// Shorter, because a negative is as likely to mean "backend was briefly down" as
	// "this mapping is genuinely unrecognisable".
	esSchemaProbeNegativeTTL = 5 * time.Minute
)

type esSchemaProbeEntry struct {
	schemas   map[esMetricSchema]bool
	timestamp string
	expiry    time.Time
}

type esSchemaProbeKey struct {
	accountId    string
	indexPattern string
}

// esSchemaProbeSweepEvery bounds the cache without a background goroutine: every N
// stores, one Range sweeps expired entries. Expiry alone is passive — it only evicts
// on re-access, so a key queried once and never again (a deleted account, a renamed
// index) would be retained for the process lifetime. The live set is small by nature
// (one entry per account per configured index pattern), so this is about not leaking
// one-off keys rather than about volume.
const esSchemaProbeSweepEvery = 64

var esSchemaProbeStores atomic.Uint64

func esSchemaProbeStore(key esSchemaProbeKey, entry esSchemaProbeEntry) {
	esSchemaProbeCache.Store(key, entry)
	if esSchemaProbeStores.Add(1)%esSchemaProbeSweepEvery != 0 {
		return
	}
	now := time.Now()
	esSchemaProbeCache.Range(func(k, v any) bool {
		if e, ok := v.(esSchemaProbeEntry); ok && now.After(e.expiry) {
			esSchemaProbeCache.Delete(k)
		}
		return true
	})
}

// newESFieldProbe returns a probe that reads the index mapping through the
// metrics_list_labels action (ES resolves `metric` as the index pattern and returns
// its mapping field names). Any failure degrades to "inconclusive" so the caller
// falls back to name classification rather than losing the prompt.
func newESFieldProbe(ctx *security.RequestContext, accountId string) esFieldProbe {
	return func(indexPattern string) (map[esMetricSchema]bool, string) {
		if ctx == nil || indexPattern == "" {
			return nil, ""
		}
		key := esSchemaProbeKey{accountId: accountId, indexPattern: indexPattern}
		if cached, ok := esSchemaProbeCache.Load(key); ok {
			entry := cached.(esSchemaProbeEntry)
			if time.Now().Before(entry.expiry) {
				return entry.schemas, entry.timestamp
			}
			esSchemaProbeCache.Delete(key)
		}

		// Negative results are cached too, on a shorter TTL: GetSystemPrompt runs on every
		// ReAct iteration, and an account whose mapping is unavailable or unrecognised
		// would otherwise re-issue this RPC (and an ES _mapping call) every iteration —
		// precisely the accounts already taking the slower discovery path. The shorter
		// TTL keeps a transient backend failure from pinning the fallback for an hour.
		cacheNegative := func() {
			esSchemaProbeStore(key, esSchemaProbeEntry{expiry: time.Now().Add(esSchemaProbeNegativeTTL)})
		}

		resp, err := services_server.ListMetricsSeriesLabels(*ctx, accountId, "ES", indexPattern)
		if err != nil {
			ctx.GetLogger().Warn("elastic_search_metrics: mapping probe failed, falling back to index-name classification",
				"index", indexPattern, "error", err)
			cacheNegative()
			return nil, ""
		}
		fields := make([]string, 0, len(resp.Labels))
		for _, l := range resp.Labels {
			fields = append(fields, l.Label)
		}
		schemas, ts := detectSchemasFromFields(fields)
		if len(schemas) == 0 {
			ctx.GetLogger().Info("elastic_search_metrics: mapping probe inconclusive, falling back to index-name classification",
				"index", indexPattern, "num_fields", len(fields))
			cacheNegative()
			return nil, ""
		}
		esSchemaProbeStore(key, esSchemaProbeEntry{
			schemas:   schemas,
			timestamp: ts,
			expiry:    time.Now().Add(esSchemaProbeTTL),
		})
		return schemas, ts
	}
}

// esTimestampField names the time field an index actually uses. Detected from the
// mapping when available; the two schemas disagree and picking wrong returns 0 hits.
const (
	esTimestampOTel = "time"
	esTimestampECS  = "@timestamp"
)

// detectSchemasFromFields identifies which metric schemas an index pattern actually
// carries, from the field names in its mapping, plus the timestamp field it uses.
//
// This is preferred over classifyIndex because the index NAME is not evidence of the
// document shape. Two cases from real accounts:
//
//   - `metrics-*-gd-ehq-non-prod` holds Elastic Agent / ECS documents but classifies
//     as OTel by name (it matches none of the name hints), so the agent was taught
//     OTel field paths and every query returned 0 series.
//   - `metricbeat-otelshape-*` on our own dev cluster holds OTel-shaped documents
//     (`name`, `value`, `attributes.resource.attributes.*`) but with an ECS
//     `@timestamp` — the name says metricbeat, the body says OTel, and neither
//     name-derived answer produces a working query.
//
// A single pattern can legitimately span more than one schema (dev's `metricbeat*`
// matches both an OTel-shaped index and a true ECS one), so this returns a set.
// Returns an empty set when the fields are inconclusive — callers fall back to
// classifyIndex rather than asserting a schema on no evidence.
func detectSchemasFromFields(fields []string) (schemas map[esMetricSchema]bool, timestampField string) {
	schemas = make(map[esMetricSchema]bool)
	var hasName, hasValue, hasOTelAttrs, hasTime, hasAtTimestamp bool

	for _, f := range fields {
		// Dynamic-template placeholders (`aws.*.metrics.*.*`, `kubernetes.annotations.*`)
		// are mapping scaffolding, not fields any document carries. Ignore them.
		if strings.Contains(f, "*") {
			continue
		}
		switch {
		case f == "name" || f == "name.keyword":
			hasName = true
		case f == "value":
			hasValue = true
		case strings.HasPrefix(f, "attributes.resource.attributes.") || strings.HasPrefix(f, "attributes.metric.attributes."):
			hasOTelAttrs = true
		case f == esTimestampOTel:
			hasTime = true
		case f == esTimestampECS:
			hasAtTimestamp = true
		// CloudWatch is claimed ONLY on its own marker field. A bare `aws.<svc>.metrics.*`
		// hit is not evidence: Metricbeat's index template maps ~170 of those on every
		// index it creates, whether or not a byte of AWS data was ever written. Dev's
		// `metricbeat-8.19.11` mapped all of them with no CloudWatch data and no
		// `aws.cloudwatch.metric_name`, which is how this rule used to produce a
		// CloudWatch routing table and examples for a pure Kubernetes account.
		case f == "aws.cloudwatch.metric_name" || f == "aws.cloudwatch.metric_name.keyword":
			schemas[schemaCloudWatch] = true
		case strings.HasPrefix(f, "kubernetes."):
			schemas[schemaElasticK8s] = true
		case f == "metricset.name" || f == "event.dataset" || strings.HasPrefix(f, "data_stream."):
			schemas[schemaMetricbeat] = true
		}
	}

	// OTel documents are identified by the metric-name/value pair plus the nested
	// attribute tree — no single one of those is conclusive on its own.
	if hasName && hasValue && hasOTelAttrs {
		schemas[schemaOTel] = true
	}

	// Elastic Agent's K8s integration also ships data_stream.*/metricset.name, so a
	// kubernetes.* hit is the more specific answer; drop the generic metricbeat label
	// to keep the prompt's routing table from advertising two rows for one index.
	if schemas[schemaElasticK8s] {
		delete(schemas, schemaMetricbeat)
	}

	switch {
	case hasTime && !hasAtTimestamp:
		timestampField = esTimestampOTel
	case hasAtTimestamp && !hasTime:
		timestampField = esTimestampECS
	}
	return schemas, timestampField
}

// classifyIndex guesses the metric schema from an index pattern's name. Fallback
// only — prefer detectSchemasFromFields, which reads the actual mapping.
//
// ok is false when the name carries no signal. It deliberately does NOT default to
// OTel: an index called anything other than the three known hints tells us nothing
// about its documents, and asserting OTel there is how `metrics-*-gd-ehq-non-prod`
// (Elastic Agent documents) came to be taught OTel field paths. Callers must treat
// !ok as "unknown" and make the agent discover the fields instead of guessing.
func classifyIndex(pattern string) (esMetricSchema, bool) {
	lower := strings.ToLower(pattern)
	if strings.Contains(lower, "cloudwatch") {
		return schemaCloudWatch, true
	}
	if strings.Contains(lower, "kubernetes") {
		return schemaElasticK8s, true
	}
	if strings.Contains(lower, "metricbeat") || strings.Contains(lower, "metricsbeat") {
		return schemaMetricbeat, true
	}
	// The stock patterns are Nudgebee's own OTel-collector convention, so the name IS
	// evidence for these two specifically — and they are what an account with nothing
	// configured resolves to.
	if lower == "metrics-*" || lower == "metrix-*" {
		return schemaOTel, true
	}
	return "", false
}

// schemaTarget records the index a schema was actually detected in, and that index's
// own timestamp field, so each prompt section can name its own target rather than
// sharing one global exemplar.
type schemaTarget struct {
	index     string
	timestamp string
}

// detectMetricSchemas inspects the account's configured index patterns and returns
// the set of active schemas plus a deduplicated list of queryable index patterns.
// Falls back to the standard OTel trio when no index is explicitly configured.
//
// probeFields reads an index pattern's actual mapping fields; pass nil to skip the
// probe and classify by name alone (the pure path used by tests).
func detectMetricSchemas(indexCfg utils.ESIndexConfig, probeFields esFieldProbe) (schemas map[esMetricSchema]bool, availableIndices []string, timestampField string, targets map[esMetricSchema]schemaTarget) {
	schemas = make(map[esMetricSchema]bool)
	targets = make(map[esMetricSchema]schemaTarget)
	seen := make(map[string]bool)
	if probeFields == nil {
		probeFields = func(string) (map[esMetricSchema]bool, string) { return nil, "" }
	}
	tsSeen := make(map[string]bool)

	add := func(pattern string) {
		// A bare `*` is not a configured target — it means "every index on the cluster",
		// which would sweep in logs and other environments. Treat it as unconfigured so
		// the stock-variant fallback applies.
		if pattern == "" || pattern == "*" || isLogIndex(pattern) || seen[pattern] {
			return
		}
		seen[pattern] = true
		// Mapping-derived schemas win over the name guess; probe returns nothing when
		// the mapping is unavailable or inconclusive.
		probed, ts := probeFields(pattern)
		// Record which index each schema was found in, and that index's own timestamp
		// field. A multi-index account can carry different schemas in different indices,
		// and stamping one global exemplar into every section would tell the model to
		// query the OTel index with Elastic Agent field paths.
		note := func(sc esMetricSchema) {
			schemas[sc] = true
			if _, ok := targets[sc]; !ok {
				targets[sc] = schemaTarget{index: pattern, timestamp: ts}
			}
		}
		if len(probed) > 0 {
			for sc := range probed {
				note(sc)
			}
		} else if named, ok := classifyIndex(pattern); ok {
			note(named)
		}
		// Neither the mapping nor the name identified a schema: leave schemas empty for
		// this index. The prompt then instructs the agent to sample a document and derive
		// the fields, which is honest, instead of handing it a canned field catalogue for
		// a schema we have no evidence of.
		if ts != "" {
			tsSeen[ts] = true
		}
		availableIndices = append(availableIndices, pattern)
	}

	add(indexCfg.DefaultIndex)
	for _, p := range indexCfg.Indices {
		add(p)
	}

	// Default fallback: no config found → assume OTel on the stock index variants.
	//
	// These variants are ONLY offered when the account configured nothing. Adding
	// them alongside a configured index (which is what this used to do whenever the
	// configured index happened to classify as OTel) hands the model a wildcard that
	// outranks the real setting in practice, and on a shared ES endpoint that wildcard
	// spans other environments' data streams — the exact failure traced in #36233.
	// Keyed on availableIndices, NOT on schemas: an account CAN have a configured index
	// whose schema we failed to identify, and that must keep its index and take the
	// discovery path — not get swapped for the stock wildcards.
	if len(availableIndices) == 0 {
		schemas[schemaOTel] = true
		availableIndices = []string{"metrics-*", "metrix-*"}
		targets[schemaOTel] = schemaTarget{index: "metrics-*", timestamp: esTimestampOTel}
	}

	// Sort availableIndices for deterministic LLM prompt generation & caching.
	sort.Strings(availableIndices)

	// Only report a timestamp field when every probed index agrees; a mixed set
	// keeps the schema-derived wording, which explains both.
	if len(tsSeen) == 1 {
		for ts := range tsSeen {
			timestampField = ts
		}
	}

	return schemas, availableIndices, timestampField, targets
}

func (e ElasticSearchMetricsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	var indexCfg utils.ESIndexConfig
	if ctx != nil {
		if provider, err := services_server.GetObservabilityProvider(*ctx, e.accountId, "metrics", ""); err == nil && provider.DefaultIndex != "" {
			indexCfg.DefaultIndex = provider.DefaultIndex
		}
	}
	if indexCfg.DefaultIndex == "" || indexCfg.DefaultIndex == "*" {
		indexCfg = utils.GetESAccountIndexConfig(e.accountId, "metrics")
	}
	schemas, availableIndices, timestampField, targets := detectMetricSchemas(indexCfg, newESFieldProbe(ctx, e.accountId))
	return e.buildSystemPrompt(schemas, availableIndices, timestampField, targets)
}

// buildSystemPrompt renders the prompt from already-resolved schema facts. Split from
// GetSystemPrompt so the rendered prompt can be asserted without a services-server
// round trip — the index and timestamp it interpolates are exactly what the model is
// told to query, so they need direct coverage.
func (e ElasticSearchMetricsAgent) buildSystemPrompt(
	schemas map[esMetricSchema]bool,
	availableIndices []string,
	timestampField string,
	targets map[esMetricSchema]schemaTarget,
) core.NBAgentPrompt {
	// --- Base instructions (always included) ---
	instructions := []string{
		"**Your Primary Goal:** Answer user questions about metrics stored in Elasticsearch / Opensearch.",
		"**Your Method:** Construct an Elasticsearch DSL query (JSON) and execute it via `es_metrics_query`. You MUST provide both `index` (the index pattern) and `query` (the DSL body) fields in the tool input.",
		fmt.Sprintf("**Target Indices:** You MUST only query these metric indices: '%s'. NEVER query log indices (e.g. 'fluentk8-*', 'logs-*', 'logs-kubernetes.*', 'logs-apm.*', 'logs-aws.cloudtrail-*').", strings.Join(availableIndices, ", ")),
	}

	// Worked examples must use the index and timestamp of THE SCHEMA THEY DESCRIBE, not
	// one global exemplar. Hardcoding `metrics-*` / `time` contradicted the Target
	// Indices and Time Range lines (the #36233 failure); sharing one exemplar across
	// schemas reintroduces the same contradiction on multi-index accounts, telling the
	// model to query the OTel index with Elastic Agent field paths.
	fallbackIndex := "metrics-*"
	if len(availableIndices) > 0 {
		fallbackIndex = availableIndices[0]
	}
	indexFor := func(sc esMetricSchema) string {
		if t, ok := targets[sc]; ok && t.index != "" {
			return t.index
		}
		return fallbackIndex
	}
	tsFor := func(sc esMetricSchema, dflt string) string {
		if t, ok := targets[sc]; ok && t.timestamp != "" {
			return t.timestamp
		}
		if timestampField != "" {
			return timestampField
		}
		return dflt
	}
	// Used only by the schema-agnostic discovery section.
	indexExemplar := fallbackIndex

	// --- Unknown schema: discover, do not guess ---
	//
	// Neither the index mapping nor the index name identified a known schema. Every
	// canned field catalogue below would be a guess, and a wrong guess costs the whole
	// investigation — the agent queries fields that do not exist, gets empty results,
	// and concludes the account has no metrics. Tell it to look instead.
	if len(schemas) == 0 {
		instructions = append(instructions,
			"**Schema Unknown — DISCOVER BEFORE QUERYING:**",
			fmt.Sprintf("  This account's metric documents do not match a schema this agent knows. Do NOT assume field names. Start with a sample document: `{\"index\":\"%s\",\"query\":{\"query\":{\"match_all\":{}},\"size\":1}}`.", indexExemplar),
			"  From that document determine, and state in your answer which you used:",
			"    * the timestamp field — commonly `@timestamp` or `time`; use whichever the document carries",
			"    * how the metric is identified — either a metric-name field (e.g. `name`/`name.keyword`, filter with `term`) or a fully-qualified value path (e.g. `<area>.<subject>.<metric>.<unit>`, filter with `exists`)",
			"    * the dimension fields (namespace / pod / container / node / instance) as they are actually spelled in this document",
			"  `metrics_labels_list` on the index returns the full field list if the sample is not enough.",
			"  If, after discovery, the fields still do not support the question, say so plainly. NEVER report 'no data' on the strength of a query whose field names you guessed.",
		)
	}

	// --- Schema routing table (only rows for detected schemas) ---
	if len(schemas) > 0 {
		instructions = append(instructions,
			"**Schema Routing (CRITICAL — pick fields by index pattern):**",
			"  Different ingestion pipelines store metrics with completely different field schemas. ALWAYS identify the schema before writing the query:",
			"  | Index pattern                           | Schema                       | Timestamp | Metric-name field |",
			"  |-----------------------------------------|------------------------------|-----------|-------------------|",
		)
	}
	if schemas[schemaOTel] {
		instructions = append(instructions,
			fmt.Sprintf("  | %-39s | OTel collector               | `%s` | `name.keyword` |", "`"+indexFor(schemaOTel)+"`", tsFor(schemaOTel, esTimestampOTel)),
		)
	}
	if schemas[schemaMetricbeat] {
		instructions = append(instructions,
			fmt.Sprintf("  | %-39s | Metricbeat (ECS)             | `%s` | (embedded field) |", "`"+indexFor(schemaMetricbeat)+"`", tsFor(schemaMetricbeat, esTimestampECS)),
		)
	}
	if schemas[schemaElasticK8s] {
		instructions = append(instructions,
			fmt.Sprintf("  | %-39s | Elastic Agent / kube-state   | `%s` | (embedded path) |", "`"+indexFor(schemaElasticK8s)+"`", tsFor(schemaElasticK8s, esTimestampECS)),
		)
	}
	if schemas[schemaCloudWatch] {
		instructions = append(instructions,
			fmt.Sprintf("  | %-39s | Elastic Agent AWS CloudWatch | `%s` | `aws.cloudwatch.metric_name.keyword` (value under `aws.<svc>.metrics.<metric>.<stat>`) |", "`"+indexFor(schemaCloudWatch)+"`", tsFor(schemaCloudWatch, esTimestampECS)),
		)
	}
	if schemas[schemaElasticK8s] || schemas[schemaCloudWatch] {
		instructions = append(instructions,
			"  Elastic Agent indices do NOT have a `name`/`name.keyword` field — the metric is identified by the index itself, and the value lives at a fully-qualified path (e.g. `kubernetes.container.cpu.usage.nanocores`). Filter on that path's existence.",
		)
	}

	// --- OTel fast path (only if account uses OTel indices) ---
	if schemas[schemaOTel] {
		instructions = append(instructions,
			fmt.Sprintf("**OTel Schema Fast Path (for `%s`):**", indexFor(schemaOTel)),
			"  For standard K8s CPU, Memory, Network, or Filesystem metrics, call `es_metrics_query` directly — no discovery needed.",
			"  **Query fields:**",
			fmt.Sprintf("    * Metric name: `name.keyword` | Timestamp: `%s` | Value: `value`", tsFor(schemaOTel, esTimestampOTel)),
			"    * Namespace: `attributes.resource.attributes.k8s@namespace@name.keyword`",
			"    * Pod: `attributes.resource.attributes.k8s@pod@name.keyword`",
			"    * Container: `attributes.resource.attributes.k8s@container@name.keyword`",
			"    * Node: `attributes.resource.attributes.k8s@node@name.keyword`",
			"  **CPU:** `container.cpu.usage`, `k8s.pod.cpu.usage`, `k8s.node.cpu.usage`",
			"  **Memory:** `container.memory.usage`, `container.memory.working_set`, `container.memory.rss`, `k8s.pod.memory.usage`, `k8s.node.memory.usage`",
			"  **Network:** `k8s.pod.network.io`, `k8s.pod.network.errors` (filter direction: `receive`/`transmit` via `attributes.metric.attributes.direction.keyword`)",
			"  **Filesystem:** `container.filesystem.usage`, `container.filesystem.capacity`, `k8s.node.filesystem.usage`",
			"  **Volume:** `k8s.volume.available`, `k8s.volume.capacity`, `k8s.volume.inodes`, `k8s.volume.inodes.free`, `k8s.volume.inodes.used`",
			"  **K8s Cluster (k8scluster receiver):** `k8s.container.cpu_request`, `k8s.container.cpu_limit`, `k8s.container.memory_request`, `k8s.container.restarts`, `k8s.pod.phase`, `k8s.deployment.desired`, `k8s.deployment.available`, `k8s.daemonset.desired_scheduled_nodes`, `k8s.daemonset.ready_nodes`, `k8s.replicaset.desired`, `k8s.hpa.current_replicas`, `k8s.hpa.desired_replicas`",
			"  **Example (pod CPU):**",
			fmt.Sprintf(`    {"index":"%s","query":{"query":{"bool":{"filter":[{"term":{"name.keyword":"container.cpu.usage"}},{"term":{"attributes.resource.attributes.k8s@namespace@name.keyword":"default"}},{"range":{"%s":{"gte":"now-1h"}}}]}}}}`, indexFor(schemaOTel), tsFor(schemaOTel, esTimestampOTel)),
		)
	}

	// --- Elastic Agent K8s fast path (only if account uses elastic_k8s indices) ---
	if schemas[schemaElasticK8s] {
		instructions = append(instructions,
			fmt.Sprintf("**Elastic Agent K8s Fast Path (query these fields on `%s`):**", indexFor(schemaElasticK8s)),
			"  Filter by `exists` on the value path plus dimension filters. Use `@timestamp`. Dimension fields are keyword-mapped — do NOT append `.keyword`.",
			"  **Dimensions:** `kubernetes.namespace`, `kubernetes.pod.name`, `kubernetes.container.name`, `kubernetes.node.name`, `kubernetes.deployment.name`, `kubernetes.labels.<name>`",
			"  **Container fields:** CPU: `kubernetes.container.cpu.usage.nanocores`, `kubernetes.container.cpu.limit.nanocores` | Memory: `kubernetes.container.memory.usage.bytes`, `kubernetes.container.memory.workingset.bytes`, `kubernetes.container.memory.rss.bytes`, `kubernetes.container.memory.limit.bytes` | FS: `kubernetes.container.fs.usage.bytes`, `kubernetes.container.fs.capacity.bytes` | Status: `kubernetes.container.status.phase`, `kubernetes.container.status.ready`, `kubernetes.container.status.restarts`",
			"  **Pod fields:** CPU: `kubernetes.pod.cpu.usage.nanocores` | Memory: `kubernetes.pod.memory.usage.bytes`, `kubernetes.pod.memory.workingset.bytes` | Network: `kubernetes.pod.network.rx.bytes`, `kubernetes.pod.network.tx.bytes`, `kubernetes.pod.network.rx.errors`, `kubernetes.pod.network.tx.errors`",
			"  **Node fields:** CPU: `kubernetes.node.cpu.usage.nanocores` | Memory: `kubernetes.node.memory.usage.bytes`, `kubernetes.node.memory.available.bytes` | FS: `kubernetes.node.fs.usage.bytes`, `kubernetes.node.fs.capacity.bytes` | Network: `kubernetes.node.network.rx.bytes`, `kubernetes.node.network.tx.bytes`",
			"  **Volume fields:** `kubernetes.volume.available.bytes`, `kubernetes.volume.capacity.bytes`, `kubernetes.volume.used.bytes`, `kubernetes.volume.inodes.count`, `kubernetes.volume.inodes.free`",
			"  **Event fields:** `kubernetes.event.count`, `kubernetes.event.type` (Normal/Warning), `kubernetes.event.reason`, `kubernetes.event.involved_object.kind`, `kubernetes.event.involved_object.name`",
			"  **kube-state fields:**",
			"    - state_pod: `kubernetes.pod.status.phase`, `kubernetes.pod.status.ready`, `kubernetes.pod.status.scheduled`",
			"    - state_container: `kubernetes.container.status.phase`, `kubernetes.container.status.ready`, `kubernetes.container.status.restarts`, `kubernetes.container.status.waiting.reason`",
			"    - state_deployment: `kubernetes.deployment.replicas.desired`, `kubernetes.deployment.replicas.available`, `kubernetes.deployment.replicas.unavailable`, `kubernetes.deployment.paused`",
			"    - state_replicaset: `kubernetes.replicaset.replicas.desired`, `kubernetes.replicaset.replicas.ready`, `kubernetes.replicaset.replicas.available`",
			"    - state_daemonset: `kubernetes.daemonset.replicas.desired`, `kubernetes.daemonset.replicas.ready`, `kubernetes.daemonset.replicas.misscheduled`",
			"    - state_job: `kubernetes.job.pods.active`, `kubernetes.job.pods.succeeded`, `kubernetes.job.pods.failed`",
			"    - state_node: `kubernetes.node.status.ready` (1=True, 0=False), `kubernetes.node.cpu.allocatable.cores`, `kubernetes.node.memory.allocatable.bytes`",
			"    - state_persistentvolume: `kubernetes.persistentvolume.capacity.bytes`, `kubernetes.persistentvolume.phase`",
			"    - state_persistentvolumeclaim: `kubernetes.persistentvolumeclaim.capacity.bytes`, `kubernetes.persistentvolumeclaim.phase`, `kubernetes.persistentvolumeclaim.request_storage.bytes`",
			"  **Example (container CPU):**",
			fmt.Sprintf(`    {"index":"%s","query":{"query":{"bool":{"filter":[{"exists":{"field":"kubernetes.container.cpu.usage.nanocores"}},{"term":{"kubernetes.namespace":"default"}},{"range":{"%s":{"gte":"now-1h"}}}]}}}}`, indexFor(schemaElasticK8s), tsFor(schemaElasticK8s, esTimestampECS)),
			"  **Example (deployment replicas):**",
			fmt.Sprintf(`    {"index":"%s","query":{"query":{"bool":{"filter":[{"exists":{"field":"kubernetes.deployment.replicas.available"}},{"term":{"kubernetes.namespace":"production"}},{"range":{"%s":{"gte":"now-30m"}}}]}}}}`, indexFor(schemaElasticK8s), tsFor(schemaElasticK8s, esTimestampECS)),
		)
	}

	// --- CloudWatch fast path (only if account uses cloudwatch indices) ---
	if schemas[schemaCloudWatch] {
		instructions = append(instructions,
			fmt.Sprintf("**AWS CloudWatch Fast Path (query these fields on `%s`):**", indexFor(schemaCloudWatch)),
			"  Filter by `exists` on the metric value path plus dimension filters. Use `@timestamp`. Do NOT append `.keyword` to dimension fields.",
			"  **Dimensions:** `cloud.region`, `cloud.account.id`, `aws.dimensions.InstanceId`, `aws.dimensions.DBInstanceIdentifier`, `aws.dimensions.FunctionName`, `aws.dimensions.QueueName`, `aws.dimensions.BucketName`",
			"  **Metric value paths (suffix: `.avg`, `.max`, `.min`, `.sum`, `.count`):**",
			"    - EC2: `aws.ec2.metrics.CPUUtilization.avg`, `aws.ec2.metrics.NetworkIn.sum`, `aws.ec2.metrics.NetworkOut.sum`, `aws.ec2.metrics.StatusCheckFailed.max`",
			"    - RDS: `aws.rds.metrics.CPUUtilization.avg`, `aws.rds.metrics.FreeableMemory.avg`, `aws.rds.metrics.FreeStorageSpace.avg`, `aws.rds.metrics.DatabaseConnections.avg`, `aws.rds.metrics.ReadLatency.avg`",
			"    - Lambda: `aws.lambda.metrics.Invocations.sum`, `aws.lambda.metrics.Errors.sum`, `aws.lambda.metrics.Duration.avg`, `aws.lambda.metrics.Throttles.sum`",
			"    - SQS: `aws.sqs.metrics.NumberOfMessagesSent.sum`, `aws.sqs.metrics.ApproximateNumberOfMessagesVisible.avg`, `aws.sqs.metrics.ApproximateAgeOfOldestMessage.max`",
			"    - S3: `aws.s3.metrics.BucketSizeBytes.avg`, `aws.s3.metrics.NumberOfObjects.avg`, `aws.s3.metrics.AllRequests.sum`",
			"    - ALB: `aws.applicationelb.metrics.RequestCount.sum`, `aws.applicationelb.metrics.HTTPCode_Target_5XX_Count.sum`, `aws.applicationelb.metrics.TargetResponseTime.avg`",
			"  **Example (EC2 CPU):**",
			fmt.Sprintf(`    {"index":"%s","query":{"query":{"bool":{"filter":[{"exists":{"field":"aws.ec2.metrics.CPUUtilization.avg"}},{"term":{"aws.dimensions.InstanceId":"i-0123456789abcdef0"}},{"range":{"%s":{"gte":"now-1h"}}}]}}}}`, indexFor(schemaCloudWatch), tsFor(schemaCloudWatch, esTimestampECS)),
		)
	}

	// --- Field discovery and query patterns (always included, schema-aware) ---
	// Schema-gated: `__name__` exists in no schema we support, and handing that hint
	// to an Elastic Agent account had the model emitting `__name__` term/prefix filters.
	var discoveryHints []string
	if schemas[schemaOTel] {
		discoveryHints = append(discoveryHints,
			"  - **OTel:** sample doc `__name__` → query `name.keyword`. `resource.attributes.k8s@namespace@name` → `attributes.resource.attributes.k8s@namespace@name.keyword`. Append `.keyword` to all text fields.",
		)
	}
	if schemas[schemaElasticK8s] {
		discoveryHints = append(discoveryHints,
			"  - **Elastic Agent K8s:** no `name` field — filter by `exists` on the value path. Dimension fields (e.g. `kubernetes.namespace`) are keyword-mapped; do NOT append `.keyword`.",
		)
	}
	if schemas[schemaCloudWatch] {
		discoveryHints = append(discoveryHints,
			"  - **CloudWatch:** metric value at `aws.<service>.metrics.<MetricName>.<stat>`. Use `aws.cloudwatch.metric_name.keyword` to filter by name when multiple metrics are present.",
		)
	}

	instructions = append(instructions,
		"**Field Discovery (for custom/non-standard metrics only):**",
		"  If the metric is NOT in the templates above:",
		"  - Step 1: Call `metrics_list` with the index pattern to discover metric names.",
		"  - Step 2: Fetch a `size:1` sample document to inspect the field structure.",
	)
	instructions = append(instructions, discoveryHints...)
	instructions = append(instructions,
		"**Query Patterns:** Use `bool`/`filter` queries only. Do NOT use `size:0` or `aggs` — the backend extracts time-series from document hits, not aggregation results.",
	)

	// Timestamp rule — tailored to the account's schemas.
	tsRule := buildTimestampRule(schemas, timestampField)
	instructions = append(instructions,
		"**Time Range:** "+tsRule+" Default to `now-1h` if the user does not specify.",
		"**Self-Correction (when `es_metrics_query` returns empty `payload`):**",
		"  1. Widen the time range: `now-1h` → `now-6h` → `now-24h`.",
		"  2. Remove filters one by one — keep only the metric filter + time range first.",
		"  3. Verify you used the correct timestamp field for this index's schema.",
		"  4. Verify field naming conventions (`.keyword` suffix / `exists` filter) match the schema.",
		"  5. Try a different (more specific) index pattern.",
		"  NEVER retry the same query unchanged. Maximum 4 retries total.",
		"**Final Answer:** Summarize metric values (peak / avg / latest) in a concise SRE-style report. Do NOT return the raw DSL.",
	)

	// --- Constraints (schema-aware) ---
	constraints := []string{
		"You MUST execute every query via `es_metrics_query` before answering.",
		"For custom/non-standard metrics, you MUST discover field names via a `size:1` sample document or `metrics_labels_list` BEFORE writing the final query.",
		tsRule,
		"NEVER use `size:0` or `aggs` in queries — use filter queries only.",
		"NEVER return the generated DSL as the final answer — users want metric values.",
		"NEVER query log-specific indices for metric data.",
		// `__name__` is OURS, not the model's invention: for Beats-family indices the
		// backend flattens each numeric field into a series and labels it `__name__` with
		// that field's path. So results advertise `__name__` while no such field is
		// queryable, and a `term` filter on it silently matches nothing — 13 times in one
		// traced investigation. Forbidding it outright (the previous wording) stopped
		// nothing, because it named no replacement. Explain the translation instead.
		"`__name__` in a RESULT is not a queryable field — it is the metric's fully-qualified field path. To select that metric, use an `exists` filter on the path itself (e.g. `{\"exists\":{\"field\":\"kubernetes.pod.cpu.usage.nanocores\"}}`), never `{\"term\":{\"__name__\":...}}`. On OTel indices, which do have a real metric-name field, filter `name.keyword` instead.",
		// Sampling first is cheaper and more reliable than any built-in field list: it
		// returns only fields this account actually populates. Measured on a customer
		// estate: one sampled document is ~4KB and names every metric in its dataset,
		// against ~400KB for the full mapping, most of which is unpopulated template.
		"When you do not know an index's fields, FETCH A SAMPLE FIRST: `{\"query\":{\"match_all\":{}},\"size\":2}` with a recent time range. The `__name__` values in the result are this environment's real metric paths and the label keys are its real dimensions. Prefer them over any field name you assume.",
		"Do not ask the user for clarification — make the best assumption and proceed.",
	}
	if schemas[schemaOTel] {
		constraints = append(constraints,
			fmt.Sprintf("OTel schema (`%s`): ALWAYS append `.keyword` to text fields in `term` filters. NEVER use `__name__` — use `name.keyword` instead.", indexFor(schemaOTel)),
		)
	}
	if schemas[schemaElasticK8s] || schemas[schemaCloudWatch] {
		constraints = append(constraints,
			"Elastic Agent / CloudWatch schemas: filter by `exists` on the value path; do NOT append `.keyword` to dimension fields — they are already keyword-mapped.",
		)
	}

	// --- Examples (only for detected schemas) ---
	var examples []core.NBAgentPromptExample

	if schemas[schemaOTel] {
		examples = append(examples,
			core.NBAgentPromptExample{
				Question: "Average CPU usage of all pods in namespace default over the last 30 minutes",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolMetricsList,
						Input:       indexExemplar,
						Explanation: "Discover available metric names (e.g. container.cpu.usage, k8s.pod.cpu.usage).",
					},
					{
						Tool:        tools.ToolESMetricsQuery,
						Input:       fmt.Sprintf(`{"index":"%s","query":{"query":{"bool":{"filter":[{"term":{"name.keyword":"container.cpu.usage"}},{"term":{"attributes.resource.attributes.k8s@namespace@name.keyword":"default"}},{"range":{"%s":{"gte":"now-30m"}}}]}}}}`, indexFor(schemaOTel), tsFor(schemaOTel, esTimestampOTel)),
						Explanation: fmt.Sprintf("Filter by name.keyword and namespace with attributes.resource.attributes. prefix. Range on `%s` for this account.", tsFor(schemaOTel, esTimestampOTel)),
					},
				},
				Explanation: fmt.Sprintf("OTel schema: use name.keyword for metric name, attributes.resource.attributes.* prefix for K8s dimensions, `%s` for timestamp.", tsFor(schemaOTel, esTimestampOTel)),
			},
		)
	}

	if schemas[schemaElasticK8s] {
		examples = append(examples,
			core.NBAgentPromptExample{
				Question: "Container CPU usage in namespace production over the last hour",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolESMetricsQuery,
						Input:       fmt.Sprintf(`{"index":"%s","query":{"query":{"bool":{"filter":[{"exists":{"field":"kubernetes.container.cpu.usage.nanocores"}},{"term":{"kubernetes.namespace":"production"}},{"range":{"%s":{"gte":"now-1h"}}}]}}}}`, indexFor(schemaElasticK8s), tsFor(schemaElasticK8s, esTimestampECS)),
						Explanation: fmt.Sprintf("Elastic Agent K8s: filter by exists on the value path, `%s` for time range, no .keyword on dimension fields.", tsFor(schemaElasticK8s, esTimestampECS)),
					},
				},
				Explanation: fmt.Sprintf("Elastic Agent K8s schema: metric = exists filter on value path; `%s`; dimensions are already keyword-mapped.", tsFor(schemaElasticK8s, esTimestampECS)),
			},
			core.NBAgentPromptExample{
				Question: "Deployment replica counts in namespace production",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolESMetricsQuery,
						Input:       fmt.Sprintf(`{"index":"%s","query":{"query":{"bool":{"filter":[{"exists":{"field":"kubernetes.deployment.replicas.available"}},{"term":{"kubernetes.namespace":"production"}},{"range":{"%s":{"gte":"now-15m"}}}]}}}}`, indexFor(schemaElasticK8s), tsFor(schemaElasticK8s, esTimestampECS)),
						Explanation: "State metrics live in dedicated state_<resource>-* indices. Filter by exists on the value path.",
					},
				},
				Explanation: "State metrics: one index per K8s resource type; filter by exists on the replica/status value path.",
			},
		)
	}

	if schemas[schemaCloudWatch] {
		examples = append(examples,
			core.NBAgentPromptExample{
				Question: "EC2 CPU utilization for instance i-0123abcd over the last 30 minutes",
				AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
					{
						Tool:        tools.ToolESMetricsQuery,
						Input:       fmt.Sprintf(`{"index":"%s","query":{"query":{"bool":{"filter":[{"exists":{"field":"aws.ec2.metrics.CPUUtilization.avg"}},{"term":{"aws.dimensions.InstanceId":"i-0123abcd"}},{"range":{"%s":{"gte":"now-30m"}}}]}}}}`, indexFor(schemaCloudWatch), tsFor(schemaCloudWatch, esTimestampECS)),
						Explanation: fmt.Sprintf("CloudWatch: value at aws.<svc>.metrics.<MetricName>.<stat>; dimension under aws.dimensions.*; `%s`.", tsFor(schemaCloudWatch, esTimestampECS)),
					},
				},
				Explanation: "CloudWatch schema: metric paths include statistic suffix (avg/max/min/sum/count); dimensions under aws.dimensions.*.",
			},
		)
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in Elasticsearch / Opensearch metric analysis in Kubernetes environments",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage: map[string][]string{
			tools.ToolESMetricsQuery: {
				"Executes an Elasticsearch DSL query against the metrics API.",
				"Input: JSON object with `index` (index pattern) and `query` (DSL body with filter queries — NO aggs or size:0).",
				"Output: Metric time-series results (timestamps and values) extracted from document hits.",
			},
			tools.ToolMetricsList: {
				"Lists available metric names in an Elasticsearch index.",
				fmt.Sprintf("Input: index pattern (e.g. '%s').", indexExemplar),
				"Output: Distinct metric names. Use to discover available metrics before querying.",
			},
			tools.ToolMetricsLabelsList: {
				"Fetches available field names and types for an index pattern.",
				"Input: index pattern.",
				"Output: Field names and data types. Use to discover correct field names before querying.",
			},
		},
		Examples:     examples,
		OutputFormat: "Markdown SRE-style summary highlighting key metric values (peak / avg / latest) and any anomalies. Do not include raw DSL.",
		Rag: core.NBAgentPromptRag{
			Module: "elasticsearch_metrics",
		},
	}
}

// buildTimestampRule returns a concise timestamp rule tailored to the active schemas.
//
// timestampField, when non-empty, is the field the account's mapping actually carries
// and overrides the schema-derived guess. Schema does not imply timestamp: our own dev
// cluster serves OTel-shaped documents (`name`/`value`/`attributes.*`) under an ECS
// `@timestamp`, and telling the model to range on `time` there returns 0 hits.
func buildTimestampRule(schemas map[esMetricSchema]bool, timestampField string) string {
	switch timestampField {
	case esTimestampOTel:
		return "Always include a `range` filter on `time` — this account's metric indices carry `time`, not `@timestamp`. Using `@timestamp` returns 0 results."
	case esTimestampECS:
		return "Always include a `range` filter on `@timestamp` — this account's metric indices carry `@timestamp`, not `time`. Using `time` returns 0 results."
	}

	// No schema identified and no timestamp detected: say so. Falling through to the
	// OTel wording here would assert `time` on an account we know nothing about — the
	// same confident guess this whole change exists to remove.
	if len(schemas) == 0 {
		return "Always include a `range` filter on the timestamp field, but determine that field from a sample document first — it is `@timestamp` on some pipelines and `time` on others, and the wrong one returns 0 results."
	}

	otel := schemas[schemaOTel]
	other := schemas[schemaElasticK8s] || schemas[schemaCloudWatch] || schemas[schemaMetricbeat]

	switch {
	case otel && other:
		return "Always include a `range` filter on the timestamp field: `time` for OTel (`metrics-*`/`metrix-*`), `@timestamp` for all other indices. Using the wrong field returns 0 results."
	case other:
		return "Always include a `range` filter on `@timestamp`. Do NOT use `time` — it is not a valid timestamp field for these indices."
	default: // otel only
		return "Always include a `range` filter on `time` (NOT `@timestamp`). Using `@timestamp` on OTel indices returns 0 results."
	}
}

func (e ElasticSearchMetricsAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	defaultIndex := ""
	if ctx != nil {
		if providerInfo, err := services_server.GetObservabilityProvider(*ctx, e.accountId, "metrics", ""); err == nil && providerInfo.DefaultIndex != "" {
			defaultIndex = providerInfo.DefaultIndex
		}
	}
	if defaultIndex == "" {
		defaultIndex = utils.GetESAccountIndexConfig(e.accountId, "metrics").DefaultIndex
	}
	return []toolcore.NBTool{
		tools.ESMetricsQueryTool{},
		tools.MetricsListTool{Provider: "ES", DefaultIndex: defaultIndex},
		tools.ListMetricsLabelsTool{Provider: "ES", DefaultIndex: defaultIndex},
		tools.ListMetricsLabelValuesTool{Provider: "ES", DefaultIndex: defaultIndex},
	}
}

func (e ElasticSearchMetricsAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// GetMaxIterations caps the ReAct loop to allow query → execute → self-correct → execute → answer.
func (e ElasticSearchMetricsAgent) GetMaxIterations() int {
	return 10
}

func (e ElasticSearchMetricsAgent) UpdateExecutorLlmResponse(actions []core.NBAgentPlannerToolAction, finished *core.NBAgentPlannerFinishAction, err error) ([]core.NBAgentPlannerToolAction, *core.NBAgentPlannerFinishAction, error) {
	return actions, finished, err
}

// UpdateToolResponseForPlanner truncates and summarizes large ES metric responses
// to prevent context window bloat in the ReAct loop.
func (e ElasticSearchMetricsAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	if !strings.EqualFold(toolRequest.Tool, tools.ToolESMetricsQuery) {
		const maxDiscoveryChars = 4000
		if len(toolResponse) > maxDiscoveryChars {
			cutoff := strings.LastIndex(toolResponse[:maxDiscoveryChars], "\n")
			if cutoff <= 0 {
				cutoff = maxDiscoveryChars
			}
			return toolResponse[:cutoff] + "\n... (truncated — use a more specific pattern to narrow results)"
		}
		return toolResponse
	}

	var response map[string]any
	if err := common.UnmarshalJson([]byte(toolResponse), &response); err != nil {
		return truncateString(toolResponse, 4000)
	}

	results, ok := response["results"].([]any)
	if !ok || len(results) == 0 {
		return toolResponse
	}

	var sb strings.Builder
	for _, resultAny := range results {
		result, ok := resultAny.(map[string]any)
		if !ok {
			continue
		}

		query, _ := result["query"].(string)
		payload, ok := result["payload"].([]any)
		if !ok || len(payload) == 0 {
			fmt.Fprintf(&sb, "Query: %s\nResult: No data found (empty payload)\n\n", query)
			continue
		}

		fmt.Fprintf(&sb, "Query: %s\nSeries count: %d\n", query, len(payload))

		maxSeries := 5
		if len(payload) > maxSeries {
			fmt.Fprintf(&sb, "(showing first %d of %d series)\n", maxSeries, len(payload))
			payload = payload[:maxSeries]
		}

		for i, seriesAny := range payload {
			series, ok := seriesAny.(map[string]any)
			if !ok {
				continue
			}

			metricLabels := ""
			if m, ok := series["metric"]; ok {
				labelBytes, _ := common.MarshalJson(m)
				metricLabels = string(labelBytes)
			}

			timestamps, _ := series["timestamps"].([]any)
			values, _ := series["values"].([]any)
			numPoints, _ := series["num_points"].(float64)
			nPoints := int(numPoints)
			if nPoints == 0 {
				nPoints = len(values)
			}

			fmt.Fprintf(&sb, "\n--- Series %d ---\n", i+1)
			fmt.Fprintf(&sb, "Labels: %s\n", metricLabels)
			fmt.Fprintf(&sb, "Points: %d\n", nPoints)

			if stats, ok := series["stats"].(map[string]any); ok {
				fmt.Fprintf(&sb, "Stats: min=%v, max=%v, avg=%v\n", stats["min"], stats["max"], stats["avg"])
			}

			if len(values) > 0 && len(values) <= 20 {
				valBytes, _ := common.MarshalJson(values)
				tsBytes, _ := common.MarshalJson(timestamps)
				fmt.Fprintf(&sb, "Values: %s\nTimestamps: %s\n", string(valBytes), string(tsBytes))
			} else if len(values) > 20 {
				fmt.Fprintf(&sb, "(raw values omitted — %d points, use stats above)\n", len(values))
			}
		}
		sb.WriteString("\n")
	}

	return truncateString(sb.String(), 6000)
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + fmt.Sprintf("\n... (truncated: %d of %d chars)", maxLen, len(s))
}

// isLogIndex returns true for index patterns known to contain logs rather than metrics.
// All Elastic Agent log streams (`logs-*`) contain the substring "log", as do
// OTel/Fluent/SigNoz log index names, so a single Contains check is sufficient.
func isLogIndex(pattern string) bool {
	lower := strings.ToLower(pattern)
	return strings.Contains(lower, "log") || strings.Contains(lower, "fluent") || strings.Contains(lower, "signoz")
}
