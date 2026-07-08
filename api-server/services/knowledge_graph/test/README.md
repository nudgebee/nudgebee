# Knowledge Graph — End-to-End Test Suite

This directory (`package test`) is the **regression net for the Knowledge Graph**.
The KG is a silent-failure surface: a wrong or missing node/edge doesn't throw — it
quietly degrades event investigation, the frontend graph, and the LLM SDG agent. This
suite proves that a known input cluster produces a known graph, and that the DB-backed
read/tombstone paths behave.

> **Scope note.** This package is **test-only** — there is no production Go code in the
> `knowledge_graph` root package. The code under test lives in
> [`../core`](../core), [`../sources`](../sources), and [`../flow_sources`](../flow_sources).
> These tests drive the **real** converters and merge pipeline via the public test
> helpers in [`../sources/test_helpers.go`](../sources/test_helpers.go).

---

## TL;DR — how to run

```bash
cd api-server/services

# Tier A only (no DB) — always runs, the always-on CI gate
go test ./knowledge_graph/test/ -count=1

# Everything incl. Tier B (needs a Postgres metastore)
export APP_DATABASE_URL='postgres://postgres:postgrespassword@localhost:5432/nudgebee?sslmode=disable'
go test ./knowledge_graph/test/ -count=1 -v

# One scenario
go test ./knowledge_graph/test/ -run TestE2E_TierA_AWSAllEdges -v

# Regenerate golden files after an intentional converter change (REVIEW the diff)
go test ./knowledge_graph/test/ -run E2E_TierA -update
```

- The DB env var is **`APP_DATABASE_URL`** (viper `AutomaticEnv` on mapstructure key
  `app_database_url`), **not** `DATABASE_URL`.
- Always pass **`-count=1`** when you change `APP_DATABASE_URL` — Go's test cache ignores
  env-var changes and will replay a stale (e.g. no-DB) result otherwise.

### Standing up the Postgres metastore for Tier B

The repo's own compose stack provisions everything. Bring up Postgres and apply the
migrations once:

```bash
cd nudgebee-enterprise
docker compose up -d postgres                      # postgres:16 on localhost:5432

# apply all Postgres migrations (one-off, no RabbitMQ needed):
docker compose run --rm --no-deps --entrypoint bash migrations -c '
  psql "$APP_DATABASE_URL" -v ON_ERROR_STOP=1 -q -c "CREATE SCHEMA IF NOT EXISTS nudgebee;"
  migrate -path ./migrations/app \
    -database "${APP_DATABASE_URL}&x-migrations-table=%22nudgebee%22.%22schema_migrations%22&x-migrations-table-quoted=true" up'
```

Data persists in the `pg_data` volume, so migrations don't re-run next time.
Teardown: `docker compose stop postgres` (keep data) or `docker compose down -v` (wipe).

---

## Architecture — two tiers, one shared golden

The **golden JSON is the contract**. Tier A proves *JSON → correct graph*; Tier B loads
that same graph into a real DB and proves *graph → correct reads + correct tombstoning*.

```
go test ./knowledge_graph/test/
  ├─ Tier A (no DB, ALWAYS runs) ──────────────────────────────────────────────
  │    load testdata/e2e/<scenario>/input/*.json
  │      → real converters (../sources/test_helpers.go)
  │      → real merge pipeline (mergeGraph, mirrors BuildGraphs phase 3+)
  │      → normalize + diff against testdata/e2e/<scenario>/golden.json  (must be empty)
  │
  └─ Tier B (needs APP_DATABASE_URL, else t.Skip via testenv.RequireMetastore) ──
       SaveNodes/SaveEdges(golden) → read APIs → assert
       Save@vN → mutate → Save@vN+1 → markInactive* → assert is_active / last_sync_version
```

`mergeGraph` (in [`e2e_scenarios_test.go`](e2e_scenarios_test.go)) hand-orders the same
phases the real `BuildGraphs` orchestrator runs (the orchestrator itself can't run DB-free):

```
DeduplicateNodesWithIDMapping → DeduplicateEdgesWithPriority
  → [BuildCrossAccountRelationships]        (cross-account scenarios only)
  → CollapseEnrichedExternalServices
  → ResolveGCPManagedServiceCalls
  → DeduplicateEdgesWithPriority
```

---

## File map

| File | What it holds |
|---|---|
| [`e2e_diff_test.go`](e2e_diff_test.go) | Golden engine: `normalizeGraph` (drops volatile fields, joins edges by `unique_key`), `assertGoldenGraph`, `diffGraphs`, and the **`-update`** flag. Built first; underpins every Tier A scenario. |
| [`e2e_scenarios_test.go`](e2e_scenarios_test.go) | **Tier A** scenarios + shared helpers (`buildK8sGraph`/`buildAWSGraph`/`buildGCPGraph`/`buildAzureGraph`, `mergeGraph`, `disableCloudCLI`, `loadCloudResourceRows`, coverage gates). |
| [`e2e_mocked_infra_test.go`](e2e_mocked_infra_test.go) | Fake cloud-collector tier: `fakeCollector(t, handler)` spins an `httptest` server on `/v1/cloud/execute_cli` to exercise edges that need a live CLI fetch (MANAGES, GCP firewall/LB chain). |
| [`e2e_relationship_coverage_test.go`](e2e_relationship_coverage_test.go) | Aggregate relationship-type coverage report across all goldens. |
| [`e2e_tier_b_test.go`](e2e_tier_b_test.go) | **Tier B** read-API tests (`GetCompleteGraph…`, `SearchNodes`, `TraverseDirectional`, `GetNodeByID`, `GetEdgeByID`, `GetFilterOptions`). |
| [`e2e_tier_b_build_test.go`](e2e_tier_b_build_test.go) | **Tier B** tombstone/freshness (`SyncVersionRestamp`) + the B3 orchestrator-smoke scaffold (skipped). |
| [`integration_test.go`](integration_test.go) | **Legacy** phase tests (predate the golden harness) **and** `TestMain` + shared fixture loaders (`loadAWSResources`, `loadK8sWorkloads`, …) that the Tier A helpers reuse. See "Legacy tests" below. |

### Related, but NOT in this directory

- **Node-type completeness** lives in [`../sources`](../sources)
  (`all_types_completeness_test.go`, `dispatch_completeness_test.go`) because it reads
  **unexported** registry maps (`awsResourceTypeMap`, `gcpResourceTypeMap`,
  `azureTypeToNodeType`). It asserts every registry entry maps to a conscious `NodeType`
  and fails when a new type is added without a decision. Run:
  `go test ./knowledge_graph/sources/ -run 'Completeness|AllTypes'`.
- **Python discovery seam**: `collector-server/k8s-collector/app/tests/test_discovery_shape.py`
  pins the discovery-message → source-row shape the Go converters read.

---

## `TestMain` — the package is hermetic

[`integration_test.go`](integration_test.go) defines a `TestMain` that **zeroes
`config.CloudCollectorServerUrl` for the whole package** (via `disableCloudCLI`).

Why it matters: that config key has a **non-empty default**
(`http://cloud-collector-servert:8000`). Without the reset, any test that reaches an
AWS/GCP/Azure live-fetch path would try a real HTTP call and **panic on a nil security
context** the moment a real metastore is present. The reset makes `cloud.ExecuteCli`
short-circuit, so Tier A stays offline and deterministic.

The mocked-infra tests **override** the URL per-test (pointing it at their `httptest`
fake) and restore it — so they still exercise the CLI path against a controlled fake.

---

## Test inventory

### Tier A — golden build-shape (no DB)

| Test | Protects |
|---|---|
| `TestE2E_TierA_BasicK8s` | k8s converter baseline (Workload/Node/Pod, `RUNS_ON`, `BELONGS_TO`) |
| `TestE2E_TierA_K8sWorkloadKindRouting` | workload `kind` → NodeType routing |
| `TestE2E_TierA_AWSResources` / `GCPResources` / `AzureResources` | per-provider converter output |
| `TestE2E_TierA_AWSAllEdges` | AWS edge coverage gate — asserts **14** relationship types offline. `MANAGES` (the 15th) is CLI-only → covered by mocked `AWSManages`. |
| `TestE2E_TierA_GCPAllEdges` | GCP edge coverage gate — asserts **9** offline types (HOSTED_ON, BELONGS_TO, RUNS_AS, USES_SECRET, PULLS_FROM, CALLS, HAS_ACCESS_TO, PUBLISHES_TO, SUBSCRIBES_TO). `PROTECTS` + LB-chain `ROUTES_TO`/`ASSOCIATED_WITH` are CLI-only → covered by mocked `GCPFirewallProtects` / `GCPLoadBalancerChain`. |
| `TestE2E_TierA_AzureAllEdges` | Azure edge coverage gate — asserts **9** offline types (BELONGS_TO, HOSTED_ON, PROTECTS, ASSOCIATED_WITH, ROUTES_TO, MANAGES, RUNS_AS, HAS_ACCESS_TO, CALLS). Only DNSZone→VNet `ASSOCIATED_WITH` (live Resource Graph) is out of scope. |
| `TestE2E_TierA_K8sPlusAWS` | cross-source enrichers (AWS LB ↔ K8sService, K8s Node ↔ EC2 `RUNS_ON`) |
| `TestE2E_TierA_AWSLBToK8s` | AWS LoadBalancer → K8s enricher |
| `TestE2E_TierA_FlowEdges` | dedup + edge-priority (k8s > aws > ebpf > traces) with injected flow edges |
| `TestE2E_TierA_MultiAccount` | cross-account rules from `default_relationships.json` |
| `TestE2E_TierA_GCPCrossAccountK8s` / `AzureCrossAccountK8s` | Workload-Identity `ASSUMES` / AKS `RUNS_ON` cross-account |

### Mocked-infra (fake cloud-collector)

| Test | Exercises |
|---|---|
| `TestE2E_MockedInfra_AWSManages` | CloudFormation stack expansion → `MANAGES` (the one AWS edge that needs a live CLI fetch) |
| `TestE2E_MockedInfra_GCPFirewallProtects` | firewall-rules fetch → `PROTECTS` |
| `TestE2E_MockedInfra_GCPLoadBalancerChain` | url-maps/backend-services/health-checks → `ROUTES_TO` + `ASSOCIATED_WITH` |
| `TestE2E_MockedInfra_AWSLBPodTarget` | **skipped** — Prometheus-mock scaffold (not yet built) |

### Tier B — DB-backed (skips without `APP_DATABASE_URL`)

| Test | Protects |
|---|---|
| `TestE2E_TierB_ReadAPIs` | every read method off a saved golden (complete graph, type filter, search, traverse, get node/edge, filter options) |
| `TestE2E_TierB_SyncVersionRestamp` | tombstoning/freshness: removed → `is_active=false`, survivors bumped `last_sync_version` |
| `TestE2E_TierB_OrchestratorSmoke` | **skipped** — B3 scaffold: needs seeded `cloud_accounts` + `knowledge_graph_tenant_filters` + a mock source to run the real `BuildGraphs` and assert == the `basic_k8s` golden |

---

## Adding a Tier A scenario

1. Create the fixture directory and hand-author minimal input JSON (in the
   `CloudResourceRow` / `K8sWorkloadRow` / `K8sNodeRow` shape the converters consume):

   ```
   testdata/e2e/<scenario>/input/aws.json        # and/or gcp.json, azure.json, k8s_nodes.json
   ```

   Keep it small — the golden must stay human-reviewable. **Sanitize** anything captured
   from real data: no real AWS account numbers / tenant IDs — use `123456789012`-style
   placeholders (standing repo rule).

2. Add a `TestE2E_TierA_<Name>` in [`e2e_scenarios_test.go`](e2e_scenarios_test.go):
   `load → build<Provider>Graph → mergeGraph → assertGoldenGraph(e2eGoldenPath("<scenario>"), …)`.

3. Generate and **review** the first golden before committing (so a current bug isn't
   frozen in):

   ```bash
   go test ./knowledge_graph/test/ -run TestE2E_TierA_<Name> -update
   git diff testdata/e2e/<scenario>/golden.json     # eyeball it
   ```

   Thereafter the golden is the frozen contract; any diff is the regression signal.

4. If the scenario adds a new relationship type, add it to the relevant coverage gate
   (`wantEdgeTypes` in the `*AllEdges` test) so a converter regression fails independently
   of the golden.

---

## Legacy tests (`integration_test.go` `TestPhase*` / `TestEndToEnd*` / `TestKMS*`)

These **predate** the golden harness and were only ever run when the metastore was
*absent* (so they silently skipped and were never validated). They overlap the golden
scenarios and are slated for migration-then-deletion per
[`../CLAUDE.md`](../CLAUDE.md) / the architecture-decisions log.

Current status:

- Those whose assertions still match current output **run and pass** with a DB
  (`TestPhase1_K8sResourceSource`, `TestPhase1_GCPResourceSource`,
  `TestPhase3_K8s_ConfigAndStorageNodes`, `TestPhase1_AWSResourceSource`,
  `TestPhase1_AWS_RelationshipMatching`, `TestPhase2_EBPF/TracesFlowSource`).
- Five assert data that **cannot exist in an offline/hermetic run** — live
  cloud-collector enrichment (KMS EBS/EFS encryption, route-table/IAM `HOSTED_ON`) or
  fixture nodes that were never present (EKS cluster, API Gateway) or eBPF/traces `CALLS`
  edges. They are **explicitly skipped** via `skipSupersededByGolden(t, reason)`, each
  pointing at its golden replacement.
- `*_Live` tests need a real cloud account and skip via `testenv.RequireTenant(t)`.

**Do not "fix" a superseded skip by fabricating counts** — migrate its intent into a
golden scenario instead, then delete it.

---

## Expected results

```
# no DB
go test ./knowledge_graph/test/ -count=1
→ ok   (Tier A + mocked-infra pass; Tier B + DB-gated legacy tests skip)

# with APP_DATABASE_URL
go test ./knowledge_graph/test/ -count=1 -v
→ ok   PASS: 27   SKIP: 9   FAIL: 0
```

The 9 skips are all intentional and labeled: 2 unfinished scaffolds
(`TierB_OrchestratorSmoke`, `MockedInfra_AWSLBPodTarget`), 2 `_Live` tests (need a real
cloud account), and the 5 superseded legacy tests.

---

## Gotchas

- **`APP_DATABASE_URL`, not `DATABASE_URL`.**
- **`-count=1` when toggling the DB** — the test cache ignores env changes.
- **Goldens are generated, never hand-edited** — change the fixture or converter, then
  `-update`, then review the diff.
- **`DeduplicateEdgesWithPriority` keys by `src:dst` without the relationship type** — two
  different edge types between the same pair collide; a fixture that relies on both
  surviving will lose one. (This is why some scenarios drop a competing edge to keep the
  one under test.)
- **Volatile fields** (`id`, `created_at`, `updated_at`, `last_sync_version`, and
  `is_active` outside Tier B) are normalized away before diffing — don't assert on them in
  Tier A.
