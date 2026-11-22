package core

import "testing"

func TestSanitizeAnnotations(t *testing.T) {
	const lastApplied = "kubectl.kubernetes.io/last-applied-configuration"

	t.Run("drops object copies on any kind, keeps the rest", func(t *testing.T) {
		got := SanitizeAnnotations(NodeTypeWorkload, map[string]string{
			lastApplied:                         `{"data":{"k":"v"}}`,
			"objectset.rio.cattle.io/applied":   "H4sI",
			"deployment.kubernetes.io/revision": "3",
		})
		if len(got) != 1 || got["deployment.kubernetes.io/revision"] != "3" {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("secret values redacted outside the allowlist", func(t *testing.T) {
		got := SanitizeAnnotations(NodeTypeK8sSecret, map[string]any{
			lastApplied:                               `{"data":{"k":"v"}}`,
			"meta.helm.sh/release-name":               "nb",
			"reconcile.external-secrets.io/data-hash": "abc",
		})
		if len(got) != 2 || got["meta.helm.sh/release-name"] != "nb" ||
			got["reconcile.external-secrets.io/data-hash"] != RedactedAnnotationValue {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("nothing left or not a map returns nil", func(t *testing.T) {
		if got := SanitizeAnnotations(NodeTypeK8sSecret, map[string]any{lastApplied: "x"}); got != nil {
			t.Fatalf("got %v", got)
		}
		if got := SanitizeAnnotations(NodeTypeWorkload, "nope"); got != nil {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("NewNode removes the property when nothing is left", func(t *testing.T) {
		n := NewNode(NodeTypeK8sSecret, "k", map[string]any{"name": "s", "annotations": map[string]any{lastApplied: "x"}}, "t", "a", "k8s")
		if _, ok := n.Properties["annotations"]; ok {
			t.Fatalf("annotations kept: %v", n.Properties["annotations"])
		}
	})
}
