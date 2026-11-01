package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
	"strings"
	"time"
)

// ToolMetricsSeriesMatch discovers which metric families actually exist for a workload,
// so the agent fills error-rate/latency/throughput from real data instead of guessing
// default templates and reporting N/A when instrumentation differs.
const ToolMetricsSeriesMatch = "metrics_series_match"

func init() {
	core.RegisterNBToolFactory(ToolMetricsSeriesMatch, func(accountId string) (core.NBTool, error) {
		// Same resolution the agents do (MetricsDiscoveryProvider). Agents that take this
		// tool from the registry rather than through PrometheusAgent.GetSupportedTools —
		// the k8s orchestrator among them — otherwise send a hardcoded "prometheus",
		// which api-server honours verbatim and routes to the agent relay.
		return MetricsSeriesMatchTool{Provider: MetricsDiscoveryProvider(accountId)}, nil
	})
}

type MetricsSeriesMatchTool struct {
	Provider string
}

func (m MetricsSeriesMatchTool) Name() string { return ToolMetricsSeriesMatch }

func (m MetricsSeriesMatchTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (m MetricsSeriesMatchTool) Description() string {
	filter, _ := m.prefixFilter()
	return `Discovers the metric families that ACTUALLY have series for a specific workload, grouped by the labels that identify the workload.
		Usage:
		* Input: workload (required, e.g. deployment/service name) and namespace (optional but recommended).
		* Output: groups of {namespace_label, workload_label, match_type, families[]}. A workload may emit under several layers (e.g. eBPF vs scraped) with different labels.

		Purpose: Call this when a templated query returns no data for a known workload. Pick a family from the result and filter it by the namespace_label/workload_label reported with it (match_type "prefix" => use ` + filter + `). Do NOT reuse labels from one group on a family from another.`
}

// prefixFilter is how a "prefix" group is filtered, in the query language the account's
// metrics tool speaks, plus an example of it. Pod names carry a replica-hash suffix, so a
// prefix group is only reachable through a wildcard, and the two languages spell it
// differently: mstats WHERE has no regex operator, and PromQL has no bare `*`. Handing a
// Splunk agent the PromQL form sent it to a filter its SPL tool cannot run.
func (m MetricsSeriesMatchTool) prefixFilter() (filter, example string) {
	if strings.EqualFold(m.Provider, splunkEnterpriseMetricProvider) {
		return `<workload_label>="<workload>*"`, `k8s.pod.name="<workload>*"`
	}
	return `=~"<workload>.*"`, "pod"
}

func (m MetricsSeriesMatchTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"workload": {
				Type:        core.ToolSchemaTypeString,
				Description: "Workload name (deployment/statefulset/service) to discover metrics for (required).",
			},
			"namespace": {
				Type:        core.ToolSchemaTypeString,
				Description: "Kubernetes namespace of the workload (optional but strongly recommended to bound the lookup).",
			},
		},
		Required: []string{"workload"},
	}
}

// seriesMatchCacheTTL returns the configured TTL for the series-match discovery cache.
// Configurable via llm_server_agent_series_match_cache_ttl_minutes (default 30m): a
// workload's set of series changes far slower than metric values.
func seriesMatchCacheTTL() time.Duration {
	if v := config.Config.LlmServerAgentSeriesMatchCacheTTLMinutes; v > 0 {
		return time.Duration(v) * time.Minute
	}
	return 30 * time.Minute
}

func (m MetricsSeriesMatchTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	originAccountID := nbRequestContext.AccountId
	nbRequestContext = nbRequestContext.ForTarget()
	if nbRequestContext.AccountId != originAccountID {
		// The provider was resolved for the conversation's account when the
		// tool was built; the target may use a different metrics backend.
		m.Provider = MetricsDiscoveryProvider(nbRequestContext.AccountId)
	}
	workload := strings.TrimSpace(argString(input, "workload"))
	if workload == "" {
		// Fall back to a bare command (some planners pass the single required arg there).
		workload = strings.TrimSpace(input.Command)
	}
	namespace := strings.TrimSpace(argString(input, "namespace"))

	if workload == "" {
		return core.NBToolResponse{
			Data:   "Error: 'workload' is required. Provide the deployment/service name to discover its metrics.",
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	// Fail fast: the tool dereferences Ctx (logging) and AccountId is tenant-scoping for
	// the downstream lookup — guard both before any cache access or service call.
	if nbRequestContext.Ctx == nil {
		return core.NBToolResponse{
			Data:   "Error: request context is missing.",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("metrics_series_match: request context is nil")
	}
	if nbRequestContext.AccountId == "" {
		return core.NBToolResponse{
			Data:   "Error: account ID is missing.",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("metrics_series_match: account ID is empty")
	}

	provider := "prometheus"
	if m.Provider != "" {
		provider = m.Provider
	}

	cacheKey := "series:" + nbRequestContext.AccountId + ":" + provider + ":" + namespace + ":" + workload
	if cached, ok := metricsListCache.Load(cacheKey); ok {
		entry := cached.(metricsListCacheEntry)
		if time.Now().Before(entry.expiry) {
			nbRequestContext.Ctx.GetLogger().Info("metrics: cache hit for metrics_series_match", "workload", workload, "namespace", namespace)
			return entry.response, nil
		}
		metricsListCache.Delete(cacheKey)
	}

	resp, err := services_server.ListMetricsSeriesMatch(*nbRequestContext.Ctx, nbRequestContext.AccountId, provider, namespace, workload, 0, 0)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("metrics: metrics_series_match failed", "error", err.Error())
		return core.NBToolResponse{
			Data:   err.Error(),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	if len(resp.Matches) == 0 {
		result := seriesMatchEmptyResponse(workload, namespace, resp.Truncated)
		// A lookup that searched every convention and found nothing is as settled as one
		// that found families, so it is cached the same way; asking again only re-runs the
		// whole sweep for the same answer. A cut-short one is not an answer and is not kept.
		if !resp.Truncated {
			metricsListCache.Store(cacheKey, metricsListCacheEntry{response: result, expiry: time.Now().Add(seriesMatchCacheTTL())})
		}
		return result, nil
	}

	filter, example := m.prefixFilter()
	out := map[string]any{
		"workload":  workload,
		"namespace": namespace,
		"matches":   resp.Matches,
		"truncated": resp.Truncated,
		"note": fmt.Sprintf("Pick a family from a group's 'families' and filter it by THAT group's namespace_label and workload_label. match_type \"prefix\" => use %s (e.g. %s). Do not mix labels across groups.",
			filter, example),
	}
	data, err := common.MarshalJson(out)
	if err != nil {
		return core.NBToolResponse{Data: "", Status: core.NBToolResponseStatusError}, err
	}

	result := core.NBToolResponse{
		Data:   string(data),
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusSuccess,
	}
	metricsListCache.Store(cacheKey, metricsListCacheEntry{response: result, expiry: time.Now().Add(seriesMatchCacheTTL())})
	return result, nil
}

// seriesMatchEmptyResponse phrases a match-less lookup. Whether the backend finished
// looking is the whole difference: a completed search that found nothing is evidence the
// workload is not emitting, whereas one cut short by a limit or a deadline is no evidence
// at all, and an agent told "no metrics" acts on it either way.
func seriesMatchEmptyResponse(workload, namespace string, truncated bool) core.NBToolResponse {
	msg := fmt.Sprintf("No metric series found for workload %q in namespace %q. The workload may not be emitting metrics, the name/namespace may be wrong, or there is no data in the recent window.", workload, namespace)
	if truncated {
		msg = fmt.Sprintf("Inconclusive: the metric catalog lookup for workload %q in namespace %q stopped before it had searched everywhere, and nothing matched in the part it did search. Do NOT conclude the workload has no metrics. Narrow the namespace to shrink the search, or fall back to keyword search with metrics_list.", workload, namespace)
	}
	return core.NBToolResponse{
		Data:   msg,
		Type:   core.NBToolResponseTypeText,
		Status: core.NBToolResponseStatusSuccess,
	}
}

// argString reads a string argument from the tool call, tolerating non-string scalars.
func argString(input core.NBToolCallRequest, key string) string {
	if input.Arguments == nil {
		return ""
	}
	v, ok := input.Arguments[key]
	if !ok {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	case fmt.Stringer:
		return s.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}
