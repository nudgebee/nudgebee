package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// getMaxDatadogMetricsToolResponseChars returns the max chars for metrics_list/metrics_labels_list
// responses to prevent context bloat. Uses the shared config (default 4000).
func getMaxDatadogMetricsToolResponseChars() int {
	if v := config.Config.LlmServerAgentPromqlMaxToolRespChars; v > 0 {
		return v
	}
	return 4000
}

// datadogFastPathTemplate represents a pre-built query template for common metric patterns.
type datadogFastPathTemplate struct {
	Metric      string // e.g., "aws.ec2.cpuutilization"
	Aggregation string // e.g., "avg"
	GroupBy     string // e.g., "host"
}

// Trend classification thresholds (percentage change between first and last quarter).
const (
	trendRisingThreshold       = 20.0  // changePct > 20% → Rising
	trendFallingThreshold      = -20.0 // changePct < -20% → Falling
	trendSlightlyRisingThresh  = 5.0   // changePct > 5% → Slightly rising
	trendSlightlyFallingThresh = -5.0  // changePct < -5% → Slightly falling
	maxSeriesToSummarize       = 5     // cap series shown in UpdateToolResponseForPlanner
)

// datadogFastPathTemplates maps keyword patterns to known Datadog query templates.
// When matched, the agent can skip metrics_list and go straight to datadog_metrics_execute.
// Each entry maps a set of keywords (all must match) to a template.
//
// ORDER MATTERS: entries are evaluated top-to-bottom, first match wins.
// Place more-specific entries (more keywords) before less-specific ones
// for the same service so that e.g. "ec2 network in" matches before "ec2 network".
var datadogFastPathTemplates = []struct {
	Keywords []string // all keywords must be present in the query (case-insensitive)
	Template datadogFastPathTemplate
}{
	// AWS EC2
	{Keywords: []string{"ec2", "cpu"}, Template: datadogFastPathTemplate{"aws.ec2.cpuutilization", "avg", "host"}},
	{Keywords: []string{"ec2", "network", "in"}, Template: datadogFastPathTemplate{"aws.ec2.network_in", "avg", "host"}},
	{Keywords: []string{"ec2", "network", "out"}, Template: datadogFastPathTemplate{"aws.ec2.network_out", "avg", "host"}},
	{Keywords: []string{"ec2", "network"}, Template: datadogFastPathTemplate{"aws.ec2.network_in", "avg", "host"}},
	{Keywords: []string{"ec2", "disk"}, Template: datadogFastPathTemplate{"aws.ebs.volume_read_ops", "avg", "host"}},
	// AWS ALB
	{Keywords: []string{"alb", "request"}, Template: datadogFastPathTemplate{"aws.applicationelb.request_count", "sum", "loadbalancer"}},
	{Keywords: []string{"load balancer", "request"}, Template: datadogFastPathTemplate{"aws.applicationelb.request_count", "sum", "loadbalancer"}},
	{Keywords: []string{"alb", "5xx"}, Template: datadogFastPathTemplate{"aws.applicationelb.httpcode_elb_5xx", "sum", "loadbalancer"}},
	{Keywords: []string{"alb", "error"}, Template: datadogFastPathTemplate{"aws.applicationelb.httpcode_elb_5xx", "sum", "loadbalancer"}},
	{Keywords: []string{"alb", "latency"}, Template: datadogFastPathTemplate{"aws.applicationelb.target_response_time.average", "avg", "loadbalancer"}},
	{Keywords: []string{"alb", "response time"}, Template: datadogFastPathTemplate{"aws.applicationelb.target_response_time.average", "avg", "loadbalancer"}},
	{Keywords: []string{"alb", "healthy"}, Template: datadogFastPathTemplate{"aws.applicationelb.healthy_host_count", "avg", "loadbalancer"}},
	// AWS EBS
	{Keywords: []string{"ebs", "read"}, Template: datadogFastPathTemplate{"aws.ebs.volume_read_ops", "avg", "host"}},
	{Keywords: []string{"ebs", "write"}, Template: datadogFastPathTemplate{"aws.ebs.volume_write_ops", "avg", "host"}},
	// AWS NAT Gateway
	{Keywords: []string{"nat", "connection"}, Template: datadogFastPathTemplate{"aws.natgateway.active_connection_count", "avg", "name"}},
	{Keywords: []string{"nat", "gateway"}, Template: datadogFastPathTemplate{"aws.natgateway.active_connection_count", "avg", "name"}},
	// AWS Elasticsearch
	{Keywords: []string{"elasticsearch", "cpu"}, Template: datadogFastPathTemplate{"aws.es.cpuutilization", "avg", "domain_name"}},
	{Keywords: []string{"es", "cpu"}, Template: datadogFastPathTemplate{"aws.es.cpuutilization", "avg", "domain_name"}},
	// AWS Lambda
	{Keywords: []string{"lambda", "memory"}, Template: datadogFastPathTemplate{"aws.lambda.memorysize", "avg", "functionname"}},
	{Keywords: []string{"lambda", "timeout"}, Template: datadogFastPathTemplate{"aws.lambda.timeout", "avg", "functionname"}},
	// Kubernetes
	{Keywords: []string{"pod", "cpu"}, Template: datadogFastPathTemplate{"kubernetes.cpu.usage.total", "avg", "pod_name"}},
	{Keywords: []string{"pod", "memory"}, Template: datadogFastPathTemplate{"kubernetes.memory.usage", "avg", "pod_name"}},
	{Keywords: []string{"container", "cpu"}, Template: datadogFastPathTemplate{"container.cpu.usage", "avg", "container_name"}},
	{Keywords: []string{"container", "memory"}, Template: datadogFastPathTemplate{"container.memory.usage", "avg", "container_name"}},
	{Keywords: []string{"container", "restart"}, Template: datadogFastPathTemplate{"kubernetes.containers.restarts", "sum", "pod_name"}},
	{Keywords: []string{"node", "cpu"}, Template: datadogFastPathTemplate{"system.cpu.user", "avg", "host"}},
	{Keywords: []string{"node", "memory"}, Template: datadogFastPathTemplate{"system.mem.used", "avg", "host"}},
	// System
	{Keywords: []string{"system", "cpu"}, Template: datadogFastPathTemplate{"system.cpu.user", "avg", "host"}},
	{Keywords: []string{"system", "memory"}, Template: datadogFastPathTemplate{"system.mem.used", "avg", "host"}},
	{Keywords: []string{"system", "disk"}, Template: datadogFastPathTemplate{"system.disk.used", "avg", "host"}},
	{Keywords: []string{"system", "load"}, Template: datadogFastPathTemplate{"system.load.1", "avg", "host"}},
}

// wordTokenizer splits on non-word characters to produce individual word tokens.
var wordTokenizer = regexp.MustCompile(`[^\w]+`)

// tokenizeQuery splits a lowercased query into word tokens and bigrams (adjacent pairs).
// Single-word keywords are checked against the token set; multi-word phrases against bigrams
// or via consecutive token lookup, preventing substring matches like "in" inside "instances".
func tokenizeQuery(queryLower string) (tokens map[string]bool, bigrams map[string]bool) {
	parts := wordTokenizer.Split(queryLower, -1)
	tokens = make(map[string]bool, len(parts))
	bigrams = make(map[string]bool, len(parts))
	var prev string
	for _, p := range parts {
		if p == "" {
			continue
		}
		tokens[p] = true
		if prev != "" {
			bigrams[prev+" "+p] = true
		}
		prev = p
	}
	return tokens, bigrams
}

// queryContainsPhrase checks if a keyword/phrase exists as whole tokens in the query.
// Single words are checked against the token set. Short keywords (< 4 chars like "in",
// "es", "io") require an exact token match to prevent false substring matches (e.g.,
// "instances" matching "in"). Keywords with 4+ chars use length-capped prefix matching
// (token at most 2 chars longer) so plurals work (node→nodes, request→requests) but
// compound words are blocked (load→loadbalancer). Multi-word phrases are checked against
// bigrams or as consecutive tokens.
func queryContainsPhrase(phrase string, tokens map[string]bool, bigrams map[string]bool) bool {
	if !strings.Contains(phrase, " ") {
		// Single word
		if tokens[phrase] {
			return true
		}
		// For keywords with 4+ chars, allow prefix matching for plurals (request→requests,
		// node→nodes) but cap the length difference at 2 chars to block compound words
		// (load→loadbalancer). This ensures "nodes" matches "node" (+1) while
		// "loadbalancer" does not match "load" (+8).
		if len(phrase) >= 4 {
			for token := range tokens {
				if strings.HasPrefix(token, phrase) && len(token) <= len(phrase)+2 {
					return true
				}
			}
		}
		return false
	}
	// Multi-word phrase — check bigram set first (covers 2-word phrases)
	if bigrams[phrase] {
		return true
	}
	// For longer phrases, check if all words exist as tokens (order-independent fallback)
	words := strings.Fields(phrase)
	for _, w := range words {
		if !tokens[w] {
			return false
		}
	}
	return true
}

// matchFastPath checks if the user query matches a fast path template.
// Uses token-based matching to avoid false substring matches (e.g., "in" inside "instances").
// Returns the template and a formatted query string, or empty if no match.
func matchFastPath(query string) (datadogFastPathTemplate, string) {
	queryLower := strings.ToLower(query)
	tokens, bigrams := tokenizeQuery(queryLower)
	for _, entry := range datadogFastPathTemplates {
		allMatch := true
		for _, kw := range entry.Keywords {
			if !queryContainsPhrase(kw, tokens, bigrams) {
				allMatch = false
				break
			}
		}
		if allMatch {
			t := entry.Template
			q := fmt.Sprintf("%s:%s{*} by {%s}", t.Aggregation, t.Metric, t.GroupBy)
			return t, q
		}
	}
	return datadogFastPathTemplate{}, ""
}

// suggestMetricsSearchTerms extracts search keywords from the user query to help
// the agent call metrics_list with the right filter. Returns a hint string for the prompt.
func suggestMetricsSearchTerms(query string) string {
	queryLower := strings.ToLower(query)

	// Map of user-facing terms to Datadog metric search keywords
	searchHints := map[string][]string{
		"cpu":           {"cpu"},
		"processor":     {"cpu"},
		"memory":        {"memory", "mem"},
		"ram":           {"memory"},
		"network":       {"network", "net", "bytes"},
		"traffic":       {"network", "bytes"},
		"disk":          {"disk", "volume", "ebs"},
		"storage":       {"disk", "volume", "ebs", "s3"},
		"io":            {"io", "read", "write"},
		"latency":       {"latency", "response_time", "duration"},
		"error":         {"error", "5xx", "4xx", "httpcode"},
		"request":       {"request", "httpcode", "connection"},
		"throughput":    {"throughput", "request_count", "processed"},
		"lambda":        {"lambda"},
		"ec2":           {"ec2"},
		"elb":           {"elb", "applicationelb"},
		"alb":           {"applicationelb"},
		"rds":           {"rds"},
		"s3":            {"s3"},
		"ecs":           {"ecs"},
		"nat":           {"natgateway"},
		"elasticsearch": {"es"},
		"container":     {"container"},
		"pod":           {"pod", "kubernetes", "container"},
		"deployment":    {"deployment", "kubernetes"},
		"node":          {"node", "system"},
		"host":          {"system", "host"},
		"load":          {"load"},
		"swap":          {"swap"},
		"restart":       {"restart"},
	}

	tokens, bigrams := tokenizeQuery(queryLower)

	// Sort keys for deterministic output across runs.
	keys := make([]string, 0, len(searchHints))
	for kw := range searchHints {
		keys = append(keys, kw)
	}
	sort.Strings(keys)

	var hints []string
	seen := make(map[string]bool)
	for _, kw := range keys {
		if queryContainsPhrase(kw, tokens, bigrams) {
			for _, t := range searchHints[kw] {
				if !seen[t] {
					hints = append(hints, t)
					seen[t] = true
				}
			}
		}
	}

	if len(hints) == 0 {
		return ""
	}
	return fmt.Sprintf("Suggested search keywords for `metrics_list`: %s", strings.Join(hints, ", "))
}

func init() {
	toolDescription := `Retrieves Datadog metrics (e.g., memory, CPU, network). Input should be a natural language question about metrics.`
	toolInput := "Provide metrices question in natural language"
	toolOutput := "The tool will return the datadog metrics data retrieved your query"

	core.RegisterNBAgentFactoryAndToolAndPrioritizeAgentResponseForTool(DatadogMetricsAgentName, func(accountId string) (core.NBAgent, error) {
		return DatadogMetricsAgent{accountId: accountId}, nil
	}, toolDescription, toolInput, toolOutput)
}

const DatadogMetricsAgentName = "datadog_metrics"

type DatadogMetricsAgent struct {
	accountId string
}

func NewDatadogMetricsAgent(accountId string) DatadogMetricsAgent {
	return DatadogMetricsAgent{accountId: accountId}
}

func (d DatadogMetricsAgent) GetName() string { return DatadogMetricsAgentName }

func (d DatadogMetricsAgent) GetNameAliases() []string { return []string{"Datadog Metrics"} }

func (d DatadogMetricsAgent) GetDescription() string {
	return `Uses Datadog to provide metrics based on the given question.`
}

// computeTrend analyzes a Datadog pointlist and returns a human-readable trend string.
// Compares the average of the first quarter vs last quarter of data points.
// Point values may be float64 or stringified numbers; both are handled.
func computeTrend(pointlist []any) string {
	var values []float64
	for _, p := range pointlist {
		if point, ok := p.([]any); ok && len(point) == 2 && point[1] != nil {
			switch v := point[1].(type) {
			case float64:
				values = append(values, v)
			case string:
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					values = append(values, f)
				}
			}
		}
	}
	if len(values) < 4 {
		return ""
	}

	quarter := len(values) / 4
	var firstSum, lastSum float64
	for _, v := range values[:quarter] {
		firstSum += v
	}
	for _, v := range values[len(values)-quarter:] {
		lastSum += v
	}
	firstAvg := firstSum / float64(quarter)
	lastAvg := lastSum / float64(quarter)

	if firstAvg == 0 {
		if lastAvg > 0 {
			return "Rising (from zero)"
		}
		return ""
	}
	changePct := ((lastAvg - firstAvg) / firstAvg) * 100

	switch {
	case changePct > trendRisingThreshold:
		return fmt.Sprintf("Rising (+%.1f%%)", changePct)
	case changePct < trendFallingThreshold:
		return fmt.Sprintf("Falling (%.1f%%)", changePct)
	case changePct > trendSlightlyRisingThresh:
		return fmt.Sprintf("Slightly rising (+%.1f%%)", changePct)
	case changePct < trendSlightlyFallingThresh:
		return fmt.Sprintf("Slightly falling (%.1f%%)", changePct)
	default:
		return "Stable"
	}
}

// isMultiMetricQuery returns true when the user query appears to ask for more than
// one distinct metric (e.g. "CPU and memory", "network in and out"). In that case
// the fast-path shortcut should be skipped so the planner discovers each metric.
func isMultiMetricQuery(query string) bool {
	queryLower := strings.ToLower(query)
	tokens, bigrams := tokenizeQuery(queryLower)

	// Count how many distinct fast-path templates the query matches.
	matchCount := 0
	for _, entry := range datadogFastPathTemplates {
		allMatch := true
		for _, kw := range entry.Keywords {
			if !queryContainsPhrase(kw, tokens, bigrams) {
				allMatch = false
				break
			}
		}
		if allMatch {
			matchCount++
			if matchCount >= 2 {
				return true
			}
		}
	}

	// Also catch explicit conjunction between metric-related terms even if only one
	// template matches (e.g. "CPU and disk IO" where disk IO has no template).
	metricTerms := []string{"cpu", "memory", "network", "disk", "latency", "error", "request",
		"throughput", "io", "traffic", "load", "swap", "restart"}
	if tokens["and"] || tokens["&"] {
		found := 0
		for _, term := range metricTerms {
			if queryContainsPhrase(term, tokens, bigrams) {
				found++
				if found >= 2 {
					return true
				}
			}
		}
	}
	return false
}

func (d DatadogMetricsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	searchHint := suggestMetricsSearchTerms(query.Query)
	_, fastPathQuery := matchFastPath(query.Query)

	instructions := []string{
		"**Your Primary Goal:** Answer user questions about Datadog metrics from any platform (AWS, GCP, Azure, Kubernetes, custom).",
		"**Your Method:** You MUST follow these steps to answer the user's question:",
	}

	// Only inject fast path for single-metric intents. Multi-metric queries
	// (e.g. "CPU and memory") must go through the full discovery flow so each
	// metric is handled separately.
	if fastPathQuery != "" && !isMultiMetricQuery(query.Query) {
		instructions = append(instructions,
			fmt.Sprintf("**FAST PATH (single-metric shortcut):** Based on the user's question, try executing this query directly with `datadog_metrics_execute`: `%s`. If it returns data, verify the results contain the expected grouping dimension (e.g., series scoped by the `by {tag}` tag). If the returned series lack the expected tag or the data looks incorrect, fall back to label discovery via `metrics_labels_list` and re-query with the correct tags. If it returns NO DATA, fall back to the discovery steps below. NOTE: This shortcut applies only when a single metric is requested.", fastPathQuery),
		)
	}

	instructions = append(instructions,
		"1.  **Metric Discovery:** Call `metrics_list` with a keyword from the user's question to discover available metrics. Different accounts have different metrics — discover what's available before constructing any query.",
		"    **Keyword Strategy:**",
		"    - For AWS services: use the service prefix (e.g., 'ec2', 'applicationelb', 'ebs', 'natgateway', 'lambda', 'es', 'rds', 's3')",
		"    - For Kubernetes: use the metric category (e.g., 'cpu', 'memory', 'container', 'pod', 'network')",
		"    - For system metrics: use 'system.cpu', 'system.mem', 'system.disk', 'system.net'",
		"    - If first search returns empty, try a broader keyword (e.g., 'applicationelb.request' → 'applicationelb' → 'elb')",
		"2.  **Label Discovery (Optional):** If you need to know what tags are available for filtering or grouping, call `metrics_labels_list` with the exact metric name.",
		"3.  **Execute Query:** Construct a Datadog metrics query using the discovered metric name and execute it with `datadog_metrics_execute`. Unless the user specifies a time range, limit to the last 1 hour.",
		"    **Datadog Query Syntax:**",
		"    - Basic: `<aggregation>:<metric_name>{<filter>} by {<group_by>}`",
		"    - Aggregations: `avg`, `sum`, `min`, `max`, `count`",
		"    - Filter with commas for simple AND: `{tag1:value1,tag2:value2}`",
		"    - Filter with functional operators for complex logic: `{tag1:value1 AND tag2 IN (a,b,c)}`",
		"    - NEVER mix commas with AND/OR/IN in the same filter",
		"    - Wildcard: `{pod_name:my-app*}`",
		"    - Group by: `by {host}`, `by {loadbalancer}`, `by {pod_name}`",
		"4.  **Present Results:** Interpret the raw output into a clear, human-readable summary with appropriate units.",
		"**Self-Correction:**",
		" - If `metrics_list` returns empty results, try a broader or different keyword. Example: 'applicationelb.request' returned nothing → try 'applicationelb'.",
		" - If `datadog_metrics_execute` returns no data, verify the metric name and tags are correct. Try removing filters to check if the metric has data at all: `avg:<metric>{*}`.",
		" - NEVER retry the same exact tool call. Each retry must change something: keyword, metric name, or tags.",
		" - Maximum 2 retries for any single step.",
	)

	if searchHint != "" {
		instructions = append(instructions, searchHint)
	}

	constraints := []string{
		"If a FAST PATH query is provided above, execute it directly with `datadog_metrics_execute` as your FIRST action. Otherwise, call `metrics_list` first to discover metrics. Do NOT guess metric names without a FAST PATH.",
		"You MUST use `datadog_metrics_execute` to execute the query. Do NOT answer without executing.",
		"Use `metrics_labels_list` when you need to discover tags for filtering or grouping.",
		"Do not ask for any clarification from the user, resolve using the available tools.",
		"CRITICAL: NEVER mix comma operators (,) with functional operators (AND, OR, IN, NOT IN) in the same query.",
		"For simple tag filtering, use commas: {tag1:value1,tag2:value2}.",
		"For multiple values of the same tag, use functional syntax: {tag1:value1 AND tag2 IN (val1,val2,val3)}.",
		"For wildcard matching, use asterisk: tag:value* or status_code:5*.",
		"Use 'by {tag}' for grouping, not '.by(tag)' or '.by {tag}'.",
		"Do not use array syntax: tag:[val1,val2]. Use: tag IN (val1,val2).",
		"Do not use Prometheus functions: rate(), increase(), etc. This is Datadog, not Prometheus.",
		"**Multi-metric queries:** If the user asks about multiple metrics (e.g., 'CPU and memory'), use ONE `metrics_list` call with a broad keyword, then execute SEPARATE `datadog_metrics_execute` calls for each metric. Do NOT combine unrelated metrics in a single query.",
	}

	examples := []core.NBAgentPromptExample{
		// Fast Path: EC2 CPU (skips metrics_list)
		{
			Question: "Show me the CPU utilization for EC2 instances.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolExecuteDatadogMetrics, Input: "avg:aws.ec2.cpuutilization{*} by {host}", Explanation: "FAST PATH matched — execute directly without metrics_list."},
			},
			Explanation: "When a FAST PATH query is provided, skip metrics_list and execute directly. Only 1 tool call needed.",
		},
		// AWS ALB errors
		{
			Question: "What is the 5xx error rate for my ALB?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolMetricsList, Input: "applicationelb", Explanation: "Search for ALB metrics."},
				{Tool: tools.ToolExecuteDatadogMetrics, Input: "sum:aws.applicationelb.httpcode_elb_5xx{*} by {loadbalancer}", Explanation: "Found httpcode_elb_5xx. Execute with sum aggregation grouped by loadbalancer."},
			},
			Explanation: "Use sum for count-based metrics like HTTP codes.",
		},
		// AWS ALB request count
		{
			Question: "How many requests are hitting my Application Load Balancers?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolMetricsList, Input: "applicationelb.request", Explanation: "Search for ALB request metrics."},
				{Tool: tools.ToolExecuteDatadogMetrics, Input: "sum:aws.applicationelb.request_count{*} by {loadbalancer}", Explanation: "Found request_count. Execute grouped by load balancer."},
			},
			Explanation: "Use specific keywords like 'applicationelb.request' for targeted discovery.",
		},
		// AWS EC2 Network
		{
			Question: "Show incoming network traffic for EC2 instances.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolMetricsList, Input: "ec2.network", Explanation: "Search for EC2 network metrics."},
				{Tool: tools.ToolExecuteDatadogMetrics, Input: "avg:aws.ec2.network_in{*} by {host}", Explanation: "Found network_in. Execute grouped by host."},
			},
			Explanation: "Discover metric, then execute.",
		},
		// AWS EBS
		{
			Question: "Show EBS volume read operations.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolMetricsList, Input: "ebs.volume", Explanation: "Search for EBS volume metrics."},
				{Tool: tools.ToolExecuteDatadogMetrics, Input: "avg:aws.ebs.volume_read_ops{*} by {host}", Explanation: "Found volume_read_ops. Execute grouped by host."},
			},
			Explanation: "Discover metric, then execute.",
		},
		// AWS Lambda
		{
			Question: "Show Lambda memory allocation by function.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolMetricsList, Input: "lambda", Explanation: "Search for Lambda metrics."},
				{Tool: tools.ToolExecuteDatadogMetrics, Input: "avg:aws.lambda.memorysize{*} by {functionname}", Explanation: "Found lambda.memorysize. Execute grouped by functionname."},
			},
			Explanation: "Discover metric, then execute grouped by functionname.",
		},
		// Kubernetes pod CPU with tag filtering
		{
			Question: "Show me CPU usage for pod 'my-app' in namespace 'default'.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolMetricsList, Input: "kubernetes.cpu", Explanation: "Search for Kubernetes CPU metrics."},
				{Tool: tools.ToolExecuteDatadogMetrics, Input: "avg:kubernetes.cpu.usage.total{pod_name:my-app*,kube_namespace:default} by {pod_name}", Explanation: "Found kubernetes.cpu.usage.total. Use comma operator for simple AND between two tags."},
			},
			Explanation: "Comma operator for simple tag filtering. Wildcard * matches all pods starting with 'my-app'.",
		},
		// Multi-value filtering with IN
		{
			Question: "Show memory for services api-server and cache-service in production.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolMetricsList, Input: "memory", Explanation: "Search for memory metrics."},
				{Tool: tools.ToolExecuteDatadogMetrics, Input: "avg:kubernetes.memory.usage{kube_namespace:production AND service IN (api-server,cache-service)} by {service}", Explanation: "Use AND with IN for multiple values. NEVER mix commas with AND."},
			},
			Explanation: "Uses AND with IN operator — never mix commas with functional operators.",
		},
		// Label discovery workflow
		{
			Question: "What is the Elasticsearch cluster CPU utilization?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolMetricsList, Input: "es.cpu", Explanation: "Search for Elasticsearch CPU metrics."},
				{Tool: tools.ToolMetricsLabelsList, Input: "aws.es.cpuutilization", Explanation: "Discover labels to find the right grouping dimension."},
				{Tool: tools.ToolExecuteDatadogMetrics, Input: "avg:aws.es.cpuutilization{*} by {domain_name}", Explanation: "Labels showed 'domain_name'. Execute grouped by domain."},
			},
			Explanation: "When you don't know the grouping tag, use metrics_labels_list to discover available labels.",
		},
		// NAT Gateway
		{
			Question: "How many active connections on my NAT Gateways?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolMetricsList, Input: "natgateway", Explanation: "Search for NAT Gateway metrics."},
				{Tool: tools.ToolExecuteDatadogMetrics, Input: "avg:aws.natgateway.active_connection_count{*} by {name}", Explanation: "Found active_connection_count. Execute grouped by name."},
			},
			Explanation: "Discover metric, then execute.",
		},
		// Multi-metric query
		{
			Question: "Show me CPU and network traffic for EC2 instances.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolMetricsList, Input: "ec2", Explanation: "Use broad keyword 'ec2' to discover both CPU and network metrics in one call."},
				{Tool: tools.ToolExecuteDatadogMetrics, Input: "avg:aws.ec2.cpuutilization{*} by {host}", Explanation: "Execute CPU query."},
				{Tool: tools.ToolExecuteDatadogMetrics, Input: "avg:aws.ec2.network_in{*} by {host}", Explanation: "Execute network query separately."},
			},
			Explanation: "For multi-metric requests, use ONE broad metrics_list call, then execute each metric separately.",
		},
	}

	toolUsage := map[string][]string{
		tools.ToolMetricsList: {
			"PRIMARY discovery tool. MUST be called FIRST before any query.",
			"Input: (required) keyword to search for metrics. Use service prefix for AWS (e.g., 'ec2', 'applicationelb', 'ebs'), metric category for K8s (e.g., 'cpu', 'memory').",
			"Output: List of matching metric names. Pick the most relevant one for your query.",
			"If results are empty, try a broader keyword.",
		},
		tools.ToolMetricsLabelsList: {
			"OPTIONAL: Fetches available tags/labels for a specific metric. Use when you need to know what dimensions are available for filtering or grouping.",
			"Input: (required) exact metric name (e.g., 'aws.ec2.cpuutilization').",
			"Output: List of label names (e.g., host, loadbalancer, availability-zone).",
		},
		tools.ToolExecuteDatadogMetrics: {
			"Executes a Datadog metrics query. Call AFTER discovering the metric name via metrics_list.",
			"Input: A bare Datadog metrics query string (e.g., `avg:aws.ec2.cpuutilization{*} by {host}`). Do NOT append CLI-style flags like `--start-time` / `--end-time` to the query — Datadog rejects them.",
			"To specify a time window, provide a JSON object with `command`, `start_time`, and `end_time` fields: `{\"command\": \"<query>\", \"start_time\": \"<ISO8601>\", \"end_time\": \"<ISO8601>\"}`.",
			"WRONG (Datadog will reject): `avg:aws.ec2.cpuutilization{*} --start-time 2026-04-28T03:44:44Z --end-time 2026-04-28T03:59:44Z`",
			"WRONG (Datadog will reject): `avg:aws.ec2.cpuutilization{*} from=2026-04-28T03:44:44Z to=2026-04-28T03:59:44Z`",
			"RIGHT (no time window, defaults to last 2h): `avg:aws.ec2.cpuutilization{*} by {host}`",
			"RIGHT (with time window): `{\"command\": \"avg:aws.ec2.cpuutilization{*} by {host}\", \"start_time\": \"2026-04-28T03:44:44Z\", \"end_time\": \"2026-04-28T03:59:44Z\"}`",
			"Output: JSON with series data including pointlist and stats (min, max, avg, p99).",
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in Datadog metrics analysis across all platforms (AWS, GCP, Azure, Kubernetes, custom)",
		Instructions: instructions,
		Constraints:  constraints,
		Examples:     examples,
		ToolUsage:    toolUsage,
		OutputFormat: "Markdown. Present metric values with appropriate units (bytes → MB/GB, percentage, requests/sec). Identify critical insights, trends, and anomalies. Do NOT include internal tool call flow or raw JSON in the final answer.",
	}
}

func (d DatadogMetricsAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{
		tools.MetricsListTool{Provider: "datadog"},
		tools.ListMetricsLabelsTool{Provider: "datadog"},
		tools.DatadogMetricsExecuteTool{},
	}
}

func (d DatadogMetricsAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// GetMaxIterations caps the ReAct loop at 6 steps.
// Typical flow: (1) metrics_list → (2) construct query + execute → (3) present results = 3 iterations.
// With label discovery: (1) metrics_list → (2) metrics_labels_list → (3) execute → (4) present = 4 iterations.
// With retry: adds 1-2 more iterations. 6 is sufficient.
func (d DatadogMetricsAgent) GetMaxIterations() int {
	return 6
}

// CritiqueEnabled disables the critique loop to save LLM calls.
func (d DatadogMetricsAgent) CritiqueEnabled() bool {
	return false
}

func (d DatadogMetricsAgent) UpdateExecutorLlmResponse(actions []core.NBAgentPlannerToolAction, finished *core.NBAgentPlannerFinishAction, err error) ([]core.NBAgentPlannerToolAction, *core.NBAgentPlannerFinishAction, error) {
	return actions, finished, err
}

func (d DatadogMetricsAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	// Cap metrics_list and metrics_labels_list responses to prevent context bloat.
	// Truncate at the last newline before the limit so metric names are not cut mid-line.
	if strings.EqualFold(toolRequest.Tool, tools.ToolMetricsList) || strings.EqualFold(toolRequest.Tool, tools.ToolMetricsLabelsList) {
		maxChars := getMaxDatadogMetricsToolResponseChars()
		if len(toolResponse) > maxChars {
			cutoff := strings.LastIndex(toolResponse[:maxChars], "\n")
			if cutoff <= 0 {
				cutoff = maxChars // no newline found, fall back to hard cut
			}
			return toolResponse[:cutoff] + "\n... (truncated - use a more specific keyword to narrow results)"
		}
		return toolResponse
	}

	if strings.EqualFold(toolRequest.Tool, tools.ToolExecuteDatadogMetrics) {
		resultsMap := map[string]any{}
		if err := common.UnmarshalJson([]byte(toolResponse), &resultsMap); err == nil {
			var overallDataResponseBuilder strings.Builder
			originalQuery, _ := resultsMap["query"].(string)

			seriesData, ok := resultsMap["series"].([]any)
			if !ok || len(seriesData) == 0 {
				overallDataResponseBuilder.WriteString("\nNo series data found in the response.")
			} else {
				seriesToSummarize := seriesData
				if len(seriesData) > maxSeriesToSummarize {
					seriesToSummarize = seriesData[:maxSeriesToSummarize]
					fmt.Fprintf(&overallDataResponseBuilder, "\nDisplaying summary for the first %d series out of %d.", len(seriesToSummarize), len(seriesData))
				}

				for i, seriesItemAny := range seriesToSummarize {
					seriesItem, ok := seriesItemAny.(map[string]any)
					if !ok {
						continue
					}

					scope := "N/A"
					if s, ok := seriesItem["scope"].(string); ok && s != "" {
						scope = s
					}

					metricIdentifier := "N/A"
					if dn, ok := seriesItem["display_name"].(string); ok && dn != "" {
						metricIdentifier = fmt.Sprintf("Display Name: %s", dn)
					} else if mn, ok := seriesItem["metric"].(string); ok && mn != "" {
						metricIdentifier = fmt.Sprintf("Metric: %s", mn)
					}

					statsStr := "Not available"
					if statsInterface, ok := seriesItem["stats"]; ok {
						statsBytes, err := common.MarshalJson(statsInterface)
						if err == nil {
							statsStr = string(statsBytes)
						}
					}

					// Compute trend direction from pointlist
					trendStr := ""
					if pointlistAny, ok := seriesItem["pointlist"].([]any); ok && len(pointlistAny) >= 2 {
						trendStr = computeTrend(pointlistAny)
					}

					fmt.Fprintf(&overallDataResponseBuilder, "\n\n--- Series %d ---", i+1)
					fmt.Fprintf(&overallDataResponseBuilder, "\n  **%s**", metricIdentifier)
					fmt.Fprintf(&overallDataResponseBuilder, "\n  **Scope (Tags)**: %s", scope)
					fmt.Fprintf(&overallDataResponseBuilder, "\n  **Stats Summary**: %s", statsStr)
					if trendStr != "" {
						fmt.Fprintf(&overallDataResponseBuilder, "\n  **Trend**: %s", trendStr)
					}
				}
			}

			if overallDataResponseBuilder.Len() == 0 {
				overallDataResponseBuilder.WriteString("\nNo data found or unable to parse series.")
			}
			return fmt.Sprintf("**Query**: `%s`\n**Response**:%s", originalQuery, overallDataResponseBuilder.String())
		}
	}
	return toolResponse
}
