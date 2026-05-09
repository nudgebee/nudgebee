package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ============================================================================
// swBuildMultiValueFilter
// ============================================================================

func TestSWBuildMultiValueFilter(t *testing.T) {
	cases := []struct {
		name     string
		attr     string
		input    string
		expected string
	}{
		{
			name:     "single exact value",
			attr:     "k8s.node.name",
			input:    "gke-cluster-node1",
			expected: "k8s.node.name: [gke-cluster-node1]",
		},
		{
			// Prometheus-style ".*" suffix is stripped to recover the exact SWO node name.
			// SWO measurements filter does not support wildcards inside [...].
			name:     "single value with prometheus trailing wildcard",
			attr:     "k8s.node.name",
			input:    "gke-cluster-node1.*",
			expected: "k8s.node.name: [gke-cluster-node1]",
		},
		{
			name:     "multiple pipe-separated values each with trailing wildcard",
			attr:     "k8s.node.name",
			input:    "gke-cluster-node1.*|gke-cluster-node2.*|gke-cluster-node3.*",
			expected: "k8s.node.name: [gke-cluster-node1,gke-cluster-node2,gke-cluster-node3]",
		},
		{
			// The exact node names from the API request, as sent by the UI.
			name:     "real GKE node names pipe-separated with prometheus wildcards",
			attr:     "k8s.node.name",
			input:    "gke-nudgebee-dev-runner-node-pool-v2-8f4d877c-lt65.*|gke-nudgebee-dev-runner-node-pool-v2-8f4d877c-5nvc.*",
			expected: "k8s.node.name: [gke-nudgebee-dev-runner-node-pool-v2-8f4d877c-lt65,gke-nudgebee-dev-runner-node-pool-v2-8f4d877c-5nvc]",
		},
		{
			// Mid-string "*" (not a trailing ".*") falls back to space-joined "attr:val*" format
			// because SWO only supports glob wildcards outside brackets.
			name:     "glob wildcard mid-string uses attr:val format",
			attr:     "k8s.node.name",
			input:    "gke-cluster-*-node",
			expected: "k8s.node.name:gke-cluster-*-node",
		},
		{
			name:     "empty string returns empty",
			attr:     "k8s.node.name",
			input:    "",
			expected: "",
		},
		{
			name:     "blank tokens dropped",
			attr:     "k8s.node.name",
			input:    "node1||node2|",
			expected: "k8s.node.name: [node1,node2]",
		},
		{
			name:     "whitespace trimmed around tokens",
			attr:     "k8s.node.name",
			input:    " node1 | node2 ",
			expected: "k8s.node.name: [node1,node2]",
		},
		{
			name:     "no wildcard in exact name",
			attr:     "k8s.node.name",
			input:    "gke-cluster-node1|gke-cluster-node2",
			expected: "k8s.node.name: [gke-cluster-node1,gke-cluster-node2]",
		},
		{
			name:     "mixed wildcards fall back to attr:val format",
			attr:     "k8s.node.name",
			input:    "gke-cluster-node1*|gke-cluster-node2*",
			expected: "k8s.node.name:gke-cluster-node1* k8s.node.name:gke-cluster-node2*",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, swBuildMultiValueFilter(tc.attr, tc.input))
		})
	}
}

// ============================================================================
// buildSolarWindsFilter
// ============================================================================

func TestBuildSolarWindsFilter(t *testing.T) {
	t.Run("node kind with pipe-separated node names strips prometheus wildcards", func(t *testing.T) {
		meta := RequestMetadata{
			Kind:     "node",
			NodeName: "gke-cluster-lt65.*|gke-cluster-5nvc.*",
		}
		got := buildSolarWindsFilter(meta)
		assert.Equal(t, "k8s.node.name: [gke-cluster-lt65,gke-cluster-5nvc]", got)
	})

	t.Run("node kind with single node name", func(t *testing.T) {
		meta := RequestMetadata{
			Kind:     "node",
			NodeName: "gke-cluster-lt65",
		}
		got := buildSolarWindsFilter(meta)
		assert.Equal(t, "k8s.node.name: [gke-cluster-lt65]", got)
	})

	t.Run("workload kind with namespace and deployment", func(t *testing.T) {
		meta := RequestMetadata{
			Kind:      "deployment",
			Namespace: "nudgebee",
			Name:      "api-server",
		}
		got := buildSolarWindsFilter(meta)
		assert.Equal(t, "k8s.namespace.name: [nudgebee] k8s.deployment.name: [api-server]", got)
	})

	t.Run("cluster level returns empty filter", func(t *testing.T) {
		meta := RequestMetadata{}
		got := buildSolarWindsFilter(meta)
		assert.Equal(t, "", got)
	})

	t.Run("node name with prometheus trailing wildcard stripped to exact value", func(t *testing.T) {
		meta := RequestMetadata{
			Kind:     "node",
			NodeName: "gke-nudgebee-dev-c4-amd-spot-v2-02132c6e-spkf.*",
		}
		got := buildSolarWindsFilter(meta)
		assert.Equal(t, "k8s.node.name: [gke-nudgebee-dev-c4-amd-spot-v2-02132c6e-spkf]", got)
	})
}
