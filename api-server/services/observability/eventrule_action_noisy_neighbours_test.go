package observability

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/eventrule/playbooks"
)

// Moved here with the action itself, from the playbooks stage-2.2 CanAutoExecute table.
// The node name is required: without it the enricher cannot pick the pod's peers, and
// answering for the wrong node is worse than not answering.
func TestNoisyNeighboursCanAutoExecute(t *testing.T) {
	ctxFor := func(aggKey, subjectType, name, namespace, node string) playbooks.PlaybookActionContext {
		return playbooks.NewPlaybookActionContext("t", "a", slog.Default(), playbooks.PlaybookEvent{
			AggregationKey:   aggKey,
			SubjectType:      subjectType,
			SubjectName:      name,
			SubjectNamespace: namespace,
			SubjectNode:      node,
		})
	}
	a := &noisyNeighboursAction{}

	assert.True(t, a.CanAutoExecute(ctxFor("pod_oom_killer_enricher", "pod", "p1", "ns", "node-1")))
	assert.True(t, a.CanAutoExecute(ctxFor("report_crash_loop", "pod", "p1", "ns", "node-1")))
	assert.False(t, a.CanAutoExecute(ctxFor("job_failure", "job", "j1", "ns", "node-1")))
	assert.False(t, a.CanAutoExecute(ctxFor("pod_oom_killer_enricher", "pod", "p1", "ns", "")))
}

// Requests and limits live in the state_container metricset, which carries NO node
// field — asking for them alongside working set returns null for every container
// (verified against a live cluster). So they are a second search keyed by the pods the
// first one found. If that lookup fails the rows still render, with an empty request
// column, rather than the whole card erroring.
func TestNoisyNeighboursSpecsAreKeyedByPodAndContainer(t *testing.T) {
	raw := []byte(`{"aggregations":{"pods":{"buckets":[
	  {"key":"kube-dns-1","containers":{"buckets":[
	     {"key":"kubedns","requested":{"value":73400320},"limit":{"value":220200960}},
	     {"key":"dnsmasq","requested":{"value":20971520},"limit":{"value":null}}]}}]}}}`)
	var resp esNNSpecResponse
	require.NoError(t, json.Unmarshal(raw, &resp))

	specs := map[string]esContainerSpec{}
	for _, pod := range resp.Aggregations.Pods.Buckets {
		for _, c := range pod.Containers.Buckets {
			specs[pod.Key+"/"+c.Key] = esContainerSpec{
				Requested: esNNValue(c.Requested.Value),
				Limit:     esNNValue(c.Limit.Value),
			}
		}
	}

	assert.Equal(t, float64(73400320), specs["kube-dns-1/kubedns"].Requested)
	assert.Equal(t, float64(220200960), specs["kube-dns-1/kubedns"].Limit)
	// A container with no limit reports 0, not a nil deref.
	assert.Equal(t, float64(20971520), specs["kube-dns-1/dnsmasq"].Requested)
	assert.Zero(t, specs["kube-dns-1/dnsmasq"].Limit)
	// A container absent from the spec search reads as zero rather than missing.
	assert.Zero(t, specs["kube-dns-1/absent"].Requested)
}

// Both providers render through noisyNeighboursResponse, so the payload the UI consumes
// cannot drift between them. The card reads these fields verbatim.
func TestNoisyNeighboursPayloadShapeIsProviderIndependent(t *testing.T) {
	resp, err := noisyNeighboursResponse("p1", "ns", map[string]any{"node_name": "n1"},
		&esNoisyNeighbourData{
			NodeName: "n1", NodeUsed: 100, NodeAllocatable: 200, TotalRequested: 150,
			Neighbours: []map[string]any{{"name": "c1", "pod_name": "p1", "memory_used": 100.0}},
		})
	require.NoError(t, err)

	out, err := json.Marshal(resp)
	require.NoError(t, err)
	for _, want := range []string{"node_name", "memory_used", "memory_allocatable", "memory_requested", "total_pods", "neighbours"} {
		assert.Containsf(t, string(out), want, "payload missing %q", want)
	}
}

// Without a node there is nothing to narrow by, and an unfiltered query would return the
// whole cluster's containers as though they were this node's. Guarded inside the lookup
// so every caller is covered, not just the enricher.
func TestNoisyNeighboursESRefusesAnEmptyNodeName(t *testing.T) {
	_, err := esNoisyNeighbours(nil, "acct", "", 10, 15)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "node name required")
}

// A node with nothing on it must render as an empty list. A nil slice marshals to
// `null`, which the card iterates.
func TestNoisyNeighboursEmptyNodeRendersAsEmptyList(t *testing.T) {
	resp, err := noisyNeighboursResponse("p1", "ns", nil, &esNoisyNeighbourData{
		NodeName: "n1", Neighbours: []map[string]any{},
	})
	require.NoError(t, err)
	raw, err := json.Marshal(resp)
	require.NoError(t, err)

	// The response nests the payload as a JSON *string*, so decode twice rather than
	// substring-matching the escaped form.
	var envelope struct {
		Data string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &envelope))
	var payload struct {
		Data struct {
			Neighbours []map[string]any `json:"neighbours"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(envelope.Data), &payload))
	assert.NotNil(t, payload.Data.Neighbours, "must serialise as [] so the card can iterate it")
	assert.Empty(t, payload.Data.Neighbours)
	assert.Contains(t, envelope.Data, `"neighbours":[]`)
}

// The alert-sourced events are the ones that actually suggest contention, and
// they were all excluded — so the question "is a neighbour to blame?" was never
// asked for a throttled container or a pod that never became ready.
func TestNoisyNeighboursRunsForContentionAlerts(t *testing.T) {
	ctxFor := func(aggKey string) playbooks.PlaybookActionContext {
		return playbooks.NewPlaybookActionContext("t", "a", slog.Default(), playbooks.PlaybookEvent{
			AggregationKey:   aggKey,
			SubjectType:      "pod",
			SubjectName:      "p1",
			SubjectNamespace: "ns",
			SubjectNode:      "node-1",
		})
	}
	a := &noisyNeighboursAction{}

	for _, key := range []string{
		"CPUThrottlingHigh",
		"KubePodNotReady",
		"KubeContainerWaiting",
		"KubePodCrashLooping",
		"Kubernetes Warning Event",
	} {
		assert.True(t, a.CanAutoExecute(ctxFor(key)), "expected %s to enrich with neighbours", key)
	}

	// Still scoped: unrelated events do not drag the node's whole neighbourhood in.
	assert.False(t, a.CanAutoExecute(ctxFor("job_failure")))
	assert.False(t, a.CanAutoExecute(ctxFor("image_pull_backoff_reporter")))
}

// A node pinned on CPU starves everything on it while memory looks fine, so the
// payload has to carry CPU for a reader to reach the right answer.
func TestNoisyNeighboursPayloadCarriesCPUWhenMeasured(t *testing.T) {
	resp, err := noisyNeighboursResponse("p1", "ns", nil, &esNoisyNeighbourData{
		NodeName:        "node-1",
		CPUMeasured:     true,
		NodeCPUUsed:     5.994,
		NodeCPUCapacity: 6,
		CPUNeighbours: []map[string]any{
			{"pod_name": "batch-job", "namespace": "other-team", "cpu_used": 5.2},
		},
	})
	require.NoError(t, err)

	data := payloadData(t, resp)
	assert.Equal(t, 5.994, data["cpu_used"])
	assert.Equal(t, float64(6), data["cpu_allocatable"])
	neighbours, ok := data["cpu_neighbours"].([]any)
	require.True(t, ok, "cpu_neighbours missing from payload")
	require.Len(t, neighbours, 1)
	first, ok := neighbours[0].(map[string]any)
	require.True(t, ok)
	// The whole point: the culprit's namespace is named, not just the pod.
	assert.Equal(t, "other-team", first["namespace"])
}

// Elasticsearch clusters cannot report CPU here. Reporting zero would read as
// "nothing is using CPU", which is a stronger and wronger claim than silence.
func TestNoisyNeighboursOmitsCPUWhenNotMeasured(t *testing.T) {
	resp, err := noisyNeighboursResponse("p1", "ns", nil, &esNoisyNeighbourData{
		NodeName:   "node-1",
		Neighbours: []map[string]any{},
	})
	require.NoError(t, err)

	data := payloadData(t, resp)
	assert.NotContains(t, data, "cpu_used")
	assert.NotContains(t, data, "cpu_allocatable")
	assert.NotContains(t, data, "cpu_neighbours")
	// The memory half is unchanged for the existing card.
	assert.Contains(t, data, "memory_used")
}

// payloadData unwraps the response envelope, whose `data` is itself a JSON
// string, and returns the card's data object.
func payloadData(t *testing.T, resp playbooks.PlaybookActionResponse) map[string]any {
	t.Helper()
	raw, err := json.Marshal(resp)
	require.NoError(t, err)
	var envelope struct {
		Data string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &envelope))
	var payload struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(envelope.Data), &payload))
	return payload.Data
}

// Alert labels carry `instance` for the scrape target, which for a
// kube-state-metrics-sourced alert is the KSM pod's address, not a node.
// Observed live on dev: a KubePodCrashLooping event with
// instance="10.64.0.141:8080" and no node produced a card reporting the node
// as completely idle, because every query filtered on a node by that name and
// matched nothing.
func TestLooksLikeNodeNameRejectsScrapeTargets(t *testing.T) {
	for _, addr := range []string{"10.64.0.141:8080", "1.2.3.4:9100", ""} {
		assert.Falsef(t, looksLikeNodeName(addr), "%q is an address, not a node name", addr)
	}
	for _, node := range []string{
		"gke-example-cluster-default-pool-a1b2c3d4-xk9p",
		"ip-10-0-1-23.ec2.internal",
		"worker-01",
		// Some clusters really do name nodes by address. Allowed through
		// because the empty-result guard catches it if this one is not a node.
		"10.64.0.141",
	} {
		assert.Truef(t, looksLikeNodeName(node), "%q is a plausible node name", node)
	}
}
