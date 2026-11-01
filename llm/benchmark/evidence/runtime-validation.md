# Metric evidence runtime validation

This supplements the bounded model replay in [README.md](README.md). It does
not replace the deployed-agent/provider benchmark requested in #880.

## Self-contained runtime regression

From `llm/llm-server`:

```sh
go test -p=1 ./agents/core -run TestMetricProvenancePlannerExecution -count=1
```

The test runs both actual ReAct3 and ReAct4 `Plan` implementations. Their actions
are dispatched through `plannerExecutor.doAction` into HTTP-backed fixture tools.
Eight cases make 88 real loopback HTTP requests: empty metric, nonempty zero,
nonempty 0.245 sample and HTTP 503, for each planner. Eleven tool turns precede
each final candidate and answer critique. The test checks executor status,
observation bytes, no-data classification, E1 evidence references, compression,
and the messages actually supplied to final generation and critique.

The empty case stresses the 4,096-character byte-fallback budget. Other controls
use the default 200,000-character fallback budget with about 216 kB of fixture
observations, so compression activates without deliberately overflowing the
recent-step budget. LLM-backed summarization is disabled for deterministic
coverage. During exploration, the 4 KiB hard cap discarded old normal/error
observations; this regression does not assert their retention at that extreme
budget. It asserts normal/error retention at the default fallback budget.

The model is scripted. The HTTP tools stand in for provider and series-discovery
tools; they are not the production Prometheus adapter or tool registry. Actions
and final answers are supplied, so this proves runtime evidence transport, not
autonomous discovery, citation choice, numeric conversion, answer correctness or
hallucination reduction. In particular, the scripted 245 ms answer is not a
measured model conversion result. No production planner or prompt was changed.

## Why a deployed-model run remains outstanding

On 2026-11-01, the actual Agy CLI read-only harness review timed out after 120
seconds without output. Direct completion probes of the four explicitly free
models in the existing Apinex configuration all returned HTTP 402:
`free/glm-5.3-flash`, `free/kimi-k3`, `free/muse-spark-1.3` and
`free/deepseek-v4.1-flash`. No paid model was selected. The other configured
provider had no explicitly free model aliases and was not invoked.

Ollama and LM Studio are installed, but neither had a listening local endpoint.
Cached Qwen3.5 9B weights occupy 5.24–6.14 GiB. The host had 13.8 GiB total RAM
and only 2.1 GiB free during the parallel tests, so loading them would compete
with the running test environments. No model server or download was started.
These local quantized variants would not establish deployed Qwen parity anyway.

The repository's broader `go test -tags=e2e ./agents/core` mode also has existing
duplicate declarations between `api_service_agent_test.go` and
`api_service_agent_e2e_test.go`, and between
`conversation_model_config_integration_test.go` and
`conversation_model_config_e2e_test.go`. It fails during compilation before any
live test starts. The hermetic regression stays in the normal unit build with
the other self-contained core tests; the unrelated migration was not edited.

## Provisioning the remaining benchmark

1. Supply a reachable compatible model endpoint with quota, exact model/revision
   and API credentials in local configuration. For the Qwen claim, provide the
   actual intended Qwen deployment. A free CLI alias is not a direct server
   endpoint. React4 requires working native function calling and replay.
2. Provide a disposable Nudgebee test tenant/account/user with an authenticated
   llm-server and services-server, the migrated PostgreSQL schema, and a configured
   synthetic Prometheus source. Seed nonempty zero, latency/rate/utilization,
   partial-data and known-empty workloads with known time ranges. No production
   customer data is required.
3. Run isolated baseline and candidate deployments against the same database/data
   snapshot and model settings. Baseline prompt revision is `32d898c28`; candidate
   prompt revision is `f5adddf04` (unchanged by the evidence commits). Record model
   revision, temperature, engine, feature flags, actual tool outputs and step IDs.
4. Run the existing `llm/benchmark/llm/agents/metrics` fixture selection, including
   `011_loop_trap_no_data`, then broader `nubi` normal-investigation regressions.
   Add the exact #291 question and seeded zero/partial/conversion controls. Check
   actual tool traces, discovery, compression and citations, not only nonempty
   answers. Repeat both arms to expose stochastic failures.
5. For the complete repository RAGAS report, configure its judge and embeddings
   providers too (`BENCHMARK_EVAL_LLM_*` and `BENCHMARK_EVAL_EMBEDDINGS_*`), plus
   `LLM_SERVER_URL`, matching `LLM_SERVER_TOKEN`, `APP_DATABASE_URL` and test
   account/user/tenant. Direct pytest runs avoid browser magic-link email setup.
   If either arm cannot reproduce the hallucination, report that result without
   claiming a measured improvement.
