# Per-service load tests

k6 scripts that load one service at a time, plus a runner that executes them as a Job
inside the cluster, next to the services they test. Test one service to find its limit,
then test `app` end to end to see which service gives out first.

```sh
TENANT_ID=<tenant uuid> loadtest/run.sh services-server-api smoke
```

`run.sh` reads the service tokens from the `nudgebee` Secret, streams k6's output, saves
it to `loadtest/results/`, and deletes the Job when it ends. Ctrl-C stops the test.

While the test runs, open **http://localhost:5665** for k6's live dashboard: request rate,
latency percentiles, failures and active users, as the test runs. When it ends, the same
charts are saved as `loadtest/results/<job>.html`, a single file you can open or share.
Load is set when you start a run (profile, `RATE`, `DURATION`, `MAX_VUS`), not from the
dashboard, which is view-only.

## Scripts

| Script | Drives | Required | What to watch |
|---|---|---|---|
| `services-server-api` | `POST /rpc/*` on the API pods, weighted mix from `services-server-actions.json` | `TENANT_ID` | per-action p95 in the summary, Postgres connections and slow queries |
| `services-server-metrics` | `POST /rpc/metrics`: the workload, node, pod and rightsizing utilisation charts (`metrics_aggregate_utilisation`), the same trends as PromQL (`metrics_list`) and the metrics explorer's name, label and label-value lookups; 104 variants in `services-server-metrics.json` | `TENANT_ID`, `ACCOUNT_IDS` (Kubernetes accounts) | per-variant p95, per-account p95 (overall and per action), `metrics_empty_response`, relay-server and each cluster's Prometheus. Every call reaches the account's agent and Prometheus, so a run loads those clusters too |
| `services-server-worker` | `POST /rpc-cron` on the worker pod, every allowlisted cron at once | `TENANT_ID`; `ACCOUNT_IDS` to include `K8s Recommendation refresh` | worker CPU/memory, Postgres, RabbitMQ queue depth. k6 latency is meaningless here: `/rpc-cron` replies before the job runs |
| `llm-server` | async chat, then polls the conversation like the UI | `TENANT_ID`, `ACCOUNT_ID`, mock LLM (below) | `conversation_duration`, `conversation_outcomes`, 503s on submit (worker pool full) |
| `rag-server` | `POST /get_matching_doc`, as llm-server calls it | `ACCOUNT_ID` | p95, pod CPU (the reranker is CPU-bound) |
| `app` | JSON-RPC `/api/rpc` through the gateway to services-server | `APP_URL` (the app's in-cluster Service; its name differs per environment, see `kubectl -n nudgebee get svc`), `API_TOKEN` (a personal API token for a user in the test tenant) | compare with `services-server-api` at the same rate: the difference is the gateway |

## Profiles

Second argument to `run.sh`. Rates are arrival rates: k6 starts iterations on schedule
even when the service slows down, so saturation shows up as rising latency and
`dropped_iterations` instead of being hidden.

| Profile | Shape | Use it to |
|---|---|---|
| `smoke` | 1 per `TIME_UNIT` for 30s | check the setup before anything bigger |
| `baseline` | `RATE` for 5m | get normal latency |
| `stress` | ramps to `RATE` over 14m | find the rate where errors or p95 jump |
| `soak` | 70% of `RATE` for 1h | find leaks and pool exhaustion |
| `spike` | 10% → 100% → 10% of `RATE` | see how autoscaling and queues cope |
| `users` | `VUS` users, each looping request → think time, for `DURATION` (5m) | model N concurrent people instead of a request rate |

Run in that order. Take the stress limit as the ceiling and use about 70% of it for the soak.

## Parameters

Set them in the shell that calls `run.sh`. Service URLs default to the in-cluster Service
names and are not forwarded from your shell.

| Name | Default | Meaning |
|---|---|---|
| `RATE` | 10 | peak iterations per `TIME_UNIT`; at least 4 for `stress` and `spike` |
| `TIME_UNIT` | `1s` (`1m` for llm-server) | unit for `RATE` |
| `DURATION` | per profile | length of smoke, baseline and soak |
| `MAX_VUS` | 200 | cap on concurrent requests in flight |
| `VUS`, `THINK_TIME` | 20, `1-3` | `users` profile: how many users, and the seconds each waits between requests (a range picks uniformly) |
| `P95_MS` | per script | p95 threshold; crossing it marks the run FAIL |
| `USER_ID` | empty | services-server: run the per-user RBAC path instead of a tenant-admin context |
| `CRONS`, `REPEAT`, `INTERVAL` | all, 1, 60 | worker: which crons, how many rounds, seconds between rounds |
| `ACCOUNT_IDS` | empty | worker: limits the two recommendation crons to these accounts; `K8s Recommendation refresh` only runs when it is set. metrics: the Kubernetes accounts to query (comma-separated) |
| `ACTIONS`, `POOL_SIZE`, `SWEEP` | all, 10, false | metrics: only these actions; how many workloads/pods/nodes/PVCs setup samples per account; `SWEEP=true` runs every variant once per account, one at a time, and logs status, latency and series count per call |
| `QUERY`, `POLL_INTERVAL`, `MAX_WAIT` | see script | llm-server |
| `MODULES`, `K`, `UNIQUE_QUERIES` | `knowledge_base`, 3, false | rag-server; `UNIQUE_QUERIES=true` defeats its embedding cache |
| `K6_PROMETHEUS_RW_SERVER_URL` | empty | push k6 metrics to a Prometheus/VictoriaMetrics `/api/v1/write` to graph them next to pod and DB metrics |
| `NAMESPACE`, `TOKEN_SECRET`, `K6_IMAGE` | `nudgebee`, `nudgebee`, `grafana/k6:2.2.0` | where the Job runs and which Secret holds the tokens |
| `DASHBOARD_PORT` | 5665 | local port for the live dashboard; change it to run two tests at once |

## Before you run on a shared environment

- Use a dedicated tenant. The load lands on the same Postgres everyone else is using.
- Every script except the worker's stops itself once more than 20% of requests fail for 30s.
- The worker script only fires crons in `SAFE_CRONS` (recompute jobs). The others email
  users, delete data, bill, comment on GitHub or push to agents; the script refuses them.
  Read a cron's handler in `api-server/services/api/cron.go` before adding it.
- A dedicated tenant does not contain the worker test. Most of these crons recompute for
  every tenant, exactly as their schedules already do, so each extra run adds load for
  the whole environment. `K8s Recommendation refresh` also dispatches scan Jobs to each
  account's agent, so it is skipped unless `ACCOUNT_IDS` names the accounts to scan.
- The metrics script queries each account's own Prometheus through relay-server and the
  agent. Load on a production cluster's account lands on that cluster. Run `SWEEP=true`
  first: it is one request per variant per account, and it does not stop itself on
  failures, so that every variant gets a line in the log.
- The default services-server mix is read-only. Adding write actions to
  `services-server-actions.json` writes real rows.

## llm-server: use the mock LLM

Without it, each iteration spends model tokens and can run real tools against clusters.

1. Start the mock in the cluster (commands at the top of `k8s/mock-llm.yaml`).
2. In the load-test tenant's LLM configuration, add a config and make it the default:
   provider `custom`, API Endpoint `http://loadtest-mock-llm:9990/v1`, Authentication
   `OAuth2 client credentials`, Token URL `http://loadtest-mock-llm:9990/oauth2/token`,
   Client ID `mock-client-id`, Client Secret `mock-client-secret`, Extra request headers
   `{"projectId": "loadtest"}`. Every conversation in that tenant now gets the canned reply.
3. `TENANT_ID=… ACCOUNT_ID=… loadtest/run.sh llm-server smoke` should end with one
   `completed` outcome.

The canned reply is plain text with no tool calls, so each conversation ends after one
model turn. The test measures llm-server's orchestration, database writes, retrieval
and queueing, not tool execution. `MOCK_GW_CANNED_DELAY` in the manifest sets the fake
model latency. To exercise real agent behaviour, set `MOCK_GW_UPSTREAM_URL` to a cheap
OpenAI-compatible model; that costs tokens.

## Reading the result

- The summary prints each threshold with ✓ or ✗, and `run.sh` ends with PASS or FAIL.
- The HTML report in `results/` has the same charts as the live dashboard. k6 skips it for
  runs too short to chart; a 30s smoke run is enough.
- `dropped_iterations` above 0 means k6 could not start iterations on schedule: either
  `MAX_VUS` is too low or the service is saturated. Latency tells you which.
- Profiles and heap dumps: services-server, llm-server and llm/gateway serve pprof at
  `/debug/pprof/`. Take one at peak load, e.g.
  `kubectl -n nudgebee port-forward svc/services-server 8000` then
  `go tool pprof http://localhost:8000/debug/pprof/profile?seconds=30`.

## Running a script locally

Against a local stack, with the [k6 binary](https://grafana.com/docs/k6/latest/set-up/install-k6/):

```sh
cd loadtest/scripts
SERVICES_SERVER_URL=http://localhost:8000 ACTION_API_SERVER_TOKEN=<token> \
TENANT_ID=<tenant> k6 run services-server-api.js
```

The URL variables are `SERVICES_SERVER_URL`, `SERVICES_SERVER_WORKER_URL`, `LLM_SERVER_URL`,
`RAG_SERVER_URL` and `APP_URL`.
