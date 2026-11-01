package observability

import (
	"encoding/json"
	"testing"

	"nudgebee/services/eventrule/playbooks"

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
