package observability

// Elastic APM dependency reads for the knowledge-graph "elastic-apm" flow source.
//
// These are the reads Kibana's Dependencies page runs, issued as plain Elasticsearch
// aggregations so they work on a Basic licence (Kibana's service map needs Platinum):
//
//  1. Dependencies — metrics-apm* service_destination metricsets: one row per
//     (service.name, span.destination.service.resource) with call count, latency
//     sum and failures. Pre-aggregated and sampling-corrected, so it is the source
//     of the edge metrics. It carries NO kubernetes.* fields, which is why the
//     other three reads exist.
//  2. Call samples — exit spans grouped by (service.name,
//     kubernetes.namespace, kubernetes.pod.name, resource), a few span ids each.
//     Grouping by pod makes every calling pod visible: one service.name is often
//     reported by several workloads, and a per-service sample would show only one.
//  3. Receivers — transactions whose parent.id is one of those span
//     ids: the receiving service, namespace and pod. This is what turns
//     "product-catalog:8080" or a bare IP into the workload that served the call.
//  4. Service pods — transactions grouped by (service.name,
//     kubernetes.namespace, kubernetes.pod.name), exhaustive over the dependency
//     window, so each service can be attributed to the cluster(s) it runs in.
//
// Reads 2-4 use the integration's configured trace index (traces-apm* when unset),
// the same index the Traces tab reads for the elastic_apm layout. No setting
// declares the metrics index, so read 1 uses Elastic's default. Field names
// verified against APM Server 8.19 on the dev cluster.

import (
	"encoding/json"
	"fmt"
	"time"

	"nudgebee/services/security"
)

const (
	elasticAPMMetricsIndex = "metrics-apm*"

	// elasticAPMCompositePageSize is one composite page; elasticAPMMaxBuckets caps a
	// paged read so a pathological cardinality cannot page unbounded.
	elasticAPMCompositePageSize = 1000
	elasticAPMMaxBuckets        = 10000
	// elasticAPMSpansPerBucket is the span ids kept per (caller pod, resource); a
	// few are enough to find the receiver, and they bound read 3's id list.
	elasticAPMSpansPerBucket = 5
)

// elasticAPMReceiverBatchSize caps the span ids sent in one receiver request
// (read 3). A variable so tests can exercise the batching.
var elasticAPMReceiverBatchSize = 5000

// ElasticAPMDependency is one read-1 row: a service calling a dependency, keyed by
// service.name only (the metricset has no namespace or pod).
type ElasticAPMDependency struct {
	CallerService string
	// Resource is span.destination.service.resource verbatim, including the
	// ":port" APM Server appends ("product-catalog:8080").
	Resource     string
	TargetType   string // service.target.type: grpc, http, redis, kafka, ...
	TargetName   string // service.target.name, when the agent reports one
	Calls        int64
	Failures     int64
	LatencySumUs float64
}

// ElasticAPMCall is one observed call from read 2, joined to its receiver by
// read 3. Receiver fields are empty when no instrumented receiver was found.
type ElasticAPMCall struct {
	CallerService     string
	CallerNamespace   string
	CallerPod         string
	Resource          string
	ReceiverService   string
	ReceiverNamespace string
	ReceiverPod       string
}

// ElasticAPMServicePod is one read-4 row: a pod a service ran in, and when it
// last reported. LastSeen lets a caller tell the pods running now from pods a
// rollout retired earlier in the window.
type ElasticAPMServicePod struct {
	Service   string
	Namespace string
	Pod       string
	LastSeen  time.Time
}

// ElasticAPMDependencies is everything one integration's reads return.
type ElasticAPMDependencies struct {
	Dependencies []ElasticAPMDependency
	Calls        []ElasticAPMCall
	Pods         []ElasticAPMServicePod
}

// FetchElasticAPMDependencies runs the four reads against one integration. depStart..end
// bounds reads 1 and 4 (cheap aggregates); sampleStart..end bounds read 2, which
// touches raw span documents. Returns (nil, nil) when the cluster has no
// service_destination data — the probe that decides whether the account uses
// Elastic APM at all.
func FetchElasticAPMDependencies(
	ctx *security.RequestContext,
	cfg *ElasticsearchConfig,
	depStart, sampleStart, end time.Time,
) (*ElasticAPMDependencies, error) {
	deps, err := fetchElasticAPMDestinations(ctx, cfg, depStart, end)
	if err != nil {
		return nil, err
	}
	if len(deps) == 0 {
		return nil, nil
	}
	calls, err := fetchElasticAPMCalls(ctx, cfg, sampleStart, end)
	if err != nil {
		return nil, err
	}
	pods, err := fetchElasticAPMServicePods(ctx, cfg, depStart, end)
	if err != nil {
		return nil, err
	}
	return &ElasticAPMDependencies{Dependencies: deps, Calls: calls, Pods: pods}, nil
}

// elasticAPMBaseFilter is the time range every read shares.
func elasticAPMBaseFilter(start, end time.Time) []map[string]any {
	return []map[string]any{
		{"range": map[string]any{"@timestamp": map[string]any{
			"gte":    start.UTC().Format(time.RFC3339),
			"lte":    end.UTC().Format(time.RFC3339),
			"format": "strict_date_optional_time",
		}}},
	}
}

// elasticAPMDestinationsQuery builds read 1. The 10m/60m rollups (APM Server 8.7+)
// are excluded so a pair is not counted three times; pre-8.7 documents have no
// metricset.interval and pass the must_not.
func elasticAPMDestinationsQuery(start, end time.Time) (map[string]any, []map[string]any, map[string]any) {
	filter := append(elasticAPMBaseFilter(start, end),
		map[string]any{"term": map[string]any{"metricset.name": "service_destination"}})
	query := map[string]any{"bool": map[string]any{
		"filter":   filter,
		"must_not": []map[string]any{{"terms": map[string]any{"metricset.interval": []string{"10m", "60m"}}}},
	}}
	sources := []map[string]any{
		{"service": map[string]any{"terms": map[string]any{"field": "service.name"}}},
		{"resource": map[string]any{"terms": map[string]any{"field": "span.destination.service.resource"}}},
	}
	subAggs := map[string]any{
		"calls":       map[string]any{"sum": map[string]any{"field": "span.destination.service.response_time.count"}},
		"latency_sum": map[string]any{"sum": map[string]any{"field": "span.destination.service.response_time.sum.us"}},
		"failures": map[string]any{
			"filter": map[string]any{"term": map[string]any{"event.outcome": "failure"}},
			"aggs": map[string]any{
				"calls": map[string]any{"sum": map[string]any{"field": "span.destination.service.response_time.count"}},
			},
		},
		"target_type": map[string]any{"terms": map[string]any{"field": "service.target.type", "size": 1}},
		"target_name": map[string]any{"terms": map[string]any{"field": "service.target.name", "size": 1}},
	}
	return query, sources, subAggs
}

func fetchElasticAPMDestinations(ctx *security.RequestContext, cfg *ElasticsearchConfig, start, end time.Time) ([]ElasticAPMDependency, error) {
	query, sources, subAggs := elasticAPMDestinationsQuery(start, end)
	buckets, err := esCompositeAll(ctx, cfg, esSearchURL(cfg, elasticAPMMetricsIndex), query, sources, subAggs,
		elasticAPMCompositePageSize, elasticAPMMaxBuckets, "elastic apm dependencies")
	if err != nil {
		return nil, err
	}
	return parseElasticAPMDestinations(buckets), nil
}

func parseElasticAPMDestinations(buckets []json.RawMessage) []ElasticAPMDependency {
	out := make([]ElasticAPMDependency, 0, len(buckets))
	for _, raw := range buckets {
		var b struct {
			Key struct {
				Service  string `json:"service"`
				Resource string `json:"resource"`
			} `json:"key"`
			Calls      esValue `json:"calls"`
			LatencySum esValue `json:"latency_sum"`
			Failures   struct {
				Calls esValue `json:"calls"`
			} `json:"failures"`
			TargetType esTermsTop `json:"target_type"`
			TargetName esTermsTop `json:"target_name"`
		}
		if err := json.Unmarshal(raw, &b); err != nil || b.Key.Service == "" || b.Key.Resource == "" {
			continue
		}
		out = append(out, ElasticAPMDependency{
			CallerService: b.Key.Service,
			Resource:      b.Key.Resource,
			TargetType:    b.TargetType.top(),
			TargetName:    b.TargetName.top(),
			Calls:         int64(b.Calls.Value),
			Failures:      int64(b.Failures.Calls.Value),
			LatencySumUs:  b.LatencySum.Value,
		})
	}
	return out
}

// elasticAPMCallSamplesQuery builds read 2: exit spans grouped by the CALLER's
// namespace and pod, so the same service name in two namespaces, or in two
// workloads, yields separate rows.
func elasticAPMCallSamplesQuery(start, end time.Time) (map[string]any, []map[string]any, map[string]any) {
	filter := append(elasticAPMBaseFilter(start, end),
		map[string]any{"exists": map[string]any{"field": "span.destination.service.resource"}})
	query := map[string]any{"bool": map[string]any{"filter": filter}}
	sources := []map[string]any{
		{"service": map[string]any{"terms": map[string]any{"field": "service.name"}}},
		{"namespace": map[string]any{"terms": map[string]any{"field": "kubernetes.namespace", "missing_bucket": true}}},
		{"pod": map[string]any{"terms": map[string]any{"field": "kubernetes.pod.name", "missing_bucket": true}}},
		{"resource": map[string]any{"terms": map[string]any{"field": "span.destination.service.resource"}}},
	}
	subAggs := map[string]any{
		"samples": map[string]any{"top_hits": map[string]any{
			"size":    elasticAPMSpansPerBucket,
			"_source": []string{"span.id"},
			"sort":    []map[string]any{{"@timestamp": map[string]any{"order": "desc"}}},
		}},
	}
	return query, sources, subAggs
}

// elasticAPMReceiversQuery builds read 3 for the given exit-span ids.
func elasticAPMReceiversQuery(start, end time.Time, spanIDs []string) map[string]any {
	filter := append(elasticAPMBaseFilter(start, end),
		map[string]any{"term": map[string]any{"processor.event": "transaction"}},
		map[string]any{"terms": map[string]any{"parent.id": spanIDs}})
	return map[string]any{
		"size":  0,
		"query": map[string]any{"bool": map[string]any{"filter": filter}},
		"aggs": map[string]any{"parents": map[string]any{
			"terms": map[string]any{"field": "parent.id", "size": len(spanIDs)},
			"aggs": map[string]any{"receiver": map[string]any{"top_hits": map[string]any{
				"size":    1,
				"_source": []string{"service.name", "kubernetes.namespace", "kubernetes.pod.name"},
			}}},
		}},
	}
}

// fetchElasticAPMCalls runs reads 2 and 3 and joins them.
func fetchElasticAPMCalls(ctx *security.RequestContext, cfg *ElasticsearchConfig, start, end time.Time) ([]ElasticAPMCall, error) {
	query, sources, subAggs := elasticAPMCallSamplesQuery(start, end)
	url := esSearchURL(cfg, esAPMTraceIndexFor(cfg, ""))
	buckets, err := esCompositeAll(ctx, cfg, url, query, sources, subAggs,
		elasticAPMCompositePageSize, elasticAPMMaxBuckets, "elastic apm call samples")
	if err != nil {
		return nil, err
	}
	samples := parseElasticAPMCallSamples(buckets)
	ids := make([]string, 0, len(samples))
	for _, s := range samples {
		ids = append(ids, s.spanIDs...)
	}
	// Batched so no single request carries tens of thousands of ids. The receiving
	// transaction starts after its parent span; widen the window end slightly so a
	// call that began at `end` still finds its receiver.
	receivers := map[string]elasticAPMDoc{}
	for len(ids) > 0 {
		batch := ids[:min(len(ids), elasticAPMReceiverBatchSize)]
		ids = ids[len(batch):]
		body, err := esPostJSON(ctx, cfg, url, elasticAPMReceiversQuery(start, end.Add(time.Minute), batch), "elastic apm receivers")
		if err != nil {
			return nil, err
		}
		for id, doc := range parseElasticAPMReceivers(body) {
			receivers[id] = doc
		}
	}
	return joinElasticAPMCalls(samples, receivers), nil
}

// elasticAPMCallSample is one read-2 bucket (one calling pod and destination)
// before the receiver join.
type elasticAPMCallSample struct {
	service, namespace, pod, resource string
	spanIDs                           []string
}

// elasticAPMDoc is the subset of an APM document the join reads.
type elasticAPMDoc struct {
	Service struct {
		Name string `json:"name"`
	} `json:"service"`
	Kubernetes struct {
		Namespace string `json:"namespace"`
		Pod       struct {
			Name string `json:"name"`
		} `json:"pod"`
	} `json:"kubernetes"`
	Span struct {
		ID string `json:"id"`
	} `json:"span"`
}

func parseElasticAPMCallSamples(buckets []json.RawMessage) []elasticAPMCallSample {
	out := make([]elasticAPMCallSample, 0, len(buckets))
	for _, raw := range buckets {
		var b struct {
			Key struct {
				Service   string  `json:"service"`
				Namespace *string `json:"namespace"`
				Pod       *string `json:"pod"`
				Resource  string  `json:"resource"`
			} `json:"key"`
			Samples struct {
				Hits struct {
					Hits []struct {
						Source elasticAPMDoc `json:"_source"`
					} `json:"hits"`
				} `json:"hits"`
			} `json:"samples"`
		}
		if err := json.Unmarshal(raw, &b); err != nil || b.Key.Service == "" || b.Key.Resource == "" {
			continue
		}
		s := elasticAPMCallSample{service: b.Key.Service, resource: b.Key.Resource}
		if b.Key.Namespace != nil {
			s.namespace = *b.Key.Namespace
		}
		if b.Key.Pod != nil {
			s.pod = *b.Key.Pod
		}
		for _, h := range b.Samples.Hits.Hits {
			if h.Source.Span.ID != "" {
				s.spanIDs = append(s.spanIDs, h.Source.Span.ID)
			}
		}
		out = append(out, s)
	}
	return out
}

func parseElasticAPMReceivers(body []byte) map[string]elasticAPMDoc {
	var resp struct {
		Aggregations struct {
			Parents struct {
				Buckets []struct {
					Key      string `json:"key"`
					Receiver struct {
						Hits struct {
							Hits []struct {
								Source elasticAPMDoc `json:"_source"`
							} `json:"hits"`
						} `json:"hits"`
					} `json:"receiver"`
				} `json:"buckets"`
			} `json:"parents"`
		} `json:"aggregations"`
	}
	out := map[string]elasticAPMDoc{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return out
	}
	for _, b := range resp.Aggregations.Parents.Buckets {
		if len(b.Receiver.Hits.Hits) > 0 {
			out[b.Key] = b.Receiver.Hits.Hits[0].Source
		}
	}
	return out
}

// joinElasticAPMCalls emits, per sample bucket (one calling pod and destination),
// one ElasticAPMCall per distinct receiver its spans reached, or a single
// receiver-less call when they reached none, so the caller's namespace and pod
// still reach the flow source for the hostname fallback.
//
// A receiver with the caller's own service name is not a hop to another service
// (an in-process child of the exit span), and APM Server reports services that
// set no name as "unknown"; neither identifies a receiver.
func joinElasticAPMCalls(samples []elasticAPMCallSample, receivers map[string]elasticAPMDoc) []ElasticAPMCall {
	var out []ElasticAPMCall
	for _, s := range samples {
		base := ElasticAPMCall{CallerService: s.service, CallerNamespace: s.namespace, CallerPod: s.pod, Resource: s.resource}
		seen := map[ElasticAPMCall]bool{}
		for _, id := range s.spanIDs {
			r, ok := receivers[id]
			if !ok || r.Service.Name == "" || r.Service.Name == s.service || r.Service.Name == "unknown" {
				continue
			}
			c := base
			c.ReceiverService, c.ReceiverNamespace, c.ReceiverPod = r.Service.Name, r.Kubernetes.Namespace, r.Kubernetes.Pod.Name
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
		if len(seen) == 0 {
			out = append(out, base)
		}
	}
	return out
}

// elasticAPMServicePodsQuery builds read 4. Transactions only: every instrumented
// service emits them (a pure caller's root operation is a transaction), and they
// are a fraction of the span volume.
func elasticAPMServicePodsQuery(start, end time.Time) (map[string]any, []map[string]any) {
	filter := append(elasticAPMBaseFilter(start, end),
		map[string]any{"term": map[string]any{"processor.event": "transaction"}},
		map[string]any{"exists": map[string]any{"field": "kubernetes.pod.name"}})
	query := map[string]any{"bool": map[string]any{"filter": filter}}
	sources := []map[string]any{
		{"service": map[string]any{"terms": map[string]any{"field": "service.name"}}},
		{"namespace": map[string]any{"terms": map[string]any{"field": "kubernetes.namespace", "missing_bucket": true}}},
		{"pod": map[string]any{"terms": map[string]any{"field": "kubernetes.pod.name"}}},
	}
	return query, sources
}

func fetchElasticAPMServicePods(ctx *security.RequestContext, cfg *ElasticsearchConfig, start, end time.Time) ([]ElasticAPMServicePod, error) {
	query, sources := elasticAPMServicePodsQuery(start, end)
	subAggs := map[string]any{"last_seen": map[string]any{"max": map[string]any{"field": "@timestamp"}}}
	buckets, err := esCompositeAll(ctx, cfg, esSearchURL(cfg, esAPMTraceIndexFor(cfg, "")), query, sources, subAggs,
		elasticAPMCompositePageSize, elasticAPMMaxBuckets, "elastic apm service pods")
	if err != nil {
		return nil, err
	}
	return parseElasticAPMServicePods(buckets), nil
}

func parseElasticAPMServicePods(buckets []json.RawMessage) []ElasticAPMServicePod {
	out := make([]ElasticAPMServicePod, 0, len(buckets))
	for _, raw := range buckets {
		var b struct {
			Key struct {
				Service   string  `json:"service"`
				Namespace *string `json:"namespace"`
				Pod       string  `json:"pod"`
			} `json:"key"`
			LastSeen esValue `json:"last_seen"` // epoch milliseconds
		}
		if err := json.Unmarshal(raw, &b); err != nil || b.Key.Service == "" || b.Key.Pod == "" {
			continue
		}
		p := ElasticAPMServicePod{Service: b.Key.Service, Pod: b.Key.Pod, LastSeen: time.UnixMilli(int64(b.LastSeen.Value)).UTC()}
		if b.Key.Namespace != nil {
			p.Namespace = *b.Key.Namespace
		}
		out = append(out, p)
	}
	return out
}

// esSearchURL is the _search endpoint for an index pattern.
func esSearchURL(cfg *ElasticsearchConfig, index string) string {
	return esIndexURL(cfg.Url, index, "_search")
}

// esPostJSON POSTs a search body bound to the request's context and returns the
// validated response body.
func esPostJSON(ctx *security.RequestContext, cfg *ElasticsearchConfig, url string, body map[string]any, operation string) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal %s request: %w", operation, err)
	}
	resp, err := esRequestCtx(ctx.GetContext(), "POST", url, string(data), cfg) //nolint:bodyclose // closed by readResponse
	if err != nil {
		return nil, fmt.Errorf("%s request failed: %w", operation, err)
	}
	return readResponse(resp, operation)
}

// esCompositeAll pages a composite aggregation named "grouped" via after_key until
// it is exhausted or maxBuckets is reached (truncation is logged, never silent).
// Unlike fetchAllCompositeTraceBuckets it is bound to the request's context and
// labelled per caller, so a cancelled build stops and failures name the read.
func esCompositeAll(
	ctx *security.RequestContext,
	cfg *ElasticsearchConfig,
	url string,
	query map[string]any,
	sources []map[string]any,
	subAggs map[string]any,
	pageSize, maxBuckets int,
	operation string,
) ([]json.RawMessage, error) {
	var all []json.RawMessage
	var afterKey map[string]any
	for {
		composite := map[string]any{"size": pageSize, "sources": sources}
		if afterKey != nil {
			composite["after"] = afterKey
		}
		grouped := map[string]any{"composite": composite}
		if len(subAggs) > 0 {
			grouped["aggs"] = subAggs
		}
		body, err := esPostJSON(ctx, cfg, url, map[string]any{
			"size":  0,
			"query": query,
			"aggs":  map[string]any{"grouped": grouped},
		}, operation)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Aggregations struct {
				Grouped struct {
					AfterKey map[string]any    `json:"after_key"`
					Buckets  []json.RawMessage `json:"buckets"`
				} `json:"grouped"`
			} `json:"aggregations"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("failed to parse %s response: %w", operation, err)
		}
		page := resp.Aggregations.Grouped
		all = append(all, page.Buckets...)
		if len(page.Buckets) < pageSize || page.AfterKey == nil {
			return all, nil
		}
		if len(all) >= maxBuckets {
			ctx.GetLogger().Warn("elasticsearch composite read truncated at the bucket cap; keys sorting later are dropped",
				"operation", operation, "cap", maxBuckets)
			return all, nil
		}
		afterKey = page.AfterKey
	}
}

// esValue is a single-value metric aggregation result.
type esValue struct {
	Value float64 `json:"value"`
}

// esTermsTop is a terms aggregation whose first bucket key is the answer.
type esTermsTop struct {
	Buckets []struct {
		Key string `json:"key"`
	} `json:"buckets"`
}

func (t esTermsTop) top() string {
	if len(t.Buckets) == 0 {
		return ""
	}
	return t.Buckets[0].Key
}
