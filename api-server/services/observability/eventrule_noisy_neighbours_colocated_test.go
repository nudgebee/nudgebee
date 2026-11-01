package observability

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The defect this file exists for: when one machine runs out of memory it kills
// several pods within minutes, and every kill was reported as an unrelated
// problem (#39083). Measured over 14 days on dev, 77 of 730 OOM events had a
// different pod killed on the same node inside 5 minutes — and 75 of those 77
// crossed a namespace, which is exactly what the namespace-scoped co-timing
// grouping cannot see.
func TestNoisyNeighboursAnnotatesCoLocatedKills(t *testing.T) {
	at := time.Date(2026, 9, 23, 4, 7, 4, 0, time.UTC)
	resp, err := noisyNeighboursResponse("openobserve-collector-gateway-collector-0", "openobserve", nil,
		&noisyNeighbourData{
			NodeName:        "gke-node-7iiz",
			NodeAllocatable: 12.06 * 1024 * 1024 * 1024,
			TotalRequested:  12.05 * 1024 * 1024 * 1024,
			Neighbours:      []map[string]any{},
			SubjectMeasured: true,
			CoKilled: []coKilledPod{{
				PodName:       "vmsingle-victoria-metrics",
				Namespace:     "victoria",
				KilledAt:      at.Add(-2 * time.Second),
				OffsetSeconds: -2,
			}},
		})
	require.NoError(t, err)

	raw, err := json.Marshal(resp)
	require.NoError(t, err)
	out := string(raw)

	// The reader must be able to see the other pod without opening its incident.
	assert.Contains(t, out, "vmsingle-victoria-metrics")
	assert.Contains(t, out, `offset_seconds\":-2`)

	// And be told what it means, rather than left to compare node names by hand.
	insights := resp.GetInsights()
	require.NotEmpty(t, insights, "a co-located kill must produce an insight")
	assert.Contains(t, insights[0].Message, "1 other pod was also OOM-killed on this node")
	assert.Contains(t, insights[0].Message, "victoria/vmsingle-victoria-metrics (2s before)")
	assert.Contains(t, insights[0].Message, "One full machine, not 2 unrelated faults.")
	assert.Equal(t, "HIGH", insights[0].Severity)
}

// With no co-located kill and a node that had memory to spare, the card must not
// imply a neighbour was to blame. Node working set exceeded 85% of allocatable on
// only 94 of 730 cards, so "the neighbours did it" is the rarer answer, not the
// default one.
func TestNoisyNeighboursSaysTheNodeWasFineWhenItWas(t *testing.T) {
	resp, err := noisyNeighboursResponse("api-1", "shop", nil, &noisyNeighbourData{
		NodeName:        "node-a",
		NodeUsed:        6 * 1024 * 1024 * 1024,
		NodeAllocatable: 12 * 1024 * 1024 * 1024,
		TotalRequested:  4 * 1024 * 1024 * 1024,
		Neighbours:      []map[string]any{},
		SubjectMeasured: true,
	})
	require.NoError(t, err)

	insights := resp.GetInsights()
	require.Len(t, insights, 1)
	assert.Contains(t, insights[0].Message, "The node was not short of memory")
	assert.Contains(t, insights[0].Message, "peaked at 50% of allocatable")
	assert.Equal(t, "INFO", insights[0].Severity)
}

// A node committed to ~100% of allocatable is the condition the issue reports as
// the norm: memory requests exceeded 95% of allocatable on 484 of 730 cards. It
// explains the kill only on a node that actually ran out.
func TestNoisyNeighboursReportsACommittedNode(t *testing.T) {
	resp, err := noisyNeighboursResponse("api-1", "shop", nil, &noisyNeighbourData{
		NodeName:        "node-a",
		NodeUsed:        11 * 1024 * 1024 * 1024,
		NodeAllocatable: 12 * 1024 * 1024 * 1024,
		TotalRequested:  11.9 * 1024 * 1024 * 1024,
		Neighbours:      []map[string]any{},
		SubjectMeasured: true,
	})
	require.NoError(t, err)

	insights := resp.GetInsights()
	require.Len(t, insights, 1)
	assert.Contains(t, insights[0].Message, "Node memory is committed to 99% of allocatable")
	assert.Equal(t, "HIGH", insights[0].Severity)
}

// Requests are a scheduling reservation; the kernel does not enforce them. A
// committed node with memory to spare had room for the pod to grow, so the card
// must not tell the reader it had none.
func TestNoisyNeighboursCommittedButIdleNodeWasNotShort(t *testing.T) {
	resp, err := noisyNeighboursResponse("api-1", "shop", nil, &noisyNeighbourData{
		NodeName:        "node-a",
		NodeUsed:        6 * 1024 * 1024 * 1024,
		NodeAllocatable: 12 * 1024 * 1024 * 1024,
		TotalRequested:  11.9 * 1024 * 1024 * 1024,
		Neighbours:      []map[string]any{},
		SubjectMeasured: true,
	})
	require.NoError(t, err)

	insights := resp.GetInsights()
	require.Len(t, insights, 1)
	assert.Contains(t, insights[0].Message, "The node was not short of memory")
	assert.Contains(t, insights[0].Message, "Requests reserve 99% of allocatable")
	assert.NotContains(t, insights[0].Message, "no headroom")
	assert.Equal(t, "INFO", insights[0].Severity)
}

// The case this was reported for: a pod OOM-killed on a node whose limits added
// up to 209% of allocatable while the node used 8% of it. The card said the
// kernel killed the pod to settle the oversubscription, and the investigation
// repeated it as the root cause. Limits are a ceiling, not usage; with no
// shortage and no other kill, the pod's own limit is the lead.
func TestNoisyNeighboursOversubscriptionOnAnIdleNodeIsNotTheCause(t *testing.T) {
	gib := float64(1024 * 1024 * 1024)
	resp, err := noisyNeighboursResponse("api-1", "shop", nil, &noisyNeighbourData{
		NodeName:        "node-a",
		NodeUsed:        1.28 * gib,
		NodeAllocatable: 16 * gib,
		TotalRequested:  11.6 * gib,
		TotalLimits:     33.44 * gib,
		Neighbours:      []map[string]any{},
		SubjectMeasured: true,
	})
	require.NoError(t, err)

	insights := resp.GetInsights()
	require.Len(t, insights, 1)
	assert.Contains(t, insights[0].Message, "The node was not short of memory — it peaked at 8% of allocatable")
	assert.Contains(t, insights[0].Message, "limits add up to 209% of allocatable (33.44 GiB)")
	assert.Contains(t, insights[0].Message, "not what killed this pod")
	assert.NotContains(t, insights[0].Message, "kernel resolves it by killing one")
	assert.Equal(t, "INFO", insights[0].Severity)
}

// Limits over allocatable and requests near it are both highlighted on the card
// and routinely true together; each gets its own "not the cause" explanation.
func TestNoisyNeighboursIdleNodeExplainsBothReservationFigures(t *testing.T) {
	gib := float64(1024 * 1024 * 1024)
	resp, err := noisyNeighboursResponse("api-1", "shop", nil, &noisyNeighbourData{
		NodeName:        "node-a",
		NodeUsed:        4 * gib,
		NodeAllocatable: 16 * gib,
		TotalRequested:  15.5 * gib,
		TotalLimits:     24 * gib,
		Neighbours:      []map[string]any{},
		SubjectMeasured: true,
	})
	require.NoError(t, err)

	insights := resp.GetInsights()
	require.Len(t, insights, 1)
	assert.Contains(t, insights[0].Message, "The node was not short of memory")
	assert.Contains(t, insights[0].Message, "limits add up to 150% of allocatable")
	assert.Contains(t, insights[0].Message, "Requests reserve 97% of allocatable")
	assert.Equal(t, "INFO", insights[0].Severity)
}

// Once the node did run out, oversubscription is how it got there and stays the
// headline — whether the shortage shows in usage or in other pods dying with it.
func TestNoisyNeighboursReportsOversubscriptionWhenTheNodeRanOut(t *testing.T) {
	gib := float64(1024 * 1024 * 1024)
	cases := map[string]*noisyNeighbourData{
		"usage near allocatable": {
			NodeUsed: 11 * gib, NodeAllocatable: 12 * gib, TotalRequested: 6 * gib, TotalLimits: 17.4 * gib,
		},
		"another pod killed on the node": {
			NodeUsed: 6 * gib, NodeAllocatable: 12 * gib, TotalRequested: 6 * gib, TotalLimits: 17.4 * gib,
			CoKilled: []coKilledPod{{PodName: "cache-0", Namespace: "shop", OffsetSeconds: -30}},
		},
	}
	for name, d := range cases {
		t.Run(name, func(t *testing.T) {
			d.NodeName, d.Neighbours, d.SubjectMeasured = "node-a", []map[string]any{}, true
			resp, err := noisyNeighboursResponse("api-1", "shop", nil, d)
			require.NoError(t, err)

			var found bool
			for _, insight := range resp.GetInsights() {
				assert.NotContains(t, insight.Message, "not short of memory")
				if strings.Contains(insight.Message, "This node is oversubscribed") {
					found = true
					assert.Contains(t, insight.Message, "145% of allocatable")
					assert.Equal(t, "HIGH", insight.Severity)
				}
			}
			assert.True(t, found, "a node that ran out with limits over allocatable must say it is oversubscribed")
		})
	}
}

// The subject was absent from its own card on 494 of 730 cards because topk(15)
// ranks by usage and a pod that just died rarely ranks. When it still cannot be
// measured, the card says so instead of leaving a silent hole.
func TestNoisyNeighboursDeclaresAnUnmeasuredSubject(t *testing.T) {
	resp, err := noisyNeighboursResponse("api-1", "shop", nil, &noisyNeighbourData{
		NodeName: "node-a", Neighbours: []map[string]any{}, SubjectMeasured: false,
	})
	require.NoError(t, err)

	raw, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `subject_measured\":false`)
	assert.Contains(t, string(raw), `subject_pod\":\"api-1\"`)

	var messages string
	for _, insight := range resp.GetInsights() {
		messages += insight.Message
	}
	assert.Contains(t, messages, "reported no memory sample on this node before it was killed")
}

// markNoisyNeighboursSubject is what lets the card pin the row the reader came
// for. It must match on pod AND namespace — two namespaces routinely run pods
// with the same name, and tagging the wrong row would be worse than tagging none.
func TestMarkNoisyNeighboursSubjectMatchesOnPodAndNamespace(t *testing.T) {
	data := &noisyNeighbourData{
		Neighbours: []map[string]any{
			{"pod_name": "api-1", "namespace": "other"},
			{"pod_name": "api-1", "namespace": "shop"},
			{"pod_name": "worker-2", "namespace": "shop"},
		},
		CPUNeighbours: []map[string]any{
			{"pod_name": "api-1", "namespace": "shop"},
		},
	}

	assert.True(t, markNoisyNeighboursSubject(data, "api-1", "shop"))
	assert.Nil(t, data.Neighbours[0]["is_subject"], "a same-named pod in another namespace is not the subject")
	assert.Equal(t, true, data.Neighbours[1]["is_subject"])
	assert.Nil(t, data.Neighbours[2]["is_subject"])
	assert.Equal(t, true, data.CPUNeighbours[0]["is_subject"], "the CPU list is pinned too")

	assert.False(t, markNoisyNeighboursSubject(&noisyNeighbourData{}, "api-1", "shop"))
}

// The list is ranked by usage and cut to the top N, and the pod that just died is
// frequently not in it — which is how the subject went missing from its own card.
// The cut must never be what removes it.
func TestTrimKeepingSubject(t *testing.T) {
	ranked := func(n int) []map[string]any {
		rows := make([]map[string]any, 0, n)
		for i := range n {
			rows = append(rows, map[string]any{"pod_name": fmt.Sprintf("hog-%d", i), "namespace": "demo"})
		}
		return rows
	}

	t.Run("keeps the subject when the cut would have dropped it", func(t *testing.T) {
		rows := append(ranked(noisyNeighboursTopN), map[string]any{"pod_name": "api-1", "namespace": "shop"})

		trimmed := trimKeepingSubject(rows, "api-1", "shop")

		require.Len(t, trimmed, noisyNeighboursTopN+1)
		assert.Equal(t, "api-1", trimmed[noisyNeighboursTopN]["pod_name"],
			"the subject is appended after the ranked rows, not sorted into them")
	})

	t.Run("keeps every container of the subject", func(t *testing.T) {
		rows := append(ranked(noisyNeighboursTopN),
			map[string]any{"pod_name": "api-1", "namespace": "shop", "name": "app"},
			map[string]any{"pod_name": "api-1", "namespace": "shop", "name": "sidecar"})

		trimmed := trimKeepingSubject(rows, "api-1", "shop")

		assert.Len(t, trimmed, noisyNeighboursTopN+2, "a pod killed by its sidecar has to be visible")
	})

	t.Run("does not duplicate a subject that already ranked", func(t *testing.T) {
		rows := append([]map[string]any{{"pod_name": "api-1", "namespace": "shop"}}, ranked(noisyNeighboursTopN)...)

		trimmed := trimKeepingSubject(rows, "api-1", "shop")

		assert.Len(t, trimmed, noisyNeighboursTopN)
	})

	t.Run("a same-named pod in another namespace is not the subject", func(t *testing.T) {
		rows := append(ranked(noisyNeighboursTopN), map[string]any{"pod_name": "api-1", "namespace": "other"})

		trimmed := trimKeepingSubject(rows, "api-1", "shop")

		assert.Len(t, trimmed, noisyNeighboursTopN)
	})

	t.Run("leaves a short list alone", func(t *testing.T) {
		assert.Len(t, trimKeepingSubject(ranked(3), "api-1", "shop"), 3)
	})
}

// A pod with three containers is one pod on the node, not three.
func TestDistinctPodsCountsPodsNotSeries(t *testing.T) {
	samples := []latestSample{
		{Metric: map[string]string{"pod": "api-1", "namespace": "shop", "container": "app"}},
		{Metric: map[string]string{"pod": "api-1", "namespace": "shop", "container": "sidecar"}},
		{Metric: map[string]string{"pod": "api-1", "namespace": "other", "container": "app"}},
		{Metric: map[string]string{"pod": "", "namespace": "shop"}},
	}

	assert.Equal(t, 2, distinctPods(samples))
}

// "2s before" is the whole point — it is what turns two rows into one story. A
// bare timestamp would leave the reader subtracting.
func TestRelativeOffsetReadsAsTimeRelativeToTheKill(t *testing.T) {
	for _, tc := range []struct {
		seconds int
		want    string
	}{
		{-2, "2s before"},
		{45, "45s after"},
		{-120, "2m before"},
		{65, "1m 5s after"},
		{0, "at the same moment"},
	} {
		assert.Equal(t, tc.want, relativeOffset(tc.seconds))
	}
}

// A long list is capped for display, but the count in the insight must still be
// the true one — a full node can kill more pods than are worth listing.
func TestCoKilledListIsCappedWithoutLosingTheCount(t *testing.T) {
	kills := make([]coKilledPod, 0, coLocatedMaxListed+3)
	for i := range cap(kills) {
		kills = append(kills, coKilledPod{
			PodName:       "pod-" + string(rune('a'+i)),
			Namespace:     "demo",
			OffsetSeconds: i + 1,
		})
	}

	assert.Len(t, coKilledPayload(kills), coLocatedMaxListed)
	assert.Contains(t, describeCoKilled(kills), "and 3 more")

	resp, err := noisyNeighboursResponse("api-1", "shop", nil, &noisyNeighbourData{
		NodeName: "node-a", Neighbours: []map[string]any{}, SubjectMeasured: true, CoKilled: kills,
	})
	require.NoError(t, err)
	assert.Contains(t, resp.GetInsights()[0].Message, "13 other pods were")
}

// The enricher also runs on throttling and never-ready alerts. The annotation
// counts this event as one of the kills ("not N unrelated faults"), which is only
// true when this event is itself an OOM kill.
func TestCoLocatedLookupOnlyRunsForOOMEvents(t *testing.T) {
	assert.True(t, isCoLocatedOOMKey("pod_oom_killer_enricher"))
	for _, key := range []string{"CPUThrottlingHigh", "KubePodNotReady", "KubeContainerWaiting", "report_crash_loop", ""} {
		assert.False(t, isCoLocatedOOMKey(key), "%s is not an OOM kill", key)
	}
}
