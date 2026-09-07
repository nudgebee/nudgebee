# Existing-skill observability verification

Inventory read through DB MCP on 2026-09-06 for account a2a30b02-0f67-42e5-a2ab-c658230fd798 (k8s-dev). No rows changed. Recheck status at execution time.

| Path | Existing skill | Size/status | Observable assertion |
| --- | --- | --- | --- |
| logs v3 | Nudgebee_Namespace_Log_Correlation | 3,526 bytes, active/enabled; mapped to fetch_logs and logs | For cross-service investigation, correlate account/tenant plus the same time window, not a shared trace_id. Loki labels use namespace/pod/app. Inspect fetch_logs_v3 input and rendered query. |
| metrics -> Prometheus | APP_Metrics_Discovery | 4,941 bytes, active/enabled; mapped to prometheus | Discover series for the actual workload and namespace before guessing metric prefixes. Check selectors use the returned label group, with exact vs prefix matching preserved. |
| metrics -> dashboard | Golden_Signal_Dashboard | 4,525 bytes, active/enabled; mapped to prometheus and k8s_debug | Discover unknown families; keep missing metrics distinct from zero. Optional later case: raw timestamped series reach the visualizer. |
| traces provider | es_metrics_discovery | 4,625 bytes, active/enabled; mapped to elastic_search_metrics but content explicitly applies to logs/metrics/traces | Verify actual trace fields and configured scope before filtering; bound time. This is a general backend guide, not a dedicated traces runbook. Confirm a usable traces provider/service before running. |

Mappings are relevance/attribution inputs, not visibility boundaries. A traces agent may discover the general backend guide account-wide even though mapped to metrics.

## Run order

1. Use local APIs or existing e2e harness; nbctl requires the gateway and is unsuitable for this local setup. Confirm llm-server, RAG forward, workspace, and authentication are ready before starting.
2. First use explicit skill names to isolate loading and propagation. Then repeat natural task wording without naming the skill to verify discovery. Do not put the skill's expected answer in the prompt.
3. Start with current Auto policy and the default-enabled workspace path. The three skills above 4 KiB exercise workspace materialization, but their full contents still fit in the initial bounded response. They do not prove selective reads or multi-MiB behavior.
4. Record account, selected agent/provider, fixed absolute time window, conversation/message IDs, loaded candidate/KB ID, workspace reference and read ranges, actual downstream tool input/rendered query, result, and final citations.
5. Require both the correct skill load and an actual operational call with matching constraints. A final answer mentioning the skill is insufficient. Empty results must retain verified scope and cannot prove system health.
6. Test metrics/traces policy inheritance separately from content selection. Offline wrapper tests cover all four policies; live runs should verify Disabled does not load and Model-directed loads only through the model's tools. Restore any user setting changed for these runs.

## Suggested requests

- Logs: "Use Nudgebee_Namespace_Log_Correlation to investigate errors across llm-server and services-server in namespace nudgebee for this account during <fixed window>. Run read-only checks and report the observed correlation."
- Metrics: "Use APP_Metrics_Discovery to identify custom metrics exposed by <verified workload> in <verified namespace>, then query one confirmed metric during <fixed window>."
- Traces: "Use es_metrics_discovery to inspect slow traces for <verified service> during <fixed window>. Run read-only queries and report confirmed fields and results."

Resolve actual workload/service/provider from the local environment before substituting placeholders. No dedicated traces skill was identified in this account's 28-row inventory. All six integration KB rows were archived; do not use them for a positive Confluence/ServiceNow test or reactivate them merely for testing.

## Local results after services-server restart (2026-09-07)

- Metrics: conversation `50d8517c-1c47-40da-84be-6afb923a1997` loaded APP_Metrics_Discovery before successful series discovery and queried a confirmed nb_llm_api_requests metric with namespace/pod selectors over 15 minutes. Nonempty time series returned.
- Traces: conversation `a4698fdd-a80f-4e54-b1de-6da94e2cf70e` returned real trace data, but loaded es_metrics_discovery after two backend queries. Load-first adherence remains open.
- Logs: conversation `3c0f4ad5-5d53-4b36-a24a-4bb7788eb746` loaded the correlation skill, then widened the requested 15 minutes to 24 hours. Indexed queries failed with a Loki parse error; Kubernetes fallback omitted time bounds. The test was interrupted after repeated failures; it did not pass operational acceptance.
- The earlier service authorization mismatch was resolved for these runs. All tests used existing documents without editing KBs or settings.
- The larger examples were saved to workspace and included inline through their final lines. A separate document beyond the initial response limit is still needed to prove selective reads. Natural-query discovery and live policy variants remain unverified.

The opt-in `TestObservabilityExistingSkillLive` requires the `e2e` build tag, `RUN_OBSERVABILITY_SKILL_E2E=true`, `TEST_ACCOUNT`, `TEST_USER`, `TEST_TENANT`, `TEST_OBSERVABILITY_AGENT`, and `TEST_OBSERVABILITY_QUERY`, plus local service/model configuration. It uses `env:global` model configuration. Run with `go test -tags=e2e ./agents -run '^TestObservabilityExistingSkillLive$' -count=1 -v -timeout 6m`. A passing harness only confirms a response returned; inspect persisted tools for acceptance and sanitize logs before sharing.
