//go:build e2e

package agents

import (
	"os"
	"testing"
)

// ============================================================
// Benchmark-fixture-backed integration tests
// ============================================================
//
// These tests demonstrate the Tier-C strategy: instead of depending on
// whatever state happens to exist in the dev cluster, they drive the
// benchmark YAML fixtures (the same source-of-truth the Python nightly
// harness consumes). The failure state is injected deterministically via
// the fixture's own `before_test` shell hook (kubectl apply of a small
// manifest into a fixture-owned namespace), so every run observes the same
// underlying condition.
//
// Curation policy: we deliberately wire only a small set of **hard**
// fixtures here. Single-tool / knowledge-style fixtures (CLI-command Q&A,
// PromQL one-liners) are skipped — a competent LLM passes them trivially
// and they don't differentiate good vs bad agent behaviour. Each test
// below covers a distinct failure surface (network-ingress,
// network-policy, storage, log chain, prometheus metric), is tagged `hard`
// or `chain-of-causation` upstream, requires real setup, and asserts on
// keywords drawn verbatim from the fixture's own `expected_output` (no
// invented strings).
//
// Skipped unless:
//   1. TEST_ACCOUNT / TEST_USER / TEST_TENANT are set (integration env)
//   2. kubectl is on PATH and can reach a cluster
//
// The Python benchmark runner keeps scoring these same fixtures nightly
// via RAGAS; the Go runner here asserts only the fast, deterministic
// property invariants we care about on every PR.
//
// Refactor note (2026-05): the original fixture tree was at
// `llm/benchmark/llm/agents/rca/fixtures/` and used an OpenTelemetry-demo
// `feature_flag` mechanism to inject faults. The Python benchmark was
// restructured into per-signal trees (`metrics/`, `traces/`, `logs/`,
// `nubi/`, ...) and every K8s RCA fixture now self-bootstraps its own
// namespace + manifests via `before_test`. These Go tests follow that move.

// TestK8sAgent_Fixture_MisconfiguredIngressClass — chain-of-causation,
// network. An ingress references a class that doesn't exist, so external
// traffic never reaches the cluster. The agent must walk from "can't reach
// service" → ingress → ingressClass lookup → conclude the class is missing.
//
// This test demonstrates all four assertion tiers:
//
//  1. Tool-call structure: WantMinToolCalls catches "agent gave up after
//     one call." WantAnyToolMatching catches "didn't reach for kubectl at all."
//  2. Tool-output: implicit — if the workspace pod returned errors, the
//     agent's response would mention the failure and we'd add
//     WantForbiddenTools to catch any unexpected shell fallback.
//  3. Response keywords: WantContainsAny is the cheap-but-fragile check.
//     False-positive risk: the question itself mentions "ingress", so the
//     agent's response trivially contains the word even on failures.
//  4. Semantic (LLM-judge): WantLLMClaims is the strict check. It catches
//     "plausible-sounding wrong answers that happen to contain the
//     keywords" — exactly the failure mode we hit with the broken-relay
//     run. Costs ~$0.001 per test; opt-in by setting WantLLMClaims.
//
// f.YAML.ExpectedOutput is the parsed list from the fixture's
// expected_output field. Passing it into WantLLMClaims opts this test
// into Tier-4 validation; tests that don't want the LLM cost simply
// leave WantLLMClaims unset.
func TestK8sAgent_Fixture_MisconfiguredIngressClass(t *testing.T) {
	skipIfNoFixtureEnv(t)
	skipIfNoKubectl(t)

	f := LoadFixture(t, fixturePath("nubi", "25_misconfigured_ingress_class"))
	f.Setup(t)

	tc := f.TestCase
	tc.WantAnyToolMatching = []string{"kubectl", "describe", "ingress", "get"}
	tc.WantMinToolCalls = 3
	tc.WantContainsAny = []string{"ingress", "class", "example-ingress-class"}
	// Tier-4 opt-in: the LLM judges every claim from the fixture YAML.
	tc.WantLLMClaims = f.YAML.ExpectedOutput

	agent := f.Agent(t, newK8sDebugAgent(os.Getenv("TEST_ACCOUNT")))
	runTestMinimal(t, agent, tc)
}

// TestK8sAgent_Fixture_NetworkPolicyBlockingTraffic — hard, holmesgpt-parity,
// network. A NetworkPolicy on `backend` only permits ingress from pods
// labelled `tier=backend`; `frontend` pods carry `tier=frontend`, so they
// time out. The agent must reach for NetworkPolicy resources after seeing
// that pods/services look healthy, then compare selector labels.
//
// PRECONDITION: cluster must run a NetworkPolicy-enforcing CNI
// (Calico, Cilium, ...). On kindnet/flannel-without-policy, traffic is
// never actually blocked and the agent will honestly answer "no problem"
// — which will fail the keyword check. The fixture's before_test warns
// loudly when this is the case.
func TestK8sAgent_Fixture_NetworkPolicyBlockingTraffic(t *testing.T) {
	skipIfNoFixtureEnv(t)
	skipIfNoKubectl(t)

	f := LoadFixture(t, fixturePath("nubi", "84_network_policy_blocking_traffic"))
	f.Setup(t)

	tc := f.TestCase
	tc.WantAnyToolMatching = []string{"kubectl", "describe", "networkpolicy", "get"}
	tc.WantMinToolCalls = 3
	tc.WantContainsAny = []string{"networkpolicy", "network policy", "tier", "backend"}
	// Tier-4 opt-in: the fixture's expected_output is short and high-signal
	// (NetworkPolicy + tier label mismatch), so the LLM judge will reliably
	// distinguish a correct diagnosis from a generic "I see a problem" answer.
	tc.WantLLMClaims = f.YAML.ExpectedOutput

	agent := f.Agent(t, newK8sDebugAgent(os.Getenv("TEST_ACCOUNT")))
	runTestMinimal(t, agent, tc)
}

// TestK8sAgent_Fixture_PVCStorageClassMismatch — hard, kubernetes, storage.
// A database StatefulSet requests `storageClassName: fast-ssd` but no such
// StorageClass exists; PVCs stay Pending forever, pods never start. The
// agent must drill from "database not running" → pod state → PVC state →
// storage class lookup.
//
// Note on tool-count: a competent agent solves this in ONE outer dispatch
// to the kubectl sub-agent (events output already names the missing
// StorageClass — "ProvisioningFailed: ... storageclass.storage.k8s.io
// \"fast-ssd\" not found"). We keep WantMinToolCalls=1 here as a "didn't
// give up" floor; correctness is gated by WantLLMClaims (Tier-4 LLM-judge).
func TestK8sAgent_Fixture_PVCStorageClassMismatch(t *testing.T) {
	skipIfNoFixtureEnv(t)
	skipIfNoKubectl(t)

	f := LoadFixture(t, fixturePath("nubi", "80_pvc_storage_class_mismatch"))
	f.Setup(t)

	tc := f.TestCase
	tc.WantAnyToolMatching = []string{"kubectl", "describe", "get", "pvc", "storageclass"}
	tc.WantMinToolCalls = 1
	tc.WantContainsAny = []string{"pvc", "storage class", "storageclass", "fast-ssd"}
	// Tier-4 opt-in: the structural floor is intentionally loose for this
	// fixture (the events output is one-shot conclusive), so semantic check
	// carries correctness.
	tc.WantLLMClaims = f.YAML.ExpectedOutput

	agent := f.Agent(t, newK8sDebugAgent(os.Getenv("TEST_ACCOUNT")))
	runTestMinimal(t, agent, tc)
}

// TestK8sAgent_Fixture_CascadingFailures — chain-of-causation, logs,
// context_window. Payment-processor failures originate from auth-service
// losing its Redis connection, then cascade. The agent must follow the
// causality chain across multiple services rather than stop at the
// proximate symptom. Tests log-summarisation under heavy log volume.
func TestK8sAgent_Fixture_CascadingFailures(t *testing.T) {
	skipIfNoFixtureEnv(t)
	skipIfNoKubectl(t)

	f := LoadFixture(t, fixturePath("nubi", "68_cascading_failures"))
	f.Setup(t)

	tc := f.TestCase
	tc.WantAnyToolMatching = []string{"logs", "loki", "kubectl", "describe"}
	tc.WantMinToolCalls = 3
	tc.WantContainsAny = []string{"redis", "auth", "cascading", "connection"}

	agent := f.Agent(t, newK8sDebugAgent(os.Getenv("TEST_ACCOUNT")))
	runTestMinimal(t, agent, tc)
}

// TestK8sAgent_Fixture_ElectricityMarketBiddingBug — hard,
// chain-of-causation, prometheus. The NordPool exchange shows ~100% bid
// acceptance vs ~10% on other exchanges — an application-level anomaly
// visible only through PromQL on custom OTel metrics. Logs and k8s state
// look fine, so the agent must reach for the metric path. Forces use of a
// tool family other than kubectl/logs.
//
// REQUIRES: a configured Prometheus integration for the test account. The
// metrics tool routes every query through services-server's
// GetObservabilityProvider, then falls back to the local DB's
// integration_config_values; in a bare local-dev test setup neither is
// populated, so every call fails with `metrics_list_names: 401
// Unauthorized` before the agent ever sees a data point. The test is
// observably correct in that case (it caught the missing integration), but
// it is testing the wrong thing — RCA over real metrics, not auth plumbing.
//
// Unskip when:
//   - the test fixture registers a Prometheus integration row for
//     TEST_ACCOUNT/TEST_TENANT before LoadFixture, OR
//   - the metrics tool grows an "in-cluster prometheus auto-discovery"
//     fallback that works without a services-server-side integration row.
//
// See TESTING.md ("Tests with observability-provider dependencies").
func TestK8sAgent_Fixture_ElectricityMarketBiddingBug(t *testing.T) {
	skipIfNoFixtureEnv(t)
	skipIfNoKubectl(t)

	f := LoadFixture(t, fixturePath("nubi", "160_electricity_market_bidding_bug"))
	f.Setup(t)

	tc := f.TestCase
	tc.WantAnyToolMatching = []string{"prometheus", "metric", "promql"}
	tc.WantMinToolCalls = 2
	tc.WantContainsAny = []string{"nordpool", "acceptance"}

	agent := f.Agent(t, newK8sDebugAgent(os.Getenv("TEST_ACCOUNT")))
	runTestMinimal(t, agent, tc)
}
