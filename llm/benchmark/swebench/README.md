# SWE-bench × NuBi

Scores the **production path** — NuBi's chat API → `code_analyzer` → workspace pod →
code-analysis — against [SWE-bench Verified](https://www.swebench.com/) (500 real
GitHub issues with the maintainer's fix as ground truth).

## Why this and not Terminal-Bench

We ran Terminal-Bench 2 first (`../harbor_bench/`, since removed). It scored 1/10, and
the number was not usable: Terminal-Bench measures a **model** doing generic terminal
work, so swapping `gemini-2.5-flash` for a better model moves it far more than any
change to our harness does. It also cannot see our dominant failure class, because it
has no logs, no telemetry, and no repository history.

SWE-bench fits because a task is *a bug report plus a large unfamiliar repo* — the same
retrieval problem as correlating an incident with a customer's code — and because the
labels are free and human-verified.

What it still does **not** measure: log→code correlation. SWE-bench inputs are prose
issues, not telemetry. That gap is what `docs/code-analysis-eval-spec.md` exists for.

## What it measures

**The score is `resolved`, and it is SWE-bench's, not ours.** Per instance it is
binary: every `FAIL_TO_PASS` passes and no `PASS_TO_PASS` regresses
(`ResolvedStatus.FULL`), decided by the task's own `test.sh`. The aggregate is
`resolved / attempted`, produced by `swebench.harness.reporting.make_run_report`.
We compute no part of it — see `k8s/collect.py`.

Every instance lands in exactly one of SWE-bench's buckets, and only the first counts:

| Bucket | Meaning |
|---|---|
| `resolved` | tests pass |
| `unresolved` | patch applied, tests still fail |
| `empty_patch` | the agent answered but produced no diff |
| `error` | grading could not be completed |
| `incomplete` | no prediction — the agent never answered |

The denominator is what was **attempted**, never what came back. An instance the
agent never answered stays in it. Getting this wrong once reported a 50% run as
61.5%.

`file_hit@1`, `line_in_hunk` and `diff_exact` are also computed, but they are
**diagnostics, not scores** — they compare to nothing published. `diff_exact` in
particular is misleading on its own: it read 0.077 on a run that resolved 50%,
because a correct fix written differently scores zero.

**Record the entry agent with every run.** Two runs that reached the agent by
different paths are not comparable, and nothing in SWE-bench's report captures
that. Two of our own rates were retired for having entered via
`k8s_orchestrator` and reached `code_analyzer` only by delegation.

### Current result

`code_analyzer`, 16-instance stratified set, `@code_analyzer` entry:
**12/16 = 75.0% resolved**. At n=16 the 95% interval is roughly ±24 points, so
treat it as "around 75", and do not compare it with leaderboard entries, which
are scored over all 500.

## Prerequisites

**The commit-pinning fix must be deployed** (PR #36419, merged as `e336e878d7`). Without
it the workspace clones the branch tip, which for these instances is years past the fix,
and the agent correctly reports there is nothing to fix — scoring 0 everywhere for a
reason that has nothing to do with its ability. Confirm the running images contain it:

```bash
kubectl get pods -n nudgebee -o custom-columns='POD:.metadata.name,IMAGE:.spec.containers[0].image' \
  | grep -E 'llm-server|workspace'
```

Download the dataset (500 tasks, ~25s):

```bash
harbor datasets download swe-bench/swe-bench-verified     # or any local copy
```

## Running

```bash
kubectl port-forward -n nudgebee svc/llm-server 8005:8000 &

export NUBI_URL=http://127.0.0.1:8005
export NUBI_TOKEN=<LLM_SERVER_TOKEN>
export NUBI_ACCOUNT_ID=<cloud_accounts.id>
export NUBI_TENANT_ID=<tenant>

python run_swebench.py --dataset /path/to/swe-bench-verified --limit 16
python run_swebench.py --dataset ... --instance astropy__astropy-12907   # single
```

Progress is one line per instance — `F` file hit, `L` line in hunk, `=` exact diff:

```
[1/16] OK  FL= 120.3s astropy__astropy-12907
[2/16] OK  F..  95.1s django__django-11087
```

Results stream to `swebench_results.json` after every instance, and a re-run **resumes**,
skipping anything already scored. A run is a long sequence of independent LLM calls;
losing an hour of them to a dropped port-forward is the difference between this being
usable and not.

## Choosing instances

Instances are **Django-heavy** — 231 of 500 (46%), then sympy 75, sphinx 44, matplotlib
34, scikit-learn 32. A random sample mostly measures "how well does it navigate Django".
Stratify by repo instead:

```bash
for r in django sympy sphinx-doc matplotlib scikit-learn pydata astropy pytest-dev; do
  ls -d "$DATASET"/${r}__*/ | head -2
done
```

Difficulty splits 194 `<15 min fix` / 261 `15 min - 1 hour` / 42 `1-4 hours` / 3 `>4 hours`.
The `<15 min` band is weak for discriminating between versions — a decent model solves
those regardless. The `15 min - 1 hour` band is where scaffolding quality shows.

## Cost and concurrency

Every instance is a full agent run: repository clone plus many LLM calls, ~2–5 minutes
each. The runner is **sequential on purpose** — workspaces are one pod per account
(`workspace-<account_id>`), so parallel instances contend on the same pod. Running
several accounts concurrently is the way to parallelise, not threads.

Always record the model alongside any number. The Terminal-Bench mistake was quoting a
score from a model we do not ship.
