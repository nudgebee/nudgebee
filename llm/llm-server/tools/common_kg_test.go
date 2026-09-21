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

	t.Run("name/namespace/cluster nested under properties render in header and metadata", func(t *testing.T) {
		out := formatKGGetNodeResponse(map[string]any{
			"id":        testKGNodeID,
			"node_type": "Workload",
			"properties": map[string]any{
				"name":      testKGTraverseSeedName,
				"namespace": "nudgebee",
				"cluster":   "k8s-dev",
			},
		})
		assert.Contains(t, out, fmt.Sprintf("**%s** (Workload) — id: %s", testKGTraverseSeedName, testKGNodeID))
		assert.NotContains(t, out, "(unnamed)")
		assert.Contains(t, out, "- Namespace: nudgebee")
		assert.Contains(t, out, "- Cluster: k8s-dev")
	})
}

// resolveAccountIdentifiers is what stands between the LLM's `account_ids` and
// a uuid-typed backend column, and it had no coverage at all until this test.
// The accepted set is exactly "canonical UUID or friendly name" — the tool
// schema previously advertised AWS account numbers instead, which is what sent
// agents down the rejected path in the first place.
func TestResolveAccountIdentifiers(t *testing.T) {
	const (
		devAwsID   = "883efbbc-bb2c-404b-9ed9-6b7ecbf6f509"
		devAwsName = "dev-aws"
		prodID     = "a6752212-f3e2-4908-b00b-b0ddfd424352"
		prodName   = "aws-prod"
	)
	accountMap := map[string]string{devAwsID: devAwsName, prodID: prodName}

	t.Run("empty input is returned unchanged without touching the map", func(t *testing.T) {
		out, err := resolveAccountIdentifiers(nil, accountMap)
		assert.NoError(t, err)
		assert.Empty(t, out)
	})

	t.Run("a canonical UUID passes straight through", func(t *testing.T) {
		out, err := resolveAccountIdentifiers([]string{devAwsID}, accountMap)
		assert.NoError(t, err)
		assert.Equal(t, []string{devAwsID}, out)
	})

	t.Run("an exact friendly name resolves to its UUID", func(t *testing.T) {
		out, err := resolveAccountIdentifiers([]string{devAwsName}, accountMap)
		assert.NoError(t, err)
		assert.Equal(t, []string{devAwsID}, out,
			"names must reach the backend as UUIDs — the column is uuid-typed, so a raw name errors with 'pq: invalid input syntax'")
	})

	t.Run("name matching is case-insensitive", func(t *testing.T) {
		out, err := resolveAccountIdentifiers([]string{"DEV-AWS"}, accountMap)
		assert.NoError(t, err)
		assert.Equal(t, []string{devAwsID}, out,
			"users and models type aws-demo/AWS-Demo/Aws-demo interchangeably")
	})

	t.Run("surrounding whitespace is trimmed before lookup", func(t *testing.T) {
		out, err := resolveAccountIdentifiers([]string{"  dev-aws  "}, accountMap)
		assert.NoError(t, err)
		assert.Equal(t, []string{devAwsID}, out)
	})

	t.Run("a mixed list of UUIDs and names resolves both", func(t *testing.T) {
		out, err := resolveAccountIdentifiers([]string{devAwsID, prodName}, accountMap)
		assert.NoError(t, err)
		assert.ElementsMatch(t, []string{devAwsID, prodID}, out)
	})

	t.Run("empty and whitespace-only entries are dropped, not rejected", func(t *testing.T) {
		out, err := resolveAccountIdentifiers([]string{"", "   ", devAwsName}, accountMap)
		assert.NoError(t, err)
		assert.Equal(t, []string{devAwsID}, out)
	})

	t.Run("map entries with a blank name are skipped rather than matching an empty input", func(t *testing.T) {
		out, err := resolveAccountIdentifiers([]string{devAwsName},
			map[string]string{devAwsID: devAwsName, "blank-id": ""})
		assert.NoError(t, err)
		assert.Equal(t, []string{devAwsID}, out)
	})

	t.Run("an unknown name is rejected and nothing partial is returned", func(t *testing.T) {
		out, err := resolveAccountIdentifiers([]string{devAwsName, "no-such-account"}, accountMap)
		assert.Error(t, err)
		assert.Nil(t, out, "a partial resolve would silently widen the query beyond what was asked for")
		assert.Contains(t, err.Error(), "no-such-account")
	})

	// The regression this fix exists for: an agent handed a CloudWatch alarm ARN
	// (arn:aws:cloudwatch:us-east-1:123456789012:alarm:...) scrapes the account
	// id out of it and passes it here. It must fail with a reason, not a shrug.
	t.Run("an AWS account number is rejected with an explanation, not a name list", func(t *testing.T) {
		_, err := resolveAccountIdentifiers([]string{"123456789012"}, accountMap)
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "cloud provider account number",
				"the model needs to know WHY a plausible-looking id failed, or it will retry the same shape")
			assert.Contains(t, err.Error(), "canonical UUID")
		}
	})

	t.Run("a near-miss name suggests the closest real account", func(t *testing.T) {
		_, err := resolveAccountIdentifiers([]string{"aws-prd"}, accountMap)
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "did you mean",
				"a one-character typo should self-correct in-flight rather than costing another tool call")
			assert.Contains(t, err.Error(), prodName)
		}
	})
}

// The error text is fed straight back into the model's context, so its SIZE is
// a correctness property, not cosmetics. On the audited 340-account tenant the
// pre-fix message was 5,541 chars (~1,385 tokens) of mostly junk names. This
// pins the cap so that regression cannot creep back in.
func TestResolveAccountIdentifiers_ErrorStaysSmallOnLargeTenants(t *testing.T) {
	accountMap := map[string]string{}
	for i := 0; i < 340; i++ {
		accountMap[fmt.Sprintf("id-%03d", i)] = fmt.Sprintf("account-%03d", i)
	}

	_, err := resolveAccountIdentifiers([]string{"123456789012"}, accountMap)
	if !assert.Error(t, err) {
		return
	}
	msg := err.Error()

	assert.Less(t, len(msg), 1000, "error was 5541 chars before the cap; keep it an actionable hint, not a context dump")
	assert.Equal(t, maxListedAccountNames, strings.Count(msg, "account-"),
		"exactly maxListedAccountNames names should be echoed, however many the tenant has")
	assert.Contains(t, msg, "of 340", "the model should still learn the true size of the tenant")
}

// Regression guard for the "did you mean" cap. The suggestion budget is shared
// across every unresolved identifier, so it must be spent on the globally
// closest names — not on whichever identifier happens to come first in the
// list. Before this was fixed, the exact match below was dropped entirely.
func TestSimilarAccountNames_BudgetIsSpentOnClosestMatchesGlobally(t *testing.T) {
	allNames := []string{"aws-prod-1", "aws-prod-2", "aws-prod-3", "gcp-dev-1"}

	t.Run("an exact match for a later identifier is not starved by an earlier one's near-misses", func(t *testing.T) {
		got := similarAccountNames([]string{"aws-prod", "gcp-dev-1"}, allNames)
		assert.Contains(t, got, "gcp-dev-1",
			"gcp-dev-1 is distance 0; suggesting three aws-prod-* variants instead tells the model its correct name does not exist")
	})

	t.Run("suggestions stay within the cap", func(t *testing.T) {
		got := similarAccountNames([]string{"aws-prod", "gcp-dev-1"}, allNames)
		assert.LessOrEqual(t, len(got), maxAccountNameSuggestions)
	})

	t.Run("a name far from everything yields no suggestions rather than noise", func(t *testing.T) {
		assert.Empty(t, similarAccountNames([]string{"zzzzzzzzzzzzzz"}, allNames),
			"a suggestion past the distance cutoff is noise, and noise costs the model a wasted retry")
	})

	t.Run("no accounts means no suggestions", func(t *testing.T) {
		assert.Empty(t, similarAccountNames([]string{"anything"}, nil))
	})
}
