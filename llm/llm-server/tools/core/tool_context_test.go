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

// Blanket (provider+model) and per-tier picks are mutually exclusive at the
// conversation row — switching modes nulls the other side. MergeFrom has to
// respect that, or a mode switch inherits the mode it just replaced.

func TestMergeFrom_TierPicksDoNotInheritStaleBlanket(t *testing.T) {
	// Turn 2 chooses per-tier models. Turn 1's blanket pick must NOT come back:
	// it would win for calls the tier picks were meant to govern.
	incoming := NBQueryConfig{
		LlmTierModels: map[string]TierModelPick{"reasoning": {Provider: "googleai", Model: "gemini-3.1-pro"}},
	}
	incoming.MergeFrom(NBQueryConfig{
		LlmProvider:     "googleai",
		LlmModelName:    "gemini-3-flash",
		LlmConfigSource: "db:int-1",
	})

	if incoming.LlmProvider != "" || incoming.LlmModelName != "" {
		t.Fatalf("stale blanket pick resurrected: provider=%q model=%q", incoming.LlmProvider, incoming.LlmModelName)
	}
	if len(incoming.LlmTierModels) != 1 {
		t.Fatalf("this turn's tier picks should stand, got %d", len(incoming.LlmTierModels))
	}
	if incoming.LlmConfigSource != "db:int-1" {
		t.Fatalf("the pin is orthogonal and must still inherit, got %q", incoming.LlmConfigSource)
	}
}

func TestMergeFrom_BlanketPickDoesNotInheritStaleTiers(t *testing.T) {
	// The mirror case: turn 2 chooses a blanket model, so turn 1's tier picks
	// must not linger.
	incoming := NBQueryConfig{LlmProvider: "googleai", LlmModelName: "gemini-2.5-flash"}
	incoming.MergeFrom(NBQueryConfig{
		LlmTierModels:   map[string]TierModelPick{"summary": {Provider: "googleai", Model: "gemini-3-flash"}},
		LlmConfigSource: "env:global",
	})

	if len(incoming.LlmTierModels) != 0 {
		t.Fatalf("stale tier picks resurrected: %v", incoming.LlmTierModels)
	}
	if incoming.LlmModelName != "gemini-2.5-flash" {
		t.Fatalf("this turn's blanket pick should stand, got %q", incoming.LlmModelName)
	}
	if incoming.LlmConfigSource != "env:global" {
		t.Fatalf("the pin is orthogonal and must still inherit, got %q", incoming.LlmConfigSource)
	}
}

func TestMergeFrom_TurnThatPicksNothingStillInherits(t *testing.T) {
	// The stickiness the feature relies on is unaffected: a turn choosing
	// neither mode inherits whichever was in force.
	blanket := NBQueryConfig{}
	blanket.MergeFrom(NBQueryConfig{LlmProvider: "googleai", LlmModelName: "gemini-3-flash", LlmConfigSource: "db:int-1"})
	if blanket.LlmModelName != "gemini-3-flash" || blanket.LlmConfigSource != "db:int-1" {
		t.Fatalf("blanket stickiness broken: %+v", blanket)
	}

	tiered := NBQueryConfig{}
	tiered.MergeFrom(NBQueryConfig{LlmTierModels: map[string]TierModelPick{"summary": {Provider: "googleai", Model: "x"}}})
	if len(tiered.LlmTierModels) != 1 {
		t.Fatalf("tier stickiness broken: %+v", tiered)
	}
}

// "Clear all" needs a signal distinct from "this turn picked nothing" — the
// latter is exactly the state that inherits the stored config.

func TestMergeFrom_ResetInheritsNothing(t *testing.T) {
	incoming := NBQueryConfig{LlmConfigReset: true}
	incoming.MergeFrom(NBQueryConfig{
		LlmProvider:     "googleai",
		LlmModelName:    "gemini-3-flash",
		LlmConfigSource: "db:int-1",
		LlmTierModels:   map[string]TierModelPick{"summary": {Provider: "googleai", Model: "x"}},
	})

	if incoming.LlmProvider != "" || incoming.LlmModelName != "" {
		t.Fatalf("blanket pick resurrected across a reset: %q / %q", incoming.LlmProvider, incoming.LlmModelName)
	}
	if incoming.LlmConfigSource != "" {
		t.Fatalf("pin resurrected across a reset: %q", incoming.LlmConfigSource)
	}
	if len(incoming.LlmTierModels) != 0 {
		t.Fatalf("tier picks resurrected across a reset: %v", incoming.LlmTierModels)
	}
}

func TestMergeFrom_ResetIsNotSticky(t *testing.T) {
	// The turn after a reset is an ordinary turn. If the flag inherited, every
	// later turn would keep wiping a config the user may have just re-picked.
	next := NBQueryConfig{}
	next.MergeFrom(NBQueryConfig{LlmConfigReset: true, LlmConfigSource: "db:int-1"})

	if next.LlmConfigReset {
		t.Fatal("reset flag inherited by the following turn")
	}
	if next.LlmConfigSource != "db:int-1" {
		t.Fatalf("ordinary inheritance broken after a reset turn: %q", next.LlmConfigSource)
	}
}

func TestIsEmpty_ResetOnlyConfigIsNotEmpty(t *testing.T) {
	// conversation.go replaces an "empty" request config wholesale with the
	// previous message's stored one — a reset that looked empty would come back
	// as the exact config it was meant to clear.
	if (NBQueryConfig{LlmConfigReset: true}).IsEmpty() {
		t.Fatal("a reset-only config must not read as empty")
	}
}
