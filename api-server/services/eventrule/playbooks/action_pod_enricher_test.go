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

	t.Run("falls back to the raw response when nothing matches, instead of emptying it", func(t *testing.T) {
		data := []any{pod("other-pod", "other-namespace")}
		got := filterPodsByNameNamespace(data, "checkout-6c54595687-cjp9x", "prod")
		assert.Equal(t, data, got)
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
