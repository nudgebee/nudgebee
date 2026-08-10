package playbooks

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func pod(name, namespace string) map[string]any {
	return map[string]any{
		"api_version": "v1",
		"kind":        "Pod",
		"metadata":    map[string]any{"name": name, "namespace": namespace},
	}
}

func TestFilterPodsByNameNamespace(t *testing.T) {
	t.Run("narrows a multi-pod response down to the exact match", func(t *testing.T) {
		data := []any{
			pod("checkout-6c54595687-cjp9x", "prod"),
			pod("checkout-6c54595687-abcde", "prod"),
			pod("checkout-6c54595687-cjp9x", "staging"), // same name, different namespace
		}
		got := filterPodsByNameNamespace(data, "checkout-6c54595687-cjp9x", "prod")
		list, ok := got.([]any)
		assert.True(t, ok)
		assert.Len(t, list, 1)
		assert.Equal(t, pod("checkout-6c54595687-cjp9x", "prod"), list[0])
	})

	t.Run("passes through a correctly-scoped single-pod response unchanged", func(t *testing.T) {
		data := []any{pod("checkout-6c54595687-cjp9x", "prod")}
		got := filterPodsByNameNamespace(data, "checkout-6c54595687-cjp9x", "prod")
		list, ok := got.([]any)
		assert.True(t, ok)
		assert.Len(t, list, 1)
	})

	// Regression: this used to fall back to the raw, completely unfiltered
	// response — observed on real dev-cluster traffic returning 525 unrelated
	// pods (5.8MB) for a single already-deleted trivy-scan job pod. The target
	// pod being genuinely gone (ephemeral job pod, or an OOMKilled pod
	// Kubernetes already rescheduled under a new name) is common enough that
	// dumping the whole account's pod list is worse than saying so directly.
	t.Run("returns a small not-found marker when nothing matches, instead of the raw unfiltered response", func(t *testing.T) {
		data := []any{pod("other-pod", "other-namespace"), pod("yet-another-pod", "yet-another-namespace")}
		got := filterPodsByNameNamespace(data, "checkout-6c54595687-cjp9x", "prod")
		list, ok := got.([]any)
		assert.True(t, ok)
		assert.Len(t, list, 1, "must not fall back to the full unfiltered list")
		marker, ok := list[0].(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, true, marker["pod_not_found"])
		assert.Equal(t, "checkout-6c54595687-cjp9x", marker["requested_name"])
		assert.Equal(t, "prod", marker["requested_namespace"])
	})

	t.Run("returns the not-found marker for an already-empty list too", func(t *testing.T) {
		data := []any{}
		got := filterPodsByNameNamespace(data, "checkout-6c54595687-cjp9x", "prod")
		list, ok := got.([]any)
		assert.True(t, ok)
		assert.Len(t, list, 1)
		marker, ok := list[0].(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, true, marker["pod_not_found"])
		assert.Equal(t, "checkout-6c54595687-cjp9x", marker["requested_name"])
	})

	t.Run("passes through non-list shapes unchanged", func(t *testing.T) {
		single := pod("checkout-6c54595687-cjp9x", "prod")
		got := filterPodsByNameNamespace(single, "checkout-6c54595687-cjp9x", "prod")
		assert.Equal(t, single, got)
	})

	t.Run("skips malformed entries without panicking", func(t *testing.T) {
		data := []any{"not-a-pod-object", map[string]any{"no_metadata": true}, pod("checkout-6c54595687-cjp9x", "prod")}
		got := filterPodsByNameNamespace(data, "checkout-6c54595687-cjp9x", "prod")
		list, ok := got.([]any)
		assert.True(t, ok)
		assert.Len(t, list, 1)
	})
}
