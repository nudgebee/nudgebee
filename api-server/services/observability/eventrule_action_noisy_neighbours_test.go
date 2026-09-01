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
