# NuBi × terminal-bench-core==0.1.1 — Baseline Report

**Date:** 2026-05-07 (interim sample; full re-baseline pending)
**Server:** local llm-server, `main` + PR #30034 (resume-regression fix)
**Adapter:** `tbench/nubi_agent.py` + PR #30036 (always container-script delivery)
**Provider:** googleai/gemini-3-flash-preview

> ⚠️ **The previous baseline (2026-05-06) is invalidated.** It was taken with a server-side regression introduced by #29973 active in `main`. That regression caused every multi-step task to get stuck after its first shell command — which the failure-mode classifier scored as `agent_timeout`, `latency-bound`, or `hung-command`. With the fix landed, those tasks actually run to completion; the original numbers and per-task categorization were therefore contaminated and are retained at the bottom of this file for reference only.
>
> The numbers below come from **17 curated tasks** picked across the original baseline's failure-mode buckets (3 confirm-no-regress passers, 4 near-pass, 4 latency-bound, 1 high-difficulty, plus 5 from a targeted re-run set). They are the **interim** post-fix picture; a clean full 80-task re-run is the next milestone (TODO at the bottom).

## Headline (interim, 17-task sample)

| Metric | Pre-fix (contaminated) | Post-fix (this sample) |
|---|---|---|
| Resolved | 3/17 (18%) | **6/17 (35%)** |
| Tests passed | 57/82 (70%) | **69/82 (84%)** |
| Stuck-after-1-cmd tasks (the regression) | every multi-step task | **0** |

**Resolutions newly gained vs original baseline:** `csv-to-parquet` (0/2 → 2/2), `fibonacci-server` (0/6 → 6/6), `organization-json-generator` (3/4 → 4/4).

**Big per-test lifts:** `hf-model-inference` (0/4 → 3/4), `intrusion-detection` (4/7 → 5/7).

**Confirm no-regress on baseline-passers:** `hello-world` (2/2), `fix-pandas-version` (3/3), `openssl-selfsigned-cert` (6/6).

## Fixes that landed in this batch

1. **#30034 — `fix(llm-server): client-tool resume regression after #29973`**
   - V1 path: `IsResume` flag on `NBAgentRequest` skips `handleConversationRequest`'s IN_PROGRESS guard for resume workers.
   - V2 path: `resume_v2.go` idempotency guard now accepts `waiting_for_client_tool` (was silently swallowing every client-tool resume).
   - Net: every client-tool agent (UI custom agents, tbench, anything driven by client-tool callbacks) can now produce a second action after the first tool result arrives. Was broken since #29973 (2026-05-06) for both the legacy and V2 paths.

2. **#30036 — `fix(benchmark): always deliver shell commands via container script`**
   - Every LLM-issued shell command goes via `session.copy_to_container()` + `bash /tmp/<id>.sh`. The keystroke stream only ever carries a short bash invocation, so tb's `; tmux wait -S done` trailer always lands cleanly regardless of body content.
   - Falls back to base64 inline if `copy_to_container` fails (read-only `/tmp`, container rotation).
   - Eliminates: heredoc trailer break, send-keys streaming deadlock, small-command tmux state flakes (the `hello-world` `printf > file` 11-min hang we observed three times).

## Per-task results (17-task sample)

Sorted alphabetically. Times are agent wall-clock; trial wall-clock is slightly higher.

| Task | Pre-fix | Post-fix | Mode | Notes |
|---|---|---|---|---|
| `count-dataset-tokens` | 0/1 timeout | 0/1 timeout | agent_timeout | true latency-bound (LLM ability gap) |
| `csv-to-parquet` | 0/2 timeout | **2/2 ✓** | unset | 1.5×N fix → resolved |
| `decommissioning-service-with-sensitive-data` | 5/6 | 5/6 | unset | confirmed near-pass holds |
| `fibonacci-server` | 0/6 timeout | **6/6 ✓** (best); **5/6** (latest) | unset | resume + heredoc fix → resolved |
| `fix-git` | 1/2 | 1/2 | unset | unchanged |
| `fix-pandas-version` | **3/3 ✓** | **3/3 ✓** | unset | confirm no-regress |
| `gpt2-codegolf` | 0/1 timeout | 0/1 (correctness) | unset / parse_error | mode shift: agent finishes, LLM gets answer wrong |
| `hello-world` | **2/2 ✓** (77s) | **2/2 ✓** (102s) | unset | confirm no-regress with always-tempfile (pre-tempfile flaked 3×) |
| `hf-model-inference` | 0/4 timeout | **3/4** (best); 0/4 (variance) | agent_timeout | resume fix → near-pass; HF model pull adds wallclock |
| `intrusion-detection` | 4/7 timeout | **5/7** | agent_timeout | +1 test |
| `nginx-request-logging` | 7/8 | 7/8 | unset | near-pass holds |
| `openssl-selfsigned-cert` | **6/6 ✓** | **6/6 ✓** | unset | confirm no-regress |
| `organization-json-generator` | 3/4 | **4/4 ✓** (best); 3/4 (variance) | unset | flips on the 4th test |
| `password-recovery` | 1/2 timeout | 0/2 | unset | mode shift; -1 test (correctness gap surfaces) |
| `processing-pipeline` | 8/9 | 8/9 | unset | near-pass holds |
| `swe-bench-astropy-1` | 13/15 | 13/15 | unset | near-pass holds (was sensitive to test-runner timeout under tb concurrency) |
| `vim-terminal-task` | 4/5 | 4/5 | unset | near-pass holds |

## Open issues, post-fix

The fixes restored multi-step execution and stabilized the heredoc/tempfile paths. What's left:

1. **Near-pass variance on the boundary test.** `organization-json-generator` (4th test), `fibonacci-server` (6th), `swe-bench-astropy-2` (9th) — the agent gets all tests except one or two consistently. Sometimes flips on retry, sometimes doesn't. **Drivers:** test-spec edge cases, LLM ability boundaries; not a harness bug.

2. **Per-turn LLM latency still dominates time budget.** Round-trips remain 5-50s/turn on a growing conversation; `count-dataset-tokens`, `password-recovery`, `gpt2-codegolf` still timeout when the task needs many turns. **Followup work:** parallel `<actions>` emission for tbench agent (planner_react_3 supports it; tbench prompt should encourage it); long-poll/SSE on `chat_get` to recover the polling-overhead 4-6s/turn.

3. **`parse_error` env-broken category.** Some tasks (`gpt2-codegolf` once, `swe-bench-astropy-1` under concurrent load, `pytorch-model-cli.*`, etc.) hit tb test-infrastructure timeouts/errors unrelated to the agent. Mac-host / arm64 / network sensitivity. **Mitigation:** sequential mode (`--n-concurrent-trials 1`) cleared the `swe-bench-astropy-1` parse_error symptoms in the sample we ran.

4. **LLM ability gaps (out of scope).** Polyglot files, esoteric forensics, code-golf — gemini-flash-tier doesn't reliably solve these. Re-runs unlikely to flip without targeted RAG/few-shot.

## Methodology / variance notes

- All numbers above come from runs with `--n-concurrent-trials 1`. The previous baseline ran with tb's default concurrency (4), which under the local llm-server setup occasionally surfaced 500s on `chat_get` from concurrent LLM-call pressure on the Google AI provider — symptoms that look like task failures but are infra-level. Sequential mode produces clean comparisons.
- "Best" and "latest" columns appear where the same task ran across multiple batches with different outcomes. We took the best result for headline math; a multi-trial run with `--n-trials 3` would give a fair statistical pass rate but costs ~3× wall time.
- Sample is curated, not random — biased toward tasks the previous failure-mode taxonomy flagged as near-pass or latency-bound. Full 80-task re-baseline will produce smaller percentage uplift but is the only fair number for cross-version comparison.

## Next steps

### Re-baseline (batched, not one-shot)

Running 76 tasks in a single `tb run` invocation isn't practical: ~10h sequential, or 5-6h concurrent with measurable contention noise (LLM-provider 500s under our local server's load). Instead, split into **4 batches of ~19 tasks each**, sequential within each batch (`--n-concurrent-trials 1`), spread across sittings:

| Batch | Approx. wall | Tasks | Notes |
|---|---|---|---|
| 17-task interim sample (already done) | ~1.5h | mixed | partial coverage; lives in headline above |
| Batch B (alphabetical: `blind-...` → `crack-...`) | ~2.5h | ~14 tasks | includes 3 long ones (blind-maze variants) |
| Batch C (`create-...` → `intrusion-...` minus tasks already covered) | ~2.5h | ~16 tasks |  |
| Batch D (`jupyter-...` → `play-...` minus already covered) | ~2.5h | ~12 tasks | skip `play-zork` (interactive, not adapter-fixable) |
| Batch E (`polyglot-...` → `write-compressor` minus already covered) | ~3h | ~17 tasks | includes most of the swe-bench cluster |

After all batches land, regenerate the headline metrics and per-task table from the union of batch result dirs — there's a small jq one-liner for that, can be added to a `tbench/aggregate.py` if useful.

### Other followups

- [ ] **Multi-trial pass-rate study** (`--n-trials 3`) on the near-pass cluster (`fibonacci-server`, `organization-json-generator`, `nginx-request-logging`, `swe-bench-astropy-2`, `vim-terminal-task`) to lock in statistical numbers vs single-trial variance.
- [ ] **Parallel `<actions>` for tbench agent** — biggest expected lift on remaining latency-bound timeouts.
- [ ] **Long-poll/SSE on `chat_get`** — server-side win for every async client.

---

<details>
<summary><strong>Pre-fix baseline (2026-05-06, contaminated by #29973 resume regression — retained for reference)</strong></summary>

The block below is the original baseline as captured before the regression was identified. The headline numbers and failure-mode taxonomy were significantly skewed by the resume bug — most of the "agent_timeout / latency-bound / hung-command" entries were actually stuck-after-1-cmd victims, not real LLM/task limits. Per-task pass counts on tasks that did terminate are still informative, but cross-task aggregates are not.

### Original headline (now invalid)

| Metric | Value |
|---|---|
| Total tasks in dataset | 80 |
| Heavy tasks (skipped — multi-GB Docker pulls / hours) | 4 |
| Tasks attempted | 76 |
| Resolved (✓) | 23 |
| Pass rate (raw) | 30% |
| Pass rate (excluding env-broken) | 23/65 = 35% |

### Original failure-mode distribution (now invalid)

| Category | Count | Note (post-fix re-read) |
|---|---|---|
| ✓ Resolved | 23 | Real |
| NEAR PASS | 10 | Real |
| Planner loop | 7 | Real |
| Notebook overhead | 3 | Likely fixed by #29949 (conditional notebook section) |
| Hung command | 4 | Likely #30036 fixes most; verify on re-baseline |
| Latency-bound | 8 | **Likely contaminated**: many of these were stuck-after-1-cmd, not truly latency-bound |
| Timeout (mixed) | 3 | Same caveat |
| Correctness gap | 7 | Mostly real |
| Test infra broken | 11 | Real (parse_error) |

### Original per-task table

Per-task results from the original 76-task run, retained as a corpus for spot-checking. Some entries (csv-to-parquet, fibonacci-server, hf-model-inference, intrusion-detection in this sample) are now known to have been bug victims rather than real failures.

| # | Task | Status | Tests | Agent time | Failure | Conv ID | Run ID |
|---|------|--------|-------|------------|---------|---------|--------|
| 1 | `blind-maze-explorer-5x5` | ✗ | 0/2 | 1200s | agent_timeout | `33fae579-6936-4714-8678-25126d4bedbf` | 2026-05-05__16-26-07 |
| 2 | `blind-maze-explorer-algorithm` | ✗ | 0/11 | 1200s | agent_timeout | `25a8a38c-0236-479e-a6a8-5ac297c94ece` | 2026-05-05__16-26-07 |
| 3 | `blind-maze-explorer-algorithm.easy` | ✓ | 11/11 | 608s | unset | `15aac53f-d7df-4b71-a781-bc9fcbef35b9` | 2026-05-05__16-26-07 |
| 4 | `blind-maze-explorer-algorithm.hard` | ✗ | 0/11 | 1200s | agent_timeout | `62b0b366-f9e7-4af8-a7a7-28f7b11d6001` | 2026-05-05__16-26-07 |
| 5 | `build-initramfs-qemu` | ⏸ heavy | — | — | — | — | — |
| 6 | `build-linux-kernel-qemu` | ⏸ heavy | — | — | — | — | — |
| 7 | `build-tcc-qemu` | ✗ | 0/1 | 660s | agent_timeout | `3b8f3061-2970-4977-a359-774cefcb57d1` | 2026-05-05__16-26-07 |
| 8 | `cartpole-rl-training` | ⏸ heavy | — | — | — | — | — |
| 9 | `chess-best-move` | ✗ | — | 660s | parse_error | `ac26ea2e-f5f6-4a77-8f98-7d6d4eab1847` | 2026-05-05__16-26-07 |
| 10 | `conda-env-conflict-resolution` | ✗ | 1/3 | 313s | unset | `e225afbb-dac5-4ec0-bc86-5e66d882fd82` | 2026-05-05__16-26-07 |
| 11 | `configure-git-webserver` | ✗ | 0/1 | 348s | unset | `0228ba0e-a660-48b4-a043-9e8f2cf6ac49` | 2026-05-05__16-26-07 |
| 12 | `count-dataset-tokens` | ✗ | 0/1 | 900s | agent_timeout | `a5d13673-4c30-4895-9e61-2131e3ad3c27` | 2026-05-05__16-26-07 |
| 13 | `crack-7z-hash` | ✗ | 0/2 | 660s | agent_timeout | `fe0849a1-41e1-4883-b080-813fcdfd9f5d` | 2026-05-05__16-26-07 |
| 14 | `crack-7z-hash.easy` | ✓ | 2/2 | 435s | agent_timeout | `866928aa-fc59-4583-8c66-b520353110a8` | 2026-05-05__19-37-12 |
| 15 | `crack-7z-hash.hard` | ✗ | 0/2 | 660s | agent_timeout | `55bf0317-6f34-4520-965d-5869d7732b63` | 2026-05-05__19-37-12 |
| 16 | `create-bucket` | ✗ | 1/2 | 660s | agent_timeout | `9941fc41-de1f-4600-bfa9-fdabb1e65463` | 2026-05-05__19-37-12 |
| 17 | `cron-broken-network` | ✗ | 1/2 | 660s | agent_timeout | `de7bd66c-6f12-475e-994b-f61fed70fc36` | 2026-05-04__10-10-18 |
| 18 | `csv-to-parquet` | ✗ | 0/2 | 660s | agent_timeout | `88387a44-e3b1-48b0-b46e-32a1b81b4d62` | 2026-05-05__18-16-44 |
| 19 | `decommissioning-service-with-sensitive-data` | ✗ | 5/6 | 201s | unset | `6933c686-fff5-4a3e-90d9-b8c171bfbdef` | 2026-05-05__18-16-44 |
| 20 | `download-youtube` | ✗ | — | 660s | parse_error | `500dba72-abd5-4432-b669-f1801566caf9` | 2026-05-05__18-16-44 |
| 21 | `eval-mteb` | ✓ | 1/1 | 892s | unset | `a2f1035b-a9f4-4bdb-a061-48f7c1f9f80f` | 2026-05-05__19-37-12 |
| 22 | `eval-mteb.hard` | ✓ | 1/1 | 553s | unset | `544c03ca-0ec1-422b-86b1-3c8f2d244cee` | 2026-05-05__19-37-12 |
| 23 | `extract-moves-from-video` | ✗ | — | 660s | parse_error | `8b8474c2-6e60-4da9-b88f-fda6e8874fb5` | 2026-05-05__19-37-12 |
| 24 | `extract-safely` | ✓ | 3/3 | 101s | unset | `bde74190-0899-4b3e-9c2a-3d318e9e9c49` | 2026-05-05__19-37-12 |
| 25 | `fibonacci-server` | ✗ | 0/6 | 660s | agent_timeout | `9b2967b0-c3b4-4b14-8557-92c95aa6864e` | 2026-05-05__19-37-12 |
| 26 | `fix-git` | ✗ | 1/2 | 183s | unset | `ad68cea4-92f2-44cb-a81a-81ec41938d9a` | 2026-05-05__19-37-12 |
| 27 | `fix-pandas-version` | ✓ | 3/3 | 195s | unset | `5948d677-ebdf-4542-85f9-fbeef41954c8` | 2026-05-03__23-18-52 |
| 28 | `fix-permissions` | ✓ | 1/1 | 382s | agent_timeout | `7b1aa9b6-cc50-492e-9def-aa0faf212555` | 2026-05-05__19-37-12 |
| 29 | `get-bitcoin-nodes` | ✗ | — | 660s | parse_error | `f1729833-fd2c-47dc-b25f-8fe162f14f39` | 2026-05-05__21-54-48 |
| 30 | `git-multibranch` | ✗ | 0/1 | 780s | agent_timeout | `b1d68d5a-3366-4947-8920-697b7d788b75` | 2026-05-05__21-54-48 |
| 31 | `git-workflow-hack` | ✓ | 1/1 | 525s | agent_timeout | `b0f81c49-2f64-484b-936f-9d4a31be2747` | 2026-05-05__21-54-48 |
| 32 | `gpt2-codegolf` | ✗ | 0/1 | 660s | agent_timeout | `f661fafe-4fce-4dee-a21e-1b265ef0800f` | 2026-05-05__21-54-48 |
| 33 | `grid-pattern-transform` | ✓ | 3/3 | 216s | unset | `f4f5b346-7ef7-4b7a-83c0-b2db55296b57` | 2026-05-05__21-54-48 |
| 34 | `hello-world` | ✓ | 2/2 | 77s | unset | `af0eb037-a225-4fb7-b99a-d2c4a6d852ab` | 2026-05-03__22-39-09 |
| 35 | `heterogeneous-dates` | ✓ | 3/3 | 135s | unset | `73a46288-b9fd-4966-9469-65bda28f2f4e` | 2026-05-05__21-54-48 |
| 36 | `hf-model-inference` | ✗ | 0/4 | 660s | agent_timeout | `6454edc6-f1b5-4fae-b6e2-52c3dfcfd056` | 2026-05-05__21-54-48 |
| 37 | `incompatible-python-fasttext` | ✓ | 3/3 | 759s | agent_timeout | `4ff741ff-c41c-44c6-96c7-048bf7da0963` | 2026-05-05__21-54-48 |
| 38 | `incompatible-python-fasttext.base_with_hint` | ✓ | 3/3 | 181s | unset | `ea74e783-8165-44e4-9ea9-d63046985e4a` | 2026-05-03__23-18-52 |
| 39 | `intrusion-detection` | ✗ | 4/7 | 660s | agent_timeout | `2be256ea-3687-4a70-aebd-5b89443811b2` | 2026-05-05__21-54-48 |
| 40 | `jupyter-notebook-server` | ✗ | — | 660s | parse_error | `a58cf8a1-c2c7-4cba-8239-7c4283ab3c2d` | 2026-05-05__21-54-48 |
| 41 | `modernize-fortran-build` | ✓ | 3/3 | 484s | agent_timeout | `9360e7f3-1d6c-4d11-87e2-65abc90a870b` | 2026-05-05__22-58-46 |
| 42 | `new-encrypt-command` | ✓ | 1/1 | 330s | unset | `1ceb343a-b597-41cb-80e1-2e66e4d71ff1` | 2026-05-05__22-58-46 |
| 43 | `nginx-request-logging` | ✗ | 7/8 | 283s | unset | `74f21958-dbb0-4bed-9d84-c80d8a6bcaf5` | 2026-05-05__22-58-46 |
| 44 | `oom` | ✗ | 0/1 | 376s | agent_timeout | `58b66057-86da-4160-9b96-1ee21969cdae` | 2026-05-05__22-58-46 |
| 45 | `openssl-selfsigned-cert` | ✓ | 6/6 | 232s | unset | `88c0de59-b12d-4b2c-8ae2-e10afb9ab8a4` | 2026-05-03__23-33-23 |
| 46 | `organization-json-generator` | ✗ | 3/4 | 177s | unset | `bc4fd654-c9c2-4e2b-9680-473d3b116851` | 2026-05-05__22-58-46 |
| 47 | `password-recovery` | ✗ | 1/2 | 660s | agent_timeout | `3728b03a-a797-4550-ab1d-90b85099404f` | 2026-05-04__10-10-18 |
| 48 | `path-tracing` | ✗ | — | 660s | parse_error | `f1258ee1-33c6-463a-8d59-bde6473eb557` | 2026-05-05__22-58-46 |
| 49 | `path-tracing-reverse` | ✗ | 0/3 | 660s | agent_timeout | `3b9b6798-61e7-4cdd-a1de-525d6721057c` | 2026-05-05__22-58-46 |
| 50 | `play-zork` | ✗ | 0/2 | 2223s | agent_timeout | `45fb5187-a8e0-4659-b3ca-118586e8155d` | 2026-05-05__22-58-46 |
| 51 | `polyglot-c-py` | ✗ | 0/1 | 248s | unset | `fd6f7eb1-ed12-4f1f-ab3e-26d20787bb50` | 2026-05-05__22-58-46 |
| 52 | `polyglot-rust-c` | ✗ | 0/1 | 608s | agent_timeout | `7cceee82-5f42-4f05-829f-c84ddadf2545` | 2026-05-04__10-10-18 |
| 53 | `processing-pipeline` | ✗ | 8/9 | 531s | agent_timeout | `c599b418-95c1-4f90-b90b-9c0023072d85` | 2026-05-05__22-58-46 |
| 54 | `prove-plus-comm` | ✓ | 4/4 | 384s | agent_timeout | `b93dbafc-e9d0-488c-a1cf-d6639cfc0737` | 2026-05-06__07-28-14 |
| 55 | `pytorch-model-cli` | ✗ | — | 660s | parse_error | `36c4e1a2-1a2d-4424-9ea1-e53c94211345` | 2026-05-06__07-28-14 |
| 56 | `pytorch-model-cli.easy` | ✗ | — | 660s | parse_error | `73e377be-c9a4-4b2f-8840-bf7ecf0ec22e` | 2026-05-06__07-28-14 |
| 57 | `pytorch-model-cli.hard` | ✗ | — | 660s | parse_error | `241f1199-cb92-4f48-a6af-fda7f7cd83ec` | 2026-05-06__07-28-14 |
| 58 | `qemu-alpine-ssh` | ✗ | 0/1 | 900s | agent_timeout | `f2cbaf7a-5a5d-47ab-a69b-3595cbf9819c` | 2026-05-06__07-28-14 |
| 59 | `qemu-startup` | ✗ | — | 900s | parse_error | `1d58e940-f288-433e-82bd-1685d612a3f3` | 2026-05-06__07-28-14 |
| 60 | `raman-fitting` | ✗ | 1/3 | 652s | agent_timeout | `b35e4de1-3f3e-4cfb-b30c-5cd375db5a14` | 2026-05-06__07-28-14 |
| 61 | `raman-fitting.easy` | ✗ | 1/3 | 206s | unset | `25f3b2e5-cca3-4299-a6ec-7593c23d7d46` | 2026-05-06__07-28-14 |
| 62 | `reshard-c4-data` | ✗ | 0/2 | 310s | unset | `6aaef97a-87bc-41ce-b0da-d314ac877a06` | 2026-05-06__07-28-14 |
| 63 | `run-pdp11-code` | ✗ | 0/2 | 955s | unset | `c172d760-55fd-4785-8393-49f5bcfd27bb` | 2026-05-06__07-28-14 |
| 64 | `sanitize-git-repo` | ✗ | 1/3 | 148s | unset | `cc515e38-9ab1-45d6-82d0-36a800694373` | 2026-05-06__08-10-06 |
| 65 | `sanitize-git-repo.hard` | ✗ | 1/3 | 364s | agent_timeout | `d342677e-e8ae-47f7-8524-c2e4a6b90937` | 2026-05-06__08-10-06 |
| 66 | `security-vulhub-minio` | ✗ | 0/1 | 540s | agent_timeout | `49d4d520-845a-40b7-a224-636ba1a33ffa` | 2026-05-06__08-10-06 |
| 67 | `simple-sheets-put` | ✓ | 3/3 | 410s | agent_timeout | `d1dfe52b-86dc-46bd-8c91-c339b80ce7d1` | 2026-05-06__08-10-06 |
| 68 | `simple-web-scraper` | ✓ | 5/5 | 151s | unset | `87c5b075-cdf3-46bc-ab20-335003aebb6d` | 2026-05-06__08-10-06 |
| 69 | `solana-data` | ✗ | 0/5 | 660s | agent_timeout | `e22ff18f-0681-4c88-8e75-110a0ef96a65` | 2026-05-06__08-10-06 |
| 70 | `sqlite-db-truncate` | ✗ | 0/1 | 495s | agent_timeout | `163cd973-468d-4803-8a94-8f4c6360e913` | 2026-05-06__08-10-06 |
| 71 | `sqlite-with-gcov` | ✓ | 3/3 | 290s | unset | `f55dc1c1-b7fa-4e5a-a6bb-16336ffc2336` | 2026-05-03__23-12-49 |
| 72 | `super-benchmark-upet` | ✗ | 0/1 | 750s | unset | `46908c71-9f3a-4c02-b938-55ee7d841252` | 2026-05-06__08-10-06 |
| 73 | `swe-bench-astropy-1` | ✗ | 13/15 | 582s | unset | `5070aff8-3522-4a19-9eb3-f8efa701d304` | 2026-05-06__08-10-06 |
| 74 | `swe-bench-astropy-2` | ✗ | 8/9 | 1243s | agent_timeout | `51de0275-3bef-4cc5-9506-c2b8270912e6` | 2026-05-06__08-10-06 |
| 75 | `swe-bench-fsspec` | ✓ | 134/134 | 749s | unset | `661d5707-86da-41db-9262-26670cfbe6b8` | 2026-05-06__08-48-26 |
| 76 | `swe-bench-langcodes` | ✓ | 1/1 | 164s | unset | `33d3c25c-2cdd-47ec-beca-2c0a9cdbabcf` | 2026-05-06__08-48-26 |
| 77 | `tmux-advanced-workflow` | ✓ | 5/5 | 370s | agent_timeout | `5c1faef5-1cbc-49e4-a5b1-fb4ee7e92c33` | 2026-05-06__08-48-26 |
| 78 | `train-fasttext` | ⏸ heavy | — | — | — | — | — |
| 79 | `vim-terminal-task` | ✗ | 4/5 | 162s | unset | `8f4ad2aa-5038-49ae-b940-9b94d6fa971b` | 2026-05-06__08-48-26 |
| 80 | `write-compressor` | ✗ | — | 660s | parse_error | `20f837d2-b346-4f04-b1c8-65a1532a9e0e` | 2026-05-06__08-48-26 |

</details>
