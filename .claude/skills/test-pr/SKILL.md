---
description: Investigate a pull request end-to-end — predict how it could be wrong, run it for real, falsify those predictions with evidence, and report by severity
user-invocable: true
allowed-tools:
  - Bash
  - Read
  - Edit
  - Write
  - Glob
  - Grep
  - Task
---

# Test a Pull Request

Investigate the pull request specified by `$ARGUMENTS` (a PR number or URL) and decide whether the change actually works — and where it might not.

**This is an investigation, not a test run.** Behave like a skeptical senior engineer trying to *prove the implementation wrong* before accepting it, not an automated runner confirming the happy path. The mental model is:

1. **Understand** the change and where it sits in the system.
2. **Predict** how it could fail.
3. **Design experiments to falsify** those predictions.
4. **Collect evidence** by running it for real.
5. **Reach a conclusion**, reported by severity.

This skill is **methodology, not a checklist.** Read the change, form hypotheses about how it's wrong, then design the tests that would catch it — executed against the repository's own conventions.

This overlaps in intent with other review-oriented skills (static diff review, code-judging). Its distinct value is going further than any of them: actually bringing the affected service(s) up and exercising the changed path end-to-end, with evidence collected from the running system rather than from reading the diff alone. Prefer those other skills for pure static/diff-level review; use this one when the change has runtime behavior worth proving.

Core principles (apply throughout):

- **Investigate, don't confirm.** Do not assume the implementation is correct because it looks reasonable. Actively search for assumptions that may be false and test those *first*. A test that only walks the expected path proves almost nothing.
- **Discover, never assume.** Derive every build/lint/test/run command from the repo itself (Makefiles, `package.json` scripts, `.github/` workflows, `CLAUDE.md`/service docs, `pyproject.toml`, `go.mod`). Never invent a command.
- **Default to end-to-end.** If the change has any runtime-observable behavior, *actually run it* — real services up, exercised through its real entry point, effect confirmed at the sink, logs read. "It compiles and units pass" is not "it works." Only skip the live run for changes with no runtime effect (docs, comments, pure-internal refactors with existing coverage) — and say so.
- **Evidence over assertion.** Every claim in the report is backed by output you actually saw. Never claim a pass you didn't observe.
- **Proportional depth.** Scale effort to blast radius, but bias toward running the real thing. Don't over-test a typo; never claim a migration or a new cross-service behavior works without running it.
- **Deterministic and re-runnable.** Prefer commands that reproduce; capture IDs, baselines, and log paths so any step can be repeated or resumed.

## Step 0: Validate arguments

If `$ARGUMENTS` is empty, stop and ask for a PR number or URL. Usage: `/test-pr 123`. Do not fall back to the current branch. Confirm `gh auth status`; if unauthenticated, stop and tell the user.

## Step 1: Get the PR into an isolated workspace

Fetch metadata and check the head out in a **git worktree** so the user's tree is untouched:

```bash
REPO_ROOT=$(git rev-parse --show-toplevel)
# Normalize $ARGUMENTS (a PR number OR a full URL) to a clean integer, so the
# snippet is directly runnable and the worktree path stays slash/colon-free.
PR=$(gh pr view "$ARGUMENTS" --json number -q .number)
gh pr view "$PR" --json number,title,body,state,baseRefName,headRefName,additions,deletions,changedFiles,author,url
BASE_BRANCH=$(gh pr view "$PR" --json baseRefName -q .baseRefName)
WT="${REPO_ROOT}-testpr-${PR}"
BRANCH="testpr-${PR}"
# Fetch by PR ref, not by head-branch name — the head branch only exists on
# `origin` for same-repo PRs; fork PRs 404 there. `pull/$PR/head` resolves
# either way (the same primitive `gh pr checkout` uses under the hood).
git fetch origin "pull/${PR}/head:${BRANCH}"
git worktree add "$WT" "$BRANCH" 2>/dev/null || git worktree add "$WT" "$BRANCH" --force
echo "WT=$WT BASE=$BASE_BRANCH"
```

Run all file/build/test operations inside `$WT`. Remember `$BASE_BRANCH` and the merge-base for failure triage.

## Step 2: Understand intent, map the change, and estimate blast radius

- Read the PR **title, body, and linked issue** (`Fixes #N`) — the claimed behavior and any acceptance criteria.
- Read the **full diff** (`gh pr diff $PR`). For each hunk, ask what observable behavior it adds/changes/removes.
- **Classify the change** (usually several at once): pure logic/refactor · new/changed behavior · API/contract · DB schema/migration · cross-service · stateful side-effect · UI · config/build/infra · dependency bump · docs.
- **Locate the change in the system before designing any test.** For each changed symbol/file, identify:
  - **upstream callers** (who invokes this),
  - **downstream consumers** (what depends on its output/contract),
  - **shared interfaces/types** it touches,
  - **feature flags** gating it,
  - **configuration dependencies** it reads.
  Estimate the **blast radius** — that, not a fixed rule, sets test depth.

**Parallelize large or multi-service PRs.** Dispatch independent `Task` agents concurrently and synthesize their results yourself — e.g. Task A: map architecture (callers/consumers of the changed symbols); Task B: discover the toolchain (Step 4); Task C: read the relevant service `CLAUDE.md`/docs; Task D: summarize what each changed component does. Keep the hypotheses and testing decisions in the main thread.

## Step 3: Predict how it could be wrong (do this *before* designing tests)

Ask outright: **"Can this implementation be wrong?"** Generate several concrete hypotheses for how it could be incorrect, then design tests intended to **disprove** them — not to reconfirm the expected path. Cover at least:

- **Edge cases** — empty/null/zero, boundaries, very large inputs, unicode, duplicates, concurrent callers.
- **Regressions** — what *adjacent* behavior could this accidentally break? Name the adjacent behaviors most likely to regress and plan to verify they still work.
- **Race conditions / ordering / idempotency** — concurrent or repeated invocation, async workers, retries.
- **Missing validation** — untrusted input reaching a trust boundary unchecked.
- **Broken assumptions** — what does the code assume that may not hold (a field always present, a lookup never failing, a single writer)?
- **Partial implementation** — one branch/caller/path handled, siblings left broken; a flag added but not honored everywhere.
- **Error/failure paths** — what happens when a dependency errors, times out, or returns empty.

Then two staff-engineer moves:

- **Invariants — "what must NEVER change?"** Identify the properties this change must preserve and verify them explicitly. E.g. auth change → permission checks still hold; serialization change → backward compatibility holds; DB migration → old rows still load; retry/queue change → operation stays idempotent; API change → existing clients still parse the response.
- **"If I were the author trying to hide a bug, where would it be?"** Prioritize testing exactly those spots — the untested branch, the silent fallback, the "shouldn't happen" path, the place the diff is conspicuously thin.

Output a short, ranked list of risks/hypotheses. This list drives Step 5.

## Step 4: Discover the repository's toolchain

For every affected component, find *from the repo* how to **build/typecheck**, **lint/format** (match what `.github/workflows/*` actually runs), **unit/integration test** (and how to run a *single* changed package/test), and — when a live run is warranted — **run the service** and what it depends on. Prefer the repo's own aggregate command (`make validate`, `npm run lint2 && npm run test`) as its definition of "good."

## Step 5: Design the experiments and run them

Choose the **minimum set of tiers that falsifies your Step-3 hypotheses and exercises the change's observable behavior.** Justify inclusion/exclusion in the report.

1. **Static** — build/typecheck + lint/format on affected components.
2. **Automated tests** — run the affected packages. **Inspect the tests, don't just run them:** do the existing and newly-added tests actually exercise the *changed behavior* (and the edge cases from Step 3), or do they merely raise coverage superficially (asserting nothing meaningful, mocking away the thing under test, only walking the happy path)? Non-trivial logic (branch, loop, parser, money/security/auth path) with no real test is a finding even when the suite is green. Add or run targeted checks for hypotheses the suite doesn't cover.
3. **Dynamic / runtime verification** — the **default** for any runtime effect. Run the affected service(s), exercise the exact changed path via its real entry point (API call, CLI, event), and **observe the effect directly** at its sink (DB row, response body, emitted message, log line, file). **Read the service logs** as you go — primary evidence the path executed, and the first place to look when it didn't. For stateful effects, **baseline → act → diff**; don't eyeball. Explicitly exercise the falsification tests and the invariants from Step 3, not just the happy path.
4. **Manual / UI verification** — when correctness is visual/interactive or only an end-to-end run proves it. Browser automation only if it's the right tool and available.

### Bringing up services (for tiers 3–4)

- **Follow the repository's documented local-development topology exactly** if it has one (e.g. a `CLAUDE.md` "Required Services" / "Local Development" section, a `docker-compose`, a dev script). Do not invent your own. Read that service's `CLAUDE.md` before running it.

  > Example: for a change to an inference/backend service, the documented topology is often "port-forward the dependencies it calls (its API/services server, a relay, a RAG/vector service, redis, a database) and run the changed service locally pointed at those." Take the actual service list, ports, and commands from the repo, not from this example.

- **Map the runtime dependency graph** and start only what the tested path needs — the changed service plus its required upstreams/downstreams/datastores.
- **Reuse an already-running stack** if one is up (check expected ports); start only what's missing. Duplicates cause port conflicts and split state.
- **Env vars: process environment ≠ config file.** A `.env` loaded by the app's config layer isn't always visible to code reading the OS env directly (`os.Getenv`/`os.environ`). If a value is in `.env` but the service reports it "not set," export it into the launching process's environment, and verify it actually loaded.
- **Wire services to each other** — point the service-under-test at *local* instances of its dependencies so you exercise the changed code, not a stale remote copy.
- **Edit `.env` / local config as needed — deliberately and reversibly.** Adjusting local config to wire services or unlock the changed path is expected. Read the current value first, note the original, make the minimal edit, and **restore it during cleanup**. Never commit these; never edit config outside the test workspace without saying so.
- **Launch in the background, log to a file, wait for readiness** (health endpoint / "listening" log; build first for compiled services). **Restart only the service(s) whose code changed** after checking out a new head, so you test the new binary. Keep each log path.

### Guardrails while exercising behavior

- **Never trigger irreversible or outward-facing actions to "test" them** (deleting real data, sending real notifications, third-party/production side effects, spending money) unless the user explicitly authorized that specific action. Prefer the safe branch (reject over approve, throwaway over real record). Flag anything skipped for safety.
- **Isolate test data** — create identifiable throwaway records/sessions; don't mutate data you didn't create.
- **Beware non-determinism** (LLM calls, async workers, races) — poll for the settled state; don't conclude "broken" from one flaky run.

## Step 6: Baseline, then execute

For stateful changes, record the "before" (row counts, current values, output) so the "after" is provable. Run the chosen tiers, capturing concrete evidence per check: command + relevant output, query result, status code, IDs. Keep artifacts (IDs, baselines, log paths) in the scratchpad for resumability.

## Step 7: Triage every failure — introduced, pre-existing, or environmental?

A failure is only a PR finding if the PR caused it.

- **Reproduce on the base** — run the same command on the merge-base / `$BASE_BRANCH` (a second worktree is cheapest). Fails there too → **pre-existing** (report as context). Passes there → **introduced by the PR** (real finding).
- **Separate environment/harness problems** (missing local env var, unconfigured dependency, port conflict, flaky async, your own test-script bug) from PR defects. Fix or route around them and re-run; say plainly which failures were environmental.
- Find the **root cause**, not the symptom — read the code path, not just the error line.

Never report a failure you haven't classified.

## Step 8: Assess quality, not just correctness

If the change emits data/output a human or system consumes (audit record, API payload, UI element, log format), judge whether it's *good*: coherent, complete, and useful for its consumer; field semantics consistent with sibling cases; nothing mislabeled, double-encoded, dropped, or ambiguous. These "works but the data is wrong/unhelpful" issues pass a green build and still ship a bad feature.

## Step 9: Report — by severity

Produce a concise, skimmable, evidence-dense report:

- **Verdict** — one line: does the PR do what it claims? (Pass / Pass with findings / Fails / Blocked) — **plus a confidence level (High / Medium / Low).**
- **Confidence — how much of the risk space you actually exercised.** Assign High / Medium / Low based on the evidence collected, not on the absence of failures:
  - **High** requires *direct* verification of the critical runtime behavior *and* its key invariants (Step 3) — you ran it, saw the effect, and confirmed what must-never-change still holds.
  - **Reduce confidence** (and say so, per hypothesis) whenever important hypotheses couldn't be tested — missing infrastructure, unavailable dependencies, safety constraints, non-determinism, or environmental limits.
  - **Do not confuse "no failures found" with "high confidence."** Absence of evidence is not evidence of absence: state explicitly which parts of the risk space you exercised and which you didn't. A clean run over half the hypotheses is Medium at best.
- **Intent** — one line on what the PR is supposed to do.
- **Blast radius** — the callers/consumers/flags/config the change reaches.
- **Hypotheses tested** — the Step-3 risks and invariants, each marked upheld or violated (this is what makes the pass credible).
- **What was tested** — the tiers run and *why those* (and why others were skipped); the concrete checks.
- **Evidence** — for each key behavior/invariant, the proof (command + result, before/after, status code, row). Compact.
- **Findings — grouped by severity**, most-severe first:
  - **Critical** — data loss, security hole, broken invariant, crash on a real path.
  - **High** — incorrect behavior on a plausible path, regression in adjacent behavior.
  - **Medium** — wrong/misleading output, missing validation, coverage gap on non-trivial logic.
  - **Low** — minor correctness/robustness nits.
  - **Suggestion** — quality/clarity improvements.
  Each finding: what's wrong, why it matters, `file:line`, and whether it's **introduced** vs **pre-existing/environmental**.
- **Risks & follow-ups** — residual risk, anything untested and why (skipped for safety, env you lacked), recommended next actions.

Keep every claim tied to evidence. If you couldn't verify something, say so rather than implying a pass. Post to the PR (`gh pr comment $PR -F <file>`) only if the user asks — it's outward-facing.

## Cleanup (always, finally-style)

**Always attempt cleanup, even if the investigation aborts on an error** — don't exit on the first fatal failure leaving state behind, and don't force your way past it either. In order:

1. Stop services you started.
2. **Restore every `.env`/config value you changed first**, from the backup you noted, *before* touching the worktree.
3. Remove the worktree **without `--force`** (`git worktree remove "$WT"`). A clean, restored worktree removes cleanly. If Git refuses because something is still dirty, that's a signal something wasn't restored — go find and restore it, don't paper over it with `--force`. Only reach for `--force` as a last resort after you've confirmed by hand (`git -C "$WT" status`) that anything left dirty is disposable (e.g. build artifacts you created), never blind, and say so in the report.

Skip this ordering only if the user asked to keep the environment up — then just report what's left running and unrestored.
