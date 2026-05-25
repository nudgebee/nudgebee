package playbooks

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The relay's prometheus_queries_enricher emits the Robusta-coerced
// `{"timestamp": <float>, "value": "<str>"}` object for instant samples.
// Older code paths may still emit the standard Prometheus `[ts, "v"]`
// tuple. The parser must accept both — see action_noisy_neighbours.go.
func TestParseInstantValue(t *testing.T) {
	cases := map[string]struct {
		raw    any
		want   float64
		wantOK bool
	}{
		"robusta_object_shape": {
			raw:    map[string]any{"timestamp": float64(1778576133), "value": "12945174528"},
			want:   12945174528,
			wantOK: true,
		},
		"prometheus_tuple_shape": {
			raw:    []any{float64(1778576133), "6489419776"},
			want:   6489419776,
			wantOK: true,
		},
		"object_missing_value": {
			raw:    map[string]any{"timestamp": float64(1)},
			wantOK: false,
		},
		"object_non_numeric": {
			raw:    map[string]any{"value": "NaNbanana"},
			wantOK: false,
		},
		"tuple_too_short": {
			raw:    []any{float64(1)},
			wantOK: false,
		},
		"nil": {
			raw:    nil,
			wantOK: false,
		},
		"unknown_type": {
			raw:    "not-a-sample",
			wantOK: false,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := parseInstantValue(tc.raw)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

// Regression for Stage-2.2 server-side enricher empty-data bug:
// the relay returned 15 series but every entry got skipped because
// vectorResultEntries asserted `value.([]any)` against the actual
// `{"timestamp", "value"}` object payload.
func TestVectorResultEntries_AcceptsBothShapes(t *testing.T) {
	t.Run("robusta_object_shape", func(t *testing.T) {
		raw := map[string]any{
			"vector_result": []any{
				map[string]any{
					"metric": map[string]any{"namespace": "ns-a", "pod": "pod-a"},
					"value":  map[string]any{"timestamp": float64(1), "value": "100"},
				},
				map[string]any{
					"metric": map[string]any{"namespace": "ns-b", "pod": "pod-b"},
					"value":  map[string]any{"timestamp": float64(1), "value": "200"},
				},
			},
		}
		entries := vectorResultEntries(raw)
		assert.Len(t, entries, 2)
		assert.Equal(t, float64(100), entries[0].value)
		assert.Equal(t, float64(200), entries[1].value)
		assert.Equal(t, "pod-a", entries[0].metric["pod"])
	})

	t.Run("prometheus_tuple_shape", func(t *testing.T) {
		raw := map[string]any{
			"vector_result": []any{
				map[string]any{
					"metric": map[string]any{"pod": "pod-x"},
					"value":  []any{float64(1), "42"},
				},
			},
		}
		entries := vectorResultEntries(raw)
		assert.Len(t, entries, 1)
		assert.Equal(t, float64(42), entries[0].value)
	})

	t.Run("malformed_entries_skipped", func(t *testing.T) {
		raw := map[string]any{
			"vector_result": []any{
				map[string]any{"metric": map[string]any{}, "value": "not-a-sample"},
				map[string]any{"metric": map[string]any{}, "value": map[string]any{}},
				map[string]any{"metric": map[string]any{"pod": "good"}, "value": map[string]any{"value": "7"}},
			},
		}
		entries := vectorResultEntries(raw)
		assert.Len(t, entries, 1)
		assert.Equal(t, "good", entries[0].metric["pod"])
	})

	t.Run("nil_input", func(t *testing.T) {
		assert.Empty(t, vectorResultEntries(nil))
		assert.Empty(t, vectorResultEntries(map[string]any{}))
	})

	// Regression for the chronic "Noisy Neighbours empty / pod_metric
	// requests=0,limits=0" symptom: the Go-agent forager returns a bare
	// Prometheus result array for instant + success
	// (nudgebee-agent/pkg/enrichers/prometheus.go:114-118), not the
	// wrapped {vector_result: [...]} envelope. The original parser only
	// handled the wrapped form, so every instant-query caller saw zero
	// entries.
	t.Run("bare_instant_array", func(t *testing.T) {
		raw := []any{
			map[string]any{
				"metric": map[string]any{"namespace": "demo", "pod": "load-gen", "container": "load-generator"},
				"value":  []any{float64(1779357970), "1148727296"},
			},
			map[string]any{
				"metric": map[string]any{"namespace": "nudgebee", "pod": "benchmark", "container": "benchmark-server"},
				"value":  map[string]any{"timestamp": float64(1779357970), "value": "919887872"},
			},
		}
		entries := vectorResultEntries(raw)
		assert.Len(t, entries, 2)
		assert.Equal(t, float64(1148727296), entries[0].value)
		assert.Equal(t, "load-generator", entries[0].metric["container"])
		assert.Equal(t, float64(919887872), entries[1].value)
	})

	t.Run("bare_instant_array_empty", func(t *testing.T) {
		assert.Empty(t, vectorResultEntries([]any{}))
	})
}

func TestFirstInstantValue_AcceptsObjectShape(t *testing.T) {
	raw := map[string]any{
		"vector_result": []any{
			map[string]any{
				"metric": map[string]any{},
				"value":  map[string]any{"timestamp": float64(1), "value": "12345"},
			},
		},
	}
	assert.Equal(t, float64(12345), firstInstantValue(raw))
	assert.Equal(t, float64(0), firstInstantValue(nil))

	// Bare-instant array — the shape the Go-agent emits for instant+success.
	bare := []any{
		map[string]any{
			"metric": map[string]any{},
			"value":  []any{float64(1), "12945174528"},
		},
	}
	assert.Equal(t, float64(12945174528), firstInstantValue(bare))
}

// indexByPodContainer is the join-key builder for merging
// kube_pod_container_resource_{requests,limits} into the per-container
// neighbours list. UI needs `name`, `memory_requested`, `memory_limit`
// per neighbour — without this index the resource values never attach
// and the card renders "Container undefined".
func TestIndexByPodContainer(t *testing.T) {
	raw := map[string]any{
		"vector_result": []any{
			map[string]any{
				"metric": map[string]any{
					"namespace": "ns-a",
					"pod":       "pod-a",
					"container": "app",
				},
				"value": map[string]any{"timestamp": float64(1), "value": "104857600"},
			},
			map[string]any{
				"metric": map[string]any{
					"namespace": "ns-a",
					"pod":       "pod-a",
					"container": "sidecar",
				},
				"value": map[string]any{"timestamp": float64(1), "value": "52428800"},
			},
			map[string]any{
				// Missing container — must be skipped.
				"metric": map[string]any{
					"namespace": "ns-b",
					"pod":       "pod-b",
				},
				"value": map[string]any{"timestamp": float64(1), "value": "999"},
			},
		},
	}
	idx := indexByPodContainer(raw)
	assert.Equal(t, float64(104857600), idx["ns-a/pod-a/app"])
	assert.Equal(t, float64(52428800), idx["ns-a/pod-a/sidecar"])
	_, hasMissing := idx["ns-b/pod-b/"]
	assert.False(t, hasMissing, "entries without container label must be skipped")
	assert.Len(t, idx, 2)

	assert.Empty(t, indexByPodContainer(nil))
	assert.Empty(t, indexByPodContainer(map[string]any{}))
}
