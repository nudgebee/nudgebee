# Event Analysis + Code Fix

Scores the production event-analysis + code-fix pipeline against synthetic,
log-driven scenarios, across four entry points: the alert-injected
diagnosis+fix pipeline (`/v1/analyze/event/rca`), `code_analyzer`'s read-only
explore mode, `PRFollowupAgent`, and a multi-turn Ask Nudgebee conversation. Full contract:
[`docs/code-analysis-eval-spec.md`](docs/code-analysis-eval-spec.md).

## Why this and not nb_scenarios or swebench alone

`llm/agents/nb_scenarios/` scores diagnosis against live k8s failures, but
never checks whether a fix is produced or correct. `swebench/` scores fix
correctness deterministically (FAIL_TO_PASS/PASS_TO_PASS), but its inputs are
prose GitHub issues, not telemetry — `swebench/README.md` names this gap
directly: *"log→code correlation... is what `docs/code-analysis-eval-spec.md`
exists for."* This suite is that spec, filled in: telemetry in, both
diagnosis and (where applicable) fix correctness scored out.

## What it measures — and doesn't

Nine scenarios across four `type`s, each dispatched differently by
`run_event_code_fix.py` (see `docs/code-analysis-eval-spec.md` §2-§6c for the
full mechanics of each):

| # | Scenario | Type | Diagnosis/Answer scored | Fix/Convergence scored |
|---|---|---|---|---|
| 001 | Memory leak (unbounded cache) → OOMKilled | `event` | ✓ | ✓ (FAIL_TO_PASS/PASS_TO_PASS) |
| 002 | Undersized memory limit → OOMKilled (same symptom, different cause) | `event` | ✓ | — (deployment/values fix, no test suite) |
| 003 | N+1 query → high API latency | `event` | ✓ | ✓ (FAIL_TO_PASS/PASS_TO_PASS) |
| 004 | Synchronous startup blocks health server → CrashLoopBackOff *(real incident, PR #37133)* | `event` | ✓ | ✓ |
| 005 | No fallback on Redis failure → restart loop *(real incident, PR #38409)* | `event` | ✓ | ✓ |
| 006 | Explore: "why does memory grow?" against 001's fixture | `explore` | ✓ (+ citation check) | n/a — read-only |
| 007 | Explore: "why is this endpoint slow?" against 003's fixture | `explore` | ✓ (+ citation check) | n/a — read-only |
| 008 | PR follow-up: harness opens a real PR with an incomplete fix, posts a review comment | `pr_followup` | n/a | ✓ (real PR, real convergence check) |
| 009 | Multi-turn: "find the bug" → "fix it" → "now raise a pr for this", with a second repo the account could drift to | `multi_turn_repo` | n/a | ✓ (every turn stays on turn 1's repo; #38547) |

Diagnosis/explore-answer scoring reuses the same RAGAS/verdict-judge
machinery `nb_scenarios` uses (`llm/agents/common/ragas_evaluation.py`,
`verdict_judge.py`). Fix/convergence scoring is deterministic: clone, apply,
run tests — **not** diff-exact-match (see `swebench/README.md`'s own warning
about that metric).

**Not measured**: organic event detection (event-type scenarios inject the
event directly — see spec §1), deterministic pre-LLM triage
(`api-server/services/triage` has its own tests).

## Prerequisites

1. **A `prometheus_alertmanager_webhook` integration** configured once on the
   benchmark account, giving you a webhook URL + token
   (`ALERTMANAGER_WEBHOOK_URL`) — needed for `event`-type scenarios only.
2. **A git integration** enabled on the benchmark account
   (`isGitIntegrationConfigured` — required for any code-fix stage to run).
3. **The fixture repo exists** — see
   [`fixture_repos/README.md`](fixture_repos/README.md); it isn't created
   automatically. Update each code-fix/explore/pr_followup scenario's commit
   reference once it does.
4. **`GITHUB_TOKEN`** — required for scenario 008 (`pr_followup`), which
   clones, pushes, and opens/closes real PRs against the fixture repo via
   `gh`. Not needed for `event`/`explore` scenarios, which clone read-only
   (local git credentials suffice, same as `code_fix_verifier.py` already
   assumed).
5. **The lightweight workload manifests are applied** (scenarios 001, 003,
   004, 005 only — 002/006/007/008 need no live k8s object):
   ```bash
   kubectl apply -f scenarios/001_memory_leak_oom/manifests/all.yaml
   kubectl apply -f scenarios/003_n_plus_one_query_latency/manifests/all.yaml
   kubectl apply -f scenarios/004_startup_ordering_crashloop/manifests/all.yaml
   kubectl apply -f scenarios/005_redis_fallback_crashloop/manifests/all.yaml
   ```
   These Deployments exist only so the fix stage's capability-annotation
   resolution finds a repo for the workload the injected alert names — they
   do not need to (and don't) actually run the buggy code live.
6. **The commit-pinning fix must be deployed** — same prerequisite
   `swebench/README.md` documents (PR #36419): without it the workspace
   clones the branch tip instead of the pinned `git.hash`, past the bug, and
   the agent correctly reports nothing to fix. Confirm before trusting a
   `resolved: false` result.

## Running

```bash
kubectl port-forward -n nudgebee svc/services-server 8120:8000 &   # api-server: alert injection + event lookup
kubectl port-forward -n nudgebee svc/llm-server 8005:8000 &        # llm-server: diagnosis + fix + explore + followup

export API_SERVER_URL=http://127.0.0.1:8120
export ACTION_API_SERVER_TOKEN=<...>
export ALERTMANAGER_WEBHOOK_URL="http://127.0.0.1:8120/api/webhooks/prometheus-alertmanager?token=<...>"
export LLM_SERVER_URL=http://127.0.0.1:8005
export LLM_SERVER_TOKEN=<...>
export BENCHMARK_ACCOUNT_ID=<cloud_accounts.id>
export BENCHMARK_TENANT_ID=<tenant>
export APP_DATABASE_URL=<...>                       # optional: enables diagnosis-stage tool-call/token/cost metrics
export GITHUB_TOKEN=<...>                           # required for scenario 008 only

python run_event_code_fix.py                              # all eight scenarios
python run_event_code_fix.py --scenario memory_leak_oom    # one
python run_event_code_fix.py --scenario explore_memory_leak --scenario explore_n_plus_one_query  # the cheap ones
python run_event_code_fix.py --diagnosis-only              # event-type scenarios: skip fix verification
```

Results stream to `event_code_fix_results.json` after every scenario, and a
re-run **resumes**, skipping anything already scored — same reasoning as
`swebench/run_swebench.py`: losing a run to a dropped port-forward should
cost minutes, not the whole run.

## VM accounts

`type: vm_event` scenarios (010-013) run the same pipeline against a VM account:
a fault is injected on a real host, the alert goes to the account's event
webhook, and the metrics/logs queries the investigation issued are scored for
host scoping and non-empty results. CLI only; see [`vm/README.md`](vm/README.md)
for the fleet variables and prerequisites.

## Running from the benchmark dashboard

Scenarios 001–007 are also registered as the **`event_code_fix`** agent in the
benchmark dashboard (Run Benchmark -> Agent -> `event_code_fix`), so they can be
launched, filtered by tag and compared like any other agent. Scenario 008
(`pr_followup`) stays CLI-only: it opens and closes a real PR on every run.

How it works: `llm/agents/event_code_fix/fixtures/*/test_case.yaml` are thin
pointers generated from `scenarios/*/scenario.yaml` (`python -m
llm.agents.event_code_fix.sync_fixtures` from `llm/benchmark`; a CI test fails
if they drift). The agent's `runner` (`llm/agents/event_code_fix/runner.py`)
replaces the chat completion with the same pipeline the CLI runs, then the
engine scores the returned diagnosis with RAGAS + the verdict judge. The fix
verification bucket / explore citation check shows up at the end of the
result's score reason, e.g. `[event_code_fix] bucket=resolved`.

The **benchmark-server** needs these env vars (it already gets
`ACTION_API_SERVER_TOKEN`, `LLM_SERVER_TOKEN` and `LLM_SERVER_URL` from the
shared `nudgebee` secret):

| Variable | Value |
|---|---|
| `API_SERVER_URL` | `http://services-server:8000` (in-cluster) |
| `ALERTMANAGER_WEBHOOK_URL` | the `prometheus_alertmanager_webhook` integration URL incl. `?token=` — event scenarios only |

The webhook token belongs to one account: select **that** account in the
dashboard, or the alert lands elsewhere and the run fails as
`ingestion_timeout`. The run's account/tenant come from the dashboard, not from
`BENCHMARK_ACCOUNT_ID`/`BENCHMARK_TENANT_ID`.

Differences from the CLI: no resume file (the dashboard has its own resume),
diagnosis-only is done by tag (`diagnosis_only`), and the dashboard's LLM /
tool-config pickers do not apply — the pipeline uses the account's own
configured model, so the RAGAS judge is the only part they influence. A
scenario whose fixture repo is not provisioned yet (`REPLACE_WITH_*`
placeholders) fails immediately with a `client_error` instead of spending an RCA.

## Operational notes

- Each injected alert (`event`-type scenarios) triggers
  `eventrule.CreateEventRule` as a side effect (the Alertmanager webhook
  parser auto-creates a persistent alert rule per `alertname` if one doesn't
  exist yet on the account) — normal production behavior, not a harness bug.
- Scenario 008 (`pr_followup`) opens and closes a real, namespaced PR
  (`event-code-fix/pr-followup-<run-id>-<timestamp>`) on the fixture repo
  every run, and sweeps any left open by a crashed prior run at the start of
  its own run — see `docs/code-analysis-eval-spec.md` §5c. Use a dedicated
  fixture repo, not one anything else depends on.
- Use a dedicated benchmark account for all of the above, not a real
  customer/dev account.

## Cost and concurrency

Sequential on purpose, same reasoning as `swebench`: workspaces are one pod
per account, so parallel scenarios would contend on the same pod. Scenario
008 is the most expensive per run (a full agent run plus real GitHub API
round-trips for PR create/comment/view/close); 006/007 (explore) are the
cheapest — no alert injection, no fixture-repo clone, no GitHub writes.
