package observability

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"nudgebee/services/security"
)

// Elastic Agent field paths for the per-container memory picture this card needs. As
// elsewhere in the ES metric mapping, the field path IS the metric identity, so an
// `exists` on the value field selects the right metricset without a term on
// `metricset.name` (which a customer template may map as `text`, where a term matches
// nothing).
const (
	esNNContainerWorkingSet = "kubernetes.container.memory.workingset.bytes"
	esNNContainerRequest    = "kubernetes.container.memory.request.bytes"
	esNNContainerLimit      = "kubernetes.container.memory.limit.bytes"
	esNNNodeAllocatable     = "kubernetes.node.memory.allocatable.bytes"
)

// esNoisyNeighbours builds the noisy-neighbours payload from Elasticsearch.
//
// The PromQL form needs five queries and a three-way guess at where the node name lands
// (`node`, `instance`, or both, depending on the scrape config). ECS has one spelling of
// the node name, so this is two searches: the per-container picture on the node, and the
// node's allocatable memory.
//
// Requests and limits are a SECOND search, keyed by the pods the first one found.
// They live in the state_container metricset, which carries no node field — verified
// against a live cluster, where asking for them alongside working set returns null for
// every container. Since that metricset cannot be narrowed by node, the pods on the node
// are resolved first and used as the filter, which is both correct and bounded.
func esNoisyNeighbours(ctx *security.RequestContext, accountID, nodeName string, lookbackMinutes, topN int) (*esNoisyNeighbourData, error) {
	// Guarded here rather than at the call site so every caller is covered: without a
	// node there is nothing to narrow by, and querying anyway would return the whole
	// cluster's containers as though they were this node's.
	if nodeName == "" {
		return nil, errors.New("noisy neighbours: node name required")
	}
	cfg, err := GetElasticsearchConfig(ctx, accountID)
	if err != nil {
		return nil, err
	}
	index := "metrics-*"
	if cfg.MetricsIndex != "" {
		index = cfg.MetricsIndex
	}

	timeRange := map[string]any{
		"range": map[string]any{
			"@timestamp": map[string]any{"gte": fmt.Sprintf("now-%dm", lookbackMinutes), "lte": "now"},
		},
	}
	nodeClause := esNodeNameClause("kubernetes.node.name", nodeName)

	containerFilters := []any{
		map[string]any{"exists": map[string]any{"field": esNNContainerWorkingSet}},
		timeRange,
	}
	if nodeClause != nil {
		containerFilters = append(containerFilters, nodeClause)
	}

	// max, not avg: this card answers "how much is this container holding", and an
	// average across the lookback understates a container that just grew — which is
	// exactly the one that pushed its neighbour into an OOM.
	body := map[string]any{
		"size":  0,
		"query": map[string]any{"bool": map[string]any{"filter": containerFilters}},
		"aggs": map[string]any{
			"pods": map[string]any{
				"terms": map[string]any{"field": "kubernetes.pod.name", "size": esUtilTermsSize},
				"aggs": map[string]any{
					"ns": map[string]any{"terms": map[string]any{"field": "kubernetes.namespace", "size": 1}},
					"containers": map[string]any{
						"terms": map[string]any{"field": "kubernetes.container.name", "size": esUtilTermsSize},
						"aggs": map[string]any{
							"used": map[string]any{"max": map[string]any{"field": esNNContainerWorkingSet}},
						},
					},
				},
			},
		},
	}

	var resp esNNResponse
	if err := esSearchInto(ctx, cfg, index, body, &resp); err != nil {
		return nil, err
	}

	// The pods this card found on the node ARE the join key for the spec lookup, so it
	// reuses them rather than issuing esPodsOnNode's query a second time.
	podNames := make([]string, 0, len(resp.Aggregations.Pods.Buckets))
	for _, pod := range resp.Aggregations.Pods.Buckets {
		podNames = append(podNames, pod.Key)
	}
	specs, err := esContainerMemorySpecs(ctx, cfg, index, podNames, timeRange)
	if err != nil {
		// Usage alone still renders the rows; the card shows the request column empty.
		ctx.GetLogger().Warn("noisy_neighbours: container requests/limits unavailable",
			"account_id", accountID, "node", nodeName, "err", err)
		specs = map[string]esContainerSpec{}
	}

	// Neighbours starts empty rather than nil: a nil slice marshals to `null`, and the
	// card iterates it. An idle node legitimately has nothing to show and must render as
	// an empty list, not fail.
	out := &esNoisyNeighbourData{NodeName: nodeName, Neighbours: []map[string]any{}}
	for _, pod := range resp.Aggregations.Pods.Buckets {
		namespace := ""
		if len(pod.NS.Buckets) > 0 {
			namespace = pod.NS.Buckets[0].Key
		}
		for _, c := range pod.Containers.Buckets {
			if c.Used.Value == nil {
				continue
			}
			used := *c.Used.Value
			spec := specs[pod.Key+"/"+c.Key]
			out.NodeUsed += used
			out.Neighbours = append(out.Neighbours, map[string]any{
				"name":             c.Key,
				"pod_name":         pod.Key,
				"namespace":        namespace,
				"node_name":        nodeName,
				"memory_used":      used,
				"memory_requested": spec.Requested,
				"memory_limit":     spec.Limit,
			})
			out.TotalRequested += spec.Requested
		}
	}

	// The node total is summed over every container before the top-N cut, so the
	// gauge still reflects the whole node when only 15 rows are shown.
	sort.SliceStable(out.Neighbours, func(i, j int) bool {
		vi, _ := out.Neighbours[i]["memory_used"].(float64)
		vj, _ := out.Neighbours[j]["memory_used"].(float64)
		return vi > vj
	})
	if len(out.Neighbours) > topN {
		out.Neighbours = out.Neighbours[:topN]
	}

	if alloc, err := esNodeAllocatableMemory(ctx, cfg, index, nodeClause, timeRange); err == nil {
		out.NodeAllocatable = alloc
	} else {
		// A missing allocatable leaves the gauge without its denominator, which the
		// card renders as an empty bar. Worth saying why rather than failing the whole
		// enricher — the neighbour rows above are still useful on their own.
		ctx.GetLogger().Warn("noisy_neighbours: node allocatable memory unavailable",
			"account_id", accountID, "node", nodeName, "err", err)
	}
	return out, nil
}

// esContainerMemorySpecs reads requests and limits for named pods, keyed pod/container.
//
// Same shape as the node-scope pod join in the utilisation path (esPodsOnNode): the
// state_container metricset holds these two numbers and carries no node field, so the
// only way to ask "on this node" is to name the pods. This one takes the pod list as an
// argument because the caller already has it.
func esContainerMemorySpecs(ctx *security.RequestContext, cfg *ElasticsearchConfig, index string, podNames []string, timeRange map[string]any) (map[string]esContainerSpec, error) {
	out := map[string]esContainerSpec{}
	if len(podNames) == 0 {
		return out, nil
	}
	body := map[string]any{
		"size": 0,
		"query": map[string]any{"bool": map[string]any{"filter": []any{
			map[string]any{"exists": map[string]any{"field": esNNContainerRequest}},
			timeRange,
			map[string]any{"terms": map[string]any{"kubernetes.pod.name": podNames}},
		}}},
		"aggs": map[string]any{
			"pods": map[string]any{
				"terms": map[string]any{"field": "kubernetes.pod.name", "size": esUtilTermsSize},
				"aggs": map[string]any{
					"containers": map[string]any{
						"terms": map[string]any{"field": "kubernetes.container.name", "size": esUtilTermsSize},
						"aggs": map[string]any{
							"requested": map[string]any{"max": map[string]any{"field": esNNContainerRequest}},
							"limit":     map[string]any{"max": map[string]any{"field": esNNContainerLimit}},
						},
					},
				},
			},
		},
	}
	var resp esNNSpecResponse
	if err := esSearchInto(ctx, cfg, index, body, &resp); err != nil {
		return nil, err
	}
	for _, pod := range resp.Aggregations.Pods.Buckets {
		for _, c := range pod.Containers.Buckets {
			out[pod.Key+"/"+c.Key] = esContainerSpec{
				Requested: esNNValue(c.Requested.Value),
				Limit:     esNNValue(c.Limit.Value),
			}
		}
	}
	return out, nil
}

type esContainerSpec struct {
	Requested float64
	Limit     float64
}

func esNodeAllocatableMemory(ctx *security.RequestContext, cfg *ElasticsearchConfig, index string, nodeClause map[string]any, timeRange map[string]any) (float64, error) {
	filters := []any{
		map[string]any{"exists": map[string]any{"field": esNNNodeAllocatable}},
		timeRange,
	}
	if nodeClause != nil {
		filters = append(filters, nodeClause)
	}
	body := map[string]any{
		"size":  0,
		"query": map[string]any{"bool": map[string]any{"filter": filters}},
		"aggs": map[string]any{
			"alloc": map[string]any{"max": map[string]any{"field": esNNNodeAllocatable}},
		},
	}
	var resp struct {
		Aggregations struct {
			Alloc struct {
				Value *float64 `json:"value"`
			} `json:"alloc"`
		} `json:"aggregations"`
	}
	if err := esSearchInto(ctx, cfg, index, body, &resp); err != nil {
		return 0, err
	}
	return esNNValue(resp.Aggregations.Alloc.Value), nil
}

// esSearchInto posts one _search and decodes it, so the two queries above do not each
// repeat the request/read/unmarshal dance.
func esSearchInto(ctx *security.RequestContext, cfg *ElasticsearchConfig, index string, body map[string]any, into any) error {
	url := fmt.Sprintf("%s/%s/_search", cfg.Url, index)
	resp, err := esRequestJSON("POST", url, body, cfg) //nolint:bodyclose
	if err != nil {
		return err
	}
	raw, err := readResponse(resp, "noisy neighbours query")
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}

func esNNValue(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

type esNoisyNeighbourData struct {
	NodeName        string
	NodeUsed        float64
	NodeAllocatable float64
	TotalRequested  float64
	Neighbours      []map[string]any
}

type esNNResponse struct {
	Aggregations struct {
		Pods struct {
			Buckets []struct {
				Key string `json:"key"`
				NS  struct {
					Buckets []struct {
						Key string `json:"key"`
					} `json:"buckets"`
				} `json:"ns"`
				Containers struct {
					Buckets []struct {
						Key  string `json:"key"`
						Used struct {
							Value *float64 `json:"value"`
						} `json:"used"`
					} `json:"buckets"`
				} `json:"containers"`
			} `json:"buckets"`
		} `json:"pods"`
	} `json:"aggregations"`
}

type esNNSpecResponse struct {
	Aggregations struct {
		Pods struct {
			Buckets []struct {
				Key        string `json:"key"`
				Containers struct {
					Buckets []struct {
						Key       string `json:"key"`
						Requested struct {
							Value *float64 `json:"value"`
						} `json:"requested"`
						Limit struct {
							Value *float64 `json:"value"`
						} `json:"limit"`
					} `json:"buckets"`
				} `json:"containers"`
			} `json:"buckets"`
		} `json:"pods"`
	} `json:"aggregations"`
}
