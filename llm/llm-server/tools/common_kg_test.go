package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	testKGTraverseSeedName = "llm-server"
	testKGNodeID           = "abc-123"
)

func TestFormatKGSearchResponse(t *testing.T) {
	t.Run("empty result", func(t *testing.T) {
		out := formatKGSearchResponse(map[string]any{
			"nodes":       []any{},
			"total_count": 0,
		}, nil)
		assert.Contains(t, out, "No nodes matched")
	})

	t.Run("renders table with ids and falls back to UUID when no name map", func(t *testing.T) {
		data := map[string]any{
			"nodes": []any{
				map[string]any{
					"id":               "abc123def456",
					"name":             "redis-master",
					"node_type":        "Workload",
					"namespace":        "nudgebee",
					"source":           "k8s",
					"cloud_account_id": "111122223333",
				},
				map[string]any{
					"id":               "xyz789abc123",
					"name":             "redis-slave",
					"node_type":        "Workload",
					"namespace":        "nudgebee",
					"source":           "k8s",
					"cloud_account_id": "444455556666",
				},
			},
			"total_count": 2,
		}
		// No name map → expect UUIDs in Account column.
		out := formatKGSearchResponse(data, nil)
		assert.Contains(t, out, "Found 2 nodes")
		assert.Contains(t, out, "redis-master")
		assert.Contains(t, out, "redis-slave")
		// Full node IDs present (needed for kg_traverse chaining).
		assert.Contains(t, out, "abc123def456")
		assert.Contains(t, out, "xyz789abc123")
		assert.Contains(t, out, "Account")
		assert.Contains(t, out, "111122223333")
		assert.Contains(t, out, "444455556666")
	})

	const accountNameAwsProd = "aws-prod"

	t.Run("renders account names when name map supplied", func(t *testing.T) {
		data := map[string]any{
			"nodes": []any{
				map[string]any{
					"id":               "abc",
					"name":             "redis",
					"node_type":        "Workload",
					"namespace":        "ns",
					"source":           "k8s",
					"cloud_account_id": "111122223333",
				},
			},
			"total_count": 1,
		}
		nameMap := map[string]string{"111122223333": accountNameAwsProd}
		out := formatKGSearchResponse(data, nameMap)
		// Account column now shows the friendly name.
		assert.Contains(t, out, accountNameAwsProd)
		// UUID is no longer shown in the Account column when name is available.
		assert.NotContains(t, out, "111122223333")
		// Node id still rendered so the LLM can chain.
		assert.Contains(t, out, "abc")
	})

	t.Run("falls back to UUID when account_id not in name map", func(t *testing.T) {
		data := map[string]any{
			"nodes": []any{
				map[string]any{
					"id":               "abc",
					"name":             "redis",
					"node_type":        "Workload",
					"source":           "k8s",
					"cloud_account_id": "unmapped-uuid-here",
				},
			},
			"total_count": 1,
		}
		out := formatKGSearchResponse(data, map[string]string{"other-uuid": accountNameAwsProd})
		// Unmapped UUID falls back to raw form.
		assert.Contains(t, out, "unmapped-uuid-here")
	})

	t.Run("indicates when total exceeds returned", func(t *testing.T) {
		data := map[string]any{
			"nodes": []any{
				map[string]any{"id": "a", "name": "n1", "node_type": "Workload", "source": "k8s"},
			},
			"total_count": 42,
		}
		out := formatKGSearchResponse(data, nil)
		assert.Contains(t, out, "Found 42 nodes (showing 1)")
	})

	t.Run("character cap is enforced", func(t *testing.T) {
		nodes := make([]any, 0, 500)
		for i := 0; i < 500; i++ {
			nodes = append(nodes, map[string]any{
				"id":        "id-aaaaaaaaaa",
				"name":      strings.Repeat("nnnnn", 5),
				"node_type": "Workload",
				"namespace": "ns-yyy",
				"source":    "k8s",
			})
		}
		out := formatKGSearchResponse(map[string]any{"nodes": nodes, "total_count": 500}, nil)
		// The cap is a soft cap — we allow some overshoot when writing the footer.
		assert.Contains(t, out, "[output truncated")
	})
}

func TestFormatKGTraverseResponse(t *testing.T) {
	t.Run("empty graph", func(t *testing.T) {
		out := formatKGTraverseResponse(map[string]any{
			"data":          map[string]any{"nodes": []any{}, "edges": []any{}},
			"seed_node_ids": []any{},
		}, nil, 50)
		assert.Contains(t, out, "No nodes matched")
	})

	t.Run("renders relationships with inline ids", func(t *testing.T) {
		data := map[string]any{
			"data": map[string]any{
				"nodes": []any{
					map[string]any{"id": "abc", "kind": "Workload", "name": testKGTraverseSeedName},
					map[string]any{"id": "def", "kind": "Namespace", "name": "nudgebee"},
					map[string]any{"id": "ghi", "kind": "Cluster", "name": "k8s-dev"},
				},
				"edges": []any{
					map[string]any{
						"source_node_id":    "abc",
						"dest_node_id":      "def",
						"relationship_type": "RUNS_ON",
					},
					map[string]any{
						"source_node_id":    "def",
						"dest_node_id":      "ghi",
						"relationship_type": "RUNS_ON",
					},
				},
			},
			"seed_node_ids":    []any{"abc"},
			"truncated":        false,
			"total_discovered": 3,
		}
		out := formatKGTraverseResponse(data, nil, 50)
		assert.Contains(t, out, "Traversal from")
		assert.Contains(t, out, testKGTraverseSeedName)
		assert.Contains(t, out, "RUNS_ON")
		assert.Contains(t, out, "Truncated: false")
		assert.Contains(t, out, "1 Cluster")
		assert.Contains(t, out, "1 Namespace")
		assert.Contains(t, out, "1 Workload")
	})

	t.Run("truncation surfaced with totals and applied limit", func(t *testing.T) {
		data := map[string]any{
			"data": map[string]any{
				"nodes": []any{
					map[string]any{"id": "abc", "kind": "Workload", "name": testKGTraverseSeedName},
				},
				"edges": []any{},
			},
			"seed_node_ids":    []any{"abc"},
			"truncated":        true,
			"total_discovered": 250,
		}
		out := formatKGTraverseResponse(data, nil, 50)
		assert.Contains(t, out, "Truncated: true")
		assert.Contains(t, out, "total_matches=250")
		assert.Contains(t, out, "result_limit=50")
		assert.Contains(t, out, "refine the query before acting")
	})

	t.Run("applied exclude filters reported", func(t *testing.T) {
		data := map[string]any{
			"data": map[string]any{
				"nodes": []any{
					map[string]any{"id": "abc", "kind": "LoadBalancer", "name": "my-lb"},
				},
				"edges": []any{},
			},
			"seed_node_ids":    []any{"abc"},
			"truncated":        false,
			"total_discovered": 1,
		}
		out := formatKGTraverseResponse(data, []string{"SecurityGroup", "NetworkInterface", "Subnet"}, 50)
		assert.Contains(t, out, "Applied filters: exclude_node_types=[SecurityGroup, NetworkInterface, Subnet]")
	})
}

func TestFormatKGGetNodeResponse(t *testing.T) {
	t.Run("missing id renders not found", func(t *testing.T) {
		out := formatKGGetNodeResponse(map[string]any{})
		assert.Equal(t, "Node not found.", out)
	})

	t.Run("minimal node renders header without label/property sections", func(t *testing.T) {
		out := formatKGGetNodeResponse(map[string]any{
			"id":        testKGNodeID,
			"name":      testKGTraverseSeedName,
			"node_type": "Workload",
		})
		assert.Contains(t, out, fmt.Sprintf("**%s** (Workload) — id: %s", testKGTraverseSeedName, testKGNodeID))
		assert.NotContains(t, out, "Labels:")
		assert.NotContains(t, out, "Properties:")
	})

	t.Run("full node renders all sections with sorted keys and rendered nested JSON", func(t *testing.T) {
		out := formatKGGetNodeResponse(map[string]any{
			"id":               testKGNodeID,
			"name":             testKGTraverseSeedName,
			"node_type":        "Workload",
			"namespace":        "nudgebee",
			"cluster":          "k8s-dev",
			"source":           "k8s",
			"cloud_account_id": "111122223333",
			"category":         "compute",
			"level":            "namespace",
			"labels": map[string]any{
				"app":        testKGTraverseSeedName,
				"managed-by": "helm",
			},
			"properties": map[string]any{
				"replicas":  float64(3),
				"available": true,
				"image":     "ecr/llm-server:1.0",
				"ports": map[string]any{
					"http": float64(9999),
				},
			},
		})

		// Header + metadata
		assert.Contains(t, out, fmt.Sprintf("**%s** (Workload) — id: %s", testKGTraverseSeedName, testKGNodeID))
		assert.Contains(t, out, "- Namespace: nudgebee")
		assert.Contains(t, out, "- Cluster: k8s-dev")
		assert.Contains(t, out, "- Source: k8s")
		assert.Contains(t, out, "- Account: 111122223333")
		assert.Contains(t, out, "- Category: compute")
		assert.Contains(t, out, "- Level: namespace")

		// Labels — sorted (app before managed-by)
		assert.Contains(t, out, "Labels:")
		assert.Contains(t, out, "- app: "+testKGTraverseSeedName)
		assert.Contains(t, out, "- managed-by: helm")
		assert.Less(t, strings.Index(out, "- app:"), strings.Index(out, "- managed-by:"))

		// Properties — scalars rendered inline, ints without ".0", nested map JSON-marshalled
		assert.Contains(t, out, "Properties:")
		assert.Contains(t, out, "- available: true")
		assert.Contains(t, out, "- image: ecr/llm-server:1.0")
		assert.Contains(t, out, "- replicas: 3")
		assert.Contains(t, out, `- ports: {"http":9999}`)
	})

	t.Run("character cap is enforced on properties section", func(t *testing.T) {
		props := map[string]any{}
		for i := 0; i < 500; i++ {
			props[fmt.Sprintf("key-%04d", i)] = strings.Repeat("v", 50)
		}
		out := formatKGGetNodeResponse(map[string]any{
			"id":         testKGNodeID,
			"name":       "fat-node",
			"node_type":  "Workload",
			"properties": props,
		})
		assert.Contains(t, out, "[output truncated")
	})
}
