package core

import "testing"

// TestNBQueryConfig_CurrentClusterHelpers guards that the current-cluster context fields (#30162)
// participate in IsEmpty and MergeFrom — otherwise the context is silently lost when configs are
// merged across agent boundaries, or a config carrying only it is treated as empty.
func TestNBQueryConfig_CurrentClusterHelpers(t *testing.T) {
	// IsEmpty: a config holding only the current-cluster context is NOT empty.
	if (NBQueryConfig{CurrentClusterId: "id-1"}).IsEmpty() {
		t.Fatal("IsEmpty: config with CurrentClusterId should not be empty")
	}
	if (NBQueryConfig{CurrentCluster: "prod-eks"}).IsEmpty() {
		t.Fatal("IsEmpty: config with CurrentCluster should not be empty")
	}
	if !(NBQueryConfig{}).IsEmpty() {
		t.Fatal("IsEmpty: zero config should be empty")
	}

	// MergeFrom: missing fields are filled from src...
	dst := NBQueryConfig{}
	dst.MergeFrom(NBQueryConfig{CurrentCluster: "prod-eks", CurrentClusterId: "id-1"})
	if dst.CurrentCluster != "prod-eks" || dst.CurrentClusterId != "id-1" {
		t.Fatalf("MergeFrom: expected fields copied, got %q / %q", dst.CurrentCluster, dst.CurrentClusterId)
	}

	// ...but existing values in dst are preserved.
	dst2 := NBQueryConfig{CurrentCluster: "keep", CurrentClusterId: "keep-id"}
	dst2.MergeFrom(NBQueryConfig{CurrentCluster: "other", CurrentClusterId: "other-id"})
	if dst2.CurrentCluster != "keep" || dst2.CurrentClusterId != "keep-id" {
		t.Fatalf("MergeFrom: expected existing values preserved, got %q / %q", dst2.CurrentCluster, dst2.CurrentClusterId)
	}
}

// TestNBQueryConfig_LogProviderOverride guards the same contract for the per-request
// log-provider override: MergeFrom is what carries config into a sub-agent executor
// resumed from saved state (executor_planner.go) and into follow-up turns, so a field
// missing from it is silently dropped on those paths.
func TestNBQueryConfig_LogProviderOverride(t *testing.T) {
	if (NBQueryConfig{LogProviderOverride: "k8s"}).IsEmpty() {
		t.Fatal("IsEmpty: config with LogProviderOverride should not be empty")
	}

	dst := NBQueryConfig{}
	dst.MergeFrom(NBQueryConfig{LogProviderOverride: "k8s"})
	if dst.LogProviderOverride != "k8s" {
		t.Fatalf("MergeFrom: expected override copied, got %q", dst.LogProviderOverride)
	}

	// An override set on this request wins over the one carried in from src.
	dst2 := NBQueryConfig{LogProviderOverride: "loki"}
	dst2.MergeFrom(NBQueryConfig{LogProviderOverride: "k8s"})
	if dst2.LogProviderOverride != "loki" {
		t.Fatalf("MergeFrom: expected existing override preserved, got %q", dst2.LogProviderOverride)
	}
}
