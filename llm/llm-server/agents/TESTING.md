# Agent Testing Guide

Most `agent_*_test.go` files in this directory are **smoke tests against a populated test cluster**, not regression-grade self-contained tests. This file explains why, the limits of the current model, and the migration target for tests that need to be self-validating.

## Build tags

This package is split into two build modes:

- **Default build** — `go test ./...` (or `make test`) compiles and runs only the un-tagged files. These are pure unit tests with no external dependencies — no LLM, no DB, no port-forwards required. They run on every PR.
- **Integration build** — `go test -tags=e2e ./...` (or `make test-e2e`) compiles both un-tagged files **and** files whose first line is `//go:build e2e`. Integration tests require `TEST_ACCOUNT`, `TEST_USER`, `TEST_TENANT` plus per-agent env vars and port-forwards to dev services. See the section on env vars below.

The fixture-driven helper files (`flagd_controller_test.go`, `benchmark_fixture_test.go`, `agent_k8s_debug_fixtures_e2e_test.go`, plus the `testdb_helpers.go`-style shared helpers used by the e2e tests) all live behind the `e2e` tag and are only compiled in integration mode.

When adding a new test, decide which side it belongs to:
- Unit (no tag, lives in `<basename>_test.go`) — pure logic, mocks, table-driven, no `HandleConversationSessionRequest`, no DB/HTTP traffic.
- Integration (`//go:build e2e` first line, lives in `<basename>_e2e_test.go`) — anything that drives an agent end-to-end, talks to the LLM, or hits a backing service.

## Helper file naming

Test helpers live in three styles depending on import scope:

| File pattern | Visible to | Build tag | Example |
|---|---|---|---|
| `*_e2e_test.go` | This package only (test build) | `//go:build e2e` | `agent_aws_debug_2_e2e_test.go` — actual e2e Test funcs |
| `*_test.go` (no `_e2e_` suffix) | This package only (test build) | `//go:build e2e` for e2e helpers, untagged for unit helpers | `flagd_controller_test.go`, `benchmark_fixture_test.go` — package-internal e2e helpers (no Test funcs) |
| `*.go` (regular Go file) | Any package | `//go:build e2e` | `testdb_helpers.go` — exported `FetchRecentEventID` used cross-package |

The third pattern exists because Go forbids importing `_test.go` files across packages. If a test helper is called from another package's tests (e.g., `api/event_analyzer_e2e_test.go` uses `agents.FetchRecentEventID`), it must live in a regular `.go` file with the `e2e` build tag — same compile-time gating, different import scope.

## Fetching test data from the DB instead of hardcoding

The earlier convention was to hardcode specific entity UUIDs in test fixtures (real event IDs, account IDs from the dev DB). That made the tests **non-portable** (they only worked against one specific DB state) and **leaked internal IDs** through the source tree.

The current pattern is to fetch entities on-the-fly:

```go
// Replaces hardcoded `eventID := "<real-uuid>"` literals.
eventID := agents.FetchRecentEventID(t, os.Getenv("TEST_ACCOUNT"))
```

`FetchRecentEventID` queries the `events` table for the most recent row for the given account and skips the test cleanly if none exist. Used by tests in `agent_events_e2e_test.go`, `agent_events_report_e2e_test.go`, `agent_events_evidence_analysis_e2e_test.go`, `api/event_analyzer_e2e_test.go`, and `agent_k8s_debug_2_e2e_test.go` (for RCA flows). The pattern can be extended to other entity types (`FetchRecentKGNodeID`, `FetchAnyCustomAgentID`, etc.) when needed.

**When to use which:**
- Test only needs *any* entity of the right shape → `FetchRecentEventID` (or similar fetch helper)
- Test reproduces a specific historical bug tied to a specific entity → env-gate via `TEST_EVENT_ID_X` with `t.Skip()` if unset
- Test creates the entity itself → benchmark-fixture pattern (`before_test` shell hook)

## The honest reality

Today, most agent integration tests follow this shape:

```go
Query:     "investigate X"
// ...
resp, _ := core.HandleConversationSessionRequest(...)
assert.Greater(t, len(resp.Response), 0)
```

That's **demonstration, not validation**. The test:

- Sends a query to the agent
- Watches the agent investigate **whatever the cluster currently has**
- Passes as long as the response is non-empty

It cannot tell you:
- Did the agent find the right root cause?
- Would it still pass if the failure mode were absent?
- Did cleanup leak state into the next test?

The implication: a green test suite means "the agents responded to queries against this cluster", not "the agents are correct."

## When to trust a test in this directory

| Test shape | Trust level |
|---|---|
| Pure unit tests (e.g., `TestCountEventsInResponse`, `TestSummarizeTraceSpan`, `TestExtractWorkflowIdFromQuery`) | High — input/output is fully specified. |
| Fixture-backed tests (`benchmark_fixture_test.go` + `flagd_controller_test.go` pattern) | High — fault is reproduced, diagnosis is checked. |
| Smoke tests (`assert.NotEmpty(resp.Response)`) against a live cluster | Low — only catches catastrophic agent failures. |

## Tier model — when (if ever) to migrate a test

| Tier | Files | Fault-reproduction difficulty | Recommended path |
|---|---|---|---|
| 1 — Cloud debug | `agent_aws_debug_2`, `agent_gcp_debug_2`, `agent_azure_debug_2`, `agent_cloud_gcp`, `agent_cloud_azure` | Hardest — cloud state is expensive to mutate | Mock cloud client OR accept as smoke-only |
| 2 — Observability | `agent_promql`, `agent_datadog_*`, `agent_clickhouse`, `agent_log_es`, `agent_elastic_search_*`, `agent_log_analysis_*`, `agent_loki_*` | Moderate — need synthetic data in datasource | Mock datasource OR seed test data |
| 3 — K8s / local | `agent_k8s_debug_2`, `agent_kubectl`, `agent_helm`, `agent_events_*` | Best fit for the fixture pattern (flagd + kubectl) | Migrate to `benchmark_fixture_test.go` + before_test / after_test scripts |
| 4 — Logic-only | `agent_code2`, `agent_conversational`, `agent_followup`, `agent_delegate*`, `agent_llm_provider`, `agent_github` | n/a — inputs are files/strings | Already self-contained |
| 5 — Integration plumbing | `agent_argocd`, `agent_aws_mcp_tools` | Same trade-off as Tier 1 | Mock the external system OR smoke-only |

## The migration target: benchmark-fixture pattern

`benchmark_fixture_test.go` + `flagd_controller_test.go` together implement the model below. New self-contained tests should adopt this pattern:

1. **`before_test`** — shell script reproduces the fault (kill a pod, set a flagd flag, inject latency, drop a connection, OOM a container).
2. **Wait** — fixture YAML declares `wait_time_seconds` for telemetry to settle.
3. **Run the agent** — same call as today.
4. **Validate** — `expected_root_cause.affected_service` (keyword check) plus optional Python RAGAS semantic scoring downstream.
5. **`after_test`** — shell script removes the fault. Registered via `t.Cleanup` so partial setup still gets cleaned.

The fixture YAML schema lives in `llm/benchmark/llm/agents/*/fixtures/*/test_case.yaml` — most K8s RCA scenarios are under `nubi/fixtures/`, with sibling trees for `metrics/`, `traces/`, `logs/`, `events/`, and per-cloud (`cloud/{aws,azure,gcp}/`). Each fixture self-bootstraps its own namespace + manifests via `before_test`, so the Go runner needs only a reachable kubectl — no shared demo cluster. Three K8s scenarios already drive the harness from Go (`agent_k8s_debug_fixtures_e2e_test.go`); Phase 2 expands coverage.

## Notable patterns in the current code

### "Stuck-detector" tests

`TestK8sAgent_HealthCheckMultiApproval` is a deliberate smoke test: the query is intentionally vague, and if the agent enters any clarifying approval flow we type a known-good fallback config-name (`TEST_FALLBACK_CONFIG_NAME`, default `"dev"`) so the conversation can make progress. It is NOT validating an "environment" concept — NB has accounts, not envs — only the multi-approval plumbing itself.

### Env-var-gated fixtures (smoke tests against your test account)

Most `*_test.go` files in this directory read `TEST_ACCOUNT`, `TEST_USER`, `TEST_TENANT`, plus agent-specific accounts (`TEST_AWS_ACCOUNT`, `TEST_GCP_ACCOUNT`, `TEST_AZURE_ACCOUNT`, `TEST_K8SCHAIN_ACCOUNT`, etc.) from `.env`. Tests that reference live entities not yet generally available (specific event UUIDs, second AWS account, etc.) `t.Skip()` when the env var is unset rather than failing.

New env vars introduced during the test-refactor PR:

**Tool-target overrides** (tests previously had inline references):
- `TEST_K8S_NAMESPACE` (default `nudgebee`) — primary K8s namespace
- `TEST_GCP_PROJECT` (default `my-gcp-project-dev`) — GCP project to investigate
- `TEST_AWS_ALB_URL` — real ALB/ELB URL for 500-error investigation tests
- `TEST_AZURE_VM` (default `my-vm-dev`) — Azure VM name for event-investigation tests

**Tool-config selection** (which configured integration to use):
- `TEST_RABBIT_CONFIG_NAME`, `TEST_PG_CONFIG_NAME`, `TEST_GCP_CONFIG_NAME`
- `TEST_K8S_CROSS_ACCOUNT_NAME` (default `k8s-dev`) — for cross-account routing tests
- `TEST_FALLBACK_CONFIG_NAME` — stuck-detector default answer (see above)

**Real-entity references** (test skips with `t.Skip()` if unset):
- `TEST_EVENT_ID_1`, `TEST_EVENT_ID_2` — specific event UUIDs (most tests now use `FetchRecentEventID` instead, see above)
- `TEST_CUSTOM_AGENT_ID` — custom-agent row for `api/agents_e2e_test.go`
- `TEST_AWS_ACCOUNT_2` — secondary AWS account (`TestEventAnalyzer_AWSAccount2`)
- `TEST_SIGNOZ_TENANT`, `TEST_SIGNOZ_ACCOUNT`, `TEST_SIGNOZ_USER` — Signoz-tenant integration tests
- `TEST_FAILURE_TENANT`, `TEST_FAILURE_ACCOUNT`, `TEST_FAILURE_EVENT_ID` — alternate code path in `TestEventAgentFailures`
- `TEST_KG_NODE_ID` — knowledge-graph node for service-dependency tests

**External system references** (GitHub, MCP, CI):
- `TEST_CI_REPO`, `TEST_CI_ACTION`, `TEST_CI_APPROVAL_NAME` — Civo CI workflow tests
- `TEST_GITHUB_REPO`, `TEST_GITHUB_ISSUE`, `TEST_GITHUB_TOKEN`, `TEST_GITHUB_USER` — GitHub agent tests
- `TEST_MCP_FETCH_URL` — MCP-fetch flow

**Per-agent account/user pairs** (when a test needs a different cloud account than `TEST_ACCOUNT`):
- `TEST_AWS_ACCOUNT`, `TEST_GCP_ACCOUNT`, `TEST_AZURE_ACCOUNT`, `TEST_MCP_ACCOUNT`
- `TEST_DATADOG_ACCOUNT`, `TEST_ES_METRICS_ACCOUNT`
- `TEST_K8SCHAIN_ACCOUNT`/`USER`, `TEST_ESCHAIN_ACCOUNT`/`USER`, `TEST_LOKICHAIN_ACCOUNT`/`USER`, `TEST_PROMETHEUSCHAIN_ACCOUNT`/`USER`, `TEST_ROUTERCHAIN_ACCOUNT`/`USER`
- `TEST_FOLLOWUP_ACCOUNT`/`USER`, `TEST_CONVERSATIONAL_AGENT_ACCOUNT`/`USER`

### Mutation hygiene

Tests that mutate cluster state (e.g., `scale_rag_server`, `network_pod_launch`) snapshot pre-state and restore via `t.Cleanup`. See `getDeploymentReplicas` / `listPodNames` helpers in `flagd_controller_test.go`. Cleanup failures log but don't fail the test (we don't want cleanup errors to mask real test results).

### Tests with observability-provider dependencies

Some agent paths (`metrics`, `prometheus`, `logs` via Loki, `traces`) route every query through services-server's `GetObservabilityProvider`, then fall back to the local `integration_config_values` table. In a bare local-dev test setup (no services-server JWT, no integration rows seeded for `TEST_ACCOUNT`), every such query returns `401 Unauthorized` before reaching the actual backend.

Implications:
- Tests that depend on these tool paths cannot run as-is. The k8s fixture suite reaches the metrics tool through one fixture today (`TestK8sAgent_Fixture_ElectricityMarketBiddingBug`), which is `t.Skip`'d with the unskip-when conditions documented in its function doc.
- Tests that use kubectl directly (workspace pod, in-cluster credentials) are fine — that path doesn't touch services-server.
- When adding a new fixture test that involves Prometheus/Loki/Datadog/etc., decide upfront whether it can pass without provider config; if not, either (a) seed a fixture row in `before_test`, (b) gate with a new env var that the dev sets after configuring the integration, or (c) `t.Skip` with a clear unskip-when comment.

## Common questions

**Q: My PR's test passes. Does that mean the agent is correct?**
A: Only that it produced *some* response. Look for explicit assertions on tool calls, response keywords, or the `WantAnyToolMatching` / `WantContainsAny` framework in `agent_k8s_debug_2_test.go` for stronger checks.

**Q: A test fails locally but passes in CI.**
A: Most likely your cluster state is different. These tests are not hermetic — they investigate live state.

**Q: Should I add a new test for my agent change?**
A: If the change has clear input/output, write a pure unit test in this directory (Tier 4 model). If the change relies on cluster state, write a fixture-backed test (`benchmark_fixture_test.go` model). Smoke-only `assert.NotEmpty` tests are accepted but discouraged for new code.

**Q: Why are some queries phrased oddly (e.g., `"Dev environment healthcheck?"`)?**
A: Most agent test queries are preserved verbatim from when the test was first written. They're often vague — the agent's job is to figure out what they mean, and the test only checks that something comes back. See the "stuck-detector" pattern note above.
