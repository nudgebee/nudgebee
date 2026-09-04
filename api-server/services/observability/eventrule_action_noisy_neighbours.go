package observability

import (
	"errors"
	"fmt"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/security"
	"sort"
)

// noisy_neighbours_enricher composes Prometheus queries against the host
// node to identify the top memory-consuming co-tenant pods.
//
// Output shape:
//
//	{
//	  "name": "noisy_neighbours",
//	  "data": {
//	    "node_name":          "<node>",
//	    "memory_used":        <bytes>,
//	    "memory_allocatable": <bytes>,
//	    "total_pods":         N,
//	    "neighbours":         [{pod_name, namespace, memory_used}, ...]
//	  }
//	}
func init() {
	playbooks.RegisterAction("noisy_neighbours_enricher", &noisyNeighboursAction{})
}

type noisyNeighboursAction struct{}

// Events worth asking "is something else on this node to blame?" about.
//
// The first group comes from the agent's own matchers and already carries the
// node. The second group is Prometheus / kube-state alerts about a single pod:
// they are the signals that actually point at contention — a throttled
// container, a pod that never became ready, a container stuck waiting — and
// they were all excluded, so the one question this enricher exists to answer
// was never asked for them. They carry no node (measured: 3589/3589 such
// events in a week on prod had neither a node nor an instance label), which is
// why nodeForSubjectPod below resolves it from the pod inventory.
var noisyNeighboursAggKeys = map[string]bool{
	"pod_oom_killer_enricher": true,
	"report_crash_loop":       true,
	"KubePodCrashLooping":     true,
	"CPUThrottlingHigh":       true,
	"KubePodNotReady":         true,
	"KubeContainerWaiting":    true,
	// Kubelet warnings arrive under one key; a failing readiness probe —
	// the textbook symptom of a neighbour eating the node — is one of them.
	"Kubernetes Warning Event": true,
}

// We query Prometheus over a short RANGE window ending at the incident and
// take the latest sample of each series, rather than a single instant query.
// The agent's live instant batch returns an empty result set at
// event-processing time — for the same OOM event the range-based
// pod_metric / pod_node_metrics cards populate while the instant
// noisy-neighbours card comes back all-zero — so we use the proven range
// path. topk(15) evaluated over a range can surface more than 15 distinct
// series across steps, so we cap to the top N after sorting by latest value.
const (
	noisyNeighboursTopN            = 15
	noisyNeighboursLookbackMinutes = 10
)

func (a *noisyNeighboursAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	if !noisyNeighboursAggKeys[ctx.GetEvent().AggregationKey] {
		return false
	}
	name, ns := playbooks.SubjectPodNamespace(ctx.GetEvent())
	if name == "" || ns == "" {
		return false
	}
	// Need the host node to filter peers. Agent-sourced events carry it on
	// the event; alert-sourced ones do not, and are resolved from inventory.
	return noisyNeighboursNodeName(ctx) != ""
}

func (a *noisyNeighboursAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	podName, namespace := playbooks.SubjectPodNamespace(ctx.GetEvent())
	return a.Execute(ctx, map[string]any{
		"pod_name":  podName,
		"namespace": namespace,
		"node_name": noisyNeighboursNodeName(ctx),
	})
}

func (a *noisyNeighboursAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	podName, _ := rawParams["pod_name"].(string)
	namespace, _ := rawParams["namespace"].(string)
	nodeName, _ := rawParams["node_name"].(string)
	if nodeName == "" {
		nodeName = noisyNeighboursNodeName(ctx)
	}
	if podName == "" || namespace == "" {
		return nil, errors.New("noisy_neighbours_enricher: pod_name + namespace required")
	}
	if nodeName == "" {
		return nil, errors.New("noisy_neighbours_enricher: no node_name on event (subject_node empty)")
	}

	// We assemble five instant queries against the host node so the
	// resulting `neighbours` shape matches what the legacy Robusta
	// playbook emitted (memory_analyzer.py:100 →
	// `{name, pod_name, namespace, memory_used, memory_requested,
	//   memory_limit}`). The UI's NoisyNeighbour card consumes those
	// fields verbatim; missing `name` or `memory_requested` renders as
	// "Container undefined does not have a memory requests".
	//
	// Where the K8s node name lands on cAdvisor
	// (container_memory_working_set_bytes) depends on the Prometheus scrape
	// config, and we've observed three real-world variations:
	//   1. kube-prometheus-stack (EKS): node name on `node`, `instance` is
	//      the kubelet scrape target (`<nodeIP>:10250`).
	//   2. older relabel rules: node name on `instance`, `node` relabelled
	//      to a node-pool category (e.g. `node="db"`).
	//   3. BOTH at once (a vmsingle cluster scraping kubelets via two jobs):
	//      one job emits convention 1, the other convention 2, so every
	//      container has TWO near-duplicate series.
	// We can't know the convention up front, so we match the node name on
	// EITHER `node` or `instance`. The catch is variation 3: a naive
	// `{node="X"} or {instance="X"}` at the selector level keeps both
	// duplicate series and DOUBLE-COUNTS memory. So we aggregate to
	// (pod, namespace, container) on each branch FIRST, then `or` — after
	// aggregation both branches share an identical label signature, so the
	// `or` takes the `node=` side and only fills in containers it's
	// missing. One scrape's view wins; no double counting.
	//   - kube-state-metrics (kube_*): node name is always on `node` (its
	//     `instance` is the kube-state-metrics pod), so those queries below
	//     filter by `node=` alone.
	// Keeping the `container` label intact lets us join against the
	// kube_pod_container_resource_{requests,limits} series, which only
	// carry `pod` / `namespace` / `container`.
	perContainer := func(series func(selector string) string, extraFilters string) string {
		return fmt.Sprintf(
			`sum by (pod, namespace, container) (%s) or sum by (pod, namespace, container) (%s)`,
			series(fmt.Sprintf(`__CLUSTER__ node="%s"%s`, nodeName, extraFilters)),
			series(fmt.Sprintf(`__CLUSTER__ instance="%s"%s`, nodeName, extraFilters)),
		)
	}
	memorySeries := func(selector string) string {
		return fmt.Sprintf(`container_memory_working_set_bytes{%s}`, selector)
	}
	// CPU is a rate, so the node/instance `or` wraps the rate() per branch —
	// aggregating first and rating after would be wrong across restarts.
	cpuSeries := func(selector string) string {
		return fmt.Sprintf(`rate(container_cpu_usage_seconds_total{%s}[5m])`, selector)
	}
	perContainerUsage := func(extraFilters string) string {
		return perContainer(memorySeries, extraFilters)
	}
	topPodsQuery := fmt.Sprintf(
		`topk(15, %s)`,
		perContainerUsage(`, pod!="", container!="", container!="POD", image!=""`),
	)
	nodeUsageQuery := fmt.Sprintf(
		`sum(%s)`,
		perContainerUsage(`, pod!="", image!=""`),
	)
	nodeAllocatableQuery := fmt.Sprintf(
		`kube_node_status_allocatable{__CLUSTER__ resource="memory", node="%s"}`,
		nodeName,
	)
	memoryRequestsQuery := fmt.Sprintf(
		`kube_pod_container_resource_requests{__CLUSTER__ resource="memory", node="%s"}`,
		nodeName,
	)
	memoryLimitsQuery := fmt.Sprintf(
		`kube_pod_container_resource_limits{__CLUSTER__ resource="memory", node="%s"}`,
		nodeName,
	)
	// CPU, the half we never measured. A node pinned at its CPU limit starves
	// every pod on it — probes time out, requests queue — while memory looks
	// perfectly healthy, so a memory-only answer reports nothing wrong and the
	// blame lands on whatever the reader can see (usually the probe's own
	// settings).
	topPodsCPUQuery := fmt.Sprintf(
		`topk(%d, %s)`,
		noisyNeighboursTopN,
		perContainer(cpuSeries, `, pod!="", container!="", container!="POD", image!=""`),
	)
	nodeCPUUsageQuery := fmt.Sprintf(
		`sum(%s)`,
		perContainer(cpuSeries, `, pod!="", image!=""`),
	)
	nodeCPUCapacityQuery := fmt.Sprintf(
		`kube_node_status_allocatable{__CLUSTER__ resource="cpu", node="%s"}`,
		nodeName,
	)
	cpuRequestsQuery := fmt.Sprintf(
		`kube_pod_container_resource_requests{__CLUSTER__ resource="cpu", node="%s"}`,
		nodeName,
	)
	cpuLimitsQuery := fmt.Sprintf(
		`kube_pod_container_resource_limits{__CLUSTER__ resource="cpu", node="%s"}`,
		nodeName,
	)

	tenantID, accountID := ctx.GetTenantId(), ctx.GetAccountId()
	if tenantID == "" || accountID == "" {
		return nil, errors.New("noisy_neighbours_enricher: tenant ID and account ID required")
	}
	requestCtx := security.NewRequestContextForTenantAdmin(tenantID, ctx.GetLogger(), nil, nil)

	// Ask the account's own metrics provider. The five PromQL queries below reach the
	// agent's prometheus_url, which a cluster shipping to Elasticsearch does not have —
	// so on those clusters this card was permanently empty with nothing to say why.
	provider, _, provErr := GetLogsMetricsTracesProvider(requestCtx, accountID, "", "metrics", "")
	if provErr != nil {
		return nil, fmt.Errorf("noisy_neighbours_enricher: metrics provider: %w", provErr)
	}
	if provider == "elasticsearch" {
		data, esErr := esNoisyNeighbours(requestCtx, accountID, nodeName,
			noisyNeighboursLookbackMinutes, noisyNeighboursTopN)
		if esErr != nil {
			return nil, fmt.Errorf("noisy_neighbours_enricher: elasticsearch: %w", esErr)
		}
		return noisyNeighboursResponse(podName, namespace, rawParams, data)
	}

	// One batch, one relay round trip. Every query is scoped to a single node
	// over a 10-minute range, so the added CPU half costs evaluation time on a
	// few dozen series, not a fan-out.
	results, err := playbooks.PromRangeQueries(ctx, []playbooks.NamedQuery{
		{Key: "top_pods", Query: topPodsQuery},
		{Key: "node_used", Query: nodeUsageQuery},
		{Key: "node_alloc", Query: nodeAllocatableQuery},
		{Key: "mem_requests", Query: memoryRequestsQuery},
		{Key: "mem_limits", Query: memoryLimitsQuery},
		{Key: "top_pods_cpu", Query: topPodsCPUQuery},
		{Key: "node_cpu_used", Query: nodeCPUUsageQuery},
		{Key: "node_cpu_alloc", Query: nodeCPUCapacityQuery},
		{Key: "cpu_requests", Query: cpuRequestsQuery},
		{Key: "cpu_limits", Query: cpuLimitsQuery},
	}, noisyNeighboursLookbackMinutes)
	if err != nil {
		return nil, fmt.Errorf("noisy_neighbours_enricher: prom: %w", err)
	}

	// Index requests / limits by (namespace, pod, container) for O(1)
	// lookup while iterating top_pods. kube-state-metrics emits one
	// series per (pod, container) per resource — no aggregation needed.
	memRequests := playbooks.IndexByPodContainer(results["mem_requests"])
	memLimits := playbooks.IndexByPodContainer(results["mem_limits"])
	totalRequested := 0.0
	for _, v := range memRequests {
		totalRequested += v
	}

	neighbours := []map[string]any{}
	if vec, ok := results["top_pods"]; ok {
		for _, s := range playbooks.LatestValueEntries(vec) {
			pod, _ := s.Metric["pod"].(string)
			ns, _ := s.Metric["namespace"].(string)
			container, _ := s.Metric["container"].(string)
			key := ns + "/" + pod + "/" + container
			entry := map[string]any{
				"name":             container,
				"pod_name":         pod,
				"namespace":        ns,
				"node_name":        nodeName,
				"memory_used":      s.Value,
				"memory_requested": memRequests[key],
				"memory_limit":     memLimits[key],
			}
			neighbours = append(neighbours, entry)
		}
		sort.Slice(neighbours, func(i, j int) bool {
			vi, _ := neighbours[i]["memory_used"].(float64)
			vj, _ := neighbours[j]["memory_used"].(float64)
			return vi > vj
		})
		// topk(15) over a range can yield >15 distinct series across steps;
		// keep only the top N by latest value to match the instant semantics.
		if len(neighbours) > noisyNeighboursTopN {
			neighbours = neighbours[:noisyNeighboursTopN]
		}
	}

	// Same shape for CPU, ranked on its own. Kept as a separate list rather
	// than extra columns on `neighbours`: the pod hogging memory is usually not
	// the pod hogging CPU, and one list can only be sorted by one of them.
	cpuRequests := playbooks.IndexByPodContainer(results["cpu_requests"])
	cpuLimits := playbooks.IndexByPodContainer(results["cpu_limits"])
	cpuNeighbours := []map[string]any{}
	if vec, ok := results["top_pods_cpu"]; ok {
		for _, entry := range playbooks.LatestValueEntries(vec) {
			pod, _ := entry.Metric["pod"].(string)
			ns, _ := entry.Metric["namespace"].(string)
			container, _ := entry.Metric["container"].(string)
			key := ns + "/" + pod + "/" + container
			cpuNeighbours = append(cpuNeighbours, map[string]any{
				"name":          container,
				"pod_name":      pod,
				"namespace":     ns,
				"node_name":     nodeName,
				"cpu_used":      entry.Value,
				"cpu_requested": cpuRequests[key],
				"cpu_limit":     cpuLimits[key],
			})
		}
		sort.Slice(cpuNeighbours, func(i, j int) bool {
			vi, _ := cpuNeighbours[i]["cpu_used"].(float64)
			vj, _ := cpuNeighbours[j]["cpu_used"].(float64)
			return vi > vj
		})
		if len(cpuNeighbours) > noisyNeighboursTopN {
			cpuNeighbours = cpuNeighbours[:noisyNeighboursTopN]
		}
	}

	nodeUsed := playbooks.FirstLatestValue(results["node_used"])
	nodeAlloc := playbooks.FirstLatestValue(results["node_alloc"])

	return noisyNeighboursResponse(podName, namespace, rawParams, &esNoisyNeighbourData{
		NodeName:        nodeName,
		NodeUsed:        nodeUsed,
		NodeAllocatable: nodeAlloc,
		TotalRequested:  totalRequested,
		Neighbours:      neighbours,
		CPUMeasured:     true,
		NodeCPUUsed:     playbooks.FirstLatestValue(results["node_cpu_used"]),
		NodeCPUCapacity: playbooks.FirstLatestValue(results["node_cpu_alloc"]),
		CPUNeighbours:   cpuNeighbours,
	})
}

// noisyNeighboursResponse renders the card. Both providers go through it so the payload
// the UI consumes cannot drift between them — the fields below are consumed verbatim by
// the NoisyNeighbour card, and a missing `name` or `memory_requested` renders as
// "Container undefined does not have a memory requests".
func noisyNeighboursResponse(podName, namespace string, rawParams map[string]any, d *esNoisyNeighbourData) (playbooks.PlaybookActionResponse, error) {
	data := map[string]any{
		"node_name":          d.NodeName,
		"memory_used":        d.NodeUsed,
		"memory_allocatable": d.NodeAllocatable,
		"memory_requested":   d.TotalRequested,
		"total_pods":         len(d.Neighbours),
		"neighbours":         d.Neighbours,
	}
	// Only when we actually measured CPU. On a provider that cannot report it,
	// omitting the keys says "unknown"; a zero would say "nothing is using CPU",
	// which is a different and much more misleading claim.
	if d.CPUMeasured {
		data["cpu_used"] = d.NodeCPUUsed
		data["cpu_allocatable"] = d.NodeCPUCapacity
		data["cpu_neighbours"] = d.CPUNeighbours
	}
	payload := map[string]any{
		"name": "noisy_neighbours",
		"data": data,
	}

	additionalInfo := map[string]any{
		"title":              "Noisy Neighbours",
		"action_name":        "noisy_neighbours_enricher",
		"actual_action_name": "noisy_neighbours_enricher",
		"node_name":          d.NodeName,
		"pod_name":           podName,
		"namespace":          namespace,
	}
	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}
	return playbooks.NewPlaybookActionResponseJson(payload, additionalInfo, []playbooks.PlaybookActionResponseInsight{}, metadata), nil
}
