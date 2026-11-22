package observability

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const gib = 1024 * 1024 * 1024

// subjectRow is one container of the pod the card is about, as buildNoisyNeighbourData
// and markNoisyNeighboursSubject leave it.
func subjectRow(container string, used, limit float64) map[string]any {
	return map[string]any{
		"name": container, "pod_name": "subject", "namespace": "ns",
		"memory_used": used, "memory_limit": limit, "is_subject": true,
	}
}

// The case this exists for: two pods in different namespaces OOM-killed seconds apart,
// neither near its own limit, on a node whose containers peaked above 90% of
// allocatable. Both are the node running out, not a pod outgrowing its limit — and
// grouping reads this verdict to make them one incident.
func TestOOMCause(t *testing.T) {
	neighbour := map[string]any{"name": "c", "pod_name": "other", "namespace": "other", "memory_used": 8 * gib, "memory_limit": 8.5 * gib}

	cases := []struct {
		name string
		data noisyNeighbourData
		want string
	}{
		{
			name: "below its own limit on a full node",
			data: noisyNeighbourData{
				NodeUsed: 11 * gib, NodeAllocatable: 12 * gib, SubjectMeasured: true,
				Neighbours: []map[string]any{neighbour, subjectRow("agent", 0.4*gib, 1*gib)},
			},
			want: oomCauseNodeMemory,
		},
		{
			name: "no limit at all on a full node",
			data: noisyNeighbourData{
				NodeUsed: 11 * gib, NodeAllocatable: 12 * gib, SubjectMeasured: true,
				Neighbours: []map[string]any{subjectRow("app", 2*gib, 0)},
			},
			want: oomCauseNodeMemory,
		},
		{
			// A neighbour at its own limit says nothing about the subject.
			name: "at its own limit, even on a full node",
			data: noisyNeighbourData{
				NodeUsed: 11 * gib, NodeAllocatable: 12 * gib, SubjectMeasured: true,
				Neighbours: []map[string]any{subjectRow("app", 0.95*gib, 1*gib)},
			},
			want: oomCauseOwnLimit,
		},
		{
			// Which container the kernel killed is unknown, so one container at its
			// limit is enough to keep the node out of it.
			name: "one container of several at its limit",
			data: noisyNeighbourData{
				NodeUsed: 11 * gib, NodeAllocatable: 12 * gib, SubjectMeasured: true,
				Neighbours: []map[string]any{subjectRow("sidecar", 0.1*gib, 1*gib), subjectRow("app", 0.99*gib, 1*gib)},
			},
			want: oomCauseOwnLimit,
		},
		{
			// A NaN reading on one container must not hide another at its limit.
			name: "one container unreadable, another at its limit",
			data: noisyNeighbourData{
				NodeUsed: 11 * gib, NodeAllocatable: 12 * gib, SubjectMeasured: true,
				Neighbours: []map[string]any{subjectRow("sidecar", math.NaN(), 1*gib), subjectRow("app", 0.99*gib, 1*gib)},
			},
			want: oomCauseOwnLimit,
		},
		{
			name: "below its limit on a node with memory to spare",
			data: noisyNeighbourData{
				NodeUsed: 6 * gib, NodeAllocatable: 12 * gib, SubjectMeasured: true,
				Neighbours: []map[string]any{subjectRow("app", 0.4*gib, 1*gib)},
			},
			want: oomCauseUndetermined,
		},
		{
			// No sample for the pod is what a kill between two scrapes looks like;
			// nothing says whether it was at its limit.
			name: "subject never measured",
			data: noisyNeighbourData{
				NodeUsed: 11 * gib, NodeAllocatable: 12 * gib, SubjectMeasured: false,
				Neighbours: []map[string]any{neighbour},
			},
			want: oomCauseUndetermined,
		},
		{
			name: "node allocatable unknown",
			data: noisyNeighbourData{
				NodeUsed: 11 * gib, SubjectMeasured: true,
				Neighbours: []map[string]any{subjectRow("app", 0.4*gib, 1*gib)},
			},
			want: oomCauseUndetermined,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, oomCause(&tc.data))
		})
	}
}

// Grouping reads oom_cause off the stored card, so it must be on the payload when
// set and absent when not — an empty string would read as a verdict.
func TestNoisyNeighboursPayloadCarriesOOMCauseOnlyWhenSet(t *testing.T) {
	decode := func(d *noisyNeighbourData) map[string]any {
		resp, err := noisyNeighboursResponse("subject", "ns", nil, d)
		require.NoError(t, err)
		card, ok := resp.(playbooks.PlaybookActionResponseJson)
		require.True(t, ok)
		var payload struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal([]byte(card.Data), &payload))
		return payload.Data
	}

	set := decode(&noisyNeighbourData{NodeName: "n1", Neighbours: []map[string]any{}, OOMCause: oomCauseNodeMemory})
	assert.Equal(t, oomCauseNodeMemory, set["oom_cause"])

	unset := decode(&noisyNeighbourData{NodeName: "n1", Neighbours: []map[string]any{}})
	assert.NotContains(t, unset, "oom_cause")
}

const mib = 1024 * 1024

// ownLimitOnFullNode is the case #40880 was reported for: a pod killed at 90% of its
// own 128Mi limit on a node at 96% of allocatable with limits at 241% of it.
func ownLimitOnFullNode() *noisyNeighbourData {
	d := &noisyNeighbourData{
		NodeName: "n1", NodeUsed: 11.6 * gib, NodeAllocatable: 12.06 * gib,
		TotalRequested: 8.9 * gib, TotalLimits: 29.11 * gib, SubjectMeasured: true,
		Neighbours: []map[string]any{subjectRow("app", 115.7*mib, 128*mib)},
	}
	d.OOMCause = oomCause(d)
	return d
}

func insightsOf(t *testing.T, d *noisyNeighbourData) []playbooks.PlaybookActionResponseInsight {
	resp, err := noisyNeighboursResponse("subject", "ns", nil, d)
	require.NoError(t, err)
	return resp.GetInsights()
}

// The headline used to be chosen from node figures alone, so this pod was told the
// node was oversubscribed and "the kernel resolves it by killing one".
func TestNoisyNeighboursInsightsLeadWithOwnLimit(t *testing.T) {
	d := ownLimitOnFullNode()
	require.Equal(t, oomCauseOwnLimit, d.OOMCause)

	insights := insightsOf(t, d)
	require.Len(t, insights, 2)
	assert.Contains(t, insights[0].Message, "This pod reached its own memory limit: container app peaked at 115.7 MiB of its 128.0 MiB limit (90%)")
	assert.Equal(t, "HIGH", insights[0].Severity)
	assert.Equal(t, "Node memory also peaked at 96% of allocatable (11.60 GiB of 12.06 GiB) in the window before the kill.", insights[1].Message)
	assert.Equal(t, "MEDIUM", insights[1].Severity)
	for _, in := range insights {
		assert.NotContains(t, in.Message, "oversubscribed")
		assert.NotContains(t, in.Message, "killing one")
		assert.NotContains(t, in.Message, "no headroom")
	}
}

// Other pods dying on the node is still reported, but not as "one full machine" when
// the card's verdict is that this pod hit its own limit.
func TestNoisyNeighboursCoKillsDoNotOverrideOwnLimit(t *testing.T) {
	d := ownLimitOnFullNode()
	d.NodeUsed = 6 * gib // short only through the co-kill
	d.CoKilled = []coKilledPod{{PodName: "other", Namespace: "other", OffsetSeconds: -30}}

	insights := insightsOf(t, d)
	require.Len(t, insights, 2)
	assert.Contains(t, insights[0].Message, "This pod reached its own memory limit")
	assert.Contains(t, insights[1].Message, "1 other pod was also OOM-killed on this node")
	assert.NotContains(t, insights[1].Message, "One full machine")
	assert.Equal(t, "MEDIUM", insights[1].Severity)
}

// The node reading is unchanged when the verdict is the node.
func TestNoisyNeighboursNodeVerdictStillNamesOversubscription(t *testing.T) {
	d := ownLimitOnFullNode()
	d.Neighbours = []map[string]any{subjectRow("app", 60*mib, 128*mib)}
	d.OOMCause = oomCause(d)
	require.Equal(t, oomCauseNodeMemory, d.OOMCause)

	insights := insightsOf(t, d)
	require.Len(t, insights, 1)
	assert.Contains(t, insights[0].Message, "This node is oversubscribed")
	assert.NotContains(t, insights[0].Message, "own memory limit")
}

// The subject's memory peak is read at 15s, not the window's derived 60s: over
// minute-spaced points the peak is whatever sample a minute boundary lands on, which
// read pods at 63-84% of their limit when they were at 96-100%. Everything else keeps
// the derived step.
func TestNoisyNeighboursReadsSubjectPeakAtFineStep(t *testing.T) {
	var asked []GetUtilisationTrendRequest
	orig := fetchNoisyNeighbourUtilisation
	t.Cleanup(func() { fetchNoisyNeighbourUtilisation = orig })
	fetchNoisyNeighbourUtilisation = func(_ *security.RequestContext, req GetUtilisationTrendRequest) (OutputMetricQuery, error) {
		asked = append(asked, req)
		return OutputMetricQuery{Results: []QueryResult{{
			QueryKey: "mem_total",
			Payload:  []Result{{Metric: map[string]string{"node": "n1"}, Values: []float64{12 * gib}}},
		}}}, nil
	}

	end := time.Date(2026, 10, 10, 19, 58, 13, 0, time.UTC)
	_, err := fetchNoisyNeighbours(security.NewRequestContextForSuperAdmin(nil, nil, nil), "acc", "n1", "subject", "ns", end.Add(-10*time.Minute), end)
	require.NoError(t, err)

	require.Len(t, asked, 3)
	assert.Equal(t, "max", asked[0].Aggregate)
	assert.Zero(t, asked[0].Step)
	assert.Equal(t, "max", asked[1].Aggregate)
	assert.Equal(t, subjectPeakStepSeconds, asked[1].Step)
	assert.Equal(t, []string{"container_memory_working_set_by_pod"}, asked[1].Request["metrics"])
	assert.Equal(t, "avg", asked[2].Aggregate)
	assert.Zero(t, asked[2].Step)
}

// The fine reading replaces the subject's row only. The node total stays the sum of
// the coarse readings: a finer step raises every container's peak, and their sum
// overstates the node's peak further, which would move the node verdict.
func TestNoisyNeighboursFineSubjectPeakStaysOutOfNodeTotal(t *testing.T) {
	sample := func(pod, container string, v float64) latestSample {
		return latestSample{Metric: map[string]string{"namespace": "ns", "pod": pod, "container": container}, Value: v}
	}
	d, err := buildNoisyNeighbourData("n1", "subject", "ns", map[string][]latestSample{
		"top_pods":     {sample("subject", "app", 83.6*mib), sample("other", "c", 2*gib)},
		"subject_peak": {sample("subject", "app", 115.7*mib), sample("other", "c", 3*gib)},
		"mem_limits":   {sample("subject", "app", 128*mib)},
		"node_alloc":   {{Value: 12 * gib}},
	}, false)
	require.NoError(t, err)

	assert.InDelta(t, 83.6*mib+2*gib, d.NodeUsed, 1)
	for _, row := range d.Neighbours {
		switch row["pod_name"] {
		case "subject":
			assert.InDelta(t, 115.7*mib, row["memory_used"], 1)
		case "other":
			assert.InDelta(t, 2*gib, row["memory_used"], 1)
		}
	}
	assert.Equal(t, oomCauseOwnLimit, oomCause(d))
}
