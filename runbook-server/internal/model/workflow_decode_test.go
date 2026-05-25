package model_test

import (
	"testing"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDecodeWorkflow_PrevEdgesShapes locks in the contract that `_prev_edges`
// is opaque on the wire. The frontend has historically shipped two shapes for
// this field (legacy flat `[]StashedEdge` from the original disable PR, and
// the current `{originals, splices?}` from the splice-on-disable PR). The
// backend must round-trip both without rejecting either, because the field is
// UI metadata and `app/src/components1/workflow/utils/toggleTaskDisable.ts`
// (`unpackStash`) is the canonical reader.
//
// If this test starts failing, do NOT narrow `model.Task.PrevEdges` back to a
// concrete slice type. Either the frontend has converged on a single shape
// and migrated saved workflows (in which case update this test alongside the
// type), or someone is reintroducing the bug fixed by relaxing the type.
func TestDecodeWorkflow_PrevEdgesShapes(t *testing.T) {
	// Each case mirrors the shape `args["workflow"].(map[string]any)` that
	// arrives in `runbook-server/api/hasura.go` (handleCreateWorkflow /
	// handleUpdateWorkflow) after Hasura deserializes the GraphQL input.
	tests := []struct {
		name      string
		prevEdges any
	}{
		{
			name: "legacy flat array",
			prevEdges: []any{
				map[string]any{
					"source": "trigger-optimization-0",
					"target": "notifications_im",
					"type":   "smoothstep",
				},
			},
		},
		{
			name: "object with originals only (leaf-task disable)",
			prevEdges: map[string]any{
				"originals": []any{
					map[string]any{
						"source": "trigger-optimization-0",
						"target": "notifications_im",
						"type":   "smoothstep",
					},
				},
			},
		},
		{
			name: "object with originals and splices (splice flow)",
			prevEdges: map[string]any{
				"originals": []any{
					map[string]any{
						"source": "task_a",
						"target": "task_b",
						"type":   "smoothstep",
					},
				},
				"splices": []any{
					map[string]any{
						"source": "task_a",
						"target": "task_c",
						"type":   "smoothstep",
					},
				},
			},
		},
		{
			name:      "absent (nil)",
			prevEdges: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			task := map[string]any{
				"id":       "notifications_im",
				"type":     "notifications.im",
				"disabled": true,
			}
			if tc.prevEdges != nil {
				task["_prev_edges"] = tc.prevEdges
			}

			payload := map[string]any{
				"name":   "decode round-trip test",
				"status": "ACTIVE",
				"definition": map[string]any{
					"version":  "v1",
					"triggers": []any{map[string]any{"type": "manual"}},
					"tasks":    []any{task},
				},
			}

			var workflow model.Workflow
			err := common.DecodeMapToStruct(payload, &workflow)
			require.NoError(t, err, "decode must accept any _prev_edges shape")

			require.Len(t, workflow.Definition.Tasks, 1)
			assert.Equal(t, tc.prevEdges, workflow.Definition.Tasks[0].PrevEdges)
		})
	}
}
