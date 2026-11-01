package observability

import (
	"errors"
	"fmt"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/security"
	"sort"
	"time"
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

	// nodeMemoryShortPct is the share of allocatable at which the node itself ran
	// out of memory. The card's "peaked at N%" reading and oomCause's node verdict
	// both key on it, so the insight and the verdict cannot disagree.
	nodeMemoryShortPct = 85
	// subjectAtOwnLimitPct is how close to its own limit a container must peak to
	// count as having hit it. Below 100 because the peak is the last scrape before
	// the kill, not the allocation that triggered it.
	subjectAtOwnLimitPct = 90
)

// oom_cause values on the card for OOM-kill events. Incident grouping reads the
// field (triage.oomCauseFromEvidence) to decide whether a kill belongs to its
// node's incident, so a renamed value silently stops that grouping.
const (
	oomCauseNodeMemory   = "node_memory"
	oomCauseOwnLimit     = "own_limit"
	oomCauseUndetermined = "undetermined"
)

// Indirected so a test can exercise the card's ranking and payload without a live
// metrics backend. There is no provider dispatch left to drive: the layer resolves
// the account's backend, so what remains worth testing is what the card does with
// what comes back.
var fetchNoisyNeighbourMetrics = fetchNoisyNeighbours

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

	tenantID, accountID := ctx.GetTenantId(), ctx.GetAccountId()
	if tenantID == "" || accountID == "" {
		return nil, errors.New("noisy_neighbours_enricher: tenant ID and account ID required")
	}
	requestCtx := security.NewRequestContextForTenantAdmin(tenantID, ctx.GetLogger(), nil, nil)

	start, end := playbooks.RangeQueryWindow(ctx.GetEvent(), noisyNeighboursLookbackMinutes, time.Now().UTC())
	data, err := fetchNoisyNeighbourMetrics(requestCtx, accountID, nodeName, podName, namespace, start, end)
	if err != nil {
		return nil, err
	}
	if data == nil {
		// No implementation can answer for this account. Leaving the event without
		// the card is honest; an empty card would claim the node is idle.
		return nil, nil
	}

	// The neighbours that died alongside. This cannot come from metrics: a pod
	// killed between two scrapes has no sample to rank, and one that landed on the
	// node moments before the kill has no history there at all — which is exactly
	// what hid the second victim in the case this was reported for. The events
	// table is the only record that it happened. See #39083.
	data.CoKilled = coLocatedOOMKills(ctx, nodeName, podName, end)
	if isCoLocatedOOMKey(ctx.GetEvent().AggregationKey) {
		data.OOMCause = oomCause(data)
	}

	return noisyNeighboursResponse(podName, namespace, rawParams, data)
}

// oomCause says whether the subject was killed because its node ran out of memory
// or because one of its containers hit its own limit, and declines to guess
// otherwise.
//
// own_limit wins whenever any of the subject's containers peaked near its own
// limit: the card does not know which container the kernel killed, so a pod with
// one container at its limit is never blamed on the node. node_memory needs
// evidence on both sides — the pod was measured with no container near its
// limit, and the node's containers peaked at nodeMemoryShortPct of allocatable.
//
// Other pods dying on the same node are deliberately not an input. Grouping reads
// this verdict to decide whether those kills are one incident, so the verdict
// cannot be derived from them.
func oomCause(d *noisyNeighbourData) string {
	for _, row := range d.Neighbours {
		if row["is_subject"] != true {
			continue
		}
		used, _ := row["memory_used"].(float64)
		limit, _ := row["memory_limit"].(float64)
		if limit > 0 && used >= limit*subjectAtOwnLimitPct/100 {
			return oomCauseOwnLimit
		}
	}
	if !d.SubjectMeasured || d.NodeAllocatable <= 0 {
		return oomCauseUndetermined
	}
	if d.NodeUsed/d.NodeAllocatable*100 >= nodeMemoryShortPct {
		return oomCauseNodeMemory
	}
	return oomCauseUndetermined
}

type noisyNeighbourData struct {
	NodeName        string
	NodeUsed        float64
	NodeAllocatable float64
	TotalRequested  float64
	// TotalLimits is the sum of every container's memory limit on the node. Above
	// allocatable the node is oversubscribed: the pods may collectively take more
	// than the machine has, and if they actually do, the kernel settles it by
	// killing one. It is a risk, not a measurement — see noisyNeighboursInsights.
	TotalLimits float64
	Neighbours  []map[string]any

	// CPUMeasured is set only when at least one container reported CPU usage.
	// Left false, the renderer omits the CPU keys entirely rather than reporting
	// a hard zero — "we did not measure it" and "nothing is using CPU" must not
	// look the same to a reader.
	CPUMeasured     bool
	NodeCPUUsed     float64
	NodeCPUCapacity float64
	CPUNeighbours   []map[string]any

	// PodsOnNode is how many distinct pods the node actually reported, counted
	// before the list is trimmed. Zero means it could not be counted, and the card
	// omits the figure rather than claiming a node with no pods on it.
	PodsOnNode int

	// SubjectMeasured records whether the pod the card is about reported anything
	// at all. False is a finding, not a gap: no sample for it on this node in the
	// window is what a kill between two scrapes looks like.
	SubjectMeasured bool

	// CoKilled are the other pods this node killed in the same window, read from
	// the events table because metrics cannot see them. See #39083.
	CoKilled []coKilledPod

	// OOMCause is oomCause's verdict, set only for OOM-kill events.
	OOMCause string
}

// noisyNeighbourKeys are the two families the card ranks, each fetched with the
// statistic that suits it. They go in separate requests because `aggregate` applies
// to a whole request, and memory and CPU want different reductions.
var (
	noisyNeighbourMemoryKeys = []string{
		"container_memory_working_set_by_pod",
		"container_memory_request_by_pod",
		"container_memory_limit_by_pod",
		"mem_total",
	}
	noisyNeighbourCPUKeys = []string{
		"container_cpu_usage_by_pod",
		"container_cpu_request_by_pod",
		"container_cpu_limit_by_pod",
		"cpu_total",
	}
)

// fetchNoisyNeighbours asks the utilisation layer which containers on a node are
// consuming what, and what each asked for.
//
// It names keys and a node, never a provider: api-server resolves the account's
// metrics backend and builds the native query. That replaces two hand-written
// implementations — one PromQL, one Elasticsearch — plus the dispatch that chose
// between them, which admitted only Prometheus and Chronosphere and silently skipped
// the card for Datadog, New Relic and Dynatrace.
//
// Two requests rather than one because `aggregate` is per request: memory is ranked on
// its PEAK over the window, CPU on its AVERAGE. That is what the Elasticsearch
// implementation already did, and it is the more useful reading for an evidence card
// on a node-pressure event — the peak is what caused the pressure. The PromQL
// implementation took the latest sample instead, so Prometheus numbers move slightly
// and the two providers now agree, which they did not before.
func fetchNoisyNeighbours(ctx *security.RequestContext, accountID, nodeName, podName, namespace string, start, end time.Time) (*noisyNeighbourData, error) {
	ask := func(keys []string, aggregate string) (map[string][]latestSample, error) {
		output, err := FetchMetricUtilisation(ctx, GetUtilisationTrendRequest{
			AccountId: accountID,
			StartTime: start.UnixMilli(),
			EndTime:   end.UnixMilli(),
			Aggregate: aggregate,
			Request: map[string]any{
				"kind":      "node",
				"node_name": nodeName,
				"metrics":   keys,
			},
		})
		if err != nil {
			return nil, err
		}
		return latestValuesByKey(output), nil
	}

	memory, err := ask(noisyNeighbourMemoryKeys, "max")
	if err != nil {
		// A provider that cannot answer these keys is not an error the event should
		// carry: the card is skipped with the reason logged, which is what the
		// dispatch this replaces did for every provider it had no implementation for.
		ctx.GetLogger().Info("noisy_neighbours_enricher: metrics unavailable for this account; skipping the card",
			"account_id", accountID, "node", nodeName, "error", err)
		return nil, nil
	}
	cpu, err := ask(noisyNeighbourCPUKeys, "avg")
	if err != nil {
		ctx.GetLogger().Info("noisy_neighbours_enricher: cpu metrics unavailable; rendering the card without them",
			"account_id", accountID, "node", nodeName, "error", err)
		cpu = map[string][]latestSample{}
	}

	results := map[string][]latestSample{
		"top_pods":     memory["container_memory_working_set_by_pod"],
		"mem_requests": memory["container_memory_request_by_pod"],
		"mem_limits":   memory["container_memory_limit_by_pod"],

		"node_alloc":   memory["mem_total"],
		"top_pods_cpu": cpu["container_cpu_usage_by_pod"],
		"cpu_requests": cpu["container_cpu_request_by_pod"],
		"cpu_limits":   cpu["container_cpu_limit_by_pod"],

		"node_cpu_alloc": cpu["cpu_total"],
	}
	// CPU is reported only when something actually measured it, so "we did not
	// measure" and "nothing is using CPU" stay distinguishable in the card.
	cpuMeasured := len(results["top_pods_cpu"]) > 0 || len(results["node_cpu_alloc"]) > 0

	return buildNoisyNeighbourData(nodeName, podName, namespace, results, cpuMeasured)
}

// buildNoisyNeighbourData ranks and trims what the layer returned. Provider-agnostic
// by construction: it reads canonical namespace / pod / container labels, which every
// builder now emits.
func buildNoisyNeighbourData(nodeName, podName, namespace string, results map[string][]latestSample, cpuMeasured bool) (*noisyNeighbourData, error) {
	// Index requests / limits by (namespace, pod, container) for O(1)
	// lookup while iterating top_pods. kube-state-metrics emits one
	// series per (pod, container) per resource — no aggregation needed.
	memRequests := indexByPodContainer(results["mem_requests"])
	memLimits := indexByPodContainer(results["mem_limits"])
	totalRequested := 0.0
	for _, v := range memRequests {
		totalRequested += v
	}
	// The sum of every container's memory limit on the node. Requests are what the
	// scheduler reserved; limits are what the pods are allowed to actually take,
	// and a node whose limits exceed its allocatable is oversubscribed — the pods
	// can collectively ask for more than the machine has, and if they do, the kernel
	// resolves that by killing one. `kubectl describe node` reports both for this reason;
	// the card reported only requests, so the condition that explains the kill was
	// the one number missing. Measured on the node from #39083: requests 50% of
	// allocatable, limits 145%.
	totalLimits := 0.0
	for _, v := range memLimits {
		totalLimits += v
	}

	neighbours := []map[string]any{}
	if vec, ok := results["top_pods"]; ok {
		for _, s := range vec {
			pod := s.Metric["pod"]
			ns := s.Metric["namespace"]
			container := s.Metric["container"]
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
		neighbours = trimKeepingSubject(neighbours, podName, namespace)
	}

	// Same shape for CPU, ranked on its own. Kept as a separate list rather
	// than extra columns on `neighbours`: the pod hogging memory is usually not
	// the pod hogging CPU, and one list can only be sorted by one of them.
	cpuRequests := indexByPodContainer(results["cpu_requests"])
	cpuLimits := indexByPodContainer(results["cpu_limits"])
	cpuNeighbours := []map[string]any{}
	if vec, ok := results["top_pods_cpu"]; ok {
		for _, entry := range vec {
			pod := entry.Metric["pod"]
			ns := entry.Metric["namespace"]
			container := entry.Metric["container"]
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
		cpuNeighbours = trimKeepingSubject(cpuNeighbours, podName, namespace)
	}

	// The node totals are the sum of the containers on it, not a node-level gauge.
	//
	// `memory_usage` and `cpu_usage` at node scope read node_memory_Active_bytes /
	// node_cpu_seconds_total filtered by `instance=~"<internal IP>.*"`, and this
	// enricher only ever knows the node NAME. With no IP that regex matches every
	// instance, so the card reported the whole cluster against one node's
	// allocatable: 51.3 GB used of 12.9 GB, and 20.0 cores of 3.92, measured on dev.
	//
	// Summing the per-container series is what the PromQL this replaces did and needs
	// no IP. It sums EVERY container the layer returned for the node, not the fifteen
	// the card lists: the total is what the node is using, and trimming it to the
	// visible rows would under-report it on any node running more than fifteen
	// containers — which is most of them. The rows therefore do not add up to the
	// total, by design.
	nodeUsed := sumLatestValues(results["top_pods"])
	nodeAlloc := firstLatestValue(results["node_alloc"])
	nodeCPUUsed := sumLatestValues(results["top_pods_cpu"])
	nodeCPUCapacity := firstLatestValue(results["node_cpu_alloc"])

	// Every query came back empty, allocatable included. A real node always
	// reports allocatable, so this is a node Prometheus has never heard of —
	// a name we guessed wrong — not an idle one. Rendering it would put a card
	// on the event saying the machine has no usage and no neighbours, which is
	// a claim we have not earned; failing leaves the event without the card,
	// which is what it had before.
	if len(neighbours) == 0 && len(cpuNeighbours) == 0 && nodeAlloc == 0 && nodeCPUCapacity == 0 {
		return nil, fmt.Errorf("noisy_neighbours_enricher: no metrics for node %q — it does not look like a node this cluster's Prometheus knows", nodeName)
	}

	data := &noisyNeighbourData{
		NodeName:        nodeName,
		NodeUsed:        nodeUsed,
		NodeAllocatable: nodeAlloc,
		TotalRequested:  totalRequested,
		TotalLimits:     totalLimits,
		Neighbours:      neighbours,
		CPUMeasured:     cpuMeasured,
		NodeCPUUsed:     nodeCPUUsed,
		NodeCPUCapacity: nodeCPUCapacity,
		CPUNeighbours:   cpuNeighbours,
		// Counted from every container the layer returned, not from the trimmed
		// rows — the same full set the node totals above are summed from.
		PodsOnNode: distinctPods(results["top_pods"]),
	}
	// Which rows are the reader's own pod. The ranking answers "who is biggest",
	// which is a different question from "where does the pod I am looking at sit".
	data.SubjectMeasured = markNoisyNeighboursSubject(data, podName, namespace)
	return data, nil
}

// trimKeepingSubject cuts a ranked list to the top N but never drops the pod the
// card is about.
//
// The plain truncation this replaces is why the subject was missing from its own
// card: the list is ranked by usage, and the pod that just died is frequently not
// in the top fifteen. The subject is appended after the cut rather than sorted in,
// so the rows above it still read as "the biggest consumers" and the extra row is
// plainly the one the reader asked about.
func trimKeepingSubject(rows []map[string]any, podName, namespace string) []map[string]any {
	if len(rows) <= noisyNeighboursTopN {
		return rows
	}
	kept := rows[:noisyNeighboursTopN]
	for _, row := range kept {
		if row["pod_name"] == podName && row["namespace"] == namespace {
			return kept
		}
	}
	// One row per container of the subject, matching how the ranked list is built:
	// a pod that died because a sidecar grew is a case the reader has to see.
	trimmed := append([]map[string]any{}, kept...)
	for _, row := range rows[noisyNeighboursTopN:] {
		if row["pod_name"] == podName && row["namespace"] == namespace {
			trimmed = append(trimmed, row)
		}
	}
	return trimmed
}

// markNoisyNeighboursSubject flags the subject's own rows in both lists so the UI
// can pin and highlight them, and reports whether the subject was measured at all.
//
// A false return is a real answer, not a failure: it means the node reported no
// sample for this pod in the window, which is what a kill between two scrapes
// looks like. The card says so rather than leaving the reader to conclude the pod
// was idle when it died.
func markNoisyNeighboursSubject(d *noisyNeighbourData, podName, namespace string) bool {
	found := false
	mark := func(rows []map[string]any) {
		for _, row := range rows {
			if row["pod_name"] == podName && row["namespace"] == namespace {
				row["is_subject"] = true
				found = true
			}
		}
	}
	mark(d.Neighbours)
	mark(d.CPUNeighbours)
	return found
}

// distinctPods counts pods, not container series — a pod with three containers is
// one pod on the node.
func distinctPods(samples []latestSample) int {
	seen := map[string]struct{}{}
	for _, s := range samples {
		if pod := s.Metric["pod"]; pod != "" {
			seen[s.Metric["namespace"]+"/"+pod] = struct{}{}
		}
	}
	return len(seen)
}

// noisyNeighboursResponse renders the card. Both providers go through it so the payload
// the UI consumes cannot drift between them — the fields below are consumed verbatim by
// the NoisyNeighbour card, and a missing `name` or `memory_requested` renders as
// "Container undefined does not have a memory requests".
func noisyNeighboursResponse(podName, namespace string, rawParams map[string]any, d *noisyNeighbourData) (playbooks.PlaybookActionResponse, error) {
	data := map[string]any{
		"node_name":          d.NodeName,
		"memory_used":        d.NodeUsed,
		"memory_allocatable": d.NodeAllocatable,
		"memory_requested":   d.TotalRequested,
		"memory_limits":      d.TotalLimits,
		"neighbours":         d.Neighbours,
		// What the list actually is, named for what it actually is. The old
		// `total_pods` held this same number while claiming to be the node's pod
		// count: it read 15 on 720 of 730 cards measured over 14 days, because that
		// is the truncation limit, and the evidence-analysis agent repeated it to
		// the model as "Number of pods in node".
		"neighbours_shown": len(d.Neighbours),
		"subject_pod":      podName,
		"subject_measured": d.SubjectMeasured,
	}
	// Omitted rather than zeroed when unknown: "we could not count the pods" and
	// "this node has no pods" must not look the same.
	if d.PodsOnNode > 0 {
		data["pods_on_node"] = d.PodsOnNode
	}
	if len(d.CoKilled) > 0 {
		data["co_killed"] = coKilledPayload(d.CoKilled)
	}
	if d.OOMCause != "" {
		data["oom_cause"] = d.OOMCause
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
	return playbooks.NewPlaybookActionResponseJson(payload, additionalInfo, noisyNeighboursInsights(d), metadata), nil
}

// noisyNeighboursInsights says what the numbers mean, so the reader is not left
// doing the arithmetic. The card shipped with an empty insight list on all 752 of
// the cards measured over 14 days.
//
// Every line states only what was measured. In particular there is no "your pod
// exceeded its own limit" line: three quarters of these events have no memory
// limit recorded for the subject at all, so that claim cannot be earned here.
func noisyNeighboursInsights(d *noisyNeighbourData) []playbooks.PlaybookActionResponseInsight {
	insights := []playbooks.PlaybookActionResponseInsight{}

	// The strongest signal first: other pods died on this machine, minutes apart.
	// It is not co-timing — it is the same node killing more than one tenant.
	if n := len(d.CoKilled); n > 0 {
		insights = append(insights, playbooks.PlaybookActionResponseInsight{
			Message: fmt.Sprintf("%s also OOM-killed on this node within %d minutes: %s. One full machine, not %d unrelated faults.",
				pluralPods(n), coLocatedWindowMinutes, describeCoKilled(d.CoKilled), n+1),
			Severity: "HIGH",
		})
	}

	if d.NodeAllocatable > 0 {
		requestedPct := d.TotalRequested / d.NodeAllocatable * 100
		usedPct := d.NodeUsed / d.NodeAllocatable * 100
		limitsPct := d.TotalLimits / d.NodeAllocatable * 100
		// Only usage, or another pod dying here, shows the machine actually ran out.
		// Requests and limits are what the pods reserved and are allowed to take, not
		// what they took, and the kernel kills on physical shortage: a node can carry
		// limits at 209% of allocatable while using 8% of it. Limits exceed allocatable
		// on almost every node, so keying the claim on them alone put it on nearly every
		// card — including nodes with memory to spare, where it told the reader, and the
		// investigation agent, that oversubscription killed the pod.
		nodeShort := usedPct >= nodeMemoryShortPct || len(d.CoKilled) > 0
		switch {
		// Oversubscribed beats every other reading once the node did run out: the
		// pods on it are allowed to take more memory than it has, which is how it got
		// there. `kubectl describe node` puts requests and limits side by side for
		// exactly this reason.
		case nodeShort && limitsPct > 100:
			insights = append(insights, playbooks.PlaybookActionResponseInsight{
				Message: fmt.Sprintf("This node is oversubscribed: the memory limits of the pods on it add up to %.0f%% of allocatable (%s of %s). They cannot all use what they are allowed, so the kernel resolves it by killing one.",
					limitsPct, formatBytesGiB(d.TotalLimits), formatBytesGiB(d.NodeAllocatable)),
				Severity: "HIGH",
			})
		case nodeShort && requestedPct >= 95:
			insights = append(insights, playbooks.PlaybookActionResponseInsight{
				Message: fmt.Sprintf("Node memory is committed to %.0f%% of allocatable (%s requested of %s) — there is no headroom on this machine for a pod to grow into.",
					requestedPct, formatBytesGiB(d.TotalRequested), formatBytesGiB(d.NodeAllocatable)),
				Severity: "HIGH",
			})
		case usedPct >= nodeMemoryShortPct:
			insights = append(insights, playbooks.PlaybookActionResponseInsight{
				Message: fmt.Sprintf("Node memory peaked at %.0f%% of allocatable (%s of %s) in the window before the kill.",
					usedPct, formatBytesGiB(d.NodeUsed), formatBytesGiB(d.NodeAllocatable)),
				Severity: "MEDIUM",
			})
		case !nodeShort:
			message := fmt.Sprintf("The node was not short of memory — it peaked at %.0f%% of allocatable (%s of %s) and no other pod was killed here in the same window. Look at this pod's own limit before its neighbours.",
				usedPct, formatBytesGiB(d.NodeUsed), formatBytesGiB(d.NodeAllocatable))
			// The reservation figures sit beside usage in the card and are the ones
			// that look alarming, so say outright that they are not the cause. Each is
			// explained on its own: the card highlights both, and both are often high.
			if limitsPct > 100 {
				message += fmt.Sprintf(" The pods' memory limits add up to %.0f%% of allocatable (%s), but a limit is a ceiling, not usage: that oversubscription is a risk, not what killed this pod.",
					limitsPct, formatBytesGiB(d.TotalLimits))
			}
			if requestedPct >= 95 {
				message += fmt.Sprintf(" Requests reserve %.0f%% of allocatable (%s), but reserved is not used: that stops the scheduler placing more pods here, not this pod from growing.",
					requestedPct, formatBytesGiB(d.TotalRequested))
			}
			insights = append(insights, playbooks.PlaybookActionResponseInsight{
				Message:  message,
				Severity: "INFO",
			})
		}
	}

	// Stated plainly, because the alternative is a card that silently omits the
	// one pod the reader came to look at.
	if !d.SubjectMeasured {
		insights = append(insights, playbooks.PlaybookActionResponseInsight{
			Message:  "This pod reported no memory sample on this node before it was killed, so it is absent from the usage list below — the usual sign of a pod that died between two scrapes, or was scheduled here moments before.",
			Severity: "INFO",
		})
	}
	return insights
}

func pluralPods(n int) string {
	if n == 1 {
		return "1 other pod was"
	}
	return fmt.Sprintf("%d other pods were", n)
}

// formatBytesGiB matches how the card renders every other memory figure, so an
// insight and the table beneath it cannot disagree about the same number.
func formatBytesGiB(bytes float64) string {
	return fmt.Sprintf("%.2f GiB", bytes/(1024*1024*1024))
}

// latestSample is one series reduced to its most recent sample.
type latestSample struct {
	Metric map[string]string
	Value  float64
}

// latestValuesByKey reduces a range result to {metric, latest value} per series,
// keyed by query — what the neighbour tables are built from.
func latestValuesByKey(output OutputMetricQuery) map[string][]latestSample {
	out := make(map[string][]latestSample, len(output.Results))
	for _, result := range output.Results {
		samples := make([]latestSample, 0, len(result.Payload))
		for _, series := range result.Payload {
			if len(series.Values) == 0 {
				continue
			}
			samples = append(samples, latestSample{Metric: series.Metric, Value: series.Values[len(series.Values)-1]})
		}
		out[result.QueryKey] = samples
	}
	return out
}

// indexByPodContainer builds a {namespace/pod/container → latest value} map from
// a kube-state-metrics result, for the O(1) requests/limits join.
func indexByPodContainer(samples []latestSample) map[string]float64 {
	out := map[string]float64{}
	for _, s := range samples {
		pod, container := s.Metric["pod"], s.Metric["container"]
		if pod == "" || container == "" {
			continue
		}
		out[s.Metric["namespace"]+"/"+pod+"/"+container] = s.Value
	}
	return out
}

// firstLatestValue is the latest sample of the first series — for the
// scalar-ish node queries that resolve to a single series.
// sumLatestValues totals one key's series — the node's own usage, summed from the
// containers running on it.
func sumLatestValues(samples []latestSample) float64 {
	total := 0.0
	for _, s := range samples {
		total += s.Value
	}
	return total
}

func firstLatestValue(samples []latestSample) float64 {
	for _, s := range samples {
		return s.Value
	}
	return 0
}
